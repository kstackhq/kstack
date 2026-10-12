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
	"math/rand"
	"net/http"
	"net/url"
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
)

// webView reads the app container of deployment web in prod.
var webView = View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}}}

// serviceOver is the service over m's mirror and the API server h, on a clock
// reading 11:00, its follows handing off and resolving at once, and retrying within
// milliseconds however often a stream drops.
func serviceOver(t *testing.T, m *mirror, h http.Handler) (*Service, *fakeService) {
	t.Helper()
	f := m.service()
	f.lease = serving(t, h).lease
	svc := New(f)
	svc.now, svc.handoff, svc.settle = func() time.Time { return at(time.Hour) }, 0, 0
	svc.retry, svc.retryCap = time.Millisecond, 8*time.Millisecond
	return svc, f
}

// A deployment's last lines come back in time order, with the cursor of the
// oldest.
func TestFetchAnswersADeploymentsLastLines(t *testing.T) {
	s := newLogServer()
	for i := range 15 {
		pod := "web-" + strconv.Itoa(1+i%2)
		s.add("prod/"+pod+"/app", stamped(time.Duration(i)*time.Second, strconv.Itoa(i)))
	}
	svc, _ := serviceOver(t, webMirror(), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 5})

	require.NoError(t, err)
	assert.Equal(t, []string{"10", "11", "12", "13", "14"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 10*time.Second, 0), page.NextCursor)
	assert.Equal(t, "web-1", page.Records[0].Source.Pod)
	assert.Equal(t, "eu-1a", page.Records[0].Source.Zone)
}

// A head page after a cursor starts past it, and grep keeps the matching lines.
func TestFetchReadsForwardAfterACursor(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "error a"), stamped(time.Second, "ok"), stamped(2*time.Second, "error b"), stamped(3*time.Second, "error c"))
	svc, _ := serviceOver(t, webMirror(), s)
	view := webView
	view.Grep = "^error"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, After: cursor(0), Limit: 1})

	require.NoError(t, err)
	assert.Equal(t, []string{"error b"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 2*time.Second, 0), page.NextCursor)
}

// A tail page before a cursor reaches the line before it however much log follows,
// and a page that fills reads no further: limit 1 reads one window.
func TestFetchPagesBackBeforeACursor(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(1000)...)
	svc, _ := serviceOver(t, webMirror(), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Before: cursor(100 * time.Second), Limit: 1})

	require.NoError(t, err)
	assert.Equal(t, []string{"99"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 99*time.Second, 0), page.NextCursor)

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
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"), stamped(0, "a'"), stamped(time.Second, "c"))
	s.add("prod/web-2/app", stamped(0, "b"))
	svc, _ := serviceOver(t, webMirror(), s)

	var got []string
	q := FetchQuery{Mode: FetchHead, Limit: 1}
	for range 4 {
		page, err := svc.Fetch(t.Context(), "7", webView, q)
		require.NoError(t, err)
		got = append(got, messages(page.Records)...)
		if page.NextCursor == nil {
			break
		}
		q.After = page.NextCursor
	}
	assert.Equal(t, []string{"a", "a'", "b", "c"}, got)
}

// Pages join: walked from either end through random logs with tied and sub-second
// moments, under small limits and budgets and a grep, the pages hold every line
// in range once, in order, each within its limit; and a since or until keeps its
// line where an after or before drops it. A page the budget refuses is the honest
// answer to a view too wide for it, so the walk is asked again with twice the
// budget, as a user would narrow the view.
func TestPagesJoinWithoutLossOrRepeat(t *testing.T) {
	for seed := range 150 {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seed)))
			s, m, all := randomLogs(rng)
			svc, f := serviceOver(t, m, s)
			svc.budget = int64(2000 + rng.Intn(8000))
			f.chunk(1 + rng.Intn(64))
			view := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web"}}}
			view.Grep = []string{"", "odd", "#.*7"}[rng.Intn(3)]
			grep := regexp.MustCompile(view.Grep)
			var want []Record
			for _, rec := range all {
				if grep.MatchString(rec.Message) {
					want = append(want, rec)
				}
			}
			limit := 1 + rng.Intn(25)

			assert.Equal(t, messages(want), walk(t, svc, view, FetchHead, limit, nil), "head")
			assert.Equal(t, messages(want), walk(t, svc, view, FetchTail, limit, nil), "tail")
			if len(want) == 0 {
				return
			}
			i := rng.Intn(len(want))
			c := cursorOf(positionOf(want[i]))
			assert.Equal(t, messages(want[i:]), walk(t, svc, view, FetchHead, limit, &FetchQuery{Since: c}), "since")
			assert.Equal(t, messages(want[:i+1]), walk(t, svc, view, FetchTail, limit, &FetchQuery{Until: c}), "until")
		})
	}
}

// randomLogs is a deployment web of one to three pods, each with an app and an
// istio-proxy container, whose logs hold up to eighty lines each at moments that
// often tie within and across containers, some inside a second, some opening
// with a burst, every other line saying odd, and every line in the order a view
// of web reads them.
func randomLogs(rng *rand.Rand) (*logServer, *mirror, []Record) {
	s := newLogServer()
	m := &mirror{pods: map[string]mirrorPod{}, nodes: []mirrorNode{{"node-a", "eu", "eu-1a", "linux", "amd64"}}, under: map[string][]string{}}
	var all []Record
	for pod := 1; pod <= 1+rng.Intn(3); pod++ {
		key := "prod/web-" + strconv.Itoa(pod)
		m.pods[key] = mirrorPod{node: "node-a", containers: []string{"app", "istio-proxy"}}
		m.under["Deployment prod/web"] = append(m.under["Deployment prod/web"], key)
		for _, container := range []string{"app", "istio-proxy"} {
			src := RecordSource{Namespace: "prod", Pod: "web-" + strconv.Itoa(pod), Container: container, Node: "node-a", Region: "eu", Zone: "eu-1a", OS: "linux", Arch: "amd64"}
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
				msg := src.Pod + "/" + container + "#" + strconv.Itoa(i) + " " + parity
				s.add(key+"/"+container, stamped(d, msg))
				all = append(all, Record{Timestamp: at(d), Message: msg, Source: src, Ordinal: ordinal})
			}
		}
	}
	slices.SortStableFunc(all, compareLines)
	return s, m, all
}

