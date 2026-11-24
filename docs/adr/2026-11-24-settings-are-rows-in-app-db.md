---
title: Keep every setting as rows in app.db
date: 2026-11-24
scope: sidecar
status: Accepted
---

# Keep every setting as rows in app.db

## Context

[The settings-file ADR](2026-10-07-settings-are-one-file-grouped-by-area.md) kept the sidecar's
settings in `<data>/settings.json`, a file the user could edit by hand. Nobody did: every value in
it was written by Kstack, from a Settings mutation or the `PATH` sync. Most of the code around it,
`internal/lib/jsonsettings`, existed only to survive a hand edit: held fields, the raw JSON kept
for a refused value, unknown keys written back, and `strictest`, which read a refused restriction
as its most restrictive state.

Other settings were already rows in `app.db`
([a setting with a cluster scope](2026-11-20-a-setting-with-scopes-is-rows.md),
[tool settings](2026-11-21-tool-settings-are-rows-in-app-db.md)). Both of those ADRs say a field
that shipped in `settings.json` cannot move into `app.db`, because a release must keep reading
what it wrote.

`desktop/v0.0.1` is the one release, and Kstack has no users who are owed its data. The
maintainer decided to move the file's settings without an import (#1127).

## Decision

**Every setting the sidecar keeps is a typed row in `app.db`.** Nothing reads or writes
`settings.json`, and `internal/lib/jsonsettings` is gone.

- **Each value has its own typed column**, with a `CHECK` for a fixed set of values. There is no
  JSON column. No row means the default, except in `sandbox_settings`, which always has its one
  row.
- **A row Kstack cannot use is refused.** It stays in its table, Settings lists it with its
  reason, and the user removes it there. A remove always works on a refused row. A value Kstack
  cannot read may cost a permission, never a restriction.
- **A failed read is an error, never an empty result.** The operation that needed it stops. A
  decision that must still be answered gets the strictest answer. A read that fails partway is a
  failed read.
- **Nothing is imported.** `settings.json`, written by `desktop/v0.0.1`, is dropped by the
  maintainer's decision, as `chat_grants` was by migration `0006`
  ([folder grants are rows](2026-11-24-a-folder-grant-is-its-own-row-and-a-mode-and-a-rule-belong-to-a-cluster.md)).
  No build deletes `settings.json`; it stays on disk, unread.

An install of `desktop/v0.0.1` loses, on upgrade, its approval modes, its always rules (deny and
ask rules included), its folder grants, its registered executables, its `PATH` decisions and
every chat's grants. Each returns to its default.

This replaces paragraphs of four earlier ADRs:

- [A setting with a cluster scope is rows](2026-11-20-a-setting-with-scopes-is-rows.md) and
  [tool settings are rows in app.db](2026-11-21-tool-settings-are-rows-in-app-db.md): "nothing
  can move it into `app.db`" and the user's need to edit a setting by hand no longer apply.
- [Every cloud platform is on until the user turns it off](2026-11-18-every-cloud-platform-is-on-until-the-user-turns-it-off.md):
  *A refused value reads off* described `strictest`. Now a failed read of `sandbox_settings`
  reads off: a run whose switches cannot be read reaches no cloud platform.
- [Each persisted format carries its own version stamp](2026-10-29-each-persisted-format-carries-its-own-version-stamp.md):
  `settings.json`'s `schemaVersion` no longer applies, and `host.json` is the one preference file.

## Alternatives considered

**Import the file once, then delete the reader.** It keeps an install's settings, but every
format it reads must still be parsed and tested, and no user is owed the data.

**Keep the file for global switches only.** Two stores for one kind of thing, and the hand-edit
machinery stays for a few booleans.

## Consequences

The user cannot edit a setting by hand; Settings is the only write path. A setting's change is an
ordinary numbered `app.db` migration. The persisted-data register's rule that every
`desktop/v0.0.1` format stays readable holds for everything but the two formats dropped here.
