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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// resolveOf is what view resolves to over f.
func resolveOf(t *testing.T, f *fakeService, view View) (target, error) {
	t.Helper()
	return resolve(t.Context(), f, "7", view, false)
}

// A deployment's pods come off the mirror through its replica set, one stream per
// container named, with where each runs.
func TestADeploymentsPodsComeOffTheMirror(t *testing.T) {
	t.Parallel()
	got, err := resolveOf(t, webMirror(t), webView)

	require.NoError(t, err)
	assert.Equal(t, "uid-1", got.serverUID)
	assert.Equal(t, cluster.ClusterCacheID(11), got.cacheID)
	assert.Equal(t, []StreamKey{
		{Namespace: "prod", Pod: "web-1", Container: "app"},
		{Namespace: "prod", Pod: "web-2", Container: "app"},
	}, keysOf(got))
	assert.Equal(t, []Place{
		{Node: "node-a", Region: "eu", Zone: "eu-1a", OS: "linux", Arch: "amd64"},
		{Node: "node-b", Region: "eu", Zone: "eu-1b", OS: "linux", Arch: "arm64"},
	}, placesOf(got))
}

// A workload is walked down through controller references alone: a pod that only
// names it as an owner, without controlling, is not one of its pods.
func TestAWorkloadIsWalkedDownItsControllers(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	notController := webReplicaSet()
	notController["controller"] = false
	f.store.Apply(t, pod("adopted", "uid-adopted", "node-a", notController, container{name: "app"}))

	got, err := resolveOf(t, f, webView)

	require.NoError(t, err)
	assert.Len(t, keysOf(got), 2)
}

