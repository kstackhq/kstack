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
	// whether a Head page read the newest gap it may fill whole, to the stream's end
	// or to the tailer's start, which no chunk sits at: the walk crosses it.
	fromKey bool
	toEnd   bool
	// wrote is whether a write filed anything, and gone whether one found the stream's
	// row gone: the janitor took a gone pod's row with nothing in the file before the
	// page held it, and the page reads it as a stream no backend serves.
	wrote bool
	gone  bool
	// newest is the newest line the first backend wrote, nil with none.
	newest *logstore.Pos
}

// filler fills one stream for one page: the gaps in the page's direction from its
// bound go to the cluster's backends in order, until the stream holds need lines
// that pass the grep past the bound, has no live gap left that way, or has spent
// its share of the fills' budget. Each window of a read is written once it is whole.
type filler struct {
	file *logstore.Store
	// clusters and id are the mirror each write checks the stream's instance against,
	// and save writes one window's fills: a page's stamped, a follow's held.
	clusters cluster.Service
	id       cluster.ClusterID
	save     func(context.Context, []logstore.Fill) (logstore.Written, error)
	// ret cuts a window into the batches a write files.
	ret      logstore.Retention
	ps       pageStream
	backends []Backend
	names    []string
	q        pageQuery
	budget   *budget
	// walk is the stream's share of the walks' budget, which counting what the file
	// holds spends.
	walk *budget
	// held is whether a chunk of the page's pattern claims a line, as the walk keeps
	// only such lines.
	held func(logstore.Pos) bool
	// seam is the stream's line just before a Head page's bound, which the read from
	// the bound continues from; nil for none.
	seam  *logstore.Pos
	count int
	out   streamFill
}

