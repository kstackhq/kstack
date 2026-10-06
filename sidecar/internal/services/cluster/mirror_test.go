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

package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/amorey/beehive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// startMirror runs a mirror over d with a resync a test never waits out, and hands
// back the channel each finished pass reports on.
func startMirror(t *testing.T, d deps) (*clusterMirror, <-chan struct{}) {
	t.Helper()
	m := newClusterMirror(d, time.Hour, time.Hour, time.Hour)
	m.passed = make(chan struct{}, 16)
	stop, err := m.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stop(context.Background())) })
	return m, m.passed
}

// runtimeObjectOf reads the row's runtime object, nil when there is none.
func runtimeObjectOf(t *testing.T, d deps, id ClusterID) *beehive.Object[ClusterRuntimeSpec, ClusterStatus] {
	t.Helper()
	obj, err := serviceOver(t, d).runtimeObject(context.Background(), id)
	require.NoError(t, err)
	return obj
}

// The first pass gives every unmarked row a runtime object named by its id and
// carrying its runtime spec, and nothing else off the row.
func TestMirrorCreatesTheObjectBehindARow(t *testing.T) {
	d := newRunningDeps(t)
	row := importCluster(t, d, "prod")
	_, err := d.db.Write.Exec(`UPDATE clusters SET name = 'Prod', monitoring_enabled = 1 WHERE id = ?`, string(row.ID))
	require.NoError(t, err)

	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	obj := runtimeObjectOf(t, d, row.ID)
	require.NotNil(t, obj)
	assert.Equal(t, kubeconfigRuntimeSpec("prod"), obj.Spec)
}

// A pass over an unchanged row writes nothing: the spec bytes match, so the object's
// generation holds and no controller is woken.
func TestMirrorLeavesAnUnchangedRowAlone(t *testing.T) {
	d := newRunningDeps(t)
	row := importCluster(t, d, "prod")
	m, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")
	before := runtimeObjectOf(t, d, row.ID)

	m.pass(context.Background())
	testutil.Recv(t, passed, "the second pass")

	after := runtimeObjectOf(t, d, row.ID)
	assert.Equal(t, before.Generation, after.Generation)
}

// A toggle on the row reaches the spec on the next pass, which the setter's signal
// wakes.
func TestMirrorFollowsAToggleIntoTheSpec(t *testing.T) {
	d := newRunningDeps(t)
	row := importCluster(t, d, "prod")
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	_, err := serviceOver(t, d).Clusters().SetSyncEnabled(context.Background(), row.ID, false)
	require.NoError(t, err)

	testutil.Recv(t, passed, "the pass the setter woke")
	assert.False(t, runtimeObjectOf(t, d, row.ID).Spec.SyncEnabled)
}

// A row inserted after the mirror started is picked up off the importer's signal:
// the subscription is taken before the first pass, so nothing lands between them.
func TestMirrorWakesOnAnInsertedRow(t *testing.T) {
	d := newRunningDeps(t)
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	row := importCluster(t, d, "prod")
	d.db.Notify(appdb.KeyClusters)

	testutil.Recv(t, passed, "the pass the signal woke")
	assert.NotNil(t, runtimeObjectOf(t, d, row.ID))
}

// An object with no row is a leftover — a row removed while the process was down, or
// a crash between the two — and is torn down. The running controllers can finish the
// teardown before the read, so an object already gone passes too.
func TestMirrorDeletesAnOrphanObject(t *testing.T) {
	d := newRunningDeps(t)
	_, _, err := d.clusterClient.CreateOrUpdate(context.Background(), "no-such-row", kubeconfigRuntimeSpec("prod"))
	require.NoError(t, err)

	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	if obj := runtimeObjectOf(t, d, "no-such-row"); obj != nil {
		assert.NotNil(t, obj.DeletionRequestedAt)
	}
}

// The mirror never touches updated_at: a reconcile is not a user edit.
func TestMirrorLeavesUpdatedAtAlone(t *testing.T) {
	d := newRunningDeps(t)
	row := importCluster(t, d, "prod")
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	got, _, err := getCluster(context.Background(), d.store.Stmts(), row.ID)
	require.NoError(t, err)
	assert.Equal(t, row.UpdatedAt, got.UpdatedAt)
}

// awaitPass drains passes until check holds, for a teardown that takes more than one:
// the mark's pass asks the object to go, and the collection's pass removes the row.
func awaitPass(t *testing.T, passed <-chan struct{}, check func() bool) {
	t.Helper()
	for !check() {
		testutil.Recv(t, passed, "a pass")
	}
}

