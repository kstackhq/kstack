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
	"container/heap"
	"context"
	"errors"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// merge is one direction's page of a view: its records, oldest first, the cut,
// zero when the page was not cut, the live end, and each stream it could not read,
// one line each.
type merge struct {
	records []Record
	cut     position
	end     position
	skipped []string
}

// next is the cursor of m's cut, nil when the page was not cut.
func (m merge) next() *string {
	if m.cut.isZero() {
		return nil
	}
	return cursorOf(m.cut)
}

// before is m's edge toward older lines: the cut while it was cut, else nothing
// older.
func (m merge) before() PageEdge {
	return PageEdge{m.next(), !m.cut.isZero()}
}

// after is m's edge toward newer lines read Head: the cut while it was cut, else
// the live end.
func (m merge) after() PageEdge {
	if m.cut.isZero() {
		return m.liveEnd()
	}
	return PageEdge{m.next(), true}
}

// liveEnd is the edge past every stream's newest line. It always carries a cursor,
// since the live end is where a follow starts.
func (m merge) liveEnd() PageEdge {
	return PageEdge{Cursor: cursorOf(m.end)}
}

// mergePage is one page of readings, each one container's lines in the order the
// page reads them — oldest first for Head, newest first for Tail — merged in
// position order and cut at limit. The cut is where the next page goes on from:
// the last line kept, or the stop of a reading that ran short when that is
// nearer. Every reading is cancelled once the merge has its lines, so no stream
// reads past the page.
func mergePage(ctx context.Context, readings []*streamRead, mode FetchMode, limit int) (merge, error) {
	recs := merged(ctx, readings, mode, limit+1)
	for _, r := range readings {
		r.cancel()
		drainReading(r)
	}
	// A reading the context ended closes as one that reached its end does.
	if err := ctx.Err(); err != nil {
		return merge{}, err
	}
	if mode == FetchTail {
		slices.Reverse(recs)
	}
	skipped, err := failures(readings)
	if err != nil {
		return merge{}, err
	}
	for _, r := range readings {
		switch {
		case !r.short || !r.stop.isZero():
		case errors.Is(r.err, errRetired):
			return merge{}, errReplaced
		default:
			return merge{}, Refusal{"this page ran out of budget before " + r.key.Pod + "/" + r.key.Container + " was read past its start; narrow the view or its grep"}
		}
	}
	// A page past the limit is cut at the far end, and the cursor is the last line kept.
	var cut position
	if len(recs) > limit {
		if mode == FetchHead {
			recs = recs[:limit]
			cut = positionOf(recs[limit-1])
		} else {
			recs = recs[len(recs)-limit:]
			cut = positionOf(recs[0])
		}
	}
	recs, cut = trimToStops(readings, mode, recs, cut)
	return merge{records: recs, cut: cut, skipped: skipped}, nil
}

// errReplaced is a page a stream of which the connection's retirement cut before
// it read a line in range, so no cursor could go on from it.
var errReplaced = Refusal{"the connection to the cluster was replaced while this page was read; read it again"}

// failures names each reading that ended in an error, one line each, and fails
// the page when the cluster refused the user's credentials, when Kstack no longer
// connects to it, or when every reading failed. A reading the page's own stop
// ended is no failure, and neither is one that ran short — the connection's
// retirement cut it, or it failed past a line in range — since the page ends where
// it stopped and the next page reads it again.
func failures(readings []*streamRead) ([]string, error) {
	skipped := []string{}
	for _, r := range readings {
		if err := fatalError(r.err); err != nil {
			return nil, err
		}
		if r.err == nil || r.short || errors.Is(r.err, context.Canceled) {
			continue
		}
		skipped = append(skipped, skippedLine(r.key, r.err))
	}
	if len(skipped) == len(readings) {
		if err := everyStreamFailed(skipped); err != nil {
			return nil, err
		}
	}
	return skipped, nil
}

// everyStreamFailed refuses a page every stream of which failed, naming the first,
// nil when there was none.
func everyStreamFailed(skipped []string) error {
	if len(skipped) == 0 {
		return nil
	}
	return Refusal{"no container of this view could be read: " + skipped[0]}
}

