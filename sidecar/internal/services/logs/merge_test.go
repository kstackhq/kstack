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
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// canned is a reading of the first record's source that sends recs, then ends
// clean.
func canned(ctx context.Context, recs ...Record) *reading {
	return cannedFrom(ctx, recs[0].Source, recs, nil, position{})
}

// cannedFrom is a reading of src that sends recs, then ends with err, and short
// at stop when stop is set.
func cannedFrom(ctx context.Context, src RecordSource, recs []Record, err error, stop position) *reading {
	out := make(chan Record)
	r := &reading{src: src, lines: out}
	go func() {
		defer close(out)
		for _, rec := range recs {
			if !send(ctx, out, rec) {
				return
			}
		}
		r.err, r.short, r.stop = err, !stop.isZero(), stop
	}()
	return r
}

// A fetch's lines from three streams come out in time order.
func TestAFetchMergesByTime(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "a0"), line("a", "app", 3*time.Second, "a3")),
		canned(ctx, line("b", "app", time.Second, "b1"), line("b", "app", 4*time.Second, "b4")),
		canned(ctx, line("c", "app", 2*time.Second, "c2")),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "b1", "c2", "a3", "b4"}, messages(page.Records))
	assert.Nil(t, page.NextCursor)
	assert.Empty(t, page.Skipped)
}

// Lines stamped alike come out by pod, then container, then ordinal, whichever
// stream is first.
func TestATieKeepsAFixedOrder(t *testing.T) {
	ctx := t.Context()
	for _, mode := range []FetchMode{FetchHead, FetchTail} {
		// A stream's lines come in the order the page reads them.
		app := []Record{line("a", "app", 0, "a-app"), nth("a", "app", 0, 1, "a-app'")}
		if mode == FetchTail {
			slices.Reverse(app)
		}
		page, err := mergePage(ctx, func() {}, []*reading{
			canned(ctx, line("b", "app", 0, "b")),
			canned(ctx, line("a", "side", 0, "a-side")),
			canned(ctx, app...),
		}, mode, 10)

		require.NoError(t, err)
		assert.Equal(t, []string{"a-app", "a-app'", "a-side", "b"}, messages(page.Records), mode)
	}
}

// Head keeps the oldest lines and hands back the last one's cursor.
func TestHeadCutsAtTheLimit(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "0"), line("a", "app", time.Second, "1"), line("a", "app", 2*time.Second, "2")),
	}, FetchHead, 2)

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1"}, messages(page.Records))
	assert.Equal(t, lineCursor("a", "app", time.Second, 0), page.NextCursor)
}

// Tail reads newest first, keeps the newest lines in time order, and hands back
// the oldest one's cursor.
func TestTailCutsAtTheLimit(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 2*time.Second, "2"), line("a", "app", time.Second, "1"), line("a", "app", 0, "0")),
	}, FetchTail, 2)

	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, messages(page.Records))
	assert.Equal(t, lineCursor("a", "app", time.Second, 0), page.NextCursor)
}

// A page cut inside a moment hands back the cursor of the line it ended at, which
// names that line among the moment's, so the next page goes on from the next.
func TestAPageCutInsideAMomentNamesItsLine(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "a0"), nth("a", "app", 0, 1, "a0'"), line("a", "app", time.Second, "a1")),
		canned(ctx, line("b", "app", 0, "b0")),
	}, FetchHead, 2)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a0'"}, messages(page.Records))
	assert.Equal(t, lineCursor("a", "app", 0, 1), page.NextCursor)
}

// failed is a reading of pod/container that ends with err and sends nothing.
func failed(ctx context.Context, pod, container string, err error) *reading {
	return cannedFrom(ctx, RecordSource{Namespace: "prod", Pod: pod, Container: container}, nil, err, position{})
}

// A stream answering 400 or 404 is skipped and named, and the others fill the page.
func TestAFailedStreamIsSkippedAndNamed(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "ok")),
		failed(ctx, "b", "istio-proxy", &statusError{http.StatusBadRequest, "container is waiting to start"}),
		failed(ctx, "c", "app", &statusError{http.StatusNotFound, `pods "c" not found`}),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"ok"}, messages(page.Records))
	assert.Equal(t, []string{"b/istio-proxy: container is waiting to start", `c/app: pods "c" not found`}, page.Skipped)
}