// A marked row's object is asked to go, and the row goes once the object has: with
// the cache finalizer, an absent object proves every cache file is gone. The removal
// wakes the watchers.
func TestMirrorTearsDownAMarkedRow(t *testing.T) {
	d := newRunningStandInDeps(t)
	row := importCluster(t, d, "prod")
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")
	sub := d.db.Subscribe(appdb.KeyClusters)
	defer sub.Close()

	require.NoError(t, serviceOver(t, d).Clusters().Delete(context.Background(), row.ID))
	testutil.Recv(t, sub.Chan(), "the mark's signal")

	awaitPass(t, passed, func() bool {
		_, ok, err := getCluster(context.Background(), d.store.Stmts(), row.ID)
		require.NoError(t, err)
		return !ok
	})
	assert.Nil(t, runtimeObjectOf(t, d, row.ID))
	testutil.Recv(t, sub.Chan(), "the removal's signal")
}

// A chat still filed under the row holds it, object or no object; the chat sweeper's
// signal is what brings the mirror back once the last one goes.
func TestMirrorHoldsARowWithChats(t *testing.T) {
	d := newRunningStandInDeps(t)
	row := importCluster(t, d, "prod")
	_, err := d.db.Write.Exec(`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', ?, 'chat', 0, 0)`, string(row.ID))
	require.NoError(t, err)
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	require.NoError(t, serviceOver(t, d).Clusters().Delete(context.Background(), row.ID))
	awaitPass(t, passed, func() bool { return runtimeObjectOf(t, d, row.ID) == nil })

	_, ok, err := getCluster(context.Background(), d.store.Stmts(), row.ID)
	require.NoError(t, err)
	assert.True(t, ok, "the chat holds the row")

	_, err = d.db.Write.Exec(`DELETE FROM chats`)
	require.NoError(t, err)
	d.db.Notify(appdb.KeyClusters)
	awaitPass(t, passed, func() bool {
		_, ok, err := getCluster(context.Background(), d.store.Stmts(), row.ID)
		require.NoError(t, err)
		return !ok
	})
}

// A row already marked when the mirror starts — a crash mid-teardown — resumes: the
// object is asked to go, or, already gone, the row is removed.
func TestMirrorResumesATeardownAtStartup(t *testing.T) {
	d := newRunningStandInDeps(t)
	row := importCluster(t, d, "prod")
	_, err := markCluster(context.Background(), d.store.Stmts(), row.ID, t0)
	require.NoError(t, err)

	_, passed := startMirror(t, d)

	awaitPass(t, passed, func() bool {
		_, ok, err := getCluster(context.Background(), d.store.Stmts(), row.ID)
		require.NoError(t, err)
		return !ok
	})
}

// A marked row never gets its object back: a pass that finds one marked skips the
// create, so the teardown is not undone by the mirror's own resync.
func TestMirrorNeverRecreatesAMarkedRowsObject(t *testing.T) {
	d := newRunningStandInDeps(t)
	row := importCluster(t, d, "prod")
	_, err := d.db.Write.Exec(`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', ?, 'chat', 0, 0)`, string(row.ID))
	require.NoError(t, err)
	_, err = markCluster(context.Background(), d.store.Stmts(), row.ID, t0)
	require.NoError(t, err)
	m, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	m.pass(context.Background())
	testutil.Recv(t, passed, "the resync")

	assert.Nil(t, runtimeObjectOf(t, d, row.ID))
}

// A pass that cannot read its rows is logged and retried on the next signal; the
// loop keeps running.
func TestMirrorSurvivesAFailedPass(t *testing.T) {
	d := newRunningDeps(t)
	m, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")
	require.NoError(t, d.db.Close())

	m.pass(context.Background())

	testutil.Recv(t, passed, "the failed pass still reports")
}

// A runtime object already on its way out under an unmarked row is a teardown under
// way — the row cannot be unmarked — and is not recreated over.
func TestMirrorLeavesADeletingObjectAlone(t *testing.T) {
	d := newRunningStandInDeps(t)
	row := importCluster(t, d, "prod")
	_, _, err := d.clusterClient.CreateOrUpdate(context.Background(), string(row.ID), runtimeSpecOf(row), beehive.WithFinalizers("hold"))
	require.NoError(t, err)
	require.NoError(t, d.clusterClient.DeleteByName(context.Background(), string(row.ID)))
	m := newClusterMirror(d, time.Hour, time.Hour, time.Hour)

	require.NoError(t, m.reconcile(context.Background()))

	obj := runtimeObjectOf(t, d, row.ID)
	require.NotNil(t, obj)
	assert.NotNil(t, obj.DeletionRequestedAt, "the teardown stands")
}

