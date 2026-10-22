# Step 1 — The log service's one query

**Package:** `services/logs` (paths relative to `sidecar/internal/`).
**Prerequisites:** none.
**Status:** Planned.

## Goal

`Service.Fetch` answers one kind of query: a window over a range, paged by position. The old
modes go away as a public shape. `Head` and `Tail` survive as `PageFrom`; `Around` is two pages
the viewer's client joins (step 5).

This step changes what `Fetch` takes and answers, and little of how a page is read. `fill` and
`merge` stay as they are, over the direction type `FetchMode` is today, unexported as `direction`
with the same two values. `page` takes a page cursor in place of its one near bound and trims a
counted side after the merge. `walk` changes in one place: it reports the first and last rows it
read, kept or rejected by the grep, and the moment its read stopped at, from which the page
cursor's entries are computed against the page's cut. Read `services/logs/CLAUDE.md` and
`fetch.go` first.

A request has three parts:

- The **scope**: what is read. Today's `View` (`view.go`: `Sources`, `Filters`, `Grep`),
  renamed. It stays a type because `Follow`, the `LogsView` action, `Check` and `resolve` all
  take one. The word "view" is left to the panel the user looks at.
- The **window**: when. The range's bounds and its padding.
- The **page**: how much, and from where.

The scope and the window are embedded in the request, so `q.Sources` and `q.Since` read flat. A
first page sets them; every page of the window shares them.

A page's place in the window is three **page cursors**, `Prev`, `Next` and `Follow`. Each is an
opaque string that holds the scope, the window, which way the next page reads and where it goes
on from. A continuation sends the cursor and nothing else about the window. The service keeps
nothing: a page cursor is a function of the page that handed it out, and `Fetch` is a function
of its request. A cursor never expires and survives a cache clear, since nothing in it names
the file.

## The types

Add these to `fetch.go`, replacing `FetchQuery`, `Page` and `PageEdge`. The names
are final; the wire and the tools bind to them.

```go
// PageFrom is where a window's first page comes from.
type PageFrom string

const (
	PageFromFirst PageFrom = "first" // the window's oldest lines
	PageFromLast  PageFrom = "last"  // the window's newest lines
)

// Padding is how far a window extends past one end of its range, to show the
// lines around it: a number of lines or a span of time. At most one is set; both
// zero means none.
type Padding struct {
	Lines int
	Span  time.Duration
}

// Window is when a read looks: Since and Until, the range's bounds, each a moment
// that includes every line at it, nil for open; and Before and After, its padding.
type Window struct {
	Since, Until  *time.Time
	Before, After Padding
}

// resolvedWindow is a Window as the first page pinned it: the Window;
// ResolvedAt, the first page's clock, which an open Until's After is measured
// from on every page, so the window's end does not slide from page to page; and
// Start, the window's start as an inclusive position once it is known: zero for
// an open Since with no Before, the moment a time or a span names, and the
// farthest line a counted Before's read found. A stream no page has seen reads
// from it. Only a page cursor holds one.
type resolvedWindow struct {
	Window
	ResolvedAt time.Time
	Start      lineCursor
}

// FetchRequest is one page. A first page sets Scope, what is read, Window, when, and
// PageFrom; with PageFromFirst it may set Seen, the last line the caller has of each
// stream, so that stream reads past its line and every other from the window's
// start. A continuation sets PageCursor alone, one of a page's Prev, Next or
// Follow as it came: the cursor holds the scope and the window. PageSize is the
// lines a page holds, 1 to MaxPageSize, 0 for DefaultPageSize. MaxMessageBytes
// cuts each message, 0 for the service's own cap. MaxPageBytes caps the sum of a
// page's messages, 0 for MaxPageBytesCap. The three are read on every page.
type FetchRequest struct {
	Scope
	Window
	Seen       []string
	PageFrom   PageFrom
	PageCursor *string

	PageSize        int
	MaxMessageBytes int
	MaxPageBytes    int
}

// Page is the page's place in the window, each a page cursor, nil where there is
// none: Prev goes on toward older lines, Next toward newer, and Follow polls a
// live window's end for lines written since.
type Page struct {
	Prev   *string
	Next   *string
	Follow *string
}

// FetchResponse is one page of the window, oldest first. Request is the request as it
// ran: defaults filled, caps applied, and for a continuation the Scope and Window
// the page cursor holds. ResolvedAt is the first page's clock. Notices is never
// nil: empty with nothing to say.
type FetchResponse struct {
	Request    FetchRequest
	ResolvedAt time.Time
	Page       Page
	Records    []Record
	Notices    []Notice
}

// Code is one of the block below: why a request was refused, or what a page or a
// follow wants its caller to know. The wire and the tools pass it through unchanged.
type Code string

// Notice is something this page wants the caller to know: a Code, and a Message
// for people. It is about this page alone; a continuation carries none forward,
// and raises its own.
type Notice struct {
	Code    Code
	Message string
}
```

`Fetch` becomes `Fetch(ctx, id cluster.ClusterID, q FetchRequest) (FetchResponse, error)`. `id`
is the cluster whose log file and connection the read uses, as every cluster-scoped call here
takes it.

`Record` (`record.go`) gains one field, and keeps `Cursor()`, the line cursor naming it:

```go
	// TruncatedBytes is the bytes a request's MaxMessageBytes cut off Message, 0
	// when whole. The file's line is taken as the whole message.
	TruncatedBytes int
```

The file caps a message when the line is written. This sequence neither records nor reports
that; it is a row in `docs/TODO.md`.

**Constants.** `MaxLimit` becomes `MaxPageSize`, still 500. Add `DefaultPageSize = 100`,
`MaxPageBytesCap = 1 << 20`, `MaxMessageBytesCap = maxMessage + len("…")` (16 KiB plus the
ellipsis `clean.go` writes, so a line the file cut arrives whole at the default cap and reports no
cut) and `MaxPaddingSpan = 7 * 24 * time.Hour`, the most a span of padding may be, which the
tools' parser reads from here. A `MaxMessageBytes` above the cap is clamped to it, and a
`MaxPageBytes` above `MaxPageBytesCap` is clamped to it; a `Padding` past its limit is refused,
not clamped, since it changes what the window means. A `PageSize` of 0 is `DefaultPageSize`; a
negative `PageSize`, `MaxMessageBytes` or `MaxPageBytes` is refused as `bad-page`. Export
`maxStreams` as `MaxStreams`.

## Codes

