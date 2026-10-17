# internal/services/logs — the log cache and its views

The service reads container logs through the cluster's connection, keeps them in a file per cache
(`logstore`), and answers pages and follows over the file. The model never reaches a line through
it; the `LogsView` tool (`internal/tools/CLAUDE.md`) opens a view the user looks at. Spec:
`docs/specs/log-views/`. Planned work and known gaps: [`docs/TODO.md`](../../../../docs/TODO.md#sidecar--log-cache).

## The file (`logstore`)

**`services/logs/logstore` is the log cache's file**: one owner-only SQLite database per cluster
cache at `<cache>/logs/<cache id>.db` (schema `migrations/0001_init.sql`: `streams`, `chunks`,
`lines`). → [ADR: the log cache](../../../../docs/adr/2026-10-17-the-log-cache.md).

- `Manager` (`manager.go`) hands out refcounted `Store`s by cache id. An open, close or delete runs
  outside its lock, one at a time per cache. Every operation goes through `Store.use`, and a close
  waits for them. `Clear` deletes the file; `Remove` also refuses the id for the manager's life;
  both still work after `Close`. A delete removes `-wal` and `-shm` first, or a fresh database
  would replay the old WAL.
- `Retention` (`retention.go`) holds every knob. Ids never come back (`AUTOINCREMENT`).
- **A chunk's `read_at_ms` is a page's stamp alone**; tailers and follows stamp nothing.
  `Store.WriteHeld` holds a follow's gap fill from commit until its walk returns.
- **The janitor** (`janitor.go`) sweeps on `Interval` and past `SweepBytes`: it protects open,
  recently read and held chunks, evicts over-share streams first and never-read before read, then
  removes chunkless streams whose pod `PodsGone` says is gone (`podsgone.go`; unset, none), at most
  `sweepPods` pods a sweep, round robin. `Store.Sweep` runs one by hand.
- `statements.go` is the file's `sqlstmt.Set`. **Coverage is a pure function** (`CoverageOf`,
  `coverage.go`). **A `Fill` is one read of one gap** (`fill.go`). **The trim** (`trim.go`) cuts a
  finished stream at the end of `finished_at`'s second (container times are whole seconds).

## The service

**`services/logs` holds a view's vocabulary** (`view.go`: `View`, `SourceKind`, `FilterField`,
`MirrorKind`). The root `.gitignore` ignores `logs`, so the package has its own exception there.

- **A view resolves inside one `ReadActive`** (`sources.go`); workloads walk down controller
  references (`podsOf`). With no container named, each pod's default (`containersOf`: the
  `default-container` annotation, else the first, never an init container). A stream is named by its
  `StreamKey` and carries its instance's runtime id; it also carries the snapshot its file row keeps
  once the pod is gone. A source with nothing now is not refused: it carries a `SourceState`
  (`Reading`, `Missing`, `Idle`). Refused: `Unmirrored`, `NoNodes`, more than `maxStreams`
  containers, or a reading cut at its row or byte cap. `Refusal` is a value, so no caller can change
  a sentinel's words. Tests run over `cluster/clustertest`.
- **Every message goes through `stripControl`** (`clean.go`) before grep and the wire; a refusal's
  message too.
- **A cursor names a line** (`cursor.go`): moment, container, instance id, ordinal. `streamBound`
  reads one cursor as each stream's bound. → [ADR: a log cursor is a position](../../../../docs/adr/2026-10-17-a-log-cursor-is-a-position.md).
- **A container's log is read over a claim** (`claim.go`), one raw `GET` of the `log` path through
  the connection's `HTTPClient`; a cut body answers its reason, never a bare cancel. A follow's
  claim also watches the record and revokes on disable or delete.
- **A read goes through a `Backend`** (`backend.go`); `Service.backends` is the ordered list (the
  kubelet alone, `kubelet.go` over `fetcher.go`, kubetail's algorithm), a test field. The grep runs
  where lines are read. **`reader.lines` is the one place a bound is applied.** **`failureKind` is the
  one classification of a failed read**: fatal (401, revoked) fails the page or follow; denied (403)
  and ended (other <500) end one stream; transient (5xx, 408, 429, transport) is skipped by a page
  and retried by a follow.
- **`Service.Fetch` answers from the file** (`fetch.go`; built in `app` as `logs.New`, which owns
  the log store and is the cluster service's `CacheFileOwner`). The model reaches no line through
  it. Modes `Head`, `Tail`, `Around` (limit ≤500; `compileGrep` refuses a bad grep, on follows too).
  A page resolves, files streams, adds gone pods' streams the file holds (`filestreams.go`), fills
  gaps from the backends (`fill.go`, buffered, then `checkInstances`, then one write), walks
  (`walk.go`) and merges (`merge.go`). A page never passes lines a stream did not read: a short
  stop becomes the cut. **A change to a read path answers to `TestPagesJoinWithoutLossOrRepeat` and
  `TestTheFileKeepsItsInvariant`** (seeded; fewer seeds under `-short`).
- **`Service.Follow` walks the file from a watermark per stream** (`follow.go`). One **tailer**
  per stream (`tailer.go`) is shared through the file's **registry** (`registry.go`) and outlives
  its last follow by `TailGrace`. Tailers hand lines to **the file's writer** (`logstore/writer.go`)
  in batches; a tailer the writer cannot take from within `WriteWait` closes its chunk and reads
  from now, or ends with no follow. Every commit pings the file's bus (`Store.Subscribe`). A walk
  crosses a tailer's gap only up to `Tail.Committed`. After each walk that sent a line the follow
  sends a `Checkpoint` (the oldest watermark); a follow opened `after` it misses no line. With no
  cursor it reads from `sinceTime`, never `tailLines=0`.
- **A follow's streams are one reconcile** (`follow.reconcile`), run after every mirror event
  (settled by `followSettle`), node change, stream end or retry. A container is tailed unless it
  ended at the `generation` the view sees now. A dropped container is held for a backoff up to
  `followRetryCap`. A `Pod` source leaves the view once its pod is gone by UID. **An ending the user
  can read is a `FollowEnded` frame, not an error** (any `Refusal`, via `asRefusal`); any other
  ending sets `Err`, and a cancel sets none.
