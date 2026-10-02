---
title: Secret data is a permissioned read
scope: sidecar, webview
status: Planned
---

# Secret data is a permissioned read

**Needs:** step 3B, whose classifier tags a Secret read class 6 and whose `Decide` answers it,
and step 4B, whose request and four answers this step's prompt rides. **Unblocks:** step 6B,
whose monitor session this step's `NoSecretData` policy is written for.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today every sandboxed read of core `secrets` is redacted, always: a value is `[redacted]`, a
helm release's values and manifest Secrets are rewritten inside, and a write of a helm release
is refused because `helm upgrade` would rebuild it from redacted values. The model has no way
to read a Secret's values but to ask the user to run the command outside the sandbox.

The note's class 6: a read of Secret data is a read that carries write-like risk, so the proxy
redacts by default and treats showing the data as a permissioned action. After this step:

- **The proxy asks `Decide` for every read of core `secrets`** — a get, a list, a table read,
  a watch. `Allowed` passes the response unredacted. `Prompted` holds the read while the user
  decides, with step 4B's request: *Show Secret data from `team-a` on `dev-eks`?*, and the four
  answers. `Denied`, and a denial or an abandoned wait, pass the response **redacted**, as
  today, with a 200: the note says "redacted unless permitted", never refused.
- **A rule for this chat or always is `Allow` class 6 `k8s`**, scoped to the context and
  namespace the request names.
- **The helm release write refusal is lifted** for a session `Decide` lets read real values in
  the release's namespace, since such a session rebuilds a release from what it read.
- **A session can be one that never reads Secret data**, whatever its rules: the policy the
  monitor session (step 6B) runs under. That is how the note's "the monitoring session must
  never receive Secret data" is pinned here, ahead of the session that needs it.

The mirror is untouched: KubeQuery reads `kubestore`, which redacts at write time and holds no
value to show. Nothing changes on Windows: no command there reaches the proxy.

## What is not in this step

- **No monitor.** Step 6B builds the session; this step gives it the policy field and the
  test.
