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
	"database/sql"
	"errors"
	"math/rand"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// A deployment's last lines come back in time order, with the cursor of the
// oldest.
func TestFetchAnswersADeploymentsLastLines(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for i := range 15 {
		pod := "web-" + strconv.Itoa(1+i%2)
		s.add("prod/"+pod+"/app", stamped(time.Duration(i)*time.Second, strconv.Itoa(i)))
	}
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 5})

	require.NoError(t, err)
	assert.Equal(t, []string{"10", "11", "12", "13", "14"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 10*time.Second, 0), page.Before.Cursor)
	assert.Equal(t, "web-1", page.Records[0].Stream.Pod)
	assert.Equal(t, "eu-1a", page.Records[0].Place.Zone)
}

// A head page after a cursor starts past it, and grep keeps the matching lines.
func TestFetchReadsForwardAfterACursor(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "error a"), stamped(time.Second, "ok"), stamped(2*time.Second, "error b"), stamped(3*time.Second, "error c"))
	svc, _ := serviceOver(t, webMirror(t), s)
	view := webView
	view.Grep = "^error"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Cursor: cursor(0), Limit: 1})

	require.NoError(t, err)
	assert.Equal(t, []string{"error b"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 2*time.Second, 0), page.After.Cursor)
}

// A tail page before a cursor reaches the line before it however much log follows,
// and a page that fills reads no further: limit 1 reads one window.
func TestFetchPagesBackBeforeACursor(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(1000)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Cursor: cursor(100 * time.Second), Limit: 1})

	require.NoError(t, err)
	assert.Equal(t, []string{"99"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 99*time.Second, 0), page.Before.Cursor)

	before := len(s.requests())
	page, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 1})

	require.NoError(t, err)
	assert.Equal(t, []string{"999"}, messages(page.Records))
	var windows []string
	for _, q := range s.requests()[before:] {
		if q.Has("tailLines") {
			windows = append(windows, q.Get("tailLines"))
		}
	}
	assert.Equal(t, []string{"10"}, windows)
}

// Two containers writing at one moment are paged through one line at a time, each
// cursor naming the line the page ended at.
func TestFetchPagesThroughAMoment(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"), stamped(0, "a'"), stamped(time.Second, "c"))
	s.add("prod/web-2/app", stamped(0, "b"))
	svc, _ := serviceOver(t, webMirror(t), s)

	var got []string
	q := FetchQuery{Mode: FetchHead, Limit: 1}
	for range 4 {
		page, err := svc.Fetch(t.Context(), "7", webView, q)
		require.NoError(t, err)
		got = append(got, messages(page.Records)...)
		if !page.After.More {
			break
		}
		q.Cursor = page.After.Cursor
	}
	assert.Equal(t, []string{"a", "a'", "b", "c"}, got)
}

// Pages join: walked from either end through random logs with tied and sub-second
// moments, under small limits and budgets and a grep, the pages hold every line
// in range once, in order, each within its limit, whether the walk starts at an end
// or at a line's cursor. Some streams lost moments to a rotation the file never saw,
// and each hole between two lines the file holds rides on exactly one record, the
// first past it the grep passes, with the hole's own two times. A page the budget refuses is the honest
// answer to a view too wide for it, so the walk is asked again with twice the
// budget, as a user would narrow the view. Every page resolves the view through
// SQL the store parses afresh, so under the race detector the full sweep takes
// minutes; -short walks a prefix of the seeds, and the stress run walks them all.
func TestPagesJoinWithoutLossOrRepeat(t *testing.T) {
	t.Parallel()
	seeds := 150
	if testing.Short() {
		seeds = 25
	}
	for seed := range seeds {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(int64(seed)))
			s, objects, all := randomLogs(rng)
			kept, runs := rotateAway(rng, s, all)
			// Windows written in batches of a few lines or bytes, so batches join too.
			ret := testRetention
			ret.WriteBatch, ret.WriteBatchBytes = 1+rng.Intn(4), int64(1+rng.Intn(64))
			svc, f := serviceIn(t, newMirror(t, objects...), s, t.TempDir(), ret)
			for key, held := range runs {
				var fills []logstore.Fill
				for _, run := range held {
					var lines []logstore.Line
					for _, rec := range run {
						lines = append(lines, lineOf(rec))
					}
					fills = append(fills, logstore.Fill{Lines: lines})
				}
				seedStamped(t, svc, f, rowOf(key.Pod, key.Container), true, fills...)
			}
			// Every page resolves the view again; the mirror holds still, and SQLite
			// parsing the same statements is most of what the walks cost.
			f.answers = map[string]cluster.ClusterCachedDataQueryResult{}
			svc.budget = int64(2000 + rng.Intn(8000))
			f.chunk(1 + rng.Intn(64))
			view := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", AllContainers: true}}}
			view.Grep = []string{"", "odd", "#.*7"}[rng.Intn(3)]
			grep := regexp.MustCompile(view.Grep)
			var want []Record
			for _, rec := range kept {
				if grep.MatchString(rec.Message) {
					want = append(want, rec)
				}
			}
			want = carried(want, all, kept, grep)
			limit := 1 + rng.Intn(25)

			assert.Equal(t, marks(want), marks(walk(t, svc, view, FetchHead, limit, nil)), "head")
			assert.Equal(t, marks(want), marks(walk(t, svc, view, FetchTail, limit, nil)), "tail")
			if len(want) == 0 {
				return
			}
			i := rng.Intn(len(want))
			c := cursorOf(positionOf(want[i]))
			assert.Equal(t, marks(want[i+1:]), marks(walk(t, svc, view, FetchHead, limit, c)), "after")
			assert.Equal(t, marks(want[:i]), marks(walk(t, svc, view, FetchTail, limit, c)), "before")
		})
	}
}

