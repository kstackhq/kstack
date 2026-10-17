---
title: The windows
scope: sidecar
status: Done
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

- **No change to how a window's lines reach the file.** That is step 2, except for the one new item
  a stopped window sends (§3).
- **No new limits.** `maxBudget` and its split stay until step 4, so the refusal can still happen
  here. This step makes it rare: a window is now about one page, a first window is sized under half
  the stream's share, and a window due to run past the share is stopped while budget remains (§3).
- **No change to forward reads.**

## Design

### 1. Which read runs

- **A `Tail` page with no cursor, at the log's end:** `tailLines` windows (`reader.backward`). The
  first asks for `need` lines. Each window after which `more` says go on asks for twice as many.
  Today it grows by one page at a time, which takes a sparse grep hundreds of GETs. Doubling needs no
  count of kept lines in the reader: `more` already says whether the stream has enough.
- **Every other backward read:** time windows (`reader.backwardUntil`, sized as §2 and checked as
  §3). That covers scrolling up from a cursor, the gap under a chunk, and the half of an `Around` page
  before its moment.

### 2. The first guess

A window should hold about `need` kept lines, with a margin, and no more than its byte target:

> window = the smaller of `need` × 1.5 ÷ kept rate and byte target ÷ byte rate,
> rounded up to whole seconds, at least one second.

The margin is on the kept lines alone, so a window sized by bytes holds about its byte target.

The byte target is 4 MiB. A stream's **first** window on a page is also kept well under the
stream's budget, since every stream reads its first window whole and a window is rounded up to a
whole second:

> first byte target = the smaller of 4 MiB and half the stream's share of `maxBudget`

At 100 streams that share is about 335 KiB, so a first window is about 165 KiB. The `firstTimestamp`
GET spends from the same share first. A window due to run past what is left is stopped and narrowed
(§3), so the guess, not the budget, decides where the first window ends. Later windows use the
smaller of 4 MiB and half what the stream has left, for the same reason as the first, so no window
starts that the budget is due to cut.
Step 4 removes the share and sizes the first byte target from the page's download limit instead,
64 MiB ÷ the streams the page reads.

**Bytes are counted as the body carries them:** a line's timestamp, a space, its message and a
newline. That is what `counted` spends from the budget. The file holds the message alone, and for
short lines the difference is half again or more, so a rate measured from the file's bytes alone
would undershoot every byte target. The file also holds the message cleaned and cut to 16 KiB, so a
rate from the file is an estimate; a window counts each line as its body carried it, before
cleaning, so its checks see what the budget spends. The same unit serves step 4's limits.

The rates come from lines next to the point:

- **Scrolling up or filling a gap:** the chunk just above the point. The point is often inside the
  gap, so this is the chunk the gap ends at, not one that starts at the point. Read its first `need`
  lines, oldest first, and at least ten, then on until they span a second or reach 5,000: a dense
  log's `need` lines can span a tenth of a second, which the one-second clamp below would read as a
  tenth of its rate. Their count and bytes over the time they span give the byte rate. The ones
  that match the grep give the kept rate. One read of the file.
- **An `Around` page:** the chunk just above `T`, which the half after `T` filled first (§4), or the
  cache already held.
- **Nothing nearby, or fewer than ten lines:** a ten-second window.

A sample's span is at least one second. One line, or lines all in one moment, span no time; the
clamp keeps the rate finite.

A kept rate of zero sizes the window from bytes alone. That is the sparse grep's case: windows of
about the byte target, each read whole.

The window never starts before the read's lower bound: the last line of the chunk below the gap, or
the log's first line. The log's first line comes from `firstTimestamp`, one GET of 100 bytes, as
today.

**The reader counts kept lines.** `readOpts` gains the grep, which the `Backend` comment already
promises, and the reader counts the kept lines of each window for the checks in §3. The filler keeps
its own count, which answers `more`.

### 3. Checking a window

A window is one GET with `sinceTime`, read up to the point (`until`, applied in `reader.lines` as
today). Its lines stream to the file (step 2). It is checked as it goes.

