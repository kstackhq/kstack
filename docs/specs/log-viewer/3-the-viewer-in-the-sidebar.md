---
title: The viewer in the sidebar
scope: frontend
status: Built
---

# The viewer in the sidebar

**Needs:** step 2. **Unblocks:** steps 4A and 4B, and rung 2 of the log views ladder.

TypeScript paths are under `src/`.

## In short

The placeholder goes. Say *tail webapp logs*, press Expand, and the right sidebar shows lines:
the time, the pod and the message of each, in a mono font, landing at the view's anchor and
following at the end when the view says so. The viewer's `LogViewport` says what is on screen
from the first line drawn. The lines are the fake's, written under the view's own pods, and the
panel's header says so.

Three pieces: a client that turns a view into a `Client` — the fake, seeded from the view's
sources — the viewport the app keeps beside the focused view, and the body of `LogViewer` that
puts the core behind the props the sidebar already mounts it by.

## What is not in this step

- **No real client, and no sidecar change.** The sidecar does not serve the README's [*The wire*](README.md#the-wire) in this
  sequence, and the client over it is sketched in the README's *Not in this sequence*. `logs-client.ts` answers the fake until
  then, and swapping it is that file and the header's line.
- **No columns to pick, no wrap, no jump buttons.** Step 4A. Lines are one row each, cut at the
  right edge with a horizontal scroll.
- **No selection, no copy.** Step 4B. `selection` in the viewport stays empty.
- **No colours**, and no timezone setting. Times are local, in the format §4 fixes.
- **No inline lines.** Rung 2 of the ladder.

## Design

### 1. The client

`src/lib/logs-client.ts`:

```ts
export type LogsClient = { client: Client; start: () => () => void };

export function createLogsClient(clusterId: string, action: LogsViewAction): LogsClient;
```

The one place in the app that imports `src/lib/log-viewer/fake-client.ts`. It answers a
`FakeClient` whose streams are the view's own: one per source, in the source's namespace, the
pod named after the source's name, and one stream per container the source names — `app` when
it names none, `previous` as the source says — each placed on a node of the fake's own naming.
The log is 5,000 lines two seconds apart, ending now, growing at 2 a second, each fetch taking
150 ms: nearly three hours of history, so a moment an hour ago lands with lines on both sides,
and paging and following both show on screen with no page instant. `clusterId` is taken and
unused, since the real client needs it and the panel passes it either way.

`start` begins what the client does on its own and answers its stop. The fake is built with no
`rate`: `start` sets the interval that appends a line every 500 ms, and the stop clears it. The
real client's will do nothing, but the seam has it so that swapping the client changes no caller.

**The fake spaces its history.** `FakeClientOptions` gains `spacingMs`: how far apart the
lines the fake starts with are stamped, 1 by default, so every existing test keeps its log.
This is the one change to `fake-client.ts`.

**The viewer owns the client's life.** It builds the client in a `useState` initializer and
calls `start` in an effect, whose cleanup runs the stop. The viewer is keyed on the call's id, so
a new call is a new client and a re-render of the same call keeps it. A client built that way
holds no timer until it starts, so the copy StrictMode's double render throws away leaks
nothing, and StrictMode's start, stop, start leaves one timer running.

### 2. The viewport

`src/lib/log-viewer-store.ts` becomes `src/lib/log-viewport.ts`, the app's, and its test follows:
`ViewerState` is `LogViewport`, `ViewerStore` is `LogViewportHandle` with `get`, `subscribe`
and `set`, `createViewerStore` is `createLogViewportHandle`, `EMPTY_VIEWER_STATE` is
`EMPTY_LOG_VIEWPORT` with `atEnd: false`, and `LogLine` is imported from the library's
`types.ts`. The store's `getState` becomes `get`; nothing outside the tests reads it yet. The
field on `LogsViewProvider` (`viewer` today), the panel's prop of the same name, the panel's
import of `ViewerStore`, and the prop on `LogViewer` (`store` today) are all `viewport`.

```ts
export type LogViewport = Pick<CoreState, 'range' | 'atEnd' | 'pinnedToEnd' | 'visible'> & { selection: LogLine[] };
```

### 3. The focused view knows its cluster

`LogsView` in `src/lib/logs-view.tsx` gains `clusterId`. The card sets it from the chat's
cluster: `LogsViewCard` gains a `clusterID` prop, passed down from `ChatTranscript`'s own (the
chat's stored `Chat.clusterID`, which `ChatPane` hands it) through `ToolCalls`, and through
`AgentBody` for a subagent's calls, since both draw cards. That prop is undefined until the list watch delivers the
chat, and the card's Expand is disabled until it is set, so a view is never focused without
its cluster. The panel hands it to the viewer with the action.
`LogViewer`'s props gain `clusterId` for it, and the panel is its one caller. The fake ignores
it; the real client is built from it, and the prop is laid now so that nothing above the viewer
changes then.

