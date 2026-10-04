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

- can read everything except Kstack's data, cache and runtime directories, and, for the `PATH`
  resolution, the denied-always list too (§1);
- can write a scratch folder of its own, deleted when the shell exits, and on Linux a private
  `/tmp` that goes with the run; nothing else;
- has no network and no cluster.

So while Kstack runs a startup file for one of these three, it cannot read `app.db`, reach the
sidecar's socket, or send anything over the network, and the run whose output leaves the
sandbox cannot read a credential to put in it. Kstack reads the shell's output the same way it
does today.

**What is confined is these three runs, not every run of a startup file, and not what their
output does later.** A command outside the sandbox is `<shell> -c` with the process
environment, so zsh still reads `/etc/zshenv` and `~/.zshenv`, and bash the file `BASH_ENV`
names when it is set, unconfined, on every such command. A startup file can also define a
function the snapshot saves, which runs unconfined in the next command outside the sandbox, and
on macOS set an imported variable or put a folder first on `PATH` for the credential plugins.
This step closes the harm a startup file does while Kstack runs the login shell — a compromised
plugin manager's update check, a line that reads `app.db` and posts it — and leaves the rest as
the residuals the Security section names.

If the machine has no sandbox, the snapshot and, on macOS, the launch resolution run as they do
today, and the sidecar logs once for each that it ran unconfined. On Linux with no sandbox, the
launch resolution is skipped: nothing reads its answer there.

## What is not in this step

