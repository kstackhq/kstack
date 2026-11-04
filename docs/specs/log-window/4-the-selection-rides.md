---
title: The selection rides
scope: frontend
status: Implemented
---

# The selection rides

**Needs:** step 3. **Unblocks:** nothing; the sequence lands after it.

## In short

A log window's selection rides a send from its chat, as the sidebar's does today. The log
window pushes its viewport to the host, the host sends it down the app window's channel, and
the app window keeps a mirror per log window that the composer reads at the press. The chip
says where the view is. The push carries whole selected lines up to a size bound, and the full
count, which the chip and the snapshot read. It also carries the grep in effect, so a reloaded
log window keeps the toolbar's grep.

## What is not in this step

- **Opening, the bar, the lifetime.** Step 3.
- **The host's side of the push.** Step 2 built `log_window_viewport`'s envelope, the retained
  push, the generation and the bound. This step defines what the opaque `viewport` holds and
  what the app window does with it.

## Design

### 1. What the log window pushes

The route's `Answer` gains `generation`, which `log_window_view` already returns. The log
window's page sends `log_window_viewport { generation, grep, viewport }`:

- `generation` is the answer's.
- `grep` is the route's grep in effect: the one applied in this window, else the answer's,
  else the action's. It is never the viewport's, which is null before the viewer's first write
  and after it unmounts. The host keeps the push's grep as the window's, and a null there
  would lose the open's grep on the next reload.
- `viewport` is `{ range, atEnd, pinnedToEnd, paused, visible, grep, selection,
  selectedCount, selectionRevision }`:
  - `selection` is described under *The lines*, below.
  - `selectedCount` is the viewer's whole count.
  - `selectionRevision` steps when the viewer writes a new selection array, by identity.
    It exists on the wire alone; `LogViewport` does not carry it.

**The lines.** Each line carries its `timestamp`, `stream` and `message`, which is what the
snapshot reads. The message stays whole. The sidecar redacts the whole message (`ForModel`)
before it cuts it to 1 KiB, so the page never cuts one. The selection takes whole lines in
order, at most `MAX_SELECTED`, while their serialized size stays within `PUSH_LINES_BYTES`
(192 KiB, measured as UTF-8). That leaves 64 KiB of the host's 256 KiB bound for the rest of the
push, which a grep of at most 1,000 bytes and a few stamps cannot fill.

- **The first selected line always fits.** A message is at most `MaxLine`, 16 KiB and an
  ellipsis. Escaping an SGR sequence's `ESC` takes six bytes in place of one, so a line
  serializes to at most about 45 KiB.
- **The cut is computed once per revision** and reused by every push until the viewer writes a
  new selection.
- **Lines past the bound do not ride.** This is accepted as a transport cost. The first line
  left out is the one that would take the serialized lines past 192 KiB, and every line after
  it is left out too. The sidecar strips control sequences from each message, redacts it, and
  cuts it to 1 KiB before applying its 8 KiB budget. So a line left out here could have
  reached the model. The count still crosses whole, and the chip says how many lines were
  sent.

**Sending.** One request is in flight at a time. The host's command is async, so two
overlapping pushes could land in either order, and the host accepts every push of the current
generation. An older selection or grep could then overwrite a newer one.

- Writes to the handle mark the state dirty.
- A change to the selection, the count or the grep goes out on the next animation frame or
  after 250 ms, whichever comes first, as watch frames are batched (`scheduleFlush`). A window
  that is hidden gets no animation frames, so the timer is the floor.
- A change to the rest alone (the range, `visible`, `atEnd`, `pinnedToEnd`, `paused`) goes out
  after 250 ms. A scroll or a following tail changes these on every frame, and each push
  carries the whole selection, so they ride at the timer's pace.
- The page's `blur` sends at once, since the user is leaving for the app window.
- While a request is in flight, nothing more is sent. A send that comes due meanwhile waits,
  and goes out when the request settles, with the latest state. A change made meanwhile is
  never lost, and intermediate states are skipped.
- Nothing is sent when no field changed since the last push; the selection compares by
  identity.

**A refused push** is reported through `reportHostError` and is not sent again. The host's
refusal names the bound it broke, never the payload. The next change
sends the latest state as usual. The size bound and the sidecar's 1,000-byte grep limit keep a
push within the host's bounds. So the host refuses a push in two cases. One is a window it is
about to destroy. The other is a push still in flight when the page reloads: the reload steps
the generation, so the old push lands stale.

