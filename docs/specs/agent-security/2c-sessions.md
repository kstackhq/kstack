---
title: Sessions
scope: sidecar
status: Planned
---

# Sessions

**Needs:** step 1B, whose runtime field this step folds in. **Unblocks:** steps 3B, 4C, 4D, 5A
and 6B, each of which adds a field to the session.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

The note's third principle: one session is one sandbox instance, one proxy token and one
approval policy, and the chat agent, the monitoring agent and a subagent are sessions with
different tokens. Today the pieces of a session are scattered: the Bash tool reads the chat's
cluster and switch off `tools.Runtime`, a subagent's runtime is assembled by hand in
`chatsvc/subagent.go`, and there is no place to hang a policy on.

After this step, a **session** is one value, `session.Session`, that every tool gets on its
runtime and that the cluster proxy reads through the run's token. In this step it holds:

- its **kind**: `chat`, `subagent` or `monitor`;
- the **chat** and **cluster** it belongs to;
- whether the user switched it to run **outside** the sandbox (step 1B).

Later steps add their own fields: the approval mode and rules (step 3B), the hosts (step 4C),
the folders (step 4D) and `NoSecretData` (step 5A). §2 fixes how each kind of field is narrowed
for a subagent, so those steps add a field without re-arguing it.

A chat's turn makes its session. A subagent's session is `Narrow` of its parent's. A `monitor`
session is named here and built in step 6B.

**This step changes no behavior.** It moves a field into one place and adds the type the next
steps fill.

## What is not in this step

- **No policy.** The mode, the rules and `Decide` are step 3B's.
- **No field for a later step.** Each adds its own; this step reserves none.
- **No workspace on the session.** The workspace stays `tools.WorkspacePath(rt.Dir)`, its one
  source (§1).
- **No token per session.** A run's token stays one per run, as the note's *Where this meets the
  code* decides; this step maps it to the session.
- **No monitor.** Step 6B builds it.
- Nothing changes on Windows.

## Design

### 1. The `Session` type

A new leaf package, `session`, importing `apimeta` alone:

```go
// Kind is what kind of agent a session runs.
type Kind string

const (
	Chat     Kind = "chat"
	Subagent Kind = "subagent"
	Monitor  Kind = "monitor"
)

// Session is one agent run's identity and policy: what its tools and the
// proxies its runs serve read to decide what it may do. chatsvc builds a chat's
// at the start of each turn; a subagent's is Narrow of its parent's.
type Session struct {
	Kind      Kind
	ChatID    apimeta.ChatID    // empty for a monitor
	ClusterID apimeta.ClusterID // the cluster its cluster tools reach
	Outside   bool              // the user switched the chat to run outside the sandbox
}

// Narrow is a subagent's session under parent: Kind Subagent, and every other
// field the parent's (see the rule below).
func Narrow(parent Session) Session
```

**The workspace is not a field.** Every reader today derives it from the chat's directory —
Bash's start directory and its refusal, `makeWorkspace` in `bash.go` and `task.go`, the
sandbox's write list, `fileguard`, `Read`, `Write` and `Edit` — through
`tools.WorkspacePath(rt.Dir)`. A copy on the session could disagree with them, and then the
start-directory check would allow one folder while the sandbox made another writable. So the
workspace stays derived from `Runtime.Dir`, and a subagent shares its parent's because chatsvc
gives it the parent's `Dir`, as today. Step 6B gives the monitor a `Dir` of its own.

**`session` stays a leaf.** It imports `apimeta` and, from step 3B, `permissions`, which is
vocabulary and `Decide` and imports no proxy. A proxy package (`kubeproxy`, `egress`)
imports `session` and never the reverse. A later field whose type would live in a proxy package
declares that type in `session` instead, so a proxy reading its session through the token cannot
make a cycle.

### 2. How a subagent narrows

`Narrow` copies or composes each field by what kind of field it is. Every later step classes the
field it adds by this rule and adds its case to `TestNarrowKeepsTheParentsIdentity`.

