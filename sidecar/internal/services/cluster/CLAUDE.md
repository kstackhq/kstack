# internal/services/cluster — the cluster subsystem

One file per family (`clusters.go`, `caches.go`, `cachedkinds.go`, `cacheddata.go`), plus
`mirror.go`, `stream.go`, `events.go`, and private leaves `internal/kubeconn`, `kubesync`,
`kubestore`. **Leaves speak native vocabulary, never records**; put mechanism in a leaf. → [ADR:
one package over private
leaves](../../../../docs/adr/2026-09-30-the-cluster-service-is-one-package-over-private-leaves.md).

**A cluster is a row in `app.db`; beehive holds its runtime.** The mirror (`mirror.go`) is the one
writer of each row's `Cluster` object, named by the row id, through `runtimeSpecOf(row)`.
`updated_at` moves on a user edit, never a reconcile. → [ADR: a cluster is a row in
app.db](../../../../docs/adr/2026-09-16-clusters-are-rows-mirrored-into-beehive.md).

**The chain**: the `ClusterSource` pass imports one row per kube-context, never updated or deleted
→ the mirror → the cluster pass (a `kubeconn` claim, then the `ClusterCache` for the probed
identity) → the cache pass (discovery, `ClusterCachedKind` records) → each kind's sync.
**kubesync decides what exists; the records decide what is mirrored.** → [ADR: beehive control
plane](../../../../docs/adr/2026-08-09-beehive-control-plane.md), [ADR: discovery as a beehive
kind](../../../../docs/adr/2026-08-18-discovery-as-a-beehive-kind.md), [ADR: arming is
policy](../../../../docs/adr/2026-08-28-arming-is-policy-never-interest.md).

## Controllers and passes

- A pass returns `Settled()`, `Unsettled()` or `Fail(err)`, never an error.
- Every controller registers `startupPass`. `ClusterSource` and `Cluster` also take a resync
  interval, since each observes something the store cannot see move (a file, a remote server): **a
  fold whose answer the store cannot see move owes a resync.**
- Write status unconditionally — beehive suppresses equal bytes. No timestamps or counters on
  status. → [ADR: two conditions, no
  timing](../../../../docs/adr/2026-09-02-cluster-conditions-two-subjects.md).
- A parent creates its children in the child's file, one write, no read. A deletion-pending pass
  writes nothing.
- **A cache carries `cacheFinalizer` from creation**, cleared only after `ForgetCache`,
  `kubestoreMgr.Remove` and the log file's removal succeed.
- A relayed value needs a `depends_on` edge; the owner edge is not one.
- **Both reconciles defer until the kubeconfig has been read** — keep the guards if you reorder
  startup.
- Tell a kubeconfig row by `kubeconfigContextOf(spec)`, never by name.
- Timestamps are nullable `Time` bound to value `time.Time`, and **the delta-watch diff compares
  frames with `==`**: no slice, map or pointer field on a record or `WatchFrame`.
- **Deletion is the mark.** `Clusters().Delete` marks and returns (refusing a row its source still
  declares, `ErrDeclaredBySource`; the toggles refuse a marked row as `ErrNotFound`); the mirror tears down and the
  row goes last. The connection gate reads the mark off the **row** (`connectableContext`).
- Neither cache controller writes a condition. → [ADR: kind records mirror the
  catalog](../../../../docs/adr/2026-09-02-kind-records-mirror-the-catalog.md).
- Shared dependencies are fields of `deps`, never constructor parameters (`testutil_test.go`).
- Every participant is a named `lifecycle.Part`, never a stop closure. → [ADR: lifecycle
  composition](../../../../docs/adr/2026-08-16-lifecycle-composition.md).

## Identity

A context is not an identity. Ask `Lease.ConnFor(ctx, serverUID)`; **never correlate a connection
with `State.ServerUID`**, which lags. `ConnFor` never waits. → [ADR: connection-carried
identity](../../../../docs/adr/2026-08-25-connection-carried-identity.md), [ADR: identity-driven
retirement](../../../../docs/adr/2026-08-27-identity-driven-retirement.md).

## Events

Three timelines keyed `(ObjectID, category)`, one read path (`events.go`). `Event.id` is unique
within a timeline only. → [ADR: event timelines](../../../../docs/adr/2026-09-02-event-timelines.md).

