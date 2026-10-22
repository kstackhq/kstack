# Step 2 — The follow on the shared types

**Package:** `services/logs` (paths relative to `sidecar/internal/`).
**Prerequisites:** step 1.
**Status:** Planned.

## Goal

`Service.Follow` takes and answers the vocabulary step 1 made. It takes a `Scope`, a start that
is a moment, the lines the caller has seen, or a page's `Follow`, and a message cap. It streams
frames that carry a `Record`, a `Notice` or a `Refusal`. The fetch and the follow are one API
with two verbs.

The engine under it changes in two places: each stream starts from its own bound rather than
one bound for all, and a stream that joins starts from the follow's `Since` rather than the
last checkpoint, since there is no checkpoint. Tailers, the registry, reconcile, fills and
watermarks stay as `follow.go` has them. This step otherwise changes what the method takes and
what a frame carries. Read `services/logs/CLAUDE.md`'s *`Service.Follow`* paragraphs and `Follow`'s
doc comment first.

## The types

```go
// FollowRequest is one follow. It starts from now, or from Since, that moment's
// lines included. Seen is the last line the caller has of each stream: that
// stream reads past its line, and a stream not in Seen reads from Since. Cursor
// is a page's Follow: each stream reads past where the page left it, as a poll
// of the page reads, and the scope is the cursor's. MaxMessageBytes cuts each
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
	Record *Record
	Notice *Notice
	Ended  *Refusal
}
```

`Follow` becomes `Follow(ctx, id cluster.ClusterID, q FollowRequest) (*cluster.Stream[FollowFrame], error)`.
`FollowFrameType` and its three constants go.

## Codes

The code block in `fetch.go` gains the follow's endings, so every `Ended` names one. An ending's
words stay as `follow.go` writes them.

| Code | When |
| --- | --- |
| `revoked` | the claim was revoked: the cluster's record was disabled or deleted (`ErrRevoked`, step 1's code) |
| `unauthorized` | the cluster refused the user's credentials (step 1's code, from the same words) |
| `finished` | every container of the scope stopped (`errStopped`), or every source was a Pod whose pod is gone |
| `too-many`, `not-mirrored` | as step 1 has them: the scope grew past `MaxStreams`; a kind the cache no longer mirrors |

## Rules

1. **The start.** A follow starts from a moment or from a page's `Follow`, never both:
   `Cursor` beside `Since` or `Seen` is `conflicting-parameters`. With nothing set it reads from
   now, as today. `Since` reads every stream from that moment through the bound a window's
   `Since` uses (`boundIn`): the lines stamped at the moment are in, and a moment ahead of now
   delivers lines from then on. There is no exclusive `Since`: a caller that wants the lines
   past one it saw names that line in `Seen`.
2. **`Seen` is where each stream picks up.** Each entry is a line cursor (step 1, *The line
   cursor*), a record's `Cursor()` as the caller got it; the stream it names reads past that line
   through `boundAfter`, or from `Since` when the line is older than `Since`, and every other
   stream reads from `Since`. One that does not parse as a line cursor, or names a moment and not
   a line, is `cursor-invalid`; of two for one stream the later in the list stands; one whose
   stream is outside the scope is dropped. So a caller that reopens a follow with the last line it
   got from each stream misses no line and repeats none, however unevenly the streams log, and a
   stream that had sent nothing re-reads an empty range. The caller builds `Seen` from the records
   it holds, keyed by their stream, and never from a cursor's inside.
3. **`Cursor` is a page's `Follow`,** decoded as a continuation decodes it (step 1, rule 6): one
   that does not decode or check is `cursor-invalid`, and so is a page cursor that is not `newer`,
   since only a `Follow` is a place a follow starts. The request's `Scope` must be zero, since the
   cursor holds it; beside `Since` or `Seen` it is required. Each stream's watermark starts at its
   `seen` entry, else at the cursor's `at`, else at the window's start, exactly as a poll of
   that page reads. So a follow opened from a page's `Follow` misses no line the page did not show
   and repeats none it did. A `seen` entry whose stream left the scope is dropped.
