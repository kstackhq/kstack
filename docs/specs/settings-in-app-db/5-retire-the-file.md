# Step 5: Retire the file

Deletes the code that reads and writes `settings.json`. Part of [the sequence](README.md), whose
shared rules apply. Needs steps 1 to 4.

## What goes

Paths are relative to `sidecar/internal/` unless rooted.

- **`lib/jsonsettings`**, whole: held fields, `ErrHeld`, the raw JSON kept for a refused value,
  and the unknown keys written back.
- **`services/settings`**: `Settings`, `Refusal`, `ErrHeld`, `strictest`, `checks` and
  `WithChecks`. `Open` takes the `app.db` handle alone. The package doc says the settings are
  rows in `app.db`.
- **`app`**: `SettingsFile` leaves the paths. Nothing opens the file.
- **The wire**: `settingsRefused` and `SettingsRefusal`. Every description that still says "the
  settings file" is reworded, and so is the `Settings` field's comment in `graph/resolver.go`.
- **The webview**: what draws `settingsRefused`.

No build deletes `settings.json` from disk. There is no migration in this step.

## Done when

- `grep -r settings.json` finds the file's name only in `docs/adr/`, `docs/security/` and the
  register's note about the dropped formats.
- No code or doc outside `docs/adr/` says "held" for a setting.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `services/settings`, `app`, `lib/jsonsettings`: delete the file's code. | Planned |
| 2 | `graph`, `src/`: remove `settingsRefused` and reword the descriptions. | Planned |
| 3 | The docs below. | Planned |

## When it lands

- **ADR**: settings are rows in `app.db`. It supersedes
  [settings are one file grouped by area](../../adr/2026-10-07-settings-are-one-file-grouped-by-area.md)
  and amends [a setting with a cluster scope is rows](../../adr/2026-11-20-a-setting-with-scopes-is-rows.md)
  and [tool settings are rows in app.db](../../adr/2026-11-21-tool-settings-are-rows-in-app-db.md):
  their "the user must be able to edit by hand" and "nothing can move it into `app.db`" no
  longer apply. It records that `settings.json` and `chat_grants` were dropped without an
  import, by the maintainer's decision, and what an install lost.
- **ADR** [every cloud platform is on until the user turns it off](../../adr/2026-11-18-every-cloud-platform-is-on-until-the-user-turns-it-off.md):
  its "a refused value reads off" describes `strictest`. Amend it to say a failed read of
  `sandbox_settings` reads off.
- **`CLAUDE.md`**: `sidecar/internal/services/settings/CLAUDE.md` opens with the tables, not the
  file; update the `settings.json` lines in `sidecar/CLAUDE.md`.
- **[`persisted-data.md`](../../persisted-data.md)**: remove the *Sidecar settings* row and the
  preference rule under *How a change ships*; take `settings.json` out of the lists. Next to
  "every format it wrote is shipped", name the two formats this sequence dropped and link the
  ADR, so the rule reads true for everything else.
- **[`security-model.md`](../../security-model.md)**: remove what is left of the row about a
  malformed value in `settings.json`, and take the file out of the owner-only files row.
- **[`TODO.md`](../../TODO.md)**: reword *Rethink how settings sync*, which asks how a synced
  value meets a hand-edited file.