**The publisher outlives the viewer.** The viewport handle, the applied grep, the revision
counter and the sender live in `Paired`, which stays mounted for as long as the page has its
answer. Today they live in `View`, which unmounts when a notice replaces it. When the viewer
unmounts, it writes `EMPTY_LOG_VIEWPORT`. The push that follows carries no lines, a zero count,
a stepped revision and the route's grep. The mirror empties, and the host keeps the grep. A
viewer that mounts again continues the same counter, so a revision is never reused for
different lines within a generation.

`LogViewport` gains `selectedCount`, which the viewer writes as `selection.length`. Its
`selection` narrows to `SelectedLine[]`, `Pick<LogLine, 'timestamp' | 'stream' | 'message'>`,
which a `LogLine` satisfies, so the viewer's writes are unchanged. `snapshotOf` reads
`selectedCount` in place of the array's length, so a count past the carried lines survives
the wire.

### 2. The mirrors

`LogWindowsProvider` folds the two frames step 3 ignored. `focused` writes the window's
`focusSeq` into the windows map, which is React state; focus changes are rare. `viewport`
writes the window's mirror, and never React state, so a push re-renders nothing but what
reads the mirror.

- **A mirror** is a `LogViewportHandle` per label, held in a ref map. It is made the first time
  `snapshot` or `opened` names the window, and dropped on `closed` and when a `snapshot` lacks
  the window. A mirror that exists is never replaced: `snapshot`, `opened` and `viewport`
  frames fold the window's `push` into it with `set`, so a reader subscribed to it keeps
  hearing. A null `push` folds to `EMPTY_LOG_VIEWPORT`.
- **The fold keeps the selection's identity**, whichever frame carries the push. While the
  push's generation and the viewport's `selectionRevision` match the last one folded, the
  mirror keeps its previous selection array. The composer's identity compare then holds: a
  scroll or a new tail line does not bring back a selection the user dismissed with ×. A
  reload's null frame, or a new generation, clears it.
- `LogWindow` gains `focusSeq`. The provider exposes `shownWindowOf(chatId)`: this chat's log
  window with the highest `focusSeq` and its mirror, `{ window, viewport }`, or null. It is
  computed with the windows map, so `focused` reorders it and a `viewport` frame does not
  touch it.

### 3. The composer

**Picking the view that rides.** It works in chat mode alone, and only with a chat (`chatID`
not null):

1. the sidebar's view, while the sidebar is open on this chat;
2. else `shownWindowOf(chatID)`: the mirror of this chat's log window with the highest
   `focusSeq`;
3. else none.

`shownView` becomes `{ view, viewport, where: 'sidebar' | 'window' }`. The composer reads the
picked handle's selection and `selectedCount` through `useSyncExternalStore`.

**The chip** reads `where`, the count N (`selectedCount`), and the lines carried K
(`min(selection.length, MAX_SELECTED)`). *Place* is *in the sidebar* or *in a window*:

| State | Chip |
| --- | --- |
| N = 0 | *With the view* place |
| × pressed | *With the view* place *(N selected lines not sent)* |
| K = N | *With the view* place *and N selected lines* |
| K < N | *With the view* place *and N selected lines (the first K sent)* |

K is at least 1 whenever N is, since the first line always fits. The × shows while N > 0 and
the selection is not the removed one. It takes the lines and the
count out of the send together: the snapshot sends `selected: []` and `selectedCount: 0`.

**`onSend` stays synchronous.** It reads the picked handle and never asks the log window.
So a send carries the last push the app window received. A change made in the log window
just before the press can miss it: one round trip through the host, after a frame or up to
250 ms. The `blur` send narrows that window, since reaching the composer means leaving the
log window, but it does not close it. This is accepted: a send never waits on another window.

## Decisions this step asks for

- **The viewport is pushed, not pulled.** The chip needs the count live, and a pull at send
  would make `onSend` wait on another window.
- **The sidebar's view wins over a log window's.** It is beside the composer; the user sees it
  while typing.
- **The count crosses whole; the lines cross whole up to 192 KiB.** The sidecar redacts each
  whole message before it cuts it, so the page never cuts a message. A selection past the
  bound loses its later lines, which this step accepts as a transport cost (§1, *The lines*).
- **One push in flight, the latest state next.** The host orders nothing within a generation,
  so the page does. It also paces a push to the host's round trip.
- **What the chip reads rides on the next frame; the rest at 250 ms.** Every push carries the
  whole selection, so a scroll must not send it on every frame.
