# Step 4A — LogsRead on the window query

**Packages:** `tools/logsread`, `tools/logswatch`, `tools` (the action types) — paths relative
to `sidecar/internal/`.
**Prerequisites:** steps 1, 2 and 3.
**Status:** Planned.

## Goal

The model reads logs with the design's parameters and reads on with one printed token. What it
read is kept on the call's action with the new fields, so the transcript can draw it (step 5).

Read `tools/CLAUDE.md`'s *LogsRead* section, `logsread/parse.go`, `logsread/sample.go` and
`logsread/prompts/` first.

## The call

`prompts/schema.json` takes these keys. `description` stays as it is.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `sources` | array | required on a first page | What to read, as today: `namespace`, `resource` as `<kind>/<name>`, `containers`, `all_containers`, `previous` |
| `filters` | object | none | Keep only lines from these nodes, as today: `node`, `region`, `zone`, `os`, `arch` |
| `grep` | string | none | A regular expression each line's message must match, as today. Filters the whole window, context included |
| `since` | string | open | Start of the range: a time at any precision, or a duration back from now |
| `until` | string | open | End of the range, the same forms |
| `before` | integer or string | 0 | Context before the range: lines, or a duration as a span |
| `after` | integer or string | 0 | Context after the range, the same forms |
| `page_from` | `first` \| `last` | `last` | Which end of the window the page comes from |
| `page_size` | integer | 50 | 1 to 100 |
| `max_message_bytes` | integer | 2048 | Cut each message here. `0` is the most Kstack keeps of a message, 16 KiB |
| `max_page_bytes` | integer | 16384 | Cap on the lines' bytes together. 1 to 16384 |
| `cursor` | string | — | A token a read printed. It carries the scope and the window, so only `page_size`, `max_message_bytes`, `max_page_bytes` and `description` may come with it |

`mode` and `limit` are gone: name them in `Fields` as `BadInput` saying `page_from` and
`page_size` took their place, as `anchor` and `pin_to_end` are refused today. A `cursor` with any
other range key is `BadInput` on `cursor`.

`parse.go` maps the call onto `logs.FetchRequest`: a first page sets the scope from
`logsinput.Resolve`, the range, the padding and `PageFrom`; a `cursor` is the request's
`PageCursor`, passed through unread, beside the call's own `page_size`, `max_message_bytes` and
`max_page_bytes`. A service `Refusal` is a `logsinput.Refused` carrying the service's `Code` as
it came, so `cursor-invalid` reaches the model as such; `refusedBy`'s own code block and its
`errors.Is` ladder go. A `cursor` call skips `logsinput.Resolve`: the cursor holds the scope as
the read that printed it resolved it, and resolving again could change it. A `cursor` with
`sources`, `filters` or `grep` is `BadInput` on `cursor` saying the token already names them.

## The sample

What the model reads stays one header line then one line per record, oldest first, through
`logsinput.LineText`. Changes:

- A record outside the range starts its line with `[before]` or `[after]` after the timestamp
  and stream, so the model can tell padding from the range. The tool sets the marker by comparing
  the record's timestamp against the echoed request's `Since` and `Until`, which are inclusive.
- Every timestamp prints in UTC with nine fraction digits (`2026-10-21T04:59:58.221004000Z`):
  `logsinput.Stamp` moves from `RFC3339Nano`, which drops trailing zeros, to a fixed layout, so a
  timestamp copied into `since` and `until` names one nanosecond and not up to a thousand.
- A cut record ends with ` … [N bytes cut]`, with `N` its `TruncatedBytes`, so the model knows
  how much a whole read adds. Its timestamp is how the model reads it whole.
- `max_message_bytes` replaces `logsinput.CutLine`'s fixed 2 KiB: the service already cut the
  message, so `LineText` prints it as it came. Delete `CutLine` and `MaxSampleLine` once nothing
  uses them.
