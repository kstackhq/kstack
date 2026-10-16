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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// podSource is a view of the pod name in prod, reading containers, every one with
// none named and all set, previous when asked.
func podSource(name string, previous bool, containers ...string) View {
	return View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: name, Containers: containers, Previous: previous}}}
}

// idsOf is each record's instance.
func idsOf(recs []Record) []string {
	out := []string{}
	for _, r := range recs {
		out = append(out, r.ContainerID)
	}
	return out
}

// thirdRun is the pod web-1 of web's replica set whose app container runs its
// third instance, the second before it.
func thirdRun() map[string]any {
	body := pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app", restarted: true}, container{name: "istio-proxy"})
	app := body["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)
	app["containerID"] = containerID("uid-web-1", "app", 2)
	app["lastState"].(map[string]any)["terminated"].(map[string]any)["containerID"] = containerID("uid-web-1", "app", 1)
	return body
}

// earlier is web-1's app instance n as an earlier page filed it, started n minutes
// past 10:00, with one line at that moment saying "a<n>".
func earlier(t *testing.T, svc *Service, f *fakeService, n int) {
	t.Helper()
	row := rowOf("web-1", "app")
	row.ContainerID, row.StartedAt = containerID("uid-web-1", "app", n), at(time.Duration(n)*time.Minute).UnixNano()
	line := logstore.Line{Pos: logstore.Pos{TS: at(time.Duration(n) * time.Minute).UnixNano()}, Message: "a" + strconv.Itoa(n)}
	seed(t, svc, f, row, logstore.Fill{Lines: []logstore.Line{line}, Head: true, AtEnd: true})
}

// A deleted pod's streams, lines and place survive it: a view of the pod reads them
// from the file, and a node filter still finds them by the node they ran on.
func TestADeletedPodsLinesSurvive(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	svc, f := serviceOver(t, webMirror(t), s)
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)

	f.store.Delete(t, pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"}))
	reads := len(s.requests())
	view := web1View
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}
	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "2"}, messages(page.Records))
	assert.Equal(t, Place{Node: "node-a", Region: "eu", Zone: "eu-1a", OS: "linux", Arch: "amd64"}, page.Records[0].Place)
	assert.True(t, page.Records[0].Stream.Previous, "a gone pod runs no instance")
	assert.False(t, page.After.More)
	assert.Len(t, s.requests(), reads, "no backend serves a gone pod")

	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1b"}}}
	page, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
}

// A pod of a deleted pod's name with another UID is another pod: a view of it reads
// its own streams alone.
func TestAPodOfTheSameNameIsAnotherPod(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "old"))
	svc, f := serviceOver(t, webMirror(t), s)
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)

	f.store.Delete(t, pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app"}))
	f.store.Apply(t, pod("web-1", "uid-web-1b", "node-a", webReplicaSet(), container{name: "app"}))
	s.clear("prod/web-1/app")
	s.add("prod/web-1/app", stamped(time.Minute, "new"))
	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"new"}, messages(page.Records))
	assert.Equal(t, []string{containerID("uid-web-1b", "app", 0)}, idsOf(page.Records))
}

// A plain view reads the running instance from the kubelet and the instances before
// it from the file, with no fill; a view with previous reads the last from the
// kubelet and the older ones from the file; a gone pod reads the same rows either
// way.
func TestAPlainViewReadsEarlierInstancesFromTheFile(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(2*time.Minute, "a2"))
	s.add("prod/web-1/app/previous", stamped(time.Minute, "a1"))
	f := newMirror(t, append([]map[string]any{thirdRun()}, webNodes()...)...)
	svc, _ := serviceOver(t, f, s)
	earlier(t, svc, f, 0)

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a2"}, messages(page.Records))
	assert.Empty(t, s.requestsOf("prod/web-1/app/previous"))

	page, err = svc.Fetch(t.Context(), "7", podSource("web-1", true, "app"), FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a1"}, messages(page.Records))
	assert.Equal(t, []bool{true, true}, []bool{page.Records[0].Stream.Previous, page.Records[1].Stream.Previous})

	f.store.Delete(t, thirdRun())
	for _, previous := range []bool{false, true} {
		page, err = svc.Fetch(t.Context(), "7", podSource("web-1", previous, "app"), FetchQuery{Mode: FetchHead, Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, []string{"a0", "a1", "a2"}, messages(page.Records), previous)
	}
}

// A view naming no container of a gone pod reads the rows marked its default.
func TestADefaultViewOfAGonePodReadsItsDefault(t *testing.T) {
	t.Parallel()
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	seed(t, svc, f, rowOf("old-1", "app"), logstore.Fill{Lines: fileLines(0)})
	seed(t, svc, f, rowOf("old-1", "istio-proxy"), logstore.Fill{Lines: fileLines(time.Second)})

	page, err := svc.Fetch(t.Context(), "7", podSource("old-1", false), FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"0"}, messages(page.Records))
	assert.Equal(t, "app", page.Records[0].Stream.Container)
}

