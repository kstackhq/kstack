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
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// web is the container app of pod web in prod, on node-a.
var web = stream{key: StreamKey{Namespace: "prod", Pod: "web", Container: "app"}, place: Place{Node: "node-a"}, gen: generation{instance: containerID("uid-web", "app", 0)}}

// A forward read since a moment asks for its second and keeps that moment's line.
func TestAForwardReadSinceAMoment(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(500*time.Millisecond, "zero"), stamped(1200*time.Millisecond, "one"), stamped(2*time.Second, "two"))

	r := readForward(t.Context(), s.get, web, readOpts{since: from(1200 * time.Millisecond)})

	recs := drain(r)
	require.NoError(t, r.err)
	assert.False(t, r.short)
	assert.Equal(t, []string{"one", "two"}, messages(recs))
	assert.Equal(t, web.key, recs[0].Stream)
	assert.Equal(t, web.place, recs[0].Place)
	assert.Equal(t, web.gen.instance, recs[0].ContainerID)
	assert.Equal(t, at(1200*time.Millisecond), recs[0].Timestamp)
	q := s.requests()[0]
	assert.Equal(t, "2026-10-08T10:00:01Z", q.Get("sinceTime"))
	assert.Equal(t, "true", q.Get("timestamps"))
	assert.Equal(t, "app", q.Get("container"))
}

// A forward read with no lower bound starts at the backend's oldest line, so it
// says head however far it read; one from a moment does not.
func TestAForwardReadWithNoLowerBoundSaysHead(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(5)...)

	r := readForward(t.Context(), s.get, web, readOpts{until: to(2 * time.Second)})
	drain(r)
	assert.True(t, r.head)

	r = readForward(t.Context(), s.get, web, readOpts{since: from(time.Second)})
	drain(r)
	assert.False(t, r.head)
}

// A forward read that reaches a finished instance's last line says so; one of a
// running instance, or one stopped at until, does not.
func TestAForwardReadOfAFinishedInstanceSaysAtEnd(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(5)...)
	done := web
	done.gen.state = "terminated"
	read := func(st stream, o readOpts) *streamRead {
		r := readForward(t.Context(), s.get, st, o)
		drain(r)
		return r
	}

	assert.True(t, read(done, readOpts{since: from(time.Second)}).atEnd, "finished")
	assert.False(t, read(web, readOpts{}).atEnd, "running")
	assert.False(t, read(done, readOpts{until: to(2 * time.Second)}).atEnd, "until")
	previous := web
	previous.key.Previous = true
	assert.True(t, read(previous, readOpts{}).atEnd, "previous")
}

// After a moment inside a second, the read asks for that second and drops the
// lines at and before the moment.
func TestAfterAMomentDropsItsLines(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, "a"), stamped(1200*time.Millisecond, "cursor"), stamped(1200*time.Millisecond, "cursor too"), stamped(1500*time.Millisecond, "b"))

	r := readForward(t.Context(), s.get, web, readOpts{since: after(1200 * time.Millisecond)})

	assert.Equal(t, []string{"b"}, messages(drain(r)))
	assert.Equal(t, "2026-10-08T10:00:01Z", s.requests()[0].Get("sinceTime"))
}

// Lines of one moment are numbered in order, and a bound at one of them keeps or
// drops that line alone.
func TestLinesOfOneMomentAreNumbered(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, "a"), stamped(time.Second, "b"), stamped(time.Second, "c"), stamped(2*time.Second, "d"))
	get := s.get

	recs := drain(readForward(t.Context(), get, web, readOpts{}))
	ordinals := []int{}
	for _, rec := range recs {
		ordinals = append(ordinals, rec.Ordinal)
	}
	assert.Equal(t, []int{0, 1, 2, 0}, ordinals)

	after := readForward(t.Context(), get, web, readOpts{since: bound{lineAt(web.key, time.Second, 1), false}})
	assert.Equal(t, []string{"c", "d"}, messages(drain(after)))
	until := readForward(t.Context(), get, web, readOpts{until: bound{lineAt(web.key, time.Second, 1), true}})
	assert.Equal(t, []string{"a", "b"}, messages(drain(until)))
}

