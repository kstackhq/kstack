# Log views

The build order for **log views**: the user asks for logs in words, the agent opens a live
view of them, and the view is something the user and the agent look at together. Each step here
says how one rung gets built. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply.

**This is a ladder, not a finished design.** The work is built outside-in: the surface the user
sees first, then what the agent reads, then what runs without anyone looking. Each rung is
specified when it is the next one to build, with what the rungs before it taught. The rungs
below past the next one are a direction, one paragraph each, and will change. When a rung's spec
is written, this README's row for it gets the link and the paragraph is cut to a sentence.

**Where the series ends.** When the last rung lands:

- the user asks for logs in words — *tail webapp logs*, *logs from yesterday*, *errors in webapp
  today* — and a live view of exactly that opens in the window, anchored where they asked;
- the view is one record in the chat, drawn compact inline and in full in the right sidebar,
  and the agent changes it by asking again, never by editing it;
- the agent reads logs only when it says so, as a separate call the transcript shows, so the
  record says what the model saw and what it did not;
- what the user is looking at rides with their next question, so *what's this?* over a selected
  line is answerable with nothing streamed to the model;
- a watch the user asks for runs without the model, and wakes it once, when it fires;
- the same shape carries the next rich view, such as a usage graph, as another tool with its own
  action and its own drawing.

## The idea in three sentences

A rich view is **a tool call's action**: the sidecar's tool reads the call's arguments into an
action, the row carries it, and the webview draws the action as the view. The model opens or
changes a view by making a call, so deciding to show one is deciding to fetch, which it already
does well, and it never composes UI. What the model reads and what the user sees are two
different things, and the call kind says which happened.

## The shape of the work

