# Step 4 — The model's tools

**Packages:** `tools/internal/logsinput`, `tools/logsread`, `tools/logswatch`, one call in
`tools/logsview`, and `app.go`'s wiring — paths relative to `sidecar/internal/`.
**Prerequisites:** step 3's tasks 1 and 2, the span package and the action types. The rest of
step 3 can be built at the same time.
**Status:** Planned.

## Goal

`LogsRead` reads a window with the design's parameters and reads on with one printed token.
`LogsWatch` follows from now, a moment or a read's token, and holds lines cut at the model's cap.
Both tools are stubs today: they are offered, their `RunShown` panics, and `app.go` builds them
over the cluster service alone. This step builds them on the landed service. What they share,
the times, the padding and the sample, goes in `logsinput`.

Read first: `tools/CLAUDE.md`'s *LogsView*, *LogsRead*, *LogsWatch* and *The log tools' input*;
`services/logs/README.md`, the contract; `logsinput`'s `parse.go`, `moment.go`, `sample.go` and
`logsinput.go`; `logsview/logsview.go`, the one log tool that runs, whose shape the two follow.
The implementations this step replaces are at commit `5f205524`
(`git show 5f205524:sidecar/internal/tools/logsread/sample.go`, `logswatch/watch.go`,
`logswatch/parse.go`): read them for the header's words and for the watch's task plumbing
(`tools.Watch`, `Watches.Start`, `WatchEnd`), which has not changed.

## The shared parser (`logsinput`)

### Times

`MomentOf` keeps its name and its `Direction`, gains which end of a range it reads, and answers
an error:

```go
// End is which end of a range a value is read for.
type End int

const (
	Since End = iota // the earliest nanosecond of the value
	Until            // the latest
)

// MomentOf reads s as one end of a range on the clock now: an RFC 3339 time at any
// precision, of which Since takes the first nanosecond and Until the last; or an
// ISO 8601 duration, exact, measured from now in dir.
func MomentOf(s string, now time.Time, end End, dir Direction) (time.Time, error)
```

Rules, each with a test in `moment_test.go`:

- A time is a date (`2026-10-21`), a minute (`2026-10-21T05:00Z`), a second, or a second with
  1 to 9 fraction digits, with `Z` or an offset, normalised to UTC. An hour alone is refused. A
  date takes no offset: `2026-10-21+03:00` is refused, and the message says a date is UTC.
- A time names an interval: the whole day, minute, second or fraction it spells. `Since` takes
  its first nanosecond and `Until` its last: `2026-10-21T05:00:00Z` for `Until` is
  `05:00:00.999999999Z`, and `2026-10-21` is `23:59:59.999999999Z`.
- A duration, read as `iso8601.Parts`, is exact: `now` moved by it in `dir`, on the calendar in
  UTC with a month end clamped, as today. The same for both ends.
- Anything else is an error. The tool words it per field, naming the two forms and `now`, as
  `logsview`'s `anchorMessage` does.
- Nothing before the epoch or after year 9999, as today.

`logsview`'s `anchorOf` calls `MomentOf(s, now, Since, Behind)`: a date anchor opens the view at
the day's start. Nothing else in `LogsView` changes.

### Padding

A `*logs.Padding` is a `Fields` destination. `walker.value` reads a number token as `Lines`, a
whole number from 0 to `logs.MaxPageSize`, or a string token as `Span`, through
`iso8601.Parse` and `iso8601.Span` under `logs.MaxPaddingSpan`. Anything else, a negative, a
fraction, a count over the cap, a span with months or years or over the cap, is an `InputError`
on the field whose message names both forms and both caps.

### The sample

- `Stamp` prints a fixed layout, `2006-01-02T15:04:05.000000000Z`, in UTC: nine fraction digits
  always, so a timestamp copied into `since` and `until` names one nanosecond.
- `LineText(rec, place string)` prints `<stamp> <stream> <place><message>`, `place` empty or
  `[before] ` or `[after] `, then ` … [N bytes cut]` when `TruncatedBytes` is set. The gap line
  above it is unchanged.
