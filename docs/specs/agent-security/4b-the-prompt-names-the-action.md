---
title: The prompt names the action
scope: sidecar, webview
status: Planned
---

# The prompt names the action

**Needs:** step 3B, whose `permissions.Action`, `Decide`, `chat_grants` and record statuses this
step builds on. **Unblocks:** steps 4C, 4D, 5A and 5B, each of which draws its own action through
this request.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a cluster write's request shows what the API server receives: *Patch in the cluster?*, the
path, the method and media type, and the body byte for byte. The user reads a JSON merge patch
and decides. The answer is one boolean, and the next identical write asks again.

The note's *Prompt UX* section asks for three things, and this step builds them:

- **The request names the action.** Its heading is the classified action's `Summary` — *Delete
  pod `api-7f9c` in `team-a` on `dev-eks`* — and for a change to an object that exists it shows
  **a diff**: what the object is now against what the dry run says it will be, as YAML. The raw
  request is one fold away.
- **Four answers**: Approve once, Allow for this chat, Always allow, Deny. "This chat" writes a
  `chat_grants` row; "Always" writes a rule into `securityconfig`, which Settings shows and
  removes. Each allow button says the scope it grants.
- **The record keeps the duration**, and the call's disclosure says it: `approved · this chat`.

The bash tool record's rule stands for a raw command outside the sandbox: it asks every time,
and its request offers Approve and Deny alone. A class 5 action, and one a shipped `AskFor`
rule names, offer once and deny alone too, since no rule can allow them. Nothing changes on
Windows: no command there reaches the proxy, and a call's own request is unchanged.

## What is not in this step

- **No Secret grant.** Step 5A puts a class 6 read through this request.
- **No host or folder prompt.** Steps 4C and 4D send their actions through it and add their
  headings and `aria-label`s.
- **No change to what asks.** `Decide` is step 3B's; this step changes what the user sees.

## Design

### 1. The record

`approvals` in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md):

```sql
kind     TEXT NOT NULL DEFAULT 'call' CHECK (kind IN ('call', 'action')),
request  TEXT,          -- an action approval's tools.ActionRequest as JSON
status   TEXT NOT NULL DEFAULT 'pending'
         CHECK (status IN ('pending','approved','denied','abandoned','allowed','refused')),
duration TEXT CHECK (duration IN ('once','chat','always')),
reason   TEXT,          -- step 3B
```

`'cluster'` becomes `'action'`: a host prompt (step 4C), a folder grant (step 4D) and a Secret
read (step 5A) are actions the same row records.
`approvals_writes_idx` becomes `approvals_actions_idx`, partial on `kind = 'action'`, and the
CHECK on `abandoned` names `'action'`. `duration` is set on `approved` alone: what the user
chose. It is NULL on a denial, an abandon, and on `allowed` and `refused`, which nobody chose.

`tools.ClusterWriteRequest` becomes **`tools.ActionRequest`**, the one shape every action
prompt carries, and `tools.ClusterWriteAsker` becomes **`tools.ActionAsker`**, on the runtime as
`Runtime.ActionAsker`:

```go
// ActionRequest is one classified action held for the user.
type ActionRequest struct {
	Action    permissions.Action `json:"action"`
	Grantable bool               `json:"grantable"` // whether a rule may allow it: not class 5, and no shipped AskFor rule matches
	Write     *ClusterWrite      `json:"write,omitempty"` // the request as sent; nil for an action with no request body (step 5A)
	Diff      string             `json:"diff"`      // §3; "" for none
	DiffCut   bool               `json:"diffCut"`   // Diff stops short of the whole change
	DiffError string             `json:"diffError"` // why there is none, when the dry run failed
}

// ClusterWrite is the landed ClusterWriteRequest's fields: Method, Path, Subresource,
// ContentType, Body, DryRun.
```

`kubeproxy.Asker.Ask` takes a `kubeproxy.Request` of the same shape (`Action`, `Grantable`,
`Write *Write`, `Diff`, `DiffCut`, `DiffError`); `bash`'s `runtimeAsker` converts it as it
converts a `Write` today. `chatsvc`'s `askClusterWrite` becomes `askAction`, unchanged but for
the type. `permissions.Grantable(rules, act) bool` is the one test of the flag: class 5 is never
grantable, and neither is an action a shipped `AskFor` rule matches.