- **What the startup files output still matters outside the sandbox.** The snapshot's functions,
  aliases and `PATH` shape every command run outside the sandbox (step 1B's switch), and the
  imported variables shape the sidecar's own `exec` credential plugins, which run with no
  sandbox. A startup file that puts a folder first on `PATH` still decides which `aws` a plugin
  runs. Sandboxed commands are not affected: they neither wait for nor source the snapshot, and
  they take their `PATH` from the frozen list (step 3A).
- **A command outside the sandbox still runs the files every `-c` shell reads** —
  `/etc/zshenv`, `~/.zshenv`, `BASH_ENV` — unconfined. That command asks, and what it runs is
  the user's shell as the user set it up.
- **What keeps a sandboxed command from changing a startup file is the write policy**, not this
  step. A sandboxed command writes the workspace and, from step 4D, the folders the user granted
  read-write. Step 4D refuses a read-write grant on, under or over a known startup file or its
  resolved target (its `NoWrite` list); a file a startup file sources from anywhere else, under a
  granted folder, is step 4D's residual.
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
		Deny:   deny,       // the denied-always list for the resolution; nil for the snapshot
		Kstack: kstackDirs, // data, cache and runtime
		Write:  []string{scratch},
	},
	// Network is left zero: no relays, no trust daemon, no network. Limits is
	// left zero: the platform's defaults, with Launch's own time limit bounding
	// the run.
}
```

**What the shell may read follows where its output goes.** Both runs close Kstack's three
directories and the network. They differ in `Deny`, which holds
[the note](../../notes/sandbox-credentials-and-permissions.md)'s denied-always list (`Never`) in a
Workspace run:

- **The resolution keeps the list shut**: its `Deny` is `sb.Never(home)`, as a Workspace run's.
  Its output leaves the sandbox. The `PATH` is stored in `security.json` and searched by every
  sandboxed run. On macOS the imported variables are set in the sidecar's own process, where
  `net/http` reads `HTTPS_PROXY` and the credential plugins and the Ollama client dial what
  `HTTPS_PROXY` and `OLLAMA_HOST` name. A startup file that could read `~/.ssh/id_ed25519` could
  write it into a proxy URL's user part or a host name, and the sidecar would send it from
  outside the sandbox. With the list shut there is no credential of the list's to send.
- **The snapshot reads the whole home**: its `Deny` is empty. Its output reaches only commands
  run outside the sandbox, which read the whole home themselves, so hiding a file from the shell
  that builds it protects nothing. Hiding it would change what the shell builds: a startup file
  reads `~/.ssh/config` for a completion and `~/.kube/config` to draw a prompt.

A startup file that reads a file on the list during the resolution gets nothing and goes on, as
§4 says of every refusal. The snapshot is the one run Kstack starts with an empty `Never`, and
the note's denied-always paragraph and the denied-always row of `security-model.md` say so
(Decisions, 1).

`scratch` is a folder made for this one run of the shell under `<cache>/login-shell/` (§2), 0700.
It is the shell's `TMPDIR`, and the one path the Always part opens: it sits inside the cache
directory, so `Check` accepts it, and nothing sits beneath it. It is not under `<cache>/tmp/`,
whose folders Bash's sweep owns. It starts with what `sandbox.SeedTmpDir` copies, as a bash
run's `TMPDIR` does, so `xcrun` finds its lookup cache.

**The trust daemon (seam with step 4C).** Step 4C adds `sandbox.NetworkPolicy.Internet`, which
on macOS also allows `com.apple.trustd.agent` and the DNS service, and which a bash run sets only
when it has network. The login shell's policy leaves it false, so the login shell keeps no
network and no trust daemon. If this step lands first, there is nothing to do but the test
(`TestTheLoginShellLooksUpNoTrustDaemon`), which holds either way. If step 4C lands first, the
policy above leaves `Internet` false and the test pins it.

Everything else about the run is the same as a Workspace run:

- On Linux: its own network and process namespaces, and `sandbox-shell`'s seccomp filter, which
  refuses Unix sockets on every run whatever the policy. `/tmp` is the run's own tmpfs, as in
  every run.
- On macOS: the blocked Mach services, the `setsid` and `setpgid` syscalls refused, and no
  network rule, since the profile adds one only for a relay. `posix_spawn` can still start a
  process in a new session; it stays confined (§2 says what that means for the kill).
- The forwarder runs but relays nothing, as in the sandbox probe.

`HOME` stays the user's home, because startup files use it.

**The environment.** The start function (§2) takes the environment its caller passes —
`scrubbedEnv` for the resolution, `ProcessEnv` for the snapshot, as today — and changes it in two
ways before it builds the run:

- every variable `sandbox.Unpassable` names is dropped, since `Run.check` refuses a run that
  carries one. `scrubbedEnv` copies `SSH_AUTH_SOCK`, and the process environment can hold
  `AWS_*`, `SSH_AUTH_SOCK`, `ENV`, `BASH_ENV` or `LD_LIBRARY_PATH`. `scrubbedEnv` copies the
  agent socket so a startup file does not start an agent that leaves the shell's session and
  outlives the kill. Inside the sandbox such an agent fails: on Linux the seccomp filter refuses
  its socket and the process namespace ends with the run, and on macOS the profile refuses the
  `setsid` it calls to leave the session. So dropping it is safe here.
- `TMPDIR` is replaced by the scratch folder, never appended beside one already there.

So what the snapshot saves changes only for a startup file that reads one of the dropped names.
Its `PATH` still starts from the process's, so what a command outside the sandbox finds does not
change. A command outside the sandbox runs with the process environment as today; only the
shell that builds the snapshot goes without those names.

### 2. `loginshell.Launch` takes a start function

Today `Launch` starts the shell with `exec.Command`. It gains a parameter that builds the command
instead. The type sits beside `Launch` in `loginshell/loginshell.go`:

```go
// Start builds the command that runs the login shell, and a cleanup Launch
// calls once the shell has exited. The command's Env is never nil, and its
// SysProcAttr is unset: Launch sets it.
type Start func(ctx context.Context, name string, args, env []string) (cmd *exec.Cmd, cleanup func(), err error)
```

**The command's environment and directory are `Start`'s.** `Launch` no longer sets `cmd.Env` or
`cmd.Dir`: the sandbox builds its command with the run's environment, and setting it again
would put back what §1 dropped. `Launch` sets up the pipes and the kill on the command `start`
returns, as it does today, and calls `cleanup` once the shell has exited.

**`SysProcAttr` stays `Launch`'s.** `Launch` sets `Setsid` on the returned command, as it does
today. This relies on `sandbox.Sandbox.Command` leaving `SysProcAttr` unset, which both
platforms do and document; the `Start` doc comment states the same contract, and
`TestLaunchSetsTheSession` pins it over the real sandbox. What the kill then reaches differs by
platform:

- **Linux.** The session is bwrap's. bwrap runs with `--new-session` and `--unshare-pid`, so
  `Kill(-pgid)` reaches bwrap alone. The rest dies behind it: `--die-with-parent` kills the
  run's first process, the PID namespace's init (`--as-pid-1`), and the kernel kills every
  process left in the namespace when its init dies. Nothing the shell started outlives the kill.
- **macOS.** The session is the run's, from `sandbox-exec` down, so the kill reaches the shell
  and everything still in its session. A process a startup file starts into a new session
  through `posix_spawn` is not in it, and can outlive the kill. It stays confined: it reads what
  the shell reads, writes nothing but the scratch, and reaches no network. Once `cleanup` has
  removed the scratch it writes nothing at all, since `<cache>/login-shell/` is not writable to
  it; a scratch it keeps from being removed is swept once this sidecar is gone.

**A sandboxed command is made with `exec.CommandContext`**, so a cancel also kills its first
process; `Launch`'s group kill follows, as today.

An error from `start` while `ctx` has ended is the `timeout` fault, whatever the error: Darwin's
`Command` answers `ctx.Err()` when the deadline passes while it builds the profile, and that is
a shell that ran out of time, not a refused policy. Any other error from `start` is a `Fault`,
so the log names it and never a value, with one of two new fixed reasons: `no scratch` when the
scratch root or folder could not be made, opened or locked, and `sandbox refused` when the
sandbox refused the run. `In`'s `Start` wraps the first in an
unexported sentinel `Launch` tells apart with `errors.Is`. The caller fails closed: the
resolution and the snapshot fail as they do on any `Fault` today, and the shell never runs
unconfined on a machine whose sandbox answered the probe.

**`loginshell.Commander`** is what `In` runs a shell through. It and `In` live in
`loginshell/sandboxed.go`, which, like the rest of the package, builds on Unix alone:

```go
// Commander builds a sandboxed command: *sandbox.Sandbox, or a test's fake.
type Commander interface {
	Command(ctx context.Context, r sandbox.Run) (*exec.Cmd, error)
}
```

`loginshell.In(sb Commander, deny, kstackDirs []string, scratchRoot string) (Start, error)`
returns a `Start`. It is the one place that builds the policy above. `deny` is the policy's
`Always.Deny`: the resolution's callers pass `sb.Never(home)`, the snapshot passes nil (§1).
**A nil `*sandbox.Sandbox` is not a nil `Commander`**: every caller converts with the same guard `bash.New` uses (`if sb != nil`)
before calling `In`, and passes a nil interface for no sandbox. `In` checks, once:

- that every path in `deny` and `kstackDirs`, and `scratchRoot`, is absolute; an error
  otherwise, so the caller logs it and runs no shell.

Each call of the `Start` it returns, in order:

1. makes `scratchRoot` with `MkdirAll` 0700, since `main` runs before `app.New` makes the cache
   directory, and opens it as an `os.Root`;
2. sweeps it: removes every folder whose sidecar no longer holds its lock, through the lock
   files the bash run directories use (below), so two sidecars sharing a cache directory never
   remove each other's live folders;
3. takes this sidecar's lock in `scratchRoot`, makes the scratch folder with
   `MkdirTemp(scratchRoot, "<pid>-*")`, `<pid>` the sidecar's own, and seeds it with
   `sandbox.SeedTmpDir`;
4. builds the `sandbox.Run` — the shell, its arguments, the user's home as `Dir`, the
   environment as §1 says, and the policy — and asks `sb.Command(ctx, run)` for the command,
   which answers an error for a policy it cannot enforce;
5. returns a `cleanup` that deletes the scratch folder through `rootdir.RemoveAll(root, name)`
   on the opened root, then closes the root.

A failure in steps 1 or 3 is `no scratch`; the sweep logs its own failures and fails nothing, as
`sweepRunDirs` does today; a failure in step 4 is `sandbox refused`.

**The locks move to a leaf package, `internal/rundir`**, which both `loginshell` and `tools/bash`
import. From `tools/bash/rundir_unix.go` it takes `holdRunLock`, `sweepRunDirs`, `heldLocks`,
`freeLock`, `lockName`, `lockPID`, `runDirPID` and `parsePID`, exported where a caller needs them
(`rundir.Hold`, `rundir.Sweep`); from `tools/bash/rundir_windows.go`, the two stubs for
`sweepRunDirs` and `holdRunLock`. `checkPrivate` stays in `bash`. `newRunDir` in
`tools/bash/rundir.go`, which holds the lock in the runs and tmp directories, and the sweep in
`bash.New` call the moved functions. The lock and sweep tests in `tools/bash/rundir_unix_test.go`
move beside them, to `rundir/rundir_unix_test.go`.

When `sb` is nil (no sandbox), `In` returns a `Start` that is `exec.Command`, as `Launch` uses
today, so nothing but `Launch`'s group kill ends the shell, with the home as `Dir`, `Env` a copy
of the environment as given (`append([]string{}, env...)`, never nil, which exec reads as the
whole process environment, as `Launch` does today), and a `cleanup` that does nothing. The snapshot logs once, when it is taken, that it ran unconfined; on macOS `main`
logs the same for the launch resolution. On a machine with no sandbox there is no Refresh PATH
(the resolver is nil, as today), so there is no third caller.

`Resolve` and `Path` (step 3A) take the `Start` too, so every caller of the package passes one.

### 3. Probe the sandbox once

Today the app probes the sandbox when it builds the Bash tool (`newBashTool` in `app/app.go`).
The launch resolution runs earlier, in `main` (`launchShell`), before the app exists. So:

- `main` probes the sandbox once, before the resolution, and hands the answer to the app:

  ```go
  // In app.Config. Nil when nothing probed; the app probes then.
  Sandbox *SandboxProbe

  // SandboxProbe is one answer of sandbox.Probe: the sandbox, nil on a machine
  // with none, and the status Probe gave.
  type SandboxProbe struct {
  	Sandbox *sandbox.Sandbox
  	Status  sandbox.Status
  }
  ```

  A nil sandbox inside a non-nil `SandboxProbe` is "probed, none here", so the app does not probe
  again. The app takes both: `newBashTool` takes the probe in place of probing, and the status
  still goes through `sandboxStatusOf`, so a sandbox with no shell to run in it reports
  unavailable to the `sandbox` query and the security service, as today.
- **The probe's context** is `context.Background()`, as the app's is today, not the shutdown
  signal context. Each platform's probe is already bounded: on macOS by `probeTimeout`, 5
  seconds; on Linux by `probeBound`, 5 seconds per bwrap tried, and `bwrapPaths` can answer two
  (the system's and Kstack's own), so up to 10 seconds. A cancel is not a timeout: on macOS only
  `DeadlineExceeded` keeps the sandbox, and on Linux a cancelled try fails the bwrap, so a signal
  that cancelled the probe would answer "no sandbox" and the login shell would then run
  unconfined. A signal that arrives during the probe is seen by `main` right after it, as one
  arriving during the resolution is today.
- When `cfg.Sandbox` is nil, the app probes as it does today, so tests that build the app without
  `main` do not change.
- **On Linux with no sandbox, `main` skips the launch resolution.** `setShellEnv` does nothing
  off macOS, and `ShellPath` is read only while the sandbox is available, so the run would read
  nothing anyone uses. `cfg.App.ShellPath` stays empty and `ShellFault` stays empty.
- `main`'s resolution, the refresh (`securityconfig.Service`'s resolver, built by `app`) and the
  snapshot (`tools/bash/snapshot_unix.go`) each pass `loginshell.In(...)`. `main` reads Kstack's
  three directories from `cfg.App` (`DataDir`, `CacheDir`, `RuntimeDir`), which `app.New` checks
  only later, so `In`'s own check is the one that applies there. `main` and the refresh pass
  `sb.Never(home)` as `deny`; the snapshot passes nil. Step 4D adds the log directory to
  Kstack's directories (`cfg.App.LogDir`); whichever of the two lands second has `main` pass it
  too. Bash gains a field,
  `Paths.LoginShellDir`, set in `app/paths.go` to `<cache>/login-shell`, and passes
  `Paths.DeniedDirs` as the Kstack directories, which holds the data, cache and runtime
  directories already. The scratch root is `<cache>/login-shell` for all three callers.

**The seams.**

- `main.go` probes through `probeSandbox(ctx) *app.SandboxProbe`, which calls `sandbox.Probe`
  and logs the answer, as `newBashTool` does today.
- `launchShell` takes the probe's sandbox, nil for none, beside the context and `cfg.App`:
  `launchShell(ctx, sb, cfg.App)`. It reads `sb.Never(home)` for `deny` and passes the sandbox
  to `In` as a `loginshell.Commander`, converting with the `if sb != nil` guard. It builds the
  `Start` with `In` and hands it to `runShell`, whose `resolve` gains the `Start` parameter. It resolves through a
  package variable in `main_unix.go`, `resolveShell = loginshell.Resolve`. The skip is a
  per-platform function beside `setShellEnv`: `skipResolution(sb)` is `sb == nil` in
  `main_default.go` and false in `main_darwin.go`.
- `app.go` probes through a package variable, `probeSandbox = sandbox.Probe`, which the app's
  tests replace to count calls.

The probe adds up to its bound before the sidecar reports ready, as the app's probe does today;
it now runs once, earlier.

### 4. What startup files can no longer do

Inside the sandbox:

- Writes to the home fail. For example, zsh cannot refresh `~/.zcompdump`, and this shell cannot
  save its history.
- **fish and nushell** write their own state on start (`fish_variables`, `~/.config/nushell`'s
  history). fish prints an error and goes on. nushell may refuse to start at all over a
  read-only config directory; then the resolution reads `bad output` or `shell exited` and the
  fallback `PATH` applies, as on any `Fault`.
- Network calls fail right away. For example, an oh-my-zsh update check, or a `curl` in a startup
  file, gets nothing.
- On macOS, the Keychain is blocked, and so are the `setsid` and `setpgid` syscalls: a startup
  file that starts a daemon of its own the usual way fails to.
- Reads under Kstack's directories fail, `app.db` and the socket included.
- A tool other than `xcrun` that keeps a cache under `TMPDIR` (a git shim) starts cold in the
  scratch, and is slower for it.
- On macOS, a tool that ignores `TMPDIR` and asks `confstr` for the user's temporary directory
  (under `/var/folders`) gets a folder it can read and not write.

Each failure prints an error, and `Launch` already throws away the shell's error output. The
resolution, the import and the snapshot only read what the shell has built, so they come out the
same. The exception is a startup file that needs something blocked in order to define a function
or set a variable.

## Decisions this step asks for

1. **The snapshot's shell reads the whole home; the resolution's does not read the denied-always
   list.** The resolution's output leaves the sandbox: on macOS an imported variable such as
   `HTTPS_PROXY` or `OLLAMA_HOST` is dialed by the sidecar itself, so a credential the shell read
   could ride out in one. The snapshot's output reaches only commands outside the sandbox, which
   read the home anyway, and hiding `~/.ssh/config` or `~/.kube/config` would change what it
   saves. Recommended. The note's denied-always paragraph and the denied-always row of
   `security-model.md` name the snapshot's run as the one exception.
2. **A start the sandbox refuses fails the resolution rather than running the shell
   unconfined.** On macOS that loses the environment import as well as the `PATH`, so a Dock
   launch runs its `exec` credential plugins with launchd's environment and cannot find one on
   the shell's `PATH`. Recommended: on a machine whose sandbox answered the probe, a refused
   policy is a bug, and the fallback `PATH` is the safe answer.
3. **Never-list variables are dropped from the shell's environment, `SSH_AUTH_SOCK` included.**
   Recommended: the sandbox refuses them, and the reason `scrubbedEnv` copies the agent socket
   does not hold inside the sandbox (§1).

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Launch` takes a `Start`; `Commander`, `loginshell.In`, the environment and the policy; the scratch root, its seed and its sweep; the locks move to `rundir` | `loginshell/loginshell.go`, `loginshell/sandboxed.go`, `rundir/rundir_unix.go`, `rundir/rundir_windows.go`, `tools/bash/rundir.go`, `tools/bash/rundir_unix.go`, `tools/bash/rundir_windows.go`, `tools/bash/bash.go`, `sandbox/policy_unix_test.go`, their tests (`rundir/rundir_unix_test.go` takes the lock and sweep tests from `tools/bash/rundir_unix_test.go`) | — | Planned |
| 2 | `main` probes once; `app.Config.Sandbox`; the app takes the sandbox from it and the status through `sandboxStatusOf`; Linux skips the resolution with no sandbox | `sidecar/main.go`, `sidecar/main_unix.go`, `sidecar/main_default.go`, `sidecar/main_darwin.go`, `app/app.go`, their tests | — | Planned |
| 3 | The launch resolution and the refresh run in the sandbox | `sidecar/main_unix.go`, `sidecar/main_darwin.go`, `app/app.go`, `app/shellpath_unix.go`, their tests | 1, 2 | Planned |
| 4 | The snapshot runs in the sandbox; `Paths.LoginShellDir` | `tools/bash/snapshot_unix.go`, `tools/bash/bash.go`, `app/paths.go`, their tests | 1, 2 | Planned |
| 5 | Docs and the ADR, per *When it lands* | see there | 1–4 | Planned |

