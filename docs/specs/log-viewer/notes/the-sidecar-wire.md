# The sidecar's wire: a plan for later

**This is a note, not a spec. Nothing in it is built in the log viewer sequence**, and no step
of that sequence changes `sidecar/`. The wire itself — the schema the webview's `Client` type
mirrors and `FakeClient` serves — is the log viewer README's [*The wire*](../README.md#the-wire).
This note keeps the sidecar's plan for serving it, as it was last worked, so the work starts from
it once the log viewer sequence has landed. It becomes a numbered spec then, and is reviewed again first: *Open problems* below lists
what a review found.

Go paths are under `sidecar/internal/`.

## In short

The sidecar takes over the paging position. A page answers two edges, `before` and `after`,
each a cursor and whether there is more that way, and the webview continues from them without
reading them. A follow whose reads have moved sends a checkpoint every five seconds saying where
it is safe to resume. A cursor becomes opaque base64 JSON, and a page's edge cursor carries a
hint per stream — where its log starts and how fast it writes — so the next backward page sizes
its first read right instead of doubling up to it. The edge at the live end and every checkpoint
also say where each stream's read reached, so a page or a follow that goes on from them sends no
line again and misses none.

Four pull requests into `wip/log-views`, in the order of the tasks below.

## What is not in this step

- **No webview change.** The real client over this wire is sketched in the log viewer
  README's *Not in this sequence*.
- **No session.** The hint is stateless: every page carries what the next needs, and the sidecar
  keeps nothing between requests.
- **No change to how a follow orders lines.** Per container, as step 1A has it.
- **No change to the reads themselves** beyond what the hint shortens. The forward stream, the
  backward windows, the budget and the merge stay as 1A built them.
- **No change to a follow opened with no cursor.** It reads from the sidecar's clock, as in step
  1A; the webview always sends one.

## Design

### 1. The wire

The schema and the edge table are the README's [*The wire*](../README.md#the-wire). What serving them changes:

The four cursor arguments `since`, `until`, `after` and `before` go; `cursor` takes their place,
and `at` carries the one time the webview names, the `At` anchor's. A cursor is only ever text
the sidecar wrote: `Head` and `Tail` refuse `at`, and `Around` refuses a cursor.
`LogRecord.cursor` stays: it names the line, and the webview drops a repeated line by it. A line
cursor is also a valid `cursor` argument, since it names a position, but it carries no hint;
a window that drops lines at an edge sends one there. `logsFollow` keeps
`after: String`, which the webview always sends.

In Go, `FetchQuery` becomes `{Mode FetchMode; Cursor *string; At *time.Time; Limit int}`, `Page`
becomes `{Records []Record; Before, After PageEdge; Anchor *int; Skipped []string}` with
`PageEdge{Cursor *string; More bool}`, and `FollowFrame` gains `Cursor *string`. `gqlgen.yml`
binds `LogRecordsPageEdge` onto `logs.PageEdge` and the `Around` member onto `logs.FetchAround`;
`LogsFollowFrameType`'s `Checkpoint` onto `logs.FollowCheckpoint`.

### 2. The edges

*The live end* is the edge of a page that read every stream to its end: a page cursor whose
moment is `liveEnd` and whose entries carry the hint and `upto` (§4), with `more: false`. *The
cut* is the position `trimToStops` answers today: the last record kept, or the nearest stop of a
stream that ran short, whichever is nearer.

The edges follow the README's table.

**Each stream keeps its own place.** The streams finish their reads at different times, and a
quiet container can write a line stamped before the newest record after its own read ended, so
the live end is not the newest record's position. A read forward from the live end starts each
stream just past its `upto`, the last line its read reached, and a stream with none — one whose
read reached no line, or one that started since — from the moment, inclusive, since nothing of
it was read there.

**The moment is the cluster's clock.** Container timestamps come from the nodes, and the
sidecar's clock is the user's machine, which can drift by more than any margin. So the getter
answers each log response's `Date` header beside its body, and `liveEnd` is the earliest `Date`
any of the fetch's log responses carried, truncated to its second, less `endOverlap`. Every
stream with no `upto` reached nothing by the end of its own read, which came after that
response, so only a line delivered late can fall before the moment, and `endOverlap` is the
margin for it. A view with no stream has no response to read: its live end is the zero moment,
from which a stream reads its whole log, since every stream that appears later is one the
webview holds nothing of.

