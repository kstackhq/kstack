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
	stream: 16 << 20, gets: 16, query: 64 << 20,
	window: byteTarget, minWindow: 64 << 10, part: 16 << 10,
}

// pool is the bytes a query's streams share once their first windows have ended.
// It goes below zero when a window reads past what it reserved.
type pool struct {
	mu   sync.Mutex
	left int64
}

func (p *pool) add(n int64) {
	p.mu.Lock()
	p.left += n
	p.mu.Unlock()
}

func (p *pool) load() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.left
}

// reserve takes a window's reservation and answers its target: a quarter of what
// is left, at most most, with four times it taken. Both under one lock, so streams
// reserving at once take no more than the pool holds.
func (p *pool) reserve(most int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	target := max(min(most, p.left/4), 0)
	p.left -= 4 * target
	return target
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
	// held is what the window being read reserved and has not used.
	held int64
	// spent and gets are what the stream has downloaded and sent.
	spent int64
	gets  int
	// follow is a follow's fill, which is cut only at a second's end.
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
// of what is left of the allowance for its first, else the smallest of c.window,
// what is left of the stream's bytes, and a quarter of the pool.
func (l *limits) target() int64 {
	switch {
	case l == nil:
		return byteTarget
	case l.first:
		return l.allowance / 4
	}
	return max(min(l.caps.window, l.caps.stream-l.spent, l.pool.load()/4), 0)
}

// take is the target of the stream's next window of time, with four times it
// reserved, the most the window reads before it is stopped as too busy.
func (l *limits) take() int64 {
	switch {
	case l == nil:
		return byteTarget
	case l.first:
		target := l.allowance / 4
		l.held = 4 * target
		l.allowance -= l.held
		return target
	}
	target := l.pool.reserve(min(l.caps.window, l.caps.stream-l.spent))
	l.held = 4 * target
	return target
}

// windowEnded returns what the window did not use to the pool, with what is left of
// the allowance once the first window has ended.
func (l *limits) windowEnded() {
	if l == nil {
		return
	}
	back := l.held
	if l.first {
		back += l.allowance
	}
	l.pool.add(back)
	l.held, l.allowance, l.first = 0, 0, false
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