**Switching cluster clears the focused view.** The omnibox's switch already leaves whichever
chat the window is on; it now also calls the provider's `clear()`, since the panel would
otherwise go on drawing the last cluster's view. `setContext` leaves it alone, as it leaves the
chat, and arriving at another cluster's chat by Back or a deep link sets no view by itself.

**The panel says the lines are invented.** `logs-view-panel.tsx` draws *Fake lines* as a muted
tag in its header, beside the sources, for as long as `logs-client.ts` answers the fake. It is
unconditional: there is one client in the app, and the change that brings the real one removes
the tag in the same commit. A view the model opened draws under the pods the model named, so
without the tag a reader could take the fake's lines for the cluster's.

### 4. The viewer

`src/components/widgets/log-viewer.tsx` becomes the composition:

- Builds the client from `clusterId` and `query`, once per mount, starts it once mounted and
  stops it on unmount (§1). The core gets it wrapped (§5).
- Hands `openAt` to the core as its `anchor` and passes `pinToEnd`. The two shapes are the same,
  so there is nothing to map.
- Sets `firstPageSize` to 300 and `pageSize` to 250, and `edgeRowHeight` and
  `estimateRowHeight` to `LOG_ROW_PX`. The row file exports it beside `LOG_ROW_H`, the class
  every row and edge row carries; the number is read off a rendered row, as `ROW_H_PX` is in
  `virtual-table.tsx`, since jsdom has no layout to check it against.
- Draws each row with **`LogViewerRow`** (`log-viewer-row.tsx`), a component of its own that
  takes one `LogLine` and whether the view reads more than one container: the time, muted; the
  pod, muted, with its container after a `/` when it does; then the message. Every cell through
  `VisibleText`, mono, `whitespace-pre`, no wrap. The row at key 0 — where the view landed — has
  a left border. The row is its own file so that a card with no scroller (rung 2 of the ladder)
  draws the same row over the same client without the core. It is memoized over its line and
  `top`, and placed by `translateY`, so a frame that adds a line draws the new row alone and a
  prepend that moves every row lays none out again.
- **The time** is in the window's local zone: `HH:mm:ss.SSS` for a line written today, and
  `MMM d HH:mm:ss.SSS` for any other day, so a view of yesterday never reads as today. The
  timestamp's fraction is cut to three digits before it is parsed, since the wire allows nine
  and how many digits `Date` accepts is the engine's choice.
- **A view reads more than one container** when any source has `allContainers` or names more
  than one container. Two sources naming one container each are told apart by the pod.
- Draws an **edge row** above the first row while `before.more` and below the last while
  `after.more`, at `edgeRowHeight`: *Loading…* while a page that way is in flight (the edge's
  `loading`); after an empty page, *No more in the last read* with a *Keep looking* button
  calling `loadMore(edge)`, since the core pages again only on the user's next scroll and a
  rare grep reads the cluster in steps the user takes; otherwise blank. An unpinned window's
  after edge always has more, so a row saying *Loading…* whenever `more` is set would say it
  with nothing asked.
- A page's `skipped` lines are drawn once, muted, under the header: *Not read:* and each
  through `VisibleText`, since they name pods and containers.
- A follow that ended draws one muted line under the header: *No new lines:* and the core's
  `ended` through `VisibleText`, since the client's words can name pods.
- While the core is loading, a spinner over the rows; after it, with no line, *No lines.*;
  after a failed landing, *Could not load lines.* with a *Try again* button that calls
  `retry()`, which lands at `openAt` pinned as `pinToEnd` says.
- Makes a `CoreStore` once (`createCoreStore()`, held in `useState`), passes it to the core as
  `store`, and reads it with `useSyncExternalStore` for the spinner and the lines under the
  header. An effect writes it into `viewport` on every change, with `selection: []`, and writes
  `EMPTY_LOG_VIEWPORT` in its cleanup, so a closed view leaves no range behind for the composer
  to pull. The viewport is the mount's: the panel passes the one `LogsViewProvider` holds, since that is the
  focused view's and what the composer reads; a second viewer on the page would be handed one
  of its own.

The scroller is the core's, `overflow-auto` both ways, inside the panel's flex column under the
header. The panel does not scroll. Rows are `min-w-full w-max`, so the horizontal extent is the
widest row drawn and can change as rows scroll in and out. That is accepted until step 4A, whose
columns have widths of their own.

**The edges say what is in flight.** `EdgeState` in `src/lib/log-viewer/window.ts` gains
`loading`: the edge has `more` and a page that way holds a ticket. `edges()` reads it off the
tickets the window already keeps. Each method that sets or clears a ticket emits: `startPage`
and `pageFailed` gain the call, and `paged`, `landed`, `setPaused` and `followOpened` already
make it or leave the after edge with no `more`. `more` is in the rule so an edge never says both
that nothing is that way and that a page is coming. This is the one change to the core.

