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
var web = stream{key: StreamKey{Namespace: "prod", Pod: "web", Container: "app"}, place: Place{Node: "node-a"}}

// A forward read since a moment asks for its second and keeps that moment's line.
func TestAForwardReadSinceAMoment(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(500*time.Millisecond, "zero"), stamped(1200*time.Millisecond, "one"), stamped(2*time.Second, "two"))

	r := readForward(t.Context(), s.get, web, readOpts{since: from(1200 * time.Millisecond)})

	recs := drain(r)
	require.NoError(t, r.err)
	assert.False(t, r.short)
	assert.Equal(t, []string{"one", "two"}, messages(recs))
	assert.Equal(t, web.key, recs[0].Stream)
	assert.Equal(t, web.place, recs[0].Place)
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

	after := readForward(t.Context(), get, web, readOpts{since: bound{lineAt(web.key, time.Second, 1), false}})
	assert.Equal(t, []string{"c", "d"}, messages(drain(after)))
	until := readForward(t.Context(), get, web, readOpts{until: bound{lineAt(web.key, time.Second, 1), true}})
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
	src.key.Previous = true

	r := readForward(t.Context(), s.get, src, readOpts{})

	recs := drain(r)
	assert.Equal(t, []string{"before the restart"}, messages(recs))
	assert.True(t, recs[0].Stream.Previous)
	assert.Equal(t, "true", s.requests()[0].Get("previous"))
}

// A follow since a moment reads the lines from it, then one written after it opened.
func TestAFollowDeliversALineWrittenLater(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "old"))

	r := readForward(t.Context(), claimOn(t, s).get, web, readOpts{since: from(0), follow: true})

	assert.Equal(t, "old", testutil.Recv(t, r.lines.Chan(), "the old line").Message)
	s.follow("prod/web/app") <- stamped(time.Second, "new")
	assert.Equal(t, "new", testutil.Recv(t, r.lines.Chan(), "the new line").Message)
	assert.Equal(t, "true", s.requests()[0].Get("follow"))
}

// A follow numbers a moment's lines from the top of its second, as a read opened
// again from a cursor in it does, so a line keeps its ordinal across the two.
func TestAFollowNumbersAMomentFromItsSecond(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(100*time.Millisecond, "a"), stamped(100*time.Millisecond, "b"))

	r := readForward(t.Context(), claimOn(t, s).get, web, readOpts{follow: true, since: from(100 * time.Millisecond)})
	first := testutil.Recv(t, r.lines.Chan(), "the first line")
	assert.Equal(t, 0, first.Ordinal)

	again := readForward(t.Context(), claimOn(t, s).get, web, readOpts{follow: true, since: bound{positionOf(first), false}})
	assert.Equal(t, "b", testutil.Recv(t, again.lines.Chan(), "the line after it").Message)
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
	assert.Equal(t, lineAt(web.key, 0, 0), r.stop)
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
	for name, read := range map[string]func(context.Context) *streamRead{
		"forward": func(ctx context.Context) *streamRead { return readForward(ctx, get, web, readOpts{}) },
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
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	get := func(ctx context.Context, _ string, _ url.Values) (io.ReadCloser, error) {
		return nil, ctx.Err()
	}

	r := readForward(ctx, get, web, readOpts{})

	assert.Empty(t, drain(r))
	assert.NoError(t, r.err)
	assert.False(t, r.short)
}

// A line each does not take is not counted as read.
func TestALineNotTakenIsNotRead(t *testing.T) {
	s := newLogServer()
	s.add("prod/web/app", stamped(0, "a"), stamped(time.Second, "b"))
	rd := &reader{ctx: t.Context(), get: s.get, key: web.key}

	read, err := rd.lines(rd.query(time.Time{}), readOpts{}, func(rec Record) bool { return rec.Message == "a" })

	require.NoError(t, err)
	assert.Equal(t, lineAt(web.key, 0, 0), read.last)
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

// A body that fails under the read is the read's error.
func TestABodyThatFailsEndsTheRead(t *testing.T) {
	get := func(context.Context, string, url.Values) (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(strings.NewReader(stamped(0, "a")+"\n"), iotest.ErrReader(errors.New("reset")))), nil
	}

	r := readForward(t.Context(), get, web, readOpts{})

	assert.Equal(t, []string{"a"}, messages(drain(r)))
	assert.EqualError(t, r.err, "reset")
}
