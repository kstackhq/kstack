---
title: Each folder kind has its own folder on every platform
date: 2026-11-22
scope: host
status: Accepted
amends:
  - [Keep each file in the platform's directory for its kind](2026-09-27-kstacks-files-follow-the-platforms-directory-kinds.md)
  - [Keep host.json in the Kstack config directory](2026-11-21-host-json-lives-under-the-kstack-config-directory.md)
  - [Linux folders follow XDG](2026-11-21-linux-folders-follow-xdg.md)
---

# Each folder kind has its own folder on every platform

## Context

Linux gives each of Kstack's folder kinds its own folder. macOS and Windows did not. On macOS,
`<data>`, `<config>` and `<state>` were all `~/Library/Application Support/Kstack/`, because the
platform's data and config roots are one folder and it has no state root. On Windows, `<data>` and
`<state>` were both `%LOCALAPPDATA%\Kstack\`, and `<cache>`, the logs, `<runtime>` and the webview
profile sat inside it, because the cache root is `%LOCALAPPDATA%` itself and there is no state or
runtime root.

Sharing cost three things. The sidecar's fences could not tell the host's files from its own:
`--state-dir` fenced the data folder. `paths.rs` and the docs carried per-platform cases such as
"`<data>/cache` on Windows". And on Windows the data folder held every other folder, so
`make clean-data` and the fences had to handle nesting.

`desktop/v0.0.1` used the shared folders, but it reached no users.

## Decision

Where a platform has no separate root for a kind, the kinds share one app folder, the `Kstack`
(`Kstack-dev`) leaf on the local data root, and each takes a subfolder of it. No kind is another's
folder, and none sits inside another's.

- **macOS**: `<data>` is `~/Library/Application Support/Kstack/data`, `<config>` `…/Kstack/config`
  and `<state>` `…/Kstack/state`. `<cache>` stays `~/Library/Caches/Kstack`, the logs
  `~/Library/Logs/Kstack` and `<runtime>` under `$TMPDIR`.
- **Windows**: `<data>` is `%LOCALAPPDATA%\Kstack\data`, `<cache>` `…\Kstack\cache`, `<state>`
  `…\Kstack\state` and `<runtime>` `…\Kstack\run`. The logs are `state\logs` and the webview profile
  `state\webview`, as on Linux. `<config>` is `%APPDATA%\Kstack\config`, which roams. The
  subfolder keeps it a sibling of the other kinds when `%APPDATA%` and `%LOCALAPPDATA%` are one
  folder, however each spells it, so no path is compared.
- **Linux** is unchanged.

`paths.rs` resolves every kind on every platform in one pure function, `layout`, so each
platform's paths are tested on every OS.

The app folder is made owner-only before any kind in it, since whoever can write a folder can
replace what is in it.

Nothing moves what the old folders hold.

## Alternatives considered

- **Keep the shared folders and fence by subpath.** The fences and `make clean-data` would keep
  their nesting cases, and the docs their per-platform exceptions.
- **Put macOS's state in `~/Library/Preferences` or a `~/Library/Application Support` sibling.**
  `Preferences` is for `defaults` property lists, and a second top-level `Kstack-state` folder
  departs from how macOS apps lay out their own files.
- **Use `%TEMP%` for Windows' runtime directory.** `TEMP` can point at a folder users share.

## Consequences

- On macOS and Windows a `desktop/v0.0.1` install starts empty. Its old files stay on disk until
  the user removes them.
- The cache folder keeps its path on both, and its files are named by ids the empty database hands
  out again. A machine that ran an earlier dev build runs `make clean-data` once.
- The sidecar's `--state-dir` and `--config-dir` fence folders that hold only the host's files.
- The app folder is fenced as well, through `--app-dir`, so nothing is written among the kinds.
  Windows passes `%APPDATA%\Kstack` the same way, since it holds the config.
- `make clean-data` deletes the app folder on macOS and Windows, which also removes what a build
  from before this change left in it. It deletes only the dev build's: a per-user Windows install
  puts the release's executable in `%LOCALAPPDATA%\Kstack` too, so that folder is never deleted
  whole.
