# Move the settings into app.db

The build order for [#1127](https://github.com/kstackhq/kstack/issues/1127). Each spec here is one
pull request into `wip/settings-in-app-db`, built in order. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply.

## Why

Today the sidecar keeps some settings in `<data>/settings.json` and others in `app.db`.

The file was designed to be edited by hand. Nobody edits it: every value in it is written by
Kstack, from a Settings mutation or the `PATH` sync. Much of the code around it
(`internal/lib/jsonsettings`) exists only to survive a hand edit.

This sequence moves the file's settings into `app.db`, one area per step, and then removes the
file's code.

## Where the sequence ends

- **Nothing reads or writes `settings.json`.** `internal/lib/jsonsettings` is deleted.
- **Every setting is a typed row in `app.db`**, in the table its step names.
- **No field is "held".** A row Kstack cannot use is refused, listed with its reason, and removed
  by the user in Settings.
- Settings works as before, except that it no longer talks about a file.

## The steps

| Step | Spec | Moves | Migration | After it |
| --- | --- | --- | --- | --- |
| 1 | [Permissions](1-permissions.md) | The approval modes, the always rules and a chat's grants, into `approval_modes` and `permission_rules` | `0006` | The file keeps the sandbox group and the executables. |
| 2 | [Sandbox settings](2-sandbox-settings.md) | The cloud platform switches, into `sandbox_settings` | `0007` | The file keeps the `PATH` fields and the executables. |
| 3 | [Executables](3-executables.md) | The registered executables, into `executables` | `0008` | The file keeps the `PATH` fields. |
| 4 | [PATH](4-path.md) | The frozen `PATH`, into `path_entries` and `path_sync` | `0009` | The file holds nothing Kstack reads. |
| 5 | [Retire the file](5-retire-the-file.md) | Nothing; deletes `jsonsettings` and what is left of "held" | none | The work is whole. |

Steps 1 to 4 do not depend on each other. They are numbered so each has its migration number.
Every step leaves a working app: an area not yet moved is still read from the file.

The sequence releases once, after step 5, so an install is reset once.

## During the sequence

`internal/services/settings` keeps its name, its `Service` and its `Store`. The `Store` holds
both the file and `app.db` until step 5:

- A step moves its area's methods onto `app.db`, laid out as `envvars` is (`store.go`,
  `statements.go`), and deletes its fields from `Settings`. The methods keep their names and
  their meaning.
- A moved area publishes after each commit, current-on-subscribe, as `toolsettings` does.
- A step removes its own part of "held" from the code and the wire. `settingsRefused` reports
  the areas still in the file, and goes in step 5.
- A step updates its area's rows in [`persisted-data.md`](../../persisted-data.md) and its
  paragraphs in `sidecar/internal/services/settings/CLAUDE.md`, in the same change.

## Rules every step follows

### No import, by decision

**Nothing is imported or copied.** This is the maintainer's decision, and it overrides the
repo's own rules for this sequence:

- [`persisted-data.md`](../../persisted-data.md) says every format `desktop/v0.0.1` wrote is
  shipped and must stay readable. `settings.json` and `chat_grants` are two of them. This
  sequence stops reading the first and drops the second.
- The same register says a migration ships with a test that seeds the old schema, migrates and
  reads the seed back. Step 1's test instead checks that `chat_grants` is gone and that every
  other table's seed survives.
- [The scoped-settings ADR](../../adr/2026-11-20-a-setting-with-scopes-is-rows.md) and
  [the tool-settings ADR](../../adr/2026-11-21-tool-settings-are-rows-in-app-db.md) say a field
  that shipped in `settings.json` cannot move into `app.db`. This sequence moves them.
- [The settings-file ADR](../../adr/2026-10-07-settings-are-one-file-grouped-by-area.md) keeps
  the settings in a file. This sequence removes the file.

Where these specs and an earlier ADR or doc disagree, the specs win. Step 5 lists the docs to
change so they agree again.

**What an install of `desktop/v0.0.1` loses on upgrade:** its approval modes, its always rules
(deny and ask rules included), its folder grants, its registered executables, its `PATH`
decisions and every chat's grants. Each returns to its default, so a context set to read-only
becomes `ask`, and a removed `PATH` folder can be adopted again by the next sync. The release
notes must list what to set again.

The cloud platform switches are not on that list: `desktop/v0.0.1` did not have them, so no
release has written one. A build from `main` that turned a platform off loses the switch, and
the platform is on again.

No build deletes `settings.json`. A moved area's keys stay in it, unread. `make clean-data`
clears a dev machine.

### The tables

- Each value has its own typed column. There is no JSON column.
- A column with a fixed set of values has a `CHECK`.
- No row means the default, except in `sandbox_settings`, which always has its one row.

### A row Kstack cannot use

A `CHECK` stops most bad values from being stored. The checks that need Go stay in Go, and run
on every write and again on every read, as `envvars` does.

A row that fails on read is **refused**. It stays in the table, Settings lists it with the
reason, and the user removes it there. **A remove always works on a refused row.** Each step
says what Kstack does while one of its rows is refused, by one rule: a value Kstack cannot read
may cost a permission, never a restriction.

On the wire a refused row arrives on the list it belongs to, with `refusal: String`: the reason
in the user's words, null for a row Kstack uses, as `EnvVar` has. The webview draws it through
`VisibleText`.

### A read that fails

A failed read is never an empty result. No row means the default, and some defaults permit: a
context with no mode row is `ask`, and a run with no `path_sync` row searches the platform's
`PATH`. So:

- **A failed read is an error, and the operation that needed it stops.** A run does not start,
  a turn does not start, a Settings query answers the error.
- **A decision that must still be answered gets the strictest answer.** Each step names its own.
- **A read that fails partway is a failed read.** The rows already read are not used.

Each reader has a test for a failed query and for a failure after some rows were read.

## Not in this sequence

- Syncing settings across machines. `PATH` entries and executables describe one machine; the
  sync design decides how to scope them.
- A cluster scope for any of these settings.
- `host.json`, the host's own preferences.
