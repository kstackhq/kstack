# Step 5: Retire the file

Deletes the code that reads and writes `settings.json`. Part of [the sequence](README.md), whose
shared rules apply. Needs steps 1 to 4.

## What goes

Paths are relative to `sidecar/internal/` unless rooted.

- **`lib/jsonsettings`**, whole: held fields, `ErrHeld`, the raw JSON kept for a refused value,
  and the unknown keys written back.
- **`services/settings`**: `Settings`, `Refusal`, `ErrHeld`, `strictest`, `checks`, `Option`,
  `options` and `WithChecks`. `Open` takes the `app.db` handle alone. The package doc says the
  settings are rows in `app.db`.
- **`app`**: `SettingsFile` leaves the paths and the tree in their doc comment. Nothing opens the
  file. `TestABadSettingsFileFailsNew` goes.
- **The wire**: `settingsRefused` and `SettingsRefusal`. Every description that still says "the
  settings file" is reworded, and so is the `Settings` field's comment in `graph/resolver.go`,
  which still names the frozen `PATH`. `folderErr` in `graph/util.go` loses its
  `settings.Refusal` branch, which nothing produces, and its test case.
- **The webview**: nothing draws `settingsRefused`, so nothing changes there.

Every test helper that calls `settings.Open` with a file drops the file: in `services/settings`,
`app`, `graph`, `agent/chat` and `tools/bash`.

No build deletes `settings.json` from disk. There is no migration in this step.

## Done when

- Outside `docs/adr/`, `docs/security/` and `docs/specs/`, `<data>/settings.json` is named only
  by the register's note about the dropped formats. (`.claude/settings.json` and `.vscode/settings.json` are other files.)
- No code or doc outside `docs/adr/`, `docs/security/` and `docs/specs/` says "held" for a
  setting.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `services/settings`, `app`, `lib/jsonsettings`, and the test helpers in `graph`, `agent/chat` and `tools/bash`: delete the file's code. | Done |
| 2 | `graph`: remove `settingsRefused`, `folderErr`'s `Refusal` branch, and reword the descriptions. | Done |
| 3 | The docs below. | Done |

## When it lands

- **ADR**: settings are rows in `app.db`. It supersedes
  [settings are one file grouped by area](../../adr/2026-10-07-settings-are-one-file-grouped-by-area.md)
  and amends [a setting with a cluster scope is rows](../../adr/2026-11-20-a-setting-with-scopes-is-rows.md)
  and [tool settings are rows in app.db](../../adr/2026-11-21-tool-settings-are-rows-in-app-db.md):
  their "the user must be able to edit by hand" and "nothing can move it into `app.db`" no
  longer apply. It also amends
  [every cloud platform is on until the user turns it off](../../adr/2026-11-18-every-cloud-platform-is-on-until-the-user-turns-it-off.md),
  whose "a refused value reads off" describes `strictest`: a failed read of `sandbox_settings`
  reads off, and [each persisted format carries its own version stamp](../../adr/2026-10-29-each-persisted-format-carries-its-own-version-stamp.md),
  whose `schemaVersion` for `settings.json` no longer applies. It records that `settings.json` and `chat_grants` were dropped without an import, by
  the maintainer's decision, and what an install lost. The superseded ADR's status flips, each
  amended ADR gains an `amended_by` pointer, and the index in `docs/adr/README.md` follows.
- **`CLAUDE.md`**: `sidecar/internal/services/settings/CLAUDE.md` opens with the tables, not the
  file, and links the new ADR in place of the superseded one. In `sidecar/CLAUDE.md`, the guides
  table's row for that guide and the `settings.json` bullet under *Persisted formats*. The root
  `CLAUDE.md`'s "every format `desktop/v0.0.1` wrote is shipped" points to the register's note.
- **[`persisted-data.md`](../../persisted-data.md)**: remove the *Sidecar settings* row; the
  preference rule under *How a change ships* names `host.json`, the one preference left; take
  `settings.json` out of the lists. Next to
  "every format it wrote is shipped", name the two formats this sequence dropped and link the
  ADR, so the rule reads true for everything else.
- **[`security-model.md`](../../security-model.md)**: remove what is left of the row about a
  malformed value in `settings.json`. In the owner-only files row, take out the file,
  `jsonsettings` and `TestTheFileIsOwnerOnly`.
- **[`TODO.md`](../../TODO.md)**: reword *Rethink how settings sync*, which asks how a synced
  value meets a hand-edited file, and *Draw each cloud platform's switch in Settings*, which says
  turning a switch off is a hand edit of `settings.json`.
