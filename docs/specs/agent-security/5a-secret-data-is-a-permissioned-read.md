---
title: Secret data is a permissioned read
scope: sidecar, webview
status: Planned
---

# Secret data is a permissioned read

**Needs:** step 3B, whose classifier tags a Secret read class 6; step 3C, whose `Authorize`
gives it a verdict; and step 4B, whose request and five answers this step's prompt rides.
**Unblocks:** step 6B, whose monitor session this step's `NoPrompts` and `NoSecretData` are
written for.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today every sandboxed read of core `secrets` is redacted, always: a value is `[redacted]`, a
helm release's values and manifest Secrets are rewritten inside, and a write of a helm release
is refused because `helm upgrade` would rebuild it from redacted values. The model has no way
to read a Secret's values but to ask the user to run the command outside the sandbox.

The note's class 6: a read of Secret data is a read that carries write-like risk, so the proxy
redacts by default and treats showing the data as a permissioned action. After this step:

- **The proxy asks `Authorize` for every read of core `secrets` that can carry data** — a get,
  a list, a watch, a table read that asks for the whole object — and acts on the verdict's
  `Outcome`. `Allowed` passes the response unredacted. `Prompted` holds the read while the user
  decides, with step 4B's request — *Show Secret data in team-a on dev-eks* — and the five
  answers. `Denied`, a denial and an abandoned wait pass the response **redacted**, as today,
  with a 200: the note says "redacted unless permitted", never refused.
- **A read that prefers metadata alone** — `kubectl get secrets`' table, a
  `PartialObjectMetadata` list — passes redacted, as today, and asks no one: it has no data to
  show. `kubectl describe secret`, `-o name` and `-o jsonpath` read whole objects, so they ask.
- **A rule for this chat or always is `GrantRule`'s**: `Allow` class 6 in the context and
  namespace the request names. `security.json` accepts a class 6 rule, and Settings adds one.
- **The helm release write refusal is lifted** for a command that read no Secret data redacted
  in the release's namespace: helm rebuilds a release from the Secrets it read, so what it read
  decides. A release body is also decoded and refused when it carries `[redacted]` inside.
- **Every helm command asks in `Ask` mode**, since helm reads a release from its Secrets:
  `list`, `status`, `history` and `get` as much as `upgrade`, and more than once per command.
  One *Allow for this command* covers a command; one *Allow for this chat* covers the chat.
- **`Auto` shows Secret data unasked**, as the note's table has it, and the Settings mode lines
  say what each mode does with it.
- **A session can be one that never asks, and one that never reads Secret data**, whatever its
  rules: the session the monitor (step 6B) runs under. That is how the note's "the monitoring
  session must never receive Secret data" is pinned here, ahead of the session that needs it.

The mirror is untouched: KubeQuery reads `kubestore`, which redacts at write time and holds no
value to show. Nothing changes on Windows: no command there reaches the proxy.

## What is not in this step

