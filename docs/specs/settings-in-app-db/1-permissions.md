# Step 1: Permissions

Moves the approval modes, the always rules and a chat's grants into `app.db`, and splits the
folder grants from the Kubernetes rules. Part of [the sequence](README.md), whose shared rules
apply.

## What moves

| Today | Becomes |
| --- | --- |
| `permissions.defaultMode`, `permissions.modes` in `settings.json` | `approval_modes` |
| `permissions.rules` in `settings.json` | `kube_rules` and `folder_grants`, `chat_id` NULL |
| `chat_grants` in `app.db` | `kube_rules` and `folder_grants`, `chat_id` set |

Migration `0006_permissions.sql` creates the three tables and drops `chat_grants` without copying
its rows, [by decision](README.md#no-import-by-decision).

## Approval modes

```sql
-- approval_modes: the approval mode of one cluster. cluster_id NULL is the
-- default, for every cluster with no row of its own.
CREATE TABLE approval_modes (
  cluster_id TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  mode       TEXT    NOT NULL CHECK (mode IN ('read-only', 'ask', 'auto')),
  updated_at INTEGER NOT NULL
) STRICT;

-- One mode per cluster, and one default.
CREATE UNIQUE INDEX approval_modes_cluster ON approval_modes (ifnull(cluster_id, ''));
```

A cluster's mode is its own row, else the default row, else `ask`. The default reaches clusters
alone: a cloud platform's request always asks. Letting it reach a cloud platform is a security
change.

A mode belongs to a cluster: the row names the cluster's record, not the name its kube-context
has today. A context that leaves the kubeconfig keeps its record, so it keeps its mode.

Settings lists the clusters with a kube-context, and `permissionModeSet` refuses any other. A
cluster with none takes the default mode.

**A rule belongs to a cluster too**, or to every cluster. Deleting a cluster deletes its mode and
its rules. A context added again under an old name is a new cluster: it starts at the default
mode with no rule of its own, and inherits no `allow`.

The file's `modes` list holds patterns, and the first match wins. Kstack itself only ever writes
an entry that names one context exactly (`SetMode`). A pattern could only come from a hand edit.
So the table has no patterns and no order.

A mode and a rule are read against the cluster an action runs on. `session.Session.Policy` takes
that cluster's id in place of its kube-context, and the action carries the id (`ClusterID`)
beside its context, which its words name it by. `kubeproxy.NewGrant` takes the id beside the
context, from the cluster `bash` already loads for the command. The read-only reason reads "this
cluster is read-only".

## Kubernetes rules

```sql
-- kube_rules: the user's rules over cluster actions, each a permissions.Rule.
-- chat_id NULL is an always rule; set, the rule lasts for that chat and goes
-- with it. cluster_id NULL is a rule for every cluster; set, the rule goes
-- with its cluster. '' in a matching column matches anything.
CREATE TABLE kube_rules (
  id         TEXT    PRIMARY KEY CHECK (id NOT IN ('', 'refused', 'refused-secrets')),
  chat_id    TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  effect     TEXT    NOT NULL CHECK (effect IN ('allow', 'deny', 'ask')),
  class      TEXT    NOT NULL CHECK (class IN ('upstream-write', 'destructive', 'secret-read')),
  cluster_id TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  namespace  TEXT    NOT NULL DEFAULT '',
  verb       TEXT    NOT NULL DEFAULT '',
  api_group  TEXT    NOT NULL DEFAULT '',
  kind       TEXT    NOT NULL DEFAULT '',
  inside     INTEGER NOT NULL DEFAULT 0 CHECK (inside IN (0, 1)),
  created_at INTEGER NOT NULL,
  -- A destructive write always asks.
  CHECK (NOT (effect = 'allow' AND class = 'destructive')),
  -- A rule for every cluster may only restrict.
  CHECK (cluster_id IS NOT NULL OR effect <> 'allow'),
  -- A Secret read rule names what a Secret read is classified with.
  CHECK (class <> 'secret-read' OR (
    inside = 0
    AND namespace <> '[cluster]'
    AND verb IN ('', 'get', 'list', 'watch')
    AND api_group IN ('', 'core')
    AND kind IN ('', 'secrets')
  ))
) STRICT;

CREATE INDEX kube_rules_chat_idx ON kube_rules (chat_id, created_at, id);
CREATE INDEX kube_rules_cluster_idx ON kube_rules (cluster_id);
```

The columns are `permissions.Rule`'s fields; `api_group` is its `Group`, since `group` is an SQL
keyword; `cluster_id` is its `ClusterID`. Every other matching column is a pattern. A rule names
one cluster exactly, or every cluster; a pattern over the kube-context's name may come later.
Every cluster restricts alone: an `allow` there would reach a cluster added after it, so a
`CHECK` refuses it and `CheckKubeRule` says so. An answer to an action writes a rule for the
action's cluster, and an action with none writes no rule (`permissions.Grantable`).
`AddKubeRule` refuses a cluster as `SetMode` does: gone or awaiting deletion, or with no
kube-context. A rule's line names its cluster by the record's kube-context.

`class` is stored by name, as `effect` and `mode` are, so the table does not depend on the
numbering of `permissions.Class`. A rule names one of the three classes a request is classified
as. The cloud platforms' proxies use the same three, but no rule matches a cloud platform's
action, so every row here is a cluster's. `NewHost`, which nothing classifies, is deleted from
`permissions.Class`, its label and the wire, and read-only covers `UpstreamWrite` to
`Destructive`.

Rules are read in `created_at, id` order. The order decides nothing: deny wins over ask, and ask
over allow.

One table holds both the always rules and a chat's rules, because they are the same type. That
gives one encoding and one check on read. `agent/chat` keeps its own statements for a chat's
rows, as it has for `chat_grants`. Its write still skips a rule the chat already holds
(`sameRule`), by a lookup in the write's transaction; the table has no unique index for it.

## Folder grants

```sql
-- folder_grants: a folder the sandbox opens to a run. chat_id NULL is an always
-- grant; set, the grant lasts for that chat and goes with it.
CREATE TABLE folder_grants (
  id         TEXT    PRIMARY KEY,
  chat_id    TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  folder     TEXT    NOT NULL CHECK (folder <> ''),
  access     TEXT    NOT NULL CHECK (access IN ('read', 'write')),
  created_at INTEGER NOT NULL
) STRICT;

-- One grant per folder, always or in a chat.
CREATE UNIQUE INDEX folder_grants_folder ON folder_grants (ifnull(chat_id, ''), folder);
```

`folder` is absolute and resolved. A folder grant is no longer a `permissions.Rule`: it is a
`FolderGrant`, read straight into `session.Folder`. `Rule` loses `Folder`, and a rule never
needs to "match no action".

Granting a folder already granted in the same place is an upsert on the index: it keeps the
row's `id` and `created_at` and changes its `access`.

A grant is still checked against the zones and the `PATH` on every read, wherever the `PATH` is
kept. One that fails is left out of runs and listed with its reason, on `SandboxFolder.refusal`,
the name [the README](README.md#a-row-kstack-cannot-use) gives a refused row's reason.

## A rule the table refuses

Every rule shape Kstack cannot apply is a `CHECK` on `kube_rules`: the two ids Kstack keeps for
`permissions.Refused` and `permissions.RefusedSecrets`, an `allow` of a destructive write, and a
Secret read rule that names anything a Secret read is not classified with. So a stored rule is
always one Kstack can apply. No rule is refused on read, and no rule holds another back.

Both writers check a rule in Go first (`ruleRefusal`), for a reason in the user's words.
`ruleClasses` narrows to the three classes the table allows, so no rule passes the Go check and
fails the table's. A test pins that: over one list of rules, the Go check and the `CHECK`s
refuse the same ones.

A mode row cannot be refused either: its `CHECK` allows only the three modes.

A folder grant can fail its check, since the check reads the disk. One that fails does not
touch the rules, and `folderRevoke` takes it like any other.

## A read that fails

- A failed read of the Kubernetes rules, always or a chat's, is no `allow` rule plus
  `permissions.Refused` and `permissions.RefusedSecrets`. Today a failed read of a chat's grants
  (`grantsIn`) adds `Refused` alone, which leaves Secret reads open; this step adds
  `RefusedSecrets`.
- A failed read of the folder grants is an error, not an empty list: the command or file tool
  that needed them fails with it. `session.Session.Folders` and `GrantedFolders` return it.
- A failed read of a mode is `read-only`.

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`run/permissions`**: `Rule` loses `Folder`, and `folderRuleRefusal` goes. `NewHost` is
  deleted. `Class` gains its stored name. The read-only reason names the cluster.
- **`services/settings`**: the modes and the always rules and folder grants read and write
  `app.db`. `ModeFor`, `SetMode` and `ClearMode` take a `ClusterID`, and `ContextModeState`
  becomes `ClusterModeState`. `Rules`, `AddRule`, `RemoveRule` and `PutRule` become
  `KubeRules`, `AddKubeRule` and `RemoveKubeRule`; an always folder grant has its own
  `FolderGrants`, `GrantFolder` and `RevokeFolder`, which publish as the rules do. `ruleRefusal`
  runs on a write alone, and `ruleClasses` holds the three cluster classes. `DiscardRefused` and
  `ErrNotHeld` are deleted. `PermissionSettings` and its three `Field*` constants leave
  `Settings`, and their entries leave `strictest` and `checks`.
- **`agent/chat`**: a chat's rules read and write `kube_rules`, its folder grants
  `folder_grants`. `GrantFolder` is the upsert, with no lookup, and `RevokeFolder` takes the
  chat. Each writes a chat's grant itself and hands an always one to `services/settings`, so the
  package holds no statement over a row whose `chat_id` is NULL. `RulesHeldReason` is deleted.
- **`run/session`, `run/kubeproxy`, `tools/bash`, `agent/chat`**: `Policy` takes the cluster's
  id and `NewGrant` takes it too, as above. `Folders` and `GrantedFolders` return an error, which
  `tools/bash`, `tools/read`, `tools/write` and `tools/edit` answer.
- **`graph`**: `permissionSettings` lists every cluster with a kube-context that is not awaiting
  deletion, by id. `approvalErr` and `permissionsAfter` lose their `ErrHeld` cases for rules and
  modes, and their messages stop naming `settings.json`.

## The wire

Each row has one writer. `services/settings` writes the modes, the always rules and the always
folder grants, and publishes after its commit. `agent/chat` writes a chat's rules and a chat's
folder grants, and notifies the chat's key. `sandboxFoldersWatch` reads again on either.

- `PermissionRule` becomes `KubeRule` and loses `folder`. `PermissionRuleInput` becomes
  `KubeRuleInput`.
- `PermissionSettings.rules` becomes `kubeRules`. `permissionRuleAdd` and `permissionRuleRemove`
  become `kubeRuleAdd` and `kubeRuleRemove`, over the always rules, and still answer
  `PermissionSettings`.
- `chatGrants` becomes `chatKubeRules`, and `chatGrantRemove` becomes
  `chatKubeRuleRemove(chatID: ChatID!, id: String!): [KubeRule!]!`, over the chat's rules alone.
- `folderRevoke` becomes `folderRevoke(chatID: ChatID, id: String!, duration: GrantDuration!)`,
  as `folderGrant` is: `Chat` removes a grant of the chat's and needs `chatID`, `Always` an
  always grant. It answers the lists `folderGrant` does.
- `SandboxFolder.reason` becomes `refusal`.
- `PermissionClass` loses `NewHost`.
- `PermissionSettings.held`, `permissionDiscardRefused` and `SandboxFolders.rulesHeld` are
  removed.
- `ModeSource` loses `Refused`. `Entry` means the cluster has its own row.
- `PermissionContextMode` becomes `PermissionClusterMode { clusterID: ClusterID!, mode, source }`,
  and `PermissionSettings.contexts` becomes `clusters`. The `pattern` and `own` fields go, since
  every entry is the cluster's own.
- `permissionModeSet` and `permissionModeClear` take `clusterID: ClusterID!` in place of
  `context`. Each is refused `KSTACK_RECORD_NOT_FOUND` for a cluster that is gone or awaiting
  deletion, and `permissionModeSet` `KSTACK_VALIDATION_ERROR` for one with no kube-context.
- `KubeRule` and `KubeRuleInput` take `clusterID: ClusterID` in place of `context`, null for
  every cluster. `kubeRuleAdd` is refused as `permissionModeSet` is for its cluster, and
  `KSTACK_VALIDATION_ERROR` for an `Allow` naming none.
- `kubeRuleRemove`, `folderGrant`, `folderRevoke` and the mode mutations lose "while the file's
  rules cannot be read" and "while the file's modes cannot be read".

The webview follows in `src/lib/permission-settings.tsx`, `sandbox-folders.tsx` and the Settings
dialog's permissions page. It is done when these hold, each with a test:

- The modes list names each cluster from `clustersWatch`, joined on `clusterID`.
- The rule form picks a cluster from that list, and offers every cluster to `Deny` and `Ask`
  alone.
- A folder grant's Remove and its access select depend on no rule.
- A chat's folder grant is revoked with `folderRevoke`.

## Tasks

All tasks start Planned. After each one the sidecar compiles and its tests pass. They land in
one pull request, since between tasks 2 and 3 the webview still speaks the old schema.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`, `run/permissions`, `agent/chat`: migration `0006` and its test, which builds the schema before it, migrates and checks each new table and each `CHECK`, that `chat_grants` is gone, and that every other table's seed survives. `Class` gains its stored name. A chat's rules and folder grants read and write their tables, and a failed read of a chat's rules adds `RefusedSecrets` (`grantsIn`). The wire is unchanged until task 2, so `agent/chat` still answers a chat's folder grant as a `Rule` naming its `Folder`, and `RemoveChatGrant` still removes one by id. `Rule` keeps `Folder` for that, and for the always grants still in the file. | Planned |
| 2 | `services/settings`, `run/permissions`, `run/session`, `run/kubeproxy`, `tools`, `agent/chat`, `graph`: the modes, the always rules and the always folder grants on `app.db`, with the failed-read rules above, and a mode read by the action's cluster. `Rule` loses `Folder` and `NewHost` goes, with the schema and resolvers of *The wire*; `chatKubeRules` and `chatKubeRuleRemove` stop answering folder grants. | Planned |
| 3 | `src/`: the webview. | Planned |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add rows for `approval_modes`, `kube_rules`
  and `folder_grants`; remove `chat_grants`; take the permissions group out of the *Sidecar
  settings* row.
- [`security-model.md`](../../security-model.md): in the row about a malformed value in
  `settings.json`, replace the rules and modes with a row saying the table refuses a rule Kstack
  cannot apply, naming the test that pins each `CHECK`.
- A record under `docs/security/`, since the rules' read-back becomes the table's `CHECK`s, a
  failed read of a chat's rules now also refuses Secret reads, no rule closes the folder grants,
  a rule names a cluster by id and goes with it, and no `allow` names every cluster.
- **ADR**: a folder grant is its own row, and a mode and a rule belong to a cluster. It supersedes the
  rule half of
  [a folder grant is a rule, and the file tools walk it by handle](../../adr/2026-10-04-a-folder-grant-is-a-rule.md);
  the file tools still walk a grant by handle. Every `CLAUDE.md` link to the old ADR's rule half
  is repointed in the same commit. `docs/security/` is append-only, so its record of
  2026-10-04 keeps its link, and the new record names the new ADR.
- `sidecar/internal/services/settings/CLAUDE.md`, `sidecar/internal/agent/CLAUDE.md`,
  `sidecar/internal/run/CLAUDE.md` and `sidecar/internal/tools/CLAUDE.md`: the permissions and
  folder grant paragraphs.
