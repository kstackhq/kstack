# The log viewer's library

The headless part of the log viewer: what feeds it, the lines it holds, and the window that
decides what every event does. Nothing here draws.

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

## The window

`LogWindow` (`window.ts`) is a class with no React in it. It holds the lines (`lines.ts`, over
the keyed deque in `double-tailed-array.ts`), both edges, the pin, the pause and where the after
edge resumes. Each event changes that state and answers an `Effect` for the scroller: `none`,
`hold` (keep the first visible row in place), `toEnd`, or `toKey`. It publishes a `CoreState`
through `subscribe` and `getSnapshot`, `useSyncExternalStore`'s shape.

Its rules, one each:

- **Keys are stable.** Key 0 is where the view landed, and a prepend lowers nothing held.
- **Only a `Tail` landing pins**, and only with `pinToEnd`. After that the pin is the user's
  hand: a scroll up unpins, and a scroll to the bottom pins once the page it asks for finds no
  more.
- **Following is derived**: landed, pinned, not paused, not ended. Frames that arrive while not
  following are dropped.
- **A window that is not following is behind**: its after edge always has more. A paused one
  asks for nothing after.
- **A page of repeats reads on** at once; a page with no line that says more is a spent budget
  the user reads on from by hand.
- **One page per edge is in flight**, named by a ticket. A stale ticket's page changes nothing.
- **A refused follow unpins**, so the next scroll to the bottom tries again through a page.

## The fake

`FakeClient` (`fake-client.ts`) is a `Client` over a log it invents, the one client the viewer
runs on until the sidecar serves the wire. It writes lines a millisecond apart from weighted
streams, grows by `append` or at a `rate`, and models what the wire does to a viewer: a live end
`repeat` lines back, a follow that sends per stream, checkpoints, latency (`delayMs`), and
`failNext`, `emptyNext`, `skip` and `endFollows`. `index.ts` leaves it out: import it by its file.

## The boundary

This directory imports its own files and nothing else: no `@/` import, and no relative import
that leaves it. It knows lines, cursors and anchors, and nothing about columns, the viewport,
GraphQL or the error bus.