`Refusal` (`sources.go`) becomes `Refusal{Code Code; Reason string}`. `Reason` stays the words a
person reads. `Code` is one of a closed set the wire and the tools pass through unchanged, so no
layer derives a code from an error's identity. Every `Refusal` the package makes names one, a
`Notice` names one through the same type, and the two share the block below.

Notices and refusals share one `const` block in `fetch.go`. The codes are kebab-case, as the
four codes the model's log tools print today are. The table names every refusal the package
makes today, by its variable or the words it carries, so none is left without a code. The
constructor in task 1 is what enforces it; a test that no `Refusal` leaves `Fetch` or `Follow`
with an empty `Code` is the backstop.

| Code | Carried by | When |
| --- | --- | --- |
| `cursor-invalid` | refusal | a `PageCursor` that is not a page cursor this version of the service wrote: it does not decode, its `v` is another version, or its content fails a check; a `Seen` entry that is not a line cursor (`errBadCursor`) |
| `conflicting-parameters` | refusal | `PageFrom` with `PageCursor` or neither; a continuation with a scope, window or `Seen`; `Seen` with `PageFromLast` or beside a `Padding`; a `Padding` with both `Lines` and `Span`; today's `Around` and mode refusals in `fetch.go` |
| `invalid-range` | refusal | `Since` after `Until` |
| `bad-grep` | refusal | a `Grep` that does not compile, or one over `MaxGrep` |
| `bad-page` | refusal | `PageSize` outside 0 to `MaxPageSize`, or a negative `MaxMessageBytes` or `MaxPageBytes` |
| `bad-padding` | refusal | a `Padding` with `Lines` outside 0 to `MaxPageSize`, or a `Span` outside 0 to `MaxPaddingSpan` |
| `bad-scope` | refusal | a filter field or a source kind the service cannot read (`sources.go`) |
| `not-mirrored`, `no-nodes`, `too-many` | refusal | as the service refuses a scope today (`Unmirrored`, `NoNodes`, `ErrTooMany`); moved here from `logsinput`'s own block. `too-many` also carries `errCut`: the cache answered more streams than the service reads at once |
| `no-container` | refusal | raised by the tools' scope check (step 3), never by the service; its constant lives here so every code is in one block |
| `no-cache` | refusal | `ErrNoCache` |
| `syncing` | refusal | `ErrSyncing` |
| `revoked` | refusal | `ErrRevoked` |
| `replaced` | refusal | `errReplaced`: the connection was replaced while the page was read. The one refusal a caller sends again as it came |
| `unauthorized` | refusal | the cluster refused to show the log (`backend.go`, and `follow.go`'s copy) |
| `finished` | refusal | every container of the scope stopped (`errStopped`), or every source was a Pod whose pod is gone (`follow.go`) |
| `read-failed` | refusal | no container of the scope could be read, or the lines one way could not be read (`merge.go`, `fetch.go`) |
| `too-loud` | refusal | `loudSecond`: more than a mebibyte of log in one second. It embeds `Refusal`, so it needs a code, but it never refuses a page: `answer` skips the stream and cuts the page at the second, so the code reaches a caller inside a `stream-skipped` notice's message alone |
| `empty-padding` | notice | `Before` was asked with `Since` open, or `After` with `Until` open on the page that reaches the window's end, the one that sets no `Next`, with no line on it stamped at or past `ResolvedAt` |
| `padding-short` | notice | the context read before the range found fewer lines than `Lines` asked for and was not cut; the window starts at the farthest line found, or the log's edge with none |
| `page-byte-limit-exceeded` | notice | the first line of the page alone was larger than `MaxPageBytes` |
| `stream-skipped` | notice | a stream of the scope the page could not read; one per stream, its message what `Skipped` held today |

The design's `range_too_large`, `unknown_id` and `cursor_expired` are not adopted (README
decisions 6, 7 and 8).

## The line cursor

A line cursor names one line: its moment, its namespace, pod and container names, the
container's runtime id, and its ordinal, its index among that container's lines at that
moment. The runtime id is what tells a line of a restarted container from a line of the one
before with the same names, and the ordinal is what tells two lines one container wrote in the
same nanosecond apart. Every `Record` carries its own as `Cursor()`, and the wire and the tools
hand it to callers as the record's `cursor`.

The types under it are `cursor.go`'s `position` and `instance`, reshaped so that a page
cursor's `seen` can key on the container's name and hold the rest. The name is `StreamKey`
without `Previous`: `Previous` says whether a run is the current one, which flips when the
container restarts, so it is no part of a line's identity and stays on `Record.Stream` for
display alone.

```go
// containerName is a container by name, which a restart does not change.
type containerName struct {
	Namespace, Pod, Container string
}

// runPlace is where a line sits within its container's lines: the runtime id
// the kubelet gave the run, so a restarted container's lines are told from the
// run before, and the line's moment and ordinal among the run's lines at that
// moment. A page cursor's seen holds one per container.
type runPlace struct {
	ContainerID string
	Timestamp   time.Time
	Ordinal     int
}

// lineCursor is a line's place in the scope's order: its container, and its
// place within it. Every line has one and no two share one, so a page can end
// at any line and the next can start just past it. One with no ContainerID is
// a moment, which stands before, or after, every line of that moment at once,
// a form only a page cursor holds; one with a zero Timestamp names nothing.
type lineCursor struct {
	containerName
	runPlace
}
```

`lineCursorOf(rec)` builds one from `rec.Stream` and `rec.ContainerID`; `Record` keeps its own
shape, since `Stream` is on the wire.

Positions order by `Timestamp`, then by namespace, pod and container, then by `Ordinal`, then by
`ContainerID` (`compareLineCursors`). The ordinal goes before the id on purpose: the id is the
resolve's, which lags a restart, so a page can end on a line keyed with the old id that the next
page keys with the new one, and with the id deciding first the new run's later lines at that
moment would all sort before the cursor and be lost. A moment, with or without a stream, compares
by `Timestamp` alone. This order is what rule 10 means by the position's tiebreaker: lines stamped
in one nanosecond across streams come out the same way on every read.

### The line cursor's encoding

A line cursor is the struct as JSON with short keys and a version field first, base64url
without padding. It is short, so it is not deflated as a page cursor is.

```json
{"v":1,"ns":"<namespace>","pod":"<pod>","c":"<container>","id":"<runtime id>","t":"<RFC 3339, nanoseconds>","n":0}
```