// The prompt's two-source view, a pod named plain and with previous, reads the
// current and the last instance once each from the kubelet and each older one once
// from the file: every line goes out once, and each stream answers once about more.
func TestTheTwoSourceViewReadsEachInstanceOnce(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(2*time.Minute, "a2"))
	s.add("prod/web-1/app/previous", stamped(time.Minute, "a1"))
	f := newMirror(t, append([]map[string]any{thirdRun()}, webNodes()...)...)
	svc, _ := serviceOver(t, f, s)
	earlier(t, svc, f, 0)
	view := podSource("web-1", false, "app")
	view.Sources = append(view.Sources, podSource("web-1", true, "app").Sources...)

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a1", "a2"}, messages(page.Records))
	assert.Len(t, s.requestsOf("prod/web-1/app"), 1)
	assert.Len(t, s.requestsOf("prod/web-1/app/previous"), 1)
	assert.False(t, page.After.More)
}

// A container's file rows beside the resolve's are its newest maxInstances, by when
// each started: a pod that crash-looped for days holds hundreds.
func TestAContainersFileRowsAreCapped(t *testing.T) {
	t.Parallel()
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	for n := range maxInstances + 2 {
		earlier(t, svc, f, n)
	}
	f.store.Delete(t, pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app"}))

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, []string{"a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}, messages(page.Records))
}

// An instance read as the current one and, after a restart, as the previous one is
// one row with one copy of each line.
func TestAnInstanceReadAsCurrentThenPreviousHasOneRow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"))
	svc, f := serviceOver(t, webMirror(t), s)
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)

	restartWeb1(t, f, s, stamped(time.Minute, "a1"))
	page, err := svc.Fetch(t.Context(), "7", podSource("web-1", true, "app"), FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"a0"}, messages(page.Records))
	rows, err := fileOf(t, svc, f).PodStreams(t.Context(), "prod", "web-1", "")
	require.NoError(t, err)
	assert.Len(t, rows, 1, "the file holds instance 0 alone: nothing read instance 1 yet")
	assert.Len(t, chunksOf(t, svc, f, "web-1", "app"), 1)
}

// A view naming no container of a pod the mirror holds reads the pod's default as
// the mirror names it now: a container the file marked default before the pod's
// annotation moved is not read, and once the pod is gone, the file's mark has moved
// with it.
func TestADefaultViewFollowsTheLivePodsDefault(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "app"))
	s.add("prod/web-1/istio-proxy", stamped(time.Second, "proxy"))
	svc, f := serviceOver(t, webMirror(t), s)
	page, err := svc.Fetch(t.Context(), "7", podSource("web-1", false), FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"app"}, messages(page.Records))

	moved := pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"})
	moved["metadata"].(map[string]any)["annotations"] = map[string]any{DefaultContainerAnnotation: "istio-proxy"}
	f.store.Apply(t, moved)
	page, err = svc.Fetch(t.Context(), "7", podSource("web-1", false), FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"proxy"}, messages(page.Records))

	f.store.Delete(t, moved)
	page, err = svc.Fetch(t.Context(), "7", podSource("web-1", false), FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"proxy"}, messages(page.Records), "the gone pod")
}

// The node filters run over a held pod's file rows by where the pod runs now: a node
// relabelled since the rows were filed leaves them out as it leaves the pod out.
func TestAHeldPodsFileRowsAreFilteredByItsPlaceNow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"))
	f := newMirror(t, append([]map[string]any{thirdRun()}, webNodes()...)...)
	svc, _ := serviceOver(t, f, s)
	earlier(t, svc, f, 0)

	f.store.Apply(t, node("node-a", "eu", "eu-1b", "linux", "amd64"))
	view := web1View
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}
	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Empty(t, page.Records)
}

// A filter on a node label over a mirror with no nodes is refused only when the file
// holds nothing the filter keeps: a gone pod's rows carry the place they ran in.
func TestAGonePodIsFilteredWithNoNodesLeft(t *testing.T) {
	t.Parallel()
	f := newMirror(t)
	f.store.Mirror(t, "v1", "Pod")
	svc, _ := serviceOver(t, f, newLogServer())
	seed(t, svc, f, rowOf("old-1", "app"), logstore.Fill{Lines: fileLines(0)})
	view := podSource("old-1", false)
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}

	page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"0"}, messages(page.Records))

	view.Filters[0].Values = []string{"eu-1b"}
	_, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
	var noNodes NoNodes
	assert.ErrorAs(t, err, &noNodes)
}

