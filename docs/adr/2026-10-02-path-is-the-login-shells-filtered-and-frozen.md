---
title: PATH is the login shell's, filtered and frozen
date: 2026-10-02
scope: sidecar, webview
status: Accepted
---

# PATH is the login shell's, filtered and frozen

## Context

A sandboxed command's `PATH` was the sidecar's own: on macOS the login shell's, read from
`$SHELL` at launch, and on Linux the process's. A startup file widened what every sandboxed
command found, unseen, and a terminal launch and a Finder launch found different tools. The
zones open no `PATH` entry, so an entry no list covered found nothing.

## Decision

- **The account's login shell, once per launch, in a scrubbed environment.** One run answers both
  readers: `PATH` for the sandbox, and on macOS the allowlist the process installs. Nushell is
  asked in its own language; any other shell runs the posix command.
- **Filtered.** Empty, relative, missing, world-writable and `node_modules` entries go, and so does
  anything under the denied-always list or Kstack's directories, and `/`, the home, an app-data
  folder under it or a folder holding one: opening that much is a grant, not a `PATH` entry.
- **Frozen in `security.json`.** An entry is adopted unasked only when it is already open to every
  run and not writable by a non-admin group; every other new entry waits for the user. A removal
  is kept as `gone`. Each entry keeps the folder it resolved to, and its state holds for that
  folder.
- **Frozen per run.** A run reads the list at its start and searches the folders that pass a
  second check, which are also its Read rules.
- **A group-writable folder waits, unless the group is an administrators' one.**

## Alternatives considered

- **Drop a group-writable folder**, as the note first said. Homebrew leaves its prefix writable by
  `admin`, so a default Mac would lose `kubectl` from every sandboxed run.
- **No tombstone for a removal.** The next launch would adopt an open entry back, and Remove would
  mean nothing.
- **Store the resolved folder in place of the entry.** It loses the spelling the user recognises,
  and a run still needs to check the link.
- **Freeze per session.** Only the user changes the list, through a socket no sandboxed command
  reaches, so per run gives the same guarantee and an Include applies to the chat the user is in.
- **Two runs of the shell at launch**, one per reader. It runs the startup files twice for
  nothing.

## Consequences

- A shell Kstack does not speak (xonsh, PowerShell) fails every launch, and sandboxed commands use
  the last list. Only a list never resolved falls back to the platform's default `PATH`, under
  the same checks; a list the user emptied searches nothing.
- `Held`, `ErrHeld` and the store's guard exist for every field that restricts.
- Include is the one way a folder outside the lists becomes readable.

## Revisit when

Step 4A runs the resolution in a read-only sandbox, step 4D adds grants beside the list, and step
6A probes after a refresh.
