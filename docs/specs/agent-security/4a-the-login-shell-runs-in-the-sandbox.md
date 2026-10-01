---
title: The login shell runs in the sandbox
scope: sidecar
status: Planned
---

# The login shell runs in the sandbox

**Needs:** step 1A, whose `Policy` this step builds, and step 3A, after which `loginshell.Launch`
is the one place the login shell is run and its output shapes nothing inside the sandbox.
**Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Kstack runs the user's login shell through `loginshell.Launch` for three things:

- **The `PATH` resolution** (step 3A), once at each launch and again on Refresh PATH. It reads
  `PATH` for the sandbox.
- **The environment import**, on macOS only, from the same launch run: the allowlisted variables
  the sidecar's own process needs, since an app opened from the Dock gets almost none.
- **The shell snapshot**, on every Unix. When the sidecar starts, Bash runs the login shell once
  and saves its functions, aliases, options and `PATH`. Every command run outside the sandbox
  sources it.

Running the login shell runs the user's startup files: `~/.zshrc`, `~/.bashrc`, `config.fish` and
the like. Today all three run them with no sandbox. If a startup file has been changed to do
something harmful, it runs with full access, Kstack's own files included.

After this step, on macOS and Linux, all three run in the sandbox. The login shell:

- can read everything except Kstack's data, cache and runtime directories;
- can write nothing, except a scratch folder of its own, deleted when the shell exits;
- has no network and no cluster.

So while Kstack runs a startup file, it cannot read `app.db`, reach the sidecar's socket, or send
anything over the network. Kstack reads the shell's output the same way it does today.

If the machine has no sandbox, all three run as they do today, and the sidecar logs that once.

## What is not in this step

- **What the startup files output still matters outside the sandbox.** The snapshot's functions,
  aliases and `PATH` shape every command run outside the sandbox (step 1B's switch), and the
  imported variables shape the sidecar's own `exec` credential plugins, which run with no
  sandbox. A startup file that puts a folder first on `PATH` still decides which `aws` a plugin
  runs. Sandboxed commands are not affected: they source no snapshot (step 2A) and take their
  `PATH` from the frozen list (step 3A). What protects a startup file from a sandboxed command is
  the write policy: it writes the workspace and, from step 4D, the folders the user granted, and
  nothing on `PATH`.
- **No change to what is saved, imported or resolved.** The snapshot's contents, the allowlist and
  step 3A's filter stay the same.
- **No change on Windows.** Git Bash's snapshot runs as today.

## Design

### 1. The login shell's policy

```go
p := sandbox.Policy{
	Files: sandbox.FilePolicy{Read: []string{"/"}},
	Always: sandbox.AlwaysPolicy{
		Kstack: kstackDirs, // data, cache and runtime
		Write:  []string{scratch},
	},
	// Network is left zero: no relays, no network.
}
```

**The Always part closes Kstack's three directories alone**; its Deny, which holds
[the note](../../notes/sandbox-credentials-and-permissions.md)'s denied-always list (`Never`) in a
Workspace run, is empty. A
startup file is the user's own program reading the user's own home: it reads `~/.zsh_history` to set options,
`~/.ssh/config` for a completion, `~/.kube/config` to draw a prompt. Hiding those would change
what the shell builds, and what it builds is what Kstack reads. What the shell must not reach is
Kstack's files and the network, and the policy closes both.

`scratch` is a folder under `<cache>/tmp/`, made for this one run of the shell, 0700 through
`MkdirTemp` as a run's `TMPDIR` is. It is the shell's `TMPDIR`, and the one path the Always part
opens: it sits inside the cache directory, so `Check` accepts it, and nothing sits beneath it. The
policy names no relay, so on Linux the filter refuses Unix sockets, as it does for a Workspace run.

Everything else about the run is the same as a Workspace run:

- On Linux: its own network and process namespaces, and `sandbox-shell`'s seccomp filter.
- On macOS: the blocked Mach services, and no network rules.
- The forwarder runs but relays nothing, as in the sandbox probe.

`HOME` stays the user's home, because startup files use it. The environment is step 3A's
scrubbed one, with `TMPDIR` the scratch folder; it holds none of step 2A's never-list, so the
compilers accept it.

### 2. `loginshell.Launch` takes a start function

Today `Launch` starts the shell with `exec.Command`. It gains a parameter that builds the command
instead:

```go
start func(name string, args, env []string) (cmd *exec.Cmd, cleanup func(), err error)
```

`Launch` sets up the session, the pipes and the kill on the command `start` returns, as it does
today, and calls `cleanup` once the shell has exited. An error from `start` is a `Fault` with a
new fixed reason, `no sandbox`, so the log names it and never a value.

`loginshell.In(sb, kstackDirs, scratchRoot)` returns a start function. It is the one place that
builds the policy above:

- it makes the scratch folder under `scratchRoot`;
- it builds the `sandbox.Run` — the shell, its arguments, the user's home as `Dir`, the scrubbed
  `env` with `TMPDIR` set to the scratch, and the policy — and asks `sb.Command` for the command,
  which answers an error for a policy it cannot enforce;
- its `cleanup` deletes the scratch folder through `rootdir.RemoveAll`.

When `sb` is nil (no sandbox), it returns plain `exec.Command` with the scrubbed `env` and a
`cleanup` that does nothing, and the caller logs once that the shell ran unconfined.

`Resolve` and `Path` (step 3A) take the start function too, so every caller of the package passes
one.

### 3. Probe the sandbox once

Today the app probes the sandbox when it builds the Bash tool (`newShell` in `app/app.go`). The
launch resolution runs earlier, in `main`, before the app exists. So:

- `main` probes the sandbox once, before the resolution, and passes it to the app in
  `app.Config.Sandbox`.
- The app uses that sandbox. When the config has none, the app probes as it does today, so tests
  that build the app without `main` do not change.
- `main`'s resolution, the refresh (`securityconfig.Service`'s resolver, built by `app`) and the
  snapshot (`tools/bash/snapshot_unix.go`) each pass `loginshell.In(...)`. `main` gets Kstack's
  three directories from its config; the app and Bash already have them, and `<cache>/tmp/` is
  `bash.Paths.TmpDir`.

