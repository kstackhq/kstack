---
title: The monitoring session
scope: sidecar, webview
status: Planned
---

# The monitoring session

**Needs:** step 5A, whose `NoPrompts` keeps Secret data redacted; step 4C, whose egress proxy
holds the host allowlist. **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

[The note](../../notes/sandbox-credentials-and-permissions.md) has two agents: a chat agent the
user talks to, and a monitoring agent that reads the cluster in the background. Today there is
no monitoring agent: `session.Kind` names `Monitor` (step 2C) and nothing builds one, and the
`clusters` row's `monitoring_enabled` is served with nothing running on it. After this step the
agent's **plumbing** exists, driven by a stand-in until an agent is designed, as the note's
*Where this meets the code* decides (decision 3):

- **`monitor`** is one package that builds a **monitor session** per watched cluster: read-only
  at every proxy, Secret data never, no prompts, its own workspace under `<data>/monitor/`, no
  folder grants and none of the user's hosts.
- **A proposal** is how the monitor asks for a change: a title, a reason and the question a chat
  is started with. `proposals` is a table, `proposalsWatch` a delta watch, and the **proposal
  card** draws it over the chat list. **Do it** starts a chat on the cluster whose first
  question is the proposal's, so the change runs under the normal permission flow;
  **Dismiss** puts it away.
- **Settings: Monitoring** shows which clusters the monitor watches and that it shares no
  folder grant.

A monitor session holds exactly what its token says: it reads everything its cluster serves but
Secret data, writes its own workspace, and asks nobody. A finding reaches the user as a card
and a chat as a message, never as a path into another workspace.

## What is not in this step

- **No monitoring agent.** What the monitor is told, when it runs, and what it looks for is a
  design of its own. This step's `Runner` is what that agent will call; a stand-in calls it in
  tests.
- **No findings view.** `findings/` is the harness's folder; the proposal card is the one
  finding that reaches the user here.
- **No shared folder grants.** The note calls sharing a chat's grants with the monitor a
  separate, explicit switch; it is drawn disabled here (§6).
- **Windows.** No sandbox, so `Runner.Run` refuses there, as on any machine with none (§1);
  the table, the watch and the card work everywhere.

## Design

### 1. The `monitor` package and the session

`monitor/monitor.go`. `monitor.New(paths Paths, bashTool *bash.Tool, clusterSvc
clustersvc.Service, proposals *Proposals) *Runner`, where `Paths` is
`{Dir string}`, the app's `<data>/monitor`, added to `app/paths.go`'s tree and made 0700 by the
package, inside the data directory every chat's `Always` part hides.

```go
// Session is the monitor's session. Each watched cluster gets a run of its
// own, whose runtime names the cluster, since a run's cluster proxy is one
// cluster's connection.
func Session() session.Session {
	return session.Session{
		Kind:      session.Monitor,
		Mode:      func(context.Context) permissions.Mode { return permissions.ReadOnly },
		NoPrompts: true,
		Rules:     func(context.Context) []permissions.Rule { return permissions.Shipped() },
		Hosts:     monitorHosts, // §2: the kubeconfig's servers alone
		Folders:   nil,
	}
}
```

The runtime's `ChatID` is empty, and `Outside` is false: a monitor never runs outside the sandbox. `Mode` is
`ReadOnly` whatever `Settings.ModeFor` says of the cluster's context, and `Rules` is the
shipped rules alone: no `chat_grants` row and no always rule of the user's reaches it, since a
grant the user wrote for a chat means that chat.

**The runner** builds a runtime and hands it to a caller:

```go
// Run builds the monitor's runtime for clusterID and calls fn with it. It
// refuses a machine with no sandbox (ErrNoSandbox) and a cluster that is not
// watched (ErrNotWatched).
func (r *Runner) Run(ctx context.Context, clusterID apimeta.ClusterID, fn func(rt tools.Runtime)) error
```

