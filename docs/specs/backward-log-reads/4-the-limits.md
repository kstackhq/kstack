---
title: The limits
scope: sidecar
status: Done
---

# The limits

**Needs:** step 3, whose windows are bounded, so a limit checked between windows bounds a page.
**Unblocks:** step 5. This step closes #320 and runs the sequence's integration gate.

Go paths are under `sidecar/internal/services/logs/`. The shared design is the README; this file
says what to build and how to know it is built.

## In short

The README's limits of normal become limits on a page. Most of them cut the page, and the next page
goes on. One, a single second too loud to read, stops the stream at that second: the page is cut
there with a reason the user can read, and the next page reads on past it. Every cut has an edge, so
a page always makes progress. Bytes are reserved before a window's GET, so streams reading at once
stay within the page. The old budget, its even split and the *"ran out of budget"* refusal go.

## What is not in this step

- **The kubetail offer, and any viewer change.** The reason reaches the viewer through `skipped`, as
  any failed stream's does today.

## Design

### 1. The limits

| Limit | Value | When it is reached |
| --- | --- | --- |
| Bytes in one second of a stream's log | 1 MiB | the stream stops at that second (§4) |
| Bytes one stream downloads for one query | 16 MiB | the page is cut when a window ends past it; a window may run up to 12 MiB past (§2) |
| GETs one stream sends for one query | 16 | the page is cut |
| Bytes one query downloads, all streams together | 64 MiB | the page is cut |

They apply to every read a page makes: backward, forward, and the first `Tail` page's `tailLines`
window, whose bytes §2 bounds by its lines. A page runs one query per direction, so an `Around` page
has two queries, each with its own limits. Bytes are counted as the body carries them: timestamp,
space, message and newline. That is the unit `counted` spends and step 3's rates use. Every GET
counts, `firstTimestamp` and `wholeMoment` included.

A follow's gap fill is a forward read under the smaller of 16 MiB and 64 MiB ÷ the follow's streams,
so one round of fills reads no more than one query. A fill cut there goes on at once from its edge
(§3), as one its budget cut does today. A follow never stops a stream at a loud second (§4).

### 2. Reserving bytes

Before any read starts, each stream the query fills (not `exhausted`) is given an **allowance**: the
smaller of 16 MiB and 64 MiB ÷ those streams. That is 16 MiB each up to four streams, 4 MiB at 16, and
about 640 KiB at 100. The query's **pool** starts with what the allowances leave of 64 MiB: 48 MiB
for one stream, none from four streams on.

**Until a stream's first window ends, it spends its allowance.** `firstTimestamp` and the
`tailLines` window spend from it, and a first time window reserves what is left of it. When the first
window ends, what is left of the allowance goes to the pool.

**A time window reserves four times its byte target before its GET.** At its end, the bytes it did
not use go back to the pool.

- A time window that is the stream's first window has a byte target of a quarter of what is left
  of its allowance: about 4 MiB up to four streams, 1 MiB at 16, and 160 KiB at 100.
- Every other window is a later window, the time windows after a `tailLines` window included. Its
  target is the smallest of 4 MiB, what is left of the stream's 16 MiB, and a quarter of the
  stream's share of the pool: the pool divided by the streams past their first window whose fills
  have not ended and that hold no reservation. It reserves four times that from the pool, so one
  reservation takes at most the stream's share and every other stream reading finds the same. A window may read up to four
  times its target before it is stopped, so a stream goes past its 16 MiB by at most three times
  its last window's target: 12 MiB. Those bytes were reserved from the pool, so they stay within
  the query's 64 MiB.

**A reservation is accounting, not a hard stop.** Step 3's check stops a window that is due to hold
more than four times its target, and the 1 MiB second (§4) stops a window as its lines arrive. Bytes
a window reads past its reservation before either stops it are charged to the pool, which may go
below zero. That is at most 1 MiB, one second's worth.

A window that replaces a stopped one runs whatever the pool holds, since a limit is checked only when
a window completes (§3). A target under `minWindowBytes` gives the one-second window as §3 defines
it.

**A forward read spends as it reads**, from its allowance and then from the pool. It is cut inside
its GET once the stream's 16 MiB is spent, or once both the allowance and the pool are empty, but
never before its first line in range. `sinceTime` is whole seconds, so the GET reads the second it
started in from its top, lines before the bound included; a read cut with no line in range would
have no edge and make no progress. That second is at most 1 MiB, or the stream stops at it (§4), and
the first line past it is in range. A follow's fill reads the second whole, whatever its size, so a
follow always gets past it.

