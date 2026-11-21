---
title: Linux folders follow XDG
date: 2026-11-21
scope: host
status: Accepted
amends: [Keep each file in the platform's directory for its kind](2026-09-27-kstacks-files-follow-the-platforms-directory-kinds.md)
---

# Linux folders follow XDG

## Context

Kstack named its Linux folders after the product, `~/.local/share/Kstack`, `~/.config/Kstack` and
`~/.cache/Kstack` (`Kstack-dev` for a dev build). Linux tools name theirs in lowercase. It also kept
its logs and `window-state.json` in the data folder, while XDG gives per-machine state its own root,
`$XDG_STATE_HOME` (`~/.local/state`).

`desktop/v0.0.1` used the old folders, but it reached no users.

## Decision

On Linux each folder takes a lowercase leaf, `kstack` or `kstack-dev`. macOS and Windows keep
`Kstack` and `Kstack-dev`.

Linux gains a state folder, `$XDG_STATE_HOME/kstack` (when absolute, else `~/.local/state/kstack`).
It holds the host's and the sidecar's logs and `window-state.json`. The host hands the sidecar the
state folder (`--state-dir`), which fences the whole of it. On macOS and Windows state is the data
folder, and the logs stay where they were.

Nothing moves what the old folders hold.

The WebKitGTK profile stays under the bundle id, shared by dev and release builds.

## Alternatives considered

- **Move the old folders at launch.** Moving the data folder would break the absolute paths a
  transcript names, so an upgraded install would keep it for good. With no installs to upgrade, that
  is code and a second spelling of the data folder for nobody.
- **Give each build its own WebKitGTK profile under the data folder.** It holds only disposable
  `localStorage`, and setting a profile per window is a path no test on the mock runtime reaches.

## Consequences

- A `desktop/v0.0.1` install starts empty on Linux. Its old folders stay on disk until the user
  removes them.
- `make clean-data` deletes the dev build's lowercase folders and its state folder.
- A dev build and a release still share `localStorage` on Linux.
