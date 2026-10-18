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
	"math"
	"time"
)

// A backward read's windows of time: how far the first reaches with no rate
// measured near the point, and the most bytes a window is sized to hold.
const (
	defaultReach = 10 * time.Second
	byteTarget   = 4 << 20
)

// maxReach is the reach of a window sized from a rate of no bytes: as far as a
// duration goes, so the read's lower bound or the log's first line ends it.
const maxReach = time.Duration(math.MaxInt64)

// A rate sample's bounds: the fewest lines a rate is measured from, and the most
// a sample reads toward a second's span, the busiest second of a normal log.
const (
	minSample = 10
	maxSample = 5000
)

// rate is how busy a log is: kept lines and bytes a second.
type rate struct{ kept, bytes float64 }

// windowFor is how far back a window reaches to hold about need kept lines at r,
// with half again for a margin, and no more than target bytes: whole seconds, at
// least one. The margin is on the kept lines alone, so a window sized by bytes
// holds about its target.
func windowFor(need int64, r rate, target int64) time.Duration {
	secs := math.Inf(1)
	if r.bytes > 0 {
		secs = float64(target) / r.bytes
	}
	if r.kept > 0 {
		secs = min(secs, 1.5*float64(need)/r.kept)
	}
	if secs >= maxReach.Seconds() {
		return maxReach
	}
	return max(time.Duration(math.Ceil(secs))*time.Second, time.Second)
}

// wireBytes is the size the log endpoint's body carries a stored line in: its
// timestamp, a space, its message and a newline. The file holds the message
// cleaned and cut, so a rate measured from it is an estimate; a window counts the
// bytes its body carried.
func wireBytes(ts time.Time, msg string) int64 {
	return int64(len(ts.UTC().Format(time.RFC3339Nano)) + len(msg) + 2)
}

// sample is the lines a rate is measured from: how many, how many the grep keeps,
// their bytes, and the moments of the first and the last.
type sample struct {
	lines, kept int64
	bytes       int64
	first, last time.Time
}

// add counts a line at ts of bytes, kept when the grep passes it.
func (s *sample) add(ts time.Time, bytes int64, kept bool) {
	if s.lines == 0 {
		s.first = ts
	}
	s.lines++
	if kept {
		s.kept++
	}
	s.bytes += bytes
	s.last = ts
}

// short is whether a sample taken next to a read's point wants more lines: the
// first need and at least minSample, then on until they span a second or reach
// maxSample, since a sample shorter than a second is measured over one.
func (s sample) short(need int64) bool {
	if s.lines < max(need, minSample) {
		return true
	}
	return s.last.Sub(s.first) < time.Second && s.lines < maxSample
}

// rate is the sample's rate over the time its lines span, at least a second, since
// lines all in one moment span none; false under minSample lines.
func (s sample) rate() (rate, bool) {
	if s.lines < minSample {
		return rate{}, false
	}
	return s.per(max(s.last.Sub(s.first), time.Second)), true
}

// per is the sample's rate over d.
func (s sample) per(d time.Duration) rate {
	secs := d.Seconds()
	return rate{kept: float64(s.kept) / secs, bytes: float64(s.bytes) / secs}
}

// plan is what a window was sized for: the kept lines the read needs, and the
// window's byte target.
type plan struct {
	need, target int64
}

// tooBusy is whether a window that has read w, whose newest second reads at recent,
// with left still to read, is due to hold far more than planned: four times need
// kept lines or four times its target, the bytes it reserved. Each waits for the
// window to have read part of its share first, since its first few lines often
// share a millisecond and project a rate far above the true one; lines all in one
// moment project past measure, so a window of them stops once a wait has passed.
func (p plan) tooBusy(w sample, recent rate, left time.Duration) bool {
	if left <= 0 {
		return false
	}
	secs := left.Seconds()
	kept := float64(w.kept) + recent.kept*secs
	bytes := float64(w.bytes) + recent.bytes*secs
	switch {
	case w.kept >= p.need && kept > 4*float64(p.need):
		return true
	case w.bytes >= p.target/4 && bytes > 4*float64(p.target):
		return true
	}
	return false
}

// recent is the lines of the one second ending at the newest line a window has
// read. from is the moment the window began at, and covered how much of that
// second the window has read: a second, or less while the window began under a
// second ago.
type recent struct {
	from        time.Time
	lines       []recentLine
	kept, bytes int64
	covered     time.Duration
}

type recentLine struct {
	ts    time.Time
	bytes int64
	kept  bool
}

// add counts a line at ts of bytes, kept or not, and lets go of the lines more than
// a second older.
func (q *recent) add(ts time.Time, bytes int64, kept bool) {
	q.lines = append(q.lines, recentLine{ts, bytes, kept})
	q.bytes += bytes
	if kept {
		q.kept++
	}
	for !q.lines[0].ts.After(ts.Add(-time.Second)) {
		old := q.lines[0]
		q.lines = q.lines[1:]
		q.bytes -= old.bytes
		if old.kept {
			q.kept--
		}
	}
	q.covered = min(ts.Sub(q.from), time.Second)
}

// rate is the newest second's rate over the part of it the window has read, past
// measure while that is none. Dividing by the lines' own span instead would read
// one line of a quiet log as a rate past measure.
func (q *recent) rate() rate {
	per := func(n int64) float64 {
		if n == 0 {
			return 0
		}
		if q.covered <= 0 {
			return math.Inf(1)
		}
		return float64(n) / q.covered.Seconds()
	}
	return rate{kept: per(q.kept), bytes: per(q.bytes)}
}
