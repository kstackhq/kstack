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
- **The shell snapshot**, on every Unix. Bash runs the login shell once and saves its functions,
  aliases, options and `PATH`, which every command run outside the sandbox sources. On a machine
  with no sandbox it is taken when the sidecar starts; on one with a sandbox, on the first run
  outside it (step 2A).

Running the login shell runs the user's startup files: `~/.zshrc`, `~/.bashrc`, `config.fish` and
the like. Today all three run them with no sandbox. If a startup file has been changed to do
something harmful, it runs with full access, Kstack's own files included.

After this step, on macOS and Linux, all three run in the sandbox. The login shell:

- can read everything except Kstack's data, cache and runtime directories;
- can write a scratch folder of its own, deleted when the shell exits, and on Linux a private
  `/tmp` that goes with the run; nothing else;
- has no network and no cluster.

So while Kstack runs a startup file, it cannot read `app.db`, reach the sidecar's socket, or send
anything over the network. Kstack reads the shell's output the same way it does today.

If the machine has no sandbox, all three run as they do today, and the sidecar logs that each
resolution and the snapshot ran unconfined.

## What is not in this step

- **What the startup files output still matters outside the sandbox.** The snapshot's functions,
  aliases and `PATH` shape every command run outside the sandbox (step 1B's switch), and the
  imported variables shape the sidecar's own `exec` credential plugins, which run with no
  sandbox. A startup file that puts a folder first on `PATH` still decides which `aws` a plugin
  runs. Sandboxed commands are not affected: they neither wait for nor source the snapshot, and
  they take their `PATH` from the frozen list (step 3A). What protects a startup file from a
  sandboxed command is the write policy: it writes the workspace and, from step 4D, the folders
  the user granted, and nothing on `PATH`.
- **No change to what is imported or resolved.** The allowlist and step 3A's filter stay the
  same. What the snapshot saves changes in one case only, §1's: a startup file that reads a
  variable the sandbox refuses.
- **No change on Windows.** Git Bash's snapshot runs as today, and `main_windows.go` resolves
  nothing.

## Design

### 1. The login shell's policy

```go
p := sandbox.Policy{
	Files: sandbox.FilePolicy{Read: []string{"/"}},
	Always: sandbox.AlwaysPolicy{
		Kstack: kstackDirs, // data, cache and runtime
		Write:  []string{scratch},
	},
	// Network is left zero: no relays, no network. Limits is left zero: the
	// platform's defaults, with Launch's own time limit bounding the run.
}
```

**The Always part closes Kstack's three directories alone**; its `Deny`, which holds
[the note](../../notes/sandbox-credentials-and-permissions.md)'s denied-always list (`Never`) in a
Workspace run, is empty. A startup file is the user's own program reading the user's own home:
it reads `~/.zsh_history` to set options, `~/.ssh/config` for a completion, `~/.kube/config` to
draw a prompt. Hiding those would change what the shell builds, and what it builds is what
Kstack reads. What the shell must not reach is Kstack's files and the network, and the policy
closes both.

`scratch` is a folder made for this one run of the shell under `<cache>/login-shell/` (§2), 0700.
It is the shell's `TMPDIR`, and the one path the Always part opens: it sits inside the cache
directory, so `Check` accepts it, and nothing sits beneath it. It is not under `<cache>/tmp/`,
whose folders Bash's sweep owns.

Everything else about the run is the same as a Workspace run:

- On Linux: its own network and process namespaces, and `sandbox-shell`'s seccomp filter, which
  refuses Unix sockets on every run whatever the policy. `/tmp` is the run's own tmpfs, as in
  every run.
- On macOS: the blocked Mach services, `setsid` and `setpgid` refused, and no network rule, since
  the profile adds one only for a relay.
- The forwarder runs but relays nothing, as in the sandbox probe.

`HOME` stays the user's home, because startup files use it.

**The environment.** The start function (§2) takes the environment its caller passes —
`scrubbedEnv` for the resolution, `ProcessEnv` for the snapshot, as today — and changes it in two
ways before it builds the run:

- every variable `sandbox.Unpassable` names is dropped, since `Run.check` refuses a run that
  carries one. `scrubbedEnv` copies `SSH_AUTH_SOCK`, and the process environment can hold
  `AWS_*`, `SSH_AUTH_SOCK` or `LD_LIBRARY_PATH`. `scrubbedEnv` copies the agent socket so a
  startup file does not start an agent that leaves the shell's session and outlives the kill;
  inside the sandbox an agent cannot do either — Linux refuses its socket and the process
  namespace ends with the run, and macOS refuses `setsid` — so dropping it is safe here.
- `TMPDIR` is replaced by the scratch folder, never appended beside one already there.

So what the snapshot saves changes only for a startup file that reads one of the dropped names.
Its `PATH` still starts from the process's, so what a command outside the sandbox finds does not
change. A command outside the sandbox runs with the process environment as today; only the
shell that builds the snapshot goes without those names.

### 2. `loginshell.Launch` takes a start function

Today `Launch` starts the shell with `exec.Command`. It gains a parameter that builds the command
instead:

```go
// Start builds the command that runs the login shell, and a cleanup Launch
// calls once the shell has exited.
type Start func(ctx context.Context, name string, args, env []string) (cmd *exec.Cmd, cleanup func(), err error)
```

**The command's environment and directory are `Start`'s.** `Launch` no longer sets `cmd.Env` or
`cmd.Dir`: the sandbox builds its command with the run's environment, and setting it again
would put back what §1 dropped. `Launch` sets up the session, the pipes and the kill on the
command `start` returns, as it does today, and calls `cleanup` once the shell has exited. An error from `start` is a `Fault` with a
new fixed reason, `no sandbox`, so the log names it and never a value. The caller fails closed:
the resolution and the snapshot fail as they do on any `Fault` today, and the shell never runs
unconfined on a machine whose sandbox answered the probe.

`loginshell.In(sb, kstackDirs, scratchRoot)` returns a `Start`. It is the one place that builds
the policy above:

- it refuses a relative directory in `kstackDirs` or `scratchRoot` (an error, so `no sandbox`);
- it makes `scratchRoot` with `MkdirAll` 0700, since `main` runs before `app.New` makes the
  cache directory;
- it removes every folder in `scratchRoot` named for a process that is no longer running, so a
  crash leaks nothing for long;
- it makes the scratch folder with `MkdirTemp(scratchRoot, "<pid>-*")`, `<pid>` the sidecar's
  own;
- it builds the `sandbox.Run` — the shell, its arguments, the user's home as `Dir`, the
  environment as §1 says, and the policy — and asks `sb.Command(ctx, run)` for the command, which
  answers an error for a policy it cannot enforce;
- its `cleanup` deletes the scratch folder through `rootdir.RemoveAll`.

When `sb` is nil (no sandbox), it returns `exec.CommandContext` with the environment as given,
the home as `Dir`, and a `cleanup` that does nothing. `main` logs once that the launch resolution ran unconfined; the
snapshot logs the same once when it is taken. On a machine with no sandbox there is no Refresh
PATH (the resolver is nil, as today), so there is no third caller.

`Resolve` and `Path` (step 3A) take the `Start` too, so every caller of the package passes one.

### 3. Probe the sandbox once

Today the app probes the sandbox when it builds the Bash tool (`newBashTool` in `app/app.go`).
The launch resolution runs earlier, in `main` (`launchShell`), before the app exists. So:

- `main` probes the sandbox once, before the resolution, and hands the answer to the app:

  ```go
  // In app.Config. Nil when nothing probed; the app probes then.
  Sandbox *SandboxProbe

  // SandboxProbe is one answer of sandbox.Probe: the sandbox, nil on a machine
  // with none, and the status that says why.
  type SandboxProbe struct {
  	Sandbox *sandbox.Sandbox
  	Status  sandbox.Status
  }
  ```

  A nil sandbox inside a non-nil `SandboxProbe` is "probed, none here", so the app does not probe
  again. The app takes both: `newBashTool` takes the sandbox, and the `sandbox` query and the
  security service take the status, as they do today.
- When `cfg.Sandbox` is nil, the app probes as it does today, so tests that build the app without
  `main` do not change.
- `main`'s resolution, the refresh (`securityconfig.Service`'s resolver, built by `app`) and the
  snapshot (`tools/bash/snapshot_unix.go`) each pass `loginshell.In(...)`. `main` reads Kstack's
  three directories from `cfg.App`, which requires them absolute; the app and Bash already have
  them. The scratch root is `<cache>/login-shell` for all three.

