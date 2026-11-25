# Target-neutral permissions

Answers [#1196](https://github.com/kstackhq/kstack/issues/1196). One engine decides every
sandboxed command's request, whatever it reaches. A classifier per target reduces a request to one
`permissions.Action`; the engine, the rules, the storage and the request the user sees never branch
on the target.

Paths are relative to `sidecar/internal/` unless rooted elsewhere.

## What changes for the user

- A cloud platform's request is decided like a cluster's: a mode and rules reach it, and an answer
  may be *for this command*, *for this chat* or *always*.
- A change to who may do what (a Role, an admission webhook) is its own access level,
  `permissions`. Inside a namespace a rule may allow it; across a cluster or an account it asks
  each time.
- A rule may name who acts: the chat, a subagent, the monitor or the console.
- An operation a classifier does not name asks each time and no rule allows it.
- Under `auto`, a cloud platform's data read (an object's contents, a connection string) runs
  unasked, as a Secret read does on a cluster.
- A namespace's rule covers what is in the namespace, never the Namespace object. A write to a
  Namespace object asks each time: no rule names one object, and a rule over every Namespace would
  be wider than today's.

## The action

`run/permissions.Action` is one classified request:

| Field | JSON | Holds |
| --- | --- | --- |
| `Principal` | `principal` | who sent it |
| `Target` | `target` | `kubernetes`, `aws`, `azure`, `gcp`, `github` |
| `Scope` | `scope` | a path of `/`-separated segments; the first is the **account** |
| `AccountName` | `accountName` | the account in the user's words, for a line; never matched |
| `Service` | `service` | the API group (`""` for core), the AWS service, the provider namespace |
| `Operation` | `operation` | the verb or the operation's name |
| `Resource` | `resource` | the resource's type, a path of segments |
| `Name` | `name` | the object's name; never matched |
| `AccessLevel` | `accessLevel` | one of the six, by name |
| `Confined` | `confined` | the scope bounds what a `permissions` action can grant |
| `Once` | `once` | the classifier holds it to asking each time |
| `Summary` | `summary` | one line, written by the classifier |
| `DryRun` | `dryRun` | a dry run, which no rule names |

`Target`'s zero value is no target. `permissions.Kubernetes` (`"kubernetes"`) replaces `Cluster`.
No segment of a scope or a resource holds `/`: a classifier that would write one refuses the
request.

### The account

The account is what a mode is keyed by and what a rule that allows must name.

| Target | Account | Rest of the scope | `AccountName` |
| --- | --- | --- | --- |
| `kubernetes` | the cluster's record id (`apimeta.ClusterID`) | the namespace; none for a cluster-scoped object | the kube-context |
| `aws` | the account id the credentials carry (`aws.Credentials.AccountID`) | the region | the account id |
| `azure` | the subscription id | the resource group | the subscription id |
| `gcp` | the project | the location, when the path names one | the project |
| `github` | the owner | the repository | the owner |

An action whose classifier reads no account it can trust has scope `""`. It takes its target's
default mode, its limit is never above `Once` (below), and no answer may write a rule for it.
**A scope is canonical**: one object has one scope, in lower case where the target ignores case. A
request that names its account another way (GitHub's `/repositories/{id}`, a Google project by
number) or names a resource outside its account has scope `""`. A Kubernetes mode and rule go with the cluster's record, as they
do today. A cloud account has no record: its mode and rules stay until the user removes them.

### Principals

`permissions.Principal` is `chat`, `subagent`, `monitor` or `console`. `session.Kind` is that type
(`type Kind = permissions.Principal`), so there is one list. The proxy stamps the grant's session
kind on every action. Four kinds are enough for now; the column stores a name, so a finer principal
is no table rebuild.

### Access levels

`permissions.AccessLevel` is a string, stored and served by name. It replaces `Class`.

| Level | Covers |
| --- | --- |
| `read` | metadata, and what changes nothing |
| `data-read` | Secret data, an object's contents, key material |
| `write` | a change that can be undone |
| `permissions` | who may do what, and what sees every request |
| `destructive` | loss that cannot be undone, or a broad outage |
| `reach` | exec, tunnels, credential minting |

An action has exactly one, the first of `reach`, `destructive`, `permissions`, `data-read`, `write`,
`read` that applies. `covers` is data: `write` covers `write`, `destructive` and `permissions`;
every other level covers itself. A `deny` or `ask` rule matches the levels its own covers. An
`allow` rule matches its own level alone.

## The verdict

`Policy.Authorize(act)` answers a `Verdict` and the reason in the user's words.

```go
type Verdict struct {
	Effect    Effect // Allow, Ask or Deny
	Grantable bool   // an Ask an answer may write a rule for
}
```

