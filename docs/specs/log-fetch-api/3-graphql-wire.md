# Step 3 — The wire

**Files:** `sidecar/graph/schema.graphqls`, `sidecar/graph/schema.resolvers.go`,
`sidecar/graph/util.go`, `sidecar/gqlgen.yml`; `sidecar/internal/tools/tool.go` (the action
types the wire binds); generated code in `sidecar/graph/` and `src/gql/`.
**Prerequisites:** none outstanding. Steps 1 and 2 landed.
**Status:** Planned.

## Goal

`logsFetch` takes the window request and answers the page. `logsFollow` takes the follow request
and streams the new frame. The generated types on both sides are regenerated, never edited.

This step is what makes the sidecar build again: `graph/generated.go` still names the follow
types step 2 removed, the `logsFetch` resolver panics, and the `logsFollow` resolver calls a
signature that no longer exists. It also settles the Go action types the transcript draws, since
gqlgen binds the schema's `LogsReadAction` and `LogsWatchAction` onto them. Step 4 fills what this
step shapes.

Read first: `sidecar/CLAUDE.md`'s *GraphQL via gqlgen*; the `# --- Logs ---` section and the
`LogsReadAction`, `LogsReadLine` and `LogsWatchAction` types of the schema; the `Logs` bindings in
`gqlgen.yml`; `services/logs/README.md`, the contract; `FetchRequest`, `FetchResponse`, `Page` and
`Notice` in `fetch.go`; `FollowRequest` and `FollowFrame` in `follow.go`; `logsErr` in `util.go`.

## The span (`services/logs/iso8601`)

A new leaf package, so `graph` and the tools read a span the one way:

```go
// Parts is an ISO 8601 duration as written: each unit's count, nanoseconds for
// the seconds' fraction. Parse refuses a unit out of order or repeated, a count
// over nine digits, a fraction outside seconds, and a duration of nothing.
func Parse(s string) (Parts, error)

// Span is p as a fixed length of time, refusing years and months, which have
// none, and anything over max.
func Span(p Parts, max time.Duration) (time.Duration, error)

// Format is d as the shortest ISO 8601 duration that names it, days first:
// 36 hours is P1DT12H, 300 seconds PT5M, 1.5 seconds PT1.5S. A zero d is "",
// which is how a padding of nothing is spelled on the wire and in an action.
func Format(d time.Duration) string
```

`Parse` is `logsinput`'s regular expression and its checks, moved. The calendar arithmetic a
range end needs stays in `logsinput.MomentOf`, which reads `Parts` from here. Tests: a round
trip of `Format` through `Parse` and `Span` for each unit, `Format(0)` empty, a month refused
as a span, the package's other refusals each.

## The action types (`tools/tool.go`)

The sequence's one rename lands here, in its own commit, so step 4 can start on it:
`tools.LogsViewInput` becomes `tools.LogsScopeInput`, still an alias of `logs.Scope`. Every use in
`tools/`, `logsinput` and `agent/` follows. `LogsViewSource` and `LogsViewFilter` keep their
names: they are the panel's drawn fields.

`LogsReadAction` changes to match the window request and response:

```go
// LogsPadding is one side's padding as the service ran it: a number of lines, or
// a span as an ISO 8601 duration, normalised. At most one is set.
type LogsPadding struct {
	Lines int    `json:"lines"`
	Span  string `json:"span"`
}

// LogsNotice is something the page told the model, by code.
type LogsNotice struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LogsReadAction is log lines the model read: the scope, the window as the service
// ran it (Since and Until the range's bounds, nil for open; Before and After its
// padding; PageFrom which end the first page came from, nil on a continuation),
// ResolvedAt the first page's clock, Lines what the model read, oldest first,
// stripped, redacted and cut, OlderMore and NewerMore whether the page had a
// prev or a next, Notices what the page said, and ViewAround the view View around
// this opens, nil with no line. Every string but the scope's is cluster text.
type LogsReadAction struct {
	LogsScopeInput
	Since      *time.Time      `json:"since"`
	Until      *time.Time      `json:"until"`
	Before     LogsPadding     `json:"before"`
	After      LogsPadding     `json:"after"`
	PageFrom   *logs.PageFrom  `json:"pageFrom"`
	ResolvedAt time.Time       `json:"resolvedAt"`
	Lines      []LogsReadLine  `json:"lines"`
	OlderMore  bool            `json:"olderMore"`
	NewerMore  bool            `json:"newerMore"`
	Notices    []LogsNotice    `json:"notices"`
	ViewAround *LogsViewAction `json:"viewAround"`
}
```

