---
title: The window and opening it
scope: frontend
status: Planned
---

# The window and opening it

**Needs:** step 2. **Unblocks:** step 4.

## In short

The webview gets the log window's page and the app window's side of it. *Open in window* on
the panel's header or on a card moves the view into a window: the viewer under a 40px bar
naming the cluster and the chat, with *Show chat*. The card reads *Showing in a window* with
*Show window*. The app window closes its log windows when its cluster changes.

After this step a log window works end to end. Its selection does not ride a send yet, and
the composer says nothing about log windows.

## What is not in this step

- **The viewport push, the mirrors, the composer's pick and chip.** Step 4. Here the log
  window never calls `log_window_viewport`, and the provider ignores `viewport` and `focused`
  frames.

## Design

### 1. The route and the layout

`src/routes/_logs.tsx` is a pathless layout rendering `LogsWindowLayout`
(`src/layouts/logs-window-layout.tsx`): the bar over the routed page, no sidebars, no
`AppDialogs`. `src/routes/logs-window.tsx` is `/logs`. `ReadyGate` and `index.html` draw a
48px drag strip before the bar mounts; in this window it overhangs the viewer's top by 8px for
that moment, which is harmless.

The route calls `log_window_view` on mount, which only reads, so React's dev-mode double mount
is harmless; the host steps the generation on the page load itself. It then watches `chatsWatch`
for the chat's title and cluster and `chatMessagesWatch` for the call. Until the answer and both
bookmarks land it draws nothing. Then:

- a chat the map lacks draws *This chat was deleted.*;
- a call the chat lacks, or one `viewOfCall` returns nothing for, draws *This log view is not
  available.*;
- otherwise it draws `LogViewer` with the view, the grep, and a `LogViewportHandle` of its
  own.

`viewOfCall(call)` in `src/lib/logs-view.tsx` is the mapping the three cards make today: a
LogsView call's action, or a LogsRead or LogsWatch call's `viewAround`. The cards and the route
share it.

The window holds no provider state of its own. The grep the user applies from the toolbar is
route state. Columns and wrap still read the shared `kstack:log-viewer-*` keys, as app-wide
defaults. A `localStorage` key never carries a view.

### 2. The bar

The bar is 40px and the drag region, as the app bar is. macOS keeps its traffic lights, which
step 2 placed for this height, and its hidden title. Linux and Windows draw `WindowControls`
at the right with no `AppMenu`, so `Ctrl+N` and `Ctrl+Q` do nothing there. It draws the
cluster's dot and name and the chat's title through `VisibleText`, then *Show chat*, which
calls `log_window_show_source` and opts out of dragging. An effect sends `log_window_title`
with the first source, the chat's title and the cluster's name whenever one changes.

### 3. The app window's side

`src/lib/log-windows.tsx` adds `LogWindowsProvider` under `LogsViewProvider` in `AppLayout`.
It calls `log_windows_watch` with a channel on mount, keeps the `page` it returns, and passes
it in every app-window command. It folds the frames, in order, into one map by label:
`snapshot` replaces it, `opened` adds, `closed` removes. For each window it keeps its cluster,
chat and call; the frames' `push` is step 4's.

Each mount's watch replaces the last and ends its page, and the answers can arrive out of
order. The provider keeps the page of its current mount and drops an answer that lands after
that mount's cleanup; React's dev-mode double mount would otherwise leave it holding an ended
page, with every command refused.

It exposes `open(view)` and `focus(callId)`, which do nothing until the watch returns its page.
`open` calls `log_window_open`, which returns once the window exists; then, if the sidebar
shows that call, it clears the focused view and closes the sidebar. A refused or failed open
leaves the sidebar as it was and reports through `error-log.ts`.

The provider calls `log_window_close_all` when `useActiveCluster` has a `clusterID` and it
differs from its windows'. That covers every path that changes `kubeContext`: the picker's
popover, history back and forward, `OpenChat`'s *Switch to it*, and the fallback to the
kubeconfig's default when the active context is deleted. It does nothing while `clusterID` is
undefined: before the clusters bookmark, on a reload, and when no context is left.

- **The cards** draw *Open in window* beside Expand or *View around this*. While their call
  has a window they draw *Showing in a window* with *Show window* instead, where *Showing in the
  sidebar* goes today.
- **The panel's header** draws *Open in window* beside its close.
- **A `showChat` frame** navigates to `/chat/$chatId`. Its cluster is the window's, since a
  change would have closed the log window.

## Decisions this step asks for

- **Move, not copy.** One place per view keeps the card's state and the snapshot's source
  unambiguous.
- **A move resets the viewer.** The view and the grep carry; scroll, pause and selection do
  not. Carrying them would mean shipping the viewer's state through the host for one click.
- **A log window closes with its app window and on its cluster change.** No orphans, no
  detached state. The deleted context's fallback counts as a change.

## Tasks

1. **The route and the layout.** `_logs`, `/logs`, `LogsWindowLayout`, the bar, `viewOfCall`,
   the title push.
2. **The app window's side.** `LogWindowsProvider`, the cards' and the panel's buttons, the
   cluster change, *Show chat*.

## Tests

- **The route.** It draws nothing before the answer and both bookmarks, each notice after
  them, and the viewer with the pair's grep; the title is sent with the three parts and resent
  when one changes; `viewOfCall` for the three call kinds and for a call with no action.
- **The app window.** The frames fold in order, and a `closed` after a `snapshot` that lacks
  the window is a no-op; `open` moves a view out of the sidebar and focuses an open window,
  and a failed open leaves the sidebar as it was; every command carries the page, and none is
  sent before the watch returns it; after a double mount whose first answer lands last, the
  provider keeps the second page; a card draws each of its three states; each cluster-change
  path closes the windows, the deleted context's fallback included, and a reload, the clusters
  watch before its bookmark, and no context left close none; a `showChat` frame navigates to
  the chat.

## Security

- **What widens.** Nothing beyond step 2: the page in the new window is the viewer, under the
  viewer's rules.
- **What narrows.** Nothing.
- **What holds it.** The bar draws the chat's title and the cluster's name through
  `VisibleText`. A view travels in the host's pair and its frames, never in `localStorage`,
  which every window shares.
- **Residual.** As step 2.

## When it lands

- `src/CLAUDE.md`: *Layouts* names `_logs` and `LogsWindowLayout`; the right sidebar's section
  says a view moves to a window and how a card reads while it is there.

## Verification

`make test-changed` while working; `make test-js` and `make lint-js` before handing off. By
hand, on each platform: open a view in a window from the header and from a card, close the
app window and watch the log window close, change cluster by the picker and by history and
watch it close, press *Show chat* from another chat, and read the title in the OS's window
switcher.