**Too busy: stop, then narrow.** Lines arrive oldest first, so a burst just before the point arrives
last, and the window's average rate says little about what is still to come. The reader keeps a
**recent rate**: the kept lines and bytes of the lines in the one second ending at the newest line
read, divided by how much of that second the window has read: the whole second, or less while the
window began under a second ago, with no minimum. Dividing by the lines' own span instead would
read one line of a quiet log as a rate past measure. Each line checks:

> expected = read so far + recent rate × time left in the window

Three conditions stop the GET. Each waits for the window to have read something first, since a
window's first few lines often share a millisecond and project a rate far above the true one:

- once the window has read `need` kept lines, expecting more than four times `need`;
- once it has read a quarter of its byte target, expecting more than four times the target (16 MiB
  for a 4 MiB target);
- once it has read a quarter of what the stream had left of its budget when the window began,
  expecting more bytes than the stream has left. A window the budget cut would be an island with
  no edge, and the page would be refused; stopping first leaves budget for the narrower window.
  Step 4 removes this condition with the share.

Lines that all share one moment have a rate too high to project: once any of the three waits has
passed, the window stops. A window with no time left is never stopped, since it has nothing left to
read.

What it already wrote is an island (step 2). Size a new window ending where the stopped one ended,
from the recent rate, by §2, under the bytes the stream has left. A widened window ends where the
window before it began, so a stop there never reads a completed window again.

Example: a page wants 501 lines with no grep. The guess is 10 lines/s, so the window is 75 s. For the
last 10 s before the point the pod logged 5,000 lines/s. The quiet 65 s arrive first, about 650
lines; the check runs from the 501st and expects about 750. Then the burst. The newest second still
holds about ten quiet lines, so the recent rate climbs from 10/s as burst lines join it. After about
115 of them it is about 125/s with 9.9 s left, and the window expects more than 2,004 lines. Stop,
with 766 lines on disk as an island. The new window is 7 s, which starts inside the burst. Its
newest second is the part it has read, so after 501 lines its recent rate is 5,000/s with almost 7 s
left. Stop again, a second island. The third window is the last second before the point, 5,000
lines, and completes. Three window GETs and about 6,300 lines in total, plus the first-line GET
(`TestABusyWindowIsStoppedAndNarrowed`).

A one-second window is never narrowed. Nothing smaller can be asked for. Step 4 decides what happens
when it is too loud.

**A stopped window is `windowCut`.** `readItem` gains it beside `windowEnd`. `windowEnd` means a
window completed: the filler links it up and takes its first line as the read's oldest, and the
reader takes it as its edge. A stopped window did none of that, since the lines between it and the
point were not read. On `windowCut`:

- the filler writes the window's last batch with no `Next`, and leaves the read's oldest line as it
  was, so the narrowed window still links up to what the stopped one would have;
- the reader leaves its edge alone and clears the window's oldest line (`windowOldest`), or the
  narrowed window would carry the island's oldest line into the edge, and a read that then stopped
  short would claim lines in the gap it never read;
- the stream's count does not take the island's kept lines. For a backward read the filler counts a
  window's kept lines on the window and adds them to the stream's count on `windowEnd` alone, so
  `more` never answers from lines the page cannot reach. A forward read still counts each line as it
  arrives and is cancelled at `need`.

**Too quiet: widen.** A window that ends with fewer than `need` kept lines is followed by an older
window. It ends where this one began and is sized by §2 from the rates just measured, for the kept
lines still missing. A window with lines but no kept lines is followed by one of the byte target by
its own byte rate. A window with no lines at all makes the next eight times longer; the stop above,
not a byte cap, guards it, since a silent stretch measures no rate.

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
   before `T` is empty. `backwardUntil` already does this. (The lost-lines work marks the hole.)
2. **The half after `T` fills first.** It is one forward read from `T`, as today. Its lines give the
   rates for the half before.