`endOverlap` is a constant in `fetch.go`, 5 s: how far behind a node's clock a quiet container
can deliver a line, covering the API server's buffering and the clock skew between nodes and the
API server.

**`Around`** is two reads inside one `Fetch`: a `Tail` read bounded by `until` the moment `at`,
exclusive, and a `Head` read bounded by `since` it, inclusive, each at `limit`, each over its own
readings and its own budget, so a page holds up to twice `limit` and spends up to twice a fetch's
budget. `Records` is the first's then the second's, and `Anchor` is the first's length.
`Skipped` names each stream once, in the order the halves first named it. A half refused is the
page refused, since an `At` landing with one side missing would put its anchor at an edge that
says nothing about the lines past it. `Around` without `at`, or with a cursor, is refused with
`errBadQuery`, a new `Refusal` naming the mode and the argument, and so is `Head` or `Tail` with
`at`. `readOptsOf` keeps `bound.incl` for this use and for a stream read from a cursor's moment;
no cursor names an inclusive line.

A page with no record and `more: true` stays the honest answer for a read that spent its
budget, as in step 1A.

### 3. The follow's checkpoints

`followCheckpointEvery` is a field on `Service`, 5 s in production (`followCheckpoint`), so a test
shrinks it. `follow.run` arms a ticker at it beside the handoff timer, and on each tick sends
`FollowFrame{Type: FollowCheckpoint, Cursor: …}` when it may.

**A checkpoint's moment is the follow's opening moment**: the moment of the cursor it was opened
`after`, or its start with none. It never advances, so it reads no clock. A stream with no entry
has reached no line since that moment — it is new, lagging, held for its backoff, waiting for its
first read, or not yet in the mirror — so a follow reopened from the checkpoint reads it from
there and misses none of it.

**An entry is a stream the view resolves now that has an `upto`**: the last line its read reached
since the follow opened, else the `upto` the follow opened with. A stream that left the view
leaves the checkpoint, so a long follow's cursor does not grow with every pod it has outlived; if
it comes back, its lines since the moment are sent again and the webview drops them by cursor.
No stream's `upto` moves back.

**A read reaches a line the grep dropped.** A follow's forward reader sends a *mark* — a position
with no record — through the fan-in when its grep has dropped lines and nothing has gone out from
that read for `followMarkEvery` (1 s). The mark moves `tracked.last` as a line does and is never
sent to the client. It rides the same channel as the read's lines, so an `upto` never passes a
line still queued, and a reopened follow does not read again hours of lines its grep drops.

No checkpoint is sent after `Ended`, none before the handoff has released (its held lines have
moved `tracked.last` and not yet gone out), and none on a tick when no entry moved since the last
checkpoint: the earlier one is still safe, and a quiet follow sends nothing.

A checkpoint's entries carry `upto` and no hint: the webview resumes from it only forward, and a
hint shortens only a backward read.

A follow opened `after` a checkpoint or a live end starts each stream just past its `upto`, and a
stream with none from the moment, inclusive, or from its start for the zero moment. So a reopen
sends again only what its streams' reads reached after the last checkpoint, at most one interval,
which the webview drops by cursor.

A checkpoint says nothing about ordering: lines still arrive per container.

### 4. The cursor

`cursor.go`'s `cursorOf` writes a line's position as the standard base64 (`base64.StdEncoding`)
of one JSON object, keys in this order, no whitespace:

```json
{"v":1,"t":"2026-10-08T10:00:00.123456789Z","ns":"team-a","pod":"api-7f9c","c":"app","prev":false,"n":0}
```

`v` is the cursor's version, `t` the moment with nanoseconds, `ns`, `pod`, `c` and `prev` the
stream, and `n` the ordinal. A moment is the object with `v` and `t` alone, which reaches the wire
only inside a page cursor; the zero moment is `t` `0001-01-01T00:00:00Z`. The keys are short
because every record carries its cursor.

