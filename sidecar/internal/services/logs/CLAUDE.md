# internal/services/logs — the log cache and its views

The service reads container logs through the cluster's connection, keeps them in a file per cache
(`logstore`), and answers pages and follows over the file. The model never reaches a line through
it; the `LogsView` tool (`internal/tools/CLAUDE.md`) opens a view the user looks at. Specs:
`docs/specs/log-cache/`, `docs/specs/log-views/`.

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
- **The janitor** (`janitor.go`) sweeps on `Interval` and past `SweepBytes`: it first retries
  closing the chunks of ended tails whose close failed, then protects open, recently read and held
  chunks, evicts over-share streams first and never-read before read, then removes chunkless
  streams whose pod `PodsGone` says is gone (`podsgone.go`; unset, none), at most `sweepPods` pods
  a sweep, round robin. `Store.Sweep` runs one by hand.
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
- **A line reaches the model through `ForModel` alone** (`formodel.go`): stripped again, then
  redacted, at read time. The file and the wire to the viewer keep it unredacted.
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
  gaps from the backends (`fill.go`), walks (`walk.go`) and merges (`merge.go`). **A read is
  written a batch at a time as it arrives** (`window`, `WriteBatch`), each batch checked against
  the mirror first and stamped. A forward read is one window, its batches linked down as they land,
  and is cancelled once the stream holds the page. A backward read's windows (`readItem.windowEnd`)
  arrive newest first, each oldest first: a window links up only when it completes, and counts
  toward the page only then; one cut part way is an island linked to nothing, and the read's link
  down is written once it ends. `AtEnd` goes only on a completed first window. A backward read is
  stopped between windows (`readOpts.more`, answered once the window's last batch is written),
  never inside one. **A backward read sizes its windows** (`windows.go`). Off the log's end,
  `tailLines` windows double. Under an upper bound, windows of time start at the top of a second:
  the first is sized from the rate of the lines just above the gap (`rateAbove`; ten seconds with
  none), to hold about the page's kept lines with half again and at most 4 MiB, under half what
  the stream's budget has left. A window counts each line as the body carried it, before
  cleaning (`readLine`'s size); a rate from the file estimates that (`wireBytes`). A window
  checks the rate of its newest second as it reads; one due to hold four times the page, four
  times its byte target, or more than the stream has left is stopped (`readItem.windowCut`), left
  as an island, and replaced by a narrower one ending where it would have. A window that leaves
  the read short is followed by an older one sized from its own rate, eight times longer after
  silence. An `Around` page fills its `Head` half first; its `Tail` half reads back at that
  half's rate and links up to its first line (`streamFill.up`). The walks read up to
  `maxWalkBudget` (64 MiB) across the page's streams. A page never passes lines a stream did not
  read: a short stop becomes the cut. **A change to a read path answers to
  `TestPagesJoinWithoutLossOrRepeat` and `TestTheFileKeepsItsInvariant`** (seeded; fewer seeds
  under `-short`).
- **A head read never links** (`rotatedAway`, which the filler and the tailer share;
  `reachedFirst`): a read from a chunk's last line that found it rotated away writes a head chunk
  with no link, so the hole is a lost gap. **A record carries the hole before it** (`Record.Gap`,
  `holes.go`): every lost gap between two spans that lies between it and its stream's last line
  the view passes; one past a tailer's start once the tailer has committed past it. A walk gives
  a hole between two records to the later; its older edge's record, and a follow's first line on
  a stream, look back past the edge (`looker`, under `Retention.LookBytes`). → [ADR: a head read
  never links](../../../../docs/adr/2026-10-17-a-head-read-never-links.md).
- **`Service.Follow` walks the file from a watermark per stream** (`follow.go`). One **tailer**
  per stream (`tailer.go`) is shared through the file's **registry** (`registry.go`) and outlives
  its last follow by `TailGrace`. Tailers hand lines to **the file's writer** (`logstore/writer.go`)
  in batches; a tailer the writer cannot take from within `WriteWait` closes its chunk and reads
  from now, or ends with no follow. Every commit pings the file's bus (`Store.Subscribe`). A walk
  crosses a tailer's gap only up to `Tail.Committed`. **A walk that meets a live gap starts its
  fill on a goroutine of its own and stops** (`follow.fill`); the fill's end comes back to the
  follow's loop, which walks the stream on (`filledGap`). Until then the stream is `filling`: no walk
  reads it, its watermark waits at the gap, and the view counts it as waiting. Lines are ordered
  per container only. After each walk that sent a line the follow sends a `Checkpoint` (the oldest
  watermark); a follow opened `after` it misses no line. With no
  cursor it reads from `sinceTime`, never `tailLines=0`.
- **A follow's streams are one reconcile** (`follow.reconcile`), run after every mirror event
  (settled by `followSettle`), node change, stream end or retry. A container is tailed unless it
  ended at the `generation` the view sees now. A dropped container is held for a backoff up to
  `followRetryCap`. A `Pod` source leaves the view once its pod is gone by UID. **An ending the user
  can read is a `FollowEnded` frame, not an error** (any `Refusal`, via `asRefusal`); any other
  ending sets `Err`, and a cancel sets none.
