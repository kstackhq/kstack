---
title: Durations are ISO 8601
scope: sidecar
status: Proposed
---

# Durations are ISO 8601

**Needs:** rung 5, landed. **Unblocks:** rung 7.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Wherever a log tool takes a moment, it takes one of two forms: an RFC 3339 time such as
`2026-10-18T09:14:20Z`, or an ISO 8601 duration such as `PT15M`, measured from now. Today the
duration form is Go's, `15m` or `1h30m`. This rung replaces it. Both forms are then standards
named by number, one parser reads every duration, and a duration can say *a month* or *a week*,
which Go's cannot.

The fields are `LogsView`'s `anchor` and `LogsRead`'s `since` and `until`. Rung 7's `LogsWatch`
takes `since` and `until` the same way, with its `until` running ahead of now, so this rung also
states the one rule that makes a duration mean the same thing in every field.

This rung adds:

- **Sidecar.** `logsinput.MomentOf` reads an ISO 8601 duration with one anchored expression and
  applies it on the calendar, in UTC, in the direction the caller names. The refusals and the
  schemas name the new form, and a refused moment names the clock.
- **Prompt.** `logsread.md` and `logsview.md` say the form and the direction rule.

Nothing changes on the wire or in the webview. The actions already carry resolved moments. No
dependency is added: the grammar is small enough to read by hand.

## What is not in this rung

- **No Go durations.** `15m` is refused, with a message that shows `PT15M`. See *Decisions*.
- **No `now` in the context block.** The block already carries `today`; the model learns the
  time of day from the tools' receipts and from a refusal. See *Decisions*.
- **No time zone for the user.** This is a choice, not a limit: the sidecar runs on the user's
  machine and knows its zone. Every moment here is UTC, and a user's *at five* is left to a later
  rung to place in their zone.

## Design

### 1. The grammar

`MomentOf(s, now, dir)` is the moment `s` names on the clock `now`. It tries, in order:

1. **An RFC 3339 time**, unchanged from the current code, through `time.RFC3339Nano`, returned
   in UTC.
2. **An ISO 8601 duration**, read as below.

Anything else is not a moment, and the caller refuses the field.

**A duration is one anchored expression plus `strconv`.** No library: the one in `go.mod`
tokenises loosely, accepting `P`, `PT5M1H`, `PT1M2M` and `P1.5D`, and would leave every rule
below to a second pass. The expression is:

```text
^P(?:(\d{1,9})Y)?(?:(\d{1,9})M)?(?:(\d{1,9})W)?(?:(\d{1,9})D)?(?:T(?:(\d{1,9})H)?(?:(\d{1,9})M)?(?:(\d{1,9})(?:\.(\d{1,9}))?S)?)?$
```

and after the match, two checks the expression cannot make: at least one unit is present, and a
`T` is followed by at least one time unit. So:

- units come in the standard's order, each at most once: `PT5M1H` and `PT1M2M` are refused;
- `P`, `PT` and `P1DT` are refused, as is `PT0S`: a duration whose every unit is zero names no
  moment, and the check is on the values, not the spelling;
- weeks combine with the other units: `P1W3D` is ten days. The standard's stricter form costs a
  rule and buys nothing here;
- a fraction is allowed in seconds alone: `PT1.5S` is a second and a half, `PT1.5H` and `P1.5D`
  are refused. Half an hour is `PT30M`; half a day has no calendar meaning;
- a count has at most nine digits, which with the sum below keeps every value in range;
- a leading `-` is refused. A duration has no sign of its own; the direction is the caller's.

**A duration measures from now, toward where that end can be.** `dir` is `Behind` or `Ahead`.
`LogsView`'s `anchor` and `LogsRead`'s `since` and `until` pass `Behind`: a view opens in the
past, a read reads the past, so `PT15M` is fifteen minutes ago in all three. Rung 7's `LogsWatch`
passes `Behind` for `since` and `Ahead` for `until`, since a watch's start can only lie behind and
its end only ahead. `Ahead` has no caller until rung 7; it lands here, tested, because the rule
is this rung's.

**The maths runs in UTC.** `now` is converted to UTC first, whatever zone the clock gave it. In
production the clock is `time.Now`, which is local, and local days are 23 or 25 hours twice a
year. In UTC `P1D` is always 24 hours, and the result is the same on every machine.

**The duration is applied in three steps, months first.**

1. **Years and months**, through `time.AddDate(y, m, 0)`. `AddDate` normalises an overflowing
   day forward, so one month back from 31 March would be 3 March, still inside March. So when
   this step moved the day of the month, the target month was shorter than the day asked for,
   and the moment is set to that month's last day: one month back from 31 March is 28 February,
   or 29 in a leap year, and a year back from 29 February is 28 February. Only this step can
   overflow a day, which is why it runs alone and is checked alone.
