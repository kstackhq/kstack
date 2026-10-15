# The log viewer

The build order for **the log viewer**: the scroller that draws a view's lines in the right
sidebar. It is ported from [kubetail](https://github.com/kubetail-org/kubetail), whose dashboard
has a log viewer built in three layers: a headless core that virtualizes and pages, a page on top
that adds columns, wrap, selection and copy, and a client underneath that talks to the server.
Kstack takes the same three layers, copied and reshaped to fit its own contracts. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply.

**Where it sits.** The [log views](../log-views/README.md) ladder says the viewer is its own
work, outside its rungs. This is that work. It builds on `wip/log-views`, since the panel it
mounts in exists there alone, and rung 2 of that ladder needs it done. Until the sequence lands,
`LogViewer` (`src/components/widgets/log-viewer.tsx`) is a placeholder whose props are the contract
the sidebar mounts it by, and those props do not change here.

**Where the sequence ends.** When the last step lands:

- the sidebar draws real lines for the view the model opened, landing at its anchor — the start,
  the end, or a moment — and loading more in either direction as the user scrolls;
- new lines arrive as they are written while the viewer is pinned to the end, which the view
  says on open and the user's hand changes after; unpinned or paused, the viewer stops listening
  and pages what it missed when the user scrolls back down;
- the user can pick columns, wrap long lines, jump to either end, select rows or cells, and copy
  them as text;
- the viewer's `Viewport` always says what is on screen, so the log views ladder can attach it
  to a question without the viewer knowing;
- the same client that feeds the sidebar can feed a card with no scroller, which is what rung 2 of
  the ladder builds.

## The idea in three sentences

The viewer never knows where lines come from: it talks to a **`Client`** with four fetches and one
follow, and anything that implements it can feed it. In kstack that client is a thin layer over
two GraphQL operations the sidecar serves through the IPC it already has. Everything above the
client — paging, landing, pinning, columns, selection — is copied from kubetail and reshaped,
never redesigned.

## The shape of the work

**Steps are numbered in waves.** `1A` and `1B` can be built at the same time: one is the sidecar,
the other the webview, and they meet only in step 2. `3A` and `3B` can be built at the same time
too, since each adds to the rows step 2 draws and neither needs the other. Prose says "step 1A".

**Every step can ship.** Step 1A serves lines nothing reads yet. Step 1B is a core with tests and
no screen. Step 2 is the first thing a user sees, and it is already useful. Steps 3A and 3B each
add a feature the viewer works without.

**Copy, then reshape.** Each step names the kubetail files it starts from and what changes. The
rule: take what works, change what kstack's contracts need, fix what is wrong, and drop what kstack has no use for.

**Attribution.** Both projects are Apache 2.0, and kubetail's files are copyright "The Kubetail
Authors", which includes contributors outside this team, so a copied file keeps their notice.
A file copied from kubetail, whole or reshaped, opens with both lines, kstack's first, over the
one license block:

```ts
// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
```

A file written fresh — the sidecar's merge, the client, the toolbar — carries kstack's line
alone, however closely it read kubetail. Each step's file table says which is which. No
`NOTICE` file: kubetail has none to carry forward.

**Every step's spec has the same sections, in the same order**: *In short*, *What is not in this
step*, *Design*, *Decisions this step asks for*, *Tasks*, *Tests*, *Security*, *When it lands*,
*Verification*.

## Fixed so far

These are decided across the sequence. A step that needs to change one says so in its *Decisions*
section.

- **The `Client` is the seam.** Four fetches — since, until, after, before, each a cursor and a
  limit answering records and a next cursor — and one `subscribe(after)`. A cursor names a
  line and is opaque to the client. The core knows nothing else, and the sidebar and a future inline card share the
  client, not the component.
- **Lines come through GraphQL.** A `logsFetch` query and a `logsFollow` subscription on the
  sidecar's schema, carrying a `LogsViewInput` built field by field from the `LogsViewAction` the
  call row keeps. The query rides `graphql_query`, the follow rides `subscribe-exchange`. No new
  transport.
- **The sidecar reads logs through the API server, with a raw request.** A container's log is
  one `GET` on the pod's `log` path, sent through the cluster connection the mirror already
  uses. Kubetail's fetcher algorithm — forward streams, backward windows, timestamp parsing — is
  copied over that request; its typed clientset is not, since `k8s.io/client-go/kubernetes` is
  what #20 is removing. The merge across containers is written fresh over the mirror, which
  already knows a workload's pods. Nothing of kubetail's cluster agent, source watcher or
  connection manager comes along.
- **Pinning is separate from the anchor.** Kubetail pins when the view opens at the tail and not
  otherwise. Kstack's view says `pinToEnd` on its own, so the core takes it as an input.
- **Lines arrive only while following.** `paused` is the user's Pause; `following` is pinned
  and not paused, and the subscription is open exactly then. "Follow" has one meaning in the
  code — lines are arriving — and `useFollow` is the hook that makes it so. An unpinned or paused viewer
  is in the core's paged state — `hasMoreAfter` set, scrolling to the bottom pages — and
  re-pinning subscribes after the last cursor. Nothing is appended behind the reader, which a
  bounded buffer later will need.
- **The viewer publishes, nothing streams.** The core publishes what it knows — loading,
  pinned, at the end, the visible range and count — and the viewer copies that into its
  `Viewport` with the selection. A reader pulls it once.
- **Log text is cluster data.** The sidecar strips terminal control sequences before a line
  reaches the wire, and every cell is drawn as text through the sinks the transcript uses.
- **The core is a library.** `src/lib/log-viewer/` imports `react` — and
  `@tanstack/react-virtual` once the core lands — and its own files, and nothing else. It knows
  lines, cursors, anchors and a scroll element, and nothing about columns, cells, the viewport,
  GraphQL or the error bus. Everything that knows those — the client, the viewport, the rows,
  the widths, the toolbar, the selection — lives in the app, beside `LogViewer` in
  `src/components/widgets/` or under `src/lib/`. The boundary is held by convention, not by
  lint, while the library may still change shape; its `README.md` states the contract and the
  boundary, so the directory reads as a package of its own.
- **The viewer's props stay as they are, but for one name.** `query`, `openAt`, `pinToEnd` and
  `viewport`, keyed on the call's id by its mount. Rung 1 called the last `store` and its type
  `ViewerStore`; step 1B renames them, since a store sounds like where the lines are kept, and
  the one thing it never holds is a line. The sequence fills the body.
- **A viewer is self-contained, so a page can hold several.** Its client, its lines, its
  widths and its selection are its own. Its `Viewport` belongs to whoever mounts it: the sidebar's
  is the one `LogsViewProvider` holds, which the composer reads for the focused view; another
  mount makes its own. Its keys are its own: keyboard handling listens on the viewer's scroller,
  never on `document`, so `Cmd/Ctrl+C` reaches the viewer that has focus and no other.

## The ladder

| Step | Builds | Spec |
| --- | --- | --- |
| 1A | Lines from the sidecar | [1A-lines-from-the-sidecar.md](1A-lines-from-the-sidecar.md) |
| 1B | The headless core | [1B-the-headless-core.md](1B-the-headless-core.md) |
| 2 | The viewer in the sidebar | [2-the-viewer-in-the-sidebar.md](2-the-viewer-in-the-sidebar.md) |
| 3A | Columns, wrap and controls | [3A-columns-wrap-and-controls.md](3A-columns-wrap-and-controls.md) |
| 3B | Select and copy | [3B-select-and-copy.md](3B-select-and-copy.md) |

## Not in this sequence

- **The inline card with lines.** Rung 2 of the log views ladder. It is not a smaller
  `LogViewer`: a fixed window of lines with no scroller needs neither the virtualizer nor the
  paging, so the card is a second consumer of the client (`logs-client.ts`) and the row
  (`log-viewer-row.tsx`) this sequence builds, and never mounts the core. Should rung 2 want a
  card that scrolls instead, `LogViewer` is self-contained and mounts there as it does in the
  sidebar.
- **`LogsRead`.** Rung 4 of that ladder. It will reuse step 1A's fetch.
- **Facets.** Kubetail's sidebar lists each region, zone, OS, architecture and node the open
  sources span, with a count per value. The panel here shows only the filters the model set.
  Facets need one query answering what the view's sources resolved to — the pods, each with its
  containers and where it runs — which step 1A's source resolution already computes and the
  wire does not carry; the panel would count off that. Whether a facet is read-only or a click
  that narrows the view is a ladder question, since a view the user narrows by hand is neither a
  new call nor the viewer's own state, and rung 3's snapshot has to say what was shown. Its own
  spec, after step 1A, names the query.
- **Download**, **ANSI colours**, and a **timezone setting**. Kubetail has all three; none is
  needed for a first viewer. Colours in particular mean drawing escape sequences, and the rule
  here is to strip them.
- **The dashboard's target.** Rung 7 of the ladder.

## Shared vocabulary

Fixed here so the steps agree. Go paths are under `sidecar/internal/`.

- **A line** (a *record* in kubetail's code): one log line with its timestamp, its message, its
  stream — the namespace, pod and container it came from, and whether it is from the instance
  before the last restart, which together name it — and its place: the node it ran on with the
  node's region, zone, OS and architecture. On the wire it is `LogRecord`; in the webview,
  `LogLine` in the library's `src/lib/log-viewer/types.ts`.
- **An anchor**: where the viewer is told to be — `Head`, `Tail`, or `At` a moment. The view's
  own word (`LogsViewAnchor` on the wire), and the only position the core's props and handle
  speak of.
- **A cursor**: how the client asks for more — the token the sidecar hands back with a page
  (`nextCursor`) and the client sends back for the lines before or after it. Opaque: it names a
  line by its moment, container and place among the container's lines at that moment, and a
  time alone names a moment. It never leaves the client and the core.
- **The client**: an object with the five methods of the `Client` type, built from a cluster id
  and a `LogsViewAction`. `src/lib/logs-client.ts`.
- **The core**: the headless component in `src/lib/log-viewer/`, which keeps the lines, pages,
  lands, follows and pins, and hands its rows to whoever draws them.
- **The viewer**: `LogViewer` in `src/components/widgets/log-viewer.tsx`, the core plus the rows,
  columns and selection, behind the props the sidebar mounts.
- **The viewport**: what one viewer has on screen — the visible range, whether it is at the
  end and pinned there, how many lines, and the selection — as a `Viewport` value held by a
  `ViewportHandle` the viewer writes and the composer pulls at send. `src/lib/viewport.ts`, the
  app's, since the selection is the viewer's and the reader is the composer. Not the lines:
  those are the core's.
- **`services/logs`**: the sidecar package that resolves a view's sources against the mirror and
  streams their lines.
