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
		"windows of time": func(ctx context.Context) *streamRead {
			o := backwardOpts()
			o.until = to(2 * time.Second)
			return readBackward(ctx, get, web, o)
		},
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
		"windows of time": func(ctx context.Context) *streamRead {
			o := backwardOpts()
			o.until = to(2 * time.Second)
			return readBackward(ctx, get, web, o)
		},
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

	read, err := rd.lines(rd.query(time.Time{}), readOpts{}, func(rec Record, _ int) bool { return rec.Message == "a" })

	require.NoError(t, err)
	assert.Equal(t, lineAt(web.key, 0, 0), read.last)
}

// A forward read is cut once it has spent its allowance and the pool, at the last
// line it read.
func TestAForwardReadIsCutAtItsLimit(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(1000)...)
	c := smallCaps
	c.query = 8 << 10

	r := readForward(t.Context(), s.get, web, readOpts{limits: limitsOf(c)})

	recs := drain(r)
	assert.NoError(t, r.err)
	assert.True(t, r.short)
	assert.Equal(t, positionOf(recs[len(recs)-1]), r.stop)
	// 8 KiB is about 330 lines, as the body arrives a buffer at a time.
	assert.Less(t, len(recs), 400)
	assert.Greater(t, len(recs), 300)
}

// A forward read is never cut before its first line in range: the lines of its
// second before the bound are read and charged, and the first line past them goes
// out before the cut.
func TestAForwardReadIsNeverCutBeforeItsFirstLine(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", stamped(time.Second, strings.Repeat("x", 900)), stamped(1500*time.Millisecond, "in range"), stamped(2*time.Second, "past"))
	c := smallCaps
	c.query = 512

	r := readForward(t.Context(), s.get, web, readOpts{since: bound{lineAt(web.key, time.Second, 0), false}, limits: limitsOf(c)})

	assert.Equal(t, []string{"in range"}, messages(drain(r)))
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web.key, 1500*time.Millisecond, 0), r.stop)
}

// A follow's fill reads every second whole: no second stops it, and it is cut at
// the end of the second it spent its share in.
func TestAFollowsFillIsCutAtTheEndOfASecond(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i := range 50 {
		s.add("prod/web/app", stamped(time.Duration(i)*100*time.Millisecond, strings.Repeat("x", 200)))
	}
	c := smallCaps
	c.query = 1 << 10

	r := readForward(t.Context(), s.get, web, readOpts{limits: followLimits(c, 1)})

	recs := drain(r)
	assert.NoError(t, r.err)
	assert.Len(t, recs, 10)
	assert.Equal(t, lineAt(web.key, 900*time.Millisecond, 0), r.stop)
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

// backwardOpts is a backward read in windows of 10 lines, with no limits.
func backwardOpts() readOpts {
	return readOpts{batch: 10}
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

// A backward read off the log's end reads one tailLines window, then windows of
// time: the first ends at the moment the tailLines window began in, which that
// window left out, and reads it whole.
func TestABackwardReadNeedsTwoWindows(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(15)...)
	o := backwardOpts()
	o.limits = limitsOf(defaultCaps)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"9|", "3|", "1|", "2|"}, shape(r))
	require.NoError(t, r.err)
	assert.False(t, r.short)
	assert.True(t, r.head)
	assert.Equal(t, []string{"10", "", "", ""}, tails(s))
	assert.Equal(t, []string{"", "2026-10-08T10:00:03Z", "2026-10-08T10:00:02Z", "2026-10-08T10:00:00Z"}, sinces(s))
	assert.Equal(t, "100", s.requests()[0].Get("limitBytes"))
}