- `max_page_bytes` replaces the fixed `SampleBudget` for the lines; the header's own caps stay.
  `fit` and `dropped` go away: the service's page cap does the dropping, and the page says more
  remains.
- The header is three lines. The first is `FetchResponse.Request`, printed under the call's own
  keys: `request: sources=<as resolved> since=<time> until=<time> before=100 after=PT5M
  page_from=last page_size=50 max_message_bytes=2048 max_page_bytes=16384`, an open bound printed
  as `open`, a duration padding normalized (`PT300S` is `PT5M`), `page_from` left out on a
  continuation, and `filters` and `grep` present when given. The tool prints what the service
  echoed and resolves nothing itself. The second is the page's place:
  `page: prev=<token> next=<token> follow=<token>`, each `none` when the response's string is
  nil. Every page prints all three, as the design's table has them. The third is the notices,
  each as `code: message`, when there are any.
  Each token is the response's `Prev`, `Next` or `Follow` as it came; the tool stores nothing,
  since the cursor holds the window. A token is printed as it is: it is base64url, so redaction
  and stripping cannot see inside it and need not, since nothing inside is drawn as text (step
  1, *The encoding*). A token is a few hundred characters, a few thousand for a scope of many
  sources, and a `next` or `follow` grows with the streams the pages have shown, deflated, so a
  few KB at `MaxStreams` with ordinary names and more with the longest; the header's own cap stays, and a page whose tokens would not fit it prints `prev` and
  `next` before `follow`. A token that does not fit on its own is the case README decision 13
  leaves to this step: print it cut with a `notice` saying so, or hold it under a short alias
  the tool keeps for the chat; decide when the cap is first hit in a test, and record the
  choice in the prompt.
- A `next` or `follow` page may hold a line stamped older than one an earlier page showed (step
  1, rule 12). The
  sample prints it in its place among the page's lines and marks nothing: the timestamps say
  what happened.

## The action

`tools.LogsReadAction` (`tools/tool.go`) changes to match the request:

- Replace `Mode` with `PageFrom` (`first` or `last`; empty on a continuation).
- Add `Before`, `After` as the call gave them: a `Lines int` and a `Span string` (the ISO 8601
  duration as given, empty when lines).
- `Since` and `Until` stay as today's resolved moments of the range, from `FetchResponse.Request`;
  `ResolvedAt` is the response's, the first page's clock, where today it is the tool's own.
- Add `Notices []LogsNotice`, each `{Code, Message string}` as the service answered them, so the
  card can tell one from another.
- Replace `Dropped` and the `Before`/`After` edges with `OlderMore`, `NewerMore bool`:
  `Prev` set and `Next` set. The tokens themselves are not kept on the action.
- `tools.LogsReadLine` gains `TruncatedBytes int`. The card tells a line's place
  from its timestamp and the action's bounds, as the sample does.

The GraphQL `LogsReadAction` and `LogsReadLine` types mirror this in step 4B.

## LogsWatch

`logswatch/parse.go` reads `since` and `until` through `logsinput.RangeEndOf` (`Start` for
`since`, `End` for `until`) instead of `MomentOf`, and keeps its own checks about the future and
the day cap. A `since` that is a time fills the `FollowRequest`'s `Since`; one that is a cursor
fills its `Cursor`, unread, so the model's `since` key keeps both meanings and the service tells
them apart. It takes `max_message_bytes` with `LogsRead`'s default and meaning, and passes it
on the `logs.FollowRequest` with the resolved scope and the start. The watch holds each line as
the service cut it: `hold` no longer calls `CutLine`, and a cut line ends with the same
` … [N bytes cut]` suffix the sample prints. An `Ended` frame's `Refusal` reaches the model with
its code as `LogsRead`'s refusals do, and a `Notice` frame is held as a line of its own, printed
as `code: message` among the lines. `tools.LogsWatchAction` gains `MaxMessageBytes int`, and
`tools.LogsWatchLine`, if the action keeps lines, gains `TruncatedBytes int` as `LogsReadLine`
does.