**A page cursor is a position or a moment with one more key, `s`**: one entry per stream, in the
order `resolve` answered them — the fetch's streams for a page, the streams the view resolves now
for a checkpoint — carrying the hint and, at the live end and on a checkpoint, `upto`.

```json
{"v":1,"t":"…","s":[{"ns":"team-a","pod":"api-7f9c","c":"app","prev":false,"first":"2026-10-07T22:14:03.1Z","spl":0.42,"upto":{"t":"2026-10-08T10:00:00.123456789Z","n":0}}]}
```

- `first` is the moment of the stream's first line as `firstTimestamp` read it, or empty when
  this fetch did not read it and the fetch's own cursor carried none for the stream.
- `spl` is seconds per line: the span the stream's records in this page cover, divided by one
  less than their count. `0` when the page held fewer than two of its records, in which case
  the entry carries the value the fetch's own cursor carried for that stream, if any.
- `upto` is the moment and ordinal of the last line the stream's read reached, whether or not it
  made the page or passed the grep, numbered as a line's cursor is: forward, the last line in
  range (`span.last`); backward, the newest line `takeBack` took, never a line of a window not yet
  taken, since a window that starts inside a moment numbers that moment from its own top. Absent
  when the read reached none, in which case the entry carries the value the fetch's own cursor
  carried for that stream, if any. Only the live end and a checkpoint carry it: every other edge
  is a position, which a read continues from exactly.

A page of 500 lines carries 500 line cursors of about 110 bytes and two edge cursors of about
100 bytes per stream, 140 at the live end. A checkpoint carries about 100 bytes per stream the
view resolves. Both are bounded by `maxStreams`.

**`parseCursor`** decodes the text as base64, then as the object, and refuses it with
`errBadCursor` when it is neither, a bare RFC 3339 time included, when `v` is not 1, when `t` is
not a time, when `c` is present and empty, when `n` is negative, when an `s` entry has an empty
`c`, or when an `upto`'s `t` is not a time or its `n` is negative. A key it does not know is
ignored. It answers the `position` and a `[]streamEntry`, which `Fetch` and `Follow` hand each
stream's `readOpts` by `StreamKey`: the hint, and for a read forward from the cursor the `upto`
as that stream's exclusive bound in place of the cursor's moment. A tab-separated cursor of 1A's
form is refused like any other text: a cursor lives in a viewer's memory and nowhere on disk.
`errBadCursor`'s words become *a cursor must be one a page handed back*.

**The hint shortens a backward read.** In `fetcher.go`:

- `readBackward` uses the hint's `first` in place of the `firstTimestamp` GET when the hint has
  one. Only a read under `until` carries a hint, since a `Tail` read with no cursor has no
  entries, so this reaches `backwardUntil` alone.
- `backwardUntil` sizes its first `reach` as `max(1s, spl × limit × 2)` when `spl` is set, instead
  of the span over 2^14, capped at the span from `first` to `until`.

**A wrong `spl` costs GETs and never lines**: it changes how many windows a read takes and
nothing else. **`first` is never later than the log's real first line**: a log loses lines at its
head and never gains older ones, and the next log under a `StreamKey` — a restart, a pod recreated
under its name — starts later. A `first` earlier than the real first line, which rotation makes
it, costs windows that read nothing and ends on the same test as today, a window clamped to
`first`. A `first` later than it would end the read early and lose lines; the sidecar never writes
one, and a cursor is only text the sidecar wrote. `fetcher_test.go` pins an earlier `first` and an
`spl` off by ten.

**What a before-page costs**, per stream, for the quiet log a day old that 1A's doubling
windows handle worst:

| | today | with the hint |
| --- | --- | --- |
| GETs | up to 16 | 1 to 2 |

### 5. The resolvers

`schema.resolvers.go`'s `LogsFetch` takes `cursor` and `at` and passes them through; `LogsFollow`
is unchanged. A `logs.Refusal`, `errBadCursor` included, reaches the wire as
`KSTACK_VALIDATION_ERROR` through `logsErr`, as today.

## Decisions this step asks for

