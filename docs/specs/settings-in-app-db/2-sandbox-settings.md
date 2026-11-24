# Step 2: Sandbox settings

Moves the cloud platform switches into `app.db`. Part of [the sequence](README.md), whose shared
rules apply.

## What moves

| In `settings.json` today | Becomes |
| --- | --- |
| `sandbox.aws`, `sandbox.github`, `sandbox.gcp`, `sandbox.azure` | `sandbox_settings` |

## The table

Migration `0007_sandbox_settings.sql`:

```sql
-- sandbox_settings: the user's settings for every sandboxed command, in one
-- row. Each <platform>_enabled is whether a sandboxed command may reach that
-- cloud platform as the user.
CREATE TABLE sandbox_settings (
  id             INTEGER PRIMARY KEY CHECK (id = 1),
  aws_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (aws_enabled IN (0, 1)),
  github_enabled INTEGER NOT NULL DEFAULT 1 CHECK (github_enabled IN (0, 1)),
  gcp_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (gcp_enabled IN (0, 1)),
  azure_enabled  INTEGER NOT NULL DEFAULT 1 CHECK (azure_enabled IN (0, 1)),
  updated_at     INTEGER NOT NULL
) STRICT;

INSERT INTO sandbox_settings (id, updated_at) VALUES (1, 0);
```

The table holds what the user chose for the sandbox as a whole. Today that is the four cloud
platform switches, each on by default. `cloudSwitch` maps a `permissions.Target` to its column,
as it maps one to a field today.

A new cloud platform is a new column, by a migration. So is a later sandbox setting that has one
value for every command. What Kstack measures on the machine (step 4's `path_sync`) stays out of
this table: it holds the user's choices alone.

## A row Kstack cannot use, and a read that fails

No row can be refused: every column has a `CHECK`.

The migration writes the one row, so a read always finds it. **A missing row is a failed read**,
not a default. A failed read of a switch is off.

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`services/settings`**: `CloudOn` and `SetCloud` read and write `app.db`. The four switches
  and their `Field*` constants leave `SandboxSettings`, and their entries leave `strictest`.

## The wire

Nothing changes: `sandboxCloudAccess` and `sandboxCloudAccessSet` keep their shape.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0007` and its test, which checks the one row, each `CHECK`, and that a second row is refused. | Planned |
| 2 | `services/settings`: the switches on `app.db`, with a missing row and a failed read both reading off. | Planned |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add a row for `sandbox_settings`; take the
  switches out of the *Sidecar settings* row.
- [`security-model.md`](../../security-model.md): the cloud platform switches' row names the
  table and the tests that pin a missing row as off.
- `sidecar/internal/services/settings/CLAUDE.md`: the cloud switches paragraph.
