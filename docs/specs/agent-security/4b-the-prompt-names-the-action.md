---
title: The prompt names the action
scope: sidecar, webview
status: Planned
---

# The prompt names the action

**Needs:** step 3B, whose `permissions.Action`, `chat_grants` and record statuses this step builds
on, and step 3C, whose `Authorize` answers the verdict a request is drawn from. **Unblocks:**
steps 5A and 5B, which draw their actions through this request.
**Shares the chat grants list with step 4D** (§2b): whichever of the two lands first adds it.

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
  row, which the composer's *Allowed for this chat* list shows and removes; "Always" writes a
  rule into `securityconfig`, which Settings shows and removes. Under each allow button is the
  rule it adds, in the words Settings uses.
- **The record keeps the duration**, and the call's disclosure says it: `approved · this chat`.

The bash tool record's rule stands for a raw command outside the sandbox: it asks every time,
and its request offers Approve and Deny alone. An action no grant can lift offers once and deny
alone too: one whose verdict is `Forbid` (class 5, or an `AskFor` rule), a dry run, and one
with no context. Nothing changes
on Windows: no command there reaches the proxy, and a call's own request is unchanged.

## What is not in this step

- **No Secret grant.** Step 5A puts a class 6 read through this request.
- **No network prompt.** A command that asks for network (step 4C) is a call's own request, with
  Once and Deny alone, like every other call's.
- **No folder prompt.** A folder is granted in Settings or from a denial (steps 4D and 5B), never
  asked for by a command.
- **No change to what asks.** The verdict is step 3C's `Authorize`; this step changes what the
  user sees, and adds the rules a `Command` answer keeps.
- **No change for a background command.** It still never changes the cluster: its grant has no
  asker and refuses every write before the policy is read.

## Design

### 1. The record (task 1)

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

`'cluster'` becomes `'action'`: a Secret read (step 5A) is an action the same row records. `approvals_writes_idx` becomes `approvals_actions_idx`, partial on
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

**The action is stored**, so `permissions.Action` gains JSON tags in camelCase (`class`,
`context`, `namespace`, `verb`, `group`, `kind`, `name`, `summary`), matching its siblings in the
`request` column.

**The action says whether it is a dry run.** `permissions.Action` gains `DryRun bool`
(`json:"dryRun"`), which `classify` sets from `isDryRun(r)`. A dry run on a group version
`honorsDryRun` lists is class 1 and never asks; one on any other is class 4 and asks. Without the
field a rule written from that request would be the real write's rule, so `Grantable` reads it
(§2).

**The verdict is taken once.** `serveWrite` calls `g.policy(ctx).Authorize(act)` once and reads
everything from that verdict: the decision (`v.Outcome()`), and `Grantable` (§2). Nothing else
re-reads the policy for the same write.

`chatsvc`'s `askClusterWrite` becomes `askAction`, `recordClusterWrite` becomes `recordAction`,
and both store the `ActionRequest` as the row's `request`. Today the cluster grant's write lock
is the only thing that keeps two of them off the journal at once; a Secret read (step 5A) asks
and records beside a write, from requests the proxy serves at the same moment. So `runJournal` gains two locks:

- **`journalMu` serializes the journal's writes.** It guards what the proxies change on the
  journal — the open call's actions, the row each writes, the status published — and every
  ask's and every record's journal write takes it. `askAction` takes it around writing the
  pending row and around `endApproval`, never across the wait; `recordAction` takes it for its
  whole length, which is one row write. So every ask and every record reaches the journal one at
  a time.
- **`askMu` serializes the asks.** `askAction` holds it from before its pending row until after
  `endApproval`, the wait on the user included, so one request of a run waits on the user at a
  time and a second ask waits for the first answer. `recordAction` never takes it: a write a
  rule allows is recorded at once while a request waits, and
  its tool never times out behind a prompt it has nothing to do with.

`runJournal.asking`, under `journalMu`, is set while an ask waits. `recordAction` publishes
`StatusWaitingApproval` while it is set, and `StatusStreaming` otherwise, so a record landing
under a waiting request never takes the request down.

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
  "Whether a rule may allow it. False offers Once and Deny alone."
  grantable: Boolean!
  "The rule a Command answer adds, in the words Settings uses."
  commandRule: String!
  "The rule a Chat or Always answer writes, in the same words."
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

An action with no write (`Write` nil: a Secret read from step 5A) still
rides a `ClusterWrite`: its `method`, `path`, `subresource`, `contentType` and `body` go out as
`""`, and `dryRun` as false. The request reads `action.class` to tell it from a write.

### 2. The decision

**Which actions take a rule.** A grant lifts only `Unmatched` (step 3C): a `Forbid` wins over any
`Allow`, so a rule written from the answer would never be read, and a `Refuse` is never put to
the user. So `permissions.Grantable(v Verdict, act Action) bool` is the one test:

- the verdict is `Unmatched`;
- the action is not a `DryRun`: a rule names no dry run, so one written from a dry run's request
  would allow the real write. A dry run's request offers Approve once and Deny alone;
- the action has a `Context`: an unset rule field matches anything, so a rule from an action
  with none would reach every context.