- **The sidecar owns the position.** It is the one process that sees every stream and where each
  read reached, so it is the one that can say where the next page starts per stream. The webview
  sees only the lines that passed.
- **The live end's moment is the cluster's clock.** It applies to streams the webview holds no
  line of, and a moment off the user's machine would skip their first lines whenever that clock
  runs ahead of the cluster's. The API server's `Date` is on every log response already.
- **A checkpoint's moment does not move.** A stream with no `upto` has reached nothing since the
  follow opened, so the opening moment is exact for it, and it needs no clock.
- **`upto` is where a read reached, not what it sent.** A grep that drops most lines would
  otherwise make every reopen read them again.
- **Two edges on every page, never one.** A landing at the end needs where to follow from; a
  landing at a moment needs both sides; a page toward one edge needs that edge. One shape
  serves all three, and the core never computes a cursor.
- **The inclusive pair goes from the wire.** Only the `Around` read and a stream read from a
  cursor's moment need an inclusive bound, and both are the sidecar's own.
- **A cursor is only what the sidecar wrote.** The one time the webview names travels as `at`, a
  `Time`, so `cursor` is opaque without exception.
- **A `before` edge with nothing older has no cursor.** A cursor nobody may send is better
  absent than present.
- **A follow's resume point is a checkpoint.** `Bookmark` already names the end of a delta
  watch's snapshot on this schema; one word with two meanings on one wire would be misread.
- **A checkpoint names the streams the view resolves now.** Its size is then bounded by
  `maxStreams`, and a stream that left and came back costs repeats the webview drops.
- **Hints ride the edge cursor, not the line's.** A line's cursor is sent 500 times a page; an
  edge's twice.
- **Base64 JSON, not a versioned tab form.** JSON ignores unknown keys and escapes anything, and
  the hint has to carry a list.

## Tasks

None is scheduled. Once this note becomes a spec, each is one pull request into
`wip/log-views`, in this order.

1. **The edges.** `services/logs/fetch.go`: `FetchQuery{Mode, Cursor, At, Limit}`, `FetchAround`,
   the `Around` double read with its refusal and its `skipped` named once, `endOverlap`.
   `services/logs/fetcher.go`: the getter answers the response's `Date` (zero when absent or
   unreadable), and a reading keeps the earliest. `services/logs/claim.go`: `get` reads it.
   `services/logs/merge.go`: `Page`, `PageEdge`, the edge rules of §2, `liveEnd` off the
   readings' `Date`s, the zero moment for a view with no stream. `graph/schema.graphqls`,
   `gqlgen.yml`, regenerate, `graph/schema.resolvers.go`. Until task 3 the live end is a bare
   moment, so a page or a follow from it sends again what its streams wrote after it, which the
   webview drops by cursor. **Not scheduled.**
2. **The checkpoints.** `services/logs/follow.go`: `FollowCheckpoint`, `FollowFrame.Cursor`,
   `followCheckpointEvery`, the ticker and its rules (§3), each checkpoint the follow's opening
   moment alone until task 3. Schema and `gqlgen.yml` for the member, regenerate. **Not scheduled.**
3. **The cursor and the entries.** `services/logs/cursor.go`: the encoding, `streamEntry`,
   `parseCursor` answering both. `services/logs/fetcher.go`: `readOpts.hint`, `readBackward` and
   `backwardUntil` reading it, `readOpts.upto` as a stream's bound, a reading's `upto` (§4), and a
   follow's reader sending marks. `services/logs/merge.go`: the edges carry entries, `upto` at the
   live end. `services/logs/fanin.go` and `follow.go`: a mark moves `tracked.last`; checkpoints
   carry `upto` for the streams the view resolves, and a follow opens each stream past its own.
   **Not scheduled.**
4. **Docs.** `sidecar/CLAUDE.md`'s *A cursor names a line* paragraph; root `CLAUDE.md`'s
   `logsFetch` mentions, if any. **Not scheduled.**

## Tests

