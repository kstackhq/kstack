# The cluster proxy and permissions

[← Security model](../security-model.md)

A sandboxed command reaches one cluster, its chat's, and only through Kstack's proxy. A read passes. Each change is classified and then runs, waits on the user or is refused, as the cluster's mode and the user's rules decide. Secret data is redacted unless the user allowed that read. The rows defend against a model, or text it read, that tries to change a cluster or read a secret the user did not approve. They assume the user reads the request before approving it, and that whatever Kstack cannot read or decode refuses. Cluster reads other than Secret data reach the model's provider by decision.

Editing a rule from Settings is reviewed in [the Permissions pane is reachable](../security/2026-11-18-the-permissions-pane-is-reachable.md).

## Protections

| ID | Protection | Status | Residual |
| --- | --- | --- | --- |
| KUBE-1 | A sandboxed command reaches the chat's cluster alone, and a read passes. | **Enforced** | — |
| KUBE-2 | `exec`, `attach`, `port-forward`, `proxy`, impersonation and a service account token come back `Forbidden` and never reach the server. | **Enforced** | — |
| KUBE-3 | Every write is classified and decided under the write lock. It runs, waits on the user or is refused as `permissions.Decide` answers for its class, the cluster's mode and the user's rules. | **Enforced** | — |
| KUBE-4 | The verdict matches the ordered table over every rule that can exist, and holds under any order of the rules. | **Enforced** | — |
| KUBE-5 | An allowed write is recorded before it is forwarded, and one whose record fails forwards nothing. | **Enforced** | — |
| KUBE-6 | A refused write is a `Forbidden` naming the mode or the rule. | **Enforced** | — |
| KUBE-7 | A destructive write is never allowed unasked. | **Enforced** | — |
| KUBE-8 | A mode or rules Kstack cannot read refuse. The mode is read-only, `Refused` refuses every cluster write and `RefusedSecrets` every Secret read. | **Enforced** | — |
| KUBE-9 | A write that asks reaches the server only once `Asker` approves, as the bytes shown. | **Enforced** | — |
| KUBE-10 | Writes ask one at a time. | **Enforced** | — |
| KUBE-11 | A write whose wait ends forwards nothing. | **Enforced** | — |
| KUBE-12 | A background task and a run with no asker refuse every write unasked, whatever the mode or rules. | **Enforced** | — |
| KUBE-13 | End to end, a sandboxed delete waits on one request and reaches the server once approved, and a denial comes back `Forbidden`. | **Enforced** | — |
| KUBE-14 | A request's allow answers write a rule only where a grant lifts the verdict: `Unmatched`, not a dry run, with a cluster. Kstack refuses them on any other request, a call's own included. | **Enforced** | — |
| KUBE-15 | No rule or mode decides a cloud platform's action. Each asks, Once or Deny. | **Enforced** | — |
| KUBE-16 | A chat or always rule names the action's cluster by its record and its namespace as a literal. A cluster-scoped action's or a Namespace's rule names the group and resource too, and any other leaves the Namespace object out. A rule goes with its cluster. | **Enforced** | — |
| KUBE-17 | A chat's rule is a `kube_rules` row listed and removed beside the composer, and an always rule is in Settings. Each is written before the decision lands, and once. | **Enforced** | — |
| KUBE-18 | A command's rule names the verb and the resource too, and ends with the command's grant. | **Enforced** | — |
| KUBE-19 | A cluster write's request names the classified action and the classifier's summary, written from the parsed path. | **Enforced** | — |
| KUBE-20 | For a `PUT` or `PATCH` of a named object that asks, the request shows a diff of the object now against a dry run of the exact bytes, on a stable group version alone. | **Enforced** | `managedFields` is dropped from the diff. |
| KUBE-21 | The diff redacts Secret values and every last-applied annotation. | **Enforced** | — |
| KUBE-22 | The raw request is one fold away under *Show the request*: the method, the path and query as sent, the media type, and the body byte for byte. It is drawn open, with Approve waiting on its body, when there is no diff or the diff is cut at 2,000 lines. Approve waits on a folded diff. | **Enforced** | — |
| KUBE-23 | A write Kstack cannot show is refused: a query that does not parse, a body it cannot draw as text or decode as its media type, and one past 1 MiB. | **Enforced** | — |
| KUBE-24 | A write carrying `[redacted]` or its base64 in any decoded string is refused. So are a helm release whose decoded body carries it, and a helm release write after the command read Secret data redacted in its namespace or across the cluster. | **Enforced** | — |
| KUBE-25 | A dry run runs unasked as a read, and its heading ends *(dry run)*. It is only a `POST`, `PUT` or `PATCH` whose every `dryRun` is `All`, on a stable group version the API server serves itself. | **Enforced** | Its body reaches the cluster's admission webhooks. |
| KUBE-26 | A sandboxed read of a Secret reads its values, `last-applied-configuration`, and a helm release's values and manifest Secrets `[redacted]` unless `Authorize` permits it for the session, per read: a rule, the user's answer to the read's request, or `Auto`. | **Enforced** | — |
| KUBE-27 | The rewriter fails closed on anything it cannot read. | **Enforced** | — |
| KUBE-28 | A denial, an abandoned wait, a read with no one to ask and one whose record fails pass redacted with a 200. | **Enforced** | — |
| KUBE-29 | A metadata-only read of a Secret asks no one and passes redacted. | **Enforced** | — |
| KUBE-30 | Rules Kstack cannot read keep Secret data redacted. | **Enforced** | — |
| KUBE-31 | A session marked `NoSecretData` never reads Secret data, ahead of every rule and under every mode. | **Enforced** | — |
| KUBE-32 | A session marked `NoPrompts` never asks. | **Enforced** | — |
| KUBE-33 | `NoSecretData` and `NoPrompts` are the session's alone, which the proxy copies onto the policy it reads. A chat's session carries neither, and the monitor's carries both. | **Enforced** | — |
| KUBE-34 | A stored permission rule is one Kstack can apply. `kube_rules`' `CHECK`s refuse the ids Kstack keeps, an `allow` of a destructive write, an `allow` naming no cluster, a class no rule decides and a Secret read rule naming more than a Secret read. The Go check refuses the same rules in the user's words. | **Enforced** | — |
| KUBE-35 | A failed read of the rules allows nothing and refuses every cluster write and Secret read. A failed read of a mode is read-only. | **Enforced** | — |
| KUBE-36 | A failed read of the folder grants fails what needed them. | **Enforced** | — |
| KUBE-37 | A monitor run changes nothing and asks no one. Its session is read-only at the proxy, never reads Secret data, never asks, and has no network and no folder grant. | **Enforced** | — |
| KUBE-38 | Every write and every Secret read a monitor run sends is refused at the proxy and recorded. | **Enforced** | — |
| KUBE-39 | In a monitor run, a file call that would ask is denied and a background command is refused. | **Enforced** | — |
| KUBE-40 | A monitor run is offered no `Agent`, `Memory`, `WebFetch`, `TaskStop`, search, `LogsView` or `LogsWatch`. | **Enforced** | — |
| KUBE-41 | A monitor run refuses a machine with no sandbox. | **Enforced** | — |
| KUBE-42 | A monitor's folder, `<data>/monitor/<cluster id>/`, is 0700 and its run's alone. A chat's command cannot read it, and the monitor's cannot read a chat's or another cluster's. | **Enforced** | — |
| KUBE-43 | A monitor's folder goes, its run cancelled first, once its cluster is not a live row. | **Enforced** | — |
| KUBE-44 | A monitor run is recorded as a chat's run is: an `agent_runs` row under its cluster and no chat, with its model calls, tool calls and every refusal the proxy recorded beneath it. | **Enforced** | — |
| KUBE-45 | A monitor run's record is cascaded with its cluster, and failed at the next start when a crash left it. | **Enforced** | — |
| KUBE-46 | A tool the user turned off in Settings › Tools is offered to no turn after the change: no chat, subagent or monitor run. | **Enforced** | — |
| KUBE-47 | A call of a tool the user turned off is refused. | **Enforced** | — |
| KUBE-48 | The tool setting only removes a tool from its provider's list, and the wire writes only listed names. | **Enforced** | — |
| KUBE-49 | TaskStop is always on. The wire refuses it off, and a stored row turning it off is ignored. | **Enforced** | — |
| KUBE-50 | A stored tool setting Kstack cannot use turns its tool off. | **Enforced** | — |
| KUBE-51 | Cluster reads leave the machine on the model's word. Everything the user's credentials can read, less Secret values the user has not allowed showing, can reach the provider with no per-call approval. | **By decision** | — |
| KUBE-52 | A helm release passes redacted inside. Its chart, default values, rendered notes and other objects' manifests pass, and so does a Secret typed `helm.sh/release.v1` by hand, all but its `config` and its manifests' Secrets, unless the user allowed showing them. A release write runs once its command read no Secret data redacted in its namespace. | **By decision** | An audit of what else carries secrets is in TODO. |
| KUBE-53 | No kube-context's mode reaches a cloud platform, so a read-only context does not stop a cloud platform's change. | **By decision** | — |