- **No monitor.** Step 6B builds the session; this step gives it the two fields and the tests.
- **No audit of what else carries a secret.** The [TODO](../../TODO.md#security) item stands:
  ConfigMaps, env values and the state of tools that hold credentials pass as they do today.
- **No change to the mirror's redaction** (`kubestore/objects.go`), which serves the
  dashboard and KubeQuery.
- **No change to what the redactor rewrites.** A response that is not allowed, a write's
  answer on `secrets` included, is rewritten as today.
- **No change to the record or the gate in `chatsvc`.** `askAction`, `recordAction` and
  `Approve` take any `ActionRequest`; a Secret read's is one more.
- **No rename on the wire.** `ToolCall.clusterWrites` keeps its name and gains the reads
  (decision 2).

## Design

### 1. The read asks `Authorize`

`ServeHTTP` routes a request the policy passed that is not a write, once its body is read,
where `p.onSecrets()` and not `metadataOnly(r)`, to `g.serveSecretRead(w, r, p, body)` in
`kubeproxy/secretread.go`. Every other read goes to `forward` as today.

**`forward` gains a `redact bool`** and applies `rewriteSecrets` when it is set. Every caller
passes `p.onSecrets()` — a write's answer on `secrets` stays redacted — but an allowed Secret
read, which passes `false`.

**`metadataOnly(r)`** is true when the first media type `Accept` names, the one the client
prefers, is `as=Table` or `as=PartialObjectMetadata`/`PartialObjectMetadataList`
(`g=meta.k8s.io`), and the query's `includeObject` is not `Object`. kubectl's `get` without
`-o` sends such a request: two Table types, then `application/json` as a fallback for a server
that serves no Table; `--sort-by` adds `includeObject=Object`, since it sorts on the object.
Getting it wrong costs nothing that matters: the response is redacted either way, so a read
wrongly called metadata-only shows `[redacted]` unasked, and one wrongly called a data read
asks for nothing it shows.

**`serveSecretRead`** decides, then forwards:

```go
redact := true
if g.asker != nil {
	allowed, answered := g.decideSecretRead(w, r, p)
	if answered {
		return
	}
	redact = !allowed
}
g.forward(w, r, p, body, redact)
```

A grant with no asker — a background command, or a runtime with none — forwards redacted and
nothing more: no record can be written, and an unredacted read nobody can see on screen is not
one this step allows.

**`decideSecretRead`** holds the write lock for the decision alone (decision 4): it takes it
through `takeWriteLock`, which answers `w` itself for a full queue or a context that ends
first — the one case `answered` is true — and releases it before it returns, since an allowed
watch streams for as long as it is open. Under the lock:

1. `act := classify(r, p, nil, g.context)`: class 6 (step 3B), `Namespace` off the path (empty
   for a cluster-wide read), `Group` `core`, `Kind` `secrets`, `Name` off the path. **A `GET`'s
   `Verb` is as the API server reads it**: `watch` when `isWatch`, else `get` with a name, else
   `list`; `verbs` keeps `get` for the method and `classify` sets the other two. A write's verbs
   are unchanged. **The summary of a class 6 action** follows `summary`'s shape with its own
   head: *Show Secret db-creds in team-a on dev-eks* for a get, *Show Secret data in team-a on
   dev-eks* for a list, *Watch Secret data in team-a on dev-eks* for a watch, and with no
   namespace *Show Secret data on dev-eks*, whose scope is the context alone. No backticks and
   no question mark: the request draws the summary as the heading, as it draws a write's.
2. `v, why := g.policy(r.Context()).Authorize(act)`, the policy the write path reads, which
   carries the session's two flags (§4) and the command's rules. The verdict is taken once, and
   `v.Outcome()` and `permissions.Grantable(v, act)` are both read from it.
3. `req` is a `Request` with `Action: act`, `Grantable` and the rule lines as a write's are
   built, and `Write: &Write{Method: "GET", Path: r.URL.RequestURI(), Subresource:
   p.subresource}`: the method and the path, no body, so the record and the wire carry what
   was read.
4. `Allowed`: `Record`, then `true`. A record that fails answers `false`: the read still
   answers, but never unredacted off the screen.
5. `Denied`: `Record` (a failure is logged, as a refused write's is), then `false`.
6. `Prompted`: `Ask`. An approval answers `true`, and a `Command` answer first appends
   `CommandRule(act)` to `commandRules`, so the same read again in that command passes. Any
   other end — denied, abandoned, an error from the asker — answers `false`.
7. **Before answering `false`, it marks the namespace on the grant**: `redactedReads`, a
   `map[string]bool` of the namespaces this command read Secret data redacted in, `""` for a
   cluster-wide read. The helm gate (§3) reads it under the same lock. A grant with no asker
   marks nothing: its writes are refused anyway.

The response is redacted or not as a whole: the Secret rewriter and the helm release rewriter
are one `rewriteSecrets`, skipped together. A watch that waits holds its connection through
the wait; its status line goes once the decision is in. An approved watch streams unredacted
for as long as it is open, under the one answer; a client that reopens a dropped watch is asked
again unless a rule covers it, which is right, since each watch is a read.

`takeWriteLock`'s 429 says *too many requests are waiting on the user. Send one at a time.*,
since a Secret read now waits in the same queue.

### 2. The rule

A `Chat` or `Always` answer writes `permissions.GrantRule(act)` through step 4B's
`service.Approve`. **For class 6, `GrantRule` names the class, the action's `Context` and its
`Namespace`, each literal, and nothing else**: no `Group`, no `Kind` and not `Inside`. Class 6
is Secret reads alone and no Secret is a Namespace object, so neither of step 4B's reasons to
name the resource applies. A namespaced read's rule allows every Secret read in that namespace;
a cluster-wide read's has no `Namespace` and allows every Secret read in the context. A
`Command` answer adds `CommandRule(act)`, which also names the verb, `core` and `secrets`, for
the rest of the command alone.

`Rule.Line()` draws a class 6 rule with no verb as *Allow Secret reads in dev-eks / team-a* or
*Allow Secret reads in dev-eks*. `scopeLine`'s word for a rule that names a group or kind but
no verb becomes `reads` for class 6, where it is `writes` for the others, so a hand-written
*Allow reads of core secrets in dev-eks* reads as what it does. A command rule reads *Allow list
of core secrets in dev-eks / team-a for this command*.

Class 6 under `Authorize` is step 3B's mode table, which the code already gives it: under
`ReadOnly` and `Ask` it is `Unmatched` and asks, under `Auto` it is `Permit`, and a `Deny` rule
makes it `Refuse`.

**The settings file accepts it.** `securityconfig`'s `ruleClasses` gains `SecretRead`, and
`ruleRefusal` gains a case for it: no `Folder`, not `Inside`, a `Namespace` that is not
`[cluster]` (every Secret is in a namespace, and a cluster-wide read has none, which an unset
`Namespace` already matches), a `Verb` that is empty or one of `get`, `list` and `watch`, a
`Group` that is empty or `core`, and a `Kind` that is empty or `secrets`. The refusal naming
the classes a rule may name adds *6 (Secret reads)*. The note's example,
`k8s:secret-read context=dev-*`, is then a rule Settings can add: `CLASSES` in
`permission-settings.tsx` offers *Secret reads* beside the cluster-write ones, for Allow, Deny
and Ask; while it is picked the form draws the context and namespace fields alone — no verb,
API group, resource or *Cluster-scoped* — and sends the rest empty.

**A file Kstack cannot read still keeps Secret data redacted.** While `rules` is held,
`Store.Rules` drops every `Allow` and appends `permissions.Refused`, a class 4 `Deny`, which
covers classes 4 and 5 and not 6. It appends `permissions.RefusedSecrets` beside it, `{ID:
"refused-secrets", Effect: Deny, Class: SecretRead}`, and `ruleRefusal` refuses that id in the
file as it refuses `Refused`'s. So a held file that may have held a class 6 `Deny` refuses Secret
data under `Auto` too.

### 3. The helm release write

**The gate.** A write of a release — a `PUT`, `PATCH` or `DELETE` that `namesRelease`, or a
`POST` to `secrets` whose body is typed `helm.sh/release.v1` (`isRelease(body)`, `notARelease`
inverted) — is refused when **this command read Secret data redacted in the release's
namespace**: `g.redactedReads[p.namespace] || g.redactedReads[""]` (§1). helm rebuilds a
release from the Secrets it read, so what the command actually read decides, not what its
policy would allow now: a read answered *Once* or *for this command* opens the write as a rule
does, and a read that passed redacted — denied, unrecorded, or made before a rule landed —
shuts it, whatever the policy says by the time of the write. The refusal is `refusedHelm`,
whose text becomes *kstack: helm rebuilds a release from Secret data this command read
redacted. Allow Secret data for this namespace, then run it again.* A write the gate passes
goes on to its own class 4 or 5 decision like any other. This is decision 1.

The gate runs under the write lock, after `checkBody`, where the body is known: the two
refusals it replaces — `namesRelease` before the lock, and `notARelease` inside `checkBody` —
go. A release write with no read before it in the command — a `kubectl apply` of a release
by hand — passes the gate, and the mark check below is what stands between it and the cluster.

**A release is checked inside.** The gate knows what this command read, not what the body
holds: a release read redacted in another command can reach this one through a file, and a
hand-written release can carry the mark. `holdsMark` sees the body as sent, where a release is
gzip under base64, under the Secret's own base64 in `data`, so a mark inside is invisible to it.
So `checkBody`, for a release write with a body, reads the release off it:

- a JSON patch (`application/json-patch+json`) is refused `refusedUnshowable`: its operations
  can set the release in shapes the check does not read, and helm sends none;
- a body that sets `stringData` is refused `refusedUnshowable`: helm writes a release under
  `data` alone, and the check reads one place;
- a body with no `data.release` passes this check: a `DELETE`, or a merge patch that leaves
  the release alone, such as one of its labels;
- `data.release` is decoded with `decodeRelease`, the decoded release read as one JSON value,
  and `holdsMark` run over it: a mark inside is `refusedRedacted`, and a release that does not
  decode `refusedUnshowable`.

Every other body carrying `[redacted]` or its base64 is refused as today.

**What helm does with a refusal.** An upgrade creates the new release first, applies the
resources, then rewrites the old release as superseded and the new one as deployed; helm logs
a failed rewrite and carries on. So the gate's refusal of the first write is what stops an
upgrade, and the mark check alone, reached on the old release's rewrite, leaves the cluster
changed with two releases marked deployed — a history to tidy, never a corrupted value. That is
why the gate reads what the command read and not its policy.

### 4. A session that never asks, and one that never reads Secret data

`session.Session` gains `NoPrompts bool` and `NoSecretData bool`, and `permissions.Policy`
gains the same two fields. **The session is their one source**: `Grant.policy` copies the
session's two onto the policy its `Policy` function answered, and no `Policy` function sets
them. `Authorize` reads them so:

- `NoSecretData`, first, for a class 6 action: `Refuse`, with *this session never reads Secret
  data*, ahead of every rule.
- `NoPrompts`, last: a verdict that would be `Unmatched` or `Forbid` is `Refuse` instead, with
  *this session never asks* before the reason it would have asked. So its outcome is `Denied`,
  never `Prompted`, and nothing is grantable.

Both are read by `Authorize`, so they bind what the proxy decides: a cluster write and a
Secret read. A call's own request (a command outside the sandbox, a `Read` outside the
workspace, a command asking for network) is the tool's gate, which reads neither; step 6B
builds the monitor with no asker, so it has no such call to make.

`chatsvc`'s `sessionFor` leaves both false; `Narrow` copies the parent's, since step 2C classes
them as identity; step 6B sets both true for the monitor. So no rule the user writes in
Settings, and no `Auto` mode, opens Secret data to a session built with `NoSecretData`: the
invariant is `Authorize`'s, not a filter step 6B has to remember.

### 5. The prompt

`tools/bash/prompts/sandbox.md`: a Secret's values read `[redacted]` unless the user allows
showing them, which a read of Secret data asks for — once, for the command, for the chat, or
always; listing Secrets' names asks for nothing; a background command reads them redacted;
`[redacted]` after a request is the user's answer, so do not ask the user to run the command
outside the sandbox for it. helm keeps a release in Secrets, so every helm command asks, and
`upgrade` more than once; a helm change is refused when the command read Secret data redacted,
and its refusal says so. The two lines saying a helm change comes back `Forbidden` and that a
Secret's values are what the sandbox lacks go. The line steering a Secret's change to `kubectl
apply --server-side` stays, with its reason now that a client-side apply first reads the Secret,
which asks, and fails on `[redacted]` if the read is not allowed.

### 6. The wire and the request

**Nothing new rides the wire.** A Secret read is a `ClusterWrite` in `ToolCall.clusterWrites`
whose `method` is `GET`, whose `path` is the path read, and whose body fields are empty; the
schema's doc strings say so: the list is *every request the call's sandboxed command sent that
the proxy decided — a change to the cluster, or a read of Secret data*, and `method` names
`GET` beside the four.

**The request is the cluster-change request as it stands.** `ApprovalRequest`'s `change`
branch draws it with no new code: the summary as the heading through `VisibleText`, no diff, the
path and `GET` open since there is no diff, the command under *Sent by*, and the five buttons
with `commandRule` and `chatRule` off the action — *Allow Secret reads in dev-eks / team-a*, or
*… in dev-eks* for a cluster-wide read. The one change is the group's `aria-label`, *Secret read
awaiting approval* when `change.action.class` is `SecretRead`. In the call's disclosure the read
is a line as a settled write is — `GET /api/v1/namespaces/team-a/secrets/db-creds`, tagged
`approved · this chat`, `denied`, `allowed` or `refused` with its reason — so a read that ran
unredacted is on screen; `ClusterWriteLines`' comment says the list holds Secret reads too. The
model's result is unchanged: the command's output, redacted by `safe.Redact` as every output
is, which is hygiene and not the boundary.

**The mode lines say what a mode does with Secret data**, since `Auto` now shows it unasked.
`MODES` in `permission-settings.tsx` reads: *Read-only* — *Every change to the cluster is
refused. Showing Secret data asks you first.*; *Ask* — *Every change to the cluster, and
showing Secret data, asks you first.*; *Auto* — *Changes run and Secret data is shown without
asking, except what always asks.*

### 7. The seam with step 5B

Step 5B, in the same wave, also edits `chat-transcript.tsx` and `prompts/sandbox.md`, in
different places: its grant offer under a failed command, and its line on a folder the sandbox
hides. The step that lands second keeps the first's lines.

## Decisions this step asks for

1. **Lift the helm release refusal for a command that read no Secret data redacted.** The
   refusal exists because a release rebuilt from redacted values corrupts the release. What
   decides is what the command read, kept on the grant, not the policy: a policy check would
   refuse the upgrade after a read answered *Once* or *for this command*, two of the five
   answers on helm's own request, and would pass one whose read happened to go redacted, where
   the mark check alone comes too late in helm's sequence (§3). The flag closes both, costs a
   map on the grant, and `helm upgrade` in the sandbox is what the note's users expect. The
   release's own mark check stays behind it for a release that arrives from elsewhere.
   Recommended.
2. **Keep `clusterWrites` as the field's name, and document the reads in it.** A rename to
   `clusterActions` reaches the schema, the generated code, the record's JSON key, the
   webview's types and eighteen files, for one `GET`, and invites renaming the stored
   request's `write` key with it. The doc strings and the comments say what the list holds.
   Recommended.
3. **A metadata-only read asks no one.** It carries no data to show, so asking would put a
   request before the user for every `kubectl get secrets` that shows nothing more once
   approved, and a request that is answered by reflex protects nothing. The client's first
   preference decides, since kubectl lists `application/json` after its Table types and a
   server that serves Tables answers with one. A helm read is a full read and still asks, since
   its release holds the values; so do `kubectl describe secret`, `-o name` and `-o jsonpath`,
   which read whole objects though they print none of the data. Recommended.
4. **A Secret read decides under the write lock.** The lock already serializes every decision
   on a grant and guards `commandRules`, and the journal's `askMu` puts one ask to the user at
   a time anyway, so a read waiting behind a write loses nothing. A lock of its own would add a
   second queue, a mutex over the command's rules and a race test. The read releases the lock
   before it forwards, since an allowed watch streams for as long as it is open. Recommended.
5. **The two flags live on the session, and the grant copies them onto the policy.** A
   `Policy` function that could set them would be a second place for a monitor to get them
   wrong; `Authorize` reads them off the policy so one function decides for the write path and
   the read path alike. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `NoPrompts` and `NoSecretData` on the session and the policy; `Authorize` reads them; `GrantRule` and `Line` for class 6; `RefusedSecrets`; `Mode`'s comment says what `Auto` does with class 6 | `session/session.go`, `permissions/permissions.go`, their tests | — | Planned |
| 2 | Class 6 rules in the settings file: `ruleClasses`, `ruleRefusal`, `Store.Rules` | `securityconfig/permissions.go`, its test | 1 | Planned |
| 3 | The class 6 verb and summary; `metadataOnly`; `forward`'s `redact`; `serveSecretRead` and `decideSecretRead`; `redactedReads`; `policy` copies the flags; the 429's text | `kubeproxy/classify.go`, `kubeproxy/secretread.go`, `kubeproxy/kubeproxy.go`, `kubeproxy/write.go`, their tests | 1 | Planned |
| 4 | The helm gate over `redactedReads` and its text; the release checked inside for the mark | `kubeproxy/write.go`, `kubeproxy/policy.go`, their tests | 3 | Planned |
| 5 | The tests pinning that `chatsvc` writes a class 6 grant as any other, and a chat's session carries neither flag | `chatsvc/approval_test.go`, `chatsvc/grants_test.go` | 3 | Planned |
| 6 | The wire's doc strings — `clusterWrites`, `method`, and `PermissionMode`'s line on `Auto` — then `gqlgen generate` and `pnpm codegen` | `sidecar/graph/schema.graphqls`, generated code, `src/gql/` | — | Planned |
| 7 | The `aria-label`; *Secret reads* in the Add form with its fields; the mode lines; the disclosure's comment | `src/components/widgets/chat-transcript.tsx`, `src/components/widgets/permission-settings.tsx`, their tests | 2 | Planned |
| 8 | The prompt | `tools/bash/prompts/sandbox.md`, `tools/bash/bash_test.go` | 4 | Planned |
| 9 | Docs, per *When it lands* | see there | 1–8 | Planned |

**Order:** 1, then 2, 3 and 6 at the same time, then 4 and 5 at the same time, then 7 and 8
at the same time, then 9.

## Tests

**`permissions`**

- `TestNoSecretDataRefusesClassSixAheadOfEveryRule`: with the flag, class 6 is `Refuse` under
  each mode and under an `Allow` rule; class 4 is unchanged.
- `TestNoPromptsRefusesWhatWouldAsk`: with the flag, an `Unmatched` and a `Forbid` verdict each
  become `Refuse`, neither is grantable, and a `Permit` is unchanged.
- `TestASecretReadGrantNamesItsScope`: `GrantRule` of a namespaced Secret read names class 6,
  the context and the namespace alone, not `Inside`; of a cluster-wide list, the context alone;
  `CommandRule` of each adds the verb, `core` and `secrets`.
- `TestAClassSixRuleReadsAsReads`: the lines of a namespaced grant, a cluster-wide grant, a
  command rule, and a hand-written rule naming `core` `secrets` with no verb; none says
  `writes`.

**`securityconfig`**

- `TestAClassSixRuleIsRead`: an `Allow` class 6 rule with a context and a namespace is kept; one
  naming a folder, `Inside`, `[cluster]`, the verb `delete`, another group or another kind is
  refused with its reason; the id `refused-secrets` is refused.
- `TestHeldRulesKeepSecretDataRedacted`: while `rules` is held, `Store.Rules` holds
  `RefusedSecrets`, and under `Auto` a class 6 read is `Refuse`.

**`session`**

- `TestNarrowKeepsNoPromptsAndNoSecretData`, a case of step 2C's
  `TestNarrowKeepsTheParentsIdentity`.

**`kubeproxy`** (the write tests' `fakeAsker` and `recordingAsker` serve the reads too)

- `TestAnAllowedSecretReadPassesUnredacted`: under an `Allow` class 6 rule for the namespace,
  a get answers the values, and the record says `allowed` with the rule and carries `GET` and
  the path; a `PUT` of a Secret under the same rule still answers redacted.
- `TestAPromptedSecretReadWaitsForTheDecision`: under `Ask`, the read holds until the asker
  answers; approved passes unredacted, denied passes `[redacted]` with a 200, and so does an
  asker that errs.
- `TestADeniedSecretReadIsRedactedAndTwoHundred`: under a `Deny` rule, and under `NoPrompts`,
  the values are `[redacted]`, the status 200, the record `refused` naming the reason.
- `TestAClassSixVerbIsTheServers`: a named get is `get`, an unnamed one `list`, `?watch=1` and
  the legacy `/watch/` path `watch`; each summary reads as §1 has it, the cluster-wide one
  naming the context alone.
- `TestAWatchAndAWholeTableAreClassSix`: a watch, and an `as=Table` read with
  `includeObject=Object`, each ask, and each passes unredacted once allowed.
- `TestAMetadataOnlyReadDoesNotAsk`: kubectl's `get` `Accept` (two Table types, then
  `application/json`) with no `includeObject`, and a `PartialObjectMetadataList` read, pass
  redacted with no ask and no record; an `Accept` naming `application/json` first asks, and so
  does kubectl's with `includeObject=Object`.
- `TestASecretReadWithNoAskerIsRedacted`: under an `Allow` rule and under `Auto`, a grant with
  no asker answers `[redacted]` and records nothing.
- `TestAClusterWideListAsksForTheContext`: the rule the answer writes has no namespace, and the
  request's chat rule line names the context alone.
- `TestACommandAnswerCoversTheReadsThatFollow`: after a `Command` answer, the same get in the
  same grant passes unredacted with no ask, and a get in another namespace asks.
- `TestASecretReadWaitsItsTurnBehindAWrite`: a write waiting on the asker holds a read behind
  it, and the read is decided once the write is; an approved watch streams while a write sent
  after it is decided, so the lock was released before the forward; the ninth request waiting
  is a 429 with the new text.
- `TestSecretDataIsRedactedWithoutTheGrant`: under `ReadOnly` and `Ask` with `NoPrompts` and no
  `Allow` rule, and under every mode with `NoSecretData` and an `Allow` rule, every read of
  `secrets` — get, list, table, watch, a helm release — answers `[redacted]`. The note's sixth
  invariant, and step 6B's. (`Auto` without `NoSecretData` allows the read, as the note's table
  has it.)