// AllContainers reads every container, and previous goes through.
func TestAllContainersReadsEveryContainer(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	f.store.Apply(t, pod("web-1", "uid-web-1", "node-a", nil, container{name: "app", restarted: true}, container{name: "istio-proxy", restarted: true}))

	got, err := resolveOf(t, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", AllContainers: true, Previous: true}}})

	require.NoError(t, err)
	assert.Equal(t, []StreamKey{
		{Namespace: "prod", Pod: "web-1", Container: "app", Previous: true},
		{Namespace: "prod", Pod: "web-1", Container: "istio-proxy", Previous: true},
	}, keysOf(got))
}

// With no container named, each pod reads its own default, as kubectl logs picks
// it: the one its annotation names when that is one of its containers, else its
// first container, never an init container.
func TestEachPodReadsItsOwnDefault(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	annotated := pod("web-2", "uid-web-2", "node-b", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"})
	annotated["metadata"].(map[string]any)["annotations"] = map[string]any{DefaultContainerAnnotation: "istio-proxy"}
	f.store.Apply(t, annotated)
	stale := pod("web-3", "uid-web-3", "node-b", webReplicaSet(), container{name: "app"})
	stale["metadata"].(map[string]any)["annotations"] = map[string]any{DefaultContainerAnnotation: "gone"}
	f.store.Apply(t, withInit(stale, "migrate"))

	got, err := resolveOf(t, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web"}}})

	require.NoError(t, err)
	assert.Equal(t, []StreamKey{
		{Namespace: "prod", Pod: "web-1", Container: "app"},
		{Namespace: "prod", Pod: "web-2", Container: "istio-proxy"},
		{Namespace: "prod", Pod: "web-3", Container: "app"},
	}, keysOf(got))
}

// Init containers come before the others, as they run and as kubectl lists them.
func TestInitContainersComeFirst(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	f.store.Apply(t, withInit(pod("web-1", "uid-web-1", "node-a", nil, container{name: "app"}), "migrate"))

	got, err := resolveOf(t, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", AllContainers: true}}})

	require.NoError(t, err)
	assert.Equal(t, []StreamKey{
		{Namespace: "prod", Pod: "web-1", Container: "migrate"},
		{Namespace: "prod", Pod: "web-1", Container: "app"},
	}, keysOf(got))
}

// CountPods counts the pods the view reads: those a workload controls, and among
// them those with a container named that restarted.
func TestCountPodsCountsTheViewsPods(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	f.store.Apply(t, pod("web-2", "uid-web-2", "node-b", webReplicaSet(), container{name: "app", restarted: true}, container{name: "istio-proxy", restarted: true}))
	notController := webReplicaSet()
	notController["controller"] = false
	f.store.Apply(t, pod("adopted", "uid-adopted", "node-a", notController, container{name: "app", restarted: true}))

	count := func(src ViewSource) PodCount {
		t.Helper()
		var c PodCount
		require.NoError(t, f.CachedData().ReadActive(t.Context(), "7", maxRows, maxBytes, func(q cluster.CacheReading, _ cluster.ActiveCluster) error {
			var err error
			c, err = CountPods(q, src)
			return err
		}))
		return c
	}

	assert.Equal(t, PodCount{Pods: 2, Previous: 1}, count(webView.Sources[0]))
	assert.Equal(t, PodCount{Pods: 2, Previous: 1}, count(ViewSource{Namespace: "prod", Kind: SourceDeployment, Name: "web"}))
	assert.Equal(t, PodCount{Pods: 2}, count(ViewSource{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"db"}}))
}

// A filter on a node's label leaves a pod out before any stream opens.
func TestALabelFilterLeavesAPodOut(t *testing.T) {
	t.Parallel()
	for _, f := range []ViewFilter{
		{Field: FilterZone, Values: []string{"eu-1b"}},
		{Field: FilterArch, Values: []string{"arm64"}},
		{Field: FilterRegion, Values: []string{"eu"}},
		{Field: FilterOS, Values: []string{"linux"}},
	} {
		got, err := resolveOf(t, webMirror(t), View{Sources: webView.Sources, Filters: []ViewFilter{f}})

		require.NoError(t, err, f.Field)
		switch f.Field {
		case FilterZone, FilterArch:
			require.Len(t, keysOf(got), 1, f.Field)
			assert.Equal(t, "web-2", keysOf(got)[0].Pod)
		default:
			assert.Len(t, keysOf(got), 2, f.Field)
		}
	}
}

// A Node filter reads the pod's node and needs no Node object.
func TestANodeFilterNeedsNoNodeObject(t *testing.T) {
	t.Parallel()
	got, err := resolveOf(t, newMirror(t, webObjects()...), View{
		Sources: webView.Sources,
		Filters: []ViewFilter{{Field: FilterNode, Values: []string{"node-a"}}},
	})

	require.NoError(t, err)
	require.Len(t, keysOf(got), 1)
	assert.Equal(t, "web-1", keysOf(got)[0].Pod)
}

// A label filter on a mirror with no nodes is refused, naming every label: by a
// follow at once, and by a page once the file holds nothing the filter keeps.
func TestALabelFilterWithNoNodesIsRefused(t *testing.T) {
	t.Parallel()
	f := newMirror(t, webObjects()...)
	view := View{
		Sources: webView.Sources,
		Filters: []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}, {Field: FilterArch, Values: []string{"arm64"}}},
	}
	const refused = "Kstack cannot read this cluster's nodes, so it cannot filter by zone or arch"

	_, err := resolve(t.Context(), f, "7", view, true)
	assert.EqualError(t, err, refused)

	got, err := resolveOf(t, f, view)
	require.NoError(t, err, "a page looks in the file before it refuses")
	assert.EqualError(t, got.noNodes, refused)
}

// A pod whose node the mirror does not hold, while it holds others, is left out
// by a label filter, and the view reads no node.
func TestAPodOnAnUnknownNodeIsLeftOut(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	got, err := resolveOf(t, f, View{
		Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "job-1"}},
		Filters: []ViewFilter{{Field: FilterOS, Values: []string{"linux"}}},
	})

	require.NoError(t, err)
	assert.Empty(t, keysOf(got))
	assert.Equal(t, []SourceState{Idle}, got.states)
	for _, s := range f.statements {
		assert.NotContains(t, s, "n.name IN")
	}
}