**Rungs are numbered in build order.** Files are named by the number
(`<n>-<slug>.md`); prose says "rung 2". Each rung needs the one before it unless
its row says otherwise. Nothing is built in parallel yet; if a wave appears, this README switches
to the lettered naming in [Naming](../README.md#naming).

**Every rung can ship.** No rung leaves a half-drawn view: a call the webview cannot draw yet is a
closed disclosure like any other tool call, which is what the transcript does today for a kind it
does not know.

**The viewer is its own PR.** The log viewer itself — give it a timestamp, it lands there, scroll
either way, follow at the end — lands in a PR of its own, outside the rungs. Until then
`LogViewer` (`src/components/widgets/log-viewer.tsx`) is a placeholder, and its props are the
contract it is mounted by.

**Every rung's spec has the same sections, in the same order**, as the retired agent-security
sequence had: *In short*, *What is not in this rung*, *Design*, *Decisions this rung asks for*,
*Tasks*, *Tests*, *Security*, *When it lands*, *Verification*. A rung not yet specified has only
its paragraph below.

## Fixed so far

These are decided across the ladder. A rung that needs to revisit one says so in its
*Decisions* section.

- **Two tools, not one with a flag.** `LogsView` opens or changes what the user sees and hands the
  model a one-line receipt. `LogsRead` hands the model a bounded sample and draws a frozen snippet.
  The call row then says honestly which happened, the way `Read` and `Write` are separate kinds.
- **The anchor is the interface, and pinning is the model's word.** A view is a list of sources,
  the filters on their lines, an optional grep, an anchor and whether the viewer is pinned to the
  end. The anchor is an edge, *head* or *tail*, or a moment: *yesterday* is a view anchored at
  yesterday's midnight. New lines arrive whatever the anchor; `pinToEnd` says whether the viewer
  keeps the end in view as they do, which the anchor cannot imply — *tail the logs* is pinned and
  *the latest lines* is not, both at the tail — so the model says it and the user unpins by hand,
  which leaves no row.
- **The view is what the log backend takes.** The backend reads any list of resources, each in
  its namespace, by its default container, the ones named or every one, its previous instance or
  its running one, and merges their lines by timestamp; it filters by node, region, zone, os and
  arch; and its grep is a regular expression over each line's message. The action carries exactly
  that, so the viewer hands it on without translating.
- **The sidecar checks, the action is what was shown.** A source is checked against the mirror,
  *yesterday* becomes an absolute time, and the action carries the resolved values, never what
  the model typed. The tool hands the action back from its run and the call row keeps it
  (`tool_calls.shown_action`), since the arguments alone cannot say it.
- **A change is a new call.** The model never mutates a view. What the user changes by hand in
  the viewer is the viewer's own state and leaves no row.
- **The focused view is window state, out of the URL.** It is chrome, like the right sidebar's
  open state, not what the window is looking at.
- **The model learns the screen at send, by pull.** The composer attaches a snapshot of the open
  view to `chatSend`; the sidecar puts it in the question's `context` block, so it is on the
  record and the Context disclosure shows it. Nothing streams the viewer's state anywhere.
- **Log text is cluster data.** Every line is drawn as text through the sinks the transcript
  already uses, with terminal control sequences stripped, as the root `CLAUDE.md` requires of the
  log tail.

## The ladder

| Rung | Builds | Spec |
| --- | --- | --- |
| 2 | The compact inline form | — |
| 3 | The screen rides with the question | — |
| 4 | `LogsRead`: what the model read, frozen | — |
| 5 | Select, then ask | — |
| 6 | A watch is a background task | — |
| 7 | The dashboard's target | — |

**Rung 1 has landed.** `LogsView` and `LogsViewAction` are on the schema, `tools/logsview` checks
a view against the mirror and answers a receipt, and the webview draws the call as a card whose
Expand sets the focused view, which chat mode's right sidebar draws. A view opens on Expand alone.
What is true now is in the root and `sidecar/` `CLAUDE.md`.

**Rung 2 — the compact inline form.** The card grows a fixed window of lines around the anchor,
with no scroller of its own, since the transcript owns the scroller. Only the newest view in the
chat runs live inline; older ones freeze. The inline card of the focused view collapses to
*Showing in the sidebar*, so one stream runs at a time. Open: how many lines, whether an
older card can be made live again by hand, and whether a new view ever opens the sidebar by
itself or stays inline until Expand.

**Rung 3 — the screen rides with the question.** `chatSend` takes a snapshot of the open view:
the resolved selector and grep, the id of the `LogsView` call that opened it, the visible range as
two timestamps, whether the viewer is at its end, a count of visible lines, and the selected lines
as bounded text. The sidecar writes it into the question's `context` block beside the cluster
card, and the prompt says the view is context, not the question. Open: the exact fields, their
caps, and what to attach when the sidebar is open on a view of another chat.

**Rung 4 — `LogsRead`.** The model reads a bounded sample over a selector, window and grep, and
the transcript draws what it read as a frozen snippet, with *View around this* opening a view
anchored at the sample's first timestamp — the user's click, not a call. The prompt gains its one
rule: open a view when the user wants to see logs, read when you need them to answer. Open:
the sample's caps, and whether a read of logs is ever a permissioned read like Secret data.

**Rung 5 — select, then ask.** Selecting lines in the viewer and typing a question is the main
gesture. An *Ask about this* on the selection focuses the composer, and the selection rides in
rung 3's snapshot. Open: whether a selection outlives a send.

**Rung 6 — a watch is a background task.** *Tell me when it throws again* starts a background
task of the chat, like a command with `run_in_background`: the sidecar matches the stream, the
match ends the task, and its end is a notice on the next question or a turn of its own, as an
agent's end is today. The model wakes once and reads the matching lines. Open: what a match is,
how long a watch lives, and how the user stops one.

**Rung 7 — the dashboard's target.** On the dashboard the chat is the right sidebar, so a view
there expands into `<main>` as a resource panel beside the tables. Open: whether that is a
`resource` value or a panel of its own, and whether a view opened in chat mode follows the window
to the dashboard.

## Shared vocabulary

Fixed here so the rungs agree. Go paths are under `sidecar/internal/`.

- **A view**: what the user is looking at — its sources, their filters, an optional grep, an
  anchor and whether it is pinned to the end. On the wire it is `LogsViewAction`; in the webview, the `action` of the `LogsView` call
  that opened it.
- **The focused view**: the one view a window draws in full, held beside the right sidebar's state
  as `{ chatId, callId, action }`.
- **A receipt**: a `LogsView` call's `output`, one line saying what is now on screen. The model
  reads nothing else from a view.
- **A sample**: a `LogsRead` call's `output`, bounded lines the model read. Rung 4 introduces it.
- **A snapshot**: what the composer attaches to a send about the focused view. Rung 3 introduces
  it.
- **`tools/logsview`**, **`tools/logsread`**: the two tools, beside `tools/kubequery`, which is the
  reference for a tool that reads the mirror and asks no one.