// walk pages through view in mode from the end mode starts at, or from the cursor
// start names, and is every line in the order read, each page within its limit.
// A page refused for budget starts the walk again with twice the budget.
func walk(t *testing.T, svc *Service, view View, mode FetchMode, limit int, start *FetchQuery) []string {
	t.Helper()
	q := FetchQuery{Mode: mode, Limit: limit}
	if start != nil {
		q.Since, q.Until = start.Since, start.Until
	}
	out := []string{}
	for range 1000 {
		page, err := svc.Fetch(t.Context(), "7", view, q)
		if err != nil && strings.Contains(err.Error(), "ran out of budget") {
			svc.budget *= 2
			return walk(t, svc, view, mode, limit, start)
		}
		require.NoError(t, err)
		require.LessOrEqual(t, len(page.Records), limit)
		if mode == FetchHead {
			out = append(out, messages(page.Records)...)
		} else {
			out = append(messages(page.Records), out...)
		}
		if page.NextCursor == nil {
			return out
		}
		q = FetchQuery{Mode: mode, Limit: limit}
		if mode == FetchHead {
			q.After = page.NextCursor
		} else {
			q.Before = page.NextCursor
		}
	}
	t.Fatal("the pages never end")
	return nil
}

// A limit past 500, or under 1, is refused before anything is read.
func TestFetchRefusesABadLimit(t *testing.T) {
	svc, _ := serviceOver(t, webMirror(), newLogServer())
	for _, limit := range []int{0, 501} {
		_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: limit})
		var r *Refusal
		assert.True(t, errors.As(err, &r), limit)
	}
}

// A cursor that names no moment is refused, whichever argument carries it, and so
// is a grep that is no regular expression.
func TestFetchRefusesABadCursor(t *testing.T) {
	svc, _ := serviceOver(t, webMirror(), newLogServer())
	bad := "yesterday"
	for name, q := range map[string]FetchQuery{
		"since":  {Since: &bad},
		"until":  {Until: &bad},
		"after":  {After: &bad},
		"before": {Before: &bad},
	} {
		q.Mode, q.Limit = FetchHead, 10
		_, err := svc.Fetch(t.Context(), "7", webView, q)
		assert.EqualError(t, err, "a cursor must be an RFC 3339 time, or one a page handed back", name)
	}

	view := webView
	view.Grep = "("
	_, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.EqualError(t, err, "the grep is not a regular expression")
}

// A fetch is refused what resolving the view or claiming the cluster refuses.
func TestFetchIsRefusedWhatItsReadsRefuse(t *testing.T) {
	svc, f := serviceOver(t, webMirror(), newLogServer())
	f.active.Cache = nil
	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrNoCache)

	svc, f = serviceOver(t, webMirror(), newLogServer())
	f.acquireErr = cluster.ErrNotConnectable
	_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrRevoked)
}

// A view no pod passes reads nothing, and answers an empty page.
func TestFetchOfAViewWithNoContainerIsEmpty(t *testing.T) {
	s := newLogServer()
	svc, _ := serviceOver(t, webMirror(), s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"nowhere"}}}

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, Page{Records: []Record{}, Skipped: []string{}}, page)
	assert.Empty(t, s.requests())
}

// manyPods adds n pods under deployment prod/web, each with one app container.
func manyPods(m *mirror, n int) {
	for i := range n {
		m.setPod("prod/many-"+strconv.Itoa(i), &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	}
}

// A view of more containers than Kstack reads at once is refused before any is
// read, by a fetch and a follow alike.
func TestAViewOfTooManyContainersIsRefused(t *testing.T) {
	const tooMany = "this view reads 101 containers and Kstack reads at most 100 at once; filter it by node, or name its containers"
	s := newLogServer()
	m := webMirror()
	manyPods(m, 99)
	svc, _ := serviceOver(t, m, s)

	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 5})
	assert.EqualError(t, err, tooMany)
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.EqualError(t, err, tooMany)
	assert.Empty(t, s.requests())
}

// A fetch ends with its context. It checks the cluster once and watches nothing,
// since the connection's retirement already bounds a page.
func TestFetchEndsWithItsContextAndWatchesNothing(t *testing.T) {
	svc, _ := serviceOver(t, webMirror(), newLogServer())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := svc.Fetch(ctx, "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, context.Canceled)

	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"))
	svc, f := serviceOver(t, webMirror(), s)
	f.watchErr = errors.New("the fetch must not watch")
	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, messages(page.Records))
}