`lineCursorVersion = 1`. A moment, the form only a page cursor holds, is `{"v":1,"t":"<time>"}`, the
version and the time alone, or with the container's fields when it is one container's.
`encodeLineCursor(c) string` and `decodeLineCursor(s) (lineCursor, error)` are the pair. A decode
fails as `cursor-invalid` when the base64, the JSON or the version is wrong, when a name is empty,
over 253 bytes, or holds a character outside lower-case letters, digits, `-` and `.` (`validName`,
beside `validContainerID`), when `id` fails `validContainerID`, when `t` is zero, when `n` is
negative or `math.MaxInt`, which the bound arithmetic in `streamBound` steps and uses as its
sentinel, when the name fields are present in part, or when `id` and `n` are not both present or
both absent. A name and `t` with no `id` and `n` is a container's moment, a form only a page
cursor's `seen` holds (*A stream's reach is its last row short of the cut*). A cursor of another
version says to read again for one from this version. A line cursor has one spelling: the encoder
writes the keys in the order above, so two cursors of one line are one string, and the viewer's
dedup by cursor holds.

Both cursors are opaque, by construction and by contract. A caller passes a line cursor back as
it came and never takes it apart: as a `Seen` entry on a first page or a follow (step 2), and
inside a page cursor as every position there. It is never a `PageCursor`: each argument takes
one kind of cursor, and each kind has its own parser. It is safe to print as it is, since
base64url has no control characters; what it names reaches a reader only through a record,
which is redacted and stripped as every record is. In prose, "a line cursor" and "a page
cursor" are the strings; `lineCursor` and `pageCursor` are their decoded forms and live only
inside the package, and `encode…` and `decode…` are the only crossings.

A line cursor never expires, for the same reason a page cursor does not: nothing in it names
the file. Lines of one container reach the file in the order it wrote them, so a line's ordinal
at its moment is the same after a cache clear and the file's refill.

A line cursor is a line, and which way to read from it belongs to the use. A `Seen` entry, a page
cursor's `seen` entry and `at` are read past it, exclusive, the cursor's way. `Start` is read from
it, inclusive. The spec says which at each use.

The moment form stays inside the package: a page cursor's `at` and `Start` are spelled with it
when they name a moment rather than a line. From a caller it is refused: a moment as `Seen` is
`cursor-invalid`, since a caller that wants a moment has `Since`. `LineCursor`, the exported
constructor for a caller spelling its own, has no caller and goes.

## The page cursor

`pagecursor.go` holds the cursor's type and its encoding, built from `cursor.go`'s `lineCursor`
and `runPlace`. Nothing outside the package sees the type.

```go
// pageCursor is which way the pages go on, and where from. The embedded Scope
// and resolvedWindow are the first page's, echoed on every continuation; the
// scope is resolved again on every read. newer is the way this cursor reads.
// seen is where each stream goes on from, exclusive: the last line of it the
// pages passed that way, one entry per stream, the stream read off the entry;
// the same list a caller sends as a request's Seen. A stream with no entry goes
// on from at, exclusive, a line or a moment; with at zero, from the window's
// Start, since nothing of that stream has been shown. Only a newer cursor
// carries entries, and only one going on from a page read the other way carries
// both entries and at. before and after are distances, for a counted Padding:
// the context lines that lie between Since and this cursor's position, and
// between it and Until; 0 for a position inside the range. A side whose edge is
// not yet a position is paged by its count alone.
type pageCursor struct {
	Scope
	resolvedWindow

	newer         bool
	seen          map[containerName]runPlace
	at            lineCursor
	before, after int
}
```

**Why a list per stream, and why `at` alone the other way.** Lines of one stream reach the file in
the order the container wrote them, so a stream's last line passed is an exact place to go on
from, whatever its clock or its tailer did. Across streams nothing holds: a stream whose clock
runs behind or whose tailer was slow lands a line behind the newest line read, and "newer than the
newest" would skip it for good. So going newer, each stream goes on from its own line, and a
stream the pages have not seen goes on from the window's start, where nothing of it has been
shown. Going older is history, which does not move, so one position is exact and the cursor
carries no entry.

**Why `seen` is keyed by the container's name and not the run.** A record's `ContainerID` is the
resolve's, which lags a restart: a page can end on a line of the old run that the next page's
resolve names with the new id. Keyed by the run, that entry would match nothing and the stream
would be read again from the floor. Keyed by name, the entry matches, and the full order, time,
then names, then ordinal, then id, puts the new run's lines past it, since one run of a name is
live at a time and runs of one name do not overlap. The id stays in the entry's value for the
tiebreak. The key leaves `Previous` out for the same reason: the file reader marks the old run
`Previous` once the new one is current, so the old run's lines change stream between two pages
while their cursors, which carry no `Previous`, do not.

**What `at` is.** `at` is the floor for a stream with no entry: the oldest line the pages have
shown, below which every line is `Prev`'s. It is set where a newer read goes on from a page read
older, carried as it came by every newer edge after that, and zero only when the pages started
from the window's start, where a stream with no entry reads from `Start`. A quiet stream earns
no entry until it logs, so the floor is what keeps a later poll from reading its history as new.

### The encoding

A page cursor is the struct as JSON, deflated (`compress/flate`), base64url without padding.
The JSON has short keys and a version field first:

```json
{"v":1,"s":{…scope…},"w":{…window…,"resolvedAt":"<time>","start":{…line cursor…}},"n":true,"seen":[{…line cursor…},…],"at":{…line cursor…},"b":0,"a":0}
```

`pageCursorVersion = 1`. A cursor whose `v` is another number is refused as `cursor-invalid`,
with a message saying to read again without it. The version exists so a later shape can be told
from this one; this sequence reads one version and migrates nothing.

Each position is the line cursor's own JSON object, the one `encodeLineCursor` encodes, placed in
the page cursor's JSON as it is, so nothing is base64 inside base64 and a line has one spelling; a
zero `at` is omitted, and a cursor with no entry omits `seen`. On the wire `seen` is a list, since
an entry's stream is read off the entry and a key would name it twice; in the struct it is a map
from `containerName` to `runPlace`, each entry split into its key and its value, built by one loop
over the list in which a later entry for the same stream stands. It is the request's `Seen` as the
service keeps it, read the same way, and a first page with `Seen` is a cursor whose `seen` is that
list. The encoder writes the list sorted by name, namespace, pod and container, never in the map's
own order, so one page has one cursor string. `Scope` is spelled by its JSON tags, as the wire
spells it. Nothing in a cursor is file-local: a position is a moment, the namespace, pod and
container names, the container's runtime id and the line's ordinal at its moment. The file's
`stream_id`, chunk ids and the cache id never enter it. So a cursor is exact across a cache clear,
and across the cluster's file being made anew.

