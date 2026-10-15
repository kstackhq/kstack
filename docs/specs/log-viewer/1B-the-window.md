---
title: The window
scope: frontend
status: Built
---

# The window

**Needs:** nothing of the code; the contract in the README. **Unblocks:** step 2.

TypeScript paths are under `src/`.

## In short

The library `src/lib/log-viewer/` is started: the `Client` contract, a keyed deque, the lines it
holds, and `LogWindow`, a class with no React in it that keeps a window of a log and
decides what every event does — a landing, a page, a follow's frames, the user's scroll up or
down, a pause. It answers each event with what the scroller must do next, and publishes what it
knows through `subscribe` and `getSnapshot`. A fake client comes with it — the one client the
viewer runs on until the sidecar's wire lands, so it models what that wire does to a viewer —
and a seeded random walk drives the window through thousands of steps against the fake's log.

After this step the window works against the fake, and nothing on screen uses it.

## What is not in this step

- **No React, no scroller, no virtualizer.** Step 2 puts the window behind one. The window
  knows a visible range as two indexes it is told, and nothing about pixels.
- **No client in the app.** Step 3 mounts the fake there, through `src/lib/logs-client.ts`.
- **No rows, columns, selection.** Steps 3, 4A, 4B.
- **No eviction.** The window keeps every line it is given. Bounding it is not in this sequence.

## Design

### 1. The contract

`src/lib/log-viewer/types.ts`:

```ts
/** Opaque. The library hands a cursor back to the client that issued it and reads nothing from it. */
export type Cursor = string;

export type LogLine = {
  /** When the line was written: RFC 3339 in UTC (`Z`), with up to nine fraction digits. */
  timestamp: string;
  /** Names the line. A repeated line is dropped by it. */
  cursor: Cursor;
  /** The text. The library draws nothing and strips nothing: the client hands it text fit to show. */
  message: string;
  /** The log the line came from. Its four fields together name it. */
  stream: { namespace: string; pod: string; container: string; previous: boolean };
  /** Where the stream's pod runs. Empty where unknown, and no part of the stream's name. */
  place: { node: string; region: string; zone: string; os: string; arch: string };
};

/** Where the window is told to be. `at` is the moment, RFC 3339, set for `At` alone. */
export type Anchor = { kind: 'Head' | 'Tail' | 'At'; at: string | null };

export type FetchMode = 'Head' | 'Tail' | 'Around';

export type FetchQuery = {
  mode: FetchMode;
  /** A page's edge cursor, a checkpoint's, or a line's. Null reads from the start or the end, and always for `Around`. */
  cursor: Cursor | null;
  /** For `Around` alone: the anchor's moment, RFC 3339. */
  at: string | null;
  limit: number;
};

/** One side of a page: where the next page that way continues, and whether there is one. Null on a `before` edge with nothing older. */
export type PageEdge = { cursor: Cursor | null; more: boolean };

export type Page = {
  /** Oldest first, as the client sorted them. */
  lines: LogLine[];
  before: PageEdge;
  after: PageEdge;
  /** For `Around`: the index of the first line at or after the moment, `lines.length` with none. Null otherwise. */
  anchor: number | null;
  /** Streams the page could not read, one line of text each, for the viewer to show. */
  skipped: string[];
};

/** `Ended` is the last frame: the follow is over, and following again would end the same way. */
export type FollowFrame =
  | { kind: 'Line'; line: LogLine }
  | { kind: 'Checkpoint'; cursor: Cursor }
  | { kind: 'Ended'; reason: string };

/** Where lines come from. Anything that implements it can feed the window. */
export type Client = {
  fetch: (query: FetchQuery) => Promise<Page>;
  /** Calls back per frame written after `after`, until the function it answers is called. */
  follow: (after: Cursor, onFrame: (frame: FollowFrame) => void) => () => void;
};
```

