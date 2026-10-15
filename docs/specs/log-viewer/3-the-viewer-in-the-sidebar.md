---
title: The viewer in the sidebar
scope: frontend
status: Planned
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
- **No colours**, and no timezone setting. Times are local, in one fixed format.
- **No inline lines.** Rung 2 of the ladder.

## Design

### 1. The client

`src/lib/logs-client.ts`:

```ts
export function createLogsClient(clusterId: string, action: LogsViewAction): Client;
```

The one place in the app that imports `src/lib/log-viewer/fake-client.ts`. It answers a
`FakeClient` whose streams are the view's own: one per source, in the source's namespace, the
pod named after the source's name, and one stream per container the source names — `app` when
it names none, `previous` as the source says — each placed on a node of the fake's own naming.
The log is 5,000 lines ending now, growing at 2 a second, each fetch taking 150 ms, so paging
and following both show on screen and a page is never instant. `clusterId` is taken and unused,
since the real client needs it and the panel passes it either way.

The client is built once per mount of the viewer, which is keyed on the call's id, so a new call
is a new client and a re-render of the same call keeps it.

### 2. The viewport

`src/lib/log-viewer-store.ts` becomes `src/lib/log-viewport.ts`, the app's, and its test follows:
`ViewerState` is `LogViewport`, `ViewerStore` is `LogViewportHandle` with `get`, `subscribe`
and `set`, `createViewerStore` is `createLogViewportHandle`, `EMPTY_VIEWER_STATE` is
`EMPTY_LOG_VIEWPORT` with `atEnd: false`, and `LogLine` is imported from the library's
`types.ts`. The field on
`LogsViewProvider` and the prop on `LogViewer` are `viewport`.

```ts
export type LogViewport = Pick<CoreState, 'range' | 'atEnd' | 'pinnedToEnd' | 'visible'> & { selection: LogLine[] };
```

### 3. The focused view knows its cluster

`LogsView` in `src/lib/logs-view.tsx` gains `clusterId`. The card sets it from the chat's
cluster, which the transcript already takes. The panel hands it to the viewer with the action.
`LogViewer`'s props gain `clusterId` for it, and the panel is its one caller. The fake ignores
it; the real client is built from it, and the prop is laid now so that nothing above the viewer
changes then.

**The panel says the lines are invented.** `logs-view-panel.tsx` draws *Fake lines* as a muted
tag in its header, beside the sources, for as long as `logs-client.ts` answers the fake. It is
unconditional: there is one client in the app, and the change that brings the real one removes
the tag in the same commit. A view the model opened draws under the pods the model named, so
without the tag a reader could take the fake's lines for the cluster's.

### 4. The viewer

`src/components/widgets/log-viewer.tsx` becomes the composition:

- Builds the client from `clusterId` and `query` (§1), once per mount.
- Hands `openAt` to the core as its `anchor` and passes `pinToEnd`. The two shapes are the same,
  so there is nothing to map.
- Sets `firstPageSize` to 300 and `pageSize` to 250, `edgeRowHeight` to one row, and
  `estimateRowHeight` to one row.
- Draws each row with **`LogViewerRow`** (`log-viewer-row.tsx`), a component of its own that
  takes one `LogLine` and whether the view reads more than one container: the time as
  `HH:mm:ss.SSS` in the window's local zone, muted; the pod, muted, with its container after a
  `/` when it does; then the message. Every cell through `VisibleText`, mono, `whitespace-pre`,
  no wrap. The row at key 0 — where the view landed — has a left border. The row is its own
  file so that a card with no scroller (rung 2 of the ladder) draws the same row over the same
  client without the core.
- Draws an **edge row** above the first row while `before.more` and below the last while
  `after.more`, at `edgeRowHeight`: *Loading…*, or after an empty page with a cursor, *No more
  in the last read* with a *Keep looking* button calling `loadMore(edge)`, since the core
  pages again only on the user's next scroll and a rare grep reads the cluster in steps the
  user takes.
- A page's `skipped` lines are drawn once, muted, under the header: *Not read:* and each
  through `VisibleText`, since they name pods and containers.
- A follow that ended draws one muted line under the header: *No new lines:* and the core's
  `ended` through `VisibleText`, since the client's words can name pods.
