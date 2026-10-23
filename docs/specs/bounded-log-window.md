# A bounded log window that reads its dropped lines back

**Area:** `src/lib/log-viewer/`, `src/lib/logs-client.ts`, the viewer's `selection.ts` and
the sidecar's `internal/services/logs` (paths relative to `src/` unless rooted).
**Answers:** [#389](https://github.com/kstackhq/kstack/issues/389).
**Prerequisites:** none.
**Status:** Built: tasks 0 to 3. The ADR is left for the PR that lands it. A seam page skips a pod the scope no longer resolves once its every held line is dropped; [`a-workload-reads-its-gone-pods-from-the-file.md`](a-workload-reads-its-gone-pods-from-the-file.md) closes that, and the two retire together.

## Goal

`maxLines` bounds the window. Each page or batch that takes it past the bound ends with a drop
at one edge, never of a line on screen, and that edge keeps *more*: scrolling back to a dropped
line pages it in again, each once, in time order, with no repeat and no gap of the viewer's
making. Every kept line keeps its key.

The window does not need to keep a line it can read again. Every line the sidecar has served is
answered by its log cache, or by the kubelet while it still holds it. A line both have let go
is the existing hole: the first line past it carries a `gap`, drawn as today. So the cost of
the bound is only the read that brings a dropped line back.

Read `lib/log-viewer/README.md`, `window.ts`, `lines.ts`, `double-tailed-array.ts` and the
*Answers are state* paragraph of `core.tsx` first.

## Where a dropped edge reads back from

A page cursor is positioned only at a page's ends: the sidecar writes each page's `prev` at the
page's first line and its `next` past its last (`pageCursor.edges` in
`sidecar/internal/services/logs/fetch.go`). A follow's lines carry no page cursor at all, and a
long follow is exactly what grows the window. So a drop that falls inside a page, or anywhere in a
follow's lines, leaves no cursor that reads on from the cut, and `seen` cannot rewind a cursor:
the sidecar keeps the newer of a container's `seen` line and the cursor's own entry.

**A dropped edge reads back from a moment.** The window remembers each edge's drops as a *seam*,
a timestamp every dropped line lies on the window's side of, and the next page that way is a
one-sided read at that moment. `FetchQuery` gains two kinds for it, each read as one first page
with the bound the wire already has (`since`/`until` in `LogsFetchInput`):

- `{ kind: 'Until'; at; seen; limit }` reads the `limit` newest lines stamped at or before
  `at`, from `Last`, each container in `seen` strictly before its line. Its `prev` is the before
  cursor from there back; `next` and `follow` are ignored.
- `{ kind: 'Since'; at; seen; limit }` reads the `limit` oldest lines stamped at or after `at`,
  from `First`, each container in `seen` strictly past its line. Its `next` and `follow` are the
  after edge from there on; `prev` is ignored.

**Both seam pages read per container.** `Since` sends `seen` as every page from the after edge
does: each container's newest held line. `Until` sends each container's oldest held line, the
one page from the before edge that sends `seen`. A container with an entry reads from strictly
past or before that line, by cursor; one with none reads from the moment, inclusive. A moment
alone would not do for the before side: a container whose clock runs behind can hold a line
stamped before the seam yet newer than its held run, written after the follow closed, and two
lines of one container can share a nanosecond, which only a cursor tells apart. The window
computes the oldest held line per container off the held lines when it builds the `Until`, one
scan per seam page.

**An older page cursor carries entries, and moves them as a newer one does.** The sidecar
accepts `seen` on a first page from `Last` (`checkRequest` in `fetch.go`), with no padding, as
it does from `First`; its `keyFor` already positions each entry's container either way. The
page's `prev` keeps the entries, each moved to the farthest older line of its container the page
passed, the mirror of `reached`, and `pageCursor.check` accepts an older cursor with entries.
The entries must ride every page back: a seam page's `prev` lands on another container's line,
a high bound by time for a container whose clock runs behind, and the lagging line the entry
kept off the seam page would come in on the next page without it. The schema's comment on
`seen` and the wire rules in `lib/log-viewer/README.md` say so.