`kubeproxy` passes it the verdict it decided with (§1), and fills `Rules` from
`permissions.CommandRule(act).Line()` and `permissions.GrantRule(act).Line()`.

**The rules.** `permissions.GrantRule(act)` is the rule a `Chat` or `Always` answer writes:
`Allow` of the action's `Class`, its `Context` and `Namespace`, and nothing else, each value
through `permissions.Literal` so a context named `dev*` grants that context and no other. One
exception:

- **an action whose `Namespace` is empty, or whose `Group` is `core` and `Kind` `namespaces`**,
  also names its `Group` and `Kind`. An unset `Namespace` matches every namespace, so without
  them one node's cordon would allow every write in the context. A Namespace object carries its
  own name as its `Namespace` (`classify`), so a rule naming that namespace alone would allow
  every write inside it; naming `core` `namespaces` keeps it to the object. `Kind` goes through
  `Literal`; `Group` is copied as it is, since a rule's `Group` is matched exactly and never as
  a pattern.

`GrantRule` leaves `ID` empty, since `permissions` imports nothing of ours. The writer sets it:
`addGrant` mints one `appdb.NewID()` for a rule with none and uses it as both the `chat_grants`
row's `id` and the stored rule's `ID`, so `chatGrantRemove`'s `id` is either (§2b); the `Always`
path sets `ID` from `appdb.NewID()` as `permissionRuleAdd` does.

**A rule already held is not written twice.** Two rules are the same when they are equal once
both `ID`s are cleared (`Rule` is comparable). A `Chat` answer whose rule the chat's grants
already hold, or an `Always` answer whose rule the settings already hold, writes nothing and
delivers the approval, so pressing *Always allow* on the same change in two chats leaves one
rule. The note's example:
`k8s:write context=dev-eks namespace=team-a` is `Allow cluster writes in dev-eks / team-a`.

**The rule's words.** `Rule.Line()` draws each pattern field — `Context`, `Namespace`, `Verb`,
`Kind` — so a line reads one way:

- a value of name characters alone (no space, no `"`, no `\`) is drawn bare, as written, a `*`
  or `?` in it a glob: `dev-eks`, `dev-*`;
- a value that is a literal — every glob character in it escaped, as `Literal` writes it — and
  holds a glob character, a space, a `"` or a `\` is drawn unescaped in quotes: `"dev*"`,
  `"a / b"`;
- any other value is drawn as written, in quotes, after *matching*: `matching "a b*"`.

Inside quotes a `"` or a `\` is preceded by `\`. A field a later step adds to `Rule` (step 4D's
`Folder`) is drawn by the same three rules, a value it never matches as a pattern being a
literal. So a grant for a context named `dev*` reads
`Allow cluster writes in "dev*"`, never `dev\*` and never a bare `dev*` that reads as a glob, and
a ` / ` outside quotes is always the one between context and namespace. `Group` is drawn as
written. The line is drawn through `VisibleText` wherever it is drawn, so an invisible character
in a value is spelled.

```graphql
"The user's answer to a request. Once and Deny answer this request alone; Command also allows the same change for the rest of the command; Chat and Always also write an Allow rule for the action's class and scope."
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

The `approve: Boolean!` argument goes. **`service.pending[id]` holds a `*waiter`**: the
channel, the chat's id, the `ActionRequest` it waits for (nil for a call's own), and `done`, a
channel the turn closes when it stops waiting. `await` returns the waiter, and the turn keeps
it; `forget(w)` becomes the one way a turn stops waiting: under `turnsMu` it deletes
`pending[w.id]` if that entry is still `w`, and closes `w.done` whether or not it was. The
table says which decisions each waiter takes:

| Waiter | Decision | Result |
| --- | --- | --- |
| a call's own (`kind: call`), a raw command | `Command`, `Chat`, `Always` | `KSTACK_VALIDATION_ERROR`; the waiter stays |
| an action with `Grantable` false | `Command`, `Chat`, `Always` | `KSTACK_VALIDATION_ERROR`; the waiter stays |
| an action | `Command` | deliver approved with `command`; no row and no file, since the proxy keeps the rule (§2a) |
| an action | `Chat` | `addGrant(ctx, chatID, GrantRule(act))`, a `chat_grants` row (§2b), unless the chat holds the same rule; then deliver approved |
| an action | `Always` | `securityconfig`'s `AddRule(GrantRule(act))`, the write `permissionRuleAdd` uses, unless the settings hold the same rule; then deliver approved. While the store holds `rules` (step 3B) that write answers `ErrHeld` |
| any | `Once` | deliver approved with `once` |
| any | `Deny` | deliver denied |

**`service.Approve(ctx, id, decision)` claims the waiter first.** Under `turnsMu` it reads the
waiter, checks the decision against the table — a refusal leaves the waiter where it was — and
removes it; then it releases `turnsMu`, so no file or database write ever runs under it. It then
writes the rule, for `Chat` or `Always`, and delivers. A rule write that fails takes `turnsMu`
again and puts the waiter back only if its `done` is still open, then answers the error; the
user can answer again. A turn that stopped waiting meanwhile closed `done` under the same lock,
so a failed write never re-inserts a waiter nobody will read. Two answers to one request race on
`turnsMu`: the first claims the waiter, and the second finds none and answers false, as today.

