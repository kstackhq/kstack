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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// webMirror is a deployment web in prod with two pods, each with app and
// istio-proxy, on two nodes in two zones, and a lone pod job-1 on no node yet.
func webMirror() *mirror {
	return &mirror{
		pods: map[string]mirrorPod{
			"prod/web-1": {node: "node-a", containers: []string{"app", "istio-proxy"}},
			"prod/web-2": {node: "node-b", containers: []string{"app", "istio-proxy"}},
			"prod/job-1": {containers: []string{"main"}},
		},
		nodes: []mirrorNode{
			{"node-a", "eu", "eu-1a", "linux", "amd64"},
			{"node-b", "eu", "eu-1b", "linux", "arm64"},
		},
		under: map[string][]string{"Deployment prod/web": {"prod/web-1", "prod/web-2"}},
	}
}

// resolveOf is what view resolves to over m.
func resolveOf(t *testing.T, m *mirror, view View) (target, error) {
	t.Helper()
	return resolve(t.Context(), m.service(), "7", view)
}

// A deployment's pods come off the mirror, one stream per container named, with
// where each runs.
func TestADeploymentsPodsComeOffTheMirror(t *testing.T) {
	got, err := resolveOf(t, webMirror(), View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}}})

	require.NoError(t, err)
	assert.Equal(t, "uid-1", got.serverUID)
	assert.Equal(t, []RecordSource{
		{Namespace: "prod", Pod: "web-1", Container: "app", Node: "node-a", Region: "eu", Zone: "eu-1a", OS: "linux", Arch: "amd64"},
		{Namespace: "prod", Pod: "web-2", Container: "app", Node: "node-b", Region: "eu", Zone: "eu-1b", OS: "linux", Arch: "arm64"},
	}, sourcesOf(got))
}

// An empty container list reads every container, and previous goes through.
func TestAnEmptyContainerListReadsEveryContainer(t *testing.T) {
	got, err := resolveOf(t, webMirror(), View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Previous: true}}})

	require.NoError(t, err)
	require.Len(t, sourcesOf(got), 2)
	assert.Equal(t, "app", sourcesOf(got)[0].Container)
	assert.Equal(t, "istio-proxy", sourcesOf(got)[1].Container)
	assert.True(t, sourcesOf(got)[0].Previous)
}

// A filter on a node's label leaves a pod out before any stream opens.
func TestALabelFilterLeavesAPodOut(t *testing.T) {
	for _, f := range []ViewFilter{
		{Field: FilterZone, Values: []string{"eu-1b"}},
		{Field: FilterArch, Values: []string{"arm64"}},
		{Field: FilterRegion, Values: []string{"eu"}},
		{Field: FilterOS, Values: []string{"linux"}},
	} {
		got, err := resolveOf(t, webMirror(), View{
			Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}},
			Filters: []ViewFilter{f},
		})

		require.NoError(t, err, f.Field)
		switch f.Field {
		case FilterZone, FilterArch:
			require.Len(t, sourcesOf(got), 1, f.Field)
			assert.Equal(t, "web-2", sourcesOf(got)[0].Pod)
		default:
			assert.Len(t, sourcesOf(got), 2, f.Field)
		}
	}
}

// A Node filter reads the pod's node and needs no Node object.
func TestANodeFilterNeedsNoNodeObject(t *testing.T) {
	m := webMirror()
	m.nodes = nil

	got, err := resolveOf(t, m, View{
		Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}},
		Filters: []ViewFilter{{Field: FilterNode, Values: []string{"node-a"}}},
	})

	require.NoError(t, err)
	require.Len(t, sourcesOf(got), 1)
	assert.Equal(t, "web-1", sourcesOf(got)[0].Pod)
}

// A zone filter on a mirror with no nodes is refused, saying why.
func TestAZoneFilterWithNoNodesIsRefused(t *testing.T) {
	m := webMirror()
	m.nodes = nil

	_, err := resolveOf(t, m, View{
		Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web"}},
		Filters: []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}},
	})

	assert.EqualError(t, err, "Kstack cannot read this cluster's nodes, so it cannot filter by zone")
}

// A pod whose node the mirror does not hold, while it holds others, is left out
// by a label filter.
func TestAPodOnAnUnknownNodeIsLeftOut(t *testing.T) {
	got, err := resolveOf(t, webMirror(), View{
		Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "job-1"}},
		Filters: []ViewFilter{{Field: FilterOS, Values: []string{"linux"}}},
	})

	require.NoError(t, err)
	assert.Empty(t, sourcesOf(got))
}

// A container named twice across sources is read once.
func TestAContainerIsReadOnce(t *testing.T) {
	got, err := resolveOf(t, webMirror(), View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}},
		{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}},
	}})

	require.NoError(t, err)
	assert.Len(t, sourcesOf(got), 2)
}