- `TestAHelmReleaseWriteFollowsWhatTheCommandRead`: under `Ask`, after a list of the
  namespace's Secrets answered `Once`, a `PUT` of `sh.helm.release.v1.x`, a `DELETE` of it and a
  `POST` typed `helm.sh/release.v1` each reach the write's own decision; after one denied, each
  is refused with the new text; a read denied in another namespace leaves them alone, and a
  cluster-wide one denied refuses them; under `Auto` with no read before, they pass the gate.
- `TestAWriteCarryingRedactedIsStillRefused`, after an approved read.
- `TestARedactedReleaseIsRefusedUnderTheGrant`: after an approved read, a `PUT` and a
  `POST` of a release whose decoded config holds `[redacted]`, and one whose manifest Secret
  does, are refused `refusedRedacted`; a release that does not decode, a release under
  `stringData`, and a JSON patch of a release are `refusedUnshowable`; a release with no mark,
  a `DELETE`, and a merge patch of a release's labels pass to their own decision.
- `TestASandboxedRunReadsASecretRedacted` (`bash`, landed) gains a case: the same run under an
  `Allow` class 6 rule reads the values, and a background run under it reads them redacted.

**`chatsvc`**

- `TestAChatAnswerWritesAClassSixGrant`: `Chat` on a Secret read's request writes
  `GrantRule`'s `Allow` class 6 with the context and namespace, and `Always` writes it into the
  settings.