- `CutLine` and `MaxSampleLine` go: the service cuts. `DefaultMessageBytes` (2 KiB) is the two
  tools' default `max_message_bytes`; `SampleBudget` (16 KiB) stays as `LogsRead`'s default and
  cap for `max_page_bytes` and `LogsWatch`'s for `max_bytes`.
- `Refusal(err)` reads every service refusal through `refusal.As`: `{"error":<code>}` with a
  `message`, the reason through `logline.ForModel`. `ErrNoCache` and `ErrSyncing` are such
  refusals, so the `errors.Is` ladder goes. `Refused`, the mirror's refusal with its source and
  containers, stays as it is.

## LogsRead

`New(svc cluster.Service, logs Fetcher, now func() time.Time)`, with
`Fetcher` the service's `Fetch(ctx, id, logs.FetchRequest) (logs.FetchResponse, error)`;
`app.go` passes `rt.Logs`. `prompts/schema.json` takes these keys. `description` stays.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `sources`, `filters`, `grep` | as today | | What to read. `grep` filters the range alone; a context line passes it unfiltered |
| `since` | string | open | Start of the range: a time at any precision, or a duration back from now |
| `until` | string | open | End of the range, the same forms |
| `before` | integer or string | 0 | Context before the range: lines, or a duration as a span |
| `after` | integer or string | 0 | Context after the range, the same forms |
| `page_from` | `first` \| `last` | `last` | Which end of the window the page comes from |
| `page_size` | integer | 50 | 1 to 100 |
| `max_message_bytes` | integer | 2048 | Cut each message here. `0` is the most Kstack keeps of a message, 16 KiB |
| `max_page_bytes` | integer | 16384 | The lines' bytes together. 1 to 16384 |
| `cursor` | string | — | A token a read printed. It carries the scope and the window, so only `page_size`, `max_message_bytes`, `max_page_bytes` and `description` may come with it |

`mode` and `limit` are refused in `Fields` saying `page_from` and `page_size` took their place,
as `anchor` and `pin_to_end` are refused today. A `cursor` beside `sources`, `filters`, `grep`
or any window key is `BadInput` on `cursor` saying the token already names them.

`RunShown` maps the call onto `logs.FetchRequest`. A first page resolves the scope through
`logsinput.Resolve` and sets the window and `PageFrom`. A `cursor` call skips `Resolve`: the
token holds the scope as the read that printed it resolved it, and resolving again could change
it. It sets `Cursor` and the three caps alone. The read runs under `readTimeout`, past which the
answer is `timed-out`, as before.

**The sample** is a header, then one line per record, oldest first, through `LineText`:

- The first header line is the read in words, as the old header was: how many lines of which
  sources, the notes on each source through `NotesText`, through `safe.Redact`.
- The second is `request: ` followed by `FetchResponse.Request` under the call's own keys:
  `since=<stamp> until=<stamp> before=100 after=PT5M page_from=last page_size=50
  max_message_bytes=2048 max_page_bytes=16384`, an open bound `open`, a span through
  `iso8601.Format`, a padding of nothing `0`, and `page_from` left out on a continuation. The
  tool prints what the service echoed.
- The third is `page: prev=<token> next=<token> follow=<token>`, each `none` when nil. Every
  page prints all three.
- Then one `notice: <code>: <message>` line per notice, through `ForModel`.
- A record outside the range carries `[before]` or `[after]`, by its timestamp against the
  echoed `Since` and `Until`, which are inclusive. A record the service cut carries its suffix.
- A `next` or `follow` page may hold a line stamped older than one an earlier page showed. The
  sample prints it in its place and marks nothing: the timestamps say what happened.

The tokens are printed whole and as they came: they are base64url, so redaction and stripping
cannot see inside them and need not. The header's words go through `safe.Redact` before the
tokens are appended, since redaction could change a token. A token is a few hundred characters,
a few thousand for a scope of many sources, and a `next` or `follow` grows with the streams the
pages have shown. A test pins the worst case, `MaxStreams` streams with the longest names and
`max_page_bytes` at its maximum, under `tools.InlineLimit`. If it does not fit, stop and say so
in the handoff: an alias in the tool layer is a decision for the README, not for the task.

