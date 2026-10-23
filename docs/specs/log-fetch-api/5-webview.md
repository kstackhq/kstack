# Step 5 — The webview

**Files:** `src/lib/logs-client.ts`, `src/lib/log-viewer/` (`types.ts`, `window.ts`,
`fake-client.ts`, `README.md`), `src/components/widgets/log-viewer/row.tsx`,
`src/components/widgets/logs-read-card.tsx`, `logs-watch-card.tsx`, `src/lib/chats.tsx`.
**Prerequisites:** steps 3 and 4.
**Status:** Planned.

## Goal

The app reads logs over the new wire with no change to how the viewer behaves. The viewer and
the transcript's two cards draw a cut line, and the `LogsRead` card marks the lines outside the
range. `pnpm build` is green again at the end of this step.

Read first: `src/CLAUDE.md`'s log viewer and tool-call paragraphs, `src/lib/log-viewer/README.md`
and `src/lib/logs-client.ts`. The wire is step 3's schema, generated into `src/gql/`.

## The client

`createLogsClient` keeps the library's `Client` contract and maps each `fetch` onto
`LogsFetchInput`. It builds the scope once from the `LogsViewAction` through `scopeInput`,
today's `viewInput` renamed, and sends that same scope on every first page, which is what the
window needs. `fetch` is implemented for the first time: today it rejects.

| Library query | Wire request |
| --- | --- |
| `Head`, no cursor, no `seen` | `pageFrom: First`, bounds open |
| `Head`, cursor | `cursor`: the library's cursor, a page's `next` or `follow` |
| `Head`, cursor, `seen` | `cursor` the same, `seen` the lines held per stream |
| `Tail`, no cursor | `pageFrom: Last`, bounds open |
| `Tail`, cursor | `cursor`: the library's cursor, a page's `prev` |
| `Around`, `at` | two pages at once: `since: at`, `pageFrom: First` and `until: at`, `pageFrom: Last` |

`limit` is sent as `pageSize`; `maxMessageBytes` and `maxPageBytes` are left at 0, the server's
caps. The page's `notices` become the library page's `skipped` lines, `message` alone, so the
viewer shows them where it shows a skipped stream today.

**The page's edges come from its three strings.** The library's `before` edge is `prev`: a
cursor when set, `more: true`; else `more: false` with no cursor. Its `after` edge is `next` when
set, `more: true`; else `follow` when set, `more: false` with that cursor, which is the live end
of the README's table; else `more: false` with no cursor.

**`Around` is two pages joined.** The client sends both, waits for both, and answers one `Page`:
the `Last` half's lines then the `First` half's, oldest first; `before` is the `Last` half's
`before` edge and `after` the `First` half's `after` edge, as the README's edge table has always
said. A line stamped exactly at `at` is in both halves; the client keeps the `First` half's copy.
`anchor` is the index of the `First` half's first line, or the joined page's length when that half
is empty. One half failing fails the `Around`.

**An edge's library cursor is the server's page cursor, as it came.** The library's `Cursor`
stays an opaque string the client passes through: the cursor holds the window, so the client
wraps nothing and sends no bounds on a continuation. A line's cursor is the server's token as it
came, and the client never sends one as `cursor`: it travels only in `seen`. A cursor never
expires and survives a cache clear. A continuation the server refuses arrives as a validation
error whose `rule` is `cursor-invalid`, which happens only across a version of Kstack; the client
surfaces it as the fetch's error, and the viewer recovers as it does from any failed fetch, by
reading a first page again.

**`follow(cursor, seen)` sends both through as they came,** on `LogsFollowInput` with
`maxMessageBytes: 0` and no scope: a library edge cursor is a page's `follow`, which the server
reads as that page's poll and then goes on from. The viewer never sends `since` to open a follow;
a follow from a moment is a page over that window and then its `follow`. **A reconnect sends the
same `cursor` and the `seen` the window holds then**, through `reconnectVariables`, so the
checkpoint state goes. The frames map by `type`: `Line` to the library's `Line` with `lineOf`;
`Notice` to the library's new `Notice` frame; `Ended` to `Ended` with the notice's `message` as
the reason, the code dropped. A follow the server refuses to open calls `onFailed` as today, and the viewer reopens
from a page.

A page from `next` or `follow` may hold a line stamped older than one the window holds, from a
stream whose clock runs behind. The library appends a `Head` page after what it holds and
prepends a `Tail` page before it, in page order, and drops a repeat by cursor; a straggler is
drawn where it lands, as a follow's out-of-order line is today. Nothing in the library changes
for it. The README's edges section says so in one sentence.

The two documents are step 3's, written there so its codegen passes: `LogsFetch` selects
`truncatedBytes` on each record, `page { prev next follow }` and `notices { code message }`,
and not `request`, since the viewer keeps its own; `LogsFollow` selects `type`,
`record { … truncatedBytes }`, `notice { code message }` and `ended { code message }`. This
step changes them only if the code needs a field they lack.

