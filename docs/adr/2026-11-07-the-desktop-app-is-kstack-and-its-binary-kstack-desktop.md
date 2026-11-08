---
title: The desktop app is Kstack, and its binary kstack-desktop
date: 2026-11-07
scope: repo
status: Accepted
---

# The desktop app is Kstack, and its binary kstack-desktop

## Context

The desktop app and the CLI both shipped a binary named `kstack`. The app's came from
`[[bin]] name = "kstack"` in `src-tauri/Cargo.toml`, which Tauri's bundler takes as the main
binary's name. The display name, `productName` in `src-tauri/tauri.conf.json`, is `Kstack`.

| Platform | App binary | CLI binary |
|---|---|---|
| Windows | `PFiles/Kstack/kstack.exe` | `kstack.exe` |
| Linux (.deb/.rpm) | `/usr/bin/kstack` | `kstack` |
| macOS | `Kstack.app/Contents/MacOS/kstack` | `kstack` |

On Linux the app's binary sat on PATH beside the CLI. `cli/internal/desktop/locate.go` had to
check that `/usr/bin/kstack` was not the CLI itself, and could never search PATH. On Windows, Task
Manager, firewall prompts and `where kstack` showed two programs under one name. No release had
shipped, so a rename had no installs to migrate ([#787](https://github.com/kstackhq/kstack/issues/787)).

## Decision

The display name stays `Kstack`. The app's binary is `kstack-desktop`, the Cargo package's name:
`src-tauri/Cargo.toml`'s `[[bin]]` names it, so `tauri dev` and `tauri build` produce the same file.

| Platform | App binary |
|---|---|
| Windows | `PFiles/Kstack/kstack-desktop.exe` (MSI), `%LOCALAPPDATA%\Kstack\kstack-desktop.exe` (NSIS) |
| Linux (.deb/.rpm) | `/usr/bin/kstack-desktop` |
| macOS | `Kstack.app/Contents/MacOS/kstack-desktop` |

Everything named after `productName` keeps its name: `Kstack.app`, the install folder, the
`Kstack_*` package files and `Kstack.desktop`. So do the names hard-coded to the product rather
than the binary: the data and cache leaf `Kstack`/`Kstack-dev` (`src-tauri/src/paths.rs`), the
keychain service `Kstack` (`sidecar/internal/app/app.go`) and `/usr/lib/kstack`.

`cli/internal/desktop` looks for the new paths and no longer guards against finding itself.
SignPath's `kstack-desktop` artifact configuration, which lives outside the repo, lists the MSI's
paths and names `PFiles/Kstack/kstack-desktop.exe`.

## Alternatives considered

- **Display name `Kstack Desktop`.** It would also have given the binary a distinct name, but it
  changes what users see in Applications, the Start menu and the Dock. The product is Kstack; the
  CLI exists to open the app (`kstack app`). Only the binary collided.
- **`mainBinaryName` in `tauri.conf.json`.** Tauri would rename the Cargo binary at bundle time.
  That leaves `tauri dev` running `kstack` and the bundle shipping `kstack-desktop`, two names for
  one program where renaming the `[[bin]]` gives one.

## Consequences

- The CLI owns the `kstack` name on every platform's PATH.
- The SignPath configuration must change with any later rename of the binary or the MSI.
- Tauri names the Linux icons after the binary (`usr/share/icons/hicolor/*/apps/kstack-desktop.png`)
  and the desktop entry after `productName`; its `Exec` is `kstack-desktop`.
