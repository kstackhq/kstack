---
title: Log windows in the host
scope: host
status: Planned
---

# Log windows in the host

**Needs:** step 1. **Unblocks:** steps 3 and 4.

## In short

The host learns a second kind of window, labelled `logs-N`, paired to the app window that
opened it. It keeps the pair, builds the window, answers its page's questions, talks to the
app window over a channel that window made, closes the log windows when their app window
closes, and builds the native title from parts the page sends. Nothing in the webview changes
yet: no page calls any of this until step 3.

## What is not in this step

- **The page a log window loads.** Step 3. Here `/logs` is a route that does not exist yet.
- **What a viewport holds.** Step 4 defines it; here the host keeps and forwards it as opaque
  JSON inside an envelope it reads.

## Design

### 1. The capabilities

The step adds eight commands, four per window kind. Each is named in `build.rs`'s `COMMANDS`
and `lib.rs`'s `generate_handler!`, so `every_command_is_in_the_app_manifest` holds them.

`capabilities/default.json` (`main`, `window-*`) grows by `allow-log-window-subscribe`,
`allow-log-window-open`, `allow-log-window-focus` and `allow-log-window-close-all`, and
`default_capability_grants_what_the_app_window_calls` grows to match.

`capabilities/logs.json` (`logs-*`) grants `core:window:default`, `core:webview:default`,
`core:event:allow-listen` and `core:event:allow-unlisten`, the five window-chrome permissions,
`allow-ready`, `allow-graphql-query`, `allow-graphql-subscribe`, `allow-graphql-unsubscribe`,
`allow-log-webview`, `allow-log-window-view`, `allow-log-window-viewport`,
`allow-log-window-title` and `allow-log-window-show-chat`. It grants neither
`allow-new-window`, `allow-update-host-file`, `allow-quit`, `allow-open-account-url` nor any
app-window log command. `logs_capability_grants_the_viewer_and_nothing_else` pins the list.

`window-*` does not match `logs-N`, and `logs-*` admits every label the manager mints.

### 2. Labels, building and placement

`WindowManager` labels a log window `logs-N`, off the counter that labels `window-N`.
`build_window` takes the URL, the size and the bar height the traffic lights center in, so one
function builds both kinds. A log window loads `/logs`, opens at 1000×700 with the app
window's `min_inner_size`, and its page asks the host what to draw, so the URL carries nothing.
Its bar height is a constant beside `APP_BAR_HEIGHT`, 40px, kept in sync with step 3's bar.

`placement` takes the anchor's label and the new window's size. A log window anchors on the
app window that opened it; `new_window` and `show_main_window` pass no label and keep today's
anchor, the focused window or the last one built. A log window counts for that anchor too:
*New Window* from the tray, the Dock or a focused log window cascades off it.

**Every log-window path that opens or shows a window is a `WindowManager` method**:
`open_log_window` builds, and `surface_window(label)` unminimizes, shows and focuses. Each
checks `quitting()` on entry and queues `hide_if_quitting` behind what it presented, as
`new_window` and `show_main_window` do. `open_log_window` returns `Option<WebviewWindow>`:
`None` when a quit has begun and it built nothing, which §4 treats as a cancelled build.
`log_window_open` builds on the blocking pool, as `new_window` does, and takes the calling
`Webview` beside the `AppHandle`.

**The host destroys the log windows it closes.** `close_all`, an app window's `Destroyed` and
a cancelled build call `destroy()`, never `close()`. A `close()` raises `CloseRequested`, which
a page listening on `tauri://close-requested` cancels, and `logs.json` grants that listen. A
log window the user closes still goes through `CloseRequested`.

### 3. The pair

`src-tauri/src/log_windows.rs` holds the pairing behind one `Mutex`:

- **Pages**, `app label → { page, channel }`. `page` is a number from a host counter,
  minted by each `log_window_subscribe`. It names one app page's lifetime: it ends when that
  page's window is destroyed or its page reloads.
- **Entries**, `logs label → { app label, state, clusterId, chatId, callId, grep, generation,
  focusSeq, push }`. `logs-N` labels are never reused, so a label names one entry for the
  process's life.