The rule is written **before** the decision is delivered, and the session's `Policy` reads rules
live (step 3B), so the command's next write finds it. A turn cancelled between the claim and the
delivery leaves the rule written; the user removes it in the chat grants list or in Settings.
`approvalDecide` maps `ErrHeld` to `KSTACK_VALIDATION_ERROR` naming the file, in
`sidecar/graph/util.go` beside `chatErr`, as `permissionsAfter` does today; `ErrBadRequest` is
already a validation error there. The request draws a `KSTACK_VALIDATION_ERROR` answering
`Always` — the only one it can get, since it offers `Always` only on a grantable action — as its
own error line, in place of today's *The decision did not reach the sidecar. Try again.*: *Kstack cannot
add a rule while security.json holds rules it cannot read. Fix them in Settings, or approve
once.* Approve once still works, since the waiter stays.

The channel carries `decision{approved bool; duration}`; `waitDecision` reads it, and
`endApproval` writes `duration` with the status. Task 1 adds the column before task 2 adds the
answers, so until then every approval records `once`, which is what the landed boolean means.

### 2a. The rest of the command

A `Command` answer allows **the same change** for as long as the command runs. The proxy's
`Grant` lives exactly as long as one Bash call, so the rule lives on it, in memory, and nothing
writes, keeps or cleans it up.

- **The rule** is `permissions.CommandRule(act)`: `GrantRule(act)` with the action's `Verb`,
  `Group` and `Kind` as well — `Verb` and `Kind` through `permissions.Literal`, `Group` as it is,
  since it is matched exactly — and `Command` set. It is
  narrower than a chat or always rule, which leave the verb unset: the user saw one object and
  allows its repeats, not every change in the namespace. `Rule.Command` is never stored
  (`json:"-"`), and `Rule.Line()` ends a command rule's line with *for this command*.
- **The grant keeps it.** On an `Answer` whose `Duration` is `command`, `serveWrite` appends the
  rule to `Grant.commandRules`, still under the write lock, so the next write is decided with it.
  `Grant.policy(ctx)` answers the session's `Policy` with the grant's own rules joined to the
  session's; the verdict does not depend on their order (step 3C).
- **What it lets through** is a write whose verdict it turns from `Unmatched` to `Permit`,
  recorded `allowed` as any rule's write is (step 3B), its reason *a rule allows it: Allow delete
  of core pods in dev-eks / web for this command*.
- **What it never lets through**: an `Allow` rule lifts only `Unmatched`, so a write whose verdict
  is `Forbid` (class 5, an `AskFor` rule) still asks and one whose verdict is `Refuse` (a `Deny`
  rule, a read-only mode) is still refused; an action that is not grantable offers no `Command`
  in the first place; and a change of another verb, group, resource, namespace or context
  matches no command rule and asks as before.

So `kubectl delete pods --all -n web` over fifty pods asks once, and *Allow for this command*
lets the other forty-nine go, each a line of the call's disclosure.

### 2b. The chat's grants (shared with step 4D)

A `Chat` answer writes a rule the user must be able to see and take back. Settings lists the
always rules alone, so a chat's rules get a list of their own. Whichever of steps 4B and 4D
lands first adds it, and the other adds its rows to it.

