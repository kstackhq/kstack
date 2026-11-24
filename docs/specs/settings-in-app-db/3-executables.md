# Step 3: Executables

Moves the user's registered executables into `app.db`. Part of [the sequence](README.md), whose
shared rules apply.

## What moves

| In `settings.json` today | Becomes |
| --- | --- |
| `executables` | `executables` |

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

## A refused executable

The read-back is `CheckExecutable`, the one shape check, as on register.

A refused executable is not probed. It only widens what is probed, so nothing else is
restricted while one is refused.

A registered executable becomes refused when a release adds its name to `CuratedExecutables`.
The curated one of that name is still probed, and Remove takes the registered row.

## A read that fails

The probe does not start, and the report answers the error.

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`services/settings`**: `RegisterExecutable`, `RemoveExecutable` and the list read and
  write `app.db`. `Executables` leaves `Settings`, and its check leaves `checks`.

## The wire

- `SandboxExecutable` gains `refusal: String`.
- `sandboxExecutableRemove` takes a refused executable.

The webview is done when these hold, each with a test:

- A refused executable shows its reason.
- When a registered name is also curated, the list shows both, keyed apart; Remove takes the
  registered one and the curated one stays.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0008` and its test. | Planned |
| 2 | `services/settings`: the executables on `app.db`, a refused one listed and not probed. | Planned |
| 3 | `graph`, `src/`: the wire and the webview. | Planned |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add a row for `executables`; take them out of
  the *Sidecar settings* row.
- `sidecar/internal/services/settings/CLAUDE.md`: the executables paragraph.
