# Scalar settings

Adds `settings`, one `app.db` table for the settings that are a single value, and moves onto it
every setting that fits: the cloud platform switches, the `PATH` setting, the approval modes and
the tool settings. `sandbox_settings`, `approval_modes` and `tool_settings` are dropped.

**Built on `wip/settings-in-app-db`**, before that branch lands on `main`. The spec merges into
it. The work is one PR into it, one commit, since the migration drops the tables the old code
reads. The branch's landing PR deletes the spec.

## Why

Every setting is a typed row in a table of its own
([ADR](../adr/2026-11-24-settings-are-rows-in-app-db.md)). That fits a setting with structure. For
a single value it costs more with each one added:

- A migration, its test and its checksum line, for good: a new cloud platform is a new column,
  and one retired after a release leaves a column behind.
- A prepared statement per column, since a statement cannot take a column as a parameter
  (`services/settings/statements.go`, the four switch `UPDATE`s).
- Its own way to fall back from a cluster to the default (`stmtSelectMode`), written again by the
  next setting that has scopes.
- A default written into the row, so a switch the user never touched reads the same as one they
  chose, and a later build cannot change the default.
- Timestamps that differ by table: `sandbox_settings` has one `updated_at` for five settings.

The settings still to come are mostly single values.

## What goes in `settings`

A setting goes in `settings` when all three hold:

1. **It is one value, read and written whole**: a boolean, an integer, a string, or a small JSON
   value such as a list of names. A setting whose parts are read, written or queried apart gets
   a table.
2. **A value Kstack cannot use can cost a permission, never grant one.** A setting that widens
   what a command, a tool or a sandbox may do belongs here only if such a value has a safe
   reading: its entry names its strictest value, which the value reads as, or its reader stops on
   it.
3. **The code defines its name, not the user.** A setting per variable, per folder or per
   anything the user names gets a table keyed on that name.

| Setting | Rule 2 | Moves |
| --- | --- | --- |
| The cloud platform switches | off is the strictest | yes |
| The approval modes | read-only is the strictest | yes |
| The tool settings | off is the strictest | yes, a key per tool |
| The `PATH` setting | its reader stops: a refused `PATH` starts no run | yes |
| `env_vars`, `executables` | — | no: rows the user names (rule 3) |
| `kube_rules`, `folder_grants` | — | no: several fields each (rule 1), guarded by `CHECK`s |
| `shell_path` | — | no: a cache Kstack writes, not a setting |

## The table

Migration `0010_settings.sql`:

```sql
-- settings: the user's single-value settings, a row per setting and scope the
-- user has set. A setting with no row, or with a NULL value, has its default.
-- cluster_id NULL is the setting for every cluster; set, it is that cluster's
-- alone and goes with the cluster.
CREATE TABLE settings (
  key        TEXT    NOT NULL CHECK (key <> ''),
  cluster_id TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  value      ANY     CHECK (typeof(value) IN ('integer', 'text', 'null')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX settings_scope_key ON settings (ifnull(cluster_id, ''), key);

-- Every mode keeps its row, and every tool setting for every cluster, the only
-- scope ever written. A switch keeps its choice when it is off, and the PATH
-- when it is not the default: the old row cannot tell a default from a choice.
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'tools.' || tool_name || '.enabled', enabled, updated_at, updated_at
  FROM tool_settings WHERE cluster_id IS NULL;
INSERT INTO settings (key, cluster_id, value, created_at, updated_at)
  SELECT 'permissions.approval_mode', cluster_id, mode, updated_at, updated_at
  FROM approval_modes;
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.path', path, updated_at, updated_at
  FROM sandbox_settings WHERE path <> '$PATH';
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.aws.enabled', 0, updated_at, updated_at
  FROM sandbox_settings WHERE aws_enabled = 0;
-- ... and the same for github, gcp and azure.

DROP TABLE tool_settings;
DROP TABLE approval_modes;
DROP TABLE sandbox_settings;
```

- **`value` is `ANY`**, which a `STRICT` table stores as given: an integer stays an integer. A
  boolean is the integer 0 or 1, and a JSON value is its text.
- **A JSON value is opaque to SQL.** No statement reads inside one: no `json_extract`, no index
  on a path. The table cannot tell JSON from a string, so the kind lives in the code.
- **No row means the default, never chosen.** The default lives in the code, so a later build can
  change it without touching a user's choice.
- **A NULL value means the default, chosen.** The user put the setting back, and `updated_at` says
  when. It follows a later build's default, as no row does. In a cluster's row it means the
  cluster takes the setting for every cluster.
- **A value equal to the default is still a value.** It stays when the default changes.
- **A row is never deleted**, except with its cluster. Putting a setting back writes NULL, so
  every change the user made is a row with its time.
- **Every write moves `updated_at`**, a write of the same value included. `created_at` is the
  first write's time; a carried row takes its old `updated_at` for both.

The carry exists for installs of this branch alone: no release wrote these tables. `0004` to
`0009` stay as they are, so those installs keep working.

## The settings in the code

