# The log viewer in its own window

The build order for [#507](https://github.com/kstackhq/kstack/issues/507). The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply, and the steps share
`wip/log-window`.

Today a log view is drawn in one place: an app window's right sidebar, where it shares width
with the chat. When the last step lands, a view can leave the sidebar for a window of its own:

- **Open in window** sits on the panel's header and on each transcript card that can show a
  view. It moves the view: the sidebar lets it go, and a new window draws it. A view is in one
  place at a time.
- **A log window belongs to one app window**, the one that opened it. The host keeps the pair
  and enforces it: a log window reaches its own app window and no other, and it closes when
  that window closes or leaves the cluster.
- **The log window is the panel's header and the viewer under a slim bar.** The bar names the
  cluster and the chat and has **Show chat**, which brings the app window back. The header
  names the sources, as the sidebar's does. The native title names the chat and the
  cluster too, for the OS's window switchers.
- **In the app window, the card reads *Showing in a window*** with **Show window**, where
  *Showing in the sidebar* is drawn today.
- **The selection still rides a send.** The log window pushes its viewport to the app window
  through the host, and the composer's chip says the lines come from a window.

## The steps

| Step | Builds | Needs |
| --- | --- | --- |
| [1. Gate the host's commands](1-gate-the-hosts-commands.md) | Every command needs a capability allow; no page emits events. No behaviour change. | nothing |
| [2. Log windows in the host](2-log-windows-in-the-host.md) | The `logs-N` label, the pair, the channel, the commands, the title builder. | 1 |
| [3. The window and opening it](3-the-window-and-opening-it.md) | The `/logs` route, the bar, the buttons, *Show chat*, the lifetime. | 2 |
| [4. The selection rides](4-the-selection-rides.md) | The viewport push, the mirrors, the composer's pick and chip. | 3 |

**Every step can ship.** After step 3 a log window works end to end; its selection rides a
send only after step 4, and until then the composer says nothing about it.

**Every spec has the same sections, in the same order.** *In short* says what the step
changes. *What is not in this step* names what a reader might expect here and says which step
has it. *Design* is numbered sections an implementer works through. *Decisions this step asks
for* lists each choice and why. *Tasks* is the order of assignment. *Tests* names every test
the step adds. *Security* says what the step widens, narrows, holds and leaves. *When it lands*
lists the docs to update. *Verification* is the commands to run, then what to check by hand.

## What is not in this sequence

- **The dashboard.** Its chat has no Expand, so it has nothing to open.
- **Reopening on relaunch.** App windows do not reopen, and a log window cannot outlive its app
  window.
- **Any other secondary window.** Exec gets its own spec. The layout, the label prefix and the
  pairing are built so it can reuse them.

## Answers to #507

**1. Opening.** Both places, and it moves. The panel's header gets *Open in window* beside its
close. A card gets *Open in window* beside Expand or *View around this*, in chat mode alone, and
drops its inline preview while the view is in a window. A
view is in one place at a time: the card has one state to draw, and the snapshot has one
source. What moves is the view and the grep the user applied. The window's viewer opens at
the action's anchor, as a fresh Expand does, so the scroll position, a pause and a selection
do not carry over. (Step 3.)

**2. Linking.** Three ways, none of them a number or a colour. The log window's bar shows the
cluster's dot and name, the chat's title, and *Show chat*. Its native title reads
`Logs · <first source> · <chat title> · <cluster>`, which is what the taskbar, Mission Control
and the Window menu show. The app window shows its log windows where their calls are: each
card reads *Showing in a window* with *Show window*. App windows carry no visible number to
match, and the cluster's dot already has a colour. (Steps 2 and 3.)

**3. Lifetime.** (Steps 2 and 3.)

| Event | The log window |
| --- | --- |
| Its app window closes | Closes. The host closes it on the app window's `Destroyed`. |
| Its app window's active cluster changes, by any path | Closes. Today the sidebar's view clears on the picker's change alone; a log window closes on every path. |
| Its app window moves to another chat or mode | Stays open and live. Its selection stops riding until the app window is back on its chat. |
| Its chat is deleted | Draws *This chat was deleted.*, as `OpenChat` does, and stops reading. |
| Its cluster is deleted | The app window's context yields to the kubeconfig's default, which is a cluster change: the row above applies. With no other context the app window has no cluster, the log window stays, and the chat's deletion follows. |

A log window never outlives its app window, so there is no detached state to draw.

**4. Many windows.** As many as the user opens, one per call per app window. *Open in window*
or *Show window* on a call that already has one focuses it. There is no cap. (Step 2.)

**5. The snapshot.** It rides, under today's rule: the view is the composer's chat's and the
app window is in chat mode. The log window pushes its viewport to the host, and the host
sends it down the app window's channel, which no other page holds. The push carries the first
`MAX_SELECTED` selected lines and the full count, and the chip and the snapshot read the
count. The composer keeps reading a `LogViewportHandle` at the press, so `onSend` stays
synchronous. With both a sidebar view and log windows on one chat, the sidebar's view rides
while the sidebar is open, else the log window focused last. The chip says which: *N selected
lines from the sidebar* or *from a log window*. (Step 4.)

**6. Chrome.** A slim bar, 40px, with no menu, omnibox or history. macOS keeps its traffic
lights, placed for the slimmer bar, and its hidden title. Linux and Windows draw
`WindowControls` at the right with no `AppMenu`, so `Ctrl+N` and `Ctrl+Q` do nothing there.
(Step 3.)

**7. Restore.** No. App windows do not reopen either.

## Decisions the sequence rests on

- **Move, not copy.** One place per view keeps the card's state and the snapshot's source
  unambiguous.
- **The host owns the pair, and talks to an app window over its channel.** A page cannot
  forge a link to another window, send an event as the host, or listen for another window's,
  and a closing app window is a host event its page may not live to see.
- **A log window closes with its app window and on its cluster change.** No orphans, no
  detached state.
- **The sidebar's view wins over a log window's.** It is beside the composer; the user sees it
  while typing.

Each step lists the decisions that are its own.

## When the sequence lands

- `docs/security/`: a review record for the new window and its commands.
- An ADR: *a log window belongs to one app window, and the host keeps the pair*.

Each step lists the `CLAUDE.md` and `security-model.md` lines it changes.