// After the tailLines window the read goes on in windows of time, each sized to
// hold at most the window cap: under limits scaled down 64 times, a sparse read
// over a 250 KiB log reaches its first line in four windows.
func TestTheTailLinesWindowIsFollowedByTimeWindows(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i := range 2500 {
		s.add("prod/web/app", stamped(time.Duration(i)*10*time.Millisecond, strings.Repeat("x", 76)))
	}
	o := backwardOpts()
	o.batch, o.grep, o.limits = 501, regexp.MustCompile("never"), limitsOf(midCaps)

	r := readBackward(t.Context(), s.get, web, o)

	// The tailLines window leaves the lines of the moment it began in to the next.
	assert.Equal(t, []string{"500|", "800|", "700|", "500|"}, shape(r))
	require.NoError(t, r.err)
	assert.True(t, r.head)
	assert.Equal(t, []string{"501", "", "", ""}, tails(s))
}

// The tailLines window is read whole, whatever it holds: a runtime that does not
// split long lines serves whole lines for the parts asked for, so the window reads
// past the allowance.
func TestTheTailLinesWindowIsReadWhole(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.part = 0
	for i := range 100 {
		s.add("prod/web/app", stamped(time.Duration(i)*time.Millisecond, strings.Repeat("x", 256)))
	}
	c := smallCaps
	c.stream = 1 << 10
	o := backwardOpts()
	o.batch, o.limits = 501, limitsOf(c)
	o.more = func(context.Context) bool { return false }

	r := readBackward(t.Context(), s.get, web, o)

	// What the first line's read left of the allowance, in parts: 57 lines, the
	// first left to the next window.
	assert.Equal(t, []string{"56|"}, shape(r))
	require.NoError(t, r.err)
	assert.Equal(t, []string{"57"}, tails(s))
	assert.Greater(t, o.limits.spent, c.stream)
}

// A backward read hands over each window oldest first and its end after its last
// line, so its reader can write the window as it arrives. The read of the first moment
// is a window of its own, older than the window that found it.
func TestABackwardReadEndsEachWindow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(15)...)

	r := readBackward(t.Context(), s.get, web, backwardOpts())

	assert.Equal(t, []string{"6", "7", "8", "9", "10", "11", "12", "13", "14", "|", "3", "4", "5", "|", "2", "|", "0", "1", "|"}, windowsOf(r))
	require.NoError(t, r.err)
}

// A backward read under until ends each of its windows of time too.
func TestABackwardReadUnderUntilEndsEachWindow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(10)...)
	o := backwardOpts()
	o.batch, o.until, o.near = 2, to(5*time.Second), &rate{kept: 1, bytes: 30}

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"2", "3", "4", "5", "|", "0", "1", "|"}, windowsOf(r))
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
		"windows of time": {lines: 10, until: to(10 * time.Second), first: "0", rest: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "|"}},
		"tailLines":       {lines: 15, first: "6", rest: []string{"7", "8", "9", "10", "11", "12", "13", "14", "|", "3", "4", "5", "|", "2", "|", "0", "1", "|"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newLogServer()
			s.add("prod/web/app", lineEvery(tc.lines)...)
			gate := make(chan struct{})
			o := backwardOpts()
			o.until = tc.until

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
	c := smallCaps
	c.gets = 2
	o.limits = limitsOf(c)
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

// A log emptied under the read after its first window holds nothing older: the
// windows of time find no line, and the read ends at the backend's oldest.
func TestABackwardReadOfALogEmptiedAfterAWindowEndsAtItsHead(t *testing.T) {
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
	assert.False(t, r.short)
	assert.True(t, r.head)
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
	assert.Equal(t, []string{"10", ""}, tails(s))
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
	// Only the first window asks for tailLines; the windows of time end where it began.
	assert.Equal(t, "10", strings.Join(tails(s), ""))
}

// A backward read is cut at the end of a window once it has spent its bytes or
// sent its GETs, or its next window would be sized under the least, at the oldest
// line of that window. Each log is 200 lines a second apart, read back from the
// last.
func TestABackwardReadIsCutAtItsLimits(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		lines   []string
		limits  *limits
		batch   int64
		grep    string
		near    rate
		windows int
		stop    position
	}{
		// Lines of 2 KiB under limits scaled down 64 times: four windows of about
		// 64 KiB reach 256 KiB.
		"bytes": {
			lines: wideLines(200, 2<<10), limits: limitsOf(midCaps), batch: 10, grep: "never", near: rate{bytes: 2071},
			windows: 4, stop: lineAt(web.key, 77*time.Second, 0),
		},
		// Windows of two seconds, each holding the one line the read needs.
		"GETs": {
			lines: halfPast(200), limits: limitsOf(defaultCaps), batch: 1, near: rate{kept: 1, bytes: 205},
			windows: 15, stop: lineAt(web.key, 169*time.Second+500*time.Millisecond, 0),
		},
		// One of a hundred streams, whose first window leaves the pool under four
		// times the least window.
		"the least window": {
			lines: wideLines(200, 180), limits: queryLimits(smallCaps, 100)[0], batch: 10, grep: "never", near: rate{bytes: 202},
			windows: 1, stop: lineAt(web.key, 198*time.Second, 0),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newLogServer()
			s.add("prod/web/app", tc.lines...)
			o := backwardOpts()
			o.batch, o.limits, o.until, o.near = tc.batch, tc.limits, to(199*time.Second), &tc.near
			if tc.grep != "" {
				o.grep = regexp.MustCompile(tc.grep)
			}

			r := readBackward(t.Context(), s.get, web, o)

			got := shape(r)
			assert.NoError(t, r.err)
			assert.Len(t, got, tc.windows, "windows: %v", got)
			assert.True(t, r.short)
			assert.Equal(t, tc.stop, r.stop)
		})
	}
}