// A follow is refused before it opens: a bad cursor, a view the mirror refuses, a
// cluster that cannot be claimed, and a pods watch that cannot be opened, which
// releases the claim.
func TestFollowIsRefusedBeforeItOpens(t *testing.T) {
	bad := "yesterday"
	svc, _ := serviceOver(t, webMirror(), newLogServer())
	_, err := svc.Follow(t.Context(), "7", webView, &bad)
	assert.EqualError(t, err, "a cursor must be an RFC 3339 time, or one a page handed back")

	svc, f := serviceOver(t, webMirror(), newLogServer())
	f.active.Cache = nil
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.ErrorIs(t, err, ErrNoCache)

	svc, f = serviceOver(t, webMirror(), newLogServer())
	f.acquireErr = cluster.ErrNotConnectable
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.ErrorIs(t, err, ErrRevoked)

	svc, f = serviceOver(t, webMirror(), newLogServer())
	f.watchObjectsErr = errors.New("no watch")
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.EqualError(t, err, "no watch")
	assert.Equal(t, int32(1), f.lease.released.Load())
}

// openFollow opens a follow of webView over f, failing the test on an error, and
// closes the pods watch's snapshot.
func openFollow(t *testing.T, svc *Service, f *fakeService) (*cluster.Stream[FollowFrame], context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	stream, err := svc.Follow(ctx, "7", webView, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	return stream, cancel
}

// A follow on a deployment picks up a pod added to the mirror after it opened,
// from the follow's start, and goes on after one is deleted.
func TestAFollowTracksTheDeploymentsPods(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)

	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	assert.Equal(t, "from web-1", recvLine(t, stream, "web-1's line").Message)

	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	s.follow("prod/web-3/app") <- stamped(time.Hour+2*time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)

	m.setPod("prod/web-1", nil, "")
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "prod/web-1")
	s.follow("prod/web-2/app") <- stamped(time.Hour+3*time.Second, "from web-2")
	assert.Equal(t, "from web-2", recvLine(t, stream, "web-2's line").Message)

	var sinces []string
	for _, q := range s.requests() {
		if q.Get("sinceTime") != "" {
			sinces = append(sinces, q.Get("sinceTime"))
		}
	}
	assert.Equal(t, []string{"2026-10-08T11:00:00Z", "2026-10-08T11:00:00Z", "2026-10-08T11:00:00Z"}, sinces)
}

// A follow with no cursor reads each log from the moment it started.
func TestAFollowOpensAtItsStart(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "old"))
	svc, f := serviceOver(t, webMirror(), s)
	stream, _ := openFollow(t, svc, f)

	s.follow("prod/web-1/app") <- stamped(time.Hour, "new")

	assert.Equal(t, "new", recvLine(t, stream, "the new line").Message)
	assert.Equal(t, "2026-10-08T11:00:00Z", s.requests()[0].Get("sinceTime"))
}

// A follow after a cursor reads every line past it first.
func TestAFollowAfterACursor(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "seen"), stamped(time.Second, "missed"))
	svc, _ := serviceOver(t, webMirror(), s)

	stream, err := svc.Follow(t.Context(), "7", webView, cursor(0))

	require.NoError(t, err)
	assert.Equal(t, "missed", recvLine(t, stream, "the missed line").Message)
}

// recvLine waits for stream's next frame and answers its line.
func recvLine(t *testing.T, stream *cluster.Stream[FollowFrame], what string) Record {
	t.Helper()
	frame := testutil.Recv(t, stream.Frames, what)
	require.Equal(t, FollowLine, frame.Type, what)
	return *frame.Record
}

// endedWith waits for stream's Ended frame, then for it to end clean, and answers
// the frame's reason.
func endedWith(t *testing.T, stream *cluster.Stream[FollowFrame]) string {
	t.Helper()
	frame := testutil.Recv(t, stream.Frames, "the follow's end")
	require.Equal(t, FollowEnded, frame.Type)
	testutil.RecvClosed(t, stream.Frames, "the follow to end")
	require.NoError(t, stream.Err())
	return frame.Reason
}

// failsWith waits for stream to end with no Ended frame and answers its Err.
func failsWith(t *testing.T, stream *cluster.Stream[FollowFrame]) error {
	t.Helper()
	testutil.RecvClosed(t, stream.Frames, "the follow to end")
	return stream.Err()
}

// awaitClosed waits for key's first follow to end, and awaitReadAgain for a read of
// key from since.
func awaitClosed(t *testing.T, s *logServer, key string) {
	t.Helper()
	require.Eventually(t, func() bool { return s.followsClosed(key) >= 1 },
		testutil.Timeout, time.Millisecond, key+"'s stream to end")
}

func awaitReadAgain(t *testing.T, s *logServer, key, since string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(s.requestsOf(key), func(q url.Values) bool { return q.Get("sinceTime") == since })
	}, testutil.Timeout, time.Millisecond, "a read of "+key+" again from "+since)
}

// A cluster disabled under a follow ends it, saying so.
func TestAFollowEndsWhenTheClusterIsDisabled(t *testing.T) {
	svc, f := serviceOver(t, webMirror(), newLogServer())
	f.frames = make(chan cluster.ClusterWatchFrame)
	stream, _ := openFollow(t, svc, f)

	disabled := enabledCluster()
	disabled.Spec.Enabled = false
	f.frames <- cluster.ClusterWatchFrame{Type: cluster.DeltaFrameModified, Cluster: disabled}

	assert.Equal(t, ErrRevoked.Reason, endedWith(t, stream))
}

// A 401 on a stream the follow opens ends it: the user's credentials, not a pod's
// state.
func TestAFollowEndsOnAForbiddenStream(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusUnauthorized, "unauthorized"}
	svc, f := serviceOver(t, webMirror(), s)
	stream, _ := openFollow(t, svc, f)

	assert.Equal(t, "the cluster refused to show this log: unauthorized", endedWith(t, stream))
}