// A read stops at its until, which keeps its own line.
func TestAForwardReadStopsAtUntil(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "b"), stamped(2*time.Second, "c"))

	r := readForward(t.Context(), s.get, web, readOpts{until: to(time.Second)})

	assert.Equal(t, []string{"a", "b"}, messages(drain(r)))
}

// The previous instance is asked for, and its lines say so.
func TestThePreviousInstanceIsSent(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app/previous", stamped(0, "before the restart"))
	src := web
	src.key.Previous = true

	r := readForward(t.Context(), s.get, src, readOpts{})

	recs := drain(r)
	assert.Equal(t, []string{"before the restart"}, messages(recs))
	assert.True(t, recs[0].Stream.Previous)
	assert.Equal(t, "true", s.requests()[0].Get("previous"))
}

// A follow since a moment reads the lines from it, then one written after it opened.
func TestAFollowDeliversALineWrittenLater(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "old"))

	r := readForward(t.Context(), claimOn(t, s).get, web, readOpts{since: from(0), follow: true})

	assert.Equal(t, "old", testutil.Recv(t, r.lines.Chan(), "the old line").Message)
	s.add("prod/web/app", stamped(time.Second, "new"))
	assert.Equal(t, "new", testutil.Recv(t, r.lines.Chan(), "the new line").Message)
	assert.Equal(t, "true", s.requests()[0].Get("follow"))
}

// A follow numbers a moment's lines from the top of its second, as a read opened
// again from a cursor in it does, so a line keeps its ordinal across the two.
func TestAFollowNumbersAMomentFromItsSecond(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(100*time.Millisecond, "a"), stamped(100*time.Millisecond, "b"))

	r := readForward(t.Context(), claimOn(t, s).get, web, readOpts{follow: true, since: from(100 * time.Millisecond)})
	first := testutil.Recv(t, r.lines.Chan(), "the first line")
	assert.Equal(t, 0, first.Ordinal)

	again := readForward(t.Context(), claimOn(t, s).get, web, readOpts{follow: true, since: bound{positionOf(first.Record), false}})
	assert.Equal(t, "b", testutil.Recv(t, again.lines.Chan(), "the line after it").Message)
	assert.Empty(t, s.requests()[0].Get("tailLines"))
}

// A read the connection's retirement cut stops short at the last line it read
// whole, and says why.
func TestARetiredReadStopsShort(t *testing.T) {
	t.Parallel()
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
	assert.Equal(t, lineAt(web.key, 0, 0), r.stop)
	assert.ErrorIs(t, r.err, errRetired)
}

// A read that fails past its first line in range stops short at the last line it
// read whole, keeping its failure; one that fails before any line is not short.
func TestAReadThatFailsPartwayStopsShort(t *testing.T) {
	t.Parallel()
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
			assert.Equal(t, lineAt(web.key, 0, 0), r.stop)
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

// A message is cleaned before it is sent, and every line is sent: the grep runs
// where the lines are read, never in the backend.
func TestALineIsCleaned(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "\x1b[31merror\x1b[0m: disk"), stamped(time.Second, "ok"))

	r := readForward(t.Context(), s.get, web, readOpts{})

	assert.Equal(t, []string{"error: disk", "ok"}, messages(drain(r)))
}

// A line with no timestamp, or one that is not a time, is skipped, as kubetail's
// reader skips it.
func TestALineWithNoTimestampIsSkipped(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", "garbage", "not a time x", stamped(0, "a"))

	r := readForward(t.Context(), s.get, web, readOpts{})

	assert.Equal(t, []string{"a"}, messages(drain(r)))
}

// A line past 64 KiB is read to there and the rest skipped, so the cleaned message
// is cut at 16 KiB, and the line after it is read whole.
func TestALongLineIsCut(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s := newLogServer()
	s.refuse["prod/web/app"] = refusal{400, "container is waiting to start"}

	r := readForward(t.Context(), s.get, web, readOpts{})

	assert.Empty(t, drain(r))
	assert.EqualError(t, r.err, "container is waiting to start")
}