- **No audit of what else carries a secret.** The [TODO](../../TODO.md#security) item stands:
  ConfigMaps, env values and the state of tools that hold credentials pass as they do today.
- **No change to the mirror's redaction** (`kubestore/objects.go`), which serves the
  dashboard and KubeQuery.
- **No change to the `[redacted]` write guard** (`checkBody`): a write carrying the mark is
  still refused.

## Design

### 1. The read asks `Decide`

`kubeproxy/secretread.go`: `(g *Grant) serveSecretRead(w, r, p)`, which `ServeHTTP` calls in
place of `forward` for a request the policy passed that is not a write and where `p.onSecrets()`:

1. `act := classify(...)`: class 6 (step 3B), `Scope{Context, Namespace: p.namespace}`, `Verb`
   `get`, `list` or `watch` as the API server reads it, `Kind` `secrets`, `Name` off the path.
   `Summary` is this step's: *Show Secret `db-creds` from `team-a` on `dev-eks`?* for a get,
   *Show Secret data from `team-a` on `dev-eks`?* for a namespaced list or watch, *Show Secret
   data from `dev-eks`?* for a cluster-wide one, whose scope is the context alone. A table
   read (`as=Table` in `Accept`) and a watch are class 6 like any read: a table row's
   metadata carries a Secret's whole, and a watch streams it.
2. `d, why := permissions.Decide(policy, rules, act)`, `policy` as the write path builds it
   (step 3B §6), plus `NoSecretData` (§4).
3. `Allowed`: `forward` with no `rewriteSecrets`, recorded `allowed` with its reason.
4. `Prompted`: `Ask` with `ActionRequest{Action: act, Grantable: ...}` and no `Write`; the
   answer decides whether `forward` rewrites: approved passes unredacted, recorded `approved`
   with its duration; denied and abandoned pass redacted, recorded as a write's are. A wait
   that ends because the run ended passes nothing: the request is cancelled with it.
5. `Denied`: `forward` redacted, recorded `refused` with the reason — the rule, `NoPrompts`,
   or `NoSecretData`.

The response is redacted or not as a whole: the Secret rewriter and the helm release rewriter
are one `rewriteSecrets`, skipped together. A watch that waits holds its connection through
the wait; its status line goes once the decision is in. The read holds no write lock and
waits beside a write: a Secret read and a write can be on screen at once, and `askAction`
serializes its own callers with `askMu`, so the journal sees them one at a time.
`maxQueuedReads` (8), a `readWaiters` semaphore beside `writeWaiters`, bounds the Secret
reads waiting on the user; one more is a 429 with no `Retry-After`, *too many Secret reads
are waiting on the user*. A `LIST` across all namespaces asks once, for the context; a rule
written from that answer has no namespace and covers every namespace of the context, and the
request's scope line says so.

### 2. The rule

A `Chat` or `Always` answer writes `Allow`, class 6, provider `k8s`, `Scope{Context,
Namespace}` off the action — the namespace unset for a cluster-wide read — verb and kind unset,
through step 4B's `service.Approve`. The note's example: `k8s:secret-read context=dev-*`, which
Settings can write by hand. `Decide`'s mode table for class 6 is step 3B's: `ReadOnly` and `Ask`
prompt, `Auto` allows, and a shipped or user `Deny` rule refuses.

### 3. The helm release write

`serveWrite`'s two refusals of a helm release — `namesRelease` on a `PUT`, `PATCH` or `DELETE`,
and `notARelease` on a `POST` to `secrets` — are conditioned on `Decide`: a class 6 action for
the release's namespace, under the session's policy and rules, that answers `Allowed` lifts
both, and the write then goes on to its own class 4 or 5 decision like any other. Any other
answer keeps the refusal, whose text becomes *kstack: helm changes a release from the Secret
data it read. Allow Secret data for this namespace, always or for this chat, then run it
again.* A body carrying `[redacted]` or its base64 is refused before either check, as today:
a session that was allowed once and denied the next time still cannot write what it read
redacted. This is the decision below.

### 4. A session that never reads Secret data

`session.Session` gains `NoSecretData bool`, and `permissions.Policy` gains the same field.
`Decide` reads it first for a class 6 action: `Denied`, with a reason that says the session
never reads Secret data, ahead of every rule. `chatsvc` sets it false for a chat's session;
`Narrow` copies the parent's, since step 2C classes it as identity; step 6B sets it true for the monitor. So no rule the user writes
in Settings, and no `Auto` mode, opens Secret data to a session built with it: the invariant
is the policy's, not a filter step 6B has to remember.

### 5. The prompt

`tools/bash/prompts/sandbox.md`: a Secret's values read `[redacted]` unless the user allows
showing them, which a read of Secrets asks for, once, for the chat, or always; do not ask the
user to run the command outside the sandbox for that. A `helm upgrade` needs Secret data
allowed for the release's namespace, and its refusal says so. The line saying a helm change
comes back `Forbidden` goes.

### 6. The wire and the request

The request rides `ToolCall.clusterWrites` as a `ClusterWrite` whose `write` fields are empty
(the decision below renames it). `ApprovalRequest` draws, for a change whose `action.class` is
6 and which has no body: the heading through `VisibleText`, the buttons of step 4B with the
scope line *Secret data in `dev-eks` / `team-a`* (or *Secret data in `dev-eks`* for the
context alone) from `scopeLine`, and nothing else. The group's `aria-label` reads *Secret read
awaiting approval*. In the call's disclosure the read is a line as a settled write is —
`GET /api/v1/namespaces/team-a/secrets/db-creds`, tagged `approved · this chat`, `denied`,
`allowed` or `refused` with its reason — so a read that ran unredacted is on screen. The model
result is unchanged: the command's output, redacted by `safe.Redact` as every output is, which
is hygiene and not the boundary.

## Decisions this step asks for

1. **Lift the helm release refusal under the grant.** The refusal exists because a release
   rebuilt from redacted values corrupts the release. A session that read the real values
   rebuilds it from them, so the reason is gone, and `helm upgrade` in the sandbox is what
   the note's users expect. The `[redacted]` guard still catches a session whose read was
   redacted after all. Recommended.
2. **Rename `ToolCall.clusterWrites` to `clusterActions` and `ClusterWrite` to
   `ClusterAction`**, with `method` documented as the request's verb (`GET` for a read), in
   this step, since it is the first to put a read in the list. `waitingRequestsOf`,
   `isWaitingWrite` (then `isWaitingAction`) and `ClusterWriteLines` change their field name
   and nothing else. Recommended: a list named writes that holds a read misleads the next
   reader, and the rename is mechanical.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `NoSecretData` on the session and the policy; `Decide` reads it | `session/session.go`, `permissions/permissions.go`, `chatsvc/turn.go`, their tests | — | Planned |
| 2 | The class 6 summary; `serveSecretRead`; `readWaiters` | `kubeproxy/classify.go`, `kubeproxy/secretread.go`, `kubeproxy/kubeproxy.go`, their tests | 1 | Planned |
| 3 | The helm refusal under `Decide`; the new text | `kubeproxy/write.go`, `kubeproxy/policy.go`, their tests | 2 | Planned |
| 4 | `askMu`; the read recorded on the call | `chatsvc/approval.go`, `chatsvc/store.go`, their tests | 2 | Planned |
| 5 | The wire rename and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 4 | Planned |
| 6 | The request, its label, the scope line, the disclosure line | `src/components/widgets/chat-transcript.tsx`, `src/lib/chats.tsx`, `src/lib/permissions.ts`, their tests | 5 | Planned |
| 7 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 3 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1, then 2, then 3 and 4 at the same time, then 5, then 6 and 7 at the same time,
then 8.

## Tests

**`permissions`**

- `TestNoSecretDataDeniesClassSixAheadOfEveryRule`: with the flag, class 6 is `Denied` under
  each mode and under an `Allow` rule; class 4 is unchanged.

**`session`**

- `TestNarrowKeepsNoSecretData`, a case of step 2C's `TestNarrowKeepsTheParentsIdentity`.

**`kubeproxy`**

- `TestAnAllowedSecretReadPassesUnredacted`: under an `Allow` class 6 rule for the namespace,
  a get answers the values, and the record says `allowed` with the rule.
- `TestAPromptedSecretReadWaitsForTheDecision`: under `Ask`, the read holds until the asker
  answers; approved passes unredacted, denied passes `[redacted]` with a 200.
- `TestADeniedSecretReadIsRedactedAndTwoHundred`: under a `Deny` rule, and under `NoPrompts`,
  the values are `[redacted]`, the status 200, the record `refused` naming the reason.
- `TestATableAndAWatchAreClassSix`: an `as=Table` read and a watch each ask, and each passes
  unredacted once allowed.
- `TestAClusterWideListAsksForTheContext`: the summary names the context alone and the rule
  the answer writes has no namespace.
- `TestSecretReadsWaitBesideAWrite`: a read and a write on one grant both wait at once, and
  the ninth waiting read is a 429.
- `TestSecretDataIsRedactedWithoutTheGrant`: under `ReadOnly` and `Ask` with `NoPrompts` and no
  `Allow` rule, and under every mode with `NoSecretData` and an `Allow` rule, every read of
  `secrets` — get, list, table, watch, a helm release — answers `[redacted]`. The note's sixth
  invariant, and step 6B's. (`Auto` without `NoSecretData` allows the read, as the note's table
  has it.)