// A 403 is one namespace's, so it ends its own stream alone, and the follow ends
// with it only once every stream has: then following again would end the same way.
func TestAForbiddenStreamEndsItselfAlone(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-2/app"] = refusal{http.StatusForbidden, "forbidden"}
	svc, f := serviceOver(t, webMirror(), s)
	stream, _ := openFollow(t, svc, f)

	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	assert.Equal(t, "from web-1", recvLine(t, stream, "web-1's line").Message)

	s.setRefusal("prod/web-1/app", &refusal{http.StatusForbidden, "forbidden"})
	close(s.follow("prod/web-1/app"))
	assert.Equal(t, "the cluster refused to show this log: forbidden", endedWith(t, stream))
}

// A follow that ends on its own, with its caller still waiting, ends its pods
// watch too, and keeps the reason it ended.
func TestAFollowThatEndsReleasesItsWatch(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusForbidden, "forbidden"}
	s.refuse["prod/web-2/app"] = refusal{http.StatusForbidden, "forbidden"}
	svc, f := serviceOver(t, webMirror(), s)
	f.podsDone = make(chan struct{})
	stream, _ := openFollow(t, svc, f)

	assert.Equal(t, "the cluster refused to show this log: forbidden", endedWith(t, stream))
	testutil.Wait(t, f.podsDone, "the pods watch to end")
}

// A follow whose every container has finished ends, saying so.
func TestAFollowEndsWhenEveryStreamIsGone(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)

	m.finish("prod/web-1")
	m.finish("prod/web-2")
	close(s.follow("prod/web-1/app"))
	close(s.follow("prod/web-2/app"))

	assert.Equal(t, "every container of this view has stopped", endedWith(t, stream))
}

// A follow whose streams all end within the handoff still sends their lines, then
// says every container stopped.
func TestAFollowDeliversWhatItHeldBeforeItEnds(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(time.Second, "a"))
	s.add("prod/web-2/app", stamped(0, "b"))
	m := webMirror()
	m.finish("prod/web-1")
	m.finish("prod/web-2")
	svc, _ := serviceOver(t, m, s)
	svc.handoff = time.Hour
	close(s.follow("prod/web-1/app"))
	close(s.follow("prod/web-2/app"))

	stream, err := svc.Follow(t.Context(), "7", webView, cursor(-time.Second))
	require.NoError(t, err)

	assert.Equal(t, "b", recvLine(t, stream, "web-2's line").Message)
	assert.Equal(t, "a", recvLine(t, stream, "web-1's line").Message)
	assert.Equal(t, "every container of this view has stopped", endedWith(t, stream))
}

// A pod that joined the view between its resolving and the pods watch opening is
// followed once the watch's snapshot closes, though no frame names it after.
func TestAFollowReconcilesTheSnapshot(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, err := svc.Follow(t.Context(), "7", webView, nil)
	require.NoError(t, err)

	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A follow whose last pods leave the view waits for the next, as a Deployment
// scaled to zero and back has it.
func TestAFollowWaitsWhenItsLastPodsLeave(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)

	m.setPod("prod/web-1", nil, "")
	m.setPod("prod/web-2", nil, "")
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "prod/web-1")

	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A follow of a view with no container yet waits for its pods rather than being
// refused, and reads them from the moment it started.
func TestAFollowOfAnEmptyViewWaitsForItsPods(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, err := svc.Follow(t.Context(), "7", View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceJob, Name: "batch"}}}, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

	m.setPod("prod/batch-1", &mirrorPod{containers: []string{"main"}}, "Job prod/batch")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/batch-1")
	s.follow("prod/batch-1/main") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "batch-1's line").Message)
	assert.Equal(t, "2026-10-08T11:00:00Z", s.requests()[0].Get("sinceTime"))
}

// A stream that ends clean on a container the view sees running is opened again
// past its last line: a proxy or the API server can close an idle stream.
func TestAFollowReopensAStreamClosedOnARunningContainer(t *testing.T) {
	s := newLogServer()
	svc, f := serviceOver(t, webMirror(), s)
	stream, _ := openFollow(t, svc, f)
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
	recvLine(t, stream, "web-1's line")

	close(s.follow("prod/web-1/app"))

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
}

// A follow narrows its pods watch to the view's namespaces.
func TestAFollowWatchesOnlyItsNamespaces(t *testing.T) {
	svc, f := serviceOver(t, webMirror(), newLogServer())
	view := View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "web-1"},
		{Namespace: "prod", Kind: SourcePod, Name: "web-2"},
		{Namespace: "staging", Kind: SourcePod, Name: "web-1"},
	}}
	_, err := svc.Follow(t.Context(), "7", view, nil)
	require.NoError(t, err)

	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, []string{"prod", "staging"}, f.podNamespaces)
}

// A follow of a pod ends when the pod is deleted, and when a pod of the same name
// with another UID stands in its place.
func TestAFollowOfAPodEndsWithThePod(t *testing.T) {
	podView := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1"}}}
	for name, replacement := range map[string]*mirrorPod{
		"gone":     nil,
		"replaced": {node: "node-a", containers: []string{"app"}, uid: "uid-b"},
	} {
		t.Run(name, func(t *testing.T) {
			m := webMirror()
			m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"app"}, uid: "uid-a"}, "")
			svc, f := serviceOver(t, m, newLogServer())
			stream, err := svc.Follow(t.Context(), "7", podView, nil)
			require.NoError(t, err)
			f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

			m.setPod("prod/web-1", replacement, "")
			f.pods <- uidFrame(cluster.DeltaFrameDeleted, "prod/web-1", "uid-a")

			assert.Equal(t, "pod prod/web-1 was deleted", endedWith(t, stream))
		})
	}
}