**Order:** 1 and 2 at the same time, then 3 and 4 at the same time, then 5.

## Tests

**`loginshell`** (`sandboxed_test.go`, beside `sandboxed.go`). Each runs through the real sandbox,
and skips where the probe finds none, unless it says it runs over a fake. The package gains a
`TestMain` that calls `sandbox.Main`, since the forwarder re-runs the test binary.

**Fixtures live outside `/tmp`.** Every Linux run mounts a private tmpfs on `/tmp`, so a fake
home under `t.TempDir()` would be invisible inside the run, and a Kstack directory there would be
hidden whether or not the policy closes it. The tests make their fixture root under the test
package's own directory (a `testdata/tmp-<random>` folder removed in `t.Cleanup`, 0700). Each sets
`HOME` to a fake home there whose `~/.profile` runs the test's script, and stubs the shell lookup
(a test seam over `findShell`) to `/bin/sh`, which as a login shell (`-l`) reads `~/.profile`.
The script is never named by `ENV`: §1 drops it. Each reads the answer through `Path`, except
where it says otherwise:

- `TestTheLoginShellCannotReadKstacksDirectories`: the script appends to `PATH` a folder named
  after the contents of a file in each of Kstack's directories. The resolved `PATH` holds none of
  it, and a control file beside those directories, outside them, is read, so the script is shown
  to have run.
