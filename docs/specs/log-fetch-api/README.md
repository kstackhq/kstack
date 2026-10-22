# Log fetch API

One way to read logs, used by two callers: the model's `LogsRead` tool and the log viewer's
wire. Today each has its own shape. `LogsRead` takes `since`, `until`, `mode` and `limit`; the
viewer's `logsFetch` takes a mode, a cursor and a moment. Neither can ask for context around a
range, cap a message's size, or recover a message that was cut.

This sequence gives the log service one query and one follow, and binds both callers to them.

## What a caller gets

A caller asks about a **range** of time and gets back a **window**: the range, padded with
context lines before and after it, paged with cursors.

- `since` and `until` bound the range. Each is a moment that includes every line stamped at it.
  Either can be left open.
- `before` and `after` pad the window. Each is a number of lines or a span of time.
- The first page comes from the start or the end of the window.
- A page holds at most so many lines and so many bytes. A message longer than the message cap is
  cut and says how many bytes it lost. The caller can read it whole with a narrower query, and
  knows how much that read brings.
- A page names its place in the window as three page cursors: `prev` toward older lines, `next`
  toward newer, `follow` to poll a live window's end. A continuation passes one back and
  nothing else, and gets the next page of the same window that way, no line repeated.
- The response echoes the request as it ran.
- A follow is the same API's other verb. It takes the same scope and message cap, starts from a
  line cursor or a page's `follow`, and streams the same records and notices.

The full design, as written on 2026-10-21, is the source for the field names and semantics.
Where this sequence departs from it, the decisions below say so and why.

## Vocabulary

- **Scope** is what is read: `sources` (pods or workloads, their containers, `previous`),
  `filters` (node, region, zone, OS, architecture) and `grep` (a regular expression a line's
  message must match). It is today's `logs.View`, renamed (decision 17). `filters` apply first,
  then `grep`, to every line of the window, context included.
- **Window** is when: the range's two bounds and its two paddings.
- **Page** is one read of the window: at most `page_size` lines and `max_page_bytes` bytes, each
  message cut at `max_message_bytes`.
- **Page cursor** is an opaque string the sidecar hands out with a page. It holds the scope,
  the window, which way the next page goes on, and where it goes on from, so a continuation
  sends it alone. The sidecar keeps nothing, and a cursor never expires. A line that reaches the
  sidecar late, stamped among lines already read, is on the next `next` or `follow` page.
- **Line cursor** names one line: its moment, its namespace, pod and container, the container's
  runtime id, and its place among that container's lines at that moment. Every record carries
  its own. It is a base64url, versioned JSON token as a page cursor is: opaque, safe to print,
  and never expires. A caller sends line cursors only as `seen`, and page cursors only as a page
  cursor; no argument takes both. Step 1's *The line cursor* defines it.
- **Notice** is something a page or a follow wants the caller to know, as a code and a message.
- **Refusal** is why a request was not served, as a code and a message. Notices and refusals
  share one code vocabulary.

## Decisions

These depart from the design or fill gaps in it. Each is final for this sequence; record them in
the ADR when the work lands.

1. **A line's cursor stays a position; a page's cursor is a stateless string.** The design
   pins the resolved range inside the cursor, and so does this sequence, but a line's cursor
   stays what [the cursor ADR](../../adr/2026-10-17-a-log-cursor-is-a-position.md) says: a
   position and nothing else. A page's place is a page cursor: the `Scope`, the `Window`, which
   way it reads, where the page ended, and, when it reads toward newer lines, one position per
   stream that had a line on the page, encoded as base64url JSON with a version. A continuation
   sends it alone. The service keeps nothing, so `Fetch` is a function of its request.
2. **A bound is a moment, and the moment is inside the range.** The design lets a bound name a
   line. Here `since` and `until` are times only, and a time includes every line at it. So
   `since` and `until` set to one nanosecond address the lines at that nanosecond: one line
   almost always, a handful when a fast logger shares a stamp. A continuation is still exclusive:
   the next page starts past what was read.
