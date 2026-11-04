---
title: The window and opening it
scope: frontend
status: Planned
---

# The window and opening it

**Needs:** step 2. **Unblocks:** step 4.

## In short

The webview gets the log window's page and the app window's side of it. *Open in window* on
the panel's header or on a card moves the view into a window: the panel's header and the viewer
under a 40px bar naming the cluster and the chat, with *Show chat*. The card reads *Showing in a
window* with *Show window*. The app window closes its log windows when its cluster changes.

After this step a log window works end to end. Its selection does not ride a send yet, and
the composer says nothing about log windows.

## What is not in this step

- **The viewport push, the mirrors, the composer's pick and chip.** Step 4. Here the log
  window never calls `log_window_viewport`, and the provider ignores `viewport` and `focused`
  frames.
- **A reload that keeps the toolbar's grep.** Step 4. `log_window_view` answers the grep of the
  last push, and step 3 sends none, so a reloaded log window opens with the grep it was opened
  with.

## Design

### 1. The route and the layout

`src/routes/_logs.tsx` is a pathless layout rendering `LogsWindowLayout`
(`src/layouts/logs-window-layout.tsx`): the bar over the routed page, no sidebars, no
`AppDialogs`. `src/routes/logs-window.tsx` is `/logs`. The layout keeps the app layout's
height chain (`h-(--app-min-h) min-h-0`, `min-h-0` on each plain flex item), so the viewer
scrolls and the window never does.

The route calls `log_window_view` on mount, which only reads, so React's dev-mode double mount
is harmless; the host steps the generation on the page load itself. It then watches `chatsWatch`
for the chat's title and cluster and `chatMessagesWatch` for the call, and reads the cluster's
name from `useClusters`. Until the answer and the three bookmarks (chats, messages, clusters)
land it draws *Loading…* under the bar. A watch never fails for good: the subscribe exchange
reconnects it, and it stays `connecting` until its bookmark lands, so *Loading…* is what a
dropped connection looks like here, as in the app window. After a bookmark, a `reconnecting`
watch keeps its last data and the route keeps drawing it.

A `log_window_view` that rejects draws *This log window could not load.* with *Try again*,
which calls it again, and reports through `error-log.ts`. The host refuses only a window it no
longer pairs, which it is about to destroy, so *Try again* is for a failed IPC call.

Once the answer and the bookmarks are in:

- a chat the map lacks draws *This chat was deleted.*;
- a call the chat lacks, or one `viewOfCall` returns nothing for, draws *This log view is not
  available.*;
- otherwise it draws the panel's header, without its close or *Open in window*, over
  `LogViewer` with the view, the grep, and a `LogViewportHandle` of its own. The grep is the
  one the user applied in this window, else the answer's, else the action's: the answer's is
  null when the open carried none. The header names the sources, filters,
  anchor and applied grep, so two windows on one chat tell themselves apart; macOS hides the
  native title that would.

`LogsViewPanel`'s `Header` is exported as `LogsViewHeader`. It takes an optional `onClose`
and an optional `onOpenInWindow`, and draws neither button without its handler. The panel
passes both; the route passes neither. Its right padding that clears the sidebar's toggle is
the panel's alone.

`viewOfCall(call)` in `src/lib/logs-view.tsx` is the mapping the three cards make today: a
LogsView call's action, a LogsRead call's `action.viewAround`, or a LogsWatch call's
`background.viewAround`, which stays null while the watch runs. The cards and the route
share it.

The window holds no provider state of its own. The grep the user applies from the toolbar is
route state. Columns and wrap still read the shared `kstack:log-viewer-*` keys, as app-wide
defaults. A `localStorage` key never carries a view.

### 2. The bar

The bar is 40px and the drag region, as the app bar is. macOS keeps its traffic lights, which
step 2 placed for this height, and its hidden title; the bar leaves them the same left inset
the app bar does. Linux and Windows draw `WindowControls`
at the right with no `AppMenu`, so `Ctrl+N` and `Ctrl+Q` do nothing there. It draws the
cluster's dot and name and the chat's title through `VisibleText`, then *Show chat*, which
calls `log_window_show_chat` and opts out of dragging. A cluster the clusters map lacks, as
when it was deleted and no other context is left, draws no dot or name. An effect sends
`log_window_title` with the first source, the chat's title and the cluster's name whenever one
changes. The first source is `namespace/kind/name`, as `SourceLine` names it; a missing
cluster or an empty chat title sends an empty part, which the host keeps in place. Each notice
state sends no title, so the window keeps the host's.

### 3. The app window's side

`src/lib/log-windows.tsx` adds `LogWindowsProvider` under `LogsViewProvider` in `AppLayout`.
It calls `log_window_subscribe` on mount with a channel made once per provider, keeps the
`page` it returns, and passes it in every app-window command. It folds the frames, in order, into one map by label:
`snapshot` replaces it, `opened` adds, `closed` removes. For each window it keeps its cluster,
chat and call; the frames' `push` is step 4's.

Each subscribe replaces the last and ends its page, and two subscribes can reach the host in
either order. Under React's dev-mode double mount the provider subscribes twice. Both use the
one channel, made in a `useState` initializer, which React keeps across that remount, and each
subscribe sends its own `snapshot` down it. The host mints pages from one rising counter, so
the provider keeps the largest page any answer returns: that is the host's current one, in
whichever order the two ran.

It exposes `open(view)` and `focus(callId)`, which do nothing until the subscribe returns its
page.

