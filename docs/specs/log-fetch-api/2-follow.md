# Step 2 — The follow on the shared types

**Package:** `services/logs` (paths relative to `sidecar/internal/`).
**Prerequisites:** step 1.
**Status:** Landed (#383).

## Goal

`Service.Follow` takes and answers the vocabulary step 1 made. It takes a `Scope`, a start that
is a moment, the lines the caller has seen, or a page's `Follow`, and a message cap. It streams
frames that carry a `Record`, a `Notice` or a `Refusal`. The fetch and the follow are one API
with two verbs.

The engine under it changes in three places. Each stream starts from its own bound rather than
one bound for all. A stream that joins starts from the follow's start rather than the last
checkpoint, since there is no checkpoint. A follow with a start other than now first reads the
instances of a `Pod` source only the file holds through the page engine, as a page does, and
then tails. Tailers, the registry, reconcile, fills and watermarks stay as `follow.go` has
them. This step otherwise changes what
the method takes and what a frame carries. Read `services/logs/CLAUDE.md`'s *`Service.Follow`*
paragraphs and `Follow`'s doc comment first.

Names below are the landed code's: the line cursor is `logline.Cursor`, a stream's key
`logline.ContainerName`, its place `logline.RunPlace`, a line `logline.Record`, and the codes
and `Refusal` live in `logs/refusal`. A bound is `logline.Bound`; `logline.BoundAfter(cursor)`
is the bound past a line, and `logline.Bound{At: logline.MomentAt(t), Inclusive: true}` the
bound a window's `Since` uses.

## The types

```go
// FollowRequest is one follow. It starts from now, or from Since, that moment's
// lines included, or from Cursor, a page's Next or Follow: each stream reads past
// where the page left it, and the scope is the cursor's. Seen, sent with either,
// is the last line the caller has of each stream: that stream reads past its
// line, and a stream not in Seen reads from the start. MaxMessageBytes cuts each
// message, 0 for the service's own cap.
type FollowRequest struct {
	Scope
	Since           *time.Time
	Seen            []string
	Cursor          *string
	MaxMessageBytes int
}

// FollowFrame is one frame of a follow, exactly one field set. Record is a line.
// Notice is something the follow wants the caller to know. Ended is why the
// follow ended, and is the last frame: following again would end the same way.
type FollowFrame struct {
	Record *logline.Record
	Notice *Notice
	Ended  *refusal.Refusal
}
```

`Follow` becomes `Follow(ctx, id cluster.ClusterID, q FollowRequest) (*cluster.Stream[FollowFrame], error)`.
`FollowFrameType`, its three constants, and the frame's `Type`, `Cursor`, `Code` and `Reason`
fields go.

## Codes

The follow's endings already have their codes in `refusal.go`: `revoked`, `unauthorized`,
`finished` (`errStopped`, and every source a Pod whose pod is gone), `too-many` and
`not-mirrored`. Each moves from the frame's `Code` and `Reason` onto `Ended`'s `Refusal`; the
words stay as `follow.go` writes them. No code is added.

## Rules

1. **The start.** A follow starts from a moment or from a page cursor, never both: `Cursor`
   beside `Since` is `conflicting-parameters`. `Seen` rides beside either, and alone is
   `conflicting-parameters` too, since a stream it does not name would read from now and lose
   what it wrote meanwhile. With nothing set it reads from now, as today. `Since` is the bound a
   window's `Since` uses: the lines stamped at the moment are in, and a moment ahead of now
   delivers lines from then on. There is no exclusive `Since`: a caller that wants the lines past
   one it saw names that line in `Seen`. Either start is normalised to one page cursor, the
   follow's **start cursor**: from a moment, `firstPage` over `Window{Since}`, `Since` set to
   now when the request has none, with `PageFromFirst` and the request's `Seen`; from a cursor, the merged cursor of rule 3
   with its `Until` and `After` cleared. Every stream is seeded from it, and rule 4's read uses
   it.
2. **`Seen` is where each stream picks up.** Each entry is a line cursor (step 1, *The line
   cursor*), a record's `Cursor()` as the caller got it. `Seen` is parsed by step 1's `seenOf`,
   so one entry that does not parse as a line cursor, or names a moment and not a line, is
   `cursor-invalid`, and of two for one stream the newer line stands, whatever their order. The
   stream an entry names reads past its line through `BoundAfter`, or from `Since` when the line
   is older than `Since`. Beside `Cursor`, the entries merge into the cursor's own `seen` through
   the same `seenOf`, the newer line of a stream standing. One whose stream is outside the scope
   is dropped. So a caller that reopens a follow with its start, moment or cursor, and the last
   line it got from each stream misses no line and repeats none, however unevenly the streams
   log. The caller builds `Seen` from every record it holds, keyed by their stream, and never
   from a cursor's inside.
3. **`Cursor` is a page's `Next` or `Follow`,** decoded as a continuation decodes it (step 1,
   rule 6): one that does not decode or check is `cursor-invalid`, and so is a `Prev`, since a
   follow reads toward newer lines. The two newer cursors are one shape, so the service cannot
   tell them apart and need not. The request's `Scope` must be zero, since the cursor holds it;
   with no `Cursor` it is required. Each stream's watermark is the start cursor's `keyFor` over
   that stream, with the request's `Seen` merged into the cursor's `seen` first, so the follow
   reproduces none of its rules: its `seen` entry, else its `at`, else the window's `start`,
   else the range's start while a counted `Before` is still unresolved, and then clipped to the
   window's known start, so an old `Seen` entry beside a cursor reads from the window's start
   and never before it, exactly as a poll of that page reads.