### 5. Errors

A fetch or a follow that fails leaves the rows as they are and draws one muted line under the
header: *Could not load more lines.* for a page, and the error's message through `VisibleText`
for a follow, since the real client will put the sidecar's reason there.

The core's `onError` cannot draw that line: it reports once per run of failures, does not say
what failed, and nothing reports a success. So the viewer hands the core the client wrapped
(`watchClient`, in `log-viewer.tsx`, built with the client in the same initializer):

- a `fetch` that rejects records *Could not load more lines.*;
- a `follow` that throws records the error's message and throws it on, so the core still
  unpins;
- a `fetch` that resolves, or a `follow` that returns, clears what is recorded.

The line is drawn while something is recorded and the core is neither `loading` nor `failed`.
A failed landing records too, but *Could not load lines.* already says it; the landing *Try
again* starts shows the spinner alone, and clears the record when it succeeds. A `Client`'s follow fails only by throwing on the
call; a follow that dies later ends with an `Ended` frame, drawn as above.

The viewer also hands the core an `onError` that calls `reportError` with source `network`, so
the error reaches the host's log as every operation error does; the core itself knows nothing
of the bus.

## Decisions this step asks for

- **The fake in the app, behind the real client's seam.** The viewer is built and tried over
  invented lines, so every rule of the core shows on screen months before the wire exists, and
  the swap is one file. The cost is a branch that cannot merge until then, which the README
  holds.
- **The tag is unconditional.** A flag that says which client is mounted is a second thing to
  keep true. One client, one tag, one commit that removes both.
- **One client per mount, keyed on the call.** The panel already keys the viewer on the call's
  id, so there is nothing to reset. A call is never edited.
- **`clusterId` on the focused view.** The action does not carry the cluster, and the panel has
  to say which one.
- **Local time, no setting.** A setting is kubetail's; nobody has asked for it here.
- **The client has a `start`.** The fake runs a timer, and a `Client` has no way to start or
  stop one. `start` is on what `createLogsClient` answers, not on `Client`: the core never runs a
  client's timer, its mount does. A `close` called from an effect's cleanup needs the client
  built in that effect and set into state, which `react-hooks/set-state-in-effect` refuses.
- **The viewer wraps the client to see errors.** The core reports a failure once and never a
  success, and it stays as built. What the viewer draws about errors is the viewer's, so it
  watches the client itself.
- **`EdgeState` gains `loading`.** The one change to step 2's core. Without it the viewer cannot
  tell a page in flight from an edge that merely has more, and an unpinned window would say
  *Loading…* at its foot forever.
- **The fake gains `spacingMs`.** Its history is 1 ms apart, so 5,000 lines span five seconds
  and no `At` landing finds lines before it. The default keeps step 1B's tests as they are.
- **Switching cluster clears the focused view.** The cluster is the window's one scope, and the
  switch already leaves the chat the view came from.
- **The date shows on a line not from today.** One fixed format would read yesterday as today;
  a date on every row costs a column the sidebar cannot spare.
- **`VisibleText` per cell.** It is the sink the transcript uses, and only the rows on screen are
  drawn, so the cost is bounded.
- **The viewer's width is left alone.** The sidebar's cap is 640px. Step 4A, which adds
  columns, decides whether to raise it.

## Tasks

Every file is kstack's alone.

1. **The client.** `spacingMs` on `FakeClientOptions` and its test; `src/lib/logs-client.ts`
   and its test (§1). **Done.**
2. **The viewport.** `src/lib/log-viewport.ts` and its test; `logs-view.tsx` and the panel renamed
   to it (§2). **Done.**
3. **The cluster and the tag.** `clusterId` on `LogsView`; `clusterID` on `LogsViewCard`, from
   `ChatTranscript` through `ToolCalls` and `AgentBody`, gating Expand and riding the card's
   `set`; on `LogViewerProps` and the panel's mount; the omnibox's switch clearing the view;
   *Fake lines* in the panel's header (§3). **Done.**
4. **The row.** `src/components/widgets/log-viewer-row.tsx` and its test (§4). **Done.**
5. **The edges in flight.** `loading` on `EdgeState` in `src/lib/log-viewer/window.ts`, and
   its test (§4). **Done.**
6. **The viewer.** `src/components/widgets/log-viewer.tsx` and its test (§4, §5). **Done.**
7. **Docs.** Root `CLAUDE.md`, per *When it lands*; the library's `README.md` for `loading` on
   an edge; the log viewer README's *Shared vocabulary* entry for the client, which gains
   `start`. **Done.**

## Tests