// A pod deleted under a view that names other sources leaves them followed, and a
// pod of its name with another UID is not read in its place.
func TestADeletedPodLeavesItsViewsOtherSources(t *testing.T) {
	s := newLogServer()
	soloOpens := testutil.NewProbe[string](4)
	s.onOpen = func(key string) {
		if key == "prod/solo/app" {
			soloOpens.Fire(key)
		}
	}
	m := webMirror()
	m.setPod("prod/solo", &mirrorPod{node: "node-a", containers: []string{"app"}, uid: "uid-a"}, "")
	svc, f := serviceOver(t, m, s)
	view := webView
	view.Sources = append(slices.Clone(view.Sources), ViewSource{Namespace: "prod", Kind: SourcePod, Name: "solo"})
	stream, err := svc.Follow(t.Context(), "7", view, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	soloOpens.Await(t, "solo's first follow")

	m.setPod("prod/solo", &mirrorPod{node: "node-a", containers: []string{"app"}, uid: "uid-b"}, "")
	f.pods <- uidFrame(cluster.DeltaFrameDeleted, "prod/solo", "uid-a")
	// The API server ends a deleted pod's stream.
	close(s.follow("prod/solo/app"))
	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
	// A negative assertion, so a bounded window: a reopen would follow the stream's
	// end after one retry, and a hundred retries is far past a local request's round trip.
	testutil.NoRecv(t, soloOpens.Chan(), 100*svc.retry, "a follow of the pod that took solo's name")
}

// A view of several pods ends once every one of them is deleted, naming them.
func TestAFollowOfPodsEndsWhenEveryPodIsDeleted(t *testing.T) {
	m := webMirror()
	svc, f := serviceOver(t, m, newLogServer())
	view := View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "web-1"},
		{Namespace: "prod", Kind: SourcePod, Name: "web-2"},
	}}
	stream, err := svc.Follow(t.Context(), "7", view, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

	m.setPod("prod/web-1", nil, "")
	m.setPod("prod/web-2", nil, "")
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "prod/web-1")
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "prod/web-2")

	assert.Equal(t, "pods prod/web-1, prod/web-2 were deleted", endedWith(t, stream))
}

// A pod the watch deletes and the mirror still holds, as a cache's clear and
// relist send it, is followed on.
func TestAFollowOfAPodOutlastsAResync(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"app"}, uid: "uid-a"}, "")
	svc, f := serviceOver(t, m, s)
	stream, err := svc.Follow(t.Context(), "7", View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1"}}}, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

	f.pods <- uidFrame(cluster.DeltaFrameDeleted, "prod/web-1", "uid-a")
	f.pods <- uidFrame(cluster.DeltaFrameAdded, "prod/web-1", "uid-a")
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "still here")

	assert.Equal(t, "still here", recvLine(t, stream, "web-1's line").Message)
}

// A container waiting to start — a pod pulling its image, or crash-looping —
// keeps the follow open after its stream is refused, and is read once it runs.
func TestAFollowWaitsForAWaitingContainer(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusBadRequest, "container is waiting to start"}
	m := webMirror()
	m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"app"}, state: "waiting"}, "")
	svc, f := serviceOver(t, m, s)
	stream, err := svc.Follow(t.Context(), "7", View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1"}}}, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	require.Eventually(t, func() bool { return len(s.requests()) >= 1 }, testutil.Timeout, time.Millisecond, "web-1 asked")

	s.setRefusal("prod/web-1/app", nil)
	m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"app"}}, "")
	f.pods <- podFrame(cluster.DeltaFrameModified, "prod/web-1")
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "web-1's line").Message)
}

// An init container that finishes while the main one waits to start does not end
// the follow of their pod.
func TestAFinishedInitContainerLeavesItsPodFollowed(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusBadRequest, "container is waiting to start"}
	m := webMirror()
	waiting := &mirrorPod{node: "node-a", containers: []string{"init", "app"}, states: map[string]string{"init": "terminated", "app": "waiting"}}
	m.setPod("prod/web-1", waiting, "")
	svc, f := serviceOver(t, m, s)
	stream, err := svc.Follow(t.Context(), "7", View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{}}}}, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	close(s.follow("prod/web-1/init"))
	awaitClosed(t, s, "prod/web-1/init")

	s.setRefusal("prod/web-1/app", nil)
	m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"init", "app"}, states: map[string]string{"init": "terminated"}}, "")
	f.pods <- podFrame(cluster.DeltaFrameModified, "prod/web-1")
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "app's line").Message)
}

// A mirror answer cut at its bounds ends the follow, since asking again would be
// cut the same way.
func TestAFollowEndsOnACutMirrorAnswer(t *testing.T) {
	svc, f := serviceOver(t, webMirror(), newLogServer())
	stream, _ := openFollow(t, svc, f)

	f.set(func() { f.moreOn = "FROM objects p" })
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")

	assert.Equal(t, errCut.Reason, endedWith(t, stream))
}

// A mirror watch that ends clean, as a cache's clear ends it, is opened again after
// the retry, and the follow goes on with what its new snapshot shows.
func TestAFollowOutlastsACacheClear(t *testing.T) {
	for _, watch := range []string{"pods", "nodes"} {
		t.Run(watch, func(t *testing.T) {
			s := newLogServer()
			m := webMirror()
			svc, f := serviceOver(t, m, s)
			stream, _ := openFollow(t, svc, f)

			pods := make(chan cluster.ClusterCachedDataObjectWatchFrame)
			var ended chan cluster.ClusterCachedDataObjectWatchFrame
			f.set(func() {
				ended = f.nodes
				if watch == "pods" {
					ended, f.pods = f.pods, pods
				} else {
					f.pods, f.nodes = pods, make(chan cluster.ClusterCachedDataObjectWatchFrame)
				}
			})
			m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
			close(ended)
			pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

			s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
			assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
		})
	}
}