4. **The instances only the file holds are read first, through the page engine.** A page reads
   a `Pod` source's instances the file holds beside what the resolve hands out (`pageStreams`'
   second loop, under `maxInstances`). The follow's engine cannot hold them: its streams are
   keyed by `StreamKey`, which every old instance shares, and a `tracked` needs a resolved
   `stream`. So a follow whose start is not now reads them before it tails anything: one newer
   read over those `pageStream`s alone, through the half of `Service.page` that follows
   `pageStreams` (task 4), to their ends, in pages of `MaxPageSize`, each page going on from
   the merge's `edges` ahead cursor as a `Next` does, until the merge reports no cut. The read's
   cursor is the start cursor (rule 1), which has no end, so an earlier instance that logged
   past the window's end before it restarted is read whole, and each instance reads from
   `Since` or past its `Seen` line, never from its oldest line. Their records are sent first, in
   the merge's order, through the follow's grep and cap; a
   `stream-skipped` notice the read raises is sent as a frame. The read's gaps and merge are the
   page engine's, and it reads no backend: a file-only instance is `exhausted`, since the
   kubelet cannot serve an older runtime instance, so a gap in one is carried on the record as a
   page carries it. The read neither holds off nor hastens the follow's end: `finished`
   is judged over the resolved streams alone, as today. An instance only the file holds never
   grows, so it is read once and not reconciled again; one the janitor removes meanwhile is
   simply gone. A follow from now reads none, since nothing of them is stamped after now.
   **The open defers `NoNodes` as a page does, and still resolves as a follow.** A label filter
   over a mirror with no nodes is refused by `resolve` at the follow's open today; a page defers
   it, since the file's rows of a gone pod carry their place and can satisfy the filter.
   `resolve`'s `follow` argument also computes `starting`, which the first `bring` seeds its
   waiting from, so the open keeps resolving as a follow: `resolve` defers `NoNodes` for both
   callers, and each refuses it itself when nothing can satisfy the filter, a page with no
   stream as today and the follow's open with no file-only row to read. With rows, the follow
   sends them and then waits for nodes through its reconcile, which already waits on
   `NoNodes`, unless every source is a gone pod: no resolve judges a pod gone without nodes,
   so that follow ends `no-nodes` once the rows are sent.
5. **A follow takes a cursor's start and ignores its end.** The cursor's `Until`, `After` and
   counted sides say where a poll stops; a follow does not stop, so it reads them for nothing.
   A follow from a cursor delivers every line a poll of it would, at the same places, and then
   goes on past the window's end for as long as it runs. A `Next` from a bounded window follows
   past the bound the same way. The grep applies to every line a follow sends, from a moment or
   from a cursor, so the one kind of line a follow and a poll disagree on is a padding line on
   either side of the range, which the poll passes unfiltered (step 1, rule 11) and the follow
   greps. A caller that wants the window's end honoured polls the cursor through `Fetch`.
6. **One start for every unnamed stream.** A stream not named in `Seen` or in the cursor's
   `seen`, at open or joining the scope mid-follow, starts from the follow's start: `Since`,
   the start cursor's `keyFor` over it with no entry, which is `Since`, the cursor's `at` or
   its window's start, or now. `followStart` keeps the start cursor for that, and nothing else.
7. **A follow hands out no cursor and sends no checkpoint.** A follow has no edges. What a
   caller needs to go on it already holds: its start, and the lines it saw, sent back as `Seen`.
