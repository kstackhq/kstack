---
title: The prompt names the action
scope: sidecar, webview
status: Planned
---

# The prompt names the action

**Needs:** step 3B, whose `permissions.Action`, `Policy.Decide`, `chat_grants` and record statuses
this step builds on. **Unblocks:** steps 5A and 5B, which draw their actions through this request.
**Shares task 1 with step 4C** (the record, the action on the request, the journal's ask lock,
and the functions that say which rule an answer adds): whichever of the two lands first does it,
and the other builds on it.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a cluster write's request shows what the API server receives: *Patch in the cluster?*, the
path, the method and media type, and the body byte for byte. The user reads a JSON merge patch
and decides. The answer is one boolean, and the next identical write asks again.

The note's *Prompt UX* section asks for three things, and this step builds them:

- **The request names the action.** Its heading is the classified action's `Summary` — *Delete
  pods/api-7f9c in team-a on dev-eks* — and for a change to an object that exists it shows
  **a diff**: what the object is now against what a dry run says it will be, as YAML. The raw
  request is one fold away.
- **Five answers**: Approve once, Allow for this command, Allow for this chat, Always allow,
  Deny. "This command" lets the rest of the command repeat the same change: a rule on the
  command's grant, in memory, gone when the command ends. "This chat" writes a `chat_grants`
  row; "Always" writes a rule into `securityconfig`, which Settings shows and removes. Under each
  allow button is the rule it adds, in the words Settings uses.
- **The record keeps the duration**, and the call's disclosure says it: `approved · this chat`.

The bash tool record's rule stands for a raw command outside the sandbox: it asks every time,
and its request offers Approve and Deny alone. An action no rule can allow offers once and deny
alone too: class 5, an action an `AskFor` rule matches, and one with no context. Nothing changes
on Windows: no command there reaches the proxy, and a call's own request is unchanged.

## What is not in this step

- **No Secret grant.** Step 5A puts a class 6 read through this request.
- **No host prompt.** Step 4C sends its action through this request and adds its heading and
  `aria-label`.
- **No folder prompt.** A folder is granted in Settings or from a denial (steps 4D and 5B), never
  asked for by a command.
- **No change to what asks.** `Decide` is step 3B's; this step changes what the user sees, and
  adds the rules a `Command` answer keeps.
- **No change for a background command.** It still never changes the cluster: its grant has no
  asker and refuses every write before `Decide`.

## Design

### 1. The record (task 1, shared with step 4C)

`approvals` in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md):

```sql
kind     TEXT NOT NULL DEFAULT 'call' CHECK (kind IN ('call', 'action')),
request  TEXT,          -- an action approval's tools.ActionRequest as JSON
status   TEXT NOT NULL DEFAULT 'pending'
         CHECK (status IN ('pending','approved','denied','abandoned','allowed','refused')),
duration TEXT CHECK (duration IN ('once','command','chat','always')),
reason   TEXT,

CHECK ((kind = 'call') = (request IS NULL)),
CHECK (status NOT IN ('abandoned', 'allowed', 'refused') OR kind = 'action'),
CHECK (duration IS NULL OR status = 'approved'),
```

`'cluster'` becomes `'action'`: a host (step 4C) and a Secret read (step 5A) are actions the
same row records. `approvals_writes_idx` becomes `approvals_actions_idx`, partial on
`kind = 'action'`. `duration` is set on `approved` alone: what the user chose. It is NULL on a
denial, an abandon, and on `allowed` and `refused`, which nobody chose. A call's own approval
records `once`.

**The action rides the request.** Today `Asker.Ask` and `Record` take a `kubeproxy.Write` alone,
and the record and the wire hold no action. This task adds it end to end:

```go
// tools: ActionRequest is one classified action held for the user, or decided
// with nobody asked. It is what an action approval's request column holds.
type ActionRequest struct {
	Action    permissions.Action `json:"action"`
	Grantable bool               `json:"grantable"` // §2: whether a rule may allow it
	Rules     GrantLines         `json:"rules"`     // the rules each allow answer adds, in words
	Write     *ClusterWrite      `json:"write,omitempty"` // the request as sent; nil for an action with none
	Diff      string             `json:"diff"`      // §3; "" for none
	DiffCut   bool               `json:"diffCut"`   // Diff stops short of the whole change
	DiffError string             `json:"diffError"` // why there is no diff, when one was looked for
}

// GrantLines is Rule.Line() of the rule a Command answer adds, and of the one
// a Chat or Always answer writes.
type GrantLines struct {
	Command string `json:"command"`
	Chat    string `json:"chat"`
}

// ClusterWrite is the landed ClusterWriteRequest's fields: Method, Path,
// Subresource, ContentType, Body, DryRun.

// ActionAsker puts an action to the user, as a request of the call that is
// running, and records one decided with nobody asked against that call.
type ActionAsker interface {
	Ask(ctx context.Context, r ActionRequest) (Answer, error)
	Record(ctx context.Context, r ActionRequest, d permissions.Decision, reason string) error
}
```

`tools.ClusterWriteRequest` becomes `ClusterWrite`, `ClusterWriteAsker` becomes `ActionAsker`,
and `Runtime.ClusterWriteAsker` becomes `Runtime.ActionAsker`. `kubeproxy.Asker` takes a
`kubeproxy.Request` of the same shape (`Action`, `Grantable`, `Rules`, `Write *Write`, `Diff`,
`DiffCut`, `DiffError`) in both methods, and `Ask` answers an `Answer`: `Approved`, and the
`Duration` the user chose (`once`, `command`, `chat` or `always`). `bash`'s `runtimeAsker`
converts both, as it converts a `Write` today. `serveWrite` builds the request from the action it
classified, so an `allowed` or `refused` write is recorded with its action too.

`chatsvc`'s `askClusterWrite` becomes `askAction` and stores the `ActionRequest` as the row's
`request`. **The journal takes one action at a time**: `askAction` and the record path take
`j.askMu` for their whole length, the wait on the user included. Today the cluster grant's write
lock is the only thing that keeps two asks off the journal at once; from step 4C a run has two
proxies that can ask at the same moment, and a Secret read (step 5A) asks beside a write. A
second ask waits for the first answer, which is also one wait on the user at a time.

On the wire, `ClusterWrite` gains the action:

```graphql
"A classified action, as the sidecar's classifier wrote it. Every string is cluster or user text: draw it through VisibleText."
type PermissionAction {
  summary: String!
  class: PermissionClass!
  context: String!
  namespace: String!
  verb: String!
  group: String!
  kind: String!
  "Whether a rule may allow it. False offers ONCE and DENY alone."
  grantable: Boolean!
  "The rule a COMMAND answer adds, in the words Settings uses."
  commandRule: String!
  "The rule a CHAT or ALWAYS answer writes, in the same words."
  chatRule: String!
}

type ClusterWrite {
  # as landed, then:
  action: PermissionAction!
  "The change as YAML, a unified diff of the object now against the dry run's answer, while the write waits; empty otherwise, and for a create or a delete."
  diff: String!
  "Whether the diff was cut short of the whole change. The request then draws the raw request open, and Approve waits on it."
  diffCut: Boolean!
  "Why there is no diff, while the write waits and one was looked for; empty otherwise."
  diffError: String!
}
```

`diff`, like `body`, is served while the write waits and is empty once it no longer does; the
record keeps it.

### 2. The decision

**Which actions take a rule.** `permissions.Grantable(p Policy, act Action) bool` is the one test:

- class 5 is never grantable: `Decide` asks for it ahead of every `Allow`;
- an action an `AskFor` rule of `p` matches is not: `AskFor` comes before `Allow` in `Decide`, so
  a rule written from the answer would never be read;
- a cluster action with no `Context` is not: an unset rule field matches anything, so its rule
  would reach every context.

`kubeproxy` computes it under the same `Policy` it decided with, and fills `Rules` from
`permissions.CommandRule(act).Line()` and `permissions.GrantRule(act).Line()`.

**The rules.** `permissions.GrantRule(act)` is the rule a `Chat` or `Always` answer writes:
`Allow` of the action's `Class`, its `Context` and `Namespace` (a cluster-scoped action has none,
so its rule matches every namespace of the context), and nothing else, each value through
`permissions.Literal` so a context named `dev*` grants that context and no other. Two exceptions:

- an action on a Namespace object carries the namespace's own name as its `Namespace`
  (`classify`), so its rule also names `Group` `core` and `Kind` `namespaces`: it allows changes
  to that namespace's object, not every write inside it;
- a host's action (step 4C) writes its `Host` and `Port` instead of a context, each literal.

`ID` comes from `appdb.NewID`. The note's example: `k8s:write context=dev-eks namespace=team-a`
is `Allow cluster writes in dev-eks / team-a`.

```graphql
"The user's answer to a request. ONCE and DENY answer this request alone; COMMAND also allows the same change for the rest of the command; CHAT and ALWAYS also write an Allow rule for the action's class and scope."
enum ApprovalDecision { Once Command Chat Always Deny }

"How long an approval holds: this request, the rest of the command, the chat, or always. Null unless the status is Approved."
enum ApprovalDuration { Once Command Chat Always }

type ToolCallApproval {
  id: ApprovalID!
  status: ApprovalStatus!
  duration: ApprovalDuration
}

extend type Mutation {
  approvalDecide(id: ApprovalID!, decision: ApprovalDecision!): Boolean!
}
```

The `approve: Boolean!` argument goes. **`service.pending[id]` holds a waiter**: the channel, the
chat's id, and the `ActionRequest` it waits for (nil for a call's own). `service.Approve(ctx, id,
decision)` reads the waiter under `turnsMu` and checks it **before** removing it, so a refused
decision leaves it there:

| Waiter | Decision | Result |
| --- | --- | --- |
| a call's own (`kind: call`), a raw command | `Command`, `Chat`, `Always` | `KSTACK_VALIDATION_ERROR`; the waiter stays |
| an action with `Grantable` false | `Command`, `Chat`, `Always` | `KSTACK_VALIDATION_ERROR`; the waiter stays |
| an action | `Command` | remove it and deliver approved with `command`; no row and no file, since the proxy keeps the rule (§2a) |
| an action | `Chat` | `addGrant(ctx, chatID, GrantRule(act))`, a `chat_grants` row through a new `stmtInsertChatGrant`; then remove it and deliver approved |
| an action | `Always` | `securityconfig`'s `AddRule(GrantRule(act))`, the write `permissionRuleAdd` uses; then remove it and deliver approved. While the store holds `rules` (step 3B) that write answers `ErrHeld`, so the answer is `KSTACK_VALIDATION_ERROR` naming the file, and the waiter stays for *Once* or *Deny* |
| any | `Once` | remove it and deliver approved with `once` |
| any | `Deny` | remove it and deliver denied |

The rule is written **before** the decision is delivered, and the session's `Policy` reads rules
live (step 3B), so the command's next write finds it. A rule write that fails is the mutation's
error, the waiter stays, and the user can answer again. Two answers to one request race on
`turnsMu`: the first removes the waiter, and the second finds none and answers false, as today.
The channel carries `decision{approved bool; duration}`; `waitDecision` reads it, and
`endApproval` writes `duration` with the status. Task 1 adds the column before task 2 adds the
answers, so until then every approval records `once`, which is what the landed boolean means.

### 2a. The rest of the command

A `Command` answer allows **the same change** for as long as the command runs. The proxy's
`Grant` lives exactly as long as one Bash call, so the rule lives on it, in memory, and nothing
writes, keeps or cleans it up.

- **The rule** is `permissions.CommandRule(act)`: `GrantRule(act)` with the action's `Verb`,
  `Group` and `Kind` as well, each through `permissions.Literal`, and `Command` set. It is
  narrower than a chat or always rule, which leave the verb and kind unset: the user saw one
  object and allows its repeats, not every change in the namespace. `Rule.Command` is never
  stored (`json:"-"`), and `Rule.Line()` ends a command rule's line with *for this command*.
- **The grant keeps it.** On an `Answer` whose `Duration` is `command`, `serveWrite` appends the
  rule to `Grant.commandRules`, still under the write lock, so the next write is decided with it.
  `Grant.policy(ctx)` answers the session's `Policy` with the grant's own rules appended after the
  session's.
- **What it lets through** is recorded `allowed`, as any rule's write is (step 3B), its reason
  *a rule allows it: Allow delete of core pods in dev-eks / web for this command*.