A forward read whose lower bound is a moment at a second's last nanosecond sends `sinceTime` of the
next second. No line of that second is in range, so the read neither downloads it again nor counts
it toward the 1 MiB. Without this, a read after a forward stop (§4) would start in the loud second
and stop there again.

**The `tailLines` window** of the first `Tail` page asks for `need` lines. It asks for no more than
its allowance holds at 16 KiB a line, the runtime's part size, and for at least `minBatch`. It is
read whole: it is not checked, and no limit cuts it, since its first lines are its oldest and a cut
would leave no edge. `tailLines` counts the runtime's parts, not the joined lines the kubelet
returns, so with the default 16 KiB parts the window stays within its allowance. A runtime set not to
split lines makes a part as long as its line, and then the window's bytes are bounded only by its
line count and the log file. Its bytes past the allowance are charged to the pool. When the window
leaves the read short, the read goes on in time windows, not by doubling `tailLines`. The next
window ends at the moment the `tailLines` window began in, which it reads whole, since that window
left that moment's lines out. It is a later window, with its target from the pool, and is sized from
the rate the `tailLines` window measured, as step 3's widening sizes one.

So a query downloads at most 64 MiB, plus up to 1 MiB per stream past a reservation, plus what the
first `Tail` page's `tailLines` windows hold past their allowances, plus what a forward read's start
second and first line in range hold past its allowance and the pool: up to 1 MiB and a line.

**Streams return bytes as their windows end, so timing decides where a page is cut.** Whether the
pool covers a stream's next window depends on which other streams have finished. A page is correct
either way; only its cut moves. Tests of more than one stream assert bytes and joins, never a count
of GETs.

### 3. Cut

A page that has read a lot is normal: a sparse grep, a quiet log read back far, or many containers.
The stream that reached a limit stops reading, and the page is cut with `more: true` at the stream's
edge. The next page goes on from the cut.

A backward read checks its limits only once a window completes (`windowEnd`), never inside a window
and never after a stopped one (`windowCut`). After a stop a replacement always follows. A stream is
cut when, at a window's end:

- it has spent its 16 MiB, or sent its 16 GETs, or
- its next window's target (§2) would be under `minWindowBytes` (64 KiB).

A stream that has been cut is offered no further gap on the page: the walk that reaches the gap it
was cut in stops at its edge.

**Every cut has an edge.**

- **Backward:** the oldest line of the last window that completed with a line. When the last window
  that completed was empty, the edge is its start, a moment: the read took every line from there to
  the point and found none. An island is older than the edge, and the lines between them were never
  read, so the cut is never at an island.
- **Forward:** the last line it read in range, which it always has, since it is never cut before
  its first (§2).

`streamFill` carries the edge's moment when what the fill wrote does not show it: an empty window's
start, or a stop's second (§4). The walk that reaches the gap the fill was cut in stops at that
moment, not at its last line.

**The one-second window** is the one from the top of the second its end lies in, or from the second
before when its end is a second's top. It covers one second of the clock, and step 3 never checks it
as too busy. Any window sized to a reach of one second is this window, whatever its end: one
measured back a second from an end inside a second would cover two seconds of the clock, be
checked, and cost a GET before the one-second window followed anyway.

**A stopped window is replaced from its stop, or halved.** Step 3's narrowing, which sizes the
replacement back from the point at the stop's rate, goes. It assumes the dense stretch runs on to
the point; a dense stretch far from the point, with quiet between it and the point, makes it crawl,
since every window widened across the quiet is stopped at the stretch again. The replacement of a
stopped window is instead:

- **Stopped in a second that is not its first or last:** the window from the second after that one
  to its end, checked as any window is. The lines between are the quiet the stop left unread.
- **Stopped in its last second:** the one-second window, which reads that second whole or stops the
  stream (§4).
- **Stopped in its first second:** the window half as long, to the same end, down to the one-second
  window. That second is dense and unread, and the stretch's newest second lies between it and the
  end; halving finds it in about log₂ of the distance in GETs.

**The newest dense second bounds widening.** A window stopped in its first second shows that second
dense and unread. Halving can stop a window in its first second again, and each such stop is newer
than the last; the bound is the newest of them. Until a window that begins at it completes, no
window starts before it: a widened window that would is cut to start there, and is then halved when
it is stopped. Without the bound, widening eightfold after a quiet window crosses the stretch
again, and the read bisects the same quiet once more. With it, the read bisects the quiet between
the stretch and the end, reads the stretch's newest second as the one-second window, and then reads
the stretch at its own rate. The bound is the read's alone: a page cut mid-bisection leaves the
next page to find the stretch again from the edge, which is nearer by what the first page
completed.