// halfPast is n lines a second apart from half past 10:00:00, saying their index.
func halfPast(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = stamped(time.Duration(i)*time.Second+500*time.Millisecond, strconv.Itoa(i))
	}
	return out
}

// wideLines is n lines a second apart from 10:00, each its index padded to size.
func wideLines(n, size int) []string {
	out := make([]string, n)
	for i := range out {
		msg := strconv.Itoa(i)
		out[i] = stamped(time.Duration(i)*time.Second, msg+strings.Repeat("x", size-len(msg)))
	}
	return out
}

// A backward read under until reads windows of time ending there, each from its
// start, so it reaches until however much log follows it.
func TestABackwardReadUnderUntilReachesIt(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(1000)...)
	o := backwardOpts()
	o.until = to(100 * time.Second)

	r := readBackward(t.Context(), s.get, web, o)

	recs := newestFirst(drain(r))
	require.NoError(t, r.err)
	assert.Len(t, recs, 101)
	assert.Equal(t, "100", recs[0].Message)
	assert.Equal(t, "0", recs[100].Message)
	assert.False(t, r.short)
	assert.Equal(t, "2026-10-08T10:01:30Z", sinces(s)[0])
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

// sinces is the sinceTime of each request after the first, which finds the first line.
func sinces(s *logServer) []string {
	var out []string
	for _, q := range s.requests()[1:] {
		out = append(out, q.Get("sinceTime"))
	}
	return out
}

// A backward read under until sizes its first window from the rate next to its point,
// ten seconds with none, and starts it at the top of a second.
func TestABackwardReadUnderUntilSizesItsFirstWindow(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		near  *rate
		since string
	}{
		"from the rate near it": {&rate{kept: 1, bytes: 30}, "2026-10-08T10:00:35Z"},
		"ten seconds":           {nil, "2026-10-08T10:00:40Z"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newLogServer()
			s.add("prod/web/app", lineEvery(100)...)
			o := backwardOpts()
			o.until, o.near = bound{position{Timestamp: at(50*time.Second + 300*time.Millisecond)}, false}, tc.near
			o.more = func(context.Context) bool { return false }

			r := readBackward(t.Context(), s.get, web, o)

			drain(r)
			require.NoError(t, r.err)
			assert.Equal(t, []string{tc.since}, sinces(s))
		})
	}
}