// A stream's transport error is named in skipped as safe renders it, with its
// controls stripped.
func TestASkippedStreamsErrorIsCleaned(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "a0")),
		failed(ctx, "b", "app", errors.New("Get \"https://me:hunter2@api/log\": reset\x1b[2J")),
	}, FetchHead, 10)

	require.NoError(t, err)
	require.Len(t, page.Skipped, 1)
	assert.NotContains(t, page.Skipped[0], "hunter2")
	assert.NotContains(t, page.Skipped[0], "\x1b")
}

// Every stream failing fails the page, naming the first.
func TestEveryStreamFailingFailsThePage(t *testing.T) {
	ctx := t.Context()
	_, err := mergePage(ctx, func() {}, []*reading{
		failed(ctx, "a", "app", &statusError{http.StatusNotFound, "not found"}),
	}, FetchHead, 10)

	assert.EqualError(t, err, "no container of this view could be read: a/app: not found")
}

// A 401 on any stream fails the page: the user's credentials, not a pod's state.
func TestAnUnauthorizedStreamFailsThePage(t *testing.T) {
	ctx := t.Context()
	_, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "ok")),
		failed(ctx, "b", "app", &statusError{http.StatusUnauthorized, "unauthorized"}),
	}, FetchHead, 10)

	assert.EqualError(t, err, "the cluster refused to show this log: unauthorized")
}

// A 403 is one namespace's, so the page skips that stream and keeps the rest.
func TestAForbiddenStreamIsSkipped(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "ok")),
		failed(ctx, "b", "app", &statusError{http.StatusForbidden, "forbidden"}),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"ok"}, messages(page.Records))
	assert.Equal(t, []string{"b/app: forbidden"}, page.Skipped)
}

// A stream the connection's retirement cut ends the page where it stopped, so the
// next page reads its lines rather than passing them; one cut before any line
// refuses the page.
func TestARetiredStreamEndsThePageWhereItStopped(t *testing.T) {
	ctx := t.Context()
	a0 := line("a", "app", 0, "a0")
	page, err := mergePage(ctx, func() {}, []*reading{
		cannedFrom(ctx, a0.Source, []Record{a0}, errRetired, positionOf(a0)),
		canned(ctx, line("b", "app", 0, "b0"), line("b", "app", time.Second, "b1")),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0"}, messages(page.Records))
	assert.Empty(t, page.Skipped)
	require.NotNil(t, page.NextCursor)

	ended := make(chan Record)
	close(ended)
	early := &reading{src: a0.Source, lines: ended, err: errRetired, short: true}
	_, err = mergePage(ctx, func() {}, []*reading{early, canned(ctx, line("b", "app", 0, "b0"))}, FetchHead, 10)
	assert.ErrorIs(t, err, errReplaced)
}

// A stream that failed past a line in range ends the page where it stopped, as a
// retired one does, rather than letting the page pass lines it never read.
func TestAStreamThatFailedPartwayEndsThePageWhereItStopped(t *testing.T) {
	ctx := t.Context()
	a0 := line("a", "app", 0, "a0")
	page, err := mergePage(ctx, func() {}, []*reading{
		cannedFrom(ctx, a0.Source, []Record{a0}, errors.New("connection reset"), positionOf(a0)),
		canned(ctx, line("b", "app", 0, "b0"), line("b", "app", time.Second, "b1")),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0"}, messages(page.Records))
	assert.Empty(t, page.Skipped)
	assert.Equal(t, cursorOf(positionOf(a0)), page.NextCursor)
}

// A merge that has its lines asks its streams for none past them.
func TestAFullMergeReadsNoFurther(t *testing.T) {
	ctx := t.Context()
	more := make(chan Record)
	full := &reading{lines: more}
	go func() { more <- line("a", "app", 0, "a0") }()

	got := make(chan []Record, 1)
	go func() { got <- merged(ctx, []*reading{full}, FetchHead, 1) }()

	assert.Equal(t, []string{"a0"}, messages(testutil.Recv(t, got, "the merge to end")))
}

// A stream that ran out of budget before it read a line in range leaves no point
// a next page could go on from, so the page is refused rather than read as the
// end.
func TestAStreamStoppedAtItsStartRefusesThePage(t *testing.T) {
	ctx := t.Context()
	closed := make(chan Record)
	close(closed)
	starved := &reading{src: RecordSource{Pod: "b", Container: "app"}, lines: closed, short: true}
	_, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "a0")),
		starved,
	}, FetchHead, 10)

	assert.EqualError(t, err, "this page ran out of budget before b/app was read past its start; narrow the view or its grep")
}