// randomLogs is a deployment web of one to three pods, each with an app and an
// istio-proxy container, whose logs hold up to eighty lines each at moments that
// often tie within and across containers, some inside a second, some opening
// with a burst, some quiet for a day part way, every other line saying odd, and
// every line in the order a view of web reads them.
func randomLogs(rng *rand.Rand) (*logServer, []map[string]any, []Record) {
	s := newLogServer()
	objects := []map[string]any{
		workload("apps/v1", "Deployment", "web", "dep-web", nil),
		workload("apps/v1", "ReplicaSet", "web-7d9f", "rs-web", controller("apps/v1", "Deployment", "web", "dep-web")),
		node("node-a", "eu", "eu-1a", "linux", "amd64"),
	}
	place := Place{Node: "node-a", Region: "eu", Zone: "eu-1a", OS: "linux", Arch: "amd64"}
	var all []Record
	for i := 1; i <= 1+rng.Intn(3); i++ {
		name := "web-" + strconv.Itoa(i)
		key := "prod/" + name
		objects = append(objects, pod(name, "uid-"+name, "node-a", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"}))
		for _, container := range []string{"app", "istio-proxy"} {
			stream := StreamKey{Namespace: "prod", Pod: name, Container: container}
			moments := make([]time.Duration, rng.Intn(81))
			for i := range moments {
				moments[i] = time.Duration(rng.Intn(12))*time.Second + []time.Duration{0, 0, 0, 250 * time.Millisecond, 500 * time.Millisecond}[rng.Intn(5)]
			}
			slices.Sort(moments)
			// A log that opens with a burst, more lines at its first moment than a
			// window holds.
			if len(moments) > 0 && rng.Intn(2) == 0 {
				for i := range min(len(moments), 1+rng.Intn(30)) {
					moments[i] = moments[0]
				}
			}
			// A log quiet for a day between its older lines and its newer, which a
			// read back across widens past.
			if len(moments) > 1 && rng.Intn(3) == 0 {
				for i := range 1 + rng.Intn(len(moments)-1) {
					moments[i] -= 24 * time.Hour
				}
			}
			ordinal := 0
			for i, d := range moments {
				if i > 0 && moments[i-1] == d {
					ordinal++
				} else {
					ordinal = 0
				}
				parity := "even"
				if i%2 == 1 {
					parity = "odd"
				}
				msg := name + "/" + container + "#" + strconv.Itoa(i) + " " + parity
				s.add(key+"/"+container, stamped(d, msg))
				all = append(all, Record{Timestamp: at(d), Message: msg, Stream: stream, Place: place, ContainerID: containerID("uid-"+name, container, 0), Ordinal: ordinal})
			}
		}
	}
	slices.SortStableFunc(all, compareLines)
	return s, objects, all
}

// rotateAway drops, from about half of all's streams with three moments or more, the
// lines at random moments, the newest never, as rotations the file never saw. Each
// run of moments left but the newest is the file's alone, and the kubelet serves only
// the newest run. It answers the lines left, in all's order, and each such stream's
// runs the file holds.
func rotateAway(rng *rand.Rand, s *logServer, all []Record) ([]Record, map[StreamKey][][]Record) {
	byStream := map[StreamKey][]Record{}
	var keys []StreamKey
	for _, rec := range all {
		if _, ok := byStream[rec.Stream]; !ok {
			keys = append(keys, rec.Stream)
		}
		byStream[rec.Stream] = append(byStream[rec.Stream], rec)
	}
	dropped := map[StreamKey]map[time.Time]bool{}
	runs := map[StreamKey][][]Record{}
	for _, key := range keys {
		lines := byStream[key]
		var moments []time.Time
		for _, rec := range lines {
			if len(moments) == 0 || !moments[len(moments)-1].Equal(rec.Timestamp) {
				moments = append(moments, rec.Timestamp)
			}
		}
		if len(moments) < 3 || rng.Intn(2) == 0 {
			continue
		}
		dropped[key] = map[time.Time]bool{}
		for _, m := range moments[:len(moments)-1] {
			if rng.Intn(4) == 0 {
				dropped[key][m] = true
			}
		}
		var cur []Record
		var held [][]Record
		for _, rec := range lines {
			if dropped[key][rec.Timestamp] {
				if cur != nil {
					held, cur = append(held, cur), nil
				}
				continue
			}
			cur = append(cur, rec)
		}
		// The newest run is the kubelet's.
		var served []string
		for _, rec := range cur {
			served = append(served, stamped(rec.Timestamp.Sub(at(0)), rec.Message))
		}
		s.mu.Lock()
		s.logs[key.Namespace+"/"+key.Pod+"/"+key.Container] = served
		s.mu.Unlock()
		if len(held) > 0 {
			runs[key] = held
		}
	}
	kept := slices.DeleteFunc(slices.Clone(all), func(rec Record) bool { return dropped[rec.Stream][rec.Timestamp] })
	return kept, runs
}

// carried is want, the lines of kept the grep passes, each carrying the holes between
// it and the line of its stream before it that the grep passes: wherever all, the
// lines before the rotations, held lines between two neighbours in kept.
func carried(want, all, kept []Record, grep *regexp.Regexp) []Record {
	left := map[string]bool{}
	for _, rec := range kept {
		left[rec.Message] = true
	}
	last := map[StreamKey]*Record{}
	hole := map[StreamKey]bool{}
	pending := map[StreamKey]*Gap{}
	gaps := map[string]*Gap{}
	for _, rec := range all {
		if !left[rec.Message] {
			hole[rec.Stream] = last[rec.Stream] != nil
			continue
		}
		if hole[rec.Stream] {
			pending[rec.Stream] = joined(pending[rec.Stream], &Gap{After: last[rec.Stream].Timestamp, Before: rec.Timestamp})
			hole[rec.Stream] = false
		}
		last[rec.Stream] = &rec
		if grep.MatchString(rec.Message) {
			gaps[rec.Message], pending[rec.Stream] = pending[rec.Stream], nil
		}
	}
	out := slices.Clone(want)
	for i := range out {
		out[i].Gap = gaps[out[i].Message]
	}
	return out
}

// walk pages through view in mode from the end mode starts at, or from the cursor
// start, and is every record in the order read, each page within its limit. A page
// refused for budget starts the walk again with twice the budget.
func walk(t *testing.T, svc *Service, view View, mode FetchMode, limit int, start *string) []Record {
	t.Helper()
	q := FetchQuery{Mode: mode, Cursor: start, Limit: limit}
	out := []Record{}
	for range 1000 {
		page, err := svc.Fetch(t.Context(), "7", view, q)
		if err != nil && strings.Contains(err.Error(), "ran out of budget") {
			svc.budget *= 2
			return walk(t, svc, view, mode, limit, start)
		}
		require.NoError(t, err)
		require.LessOrEqual(t, len(page.Records), limit)
		if mode == FetchHead {
			out = append(out, page.Records...)
		} else {
			out = append(slices.Clone(page.Records), out...)
		}
		edge := page.After
		if mode == FetchTail {
			edge = page.Before
		}
		if !edge.More {
			return out
		}
		q = FetchQuery{Mode: mode, Cursor: edge.Cursor, Limit: limit}
	}
	t.Fatal("the pages never end")
	return nil
}

// The edges follow the log viewer README's table: a Head page from the start has
// nothing before it, one from a cursor has that cursor behind it, and each ends at
// the cut while it was cut and at the live end once it was not; a Tail page is the
// mirror image.
func TestTheEdgesFollowTheTable(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"), stamped(time.Second, "b"), stamped(2*time.Second, "c"))
	svc, _ := serviceOver(t, webMirror(t), s)
	fetch := func(q FetchQuery) Page {
		t.Helper()
		page, err := svc.Fetch(t.Context(), "7", webView, q)
		require.NoError(t, err)
		return page
	}
	b := lineCursor("web-1", "app", time.Second, 0)
	liveEnd := lineCursor("web-1", "app", 2*time.Second, 0)

	page := fetch(FetchQuery{Mode: FetchHead, Limit: 2})
	assert.Equal(t, []string{"a", "b"}, messages(page.Records))
	assert.Equal(t, PageEdge{}, page.Before)
	assert.Equal(t, PageEdge{Cursor: b, More: true}, page.After)
	assert.Nil(t, page.Anchor)

	page = fetch(FetchQuery{Mode: FetchHead, Cursor: b, Limit: 2})
	assert.Equal(t, []string{"c"}, messages(page.Records))
	assert.Equal(t, PageEdge{Cursor: b, More: true}, page.Before)
	assert.Equal(t, PageEdge{Cursor: liveEnd}, page.After)

	page = fetch(FetchQuery{Mode: FetchTail, Limit: 2})
	assert.Equal(t, []string{"b", "c"}, messages(page.Records))
	assert.Equal(t, PageEdge{Cursor: b, More: true}, page.Before)
	assert.Equal(t, PageEdge{Cursor: liveEnd}, page.After)

	page = fetch(FetchQuery{Mode: FetchTail, Cursor: b, Limit: 2})
	assert.Equal(t, []string{"a"}, messages(page.Records))
	assert.Equal(t, PageEdge{}, page.Before)
	assert.Equal(t, PageEdge{Cursor: b, More: true}, page.After)
}

// An Around page is its two halves: limit lines before at, then limit from it on,
// its anchor the first record at or after at, its before edge the Tail half's and
// its after edge the Head half's.
func TestAroundIsItsTwoHalves(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(6)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	mid := at(3 * time.Second)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchAround, At: &mid, Limit: 2})

	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3", "4"}, messages(page.Records))
	require.NotNil(t, page.Anchor)
	assert.Equal(t, 2, *page.Anchor)
	assert.Equal(t, PageEdge{Cursor: lineCursor("web-1", "app", time.Second, 0), More: true}, page.Before)
	assert.Equal(t, PageEdge{Cursor: lineCursor("web-1", "app", 4*time.Second, 0), More: true}, page.After)
}