// A follow ends when a mirror watch fails, with the watch's reason.
func TestAFollowEndsWithAFailedWatch(t *testing.T) {
	svc, f := serviceOver(t, webMirror(), newLogServer())
	f.podsErr = errors.New("watch failed")
	stream, _ := openFollow(t, svc, f)
	close(f.pods)
	assert.EqualError(t, failsWith(t, stream), "watch failed")
}

// A pod change the mirror cannot be read over leaves the follow as it is, while
// a mirror that fails ends it, and a cluster whose identity moved is one Kstack no
// longer connects to.
func TestAFollowOnAMirrorThatChangesUnderIt(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}
	stream, err := svc.Follow(t.Context(), "7", view, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

	m.setNodes(nil)
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	s.follow("prod/web-1/app") <- stamped(time.Hour, "still here")
	assert.Equal(t, "still here", recvLine(t, stream, "web-1's line").Message)

	svc, f = serviceOver(t, webMirror(), newLogServer())
	stream, _ = openFollow(t, svc, f)
	f.set(func() { f.queryErr = errors.New("db closed") })
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	assert.EqualError(t, failsWith(t, stream), "db closed")

	svc, f = serviceOver(t, webMirror(), newLogServer())
	stream, _ = openFollow(t, svc, f)
	f.set(func() { f.active.Cache.Spec.ServerUID = "uid-2" })
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	assert.Equal(t, ErrRevoked.Reason, endedWith(t, stream))
}

// A frame with no object moves nothing: the follow resolves the view again only
// for a pod.
func TestAFrameWithNoObjectIsIgnored(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)

	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameModified}
	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A follow cancelled by its context ends clean.
func TestACancelledFollowEndsClean(t *testing.T) {
	svc, f := serviceOver(t, webMirror(), newLogServer())
	stream, cancel := openFollow(t, svc, f)

	cancel()

	assert.NoError(t, failsWith(t, stream))
}

// A container that restarts is read again past its last line once its old stream
// ends, whether the restart reaches the mirror before that end or after it.
func TestAFollowReopensARestartedContainer(t *testing.T) {
	for name, frameFirst := range map[string]bool{"frame first": true, "end first": false} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			m := webMirror()
			svc, f := serviceOver(t, m, s)
			stream, _ := openFollow(t, svc, f)
			s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
			recvLine(t, stream, "web-1's line")

			restarted := func() {
				m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"app"}, restarts: 1}, "Deployment prod/web")
				f.pods <- podFrame(cluster.DeltaFrameModified, "prod/web-1")
			}
			if frameFirst {
				read := testutil.NewProbe[cluster.ClusterCacheHealth](8)
				f.set(func() { f.healthRead = read })
				restarted()
				read.Await(t, "the follow to resolve the view again")
				close(s.follow("prod/web-1/app"))
			} else {
				close(s.follow("prod/web-1/app"))
				awaitClosed(t, s, "prod/web-1/app")
				restarted()
			}

			awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
		})
	}
}

// A container that ends and does not restart is not read again when its pod
// changes for another reason.
func TestAFinishedContainerIsNotReadAgain(t *testing.T) {
	s := newLogServer()
	web1Opens := testutil.NewProbe[string](4)
	s.onOpen = func(key string) {
		if key == "prod/web-1/app" {
			web1Opens.Fire(key)
		}
	}
	m := webMirror()
	m.finish("prod/web-1")
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)
	web1Opens.Await(t, "web-1's first follow")
	close(s.follow("prod/web-1/app"))
	awaitClosed(t, s, "prod/web-1/app")

	f.pods <- podFrame(cluster.DeltaFrameModified, "prod/web-1")
	s.follow("prod/web-2/app") <- stamped(time.Hour+time.Second, "from web-2")

	assert.Equal(t, "from web-2", recvLine(t, stream, "web-2's line").Message)
	// A negative assertion, so a bounded window: a reopen would follow the pod frame's
	// resolve at once, and a hundred retries is far past a local request's round trip.
	testutil.NoRecv(t, web1Opens.Chan(), 100*svc.retry, "a follow of web-1 again")
}

// A pod whose node's labels move is read again past its last line, under the new
// labels, not from the follow's start.
func TestAFollowKeepsItsPlaceWhenANodesLabelsMove(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)
	f.nodes <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
	recvLine(t, stream, "web-1's line")

	moved := mirrorNode{"node-a", "eu", "eu-1c", "linux", "amd64"}
	m.setNodes([]mirrorNode{moved, {"node-b", "eu", "eu-1b", "linux", "arm64"}})
	f.nodes <- nodeFrame(cluster.DeltaFrameModified, moved)

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
	s.follow("prod/web-1/app") <- stamped(time.Hour+2*time.Second, "after")
	assert.Equal(t, "eu-1c", recvLine(t, stream, "web-1's next line").Source.Zone)
}

// A node that leaves the zone a view filters on ends its pod's stream, and one
// that enters it opens one, with no pod frame between.
func TestAFollowTracksItsNodesLabels(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}
	stream, err := svc.Follow(t.Context(), "7", view, nil)
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	a, b := m.nodes[0], m.nodes[1]
	f.nodes <- nodeFrame(cluster.DeltaFrameAdded, a)
	f.nodes <- nodeFrame(cluster.DeltaFrameAdded, b)
	f.nodes <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	recvLine(t, stream, "web-1's line")

	a.zone, b.zone = "eu-1c", "eu-1a"
	m.setNodes([]mirrorNode{a, b})
	f.nodes <- nodeFrame(cluster.DeltaFrameModified, a)
	f.nodes <- nodeFrame(cluster.DeltaFrameModified, b)

	awaitClosed(t, s, "prod/web-1/app")
	s.follow("prod/web-2/app") <- stamped(time.Hour+2*time.Second, "from web-2")
	rec := recvLine(t, stream, "web-2's line")
	assert.Equal(t, "from web-2", rec.Message)
	assert.Equal(t, "eu-1a", rec.Source.Zone)
}