## Evidence

### KUBE-1

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`, `sidecar/internal/tools/bash/upstream.go`, `sidecar/internal/tools/bash/proxy.go`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestThePolicy`, `TestThePathParse` — the proxy's policy and its parse of a request's path.
- `TestAPathThatIsNotCanonicalIsRefused` — a path that is not canonical is refused.
- `TestASelfReviewPassesUnasked` — a self review passes unasked.

### KUBE-2

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`, `sidecar/internal/tools/bash/upstream.go`, `sidecar/internal/tools/bash/proxy.go`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestEachRefusalIsAForbiddenStatus` — each refusal is a `Forbidden`, the token among them.

### KUBE-3

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestEveryRequestIsClassified`, `TestAScaleIsClassFourOnlyInAPlainForm` — every write is classified.
- `TestDecideRunsUnderTheWriteLock`, `TestDecideFollowsTheModeTable` — the write is decided under the write lock.

### KUBE-4

- **Where:** `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestTheVerdictMatchesTheOrderedTable`, `TestAVerdictIsOrderIndependent` — the verdict matches the ordered table over every rule that can exist, and holds under any order of the rules.

### KUBE-5

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestAnAllowedWriteForwardsUnasked` — an allowed write is recorded before it is forwarded, and one whose record fails forwards nothing.