// A read whose context ends while a line waits to be taken ends clean.
func TestACancelledReadEndsClean(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(3)...)
	get := s.get
	for name, read := range map[string]func(context.Context) *streamRead{
		"forward":  func(ctx context.Context) *streamRead { return readForward(ctx, get, web, readOpts{}) },
		"backward": func(ctx context.Context) *streamRead { return readBackward(ctx, get, web, backwardOpts()) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			r := read(ctx)
			testutil.Recv(t, r.lines.Chan(), "the first line")

			cancel()

			testutil.WaitClosed(t, r.lines.Chan(), "the read to end")
			assert.NoError(t, r.err)
			assert.False(t, r.short)
		})
	}
}

// A read whose context ends before its GET answers ends clean.
func TestAReadCancelledBeforeItsGetEndsClean(t *testing.T) {
	t.Parallel()
	get := func(ctx context.Context, _ string, _ url.Values) (io.ReadCloser, error) {
		return nil, ctx.Err()
	}
	for name, read := range map[string]func(context.Context) *streamRead{
		"forward":  func(ctx context.Context) *streamRead { return readForward(ctx, get, web, readOpts{}) },
		"backward": func(ctx context.Context) *streamRead { return readBackward(ctx, get, web, backwardOpts()) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			r := read(ctx)

			assert.Empty(t, drain(r))
			assert.NoError(t, r.err)
			assert.False(t, r.short)
		})
	}
}

// A line each does not take is not counted as read.
func TestALineNotTakenIsNotRead(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "b"))
	rd := &reader{ctx: t.Context(), get: s.get, key: web.key, instance: web.gen.instance}

	read, err := rd.lines(rd.query(time.Time{}), readOpts{}, func(rec Record) bool { return rec.Message == "a" })

	require.NoError(t, err)
	assert.Equal(t, lineAt(web.key, 0, 0), read.last)
}

// A forward read that spends its budget stops at the last line it read whole.
func TestAForwardReadStopsAtTheBudget(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(1000)...)

	r := readForward(t.Context(), s.get, web, readOpts{budget: newBudget(4 << 10)})

	recs := drain(r)
	assert.NotEmpty(t, recs)
	assert.Equal(t, positionOf(recs[len(recs)-1]), r.stop)
	assert.NoError(t, r.err)
	assert.True(t, r.short)
	assert.Equal(t, "app", r.stop.Container)
	assert.True(t, r.stop.Timestamp.After(at(0)))
	assert.True(t, r.stop.Timestamp.Before(at(999*time.Second)))
}

// A forward read whose budget is spent before its first whole line has no stop: it
// read nothing in range.
func TestAForwardReadSpentBeforeItsFirstLineHasNoStop(t *testing.T) {
	t.Parallel()
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

// A body that fails under the read is the read's error.
func TestABodyThatFailsEndsTheRead(t *testing.T) {
	t.Parallel()
	get := func(context.Context, string, url.Values) (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(strings.NewReader(stamped(0, "a")+"\n"), iotest.ErrReader(errors.New("reset")))), nil
	}

	r := readForward(t.Context(), get, web, readOpts{})

	assert.Equal(t, []string{"a"}, messages(drain(r)))
	assert.EqualError(t, r.err, "reset")
}

// backwardOpts is a backward read in windows of 10 lines, with budget to spare.
func backwardOpts() readOpts {
	return readOpts{batch: 10, windows: 16, budget: newBudget(32 << 20)}
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

// A backward read that needs two windows reads the second further back; the window
// that reaches the first line is followed by a read of the first moment.
func TestABackwardReadNeedsTwoWindows(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(15)...)

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := newestFirst(drain(r))
	require.NoError(t, r.err)
	assert.Equal(t, []string{"14", "13", "12", "11", "10", "9", "8", "7", "6", "5", "4", "3", "2", "1", "0"}, messages(recs))
	assert.False(t, r.short)
	assert.Equal(t, []string{"10", "20", ""}, tails(s))
	assert.Equal(t, "100", s.requests()[0].Get("limitBytes"))
}