`services/settings/scalar.go` holds the entries. An entry is its key, its kind (boolean, integer,
string or JSON), its default, its strictest value if it has one, whether a cluster can have its
own, and a check of a value:

| Key | Kind | Default | Strictest | Per cluster | Check |
| --- | --- | --- | --- | --- | --- |
| `sandbox.aws.enabled`, and `github`, `gcp`, `azure` | boolean | true | false | no | none |
| `sandbox.path` | string | `$PATH` | none: its reader stops | no | `CheckPath` |
| `permissions.approval_mode` | string | `ask` | `read-only` | yes | one of the three modes |
| `tools.<name>.enabled` | boolean | true | false | no | none |

A key is names joined by dots, the first its area, and is never reused for another meaning.
Removing a setting removes its entry; its rows stay, unread.

**A tool's entry is built from its name** (`services/settings/tools.go`), since the list differs
by build and machine and is not known when `settings.Open` runs. `tools.go` exports the read, a
write's arguments and the row reader, which `toolsettings` and `agent/chat` use. `<name>` is the
tool's `Name()` as it is. `scalar.go` and each setting's own file hold every other entry.

**A JSON setting has a Go type.** The write encodes it and the read decodes it, refusing an
unknown field, so the rest of the code never holds JSON. Its check bounds its size. On the wire it
is a typed field (`[String!]!`, say), never the `JSON` scalar. No setting is JSON yet: the store's
tests cover the kind with an entry of their own.

Each setting keeps typed methods. Adding a setting is one entry, its methods, its field on the
wire and its control. It needs no migration and no statement.

## Reading and writing

**Four statements serve every setting:**

- the four switches for every cluster, by their keys and the index:
  `SELECT key, value FROM settings WHERE ifnull(cluster_id, '') = '' AND key IN (?, ?, ?, ?)`;
- one key for one cluster and for every cluster, by the index, as `stmtSelectMode` reads today:
  `SELECT ifnull(cluster_id, ''), value FROM settings WHERE ifnull(cluster_id, '') IN (?, '')
  AND key = ?`;
- one key in every scope, for Settings' list of the clusters' modes:
  `SELECT ifnull(cluster_id, ''), value FROM settings WHERE key = ?`;
- one upsert, on `(ifnull(cluster_id, ''), key)`.

The shared read and write take the statements they run on, so a caller reads inside its own
transaction.

**A cluster's value** is its own row's, else the row for every cluster, else the default. A NULL
in its own row falls through.

