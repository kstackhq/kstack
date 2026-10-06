package graph_test

// Shared fixtures for the cluster/cache/data resolver tests. The resolvers
// delegate to a cluster.ClusterService, so the tests wire a fakeClusterService
// built from fixtures — that keeps the focus on the GraphQL wire mapping
// (nil→null, conditions/cache shapes) and off whatever backs the service.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kstackhq/kstack/sidecar/graph"
	"github.com/kstackhq/kstack/sidecar/internal/services/auth"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// ownerOf reads a record's owner ref out of a decoded JSON response.
func ownerOf(record map[string]any) map[string]any {
	owner, _ := record["owner"].(map[string]any)
	return owner
}

// clusterFixture bundles all data for one test cluster record. id is the beehive
// ObjectID; on the wire it is its decimal string ("1", "2", …).
type clusterFixture struct {
	id         cluster.ClusterID
	spec       cluster.ClusterSpec
	connStatus cluster.ClusterStatus
	// Conditions are beehive object rows, not part of either status block. syncConds
	// belong to the fixture's per-kind sync child.
	connConds  []cluster.Condition
	cacheConds []cluster.Condition
	syncConds  []cluster.Condition
}

// fakeClusterService implements cluster.ClusterService over an in-memory map
// built from fixtures: it joins each fixture's connection + cache status into a
// cluster.Cluster (exactly as the real service's buildCluster does), so the
// resolver/wire assertions see the same shapes.
type fakeClusterService struct {
	mu          sync.Mutex
	order       []cluster.ClusterID
	clusters    map[cluster.ClusterID]*cluster.Cluster
	caches      []cluster.ClusterCache      // one active cache per fixture, streamed via Caches().Watch
	cachedKinds []cluster.ClusterCachedKind // per-kind sync records, streamed cache-scoped via CachedKinds().Watch
	cacheStats  map[cluster.ClusterCacheID]cluster.ClusterCacheStats
	// When set, Delete fails with it.
	deleteErr error
	// When set, List fails with it.
	listErr     error
	syncEvents  map[cluster.ClusterCachedKindID][]cluster.Event
	events      map[cluster.ClusterID][]cluster.Event                   // connection-event history, keyed by ClusterID
	cacheEvents map[cluster.ClusterCacheID][]cluster.Event              // sync-event history, keyed by ClusterCacheID
	kinds       map[cluster.ClusterID][]cluster.ClusterCachedDataKind   // discovered kind catalog, keyed by ClusterID
	dataEvents  map[cluster.ClusterID][]cluster.ClusterCachedDataEvent  // cached Kubernetes Events, keyed by ClusterID
	dataObjects map[cluster.ClusterID][]cluster.ClusterCachedDataObject // cached objects for one kind, keyed by ClusterID
	watchFail   error                                                   // when set, every watch ends with it after its snapshot
}

// The fake mirrors production's shape: one shared state struct, four accessor
// views that carry the family method sets. Each family is asserted separately —
// satisfying cluster.Service only proves the accessors exist.
type (
	fakeClusters    struct{ s *fakeClusterService }
	fakeCaches      struct{ s *fakeClusterService }
	fakeCachedKinds struct{ s *fakeClusterService }
	fakeCachedData  struct{ s *fakeClusterService }
)

func (f *fakeClusterService) Clusters() cluster.Clusters { return fakeClusters{f} }
func (f *fakeClusterService) Caches() cluster.Caches     { return fakeCaches{f} }
func (f *fakeClusterService) CachedKinds() cluster.CachedKinds {
	return fakeCachedKinds{f}
}
func (f *fakeClusterService) CachedData() cluster.CachedData { return fakeCachedData{f} }

// The resolvers never drive the lifecycle — the composition root does — so the fake
// satisfies it and nothing more.
func (f *fakeClusterService) Start(context.Context) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

func (f *fakeClusterService) Close() error { return nil }

var (
	_ cluster.Service     = (*fakeClusterService)(nil)
	_ cluster.Clusters    = fakeClusters{}
	_ cluster.Caches      = fakeCaches{}
	_ cluster.CachedKinds = fakeCachedKinds{}
	_ cluster.CachedData  = fakeCachedData{}
)

// Fixture ids are distinct per kind. A cluster's id is its number spelled as text;
// beehive draws every other kind from one AUTOINCREMENT sequence, so a cache never
// shares a number with its cluster — and a fixture that reused one would let a
// resolver read the wrong id and still pass.
func fixtureNum(id cluster.ClusterID) int64 {
	n, err := strconv.ParseInt(string(id), 10, 64)
	if err != nil {
		panic("fixture cluster id is not a number: " + string(id))
	}
	return n
}

func fixtureCacheID(id cluster.ClusterID) cluster.ClusterCacheID {
	return cluster.ClusterCacheID(fixtureNum(id) + 100)
}

