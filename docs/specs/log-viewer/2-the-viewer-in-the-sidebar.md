---
title: The viewer in the sidebar
scope: frontend
status: Planned
---

# The viewer in the sidebar

**Needs:** steps 1A and 1B. **Unblocks:** steps 3A and 3B, and rung 2 of the log views ladder.

## In short

The placeholder goes. Say *tail webapp logs*, press Expand, and the right sidebar shows real
lines: the time, the pod and the message of each, in a mono font, landing at the view's anchor
and following at the end when the view says so. The viewer's `Viewport` says what is on
screen from the first line drawn.

Two pieces: a client that turns a view into the sidecar's two operations, and the body of
`LogViewer` that puts the core behind the props the sidebar already mounts it by.

## What is not in this step

- **No columns to pick, no wrap, no jump buttons.** Step 3A. Lines are one row each, cut at the
  right edge with a horizontal scroll.
- **No selection, no copy.** Step 3B. `selection` in the viewport stays empty.
- **No colours**, and no timezone setting. Times are local, in one fixed format.
- **No inline lines.** Rung 2 of the ladder.

## Design

### 1. The client

`src/lib/logs-client.ts`:

```ts
export function viewInput(action: LogsViewAction): LogsViewInput;
export function createLogsClient(client: UrqlClient, clusterId: string, action: LogsViewAction): Client;
```

`viewInput` is the one function that turns an action into the wire's input: sources, filters and
grep, field by field. `createLogsClient` answers the five methods:

- The four fetches run `logsFetch` through `client.query(...).toPromise()` with
  `requestPolicy: 'network-only'`, since a page is never the same twice. Each maps its
  argument onto the query's: `fetchSince` sends `since` with `Head`, `fetchUntil` sends `until`
  with `Tail`, `fetchAfter` sends `after` with `Head`, `fetchBefore` sends `before` with `Tail`.
  A result with an error rejects with it.
- `subscribe(after, onLine)` runs `logsFollow` through `client.subscription(...)` and calls
  back once per `Line` frame. It rides `subscribe-exchange`, which reconnects on its own with
  the same `after`; the core drops what it has seen (step 1B). A `watchFailed` frame is reported
  by the exchange already and the client does nothing more. An `Ended` frame is the follow's
  last: the client unsubscribes, so the exchange does not follow again, and hands the reason to
  `onError`.
- The records come back as `LogLine`s with `cursor` set to the record's own.

The client is built once per mount of the viewer, which is keyed on the call's id, so a new call
is a new client and a re-render of the same call keeps it.

### 2. The focused view knows its cluster

`LogsView` in `src/lib/logs-view.tsx` gains `clusterId`. The card sets it from the chat's cluster,
which the transcript already takes. The panel hands it to the viewer with the action.

`LogViewer`'s props gain `clusterId` for it. This is the one change to the contract, and the
panel is its one caller.

### 3. The viewer

`src/components/widgets/log-viewer.tsx` becomes the composition:

- Builds the client from `clusterId` and `query` (§1).
- Hands `openAt` to the core as its `anchor` and passes `pinToEnd`. The two shapes are the same,
  so there is nothing to map.
- Sets the core's page sizes to 300 on open and 250 after, kubetail's numbers for a page, and
  the edge row height to one row.
- Draws each row with **`LogViewerRow`** (`log-viewer-row.tsx`), a component of its own that
  takes one `LogLine` and whether the view reads more than one container: the time as
  `HH:mm:ss.SSS` in the window's local zone, muted; the pod, muted, with its container after a
  `/` when it does; then the message. Every cell through `VisibleText`, mono, `whitespace-pre`,
  no wrap. The row at key 0 — where the view landed — has a left border, as kubetail marks it.
  The row is its own file so that a card with no scroller (rung 2 of the ladder) draws the same
  row over the same client without the core.
- A row above the first or below the last says *Loading…* while more is coming, and, after an
  empty page with a cursor, *No more in the last read; scroll to keep looking* — the core
  fetches the next page only on the user's next scroll (step 1B), so a rare grep reads the
  cluster in steps the user takes, never in a loop.
- A page's `skipped` lines are drawn once, muted, under the header: *Not read:* and each
  through `VisibleText`, since they name pods and containers.
