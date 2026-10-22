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
	"slices"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// errRestarted is a stream whose pod ran another instance by the time its reads
// ended, so what they read was thrown away.
var errRestarted = errors.New("restarted while reading")

// streamFill is what a page's fills did for one stream: the failure that skips the
// stream, and what the page and the walk need to know of the reads.
type streamFill struct {
	err error
	// retired is whether the connection's retirement cut a read before its first
	// line, which refuses the page as the merge refuses a live read so cut.
	retired bool
	// fromKey is whether the first gap a fill may be offered was read from where the
	// walk starts in it, the page's bound or the tailer's start, so nothing lies
	// between there and the first line past it: the walk crosses that gap. toEnd is
	// whether a newer page read the newest gap it may fill whole, to the stream's end
	// or to the tailer's start, which no chunk sits at: the walk crosses it.
	fromKey bool
	toEnd   bool
	// toFar is whether the fill read the gap the page's far bound lies in up to the
	// bound, so the walk crosses it there.
	toFar bool
	// wrote is whether a write filed anything, and gone whether one found the stream's
	// row gone: the janitor took a gone pod's row with nothing in the file before the
	// page held it, and the page reads it as a stream no backend serves.
	wrote bool
	gone  bool
	// up is what an older fill from the same bound links its first window up to: the
	// first line a newer fill read from its bound, or, when it read its first gap whole
	// and found none, that gap's far edge. Nil with neither.
	up *logstore.Pos
	// stop is the moment a read was cut or stopped at when what it wrote does not
	// show it: an empty window's start, or a loud second's edge. The walk that
	// reaches the gap the read ended in stops there. Zero with none.
	stop lineCursor
	// downloaded is the bytes the stream's reads downloaded, which its walk may read
	// past its share.
	downloaded int64
}

// filler fills one stream for one page: the gaps in the page's direction from its
// bound go to the cluster's backends in order, until the stream holds need lines
// the page keeps past the bound, has no live gap left that way, or a read of it
// was cut. A read's lines are written a batch at a time as they arrive.
type filler struct {
	file *logstore.Store
	// clusters and id are the mirror each write checks the stream's instance against,
	// and save writes one batch's fills: a page's stamped, a follow's held.
	clusters cluster.Service
	id       cluster.ClusterID
	save     func(context.Context, []logstore.Fill) (logstore.Written, error)
	// ret sizes the batches a write files.
	ret      logstore.Retention
	ps       pageStream
	backends []Backend
	names    []string
	q        pageQuery
	// limits is the stream's account of the query, which every read of it spends.
	limits *limits
	// walk is the stream's share of the walks' budget, which counting what the file
	// holds spends.
	walk *budget
	// held is whether a chunk of the page's pattern claims a line, as the walk keeps
	// only such lines.
	held func(logstore.Pos) bool
	// seam is the stream's line just past an older page's bound, which a newer fill from
	// the same bound proved nothing lies before: the read back from the bound links
	// up to it. Nil for none.
	seam *logstore.Pos
	// near is the rate next to the point of the gap an older fill reads, nil with none
	// measured.
	near *rate
	// far is where the page's range ends in the stream, nil for no end.
	far *logstore.Pos
	// count is the lines the stream holds for the page, past of them past the
	// range's end.
	count, past int
	out         streamFill
}

// run fills the stream from key, its bound, and answers what it did. Only a fatal
// failure or one of the file is an error.
func (f *filler) run(ctx context.Context, key logstore.Pos) (streamFill, error) {
	chunks, err := f.file.Chunks(ctx, f.ps.row.ID, f.q.scope.Grep)
	if err != nil {
		return streamFill{}, err
	}
	f.held = heldBy(chunks)
	segs := segmentsFrom(logstore.CoverageOf(chunks, f.names, f.ps.tail), key, f.q.mode)
	// Counting a span only decides whether to fill a gap past it, so the fill ends
	// at the last live gap.
	segs = segs[:lastLiveGap(segs)+1]
	f.far = f.q.far(f.ps)
	first := true
	for _, seg := range segs {
		if f.far != nil && pastFar(seg, *f.far, f.q.mode) {
			break
		}
		var more bool
		switch {
		case seg.span != nil:
			more, err = f.countSpan(ctx, *seg.span, key)
		case seg.gap.Live():
			more, err = f.fillGap(ctx, *seg.gap, key, first)
		default:
			continue
		}
		if err != nil || !more {
			return f.out, err
		}
		first = false
	}
	return f.out, nil
}