// skippedLine is the page's line naming key, a stream err kept it from reading. A
// transport error can carry a URL's userinfo or the server's bytes.
func skippedLine(key StreamKey, err error) string {
	return key.Pod + "/" + key.Container + ": " + stripControl(safe.Safe(err))
}

// beyond is whether a lies past b in the direction mode reads: later for Head,
// earlier for Tail.
func beyond(mode FetchMode, a, b position) bool {
	c := comparePositions(a, b)
	if mode == FetchHead {
		return c > 0
	}
	return c < 0
}

// nearerStop is the nearer of stop and r's, the nearest for Head being the oldest
// and for Tail the newest, or stop when r did not run short.
func nearerStop(mode FetchMode, stop position, r *streamRead) position {
	if r.short && (stop.isZero() || beyond(mode, stop, r.stop)) {
		return r.stop
	}
	return stop
}

// trimToStops keeps the lines on the near side of the nearest point a reading
// that ran short stopped at, and answers that point as the cut, since past it
// some stream's lines were never read. A cut the limit made nearer than it stands.
// recs are oldest first.
func trimToStops(readings []*streamRead, mode FetchMode, recs []Record, cut position) ([]Record, position) {
	var stop position
	for _, r := range readings {
		stop = nearerStop(mode, stop, r)
	}
	if stop.isZero() {
		return recs, cut
	}
	i, found := slices.BinarySearchFunc(recs, stop, func(rec Record, p position) int {
		return comparePositions(positionOf(rec), p)
	})
	if mode == FetchHead {
		if found {
			i++
		}
		recs = recs[:i]
	} else {
		recs = recs[i:]
	}
	if cut.isZero() || beyond(mode, cut, stop) {
		cut = stop
	}
	return recs, cut
}

// merged is the first n lines of readings in the order mode reads them: each
// reading's next line is held, and the nearest goes out. A reading is asked for
// a line only when the merge needs one, so a full page reads nothing past its
// last line. The merge also ends at the first line past a stop, since the page
// is cut there, and at once when a reading runs short before any line in range
// or ctx ends, since the page is refused.
func merged(ctx context.Context, readings []*streamRead, mode FetchMode, n int) []Record {
	h := &heads{mode: mode}
	var stop position
	// waiting is the readings with no line held.
	waiting := readings
	var out []Record
	for {
		for _, r := range waiting {
			rec, ok := next(ctx, r)
			switch {
			case ok:
				heap.Push(h, headed{rec, r})
			case ctx.Err() != nil, r.short && r.stop.isZero():
				return out
			default:
				stop = nearerStop(mode, stop, r)
			}
		}
		if h.Len() == 0 {
			return out
		}
		top := heap.Pop(h).(headed)
		if !stop.isZero() && beyond(mode, positionOf(top.rec), stop) {
			return out
		}
		out = append(out, top.rec)
		if len(out) == n {
			return out
		}
		waiting = []*streamRead{top.from}
	}
}

// next is r's next line, false once r has ended or ctx has.
func next(ctx context.Context, r *streamRead) (Record, bool) {
	select {
	case rec, ok := <-r.lines.Chan():
		return rec, ok
	case <-ctx.Done():
		return Record{}, false
	}
}

// headed is a reading's next line.
type headed struct {
	rec  Record
	from *streamRead
}

// heads is the lines the readings hold, a heap with the nearest in the order mode
// reads them on top.
type heads struct {
	mode FetchMode
	h    []headed
}

func (s *heads) Len() int { return len(s.h) }
func (s *heads) Less(i, j int) bool {
	return beyond(s.mode, positionOf(s.h[j].rec), positionOf(s.h[i].rec))
}
func (s *heads) Swap(i, j int) { s.h[i], s.h[j] = s.h[j], s.h[i] }
func (s *heads) Push(x any)    { s.h = append(s.h, x.(headed)) }
func (s *heads) Pop() any {
	last := s.h[len(s.h)-1]
	s.h = s.h[:len(s.h)-1]
	return last
}

// drainReading waits for r to end, so how it ended is set.
func drainReading(r *streamRead) {
	for range r.lines.Chan() {
	}
}