2. **Weeks and days**, through `time.AddDate(0, 0, 7w+d)`. A day always moves the day of the
   month, so no check follows this step.
3. **Hours, minutes and seconds**, summed first as whole seconds in an `int64`
   (`h*3600 + m*60 + s`), which nine-digit counts cannot overflow, then applied as one
   `time.Duration`. Summed as a `time.Duration` directly, `PT999999999H` would wrap: the
   `Duration` type holds about 292 years, and Go wraps an integer product silently. The fraction
   of a second is added last, as nanoseconds: its digits are padded on the right to nine before
   they are read, so the `5` of `PT1.5S` is 500,000,000 ns, not 5.

So `P1M` on 15 March is 15 February, which a fixed number of days cannot give, and `P1D` on 31
March is 30 March, which the clamp never touches.

**The result is bounded.** A time part of more than `math.MaxInt64` nanoseconds, about 292 years,
is refused before it is applied; it names no moment a log holds. A duration whose moment lies
before the Unix epoch, when `Behind`, or after the year 9999, when `Ahead`, is refused. Every
intermediate value is in range, so each bound is a comparison, never a wrap. A duration the bound
refuses gets the field's ordinary refusal, which names the form and the clock; that is on
purpose, since the clock is what the retry needs and a second message would teach nothing.

### 2. What the model is told

Three places name the form, and change together:

- **The schemas.** `logsview`'s `anchor` and `logsread`'s `since` and `until` descriptions read
  *an RFC 3339 time, or an ISO 8601 duration back from now such as PT15M*. `logsread`'s keep
  the cursor clause.
- **The refusals.** `anchorMessage` and `endMessage` name the same forms. **A refused moment
  also names the clock**: *now is 2026-10-18T13:50:00Z*. The model has no clock of its own, so
  a refusal that only names the form leaves it to guess a time it cannot know. With the clock in
  the refusal, the retry can be exact. The two messages become functions of `now`; the tests
  that compare against them already run on a fixed clock.
- **The prompts.** `logsread.md`'s sentence on `since` and `until` names the form, and adds the
  direction rule in one sentence: *a duration measures from now, toward where that end can be;
  here both ends lie in the past*. `logsview.md` says the same of `anchor`, and its example
  *"the last two minutes" is `anchor: 2m`* becomes `anchor: PT2M`.

Six prompt goldens change, `chat_linux`, `chat_macos`, `chat_windows`, `chat_no_shell`,
`subagent_linux` and `monitor_linux`; run `make prompts`.

### 3. The direction in code

`MomentOf` takes the direction as a parameter rather than a sign on the value, so a caller
cannot pass a duration the wrong way by forgetting to negate it. The two directions are a
two-valued type in `tools/internal/logsinput`, and each caller names its own at the call site.

`logsread`'s `endOf` tries `MomentOf` before the cursor, as it does now. A cursor is base64url
JSON and starts with `eyJ`, so it can never read as a duration, and the order is safe.

## Decisions this rung asks for

- **ISO 8601 replaces Go's grammar; it does not sit beside it.** Reason: one grammar means one
  parser, one refusal message, one sentence in each prompt and one form in every example. Two
  grammars would need the refusal to teach both and the model to pick, and `1m` would be a
  minute in one and refused as a month in the other. The cost: the model knows `15m` from
  `kubectl logs --since` and will send it sometimes. The refusal shows `PT15M` and the clock, so
  that is one retry, not a wrong answer. If transcripts show the retry is common, the fix is one
  line that accepts Go's form too.
- **One expression, no library.** Reason: the library in `go.mod` checks none of the rules the
  grammar needs, so every one would be a second pass over its output, and its conversion treats
  a month as 30.4 days, which this rung does not use. An anchored expression enforces order,
  uniqueness, whole calendar units and the fraction rule in one place a reader can check against
  the standard. `go.mod` does not change.
- **Years and months are accepted, on the calendar.** Reason: *a month ago* is an ordinary
  thing to ask for, and the standard spells it. A calendar month is what the user means by it,
  so the moment is found with `AddDate`, never by a fixed number of days. The cost: a month is a
  different length each time, which is what a calendar month is.
- **A month end clamps to the target month's last day.** Reason: *the last month* asked on 31
  March means from the end of February, not from 3 March. Clamping is what every date library
  that offers calendar months does. The cost: the calendar part is applied in two steps so the
  check sees only the months, and two moments a month apart are not always a month apart going
  forward again, which no caller here does.
- **UTC for the calendar maths.** Reason: it is deterministic and the same on every machine, and
  every moment the tools already carry is UTC. The cost: a user's calendar day in a zone far from
  UTC is not the day the maths uses, which the time-zone rung will have to address with the
  rest.
