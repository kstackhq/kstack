---
title: First-class cluster tools
scope: sidecar, webview
status: Planned
---

# First-class cluster tools

**Needs:** step 4B, whose request draws a classified action with its diff and its four
answers, and through it steps 3B and 5A, whose engine and Secret grant the tools ask.
**Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the model reaches the cluster through `kubectl` in Bash, where a change is a request
the proxy parsed out of an HTTP body, and KubeQuery, which reads the mirror. [The
note](../../notes/sandbox-credentials-and-permissions.md) asks for a few first-class tools
beside raw Bash, so most changes arrive as a prompt written from arguments, not parsed.

After this step, every turn on a model that takes tools is offered five more tools, in
`tools/kube`:

| Tool | Does |
| --- | --- |
| `k8s_read` | an object or a list, as JSON, YAML or a table, a Secret's values redacted unless the session may read them; a write's answer always redacts them |
| `k8s_apply` | a server-side apply of one manifest, field manager `kstack` |
| `k8s_delete` | one object |
| `k8s_scale` | a workload to `replicas` |
| `k8s_rollout_restart` | a Deployment, StatefulSet or DaemonSet, as kubectl does it |

Each reaches the cluster through `clustersvc`, on the chat's cluster, with the user's
connection, the way KubeQuery reaches the cache. Each write builds the same
`permissions.Action` the proxy's classifier builds for the same request, asks `Decide` with
the session's policy, and runs only as the answer says: unasked and recorded under `Allowed`,
after step 4B's request under `Prompted`, not at all under `Denied`. So nothing a tool can do
is wider than what `kubectl` in the sandbox can do; it is the same gate with a better prompt.

`kubectl` in Bash stays for everything else: logs, describe, plugins, diff, a helm change.
The ADR *the cluster is kubectl in Bash, and KubeQuery over the mirror* is amended, not
replaced, and the tools' token cost is measured before they are offered by default.

On Windows the five tools work as anywhere, since they run in the sidecar and enter no
sandbox; there they are the one cluster path under the engine's modes and rules.

## What is not in this step

- **No new request and no new class.** Step 4B's `ApprovalRequest` draws a tool's action as
  it draws a proxy write's, from the same `Action` and diff; the class 5 list moves and grows
  by nothing.
- **No batch.** A manifest is one object; a multi-document one is refused with a line saying
  to apply one at a time, or to use `kubectl apply` in Bash.
- **No `exec`, `logs`, `port-forward` or `describe`.** Bash has them.

## Design

### 1. The tools

`tools/kube`, one file per tool (`read.go`, `apply.go`, `delete.go`, `scale.go`,
`restart.go`), `cluster.go` for the connection, `action.go` for the action, and `prompts/`.
`kube.New(clusterSvc)` answers the five as `[]tools.Tool`, which `app`'s `chatTools` appends
to the box, beside `kubequery.New(clusterSvc)`. Each is `tools.Custom`; each is
`tools.Gated`, `k8s_read` included (§2). `catalog`'s `ours` gains the five names, so every
provider's list offers them, and `TestEachListIsOneToolOfEachKind` holds since each is its
own kind (§3).

The schemas, each `additionalProperties: false`, each with a `description` field the
transcript draws as every tool has:

| Tool | Required | Optional |
| --- | --- | --- |
| `k8s_read` | `kind` | `name`, `namespace`, `labelSelector`, `output: json \| yaml \| table` (json) |
| `k8s_apply` | `manifest` (YAML or JSON, one object) | `force` (false) |
| `k8s_delete` | `kind`, `name` | `namespace` |
| `k8s_scale` | `kind`, `name`, `replicas` | `namespace` |
| `k8s_rollout_restart` | `kind`, `name` | `namespace` |

`kind` is read as kubectl reads one: a kind (`Deployment`), a resource (`deployments`, `deploy`),
or either with its group (`deployments.apps`). A `namespace` left out is `default`. Each
description is one sentence, and the five schemas together are held under Decisions 1's
budget: `TestTheOfferIsSmall` bounds the definitions' bytes, and a live test on the Messages
API records the token count for the ADR.

### 2. How a tool runs

**The cluster.** `cluster.go` reaches it as Bash's `target.go` and `upstream.go` do: the
record by `rt.Session.ClusterID` through `Clusters().Get`, refused when gone or marked for
deletion; the context by `clustercard.ContextName`; the server UID off
`Status.Server.UID`, and none is `ErrNotIdentified`, the tool answering `no-cluster`; then
`AcquireConnection` for the call and `Lease.ConnFor(ctx, serverUID)` for the connection,
whose `BaseURL` and `HTTPClient` send the request, released when the call ends. Never Bash,
never the proxy's socket: the tool is its own client of the same connection.