`Mode`, `Dropped`, the `Before`/`After` edges and `Skipped` go, with `LogsReadMode`,
`LogsReadEdge` and their constants. `LogsReadLine` gains `TruncatedBytes int`, the bytes the
request's cap cut, which a watch's held lines carry too, since `WatchResult.Lines` is the same
type. `LogsWatchAction` gains `MaxMessageBytes int`, and its `Since` becomes `*time.Time`: nil
when the watch went on from a read's token, which names no moment.

`PageFrom` is a pointer because the schema's field is nullable: gqlgen turns a bound enum into
its string through a lookup table, and an empty value is not in it, so a non-pointer field
would send `""`, which is not a member, where the schema promises null. Any nullable enum bound
to a Go type is a pointer for the same reason.

`gqlgen.yml` drops the `LogsReadMode` and `LogsReadEdge` bindings, binds `LogsPageFrom` onto
`logs.PageFrom` member by member (`First`, `Last`), `LogsPadding` onto `tools.LogsPadding` and
`LogNotice` onto `tools.LogsNotice`. The `LogsFollowFrame` and `LogsFollowFrameType` bindings
go: they name `logs.FollowFrameType` and its three constants, which step 2 removed, so
`gqlgen generate` fails until they do. Both become generated `model` types, the frame because
it carries a `type` the Go frame does not, the enum with its new `Notice` member.

## The schema

Replace `LogsFetchMode`, `LogRecordsPageEdge`, `LogsReadMode`, `LogsReadEdge` and `logsFetch`'s
arguments with these. Doc strings are part of the schema: one or two plain sentences each, as
the file does.

```graphql
"Where a window's first page comes from."
enum LogsPageFrom { First Last }

"How far the window extends past one end of the range, to show the lines around it: a number of lines, or a span as an ISO 8601 duration such as `PT5M`, at most seven days. Give one."
input LogsPaddingInput {
  lines: Int
  span: String
}

"""
One page: a first page, which gives `scope` and the window, or a continuation, which gives
`cursor` and nothing of them. The three caps are read on every page.
"""
input LogsFetchInput {
  scope: LogsScopeInput
  "The range's bounds, each including every line at that moment. Null is open."
  since: Time
  until: Time
  "The window's padding past each bound."
  before: LogsPaddingInput
  after: LogsPaddingInput
  "Required on a first page, omitted on a continuation: there is no default."
  pageFrom: LogsPageFrom
  "The last line the caller has of each stream, by its `cursor`: that stream reads past it, and the rest read from the window's start, or from where the page left them beside a `cursor`. Refused with `Last`, with padding on a first page, beside a `prev`, and beside a cursor whose `after` is a count."
  seen: [String!]
  "A page's `prev`, `next` or `follow` as it came. It holds the scope and the window, so sending either beside it is refused."
  cursor: String
  "1 to 500; null or 0 is the server's default."
  pageSize: Int
  "Cuts each message; 0 is the server's cap."
  maxMessageBytes: Int
  "Caps the page's messages together; 0 is the server's default."
  maxPageBytes: Int
}

"One side's padding as it ran: `lines`, or `span` as an ISO 8601 duration, normalised (`PT300S` is `PT5M`). Empty `span` and 0 `lines` is none."
type LogsPadding {
  lines: Int!
  span: String!
}

"The request as it ran: defaults filled, caps applied, and on a continuation the scope, range and padding the page cursor holds."
type LogsFetchRequest {
  sources: [LogsViewSource!]!
  filters: [LogsViewFilter!]!
  "The model may have written it, so draw it as text."
  grep: String!
  since: Time
  until: Time
  before: LogsPadding!
  after: LogsPadding!
  "Null on a continuation."
  pageFrom: LogsPageFrom
  pageSize: Int!
  maxMessageBytes: Int!
  maxPageBytes: Int!
}

"""
The page's place in the window, each a page cursor to pass back as `cursor`, null where there is
none. A page cursor is an opaque string that holds everything the next page needs, and it never
expires. `prev` goes on toward older lines and `next` toward newer; `follow` polls a live window's
end for lines written since. A page going on from `next` or `follow` may hold a line stamped
older than one already read.
"""
type LogRecordsPlace {
  prev: String
  next: String
  follow: String
}

"Something the page wants the reader to know. `code` is one of a closed, kebab-case set the server shares with its refusals; `message` is for people. Cluster text."
type LogNotice {
  code: String!
  message: String!
}

type LogRecordsPage {
  request: LogsFetchRequest!
  "The first page's clock, which an open `until`'s `after` is measured from on every page."
  resolvedAt: Time!
  page: LogRecordsPlace!
  records: [LogRecord!]!
  "Never null: empty with nothing to say."
  notices: [LogNotice!]!
}
```

`LogRecord` gains `truncatedBytes: Int!`, "The bytes cut off `message` at the request's
`maxMessageBytes`. 0 when whole." Its `cursor` doc becomes "Names this line: a fetch's or a
follow's `seen` reads this stream past it. Opaque." Its `message` doc says the cut is at
`maxMessageBytes`, at most the file's own 16 KiB plus its ellipsis.

