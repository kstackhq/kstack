# Step 3 — The tools' shared parser

**Package:** `tools/internal/logsinput` (paths relative to `sidecar/internal/`).
**Prerequisites:** step 1.
**Status:** Planned.

## Goal

`LogsRead` and `LogsWatch` read times, context values and tokens the same way, from one place.
This step adds the three readers the new `LogsRead` needs. It changes no tool.

Read `tools/CLAUDE.md`'s *The log tools' input* section, `logsinput/parse.go` and
`logsinput/cursor.go` first.

## Times at any precision

Replace `MomentOf(s, now, dir)` with:

```go
// Side is which end of a range a value is read for.
type Side int

const (
	Start Side = iota // the earliest nanosecond of the value
	End               // the latest nanosecond of the value
)

// RangeEndOf reads one end of a range as the moment it names: an RFC 3339 time
// at any precision, or an ISO 8601 duration back from now.
func RangeEndOf(s string, now time.Time, side Side) (time.Time, error)
```

Rules, each with a test in `moment_test.go`:

- A time may be a date (`2026-10-21`), a minute, a second, or a second with 1 to 9 fraction digits,
  with `Z` or an offset, which is normalized to UTC. A date takes no offset: refuse
  `2026-10-21+03:00` with a message saying a date is UTC (README decision 9).
- A time names an interval: the whole day, minute, second or fraction it spells. `Start` takes the
  interval's first nanosecond, `End` its last. `2026-10-21T05:00:00Z` for `End` is
  `05:00:00.999999999Z`.
- A duration (`PT15M`, `P1D`) is exact: `now` minus that span, on the calendar in UTC, with a
  month end clamped as today. The same for both sides.
- Anything else is an error naming the two forms and `now`, as `logsread`'s `endMessage` does. A
  version-1 cursor from an old transcript is one such value.
- Nothing before the epoch or after year 9999, as today.

`LogsWatch` keeps its own rules about the future; it calls `RangeEndOf` and checks the moment
itself.

## Padding values

```go
// PaddingOf reads a before or after value: a whole number of lines, or an ISO
// 8601 duration as a span.
func PaddingOf(v json.RawMessage, field string) (logs.Padding, error)
```

- A JSON number that is a whole number from 0 to `logs.MaxPageSize` is lines.
- A JSON string that is a duration is a span, at most `logs.MaxContextSpan`, seven days.
- A negative number, a fraction, a number over the cap, a span over the cap, or any other value
  is a `BadInput` on `field` naming both forms and both caps.

Add `Fields` support for a raw value: today `Parse` takes typed destinations. Let a
`*json.RawMessage` destination take the field's raw text, so the tool can call `PaddingOf` on it.

## The printed token

The model's token is the service's page cursor, printed as it came and passed back as it came.
It is already base64url JSON with a version (step 1), it holds the scope, the range and the
padding (README decision 11), and the service checks it. This package wraps nothing and stores
nothing. `EncodeCursor`, `DecodeCursor`, `ErrNotCursor`, `ErrOldCursor` and `OldCursorMessage`
go: nothing has shipped, so no old transcript holds a token of another shape. No line is
printed with an id (README decision 8).

A token in a `since` or `until` is refused as any other value that is not a time is: the
message names the two forms a time takes.

## Tasks

1. **`RangeEndOf`** in `moment.go`, with tests for every rule above. Delete `MomentOf` and move
   its calendar arithmetic into the new function.
2. **`PaddingOf`** in `padding.go`, with tests. Add the raw-value destination to `Parse`.
3. **Delete `cursor.go`'s** `EncodeCursor`, `DecodeCursor` and their errors, and their tests.
4. **Update `tools/CLAUDE.md`'s** *The log tools' input* paragraph.

## Acceptance

- `go test -race ./internal/tools/internal/logsinput/...` passes.
- No tool is changed; `logsread` and `logswatch` fail to compile until step 4A, which is expected
  on `wip/log-fetch-api`.

## When it lands

The *The log tools' input* paragraph of `tools/CLAUDE.md` names the three readers.
