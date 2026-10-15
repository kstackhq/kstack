---
title: The headless core
scope: frontend
status: In progress
---

# The headless core

**Needs:** `wip/log-views` as it stands after rung 1. **Unblocks:** step 2. Can be built beside
step 1A.

## In short

Kubetail's headless log viewer is copied into `src/lib/log-viewer/` with its tests, and reshaped
to kstack's contracts. It draws nothing: it keeps the lines, lands at the start, the end or a
moment, loads more at either edge as the user scrolls, follows new lines, pins to the end or not,
and hands its rows to a render function. A fake client comes with it, so every behaviour is
tested without a sidecar.

After this step the core works against the fake and nothing on screen uses it yet.

## What is not in this step

- **No rows on screen.** Step 2 draws them.
- **No client over the sidecar.** Step 2 writes it against step 1A.
- **No columns, wrap, selection or copy.** Steps 3A and 3B.
- **No pull-to-refresh.** Kubetail refreshes the end when follow is off and the user scrolls
  down past it. Here an unpinned or paused viewer has `hasMoreAfter` set, so scrolling to the
  bottom pages the same way it pages anywhere else, and the feature has no case.

## Design

### 1. What is copied

From kubetail's `dashboard-ui/src/components/widgets/log-viewer/`:

| Kubetail | Kstack | What changes |
| --- | --- | --- |
| `types.ts` | `src/lib/log-viewer/types.ts` | `LogRecord` becomes `LogLine` (§2) |
| `double-tailed-array.ts` + test | `src/lib/log-viewer/double-tailed-array.ts` | Rewritten smaller: `first`/`last` answer `undefined` when empty, an index not held throws `RangeError`, and the unused `set`, `forEach` and `map` go |
| `before-paint.ts` + test | `src/lib/log-viewer/before-paint.ts` | `trigger` typed `unknown`; the tests rewritten, with an async and a throwing callback |
| `fake-client.ts` + test | `src/lib/log-viewer/fake-client.ts` | Rewritten over `LogLine`, a third the size: `lines`, `rate` and `delayMs` options, `lineAt`, no lorem generator; `fetchSince` keeps the newest line, and a cancelled subscription delivers nothing |
| `log-viewer.tsx` + test | `src/lib/log-viewer/core.tsx` | §3; the lines it holds are `lines.ts`, a class of kstack's own |
| `index.ts` | `src/lib/log-viewer/index.ts` | Exports the above, but not the fake |

`@tanstack/react-virtual` is added to `package.json`. It is the one dependency the core has.

