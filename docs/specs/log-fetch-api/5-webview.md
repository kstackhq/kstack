# Step 5 — The webview

**Files:** `src/lib/logs-client.ts`, `src/lib/log-viewer/types.ts` and `README.md`,
`src/components/widgets/log-viewer/row.tsx`, `src/components/widgets/logs-read-card.tsx`,
`src/lib/chats.tsx`, `src/lib/logs-view.tsx`.
**Prerequisites:** steps 4A and 4B.
**Status:** Planned.

## Goal

The app reads logs over the new wire with no change to how the viewer behaves. The viewer and
the transcript's `LogsRead` card draw a cut line, and the card marks the lines outside the range.

Read `src/CLAUDE.md`'s log viewer paragraphs, `src/lib/log-viewer/README.md` and
`src/lib/logs-client.ts` first.

## The client

`createLogsClient` keeps the library's `Client` contract and maps each `fetch` onto
`LogsFetchInput`. It builds the scope once from the `LogsViewAction` through `scopeInput`,
today's `viewInput` renamed, and puts that same scope in every request, which is what the window
needs.

| Library query | Wire request |
| --- | --- |
| `Head`, no cursor | `pageFrom: First`, bounds open |
| `Head`, cursor | `pageCursor`: the library's cursor, a page's `next` or `follow` |
| `Head`, seen | `pageFrom: First`, `since` the follow's start, no padding, `seen` the lines held per stream |
| `Tail`, no cursor | `pageFrom: Last`, bounds open |
| `Tail`, cursor | `pageCursor`: the library's cursor, a page's `prev` |
| `Around`, `at` | two pages at once: `since: at`, `pageFrom: First` and `until: at`, `pageFrom: Last` |

`limit` is sent as `pageSize`; `maxMessageBytes` and `maxPageBytes` are left at 0 (the server's caps).
The page's `notices` become the library page's `skipped` lines of text, so the viewer shows them
where it shows a skipped stream today; `stream-skipped` notices are those streams.

**The page's edges come from its three strings.** The library's `before` edge is `prev`: a
cursor when set, `more: true`; else `more: false` with no cursor. Its `after` edge is `next` when
set, `more: true`; else `follow` when set, `more: false` with a cursor, the live end of the
README's table; else `more: false` with no cursor.

**`Around` is two pages joined.** The client sends both, waits for both, and answers one `Page`:
the `Last` half's lines then the `First` half's, oldest first; `before` is the `Last` half's
`before` edge and `after` is the `First` half's `after` edge, as the README's edge table has
always said. A line stamped exactly at `at` is in both halves; the client keeps the `First`
half's copy. `anchor` is the index of the `First` half's first line, or the joined page's length
when that half is empty. One half failing fails the `Around`.

**An edge's library cursor is the server's page cursor, as it came.** The library's `Cursor`
stays an opaque string the client passes through: the cursor holds the window, the two halves of
an `Around` included, so the client wraps nothing and sends no bounds on a continuation. A line's
cursor is the server's token as it came, and the client never sends one as `pageCursor`: the
library's `FetchQuery` gains `seen: Cursor[]`, filled from its per-stream map of newest lines, and
a `Head` with `seen` is a first page with `seen` (step 1), which is what catching up after a
follow needs. A cursor never expires and survives a cache clear. A continuation the server refuses
arrives as a validation error whose `rule` is `cursor-invalid`, which happens only across a
version of Kstack; the client surfaces it as the fetch's error, and the viewer recovers as it does
from any failed fetch, by reading a first page again.

**`follow(cursor)` sends `cursor` through as it came,** on `LogsFollowInput` with
`maxMessageBytes: 0` and no scope: a library edge cursor is a page's `follow`, which the server
reads as that page's poll. The viewer never sends `since` to open a follow; a follow from a moment
is a page over that window and then its `follow`. **A reconnect sends `seen`,** the last line the
library holds of each stream, with the scope and `since` the moment the follow was first opened:
the library keeps a map of stream key to the newest line's `cursor` as lines arrive, and the
checkpoint state and frame go. **The page after a follow reads the same way.** The window's
`resume` is no longer a cursor once a follow has run; the next `Head` carries `seen` from the same
map, which the client sends as a first page with `seen`, `since` the follow's start. `FetchQuery`
gains `seen: Cursor[]`, null on every other read. The frames map by `type`: `Line` to the
library's `Line` with `lineOf`; `Notice` to a new library frame `{ kind: 'Notice'; code; message
}`, which the viewer shows where it shows a page's `skipped` text; `Ended` to `Ended` with the
reason, the code dropped. A follow the server refuses to open calls `onFailed` as today, and the
viewer reopens from a page.

A page from `next` or `follow` may hold a line stamped older than one the window holds, from a
stream whose clock runs behind (step 1, rule 12). The library appends a `Head` page after what it holds
and prepends a `Tail` page before it, in page order, and drops a repeat by cursor; a straggler is
drawn where it lands, as a follow's out-of-order line is today. Nothing in the library changes
for it. The README's edges section says so in one sentence.

Update the `LogsFetch` document to select `truncatedBytes` on each record,
`page { prev next follow }` and `notices { code message }`. `request` is not selected: the viewer
keeps its own.

## The library

`LogLine` in `types.ts` gains:

```ts
  /** The bytes the server cut off `message` at the request's cap, 0 when whole. */
  truncatedBytes: number;
