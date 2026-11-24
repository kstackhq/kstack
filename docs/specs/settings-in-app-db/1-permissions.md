# Step 1: Permissions

Moves the approval modes, the always rules and a chat's grants into `app.db`. Part of
[the sequence](README.md), whose shared rules apply.

## What moves

| Today | Becomes |
| --- | --- |
| `permissions.defaultMode`, `permissions.modes` in `settings.json` | `approval_modes` |
| `permissions.rules` in `settings.json` | `permission_rules`, `chat_id` NULL |
| `chat_grants` in `app.db` | `permission_rules`, `chat_id` set |

Migration `0006_permissions.sql` creates both tables and drops `chat_grants` without copying its
rows, [by decision](README.md#no-import-by-decision).

## Approval modes

```sql
-- approval_modes: the approval mode of one kube-context. context NULL is the
-- default, for every context with no row of its own.
CREATE TABLE approval_modes (
  context    TEXT,
  mode       TEXT    NOT NULL CHECK (mode IN ('read-only', 'ask', 'auto')),
  updated_at INTEGER NOT NULL,
  CHECK (context IS NULL OR context <> '')
) STRICT;

-- One mode per context, and one default.
CREATE UNIQUE INDEX approval_modes_context ON approval_modes (ifnull(context, ''));
```

`context` is the kube-context's plain name. A context's mode is its own row, else the default
row, else `ask`.

The key is the context's name, not a `cluster_id`, so a row does not go when its cluster does.
That matches the rules, which also name a context: both are read against the context an action
carries (`permissions.Action.Context`), and a context deleted and added again under the same name
keeps its mode.

The file's `modes` list holds patterns, and the first match wins. Kstack itself only ever writes
an entry that names one context exactly (`SetMode`). A pattern could only come from a hand edit.
So the table has no patterns and no order.

## Rules

```sql
-- permission_rules: the user's rules, each a permissions.Rule. chat_id NULL is
-- an always rule; set, the rule lasts for that chat and goes with it. '' in a
-- matching column matches anything.
CREATE TABLE permission_rules (
  id         TEXT    PRIMARY KEY,
  chat_id    TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  effect     TEXT    NOT NULL CHECK (effect IN ('allow', 'deny', 'ask')),
  class      INTEGER NOT NULL CHECK (class BETWEEN 1 AND 6),
  context    TEXT    NOT NULL DEFAULT '',
  namespace  TEXT    NOT NULL DEFAULT '',
  verb       TEXT    NOT NULL DEFAULT '',
  api_group  TEXT    NOT NULL DEFAULT '',
  kind       TEXT    NOT NULL DEFAULT '',
  inside     INTEGER NOT NULL DEFAULT 0 CHECK (inside IN (0, 1)),
  folder     TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  CHECK (folder = '' OR class IN (1, 2))
) STRICT;

CREATE INDEX permission_rules_chat_idx ON permission_rules (chat_id);
```

The columns are `permissions.Rule`'s fields; `api_group` is its `Group`, since `group` is an SQL
keyword. Rules are read in `created_at, id` order. The order decides nothing: deny wins over ask,
and ask over allow. Replacing a rule (`PutRule`, `GrantFolder`) updates its row in place, so it
keeps its `id` and its `created_at`.

One table holds both the always rules and a chat's grants, because they are the same type. That
gives one encoding and one check on read. `agent/chat` keeps its own statements for a chat's
rows, as it has for `chat_grants`.

A folder grant is a rule, so it moves here. It is still checked against the zones and the
`PATH`, wherever the `PATH` is kept.

## A refused rule

The read-back refuses a rule when:

- its shape is one Kstack cannot apply (`folderRuleRefusal`, `secretRuleRefusal` and the rest);
- its `id` is `refused` or `refused-secrets`, the ids of the two rules Kstack adds itself and
  never stores.

| Refused row | What Kstack does |
| --- | --- |
| An always rule | Every always `allow` rule is ignored, and `permissions.Refused` and `permissions.RefusedSecrets` apply. This is today's behaviour while `permissions.rules` is held. |
| A chat's rule | The same, for that chat's rules. Today a chat's bad grant adds `Refused` alone. |

**While an always rule is refused, adding an always `allow` rule is refused**, a folder grant
included, since it would be ignored. An approval answered "always" fails with that reason, as it
does today. Adding a `deny` or an `ask` rule works: it only restricts. A chat's rules follow the
same rule, for that chat alone.

Removing works throughout: `permissionRuleRemove`, `folderRevoke` and `chatGrantRemove` take any
rule, refused or not, while a rule is refused.

A mode row cannot be refused: its `CHECK` allows only the three modes.

## A read that fails

- A failed read of the rules, always or a chat's, is no `allow` rule plus `permissions.Refused`
  and `permissions.RefusedSecrets`. Today a failed read of a chat's grants (`grantsIn`) adds
  `Refused` alone, which leaves Secret reads open; this step adds `RefusedSecrets`.
- A failed read of a mode is `read-only`.

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`services/settings`**: `ModeFor`, `DefaultMode`, `SetDefaultMode`, `SetMode`, `ClearMode`,
  `Rules`, `AddRule`, `RemoveRule` and `PutRule` read and write `app.db`. `DiscardRefused` and
  `ErrNotHeld` are deleted. `PermissionSettings` and its three `Field*` constants leave
  `Settings`, and their entries leave `strictest` and `checks`.
- **`agent/chat`**: a chat's grants read and write `permission_rules`. `RulesHeldReason` becomes
  `RulesRefusedReason`.
- **`graph`**: `approvalErr` and `permissionsAfter` lose their `ErrHeld` cases for rules and
  modes, and their messages stop naming `settings.json`.

## The wire

- `PermissionRule` gains `refusal: String`.
- `PermissionSettings.held` and `permissionDiscardRefused` are removed.
- `rulesHeld` is renamed `rulesRefused`, and is true while an always rule is refused.
- `ModeSource` loses `Refused`. `Entry` means the context has its own row. The pattern and `own`
  fields go, since every entry is the context's own.
- `permissionRuleRemove`, `folderRevoke`, `chatGrantRemove` and the mode mutations lose "while
  the file's rules cannot be read" and "while the file's modes cannot be read".

The webview follows in `src/lib/permission-settings.tsx` and the Settings dialog's permissions
page. It is done when these hold, each with a test:

- A refused rule shows its reason.
- A folder grant's Remove stays enabled while a rule is refused, and its access select does not.
  `sandbox-folders.tsx` passes both controls one `busy` flag today, so it needs one for each.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`, `agent/chat`: migration `0006` and its test, which builds the schema before it, migrates and checks each new table and each `CHECK`, that `chat_grants` is gone, and that every other table's seed survives. In the same change, a chat's grants read and write `permission_rules`. | Planned |
| 2 | `services/settings`: the modes and the always rules on `app.db`, with the refused and failed-read rules above. | Planned |
| 3 | `graph`, `src/`: the wire and the webview. | Planned |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add rows for `approval_modes` and
  `permission_rules`; remove `chat_grants`; take the permissions group out of the *Sidecar
  settings* row.
- [`security-model.md`](../../security-model.md): in the row about a malformed value in
  `settings.json`, replace the rules and modes with a row about a refused rule, naming the tests
  that pin the table above.
- A record under `docs/security/`, since the read-back of the rules changes and a chat's bad
  grant now also refuses Secret reads.
- `sidecar/internal/services/settings/CLAUDE.md` and `sidecar/internal/agent/CLAUDE.md`: the
  permissions paragraphs.
