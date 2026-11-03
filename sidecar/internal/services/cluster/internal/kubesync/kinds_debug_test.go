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

//go:build debug

// The quiet fault drives kubesync's Stale verdict, so it is pinned against kubesync: a real
// connection carrying the rules, its lists and watches over HTTP rather than the in-memory
// client the other kind tests use.
package kubesync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/internal/kubeconn"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/internal/kubestore"
)

// secretKind is here, not beside podKind: only a debug build uses it, and staticcheck runs
// untagged.
var secretKind = kubestore.Kind{APIVersion: "v1", Kind: "Secret", Resource: "secrets"}

// faultedConnection is a connection to c carrying spec, whose lists and watches go over HTTP
// through the fault wrapper.
func (c *fakeCluster) faultedConnection(t *testing.T, spec string) *kubeconn.Connection {
	t.Helper()
	faults, err := kubeconn.ParseFaults(spec)
	require.NoError(t, err)
	conn, err := kubeconn.NewConnection(&rest.Config{Host: c.baseURL.String()}, kubeconn.WithFaults(faults))
	require.NoError(t, err)
	t.Cleanup(conn.Retire)
	return conn
}

// listsOverHTTP serves an empty collection for each kind, as the api server's LIST answers.
func (c *fakeCluster) listsOverHTTP(kinds ...kubestore.Kind) {
	for _, k := range kinds {
		c.serveKind(k, true)
		c.serveRaw("/api/v1/"+k.Resource,
			`{"kind":"`+k.Kind+`List","apiVersion":"v1","metadata":{"resourceVersion":"10"},"items":[]}`)
	}
}

// newQuietService is a real kind sync over prod, whose connection carries spec.
func newQuietService(t *testing.T, cluster *fakeCluster, spec string, opts ...func(*pacing)) (*Service, *fakePool) {
	t.Helper()
	svc, pool := newTestService(t, withPacing(syncPacing(opts...)), withRealKindSync())
	pool.lease("prod").hand(cluster.faultedConnection(t, spec), "uid-1")
	start(t, svc)
	return svc, pool
}

func TestAQuietWatchReadsWatchingThenStale(t *testing.T) {
	cluster := newFakeCluster(t)
	cluster.listsOverHTTP(podKind)
	svc, _ := newQuietService(t, cluster, "watch pods=quiet",
		func(p *pacing) { p.staleAfter = 50 * time.Millisecond })

	syncKind(t, svc, 1, podKind)

	awaitKindReason(t, svc, 1, podKind, ReasonWatching)
	awaitKindReason(t, svc, 1, podKind, ReasonStale)
}

// Every kind's first open answers at once, so none holds the one start slot the other needs.
func TestEveryQuietKindReachesWatchingAtOnce(t *testing.T) {
	cluster := newFakeCluster(t)
	cluster.listsOverHTTP(podKind, secretKind)
	svc, _ := newQuietService(t, cluster, "watch *=quiet", func(p *pacing) { p.kindStartConcurrency = 1 })

	syncKind(t, svc, 1, podKind)
	svc.TrackKind(1, secretKind)

	awaitKindReason(t, svc, 1, podKind, ReasonWatching)
	awaitKindReason(t, svc, 1, secretKind, ReasonWatching)
}

// One kind on two caches over two connections plays two sequences.
func TestAQuietKindOnTwoConnectionsReachesWatchingOnBoth(t *testing.T) {
	cluster := newFakeCluster(t)
	cluster.listsOverHTTP(podKind)
	svc, pool := newQuietService(t, cluster, "watch pods=quiet", func(p *pacing) { p.kindStartConcurrency = 1 })
	pool.lease("stage").hand(cluster.faultedConnection(t, "watch pods=quiet"), "uid-2")

	syncKind(t, svc, 1, podKind)
	svc.TrackDiscovery(2, Params{ContextName: "stage", ServerUID: "uid-2"})
	awaitDiscovered(t, svc, 2)
	svc.TrackKind(2, podKind)

	awaitKindReason(t, svc, 1, podKind, ReasonWatching)
	awaitKindReason(t, svc, 2, podKind, ReasonWatching)
}

// A worker forgotten while its reopen hangs ends that sequence, so the next one starts silent.
func TestAQuietKindForgottenWhileItsReopenHangsWatchesAgainWhenTracked(t *testing.T) {
	cluster := newFakeCluster(t)
	cluster.listsOverHTTP(podKind)
	svc, _ := newQuietService(t, cluster, "watch pods=quiet",
		func(p *pacing) { p.staleAfter = 50 * time.Millisecond })
	syncKind(t, svc, 1, podKind)
	awaitKindReason(t, svc, 1, podKind, ReasonStale)

	svc.ForgetKind(1, podKind)
	svc.TrackKind(1, podKind)

	awaitKindReason(t, svc, 1, podKind, ReasonWatching)
}