- `TestAChatsSessionAsksAndReadsSecretData`: `sessionFor`'s session, and `Narrow` of it, carry
  neither flag.

**Webview** (`chat-transcript.test.tsx`, `permission-settings.test.tsx`)

- A waiting read's request: the summary as heading, the path and `GET`, the five buttons, the
  rule lines for a namespace and for a context alone, the `aria-label`; a settled read's line
  in the disclosure with each tag.
- The Add form offers *Secret reads*, draws the context and namespace fields alone while it is
  picked, and sends class `SecretRead` with the rest empty.
- Each mode's line names what it does with Secret data.

## Security

This step widens what leaves the machine: a Secret's values reach the model, and so the
provider, once the user allows it — for one read, for the rest of the command, for the chat, or
always for a context and namespace. What holds it: the default is redacted, in every mode but
`Auto`, and a denial is a redacted 200, so a hijacked command gets nothing by asking twice; the
request names the Secret or the namespace and the scope every rule covers; an always rule is on
screen in Settings; a read that runs unredacted is recorded on the call, and one that cannot be
recorded — no asker, or a failed record — is redacted; a session marked `NoSecretData` is
refused ahead of every rule, which is what the monitor will run under; a settings file Kstack
cannot read refuses Secret data as it refuses writes; the helm gate opens only for a command
that read no Secret data redacted in the namespace, and a body carrying `[redacted]` never
goes, a release's decoded contents included; the Settings mode lines say that `Auto` shows
Secret data.

