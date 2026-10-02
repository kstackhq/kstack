---
title: The permissions engine
scope: sidecar, webview
status: Planned
---

# The permissions engine

**Needs:** step 2C, whose session carries the mode, and step 1C, whose settings file keeps the
rules. **Unblocks:** steps 4B, 4C, 4D, 5A and 6B.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the cluster proxy has one rule for a write: put it to the user, or refuse it when nobody
can be asked. The note wants every action to fall into one of six **classes**, and the
user's **approval mode** and **rules** to decide, per class and scope, whether the action runs,
asks or is refused. The model is never the enforcement point; the proxy is.

After this step:

- **`permissions`** is one package that knows the classes, the modes, the rules, the shipped
  deny rules, and `Decide`.
- **The cluster proxy classifies every request** (class 1, 4, 5 or 6) and asks `Decide` before
  each write. `Allow` forwards it unasked and records it; `Prompt` puts it to the user as
  today; `Deny` refuses it with a `Status` that names the mode or the rule.
- **The approval mode is per context**, kept in `securityconfig`, with `*prod*` read-only unless
  the user says otherwise, and Ask everywhere else.
- **Rules last for a chat or always.** A chat's are rows in `app.db` that go with the chat; the
  always rules live in `securityconfig`. Step 4B lets a prompt write either; this step lets
  Settings write the always rules.
- **A Settings section** shows the modes and the rules, the shipped ones read-only.

The request the user sees is unchanged in this step; step 4B redraws it around the action. Class
3 (a new host) arrives with step 4C, class 2 (a granted folder) with step 4D, and class 6's
grant with step 5A. This step builds the engine and wires Kubernetes.

## What is not in this step

- **No new prompt.** Step 4B draws the action and offers the durations. Until then a `Prompt` is
  today's request, answered once.
- **No Secret grant.** Secret reads are tagged class 6 and still always redacted; step 5A asks.
- **No host or folder actions.** Their steps add the classifier and the scope each needs;
  `Decide` takes them as it takes Kubernetes'.
- **No writes from a background command.** A background task's grant has no asker and no call
  open to record against, so it refuses every write, as today, whatever the mode or rules (§6).
  No step in this sequence changes that yet; one that does gives the task a recorder first.
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
	UpstreamWrite Class = 4 // change the cluster
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
	Net        Provider = "net"  // step 4C
	Path       Provider = "path" // step 4D
)

// Scope is where an action lands. On a rule each field is a pattern (Match;
// "" matches everything): context and namespace for Kubernetes, host for net,
// folder for path.
type Scope struct {
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Host      string `json:"host,omitempty"`
	Folder    string `json:"folder,omitempty"`
}

// Effect is what a rule does to an action it matches. Deny wins over Ask, and
// Ask over Allow.
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
	AskFor Effect = "ask" // always prompt, unless the mode refuses first
)