- **`chatsvc`**: `addGrant(ctx, chatID, rule)` writes a `chat_grants` row through a new
  `stmtUpsertChatGrant`, refusing a chat that is gone with `ErrChatGone`. A rule with no `ID` is
  inserted under a fresh one (§2). A rule whose `ID` the chat holds replaces that row's rule, so
  an id outlives a change to its rule (step 4D changes a folder's mode this way); an `ID` the chat
  does not hold is `ErrGrantGone`; `removeGrant(ctx,
  chatID, id)` deletes one through `stmtDeleteChatGrant`, and answers a new `ErrGrantGone`,
  mapped beside `ErrChatGone` in `chatRefusals`, for an id the chat does not hold. `grantsFor` reads both live, so a removed rule is gone from the command's next
  decision.
- **The wire**:

  ```graphql
  extend type Query {
    "The chat's own rules, each as Settings spells a rule. Empty for a chat with none."
    chatGrants(chatID: ChatID!): [PermissionRule!]!
  }

  extend type Mutation {
    "Removes one of the chat's rules by id. Refused KSTACK_RECORD_NOT_FOUND for a chat or an id that is gone."
    chatGrantRemove(chatID: ChatID!, id: String!): [PermissionRule!]!
  }
  ```

- **The webview**: `useChatGrants(chatID)` in `src/lib/chat-grants.tsx` is the one reader of the
  query and the mutation, and asks the query again after the mutation, after an approval this
  window decides with `Chat`, and when the window takes focus. `ChatGrants`
  (`src/components/widgets/chat-grants.tsx`) is the composer's *Allowed for this chat* list,
  mounted beside the sandbox switch for an open chat while it holds any rule: a trigger with
  the count, opening one row per rule, its `line` through `VisibleText` and a Remove button,
  disabled in flight, with a refusal's reason under the list. Step 4D adds its folder rows to
  the same list, each with a folder's refused reason.

### 3. The diff

`kubeproxy/diff.go`, called from `serveWrite` **only when the verdict's outcome is `Prompted`**,
under the write lock, for a `PUT` or a `PATCH` (a merge, strategic, JSON or apply patch alike) of
a named object. A write that is allowed or refused is never previewed: its body reaches no webhook
unasked, and auto mode pays nothing.

0. **Only a group version that honors a dry run.** The dry run below is a write with `dryRun=All`,
   and `honorsDryRun` (`classify.go`) lists the group versions the API server serves itself. Any
   other may be an aggregated API that ignores `dryRun` and applies the write. For one of those
   there is no diff, and `DiffError` is *This API may not honor a dry run, so Kstack does not
   preview it.*
1. **Read the object**: `GET` the path without its query through `g.up.Endpoint(ctx)`'s
   `Client` at its `Base`, `Accept: application/json`, as `forward` reaches the cluster: the
   request is cut when the endpoint's `Done` closes or the grant's context ends. A 404 is an object that does not exist: the request
   shows the body as it does today and no diff, with no `DiffError`. Any other failure is
   `DiffError`, *Kstack could not read the object: <status message>*.
2. **Dry-run the write**: the same request, body and `Content-Type` as sent, with `dryRun=All`
   set on the query beside whatever it holds, through the same endpoint. The answer is the
   object as it would be now. A failure —
   a webhook's refusal, a validation error, a conflict — is `DiffError`, *The dry run failed:
   <status message>*. Every status message goes through `safe.String`, never the body whole.
3. **Drop** `metadata.managedFields`, `metadata.resourceVersion` and `metadata.generation` from
   both: the server assigns them on every write, so they would make every diff noise. Nothing
   else goes: a write can set `status`, through the `status` subresource or on a kind that has
   none.
4. **Redact.** Both sides are decoded with `UseNumber`.
   - **On core `secrets`**, each `data` and `stringData` key's raw value is compared on the two
     sides first, then both go through `redact` (`redact.go`): the values, a helm release inside,
     and the last-applied annotation. A key whose value differs, or that only the dry run's side
     holds, reads `[redacted: changed]` on that side. So the diff shows which keys change and
     never what they hold, and a change to a value alone is never *No change.* An object `redact`
     cannot redact (`errUnredactable`) gives no diff, its `DiffError` *Kstack could not hide this
     object's secret values, so it does not preview it.*
   - **On every other kind**, both go through `redactLastApplied`, a new function in
     `redact.go` that blanks the `last-applied-configuration` annotation alone. Its raw value is
     compared on the two sides first, as a Secret's keys are: when it differs, or only the dry
     run's side holds it, that side reads `[redacted: changed]`, so a change to it alone is never
     *No change.* `redact` is never called there: it blanks the `data` of any map with
     `metadata`, which the rewriter makes safe by running it on `secrets` paths alone, and a
     ConfigMap's change would read *No change.*
5. **Encode** both with `sigs.k8s.io/yaml.Marshal`, keys sorted, and diff them line by line with
   `github.com/pmezard/go-difflib/difflib` (`UnifiedDiff`, three lines of context, `FromFile` and
   `ToFile` empty, so no header). **It becomes a direct requirement in `sidecar/go.mod`**: MIT,
   one file. It has lines in `go.sum` already and none in `go.mod`.
6. **Cut** past `maxDiffLines` (2,000): the first 2,000 lines, then one line, `… N more lines
   not shown`, and `DiffCut` set. A cut diff is not the whole change, so the request then draws
   the raw request as it does with no diff (§4). An empty diff — a write that changes nothing —
   is the line `No change.`

Both requests run on the write's context, bounded by `diffTimeout` (10s, a field a test
shrinks), and take a slot and the limiter as a read does. A slot is taken with `TryAcquire`, as
`forward` takes one: none free is `DiffError`, *Too many requests are open at once to preview
this change.*, and the write still asks. Each answer is read through `io.LimitReader` to
`maxDiffObject` (3 MiB, the API server's own request limit, which bounds an object it stores);
one past it is `DiffError`, *The object is too large to preview.* A `POST` has no object to read,
so its request shows the body; a `DELETE` shows the summary alone. Neither sends a `GET` or a dry
run.

**The diff is a preview, not a lock.** The approved write is forwarded as sent, against the
object as it is then. If the object changed between the dry run and the forward — a controller
updated it, the user waited — a patch applies to the newer object, and what lands can differ
from the diff. A `PUT` carrying a `resourceVersion` fails with a conflict instead.

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
   by `--hl-addition` and `--hl-deletion`, which `markdown.css` sets on `:root, .light` and on
   `.dark`) and a muted one
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
   written. An action with `grantable` false — a dry run among them — draws Approve once and
   Deny alone. Each rule line is drawn through `VisibleText`. The four allow
   buttons arm on `useHeldStill` as Approve does today; Deny never does. Each calls
   `approvalDecide(id, decision)`; the pressed state and the error line are as today.

Every other request — a command (one asking for network, step 4C, included), a read, a write, an edit, a fetch, a memory for every cluster —
keeps its Approve and Deny, which now send `Once` and `Deny`.