`LogsViewInput` is renamed `LogsScopeInput`, its fields unchanged; `LogsViewSourceInput` and
`LogsViewFilterInput` keep theirs. The query becomes
`logsFetch(id: ClusterID!, request: LogsFetchInput!): LogRecordsPage!`, with this doc:

```graphql
  """
  One page of a window of a view's lines on the cluster, oldest first. A first page
  names what to read and when; the next page that way is one of the page's cursors,
  sent back as it came. A refusal is a validation error whose `rule` is its code.
  """
```

The follow:

```graphql
"One follow: what to follow, and where it starts. With neither `since` nor `cursor` it starts from now."
input LogsFollowInput {
  "What is followed. Omitted beside `cursor`, which holds it."
  scope: LogsScopeInput
  "A start: that moment's lines included. Refused beside `cursor`."
  since: Time
  "The last line the caller has of each stream, by its `cursor`: that stream reads past it, and a stream not named reads from the start. Refused alone."
  seen: [String!]
  "A page's `next` or `follow`: each stream reads past where that page left it, and on past the page's window."
  cursor: String
  "Cuts each message; 0 is the server's cap."
  maxMessageBytes: Int
}

"""
One frame of a follow: a line, a notice, or the last frame, saying why the follow ended.
Following again after `Ended` would end the same way. Detect each frame by `type`, never by
which field is missing.
"""
type LogsFollowFrame {
  type: LogsFollowFrameType!
  "The line, cut at `maxMessageBytes`. Set on `Line`."
  record: LogRecord
  "Something the follow wants the reader to know. Set on `Notice`."
  notice: LogNotice
  "Why the follow ended: its code, and the reason in the user's words. Set on `Ended`."
  ended: LogNotice
}

enum LogsFollowFrameType { Line Notice Ended }
```

The subscription becomes `logsFollow(id: ClusterID!, request: LogsFollowInput!): LogsFollowFrame!`,
with this doc:

```graphql
  """
  A view's lines as they are written, from a start the request names. Lines are ordered
  per container, and across containers by arrival. A follow that ends for a reason the
  user can read sends one `Ended` frame and completes: stop subscribing. One that fails
  reports `watchFailed`; follow again from the same start, with `seen` naming the last
  line held of each stream.
  """
```

The `type` discriminator stays on the wire though the Go frame has none: a nested non-null field
erroring nulls its parent, so a client that detected a frame by its set field would read an
errored line as nothing. An ending is a `LogNotice`, its `Reason` in `message`, so a client
handles one shape for what a page says, what a follow says and why it ended.

`LogsReadAction`, `LogsReadLine` and `LogsWatchAction` mirror the Go types above: `mode` becomes
`pageFrom: LogsPageFrom` (null on a continuation); add `before`, `after: LogsPadding!`,
`olderMore`, `newerMore: Boolean!` and `notices: [LogNotice!]!`; drop `dropped`, the two edges and
`skipped`; `LogsReadLine` gains `truncatedBytes: Int!`; `LogsWatchAction` gains
`maxMessageBytes: Int!` and its `since` becomes nullable, null for a watch from a token.
`LogNotice` serves the page, the follow's notice and its ending, and the action, bound onto
`tools.LogsNotice`; the page's and the follow's are converted in the mappers.

## The resolvers

Both resolvers map by hand in `util.go`, each mapper a pure function with a test:

- `fetchRequestOf(in model.LogsFetchInput) (logs.FetchRequest, error)`: field by field,
  `cursor` passed through unread, a nil `scope` the zero `Scope`, each `span` to a
  `time.Duration` through `iso8601.Span` under `logs.MaxPaddingSpan`. **Null and zero are
  one thing on this wire**, as they are to the service: a `scope` with no sources, no filters and
  an empty `grep` is no scope, so one beside a `cursor` is a continuation, and one with any of
  the three is refused there; `lines: 0` beside a `span` is a span; `pageSize:
  0` is the default. The mapper keeps no presence the service would not read. A span it cannot read,
  or over the cap, is the mapper's one refusal, `refusal.New(CodeBadPadding, …)` with the
  reader's words; the service checks the duration again and never sees a bad one. Everything
  else, a cursor beside a scope, padding with both `lines` and `span`, is the service's
  refusal. `logsErr` already answers every refusal as `KSTACK_VALIDATION_ERROR` with the code
  in `rule`, so a client reads `rule: cursor-invalid` and can act on it.
- `fetchRequestOut(q logs.FetchRequest) *model.LogsFetchRequest`: the echoed request, each
  `Padding` to a `tools.LogsPadding` with its `Span` through `iso8601.Format`, so a padding of
  nothing is `lines: 0, span: ""` as the schema promises; `pageFrom` nil on a continuation.
