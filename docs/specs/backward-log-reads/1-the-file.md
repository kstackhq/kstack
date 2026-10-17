---
title: The file
scope: sidecar
status: Done
---

# The file

**Needs:** nothing in this sequence. **Unblocks:** step 2, which streams inside a window and leaves
islands.

Go paths are under `sidecar/internal/services/logs/`. The shared design is the README; this file
says what to build and how to know it is built.

## In short

A page writes each window to the log file as soon as the window is complete, instead of holding every
window of every stream until one write. Each write stamps what it wrote, as the page's one write does
today. The walk's budget grows. The file's budget grows to 1 GiB.

The readers do not change. A window is still collected and handed over newest first. Only when its
lines reach the file, and how long they stay, changes.

## What is not in this step

- **No change to the reader loops** (`backward`, `backwardUntil`). Step 2 rewrites them.
- **No streaming inside a window, and no islands.** A window is written once it is complete, and a
  cancel keeps the part nearest the point (§1). Step 2 writes inside a window and stops windows part
  way.
- **No change to the download limits.** That is step 4. `maxBudget` and its split stay for now.
- **No grep in the write.** Every kubelet line is written unfiltered, with pattern `""`, as today.

## Design

### 1. A window is written when it completes

Today a page buffers every line of every stream, then checks instances, then writes once. Now a fill
writes each window as soon as the reader has handed all of it over.

- **The reader marks where each window ends.** `streamRead` today is a flat channel of lines. It
  gains a window boundary, sent after a window's last line. A forward read is one window, which its
  close ends.
- **The filler writes at each boundary.** It turns the window's lines oldest first and hands them to
  the file in batches of `WriteBatch` lines or `WriteBatchBytes` (1 MiB), oldest batch first. It
  writes a window once the next window's first line arrives, or once the read ends: only then does
  it know whether the window is the read's oldest, which takes `Prev` and `Head`.
- **How a window's batches link.** Each `Fill` below is one batch.
  - **Down, first batch:** `Prev` is the line below the window, only on the oldest window of the
    read and only when it reached the lower bound, as `fillOf` decides today. Every other window's
    first batch has no `Prev`. `Head` goes on the oldest window's first batch too, where `fillOf`
    puts it today.
  - **Between batches:** batch k has `Prev` = batch k−1's last line and `Merge` set, so it extends
    the chunk batch k−1 wrote.
  - **Up, last batch:** `Next` is the oldest line this read has written so far, or the chunk at the
    point when it has written none. An empty window writes nothing, so the next older window links
    over it to the last line written. Only the last batch carries `Next`, so no link says "nothing
    lies between" until every line between was read.
- **`AtEnd` goes on the first window's last batch**, the newest lines, since `cutChunks` marks the
  last chunk of a `Fill`. Today `fillOf` sets it once the whole read is done: no upper bound, a
  finished instance, no error, some lines. The first window is now written before the read is done,
  so its last batch carries `AtEnd` when there is no upper bound, the instance is finished, and the
  window handed over its newest line without error. A cancel by the filler keeps it, as today: lines
  arrive newest first, so a cancelled first window still holds the log's last line. Without this, a
  finished stream's newest chunk never gets `AtEnd`, and every `Head` page to its end reads it again.
- **A read cancelled part way** hands over the newest part of a window, the part nearest the point,
  as today. The filler writes that part linked up (`Next`) and not down. Lines inside a window still
  arrive newest first in this step, so a cancel cannot leave a hole at the point, and no window
  becomes an island. A window whose GET fails hands over nothing, as today.
- **Memory is about two windows per stream**, not the whole page: the reader's collected window and
  the filler's copy while it turns it. In this step a window is bounded by the stream's share of the
  budget, as today.
- **`afterFill` still runs once per page**, after the page's last window is written and before its
  walk. Its tests count on that.

### 2. Instances are checked per window

`checkInstances` runs before each window's write, for that stream alone. It reads the mirror once for
the stream (`reading.serves`), not for every stream of the page. A window is as sure as a whole read
is today: the check catches a restart the mirror has seen by the time the window is written, and no
more. A stream that fails the check stops there and is skipped, as today. Windows already written stay.
It costs one mirror read per window, a local SQLite query.

### 3. A page stamps what it writes, as today

Each window's write stamps its chunks read (`Write` with `stamp`), as the page's one write does today.
So a sweep between a page's write and its walk evicts nothing the page wrote. It also keeps a window's
over-read, the lines past `need` that the next scroll up wants, for `ReadTTL`.

Effects to expect:

- **Each window's write pings the stream's bus**, so a follow on the stream wakes per window rather
  than once per page. Pings coalesce, so this costs a walk, never a lost line.
- **Each window's write counts toward `SweepBytes`**, so a sweep can run while a page is filling. The
  stamp covers it.