**The clients read each kind as one first page with the bound as given**: `until: at` with
`pageFrom: 'Last'`, `since: at` with `pageFrom: 'First'`, `seen` on both. Both bounds include
every line at the moment, unlike the older half of `At`, which ends the nanosecond before it.
The fake (`fake-client.ts`) answers each from the moment inclusive and applies `seen` on the
`Until` as the mirror of its `Continue` rule.

**Each container's held lines are one run of its log.** A follow and a page after deliver a
stream in its own order, and the before side is time-sorted pages going back in time, so a drop
at either edge takes each container's oldest or newest held lines and never opens a hole inside
a container. This rests on a container's timestamps only moving forward, which holds across its
previous and current runs too, since the two share one `containerKey` and the previous run is
the older. This is what makes one line per container enough at each edge: a container's newest
kept line precedes every line of it an after-edge drop took, and its oldest follows every one a
before-edge drop took.

**Lines come back in time order**, as the page's client sorted them, whatever order they first
arrived in: a follow's interleaving is not replayed. Held lines a seam page sends again are
repeats, dropped by cursor; a page of them reads on.

**The seam is cumulative.** Lines are held in arrival order: a follow's streams interleave and a
page after may bring a line older than one held, so the newest line dropped by one drop may be
older than one dropped before it at the same edge. The seam of a drop is therefore folded into
the seam already set, never set over it:

- **Before edge.** The seam becomes the newest timestamp among the dropped lines, or the seam
  already set, whichever is later. Every line ever dropped there is stamped at or before it. The
  page is an `Until` at the seam; its `prev` becomes the before cursor, and the seam is cleared.
- **After edge.** The seam becomes the oldest timestamp among the dropped lines, or the seam
  already set, whichever is earlier. Every line ever dropped there is stamped at or after it.
  The page is a `Since` at the seam; `resumeOf(page)` becomes `resume`, and the seam is cleared.

After the seam page, paging that way is the ordinary `Continue` from the cursor it answered. A
line read back is new, not a repeat, because the drop takes the dropped lines' cursors out of
`held`; a kept line the page sends again is a repeat, dropped by cursor as today. `paged` treats
a seam page as any other page: `newest`, `repeats` and `empty` are read off the lines it holds.

More lines at the seam's nanosecond than one page holds page through the entries the page's
cursor carries, as every range does today; a `stream-skipped` notice is the sidecar's byte
budget for one second, unchanged by this.

## The drop

A drop runs at the end of `paged` and `followed`, after the page's or the batch's lines are in,
while the window holds more than `maxLines`. It never runs in `landed`: a landing is one page,
and one over `maxLines` is brought down by the first page or batch after it. **While following,
the range the cap protects is the end**, as many lines as are on screen: the core keeps the end
in view on every commit, so a batch larger than the room above the range before it still ends
at the bound. **The bound is restored once
the reader's range settles after each page or batch, not held at every moment**: a page drop
capped short while not following, or a landing over the bound, leaves an excess that stays
until the next page or batch.

**Which edge.** While following, the before edge, whether a batch or a page brought the lines:
the reader is at the end, and a page before that lands after the user pressed End must not close
the follow they asked for. Otherwise `paged` drops at the edge the page did not add to, so the
drop never takes the lines the reader is at.

**How many.** The excess over `maxLines`, capped so that the visible range and
`loadMoreThreshold + 1` lines past it on the dropped side stay. The window holds the range the
core last reported by its first and last keys, so a prepend or a drop leaves it on the same
lines. The margin keeps the dropped edge
out of the core's paging threshold, since a drop that put it there would page that edge, and the
drop that page brings would page the other, without end when `maxLines` is small. The core
passes `loadMoreThreshold` through `LogWindowOptions`. With no visible range yet, nothing is
protected. A drop capped short leaves the window over `maxLines` until the next drop. **The
bound the window settles at with the reader at an edge is
`max(maxLines, visible + loadMoreThreshold + 1)`**, the lines the cap never gives up there; a
`maxLines` under that is met by the cap, not the setting. **A reader between the edges settles
at or under `max(maxLines, visible + 2 · loadMoreThreshold + pageSize + 1)`**: the margin on the
side last dropped, and on the other the threshold that asked for the last page plus that page,
which the drop at the opposite edge never touches. The window's tests and the core's assert
against the edge bound while following and the between bound while paging, and that paging
stops with every visible key held. **A drop of zero lines changes nothing**: no seam, no ticket
cleared, no cursor cleared, no unpin.