`encodePageCursor(c) string` and `decodePageCursor(s) (pageCursor, error)` are the pair. A decoded
cursor is checked as a parsed request is. Its shape is `cursor-invalid` when it fails: the base64,
the JSON, the version, each position as `decodeLineCursor` checks one, no entry on an older
cursor, a count outside 0 to its `Lines` or set for a span, a `start` set for an open `Since` with
no count, and the window's own checks. The inflate reads through a limit of
`maxPageCursorInflate = 16 << 20`, a bound on the decompression alone, far above any cursor the
service writes. The decoder caps neither the encoded form nor the entries:
a scope's sources and filter values are not bounded today, and `MaxStreams` bounds the streams the
mirror resolves, not the cached streams of gone pods the file reader adds beside them, so a page
can pass lines of more than `MaxStreams` containers and a cap the decoder enforced could refuse a
cursor the service itself wrote. Bounding the scope and the containers a page may name, and then
the cursor, is a `docs/TODO.md` row (README, *When it lands*). Its scope goes through `Check` and
`resolve` as a first page's does and keeps their codes: a cursor whose kind is no longer mirrored
is `not-mirrored`, not `cursor-invalid`. A cursor comes from the viewer or the model, and the
model's output is attacker-influenced text, so a cursor is checked all the same as a first page's
request. A cursor is not signed: the only client is the host process, and nothing a cursor names
is beyond what a first page may ask for.

A cursor is printed as it is, never redacted or stripped. It holds the scope's names and the
grep, which are cluster and model text, but base64url has no control characters and no caller
draws a cursor as text: the viewer passes it back, and the model copies it. What a cursor names
reaches a reader only through a page, which is redacted and stripped as every page is.

**Size.** As JSON, a `Prev` is the scope, the window and a position: a few hundred bytes for a
scope of one source, and as large as the scope's sources and filters make it, which nothing
bounds today. A `Next` or `Follow` adds one entry per stream the pages have passed a line of,
about 200 bytes for ordinary names and over 500 for the longest, so it grows with the window's
streams: a handful in practice, near 20 KB at `MaxStreams` with ordinary names, and near 50 KB
with the names at their longest. The deflate shrinks that several-fold, since a scope's entries
repeat their namespace, their pods' prefixes and their timestamps' leading digits: a cursor of a
handful of streams is a few hundred characters of base64, and one at `MaxStreams` a few KB with
ordinary names and more with the longest. Step 4A measures the worst case against the model's
16 KiB header, prints `prev` and `next` before `follow`, and decides there what to do with a
token that does not fit (README decision 13). JSON, not a binary form, because a cursor can be
inflated and read when debugging.

### The window's edges on every read

`resolveWindow` runs for a first page and for every continuation, from the `resolvedWindow`:
the request's `Window` with `ResolvedAt` set to now on a first page, or the cursor's. A time
bound costs nothing to resolve, and so does a span: it widens the bound by that much time, and
an open `Until`'s span is measured from `ResolvedAt`, so it is the same moment on every page.

A count is a distance until its edge is found, and a position after. The cursor carries
`before`, how many context lines lie between `Since` and its position, and `after`, how many
between its position and `Until`, each 0 for a position inside the range. While a side's edge
is not yet a position, a page moving away from its bound may deliver at most `Lines − count`
lines past it, and its outgoing cursor adds what it delivered; a page moving toward the bound
delivers the lines between and subtracts them, with nothing to check, since they are inside
the window already. So a continuation reads nothing to know where the context ends, and
turning back re-delivers the context as rule 10 asks.

The start becomes a position, `resolvedWindow.Start`, as soon as a page knows it: a counted
`PageFromFirst` from its read, the farthest line found, and an empty page as `Since`, since nothing
lay before it. A stream no page has seen reads from `Start`, which is why it has to be in the
cursor: a `Next` under a counted `Before` would otherwise have nowhere to start such a stream that
is neither outside the window nor short of it. With `Start` set, that side is paged as a time
bound is, its count the plain distance, and a `Prev` stops at `Start`. The end never needs this: a
stream with no entry only reads newer, and a counted side's far bound stays open until its edge is
a position, with the trim below keeping the page within its count.

The one read a count costs is on a first page whose `PageFrom` edge is counted, since that is
where the page starts: `PageFromFirst` with a counted `Before` reads `Lines` lines back from
`Since`, as `around` reads a half today, and starts at the farthest line found; `PageFromLast`
with a counted `After` reads forward from `Until` the same. The read is a second `pageQuery` in
the same `s.page` call as the range's, as `around`'s halves are today, so it shares the range's
resolve and claim and `fill`'s seams join the two reads at the bound. `resolveWindow` computes
bounds and reads nothing.

**Two reads, one page.** The call answers two merges, the context's and the range's, and `Fetch`
joins them into one before anything else looks at them, as `around` joins its halves today. The
records are the context's then the range's, and the page keeps the first `PageSize` in the order it
reads, from its starting edge: under a counted `Before` on a `PageFromFirst` the oldest `PageSize` lines
of context then range, so a context that fills the page alone leaves the range's lines for `Next`;
under a counted `After` on a `PageFromLast` the newest `PageSize`. So a `PageFromLast` whose `Lines` of
context fill the page alone fills its range read and keeps none of it: the fill is spent, and the
lines wait for `Prev`. That is correct and not worth avoiding; a keep that leans on the reads' sizes
would tie the page to the fill. The records are answered oldest first whichever way the page read,
as every page's are. Each stream's last row in the join is the range read's, or the context read's
first row, the one nearest the bound, when the range read read none. Its cut is not the lookup's:
`mergePage` cuts a read at its limit whenever a line lies past it, and the lookup's limit is
`Lines`, so a lookup that found its context whole is cut at the farthest line found though nothing
is missing. That cut is the window's edge, `Start`, and sets no edge of the page. `joinReads` tells
the cases apart by the merge alone: a stop is a moment, a limit cut is a line, and the lookup found
its context whole when it returned `Lines` records. `answer` also sets a line as the cut when a
stream was skipped, which looks like a limit cut; with fewer than `Lines` records that is the log's
edge, rule 2's skipped-stream case, and `Start` is the farthest line found as there. A stop in the
lookup, a loud second's edge or an empty window's start, lies the lookup's way, which is the
opposite of the page's: it is the edge behind, rule 2's cut case, and never the page's cut ahead. A
`PageFromFirst` whose lookup found line 9 and stopped at moment 8 delivers 9 to 11, its `Prev` reads
older from 8, and its `Next` goes on from 11; a `PageFromLast` whose lookup past `Until` stopped at a
moment has its `Next` read newer from that moment and its `Prev` go on from the page's oldest line
the same way. The page's cut ahead is the range read's cut, or the page's last line when the keep
ended the page inside the context. The join is one `merge`, and the edges are computed from it as
from any page's: each stream's reach against that cut in the page's direction, so a `PageFromFirst`
whose context lookup read back to line 1 and delivered lines 1 to 3 hands out an entry at 3, not 1,
and one whose lookup found lines 8 and 9 whole under `Before` 2 and delivered 8 to 11 sets no `Next`
from the lookup's limit.

