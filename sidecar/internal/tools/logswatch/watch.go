// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logswatch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// Why a watch ended, in its header's words, when the follow did not say.
const (
	byLines  = "it reached its line limit"
	byBytes  = "it reached its byte limit"
	byTime   = "its time ran out"
	byUser   = "the user stopped it"
	byApp    = "Kstack quit"
	byModel  = "it was stopped with TaskStop"
	byUnread = "the logs could not be read"
)

var _ tools.Watch = (*watch)(nil)

// watch is a tools.Watch over one follow, whose context is the watch's own: its
// deadline is the watch's time limit, and cancel ends it on a fill or a stop. in
// is the call, view what it resolved to, now the clock the window's end is read
// off.
type watch struct {
	ctx    context.Context
	cancel context.CancelFunc
	stream *cluster.Stream[logs.FollowFrame]
	in     input
	view   tools.LogsViewInput
	now    func() time.Time

	// Set by Wait alone, read by End after it: the lines held, as the model reads
	// them, and their size; full, why the watch filled, "" until it does; reason,
	// the follow's own end, "" with none; failed, a follow that ended on an error;
	// endedAt, when the follow let go.
	recs    []logs.Record
	size    int
	full    string
	reason  string
	failed  bool
	endedAt time.Time
}

// Wait reads the follow until it closes, holding each line until the watch is
// full. A cancelled follow closes with no frame and no error, so a fill, a stop and
// the deadline all close it the same way: End tells them apart. The follow orders
// lines per container only, so the lines held are sorted once it is done.
func (w *watch) Wait() {
	defer w.cancel()
	for f := range w.stream.Frames {
		switch {
		case f.Type == logs.FollowLine && f.Record != nil && w.full == "":
			w.hold(*f.Record)
		case f.Type == logs.FollowEnded:
			w.reason = strings.TrimSpace(logs.ForModel(f.Reason))
		}
	}
	w.failed = w.stream.Err() != nil
	w.endedAt = w.now()
	slices.SortStableFunc(w.recs, logs.CompareLines)
}

// hold keeps rec as the model reads it, through logs.ForModel and cut, and ends the
// follow once the lines reach either limit, keeping the line that reached it.
func (w *watch) hold(rec logs.Record) {
	rec.Message = logsinput.CutLine(logs.ForModel(rec.Message))
	w.recs = append(w.recs, rec)
	w.size += len(logsinput.LineText(rec)) + 1
	switch {
	case len(w.recs) >= w.in.limit:
		w.full = byLines
	case w.size >= w.in.maxBytes:
		w.full = byBytes
	default:
		return
	}
	w.cancel()
}

// Stop ends the follow.
func (w *watch) Stop() { w.cancel() }

// End is how the watch ended. A fill, the follow's own end and its deadline each
// close the stream on their own, so each is read before a stop: one that lands
// while the stream drains changes nothing. A watch holding lines is matched
// whatever ended it; failed means it holds none.
func (w *watch) End(stoppedBy string) tools.WatchEnd {
	status := tools.WatchMatched
	var because string
	switch {
	case w.full != "":
		because = w.full
	case w.reason != "" || w.failed:
		because = w.reason
		if w.failed {
			because = byUnread
		}
		if len(w.recs) == 0 {
			status = tools.WatchFailed
		}
	case errors.Is(w.ctx.Err(), context.DeadlineExceeded):
		because = byTime
		if len(w.recs) == 0 {
			status = tools.WatchExpired
		}
	default:
		status, because = tools.WatchStopped, stopText(stoppedBy)
	}
	r := tools.WatchResult{Text: w.text(status, because), Lines: []tools.LogsReadLine{}, EndedBecause: because}
	for _, rec := range w.recs {
		r.Lines = append(r.Lines, logsinput.ReadLine(rec))
	}
	if len(w.recs) > 0 {
		r.ViewAround = logsinput.ViewAround(w.view, w.recs[0].Timestamp, w.endedAt)
	}
	return tools.WatchEnd{Status: status, Result: r}
}

// stopText is a stop as the header says it, by who stopped it.
func stopText(by string) string {
	switch by {
	case llm.TaskStoppedByUser:
		return byUser
	case llm.TaskStoppedByApp:
		return byApp
	}
	return byModel
}

// text is the watch as the model reads it, a LogsRead sample: a header naming the
// sources, the pattern, the window and why it ended, then one line per match,
// oldest first. The header is cluster text, redacted as a read's is; the cursors
// to read around the lines go after the redaction, which could change them.
func (w *watch) text(status tools.WatchStatus, because string) string {
	sources := logsinput.SourcesText(w.in.Sources)
	window := logsinput.Stamp(w.in.since)
	if len(w.recs) == 0 {
		head := "No line of " + sources + " was written"
		if w.in.Grep != "" {
			head = "No line of " + sources + " matched /" + w.in.Grep + "/"
		}
		head += " between " + window + " and " + logsinput.Stamp(w.endedAt)
		if status != tools.WatchExpired {
			head += "; the watch ended because " + because
		}
		return safe.Redact(head + ".")
	}
	var b strings.Builder
	if n := len(w.recs); n == 1 {
		b.WriteString("Watched 1 line of " + sources)
	} else {
		fmt.Fprintf(&b, "Watched %d lines of %s", n, sources)
	}
	if w.in.Grep != "" {
		b.WriteString(" matching /" + w.in.Grep + "/")
	}
	b.WriteString(" from " + window + " until " + logsinput.Stamp(w.endedAt) + ", ended because " + because + ".")
	lines := []string{safe.Redact(b.String())}
	for _, rec := range w.recs {
		lines = append(lines, logsinput.LineText(rec))
	}
	return strings.Join(lines, "\n")
}
