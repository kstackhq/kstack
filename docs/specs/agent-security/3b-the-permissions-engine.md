---
title: The permissions engine
scope: sidecar, webview
status: Planned
---

# The permissions engine

**Needs:** step 2C, whose session carries the mode, and step 1C, whose settings file keeps the
rules. **Unblocks:** steps 4B, 4C, 4D, 5A, 5C, 6B, 6C, 6D and 6E.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the cluster proxy has one rule for a write: put it to the user, or refuse it when nobody
can be asked. The note wants every upstream action to fall into one of six **classes**, and the
user's **approval mode** and **rules** to decide, per class and scope, whether the action runs,
asks or is refused. The model is never the enforcement point; the proxy is.

After this step:

- **`permissions`** is one package that knows the classes, the modes, the rules, the shipped
  deny rules, and `Decide`.
- **The cluster proxy classifies every request** (class 1, 4, 5 or 6) and asks `Decide` before
  each write. `Allow` forwards it unasked and records it; `Prompt` puts it to the user as
  today; `Deny` refuses it with a `Status` that names the mode or the rule.
- **The approval mode is per context**, kept in `sandboxconfig`, with `prod*` read-only unless
  the user says otherwise, and Ask everywhere else.
- **Rules last for a chat or always.** A chat's are rows in `app.db` that go with the chat; the
  always rules live in `sandboxconfig`. Step 4B lets a prompt write either; this step lets
  Settings write the always rules.
- **A Settings section** shows the modes and the rules, the shipped ones read-only.

The request the user sees is unchanged in this step; step 4B redraws it around the action. Class
3 (a new host) arrives with step 4C, class 2 (a granted folder) with step 4D, class 6's grant
with step 5A, and the AWS and GitHub classifiers with steps 5C and 5D. This step builds the
engine and wires Kubernetes.

## What is not in this step

- **No new prompt.** Step 4B draws the action and offers the durations. Until then a `Prompt` is
  today's request, answered once.
- **No Secret grant.** Secret reads are tagged class 6 and still always redacted; step 5A asks.
- **No host, folder, AWS or GitHub actions.** Their steps add the classifier and the scope each
  needs; `Decide` takes them as it takes Kubernetes'.
- Nothing changes on Windows: a command there runs outside the sandbox and reaches no proxy.

## Design

### 1. Classes, modes, rules, actions

`permissions/permissions.go`, a leaf that imports nothing of ours:

```go
// Class is how much an action can do, as the note numbers them.
type Class int

const (
	ReadInside    Class = 1 // read inside the sandbox
	WriteInside   Class = 2 // write the workspace or a granted folder
	NewHost       Class = 3 // reach a host not on the allowlist
	UpstreamWrite Class = 4 // change the cluster, the cloud or GitHub
	Destructive   Class = 5 // a curated list of high blast-radius writes
	SecretRead    Class = 6 // read Kubernetes Secret data
)

// Mode is which classes ask. ReadOnly refuses writes and new hosts; Ask asks
// for everything past class 2; Auto asks for class 5 alone.
type Mode string

const (
	ReadOnly Mode = "read-only"
	Ask      Mode = "ask"
	Auto     Mode = "auto"
)

// Provider is whose action it is, and decides which Scope fields apply.
type Provider string

const (
	Kubernetes Provider = "k8s"
	AWS        Provider = "aws"
	GitHub     Provider = "github"
	Google     Provider = "gcp"   // step 6C
	Azure      Provider = "azure" // step 6C
	Net        Provider = "net"   // step 4C
	Path       Provider = "path"  // step 4D
)

// Scope is where an action lands. Each field is a glob ("" matches
// everything): context and namespace for Kubernetes, account and region for
// AWS, org and repo for GitHub, host for net, folder for path.
type Scope struct {
	Context, Namespace string
	Account, Region    string
	Org, Repo          string
	Host, Folder       string
}

// Effect is what a rule does to an action it matches. Deny wins over Ask, and
// Ask over Allow.
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
	AskFor Effect = "ask" // always prompt, whatever the mode; the shipped rules are these
)

// Rule is one line of policy. Shipped is set on the rules Kstack ships, which
// the user can read and not remove. Verb and Kind narrow a Kubernetes rule
// ("delete" of "namespaces"); "" matches all.
type Rule struct {
	ID       string
	Effect   Effect
	Class    Class
	Provider Provider
	Scope    Scope
	Verb     string
	Kind     string
	Shipped  bool
}

// Action is one classified request: what a prompt says and what Decide reads.
type Action struct {
	Provider Provider
	Class    Class
	Scope    Scope
	Verb     string // get, create, update, patch, delete, deletecollection; an AWS action name; an HTTP method
	Kind     string // the Kubernetes resource, the AWS service, the GitHub route
	Name     string // the object's name, when it has one
	Summary  string // one line for the prompt, written by the classifier
}

// Decision is what Decide answers.
type Decision string

const (
	Allowed  Decision = "allow"
	Prompted Decision = "prompt"
	Denied   Decision = "deny"
)

// Policy is what a session brings to Decide: its mode, and whether anyone can
// be asked. With NoPrompts every Prompted becomes Denied.
type Policy struct {
	Mode      Mode
	NoPrompts bool
}

// Decide answers what happens to act under policy and rules, in this order.
func Decide(p Policy, rules []Rule, act Action) (Decision, Reason)
```

