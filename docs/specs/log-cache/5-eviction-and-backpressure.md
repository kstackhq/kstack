---
title: Eviction and backpressure
scope: sidecar
status: Planned
---

# Eviction and backpressure

**Needs:** step 4, whose tailers this step sheds and whose writes it bounds. **Unblocks:**
step 6, and the sequence's integration gate runs at the end of this step.

Go paths are under `sidecar/internal/`. The design is the README's §6 and §7; this file says
what to build and how to know it is built.

## In short

The janitor runs. Each sweep measures the file, protects the open chunks and the chunks a page
read recently, evicts the rest least-recently-read first until the file and every stream are
under budget, removes the empty streams of pods that are gone, and vacuums. A page stamps the
chunks it read; a write stamps nothing, so a tailed firehose cannot hold the file. A tailer in
its grace that falls behind the writer ends, where an attached one reopens. `DefaultRetention`
is already wired in `app`; this step makes it live, and the whole suite, both coverage gates
and the lints run at the end.

## What is not in this step

- **No compression**, as the README's decision says.
- **No preference for evicting chunks whose backend still holds them.** Every chunk is evicted by
  its stamp alone. The README names this as later.
- **No shed order across tailers.** Each tailer decides for itself when it falls behind (§3); the
  registry ranks nothing.

## Design

### 1. `read_at_ms` is a page's stamp