- **More commits.** A page commits once per window per stream instead of once, all through SQLite's
  one writer, alongside the tailers' writer, with a mirror read per window (§2). A commit of one
  window is milliseconds, and a page has at most a few windows per stream, so this is acceptable. If
  it shows, page writes can go through the file's writer later.

### 4. The seam

An `Around` page's `Head` half reads on from the `Tail` half's newest line (`seams`). Today it reads
that line off the buffered fills. Now each stream's fill keeps the newest line it wrote, and the seam
comes from that. As today, a seam comes only from the first backend's fill, and only when `fromKey`
is set. (Step 3 fills the halves the other way round and drops the seam.)

### 5. The walk's budget

The walk keeps its even split across streams: the page is cut at the nearest stop across streams, so
a share one stream walks past another's stop is wasted. Walks run one stream after another in one read
transaction, so a pool would drain in stream order and cut every page near the key. The total goes from
32 MiB to **64 MiB** (`maxWalkBudget`), about 640 KiB per stream at a hundred.

### 6. The budget

`Retention.Bytes` (`logstore/retention.go`, `defaultBytes`) goes from 256 MiB to **1 GiB**. The knobs
derived from it follow:

| Knob | Today | After |
| --- | --- | --- |
| `Bytes` | 256 MiB | 1 GiB |
| `StreamShare` (a quarter of `Bytes`) | 64 MiB | 256 MiB |
| `SweepBytes` (a sixteenth of `Bytes`) | 16 MiB | 64 MiB |

The budget is per cluster cache, with SQLite's index and WAL on top. A user with several clusters can
use several GiB of disk, and there is no setting to lower it. A file shrinks slowly after a large
search: a sweep hands back at most 8 MiB (`VacuumPages`, 2,048 pages of 4 KiB). Chunks a walk read in
the last `ReadTTL` are not evicted, so a large search can hold the file over its budget for up to ten
minutes.

## Decisions this step asks for

1. **Write per window, not per batch inside a window.** It needs no change to the reader loops, and
   linking only completed windows keeps every link true. Streaming inside a window is step 2's.
2. **A page stamps what it writes, as today.** It protects the page's lines until its walk, and keeps
   the over-read for the next scroll. A large search's lines are protected for `ReadTTL` too (§6).
3. **Keep the walk's even split, at 64 MiB.** A pool drains in stream order and cuts every page near
   the key.
4. **1 GiB per cluster, with no setting.** A search across a view's logs stays cached. A setting can
   come later if disk use is a problem; a `docs/TODO.md` line tracks it.

## Tasks

1. **Window boundaries.** `streamRead` carries the end of each window; both backward readers send it,
   and a forward read's close ends its one window. — Done.
2. **Writing per window.** The filler writes each window in batches with the linking rule, checks the
   stream's instance before each write, and keeps its newest line for the seam. `pull`'s page-long
   buffer goes. — Done.
3. **Links and marks.** `Prev` and `Head` on the oldest window's first batch, batch-to-batch `Merge`,
   `Next` to the oldest line written so far, `AtEnd` on the first window's last batch. — Done.
4. **The walk budget and the file budget.** `maxWalkBudget` at 64 MiB; `defaultBytes` at 1 GiB.
   — Done.

## Tests

- **Memory is per window.** A page over many windows holds at most about two windows per stream.
- **Windows join.** A read of several windows, each written in several batches, forms one span to
  the chunk at the point, and `TestPagesJoinWithoutLossOrRepeat` passes with batch sizes seeded small.
- **An empty window.** A quiet window between two windows with lines writes nothing, and the older
  window links up to the newer one's oldest line.
- **A cancel part way.** A read cancelled inside a window writes the newest part, linked up to the
  chunk at the point and not down. No line is lost at the cursor.
- **`AtEnd`.** A `Tail` page from the end of a finished instance marks its newest chunk `AtEnd`, and a
  later `Head` page to the end sends no GET. It does so too when the filler cancels the first window
  part way.
- **`Head`.** A read that reaches the log's first line marks the oldest chunk it wrote `Head`.
- **A restart between windows.** The pod's instance changes after the first window is written. The
  read stops, the first window stays, and none of the new instance's lines go to the old stream.
- **The stamp.** A sweep between a page's write and its walk evicts nothing the page wrote.
- **The seam.** An `Around` page's halves still join with no lost or repeated line.
- **`afterFill` runs once per page.**
- **`TestTheFileKeepsItsInvariant` passes**, its model taking per-window fills.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: the page line says each window is written when it
  completes; the walk budget's total.
- `docs/specs/log-cache/README.md`: its `Retention` numbers.
- `docs/TODO.md`: a setting for the log file's budget.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the Go checks. No wire change.
