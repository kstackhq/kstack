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

import "sync"

// caps is the limits a page's reads keep to, as bytes the body carries.
type caps struct {
	// second is the most of one second of one stream's log a page reads: a louder
	// second stops the stream there.
	second int64
	// stream and gets are the bytes one stream downloads and the GETs it sends for
	// one query, and query the bytes all its streams download together.
	stream int64
	gets   int
	query  int64
	// window is the most a later window is sized to hold, and minWindow the least:
	// under it the stream is cut, and a stopped window is replaced by the
	// one-second window.
	window, minWindow int64
	// part is the runtime's part size, the most a line of tailLines counts holds.
	part int64
}

var defaultCaps = caps{
	second: 1 << 20, stream: 16 << 20, gets: 16, query: 64 << 20,
	window: byteTarget, minWindow: 64 << 10, part: 16 << 10,
}

// pool is the bytes a query's streams share once their first windows have ended.
// reading is the streams past their first window still filling, and holding those
// among them with a reservation out. It goes below zero when a window reads past
// what it reserved.
type pool struct {
	mu               sync.Mutex
	left             int64
	reading, holding int
}

func (p *pool) add(n int64) {
	p.mu.Lock()
	p.left += n
	p.mu.Unlock()
}

// give returns n bytes a window did not use and moves the counts of streams reading
// and reservations out by reading and holding, in one step: a stream's share is
// never seen with one changed and not the other.
func (p *pool) give(n int64, reading, holding int) {
	p.mu.Lock()
	p.left += n
	p.reading += reading
	p.holding += holding
	p.mu.Unlock()
}

func (p *pool) load() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.left
}

// target is what a window reading from the pool is sized to hold: a quarter of the
// stream's share of what is left, at most most.
func (p *pool) target(most int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quarter(most)
}

// reserve takes a window's reservation and answers its target, with four times it
// taken. Both under one lock, so streams reserving at once take no more than the
// pool holds.
func (p *pool) reserve(most int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	target := p.quarter(most)
	p.left -= 4 * target
	p.holding++
	return target
}

// quarter is a quarter of one stream's share of what is left, at most most. The
// share divides what is left among the streams reading with no reservation out,
// so each of them finds the same share. The caller holds mu.
func (p *pool) quarter(most int64) int64 {
	return max(min(most, p.left/4/int64(max(p.reading-p.holding, 1))), 0)
}

// limits is one stream's account of one query. Its read alone touches it while the
// read runs, and its fill once the read has ended. A nil limits is no limit.
type limits struct {
	caps caps
	pool *pool
	// allowance is the stream's own bytes, spent until its first window ends; first
	// is whether it has not ended yet.
	allowance int64
	first     bool
	// held is what the window being read reserved and has not used, and holding
	// whether it reserved it from the pool.
	held    int64
	holding bool
	// spent and gets are what the stream has downloaded and sent.
	spent int64
	gets  int
	// follow is a follow's fill, which reads every second whole: no second stops it,
	// and it is cut only at a second's end.
	follow bool
}

// queryLimits is the limits of each of a query's n streams: each allowed the smaller
// of c.stream and c.query ÷ n, and the pool seeded with what the allowances leave.
func queryLimits(c caps, n int) []*limits {
	allowance := min(c.stream, c.query/int64(n))
	p := &pool{left: c.query - allowance*int64(n)}
	out := make([]*limits, n)
	for i := range out {
		out[i] = &limits{caps: c, pool: p, allowance: allowance, first: true}
	}
	return out
}

// followLimits is the limits of a follow's gap fill, one of n streams: a forward
// read under the smaller of c.stream and c.query ÷ n, so a round of fills reads no
// more than one query.
func followLimits(c caps, n int) *limits {
	return &limits{caps: c, pool: &pool{}, allowance: min(c.stream, c.query/int64(max(n, 1))), first: true, follow: true}
}

// charge counts n bytes downloaded: from the window's reservation, then the
// allowance while the first window has not ended, then the pool.
func (l *limits) charge(n int64) {
	if l == nil {
		return
	}
	l.spent += n
	take := min(n, l.held)
	l.held -= take
	n -= take
	if l.first {
		take = min(n, l.allowance)
		l.allowance -= take
		n -= take
	}
	l.pool.add(-n)
}

// get counts a GET.
func (l *limits) get() {
	if l != nil {
		l.gets++
	}
}

// target is the bytes the stream's next window of time is sized to hold: a quarter
// of what is left of the allowance for its first, else the pool's target under most.
func (l *limits) target() int64 {
	switch {
	case l == nil:
		return byteTarget
	case l.first:
		return l.allowance / 4
	}
	return l.pool.target(l.most())
}

// take is the target of the stream's next window of time, with four times it
// reserved, the most the window reads before it is stopped as too busy.
func (l *limits) take() int64 {
	var target int64
	switch {
	case l == nil:
		return byteTarget
	case l.first:
		target = l.allowance / 4
		l.allowance -= 4 * target
	default:
		target = l.pool.reserve(l.most())
		l.holding = true
	}
	l.held = 4 * target
	return target
}

// most is the most a later window is sized to hold: c.window, or what is left of the
// stream's bytes when that is less.
func (l *limits) most() int64 {
	return min(l.caps.window, l.caps.stream-l.spent)
}

// windowEnded returns what the window did not use to the pool, with what is left of
// the allowance once the first window has ended, from when the stream reads from
// the pool.
func (l *limits) windowEnded() { l.settle(false) }

// ended returns what the stream holds and leaves the pool: its fill reads no more.
func (l *limits) ended() { l.settle(true) }

// settle is windowEnded, and ended when left, with the pool's counts moved in the
// same step as its bytes.
func (l *limits) settle(left bool) {
	if l == nil {
		return
	}
	back, reading, holding := l.held, 0, 0
	if l.first {
		back += l.allowance
		reading++
	}
	if left {
		reading--
	}
	if l.holding {
		holding--
	}
	l.pool.give(back, reading, holding)
	l.held, l.allowance, l.first, l.holding = 0, 0, false, false
}

// usedUp is whether the stream has spent its bytes or sent its GETs, after which its
// fill reads no further gap.
func (l *limits) usedUp() bool {
	return l != nil && (l.spent >= l.caps.stream || l.gets >= l.caps.gets)
}

// reached is whether the stream is cut at the end of a window: it is used up, or its
// next window would be sized under the least.
func (l *limits) reached() bool {
	return l != nil && (l.usedUp() || l.target() < l.caps.minWindow)
}

// forwardSpent is whether a forward read is cut: it has spent the stream's bytes,
// or both its allowance and the pool are empty.
func (l *limits) forwardSpent() bool {
	return l != nil && (l.spent >= l.caps.stream || l.allowance <= 0 && l.pool.load() <= 0)
}

// tailLines is the lines the tailLines window asks for to hold need: no more than
// the allowance holds at a part a line, and at least minBatch.
func (l *limits) tailLines(need int64) int64 {
	if l != nil {
		need = min(need, l.allowance/l.caps.part)
	}
	return max(need, minBatch)
}

// second is the most one second of the log may hold before it stops the read, zero
// for a read no second stops: one under no limits, or a follow's fill.
func (l *limits) second() int64 {
	if l == nil || l.follow {
		return 0
	}
	return l.caps.second
}
