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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// A page over a range the file does not cover fills it from the kubelet: the lines
// it read are in the file afterwards, under a chunk claiming them.
func TestAPageOverAGapFillsIt(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(5)...)
	svc, f := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "2", "3", "4"}, messages(page.Records))
	chunks := chunksOf(t, svc, f, "web-1", "app")
	require.Len(t, chunks, 1)
	assert.Equal(t, logstore.Pos{TS: at(0).UnixNano()}, chunks[0].From)
	assert.Equal(t, logstore.Pos{TS: at(4 * time.Second).UnixNano()}, chunks[0].To)
	assert.True(t, chunks[0].Head, "a forward read from the start begins at the kubelet's oldest line")
}

// A second page over a range the first filled reads it from the file and asks the
// kubelet nothing.
func TestASecondPageOverTheSameRangeMakesNoRead(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	q := FetchQuery{Mode: FetchHead, Limit: 3}

	first, err := svc.Fetch(t.Context(), "7", web1View, q)
	require.NoError(t, err)
	reads := len(s.requests())
	second, err := svc.Fetch(t.Context(), "7", web1View, q)

	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, []string{"0", "1", "2"}, messages(second.Records))
	assert.Len(t, s.requests(), reads)
}

// A sparse grep on a Tail page fills back until the stream holds limit plus one
// matches, and pages on from there; one whose budget runs out first answers the honest
// short page, cut where its reads reached with more to read, and each page moves.
func TestASparseGrepFillsBackUntilItHasAPage(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	var log []string
	for i := range 200 {
		msg := "line " + strconv.Itoa(i)
		if i%50 == 0 {
			msg = "match " + strconv.Itoa(i)
		}
		log = append(log, stamped(time.Duration(i)*time.Second, msg))
	}
	s.add("prod/web-1/app", log...)
	svc, f := serviceOver(t, webMirror(t), s)
	view := web1View
	view.Grep = "^match"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"match 100", "match 150"}, messages(page.Records))
	assert.True(t, page.Before.More)
	assert.Equal(t, []string{"match 0", "match 50", "match 100", "match 150"}, recordsOf(pages(t, svc, view, FetchTail, 2), FetchTail))

	s = newLogServer()
	s.add("prod/web-1/app", lineEvery(1000)...)
	svc, f = serviceOver(t, webMirror(t), s)
	f.chunk(64)
	svc.budget = 4 << 10
	view.Grep = "never"
	page, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Limit: 2})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.True(t, page.Before.More)
	next, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Cursor: page.Before.Cursor, Limit: 2})
	require.NoError(t, err)
	assert.True(t, next.Before.More)
	assert.NotEqual(t, page.Before.Cursor, next.Before.Cursor, "reading on moves")
}

// A fill over a stream the file holds in pieces counts each span's lines as it goes,
// and stops filling once the stream holds a page past the cursor: the first gap is
// read, the second is not.
func TestAFillCountsEverySpanBetweenItsGaps(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"),
		logstore.Fill{Lines: fileLines(0, time.Second), Head: true},
		logstore.Fill{Lines: fileLines(4*time.Second, 5*time.Second)},
		logstore.Fill{Lines: fileLines(8*time.Second, 9*time.Second)})

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 3})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "2"}, messages(page.Records))
	require.Len(t, s.requests(), 1, "the first gap alone")
	assert.Equal(t, "2026-10-08T10:00:01Z", s.requests()[0].Get("sinceTime"))
}

// outsideReading is a backend that reads through next and notes whether any read
// started while a reading of f's mirror ran.
type outsideReading struct {
	next   Backend
	f      *fakeService
	inside atomic.Bool
}

func (b *outsideReading) note() { b.inside.Store(b.inside.Load() || b.f.reading.Load() > 0) }

func (b *outsideReading) Forward(ctx context.Context, st stream, o readOpts) *streamRead {
	b.note()
	return b.next.Forward(ctx, st, o)
}

func (b *outsideReading) Backward(ctx context.Context, st stream, o readOpts) *streamRead {
	b.note()
	return b.next.Backward(ctx, st, o)
}

func (b *outsideReading) Tail(ctx context.Context, st stream, o readOpts) *streamRead {
	b.note()
	return b.next.Tail(ctx, st, o)
}

// The fills run outside the mirror's reading, which holds one of the mirror's
// connections: a page reads the mirror once to resolve and once to check what it
// read, however many gaps it filled.
func TestTheFillRunsOutsideTheMirrorReading(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"),
		logstore.Fill{Lines: fileLines(0), Head: true},
		logstore.Fill{Lines: fileLines(4 * time.Second)},
		logstore.Fill{Lines: fileLines(8 * time.Second)})
	backend := &outsideReading{f: f}
	svc.backends = []namedBackend{{"kubelet", func(get getter) Backend {
		backend.next = newKubelet(get)
		return backend
	}}}
	before := f.readings.Load()

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, messages(linesOf(lineEvery(10))), messages(page.Records))
	assert.Len(t, s.requests(), 3, "three gaps")
	assert.False(t, backend.inside.Load())
	assert.Equal(t, int32(2), f.readings.Load()-before, "the resolve and one check")
}

