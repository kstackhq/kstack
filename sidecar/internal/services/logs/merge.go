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
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// mergePage is one page of readings, each one container's lines in the order the
// page reads them — oldest first for Head, newest first for Tail — merged in
// position order and cut at limit, the cursor the last line kept. Every reading
// of a fetch ends, so the merge never waits on one for long. stop ends the
// readings' reads, called once the page is full so no stream reads past it.
func mergePage(ctx context.Context, stop context.CancelFunc, readings []*reading, mode FetchMode, limit int) (Page, error) {
	recs := merged(ctx, readings, mode, limit+1)
	stop()
	for _, r := range readings {
		drainReading(r)
	}
	if mode == FetchTail {
		slices.Reverse(recs)
	}
	skipped, err := failures(readings)
	if err != nil {
		return Page{}, err
	}
	for _, r := range readings {
		switch {
		case !r.short || !r.stop.isZero():
		case errors.Is(r.err, errRetired):
			return Page{}, errReplaced
		default:
			return Page{}, &Refusal{"this page ran out of budget before " + r.src.Pod + "/" + r.src.Container + " was read past its start; narrow the view or its grep"}
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
	page := Page{Records: recs, Skipped: skipped}
	if !cut.isZero() {
		page.NextCursor = cursorOf(cut)
	}
	return page, nil
}

// errReplaced is a page a stream of which the connection's retirement cut before
// it read a line in range, so no cursor could go on from it.
var errReplaced = &Refusal{"the connection to the cluster was replaced while this page was read; read it again"}

// failures names each reading that ended in an error, one line each, and fails
// the page when the cluster refused the user's credentials, when Kstack no longer
// connects to it, or when every reading failed. A reading the page's own stop
// ended is no failure, and neither is one that ran short — the connection's
// retirement cut it, or it failed past a line in range — since the page ends where
// it stopped and the next page reads it again.
func failures(readings []*reading) ([]string, error) {
	skipped := []string{}
	for _, r := range readings {
		if err := fatal(r.err); err != nil {
			return nil, err
		}
		if r.err == nil || r.short || errors.Is(r.err, context.Canceled) {
			continue
		}
		// A transport error can carry a URL's userinfo or the server's bytes.
		skipped = append(skipped, r.src.Pod+"/"+r.src.Container+": "+stripControl(safe.Safe(r.err)))
	}
	if len(skipped) > 0 && len(skipped) == len(readings) {
		return nil, &Refusal{"no container of this view could be read: " + skipped[0]}
	}
	return skipped, nil
}

// fatal is the error that fails a whole read when err ends one stream: the
// cluster refusing the user's credentials (401), which are not one pod's state, or
// Kstack no longer connecting to it. Nil for any other, a 403 included: access to
// pods/log is granted per namespace, so it is that stream's alone.
func fatal(err error) error {
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusUnauthorized {
		return &Refusal{"the cluster refused to show this log: " + se.Error()}
	}
	if errors.Is(err, ErrRevoked) {
		return ErrRevoked
	}
	return nil
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

// trimToStops keeps the lines on the near side of the nearest point a reading
// that ran short stopped at — the oldest for Head, the newest for Tail — and
// answers that point as the cut, since past it some stream's lines were never
// read. A cut the limit made nearer than it stands.
func trimToStops(readings []*reading, mode FetchMode, recs []Record, cut position) ([]Record, position) {
	var stop position
	for _, r := range readings {
		if r.short && (stop.isZero() || beyond(mode, stop, r.stop)) {
			stop = r.stop
		}
	}
	if stop.isZero() {
		return recs, cut
	}
	recs = slices.DeleteFunc(recs, func(rec Record) bool { return beyond(mode, positionOf(rec), stop) })
	if cut.isZero() || beyond(mode, cut, stop) {
		cut = stop
	}
	return recs, cut
}

// merged is the first n lines of readings in the order mode reads them: each
// reading's next line is held, and the nearest goes out. A reading is asked for
// a line only when the merge needs one, so a full page reads nothing past its
// last line.
func merged(ctx context.Context, readings []*reading, mode FetchMode, n int) []Record {
	var heads []headed
	for _, r := range readings {
		if rec, ok := next(ctx, r); ok {
			heads = append(heads, headed{rec, r})
		}
	}
	var out []Record
	for len(heads) > 0 {
		best := 0
		for i := range heads {
			if beyond(mode, positionOf(heads[best].rec), positionOf(heads[i].rec)) {
				best = i
			}
		}
		out = append(out, heads[best].rec)
		if len(out) == n {
			break
		}
		if rec, ok := next(ctx, heads[best].from); ok {
			heads[best].rec = rec
		} else {
			heads = slices.Delete(heads, best, best+1)
		}
	}
	return out
}

// next is r's next line, false once r has ended or ctx has.
func next(ctx context.Context, r *reading) (Record, bool) {
	select {
	case rec, ok := <-r.lines:
		return rec, ok
	case <-ctx.Done():
		return Record{}, false
	}
}

// headed is a reading's next line.
type headed struct {
	rec  Record
	from *reading
}

// drainReading waits for r to end, so how it ended is set.
func drainReading(r *reading) {
	for range r.lines {
	}
}

// fanIn gathers a follow's readings: each line goes out as its stream delivers
// it, in that container's order and across containers in arrival order, so one
// quiet container never holds back the rest. ended carries each reading as it
// ends. Both say which reading, since a stream cancelled and reopened on the same
// source can deliver a line, and ends, after its successor started.
type fanIn struct {
	lines chan headed
	ended chan *reading
}

func newFanIn() *fanIn {
	return &fanIn{lines: make(chan headed), ended: make(chan *reading)}
}

// add relays r's lines until it ends, then reports its end. Both stop once ctx
// ends, which is the follow's: a reading cancelled on its own still reports.
func (f *fanIn) add(ctx context.Context, r *reading) {
	go func() {
		for rec := range r.lines {
			select {
			case f.lines <- headed{rec, r}:
			case <-ctx.Done():
				drainReading(r)
				return
			}
		}
		select {
		case f.ended <- r:
		case <-ctx.Done():
		}
	}()
}

// handoff holds a follow's first lines, the ones that arrive while the page the
// client holds and the follow's opening reads overlap, and releases them sorted,
// as kubetail's runForwarder does.
type handoff struct {
	held     []Record
	released bool
}

// hold keeps rec while the handoff holds, and is false once it has been released,
// when rec goes out as it comes.
func (h *handoff) hold(rec Record) bool {
	if h.released {
		return false
	}
	h.held = append(h.held, rec)
	return true
}

// release is every held line in order; from then on hold keeps nothing.
func (h *handoff) release() []Record {
	h.released = true
	slices.SortStableFunc(h.held, compareLines)
	held := h.held
	h.held = nil
	return held
}