**The scope is resolved again on every read** too, from the `Scope` the cursor holds: pods that
came since are read, pods that went are gone, and each `seen` entry is matched to a resolved
stream by its `containerName`, whichever run of it the resolve names.

### Reading on from a cursor

`page`'s query takes a page cursor in place of its one near bound. A stream's key is `streamBound`
of its `seen` entry when it has one, else of `at`, exclusive, read the cursor's way; with `at`
zero, `Start`, inclusive. The key never lies outside the window: an entry or `at` short of the
window's near edge is clipped to that edge, inclusive, so a `Seen` line older than `Start` reads
from `Start` and the page never crosses the window's edge (rule 6). No clip applies while that
side's edge is still a distance, since the edge is not yet known: under a counted `Before` whose
lookup stopped, `at` is the stop, older than `Since`, and the context between is still to read.
The far bound is the window's edge ahead: a moment for a time bound or a span, open for a live
end, and `Start` for a counted side once its edge is a position. While a counted side's edge is
still a distance the far bound is open, since the walk and the filler stop at a far bound and the
context lies past the range's; the read is bounded by `PageSize` as every page is, since the merge
reads no line past the page's last, and a backward read off the log's end is what a `PageFromLast`
page reads today. A counted side is then trimmed after the merge: the page keeps at most `Lines −
count` lines past the range's bound and drops the rest. Range lines sort before context lines in
either direction, so the trim only takes context off the page's far end. The dropped lines are
outside the window, so the edge there is the window's edge, not a cut, and it sets no `Next` or
`Prev`. An entry whose stream left the scope is dropped. A stream that joined the scope since has
no entry and reads as any stream without one.

### `Seen` on a first page

`Seen` is the fetch's half of what the follow's `Seen` is (step 2, rule 2): the last line the
caller has of each stream, as a record's `Cursor()` came. It goes with `PageFromFirst` alone. The page
reads as a `newer` cursor would whose entries are the `Seen` lines, each at its stream, and whose
`at` is zero: a named stream reads past its line, or from `Start` when its line is older than
`Start`, and every other stream from `Start`. An entry that is not a line cursor, or names a
moment and not a line, is `cursor-invalid`; of two for one stream the later in the list stands;
one whose stream is outside the scope is dropped. `Seen` beside `PageCursor`, `PageFromLast` or a
`Padding` is `conflicting-parameters`: a counted `Before` reads its context in a second read back
from `Since` that no entry bounds, so a named stream's lines between the context's start and its
line would come again. This is what the viewer's `Head` after a follow needs: a window from the
follow's start, with no padding, with the lines it holds named, which reads the rest once and
nothing again.

### The edges after a page

Call the way the page read `D`: newer for `PageFromFirst`, `Next` and `Follow`; older for
`PageFromLast` and `Prev`. Call the other way `D'`. `Scope` and `Window` are the cursor's, or the
request's for a first page. "Passed" means delivered or rejected by the grep: a line the merge
cut or the page cap dropped is not passed, so no edge reaches past a cut or a drop. Each edge's
counts are the context lines between its position and each bound: the incoming counts, plus
the context lines this page delivered moving away from a bound, minus those it delivered
moving toward one, counting only lines between the incoming position and the edge's. A
straggler delivered from behind the incoming position lies outside that span and changes
nothing. The result is clamped to 0 through `Lines`, the backstop for the by-one case README
decision 3 admits, so no edge is handed out that the decoder refuses.

- **The edge ahead, facing `D`.** When `D` is newer: `seen` is the incoming entries, each stream
  the page passed a line of replaced by its last line passed, and `at` is the incoming `at` as it
  came, zero on a first page, or the cut when a first page's counted read was cut (rule 2). When
  `D` is older: no entry, and `at` is the farthest line the page passed, or the cut when the merge
  was cut past every line passed, or the incoming `at` when it passed none and was not cut. A line
  lies beyond this edge when a record was dropped or the merge was cut, or when `D` is newer and
  the window is live (rule 7).
- **The edge behind, facing `D'`.** When `D'` is older: no entry, and `at` is the page's oldest
  line. When `D'` is newer: `seen` is each stream with a line on the page at its newest line
  there, and `at` is the farthest line the page passed in `D`, the page's oldest, so a stream with
  no entry reads past where the page stopped and nothing the page showed is repeated. A line lies
  beyond this edge when the page was a continuation. A first page has none behind, except one
  whose context lookup stopped short of its count (rule 2): the edge the lookup's way, `Prev`
  under a counted `Before` and `Next` under a counted `After`, reads on from the stop, with that
  side's count the lines found, so `Lines − found` remain.
- **A page that delivers nothing** has the edge ahead the rules above give it: a page that passed
  lines the grep rejected moves their entries all the same, so a poll over rejected lines does not
  read them again; one that was cut moves to the cut, as the reach rule below says; and one that
  passed none and was not cut hands the incoming cursor back as it came. It has none behind, since
  the caller holds the edge it came from. An empty first page's `Follow`, when the window is live,
  is a newer cursor with no entry and a zero `at`: every stream from `Start`, which the empty page
  sets to `Since` under a counted `Before`.