func fixtureKindID(id cluster.ClusterID) cluster.ClusterCachedKindID {
	return cluster.ClusterCachedKindID(fixtureNum(id) + 300)
}

// fixtureStamp is every fixture's created and updated time: the wire serializes a
// zero time as null, and both fields are non-null.
var fixtureStamp = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func newFakeClusterService(fixtures []clusterFixture) *fakeClusterService {
	f := &fakeClusterService{
		clusters:   map[cluster.ClusterID]*cluster.Cluster{},
		events:     map[cluster.ClusterID][]cluster.Event{},
		cacheStats: map[cluster.ClusterCacheID]cluster.ClusterCacheStats{},
	}
	for _, fx := range fixtures {
		id := fx.id
		f.order = append(f.order, id)
		f.clusters[id] = &cluster.Cluster{
			ID:         id,
			CreatedAt:  fixtureStamp,
			UpdatedAt:  fixtureStamp,
			Conditions: fx.connConds,
			Spec:       fx.spec,
			Status:     fx.connStatus,
		}
		// Caches stream standalone via WatchCaches and are joined client-side.
		// Give each fixture one cache whose ServerUID matches the cluster's
		// identity (the client's active-cache rule).
		f.caches = append(f.caches, cluster.ClusterCache{
			RecordMeta: cluster.RecordMeta{ID: fixtureCacheID(id), Conditions: fx.cacheConds},
			ClusterID:  id,
			Spec:       cluster.ClusterCacheSpec{ServerUID: "uid-" + string(id), ClusterID: id},
		})
		// Each cache gets one per-kind sync record, so the cache-scoped watch has
		// something to scope. Deliberately one per cache so a leak across caches is
		// visible as an extra frame.
		f.cachedKinds = append(f.cachedKinds, cluster.ClusterCachedKind{
			RecordMeta: cluster.RecordMeta{ID: fixtureKindID(id), Conditions: fx.syncConds},
			Owner:      cluster.ObjectRef{ID: fixtureCacheID(id), Kind: "ClusterCache"},
			Spec: cluster.ClusterCachedKindSpec{
				APIVersion: "apps/v1", Kind: "Deployment",
				Resource: "deployments", Namespaced: true,
			},
		})
		f.cacheStats[fixtureCacheID(id)] = cluster.ClusterCacheStats{
			Exists: true, Bytes: 4096, ObjectCount: 1386, KindCount: 62,
		}
	}
	return f
}

func (f *fakeClusterService) snapshot() []*cluster.Cluster {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*cluster.Cluster, 0, len(f.order))
	for _, id := range f.order {
		if c, ok := f.clusters[id]; ok {
			cp := *c
			out = append(out, &cp)
		}
	}
	return out
}

func (f *fakeClusterService) cacheSnapshot() []cluster.ClusterCache {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cluster.ClusterCache(nil), f.caches...)
}

func (f fakeClusters) List(context.Context) ([]*cluster.Cluster, error) {
	if f.s.listErr != nil {
		return nil, f.s.listErr
	}
	return f.s.snapshot(), nil
}

func (f fakeClusters) Get(_ context.Context, id cluster.ClusterID) (*cluster.Cluster, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	c, ok := f.s.clusters[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

// ReadActive hands read the record alone: no resolver reads a cache through it.
func (f fakeClusters) ReadActive(ctx context.Context, id cluster.ClusterID, read func(context.Context, cluster.ActiveCluster) error) error {
	c, err := f.Get(ctx, id)
	if err != nil {
		return err
	}
	if c == nil {
		return cluster.ErrNotFound
	}
	return read(ctx, cluster.ActiveCluster{Cluster: c})
}

// deltaStream models every delta watch the service exposes: replay the current set as
// Added frames, close the snapshot with bookmark, then hold the stream open until ctx
// ends (a real watch never completes on its own, and several tests assert exactly
// that). Each watch differs only in how it wraps an item into its frame.
//
// The bookmark rides the stream even when the snapshot is empty — that is the whole
// point of it, and how a consumer tells "nothing here" from "still listing".
//
// A fixture with watchFail set ends every watch with it instead, standing in for a
// source that died mid-stream.
func deltaStream[T, C any](ctx context.Context, f *fakeClusterService, items []T, wrap func(*T) C, bookmark C) *cluster.Stream[C] {
	snap := make([]C, 0, len(items)+1)
	for i := range items {
		item := items[i]
		snap = append(snap, wrap(&item))
	}
	snap = append(snap, bookmark)
	return streamOf(ctx, f, snap)
}

// gaugeStream is deltaStream's counterpart for a latest-value gauge: current on
// subscribe, so no bookmark closes anything. WatchHealth is the only one.
func gaugeStream[T, C any](ctx context.Context, f *fakeClusterService, items []T, wrap func(*T) C) *cluster.Stream[C] {
	snap := make([]C, 0, len(items))
	for i := range items {
		item := items[i]
		snap = append(snap, wrap(&item))
	}
	return streamOf(ctx, f, snap)
}

func streamOf[C any](ctx context.Context, f *fakeClusterService, frames []C) *cluster.Stream[C] {
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- C) error {
		for _, c := range frames {
			select {
			case out <- c:
			case <-ctx.Done():
				return nil
			}
		}
		if err := f.failure(); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	})
}