// A cursor is only ever text a page handed back and at only the Around anchor's, so
// Around needs at and no cursor, and Head and Tail take no at.
func TestABadQueryIsRefused(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	now := at(0)
	for want, q := range map[string]FetchQuery{
		"an Around page needs at":                      {Mode: FetchAround},
		"an Around page takes no cursor":               {Mode: FetchAround, At: &now, Cursor: cursor(0)},
		"a Head page takes no at":                      {Mode: FetchHead, At: &now},
		"a Tail page takes no at":                      {Mode: FetchTail, At: &now},
		`Kstack cannot read a page of mode "sideways"`: {Mode: "sideways"},
	} {
		q.Limit = 10
		_, err := svc.Fetch(t.Context(), "7", webView, q)
		assert.EqualError(t, err, want)
	}
}

// web1View reads the app container of the pod web-1 in prod.
var web1View = View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}}}}

// fileOf is the log file of f's active cache, for a test to read.
func fileOf(t *testing.T, svc *Service, f *fakeService) *logstore.Store {
	t.Helper()
	file, err := svc.files.OpenOrCreate(int64(f.active.Cache.ID))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, file.Close()) })
	return file
}

// chunksOf is the unlabelled chunks of pod/container's stream of the first instance
// of a fixture pod, in the file behind svc.
func chunksOf(t *testing.T, svc *Service, f *fakeService, pod, container string) []logstore.Chunk {
	t.Helper()
	file := fileOf(t, svc, f)
	rows, err := file.PodStreams(t.Context(), "prod", pod, "")
	require.NoError(t, err)
	for _, row := range rows {
		if row.Container == container && row.ContainerID == containerID("uid-"+pod, container, 0) {
			chunks, err := file.Chunks(t.Context(), row.ID, "")
			require.NoError(t, err)
			return chunks
		}
	}
	return nil
}