// Under a filter on a label, the nodes watch resolves the view again when its
// snapshot closes, and past it for a node that came, went, moved a label a line
// carries, or whose body did not load; a node whose other fields moved resolves
// nothing.
func TestANodeFrameMovesTheViewOnlyForItsLabels(t *testing.T) {
	a := mirrorNode{"node-a", "eu", "eu-1a", "linux", "amd64"}
	moved := a
	moved.arch = "arm64"
	unloaded := nodeFrame(cluster.DeltaFrameModified, a)
	unloaded.Object.RawJSON = ""
	bookmark := cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}
	for name, tc := range map[string]struct {
		frame cluster.ClusterCachedDataObjectWatchFrame
		moves bool
	}{
		"its snapshot closing": {bookmark, true},
		"the same labels":      {nodeFrame(cluster.DeltaFrameModified, a), false},
		"a label moved":        {nodeFrame(cluster.DeltaFrameModified, moved), true},
		"a new node":           {nodeFrame(cluster.DeltaFrameAdded, mirrorNode{name: "node-b"}), true},
		"a node gone":          {nodeFrame(cluster.DeltaFrameDeleted, a), true},
		"a body not loaded":    {unloaded, true},
		"no object":            {cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameModified}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := &follow{nodes: map[string]RecordSource{}, view: View{Filters: []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}}}
			done := make(chan cluster.ClusterCachedDataObjectWatchFrame)
			close(done)
			f.nodesMoved(nodeFrame(cluster.DeltaFrameAdded, a), done)
			if tc.frame.Type != cluster.DeltaFrameBookmark {
				require.True(t, f.nodesMoved(bookmark, done))
			}

			assert.Equal(t, tc.moves, f.nodesMoved(tc.frame, done))
		})
	}
}

// With no filter on a label, only a node a stream runs on can move the view: a
// frame for any other is not read.
func TestANodeNoStreamRunsOnIsNotRead(t *testing.T) {
	a, b := mirrorNode{"node-a", "eu", "eu-1a", "linux", "amd64"}, mirrorNode{"node-b", "eu", "eu-1b", "linux", "arm64"}
	f := &follow{nodes: map[string]RecordSource{}, nodesSynced: true, containers: map[container]*tracked{}}
	f.track(RecordSource{Pod: "web-1", Container: "app"}).stream = &opened{src: RecordSource{Pod: "web-1", Container: "app", Node: "node-a"}}
	done := make(chan cluster.ClusterCachedDataObjectWatchFrame)
	close(done)

	assert.False(t, f.nodesMoved(nodeFrame(cluster.DeltaFrameAdded, b), done))
	assert.NotContains(t, f.nodes, "node-b")
	assert.True(t, f.nodesMoved(nodeFrame(cluster.DeltaFrameAdded, a), done))
	b.zone = "eu-1c"
	assert.False(t, f.nodesMoved(nodeFrame(cluster.DeltaFrameModified, b), done))
}

// A line a replaced stream still delivers is dropped, and moves nothing its
// replacement resumes from: the replacement reads it again.
func TestAFollowDropsALineFromAReplacedStream(t *testing.T) {
	src := RecordSource{Namespace: "prod", Pod: "web-1", Container: "app", Zone: "eu-1a"}
	old, current := &reading{src: src}, &reading{src: src}
	f := &follow{containers: map[container]*tracked{}}
	f.track(src).stream = &opened{reading: current}
	rec := line("web-1", "app", time.Second, "x")
	rec.Source = src

	assert.False(t, f.accept(headed{rec, old}))
	assert.True(t, f.track(src).last.isZero())
	assert.True(t, f.accept(headed{rec, current}))
	assert.Equal(t, positionOf(rec), f.track(src).last)
}

// A pod that joins while the mirror is syncing is followed once it is ready
// again, with no frame after.
func TestAFollowAsksASyncingMirrorAgain(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)

	read := testutil.NewProbe[cluster.ClusterCacheHealth](8)
	f.set(func() { f.health, f.healthRead = cluster.ClusterCacheHealth{Reason: "Connecting"}, read })
	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	read.Await(t, "the follow to read the syncing cache")
	f.set(func() { f.health = cluster.ClusterCacheHealth{Reason: "Watching"} })

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A pod that leaves the view and comes back is read again, past the last line its
// stream sent.
func TestAFollowReopensAPodThatCameBack(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	stream, _ := openFollow(t, svc, f)
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
	recvLine(t, stream, "web-1's first line")

	m.setPod("prod/web-1", nil, "")
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "prod/web-1")
	awaitClosed(t, s, "prod/web-1/app")
	m.setPod("prod/web-1", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-1")

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
}

// A pod that joins a follow opened after a cursor is read from the cursor, like
// the pods it opened with, not from the moment the follow began.
func TestAFollowAfterACursorReadsANewPodFromIt(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	_, err := svc.Follow(t.Context(), "7", webView, cursor(30*time.Minute))
	require.NoError(t, err)
	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")

	require.Eventually(t, func() bool { return len(s.requests()) == 3 }, testutil.Timeout, time.Millisecond, "web-3's read")
	for _, q := range s.requests() {
		assert.Equal(t, "2026-10-08T10:30:00Z", q.Get("sinceTime"))
	}
}