The runtime: `Session` as above; `ClusterID`; no `ChatID`; `Dir` a `monitorDir`, a `tools.ChatDir`
over `<data>/monitor/<clusterID>` (`Path`, and `Root(create)` through `os.Root` as `chatDir`
is), so `tools.WorkspacePath(rt.Dir)` is the session's workspace; `Tasks` a `tasks` whose
`Start` answers `ErrNoBackgroundTask` (*the monitor runs no background command*) and whose
`Stop` is false, since no row and no notice exist for one; `Files` a per-run stamp map like
`chatsvc`'s `runStamps`; no `Agent`; no `ActionAsker`. The stand-in in tests runs Bash
calls through it (`bashTool.Run(ctx, rt, raw)`); the future agent will run an `agent.Turn`
over it with an `Approver` that denies everything, offered the chat's box
`Without(tools.ActionDelegate, tools.ActionMemory, tools.ActionFetch)`: no subagent, no note a
chat would read, and no fetch, which dials from the sidecar and not through the egress proxy.

A run whose sandbox does not confine (`Confines()` false, Windows included) is refused before
anything is made: a monitor is never asked about and never runs unconfined.

### 2. What the token policy gives it

Each row is one of the note's invariants and the test that pins it, through the real proxies
with a fake upstream. The policy is `{Mode: ReadOnly, NoPrompts: true}`, so step 3B's `Decide`
answers `Denied` for class 3, 4 and 5, and for class 6, since a `Prompted` under `NoPrompts` is a denial.

| Invariant | What holds it | Test |
| --- | --- | --- |
| A `POST`, `PUT`, `PATCH`, `DELETE` or `DELETECOLLECTION` from a monitor token is refused (the note's fifth) | the cluster proxy's write path: `Decide` under `ReadOnly` refuses class 4 and 5 with a 403 naming the mode, and `writesFor` has no asker to fall back to | `TestAMonitorWriteIsRejected`, a table over the five methods and over `scale`, `status`, `ephemeralcontainers` and `binding`; each reaches nothing upstream |
| `exec`, `attach`, `portforward` and `proxy` are refused whatever the verb | the policy's refusals, before classification, as for any session | the same test's last rows |
| Secret `data` and `stringData` are always redacted (the note's sixth) | step 5A: a class 6 read under `NoPrompts` never holds the grant, so the rewriter runs | `TestAMonitorReadsASecretRedacted`, a `GET` of `secrets` and a list, values `[redacted]` |
| An unlisted host is refused with no prompt | the egress proxy: a monitor session's `Hosts` answers step 4C's `kubeconfig` source alone, never one the user added, never a chat's rule; class 3 under `NoPrompts` is `Denied` | `TestAMonitorNeverAsksForAHost`: a `CONNECT` to a user-added host is refused, the asker is never called, and the API server's host passes |
| No chat's folder grant reaches it (the note's ninth) | `Folders` is nil and nothing fills it; the policy's Files rules are `System` and the run's own | `TestAChatsFoldersNeverReachTheMonitor`: a folder granted always and one granted to a chat are absent from the monitor run's policy and unreadable in it (step 4D keeps the same test) |

The run's token is one per run, as today: `sandboxedRunFor` makes the grant with the session,
and the proxies read the policy off it. Nothing in this step changes the proxies; they read
`Kind` nowhere. A monitor is refused by its mode and its `NoPrompts`, so a proxy that forgot
about monitors would still refuse it.

### 3. The workspace and handoffs

`<data>/monitor/<clusterID>/` holds `workspace/`, the one folder the session's commands write,
and `findings/`, the harness's: what the future agent keeps between runs (baselines, last-seen
events), written through the folder's root by Go code and never by the model's tools. Both go
when the cluster goes: `Runner` subscribes to `KeyClusters` and removes the folder of a cluster
whose row is marked or gone, and a start sweep removes folders naming no cluster
(`rootdir.Sweep`, as the chats' directory does).

Handoffs go through the app. No chat's tools reach the monitor's folder, and the monitor's
Bash reaches no chat's: both are inside the data directory, which every run's `Always` part
hides but for its own paths (`TestAChatCannotReadTheMonitorsFolder`,
`TestTheMonitorCannotReadAChatsFolder`). A finding reaches a chat as the text of a proposal's
prompt (§4), never as a path.

### 4. Proposals

`proposals` in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md):

```sql
-- proposals: a change the monitor proposes; prompt is the question Do it starts a chat with.
-- request_key is the start's send key, set with 'started' and cleared once chat_id is written.
CREATE TABLE proposals (
  id          TEXT    PRIMARY KEY,
  cluster_id  TEXT    NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  title       TEXT    NOT NULL,
  reason      TEXT    NOT NULL,
  prompt      TEXT    NOT NULL,
  status      TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'started', 'dismissed')),
  chat_id     TEXT    REFERENCES conversations(id) ON DELETE SET NULL,
  request_key TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX proposals_cluster_idx ON proposals (cluster_id) WHERE status = 'open';
```