Go, beside the files they cover. `TestPagesJoinWithoutLossOrRepeat` in `fetch_test.go` is the
gate: it pages by `before.cursor` and `after.cursor` alone and must hold on every task. Its
live-end cases join in task 3, since before `upto` a page from the live end repeats what its
streams wrote after the moment.

- `services/logs/fetch_test.go`: each row of the edge table in §2, including a page with no
  record and a view with no stream; the live end's moment is the earliest response `Date`,
  truncated, less `endOverlap`, whatever the sidecar's clock says; the live end has an `upto` for
  each stream its read reached a line of, the page's or not, the grep's or not; `Around`'s
  `anchor` with lines on both sides, on one side, on none; `Around` at `limit` holds up to
  `limit` lines each side; `Around` refused when either half is, and naming a stream skipped in
  both halves once; `Around` refuses any cursor and needs `at`, and `Head` and `Tail` refuse
  `at`; a `before` edge with nothing older is null; a page cut by a stop answers the stop as its
  cursor; `TestPagesJoinWithoutLossOrRepeat` over the new edges, from both ends and from a
  moment, and (task 3) growing its logs between pages, with a quiet stream's late line stamped
  before the newest, so a page from the live end is held to no loss and no repeat too.
- `services/logs/follow_test.go`: a checkpoint every interval while reads move; its moment the
  opening cursor's, never later; each stream's `upto` the last line its read reached, a grep's
  drop included through a mark, never moving back and never past a line not yet sent; none
  before the handoff releases; none after `Ended`; none for an interval in which no entry moved;
  a stream that left the view leaves the checkpoint; a follow opened after a live end sends no
  line the page held; a follow opened after a checkpoint sends again only what its streams' reads
  reached after it; a stream held for its backoff that has sent nothing loses no line across a
  reopen from a checkpoint taken meanwhile; a follow opened after the zero moment reads a new
  stream from its start.
- `services/logs/cursor_test.go`: a line's cursor decodes to its position and back; the zero
  moment round-trips; a bare RFC 3339 time, `v` other than 1, a tab-separated cursor, base64 of
  text that is not the object, an empty `c`, a negative `n` and an `s` entry with an empty `c` are
  each refused with `errBadCursor`; two cursors of one moment order by stream and ordinal as
  their positions do; an entry round-trips, with and without `upto`; an `upto` with a bad `t` or a
  negative `n` is refused; an unknown key is ignored; a missing `s` is no entry.
- `services/logs/fetcher_test.go`: a hint's `first` skips the first-line GET; a hint's `spl`
  sizes the first reach; a `first` earlier than the real first line and an `spl` off by ten
  each read every line in range, with the GETs counted; a page carries `spl` from its own cursor
  for a stream that gave it fewer than two lines; a backward read's `upto` keeps the right
  ordinal when its first window starts inside the newest moment; a follow's reader sends a mark
  after a second of dropped lines and none while lines go out.
- `graph/schema.resolvers_test.go`: `logsFetch` with each mode, reading the edges off the real
  `cursorOf`, never as a literal; `at` as a `Time`; a `Checkpoint` frame on `logsFollow`; a bad
  cursor and a bad mix of `cursor` and `at` are `KSTACK_VALIDATION_ERROR`.

The interval tests shrink `followCheckpointEvery` and `followMarkEvery` and wait on the frames;
"none for an interval" is a negative assertion, a `select` against a window of a few shrunk
intervals, and says so.

## Security

**Widens:** nothing. A cursor is text the webview passes back unread, as today; `parseCursor`
refuses the shapes it refused with base64 and JSON decoding in front. The hint's fields reach the
API server only as a `sinceTime` and a `tailLines`, each bounded as the fetch's own values are,
and a hint cannot make a read exceed the fetch's budget or its window cap. An `upto` only
narrows a forward read. A response's `Date` only moves where a stream with no line starts
reading; a cluster that lies in it hides its own lines, which it can do anyway. A checkpoint
carries stream names and line positions, all of which the page already carries.

**Residual:** as step 1A's.

## When it lands

- `sidecar/CLAUDE.md`, *A cursor names a line*: the encoding, the entries, the edges, the live
  end's moment and the checkpoint.
