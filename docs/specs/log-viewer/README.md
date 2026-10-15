# The log viewer

What is left of **the log viewer** sequence. Its steps are built: the sidecar reads a view's
logs through the API server (`services/logs`), the webview's headless core keeps a window of
lines and puts it behind a scroller (`src/lib/log-viewer/`, whose `README.md` states it), and the
viewer draws rows, columns and a selection over it in the right sidebar
(`src/components/widgets/log-viewer/`). Root `CLAUDE.md` and `sidecar/CLAUDE.md` say what each
does. The design started from [kubetail](https://github.com/kubetail-org/kubetail)'s dashboard
viewer and keeps its reading algorithm and its data structures; the contract between the layers
is kstack's own.

**Every line on screen is the fake's.** The viewer is written to a wire the sidecar does not
serve yet, [*The wire*](#the-wire), and `FakeClient` serves it in the meantime: `createLogsClient`
(`src/lib/logs-client.ts`) answers the fake, and the panel's header says *Fake lines* while it
does. `wip/log-views` does not merge into `main` until the real client stands in the fake's
place, since a shipped build that drew invented lines under a pod's name would be a bug the user
cannot tell from the truth. This README stays until then.

The contract the wire holds, which the real client keeps:

- **The client owns the paging position.** A page answers two edges, `before` and `after`,
  each a cursor and whether there is more that way. The core continues from a page's cursor and
  never from a line's. A cursor is opaque: the client mints every one, and the core mints
  none, reads none, and passes each back as it came. The `At` anchor's moment travels beside the
  cursor, as `at`, never in it.
- **The `Client` is the seam.** One `fetch({ mode, cursor, at, limit })` answering a `Page`, and
  one `follow(after, onFrame)` whose frames are lines and checkpoints. The fake, a file or the
  GraphQL client can feed the core. The sidebar and a future inline card share the client, not
  the component.
- **The viewer drops a repeat by cursor and keeps lines as they came.** A page or a follow can
  send again lines the viewer holds at the edge or checkpoint it went on from, and a follow's
  lines arrive per stream, not merged: two containers' lines can interleave out of time order.
- **Log text is cluster data.** The sidecar strips terminal control sequences before a line
  reaches the wire, and every cell is drawn as text.

## The wire

The schema the viewer is written to. The library's `Client` type
(`src/lib/log-viewer/types.ts`) mirrors it field for field, and `FakeClient` serves it. The
sidecar serves an older shape today, with `since`, `until`, `after` and `before` in place of the
cursors; its plan for serving this one is [`notes/the-sidecar-wire.md`](notes/the-sidecar-wire.md).

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

## What is left

- **The sidecar's side of the wire, and the client over it.** Serving [*The wire*](#the-wire) — the pages
  with two edges, the checkpoints and the cursors, on `logsFetch` and `logsFollow` — is planned in
  [`notes/the-sidecar-wire.md`](notes/the-sidecar-wire.md), taken up next. With it comes
  the real client, designed no further than this paragraph: `createLogsClient`
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
  card is a second consumer of the client (`logs-client.ts`) and the row (`log-viewer/row.tsx`)
  the viewer has, and never mounts the core.
- **`LogsRead`.** Rung 4 of that ladder. It will reuse the fetch.
- **Facets.** Kubetail's sidebar lists each region, zone, OS, architecture and node the open
  sources span, with a count per value. The panel here shows only the filters the model set.
  Facets need one query answering what the view's sources resolved to, which the sidecar's source
  resolution already computes and the wire does not carry. Its own spec names the query.
- **Download**, **ANSI colours**, and a **timezone setting**. None is needed for a first viewer.
  Colours mean drawing escape sequences, and the rule here is to strip them.
- **The dashboard's target.** Rung 7 of the ladder.

## Shared vocabulary

- **A position**: a line's place in a view's order — its moment, its stream, and its ordinal
  among its stream's lines at that moment. Every line has one and no two share one. The sidecar's
  `position` type.
- **A cursor**: text the client wrote, which the webview passes back and never reads. A **line
  cursor** rides every line and names it; a **page cursor** rides a page's edges and a follow's
  checkpoints and names where to continue. What else it carries is the client's business: the
  sidecar's plan is §4 of [the note](notes/the-sidecar-wire.md), and the fake's is its own.
- **An edge**: one side of a page — a page cursor and whether there is more that way. A
  `before` edge with nothing older has no cursor.
- **A checkpoint**: a follow frame carrying the cursor it is safe to resume from: every line
  before it has been sent.