// A restart the mirror reports late files the new instance's first lines under the
// old one, since the check passed on the lagging mirror; once a resolve sets the old
// instance's finished_at, the trim takes them from it, and each instance's lines
// are under its own row, every line once.
func TestARestartTheMirrorReportsLateIsTrimmed(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"), stamped(time.Second, "a1"))
	svc, f := serviceOver(t, webMirror(t), s)
	s.mu.Lock()
	s.logs["prod/web-1/app/previous"] = s.logs["prod/web-1/app"]
	s.logs["prod/web-1/app"] = []string{stamped(6*time.Second, "b6"), stamped(7*time.Second, "b7")}
	s.mu.Unlock()

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"b6", "b7"}, messages(page.Records), "filed under the instance the mirror still names")

	restarted := pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app", restarted: true}, container{name: "istio-proxy"})
	app := restarted["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)
	app["lastState"].(map[string]any)["terminated"].(map[string]any)["finishedAt"] = "2026-10-08T10:00:05Z"
	f.store.Apply(t, restarted)
	previous := web1View
	previous.Sources = []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}, Previous: true}}
	page, err = svc.Fetch(t.Context(), "7", previous, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a1"}, messages(page.Records))

	page, err = svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "a1", "b6", "b7"}, messages(page.Records))
	var ids []string
	for _, rec := range page.Records {
		ids = append(ids, rec.ContainerID)
	}
	a, b := containerID("uid-web-1", "app", 0), containerID("uid-web-1", "app", 1)
	assert.Equal(t, []string{a, a, b, b}, ids)
}

// A restart the mirror reports late is trimmed even when no view reads the
// previous instance: a plain view's resolve learns when the instance before the
// running one finished, and the file's row of it is trimmed before it is read.
func TestALateRestartIsTrimmedUnderAPlainView(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"), stamped(time.Second, "a1"))
	svc, f := serviceOver(t, webMirror(t), s)
	s.mu.Lock()
	s.logs["prod/web-1/app/previous"] = s.logs["prod/web-1/app"]
	s.logs["prod/web-1/app"] = []string{stamped(6*time.Second, "b6"), stamped(7*time.Second, "b7")}
	s.mu.Unlock()
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)

	restarted := pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app", restarted: true}, container{name: "istio-proxy"})
	app := restarted["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)
	app["lastState"].(map[string]any)["terminated"].(map[string]any)["finishedAt"] = "2026-10-08T10:00:05Z"
	f.store.Apply(t, restarted)
	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"b6", "b7"}, messages(page.Records))
	b := containerID("uid-web-1", "app", 1)
	assert.Equal(t, []string{b, b}, idsOf(page.Records))
}

// A page labels a stream as its view reads it, as the follow does: a waiting
// container's log is the instance before, which a view with no Previous reads as the
// container's own, and one with Previous as its previous instance.
func TestAPageLabelsAWaitingContainerAsItsViewReadsIt(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/crash/app", stamped(0, "boom"))
	s.add("prod/crash/app/previous", stamped(0, "boom"))
	f := newMirror(t, append(webObjects(), waiting(pod("crash", "uid-crash", "node-a", nil, container{name: "app", restarted: true})))...)
	svc, _ := serviceOver(t, f, s)

	for _, previous := range []bool{false, true} {
		page, err := svc.Fetch(t.Context(), "7", podSource("crash", previous, "app"), FetchQuery{Mode: FetchHead, Limit: 10})
		require.NoError(t, err)
		require.Equal(t, []string{"boom"}, messages(page.Records))
		assert.Equal(t, previous, page.Records[0].Stream.Previous)
	}
}

// Each node filter reads a gone pod's rows by the place it ran in, and a page past
// the last line of a gone pod is empty, not refused.
func TestEachNodeFilterReadsAGonePodsPlace(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	svc, f := serviceOver(t, webMirror(t), s)
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	f.store.Delete(t, pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"}))

	for field, values := range map[FilterField][2]string{
		FilterNode:   {"node-a", "node-b"},
		FilterRegion: {"eu", "us"},
		FilterOS:     {"linux", "windows"},
		FilterArch:   {"amd64", "arm64"},
	} {
		view := web1View
		view.Filters = []ViewFilter{{Field: field, Values: []string{values[0]}}}
		page, err := svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
		require.NoError(t, err, field)
		assert.Equal(t, []string{"0", "1", "2"}, messages(page.Records), field)

		view.Filters[0].Values = []string{values[1]}
		page, err = svc.Fetch(t.Context(), "7", view, FetchQuery{Mode: FetchHead, Limit: 10})
		require.NoError(t, err, field)
		assert.Empty(t, page.Records, field)
	}

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	page, err = svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Cursor: page.After.Cursor, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Empty(t, page.Skipped)
}