| Kind of field | Fields | What `Narrow` does |
| --- | --- | --- |
| Identity, fixed for the session's life | `Kind` (set to `Subagent`), `ChatID`, `ClusterID`; later `NoPrompts` and `NoSecretData`, which a kind sets | copied at spawn |
| The sandbox switch | `Outside` | copied at spawn |
| Policy the user can change while the session runs | later `Mode`, `Rules`, `Hosts`, `Folders` | a function read live; the subagent's calls the parent's, or tightens it, never widens it |

**Policy the user can change is a live read.** A subagent runs in the background and outlives
the turn that spawned it, while the parent's session is rebuilt every turn. A value copied at
spawn would keep a mode the user has since tightened, and the subagent would hold more than its
parent now does. So every such field is a `func(context.Context) T` read when it is used, and
`Narrow` hands the subagent the parent's function, or one that reads it and tightens the answer.
A subagent's answer is then never looser than its parent's current one.

**The switch is copied at spawn.** A subagent started while the chat ran outside the sandbox keeps
running outside after the user switches back; each of its commands still asks. This is 1B's
accepted residual, *What started under the switch keeps it*, in
[outside the sandbox is the user's choice](../../security/2026-09-30-outside-the-sandbox-is-the-users-choice.md).
The switch is not a live read because a turn's commands must match the context block the model
was told, and that block is written once, when the turn is reserved.

### 3. The runtime carries it

`tools.Runtime` gains `Session session.Session` and loses `OutsideSandbox`, which becomes
`Session.Outside`.

`ClusterID` and `ChatID` stay on the runtime, since every tool reads them (seven production
reads), and `Session` repeats them for a proxy handed the session alone. chatsvc sets both
copies from the same local values, and the chatsvc tests below pin that they are equal.

- `chatsvc/turn.go` builds `Session{Kind: Chat, ChatID: t.chatID, ClusterID: t.clusterID,
  Outside: t.outsideSandbox}` where it builds the runtime today, in `run`, after `chatOf`
  has set `t.clusterID`. The switch is `t.outsideSandbox`, read in the transaction that
  reserves the turn, beside the context block (`chatsvc/service.go`, `chatsvc/notices.go`).
  Neither value is read again: re-reading the switch would break 1B's rule that a turn's
  commands match what the model was told.
- `chatsvc/subagent.go` sets the subagent's runtime `Session` to `session.Narrow(parent)`, where
  `parent` is the spawning turn's session, built from the same `t` fields.
- A test's runtime sets the fields its tool reads, as today: `Dir` for the workspace,
  `Session.Outside` where it set `OutsideSandbox`.

### 4. Bash reads the session

`tools/bash`:

- `sandboxerFor(rt)` reads `rt.Session.Outside`.
- The run's `kubeproxy.Grant` is made with the session. `NewGrant` gains a `session.Session`
  argument, kept on the grant and answered by `Grant.Session()`, which step 3B reads to decide
  each write. `startProxy` in `tools/bash/proxy.go` gains the same argument, and
  `sandboxedRunFor` passes `rt.Session`. The token stays the grant's, one per run: the run's
  kubeconfig carries it, and the handler that checks it finds the session on the grant. That is
  the map from token to session the note asks for.

`kubeproxy` importing `session` keeps it a leaf beside `sandbox`: `session` imports nothing of
`tools` or `clustersvc`.

### 5. Where the monitor will go

`Monitor` is a `Kind` and nothing else in this step. Step 6B builds the session, with no chat,
its own `Dir`, and the policy the note gives it. Naming the kind now lets steps 3B, 4B, 4C, 4D
and 5A say what a monitor session does in each of their tables, so nothing is retrofitted.

### 6. Seams

- **With 2B** — both edit `sandboxedRunFor` in `tools/bash/bash.go`: 2B sets `Limits` on the
  policy `workspacePolicy` builds, and this step passes `rt.Session` to `startProxy`. Different
  lines; whichever lands second rebases onto the other.
- **With 2A** — both edit `sandboxedRunFor`: 2A drops its `snapshot` argument and adds the tool
  home's Write rule, and this step passes `rt.Session` to `startProxy`. 2A also changes
  `sandboxedRunEnv` and `workspacePolicy`, which this step does not touch. Different lines;
  whichever lands second rebases onto the other.

## Decisions this step asks for

1. **The workspace stays derived from `Runtime.Dir`**, not a session field, so it has one source.
2. **`ChatID` and `ClusterID` live on both the runtime and the session**, set from the same
   values and pinned equal by test, rather than moving every tool's read to the session.
3. **Policy the user can change is a live read, and identity is copied** (§2), so a subagent is
   never looser than its parent's current policy.
4. **The switch is copied at spawn**, carrying 1B's accepted residual rather than re-reading it.
5. **The token stays one per run**, mapped to its session through the grant.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The `session` package | `session/session.go`, `session/session_test.go` | — | Planned |
| 2 | `Runtime.Session`; chatsvc builds and narrows it | `tools/tool.go`, `chatsvc/turn.go`, `chatsvc/subagent.go`, `chatsvc/turn_test.go`, `chatsvc/subagent_test.go`, every test that set `OutsideSandbox` | 1 | Planned |
| 3 | Bash reads the session; the grant keeps it | `tools/bash/bash.go`, `tools/bash/proxy.go`, `kubeproxy/kubeproxy.go`, and the eleven `NewGrant` calls in `kubeproxy/server_test.go`, `write_test.go` and `kubeproxy_test.go` (each passes `session.Session{}`) | 2 | Planned |
| 4 | Docs, per *When it lands* | see there | 1–3 | Planned |

**Order:** 1, then 2, then 3, then 4.

## Tests

**`session`**

- `TestNarrowKeepsTheParentsIdentity`: a narrowed session has `Kind` subagent and every other
  field the parent's, compared field by field, never with `==` on the whole `Session`: a
  live-read field is a function, which `==` cannot compare. It grows a case per field later
  steps add, and a live-read field's case changes the parent's answer after `Narrow` and checks
  the subagent sees the change.

**`chatsvc`** — extend the existing tests rather than adding parallel ones:

- `TestATurnsRuntimeCarriesItsChatsSwitch` (`turn_test.go`) also asserts the runtime's
  `Session`: `Kind` chat, `Session.ChatID == rt.ChatID`, `Session.ClusterID == rt.ClusterID`, and
  `Session.Outside` the switch the turn reserved under. Its mid-turn flip case stays: a switch
  flipped while a turn runs changes the next turn, never this one.
- `TestASubagentsRuntimeCarriesItsParentsSwitch` (`subagent_test.go`) also asserts the
  subagent's `Session` is `Narrow` of its parent's, field by field, with the same equalities.
- `TestATurnsRuntimeIsItsChatsCluster` and `TestASubagentsRuntimeIsItsChatsCluster` assert
  `Session.ClusterID` beside `rt.ClusterID`.

**`bash`**

- `TestTheGrantKeepsTheSession`: over a fake sandbox, the grant a run serves answers, through
  `Grant.Session()`, the runtime's session.

Every existing test that set `Runtime.OutsideSandbox` sets `Session.Outside` instead.

## Security

No boundary moves. This step states and pins one property: a subagent's session is never looser
than its parent's. Identity is copied at spawn; policy the user can change is read live through
the parent (§2), so nothing a later step hangs on a session gives a subagent more than its parent
holds now. The one exception is the sandbox switch, copied at spawn: 1B's accepted residual, which
this step carries unchanged.

The note's session-token invariant, that a request with an unknown or dead token is rejected,
holds today (`TestAWrongOrDeadTokenIsUnauthorized`) and is unchanged.

No security record.

## When it lands

- **`sidecar/CLAUDE.md`**: the `session` package and its narrowing rule, `Runtime.Session`, where
  a turn and a subagent build it, `Grant.Session()`, and the leaf rule.
- **`security-model.md`**:
  - the KubeQuery row ("A chat's KubeQuery reads its own cluster alone") names `chatOf`, not
    `chatCluster`, which does not exist;
  - the row on a command outside the sandbox names the extended switch tests;
  - a new row: a subagent's session is never looser than its parent's, pinned by
    `TestNarrowKeepsTheParentsIdentity` and `TestASubagentsRuntimeCarriesItsParentsSwitch`, with
    the switch's residual linked to 1B's record.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands). By hand, `pnpm tauri dev`:
a sandboxed command and a subagent's sandboxed command behave as before, and a switched chat's
commands still ask.