- **A send reads the last push received.** It can miss a change made just before the press.
- **The push names the route's grep, not the viewport's.** The host keeps it for the next
  reload.
- **The chip names the place, not the window.** With two windows on one chat, the one focused
  last rides, and the chip reads the same for either.

## Tasks

1. **The push.** `SelectedLine`, `selectedCount` on `LogViewport`, `snapshotOf` reading it,
   `generation` on the route's answer, the publisher in `Paired` with its pacing, the one
   request in flight, the cut and the revision.
2. **The mirrors and the composer.** The two frames, the mirrors and their fold, the pick, the
   chip.

## Tests

- **The push.**
  - A change to the selection, the count or the grep is sent on the next animation frame, or
    after 250 ms when no frame comes. A change to the range alone waits 250 ms, with frames
    passing. A push goes at once on `blur`, and not when no field changed.
  - One request is in flight at a time. With the first request held, two later changes send
    nothing until it settles, then send the latest state alone, once.
  - The selection takes whole lines, at most `MAX_SELECTED`, while within
    `PUSH_LINES_BYTES` measured as UTF-8. A single `MaxLine` message dense with SGR sequences
    fits. No message is cut. Each line carries the three fields alone. The cut is not
    recomputed while the revision holds.
  - The count is whole, the generation is the answer's, and the revision steps on a new
    array alone.
  - The grep is the route's in effect, including after the viewer unmounts.
  - When a notice replaces the viewer, the empty push reaches the mirror. A viewer mounted
    again, with a new selection, sends a revision above every earlier one, and the applied
    grep survives the remount.
  - A refused push is reported and not resent.
  - `snapshotOf` sends `selectedCount` from the viewport.
  - A reload keeps the toolbar's grep: apply one, reload against the host's answer, and the
    viewer opens with it.
- **The app window.**
  - The pick prefers the sidebar's view over a window's, and takes the highest `focusSeq`
    among windows. It picks none when the chat differs, in dashboard mode, and with no chat.
  - The mirrors are seeded from `snapshot` and `opened`, and dropped on `closed`. A later
    `snapshot` folds into the mirror a reader already holds, and that reader hears it.
  - `shownWindowOf` names the chat's window with the highest `focusSeq`, and null for a chat
    with none.
  - A frame with the same generation and revision keeps the mirror's selection array, so a
    dismissed selection stays dismissed. A null push or a new generation clears it.
  - A `viewport` frame re-renders neither the provider's consumers nor a card.
  - `focused` reorders the pick.
  - The chip draws each row of the table for both places. × sends neither lines nor count.

## Security

- **What widens.** Log lines cross from one window to another through the host.
- **What narrows.** Nothing.
- **What holds it.** A viewport crossing windows is bounded: 192 KiB of lines by the page, and
  256 KiB in all by the host. Messages cross whole, so the sidecar's redaction reads each whole. It is stamped with its page's generation, so a reloaded page's
  old lines cannot ride. It is drawn only as a count in the chip and as data in the snapshot,
  never as HTML.
- **Residual.** As step 2.

## When it lands

- `docs/security-model.md`: a row for the push. A log window's viewport reaches its paired app
  window alone, bounded (192 KiB of lines by the page, 256 KiB by the host) and stamped with a
  generation only the host steps. **Enforced** by `own_a_push_from_a_stale_generation_is_refused`,
  `own_every_bound_is_refused` and `own_frames_reach_the_paired_app_window_alone`.
- `src-tauri/CLAUDE.md`: the push's line says the viewport is the page's view and selection,
  which the host forwards unread.
- `src/CLAUDE.md`:
  - the composer's snapshot rule names the pick, the place, the count and the generation;
  - the `LogWindowsProvider` paragraph says it folds `focused` and `viewport`, and keeps the
    mirrors out of React state;
  - the log window's paragraph says it pushes its viewport with the grep in effect, and that
    a reload keeps that grep.
- `src/lib/log-viewport.ts`'s header comment and the composer's *Pulled at the press* comment
  say the sidebar's handle is pulled and a log window's is pushed to a mirror.

## Verification

`make test-changed` while working; `make test-js` and `make lint-js` before handing off. By
hand:

1. Select lines in a log window, then send from the chat, and see them ride.
2. Dismiss them with ×, scroll, and see them stay dismissed.
3. Apply a grep, reload the log window, and see the grep kept and the selection cleared.
4. Select many long lines, past 192 KiB, and read *(the first K sent)*.
5. Cover the log window with the app window right after selecting, and see the chip update.
6. With the sidebar open on the same chat, see its lines ride instead.
