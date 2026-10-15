---
title: The core
scope: frontend
status: Planned
---

# The core

**Needs:** step 1B. **Unblocks:** step 3.

TypeScript paths are under `src/`.

## In short

`LogViewerCore` puts a `LogWindow` behind a scroller. It is a React component with a render
function as its child: it makes one window per landing, lands it through the client, runs
`@tanstack/react-virtual` over the window's lines, pages when the visible range nears an edge,
opens the follow while the window says so and batches its frames per animation frame, reads
the user's intent off wheel, keyboard, pointer and touch events, performs the window's effects
against the scroller after each commit, and hands its rows to the render function. It draws
nothing and knows nothing of the app.

After this step the core works against the fake in tests, and nothing on screen uses it.

## What is not in this step

- **No rows on screen.** Step 3 draws them.
- **No client in the app.** Step 3 mounts the fake there.
- **No bound on the lines.** The window keeps every line it is given, so the virtualizer's
  count only grows or is reset by a new landing. Bounding it is not in this sequence.
- **No rule about lines, edges or the pin.** Every such rule is the window's (step 1B). If the
  adapter needs to decide something about lines, the window is missing an event.

## Design

### 1. The props and the handle

```ts
export type LogViewerCoreProps = {
  ref?: Ref<CoreHandle>;
  client: Client;
  /** Where the core lands on mount. Read once: a new landing is `jumpTo`, or a new mount. A new `client` lands again where the last landing did. */
  anchor: Anchor;
  /** Whether a `Tail` landing starts pinned to the end. Read once, like `anchor`. */
  pinToEnd: boolean;
  /** The user's Pause. While set, no lines arrive. */
  paused?: boolean;
  onError?: (error: unknown) => void;
  estimateRowHeight: (line: LogLine) => number;
  measureElement?: (element: Element, entry: ResizeObserverEntry | undefined, instance: Virtualizer<HTMLDivElement, Element>) => number;
  overscan?: number;        // 3
  firstPageSize?: number;   // 150
  pageSize?: number;        // 100
  /** Pages toward an edge once at most this many rows lie between the visible range and it. */
  loadMoreThreshold?: number; // 50
  /** How far from the bottom, in pixels, still counts as reaching it. */
  pinTolerance?: number;    // 10
  /** The height of an edge row while that edge has more; the core lays rows out below it. */
  edgeRowHeight?: number;   // 0
  className?: string;
  children: (rows: CoreRows) => ReactNode;
};

export type CoreHandle = {
  /** Lands again at `anchor`. A jump to `Tail` pins to the end; any other unpins. */
  jumpTo: (anchor: Anchor) => void;
  /** Re-measures every row, keeping the reader's place, or the end while following. */
  measure: () => void;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => CoreState;
};

export type CoreRow = { key: number; index: number; size: number; start: number; line: LogLine };

export type CoreRows = {
  loading: boolean;
  failed: boolean;
  before: EdgeState;
  after: EdgeState;
  edgeRowHeight: number;
  /** The rows and the edge rows together, for the sizer. */
  totalSize: number;
  rows: CoreRow[];
  count: number;
  lineAt: (key: number) => LogLine | undefined;
  keyAt: (index: number) => number | undefined;
  /** -1 for a key not held. */
  indexOfKey: (key: number) => number;
  measureElement: (node: Element | null) => void;
  /** Reads the next page toward `edge` now, for an edge row's button: with no row drawn after an empty page, there is no scroll to read on at. */
  loadMore: (edge: Edge) => void;
};
```

`subscribe` and `getSnapshot` serve one store that outlives landings: `LogViewerCore` holds it
and each window's listener copies the window's snapshot into it.

### 2. One landing

`LogViewerCore` keys a `Landing` component on a landing id, bumped by `jumpTo` and by a new
`client`, so a jump or a new client starts over with a fresh window and a fresh scroller, and
nothing of the last landing carries across. `Landing` holds the window in a ref, made once with
`{ pinToEnd, firstPageSize, pageSize }`, and re-renders off the window's snapshot through
`useSyncExternalStore`: the snapshot carries `length` and the edges, so every line a page or a
follow adds renders.