### KUBE-6

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestADeniedWriteIsAForbiddenStatus`, `TestAReadOnlyContextRefusesEveryWrite` — a refused write is a `Forbidden` naming the mode or the rule.

### KUBE-7

- **Where:** `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestClassFiveAsksInEveryMode`, `TestAClassFourRuleCoversClassFive` — a destructive write is never allowed unasked.

### KUBE-8

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`, `sidecar/internal/tools/bash/upstream.go`, `sidecar/internal/tools/bash/proxy.go`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestAPolicyThatCannotBeReadRefuses`, `TestAModeKstackCannotUseIsReadOnly`, `TestAGrantsReadThatFailsRefuses`, `TestUnreadRulesRefuseWritesAndSecretReads` — a mode or rules Kstack cannot read refuse.

### KUBE-9

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestAWriteWaitsOnTheAsker`, `TestADeniedWriteIsForbidden` — a write that asks reaches the server only once `Asker` approves, as the bytes shown.

### KUBE-10

- **Where:** `sidecar/internal/run/kubeproxy`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestWritesAskOneAtATime`, `TestQueuedWritesAreBounded` — writes ask one at a time.

### KUBE-11

- **Where:** `sidecar/internal/run/kubeproxy`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestAWriteWhoseWaitEndsForwardsNothing` — a write whose wait ends forwards nothing.

### KUBE-12

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`, `sidecar/internal/tools/bash/upstream.go`, `sidecar/internal/tools/bash/proxy.go`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestABackgroundGrantRefusesWrites`, `TestABackgroundWriteIsRefused`, `TestAForegroundGrantAsksThroughTheRuntime`, `TestAGrantWithNoAskerRefusesWrites` — a background task and a run with no asker refuse every write unasked, whatever the mode or rules.

### KUBE-13

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`, `sidecar/internal/tools/bash/upstream.go`, `sidecar/internal/tools/bash/proxy.go`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestASandboxedDeleteAsksAndRuns`, `TestASandboxedDeleteDeniedIsForbidden` — end to end, a delete waits on one request and reaches the server once approved, and a denial comes back `Forbidden`.