### 4. What startup files can no longer do

Inside the sandbox:

- Writes to the home fail. For example, zsh cannot refresh `~/.zcompdump`, and this shell cannot
  save its history.
- Network calls fail right away. For example, an oh-my-zsh update check, or a `curl` in a startup
  file, gets nothing.
- On macOS, the Keychain is blocked.
- Reads under Kstack's directories fail, `app.db` and the socket included.

Each failure prints an error, and `Launch` already throws away the shell's error output. The
resolution, the import and the snapshot only read what the shell has built, so they come out the
same. The exception is a startup file that needs something blocked in order to define a function
or set a variable.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Launch` takes a start function; `loginshell.In` and the policy | `loginshell/`, its tests | — | Planned |
| 2 | `main` probes once; the app takes the sandbox from its config | `sidecar/main.go`, `sidecar/config.go`, `app/app.go`, their tests | — | Planned |
| 3 | The launch resolution and the refresh run in the sandbox | `sidecar/main.go`, `sidecar/main_darwin.go`, `app/app.go`, their tests | 1, 2 | Planned |
| 4 | The snapshot runs in the sandbox | `tools/bash/snapshot_unix.go`, `tools/bash/bash.go`, its tests | 1, 2 | Planned |
| 5 | Docs, per *When it lands* | see there | 1–4 | Planned |

**Order:** 1 and 2 at the same time, then 3 and 4 at the same time, then 5.

## Tests

**`loginshell`** (`loginshell_unix_test.go`). Each runs through the real sandbox, with a fake home
whose startup file tries something:

- `TestTheLoginShellCannotReadKstacksDirectories`: the startup file reads a file from each of
  Kstack's directories while defining a function. The saved function holds none of it.
- `TestTheLoginShellReadsTheHome`: the startup file reads a file named like a history file
  under the home while setting `PATH`. The resolved `PATH` holds what it read, since the `Never`
  list is not this policy's.
- `TestTheLoginShellCannotWriteTheHome`: the startup file writes a file in the home. The file is
  not there afterwards.
- `TestTheLoginShellWritesItsScratch`: a file the startup file writes under `$TMPDIR` lands, and
  `TestTheLoginShellsScratchIsGone` finds the folder gone once the shell has exited.
- `TestTheLoginShellHasNoNetwork`: a listener on loopback, outside the sandbox, gets no
  connection.
- `TestAPolicyTheSandboxRefusesIsAFault`: a `sb.Command` that answers an error yields the `no
  sandbox` reason and starts nothing.
- `TestWithoutASandboxTheShellRunsAsBefore`: `In(nil, …)` returns plain `exec.Command` with the
  scrubbed environment.

**`bash`**

- `TestTheSnapshotIsTakenInTheSandbox`: over a fake sandbox, the snapshot's shell reaches
  `Command` with the policy in §1 and `TMPDIR` under `Paths.TmpDir`.
- Every existing snapshot test passes, with what it checks unchanged.

**`main` and `app`**

- `TestTheLaunchResolutionRunsInTheSandbox` (`main_unix_test.go`): the shell `Resolve` spawns
  reaches the fake sandbox's `Command`.
- `TestTheRefreshRunsInTheSandbox` (`app_unix_test.go`).
- `TestTheSandboxIsProbedOnce`.
- `TestTheAppProbesWhenItIsGivenNoSandbox`.

## Security

Kstack no longer runs the user's startup files with full access. While it runs them, they cannot
read Kstack's files, reach its socket, or use the network.

Two gaps remain, and the security record says so:

- What the startup files output still shapes what runs outside the sandbox, through the
  snapshot's `PATH`, functions and aliases, and the imported variables.
- The sidecar's `exec` credential plugins still run with no sandbox.

The login shell reads the whole home on purpose, the denied-always list included; the record
argues that too, since it is the one run Kstack starts that reads `~/.ssh` and `~/.kube`. What
bounds it is that the shell writes nothing, reaches nothing, and that Kstack reads one framed
answer from it: the resolution takes `PATH` and the allowlist, the snapshot takes the profile, and
nothing else the shell printed goes anywhere.

The record, `docs/security/<date>-the-login-shell-in-the-sandbox.md`, argues all three, and names
step 1B's switch as the one way a command can change a startup file.

## When it lands

- **The security record** above.
- **`security-model.md`**: the rows for the snapshot and the shell import say the shell runs in
  the sandbox, and list the new tests. How the output shapes what runs outside stays a
  **By decision** row.
- **`sidecar/CLAUDE.md`**: the login shell resolution's paragraph and the snapshot's; `main`
  probes the sandbox; `loginshell.In` and the policy.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands).

By hand, run `pnpm tauri dev` on macOS and on Linux, with a real profile (oh-my-zsh, nvm,
Homebrew's `shellenv`), and check that:

- the log shows the resolution and the snapshot taken, each within its time limit;
- Settings shows the same `PATH` entries as before, and a command outside the sandbox finds
  `type <a function from the profile>`;
- a startup file with this line:

  ```sh
  eval "leak() { echo '$(head -c 16 <data dir>/app.db | base64)'; }"
  ```

  saves a `leak` function that prints nothing. The line reads `app.db` while defining the
  function, so an empty function means the read was blocked.