An agent's request opens with *An agent asks:* as today. The group's `aria-label` reads *Cluster
change awaiting approval*; step 5A adds its own. A `change` with no `write` and no class a
later step draws offers Deny alone, as a request with no drawable action does today.

### 5. The tags in the disclosure

`ClusterWriteLines` tags each settled write off `approval.status` and `approval.duration`:
`approved · once`, `approved · this command`, `approved · this chat`, `approved · always`,
`denied`, `not answered` (`Abandoned`, or `Pending` on a stranded call), and step 3B's `allowed`
and `refused`, each with its reason in a muted span. `clusterWriteTag` is the one spelling.
`waitingRequestsOf` and `isWaitingWrite` (`src/lib/chats.tsx`), `approvalAnchor`
(`src/lib/approval-anchor.ts`), the composer's *Show* and the chat list's dot key on the
approval's id and status, and change only for the new fields.

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
4. **The diff drops what the server assigns and keeps `status`.** `managedFields`,
   `resourceVersion` and `generation` change on every write and say nothing about it; a client
   that sets `managedFields` itself is read in the raw request (Security). A write to
   the `status` subresource, or to a kind with none, changes `status`, and dropping it would read
   *No change.* over a real one. Recommended: a line a controller changed between the read and
   the dry run shows as noise, which is honest; a hidden change is not.
5. **A command rule names the verb and the resource; a namespaced chat or always rule does
   not.** The user saw one object and allows the command to repeat that change, so `delete` of
   `pods` in `web` allows the rest of a `--all`, and a `delete` of a deployment in the same
   command still asks. A chat or always rule allows the namespace's writes, as the button's line
   says. Recommended: the scope stays what the request showed, and the other forty-nine
   objects, which the user did not see, are changes of the same kind.
6. **A cluster-scoped grant names the resource.** A rule with no namespace matches every
   namespace, so a grant from one cluster-scoped write that named only the context would allow
   every write in it. Recommended: the rule names the group and the resource, so approving a
   node's cordon for the chat allows node writes in that context and nothing else.
7. **The rule's words come from the sidecar.** `Rule.Line()` already spells every rule in
   Settings; the request draws the same line rather than a second spelling in the webview.
   Recommended.
8. **A chat's rules are listed beside the composer.** Settings shows the always rules; a chat's
   rules belong to the chat, so they are seen and removed where the chat is. Recommended.
9. **A dry run takes no rule.** A dry run that asks is approved once or denied. Recommended: a
   rule has no field for a dry run, and adding one would leave every rule the user writes in
   Settings silent on it; a dry run is rare enough to ask each time.
10. **A record never waits on a request.** Asks take `askMu`, so one request of a run waits at a
    time; journal writes take `journalMu`, which no wait holds. Recommended: a write a rule
    allows lands while a request is up, rather than stalling its
    tool behind a question about something else.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The record: `kind`, `duration`, the CHECKs, the index; `ActionRequest`, `ActionAsker`, `Request` and `Answer`; the action's JSON tags and `DryRun`, and the action recorded with every write; the verdict taken once in `serveWrite`; `askAction`, `recordAction`, `journalMu`, `askMu` and `asking`; `Grantable`, `GrantRule`, `CommandRule`, `Rule.Command` and `Rule.Line()`'s drawing, which fill the request's flag and rule lines; `PermissionAction` and `ClusterWrite.action` on the wire | `appdb/migrations/0001_init.sql`, `permissions/permissions.go`, `tools/tool.go`, `kubeproxy/classify.go`, `kubeproxy/write.go`, `tools/bash/proxy.go`, `chatsvc/approval.go`, `chatsvc/turn.go`, `chatsvc/store.go`, `sidecar/graph/schema.graphqls`, `sidecar/graph/`, generated code, `src/gql/`, their tests | — | Planned |
| 2 | The decision: `service.Approve`, the waiter and its `done`, the waiter claimed first, the always rule, a rule already held written once, the `ErrHeld` mapping; until task 6 the resolver maps `approve` to `Once` or `Deny`, so the build holds | `chatsvc/approval.go`, `chatsvc/service.go`, `sidecar/graph/util.go`, `sidecar/graph/schema.resolvers.go`, their tests | 1, 5 | Planned |
| 3 | The diff | `kubeproxy/diff.go`, `kubeproxy/redact.go`, `kubeproxy/write.go`, `sidecar/go.mod`, their tests | 1 | Planned |
| 4 | The grant's command rules | `kubeproxy/write.go`, `kubeproxy/kubeproxy.go`, `tools/bash/proxy.go`, their tests | 2 | Planned |
| 5 | The chat's grants (§2b): `addGrant`, `removeGrant`, their statements, `ErrGrantGone`, `chatGrants` and `chatGrantRemove`, `useChatGrants` and `ChatGrants`. **Shared with 4D**: whichever lands first does it | `chatsvc/grants.go`, `chatsvc/statements.go`, `chatsvc/service.go`, `sidecar/graph/schema.graphqls`, `sidecar/graph/`, generated code, `src/gql/`, `src/lib/chat-grants.tsx`, `src/components/widgets/chat-grants.tsx`, `src/components/widgets/chat-composer.tsx`, their tests | 1 | Planned |
| 6 | The wire: `approvalDecide`, the durations, the diff fields; codegen | `sidecar/graph/schema.graphqls`, `sidecar/graph/`, generated code, `src/gql/`, their tests | 2, 3 | Planned |
| 7 | The request, the buttons, the tags; every other request sends `Once` | `src/components/widgets/chat-transcript.tsx`, `src/components/widgets/diff-block.tsx`, `src/lib/chats.tsx`, their tests | 6 | Planned |
| 8 | The prompt | `tools/bash/prompts/sandbox.md`, `tools/bash/bash_test.go` | 4 | Planned |
| 9 | Docs, per *When it lands* | see there | 1–8 | Planned |