### KUBE-14

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestGrantableIsUnmatched`, `TestADryRunIsNotGrantable`, `TestADryRunAsksWithNoGrant`, `TestAnUngrantableActionTakesOnceOrDenyAlone`, `TestACallsOwnApprovalTakesOnceOrDenyAlone` — the allow answers write a rule only where a grant lifts the verdict, and Kstack refuses them on any other request, a call's own included.

### KUBE-15

- **Where:** `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestNoRuleReachesACloudPlatformAction`, `TestACloudPlatformActionAlwaysAsks` — no rule or mode decides a cloud platform's action, and each asks.

### KUBE-16

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestAGrantRuleNamesTheActionsCluster`, `TestARuleNamesAClusterSettingsListsAndGoesWithIt`, `TestAClusterScopedGrantNamesTheResource` — what a chat or always rule names, and that a rule goes with its cluster.

### KUBE-17

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestChatWritesAGrantBeforeTheDecisionLands`, `TestAlwaysWritesTheRuleIntoTheSettings`, `TestARuleAlreadyHeldIsWrittenOnce`, `TestAChatsRulesAreListedAndRemoved`, `chat-grants.test.tsx` — a chat's rule is listed and removed, an always rule is in Settings, and each is written before the decision lands and once.

### KUBE-18

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/run/permissions`
- **Records:** [The permissions engine](../security/2026-10-02-the-permissions-engine.md), [The prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md)
- `TestACommandRuleNamesTheChange`, `TestACommandAnswerAllowsTheRestOfTheCommand`, `TestACommandRuleEndsWithTheGrant` — a command's rule names the verb and the resource too, and ends with the command's grant.

### KUBE-19

- **Where:** `sidecar/internal/run/kubeproxy/write.go`, `src/components/chat/chat-transcript/transcript.tsx`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestARequestCarriesTheAction`, `TestAWriteNamesItsSubresource` — the request carries the classified action.
- Note: [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md) supersedes the request paragraph of [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md).

### KUBE-20

- **Where:** `sidecar/internal/run/kubeproxy/write.go`, `sidecar/internal/run/kubeproxy/diff.go`, `src/components/chat/chat-transcript/transcript.tsx`, `src/components/chat/chat-transcript/diff-block.tsx`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestADiffForAPutShowsTheChange`, `TestADiffForAPatch`, `TestOnlyAPromptedWriteIsPreviewed`, `TestAnAPIThatMayIgnoreADryRunIsNotPreviewed`, `TestAPostOrADeleteHasNoDiff`, `TestADiffDropsWhatTheServerAssigns`, `TestADiffShowsAStatusWrite`, `TestTheDiffsRequestsAreBounded`, `TestADiffUsesTheEndpoint` — the diff.
- Note: `managedFields` dropped from the diff is a residual of the prompt names the action.

### KUBE-21

- **Where:** `sidecar/internal/run/kubeproxy/diff.go`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestADiffOfASecretMarksTheChangedKeys`, `TestADiffHidesTheLastApplied` — the diff redacts Secret values and the last-applied annotation.

### KUBE-22

- **Where:** `sidecar/internal/run/kubeproxy/write.go`, `sidecar/internal/run/kubeproxy/diff.go`, `src/components/chat/chat-transcript/transcript.tsx`, `src/components/chat/chat-transcript/diff-block.tsx`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestADiffIsCutAtTwoThousandLines` — the diff is cut at 2,000 lines.
- `cluster writes`, `chat-transcript/transcript.test.tsx`, `chat-transcript/diff-block.test.tsx` — the heading, the diff, the folds and *Sent by*.
- Note: the command under *Sent by* is a fold Approve does not wait on, and so is the request behind a whole diff.

### KUBE-23

- **Where:** `sidecar/internal/run/kubeproxy/write.go`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestAWriteThatCannotBeShownIsRefused`, `TestAWriteThatDoesNotDecodeIsRefused`, `TestABodyWithTrailingDataIsRefused`, `TestAWriteWithAQueryThatDoesNotParseIsRefused` — a write that cannot be shown, decoded or parsed is refused.

### KUBE-24

- **Where:** `sidecar/internal/run/kubeproxy/write.go`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestAWriteCarryingRedactedIsRefused`, `TestAWriteCarryingRedactedIsStillRefused` — a write carrying `[redacted]` is refused.
- `TestAHelmReleaseWriteFollowsWhatTheCommandRead`, `TestARedactedReleaseIsRefusedUnderTheGrant` — a helm release write follows what the command read, and a redacted release is refused.

### KUBE-25