3. **A count of context is a distance the cursor carries.** `before: 100` is the hundred lines
   before the range, whichever they turn out to be; `after: 100` the hundred past it, and while
   fewer exist the window's end is open and the window is live. Each cursor holds how many
   context lines lie between its position and each bound, so a page moving away from a bound
   knows how many it may still deliver, a page moving toward one re-delivers what lies between,
   and a continuation reads nothing to find the edge. The one read is a counted first page
   finding where it starts. A late line can move a counted edge by one; nothing else moves it.
4. **The first page comes from `first` or `last`, as the design says.** The viewer's
   jump-to-time, today's `Around` mode, is two first pages the app's client sends together: a
   window since the moment read first, and a window until the moment read last. The library
   drops the line both hold, and the client knows the anchor. The service has no `At` page and
   no anchor.
5. **The polled `follow` is the live end's page cursor.** The design's live window is not
   built. A live window is one whose end is open, lies after now, or whose counted `after` is
   short of its count. Its newest page carries `follow`, and a continuation from it answers what each
   stream wrote since its own last line, merged by time. One position could not serve this:
   streams' clocks differ, so a line stamped earlier than the newest line read can arrive after
   it, and "newer than the newest" would skip it. `next` and `follow` go on per stream for the
   same reason, and a stream the pages have not seen goes on from the window's start, since
   nothing of it has been shown. `prev` reads history, and goes on from one position. The viewer keeps following
   through `logsFollow` and the model through `LogsWatch`; the poll is for a reader that holds
   no stream.
6. **No refusal for a large range.** The service cuts a page at its read limits and says more
   remains, as it does today. The design's "narrow the range" error is not added.
7. **No cursor expires.** A position into evicted data refills from the kubelet or crosses a
   lost gap, as today. A page cursor holds no file-local value, only moments, names, runtime ids
   and ordinals, so it is exact across a cache clear and across the cluster's file being made
   anew. The design's `cursor_expired` is not adopted. A cursor carries a version, and one of
   another version is `cursor-invalid` with a message saying to read again without it; this
   sequence reads one version and migrates nothing, since nothing has shipped.
8. **A line is addressed by its timestamp, and no line id is printed.** A printed id per line
   would cost most of the sample's budget, and a nanosecond timestamp names a line as well as
   the viewer's jump-to-time does. To read a cut line whole, the model puts the line's printed
   timestamp in both `since` and `until` with `max_message_bytes: 0`. The design's line ids are
   not built, and today's `EncodeCursor` and `DecodeCursor` go.
9. **The service takes full-precision moments.** The rule that a short timestamp means its whole
   interval (`2026-10-21T05:00:00Z` is the whole second) lives in the tools' shared parser. The
   viewer sends nanosecond moments already. The sample prints every timestamp with nine fraction
   digits, so a timestamp copied into `since` and `until` names one nanosecond. A date with an
   offset (`2026-10-21+03:00`) is refused with a message saying a date is UTC. The design reads
   it as that day in that offset, but one shape read two ways is one too many for a model to get
   right.
10. **`grep` filters the whole window.** Context lines pass through the same grep as the range,
    so a count of context counts matching lines. Unfiltered context, as `grep -C` gives, is a
    later flag, noted in `docs/TODO.md` when this lands.
11. **The model's token is the service's page cursor, as it came.** A continuation is `cursor`
    alone, as the design says. The tool prints the cursor and passes it back; it wraps nothing
    and stores nothing. Today's `EncodeCursor` and `DecodeCursor` go.
12. **The viewer's library contract changes as little as it can.** `src/lib/log-viewer` keeps
    its `Client` with `Head`, `Tail` and `Around`, and its cursors stay opaque strings. The
    app's `logs-client.ts` maps that onto the new wire: a library edge cursor is the service's
    page cursor as it came. The library learns the new line field, a `Notice` follow frame, and
    `seen` on a `Head`, the lines it holds per stream, which is how it goes on after a follow
    now that a follow sends no checkpoint.
13. **One stateless cursor for every caller.** A `prev` cursor is the scope and the window, a few
    hundred characters, or a few thousand for a scope of many sources. A `next` or `follow` adds
    an entry per stream that had a line on the page, so it grows with the page's streams: a
    handful in practice. The JSON is deflated before it is base64'd, since a scope's names
    repeat, so a cursor of a handful of streams is a few hundred characters, and only one at
    `MaxStreams` with long names may still not fit the model's 16 KiB header. The service
    writes nothing on a read, keeps no table, sweeps nothing, and a cursor outlives a cache
    clear. The cursor holds the scope that resolved its window, so the service can check
    that a continuation pages that scope, and a cursor can be inflated and read when debugging. If
    the model's token does not fit its header, the fix is an alias in the tool layer, where the
    cost is, not state in the service; step 4A decides when that is needed.