**Order:** 1, then 3 and 5 at the same time, then 2, then 4 and 6 at the same time, then 7 and 8
at the same time, then 9.

## Tests

**`permissions`**

- `TestGrantableIsUnmatched`: only an `Unmatched` verdict is grantable — not a `Forbid` (class
  5, an `AskFor` rule), a `Refuse` (a `Deny` rule, read-only) or a `Permit`; a cluster action
  with no context is not, and a class 4 write nothing names is.
- `TestADryRunIsNotGrantable`: an `Unmatched` class 4 action with `DryRun` set is not
  grantable, and the same action without it is; `classify` sets `DryRun` on a `PATCH` whose
  `dryRun` is `All` on a group version `honorsDryRun` does not list, and its class stays 4.
- `TestAGrantRuleKeepsTheLiteralScope`: `GrantRule` of an action in a context named `dev*`
  matches `dev*` and not `dev-eks`, and leaves the verb and kind unset; of a Namespace object's
  patch, it names `core` `namespaces`, does not match a pod write in that namespace, and its line
  reads `Allow writes of core namespaces in dev-eks / team-a`.
- `TestAClusterScopedGrantNamesTheResource`: `GrantRule` of a node patch names `core` `nodes`,
  matches another node's patch in that context, and matches neither a pod write in any namespace
  nor a ClusterRole write.
- `TestALiteralFieldReadsQuoted`: a rule whose context is `Literal("dev*")` reads `"dev*"` in its
  line, and a glob `dev-*` reads bare.
- `TestALineReadsOneWay`: a rule for the context `Literal("a / b")` reads `in "a / b"`, and one
  for context `a`, namespace `b` reads `in a / b`; a glob `a b*` reads `matching "a b*"`; a `"`
  in a quoted value reads `\"`; no two of these rules share a line.
- `TestAnActionRoundTripsAsJSON`: the stored keys are camelCase.
- `TestACommandRuleNamesTheChange`: `CommandRule` of a pod delete in `dev*` matches another
  pod's delete there, and not a deployment's, a patch of a pod, or a pod delete in `dev-eks`;
  its line ends *for this command*, and decoding a rule from JSON never sets `Command`.

**`chatsvc`**

- `TestAnActionIsRecordedWithItsRequest`: an asked write's row holds the `ActionRequest`, and an
  `allowed` and a `refused` one hold theirs.
- `TestTheApprovalChecksHold`: the schema refuses a `call` row with a `request`, an `action`
  row without one, an `allowed` `call` row, a `duration` on a denial, and a `kind` or `duration`
  outside its list.
- `TestAsksTakeTheJournalOneAtATime`: two asks from two goroutines on one run; the second's row
  is written only after the first is answered.
- `TestARecordNeverWaitsBehindAnAsk`: while an ask waits on the user, a record from another
  goroutine on the same run writes its row and returns at once, and the run still publishes
  `StatusWaitingApproval`.
- `TestOnceWritesTheDurationAndNoRule`: `Once` records `approved` with `once`, and `grantsFor`
  and the file are unchanged.
- `TestChatWritesAGrantBeforeTheDecisionLands`: `Chat` writes `GrantRule`'s row, and the turn
  reads `approved` with `chat` only after the row is there.
- `TestAlwaysWritesTheRuleIntoTheSettings`: the same rule in `securityconfig`, and `always` on
  the row.
- `TestARuleAlreadyHeldIsWrittenOnce`: `Chat` twice on the same action in one chat leaves one
  `chat_grants` row, and `Always` twice leaves one rule in the settings; each answer is
  delivered approved.
- `TestAlwaysWaitsWhileTheRulesAreHeld`: with `rules` held, `Always` is refused with
  `KSTACK_VALIDATION_ERROR` naming the file, writes nothing, and the waiter stays, answerable
  with `Once`.
- `TestApproveClaimsTheWaiterBeforeWriting`: two concurrent `Chat` and `Always` answers to one
  request write one rule between them, and the other answers false; `turnsMu` is not held
  across the rule's write (a store that blocks its write does not block another request's
  `Approve`).
- `TestACallsOwnApprovalTakesOnceOrDenyAlone`: `Command`, `Chat` and `Always` on a raw
  command's request are `ErrBadRequest`, the waiter still there, and `Once` then lands.
- `TestCommandWritesNoRule`: `Command` records `approved` with `command`, and `grantsFor` and
  the file are unchanged.