// failure is the injected terminal reason, nil unless a test set one.
func (f *fakeClusterService) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watchFail
}

// copySlice returns a copy of src taken under the fake's lock, so a watch's replay can't
// race a concurrent mutation of the fixture.
func copySlice[T any](f *fakeClusterService, src *[]T) []T {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]T(nil), *src...)
}

func (f fakeClusters) WatchList(ctx context.Context) (*cluster.Stream[cluster.ClusterWatchFrame], error) {
	return deltaStream(ctx, f.s, f.s.snapshot(), func(c **cluster.Cluster) cluster.ClusterWatchFrame {
		return cluster.ClusterWatchFrame{Type: cluster.DeltaFrameAdded, Cluster: *c}
	}, cluster.ClusterWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

func (f fakeClusters) Watch(ctx context.Context, id cluster.ClusterID) (*cluster.Stream[cluster.ClusterWatchFrame], error) {
	var rows []*cluster.Cluster
	for _, c := range f.s.snapshot() {
		if c.ID == id {
			rows = append(rows, c)
		}
	}
	return deltaStream(ctx, f.s, rows, func(c **cluster.Cluster) cluster.ClusterWatchFrame {
		return cluster.ClusterWatchFrame{Type: cluster.DeltaFrameAdded, Cluster: *c}
	}, cluster.ClusterWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

func (f fakeCaches) WatchList(ctx context.Context) (*cluster.Stream[cluster.ClusterCacheWatchFrame], error) {
	return deltaStream(ctx, f.s, f.s.cacheSnapshot(), func(c *cluster.ClusterCache) cluster.ClusterCacheWatchFrame {
		return cluster.ClusterCacheWatchFrame{Type: cluster.DeltaFrameAdded, Cache: c}
	}, cluster.ClusterCacheWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

func (f fakeCaches) WatchByCluster(ctx context.Context, clusterID cluster.ClusterID) (*cluster.Stream[cluster.ClusterCacheWatchFrame], error) {
	var rows []cluster.ClusterCache
	for _, c := range f.s.cacheSnapshot() {
		if c.ClusterID == clusterID {
			rows = append(rows, c)
		}
	}
	return deltaStream(ctx, f.s, rows, func(c *cluster.ClusterCache) cluster.ClusterCacheWatchFrame {
		return cluster.ClusterCacheWatchFrame{Type: cluster.DeltaFrameAdded, Cache: c}
	}, cluster.ClusterCacheWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

func (f fakeCaches) Watch(ctx context.Context, id cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCacheWatchFrame], error) {
	var rows []cluster.ClusterCache
	for _, c := range f.s.cacheSnapshot() {
		if c.ID == id {
			rows = append(rows, c)
		}
	}
	return deltaStream(ctx, f.s, rows, func(c *cluster.ClusterCache) cluster.ClusterCacheWatchFrame {
		return cluster.ClusterCacheWatchFrame{Type: cluster.DeltaFrameAdded, Cache: c}
	}, cluster.ClusterCacheWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

// WatchHealth folds the fixture's per-kind records per cache, the same way the
// real service does — enough to prove the wire shape and the join key.
func (f fakeCaches) WatchHealth(ctx context.Context) (*cluster.Stream[cluster.ClusterCacheHealth], error) {
	f.s.mu.Lock()
	byCache := map[cluster.ClusterCacheID]*cluster.ClusterCacheHealth{}
	for i := range f.s.cachedKinds {
		cacheID := f.s.cachedKinds[i].Owner.ID
		h := byCache[cacheID]
		if h == nil {
			h = &cluster.ClusterCacheHealth{CacheID: cacheID, Status: cluster.ConditionTrue, Reason: "Watching"}
			byCache[cacheID] = h
		}
		h.TotalKinds++
		for _, c := range f.s.cachedKinds[i].Conditions {
			if c.Type == string(cluster.ConditionSynced) && c.Reason != "Watching" {
				h.Status, h.Reason = c.Status, c.Reason
				h.UnhealthyKindRefs = append(h.UnhealthyKindRefs, cluster.SyncedKindRef{
					APIVersion: f.s.cachedKinds[i].Spec.APIVersion,
					Resource:   f.s.cachedKinds[i].Spec.Resource,
				})
				h.UnhealthyKinds++
			}
		}
	}
	f.s.mu.Unlock()
	verdicts := make([]cluster.ClusterCacheHealth, 0, len(byCache))
	for _, h := range byCache {
		verdicts = append(verdicts, *h)
	}
	return gaugeStream(ctx, f.s, verdicts, func(h *cluster.ClusterCacheHealth) cluster.ClusterCacheHealth {
		return *h
	}), nil
}

// WatchByCache serves only the records the requested cache owns, standing in for the
// real service's owner-edge filter.
func (f fakeCachedKinds) WatchByCache(ctx context.Context, cacheID cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCachedKindWatchFrame], error) {
	f.s.mu.Lock()
	var scoped []cluster.ClusterCachedKind
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].Owner.ID == cacheID {
			scoped = append(scoped, f.s.cachedKinds[i])
		}
	}
	f.s.mu.Unlock()
	return deltaStream(ctx, f.s, scoped, func(gs *cluster.ClusterCachedKind) cluster.ClusterCachedKindWatchFrame {
		return cluster.ClusterCachedKindWatchFrame{Type: cluster.DeltaFrameAdded, Kind: gs}
	}, cluster.ClusterCachedKindWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

func (f fakeCachedKinds) WatchList(ctx context.Context) (*cluster.Stream[cluster.ClusterCachedKindWatchFrame], error) {
	return deltaStream(ctx, f.s, copySlice(f.s, &f.s.cachedKinds), func(gs *cluster.ClusterCachedKind) cluster.ClusterCachedKindWatchFrame {
		return cluster.ClusterCachedKindWatchFrame{Type: cluster.DeltaFrameAdded, Kind: gs}
	}, cluster.ClusterCachedKindWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

func (f fakeCachedKinds) Watch(ctx context.Context, id cluster.ClusterCachedKindID) (*cluster.Stream[cluster.ClusterCachedKindWatchFrame], error) {
	f.s.mu.Lock()
	var rows []cluster.ClusterCachedKind
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].ID == id {
			rows = append(rows, f.s.cachedKinds[i])
		}
	}
	f.s.mu.Unlock()
	return deltaStream(ctx, f.s, rows, func(gs *cluster.ClusterCachedKind) cluster.ClusterCachedKindWatchFrame {
		return cluster.ClusterCachedKindWatchFrame{Type: cluster.DeltaFrameAdded, Kind: gs}
	}, cluster.ClusterCachedKindWatchFrame{Type: cluster.DeltaFrameBookmark}), nil
}

// WatchSyncStatus expands the fixture's per-kind records for one cache, the counterpart of
// the fold WatchHealth does — enough to prove the wire shape.
func (f fakeCaches) WatchSyncStatus(ctx context.Context, _ cluster.ClusterID, cacheID cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCacheSyncStatus], error) {
	f.s.mu.Lock()
	status := cluster.ClusterCacheSyncStatus{
		CacheID:   cacheID,
		Discovery: cluster.ClusterCacheDiscoveryStatus{Reason: "Discovered"},
	}
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].Owner.ID != cacheID {
			continue
		}
		spec := f.s.cachedKinds[i].Spec
		row := cluster.ClusterCacheKindSyncStatus{
			APIVersion: spec.APIVersion, Kind: spec.Kind, Resource: spec.Resource, Reason: "Watching",
		}
		for _, c := range f.s.cachedKinds[i].Conditions {
			if c.Type == string(cluster.ConditionSynced) {
				row.Reason, row.Message = c.Reason, c.Message
			}
		}
		status.Kinds = append(status.Kinds, row)
	}
	f.s.mu.Unlock()

	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterCacheSyncStatus) error {
		select {
		case out <- status:
		case <-ctx.Done():
			return nil
		}
		<-ctx.Done()
		return nil
	}), nil
}

// WatchStats emits the fixture's single measurement and then holds the
// stream open, as a gauge with nothing new to report does.
func (f fakeCaches) WatchStats(ctx context.Context, _ cluster.ClusterID, cacheID cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCacheStats], error) {
	f.s.mu.Lock()
	st := f.s.cacheStats[cacheID]
	f.s.mu.Unlock()
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterCacheStats) error {
		select {
		case out <- st:
		case <-ctx.Done():
			return nil
		}
		<-ctx.Done()
		return nil
	}), nil
}