**What the drop does**, in the window:

1. Takes the lines off `Lines` at that edge, which takes their cursors out of `held`, and folds
   their farthest stamp into the seam, as above.
2. Clears the ticket at that edge. A page in flight that way was a `Continue` from the cursor the
   drop clears, or a seam page at the seam it moves; its lines would land next to a hole. Stale,
   it changes nothing when it lands, as a stale ticket does today. The core asks again through
   its range effect, which an after-edge drop fires by moving `length`; a before-edge drop
   leaves the before edge past the margin, so the next ask that way is the reader's scroll up.
3. Before edge: the before cursor is cleared, the edge keeps `more` and loses `empty`, and
   `visibleRange` moves down by the count so the next `emit` reads the right lines. After edge:
   `resume` is cleared, the pin and the wish to pin are dropped, since the newest lines are no
   longer held, and `newest` is rebuilt from the lines still held. A window that dropped at the
   after edge is behind, so its after edge has more by the existing rule.
4. Answers `hold` for a before-edge drop unless the answer is already `toEnd`; an after-edge drop
   changes nothing the scroller must do beyond what the page already answered.

**Invariants** the tests pin:

- `resume` is null exactly while an after seam is set, and the window is then unpinned, so
  `followFrom` is never called over a null `resume`.
- `beforeCursor` is null exactly while a before seam is set, or the before edge has no more.
- A seam is set only while its edge's cursor is null: the before seam while `edge.before.more`,
  the after seam while landed. `query(edge)` answers the seam's `Until` or `Since` while the edge
  has more and a seam is set, the `Continue` while a cursor is, and null otherwise. A paused
  window keeps its after seam and answers null after, as it answers null over a cursor.
- No ticket is held at an edge just dropped: the page it named changes nothing when it lands.
- Each container's held lines are one run of its log, at both edges, so `newest` rebuilt from
  the kept lines is each container's newest kept line.
- A page or a follow that leaves the window at or under `maxLines` drops nothing and changes no
  edge state.
- A seam page before, and every `Continue` from its `prev`, adds no line of a container past
  that container's oldest held line; a seam page after, and every `Continue` from it, adds none
  before its newest. Both by cursor.
- A seam page of repeats alone that says more reads on, and one with no line that way and a page
  on is `empty`, both by the rules `paged` has today.
- A landing clears both seams, as it clears every other edge state.

## Keys are stable across a drop

`DoubleTailedArray` gets `dropFirst(n)` and `dropLast(n)`. `dropFirst` raises `keyOffset` by `n`,
so every kept line keeps `keyOffset + index`; `dropLast` moves nothing. Either may cross the
head–tail boundary, in which case it drops the one side whole and the rest off the other.
`Lines` gets `dropFirst(n)` and `dropLast(n)` that return the lines dropped, their cursors taken
out of `held`.

`CoreState` gains `firstKey`, the key of the first line held, 0 with none, and the core reads it
from the snapshot in place of `keyAt(0)`. **`length` and `firstKey` together move whenever the
lines held change**: a page or a follow that adds lines moves `length`, and a drop that brings
it back moves `firstKey`, since a prepend lowers it and a before-edge drop raises it, and a drop
at the after edge follows a prepend. So `emit` publishes every change to the lines, and a render
reading the snapshot sees it; a steady follow at `maxLines` moves `firstKey` by each batch.

The core needs nothing else for keys: `getItemKey` reads `firstKey + index`, and the virtualizer
caches a row's measured size by its key, so a kept row keeps its measurement across the shift. A
`hold` answer is read before the commit as a key and an offset and performed after it through
`indexOfKey`, so it survives a drop that keeps the first visible row, which every drop does.

