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

	"github.com/amorey/gochan/spsc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// canned is a reading of the first record's stream that sends recs, then ends
// clean.
func canned(ctx context.Context, recs ...Record) *streamRead {
	return cannedFrom(ctx, recs[0].Stream, recs, nil, position{})
}

// cannedFrom is a reading of key that sends recs, then ends with err, and short
// at stop when stop is set.
func cannedFrom(ctx context.Context, key StreamKey, recs []Record, err error, stop position) *streamRead {
	return cannedUntil(ctx, key, recs, nil, err, stop)
}

// cannedUntil is cannedFrom that, once it has sent recs, ends only once wait
// closes, a nil wait at once.
func cannedUntil(ctx context.Context, key StreamKey, recs []Record, wait <-chan struct{}, err error, stop position) *streamRead {
	ctx, cancel := context.WithCancel(ctx)
	out, lines := spsc.New[Record](0)
	r := &streamRead{key: key, lines: lines, cancel: cancel}
	go func() {
		defer out.Close()
		for _, rec := range recs {
			if out.SendContext(ctx, rec) != nil {
				return
			}
		}
		if wait != nil {
			select {
			case <-wait:
			case <-ctx.Done():
				return
			}
		}
		r.err, r.short, r.stop = err, !stop.isZero(), stop
	}()
	return r
}

// stalled is a reading of key that sends recs, then waits until it is cancelled.
func stalled(ctx context.Context, key StreamKey, recs ...Record) *streamRead {
	ctx, cancel := context.WithCancel(ctx)
	out, lines := spsc.New[Record](0)
	r := &streamRead{key: key, lines: lines, cancel: cancel}
	go func() {
		defer out.Close()
		for _, rec := range recs {
			if out.SendContext(ctx, rec) != nil {
				break
			}
		}
		<-ctx.Done()
		r.err = ctx.Err()
	}()
	return r
}

// endedShort is a reading of key that has ended with err, short before any line
// in range.
func endedShort(key StreamKey, err error) *streamRead {
	out, lines := spsc.New[Record](0)
	out.Close()
	return &streamRead{key: key, lines: lines, cancel: func() {}, err: err, short: true}
}

// A fetch's lines from three streams come out in time order.
func TestAFetchMergesByTime(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "a0"), line("a", "app", 3*time.Second, "a3")),
		canned(ctx, line("b", "app", time.Second, "b1"), line("b", "app", 4*time.Second, "b4")),
		canned(ctx, line("c", "app", 2*time.Second, "c2")),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "b1", "c2", "a3", "b4"}, messages(page.records))
	assert.Nil(t, page.next())
	assert.Empty(t, page.skipped)
}

// Lines stamped alike come out by pod, then container, then ordinal, whichever
// stream is first.
func TestATieKeepsAFixedOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for _, mode := range []FetchMode{FetchHead, FetchTail} {
		// A stream's lines come in the order the page reads them.
		app := []Record{line("a", "app", 0, "a-app"), nth("a", "app", 0, 1, "a-app'")}
		if mode == FetchTail {
			slices.Reverse(app)
		}
		page, err := mergePage(ctx, []*streamRead{
			canned(ctx, line("b", "app", 0, "b")),
			canned(ctx, line("a", "side", 0, "a-side")),
			canned(ctx, app...),
		}, mode, 10)

		require.NoError(t, err)
		assert.Equal(t, []string{"a-app", "a-app'", "a-side", "b"}, messages(page.records), mode)
	}
}

// Head keeps the oldest lines and hands back the last one's cursor.
func TestHeadCutsAtTheLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "0"), line("a", "app", time.Second, "1"), line("a", "app", 2*time.Second, "2")),
	}, FetchHead, 2)

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1"}, messages(page.records))
	assert.Equal(t, lineCursor("a", "app", time.Second, 0), page.next())
}

// Tail reads newest first, keeps the newest lines in time order, and hands back
// the oldest one's cursor.
func TestTailCutsAtTheLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 2*time.Second, "2"), line("a", "app", time.Second, "1"), line("a", "app", 0, "0")),
	}, FetchTail, 2)

	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, messages(page.records))
	assert.Equal(t, lineCursor("a", "app", time.Second, 0), page.next())
}