// The one-shot reads serve no resolver; the card reads them off the real service.
func (f fakeCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	return cluster.ClusterCacheHealth{}, false, nil
}

func (f fakeCaches) SyncStatus(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheSyncStatus, bool, error) {
	return cluster.ClusterCacheSyncStatus{}, false, nil
}

func (f fakeCachedData) ListObjects(context.Context, cluster.ClusterID, cluster.ClusterCacheID, string, string) ([]cluster.ClusterCachedDataObject, bool, error) {
	return nil, false, nil
}

func (f fakeCachedData) Query(context.Context, cluster.ClusterID, cluster.ClusterCacheID, string, int, int) (cluster.ClusterCachedDataQueryResult, bool, error) {
	return cluster.ClusterCachedDataQueryResult{}, false, nil
}

func (f fakeCachedData) ListKinds(_ context.Context, clusterID cluster.ClusterID, _ cluster.ClusterCacheID) ([]cluster.ClusterCachedDataKind, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return f.s.kinds[clusterID], nil
}

func (f fakeCachedData) WatchKinds(ctx context.Context, clusterID cluster.ClusterID, _ cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCachedDataKindWatchFrame], error) {
	f.s.mu.Lock()
	snap := append([]cluster.ClusterCachedDataKind(nil), f.s.kinds[clusterID]...)
	f.s.mu.Unlock()
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterCachedDataKindWatchFrame) error {
		for _, k := range snap {
			select {
			case out <- cluster.ClusterCachedDataKindWatchFrame{Type: cluster.DeltaFrameAdded, Kind: &k}:
			case <-ctx.Done():
				return nil
			}
		}
		<-ctx.Done()
		return nil
	}), nil
}