**A dropped key comes back on another line.** Keys stay consecutive, so the next prepend after
a `dropFirst`, or append after a `dropLast`, hands the dropped keys out again, and a read back
in time order puts a different line under each. Anything that holds a key across the drop must
let it go while the key is held by no line, and that window is always a commit wide: the drop
clears the ticket at its edge, so the first add there is a page the core asks for from an
effect, after the dropped state has rendered. So:

- **The selection prunes on `firstKey` and `length`.** `useSelection` (`selection.ts`) takes
  both and, in a layout effect on them, removes every selected key and cell outside
  `[firstKey, firstKey + length)`, and clears the anchor and the last clicked key when they go.
  A selection over a dropped row is gone before its key can name another line.
- **A reused key's cached measurement** is an estimate for one render, corrected by the row's
  measure on mount as any row's first render is; nothing lasts past it.

## The core

`maxLines` joins `Settings` in `core.tsx` with a default of `5000`, passed to `LogWindow` through
`LogWindowOptions` with `loadMoreThreshold`. `LogViewer` passes nothing. `CoreRows` is
unchanged: `lineOfKey` already answers `undefined` and `indexOfKey` `-1` for a key no longer
held, which is what a copy over a dropped row gets in the commit that drops it.

## Tasks

Do them in order. Each one passes `make test-changed`. The four are one pull request.

0. **The sidecar.** `checkRequest` accepts `seen` on a first page from `Last` with no padding,
   each entry's container reading strictly before its line; `edges` moves an older cursor's
   entries to the farthest older line each container passed, as `reached` does the newer way;
   `pageCursor.check` accepts an older cursor with entries. Tests in `fetch_test.go`: a first
   page from `Last` with an entry stops that container before the entry and reads the others
   from `until` inclusive; a lagging container's line newer than its entry, stamped under the
   bound, stays out across three pages of one line each, the `prev` of each carrying the
   entries on; two lines of one container at one nanosecond, the newer the entry, read the older
   alone, across a page boundary too; padding beside it is still refused. Update the schema
   comment on `seen` and `sidecar/internal/services/logs`'s docs.
1. **The deque and the lines.** `dropFirst`/`dropLast` on `DoubleTailedArray` and `Lines`.
   Tests in `double-tailed-array.test.ts`: a drop at each end within one side, one that crosses
   the boundary, one of everything, and the keys of the kept elements unchanged after each. In
   `lines.test.ts`: the dropped lines returned in arrival order, their cursors fresh again on the
   next `append`/`prepend`, and the kept ones still repeats.
2. **The window and the clients.** `Until` and `Since` in `types.ts`, read by `logs-client.ts`
   as one `read` each and answered by `fake-client.ts`. `maxLines` and `loadMoreThreshold` in
   `LogWindowOptions`; `firstKey` in `CoreState`; the seam per edge; the drop at the end of
   `paged` and `followed`; `query` answering the seam page. Tests in `window.test.ts`: a follow
   past `maxLines` drops at the before edge, keeps `length` at `maxLines` and moves `firstKey`,
   answering `toEnd`; a page before that lands while following drops at the before edge and
   leaves the follow open; a page after drops at the before edge and holds; a page before drops
   at the after edge, unpins, rebuilds `newest` from the kept lines and sends that `seen` on the
   next page after; the visible range and the margin past it are never dropped, the drop capped
   short, and a drop with no visible range takes the whole excess; a page that leaves the window
   at `maxLines` changes no edge state; the seam is cumulative, pinned with a second drop at the
   same edge whose farthest stamp lies short of the first's; the before query after a drop is an
   `Until` at the seam with each container's oldest held line as `seen` and the after query a
   `Since` with the rebuilt `newest`;
   the seam page's `prev` (or `resumeOf`) replaces the seam; a seam page of repeats reads on; a
   drop clears the ticket at its edge and the page it named changes nothing; a landing clears
   the seams; the `Until` after a drop sends each container's oldest held line, so a lagging
   container's line newer than its held run, written after the follow closed and stamped
   before the seam, and a newer line at the same nanosecond as the oldest held, both stay for
   the after side, which brings them past `seen`; a page before that lands with the visible
   range at the top
   drops at the after edge and keeps every visible key; a dropped line both the cache and the
   kubelet let go comes back as a `gap` on the line past it, through the fake's `gaps`.
   `fake-client.test.ts` pins each new kind reading from its moment inclusive, a `Since`
   reading past its `seen` per container as a `Continue` does, and an `Until` reading before
   its. `logs-client.test.ts` pins `until: at` and `since: at` sent as given, with `seen`.

   Extend the seeded walk at the end of the file with a small `maxLines`, an out-of-order line
   that two drops at one edge straddle, streams whose clocks differ, a stream dropped whole at
   one edge, and alternating drops at both edges. A drop by arrival order, and a seam page, can
   both leave the held lines short of one global stretch of the fake's log, so the walk's checks
   change: after every step, each container's held lines are one run of its log, no line is held
   twice, and scrolling back and forward over a drop brings every dropped line once, by identity
   against the fake. The check that a page before brings only lines older than everything held,
   and a page after only newer, and the one-stretch check over held and passed-over lines, apply
   to pages that are not seam pages and to a walk with no drop.

   As built, the fake keeps one clock: its streams' clocks do not differ, since its reads key on a
   line's place in one log. Out-of-order lines come from a follow sending a spaced `append` stream
   by stream, and a burst of them while following, then small batches, is what two drops
   straddle.