- `TestTheResolutionCannotReadTheNeverList`: through `In` with `deny` set to `sb.Never` of the
  fake home, the same for a file under the fake home's `.ssh`. The resolved `PATH` holds none of
  it, and a control file elsewhere in the home is read.
- `TestTheSnapshotsShellReadsTheHome`: through `In` with a nil `deny` and `Launch` directly, the
  script prints a file under the fake home's `.ssh`, and the output holds it.
- `TestTheLoginShellCannotWriteTheHome`: the script writes a file in the home, then appends a
  marker folder to `PATH`. The marker is in the resolved `PATH`, and the file is not there
  afterwards.
- `TestTheLoginShellWritesItsScratch`: a file the script writes under `$TMPDIR` lands, and
  `TestTheLoginShellsScratchIsGone` finds the folder gone once the shell has exited.
- `TestALeftoverScratchIsRemoved`: a folder in the scratch root whose lock no process holds is
  gone after the next start, and one under a lock another process holds is kept.
- `TestTheLoginShellHasNoNetwork`: a listener on loopback, outside the sandbox, gets no
  connection.
- `TestTheLoginShellLooksUpNoTrustDaemon`: over a `Commander` that records the command the real
  sandbox builds, the `Run` carries a zero `Network`, and no argument of the command names
  `com.apple.trustd.agent`. On macOS that is the compiled profile, which `sandbox-exec` takes
  after `-p`.
