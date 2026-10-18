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
	"io"
	"math/rand"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
// moments and long lines, under small limits and a grep, the pages hold every line
// in range once, in order, each within its limit, whether the walk starts at an end
// or at a line's cursor. Some streams lost moments to a rotation the file never saw,
// and each hole between two lines the file holds rides on exactly one record, the
// first past it the grep passes, with the hole's own two times. A second of the
// kubelet's log louder than the limit stops its stream: its lines may be left out,
// all of them backward and the rest of the second past what the read took forward,
// and a walk from the start names the stream in skipped. Every page resolves the
// view through SQL the store parses afresh, so under the race detector the full
// sweep takes minutes; -short walks a prefix of the seeds, and the stress run walks
// them all.
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
			svc.caps = randomCaps(rng)
			loud := loudLines(s, svc.caps.second)
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

			joins := func(want, got []Record, name string) {
				t.Helper()
				assert.True(t, ordered(got), "%s: in order, none repeated", name)
				assert.Equal(t, marks(without(want, loud)), marks(without(got, loud)), name)
			}
			got, skipped := walkSkipping(t, svc, view, FetchHead, limit, nil)
			joins(want, got, "head")
			for _, key := range loud {
				assert.Contains(t, skipped, key.Pod+"/"+key.Container, "a loud second is named")
			}
			joins(want, walk(t, svc, view, FetchTail, limit, nil), "tail")
			if len(want) == 0 {
				return
			}
			i := rng.Intn(len(want))
			c := cursorOf(positionOf(want[i]))
			joins(want[i+1:], walk(t, svc, view, FetchHead, limit, c), "after")
			joins(want[:i], walk(t, svc, view, FetchTail, limit, c), "before")
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
			// Some logs hold long lines, which make some seconds loud.
			long := []int{0, 0, 1500}[rng.Intn(3)]
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
				if long > 0 && rng.Intn(3) == 0 {
					msg += " " + strings.Repeat("x", rng.Intn(long))
				}
				s.add(key+"/"+container, stamped(d, msg))
				all = append(all, Record{Timestamp: at(d), Message: msg, Stream: stream, Place: place, ContainerID: containerID("uid-"+name, container, 0), Ordinal: ordinal})
			}
		}
	}
	slices.SortStableFunc(all, CompareLines)
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

// loudLines is each line of the kubelet's logs in s whose second carries more than
// limit bytes, by message: a read may leave it out.
func loudLines(s *logServer, limit int64) map[string]StreamKey {
	out := map[string]StreamKey{}
	for key, lines := range s.logs {
		parts := strings.Split(key, "/")
		stream := StreamKey{Namespace: parts[0], Pod: parts[1], Container: parts[2]}
		bytes := map[time.Time]int64{}
		for _, line := range lines {
			rec, _ := parseLine([]byte(line))
			bytes[rec.Timestamp.Truncate(time.Second)] += int64(len(line) + 1)
		}
		for _, line := range lines {
			if rec, _ := parseLine([]byte(line)); bytes[rec.Timestamp.Truncate(time.Second)] > limit {
				out[rec.Message] = stream
			}
		}
	}
	return out
}

// without is recs less the loud lines, with no hole on a record of a stream that
// holds one: a loud second's unread part is a live gap, which no record looks back
// past.
func without(recs []Record, loud map[string]StreamKey) []Record {
	streams := map[StreamKey]bool{}
	for _, key := range loud {
		streams[key] = true
	}
	out := slices.DeleteFunc(slices.Clone(recs), func(rec Record) bool {
		_, ok := loud[rec.Message]
		return ok
	})
	for i := range out {
		if streams[out[i].Stream] {
			out[i].Gap = nil
		}
	}
	return out
}

