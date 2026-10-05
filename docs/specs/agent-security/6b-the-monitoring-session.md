---
title: The monitoring session
scope: sidecar
status: Planned
---

# The monitoring session

**Needs:** step 5A, whose `NoPrompts` and `NoSecretData` keep Secret data redacted and a prompt
unasked; step 4C, whose `Session.Network` a monitor leaves nil. **Unblocks:** the monitoring
agent, a step of its own (*What is not in this step*).

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

[The note](../../notes/sandbox-credentials-and-permissions.md) has two agents: a chat agent the
user talks to, and a monitoring agent that reads the cluster in the background and changes
nothing. Today there is no monitor: `session.Kind` names `Monitor` and nothing builds one,
`agent_runs.trigger` names `monitor` and no row carries it, and `clusters.monitoring_enabled` is
served with nothing running on it. After this step:

- **A monitor run is a run of the chat service.** `chatsvc.Service.RunMonitor(ctx, clusterID,
  target, brief)` takes one `agent.Run` over the chat's own loop, box, Bash tool, sandbox and
  cluster proxy, under a session that holds less than any chat's: read-only at the proxy, Secret
  data never, no prompts, no network, no folder grants, and a workspace of its own under
  `<data>/monitor/<cluster id>/`. It refuses a machine with no sandbox, a cluster that is marked
  or not watched, and a second run on a cluster while one runs.
- **Its record is the chat's record.** The run is an `agent_runs` row with `trigger = 'monitor'`,
  no chat and its cluster; its `llm_calls`, `tool_calls` and `approvals` rows hang under it as a
  chat's do, written by the same journal. Every command it ran, every cluster write the proxy
  refused and every Secret read it redacted is a row, since a monitor reads injected content
  unattended and is the agent whose trail matters most.
- **Nothing schedules a run.** What the monitor is told, when it runs, what it looks for and how a
  finding reaches the user are the agent's step. This step is the session, the run, the record,
  the folder and its teardown, each exercised through the real pieces in tests, and the runner
  wired into the app so its folder is swept and its runs end with the service.

## What is not in this step

- **No monitoring agent.** Its schedule, its brief, its prompt, what it keeps between runs and
  how its model is picked are a design of their own. `RunMonitor` is what that agent calls, and
  the tests here are its only caller.
- **No proposals and no proposal card.** They go to the agent's step with the two facts a review
  of this step's earlier draft found, so it starts from them. First, the draft of the chat that
  has not started is keyed `new:<mode>` (`outboxKey`, `src/lib/chat-outbox.tsx`), not per
  cluster, and its composer outlives a cluster switch, so a prompt joined onto it under cluster A
  can be sent to cluster B, where under `Auto` a class 4 write runs unasked: the join must carry
  the proposal's cluster, and Send must hold while the window is on any other. Second, a
  proposal's prompt is text the monitor wrote from cluster data: the card draws it through
  `VisibleText`, and it becomes a question only as the user's own draft, never sent on a press.
- **No Settings section.** The per-cluster switch exists on the row (`clusters.monitoring_enabled`,
  `clustersvc.SetMonitoringEnabled`, `clusterMonitoringEnabledSet`) and stays undrawn: a switch
  with nothing behind it says something is running when nothing is. The agent's step draws it,
  with the note's separate switch for sharing folder grants with the monitor. Until then no
  cluster is watched but by a test, and no folder grant reaches the monitor.
- **Nothing on the wire.** No query, mutation, type or codegen. The schema edit is `app.db`'s.
- **No `monitor` package.** The run's record, journal, gate and teardown are `chatsvc`'s and
  unexported (§1), so the run is built there. The sequence README's vocabulary row for `monitor`
  and its 6B row say a package and a card; both are corrected in this spec's PR.
- **Windows.** No sandbox, so `RunMonitor` answers `ErrNoSandbox` there, as on any machine with
  none. The schema edit and the lifecycle work everywhere.

## Design

### 1. Where a monitor run lives

Three facts of the code decide it:

- **The audit trail keys on a run, not a chat.** `llm_calls.run_id` references `agent_runs`,
  `tool_calls.llm_call_id` references `llm_calls`, and `approvals.tool_call_id` references
  `tool_calls` (`appdb/migrations/0001_init.sql`). Only `messages` and `background_tasks` need a
  chat. `agent_runs.chat_id` is nullable, `trigger` already names `'monitor'`, the table's
  comment says *The monitor's runs are to come*, and `failStrandedRuns` (`chatsvc/store.go`)
  already scans `chat_id` as a `NullString`. The row was designed for this.