14. **One code vocabulary, no problem-details envelope.** The design answers errors as RFC 9457
    problem details with a `code` shared with the notices. The envelope does not fit: GraphQL
    answers an error inside a 200 with `extensions.code`, and a tool refuses with one JSON line,
    so `type`, `title`, `status` and `detail` have nowhere to go. The shared vocabulary is
    adopted at the service: `logs.Refusal` carries a `Code` from the same closed block as the
    notice codes, the wire puts it in the validation error's `rule` extension, and the tools
    print it as it came. No layer derives a code from an error's identity. The codes are
    kebab-case, as the four the model's log tools print today are: `not-mirrored`, `no-nodes`,
    `too-many` and `no-container`. The design's `range_too_large`, `unknown_id` and `cursor_expired`
    are not adopted (decisions 6, 7 and 8). `notices` is always present, empty with nothing to say.
15. **The tool's caps are the tool's.** The design's defaults and maxima (`page_size` 500 of
    5000, `max_message_bytes` 4096, `max_page_bytes` 1 MiB) are for a client reading over HTTP.
    The model reads through a 16 KiB inline budget, so `LogsRead` keeps 50 of 100 lines, 2 KiB a
    message and 16 KiB a page. The service's own caps are step 1's: `MaxPageSize` 500,
    `MaxMessageBytesCap` 16 KiB plus the ellipsis, `MaxPageBytesCap` 1 MiB.
16. **A record says how many bytes the request cut.** `Record` gains `TruncatedBytes`, the
    bytes the request's message cap removed. The request's cut leaves no mark in the text; what
    to draw is the reader's choice. The file's own cut leaves the ellipsis the file wrote, and
    the default cap is one ellipsis wider than the file's, so such a line arrives whole. It is the bytes cut, not the message's size, because that is the number
    the model weighs before reading the line whole. The file caps a message at 16 KiB when the
    line is written; this sequence takes the file's line as the whole message and neither
    records nor reports that cut. Making readers aware of it, and reading such a line whole
    safely, are rows in `docs/TODO.md`. A line's place in the window is not a field: the bounds
    are inclusive and the response echoes them, so a caller compares timestamps.
17. **What is read is a scope, and a view is the panel.** `logs.View` becomes `logs.Scope`, with
    `ScopeSource` and `ScopeFilter`, and the wire's `LogsViewInput` becomes `LogsScopeInput`.
    The one word had two meanings: the struct that says which streams and lines a read covers,
    and the panel the user looks at (`LogsView`, `LogsViewAction`). The panel keeps the word.
    The struct stays a type, embedded in `FetchRequest` beside the `Window`, because `Follow`,
    the `LogsView` action, `Check` and `resolve` all take one and none takes a window.
18. **A skipped stream is a notice.** Today's `Skipped` lines become `stream-skipped` notices,
    one per stream, so the page has one list of things to tell the caller.
19. **The follow is redesigned on the shared types, not adapted.** `Scope`, `Record`, `Notice`,
    `Refusal`, the code block, `Padding` and the line cursor are the package's vocabulary. The
    follow (step 2) takes and answers them as they are: a `FollowRequest` of a `Scope`, a start
    and a message cap, and `FollowFrame`s carrying a `Record`, a `Notice` or a `Refusal`. The fetch's own types are `FetchRequest`, `FetchResponse`, `Window`, `PageFrom`,
    `Page` and the page cursor, named for the method where a bare noun would read two ways.
    The engine under the follow does not change. Its surface changes in this sequence because
    steps 4B and 5 rewrite the wire and the client once; a follow patched to compile now would
    have them rewritten twice.