// A window that leaves the read short is followed by an older one sized from its own
// rate, and an empty one by one eight times longer, so a day of silence costs a few
// GETs.
func TestABackwardReadWidensPastAQuietStretch(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	day := 24 * time.Hour
	for i := range 21 {
		s.add("prod/web/app", stamped(time.Duration(i)*time.Second, "old"))
	}
	for i := range 21 {
		s.add("prod/web/app", stamped(day+time.Duration(i)*time.Second, "new"))
	}
	o := backwardOpts()
	o.batch, o.until, o.near = 30, to(day+20*time.Second), &rate{kept: 1, bytes: 30}

	r := readBackward(t.Context(), s.get, web, o)

	assert.Len(t, drain(r), 42)
	require.NoError(t, r.err)
	assert.False(t, r.short)
	assert.True(t, r.head)
	assert.Equal(t, []string{
		"2026-10-09T09:59:35Z", "2026-10-09T09:59:06Z", "2026-10-09T09:55:14Z",
		"2026-10-09T09:24:18Z", "2026-10-09T05:16:50Z", "2026-10-08T10:00:00Z",
	}, sinces(s))
}

// shape is how many lines each window r hands over held, with "|" after a window it
// ended and "/" after one it stopped part way.
func shape(r *streamRead) []string {
	var out []string
	n := 0
	for it := range r.lines.Chan() {
		switch {
		case it.windowEnd:
			out, n = append(out, strconv.Itoa(n)+"|"), 0
		case it.windowCut:
			out, n = append(out, strconv.Itoa(n)+"/"), 0
		default:
			n++
		}
	}
	return out
}

// filling makes o's read stop once the windows read whole hold need lines, as a
// fill does, answering more at each window's end, and is the shape of what the read
// sent. A read that ends without asking leaves one answer unread.
func filling(o *readOpts, need int) func(r *streamRead) []string {
	answers := make(chan bool, 1)
	o.more = func(ctx context.Context) bool {
		select {
		case ok := <-answers:
			return ok
		case <-ctx.Done():
			return false
		}
	}
	return func(r *streamRead) []string {
		var out []string
		n, held := 0, 0
		for it := range r.lines.Chan() {
			switch {
			case it.windowEnd:
				out, held, n = append(out, strconv.Itoa(n)+"|"), held+n, 0
				answers <- held < need
			case it.windowCut:
				out, n = append(out, strconv.Itoa(n)+"/"), 0
			default:
				n++
			}
		}
		return out
	}
}

// burst is a log of a line every 100ms for 65s, then 5,000 lines a second for 10s,
// and the point just after it.
func burst(s *logServer) bound {
	for i := range 650 {
		s.add("prod/web/app", stamped(time.Duration(i)*100*time.Millisecond, "quiet"))
	}
	for i := range 50000 {
		s.add("prod/web/app", stamped(65*time.Second+time.Duration(i)*200*time.Microsecond, "loud"))
	}
	return bound{position{Timestamp: at(75 * time.Second)}, false}
}

// A window guessed far too long for a burst just before the point is stopped once it
// expects four times what the read needs, and replaced from the second after its
// stop. That window is stopped in its first second, which shows that second dense,
// and is halved down to the one-second window at the point, which is read whole.
func TestABusyWindowIsReplacedFromItsStop(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	o := backwardOpts()
	o.batch, o.until, o.near = 501, burst(s), &rate{kept: 10, bytes: 400}
	o.more = func(context.Context) bool { return false }

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"766/", "501/", "501/", "501/", "501/", "5000|"}, shape(r))
	require.NoError(t, r.err)
	assert.Equal(t, []string{
		"2026-10-08T10:00:00Z", "2026-10-08T10:01:06Z", "2026-10-08T10:01:10Z",
		"2026-10-08T10:01:12Z", "2026-10-08T10:01:13Z", "2026-10-08T10:01:14Z",
	}, sinces(s))
}