- `TestAHelmReleaseWriteRunsUnderTheGrant`: under an `Allow` class 6 rule for the namespace,
  a `PUT` of `sh.helm.release.v1.x` and a `POST` typed `helm.sh/release.v1` reach the write's
  own decision; without the rule each is refused with the new text.
- `TestAWriteCarryingRedactedIsStillRefused`, under the rule.
- `TestASandboxedRunReadsASecretRedacted` (`bash`, landed) gains a case: the same run under an
  `Allow` class 6 rule reads the values.

**`chatsvc`**

- `TestAChatAnswerWritesAClassSixGrant`: `Chat` on a Secret read's request writes `Allow`
  class 6 `k8s` with the context and namespace, and `Always` writes it into the settings.
- `TestAReadAndAWriteAskAtOnce`: two asks on one journal from two goroutines both land, one
  after the other.
- `TestATurnsSessionReadsSecretData`: a chat's and a subagent's `NoSecretData` is false.

**Webview** (`chat-transcript.test.tsx`, `permissions.test.ts`)

- The heading for a get, a namespaced list and a cluster-wide list, the four buttons, the scope
  line for a namespace and for a context alone, the `aria-label`, and no body, path or *Sent
  by* fold beyond the command.
- A settled read's line in the disclosure with each tag.