// The runtime watch is the mirror's second subscription, so a store that cannot open
// one fails Start.
func TestMirrorStartReportsAStoreThatWillNotWatch(t *testing.T) {
	d := newTestDepsOverAClosedStore(t)

	_, err := newClusterMirror(d, time.Hour, time.Hour, time.Hour).Start(context.Background())

	assert.Error(t, err)
}

// A runtime store that will not list fails the pass before any row is touched.
func TestMirrorReportsARuntimeStoreThatWillNotList(t *testing.T) {
	d, closeStore := newTestDepsWithABreakableStore(t)
	importCluster(t, d, "prod")
	closeStore()

	assert.Error(t, newClusterMirror(d, time.Hour, time.Hour, time.Hour).reconcile(context.Background()))
}

// The resync is the backstop for a signal that went missing: a row inserted with no
// notification is mirrored on the next tick.
func TestMirrorResyncsOnItsInterval(t *testing.T) {
	d := newRunningDeps(t)
	m := newClusterMirror(d, time.Millisecond, time.Hour, time.Hour)
	m.passed = make(chan struct{}, 64)
	stop, err := m.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stop(context.Background())) })
	testutil.Recv(t, m.passed, "the first pass")

	row := importCluster(t, d, "prod")

	awaitPass(t, m.passed, func() bool { return runtimeObjectOf(t, d, row.ID) != nil })
}

// A row the runtime store refuses — here one with no id at all, the one name beehive
// rejects — is logged and skipped, and the other rows still mirror.
func TestMirrorSkipsARowTheRuntimeStoreRefuses(t *testing.T) {
	d := newRunningDeps(t)
	_, err := d.db.Write.Exec(`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('', 'kubeconfig', 'blank', 0, 0)`)
	require.NoError(t, err)
	row := importCluster(t, d, "prod")
	m := newClusterMirror(d, time.Hour, time.Hour, time.Hour)

	err = m.reconcile(context.Background())

	assert.Error(t, err)
	assert.NotNil(t, runtimeObjectOf(t, d, row.ID), "the refused row does not hold the others")
}

// A pass that failed is retried on the pass-retry delay, since no signal follows a
// failure: a row the runtime store refuses fails the first pass, and a later pass
// mirrors a row added with no notification. The row goes in before the refused
// one leaves, so every pass that could miss it still fails and retries.
func TestMirrorRetriesAPassThatFailed(t *testing.T) {
	d := newRunningDeps(t)
	_, err := d.db.Write.Exec(`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('', 'kubeconfig', 'blank', 0, 0)`)
	require.NoError(t, err)
	m := newClusterMirror(d, time.Hour, time.Hour, time.Millisecond)
	m.passed = make(chan struct{}, 64)
	stop, err := m.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stop(context.Background())) })
	testutil.Recv(t, m.passed, "the first pass")

	row := importCluster(t, d, "prod")
	_, err = d.db.Write.Exec(`DELETE FROM clusters WHERE id = ''`)
	require.NoError(t, err)

	awaitPass(t, m.passed, func() bool { return runtimeObjectOf(t, d, row.ID) != nil })
}

// A row's teardown reports the store that refused it: the runtime store for the
// object's deletion, app.db for the row's.
func TestMirrorReportsATeardownWriteThatFailed(t *testing.T) {
	ctx := context.Background()
	marked := ClusterRow{ID: ClusterID(appdb.NewID()), DeleteRequestedAt: &t0}

	t.Run("runtime object", func(t *testing.T) {
		d, closeStore := newTestDepsWithABreakableStore(t)
		obj, _, err := d.clusterClient.CreateOrUpdate(ctx, string(marked.ID), runtimeSpecOf(marked))
		require.NoError(t, err)
		closeStore()

		assert.Error(t, newClusterMirror(d, time.Hour, time.Hour, time.Hour).reconcileRow(ctx, marked, obj))
	})
	t.Run("row", func(t *testing.T) {
		d := newTestDeps(t)
		require.NoError(t, d.db.Close())

		assert.Error(t, newClusterMirror(d, time.Hour, time.Hour, time.Hour).reconcileRow(ctx, marked, nil))
	})
}

// scriptedWatches is the cluster client with its WatchList driven by the test: each
// call takes the next answer — an error fails it, nil hands back a stream whose
// changes are the test's to send or close — and a call with none queued fails, as
// against a store that is down. It never blocks, since the mirror's loop waits on
// it. Everything else is the real client's.
type scriptedWatches struct {
	beehive.Client[ClusterRuntimeSpec, ClusterStatus]
	answers chan error
	opened  chan chan beehive.ObjectChange[ClusterRuntimeSpec, ClusterStatus]
}