- While the core is loading, a spinner over the rows; after it, with no line, *No lines.*
- Writes the core's published state into `viewport` on every change, with `selection: []`.
  The viewport is the mount's, not the window's: the panel passes the one `LogsViewProvider`
  holds, since that is the focused view's and what the composer reads; a second viewer on the
  page would be handed one of its own and never this one.

The scroller is the core's, `overflow-auto` both ways, inside the panel's flex column under the
header. The panel does not scroll.

### 4. Errors

A fetch that fails leaves the rows as they are and draws one muted line under the header:
*Could not load more lines.* It clears on the next fetch that succeeds. The viewer hands the core
an `onError` that calls `reportError` with source `network`, so the error reaches the host's log
as every operation error does; the core itself knows nothing of the bus.

## Decisions this step asks for

- **One client per mount, keyed on the call.** The panel already keys the viewer on the call's
  id, so there is nothing to reset. A call is never edited.
- **`clusterId` on the focused view.** The action does not carry the cluster, and the panel has
  to say which one. The chat's cluster is known where the card is drawn.
- **Local time, one format.** A setting is kubetail's; nobody has asked for it here.
- **`VisibleText` per cell.** It is the sink the transcript uses, and only the rows on screen are
  drawn, so the cost is bounded.
- **The viewer's width is left alone.** The sidebar's cap is 640px. Step 3A, which adds columns,
  decides whether to raise it.

## Tasks

1. **Operations.** `LOGS_FETCH` and `LOGS_FOLLOW` with `graphql()` from `@/gql`, in
   `src/lib/logs-client.ts`; `pnpm codegen`.
2. **The client.** `src/lib/logs-client.ts` (§1).
3. **The cluster.** `clusterId` on `LogsView` and on the card's `set`; on `LogViewerProps` and the
   panel's mount (§2).
4. **The row.** `src/components/widgets/log-viewer-row.tsx` (§3).
5. **The viewer.** `src/components/widgets/log-viewer.tsx` (§3, §4).
6. **Docs.** Root `CLAUDE.md`, per *When it lands*.

## Tests

TypeScript, beside the files they cover. The Tauri bridge is `mockTauriCore()`; a fetch is
answered through `graphql_query`, a follow pushed through `liveChannel().onmessage!(...)`;
`stubLayout()` for the rows.

- `src/lib/logs-client.test.ts`: `viewInput` carries every field and nothing else; each fetch
  sends the right cursor argument and mode; a result with an error rejects; `subscribe` opens
  `logsFollow` with `after` and calls back per frame; cancel unsubscribes.
- `src/components/widgets/log-viewer-row.test.tsx`: time, pod and message through `VisibleText`
  (a message with a reordering character is spelled out); the container after the pod only when
  asked for; the landing row's border.
- `src/components/widgets/log-viewer.test.tsx`: opens at the tail with the last page drawn as
  rows; opens at a moment with that line at key 0; a follow frame adds a row and, pinned, keeps
  the bottom; the viewport holds `range`, `visible`, `atEnd` and `pinnedToEnd` after the first
  paint and `selection` empty; a failed fetch keeps the rows and draws the line; a view reading
  two containers hands the row the flag.
- `src/components/widgets/logs-view-panel.test.tsx`: the viewer is mounted with the view's
  cluster.
- `src/components/widgets/logs-view-card.test.tsx`: Expand sets the chat's cluster.

## Security

**Widens:** what the webview can ask the sidecar for — a cluster's logs, through operations the
sidecar already serves since step 1A. The CSP is unchanged: lines come over IPC like every other
frame.

**What the user sees** is log text, which is cluster data, drawn through `VisibleText` into text
nodes. No HTML sink; the `cluster-data-is-text` lint covers the file.

**Residual:** as step 1A's.

## When it lands

- Root `CLAUDE.md`, *Chat*: the right sidebar's paragraph says the viewer is real — the client,
  what a row holds, what the viewport gets, and that the placeholder is gone. The log views README's
  *The viewer is its own work* paragraph is cut to a sentence.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm codegen`, `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while
working.

By hand, in chat mode on a cluster with a Deployment that writes: *tail webapp logs*, Expand,
and the panel fills with lines and keeps up; scroll up and it stops listening, scroll to the
bottom and what was written meanwhile pages in and it follows again; *logs from an hour ago* lands on that moment with lines above and
below, and scrolling either way loads more; close and Expand again, and it lands fresh.