TypeScript, beside the files they cover. The viewer's tests hand it a client of their own
through `createLogsClient`'s seam (`vi.mock` of `@/lib/logs-client`) — a scripted `Client`, or a `FakeClient` driven by fake
timers — and `stubLayout()` from `@/test-utils` for the rows. No Tauri bridge: nothing here
talks to the sidecar.

- `src/lib/log-viewer/fake-client.test.ts`: with `spacingMs`, the history's lines are that far
  apart and end now; an `Around` page at a moment inside it holds lines on both sides.
- `src/lib/log-viewer/window.test.ts`: an edge is `loading` from `startPage` until `paged` or
  `pageFailed`, each publishing a snapshot; a landing and a pause clear it; the seeded walk
  checks that `loading` is set exactly while that edge has `more` and holds a ticket.
- `src/lib/logs-client.test.ts`: a view with two sources, one naming two containers, seeds one
  stream per source and container in the source's namespace under the source's name, `previous`
  as the source says; a source naming none seeds `app`; the client answers a page; no line is
  written before `start` or after its stop, and two a second between.
- `src/lib/log-viewport.test.ts`: as the store's test, renamed.
- `src/components/widgets/log-viewer-row.test.tsx`: time, pod and message through `VisibleText`
  (a message with a reordering character is spelled out); a line from today shows the time
  alone and one from another day the date before it; a timestamp with nine fraction digits
  draws its milliseconds; the container after the pod only when asked for; the landing row's
  border.
- `src/components/widgets/log-viewer.test.tsx`: opens at the tail with the last page drawn as
  rows; opens at a moment with that line at key 0; a follow frame adds a row and, pinned, keeps
  the bottom; the viewport holds `range`, `visible`, `atEnd` and `pinnedToEnd` after the first
  paint and `selection` empty; a failed fetch keeps the rows and draws the line, and the next
  fetch that succeeds clears it; a follow that throws draws its message; a follow that ended
  draws its reason; a failed landing offers *Try again* and draws no *Could not load more
  lines.*, not even while *Try again*'s landing loads, and *Try again* lands at the view's
  anchor without pinning a view that said not to;
  the edge row says *Loading…* only while a page that way is in flight; the edge row's button
  pages; a skipped stream is drawn under the header; a view with a source reading every
  container hands the row the flag, and two sources naming one container each do not;
  mounting starts the client, and unmounting stops it and empties the viewport.
- `src/components/widgets/logs-view-panel.test.tsx`: the viewer is mounted with the view's
  cluster and the provider's viewport; the header carries *Fake lines*.
- `src/components/widgets/logs-view-card.test.tsx`: Expand sets the chat's cluster, and is
  disabled while the cluster is undefined.
- `src/components/widgets/chat-transcript.test.tsx`: a subagent's `LogsView` card gets the
  chat's cluster.
- `src/components/widgets/context-omnibox.test.tsx`: picking another cluster clears the focused
  view.

## Security

**Widens:** nothing the webview can reach. No operation is sent: the lines are the fake's, made
in the webview, and the sidecar is not asked. The CSP is unchanged.

**What the user sees** is drawn as log text will be — cluster data, through `VisibleText` into
text nodes — so the real client changes nothing on this side. No HTML sink; the
`cluster-data-is-text` lint covers the files.

**What the user could mistake:** invented lines under a real pod's name. The header's tag is the
guard, and the README holds the branch from `main` until the tag and the fake go.

**Residual:** as step 1A's.

## When it lands

- Root `CLAUDE.md`, *Chat*: the right sidebar's paragraph says the viewer draws lines — the
  client seam, that it answers the fake for now with the header's tag, what a row holds, what
  the viewport gets, and that the placeholder is gone. Its *lines arrive either way; pinning is
  the viewport alone* becomes the rule the core keeps: lines arrive only while following, which
  is landed, pinned and not paused, so scrolling up closes the follow and reaching the end again
  pages in what was written meanwhile. The paragraph also says switching cluster clears the
  focused view, beside the omnibox's paragraph saying the switch leaves the chat. The log views
  README's *The viewer is its own work*
  paragraph is cut to a sentence.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while working.

By hand, in chat mode on any cluster: *tail webapp logs*, Expand, and the panel fills with the
fake's lines under the view's pods, tagged *Fake lines*, and keeps up at two a second; scroll up
with the wheel, and again by dragging the scrollbar, on each platform you can reach (macOS and
Linux draw it as an overlay), and it stops listening, scroll to the bottom and what was written meanwhile pages in and it
follows again; *logs from an hour ago* lands on that moment with lines above and below, and
scrolling either way loads more, each page after a visible beat; close and Expand again, and it
lands fresh; a view naming two containers draws the container after the pod; switch cluster
from the omnibox and the panel closes its view. Expand several
views in turn and confirm in the devtools' performance monitor that the timer count stays flat
once each panel is closed.
