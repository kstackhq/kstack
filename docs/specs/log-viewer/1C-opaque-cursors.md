---
title: Opaque cursors
scope: sidecar
status: Planned
---

# Opaque cursors

**Needs:** step 1A. **Unblocks:** nothing; step 2's client passes cursors through unread, so it
lands on either form. Can be built beside step 1B.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

A line's cursor becomes base64 of a JSON object, so nothing outside `services/logs` can read one
by accident and a later sidecar can add a key to it. Today it is the line's position as
tab-separated fields opening with an RFC 3339 timestamp, which is what led step 1B's first draft
to parse it as a time. A moment stays a bare RFC 3339 time: the `At` anchor sends one, and the
viewer mints two, so that form is the contract's and does not change.

The shape is Kubernetes' own `continue` token, base64 of `{"v":1,…}`, which a client passes back
and never reads.

## What is not in this step

- **No paging hint yet.** A before-page is read in doubling time windows ending at the cursor's
  moment (`backwardUntil`), and the window reach that found a page could ride the cursor so the
  next page's first window is sized to hold about `limit` lines. That is its own change, and it
  needs this one first: a hint is a key a client must not read.
- **No schema change.** `LogRecord.cursor` and the four cursor arguments already say the cursor
  is opaque and a time alone names a moment.
- **No webview change.** Step 1B's library reads nothing from a cursor by then.

## Design

### 1. The encoding

`cursor.go`'s `cursorOf` writes a line cursor as the standard base64 (`base64.StdEncoding`) of one
JSON object, keys in this order, no whitespace:

```json
{"v":1,"t":"2026-10-08T10:00:00.123456789Z","ns":"team-a","pod":"api-7f9c","c":"app","prev":false,"n":0}
```

`v` is the cursor's version, `t` the moment with nanoseconds, `ns`, `pod`, `c` and `prev` the
stream, and `n` the ordinal. The keys are short because every record carries its cursor: a page
of 500 lines carries 500 of them, and the encoding costs about 110 bytes a line against 80 today.
A moment is written as it is, `t` alone, never encoded.

### 2. Reading one

`parseCursor` reads a time first, as it does now: a bare RFC 3339 time is a moment. Anything else
is decoded as base64, then as the object, and refused with `errBadCursor` when it is neither, when
`v` is not 1, when `t` is not a time, when `c` is empty, or when `n` is negative. A key it does not
know is ignored, which is what lets a later version add one. A tab-separated cursor is refused
like any other text, since no client holds one past the sidecar that minted it: a cursor lives
in a viewer's memory and nowhere on disk.

### 3. What stays

`position`, `comparePositions`, the bounds and the merge are untouched: the encoding is the text
form of a `position` and nothing else. `Record.Cursor()` and `merge.go`'s `NextCursor` call
`cursorOf` as they do now.

## Decisions this step asks for

- **Base64 JSON, not a versioned tab form.** A `v1:` prefix on the tab form would also mark the
  version, but the fields would still read as a timestamp to anyone looking, and a hint carrying
  text would have to keep the no-tab promise. JSON ignores unknown keys and escapes anything.
- **Short keys.** A cursor rides every record, so its size is paid per line, not per page.
- **A moment stays bare.** Encoding it too would make the client mint base64, and the contract
  already names the bare form.

## Tasks

1. **The encoding.** `services/logs/cursor.go`: `cursorOf` and `parseCursor` over the object in
   §1 and §2; the file's comment names the form and the version. **Planned.**
2. **Tests.** `services/logs/cursor_test.go`: the cases below. `graph/schema.resolvers_test.go`
   reads `nextCursor` and each record's `cursor` through the real `cursorOf`, never as a literal.
   **Planned.**
3. **Docs.** `sidecar/CLAUDE.md`'s cursor paragraph names the form; the schema's comments stand.
   **Planned.**

## Tests

Go, beside the files they cover:

- `services/logs/cursor_test.go`: a line's cursor decodes to its position and back; a moment is
  a bare time both ways; a cursor with a key the reader does not know still reads; `v` other
  than 1, a tab-separated cursor, base64 of text that is not the object, an empty `c` and a
  negative `n` are each refused with `errBadCursor`; two cursors of one moment order by stream
  and ordinal as their positions do.
- `services/logs/fetch_test.go` and `merge_test.go` already page by the cursors a page hands back
  and never spell one, so they hold as they are; a test that spelled one is rewritten to read it
  off a record.

## Security

**Widens:** nothing. A cursor is the client's own text passed back, as it is now, and
`parseCursor` refuses the same shapes it refuses today, with base64 and JSON decoding in front.
The decoded fields reach the API server only as a `sinceTime` and client-side bounds, as the tab
fields do.

**Residual:** none new.

## When it lands

- `sidecar/CLAUDE.md`: the *A cursor names a line* paragraph names the encoding.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs ./graph/...`; `make test-changed` while
working; `make lint-go`, `make vet-go`.

By hand, with the sidecar running against a cluster: a `logsFetch` over the socket with `curl`
for a deployment's last 20 lines answers a `nextCursor` that decodes from base64 to the object in
§1, and sending it back as `before` answers the 20 lines before them with none repeated.
