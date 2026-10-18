# internal/services/logs — the log cache and its views

The service reads container logs through the cluster's connection, keeps them in a file per cache
(`logstore`), and answers pages and follows over the file. A line reaches the model through
`LogsRead` and the View section, both through `ForModel`; the `LogsView` tool
(`internal/tools/CLAUDE.md`) opens a view the user looks at. Specs:
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
  the log store and is the cluster service's `CacheFileOwner`). A line reaches the model through
  `LogsRead` and the View section, both through `ForModel`. Modes `Head`, `Tail`, `Around` (limit
  ≤500; `compileGrep` refuses a bad grep, on follows too). **A page takes a range**: `Since` and
  `Until` in place of `Cursor`, a moment's lines in it, a line's not (`boundIn`). The far bound is
  applied in the walk next to the near one (`pageQuery.far`, `clampTo`), and the filler clamps each
  gap's download to the range by passing it in through `readOpts` (`fillGap`), so
  `reader.lines` stays the one place a bound is applied. A page that stopped at its far bound says
  nothing is left that way. `LineCursor` spells a line's cursor for a caller with its own form.
  A page resolves, files streams, adds gone pods' streams the file holds (`filestreams.go`), fills
  gaps from the backends (`fill.go`), walks (`walk.go`) and merges (`merge.go`). **A read is
  written a batch at a time as it arrives** (`window`, `WriteBatch`), each batch checked against
  the mirror first and stamped. A forward read is one window, its batches linked down as they land,
  and is cancelled once the stream holds the page. A backward read's windows (`readItem.windowEnd`)
  arrive newest first, each oldest first: a window links up only when it completes, and counts
  toward the page only then; one cut part way is an island linked to nothing, and the read's link
  down is written once it ends. `AtEnd` goes only on a completed first window. A backward read is
  stopped between windows (`readOpts.more`, answered once the window's last batch is written),
  never inside one. **A backward read sizes its windows** (`windows.go`). Off the log's end it
  reads one `tailLines` window, read whole, then windows of time ending where it began; under an
  upper bound, windows of time alone. They start at the top of a second: the first is sized from
  the rate of the lines just above the gap (`rateAbove`; ten seconds with none), to hold about the
  page's kept lines with half again and at most its byte target. A window counts each line as the
  body carried it, before cleaning (`readLine`'s size); a rate from the file estimates that
  (`wireBytes`). A window checks the rate of its newest second as it reads; one due to hold four
  times the page or four times its byte target is stopped (`readItem.windowCut`), left as an
  island, and replaced (`reader.replace`): from the second after its stop, by the one-second
  window when stopped in its last second, or halved when stopped in its first, whose second then
  bounds widening until a window from it completes. A window that leaves the read short is
  followed by an older one sized from its own rate, eight times longer after silence. An `Around`
  page fills its `Head` half first; its `Tail` half reads back at that half's rate and links up to
  its first line (`streamFill.up`).
- **A page reads within limits** (`limits.go`, `caps`): 16 MiB and 16 GETs a stream, 64 MiB a
  query (each direction of a page is one). Each stream filled gets an allowance, the smaller of
  16 MiB and 64 MiB ÷ streams, which it spends until its first window ends; the rest seeds a pool
  shared by later windows. A window of time takes its byte target and reserves four times it
  (`limits.take`); what it does not use goes back. A reservation is charged, not enforced. A fill
  reads no gap past its stream's bytes or GETs, and gives back what it holds when it ends. A
  backward read checks its limits only when a window completes, and is cut there; a forward read
  is cut inside its GET, never before its first line in range. A follow's fill reads under the
  smaller of 16 MiB and 64 MiB ÷ its streams (`followLimits`) and is cut only at a second's end.
  Each walk reads its share of `maxWalkBudget` (64 MiB) plus what its fill downloaded. A page
  never passes lines a stream did not read: a short stop becomes the cut, and a stream cut before
  a line in range refuses the page. **A change to a read path answers to
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
