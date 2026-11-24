# Step 3: Executables

Moves the user's registered executables into `app.db`. Part of [the sequence](README.md), whose
shared rules apply.

## What moves

| In `settings.json` today | Becomes |
| --- | --- |
| `executables` | `executables` |

The executables describe one machine, so the table has no cluster scope
([Not in this sequence](README.md#not-in-this-sequence)).

## The table

Migration `0008_executables.sql`:

```sql
-- executables: the programs the user asked the probe to check, in the order added.
CREATE TABLE executables (
  name       TEXT    PRIMARY KEY,
  invocation TEXT    NOT NULL,
  created_at INTEGER NOT NULL
) STRICT;
```

They are read `ORDER BY created_at, rowid`, since two can be added in the same millisecond.

## Reading and writing

- **`Executables(ctx) ([]Executable, error)`** is every registered row, in order, a refused one
  with its `Refusal`.
- **`RegisterExecutable(ctx, name, invocation) error`**, in one transaction, looks the name up,
  then checks with `CheckExecutable`, then inserts. **A stored name wins**: a name already
  stored, refused or not, is `ErrExecutableListed`, even when `CheckExecutable` would refuse it
  too. So a registered name that a release made curated answers `ErrExecutableListed`, and once
  the user removes that row, the curated-name refusal.
- **`RemoveExecutable(ctx, name) error`** deletes the row, a refused one too. A delete that
  changes no row is `ErrExecutableNotRegistered`.
- **`WatchExecutables()`** pings after each register or remove commits, on `appdb.KeyExecutables`.
  It carries no value: a watcher reads `Executables` again on each ping.

`Executable` gains `Refusal string`, empty for a row Kstack uses, as `envvars.Var` has. Its JSON
tags go with the file.

## A refused executable

The read-back is `CheckExecutable`, the one shape check, as on register. The primary key makes a
name listed twice impossible, so that refusal goes.

**A refused executable is never probed, and the probe checks for itself.** `executableList`,
under both `ReportOver` and `runProbe`, runs `CheckExecutable` on each registered entry that
carries no `Refusal`, and sets the `Refusal` of one it refuses; an entry that already carries
one keeps it. `runProbe` then skips every entry with a `Refusal`. So the shape check guards
every invocation the probe runs, whatever its caller passed. The curated entries are not
checked, so a curated executable still runs when a registered row has its name. A refused
executable only widens what is probed, so nothing else is restricted while one is refused.

The report lists a refused executable where its row sits, `registered` and not probed. Its
`Refusal` replaces the probe's result: `probed` and `ok` false, `error` empty.

A registered executable becomes refused when a release adds its name to `CuratedExecutables`.
The curated one of that name is still probed, and Remove takes the registered row.

## A read that fails

A failed read is an error, never an empty list.

| Reader | A failed read |
| --- | --- |
| `sandboxExecutables` | answers the error |
| `sandboxExecutablesProbe` | answers the error and starts no probe, the curated executables' included. It reads the list once, and that read feeds both the probe it starts and its answer, so no read fails after the probe has started |
| `sandboxExecutableRegister` and `sandboxExecutableRemove`, after their write committed | `KSTACK_SAVED_UNREAD` (`savedUnread`) |
| `sandboxExecutablesWatch` | ends the watch with its reason (`recordWatchFailure`), as `sandboxFoldersWatch` does |
| The probe after a `PATH` sync (`app`) | starts no probe, and logs a warning that the executables could not be read |

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`appdb`**: `KeyExecutables`.
- **`services/settings`**: `Executables`, `RegisterExecutable`, `RemoveExecutable` and
  `WatchExecutables` on `app.db`, laid out as the cloud platform switches are, with their
  statements in `statements.go`. A write notifies `KeyExecutables` after its commit.
  `Executables` leaves `Settings`. `checkExecutables`, its *listed twice* refusal and
  `FieldExecutables` are deleted, and `checks` keeps `checkPath` alone.
- **`tools/bash`**: `executableList` checks each registered entry and lists a refused one with
  its `Refusal`, and `runProbe` skips it. `ExecutableReport` carries the `Refusal` through its
  embedded `Executable`.
- **`graph`**: each resolver reads `Executables` once (`executablesReport`), and a register or
  remove answers through `executablesAfter`. `sandboxExecutablesWatch` subscribes to `WatchExecutables` in place of
  `Settings.Subscribe`, then reads once before its first frame, since a ping is not
  current-on-subscribe. The `ExecutableProber` comment says a refused executable is listed and
  not probed.
- **`app`**: the `PATH` sync's probe reads `Executables` with the start's context.

## The wire

- `SandboxExecutable` gains `refusal: String`: the reason in the user's words, null for an
  executable Kstack probes. Draw it through `VisibleText`.
- `sandboxExecutableRemove` takes a refused executable. Its description and
  `sandboxExecutableRegister`'s say a name a refused row holds is already listed.
- `sandboxExecutables` and `sandboxExecutablesProbe` answer an error when the list cannot be read.

The webview follows in `src/lib/sandbox-executables.tsx` and the Settings dialog's
`sandbox.tsx`. It is done when these hold, each with a test:

- A refused executable shows its reason in place of a probe result, and can be removed.
- A register answered `KSTACK_SAVED_UNREAD` was taken: the form clears and the message stays
  drawn, as `permission-settings.tsx` treats one (`savedUnread`).
- When a registered name is also curated, both lists key each row by whether it is registered
  and its name, so both rows draw. Remove takes the registered one and the curated one stays.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0008` and its test, which builds the schema before it, seeds a row in each earlier table, migrates, and checks that every seed survives and that a second row of one name is refused. | Done |
| 2 | `services/settings`, `tools/bash`, `graph`, `app`: the executables on `app.db`, with each reader's failed read as above, tested with a failed query and a failure after some rows were read. A refused row is listed and never probed (`TestAProbeRunsNoRefusedExecutable`), and so is a malformed registered entry passed to the probe with no `Refusal`, while a curated executable of the same name still runs. Registering a stored name that is now curated answers `ErrExecutableListed`, and after its removal the curated-name refusal. The probe mutation reads the list once. A register or remove pings the watch once it commits. | Done |
| 3 | `graph`, `src/`: the wire and the webview. | Done |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add a row for `executables`; take them out of
  the *Sidecar settings* row.
- [`security-model.md`](../../security-model.md): the executable probe's row says a registered
  executable is read from `app.db`, a refused one is listed and never run, and names
  `TestAProbeRunsNoRefusedExecutable` beside `TestCheckExecutableRefusesEachBadShape`.
- `sidecar/internal/services/settings/CLAUDE.md`: the executables paragraph. A refused one is
  listed with its reason and never probed, no longer dropped.
- `src/components/settings/CLAUDE.md`: the Executables bullet names a refused row.