```

`FollowFrame` gains `{ kind: 'Notice'; code: string; message: string }`, and the window shows it
as it shows a page's `skipped` lines.

`lineOf` in `logs-client.ts` fills it. `FakeClient` fills it (`0`), and gains a `truncate`
option that sets it on chosen lines, for the row test. The library draws nothing,
as today.

Update `README.md`'s *The contract* section: the client now speaks the window wire, the table of
edges is unchanged, and a page going on from the `after` edge may hold a line older than one
held, from a stream whose clock runs behind, drawn where it lands.

## What is drawn

- **The viewer's row** (`row.tsx`): a cut line ends with a muted ` … 18 KiB cut` showing
  `truncatedBytes` in KiB (one decimal, `B` under 1 KiB), with `title="Cut by the server"`. Nothing opens the
  whole line yet; that is a later change.
- **The `LogsRead` card** (`logs-read-card.tsx`, mounted by `chat-transcript.tsx`): draw a line
  stamped before the action's `since` or after its `until` muted, with a `before` or `after` tag
  before its timestamp, the same two comparisons the tool's sample makes; draw a cut line as the viewer's
  row does; replace the line that reads `dropped` and the two edges with one muted line built
  from `olderMore`, `newerMore` and `notices`: *Older lines exist.*, *Newer lines exist.*, then
  each notice's `message`, with its `code` in a `title` so a reader can tell them apart. Draw
  `pageFrom` where `mode` was drawn. Every string is cluster text through
  `VisibleText`, as the card does today.
- **`chats.tsx`** selects the new action fields in the messages document. **`logs-view.tsx`**
  only re-exports the action's type and needs no change.

## Tasks

1. **Client.** Rewrite the `LogsFetch` and `LogsFollow` documents, the `fetch` mapping and the
   `follow` mapping; the edges from the three strings; the `Around` join; delete `edgeCursor`
   and `edgeOf`. Tests in
   `logs-client.test.ts` for each row of the table, for each state of the three strings, for
   notices becoming `skipped`, for a `Head` with `seen` sent as a first page, for an edge passed
   back as it came, for a refused cursor reaching the caller, for `Around`: the join's order, the line at
   `at` kept once, the anchor with and without a `First` half, and one half failing; and for
   `follow`: an edge cursor sent as `cursor`, a reconnect sending `seen` with one line per
   stream, the newest, each frame type mapped, and a refused open.
2. **Library types.** Add the fields, `FetchQuery.seen` and the `Notice` frame; replace the
   window's checkpoint `resume` with the per-stream map, read by the reconnect and by the next
   `Head`; update `FakeClient` to honour `seen` and every test fixture that builds a `LogLine`
   (`grep -rln "gap: null" src` finds most); show a `Notice` frame in the window. Tests: a
   `Head` after a follow sends `seen` and holds no line twice.
3. **Row.** The cut marker and its test.
4. **Card.** `logs-read-card.tsx` and its tests; `chats.tsx`'s selection.
5. **Build and docs.** `pnpm build`, `make test-js`, `make lint-js`. Update
   `src/lib/log-viewer/README.md` and the `LogsRead` sentences in `src/CLAUDE.md`.

## Acceptance

- `pnpm build`, `make test-js` and `make lint-js` pass.
- The viewer behaves as before over the new wire: `log-viewer.test.tsx` passes unchanged except
  for the fixtures' new fields.
- `make cover-js` does not drop below `scripts/coverage-threshold`.

## When it lands

`src/CLAUDE.md`'s log viewer and `LogsRead` paragraphs and `src/lib/log-viewer/README.md`
describe the window wire.
