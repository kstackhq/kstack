# Step 4: PATH

Moves the frozen `PATH` into `app.db`. Part of [the sequence](README.md), whose shared rules
apply.

## What moves

| In `settings.json` today | Becomes |
| --- | --- |
| `sandbox.path` | `path_entries` |
| `sandbox.pathResolved` | the `path_sync` row exists |
| `sandbox.pathStrict` | `path_sync.strict`, and a computed part |

## The tables

Migration `0009_path.sql`:

```sql
-- path_entries: the user's PATH, frozen, in the shell's order. A state holds
-- for target alone.
CREATE TABLE path_entries (
  dir      TEXT    PRIMARY KEY,
  target   TEXT    NOT NULL,
  state    TEXT    NOT NULL CHECK (state IN ('adopted', 'pending', 'gone')),
  source   TEXT    NOT NULL CHECK (source IN ('shell', 'user')),
  shared   INTEGER NOT NULL CHECK (shared IN (0, 1)),
  position INTEGER NOT NULL
) STRICT;

-- path_sync: one row once a sync has finished. Before it a run searches the
-- platform's default PATH, after it path_entries alone. strict is set when a
-- refused entry is removed and cleared by the next sync, which it makes file
-- every new entry pending.
CREATE TABLE path_sync (
  id        INTEGER PRIMARY KEY CHECK (id = 1),
  synced_at INTEGER NOT NULL,
  strict    INTEGER NOT NULL DEFAULT 0 CHECK (strict IN (0, 1))
) STRICT;
```

## Writes

Each write is one transaction:

- **A sync** writes its changes to `path_entries` and the `path_sync` row.
- **Include** sets its row `adopted` and deletes the removals kept for the same folder under
  other `dir`s, so several rows can share a `target`.
- **Remove** sets its row `gone`.

**No transaction reads the disk.** A sync and an Include compare stored `target`s with folders
on disk (`targetLookup`), and a lookup can hang. So they read first, then open the transaction
and check there that the rows they compared are still the rows stored. Include retries a list
that changed under it and gives up with `ErrPathBusy`, as it does today. Syncs still run one at
a time.

**A sync that cannot compare a stored `target` writes nothing**, as today. That includes
`path_sync`: it does not clear `strict`.

## A refused entry

The read-back refuses an entry whose `dir` or `target` is not an absolute folder. Its `target`
is never looked up on disk.

A refused entry is treated as `pending`: not searched. **While any entry is refused, a sync
files every new entry as `pending`, and Include is refused.** This is the computed part of
`pathStrict`. A removal is of a folder, not of one row: `diffPath` also keeps out every other
spelling that resolves to a removed `target`. A refused row's `target` cannot be trusted, so
until the user removes that row, no new folder is adopted without them.

**Remove on a refused row makes it a removal Kstack can read.** In one transaction, Remove:

- sets the row `gone`, by the user, with its `target` set to its `dir`, when the `dir` is
  absolute. The row is no longer refused, and the next sync that lists the `dir` stores what it
  resolves to, as it does for any removal;
- deletes the row when the `dir` is not absolute. The shell cannot list such a `dir`, so there
  is nothing to keep out by name;
- sets `path_sync.strict` either way.

So a removed `dir` stays removed, whenever the shell lists it again, until the user includes it.

`strict` is the stored part of `pathStrict`, kept so it survives a restart. The row's old
`target` could not be read, so Kstack cannot tell which other `dir`s lead to the same folder.
The next sync therefore files every new entry as `pending`, and then clears `strict`. Once no
row is refused, Include works again; a sync adopts again after the strict one.

**What this does not cover:** another `dir` that leads to the removed folder, and that the shell
first lists after the strict sync, is new to Kstack and can be adopted. The code has the same
limit today.

Three tests pin this. The first two are today's `TestARemovalWhileHeldKeepsTheSyncStrict` and
`TestARemovalWhileHeldStaysStrictAcrossARestart`, rewritten for rows:

- Remove a refused row, then sync with another `dir` of the same folder listed: that entry is
  `pending`.
- The same, with the store closed and opened between the Remove and the sync.
- Remove a refused row, sync with its `dir` absent from the shell's `PATH`, then sync with it
  present: the entry is `gone`.

## A read that fails

A run does not start. It never falls back to the platform's default `PATH`: that is for a
machine with no `path_sync` row, read successfully.

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`services/settings`**: `SyncPath`, `AdoptPath` and `DropPath` read and write `app.db`,
  and a run's check (`RunCheck`) reads the entries from it. `ErrPathHeld` becomes `ErrPathRefused`. `Path`, `PathResolved` and `PathStrict`
  leave `SandboxSettings`, which is then empty and is deleted.

## The wire

- `SandboxPathEntry` gains `refusal: String`.
- `sandboxPathRemove` works on a refused entry, whatever its state, and its description says
  what it does to one.
- `sandboxPathInclude` is refused while any entry is refused.

The webview is done when these hold, each with a test:

- A refused entry shows its reason.
- A refused entry shows a Remove action in place of its Include switch, which the sidecar would
  refuse.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0009` and its test. | Planned |
| 2 | `services/settings`: the sync, Include and Remove on `app.db`, with no disk read inside a transaction. | Planned |
| 3 | `services/settings`: the refused entry, `strict` and the three tests above. | Planned |
| 4 | `graph`, `src/`: the wire and the webview. | Planned |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add rows for `path_entries` and `path_sync`;
  take the `PATH` fields out of the *Sidecar settings* row.
- [`security-model.md`](../../security-model.md): the frozen `PATH` rows name the tables and the
  tests above.
- `sidecar/internal/services/settings/CLAUDE.md`: the `PATH` paragraphs.