// ordered is whether recs are in order with none repeated.
func ordered(recs []Record) bool {
	for i := 1; i < len(recs); i++ {
		if CompareLines(recs[i-1], recs[i]) >= 0 {
			return false
		}
	}
	return true
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
// start, and is every record in the order read, each page within its limit.
func walk(t *testing.T, svc *Service, view View, mode FetchMode, limit int, start *string) []Record {
	t.Helper()
	out, _ := walkSkipping(t, svc, view, mode, limit, start)
	return out
}

// walkSkipping is walk, and each stream a page skipped, as pod/container.
func walkSkipping(t *testing.T, svc *Service, view View, mode FetchMode, limit int, start *string) ([]Record, []string) {
	t.Helper()
	q := FetchQuery{Mode: mode, Cursor: start, Limit: limit}
	out := []Record{}
	var skipped []string
	for range 1000 {
		page, err := svc.Fetch(t.Context(), "7", view, q)
		require.NoError(t, err)
		for _, line := range page.Skipped {
			skipped = append(skipped, strings.SplitN(line, ":", 2)[0])
		}
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
			return out, skipped
		}
		q = FetchQuery{Mode: mode, Cursor: edge.Cursor, Limit: limit}
	}
	t.Fatal("the pages never end")
	return nil, nil
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

// A Head page from a moment starts at its first line, the moment's own lines in, and
// a Tail page to one ends at its last; the edge the page started from carries the
// bound, with more past it, since the page never looked there.
func TestAPageReadsFromItsNearBound(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(6)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Since: cursor(2 * time.Second), Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"2", "3", "4", "5"}, messages(page.Records))
	assert.Equal(t, PageEdge{Cursor: cursor(2 * time.Second), More: true}, page.Before)
	assert.False(t, page.After.More)

	page, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Until: cursor(3 * time.Second), Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "2", "3"}, messages(page.Records))
	assert.Equal(t, PageEdge{Cursor: cursor(3 * time.Second), More: true}, page.After)
	assert.Equal(t, PageEdge{}, page.Before)
}

// A page stops at its far bound, whose moment's lines are in, and says nothing is
// left that way; one the limit cut first still says more. A line at either end is
// out of the range.
func TestAPageStopsAtItsFarBound(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(6)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	fetch := func(q FetchQuery) Page {
		t.Helper()
		page, err := svc.Fetch(t.Context(), "7", webView, q)
		require.NoError(t, err)
		return page
	}

	page := fetch(FetchQuery{Mode: FetchHead, Since: cursor(time.Second), Until: cursor(3 * time.Second), Limit: 10})
	assert.Equal(t, []string{"1", "2", "3"}, messages(page.Records))
	assert.False(t, page.After.More)

	page = fetch(FetchQuery{Mode: FetchTail, Since: cursor(2 * time.Second), Until: cursor(4 * time.Second), Limit: 10})
	assert.Equal(t, []string{"2", "3", "4"}, messages(page.Records))
	assert.False(t, page.Before.More)

	page = fetch(FetchQuery{Mode: FetchHead, Since: cursor(time.Second), Until: cursor(3 * time.Second), Limit: 2})
	assert.Equal(t, []string{"1", "2"}, messages(page.Records))
	assert.Equal(t, PageEdge{Cursor: lineCursor("web-1", "app", 2*time.Second, 0), More: true}, page.After)

	one, three := lineCursor("web-1", "app", time.Second, 0), lineCursor("web-1", "app", 3*time.Second, 0)
	assert.Equal(t, []string{"2"}, messages(fetch(FetchQuery{Mode: FetchHead, Since: one, Until: three, Limit: 10}).Records))
	assert.Equal(t, []string{"2"}, messages(fetch(FetchQuery{Mode: FetchTail, Since: one, Until: three, Limit: 10}).Records))
}