// A stopped window is replaced the same way under limits: the replacement is sized
// from what the pool holds before its own reservation, so a pool the stopped
// window's reservation drained does not make every replacement the one-second
// window.
func TestABusyWindowIsReplacedFromItsStopUnderLimits(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	o := backwardOpts()
	o.batch, o.until, o.near, o.limits = 501, burst(s), &rate{kept: 10, bytes: 400}, queryLimits(defaultCaps, 4)[0]
	o.more = func(context.Context) bool { return false }

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"766/", "501/", "501/", "501/", "501/", "5000|"}, shape(r))
	require.NoError(t, r.err)
	assert.Equal(t, []string{
		"2026-10-08T10:00:00Z", "2026-10-08T10:01:06Z", "2026-10-08T10:01:10Z",
		"2026-10-08T10:01:12Z", "2026-10-08T10:01:13Z", "2026-10-08T10:01:14Z",
	}, sinces(s))
}

// A stopped window is not the read's edge: a read that fails after the window that
// replaced it stops at that window's oldest line, not the island's.
func TestAStoppedWindowIsNotTheEdge(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	o := backwardOpts()
	o.batch, o.until, o.near = 501, burst(s), &rate{kept: 10, bytes: 400}
	s.onRequest = func(n int) {
		if n == 7 {
			s.setRefusal("prod/web/app", &refusal{503, "unavailable"})
		}
	}

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"766/", "501/", "501/", "501/", "501/", "5000|"}, shape(r))
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web.key, 74*time.Second, 0), r.stop)
}

// A window sized to a reach of one second is the one-second window: the second its
// end lies in, or the one before when its end is a second's top.
func TestAOneSecondWindowIsOneSecondOfTheClock(t *testing.T) {
	t.Parallel()
	assert.Equal(t, at(50*time.Second), startOf(at(50*time.Second+300*time.Millisecond), time.Second, at(0)))
	assert.Equal(t, at(49*time.Second), startOf(at(50*time.Second), time.Second, at(0)))
	assert.Equal(t, at(50*time.Second+100*time.Millisecond), startOf(at(50*time.Second+300*time.Millisecond), time.Second, at(50*time.Second+100*time.Millisecond)))
}

// A dense second far from the point stops the window that reaches it there; the
// window from the second after it completes, the window that ends at the second is
// stopped in its last second, and the one-second window reads it whole. No window
// starts at a second it already read.
func TestADenseSecondFarFromThePoint(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i := range 60 {
		s.add("prod/web/app", stamped(time.Duration(i)*time.Second, "quiet"))
	}
	for i := range 5000 {
		s.add("prod/web/app", stamped(60*time.Second+time.Duration(i)*200*time.Microsecond, "dense"))
	}
	for i := range 30 {
		s.add("prod/web/app", stamped(time.Duration(61+i)*time.Second, "quiet"))
	}
	o := backwardOpts()
	o.batch, o.until, o.near = 501, after(91*time.Second), &rate{kept: 1, bytes: 30}
	o.limits = limitsOf(defaultCaps)
	shape := filling(&o, 501)

	r := readBackward(t.Context(), s.get, web, o)

	got := shape(r)
	require.NoError(t, r.err)
	assert.False(t, r.short)
	assert.Equal(t, []string{"501/", "30|", "1151/", "5000|"}, got)
	assert.Equal(t, []string{"2026-10-08T10:00:00Z", "2026-10-08T10:01:01Z", "2026-10-08T10:00:00Z", "2026-10-08T10:01:00Z"}, sinces(s))
}