func (f fakeCachedData) WatchEvents(ctx context.Context, clusterID cluster.ClusterID, _ cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCachedDataEventWatchFrame], error) {
	f.s.mu.Lock()
	snap := append([]cluster.ClusterCachedDataEvent(nil), f.s.dataEvents[clusterID]...)
	f.s.mu.Unlock()
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterCachedDataEventWatchFrame) error {
		for _, e := range snap {
			select {
			case out <- cluster.ClusterCachedDataEventWatchFrame{Type: cluster.DeltaFrameAdded, Event: &e}:
			case <-ctx.Done():
				return nil
			}
		}
		<-ctx.Done()
		return nil
	}), nil
}

func (f fakeCachedData) WatchObjects(ctx context.Context, clusterID cluster.ClusterID, _ cluster.ClusterCacheID, _, _ string) (*cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame], error) {
	f.s.mu.Lock()
	snap := append([]cluster.ClusterCachedDataObject(nil), f.s.dataObjects[clusterID]...)
	f.s.mu.Unlock()
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterCachedDataObjectWatchFrame) error {
		for _, o := range snap {
			select {
			case out <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameAdded, Object: &o}:
			case <-ctx.Done():
				return nil
			}
		}
		<-ctx.Done()
		return nil
	}), nil
}

// ListEvents and WatchEvents serve the cache and kind timelines from one reader, as
// the real service does; a cluster's is the Clusters() pair. The fixtures' ids are
// disjoint across kinds (see fixtureCacheID and friends), so the id alone picks the
// log out.
func (f *fakeClusterService) ListEvents(_ context.Context, id cluster.ObjectID, _ *string, _ *int) ([]cluster.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if evs, ok := f.cacheEvents[id]; ok {
		return evs, nil
	}
	return f.syncEvents[id], nil
}

func (f *fakeClusterService) WatchEvents(ctx context.Context, _ cluster.ObjectID, _ *string) (*cluster.Stream[cluster.EventWatchFrame], error) {
	return f.eventStream(ctx)
}

func (f *fakeClusterService) eventStream(ctx context.Context) (*cluster.Stream[cluster.EventWatchFrame], error) {
	return deltaStream(ctx, f, nil, func(e *cluster.Event) cluster.EventWatchFrame {
		return cluster.EventWatchFrame{Type: cluster.EventFrameRun, Event: e}
	}, cluster.EventWatchFrame{Type: cluster.EventFrameBookmark}), nil
}

func (f fakeClusters) ListEvents(_ context.Context, id cluster.ClusterID, _ *string, _ *int) ([]cluster.Event, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return f.s.events[id], nil
}

func (f fakeClusters) WatchEvents(ctx context.Context, _ cluster.ClusterID, _ *string) (*cluster.Stream[cluster.EventWatchFrame], error) {
	return f.s.eventStream(ctx)
}

func (f fakeCaches) Get(_ context.Context, id cluster.ClusterCacheID) (*cluster.ClusterCache, error) {
	for _, c := range f.s.cacheSnapshot() {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, nil
}

func (f fakeCaches) List(context.Context) ([]*cluster.ClusterCache, error) {
	var out []*cluster.ClusterCache
	for _, c := range f.s.cacheSnapshot() {
		out = append(out, &c)
	}
	return out, nil
}

func (f fakeCaches) ListByCluster(_ context.Context, clusterID cluster.ClusterID) ([]*cluster.ClusterCache, error) {
	var out []*cluster.ClusterCache
	for _, c := range f.s.cacheSnapshot() {
		if c.ClusterID == clusterID {
			out = append(out, &c)
		}
	}
	return out, nil
}

func (f fakeCachedKinds) Get(_ context.Context, id cluster.ClusterCachedKindID) (*cluster.ClusterCachedKind, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].ID == id {
			gs := f.s.cachedKinds[i]
			return &gs, nil
		}
	}
	return nil, nil
}

