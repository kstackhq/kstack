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
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// web is the container app of pod web in prod.
var web = RecordSource{Namespace: "prod", Pod: "web", Container: "app"}

// A forward read since a moment asks for its second and keeps that moment's line.
func TestAForwardReadSinceAMoment(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(500*time.Millisecond, "zero"), stamped(1200*time.Millisecond, "one"), stamped(2*time.Second, "two"))

	r := readForward(t.Context(), s.get, web, readOpts{since: from(1200 * time.Millisecond)})

	recs := drain(r)
	require.NoError(t, r.err)
	assert.False(t, r.short)
	assert.Equal(t, []string{"one", "two"}, messages(recs))
	assert.Equal(t, web, recs[0].Source)
	assert.Equal(t, at(1200*time.Millisecond), recs[0].Timestamp)
	q := s.requests()[0]
	assert.Equal(t, "2026-10-08T10:00:01Z", q.Get("sinceTime"))
	assert.Equal(t, "true", q.Get("timestamps"))
	assert.Equal(t, "app", q.Get("container"))
}

// After a moment inside a second, the read asks for that second and drops the
// lines at and before the moment.
func TestAfterAMomentDropsItsLines(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, "a"), stamped(1200*time.Millisecond, "cursor"), stamped(1200*time.Millisecond, "cursor too"), stamped(1500*time.Millisecond, "b"))

	r := readForward(t.Context(), s.get, web, readOpts{since: after(1200 * time.Millisecond)})

	assert.Equal(t, []string{"b"}, messages(drain(r)))
	assert.Equal(t, "2026-10-08T10:00:01Z", s.requests()[0].Get("sinceTime"))
}

// Lines of one moment are numbered in order, and a bound at one of them keeps or
// drops that line alone.
func TestLinesOfOneMomentAreNumbered(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, "a"), stamped(time.Second, "b"), stamped(time.Second, "c"), stamped(2*time.Second, "d"))
	get := s.get

	recs := drain(readForward(t.Context(), get, web, readOpts{}))
	ordinals := []int{}
	for _, rec := range recs {
		ordinals = append(ordinals, rec.Ordinal)
	}
	assert.Equal(t, []int{0, 1, 2, 0}, ordinals)

	after := readForward(t.Context(), get, web, readOpts{since: bound{lineAt(web, time.Second, 1), false}})
	assert.Equal(t, []string{"c", "d"}, messages(drain(after)))
	until := readForward(t.Context(), get, web, readOpts{until: bound{lineAt(web, time.Second, 1), true}})
	assert.Equal(t, []string{"a", "b"}, messages(drain(until)))
}

// A read stops at its until, which keeps its own line.
func TestAForwardReadStopsAtUntil(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "b"), stamped(2*time.Second, "c"))

	r := readForward(t.Context(), s.get, web, readOpts{until: to(time.Second)})

	assert.Equal(t, []string{"a", "b"}, messages(drain(r)))
}

// The previous instance is asked for, and its lines say so.
func TestThePreviousInstanceIsSent(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app/previous", stamped(0, "before the restart"))
	src := web
	src.Previous = true

	r := readForward(t.Context(), s.get, src, readOpts{})

	recs := drain(r)
	assert.Equal(t, []string{"before the restart"}, messages(recs))
	assert.True(t, recs[0].Source.Previous)
	assert.Equal(t, "true", s.requests()[0].Get("previous"))
}

// A follow since a moment reads the lines from it, then one written after it opened.
func TestAFollowDeliversALineWrittenLater(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "old"))

	r := readForward(t.Context(), claimOn(t, s).get, web, readOpts{since: from(0), follow: true})

	assert.Equal(t, "old", testutil.Recv(t, r.lines, "the old line").Message)
	s.follow("prod/web/app") <- stamped(time.Second, "new")
	assert.Equal(t, "new", testutil.Recv(t, r.lines, "the new line").Message)
	assert.Equal(t, "true", s.requests()[0].Get("follow"))
}