// A cluster with no cache, or a cache still syncing, is refused as the tool
// refuses it, and so is a cluster gone or moved under the read, or a cache gone
// under one of its statements.
func TestACacheNotReadyIsRefused(t *testing.T) {
	for name, set := range map[string]func(f *fakeService){
		"no cache":       func(f *fakeService) { f.active.Cache = nil },
		"no health":      func(f *fakeService) { f.noHealth = true },
		"record gone":    func(f *fakeService) { f.readActiveErr = cluster.ErrNotFound },
		"identity moved": func(f *fakeService) { f.readActiveErr = cluster.ErrIdentityMoved },
		"no cache file":  func(f *fakeService) { f.noQuery = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := webMirror().service()
			set(f)
			_, err := resolve(t.Context(), f, "7", webView)
			assert.ErrorIs(t, err, ErrNoCache)
		})
	}

	f := webMirror().service()
	f.health = cluster.ClusterCacheHealth{Reason: "Connecting"}
	_, err := resolve(t.Context(), f, "7", View{})
	assert.ErrorIs(t, err, ErrSyncing)
}

// A read of the cluster that fails is the failure.
func TestAFailedReadIsTheFailure(t *testing.T) {
	for name, set := range map[string]func(f *fakeService){
		"reading":    func(f *fakeService) { f.readActiveErr = errors.New("db closed") },
		"health":     func(f *fakeService) { f.healthErr = errors.New("db closed") },
		"nodes":      func(f *fakeService) { f.queryErr = errors.New("db closed") },
		"pods alone": func(f *fakeService) { f.queryErr, f.queryErrOn = errors.New("db closed"), "kind = 'Pod'" },
	} {
		t.Run(name, func(t *testing.T) {
			f := webMirror().service()
			set(f)
			_, err := resolve(t.Context(), f, "7", webView)
			assert.EqualError(t, err, "db closed")
		})
	}
}

// An answer the mirror cut short is refused rather than read as every row: the
// nodes, or a source's pods.
func TestACutAnswerIsRefused(t *testing.T) {
	for name, on := range map[string]string{"nodes": "kind = 'Node'", "pods": "kind = 'Pod'"} {
		t.Run(name, func(t *testing.T) {
			f := webMirror().service()
			f.moreOn = on
			_, err := resolve(t.Context(), f, "7", webView)
			assert.EqualError(t, err, "this view names more containers or nodes than Kstack reads at once")
		})
	}
}

// A view's containers and filters narrow the pods statement itself, so the row cap
// counts only what the view reads, and the nodes statement reads only the nodes
// its streams run on.
func TestTheFiltersNarrowTheStatement(t *testing.T) {
	f := webMirror().service()
	view := View{
		Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}},
		Filters: []ViewFilter{
			{Field: FilterNode, Values: []string{"node-a"}},
			{Field: FilterZone, Values: []string{"eu-1a", "eu-1b"}},
		},
	}
	_, err := resolve(t.Context(), f, "7", view)
	require.NoError(t, err)

	require.Len(t, f.statements, 3)
	pods := f.statements[1]
	assert.Contains(t, pods, "c.name IN ('app')")
	assert.Contains(t, pods, "p.node IN ('node-a')")
	assert.Contains(t, pods, "l.key = 'topology.kubernetes.io/zone' AND l.value IN ('eu-1a', 'eu-1b')")
	assert.Contains(t, f.statements[2], "n.name IN ('node-a')")
}

// A workload's pods are walked down to from it, never through the ancestors of
// every object in the cache.
func TestAWorkloadIsWalkedDownFrom(t *testing.T) {
	f := webMirror().service()
	_, err := resolve(t.Context(), f, "7", webView)
	require.NoError(t, err)

	pods := f.statements[0]
	assert.Contains(t, pods, "o.kind = 'Deployment' AND o.namespace = 'prod' AND o.name = 'web'")
	assert.Contains(t, pods, "JOIN owners r ON r.owner_uid = down.uid")
	assert.NotContains(t, pods, "ancestors")
}

// A view with no stream, or more than Kstack reads at once, reads no node.
func TestAViewWithNoStreamToPlaceReadsNoNode(t *testing.T) {
	m := webMirror()
	f := m.service()
	_, err := resolve(t.Context(), f, "7", View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "nope"}}})
	require.NoError(t, err)
	assert.Len(t, f.statements, 1)

	manyPods(m, maxStreams)
	f = m.service()
	_, err = resolve(t.Context(), f, "7", webView)
	require.NoError(t, err)
	assert.Len(t, f.statements, 1)
}