- **What it never lets through**: an action that is not grantable offers no `Command`; a `Deny`
  and a read-only mode come before every `Allow` in `Decide`; and a change of another verb,
  group, resource, namespace or context asks as before.

So `kubectl delete pods --all -n web` over fifty pods asks once, and *Allow for this command*
lets the other forty-nine go, each a line of the call's disclosure.

### 3. The diff

`kubeproxy/diff.go`, called from `serveWrite` **only when `Decide` answers `Prompted`**, under the
write lock, for a `PUT` or a `PATCH` (a merge, strategic, JSON or apply patch alike) of a named
object. A write that is allowed or refused is never previewed: its body reaches no webhook
unasked, and auto mode pays nothing.

0. **Only a group version that honors a dry run.** The dry run below is a write with `dryRun=All`,
   and `honorsDryRun` (`classify.go`) lists the group versions the API server serves itself. Any
   other may be an aggregated API that ignores `dryRun` and applies the write. For one of those
   there is no diff, and `DiffError` is *This API may not honor a dry run, so Kstack does not
   preview it.*
1. **Read the object**: `GET` the path without its query through the connection's `Client` at
   its `Base`, `Accept: application/json`. A 404 is an object that does not exist: the request
   shows the body as it does today and no diff, with no `DiffError`. Any other failure is
   `DiffError`, *Kstack could not read the object: <status message>*.
2. **Dry-run the write**: the same request, body and `Content-Type` as sent, with `dryRun=All`
   set on the query beside whatever it holds. The answer is the object as it will be. A failure —
   a webhook's refusal, a validation error, a conflict — is `DiffError`, *The dry run failed:
   <status message>*. Every status message goes through `safe.String`, never the body whole.
3. **Drop** `metadata.managedFields` from both, and nothing else: a write can set `status`,
   through the `status` subresource or on a kind that has none.
4. **On core `secrets`**, compare each `data` key's raw value on the two sides first, then run
   both through `redact` (`redact.go`). A key whose value differs, or that only the dry run's
   side holds, reads `[redacted: changed]` on that side. So the diff shows which keys change and
   never what they hold, and a change to a value alone is never *No change.*
5. **Encode** both with `sigs.k8s.io/yaml.Marshal`, keys sorted, and diff them line by line with
   `github.com/pmezard/go-difflib/difflib` (`UnifiedDiff`, three lines of context, `FromFile` and
   `ToFile` empty, so no header). **This is a new direct dependency**: MIT, one file, already in
   the module graph as an indirect one.
6. **Cut** past `maxDiffLines` (2,000): the first 2,000 lines, then one line, `… N more lines
   not shown`, and `DiffCut` set. A cut diff is not the whole change, so the request then draws
   the raw request as it does with no diff (§4). An empty diff — a write that changes nothing —
   is the line `No change.`

Both requests run on the write's context, bounded by `diffTimeout` (10s, a field a test
shrinks), and take a slot and the limiter as a read does. A `POST` has no object to read, so its
request shows the body; a `DELETE` shows the summary alone.

### 4. The request

`ApprovalRequest` in `src/components/widgets/chat-transcript.tsx`, for a `change`, draws in
this order:

1. **The heading**: `change.action.summary` through `VisibleText`, with *(dry run)* after it for
   a write whose `dryRun` is set, as today: a dry run on a group version step 3B does not run
   unasked still asks. The sidecar writes the summary, so the webview parses no path.
   `kubectl delete --dry-run=server` carries its dry run in a body the proxy does not read
   (`isDryRun`), so it asks as the delete it names, which errs toward asking.
2. **The diff**, when `diff` is not empty: `DiffBlock` (`diff-block.tsx`), one `<span>` per line
   through `VisibleText`, a class on a line starting `+` or `-` (`diff-add`, `diff-del`, colored
   by `--hl-addition` and `--hl-deletion`, which `markdown.css` sets on `:root`) and a muted one
   on a `@@` line. No HTML sink: the lines are React elements. It is folded by `cutText` behind
   *Show the rest*, and **Approve waits on it**. When `diffError` is set, one muted line under the
   heading: *No preview: <diffError through VisibleText>*, and the request is still approvable.