// A page cut inside a moment hands back the cursor of the line it ended at, which
// names that line among the moment's, so the next page goes on from the next.
func TestAPageCutInsideAMomentNamesItsLine(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "a0"), nth("a", "app", 0, 1, "a0'"), line("a", "app", time.Second, "a1")),
		canned(ctx, line("b", "app", 0, "b0")),
	}, FetchHead, 2)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a0'"}, messages(page.records))
	assert.Equal(t, lineCursor("a", "app", 0, 1), page.next())
}

// failed is a reading of pod/container that ends with err and sends nothing.
func failed(ctx context.Context, pod, container string, err error) *streamRead {
	return cannedFrom(ctx, StreamKey{Namespace: "prod", Pod: pod, Container: container}, nil, err, position{})
}

// A stream answering 400 or 404 is skipped and named, and the others fill the page.
func TestAFailedStreamIsSkippedAndNamed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "ok")),
		failed(ctx, "b", "istio-proxy", &statusError{http.StatusBadRequest, "container is waiting to start"}),
		failed(ctx, "c", "app", &statusError{http.StatusNotFound, `pods "c" not found`}),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"ok"}, messages(page.records))
	assert.Equal(t, []string{"b/istio-proxy: container is waiting to start", `c/app: pods "c" not found`}, page.skipped)
}

// A stream's transport error is named in skipped as safe renders it, with its
// controls stripped.
func TestASkippedStreamsErrorIsCleaned(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "a0")),
		failed(ctx, "b", "app", errors.New("Get \"https://me:hunter2@api/log\": reset\x1b[2J")),
	}, FetchHead, 10)

	require.NoError(t, err)
	require.Len(t, page.skipped, 1)
	assert.NotContains(t, page.skipped[0], "hunter2")
	assert.NotContains(t, page.skipped[0], "\x1b")
}

// Every stream failing fails the page, naming the first.
func TestEveryStreamFailingFailsThePage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, err := mergePage(ctx, []*streamRead{
		failed(ctx, "a", "app", &statusError{http.StatusNotFound, "not found"}),
	}, FetchHead, 10)

	assert.EqualError(t, err, "no container of this view could be read: a/app: not found")
}

// A 401 on any stream fails the page: the user's credentials, not a pod's state.
func TestAnUnauthorizedStreamFailsThePage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "ok")),
		failed(ctx, "b", "app", &statusError{http.StatusUnauthorized, "unauthorized"}),
	}, FetchHead, 10)

	assert.EqualError(t, err, "the cluster refused to show this log: unauthorized")
}

// A 403 is one namespace's, so the page skips that stream and keeps the rest.
func TestAForbiddenStreamIsSkipped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "ok")),
		failed(ctx, "b", "app", &statusError{http.StatusForbidden, "forbidden"}),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"ok"}, messages(page.records))
	assert.Equal(t, []string{"b/app: forbidden"}, page.skipped)
}

// A stream the connection's retirement cut ends the page where it stopped, so the
// next page reads its lines rather than passing them; one cut before any line
// refuses the page.
func TestARetiredStreamEndsThePageWhereItStopped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a0 := line("a", "app", 0, "a0")
	page, err := mergePage(ctx, []*streamRead{
		cannedFrom(ctx, a0.Stream, []Record{a0}, errRetired, positionOf(a0)),
		canned(ctx, line("b", "app", 0, "b0"), line("b", "app", time.Second, "b1")),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0"}, messages(page.records))
	assert.Empty(t, page.skipped)
	require.NotNil(t, page.next())

	_, err = mergePage(ctx, []*streamRead{endedShort(a0.Stream, errRetired), canned(ctx, line("b", "app", 0, "b0"))}, FetchHead, 10)
	assert.ErrorIs(t, err, errReplaced)
}

// A stream that failed past a line in range ends the page where it stopped, as a
// retired one does, rather than letting the page pass lines it never read.
func TestAStreamThatFailedPartwayEndsThePageWhereItStopped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a0 := line("a", "app", 0, "a0")
	page, err := mergePage(ctx, []*streamRead{
		cannedFrom(ctx, a0.Stream, []Record{a0}, errors.New("connection reset"), positionOf(a0)),
		canned(ctx, line("b", "app", 0, "b0"), line("b", "app", time.Second, "b1")),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0"}, messages(page.records))
	assert.Empty(t, page.skipped)
	assert.Equal(t, cursorOf(positionOf(a0)), page.next())
}

// A merge that has its lines asks its streams for none past them.
func TestAFullMergeReadsNoFurther(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	more, lines := spsc.New[Record](0)
	full := &streamRead{lines: lines}
	go func() { _ = more.Send(line("a", "app", 0, "a0")) }()

	got := make(chan []Record, 1)
	go func() { got <- merged(ctx, []*streamRead{full}, FetchHead, 1) }()

	assert.Equal(t, []string{"a0"}, messages(testutil.Recv(t, got, "the merge to end")))
}

// A stream that ran out of budget before it read a line in range leaves no point
// a next page could go on from, so the page is refused rather than read as the
// end.
func TestAStreamStoppedAtItsStartRefusesThePage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "a0")),
		endedShort(StreamKey{Pod: "b", Container: "app"}, nil),
	}, FetchHead, 10)

	assert.EqualError(t, err, "this page ran out of budget before b/app was read past its start; narrow the view or its grep")
}