- **Where:** `sidecar/internal/run/kubeproxy/write.go`, `src/components/chat/chat-transcript/transcript.tsx`
- **Records:** [the prompt names the action](../security/2026-10-04-the-prompt-names-the-action.md), [cluster writes ask](../security/2026-09-29-cluster-writes-ask.md)
- `TestADryRunIsARead` — a dry run runs unasked as a read.
- Note: a dry run reaching webhooks is a residual of the prompt names the action.

### KUBE-26

- **Where:** `sidecar/internal/run/kubeproxy/secretread.go`, `sidecar/internal/run/kubeproxy/redact*.go`, `sidecar/internal/run/permissions`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestSecretDataIsRedactedWithoutTheGrant`, `TestAPromptedSecretReadWaitsForTheDecision`, `TestAnAllowedSecretReadPassesUnredacted`, `TestAWatchAndAWholeTableAreClassSix`, `TestASecretReadWaitsItsTurnBehindAWrite`, `TestACommandAnswerCoversTheReadsThatFollow`, `TestAClusterWideListAsksForTheContext`, `TestASecretReadGrantNamesItsScope` — Secret data is redacted unless `Authorize` permits the read.
- `TestASandboxedRunReadsASecretRedacted` — end to end.

### KUBE-27

- **Where:** `sidecar/internal/run/kubeproxy/redact*.go`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestTheWalkRedactsASecretsData`, `TestATableIsRedacted`, `TestEachWatchEventArrivesAlone`, `TestAHelmReleasesValuesAreRedacted`, `TestAHelmReleasesSecretsAreRedacted`, `TestAnUnreadableValueFailsClosed`, `TestAReleaseThatDoesNotDecodeFailsClosed` — the rewriter.

### KUBE-28

- **Where:** `sidecar/internal/run/kubeproxy/secretread.go`, `sidecar/internal/run/permissions`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestADeniedSecretReadIsRedactedAndTwoHundred`, `TestASecretReadWithNoAskerIsRedacted` — a denial and a read with no one to ask pass redacted with a 200.

### KUBE-29

- **Where:** `sidecar/internal/run/kubeproxy/secretread.go`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestAMetadataOnlyReadDoesNotAsk` — a metadata-only read asks no one.

### KUBE-30

- **Where:** `sidecar/internal/run/kubeproxy/secretread.go`, `sidecar/internal/run/permissions`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestAPolicyThatCannotBeReadRefuses`, `TestUnreadRulesRefuseWritesAndSecretReads` — rules Kstack cannot read keep it redacted.

### KUBE-31

- **Where:** `sidecar/internal/run/session`, `sidecar/internal/run/permissions`, `sidecar/internal/run/kubeproxy/write.go`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestNoSecretDataRefusesClassSixAheadOfEveryRule`, `TestSecretDataIsRedactedWithoutTheGrant` — `NoSecretData` refuses a Secret data read ahead of every rule.

### KUBE-32