func scriptWatches(d *deps) *scriptedWatches {
	s := &scriptedWatches{Client: d.clusterClient, answers: make(chan error, 16), opened: make(chan chan beehive.ObjectChange[ClusterRuntimeSpec, ClusterStatus], 16)}
	d.clusterClient = s
	return s
}

func (s *scriptedWatches) WatchList(context.Context, ...beehive.WatchOption) (*beehive.ObjectListStream[ClusterRuntimeSpec, ClusterStatus], error) {
	select {
	case err := <-s.answers:
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("store down")
	}
	ch := make(chan beehive.ObjectChange[ClusterRuntimeSpec, ClusterStatus])
	s.opened <- ch
	return &beehive.ObjectListStream[ClusterRuntimeSpec, ClusterStatus]{Changes: ch}, nil
}

// A runtime watch that ends — beehive closes one that fell below its log's
// retention — is reopened, with a pass for whatever was collected in the gap, and
// the rows keep driving passes throughout.
func TestMirrorReopensARuntimeWatchThatEnded(t *testing.T) {
	d := newRunningDeps(t)
	watches := scriptWatches(&d)
	watches.answers <- nil
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")
	first := testutil.Recv(t, watches.opened, "the first watch")

	watches.answers <- nil
	close(first)
	second := testutil.Recv(t, watches.opened, "the reopened watch")
	testutil.Recv(t, passed, "the pass covering the gap")

	row := importCluster(t, d, "prod")
	d.db.Notify(appdb.KeyClusters)
	testutil.Recv(t, passed, "the pass the row signal woke")
	assert.NotNil(t, runtimeObjectOf(t, d, row.ID))

	second <- beehive.ObjectChange[ClusterRuntimeSpec, ClusterStatus]{Type: beehive.Deleted}
	testutil.Recv(t, passed, "the pass the reopened watch woke")
}

// A reopen that fails is retried on the rewatch delay; the rows keep driving passes
// while the watch is down.
func TestMirrorRetriesAReopenThatFailed(t *testing.T) {
	d := newRunningDeps(t)
	watches := scriptWatches(&d)
	watches.answers <- nil
	m := newClusterMirror(d, time.Hour, time.Millisecond, time.Hour)
	m.passed = make(chan struct{}, 16)
	stop, err := m.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stop(context.Background())) })
	testutil.Recv(t, m.passed, "the first pass")
	first := testutil.Recv(t, watches.opened, "the first watch")

	close(first) // nothing queued: the reopen fails until the test answers nil
	row := importCluster(t, d, "prod")
	d.db.Notify(appdb.KeyClusters)
	testutil.Recv(t, m.passed, "the pass the row signal woke while the watch is down")
	assert.NotNil(t, runtimeObjectOf(t, d, row.ID))

	watches.answers <- nil
	testutil.Recv(t, watches.opened, "the watch reopened on the retry")
	testutil.Recv(t, m.passed, "the pass covering the gap")
}

// reportingSourceController stands in for the source controller and reports every pass.
type reportingSourceController struct {
	lifecycle.None
	passes chan struct{}
}

func (c reportingSourceController) Reconcile(context.Context, beehive.ControllerClient[ClusterSourceStatus], *beehive.Object[ClusterSourceSpec, ClusterSourceStatus]) beehive.Result {
	c.passes <- struct{}{}
	return beehive.Settled()
}

// A row's removal wakes the source that imported it: a context that came back while
// its old row was marked found the importer's insert a no-op, and gets its row from
// the pass this wakes rather than from the source's resync.
func TestMirrorWakesTheSourceWhenARowGoes(t *testing.T) {
	bh := newTestBeehive(t)
	registerCacheStandIn(t, bh)
	require.NoError(t, beehive.Register(bh, ClusterGroupKind, settlingClusterController{}))
	source := reportingSourceController{passes: make(chan struct{}, 16)}
	require.NoError(t, beehive.Register(bh, ClusterSourceGroupKind, source))
	stop, err := bh.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, stop(context.Background())) })
	d := newTestDepsOver(t, bh)
	require.NoError(t, ensureClusterSources(context.Background(), d.sourceClient))
	testutil.Recv(t, source.passes, "the pass the anchor's creation woke")
	row := importCluster(t, d, "prod")
	_, passed := startMirror(t, d)
	testutil.Recv(t, passed, "the first pass")

	require.NoError(t, serviceOver(t, d).Clusters().Delete(context.Background(), row.ID))
	awaitPass(t, passed, func() bool {
		_, ok, err := getCluster(context.Background(), d.store.Stmts(), row.ID)
		require.NoError(t, err)
		return !ok
	})

	testutil.Recv(t, source.passes, "the pass the removal woke")
}
