---
title: Tailers and the follow
scope: sidecar
status: Planned
---

# Tailers and the follow

**Needs:** step 3, whose chunks a tailer extends and whose fills a follow runs over a gap.
**Unblocks:** step 5, whose backpressure sheds the tailers this step runs.

Go paths are under `sidecar/internal/`. The design is the README's §5 and the follow half of
§9; this file says what to build and how to know it is built.

## In short

One tailer per stream and pattern, shared by every follow that wants it and kept `TailGrace`
after the last lets go, writes lines to the file through one writer. `Service.Follow` becomes a
walk of the file from a watermark per stream, run again on every commit to those streams, so a
line reaches a follow from the file and never from the tailer. A watermark never crosses a live
gap unfilled, which is what makes a checkpoint true by construction. `logsFollow` sends
`LogsFollowFrame`s with a `type`, and the webview's generated types follow.

## What is not in this step

- **No eviction and no shedding.** The writer's `mpsc` is bounded to `WriteQueue`, and a tailer
  that cannot send within `WriteTick` stops and reopens as the README's §6 says, but which
  tailer stops first when the writer is behind is step 5's rule. Here they are equal.
- **No `read_at_ms` stamps.** Step 5.
- **No tailer for a finished instance**, as the README says; that is this step's rule, but the
  `at_end` chunk it leaves is step 3's.
- **No new frame kind.** A gap a follow meets is filled or lost, never announced: the wire stays
  lines, checkpoints and `Ended`.

## Design

### 1. The writer and the commit bus

`services/logs/logstore/writer.go`. One goroutine per open file owns every tail write. Tailers
reach it through a `gochan` `mpsc` with `WithKeepOpen`, one sender per tailer, closed when the
tailer ends, bounded to `WriteQueue` batches. A batch closes at `WriteBatch` lines or
`WriteBatchBytes`, whichever comes first, so the queue's worst case is bytes and not lines. The
writer drains what has arrived every `WriteTick` or when a batch closes and writes it in one
`InTx`, extending each stream's `open` chunk by step 3's chunk rules: a chunk closes at
`ChunkBytes` or `ChunkSpan` and the next opens at the line after, linked to it. A tailer that
stops, for any reason, closes its chunk at its last line in the write that ends it. Fills from
step 3 keep writing through `Store.Write`; SQLite's one writer serializes the two.

**The bus is the file's.** `Store.Subscribe(ids)` is a `gobus/conflate` receiver keyed by
stream id, and every commit of lines pings each stream it wrote, after the commit: the writer's
batches and `Store.Write`'s fills alike, so a follow learns of a page's fill as it learns of a
tailer's batch, and a receiver exists before anything is read. A receiver's keys are fixed when
it is made (`WithKeyFilter`), so a follow whose streams change makes the new receiver, drops the
old one, and walks every stream once after the swap, since a ping sent between the two reached
only the old one.

### 2. The registry and the tailers

`registry.go` and `tailer.go` in `services/logs`. The registry holds, per file, one tailer per
`(stream id, pattern)` with a refcount. `Attach(stream)` starts a tailer that is not running and
joins one that is, answering a handle: `Done()`, closed when the tailer ends, with the failure
that ended it, and `Release()`. A tailer whose last holder releases runs on for `TailGrace`, then
ends. **The registry reconciles nothing.** A tailer in its grace that ends, for any reason, is
dropped, and the next follow asking for the stream starts one afresh; what a follow knows of a
stream — ended at which generation, the refusal, the backoff — stays the follow's, as today. The
mirror watches stay the follow's too: opening them is a local read, and a reopen inside the grace
pays only that. A tailer is `Backend.Tail` over the registry's own claim, one per file, held
while any tailer runs. The registry asks for one tailer per stream; a backend may share a
connection underneath.

