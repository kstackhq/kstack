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

package logsread

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// A sample's caps, in bytes: one message, the lines together, and the header's
// notes and skipped streams. With the header's fixed part they keep the output
// under tools.InlineLimit, so it is never saved to a file.
const (
	maxSampleLine = 2 << 10
	sampleBudget  = 16 << 10
	notesBudget   = 4 << 10
	skippedBudget = 2 << 10
)

// sample is a page as the model reads it: the records kept, oldest first, their
// messages stripped, redacted and cut; the records the budget dropped; what lies
// past each end; the cursor to read on from, "" for none; and each stream the page
// could not read.
type sample struct {
	records       []logs.Record
	dropped       int
	before, after tools.LogsReadEdge
	next          string
	skipped       []string
}

// sampleOf is in's page as a sample. The budget keeps lines from the end the read
// started at, so a head read drops its newest lines and a tail read its oldest, and
// the end that lost lines has more past it.
func sampleOf(in input, page logs.Page) sample {
	tail := in.mode == tools.LogsReadTail
	// In the order read: the records, the ends, and whether the page left more past
	// the far end.
	recs, near, far, more := page.Records, in.since, in.until, page.After.More
	if tail {
		recs, near, far, more = reversed(recs), in.until, in.since, page.Before.More
	}
	kept := fit(recs)
	dropped := len(recs) - len(kept)
	more = more || dropped > 0
	s := sample{records: kept, dropped: dropped, before: edge(near, false), after: edge(far, more), skipped: []string{}}
	if more && len(kept) > 0 {
		s.next = encodeCursor(kept[len(kept)-1])
	}
	if tail {
		s.records = reversed(kept)
		s.before, s.after = s.after, s.before
	}
	for _, line := range page.Skipped {
		s.skipped = append(s.skipped, logs.ForModel(line))
	}
	return s
}

// fit is the longest prefix of recs whose lines fit sampleBudget, each message
// through logs.ForModel and then cut, since redaction reads the whole message.
func fit(recs []logs.Record) []logs.Record {
	var kept []logs.Record
	size := 0
	for _, rec := range recs {
		rec.Message = cut(logs.ForModel(rec.Message))
		if size += len(spell(rec)) + 1; size > sampleBudget {
			break
		}
		kept = append(kept, rec)
	}
	return kept
}

func reversed(recs []logs.Record) []logs.Record {
	out := slices.Clone(recs)
	slices.Reverse(out)
	return out
}

// edge is what lies past one end of the sample: more when the page or the budget
// left lines that way, or when the end is a printed cursor, whose lines the model
// read already; unread past a moment the read stopped at; nothing past an open end
// it reached.
func edge(e *end, more bool) tools.LogsReadEdge {
	switch {
	case more, e != nil && e.line:
		return tools.LogsReadMore
	case e != nil:
		return tools.LogsReadUnread
	}
	return tools.LogsReadEnd
}

// cut is s in at most maxSampleLine bytes, cut on a character boundary with an
// ellipsis.
func cut(s string) string {
	if len(s) <= maxSampleLine {
		return s
	}
	return s[:tools.RuneBoundary(s, maxSampleLine-len("…"))] + "…"
}

// spell is rec as the model reads it: the hole before it on a line of its own,
// then its timestamp, its stream and its message. A message holds no newline, so
// every line a record writes starts with the sidecar's [ or a timestamp.
func spell(rec logs.Record) string {
	line := stamp(rec.Timestamp) + " " + streamOf(rec.Stream) + " " + rec.Message
	if rec.Gap == nil {
		return line
	}
	return "[lines may be missing from " + streamOf(rec.Stream) + " between " + stamp(rec.Gap.After) + " and " + stamp(rec.Gap.Before) + "]\n" + line
}

// streamOf is a stream as a line names it.
func streamOf(k logs.StreamKey) string {
	s := k.Namespace + "/" + k.Pod + "/" + k.Container
	if k.Previous {
		s += " (previous)"
	}
	return s
}

// stamp is t in RFC 3339, in UTC, at its own precision.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// output is the sample as the model reads it: the header, then its lines.
func (s sample) output(in input, notes []logsinput.Note, view tools.LogsViewAction) string {
	lines := []string{s.header(in, notes, view)}
	for _, rec := range s.records {
		lines = append(lines, spell(rec))
	}
	return strings.Join(lines, "\n")
}

