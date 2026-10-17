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

package logs

import (
	"errors"
	"slices"
	"time"

	"github.com/amorey/gochan/spsc"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// maxWalkBudget is the bytes one page's walks of the file read, shared evenly across
// its streams as maxBudget is: a sparse grep over a dense log walks far.
const maxWalkBudget = 32 << 20

// walker walks one stream of one page's file: its lines from the page's bound in the
// page's direction, bounded by the span the bound sits in with lost gaps crossed,
// that the grep passes, until it holds need of them or has spent its budget.
type walker struct {
	r      logstore.Reader
	ps     pageStream
	names  []string
	q      pageQuery
	budget *budget
	fill   streamFill
	// look is what the look past the walk's edge may read.
	look int64
}

// walked is one stream's walk: the reading the merge takes, its lines in the order
// the page reads them; the newest line the file holds of the stream, nil with none;
// and whether the walk stopped at a live gap of a stream skipped on a transient
// failure, which does not cut the page but leaves more to read that way. A stream
// the server denied or answered for has nothing more to give the next page, and
// neither does one that restarted, since the next resolve reads the new instance.
// read is the chunks the walk read a line from, which the page stamps.
type walked struct {
	reading *streamRead
	newest  *logstore.Pos
	left    bool
	read    []logstore.Chunk
}

// walk reads the stream from key. A walk stopped by its budget, or by a live gap on
// a stream not skipped, stops short where it reached, so the page is cut there.
func (w walker) walk(key logstore.Pos) (walked, error) {
	chunks, err := w.r.Chunks(w.ps.row.ID, w.q.view.Grep)
	if err != nil {
		return walked{}, err
	}
	cov := logstore.CoverageOf(chunks, w.names, w.ps.tail)
	reach, gap := w.reach(cov, key)
	held := heldBy(chunks)
	var recs []Record
	var first, last logstore.Pos
	read := false
	short := gap && w.fill.err == nil
	err = w.r.Lines(w.ps.row.ID, key, reach, func(l logstore.Line) bool {
		if !read {
			first = l.Pos
		}
		last, read = l.Pos, true
		spent := !w.budget.spend(len(l.Message))
		if held(l.Pos) && matches(w.q.grep, l.Message) {
			recs = append(recs, w.record(l))
		}
		switch {
		case spent:
			short = true
			return false
		case len(recs) == w.q.need():
			short = false
			return false
		}
		return true
	})
	if err != nil {
		return walked{}, err
	}
	if err := w.mark(recs, cov, held); err != nil {
		return walked{}, err
	}
	var stop position
	var failed error
	switch {
	case read:
		stop = positionAt(w.ps.instance(), last)
	case short && w.fill.retired:
		failed = errRetired
	}
	transient := failureKind(w.fill.err) == failTransient && !errors.Is(w.fill.err, errRestarted)
	left := gap && transient && len(recs) < w.q.need()
	out := walked{reading: readingOf(w.ps.key, recs, short, stop, failed), newest: newestOf(chunks), left: left}
	if read {
		out.read = readFrom(chunks, first, last)
	}
	return out, nil
}

// readFrom is each of chunks holding a line between a and b, either way round.
func readFrom(chunks []logstore.Chunk, a, b logstore.Pos) []logstore.Chunk {
	if a.Compare(b) > 0 {
		a, b = b, a
	}
	var out []logstore.Chunk
	for _, c := range chunks {
		if c.From.Compare(b) <= 0 && c.To.Compare(a) >= 0 {
			out = append(out, c)
		}
	}
	return out
}

// reach is how far from key the walk may read: through the spans, across the gaps no
// fill may be offered, the first live gap when the fill read it from where the walk
// starts, and the newest when the fill read it to its end, up to the first live gap
// left, and whether it stops at one.
func (w walker) reach(cov logstore.Coverage, key logstore.Pos) (logstore.Pos, bool) {
	head := w.q.mode == FetchHead
	reach := key
	first := true
	for _, seg := range segmentsFrom(cov, key, w.q.mode) {
		switch {
		case seg.span != nil && head:
			reach = seg.span.To
		case seg.span != nil:
			reach = seg.span.From
		case !seg.gap.Live():
			continue
		case first && w.fill.fromKey, w.fill.toEnd && seg.gap.Newest():
		default:
			return reach, true
		}
		first = false
	}
	return reach, false
}

// mark sets the hole each of recs carries, recs in the walk's order. A hole between
// two goes to the later in time. The record at the walk's older edge, a Head walk's
// first or a Tail walk's last, looks back past the edge for its own, since the line
// before it that the view passes may lie outside what the walk read.
func (w walker) mark(recs []Record, cov logstore.Coverage, held func(logstore.Pos) bool) error {
	if len(recs) == 0 {
		return nil
	}
	edge, before := 0, -1
	if w.q.mode == FetchTail {
		edge, before = len(recs)-1, 1
	}
	for i := range recs {
		if i != edge {
			recs[i].Gap = holesBetween(cov.Gaps, w.ps.committed, lineOf(recs[i+before]).Pos, lineOf(recs[i]).Pos)
		}
	}
	l := looker{r: w.r, row: w.ps.row.ID, cov: cov, committed: w.ps.committed, held: held, grep: w.q.grep, bytes: w.look}
	var err error
	recs[edge].Gap, err = l.back(lineOf(recs[edge]).Pos)
	return err
}

// record is l as a line of the page.
func (w walker) record(l logstore.Line) Record {
	return recordOf(l, w.ps.key, w.ps.place, w.ps.row.ContainerID)
}

// recordOf is l, a line of the instance containerID of key's stream running in place,
// as a page or a follow answers it.
func recordOf(l logstore.Line, key StreamKey, place Place, containerID string) Record {
	return Record{
		Timestamp: time.Unix(0, l.TS).UTC(), Message: l.Message, Stream: key, Place: place,
		ContainerID: containerID, Ordinal: l.Ord,
	}
}

// heldBy is whether a chunk of chunks, oldest first as Chunks reads them, claims a
// line at a position: a line between two chunks of a span is another pattern's,
// which the page's chunks do not vouch for.
func heldBy(chunks []logstore.Chunk) func(logstore.Pos) bool {
	var runs []logstore.Span
	for _, c := range chunks {
		if n := len(runs); n > 0 && c.From.Compare(runs[n-1].To) <= 0 {
			runs[n-1].To = logstore.MaxPos(runs[n-1].To, c.To)
			continue
		}
		runs = append(runs, logstore.Span{From: c.From, To: c.To})
	}
	return func(p logstore.Pos) bool {
		i, found := slices.BinarySearchFunc(runs, p, func(r logstore.Span, p logstore.Pos) int { return r.From.Compare(p) })
		if found {
			return true
		}
		return i > 0 && p.Compare(runs[i-1].To) <= 0
	}
}

// newestOf is the newest line chunks claim, nil with none.
func newestOf(chunks []logstore.Chunk) *logstore.Pos {
	if len(chunks) == 0 {
		return nil
	}
	newest := slices.MaxFunc(chunks, func(a, b logstore.Chunk) int { return a.To.Compare(b.To) }).To
	return &newest
}

// readingOf is a reading of key that has already read recs and ended as it says.
func readingOf(key StreamKey, recs []Record, short bool, stop position, err error) *streamRead {
	out, lines := spsc.New[Record](len(recs))
	for _, rec := range recs {
		_ = out.Send(rec) // the buffer holds every line, so nothing blocks or fails
	}
	out.Close()
	if !short {
		stop = position{}
	}
	return &streamRead{key: key, lines: lines, cancel: func() {}, err: err, short: short, stop: stop}
}