**A stream's reach is its last row short of the cut.** `walk` reports the first and last rows it
read, kept or rejected by the grep, and the moment its read stopped at when it stopped at one.
`edges` computes each stream's reach from them against the page's cut, in the page's direction,
taking the first of these that is not past the cut: its stop, the moment the stream's own read
stopped at, a loud second's edge or an empty window's start (`streamFill.stop`), which lies past
every row it read; its last row; its last kept record. With none, the incoming entry stands. A stop
or a row past the cut counts for nothing, since the page was cut short of it by another stream and
the rows between are not passed, and the stop is met again on the next page. Falling back to a kept
record reads the rejected rows between it and the cut again on the next page, which is exact and
cheap; the last row is what keeps a poll over rejected lines from reading them again. Two positions
a stream, never a list of its rows: a grep that matches nothing walks the stream's whole budget. The
cut is the merge's, or the last line delivered when a limit ended the page, so the same rule serves
a page cut by its limit, by a stop, or by the two-read join's keep. The moment is what makes the
next page go on: a stream stopped at a loud second has its lines of that second left out, as today,
and an entry at its last row before the second would read into the second and stop there again on
every page. An entry that is a moment is spelled with the stream and `t` and no `id` or `n`, and
compares by `Timestamp` alone, so the stream reads past the whole second. The cut in `at` is the
nearest stop, as `trimToStops` finds it, which an older edge takes when no line lies past it.

`Follow` after a `PageFromLast` first page is the behind edge. Each stream on the page reads past its
newest line there, and a stream with none reads past the page's oldest line. So a tailer that
was slow to deliver a stream's lines within the page's span delivers them on the poll. The one
line a poll cannot reach is one stamped older than the page's oldest line from a stream the
page did not show; that line is history, and `Prev` reads it.

**The tests pin** rules 10 and 12 over this layout, both ways, with a flip in each direction (a
`Prev` chain then `Next`, a `Next` chain then `Prev`), the poll, that an older cursor carries no
entry, and that a stream with no entry reads from `Start` on a `Next`, under a counted
`Before` too, and past the page's oldest line on a `Follow` after `PageFromLast`.

## Rules

Write these as the doc comment on `Fetch`. Each has a test.

1. **A bound includes its moment.** `Since` and `Until` each include every line at that
   nanosecond, so a range of one nanosecond is the lines stamped with it. `Since` later than
   `Until` is refused as `invalid-range`.
2. **Context is measured from the range's bounds, not from its lines.** A span widens the bound by
   that much time, on every page. A count is a distance the cursor carries: `Lines` lines of the
   merged scope past the bound, whichever they turn out to be, with `before` and `after` the
   context lines between the cursor's position and each bound. A page moving away from a bound
   delivers at most `Lines − count` lines past it and adds them, while that side's edge is not yet
   a position; one moving toward a bound delivers the lines between and subtracts them. Once a
   side's edge is known it is a position in the cursor (`Start`), and the side is paged as a time
   bound is. The first page alone reads to find where it starts when its `PageFrom` edge is
   counted, as `around` reads a half today, and sets that side's count to the lines found. That
   read coming back short says one of three things. The log's edge: `Start` is the farthest line
   found, the count is the lines found, and the page carries `padding-short`. A stop, at a loud
   second's edge or an empty window's start (`streamFill.stop`): the page starts at the stop with
   the lines found as the count, `Start` unknown, `at` the stop so a stream with no entry reads
   past it, and the edge the lookup's way, `Prev` under `Before` and `Next` under `After`,
   delivers the rest. A skipped stream: as the log's edge, with the read's `stream-skipped` beside
   `padding-short`. A counted `After` short of `Lines` on a newer cursor leaves the window live
   (rule 7).
3. **Context past an open bound is measured from the log's edge.** An open `Since` has no
   context before it, and the page carries `empty-padding`. An open `Until` has its `After` measured
   from `ResolvedAt`, the first page's clock, carried in every cursor: a span sets the window's end
   at that moment ahead, the same on every page, and a count counts the lines stamped at or past it.
   The page that reaches the window's end, the one that sets no `Next`, carries `empty-padding` when
   no line on it is stamped at or past `ResolvedAt`; a page with a `Next` says nothing, since the
   lines may lie ahead. With no `After`, the window's end is open and the window is live for as long
   as it is polled. The echoed `Until` stays nil either way; `ResolvedAt` beside it is where the
   range ends and the padding starts.
4. **A record's place in the window is its timestamp.** Lines stamped before `Since` are the
   padding before the range; lines after `Until`, or at or past `ResolvedAt` when `Until` is open
   and `After` is set, are the padding after. With `Until` open and no `After` the range has no end,
   and nothing is padding after. The bounds are inclusive, so a caller compares a timestamp against
   the echoed request's bounds. The page carries no label for it.
5. **The first page.** `PageFromFirst` reads oldest first from the window's start. `PageFromLast` reads
   newest first from its end.
6. **A continuation.** `PageCursor` says which way the page reads, where it goes on from, and
   the window. A `Prev` reads newest first toward the window's start; a `Next` or `Follow` reads
   oldest first toward its end. The page never crosses the window's edge. Refused as
   `conflicting-parameters`: both `PageFrom` and `PageCursor`, neither, or a continuation whose
   `Scope`, `Window` or `Seen` is not zero. The cursor holds no `PageSize`, `MaxMessageBytes` or
   `MaxPageBytes`: a continuation names its own, checked and defaulted as a first page's are, so a
   0 is the default on every page and never what the page before asked. A `PageCursor` that is not
   a page cursor this service wrote is `cursor-invalid`; a cursor whose scope the service now
   refuses keeps the scope's code. A cursor with an entry for a stream that left the scope is not
   refused; the entry is dropped. The scope is resolved again on every page.
7. **The page's place.** `Prev` is set when a line is known to be older than the page inside the
   window, `Next` when one is known to be newer. A window is **live** while its end is open, lies
   after now, or a newer cursor's `after` is under its `Lines`. A live window's page with nothing
   newer sets `Follow`, since a continuation from it is the poll. A page never sets both `Next` and
   `Follow`. A cursor never expires: it holds everything a continuation needs, and the service keeps
   nothing.
8. **Message cap.** A message longer than `MaxMessageBytes` is cut at a UTF-8 character boundary,
   and `TruncatedBytes` is the bytes removed. The cut adds no ellipsis or other mark: the record
   says it was cut, and what to draw is the reader's choice. It cuts the stored line, the one
   `keepColor` wrote, so the viewer keeps its colours, and it never splits an SGR sequence: a
   sequence that does not fit whole is cut off with what follows it, as `clean` cuts. It happens
   on the merged page, in `answer`. The file keeps the whole line.
9. **Page cap.** Records are kept in the order the page reads, from its starting edge, while the
   sum of their messages, after the message cap, fits `MaxPageBytes`: a `PageFromLast` or `Prev` keeps
   its newest lines and drops its oldest, since those lie nearest the edge it reads toward. The
   first record kept is the one at the starting edge, always; if it alone is over, the page
   carries `page-byte-limit-exceeded`. A record dropped by the cap sets `Prev` or `Next`,
   whichever way it lay, and no edge passes the last kept line. This runs after the message cap
   and before the edges are computed.