// A fill never asks for a line past the page's far bound: a range no line of which
// passes the grep reads up to the bound and no further, however much log follows,
// either way.
func TestAFillStopsAtTheFarBound(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3000)...)
	svc, f := serviceOver(t, webMirror(t), s)
	view := webView
	view.Grep = "^none"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Since: cursor(100 * time.Second), Until: cursor(110 * time.Second), Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.False(t, page.After.More)
	for _, c := range chunksOf(t, svc, f, "web-1", "app") {
		assert.LessOrEqual(t, c.To.TS, at(110*time.Second).UnixNano())
	}

	page, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Since: cursor(2000 * time.Second), Until: cursor(2010 * time.Second), Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.False(t, page.Before.More)
	for _, c := range chunksOf(t, svc, f, "web-1", "app") {
		if c.To.TS > at(110*time.Second).UnixNano() {
			assert.GreaterOrEqual(t, c.From.TS, at(2000*time.Second).UnixNano())
		}
	}
}

// A page over a range the file holds whole asks the backend for nothing.
func TestAPageInsideARangeTheFileHoldsReadsNothing(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3000)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Since: cursor(100 * time.Second), Until: cursor(110 * time.Second), Limit: 50})
	require.NoError(t, err)
	before := len(s.requests())

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Since: cursor(102 * time.Second), Until: cursor(105 * time.Second), Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, []string{"102", "103", "104", "105"}, messages(page.Records))
	assert.Len(t, s.requests(), before)
}

// A group of lines sharing one moment, larger than the page, is read across pages
// of a range through each page's edge cursor, either way, losing and repeating
// nothing: the cursor names a line, where a moment could not.
func TestARangePagesThroughAMomentLargerThanThePage(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	var want []string
	s.add("prod/web-1/app", stamped(4*time.Second, "before"))
	for i := range 9 {
		s.add("prod/web-1/app", stamped(5*time.Second, "a"+strconv.Itoa(i)))
		want = append(want, "a"+strconv.Itoa(i))
	}
	s.add("prod/web-1/app", stamped(6*time.Second, "after"))
	for i := range 4 {
		s.add("prod/web-2/app", stamped(5*time.Second, "b"+strconv.Itoa(i)))
		want = append(want, "b"+strconv.Itoa(i))
	}
	svc, _ := serviceOver(t, webMirror(t), s)
	moment := cursor(5 * time.Second)

	var got []string
	q := FetchQuery{Mode: FetchHead, Since: moment, Until: moment, Limit: 4}
	for range 10 {
		page, err := svc.Fetch(t.Context(), "7", webView, q)
		require.NoError(t, err)
		got = append(got, messages(page.Records)...)
		if !page.After.More {
			break
		}
		q.Since = page.After.Cursor
	}
	assert.Equal(t, want, got)

	got = nil
	q = FetchQuery{Mode: FetchTail, Since: moment, Until: moment, Limit: 4}
	for range 10 {
		page, err := svc.Fetch(t.Context(), "7", webView, q)
		require.NoError(t, err)
		got = append(messages(page.Records), got...)
		if !page.Before.More {
			break
		}
		q.Until = page.Before.Cursor
	}
	assert.Equal(t, want, got)
}

