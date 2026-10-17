---
title: The compact inline form
scope: sidecar, frontend
status: Implemented
---

# The compact inline form

**Needs:** rung 1, landed. **Unblocks:** rung 3.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Say *tail webapp logs*, and the card in the transcript shows the last eight lines of `webapp`,
moving as lines arrive. Say *tail api logs*, and the `webapp` card stops moving: it keeps the
eight lines that were the tail when the `api` view was asked for, and shows the same eight after
a reload. Expand still opens the full viewer in the right sidebar, and while the sidebar shows
it, the card collapses to *Showing in the sidebar*.

This rung adds:

- **Sidecar.** `resolvedAt` on `LogsViewAction`: the moment the tool resolved the view, on its
  clock. A frozen card reads up to a moment it names.
- **Webview.** A fixed window of lines on the `LogsView` card, with no scroller of its own. The
  newest pinned `Tail` view of the chat runs live; every other card draws one page and stops.

## What is not in this rung

- **No interaction with the lines.** No selection, copy, context menu or columns. Rung 5 builds
  selection, in the full viewer.
- **No live again by hand.** An older card stays frozen; Expand is the way to see it live.
- **The sidebar opens on Expand alone**, as today.
- **The model reads nothing new.** The receipt is unchanged.

## Design

### 1. `resolvedAt`

`LogsViewAction` gains one field:

```graphql
  "When the sidecar resolved the view, on its clock."
  resolvedAt: Time!
```

`tools/logsview` writes it from the clock it already resolves a duration anchor on, so the
action keeps carrying resolved values only. The receipt does not mention it. A `shown_action`
stored before this rung has none; per the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md), a dev `app.db`
from before it is reset.

### 2. What the window shows

Eight rows, always. The window is a fixed height of eight `LOG_ROW_PX` rows from its first
paint, loading or not, so a card filling in never moves the transcript under the user.

A card is **live** when its view is a pinned `Tail` view, it is the newest `LogsView` call with
an action in the chat, and the sidebar is not showing it (§4). Every other card is **frozen**.
This keeps the ladder's rule that only a `Tail` view pins as it opens: a pinned `Head` or `At`
view shows its anchor, inline as in the viewer.

A frozen tail window ends at its **until** moment: for a pinned `Tail` view, the `resolvedAt` of
the next `LogsView` call with an action in the chat, the moment it stopped being live; for an
unpinned `Tail` view, its own `resolvedAt`, since it never ran.

**A live card that freezes ends at *until* too.** The newer call resolves before the webview
draws it, so a live card can hold lines stamped at or after *until* by the time it stops, or its
follow can still owe lines before it. It drops the lines at or past *until* and always reads its
frozen page, so it ends on the lines a reload reads.

| View | Live | Frozen |
| --- | --- | --- |
| `Tail`, pinned | the newest eight lines, following | the eight lines before *until* |
| `Tail`, unpinned | — | the eight lines before *until* |
| `Head` | — | the first eight lines |
| `At` | — | the eight lines from the moment |

Each cell starts with one read through the client the viewer uses, `createLogsClient`:

- *The newest eight*: `Tail`, no cursor, limit 8. Then `follow` from that first page's `after`
  cursor, even when the read went on past it, since a read on goes backward. Each line is
  appended, a cursor already held is dropped, and the window keeps the last eight. Lines arrive
  per stream, so the window sorts by timestamp as it appends.
- *Before until*: `Around` at *until*, limit 8, keeping the lines before the page's `anchor`.
- *From the moment*: `Around` at the anchor's `at`, limit 8, keeping the lines from `anchor` on.
- *The first eight*: `Head`, no cursor, limit 8.