// A follow numbers a moment's lines from the top of its second, as a read opened
// again from a cursor in it does, so a line keeps its ordinal across the two.
func TestAFollowNumbersAMomentFromItsSecond(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(100*time.Millisecond, "a"), stamped(100*time.Millisecond, "b"))

	r := readForward(t.Context(), claimOn(t, s).get, web, readOpts{follow: true, since: from(100 * time.Millisecond)})
	first := testutil.Recv(t, r.lines, "the first line")
	assert.Equal(t, 0, first.Ordinal)

	again := readForward(t.Context(), claimOn(t, s).get, web, readOpts{follow: true, since: bound{positionOf(first), false}})
	assert.Equal(t, "b", testutil.Recv(t, again.lines, "the line after it").Message)
	assert.Empty(t, s.requests()[0].Get("tailLines"))
}

// A read the connection's retirement cut stops short at the last line it read
// whole, and says why.
func TestARetiredReadStopsShort(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "b"))
	get := func(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
		body, err := claimOn(t, s).get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		return cutAfter{body, 1, errRetired}, nil
	}

	r := readForward(t.Context(), get, web, readOpts{})

	assert.Equal(t, []string{"a"}, messages(drain(r)))
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web, 0, 0), r.stop)
	assert.ErrorIs(t, r.err, errRetired)
}

// A read that fails past its first line in range stops short at the last line it
// read whole, keeping its failure; one that fails before any line is not short.
func TestAReadThatFailsPartwayStopsShort(t *testing.T) {
	reset := errors.New("connection reset")
	for n, short := range map[int]bool{1: true, 0: false} {
		s := newLogServer()
		s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "b"))
		get := func(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
			body, err := s.get(ctx, path, q)
			if err != nil {
				return nil, err
			}
			return cutAfter{body, n, reset}, nil
		}

		r := readForward(t.Context(), get, web, readOpts{})

		assert.Len(t, drain(r), n)
		assert.Equal(t, short, r.short, n)
		if short {
			assert.Equal(t, lineAt(web, 0, 0), r.stop)
		}
		assert.ErrorIs(t, r.err, reset)
	}
}

// cutAfter is a body that ends with err after n lines.
type cutAfter struct {
	io.ReadCloser
	n   int
	err error
}

func (b cutAfter) Read(p []byte) (int, error) {
	data, err := io.ReadAll(b.ReadCloser)
	if err != nil {
		return 0, err
	}
	lines := strings.SplitAfterN(string(data), "\n", b.n+1)
	return copy(p, strings.Join(lines[:b.n], "")), b.err
}

// A message is cleaned before it is sent, and grep matches the cleaned text.
func TestALineIsCleanedAndGrepped(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "\x1b[31merror\x1b[0m: disk"), stamped(time.Second, "ok"))

	r := readForward(t.Context(), s.get, web, readOpts{grep: regexp.MustCompile(`^error: `)})

	assert.Equal(t, []string{"error: disk"}, messages(drain(r)))
}

// A line with no timestamp, or one that is not a time, is skipped, as kubetail's
// reader skips it.
func TestALineWithNoTimestampIsSkipped(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", "garbage", "not a time x", stamped(0, "a"))

	r := readForward(t.Context(), s.get, web, readOpts{})

	assert.Equal(t, []string{"a"}, messages(drain(r)))
}

// A line past 64 KiB is read to there and the rest skipped, so the cleaned message
// is cut at 16 KiB, and the line after it is read whole.
func TestALongLineIsCut(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, strings.Repeat("x", maxLine+100)), stamped(time.Second, "after"))

	r := readForward(t.Context(), s.get, web, readOpts{})

	recs := drain(r)
	require.Len(t, recs, 2)
	assert.Equal(t, strings.Repeat("x", maxMessage)+"…", recs[0].Message)
	assert.Equal(t, "after", recs[1].Message)
}

// A container the API server refuses ends the read with its status.
func TestARefusedReadEndsWithItsStatus(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web/app"] = refusal{400, "container is waiting to start"}

	r := readForward(t.Context(), s.get, web, readOpts{})

	assert.Empty(t, drain(r))
	assert.EqualError(t, r.err, "container is waiting to start")
}