4. **A follow hands out no cursor and sends no checkpoint.** A follow has no edges. What a
   caller needs to go on it already holds: the lines it saw, sent back as `Seen`. A stream that
   joins the scope mid-follow starts from `Since`, or now with none.
5. **Message cap.** A line's `Message` is cut at `MaxMessageBytes` as a page's is (step 1, rule
   8), through the same `capLines`, and `TruncatedBytes` is the bytes removed. 0 is
   `MaxMessageBytesCap`. There is no page cap: a follow has no page.
6. **A notice is a frame.** Where the engine today drops or ends a stream and says nothing on
   the wire, it sends one `Notice`: `stream-skipped`, once per stream, naming the stream and the
   reason in `Message`. The engine's decisions about when a stream is skipped, retried or ended
   do not change. Only what was silent is said.
7. **The end is a refusal.** An ending the user can read is one `Ended` frame carrying the
   `Refusal` with its code, and then the stream closes clean. Any other ending sets `Err`, and a
   cancel sets none, as today.
8. **Every string on a frame is cluster text,** read through `ForModel` by a tool and
   `VisibleText` by the viewer, as today.

## Tasks

1. **Types.** Add `FollowRequest`, the new `FollowFrame` and the codes; change `Follow`'s
   signature. Delete `FollowFrameType` and its constants. Fix every compile error in
   `services/logs`.
2. **The start.** Add `followStart(q FollowRequest) (Scope, at lineCursor, seen map[containerName]runPlace, error)`
   in `follow.go` beside `boundAfter`: rules 1 to 3, the page-cursor case through
   `decodePageCursor` and its `seen`, and the request's `Seen` parsed into the same list. Each
   stream is then seeded from its own entry, else from `at`; a stream that joins later is seeded
   from `at` too. Tests: from now; from a `Since`, with a line stamped at the moment delivered;
   with `Seen` naming one stream's line, that stream past it and another from `Since`, with the
   named line's moment delivered for the other stream alone; a `Seen` line older than `Since`
   read from `Since`; two `Seen` for one stream with the
   later standing; a `Seen` that is not a line cursor refused; one outside the scope dropped;
   `Cursor` beside `Since` or `Seen` refused; from a `Follow` with an entry for one stream and
   none for another; an older cursor refused; a cursor of another version refused; a `Scope` sent
   beside a page's `Follow` refused, and one missing beside `Since` refused; a stream that left
   the scope dropped; a stream that joins seeded from `at`.
3. **The cap.** Cut each line through `capLines` before it is sent, setting `TruncatedBytes`.
   Tests: a long line cut, a short one whole, 0 as the cap.
4. **Notices and endings.** Send a `stream-skipped` notice where a stream is dropped silently
   today; put each ending's code on its `Refusal`. Delete the checkpoint frame and `flush`'s
   oldest-watermark pass. Update every test in `follow_test.go` that reads a frame's `Type` to
   read the field that is set, and every one that waits on a checkpoint to wait on the lines.
   A test that asserts an ending's words now asserts its code too.
5. **The exact-once test.** Add to step 1's poll test (its task 8) a follow opened from the same
   page's `Follow`, delivering exactly the lines the poll does; then one opened with `Since` the
   page's start and `Seen` the last line the first follow delivered of each stream, delivering
   the rest and none again, with one stream quiet since before the first follow began.
6. **Callers.** `graph` and `tools/logswatch` fail to compile until steps 4A and 4B, which is
   expected on `wip/log-fetch-api`.
7. **Docs.** Update `services/logs/CLAUDE.md`'s *`Service.Follow`* paragraphs and `Follow`'s
   doc comment.

## Acceptance

- `go test -race ./internal/services/logs/...` passes.
- A follow opened from a page's `Follow` and the page's poll deliver the same lines, and a
  follow reopened with `Seen` delivers the rest once, in a test over the poll fixtures.
- A followed line longer than the cap arrives cut with its `TruncatedBytes` set.

## When it lands

`services/logs/CLAUDE.md` describes a follow's request, frames and start in the same words as a
page's.