- `TestAnUngrantableActionTakesOnceOrDenyAlone`: the same for a class 5 action.
- `TestAFailedRuleWriteLeavesTheRequestWaiting`, and `TestADenialRecordsNoDuration`.
- `TestAFailedRuleWriteAfterTheTurnStoppedLeavesNoWaiter`: a store blocks `Chat`'s row write;
  the turn is cancelled meanwhile and closes `done`; the write then fails, `pending` holds no
  waiter for the id, and a second `Approve` answers false.
- `TestAChatsGrantsAreListedAndRemoved`: `addGrant` then `grantsFor` holds the rule, with the
  row's id as its `ID`; `addGrant` of a changed rule under that `ID` replaces it and keeps the
  id, and under an `ID` the chat does not hold is `ErrGrantGone`; `removeGrant` takes it out,
  the command's next decision no longer finds it, and an unknown id is `ErrGrantGone` and a deleted chat `ErrChatGone`.

**`graph`**

- `TestChatGrantsListAndRemove`: `chatGrants` lists a chat's rule by its line, and
  `chatGrantRemove` answers the list without it; an unknown id and a deleted chat are each
  `KSTACK_RECORD_NOT_FOUND`.
- `TestApprovalDecideMapsHeldToValidation`: `Always` while the rules are held is
  `KSTACK_VALIDATION_ERROR` naming `security.json`.

**`kubeproxy`**

- `TestADiffForAPutShowsTheChange`, against a fake API server that answers the `GET` and the
  dry run: the diff holds the changed line as `-` and `+`, and the request still asks.
- `TestADiffForAPatch`, for a merge patch, a strategic patch and an apply, the dry run
  carrying the body and media type as sent, `dryRun=All` beside the query's own pairs.
- `TestOnlyAPromptedWriteIsPreviewed`: under `Auto`, a `Deny` rule and a read-only mode, the fake
  API server sees no `GET` and no dry run.
- `TestAnAPIThatMayIgnoreADryRunIsNotPreviewed`: a `PATCH` of a group version `honorsDryRun`
  does not list sends no dry run, and `DiffError` says why.
- `TestADiffDropsWhatTheServerAssigns`: `managedFields`, `resourceVersion` and `generation`
  differ on the two sides and no line shows them; every other field is kept.
- `TestADiffUsesTheEndpoint`: a closed `Done` cuts the read and the dry run, and the diff answers
  `DiffError`.
- `TestADiffOfASecretMarksTheChangedKeys`: a changed value reads `[redacted]` against
  `[redacted: changed]`, an unchanged key reads `[redacted]` on both sides, a `stringData` key
  the same, and no value is in the diff.
- `TestADiffHidesTheLastApplied`: a ConfigMap whose `last-applied-configuration` annotation
  holds a Secret's manifest diffs with the annotation redacted; one whose annotation alone
  changes reads `[redacted]` against `[redacted: changed]`, never *No change.*
- `TestADiffShowsAStatusWrite`: a `PUT` to `/status`, and one to a custom resource with no
  `status` subresource, each show the changed `status` line, never *No change.*
- `TestAFailingDryRunIsReportedAndTheWriteStillAsks`: a webhook's 400 lands in `DiffError`,
  `Diff` is empty, and an approval forwards the write.
- `TestAnObjectNotThereHasNoDiff`: a 404 on the read asks with the body alone.
- `TestADiffIsCutAtTwoThousandLines`, with the closing line and `DiffCut` set, and
  `TestAWriteThatChangesNothingSaysSo`.
- `TestTheDiffsRequestsAreBounded`: a dry run that hangs answers `DiffError` at `diffTimeout`,
  an answer past `maxDiffObject` answers `DiffError`, and with every slot taken the diff answers
  `DiffError` with no request sent; each time the write still asks.
- `TestAPostOrADeleteHasNoDiff`: a `POST` and a `DELETE` that ask send no `GET` and no dry run,
  and carry no `Diff` and no `DiffError`.
- `TestADryRunAsksWithNoGrant`: a `PATCH` with `dryRun=All` on a group version `honorsDryRun`
  does not list asks with `Grantable` false, and the fake API server sees no `GET` and no dry
  run of the diff's own.
- `TestARuleWrittenBetweenTwoWritesAllowsTheSecond`: two writes on one grant, the session's
  rules flipped by the test between them; the second is `allowed` unasked.
- `TestACommandAnswerAllowsTheRestOfTheCommand`: three pod deletes on one grant, the first
  answered `command`; the other two are `allowed` with the rule's line, and a deployment delete
  after them still asks.
- `TestACommandRuleEndsWithTheGrant`: a second grant on the same session asks for the same
  delete again.

**`tools/bash`**

- `TestThePromptSaysWhatAnAllowKeeps`: the sandbox prompt says a change allowed for the chat or
  always runs at once next time, one allowed for the command runs for the rest of it and asks
  again in the next, and a background command changes nothing.

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
- A command's, a read's, a write's, an edit's, a fetch's and a memory-for-every-cluster's
  Approve send `Once`.
- A `KSTACK_VALIDATION_ERROR` answering *Always allow* draws the `security.json` line, and
  Approve once then sends `Once`.
- A rule line holding a `"` and an invisible character is drawn through `VisibleText`.
- Each tag: `approved · once`, `approved · this command`, `approved · this chat`, `approved ·
  always`, `denied`, `not answered`, `allowed` and `refused` with their reasons.