### 2. The decision on the wire

```graphql
"The user's answer to a request. ONCE and DENY answer this request alone; CHAT and ALWAYS also write an Allow rule for the action's class and scope."
enum ApprovalDecision { Once Chat Always Deny }

"How long an approval holds: this request, the chat, or always. Null unless the status is Approved."
enum ApprovalDuration { Once Chat Always }

type ToolCallApproval {
  id: ApprovalID!
  status: ApprovalStatus!
  duration: ApprovalDuration
}

type PermissionAction {
  # step 3B's summary, class, provider and scope, then:
  "Whether a rule may allow it: false for class 5 and for an action a shipped rule always asks about. The request offers ONCE and DENY alone then."
  grantable: Boolean!
}

type ClusterWrite {
  # as landed, then:
  "The change as YAML, a unified diff of the object now against the dry run's answer; empty for a create, a delete, or a dry run that failed."
  diff: String!
  "Whether the diff was cut short of the whole change. The request then draws the raw request open, and Approve waits on it."
  diffCut: Boolean!
  "Why there is no diff, when the dry run or the read before it failed; empty otherwise."
  diffError: String!
}

extend type Mutation {
  approvalDecide(id: ApprovalID!, decision: ApprovalDecision!): Boolean!
}
```

The `approve: Boolean!` argument goes. `service.Approve(ctx, id, decision)` reads the waiter
(`service.pending[id]` now holds the channel and the `ActionRequest` it waits for, or none for
a call's own) and checks before it delivers:

| Waiter | Decision | Result |
| --- | --- | --- |
| a call's own (`kind: call`) with no `Action`, a raw command | `Chat`, `Always` | `KSTACK_VALIDATION_ERROR`; the request stays |
| an action with `Grantable` false | `Chat`, `Always` | `KSTACK_VALIDATION_ERROR`; the request stays |
| an action | `Chat` | `addGrant(ctx, chatID, rule)`: a `chat_grants` row, then deliver approved |
| an action | `Always` | the rule into `securityconfig`'s `Rules`, through the write `permissionRuleAdd` uses, then deliver approved |
| any | `Once` | deliver approved |
| any | `Deny` | deliver denied |

The rule is `Allow` of the action's `Class` and `Provider`, `Scope` the fields the provider has
(`Context` and `Namespace` for Kubernetes; a cluster-scoped action has no namespace, so its rule
matches every namespace of the context), `Verb` unset, `ID` from `appdb.NewID`. `Kind` is unset
but where the provider's scope rides on it: a `net` action's `Kind` is its port (step 4C), so
its rule keeps it and a grant for `example.com:443` reaches port 443 alone. Each value the rule
copies goes through `permissions.Literal` (step 3B), so a context named `dev*` grants that
context and no other. The note's example: `k8s:write context=dev-eks namespace=team-a`. The rule
is written **before** the decision is delivered, and `Session.Rules` reads live (step 3B), so
the command's next write finds it. A rule write that fails is the mutation's error, the waiter
stays, and the user can answer again. The decision carries its duration to the turn
(`waitDecision` reads a `decision{approved bool; duration}` off the channel), and `endApproval`
writes `duration` with the status. `Once` records `once`.

### 3. The diff

`kubeproxy/diff.go`, called from `serveWrite` after `checkBody` and `classify`, under the write
lock, for a `PUT` or a `PATCH` (a merge, strategic, JSON or apply patch alike) of a named
object:

1. **Read the object**: `GET` the path without its query through the connection's `Client`
   at its `Base`, `Accept: application/json`. A 404 is an object that does not exist: the
   request shows the body as it does today and no diff. Any other failure is `DiffError`.
2. **Dry-run the write**: the same request, body and `Content-Type` as sent, with `dryRun=All`
   set on the query beside whatever it holds. Step 3B made a dry run a read, so this asks no
   one. The answer is the object as it will be. A request that is itself a dry run is sent
   once, as it is: it is its own dry run. A failure — a webhook's refusal, a validation error,
   a conflict — is `DiffError`: the `Status` message through `safe.String`, never the body
   whole.
3. **Drop** `metadata.managedFields` from both, and nothing else: a write can set `status`,
   through the `status` subresource or on a kind that has none. On core `secrets`, run both
   through `redact` (`redact.go`), so a value is `[redacted]` on both sides and the diff shows
   which keys change and never what they hold.
4. **Encode** both with `sigs.k8s.io/yaml.Marshal`, keys sorted, and diff them line by line
   with `github.com/pmezard/go-difflib/difflib` (`UnifiedDiff`, three lines of context, no
   file header). **This is a new direct dependency**, MIT, one file, already in the module
   graph through testify.
5. **Cut** past `maxDiffLines` (2,000): the first 2,000 lines, then one line, `… N more lines
   not shown`, and `DiffCut` set. A cut diff is not the whole change, so the request then draws
   the raw request as it does with no diff (§4). An empty diff — a write that changes nothing —
   is the line `No change.`

Both requests run on the write's context, bounded by `diffTimeout` (10s, a field a test
shrinks), and take a slot and the limiter as a read does. A `POST` has no object to read, so
its request shows the body; a `DELETE` shows the summary alone. `Diff`, `DiffCut` and
`DiffError` ride `Request`, `ActionRequest`, the row and the wire.