// A previous instance is read only of a container that restarted.
func TestAPreviousInstanceIsReadOnlyWhereItRestarted(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	f.store.Apply(t, pod("web-2", "uid-web-2", "node-b", webReplicaSet(), container{name: "app", restarted: true}, container{name: "istio-proxy"}))

	got, err := resolveOf(t, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}, Previous: true}}})

	require.NoError(t, err)
	assert.Equal(t, []StreamKey{{Namespace: "prod", Pod: "web-2", Container: "app", Previous: true}}, keysOf(got))
}

// Each stream carries the runtime id of the instance it reads, and a container
// whose id a cursor could not carry is not read: the cluster wrote the id, and a
// page must never hand out a cursor it would refuse.
func TestAStreamCarriesItsInstancesID(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	body := pod("odd", "uid-odd", "node-a", nil, container{name: "app"}, container{name: "tabbed"}, container{name: "long"})
	statuses := body["status"].(map[string]any)["containerStatuses"].([]any)
	statuses[1].(map[string]any)["containerID"] = "containerd://a\tb"
	statuses[2].(map[string]any)["containerID"] = "containerd://" + strings.Repeat("a", 244)
	f.store.Apply(t, body)

	got, err := resolveOf(t, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "odd", AllContainers: true}}})

	require.NoError(t, err)
	assert.Equal(t, []StreamKey{{Namespace: "prod", Pod: "odd", Container: "app"}}, keysOf(got))
	assert.Equal(t, containerID("uid-odd", "app", 0), got.streams[0].gen.instance)
}

// Each stream carries the snapshot its file row keeps once the pod is gone: the
// pod's UID and labels, whether the container is the pod's default, when the
// instance read started and finished, and the pod's current instance; a fetch
// learns each Pod source's pod and its current instances too.
func TestResolveCarriesEachStreamsSnapshot(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	body := pod("api", "uid-api", "node-a", nil, container{name: "app", restarted: true}, container{name: "sidecar"})
	body["metadata"].(map[string]any)["labels"] = map[string]any{"app": "api"}
	statuses := body["status"].(map[string]any)["containerStatuses"].([]any)
	app := statuses[0].(map[string]any)
	app["state"] = map[string]any{"running": map[string]any{"startedAt": "2026-10-08T10:05:00Z"}}
	app["lastState"].(map[string]any)["terminated"].(map[string]any)["startedAt"] = "2026-10-08T10:00:00Z"
	app["lastState"].(map[string]any)["terminated"].(map[string]any)["finishedAt"] = "2026-10-08T10:04:59Z"
	f.store.Apply(t, body)
	source := ViewSource{Namespace: "prod", Kind: SourcePod, Name: "api", AllContainers: true}
	previous := source
	previous.Previous = true

	got, err := resolveOf(t, f, View{Sources: []ViewSource{source, previous}})

	require.NoError(t, err)
	require.Len(t, got.streams, 3)
	current, sidecar, last := got.streams[0], got.streams[1], got.streams[2]
	assert.Equal(t, "uid-api", current.gen.uid)
	assert.JSONEq(t, `{"app":"api"}`, current.labels)
	assert.True(t, current.isDefault)
	assert.False(t, sidecar.isDefault)
	assert.Equal(t, at(5*time.Minute).UnixNano(), current.startedAt)
	assert.Zero(t, current.finishedAt)
	assert.Equal(t, containerID("uid-api", "app", 1), current.gen.instance)
	assert.Equal(t, containerID("uid-api", "app", 0), last.gen.instance)
	assert.Equal(t, at(0).UnixNano(), last.startedAt)
	assert.Equal(t, at(5*time.Minute-time.Second).UnixNano(), last.finishedAt)
	assert.Equal(t, map[string]string{"prod/api": "uid-api"}, got.pods)
	assert.Equal(t, map[string]string{"prod/api": "app"}, got.defaults)
	assert.Equal(t, map[string]string{
		"prod/api/app":     containerID("uid-api", "app", 1),
		"prod/api/sidecar": containerID("uid-api", "sidecar", 0),
	}, got.current)
}

