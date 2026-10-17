---
title: The windows
scope: sidecar
status: Planned
---

# The windows

**Needs:** step 2, whose windows stream to the file and leave an island when cut. **Unblocks:**
step 4, whose limits rely on a window being bounded.

Go paths are under `sidecar/internal/services/logs/`. The shared design is the README; this file
says what to build and how to know it is built.

## In short

A backward read sizes each window from how busy the log is next to the point, in kept lines and in
bytes. It checks the window as it reads. A window far busier than planned is stopped, left as an
island, and replaced by a shorter one. A window that is too quiet is followed by an older one. The
first `Tail` page's `tailLines` windows double instead of growing by one page. An `Around` page fills
its forward half first, so the backward half can use its rates.

## What is not in this step

- **No change to how a window's lines reach the file.** That is step 2.
- **No new limits.** `maxBudget` and its split stay until step 4, so the refusal can still happen
  here. This step makes it rare: a window is now about one page.
- **No change to forward reads.**

## Design

### 1. Which read runs

- **A `Tail` page with no cursor, at the log's end:** `tailLines` windows (`reader.backward`). The
  first asks for `need` lines. Each window that ends with fewer than `need` kept lines makes the next
  twice as large. Today it grows by one page at a time, which takes a sparse grep hundreds of GETs.
- **Every other backward read:** time windows (`reader.backwardUntil`, sized as §2 and checked as
  §3). That covers scrolling up from a cursor, the gap under a chunk, and the half of an `Around` page
  before its moment.

### 2. The first guess

A window should hold about `need` kept lines and no more than its byte target:

> window = the smaller of `need` ÷ kept rate and byte target ÷ byte rate, × 1.5,
> rounded up to whole seconds, at least one second.

The byte target is 4 MiB. A stream's **first** window on a page has a smaller one when the page
reads many streams:

> first byte target = the smaller of 4 MiB and 64 MiB ÷ the number of streams the page reads

64 MiB is the page's download limit (step 4). Every stream reads its first window whole, so this
keeps the first windows together within the page's limit: 4 MiB each up to 16 streams, about
640 KiB each at 100. Later windows on the page use 4 MiB and draw on what is left of the limit. Views
are capped at 100 streams, so a first window is never below 640 KiB and needs no floor of its own.

The rates come from lines next to the point:

- **Scrolling up or filling a gap:** the chunk that starts at the point. Read its first `need` lines,
  oldest first. Their count and bytes over the time they span give the byte rate. The ones that match
  the grep give the kept rate. One read of the file.
- **An `Around` page:** the half after the moment fills first (§4), and its lines give both rates.
- **Nothing nearby:** a ten-second window.

A kept rate of zero sizes the window from bytes alone. That is the sparse grep's case: windows of
about the byte target, each read whole.

The window never starts before the read's lower bound: the last line of the chunk below the gap, or
the log's first line. The log's first line comes from `firstTimestamp`, one GET of 100 bytes, as
today.

### 3. Checking a window

A window is one GET with `sinceTime`, read up to the point (`until`, applied in `reader.lines` as
today). Its lines stream to the file (step 2). It is checked as it goes.

**Too busy: stop, then narrow.** Once a window has read `need` kept lines or a quarter of its byte
target, estimate the whole window from the part read:

> expected = what was read × (window length ÷ time covered so far)

If it expects more than four times `need` kept lines, or more than four times its byte target
(16 MiB for a 4 MiB target), stop the GET. What it already wrote is an island (step 2). Measure the
rates from what was read, and read a new window ending at the point, sized from them.

Example: a page wants 501 lines with no grep. The guess is 10 lines/s, so the window is 75 s. Just
before the point, the pod was logging 5,000 lines/s. After 501 lines, about 0.1 s of the window is
covered, so the whole window would hold about 375,000 lines. Stop. The new rate is 5,000/s, so the
new window is the last second before the point. About 5,500 lines are downloaded in total, and the
first 501 stay on disk for the next scroll.