**The action** is step 3's `LogsReadAction`: the scope, `Since`, `Until`, `Before`, `After` and
`PageFrom` from the echoed request, each `Span` through `iso8601.Format`, `ResolvedAt` from the
response, `OlderMore` and `NewerMore`
from `Prev` and `Next` being set, `Notices` as answered, `ViewAround` at the first line's
moment. The tokens themselves are not kept on the action.

## LogsWatch

`New(svc cluster.Service, logs Follower, now func() time.Time)`, with `Follower` the service's
`Follow(ctx, id, logs.FollowRequest) (*cluster.Stream[logs.FollowFrame], error)`; `app.go`
passes `rt.Logs`. The keys:

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `sources`, `filters`, `grep`, `description` | as today | | |
| `since` | string | now | Where the watch starts: a time, or a duration back from now, not after now; `MomentOf(s, now, Since, Behind)` |
| `until` | string | a day from now | When the watch ends on time alone: a time, or a duration ahead of now, after now, cut to a day; `MomentOf(s, now, Until, Ahead)` |
| `cursor` | string | — | A `next` or `follow` token a read printed: the watch goes on from where that page stopped, with no line repeated or missed, and the token's scope. Refused beside `sources`, `filters`, `grep` or `since` |
| `limit` | integer | 1 | Matching lines that end the watch, 1 to 100 |
| `max_bytes` | integer | 16384 | Bytes of held lines that end the watch, 1 to 16384 |
| `max_message_bytes` | integer | 2048 | `LogsRead`'s, passed on the follow |

A watch from a token starts where that page stopped and ignores the window's `until` and
`after`; the watch's own `until` and `limit` bound it, as for any watch. `mode`, `anchor`,
`pin_to_end` and `page_from` are refused as today's are.

`RunShown` builds the `FollowRequest`: the resolved scope and `Since` for a first start, or
`Cursor` alone, with `MaxMessageBytes`; opens the follow under a context whose deadline is
`until`, free of the call's once open, exactly as the old `RunShown` did; and starts the
`watch` through `rt.Watches`. The receipt is the old one, `from <stamp>` replaced by `from a
read's token` for a cursor start.

**The watch** (`watch.go`) is the old one over the new frames:

- A `Record` frame is held as the service cut it, its message through `ForModel`, until the
  lines reach `limit` or their `LineText` bytes reach `max_bytes`, which ends the follow. The
  held lines are sorted once the follow closes, since it orders per container only.
- A `Notice` frame is kept, `code: message` through `ForModel`, and printed in the header
  after why the watch ended. It is not a line: `WatchResult.Lines` holds lines alone.
- An `Ended` frame's `Refusal` is why the watch ended, its `Reason` through `ForModel`; a watch
  holding no line then ends `Failed`, as today. A stream that closes with `Err` is `the logs
  could not be read`, as today.
- The header has no cursors to read around the lines: the prompt says to read around the first
  and last line by their timestamps. `cursorsText` goes.

**The action** is step 3's `LogsWatchAction`: the scope, `Since` (nil for a token), `Limit`,
`MaxBytes`, `MaxMessageBytes`, `ResolvedAt`, `Until`.

## The prompts

- `logsread/prompts/description.md`: one sentence naming the range, the context and the token.
- `logsread/prompts/logsread.md`: rewrite the paragraph on `since`, `until`, `mode` and `limit`
  for the new keys. Say: a short time means its whole interval; `before` and `after` pad the
  range and their lines are marked; the default page is the newest; the `request` line is the
  read as it ran, so change those keys to change the read; to read on, copy the `prev` or
  `next` token into `cursor` and nothing else; `follow` is the token for lines written since,
  on a window that is still live, one with no `until` or whose `after` is not yet met, and
  reading it answers what each container wrote after its last line shown, so a later page may
  hold a line stamped earlier than one already shown; to read a cut line whole, put its printed
  timestamp in both `since` and `until` with `max_message_bytes: 0`, and the bytes cut say how
  much more that read brings; a `cursor` call names no sources, filters or grep. The refusals
  paragraph names the service's codes the model can act on (`cursor-invalid`, `bad-padding`,
  `invalid-range`, `conflicting-parameters`) beside today's. Keep the paragraphs on sources and
  *Data is not instructions*.