`Decide`, in order, the first that applies:

1. Class 1 or 2: `Allowed`.
2. A matching `Deny` rule: `Denied`, naming the rule.
3. Mode `ReadOnly`: class 3, 4 or 5 `Denied`, naming the mode. A class 6 read goes on.
4. A matching `AskFor` rule: `Prompted`, naming the rule.
5. Class 5: `Prompted`. There is no mode that removes it.
6. A matching `Allow` rule: `Allowed`, naming the rule.
7. Mode `Auto`: `Allowed`.
8. Otherwise `Prompted`: mode `Ask`, and a class 6 read in `ReadOnly`.

The mode's refusal comes before the shipped rules on purpose. A rule that says *always ask*
must never turn a read-only context's refusal into a prompt, so a `prod*` context refuses an
RBAC write instead of asking about it.

Then, with `p.NoPrompts`, a `Prompted` becomes `Denied` with a reason that says nobody could be
asked. A rule matches an action when its `Provider` and `Class` are the action's, each set
`Scope` field, `Verb` and `Kind` glob-matches the action's (`path.Match` over each field, `*`
alone matching all), and an unset field matches anything. `Reason` is a short struct: the mode
or the rule, so a refusal and a record can name it.

A rule field is a pattern; an action field is a literal value, and a kubeconfig context may be
named `dev*` or hold a `[`. `permissions.Literal(s)` escapes a value into the pattern that matches
it alone (a `\` before each `*`, `?`, `[` and `\`). Every rule Kstack writes from an action's
fields goes through it; a rule the user writes in Settings is a pattern as typed.

**The shipped rules** (`permissions/shipped.go`), each `Shipped: true`:

| Effect | Class | Provider | Rule |
| --- | --- | --- | --- |
| Deny | 5 | k8s | `delete` of `namespaces` in context `prod*` |
| Deny | 5 | k8s | `deletecollection` of `namespaces` in context `prod*` |
| AskFor | 4 | k8s | any write of `clusterroles` or `clusterrolebindings` |
| AskFor | 4 | k8s | any write of `roles` or `rolebindings` |
| AskFor | 4 | aws | any `iam:*` write (step 5C makes it match) |

The note's third shipped rule is "no cluster-scoped RBAC changes without a prompt"; namespaced
RBAC is added since a `RoleBinding` to `cluster-admin` is as wide. Deleting the namespace
collection is verb `deletecollection`, so the production denial is two rules, one per verb.

### 2. The modes, per context

`sandboxconfig.Settings` ([step 1C](1c-the-settings-file.md)) gains three fields:

```go
// In sandboxconfig.Settings.
DefaultMode permissions.Mode   `json:"defaultMode,omitempty"` // Ask when empty
Modes       []ContextMode      `json:"modes,omitempty"`       // first match wins
Rules       []permissions.Rule `json:"rules,omitempty"`       // the always rules, the user's

// ContextMode is the mode for the contexts a glob matches.
type ContextMode struct {
	Context string           `json:"context"`
	Mode    permissions.Mode `json:"mode"`
}
```

`(Settings).ModeFor(context)`, a method on step 1C's `Settings`: the first `Modes` entry whose glob matches, else `prod*` →
`ReadOnly` when no entry names `prod*`, else `DefaultMode`. The `prod*` default is code, not a
row, so it is there on a fresh file and stays until the user writes a `prod*` entry of their
own. `Rules` are read back through the same shape check the file's other fields get: a rule
with an unknown class, effect or provider is left out, logged, and shown in Settings with its
reason. The shipped rules are never in the file.

### 3. A chat's rules

`chat_grants` in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md):

```sql
CREATE TABLE chat_grants (
  id         TEXT    PRIMARY KEY,
  chat_id    TEXT    NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  rule       TEXT    NOT NULL, -- a permissions.Rule as JSON
  created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX chat_grants_chat_idx ON chat_grants (chat_id);
```

`chatsvc` owns it: `grantsFor(ctx, chatID) []permissions.Rule`, read on every `Decide`, never
cached, so a rule written while a command runs applies to its next write. A rule row that does
not decode is skipped and logged. Nothing writes the table in this step; step 4B's "this chat"
answer does, and `chatDelete` cascades.

### 4. The session's policy

`session.Session` gains `Mode permissions.Mode` and `NoPrompts bool`. `chatsvc` sets `Mode`
from `Settings.ModeFor` of the chat's cluster's context (`clustercard.ContextName` of the
record; a chat with no cluster gets `DefaultMode`), once per turn, so a mode changed in Settings
applies to the next turn. `NoPrompts` is false for a chat and a subagent; step 6D sets it for
the monitor. `Narrow` keeps both.

`session.Session` also gains `Rules func(ctx) []permissions.Rule`, set by `chatsvc` to the
chat's grants joined with `sandboxconfig`'s rules and the shipped ones, read live. A test sets a
function of its own.

### 5. The Kubernetes classifier

`kubeproxy/classify.go`: `classify(r *http.Request, p apiPath, body []byte, context string)
permissions.Action`, called after the path parse and the refusals that stand today:

| Request | Class |
| --- | --- |
| `GET` of a resource, discovery, `pods/log` | 1 |
| `GET` of core `secrets` | 6 |
| a self review `POST` | 1 |
| `POST`, `PUT`, `PATCH`, `DELETE`, `DELETECOLLECTION` of a resource | 4 |
| the class 5 list below | 5 |

The class 5 list, keyed on verb and kind, and on the body where the kind alone is not enough:

- `delete` or `deletecollection` of `namespaces`, `nodes`, `persistentvolumes`,
  `persistentvolumeclaims`, `customresourcedefinitions`;
- `deletecollection` of anything;
- any write of `clusterroles`, `clusterrolebindings`, `roles`, `rolebindings`;
- a write of the `scale` subresource, or a `PUT`/`PATCH` of `deployments`, `statefulsets` or
  `replicasets`, that sets `spec.replicas` to 0, read per media type by `scalesToZero`:
  - a `PUT`, a merge patch or a strategic merge patch: the decoded body's `spec.replicas`;
  - an apply patch: the same, decoded through the YAML path `checkBody` already uses;
  - a JSON Patch: each operation whose `path` is `/spec/replicas` or a parent of it (`/spec`,
    `""`). An `add` or `replace` there counts when its `value` puts 0 at `spec.replicas`; a
    `move` or `copy` there counts always, since its value is not in the body.

  **It fails closed.** A body that does not decode under its media type, a media type not in
  that list, or a value at `spec.replicas` that is not a number, is class 5.

`Scope` is `{Context: context, Namespace: p.namespace}`; `Verb`, `Kind` and `Name` off the
path; `Summary` is the note's form, *Delete pod `api-7f9c` in `team-a` on `dev-eks`*, spelled
by one function per verb, with the kind's singular from the path's resource as kubectl prints
it, and *(dry run)* appended where step 4B draws it. The context is the grant's session's
cluster's context, which Bash passes when it makes the grant.

`exec`, `attach`, `portforward` and every `proxy` path stay refused before classification, as
the note's *Where this meets the code* decides. `ephemeralcontainers` is a class 4 write.

### 6. The proxy asks `Decide`

`kubeproxy`'s write path (`write.go`), after `checkBody` and before the write lock:

1. `act := classify(...)`.
2. `d, why := permissions.Decide(policy, rules, act)`, where `policy` is
   `{Mode: session.Mode, NoPrompts: session.NoPrompts || nobody to ask}` — a background task's
   grant and a grant with no asker set `NoPrompts`, so a `Prompted` there is `Denied` with
   today's message, and an `Allowed` goes through, which is new: a background `kubectl apply`
   under `Auto`, or under an allow rule, runs.
3. `Allowed`: forward as an approved write does today, and record it (§7).
4. `Prompted`: `Ask` as today, with the `Action` on the `ClusterWriteRequest`.
5. `Denied`: a 403 `Status`, *kstack: <summary> is not allowed: <reason>* — the mode
   (*this context is read-only*) or the rule (*a rule denies it*, naming the rule's line).

`Grant.dryRun` follows the note: a request whose every `dryRun` is `All` is class 1 and passes
unasked, except on `secrets`, which stays class 6. This changes today's behavior, where a dry
run asks; the reason is under Decisions.

### 7. The record

`approvals.status` gains `allowed` and `refused`: a decision the engine made with nobody asked.
`approvals` gains `reason TEXT`, the mode or rule that decided, and `request` carries the
`Action` beside the write. `ClusterWrite` on the wire gains `action: PermissionAction` (its
summary, class, provider and scope) and `reason: String`, and the call's disclosure lines gain
the tags `allowed` and `refused` beside `approved`, `denied` and `not answered`, each with the
reason in a `title`-free muted span. So a write that ran under a rule is on screen, as the note
asks: every upstream write is logged per command.

### 8. Settings: Permissions

`permission-settings.tsx`, a Permissions section in the Settings dialog:

- **Default mode**: a segmented picker, Read-only / Ask / Auto, with one line under each
  saying what asks. Auto's line says plainly that it also shows Secret values without asking,
  since that is the one read a mode opens (the note's table; step 5A).
- **Contexts**: one row per context the clusters watch knows, its effective mode and a picker
  to override it; a row whose mode comes from `prod*` says *read-only by default*. Writing an
  override calls `permissionModeSet(context, mode)`; clearing one `permissionModeClear`.
- **Rules**: the always rules as lines in the user's terms (*Allow cluster writes in `dev-eks`
  / `team-a`*), each with Remove, then the shipped rules under *Always asks* and *Never
  allowed*, read-only. An Add form: effect, class, provider, and the scope fields the provider
  has. A rule the file refused shows its reason, read from step 1C's `sandboxRefused` for the
  `rules` field.

The wire: `permissionSettings: PermissionSettings!` (`defaultMode`, `modes`, `rules` with
`shipped`), `permissionModeSet`, `permissionModeClear`, `permissionRuleAdd(input)`,
`permissionRuleRemove(id)`. A bad rule is `KSTACK_VALIDATION_ERROR` with the reason.

### 9. The prompt

`prompts/sandbox.md` says a change to the cluster may run at once under the user's rules, wait
for the user, or come back `Forbidden` because the user's mode for this context refuses it, and
that a `Forbidden` naming the mode or a rule is the user's decision and not an error to work
around.

## Decisions this step asks for

1. **Three modes, not four.** The note lists *Trusted scopes* as a mode: class 4 allowed in
   listed contexts and namespaces, asked elsewhere. That is exactly `Ask` with `Allow` rules
   scoped to those contexts and namespaces, and the prompt's "allow for this chat" and "always
   allow this scope" answers have to work in `Ask` for the prompt to be honest. So the engine
   has `ReadOnly`, `Ask` and `Auto`, and the Settings section calls a context's `Allow` rules
   its trusted scopes. Recommended; a fourth mode would be a second spelling of the same rules.
2. **A dry run is a read.** The note says so, and `kubectl diff` and `--dry-run=server` are how
   a careful model previews a change. Today a dry run asks, because the API server runs
   admission webhooks over the body. Recommended: follow the note, and say in the security
   record that a dry run's body reaches the cluster's webhooks unasked.
3. **A chat's rules end with the chat**, not with the app. "For this chat" then means what it
   says, and a chat reopened tomorrow keeps what the user allowed. Recommended.
4. **A scale body the classifier cannot read is class 5.** The other answer is class 4, which
   `Auto` runs unasked, so a form the classifier misses would scale to zero with no one asked.
   Recommended: an unreadable body asks in `Auto`, and a read-only context refuses it either
   way.
5. **A rule Kstack writes escapes the action's values**, rather than a rule gaining an exact
   field beside each pattern. A context is text the user's kubeconfig names, and read as a
   pattern `dev*` would grant every `dev` context. Recommended: `Literal` keeps one rule shape,
   and the file stays one a user can read and edit.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `permissions`: the types, `Decide`, the shipped rules | `permissions/permissions.go`, `permissions/shipped.go`, their tests | — | Planned |
| 2 | The settings: modes and rules, `ModeFor`, the read-back check | `sandboxconfig/`, its tests | 1 | Planned |
| 3 | `chat_grants` and `grantsFor`; the session's `Mode`, `NoPrompts` and `Rules` | `appdb/migrations/0001_init.sql`, `chatsvc/`, `session/`, their tests | 1, 2 | Planned |
| 4 | The Kubernetes classifier | `kubeproxy/classify.go`, its test | 1 | Planned |
| 5 | The proxy asks `Decide`; the record's new statuses and reason | `kubeproxy/write.go`, `tools/tool.go`, `tools/bash/proxy.go`, `chatsvc/approval.go`, their tests | 3, 4 | Planned |
| 6 | The wire and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 2, 5 | Planned |
| 7 | The Settings section; the disclosure's tags | `src/components/widgets/permission-settings.tsx`, `settings-dialog.tsx`, `chat-transcript.tsx`, `src/lib/chats.tsx`, their tests | 6 | Planned |
| 8 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 5 | Planned |
| 9 | Docs, per *When it lands* | see there | 1–8 | Planned |

**Order:** 1, then 2 and 4 at the same time, then 3, then 5, then 6, then 7 and 8 at the same
time, then 9.

## Tests

**`permissions`**

- `TestDecideFollowsTheModeTable`: one table over every mode and class, matching the note's
  table with *Trusted* read as `Ask` plus an `Allow` rule.
- `TestClassFiveAsksInEveryMode`, and is denied in `ReadOnly`.
- `TestShippedRulesApplyInEveryMode`: each shipped rule, under each mode, with and without an
  `Allow` rule over the same scope. The note's eighth invariant.
- `TestDenyWinsOverAskWinsOverAllow`.
- `TestAScopeMatchesByGlob`: `dev-*` matches `dev-eks`, an unset field matches all, a set one
  refuses another value.
- `TestALiteralMatchesItselfAlone`: `Literal("dev*")` matches `dev*` and not `dev-eks`, and a
  context holding `[`, `?` or `\` matches itself.
- `TestProdNamespaceDeletionIsDeniedByEitherVerb`: `delete` and `deletecollection` of
  `namespaces` in `prod-eu`, under a user's `prod*` entry of `Ask` and of `Auto`, are `Denied`
  naming a shipped rule.
- `TestNoPromptsTurnsAPromptIntoADenial`, and leaves an `Allowed` alone.

**`sandboxconfig`**

- `TestProdIsReadOnlyByDefault`, and a user's `prod*` entry replaces it.
- `TestModesMatchInOrder`.
- `TestABadRuleIsLeftOutWithItsReason`.

**`chatsvc`**

- `TestAChatsGrantsGoWithTheChat`: rows are read live and cascade on delete.
- `TestTheSessionCarriesTheContextsMode`: a chat on a `prod-eu` cluster gets `ReadOnly`, one on
  `dev` gets `Ask`, and a mode changed in the file reaches the next turn and not the running one.

**`kubeproxy`**

- `TestEveryRequestIsClassified`: a table over the rows of §5, the class 5 list included, a
  scale to 0 as a merge patch, a strategic patch and an apply, and a scale to 1 as class 4.
- `TestAScaleToZeroIsClassFiveInEveryPatchForm`: a JSON Patch `replace` and `add` of
  `/spec/replicas` to 0, a `replace` of `/spec` holding `replicas: 0`, a `move` and a `copy` onto
  `/spec/replicas`, each as class 5, on the resource and on `scale`; a JSON Patch to 1 as class
  4; a body that does not decode, and an unknown media type, as class 5.
- `TestASummaryReadsAsTheNoteSays`: *Delete pod `api-7f9c` in `team-a` on `dev-eks`*, and the
  cluster-scoped and unnamed forms.
- `TestAnAllowedWriteForwardsUnasked`, and is recorded `allowed` with its reason.
- `TestADeniedWriteIsAForbiddenStatus` naming the mode, and one naming the rule.
- `TestAReadOnlyContextRefusesEveryWrite`, class 5 and a write a shipped `AskFor` rule names
  included, and asks for a Secret read (step 5A turns the ask into a grant).
- `TestADryRunIsARead`, except on `secrets`.
- `TestABackgroundWriteRunsUnderAnAllowRule`, and is refused under `Ask`, with today's message.

**Webview** (`permission-settings.test.tsx`, `chat-transcript.test.tsx`)

- The modes picker, the contexts' effective modes with the `prod*` line, the rules with Remove
  and the shipped ones without, the add form's fields per provider, and each mutation.
- A write tagged `allowed` or `refused` draws its reason.

## Security

This is the security change the note's second decision names: for a classified cluster write,
a rule or the `Auto` mode can let a change run with no one asked, which the bash tool record
forbade ("no setting, no allowlist"). What holds it: the sandbox is still the floor, so a
command reaches the cluster only through the proxy, which classifies mechanically; class 5
is never allowed by a mode or a rule (it asks, or is refused in a read-only context) and the
shipped rules cannot be removed; a rule is scoped, written by the
user, and on screen in Settings; every write that ran unasked is recorded and drawn with its
reason. A raw command outside the sandbox still asks every time.

Residuals: an `Allow` rule's scope is a context and a namespace, and a write in that scope can
reach past it (a Pod that mounts a Secret and prints it, as the sandboxed-bash record says); a
dry run's body reaches admission webhooks unasked (decision 2); a class 5 list is a list, and a
destructive write it does not name is class 4.

The record, `docs/security/<date>-the-permissions-engine.md`, argues this and supersedes the
consent paragraph of [the bash tool](../../security/2026-09-18-bash-tool.md) for classified
actions.

## When it lands

- **The security record** above, and an ADR: permissions are classes, modes and rules decided
  at the proxy; three modes; a chat's rules are rows and the always rules are settings; a dry
  run is a read.
- **`security-model.md`**: the cluster-write rows say a write runs, asks or is refused by
  `Decide`, with the tests; a row for the shipped rules; the *Consent* line of the bash tool
  record's row amended for classified actions.
- **`sidecar/CLAUDE.md`**: `permissions`, the classifier, the write path's order, the session's
  policy fields, `chat_grants`, `sandboxconfig`'s modes and rules.
- **Root `CLAUDE.md`**, *Chat* and the Settings dialog: the Permissions section, the new tags.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` against a kind cluster with two contexts, `dev` and `prod-test`: on a
`dev` chat, `kubectl delete pod x` asks as today; on a `prod-test` chat it comes back
`Forbidden` naming the read-only mode, and `kubectl get secret x -o yaml` reads `[redacted]`;
add an always rule allowing cluster writes in `dev` / `default` in Settings, ask again on `dev`,
and read the pod go with no request and the write tagged `allowed` in the disclosure; ask for
`kubectl delete ns test` on `dev` and read it ask, rule or not.