On mount, once (refs, not the effect's own variables, since StrictMode runs the effect twice):
`client.fetch(window.landing(anchor))`, then `window.landed(page, anchor)` or
`window.landingFailed()` and `onError`. A landing a jump replaced while it was in flight
reports nothing. Loading ends a frame after the landing settled, through `onNextFrame` and
`window.loadingEnded()`:
`requestAnimationFrame`, or `HIDDEN_FLUSH_MS` (250 ms) when no frame comes, since a minimized
window gets none.

### 3. The virtualizer

`useVirtualizer` over `window.length`, with `getItemKey` answering the window's keys, so a row
keeps its key as pages land above it; `estimateSize` off `estimateRowHeight`; `scrollMargin`
the edge row's height while the before edge has more; `overscan`; `useScrollendEvent`. The
visible range without overscan is `virtualizer.calculateRange()`, read each render and handed
to `window.setVisibleRange`.

The virtualizer mutates in place, so the component carries the compiler's
`incompatible-library` disable on that one call, as the repo's other virtualized component
does.

### 4. Paging

An effect on the visible range: when `range.startIndex <= loadMoreThreshold`, `loadMore('before')`;
when `range.endIndex >= count - 1 - loadMoreThreshold`, `loadMore('after')`. `loadMore(edge)` asks
`window.startPage(edge)`; with a ticket, it fetches `window.query(edge)` and calls
`window.paged(ticket, page)` or `window.pageFailed(ticket)` and `onError`. A page that landed holds
its edge from being asked again until the next frame, through `onNextFrame`, so the range the
page moves does not ask twice. Paging runs when the range moves, never because an edge
changed, with two exceptions. When `window.reachedBottom()` answers true, the core pages after
at once, since a scroller already at the bottom moves no range (§6). After a page lands, while
`window.readOn(edge)`, the core pages
that way again on the next frame, since a page of repeats moved the edge and not the range. A
page with no line at all leaves the range where it was, and reading on waits for the user's
next scroll or the render function's `loadMore`.

### 5. The follow

An effect on `window.following()`: while true, `window.followOpened()`, then
`client.follow(window.followFrom(), onFrame)`. Frames are batched and handed to
`window.followed(batch)` once per animation frame, or after `HIDDEN_FLUSH_MS`, through
`onNextFrame`. The cleanup unsubscribes and drops a batch still waiting; a batch that flushes
between the window closing the follow and the cleanup running is ignored by the window. A follow the client
refuses to open goes to `window.followFailed()`, which unpins, then to `onError`.

### 6. The user's intent

The scroll event says where the scroller is, never who moved it. The adapter tells the user's
scrolls from its own by the input events that precede them, and reads a scroll event as the
user's only within `INTENT_MS` (150 ms) of one of these on the scroller:

- `wheel`: `deltaY < 0` is up, `> 0` is down. Momentum scrolling keeps sending wheel events, so
  a fling stays the user's.
- `keydown`: `ArrowUp`, `PageUp`, `Home` are up; `ArrowDown`, `PageDown`, `End`, `Space` are
  down. The scroller is `tabIndex={0}` so it receives them.
- `pointerdown` on the scroller where `clientX` lies in the vertical scrollbar's gutter, that
  is past `scroller.clientWidth` from its left border: a scrollbar drag starts, and every scroll
  event until `pointerup` on `window` is the user's, its direction read off the scroll
  position's change.
- `touchstart` and `touchmove`: a touch drag, every scroll event until `touchend` the user's.

On a scroll event that is the user's: `window.scrolledUp()` when the position moved up by any
amount, and `window.reachedBottom()` when it moved down and `scrollTop + clientHeight >=
scrollHeight - pinTolerance`. `End` and `Home` keys call `reachedBottom` and `scrolledUp`
directly, since the scroll they cause may be clamped to no move. For the same reason, a downward
`wheel` or key while the scroller is already within `pinTolerance` of the bottom calls
`reachedBottom` directly: a scroller at its bottom sends no scroll event for it, and without
this a window that landed at the end unpinned could be pinned only with `End`. A scroll event with no intent
behind it is the core's or the browser's — keep-place, the end kept in view, a clamp as content
shrank — and changes nothing.

Only the pin is read off intent; the virtualizer reads every scroll event as it does today.

### 7. Effects, and keeping the reader's place

A layout effect after every commit performs the effect the last window event answered, then
clears it:

- `hold`: as soon as `paged` answers it, before the commit draws the new lines, the adapter
  notes the first visible row's key and how far into it the scroll sat
  (`virtualizer.getVirtualItemForOffset`), off the layout still on screen; keys are stable across
  a prepend, so that key names the same row after it. After the commit it sets `scrollTop` to
  that row's new `start` plus the offset. Only the first hold before a commit counts, since a
  second is read against lines the screen does not show yet.
- `toEnd`: `scrollTop = scrollHeight`.
- `toKey(key)`: `scrollTop` to that row's `start`.

While following, every commit sets `scrollTop = scrollHeight`, whatever the effect, since lines
arriving keep the end in view. A `ResizeObserver` on the scroller does the same while following,
so a scrollbar appearing or the panel resizing does not hide the last row.

`measure()` on the handle holds the place, then `virtualizer.measure()`.

### 8. The rows

The render function is handed `CoreRows` built each render: the virtual items mapped to
`CoreRow`s with the window's line at each index, `totalSize` off the virtualizer plus the edge
rows, `before` and `after` off `window.edges()`, and `loadMore`.

### 9. Errors and the library's shape

Errors go to `onError`: a landing's, a page's, a follow's. The core keeps what it has. The
library imports `react`, `@tanstack/react-virtual` and its own files, and nothing else; `index.ts`
exports `LogViewerCore` and the types above beside step 1B's.

## Decisions this step asks for

- **Intent from input events, not from attributing scroll events.** A scroll event cannot say
  who moved the scroller, and telling the core's scrolls from the user's after the fact needs
  bookkeeping around every scroll the core makes. Input events say who moved it before it
  moves. The cost is four listeners and a scrollbar gutter test; the hard case is a scrollbar
  drag, which the gutter test covers on every platform that draws one, while an overlay
  scrollbar is still dragged with the pointer down over the scroller, which the same listener
  sees.
- **The window performs nothing; the adapter decides nothing.** Every event the adapter sends is
  a fact about the DOM or the client; every answer it performs is a fact about lines.
- **One window per landing.** A jump is a new window, a new scroller, a new virtualizer: nothing
  to reset, nothing to leak across.
- **Loading ends a frame late.** So the render function can draw the landing before the state
  says it is done, and a test can see both.

## Tasks

`@tanstack/react-virtual` is added to `package.json`. Every file is kstack's alone.

1. **The scroller and the landing.** `src/lib/log-viewer/core.tsx`: `LogViewerCore`, `Landing`,
   the store, the virtualizer, the landing, loading (§1–§3). **Planned.**
2. **Paging and the follow.** §4, §5, `onNextFrame`. **Planned.**
3. **Intent.** §6, as a hook `useIntent(scrollElRef, window, pinTolerance)` in `core.tsx`.
   **Planned.**
4. **Effects.** §7, as a hook `useEffects(scrollElRef, virtualizer, window)` in `core.tsx`.
   **Planned.**
5. **The index and the README.** `index.ts`, `README.md`. **Planned.**
6. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Planned.**

## Tests

`core.test.tsx`, over a jsdom layout stub of its own: the scroller is a fixed height, every row
a fixed height, `scrollHeight` the sizer the render function draws, `scrollTop` clamped as a
browser's and firing `scroll`, and content shrinking under the scroll clamping it a task later.
A scripted `Client` answers `Page`s by hand; the `FakeClient` serves the longer cases. Fake
timers throughout.

- Each landing kind lands with the right rows at the right keys and the scroller where §7's
  effect puts it; a jump replacing a landing in flight reports nothing; loading ends on a
  timer when no frame comes.
- A page of repeats that says more is followed by another on the next frame, with no scroll;
  a page before and after as the range nears each edge; the first visible row stays where it
  was after a page before, and after a re-measure, and when both land in one commit; an empty
  page with a cursor leaves the edge row and pages again only on the next scroll or `loadMore`;
  a failed page goes to `onError` once and keeps the rows.
- The follow opens exactly while the window follows, at `followFrom()`; a follow the client
  refuses goes to `followFailed` and `onError` once, and the next scroll to the bottom opens it
  again; frames flush per frame
  and on the hidden timer; a batch still waiting is dropped when the follow closes; the end
  stays in view as lines arrive and when the scroller resizes.
- Intent: a wheel up unpins and closes the follow; a wheel up between two batches; a wheel up
  whose scroll event lands with a batch's; a keyboard `ArrowUp`; `Home`; a scrollbar drag up; a
  touch drag up; a wheel down reaching the bottom of a window behind asks for a page and pins
  when it finds no more; a wheel down on a scroller already at the bottom does the same; `End`; a core scroll with no input behind it changes nothing, and so
  does the clamp when an edge row goes.
- `measure` keeps the row, or the end while following; `jumpTo` to each kind; a new client
  lands where the last jump did.

## Security

**Widens:** nothing. The core draws nothing and talks to nothing. One dependency is added,
`@tanstack/react-virtual`, which computes positions and touches no HTML sink; the
`cluster-data-is-text` lint runs over the files like any other.

## When it lands

- Root `CLAUDE.md`, *Frontend*: the library's paragraph gains the core — the landing, paging,
  the follow, intent, effects — in a sentence each.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `make test-changed` while working; `pnpm build`, `make test-js`, `make lint-js`.

Nothing to see by hand. The tests are the whole check.
