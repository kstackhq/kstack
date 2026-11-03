---
title: The log viewer in its own window
scope: host, frontend
status: Planned
---

# The log viewer in its own window

**Answers:** [#507](https://github.com/kstackhq/kstack/issues/507). **Needs:** nothing beyond
`main`.

## In short

Today a log view is drawn in one place: an app window's right sidebar, where it shares width
with the chat. This spec lets a view leave the sidebar for a window of its own.

- **Open in window** sits on the panel's header and on each transcript card that can show a
  view. It moves the view: the sidebar lets it go, and a new window draws it. A view is in one
  place at a time.
- **A log window belongs to one app window**, the one that opened it. The host keeps the pair
  and enforces it: a log window reaches its own app window and no other, and it closes when
  that window closes or switches cluster.
- **The log window is the viewer under a slim bar.** The bar names the cluster and the chat and
  has **Show chat**, which brings the app window back. The native title names the chat and the
  cluster too, for the OS's window switchers.
- **In the app window, the card reads *Showing in a window*** with **Show window**, where
  *Showing in the sidebar* is drawn today.
- **The selection still rides a send.** The log window pushes its viewport to the app window
  through the host, and the composer's chip says the lines come from a window.

Beside the feature, the spec gates the host's commands per window. Today every command is open
to every window, so a capability file for log windows could grant nothing narrower.

## What is not in this spec

- **The dashboard.** Its chat has no Expand, so it has nothing to open.
- **Reopening on relaunch.** App windows do not reopen, and a log window cannot outlive its app
  window.
- **Any other secondary window.** Exec gets its own spec. The layout, the label prefix and the
  pairing are built so it can reuse them.

## Answers to #507

**1. Opening.** Both places, and it moves. The panel's header gets *Open in window* beside its
close. A card gets *Open in window* beside Expand or *View around this*, in chat mode alone. A
view is in one place at a time: the card has one state to draw, and the snapshot has one
source.

**2. Linking.** Three ways, none of them a number or a colour. The log window's bar shows the
cluster's dot and name, the chat's title, and *Show chat*. Its native title reads
`Logs · <first source> · <chat title> · <cluster>`, which is what the taskbar, Mission Control
and the Window menu show. The app window shows its log windows where their calls are: each
card reads *Showing in a window* with *Show window*. App windows carry no visible number to
match, and the cluster's dot already has a colour.

**3. Lifetime.**

| Event | The log window |
| --- | --- |
| Its app window closes | Closes. The host closes it on the app window's `Destroyed`. |
| Its app window switches cluster | Closes, as the sidebar's view clears today. |
| Its app window moves to another chat or mode | Stays open and live. Its selection stops riding until the app window is back on its chat. |
| Its chat is deleted | Draws *This chat was deleted.*, as `OpenChat` does, and stops reading. |
| Its cluster is deleted | Draws the viewer's own errors until the chat's deletion follows. |

A log window never outlives its app window, so there is no detached state to draw.

**4. Many windows.** As many as the user opens, one per call per app window. *Open in window*
or *Show window* on a call that already has one focuses it. There is no cap.

**5. The snapshot.** It rides, under today's rule: the view is the composer's chat's and the
app window is in chat mode. The log window pushes its viewport to the host, and the host
forwards it to the app window alone. The composer keeps reading a `LogViewportHandle` at the
press, so `onSend` stays synchronous. With both a sidebar view and log windows on one chat, the
sidebar's view rides while the sidebar is open, else the log window focused last. The chip says
which: *N selected lines from the sidebar* or *from a log window*.

**6. Chrome.** A slim bar, 40px, with no menu, omnibox or history. macOS keeps its traffic
lights, placed for the slimmer bar, and its hidden title. Linux and Windows draw
`WindowControls` at the right with no `AppMenu`, so `Ctrl+N` and `Ctrl+Q` do nothing there.

**7. Restore.** No. App windows do not reopen either.

## Design

### 1. The host gates its commands per window

`build.rs` lists every command in `tauri_build::AppManifest::commands`, so each needs an
`allow-<command>` permission in a capability. `capabilities/default.json` (`main`,
`window-*`) adds an allow for every command it serves today and for the app-window commands
below. A new `capabilities/logs.json` (`logs-*`) grants `core:default`, the five window-chrome
permissions, `ready`, `graphql_query`, `graphql_subscribe`, `graphql_unsubscribe`,
`log_webview` and the log-window commands below. It grants neither `new_window`,
`update_host_file`, `quit` nor `open_account_url`. A test pins each file's list, as
`default_capability_grants_only_window_chrome` does today.

### 2. The host builds and pairs log windows

`WindowManager` labels a log window `logs-N`, off the counter that labels `window-N`.
`build_window` takes the URL, the size and the bar height the traffic lights center in, so one
function builds both kinds. A log window opens at 1000×700 with the app window's
`min_inner_size`, cascaded off the app window that opened it.

The pairing is a `Mutex<HashMap>` on the manager, `logs label → { app label, chatId, callId }`.
Every command reads the caller's label off its `tauri::Webview` argument, as `log_webview`
does, never off the payload. A log window therefore reaches its own app window and no other,
and an app window its own log windows.

| Command | From | Does |
| --- | --- | --- |
| `log_window_open { chatId, callId, grep? }` | app window | Focuses the caller's window on that call if one is open, else builds one. Returns the label. |
| `log_window_focus { callId }` | app window | Focuses the caller's window on that call. |
| `log_windows` | app window | The caller's `{ label, callId }` pairs, so a reloaded page can rebuild its cards. |
| `log_window_close_all` | app window | Closes the caller's log windows. |
| `log_window_viewport { viewport }` | log window | Emits `log-window:viewport { label, callId, viewport }` to the caller's app window. |
| `log_window_title { parts }` | log window | Sets the caller's native title from `window_title::build(parts)`. |
| `log_window_show_source` | log window | Focuses the caller's app window and emits `log-window:show-chat { chatId }` there. |

Two events reach an app window on their own: `log-window:closed { label, callId }` when one
of its log windows is destroyed, and the host closes every log window paired to an app window
on that window's `Destroyed`.

`chatId`, `callId` and `grep` are opaque to the host, which bounds their length and nothing
else. They enter the window's URL as path segments and a query parameter through `Url`'s
segment and query APIs, never through `format!`.

### 3. The title is built by the host

`src-tauri/src/window_title.rs` has one function, `build(parts: &[&str]) -> String`. It drops
every `Cc` character and every bidi or invisible format character (U+061C, U+200B–U+200F,
U+202A–U+202E, U+2060–U+2069, U+FEFF, U+00AD) from each part, cuts the part to 60 characters on
a character boundary, and joins the parts with ` · `. The webview sends the parts; it never
sends a title.

### 4. The log window's route

`src/routes/_logs.tsx` is a pathless layout rendering `LogsWindowLayout`
(`src/layouts/logs-window-layout.tsx`): the bar over the routed page, no sidebars, no
`AppDialogs`. `src/routes/logs-window.tsx` is `/logs/$chatId/$callId` with search
`{ grep?: string }`.

The route watches `chatsWatch` for the chat's title and cluster, and `chatMessagesWatch` for
the call. Until both bookmarks land it draws nothing. Then:

- a chat the map lacks draws *This chat was deleted.*;
- a call the chat lacks, or one `viewOfCall` returns nothing for, draws *This log view is not
  available.*;
- otherwise it draws `LogViewer` with the view, the grep from the URL, and a
  `LogViewportHandle` of its own.

`viewOfCall(call)` in `src/lib/logs-view.tsx` is the mapping the three cards make today: a
LogsView call's action, or a LogsRead or LogsWatch call's `viewAround`. The cards and the route
share it.

The window holds no provider state of its own. The grep the user applies from the toolbar is
route state. Columns and wrap still read the shared `kstack:log-viewer-*` keys, as app-wide
defaults. A `localStorage` key never carries a view.

The bar is the drag region, as the app bar is. It draws the cluster's dot and name and the
chat's title through `VisibleText`, then *Show chat*, which calls `log_window_show_source` and
opts out of dragging. An effect sends `log_window_title` with the first source, the chat's
title and the cluster's name whenever one changes.

The viewer writes the window's handle as it does the sidebar's. A subscriber on the handle
sends `log_window_viewport` at most once per animation frame, and only when the viewport
changed.

### 5. The app window's side

`src/lib/log-windows.tsx` adds `LogWindowsProvider` under `LogsViewProvider` in `AppLayout`.
It holds the window's log windows by call id: seeded from `log_windows` on mount, added by
`open`, dropped by `log-window:closed`. For each it keeps a `LogViewportHandle` mirror fed by
`log-window:viewport`, and the time its window was last focused. It exposes
`open(view)`, which calls `log_window_open` and then, if the sidebar shows that call, clears
the focused view and closes the sidebar, and `focus(callId)`.

- **The cards** draw *Open in window* beside Expand or *View around this*. While their call
  has a window they draw *Showing in a window* with *Show window* instead, where *Showing in the
  sidebar* goes today.
- **The panel's header** draws *Open in window* beside its close.
- **The composer** picks the view that rides: the sidebar's while it is open on this chat,
  else the mirror of the log window on this chat focused last, else none. `shownView` becomes
  `{ view, viewport, where: 'sidebar' | 'window' }` and the chip reads `where`.
- **The cluster picker's popover** calls `log_window_close_all` beside the clears it runs
  today.
- **`log-window:show-chat`** navigates to `/chat/$chatId`. Its cluster is the window's, since a
  switch would have closed the log window.

## Decisions this spec asks for

- **Move, not copy.** One place per view keeps the card's state and the snapshot's source
  unambiguous.
- **The host owns the pair.** A page cannot forge a link to another window, and a closing app
  window is a host event its page may not live to see.
- **A log window closes with its app window and on its cluster switch.** No orphans, no
  detached state.
- **The viewport is pushed, not pulled.** The chip needs the count live, and a pull at send
  would make `onSend` wait on another window.
- **The sidebar's view wins over a log window's.** It is beside the composer; the user sees it
  while typing.
- **Commands are gated per window.** Without the gate the new capability is a label and
  nothing else.

## Tasks

All start Planned.

1. **Gate the commands.** `AppManifest::commands` in `build.rs`; every current command allowed
   in `default.json`; the test pins the list. No behaviour change. *(host)*
2. **The title builder.** `window_title.rs` and its tests. *(host)*
3. **Log windows in the host.** The `logs-N` label, `logs.json`, the pairing, the seven
   commands, the two events, closing with the app window. *(host, needs 1 and 2)*
4. **The route and the layout.** `_logs`, `/logs/$chatId/$callId`, `LogsWindowLayout`,
   `viewOfCall`, the title and viewport pushes. *(frontend, needs 3)*
5. **The app window's side.** `LogWindowsProvider`, the cards' and the panel's buttons, the
   composer's pick and chip, the cluster switch, *Show chat*. *(frontend, needs 4)*

Tasks 1 and 2 can be built at the same time.

## Tests

- **Task 1.** `default_capability_grants_only_window_chrome` grows to the gated list. A test
  reads `build.rs` and asserts every handler in `invoke_handler` is in the manifest's list, so a
  new command cannot be forgotten.
- **Task 2.** `build` drops each listed character class, cuts on a character boundary, and
  joins with the separator; an empty part is kept, so the positions stay stable.
- **Task 3.** The label prefix; `logs_capability_grants_the_viewer_and_nothing_else`; open
  focuses an existing window on the same call and builds for another; a log window's command
  reaches only its own app window, and an app window's only its own log windows; the pair is
  dropped and `log-window:closed` emitted on destroy; an app window's destroy closes its log
  windows; a `chatId` over the bound is refused. Each uses the mock runtime and
  `testutil::OneMonitor`.
- **Task 4.** The route draws nothing before both bookmarks, each notice after them, and the
  viewer with the URL's grep; the title is sent with the three parts and resent when one
  changes; the viewport is sent once per frame and not when unchanged; `viewOfCall` for the
  three call kinds and for a call with no action.
- **Task 5.** `open` moves a view out of the sidebar and focuses an open window; a card draws
  each of its three states; the composer picks the sidebar's view over a window's, the window
  focused last among windows, and none when the chat differs; the chip's two wordings; a
  cluster switch closes the windows; `log-window:show-chat` navigates to the chat.

## Security

- **What widens.** A second window kind holds the whole GraphQL surface, since it reads logs
  through `graphql_query`, which forwards operations unexamined. The CSP stays the containment.
- **What narrows.** Every command is gated per window. A log window cannot open windows,
  change host preferences, quit the app or open the account URL. Each capability's list is
  pinned.
- **What holds it.** A page never names a target window: every lookup starts from the caller's
  label. The native title is attacker-controlled text, built by the host from stripped and
  bounded parts. A viewport crossing windows is drawn only as a count in the chip and as data
  in the snapshot, never as HTML. A view travels in a URL and host events, never in
  `localStorage`, which every window shares.
- **Residual.** A compromised log window page can do to its cluster what a compromised app
  window page can.

## When it lands

- `src/CLAUDE.md`: *Layouts* names `_logs` and `LogsWindowLayout`; the right sidebar's section
  says a view moves to a window and how a card reads while it is there; the composer's snapshot
  rule names the pick.
- `src-tauri/CLAUDE.md`: labels are `main`, `window-N` or `logs-N`; the pairing; the gate and
  the two capability files; `window_title`.
- `docs/security-model.md`: the capability row names both files and the gate; a row for the
  title, pinned by `window_title`'s tests.
- `docs/security/`: a review record for the new window and its commands.
- An ADR: *a log window belongs to one app window, and the host keeps the pair*.

## Verification

Before any build or test in the Linux sandbox, run `bash scripts/sandbox-dev-setup.sh`. While
working, `make test-changed`. At the gate: `make test`, `make lint`, `make vet`, and a green
`Coverage · Gate` on the PR. Then by hand, on each platform: open a view in a window from the
header and from a card, close the app window and watch the log window close, switch cluster and
watch it close, select lines in the window and send from the chat, and read the title in the
OS's window switcher.
