---
title: Select, then ask
scope: frontend
status: Proposed
---

# Select, then ask

**Needs:** rung 4, landed. **Unblocks:** rung 6.

## In short

The user selects three lines in the sidebar viewer. A chip above the composer reads *With the
view and 3 selected lines*. They type *what's this?* and send. The lines ride in rung 3's
snapshot, as they already do. The lines stay selected and keep riding, so a follow-up such as
*why would that be nil?* is about them too, until the user presses ×, presses Escape in the
viewer or selects something else.

This rung adds:

- **Webview.** A chip in the composer for what rides the send, whose × takes the selected lines
  out of it.

Nothing changes on the wire, in the sidecar or in the viewer.

## What is not in this rung

- **No button, menu item or shortcut to ask about a selection.** Selecting is the gesture. The
  chip shows the lines are attached the moment they are selected.
- **No chip for a `LogsRead` snippet or the inline card.** Both are records of a call, not the
  user's screen.
- **No dashboard.** Its right sidebar is the chat. Rung 7 gives it a view.
- **No pre-filled question.** The user writes the question.

## Design

### 1. When the selection rides

Rung 3's conditions still decide whether a snapshot rides: chat mode, the right sidebar open,
and the focused view a call of this chat. Within a snapshot, the selected lines ride unless they
are **removed**.

The composer holds one value, `removed`: the selection array it last took out of the send, or
null. The lines ride when `selection.length > 0 && selection !== removed`. The viewer writes a
new array whenever the selection changes and the same array on every other write, so:

- a new selection is never `removed`, and attaches by itself;
- a following tail does not re-render the composer, which reads `viewport.get().selection`
  through `useSyncExternalStore` in place of its length;
- a viewer unmounting writes the empty selection, which never rides.

A click on a cell selects it, and a selected cell sends its whole line, as the viewport already
reports the lines of a cell block's rows.

`removed` is composer state. A remount forgets it, so a removed selection rides again after the
user leaves the chat and comes back. The viewer never learns of it.

### 2. The chip

The muted line above the box becomes a chip, shown while a snapshot would ride:

- no selection: *With the view in the sidebar*;
- a selection that rides: *With the view and 3 selected lines*;
- past the snapshot's cap of 50: *With the view and 212 selected lines (the first 50 sent)*;
- a removed selection: *With the view (3 selected lines not sent)*.

While lines ride, the chip has a ×. It sets `removed` to the current selection. The highlight
stays in the viewer. With no selection or a removed one, the chip has no ×: closing the sidebar
is still the only way to drop the view.

The chip says what goes on the wire. The sidecar's 8 KiB budget may keep fewer lines, and the
question's *Context* disclosure shows how many it kept.

## Decisions this rung asks for

- **Selecting attaches the lines; the chip's × takes them out.** Reason: the selection already
  rides every send under rung 3, and the chip makes that visible the moment it happens, as an
  editor's chat does with selected code. A button to attach would be a second step to the same
  place.
- **A clicked line rides like any selection.** Reason: clicking a line is how the user points at
  it, and *what's this?* over a clicked line is this rung's main case. The cost: a line clicked
  for any other reason rides until ×, Escape or another selection.
- **× takes the lines out of the send, not out of the viewer.** Reason: the user may still want
  the highlight, and the selection stays the viewer's own state, so nothing outside the viewer
  writes to it. The cost: a highlighted selection may not ride, which the chip's
  *not sent* wording states.
- **A selection rides every send while it is selected and not removed.** Reason: what the chip
  shows is what rides, with no rule the screen does not state, and a follow-up about the same
  lines needs no second selection. The prompt already reads `## View` as what the user was
  looking at, not what they asked. The cost: copied lines ride every send until the user
  presses ×, presses Escape or selects something else.

## Tasks

All tasks start Planned.

1. **The ride rule.** `removed` in the composer, the selection read by identity, and the
   selection left out of `snapshotOf`'s input when it is removed.
2. **The chip.** Replace the muted line, with its four wordings and its ×.
3. **Docs.** As listed under *When it lands*.

## Tests

In `chat-composer.test.tsx`:

- The chip's four wordings, and no chip for another chat's view, with the sidebar closed, or on
  the dashboard.
- × leaves the selection in the viewport and sends a snapshot with no selected lines; a new
  selection after × rides again, a click on the same cell included.
- A selection rides two sends in a row.
- A held send's Retry resends the held snapshot, whatever the selection is now.
- A following tail writing the viewport does not re-render the composer.

## Security

**Widens nothing.** The selected lines reach the model through rung 3's path, on the user's send,
under the same caps, escaping and redaction. This rung only sends fewer of them.

## When it lands

- `src/CLAUDE.md`: the composer paragraph covers the chip and its ×.
- This README's row for rung 5 keeps its link until the sequence lands, and its paragraph says
  what this rung builds.

## Verification

`bash scripts/sandbox-dev-setup.sh` first, then `make test-changed` and `make lint-js`.

By hand, in chat mode with a view in the sidebar:

- Select three lines: the chip says *3 selected lines*. Send *what's this?*: the answer is about
  those lines. Send *why would that be nil?*: the chip still says *3 selected lines*, and the
  answer is about them too.
- Select lines and press ×: the highlight stays, the chip says *not sent*, and the next answer's
  *Context* disclosure has no selected lines.
- Select other lines: the chip says they ride.
- Focus another chat's view: no chip.