### 4. The request

`ApprovalRequest` in `src/components/widgets/chat-transcript.tsx`, for a `change`, draws in
this order:

1. **The heading**: `change.action.summary` through `VisibleText`, with *(dry run)* after it
   when `dryRun` is set. The sidecar writes the summary (step 3B), so the webview parses no
   path.
2. **The diff**, when `diff` is not empty: `DiffBlock` (`diff-block.tsx`), one `<span>` per
   line through `VisibleText`, a class on a line starting `+` or `-` (`diff-add`, `diff-del`,
   colored by `--hl-addition` and `--hl-deletion` from `markdown.css`) and a muted one on a
   `@@` line. No HTML sink: the lines are React elements. It is folded by `cutText` behind
   *Show the rest*, and **Approve waits on it**. When `diffError` is set, one muted line
   under the heading: *The dry run failed: <error through VisibleText>*, and the request is
   still approvable.
3. **The request itself**: with a diff, a closed `<details>`, *Show the request*, holding the
   path, the method and media type, and the body as the landed request draws them — its own
   fold, which Approve does not wait on, since the diff is what the user reads. With no diff
   (a create, a delete, a failed dry run, an object not there), or with a diff `diffCut` marks,
   they are drawn open as today, the body folded with Approve waiting on it, so a change past
   the cut is never approved unseen.
4. ***Sent by*** and the command, folded, which Approve does not wait on, as today.
5. **The buttons**: **Approve once**, **Allow for this chat**, **Always allow**, **Deny**. Under
   each allow button, the scope in muted text: *cluster writes in `dev-eks` / `team-a`*,
   spelled by `scopeLine(action)` in `src/lib/permissions.ts`, the one spelling, which the
   Settings rules (step 3B) use too. An action with `grantable` false draws Approve once and
   Deny alone. The three allow buttons arm on `useHeldStill` as Approve does today; Deny
   never does. Each calls `approvalDecide(id, decision)`; the pressed state and the error line
   are as today.

An agent's request opens with *An agent asks:* as today. The group's `aria-label` reads
*Cluster change awaiting approval*; steps 4C, 4D and 5A add theirs. A `change` with no `write` and
no kind a later step draws offers Deny alone, as a request with no drawable action does today.

### 5. The tags in the disclosure

`ClusterWriteLines` tags each settled write off `approval.status` and `approval.duration`:
`approved · once`, `approved · this chat`, `approved · always`, `denied`, `not answered`
(`Abandoned`, or `Pending` on a stranded call), and step 3B's `allowed` and `refused`, each with
its reason in a muted span. `clusterWriteTag` is the one spelling. `waitingRequestsOf`,
`isWaitingWrite`, `approvalAnchor`, the composer's *Show* and the chat list's dot key on the
approval's id and status, and change only for the renamed type.

### 6. The prompt

`tools/bash/prompts/sandbox.md`: a change the user allowed for this chat, or always, runs at
once the next time; the model need not ask again for one in the same context and namespace.

## Decisions this step asks for

1. **The diff runs a dry run the cluster's webhooks see, for a write the user may deny.**
   Step 3B's decision 2 accepted that a dry run's body reaches admission unasked. Recommended:
   it is the only way to show what a strategic patch or an apply will do, and a webhook that
   refuses the dry run refuses the write too, so its error is worth showing first.