// A merge whose context ends waits on no reading.
func TestAMergeEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, ok := next(ctx, &reading{lines: make(chan Record)})

	assert.False(t, ok)
}

// b is the container app of pod b in prod.
var b = RecordSource{Namespace: "prod", Pod: "b", Container: "app"}

// A backward read that spent its budget keeps only the lines at and after the
// newest point any stream stopped at, and hands that point back.
func TestABudgetKeepsTheNearSideOfTheNewestStop(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 5*time.Second, "a5"), line("a", "app", time.Second, "a1")),
		cannedFrom(ctx, b, []Record{line("b", "app", 4*time.Second, "b4"), line("b", "app", 3*time.Second, "b3")}, nil, lineAt(b, 3*time.Second, 0)),
	}, FetchTail, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"b3", "b4", "a5"}, messages(page.Records))
	assert.Equal(t, lineCursor("b", "app", 3*time.Second, 0), page.NextCursor)
}

// A budget spent before any line matched answers no lines with a cursor.
func TestABudgetCanAnswerNoLinesWithACursor(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		cannedFrom(ctx, b, nil, nil, lineAt(b, 7*time.Second, 0)),
	}, FetchTail, 10)

	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Equal(t, lineCursor("b", "app", 7*time.Second, 0), page.NextCursor)
}

// A forward read that spent its budget keeps the lines at and before the oldest
// stop.
func TestABudgetKeepsTheNearSideOfTheOldestStop(t *testing.T) {
	ctx := t.Context()
	page, err := mergePage(ctx, func() {}, []*reading{
		canned(ctx, line("a", "app", 0, "a0"), line("a", "app", 5*time.Second, "a5")),
		cannedFrom(ctx, b, []Record{line("b", "app", 2*time.Second, "b2")}, nil, lineAt(b, 2*time.Second, 0)),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "b2"}, messages(page.Records))
	assert.Equal(t, lineCursor("b", "app", 2*time.Second, 0), page.NextCursor)
}

// A follow sends one stream's lines while another stays silent.
func TestAFollowDoesNotWaitOnASilentStream(t *testing.T) {
	ctx := t.Context()
	f := newFanIn()
	silent := make(chan Record)
	f.add(ctx, &reading{src: RecordSource{Pod: "quiet"}, lines: silent})
	talking := make(chan Record)
	r := &reading{src: RecordSource{Pod: "a"}, lines: talking}
	f.add(ctx, r)

	talking <- line("a", "app", 0, "one")
	first := testutil.Recv(t, f.lines, "the first line")
	assert.Equal(t, "one", first.rec.Message)
	assert.Same(t, r, first.from)
	talking <- line("a", "app", time.Second, "two")
	assert.Equal(t, "two", testutil.Recv(t, f.lines, "the second line").rec.Message)
}

// A fan-in whose follow ends drains the reading it was relaying.
func TestAFanInDrainsOnceTheFollowEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	f := newFanIn()
	lines := make(chan Record)
	f.add(ctx, &reading{src: RecordSource{Pod: "a"}, lines: lines})
	lines <- line("a", "app", 0, "held")

	cancel()

	// Taken only once the relay has given up on the follow and is draining.
	lines <- line("a", "app", time.Second, "drained")
	close(lines)
}

// A reading that ends is reported, with its error on it.
func TestAFollowedReadingReportsItsEnd(t *testing.T) {
	ctx := t.Context()
	f := newFanIn()
	r := cannedFrom(ctx, RecordSource{Pod: "a", Container: "app"}, []Record{line("a", "app", time.Second, "x")}, errors.New("gone"), position{})
	f.add(ctx, r)

	testutil.Recv(t, f.lines, "the line")
	end := testutil.Recv(t, f.ended, "the end")

	assert.Same(t, r, end)
	assert.EqualError(t, end.err, "gone")
}

// The lines that arrive before the handoff closes go out sorted, and every later
// one goes out as it comes.
func TestTheHandoffSortsTheFirstLines(t *testing.T) {
	var h handoff

	assert.True(t, h.hold(line("b", "app", 2*time.Second, "b2")))
	assert.True(t, h.hold(line("a", "app", time.Second, "a1")))
	assert.True(t, h.hold(line("a", "app", 3*time.Second, "a3")))
	assert.Equal(t, []string{"a1", "b2", "a3"}, messages(h.release()))
	assert.False(t, h.hold(line("c", "app", 0, "late")))
}