// run fills the stream from key, its bound, and answers what it did. Only a fatal
// failure or one of the file is an error.
func (f *filler) run(ctx context.Context, key logstore.Pos) (streamFill, error) {
	chunks, err := f.file.Chunks(ctx, f.ps.row.ID, f.q.view.Grep)
	if err != nil {
		return streamFill{}, err
	}
	f.held = heldBy(chunks)
	segs := segmentsFrom(logstore.CoverageOf(chunks, f.names, f.ps.tail), key, f.q.mode)
	// Counting a span only decides whether to fill a gap past it, so the fill ends
	// at the last live gap.
	segs = segs[:lastLiveGap(segs)+1]
	first := true
	for _, seg := range segs {
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
func (f *filler) filled() bool { return f.count >= f.q.need() }

// countSpan counts the lines past key in sp that the walk keeps, and is whether the
// fill may go on past the span: the stream still needs lines, and the walks' budget
// is not spent.
func (f *filler) countSpan(ctx context.Context, sp logstore.Span, key logstore.Pos) (bool, error) {
	from, to := logstore.MaxPos(key, sp.From.Prev()), sp.To
	if f.q.mode == FetchTail {
		from, to = logstore.MinPos(key, sp.To.Next()), sp.From
	}
	counted := true
	err := f.file.Read(ctx, func(r logstore.Reader) error {
		return r.Lines(f.ps.row.ID, from, to, func(l logstore.Line) bool {
			if !f.walk.spend(len(l.Message)) {
				counted = false
				return false
			}
			if f.held(l.Pos) && matches(f.q.grep, l.Message) {
				f.count++
			}
			return !f.filled()
		})
	})
	return counted && !f.filled(), err
}

// fillGap reads g, a live gap, from the backends, and is whether the fill may go on
// past it: the gap was read whole, the stream still needs lines, and the read neither
// failed nor spent the budget. first is whether g is the first gap the fill is
// offered, the one the walk starts in.
func (f *filler) fillGap(ctx context.Context, g logstore.Gap, key logstore.Pos, first bool) (bool, error) {
	lower, upper, prev, next := gapRange(g, key, f.q.mode)
	if f.q.mode == FetchHead && lower != nil && prev == nil {
		// The read starts at the bound, inside the gap.
		prev = f.seam
	}
	whole, got, err := f.read(ctx, g.Tier, lower, upper, prev, next)
	if err != nil {
		return false, err
	}
	if first && (whole || got) {
		f.out.fromKey = true
	}
	if whole && f.q.mode == FetchHead && g.Newest() {
		f.out.toEnd = true
	}
	return whole && !f.filled() && f.out.err == nil && !f.budget.spent(), nil
}

// read reads the range between lower and upper, both excluded and nil for none, from
// the backend at tier, and writes what it read and proved. prev and next are the
// chunk edges the range runs from and to, nil where it does not reach one. It
// answers whether the read served every line in the range, and whether it read
// any. A backend that answers head offers what is older than its first line to the
// next.
func (f *filler) read(ctx context.Context, tier int, lower, upper, prev, next *logstore.Pos) (whole, got bool, err error) {
	p, ok, err := f.pull(ctx, tier, lower, upper, next)
	if !ok || err != nil {
		return false, false, err
	}
	oldest := p.oldest
	if len(p.last) > 0 {
		oldest = new(p.last[0].Pos)
	}
	got = oldest != nil
	if !got && !p.whole {
		// Nothing in range was read: the stream is skipped, or the page refused
		// when the retirement cut the read.
		if p.r.short {
			f.out.retired = errors.Is(p.r.err, errRetired)
		} else {
			f.out.err = p.r.err
		}
		return false, false, nil
	}
	// What the backend lacks below its first line goes to the next before anything
	// links across it: older than its oldest line, or a hole between prev and its
	// first, which it may have rotated away.
	head := f.headRead(p.r, lower, prev)
	offer := tier+1 < len(f.backends) && (head || f.hole(oldest, p.whole, prev))
	fill := f.fillOf(tier, p.r, p.last, p.whole, head, prev, p.up(next), upper, p.oldest == nil)
	if offer {
		fill.Prev = nil
	}
	if ok, err := f.write(ctx, fill); !ok {
		return false, false, err
	}
	if offer {
		// The moment of the backend's first line is its whole: the next is asked for
		// what is older than that moment.
		older, olderNext := upper, next
		if got {
			older, olderNext = &logstore.Pos{TS: oldest.TS, Ord: -1}, oldest
		}
		olderWhole, olderGot, err := f.read(ctx, tier+1, lower, older, prev, olderNext)
		return p.whole && olderWhole, got || olderGot, err
	}
	return p.whole, got, nil
}

// headRead is whether r reached the backend's oldest line: r says so, or it is a
// forward read from prev's second that found prev's line rotated away, and got a
// line or ended clean. A read from any other bound, a page's key, says nothing of
// rotation.
func (f *filler) headRead(r *streamRead, lower, prev *logstore.Pos) bool {
	fromPrev := f.q.mode == FetchHead && prev != nil && lower != nil && *lower == *prev
	if r.head || !fromPrev {
		return r.head
	}
	got, clean := !r.oldest.IsZero(), r.err == nil && !r.short
	return rotatedAway(r.oldest, prev) && (got || clean)
}

// hole is whether a backward read from prev left a range below its oldest line it
// may not have served: it reached back to prev with its oldest line past it, or
// found nothing at all in a range it read whole. A read that stopped short of prev,
// cut by the page filling or by its budget, says nothing of what lies below it. A
// forward read's hole is a head read.
func (f *filler) hole(oldest *logstore.Pos, whole bool, prev *logstore.Pos) bool {
	switch {
	case prev == nil, f.q.mode == FetchHead:
		return false
	case oldest == nil:
		return whole
	}
	return whole && oldest.TS > prev.TS
}

// pulled is one backend read as pull ended it: the read; its last window, oldest
// first, which the read's end decides how to link; and whether the read served every
// line in the range. oldest is the oldest line the windows before the last wrote, nil
// with none.
type pulled struct {
	r      *streamRead
	last   []logstore.Line
	whole  bool
	oldest *logstore.Pos
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

// pull runs the backend read of the range, counting the lines that pass the grep and
// cancelling the read once the stream holds what the page needs. A window is written
// once the next one starts, so it was not the read's last; the last is answered. It
// is false once a write ended the read. A read the page's context ended is an error.
func (f *filler) pull(ctx context.Context, tier int, lower, upper, next *logstore.Pos) (pulled, bool, error) {
	o := readOpts{since: f.boundAt(lower), until: f.boundAt(upper), budget: f.budget, batch: int64(f.q.need()), windows: maxWindows}
	var r *streamRead
	if f.q.mode == FetchHead {
		r = f.backends[tier].Forward(ctx, *f.ps.st, o)
	} else {
		r = f.backends[tier].Backward(ctx, *f.ps.st, o)
	}
	out := pulled{r: r}
	// cur is the window arriving, done the last whole one, not yet written.
	var cur, done []logstore.Line
	cut := false
	for rec := range r.lines.Chan() {
		if rec.windowEnd {
			if len(cur) > 0 {
				done, cur = cur, nil
			}
			continue
		}
		if done != nil {
			// Only a backward read ends a window before its last. Its window links up
			// and down to nothing, since the line below it comes in a later window: no
			// link says nothing lies between until every line between was read.
			slices.Reverse(done)
			window := f.fillOf(tier, r, done, false, false, nil, out.up(next), upper, out.oldest == nil)
			if ok, err := f.write(ctx, window); !ok || err != nil {
				r.cancel()
				drainReading(r)
				return pulled{}, false, err
			}
			// A copy, so the window's lines are not kept alive by it.
			out.oldest, done = new(done[0].Pos), nil
		}
		cur = append(cur, lineOf(rec.Record))
		if matches(f.q.grep, rec.Message) {
			f.count++
		}
		if f.filled() {
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
	// A line of cur would have written done, so at most one holds lines.
	out.last = cur
	if len(cur) == 0 {
		out.last = done
	}
	if f.q.mode == FetchTail {
		slices.Reverse(out.last)
	}
	out.whole = !cut && r.err == nil && !r.short
	return out, true, nil
}

// atEnd is whether lines, a window of a backward read from upper, hold the instance's
// last line: the read's first window, first, from the end of a finished instance.
// Lines arrive newest first, so a window cut part way holds it too.
func (f *filler) atEnd(upper *logstore.Pos, first bool, lines []logstore.Line) bool {
	return upper == nil && first && f.ps.st.finished() && len(lines) > 0
}

// write files window, one window's fill, in batches, once the mirror shows the
// stream's pod still runs the instance it was resolved as, and is whether the read
// may go on. The mirror lags a restart, while the kubelet serves whichever instance
// runs now: a stream that restarted is skipped, and what it wrote before stays.
func (f *filler) write(ctx context.Context, window logstore.Fill) (bool, error) {
	serves, err := f.serves(ctx)
	if err != nil {
		return false, err
	}
	if !serves {
		f.out = streamFill{err: errRestarted, wrote: f.out.wrote}
		return false, nil
	}
	w, err := f.save(ctx, batches(window, f.ret))
	if err != nil {
		return false, err
	}
	if len(w.Skipped) > 0 {
		f.out.gone = true
		return false, nil
	}
	f.out.wrote = true
	// A Tail fill writes newest first, so the first write holds the newest line.
	if f.out.newest == nil && window.Origin == f.names[0] && len(window.Lines) > 0 {
		f.out.newest = new(window.Lines[len(window.Lines)-1].Pos)
	}
	return true, nil
}

// batches is window cut into fills of at most ret's WriteBatch lines or
// WriteBatchBytes, oldest first: the first keeps its Prev, Merge and Head, each next
// one extends the chunk the one before it ends, and the last keeps its Next and AtEnd.
func batches(window logstore.Fill, ret logstore.Retention) []logstore.Fill {
	if len(window.Lines) == 0 {
		return []logstore.Fill{window}
	}
	var out []logstore.Fill
	for lines := window.Lines; len(lines) > 0; {
		n, bytes := 0, int64(0)
		for n < len(lines) && n < ret.WriteBatch && bytes < ret.WriteBatchBytes {
			bytes += int64(len(lines[n].Message))
			n++
		}
		b := window
		b.Lines, b.Next, b.AtEnd = lines[:n], nil, false
		if len(out) > 0 {
			before := out[len(out)-1].Lines
			b.Prev, b.Merge, b.Head = &before[len(before)-1].Pos, true, false
		}
		out = append(out, b)
		lines = lines[n:]
	}
	out[len(out)-1].Next, out[len(out)-1].AtEnd = window.Next, window.AtEnd
	return out
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

// fillOf is the fill of lines, a window the backend at tier read of the range
// between prev and next, as Write files it: the chunk it continues, the chunk it
// links to, and whether it reached the backend's oldest line, head, or the instance's
// last. first is whether no window of the read came before it. A head read links to
// nothing below it, which leaves a lost gap there.
func (f *filler) fillOf(tier int, r *streamRead, lines []logstore.Line, whole, head bool, prev, next, upper *logstore.Pos, first bool) logstore.Fill {
	fill := logstore.Fill{Stream: f.ps.row.ID, Origin: f.names[tier], Lines: lines, Head: head}
	if head {
		prev = nil
	}
	if f.q.mode == FetchHead {
		// A forward read that is not head starts at the line after prev, so its first
		// line continues prev's chunk.
		fill.Prev = prev
		fill.Merge = returnedWhole(r.oldest, prev)
		if whole {
			fill.Next = next
		}
		fill.AtEnd = r.atEnd
		return fill
	}
	// A backward read takes every line back from upper, so its newest line meets
	// next's chunk whether or not it reached lower.
	fill.Next = next
	if whole {
		fill.Prev = prev
	}
	fill.AtEnd = f.atEnd(upper, first, lines)
	return fill
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
	return bound{positionAt(f.ps.instance(), *p), false}
}

// positionAt is the position of in's line at p.
func positionAt(in instance, p logstore.Pos) position {
	return position{time.Unix(0, p.TS).UTC(), in.namespace, in.pod, in.container, in.id, p.Ord}
}

// segment is one span or one gap of a stream's coverage.
type segment struct {
	span *logstore.Span
	gap  *logstore.Gap
}

// segmentsFrom is cov's spans and gaps in the order a page reading mode from key
// meets them, less the ones wholly behind key: a span, or a gap with no place left
// for a line between key and its far edge.
func segmentsFrom(cov logstore.Coverage, key logstore.Pos, mode FetchMode) []segment {
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
	head := mode == FetchHead
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
func gapRange(g logstore.Gap, key logstore.Pos, mode FetchMode) (lower, upper, prev, next *logstore.Pos) {
	lower, upper, prev, next = g.After, g.Before, g.After, g.Before
	if mode == FetchHead && key != logstore.Oldest && (g.After == nil || key.Compare(*g.After) > 0) {
		lower, prev = &key, nil
	}
	if mode == FetchTail && key != logstore.Newest && (g.Before == nil || key.Compare(*g.Before) < 0) {
		upper, next = &key, nil
	}
	return lower, upper, prev, next
}