A dense second far from the point costs about four GETs; a dense stretch an hour back, about two
GETs per halving of the hour, which runs past one page's 16 GETs. In step 3's near-burst example the
replacement from the second after the stop is stopped in its first second and halved down to the
one-second window at the point: five or six GETs instead of three.

An empty window that completes writes nothing to the file, so its range stays a gap there. A later
page that crosses the range reads it again, one GET; nothing links across it.

**A chain of stops is bounded by the GET limit.** Once a stream has sent 16 GETs, or its next
window's target would be under `minWindowBytes`, a stopped window is replaced by the one-second
window ending where it would have ended. That window either completes, giving the edge, or holds more
than 1 MiB, and the stream stops (§4).

A short reading with no stop that the connection's retirement did not cut can no longer happen.
`mergePage` returns it as an internal error, not a `Refusal`, so a test that reaches it fails loudly.

### 4. Stop

A second that holds more than 1 MiB of one stream's log is the one case no normal read gets past. A
window cannot be made smaller than one second, and such a second is not normal. A single line past
1 MiB is such a second.

Every read of a page but the `tailLines` window counts the bytes of each second of the clock as its
lines arrive. A long line is counted as it is read, not once it ends. The GET ends once one second
passes 1 MiB. What then happens depends on the read:

- **A one-second window, `wholeMoment`, or a forward read:** the stream stops at that second.
- **A wider time window:** it is stopped (`windowCut`), since the lines between that second and the
  point are not read yet, and replaced as §3 says. The one-second window that reaches the loud
  second in the end stops the stream. So a loud second far from the point costs a few GETs, not one
  per second between them.

The stream's read ends, and **the page is cut at that second**. Backward, the cut is the moment at
the second's first nanosecond; forward, the moment at its last nanosecond. A moment position
(`position.moment`) stands before, or after, every line of its nanosecond. The page keeps the other
streams' lines on the near side of the cut, all of that second's lines included, so the next page,
bounded by the moment, repeats none of them. The next page reads on past the second. Only that second
of the stream is left out: all of it backward, and forward what the read had not taken before the
1 MiB. The page names it in `skipped`:

> *more than 1 MiB of log in the second at 2026-10-17T09:14:03Z, more than Kstack reads through the
> Kubernetes API; lines of that second are left out*

`skippedLine` puts `pod/container: ` before it, as for any skipped stream.

What the stopped read wrote stays in the file: an island backward, the start of a chunk forward. A
later read through that second stops there again, and is cut at it again.

The stop is its own failure kind, `failLimit`, beside fatal, denied, ended and transient. `failureKind`
checks for it first, since an error that is not a `statusError` is otherwise transient. The kubetail
offer can find it later. The error carries the second, and its words are a `Refusal` value, so no
caller changes them.

A stopped stream is cut, not dropped:

- `streamFill` carries the stop's moment, as it carries a cut's (§3). The walk of a stream whose fill
  stopped is short, with its stop at that moment, so the merge cuts the page there (`trimToStops`)
  and the page answers `more: true`.
- A stopped stream does not count toward `everyStreamFailed`. A view whose only stream stops answers
  a page with no lines from that stream, the reason in `skipped`, and `more: true`. The page is never
  refused for it.

### 5. The walk

A stream's walk reads its even share of `maxWalkBudget`, plus the bytes its fill downloaded for the
query. A stream the pool let read far is then walked as far, and a sparse grep does not return empty
pages over lines already in the file. A page's walks read about 128 MiB of the file at most: the walk
budget, and what the fills downloaded, overages (§2) included.

### 6. What goes

- `maxBudget` and the per-stream `share` in `fillQuery`, and the share a follow's fill takes from it.
- `counted`'s hard stop for a window: a reservation is charged, not enforced (§2).
- Step 3's budget stop: the third condition of `plan.tooBusy`, and `plan.left`. A window's
  reservation (§2) replaces it.
- Step 3's narrowing, `reader.narrow`: a stopped window is replaced from the second after its stop,
  or halved (§3).
- `fillGap`'s offer of a further gap while budget remains: a cut stream is offered none (§3).
- Doubling `tailLines` after the first window.
- The *"ran out of budget"* refusal in `mergePage`.