The probe adds up to its bound (`probeBound` on Linux) before the sidecar reports ready, as the
app's probe does today; it now runs once, earlier.

### 4. What startup files can no longer do

Inside the sandbox:

- Writes to the home fail. For example, zsh cannot refresh `~/.zcompdump`, and this shell cannot
  save its history.
- Network calls fail right away. For example, an oh-my-zsh update check, or a `curl` in a startup
  file, gets nothing.
- On macOS, the Keychain is blocked, and so are `setsid` and `setpgid`: a startup file that starts
  a daemon of its own fails to.
- Reads under Kstack's directories fail, `app.db` and the socket included.
- A tool that keeps a cache under `TMPDIR` (Xcode's `xcrun`, a git shim) starts cold in the
  scratch, and is slower for it.

Each failure prints an error, and `Launch` already throws away the shell's error output. The
resolution, the import and the snapshot only read what the shell has built, so they come out the
same. The exception is a startup file that needs something blocked in order to define a function
or set a variable.

## Decisions this step asks for

1. **The login shell reads the whole home, the denied-always list included.** Its output is what
   Kstack reads, and hiding `~/.ssh/config` or `~/.kube/config` changes it. Recommended: it
   writes nothing outside its scratch and reaches nothing, and Kstack reads one framed answer
   from it.
2. **A start the sandbox refuses fails the resolution rather than running the shell
   unconfined.** Recommended: on a machine whose sandbox answered the probe, a refused policy is
   a bug, and the fallback `PATH` is the safe answer.
3. **Never-list variables are dropped from the shell's environment, `SSH_AUTH_SOCK` included.**
   Recommended: the sandbox refuses them, and the reason `scrubbedEnv` copies the agent socket
   does not hold inside the sandbox (§1).

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Launch` takes a `Start`; `loginshell.In`, the environment and the policy; the scratch root | `loginshell/`, its tests | — | Planned |
| 2 | `main` probes once; `app.Config.Sandbox`; the app takes the sandbox and its status from it | `sidecar/main.go`, `sidecar/main_unix.go`, `sidecar/config.go`, `app/app.go`, their tests | — | Planned |
| 3 | The launch resolution and the refresh run in the sandbox | `sidecar/main_unix.go`, `sidecar/main_darwin.go`, `app/app.go`, `app/shellpath_unix.go`, their tests | 1, 2 | Planned |
| 4 | The snapshot runs in the sandbox | `tools/bash/snapshot_unix.go`, `tools/bash/bash.go`, its tests | 1, 2 | Planned |
| 5 | Docs, per *When it lands* | see there | 1–4 | Planned |

**Order:** 1 and 2 at the same time, then 3 and 4 at the same time, then 5.

## Tests

**`loginshell`** (`loginshell_unix_test.go`). Each runs through the real sandbox, and skips where
the probe finds none. The file's `TestMain` calls `sandbox.Main`, since the forwarder re-runs the
test binary. Each sets `HOME` to a fake home whose startup file tries something, and reads the
answer through `Path`:

- `TestTheLoginShellCannotReadKstacksDirectories`: the startup file appends to `PATH` a folder
  named after the contents of a file in each of Kstack's directories. The resolved `PATH` holds
  none of it.
- `TestTheLoginShellReadsTheHome`: the same, for a file named like a history file under the home.
  The resolved `PATH` holds what it read, since the `Never` list is not this policy's.
- `TestTheLoginShellCannotWriteTheHome`: the startup file writes a file in the home. The file is
  not there afterwards.
- `TestTheLoginShellWritesItsScratch`: a file the startup file writes under `$TMPDIR` lands, and
  `TestTheLoginShellsScratchIsGone` finds the folder gone once the shell has exited.
- `TestALeftoverScratchIsRemoved`: a folder in the scratch root named for a pid no process holds
  is gone after the next `In`, and one named for the test's own pid is kept.
- `TestTheLoginShellHasNoNetwork`: a listener on loopback, outside the sandbox, gets no
  connection.
- `TestANeverListVariableIsDropped`: with `SSH_AUTH_SOCK` and `AWS_PROFILE` in the environment
  passed, the shell starts, and neither is set inside it.
- `TestAPolicyTheSandboxRefusesIsAFault`: a `Command` that answers an error yields the `no
  sandbox` reason and starts nothing.
- `TestWithoutASandboxTheShellRunsAsBefore`: `In(nil, …)` returns a plain command with the
  environment as given.
- `TestLaunchKeepsTheStartsEnvironment`: `Launch` leaves the command's `Env` and `Dir` as `start`
  set them.

**`bash`**

- `TestTheSnapshotIsTakenInTheSandbox`: over a fake sandbox, the snapshot's shell reaches
  `Command` with the policy in §1, `TMPDIR` under `<cache>/login-shell`, and no never-list name.
- Every existing snapshot test passes, with what it checks unchanged.

**`main` and `app`**

- `TestTheLaunchResolutionRunsInTheSandbox` (`main_unix_test.go`): the shell `Resolve` spawns
  reaches the fake sandbox's `Command`.
- `TestTheRefreshRunsInTheSandbox` (`app_unix_test.go`).
- `TestTheSandboxIsProbedOnce`: given a `SandboxProbe`, the app probes nothing, including one
  whose sandbox is nil.
- `TestTheAppProbesWhenItIsGivenNoSandbox`: given nil, the app probes once.

## Security

Kstack no longer runs the user's startup files with full access. While it runs them, they cannot
read Kstack's files, reach its socket, or use the network.

Two gaps remain, and the security record says so:

- What the startup files output still shapes what runs outside the sandbox, through the
  snapshot's `PATH`, functions and aliases, and the imported variables.
- The sidecar's `exec` credential plugins still run with no sandbox.

The login shell reads the whole home on purpose, the denied-always list included; the record
argues that too, since it is the one run Kstack starts that reads `~/.ssh` and `~/.kube`. What
bounds it is that the shell writes nothing that outlives it, reaches nothing, and that Kstack
reads one framed answer from it: the resolution takes `PATH` and the allowlist, the snapshot
takes the profile, and nothing else the shell printed goes anywhere.

The record, `docs/security/<date>-the-login-shell-in-the-sandbox.md`, argues all three, and names
step 1B's switch as the one way a command can change a startup file.

## When it lands

- **The security record** above.
- **`security-model.md`**: the rows for the snapshot and the shell import say the shell runs in
  the sandbox, and list the new tests. How the output shapes what runs outside stays a
  **By decision** row.
- **`sidecar/CLAUDE.md`**: the login shell resolution's paragraph and the snapshot's; `main`
  probes the sandbox and hands it on in `app.Config.Sandbox`; `loginshell.In`, the environment,
  the policy and the scratch root.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands).

By hand, run `pnpm tauri dev` on macOS and on Linux, with a real profile (oh-my-zsh, nvm,
Homebrew's `shellenv`) and an SSH agent running, and check that:

- the log shows the resolution taken within its time limit, and the snapshot taken on the first
  command outside the sandbox;
- Settings shows the same `PATH` entries as before, and a command outside the sandbox finds
  `type <a function from the profile>`;
- a startup file with this line:

  ```sh
  eval "leak() { echo '$(head -c 16 <data dir>/app.db | base64)'; }"
  ```

  saves a `leak` function that prints nothing. The line reads `app.db` while defining the
  function, so an empty function means the read was blocked.
