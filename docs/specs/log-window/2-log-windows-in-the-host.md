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
- **The viewport's contents and bound.** Step 4 defines what a push carries; here the host
  keeps and forwards it as opaque JSON.

## Design

### 1. The capability

`capabilities/logs.json` (`logs-*`) grants what `default.json` grants after step 1, less the
app-window commands, plus the log-window commands below: `core:window:default`,
`core:webview:default`, `core:event:allow-listen` and `core:event:allow-unlisten`, the five
window-chrome permissions, `ready`, `graphql_query`, `graphql_subscribe`,
`graphql_unsubscribe`, `log_webview`, `log_window_view`, `log_window_viewport`,
`log_window_title` and `log_window_show_source`. It grants neither
`new_window`, `update_host_file`, `quit` nor `open_account_url`. A test pins the list, as
`default_capability_grants_what_the_app_window_calls` does.

### 2. Labels and building

`WindowManager` labels a log window `logs-N`, off the counter that labels `window-N`.
`build_window` takes the URL, the size and the bar height the traffic lights center in, so one
function builds both kinds. A log window loads `/logs`, opens at 1000×700 with the app
window's `min_inner_size`, and cascades off the app window that opened it. Its page asks the
host what to draw, so the URL carries nothing.

### 3. The pair and the channel

The pairing is a `Mutex<HashMap>` on the manager, `logs label → { app label, clusterId,
chatId, callId, grep, generation, focusSeq, viewport }`. `generation` steps on each load of
the log window's page, `focusSeq` is the value of one host counter when the window was last
focused, and `viewport` is the last one its page pushed. Beside the map, one
`tauri::ipc::Channel` per app label, made by `log_windows_watch`; a reload's call replaces
it, and the app window's `Destroyed` drops it.

Every command reads the caller's label off its `tauri::Webview` argument, as `log_webview`
does, never off the payload. A log window therefore reaches its own app window and no other,
and an app window its own log windows.

| Command | From | Does |
| --- | --- | --- |
| `log_windows_watch(channel)` | app window | Keeps the channel as the caller's and sends a `snapshot` of the caller's log windows down it first. Every later frame goes down it in order. |
| `log_window_open { clusterId, chatId, callId, grep? }` | app window | Focuses the caller's window on that call if one is open, keeping that window's grep, else builds one. Returns the label. |
| `log_window_focus { callId }` | app window | Focuses the caller's window on that call. |
| `log_window_close_all` | app window | Closes the caller's log windows. |
| `log_window_view` | log window | Steps the caller's `generation`, clears its retained viewport, and sends an empty `viewport` frame to the app window. Returns `{ chatId, callId, grep, generation }`, the grep the last push carried, so a reload keeps the toolbar's. |
| `log_window_viewport { viewport }` | log window | Refused unless `viewport.generation` is the caller's current one and the JSON is within 256 KiB. Keeps it, and its `grep`, as the caller's last, and sends a `viewport` frame to the app window. |
| `log_window_title { parts }` | log window | Sets the caller's native title from `window_title::build(parts)`. |
| `log_window_show_source` | log window | Focuses the caller's app window and sends a `showChat` frame down its channel. |

The channel carries six frames, one key each, in the order the host saw them, so the page
never has to merge a list with a stream:

- `snapshot { windows }`: first on every channel. Each window is `{ label, clusterId, chatId,
  callId, focusSeq, viewport }`.
- `opened { window }`: the same shape, after `log_window_open` builds one. `open`'s return is
  the label alone; the page's entry comes from this frame.
- `viewport { label, viewport }`: after a push, and the empty one on a reload.
- `focused { label, focusSeq }`: on a log window's `Focused(true)`.
- `closed { label }`: when one is destroyed.
- `showChat { chatId }`: from `log_window_show_source`.

On an app window's `Destroyed`, the host closes every log window paired to it.

`clusterId`, `chatId`, `callId` and `grep` are opaque to the host, which bounds their length
and nothing else. The viewport is opaque too, bounded by its size alone.

### 4. The title

`src-tauri/src/window_title.rs` has one function, `build(parts: &[&str]) -> String`. It drops
from each part what `visible-text.ts` treats as invisible: every `Cc`, `Cf`, `Cs`, `Zl`, `Zp`
and `Zs` character, every `Default_Ignorable_Code_Point`, and U+2800, with the space kept. The
classes come from a crate or a generated table, with tests on each class. It cuts the part to
60 characters on a character boundary, puts `Logs` first, and joins with ` · `. The webview
sends the parts; it never sends a title.

## Decisions this step asks for

- **The host owns the pair, and talks to an app window over its channel.** A page cannot
  forge a link to another window, send an event as the host, or listen for another window's,
  and a closing app window is a host event its page may not live to see.
- **One watch, snapshot first.** The app window folds one ordered stream, so a `closed` can
  never race a list that still has the window.
- **The log window asks the host what to draw.** The pair already holds it, so the URL
  encodes nothing and a reload still works.
- **A page generation.** A reloaded page's revisions start over, so the host stamps each load
  and refuses a push from an earlier one.
- **The host builds the title.** The parts are the model's, the user's and the cluster's
  text, and the OS gets nothing the host did not strip.

## Tasks

1. **The title builder.** `window_title.rs` and its tests.
2. **The label and the capability.** `logs-N`, `logs.json`, `build_window`'s parameters.
3. **The pair, the channel and the commands.** The map, `log_windows_watch`, the eight
   commands, the six frames, closing with the app window.

Tasks 1 and 2 can be built at the same time.

## Tests

- **The title.** `build` drops each listed class, keeps the space, cuts on a character
  boundary, puts `Logs` first and joins with the separator; an empty part is kept, so the
  positions stay stable.
- **The host.** The label prefix; `logs_capability_grants_the_viewer_and_nothing_else`; open
  focuses an existing window on the same call, keeping its grep, and builds for another; a log
  window's command reaches only its own app window, and an app window's only its own log
  windows; frames reach the channel of the paired app window and no other; a `snapshot` is
  first on every channel and a second `log_windows_watch` replaces the first; `opened` follows
  a build; `log_window_view` steps the generation, clears the viewport, sends the empty frame
  and answers with the grep last pushed; a push with a stale generation is refused; `focused`
  carries a rising `focusSeq`; the pair is dropped and `closed` sent on destroy; an app
  window's destroy closes its log windows; a `chatId` over the bound and a viewport over 256
  KiB are refused. Each uses the mock runtime and `testutil::OneMonitor`.

## Security

- **What widens.** A second window kind holds the whole GraphQL surface, since it reads logs
  through `graphql_query`, which forwards operations unexamined. The CSP stays the containment.
- **What narrows.** A log window cannot open windows, change host preferences, quit the app
  or open the account URL. Its capability's list is pinned.
- **What holds it.** A page never names a target window: every lookup starts from the caller's
  label, and what the host says to an app window rides a channel that page made, so a listener
  on another window's events hears none of it. The native title is attacker-controlled text,
  built by the host from stripped and bounded parts. A viewport is kept and forwarded as
  bounded, opaque JSON, stamped with its page's generation.
- **Residual.** A compromised log window page can do to its cluster what a compromised app
  window page can.

## When it lands

- `src-tauri/CLAUDE.md`: labels are `main`, `window-N` or `logs-N`; the pairing and the
  channel; the two capability files; `window_title`.
- `docs/security-model.md`: the capability row names both files; a row saying the host's
  frames to an app window ride its own channel; a row for the title, pinned by
  `window_title`'s tests.

## Verification

`make test-changed` while working; `cargo test` in `src-tauri` before handing off. There is
nothing to check by hand until step 3 gives the window a page.