"Copied" understates the work on `core.tsx` and its test: kstack's lint bans `for…of` and
`for…in` (airbnb's four, restated in `custom/cluster-data-is-text`), and the React compiler's
rules refuse refs mutated through a shared object, so both files are rewritten line by line
rather than pasted. The behaviour is kubetail's; the text will not be.

### 2. The line

`LogLine` is defined in the library's `types.ts` as the wire's `LogRecord` from step 1A, its
`cursor` included:

```ts
export type LogLine = {
  /** RFC 3339, as the sidecar hands it back. */
  timestamp: string;
  /** What the client sends back to ask for lines before or after this one. Opaque. */
  cursor: string;
  /** Text, with terminal control sequences stripped by the sidecar. */
  message: string;
  /** The log the line came from. Its four fields together name it. */
  stream: {
    namespace: string;
    pod: string;
    container: string;
    previous: boolean;
  };
  /** Where the stream's pod runs, empty where the mirror does not know. It takes no part in naming the stream. */
  place: {
    node: string;
    region: string;
    zone: string;
    os: string;
    arch: string;
  };
};
```

The viewport (`src/lib/viewport.ts`, the app's) imports it from the library, so the dependency
runs from the app into the library and never back. The `Client` type
keeps kubetail's five methods over it, with `subscribe(after, onLine)` in place of kubetail's
options object; `FetchResult` answers `lines`, and gains `skipped: string[]`, the page's lines
about streams it could not read, which the core publishes with its state and step 2 draws.

### 3. The core

`LogViewerCore` in `core.tsx`, from kubetail's `LogViewer`. Same shape: a component with a render
function as its child, given a `Client` and a handful of settings, handing back a `Virtualizer`
with the rows to draw. These change:

- **It opens at an anchor.** Kubetail's `initialPosition` — `head`, `tail` or a `cursor` — becomes
  an `anchor` prop of the view's own shape:

  ```ts
  export type Anchor = { kind: 'Head' | 'Tail' | 'At'; at: string | null };
  ```

  The same fields as the wire's `LogsViewAnchor`, so the panel's `openAt` goes straight through.
  An `At` anchor is resolved into a cursor once, at landing: `fetchBefore` and `fetchSince` at
  `at`, as kubetail's cursor landing does. That is the one place an anchor becomes a cursor.
- **`pinToEnd` is a prop, and "auto-scroll" is renamed to say so.** Kubetail turns auto-scroll
  on when the view opens at the tail and off otherwise. Here the prop sets it on open whatever
  the anchor, and the user's hand changes it after: scrolling up unpins, and a scroll the user
  makes that reaches the bottom pins again — only a user's scroll, and only once the loaded
  window is at the end (the last page after answered no `nextCursor`), since a `Tail` landing
  opens at the bottom and must not pin a view that said `pinToEnd: false`. Kubetail's auto-scroll is this one idea and nothing else — the flag is read only
  when lines arrive and when the scroller resizes, to keep the end in view — so the word goes
  from the code, not just the props: `isAutoScrollEnabled` becomes `pinnedToEnd`, `useAutoScroll`
  becomes `usePinToEnd`, `pinToBottomTolerance` becomes `pinTolerance`. "Follow" stays for the
  other idea, lines arriving: `useFollowFromEnd` becomes `useFollow`. A reader should find
  *follow* where lines arrive and *pin* where the viewport moves, and *auto-scroll* nowhere.
- **The state it publishes grows.** Kubetail's handle publishes `{ isLoading }` through
  `subscribe`/`getSnapshot`. Here it publishes:

  ```ts
  export type CoreState = {
    loading: boolean;
    /** Whether the viewport keeps the end in view. The user's hand changes it. */
    pinnedToEnd: boolean;
    /** Whether the subscription is open: pinned and not paused. */
    following: boolean;
    /** Whether the newest line is on screen: loaded to the end, with nothing more after. False the moment the viewer unpins. */
    atEnd: boolean;
    /** Timestamps of the first and last visible lines; null before any is drawn. */
    range: { from: string; to: string } | null;
    /** How many lines are on screen. */
    visible: number;
  };
  ```

  `range` and `visible` come off the virtualizer's range each time it changes; `pinnedToEnd` off
  the pin flag, which moves from a ref to state; `atEnd` off the scroll position and
  `hasMoreAfter`.
- **Lines arrive only while the viewer is following.** `paused` is a prop, kubetail's `follow`
  turned around: the user's Pause, `false` by default, so the common case omits it. The
  subscription is open while `following = pinnedToEnd && !paused` and closed otherwise: unpinning, or pausing, unsubscribes and sets `hasMoreAfter`, so the
  viewer is in the paged state it is in when anchored mid-stream, and scrolling to the bottom
  runs `loadMoreAfter` as it does there. Re-pinning while not paused, or un-pausing while
  pinned, subscribes `after` the newest cursor the core holds **less `REOPEN_OVERLAP`, five
  seconds**: lines arrive per container, so a container a moment behind has lines older than
  the single newest cursor, and a subscription opened exactly there would lose them with no
  page to find them in. The overlap re-sends a few seconds the core already holds, and the
  duplicate rule below drops them; a follow's first open overlaps the same way, since the rule
  makes it free and one rule is simpler than two. The first `loadMoreAfter` after an unpin has the same gap
  and the same fix: it asks `after` the newest cursor less `REOPEN_OVERLAP`, and the lines it
  brings back go through the duplicate rule, since that page is the follow's catch-up and not
  a page of history. Nothing is appended while the user is not at the end, which is
  what a bounded buffer needs: a line appended behind a reader's back would evict the lines
  they are reading. `useFollow` owns the subscription and reads `following`; `usePinToEnd` owns
  the flag.
- **An empty page with a cursor is not a loop.** A fetch can answer no lines and a `nextCursor`
  (step 1A: the budget ran out before a line matched). The core keeps `hasMoreBefore` or
  `hasMoreAfter` set and fetches again only on the user's next scroll toward that edge, never
  by itself, so a rare grep reads the cluster in steps the user takes. The edge row says so
  (step 2).
- **The handle is `jumpTo(anchor)` and `measure`.** Kubetail's three jumps become one that takes
  an anchor, so a toolbar's End and a later *View around this* call the same thing. The ref is a
  prop, since React 19 has no need for `forwardRef`.
- **A repeated line is dropped by its own stream's newest cursor.** A follow's lines arrive in
  each container's own order, not in time order across containers (step 1A), so a line cannot
  be dropped for being older than the last line seen from anyone: an earlier line from one
  container landing after a later line from another is real. What repeats is a whole stretch —
  `subscribe-exchange` reconnects with the variables it was given, so after a dropped connection
  the follow replays from the cursor it first opened with, and the reopen overlap above replays
  a few seconds on purpose. So the core keeps the newest cursor it holds **per stream** — keyed
  on the line's `stream`, never its `place`, which moves when a node's labels do — and how many
  lines with each message are held at it, and drops a follow line before its own
  stream's newest, or at that moment while the read has not passed as many lines with its
  message as are held there. A read is a page, or a follow across its batches; a stream going
  back in time within a follow is its reconnect's replay, and the count starts over. That is
  exact, because each container's lines arrive in order, but for a replay whose first line of a
  stream falls at that stream's newest moment, which reads as lines written twice. **The rule applies to lines from the follow,
  and to the catch-up fetch after an unpin, and to nothing else**: a page of history from
  `loadMoreBefore` is older than every stream's newest by definition, and is placed by its
  cursors as kubetail places it, never filtered. Kubetail's `lastTSMap` keeps the same
  per-stream map for its handoff; here it is kept for the whole follow.
- **Errors go to an `onError` prop.** Kubetail's `console.error` calls become a call to it, and
  the core keeps what it has. The library does not know the error bus; step 2 wires the prop to
  `reportError`.
- **The library imports nothing from the app.** Nothing under `src/lib/log-viewer/` imports
  from `@/`, from a relative path that leaves the directory, or from a package of the app's: it
  knows lines, cursors, anchors and a scroll element, and nothing about columns, cells, the
  viewport, GraphQL or the error bus. The boundary is held by convention, since the library may
  still change shape before it is split out; the library's `README.md` states the contract and
  the boundary for a reader who opens the directory cold.
- **The loading row heights and batch sizes** stay settings with kubetail's defaults. Step 2
  sets what the sidebar wants.
- **Lint.** The file passes kstack's rules as they are: the `eslint-disable` lines for the React
  compiler go, and what they covered is rewritten — refs mutated through the `Runtime` object
  become refs each hook owns.

### 4. The fake client

`FakeClient` from kubetail, over `LogLine`. It makes a fixed number of deterministic lines, pages
over them, and can append one every so often. Its delays are `setTimeout`s, so a test drives them
with fake timers.

It is a test fixture, not part of the library: kubetail exports it for a developer switch, and
kstack does not. Nothing but a `*.test.tsx` imports it, so it never reaches the bundle, and
`index.ts` leaves it out. The core's copied tests are written against its knobs, which is why
it is copied rather than written smaller; steps 3A and 3B mount the viewer over it too.

## Decisions this step asks for

- **Copy into `src/lib/`, not a package.** The core is ours to reshape, and a package would hold
  the two projects to one API they do not share. Every file in §1's table opens with both
  copyright lines, per the README's *Attribution*.
- **The core speaks anchors; only the client speaks cursors.** An anchor is where the viewer is
  told to be, a cursor is how the client asks for more. Kubetail blurs the two in
  `initialPosition`; keeping them apart means the viewer's public surface never names a cursor.
- **Pinning is state the core publishes.** The viewport needs it, and the viewer's header
  will show it. A ref cannot be read by either.
- **Drop a repeated line by its stream's newest cursor.** Simpler than teaching the exchange a
  cursor, and the only rule a per-container follow allows; two lines with one timestamp from one
  container are not told apart, which `kubectl logs` cannot do either.
- **Reopen five seconds back.** A container behind the newest cursor would otherwise lose lines
  on every re-pin. Five seconds costs a few repeated frames the per-stream rule drops, and is
  longer than any pause the merge or the API server adds between containers.
- **The subscription is open only while following.** A viewer appending lines nobody is
  looking at fills a buffer for nothing, and a bounded buffer could not allow it at all. The
  paged state the core already has for a mid-stream anchor covers an unpinned viewer with no
  new code path. This revisits the ladder's *New lines arrive whatever the anchor*: they arrive
  whatever the anchor, while the viewer is pinned.
- **No pull-to-refresh.** The paged state replaces it.
- **A library, held apart by convention.** Kubetail keeps its core in one directory and its
  page in another, and nothing stops a page concern drifting in. Here the directory imports
  nothing from the app, and its README says so; a lint can hold the line once the library's
  shape has settled.

## Tasks

1. **Dependency.** `@tanstack/react-virtual` in `package.json`; `pnpm install`.
2. **The copies.** `types.ts` with `LogLine` (§2), `double-tailed-array.ts`, `before-paint.ts`,
   `fake-client.ts` and their tests, under `src/lib/log-viewer/`.
3. **The viewport.** `src/lib/log-viewer-store.ts` becomes `src/lib/viewport.ts`, the app's:
   `ViewerState` is `Viewport`, `ViewerStore` is `ViewportHandle` with `get`, `subscribe` and
   `set`, `createViewerStore` is `createViewportHandle`, `LogLine` is imported from the library
   and gains `cursor`, and `log-viewer-store.test.ts` becomes `viewport.test.ts`. The field on
   `LogsViewProvider` and the prop on `LogViewer` are `viewport`. Root `CLAUDE.md` follows.
4. **The core.** `src/lib/log-viewer/core.tsx` and `core.test.tsx` (§3).
5. **The index.** `src/lib/log-viewer/index.ts`, and the library's `README.md`.
6. **Docs.** Root `CLAUDE.md`, per *When it lands*.

## Tests

TypeScript, beside the files they cover. Kubetail's suites are the starting point, under
`vi.useFakeTimers()` with `advanceTimersByTimeAsync`, never a wall-clock wait; `stubLayout()` from
`@/test-utils` for the virtualizer.

- `double-tailed-array.test.ts`, `before-paint.test.ts`, `fake-client.test.ts`: as kubetail has
  them.
- `core.test.tsx`: opens at `Head`, `Tail` and `At` a moment, with the right rows at the right
  keys and, for `At`, that moment's line at key 0; loads more before and after as the range
  reaches an edge; pinned, a follow frame appends and the bottom stays in view; `pinToEnd` false
  opens no subscription and sets `hasMoreAfter`; scrolling up unpins, unsubscribes, and the
  published state says so with `atEnd` false; scrolling back to the bottom pages what arrived
  meanwhile, re-pins and resubscribes five seconds before the newest cursor with no row
  repeated; `paused: true` while pinned
  unsubscribes and leaves the viewport where it is, and `paused: false` again catches up the
  same way; `range`,
  `visible` and `atEnd` follow the viewport; `jumpTo` with each anchor kind lands there; a
  reconnect that replays the follow from its opening cursor adds no row; a follow line at or
  before its stream's newest is dropped and an older one from another stream is kept; a
  backward page fetched after follow lines still prepends every line it holds; the catch-up
  fetch after an unpin asks five seconds back and adds no row twice; an empty page
  with a cursor leaves the edge row saying *No more in the last read; scroll to keep looking*
  and fetches again only on the user's next scroll, never by itself; a fetch that rejects calls
  `onError` once and keeps the rows.
- `core.test.tsx` also: a `Tail` landing with `pinToEnd: false` stays unpinned; a user scroll
  to the bottom re-pins only once the last page after answered no `nextCursor`.

## Security

**Widens:** nothing. The core draws nothing and talks to nothing. One dependency is added,
`@tanstack/react-virtual`, which computes positions and touches no HTML sink; the
`cluster-data-is-text` lint runs over the copied files like any other.

## When it lands

- Root `CLAUDE.md`, *Frontend*: a paragraph on `src/lib/log-viewer/` — the core, the client
  seam, the fake, and that it is kubetail's reshaped. The right sidebar's sentence *lines arrive
  either way; pinning is the viewport alone* becomes *lines arrive while the viewer is pinned*,
  since the `LogViewer` contract now says so.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `make test-changed` while working; `pnpm build`, `make test-js`, `make lint-js`.

Nothing to see by hand. The tests are the whole check.