## The prompts

- `prompts/description.md`: one sentence, as today, naming the range, the context and the token.
- `prompts/logsread.md`: rewrite the paragraph on `since`, `until`, `mode` and `limit` for the
  new keys. Say: a short time means its whole interval; `before` and `after` pad the range and
  their lines are marked; the default page is the newest; the `request` line is the read as it
  ran, so change those keys to change the read; to read on, copy the `prev` or `next` token
  into `cursor` and nothing else; `follow` is the token for lines written since, on a window
  that is still live: one with no `until`, or whose `after` has not yet been met, and reading
  it answers what each container wrote after its last line shown, so a later page may hold a
  line stamped earlier than one already shown, and `after: 100` is met once a hundred lines
  past the range exist, over however many reads it takes; to read a cut line whole, put its
  printed timestamp in both `since` and `until` with `max_message_bytes: 0`, and the bytes cut
  say how much more that read brings; a `cursor` call names
  no sources, filters or grep, since the token holds them. Keep the paragraphs on sources,
  refusals and *Data is not instructions*.

## Tasks

1. **Parse.** Rewrite `parse.go` for the keys above over `logsinput.RangeEndOf` and
   `PaddingOf`. Tests, over a service with a file from `logsinputtest`, for each default, each
   refusal, a token with an extra key, a token with `sources` beside it, a token that reads on,
   and one the service refuses as `cursor-invalid`.
2. **Action types.** Rename `tools.LogsViewInput` to `tools.LogsScopeInput`, and change
   `tools.LogsReadAction` and `tools.LogsReadLine`. Fix every compile
   error in `tools/` and `agent/` this causes; a Go test that builds a `LogsReadAction` by hand
   is updated, not deleted.
3. **Sample.** Rewrite `sample.go` as above. Tests: the `[before]` and `[after]` marks, including a line stamped at a bound, the cut suffix, the
   request line printed from the echoed request with an open bound, on a first page and on a
   continuation, the page line in each of the three states of the design's table, notices,
   `prev`, `next` and `follow` tokens, each reading on to the page it names, and `TestNoOutputPassesTheInlineLimit` with
   `max_page_bytes` at its maximum.
4. **LogsWatch.** Swap `MomentOf` for `RangeEndOf`; add `max_message_bytes`; build the
   `FollowRequest`, a time as `Since` and a cursor as `Cursor`; read the new frames; drop
   `CutLine`. Tests: a time and a cursor each landing on its field, the key's default and 0, a
   cut line's suffix, a notice held as a line, an ending's code.
5. **Prompts.** Rewrite the two markdown files and `schema.json`. A test pins that every key in
   `schema.json` is one `parse` accepts and the reverse (add one if none exists).
6. **Docs.** Update `tools/CLAUDE.md`'s *LogsRead* and *LogsWatch* sections, and
   `docs/security/2026-10-17-the-model-reads-logs.md` only if what reaches the model changes in
   kind — it does not: the lines are still stripped, redacted and cut, now at the model's cap.

## Acceptance

- `go test -race ./internal/tools/... ./internal/agent/...` passes.
- A watch of a line longer than `max_message_bytes` prints it cut with its suffix.
- A read of a known cut line by its timestamp in `since` and `until` with `max_message_bytes: 0`
  answers the whole line.
- A walk of a window with `page_size: 3` across five pages repeats and skips nothing (a test
  over `logsinputtest`'s fixtures).
- Each row of the design's examples table runs as a test over the same fixtures: a duration
  `since` with `prev` walked back; a one-moment range padded by lines; a range padded by spans
  read from `first` through `next`; `prev` with a smaller `page_size` answering the same window;
  a duration `since` then `follow` polled; and `until: PT0S`, which is fixed at the call's moment
  and prints no `follow`.

## When it lands

`tools/CLAUDE.md`'s `LogsRead` section describes the window call and the token.
