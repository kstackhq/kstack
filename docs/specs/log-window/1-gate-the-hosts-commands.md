---
title: Gate the host's commands
scope: host
status: Planned
---

# Gate the host's commands

**Needs:** nothing beyond `main`. **Unblocks:** step 2, whose capability file would grant
nothing narrower without this.

## In short

Today every custom command is open to every window, and every window can send any event to
any window. This step makes each command need an allow in a capability. It also replaces
`core:default` with the core permissions a page uses, which drops event sending and the menu,
tray, image, resources, path and app APIs nothing in `src/` calls.

**This step changes no behaviour.** The app window keeps every command it has, and nothing in
`src/` calls a permission this step drops.

## What is not in this step

- **A second capability.** Step 2 adds `logs.json` for `logs-*` windows.
- **Any new command.** Step 2.

## Design

### 1. The app manifest

`build.rs` lists every command in `tauri_build::AppManifest::commands`. tauri-build then
generates an `allow-<command>` permission for each, its underscores turned to hyphens
(`allow-graphql-query`), and Tauri refuses any command no capability allows. A command left
off the list is denied in every window.

A test reads `lib.rs`'s `generate_handler!` and `build.rs`'s list and asserts they hold the
same names. `generate_handler!` names paths (`commands::ready`, `app::quit`); the test
compares their last segment.

### 2. The default capability

`capabilities/default.json` (`main`, `window-*`) stops granting `core:default`. That set
carries `core:event:default`, whose `allow-emit` and `allow-emit-to` let a page send any event
to any window, and a page's emit is indistinguishable from the host's at the listener. In its
place the file grants:

- `core:window:default`, for the window getters (`isMaximized`).
- `core:webview:default`, for Tauri's injected devtools shortcut, which calls
  `internal_toggle_devtools` in debug and `debug-prod` builds.
- `core:event:allow-listen` and `core:event:allow-unlisten`.
- The five window-chrome permissions it grants today.
- An allow for each of the nine commands it serves today.

It drops `core:app:default`, `core:image:default`, `core:path:default`,
`core:resources:default`, `core:menu:default` and `core:tray:default`: the menus and the tray
are built in Rust, and no page calls the others.

`allow-listen` stays, for `host-file.ts` and `onResized`. A listener names its own target, so
a page can listen for any window's events; step 2 sends an app window nothing by event for
that reason.

## Decisions this step asks for

- **Commands are gated per window.** Without the gate, step 2's capability is a label and
  nothing else.
- **No page emits.** The host's pair in step 2 holds only if a page cannot send an event as
  the host.
- **The page holds only what it calls.** Listing the core permissions one by one costs a
  longer file and pins what the page holds, which is what the capability test is for.

## Tasks

1. **The manifest.** `AppManifest::commands` in `build.rs`; the test over `lib.rs` and
   `build.rs`.
2. **The capability.** `default.json`'s new list; the pinning test grows to it.

## Tests

- `default_capability_grants_only_window_chrome` becomes
  `default_capability_grants_what_the_app_window_calls`. It pins the list above and asserts
  no `core:default`, no emit, and no app, image, path, resources, menu or tray permission.
- `every_command_is_in_the_app_manifest` reads `lib.rs`'s handler list and `build.rs`'s
  manifest list and asserts they match.

The mock runtime's tests do not reach the gate: `tauri::test::mock_context` carries no app
manifest and no capability, so every `invoke` test passes whatever the capability says. The two
pinning tests and the check by hand are what hold *no behaviour change*.

## Security

- **What widens.** Nothing.
- **What narrows.** Every command needs an allow. No page can emit an event, or call the menu,
  tray, image, resources, path or app APIs.
- **What holds it.** The pinned list, and the manifest test.
- **Residual.** An app window page holds every command it held before.

## When it lands

- `src-tauri/CLAUDE.md`: the *webview is trusted by custom host commands* bullet names the
  gate, the core permissions kept and the renamed test.
- `docs/security-model.md`: the *No permission granted to the webview ahead of a consumer*
  row names the gate, the dropped permissions and both tests.

## Verification

`make test-changed` while working; `cargo test` in `src-tauri` before handing off.

By hand, on Linux or Windows: there `AppMenu` invokes `new_window` and `quit` from the page,
while macOS's native menu runs them in Rust. Start the app with `pnpm tauri dev` and, in
`main` and again in a `window-N`, check that each command still runs:

| Command | Seen by |
| --- | --- |
| `ready` | The window shows. |
| `graphql_query`, `graphql_subscribe`, `graphql_unsubscribe` | The cluster list loads, and a view that subscribes opens and closes. |
| `new_window` | *New Window* from `AppMenu` opens a window. |
| `update_host_file` | Changing the theme sticks across a new window. |
| `log_webview` | A console error from the page lands in `webview.log`. |
| `open_account_url` | Clicking the account avatar opens the browser. |
| `quit` | *Quit* from `AppMenu` quits. |

Toggle the devtools with their shortcut, to see `core:webview:default` still holds.
