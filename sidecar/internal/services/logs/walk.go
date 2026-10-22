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
	"sync/atomic"
	"time"

	"github.com/amorey/gochan/spsc"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// maxWalkBudget is the bytes one page's walks of the file read, shared evenly across
// its streams; each stream's walk also reads what its fill downloaded, so a stream
// read far is walked as far.
const maxWalkBudget = 64 << 20

// budget is the bytes a walk of the file may still read. A nil budget is no bound.
type budget struct{ left atomic.Int64 }

func newBudget(n int64) *budget {
	b := &budget{}
	b.left.Store(n)
	return b
}

// spend takes n bytes, and is false once there were not n left.
func (b *budget) spend(n int) bool { return b == nil || b.left.Add(-int64(n)) >= 0 }

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
// the page reads them, and whether the walk stopped at a live gap of a stream skipped on a transient
// failure, which does not cut the page but leaves more to read that way. A stream
// the server denied or answered for has nothing more to give the next page, and
// neither does one that restarted, since the next resolve reads the new instance.
// read is the chunks the walk read a line from, which the page stamps.
type walked struct {
	reading *streamRead
	left    bool
	read    []logstore.Chunk
}

// walk reads the stream from key. A walk stopped by its budget, or by a live gap on
// a stream not skipped or stopped at a loud second, stops short where it reached, so
// the page is cut there: at the fill's stop when that lies past what it read.
func (w walker) walk(key logstore.Pos) (walked, error) {
	chunks, err := w.r.Chunks(w.ps.row.ID, w.q.scope.Grep)
	if err != nil {
		return walked{}, err
	}
	cov := logstore.CoverageOf(chunks, w.names, w.ps.tail)
	reach, gap := w.reach(cov, key)
	held := heldBy(chunks)
	var recs []Record
	var first, last logstore.Pos
	read := false
	cut := w.fill.err == nil || failureKind(w.fill.err) == failLimit
	short := gap && cut
	spent := false
	err = w.r.Lines(w.ps.row.ID, key, reach, func(l logstore.Line) bool {
		if !read {
			first = l.Pos
		}
		last, read = l.Pos, true
		spent = !w.budget.spend(len(l.Message))
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
	var stop lineCursor
	var failed error
	switch {
	case read:
		stop = w.ps.run().at(last)
	case short && w.fill.retired:
		failed = errRetired
	}
	// At the gap the fill ended in, the fill's stop is the edge when it lies past what
	// the walk read: an empty window's start, or a loud second.
	if short && !spent && !w.fill.stop.isZero() && (stop.isZero() || beyond(w.q.mode, w.fill.stop, stop)) {
		stop = w.fill.stop
	}
	transient := failureKind(w.fill.err) == failTransient && !errors.Is(w.fill.err, errRestarted)
	left := gap && transient && len(recs) < w.q.need()
	out := walked{reading: readingOf(w.ps.key, recs, short, stop, failed), left: left}
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
// starts, the newest when the fill read it to its end, and the one the far bound
// lies in when the fill read it to the bound, up to the first live gap left or the
// far bound, and whether it stops at a gap.
func (w walker) reach(cov logstore.Coverage, key logstore.Pos) (logstore.Pos, bool) {
	head := w.q.mode == newer
	far := w.q.far(w.ps)
	reach := key
	first := true
	for _, seg := range segmentsFrom(cov, key, w.q.mode) {
		if far != nil && pastFar(seg, *far, w.q.mode) {
			break
		}
		switch {
		case seg.span != nil && head:
			reach = seg.span.To
		case seg.span != nil:
			reach = seg.span.From
		case !seg.gap.Live():
			continue
		case first && w.fill.fromKey, w.fill.toEnd && seg.gap.Newest(), w.fill.toFar:
		default:
			return clampTo(reach, key, far, w.q.mode), true
		}
		first = false
	}
	return clampTo(reach, key, far, w.q.mode), false
}

// pastFar is whether seg holds no place short of far, the first place past a page's
// range in the direction mode reads.
func pastFar(seg segment, far logstore.Pos, mode direction) bool {
	switch {
	case seg.span != nil && mode == newer:
		return seg.span.From.Compare(far) >= 0
	case seg.span != nil:
		return seg.span.To.Compare(far) <= 0
	case mode == newer:
		return seg.gap.After != nil && seg.gap.After.Next().Compare(far) >= 0
	}
	return seg.gap.Before != nil && seg.gap.Before.Prev().Compare(far) <= 0
}

// clampTo is reach, a walk's last place from key, held short of far, nil for no
// bound, and never behind key, so a range with no place left reads nothing.
func clampTo(reach, key logstore.Pos, far *logstore.Pos, mode direction) logstore.Pos {
	if far == nil {
		return reach
	}
	if mode == newer {
		return logstore.MaxPos(key, logstore.MinPos(reach, far.Prev()))
	}
	return logstore.MinPos(key, logstore.MaxPos(reach, far.Next()))
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
	if w.q.mode == older {
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
// line at a lineCursor: a line between two chunks of a span is another pattern's,
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
func readingOf(key StreamKey, recs []Record, short bool, stop lineCursor, err error) *streamRead {
	out, lines := spsc.New[readItem](len(recs))
	for _, rec := range recs {
		_ = out.Send(readItem{Record: rec}) // the buffer holds every line, so nothing blocks or fails
	}
	out.Close()
	if !short {
		stop = lineCursor{}
	}
	return &streamRead{key: key, lines: lines, cancel: func() {}, err: err, short: short, stop: stop}
}