// A backward read under until over a range far longer than its first window
// reaches the log's first line in its last window, and is not short: the first

// A backward read hands over each window oldest first and its end after its last
// line, so its reader can write the window as it arrives. The read of the first moment
// is a window of its own, older than the window that found it.
func TestABackwardReadEndsEachWindow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(15)...)

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	assert.Equal(t, []string{"6", "7", "8", "9", "10", "11", "12", "13", "14", "|", "1", "2", "3", "4", "5", "|", "0", "|"}, windowsOf(r))
	require.NoError(t, r.err)
}

// A backward read under until ends each of its windows of time too.
func TestABackwardReadUnderUntilEndsEachWindow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(10)...)
	o := backwardOpts()
	o.until, o.windows = to(5*time.Second), 3

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"3", "4", "5", "|", "0", "1", "2", "|"}, windowsOf(r))
	require.NoError(t, r.err)
}

// A backward window's lines go out as its GET reads them, not once it has read them
// all: a window's first line arrives while the rest of its body is still to come.
func TestABackwardWindowStreamsItsLines(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		lines int
		until bound
		first string
		rest  []string
	}{
		"windows of time": {lines: 10, until: to(10 * time.Second), first: "5", rest: []string{"6", "7", "8", "9", "|", "0", "1", "2", "3", "4", "|"}},
		"tailLines":       {lines: 15, first: "6", rest: []string{"7", "8", "9", "10", "11", "12", "13", "14", "|", "1", "2", "3", "4", "5", "|", "0", "|"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newLogServer()
			s.add("prod/web/app", lineEvery(tc.lines)...)
			gate := make(chan struct{})
			o := backwardOpts()
			o.until = tc.until
			if !tc.until.at.isZero() {
				o.windows = 3
			}

			r := readBackward(t.Context(), (&pauser{n: 2, gate: gate}).on(s.get), web, o)

			assert.Equal(t, tc.first, testutil.Recv(t, r.lines.Chan(), "the window's first line").Message)
			close(gate)
			assert.Equal(t, tc.rest, windowsOf(r))
			assert.NoError(t, r.err)
		})
	}
}

// The fake kubelet serves a log as the README's kubelet points say the real one does,
// since every read here is tested against it.
func TestTheFakeKubeletServesAsTheKubelet(t *testing.T) {
	t.Parallel()
	read := func(s *logServer, q url.Values) []string {
		body, err := s.get(t.Context(), logPath(web.key), q)
		require.NoError(t, err)
		defer body.Close()
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		var out []string
		for line := range strings.Lines(string(data)) {
			out = append(out, strings.SplitN(strings.TrimSpace(line), " ", 2)[1])
		}
		return out
	}
	query := func(kv ...string) url.Values {
		q := url.Values{"container": {"app"}, "timestamps": {"true"}}
		for i := 0; i < len(kv); i += 2 {
			q.Set(kv[i], kv[i+1])
		}
		return q
	}

	t.Run("only the current file", func(t *testing.T) {
		s := newLogServer()
		s.add("prod/web/app", lineEvery(5)...)
		s.rotate("prod/web/app", 2*time.Second)
		assert.Equal(t, []string{"2", "3", "4"}, read(s, query()))
	})
	t.Run("sinceTime filters from the first line", func(t *testing.T) {
		s := newLogServer()
		s.add("prod/web/app", lineEvery(5)...)
		assert.Equal(t, []string{"3", "4"}, read(s, query("sinceTime", "2026-10-08T10:00:03Z")))
	})
	t.Run("sinceTime is whole seconds", func(t *testing.T) {
		s := newLogServer()
		s.add("prod/web/app", stamped(time.Second+250*time.Millisecond, "a"), stamped(time.Second+750*time.Millisecond, "b"))
		assert.Equal(t, []string{"a", "b"}, read(s, query("sinceTime", "2026-10-08T10:00:01.5Z")))
	})
	t.Run("tailLines counts a long line's parts", func(t *testing.T) {
		s := newLogServer()
		s.part = 4
		s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "longer"), stamped(2*time.Second, "c"))
		assert.Equal(t, []string{"longer", "c"}, read(s, query("tailLines", "3")))
		assert.Equal(t, []string{"c"}, read(s, query("tailLines", "2")))
	})
}