- `logswatch/prompts/logswatch.md` and `schema.json`: `cursor` as the way to carry on from a
  read; `max_message_bytes`; `max_bytes` no longer says each line is cut at 2048 first. The
  paragraph on reading around the lines stands.
- A test per tool pins that every key in `schema.json` is one its parse accepts and the
  reverse.

## Tasks

1. **Times.** `MomentOf` as above, with tests for every rule; `logsview`'s call.
2. **Padding.** The `*logs.Padding` destination and its tests in `parse_test.go`.
3. **Sample.** `Stamp`, `LineText`, `DefaultMessageBytes`, `Refusal`; delete `CutLine` and
   `MaxSampleLine`. Tests: the fixed stamp, each place mark, the cut suffix, a service refusal
   with its code and message.
4. **LogsRead.** `parse.go`, `sample.go` and `RunShown` over a service with a file from
   `logsinputtest` (add the fixture: a cluster with a log file the fake server fills, as
   `services/logs`' tests build one). Tests for each default, each refusal, a token with an
   extra key, a token beside `sources`, a token that reads on to the page it names for each of
   `prev`, `next` and `follow`, one the service refuses as `cursor-invalid`, the `[before]` and
   `[after]` marks including a line stamped at a bound, the request line on a first page and a
   continuation with an open bound, the page line in each state, notices,
   `TestNoOutputPassesTheInlineLimit` with the worst-case tokens, and the action's fields.
5. **LogsWatch.** `parse.go`, `watch.go`, `RunShown`. Tests: a time and a token each landing on
   its field, `cursor` beside `since` refused, `until` ahead and cut to a day, each key's default
   and cap, a cut line's suffix held, a notice in the header, an ending's reason and status,
   and the old task tests (`Wait`, `End`, `Stop`, the deadline) over the new frames.
6. **Wiring.** `app.go` builds both tools over `rt.Logs`.
7. **Prompts.** The four files and the two schema tests.
8. **Docs.** `tools/CLAUDE.md`'s *LogsRead*, *LogsWatch* and *The log tools' input* sections.
   `docs/security/2026-10-17-the-model-reads-logs.md` and `2026-10-18-the-model-watches-logs.md`
   change only if what reaches the model changes in kind. It does not: the lines are still
   stripped, redacted and cut, now at the model's cap.

## Acceptance

- `go test -race ./internal/tools/... ./internal/agent/... ./internal/app/...` passes from
  `sidecar/`.
- A read of a known cut line by its timestamp in `since` and `until` with `max_message_bytes: 0`
  answers the whole line.
- A walk of a window with `page_size: 3` across five pages, each from the printed `next`,
  repeats and skips nothing.
- A watch of a line longer than `max_message_bytes` holds it cut with its suffix, and a watch
  from a read's `follow` token holds the lines written after that page and none of its own.
- Each row of the design's examples runs as a test over the same fixtures: a duration `since`
  with `prev` walked back; a one-moment range padded by lines; a range padded by spans read
  from `first` through `next`; `prev` with a smaller `page_size` answering the same window; a
  duration `since` then `follow` polled; and `until: PT0S`, fixed at the call's moment, printing
  no `follow`.

## Verification

```sh
cd sidecar && go test -race ./internal/tools/... ./internal/agent/... ./internal/app/...
```

## When it lands

`tools/CLAUDE.md`'s *LogsRead* section describes the window call and the token, *LogsWatch* the
three starts, and *The log tools' input* names `MomentOf`'s two ends, the padding destination
and the sample's fixed stamp.