// lastLiveGap is the index of the last gap of segs that a fill may be offered, -1
// with none.
func lastLiveGap(segs []segment) int {
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].gap != nil && segs[i].gap.Live() {
			return i
		}
	}
	return -1
}

// filled is whether the stream holds what the page needs past its bound.
func (f *filler) filled() bool { return f.q.full(f.count, f.past) }

// countSpan counts the lines past key in sp that the walk keeps, and is whether the
// fill may go on past the span: the stream still needs lines, and the walks' budget
// is not spent.
func (f *filler) countSpan(ctx context.Context, sp logstore.Span, key logstore.Pos) (bool, error) {
	from, to := logstore.MaxPos(key, sp.From.Prev()), sp.To
	if f.q.mode == older {
		from, to = logstore.MinPos(key, sp.To.Next()), sp.From
	}
	counted := true
	err := f.file.Read(ctx, func(r logstore.Reader) error {
		return r.Lines(f.ps.row.ID, from, to, func(l logstore.Line) bool {
			if !f.walk.spend(len(l.Message)) {
				counted = false
				return false
			}
			if f.held(l.Pos) && f.q.keep(l.TS, l.Message) {
				f.count++
				if f.q.pastEnd(l.Pos) {
					f.past++
				}
			}
			return !f.filled()
		})
	})
	return counted && !f.filled(), err
}

// fillGap reads g, a live gap, from the backends, and is whether the fill may go on
// past it: the gap was read whole, so no limit cut it, the stream still needs lines,
// the read did not fail, and the stream's limits are not used up. first is whether g
// is the first gap the fill is offered, the one the walk starts in. The fill ends at
// the page's far bound.
func (f *filler) fillGap(ctx context.Context, g logstore.Gap, key logstore.Pos, first bool) (bool, error) {
	lower, upper, prev, next := gapRange(g, key, f.q.mode)
	if f.q.mode == older && upper != nil && next == nil {
		// The read starts at the bound, inside the gap.
		next = f.seam
	}
	near, err := f.rateAbove(ctx, g.Before)
	if err != nil {
		return false, err
	}
	f.near = near
	// A gap the far bound lies in is read up to the bound, where no chunk sits.
	clamped := false
	switch {
	case f.far == nil:
	case f.q.mode == newer && (upper == nil || upper.Compare(*f.far) > 0):
		upper, next, clamped = f.far, nil, true
	case f.q.mode == older && (lower == nil || lower.Compare(*f.far) < 0):
		lower, prev, clamped = f.far, nil, true
	}
	whole, oldest, err := f.read(ctx, g.Tier, lower, upper, prev, next)
	if err != nil {
		return false, err
	}
	if first && (whole || oldest != nil) {
		f.out.fromKey = true
		if f.q.mode == newer {
			f.out.up = oldest
			if oldest == nil {
				f.out.up = upper
			}
		}
	}
	if clamped {
		f.out.toFar = whole
		return false, nil
	}
	if whole && f.q.mode == newer && g.Newest() {
		f.out.toEnd = true
	}
	return whole && !f.filled() && f.out.err == nil && !f.limits.usedUp(), nil
}

// rateAbove is the rate of an older fill's lines next to its point, sampled oldest
// first from above, the first line of the chunk the gap ends at. It is nil for a
// newer fill, a gap that ends at nothing, or too few lines to measure. The point
// often lies inside the gap, so the chunk is the gap's, not one at the point.
func (f *filler) rateAbove(ctx context.Context, above *logstore.Pos) (*rate, error) {
	if f.q.mode != older || above == nil {
		return nil, nil
	}
	need := int64(f.q.need())
	var s sample
	err := f.file.Read(ctx, func(r logstore.Reader) error {
		s = sample{}
		return r.Lines(f.ps.row.ID, above.Prev(), logstore.Newest, func(l logstore.Line) bool {
			ts := time.Unix(0, l.TS)
			s.add(ts, wireBytes(ts, l.Message), f.q.keep(l.TS, l.Message))
			return s.short(need)
		})
	})
	near, ok := s.rate()
	if err != nil || !ok {
		return nil, err
	}
	return &near, nil
}