10. **Paging is exact.** Along one direction no line is repeated, and no line the file holds when
    a page is read is skipped, whatever `PageSize`, `MaxMessageBytes` or `MaxPageBytes` the
    continuation names. The one exception is a loud second: a stream stopped at one has the rest
    of that second left out and reads on past it, as today, though the file may hold part of the
    second; the page names the stream in a `stream-skipped` notice. Lines at one moment order by
    the position's tiebreaker, the same on every read. A smaller `PageSize` on a continuation
    changes how much a page holds, never where it starts.
11. **The scope is the window's, and the grep is the range's.** `Filters` apply to every line in
    the window. `Grep` applies to the range alone: a padding line passes unfiltered
    (`pageQuery.keep`, which the walk, the fill's count and the kubelet's window sizing share),
    so a count counts every line, a padding costs its count whatever the grep, and the caller
    sees what was happening around the matches. A continuation reads the scope its cursor
    holds, so no caller can page a window under a scope other than the one that resolved it.
12. **A late line is on the next newer page.** A line that reaches the file after a page was
    read, stamped among lines already delivered, is a straggler: a stream whose clock runs
    behind, or whose tailer was slow. A `Next` or `Follow` goes on from where each stream was,
    not from the newest line read, so the straggler is on the next page that way. A `Prev` reads
    history as it stands and goes on from one position. Within a page, records stay oldest
    first. A caller that places lines by time must expect a `Next` or `Follow` page to reach
    behind what it already holds.
13. **The request is echoed as it ran.** `FetchResponse.Request` is `q` with defaults filled
    and caps applied: `PageSize`, `MaxMessageBytes` and `MaxPageBytes` as used; `Scope` and
    `Window` as a first page sent them, or as the cursor holds them on a continuation;
    `PageFrom` empty and `PageCursor` as sent on a continuation. `ResolvedAt` is the first
    page's clock beside it. A caller prints or stores them in place of its own copy.

Rules 6, 7, 10 and 12 are what *The page cursor* above exists to keep.

## Tasks

Do them in order. Each one compiles and passes `go test ./internal/services/logs/...`. Task 1 is
a pull request of its own: it is mechanical, and it is what breaks the packages above. Tasks 2 to
8 are the second.

1. **Rename `View` to `Scope`.** `ViewSource` and `ViewFilter` become `ScopeSource` and
   `ScopeFilter`; `view.go` becomes `scope.go`; every signature and receiver in the package
   follows. `tools.LogsViewInput` aliases the new name until step 4A renames it. **Give `Refusal`
   its `Code`**: a named string type whose only values are the code block's constants, and a
   constructor `refuse(code Code, reason string) Refusal` every refusal in the package is built
   through, so none is made without one; the four `code…` constants in
   `tools/internal/logsinput/resolve.go` move here. **Rename the line cursor's names** to match
   the spec: `position` to `lineCursor`, `cursorOf` to `encodeLineCursor`, `parseCursor` to
   `decodeLineCursor`, `positionOf` to `lineCursorOf`, `comparePositions` to `compareLineCursors`
   and `instance` to `runPlace`, reshaped so that `lineCursor` embeds the new `containerName`
   beside it, as the block above has them; `streamBound`, `boundIn` and `boundAfter` keep theirs.
   **Respell the line cursor** as *The line cursor's encoding* has it, with `lineCursorVersion`.
   Tests: a round trip, a line and a moment; the same line encoded twice giving one string; a line
   of a run marked `Previous` encoding the same as before it was; a string that is not base64url,
   one that is not JSON, one of another version, one with a bad name, a bad id, a zero time, a
   negative ordinal or `math.MaxInt`, one with the name fields in part, and one with `id` and not
   `n`, each refused. Then **add the types** above beside the old ones, and `TruncatedBytes` to
   `Record`; delete `LineCursor`. Nothing calls them yet. The rename and the deletion break
   `tools` and `graph`, so `wip/log-fetch-api` is red above this package from this task until step
   4B lands; this package compiles and passes at every task. CI runs on every pull request, so
   each one into `wip/log-fetch-api` until then fails its Go build, vet and tests above this
   package. That is accepted: the pull request's summary says so, and the reviewer runs this
   package's tests.
2. **Resolve a window.** Add `resolveWindow(w resolvedWindow, from PageFrom) (from, to bound,
   before, after int, context *pageQuery, notices []Notice, err error)` in a new `window.go`:
   check the window (`invalid-range`, `bad-padding`), take the bounds as inclusive moments,
   widen by spans, and on a first page whose `PageFrom` edge is counted return the `pageQuery`
   for that side's context read, which `Fetch` runs beside the range's in one `s.page` call, as
   *The window's edges on every read* says. It reads nothing itself. Test it as a pure function:
   a span, an open bound, an open `Until` with a span measured from `ResolvedAt` on two
   `resolvedWindow`s, `Since` after `Until`, a padding past each limit, a one-nanosecond range,
   a counted first-page edge returning its context query and a continuation returning none.