// A read whose context ends while a line waits to be taken ends clean.
func TestACancelledReadEndsClean(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(3)...)
	get := s.get
	for name, read := range map[string]func(context.Context) *reading{
		"forward":  func(ctx context.Context) *reading { return readForward(ctx, get, web, readOpts{}) },
		"backward": func(ctx context.Context) *reading { return readBackward(ctx, get, web, backwardOpts()) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			r := read(ctx)
			testutil.Recv(t, r.lines, "the first line")

			cancel()

			testutil.WaitClosed(t, r.lines, "the read to end")
			assert.NoError(t, r.err)
			assert.False(t, r.short)
		})
	}
}

// A forward read that spends its budget stops at the last line it read whole,
// whether or not the grep kept it.
func TestAForwardReadStopsAtTheBudget(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(1000)...)

	r := readForward(t.Context(), s.get, web, readOpts{budget: newBudget(4 << 10), grep: regexp.MustCompile(`never`)})

	assert.Empty(t, drain(r))
	assert.NoError(t, r.err)
	assert.True(t, r.short)
	assert.Equal(t, "app", r.stop.Container)
	assert.True(t, r.stop.Timestamp.After(at(0)))
	assert.True(t, r.stop.Timestamp.Before(at(999*time.Second)))
}

// A forward read whose budget is spent before its first whole line has no stop: it
// read nothing in range.
func TestAForwardReadSpentBeforeItsFirstLineHasNoStop(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, strings.Repeat("x", 8<<10)))

	r := readForward(t.Context(), s.get, web, readOpts{since: from(time.Second), budget: newBudget(1 << 10)})

	assert.Empty(t, drain(r))
	assert.True(t, r.short)
	assert.True(t, r.stop.isZero())
}

// lineEvery is n lines a second apart from 10:00, saying their index.
func lineEvery(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = stamped(time.Duration(i)*time.Second, strconv.Itoa(i))
	}
	return out
}

// backwardOpts is a backward read in windows of 10 lines, with budget to spare.
func backwardOpts() readOpts {
	return readOpts{batch: 10, windows: maxWindows, budget: newBudget(maxBudget)}
}

// tails is the tailLines of each request after the first, which finds the first
// line, "" for the read of the first moment.
func tails(s *logServer) []string {
	var out []string
	for _, q := range s.requests()[1:] {
		out = append(out, q.Get("tailLines"))
	}
	return out
}

// A backward read that needs two windows reads the second further back, and sends
// the lines newest first; the window that reaches the first line is followed by a
// read of the first moment.
func TestABackwardReadNeedsTwoWindows(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(15)...)

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := drain(r)
	require.NoError(t, r.err)
	assert.Equal(t, []string{"14", "13", "12", "11", "10", "9", "8", "7", "6", "5", "4", "3", "2", "1", "0"}, messages(recs))
	assert.False(t, r.short)
	assert.Equal(t, []string{"10", "20", ""}, tails(s))
	assert.Equal(t, "100", s.requests()[0].Get("limitBytes"))
}

// A backward read under until over a range far longer than its first window
// reaches the log's first line in its last window, and is not short: the first
// window's reach is rounded up, so the doublings land on the first line.
func TestABackwardReadUnderUntilReachesTheFirstLine(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "first"), stamped(5*time.Hour, "last"))
	o := backwardOpts()
	o.until = to(5 * time.Hour)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"last", "first"}, messages(drain(r)))
	require.NoError(t, r.err)
	assert.False(t, r.short)
}

// A backward read finds the first line from since's second, keeps since's line,
// and under until reads no window from before since.
func TestABackwardReadKeepsItsBounds(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(30)...)
	o := backwardOpts()
	o.since, o.until = from(5*time.Second), to(22*time.Second)

	r := readBackward(t.Context(), s.get, web, o)

	recs := drain(r)
	assert.Equal(t, "22", recs[0].Message)
	assert.Equal(t, "5", recs[len(recs)-1].Message)
	assert.Len(t, recs, 18)
	assert.Equal(t, "2026-10-08T10:00:05Z", s.requests()[0].Get("sinceTime"))
	for _, q := range s.requests() {
		since, err := time.Parse(time.RFC3339, q.Get("sinceTime"))
		require.NoError(t, err)
		assert.False(t, since.Before(o.since.at.Timestamp))
	}
}