`monitor.Proposals` owns the table (`monitor/proposals.go`, a `sqlstmt.Set` of its own):

- `Add(ctx, clusterID, title, reason, prompt) (Proposal, error)`: title one line under 120
  characters, reason under 2,000, prompt under 4,000, else `ErrBadRequest`; a cluster marked or
  gone is `ErrClusterGone`. It notifies `appdb.KeyProposals`.
- `Start(ctx, id, mode chatsvc.Mode) (chatsvc.Chat, error)`: mints the send's key
  (`appdb.NewID()`) and, in one transaction, flips `open` to `started` and stores the key as
  `request_key`, refusing any other status (`ErrNotOpen`); then calls `chatsvc.Send(ctx, nil,
  mode, clusterID, providerID, modelID, effort, requestKey, prompt)` on the catalog's first
  provider's first model at its default effort, the seed the composer gives a chat that has not
  started; then writes `chat_id` and clears `request_key` in one transaction. A send that fails
  puts the row back to `open`, clears the key and returns the error, so the card can be pressed
  again; an empty catalog is refused before the flip. A link that fails to write after the
  send committed leaves the row `started` with its key, for `Recover`. The key is what makes the chat
  findable: `Send` answers a repeat of its key with the message it wrote, and
  `chatsvc.ChatForRequest(ctx, key) (ChatID, bool, error)` names the chat of the message that
  holds it.
- `Recover(ctx)`: finishes a start the sidecar stopped inside. `app` runs it at start, after
  `chatsvc`'s own start sweep and before the wire serves. For each `started` row that still
  holds a `request_key`: a chat that holds the key is linked as `Start` links it; none puts the
  row back to `open`, so its card returns. It never sends, so no turn runs that nobody is
  watching (Decisions, 4).
- `Dismiss(ctx, id)`: `open` to `dismissed`; any other status is `ErrNotOpen`.
- `Watch(ctx, clusterID)`: a delta watch over the cluster's open proposals, folded with
  `deltafold` on `KeyProposals`: a snapshot, one `Bookmark`, then `Added` for a new one and
  `Deleted` when one leaves `open`, since the card is for open ones alone.

The prompt is the question the chat's model runs: the monitor's text, so cluster data at one
remove. The chat's turn treats it as any question — the card is attached, the tools are the
chat's, and every write asks as any chat's does. Nothing marks the chat as the monitor's.

**The wire:**

```graphql
scalar ProposalID

"A change the monitor proposes. `reason` and `prompt` are the monitor's text, read from cluster data."
type Proposal { id: ProposalID!, clusterID: ClusterID!, title: String!, reason: String!, prompt: String!, createdAt: Time! }

"One frame on `proposalsWatch`. `proposal` is null on `Bookmark`."
type ProposalWatchFrame { type: DeltaFrameType!, proposal: Proposal }

extend type Subscription {
  "DELTA WATCH. One cluster's open proposals, keyed by `proposal.id`. A started or dismissed one leaves as `Deleted`."
  proposalsWatch(clusterID: ClusterID!): ProposalWatchFrame!
}

extend type Mutation {
  "Start a chat on the proposal's cluster, in `mode`, whose first question is the proposal's prompt, and mark it started. Refused KSTACK_VALIDATION_ERROR when it is not open or the catalog is empty, KSTACK_RECORD_NOT_FOUND for an unknown id."
  proposalStart(id: ProposalID!, mode: ChatMode!): Chat!
  "Put a proposal away. Refused KSTACK_VALIDATION_ERROR when it is not open."
  proposalDismiss(id: ProposalID!): Boolean!
}
```

`graph.Resolver` gains `Proposals *monitor.Proposals`. No mutation adds a proposal from the
webview: `Add` is the harness's.

### 5. The card