3. **The request itself**: with a diff, a closed `<details>`, *Show the request*, holding the
   path, the method and media type, and the body as the landed request draws them — its own
   fold, which Approve does not wait on, since the diff is what the user reads. With no diff
   (a create, a delete, an object not there, no preview), or with a diff `diffCut` marks, they
   are drawn open as today, the body folded with Approve waiting on it, so a change past the cut
   is never approved unseen.
4. ***Sent by*** and the command, folded, which Approve does not wait on, as today.
5. **The buttons**: **Approve once**, **Allow for this command**, **Allow for this chat**,
   **Always allow**, **Deny**. Under *Allow for this command*, `action.commandRule` in muted text;
   under the other two allow buttons, `action.chatRule`. Both are the sidecar's `Rule.Line()`,
   which Settings draws for its rules too, so a rule reads the same before and after it is
   written. An action with `grantable` false draws Approve once and Deny alone. The four allow
   buttons arm on `useHeldStill` as Approve does today; Deny never does. Each calls
   `approvalDecide(id, decision)`; the pressed state and the error line are as today.

An agent's request opens with *An agent asks:* as today. The group's `aria-label` reads *Cluster
change awaiting approval*; steps 4C and 5A add theirs. A `change` with no `write` and no class a
later step draws offers Deny alone, as a request with no drawable action does today.

### 5. The tags in the disclosure

`ClusterWriteLines` tags each settled write off `approval.status` and `approval.duration`:
`approved · once`, `approved · this command`, `approved · this chat`, `approved · always`,
`denied`, `not answered` (`Abandoned`, or `Pending` on a stranded call), and step 3B's `allowed`
and `refused`, each with its reason in a muted span. `clusterWriteTag` is the one spelling.
`waitingRequestsOf`, `isWaitingWrite`, `approvalAnchor`, the composer's *Show* and the chat
list's dot key on the approval's id and status, and change only for the new fields.

### 6. The prompt

`tools/bash/prompts/sandbox.md`: a change the user allowed for this chat, or always, runs at
once the next time a foreground command sends it; the model need not ask again for one in the
same context and namespace. A change allowed for the command runs at once for the rest of that
command, and asks again in the next one. A background command still changes nothing.

## Decisions this step asks for

1. **The diff runs a dry run the cluster's webhooks see, for a write the user may deny.**
   Step 3B's decision 2 accepted that a dry run's body reaches admission unasked. Recommended:
   it is the only way to show what a strategic patch or an apply will do, and a webhook that
   refuses the dry run refuses the write too, so its error is worth showing first. It runs only
   for a write that asks, and only on a group version that honors a dry run.
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
5. **A command rule names the verb and the resource; a chat or always rule does not.** The
   user saw one object and allows the command to repeat that change, so `delete` of `pods`
   in `web` allows the rest of a `--all`, and a `delete` of a deployment in the same command
   still asks. Recommended: the scope stays what the request showed, and the other forty-nine
   objects, which the user did not see, are changes of the same kind.
6. **The rule's words come from the sidecar.** `Rule.Line()` already spells every rule in
   Settings; the request draws the same line rather than a second spelling in the webview.
   Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The record: `kind`, `duration`, the CHECKs, the index; `ActionRequest`, `ActionAsker`, `Request` and `Answer`; the action recorded with every write; `askAction` and `askMu`; `Grantable`, `GrantRule`, `CommandRule` and `Rule.Command`, which fill the request's flag and rule lines; `PermissionAction` and `ClusterWrite.action` on the wire. **Shared with 4C**: whichever lands first does it | `appdb/migrations/0001_init.sql`, `permissions/permissions.go`, `tools/tool.go`, `kubeproxy/write.go`, `tools/bash/proxy.go`, `chatsvc/approval.go`, `chatsvc/store.go`, `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/`, their tests | — | Planned |
| 2 | The decision: `service.Approve`, the waiter, `addGrant` and its statement (unless 4C or 4D added them), the always rule | `chatsvc/approval.go`, `chatsvc/grants.go`, `chatsvc/statements.go`, their tests | 1 | Planned |
| 3 | The diff | `kubeproxy/diff.go`, `kubeproxy/write.go`, `sidecar/go.mod`, their tests | 1 | Planned |
| 4 | The grant's command rules | `kubeproxy/write.go`, `kubeproxy/kubeproxy.go`, `tools/bash/proxy.go`, their tests | 2 | Planned |
| 5 | The wire: `approvalDecide`, the durations, the diff fields; codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 2, 3 | Planned |
| 6 | The request, the buttons, the tags | `src/components/widgets/chat-transcript.tsx`, `diff-block.tsx`, `src/lib/chats.tsx`, their tests | 5 | Planned |
| 7 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 4 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1, then 2 and 3 at the same time, then 4 and 5, then 6 and 7 at the same time, then 8.