## Security

This step widens what leaves the machine: a Secret's values reach the model, and so the
provider, once the user allows it — for one read, for the chat, or always for a context and
namespace. What holds it: the default is redacted, in every mode but `Auto`, and a denial is a
redacted 200, so a hijacked command gets nothing by asking twice; the request names the Secret
or the namespace and the scope every rule covers; an always rule is on screen in Settings; a
session marked `NoSecretData` is refused ahead of every rule, which is what the monitor will
run under; the helm refusal lifts only where the session may already read the values, and a
body carrying `[redacted]` never goes.

Residuals: `Auto` allows class 6, as the note's table has it, so a user who picks `Auto` for a
context has allowed every Secret there; a rule for a context alone, written from a
cluster-wide list, covers every namespace of it; a value copied out of a Secret — a ConfigMap,
an env value, a Pod that mounts one and logs it — passes as before ([TODO](../../TODO.md#security));
the output's `safe.Redact` is hygiene, and a value with no known shape reaches the model as it
would from any read.

The record, `docs/security/<date>-secret-data-is-a-permissioned-read.md`, argues this.

## When it lands

- **The security record** above, and an ADR: Secret data is a class 6 read, redacted unless the
  session's policy and rules allow it; a monitor session never reads it; the helm release
  refusal is lifted under the grant.
- **`security-model.md`**: the row *A sandboxed read of a Secret reads its values …
  `[redacted]`* becomes *… unless `Decide` allows it for the session, per read*, with this
  step's tests, `TestSecretDataIsRedactedWithoutTheGrant` first; the helm release row says
  its writes run under the grant; the *Cluster reads leave the machine* row names Secret data
  as the exception the user grants; a row for `NoSecretData`.
- **`sidecar/CLAUDE.md`**: `serveSecretRead`, the class 6 summary, `readWaiters`, the helm
  refusal's condition, `NoSecretData` on the session and the policy, `askMu`.
- **Root `CLAUDE.md`**, *Chat* and the security invariants: the Secret read's request, its
  label and scope line, the disclosure line; the wire rename.
- **`docs/TODO.md`**: *Check Secret redaction by hand* gains the allowed read.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks.

By hand, `pnpm tauri dev` against a kind cluster with a Secret and a helm release, on Linux or
macOS: ask for `kubectl get secret x -o yaml` and read *Show Secret `x` from `default` on
`kind-kind`?*; deny it and read `[redacted]` in the answer; ask again, press Allow for this
chat, and read the values; ask for `helm get values <release>` and read them with no request;
ask for `helm upgrade` of the release and read it ask as a cluster write, not `Forbidden`; on
a new chat, ask for `helm list` and read the request again; ask for `kubectl get secrets -A`
and read the request name the context alone.