// A restart that lands between the resolve and the read files nothing under the
// instance the resolve named, whose log the kubelet no longer serves: the page names
// the stream as skipped, and the next page resolves and reads the new instance.
func TestARestartBetweenTheResolveAndTheReadFilesNothing(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "old"))
	svc, f := serviceOver(t, webMirror(t), s)
	s.onRequest = func(n int) {
		if n == 0 {
			restartWeb1(t, f, s, stamped(time.Minute, "new"))
		}
	}

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Equal(t, []string{"web-1/app: restarted while reading"}, page.Skipped)
	assert.Empty(t, chunksOf(t, svc, f, "web-1", "app"))

	page, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"new"}, messages(page.Records))
	assert.Equal(t, containerID("uid-web-1", "app", 1), page.Records[0].ContainerID)
	assert.Empty(t, page.Skipped)
}

// A finished instance read to its end has its chunk marked at_end, and no page reads
// it forward again: its live end is that chunk's last line with no read at all.
func TestAFinishedInstanceReadToItsEndIsNotReadAgain(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	f := newMirror(t, append([]map[string]any{finished(pod("web-1", "uid-web-1", "node-a", nil, container{name: "app"}))}, webNodes()...)...)
	svc, _ := serviceOver(t, f, s)

	first, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	chunks := chunksOf(t, svc, f, "web-1", "app")
	require.Len(t, chunks, 1)
	assert.True(t, chunks[0].AtEnd)
	reads := len(s.requests())

	for _, q := range []FetchQuery{{Mode: FetchHead, Limit: 10}, {Mode: FetchTail, Limit: 10}, {Mode: FetchHead, Cursor: first.After.Cursor, Limit: 10}} {
		page, err := svc.Fetch(t.Context(), "7", web1View, q)
		require.NoError(t, err)
		assert.Equal(t, lineCursor("web-1", "app", 2*time.Second, 0), page.After.Cursor, q.Mode)
	}
	assert.Len(t, s.requests(), reads)
}

// A stream whose fill spent its budget before reading a line refuses the page, since
// any cut would pass its lines.
func TestAFillSpentBeforeALineRefusesThePage(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)
	f.chunk(4)
	svc.budget = 8

	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})

	assert.EqualError(t, err, "this page ran out of budget before web-1/app was read past its start; narrow the view or its grep")
}

// The walks have a budget of their own: a fill that spends all of its leaves the
// page what the file held and what the fill read before it ran out.
func TestAFillThatSpendsItsBudgetLeavesTheWalkItsOwn(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(100)...)
	svc, f := serviceOver(t, webMirror(t), s)
	var cached []time.Duration
	for i := range 50 {
		cached = append(cached, time.Duration(i)*time.Second)
	}
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(cached...), Head: true})
	f.chunk(64)
	svc.budget = 512

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 100})

	require.NoError(t, err)
	assert.Greater(t, len(page.Records), 50)
	assert.Less(t, len(page.Records), 100)
	assert.True(t, page.After.More)
}

// A line a grepped page's fill found and a plain page's found again is stored once:
// the kubelet's fills read every line whatever the grep, and a line's position is
// its key.
func TestALineFoundByAGreppedAndAPlainPageIsStoredOnce(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)
	odd := web1View
	odd.Grep = "[13579]$"

	page, err := svc.Fetch(t.Context(), "7", odd, FetchQuery{Mode: FetchHead, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "3"}, messages(page.Records))
	page, err = svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, messages(linesOf(lineEvery(10))), messages(page.Records))

	var n int
	file := fileOf(t, svc, f)
	rows, err := file.PodStreams(t.Context(), "prod", "web-1", "")
	require.NoError(t, err)
	require.NoError(t, file.Read(t.Context(), func(r logstore.Reader) error {
		return r.Lines(rows[0].ID, logstore.Oldest, logstore.Newest, func(logstore.Line) bool {
			n++
			return true
		})
	}))
	assert.Equal(t, 10, n)
}

// With two backends in order, the second is asked only for what the first answered
// head for, older than the moment of the first's oldest line, which is the first's
// whole: the lines they both hold go out once. A Tail page answers nothing older
// only once the second says head too.
func TestTheSecondBackendIsAskedOnlyPastTheFirstsHead(t *testing.T) {
	t.Parallel()
	kubelet := newLogServer()
	kubelet.add("prod/web-1/app", lineEvery(10)[5:]...)
	archive := newLogServer()
	archive.add("prod/web-1/app", lineEvery(8)...)
	svc, _ := serviceOver(t, webMirror(t), kubelet)
	svc.backends = append(svc.backends, namedBackend{"archive", func(getter) Backend { return newKubelet(archive.get) }})

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, messages(linesOf(lineEvery(10))), messages(page.Records))
	assert.Equal(t, PageEdge{}, page.Before)
	for _, q := range archive.requests() {
		assert.NotContains(t, q.Get("sinceTime"), "10:00:05", "nothing from the kubelet's moments")
	}

	kubelet = newLogServer()
	kubelet.add("prod/web-1/app", lineEvery(10)[5:]...)
	archive.setRefusal("prod/web-1/app", &refusal{503, "archive down"})
	svc, _ = serviceOver(t, webMirror(t), kubelet)
	svc.backends = append(svc.backends, namedBackend{"archive", func(getter) Backend { return newKubelet(archive.get) }})
	page, err = svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, []string{"5", "6", "7", "8", "9"}, messages(page.Records))
	assert.Equal(t, PageEdge{Cursor: lineCursor("web-1", "app", 5*time.Second, 0), More: true}, page.Before)
}