// A stream the server failed is opened again after the retry, and the follow
// goes on; one the server refused for the log's sake is not.
func TestAFollowReopensADroppedStream(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusInternalServerError, "kubelet restarting"}
	svc, f := serviceOver(t, webMirror(), s)
	stream, _ := openFollow(t, svc, f)
	require.Eventually(t, func() bool { return len(s.requestsOf("prod/web-1/app")) >= 2 }, testutil.Timeout, time.Millisecond, "web-1 asked again")

	s.setRefusal("prod/web-1/app", nil)
	require.Eventually(t, func() bool { return s.followsOpened("prod/web-1/app") == 1 }, testutil.Timeout, time.Millisecond, "web-1 open")
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "back")

	assert.Equal(t, "back", recvLine(t, stream, "web-1's line").Message)
}

// A container whose stream dropped is held for its backoff: a reconcile leaves it
// closed, and keeps the follow open while it is the view's only container, then
// opens it once the hold is over.
func TestAHeldContainerWaitsOutItsBackoff(t *testing.T) {
	s := newLogServer()
	svc, fake := serviceOver(t, webMirror(), s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, err := acquireClaim(ctx, fake, "7", "uid-1")
	require.NoError(t, err)
	f := &follow{
		s: svc, id: "7", view: View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}}}},
		claim: c, target: target{serverUID: "uid-1"}, fan: newFanIn(), containers: map[container]*tracked{},
		nodes: map[string]RecordSource{}, deleted: map[string]string{}, gone: map[string]bool{}, due: make(chan container),
	}
	web1 := container{"prod", "web-1", "app", false}
	f.containers[web1] = &tracked{held: true}

	require.NoError(t, f.reconcile(ctx))
	assert.Nil(t, f.containers[web1].stream)

	f.containers[web1].held = false
	require.NoError(t, f.reconcile(ctx))
	assert.NotNil(t, f.containers[web1].stream)
}

// A dropped stream waits the retry, doubled for each drop in a row up to the cap;
// a stream that ran for the cap, or a line from its successor, starts the count
// again.
func TestADroppingStreamBacksOff(t *testing.T) {
	f := &follow{s: &Service{retry: time.Second, retryCap: time.Minute}, containers: map[container]*tracked{}}
	tr := &tracked{}

	var waits []time.Duration
	for range 8 {
		waits = append(waits, f.backoff(tr, 0))
	}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}, waits)
	assert.Equal(t, time.Second, f.backoff(tr, time.Minute))

	r := &reading{src: RecordSource{Namespace: "prod", Pod: "web-1", Container: "app"}}
	f.containers[containerOf(r.src)] = tr
	tr.stream = &opened{reading: r}
	f.accept(headed{Record{Source: r.src}, r})
	assert.Zero(t, tr.drops)
}

// A stream ends quietly for the log's sake on a status under 500, and is dropped
// on any other failure.
func TestDropped(t *testing.T) {
	assert.False(t, dropped(nil))
	assert.False(t, dropped(&statusError{code: http.StatusBadRequest}))
	assert.True(t, dropped(&statusError{code: http.StatusInternalServerError}))
	assert.True(t, dropped(errors.New("connection reset")))
}

// A follow whose view grows past what Kstack reads at once ends, saying so.
func TestAFollowEndsWhenItsViewGrowsTooLarge(t *testing.T) {
	m := webMirror()
	svc, f := serviceOver(t, m, newLogServer())
	stream, _ := openFollow(t, svc, f)

	manyPods(m, 99)
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/many-0")

	assert.Equal(t, "this view reads 101 containers and Kstack reads at most 100 at once; filter it by node, or name its containers", endedWith(t, stream))
}

// A pod frame resolves the view at once with no settle; with one, the first frame
// arms one wait that the frames after it share, and a resolve clears it.
func TestAPodFrameSettles(t *testing.T) {
	f := &follow{s: &Service{}}
	assert.True(t, f.settle())

	f.s.settle = time.Hour
	assert.False(t, f.settle())
	armed := f.settled
	require.NotNil(t, armed)
	assert.False(t, f.settle())
	assert.Equal(t, armed, f.settled)
}

// A pod frame under a settle is followed once the settle has passed.
func TestAFollowResolvesOnceThePodFramesSettle(t *testing.T) {
	s := newLogServer()
	m := webMirror()
	svc, f := serviceOver(t, m, s)
	svc.settle = time.Millisecond
	stream, _ := openFollow(t, svc, f)

	m.setPod("prod/web-3", &mirrorPod{node: "node-a", containers: []string{"app"}}, "Deployment prod/web")
	f.pods <- podFrame(cluster.DeltaFrameAdded, "prod/web-3")
	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")

	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// Each stream of a fetch reads under its own share of the budget, so a stream whose
// log is large cannot spend what a quiet one needs to read its start.
func TestAFetchSharesItsBudgetPerStream(t *testing.T) {
	s := newLogServer()
	var noise []string
	for i := range 200 {
		noise = append(noise, stamped(time.Duration(i)*time.Millisecond, strings.Repeat("x", 100)))
	}
	s.add("prod/web-1/app", noise...)
	s.add("prod/web-2/app", stamped(0, "match"))
	svc, f := serviceOver(t, webMirror(), s)
	f.chunk(64)
	svc.budget = 8 << 10
	view := webView
	view.Grep = "match"

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"match"}, messages(page.Records))
	require.NotNil(t, page.NextCursor, "web-1 stopped short, so the page goes on from there")
}