- **The journal is unexported.** `runJournal` (`chatsvc/turn.go`) is the `agent.Recorder` and
  `agent.Approver` of a run and the journal the proxies' asker writes through (`askAction`,
  `recordAction` in `approval.go`, under `journalMu` and `askMu`); `boxFor`, the statements and
  the store helpers are the service's. Building the run anywhere else means exporting the
  journal or rebuilding it.
- **A run with no message of its own already exists.** `subagent.go` builds one: a
  `runJournal` with its own `publish`, an `agent.Turn` whose one message is the cluster card and
  a brief, `agent.Run(ctx, spec, c, c)`, and a settle through `settleRun` and `writeCalls`.

So the monitor run is built in `chatsvc`, in `monitor.go`, line for line as `subagent.go` builds
a subagent, with no chat and no task. The schema gives the row its cluster:

```sql
-- agent_runs (0001_init.sql, edited in place under the pre-release schema policy)
  chat_id         TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  cluster_id      TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  ...
  CHECK ((trigger = 'monitor') = (chat_id IS NULL)),
  CHECK ((trigger = 'monitor') = (cluster_id IS NOT NULL))
) STRICT;
CREATE INDEX agent_runs_cluster_idx ON agent_runs (cluster_id);
```

`cluster_id` is NULL on a chat's and a subagent's run, whose cluster is their chat's, and set on a
monitor's alone, so a cluster's delete cascades its monitor runs and their rows as it cascades its
chats'. The table comment's last sentence becomes: *A monitor's run (trigger 'monitor') is
inserted queued under its cluster, with no chat and no message; task is its brief and result its
report.* `runColumns` names its columns, so nothing positional moves. Three statements join
`statements.go`:

- `stmtInsertMonitorRun`: `INSERT INTO agent_runs (id, agent_type, app_version, trigger,
  cluster_id, provider, model, effort, dialect, task, status, created_at) VALUES (?, 'monitor',
  ?, 'monitor', ?, ?, ?, ?, ?, ?, 'queued', ?)`, on the writer.
- `stmtSelectClusterMonitored`: `SELECT monitoring_enabled FROM clusters WHERE id = ? AND
  delete_requested_at IS NULL`, on both: no row is a cluster marked or gone, `0` one not watched.
- `stmtSelectClusterIDs`: `SELECT id FROM clusters WHERE delete_requested_at IS NULL`, on the
  reader: the live rows a monitor may be kept under (§4).

→ [ADR: schema edit, not migration](../../adr/2026-08-29-schema-edit-not-migration.md).

### 2. The session

`monitorSession()` in `grants.go`, beside `sessionFor`:

```go
// monitorSession is the monitor's session: it reads the cluster through the
// proxy and nothing else. Every field it leaves zero is read by something.
func monitorSession() session.Session {
	return session.Session{Kind: session.Monitor, NoPrompts: true, NoSecretData: true}
}
```

What each field does, by the reader that reads it:

| Field | Reader | Effect |
| --- | --- | --- |
| `Kind: Monitor` | nothing: no proxy and no tool reads `Kind` | the monitor is refused by what follows, never by its kind (the note's decision 17) |
| `Outside: false` | `bash.Tool.sandboxerFor` | the run is sandboxed; a boxer that does not confine is refused before anything is made (§3) |
| `Policy: nil` | `kubeproxy.Grant.policy` | `{Mode: ReadOnly}` with no rules, whatever Settings' mode for the context; no `chat_grants` row and no always rule reaches it |
| `Network: nil` | `bash.networkFor` at the gate; `sandboxedRunFor` | a `network: true` call is refused with *This session never has network.*; the run's policy leaves `Internet` false |
| `Folders: nil` | `Session.GrantedFolders` | no folder, so `workspacePolicy` gets the system zones and the run's own paths alone |
| `NoPrompts`, `NoSecretData` | `Grant.policy` copies them onto the `permissions.Policy`; `Authorize` reads them | class 6 is refused ahead of every rule; what would have asked is refused instead |

### 3. The run

`Service` gains one method, and `monitor.go` holds it:

```go
// MonitorResult is how a monitor run ended: its row, its status, the report
// (the text of its last reply, empty unless it succeeded) and its error text.
type MonitorResult struct {
	RunID  RunID
	Status RunStatus // Succeeded, Failed or Cancelled
	Report string
	Error  string
}

// RunMonitor takes one monitor run on clusterID: a run of target over brief,
// on the calling goroutine, settled when it returns. ErrNoSandbox where no
// sandbox confines a run; ErrClusterGone for a cluster marked or gone;
// ErrNotWatched for one whose monitoring_enabled is off; ErrMonitorInFlight
// while the cluster has a run; ErrBadRequest for an empty brief, one over
// maxBriefLen, or a target that takes no tools; ErrStopping once the service
// is stopping. The run's rows are written whatever it answers, once the run
// was inserted.
func (s *service) RunMonitor(ctx context.Context, clusterID apimeta.ClusterID, target llm.Target, brief string) (MonitorResult, error)
```

`ErrNoSandbox`, `ErrNotWatched` and `ErrMonitorInFlight` are new errors in `service.go`;
`RunStatus` is `runStatus` exported with its three settled values, since the result names one.
`maxBriefLen` is 4,000 bytes and `maxMonitorToolCalls` 16, as `maxSubagentToolCalls` is.

**In order:**

1. **Refuse before anything is made.** `!s.sandboxStatus.Available` is `ErrNoSandbox`: a monitor
   never runs unconfined, and a machine with no sandbox has nothing to confine it. The brief and
   `s.boxFor(target).Empty()` are checked next.
2. **Enter and take the slot.** `s.enter()` (`ErrStopping`), so `stop` waits for the run. Under
   `turnsMu`, `s.monitors[clusterID]` holds the one run per cluster, a `monitorRun{cancel, done}`;
   one there is `ErrMonitorInFlight`. The run's context is the caller's, cancelled with `s.ctx`
   through `context.AfterFunc`, and the sweeper cancels it when the cluster is marked (§4).
3. **Insert the run.** One transaction: `stmtSelectClusterMonitored` (no row `ErrClusterGone`,
   `0` `ErrNotWatched`, as a send checks its cluster's mark inside its own transaction), then
   `stmtInsertMonitorRun` queued, `task` the brief, `agent_type` `monitor`.
4. **Build the journal.** `&runJournal{s: s, runID: run.ID, target: target, publish:
   func(MessageStatus) {}}`: no `chatID`, no `agentCallID`, so `isTurns()` holds and
   `LLMCallStarted` claims the queued row on its first round as a turn's does; nothing watches a
   monitor run, so `publish` does nothing. The journal is wrapped in `monitor`, which is the
   run's `agent.Recorder` and `agent.Approver`:
   - `Progress` does nothing, as the subagent's does.
   - `Settled` keeps the outcome: `status, errText = runOutcome(err)`, `report =
     lastReplyText(res.Blocks)` on success.
   - **`Approve` answers `false, nil` and writes nothing.** The loop then answers the call
     `{"error":"denied"}` and `ToolCallFinished` writes its one row `failed` with that error and
     no `started_at`. The calls that reach it are `Read`, `Write` and `Edit` outside the
     workspace: a sandboxed Bash call skips the gate, and one asking for network is refused by
     `Approval` itself.
5. **Build the turn.**

   ```go
   spec := agent.Turn{
   	Target: target, SystemPrompt: monitorSystemPrompt(),
   	Messages: []llm.Message{{Role: string(RoleUser), Blocks: subagentMessage(card, brief)}},
   	AffinityKey: string(run.ID),
   	Tools: s.boxFor(target).Without(tools.ActionDelegate, tools.ActionMemory, tools.ActionFetch, tools.ActionStop, tools.ActionSearch),
   	Runtime: tools.Runtime{
   		ClusterID: clusterID, Session: monitorSession(),
   		Dir: s.monitorDir(clusterID), Tasks: monitorTasks{}, Files: runStamps{},
   		ActionAsker: monitorAsker{j: m.runJournal},
   	},
   	MaxToolCalls: maxMonitorToolCalls, DefaultToolTimeout: defaultToolTimeout,
   }
   ```

   `card` is `s.clusterCards.ClusterCard` under `clusterCardTimeout`, what a send attaches to a
   first question, so the model knows the context its commands must name. **It is the card
   alone, not `withMemory`'s join**: a send's card carries the cluster's memory notes, and a note
   the user wrote reads as an instruction to the model, which nobody is here to stand behind.
   Leave the join out on purpose. `ChatID` is empty and
   `Agent` nil: nothing reads the first (`sandboxedRunFor` reads `ClusterID` and `Dir`), and the
   second means no subagent. The box leaves out: `Agent`, which needs an approver; `Memory`,
   whose note a chat would read as the user's cluster memory; `WebFetch`, which dials from the
   sidecar, outside the run's sandbox; `TaskStop`, since the monitor runs no task; and the
   provider's search, which would send a query the model composed from cluster data to a third
   party with nobody watching. `KubeQuery`, `Read`, `Write`, `Edit` and Bash stay: each is
   bounded by the sandbox, the proxy or the workspace.
6. **Run.** `agent.Run(ctx, spec, m, m)`, with the subagent's `recover` around it, so a panic in
   the stream fails the run and not the process.
7. **Settle.** One transaction on `context.WithoutCancel(s.ctx)`: `settleRun` with the outcome,
   then `m.closeOpen("", streamErr, at)` and `m.writeCalls`, so every call row is written whole
   and a call a panic left open is closed, as the subagent's end does. A settle the store refuses
   is logged and left: the next `Start` fails the run as stranded, which is what a crash gets.
8. **Release.** Delete the slot under `turnsMu`, close `done`, answer the result.

**The asker.** `monitorAsker` is the run's `tools.ActionAsker`, so the proxy has one and records
through it:

```go
// monitorAsker records what the proxy decided and never asks. Ask is unreachable
// under NoPrompts, which turns every verdict the user could lift into a refusal;
// an Ask that reached it anyway answers an error, which the proxy refuses the
// write on (refusedUnrecorded) and forwards nothing.
type monitorAsker struct{ j *runJournal }

func (a monitorAsker) Ask(context.Context, tools.ActionRequest) (tools.Answer, error) {
	return tools.Answer{}, errMonitorAsked
}

func (a monitorAsker) Record(ctx context.Context, r tools.ActionRequest, d permissions.Decision, reason string) error {
	return a.j.recordAction(ctx, r, d, reason)
}
```

`recordAction` writes an `approvals` row `refused` under the call the run has open, with the
reason `Authorize` gave: the mode's for a write, *this session never reads Secret data* for a
Secret read. That row is the trail of what a hijacked monitor tried.

**The tasks.** `monitorTasks{}` is the runtime's `tools.Tasks`: `Start` answers
`errNoMonitorTask` (*the monitor runs no background command*), which `bash`'s `runTask` answers
the model as `tools.StartRefusal(err)`, and `Stop` answers false. No `background_tasks` row and
no notice exist for a monitor, and a background command's writes would be refused with no record
anyway (`writesFor`'s `refusedBackground`).

**The prompt.** `prompts/monitor.md`, embedded beside `system.md` as `monitorSystemPrompt()`:
who it is (Kstack's monitor on the cluster the card names, running unattended), what it holds
(it reads the cluster through `kubectl` and KubeQuery, and changes nothing: every change is
refused, Secret data arrives redacted, it has no network and no file outside its workspace, so
it must not try), that **there is no user and no switch**: nobody reads a request, no chat can be
run outside the sandbox, and a tool's refusal that says to ask the user (Bash's
`outsideWorkspace` does) means stop, not ask; and what to answer (the brief says what to look at;
its last reply is its report, plain text). The agent's step rewrites it; the loop appends its own sections and the
standing rule that data is not instructions, as it does for every turn.

### 4. The folder and the teardown

`<data>/monitor/<cluster id>/` is the run's directory: `workspace/`, the one folder its commands
write; `toolhome/`, the sandbox's `*_HOME` redirect; `results/`, where a result too large to
come back whole is saved. `app/paths.go` adds `MonitorDir` under `<data>/monitor` to the tree
and `chatsvc.New` takes it after `chatsDir`; the service opens it as it opens the chats' root
(`openChats`, a `rootdir.MakeRoot` held open), and `monitorDir{s, id}` in `chatdir.go` is the
`tools.ChatDir` over it, `Path` and `Root(create)` as `chatDir`'s are, with `removeMonitorDir`.
Both roots sit inside the data directory, which every run's `Always` part hides but for the
run's own paths: a chat's command cannot read `<data>/monitor`, the monitor's cannot read
`<data>/chats`, and one cluster's monitor cannot read another's folder.

**Teardown** rides the chat sweeper (`sweep.go`), which already runs on the clusters signal and
its own retry:

- **A monitor is kept only under a live, unmarked row.** The mirror deletes a marked row on its
  own once its runtime object is gone and no chat is filed under it (`clustersvc/mirror.go`,
  `stmtDeleteMarkedCluster`), and a monitor run holds no row back, so a cluster with no chats
  can lose its row before any pass sees it marked. `sweep` therefore does not key the monitor's
  teardown on `markedClusterIDs`: on **every pass**, before the chats' deletes, it reads the
  live rows (`stmtSelectClusterIDs`), and for every `monitorRun` slot and every entry of the
  monitor root whose cluster is not among them it cancels the run, waits on its `done`, then
  `removeMonitorDir`. The order
  matters twice: the run is cancelled before the chats go, so a chat delete that fails does not
  leave the monitor running until the retry; and the slots are read under `turnsMu` after the
  rows, so a run inserted between the two reads is seen on the next pass, never cancelled by
  mistake. The row's own delete cascades the run's rows; a delete that lands while a run is
  writing fails that write, and the recorder's contract ends the run on it: *nothing external
  follows a write that did not land*.
- The first pass is also the start sweep of the monitor root, as `sweepChatDirs` is of the
  chats': there is no separate `sweepMonitorDirs`, since every pass removes what names no live
  cluster. The listing is taken before the read, as the chats' is.

`Start` needs nothing new: `failStrandedRuns` fails a monitor run a previous process left queued
or running, and its `RETURNING chat_id` is NULL there, which it already skips. `stop` needs
nothing new: it cancels `s.ctx`, which cancels every run, and `wg` joins them through `enter`.

### 5. How a monitor is refused, layer by layer

A run goes through `agent.Run`, so every gate a chat's call meets, a monitor's meets. Each row is
one thing a hijacked monitor might try, where it stops, what the record says, and the test that
pins it (§Tests). The first three are the note's fifth, sixth and ninth invariants.

| The model tries | Where it stops | The record | Test |
| --- | --- | --- | --- |
| `kubectl delete`, `apply`, `patch`, `scale`, a `DELETECOLLECTION`, a write to `status` or `ephemeralcontainers` | `kubeproxy.serveWrite`: `Authorize` under `{ReadOnly, NoPrompts, NoSecretData}` answers `Refuse`, a 403 naming the mode, nothing upstream | an `approvals` row `refused`, kind `action`, reason the mode's | `TestAMonitorWriteIsRefusedAndRecorded` |
| `exec`, `attach`, `port-forward`, `proxy`, a token request | the proxy's policy, before classification, as for any session (`refusedReach`, `refusedToken`) | the call's row and its output | the same test's last rows |
| `kubectl get secret` | `serveSecretRead`: `Authorize` refuses class 6 ahead of every rule, the response is forwarded redacted | an `approvals` row `refused`, reason *this session never reads Secret data* | `TestAMonitorReadsASecretRedactedAndRecorded` |
| a command with `network: true` | `bash.Tool.Approval`, `networkFor` with `Session.Network` nil: a `tools.Refusal` the loop answers as the result, no approver and no proxy involved | the call's row `failed`, result *This session never has network.* | `TestAMonitorHasNoNetwork` |
| `curl` to anything but the proxy | the sandbox: the run's policy leaves `Internet` false | the command's own error | the same test |
| a `Read` of `~/code`, a `Write` outside the workspace | the gate: `Approve` answers false | the call's row `failed`, `{"error":"denied"}`, no `approvals` row, no waiter | `TestAMonitorCallThatAsksIsDenied` |
| a file in a folder a chat was granted, or granted always | `Folders` nil: the policy holds no grant, the sandbox refuses the open | the command's own error | `TestAChatsFoldersNeverReachTheMonitor` |
| a chat's workspace, or another cluster's monitor folder | the sandbox's `Always` part | the command's own error | `TestTheMonitorCannotReadAChatsFolder` |
| `run_in_background` | `monitorTasks.Start` | the call's row, result *could not start: the monitor runs no background command* | `TestAMonitorBackgroundCommandIsRefused` |
| spawning an agent, saving a memory, fetching a page, searching | not offered: the box leaves the tool out | an unknown tool is the loop's refusal | `TestRunMonitorRecordsARun` checks the offer |

The nil-asker short-circuit in `serveWrite` and `serveSecretRead` is not in the table: a monitor
has an asker. It stays for a background command, which asks nobody and is refused with
`refusedBackground`, and as the backstop it already is (`TestAGrantWithNoAskerRefusesWrites`,
`TestASecretReadWithNoAskerIsRedacted`): a runtime handed no asker still changes nothing.

### 6. What the agent's step gets

A caller: `llmSvc.Resolve(providerID, modelID, effort)` for the target, `RunMonitor` for the
run, `MonitorResult.Report` for what it found, and `agent_runs.result` and `task` for the last
run's report and brief, which is what the agent keeps between runs until it needs more. The
schedule, the brief and the card are its own.

## Decisions this step asks for

1. **A monitor run is a chat-service run with no chat, not a hidden chat and not a table of its
   own.** A hidden chat (a `chats` row in a mode no list shows) would need a synthetic question,
   a title, a mode the `CHECK` and the webview's filters learn, and every list consumer to
   exclude it; a table of the monitor's own would duplicate `llm_calls`, `tool_calls` and
   `approvals` and the journal that writes them, and record nothing a chat's record does not.
   The schema already keys the trail on the run and leaves `chat_id` nullable for exactly this
   (§1). The package's name is stale: `chatsvc` already owns the subagents, the tasks and the
   askers, and the monitor follows the subagent's shape; the rename is a chore of its own in
   `docs/TODO.md`, not this step's. Recommended.
2. **The monitor's asker records and never asks.** With an asker, `serveWrite` reaches
   `Authorize`, whose refusal is recorded; with none it refuses first and records nothing, which
   is what the earlier draft had and why its invariants were read off the code rather than run
   through it. The verdict is the refusing layer, the record is the trail, and the nil-asker
   path keeps its one job, the background command. Recommended.
3. **Proposals and the card go to the agent's step.** No producer exists here, and a card with
   a dev hook behind it is the same thing this step refuses a Settings switch for. Their two
   fixes are recorded above so nothing is lost. Recommended.
4. **No scheduler, and the boundary is pinned by tests through the real pieces.** The one thing
   that differs between a monitor run and a chat's is the session; everything it passes through
   is the chat's production code, and the tests call the production entry (`RunMonitor`) with
   the production wiring: the real loop, the real Bash tool, the real sandbox and the real proxy
   over a fake upstream. A scheduler adds a caller and no boundary; the runner, the folder and
   the sweep are still wired into `app.New`, so the lifecycle runs in production with zero runs.
   Recommended.
5. **`agent_runs.cluster_id`, set on a monitor run alone.** It is what the cascade and the
   agent's later reads need; a chat run's cluster is its chat's and stays NULL, held by a
   `CHECK`. Recommended.
6. **The box leaves out `Agent`, `Memory`, `WebFetch`, `TaskStop` and the provider's search.**
   The first four for the reasons in §3; the last because a search leaves the machine on the
   model's word, which a chat accepts under the user's eye and a monitor would do unattended.
   Recommended; the agent's step can widen it.
7. **The per-cluster toggle is `clusters.monitoring_enabled`**, not a list in `securityconfig`:
   it exists, it goes with the row, and a second home for one fact would drift. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `agent_runs.cluster_id`, its index and `CHECK`s; the three statements; `insertMonitorRun`, `clusterMonitored`, `clusterIDs` in `store.go` | `appdb/migrations/0001_init.sql`, `chatsvc/statements.go`, `chatsvc/store.go`, their tests | — | Planned |
| 2 | `monitorSession`; `MonitorDir` in the tree and `New`'s signature; `monitorDir`, its root and removal | `chatsvc/grants.go`, `chatsvc/chatdir.go`, `chatsvc/service.go`, `app/paths.go`, `app/app.go`, their tests | — | Planned |
| 3 | `RunMonitor`: the errors, the slot, `monitor`, `monitorAsker`, `monitorTasks`, the prompt, `MonitorResult`, the `Service` method | `chatsvc/monitor.go`, `chatsvc/prompts/monitor.md`, `chatsvc/prompt.go`, `chatsvc/service.go`, `chatsvc/monitor_test.go` | 1, 2 | Planned |
| 4 | The teardown: every pass cancels and removes what names no live cluster, before the chats; `Start`'s stranded test | `chatsvc/sweep.go`, `chatsvc/sweep_test.go`, `chatsvc/service_test.go` | 3 | Planned |
| 5 | §5 through the real sandbox and proxy | `chatsvc/monitor_unix_test.go` | 3 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4 and 5 at the same time, then 6.

## Tests

**`chatsvc`**, with the test service's box and `sandboxStatus.Available` set, as `grantable` sets
it, unless the test says the real sandbox.

- `TestTheMonitorSessionIsReadOnlyAndAsksNobody` (`grants_test.go`, beside
  `TestAChatsSessionAsksAndReadsSecretData`): every field of §2's table, and
  `permissions.Policy{Mode: ReadOnly, NoPrompts: true, NoSecretData: true}.Authorize` answering
  `Refuse` for a class 4, 5 and 6 action and for one a rule would have asked about.
- `TestRunMonitorRecordsARun`: the fake provider scripted with one tool round, then a reply. The
  run's row is `trigger = 'monitor'`, `chat_id` NULL, `cluster_id` the cluster's, `task` the
  brief, `queued` then `running` then `succeeded`, `result` the reply's text; one `llm_calls` row
  per round and the call's `tool_calls` row under it; the fake's request carries the card as a
  `context` block, then the brief; the offered tools name none of the five kinds left out; the
  result's `Report` is the reply.
- `TestRunMonitorRefuses`: a table: no sandbox is `ErrNoSandbox` and nothing is written; a
  marked cluster `ErrClusterGone`; `monitoring_enabled` off `ErrNotWatched`; an empty brief and
  one over `maxBriefLen` `ErrBadRequest`; a target whose box is empty `ErrBadRequest`; a second
  run on a cluster while the first is held at the fake's gate `ErrMonitorInFlight`, and a run on
  another cluster meanwhile is not.
- `TestAMonitorCallThatAsksIsDenied`: a `Read` of a path outside the workspace: the row is
  `failed`, `{"error":"denied"}`, no `started_at`, no `approvals` row, `pending` holds no waiter,
  and the run never reads `waiting_approval`.
- `TestTheMonitorsAskerRecordsAndNeverAsks`: `Record` with `Denied` writes an `approvals` row
  `refused` with the reason under the open call; `Ask` answers `errMonitorAsked` and writes
  nothing.
- `TestAMonitorBackgroundCommandIsRefused`: a `run_in_background` call's result is *could not
  start: the monitor runs no background command* and no `background_tasks` row exists.
- `TestAMonitorRunEndsWithTheService`: `stop` during a run held at the fake's gate cancels it,
  the row settles `cancelled`, and `stop` returns.
- `TestAMonitorRunEndsWithItsCluster` (`sweep_test.go`): marking the cluster cancels its run and
  removes its folder before the cluster's chats are deleted, and a chat delete that fails
  (`deleteWrite`) leaves no monitor running; the row's delete cascades the run's rows.
- `TestAMonitorEndsWhenItsRowIsGone` (`sweep_test.go`): the cluster's row is deleted outright
  before the pass, with no mark the sweeper sees; the pass still cancels the run and removes the
  folder, and a run on a live cluster is untouched.
- `TestTheMonitorsFolderIsSwept` (`sweep_test.go`): a folder naming no cluster is removed on the
  first pass, and a live cluster's is kept.
- `TestAStrandedMonitorRunIsFailed` (`service_test.go`): a monitor run left `running` is
  `failed` by `Start`, its open call closed.
- `TestANewMonitorDirIsOwnerOnly` (`chatdir_unix_test.go`): `Root(true)` makes the folder 0700.

**`chatsvc/monitor_unix_test.go`**, through `RunMonitor` with the real Bash tool, the real
sandbox and the real proxy over a fake upstream, as `bash/proxy_unix_test.go` builds them:

- `TestAMonitorWriteIsRefusedAndRecorded`: a table over `POST`, `PUT`, `PATCH`, `DELETE` and
  `DELETECOLLECTION`, and over `scale`, `status`, `ephemeralcontainers` and `binding`, each a
  `kubectl` command the scripted model runs: the proxy answers 403 naming the mode, nothing
  reaches the upstream, and one `approvals` row `refused` is written per write with the mode as
  its reason. The last rows are `exec`, `attach`, `port-forward` and `proxy`, refused before
  classification with no record.
- `TestAMonitorReadsASecretRedactedAndRecorded`: a `GET` of one Secret and a list: every value
  `[redacted]`, and one `approvals` row `refused`, reason *this session never reads Secret
  data*.
- `TestAMonitorHasNoNetwork`: a `network: true` call's row is `failed` with *This session never
  has network.*, the proxy saw no request; a plain call's run policy leaves `Internet` false and
  a listener outside gets no connection, while a chat on the same machine has its switch on.
- `TestAChatsFoldersNeverReachTheMonitor`: a folder granted always and one granted to a chat are
  absent from the run's policy and unreadable in it. Step 4D's row names this test.
- `TestTheMonitorCannotReadAChatsFolder` and `TestAChatCannotReadTheMonitorsFolder`: each a
  command that reads the other's workspace, refused; and a cluster's monitor reading another
  cluster's monitor folder, refused.

## Security

This step adds a session that holds less than any chat's and a run that records as a chat's
does. What a hijacked monitor can do: read everything its cluster serves but Secret data, through
the cluster proxy alone; write its own workspace; write a report the agent's step will read.
What it cannot: change the cluster; read Secret data; reach the network, the cluster's API
server included; read a chat's files, another cluster's monitor folder or any granted folder; run
a background command, a subagent, a memory, a fetch or a search; run outside the sandbox; or ask
anyone. Each refusal is a row (§5), so a hijack leaves a trail.

What holds it: the session's six fields, each read by the code §2 names, and the box. The
monitor is refused by its mode, its `NoSecretData` and its `NoPrompts`, never by its kind, so a
proxy that forgot about monitors would still refuse it. No boundary moves for a chat: `serveWrite`
and `serveSecretRead` are unchanged, and a runtime with no asker is refused as before.

Residuals: a cluster read leaves the machine on the monitor's word, as on a chat's (the existing
*by decision* row), and unattended; a report is cluster data at one remove, and the agent's step
must draw it as text. The record, `docs/security/<date>-the-monitoring-session.md`, argues this
and names the tests, and an ADR records decisions 1, 2 and 4: a monitor run is a run of the chat
service under the chat's own proxies, refused by its session and recorded by its journal.

## When it lands

- **The security record** and **the ADR** above.
- **`security-model.md`**: rows for the monitor session (§5's tests), its folder, and its record;
  the `NoSecretData`/`NoPrompts` row and the folders row stop saying *nothing builds such a
  session yet* and *as a monitor's will*; step 4D's monitor row names
  `TestAChatsFoldersNeverReachTheMonitor`.
- **`sidecar/CLAUDE.md`**: `monitor.go` in the `chatsvc` file list; `RunMonitor`, `monitorSession`,
  `monitorAsker`, `monitorTasks` and the prompt under *Chat*; `<data>/monitor/<cluster id>/` in
  the directory tree with `chatsvc` as its owner; `agent_runs.cluster_id` and the monitor run
  under the record; the sessions paragraph stops saying *built by nothing yet* and *step 6B's
  monitor will set both*. **Root `CLAUDE.md`**: nothing, since nothing reaches the webview.
- **The note's** *Where this meets the code*: decision 3 has its plumbing, and decision 17 names
  the asker. **`docs/TODO.md`**: a line for the monitoring agent, with the schedule, the brief,
  the Settings section and its two switches, the proposal card with its two fixes, and a findings
  view.
- **The sequence's README**: this row's status, its vocabulary row for `monitor`, and the wire
  line's `proposal…`.

## Verification

Run the [verification commands](../README.md#verification-commands); no wire changed, so the Go
checks alone, with the sandbox's tests on Linux and in CI's macOS job. `make test-changed` while
working.

Nothing by hand: nothing in the app calls `RunMonitor` until the agent's step, and
`monitor_unix_test.go` runs the same `kubectl delete`, Secret read, network and folder cases
through the real sandbox and proxy that a hand check would. The agent's step, which adds the
caller, adds the hand check with it.
