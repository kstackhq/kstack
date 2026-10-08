---
title: A view the sidebar draws
scope: sidecar, frontend
status: Planned
---

# A view the sidebar draws

**Needs:** nothing beyond `main`, and the log viewer (see *The viewer is assumed* in the
[README](README.md)). **Unblocks:** every later rung.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Say *tail webapp logs* in chat mode, and the log viewer opens in the right sidebar on the
`webapp` deployment, following at the end. Say *logs from yesterday* and it opens anchored at
yesterday's midnight. The model reads one line about what opened and answers in a sentence.

This rung builds the thinnest slice through every layer that the rest of the ladder hangs off:

- **Schema.** `LogsView` in `ToolActionKind`, and `LogsViewAction` on `ToolAction`: the sources,
  their filters, the grep, and the anchor.
- **Sidecar.** `tools/logsview`: a tool that reads the call's arguments, checks the sources
  against the mirror, writes the action, and answers a one-line receipt. It streams nothing to
  the model and asks no one.
- **Webview.** The **focused view**, held beside the right sidebar's state; chat mode's right
  sidebar mounting the viewer off it; and the transcript drawing a `LogsView` call as a one-line
  card whose Expand sets it. The newest `LogsView` call of the open chat sets the view as it
  arrives, so the model's call opens the panel without a click.

It exercises the two decisions that are hard to change later — a view is a record the model
makes and the webview draws, and the focused view is window state the sidebar follows — and
nothing after it changes that shape.

## What is not in this rung

- **No lines inline.** The card is one line. Rung 2 adds the compact form.
- **The model learns nothing about the screen.** Rung 3 attaches the snapshot.
- **No `LogsRead`.** The model cannot read logs through a tool of ours in this rung; `kubectl
  logs` in Bash is what it has, as today. Rung 4 adds the read.
- **No dashboard target.** On the dashboard a `LogsView` call draws its card, and Expand does
  nothing there until rung 7. The card says so.
- **No change on Windows** beyond what every platform gets: the tool reads the mirror, not a
  sandbox.

## Design

### 1. The action

`LogsViewAction` is what the log backend takes, never what the model typed:

```graphql
"A live view of logs the user is looking at, as the sidecar hands it to the log backend."
type LogsViewAction {
  "What the view reads, in the order asked. Their lines are merged by timestamp."
  sources: [LogsViewSource!]!
  "What the view keeps of its sources' lines. Empty keeps every line."
  filters: [LogsViewFilter!]!
  "A regular expression each line's message must match; empty for none. The model wrote it, so draw it as text."
  grep: String!
  anchor: LogsViewAnchor!
  "Whether the viewer keeps the end in view as lines arrive. Lines arrive either way; the user can unpin it by hand."
  pinToEnd: Boolean!
}

"Where a view opens: an edge of the stream, or a moment in it."
type LogsViewAnchor {
  kind: LogsViewAnchorKind!
  "The moment. Set for At alone."
  at: Time
}
enum LogsViewAnchorKind { Head Tail At }

"One resource whose logs a view reads."
type LogsViewSource {
  namespace: String!
  kind: LogsViewSourceKind!
  name: String!
  "The containers read, by exact name: the default container the sidecar resolved, or the ones named. Empty reads every container."
  containers: [String!]!
  "The instance before the last restart, rather than the running one. Finished: nothing arrives on it."
  previous: Boolean!
}
enum LogsViewSourceKind { Pod Deployment StatefulSet DaemonSet Job CronJob ReplicaSet }

"One filter on a view's lines: the values its field may take."
type LogsViewFilter {
  field: LogsViewFilterField!
  "The values allowed. Never empty."
  values: [String!]!
}
enum LogsViewFilterField { Node Region Zone Os Arch }
```

A source names a pod or a workload, and which pods a workload is at any moment is the backend's
to follow, so the action does not change when a pod goes or arrives. The model types a source as
`<kind>/<name>`, as kubectl spells one, and the tool parses it once, so the webview parses
nothing.

A source reads its default container, as `kubectl logs` does: the one the
`kubectl.kubernetes.io/default-container` annotation names on the pod or the workload's pod
template, else the first. The tool resolves it off the mirror and writes the exact name, so the
record says what was shown, and the receipt names the containers it left out, so the model can
widen on its next call. `all_containers` writes the empty list, which stays live for a workload
whose template changes.