## The library

`LogLine` in `types.ts` gains:

```ts
  /** The bytes the server cut off `message` at the request's cap, 0 when whole. */
  truncatedBytes: number;
```

`FetchQuery` gains `seen: Cursor[] | null`, set on the `Head` that follows a follow and null on
every other read. `FollowFrame` loses `Checkpoint` and gains
`{ kind: 'Notice'; code: string; message: string }`, which the window shows as it shows a page's
`skipped` lines. `Client.follow` becomes `follow(cursor, seen, onFrame, onFailed)`.

**The window keeps the newest line per stream, not a checkpoint.** `LogWindow` holds a map of
stream key to the newest line's `cursor` as lines land, from pages and from frames, keyed as the
sidecar names a stream (namespace, pod, container, previous). `resume` stays the follow's cursor,
the `after` edge of the page that pinned the window, and never moves on a frame: the page after a
follow, and a reconnect, send it with `seen` from the map, which is how a reader that holds no
stream catches up. `followed` reads no checkpoint; a `Notice` frame joins `skipped`.

`lineOf` in `logs-client.ts` fills `truncatedBytes`. `FakeClient` fills it (`0`) and gains a
`truncate` option that sets it on chosen lines, for the row test; it honours `seen` by reading
each named stream past its line and the rest from the cursor, as the server does, and sends no
checkpoint. The library draws nothing, as today.

Update `README.md`'s *The contract* and *The edges* sections: the client speaks the window wire,
`seen` is how a page after a follow goes on, the table of edges is unchanged, a `Notice` frame
shows as a skipped line, and a page going on from the `after` edge may hold a line older than
one held, drawn where it lands.

## What is drawn

- **The viewer's row** (`row.tsx`): a cut line ends with a muted ` … 18 KiB cut` showing
  `truncatedBytes` in KiB (one decimal, `B` under 1 KiB), with `title="Cut by the server"`.
  Nothing opens the whole line yet; that is a later change.
- **The `LogsRead` card** (`logs-read-card.tsx`): `logLine` carries `truncatedBytes`, so the
  row draws the cut. A line stamped before the action's `since` or after its `until` is muted
  with a `before` or `after` tag before its timestamp, the two comparisons the tool's sample
  makes. `rangeLine` draws the padding (`100 lines before`, `PT5M after`, the span as the action
  holds it) and `pageFrom` where
  `mode` was drawn, nothing on a continuation. `Beyond` is built from `olderMore`, `newerMore`
  and `notices`: *Older lines exist.*, *Newer lines exist.*, then each notice's `message` with
  its `code` in a `title`. Every string is cluster text through `VisibleText`, as today.
- **The `LogsWatch` card** (`logs-watch-card.tsx`): its held lines go through the same
  `logLine`, so a cut line draws its cut. Nothing else changes.
- **`chats.tsx`**'s messages document already selects the new action fields (step 3).
  `logs-view.tsx` only re-exports the action's type.

## Tasks

1. **Client.** The `fetch` mapping with the `Around` join, the `follow` mapping, and
   `scopeInput`, over step 3's documents. Tests in `logs-client.test.ts` for each row of the table, for
   each state of the three strings, for notices becoming `skipped`, for a `Head` with `seen`
   sent as the cursor with `seen`, for an edge passed back as it came, for a refused cursor
   reaching the caller, for `Around`: the join's order, the line at `at` kept once, the anchor
   with and without a `First` half, and one half failing; and for `follow`: an edge cursor and
   `seen` sent through, a reconnect sending the `seen` of the moment, each frame type mapped,
   and a refused open.
2. **Library.** The types; the window's per-stream map and `seen` on the `Head` after a follow;
   the `Notice` frame shown; `FakeClient`; every test fixture that builds a `LogLine`
   (`grep -rln "gap: null" src` finds them). Tests: a `Head` after a follow sends `seen` and
   holds no line twice; a reconnect sends the newest line per stream; a `Notice` frame reaches
   `skipped`.
3. **Row.** The cut marker and its test.
4. **Cards.** `logs-read-card.tsx` and its tests; the watch card's test for a cut line.
5. **Build and docs.** `pnpm build`, `make test-js`, `make lint-js`. Update
   `src/lib/log-viewer/README.md` and the log viewer and `LogsRead` sentences in `src/CLAUDE.md`.

## Acceptance

- `pnpm build`, `make test-js` and `make lint-js` pass.
- The viewer behaves as before over the new wire: `log-viewer.test.tsx` passes unchanged except
  for the fixtures' new fields and the follow's new signature.
- `make cover-js` does not drop below `scripts/coverage-threshold`.

## When it lands

`src/CLAUDE.md`'s log viewer and `LogsRead` paragraphs and `src/lib/log-viewer/README.md`
describe the window wire and `seen`.