// read reads the range between lower and upper, both excluded and nil for none, from
// the backend at tier, and writes what it read and proved. prev and next are the
// chunk edges the range runs from and to, nil where it does not reach one. It
// answers whether the read served every line in the range, and the oldest line it
// wrote, nil with none. A backend that answers head offers what is older than its
// first line to the next.
func (f *filler) read(ctx context.Context, tier int, lower, upper, prev, next *logstore.Pos) (whole bool, oldest *logstore.Pos, err error) {
	p, ok, err := f.pull(ctx, tier, lower, upper, prev, next)
	if !ok || err != nil {
		return false, nil, err
	}
	if !p.whole && p.r.short && p.r.stop.moment() {
		f.out.stop = p.r.stop
	}
	if !p.whole && failureKind(p.r.err) == failLimit {
		// The stream stopped at a loud second: the page names it, and is cut there. A
		// read whole stopped past its range, which a later gap meets again.
		f.out.err = p.r.err
	}
	got := p.oldest != nil
	if !got && !p.whole {
		// Nothing in range was read: the stream is skipped, the page refused when the
		// retirement cut the read, or cut at the read's moment.
		switch {
		case p.r.short && p.r.stop.isZero():
			f.out.retired = errors.Is(p.r.err, errRetired)
		case !p.r.short:
			f.out.err = p.r.err
		}
		return false, nil, nil
	}
	if ok, err := f.link(ctx, tier, p, prev, next); !ok {
		return false, nil, err
	}
	if p.offer {
		// The moment of the backend's first line is its whole: the next is asked for
		// what is older than that moment.
		older, olderNext := upper, next
		if got {
			older, olderNext = &logstore.Pos{TS: p.oldest.TS, Ord: -1}, p.oldest
		}
		olderWhole, olderOldest, err := f.read(ctx, tier+1, lower, older, prev, olderNext)
		if olderOldest != nil {
			return p.whole && olderWhole, olderOldest, err
		}
		return p.whole && olderWhole, p.oldest, err
	}
	return p.whole, p.oldest, nil
}

// pulled is one backend read as pull ended it: the read; whether it served every
// line in the range; oldest, a forward read's first line or the first line of the
// last window a backward read completed, nil with none; head, whether the read
// reached the backend's oldest line; and offer, whether what lies below its first
// line goes to the next backend.
type pulled struct {
	r           *streamRead
	whole       bool
	oldest      *logstore.Pos
	head, offer bool
}

// up is what a window links up to: the oldest line the read has written, or next,
// the chunk at the point, while it has written none. An empty window writes nothing,
// so the next older one links over it.
func (p pulled) up(next *logstore.Pos) *logstore.Pos {
	if p.oldest != nil {
		return p.oldest
	}
	return next
}