// List is every record, unscoped.
func (f fakeCachedKinds) List(context.Context) ([]*cluster.ClusterCachedKind, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	var out []*cluster.ClusterCachedKind
	for i := range f.s.cachedKinds {
		gs := f.s.cachedKinds[i]
		out = append(out, &gs)
	}
	return out, nil
}

func (f fakeCachedKinds) ListByCache(_ context.Context, cacheID cluster.ClusterCacheID) ([]*cluster.ClusterCachedKind, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	var out []*cluster.ClusterCachedKind
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].Owner.ID == cacheID {
			gs := f.s.cachedKinds[i]
			out = append(out, &gs)
		}
	}
	return out, nil
}

// WatchSchedule is a gauge: one current value on subscribe, then open until ctx
// ends, as a cluster with nothing newly scheduled behaves.
func (f fakeClusters) WatchSchedule(ctx context.Context, _ cluster.ClusterID) (<-chan cluster.Schedule, error) {
	ch := make(chan cluster.Schedule)
	go func() {
		defer close(ch)
		select {
		case ch <- cluster.Schedule{Probing: true}:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}()
	return ch, nil
}

func (f fakeClusters) SetEnabled(_ context.Context, id cluster.ClusterID, enabled bool) (*cluster.Cluster, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	c, ok := f.s.clusters[id]
	if !ok {
		return nil, cluster.ErrNotFound
	}
	c.Spec.Enabled = enabled
	cp := *c
	return &cp, nil
}

func (f fakeClusters) SetSyncEnabled(_ context.Context, id cluster.ClusterID, enabled bool) (*cluster.Cluster, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	c, ok := f.s.clusters[id]
	if !ok {
		return nil, cluster.ErrNotFound
	}
	c.Spec.SyncEnabled = enabled
	cp := *c
	return &cp, nil
}

func (f fakeClusters) SetMonitoringEnabled(_ context.Context, id cluster.ClusterID, enabled bool) (*cluster.Cluster, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	c, ok := f.s.clusters[id]
	if !ok {
		return nil, cluster.ErrNotFound
	}
	c.Spec.MonitoringEnabled = enabled
	cp := *c
	return &cp, nil
}

func (f *fakeClusterService) RetryConnection(_ context.Context, id cluster.ClusterID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.clusters[id]; !ok {
		return cluster.ErrNotFound
	}
	return nil
}

func (f fakeCachedKinds) SetSyncEnabled(_ context.Context, id cluster.ClusterCachedKindID, syncEnabled bool) (*cluster.ClusterCachedKind, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].ID == id {
			f.s.cachedKinds[i].Spec.Paused = !syncEnabled
			cr := f.s.cachedKinds[i]
			return &cr, nil
		}
	}
	return nil, cluster.ErrNotFound
}

func (f fakeCachedKinds) Clear(_ context.Context, id cluster.ClusterCachedKindID) (*cluster.ClusterCachedKind, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	for i := range f.s.cachedKinds {
		if f.s.cachedKinds[i].ID == id {
			cr := f.s.cachedKinds[i]
			return &cr, nil
		}
	}
	return nil, cluster.ErrNotFound
}

func (f fakeCaches) Clear(_ context.Context, id cluster.ClusterCacheID) (*cluster.ClusterCache, error) {
	for _, c := range f.s.cacheSnapshot() {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, cluster.ErrNotFound
}

// markDeleting puts beehive's tombstone on a fixture, standing in for a record
// between the delete that asked for it and the collection that takes it away.
func (f *fakeClusterService) markDeleting(id cluster.ClusterID, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusters[id].DeletionRequestedAt = &at
}

func (f fakeClusters) Delete(_ context.Context, id cluster.ClusterID) error {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.deleteErr != nil {
		return f.s.deleteErr
	}
	if _, ok := f.s.clusters[id]; !ok {
		return cluster.ErrNotFound
	}
	delete(f.s.clusters, id)
	return nil
}

func (f *fakeClusterService) AcquireConnection(context.Context, cluster.ClusterID) (cluster.Lease, error) {
	return nil, nil
}

// clusterFixtures returns two records: one fully-probed/present (1) and
// one never-probed/orphaned (2), so nullable fields exercise both arms.
func clusterFixtures() []clusterFixture {
	prodName := "Production"
	uid1 := "uid-1"
	ver := "v1.29.3"
	admin := "system:admin"
	return []clusterFixture{
		{
			id: "1",
			spec: cluster.ClusterSpec{
				Name:        &prodName,
				SyncEnabled: true,
				Enabled:     true,
				Source:      cluster.ClusterSpecSource{Kubeconfig: &cluster.ClusterSpecSourceKubeconfig{Context: "prod"}},
			},
			connStatus: cluster.ClusterStatus{
				Source: cluster.ClusterStatusSource{Kubeconfig: &cluster.ClusterStatusSourceKubeconfig{
					Cluster:   cluster.ClusterStatusSourceKubeconfigCluster{Name: "prod-cluster"},
					User:      cluster.ClusterStatusSourceKubeconfigUser{Name: "prod-user"},
					IsPresent: true, IsDefault: true,
				}},
				Server:    cluster.ClusterServer{UID: &uid1, Version: &ver},
				Principal: cluster.ClusterPrincipal{Username: &admin},
			},
		},
		{
			id: "2",
			spec: cluster.ClusterSpec{
				Source: cluster.ClusterSpecSource{Kubeconfig: &cluster.ClusterSpecSourceKubeconfig{Context: "staging"}},
			},
			connStatus: cluster.ClusterStatus{
				Source: cluster.ClusterStatusSource{Kubeconfig: &cluster.ClusterStatusSourceKubeconfig{
					Cluster: cluster.ClusterStatusSourceKubeconfigCluster{Name: "staging-cluster"},
					User:    cluster.ClusterStatusSourceKubeconfigUser{Name: "staging-user"},
				}},
			},
		},
	}
}

// newTestServer returns an httptest.Server backed by a real resolver wired to a
// fakeClusterService built from the fixtures. Cleanup is registered via
// t.Cleanup so callers need not defer Close.
func newTestServer(t *testing.T, fixtures []clusterFixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: newFakeClusterService(fixtures),
		Auth:       newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)
	return srv
}