**The request.** `kind` resolves through the connection's discovery, `/api/v1` and `/apis`,
read once per call and cached per lease, so a kind the cache does not mirror still resolves.
Then one request each, as the API server takes it:

| Tool | Request |
| --- | --- |
| `k8s_read` | `GET` of the resource, or of the name; `labelSelector` in the query; `output: table` with `Accept: application/json;as=Table;v=v1;g=meta.k8s.io`. A core `secrets` answer goes through `kubeproxy`'s walk, exported as `kubeproxy.RedactSecret(value)` and `RedactRelease`, unless the session may read Secret data (below); YAML is rendered from the redacted JSON |
| `k8s_apply` | `PATCH` of the name with `application/apply-patch+yaml`, `fieldManager=kstack`, `force` as given. The body is the manifest as sent, checked as the proxy checks a body: `kubeproxy.CheckBody`, exported over its pure part — not valid UTF-8, past 1 MiB, does not decode, holds `[redacted]` or its base64 in any string, or a Secret typed `helm.sh/release.v1` — each a refusal in the proxy's words |
| `k8s_delete` | `DELETE` of the name, no body, so the API server's own propagation applies as kubectl's does |
| `k8s_scale` | `PATCH` of the `scale` subresource with `application/merge-patch+json` `{"spec":{"replicas":N}}` |
| `k8s_rollout_restart` | `PATCH` of the name with `application/strategic-merge-patch+json` setting `spec.template.metadata.annotations["kubectl.kubernetes.io/restartedAt"]` to now in RFC 3339, the patch kubectl sends; a kind with no pod template is refused |

**The gate.** `Approval(ctx, rt, input)`, which the loop calls before `Run`, does all of the
deciding, and `Run` only executes:

1. Parse the input; bad input is the error the loop answers `bad-input`.
2. Resolve the kind and build `kubeclass.Request` (§2, *shared classification*), and from it
   `act := kubeclass.Classify(req)`: the same `permissions.Action` the proxy would build for
   the same method, path and body, with `Scope{Context, Namespace}`, `Verb`, `Kind`, `Name`
   and the `Summary` step 3B spells (*Delete pod `api-7f9c` in `team-a` on `dev-eks`*).
3. `d, why := permissions.Decide(policy, rt.Session.Rules(ctx), act)`, `policy` being
   `{Mode: rt.Session.Mode, NoPrompts: rt.Session.NoPrompts}`.
4. `Allowed`: answer `tools.Approval{Skip: true}` carrying `act` and `why` in the fields step
   4B adds to `Approval` for a decision made with nobody asked, so the journal records an
   `allowed` row with its reason, as step 3B §7 records the proxy's.
5. `Prompted`: answer `tools.Approval{}` carrying `act`, and for an apply or a scale the diff
   step 4B computes, computed the same way: a `GET` of the object, then the same request with
   `dryRun=All`, the two diffed by step 4B's function. The loop puts it to the user as step
   4B's request, keyed as any call's on `approval.id`.
6. `Denied`: a `*tools.Refusal` whose `Result` is the proxy's refusal for the same decision,
   *<summary> is not allowed: this context is read-only* or *a rule denies it*, naming the
   rule. The model reads it, as step 3B §9's prompt tells it, as the user's decision.

Class 1 reads skip the gate: `k8s_read` answers `Skip` for anything but core `secrets`. A
Secret read is class 6, and follows step 5A: `Decide` answers `Allowed` when the session holds
the grant, and `Run` reads the values; `Prompted` asks, and an approval reads them; `Denied`,
which the monitor always is, reads them redacted rather than refusing, since the object's
shape is still an answer. A write's answer is always redacted (*The result*, below).
Class 5 asks in every mode and never runs under `NoPrompts`, as
`Decide` has it; a background subagent's session is its parent's, so a write in it asks the
user like the parent's does (the note's *An agent's requests wait* rule).

**Shared classification.** Step 3B's `kubeproxy/classify.go` moves to a new leaf,
`kubeclass`, importing `permissions` alone:

```go
// Request is one Kubernetes request as both the proxy and the tools see it.
type Request struct {
	Method, Group, Version, Resource, Namespace, Name, Subresource string
	MediaType, Context                                             string
	Body                                                           []byte // decoded for the class 5 list
	DryRun                                                         bool
}

func Classify(r Request) permissions.Action
func CheckBody(method, mediaType string, body []byte, onSecrets bool) error // checkBody, less HTTP
```

`kubeproxy` fills a `Request` from its `apiPath` and headers and calls both; `tools/kube`
fills one from its arguments. The class 5 list, `decodeBody`, `holdsMark` and `notARelease`
move with them. One table, two callers: `TestTheToolsClassifyAsTheProxyDoes` keeps them one.

**The result.** What the model reads is the API server's answer, redacted as a command's
output is (`safe.Redact` over text, `safe.RedactJSON` over a body), through `tools.Fit` at
`InlineLimit` and `SaveTo(rt.Dir)` past it. A `Status` the server refuses with is answered as
its message, `isError` set; a write answers the object as returned, a delete the `Status`.
A write's or a delete's answer about core `secrets` always goes through `RedactSecret` and
`RedactRelease`, as the proxy's `rewriteSecrets` does for every write it forwards; only a
`k8s_read` that step 5A lets through is whole. So an apply that changes only a label on an
existing Secret does not hand back the values it left alone.

### 3. The record and the wire

`tools.Action` gains `Kube *KubeAction`, and `ActionKind` five kinds, `kube-read`,
`kube-apply`, `kube-delete`, `kube-scale` and `kube-restart`, one per tool, so the box's
one-tool-per-kind rule holds. `Kind()` reads `Kube.Op`:

```go
// KubeAction is a call of one of the cluster tools as its arguments name it.
type KubeAction struct {
	Op            string `json:"op"`   // read, apply, delete, scale, restart
	Kind          string `json:"kind"` // as the call spelled it
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
	Output        string `json:"output"`
	Manifest      string `json:"manifest"`
	Replicas      *int   `json:"replicas"`
	Force         bool   `json:"force"`
}
```

Each tool's `ActionOf(arguments, cwd)` is the package-level function over its `parse`, as
every tool has ([ADR](../../adr/2026-09-23-a-tool-call-shows-itself-from-its-arguments.md)),
and `TestActionOfReadsOldRowsTheSame` pins a fixed set of stored arguments per tool. On the
wire `ToolAction` gains `kube: KubeAction` and `ToolActionKind` the five members, which
`TestEveryActionKindIsServed` covers as it covers the rest.

The request is step 4B's: the call's `approval` carries the `PermissionAction` (its summary,
class, provider and scope) and the diff, and `ApprovalRequest` draws them whether the row is a
call's own or a proxy write's. `waitingRequestsOf` in `chats.tsx` needs nothing new: a
tool's request is a call's own approval.

`summaryOf` in `chat-transcript.tsx` gains the five, read off `action.kube`, in the note's
form and in the user's terms:

| Op | Summary |
| --- | --- |
| read | *Read pods in `team-a`*, *Read pod `api-7f9c` in `team-a`*, *Read nodes* |
| apply | *Apply Deployment `api` in `team-a`*, read off the manifest's `kind` and `metadata` by the sidecar, never parsed in the webview: `KubeAction.Kind`, `Name` and `Namespace` are filled from the manifest for an apply |
| delete | *Delete pod `api-7f9c` in `team-a`* |
| scale | *Scale deployment `api` in `team-a` to 3* |
| restart | *Restart deployment `api` in `team-a`* |

The name, namespace and kind go through `VisibleText`, since they are cluster data. A call
whose arguments the tool refuses reads through `actionKindLabel` (*Read cluster*, *Apply*,
*Delete*, *Scale*, *Restart*). An apply's disclosure opens with the manifest through
`FoldedBlock`, as KubeQuery's SQL does, then what the model read.

### 4. The prompt

`tools/kube/prompts/kube.md` is one section for the five, returned by `k8s_read`'s
`Prompt()`; the other four answer an empty section, which `Box.Prompts()` leaves out. It
says: use these tools for a change to the cluster, so the user reads a clear request naming
the object; a read here costs the user nothing, but a Secret's values ask; use `kubectl` in
Bash for what they do not cover (logs, describe, plugins, diff, helm) and KubeQuery for what
the cache answers; a `Forbidden` naming the mode or a rule is the user's decision. A tool's
`prompts/description.md` is its schema's one sentence. Bash's `sandbox.md` gains one line
pointing a change at these tools first.

### 5. The ADR