- `chat-grants.test.tsx`: no trigger for a chat with no rules; the count; one row per rule
  through `VisibleText`; Remove sends `chatGrantRemove`, is disabled in flight, and a refusal's
  reason shows; the list asks again after a `Chat` answer and when the window takes focus.

## Security

What the user decides on changes shape: the classified action and a diff the API server
computed, with the raw request one fold away, in place of the raw request alone. What holds
it: the summary and the rules' words are the sidecar's, each line reading one way whatever a
context or namespace is named, written by the classifier from the
parsed path and never from the model's text; the diff is a dry run of the exact bytes the
request holds, so it shows what the cluster will do rather than what the body says, and it runs
only for a write that asks, on an API that honors a dry run; the raw request is still drawn,
byte for byte, behind *Show the request*, and drawn open with Approve waiting on it when the
diff is cut or absent; an action whose verdict a grant cannot lift offers no rule, so no answer
writes a rule past a forbid; a dry run offers no rule either, since a rule names no dry run and
one written from it would allow the real write (`TestADryRunIsNotGrantable`); a chat or always rule is scoped to the context and namespace the
button names, and for a cluster-scoped action to its resource too, each copied as a literal; a
chat rule is on screen in the composer's list and an always rule in Settings, each removable
there; a "this command" rule names the verb and the resource as well, lives on the command's
grant and ends with it; a raw command outside the sandbox keeps the bash tool record's rule.

Residuals: a dry run's body reaches admission webhooks for a write the user may then deny
(decision 1); an `Allow` rule for a namespace covers every write there, a Pod that mounts a
Secret included, as step 3B's record says, and a privileged Pod — host path, host network,
`privileged: true` — that reaches the node, which only the cluster's own admission stops; it
covers the Namespace object too, so a chat or always rule for `team-a` allows relabelling
`team-a`, removing its `pod-security.kubernetes.io/enforce` label included; the diff drops
`managedFields` on both sides, and a client can set them (an update carrying them replaces the
server's), so a change to them shows no line and is read only in the raw request behind *Show
the request*; a cluster-scoped chat rule covers every object of its resource in the
context, and the rule's line says so; *Allow for this command* approves objects the user has not
seen, how many unknown until the command ends, and the rule's line, not the object, is what it
allows; the diff is a preview, not a lock: an object that changes between the dry run and the
forward takes the patch as it is then, so what lands can differ from what was shown (§3); a
`Chat` or `Always` rule is written before the decision reaches the turn, so a cancel that wins
the turn's select leaves the rule in place, and the user removes it in the chat's list or in
Settings.

The record, `docs/security/<date>-the-prompt-names-the-action.md`, argues this. It supersedes
the request paragraph of [cluster writes ask](../../security/2026-09-29-cluster-writes-ask.md),
and narrows the Consent section of [the bash tool](../../security/2026-09-18-bash-tool.md),
whose "trust this chat" is a new record: this is that record, for a classified action and never
for a raw command.

## When it lands

- **The security record** above, and an ADR: a prompt names the action and offers a duration;
  a diff is a dry run, only of a write that asks; the record keeps the duration.
- **`security-model.md`**: the cluster-write request row says the heading is the action, the
  diff is a dry run, the raw request is one fold away, and which fold Approve waits on, with
  the tests; the rules row gains the chat and always writes, the cluster-scoped grant naming its
  resource, and the chat's list.
- **`sidecar/CLAUDE.md`**: `ActionRequest`, `ActionAsker`, `askMu` and `journalMu`,
  `Action.DryRun`, `Grantable`, `GrantRule`,
  `CommandRule`, `Rule.Line()`'s quoting, the diff and its two requests, `approvals.duration` and
  `kind: action`, `service.Approve`'s table, the waiter and its `done`, the waiter claimed first, the grant's rules, `addGrant`,
  `removeGrant` and the chat's grants on the wire.
- **Root `CLAUDE.md`**, *Chat* and the security invariants: the request's order, the five
  buttons and their arming, the rule lines under them, the tags, `DiffBlock`; the "no always
  allow" invariant says the request offers a rule for a classified action alone, never for a raw
  command; `ChatGrants` and `useChatGrants`.
- **`docs/TODO.md`**: *Check cluster writes by hand* reads the new request. **The sequence's
  README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks.

By hand, `pnpm tauri dev` against a kind cluster, on Linux or macOS: ask for `kubectl scale
deploy x --replicas=2` and read *Patch deployments/x/scale in default on kind-kind* over a diff
whose `replicas` line changes, the patch under *Show the request*; press Allow for this chat,
ask to scale it back, and read the write go with no request and `approved · this chat` on the
first call and `allowed` on the second; read the rule under the composer's *Allowed for this
chat*, remove it, and read the next scale ask again; ask for `kubectl delete ns test` and read a request with
Approve once and Deny alone; ask for `kubectl apply --server-side` of a Secret with one value
changed and read `[redacted: changed]` on its key. Create three pods, ask for `kubectl delete
pods --all`, press Allow for this command on the first request, and read the other two go with
no request, tagged `allowed`; ask for the same again and read a request.