// A range is the Head and Tail pages' alone, and never beside a cursor or at.
func TestARangeIsRefusedBesideACursorOrAt(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	now := at(0)
	for want, q := range map[string]FetchQuery{
		"an Around page takes no since or until": {Mode: FetchAround, At: &now, Since: cursor(0)},
		"a Head page takes a cursor or a range":  {Mode: FetchHead, Cursor: cursor(0), Until: cursor(time.Second)},
		"a Tail page takes a cursor or a range":  {Mode: FetchTail, Cursor: cursor(0), Since: cursor(0)},
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

// loudAround is web-1/app and web-2/app each logging a line a second from 10:00:00 to
// 10:00:09, with web-1/app also logging a loud second at 10:00:05, and a service
// reading it under smallCaps.
func loudAround(t *testing.T) (*Service, *logServer) {
	t.Helper()
	s := newLogServer()
	for i := range 10 {
		d := time.Duration(i) * time.Second
		s.add("prod/web-2/app", stamped(d+500*time.Millisecond, "web-2 "+strconv.Itoa(i)))
		s.add("prod/web-1/app", stamped(d, "web-1 "+strconv.Itoa(i)))
		if i == 5 {
			for j := range 12 {
				s.add("prod/web-1/app", stamped(d+time.Duration(1+j)*time.Millisecond, loudLine))
			}
		}
	}
	svc, _ := serviceOver(t, webMirror(t), s)
	svc.caps = smallCaps
	return svc, s
}

// A stream too loud to read in one second stops there, and the page is cut at that
// second's top: the stream is named in skipped with the reason, the other streams'
// lines at and after that second are on the page, and the next page reads on below
// it.
func TestATooLoudSecondCutsATailPage(t *testing.T) {
	t.Parallel()
	svc, _ := loudAround(t)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Cursor: cursor(9 * time.Second), Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, []string{"web-2 5", "web-1 6", "web-2 6", "web-1 7", "web-2 7", "web-1 8", "web-2 8"}, messages(page.Records))
	assert.Equal(t, []string{"web-1/app: " + loudAt(at(5*time.Second)).Error()}, page.Skipped)
	assert.Equal(t, PageEdge{Cursor: cursorOf(position{Timestamp: at(5 * time.Second)}), More: true}, page.Before)

	next, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Cursor: page.Before.Cursor, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, []string{"web-1 0", "web-2 0", "web-1 1", "web-2 1", "web-1 2", "web-2 2", "web-1 3", "web-2 3", "web-1 4", "web-2 4"}, messages(next.Records))
	assert.Empty(t, next.Skipped)
}

// Forward, the page is cut at the loud second's last nanosecond, with the lines of
// it the read took before the limit, and the next page reads from the second after
// it, so it does not stop again.
func TestATooLoudSecondCutsAHeadPage(t *testing.T) {
	t.Parallel()
	svc, s := loudAround(t)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Cursor: cursor(3 * time.Second), Limit: 500})

	require.NoError(t, err)
	got := messages(page.Records)
	assert.Equal(t, []string{"web-2 3", "web-1 4", "web-2 4", "web-1 5"}, got[:4])
	assert.Equal(t, "web-2 5", got[len(got)-1])
	assert.Len(t, got, 5+7, "the loud lines that fit in the second beside web-1 5")
	assert.Equal(t, []string{"web-1/app: " + loudAt(at(5*time.Second)).Error()}, page.Skipped)
	assert.Equal(t, PageEdge{Cursor: cursorOf(position{Timestamp: at(6*time.Second - 1)}), More: true}, page.After)

	next, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Cursor: page.After.Cursor, Limit: 500})
	require.NoError(t, err)
	assert.Equal(t, []string{"web-1 6", "web-2 6", "web-1 7", "web-2 7", "web-1 8", "web-2 8", "web-1 9", "web-2 9"}, messages(next.Records))
	assert.Empty(t, next.Skipped)
	reads := s.requestsOf("prod/web-1/app")
	assert.Equal(t, "2026-10-08T10:00:06Z", reads[len(reads)-1].Get("sinceTime"))
}

// A view whose only stream is too loud answers a page with no lines, the reason and
// more, not a refusal; the next page reads the lines below the second.
func TestAViewOfOneTooLoudStreamAnswersThePage(t *testing.T) {
	t.Parallel()
	svc, _ := loudAround(t)

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Cursor: cursor(5*time.Second + 500*time.Millisecond), Limit: 100})

	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Equal(t, []string{"web-1/app: " + loudAt(at(5*time.Second)).Error()}, page.Skipped)
	assert.True(t, page.Before.More)

	next, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Cursor: page.Before.Cursor, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, []string{"web-1 0", "web-1 1", "web-1 2", "web-1 3", "web-1 4"}, messages(next.Records))
}