// pull runs the backend read of the range and writes its lines as they arrive, a
// batch at a time, counting the lines the page keeps. A forward read is one
// window whose lines arrive nearest the point first: its first batch links down,
// and it is cancelled once the stream holds what the page needs. A backward read's
// windows arrive newest first, each oldest first: a window links up only at its
// end, and counts toward the page only then; one stopped part way, or one the read
// ended inside, is an island linked to nothing. The read is stopped between
// windows, since the lines a window has counted part way are the ones farthest
// from the point. It is false once a write ended the read. A read the page's
// context ended is an error.
func (f *filler) pull(ctx context.Context, tier int, lower, upper, prev, next *logstore.Pos) (pulled, bool, error) {
	forward := f.q.mode == newer
	o := readOpts{since: f.boundAt(lower), until: f.boundAt(upper), limits: f.limits, batch: int64(f.q.need()), keep: f.q.kept, near: f.near}
	// began is the moment a forward read's GET began at, told before its first line
	// arrives.
	var began time.Time
	o.onOldest = func(t time.Time) { began = t }
	// answers holds the answer to the reader's next more, whether another window
	// may follow, given once a window's last batch is written. The reader asks only
	// when another GET would follow, so an answer it never took is replaced by the
	// next window's. stopped is the reader taking a no.
	answers := make(chan bool, 1)
	stopped := false
	o.more = func(ctx context.Context) bool {
		select {
		case ok := <-answers:
			stopped = !ok
			return ok
		case <-ctx.Done():
			return false
		}
	}
	var r *streamRead
	if forward {
		r = f.backends[tier].Forward(ctx, *f.ps.st, o)
	} else {
		r = f.backends[tier].Backward(ctx, *f.ps.st, o)
	}
	out := pulled{r: r}
	w := f.newWindow(tier)
	abandon := func(err error) (pulled, bool, error) {
		r.cancel()
		drainReading(r)
		return pulled{}, false, err
	}
	cut := false
	for it := range r.lines.Chan() {
		if it.windowCut {
			// The window was stopped part way, so the lines between it and the point
			// were never read: an island, linked to nothing, whose kept lines the page
			// cannot reach.
			if ok, err := f.endWindow(ctx, w, nil, false); !ok {
				return abandon(err)
			}
			w = f.newWindow(tier)
			continue
		}
		if it.windowEnd {
			// The window was read whole, so its last batch links it up. The read's
			// first window from the end of a finished instance holds its last line.
			atEnd := upper == nil && out.oldest == nil && f.ps.st.finished()
			if ok, err := f.endWindow(ctx, w, out.up(next), atEnd); !ok {
				return abandon(err)
			}
			if w.first != nil {
				out.oldest = w.first
			}
			f.count += w.kept
			w = f.newWindow(tier)
			select {
			case <-answers:
			default:
			}
			answers <- !f.filled()
			continue
		}
		if forward && w.first == nil {
			// A forward read links down from its first batch, so what it continues
			// is judged at its first line, from the moment its GET began at.
			out.head = f.headRead(lower, prev, began, true)
			w.batch.Head = out.head
			if !out.head {
				w.batch.Prev, w.batch.Merge = prev, returnedWhole(began, prev)
			}
		}
		if ok, err := f.add(ctx, w, lineOf(it.Record)); !ok {
			return abandon(err)
		}
		// A forward read counts as its lines arrive, nearest the point first; a
		// backward window counts once it completes, since its lines arrive farthest
		// first.
		if f.q.kept(it.Record) {
			if forward {
				f.count++
				if f.q.pastEnd(lineOf(it.Record).Pos) {
					f.past++
				}
			} else {
				w.kept++
			}
		}
		if forward && f.filled() {
			cut = true
			r.cancel()
			drainReading(r)
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return pulled{}, false, err
	}
	if err := fatalError(r.err); err != nil {
		return pulled{}, false, err
	}
	clean := r.err == nil && !r.short
	// A forward GET reads from the range's lower second, so one whose first line lies
	// past the range found the backend holds none of it, however the read then ended.
	past := forward && w.first == nil && upper != nil && !began.IsZero() && began.UnixNano() > upper.TS
	out.whole = !cut && !stopped && (clean || past)
	if forward {
		out.oldest = w.first
		if w.first == nil {
			out.head = f.headRead(lower, prev, began, !began.IsZero() || clean)
		}
		out.offer = tier+1 < len(f.backends) && out.head
		// The read's last batch links up only when the read served every line to next.
		up := next
		if !out.whole {
			up = nil
		}
		if ok, err := f.endWindow(ctx, w, up, r.atEnd); !ok {
			return pulled{}, false, err
		}
		return out, true, nil
	}
	// A window the read ended inside is an island: what it read goes to the file,
	// linked up to nothing, since the lines between it and the point were never read.
	if ok, err := f.endWindow(ctx, w, nil, false); !ok {
		return pulled{}, false, err
	}
	out.head = r.head
	out.offer = tier+1 < len(f.backends) && (r.head || hole(out.oldest, out.whole, prev))
	return out, true, nil
}

// headRead is whether a forward read from lower reached the backend's oldest line:
// it has no lower bound, so it started there, or it read from prev's second and its
// GET began past prev's moment, so prev's line rotated away. began is the moment of
// the GET's first line, zero with none, and proved whether the read got a line or
// ended clean: a GET that failed before its first line says nothing. A read from any
// other bound, a page's key, says nothing of rotation.
func (f *filler) headRead(lower, prev *logstore.Pos, began time.Time, proved bool) bool {
	fromPrev := prev != nil && lower != nil && *lower == *prev
	return proved && (lower == nil || fromPrev && rotatedAway(began, prev))
}

// hole is whether a backward read from prev left a range below its oldest line it
// may not have served: it reached back to prev with its oldest line past it, or
// found nothing at all in a range it read whole. A read that stopped short of prev,
// cut by the page filling or by a limit, says nothing of what lies below it.
func hole(oldest *logstore.Pos, whole bool, prev *logstore.Pos) bool {
	if prev == nil || !whole {
		return false
	}
	return oldest == nil || oldest.TS > prev.TS
}

// link writes what a read proved beyond its lines, once it has ended, and is false
// once the write ended the read. A forward read links down from its first batch and
// up from its last, so only one that wrote no line has links left: from prev to
// next, with its marks. A backward read links up window by window and down once it
// ends: from the oldest line it wrote, or next with none, to prev, which only a read
// that served every line down to prev proves, with the head mark.
func (f *filler) link(ctx context.Context, tier int, p pulled, prev, next *logstore.Pos) (bool, error) {
	fill := logstore.Fill{Stream: f.ps.row.ID, Origin: f.names[tier], Head: p.head}
	if f.q.mode == newer {
		if p.oldest != nil {
			return true, nil
		}
		if !p.head {
			fill.Prev = prev
		}
		if p.whole {
			fill.Next = next
		}
		fill.AtEnd = p.r.atEnd
		return f.write(ctx, fill)
	}
	fill.Next = p.up(next)
	if p.whole && !p.head && !p.offer {
		fill.Prev = prev
	}
	if fill.Prev == nil && !fill.Head {
		return true, nil
	}
	return f.write(ctx, fill)
}

// window is one window of a read as it arrives, written a batch at a time. batch is
// the fill of the lines not yet written: the window's first carries its link down and
// head mark, each later one continues the chunk the one before it wrote. first is the
// window's first line and last the last line written, nil before either.
type window struct {
	batch       logstore.Fill
	bytes       int64
	first, last *logstore.Pos
	// kept is a backward window's lines the page keeps, counted toward the page
	// once it completes.
	kept int
}

// newWindow is an empty window of the backend at tier's read.
func (f *filler) newWindow(tier int) *window {
	return &window{batch: logstore.Fill{Stream: f.ps.row.ID, Origin: f.names[tier]}}
}

// add takes line into w, first writing the batch it fills, and is false once a
// write ended the read.
func (f *filler) add(ctx context.Context, w *window, line logstore.Line) (bool, error) {
	if n := len(w.batch.Lines); n > 0 && (n >= f.ret.WriteBatch || w.bytes >= f.ret.WriteBatchBytes) {
		if ok, err := f.flush(ctx, w); !ok {
			return false, err
		}
	}
	if w.first == nil {
		w.first = new(line.Pos)
	}
	w.batch.Lines = append(w.batch.Lines, line)
	w.bytes += int64(len(line.Message))
	return true, nil
}

// endWindow writes w's last batch, linked up to next and marked atEnd, and is false
// once the write ended the read. A window with no line writes nothing.
func (f *filler) endWindow(ctx context.Context, w *window, next *logstore.Pos, atEnd bool) (bool, error) {
	if len(w.batch.Lines) == 0 {
		return true, nil
	}
	w.batch.Next, w.batch.AtEnd = next, atEnd
	return f.flush(ctx, w)
}

// flush writes w's batch, and starts the next one as the continuation of the chunk
// it ends.
func (f *filler) flush(ctx context.Context, w *window) (bool, error) {
	if ok, err := f.write(ctx, w.batch); !ok {
		return false, err
	}
	// A copy, so the batch's lines are not kept alive by it.
	w.last = new(w.batch.Lines[len(w.batch.Lines)-1].Pos)
	w.batch = logstore.Fill{Stream: w.batch.Stream, Origin: w.batch.Origin, Prev: w.last, Merge: true}
	w.bytes = 0
	return true, nil
}

// write files one batch's fill once the mirror shows the stream's pod still runs the
// instance it was resolved as, and is whether the read may go on. The mirror lags a
// restart, while the kubelet serves whichever instance runs now: a stream that
// restarted is skipped, and what it wrote before stays.
func (f *filler) write(ctx context.Context, fill logstore.Fill) (bool, error) {
	serves, err := f.serves(ctx)
	if err != nil {
		return false, err
	}
	if !serves {
		f.out = streamFill{err: errRestarted, wrote: f.out.wrote}
		return false, nil
	}
	w, err := f.save(ctx, []logstore.Fill{fill})
	if err != nil {
		return false, err
	}
	if len(w.Skipped) > 0 {
		f.out.gone = true
		return false, nil
	}
	f.out.wrote = true
	return true, nil
}

// serves is whether the mirror shows the stream's pod running the instance it was
// resolved as.
func (f *filler) serves(ctx context.Context) (bool, error) {
	var serves bool
	err := f.clusters.CachedData().ReadActive(ctx, f.id, maxRows, maxBytes, func(q cluster.CacheReading, _ cluster.ActiveCluster) error {
		var err error
		serves, err = reading{q}.serves(*f.ps.st)
		return err
	})
	if err != nil {
		return false, mirrorErr(err)
	}
	return serves, nil
}

// rotatedAway is whether a forward read from prev's second, a fill's or a tailer's,
// whose GET's first line came at oldest, found prev's line gone. Rotation drops the
// oldest lines first, so everything up to prev is gone too, and the GET began at the
// backend's oldest line: the read is head.
func rotatedAway(oldest time.Time, prev *logstore.Pos) bool {
	return prev != nil && !returnedWhole(oldest, prev)
}

// returnedWhole is whether a forward read from prev's second, whose GET's first line
// came at oldest, returned prev's moment whole, so it extends the chunk ending there.
func returnedWhole(oldest time.Time, prev *logstore.Pos) bool {
	return prev != nil && !oldest.IsZero() && oldest.UnixNano() <= prev.TS
}

// boundAt is the read's bound at p, a position of the stream, excluded; zero with
// none.
func (f *filler) boundAt(p *logstore.Pos) bound {
	if p == nil {
		return bound{}
	}
	return bound{f.ps.run().at(*p), false}
}

// segment is one span or one gap of a stream's coverage.
type segment struct {
	span *logstore.Span
	gap  *logstore.Gap
}

// segmentsFrom is cov's spans and gaps in the order a page reading mode from key
// meets them, less the ones wholly behind key: a span, or a gap with no place left
// for a line between key and its far edge.
func segmentsFrom(cov logstore.Coverage, key logstore.Pos, mode direction) []segment {
	var all []segment
	spans := cov.Spans
	for i := range cov.Gaps {
		// A span goes before the first gap that starts at or past its end.
		g := &cov.Gaps[i]
		for len(spans) > 0 && g.After != nil && spans[0].To.Compare(*g.After) <= 0 {
			all = append(all, segment{span: &spans[0]})
			spans = spans[1:]
		}
		all = append(all, segment{gap: g})
	}
	head := mode == newer
	if !head {
		slices.Reverse(all)
	}
	var out []segment
	for _, seg := range all {
		var behind bool
		switch {
		case seg.span != nil && head:
			behind = seg.span.To.Compare(key) <= 0
		case seg.span != nil:
			behind = seg.span.From.Compare(key) >= 0
		case head:
			behind = seg.gap.Before != nil && seg.gap.Before.Prev().Compare(key) <= 0
		default:
			behind = seg.gap.After != nil && seg.gap.After.Next().Compare(key) >= 0
		}
		if !behind {
			out = append(out, seg)
		}
	}
	return out
}

// gapRange is the part of g a page reading mode from key reads: its edges, nil for
// none, and the chunk edges among them, which a fill links to. Where key lies inside
// g, the range starts at key and links to nothing there.
func gapRange(g logstore.Gap, key logstore.Pos, mode direction) (lower, upper, prev, next *logstore.Pos) {
	lower, upper, prev, next = g.After, g.Before, g.After, g.Before
	if mode == newer && key != logstore.Oldest && (g.After == nil || key.Compare(*g.After) > 0) {
		lower, prev = &key, nil
	}
	if mode == older && key != logstore.Newest && (g.Before == nil || key.Compare(*g.Before) < 0) {
		upper, next = &key, nil
	}
	return lower, upper, prev, next
}