// A backward read of a log whose long lines tailLines counts in parts reads every
// line: a window holds fewer lines than it asked for, and the next reaches further.
func TestABackwardReadOfSplitLinesReadsThemAll(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.part = 4
	var want []string
	for i := range 40 {
		msg := strconv.Itoa(i) + strings.Repeat("x", i%3*4)
		s.add("prod/web/app", stamped(time.Duration(i)*time.Second, msg))
		want = append([]string{msg}, want...)
	}

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	assert.Equal(t, want, messages(newestFirst(drain(r))))
	require.NoError(t, r.err)
	assert.False(t, r.short)
}

// A log that grows under a read of windows of time returns the lines it would have
// with no growth: each window ends where the last began, never at the log's end.
func TestABackwardReadUnderUntilIgnoresGrowth(t *testing.T) {
	t.Parallel()
	read := func(grow bool) []string {
		s := newLogServer()
		s.add("prod/web/app", lineEvery(100)...)
		if grow {
			s.onRequest = func(n int) {
				s.add("prod/web/app", stamped(time.Duration(100+n)*time.Second, "new"))
			}
		}
		o := backwardOpts()
		o.until = to(50 * time.Second)
		r := readBackward(t.Context(), s.get, web, o)
		recs := drain(r)
		require.NoError(t, r.err)
		return messages(recs)
	}

	assert.Equal(t, read(false), read(true))
	assert.Len(t, read(true), 51)
}

// windowsOf is each line's message r hands over until it closes, and "|" for the end
// of each window.
func windowsOf(r *streamRead) []string {
	var out []string
	for it := range r.lines.Chan() {
		if it.windowEnd {
			out = append(out, "|")
		} else {
			out = append(out, it.Message)
		}
	}
	return out
}

// window's reach is rounded up, so the doublings land on the first line.
func TestABackwardReadUnderUntilReachesTheFirstLine(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "first"), stamped(5*time.Hour, "last"))
	o := backwardOpts()
	o.until = to(5 * time.Hour)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"last", "first"}, messages(newestFirst(drain(r))))
	require.NoError(t, r.err)
	assert.False(t, r.short)
}

// A backward read finds the first line from since's second, keeps since's line,
// and under until reads no window from before since.
func TestABackwardReadKeepsItsBounds(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(30)...)
	o := backwardOpts()
	o.since, o.until = from(5*time.Second), to(22*time.Second)

	r := readBackward(t.Context(), s.get, web, o)

	recs := newestFirst(drain(r))
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

// A backward read that reaches the log's first line says the backend holds nothing
// older, under until too; one that stops at since or runs short does not.
func TestABackwardReadThatReachesTheFirstLineSaysHead(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(30)...)
	read := func(o readOpts) *streamRead {
		r := readBackward(t.Context(), s.get, web, o)
		drain(r)
		return r
	}

	assert.True(t, read(backwardOpts()).head, "to the first line")
	o := backwardOpts()
	o.until = to(20 * time.Second)
	assert.True(t, read(o).head, "under until")
	o = backwardOpts()
	o.since = from(5 * time.Second)
	assert.False(t, read(o).head, "stopped at since")
	o = backwardOpts()
	o.windows = 3
	r := read(o)
	assert.True(t, r.short)
	assert.False(t, r.head, "short")
}

