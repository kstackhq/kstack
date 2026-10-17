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

The janitor runs. Each sweep measures the file, protects open and recently read chunks, evicts
the least recently read until the file and every stream are under budget, removes streams with
nothing left, and vacuums. Pages and tailers stamp `read_at_ms`. When the writer falls behind, a
tailer with no follow attached stops before one that has. `DefaultRetention` is wired in `app`,
and the whole suite, both coverage gates and the lints run at the end.

## What is not in this step

- **No compression**, as the README's decision says.
- **No preference for evicting chunks whose backend still holds them.** Every chunk is evicted by
  `read_at_ms` alone. The README names this as later.

## Design

### 1. `read_at_ms`

A page stamps `read_at_ms` on every chunk it read from, in the same transaction as any fill it
made, and a tailer's writer stamps every chunk it extends. Nothing else touches it. The stamp
is unix milliseconds from the store's clock, a parameter so a test can move it.

### 2. The sweep

`logstore/janitor.go`, one per open file, started by `OpenOrCreate` when `Interval` is not zero
and stopped by the store's close, as `kubestore`'s is. It sweeps every `Interval` and after
every commit through a capacity-one notify, on its own context, so a close aborts it
mid-statement. One sweep, in order:

1. Sum `bytes` over `chunks`, for the file and per stream.
2. Protect every `open` chunk and every chunk whose `read_at_ms` is within `ReadTTL`.
3. While the file is over `Bytes` or a stream over `StreamShare`: pick the unprotected chunk
   with the oldest `read_at_ms`, an over-share stream's first; delete its lines by the position
   index in bounded batches, then the chunk row, in one `InTx` per chunk.
4. Delete every stream with no chunks, and every stream whose pod the mirror no longer holds
   once its last chunk is gone.
5. `PRAGMA incremental_vacuum` bounded by `VacuumPages`, gated on `freelist_count`, then a
   `TRUNCATE` checkpoint when the WAL is larger than the file.

A sweep that is over budget with nothing unprotected left stops and says so in its stats; the
next sweep tries again.

### 3. Backpressure

In the writer and the registry: a tailer that cannot send its batch within `WriteWait` stops its
read, closes its chunk at the last committed line, and reopens from now, leaving a gap. When
more than one tailer is waiting on a full queue, the registry stops the tailers with no follow
attached first, then the attached ones, oldest first. No cap on what a tailer writes during its
grace.

### 4. Wiring

`NewManager` takes a `Retention`, and `app` passes `logstore.DefaultRetention`. Tests about anything else keep a zero
`Interval`.

## Tasks

1. **`read_at_ms`.** The stamps in `fetch.go` and `writer.go`, the clock parameter.
2. **The sweep.** `janitor.go`, the stats, the notify.
3. **Backpressure.** The shed order in `registry.go` and the writer.
4. **The gate.** `DefaultRetention` in `app`; the full checks under *Verification*; raise a
   coverage threshold if CI reports the number rose.

## Tests

Go, beside the files they cover, under a shrunk `Retention` and a test clock. Every wait is on
the janitor's notify or a channel.

- `janitor_test.go`: a protected chunk survives a sweep over budget, written as a `select`
  against a window sized from the shrunk `Interval` and saying so; an unprotected chunk with the
  oldest `read_at_ms` goes first; an over-share stream's chunks go before an older chunk of a
  stream under its share; the file settles under `Bytes` within two sweeps; a stream with no
  chunks is removed, and a stream of a gone pod goes with its last chunk while a live pod's
  stays; a sweep with nothing to evict reports it; the vacuum runs only with a freelist; a close
  mid-sweep returns.
- `fetch_test.go`: a page stamps the chunks it read; a page over an evicted chunk fills it again
  when the fake still has the lines, and answers `more: false` at a `head` chunk when it does
  not.
- `writer_test.go` and `registry_test.go`: a backpressured tailer leaves a gap and nothing else,
  and its chunk is closed at the last committed line; with the queue full, a tailer in its
  grace stops before an attached one.
- `walk_test.go`: the invariant's walk runs with the janitor live, evicting on its own instead
  of by hand.

## Security

**Widens:** nothing. Eviction never leaves a chunk half deleted, since the lines and the chunk
go in one transaction, so a page never reads a range the file claims and does not hold.

**Residual:** none in this step.

## When it lands

- `sidecar/CLAUDE.md`: a janitor paragraph for the log file beside `kubestore`'s, naming the
  knobs and the shed order.
- This README's row reads Built, with the pull request, and the sequence's integration gate is
  recorded as passed.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/...` while working; at the end, the
integration gate: `make test`, `make lint`, `make vet`, `make cover-go`, `make cover-js`, with
no gate lowered.