`previous` is per source and applies to each container the source shows; a container that never
restarted has no previous instance and contributes nothing, so a healthy sidecar never fails the
view. *Why did it crash* is one view of a pod's previous instance and its running one as two
sources, merged, with the restart as the seam, and one container's previous instance beside
another's current one is the same shape. The mirror's `containers` table says whether a previous
instance exists, so the tool refuses a pod source none of whose containers restarted before
anything is opened, and a workload's receipt counts the pods that have one.

### 2. The tool

`tools/logsview`, built like `tools/kubequery`: a `Definition()` from `prompts/schema.json` and
`prompts/description.md`, a `Prompt()` from `prompts/logsview.md`, an `ActionKind()` of
`tools.ActionLogsView`, and a `Run` that parses, checks, writes the action and answers.

**Arguments.** `description` (what the view is for, drawn as every description is); `sources`
(one or more `{ namespace, resource, containers?, all_containers?, previous? }`, the resource as
`<kind>/<name>`, the containers exact names, `all_containers` refused beside `containers`);
`filters` (optional: `node`, `region`, `zone`, `os`, `arch`, each a list of values, carried on
the action as one `LogsViewFilter` per field set); `grep` (optional, a regular expression over
each line's message); `anchor` (optional: `tail`, the default, `head`, an RFC 3339 time, or a
duration like `2m` back from the sidecar's clock); `pin_to_end` (optional, default false). The
schema says every call opens or replaces the user's view and that the model reads only a receipt.

**Checks.** Through `cluster.Service` over the chat's cluster, as KubeQuery reads the mirror: each
source's kind is one the backend reads and its object is in the mirror; each container named is
one of the source's pods', and with none named the default is resolved; a `previous` pod source
has a container that restarted, and a container of it that did not contributes nothing; `grep` compiles; `anchor` reads. Each failure is a refusal the
model reads, and the call has no action.

**The receipt.** One line: `Viewing deployments/webapp in prod from 2026-10-08T14:02:00Z`. The
anchor reads `from the start`, `at the newest line` or `from <time>`, then `pinned to the end`
when pinned; `and 2 more sources`, `defaulted container app out of app, istio-proxy` for a source
defaulted among several, `containers app, istio-proxy` for the ones named or all, `previous
instance` or `previous instance of 2 of 3 pods`, and `matching /error/` follow as the call sets
them. Nothing else. The tool reads no log line.

**The record.** The row's `action` is the `LogsViewAction` of §1; `actionKind` is `LogsView`.
Nothing is written anywhere else: the view lives in the call row.

**The prompt.** One paragraph in `prompts/logsview.md`, folded into the system prompt where the
other tools' are: the user sees every call as a live view in their window; call it whenever they
ask to see, tail or search logs; a view reads one or more resources merged by timestamp, with
filters and a regex grep; *tail the logs* is `anchor: tail, pin_to_end: true` and *the last two
minutes* is `anchor: 2m` alone; to narrow or move the view, call again; do not describe lines you
have not read. The `kstack-logs` skill is left as it is in this rung.

### 3. The focused view

`src/lib/logs-view.tsx`: `LogsViewProvider` and `useLogsView()`, mounted in `AppLayout` beside
`RightSidebarProvider`, holding `{ chatId, callId, action } | null` with `set` and `clear`. It is
chrome, not what the window is looking at, so it is out of the URL and not persisted: a new
window opens with none. `set` also opens the right sidebar, since a view set with the panel closed
is a view nobody sees.

**The newest call sets it.** `OpenChat` watches the chat's messages already; an effect over them
finds the newest `LogsView` call with an action whose id is not the one last seen, and sets the
focused view to it. It runs in chat mode alone (rung 7 decides the dashboard). A pane that
unmounts leaves the view in place: the panel is the window's, and the user may be reading it.

### 4. The sidebar

Chat mode's right sidebar draws the viewer when the focused view is set, else the placeholder it
draws today. The viewer is mounted off the action — sources, filters, grep, anchor — with a header
naming them through `VisibleText`, and a close that clears the focused view. The panel does not
scroll; the viewer owns its scroller, as the transcript does.

**Width.** The viewer wants more than a transcript: `WIDTH.max` in `right-sidebar.tsx` is raised
for chat mode if the viewer needs it, under its own key as the widths already are.

### 5. The card

In `chat-transcript.tsx`, a `LogsView` call draws a card instead of the closed disclosure: one
line, `Logs: Deployment webapp in prod from 14:02` (`and 2 more` for more sources), each name
through `VisibleText`, then
**Expand**, which sets the focused view to this call. The card of the focused call says
*Showing in the sidebar* in Expand's place. A call with no action (a refusal) draws as every
refused call does, the kind through `actionKindLabel` as `Logs`. On the dashboard the card draws
and Expand is absent.

## Decisions this rung asks for

- **Two tools, and this rung builds only the view.** The model has no way to read logs through
  a tool of ours until rung 4. Reason: the receipt-only tool is the piece every later rung hangs
  off, and `kubectl logs` in Bash covers the model's reading meanwhile.
- **The action is the backend's input, sources and all.** Reason: the backend follows a
  workload's pods itself, so resolving them in the sidecar would freeze a view the backend keeps
  live, and the viewer hands the action on without translating.
- **`pinToEnd` is on the action.** Reason: whether the viewer keeps the end in view cannot be read
  off the anchor — *tail the logs* and *show me the latest* both open at the tail — and a viewer
  that pins itself on finding the viewport at the end guesses wrong for a short window that fills
  in. Only the model heard which was asked. It is named for the viewport, not `follow`, since `-f`
  means "keep the stream open" everywhere the model has read it, and lines arrive here either way.
- **Containers are the source's, by exact name, the default one by default.** Reason: a container
  belongs to a pod, where the node filters belong to where a line ran; the default-container
  annotation is the app's author saying which container is the app, and a sidecar can bury the
  app's lines at a rate no labelling makes readable, so the view follows `kubectl logs` and the
  receipt says what it left out; and the mirror holds the exact names, so a pattern would add a
  matcher for a need the receipt already meets.
- **`previous` is the source's.** Reason: it is one container instance's finished log, and the
  view a user wants of a crash is the previous and the running instance of one pod merged.
- **The anchor has a head.** Reason: *from the start* is a real request whose time only the backend
  knows, so it is an edge, not a timestamp, and an Expand on an old tail card lands at the live
  end rather than at the moment the card was made.
- **The focused view is not persisted.** Reason: a view is a call of a chat; a window that
  reopens on a chat can expand the card again, and a persisted call id can point at a deleted
  chat.
- **`set` opens the panel.** Reason: the model's call must put the view in front of the user
  with no click, which is the point of the rung.
- **No change to the `kstack-logs` skill.** Reason: the skill fetches for the model; the prompt
  paragraph is enough for the model to prefer the view when the user wants to see logs. Rung 4
  reconciles them.

## Tasks

Paths are under `sidecar/internal/` unless rooted.

**Status.** The first PR on `wip/log-views` lands the skeleton: task 1 whole; task 2 as the
package with its offer and prompt, `parse` and `resolve` refusing as not implemented, and not yet
registered; task 3 whole; task 4's selection and label, not the effect; task 5 with a line in the
viewer's place; task 7's paragraphs for what landed. Task 6, the effect, the registration and
the tool's body follow.

1. **Schema and kind.** `sidecar/graph/schema.graphqls`: `LogsView` in `ToolActionKind`,
   `LogsViewAction`, and `logsView` on `ToolAction`. `tools/tool.go`: `ActionLogsView` and the
   Go `LogsViewAction`. `sidecar/graph/schema.resolvers.go` maps both; `enumOfKind` in
   `schema.resolvers_test.go` gains the pair. `gqlgen generate`, then root `pnpm codegen`.
2. **The tool.** `tools/logsview/{logsview.go,resolve.go,prompts/}`: §2. Registered in
   `agent/catalog/catalog.go`'s `ours` and built in `app/app.go` beside `kubequery.New`.
3. **The focused view.** `src/lib/logs-view.tsx` (§3); mounted in `src/layouts/app-layout.tsx`.
4. **The selection and the effect.** `src/lib/chats.tsx`: select `logsView { sources filters grep
   anchor }`, add `LogsView: 'Logs'` to `ACTION_KIND_LABELS`. The newest-call effect in
   `src/components/widgets/chat-pane.tsx`'s `OpenChat`.
5. **The sidebar.** `src/components/widgets/right-sidebar.tsx`: the viewer off the focused view
   (§4), a `logs-view-panel.tsx` beside it holding the header and the viewer mount.
6. **The card.** `src/components/widgets/chat-transcript.tsx` (§5).
7. **Docs.** Root `CLAUDE.md` (Chat, the right sidebar's chat-mode paragraph) and
   `sidecar/CLAUDE.md` (the tool's paragraph beside KubeQuery's), per *When it lands*.

## Tests

Go, beside the files they cover:

- `tools/logsview/logsview_test.go`: a source whose object is in the mirror passes, and one that
  is not refuses with no action; a kind the backend does not read refuses; a container no pod of
  the source has refuses; no container named resolves the annotation's, else the first, and a
  one-container pod resolves it with no *defaulted* line; `all_containers` writes the empty list
  and refuses beside `containers`; `previous` on a pod that never restarted refuses, on one whose
  sidecar did not shows the sidecar nothing, and on a workload counts; a `grep` that does not compile refuses; `anchor` as `head`, `tail`, absent
  (tail), a time, and a duration from an injected clock; an unreadable `anchor` refuses;
  `pin_to_end` absent is false; the receipt's spelling with and without filters, a grep and the
  pin; the action carries the checked values and never the model's text.
- `sidecar/graph/schema.resolvers_test.go`: a `LogsView` call serves its action, as
  `TestAKubeQueryCallServesItsQuery` pins KubeQuery's.
- `agent/chat` prompt goldens: the tool's paragraph where the system prompt folds it in.

TypeScript, beside the files they cover:

- `src/lib/logs-view.test.tsx`: `set` opens the right sidebar; `clear` leaves it open; a new
  window starts with none.
- `src/components/widgets/chat-pane.test.tsx`: the newest `LogsView` call with an action sets the
  focused view once, a second frame of the same call does not set it again, a newer call
  replaces it, a call with no action is skipped.
- `src/components/widgets/chat-transcript.test.tsx`: the card's line through `VisibleText` (a
  resource name with a reordering character is spelled out); Expand sets the focused view; the
  focused call's card says *Showing in the sidebar*; a refused call draws as *Logs* with the
  refusal; on the dashboard Expand is absent.
- `src/components/widgets/right-sidebar.test.tsx`: with no focused view the placeholder; with
  one, the viewer mounted at the action's anchor and pin with a header through `VisibleText`;
  close clears it.

## Security

**Widens:** nothing the model can do. The tool reads the mirror, which KubeQuery
already serves, and no log line reaches the model. **What the user sees** is log text, which is
cluster data: the viewer draws every line as text with control sequences stripped, and every name
on the card and the header goes through `VisibleText`. The root `CLAUDE.md`'s *log-tail windows
inherit this* line is what this rung is held to; the viewer's own tests pin the stripping.

**Residual:** a log line can hold a secret, and the view shows it to the user as `kubectl logs`
would. The model reads none in this rung. Whether a read of logs is a permissioned read is rung
4's question, and its row in `docs/security-model.md` is written then.

## When it lands

- Root `CLAUDE.md`, *Chat*: a paragraph on the focused view and the card; the right sidebar's
  chat-mode sentence, which today says it is a placeholder.
- `sidecar/CLAUDE.md`: the `LogsView` tool beside KubeQuery's paragraph.
- This README's row for rung 1 keeps its link until the sequence lands; the spec itself goes
  when the sequence does, per [Working a numbered spec](../README.md#working-a-numbered-spec).
- No ADR yet. *A rich view is a tool call's action* is the sequence's decision, and its ADR is
  written when the sequence lands, with what the later rungs taught.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/tools/logsview ./graph/... ./internal/agent/...`;
`gqlgen generate`, root `pnpm codegen`, `pnpm build`, `make test-js`, `make lint-js`;
`make test-changed` while working.

By hand, in chat mode on a cluster with a Deployment: *tail webapp logs* opens the panel on the
deployment following at the end, and the answer is one sentence; *logs from yesterday* opens it anchored
at midnight and the card's time says so; a pod name that does not exist draws a refused *Logs*
call and no panel; closing the panel and pressing Expand on the card reopens it; on the dashboard
the card draws and nothing opens.