// A page stamps the chunks its walk read a line from, and none past where it cut: a
// chunk the page never reached stays as its fill left it.
func TestAPageStampsTheChunksItRead(t *testing.T) {
	t.Parallel()
	svc, f := serviceIn(t, webMirror(t), newLogServer(), t.TempDir(), testRetention)
	seedUnread(t, svc, f, rowOf("web-1", "app"),
		logstore.Fill{Lines: fileLines(time.Second, 2*time.Second, 3*time.Second), Head: true},
		logstore.Fill{Lines: fileLines(10*time.Second, 11*time.Second), Prev: &logstore.Pos{TS: at(3 * time.Second).UnixNano()}})

	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 2})
	require.NoError(t, err)

	chunks := chunksOf(t, svc, f, "web-1", "app")
	require.Len(t, chunks, 2)
	assert.NotZero(t, chunks[0].ReadAt, "the chunk the page read")
	assert.Zero(t, chunks[1].ReadAt, "the chunk past its cut")
}

// A page's fill lands stamped, so a sweep over budget between the fill's commit and the
// page's walk leaves what the page fetched, and the page is whole.
func TestASweepBetweenAPagesFillAndWalkLeavesThePage(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(time.Second, "1"), stamped(2*time.Second, "2"))
	ret := testRetention
	ret.Bytes = 0
	svc, f := serviceIn(t, webMirror(t), s, t.TempDir(), ret)
	svc.afterFill = func() { sweepFile(t, svc, f) }

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, messages(page.Records))
}