// A container waiting to start has no log, so its source is idle until it does;
// one restarting after a crash still has its last instance's, which kubectl logs
// serves meanwhile.
func TestAContainerWaitingToStartIsNotRead(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	f.store.Apply(t, waiting(pod("creating", "uid-creating", "node-a", nil, container{name: "app"})))
	f.store.Apply(t, waiting(pod("crashing", "uid-crashing", "node-a", nil, container{name: "app", restarted: true})))

	got, err := resolveOf(t, f, View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "creating"},
		{Namespace: "prod", Kind: SourcePod, Name: "crashing"},
	}})

	require.NoError(t, err)
	assert.Equal(t, []SourceState{Idle, Reading}, got.states)
	assert.Equal(t, []StreamKey{{Namespace: "prod", Pod: "crashing", Container: "app"}}, keysOf(got))
}

// A container named twice across sources is read once.
func TestAContainerIsReadOnce(t *testing.T) {
	t.Parallel()
	got, err := resolveOf(t, webMirror(t), View{Sources: []ViewSource{
		webView.Sources[0],
		{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}},
	}})

	require.NoError(t, err)
	assert.Len(t, keysOf(got), 2)
}

// A view follows each source as its object and pods come and go, so none is
// refused for reading nothing now: each says whether its object is missing or
// it has nothing to read yet.
func TestEachSourceSaysWhatItHoldsNow(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	f.store.Mirror(t, "apps/v1", "StatefulSet")
	f.store.Apply(t, workload("apps/v1", "Deployment", "idle", "dep-idle", nil))
	f.store.Apply(t, pod("bare", "uid-bare", "", nil))
	sources := []struct {
		src  ViewSource
		want SourceState
	}{
		{ViewSource{Namespace: "prod", Kind: SourcePod, Name: "web-1"}, Reading},
		{ViewSource{Namespace: "prod", Kind: SourcePod, Name: "nope"}, Missing},
		{ViewSource{Namespace: "prod", Kind: SourceStatefulSet, Name: "web"}, Missing},
		{ViewSource{Namespace: "prod", Kind: SourceDeployment, Name: "idle"}, Idle},
		{ViewSource{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"api"}}, Idle},
		{ViewSource{Namespace: "prod", Kind: SourceDeployment, Name: "web", Previous: true}, Idle},
		{ViewSource{Namespace: "prod", Kind: SourcePod, Name: "bare"}, Idle},
		{ViewSource{Namespace: "prod", Kind: SourcePod, Name: "web-1", Previous: true}, Idle},
	}
	var view View
	var want []SourceState
	for _, s := range sources {
		view.Sources = append(view.Sources, s.src)
		want = append(want, s.want)
	}

	got, err := resolveOf(t, f, view)

	require.NoError(t, err)
	assert.Equal(t, want, got.states)
	assert.Equal(t, []StreamKey{{Namespace: "prod", Pod: "web-1", Container: "app"}}, keysOf(got))
}

// A kind the cache does not mirror is refused, since no object of it could ever
// appear to follow, and so is a cache that mirrors no pods.
func TestAnUnmirroredKindIsRefused(t *testing.T) {
	t.Parallel()
	_, err := resolveOf(t, webMirror(t), View{Sources: []ViewSource{webView.Sources[0], {Namespace: "prod", Kind: SourceJob, Name: "nightly"}}})
	assert.EqualError(t, err, "Kstack's cache of this cluster holds no jobs, so it cannot follow jobs/nightly in prod")
	var unmirrored Unmirrored
	require.ErrorAs(t, err, &unmirrored)
	assert.Equal(t, 1, unmirrored.Source)

	_, err = resolveOf(t, newMirror(t, workload("apps/v1", "Deployment", "web", "dep-web", nil)), webView)
	require.ErrorAs(t, err, &unmirrored)
	assert.Equal(t, -1, unmirrored.Source)
}