**A short page reads on.** A selective grep can answer a page with fewer than eight lines, or
none, while its edge still says `more` (a spent budget, in the library's words). The read then
goes on in the same direction — `Tail` from the `before` edge for a tail window, `Head` from the
`after` edge for the others — until it holds eight lines or the edge says no more, at most four
pages past the first. A window that stops at the cap with `more` still set says *More lines may
match — Expand to read on* on the side where the read stopped: its top row for a tail window, its
bottom row for a `Head` or `At` window.

A card mounts its read once. When it stops being live, it closes its follow and keeps its lines.

### 3. A row

One line per row, no wrap: the time, the stream's dot and `pod/container` when the view reads more
than one stream, and the message, cut with an ellipsis at the card's edge. Every name and message
goes through `VisibleText`, as the viewer's rows do.

**Notices take a row.** A line's `gap` draws the viewer's notice in the row above it. The cap's
*More lines may match* draws where §2 says; a stopped follow's notice draws in the bottom row. Each
notice
displaces one line: the oldest for a tail window, the newest for a `Head` or `At` window, so the
window keeps the end it is anchored to.

Fewer lines than eight leave the rest of the window empty. No line at all, with no more to read,
draws *No lines*. A first read that fails draws *Couldn't read these logs*; `errorReportExchange`
has reported it. A follow that fails or ends keeps its lines, freezes the card and draws *Stopped
following*. `skipped` streams draw nothing inline: the full viewer names them.

### 4. The card

`LogsViewCard` keeps its header, description and receipt, and draws the window under them. While
its call is the focused view **and the right sidebar is open**, it draws *Showing in the
sidebar* in Expand's place and no window. Hiding the sidebar with its toggle, closing the panel,
or focusing another call brings the window back, frozen or live by §2. On the dashboard the window
draws by the same rules, with no Expand.

`ChatTranscript` walks the chat's `LogsView` calls with an action once per render and passes each
card its *until* moment: the next such call's `resolvedAt`, null for the newest, which alone may
run live. The window waits for the chat's cluster, as Expand does.

## Decisions this rung asks for

- **Eight lines.** Reason: enough to read a stack trace's head or a burst of requests, few
  enough that a chat with three views stays a chat. The number is one constant.
- **Live means newest, pinned and `Tail`.** Reason: an unpinned window that moved would not be
  the view the user asked for, and the ladder fixed that a `Head` or `At` view opens at its
  anchor. The viewer's window pins on the same condition.
- **A frozen pinned card ends where it stopped being live.** Reason: the call is a record, so a
  reload must show the same lines, and the lines it held when it froze must be those lines, not
  a jump back to when it was asked. The next call's `resolvedAt` is that moment, and the
  transcript already has it.
- **Clock skew is accepted.** *until* is on the sidecar's clock and a line's time is its node's,
  so on a cluster whose clock is off a frozen window can gain or lose the last lines around the
  moment. The full viewer shows them either way.
- **An older card is never live again by hand.** Reason: Expand already opens any card live in
  the sidebar, and a second way to run a stream inline would mean two live cards.
- **A new view stays inline until Expand.** Reason: with lines on the card the user sees the
  view without the sidebar, and a panel opening on every call takes the window from them.
- **One live card, plus the sidebar.** The README says *one stream at a time*. This rung reads
  that as one stream per view: the newest card can run inline while the sidebar shows an older
  view, which is two streams. Freezing the card whenever the sidebar is open would stop the
  stream the user just asked for.

## Tasks

All five tasks are done, in one commit on `log-views-2`.

1. **`resolvedAt`.** `sidecar/graph/schema.graphqls`, `tools/tool.go`'s `LogsViewAction`,
   `tools/logsview` setting it from its clock. `gqlgen generate`; root `pnpm codegen`;
   `src/lib/chats.tsx` selects it.
2. **The plan.** `src/lib/inline-log-lines.ts`: `inlinePlan(action, until)` answers the first
   read, the direction to read on and which lines to keep, per §2's table; `InlineLines` holds
   the eight lines, the notices, the follow and the error; `useInlineLogLines(client, action,
   until)` runs it over the client the card builds with `createLogsClient`, a parameter so the
   tests pass `FakeClient`.
3. **The window.** `src/components/widgets/logs-view-inline.tsx`: the rows of §3 at a fixed
   height. It reuses the viewer's `LOG_ROW_PX`, stream dot, gap notice and `lineTime`, which
   `components/widgets/log-viewer/index.ts` exports beside `LogViewer`.
4. **The card.** `logs-view-card.tsx` draws the window (§4); `chat-transcript.tsx` passes
   `until`.
5. **Docs.** Per *When it lands*.

## Tests

Go:

- `tools/logsview/logsview_test.go`: the action's `resolvedAt` is the injected clock's now.
- `sidecar/graph/schema.resolvers_test.go`: a `LogsView` call serves `resolvedAt`.

TypeScript, beside the files they cover, over `FakeClient`:

- `src/lib/inline-log-lines.test.ts`: each cell of §2's table reads what it says and keeps the
  right eight; a pinned `Head` or `At` view is never live; a live window follows from the page's
  `after` cursor, drops a repeated cursor, keeps the newest eight in timestamp order across
  streams; a live window whose first page was short follows from that page's `after` cursor; a
  short page reads on in its direction until eight lines or no more, and stops at the cap with its
  notice on the side it stopped; an *until* arriving closes the follow and keeps the lines; a live
  window that freezes holding lines at or past *until* drops them, and ends on the lines its frozen
  page reads, even when its follow had not yet delivered lines before *until*; a followed window
  that fills drops the short-read notice; a failed first read answers the error; a refused or
  ended follow keeps the lines with its notice.
- `src/components/widgets/logs-view-inline.test.tsx`: the window is eight rows high while
  loading, with fewer lines, and with none (*No lines*); a message and a pod name with a
  reordering character are spelled through `VisibleText`; a gap's notice displaces the oldest
  line of a tail window and the newest of a `Head` window; `pod/container` shows only for more
  than one stream; a failed first read draws *Couldn't read these logs*.
- `src/components/widgets/logs-view-card.test.tsx`: the focused card draws no window while the
  sidebar is open, and draws it when the sidebar is hidden or the focus clears; the dashboard
  draws the window with no Expand.
- `src/components/widgets/chat-transcript.test.tsx`: only the newest `LogsView` call with an
  action is live; each older card's *until* is the next such call's `resolvedAt`; a refused newer
  call neither freezes the older one nor sets its *until*.

## Security

**Widens:** nothing. The window reads through `logsFetch` and `logsFollow`, which the full viewer
already calls, over the same view. Log text is cluster data: the sidecar strips control
sequences before a line reaches the wire, and every message and name on the card goes through
`VisibleText`. No log line reaches the model.

## When it lands

- `src/CLAUDE.md`: the tool-calls paragraph says a `LogsView` card draws an eight-line window,
  live for the newest pinned `Tail` view, and points at `inline-log-lines.ts`; the focused-view
  paragraph says the card collapses only while the sidebar is open; the log viewer's paragraph
  says its `index.ts` also exports `LOG_ROW_PX`, the stream dot, the gap notice and `lineTime`.
- `sidecar/internal/tools/CLAUDE.md`: the `LogsView` paragraph mentions `resolvedAt`.
- The PR that lands task 1 says a dev `app.db` from before it is reset.
- This README's row for rung 2 keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/tools/logsview ./graph/...`; `gqlgen generate`, root
`pnpm codegen`, `pnpm build`, `make test-changed`, `make lint-js`.

By hand, in chat mode on a cluster with a busy Deployment:

- *tail webapp logs* draws a card whose eight lines move.
- *tail api logs* freezes the first card where it was, and the second moves. Reloading the window
  leaves the first card on the same eight lines.
- Expand on the first card opens it live in the sidebar and the card says *Showing in the
  sidebar*. Closing the panel, or hiding the sidebar with its toggle, brings its frozen lines back.
- *logs from yesterday* draws the eight lines from midnight and never moves; *logs since 10m ago,
  keep following* draws the eight from ten minutes ago, as Expand opens it.
- *errors in webapp today* fills the window with matches rather than *No lines*, or says more may
  match.
