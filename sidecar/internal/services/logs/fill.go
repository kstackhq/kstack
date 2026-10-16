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

// streamFill is what a page's fills did for one stream: the reads to write, the
// failure that skips the stream, and what the walk needs to know of the reads.
type streamFill struct {
	fills []logstore.Fill
	err   error
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
}

// filler fills one stream for one page: the gaps in the page's direction from its
// bound go to the cluster's backends in order, until the stream holds need lines
// that pass the grep past the bound, has no live gap left that way, or has spent
// its share of the fills' budget. Its reads are buffered for the write.
type filler struct {
	file     *logstore.Store
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
// the backend at tier, and buffers what it read and proved. prev and next are the
// chunk edges the range runs from and to, nil where it does not reach one. It
// answers whether the read served every line in the range, and whether it read
// any. A backend that answers head offers what is older than its first line to the
// next.
func (f *filler) read(ctx context.Context, tier int, lower, upper, prev, next *logstore.Pos) (whole, got bool, err error) {
	lines, r, whole, err := f.pull(ctx, tier, lower, upper)
	if err != nil {
		return false, false, err
	}
	got = len(lines) > 0
	if !got && !whole {
		// Nothing in range was read: the stream is skipped, or the page refused
		// when the retirement cut the read.
		if r.short {
			f.out.retired = errors.Is(r.err, errRetired)
		} else {
			f.out.err = r.err
		}
		return false, false, nil
	}
	// What the backend lacks below its first line goes to the next before anything
	// links across it: older than its oldest line, or a hole between prev and its
	// first, which it may have rotated away.
	offer := tier+1 < len(f.backends) && (r.head || f.hole(r, lines, whole, prev))
	fill := f.fillOf(tier, r, lines, whole, prev, next, upper)
	if offer {
		fill.Prev = nil
	}
	f.out.fills = append(f.out.fills, fill)
	if offer {
		// The moment of the backend's first line is its whole: the next is asked for
		// what is older than that moment.
		older, olderNext := upper, next
		if got {
			older, olderNext = &logstore.Pos{TS: lines[0].TS, Ord: -1}, &lines[0].Pos
		}
		olderWhole, olderGot, err := f.read(ctx, tier+1, lower, older, prev, olderNext)
		return whole && olderWhole, got || olderGot, err
	}
	return whole, got, nil
}

// hole is whether a read from prev left a range below its first line it did not
// serve: a forward read whose GET began past prev's moment, a backward read that
// reached back to prev with its oldest line past it, or one that found nothing at
// all in a range it read whole. A backward read that stopped short of prev, cut by
// the page filling or by its budget, says nothing of what lies below it.
func (f *filler) hole(r *streamRead, lines []logstore.Line, whole bool, prev *logstore.Pos) bool {
	switch {
	case prev == nil:
		return false
	case len(lines) == 0:
		return whole
	case f.q.mode == FetchHead:
		return r.oldest.UnixNano() > prev.TS
	}
	return whole && lines[0].TS > prev.TS
}

// pull runs the backend read of the range and buffers its lines, oldest first,
// counting the ones that pass the grep and cancelling the read once the stream
// holds what the page needs. It answers the lines, the read, and whether the read
// served every line in the range. A read the page's context ended is an error.
func (f *filler) pull(ctx context.Context, tier int, lower, upper *logstore.Pos) ([]logstore.Line, *streamRead, bool, error) {
	o := readOpts{since: f.boundAt(lower), until: f.boundAt(upper), budget: f.budget, batch: int64(f.q.need()), windows: maxWindows}
	var r *streamRead
	if f.q.mode == FetchHead {
		r = f.backends[tier].Forward(ctx, *f.ps.st, o)
	} else {
		r = f.backends[tier].Backward(ctx, *f.ps.st, o)
	}
	var lines []logstore.Line
	cut := false
	for rec := range r.lines.Chan() {
		lines = append(lines, lineOf(rec))
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
		return nil, nil, false, err
	}
	if err := fatalError(r.err); err != nil {
		return nil, nil, false, err
	}
	if f.q.mode == FetchTail {
		slices.Reverse(lines)
	}
	return lines, r, !cut && r.err == nil && !r.short, nil
}

// fillOf is the fill of lines, what the backend at tier read of the range between
// prev and next, as Write files it: the chunk it continues, the chunk it links to,
// and whether it reached the backend's oldest line or the instance's last.
func (f *filler) fillOf(tier int, r *streamRead, lines []logstore.Line, whole bool, prev, next, upper *logstore.Pos) logstore.Fill {
	fill := logstore.Fill{Stream: f.ps.row.ID, Origin: f.names[tier], Lines: lines, Head: r.head}
	if f.q.mode == FetchHead {
		// A forward read starts at the line after prev or at the first the backend
		// still has, so its first line continues prev's chunk either way.
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
	fill.AtEnd = upper == nil && f.ps.st.finished() && r.err == nil && len(lines) > 0
	return fill
}

// returnedWhole is whether a forward read from prev's second, whose GET's first line
// came at oldest, returned prev's moment whole, so it extends the chunk ending there.
func returnedWhole(oldest time.Time, prev *logstore.Pos) bool {
	return prev != nil && !oldest.IsZero() && oldest.UnixNano() <= prev.TS
}

// lineOf is rec as the file stores it.
func lineOf(rec Record) logstore.Line {
	return logstore.Line{Pos: logstore.Pos{TS: rec.Timestamp.UnixNano(), Ord: rec.Ordinal}, Message: rec.Message}
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

// checkInstances throws away the reads of each stream whose pod no longer runs the
// instance it was resolved as, and skips it in every direction: the mirror lags a
// restart, while the kubelet serves whichever instance runs now. One reading of the
// mirror checks every stream that read anything, after its reads and before their
// write. fills holds each direction's fills by stream.
func checkInstances(ctx context.Context, clusters cluster.Service, id cluster.ClusterID, streams []pageStream, fills [][]streamFill) error {
	var read []int
	for i := range streams {
		if slices.ContainsFunc(fills, func(byStream []streamFill) bool { return len(byStream[i].fills) > 0 }) {
			read = append(read, i)
		}
	}
	if len(read) == 0 {
		return nil
	}
	var moved []int
	err := clusters.CachedData().ReadActive(ctx, id, maxRows, maxBytes, func(q cluster.CacheReading, _ cluster.ActiveCluster) error {
		// Set whole, never appended to: ReadActive may run this twice.
		moved = nil
		for _, i := range read {
			serves, err := reading{q}.serves(*streams[i].st)
			if err != nil {
				return err
			}
			if !serves {
				moved = append(moved, i)
			}
		}
		return nil
	})
	if err != nil {
		return mirrorErr(err)
	}
	for _, i := range moved {
		for _, byStream := range fills {
			byStream[i] = streamFill{err: errRestarted}
		}
	}
	return nil
}