- `pageOf(resp logs.FetchResponse) *model.LogRecordsPage`: the request through
  `fetchRequestOut`, the three strings, the records (bound to `logline.Record` as today, so
  `truncatedBytes` and `cursor` need nothing), and the notices.
- `followRequestOf(in model.LogsFollowInput) logs.FollowRequest`, the same way, with nothing
  to refuse.
- `frameOf(f logs.FollowFrame) *model.LogsFollowFrame`: `type` from whichever field is set,
  `ended` a `LogNotice` from the `Refusal`'s `Code` and `Reason`.

The subscription streams through `mapStream` with `frameOf` as its map and
`recordWatchFailure(ctx, st.Err())` as its teardown, exactly what `watchStream` does for a
pointer map: a bare `mapStream` would drop the stream's `Err`, and a failed follow would reach
the client as a graceful end. `TestLogsFollowReportsItsFailure` stays and pins it. If a second
resolver ever needs the same shape, make it a mapping variant of `watchStream`; one caller is
not worth one.

`graph.LogsService` becomes the two methods with their landed signatures. `logsFetch` runs the
fetch under the request's context, as every query does.

## Tasks

1. **The span.** `services/logs/iso8601` and its tests; `logsinput.MomentOf` reads through it.
   Its own commit.
2. **Action types.** `tools/tool.go` as above and the `LogsScopeInput` rename; fix every compile
   error in `tools/`, `logsinput` (`ReadLine` fills `TruncatedBytes`; `ViewAround` takes the new
   name) and `agent/`. The two tool stubs still panic in `RunShown`; nothing else changes in
   them. A Go test that builds a `LogsReadAction` by hand is updated, not deleted. Its own
   commit: step 4 starts once tasks 1 and 2 have landed.
3. **Schema and bindings.** Edit `schema.graphqls` and `gqlgen.yml` as above.
4. **Generate and implement.** `cd sidecar && go run github.com/99designs/gqlgen generate`;
   implement the stubs over the five mappers; never edit `generated.go` or `models_gen.go`.
   `go build ./...` is green from `sidecar/` at the end of this task.
5. **Tests.** The mappers: a first page with both paddings, a bad span refused `bad-padding`, a
   continuation with `seen`, an empty `scope` beside a `cursor` reaching the service as a
   continuation, and `{sources: [], grep: "error"}` beside one refused `conflicting-parameters`, `lines: 0` beside a `span` reaching it as a span, an echoed request
   with a span, one with no padding answering an
   empty `span`, one from a continuation with `pageFrom` nil, a follow from `since`, from
   `cursor`, from `cursor` with `seen`, each frame kind. The resolvers, over the fake the `logsFollow` tests use, widened with
   `Fetch`: a first page with context, a continuation whose `cursor` reaches the fake as it
   came, a refusal the fake answers as `cursor-invalid` arriving with that `rule`, a
   continuation answering `pageFrom: null`, and `logsFollow` streaming a `Line`, a `Notice` and
   an `Ended` with its code, each bound by its `type`, and still reporting a failed stream as
   `watchFailed`.
6. **Frontend documents and codegen.** `pnpm codegen` validates every operation under `src/`
   against the schema, so the documents that name what this step removes change here, and the
   code around them in step 5. Rewrite the `LogsFollow` document in `src/lib/logs-client.ts`
   and add the `LogsFetch` document beside it, selecting what step 5 says each selects; in
   `src/lib/chats.tsx`'s messages document, select the action's new fields (`pageFrom`,
   `before { lines span }`, `after { lines span }`, `olderMore`, `newerMore`,
   `notices { code message }`, `truncatedBytes` on each line and on `background.matched`,
   `maxMessageBytes` on `logsWatch`) in place of `mode`, `dropped`, `before`, `after` and
   `skipped`. Then `pnpm codegen`, and commit `src/gql/`. `pnpm build` fails until step 5
   rewrites the code those documents feed; that is expected on `wip/log-fetch-api`, and the
   pull request's summary says so.

## Acceptance

- `go build ./...` and `go test -race ./graph/... ./internal/tools/... ./internal/agent/...`
  pass from `sidecar/`; `make vet-go` and `make lint-go` pass.
- `pnpm codegen` runs clean over the rewritten documents and `src/gql/` is committed.

## Verification

```sh
cd sidecar && go run github.com/99designs/gqlgen generate && go build ./... && go test -race ./graph/... ./internal/tools/... ./internal/agent/...
```

```sh
pnpm codegen
```

## When it lands

The schema's doc strings are the wire's documentation. `services/logs/CLAUDE.md`'s package
table gains the `iso8601` row. `tools/CLAUDE.md`'s *LogsView* paragraph names `LogsScopeInput`.
Nothing else to fold.
