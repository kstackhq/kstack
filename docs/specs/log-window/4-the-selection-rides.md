---
title: The selection rides
scope: frontend, host
status: Planned
---

# The selection rides

**Needs:** step 3. **Unblocks:** nothing; the sequence lands after it.

## In short

A log window's selection rides a send from its chat, as the sidebar's does today. The log
window pushes its viewport to the host, the host sends it down the app window's channel, and
the app window keeps a mirror per log window that the composer reads at the press. The chip
says where the lines come from. The push carries the first `MAX_SELECTED` lines and the full
count, and the count is what the chip and the snapshot read.

## What is not in this step

- **Opening, the bar, the lifetime.** Step 3.
- **The host's side of the push.** Step 2 built `log_window_viewport`'s envelope, the retained
  push, the generation and the bound. This step defines what the opaque `viewport` holds and
  what the app window does with it.

## Design

### 1. What the log window pushes

The viewer writes the window's handle as it does the sidebar's. A subscriber on the handle
sends `log_window_viewport { generation, grep, viewport }` at most once per animation frame,
and only when the viewport changed. `generation` is the one `log_window_view` answered, which
the host steps on each page load. `grep` is the viewport's. `viewport` is the viewport with
`selection` cut to `MAX_SELECTED` lines and a `selectionRevision` that steps when the viewer
writes a new selection array. The snapshot carries no more lines than that, and the composer's
× needs the generation and the revision together, since a reloaded page's revisions start
over. A pinned, tailing viewer pushes on each frame a line lands; the cut keeps a push small
unless its lines are long, and the host refuses one over 256 KiB. A refused push is sent again
with no selected lines, a stepped revision and the count kept, so the mirror moves on and no
older lines stay eligible.

`LogViewport` gains `selectedCount`, which the viewer writes as `selection.length`.
`snapshotOf` and the chip read it in place of the array's length, so a count over
`MAX_SELECTED` survives the wire.

### 2. The mirrors

`LogWindowsProvider` folds the two frames step 3 ignored: `focused` stamps the window's
`focusSeq`, and `viewport` writes its mirror, a `LogViewportHandle` per window, from the
frame's `push`; a null `push` resets the mirror to `EMPTY_LOG_VIEWPORT`. The snapshot seeds
each mirror the same way. The mirror
keeps its previous selection array while the generation and the revision are unchanged, so
the composer's compare by identity holds: a scroll or a new tail line does not bring back a
selection the user dismissed with ×, and a reload's empty frame clears it.

### 3. The composer

The composer picks the view that rides: the sidebar's while it is open on this chat, else the
mirror of this chat's log window with the highest `focusSeq`, else none. `shownView` becomes
`{ view, viewport, where: 'sidebar' | 'window' }`, and the chip reads `where` and
`selectedCount`: *N selected lines from the sidebar* or *from a log window*. Its × takes the
lines and the count out of the send together. `onSend` stays synchronous: it reads the mirror
and never asks the log window.

## Decisions this step asks for

- **The viewport is pushed, not pulled.** The chip needs the count live, and a pull at send
  would make `onSend` wait on another window.
- **The sidebar's view wins over a log window's.** It is beside the composer; the user sees it
  while typing.
- **The count crosses whole, the lines cut.** The sidecar refuses more than `MAX_SELECTED`
  lines and already takes the count beside them.

## Tasks

1. **The push.** `selectedCount` on `LogViewport`, `snapshotOf` reading it, the subscriber in
   the route, the resend on refusal.
2. **The mirrors and the composer.** The two frames, the mirror's fold, the pick, the chip.

## Tests

- **The push.** The viewport is sent once per frame, not when unchanged, with the selection
  cut to `MAX_SELECTED`, the count whole, the generation and grep beside it, and the revision
  stepping on a new array; a refused push is sent again with no lines, a stepped revision and
  the count; `snapshotOf` sends
  `selectedCount` from the viewport.
- **The app window.** The composer picks the sidebar's view over a window's, the highest
  `focusSeq` among windows, and none when the chat differs; a frame with the same generation and
  revision keeps the mirror's selection array, so a dismissed selection stays dismissed, and a
  null push or a new generation clears it; the snapshot seeds the mirrors; the chip's two
  wordings; the chip reads the count over the lines, and × drops both.

## Security

- **What widens.** Log lines cross from one window to another through the host.
- **What narrows.** Nothing.
- **What holds it.** A viewport crossing windows is bounded, stamped with its page's
  generation so a reloaded page's old lines cannot ride, and drawn only as a count in the chip
  and as data in the snapshot, never as HTML.
- **Residual.** As step 2.

## When it lands

- `src/CLAUDE.md`: the composer's snapshot rule names the pick, the count and the generation.

## Verification

`make test-changed` while working; `make test-js` and `make lint-js` before handing off. By
hand: select lines in a log window, dismiss them with × and scroll, reload the log window, and
send from the chat; with the sidebar open on the same chat, see its lines ride instead.