**A tail's write protects nothing; a page's fill does.** `chunks.read_at_ms` becomes nullable,
and **`putChunk` never touches it**: its upsert inserts `NULL` and its update leaves the row's
value alone, with no `max()` or `coalesce`, since either answers `NULL` beside one. A page's fill
was made for a reader that has not read it yet, so `Store.Write(ctx, fills, stamp)` with `stamp`
set runs one more statement in the fill's transaction, `UPDATE chunks SET read_at_ms = ?now
WHERE id IN (...)` over every chunk the fills wrote or extended: a sweep between the fill's
commit and the walk, one in progress or one the fill's own bytes woke, would otherwise evict
exactly the old history a backward page just fetched, and the next page would fetch it again. A
page then stamps every chunk its walk read a line from, after the walk and before it answers, in
one write: the same `UPDATE` with `AND (read_at_ms IS NULL OR read_at_ms < ?now - ReadTTL/10)`,
so a chunk the viewer sits on costs one write a minute, not one a page. A stamp that fails is
logged and the page is still answered. The walk already loads the stream's chunks for coverage,
so it knows which ones it read from.

**A follow stamps nothing**, neither its walk nor its gap fill: what it sends is in the viewer's
window, and a stamped gap fill would let a followed firehose back in through the side door —
the writer falls behind, the tailer reopens from now, and the follow fetches the very lines the
writer could not take, protected for `ReadTTL`, so the stream runs over its share by the write
rate times ten minutes exactly when the machine is already overloaded. The fill has to survive
only until the follow's own walk, which runs at once, so the follow passes `stamp` false and
**holds the chunks `Write` answers** (`Store.HoldChunks(ids) (release func())`, the holds'
mechanism over chunk ids, which the sweep's candidates skip) until that walk returns. A page
needs no chunk hold, since its stamp lands with its fill.

**A fill whose row is gone is skipped, not failed.** The page's fills commit in one transaction,
and a row the janitor removed between a lookup and its hold would fail that transaction's foreign
key check and take every other stream's fill down with it. So `Write` checks each fill's stream
row inside the transaction, writes nothing for a fill whose row is gone, and answers which fills
it skipped; the page reads such a stream as having nothing, as it reads any stream with no
chunks, and a follow's fill that is skipped fails that stream as a failed fill does (§4).

The stamp is unix milliseconds from the manager's clock: `NewManager(dir, ret, now)` takes it
as a parameter, `time.Now` in `app`, and the two `time.Now()` calls in `fill.go` and
`writer.go` read it instead.

So the file holds a firehose only to its share: the open chunk is at most `ChunkBytes`, and the
rest of a stream nobody pages is unprotected from the moment it is written. A viewer following
the firehose is served from the viewer's own window, and a scroll back past what the window
kept pages the file, which refills what it evicted from the kubelet while the kubelet still has
the lines, and crosses a lost gap once it does not.

### 2. Ids never come back

`streams.id` and `chunks.id` gain `AUTOINCREMENT`, under the pre-release schema-edit policy. A
tailer, a follow's watermark, a page's stamp list and the janitor's pick all name a row by id
across transactions, and a deleted id reused would hand one of them another stream's lines.

### 3. Backpressure

Step 4 built the mechanism: a tailer that cannot hand the writer a batch within `WriteWait`
stops its read, closes its chunk at the last committed line and reopens from now, leaving a
live gap (`tailer.tail`, `registry_test.go`'s backpressure test). This step adds one rule. **A
tailer with no follow attached ends instead of reopening.** The decision is taken under the
registry's lock, where `refs` is read and `stopping` set in the same critical section, so an
attach that lands first keeps the tailer, which reopens, and one that lands after waits on
`done` and starts a fresh tailer, as it does for any stopping one. An attached tailer behaves
as today, so a follow sees nothing new. A tailer that ends this way stops its grace timer, which
would otherwise fire on a tailer the registry has dropped. That is the whole of the rule: under
a writer that cannot keep up, a tailer nobody is reading stops competing the first time it is
the one that times out, and the ones being read lose a gap each.

### 4. The sweep

`logstore/janitor.go`, one goroutine per open `file`, started by the manager's open when
`Interval` is not zero and stopped by the file's close, which cancels the file's context and
joins it; a close mid-statement returns. Two things wake it: the ticker, every `Interval`, and
**the writer's byte count**: every commit of lines, a tailer's batch or a fill, adds its
message bytes to a counter on the file, and the commit that carries it past `SweepBytes`
resets it and wakes the janitor through a capacity-one notify. The janitor's own writes count
nothing, so a sweep never wakes the next. Overshoot is bounded by `SweepBytes` plus what lands
during a sweep, and a quiet file is swept once an `Interval`.

`Store.Sweep(ctx)` runs one sweep synchronously and is what the goroutine calls; a test drives
it by hand. One sweep, in order:

1. **Measure**: `bytes` summed over `chunks` per stream, one statement. `bytes` is message
   bytes, not file size: row and index overhead and the WAL sit on top, and a line two chunks
   claim counts twice, so the file on disk runs over `Bytes` by a constant factor and the
   footprint `kubestore`'s `SizeLimit` sums is a different number.
2. **Protect** every `open` chunk and every chunk whose `read_at_ms` is within `ReadTTL` of
   the clock. Everything else is a candidate.
3. **Evict** while the file is over `Bytes` or any stream over `StreamShare`: the next
   candidate is one of an over-share stream when there is one, else any; among those, a
   never-read chunk before a read one, the oldest `from_ns` first among the never-read and the
   oldest `read_at_ms` among the read. Each goes through `Store.Evict(ctx, id, cutoff)`, one
   transaction per chunk, so the tailers' batches interleave between evictions and no
   statement holds the writer longer than one chunk's delete. **`Evict` checks protection
   again**: the candidates were one snapshot and a page can stamp a chunk between it and the
   chunk's turn, so `stmtSelectChunkBounds` takes `read_at_ms IS NULL OR read_at_ms < ?cutoff`
   beside its `open = 0`, and a chunk that fails it is left where it is; the walk test's
   by-hand eviction passes a cutoff past every stamp. A sweep that is over budget with no
   candidate left stops; it logs one line entering that state and one leaving it, never one
   per sweep.
4. **Remove streams**: every stream with no chunks that no one holds and whose pod the mirror
   no longer holds, in one statement per stream that checks the chunk count again. **A hold is
   in memory on the file** (`Store.Hold(rows) (release func())`): a page holds each row the
   moment a lookup answers its id — `PutStreams`' and `filestreams.go`'s alike — until it has
   answered; a follow holds the rows its resolve and each reconcile name until it ends; and a
   tailer holds its row from its start to its end. The janitor reads the holds and runs the
   delete under the holds' lock, so a row is removed before its hold or never. A row removed
   before its hold is a gone pod's, which no backend serves: a page's walk over it reads
   nothing, as a stream with no chunks had nothing to walk, and a fill that meets it is skipped
   by `Write` (§1), the follow then failing that stream as any failed fill does until the next
   reconcile drops the pod with it. **Which pods
   are gone is the service's answer**: `Manager.SetPodsGone(fn)`, read under the manager's
   lock since files are open and sweeping before `logs.New` sets it, over the cluster service
   — the cache's cluster through `Caches().Get`, then one `CachedData().ReadActive` on it,
   answering nothing gone unless the active cache is this file's and the reading holds pods,
   and otherwise the UIDs among those asked that the `objects` view lacks, at most `sweepPods`
   (500) a sweep. Unset, the janitor removes no stream. A stream with an empty `pod_uid` is
   never removed by this rule.
5. **Vacuum**: `PRAGMA incremental_vacuum` bounded by `VacuumPages`, gated on
   `freelist_count`, then a `TRUNCATE` checkpoint when the WAL is larger than the file, as
   `kubestore`'s sweep does.

A sweep reports what it did through a hook on the manager (`sweeps`, a function of the file's
cache id and a `sweepReport`: bytes before and after, chunks evicted, streams removed, and
whether it stopped short), nil in production, which a `logstore` test sets to wait on. It logs
at `Debug` per sweep and at `Warn` on a failed statement.

**`Store.Evict` is measured before it is trusted.** `stmtEvictLines` runs a correlated
`NOT EXISTS` over the stream's chunks per line; a full `ChunkBytes` chunk of short lines is
ten thousand rows. The task below times it, and a delete past 100 ms is rewritten as range
deletes between the neighbouring chunks' edges before the janitor lands.

### 5. Wiring

`app` already passes `logstore.DefaultRetention`; it now passes `time.Now` beside it, and
`logs.New` sets the manager's `PodsGone`. `Retention` gains `SweepBytes`, 16 MiB in
`DefaultRetention`, a sixteenth of `Bytes`. Tests about anything else keep a zero `Interval`.

## Tasks

1. **The stamp.** `read_at_ms` nullable, `AUTOINCREMENT` on `streams` and `chunks`, the
   manager's clock, `Write`'s `stamp` and its skip of a gone row, the page's stamp after the
   walk, the follow's chunk hold, `putChunk` leaving `read_at_ms` alone.
2. **Measure `Evict`.** Time it on a full chunk of 100-byte lines and of 16 KiB lines over a
   stream of a hundred chunks; rewrite it as range deletes if any delete passes 100 ms.
3. **The sweep.** `janitor.go`, `Store.Sweep`, `Evict`'s cutoff, `Store.Hold` and its three
   holders, `Store.HoldChunks`, the byte counter and notify, `SetPodsGone` and its reader in
   `services/logs`, the hook, the log lines.
4. **Backpressure.** The grace rule in `tailer.run` and `registry`, the timer stopped.
5. **The gate.** The full checks under *Verification*; raise a coverage threshold if CI reports
   the number rose.

## Tests

Go, beside the files they cover, under a shrunk `Retention` and a clock the test moves. The
sweep tests call `Store.Sweep` by hand, so a negative assertion ("this chunk survives") reads
the file after a synchronous sweep and needs no window; the one test with the janitor live
waits on the manager's hook.

- `janitor_test.go`: a chunk read within `ReadTTL`, a chunk a stamped fill wrote within it, a
  held chunk and an open chunk survive a sweep over budget, and the held one goes once released;
  a chunk stamped after the candidates were picked survives its turn; a tail's chunk nobody read
  goes before a read one, and the oldest first within each; a `Write` with a fill of a gone row
  writes the other fills and answers that one skipped; an
  over-share stream's chunks go before an older chunk of a stream under its share; a tailed
  stream written past `StreamShare` with nothing paged settles to its open chunk after one
  sweep, and a file written past `Bytes` the same; a sweep over budget with no candidate left
  reports it and logs once across repeats; a stream with no chunks goes when `PodsGone` names
  its pod and stays while a hold is on it, while its pod is held, while its `pod_uid` is empty,
  and with no `PodsGone` set; a hold taken while a sweep runs is seen or the row is already
  gone, never a hold on a row the sweep then takes; the vacuum runs only with a freelist; a
  close mid-sweep returns;
  with the janitor live, a write past `SweepBytes` is followed by a sweep and an eviction's own
  writes are not.
- `fetch_test.go`: a page's fill lands stamped, and a sweep over budget between the fill and
  the walk leaves the page whole; a page stamps the chunks its walk read from and not one its
  cut left unread; a second page inside `ReadTTL/10` rewrites nothing; a page holds a gone
  pod's rows from the lookup on; a page over an evicted chunk fills it again when the fake
  still has the lines, and answers `more: false` at a `head` chunk when it does not.
- `follow_test.go`: a follow's walk and its gap fill stamp nothing, and the fill's chunks are
  held until the walk returns, so a sweep over budget between the two leaves the follow's lines
  whole; a follow holds its streams' rows while it runs; a gap fill `Write` skipped fails that
  stream as a failed fill does.
- `registry_test.go`: a tailer in its grace that cannot hand over a batch ends with its grace
  timer stopped, and the next attach starts one afresh at its watermark; an attached one
  reopens from now, as the existing test pins.
- `walk_test.go`: the seeded walk keeps its by-hand `Evict` step and adds a `Store.Sweep` step
  under a shrunk budget, so the janitor's own pick runs inside the invariant and a failing seed
  still reproduces.

## Security

**Widens:** nothing. Eviction never leaves a chunk half deleted, since the lines and the chunk
go in one transaction, so a page never reads a range the file claims and does not hold. The
`PodsGone` reader runs the sidecar's own statement over the mirror with the UIDs bound as one
JSON argument.

**Residual:** none in this step.

## Known gaps

- A page over a firehose stamps what it fetched, so a user paging back through one protects up
  to a page's fill budget per page for `ReadTTL`. That is bounded by how fast a person scrolls,
  and is the one way a tailed stream exceeds its share.

## When it lands

- `sidecar/CLAUDE.md`: the `logstore` paragraph loses "a `lifecycle.Part` with no background
  work" and "while the rest wait for the janitor", and gains the janitor — the wakeups, the
  stamp rule, the order, the stream rule, the knobs — and the grace rule beside the tailer's
  reopen; the `Manager` doc comment and the schema's `read_at_ms` comment say the same.
- This README's row reads Built, with the pull request, and the sequence's integration gate is
  recorded as passed.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/...` while working; at the end, the
integration gate: `make test`, `make lint`, `make vet`, `make cover-go`, `make cover-js`, with
no gate lowered.