// An empty log reads nothing, and so does one emptied under the read.
func TestABackwardReadOfAnEmptyLog(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		r := readBackward(t.Context(), claimOn(t, newLogServer()).get, web, backwardOpts())
		assert.Empty(t, drain(r))
		assert.NoError(t, r.err)
	})
	t.Run("emptied", func(t *testing.T) {
		s := newLogServer()
		s.add("prod/web/app", lineEvery(3)...)
		s.onRequest = func(n int) {
			if n == 1 {
				s.clear("prod/web/app")
			}
		}
		r := readBackward(t.Context(), s.get, web, backwardOpts())
		assert.Empty(t, drain(r))
		assert.NoError(t, r.err)
		assert.False(t, r.short)
	})
}

// A log that grows by a whole batch under the read is read in a larger window,
// and the lines that arrived are left for the next page.
func TestABackwardReadOutgrowsAGrowingLog(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(25)...)
	s.onRequest = func(n int) {
		if n == 2 {
			s.add("prod/web/app", lineEvery(35)[25:]...)
		}
	}

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := drain(r)
	require.NoError(t, r.err)
	assert.Len(t, recs, 25)
	assert.Equal(t, "24", recs[0].Message)
	assert.Equal(t, "0", recs[24].Message)
	assert.Equal(t, []string{"10", "20", "40", ""}, tails(s))
}

// A backward read that spends its budget stops at the oldest line it read whole
// past the moment its last window started in, whether or not the grep kept it; a
// window the budget cut short is left unsent, and a read that sent nothing has no
// stop. Each line is 24 bytes, the first read 100, and a window of ten lines 240.
func TestABackwardReadStopsAtTheBudget(t *testing.T) {
	for name, tc := range map[string]struct {
		budget   int64
		until    bound
		grep     *regexp.Regexp
		lines    []string
		stop     position
		requests int
	}{
		"after a whole window":    {budget: 400, lines: []string{"99", "98", "97", "96", "95", "94", "93", "92", "91", "90", "89", "88", "87", "86", "85", "84", "83", "82", "81"}, stop: lineAt(web, 81*time.Second, 0), requests: 3},
		"with a grep that misses": {budget: 400, grep: regexp.MustCompile(`never`), lines: []string{}, stop: lineAt(web, 81*time.Second, 0), requests: 3},
		"inside the first window": {budget: 200, lines: []string{}, requests: 2},
		"under until":             {budget: 200, until: to(50 * time.Second), lines: []string{"50", "49"}, stop: lineAt(web, 49*time.Second, 0), requests: 2},
		"inside the first span":   {budget: 110, until: to(99 * time.Second), lines: []string{}, requests: 2},
		"finding the first line":  {budget: 50, lines: []string{}, requests: 1},
	} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			s.add("prod/web/app", lineEvery(100)...)
			o := backwardOpts()
			o.budget, o.until, o.grep = newBudget(tc.budget), tc.until, tc.grep

			r := readBackward(t.Context(), s.get, web, o)

			recs := drain(r)
			assert.NoError(t, r.err)
			assert.Equal(t, tc.lines, messages(recs))
			assert.True(t, r.short)
			assert.Equal(t, tc.stop, r.stop)
			assert.Len(t, s.requests(), tc.requests)
		})
	}
}

// A read past its window cap stops at the oldest line it sent.
func TestABackwardReadStopsAtItsWindowCap(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(100)...)
	o := backwardOpts()
	o.windows = 4

	r := readBackward(t.Context(), s.get, web, o)

	recs := drain(r)
	assert.Len(t, recs, 19)
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web, 81*time.Second, 0), r.stop)
	assert.Len(t, s.requests(), 3)
}

// A backward read under until reads windows of time ending there, each from its
// start, so it reaches until however much log follows it; with three windows the
// first reaches halfway back to the first line.
func TestABackwardReadUnderUntilReachesIt(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", lineEvery(1000)...)
	o := backwardOpts()
	o.until, o.windows = to(100*time.Second), 3

	r := readBackward(t.Context(), s.get, web, o)

	recs := drain(r)
	require.NoError(t, r.err)
	assert.Len(t, recs, 101)
	assert.Equal(t, "100", recs[0].Message)
	assert.Equal(t, "0", recs[100].Message)
	assert.False(t, r.short)
	var sinces []string
	for _, q := range s.requests()[1:] {
		sinces = append(sinces, q.Get("sinceTime"))
	}
	assert.Equal(t, []string{"2026-10-08T10:00:50Z", "2026-10-08T10:00:00Z"}, sinces)
}