- The log viewer README's *The wire* moves into `sidecar/CLAUDE.md` and root `CLAUDE.md`, and its
  *Not in this sequence* loses its first item.

## Verification

Per [Verification commands](../../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs ./graph/...`; `make test-changed` while
working; `make lint-go`, `make vet-go`; after a schema change, `cd sidecar && go run
github.com/99designs/gqlgen generate` then `pnpm codegen` and `pnpm build` from the root.

By hand, with the sidecar running against a cluster whose deployment has been logging for a
day: a `Tail` `logsFetch` of 100 lines, then `Tail` with its `before.cursor`, repeated ten
times, each answering within a second and no line repeated or skipped across the pages; the
`after.cursor` of the first page decodes from base64 to a moment within seconds of the API
server's clock with an `s` entry per container, each with an `upto`; a `logsFollow` opened
`after` it, on a container that is writing, sends no line the page held, and a `Checkpoint`
within five seconds whose moment is the page's.

## Open problems

A review on 2026-10-15 found these in the plan above. Settle each before the plan becomes a spec.

1. **A hint can refuse a page.** Today `backwardUntil` sizes its first reach as the span over
   2^14, which is what lets the window cap reach `first`. `spl × limit × 2` is smaller whenever a
   page is denser than the log behind it — a bursty job, or a grep whose matches cluster — and
   then the windows run out before `first`. A stream that took no line answers `errShort` with
   no stop, and the merge refuses the page. Size the first reach as the larger of the two: the
   hint's gain is on quiet logs, where its reach is the larger one. Test a dense page over a log
   whose first line is far back.
2. **The zero moment is the "no bound" value.** `position.isZero()` means "names nothing" in
   `bound`, `inSinceMoment`, `tracked.last` and `follow.since()`, so a zero-moment cursor parses
   to no cursor, and a follow opened after it reads new streams from now, not from their start.
   A moment needs a representation of its own.
3. **A mark cannot wait out a quiet stream.** A read's channel is `spsc` and its one sender is
   the reader, which a quiet stream holds in a body read. So a mark can go out only on a dropped
   line, and the last second of drops before a silence stays unmarked, costing a repeat on
   reopen. Say so, and give the reader a clock parameter: the mark's test otherwise waits on the
   wall clock.
4. **"Misses none" fails past 5 s of node skew.** A stream with no `upto` reads from the live
   end's moment, off the API server's clock; a node more than `endOverlap` behind it stamps the
   lines written just after the fetch before that moment. A forward read that reached no line
   proved its range empty, so its `upto` can be its own lower bound — the cursor's position, or
   the log's start with no cursor — which needs no clock. The moment then covers only streams
   that appear after the fetch. Either do that, or list skew as a residual.
5. **Task 1 misses the follow.** `Follow` builds its bounds through
   `readOptsOf(view, FetchQuery{After: after})`, which task 1 replaces.
6. **A stream that leaves the view and returns repeats from the opening moment**, which can be
   hours, against the README's rule that a repeat is only what arrived after the last
   checkpoint. Once the window drops lines, the viewer may have forgotten those cursors, so the repeats are appended
   at the end, out of time order. Keep a departed stream's `upto` a while, or change the rule.
7. **A stream the page skipped** has no `upto` at the live end, reads from the moment, and its
   history never reaches the viewer. Say so.
8. **A position edge is not continued exactly.** A Head cut seconds behind the end misses a line
   a quiet container delivers late, stamped before the cut. Rare; the claim should say so.
9. **`parseCursor` checks too little.** Also refuse a `first` that is not a time, a negative
   `spl`, two entries for one stream, and more entries than `maxStreams`.
10. **`Around` does not say whose `Date`s make its live end.** The `Head` half's.
11. **The docs task misses two `sidecar/CLAUDE.md` paragraphs**: `logsFetch` ("hands it back as
    the cursor") and `logsFollow` ("asks a few seconds before its newest cursor").
12. **The fake's checkpoints differ from this plan's.** `FakeClient` checkpoints at the newest
    line less 5 s; this plan's keep the opening moment and a place per stream. Cursors are
    opaque, so nothing breaks, but the fake exercises other repeats than the sidecar will send.