[*The cluster is kubectl in Bash, and KubeQuery over the
mirror*](../../adr/2026-09-27-the-cluster-is-kubectl-in-bash-and-kubequery.md) gains
`amended_by` naming the new ADR, which says: kubectl in Bash stays the fallback; five tools
are offered because the prompt the note asks for is better written from arguments than parsed
out of a body; the set costs N tokens a fresh prefix, measured (Decisions, 1), against 10.6k.

## Decisions this step asks for

1. **The token budget.** The full structured set cost 10.6k tokens a fresh prefix. These five
   schemas are small and their descriptions one sentence. Recommended: the five together
   under 2,500 tokens on the Messages API, measured by a live test before they are offered by
   default, the number recorded in the ADR; over it, the descriptions are cut before the
   tools are.
2. **`kubeclass`, not `kubeproxy/classify`, and five kinds over one action type.**
   `tools/bash` already imports `kubeproxy`, so either home works; a leaf beside `permissions`
   reads as what it is, one classifier with two callers. The box offers one tool per kind, so
   five tools are five kinds, and one `KubeAction` with an `Op` keeps the wire to one type and
   the transcript to one reader. Both recommended.
3. **A Secret the monitor reads is redacted, not refused.** The object's shape is an answer a
   monitor can use, and the values are what step 5A protects. Recommended.
4. **A write to a Secret always answers redacted.** The API server answers a write with the
   whole object, values included, so an unredacted answer is a Secret read by another name.
   Showing it under step 5A's grant is the other way; it adds a second decision to every
   write and makes the tools answer what `kubectl` through the proxy never does. A session
   that wants the values reads them with `k8s_read`. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `kubeclass`: move the classifier, the class 5 list and the body checks; the proxy calls them | `kubeclass/`, `kubeproxy/classify.go`, `kubeproxy/write.go`, their tests | — | Planned |
| 2 | `KubeAction`, the five kinds, the wire, `Prompts()` skipping an empty section | `tools/tool.go`, `tools/box.go`, `sidecar/graph/schema.graphqls`, `graph/`, generated code, their tests | — | Planned |
| 3 | `tools/kube`: the connection, the five tools, the gate, the results | `tools/kube/*.go`, `tools/kube/prompts/`, their tests | 1, 2 | Planned |
| 4 | The box and the catalog offer them; the token measurement | `app/app.go`, `catalog/catalog.go`, `catalog/kube_live_test.go`, their tests | 3 | Planned |
| 5 | Codegen, `summaryOf`, the labels, the disclosure's manifest | `src/gql/`, `src/lib/chats.tsx`, `src/components/widgets/chat-transcript.tsx`, their tests | 2 | Planned |
| 6 | The prompts | `tools/kube/prompts/kube.md`, `tools/bash/prompts/sandbox.md`, their tests | 3 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4, 5 and 6 at the same time, then 7.

## Tests

**`kubeclass`**

- `TestEveryRequestIsClassified` and `TestASummaryReadsAsTheNoteSays`: step 3B's tables, moved
  here unchanged.
- `TestCheckBodyRefusesWhatTheProxyRefused`: each of `checkBody`'s refusals, moved.

**`kube`**, each against a fake API server (`httptest`, answering discovery and recording
each request), the way `app`'s end-to-end tests fake one:

- `TestReadListsAndGets`: a list with a selector, an object by name, each `output`, the
  `Accept` for a table.
- `TestReadRedactsASecret`: values `[redacted]`, `last-applied-configuration` too, and a
  helm release's inside; and read whole under a session holding step 5A's grant.
- `TestAWriteToASecretAnswersRedacted`: an apply that changes one label on an existing Secret,
  and a delete of one, answer the fake's object with its values `[redacted]` and a helm
  release's inside, and no request is put to the user for it; under an `Allow` class 6 rule
  for the namespace they are still redacted, as the proxy's are.
- `TestApplyIsServerSideUnderTheFieldManager`: `PATCH`, `application/apply-patch+yaml`,
  `fieldManager=kstack`, `force` as given; a multi-document manifest refused.
- `TestApplyChecksTheBodyAsTheProxyDoes`: `[redacted]` in a string, a helm release Secret,
  and 1 MiB refused with the proxy's words.
- `TestScalePatchesTheScaleSubresource`, `TestRolloutRestartSetsTheAnnotation` (RFC 3339, the
  strategic patch, a kind with no template refused), `TestDeleteSendsNoBody`.