// A page whose stream row went between its lookup and its write reads that stream as
// having nothing: Write skips its fill rather than failing every other stream's.
func TestAPageWhoseRowWentReadsNothingOfIt(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	release := make(chan struct{})
	backend.set("web-1/app", fakeRead{lines: []Record{{Timestamp: at(time.Second), Message: "1"}}, wait: release})
	// Both reads wait, since a stream writes once its read ends, and the delete
	// below takes the file's one writer.
	backend.set("web-2/app", fakeRead{lines: []Record{{Timestamp: at(2 * time.Second), Message: "2"}}, wait: release})
	dir := t.TempDir()
	svc, _ := serviceIn(t, webMirror(t), newLogServer(), dir, testRetention)
	svc.backends = []namedBackend{{"kubelet", func(getter) Backend { return backend }}}
	view := View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}},
		{Namespace: "prod", Kind: SourcePod, Name: "web-2", Containers: []string{"app"}},
	}}
	done := make(chan error, 1)
	var page Page
	go func() {
		var err error
		page, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
		done <- err
	}()
	backend.opened.Await(t, "a stream's read")
	backend.opened.Await(t, "the other's")
	db, err := sql.Open("sqlite", filepath.Join(dir, "11.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	_, err = db.Exec(`DELETE FROM streams WHERE pod = 'web-1'`)
	require.NoError(t, err)

	close(release)
	require.NoError(t, testutil.Recv(t, done, "the page"))
	assert.Equal(t, []string{"2"}, messages(page.Records))
}

// A page whose stamp the file refuses still answers: a stamp only keeps the chunks out
// of eviction, and a chunk evicted is filled again.
func TestAPageWhoseStampFailsStillAnswers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc, f := serviceIn(t, webMirror(t), newLogServer(), dir, testRetention)
	seedUnread(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(time.Second, 2*time.Second), Head: true})
	db, err := sql.Open("sqlite", filepath.Join(dir, "11.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TRIGGER refuse_stamp BEFORE UPDATE OF read_at_ms ON chunks BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 1})

	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, messages(page.Records))
}

// everyPodGone has the sweeps of svc's log files read every pod as gone.
func everyPodGone(svc *Service) {
	svc.files.SetPodsGone(func(_ context.Context, _ int64, uids []string) ([]string, error) { return uids, nil })
}

// rowsOf is the file rows of pod in the file behind svc.
func rowsOf(t *testing.T, svc *Service, f *fakeService, pod string) []logstore.Stream {
	t.Helper()
	rows, err := fileOf(t, svc, f).PodStreams(t.Context(), "prod", pod, "")
	require.NoError(t, err)
	return rows
}

// sweepFile runs one sweep of the file behind svc.
func sweepFile(t *testing.T, svc *Service, f *fakeService) {
	t.Helper()
	require.NoError(t, fileOf(t, svc, f).Sweep(t.Context()))
}

// A page holds the rows it files until it has answered: a sweep while its fill reads
// leaves a row with nothing in it yet, whatever the mirror says of its pod.
func TestAPageHoldsItsRows(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	release := make(chan struct{})
	backend.set("web-1/app", fakeRead{lines: []Record{{Timestamp: at(time.Second), Message: "1"}}, wait: release})
	svc, f := serviceOn(t, backend)
	everyPodGone(svc)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
		done <- err
	}()
	backend.opened.Await(t, "the page's read")

	sweepFile(t, svc, f)
	assert.Len(t, rowsOf(t, svc, f, "web-1"), 1, "held while the page reads")

	close(release)
	require.NoError(t, testutil.Recv(t, done, "the page"))
}

