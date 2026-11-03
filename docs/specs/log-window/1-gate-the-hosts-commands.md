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
any window. This step makes each command need an allow in a capability, and takes the page's
event sending, and the menu and tray APIs nothing calls, out of the one capability there is.

**This step changes no behaviour.** The app window keeps every command it has, and nothing in
`src/` emits an event or calls a menu or tray API.

## What is not in this step

- **A second capability.** Step 2 adds `logs.json` for `logs-*` windows.
- **Any new command.** Step 2.

## Design

### 1. The app manifest

`build.rs` lists every command in `tauri_build::AppManifest::commands`, so each needs an
`allow-<command>` permission in a capability. A command left off the list is denied in every
window, so the omission shows at once; a test reads `lib.rs`'s `generate_handler!` and
`build.rs`'s list and asserts they match, to name the missing one.

### 2. The default capability

`capabilities/default.json` (`main`, `window-*`) stops granting `core:default`. That set
carries `core:event:default`, whose `allow-emit` and `allow-emit-to` let a page send any event
to any window, and a page's emit is indistinguishable from the host's at the listener. The
file lists `core:default`'s parts instead, with `core:event:allow-listen` and
`core:event:allow-unlisten` in place of `core:event:default`, and without `core:menu:default`
and `core:tray:default`: the menus and the tray are built in Rust. It keeps the five
window-chrome permissions and adds an allow for each of the nine commands it serves today.

`allow-listen` stays, for `host-file.ts`. A listener names its own target, so a page can
listen for any window's events; step 2 sends an app window nothing by event for that reason.

## Decisions this step asks for

- **Commands are gated per window.** Without the gate, step 2's capability is a label and
  nothing else.
- **No page emits.** The host's pair in step 2 holds only if a page cannot send an event as
  the host.
- **The parts are spelled out.** Listing `core:default`'s parts costs a longer file and pins
  what the page holds, which is what the capability test is for.

## Tasks

1. **The manifest.** `AppManifest::commands` in `build.rs`; the test over `lib.rs` and
   `build.rs`.
2. **The capability.** `default.json`'s new list; the pinning test grows to it.

## Tests

- `default_capability_grants_only_window_chrome` grows to the gated list and asserts no
  `core:default`, no emit, menu or tray permission.
- A test reads `lib.rs`'s handler list and `build.rs`'s manifest list and asserts they match.

## Security

- **What widens.** Nothing.
- **What narrows.** Every command needs an allow. No page can emit an event, or call the menu
  or tray APIs.
- **What holds it.** The pinned list, and the manifest test.
- **Residual.** An app window page holds every command it held before.

## When it lands

- `src-tauri/CLAUDE.md`: the gate and the dropped permissions.
- `docs/security-model.md`: the capability row names the gate and the dropped permissions.

## Verification

`make test-changed` while working; `cargo test` in `src-tauri` before handing off. By hand:
start the app, open a second window, change the theme and quit from the menu, so every command
is seen to still run.