// clustersQueryData creates a test server from the default fixtures, POSTs
// query, decodes data, and fails the test on any GraphQL errors.
func clustersQueryData(t *testing.T, query string) map[string]any {
	t.Helper()
	srv := newTestServer(t, clusterFixtures())

	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))

	var resp struct {
		Data   map[string]any
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}
	return resp.Data
}

// firstCacheFrame opens clusterCachesWatch and returns the `cache` payload of the
// first Added frame for cluster "1", decoded to a generic map — the cache-side
// analogue of clustersQueryData, used by the wire-shape tests; cache status is
// exposed through clusterCachesWatch, not the Cluster query.
func firstCacheFrame(t *testing.T, srvURL string) map[string]any {
	t.Helper()
	resp := openSSESubscription(t, srvURL, "",
		`subscription { clusterCachesWatch { type cache { id clusterID spec { serverUid } `+
			`conditions { type status reason } } } }`)
	t.Cleanup(func() { resp.Body.Close() })
	events := sseEvents(t, resp)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("cache stream closed before a frame arrived")
			}
			if ev.event != "next" {
				continue
			}
			var frame struct {
				Data struct {
					ClusterCachesWatch struct {
						Type  string         `json:"type"`
						Cache map[string]any `json:"cache"`
					} `json:"clusterCachesWatch"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode cache frame %s: %v", ev.data, err)
			}
			if frame.Data.ClusterCachesWatch.Cache["clusterID"] == "1" {
				return frame.Data.ClusterCachesWatch.Cache
			}
		case <-deadline:
			t.Fatal("timed out waiting for cluster 1 cache frame")
		}
	}
}

// --- Failing doubles ---
//
// Every resolver that returns (value, error) has an arm that only a failing
// service reaches. These doubles embed the interfaces rather than implementing
// them: the resolvers call a handful of methods, and anything else is a nil-call
// panic naming exactly what a test reached without meaning to.

// errClusterService fails every call the resolvers make with err.
type errClusterService struct {
	cluster.Service
	err error
}

type (
	errClusters struct {
		cluster.Clusters
		err error
	}
	errCaches struct {
		cluster.Caches
		err error
	}
	errCachedKinds struct {
		cluster.CachedKinds
		err error
	}
	errCachedData struct {
		cluster.CachedData
		err error
	}
)

func (e errClusterService) Clusters() cluster.Clusters       { return errClusters{err: e.err} }
func (e errClusterService) Caches() cluster.Caches           { return errCaches{err: e.err} }
func (e errClusterService) CachedKinds() cluster.CachedKinds { return errCachedKinds{err: e.err} }
func (e errClusterService) CachedData() cluster.CachedData   { return errCachedData{err: e.err} }

func (e errClusterService) ListEvents(context.Context, cluster.ObjectID, *string, *int) ([]cluster.Event, error) {
	return nil, e.err
}

func (e errClusterService) WatchEvents(context.Context, cluster.ObjectID, *string) (*cluster.Stream[cluster.EventWatchFrame], error) {
	return nil, e.err
}

func (e errClusterService) RetryConnection(context.Context, cluster.ClusterID) error { return e.err }

func (e errClusters) List(context.Context) ([]*cluster.Cluster, error) { return nil, e.err }

func (e errClusters) Get(context.Context, cluster.ClusterID) (*cluster.Cluster, error) {
	return nil, e.err
}

func (e errClusters) ReadActive(context.Context, cluster.ClusterID, func(context.Context, cluster.ActiveCluster) error) error {
	return e.err
}

func (e errClusters) WatchList(context.Context) (*cluster.Stream[cluster.ClusterWatchFrame], error) {
	return nil, e.err
}

func (e errClusters) WatchSchedule(context.Context, cluster.ClusterID) (<-chan cluster.Schedule, error) {
	return nil, e.err
}

func (e errClusters) SetEnabled(context.Context, cluster.ClusterID, bool) (*cluster.Cluster, error) {
	return nil, e.err
}

func (e errClusters) SetSyncEnabled(context.Context, cluster.ClusterID, bool) (*cluster.Cluster, error) {
	return nil, e.err
}
func (e errClusters) SetMonitoringEnabled(context.Context, cluster.ClusterID, bool) (*cluster.Cluster, error) {
	return nil, e.err
}

func (e errClusters) Delete(context.Context, cluster.ClusterID) error { return e.err }

func (e errClusters) ListEvents(context.Context, cluster.ClusterID, *string, *int) ([]cluster.Event, error) {
	return nil, e.err
}
func (e errClusters) WatchEvents(context.Context, cluster.ClusterID, *string) (*cluster.Stream[cluster.EventWatchFrame], error) {
	return nil, e.err
}

func (e errCaches) Get(context.Context, cluster.ClusterCacheID) (*cluster.ClusterCache, error) {
	return nil, e.err
}

func (e errCaches) List(context.Context) ([]*cluster.ClusterCache, error) { return nil, e.err }

func (e errCaches) ListByCluster(context.Context, cluster.ClusterID) ([]*cluster.ClusterCache, error) {
	return nil, e.err
}

func (e errCaches) Clear(context.Context, cluster.ClusterCacheID) (*cluster.ClusterCache, error) {
	return nil, e.err
}

func (e errCaches) WatchList(context.Context) (*cluster.Stream[cluster.ClusterCacheWatchFrame], error) {
	return nil, e.err
}

func (e errCaches) WatchHealth(context.Context) (*cluster.Stream[cluster.ClusterCacheHealth], error) {
	return nil, e.err
}

func (e errCaches) WatchStats(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCacheStats], error) {
	return nil, e.err
}

func (e errCaches) WatchSyncStatus(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCacheSyncStatus], error) {
	return nil, e.err
}

func (e errCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	return cluster.ClusterCacheHealth{}, false, e.err
}

func (e errCaches) SyncStatus(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheSyncStatus, bool, error) {
	return cluster.ClusterCacheSyncStatus{}, false, e.err
}

func (e errCachedKinds) Get(context.Context, cluster.ClusterCachedKindID) (*cluster.ClusterCachedKind, error) {
	return nil, e.err
}

func (e errCachedKinds) List(context.Context) ([]*cluster.ClusterCachedKind, error) {
	return nil, e.err
}

func (e errCachedKinds) ListByCache(context.Context, cluster.ClusterCacheID) ([]*cluster.ClusterCachedKind, error) {
	return nil, e.err
}

func (e errCachedKinds) SetSyncEnabled(context.Context, cluster.ClusterCachedKindID, bool) (*cluster.ClusterCachedKind, error) {
	return nil, e.err
}

func (e errCachedKinds) WatchByCache(context.Context, cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCachedKindWatchFrame], error) {
	return nil, e.err
}

func (e errCachedData) ListKinds(context.Context, cluster.ClusterID, cluster.ClusterCacheID) ([]cluster.ClusterCachedDataKind, error) {
	return nil, e.err
}

func (e errCachedData) WatchKinds(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCachedDataKindWatchFrame], error) {
	return nil, e.err
}

func (e errCachedData) WatchEvents(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (*cluster.Stream[cluster.ClusterCachedDataEventWatchFrame], error) {
	return nil, e.err
}

func (e errCachedData) WatchObjects(context.Context, cluster.ClusterID, cluster.ClusterCacheID, string, string) (*cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame], error) {
	return nil, e.err
}

func (e errCachedData) ListObjects(context.Context, cluster.ClusterID, cluster.ClusterCacheID, string, string) ([]cluster.ClusterCachedDataObject, bool, error) {
	return nil, false, e.err
}

func (e errCachedData) Query(context.Context, cluster.ClusterID, cluster.ClusterCacheID, string, int, int) (cluster.ClusterCachedDataQueryResult, bool, error) {
	return cluster.ClusterCachedDataQueryResult{}, false, e.err
}

// errAuth fails Current and Logout; the login flow's own failure arm is covered
// by fakeAuth.loginErr.
type errAuth struct {
	auth.Service
	err error
}

func (e errAuth) Current(context.Context) (auth.State, error) { return auth.State{}, e.err }
func (e errAuth) Logout(context.Context) error                { return e.err }