**The focused view moves with an open.** The sidebar keeps its focused view, and its grep,
while it is collapsed, so a card's *Open in window* can name the focused call with the
sidebar closed. `open` therefore reads the focused view when it is called: if it is the same
call, the open carries its grep. When `log_window_open` returns, `open` reads the focused view
again and clears it if it is still that call, whether the sidebar is open or collapsed, and
closes the sidebar if it was showing it. A view the user focused while the open built is a
different call and stays. A refused or failed open leaves the focused view and the sidebar as
they were and reports through `error-log.ts`.

**Opens and closes go one at a time.** `log_window_open` is async in the host and
`log_window_close_all` is not, so a close sent after an open can run before the open reserves,
and miss it. The provider therefore sends both through one queue, each command after the last
one settles. `focus` does not queue.

The provider holds the cluster its windows belong to: the last defined `clusterID` it saw.
The first defined `clusterID` after the subscribe is held with no close; the snapshot check
below decides that case. After it, when `useActiveCluster`'s `clusterID` becomes a defined
value other than the held one, the provider queues `log_window_close_all` and holds the new one. An open queued before the change settles
first, so its window is built and then closed; an open queued after it runs on the new cluster.
When an open's turn comes, it is dropped if its view's `clusterId` is not the held cluster, so a
click from a card drawn before the change builds nothing. A late window's `opened` frame can
reach the map before the close does; nothing reads the map for another cluster's chat, and the
`closed` follows.

That covers every path that changes `kubeContext`: the picker's popover, history back and
forward, `OpenChat`'s *Switch to it*, and the fallback to the kubeconfig's default when the
active context is deleted. Nothing changes while `clusterID` is undefined: before the clusters
bookmark, on a reload, and when no context is left. After a subscribe the provider holds no
cluster; once it has both the `snapshot` and a defined `clusterID`, it queues a close if any
window in the snapshot is on another cluster. With the queue, the snapshot holds one cluster's
windows unless a reload interrupted a change, and then closing them all is what the change
asked for.

- **The cards** draw their buttons in chat mode alone, as Expand is today, and have three
  states:
  - *Neither*: Expand or *View around this*, with *Open in window* beside it, and the inline
    preview.
  - *In the sidebar*: *Showing in the sidebar*, no buttons, no inline preview, as today. The
    panel's header has *Open in window*.
  - *In a window*: *Showing in a window* with *Show window*, and no inline preview, since a
    view is in one place at a time.
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
- **The provider queues its opens and closes, and the host keeps one close.** A cluster change
  closes after every open sent before it and before every open sent after it, so a late window
  from the old cluster never takes a new one with it. A close by label in the host would also
  do it, but step 2 has landed and the queue needs no host change.

## Tasks

1. **The route and the layout.** `_logs`, `/logs`, `LogsWindowLayout`, the bar, the shared
   header, `viewOfCall`, the title push.
2. **The app window's side.** `LogWindowsProvider`, the cards' and the panel's buttons, the
   cluster change, *Show chat*.

## Tests

- **The route.** It draws *Loading…* before the answer and each of the three bookmarks,
  including while a watch reconnects before its bookmark, and keeps drawing the view through a
  `reconnecting` phase; a rejected `log_window_view` draws the error, reports it, and *Try
  again* asks again and draws the view when it answers; each notice after the bookmarks, and
  the header, with no close and no *Open in window*, over the viewer with the pair's grep, or
  the action's when the pair's is null; the bar draws no dot or name for a cluster the map
  lacks; the title is sent with the three parts and resent when one changes, with an empty
  part for a missing cluster or an empty chat title, and none is sent from a notice;
  `viewOfCall` for the three call kinds, for a LogsWatch still running, and for a call with no
  action. The panel's header still draws its close and *Open in window*.
- **The app window.** The frames fold in order, and a `closed` after a `snapshot` that lacks
  the window is a no-op; `open` moves a view out of the sidebar and focuses an open window,
  and a failed open leaves the sidebar as it was; with the sidebar collapsed on a focused view,
  a card's open carries the view's grep and clears it, and reopening the sidebar shows the
  placeholder; a view focused on another call while an open builds stays focused; opens and
  closes are sent one at a time, each after the last settles; an open from A in flight, a
  change to B, and an open from B leave B's window alone and close A's when it lands; an open
  whose view is on a cluster other than the held one sends nothing; every command carries the
  page, and none is sent before the subscribe returns it; after a double mount, the provider
  keeps the larger page whichever answer lands last, and folds the second snapshot; the first
  defined `clusterID` after a subscribe closes nothing, whether it lands before or after a
  snapshot on that cluster, and a snapshot holding another cluster's window closes it; a card draws
  each of its three states, the inline preview in the first alone, and no buttons in dashboard
  mode; each cluster-change path closes the windows, the deleted context's fallback included,
  and so does an `opened` frame carrying the old cluster after a change; a reload, the clusters
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
  says a view moves to a window, how a card reads while it is there, and that the log window
  draws the panel's header.

## Verification

`make test-changed` while working; `make test-js` and `make lint-js` before handing off. By
hand, on each platform: open a view in a window from the header and from a card, close the
app window and watch the log window close, change cluster by the picker and by history and
watch it close, press *Show chat* from another chat, and read the title in the OS's window
switcher. In a production build (`tauri build`), open a log window and reload it: the host
loads the path `logs`, which only Tauri's fallback to `index.html` serves.