// A backward read past since that reaches the log's first line, or finds the log
// empty or its first line past until, says head: the lines since names were rotated
// away. One whose first line is since's is not.
func TestABackwardReadThatReachesAFirstLinePastSinceSaysHead(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(30)[10:]...)
	read := func(o readOpts) *streamRead {
		r := readBackward(t.Context(), s.get, web, o)
		drain(r)
		return r
	}

	o := backwardOpts()
	o.since = from(5 * time.Second)
	assert.True(t, read(o).head, "past since")
	o.until = to(20 * time.Second)
	assert.True(t, read(o).head, "past since, under until")
	o.until = to(8 * time.Second)
	assert.True(t, read(o).head, "past since and until")
	o = backwardOpts()
	o.since = from(10 * time.Second)
	assert.False(t, read(o).head, "the first line is since's")

	empty := backwardOpts()
	empty.since = from(5 * time.Second)
	r := readBackward(t.Context(), claimOn(t, newLogServer()).get, web, empty)
	drain(r)
	assert.True(t, r.head, "empty from since")
}

// An empty log reads nothing, and so does one emptied under the read.
func TestABackwardReadOfAnEmptyLog(t *testing.T) {
	t.Parallel()
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

// A log emptied after a window was sent ends the read short at the oldest line
// sent, since the lines before it were never read.
func TestABackwardReadOfALogEmptiedAfterAWindowStopsShort(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(15)...)
	s.onRequest = func(n int) {
		if n == 2 {
			s.clear("prod/web/app")
		}
	}

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	assert.Equal(t, []string{"14", "13", "12", "11", "10", "9", "8", "7", "6"}, messages(newestFirst(drain(r))))
	assert.NoError(t, r.err)
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web.key, 6*time.Second, 0), r.stop)
}

// A window that begins below a sub-second since holds every line in range, so
// the read ends there rather than growing toward the second's first line.
func TestABackwardReadEndsAtTheWindowThatPassesSince(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i := range 30 {
		s.add("prod/web/app", stamped(5*time.Second+time.Duration(i)*10*time.Millisecond, strconv.Itoa(i)))
	}
	o := backwardOpts()
	o.since = from(5*time.Second + 150*time.Millisecond)

	r := readBackward(t.Context(), s.get, web, o)

	recs := newestFirst(drain(r))
	require.NoError(t, r.err)
	assert.Len(t, recs, 15)
	assert.Equal(t, "29", recs[0].Message)
	assert.Equal(t, "15", recs[14].Message)
	assert.False(t, r.short)
	assert.Equal(t, []string{"10", "20"}, tails(s))
}

// The read that finds the first line takes no more than it asked for, though a
// server that ignores limitBytes sends the whole log.
func TestFindingTheFirstLineReadsItsLimit(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(100)...)
	var read int
	get := func(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
		first := q.Has("limitBytes")
		q.Del("limitBytes")
		body, err := s.get(ctx, path, q)
		if err != nil || !first {
			return body, err
		}
		data, err := io.ReadAll(body)
		body.Close()
		if err != nil {
			return nil, err
		}
		return io.NopCloser(iotest.OneByteReader(&countingReader{strings.NewReader(string(data)), &read})), nil
	}

	r := readBackward(t.Context(), get, web, backwardOpts())

	drain(r)
	require.NoError(t, r.err)
	assert.LessOrEqual(t, read, firstBytes)
}

// countingReader adds every byte it reads to n.
type countingReader struct {
	io.Reader
	n *int
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.Reader.Read(p)
	*c.n += k
	return k, err
}

// A log that grows by a whole batch under the read is read in a larger window,
// and the lines that arrived are left for the next page.
func TestABackwardReadOutgrowsAGrowingLog(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(25)...)
	s.onRequest = func(n int) {
		if n == 2 {
			s.add("prod/web/app", lineEvery(35)[25:]...)
		}
	}

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := newestFirst(drain(r))
	require.NoError(t, r.err)
	assert.Len(t, recs, 25)
	assert.Equal(t, "24", recs[0].Message)
	assert.Equal(t, "0", recs[24].Message)
	assert.Equal(t, []string{"10", "20", "40", ""}, tails(s))
}