## Streams and watches

- Every send goes through `sendFrame`; a bare send leaks the goroutine.
- One pump, `deltaWatch`, serves every record watch. Never add another.
- A watch that can die returns `*Stream[T]`; `Err` is set before `Frames` closes.
- Reads never filter. The cluster list watch rereads on `KeyClusters` **and** the runtime
  `WatchList`. Only the `Deleted` is promised.
- Gauges are current-on-subscribe, no `Bookmark`. → [ADR: cache health
  fold](../../../../docs/adr/2026-09-02-cache-health-fold.md).
- A cache at its size ceiling stops syncing, held by its record's condition; nothing evicts. →
  [ADR: a stopped cache is held by its
  record](../../../../docs/adr/2026-09-03-a-stopped-cache-is-held-by-its-record.md).

## Families and the GraphQL surface

Four families: `Clusters()`, `Caches()`, `CachedKinds()`, `CachedData()`. The scope is the entry
point, never an argument. Every GraphQL type binds 1:1 in `gqlgen.yml`; resolvers are one-liners.

- Every delta watch closes its snapshot with one `FrameBookmark` (nil entity). → [ADR: delta-watch
  protocol](../../../../docs/adr/2026-08-09-delta-watch-protocol.md).
- Gauges are their own subscriptions, never a field on a record.
- `Cluster.caches` is a set; activeness is a live join (`CacheIsActive`).
- **`Clusters().ReadActive` is the one reading of a cluster at one identity**; `read` may run
  twice, so it must set, never append. **`CachedData().ReadActive`** reads the active cache by SQL,
  every statement on one snapshot. → [ADR: the cluster service resolves a
  cluster](../../../../docs/adr/2026-09-27-the-cluster-service-resolves-a-cluster.md), [ADR: a cache reading
  is one snapshot](../../../../docs/adr/2026-10-13-a-cache-reading-is-one-snapshot.md), [ADR: the sidecar's
  own mirror reads keep their
  connections](../../../../docs/adr/2026-10-13-the-sidecars-own-mirror-reads-keep-their-connections.md).
- Every cluster mutation returns through `clusterErr` (`graph/util.go`).
- A resolver over a `*cluster.Stream` uses `watchStream`, never `ptrStream`. → [ADR: watch-failure
  reporting](../../../../docs/adr/2026-08-14-watch-failure-reporting.md).
- Every condition is built by `LiveCondition`; `Unconfirmed` is load-bearing on the wire. → [ADR:
  liveness conditions](../../../../docs/adr/2026-08-09-liveness-conditions.md).

## The connection pool (`internal/kubeconn`)

A cluster is the only way to address a connection. → [ADR: addressed by
ClusterID](../../../../docs/adr/2026-08-22-connections-addressed-by-cluster-id.md), [ADR: one connection
per context](../../../../docs/adr/2026-08-23-one-connection-per-context.md).

- `Acquire` never fails or waits; `Conn` never dials.
- `RetryAndWait` returns once the probe it asked for ran. → [ADR: retry resolves with its
  probe](../../../../docs/adr/2026-08-30-retry-resolves-with-its-probe.md).
- Every non-stream request has an idle-read bound, never a deadline. → [ADR: idle-read
  bound](../../../../docs/adr/2026-09-02-idle-read-bound.md).
- `WatchState` delivers nothing on attach — pair it with `State()`. Every value is a level.
- Five `Observation[T]`s; a value survives a failure. `NotFound` and `Unsupported` are both a 404.
  → [ADR: probe engine](../../../../docs/adr/2026-08-24-probe-engine.md).
- Reaching the server is `GET /api`. → [ADR: the connection probe dials
  /api](../../../../docs/adr/2026-08-25-connection-probe-dial.md).

## The supervisor (`internal/lib/supervisor`)

Kubernetes-free scheduling. A **job** runs and returns; a **worker** blocks until stopped (the
kind sync is the one worker). A `Result` is its schedule. Commit only on a change. `Remove` and
`Close` wait — never call them inside a `Run`. Gate a revival wake on `Suspended()`. → [ADR: probe
engine](../../../../docs/adr/2026-08-24-probe-engine.md), [ADR: supervisor
vocabulary](../../../../docs/adr/2026-08-28-supervisor-vocabulary.md), [ADR: jobs and
workers](../../../../docs/adr/2026-08-28-jobs-and-workers.md).