- **Where:** `sidecar/internal/run/session`, `sidecar/internal/run/permissions`, `sidecar/internal/run/kubeproxy/write.go`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestNoPromptsRefusesWhatWouldAsk` — `NoPrompts` refuses what would ask.

### KUBE-33

- **Where:** `sidecar/internal/run/session`, `sidecar/internal/run/permissions`, `sidecar/internal/run/kubeproxy/write.go`
- **Records:** [Secret data is a permissioned read](../security/2026-10-05-secret-data-is-a-permissioned-read.md)
- `TestNarrowKeepsTheParentsIdentity` — the marks are the session's alone.
- `TestAChatsSessionAsksAndReadsSecretData` — a chat's session carries neither.
- `TestTheMonitorSessionIsReadOnlyAndAsksNobody` — the monitor's carries both.

### KUBE-34

- **Where:** `sidecar/internal/appdb/migrations/0006_settings.sql`, `sidecar/internal/services/settings/permissions.go`
- **Records:** [Record](../security/2026-11-25-settings-in-app-db.md)
- `TestMigration0006AddsTheSettingsTables` — the table and its `CHECK`s.
- `TestTheGoCheckAndTheTableRefuseTheSameRules` — the Go check and the table refuse the same rules.

### KUBE-35

- **Where:** `sidecar/internal/services/settings/permissions.go`, `sidecar/internal/agent/chat/grants.go`
- **Records:** [Record](../security/2026-11-25-settings-in-app-db.md)
- `TestAPolicyThatCannotBeReadRefuses`, `TestAGrantsReadThatFailsRefuses` — a failed read of the rules or of a mode refuses.

### KUBE-36

- **Where:** `sidecar/internal/agent/chat/grants.go`
- **Records:** [Record](../security/2026-11-25-settings-in-app-db.md)
- `TestAFoldersReadThatFailsIsAnError` — a failed read of the folder grants is an error.

### KUBE-37

- **Where:** `sidecar/internal/agent/chat/monitor.go`, `sidecar/internal/agent/chat/grants.go`
- **Records:** [the monitoring session](../security/2026-10-05-the-monitoring-session.md)
- `TestTheMonitorSessionIsReadOnlyAndAsksNobody` — the session is read-only, reads no Secret data and asks nobody.
- `TestAMonitorHasNoNetwork`, `TestAChatsFoldersNeverReachTheMonitor` — no network and no folder grant, through the real sandbox and proxy.
- Note: nothing calls `RunMonitor` yet but its tests.

### KUBE-38

- **Where:** `sidecar/internal/agent/chat/monitor.go`, `sidecar/internal/agent/chat/grants.go`
- **Records:** [the monitoring session](../security/2026-10-05-the-monitoring-session.md)
- `TestAMonitorWriteIsRefusedAndRecorded`, `TestAMonitorReadsASecretRedactedAndRecorded` — a write and a Secret read are refused and recorded, through the real sandbox and proxy.
- `TestTheMonitorsAskerRecordsAndNeverAsks` — the monitor's asker records and never asks.

### KUBE-39

- **Where:** `sidecar/internal/agent/chat/monitor.go`, `sidecar/internal/agent/chat/grants.go`
- **Records:** [the monitoring session](../security/2026-10-05-the-monitoring-session.md)
- `TestAMonitorCallThatAsksIsDenied` — a file call that would ask is denied.
- `TestAMonitorBackgroundCommandIsRefused` — a background command is refused.

### KUBE-40

- **Where:** `sidecar/internal/agent/chat/monitor.go`
- **Records:** [the monitoring session](../security/2026-10-05-the-monitoring-session.md)
- `TestRunMonitorRecordsARun` — the offer.
- Note: its `LogsRead` reads run on the cluster connection, outside the proxy, and read only `pods/log`.

### KUBE-41

- **Where:** `sidecar/internal/agent/chat/monitor.go`
- **Records:** [the monitoring session](../security/2026-10-05-the-monitoring-session.md)
- `TestRunMonitorRefuses` — what a monitor run refuses.

### KUBE-42

- **Where:** `sidecar/internal/agent/chat/chatdir.go`, `sidecar/internal/agent/chat/sweep.go`
- `TestANewMonitorDirIsOwnerOnly` — the folder is 0700.
- `TestTheMonitorCannotReadAChatsFolder`, `TestAChatCannotReadTheMonitorsFolder` — neither reads the other's folder.

### KUBE-43

- **Where:** `sidecar/internal/agent/chat/chatdir.go`, `sidecar/internal/agent/chat/sweep.go`
- `TestAMonitorRunEndsWithItsCluster`, `TestAMonitorEndsWhenItsRowIsGone` — the run is cancelled once its cluster is not a live row.
- `TestTheMonitorsFolderIsSwept` — the folder goes.

### KUBE-44

- **Where:** `sidecar/internal/agent/chat/monitor.go`, `sidecar/internal/appdb/migrations/0001_init.sql`
- `TestAMonitorRunIsFiledUnderItsClusterWithNoChat`, `TestRunMonitorRecordsARun` — the run's row and what is recorded beneath it.

### KUBE-45

- **Where:** `sidecar/internal/agent/chat/monitor.go`, `sidecar/internal/appdb/migrations/0001_init.sql`
- `TestAClustersDeleteTakesItsMonitorRuns` — the record is cascaded with its cluster.
- `TestAMonitorRunEndsWithTheService`, `TestAStrandedMonitorRunIsFailed` — a run a crash left is failed at the next start.

### KUBE-46

- **Where:** `sidecar/internal/services/toolsettings`, `sidecar/internal/tools/box.go`, `sidecar/internal/agent/chat/turn.go`
- **Records:** [the user turns tools off](../security/2026-11-20-the-user-turns-tools-off.md)
- `TestATurnIsNotOfferedAToolTheUserTurnedOff`, `TestASubagentFollowsTheToolSettings`, `TestAMonitorRunFollowsTheToolSettings` — the offer.
- Note: the code is `WithoutNames` in `box.go` and `boxFor` in `turn.go`.

### KUBE-47

- **Where:** `sidecar/internal/tools/box.go`, `sidecar/internal/agent/chat/turn.go`
- **Records:** [the user turns tools off](../security/2026-11-20-the-user-turns-tools-off.md)
- `TestWithoutNamesDropsTheNamedTools`, `TestACallOfAToolTurnedOffIsRefusedAndStillDraws` — the refused call.
- Note: the code is `WithoutNames` in `box.go` and `boxFor` in `turn.go`.

### KUBE-48

- **Where:** `sidecar/internal/services/toolsettings`
- **Records:** [the user turns tools off](../security/2026-11-20-the-user-turns-tools-off.md)
- `TestToolSettingSetRefusesANameNotListed` — the wire writes only listed names.

### KUBE-49

- **Where:** `sidecar/internal/services/toolsettings`, `sidecar/internal/agent/chat/turn.go`
- **Records:** [the user turns tools off](../security/2026-11-20-the-user-turns-tools-off.md)
- `TestToolSettingSetRefusesToTurnOffAnAlwaysOnTool`, `TestATurnIsAlwaysOfferedTaskStop` — the wire refuses TaskStop off, and a stored row turning it off is ignored.

### KUBE-50

- **Where:** `sidecar/internal/services/toolsettings`, `sidecar/internal/agent/chat/turn.go`
- **Records:** [the user turns tools off](../security/2026-11-20-the-user-turns-tools-off.md)
- `TestARowKstackCannotUseTurnsItsToolOff`, `TestATurnIsNotOfferedAToolWhoseSettingCannotBeUsed` — a stored setting Kstack cannot use turns its tool off.

### KUBE-51

- **Where:** `sidecar/internal/run/kubeproxy`, `sidecar/internal/tools/logsread`
- **Records:** [the sandbox is the gate](../adr/2026-09-28-the-sandbox-is-the-gate-for-a-sandboxed-command.md), [Bash runs in a sandbox](../security/2026-09-28-bash-runs-in-a-sandbox.md), [the model reads logs](../security/2026-10-17-the-model-reads-logs.md), [a sandboxed command asks for each cluster write](../adr/2026-09-29-a-sandboxed-command-asks-for-each-cluster-write.md), [Secret data is a permissioned read](../adr/2026-10-05-secret-data-is-a-permissioned-read.md)
- Note: ConfigMap data, env values and logs go as they are ([Bash runs in a sandbox](../security/2026-09-28-bash-runs-in-a-sandbox.md)).
- Note: logs go through `LogsRead` as through `kubectl logs` ([the model reads logs](../security/2026-10-17-the-model-reads-logs.md)).
- Note: so does a Secret an approved write brings back, such as a Pod that mounts one and prints it to its log ([a sandboxed command asks for each cluster write](../adr/2026-09-29-a-sandboxed-command-asks-for-each-cluster-write.md)).
- Note: Secret data is the exception the user grants: per read, for the command, the chat, or always for a context and namespace, and under `Auto` unasked ([Secret data is a permissioned read](../adr/2026-10-05-secret-data-is-a-permissioned-read.md)).

### KUBE-52

- **Where:** `sidecar/internal/run/kubeproxy/redact_helm.go`
- **Records:** [helm's release Secrets pass, redacted inside](../adr/2026-09-28-helm-release-secrets-pass-redacted-inside.md), [Secret data is a permissioned read](../adr/2026-10-05-secret-data-is-a-permissioned-read.md), [TODO](../TODO.md#security)
- `TestAHelmReleaseWriteFollowsWhatTheCommandRead`, `TestARedactedReleaseIsRefusedUnderTheGrant` — the writes, under [Secret data is a permissioned read](../adr/2026-10-05-secret-data-is-a-permissioned-read.md).
- Note: an audit of what else carries secrets is in [TODO](../TODO.md#security).

### KUBE-53

- **Where:** `sidecar/internal/run/permissions`
- **Records:** [the run's door](../adr/2026-11-17-a-sandboxed-command-reaches-a-cloud-platform-through-a-proxy-on-its-runs-one-relay.md)
- `TestACloudPlatformActionAlwaysAsks` — each change asks.