It narrows nothing: a metadata-only read is redacted as today, and asks no one.

Residuals: `Auto` allows class 6, as the note's table has it, so a user who picked `Auto` for a
context before this step has allowed every Secret there once it lands, which the mode line is
the one warning of; in `Ask` every helm command asks, `list` and `status` included, and a
request answered by reflex protects little — the chat rule is the way out, and the prompt says
so; a rule for a context alone, written from a cluster-wide list, covers every namespace of it;
an approved watch streams every later value under one answer; a helm upgrade whose old
release's rewrite the mark check refuses leaves two releases marked deployed; a value copied
out of a Secret — a ConfigMap, an env value, a Pod that mounts one and logs it — passes as
before ([TODO](../../TODO.md#security)); the output's `safe.Redact` is hygiene, and a value with
no known shape reaches the model as it would from any read.

The record, `docs/security/<date>-secret-data-is-a-permissioned-read.md`, argues this.

## When it lands

- **The security record** above, and an ADR: Secret data is a class 6 read, redacted unless the
  session's policy and rules allow it; a metadata-only read asks no one; a monitor session never
  reads it; the helm release refusal is lifted for a command that read no Secret data redacted.
- **`security-model.md`**: the row *A sandboxed read of a Secret reads its values …
  `[redacted]`* becomes *… unless `Authorize` permits it for the session, per read*, with this
  step's tests, `TestSecretDataIsRedactedWithoutTheGrant` first; the helm release row says
  its writes run once the command read the release unredacted; the *Cluster reads leave the machine* row names Secret data
  as the exception the user grants; a row for `NoSecretData`; the held-rules row names
  `RefusedSecrets`.
- **`sidecar/CLAUDE.md`**: `serveSecretRead` and `decideSecretRead`, `metadataOnly`,
  `forward`'s `redact`, the class 6 verb and summary, the write lock held for a read's
  decision, `redactedReads` and the helm gate over it, the release's own mark check, `NoPrompts` and
  `NoSecretData` on the session and the policy, class 6 rules in `securityconfig`, the write
  path's two refusals that moved.
- **Root `CLAUDE.md`**, *Chat*, the Permissions section and the security invariants: the Secret
  read's request and its label, the disclosure line, `clusterWrites` holding reads; *Secret
  reads* in the Add form; the mode lines.
- **`docs/TODO.md`**: *Check Secret redaction by hand* gains the allowed read.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks.

By hand, `pnpm tauri dev` against a kind cluster with a Secret and a helm release, on Linux or
macOS: ask for `kubectl get secrets` and read the names with no request; ask for `kubectl get
secret x -o yaml` and read *Show Secret x in default on kind-kind*; deny it and read
`[redacted]` in the answer; ask for `kubectl describe secret x` and read the same request, then
deny it; ask for the `-o yaml` again, press Allow for this chat, and read the values; ask for
`helm get values <release>` and read them with no request; on a new chat, ask for `helm
upgrade` of the release, read *Show Secret data in default on kind-kind*, press Allow for this
command, and read the release's writes ask as cluster writes, not `Forbidden`; ask for it again,
deny the read, and read the helm refusal name what the command read redacted; ask for `helm
list` and read the request again; ask for `kubectl get secrets -A -o yaml` and read the request name
the context alone; in Settings, add *Deny Secret reads* for the context and read the next read
come back `[redacted]` with no request.