// A backward read that spends its budget stops at the oldest line of the last window
// it read whole; a window the budget cut short goes out without its end, and a read
// that read no window whole has no stop. Each line is 24 bytes, the first read 100,
// and a window of ten lines 240.
func TestABackwardReadStopsAtTheBudget(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		budget   int64
		until    bound
		lines    []string
		stop     position
		requests int
	}{
		"after a whole window":    {budget: 400, lines: []string{"99", "98", "97", "96", "95", "94", "93", "92", "91", "90", "89", "88", "87", "86", "85", "84", "83", "82", "81"}, stop: lineAt(web.key, 81*time.Second, 0), requests: 3},
		"inside the first window": {budget: 200, lines: []string{"99", "98", "97", "96", "95", "94", "93", "92", "91"}, requests: 2},
		"under until":             {budget: 200, until: to(50 * time.Second), lines: []string{"50", "49"}, stop: lineAt(web.key, 49*time.Second, 0), requests: 2},
		"inside the first span":   {budget: 110, until: to(99 * time.Second), lines: []string{"99", "98"}, requests: 2},
		"finding the first line":  {budget: 50, lines: []string{}, requests: 1},
	} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			s.add("prod/web/app", lineEvery(100)...)
			o := backwardOpts()
			o.budget, o.until = newBudget(tc.budget), tc.until

			r := readBackward(t.Context(), s.get, web, o)

			recs := newestFirst(drain(r))
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
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(100)...)
	o := backwardOpts()
	o.windows = 4

	r := readBackward(t.Context(), s.get, web, o)

	recs := newestFirst(drain(r))
	assert.Len(t, recs, 19)
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web.key, 81*time.Second, 0), r.stop)
	assert.Len(t, s.requests(), 3)
}

// A backward read under until reads windows of time ending there, each from its
// start, so it reaches until however much log follows it; with three windows the
// first reaches halfway back to the first line.
func TestABackwardReadUnderUntilReachesIt(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(1000)...)
	o := backwardOpts()
	o.until, o.windows = to(100*time.Second), 3

	r := readBackward(t.Context(), s.get, web, o)

	recs := newestFirst(drain(r))
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
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, "before"), stamped(time.Second+300*time.Millisecond, "after"))
	o := backwardOpts()
	o.since, o.until = from(time.Second+200*time.Millisecond), to(time.Minute)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"after"}, messages(newestFirst(drain(r))))
	assert.NoError(t, r.err)
}

// Lines sharing a window's starting moment are held back for the next window,
// which reads the whole moment, so none is lost at the seam.
func TestABackwardWindowKeepsAMomentWhole(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	lines := lineEvery(20)
	lines[9] = stamped(10*time.Second, "9'")
	s.add("prod/web/app", lines...)

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := newestFirst(drain(r))
	require.NoError(t, r.err)
	assert.Len(t, recs, 20)
	assert.Equal(t, []string{"11", "10", "9'", "8"}, messages(recs[8:12]))
}

// A first moment longer than a window is read whole: a window starting in it
// cannot tell whether it began at its top.
func TestABackwardReadKeepsTheFirstMomentWhole(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i := range 25 {
		s.add("prod/web/app", stamped(0, strconv.Itoa(i)))
	}
	s.add("prod/web/app", stamped(time.Second, "last"))

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	recs := newestFirst(drain(r))
	require.NoError(t, r.err)
	assert.Len(t, recs, 26)
	assert.Equal(t, "last", recs[0].Message)
	assert.False(t, r.short)
}

// A backward read after a line of a moment longer than its window reads that
// moment whole, since the window numbers its lines from its own top: with 100
// lines at one moment, after the 91st, it sends the nine after it.
func TestABackwardReadAfterALineReadsItsMomentWhole(t *testing.T) {
	t.Parallel()
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
			o.since = bound{lineAt(web.key, 0, 90), false}

			r := readBackward(t.Context(), s.get, web, o)

			recs := newestFirst(drain(r))
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
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(100*time.Millisecond, "before"), stamped(500*time.Millisecond, "after"))
	o := backwardOpts()
	o.since = from(400 * time.Millisecond)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"after"}, messages(newestFirst(drain(r))))
	assert.NoError(t, r.err)
}

// A backward read the API server refuses ends with its status, on its first read
// or on a later window.
func TestARefusedBackwardReadEndsWithItsStatus(t *testing.T) {
	t.Parallel()
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
