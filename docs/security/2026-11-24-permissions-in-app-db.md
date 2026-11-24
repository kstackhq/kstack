# Security record — the permissions move into app.db, 24 November 2026

**Subject:** the approval modes, the always rules and a chat's grants, moved from
`settings.json` and `chat_grants` into `approval_modes`, `kube_rules` and `folder_grants`
(migration `0006`). The decision is
[ADR: a folder grant is its own row, and a mode and a rule belong to a cluster](../adr/2026-11-24-a-folder-grant-is-its-own-row-and-a-mode-and-a-rule-belong-to-a-cluster.md).
The living model is [security-model.md](../security-model.md).

## What it changes

- A rule's read-back was the file's checks, and a value they refused held the whole field: no
  `allow`, plus `permissions.Refused` and `RefusedSecrets`. It is now `kube_rules`' `CHECK`s,
  which refuse each shape Kstack cannot apply before it is stored, so a stored rule always applies
  and no rule holds another back.
- A failed read of a chat's rules was `permissions.Refused` alone, which left Secret reads to the
  mode. It is now `permissions.Unread()`, `RefusedSecrets` included, as a failed read of the
  always rules is.
- A folder grant is no rule. Nothing about the rules can close it any more, and its own check,
  against the zones and the `PATH`, still runs on every read.
- A mode and a rule are keyed by the cluster's record, never its context's name. Deleting a
  cluster deletes its mode and its rules, and a context added again under an old name inherits
  no `allow`. A rule may name no cluster, and then applies to every cluster.
- No `allow` names every cluster: one would reach a cluster added after it.

## What holds

- **Every shape Kstack refuses is a `CHECK`**: the two ids Kstack keeps, an `allow` of a
  destructive write, an `allow` naming no cluster, a class no rule decides, and a Secret read
  rule naming more than a Secret read (`TestMigration0006AddsThePermissionTables`). The Go check refuses the same rules in the
  user's words (`TestTheGoCheckAndTheTableRefuseTheSameRules`, `TestARuleKstackCannotApplyIsRefused`).
- **A failed read never permits.** Rules read as `Unread()` (`TestAGrantsReadThatFailsRefuses`,
  `TestAPolicyThatCannotBeReadRefuses`, `TestUnreadRulesRefuseWritesAndSecretReads`), a mode as
  read-only (`TestAPolicyThatCannotBeReadRefuses`), and the folder grants as an error that fails
  the run, the file tool or the send (`TestAFoldersReadThatFailsIsAnError`,
  `TestAFoldersReadThatFailsFailsTheRun`, `TestAFoldersReadThatFailsFailsTheCall`).
- **A mode belongs to a cluster Settings lists.** `permissionModeSet` refuses a cluster that is
  gone, awaiting deletion or has no kube-context (`TestAModeIsRefusedForAClusterSettingsDoesNotList`,
  `TestAPermissionRefusalCarriesItsCode`). A cloud platform's request still always asks: no
  mode reaches it.
- **A rule names its cluster by id, and goes with it.** `kubeRuleAdd` refuses a cluster as
  `permissionModeSet` does (`TestARuleNamesAClusterSettingsListsAndGoesWithIt`), a rule matches
  only its own cluster's actions, whatever their context is named
  (`TestAGrantRuleNamesTheActionsCluster`), and an approval's rule names the action's cluster
  (`TestAChatAnswerWritesAClassSixGrant`).
- **A folder grant is still checked where it is read**, and a stored one that fails is listed
  with its refusal and given to no run (`TestAGrantIsListedWithItsRefusal`,
  `TestFoldersForAnswersOnlyCheckedFolders`).
- **Each row has one writer.** `services/settings` writes the rows whose `chat_id` is NULL,
  `agent/chat` a chat's; a chat's rows go with the chat (`TestAChatsGrantsGoWithTheChat`).

## Residual risk

- **A rule's words name its cluster by the record's kube-context**, which two clusters from two
  kubeconfig files can share. The rule still matches one cluster alone; Settings names each
  cluster by its name and context in the form.
- **An install of `desktop/v0.0.1` loses its modes, rules and chats' grants**, by decision. A
  cluster the user set read-only becomes `ask` until they set it again; deny and ask rules go too.
  The release notes must say so.