3. **The page cursor.** Add `pagecursor.go`: `pageCursor`, `encodePageCursor` and
   `decodePageCursor` with the checks above, `(pageCursor).keyFor(ps pageStream) logstore.Pos`
   (the stream's entry, found by `containerName` and joined back into a `lineCursor`, clipped to
   the window, through `streamBound`, else `at`, else `Start`), `Seen` read into a first page's
   entries, and `(pageCursor).edges(m merge, records) (ahead, behind pageCursor)` as *The edges
   after a page* defines them, each stream's reach computed from the rows and stop `walk` now
   reports against the page's cut in the page's direction, where a stream that passed lines of
   two runs of one name has the later by `compareLineCursors` as its entry. `page` takes a page
   cursor in place of its one near bound and trims a counted side after the merge. Tests:

   | Area | Tests |
   | --- | --- |
   | Encoding | a round trip; the same cursor encoded twice giving one string; a string that is not base64url, one that does not inflate, one that is not JSON and one of another version each refused; more than `MaxStreams` entries decoding; a counts round trip and one past its `Lines` refused |
   | Scope | a scope of `MaxStreams` live pods and one gone pod whose logs the file keeps paging on through a `Next` that names every container; a cursor whose scope is now refused keeping the scope's code; a stream that left the scope; one that joined it |
   | Entries | an older cursor with an entry refused; a stream with an entry and one without; two entries for one stream with the later standing; two runs of one name across a restart with the resolve one page behind and no line repeated; a restart with the old run kept in the cache and marked `Previous` on the next page, its lines not repeated and their cursors unchanged |
   | `Seen` | a first page reading the named stream past its line and another from `Start`; a `Seen` line older than `Start` read from `Start`; an entry that is a moment refused; `Seen` with `PageFromLast` refused |
   | `Start` and counts | a stream with no entry read from `Start` on a `Next`, and under a counted `Before` from the line the first page's read found, with a line of it older than that not delivered; a counted side trimmed to its remainder with no `Next` set past it |
   | Reach | an entry at a grep-rejected line so the stream is not re-read on the poll; no entry passing a record the page cap dropped; a poll whose lines the grep all rejected moving its entries past them; a stream stopped at a loud second whose entry is the second's edge and whose next page reads past it; a stream stopped at a moment past a cut another stream made whose entry is its last kept line short of the cut; a cut first page whose `Next` reads a stream with no entry past the cut |
   | Edges | a page that passed no line keeping its incoming edge ahead and none behind; a first page with no `Prev`; a `Follow` after a `PageFromLast` page polled twice with a stream quiet throughout and nothing of it delivered; a `Prev` cut at an empty window's start with no line passed whose `Prev` goes on from the cut; each edge after a first page and after a continuation each way, the older edge carrying no entry |
4. **Rewrite `Fetch`** over `FetchRequest` and `FetchResponse`: refuse a mixed request (rule 6),
   decode the cursor for a continuation, resolve the window, build the `pageQuery`, call `s.page`,
   apply the caps, compute and encode the edges into `Page`, turn `skipped` into `stream-skipped`
   notices, echo the request. Delete `bounds`, `checkQuery` and `around` once nothing uses them, and
   unexport `FetchMode` as `direction` with `FetchHead` and `FetchTail` as `newer` and `older`:
   `fill`, `merge`, `walk`, `holes` and `follow` keep reading it, and `pageQuery.mode` is still what
   tells them which way; a first page's count context is `resolveWindow`'s second `pageQuery`, run
   in the same `s.page` call as the range's, as `around` ran its halves, and joined with it as *Two
   reads, one page* says, in a `joinReads(context, rng merge) merge` beside `around`'s old home; the
   forward read goes first in the call, since `fill` builds seams for a `Head` query followed by a
   `Tail`, so under a counted `After` the context read is passed before the range's. A
   continuation's context is counted as it delivers. Make `walk` report the first and last rows it
   read, each as its `logstore.Pos` whether or not the grep kept it, and the moment its read stopped
   at when it stopped at one; `walked` carries them to `merge`, and `edges` reads them.
   Tests, over the fixtures in `testutil_test.go`:
   - A count that finds enough lines; one that does not; one whose read was cut; one with a
     skipped stream; an open bound with padding; a count under a `Grep` delivered unfiltered
     matching line.
   - A `PageSize` under `Lines`, so the first page ends inside the context and its `Next` goes on
     from the page's last line, not the lookup's farthest, with a grep-rejected row between two
     delivered lines passed and the range untouched; the same under `PageFromLast` with a counted
     `After`.
   - A lookup that found its `Lines` whole with more context beyond, setting no `Next` and no
     `Prev` past `Start`; the same under a counted `After`.
   - A lookup stopped at a moment under `Before`, whose `Prev` reads older from the stop and whose
     `Next` goes on from the page's last line with no line repeated; the same under `After`, with
     `Next` from the stop and `Prev` from the page's oldest; a counted `After` whose lookup stopped
     and whose range read was cut, the stop the `Next` and the cut the page's own.
   - A counted `After` delivered across two `Next` pages and closing the window on the second; a
     counted `Before` found whole and delivered across a `PageFromFirst` and a `Next` with each
     cursor's count within 0 to `Lines`; a `Prev` chain back through the context and a `Next`
     that re-delivers it with the counts falling to 0; a short read setting `Start` and a `Next`
     back into the range ending at 0.
   - A straggler in the before-context delivered on a `Next` leaving `before` at 0; a
     continuation that reads no context at all.
5. **Caps.** Add `capLines(records []Record, maxLine, maxPage) ([]Record, cut bool, notices)` in
   a new `caps.go`, setting each cut record's `TruncatedBytes`. Test the UTF-8 boundary, a cap
   that lands inside an SGR sequence cutting before it, a coloured line under the cap arriving
   with its colours, `TruncatedBytes`, the first-line exception and the edge it sets.
6. **Update every caller** inside `services/logs` (tests, `fetch_test.go`'s helpers) to the new
   request. Other packages are red from task 1 until step 4B, as task 1 says; the sequence's
   `wip/log-fetch-api` branch is red above this package until then, which is expected.
7. **Extend the invariant tests.** `TestPagesJoinWithoutLossOrRepeat` walks a window both ways
   through `Prev` and `Next` with random `PageSize` and `MaxPageBytes` per continuation and
   asserts rule 10; add context on both sides to its windows. Add `TestAStragglerIsOnTheNextPage`
   for rule 12: after a page is read, write a line stamped inside that page's span into a stream
   that had a line on it, into one that had none, and into a stream that joined the scope; each is
   on the next `Next` page, and on no later one. Add the poll: a `PageFromLast` page with an open
   `Until`, lines written to two streams with skewed stamps, and `Follow` continuations that
   deliver each exactly once. Add the counted `After`: 100 lines with 20 present on the first
   page, then polls as 80 more arrive; the poll that finds the hundredth past `Until` sets no
   `Follow`, since its `after` is `Lines` (rule 7), and a late in-range line written meanwhile is
   delivered and moves no edge. Add the clear: a page, `Clear`, and its `Next` answering, once
   the file has refilled, the lines the same `Next` answers without the clear.
8. **Update `services/logs/CLAUDE.md`'s** *`Service.Fetch`* paragraph to describe the window
   request, the response and the page cursor, in the file's own style.

## Acceptance

- Every rule above has a named test.
- `go test -race ./internal/services/logs/...` passes.
- `TestTheFileKeepsItsInvariant` is unchanged and passes.

## Verification

```sh
cd sidecar && go test -race ./internal/services/logs/...
```

## When it lands

The `Fetch` paragraph of `services/logs/CLAUDE.md` describes the window request, the response
and the page cursor. The sequence's ADR records the README's decisions.