2. **Approve waits on the diff, not the raw request.** Recommended, and the security record
   says so: the API server computed the diff from the very bytes the request holds, and the
   request is one fold away. A diff cut at 2,000 lines is not the whole change, so Approve then
   waits on the raw request too.
3. **A cut diff hands the review to the raw request**, rather than sending the whole diff. A
   whole diff of a large object is tens of thousands of lines on the wire and in the row, drawn
   in the request. Recommended: the raw request is the exact bytes, always whole behind its
   fold, and a change that large is rare enough to read there.
4. **The diff keeps `status`.** A write to the `status` subresource, or to a kind with none,
   changes it, and dropping it would read *No change.* over a real one. Recommended: a line a
   controller changed between the read and the dry run shows as noise, which is honest; a
   hidden change is not.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The record: `kind`, `duration`, the index; `ActionRequest` and `ActionAsker`; `Grantable` | `appdb/migrations/0001_init.sql`, `tools/tool.go`, `permissions/permissions.go`, `kubeproxy/write.go`, `tools/bash/proxy.go`, `chatsvc/approval.go`, `chatsvc/store.go`, their tests | — | Planned |
| 2 | The decision: `approvalDecide`, `service.Approve`, `addGrant`, the always rule | `chatsvc/approval.go`, `chatsvc/statements.go`, `securityconfig/`, `sidecar/graph/schema.graphqls`, `graph/`, their tests | 1 | Planned |
| 3 | The diff | `kubeproxy/diff.go`, `kubeproxy/write.go`, `sidecar/go.mod`, their tests | 1 | Planned |
| 4 | The wire and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 2, 3 | Planned |
| 5 | The request, the buttons, the tags | `src/components/widgets/chat-transcript.tsx`, `diff-block.tsx`, `src/lib/permissions.ts`, `src/lib/chats.tsx`, their tests | 4 | Planned |
| 6 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 2 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1, then 2 and 3 at the same time, then 4, then 5 and 6 at the same time, then 7.

## Tests

**`permissions`**

- `TestGrantableRefusesClassFiveAndAShippedAsk`: class 5 is never grantable; an action a
  shipped `AskFor` rule matches is not; a class 4 write nothing names is.

**`chatsvc`**

- `TestOnceWritesTheDurationAndNoRule`: `Once` records `approved` with `once`, and
  `grantsFor` and the file are unchanged.
- `TestChatWritesAGrantBeforeTheDecisionLands`: `Chat` writes an `Allow` row of the action's
  class, provider, context and namespace, verb and kind unset, and the turn reads `approved`
  with `chat` only after the row is there.
- `TestAGrantKeepsTheLiteralScope`: `Chat` on an action in a context named `dev*` writes a
  rule that matches `dev*` and not `dev-eks`; on a `net` action for `example.com:443` it writes
  `Kind` `443`, and the rule matches port 443 and not 8443.
- `TestAlwaysWritesTheRuleIntoTheSettings`: the same rule in `securityconfig`, and `always` on
  the row.
- `TestACallsOwnApprovalTakesOnceOrDenyAlone`: `Chat` and `Always` on a raw command's request
  are `ErrBadRequest`, the waiter still there, and `Once` then lands.
- `TestAnUngrantableActionTakesOnceOrDenyAlone`: the same for a class 5 action.
- `TestAFailedRuleWriteLeavesTheRequestWaiting`, and `TestADenialRecordsNoDuration`.

**`kubeproxy`**

- `TestADiffForAPutShowsTheChange`, against a fake API server that answers the `GET` and the
  dry run: the diff holds the changed line as `-` and `+`, and the request still asks.
- `TestADiffForAPatch`, for a merge patch, a strategic patch and an apply, the dry run
  carrying the body and media type as sent, `dryRun=All` beside the query's own pairs.
- `TestADiffDropsManagedFieldsAlone`, and `TestADiffOfASecretIsRedactedOnBothSides`: a
  changed value shows its key and `[redacted]` on both lines.
- `TestADiffShowsAStatusWrite`: a `PUT` to `/status`, and one to a custom resource with no
  `status` subresource, each show the changed `status` line, never *No change.*
- `TestAFailingDryRunIsReportedAndTheWriteStillAsks`: a webhook's 400 lands in `DiffError`,
  `Diff` is empty, and an approval forwards the write.