8. **Message cap.** `MaxMessageBytes` is checked as `checkRequest` checks a page's: a negative
   is `bad-page`, 0 and anything over `MaxMessageBytesCap` are `MaxMessageBytesCap`. A line's
   `Message` is cut at it through `capMessage` before it is sent, as a page's is (step 1, rule
   8), and `TruncatedBytes` is the bytes removed. There is no page cap: a follow has no page.
9. **A notice is a frame.** A stream `failed` ends with an error, rather than holds, sends one
   `stream-skipped` notice naming the stream and the error's words in `Message`: the server's
   refusal (`FailDenied`), the server's answer for this log (`FailEnded`), and a second too loud
   to read (`FailLimit`). A held stream sends none, since it is read again; `errRowGone` is
   transient and holds, so it sends none. A container whose stream ended clean sends none, since
   it finished. A container waiting to start sends none for `FailEnded`, since its log is still
   to come. The notice is sent once per stream per generation: a stream tailed again at a
   later generation that fails again says so again.
   The engine's decisions about when a stream is held, ended or retried do not change. Only what
   was silent is said.
10. **The end is a refusal.** An ending the user can read is one `Ended` frame carrying the
   `Refusal` with its code, and then the stream closes clean. Any other ending sets `Err`, and a
   cancel sets none, as today.
11. **Every string on a frame is cluster text,** read through `ForModel` by a tool and
    `VisibleText` by the viewer, as today.

## Tasks

1. **Rename the fetch's cursor.** `FetchRequest.PageCursor` becomes `FetchRequest.Cursor`
   (README decision 22), in `fetch.go`, its tests, `services/logs/CLAUDE.md` and
   `services/logs/README.md`. The type `pageCursor` and its functions keep their names. One
   `refactor(logs)` commit on its own, before anything else in this step.
2. **Types.** Add `FollowRequest` and the new `FollowFrame`; change `Follow`'s signature.
   Delete `FollowFrameType` and its constants. Fix every compile error in `services/logs`.
3. **The start.** Add
   `followStart(q FollowRequest, now time.Time) (scope Scope, start pageCursor, err error)`
   in `follow.go`: rules 1 to 3 and 6, the page-cursor case through `decodePageCursor` with the
   request's `Seen` merged into its `seen` through `seenOf`, then `Until` and `After` cleared;
   the moment case through `firstPage`. Every stream is seeded from `start.keyFor` over a
   `pageStream` built for it, at open or joining later, which replaces `follow.cursor()` and
   `follow.since`. Tests:
   from now; from a `Since`, with a line stamped at the moment delivered; with `Seen` naming one
   stream's line, that stream past it and another from `Since`, with the named line's moment
   delivered for the other stream alone; a `Seen` line older than `Since` read from `Since`; two
   `Seen` for one stream with the newer standing, in either order; a `Seen` that is not a line
   cursor refused; `Seen` alone refused; one outside the scope dropped; `Cursor` beside `Since`
   refused; from a `Follow` with an entry for one stream and none for another; from a `Follow`
   with `Seen`, a request entry newer than the cursor's standing and one older yielding; a
   `Seen` entry older than the cursor's window start clipped to it; a cursor with a counted
   `Before` still unresolved seeding from the range's start; a `Prev` refused; a cursor of another version refused; a `Scope` sent
   beside a page cursor refused, and one missing with no `Cursor` refused; a stream that left
   the scope dropped; a stream that joins seeded from the start.
4. **The instances only the file holds.** Rule 4. Split `Service.page` after `pageStreams`:
   `page` keeps the resolve, the file and `pageStreams`, then calls
   `(s *Service) pageOver(ctx, id, t target, file *logstore.Store, streams []pageStream, qs ...pageQuery) ([]merge, error)`,
   the rest of today's body from `tailOf` on. `Fetch` is unchanged. Before the follow's loop
   starts, a follow whose start is not now builds the file-only `pageStream`s as `pageStreams`'
   second loop does and calls `pageOver` with them alone, a newer `pageQuery` whose `cur` is the
   read's cursor as rule 4 has it and `limit` `MaxPageSize`, page after page from `edges`'
   ahead cursor until no cut, sending the records through `keep` and the cap and the read's
   notices as frames. Tests, over a restart fixture, a pod whose container ran twice: from a
   page's `Next` cut inside the earlier instance, the follow delivering the earlier instance's
   rest then the later's lines, as the poll does; the same with a gap in the earlier instance,
   carried on the record and no backend read, the fake server asserting no request for it; the
   earlier instance holding more than `MaxPageSize` lines, all delivered once; an earlier
   instance with lines past a past `Until`, delivered; a pod whose container ran three times,
   both earlier instances read whole and in order; from a `Since` before the restart, the
   earlier instances' lines from `Since` first and none older; from a `Since` with a `Seen`
   entry in the earlier instance, its lines past the entry; from now, none of them; a
   `stream-skipped` the read raises arriving as a frame; the earlier instance's read neither
   ending the follow nor keeping it open once the later instance has stopped; a resolved
   stream's lines arriving once, from its tailer alone; the follow twin of
   `TestAGonePodIsFilteredWithNoNodesLeft`, a zone filter over a gone pod with no nodes left
   delivering the cached rows then ending `no-nodes`, and the same with no cached row refused
   `no-nodes`;
   a follow from a cursor over a scope whose one container is still starting, not ending at
   open.