- `TestANeverListVariableIsDropped`: through `In` and `Launch` directly, since `scrubbedEnv` never
  copies `AWS_PROFILE`: with `SSH_AUTH_SOCK`, `AWS_PROFILE` and `ENV` in the environment passed,
  the shell starts and prints its environment, and none of the three is in it.
- `TestAPolicyTheSandboxRefusesIsAFault`, over a fake: a `Commander` that answers an error yields
  the `sandbox refused` reason and starts nothing.
- `TestAScratchThatCannotBeMadeIsAFault`, over a fake: a scratch root under a file yields the
  `no scratch` reason, and the `Commander` is never asked.
- `TestARelativeDirectoryIsRefused`: `In` with a relative `deny` path, Kstack directory or
  scratch root answers an error.
- `TestAStartCutShortIsATimeout`, over a fake: a `Commander` that answers `ctx.Err()` once the
  context has ended yields the `timeout` reason, not `sandbox refused`.
- `TestWithoutASandboxTheShellRunsAsBefore`: `In(nil, …)` returns a plain command with the
  environment as given and the home as `Dir`, and an empty environment yields an `Env` that is
  empty and not nil. `Launch` leaves the command's `Env` and `Dir` as `start` set them.
- `TestLaunchSetsTheSession`: the command the real sandbox builds has `Setsid` set once `Launch`
  starts it, and a kill reaches a child the script left behind in its group.