3. **The core and the docs.** `maxLines` in `Settings`, and `firstKey` read from the snapshot.
   Tests in `core.test.tsx`: a follow run past `maxLines` keeps the window at `maxLines` with the
   end in view; scrolling up past a drop pages the dropped lines in again and the first visible
   row stays where it was; reading back past `maxLines` drops at the after edge and reaching the
   bottom pages them in and pins once the page finds no more; a batch larger than the room
   above the view ends at the edge bound once the end is in view, with no further batch; a
   small `maxLines` with a still reader between the edges settles after one page each way,
   under the between bound, paging stopped and every visible key held; a selection over a row
   the drop takes is gone, and a reordered read back under the same keys selects nothing (in
   `selection.test.tsx`); rows of differing heights read back under reused keys settle at
   their measured sizes, and a page made stale by a
   drop holds its edge for the frame it lands in, as every page does. Then update
   `lib/log-viewer/README.md`: the contract names the two new kinds, the edges table gains
   their rows, and the `seen` rule names `Until` as the one page from the before edge that
   sends it; the window's rules gain *The window is bounded* and *A dropped edge reads back
   from a moment*, and *Keys are stable* says a drop lowers nothing held. Add the bound to the
   brief in `src/CLAUDE.md`. In `docs/TODO.md`, remove the first item under *Frontend (webview)*,
   and reword the inline card's item: the kinds that ask for one half exist, and the card still
   asks `At`.

## Acceptance

- A follow that runs past `maxLines` brings the window back to the edge bound,
  `max(maxLines, visible + loadMoreThreshold + 1)`, once each batch's end is in view, with the
  view pinned there. A reader between the edges stays under the between bound, with paging
  stopped.
- Scrolling back past a drop pages the dropped lines in again, each once, in time order, with no
  repeat and no gap of the viewer's making, and the first visible row stays where it was. A
  line the sidecar can no longer serve shows as the existing `gap` on the line past it.
- `fetch_test.go`, `window.test.ts`, `lines.test.ts`, `double-tailed-array.test.ts`,
  `fake-client.test.ts`, `logs-client.test.ts` and `core.test.tsx` cover a drop at each edge
  and a read back over it.
- `make lint-js` passes, and `make cover-js` does not fall.

## Verification

```sh
make test-changed
pnpm test --run src/lib/log-viewer src/lib/logs-client.test.ts
make lint-js
cd sidecar && go test ./internal/services/logs/...
```

## When it lands

`lib/log-viewer/README.md` states the bound, the seam, the two one-sided kinds and the key rule;
`src/CLAUDE.md`'s brief names `maxLines`. An ADR records why a dropped edge reads back from a
moment rather than a page cursor, why the seam is cumulative, and why both seam pages read per
container, the entries riding every page back. The TODO item is gone, and this spec with it.