// A stream whose fill failed before any line is the page's skipped entry, and its
// walk still runs: the lines the file holds of it up to its first live gap answer,
// and its stop at that gap does not move the cut.
func TestASkippedStreamAnswersFromTheFile(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-2/app", stamped(500*time.Millisecond, "b0"), stamped(5*time.Second, "b5"))
	s.setRefusal("prod/web-1/app", &refusal{404, `pods "web-1" not found`})
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second), Head: true})

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "b0", "1", "b5"}, messages(page.Records))
	assert.Equal(t, []string{`web-1/app: pods "web-1" not found`}, page.Skipped)
	assert.False(t, page.After.More)
}

// A skipped stream whose file span is denser than the page stops at limit plus one
// lines, which hold the cut as any stream's do, so every line goes out once. Only a
// page that reaches the stream's gap reads the kubelet, and is refused there.
func TestADenseSkippedStreamHoldsTheCut(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	var want []string
	var dense []time.Duration
	for i := range 20 {
		dense = append(dense, time.Duration(i)*time.Second)
		want = append(want, strconv.Itoa(i))
	}
	for i := range 5 {
		s.add("prod/web-2/app", stamped(time.Duration(4*i)*time.Second+500*time.Millisecond, "b"+strconv.Itoa(4*i)))
		want = slices.Insert(want, slices.Index(want, strconv.Itoa(4*i))+1, "b"+strconv.Itoa(4*i))
	}
	s.setRefusal("prod/web-1/app", &refusal{404, `pods "web-1" not found`})
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(dense...), Head: true})

	got := pages(t, svc, webView, FetchHead, 3)

	assert.Equal(t, want, recordsOf(got, FetchHead))
	assert.Empty(t, got[0].Skipped, "the file holds the first page")
	assert.Equal(t, []string{`web-1/app: pods "web-1" not found`}, got[len(got)-1].Skipped)
}

// A grep matches as Go's regexp does, over the text the file stored: the message
// after stripControl, flags and all.
func TestAGrepMatchesTheStoredText(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "\x1b[31mERROR\x1b[0m disk"), stamped(time.Second, "error net"), stamped(2*time.Second, "ok"))
	svc, _ := serviceOver(t, webMirror(t), s)
	view := web1View
	view.Grep = `(?i)^error\b`

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"ERROR disk", "error net"}, messages(page.Records))
}

// A Tail page that needs fewer lines than the log's first moment holds leaves the
// rest of that moment to the next page: the read reached the backend's oldest
// moment, but stopped inside it, so nothing older is lost.
func TestATailPageInsideTheFirstMomentLosesNothing(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	for _, msg := range []string{"a", "b", "c", "d", "e"} {
		s.add("prod/web-1/app", stamped(0, msg))
	}
	svc, _ := serviceOver(t, webMirror(t), s)

	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, recordsOf(pages(t, svc, web1View, FetchTail, 1), FetchTail))
}

// A view of one container of a pod holding more containers than Kstack reads at
// once reads it, as the LogsView tool's check passes it, and so does one whose filter
// leaves the pod out: the limit is on what the view selects, never on the pod.
func TestOneContainerOfALargePodIsRead(t *testing.T) {
	t.Parallel()
	containers := make([]container, maxStreams+5)
	for i := range containers {
		containers[i] = container{name: "c" + strconv.Itoa(i), restarted: i == 7}
	}
	s := newLogServer()
	s.add("prod/big/c7", stamped(0, "seven"))
	svc, _ := serviceOver(t, newMirror(t, append([]map[string]any{pod("big", "uid-big", "node-a", nil, containers...)}, webNodes()...)...), s)

	for _, view := range []View{podSource("big", false, "c7"), podSource("big", false)} {
		page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
		require.NoError(t, err)
		want := map[bool][]string{true: {"seven"}, false: {}}[len(view.Sources[0].Containers) > 0]
		assert.Equal(t, want, messages(page.Records))
	}
	all := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "big", AllContainers: true}}}
	_, err := svc.Fetch(t.Context(), "7", all, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrTooMany)

	previous := all
	previous.Sources = []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "big", AllContainers: true, Previous: true}}
	_, err = svc.Fetch(t.Context(), "7", previous, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err, "the one previous instance")

	elsewhere := all
	elsewhere.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1b"}}}
	page, err := svc.Fetch(t.Context(), "7", elsewhere, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err, "a filter that leaves the pod out")
	assert.Empty(t, page.Records)
}

