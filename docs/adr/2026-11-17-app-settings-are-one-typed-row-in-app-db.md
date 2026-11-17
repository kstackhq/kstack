---
title: App settings are one typed row in app.db, migrated ahead of the backup
date: 2026-11-17
scope: sidecar
status: Accepted
---

# App settings are one typed row in app.db, migrated ahead of the backup

## Context

Settings → Clusters (#944) lets the user pick a default cluster that sits above the kubeconfig's
`current-context`. The value names one cluster by its id, the primary key of `app.db`'s
`clusters` table. It is the first app-level choice that references a row, and more choices made
in Kstack's own UI will follow.

[Settings are one file grouped by area](2026-10-07-settings-are-one-file-grouped-by-area.md)
keeps the user's settings in `settings.json`, and names a setting that must commit beside an
`app.db` row as the case to revisit. That file is the user's security policy: it is read and
edited by hand, and its store refuses and holds a bad value per field.

[A shipped schema changes only by a new
migration](2026-11-08-a-shipped-schema-changes-only-by-a-new-migration.md) held the first
`app.db` migration past `0001` until the backup before a migration (#823) lands, since a
migration runs on the file in place.

## Decision

`app.db` gains `app_settings`, added by `0002_app_settings.sql`: one row, its `slot` fixed at 0,
inserted by the migration, with a typed column per setting. A new setting is a new column, added
by a later migration.

The first column is `default_cluster_id`, which references `clusters(id)` `ON DELETE SET NULL`.

`internal/services/appsettings` owns the table. It serves the row as a gauge,
`appSettingsWatch`, so every window follows a change from one place; a write notifies
`appdb.KeyAppSettings`, and the watch re-reads on `KeyClusters` too, since a cluster's deletion
clears the default.

**The two homes divide by who writes the value.** `settings.json` holds the policy a person
reads and edits by hand: permissions, the sandbox, executables. `app_settings` holds a choice
made in Kstack's UI. This replaces the older settings ADR's sentence that the settings are never
rows in `app.db`; its decision for the file stands.

`0002` ships before #823. It creates one table and inserts its row, so a failure mid-run leaves
`0001`'s data as it was. This replaces the schema ADR's sentence holding the first migration past
`0001`; the rest of that ADR stands.

## Alternatives considered

- **A table per value** (`default_cluster`, one row, a `cluster_id`). The same integrity for one
  value, but each later setting brings a table, a set of statements and a watch of its own.
- **A key/value table.** Any setting fits without a migration, but a value is untyped text: no
  foreign key, so a deleted cluster leaves an id naming nothing, and no `CHECK` per setting.
- **A flag column on `clusters`.** The default would ride the clusters watch. But it is the
  app's choice, not a cluster's property: "at most one" becomes an index, setting it writes two
  rows, and either two clusters' `updated_at` move or that column's rule gains an exception.
- **`settings.json`.** The file and `app.db` would each hold half of one fact, and nothing keeps
  them in step: a deleted cluster, or a rebuilt `app.db`, leaves an id naming nothing. Nobody
  types a UUID into a file.
- **Waiting for #823.** It blocks a finished feature on a safeguard this migration does not
  need: the migration rewrites no existing row.

## Consequences

Deleting a cluster clears the default with no code. The default may still name a disabled or
archived cluster; readers fall back to the kubeconfig's `current-context`.

There are two settings homes, and a new setting has to pick one by the rule above. A setting
with no reason to be in `app.db` other than convenience still belongs in the file when a person
is meant to edit it.

Adding a column does not rewrite the row, so a later setting's migration is as safe as this one.
A migration that rewrites existing rows still wants #823 first.

## Revisit when

#823 lands: the reason for the exception is gone, and the register's wording returns to the
general rule.