// A kind Kstack does not know is refused, never read as matching nothing.
func TestAnUnknownKindIsRefused(t *testing.T) {
	t.Parallel()
	_, err := resolveOf(t, webMirror(t), View{Sources: []ViewSource{{Namespace: "prod", Kind: "cronjobs", Name: "web"}}})
	assert.EqualError(t, err, `Kstack cannot read the logs of a "cronjobs"`)
}

// A filter field Kstack does not know is refused, never read as matching nothing.
func TestAnUnknownFilterFieldIsRefused(t *testing.T) {
	t.Parallel()
	_, err := resolveOf(t, webMirror(t), View{Sources: webView.Sources, Filters: []ViewFilter{{Field: "rack", Values: []string{"a"}}}})
	assert.EqualError(t, err, `Kstack cannot filter by "rack"`)
}

// A cluster with no cache, or a cache still syncing, is refused as the tool
// refuses it, and so is a cluster gone or moved under the read, or a cache gone
// under one of its statements.
func TestACacheNotReadyIsRefused(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]func(f *fakeService){
		"no cache":       func(f *fakeService) { f.active.Cache = nil },
		"no health":      func(f *fakeService) { f.noHealth = true },
		"record gone":    func(f *fakeService) { f.readActiveErr = cluster.ErrNotFound },
		"identity moved": func(f *fakeService) { f.readActiveErr = cluster.ErrIdentityMoved },
		"no cache file":  func(f *fakeService) { f.noQuery = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := webMirror(t)
			set(f)
			_, err := resolveOf(t, f, webView)
			assert.ErrorIs(t, err, ErrNoCache)
		})
	}

	f := webMirror(t)
	f.health = cluster.ClusterCacheHealth{Reason: "Connecting"}
	_, err := resolveOf(t, f, View{})
	assert.ErrorIs(t, err, ErrSyncing)
}

// A read of the cluster that fails is the failure, whichever statement it fails on.
func TestAFailedReadIsTheFailure(t *testing.T) {
	t.Parallel()
	// web1Elsewhere reads a container web-1 lacks, so only the pod's own reading
	// asks where it runs.
	web1Elsewhere := podSource("web-1", false, "db")
	// starting opens with a previous instance, which never starts, so the check
	// asks of the source after it.
	starting := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-2", Previous: true}, webView.Sources[0]}}
	nowhere := webView
	nowhere.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"nowhere"}}}
	for name, c := range map[string]struct {
		set    func(f *fakeService)
		view   View
		follow bool
	}{
		"reading":         {set: func(f *fakeService) { f.readActiveErr = errors.New("db closed") }, view: webView},
		"health":          {set: func(f *fakeService) { f.healthErr = errors.New("db closed") }, view: webView},
		"every query":     {set: func(f *fakeService) { f.queryErr = errors.New("db closed") }, view: webView},
		"the node query":  {set: func(f *fakeService) { f.queryErr, f.queryErrOn = errors.New("db closed"), "kind = 'Node'" }, view: webView},
		"the kinds query": {set: func(f *fakeService) { f.queryErr, f.queryErrOn = errors.New("db closed"), "FROM kinds" }, view: webView},
		"a pod source's query": {set: func(f *fakeService) {
			f.queryErr, f.queryErrOn = errors.New("db closed"), "json_group_object"
		}, view: web1View},
		"a pod source's node": {set: func(f *fakeService) { f.queryErr, f.queryErrOn = errors.New("db closed"), "kind = 'Node'" }, view: web1Elsewhere},
		"the nodes listed": {set: func(f *fakeService) {
			f.queryErr, f.queryErrOn = errors.New("db closed"), "n.kind = 'Node' LIMIT 1"
		}, view: nowhere},
		"the starting query": {set: func(f *fakeService) {
			f.queryErr, f.queryErrOn = errors.New("db closed"), "coalesce(c.last_container_id, '') = '' LIMIT 1"
		}, view: starting, follow: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := webMirror(t)
			c.set(f)
			_, err := resolve(t.Context(), f, "7", c.view, c.follow)
			assert.EqualError(t, err, "db closed")
		})
	}
}