// A dense stretch far from the point is found by halving: each window stopped in its
// first second is halved toward the point, and the newest such second bounds the
// windows widened after a quiet one, so the read reaches the stretch's newest second
// as the one-second window. Here five seconds of 2,000 lines, a line every ten
// seconds from a minute later, and the point ten minutes on.
func TestADenseStretchFarFromThePoint(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for sec := range 5 {
		for i := range 2000 {
			s.add("prod/web/app", stamped(time.Duration(sec)*time.Second+time.Duration(i)*500*time.Microsecond, "dense"))
		}
	}
	for i := 6; i < 60; i++ {
		s.add("prod/web/app", stamped(time.Duration(i)*10*time.Second, "quiet"))
	}
	o := backwardOpts()
	o.batch, o.until, o.near = 501, after(10*time.Minute), &rate{kept: 0.1, bytes: 3}
	shape := filling(&o, 501)

	r := readBackward(t.Context(), s.get, web, o)

	got := shape(r)
	require.NoError(t, r.err)
	assert.Equal(t, "2000|", got[len(got)-1], "the stretch's newest second whole: %v", got)
	assert.Equal(t, "2026-10-08T10:00:04Z", sinces(s)[len(got)-1])
	assert.LessOrEqual(t, len(got), 2*10, "about two GETs a halving of the ten minutes: %v", got)
}

// A chain of stops ends: once the stream has sent its GETs, a stopped window is
// replaced by the one-second window, which completes, and the read is cut at its
// oldest line. A rate near nothing sizes every window to the whole log, and each is
// stopped at its first line.
func TestAChainOfStopsEnds(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web/app", lineEvery(60)...)
	c := defaultCaps
	c.gets = 6
	o := backwardOpts()
	o.batch, o.until, o.near, o.limits = 1, to(59*time.Second), &rate{kept: 1e-6, bytes: 1e-6}, limitsOf(c)

	r := readBackward(t.Context(), s.get, web, o)

	assert.Equal(t, []string{"1/", "1/", "1/", "1/", "1/", "2|"}, shape(r))
	require.NoError(t, r.err)
	assert.True(t, r.short)
	assert.Equal(t, lineAt(web.key, 58*time.Second, 0), r.stop)
	assert.Len(t, s.requests(), 7)
}

// A window counts each line as the body carried it, before the message is cleaned
// and cut: a log of 200 KiB lines, which the file keeps as 16 KiB, stops a window
// sized for 4 MiB.
func TestAWindowCountsTheBytesItsBodyCarried(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	long := strings.Repeat("x", 200<<10)
	for i := range 100 {
		s.add("prod/web/app", stamped(time.Duration(i)*time.Second, long))
	}
	o := backwardOpts()
	o.batch, o.until, o.grep = 100, to(99*time.Second), regexp.MustCompile("never")
	o.limits, o.near = limitsOf(defaultCaps), &rate{bytes: 1000}
	o.more = func(context.Context) bool { return false }

	r := readBackward(t.Context(), s.get, web, o)

	got := shape(r)
	require.NoError(t, r.err)
	assert.True(t, strings.HasSuffix(got[0], "/"), "stopped: %v", got)
	assert.True(t, strings.HasSuffix(got[len(got)-1], "|"), "completed: %v", got)
}

// A window with no line widens eight times, up to as far as a duration goes.
func TestAnEmptyWindowWidensEightTimes(t *testing.T) {
	t.Parallel()
	r := &reader{}
	assert.Equal(t, 80*time.Second, r.widen(sample{}, 10*time.Second, 0, byteTarget))
	assert.Equal(t, maxReach, r.widen(sample{}, maxReach/4, 0, byteTarget))
}

// A read cancelled as it stops a window ends clean, with no end sent for the window.
func TestAReadCancelledAtAStoppedWindowEndsClean(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	o := backwardOpts()
	o.batch, o.until, o.near = 501, burst(s), &rate{kept: 10, bytes: 400}
	ctx, cancel := context.WithCancel(t.Context())
	r := readBackward(ctx, s.get, web, o)

	for range 766 {
		assert.False(t, testutil.Recv(t, r.lines.Chan(), "a line of the stopped window").windowCut)
	}
	cancel()

	testutil.WaitClosed(t, r.lines.Chan(), "the read to end")
	assert.NoError(t, r.err)
	assert.False(t, r.short)
}
