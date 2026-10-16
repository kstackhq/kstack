---
title: Tailers and the follow
scope: sidecar
status: Planned
---

# Tailers and the follow

**Needs:** step 3, whose chunks a tailer extends and whose fills a follow resumes from.
**Unblocks:** step 5, whose backpressure sheds the tailers this step runs.

Go paths are under `sidecar/internal/`. The design is the README's §5 and the follow half of
§9; this file says what to build and how to know it is built.

## In short

One tailer per stream and pattern, shared by every follow that wants it and kept `TailGrace`
after the last lets go, writes lines to the file through one writer. `Service.Follow` becomes a
read of the file after a watermark per stream, run again on every commit, so a line reaches a
follow from the file and never from the tailer. Checkpoints are true by construction.
`logsFollow` sends `LogsFollowFrame`s with a `type`, and the webview's generated types follow.

## What is not in this step

- **No eviction and no shedding.** The writer's `mpsc` is bounded to `WriteQueue`, and a tailer
  that cannot send within `WriteTick` stops and reopens as the README's §6 says, but which
  tailer stops first when the writer is behind is step 5's rule. Here they are equal.
- **No `read_at_ms` stamps.** Step 5.
- **No tailer for a finished instance**, as the README says; that is this step's rule, but the
  `end` chunk it leaves is step 3's.

## Design

### 1. The writer and the commit bus

`logstore/writer.go`. One goroutine per open file owns every tail write. Tailers reach it
through a `gochan` `mpsc` with `WithKeepOpen`, one sender per tailer, closed when the tailer
ends, bounded to `WriteQueue` batches. A batch closes at `WriteBatch` lines or
`WriteBatchBytes`, whichever comes first, so the queue's worst case is bytes and not lines. The
writer drains what has arrived every `WriteTick` or when a batch closes, writes it in one `InTx` extending each stream's `open` chunk, and after
the commit sends on a `gobus/conflate` bus keyed by stream id, one coalesced ping per stream
written. Fills from step 3 keep writing directly on the writer connection; SQLite's one writer
serializes the two.

### 2. The registry and the tailers

`registry.go` in `services/logs`. The registry holds, per file, the tailers keyed by
`(stream id, pattern)` with a refcount, and the view watches keyed by the view's namespaces and
kinds, each kept `TailGrace` after its last holder lets go. A follow's reconcile asks it for
the streams the view wants at each generation; the registry starts a tailer that is not
running, attaches to one that is, and detaches on the next reconcile for a stream the view no
longer wants. A tailer is `Source.Tail` from the moment of the stream's newest chunk's last
line, or the follow's `after`, or now. It hands its lines to the writer. A `Fatal` failure ends
every follow on the file, a `Denied` one ends the stream, a `Transient` one is retried under the
follow's backoff. A reconcile that sees an instance finish sets `finished_at`, ends its tailer,
and the tailer's last read, reaching the end, marks the chunk `end`. The registry asks for one
tailer per stream; a source may share a connection underneath.

### 3. The follow

`Service.Follow` in `follow.go`:

1. Resolve the view and take the watches and tailers from the registry.
2. Subscribe to the commit bus under the view's stream ids before the first read, and again as
   the reconcile changes them.
3. Set each stream's watermark from `after`, or from the follow's opening moment with none.
4. On each ping, read the rows past the last `id` seen in one walk, keep those past their
   stream's watermark, send each stream's kept lines in position order, advance the watermarks,
   then send a `Checkpoint` whose cursor is the oldest watermark.
5. Send `Ended` on a `Fatal` failure, when the cluster is disabled or deleted, when the file
   closes, or when the reconcile ends the follow for a reason it has today.

The first frame waits for the tailers' first commit, at most one `WriteTick`. A stream that
joins the view later starts from the follow's cursor.

### 4. The wire

`logsFollow(id, view, after)` sends `LogsFollowFrame` with `type`, `record`, `cursor` and
`reason`, per the log viewer README's *The wire*; `gqlgen.yml` binds `Checkpoint` onto
`logs.FollowCheckpoint`. `gqlgen generate`, then `pnpm codegen`; the webview builds, since
nothing in `src/` subscribes to `logsFollow`.

## Tasks

1. **The writer and the bus.** `logstore/writer.go`, the `mpsc`, the `conflate` bus, the tick.
2. **The registry and the tailers.** `registry.go`, `tailer.go`, the grace, the watches, the
   finished instance.
3. **The follow.** `follow.go` as a read after watermarks, the checkpoint, `Ended`.
4. **The wire.** The schema, `gqlgen.yml`, the resolver through `watchStream`, codegen.

## Tests

Go, beside the files they cover. Every wait is on a channel or the bus; `WriteTick` and
`TailGrace` are shrunk through `Retention`.

- `writer_test.go`: a batch commits within a tick; two tailers' lines land in one transaction;
  a ping per stream written and none for a stream not written; a tailer whose sender closes
  leaves the hub open for the others.
- `registry_test.go`: two follows share one tailer; a tailer and its watches survive
  `TailGrace` and a follow attaching inside it causes no new `Source.Tail`, while one after it
  does; a stream the view drops detaches on the next reconcile; a finished instance ends its
  tailer and leaves an `end` chunk; a `Fatal` tailer ends every follow and a `Denied` one ends
  its stream alone.
- `follow_test.go`: the existing cases over the file; a follow gets its first frame within one
  tick of a tailer's first line; a reopen `after` a checkpoint sends no line twice and misses
  none; a backfill written by a page while a follow runs is not sent; a follow wakes only on
  pings for its streams; a restart opens a new stream and the follow continues over both;
  `Ended` on each of its causes with the reason.
- `graph`: `logsFollow` frames bind by `type`, with `record` null on a checkpoint and `cursor`
  null on a line.

## Security

**Widens:** nothing beyond step 3. The follow reads the file, so it shows nothing a page could
not. Tailers run over the kubelet source's claims, so a disabled or deleted cluster ends them
through `Fatal`.

**Residual:** a tailer in its grace keeps reading a log nobody is watching for up to
`TailGrace`, and writes it to the file. The lines are the same lines a page would fetch.

## When it lands

- `sidecar/CLAUDE.md`: the `logsFollow` paragraph says the registry, the tailers, the writer,
  the commit bus and the follow as a read; the *Patterns* bullet on `fanIn` points at the
  writer's `mpsc` too.
- This README's row reads Built, with the pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/... ./internal/graph/...`; the wire
checks: `cd sidecar && go run github.com/99designs/gqlgen generate`, then `pnpm codegen`,
`pnpm build`, `make test-js`, `make lint-js`; `make lint-go`, `make vet-go`; `make test-changed`
while working.