// CountPods refuses a kind it cannot read, and fails with the mirror.
func TestCountPodsFailsWithItsRead(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	count := func(src ViewSource) error {
		return f.CachedData().ReadActive(t.Context(), "7", maxRows, maxBytes, func(q cluster.CacheReading, _ cluster.ActiveCluster) error {
			_, err := CountPods(q, src)
			return err
		})
	}

	assert.EqualError(t, count(ViewSource{Namespace: "prod", Kind: "Gadget", Name: "web"}), `Kstack cannot read the logs of a "Gadget"`)
	f.queryErr = errors.New("db closed")
	assert.EqualError(t, count(webView.Sources[0]), "db closed")
}

// An answer the mirror cut short is refused rather than read as every row: the
// nodes, or a source's pods.
func TestACutAnswerIsRefused(t *testing.T) {
	t.Parallel()
	for name, on := range map[string]string{"nodes": "kind = 'Node'", "pods": "kind = 'Pod'"} {
		t.Run(name, func(t *testing.T) {
			f := webMirror(t)
			f.moreOn = on
			_, err := resolveOf(t, f, webView)
			assert.EqualError(t, err, "the cluster's cache answered more than Kstack reads at once")
		})
	}
}

// A view's containers and filters narrow the pods statement itself, so the row cap
// counts only what the view reads, and the nodes statement reads only the nodes
// its streams run on.
func TestTheFiltersNarrowTheStatement(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	got, err := resolveOf(t, f, View{
		Sources: webView.Sources,
		Filters: []ViewFilter{
			{Field: FilterNode, Values: []string{"node-a"}},
			{Field: FilterZone, Values: []string{"eu-1a", "eu-1b"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, keysOf(got), 1)

	n := len(f.statements)
	pods := f.statements[n-2]
	assert.Contains(t, pods, "c.name IN ('app')")
	assert.Contains(t, pods, "p.node IN ('node-a')")
	assert.Contains(t, pods, "l.key = 'topology.kubernetes.io/zone' AND l.value IN ('eu-1a', 'eu-1b')")
	assert.Contains(t, f.statements[n-1], "n.name IN ('node-a')")
}

// A view of more containers than Kstack reads at once is refused, reading no more
// than one past the limit and no node; one at the limit is not.
func TestAViewOfTooManyContainersIsRefused(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	addPods(t, f, maxStreams-1)
	_, err := resolveOf(t, f, webView)
	require.EqualError(t, err, "this view reads more than 100 containers, the most Kstack reads at once; filter it by node, or narrow it to fewer sources, containers or pods")
	pods := f.statements[len(f.statements)-1]
	assert.True(t, strings.HasSuffix(pods, "LIMIT 101"), pods)

	f = webMirror(t)
	addPods(t, f, maxStreams-2)
	got, err := resolveOf(t, f, webView)
	require.NoError(t, err)
	assert.Len(t, got.streams, maxStreams)
}

// Too many containers across sources are refused too.
func TestTooManyAcrossSourcesIsRefused(t *testing.T) {
	t.Parallel()
	f := webMirror(t)
	addPods(t, f, maxStreams-2)
	_, err := resolveOf(t, f, View{Sources: []ViewSource{webView.Sources[0], {Namespace: "prod", Kind: SourcePod, Name: "job-1"}}})
	assert.ErrorIs(t, err, ErrTooMany)
}