These mirror the wire's `LogRecord`, `LogRecordsPage`, `LogRecordsPageEdge` and
`LogsFollowFrame` (the README's [*The wire*](README.md#the-wire)) field for field, so the real client, when it comes, has
nothing to map but names. The modes mean: `Head` reads oldest first after `cursor`, from the
start with none; `Tail` reads newest first before `cursor`, from the end with none; `Around`
reads `limit` lines before `at` and `limit` from it on, so its page holds up to twice `limit`,
and takes `at` and no cursor. A page's lines are oldest first whatever the mode.

### 2. The deque

`src/lib/log-viewer/double-tailed-array.ts`, from kubetail (both copyright lines): an array that
grows at both ends in amortised O(1), indexed from its first element, where each element also
has a **key** that never changes while it is held. Keys run `keyOffset`, `keyOffset + 1`, …;
a prepend lowers `keyOffset` by what it adds, so the elements already held keep theirs.

Its surface: `length`, `at(index)` (throws `RangeError` outside the length), `first()`,
`last()` (undefined when empty), `keyAt(index)`, `indexOfKey(key)` (unchecked), `append(items)`,
`prepend(items)` (in their own order). Both add with a loop, since a spread's arguments are
capped and a follow's batch is not.

### 3. The lines

`src/lib/log-viewer/lines.ts`: `Lines`, what the window holds — a deque of `LogLine` and a `Set`
of every held line's cursor. A cursor names one line, so a line whose cursor is held is a repeat
— what a follow opened again sends of what arrived after its last checkpoint — and is dropped.
Lines stay in the order the client answered them: a page as the client sorted it, a follow as it
arrived.

- `reset(lines, zeroAt)`: starts over, key 0 at the index `zeroAt`, kept within the lines.
- `prepend(lines)` and `append(lines)`: add the lines not held, each once, and answer how many
  were added.
- `length`, `at`, `keyAt`, `indexOfKey`, `first`, `last`.

`lines.ts` also exports `compareTimestamps(a, b)` (§4, `setVisibleRange`). No callbacks: the
window notifies.

### 4. The window

`src/lib/log-viewer/window.ts`: `LogWindow`, made with `{ pinToEnd, firstPageSize, pageSize }`.

```ts
export type Edge = 'before' | 'after';

export type EdgeState = {
  /** Whether there are more lines that way. */
  more: boolean;
  /** Whether the last page that way held no line at all but said there are more: a budget spent, read on by hand. */
  empty: boolean;
};

export type Effect =
  | { kind: 'none' }
  /** Keep the first visible row where it is on screen across this change. */
  | { kind: 'hold' }
  | { kind: 'toEnd' }
  | { kind: 'toKey'; key: number };

export type CoreState = {
  loading: boolean;
  /** The landing failed. The window holds nothing and pages nothing; `jumpTo` lands again. */
  failed: boolean;
  /** Whether the viewport keeps the end in view. The user's hand changes it. */
  pinnedToEnd: boolean;
  /** Whether the follow is open: landed, pinned, not paused, not ended. */
  following: boolean;
  /** Why the follow ended, in the client's words; null while it has not. A landing clears it. */
  ended: string | null;
  /** Whether the newest line is on screen: following, with the last line in the visible range. */
  atEnd: boolean;
  /** The earliest and latest timestamps among the visible lines; null before any is on screen. */
  range: { from: string; to: string } | null;
  /** How many lines are on screen. */
  visible: number;
  /** How many lines the window holds. It moves whenever a page or a follow adds one, so a render reading the snapshot sees every new line. */
  length: number;
  /** The edges, as `edges()` answers them. */
  before: EdgeState;
  after: EdgeState;
  /** Every stream a page of this landing could not read, once each, first seen first. */
  skipped: string[];
};
```

`CORE_INITIAL_STATE` is the snapshot before any event: `loading` true, every other flag false,
`ended` and `range` null, `visible` and `length` 0, both edges `{ more: false, empty: false }`,
`skipped` empty.

**State** (private, published through `getSnapshot` as `CoreState`):

| Field | Meaning |
| --- | --- |
| `lines` | the `Lines` |
| `landed`, `failed` | whether the landing settled, and how |
| `pinned`, `paused` | the pin and the user's Pause |
| `ended` | the reason off an `Ended` frame, until the next landing |
| `wantsPin` | the user's last scroll reached the bottom of a window that is behind; the next page after that finds no more pins |
| `before`, `after: EdgeState` | the server's word on each edge, off the last page that way |
| `readOn: { before, after }` | whether the last page that way held lines, all of them held already, and said more |
| `resume: Cursor \| null` | where the after edge continues: the last page's `after.cursor`, or the last checkpoint |
| `ticket: { before, after }` | the page in flight toward each edge, by a number the window hands out; a ticket names its edge |
| `visibleRange` | the first and last index on screen, as the adapter last told it; null before any |
| `skipped` | every stream a page of this landing could not read |

**Derived**, each a method:

- `following()`: `landed && pinned && !paused && ended === null`.
- `edges()`: `{ before, after }` as the adapter pages by and the render function draws.
  `before` is the server's. `after` is `{ more: false, empty: false }` while following or
  paused, and otherwise `{ more: landed, empty: after.empty }`: a window that is neither is
  behind the live end whatever the last page said, and a paused one asks for nothing, since
  Pause stops lines arriving.
- `readOn(edge)`: `readOn[edge]` and `edges()[edge].more`. The adapter pages that way again at
  once: a page of repeats moved the edge but brought no row to scroll to.
- `atEnd()`: `following()` and `visibleRange.end === lines.length - 1`.
- `query(edge): FetchQuery | null`: what to fetch toward `edge` — `{ mode: 'Tail', cursor:
  before.cursor }` or `{ mode: 'Head', cursor: resume }`, with `at: null` and `limit: pageSize`;
  null when `edges()[edge].more` is false, which a `before` edge with no cursor always is.
- `followFrom(): Cursor`: `resume`. Only called while following, when it is never null.

**Events.** Each changes the state and notifies the listeners once if the snapshot changed. Adding
a line always changes it, since it carries `length`. Those that change the lines answer an
`Effect` for the adapter to perform after the next commit.

- `landing(anchor): FetchQuery`: `{ mode: 'Head', cursor: null, at: null }`, `{ mode: 'Tail',
  cursor: null, at: null }` or `{ mode: 'Around', cursor: null, at: anchor.at }`, `limit`
  `firstPageSize`. `Around` reads that many on each side, so an `At` landing holds up
  to twice as many lines as the others: more context either side of the moment. Changes
  nothing; the adapter fetches it.
- `landed(page, anchor): Effect`: `lines.reset` with key 0 at the first line for `Head`, the
  last for `Tail`, and for `At` at index `min(page.anchor, lines.length - 1)` (0 with no line).
  `before` and `after` off the page's edges, each `empty` set when the page held no line and
  said more that way, so a landing that spent its budget offers its edge row's read-on as a page
  does; `readOn` false; `resume =
  page.after.cursor`; `skipped` off the page; `landed = true`; `ended = null`; `wantsPin =
  false`; `pinned = pinToEnd && anchor.kind === 'Tail'`. A `Tail` landing shows the end, so it
  follows from there; a `Head` or `At` landing shows its anchor, and pins as any window that is
  behind does, once the user scrolls to the bottom. Effect: `toEnd` for `Tail`, `toKey(0)` for
  `At` with a line, `none` otherwise.
- `landingFailed(): void`: `failed = true`, `pinned = false`, `landed` stays false.
- `startPage(edge): number | null`: a fresh ticket when `landed`, no ticket is out that way, and
  `query(edge)` is not null; else null. Records the ticket. The adapter fetches `query(edge)`.
- `paged(ticket, page): Effect`: `none` and no change unless `ticket` is the one out toward its
  edge, which it then clears. Only the edge paged toward reads the page's edges; the other side
  of the page is ignored. On either edge: that edge's state is the page's edge,
  `empty` set when the page held no line and said more, and `readOn` set when it held lines,
  added none, and said more; the page's `skipped` joins `skipped`. For `before`: `lines.prepend`;
  effect `hold`. Keys are stable across a prepend, so the adapter reads the first visible row's
  key and offset off the layout still on screen, after the call returns and before the commit
  draws the new lines. For `after`: `lines.append`; `resume =
  page.after.cursor`; then, when `wantsPin`:
  - the page said no more: `pinned = true`, `wantsPin = false`, effect `toEnd`, whether or not it
    added lines: they are the newest, and a follow would have brought them the same way;
  - it added lines and said more: `wantsPin = false`, since the user has rows to read before the
    bottom, and reaching it again asks again;
  - it added none and said more: `wantsPin` stays, and the read-on carries it to the end.

  Effect `none` otherwise.
- `pageFailed(ticket): void`: nothing unless `ticket` is the one out toward its edge; then clears
  it and that edge's `readOn`, so a failure waits for the user's next scroll. The adapter reports
  the error.
- `followOpened(): void`: clears `ticket.after`, so a page after asked before the follow opened
  is dropped when it lands.
- `followed(frames): Effect`: `none` and no change unless `following()`. A batch the adapter
  flushes after the follow closed — the user scrolled up or paused before the unsubscribe ran —
  must neither add a line behind the reader nor pull the scroller to the end; the page after
  that comes next reads those lines again from `resume`. While following: each `Line` appended
  less those held, each `Checkpoint` moving `resume`, and an `Ended` setting `ended`, which closes
  the follow by derivation; frames after it are ignored. Effect `toEnd`. Notifies only when a
  line was appended or the follow ended, since `resume` is not in the snapshot.
- `followFailed(): void`: the client refused to open the follow. `pinned = false` and `wantsPin =
  false`, so the window is behind: the user's next scroll to the bottom pages after, pins when
  that page finds no more, and so opens the follow again. The adapter reports the error.
- `scrolledUp(): void`: `wantsPin = false`, `pinned = false`. The follow closes by derivation.
- `reachedBottom(): boolean`: nothing while following or paused, and answers false; else
  `wantsPin = true`, and answers true. On true the adapter pages after at once, since a scroller
  already at the bottom moves no range to page from; the page pins when it finds no more.
- `setPaused(paused): void`. Pausing also clears `ticket.after` and `wantsPin`, so a page after
  asked before the pause adds nothing when it lands, and nothing pins until the user plays and
  reaches the bottom again.
- `setVisibleRange(start, end): void`: for `atEnd`, `visible`, and `range`, the earliest and
  latest timestamps among the lines from `start` to `end`: a follow's lines keep their arrival
  order, so the first and last visible need not be the bounds. `compareTimestamps` in `lines.ts`
  orders two of the contract's timestamps as instants: the text up to the seconds, then the
  fraction padded to nine digits, since `Date.parse` cuts it to milliseconds. Notifies only when
  the snapshot changed: `range`, `visible` or `atEnd`.
- `loadingEnded(): void`: `loading = false`. The adapter calls it a frame after the landing
  settled, landed or failed.

**Publishing.** `subscribe(listener)` and `getSnapshot(): CoreState`, `useSyncExternalStore`'s
shape. A snapshot is a new object only when a field changed, so a render reading it does not
loop. `skipped` is a new array only when a stream joins it, and an edge a new object only when
one of its flags changed.

### 5. The fake client

`src/lib/log-viewer/fake-client.ts` (both copyright lines), which `index.ts` leaves out: the
tests import it by its file, and so does step 3's `logs-client.ts`, the one place in the app
that does. It is a `Client` over a log it invents, made with `FakeClientOptions`: `lines` lines
a millisecond apart, the last stamped with the clock's time when the fake is made, written by the
streams `streams` names — each a `LogLine['stream']`, a `place` and an optional `weight`, so step
3 can seed it from a view's own sources — in a fixed rotation in which each stream takes `weight`
turns a round (1 by default), spread evenly through it. The log grows at `rate` lines per second or by `append(count)`, each new line stamped
with the clock's time and never earlier than the line before it. The clock is `Date.now()`, so
fake timers move it. Each fetch takes `delayMs`. Every string it writes is plain ASCII of its
own: nothing a sink need worry about, and nothing that looks like a real line.

- **Cursors are the fake's own.** A line's names its index in the log; an edge's names a
  position between two. Both are opaque text the fake can tell from anything else. A cursor it
  did not write rejects the fetch, or throws from `follow`, since the window must send only what
  it was given; so does a query mixing `cursor` and `at`, or `Around` with none.
- **`fetch` answers the contract's edges.** `Head` with no cursor: `before` null and no more,
  `after` the cut with more when the limit cut the page, else the live end. `Head` with a
  cursor: `before` that cursor with more. `Tail` with no cursor: `before` the cut with more when
  the limit cut it, else null and no more; `after` the live end. `Tail` with a cursor: `after`
  that cursor with more. `Around`: the `Tail` half's `before` and the `Head` half's `after`, and
  `anchor` the index of the first line at or after `at`. The live end is the position `repeat`
  lines (20 by default) before the newest line when the fetch was called, and never earlier than
  where the read started — its cursor, or, for a read with none, the page's oldest line — so a
  page or a follow from it sends again up to `repeat` lines the window holds, and never one it
  does not. That keeps the window's drop of a
  repeat exercised, and a `repeat` above the page size makes a page of nothing but repeats,
  which keeps its read-on exercised.
- **`follow(after, onFrame)` sends per stream, like the sidecar.** It replays the lines after
  `after` once `delayMs` has passed, holding back what is appended meanwhile and sending it with
  the replay. After that, each append's new lines go out inside the `append` call that wrote
  them, one stream's batch after another in the streams' order, so lines of two streams written
  in one append arrive out of time order; then one `Checkpoint`, `repeat` lines before the
  newest line and never earlier than the follow's `after` or the last checkpoint it sent. A
  checkpoint's cursor is a position like an edge's, so `fetch` takes it as a `Head` cursor and
  `follow` as `after`. A cancelled follow delivers nothing.
- **Reading on moves.** `emptyNext()` makes the next fetch, a landing or a page, answer no line,
  as a read that spent its budget on lines a grep dropped does. Each edge that reads on — a
  `Head` read's `after`, a `Tail` read's `before`, both of an `Around` — sits `limit` lines past
  where that read started, with more; where that passes the end of the log, the edge is that
  end, as the mode's table says. The other edge is as the table says, and `Around`'s `anchor` is
  0.
- **It fails on demand.** `failNext('fetch')` makes the next fetch, a landing or a page, reject
  with an `Error`, and `failNext('follow')` makes the next `follow` throw, the one way the
  `Client` type lets a follow fail; `skip(stream)` keeps a stream's lines out of every page and
  every follow's frames and names it in each page's `skipped`, until `unskip(stream)`; `endFollows(reason)` sends an `Ended` frame to
  every open follow, and nothing after it.
- `lineAt(index)`, `lineCount` and `passedOver()` for a test's checks. `passedOver()` lists
  each span an empty read moved its edge across — from where the read started to that edge —
  whose lines no read will serve.

Its delays are timers, so a test drives them with fake timers, and the app runs them real.

### 6. The index and the README

`src/lib/log-viewer/index.ts` exports `LogWindow`, `CORE_INITIAL_STATE` and the types of §1 and
§4, `Edge` among them. `src/lib/log-viewer/README.md` states the contract, the window and the boundary, for a
reader who opens the directory cold.

## Decisions this step asks for

- **A class, not a reducer.** The lines are a mutable deque and the effects are answers to
  calls; a class says that plainly, and a test calls it like any object.
- **The window answers effects; the adapter performs them.** The window never sees an element,
  so every rule about lines, edges and pinning is testable without a DOM, and the adapter has
  nothing to decide.
- **A window that is not following is behind.** One rule for unpinned, ended and landed short
  of the end, at the cost of one small page when the user scrolls back to the bottom. While
  unpinned near the end, each move of the visible range can ask for a page after: one fetch at
  a time, cheap on the fake. The real client's spec weighs it against the sidecar's GETs.
- **Pause asks for nothing.** A paused window's after edge has no more, so it neither pages nor
  pins, and a page after already in flight lands as nothing, so the user can rest at the bottom
  while the log is written (step 4A's Pause). Playing again while pinned reopens the follow from
  `resume`.
- **Only a `Tail` landing pins.** `pinToEnd` keeps the end in view, and only a `Tail` landing
  starts there. A `Head` or `At` landing shows its anchor even when its page reached the end, and
  pins when the user scrolls to the bottom.
- **The pin from the bottom waits for the page.** A scroll that reaches the bottom of a window
  that is behind asks for the lines written meanwhile first; pinning on the scroll alone would
  open a follow over a gap. A page that brings rows to read and says more cancels the wish, so a
  chain of pages never pulls the reader past lines they have not seen; the last page, which
  says no more, pins and shows its lines as a follow's batch would.
- **A page of repeats reads on.** A page whose lines were all held moved its edge and showed
  nothing new, so the window says to read on at once. Only a page with no line at all is the
  budget spent that the user reads on from by hand.
- **A refused follow unpins.** The window then reads as behind, so the user's next scroll to the
  bottom tries again through a page, and nothing claims to follow a stream that is not open.
- **Lines from a closed follow are dropped.** The window ignores frames while not following, so
  the adapter's unsubscribe can lag the close without a line landing behind the reader.

## Tasks

`double-tailed-array.ts` and `fake-client.ts` carry both copyright lines. `lines.ts` and
`window.ts` are written fresh from this spec, not ported from kubetail's viewer, so they and the
rest carry kstack's line alone. `@tanstack/react-virtual` is not added here; step 2 adds it.

1. **The contract.** `src/lib/log-viewer/types.ts` (§1). **Done.**
2. **The deque.** `src/lib/log-viewer/double-tailed-array.ts` and its test (§2). **Done.**
3. **The lines.** `src/lib/log-viewer/lines.ts` and its test (§3). **Done.**
4. **The fake client.** `src/lib/log-viewer/fake-client.ts` and its test (§5). **Done.**
5. **The window.** `src/lib/log-viewer/window.ts` and its test, with the walk (§4, *Tests*). **Done.**
6. **The index and the README.** `src/lib/log-viewer/index.ts`, `index.test.ts`, `README.md`
   (§6). **Done.**
7. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Done.**

## Tests

Vitest, beside the files they cover, under `vi.useFakeTimers()` where the fake client's delays
are in play. No DOM anywhere in this step.

- `double-tailed-array.test.ts`: keys hold across prepends; `at` throws outside the length;
  `first` and `last` undefined when empty.
- `lines.test.ts`: `reset` with each `zeroAt`; `compareTimestamps` orders two stamps that differ
  below the millisecond, and stamps with different fraction lengths; a line held is dropped on prepend and on append,
  by its cursor alone; two lines of one container at one moment with different cursors are both
  kept; a follow's order is kept as it arrived.
- `fake-client.test.ts`: each mode's edges; `Around`'s `anchor`; a line cursor as a `Head`
  cursor reads after that line; a cursor the fake did not write rejects the fetch and throws from
  `follow`, and so does `cursor` beside `at`; a checkpoint's cursor reads on as a `Head` cursor and
  opens a follow; a follow replays after `delayMs`, then sends
  appended lines once, inside `append`, one stream's batch after another, and a checkpoint after
  each append; a page or a follow from the live end repeats `repeat` lines and no more, and with
  `repeat` above the page's length, none older than the page's oldest line; a checkpoint is never
  earlier than the follow's `after`; weights write each stream in proportion; the clock
  stamps appended lines and never runs backwards; `failNext('fetch')` rejects the next fetch and
  only it, `failNext('follow')` throws on the next follow and only it; a skipped stream is out of
  every page and named in `skipped`; `emptyNext` answers no line with its read-on edges `limit`
  lines past where each read started, on a landing and on a page, and `passedOver()` lists
  the span each moved across; `endFollows` sends `Ended` and
  nothing after it; a cancelled follow delivers nothing; every string is ASCII.
- `window.test.ts`, one case per rule:
  - `getSnapshot` answers `CORE_INITIAL_STATE` before any event;
  - each landing kind puts key 0 where §4 says and answers its effect; `At` with no line after
    the moment puts key 0 at the last line; an empty landing that says more sets `empty` on
    each edge that says so;
  - `length` moves, and listeners hear, on every page and batch that adds a line, and not on one
    of repeats;
  - a pinned `Tail` landing follows; a pinned `Head` or `At` landing is not pinned, even when its
    page reached the end;
  - `landingFailed` leaves nothing to page and `pinnedToEnd` false;
  - `startPage` answers null while a ticket is out, before landing, and when the edge has no
    more; `paged` and `pageFailed` with a stale ticket change nothing; `followOpened` makes the
    after ticket stale;
  - a page before holds, prepends less the held, and sets `empty` when it held no line and said
    more; a page after appends and moves `resume`;
  - a page of nothing but repeats that says more sets `readOn` and not `empty`; the next page
    clears it; `pageFailed` clears it;
  - `edges().after.more` is false while following and while paused, and true otherwise, whatever
    the page said;
  - `reachedBottom` sets `wantsPin` and answers true, and does nothing and answers false while
    following or paused; `scrolledUp` clears it and
    unpins; the page that finds no more pins and answers `toEnd`, whether or not it added lines; one that adds lines and finds
    more clears `wantsPin`; one of repeats that finds more keeps it;
  - `skipped` gathers each page's streams once and a landing resets it;
  - `followed` appends less the held, a checkpoint moves `resume` and a line does not, checkpoints
    alone notify no one; `toEnd`;
  - `followed` while not following — unpinned, paused, ended or not landed — changes nothing and
    answers `none`;
  - an `Ended` frame sets `ended`, closes the follow by derivation and ignores the frames after
    it; `edges().after.more` turns true; a landing clears `ended`;
  - `setPaused(true)` closes the follow by derivation, asks for no page after, clears `wantsPin`,
    and a page after asked before it adds nothing when it lands; `setPaused(false)` while pinned
    follows again;
  - `followFailed` unpins; the next scroll to the bottom pages, pins and follows;
  - `getSnapshot` answers the same object until a field changes; `visible` and `atEnd` follow
    `setVisibleRange`, and `range` spans the earliest and latest visible timestamps when a
    follow's lines arrived out of time order; a visible range that moves its end alone notifies
    when `atEnd` changes.
- `window.test.ts`, the walk: a seeded linear congruential generator drives a `LogWindow` over a
  `FakeClient` log written by three pods, one at a tenth of the others' weight, for 100 seeds of
  60 steps. The seed
  picks the first landing, its `pinToEnd`, and the fake's `repeat`, sometimes above the page
  size. Each step is one to three of: a scroll to a random row, to the bottom, one row up, a
  pause toggled, lines appended, a jump to a random anchor, a read-on at an edge, a held fetch
  answered, a checkpoint, the follows ended, the next follow refused, time passing. A jump makes a new window with
  `pinToEnd` set for a `Tail` anchor alone, as step 2's `jumpTo` does. The test models the
  adapter: the scroller as a scroll top over rows of one height and a view of five rows, each
  effect applied to it, `setVisibleRange` called off it, a page asked as the range nears an edge,
  again while `readOn` says so, and after when `reachedBottom` answers true, and the follow
  opened and closed as `following()` turns,
  its frames handed to `followed` before the step's checks. Each fetch is answered whole, empty
  through `emptyNext`, or failed, as the seed decides, some held until a later step answers them
  in any order. After every step it checks: the lines held, with the spans `passedOver()`
  lists, are one stretch of the log, each once — what pages brought in log order, what a follow
  brought in the order it arrived;
  following means pinned, not paused, not ended and landed, and once the follow's replay has
  been handed over, holding the log's last line; no line was appended while paused, and only
  pages added any while unpinned; an ended follow stays closed until a landing; a refused
  follow leaves the window unpinned; pinned turned on only by a `Tail` landing with
  `pinToEnd`, or by a scroll to the bottom whose pages found no more; a scroll up that ended off
  the bottom left it unpinned; a failed fetch leaves its edge to be asked again; a step with no
  scroll, no page landing and no follow leaves the first visible row's key and offset where
  they were.

## Security

**Widens:** nothing. The window draws nothing and talks to nothing; the fake reaches the bundle
only in step 3, which mounts it on purpose, and invents every string it hands out. No dependency
is added.

## When it lands

- Root `CLAUDE.md`, *Frontend*: a paragraph on `src/lib/log-viewer/` — the contract, the
  window's rules in a sentence each, the fake, and the boundary.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `make test-changed` while working; `pnpm build`, `make test-js`, `make lint-js`.

Nothing to see by hand. The tests are the whole check.