// A page whose context ends mid-merge fails with it rather than reading as the
// end of the log.
func TestAPageEndsWithItsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	got := make(chan error, 1)
	go func() {
		_, err := mergePage(ctx, []*streamRead{stalled(t.Context(), b)}, FetchHead, 10)
		got <- err
	}()
	cancel()

	assert.ErrorIs(t, testutil.Recv(t, got, "the page to end"), context.Canceled)
}

// A full page cancels the readings still going, so it waits on none of them.
func TestAFullPageCancelsItsReadings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	got := make(chan merge, 1)
	go func() {
		page, err := mergePage(ctx, []*streamRead{
			stalled(ctx, b, line("b", "app", 0, "b0"), line("b", "app", time.Second, "b1")),
		}, FetchHead, 1)
		assert.NoError(t, err)
		got <- page
	}()

	page := testutil.Recv(t, got, "the page to end")
	assert.Equal(t, []string{"b0"}, messages(page.records))
	assert.Equal(t, lineCursor("b", "app", 0, 0), page.next())
}

// A merge ends at the first line past a stream's stop, asking no stream for more.
func TestAMergeEndsAtAStop(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a0 := line("a", "app", 0, "a0")
	got := make(chan merge, 1)
	go func() {
		page, err := mergePage(ctx, []*streamRead{
			cannedFrom(ctx, a0.Stream, []Record{a0}, nil, positionOf(a0)),
			// b would wait for its cancel if asked for a line past b1.
			stalled(ctx, b, line("b", "app", time.Second, "b1")),
		}, FetchHead, 10)
		assert.NoError(t, err)
		got <- page
	}()

	page := testutil.Recv(t, got, "the page to end")
	assert.Equal(t, []string{"a0"}, messages(page.records))
	assert.Equal(t, cursorOf(positionOf(a0)), page.next())
}

// b is the container app of pod b in prod.
var b = StreamKey{Namespace: "prod", Pod: "b", Container: "app"}

// A backward read that spent its budget keeps only the lines at and after the
// newest point any stream stopped at, and hands that point back.
func TestABudgetKeepsTheNearSideOfTheNewestStop(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 5*time.Second, "a5"), line("a", "app", time.Second, "a1")),
		cannedFrom(ctx, b, []Record{line("b", "app", 4*time.Second, "b4"), line("b", "app", 3*time.Second, "b3")}, nil, lineAt(b, 3*time.Second, 0)),
	}, FetchTail, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"b3", "b4", "a5"}, messages(page.records))
	assert.Equal(t, lineCursor("b", "app", 3*time.Second, 0), page.next())
}

// A budget spent before any line matched answers no lines with a cursor.
func TestABudgetCanAnswerNoLinesWithACursor(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		cannedFrom(ctx, b, nil, nil, lineAt(b, 7*time.Second, 0)),
	}, FetchTail, 10)

	require.NoError(t, err)
	assert.Empty(t, page.records)
	assert.Equal(t, lineCursor("b", "app", 7*time.Second, 0), page.next())
}

// A forward read that spent its budget keeps the lines at and before the oldest
// stop.
func TestABudgetKeepsTheNearSideOfTheOldestStop(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	page, err := mergePage(ctx, []*streamRead{
		canned(ctx, line("a", "app", 0, "a0"), line("a", "app", 5*time.Second, "a5")),
		cannedFrom(ctx, b, []Record{line("b", "app", 2*time.Second, "b2")}, nil, lineAt(b, 2*time.Second, 0)),
	}, FetchHead, 10)

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "b2"}, messages(page.records))
	assert.Equal(t, lineCursor("b", "app", 2*time.Second, 0), page.next())
}