- `TestTheToolsClassifyAsTheProxyDoes`: for each write, the `Action` the tool builds equals
  the one `kubeclass.Classify` builds from the HTTP request the tool then sent, over a table
  including a scale to 0, an apply of a `RoleBinding`, and a delete of a namespace.
- `TestAnAskSessionsWriteWaitsOnTheRequest`: the `Approval` carries the action and, for an
  apply, the diff computed from the `GET` and the dry run; nothing is sent until `Run`.
- `TestAnAutoSessionsWriteRunsAndIsRecorded`: `Skip` with the decision and reason.
- `TestAReadOnlySessionRefusesAWrite`: a `*tools.Refusal` naming the mode; a rule's denial
  names the rule.
- `TestClassFiveAlwaysAsks`: a scale to 0 under `Auto` is `Prompted`.
- `TestTheMonitorNeverRunsAWrite`: under `ReadOnly` and `NoPrompts`, every write is refused,
  nothing reaches the fake server, and a Secret read is redacted.
- `TestATurnsToolsReachItsChatsCluster`: `rt.Session.ClusterID` alone decides the record, as
  KubeQuery's test has it.
- `TestActionOfReadsOldRowsTheSame`, per tool, and `TestTheOfferIsSmall`: the five
  definitions' bytes under a fixed bound.

**`catalog`** (`kube_live_test.go`, with a real key, as the other live tests run)

- `TestTheFiveToolsCostUnderTheBudget`: `count_tokens` on the Messages API with the five
  offered, asserting Decisions 1's number and printing it for the ADR.

**`app`**

- `TestEveryToolIsOnAList`, `TestEachListIsOneToolOfEachKind`, `TestEveryGatedToolCanBeShown`:
  the existing tests, now over the five.

**Webview** (`chat-transcript.test.tsx`)

- Each summary of §3, the refused-arguments labels, the manifest under an apply's summary,
  and a kube request drawn as step 4B draws a proxy write's.

## Security

Nothing widens. Each tool reaches the cluster with the user's own connection, on the chat's
cluster, under the same classifier (`kubeclass`, one table for the proxy and the tools:
`TestTheToolsClassifyAsTheProxyDoes`) and the same `Decide` as a proxied write, so a rule, a
mode, the class 5 list and step 5A's Secret grant answer the same for `k8s_delete` as for
`kubectl delete`. The request is built by the sidecar from the call's arguments, which the
tool alone reads ([ADR](../../adr/2026-09-23-a-tool-call-shows-itself-from-its-arguments.md)),
and the diff as step 4B computes it. A monitor session runs no write and reads no Secret value
through them (`TestTheMonitorNeverRunsAWrite`). A write's answer holds the object, so a
Secret's values in it are always redacted (`TestAWriteToASecretAnswersRedacted`).

To name: the tools bypass the sandbox's socket, so their bound is the engine and the
connection, not the proxy's handler, which is why the classifier is shared rather than copied.
Residuals are step 3B's: an `Allow` rule's scope can be reached past, and a class 5 list is a
list.

The record, `docs/security/<date>-first-class-cluster-tools.md`, is short and says this.

## When it lands

- **The security record** above, and the ADR amendment of §5, with the measured number.
- **`security-model.md`**: a row for the five tools naming `TestTheToolsClassifyAsTheProxyDoes`,
  `TestTheMonitorNeverRunsAWrite`, `TestAWriteToASecretAnswersRedacted` and
  `TestATurnsToolsReachItsChatsCluster`; the KubeQuery
  cluster-scope row gains the tools.
- **`sidecar/CLAUDE.md`**: `tools/kube`, `kubeclass`, the exported `kubeproxy` checks and
  walk, the five kinds, `Prompts()`'s empty section, `catalog`'s `ours`. **Root
  `CLAUDE.md`**, *Chat*: `summaryOf`'s five, the labels, the disclosure's manifest.
- **`docs/TODO.md`**: the off switch for KubeQuery covers `k8s_read` too; say so.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks, and the live test with a key.

By hand, `pnpm tauri dev` against a kind cluster with `dev` and `prod-test` contexts: on a
`dev` chat ask to restart deployment `api` and read the request *Restart deployment `api` in
`default` on `dev`* with its four answers; approve, and read the annotation in `k8s_read`'s
answer; ask to scale it to 0 and read it ask whatever the mode; on `prod-test` read the write
refused naming the read-only mode; ask for a Secret and read its values `[redacted]` until
you approve; ask for the pod's logs and read the model use `kubectl logs` in Bash.