5. **`Seen` beside a page cursor on `Fetch`.** `checkRequest` stops refusing `Seen` on a
   continuation, and `cursorOf` merges the request's `Seen` over the cursor's entries through
   `seenOf`, the newer standing, as rule 2 does for the follow. It refuses `Seen` beside a
   cursor whose window has a counted `After` as `conflicting-parameters`, since the cursor's
   `after` counts the pages' padding lines alone and the merged start would deliver the count
   again. It is how a caller pages on after a follow it opened from a cursor (step 5). Update
   step 1's `conflicting-parameters` row in `services/logs/README.md`, in `1-service.md`, and
   `Fetch`'s rule 6. Tests: a `Next` with `Seen`, a request entry newer than the cursor's
   standing and one older yielding; `Seen` beside a `Prev` refused, since an older page has
   nothing to read past; `Seen` beside a cursor with a counted `After` refused.
6. **The cursor's end, ignored.** Rule 5: the start cursor carries no `Until` or `After`, and
   nothing in the follow reads a window's end. Tests: a follow from a `Follow` whose window has an `Until` in
   the past delivering a line stamped after it; one whose counted `After` is met delivering
   more; a `Next` from a window with an `Until` going on past it; a padding line before the
   window's `Since`, and one after its `Until`, that fails the grep not sent.
7. **The cap.** Check `MaxMessageBytes` as rule 8 has it; cut each line through `capMessage`
   before it is sent, setting `TruncatedBytes`. Tests: a long line cut, a short one whole, 0 as
   the cap, a negative refused `bad-page`, one over `MaxMessageBytesCap` clamped.
8. **Notices and endings.** Send a `stream-skipped` notice where `failed` ends a stream with an
   error; move each ending's code and words onto `Ended`'s `Refusal`. Delete the checkpoint
   frame, `flush`'s oldest-watermark pass and `follow.cursor()`. Update every test in
   `follow_test.go` that reads a frame's `Type` to read the field that is set, and every one that
   waits on a checkpoint to wait on the lines. A test that asserts an ending's words now asserts
   its code too. Tests: a denied stream's notice, an ended stream's notice (`FailEnded`), a loud
   second's notice, a held stream sending none, a gone row sending none, a finished container
   sending none, a stream failing again at a later generation saying so again.
9. **The exact-once test.** Add to step 1's poll test (its task 8) a follow opened from the same
   page's `Follow`, delivering exactly the lines the poll does and then the lines written since;
   then one opened from the same `Follow` with `Seen` built from every record held, the page's
   and the first follow's, delivering the rest and none again, with one stream quiet since
   before the first follow began, and one seen on the page alone not read again. The fixtures
   use an open `Until`, no `After`, and no `Before` beside a grep, so the poll and the follow
   stop nowhere different and pass the same lines.
10. **Callers.** `graph` and `tools/logswatch` fail to compile until steps 4A and 4B, which is
   expected on `wip/log-fetch-api`.
11. **Docs.** Update `services/logs/CLAUDE.md`'s *`Service.Follow`* paragraphs and `Follow`'s
    doc comment.

## Acceptance

- `go test -race ./internal/services/logs/...` passes.
- A follow opened from a page's `Follow` and the page's poll deliver the same lines, and a
  follow reopened from the same `Follow` with `Seen` delivers the rest once, in a test over the
  poll fixtures.
- A follow from a cursor whose window has an `Until` delivers lines stamped after it.
- A followed line longer than the cap arrives cut with its `TruncatedBytes` set.

## When it lands

`services/logs/CLAUDE.md` describes a follow's request, frames and start in the same words as a
page's.