**Where a tailer starts** is the later of the stream's newest chunk's last line and the
watermark of the follow asking — its `after`, or its opening moment with none — so a tailer
never streams hours of log that sit below every watermark, and a fresh follow's first run finds
no gap between its watermark and its tailer's start. Its first read is a forward fill's (step 3): a read that returns its start
moment whole extends the chunk ending there, and one whose first line is past it links across the
hole, since the kubelet has rotated those lines away. A tailer started past the newest chunk
opens its chunk on a gap, and the lines between are live: a page or a follow that reaches them
fills them (§3). Those are the only live gaps a tailer leaves behind, with one more: a tailer
that cannot send within `WriteTick` closes its chunk and reopens from now, as the README's §6
says, and the lines in between are a live gap past every watermark on the stream.

**Its failures are the backend's four kinds** (step 2). A `Fatal` one ends every follow on the
file. `Ended`, `Denied` and `Transient` end the tailer, and each holder learns the kind through
`Done()`: a follow holds a `Transient` stream for its backoff and asks for it again, and an
`Ended` or `Denied` one until the view sees a later generation of it, the refusal remembered, as
it does today. A clean end on a container the view sees running is a drop, since a proxy can
close an idle stream.

**A finished instance.** The kubelet closes a log when its container ends, and the tailer ends
with it: its chunk closes at its last line without `at_end`, since a tailer cannot tell the log's
end from a dropped connection. The follow's reconcile sets `finished_at` as a page's resolve does
(step 3's `FinishStreams`) and asks for no tailer at that generation; the next page's forward
fill of the instance reads nothing more and marks `at_end`.

### 3. The follow

`Service.Follow` in `follow.go`:

1. Resolve the view and open its mirror watches, as today. Set each stream's watermark from
   `after` through `streamBound`, or to the follow's opening moment with none.
2. Subscribe to the bus under the view's stream ids, attach to each stream's tailer, then run
   once. The first run walks every stream from its watermark, which is how a reopen inside the
   grace finds the lines its tailer wrote while nobody was attached. A later run walks the
   streams its ping named.
3. **A run walks a stream from its watermark, by position, along the span the watermark sits
   in**, as a `Head` page's walk does (step 3), lost gaps crossed, keeping the lines the grep
   passes. It sends them in position order and moves the watermark to the last line it read,
   matched or not. A line below a watermark is never read, so a backfill a page writes below one
   never reaches a follow.
4. **A walk that stops at a live gap holds the watermark there, and the follow fills the gap**
   as a `Head` page would — step 3's filler, from the watermark to the gap's far edge, under the
   follow's own budget share — then walks on. The run carries what a page's `streamFill`
   carries: a fill that read the gap whole from the watermark lets the walk cross it, lines or
   none, **and a run that crosses a gap moves the watermark to the gap's far edge** — just
   before the next chunk's first line (`justBefore(From)`), or the tailer's start — not only to
   the last line it read. A watermark is an exclusive bound and a `Pos`, so it need not be a
   line, and a gap the kubelet holds nothing for is
   crossed once and never filled again, a shared tailer that started past a joining follow's
   `after` included. A fill cut short by its budget leaves the watermark at its
   last line and the run goes again at once, waiting on no ping. One that fails `Transient`
   holds the stream for its backoff, the watermark with it; one that fails `Denied` or `Ended`
   holds the stream until the view sees a later generation of it, as the tailer's failure does,
   since the gap's lines are the ones the cluster refused. A lost gap is crossed, as a page
   crosses it. So a follow sends past no line the file lacks, and only a lost gap is crossed
   silently.
5. After each run that sent a line, send a `Checkpoint` whose cursor is the oldest watermark
   across the view's streams.
6. Reconcile as today. A stream the view drops releases its tailer; one it adds is attached with
   its watermark at the follow's cursor, the last checkpoint's, or the follow's own start with
   none sent yet; the receiver is remade as §1 says.
7. Send `Ended` for an ending the user can read, as today: a `Fatal` failure, the claim
   revoked, every container of the view ended, every Pod source deleted, the view grown past
   its bounds, a kind the cache no longer mirrors. **The file closing is not one**: a
   `clusterCacheClear` or a shutdown ends the follow with an error and no frame, since
   following again would not end the same way, and the client reconnects.

A tailer's line reaches its follows on the ping its commit sends, so the sidecar's own batching
is the follow's only latency.

### 4. Step 3's live end

A page and a tailer must never extend one chunk. **Every gap past a running tailer's start is
the tailer's: crossed, never filled**, whether or not it has written a chunk yet. A tailer reads
continuously from its start, so nothing is missing past it — the gap between its start and its
first line, which opens with no link since a start is a moment and not a line, included — while
a tailer writes its first chunk row on its first commit, and a quiet stream may never commit,
so the file alone cannot say where the tailer is. The registry knows each running tailer's start
position, and a page and a follow both ask it (`registry.TailStart(stream)`) and hand the answer
to `CoverageOf` as the stream's tail bound: a walk runs through every gap past it to the newest
line the file holds, and a page's live end is that line with no forward read, as it is for an
`at_end` chunk. A gap below the tail bound — the one a backpressure reopen from now leaves —
stays live and is filled. `logstore.Chunk`
carries `Open`, and `chunkEndingAt` never answers an open chunk, so no fill extends one; a
forward fill reaches an open chunk only from below, through a live gap, and links it. **A stale
`open` is closed when the file is opened**: closing a chunk is the tailer's last write, and a
crash, a failed write or a file closed under the tailer leaves the flag set, so the manager's
open runs one statement that closes every open chunk at its last line, the one point at which no
tailer can be running. With the follow walking positions, nothing reads `lines` by `id`, and
the column loses `AUTOINCREMENT`. **The writer trims a finished stream** after each batch it
writes to one, as `Store.Write` does: the reconcile's `FinishStreams` can trim a stream before
the tailer's last batch commits, and that batch would otherwise put the next instance's lines
back under the old one.

### 5. The wire

`logsFollow(id, view, after)` sends `LogsFollowFrame` with `type`, `record`, `cursor` and
`reason`, per the log viewer README's *The wire*; `gqlgen.yml` binds `Checkpoint` onto
`logs.FollowCheckpoint`. `gqlgen generate`, then `pnpm codegen`; the webview builds, since
nothing in `src/` subscribes to `logsFollow`.

## Tasks

1. **The writer and the bus.** `services/logs/logstore/writer.go`, the `mpsc`, the tick,
   `Store.Subscribe`, a ping on every commit of lines.
2. **Step 3's live end.** The tail bound in `CoverageOf`, `Open` on `Chunk`, `chunkEndingAt`,
   the open chunks closed at the manager's open, the writer's trim, the schema's `id`.
3. **The registry and the tailers.** `registry.go`, `tailer.go`, the grace, the start rule, the
   finished instance.
4. **The follow.** `follow.go` as a walk from watermarks, the gap fill, the checkpoint, `Ended`.
5. **The wire.** The schema, `gqlgen.yml`, the resolver through `watchStream`, codegen.

## Tests

Go, beside the files they cover. Every wait is on a channel or the bus; `WriteTick` and
`TailGrace` are shrunk through `Retention`.

- `writer_test.go`: a batch commits on the tick; two tailers' lines land in one transaction; a
  ping per stream written and none for a stream not written, for a fill as for a batch; a
  tailer whose sender closes leaves the hub open for the others; a tailer's chunk closes at
  `ChunkBytes` and at `ChunkSpan` with the next linked to it; a tailer that stops closes its
  chunk.
- `coverage_test.go`: every gap past a tailer's start is the tailer's — with a chunk written
  past it, a page's walk reaches its last line and no fill is offered, and with none, the walk
  ends at the file's newest line below the start and no fill is offered; a gap below the start
  is still live; no fill extends an open chunk; a forward fill into an open chunk links it.
- `manager_test.go`: a file reopened with an open chunk left in it has the chunk closed at its
  last line, and a tailer then extends it.
- `writer_test.go`, with the cases below: a batch written to a finished stream after its
  `finished_at` is trimmed to it.
- `registry_test.go`: two follows share one tailer; a tailer survives `TailGrace` and a follow
  attaching inside it causes no new `Backend.Tail`, while one after it does; a tailer in its
  grace that ends is dropped and the next attach starts one; a stream the view drops releases
  on the next reconcile; a tailer starts at the later of its newest chunk's last line and the
  follow's watermark; a resume that gets its moment whole extends the chunk, and one
  whose first line is past it links across; a `Fatal` tailer ends every follow, a `Denied` one
  its stream alone, asked for again only once the generation moves, and a `Transient` one after
  the backoff; a finished instance ends its tailer with its chunk closed and no `at_end`, which
  the next page's forward fill sets.
- `follow_test.go`: the existing cases over the file; a follow's first frame follows the ping
  of its tailer's first commit; a reopen `after` a checkpoint inside the grace sends the lines
  the tailer wrote meanwhile, none twice and none missed, and one after the grace the same over
  a fresh tailer; a backfill a page writes below a watermark is not sent, and one it writes
  past a watermark inside a live gap is sent once, in position order, on its own commit's ping;
  a follow at a live gap fills it and sends its lines before the tailer's past it, and no
  checkpoint names a line past an unfilled gap; a gap read whole with no line is crossed and
  not filled again on the next ping; a fill cut by its budget goes on with no ping; a fill
  refused `Denied` holds the stream until its generation moves, the lines past the gap unsent;
  a follow attaching to a quiet stream before its tailer's first commit makes no kubelet read;
  a lost gap is crossed; a follow wakes only on pings for its streams; a stream the reconcile
  adds is walked after the receiver swap, so a ping between the two loses nothing; a restart
  opens a new stream and the follow continues over both; `Ended` on each of its causes with the
  reason, and the file closing ends with an error and no frame.
- `graph`: `logsFollow` frames bind by `type`, with `record` null on a checkpoint and `cursor`
  null on a line.

## Residuals

- **A quiet stream pins the checkpoint.** The cursor is the oldest watermark, so one stream that
  writes nothing holds it at the follow's start, and a reopen after it sends again everything the
  noisy streams wrote since. Correct, since the webview drops repeats by cursor, and bounded by
  what the file holds, but it can be large. Advancing a quiet stream's watermark to now would be
  unsafe: a line can arrive stamped before the moment it arrives at.
- **A gap holds a stream's newest lines behind its fill.** A follow that meets a live gap sends
  nothing of that stream past it until the gap is filled or lost; the other streams go on.
- **A page and a tailer can write overlapping chunks**: a page that read `TailStart` as no
  tailer a moment before one started fills forward over the range the tailer then writes, and a
  page and a tailer resuming at one closed chunk's end each write a chunk from it, as two pages
  over one gap can today. Coverage joins them; eviction counts the bytes twice until one goes
  (step 5).
- **An `open` flag left by a closing write that failed while the file stays open** holds until
  the file is next opened. Pages read `TailStart`, so none is blocked; the next tailer cannot
  merge into that chunk and opens one on a gap beside it, which coverage joins.

## Security

**Widens:** nothing beyond step 3. The follow reads the file, so it shows nothing a page could
not. Tailers run over the kubelet backend's claims, so a disabled or deleted cluster ends them
through `Fatal`.

**Residual:** a tailer in its grace keeps reading a log nobody is watching for up to
`TailGrace`, and writes it to the file. The lines are the same lines a page would fetch.

## When it lands

- `sidecar/CLAUDE.md`: the `logsFollow` paragraph says the registry, the tailers, the writer,
  the commit bus and the follow as a walk from watermarks that fills a live gap; the *Patterns*
  bullet on `fanIn` points at the writer's `mpsc` too, and its `logstore` paragraph no longer
  justifies `AUTOINCREMENT` by a follow reading commit order.
- `logstore/migrations/0001_init.sql`: the `lines` comment says the same.
- This README's row reads Built, with the pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/... ./internal/graph/...`; the wire
checks: `cd sidecar && go run github.com/99designs/gqlgen generate`, then `pnpm codegen`,
`pnpm build`, `make test-js`, `make lint-js`; `make lint-go`, `make vet-go`; `make test-changed`
while working.