## Tests

**`permissions`**

- `TestGrantableRefusesWhatNoRuleAllows`: class 5 is not grantable; an action an `AskFor` rule
  of the policy matches is not; a cluster action with no context is not; a class 4 write nothing
  names is.
- `TestAGrantRuleKeepsTheLiteralScope`: `GrantRule` of an action in a context named `dev*`
  matches `dev*` and not `dev-eks`, and leaves the verb and kind unset; of a Namespace object's
  patch, it names `core` `namespaces` and does not match a pod write in that namespace.
- `TestACommandRuleNamesTheChange`: `CommandRule` of a pod delete in `dev*` matches another
  pod's delete there, and not a deployment's, a patch of a pod, or a pod delete in `dev-eks`;
  its line ends *for this command*, and decoding a rule from JSON never sets `Command`.

**`chatsvc`**

- `TestAnActionIsRecordedWithItsRequest`: an asked write's row holds the `ActionRequest`, and an
  `allowed` and a `refused` one hold theirs.
- `TestAsksTakeTheJournalOneAtATime`: two asks from two goroutines on one run; the second's row
  is written only after the first is answered.
- `TestOnceWritesTheDurationAndNoRule`: `Once` records `approved` with `once`, and `grantsFor`
  and the file are unchanged.
- `TestChatWritesAGrantBeforeTheDecisionLands`: `Chat` writes `GrantRule`'s row, and the turn
  reads `approved` with `chat` only after the row is there.
- `TestAlwaysWritesTheRuleIntoTheSettings`: the same rule in `securityconfig`, and `always` on
  the row.
- `TestAlwaysWaitsWhileTheRulesAreHeld`: with `rules` held, `Always` is refused, writes nothing,
  and the waiter stays, answerable with `Once`.
- `TestACallsOwnApprovalTakesOnceOrDenyAlone`: `Command`, `Chat` and `Always` on a raw
  command's request are `ErrBadRequest`, the waiter still there, and `Once` then lands.
- `TestCommandWritesNoRule`: `Command` records `approved` with `command`, and `grantsFor` and
  the file are unchanged.
- `TestAnUngrantableActionTakesOnceOrDenyAlone`: the same for a class 5 action.
- `TestAFailedRuleWriteLeavesTheRequestWaiting`, and `TestADenialRecordsNoDuration`.

**`kubeproxy`**

- `TestADiffForAPutShowsTheChange`, against a fake API server that answers the `GET` and the
  dry run: the diff holds the changed line as `-` and `+`, and the request still asks.
- `TestADiffForAPatch`, for a merge patch, a strategic patch and an apply, the dry run
  carrying the body and media type as sent, `dryRun=All` beside the query's own pairs.
- `TestOnlyAPromptedWriteIsPreviewed`: under `Auto`, a `Deny` rule and a read-only mode, the fake
  API server sees no `GET` and no dry run.
- `TestAnAPIThatMayIgnoreADryRunIsNotPreviewed`: a `PATCH` of a group version `honorsDryRun`
  does not list sends no dry run, and `DiffError` says why.
- `TestADiffDropsManagedFieldsAlone`.
- `TestADiffOfASecretMarksTheChangedKeys`: a changed value reads `[redacted]` against
  `[redacted: changed]`, an unchanged key reads `[redacted]` on both sides, and no value is in
  the diff.
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
  rules flipped by the test between them; the second is `allowed` unasked.
- `TestACommandAnswerAllowsTheRestOfTheCommand`: three pod deletes on one grant, the first
  answered `command`; the other two are `allowed` with the rule's line, and a deployment delete
  after them still asks.
- `TestACommandRuleEndsWithTheGrant`: a second grant on the same session asks for the same
  delete again.

**Webview** (`chat-transcript.test.tsx`, `diff-block.test.tsx`)