// A Tail page whose only lines left to read are a skipped stream's refuses to go on
// rather than answering an empty page at the cursor it was given, which would hand
// back the same page forever.
func TestATailPageThatCannotAdvanceIsRefused(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-2/app", stamped(0, "b0"))
	s.setRefusal("prod/web-1/app", &refusal{503, "unavailable"})
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 5})
	require.NoError(t, err)
	assert.Equal(t, []string{"b0"}, messages(page.Records))
	require.True(t, page.Before.More, "web-1 is still to read")

	_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Cursor: page.Before.Cursor, Limit: 5})
	assert.EqualError(t, err, "Kstack could not read older lines of this view: web-1/app: unavailable")
}

// A limit past 500, or under 1, is refused before anything is read.
func TestFetchRefusesABadLimit(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	for _, limit := range []int{0, 501} {
		_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: limit})
		var r Refusal
		assert.True(t, errors.As(err, &r), limit)
	}
}

// A cursor that names no moment is refused, whichever way the page reads, and so
// is a grep that is no regular expression.
func TestFetchRefusesABadCursor(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	bad := "yesterday"
	for _, mode := range []FetchMode{FetchHead, FetchTail} {
		_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: mode, Cursor: &bad, Limit: 10})
		assert.EqualError(t, err, "a cursor must be an RFC 3339 time, or one a page handed back", mode)
	}

	view := webView
	view.Grep = "("
	_, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.EqualError(t, err, "the grep is not a regular expression")
}

// A grep past MaxGrep is refused by a page and by a follow alike, since the wire
// takes any view the webview sends; one at the cap is read.
func TestAGrepPastMaxGrepIsRefused(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	view := webView
	view.Grep = strings.Repeat("a", MaxGrep+1)

	_, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.EqualError(t, err, "a grep is at most 1000 bytes")
	_, err = svc.Follow(t.Context(), "7", view, nil)
	assert.EqualError(t, err, "a grep is at most 1000 bytes")

	view.Grep = strings.Repeat("a", MaxGrep)
	_, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.NoError(t, err)
}

// A fetch is refused what resolving the view or claiming the cluster refuses.
func TestFetchIsRefusedWhatItsReadsRefuse(t *testing.T) {
	t.Parallel()
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	f.active.Cache = nil
	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrNoCache)

	svc, f = serviceOver(t, webMirror(t), newLogServer())
	f.acquireErr = cluster.ErrNotConnectable
	_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrRevoked)
}

// A view no pod passes reads nothing, and answers an empty page whose after edge is
// the live end.
func TestFetchOfAViewWithNoContainerIsEmpty(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	svc, _ := serviceOver(t, webMirror(t), s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"nowhere"}}}

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, Page{Records: []Record{}, After: PageEdge{Cursor: cursor(time.Hour)}, Skipped: []string{}}, page)
	assert.Empty(t, s.requests())
}

// A view of more containers than Kstack reads at once is refused before any is
// read.
func TestAFetchOfTooManyContainersIsRefused(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	addPods(t, f, maxStreams-1)
	svc, _ := serviceOver(t, f, s)

	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 5})
	assert.ErrorIs(t, err, ErrTooMany)
	assert.Empty(t, s.requests())
}

// A fetch ends with its context. It checks the cluster once and watches nothing,
// since the connection's retirement already bounds a page.
func TestFetchEndsWithItsContextAndWatchesNothing(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := svc.Fetch(ctx, "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, context.Canceled)

	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"))
	svc, _ = serviceOver(t, webMirror(t), s)
	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, messages(page.Records))
}

// Each stream of a fetch reads under its own share of the budget, so a stream whose
// log is large cannot spend what a quiet one needs to read its start.
func TestAFetchSharesItsBudgetPerStream(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	var noise []string
	for i := range 200 {
		noise = append(noise, stamped(time.Duration(i)*time.Millisecond, strings.Repeat("x", 100)))
	}
	s.add("prod/web-1/app", noise...)
	s.add("prod/web-2/app", stamped(0, "match"))
	svc, f := serviceOver(t, webMirror(t), s)
	f.chunk(64)
	svc.budget = 8 << 10
	view := webView
	view.Grep = "match"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"match"}, messages(page.Records))
	assert.True(t, page.After.More, "web-1 stopped short, so the page goes on from there")
}