- **A fraction in seconds alone.** Reason: it keeps the expression to one optional group and the
  rule to one sentence. The standard allows a fraction in the smallest unit, so `PT1.5H` is
  valid ISO 8601 and is refused here; `PT90M` says the same thing. A calendar unit never takes
  one, since half a month has no meaning on the calendar.
- **Bounds on the result and on the time part, not on the input alone.** Reason: a nine-digit
  cap on each count keeps the calendar arithmetic in range, but `P999999999Y` still names a
  moment no log holds, and `PT999999999H` would wrap a `time.Duration` into a wrong moment inside
  the bound. Summing the time part in seconds and refusing it past 292 years makes the wrap
  impossible; refusing a moment before the epoch or after 9999 catches the rest with one
  comparison and gives the model a refusal it can act on.
- **A refused moment names the clock, and the context block does not.** Reason: the block
  already carries `today`, which is what an absolute time the user named needs: *since 9am* is
  today's date plus a time. The time of day is needed only to turn a relative time into an
  absolute one, which a duration does for the model, and for the retry after a refusal, which
  now carries the clock. A `now` field in the block would change on every send, and the block is
  sent only when it changed, so every message would carry the whole block again. The receipts
  and headers already state the moment they resolved, so after one call the model knows the
  clock to within a turn.
- **The direction is the caller's parameter, not the value's sign.** Reason: a sign on the value
  would let the model send `-PT15M` and let a caller forget to flip it. A parameter makes each
  field's direction a line of code beside the field, which a test pins.

## Tasks

1. **The grammar.** `MomentOf` with the direction, the expression, the UTC conversion, the
   three-step arithmetic with the month-end clamp, the fraction rule and the two bounds. Its
   tests in `tools/internal/logsinput`.
2. **The callers.** `logsview`'s `anchorOf` and `logsread`'s `endOf` pass `Behind`; their
   refusals name the form and the clock; the tests that spell `2m` spell `PT2M`.
3. **The words.** The two schemas, the two prompts and the `logsview.md` example, then
   `make prompts`.
4. **Docs.** As listed under *When it lands*.

## Tests

In `tools/internal/logsinput`:

- Each of `P1W`, `P3D`, `PT15M`, `PT1H30M`, `P1DT12H`, `P1W3D`, `PT1.5S` is a moment behind
  `now`, and the same strings are a moment ahead with `Ahead`. The moments are asserted exactly:
  `PT1.5S` is one and a half seconds, `PT0.001S` one millisecond, `P1W3D` ten days.
- `P1M` from 15 March is 15 February. `P1M` from 31 March 2027 is 28 February, and from 31
  March 2028 is 29 February. `P1Y` from 29 February 2028 is 28 February 2027. `P1D` from 31
  March is 30 March, and `P1M3D` from 31 March 2027 is 25 February: the clamp sees the months
  alone.
- `P1D` across a daylight-saving change, on a clock in a zone that has one, is 24 hours. The
  test file imports `time/tzdata` so the zone is there on every runner; the case pins the
  conversion to UTC.
- Each of `15m`, `-PT15M`, `P`, `PT`, `P1DT`, `PT0S`, `P0D`, `PT5M1H`, `PT1M2M`, `P1.5D`,
  `PT1.5H`, `P1.5DT1H`, `PT1234567890S`, `PT999999999H`, `PT999999999M`, `P999999999Y` behind
  a near-epoch clock, `P999999999Y` ahead of a near-9999 clock, and `yesterday`, is not a moment.
- An RFC 3339 time is returned unchanged, in UTC, whatever `dir` says.

In `logsview` and `logsread`:

- `TestTheAnchorIsReadOnTheClock` and `TestAnEndIsAMomentADurationOrACursor` read `PT2M`.
- An unreadable anchor's and end's refusal names the form and the clock the test fixed.

## Security

**Widens nothing.** The same fields take the same two kinds of value, in a different spelling of
one of them. A refusal now carries the sidecar's clock, which is not cluster data and not a
secret. The expression is anchored and every count is bounded, so no input can make the parser
loop or overflow.

## When it lands

- `internal/tools/CLAUDE.md`: `logsinput`'s line says `MomentOf` reads an RFC 3339 time or an
  ISO 8601 duration, applied on the calendar in UTC in the caller's direction, with a month end
  clamped.
- This README's row for rung 6 keeps its link until the sequence lands, and its paragraph says
  what this rung builds.

## Verification

`bash scripts/sandbox-dev-setup.sh` first, then `make test-changed` and `make prompts`.

By hand, in a chat:

- Ask *show me webapp's logs from the last fifteen minutes*. The `LogsView` call's `anchor` is
  `PT15M`, and the view opens fifteen minutes back.
- Ask *errors in webapp over the last month*. The `LogsRead` call's `since` is `P1M`, and the
  header's range starts on today's date last month, or the month's last day when today's date
  does not exist in it.
- Send a call with `since: "15m"` through the fake provider. The refusal names `PT15M` and the
  current time.