- The heading is the summary and never the method; a dry run's ends *(dry run)*.
- A diff's `+` and `-` lines carry their classes, a `@@` line is muted, every line goes
  through `VisibleText`, and a long diff folds with Approve held until *Show the rest*.
- With a diff the raw request sits under *Show the request*, closed, and Approve does not
  wait on it; without one the body is open and Approve waits on it.
- A `diffCut` diff draws the raw request open under it, and Approve waits on both folds.
- A `diffError` line, and the request still approvable.
- The five buttons, the command rule's line under *Allow for this command* and the chat rule's
  under the other two, each decision's mutation with its enum value, the four allow buttons armed
  by `useHeldStill` and Deny not; a `grantable: false` action draws Approve once and Deny alone.
- Each tag: `approved · once`, `approved · this command`, `approved · this chat`, `approved ·
  always`, `denied`, `not answered`, `allowed` and `refused` with their reasons.

## Security

What the user decides on changes shape: the classified action and a diff the API server
computed, with the raw request one fold away, in place of the raw request alone. What holds
it: the summary and the rules' words are the sidecar's, written by the classifier from the
parsed path and never from the model's text; the diff is a dry run of the exact bytes the
request holds, so it shows what the cluster will do rather than what the body says, and it runs
only for a write that asks, on an API that honors a dry run; the raw request is still drawn,
byte for byte, behind *Show the request*, and drawn open with Approve waiting on it when the
diff is cut or absent; an action no rule could allow offers no rule; an "always" rule is scoped
to the context and namespace the button names, each copied as a literal, and is on screen in
Settings, where the user removes it; a "this command" rule names the verb and the resource as
well, lives on the command's grant and ends with it; a raw command outside the sandbox keeps
the bash tool record's rule.

Residuals: a dry run's body reaches admission webhooks for a write the user may then deny
(decision 1); an `Allow` rule for a namespace covers every write there, a Pod that mounts a
Secret included, as step 3B's record says; the diff hides what `managedFields` would show,
which a write never sets; on a cluster-scoped action a chat rule covers every namespace of the
context, and the rule's line says so; *Allow for this command* approves objects the user has not
seen, how many unknown until the command ends, and the rule's line, not the object, is what it
allows; a `Chat` or `Always` rule is written before the decision reaches the turn, so a cancel
that wins the turn's select leaves the rule in place, and the user removes it in Settings.

The record, `docs/security/<date>-the-prompt-names-the-action.md`, argues this and supersedes
the request paragraph of [cluster writes ask](../../security/2026-09-29-cluster-writes-ask.md).

## When it lands

- **The security record** above, and an ADR: a prompt names the action and offers a duration;
  a diff is a dry run, only of a write that asks; the record keeps the duration.
- **`security-model.md`**: the cluster-write request row says the heading is the action, the
  diff is a dry run, the raw request is one fold away, and which fold Approve waits on, with
  the tests; the rules row gains the chat and always writes.
- **`sidecar/CLAUDE.md`**: `ActionRequest`, `ActionAsker`, `askMu`, `Grantable`, `GrantRule`,
  `CommandRule`, the diff and its two requests, `approvals.duration` and `kind: action`,
  `service.Approve`'s table and the waiter, the grant's rules.
- **Root `CLAUDE.md`**, *Chat* and the security invariants: the request's order, the five
  buttons and their arming, the rule lines under them, the tags, `DiffBlock`.
- **`docs/TODO.md`**: *Check cluster writes by hand* reads the new request. **The sequence's
  README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks.

By hand, `pnpm tauri dev` against a kind cluster, on Linux or macOS: ask for `kubectl scale
deploy x --replicas=2` and read *Patch deployments/x/scale in default on kind-kind* over a diff
whose `replicas` line changes, the patch under *Show the request*; press Allow for this chat,
ask to scale it back, and read the write go with no request and `approved · this chat` on the
first call and `allowed` on the second; ask for `kubectl delete ns test` and read a request with
Approve once and Deny alone; ask for `kubectl apply --server-side` of a Secret with one value
changed and read `[redacted: changed]` on its key. Create three pods, ask for `kubectl delete
pods --all`, press Allow for this command on the first request, and read the other two go with
no request, tagged `allowed`; ask for the same again and read a request.
