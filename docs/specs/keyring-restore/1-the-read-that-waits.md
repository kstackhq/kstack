---
title: The read that waits
scope: sidecar
status: Planned
---

# The read that waits

**Answers:** [#1094](https://github.com/kstackhq/kstack/issues/1094), step 1 of
[the sequence](README.md). **Needs:** saved provider keys (#1084), on `main`. **Unblocks:** step 2.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

This step builds what step 2 restores with:

- `lib/oskeyring` gains a read with no deadline that a context can abandon, and a gate that opens
  the keyring once for every reader, retrying a keyring that is not on the bus yet.
- `services/internal/signer` gains `Pending`, a binding that waits for a restore or the service's
  end.
- Every connector takes its keyring as a value, and `build` passes one to all of them.

After it, each `New` still reads the keyring before it returns. One thing a user can see does
change: a dismissed unlock prompt is shown once, where today it returns for each service.

## Not in this step

- Moving any read off the launch path, and `Restoring`. Step 2.
- Any change to GraphQL, gRPC, the webview or the host.

## Design

### 1. `oskeyring.Read` and `Delete`

```go
// ErrUnavailable is a keyring that is not there to ask yet.
var ErrUnavailable = errors.New("oskeyring: the keyring is not available")

// SlowAfter is how long a read may take before it is logged.
const SlowAfter = 30 * time.Second

// Read is account's value. It has no deadline, and logs once past slowAfter.
// When ctx ends first it answers ctx's error and leaves the keyring's own
// call to finish alone.
func Read(ctx context.Context, k Keyring, account string, slowAfter time.Duration) (string, error)

// Delete is k.Delete(account), left to finish alone when ctx ends first.
func Delete(ctx context.Context, k Keyring, account string) error
```

- Each runs the keyring's call on a goroutine of its own and takes the answer over a buffered
  channel, so an abandoned call blocks nothing and its answer is dropped.
- Past `slowAfter`, `Read` logs one `Warn`, `keyring: still waiting for the keyring`, with the
  `account` and never the value, and keeps waiting.
- Neither retries. The retry is the gate's (§3), so it is made once for every reader.

### 2. `ErrUnavailable` on Linux

`osKeyring` answers `ErrUnavailable` on Linux alone, in `unavailable_linux.go`, from `Get`, `Set`
and `Delete`. It wraps an error that is a D-Bus error reply named
`org.freedesktop.DBus.Error.ServiceUnknown`, `…NameHasNoOwner`, or anything under
`org.freedesktop.DBus.Error.Spawn.`. `go-keyring` calls with auto-start on, so a Secret Service
that is not running yet answers `ServiceUnknown` or a `Spawn` error; `NameHasNoOwner` is kept for
a bus that answers it.

- **The match takes both `dbus.Error` and `*dbus.Error`.** godbus puts the value on a failed call
  (`conn.go`, `finalizeWithError`) and builds the pointer in `NewError`; the test pins both.
- **No session bus is not `ErrUnavailable`.** `dbus.SessionBus()` then fails with a plain error of
  no stable type, which `go-keyring` returns as it is. A desktop session has its bus before any
  app starts, so a machine with none has no keyring this launch: the gate fails at once (§3), and
  every service is signed out, as today.

`unavailable_other.go` (`//go:build !linux`) wraps nothing. `godbus/dbus/v5` becomes a direct
dependency. Task 1's manual check (*Tasks*) confirms the names.

### 3. The gate

```go
// Pace is how the gate retries a keyring that is not there yet.
type Pace struct {
	// Retries is how many more times a probe that answered ErrUnavailable is made.
	Retries int
	// Backoff is the wait before the first retry; each later one doubles it.
	Backoff time.Duration
}

var DefaultPace = Pace{Retries: 4, Backoff: 500 * time.Millisecond}

// Gate is k with its reads behind one opening.
func Gate(k Keyring, pace Pace) Keyring
```

`oskeyring.New(service)` returns the OS keyring behind `Gate(…, DefaultPace)`. A fake has none
unless a test wraps it.

**The gate opens the keyring with a probe: a `Get` of `keyring-probe`, an account nothing ever
writes.** Its answer can only be about the keyring, never about an entry, so one service's bad
entry cannot sign out the rest. On Linux the probe raises the unlock prompt, since `go-keyring`
unlocks the collection before it searches it.

**The opening happens once per gate, and its result stands for the process.** The first `Get` to
arrive starts it, on a goroutine of the gate's own, since no one caller's context may end it:

1. The probe is made.
2. `ErrUnavailable` is retried after `Backoff`, `2×Backoff`, … up to `Retries` times.
3. `ErrNoEntry`, or a value, **opens** the gate. Any other error, and `ErrUnavailable` after the
   last retry, **fails** it, and the gate keeps that error. **A dismissed unlock prompt is such an
   error, and is not retried.**

| Call | Before the opening has ended | Gate open | Gate failed |
| --- | --- | --- | --- |
| `Get` | waits for it | calls the keyring | answers the kept error, calling nothing |
| `Set`, `Delete` | calls the keyring | calls the keyring | calls the keyring |

So, whenever each reader arrives and however fast the keyring answers:

- **A locked keyring raises one prompt for the launch's reads.** Dismissed, every read fails with
  no second prompt. This holds wherever the probe raises the prompt: Linux does, and macOS is
  task 1's manual check.
- **A Secret Service that is not on the bus is asked `1 + Retries` times in all**, not per reader.
- **Once the gate is open each read is its own.** An entry the keyring cannot hand over (a value
  macOS's `security` returns that `go-keyring` cannot decode, an item `GetSecret` fails on) fails
  that service's read alone, as today.
- **No `Get` reaches a keyring whose opening failed, for the rest of the process.** Nothing reads
  after a restore today; the follow-up that reads again ([the sequence](README.md#not-in-this-sequence))
  starts a new opening.

**Writes are not gated.** Both follow a user's action, and a `Connect` after a dismissed prompt
must reach the keyring, and may prompt, to sign in. No write can arrive during an opening: after
step 2 every service is `Restoring` until its own read has answered, and refuses every change
until then. Before step 2 every read is made inside `build`.

The only `Get`s are the restores' (step 2), and the probe.

### 4. `oskeyringtest`

- `Hold(kinds ...Kind) (release func())`: every call of those kinds made from now waits for
  `release`; with no kind, every call. A test registers `release` in a `t.Cleanup` after its
  `TempDir`, so no goroutine outlives it.
- `FailGets(n int, err error)`: the next `n` `Get`s answer `err`, whatever the account. Under a
  gate the first `Get` is the probe, so this is how a test fails the opening.
- `FailGet(account string, err error)`: every `Get` of `account` answers `err`, and no other
  account's does. It is how a test fails one entry's read with the gate open.
- `Calls() []Call`: each call's kind and account, in order, so a test asserts no `Set` or
  `Delete` came before a read answered.

### 5. `signer.Pending`

```go
// Pending is a binding made before the restore landed. Each Sign waits for
// ready, then signs with what resolve answers. The request's context ends the
// wait with its error, and done ends it with llm.ErrSignedOut.
func Pending(ready, done <-chan struct{}, resolve func() llm.Signer) llm.Signer
```

`done` is the service's own end: its context's `Done()`. A restore that `Close` cancels never
closes `ready`, so without `done` a bound request with a live context would wait for good.

`Refused` calls `resolve` once `ready` is closed and does nothing before. `resolve` is called at
each use, so it must answer the same binding every time.

### 6. One keyring for every service

**`Config.Keyring oskeyring.Keyring` replaces `Config.KeychainService`** in `oauthconn`, `chatgpt`,
`google`, `kstackcloud`, `openrouter`, `github` and `aws`, as `providerkeys` has it. Each
package's `withKeyring` option goes, and its rig passes its fake through `Config`.

`app/runtime.go`, `build`, makes one keyring before the connectors,
`cmp.Or(cfg.keyring, oskeyring.New(keychainService))`, and passes it to each `New` and to
`providerkeys.New`. They then share the gate, and one fake reaches every service in a test.

## Tasks

| # | Task | Status |
| --- | --- | --- |
| 1 | `oskeyring`: `ErrUnavailable` and its Linux file, `Gate` and `Pace`, `Read`, `Delete`; the manual check below | Planned |
| 2 | `oskeyringtest`: `Hold`, `FailGets`, `FailGet`, `Calls` | Planned |
| 3 | `signer.Pending` | Planned |
| 4 | `Config.Keyring` in every connector; one keyring in `build`; every rig and test that passed `KeychainService` or `withKeyring` | Planned |

**Task 1's manual check**, with a throwaway program over `oskeyring.New`, its results recorded in
the pull request and this spec corrected where they differ:

- Linux, the Secret Service stopped: the probe's error, its Go type and its D-Bus name. Then with
  the service unit masked, so auto-start fails.
- Linux, no session bus (`DBUS_SESSION_BUS_ADDRESS` unset, no `XDG_RUNTIME_DIR` bus): the probe
  fails at once and is not retried.
- Linux, the login keyring locked: the probe raises one prompt; unlocked, it answers `ErrNoEntry`;
  dismissed, it answers an error that is not `ErrUnavailable`.
- macOS, the login keychain locked (`security lock-keychain`): whether the probe, a
  `find-generic-password` for an item that is not there, raises the unlock dialog. If it does not,
  the gate opens on `ErrNoEntry` and the first real read raises the dialog; say here which, and
  whether the reads that follow share the one dialog.

Verification is `docs/specs/README.md`'s *Verification commands*: `make test-changed` while
working, `make check-changed` before the pull request. A new `GOOS` split joins Vet · Go,
`scripts/staticcheck.sh`, `scripts/check-go-fix.sh` and `scripts/vet-go-changed.sh`'s `configs`
only if they do not already cover `linux` and one other.

## Acceptance

Every test uses `oskeyringtest` and never sleeps.

- `Read` logs once past `slowAfter`, naming the account and no value
  (`TestReadLogsASlowKeyringOnce`).
- `Read` and `Delete` answer `ctx`'s error when `ctx` ends while the keyring holds the call
  (`TestReadEndsWithItsContext`, `TestDeleteEndsWithItsContext`).
- The opening is one `Get`, of `keyring-probe`. Every reader gets through once it answers
  `ErrNoEntry`, those that waited and those that came after (`TestTheGateOpensOnAnAnswer`).
- A probe that fails at once with an error that is not `ErrUnavailable` is the keyring's only
  `Get`: readers started one after another, each after the last has answered, all answer that
  error (`TestAFailedOpeningAnswersEveryReader`, with immediate errors and staggered callers).
- An unavailable keyring is probed `1 + Retries` times at the paced backoff, whatever the number
  of readers and whenever they arrive; it opens if a retry answers, and fails after the last
  (`TestTheGateRetriesAKeyringThatIsNotThereYet`, staggered callers likewise).
- With `FailGet` set for one account, that account's read fails and every other account's reaches
  the keyring and answers, whichever read came first (`TestABadEntryFailsItsOwnReadAlone`).
- A `Set` and a `Delete` reach the keyring during an opening and after a failed one
  (`TestWritesAreNotGated`).
- `ServiceUnknown`, `NameHasNoOwner` and a `Spawn` name each wrap `ErrUnavailable`, as a
  `dbus.Error` and as a `*dbus.Error`; another D-Bus name and a plain error do not
  (`unavailable_linux_test.go`).
- `Pending` signs once `ready` closes, ends with the request's context before it, and tells
  `Refused` to the resolved binding alone (`TestPendingWaitsForReady`).
- A `Pending` whose request context is live answers `llm.ErrSignedOut` when `done` closes with
  `ready` still open (`TestPendingEndsWithItsService`).
- `app.New` passes one keyring to every service: with `cfg.keyring` set, a restored entry of each
  connector's is read from it (`TestEveryServiceReadsTheOneKeyring` in `app`).

`coverage / Gate` is green.

## Things to watch

- **The D-Bus error must survive `go-keyring`'s wrapping.** It returns the call's error unwrapped
  today; the test pins the names and both types, and task 1's manual check pins the path.
- **A user can see this step.** Today a dismissed unlock prompt comes back for each service that
  reads after it. From this step every later read fails without asking, so it is shown once.
- **A failed opening is kept.** After it a restore reads nothing, which is today's outcome for an
  unreadable keyring, reached without asking again.
- **One more keyring call per launch**, the probe: on macOS one `security` process.
- **A keyring that locks again after it opened is not gated.** Each later call then prompts on its
  own, as today.

## When it lands

`oskeyring` has no guide: its package comment and each function's say what is above. Each
connector's guide and `providerkeys`' already say tests fake the keyring; correct any that names
`withKeyring`. This spec stays, marked built, until step 2 is complete.