// header is the line before the sample: what was read, over what range and which
// end of it, the grep, each source's notes, the streams skipped and the lines
// dropped, then each edge, the one the read moved toward last. It is cluster text,
// redacted as the receipt is; the cursor to read on from goes after the redaction,
// which could change it.
func (s sample) header(in input, notes []logsinput.Note, view tools.LogsViewAction) string {
	var b strings.Builder
	switch n := len(s.records); n {
	case 0:
		b.WriteString("No lines of ")
	case 1:
		b.WriteString("Read 1 line of ")
	default:
		fmt.Fprintf(&b, "Read %d lines of ", n)
	}
	b.WriteString(logsinput.SourcesText(in.Sources) + rangeText(in.since, in.until))
	switch {
	case len(s.records) == 0:
	case in.mode == tools.LogsReadHead:
		b.WriteString(", oldest first")
	default:
		b.WriteString(", the newest")
	}
	if in.Grep != "" {
		b.WriteString(", matching /" + in.Grep + "/")
	}
	b.WriteString(".")
	b.WriteString(notesText(in.Sources, view.Sources, notes))
	b.WriteString(skippedText(s.skipped))
	switch {
	case s.dropped == 1:
		b.WriteString(" 1 more line was left out to fit.")
	case s.dropped > 1:
		fmt.Fprintf(&b, " %d more lines were left out to fit.", s.dropped)
	}
	older := edgeText(s.before, " Older lines exist", " Older lines may exist")
	newer := edgeText(s.after, " More lines follow", " Newer lines may exist")
	near, far, field := older, newer, "since"
	if in.mode == tools.LogsReadTail {
		near, far, field = newer, older, "until"
	}
	if near != "" {
		b.WriteString(near + ".")
	}
	b.WriteString(far)
	text := safe.Redact(b.String())
	switch {
	case s.next != "":
		return text + "; to read on, " + field + ": " + s.next
	case far != "":
		return text + "."
	}
	return text
}

// edgeText is what the header says of an edge: more for More, maybe for Unread,
// nothing for End.
func edgeText(e tools.LogsReadEdge, more, maybe string) string {
	switch e {
	case tools.LogsReadMore:
		return more
	case tools.LogsReadUnread:
		return maybe
	}
	return ""
}

// rangeText is the range as the header names it: a moment's lines are in it, a
// printed cursor's line is not.
func rangeText(since, until *end) string {
	if since != nil && until != nil && !since.line && !until.line {
		return " between " + stamp(since.at) + " and " + stamp(until.at)
	}
	var out string
	if since != nil {
		word := " from "
		if since.line {
			word = " after "
		}
		out += word + stamp(since.at)
	}
	if until != nil {
		word := " up to "
		if until.line {
			word = " before "
		}
		out += word + stamp(until.at)
	}
	return out
}

// notesText is what the header says of each source beyond its name, up to
// notesBudget; the sources past it are counted.
func notesText(typed []logsinput.Source, sources []tools.LogsViewSource, notes []logsinput.Note) string {
	var b strings.Builder
	for i, text := range logsinput.NotesText(typed, sources, notes) {
		if b.Len()+len(text) > notesBudget {
			fmt.Fprintf(&b, " Notes on %d more sources left out.", len(notes)-i)
			break
		}
		b.WriteString(text)
	}
	return b.String()
}

// skippedText names each stream the read could not read, up to skippedBudget; the
// streams past it are counted.
func skippedText(skipped []string) string {
	if len(skipped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" Could not read ")
	for i, line := range skipped {
		if i > 0 && b.Len()+len(line)+2 > skippedBudget {
			fmt.Fprintf(&b, ", and %d more streams", len(skipped)-i)
			break
		}
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(line)
	}
	b.WriteString(".")
	return b.String()
}

// action is the sample as the row keeps it: the view read, the range's ends as
// moments, and the lines the model read, with the view View around this opens at
// the first of them.
func (s sample) action(in input, view tools.LogsViewAction) tools.LogsReadAction {
	read := tools.LogsReadAction{
		LogsViewInput: view.LogsViewInput, Mode: in.mode, ResolvedAt: view.ResolvedAt,
		Lines: []tools.LogsReadLine{}, Dropped: s.dropped, Before: s.before, After: s.after, Skipped: s.skipped,
	}
	if in.since != nil {
		read.Since = &in.since.at
	}
	if in.until != nil {
		read.Until = &in.until.at
	}
	for _, rec := range s.records {
		read.Lines = append(read.Lines, tools.LogsReadLine{
			Timestamp: rec.Timestamp, Namespace: rec.Stream.Namespace, Pod: rec.Stream.Pod, Container: rec.Stream.Container,
			Previous: rec.Stream.Previous, Message: rec.Message, Gap: rec.Gap,
		})
	}
	if len(s.records) > 0 {
		at := s.records[0].Timestamp
		read.ViewAround = &tools.LogsViewAction{
			LogsViewInput: view.LogsViewInput, Anchor: tools.LogsViewAnchor{Kind: tools.LogsAnchorAt, At: &at}, ResolvedAt: view.ResolvedAt,
		}
	}
	return read
}