Each entry's fields:

- `state` is `Building` or `Live`; §4 gives the transitions.
- `generation` steps on each `PageLoadEvent::Started` of the log window, from `page_started`.
  A page reads the current generation from `log_window_view`, which changes nothing, so a call
  made twice (React's dev-mode double mount) answers the same.
- `focusSeq` is the value of one host counter, taken when the entry is recorded and again on
  each `Focused(true)`. A new window ranks above the ones before it.
- `push` is the last `log_window_viewport` the page sent, or none.

**Every send on a channel happens under the lock**, together with the change it reports.
`log_window_subscribe` swaps the channel and sends the `snapshot` under the same lock, so no frame
can land before the snapshot or describe a change the snapshot already holds.

**The app window sees only `Live` entries.** A `Building` entry is in no `snapshot`, and no
frame names it until `opened`.

Every command reads the caller's label off its `tauri::Webview` argument, as `log_webview`
does, never off the payload. A log window therefore reaches its own app window and no other,
and an app window its own log windows. Each command also checks the label's kind before it
reads or changes anything: an app-window command refuses a caller that is not `main` or
`window-N`, and a log-window command one that is not `logs-N`. That refusal stands beside the
capability that already denies the call, so a `logs-N` page can never mint a page or record an
entry of its own.

**An app window's commands also carry the `page` it was given**, and each is refused
unless it is the caller's current one. The label says which window asks; the page says which
load of it. A request queued before its window was destroyed, before `main` was rebuilt under
the same label, or before its page reloaded names a page that has ended, and is refused
before it changes anything.

### 4. The lifecycle

An entry moves through these states, each transition under the lock:

| From | Event | To | Sends |
| --- | --- | --- | --- |
| — | `log_window_open`, no entry on that call | `Building` | nothing |
| `Building` | the build succeeds | `Live` | `opened` |
| `Building` | the build fails, or builds nothing as a quit has begun | removed | nothing |
| `Building` | `log_window_close_all`, or the app window's `Destroyed` | removed | nothing |
| `Building` | its own `Destroyed`, before `build()` returns | removed | nothing |
| `Live` | `log_window_close_all` | removed | `closed` |
| `Live` | its own `Destroyed` | removed | `closed` |
| `Live` | the app window's `Destroyed` | removed | nothing, as the channel is gone |
| removed | the build finishes | — | nothing; the new window is destroyed |
| removed | its own `Destroyed` | — | nothing |

**The lock guards the maps and the channel sends, and nothing else.** No window is built,
surfaced or destroyed, and no outcome awaited, while it is held. Each transition returns what the
wiring does next (§7), and the wiring does it after releasing the lock.

`log_window_open` reserves before it builds:

1. Under the lock, it checks the caller's page and looks for an entry on that call. A `Live`
   one: the transition returns its label, to surface. A `Building` one: it returns a receiver
   of that build's outcome. None: it mints a label, records a `Building` entry, and returns the
   label to build.
2. After the lock is released, it surfaces the `Live` window and returns its label, keeping its
   grep; or awaits the receiver and returns what it yields; or builds the window on the
   blocking pool through `open_log_window`. A `Live` window destroyed before it is surfaced
   returns `Cancelled`, as `surface_window` finds no window to show.
3. After a build, under the lock again: if the entry is still there, it becomes `Live`,
   `opened` is sent, and the outcome is the label. If the build failed, the entry is removed
   and the outcome is the error. If `open_log_window` returned `None` because a quit began,
   the entry is removed and the outcome is `Cancelled`. If the entry is gone, a close, the app
   window's destroy or the window's own destroy cancelled it while it built, and the outcome is
   `Cancelled`. After the lock is released, a cancelled build's window is destroyed, and the
   open returns its outcome.

A `Building` entry holds a `tokio::sync::watch` sender of its outcome, and every open on that
call but the first holds a receiver. Each outcome reaches all of them: the label, the build's
error, or `Cancelled`. The error travels as its message, since `tauri::Error` is not `Clone`
and every receiver reads its own copy; `error.rs` gains a `Cancelled` variant. A transition
that removes a `Building` entry sends `Cancelled` at once, so a waiting open returns without
waiting out the build. No caller hears success before a
window exists, and none waits on a sender that is gone: a dropped sender reads as
`Cancelled`.

A `Building` entry still answers its own window, whose page can load before `build()`
returns. `log_window_view` answers from it; a page load steps its generation; `Focused(true)`
stamps its `focusSeq`; a push is kept. None of these sends a frame while it builds: `opened`
carries the entry as it stands.

`log_window_close_all` removes every entry of the caller's and sends `closed` for each `Live`
one, under the lock; it destroys their windows after releasing it. A `Building` entry it
removed is cancelled: its waiters hear `Cancelled`, and its build finishes, finds no entry, and
destroys what it built. `log_window_focus` surfaces a `Live` window and does nothing for a
`Building` one, which surfaces when it is built.

`clusterId`, `chatId` and `callId` are each at most 256 bytes and `grep` at most 4 KiB; a
longer one is refused. The host reads nothing else in them. In a push it reads `generation`
and `grep`, and `viewport` stays opaque, bounded by the payload's size alone. The bound caps
one push; step 4 caps how often one is sent.

### 5. The commands

| Command | From | Does |
| --- | --- | --- |
| `log_window_subscribe(channel)` | app window | Mints a page, keeps it and the channel as the caller's, replacing any earlier pair, sends a `snapshot` of the caller's `Live` log windows down it, and returns `{ page }`. |
| `log_window_open { page, clusterId, chatId, callId, grep? }` | app window | Opens or surfaces the caller's window on that call, as §4 says. Returns the label once the window exists. |
| `log_window_focus { page, callId }` | app window | Surfaces the caller's `Live` window on that call. |
| `log_window_close_all { page }` | app window | Removes and destroys the caller's log windows, cancelling any still building. |
| `log_window_view` | log window | Returns `{ chatId, callId, grep, generation }`: the grep of the last push, else the open's, so a reload keeps the toolbar's. |
| `log_window_viewport { generation, grep, viewport }` | log window | Refused unless `generation` is the caller's current one and the payload is within 256 KiB. Keeps the payload as the caller's `push` and its `grep` as the caller's grep; sends a `viewport` frame if the entry is `Live`. |
| `log_window_title { parts }` | log window | Refused unless `parts` holds at most three strings, each within 4 KiB. Sets the caller's native title from `window_title::build(parts)`. |
| `log_window_show_chat` | log window | Surfaces the caller's app window and sends a `showChat` frame down its channel. |

### 6. The channel's frames

The channel carries six frames, one key each, in the order the host made them, so the page
never has to merge a list with a stream:

- `snapshot { windows }`: first on every channel. Each window is `{ label, clusterId, chatId,
  callId, focusSeq, push }`, with `push` null when there is none. `Live` entries alone.
- `opened { window }`: the same shape, when an entry becomes `Live`.
- `viewport { label, push }`: after a push to a `Live` entry, and with `push` null when a
  `Live` entry's page reloads.
- `focused { label, focusSeq }`: on a `Live` log window's `Focused(true)`.
- `closed { label }`: when a `Live` entry is removed by a close or its own destroy.
- `showChat { chatId }`: from `log_window_show_chat`.

### 7. The window events

The pairing's transitions are functions in `log_windows.rs`: `reserve`, `built`, `failed`,
`close_all`, `focused`, `page_started` and `destroyed`. `lib.rs`'s handlers and the commands
call them; each returns what the wiring must do (which windows to destroy, which to surface), so
a test drives any interleaving of them directly, with no window built.

- **A log window's `Focused(true)`** stamps its `focusSeq`, and sends `focused` if `Live`.
- **A log window's `Destroyed`** removes its entry, and sends `closed` if it was `Live`. A
  `Building` entry it removes is cancelled.
- **An app window's `Destroyed`** ends its page, drops its channel, removes its entries, and
  returns the `Live` ones' labels for the wiring to destroy. Its `Building` ones are cancelled,
  as `log_window_close_all` cancels them. It ends whatever page the label holds, which is safe
  because Tauri runs the old window's `Destroyed` handler on the event loop before a rebuild
  queued from another thread can create a new window under that label.
- **A `PageLoadEvent::Started`** steps a log window's generation and clears its push. On an
  app window it ends the page and drops the channel; its log windows stay, and the next
  `log_window_subscribe` lists them.

`app.rs` adds the `Focused` arm to its `RunEvent::WindowEvent` match and calls the `lib.rs`
handler; it decides nothing.

### 8. The title

`src-tauri/src/window_title.rs` has one function, `build(parts: &[&str]) -> String`. It drops
from each part what `visible-text.ts` treats as invisible: every `Cc`, `Cf`, `Cs`, `Zl`, `Zp`
and `Zs` character, every `Default_Ignorable_Code_Point`, and U+2800. It keeps the space
alone, where `visible-text.ts` also keeps tab and newline, since a title is one line. The
classes come from a crate or a generated table, with tests on each class but `Cs`, which a
Rust `&str` cannot hold. It cuts each part to 60 characters on a character boundary, puts
`Logs` first, and joins with ` · `. The webview sends the parts; it never sends a title.

## Decisions this step asks for

- **The host owns the pair, and talks to an app window over its channel.** A page cannot
  forge a link to another window, send an event as the host, or listen for another window's,
  and a closing app window is a host event its page may not live to see.
- **One subscription, snapshot first, every send under the lock.** The app window folds one
  ordered stream, so a `closed` can never race a list that still has the window.
- **The log window asks the host what to draw.** The pair already holds it, so the URL
  encodes nothing and a reload still works.
- **The host stamps each page load.** A reloaded page's revisions start over, so the host
  steps the generation on the load it observes and refuses a push from an earlier one. A page
  cannot step it.
- **Open reserves before it builds, and the app window sees only what exists.** An entry is
  `Building` until its window is, and no frame names it before then. A double click builds one
  window, both clicks hear its outcome, and a close or a destroy cancels a build in flight.
- **An app window's command names its page.** The label alone cannot tell a destroyed
  `main` from the one rebuilt in its place, or a page from the one that reloaded over it.
- **The host destroys what it closes.** A `close()` asks the page, and a page may refuse; a
  log window that must not outlive its app window cannot be given that choice.
- **The host builds the title.** The parts are the model's, the user's and the cluster's
  text, and the OS gets nothing the host did not strip.

## Tasks

1. **The title builder.** `window_title.rs` and its tests.
2. **The labels and the capabilities.** `logs-N`, `logs.json`, the four allows in
   `default.json`, the eight names in `build.rs`, `build_window`'s and `placement`'s
   parameters, `open_log_window` and `surface_window` behind the quit check.
3. **The pair, the channel and the commands.** `log_windows.rs`, the pages, the lifecycle,
   the eight commands, the six frames, the window events.

Tasks 1 and 2 can be built at the same time.

## Tests

- **The title.** `build` drops each listed class but `Cs`, keeps the space, drops tab and
  newline, cuts on a character boundary, puts `Logs` first and joins with the separator; an
  empty part is kept, so the positions stay stable.
- **The capabilities.** `logs_capability_grants_the_viewer_and_nothing_else`;
  `default_capability_grants_what_the_app_window_calls` with the four new allows;
  `every_command_is_in_the_app_manifest` with the eight new names.
- **The window manager.** The label prefix; a log window's anchor is its app window even when
  another window was built last, checked on the chosen window, since the mock runtime puts every
  window at the origin; with no anchor it centers at its own size; `open_log_window` returns
  `None` and `surface_window` shows nothing once a quit has begun; `log_windows.rs` and its
  wiring call `destroy()` and never `close()`, read from source as
  `build_window_installs_the_navigation_policy` does.
- **The page.** `log_window_subscribe` returns a new page each time, and a `snapshot` is first
  on its channel; a second replaces the first. Each app-window command is refused with an
  ended page: after the window's destroy, after its page reloads, and after `main` is
  destroyed and rebuilt, where the rebuilt window's page works and the old one's open, queued
  before the destroy and reserving after it, records nothing.
- **The lifecycle.** Each row of §4's table, driven through the transition functions: a
  `Building` entry is in no `snapshot` and sends no frame until `opened`, which carries a
  `focusSeq`, generation and push stamped while it built; a failed build sends nothing and
  both waiting opens return its error; a second open while the first builds builds once and
  returns its label; `close_all` during a build sends nothing, and the finished build destroys
  its window and sends no `opened`; `close_all` on `Live` entries sends `closed` for each, and
  their later `Destroyed` sends nothing; an app window's destroy during a build cancels it,
  and returns its `Live` windows' labels. With a build in flight and two more opens
  waiting on it, `close_all` and the app window's destroy each make both waiters return
  `Cancelled` before the build finishes; the building open returns `Cancelled` when it does
  and destroys its window; none returns a label, and none hangs. A build that returns `None`
  for a quit records no `Live` entry, sends no `opened`, and its waiters hear `Cancelled`. The
  log window's own `Destroyed` while it builds removes the entry, sends nothing, and the finished
  build destroys what it built. Every window the host closes it destroys, so a page listening on
  `tauri://close-requested` cannot keep one open. An app window's page reload during a build
  keeps the entry, and the new page hears `opened`. The transitions return their actions rather
  than performing them, so the test performs each one after the transition.
- **The pair.** Open surfaces an existing window on the same call, keeping its grep, and builds
  for another; an open whose `Live` window is destroyed before it is surfaced returns
  `Cancelled`; a log window's command reaches only its own app window, and an app window's
  only its own log windows; each app-window command from a `logs-N` caller and each
  log-window command from a `main` or `window-N` caller is refused and changes nothing, so a
  `logs-N` caller of `log_window_subscribe` mints no page; frames reach the channel of the paired
  app window and no other; an app window's page load ends its page and
  keeps its log windows; a log window's page load steps the generation, clears the push and
  sends the null frame; `log_window_view` answers the same twice, with the grep last pushed; a
  push with a stale generation is refused; a new window's `focusSeq` is above the others', and
  `focused` carries a rising one; an id or grep over its bound, a push over 256 KiB, and a
  title with four parts or a part over 4 KiB are refused. Each uses the mock runtime and
  `testutil::OneMonitor`.

## Security

- **What widens.** A second window kind holds the whole GraphQL surface, since it reads logs
  through `graphql_query`, which forwards operations unexamined. The CSP stays the containment.
  The app window gains four commands, each acting only on its own log windows.
- **What narrows.** A log window cannot open windows, change host preferences, quit the app
  or open the account URL. Its capability's list is pinned.
- **What holds it.** A page never names a target window: every lookup starts from the caller's
  label, and what the host says to an app window rides a channel that page made, so a listener
  on another window's events hears none of it. An app window's command must name its current
  page, so a request from a page that is gone changes nothing, and no log window is published to
  a window that did not ask for it. A build that outlives a reload of its app window's page is
  still that window's, and the new page learns of it from `opened` or its `snapshot`. The native
  title is attacker-controlled text, built by the host from stripped and bounded parts. A push
  is bounded, opaque past the two fields the host reads, and stamped with a generation only the
  host steps. No window opens once a quit has begun. Each command checks its caller's window
  kind, beside the capability. The host destroys the log windows it closes, so no page can
  refuse the close and outlive its app window.
- **Residual.** A compromised log window page can do to its cluster what a compromised app
  window page can.

## When it lands

- `src-tauri/CLAUDE.md`: labels are `main`, `window-N` or `logs-N`; `log_windows.rs`, the pair,
  the page, the `Building`/`Live` lifecycle, the channel and the lock its sends take; the two
  capability files and the thirteen commands of the app window; every path that opens or shows a
  window ends in a `WindowManager` method that checks the quit; each command checks its
  caller's window kind; the host destroys the log windows it closes, never `close()`s them;
  `window_title`.
- `docs/security-model.md`: the capability row names both files and the app window's thirteen
  commands; a row saying the host's frames to an app window ride its own channel; a row for
  the title, pinned by `window_title`'s tests; a row saying a log window cannot outlive its app
  window, since the host destroys it, pinned by the lifecycle tests.

## Verification

`make test-changed` while working; `cargo test` in `src-tauri` before handing off. There is
nothing to check by hand until step 3 gives the window a page.
