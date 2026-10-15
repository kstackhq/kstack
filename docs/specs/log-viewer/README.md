# The log viewer

The build order for **the log viewer**: the scroller that draws a view's lines in the right
sidebar. It has three layers: a sidecar that reads logs through the API server and serves pages
and a follow; a headless core in the webview that keeps a window of lines, pages, lands, follows
and pins; and the viewer that draws rows, columns and a selection over the core. The design
started from [kubetail](https://github.com/kubetail-org/kubetail)'s dashboard viewer and keeps
its reading algorithm and its data structures; the contract between the layers is kstack's own.
The rules in [Working a numbered spec](../README.md#working-a-numbered-spec) apply.

**This sequence builds the webview's two layers over a fake client.** The sidecar's reads are
built (step 1A). The wire the viewer is written to — pages with two edges, a follow with
checkpoints — is a contract here, [*The wire*](#the-wire), served by `FakeClient` until the
sidecar serves it. So the viewer reaches the screen over invented lines, says so in its
header, and `wip/log-views` does not merge into `main` until the real client stands in the fake's
place.

**No step of this sequence changes the sidecar.** Every step is webview work under `src/`.
Nothing here edits `sidecar/`, its GraphQL schema or `gqlgen.yml`, or regenerates `src/gql/`.
A step that seems to need the server to change is a step to stop and ask about, not to widen:
the fake stands in for whatever the server will do. The sidecar's side of the wire is work of
its own, after this sequence, and its plan waits in a note,
[`notes/the-sidecar-wire.md`](notes/the-sidecar-wire.md), which is not a spec and holds no task.

**Where it sits.** The [log views](../log-views/README.md) ladder says the viewer is its own
work, outside its rungs. This is that work. It builds on `wip/log-views`, since the panel it
mounts in exists there alone, and rung 2 of that ladder needs it done. Until step 3 lands,
`LogViewer` (`src/components/widgets/log-viewer.tsx`) is a placeholder whose props are the
contract the sidebar mounts it by.

**Where the sequence ends.** When the last step lands:

- the sidebar draws lines for the view the model opened, landing at its anchor — the start,
  the end, or a moment — and loading more in either direction as the user scrolls;
- new lines arrive as they are written while the viewer is pinned to the end, which the view
  says on open and the user's hand changes after; unpinned, the viewer stops listening and pages
  what it missed when the user scrolls back down; paused, it asks for nothing until the user
  plays again;
- the user can pick columns, wrap long lines, jump to either end, select rows or cells, and copy
  them as text;
- the viewer's `LogViewport` always says what is on screen, so the log views ladder can attach it
  to a question without the viewer knowing;
- the same client that feeds the sidebar can feed a card with no scroller, which is what rung 2
  of the ladder builds;
- every line on screen is the fake's, until the sidecar's wire lands and the real client takes
  the fake's place behind the same contract.

## The idea in three sentences

The client owns the paging position: every page answers where the next page on either side
continues, a follow says where it is safe to resume, and the webview passes those cursors back
unread. The core is a plain class, the **window**, that holds the lines and decides what happens
on every event, and a thin React component that wires it to a scroller. Everything the user
sees — rows, columns, selection — sits above the core and never talks to the client itself.

## The shape of the work

**Steps are numbered in waves.** `1A` and `1B` are built, so wave 1 is done. Step 2 puts the window
behind a scroller, step 3 puts the viewer on screen over the fake, and `4A` and `4B` can be built at
the same time, since each adds to the rows step 3 draws and neither needs the other. Prose says
"step 1B".

**Every step can ship onto `wip/log-views`.** Step 1B is a class with tests and no screen. Step
2 is a component with tests and no screen. Step 3 is the first thing a user sees, over the fake's
lines, and every rule of the viewer can be tried there. Steps 4A and 4B each add a feature the
viewer works without. None of it reaches `main` until the real client does: a shipped build
that drew invented lines under a pod's name would be a bug the user cannot tell from the truth.

**Attribution.** Both projects are Apache 2.0, and kubetail's files are copyright "The Kubetail
Authors", which includes contributors outside this team. A file that carries kubetail's code,
whole or reshaped, opens with both lines, kstack's first, over the one license block:

```ts
// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
```

A file written fresh carries kstack's line alone, however closely it read kubetail. Each step's
task list says which is which. No `NOTICE` file: kubetail has none to carry forward.

**Every step's spec has the same sections, in the same order**: *In short*, *What is not in this
step*, *Design*, *Decisions this step asks for*, *Tasks*, *Tests*, *Security*, *When it lands*,
*Verification*.

## Fixed so far

These are decided across the sequence. A step that needs to change one says so in its
*Decisions* section. The first three are the contract between the viewer and whatever feeds it,
stated in full in [*The wire*](#the-wire): the fake honours it now, and the sidecar will once it
serves it.

- **The client owns the paging position.** A page answers two edges, `before` and `after`,
  each a cursor and whether there is more that way. The core continues from a page's cursor and
  never from a line's. A cursor is opaque: the client mints every one, and the core mints
  none, reads none, and passes each back as it came. The `At` anchor's moment travels beside the
  cursor, as `at`, never in it.
- **The `Client` is the seam.** One `fetch({ mode, cursor, at, limit })` answering a `Page`, and
  one `follow(after, onFrame)` whose frames are lines and checkpoints. Anything that implements
  it can feed the core: the fake, a file, the GraphQL client when it comes. The sidebar and a
  future inline card share the client, not the component.
- **The viewer drops a repeat by cursor and keeps lines as they came.** A page or a follow can
  send again lines the viewer holds at the edge or checkpoint it went on from, and a follow's
  lines arrive per stream, not merged: two containers' lines can interleave out of time order.
- **The viewer is built over the fake.** `FakeClient` (`src/lib/log-viewer/fake-client.ts`, step
  1B) is the one client in the app until the sidecar's wire lands: `createLogsClient` in
  `src/lib/logs-client.ts` (step 3) answers it, seeded from the view's own sources, and the
  panel's header says *Fake lines* while it does. Replacing the fake with the real client changes
  that one file and that one line. The fake models what the real wire does to a viewer — a page
  that fails, a stream it could not read, a follow whose streams interleave, a reopen's repeats,
  latency — so a viewer that holds against it holds against the sidecar.
- **The sidecar reads logs through the API server, with a raw request** (step 1A, built). A
  container's log is one `GET` on the pod's `log` path, sent through the cluster connection the
  mirror already uses. Kubetail's fetcher algorithm — forward streams, backward windows,
  timestamp parsing — is copied over that request; its typed clientset is not. The merge across
  containers is written fresh over the mirror, which already knows a workload's pods.
- **The core is a window and an adapter.** `LogWindow` (`src/lib/log-viewer/window.ts`) is a
  class with no React in it: it holds the lines, both edges, the pin, the pause and the resume
  cursor, takes events, and answers what the scroller must do next. `LogViewerCore`
  (`src/lib/log-viewer/core.tsx`) is the React component that makes one window per landing,
  runs the virtualizer, reads the user's intent off input events, and performs the window's
  answers against the scroller. Every rule about lines, edges and pinning is the window's;
  every rule about pixels is the adapter's.
- **A window that is not following is behind.** While the follow is open the after edge has
  nothing more; otherwise it always has, whatever the last page said, since lines go on being
  written past it. One rule covers unpinned, ended, and landed short of the end. A paused
  window is the exception: it asks for nothing that way until the user plays again.
- **Keys are stable.** Key 0 is where the view landed, and a prepend lowers nothing already
  held. The window keeps every line it is given: bounding it is work of its own (*Not in this
  sequence*).
- **Pinning is separate from the anchor, and the pin is the user's hand.** The view says
  `pinToEnd` on open, and it pins a `Tail` landing alone: a `Head` or `At` landing shows its
  anchor, and pins once the user scrolls to the end. After that, only the user's input unpins (a wheel, a key, a scrollbar
  drag, a touch that moves up) or pins (an input that reaches the bottom of a window loaded to
  the end, or a jump to the end). The core's own scrolls change nothing, because the adapter
  never reads a scroll event as the user's without an input event behind it.
- **Lines arrive only while following, or by a page the user scrolled for.** `paused` is the
  user's Pause; `following` is landed, pinned, not paused and not ended, and the subscription is
  open exactly then. Frames that arrive after it closed are dropped, and a paused window pages
  nothing after: nothing is appended behind the reader.
- **The viewer publishes, nothing streams.** The core publishes what it knows — loading,
  pinned, following, at the end, the visible range and count — and the viewer copies that into
  its `LogViewport` with the selection. A reader pulls it once.
- **Log text is cluster data.** The sidecar strips terminal control sequences before a line
  reaches the wire, and every cell is drawn as text through the sinks the transcript uses.
- **The core is a library.** `src/lib/log-viewer/` imports `react`, `@tanstack/react-virtual`
  and its own files, and nothing else: no `@/` import, no relative import that leaves the
  directory. It knows lines, cursors, anchors and a scroll element, and nothing about columns,
  cells, the viewport, GraphQL or the error bus. Everything that knows those lives in the app.
  Its `README.md` states the contract and the boundary. The boundary is held by convention
  until the library's shape settles, then by an ESLint config object scoped to the directory.
- **A viewer is self-contained, so a page can hold several.** Its client, its lines, its
  widths and its selection are its own. Its `LogViewport` belongs to whoever mounts it: the
  sidebar's is the one `LogsViewProvider` holds, which the composer reads for the focused view;
  another mount makes its own. Its keys are its own: keyboard handling listens on the viewer's
  scroller, never on `document`.

## The wire

The schema the viewer is written to. The sidecar serves step 1A's today; nothing in this
sequence changes that. Step 1B's `Client` type mirrors this schema field for field, and
`FakeClient` serves it. The sidecar's plan for serving it is
[`notes/the-sidecar-wire.md`](notes/the-sidecar-wire.md).

```graphql
"""
Which page of a view to read. `Head` reads oldest first after `cursor`, from the start with
none; `Tail` reads newest first before `cursor`, from the end with none; `Around` reads
`limit` lines before `at` and `limit` from it on, so its page holds up to twice `limit`, and
needs `at` and no cursor.
"""
enum LogsFetchMode {
  Head
  Tail
  Around
}

"One edge of a page: where the next page that way continues from, and whether there is one."
type LogRecordsPageEdge {
  "Opaque. A page's cursor, never a line's: it carries what the next read needs. Null on a `before` edge with nothing older."
  cursor: String
  more: Boolean!
}

"A page of a view's lines, oldest first in position order, whatever the mode."
type LogRecordsPage {
  records: [LogRecord!]!
  before: LogRecordsPageEdge!
  after: LogRecordsPageEdge!
  "For `Around`: the index of the first record at or after `at`, `records.length` with none. Null for the other modes."
  anchor: Int
  "Streams the page could not read, one line each, in the sidecar's words. Empty when every stream answered."
  skipped: [String!]!
}

extend type Query {
  logsFetch(
    id: ClusterID!
    view: LogsViewInput!
    mode: LogsFetchMode!
    cursor: String
    at: Time
    "From 1 to 500: the lines in a page, or on each side of `at` for `Around`."
    limit: Int!
  ): LogRecordsPage!
}

"""
One frame of a follow: a line, a checkpoint, or the last frame, saying why the follow ended.
Following again after `Ended` would end the same way. Detect each frame by `type`, never by
which field is missing.
"""
type LogsFollowFrame {
  type: LogsFollowFrameType!
  "The line. Null on `Checkpoint` and `Ended`."
  record: LogRecord
  "A follow opened `after` it misses no line. Null on `Line` and `Ended`."
  cursor: String
  "Why the follow ended, in the user's words. Empty on `Line` and `Checkpoint`."
  reason: String!
}

enum LogsFollowFrameType {
  Line
  Checkpoint
  Ended
}

extend type Subscription {
  "A view's lines as they are written, after `after`."
  logsFollow(id: ClusterID!, view: LogsViewInput!, after: String): LogsFollowFrame!
}
```

A cursor is only ever text the server wrote: `Head` and `Tail` refuse `at`, and `Around` refuses
a cursor. A line's cursor names the line, and the webview drops a repeated line by it. A page's
edge cursor names where the next page that way continues, and may carry whatever the server needs
to continue cheaply. A line cursor is also a valid `cursor` argument, since it names a position:
a window that drops lines at an edge continues from the last line it kept. `logsFollow` takes
`after: String`, which the webview always sends.

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

A `Head` page from a cursor does not look behind it, so its `before.more` is `true` whether or not
a line is there; a page that way may come back empty.

An `after` edge always carries a cursor, since the live end is where a follow starts. A view that
resolves to no stream answers a page with no record, `before` null and `more: false`, and `after`
the live end.

A page with no record and `more: true` is the honest answer for a read that spent its budget.
Its edge is still past the cursor it went on from, so reading on always moves.

A page or a follow that goes on from an edge or a checkpoint misses no line. It may send again
lines at or past that edge or checkpoint, which the viewer holds and drops by cursor, and never
an older one.

### A follow's checkpoints

While a follow's reads move, it sends a `Checkpoint` now and then: each stream's lines up to it
have been sent, so a follow opened `after` it misses none. None comes after `Ended`, and a quiet
follow sends none. A reopen sends again only lines at or past the last checkpoint, which the
webview drops by cursor. Lines arrive per stream, not merged.

## The ladder

| Step | Builds | Spec |
| --- | --- | --- |
| 1A | Lines from the sidecar | [1A-lines-from-the-sidecar.md](1A-lines-from-the-sidecar.md) |
| 1B | The window | [1B-the-window.md](1B-the-window.md) |
| 2 | The core | [2-the-core.md](2-the-core.md) |
| 3 | The viewer in the sidebar | [3-the-viewer-in-the-sidebar.md](3-the-viewer-in-the-sidebar.md) |
| 4A | Columns, wrap and controls | [4A-columns-wrap-and-controls.md](4A-columns-wrap-and-controls.md) |
| 4B | Select and copy | [4B-select-and-copy.md](4B-select-and-copy.md) |

Steps 1A and 1B are built; their specs stay as the record of what shipped.

## Not in this sequence

- **The sidecar's side of the wire, and the client over it.** Serving [*The wire*](#the-wire) — the pages
  with two edges, the checkpoints and the cursors, on `logsFetch` and `logsFollow` — is planned in
  [`notes/the-sidecar-wire.md`](notes/the-sidecar-wire.md), taken up after this sequence. With it comes
  the real client, which this sequence designs no further than this paragraph: `createLogsClient`
  runs `logsFetch` through `client.query(...).toPromise()` with `requestPolicy: 'network-only'`
  and `logsFollow` through `client.subscription(...)`, `viewInput` builds the `LogsViewInput` from
  the action field by field, the client keeps the last checkpoint and hands `subscribe-exchange` a
  `reconnectVariables` function in the operation's context so a reconnect dials from it, and an
  `Ended` frame unsubscribes and reports its reason. Both ride the transports the app has
  (`graphql_query`, `subscribe-exchange`); no new one. The fake goes from the app in the same
  change, and `wip/log-views` may merge after it.
- **The bounded window.** A cap of `maxLines` lines, dropped at the edge farther from the
  reader and never on screen, with what that asks of the virtualizer. Until it lands a long
  follow grows without end. It is proposed in a pull request of its own, over a viewer that
  already works.
- **The inline card with lines.** Rung 2 of the log views ladder. It is not a smaller `LogViewer`:
  a fixed window of lines with no scroller needs neither the virtualizer nor the paging, so the
  card is a second consumer of the client (`logs-client.ts`) and the row (`log-viewer-row.tsx`)
  this sequence builds, and never mounts the core.
- **`LogsRead`.** Rung 4 of that ladder. It will reuse the fetch.
- **Facets.** Kubetail's sidebar lists each region, zone, OS, architecture and node the open
  sources span, with a count per value. The panel here shows only the filters the model set.
  Facets need one query answering what the view's sources resolved to, which step 1A's source
  resolution already computes and the wire does not carry. Its own spec names the query.
- **Download**, **ANSI colours**, and a **timezone setting**. None is needed for a first viewer.
  Colours mean drawing escape sequences, and the rule here is to strip them.
- **The dashboard's target.** Rung 7 of the ladder.

## Shared vocabulary

Fixed here so the steps agree. Go paths are under `sidecar/internal/`.

- **A line** (a *record* in the sidecar): one log line with its timestamp, its message, its
  stream — the namespace, pod and container it came from, and whether it is from the instance
  before the last restart, which together name it — and its place: the node it ran on with the
  node's region, zone, OS and architecture. On the wire it will be `LogRecord`; in the webview,
  `LogLine` in the library's `src/lib/log-viewer/types.ts`.
- **An anchor**: where the viewer is told to be — `Head`, `Tail`, or `At` a moment. The view's
  own word (`LogsViewAnchor` on the wire), and the only position the core's props and handle
  speak of.
- **A position**: a line's place in a view's order — its moment, its stream, and its ordinal
  among its stream's lines at that moment. Every line has one and no two share one. The sidecar's
  `position` type.
- **A cursor**: text the client wrote, which the webview passes back and never reads. A **line
  cursor** rides every line and names it; a **page cursor** rides a page's edges and a follow's
  checkpoints and names where to continue. What else it carries is the client's business: the
  sidecar's plan is §4 of [the note](notes/the-sidecar-wire.md), and the fake's is its own.
- **A page**: `Page` in the webview (`LogRecordsPage` on the wire, when it comes) — lines, a
  `before` edge, an `after` edge, and the streams it could not read.
- **An edge**: one side of a page — a page cursor and whether there is more that way. A
  `before` edge with nothing older has no cursor.
- **A checkpoint**: a follow frame carrying the cursor it is safe to resume from: every line
  before it has been sent.
- **The client**: an object with the two methods of the `Client` type, built from a cluster id
  and a `LogsViewAction`. `src/lib/logs-client.ts`. The fake, in this sequence.
- **The fake**: `FakeClient` in `src/lib/log-viewer/fake-client.ts`, a `Client` over a log it
  invents, with knobs for what the real wire does to a viewer.
- **The window**: `LogWindow` in `src/lib/log-viewer/window.ts`, which keeps the lines and
  decides what each event does.
- **The core**: `LogViewerCore` in `src/lib/log-viewer/core.tsx`, the window behind a scroller,
  handing its rows to whoever draws them.
- **The viewer**: `LogViewer` in `src/components/widgets/log-viewer.tsx`, the core plus the rows,
  columns and selection, behind the props the sidebar mounts.
- **The viewport**: what one viewer has on screen — the visible range, whether it is at the
  end and pinned there, how many lines, and the selection — as a `LogViewport` value held by a
  `LogViewportHandle` the viewer writes and the composer pulls at send.
  `src/lib/log-viewport.ts`. Not the lines: those are the window's.
- **`services/logs`**: the sidecar package that resolves a view's sources against the mirror and
  streams their lines.