`Effect` is `allow`, `ask` or `deny`: the constants `Allow`, `Ask` (today's `AskFor`) and `Deny`.
The modes become `ModeReadOnly`, `ModeAsk` and `ModeAuto`, so `Ask` names one thing. `Verdict.Outcome()` maps them to
`Allowed`, `Prompted`, `Denied`. `Permit`, `Forbid`, `Refuse` and `Unmatched` go.

### Layer 1: the boundary

`boundary` is a table from principal and access level to a `Limit`, Kstack's and no setting:

| Limit | Means |
| --- | --- |
| `Free` | the mode and the rules decide |
| `Scoped` | `Free` for a `Confined` action, else `Once` |
| `Once` | no rule or mode allows it: it asks each time |
| `Never` | denied |

| Principal | `read` | `data-read` | `write` | `permissions` | `destructive` | `reach` |
| --- | --- | --- | --- | --- | --- | --- |
| `chat`, `subagent` | `Free` | `Free` | `Free` | `Scoped` | `Once` | `Never` |
| `monitor`, `console` | `Free` | `Never` | `Never` | `Never` | `Never` | `Never` |

`asks` is a second table: `chat` and `subagent` ask, `monitor` and `console` do not. A principal
that does not ask turns every `ask` into a `deny`. A principal or level with no row is `Never`.
These tables replace `NoPrompts` and `NoSecretData` on `session.Session` and `permissions.Policy`.
The limits order `Free`, `Scoped`, `Once`, `Never`, each more restrictive than the one before. An
action marked `Once`, and one whose scope is `""`, takes the more restrictive of its level's limit
and `Once`.

Only a classifier sets `Confined`, and only where the scope bounds what the change can grant: a
Kubernetes `permissions` write in a namespace, and an Azure role assignment in a resource group.
Every other cloud `permissions` action asks each time.

### Layer 2: the mode

`Policy.Mode` is the account's: `read-only`, `ask` or `auto`. `modes` is a table from mode and
access level to a default effect:

| Mode | `read` | `data-read` | `write` | `permissions` | `destructive` | `reach` |
| --- | --- | --- | --- | --- | --- | --- |
| `read-only` | allow | ask | deny | deny | deny | deny |
| `ask` | allow | ask | ask | ask | ask | deny |
| `auto` | allow | allow | allow | ask | ask | deny |

A test fails on a mode and level with no row.

### Layer 3: the rules

```go
type Rule struct {
	ID          string
	Effect      Effect
	AccessLevel AccessLevel
	Principal   string // a Principal's name, or "*"
	Target      string // a Target's name, or "*"
	Scope       string // a path pattern
	Service     string // a pattern
	Operation   string // a pattern
	Resource    string // a path pattern
	Command     bool   // one command's, never stored
}
```

A rule matches an action when its level covers the action's (above), its principal and target are
the action's or `*`, and each pattern matches.

### Patterns

- `Match(pattern, value)` stays: `*` any run of characters, `?` one, `\` escapes the next.
- `MatchPath(pattern, value)` is new, for `Scope` and `Resource`. A pattern that is `*` alone
  matches any value. Any other matches when it has as many segments as the value and each segment
  `Match`es its own. So `*` never crosses `/`: `c1` is cluster `c1`'s cluster-scoped objects alone,
  `c1/team-a` one namespace, `c1/*` every namespace, `*/*` every namespace of every cluster. A last
  segment of `**` matches no segment or any number: `c1/**` is `c1` and everything under it. `**`
  anywhere else is refused by `CheckRule`.
- No empty pattern is a wildcard. `Service` `""` matches the core group alone. A stored rule's
  `Scope`, `Operation` and `Resource` are never empty.
- `Literal` stays, and every pattern Kstack writes from a value goes through it, segment by
  segment for a path.

`ClusterScope`, `Inside`, `matchNamespace` and `matchKind` go. A resource pattern of `deployments`
no longer covers `deployments/scale`: a rule names a subresource to cover it, or `*`.

### The order

`verdict` takes the first of these that applies. Which rules match decides it, whatever their
order; rule order picks the reason alone.

1. The boundary's limit is `Never`: **deny**, *it is never allowed*.
2. A `deny` rule matches: **deny**, naming the rule's line.
3. The mode's effect is `deny`: **deny**, *this account is read-only*.
4. An `ask` rule matches: **ask**, not `Grantable`.
5. The limit is `Once`: **ask**, *it always asks*, not `Grantable`.
6. An `allow` rule matches: **allow**, naming the rule's line.
7. The mode's effect is `allow`: **allow**, naming the mode.
8. Otherwise **ask**, `Grantable` unless it is a dry run.

Then, in `Authorize`:

- A principal that does not ask turns `ask` into `deny`, *this session never asks*.
- **A subagent is never wider than its parent.** A `subagent` action's verdict is the more
  restrictive of its own and the same action's as `chat`. Verdicts order deny, ask not grantable,
  ask grantable, allow. Only a chat spawns a subagent; `session.Narrow` of a parent that is not a
  chat keeps the parent's kind, so a narrowed monitor stays a monitor.

A `read` action is forwarded without `Authorize`, unrecorded, as a read is today.

A run with nobody to ask (a background command, a grant with no asker) refuses every request that
is not a read before any queue, as it does today. That stays in the proxies.

### What an answer writes

- `GrantRule(act)`: `allow`, the action's level and target, principal `*`, scope the action's as a
  literal, and `*` for service, operation and resource. An action whose scope is one segment also
  names its service and resource, since a rule over a whole account's objects would be too wide.
- `CommandRule(act)`: `GrantRule`'s, naming service, operation and resource as literals, with
  `Command` set.
- **An answer's rule names principal `*`.** The user approved the action, whoever sent it, so a
  chat's grant reaches its subagents as it does today. A rule that narrows one principal is written
  in Settings.
- `Refused` is `deny`, level `write`, every other field `*`. `RefusedData` is the same at level
  `data-read`, which `write` does not cover. `Unread()` is both. A stored row `CheckRule` refuses
  reads as both in its place, logged by id, so the checks the table's `CHECK`s make today run on
  every read.
- `Rule.Line(account string)` is the rule in words; the caller names the account of a rule whose
  first scope segment is a literal. Shape: `<Effect> <what> [by <principal>] <where>`.
  - `Allow writes in dev-eks / team-a`
  - `Deny destructive delete of core namespaces in dev-eks`
  - `Ask for writes by subagent everywhere`
  - `Allow writes in AWS 123456789012 / eu-west-1`
  - `Allow patch of apps deployments in dev-eks / team-a for this command`

  A non-Kubernetes rule names its target before the account (`AWS`, `Azure`, `Google Cloud`,
  `GitHub`). `fieldWords`' quoting stays, so ` / ` outside quotes separates scope segments.

## Storage

`appdb/migrations/0006_settings.sql` has not shipped (`desktop/v0.0.1` predates it), so it is
edited in place and its checksum in `shippedMigrations` updated. A development database that ran
the earlier `0006` is deleted with `make clean-data`.

```sql
CREATE TABLE permission_rules (
  id           TEXT    PRIMARY KEY CHECK (id <> ''),
  chat_id      TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  effect       TEXT    NOT NULL,
  access_level TEXT    NOT NULL,
  principal    TEXT    NOT NULL,
  target       TEXT    NOT NULL,
  scope        TEXT    NOT NULL,
  service      TEXT    NOT NULL,
  operation    TEXT    NOT NULL,
  resource     TEXT    NOT NULL,
  created_at   INTEGER NOT NULL
) STRICT;
CREATE INDEX permission_rules_chat_idx ON permission_rules (chat_id, created_at, id);

CREATE TABLE permission_modes (
  target     TEXT    NOT NULL,
  account    TEXT    NOT NULL,
  mode       TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (target, account)
) STRICT;
```

- `kube_rules` and its indexes are not created. The `permissions.approval_mode` entry leaves
  `settings`.
- The schema enumerates no effect, level, principal or target. `settings.CheckRule` refuses on
  write what Go cannot apply, and a read turns a row it cannot read into `Refused`.
- A mode row's account is an account, or `*` for the target's default. A mode Go cannot read is
  `read-only`.
- **A cluster's rows go with it**, by a trigger on `clusters`' delete: its `permission_modes` row
  (`target = 'kubernetes' AND account = OLD.id`) and each `permission_rules` row whose target is
  `kubernetes` and whose scope is `OLD.id` or starts `OLD.id || '/'` (compared with `substr`, never
  `LIKE`).
- **Stored requests are rewritten.** `approvals`' request column holds a `tools.ActionRequest`
  whose `action` `desktop/v0.0.1` wrote with `class`, `context`, `namespace`, `verb`, `group` and
  `kind`. `0006` rewrites each with SQLite's JSON functions: `target`,
  `accessLevel`, `scope`, `accountName`, `service`, `operation` and `resource`; `principal` is `""`. A row whose request is NULL, is not JSON or holds no `action` is left as it
  is. The output for a `desktop/v0.0.1` action, key by key:

  | New key | Value |
  | --- | --- |
  | `target` | kept when the action has one that is not `""`, else `kubernetes` |
  | `accessLevel` | `class` 1 and 2 `read`; 3 and 4 `write`; 5 `destructive`; 6 `data-read`; any other `write` |
  | `scope` | `""` (the row names no cluster record; its `summary` names the namespace) |
  | `accountName` | `context` |
  | `service` | `group`, `core` as `""` |
  | `operation`, `resource` | `verb`, `kind`, as stored |
  | `name`, `summary`, `dryRun` | as stored |
  | `principal` | `""` |
  | `confined`, `once` | `false` |

  `class`, `context`, `namespace`, `verb`, `group`, `kind` and `clusterID` are removed. An action that already
  has `accessLevel` is left as it is.

### `services/settings`

- `CheckRule(r)` refuses, as a `RuleRefusal`: a reserved id; an effect, level, principal or target
  Go does not know; level `read` or `reach`, which no rule decides; an empty scope, operation or
  resource; a `**` that is not a path's last segment; a principal other than `*`, `chat` or `subagent`, since the boundary alone decides the
  monitor and the console; an `allow` of `destructive`; an `allow` naming `subagent`, which the
  parent rule would never let win; and an `allow` whose target is `*` or whose first scope segment
  is not a literal, since a rule for every account may only restrict. Every read of a rule runs it
  too.
- `Rules`, `AddRule`, `RemoveRule` replace the `KubeRule` trio; `RuleColumns`, `ScanRules` and
  `RuleValues` are the one encoding, which `agent/chat` reads and writes a chat's rows through. A
  Kubernetes rule whose first scope segment is a literal names a cluster Settings lists, as
  `AddKubeRule` requires today.
- `ModeFor(ctx, target, account)`: the account's row, else the target's `*` row, else `ask`.
  `Modes(ctx)` is every row. `SetMode(ctx, target, account, mode)` and `ClearMode` write one; a
  Kubernetes account is a listed cluster's id or `*`; a cloud account is a non-empty literal with no
  `/`, or `*`.

## The classifiers

Each classifier fills every field. A table names what it knows. An operation no table names is
level `write` and `Once`, so a `deny` rule and `read-only` stop it as a write, and nothing allows
it. Adding a row to any table is a security change.

### Kubernetes (`run/kubeproxy/classify.go`)

- Scope `<cluster id>` or `<cluster id>/<namespace>`. **A Namespace object sits at cluster scope**:
  its name is no longer its namespace, and a write to one (core `namespaces` and its
  subresources) is `Once`.
- A `permissions` write in a namespace is `Confined`.
- Service is the API group, `""` for core. Operation is the verb. Resource is `p.resource`, with
  `/<subresource>` for a subresource.
- Levels, first match:
  - `reach`: what `policy.go` refuses today (`exec`, `attach`, `portforward`, `proxy`, a
    ServiceAccount's `token`). The handler still answers these before any queue, with today's
    statuses; a new `classifyRefusal` names them `reach` so a test pins the list against the boundary.
  - `destructive`: `deletecollection`; a delete in `deletedHard`; a Namespace's `finalize`; an
    `ephemeralcontainers` write; a replica write `keepsReplicas` cannot read as plain.
  - `permissions`: any write in `writtenHard` (RBAC and admission), and a CSR's `approval`.
  - `data-read`: a `GET` of core `secrets` that is not metadata only.
  - `read`: any other read. A dry run on a group version in `honorsDryRun` is `read` ahead of
    every level but `reach`, as it is today.
  - `write`: the rest.
- `builtin` is a table of the resources and subresources the API server serves itself in
  `honorsDryRun`'s group versions. A write in one of those groups to a resource or subresource the
  table lacks is unnamed. In any other group (a custom resource, an aggregated API) a resource,
  its `status` and its `scale` are named by rule; any other subresource is unnamed.
  `TestBuiltinNamesEveryServedKind` walks `client-go`'s scheme for those group versions, maps each
  kind to its resource with `meta.UnsafeGuessKindToResource`, and fails on one the table lacks or
  an `excluded` list (kinds that are no resource: lists, options, `Status`) does not hold.
  Subresources are the table's own rows, pinned by `TestEveryRequestIsClassified`.
- `Destructive` (the words Settings shows) splits into `AlwaysAsks`, one line per `destructive`
  item, and `Permissions`, one line per `permissions` item.

### The cloud platforms

`runproxy.Request.Action` carries every field, and `Grant.Decide` keeps them all. A read still
skips `Decide`.

**A cloud classifier allows by list.** Each has an `ordinary` table: the changes a mode or a rule
may allow. A change off the table is `Once`, so it asks each time, as every cloud change does
today. The tables start short and grow by security review; what they leave out behaves as it does
now.

| | `aws` | `azure` | `gcp` | `github` |
| --- | --- | --- | --- | --- |
| Scope | `account/region` | `subscription/group`; `subscription` with no group | `project/location`; `project` with none | `owner/repo`; `owner` with none |
| Service | the SigV4 service | the last provider namespace, lower case | the service's table name | `api`, `git` or `graphql` |
| Operation | the operation's name; the method on a REST row | `write` (`PUT`, `PATCH`), `delete`, or the `POST` action's name | the custom method after `:`, else the method, lower case | the method, lower case; `push`; `mutation` |
| Resource | `bucket` or `object` on `s3`; else `""` | the type path, lower case (`virtualmachines/extensions`) | the collection path's type words | the first segment after the repository, else the first segment |

A value the classifier cannot read is `""`, which only a `*` pattern matches.

**A read is classified as it is today**, its foreign checks included, and its scope is never
computed: it skips `Decide`. Every request that reaches `Decide`, a data read and a change alike,
takes its scope by the rules below, the foreign checks included.

**Scopes, canonically.**

- `aws`: the account is `aws.Credentials.AccountID`. A request that reaches `Decide`
  resolves `Identity.Credentials` before the decision and signs its forward with the same value; a failed resolve answers what the
  forward answers for one today. Credentials that carry no account id give
  scope `""`, so that login's changes ask each time, as now. The region segment is `global` on a
  row whose `hostFormat` has no `{region}`. A request is **foreign**, and has scope `""`, when
  today's `foreign` or the bucket check says so, or when a query or form value, or a key or string
  of a JSON body read token by token as `mentions` reads it, holds a run of exactly 12 digits that
  is not the credentials' account. An empty body names nothing, and an S3 object `PUT`'s
  payload is not scanned, since S3 reads no account from it. Any other body that is neither a form
  nor JSON is foreign, and so is a
  request whose `X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id` header holds such a run.
- `azure`: the path's own subscription and group in lower case; a path that names no subscription
  has scope `""`, whatever default the proxy forwards with. A JSON body, read token by token as
  `mentions` reads one, that names `/subscriptions/<another>` gives scope `""`, and so does a body
  that is not JSON.
- `gcp`: the project as an id (`[a-z][a-z0-9-]{4,28}[a-z0-9]`). Any other project segment (a
  number, `-`), a path that names no project (a storage object), and a JSON body, read the same
  way, that names `projects/<another>` or is not JSON, give scope `""`.
- `github`: the owner and the repository in lower case. A route by id (`/repositories/{id}`,
  `/organizations/{id}`) and one that names no owner have scope `""`.

**Levels**, first match:

- `reach`: every request a classifier refuses today as answering a credential or reaching a
  machine. The proxy still refuses these itself, with today's text. A refusal for any other reason
  (a request named twice, a resumable upload, a method no route takes) has no level and stays a
  refusal.
- `destructive`: what is class 5 today, less what moves to `permissions`.
- `permissions`, which today is class 5 or class 4:
  - `aws`: every change on `iam` (its `destroys` rows included), and a change on a row that is not
    REST whose operation's name holds `Policy`, `Permission` or `Grant`.
  - `azure`: a change under `Microsoft.Authorization` (class 5 today). A `roleassignments` change
    whose path names a resource group is `Confined`; nothing else is.
  - `gcp`: a `setIamPolicy` method and a storage IAM `PUT` (class 5 today), and every change on
    the `iam` service.
  - `github`: a change under a repository's or an organization's `collaborators`,
    `outside_collaborators`, `teams`, `members`, `memberships`, `invitations`, `keys`, `hooks`,
    `branches/.../protection`, `rulesets`, `environments`, `organization-roles`,
    `security-managers` or `actions/permissions`, and under `/user/keys`, `/user/gpg_keys` or
    `/user/ssh_signing_keys`.
- `data-read`: what is class 6 today.
- `write`: any other change. It is `Once` unless `ordinary` names it:
  - `aws`:
    - `ec2`: `StartInstances`, `CreateTags`, `CreateVolume`, `CreateSnapshot`.
    - `elasticloadbalancing`: `RegisterTargets`, `AddTags`.
    - `ecr`: `InitiateLayerUpload`, `UploadLayerPart`, `CompleteLayerUpload`, `PutImage`.
    - `logs`: `CreateLogGroup`, `CreateLogStream`, `PutLogEvents`.
    - `s3`: a `PUT` of an object whose query holds no key but `x-id`, and which carries no
      `X-Amz-Acl`, `X-Amz-Grant-*` or `X-Amz-Copy-Source` header.
  - `azure`: a `POST` of `start` or `restart` on `microsoft.compute/virtualmachines`; a `PUT` or
    `PATCH` of `microsoft.resources/tags`.
  - `gcp`: on `compute`, `start`, `reset` and `setLabels` on `instances`.
  - `github`, on the API host under `/repos/{owner}/{repo}`, matched segment by segment and no
    deeper: a `POST` to `issues`, `issues/*/comments`, `issues/*/labels`, `pulls` or
    `pulls/*/comments`, and a `PATCH` of `issues/*` or `pulls/*`. A review, a push and a GraphQL
    mutation are off the table.

No row stops or scales anything: on a cluster, scaling to zero is `destructive`.

A row of `ordinary` that an earlier level also matches takes the earlier level.


### `runproxy`

- `Grant.Decide` stamps the grant's target and its session's kind, then reads
  `session.Policy(ctx, target, account)` plus the grant's command rules. No policy is `read-only`
  with no rules. `allow` is recorded, then forwarded; `deny` is recorded and answered 403; `ask`
  goes to the asker with `Grantable` and the two rule lines.
- A request that cannot be shown is refused before the verdict, as today, so what is recorded is
  what was sent. An allowed data read gives up the grant's lock before its forward, as an approved
  one does.
- `Asker.Ask` answers `(Answer, error)`, an `Answer` holding `Approved` and a
  `permissions.Duration`. A `command` answer adds `CommandRule(act)` to the grant.
- `Request` gains `Grantable`, `CommandRule` and `ChatRule`, as `kubeproxy.Request` has.

## The session and the chat

- `session.Session.Policy` is `func(ctx, target permissions.Target, account string)
  permissions.Policy`. `NoPrompts` and `NoSecretData` go; `TestNarrowKeepsTheParentsIdentity` loses
  their lines, keeps the rest, and gains the kind's.
- `agent/chat`'s `sessionFor` reads the mode with `settings.ModeFor(target, account)` (a failed
  read is `read-only`) and the chat's rules, then the always rules (a failed read is `Unread()`).
  `monitorSession()` and the console's session set `Kind` alone.
- `waiter.decide` offers every duration on any `Grantable` request, whatever its target.
  `writeRule` writes `GrantRule(act)` through `addRule` (a chat's) or `settings.AddRule`.
- `tools/bash`'s `cloudAsker` hands the answer's duration back, and `cloudRequestOf` carries
  `Grantable` and the rule lines.

## The wire (`sidecar/graph/schema.graphqls`)

- `PermissionClass` becomes `PermissionAccessLevel`: `Read`, `DataRead`, `Write`, `Permissions`,
  `Destructive`, `Reach`.
- `PermissionPrincipal`: `Chat`, `Subagent`, `Monitor`, `Console`.
- `PermissionTarget`'s `Cluster` becomes `Kubernetes`. `sandboxCloudAccessSet` refuses it, as it
  refuses `Cluster` today.
- `PermissionAction`: `target`, `summary`, `accessLevel`, `principal` (null for a stored action
  that names none), `scope`, `accountName`, `service`, `operation`, `resource`, `name`, `once`,
  `grantable`, `commandRule`, `chatRule`. `context`, `namespace`, `verb`, `group`, `kind` and
  `class` go.
- `KubeRule` becomes `PermissionRule`: `id`, `effect`, `accessLevel`, `principal` (null for every
  principal), `target` (null for every target), `scope`, `service`, `operation`, `resource`,
  `line`. `PermissionRuleInput` has the same fields but `id` and `line`; `scope`, `operation` and
  `resource` default to `"*"`, `service` to `"*"`.
- `PermissionSettings`: `targets: [PermissionTargetModes!]!`, `rules: [PermissionRule!]!`,
  `alwaysAsks: [String!]!`, `permissions: [String!]!`.
  - `PermissionTargetModes`: `target`, `defaultMode`, `accounts: [PermissionAccountMode!]!`.
  - `PermissionAccountMode`: `account`, `name`, `clusterID` (set on Kubernetes, for the join onto
    `clustersWatch`), `mode`, `source`. Kubernetes lists every cluster with a kube-context; a cloud
    platform lists the accounts with a row.
- Mutations: `permissionDefaultModeSet(target, mode)`, `permissionModeSet(target, account, mode)`,
  `permissionModeClear(target, account)`, `permissionRuleAdd(input)`, `permissionRuleRemove(id)`,
  `chatPermissionRuleRemove(chatID, id)`; query `chatPermissionRules(chatID)`. They replace the
  `kubeRule*` and cluster-keyed mode operations.
- A rule's scope is built by the sidecar's caller as text: the webview writes
  `<account>/<pattern>` from an account's `account` string, which it treats as opaque, through one
  helper that escapes it as `Literal` does (`src/lib/permission-pattern.ts`).

## The webview

- `src/lib/permission-settings.tsx`, `chat-grants.tsx` and `chats.tsx` move to the new operations
  and types; `pnpm codegen` regenerates `src/gql/`.
- Settings › Permissions draws a section per target: its default mode, then a mode per account.
  Kubernetes lists its clusters as today. A cloud platform's section lists the accounts with a mode
  and has one form to add an account by id. A section for a cloud platform shows only on a machine
  that offers it (`sandboxCloudAccess`).
- The rule form gains a target, a principal (*anyone* by default) and the neutral fields; a
  Kubernetes rule picks its cluster and namespace as today and the helper builds the scope:
  a cluster and no namespace is `<account>/**`, a cluster and *cluster-scoped objects* is
  `<account>`, a cluster and a namespace pattern is `<account>/<pattern>`, and no cluster is `*` or
  `*/<pattern>`. A cloud platform's rule takes its account and the rest of the scope the same way. The
  access levels a rule may name are `DataRead`, `Write`, `Permissions` and `Destructive`.
- *Always asks* lists `alwaysAsks`; a second list, *Asks unless a rule inside a namespace allows
  it*, lists `permissions`.
- A cloud request's card offers the durations its action is `grantable` for, as a cluster write's
  does. Every string of an action or a rule is drawn through `VisibleText`.

## Tasks

In order; each is red, then green.

1. `permissions`: `AccessLevel`, `Principal`, `Target`, `covers`, `MatchPath`, the neutral `Action`
   and `Rule`, `Rule.Matches`.
2. `permissions`: `boundary`, `asks`, `modes`, `Verdict`, `verdict`, `Authorize`, the subagent
   rule.
3. `permissions`: `Grantable` (on the verdict), `GrantRule`, `CommandRule`, `Refused`, `Unread`,
   `Rule.Line`.
4. `session`: the policy reader's signature, `Kind` as `Principal`, the two flags gone.
5. `appdb`: `0006` in the new shape, the trigger, the request rewrite.
6. `services/settings`: rules and modes over the new tables.
7. `kubeproxy`: the classifier, `builtin`, the grant's policy read.
8. `runproxy`: `Decide`, `Asker`, the request's grant fields.
9. `awsproxy`, `azureproxy`, `gcpproxy`, `githubproxy`: every field, canonical scopes, `permissions`, `ordinary`.
10. `tools/bash` and `agent/chat`: the askers, `sessionFor`, the answer's rule, the chat's rules.
11. `graph`: the schema, `gqlgen generate`, the resolvers.
12. The webview: codegen, the libraries, Settings, the request card.
13. The documents (*When it lands*).

## Acceptance

Each is a test, named here or by the task that writes it.

- `TestTheVerdictMatchesTheOrderedTable` and `TestAVerdictIsOrderIndependent` hold over the new
  engine, every principal, level, mode and limit included.
- `TestEveryModeHasEveryLevel` and `TestEveryPrincipalHasEveryLevel` fail on a missing row.
- `TestTheMonitorNeverWritesOrReadsData`, `TestTheConsoleNeverAsks`: the two sessions' limits are
  rows of the boundary.
- `TestASubagentIsNeverWiderThanItsParent`: over every action and policy of the ordered table, a
  subagent's verdict is never weaker than the chat's.
- `TestAnUnnamedOperationAsksOnceAndIsNotGranted`, in `permissions` and once per classifier.
- `TestAnActionWithNoAccountAsksOnce`; per cloud proxy, `TestAnAliasedOrForeignScopeIsEmpty`
  (a route by id, a project number, `projects/-`, another account's id in a body, another
  subscription or project in a body); `TestAScopeIsLowerCase`.
- `TestAWriteToANamespaceObjectAsksOnce`.
- `TestOnlyAConfinedPermissionsActionIsGrantable`, with an AWS `iam` change and a Google
  `setIamPolicy` asking once under every mode and rule.
- Per cloud proxy, `TestAChangeOffTheOrdinaryTableAsksOnce` (S3 `PUT ?policy`, an EKS access
  entry, a CloudFormation `CreateStack`, an ARM deployment, a GitHub `transfer`, a pull request review, a push, and each cloud's stop
  and scale operations) and
  `TestOrdinaryIsExactlyTheTable`, which pins the table's rows.
- `TestAGlobalAWSServiceHasOneScope`: a Route 53 `DELETE` with no body, signed in any region,
  has scope `account/global`.
- `TestReachIsNeverAllowed`: no mode or rule allows a `reach` action, and each proxy's refused
  list classifies as `reach`.
- `TestAnAllowRuleMatchesItsOwnLevelAlone`, `TestAWriteRuleCoversDestructiveAndPermissions`.
- `TestAPermissionsWriteIsGrantedInsideAScopeAlone`: a Role in a namespace is grantable, a
  ClusterRole asks once.
- `TestNarrowOfAMonitorStaysAMonitor`.
- `TestANamespacesRuleDoesNotCoverTheNamespaceObject`.
- `TestAScopePatternNeverCrossesASegment`, `TestATrailingDoubleStarCoversTheAccountAndBelow`; the
  rule form's test pins the scope each choice writes.
- `TestAReadIsClassifiedAsToday`, per cloud proxy: a GraphQL query, a read by a login with no
  account id and a tenant-level Azure read forward unasked.
- `TestAnS3PutWithAnACLOrACopySourceAsksOnce`, `TestOnlyARoleAssignmentInAGroupIsConfined`.
- `TestARuleAndAModeDecideAnAWSAction`, and the same for `azure`, `gcp` and `github`: under `auto`
  a write forwards unasked and is recorded; under `read-only` it is denied; an `allow` rule
  forwards it; a `deny` rule refuses it; a `command` answer allows the command's next one.
  A data read in the account forwards unasked under `auto` and is recorded; one that names another
  account (an ARN in a JSON body, a bucket the login does not own) asks once.
- `TestNoProxyDropsAFieldBeforeTheDecision`, per proxy: the recorded action holds every field.
- `TestMigration0006AddsTheSettingsTables` reads the new tables, the trigger's deletes and a
  rewritten `desktop/v0.0.1` request back; `TestAShippedMigrationIsNeverEdited` holds with the new
  checksum.
- `TestARowGoCannotReadDenies`, `TestTheGoCheckRefusesWhatNoRuleMayHold`.
- `TestNarrowKeepsTheParentsIdentity` holds.
- The webview's `permissions.test.tsx`, `chat-grants.test.tsx` and the transcript's request tests
  hold over the new types, with a cloud request offering *Always*.

Verification: `make check-changed`, `cd sidecar && go tool gqlgen generate`, `pnpm codegen`,
`pnpm build`.

## The open questions, answered

- **Which principal an answer's rule names:** `*`.
- **A principal finer than a kind:** no; four kinds.
- **The Namespace object:** it sits at cluster scope, and a namespace's rule does not cover it.
  Deleting one is `destructive`, which asks each time whatever the rules.
- **The `permissions` boundary on a cloud platform:** `Scoped`, which is `Once` unless the
  classifier marks the action `Confined`: an Azure role assignment in a resource group, nothing
  else yet.
- **What the first scope segment names:** the account, per target (above). A cloud account has no
  record.
- **An unnamed operation under `auto`:** it asks once. It is a `write`, so a `deny` rule and
  `read-only` stop it. On a cloud platform that is every change off `ordinary`.

## Not in scope

- What the issue lists: a policy language, more than one level per action, a Cloudflare proxy, a
  user or team as a principal, sandbox reads and writes, folder grants.
- Pinning a cloud classifier's table against its vendor's published model (AWS's access levels,
  Azure's `actions`, Google's and GitHub's API descriptions), and growing `ordinary`. Each table is
  pinned by a test over its own rows; the vendor pin is its own issue, filed when this lands.
- Learning the account of AWS credentials that carry none (static keys). Those logins ask for each
  change, as today.
- A separate spec PR and `wip/` branch. The maintainer asked for the spec and its implementation in
  one pull request, so the ADR the issue wants beside the spec lands in that pull request (*When it
  lands*), and the spec is deleted in it.
- A record per cloud account, and a list of the accounts a login can reach.
- A change to what a background command or a grant with no asker may send.

## When it lands

- ADR *permissions are access levels, a boundary, modes and rules over one action*, superseding
  [permissions are classes, modes and rules](../adr/2026-10-02-permissions-are-classes-modes-and-rules-decided-at-the-proxy.md)
  and the rule half of
  [a mode and a rule belong to a cluster](../adr/2026-11-24-a-folder-grant-is-its-own-row-and-a-mode-and-a-rule-belong-to-a-cluster.md);
  every `CLAUDE.md` link is repointed.
- A security record under `docs/security/`: a rule and a mode now allow a cloud platform's request,
  and an answer may grant one; a cloud data read runs unasked under `auto`; that a
  namespace's `permissions` rule reaches `kube-system`'s Roles; that a write to a Namespace
  object, a create included, asks each time; and what `ordinary` rests on the user's
  mode for (a tag can change an attribute-based policy's answer, and a comment
  can trigger automation that reads it).
- `docs/security-model.md`: the cluster row, the cloud rows and the *no rule decides a cloud
  platform's action* sentence move to what ships, with the tests above.
- `docs/persisted-data.md`: the `kube_rules` row becomes `permission_rules`, `permission_modes`
  gains a row, the settings row loses `permissions.approval_mode`, and the JSON columns row names
  the action's keys.
- `sidecar/internal/run/CLAUDE.md`, `services/settings/CLAUDE.md`, `agent/CLAUDE.md`,
  `tools/CLAUDE.md` and `src/CLAUDE.md` state the new shape.
- The vendor-pin issue is filed. This spec is deleted.