// A fill the page cut short, having read enough before it reached the chunk past its
// gap, leaves that seam a live gap: the next page fills the rest of it, and no line
// is lost.
func TestAFillCutShortLeavesItsSeamALiveGap(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)
	var later []time.Duration
	for i := 5; i < 10; i++ {
		later = append(later, time.Duration(i)*time.Second)
	}
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(later...)})

	assert.Equal(t, messages(linesOf(lineEvery(10))), recordsOf(pages(t, svc, web1View, FetchHead, 2), FetchHead))
}

// A gap the first backend has rotated away is offered to the next before anything
// links across it: with the file holding 0 and 1, the kubelet 5 to 9 and an archive
// every line, a Head page reads 2 to 4 from the archive.
func TestARotatedGapGoesToTheNextBackend(t *testing.T) {
	t.Parallel()
	kubelet := newLogServer()
	kubelet.add("prod/web-1/app", lineEvery(10)[5:]...)
	archive := newLogServer()
	archive.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), kubelet)
	svc.backends = append(svc.backends, namedBackend{"archive", func(getter) Backend { return newKubelet(archive.get) }})
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second)})

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, messages(linesOf(lineEvery(10))), messages(page.Records))
}

// A Tail read the page cut before it reached the chunk below its gap offers nothing
// to the next backend: it stopped because it had enough, not because the first
// backend lacks what lies under it.
func TestATailReadCutByThePageOffersNothingToTheNextBackend(t *testing.T) {
	t.Parallel()
	kubelet := newLogServer()
	kubelet.add("prod/web-1/app", lineEvery(10)...)
	archive := newLogServer()
	archive.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), kubelet)
	svc.backends = append(svc.backends, namedBackend{"archive", func(getter) Backend { return newKubelet(archive.get) }})
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second)})

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Limit: 3})

	require.NoError(t, err)
	assert.Equal(t, []string{"7", "8", "9"}, messages(page.Records))
	assert.Empty(t, archive.requests())
}

// A fill counts only the lines the page's chunks hold, as the walk keeps only those:
// a line another pattern's chunk filed between two chunks of one span is not the
// page's, so the fill reads on past the span for the page's third line.
func TestAFillCountsOnlyTheLinesItsChunksHold(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i, msg := range []string{"x0", "xy1", "xy2", "xy3", "y4", "x5", "x6", "x7", "x8"} {
		s.add("prod/web-1/app", stamped(time.Duration(i)*time.Second, msg))
	}
	svc, f := serviceOver(t, webMirror(t), s)
	lineAt := func(d time.Duration, msg string) logstore.Line {
		return logstore.Line{Pos: logstore.Pos{TS: at(d).UnixNano()}, Message: msg}
	}
	first := lineAt(0, "x0")
	seed(t, svc, f, rowOf("web-1", "app"),
		logstore.Fill{Pattern: "x", Lines: []logstore.Line{first}, Head: true},
		logstore.Fill{Pattern: "y", Lines: []logstore.Line{lineAt(time.Second, "xy1"), lineAt(2*time.Second, "xy2"), lineAt(3*time.Second, "xy3")}},
		logstore.Fill{Pattern: "x", Lines: []logstore.Line{lineAt(5*time.Second, "x5")}, Prev: &first.Pos})
	view := web1View
	view.Grep = "x"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 3})

	require.NoError(t, err)
	assert.Equal(t, []string{"x0", "x5", "x6"}, messages(page.Records))
}

// An Around page's halves join at its moment: the Head half's read continues the
// chunk the Tail half's ends, so the file holds one span and a page across the
// moment asks the kubelet nothing.
func TestAnAroundPagesHalvesJoin(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)

	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchAround, At: new(at(5 * time.Second)), Limit: 2})
	require.NoError(t, err)
	assert.Len(t, logstore.CoverageOf(chunksOf(t, svc, f, "web-1", "app"), []string{"kubelet"}).Spans, 1)

	reads := len(s.requests())
	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Cursor: lineCursor("web-1", "app", 3*time.Second, 0), Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"4", "5"}, messages(page.Records))
	assert.Len(t, s.requests(), reads)
}

// A page whose check of the instances it read fails is the failure, and writes
// nothing.
func TestAFailedInstanceCheckFailsThePage(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"))
	svc, f := serviceOver(t, webMirror(t), s)
	f.queryErr, f.queryErrOn = errors.New("db closed"), "SELECT p.uid, coalesce(c.container_id"

	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})

	assert.EqualError(t, err, "db closed")
	assert.Empty(t, chunksOf(t, svc, f, "web-1", "app"))
}