// A quiet stream cut when its last window that completed was empty is cut at that
// window's start, and the next page goes on below it.
func TestAnEmptyWindowsCutIsItsStart(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	svc.caps.gets = 3

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Cursor: cursor(2 * time.Minute), Limit: 10})

	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Empty(t, page.Skipped)
	// Ten seconds back, then eight times that.
	assert.Equal(t, PageEdge{Cursor: cursorOf(position{Timestamp: at(30 * time.Second)}), More: true}, page.Before)
	assert.Equal(t, messages(linesOf(lineEvery(10))), recordsOf(pagesFrom(t, svc, web1View, FetchTail, 10, page.Before.Cursor), FetchTail))
}

// A stream the pool let read far is walked as far: its walk reads its share of the
// walks' budget and what its fill downloaded, so a sparse grep does not return an
// empty page over lines already in the file.
func TestTheWalkFollowsTheFill(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "match"))
	for i := range 100 {
		s.add("prod/web-1/app", stamped(time.Duration(1+i)*time.Second, strings.Repeat("x", 80)))
	}
	svc, _ := serviceOver(t, webMirror(t), s)
	svc.walkBudget = 1 << 10
	view := webView
	view.Grep = "^match"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"match"}, messages(page.Records))
	assert.False(t, page.Before.More)
}

// hundredStreams is a mirror of a hundred pods of web, and the key of each one's
// app container.
func hundredStreams(t *testing.T) (*fakeService, []string) {
	t.Helper()
	f := webMirror(t)
	addPods(t, f, 98)
	keys := []string{"prod/web-1/app", "prod/web-2/app"}
	for i := range 98 {
		keys = append(keys, "prod/many-"+strconv.Itoa(i)+"/app")
	}
	return f, keys
}

// The regression: a view of a hundred streams, one of which bursts for ten seconds
// just before the cursor at two thirds of what a second may hold, pages back with
// no refusal and no stopped stream. Under midCaps, a second holds 16 KiB and the
// burst is 83 lines of 100 bytes a second.
func TestABurstInAHundredStreamsPagesBack(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f, keys := hundredStreams(t)
	for i, key := range keys {
		s.add(key, stamped(time.Duration(i)*time.Second, "quiet"))
	}
	for i := range 833 {
		s.add("prod/web-1/app", stamped(100*time.Second+time.Duration(i)*12*time.Millisecond, loudLine))
	}
	svc, _ := serviceOver(t, f, s)
	svc.caps = midCaps

	got := pagesFrom(t, svc, webView, FetchTail, MaxLimit, cursor(110*time.Second))

	for _, page := range got {
		assert.Empty(t, page.Skipped)
	}
	assert.Len(t, recordsOf(got, FetchTail), 833+100)
}

// counting reads through the kubelet over a get whose bodies count the bytes read
// from them in n.
func counting(svc *Service, n *atomic.Int64) {
	svc.backends = []namedBackend{{"kubelet", func(get getter) Backend {
		return newKubelet(func(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
			body, err := get(ctx, path, q)
			if err != nil {
				return nil, err
			}
			return countedBody{body, n}, nil
		})
	}}}
}

// countedBody counts the bytes read from it in n.
type countedBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (b countedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

// A sparse grep over a hundred logs shares the query's limit: the first page
// downloads about the query's bytes and what its GETs read past their windows, not
// every log. Under smallCaps, a hundred 36 KiB logs and a 64 KiB query.
func TestFirstWindowsShareTheLimit(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f, keys := hundredStreams(t)
	for _, key := range keys {
		s.add(key, wideLines(300, 100)...)
	}
	svc, _ := serviceOver(t, f, s)
	svc.caps = smallCaps
	var n atomic.Int64
	counting(svc, &n)
	view := webView
	view.Grep = "never"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Limit: 10})

	require.NoError(t, err)
	assert.True(t, page.Before.More)
	// A GET reads at most a 4 KiB buffer past its window, and a stream sends at most
	// three before it is cut.
	assert.Less(t, n.Load(), smallCaps.query+100*3*(4<<10))
}