- `TestAnObjectNotThereHasNoDiff`: a 404 on the read asks with the body alone.
- `TestADiffIsCutAtTwoThousandLines`, with the closing line and `DiffCut` set, and
  `TestAWriteThatChangesNothingSaysSo`.
- `TestTheDiffsRequestsAreBounded`: a dry run that hangs answers `DiffError` at `diffTimeout`
  and the write still asks.
- `TestARuleWrittenBetweenTwoWritesAllowsTheSecond`: two writes on one grant, the session's
  `Rules` flipped by the test between them; the second is `allowed` unasked.

**Webview** (`chat-transcript.test.tsx`, `diff-block.test.tsx`, `permissions.test.ts`)

- The heading is the summary, with *(dry run)* where set, and never the method.
- A diff's `+` and `-` lines carry their classes, a `@@` line is muted, every line goes
  through `VisibleText`, and a long diff folds with Approve held until *Show the rest*.
- With a diff the raw request sits under *Show the request*, closed, and Approve does not
  wait on it; without one the body is open and Approve waits on it.
- A `diffCut` diff draws the raw request open under it, and Approve waits on both folds.
- A failed dry run's line, and the request still approvable.
- The four buttons, each allow button's scope line from `scopeLine`, each decision's mutation
  with its enum value, the three allow buttons armed by `useHeldStill` and Deny not; a
  `grantable: false` action draws Approve once and Deny alone.
- `scopeLine` spells a namespaced and a cluster-scoped Kubernetes scope.
- Each tag: `approved · once`, `approved · this chat`, `approved · always`, `denied`, `not
  answered`, `allowed` and `refused` with their reasons.

## Security

What the user decides on changes shape: the classified action and a diff the API server
computed, with the raw request one fold away, in place of the raw request alone. What holds
it: the summary and the scope are the sidecar's, written by the classifier from the parsed
path and never from the model's text; the diff is a dry run of the exact bytes the request
holds, so it shows what the cluster will do rather than what the body says; the raw request is
still drawn, byte for byte, behind *Show the request*, and drawn open with Approve waiting on
it when the diff is cut; a class 5 action and a shipped ask cannot be allowed by a rule, so
their requests offer no rule; an "always" rule is scoped to the context and namespace the
button names, each copied as a literal, a host's port included, and is on screen in Settings,
where the user removes it; a raw command outside the sandbox keeps the bash tool record's rule.

Residuals: a dry run's body reaches admission webhooks for a write the user may then deny
(decision 1); an `Allow` rule for a namespace covers every write there, a Pod that mounts a
Secret included, as step 3B's record says; the diff hides what `managedFields` would show,
which a write never sets; on a cluster-scoped action a chat rule covers every namespace of the
context, and the scope line says so.

The record, `docs/security/<date>-the-prompt-names-the-action.md`, argues this and supersedes
the request paragraph of [cluster writes ask](../../security/2026-09-29-cluster-writes-ask.md).

## When it lands

- **The security record** above, and an ADR: a prompt names the action and offers a duration;
  a diff is a dry run; the record keeps the duration.
- **`security-model.md`**: the cluster-write request row says the heading is the action, the
  diff is a dry run, the raw request is one fold away, and which fold Approve waits on, with
  the tests; the rules row gains the chat and always writes.
- **`sidecar/CLAUDE.md`**: `ActionRequest`, `ActionAsker`, `Grantable`, the diff and its two
  requests, `approvals.duration` and `kind: action`, `service.Approve`'s table.
- **Root `CLAUDE.md`**, *Chat* and the security invariants: the request's order, the four
  buttons and their arming, `scopeLine`, the tags, `DiffBlock`.
- **`docs/TODO.md`**: *Check cluster writes by hand* reads the new request. **The sequence's
  README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks.

By hand, `pnpm tauri dev` against a kind cluster, on Linux or macOS: ask for `kubectl scale
deploy x --replicas=2` and read *Patch deployment `x` in `default` on `kind-kind`* over a diff
whose `replicas` line changes, the patch under *Show the request*; press Allow for this chat,
ask to scale it back, and read the write go with no request and `approved · this chat` on the
first call and `allowed` on the second; ask for `kubectl delete ns test` and read a request
with Approve once and Deny alone; ask for `kubectl apply` of a Secret and read `[redacted]` on
both sides of its diff.
