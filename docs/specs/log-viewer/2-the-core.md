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
  /** Where the core lands on mount. Read once: a new landing is `jumpTo`, `retry`, or a new mount. A new `client` lands again as the last landing did. */
  anchor: Anchor;
  /** Whether the mount's `Tail` landing starts pinned to the end. Read once, like `anchor`. */
  pinToEnd: boolean;
  /** The user's Pause. While set, no lines arrive, across landings. */
  paused?: boolean;
  /** Where the core publishes its state. The core makes its own when none is given. */
  store?: CoreStore;
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
  /** Lands again as the mount did: at the `anchor` prop, pinned as `pinToEnd` says. */
  retry: () => void;
  /** Re-measures every row, keeping the reader's place, or the end while following. */
  measure: () => void;
};

/** The core's published state, in `useSyncExternalStore`'s shape. It outlives landings. */
export type CoreStore = {
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => CoreState;
  /** The core's alone. */
  set: (state: CoreState) => void;
};

export function createCoreStore(): CoreStore;

/** `start` is from the top of the sizer, the before edge row included. */
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
  lineOfKey: (key: number) => LogLine | undefined;
  keyAt: (index: number) => number | undefined;
  /** -1 for a key not held. */
  indexOfKey: (key: number) => number;
  measureElement: (node: Element | null) => void;
  /** Reads the next page toward `edge` now, for an edge row's button: with no row drawn after an empty page, there is no scroll to read on at. */
  loadMore: (edge: Edge) => void;
};
```

**The store is how a component above the core reads its state in render.** It makes one with
`createCoreStore()`, held in `useState`, passes it as `store`, and reads it with
`useSyncExternalStore`. The handle sits in a ref, which render may not read, so it carries
events alone. Each `Landing` sets the store to its window's snapshot in its first layout
effect, and again on every change its window emits. A layout effect runs before paint, and the
parent's `useSyncExternalStore` re-renders before paint too, so no frame shows `loading` false
or the last landing's pin under a new landing. Render never writes the store.

**The core renders the scroller**: a `div` with `className`, `tabIndex={0}`, and inline
`overflow: auto` and `overflow-anchor: none`. §7 keeps the reader's place itself, and Chromium's
scroll anchoring (Windows) would stack on it. The render function's output goes inside the
scroller and draws the sizer, `totalSize` tall, each row at its `start`, and the edge rows.

### 2. One landing

`LogViewerCore` keys a `Landing` component on a landing id, bumped by `jumpTo`, by `retry` and
by a new `client`, so each starts over with a fresh window and a fresh scroller, and nothing of
the last landing carries across. A new `client` bumps the id during render: `LogViewerCore`
keeps the last client in `useState` and compares, React's pattern for adjusting state to a
prop, so the old `Landing` never renders with a client whose cursors it does not hold.
`Landing` makes its window once, in `useState(() => new
LogWindow(...))`, since render reads it, with `{ pinToEnd, firstPageSize, pageSize }`.
`pinToEnd` is the prop for the mount's landing and `retry`'s, `anchor.kind === 'Tail'` for a
jump's, and the last landing's for a new client's. `Landing` re-renders off the window's
snapshot through `useSyncExternalStore`: the snapshot carries `length` and the edges, so every
line a page or a follow adds renders.

Below, `window` is the landing's `LogWindow`. In code it is `logWindow`, since §5 and §6 need
the global `window`.

On mount, once (refs, not the effect's own variables, since StrictMode runs the effect twice):
`window.setPaused(paused)`, then `client.fetch(window.landing(anchor))`, then
`window.landed(page, anchor)` or
`window.landingFailed()` and `onError`. A landing a jump replaced while it was in flight
reports nothing. Loading ends a frame after the landing settled, through `onNextFrame` and
`window.loadingEnded()`:
`requestAnimationFrame`, or `HIDDEN_FLUSH_MS` (250 ms) when no frame comes, since a minimized
window gets none.

An effect on `paused` calls `window.setPaused(paused)` when it changes. Pause closes an open
follow through §5's effect, and playing opens it again if the window is pinned.

### 3. The virtualizer

`useVirtualizer` over `window.length`, with `getItemKey` answering the window's keys, so a row
keeps its key as pages land above it; `estimateSize` off `estimateRowHeight`; `scrollMargin`
the edge row's height while the before edge has more; `overscan`; `useFlushSync: false`.
`getItemKey` is a new function whenever `length` changes, since the virtualizer caches its
measurements on the function's identity and the count, and keys shift on every prepend.

`useFlushSync: false` makes the virtualizer re-render as an ordinary update rather than a
synchronous commit inside the scroll event. So every scroll listener has run before the commit
that answers it, and §7's keep-the-end cannot undo a scroll the intent listener has not read
yet. React 19 also warns on the `flushSync` the virtualizer would otherwise call while a row
is measured during a commit. The option is in `@tanstack/react-virtual` 3.14 and later. The
visible range without overscan is `virtualizer.calculateRange()`, read each render. A layout
effect after every commit hands it to `window.setVisibleRange`, never render itself, since the
window emits and its subscribers re-render. An unchanged range emits nothing, so it settles.

`totalSize` is `virtualizer.getTotalSize()`, which leaves `scrollMargin` out, plus the height
of each edge row drawn. A virtual item's `start` counts `scrollMargin` in, so it is already
measured from the top of the sizer.

The virtualizer mutates in place, so the component carries the compiler's
`incompatible-library` disable on that one call. It is the repo's first use of
`@tanstack/react-virtual`: `VirtualTable` is hand-rolled.

### 4. Paging

**Paging driven by the range waits while the range is not the reader's**: until loading has
ended, and while a re-measure is in flight (§7). A landing's first commit draws its rows with
the scroller still at the top, so a `Tail` landing reads a range at index 0 until its `toEnd`
lands, and a re-measure resets every row to its estimate before the hold puts the reader back.
Loading ends a frame after the landing settled (§2), after that answer's commit, which is why
it ends late.

An effect on the visible range: when `range.startIndex <= loadMoreThreshold`, `loadMore('before')`;
when `range.endIndex >= count - 1 - loadMoreThreshold`, `loadMore('after')`. `loadMore(edge)` asks
`window.startPage(edge)`; with a ticket, it fetches `window.query(edge)` and calls
`window.paged(ticket, page)` or `window.pageFailed(ticket)` and `onError`. A page that landed holds
its edge from paging driven by the range until the next frame, through `onNextFrame`, so the
range the page moves does not ask twice. Paging runs when the range moves, never because an
edge changed, with two exceptions, neither held by that frame. When `window.reachedBottom()`
answers true, the core pages after at once, since a scroller already at the bottom moves no
range (§6). After a page lands, while `window.readOn(edge)`, the core pages that way again on
the next frame, since a page of repeats moved the edge and not the range. A
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
- `keydown`: `ArrowUp`, `PageUp`, `Home`, `Shift+Space` are up; `ArrowDown`, `PageDown`,
  `End`, `Space` are down. The scroller is `tabIndex={0}` so it receives them.
- `pointerdown` anywhere on the scroller: every scroll event until `pointerup`,
  `pointercancel` or `blur` on the global `window` is the user's, its direction read off the
  scroll position's change. `pointercancel` and `blur` end it too, since an engine can drop the
  `pointerup` after a scrollbar drag or a release outside the window. This covers a scrollbar
  drag whether the scrollbar is classic or overlay. An overlay scrollbar (macOS, and GTK's
  default on Linux) takes no width, so no gutter test could find it. It also covers a selection
  drag that scrolls, which is the user's too.
- `touchstart` and `touchmove`: a touch drag, every scroll event until `touchend` or
  `touchcancel` the user's.

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

The adapter keeps each answer other than `none` in React state, so the answer brings a commit of
its own, and a layout effect after that commit performs it, then clears it. An answer must
never wait for an unrelated commit: a page of repeats answers `hold` and changes no snapshot, and
a hold performed on the user's next scroll would pull the reader back to where it was noted.

- `hold`: as soon as `paged` answers it, before the commit draws the new lines, the adapter
  notes the first visible row's key and how far into it the scroll sat
  (`virtualizer.getVirtualItemForOffset`), off the layout still on screen, and keeps both with
  the answer; keys are stable across a prepend, so that key names the same row after it. After
  the commit it sets `scrollTop` to that row's new `start` plus the offset. Only the first hold
  before a commit counts, since a second is read against lines the screen does not show yet.
- `toEnd`: `scrollTop = scrollHeight`.
- `toKey(key)`: `virtualizer.scrollToIndex(index, { align: 'start' })`, which corrects itself
  over the next frames as the rows around it are measured; a `scrollTop` set once would land
  off by every estimate above it that turned out wrong.

While following, every commit sets `scrollTop = scrollHeight`, whatever the effect, since lines
arriving keep the end in view. A `ResizeObserver` on the scroller does the same while following,
so a scrollbar appearing or the panel resizing does not hide the last row.

`measure()` on the handle holds the place, then `virtualizer.measure()`. It counts as in flight
from the call until a frame after the hold is performed, and §4 pages nothing by the range
meanwhile. A second call while one is in flight does nothing.

### 8. The rows

The render function is handed `CoreRows` built each render: the virtual items mapped to
`CoreRow`s with the window's line at each index, `totalSize` off the virtualizer plus the edge
rows, `before` and `after` off `window.edges()`, and `loadMore`.

### 9. Errors and the library's shape

Errors go to `onError`: a landing's, a page's, a follow's. The core keeps what it has. A
page's error is reported once per run of failures, and again only after a page succeeds:
momentum at the bottom calls `reachedBottom` many times a second, and against a client that
keeps failing each call would be a report. The
library imports `react`, `@tanstack/react-virtual` and its own files, and nothing else; `index.ts`
exports `LogViewerCore`, `createCoreStore` and the types above beside step 1B's.

## Decisions this step asks for

- **Intent from input events, not from attributing scroll events.** A scroll event cannot say
  who moved the scroller, and telling the core's scrolls from the user's after the fact needs
  bookkeeping around every scroll the core makes. Input events say who moved it before it
  moves. The cost is four listeners. The hard case is a scrollbar drag, which a pointer held
  down on the scroller covers whether the scrollbar is classic or overlay. The residual risk: a
  scroll with no intent behind it that lands within `INTENT_MS` of an input reads as the user's.
  Examples are the clamp when an edge row goes, or the virtualizer correcting a row's size above
  the view. Only one that moves up changes anything: it unpins, and a scroll to the bottom pins
  again.
- **Answers are state.** Every answer brings its own commit, so none is performed on a commit
  some other cause made.
- **The parent holds the store.** Render can read a store held in state, and cannot read a
  handle held in a ref, so the published state rides the store and the handle carries events.
- **`retry` lands as the mount did.** A failed landing tried again keeps the view's own
  `pinToEnd`, where a jump to `Tail` always pins.
- **The window performs nothing; the adapter decides nothing.** Every event the adapter sends is
  a fact about the DOM or the client; every answer it performs is a fact about lines.
- **One window per landing.** A jump is a new window, a new scroller, a new virtualizer: nothing
  to reset, nothing to leak across.
- **Loading ends a frame late.** So the render function can draw the landing before the state
  says it is done, and a test can see both.

## Tasks

`@tanstack/react-virtual` is added to `package.json`, at 3.14 or later for `useFlushSync`. Every
file is kstack's alone: kubetail's viewer was read for its virtualizer options, its paging
gate and its landing at a row, and none of its code is carried.

1. **The scroller and the landing.** `src/lib/log-viewer/core.tsx`: `LogViewerCore`, `Landing`,
   `createCoreStore`, the scroller, the virtualizer, the landing, the pause, loading (§1–§3).
   **Planned.**
2. **Paging and the follow.** §4, §5, `onNextFrame`. **Planned.**
3. **Intent.** §6, as a hook `useIntent(scrollElRef, logWindow, pinTolerance)` in `core.tsx`.
   **Planned.**
4. **Effects.** §7, as a hook `useEffects(scrollElRef, virtualizer, logWindow)` in `core.tsx`.
   **Planned.**
5. **The index and the README.** `index.ts`, `README.md`. **Planned.**
6. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Planned.**

## Tests

`core.test.tsx`, over a jsdom layout stub of its own: the scroller is a fixed height, every row
a fixed height, `scrollHeight` the sizer the render function draws, `scrollTop` clamped as a
browser's and firing `scroll`, and content shrinking under the scroll clamping it a task later.
jsdom has no `ResizeObserver` and measures every element at zero, and the virtualizer draws no
row without both, so the stub supplies a `ResizeObserver` the test can fire and gives the
scroller its size through `getBoundingClientRect`.
A scripted `Client` answers `Page`s by hand; the `FakeClient` serves the longer cases. Fake
timers throughout.

- Each landing kind lands with the right rows at the right keys and the scroller where §7's
  effect puts it; a jump replacing a landing in flight reports nothing; loading ends on a
  timer when no frame comes.
- The store: a parent reading it in render sees each change; the first paint after a jump, a
  `retry` or a new client shows `loading` and none of the last landing's `pinnedToEnd` or
  `following`; a new client's first fetch goes to the new client, and the old one is asked
  nothing more.
- Pause closes the follow and pages nothing after; a jump while paused lands paused and opens no
  follow; playing a pinned window opens the follow again.
- A `Tail` landing whose before edge has more asks for no page before until the user scrolls
  near it; `measure` pages nothing while it is in flight, and a second call does nothing;
  `toKey` lands its row at the top after the rows above it measured taller than estimated.
- A page of repeats that says more is followed by another on the next frame, with no scroll;
  a page before and after as the range nears each edge; the first visible row stays where it
  was after a page before, and after a re-measure, and when both land in one commit; an empty
  page with a cursor leaves the edge row and pages again only on the next scroll or `loadMore`;
  `End` in the frame after a page after landed still pages;
  a failed page goes to `onError` once and keeps the rows; repeated failures at the bottom
  report once, and a failure after a success reports again; a page of repeats before, then the
  user's scroll, leaves the scroller where the user put it.
- The follow opens exactly while the window follows, at `followFrom()`; a follow the client
  refuses goes to `followFailed` and `onError` once, and the next scroll to the bottom opens it
  again; frames flush per frame
  and on the hidden timer; a batch still waiting is dropped when the follow closes; the end
  stays in view as lines arrive and when the scroller resizes.
- Intent: a wheel up unpins and closes the follow; a wheel up while following moves the
  scroller up and stays there, with the real virtualizer mounted and the core rendered under
  `StrictMode`; a wheel up between two batches; a wheel up
  whose scroll event lands with a batch's; a keyboard `ArrowUp`; `Shift+Space`; `Home`; a
  pointer drag up, with the scroller's `clientWidth` its full width as an overlay scrollbar
  leaves it; pointer intent ending on `pointercancel` and on `blur` with no `pointerup`, after
  which a core scroll up changes nothing; a touch drag up; a wheel down reaching the bottom of a window behind asks for a page and pins
  when it finds no more; a wheel down on a scroller already at the bottom does the same; `End`; a core scroll with no input behind it changes nothing, and so
  does the clamp when an edge row goes.
- `measure` keeps the row, or the end while following; `jumpTo` to each kind, a jump to `Tail`
  pinning whatever `pinToEnd` said; `retry` lands at the mount's anchor, pinned as `pinToEnd`
  says; a new client lands where the last jump did.

## Security

**Widens:** nothing. The core draws nothing and talks to nothing. One dependency is added,
`@tanstack/react-virtual`, which computes positions and touches no HTML sink; the
`cluster-data-is-text` lint runs over the files like any other.

## When it lands

- Root `CLAUDE.md`, *Frontend*: the library's paragraph gains the core — the landing, paging,
  the follow, intent, effects, the store — in a sentence each. Its *The directory imports its
  own files and nothing else* becomes *imports `react`, `@tanstack/react-virtual` and its own
  files, and nothing else*.
- The library's `README.md`: *The boundary* says the same, and the README gains the core.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `make test-changed` while working; `pnpm build`, `make test-js`, `make lint-js`.

Nothing to see by hand. The tests are the whole check.