- While the core is loading, a spinner over the rows; after it, with no line, *No lines.*;
  after a failed landing, *Could not load lines.* with a *Try again* button that calls
  `jumpTo(openAt)`.
- Writes the core's published state into `viewport` on every change, with `selection: []`. The
  viewport is the mount's: the panel passes the one `LogsViewProvider` holds, since that is the
  focused view's and what the composer reads; a second viewer on the page would be handed one
  of its own.

The scroller is the core's, `overflow-auto` both ways, inside the panel's flex column under the
header. The panel does not scroll.

### 5. Errors

A fetch or a follow that fails leaves the rows as they are and draws one muted line under the
header: *Could not load more lines.* for a page, and the error's message through `VisibleText`
for a follow, since the real client will put the sidecar's reason there. It clears on the next
fetch that succeeds. The viewer hands the core an `onError` that calls `reportError` with source
`network`, so the error reaches the host's log as every operation error does; the core itself
knows nothing of the bus.

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
- **Local time, one format.** A setting is kubetail's; nobody has asked for it here.
- **`VisibleText` per cell.** It is the sink the transcript uses, and only the rows on screen are
  drawn, so the cost is bounded.
- **The viewer's width is left alone.** The sidebar's cap is 640px. Step 4A, which adds
  columns, decides whether to raise it.

## Tasks

Every file is kstack's alone.

1. **The client.** `src/lib/logs-client.ts` and its test (§1). **Planned.**
2. **The viewport.** `src/lib/log-viewport.ts` and its test; `logs-view.tsx` and the panel renamed
   to it (§2). **Planned.**
3. **The cluster and the tag.** `clusterId` on `LogsView` and on the card's `set`; on
   `LogViewerProps` and the panel's mount; *Fake lines* in the panel's header (§3). **Planned.**
4. **The row.** `src/components/widgets/log-viewer-row.tsx` and its test (§4). **Planned.**
5. **The viewer.** `src/components/widgets/log-viewer.tsx` and its test (§4, §5). **Planned.**
6. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Planned.**

## Tests

TypeScript, beside the files they cover. The viewer's tests hand it a client of their own
through `createLogsClient`'s seam — a scripted `Client`, or a `FakeClient` driven by fake
timers — and `stubLayout()` from `@/test-utils` for the rows. No Tauri bridge: nothing here
talks to the sidecar.

- `src/lib/logs-client.test.ts`: a view with two sources, one naming two containers, seeds one
  stream per source and container in the source's namespace under the source's name, `previous`
  as the source says; a source naming none seeds `app`; the client answers a page.
- `src/lib/log-viewport.test.ts`: as the store's test, renamed.
- `src/components/widgets/log-viewer-row.test.tsx`: time, pod and message through `VisibleText`
  (a message with a reordering character is spelled out); the container after the pod only when
  asked for; the landing row's border.
- `src/components/widgets/log-viewer.test.tsx`: opens at the tail with the last page drawn as
  rows; opens at a moment with that line at key 0; a follow frame adds a row and, pinned, keeps
  the bottom; the viewport holds `range`, `visible`, `atEnd` and `pinnedToEnd` after the first
  paint and `selection` empty; a failed fetch keeps the rows and draws the line; a follow that
  throws draws its message; a follow that ended draws its reason; a failed landing offers *Try again*; the edge row's button pages; a
  skipped stream is drawn under the header; a view reading two containers hands the row the
  flag.
- `src/components/widgets/logs-view-panel.test.tsx`: the viewer is mounted with the view's
  cluster and the provider's viewport; the header carries *Fake lines*.
- `src/components/widgets/logs-view-card.test.tsx`: Expand sets the chat's cluster.

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
  pages in what was written meanwhile. The log views README's *The viewer is its own work*
  paragraph is cut to a sentence.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while working.

By hand, in chat mode on any cluster: *tail webapp logs*, Expand, and the panel fills with the
fake's lines under the view's pods, tagged *Fake lines*, and keeps up at two a second; scroll up
and it stops listening, scroll to the bottom and what was written meanwhile pages in and it
follows again; *logs from an hour ago* lands on that moment with lines above and below, and
scrolling either way loads more, each page after a visible beat; close and Expand again, and it
lands fresh; a view naming two containers draws the container after the pod.