// A backward read under until keeps since to the nanosecond, though the log
// endpoint finds its first line from since's second.
func TestABackwardReadUnderUntilKeepsSince(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, "before"), stamped(time.Second+300*time.Millisecond, "after"))
	o := backwardOpts()
	o.since, o.until = from(time.Second+200*time.Millisecond), to(time.Minute)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"after"}, messages(drain(r)))
	assert.NoError(t, r.err)
}

// Lines sharing a window's starting moment are held back for the next window,
// which reads the whole moment, so none is lost at the seam.
func TestABackwardWindowKeepsAMomentWhole(t *testing.T) {
	s := newLogServer()
	lines := lineEvery(20)
	lines[9] = stamped(10*time.Second, "9'")
	s.add("prod/web/app", lines...)

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := drain(r)
	require.NoError(t, r.err)
	assert.Len(t, recs, 20)
	assert.Equal(t, []string{"11", "10", "9'", "8"}, messages(recs[8:12]))
}

// A first moment longer than a window is read whole: a window starting in it
// cannot tell whether it began at its top.
func TestABackwardReadKeepsTheFirstMomentWhole(t *testing.T) {
	s := newLogServer()
	for i := range 25 {
		s.add("prod/web/app", stamped(0, strconv.Itoa(i)))
	}
	s.add("prod/web/app", stamped(time.Second, "last"))

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := drain(r)
	require.NoError(t, r.err)
	assert.Len(t, recs, 26)
	assert.Equal(t, "last", recs[0].Message)
	assert.False(t, r.short)
}

// A backward read after a line of a moment longer than its window reads that
// moment whole, since the window numbers its lines from its own top: with 100
// lines at one moment, after the 91st, it sends the nine after it.
func TestABackwardReadAfterALineReadsItsMomentWhole(t *testing.T) {
	for name, tail := range map[string][]string{
		"alone":             nil,
		"before later ones": {stamped(time.Second, "later")},
	} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			for i := range 100 {
				s.add("prod/web/app", stamped(0, strconv.Itoa(i)))
			}
			s.add("prod/web/app", tail...)
			o := backwardOpts()
			o.since = bound{lineAt(web, 0, 90), false}

			r := readBackward(t.Context(), s.get, web, o)

			recs := drain(r)
			require.NoError(t, r.err)
			want := []string{"99", "98", "97", "96", "95", "94", "93", "92", "91"}
			if tail != nil {
				want = append([]string{"later"}, want...)
			}
			assert.Equal(t, want, messages(recs))
			assert.Equal(t, 91, recs[len(recs)-1].Ordinal)
			assert.False(t, r.short)
		})
	}
}

// A backward read with a sub-second since reads the first moment from since,
// though the log endpoint finds that moment from since's second.
func TestABackwardReadKeepsSinceInTheFirstMoment(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(100*time.Millisecond, "before"), stamped(500*time.Millisecond, "after"))
	o := backwardOpts()
	o.since = from(400 * time.Millisecond)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"after"}, messages(drain(r)))
	assert.NoError(t, r.err)
}

// A backward read the API server refuses ends with its status, on its first read
// or on a later window.
func TestARefusedBackwardReadEndsWithItsStatus(t *testing.T) {
	for name, on := range map[string]int{"finding the first line": 0, "a window": 1} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			s.add("prod/web/app", lineEvery(3)...)
			s.onRequest = func(n int) {
				if n == on {
					s.refuse["prod/web/app"] = refusal{404, "not found"}
				}
			}

			r := readBackward(t.Context(), s.get, web, backwardOpts())

			assert.Empty(t, drain(r))
			assert.EqualError(t, r.err, "not found")
		})
	}
}

// A body that fails under the read is the read's error.
func TestABodyThatFailsEndsTheRead(t *testing.T) {
	get := func(context.Context, string, url.Values) (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(strings.NewReader(stamped(0, "a")+"\n"), iotest.ErrReader(errors.New("reset")))), nil
	}

	r := readForward(t.Context(), get, web, readOpts{})

	assert.Equal(t, []string{"a"}, messages(drain(r)))
	assert.EqualError(t, r.err, "reset")
}
