# Lost log lines

The log viewer tells the user where lines may be missing. It draws a short notice above the
first line after a hole, naming the container and the time range. Answers
[#313](https://github.com/kstackhq/kstack/issues/313). Builds on the log cache
(`docs/specs/log-cache/`) and lands on `wip/log-views`. Go paths are under
`sidecar/internal/services/logs/`.

## In short

The kubelet keeps only the recent part of a container's log. Older lines are rotated away. If
the log file never read them, they are gone for good. Today the viewer walks straight past the
hole and shows nothing. The log looks unbroken. A busy container rotates fastest, so it loses the
most, and someone debugging can read a silent hole as "nothing happened".

Three changes fix it:

1. **The file records the hole.** Today the commonest rotation leaves no trace, because a read
   that started after a rotation still links its chunk to the one before it (§1).
2. **Each line says whether a hole comes before it.** A `LogRecord` gains one nullable field,
   `gap`. No new list, frame or cursor (§3, §4).
3. **The viewer draws the notice above that line** (§5).

## What is not in this spec

- **No archive backend.** An archive is what would fill a hole. Once one exists, the tier rule
  already offers it the hole, and the mark goes away on its own.
- **No mark between two instances of a container.** A restart is not a hole. Drawing one is a
  separate row, for a later change.
- **No mark after a finished instance's last line.** An `at_end` chunk means the log ended there.
- **No change to `skipped`.** It still names only reads that failed.
- **No count of the missing lines.** Nothing knows it. The kubelet does not say what it rotated.
- **No mark at the start of a stream.** The kubelet cannot tell a rotated start from a quiet one,
  and guessing from `started_at` would draw a notice on nearly every log. A busy container whose
  start was rotated away shows nothing at the top, as today. Tracked in `docs/TODO.md`.

## Design

### 1. The file records the hole

The log cache's ADR says a link between two chunks means *the backend promised nothing lies
between them*. Two reads break that promise today:

- A forward fill whose GET's first line comes after `prev` still sets `fill.Prev = prev`
  (`fill.go`, `fillOf`). `CoverageOf` then joins the two chunks into one span.
- A tailer resuming after a rotation does the same through `OpenTail(…, prev, merge)` with
  `merge` false (`tailer.go`, `logstore/writer.go`).

Both reads ask the kubelet for lines from `prev`'s second on. If the kubelet still has `prev`'s
line, the GET returns it (`returnedWhole`). If the GET's first line is later than `prev`, then
`prev`'s line was rotated away. Rotation drops the oldest lines first, so everything up to `prev`
is gone too, and the GET's first line is the oldest line the kubelet has. A quiet container never
looks like this, because `prev`'s own line is still there. So the read can tell a rotation from a
quiet stretch exactly.

The rule: **a forward read from `prev`'s second whose GET returned no line at or before `prev`
is a head read, and a head read never links to `prev`.**

The rule is tied to `prev`, not to any lower bound. A fill whose lower bound is the page's own
key, a cursor or a moment the viewer chose, says nothing about rotation: the kubelet never served
a line there, so a first line past it is normal. That read keeps its link as today, through the
seam when it has one. A GET that returned nothing is a head read only when it ended cleanly, as
the no-bound rule already requires: a cut or failed read proves nothing.

- `fill.go` decides the forward case, since only the filler knows `prev`; the fetcher sees a
  position as `since` and cannot tell `prev` from a key. A forward read is head when
  `r.head`, or when `prev` is set, `returnedWhole` is false, and the read got a line or ended
  cleanly. `hole` becomes that. A head read with no line marks the chunk at `Next` as head
  instead, which `Fill.Head` already does.
- `fetcher.go` keeps the backward case in `reachedFirst`: a backward read is head when it reached
  the log's first line and that line is in range. A `Tail` fill's lower bound is always `prev` or
  nothing (`gapRange` moves only the upper bound for `Tail`), so the rule stays tied to `prev`
  there too. With no lower bound, nothing changes.
- `fillOf` clears `fill.Prev` and sets `fill.Head` for **any** head read, forward or backward, on
  every tier. Today its backward branch links to `prev` whenever the read covered its range
  whole, which would link across the hole a backward head read found.
- `logstore/writer.go`: a tail always reads from `prev`'s second, so a tail opened with `prev`
  set and `merge` false is a head read. Its first chunk opens as head with no link. `OpenTail`
  keeps its signature.

`CoverageOf` then finds a gap below the head chunk and marks it lost, because it is older than a
head chunk of the last backend. `coverage.go` does not change. Walks already cross lost gaps, so
pages and follows read on as before. The tier rule also becomes honest: a rotation hole is
offered to the next backend as an older range, which `offer` already does.

### 2. Which holes get a mark

A lost gap gets a mark only when it lies **between two spans** (`After` and `Before` both set).
It came from a rotation (§1), a `Tail` page reaching the kubelet's head above an older span, or
an eviction on a stream no backend serves.

Never marked:

- **Older than every span** (`After` nil). The kubelet cannot tell a rotated start from a quiet
  one. See *What is not in this spec*.
- **Newer than every span** (`Before` nil). That is the range past an `at_end` chunk, or the
  tailer's.
- A live gap.
- A tailer's gap, until the tailer has committed past it. A tailer resumed after a rotation
  leaves its hole past its own start, where the tailer may still write lines. Once it has
  written past the hole, the hole is marked like any other.

Every mark says lines *may* be missing. A rotated file can end exactly at `prev`, and an evicted
chunk can hold no line that passes the grep.

### 3. The wire

```graphql
type LogRecord {
  # ...existing fields...
  "Set when lines of this stream may be missing just before this line. Null when none are."
  gap: LogGap
}

"""
Lines of a stream the cluster no longer holds and the log file never read. They lie before the
line that carries the gap.
"""
type LogGap {
  "When the last known line before the hole was stamped."
  after: Time!
  "When the first known line past the hole was stamped: the carrying line's own stamp, unless the grep hid the lines between."
  before: Time!
}
```

That is the whole wire change. Pages and follows carry the mark inside the line:

- `LogRecordsPage.records` stays a plain list.
- `LogsFollowFrame` keeps its three types. A `Line` frame's record carries `gap` like any other.
- In Go, `Record` gains `Gap *Gap`, and `Gap` holds `After` and `Before`, both `time.Time`. `graph/` maps it as it
  maps the rest of `Record`.

### 4. Which line carries the mark

**A record carries every marked hole between it and the previous line of its stream that the
view passes.** The carrier is fixed by position, not by which walk happened to cross the hole.
A line is sent exactly once across a page tiling (`TestPagesJoinWithoutLossOrRepeat`), so a hole
is reported exactly once too, except where a look is cut short (below).

A line *passes* when the walk would send it: a chunk of the page's pattern holds it and the grep
matches it, the same test the walk uses.

**Inside a walk**, a hole crossed between two records goes to the record just past it in time:
the later one, which a `Head` walk or a follow reaches next, and a `Tail` walk has already
reached.

**At a walk's edge**, the carrier may lie outside what the walk read. With no grep the previous
passing line is the line just before the hole, so this never happens. With a grep, lines past the
hole may fail it, so a walk can stop between a hole and its carrier, or start there. Each walk
therefore takes one look past its edge:

- **A `Head` walk** looks back from its first record, through failing lines and holes, until it
  meets a passing line. Every hole it met belongs to that record. It does not stop at the first
  hole: two holes with only failing lines between them are both the record's.
- **A `Tail` walk** looks past its oldest record the same way.
- **A follow** takes the `Head` look for its first line on each stream, from where the follow
  started. Pages carry nothing past the last passing line, so the follow's start may lie past an
  uncarried hole, and so may a checkpoint it reopened `after`. After that first line, the follow
  keeps a hole it crosses on its stream's `tracked` until the stream's next sent line.
- **The look runs only when coverage says there is a hole to find.** Coverage is in memory, so
  the walk checks for a marked hole between the record and the previous span's end first. A grep
  with rare matches over a busy log pays for the look only when it will find something.
- **The look has a budget of its own**, `lookBytes`, a `Retention` knob, and leaves the walk's
  alone, so a page's cut stays where the walk put it. When its budget runs out, or it meets a
  live gap that eviction opened under its path, it stops and attaches what it found. Every hole
  it met lies behind lines that all failed the grep, so those are this record's. A cut look can
  miss holes past the cut; it never repeats one.
- Two holes on one record take the older `after` and the newer `before`. The notice then covers
  both, and the lines read between them.
- `merge.go` does not change. The mark rides inside its record.
- A follow reopened `after` a checkpoint may send the line again. The window drops it by cursor,
  mark and all.

**No later match, no notice.** Under a grep, a hole with no passing line after it has no carrier
yet. A follow shows it when a match arrives. A page that is not following never shows it.

**A mark learned late never shows.** The window keeps the first copy of a line it holds. A gap
that becomes lost after its carrier is on screen, through eviction or a `Tail` page reaching the
kubelet's oldest line, is marked only on the next landing, when the line is fetched again.

### 5. The viewer

`LogLine` in `src/lib/log-viewer/types.ts` gains `gap: { after: string; before: string } | null`, mirroring
the wire. `src/lib/logs-client.ts` copies it. Nothing else in the library changes: the window
keys, holds and drops lines as it does today.

**The notice** (`src/components/widgets/log-viewer/`) is one muted line drawn above the line's
own text, inside the same row. The row grows by one `LOG_ROW_PX`. Rows are measured only with
wrap on, so both height estimates in `log-viewer.tsx`, `oneLine` and `wrapped`, add that row for
a line with a gap. It spans every column after the position, and reads:

> Lines may be missing from `namespace/pod/container` between *10:00:03* and *10:05:41*: the
> cluster no longer holds them.

The two times are the gap's `after` and `before`, never the line's own stamp: with a grep the
carrier can sit well past the hole. The names go through `VisibleText`, the times
through the time column's formatter (`line-time.ts`). Cell selection ignores the notice. Row
selection copies it in brackets above the line, so a pasted log still shows the hole.

`FakeClient` lets a line carry `gap`, so stories and tests can show one.

## Decisions this spec asks for

The issue's three questions:

1. **What carries the mark: the next line the view passes.** One nullable field on `LogRecord`.
   It needs no index, no second list, no new frame and no new cursor, and merge, follow and the
   window all keep their shape. The cost is the look each walk takes past its edge (§4), which is
   one line without a grep.
2. **How the viewer draws it: a notice above that line**, naming the container and the time
   range. Not a banner, because a banner cannot say *where*.
3. **The `LogsView` receipt and the model: no change.** `LogsView` reads no line, and its receipt
   says what was opened, not what was read. Rung 4's `LogsRead` will hand the model a sample, and
   that sample names the holes it crossed. Rung 4's spec inherits this.

Two more:

- **Recording the hole changes the file's data, not its schema.** The flags exist: `at_head` and
  a NULL link. A dev file written before this change keeps its links across holes until eviction
  takes them. `make clean-data` resets it.
- **The start of a stream is left blank.** A guess from `started_at` would be wrong on most
  logs, and a notice that is usually wrong teaches users to ignore the one that is exact. The
  open question lives in `docs/TODO.md`.

## Tasks

All Done, in one PR into `wip/log-views`.

1. **Record the hole** (§1). `fill.go`'s `hole` and `fillOf`, `fetcher.go`'s `reachedFirst`, and the
   writer's tail. Update `TestTheFileKeepsItsInvariant`'s model so a rotation leaves a lost gap
   and no link.
2. **Report the hole** (§2–§4). `Gap`, the walker's and the follow's look past their edge, the schema,
   gqlgen, the resolvers.
3. **Draw the hole** (§5). `LogLine.gap`, the client, `FakeClient`, the row, copy.

## Tests

- **Rotation is told from quiet.** A forward fill and a tailer over a fake backend whose oldest
  line is past `prev` write a head chunk with no link. One whose GET returns `prev`'s line links
  as today. A fill from a page's key inside a gap, with and without a seam, links as today
  whatever the backend's first line. A read that returned nothing and failed is not head. A
  backward read that reaches the first line above its bound is head and writes no link.
- **Every marked hole is reported once.** Extend `TestPagesJoinWithoutLossOrRepeat`: across a
  `Head` tiling and a `Tail` tiling of a seeded file with lost gaps, under each of its greps, each
  marked gap appears on exactly one record, the first past it that passes the grep, with the
  hole's own two times. Under `-short`, fewer seeds.
- **Which holes get a mark** (§2). A table over `CoverageOf` results: only a lost gap with both
  edges set.
- **A follow marks the right line**, once, including when a walk's allowance runs out inside the
  gap, and when a grep makes the walk's watermark pass the gap with no line sent. A follow
  reopened at a checkpoint below the gap sends the line again, and the window drops it.
- **The two seams.** Under a grep, a hole after the last passing line is carried by the follow's
  first match, both when the follow opens at the live end and when it reopens `after` a
  checkpoint past the hole.
- **The look-back passes a hole.** Two holes with only failing lines between them, and a page cut
  between them, both land on the next page's first record.
- **The look is skipped** when coverage shows no marked hole behind the record, and attaches what
  it found when the budget cuts it or it meets a live gap.
- **The row's height estimate** adds one row for a line with a gap, with wrap on and off.
- **The notice draws names as text** (`spells out what cluster text could hide`), and row copy
  includes it.

## Security

No new boundary. A mark adds two times the sidecar computed to a line whose names already come
from the cluster. The notice draws the names through `VisibleText`, like every cell
(`security-model.md`'s log line row, which gains the notice's test).

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: a head read never links; a record carries the hole
  before it.
- `src/lib/log-viewer/README.md`: `LogLine.gap`.
- `docs/specs/log-views/README.md`: rung 4's paragraph says a sample names its holes.
- A new ADR, *A head read never links*, for §1's rule. It says log-cache step 3's residual
  (*the viewer cannot tell a lost range from a quiet one*) is closed between spans, and open at
  a stream's start.
- `docs/TODO.md`: the start-of-stream entry stays, and links the ADR.
- Delete this spec.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](README.md): the Go checks for task 1, plus
the wire checks for tasks 2 and 3.