// A sparse grep over several logs past a stream's limit pages on with more until
// every stream reaches its first line, none stopped. Four 12 KiB logs under
// smallCaps, three GETs a stream.
func TestAGrepWithNoMatchPagesToEveryFirstLine(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	addPods(t, f, 2)
	for _, key := range []string{"prod/web-1/app", "prod/web-2/app", "prod/many-0/app", "prod/many-1/app"} {
		s.add(key, wideLines(100, 100)...)
	}
	svc, _ := serviceOver(t, f, s)
	svc.caps = smallCaps
	svc.caps.gets = 3
	view := webView
	view.Grep = "never"

	got := pages(t, svc, view, FetchTail, 10)

	assert.Greater(t, len(got), 1, "cut")
	for _, p := range got {
		assert.Empty(t, p.Records)
		assert.Empty(t, p.Skipped)
	}
	assert.False(t, got[len(got)-1].Before.More)
}

// A grep with no match over one stream reads back to the log's first line: the
// page answers no record and no more.
func TestAGrepWithNoMatchReadsToTheFirstLine(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(1000)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	view := web1View
	view.Grep = "never"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchTail, Limit: 10})

	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.False(t, page.Before.More)
}

// A stream is cut at its limits, backward and forward, and each next page goes on
// from its cut: a sparse grep over 640 KiB under limits scaled down 64 times reads
// every match over several pages.
func TestAStreamsCutsJoin(t *testing.T) {
	t.Parallel()
	for _, mode := range []FetchMode{FetchTail, FetchHead} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			s := newLogServer()
			var log []string
			var want []string
			for i := range 320 {
				msg := strconv.Itoa(i) + strings.Repeat("x", 2<<10)
				if i%40 == 7 {
					msg = "match " + strconv.Itoa(i)
					want = append(want, msg)
				}
				log = append(log, stamped(time.Duration(i)*time.Second, msg))
			}
			s.add("prod/web-1/app", log...)
			svc, _ := serviceOver(t, webMirror(t), s)
			svc.caps = midCaps
			view := web1View
			view.Grep = "^match"

			got := pages(t, svc, view, mode, 100)

			assert.Greater(t, len(got), 1, "cut")
			assert.Equal(t, want, recordsOf(got, mode))
		})
	}
}

// gated is a Backend that runs wait before each read it hands to b.
type gated struct {
	b    Backend
	wait func(st stream, o readOpts)
}

func (w gated) Forward(ctx context.Context, st stream, o readOpts) *streamRead {
	w.wait(st, o)
	return w.b.Forward(ctx, st, o)
}

func (w gated) Backward(ctx context.Context, st stream, o readOpts) *streamRead {
	w.wait(st, o)
	return w.b.Backward(ctx, st, o)
}

func (w gated) Tail(ctx context.Context, st stream, o readOpts) *streamRead {
	w.wait(st, o)
	return w.b.Tail(ctx, st, o)
}

// A fill that ends early gives back what it holds, so the streams still reading may
// spend it: web-2's one line is read in its first window, and web-1's read begins
// only once the pool holds more than its seed, which web-2's allowance alone adds.
func TestAFillThatEndsEarlyReturnsItsAllowance(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	s.add("prod/web-2/app", stamped(0, "quiet"))
	svc, _ := serviceOver(t, webMirror(t), s)
	svc.caps = smallCaps
	seeded := smallCaps.query - 2*smallCaps.stream
	svc.backends = []namedBackend{{"kubelet", func(get getter) Backend {
		return gated{newKubelet(get), func(st stream, o readOpts) {
			if st.key.Pod == "web-1" {
				assert.Eventually(t, func() bool { return o.limits.pool.load() > seeded }, testutil.Timeout, time.Millisecond)
			}
		}}
	}}}

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 100})

	require.NoError(t, err)
	assert.Len(t, page.Records, 11)
}