// An Around page's halves read one resolve of the view, so they read the same
// streams: one reading of the mirror to resolve, and one claim. Each window written
// checks the stream's instance with a reading of its own.
func TestAnAroundPageReadsTheViewOnce(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, f := serviceOver(t, webMirror(t), s)
	readings := f.readings.Load()

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchAround, At: new(at(5 * time.Second)), Limit: 2})

	require.NoError(t, err)
	assert.Equal(t, []string{"3", "4", "5", "6"}, messages(page.Records))
	assert.Equal(t, int32(4), f.readings.Load()-readings, "the resolve, and a check for the Head half's read, the Tail half's window and its head mark")
	assert.Equal(t, int32(1), f.lease.released.Load(), "one claim")
}

// The live end names the newest line, so a line another stream writes at its moment
// after the page, in a container that sorts after it, is past it: the next page from
// the live end reads it.
func TestTheLiveEndKeepsALaterLineAtItsMoment(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(time.Second, "a"))
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, messages(page.Records))
	require.Equal(t, PageEdge{Cursor: lineCursor("web-1", "app", time.Second, 0)}, page.After)

	s.add("prod/web-2/app", stamped(time.Second, "b"))
	page, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Cursor: page.After.Cursor, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, messages(page.Records))
}

// A Head page over a stream skipped on a transient failure has not read every
// stream to its end: it answers more from its newest line rather than the live end,
// and refuses to go on once nothing newer is left but that stream's.
func TestAHeadPageThatCannotAdvanceIsRefused(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-2/app", stamped(0, "b0"))
	s.setRefusal("prod/web-1/app", &refusal{503, "unavailable"})
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 5})
	require.NoError(t, err)
	assert.Equal(t, []string{"b0"}, messages(page.Records))
	require.Equal(t, PageEdge{Cursor: lineCursor("web-2", "app", 0, 0), More: true}, page.After, "web-1 is still to read")

	_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Cursor: page.After.Cursor, Limit: 5})
	assert.EqualError(t, err, "Kstack could not read newer lines of this view: web-1/app: unavailable")
}

// A fill that fails the page ends the others rather than letting them read on: a
// stream whose read would never end on its own ends with it.
func TestAFatalFillEndsTheOthers(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.set("web-1/app", fakeRead{err: &statusError{401, "Unauthorized"}})
	backend.set("web-2/app", fakeRead{wait: never})
	svc, _ := serviceOn(t, backend)

	var err error
	testutil.WaitReturn(t, func() {
		_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	}, "the page")
	assert.EqualError(t, err, "the cluster refused to show this log: Unauthorized")
}

// A page the log file refuses fails: the file closed before the page opens it, in
// either mode, or taken by a clear before the page fills it or writes what it read.
func TestAPageTheLogFileRefusesFails(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	require.NoError(t, svc.files.Close())
	mid := at(0)
	for _, q := range []FetchQuery{{Mode: FetchHead, Limit: 10}, {Mode: FetchAround, At: &mid, Limit: 10}} {
		_, err := svc.Fetch(t.Context(), "7", webView, q)
		assert.ErrorIs(t, err, logstore.ErrClosed, q.Mode)
	}

	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"))
	svc, f := serviceOver(t, webMirror(t), s)
	f.beforeStatement = func(text string) {
		if strings.Contains(text, "SELECT p.uid, coalesce(c.container_id") {
			assert.NoError(t, svc.files.Clear(int64(f.active.Cache.ID)))
		}
	}
	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, logstore.ErrClosed)

	svc, f = serviceOver(t, webMirror(t), s)
	f.onAcquire = func() { assert.NoError(t, svc.Clear(int64(f.active.Cache.ID))) }
	_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, logstore.ErrClosed)
}

// A page every stream of which failed, with no line to show, is refused naming the
// first.
func TestAPageEveryStreamFailedIsRefused(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.setRefusal("prod/web-1/app", &refusal{404, `pods "web-1" not found`})
	s.setRefusal("prod/web-2/app", &refusal{404, `pods "web-2" not found`})
	svc, _ := serviceOver(t, webMirror(t), s)

	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})

	assert.ErrorContains(t, err, "no container of this view could be read: ")
}

// An Around page names a stream both halves skipped once, and anchors at the Tail
// half's length.
func TestAnAroundPageNamesASkippedStreamOnce(t *testing.T) {
	t.Parallel()
	page := around(merge{records: make([]Record, 3), skipped: []string{"a"}}, merge{skipped: []string{"a", "b"}})
	assert.Equal(t, []string{"a", "b"}, page.Skipped)
	require.NotNil(t, page.Anchor)
	assert.Equal(t, 3, *page.Anchor)
}