## The sync engine (`internal/kubesync`)

Speaks cache ids, contexts, server UIDs and GVRs; never records.

- A clear's `fn` (`RunWithCacheSyncStopped`) must not call back into the Service.
- Two arming levels that AND: `TrackDiscovery` and `TrackKind`.
- Nothing syncs into a cache whose connection does not vouch for its `ServerUID`.
- A verdict is a gauge; `false` from `Get*State` means not yet observed.
- Snapshot `s.tracked` under `s.mu`, act outside it.

The sweep (`discovery.go`) writes only on a changed fingerprint. → [ADR: discovery sweep
rules](../../../../docs/adr/2026-09-02-discovery-sweep-rules.md). The kind sync (`kinds.go`) is one stream
per run; a quiet window reopens from the cookie and an expired reopen relists in place. → [ADR:
kind sync verdicts](../../../../docs/adr/2026-09-02-kind-sync-verdicts.md), [ADR: a quiet watch is
reopened](../../../../docs/adr/2026-09-27-a-quiet-watch-is-reopened-from-its-cookie.md), [ADR: an expired
quiet reopen relists in
place](../../../../docs/adr/2026-09-29-an-expired-quiet-reopen-relists-in-place.md).

## The store (`internal/kubestore`)

One SQLite file per cache behind a refcounted `Manager`. → [ADR: one store per
cache](../../../../docs/adr/2026-08-26-cache-store-per-cache.md).

- Nothing on the read side creates a file.
- `ForgetCache` before `Manager.Remove`; `ForgetKind` before `Store.ClearKind`.
- Every delete logs to `deletes` first; readers apply deletes before writes. → [ADR: write
  positions and the deletes log](../../../../docs/adr/2026-08-30-write-positions-and-the-deletes-log.md).
- Every statement is in `statements.go`, no `OnBoth`. → [ADR: SQL
  discipline](../../../../docs/adr/2026-09-02-kubestore-sql-discipline.md).
- **Every SQLite file is owner-only** (umask 0o077, `sqlitepool.OpenWriter` chmods). → [ADR: the
  profile ACL protects the Windows
  files](../../../../docs/adr/2026-09-02-windows-cache-files-rely-on-the-profile-acl.md).
- One janitor per file. → [ADR: janitor](../../../../docs/adr/2026-09-02-kubestore-janitor.md).
- `Retention.SizeLimit` caps the three files summed. → [ADR: bound the cache by total
  size](../../../../docs/adr/2026-09-03-bound-the-cache-by-total-size.md).
- **Side tables are written with their object, off the sanitized body** (`containers.go`,
  `refs.go`, `selectors.go`). A non-selectable selector writes no row: read `Requirements()`' flag,
  never the empty list. → [ADR: KubeQuery reads
  tables written with the
  object](../../../../docs/adr/2026-09-27-kubequery-reads-tables-written-with-the-object.md).
- **KubeQuery reads `Store.Query`** (`query.go`) through `TEMP` views (`query_views.sql`), the
  tool prompt's contract. Traps: `body()` caps inflation at 16 MiB; the `refs` view's lookup stays
  a `CROSS JOIN` (`TestTheRefsViewFindsATargetByItsIndex`); the `LIMIT` wrap is prepared first,
  then the statement alone; caller values bind, never paste. → [ADR:
  KubeQuery reads views on a connection of its
  own](../../../../docs/adr/2026-09-27-kubequery-reads-views-on-a-connection-of-its-own.md).
- The change signal is a coalesced ping, never a row delta. → [ADR: ping
  bus](../../../../docs/adr/2026-08-26-store-change-ping-bus.md).

**Cached-data watches** (`cacheddatawatch.go`): subscribe, snapshot, `Bookmark`, then a read per
burst past a `Cursor`. No read loads a body. → [ADR: cached-data
reads](../../../../docs/adr/2026-08-26-cached-data-read-loop.md), [ADR: objects read
split](../../../../docs/adr/2026-08-29-object-read-split.md).