## Decisions this step asks for

1. **16 MiB and 16 GETs per stream, 64 MiB per query.** They keep a page to a few seconds. A default
   10 MiB log fits in one page's reads in a view of up to four containers, in three 4 MiB windows.
2. **Bytes are reserved before a GET, four times a window's target.** A window holds at most about
   that before it is stopped, so streams reading at once stay within the page. A later window's
   target is sized from the stream's share of the pool, so one reservation never empties it and
   which streams are cut does not depend on which finished a window first. A reservation is
   charged, not enforced, so no read ends without an edge.
3. **1 MiB in one second is the line for abnormal.** It is about 5,000 lines of normal length. A line
   past 1 MiB is such a second.
4. **The stop cuts the page at that second.** One wild container does not take the view down, and
   only its loud second is left out.
5. **Every cut has an edge, and the next read starts past it.** A forward read is never cut before
   its first line in range, and a read from a second's end starts at the next second; an empty
   window's edge is its start. So a page always makes progress.
6. **After the first `tailLines` window, time windows.** A time window's bytes are bounded; a
   `tailLines` window's are bounded only by its lines, so there is one, read whole.
7. **The walk follows the fill.** A stream is walked as far as its fill read.
8. **A stopped window is replaced from its stop, or halved, and the newest dense second bounds
   widening.**
   Narrowing from the point assumes the dense stretch reaches the point. Halving assumes nothing: it
   finds the stretch's newest second in about log₂ of the distance in GETs, wherever it lies, and
   the bound keeps widening from crossing the stretch again.

## Tasks

1. **The reservations and the limits.** The allowance, the pool and its seed, a window's reservation,
   charged not enforced; a forward read's spending, never cut before its first line in range, and
   `sinceTime` past a bound at a second's end; the three cutting limits; the `tailLines` window's
   cap and the time windows after it; the follow's fill at its share. — Done.
2. **The cut.** Checked at `windowEnd` alone; no further gap for a cut stream; the edge, a line or a
   moment, carried by `streamFill` to the walk; the replacement from the second after a stop, the
   halving and the newest dense second's bound on widening; the one-second replacement; the
   internal error in `mergePage`. — Done.
3. **The stop.** The 1 MiB second, counted per second of the clock and inside a line; stopping a
   wider window and stopping the stream; `failLimit`; the moment cut through `streamFill`, the walk
   and the merge; `everyStreamFailed` passing it by. — Done.
4. **The walk.** Its share plus what the fill downloaded. — Done.
5. **The removals.** `maxBudget`, the split, `counted`'s hard stop for windows, the budget stop,
   narrowing, doubling, the refusal, and the tests of each (*Tests*). — Done.
6. **The regression test and the gate.** — Done.

## Tests

- **The regression.** A 100-stream view where one stream bursts at 5,000 lines/s of 100-byte
  messages (about 650 KiB/s, under the 1 MiB second) just before the cursor pages back with no
  refusal and no stopped stream.
- **First windows share the limit.** A sparse grep over a hundred 10 MiB logs: each stream's first
  window targets about 160 KiB, and the first page downloads at most 64 MiB plus 1 MiB a stream, not
  400 MiB. A single-stream view's first window targets 4 MiB.
- **Streams reading at once stay within the page.** A hundred quiet streams whose first windows use
  little of their allowances: their later windows together reserve no more than the pool holds.
- **A grep with no match.** One stream: the read reaches the log's first line, and the page answers no
  record and no more. A hundred streams: pages are cut with `more: true` until every stream reaches
  its first line, and no stream stops.
- **The per-stream cuts.** One stream, a sparse grep over a 40 MiB log is cut at 16 MiB after four
  4 MiB windows. On a log whose windows are tiny it is cut at 16 GETs. Each next page continues from
  its cut.
- **An empty window's cut.** A quiet stream, cut when its last completed window is empty: the page is
  cut at that window's start, and the next page goes on below it.
- **A forward cut.** A sparse grep read forward over a 40 MiB log is cut inside its GET at 16 MiB, at
  its last line, and the next page goes on from it.
- **A forward read in a busy second.** A 100-stream view whose cursor lies at the end of one stream's
  900 KiB second: the read takes that second whole past its allowance, and the page returns its lines
  past the cursor.
- **The `tailLines` window.** From the log's end with a sparse grep, the first window is capped by its
  allowance, and the read goes on in time windows of at most 4 MiB. With one stream, it reaches back
  10 MiB in about four GETs and the first-line GET.
