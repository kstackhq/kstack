# The log viewer's library

The headless part of the log viewer: what feeds it, the lines it holds, the window that
decides what every event does, and the core that puts the window behind a scroller. Nothing
here draws a row.

## The contract

`types.ts` is the seam between the window and whatever feeds it. A `Client` has two methods:

- `fetch({ mode, cursor, at, limit })` answers a `Page`: its lines oldest first, a `before`
  and an `after` edge, each a cursor and whether there is more that way, and the streams it
  could not read. `Head` reads after `cursor`, `Tail` before it, and `Around` reads `limit`
  lines either side of the moment `at`.
- `follow(after, onFrame)` sends a view's lines as they are written: `Line`, `Checkpoint` (a
  cursor a reopened follow misses nothing after) and `Ended`, the last frame.

**A cursor is opaque.** The client writes every one; the library reads none and passes each
back as it came. A line's cursor names it, and a line whose cursor is held is a repeat, dropped.
A page's edge cursor names where the next page that way continues.

The types mirror the wire's `LogRecord`, `LogRecordsPage` and `LogsFollowFrame` field for field.

### The edges

*The live end* is the edge of a page that read every stream to its end, with `more: false`: where
a follow starts, and where a page after it would find nothing yet. *The cut* is where a page cut
short ends: the line kept farthest from where the read started, or the point past which some
stream was not read.

| Mode | `before` | `after` |
| --- | --- | --- |
| `Head`, no cursor | null, `more: false` | the cut, `more: true` when the page was cut; else *the live end* |
| `Head`, cursor | the cursor given, `more: true` | as `Head` with none |
| `Tail`, no cursor | the cut, `more: true` when the page was cut; else null, `more: false` | *the live end* |
| `Tail`, cursor | as `Tail` with none | the cursor given, `more: true` |
| `Around` | the `Tail` half's `before` | the `Head` half's `after` |

- A `Head` page from a cursor does not look behind it, so its `before.more` is `true` whether or
  not a line is there; a page that way may come back empty.
- An `after` edge always carries a cursor, since the live end is where a follow starts.
- A page with no line and `more: true` is a read that spent its budget. Its edge is still past
  the cursor it went on from, so reading on always moves.
- A page or a follow that goes on from an edge or a checkpoint misses no line. It may send again
  lines at or past it, which the window drops by cursor, and never an older one.
- A follow's lines arrive per stream, not merged: two containers' lines can interleave out of
  time order. A quiet follow sends no checkpoint, and none comes after `Ended`.

## The window

`LogWindow` (`window.ts`) is a class with no React in it. It holds the lines (`lines.ts`, over
the keyed deque in `double-tailed-array.ts`), both edges, the pin, the pause and where the after
edge resumes. Each event changes that state and answers an `Effect` for the scroller: `none`,
`hold` (keep the first visible row in place), `toEnd`, or `toKey`. It publishes a `CoreState`
through `subscribe` and `getSnapshot`, `useSyncExternalStore`'s shape.

Its rules, one each:

- **Keys are stable.** Key 0 is where the view landed, and a prepend lowers nothing held.
- **Only a `Tail` landing pins**, and only with `pinToEnd`. After that the pin is the user's
  hand: a scroll up or a pointer down on the lines unpins, and a scroll to the bottom pins once
  the page it asks for finds no more.
- **Following is derived**: landed, pinned, not paused, not ended. Frames that arrive while not
  following are dropped.
- **A window that is not following is behind**: its after edge always has more. A paused one
  asks for nothing after.
- **A page of repeats reads on** at once; a page with no line that says more is a spent budget
  the user reads on from by hand.
- **One page per edge is in flight**, named by a ticket. A stale ticket's page changes nothing.
  Each edge in the state reads `loading` while it has more and a page that way holds a ticket.
- **A refused follow unpins**, whether `follow` throws or calls `onFailed` later, so the next
  scroll to the bottom tries again through a page.

## The core

`LogViewerCore` (`core.tsx`) is a React component with a render function as its child. It
renders the scroller, a focusable `div` with its own overflow, and hands the render function
`CoreRows`: the rows on screen, each with its key, line and `start`, the sizer's `totalSize`,
both edges, and `loadMore` for an edge row's button. It decides nothing about lines; it tells
the window what the DOM and the client did and performs what the window answers.

- **One landing, one window.** Each landing — the mount, `jumpTo`, `retry`, a new `client` — is
  a fresh window, scroller and `@tanstack/react-virtual` virtualizer. Loading ends a frame
  after the landing settles.
- **Paging follows the visible range**, once loading has ended and while no re-measure is in
  flight. A page that landed holds its edge until the next frame; a page of repeats reads on.
- **The follow is open exactly while the window follows**, its frames handed over once per
  animation frame, or after 250 ms when no frame comes.
- **Intent comes from input events.** A scroll is the user's within 150 ms of a wheel or a
  scrolling key on the scroller, or while a pointer or a touch is down on it, and a pointer down
  unpins by itself. Only the pin is read off it.
- **Answers are state**, so each brings its own commit, performed in the layout effect after
  it: a hold keeps the first visible row by its key, `toEnd`, and `toKey` through
  `scrollToIndex`. While following, every commit that changes the sizer and every resize keeps
  the end in view.
- **The store** (`createCoreStore`) is where the core publishes the window's `CoreState`. A
  parent holds it and reads it with `useCoreFields`, which re-renders only when a field it names
  moves: the visible range moves the state on every row scrolled past. Each landing writes it
  before its first paint.
- **`scrollerRef`** gets each landing's scroller, a callback since each landing mounts a new
  one, for a caller that reads its size or its sideways scroll. It is the DOM node and nothing
  else: the core still knows no columns.

## The fake

`FakeClient` (`fake-client.ts`) is a `Client` over a log it invents, for the tests. It writes its history `spacingMs` apart (a
millisecond by default) from weighted streams, grows by `append` or at a `rate`, and models what
the wire does to a viewer: a live end `repeat` lines back, a follow that sends per stream,
checkpoints, latency (`delayMs`), and `failNext`, `emptyNext`, `skip` and `endFollows`. `index.ts` leaves it out: import it by its file.

## The boundary

This directory imports `react`, `@tanstack/react-virtual` and its own files, and nothing else:
no `@/` import, and no relative import that leaves it. It knows lines, cursors and anchors, and nothing about columns, the viewport,
GraphQL or the error bus.