`proposal-card.tsx`. `useProposals(clusterID)` in `src/lib/proposals.tsx` folds the watch into
an id-keyed map over `useWatchSubscription`, gated on `type === 'Bookmark'`, `unshared` since
both panes can watch, and paused with no cluster. **`ProposalCards({ mode, onStarted })`** draws
one card per open proposal of the window's cluster (`useActiveCluster().clusterID`), newest
first, and nothing before the `Bookmark`: the title, one line, `truncate` with no `title`; the
reason as plain text in `whitespace-pre-wrap`, never markdown, since it came from cluster data,
folded past 6 lines behind *Show more* through `cutText`; **Do it**, which calls
`proposalStart(id, chatModeOf(mode))` and, on its answer, `onStarted(chat.id)`; **Dismiss**,
which calls `proposalDismiss(id)`. Both are disabled in flight and handed back on an error,
which `errorReportExchange` reports; the card goes when its `Deleted` lands, so a press whose
answer is lost still clears. The group's `aria-label` is *Proposals*, each card an `article`
labelled by its title.

Two homes mount it, both above the chat list: `DashboardChat`'s `empty` slot, over `ChatNav`,
with `onStarted` the `select(id, true)` its `onCreated` uses; and `AppLayout`'s chat-mode
`nav`, over `ChatNav`, with `onStarted` navigating to `/chat/$chatId` as `chat.tsx` does on a
create. Neither draws it for a window with no cluster.

### 6. Settings: Monitoring

`monitor-settings.tsx`, a Monitoring section in the Settings dialog:

- **Clusters**: one row per cluster the clusters watch knows, its name (or context) and a
  switch bound to `spec.monitoringEnabled`, writing `clusterMonitoringEnabledSet(id, on)`; off
  for every cluster until an agent exists (the column's default).
- **Folders**: a switch, *Share folder grants with the monitor*, drawn off and disabled with
  *Not yet* beside it, and one line: *The monitor sees no folder you granted to a chat. Sharing
  one is a separate switch, and it is not built.* The note calls it a separate explicit switch,
  and no grant reaches the monitor until someone builds it on purpose.

`securityconfig.Settings` gains `Monitor MonitorSettings` with `ShareFolders bool`, always false
and never written in this step: the switch's home. The wire: `monitorSettings: MonitorSettings!`
(`shareFolders: Boolean!`).

## Decisions this step asks for

1. **The per-cluster toggle is `clusters.monitoring_enabled`**, not a list in `securityconfig`.
   The column, `clustersvc.SetMonitoringEnabled` and `clusterMonitoringEnabledSet` exist, it
   goes with the row, and a second home for one fact would drift. Recommended.
2. **The monitor's box has no Memory, Agent or Fetch.** A note is a chat's, an agent needs an
   approver, and `WebFetch` dials from the sidecar. Recommended; the agent's step can widen it.
3. **`proposalStart` runs on the catalog's first model.** The card has no model select, and the
   chat can switch models on its next send. Recommended over a `modelID` argument.
4. **A start the sidecar stopped inside is finished or reopened, never re-sent.** `Recover`
   links the chat when the send committed and reopens the proposal when it did not. Re-sending
   the prompt at start would also finish every interrupted start, but it runs a turn the user
   is not watching, on a press they may have forgotten. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `monitor`: `Session`, `Runner`, `monitorDir`, `tasks`, the folder, the sweep | `monitor/monitor.go`, `monitor/dir.go`, `app/paths.go`, `app/app.go`, their tests | — | Planned |
| 2 | The invariants through the real proxies | `monitor/policy_test.go`, `kubeproxy/`, `egress/` tests | 1 | Planned |
| 3 | `proposals` and `Proposals`: add, start, recover, dismiss, watch, cascade; `chatsvc.ChatForRequest` | `appdb/migrations/0001_init.sql`, `appdb/appdb.go`, `monitor/proposals.go`, `chatsvc/`, `app/app.go`, their tests | — | Planned |
| 4 | The wire and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 3 | Planned |
| 5 | `useProposals`, the card, its two homes | `src/lib/proposals.tsx`, `src/components/widgets/proposal-card.tsx`, `dashboard-chat.tsx`, `src/layouts/app-layout.tsx`, their tests | 4 | Planned |
| 6 | `Settings.Monitor`, the Settings section | `securityconfig/`, `src/components/widgets/monitor-settings.tsx`, `settings-dialog.tsx`, their tests | 1, 4 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1 and 3 at the same time, then 2 and 4, then 5 and 6 at the same time, then 7.

## Tests

**`monitor`**

- `TestTheSessionIsReadOnlyAndAsksNobody`: every field of §1's `Session`, `ChatID` empty and
  `Outside` false among them.
- `TestRunBuildsTheRuntime`: `Dir` is the cluster's folder, `Tasks` refuses, `Agent` and
  `ActionAsker` are nil, and the stand-in's sandboxed Bash call reads and writes the
  workspace; an unwatched cluster is `ErrNotWatched`.
- `TestAMonitorRunsOnlyInASandbox`: a sandbox that does not confine, and none, is `ErrNoSandbox`
  and nothing is made.
- `TestAMonitorWriteIsRejected`, `TestAMonitorReadsASecretRedacted`,
  `TestAMonitorNeverAsksForAHost`, `TestAChatsFoldersNeverReachTheMonitor`: §2's table, each
  through the real proxy with a fake upstream, in `policy_test.go`.
- `TestAChatCannotReadTheMonitorsFolder` and `TestTheMonitorCannotReadAChatsFolder`, in
  `monitor_unix_test.go`, through the real sandbox.
- `TestTheFolderGoesWithTheCluster`: a marked cluster's folder is removed, and a start sweep
  removes one naming no cluster.
- `TestProposalsAddAndWatch`: an `Added` after the `Bookmark`, for the cluster asked alone.
- `TestStartCreatesTheChatAndSendsThePrompt`: the chat is on the cluster in the mode asked with
  the prompt as its first user message, the row is `started` with the chat's id, a second start
  is refused, and the watch delivers `Deleted`; `TestAFailedSendReopensTheProposal`.
- `TestRecoverFinishesOrReopensAnInterruptedStart`: a `started` row whose key a message holds
  is linked to that message's chat and its key cleared; one whose key no message holds is
  `open` again with no key, and nothing is sent; a started row whose chat was deleted
  (`chat_id` null, no key) is left alone.
- `TestDismissPutsItAway`, and a dismissed one cannot be started;
  `TestProposalsGoWithTheCluster`, the cascade.

**`securityconfig`**

- `TestShareFoldersIsFalseAndUnwritten`.

**Webview** (`proposal-card.test.tsx`, `monitor-settings.test.tsx`, `dashboard-chat.test.tsx`,
`app-layout.test.tsx`)

- Nothing draws before the `Bookmark`; a card per open proposal, newest first; the reason as
  text with markdown left literal; Do it calls `proposalStart` and `onStarted` with the chat's
  id; Dismiss calls `proposalDismiss`; a `Deleted` removes the card; both homes mount it over
  the list and neither without a cluster.
- The clusters' switches call `clusterMonitoringEnabledSet`; the folders switch is off, disabled
  and says *Not yet*.

## Security

This step adds a session that holds less than any chat's: no prompt can widen it, no grant
reaches it, and its writes are refused at the proxy by its mode before any asker is looked for.
What a hijacked monitor can do: read everything its cluster serves but Secret data, and write
its own workspace. What it cannot: change the cluster; reach a host the kubeconfig did not name;
reach a chat's files or a credential; run outside the sandbox; or ask anyone. The proposal is
text the user reads before a chat runs it, and the chat asks as any chat does.

Residuals: a hijacked monitor can propose in persuasive words, and the user's guard is reading
the reason and the chat's requests; a cluster read leaves the machine on the monitor's word,
as on a chat's (the existing *by decision* row), and unattended. The record,
`docs/security/<date>-the-monitoring-session.md`, argues this and names the tests.

## When it lands

- **The security record** above, and an ADR: the monitor is a session under the chat's
  proxies, refused by mode and not by kind; a proposal is text and the chat is the gate.
- **`security-model.md`**: rows for the monitor session (§2's tests), its folder and the proposal.
- **`sidecar/CLAUDE.md`**: the `monitor` package, its folder in the directory tree, `Session`,
  `Runner`, `Proposals` and the table, the wire. **Root `CLAUDE.md`**, *Chat* and the Settings
  dialog: `useProposals`, the card and its two homes, the Monitoring section.
- **The note's** *Where this meets the code*: decision 3 has its plumbing. **`docs/TODO.md`**: a
  line for the monitoring agent and the findings view.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the sandbox's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` against a kind cluster, with a dev build's hook that calls
`Proposals.Add` for the window's cluster: read the card above the chat list on both panes;
press Do it and read a chat open on the cluster with the prompt as its first question, its
`kubectl delete` asking as any chat's does; press Dismiss on another and read it go. Turn the
cluster's monitoring on in Settings and read the switch hold.