- `TestAShellThatCannotWriteItsStateFallsBack`: the shell lookup is stubbed to a fixture script
  named `nu`, so the package reads it as nushell, which writes under `$HOME/.config/nushell` and
  exits 1 when it cannot. It answers a `Fault` with `shell exited`, within the timeout.

**`rundir`** (`rundir_unix_test.go`): the lock and sweep tests from `tools/bash`, unchanged but
for the names they call.

**`sandbox`** (`policy_unix_test.go`): the table both compilers answer gains this step's shape —
a Read rule on `/`, the Kstack directories closed, a run's own write inside one of them — with
cases that a file under each Kstack directory is unreadable, a file in the home is readable, the
scratch is writable, and the home is not.

**`bash`**

- `TestTheSnapshotIsTakenInTheSandbox`: over a fake `sandboxer`, the snapshot's shell reaches
  `Command` with the policy in §1 and an empty `Always.Deny`, `TMPDIR` under
  `Paths.LoginShellDir`, and no never-list name.
- Every existing snapshot and run directory test passes, with what it checks unchanged.

**`main` and `app`**

- `TestTheLaunchResolutionRunsInTheSandbox` (`main_unix_test.go`): `launchShell` with a fake
  `Commander` that records its `Run` and answers an error: the `Run` carries the login shell's
  policy with the never list in `Always.Deny`, and the answer is the `sandbox refused` fault.