A one-second window is never narrowed. Nothing smaller can be asked for. Step 4 decides what happens
when it is too loud.

**Too quiet: widen.** A window that ends with fewer than `need` kept lines is followed by an older
window. It ends where this one began and is sized from the rates just measured, for the kept lines
still missing. A window with no kept lines makes the next one eight times longer, but no more than
its byte target by the byte rate.

**The island and the gap.** A stopped window's island sits below the narrowed window, with a gap
between them. A later scroll fills that gap with the same backward read, the island as its lower
bound.

**When the read ends.** When the windows read so far hold `need` kept lines, judged at a window's
boundary (step 2), when a window reaches the lower bound or the log's first line, or when the budget
runs out. A window that starts at the lower bound reads that bound's second forward, as today
(`wholeMoment`).

`maxWindows`, `readOpts.windows` and the time-span sizing leave the backward read.

### 4. Anchors

An `Around` page at moment `T` reads both ways from `T`.

1. **Check the log's first line.** If `T` is before it, nothing before `T` can be read. The half
   before `T` is empty. (The lost-lines work marks the hole.)
2. **The half after `T` fills first.** It is one forward read from `T`, as today. Its lines give the
   rates for the half before.
3. **The half before `T`** is an ordinary backward read with `T` as the point. Its first window links
   up to the forward half's first line. There is no seam: step 1's seam came from the backward half,
   which now fills second.

## Decisions this step asks for

1. **Stop a window at four times `need`, or four times its byte target.** Smaller wastes GETs on
   small misses. Larger downloads more before the stop.
2. **4 MiB windows for a sparse grep.** They read a default 10 MiB log in three GETs, and each GET
   costs the node a scan of the file up to its start.
3. **First windows share the page's limit.** Every stream must read one window for the page to make
   progress. Sizing the first windows from the limit keeps a page of a hundred streams as quick as a
   page of one. It only sizes a window; it never refuses one.
4. **Rates from the near side.** The chunk at the point is already in the file, so the guess costs no
   GET.

## Tasks

1. **The rates.** From the chunk at the point, in kept lines and bytes; from the `Around` forward
   half; the ten-second default. — Planned.
2. **The time windows.** `backwardUntil` sized by §2; stop and narrow, widen. — Planned.
3. **Doubling `tailLines`.** The first `Tail` page's windows double after a short window. — Planned.
4. **`Around`.** The forward half fills first; the first-line check; no seam. — Planned.

## Tests

- **The burst.** A view of a hundred streams. One is quiet for days, then logs 5,000 lines/s just
  before the cursor. Paging back returns lines on every page, and the pages join without a lost or
  repeated line.
- **An anchor in a burst.** An `Around` page at the start of the same burst returns `need` lines on
  each side.
- **Quiet stretches.** A log with a day of silence before the point reaches the older lines in a few
  GETs.
- **A bad guess costs little.** With a rate ten times too low, a read downloads at most about four
  pages plus one second of lines, and what it downloaded stays on disk as an island.
- **A sparse grep.** Three matches in a 10 MiB log. The read downloads the log once, in about three
  GETs, and the page answers the three matches.
- **First windows share the limit.** A sparse grep over a hundred 10 MiB logs: each stream's first
  window is about 640 KiB, and the first page downloads about 64 MiB, not 400 MiB. A single-stream
  view's first window is 4 MiB.
- **Searching again is free.** A second grep over a range the first downloaded sends no GET.
- **Doubling `tailLines`.** A sparse grep from the log's end reaches back 10 MiB in about eight GETs,
  not hundreds.
- **`TestPagesJoinWithoutLossOrRepeat` and `TestTheFileKeepsItsInvariant` pass**, with bursts, quiet
  stretches and sparse greps seeded.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: the backend line describes how a window is sized and
  checked.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the Go checks. No wire change.
