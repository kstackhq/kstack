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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// A deployment's last lines come back in time order, with the cursor of the
// oldest.
func TestFetchAnswersADeploymentsLastLines(t *testing.T) {
	s := newLogServer()
	for i := range 15 {
		pod := "web-" + strconv.Itoa(1+i%2)
		s.add("prod/"+pod+"/app", stamped(time.Duration(i)*time.Second, strconv.Itoa(i)))
	}
	svc, _ := serviceOver(t, webMirror(t), s)

	page, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchTail, Limit: 5})

	require.NoError(t, err)
	assert.Equal(t, []string{"10", "11", "12", "13", "14"}, messages(page.Records))
	assert.Equal(t, lineCursor("web-1", "app", 10*time.Second, 0), page.NextCursor)
	assert.Equal(t, "web-1", page.Records[0].Stream.Pod)
	assert.Equal(t, "eu-1a", page.Records[0].Place.Zone)
}

// A head page after a cursor starts past it, and grep keeps the matching lines.
func TestFetchReadsForwardAfterACursor(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "error a"), stamped(time.Second, "ok"), stamped(2*time.Second, "error b"), stamped(3*time.Second, "error c"))
	svc, _ := serviceOver(t, webMirror(t), s)
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
	svc, _ := serviceOver(t, webMirror(t), s)

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
	svc, _ := serviceOver(t, webMirror(t), s)

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
			s, objects, all := randomLogs(rng)
			svc, f := serviceOver(t, newMirror(t, objects...), s)
			svc.budget = int64(2000 + rng.Intn(8000))
			f.chunk(1 + rng.Intn(64))
			view := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", AllContainers: true}}}
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
				all = append(all, Record{Timestamp: at(d), Message: msg, Stream: stream, Place: place, Ordinal: ordinal})
			}
		}
	}
	slices.SortStableFunc(all, compareLines)
	return s, objects, all
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
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	for _, limit := range []int{0, 501} {
		_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: limit})
		var r Refusal
		assert.True(t, errors.As(err, &r), limit)
	}
}

// A cursor that names no moment is refused, whichever argument carries it, and so
// is a grep that is no regular expression.
func TestFetchRefusesABadCursor(t *testing.T) {
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
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
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	f.active.Cache = nil
	_, err := svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrNoCache)

	svc, f = serviceOver(t, webMirror(t), newLogServer())
	f.acquireErr = cluster.ErrNotConnectable
	_, err = svc.Fetch(t.Context(), "7", webView, FetchQuery{Mode: FetchHead, Limit: 10})
	assert.ErrorIs(t, err, ErrRevoked)
}

// A view no pod passes reads nothing, and answers an empty page.
func TestFetchOfAViewWithNoContainerIsEmpty(t *testing.T) {
	s := newLogServer()
	svc, _ := serviceOver(t, webMirror(t), s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"nowhere"}}}

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, Page{Records: []Record{}, Skipped: []string{}}, page)
	assert.Empty(t, s.requests())
}

// A view of more containers than Kstack reads at once is refused before any is
// read.
func TestAFetchOfTooManyContainersIsRefused(t *testing.T) {
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
	require.NotNil(t, page.NextCursor, "web-1 stopped short, so the page goes on from there")
}