- **Long lines.** A 100-stream view whose lines are 256 KiB each, with the fake set not to split
  them (`part: 0`): the first `Tail` page's `tailLines` window is read whole past its allowance, and
  the page is not refused. With the default 16 KiB parts, the same window stays within its allowance.
  Scrolling up from a cursor, one line of 2 MiB is a loud second and stops its stream.
- **A chain of stops ends.** A log whose windows are each stopped as too busy: after 16 GETs the next
  window is the one-second window, it completes, and the page is cut at its oldest line.
- **Too loud.** One stream logs 2 MiB in one second just before the cursor. The page is cut at that
  second's top, the stream is named in `skipped` with the reason, the other streams' lines at and
  after that second are on the page, and the next page reads on below it. The same, forward, cuts at
  the second's last nanosecond. A wider window holding that second is replaced by one from the
  second after it, then by the one-second window.
- **A dense second far from the point.** One stream, no grep, scrolling up from a cursor 30 quiet
  seconds above a second of 5,000 lines: the window that reaches the second is stopped there on the
  kept-lines check, its replacement from the second after it completes, and the read goes on below
  the second within a few GETs. No stop.
- **A dense stretch far from the point.** One stream, no grep, 30 seconds of 5,000 lines each an
  hour above the cursor and quiet between: paging back reaches the stretch's newest second over more
  than one page, each cut at 16 GETs, then reads the stretch at its own rate. No stop, and the pages
  join without a lost or repeated line.
- **A loud second far from the point.** One stream, scrolling up from a cursor 30 quiet seconds
  above a 2 MiB second: the stream stops at that second within four window GETs, and the page is cut
  at its top.
- **Past a forward stop.** After a Head page is cut at a loud second's end, the next Head page's read
  sends `sinceTime` of the second after it, does not stop again, and returns that stream's lines past
  it.
- **Two half-loud seconds.** A first window ending mid-second, over two seconds of 600 KiB each, is
  not stopped as loud.
- **One stream, too loud.** A view whose only stream is too loud answers an empty page with the reason
  and `more: true`, not a refusal. The next page reads the lines below the second.
- **A follow's fill.** A fill of one of four streams over a 40 MiB gap is cut at 16 MiB and goes on
  from its last line. A fill over a 20 MiB second reads it whole and gets past it. A fill whose
  watermark is the last line of a second that alone spends its share is cut after its first line
  past that second, not before, and the follow walks on.
- **The walk follows the fill.** A sparse grep in a 100-stream view where one stream's fill reads
  8 MiB: its walk reads all of it on the same page.
- **No budget refusal.** No seeded walk of `TestPagesJoinWithoutLossOrRepeat` ends in *"ran out of
  budget"* or the internal error, with loud seconds and long lines seeded. Its oracle leaves out each
  loud second of the stream holding it, and expects that stream named in `skipped`.

**Tests this step invalidates**, deleted or rewritten under task 5:

- `TestAForwardReadSpentBeforeItsFirstLineHasNoStop` asserts the cut §2 forbids; it goes.
- `TestABusyWindowIsStoppedAndNarrowed` counts the narrowing's three GETs; it is rewritten for the
  halving, five or six.
- The budget's tests go with it: `TestAWindowDueToRunPastTheBudgetIsStopped`,
  `TestAWindowIsSizedUnderHalfTheBudgetLeft`, `TestABackwardReadStopsAtTheBudget`,
  `TestAForwardReadStopsAtTheBudget`, `TestAFetchSharesItsBudgetPerStream` and
  `TestAFillThatSpendsItsBudgetLeavesTheWalkItsOwn`; `TestABackwardReadDoublesItsWindows` with the
  doubling.
- Step 3's spec tests *Doubling `tailLines`*, *The budget stops a window first* and *A first window
  fits its share* are replaced by *The `tailLines` window*, *Streams reading at once stay within the
  page* and *First windows share the limit*.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: the limits, the allowance and the pool, reservations
  charged not enforced, cut and stop; every cut's edge, a line or a moment; the read off the log's
  end is one `tailLines` window, then time windows; a forward read is never cut before its first line
  in range, and one from a second's end starts at the next second; a stopped window is replaced from
  the second after its stop, or halved, and the newest dense second bounds widening; the walk's share
  follows the fill; `failureKind` gains `failLimit`.
- The README's step 4 row.
- Close #320.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the full checks, since this
step is the sequence's integration gate. No wire change.
