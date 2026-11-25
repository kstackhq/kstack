---
title: Keep each single-value setting as a row of one settings table
date: 2026-11-25
scope: sidecar
status: Accepted
---

# Keep each single-value setting as a row of one settings table

## Context

[Keep every setting as rows in app.db](2026-11-24-settings-are-rows-in-app-db.md) gave each
setting a typed column in a table of its own. That fits a setting with structure. For a single
value it cost more with each one added: a migration and its test per setting, a prepared
statement per column (the four cloud platform switches had four `UPDATE`s), a fallback from a
cluster to the default written again by each setting with scopes, and a default written into the
row, so a later build could not change it and a switch never touched read the same as one the
user chose. Most of the settings still to come are single values. Nothing had shipped these
tables: `desktop/v0.0.1` carries migration `0001` alone.

## Decision

**A single-value setting is a row of `settings`** (migration `0010`): a dotted `key`, a scope
(`cluster_id` NULL for every cluster) and a `value` of `ANY` kind, an integer, text or NULL. The
cloud platform switches, the `PATH` setting, the approval modes and the tool settings moved there,
and `sandbox_settings`, `approval_modes` and `tool_settings` were dropped.

A setting belongs in `settings` when all three hold: it is one value, read and written whole; a
value Kstack cannot use can cost a permission, never grant one, because its entry names a
strictest value that such a value reads as, or its reader stops on it; and the code names it, not
the user.

- **The code holds each setting's entry** (`services/settings/scalar.go`, `Entry[T]`): its key,
  default, strictest value, whether a cluster may have its own, and check. No row, or a NULL, is
  the default, so a later build can change it. A reset writes NULL; a row goes only with its
  cluster.
- **A value Kstack cannot use reads as its strictest value, else its default**, logged by key and
  reason, never by value, and is not listed. Choosing in Settings writes over it. The `PATH` is
  the exception: its reader lists the refusal and starts no run.
- **A JSON value is opaque to SQL** and decodes into the entry's Go type, refusing an unknown
  field.

This amends three ADRs. For `settings` it replaces, in
[Keep every setting as rows in app.db](2026-11-24-settings-are-rows-in-app-db.md), "every setting
is a typed row", "each value has its own typed column … there is no JSON column" and "a row Kstack
cannot use is refused … Settings lists it with its reason". In
[Keep the tool settings as rows in app.db](2026-11-21-tool-settings-are-rows-in-app-db.md) and
[Keep a setting with a cluster scope as rows in app.db](2026-11-20-a-setting-with-scopes-is-rows.md)
it replaces the tables they name for the tool settings and the approval modes.

## Alternatives considered

**A typed table per setting**, as before. Every new single value repays the migration, the
statements and the scope fallback, and keeps its default in the row.

**One `settings` table with a column per setting.** A setting retired after a release still
leaves a column, and a new one is still a migration.

**List a value Kstack cannot use, as the typed tables did.** Each setting would need its own
refusal on the wire and a control to show it. Rule 2 already makes the strictest reading safe,
and the next write repairs the row.

## Consequences

Adding a setting is an entry, its methods, its wire field and its control: no migration and no
statement. The tables' `CHECK`s on a mode and a switch are gone; a value they refused at the
write is now refused at the read, as its strictest value. A missing row is the default, not a
failed read: every switch on, `$PATH`. A JSON value written by a newer build, with a field an
older build does not know, reads there as the default and is lost at that build's next write.
`created_at` and `updated_at` are not a sync design.

## Revisit when

A setting needs its parts read, written or queried apart, or a value Kstack cannot use must be
shown to the user rather than read as its strictest value.
