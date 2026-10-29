# A refused persisted file names itself to the user

Answers the code half of #512. The policy is the
[ADR](../adr/2026-10-29-each-persisted-format-carries-its-own-version-stamp.md); the current state
is [`docs/persisted-data.md`](../persisted-data.md). Read both, then `sidecar/CLAUDE.md` and
`src-tauri/CLAUDE.md`, before the first task.

## What it builds

Today a durable file written by a newer Kstack is refused by `sqlitemigrate` (`internal/lib/sqlitemigrate`),
and that is correct. What is missing is the path from the refusal to the user:

- `app.db` or `beehive.db` refused: `app.New` fails, `run` in `sidecar/main.go` logs `app init`
  and returns 1 before `READY`, and the webview's start screen (`src/lib/ready-gate.tsx`) reads
  *Failed to start: sidecar exited before announcing readiness*. The reason is in the log only.
- `kube/<id>.db` refused: `kubesync` already reports `StoreFailed` with the open error as its
  message, and `cluster-sync-panel.tsx` already draws it. Nothing to build; a test pins it.
- `logs/<id>.db` refused: `logstore.Manager.OpenOrCreate` returns the error to its caller. Nothing
  to build; a test pins it.

After this spec:

1. `sqlitemigrate` returns a typed error for a newer file, so a caller can tell it from any other
   migrate failure and so a test can assert on the type rather than the text.