3. **The half before `T`** is an ordinary backward read with `T` as the point. Its key, `(T, −1)`,
   lies inside the gap the forward half's chunk ends, so `gapRange` offers it no `next`. The seam
   now runs the other way: when the forward half's fill read from `T` (`fromKey`), the backward
   half's `next` is the first line that fill wrote, or, when it read its first gap whole and wrote
   nothing, that gap's upper edge (`g.Before`). `streamFill` carries it in a field of its own, since
   `newest` is a `Tail` fill's. `page.seams` hands it from the `Head` query to the `Tail` query, and
   the filler uses it where a `Tail` read starts at its bound with no `next`. When `T` lies inside a
   chunk the cache held, the gap's own edge is the `next`, and no seam is needed.

## Decisions this step asks for

1. **Stop a window at four times `need`, or four times its byte target.** Smaller wastes GETs on
   small misses. Larger downloads more before the stop.
2. **4 MiB windows for a sparse grep.** They read a default 10 MiB log in three windows, and each GET
   costs the node a scan of the file up to its start.
3. **First windows sized under the stream's budget, and stopped before it runs out.** Every stream
   must read one window for the page to make progress. In this step the target is half the share of
   `maxBudget`; step 4 makes it the page's download limit. It only sizes a window; it never refuses
   one.
4. **Rates from the near side.** The chunk at the point is already in the file, so the guess costs no
   GET. The recent rate, not the window's average, projects a window, because the lines nearest the
   point arrive last.
5. **A stopped window is its own item.** `windowEnd` promises a link up and an edge; a stopped window
   can give neither.

## Tasks

1. **The rates.** From the chunk just above the point, in kept lines and bytes; the recent rate inside
   a window; the clamp and the ten-second default; the grep on `readOpts`. — Done.
2. **The time windows.** `backwardUntil` sized by §2; the three stops and the narrowing with
   `windowCut`; widen. — Done.
3. **Doubling `tailLines`.** The first `Tail` page's windows double after a short window. — Done.
4. **`Around`.** The forward half fills first; the seam from the forward half's first line or its
   gap's edge. — Done.

## Tests

- **A burst.** A view of a few streams. One is quiet for days, then logs 5,000 lines/s for the last
  ten seconds before the cursor. Paging back returns lines on every page, and the pages join without
  a lost or repeated line. The hundred-stream case is step 4's regression test.
- **An anchor in a burst.** An `Around` page at the start of the same burst returns `need` lines on
  each side, and its halves join without a lost or repeated line.
- **Quiet stretches.** A log with a day of silence before the point reaches the older lines in a few
  GETs.
- **A bad guess costs little.** Scrolling up from a cursor with a rate ten times too low, a read
  downloads at most about four pages plus one second of lines, and what it downloaded stays on disk
  as an island.
- **The budget stops a window first.** Scrolling up from a cursor with a guess that would run past
  the stream's share, the window stops with budget left, a narrower one completes, and the page is
  not refused.
- **The island keeps its lines to itself.** After a stop, the narrowed window links up to the chunk at
  the point, not to the island, and the page's count holds no island line. The next page reads the
  lines nearest the cursor.
- **A sparse grep.** Three matches in a 10 MiB log, scrolling up from a cursor. The read downloads
  the log once, in three windows plus the first-line GET, and the page answers the three matches.
- **A first window fits its share.** A sparse grep over a hundred 10 MiB logs, scrolling up from a
  cursor: no stream's first window runs past its share, and the page is not refused. A
  single-stream view's first window is 4 MiB.
- **An `Around` page in a cached range.** The forward half answers from the file with no fill, and
  the halves still join. One whose forward half reads its gap whole and finds no line joins too.
- **Searching again is free.** A second grep over a range the first downloaded sends no GET.
- **Doubling `tailLines`.** A sparse grep from the log's end reaches back 10 MiB in about eight GETs,
  not hundreds.
- **`TestPagesJoinWithoutLossOrRepeat` and `TestTheFileKeepsItsInvariant` pass**, with bursts, quiet
  stretches and sparse greps seeded.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: the backend line describes how a window is sized and
  checked, and the page line names `windowCut` beside `windowEnd`.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the Go checks. No wire change.
