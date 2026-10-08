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

Say *tail webapp logs* in chat mode, and the log viewer opens in the right sidebar on the pods
that make up `webapp`, following at the end. Say *logs from yesterday* and it opens anchored at
yesterday's midnight. The model reads one line about what opened and answers in a sentence.

This rung builds the thinnest slice through every layer that the rest of the ladder hangs off:

- **Schema.** `LogsView` in `ToolActionKind`, and `LogsViewAction` on `ToolAction`: the resolved
  namespace and pods, the grep, and the anchor.
- **Sidecar.** `tools/logsview`: a tool that reads the call's arguments, resolves the selector
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

`LogsViewAction` is what was shown, never what the model typed:

```graphql
"A live view of logs the user is looking at. Every field is resolved by the sidecar."
type LogsViewAction {
  namespace: String!
  "The pods the selector matched against the mirror, in name order."
  pods: [String!]!
  "The text each line must hold; empty for none. The model wrote it, so draw it as text."
  grep: String!
  "Where the view opens. The viewer follows at its end, so an anchor at now is a tail."
  anchor: Time!
}
```

`pods` is resolved at the call, so a view opened on a Deployment is the Deployment's pods then.
What the viewer does when a pod goes or a new one arrives is the viewer's own, and the action
does not change. *Open for rung 2 or later:* whether the action should also keep the selector
the model named, so a later call or the viewer can re-resolve it.

### 2. The tool

`tools/logsview`, built like `tools/kubequery`: a `Definition()` from `prompts/schema.json` and
`prompts/description.md`, a `Prompt()` from `prompts/logsview.md`, an `ActionKind()` of
`tools.ActionLogsView`, and a `Run` that parses, resolves, writes the action and answers.

**Arguments.** `description` (what the view is for, drawn as every description is); `namespace`;
one of `pod`, `deployment`, `statefulset`, `daemonset` or `selector` (a label selector); `grep`
(optional); `since` (optional — an RFC 3339 time or a duration like `1h`, resolved against the
sidecar's clock; absent means now). The schema says every call opens or replaces the user's view
and that the model reads only a receipt.

**Resolution.** Through `cluster.Service` over the chat's cluster, as KubeQuery reads the mirror:
a workload's pods are the ones its selector matches in the mirror, a pod is checked to exist. No
match is a refusal the model reads, and the call has no action. The clock is the sidecar's, and
a `since` it cannot read is a refusal too.

**The receipt.** One line: `Viewing 3 pods of deployment/webapp in prod from 2026-10-08T14:02:00Z`,
with `matching "error"` when a grep is set. Nothing else. The tool reads no log line.

**The record.** The row's `action` is the `LogsViewAction` of §1; `actionKind` is `LogsView`.
Nothing is written anywhere else: the view lives in the call row.

**The prompt.** One paragraph in `prompts/logsview.md`, folded into the system prompt where the
other tools' are: the user sees every call as a live view in their window; call it whenever they
ask to see, tail or search logs; to narrow or move the view, call again; do not describe lines you
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
draws today. The viewer is mounted off the action — namespace, pods, grep, anchor — with a header
naming them through `VisibleText`, and a close that clears the focused view. The panel does not
scroll; the viewer owns its scroller, as the transcript does.

**Width.** The viewer wants more than a transcript: `WIDTH.max` in `right-sidebar.tsx` is raised
for chat mode if the viewer needs it, under its own key as the widths already are.

### 5. The card

In `chat-transcript.tsx`, a `LogsView` call draws a card instead of the closed disclosure: one
line, `Logs: 3 pods of webapp in prod from 14:02`, each name through `VisibleText`, then
**Expand**, which sets the focused view to this call. The card of the focused call says
*Showing in the sidebar* in Expand's place. A call with no action (a refusal) draws as every
refused call does, the kind through `actionKindLabel` as `Logs`. On the dashboard the card draws
and Expand is absent.

## Decisions this rung asks for

- **Two tools, and this rung builds only the view.** The model has no way to read logs through
  a tool of ours until rung 4. Reason: the receipt-only tool is the piece every later rung hangs
  off, and `kubectl logs` in Bash covers the model's reading meanwhile.
- **`pods` resolved at the call, selector not kept.** Reason: the action is what was shown, and
  this rung has nothing that re-resolves. Revisit when something does.
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

1. **Schema and kind.** `sidecar/graph/schema.graphqls`: `LogsView` in `ToolActionKind`,
   `LogsViewAction`, and `logsView` on `ToolAction`. `tools/tool.go`: `ActionLogsView` and the
   Go `LogsViewAction`. `sidecar/graph/schema.resolvers.go` maps both; `enumOfKind` in
   `schema.resolvers_test.go` gains the pair. `gqlgen generate`, then root `pnpm codegen`.
2. **The tool.** `tools/logsview/{logsview.go,resolve.go,prompts/}`: §2. Registered in
   `agent/catalog/catalog.go`'s `ours` and built in `app/app.go` beside `kubequery.New`.
3. **The focused view.** `src/lib/logs-view.tsx` (§3); mounted in `src/layouts/app-layout.tsx`.
4. **The selection and the effect.** `src/lib/chats.tsx`: select `logsView { namespace pods grep
   anchor }`, add `LogsView: 'Logs'` to `ACTION_KIND_LABELS`. The newest-call effect in
   `src/components/widgets/chat-pane.tsx`'s `OpenChat`.
5. **The sidebar.** `src/components/widgets/right-sidebar.tsx`: the viewer off the focused view
   (§4), a `logs-view-panel.tsx` beside it holding the header and the viewer mount.
6. **The card.** `src/components/widgets/chat-transcript.tsx` (§5).
7. **Docs.** Root `CLAUDE.md` (Chat, the right sidebar's chat-mode paragraph) and
   `sidecar/CLAUDE.md` (the tool's paragraph beside KubeQuery's), per *When it lands*.

## Tests

Go, beside the files they cover:

- `tools/logsview/logsview_test.go`: a deployment resolves to its pods in name order; a pod that
  is not in the mirror refuses with no action; `since` as a time, as a duration, and absent
  (now, from an injected clock); an unreadable `since` refuses; the receipt's spelling, with and
  without a grep; the action carries the resolved values and never the model's text.
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
  pod name with a reordering character is spelled out); Expand sets the focused view; the
  focused call's card says *Showing in the sidebar*; a refused call draws as *Logs* with the
  refusal; on the dashboard Expand is absent.
- `src/components/widgets/right-sidebar.test.tsx`: with no focused view the placeholder; with
  one, the viewer mounted with the action's values and a header through `VisibleText`; close
  clears it.

## Security

**Widens:** nothing the model can do. The tool reads the mirror's pod list, which KubeQuery
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

By hand, in chat mode on a cluster with a Deployment: *tail webapp logs* opens the panel on its
pods following at the end, and the answer is one sentence; *logs from yesterday* opens it anchored
at midnight and the card's time says so; a pod name that does not exist draws a refused *Logs*
call and no panel; closing the panel and pressing Expand on the card reopens it; on the dashboard
the card draws and nothing opens.