2. The sidecar hands the reason for a failed start to the host on stdout, and the start screen
   shows it: *Failed to start: app.db was written by a newer Kstack (schema 3; this build reads up
   to 2)*. For `beehive.db` the line is *beehive.db was written by a newer Kstack*, without the
   versions: beehive's runner returns plain text, and the typed error is asked for in
   [amorey/beehive#162](https://github.com/amorey/beehive/issues/162). beehive is a separate
   project; nothing here patches it or parses its text for numbers.
3. One test per store pins that a newer file is refused with that error.

## Non-goals

Do not build any of these. Each was considered and rejected or deferred in the ADR.

- A version marker in the data directory, or any check keyed on the app version.
- A startup "upgrade step", a Go migration step in `sqlitemigrate`, or any change to a
  `0001_init.sql`.
- Discarding or rebuilding a cache on a mismatch.
- A v1 fixture database, or a CI check on `0001_init.sql`.
- A native dialog. The start screen is the surface.
- A change to `settings.json` or `host.json` handling.
- Any new GraphQL field. `StoreFailed` and its message already reach the webview.
- A reason for a failed bind. `ipc.Listen` failing in `run` keeps the generic *exited before
  announcing readiness*. Only the `app.New` branch prints `FATAL`.
- Parsing beehive's error text for its versions, or a change to beehive.

## Tasks

Do them in order. Each is one commit. Run `make test-changed` after each; the full checks are in
`docs/specs/README.md` under *Verification commands*.

### 1. A typed error in `sqlitemigrate`

`internal/lib/sqlitemigrate/sqlitemigrate.go`, where `Apply` compares `current > latest`.

- Add `type NewerError struct{ Current, Latest int }` with `Error()` reading
  `schema version 3 is newer than this build supports (2)`. Return it by pointer from `Apply` in
  place of the `fmt.Errorf`.
- Extend `TestApplyRejectsNewerThanBinary` in `sqlitemigrate_test.go`: `errors.As` into
  `*NewerError`, and `Current`/`Latest` hold the two versions.

The three callers (`appdb.Open`, `kubestore.openFile`, `logstore.openFile`) keep wrapping with
`%w`, so the type survives to their callers. Touch nothing else in them.

### 2. Per-store tests that a newer file is refused

One test each, beside the open it covers. A "newer" file is made by opening the store once, closing
it, and running on the file:

```sql
INSERT INTO schema_migrations(version, name, applied_at) VALUES (999, '0999_test.sql', 0)
```

Open the file with `database/sql` and the `sqlite` driver the test file already imports, or
through `sqlitepool.OpenWriter`, and close it before the second open. Then open the store again and
assert `errors.As(err, new(*sqlitemigrate.NewerError))`.

- `internal/appdb/appdb_test.go`: `TestOpenRefusesANewerFile`, beside
  `TestOpenFailsOnAnUnusablePath`.
- `internal/services/cluster/internal/kubestore/manager_test.go`: `TestOpenRefusesANewerFile`,
  beside `TestOpenAfterRemoveIsRefused`. Use whatever helper the neighbouring tests use to make a
  manager and open a cache id.
- `internal/services/logs/logstore/manager_test.go`: `TestOpenOrCreateRefusesANewerFile`, beside
  `TestAnOpenThatCannotOpenTheFileFails`.

Every test closes what it opens before it ends (root `CLAUDE.md`, *Testing conventions*): Windows
fails `t.TempDir` cleanup on an open handle. No sleeps.

The cache reason needs no new test: `kubesync`'s `TestArmReportsAStoreThatWillNotOpen` pins that
any open failure reaches `StoreFailed` with its message, and the message is the error text through
`capStoreFailure(safe.Safe(err))`, unchanged.

### 3. The sidecar names a failed start on stdout

`sidecar/main.go`, `run`: the `app.New` error branch that logs `app init` and returns 1.

- Before `return 1`, write one line to `stdout`: `FATAL ` followed by `safe.Safe(err)`
  (`internal/lib/safe`), which bounds the text and redacts credential shapes. One line: replace
  every `\n` **and `\r`** in the message with a space. The host's line reader ends a line at a
  bare `\r` too, so a `\r` left in would cut the `FATAL` line short and forward the rest as a
  stray log line.
- Only this branch. A failed `Start` after `READY` is already reported by the running app, and a
  quit during startup is a clean exit.
- stdout carries the host protocol and nothing else; the marker sits beside `READY ` in the
  comment above the `fmt.Fprintf` that prints `READY`.
- Make the message read as the user will see it:
  - `internal/app/runtime.go`, where `appdb.Open` fails: when `errors.As` gives a
    `*sqlitemigrate.NewerError`, the error is
    `app.db was written by a newer Kstack (schema 3; this build reads up to 2)`; otherwise keep
    the existing `open app database: %w` wrap.
  - `internal/services/cluster/service.go`, where `beehivesqlite.Open` fails: when the error text
    contains `newer than binary supports`, the error is `beehive.db was written by a newer Kstack`;
    otherwise keep the existing wrap. The comment says beehive's runner has no typed error yet and
    links amorey/beehive#162. Its text is not ours and does not change with task 1.
- Tests:
  - `sidecar/main_test.go`: **change** `TestRunFailsWhenTheAppCannotBeComposed`, which today
    asserts stdout is empty in exactly this branch. It now asserts stdout is one line starting
    `FATAL ` that contains the error text and no `READY`; update its comment to say `READY` follows
    a composed app and `FATAL` a failed one. No second test.
  - `internal/app/runtime_test.go`, beside `TestAnAppDBThatCannotOpenFailsTheBuild`:
    `TestANewerAppDBFailsTheBuildNamingTheVersions`. Plant a newer `app.db` as in task 2 and
    assert the exact text, with `(schema 999; this build reads up to 1)`. This is the only test of
    the text the user sees.
  - `internal/services/cluster/service_test.go`, beside `newTestService`:
    `TestANewerBeehiveDBFailsTheServiceSayingSo`. Plant a newer `beehive.db` the same way (its
    runner uses the same `schema_migrations` table) and assert the exact text.

### 4. The host carries the reason to the start screen

`src-tauri/src/services/sidecar/service.rs`. The drain loop is an inline closure in `spawn`, fed
by a real child, so the line handling is split out so a test can drive it. The reason rides the
readiness channel, so `ready()` reads one source and cannot see a reason before it is stored.

- Add `const FATAL_MARKER: &[u8] = b"FATAL ";` beside `READY_MARKER`.
- Replace the `watch::channel(false)` with a channel of a small enum, say
  `enum Startup { Pending, Ready, Failed(String) }`, defined in this file. The `Pending` state is
  the initial value.
- Split the `CommandEvent::Stdout` handling into `fn stdout_line(line: &[u8], startup:
  &watch::Sender<Startup>)`: a line starting `READY_MARKER` sends `Ready` and keeps the
  `tracing::info!("sidecar ready")` line, which `src-tauri/CLAUDE.md` promises; a line starting
  `FATAL_MARKER` sends `Failed(rest)`, where `rest` is the remainder decoded with
  `String::from_utf8_lossy` and **`trim_end`** (the reader keeps the trailing newline, as
  `forward_sidecar_line` in `logs.rs` already allows for); any other line goes to
  `forward_sidecar_line`. Discard each send's result with `let _ =`, as the current code does: it
  fails only when no receiver is left, which is teardown, not an error. The drain loop calls it.
- Split the wait in `ready()` into `async fn wait_ready(mut rx: watch::Receiver<Startup>) ->
  Result<()>`: `Ready` is `Ok`; `Failed(msg)` is `AppError::Io(std::io::Error::other(msg))`; the
  sender dropping while `Pending` keeps the existing *exited before announcing readiness*; the
  budget elapsing keeps the existing `TimedOut`. `ready()` calls it with a clone of its receiver.
- Tests in the `tests` module at the bottom of `service.rs`: `stdout_line` with a `READY ` line, a
  `FATAL ` line with a trailing newline (the stored text has none), and a plain line (the channel
  stays `Pending`); `wait_ready` with `#[tokio::test(start_paused = true)]` for each of the four
  outcomes, driving the sender by hand.
- `src/lib/ready-gate.tsx`: draw `state.message` through `VisibleText`
  (`src/components/widgets/visible-text.tsx`) instead of as bare JSX text. The sidecar's text is
  bounded and redacted but not control-stripped, and the root invariant is that every untrusted
  string is drawn through `VisibleText`. `ready-gate.test.tsx` keeps passing as written; add no
  test.

### 5. The register

`docs/persisted-data.md`:

- *Pinned by* for App DB, Cluster cache and Log cache: the tests from task 2; for App DB and
  Control-plane store also the text tests from task 3, `TestRunFailsWhenTheAppCannotBeComposed`,
  and the `wait_ready` tests from task 4 for the message path.
- Delete the *Open on #512* section.
- *Newer file* for App DB and Control-plane store: `refused; the sidecar does not start and the
  start screen names the file`.
- `sidecar/CLAUDE.md`, *Persisted formats*: one sentence that `run` prints `FATAL <reason>` on
  stdout when `app.New` fails and the host shows it on the start screen.
- `src-tauri/CLAUDE.md`, *Sidecar IPC*: the sentence that stdout carries the readiness protocol
  now says a `FATAL <reason>` line ends readiness with that reason, which `ready()` returns.

## Done when

- `sqlitemigrate.Apply` returns `*NewerError` for a newer file, and its test asserts the type.
- A newer `app.db`, `kube/<id>.db` and `logs/<id>.db` is refused with that error, one test each.
- A sidecar whose `app.db` or `beehive.db` is newer prints `FATAL <reason>` on stdout, exits 1, and
  the start screen shows *Failed to start: <reason>*, naming the file. For `app.db` the reason
  holds both versions; for `beehive.db` it does not, until amorey/beehive#162 lands.
- The start screen draws the reason through `VisibleText`.
- The register's *Pinned by* and *Newer file* columns are updated for those rows, *Open on #512*
  is gone, and both `CLAUDE.md` files describe the `FATAL` line.
- `make test-changed`, `make lint`, `make vet` pass. Coverage gates are not lowered.

## Handoff

Report changed files, the test names added, the verification commands run with their results, and
anything left failing. If a step cannot be done as written, stop and say why rather than build
around it; in particular, do not add a mechanism listed under *Non-goals*.

## When it lands

This spec is deleted in the PR that lands it. `docs/persisted-data.md` and `sidecar/CLAUDE.md` are
updated by task 5; no ADR is needed, the decisions are already in the 2026-10-29 ADR.