// Rule is one line of policy. Shipped is set on the rules Kstack ships, which
// the user can read and not remove; it is never read from a file. Verb, Group
// and Kind narrow a Kubernetes rule ("delete" of core "namespaces"); unset
// matches all. Group is a literal: "" is unset, "core" the core group.
type Rule struct {
	ID       string   `json:"id"`
	Effect   Effect   `json:"effect"`
	Class    Class    `json:"class"`
	Provider Provider `json:"provider"`
	Scope    Scope    `json:"scope,omitzero"`
	Verb     string   `json:"verb,omitempty"`
	Group    string   `json:"group,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Shipped  bool     `json:"-"`
}

// Action is one classified request: what a prompt says and what Decide reads.
type Action struct {
	Provider Provider
	Class    Class
	Scope    Scope
	Verb     string // get, create, update, patch, delete, deletecollection; CONNECT for a host
	Group    string // the Kubernetes API group, "core" for the core group
	Kind     string // the resource, "deployments/scale" for a subresource; a port for a host
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

The mode's refusal comes before every `AskFor` rule and class 5's prompt on purpose. Neither may
turn a read-only context's refusal into a prompt, so a `*prod*` context refuses an RBAC write
instead of asking about it.

Then, with `p.NoPrompts`, a `Prompted` becomes `Denied` with a reason that says nobody could be
asked. A rule matches an action when its `Provider` is the action's, its `Class` covers the
action's, and each set `Scope` field, `Verb`, `Group` and `Kind` matches the action's; an unset
field matches anything. `Group` is compared exactly. A `Kind` with no `/` matches the resource and
each of its subresources, so *allow patch of `deployments`* covers `deployments/scale`; one with a
`/` matches that subresource alone. A class covers itself, and class 4 covers class 5 too: a
destructive write is a cluster write, so "deny cluster writes in `prod-eu`" refuses a scale to zero
there rather than asking. An `Allow` of class 4 cannot reach class 5, since step 5 prompts first.
`Reason` is a short struct: the mode or the rule, so a refusal and a record can name it.

**`permissions.Match(pattern, value)`** is the one matcher, for rules and for `Modes` (§2): `*`
matches any run of characters, `/` and `:` included, `?` one character, `\` escapes the next, and
nothing else is special. A context is often an ARN (`arn:aws:eks:…:cluster/prod-eu`) or a GKE name
(`gke_project_zone_prod`), so a `*` that stopped at `/`, as `path.Match`'s does, could not reach
the cluster's name.

A rule field is a pattern; an action field is a literal value, and a kubeconfig context may be
named `dev*`. `permissions.Literal(s)` escapes a value into the pattern that matches it alone (a
`\` before each `*`, `?` and `\`). Every rule and mode entry Kstack writes from a value goes
through it; a rule the user writes in the Settings form, or by hand, is a pattern as typed.

**The shipped rules** (`permissions/shipped.go`), each `Shipped: true`:

| Effect | Class | Provider | Rule |
| --- | --- | --- | --- |
| Deny | 5 | k8s | `delete` of core `namespaces` in context `*prod*` |
| Deny | 5 | k8s | `deletecollection` of core `namespaces` in context `*prod*` |

Deleting the namespace collection is verb `deletecollection`, so the production denial is two rules,
one per verb. **A shipped rule cannot be removed, and no rule or prompt overrides it.** One thing
lifts it for one context: a mode the user sets for that context in Settings, which is the user
saying the context is not what `*prod*` guessed. `*prod*` is a guess at a name, and `dev-products`
or `nonprod` would otherwise be unable to delete a namespace in the sandbox at all. So the shipped
rules a run sees are `(*Store).ShippedFor(context)`: both, unless `Modes` holds an entry equal to
`Literal(context)` and the store does not hold `modes` (§2); a pattern entry, `*` included, lifts
nothing. Where they hold, what they refuse is done from a chat switched outside the sandbox, where
the user approves the command itself. The note's other shipped rule, "no cluster-scoped RBAC changes
without a prompt", needs no rule: the classifier puts every write of `clusterroles`,
`clusterrolebindings`, `roles` and `rolebindings` in class 5 (§5), which asks in every mode and is
refused in a read-only one. Namespaced RBAC is there too, since a `RoleBinding` to `cluster-admin`
is as wide.

### 2. The modes, per context

`securityconfig.Settings`
([`sidecar/CLAUDE.md`](../../../sidecar/CLAUDE.md#security-settings-internalsecurityconfig)) gains
three fields:

```go
// In securityconfig.Settings.
DefaultMode permissions.Mode   `json:"defaultMode,omitempty"` // Ask when empty
Modes       []ContextMode      `json:"modes,omitempty"`       // first match wins
Rules       []permissions.Rule `json:"rules,omitempty"`       // the always rules, the user's

// ContextMode is the mode for the contexts a glob matches.
type ContextMode struct {
	Context string           `json:"context"`
	Mode    permissions.Mode `json:"mode"`
}
```

`(*Store).ModeFor(context) (Mode, ModeSource)`: `ReadOnly` while the store holds `modes` (below),
else the first `Modes` entry whose pattern matches (`permissions.Match`), else `ReadOnly` when the
context matches `*prod*`, else `DefaultMode`. `ModeSource` says which (`refused`, `entry`, `prod`,
`default`), so Settings can say where a context's mode comes from. `(*Store).Rules()` is the always
rules as `Decide` reads them, the same way: while the store holds `rules`, the rules that passed
less every `Allow`, plus a `Deny` of class 4 for `k8s` with the id `refused`, which is never written
to the file and which the `rules` check refuses there. These two are the only readers of `Modes` and
`Rules` outside the Settings resolvers. The `*prod*` default is code, not a row, so it is there on a
fresh file. An entry the user writes for one context in Settings is `Literal(context)`, so it names
that context alone, and `permissionModeSet` puts it first in `Modes`, replacing any entry already
equal to it: a literal names one context, so ahead of every pattern it decides that context and no
other. A pattern entry is a hand edit, and one that matches a production context (`*` included)
replaces the read-only default for it, which Settings shows on that context's row.

**The read-back**, one line per field in step 1C's `checks`, and a line in `strictest` for each,
since every one of them can restrict:

| Field | Refused | Strictest state while refused |
| --- | --- | --- |
| `defaultMode` | a value other than `read-only`, `ask`, `auto` | `ReadOnly` |
| `modes` | an entry whose mode is not one of those, or whose context is empty | every context read-only |
| `rules` | a rule with an unknown effect, class or provider, a class its provider does not have or one of 1 and 2, which `Decide` allows before any rule, an `Allow` of class 5, which `Decide` asks about before any `Allow`, an empty or repeated `id` or the id `refused`, or a `Verb`, `Group` or `Kind` on a provider other than `k8s` | the rules that passed less every `Allow`, plus a `Deny` of class 4 for `k8s`: every cluster write refused |

A refused `rules` element may have been a `Deny`, so the field answers its strictest state rather
than dropping the element, as `securityconfig` requires of a field that restricts; a later step
that adds a provider adds its `Deny` to this line.

**For `modes` and `rules` the strictest state is applied where the field is read, not stored.**
Their `strictest` lines leave the elements that passed as they are; the line is what makes the
store keep the field's raw JSON. The store gains `Held(field string) bool`, whether it still keeps
it, and `ModeFor` and `Rules` above read it. A strictest state written into `Settings` would be
saved by the next per-element mutation: a `{"*", ReadOnly}` entry ahead of every context the user
then set, or the synthetic `Deny` in place of the user's `Allow` rules. `defaultMode` is one value,
so its line sets `ReadOnly` as step 1C's convention says, and setting the default mode is its fix.
**The store guards a held field.** Today any `Update` that changes a held field drops its raw
JSON (`stillHeld`), so whichever write came first would lose a value that may have been a
`Deny`. So `Update` refuses, with `ErrHeld`, a change to a held field its `fields` does not name.
The guard is in the store, so every writer is held to it: `permissionModeSet`,
`permissionModeClear`, `permissionRuleAdd` and `permissionRuleRemove`, and step 4B's *Always*
answer, which writes `Rules` through the same write. Each refusal is `KSTACK_VALIDATION_ERROR`
saying the file holds a value Kstack cannot read. The user fixes the file, or calls
`permissionDiscardRefused(field)`, which writes the field as it stands, the elements that passed,
naming it in `fields`: that ends the hold and drops what could not be read, which Settings says
before the user confirms (§8). `permissionDefaultModeSet` names `defaultMode` the same way.

`Held`, `ErrHeld` and the guard are shared with step 3A, which needs them for `path`: whichever step
lands first adds them to `store.go` with their tests, and the other uses them. Each refusal is
logged and shown in Settings with its reason while its field is held. The shipped rules are never in
the file.

### 3. A chat's rules

`chat_grants` in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md):

```sql
CREATE TABLE chat_grants (
  id         TEXT    PRIMARY KEY,
  chat_id    TEXT    NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  rule       TEXT    NOT NULL, -- a permissions.Rule as JSON
  created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX chat_grants_chat_idx ON chat_grants (chat_id);
```

`chatsvc` owns it: `grantsFor(ctx, chatID) []permissions.Rule`, read on every `Decide`, never
cached, so a rule written while a command runs applies to its next write. Only Kstack writes
the table, but a row that does not decode may have been a `Deny`, so it is logged and read as
a `Deny` of class 4 for `k8s`, as a refused rule of the file is (§2): the chat's cluster writes
are refused until the row is removed. A later step that adds a provider adds its `Deny` here
too. Nothing writes the table in this step; step 4B's "this chat"
answer does, and `chatDelete` cascades.

### 4. The session's policy

`session.Session` gains `Mode func(ctx context.Context, kubeContext string) permissions.Mode` and
`NoPrompts bool`. The proxy calls `Mode` with the action's `Scope.Context`, so the mode and the
rules are decided for one context, the one the grant was made for, and a context renamed while a
run lives cannot split them. `chatsvc` sets `Mode` to the store's `ModeFor`, read live on every
call, so a mode changed in Settings applies to the next write, the running turn's and a running
subagent's included.

**The context is `clustercard.ScopeContext(cluster)`**, which Bash reads once per run and passes
to `NewGrant`, so the classifier's `Scope.Context`, `Mode` and `Rules` all see the same value: the
record's `KubeContext()`, uncut, and `""` for a record that names none. Not
`ContextName` beside it, which cuts a long name, so two long contexts would share one rule; and
not the name Bash gives the run's kubeconfig, which is `kstack` for a record that names none. `""`
is matched only by an unset or `*` pattern.

`NoPrompts` is false for a chat and a subagent; step 6B sets it for the monitor. By step 2C's
narrowing rule `NoPrompts` is identity, copied at spawn, and `Mode` is policy: `Narrow` hands the
subagent the parent's function.

`session.Session` also gains `Rules func(ctx context.Context, kubeContext string)
[]permissions.Rule`, called the same way and set by `chatsvc` to the chat's grants joined with the
store's `Rules()` and `ShippedFor(kubeContext)`, read live. A test sets a function of its own.

### 5. The Kubernetes classifier

`kubeproxy/classify.go`: `classify(r *http.Request, p apiPath, body []byte, context string)
permissions.Action`, called after the path parse and the refusals that stand today:

| Request | Class |
| --- | --- |
| `GET` of a resource, discovery, `pods/log` | 1 |
| `GET` of core `secrets` | 6 |
| a self review `POST` | 1 |
| a `POST`, `PUT` or `PATCH` whose every `dryRun` is `All` (`isDryRun`), on any resource | 1 |
| `POST`, `PUT`, `PATCH`, `DELETE` of a resource; a `DELETE` with no name is verb `deletecollection` | 4 |
| the class 5 list below | 5 |

The class 5 list, keyed on verb, group and resource, and on the body where those are not enough.
A custom resource of another group that shares a built-in's plural is class 4 like any other.
`classify.go` also exports it in words as `Destructive []string`, one line per item below, which
Settings shows under *Always asks*:

- `delete` or `deletecollection` of core `namespaces`, `nodes`, `persistentvolumes`,
  `persistentvolumeclaims`, and of `apiextensions.k8s.io` `customresourcedefinitions`;
- `deletecollection` of anything;
- any write of `rbac.authorization.k8s.io` `clusterroles`, `clusterrolebindings`, `roles`,
  `rolebindings`;
- any write of `admissionregistration.k8s.io` `validatingwebhookconfigurations`,
  `mutatingwebhookconfigurations`, `validatingadmissionpolicies` and
  `validatingadmissionpolicybindings`, which see or change every write to the cluster;
- any write of the `certificates.k8s.io` `certificatesigningrequests/approval` subresource,
  which can mint a client certificate for any identity a signer serves;
- any write of the `ephemeralcontainers` subresource, which starts a process of the model's in a
  running pod, with the pod's service account and mounts;
- a write of the `scale` subresource, or a `PUT`/`PATCH` of `deployments`, `statefulsets` or
  `replicasets`, that sets `spec.replicas` to 0, read per media type by `scalesToZero`:
  - a `PUT`, a merge patch or a strategic merge patch: the decoded body's `spec.replicas`;
  - an apply patch: the same, decoded through the YAML path `checkBody` already uses;
  - a JSON Patch: each operation whose `path` is `/spec/replicas` or a parent of it (`/spec`,
    `""`). An `add` or `replace` there counts when its `value` puts 0 at `spec.replicas`; a
    `move` or `copy` there counts always, since its value is not in the body.

  **It fails closed.** A body that does not decode under its media type, or a value at
  `spec.replicas` that is not a number, is class 5. (`checkBody` has already refused a media
  type not in `showableTypes`.)

The dry-run row comes before the writes, so a dry run is a read whatever it names; on `secrets` the
response is redacted as every Secret response is (`rewriteSecrets`), so a dry run reads nothing a
`GET` would not. The class 5 list counts only writes that are not dry runs. A `DELETE` is never a
dry run (`isDryRun`: the API server reads its options from the body), so `kubectl delete
--dry-run=server` is a write like any other and is refused in a read-only context.

`Scope` is `{Context: context, Namespace: p.namespace}`; `Verb`, `Group` (`core` for the core
group), `Kind` (the resource, `/` and the subresource when there is one) and `Name` off the
path; `Summary` is the note's form, *Delete pod `api-7f9c` in `team-a` on `dev-eks`*, spelled by
one function per verb, and *(dry run)* appended where step 4B draws it. The kind's singular
comes from a table of the built-in resources as kubectl prints them; any other resource, a
custom one included, is named by its plural as the path gives it, since the proxy reads no
discovery. The context is `clustercard.ScopeContext` of the grant's cluster, which Bash passes
to `NewGrant` beside the session.

`exec`, `attach`, `portforward` and every `proxy` path stay refused before classification, as
the note's *Where this meets the code* decides.

### 6. The proxy asks `Decide`

**A grant with no asker refuses every write, as today**, before any of the below: a background
task's grant, and any other run nobody can be asked about. The asker is also what records a
write (§7), and a background command's call has returned by the time its writes arrive, so there
is no call open to record against. A background write that runs under a rule waits for a later
step to give a background task a recorder of its own.

On a grant with an asker, `kubeproxy`'s write path (`write.go`) keeps its order — the release and
query refusals, the write lock, the body read, `checkBody` — and then, still under the lock, so
writes reach the cluster in the order they were decided:

1. `act := classify(...)`.
2. `d, why := permissions.Decide(policy, rules, act)`, where `policy` is
   `{Mode: session.Mode(ctx, act.Scope.Context), NoPrompts: session.NoPrompts}` and `rules` is
   `session.Rules(ctx, act.Scope.Context)`.
3. `Allowed`: record it through the asker (§7), then forward as an approved write does today. A
   record the store refuses is `refusedUnrecorded`, as today: nothing runs that the record does
   not hold.
4. `Prompted`: `Ask` as today, with the `Action` on the `ClusterWriteRequest`.
5. `Denied`: record it, then a 403 `Status`, *kstack: <summary> is not allowed: <reason>* — the
   mode (*this context is read-only*) or the rule (*a rule denies it*, naming the rule's line).
   A record the store refuses is logged and the write is refused all the same, with that
   `Status`: nothing runs either way, so the refusal does not wait on the record.

A dry run is class 1 (§5), so it runs unasked, recorded `allowed`. This changes today's behavior,
where a dry run asks; the reason is under Decisions.

### 7. The record

`approvals.status` gains `allowed` and `refused`: a decision the engine made with nobody asked, for
`kind = 'cluster'` alone (the `CHECK`s say so, as they do for `abandoned`). `approvals` gains
`reason TEXT`, the mode or rule that decided, and `request` carries the `Action` beside the write.
`kubeproxy.Asker` gains `Record(ctx, w Write, d Decision, why Reason) error`, which
`clusterWriteAsker` implements as `askClusterWrite` writes a request, against the call the run has
open, with the decision already made: no wait, and the run stays `Streaming`. `ClusterWrite` on the
wire gains `action: PermissionAction` and `reason: String` (§8 gives both),
and the call's disclosure lines gain the tags `allowed` and `refused` beside `approved`, `denied`
and `not answered`, each with the reason in a `title`-free muted span. A dry run's line says *(dry
run)* after its path, where its request said it before; the request's heading no longer needs the
case, and loses it. So a write that ran under a rule is on screen, as the note asks: every upstream
write is logged per command.

### 8. Settings: Permissions

`permission-settings.tsx`, a Permissions section in the Settings dialog, drawn only while step
1B's `sandbox.available` is true: on a machine with no sandbox every command runs outside it and
asks, and no mode or rule applies.

- **Default mode**: a segmented picker, Read-only / Ask / Auto, which calls
  `permissionDefaultModeSet(mode)`, with one line under each saying what asks. Auto's line says
  plainly that it also shows Secret values without asking, since that is the one read a mode opens
  (the note's table; step 5A). One line under the picker says what a mode governs: *Modes and rules
  decide what sandboxed commands may change. A chat switched outside the sandbox asks for every
  command.*
- **Contexts**: one row per context the clusters watch knows, its effective mode and a picker
  to override it, and where the mode comes from: *read-only by default* for the `*prod*` default,
  the pattern of a hand-written entry, or *read-only until the file is fixed* while `modes` is
  held. Writing an override calls
  `permissionModeSet(context, mode)` with the context's name, which the resolver writes first as
  `Literal(context)`; clearing one calls `permissionModeClear(context)`. On a context the shipped
  rules hold for, the picker's confirm says the override also lets the sandbox delete its
  namespaces with the user's approval.
- **Rules**: the always rules as lines in the user's terms (*Allow cluster writes in `dev-eks` /
  `team-a`*), each with Remove, then *Never allowed*, the shipped rules, and *Always asks*, the
  class 5 list as `classify.go`'s `Destructive` names it, both read-only. An Add form: effect, class
  (4, 5 or 6 for `k8s`, and 5 only for `Deny` and *ask*), provider, the scope fields the provider
  has, and for `k8s` the verb, the API group and the resource, each optional; every field but the
  group is a pattern. The shipped rules' line says they hold in every mode in a context matching
  `*prod*` until the user sets that context's mode, and that a chat switched outside the sandbox is
  how to do what they refuse.
- **A held field** (`modes` or `rules`) shows each refused value with its reason, read from step
  1C's `securityRefused`, and what Kstack does meanwhile (*every context is read-only*, *every
  cluster write is refused*), until the file is fixed. Its pickers, Add and Remove are disabled,
  and one button, *Discard what Kstack cannot read*, calls `permissionDiscardRefused(field)` after
  a confirm that names the values it drops. A refused `defaultMode` shows its reason above the
  picker, which is its fix.

The record's wire, beside today's `ClusterWrite`:

```graphql
enum ApprovalStatus { Pending Approved Denied Abandoned Allowed Refused }

enum PermissionClass { ReadInside WriteInside NewHost UpstreamWrite Destructive SecretRead }
enum PermissionProvider { K8s Net Path }

"What the proxy classified a request as. Every string is cluster or user text: draw it through VisibleText."
type PermissionAction {
  "One line, as the prompt and the disclosure draw it."
  summary: String!
  class: PermissionClass!
  provider: PermissionProvider!
  "The context, as the grant was made for it; empty for a record that names none."
  context: String!
  "The namespace; empty for a cluster-scoped request."
  namespace: String!
}

extend type ClusterWrite {
  "What the proxy classified the write as; null on a write recorded before this step."
  action: PermissionAction
  "Who decided a write nobody was asked about: the mode (`this context is read-only`, `auto mode`) or the rule's line. Null for a write the user answered."
  reason: String
}
```

`Allowed` and `Refused` are `kind = 'cluster'` alone, as `Abandoned` is. 4C and 4D add `host` and
`folder` to `PermissionAction` when their providers arrive.

The settings' wire: `permissionSettings: PermissionSettings!` (`defaultMode`, `modes`, `rules` with
`shipped`, `destructive: [String!]!`, `held: [String!]!` naming the held fields, and per known
context its effective mode and source), `permissionDefaultModeSet(mode)`,
`permissionModeSet(context, mode)`, `permissionModeClear(context)`, `permissionRuleAdd(input)`,
`permissionRuleRemove(id)` and `permissionDiscardRefused(field)`. A bad rule, a per-element
mutation on a held field, or a discard of a field that is not held is `KSTACK_VALIDATION_ERROR`
with the reason; the resolver mints a rule's `id` with `appdb.NewID()`.

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
2. **A dry run is a read, on any resource.** The note says so, and `kubectl diff` and
   `--dry-run=server` are how a careful model previews a change. Today a dry run asks, because
   the API server runs admission webhooks over the body. On `secrets` its response is redacted
   like any read's, so it needs no class of its own. Recommended: follow the note, and say in the
   security record that a dry run's body reaches the cluster's webhooks unasked.
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
6. **The production default is `*prod*`, where the note had `prod*`.** Cloud tools name contexts
   after the account first: `arn:aws:eks:…:cluster/prod-eu`, `gke_project_zone_prod`. A prefix
   misses both, and it is the default that protects a user who never opens Settings. `*prod*`
   also catches `preprod`, `nonprod` and `dev-products`, which errs toward refusing. One
   override per context, set in Settings, undoes both the read-only mode and the shipped
   namespace-deletion rules there: the user has said what the context is, and a guess at a name
   should not outlast that. No rule or prompt lifts them, and neither does a pattern entry, so a
   hand-written `*` cannot switch them off everywhere. Recommended; the note, the README and step
   7A say `*prod*`, and the note says what lifts the shipped rules.
7. **`*` crosses `/`.** `path.Match`'s does not, so no pattern but `*` alone could reach past an
   ARN's `cluster/`. `Match` has `*`, `?` and `\` and nothing else: no character classes, so a `[`
   in a context needs no escape. Recommended.
8. **A cluster-write rule covers destructive writes.** Matching a class alone, a user's *Deny
   cluster writes in `prod-eu`* would leave a scale to zero there asking, which reads as the rule
   not holding. Class 4 covers class 5, and nothing lets an `Allow` reach class 5 (decision
   order, step 5). Recommended.
9. **A refused value fails toward refusing.** A hand edit must never cost a restriction, and a
   `rules` element the store cannot read may have been a `Deny`; the field then refuses every
   cluster write until it is fixed, and Settings says so. For a list the strict state is applied
   where the field is read and its per-element mutations wait for the fix or an explicit discard,
   since a strict state kept in `Settings` would be saved by the next edit. Recommended.
10. **Background writes stay refused.** Letting one run under `Auto` or a rule needs a record
    that outlives the call, and the decision of what the transcript draws for a write that
    arrives after the answer settled. That is its own change. Recommended.
11. **Rules name the API group, and a resource covers its subresources.** Without a group a rule
    for `deployments` reaches every group's; without the subresource rule an *allow patch of
    `deployments`* would leave a `kubectl scale` asking. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `permissions`: the types, `Match`, `Literal`, `Decide`, the shipped rules | `permissions/permissions.go`, `permissions/match.go`, `permissions/shipped.go`, their tests | — | Planned |
| 2 | The settings: modes and rules, `ModeFor`, `Rules`, `ShippedFor`, `Held`, `ErrHeld` and the store's guard (unless step 3A added them), the read-back checks and strictest lines | `securityconfig/`, its tests | 1 | Planned |
| 3 | `chat_grants` and `grantsFor`; `ScopeContext`; the session's `Mode`, `NoPrompts` and `Rules` | `appdb/migrations/0001_init.sql`, `clustercard/`, `chatsvc/`, `session/`, `app/app.go` (`chatsvc` gets the store), their tests | 1, 2 | Planned |
| 4 | The Kubernetes classifier and its `Destructive` list | `kubeproxy/classify.go`, its test | 1 | Planned |
| 5 | The proxy asks `Decide`; `Asker.Record`; the record's new statuses and reason | `kubeproxy/write.go`, `kubeproxy/kubeproxy.go` (`NewGrant`'s context), `appdb/migrations/0001_init.sql` (`approvals`' statuses and `reason`), `tools/tool.go`, `tools/bash/proxy.go`, `chatsvc/approval.go`, their tests | 3, 4 | Planned |
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
  `Allow` rule over the same scope, is `Denied` when it is in the rules `Decide` is given. The
  note's invariant on shipped rules; whether they are given is `ShippedFor`'s, tested under
  `securityconfig`.
- `TestDenyWinsOverAskWinsOverAllow`.
- `TestAClassFourRuleCoversClassFive`: a user's class 4 `Deny` refuses a class 5 action in its
  scope, a class 4 `AskFor` asks for it, and a class 4 `Allow` leaves it asking.
- `TestMatchCrossesSlashes`: `*prod*` matches `arn:aws:eks:us-east-1:1:cluster/prod-eu` and
  `gke_p_z_prod`, `dev-*` matches `dev-eks`, `?` matches one character, `[` is an ordinary
  character, an unset field matches all, and a set one refuses another value.
- `TestALiteralMatchesItselfAlone`: `Literal("dev*")` matches `dev*` and not `dev-eks`, and a
  context holding `?` or `\` matches itself.
- `TestProdNamespaceDeletionIsDeniedByEitherVerb`: `delete` and `deletecollection` of
  `namespaces` in `prod-eu`, under a user's `*prod*` entry of `Ask` and of `Auto`, are `Denied`
  naming a shipped rule.
- `TestNoPromptsTurnsAPromptIntoADenial`, and leaves an `Allowed` alone.
- `TestARuleRoundTripsThroughJSON`: a rule's JSON uses the lower-case keys, and `shipped` in a
  file is not read.
- `TestARuleMatchesByGroupAndSubresource`: a rule for core `pods` does not match `pods` of
  another group; a `Kind` of `deployments` matches `deployments/scale`, and one of
  `deployments/scale` does not match `deployments`.

**`securityconfig`**

- `TestProdIsReadOnlyByDefault`: `prod-eu` and an EKS ARN ending `cluster/prod-eu` are
  `ReadOnly` with source `prod`; a user's entry for one replaces it, and Settings' source says so.
- `TestModesMatchInOrder`.
- `TestABadModeIsReadOnly`: a `defaultMode` of `readonly` answers `ReadOnly`; a `modes` entry
  with an unknown mode makes every context `ReadOnly`; each is listed by `Refused()`, and a
  write that does not touch the field keeps the file's raw value.
- `TestABadRuleRefusesEveryClusterWrite`: a file with a `Deny` and a rule of unknown class
  answers, through `Rules()`, the `Deny`, no `Allow`, and the class 4 `Deny`; `Get()` still holds
  the `Allow`; a repeated `id`, the id `refused`, a rule of class 1 or 2, and an `Allow` of class
  5 are refused the same way, and `permissionRuleAdd` refuses each with its reason.
- `TestAHeldListRefusesItsEdits`: while `modes` or `rules` is held, `ModeFor` answers `ReadOnly`
  with source `refused` for every context, and the per-element mutations, and a rule written as
  step 4B's *Always* answer writes one, answer `ErrHeld` and write nothing;
  `permissionDiscardRefused` writes the elements that passed, ends the hold, and the next
  `ModeFor` reads them; a refused `defaultMode` is fixed by setting the default mode.
- `TestAHeldFieldRefusesAnUnnamedChange` (`store_test.go`): an `Update` that changes a held
  field without naming it answers `ErrHeld` and writes nothing; one that names it writes and ends
  the hold; one that leaves the field alone writes and keeps it.
- `TestAModeSetInSettingsWinsOverAPattern`: with a hand-written `{"*", Auto}` entry,
  `permissionModeSet("prod-eu", Ask)` puts its entry first and `prod-eu` answers `Ask`, the
  others `Auto`; setting it again replaces the entry rather than adding one.
- `TestASettingsModeLiftsTheShippedRules`: `ShippedFor("prod-eu")` holds both rules, none once
  Settings set `prod-eu`'s mode, both again with only a `*` or `prod*` entry, and both while
  `modes` is held.

**`clustercard`**: `TestScopeContextIsTheContextUncut`: a context past the card's limit comes
back whole, and a record naming none answers `""`.

**`chatsvc`**

- `TestAChatsGrantsGoWithTheChat`: rows are read live and cascade on delete, and a row that
  does not decode refuses the chat's cluster writes.
- `TestTheSessionCarriesTheContextsMode`: the session's `Mode` answers `ReadOnly` for `prod-eu`
  and `Ask` for `dev`, a mode changed in the file reaches the running turn's next write, and
  `Rules` holds the shipped rules for `prod-eu` alone.
- `TestARecordedWriteNeedsNoWait`: `Record` writes an `allowed` and a `refused` row against the
  open call with its reason, and the run stays `Streaming`.

**`kubeproxy`**

- `TestEveryRequestIsClassified`: a table over the rows of §5, the class 5 list included, a
  write of `ephemeralcontainers`, of a webhook configuration and of a CSR's `approval`, a
  `DELETE` of a custom resource whose plural is `namespaces` as class 4, a `DELETE` with
  `dryRun=All` as a write, a `DELETE` with no name as `deletecollection`, a dry run on a
  Deployment and on a Secret as class 1, a scale to 0 as a merge patch, a strategic patch and an
  apply, and a scale to 1 as class 4.
- `TestAScaleToZeroIsClassFiveInEveryPatchForm`: a JSON Patch `replace` and `add` of
  `/spec/replicas` to 0, a `replace` of `/spec` holding `replicas: 0`, a `move` and a `copy` onto
  `/spec/replicas`, each as class 5, on the resource and on `scale`; a JSON Patch to 1 as class
  4; a body that does not decode, and a `replicas` that is not a number, as class 5.
- `TestASummaryReadsAsTheNoteSays`: *Delete pod `api-7f9c` in `team-a` on `dev-eks`*, the
  cluster-scoped and unnamed forms, and a custom resource by its plural.
- `TestAnAllowedWriteForwardsUnasked`, and is recorded `allowed` with its reason before it is
  forwarded; a record the store refuses forwards nothing.
- `TestADeniedWriteIsAForbiddenStatus` naming the mode, and one naming the rule; each recorded
  `refused`; one whose record the store refuses is still the same `Status`, and nothing forwards.
- `TestDecideRunsUnderTheWriteLock`: an allowed write behind a pending one waits for it, so the
  cluster sees them in the order they were decided.
- `TestAReadOnlyContextRefusesEveryWrite`, class 5 and RBAC included, and still answers a
  Secret read redacted.
- `TestTheModeIsReadForTheGrantsContext`: the proxy calls `Mode` and `Rules` with the context
  the grant was made for, whatever the chat's cluster says afterwards.
- `TestADryRunIsARead`: it forwards unasked in every mode, and on `secrets` its answer is
  redacted.
- `TestABackgroundWriteIsRefused`: a grant with no asker refuses a write under `Auto` and under
  an `Allow` rule, with today's message, and records nothing.

**Webview** (`permission-settings.test.tsx`, `chat-transcript.test.tsx`)

- The modes picker and its line on what a mode governs, the contexts' effective modes with the
  *read-only by default* line and a hand-written entry's pattern, the rules with Remove, the
  shipped ones and the class 5 list without, the add form's fields per provider, `k8s`'s verb,
  group and resource among them, the override's confirm on a `*prod*` context, each mutation,
  the default-mode picker calling `permissionDefaultModeSet`, a held field's refused values with
  its edits disabled and the discard behind its confirm, and nothing while `sandbox.available`
  is false.
- A write tagged `allowed` or `refused` draws its reason, and a dry run's line says *(dry run)*.

## Security

This is the security change the note's second decision names: for a classified cluster write,
a rule or the `Auto` mode can let a change run with no one asked, which the bash tool record
forbade ("no setting, no allowlist"). What holds it: the sandbox is still the floor, so a
command reaches the cluster only through the proxy, which classifies mechanically; class 5
is never allowed by a mode or a rule (it asks, or is refused in a read-only context); the
shipped rules cannot be removed, and only a mode the user sets for one context in Settings lifts
them, for that context alone; a rule is scoped, written by the
user, and on screen in Settings; every write that ran unasked is recorded before it is forwarded
and drawn with its reason, and a write that cannot be recorded does not run; a background
command's write is refused, since it has no record to land in; a value of the file Kstack cannot
read refuses rather than allows. A raw command outside the sandbox still asks every time.

Residuals: an `Allow` rule's scope is a context and a namespace, and a write in that scope can
reach past it (a Pod that mounts a Secret and prints it, as the sandboxed-bash record says); a
dry run's body reaches admission webhooks unasked (decision 2); a class 5 list is a list, and a
destructive write it does not name is class 4, a privileged or `hostPath` Pod included, which
`Auto` or an `Allow` rule runs unasked; the production default matches a context's name,
so a production cluster whose context does not say `prod` gets the default mode until the user
sets one; a mode binds sandboxed commands alone, and a chat switched outside the sandbox can
change a read-only context once the user approves the command.

The record, `docs/security/<date>-the-permissions-engine.md`, argues this and supersedes the
consent paragraph of [the bash tool](../../security/2026-09-18-bash-tool.md) for classified
actions.

## When it lands

- **The security record** above, and an ADR: permissions are classes, modes and rules decided
  at the proxy; three modes; a chat's rules are rows and the always rules are settings; a dry
  run is a read; `*prod*` and a `*` that crosses `/`; class 4 covers class 5; background writes
  stay refused.
- **`security-model.md`**: the cluster-write rows say a write runs, asks or is refused by
  `Decide`, with the tests; a row for the shipped rules; the *Consent* line of the bash tool
  record's row amended for classified actions.
- **`sidecar/CLAUDE.md`**: `permissions`, the classifier, the write path's order, the session's
  policy fields, `chat_grants`, `securityconfig`'s modes and rules.
- **Root `CLAUDE.md`**, *Chat* and the Settings dialog: the Permissions section, the new tags,
  and the cluster write request's *(dry run)* heading, which no request reaches once a dry run
  runs unasked: it moves to the disclosure line (§7).
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` against a kind cluster with two contexts, `dev` and `prod-test`: on a
`dev` chat, `kubectl delete pod x` asks as today; on a `prod-test` chat it comes back `Forbidden`
naming the read-only mode, and `kubectl get secret x -o yaml` reads `[redacted]`; add an always rule
allowing cluster writes in `dev` / `default` in Settings, ask again on `dev`, and read the pod go
with no request and the write tagged `allowed` in the disclosure; ask for `kubectl delete ns test`
on `dev` and read it ask, rule or not, and on `prod-test` read it refused naming a shipped rule
until Settings sets `prod-test` to Ask, after which it asks; `kubectl apply --dry-run=server` on
`prod-test` runs with no request. Then edit `security.json` by hand to give a rule a class of `9`,
restart, and read Settings name the refusal with Add and Remove disabled, and `kubectl delete pod x`
on `dev` come back `Forbidden`; discard it, and read the `Allow` rule hold again and the file keep
it.