- `TestLinuxSkipsTheResolutionWithNoSandbox` (`main_default_test.go`): `launchShell` with a nil
  `Commander`, and `resolveShell` replaced by one that fails the test, answers no `PATH` and no
  fault.
- `TestTheRefreshRunsInTheSandbox` (`app_unix_test.go`): the refresh's `Run` carries the never
  list in `Always.Deny`.
- `TestTheSandboxIsProbedOnce`: given a `SandboxProbe`, the app calls `probeSandbox` never — for
  one whose sandbox is nil, and, where the machine has one, for a real probe's answer — and its
  status still goes through `sandboxStatusOf`.
- `TestTheAppProbesWhenItIsGivenNoSandbox`: given nil, the app calls `probeSandbox` once.

## Security

**Confined.** Kstack no longer runs the user's startup files with full access when it runs the
login shell itself: for the launch resolution, the refresh and the snapshot. While that shell
runs, it cannot read Kstack's files, reach its socket, write outside its scratch, or use the
network. This closes a startup file whose harm happens during that run: an update check of a
compromised plugin manager, a line that reads `app.db` and sends it. On Linux nothing it starts
outlives the kill. On macOS a process it starts into a new session through `posix_spawn` can, and
stays confined while it does.

**Not confined.** Two residuals, each a **By decision** row:

- **A command outside the sandbox runs the `-c` startup files.** It is `<shell> -c` with the
  process environment (`tools/bash/bash_unix.go`), so zsh reads `/etc/zshenv` and `~/.zshenv`,
  and bash the file `BASH_ENV` names when it is set, with full access, once per command. A
  startup file changed to do harm there does it on the next such command. That command asks,
  every one, and running the user's shell as the user set it up is what step 1B's switch is for.
- **What the login shell outputs shapes what runs outside the sandbox**, and a startup file can
  use that to postpone its harm: the snapshot's functions, aliases and `PATH` run, unconfined, in
  every command outside the sandbox (a `cd` the profile redefines runs there with full access);
  and on macOS the imported variables (`HTTPS_PROXY` among them) and the imported `PATH` shape the
  sidecar's own `exec` credential plugins, which run with no sandbox. The snapshot exists to carry
  the user's shell into commands run outside the sandbox, and those commands ask, every one. On
  macOS the import can also carry out what the resolution's shell read: the denied-always list
  is shut to it, but a secret in a file the list does not name (`~/.pgpass`) can be written into
  `HTTPS_PROXY` or `OLLAMA_HOST`, which the sidecar dials. That is the list's curated-list
  residual, the one a read grant of `~` has (step 4D).

The resolution's shell cannot read the denied-always list, since what it answers leaves the
sandbox: the `PATH` into every sandboxed run, and on macOS the imported variables into the
sidecar's own process. The snapshot's shell reads the whole home on purpose, the list included;
the record argues that too, since it is the one run Kstack starts that reads `~/.ssh` and
`~/.kube`. What bounds it is that the shell writes nothing outside its scratch and reaches
nothing, and that its answer, the profile, is sourced only by commands outside the sandbox,
which read the home themselves.

The record, `docs/security/<date>-the-login-shell-in-the-sandbox.md`, argues all of this, and
names the ways a command can change a startup file: step 1B's switch, and, from step 4D, a
read-write grant of a folder holding a file a startup file sources from outside the `NoWrite`
list, which is step 4D's residual.

## When it lands

- **The security record** above.
- **The ADR**, `docs/adr/<date>-the-login-shell-runs-in-the-sandbox.md`: the snapshot's shell
  reads the whole home, the resolution's all of it but the denied-always list, and both write
  only their scratch; a refused start fails closed; and the two residuals in
  Security are accepted. The **By decision** rows cite it.
- **`security-model.md`**: the rows for the snapshot and the shell import say the shell runs in
  the sandbox, and list the new tests. Two **By decision** rows, citing the ADR: how the shell's
  output shapes what runs outside the sandbox, the macOS import's curated-list residual
  included, and the `-c` startup files a command outside the sandbox runs. The denied-always row
  names the snapshot's shell as the one run without it; step 4D edits the same row, and
  whichever lands second keeps the other's text.
- **The note**: its denied-always paragraph names the snapshot's run as the exception.
- **`sidecar/CLAUDE.md`**: the login shell resolution's paragraph and the snapshot's; `main`
  probes the sandbox and hands it on in `app.Config.Sandbox`, and skips the resolution on Linux
  with none; `loginshell.In`, `Commander`, the environment, the policy and its `deny` per run, a
  start cut short being a timeout, the scratch root, its seed and its lock-based sweep in
  `rundir`; `Paths.LoginShellDir`.
- **The sequence's README**: this row's status; the record table's 4A row (ADR **yes**, both
  **By decision** rows); the denied-always row and the note paragraph in the seams with step 4D.

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
