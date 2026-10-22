# Step 4B — The wire

**Files:** `sidecar/graph/schema.graphqls`, `sidecar/graph/schema.resolvers.go`,
`sidecar/graph/util.go`; generated code in `sidecar/graph/` and `src/gql/`.
**Prerequisites:** steps 1, 2 and 3 (step 4A can be built at the same time; this step touches the
`LogsReadAction` types too, so agree on the field names in step 4A's spec and do not rename).
**Status:** Planned.

## Goal

`logsFetch` takes the window request and answers the new response. `logsFollow` takes the
follow request and streams the new frame. The generated types on both sides are regenerated,
never edited.

Read `sidecar/CLAUDE.md`'s *GraphQL via gqlgen* section and the `# --- Logs ---` parts of the
schema first.

## The schema

Replace `LogsFetchMode` and `logsFetch`'s arguments with one input. Doc strings are part of the
schema: write each in one or two plain sentences, as the file does.

```graphql
"Where a window's first page comes from."
enum LogsPageFrom { First Last }

"How far the window extends past one end of the range, to show the lines around it: a number of lines or an ISO 8601 span. Give one."
input LogsPaddingInput {
  lines: Int
  span: String
}

"""
One page. A first page gives `scope`, what is read; `since` and `until`, the range's bounds, each
including every line at that moment, null for open; `before` and `after`, its padding; and
`pageFrom`. A continuation gives `pageCursor` alone, a page's `prev`, `next` or `follow` as it
came: the cursor holds the rest. `pageSize` is 1 to 500, null or 0 for the server's default;
`maxMessageBytes` cuts each message, 0 for the server's cap; `maxPageBytes` caps the page's
messages together, 0 for the server's default. The three are read on every page.
"""
input LogsFetchInput {
  scope: LogsScopeInput
  since: Time
  until: Time
  before: LogsPaddingInput
  after: LogsPaddingInput
  pageFrom: LogsPageFrom
  "With `First`: the last line the caller has of each stream, by its `cursor`; that stream reads past it, the rest from the window's start."
  seen: [String!]
  pageCursor: String
  pageSize: Int
  maxMessageBytes: Int
  maxPageBytes: Int
}

"The request as it ran: defaults filled, caps applied, and on a continuation the scope, range and padding the page cursor holds. The scope is drawn as `LogsViewAction` draws one."
type LogsFetchRequest {
  sources: [LogsViewSource!]!
  filters: [LogsViewFilter!]!
  "The model may have written it, so draw it as text."
  grep: String!
  since: Time
  until: Time
  before: LogsPadding!
  after: LogsPadding!
  pageFrom: LogsPageFrom
  pageSize: Int!
  maxMessageBytes: Int!
  maxPageBytes: Int!
}

type LogsPadding {
  lines: Int!
  span: String!
}

"""
The page's place in the window, each a page cursor to pass back as `pageCursor`, null where there
is none. A page cursor is an opaque string that holds everything the next page needs; it never
expires. `prev` goes on toward older lines and `next` toward newer; `follow` polls a live
window's end for lines written since. A page going on from `next` or `follow` may hold a line stamped
older than one already read.
"""
type LogRecordsPlace {
  prev: String
  next: String
  follow: String
}

"Something the page wants the reader to know. `code` is one of a closed, kebab-case set the server shares with its refusals; `message` is for people."
type LogNotice {
  code: String!
  message: String!
}
```

Add to `LogRecord`:

```graphql
  "The bytes cut off `message` at the request's `maxMessageBytes`. 0 when whole."
  truncatedBytes: Int!
```

`cursor`'s doc becomes "Names this line: a fetch's `pageCursor` reads past it, and a follow's
`seen` reads this stream past it. Opaque."

`LogRecordsPage` becomes:

```graphql
type LogRecordsPage {
  request: LogsFetchRequest!
  "The first page's clock, which an open `until`'s `after` is measured from on every page."
  resolvedAt: Time!
  page: LogRecordsPlace!
  records: [LogRecord!]!
  notices: [LogNotice!]!
}
```

`notices` is always present, empty with nothing to say. It loses `before`, `after`, `anchor` and `skipped`: the edges are the three strings, the client
that sends two pages for a jump knows which line it asked for, and a skipped stream is a
`stream-skipped` notice. Delete `LogRecordsPageEdge`.

Change the query to `logsFetch(id: ClusterID!, request: LogsFetchInput!): LogRecordsPage!`.
`LogsViewInput` is renamed `LogsScopeInput`, its fields (`sources`, `filters`, `grep`) unchanged,
and is now a first page's field of the request; every other use of it, `logsFollow`'s `view` argument and the
`LogsView` tool's call among them, takes the new name. `LogsView`, `LogsViewAction` and the
`LogsViewSource` and `LogsViewFilter` types keep theirs: they are the panel and its drawn
fields.

`logsFollow` becomes `logsFollow(id: ClusterID!, request: LogsFollowInput!): LogsFollowFrame!`:

```graphql
"""
One follow. `scope` is what is followed. It starts from now, or from `since`, that moment's
lines included. `seen` is the last line the caller has of each stream, by its `cursor`: that
stream reads past it, and a stream not in `seen` reads from `since`. `cursor` is a page's
`follow`: each stream reads past where that page left it, and the scope is the cursor's, so
`scope` is omitted. `cursor` with `since` or `seen` is refused. `maxMessageBytes` cuts each
message, 0 for the server's cap.
"""
input LogsFollowInput {
  scope: LogsScopeInput
  since: Time
  seen: [String!]
  cursor: String
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
  "Why the follow ended: a code from the shared set, and the reason in the user's words. Set on `Ended`."
  ended: LogsFollowEnd
}

enum LogsFollowFrameType { Line Notice Ended }

type LogsFollowEnd {
  code: String!
  reason: String!
}
```

The `type` discriminator stays on the wire though the Go frame has none: a nested non-null
field erroring nulls its parent, so a client that detected a frame by its set field would read
an errored line as nothing. The resolver sets `type` from whichever field the Go frame carries.
`LogsWatchAction` gains `maxMessageBytes: Int!`.

`LogsReadAction` and `LogsReadLine` mirror step 4A's Go types: `mode: LogsReadMode!` becomes
`pageFrom: LogsPageFrom`; add `before`, `after: LogsPadding!`; add `notices: [LogNotice!]!`; replace
`dropped`, `before`, `after` with `olderMore`, `newerMore: Boolean!`. `LogsReadLine` gains
`truncatedBytes: Int!`. Delete `LogsReadMode` and
`LogsReadEdge`.

## The resolver

`logsFetch` in `schema.resolvers.go` maps `LogsFetchInput` onto `logs.FetchRequest` field by field
in a small `requestOf(in) (logs.FetchRequest, error)` in `util.go`, with a test. `pageCursor` passes
through unread. A span string is parsed there with the same ISO 8601 reader `logsinput` uses;
export that reader from `services/logs` if it lives in `logsinput` (the layering forbids `graph`
importing `tools/internal`). A bad span or a `lines` and `span` both set is
`KSTACK_VALIDATION_ERROR` through the mapper in `util.go`, as every domain refusal is. `logsErr`
puts the refusal's `Code` in the error's `rule` extension, where `"logs"` is today, so a client
reads `code: KSTACK_VALIDATION_ERROR, rule: cursor-invalid` and can act on the rule; the message
stays the refusal's words. A `lines` and `span` conflict found in the mapper is refused as
`conflicting-parameters` too.

The response maps back field by field: the echoed request, the three strings, the records with
`truncatedBytes`, and `notices`. `logsFollow` maps `LogsFollowInput` onto `logs.FollowRequest` in
a `followRequestOf(in)` beside `requestOf`, and each frame onto `LogsFollowFrame` with its
`type`. `LogsReadAction`'s mapping follows the Go type.

## Tasks

1. **Schema.** Edit `schema.graphqls` as above, the `LogsScopeInput` rename and the follow's
   input and frame included.
2. **Generate.** `cd sidecar && go run github.com/99designs/gqlgen generate`. Implement the
   stubs. Never edit `generated.go` or `models_gen.go`.
3. **Resolver.** `requestOf` and `followRequestOf` and their tests, including a follow from
   `since`, with `seen`, from `cursor`, and `cursor` beside either refused as
   `conflicting-parameters`; the response and frame mappings; the action mappings.
4. **Resolver test.** In the existing `logsFetch` resolver test, add a first page with context,
   a continuation from a page's `next` passed back as it came, a `lines` and `span` conflict, a
   bad span, and a `pageCursor` that does not decode, refused by the service. In the
   `logsFollow` resolver test, a follow from a page's `follow`, a `Notice` frame, and an
   `Ended` frame carrying its code.
5. **Frontend codegen.** From the root, `pnpm codegen`. `pnpm build` fails until step 5 rewrites
   `logs-client.ts`; that is expected on `wip/log-fetch-api`, and step 5 follows at once.

## Acceptance

- `go test -race ./graph/...` passes from `sidecar/`.
- `pnpm codegen` runs clean and `src/gql/` is committed.

## Verification

```sh
cd sidecar && go run github.com/99designs/gqlgen generate && go test -race ./graph/...
```

```sh
pnpm codegen
```

## When it lands

The schema's doc strings are the wire's documentation. Nothing else to fold.