20. **A follow starts from a moment, the lines seen, or a page's `follow`.** `Follow` takes
    `Since`, a time; `Seen`, the last line the caller has of each stream, each stream reading
    past its own; or `Cursor`, a page's `Follow`, each stream starting where the page left it,
    exactly as a poll of that page reads. The viewer's `follow(cursor)` sends its edge cursor
    through, and a reconnect sends the last line it holds of each stream as `Seen`. `Since` is
    inclusive, as a window's is, and has no exclusive twin: the lines past one a caller saw
    are what `Seen` asks for.
    **A follow sends no checkpoint.** A checkpoint named the oldest watermark across the scope,
    so one quiet stream made every reconnect replay every busy stream from where the quiet one
    stopped. The caller already holds what a reconnect needs, its own last line per stream, so
    the follow sends nothing to stand in for it.
21. **A follow takes the message cap.** `MaxMessageBytes` on the follow request cuts and counts
    in the service as a page's does. `LogsWatch` stops cutting at 2 KiB on its own, and
    `logsinput.CutLine` goes.

## Open

- **Whether the viewer needs `FetchResponse.Request`.** Step 5 does not select it. It would let
  the viewer draw the caps the server applied.

## The pieces

| Step | Spec | What it builds |
| --- | --- | --- |
| 1 | [1-service.md](1-service.md) | The log service's one query: window, context, caps, page cursor, notices |
| 2 | [2-follow.md](2-follow.md) | The follow on the same types: scope, a start that is a line cursor or a page's `follow`, the message cap, frames with notices and a coded end |
| 3 | [3-logsinput.md](3-logsinput.md) | The tools' shared parser: precise times, context values, the printed token |
| 4A | [4A-logsread-tool.md](4A-logsread-tool.md) | `LogsRead` on the new query, its prompt, its action; `LogsWatch` on the new follow |
| 4B | [4B-graphql-wire.md](4B-graphql-wire.md) | `logsFetch` and `logsFollow` on the new types, generated code on both sides |
| 5 | [5-webview.md](5-webview.md) | The app's logs client on the new wire, fetch and follow; the viewer and the transcript draw the new fields |

## Order

Build in step order. Step 2 needs step 1's cursor and codes. Step 3 needs step 1's types. Steps
4A and 4B need steps 1, 2 and 3, and can be built at the same time. Step 5 needs step 4B's
generated types and step 4A's action shape.

```
1 → 2 → 3 → 4A ─┐
           └ 4B ─┴→ 5
```

Each step is one pull request into `wip/log-fetch-api`, except step 1, which is two: its task 1
alone, then the rest. CI runs on every pull request and is red above `services/logs` from step
1's first pull request until step 4B lands; each pull request in between says so in its summary.
A step's spec names its tasks in the order to do them; do one task at a time.

## When it lands

- Fold what is now true into `sidecar/internal/services/logs/CLAUDE.md`,
  `sidecar/internal/tools/CLAUDE.md`, `src/CLAUDE.md` and `src/lib/log-viewer/README.md`.
- Write one ADR, *A log read is a window over a range*, recording the decisions above. Link it
  from the three `CLAUDE.md` files. It supersedes the cursor ADR, whose decision names the
  tab-separated spelling and rejects base64 JSON: flip that ADR's status to superseded and repoint
  its links in the same commit, as `CLAUDE.md` asks. The new ADR keeps what survives, that a line
  cursor is one position and the page cursor a separate thing beside it, and records the
  respelling: one versioned JSON form for both cursors, so a line cursor sits inside a page cursor
  as it is.
- Remove the `docs/TODO.md` row *Revisit an opaque cursor for the log viewer's wire*: a page's
  place is the service's opaque id on every wire, and a line's cursor stays a position.
- Add a `docs/TODO.md` row *Bound a log scope's size, then cap the page cursor*: a scope's
  sources and filter values have no size limit, `MaxStreams` bounds the streams the mirror
  resolves and not the cached streams of gone pods read beside them, so a page cursor, which
  carries the scope and an entry per container passed, has no bound either and the decoder caps
  nothing. A serialized scope budget and a total container limit, both checked before a page is
  served, let the decoder refuse an oversized token without ever refusing one the service wrote.
- Add a `docs/TODO.md` row *Bound the viewer's window and re-fetch from its edge page cursors*:
  a long follow grows the viewer's lines without end. Dropping lines at an end and keeping that
  end's page cursor makes scrolling back an ordinary continuation, at the cost of the library's
  stable-key rule across a drop.
- Delete this directory.