**A write runs the check first.** A value the check refuses is a `SettingRefusal`, which names
the key and the reason, and writes nothing; so is a JSON value that encodes as `null`, since a
NULL is the reset. A write or reset for a cluster of a setting no cluster has its own of is
refused too, and writes no row. A setting whose own error the wire already maps
(`CheckPath`'s) keeps it.

**A reset writes NULL**, through the same upsert.

**A value Kstack cannot use reads as the entry's strictest value, or as its default when it has
none.** That is a value of the wrong kind, JSON that does not decode, or a value the check
refuses, as a build with a narrower check would find. The store logs the key and the reason,
never the value. The row stays, and the next write replaces it. Nothing lists it: the setting's
control shows what was read, and choosing there is the repair. The `PATH` setting is the
exception, below.

**A row whose key no entry has is kept and ignored**, a tool this build or machine lacks included.

**A failed read is an error**, never the default. Each reader does with it what it does today.

**Each setting notifies the key it notifies today**: `KeyPermissions` for a mode,
`KeySandboxPath` for the `PATH`, `toolsettings`' hub for a tool, none for a switch.

## The cloud platform switches

`CloudPlatformSwitches(ctx)` and `SetCloudPlatformSwitch(ctx, target, on)` keep their names,
signatures and callers. The wire does not change.

- **One read still takes all four**, so no read fails partway.
- **A switch with no row is on**
  ([ADR](../adr/2026-11-18-every-cloud-platform-is-on-until-the-user-turns-it-off.md)), and one
  whose value Kstack cannot use is off.
- **`SetCloudPlatformSwitch` upserts its one key.** The four `UPDATE`s and `switchUpdates` go.
- **A failed read is as today**: the error to Settings, off to a grant and to a run's start, no
  platform named in a chat's Sandbox block.

## The `PATH` setting

`SetPath` and `readPath` keep their names and callers. The wire does not change.

- **No row is `$PATH`.** `SetPath` upserts, and `ErrNoSandboxSettings` goes.
- **A stored value the grammar refuses is read with its refusal**, as today (`PathSetting`). It
  is listed in Settings and starts no run. It never reads as `$PATH`.
- **A stored value that is not text** is a `PathSetting` with an empty `Value` and the refusal
  "The stored PATH is not text." Settings shows an empty field with that refusal.
- **`readPath` still reads the setting and `shell_path` in one read transaction.**

## The approval modes

`ModeFor`, `ClusterModes`, `SetDefaultMode`, `SetMode` and `ClearMode` keep their names and
callers. The wire does not change.

- **The default mode is the key's row for every cluster**; with no row, `ask`.
- **A cluster's own mode is its row.** `ClearMode` writes NULL there, where it deleted the row.
  A cluster with no row or a NULL takes the default (`SourceDefault`).
- **A mode Kstack cannot use is read-only**, for the cluster whose row holds it, or for every
  cluster that takes the default when the default's row does.
- **A failed read is read-only**, as today.
- `SetMode` and `ClearMode` still check the cluster in the write's transaction.

## The tool settings

`toolsettings.Service` keeps `List`, `Set`, its hub and its callers. The wire does not change.

- **A tool with no row is on.** A row is written on every `Set`, on or off, as today.
- **A row whose value Kstack cannot use turns its tool off.** An always-on tool stays on whatever
  its row says.
- **`toolsettings` runs its statements over `settings`**, with the upsert and the key's spelling
  from `services/settings`.
- **A chat reads the tools that are off inside its turn's transaction**, as today. Its statement
  takes the `tools.` rows by the index:

  ```sql
  SELECT key, value FROM settings
  WHERE ifnull(cluster_id, '') = '' AND key >= 'tools.' AND key < 'tools/'
  ```

  `services/settings` exports the statement and the function that reads a row as a tool's name
  and whether it is off, so the chat and `toolsettings` cannot read a row apart.

## What this gives up

- **The tables' `CHECK`s on a mode and on a switch.** A value they refused at the write is now
  refused at the read, as its strictest value.
- **A missing row as a failed read.** Today `sandbox_settings` with no row is an error, so every
  switch is off and no run starts. Here no row is the default: every switch on, `$PATH`. Whoever
  can delete a row can also write one.

## Not in this spec

- **The webview's `localStorage` preferences.** They are disposable, per origin and read before
  the sidecar answers. Each moves only if the sidecar needs it or it should follow the user.
- **A `Settings` type and one gauge for every setting.** Each moved setting keeps its own wire.
- **A reset on the wire.** `ClearMode` is the one reset. A control that resets through an input
  field has to tell an explicit `null` from a field left out, which gqlgen does only for a field
  marked omittable.
- **Sync.** `created_at` and `updated_at` are not a sync design. That needs its ADR
  ([`TODO.md`](../TODO.md), *Rethink how settings sync*).

## Tasks

All tasks start Planned, and are built in order, as one commit: the first drops the tables the old
code reads.
The [verification commands](README.md#verification-commands) apply.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0010`, its checksum line and its test, which builds the schema before it, seeds each earlier table (a tool off, one on and one for a cluster alone, a default mode and a cluster's, a switch off, a changed `PATH`), migrates, and checks that every other seed survives, each carried row with its key, scope, value and times, no row for a switch that was on or for the cluster's tool setting, the three tables gone, the unique index, each `CHECK` (an empty key, a real, a blob), that a NULL value is taken, that an integer reads back as an integer, and that a cluster's row goes with its cluster. A second test: a default `PATH` and every switch on carry no row. | Done |
| 2 | `services/settings`: `scalar.go` and its statements. Tests, over the test's own entries: no row reads the default; a write round-trips; a second write keeps `created_at` and moves `updated_at`; a reset writes NULL, keeps the row and reads the default; a value equal to the default is stored as a value; a cluster's row wins, and its NULL and its absence fall through; the read for one cluster uses the index; a write the check refuses is a `SettingRefusal` and writes nothing; a JSON value round-trips through its Go type; a row of the wrong kind, a row of JSON that does not decode and a row the check refuses read the strictest value, or the default for an entry with none; a row with no entry is ignored and kept; a failed query is an error. | Done |
| 3 | The switches. `TestEachCloudPlatformStartsOn` (no row), `TestSetCloudPlatformSwitchWritesOneSwitch`, `TestACloudPlatformSwitchReadThatFailsIsAnError`, and `TestASwitchValueKstackCannotUseIsOff` in place of `TestAMissingSandboxSettingsRowIsAFailedRead`. Every reader's failed-read test stays. | Done |
| 4 | The `PATH` setting. No row reads `$PATH`; `SetPath` writes the first row; `TestARefusedPathSettingIsReadWithItsRefusal` stays, with a value that is not text beside it, read as an empty value and its refusal; the read with `shell_path` is one transaction. | Done |
| 5 | The approval modes. `TestAClusterWithNoModeOfItsOwnTakesTheDefault` stays; a cleared mode keeps its row as NULL and takes the default; a mode Kstack cannot use is read-only, in a cluster's row and in the default's; a failed read is read-only. | Done |
| 6 | The tool settings, in `services/toolsettings` and `agent/chat`. `TestSetWritesOneRowPerToolAndKeepsIt`, `TestRowsOutsideTheListAreIgnored` and `TestAnAlwaysOnToolStaysOn` stay; a row Kstack cannot use turns its tool off, in `List` and in a turn's offer; the chat's read uses the index. | Done |

## When it lands

Nothing is left for the landing. The ADR
([keep each single-value setting as a row of one settings table](../adr/2026-11-25-a-single-value-setting-is-a-row-of-settings.md)),
its `amended_by` pointers, the
[security record](../security/2026-11-25-single-value-settings.md), `security-model.md`,
`persisted-data.md` and the guides changed with the work.
