---
title: Process limits and restrictions
scope: sidecar
status: Planned
---

# Process limits and restrictions

**Needs:** step 1A's `Policy`, which this step extends (landed; `sidecar/CLAUDE.md` describes
it). **Unblocks:** step 4C, which needs the forwarder as every run's first process.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command is bounded in time alone. A foreground call has a wall-clock timeout,
capped at `MaxTimeout` (600 s), and the process-group kill that follows it. A background command
has no clock: it runs until it exits or the user stops it. Nothing bounds what the command's
processes use meanwhile. A `while :; do :; done` under a fork, or a `kubectl get --watch` that
leaks descriptors, can take the machine.

Of the note's *Process and kernel restrictions*, two hold on one platform and not the other.
Linux has `no_new_privs`; macOS lets `sudo` be started. macOS reads no other process's
information; Linux's filter leaves `ptrace` open.

After this step the note's three remaining bullets are true of the code:

- *"Resource limits per command: CPU time, memory, open files, process count, and a wall-clock
  timeout."* A run carries **`Policy.Limits`**, set by Bash's Workspace policy:
  - `MaxTimeout` plus `killGrace` times the machine's CPU count of CPU time per process, on a
    foreground run: 4840 s on an 8-CPU machine;
  - 16 GiB of address space per process, on Linux;
  - 4096 open files per process;
  - a process count over what the kernel already counts against the run: 512 processes on
    macOS, and on Linux, which counts threads, 128 tasks per CPU and at least 1024.

  They are set inside the run, before the shell starts, so they hold the shell and everything
  under it and never the sidecar. The forwarder, `sandbox-init`, sets CPU time and open files on
  both platforms, and the process count on macOS. On Linux `sandbox-shell` sets memory and the
  process count. On macOS the forwarder becomes every run's first process, as it is on Linux.
  Every run also gets a core size of zero.
- *"No privilege escalation: `no_new_privs` on Linux; deny execution of `sudo` and other setuid
  binaries explicitly on both platforms."* Linux has `no_new_privs`. macOS gains a fixed rule in
  `profile_darwin.sb` refusing the setuid programs that exist to escalate (§4).
- *"No `ptrace` or `task_for_pid`; no reading other processes' environment."* Linux's filter
  answers `EPERM` to `ptrace`, `process_vm_readv`, `process_vm_writev`, `pidfd_getfd`, `kcmp`
  and `process_madvise`. macOS's profile already refuses the task port and other processes'
  information.

Beyond the note, Linux's filter also refuses the kernel keyring (`keyctl`, `add_key`,
`request_key`), which a run inherits from the user's session (§5).

The wall-clock timeout and the group kill stay as they are. A limit is a floor under a runaway
command, not a gate: nothing asks, and nothing is logged that is not logged today. It is a step
of [the sandbox, credentials and permissions note](../../notes/sandbox-credentials-and-permissions.md).

## What is not in this step

- **No setting.** The values are constants in `tools/bash`. No step of the sequence puts them in
  `securityconfig`; one may, once a user needs to.
- **No limit outside the sandbox.** A command the user switched out of the sandbox runs as the
  user, with the user's limits, as today.
- **No limit on the login shell.** Step 4A builds its policy and may set `Limits` there the same
  way.
- **No cgroup.** A process count and a memory bound through a cgroup need a delegated one, which
  a desktop session does not give an unprivileged app (*Decisions*).
- **No change on Windows**, which has no sandbox.

## Design

### 1. `Policy.Limits`

`sandbox/policy.go`:

```go
// Limits bounds what a run's processes may use. Zero is the platform's
// default for that resource. Each holds per process but Processes, which
// the kernel holds against a count (Sandbox.CountedProcesses).
type Limits struct {
	CPUSeconds  int // CPU time per process
	MemoryBytes int // address space per process; Linux alone
	OpenFiles   int // open descriptors per process
	Processes   int // the kernel's count of tasks (Linux) or processes (macOS)
}
```

`Policy` gains `Limits Limits`. `Check` refuses a negative limit. It also refuses `MemoryBytes`
or `Processes` set with `OpenFiles` zero, since `sandbox-shell` relies on the open-files limit
being set (§3).

Each limit is one resource limit, set soft and hard so a process cannot raise it back:

| Limit | Resource | Soft | Hard | Past it |
| --- | --- | --- | --- | --- |
| `CPUSeconds` | `RLIMIT_CPU` | the value | the value plus `cpuGrace` (5 s) | `SIGXCPU`; on Linux, `SIGKILL` at the hard limit |
| `MemoryBytes` | `RLIMIT_AS` | the value | the value | `mmap` and `brk` fail with `ENOMEM` |
| `OpenFiles` | `RLIMIT_NOFILE` | the value | the value | `open` fails with `EMFILE` |
| `Processes` | `RLIMIT_NPROC` | the value | the value | `fork` fails with `EAGAIN` |

The CPU limit has a grace for the same reason the timeout does: a process that traps `SIGXCPU`
can say something, and on Linux the kernel kills it five seconds of CPU later whatever it does.
macOS sends `SIGXCPU` once, at the soft limit, and nothing at the hard one (XNU's `bsd_ast`), so
there a process that ignores the signal runs on (Security). A shell whose child was killed by
`SIGXCPU` exits 152 (`ExitCode`: 128 plus the signal).

`MemoryBytes` is Linux's alone: on macOS only a process that sets the limit and then execs
could apply it, and macOS has none, so the macOS compiler passes it to nothing (*Decisions*).

**What `RLIMIT_NPROC` counts depends on the kernel.** Both platforms count by the real uid.

- **Linux from 5.14** keeps the count per user namespace. A run gets its own wherever the
  machine allows one: `Sandbox.args` passes `--unshare-user-try`, which an unprivileged bwrap
  needs anyway and a setuid one takes when user namespaces are on. In its own namespace the
  limit counts the run's own tasks alone, every thread one. When the shell starts, the
  namespace holds the forwarder and its threads, which the margin covers. A machine that turns
  user namespaces off (`user.max_user_namespaces=0`) runs a setuid bwrap with none, and the
  count is the machine's, as before 5.14.
- **Linux before 5.14** (RHEL 8's 4.18, for one) counts every task of the user on the machine.
- **macOS** counts every process of the user on the machine; threads do not count.

Neither kernel holds a process of uid 0 to the limit. Linux also exempts a process with
`CAP_SYS_RESOURCE` or `CAP_SYS_ADMIN` in the initial namespace. A capability in the run's own
namespace exempts nothing.

Linux from 5.14 to 5.16 lets a fork through at the limit and refuses the next; 5.17 refuses at
the limit. One task either way changes nothing the margin does.

So `Processes` is a count over a base, and the sandbox answers the base.

**`(*Sandbox).CountedProcesses() (int, error)`** answers how many the kernel already counts
against a run that starts now. It is a method because Bash reaches it through the `sandboxer`
seam (§2).

- On Linux, `Probe` records two things on the `Sandbox`. It reads the kernel's release once with
  `unix.Uname` and keeps whether it is 5.14 or later. It also keeps whether the probe's run had
  a user namespace of its own: the probe's command becomes
  `read -r m < /proc/self/uid_map; echo "$m"`, which uses the shell's builtins alone, since a
  NixOS run's `PATH=/usr/bin:/bin` holds no `readlink`. The namespace is its own when the line
  it prints, split into fields, differs from the first line of the sidecar's own
  `/proc/self/uid_map`: two namespaces with different maps are different namespaces. An empty
  line, or one that matches, is not its own. The probe still passes either way.
- From 5.14, with a namespace of its own, the method answers 0. Otherwise, or when the release
  does not parse, it scans `/proc` in `procs_linux.go`. For each `/proc/<pid>/status` whose
  `Uid:` line's first field equals `os.Getuid()`, the real uid, it adds the `Threads:` line. It
  never reads the owner of `/proc/<pid>`, which is the effective uid, and root for a
  non-dumpable process such as a forwarder. A process that exits between the listing and the
  read is skipped. The scan runs outside the sandbox: inside, `/proc` shows the run's own
  processes alone. Any doubt takes the scan, because a base that is too high loosens the limit,
  while a base of 0 under a machine-wide count would refuse the shell. The scan sees the
  sidecar's PID namespace and those under it, so a sidecar in a container's namespace counts
  short (Security).
- The scan is `countThreads(pids []int)` over the pids it lists, so a test can count one
  process's threads.
- On macOS, `procs_darwin.go` counts `kern.proc.ruid.<uid>` through
  `unix.SysctlKinfoProcSlice`.
- On Windows, `procs_windows.go` answers `errNone`.

### 2. The Workspace policy's limits

`sandboxedRunFor` in `tools/bash/bash.go` sets `Limits` on the policy it builds. It already knows
whether the run is a background one.

| Limit | Value | Why |
| --- | --- | --- |
| `CPUSeconds` | `MaxTimeout` plus `killGrace`, in seconds, times `runtime.NumCPU()` on a foreground run; 0 on a background one | A process's threads together spend at most one CPU-second per CPU each wall-clock second, so no foreground process reaches it before the clock and the grace end the run. It binds a process that outlives the clock (*Decisions*). A background run has no clock, and a long one (`kubectl logs -f`, a polling loop) must not die of CPU. |
| `MemoryBytes` | 16 GiB | Room for a large `helm template` and the ~10 GiB V8 reserves for one WebAssembly memory; not for a leak. |
| `OpenFiles` | 4096 | The system's usual default; `kubectl` and `helm` need far fewer. |
| `Processes` | `CountedProcesses()` plus `processMargin(runtime.NumCPU())` | A fork loop stops before it takes the machine. |

The margin counts what the kernel counts. On macOS it is 512 processes: a shell, its tools and
an `xargs -P 8` are a few dozen. On Linux every thread is a task, and a Go program starts about
one per CPU and a few more, so `kubectl` alone is 20 on a 16-CPU machine, and a loop of 50
backgrounded `kubectl` calls is 1000. There the margin is `max(1024, 128 × runtime.NumCPU())`:
1024 tasks on 8 CPUs, 4096 on 32. A Go or Java program that cannot start a thread past it does
not see `EAGAIN`; it crashes (`runtime: failed to create new OS thread`), which the prompt names
(§7). On Linux from 5.14 the limit counts the run's namespace alone. Elsewhere it is the margin
over the user's count at the run's start.

`sandboxedRunFor` reads the base first, through the `sandboxer` seam, which gains
`CountedProcesses`, so Bash's fake answers it. It reads it before it makes the run's directory
or starts its proxy, and returns its error, so a base that cannot be read fails the call and
nothing is made or run: as with a policy that fails `Check`, a run never starts with a limit
other than its policy's. It passes the `Limits` to `workspacePolicy` as an argument, so the
policy goroutine stays as it is. The constants are `limitCPU`, `limitMemory` and `limitOpenFiles` in
`bash.go`; `limitCPU` is `MaxTimeout + killGrace`, and `sandboxedRunFor` multiplies it by
`runtime.NumCPU()`. `processMargin(cpus int)` is per platform, and `sandboxedRunFor` passes it
`runtime.NumCPU()`: `limits_linux.go` answers `max(1024, 128 × cpus)`, `limits_other.go`
(`!linux`) 512.

### 3. Where the limits are applied

**The process tree.** On Linux, bwrap starts the forwarder as the first process of the run's
namespaces (`--as-pid-1`) and stays outside them as their parent. The forwarder starts
`sandbox-shell` as its child, which installs the filter and execs into the shell. On macOS,
`sandbox-exec` execs into the forwarder, which starts the shell as its child; macOS has no
`sandbox-shell`.

```
Linux:  bwrap → sandbox-init (pid 1) → sandbox-shell ⇒ shell
macOS:  sandbox-exec ⇒ sandbox-init → shell
```

**The forwarder applies CPU and files on both platforms, and processes on macOS.**
`ForwarderArgs(r Relay, l Limits)` (`sandbox/forward.go`) writes one argument after the socket
and port:

```
sandbox-init [--socket <S> --port <P>] [--limits cpu=<C>,files=4096,processes=<N>] -- <argv…>
```

Every key is present, `0` for the platform's default. The flag is left out when every key is
zero, so a probe's line is what it is today. Linux's compiler writes `processes=0`, since
`sandbox-shell` sets it there. `parseInitArgs` reads it into `initArgs.limits`: an unknown key, a
missing one, or a value that is not a non-negative integer is *bad arguments*, exit 125.

`InitMain` (`forward_unix.go`) applies each nonzero one with `unix.Setrlimit` after it listens
and before `ForkExec`, so the child inherits them. An unprivileged process cannot raise a hard
limit, so **each is clamped to the process's own hard limit** (`own`):

- files and processes: soft and hard are both `min(value, own)`;
- CPU: soft is `min(value, own)` and hard is `min(value + cpuGrace, own)`. When `own` is at or
  below the value, soft and hard are equal, and the kernel sends `SIGKILL` at the limit with no
  `SIGXCPU` first.

A machine whose limit is stricter stays stricter. A kernel that refuses the call anyway is
*cannot set limits*, exit 125. On `RLIMIT_NOFILE`, `unix.Setrlimit` goes through
`syscall.Setrlimit`, which since Go 1.21 drops the runtime's saved copy of the original limit,
so the exec'd child keeps the value set rather than the original the runtime would restore.

**Every run's core size is zero.** `InitMain` sets `RLIMIT_CORE` to 0, soft and hard, whether
or not `--limits` is given. A `SIGXCPU` or `SIGSEGV` would otherwise dump the process's memory
where the machine's `core_pattern` says: a file outside the sandbox, or `systemd-coredump` or
`apport`, which read the limit and keep nothing at zero.

The forwarder runs under the limits it sets. That is fine:

- It forks once and relays a few dozen connections.
- It carries a CPU limit only on a foreground run, which the clock and the grace end within
  605 s. A relay never spends that much CPU in that time.
- On Linux it carries no process limit, so a full namespace never stops the Go runtime starting
  a thread. On macOS it carries one, which counts processes, never its threads.

**`sandbox-shell` applies memory and processes, on Linux.** `Sandbox.args` (`sandbox_linux.go`)
writes `--memory <bytes>` and `--processes <n>` before the `--` of `sandbox-shell`, each when
set, and `parseShellArgs` (`shell.go`) reads them. `ShellMain` (`seccomp_linux.go`) applies them
after `no_new_privs` and the filter, clamped as the forwarder clamps:

1. It turns the garbage collector off (`debug.SetGCPercent(-1)`), so no collection starts
   between here and the exec.
2. It builds the exec's path, argv and environment as C strings
   (`syscall.BytePtrFromString`, `syscall.SlicePtrFromStrings`), and the line it writes if the
   exec fails, `sandbox-shell: cannot start <argv[0]>: `, as bytes.
3. It sets `RLIMIT_NPROC`, then `RLIMIT_AS`, last, since the address space is the limit a
   stray mapping trips.
4. It calls `execve` through `unix.RawSyscall`, so nothing is allocated under the limits.
5. If the exec fails, it writes the prepared line and the errno's number through raw
   `write`s on fd 2 and exits 125 through `unix.RawSyscall(SYS_EXIT_GROUP, …)`. `fail` would
   format through `fmt`, which allocates, and an allocation past the address-space limit is
   a runtime crash rather than the line.

The runtime's other threads keep running through steps 3 and 4. With nothing allocating and no
collection running, they have no reason to map memory or start a thread, so the limits cannot
fail the runtime before the exec. This matters under `-race`, whose shadow memory already maps
far more than 16 GiB.

With no limit to set it calls `unix.Exec`, as today. The raw `execve` skips the runtime's
restoring of the soft open-files limit it raised at start. That changes nothing: `Check`
requires `OpenFiles` beside either limit, and the forwarder set it soft and hard alike, so the
runtime raised nothing.

The forwarder sets neither. It is a Go program that keeps running to relay: it must keep its
address space, and on Linux every thread it starts counts against the process limit
(*Decisions*).

**The namespace is the run's own where the machine allows one.** `Sandbox.args` gains
`--unshare-user-try` beside the other `--unshare-*` flags (§1). Where bwrap cannot make one, the
run starts without it, and the probe's check sends `CountedProcesses` to the scan.

**On macOS the forwarder is every run's first process.** `Sandbox.argv` (`sandbox_darwin.go`)
answers `<self> sandbox-init … -- <shell> <args…>` for every run, a run with no socket included;
today it does so only for a run with one. `sandbox-exec` execs into the forwarder, so the profile
holds it and the shell under it, as today. The probe's run then starts this executable once
more, so `probeTimeout` goes from 2 s to 5 s, as Linux's `probeBound` is for the same reason.

### 4. `sudo` and setuid

Linux is done: `sandbox-shell` sets `PR_SET_NO_NEW_PRIVS` before it execs the shell, so a setuid
bit under it grants nothing, and `sudo` says so and exits 1.

macOS gains one fixed rule in `profile_darwin.sb`, among the process rules right after
`(allow process-exec)`, beside the `setsid` denial. It sits before the policy's rules, as those
do. No rule of a policy can undo it: the compiler writes only `file-read*` and `file-write*`
rules there, never a `process-exec` one.

The rule names the programs that exist to escalate, at the paths the system installs them:

```scheme
;; No privilege escalation (TestSudoCannotGainRoot).
(deny process-exec
  (literal "/usr/bin/sudo")
  (literal "/usr/bin/su")
  (literal "/usr/bin/login")
  (literal "/usr/libexec/security_authtrampoline"))
```

The shell reports a refused exec as *Operation not permitted*, exit 126. The rule is defense in
depth: Seatbelt already holds a setuid child to the profile. A setuid program off the list runs,
inside the profile, as it does today (Security).

**`ps` and `top` stay.** macOS installs `/bin/ps` and `/usr/bin/top` setuid root, so they can
read other processes' information. A command uses `ps` to see its own background jobs, and
§5's `process-info*` rule is what keeps it to the run's own processes.

**Why a list, not the mode.** Task 1 tried `(deny process-exec (file-mode #o4000))` on macOS
27.0.1 (xnu-13432). It refused `sudo`, `su` and a setuid file of the user's own, and left `ls`
running, but it also refused a setgid-only file, and an `(allow process-exec (literal …))` after
it did not win `ps` or `top` back: both exited 126 with the allow line and without it. A rule
that refuses `ps` breaks commands for no gain, so the list it is.

#### Task 1's findings (macOS 27.0.1, Apple silicon)

- **Setuid programs.** `/bin/ps` and `/usr/bin/top` are setuid root, beside `sudo`, `su`,
  `login`, `security_authtrampoline` and the system's other setuid programs, and `ARDAgent`.
  Third-party installs add their own (VirtualBox's `VBoxNetAdpCtl`, Dropbox's helpers).
- **`file-mode #o4000`** matches setuid and setgid files alike, and no later `allow` overrides
  it for `process-exec`.
- **The CPU limit.** Under `ulimit -St 1; ulimit -Ht 6`, a spin loop dies of `SIGXCPU` at 1 s
  (exit 152). One that ignores `SIGXCPU` spent 19.7 s of CPU and was still running when an alarm
  stopped it at 20 s: macOS enforces no hard CPU limit, as XNU's source says.
- **The task port.** Refused `mach-priv-task-port` and `mach-task-name`, `/usr/bin/sample <pid> 1`
  exits 255 printing a line that begins `sample cannot examine process <pid>` and ends by
  suggesting `sudo`. Outside, it exits 0.
- **`RLIMIT_AS`.** `ulimit -v` fails with *Invalid argument* at 16 GiB and at 1 GiB: an ordinary
  shell already maps about 466 GiB (VSZ), mostly the shared region, while using 2.8 MB.
- **Process counts.** `ulimit -Su` 4000, `ulimit -Hu` 6000, `kern.maxprocperuid` 4000,
  `kern.maxproc` 6000; the user was running 518 processes. A run's limit is then about 1030.

### 5. `ptrace` and the keyring

`filter()` in `seccomp_linux.go` answers `EPERM` to `ptrace`, `process_vm_readv`,
`process_vm_writev`, `pidfd_getfd`, `kcmp` and `process_madvise`, and to `keyctl`, `add_key`
and `request_key`, beside the sockets and namespaces it refuses today. All nine are in
`x/sys/unix` v0.47.0 for amd64 and arm64.

**The keyring.** Every process inherits its parent's session keyring, and bwrap makes no new
one, so a sandboxed run holds the user's. A key there is the user's to read: a Kerberos ticket
cached as `KEYRING:`, or a key `pam_keyinit` or a mount helper left. With the three calls
refused, a command can neither read a key nor add one. Nothing a sandboxed command needs uses
the kernel keyring; Docker's default seccomp profile refuses the same three.

A command in the sandbox cannot attach to, single-step or trace any process, its own children
included, so `strace` and `gdb` do not run there. It cannot read another process's memory
through those calls, take another's descriptors, or compare kernel objects with another.

It can still read `/proc/<pid>/mem` and `/proc/<pid>/environ` of its own descendants. The
kernel checks those files with the `ptrace` permission check, not the syscall, and Yama allows a
process its descendants. Those are the run's own processes, holding nothing the run did not give
them. The forwarder is the one process in the run's PID namespace that is not a descendant of
the shell, and it is non-dumpable (`guardMemory`), so its files are root's.

macOS needs no new rule. `task_for_pid` needs the `mach-priv-task-port` operation and
`task_name_for_pid` the `mach-task-name` one, and `(deny default)` refuses both. `process-info*`
is allowed for `(target same-sandbox)` alone, so `ps`, `proc_pidinfo` and `sysctl kern.proc`
see the run's own processes and nothing else. `TestNoOtherProcessIsRead` pins the information
half today; this step adds a test for the task port, and makes that test assert `ps` ran, since
today it passes as well when `ps` cannot start.

### 6. What stays

The wall-clock timeout, `killGrace`, the group kill after every run and the cancel's immediate
`SIGKILL` in `tools/bash` are unchanged: the clock bounds a foreground run, the limits bound each
process. Windows is unchanged too; `forward_windows.go` answers 125 as today.

### 7. What the model is told, and what the user sees

`prompts/sandbox.md` gains three sentences. First, `sudo` does not work in the sandbox. Second,
a command's processes are limited in memory, open files and count, and in the foreground in CPU
time, so a process past its CPU time is killed, exit 152. Third, a program that crashes saying
it cannot create a thread hit the count, and fewer parallel jobs is the fix. The user sees
nothing new: a killed command's disclosure shows its exit code, as today.

### 8. Where this meets the other wave-2 steps

**2A** changes the read rules the compilers write, so it changes the same goldens
(`sandbox/testdata/args_linux_*.golden`, `profile_darwin_*.golden`). 2B's lines are separate
from 2A's: `--unshare-user-try` among bwrap's flags, `--limits` on `sandbox-init`, `--memory` and
`--processes` on `sandbox-shell`, and the `deny process-exec` list after `(allow process-exec)`.
The step that lands second rebases onto the other and regenerates the goldens. Its reviewer
checks that the diff holds its own lines and nothing else.

2A also changes `sandboxedRunFor` and `workspacePolicy`: `sandboxedRunFor` loses its `snapshot`
argument, and `Never` is called on its policy goroutine. 2B's changes there are separate:
`CountedProcesses` is read at the top of `sandboxedRunFor`, before the run's directory and the
goroutine, and its error is returned from there; `workspacePolicy` gains a `Limits` argument
and sets it on the policy it returns. Whichever lands second keeps the other's change. 2B makes
no other change in either order.

**2C** changes how `sandboxedRunFor` reaches its runtime's values. 2B adds the base read and the
`Limits` it builds there, which read only the `background` argument the function already takes.
Whichever lands second keeps the other's change.

## Decisions this step asks for

**The process limit is a margin over a base the sandbox answers.** On Linux from 5.14 the base is
0, because the kernel counts per user namespace and every run has its own. Before 5.14, and on
macOS, the kernel counts the user's whole machine, so the base is the user's count, read when
the run is built. A fixed value there would either refuse the shell on a busy machine or allow a
fork loop thousands of processes on a quiet one. Linux counts threads, and a desktop's browsers
and editors run thousands, so the scan counts them the kernel's way. The base can move between
the read and the shell's start; the margin covers it. The margin is in the kernel's own unit:
processes on macOS, tasks on Linux, where it grows with the CPU count because a Go program's
threads do. A fork refused past it is `EAGAIN`, which the command reports; a thread refused
crashes a Go or Java program, which the prompt explains. The alternative, a cgroup's
`pids.max`, needs a cgroup delegated to the app, which systemd's user session gives no
unprivileged program by default, and which bwrap's `--unshare-cgroup-try` does not make.
Rejected.

**The run's user namespace is asked for, never required, and checked.** A setuid bwrap makes no
user namespace unless told to, and then the count is the machine's. So `Sandbox.args` passes
`--unshare-user-try`, and the base is 0 only when the probe saw a namespace of its own. The flag
is `-try` because some hardened machines turn user namespaces off for everyone
(`user.max_user_namespaces=0`): there `--unshare-user` would fail every run, the probe would
answer no sandbox, and every command would run outside it. A looser process count is the
smaller loss.

**On Linux the process limit is `sandbox-shell`'s, on macOS the forwarder's.** Linux counts a
thread as a task, so a Go forwarder under the limit fails to start a thread once the run is full,
and the runtime dies, taking pid 1 and the run with it. `sandbox-shell` sets the limit and
execs, so nothing of Kstack's runs under it. macOS counts processes alone, so the forwarder
sets it there, and no second process is needed.

**A background run has no CPU limit.** It has no wall clock either: the user sees it running
and stops it. A CPU limit derived from the foreground clock would kill a legitimate long job.
The forwarder therefore carries a CPU limit only when the clock also bounds it, which is why
CPU stays the forwarder's to set and needs no second process on macOS.

**Memory is 16 GiB on Linux.** V8 reserves about 10 GiB of address space for each WebAssembly
memory, and Node fails under a lower `RLIMIT_AS`. 16 GiB keeps `node` working with one such
memory and still stops a leak long before it fills a desktop's swap. It does not fit a program
holding several such memories. Runtimes that reserve a heap at start are untested under it: a
JVM's default heap is a quarter of the machine's memory, though HotSpot is understood to cap it
at half of `RLIMIT_AS`, and .NET reserves a range for its GC. The by-hand checks run `java` and
`pwsh` on a machine with at least 64 GiB, and the ADR records what they found. Task 6's check
confirms `node`; if `node` fails at 16 GiB, the memory limit is dropped and Linux, like macOS, is
bounded by the clock alone (recorded in the ADR).

**Memory on macOS is bounded by the wall clock alone.** XNU checks `RLIMIT_AS` when a mapping
is made (`vm_map_enter`), but `setrlimit` refuses a limit below the caller's current map size,
and every process maps the shared region: an ordinary shell maps about 466 GiB (task 1). So no
process can set a limit that would bound a leak, and `ulimit -v` fails with *Invalid argument*
at 16 GiB and at 1 GiB alike. A Jetsam limit needs an entitlement or root. So a macOS run's
memory is bounded by the timeout and the group kill, and the macOS compiler passes
`MemoryBytes` to nothing.

**Memory is `sandbox-shell`'s to set, not the forwarder's.** The forwarder keeps running after
the shell starts, and a Go program under an address-space limit fails on its next arena; the
test binaries CI runs as the forwarder under `-race` map far more than 16 GiB before `main`.
`sandbox-shell` sets the limit and execs, allocating nothing in between, so nothing of
Kstack's runs under it.

**The CPU limit is `MaxTimeout` plus `killGrace` times the CPU count.** `RLIMIT_CPU` counts
every thread of a process, so a process on 16 threads spends 600 s of CPU in under 40 s of wall
clock. A limit of 600 s would kill a numpy job, `zstd -T0` or a parallel `rustc` well inside its
timeout. Scaled by the CPU count, and past the grace the timeout gives before its `SIGKILL`, no
foreground process can reach the limit before the clock ends the run, so the limit never fires
on legitimate work. It binds a process that outlives the clock: on macOS one that `posix_spawn`
started in a new session, which the group kill misses. On Linux the namespace ends with its
first process, so no process outlives the run, and the limit is kept there so both platforms
follow one rule.

**The CPU limit has a five-second grace.** Soft at the value, hard five seconds above it, so on
Linux a process that traps `SIGXCPU` is killed anyway. XNU sends `SIGXCPU` once at the soft
limit and enforces no hard one, so on macOS the limit ends a runaway, whose `SIGXCPU` is fatal
by default, and not a process that ignores the signal (Security).

**The core size is zero on every run.** A dump is the process's memory written outside the
sandbox. Nothing in the sandbox needs one.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | Spike, on macOS: the setuid programs; whether `file-mode #o4000` refuses setuid alone and an `allow` wins `ps` and `top` back; the hard CPU limit; `sample`'s refusal; `RLIMIT_AS`; the process counts. Recorded in §4 | — | — | Done |
| 2 | `Limits` on `Policy`, `Check`; `CountedProcesses`, the kernel release and the probe's user namespace; `--unshare-user-try` | `sandbox/policy.go`, `sandbox/sandbox_linux.go`, `sandbox/procs_linux.go`, `sandbox/procs_darwin.go`, `sandbox/procs_windows.go`, their tests | — | Planned |
| 3 | The forwarder: `--limits`, the clamp, the core size, every macOS run first; the probe's bound | `sandbox/forward.go`, `sandbox/forward_unix.go`, `sandbox/sandbox_darwin.go`, their tests | 2 | Planned |
| 4 | `sandbox-shell`: `--memory` and `--processes`, the GC off, the raw `execve` and its failure line; the nine calls in the filter | `sandbox/shell.go`, `sandbox/seccomp_linux.go`, `sandbox/sandbox_linux.go`, their tests | 2 | Planned |
| 5 | The profile's setuid rule, per task 1; the task-port test; `TestNoOtherProcessIsRead` asserts `ps` ran | `sandbox/profile_darwin.sb`, `sandbox/sandbox_darwin_test.go` | 1 | Planned |
| 6 | Bash reads the base and sets the limits, CPU in the foreground alone; the per-platform margin; the `sandboxer` seam; the prompt; the `node` check | `tools/bash/bash.go`, `tools/bash/limits_linux.go`, `tools/bash/limits_other.go`, `tools/bash/prompts/sandbox.md`, their tests | 2 | Planned |
| 7 | The real-sandbox tests and the goldens | `sandbox/policy_unix_test.go`, `sandbox/policy_linux_test.go`, `sandbox/golden_unix_test.go`, `sandbox/testdata/args_linux_*.golden`, `sandbox/testdata/profile_darwin_*.golden` | 3, 4, 5 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1 is done; 2 first; then 3, 4 and 5 at the same time; then 6; then 7; then 8.

## Tests

Every test through the real sandbox calls `confining(t)`. A test that first checks a program
outside the sandbox, to prove the sandbox is what refuses it, skips when that check fails.

No test sets a process limit a few above a machine-wide count, or asserts on how that count
moves: other tests and the Go runtime's own threads move it, and a run that cannot start its
runtime fails for the wrong reason. A test that needs a fork refused skips when `os.Getuid()` is
0, since the kernel holds root to no process limit.

**`sandbox`**

- `TestLimitsAreChecked`: a negative limit fails `Check`, and so does `MemoryBytes` or
  `Processes` with `OpenFiles` zero; zero passes.
- `TestTheKernelReleaseIsRead`, in `sandbox_linux_test.go`: `5.14.0-284.el9`, `6.1.0` and
  `7.0.14` count per namespace; `4.18.0-553.el8` and an unparsable release do not.
- `TestTheProbeFindsItsOwnUserNamespace`, in `sandbox_linux_test.go`: where `unshare -U true`
  runs outside the sandbox (else skipped), the probed sandbox records a user namespace of its
  own, and the compiled arguments carry `--unshare-user-try`.
- `TestTheUidMapDecidesTheNamespace`, in `sandbox_linux_test.go`: against the sidecar's line
  `0 0 4294967295`, the probe's `1000 1000 1` is its own namespace; the same line with other
  spacing, and an empty line, are not.
- `TestTheBaseIsZeroOnlyInANamespaceOfItsOwn`, in `procs_linux_test.go`: a `Sandbox` from 5.14
  without its own namespace, and one before 5.14 with it, both scan and answer at least one; one
  from 5.14 with its own namespace answers 0.
- `TestANewNamespaceCountsNothing`, in `procs_linux_test.go`: on a kernel from 5.14 (else
  skipped), the probed sandbox's `CountedProcesses` answers 0.
- `TestTheUsersProcessesAreCounted`, in `procs_unix_test.go`: through the scan on Linux and the
  sysctl on macOS, with a `sleep` child running, the count is at least two, the test and its
  child.
- `TestThreadsAreCounted`, in `procs_linux_test.go`: `countThreads` over a helper's pid alone,
  where the helper in the test binary holds 64 threads and makes itself non-dumpable, answers
  at least 64, though `/proc/<its pid>` is owned by root.
- `TestForwarderArgsWriteTheLimits`: `--limits` is written with every key when any limit is set,
  and left out when none is; `TestForwarderArgsRoundTrip` covers the new flag.
- `TestInitArgsReadTheLimits`: the value parses; an unknown key, a missing key, a negative value
  and a value that is not an integer are refused.
- `TestInitAppliesTheLimitsToItsChild`, in `forward_unix_test.go`: `sandbox-init --limits
  cpu=7,files=64,processes=<P> -- sh -c 'ulimit -t; ulimit -n; ulimit -u; ulimit -c'` prints 7,
  64, `P` and 0. `P` is the test's own soft `RLIMIT_NPROC`, or 1048576 when that is unlimited,
  so the limit changes nothing the test or its runtime needs.
- `TestInitLowersALimitToItsOwnHardOne`, in `forward_unix_test.go`: started by `sh -c 'ulimit -H
  -n 512; exec …'` with `files=4096`, the child's `ulimit -n` prints 512.
- `TestTheCPULimitWithNoRoomHasNoGrace`, in `forward_unix_test.go`: started by `sh -c 'ulimit -H
  -t 7; exec …'` with `cpu=600`, the child's `ulimit -St` and `ulimit -Ht` both print 7.
- `TestTheCoreSizeIsZeroWithoutLimits`, in `forward_unix_test.go`: with no `--limits`, the
  child's `ulimit -c` prints 0.
- `TestShellArgsReadTheLimits`: `--memory <n>` and `--processes <n>` parse before the `--`; a
  value that is not a non-negative integer is refused.
- `TestTheShellAppliesItsLimits`, in `seccomp_linux_test.go`: `sandbox-shell --memory 536870912
  --processes <P> -- sh -c 'ulimit -v; ulimit -u'` prints 524288 and `P`, with `P` chosen as in
  `TestInitAppliesTheLimitsToItsChild`.
- `TestAShellThatCannotExecUnderLimitsSaysWhy`, in `seccomp_linux_test.go`: `sandbox-shell
  --memory 536870912 --processes <P> -- <file>`, where the file is executable and holds bytes no
  kernel loads, writes `sandbox-shell: cannot start <file>: ` and the errno and exits 125, under
  `-race` too.
- `TestTheFilterRefusesTracing`, in `seccomp_linux_test.go`, on `bpf.VM`: `ptrace`,
  `process_vm_readv`, `process_vm_writev`, `pidfd_getfd`, `kcmp` and `process_madvise` answer
  `EPERM`.
- `TestTheFilterRefusesTheKeyring`, in `seccomp_linux_test.go`, on `bpf.VM`: `keyctl`,
  `add_key` and `request_key` answer `EPERM`.
- `TestTheShellCannotReadTheKeyring`, in `seccomp_linux_test.go`: a helper in the test binary
  asks `keyctl(KEYCTL_GET_KEYRING_ID, KEY_SPEC_SESSION_KEYRING)`, which succeeds outside the
  filter (checked first, else skipped); under `sandbox-shell` it answers `EPERM`.
- `TestTheShellCannotTraceAProcess`, in `seccomp_linux_test.go`: a helper in the test binary
  starts a `sleep` child and calls `PtraceAttach` on it, which Yama allows a parent outside the
  filter (checked first, else skipped); under `sandbox-shell` it answers `EPERM`.
- `TestTheShellCannotTraceItsOwnChild`, in `sandbox_linux_test.go`: the same helper through the
  real sandbox answers `EPERM`. Without the filter Yama would allow it, so the test fails if the
  filter's rule goes.
- `TestEveryRunStartsAsTheForwarder`, in `sandbox_darwin_test.go`, in place of
  `TestARunWithASocketStartsAsTheForwarder`: a run with no socket is `<self> sandbox-init --
  <shell> …`, one with a socket carries `--socket` and `--port`, and one with limits `--limits`
  with its `processes` key and no memory key.
- `TestTheProfileRefusesSetuidPrograms`, in `sandbox_darwin_test.go`: the profile text holds the
  `deny process-exec` list after `(allow process-exec)`. Through the real sandbox, each of the
  four that exists on the machine exits 126 with *Operation not permitted*.
- `TestPsAndTopStillRun`, in `sandbox_darwin_test.go`: through the real sandbox,
  `ps -o pid= -p $$` prints the shell's own pid and `top -l 1 -n 0` exits 0.
- `TestNoOtherProcessIsRead`, changed: inside, the same command also runs `ps -o pid= -p $$`,
  and the test asserts its pid is printed, so a `ps` that cannot start fails it.
- `TestATaskPortIsRefused`, in `sandbox_darwin_test.go`: `/usr/bin/sample <a sleep's pid> 1`
  succeeds outside the sandbox (else skipped). Inside it exits nonzero and prints
  `cannot examine process <pid>`, the line task 1 saw when the task port was refused, so a
  `sample` that fails to start or to read a file does not pass.
- `TestAnIgnoredSIGXCPUOutlivesTheLimit`, in `policy_darwin_test.go`: with `CPUSeconds: 1`,
  `trap '' XCPU; while :; do :; done` is still running at seven seconds of CPU, read from its
  `ps -o time=`; the test then kills it. It pins the residual, so the day macOS enforces the
  hard limit the test fails and the residual comes off.
- `TestACPUSpinEndsWithSIGXCPU`, in `policy_unix_test.go`: with `CPUSeconds: 1`, `while :; do
  :; done` exits 152 within the test's own bound.
- `TestAnIgnoredSIGXCPUIsKilledAtTheHardLimit`, in `policy_linux_test.go`: with `CPUSeconds: 1`,
  `trap '' XCPU; while :; do :; done` exits 137, `SIGKILL`, at six seconds of CPU.
- `TestAllocatingPastTheMemoryLimitFails`, in `policy_linux_test.go`: with `MemoryBytes` 512 MiB,
  `dd if=/dev/zero of=/dev/null bs=1G count=1` fails saying memory is exhausted; with no limit it
  succeeds, so the test proves the limit.
- `TestOpeningPastTheFileLimitFails`, in `policy_unix_test.go`: with `OpenFiles: 64`, a helper
  in the test binary opens `/dev/null` until it cannot, fewer than 64 times, and the error is
  `EMFILE`.
- `TestAForkLoopStopsAtTheProcessLimit`, in `policy_linux_test.go`, on a kernel from 5.14 with
  the run's own namespace, not as root (else skipped): with `Processes: 128`, a helper starts
  `sleep` children until it cannot, fewer than 128 times, and the error is `EAGAIN`. It prints
  `full` and waits on its stdin. The test then starts `/bin/true` itself, which runs, since the
  limit is the run's, and closes the helper's stdin. The count is the run's namespace alone, so
  no other test moves it.
- `TestAForkLoopStopsUnderAMachineWideCount`, in `policy_unix_test.go`, on macOS and on Linux
  where the scan answers the base, not as root (else skipped): with `Processes` the user's count
  plus 512, the same helper stops with `EAGAIN` before 4096 children. The bound is loose because
  other tests move the count; the test proves a limit holds, not its value.
- `TestSudoCannotGainRoot`, in `policy_unix_test.go`: `sudo -n id -u` prints `0` outside the
  sandbox (else skipped), and inside it exits nonzero and prints no `0`, on both platforms.
- `TestTheCompiledArgumentsMatchTheGolden` (Linux) and `TestTheCompiledProfileMatchesTheGolden`
  (macOS), their goldens updated: the fixture's policy carries the Workspace limits; the
  argument list gains `--unshare-user-try`, `--limits` on `sandbox-init`, and `--memory` and
  `--processes` on `sandbox-shell`; the profile gains the setuid rule. The reviewer reads the
  diff, and nothing else may change.

**`bash`**

- `TestTheWorkspacePolicyHasTheLimits`: over the fake sandbox answering a base of N, a
  foreground run's limits are 605 times `runtime.NumCPU()`, 16 GiB, 4096 and N plus
  `processMargin(runtime.NumCPU())`.
- `TestABackgroundRunHasNoCPULimit`: the same, for a background run, has `CPUSeconds` 0 and the
  other three unchanged.
- `TestTheProcessMarginGrowsWithTheCPUs`, in `limits_linux_test.go`: `processMargin` is 1024
  for 1 and 8 CPUs and 4096 for 32.
- `TestABaseThatCannotBeReadFailsTheCall`: when the fake's `CountedProcesses` answers an error,
  the call fails with it, no run directory is made and nothing runs, in the foreground and the
  background.
- `TestASandboxedCommandRunsUnderTheLimits`, in `bash_unix_test.go`, through the real sandbox:
  `ulimit -t; ulimit -n; ulimit -c` prints 605 times `runtime.NumCPU()`, 4096 and 0, and
  `ulimit -u` prints the margin where the sandbox's `CountedProcesses` answers 0 and a number
  of at least the margin elsewhere. The test keys on that answer, never on the kernel's
  release, since a 5.14 kernel with user namespaces off scans.
- `TestNodeRunsUnderTheMemoryLimit`, in `bash_linux_test.go`, through the real sandbox, when
  `node` is on `PATH` (else skipped): `node -e 'new WebAssembly.Memory({initial: 1})'` exits 0.
- `TestThePromptSaysSudoDoesNotWork`: the sandbox prompt names `sudo`, the limits, and a thread
  that cannot be created.

## Security

This step narrows only:

- A sandboxed command's processes are bounded where they were bounded by the clock alone, or,
  in the background, not at all.
- `sudo`, `su`, `login` and `security_authtrampoline` cannot be started on macOS, where today
  they can.
- A process in the Linux sandbox cannot trace another, where today it can trace its own
  children.
- A process in the Linux sandbox cannot read or add a key in the user's keyring, where today it
  can.
- No sandboxed process leaves a core dump.
- Every Linux run has a user namespace of its own where the machine allows one, which a setuid
  bwrap did not give it.

No boundary moves, so no security record. `security-model.md` gains four rows, each with the
tests above: the limits, the setuid programs, tracing, and the keyring. The tracing row claims
what the filter refuses and names the `/proc` residual below.

The residuals:

- **Memory on macOS** is bounded by the wall clock alone, and in the background by nothing but
  Stop.
- **A background run has no CPU limit.** The user sees it running and stops it.
- **On macOS a process that ignores `SIGXCPU` outlives its CPU limit**, since XNU sends the
  signal once and enforces no hard limit. Such a process that also left the run's group through
  `posix_spawn`'s new session outlives the run, held by the profile and by nothing else.
- **The CPU limit is late for a single thread.** Scaled by the CPU count, it lets a
  single-threaded process that outlived the run spin for `MaxTimeout` plus `killGrace` times
  the CPU count, about 100 minutes on a 10-CPU Mac, before `SIGXCPU`.
- **A limit is per run**, so parallel runs multiply it: an answer's background commands and its
  agents' calls each get their own process margin and, in the foreground, their own CPU time
  per process.
- **`RLIMIT_CPU` is per process.** A fork loop under the process limit can spend the process
  limit times the CPU limit before the clock stops it. The clock stops it.
- **On a Linux machine with user namespaces off**, the base is the user's whole count, as on
  macOS.
- **The probe decides the namespace once.** If user namespaces are turned off while Kstack
  runs, later runs get none, `CountedProcesses` still answers 0, and the limit is the margin
  against the user's whole count: every fork in those runs fails until Kstack restarts. Turned
  on, the scan's base stays, which only loosens the limit.
- **A sidecar inside a container's PID namespace** scans short, since `/proc` there misses the
  user's tasks outside it. Where the kernel counts them, the limit sits below the real count and
  every fork in the run fails.
- **On macOS and wherever Linux scans, the base is read once.** A long background run whose
  user starts many programs later can find its margin gone and its forks refused.
- **A user running Kstack as root has no process limit**, since the kernel holds uid 0 to none.
- **A process can read its own descendants' `/proc/<pid>/mem` and `environ`** on Linux. Those
  are the run's own processes.
- **On macOS `ps` and `top` still run as root**, inside the profile, as they do today.
- **On macOS a setuid program off the list runs as root**, inside the profile: the system's
  others, such as `ARDAgent`, and third-party ones, such as VirtualBox's `VBoxNetAdpCtl` or a
  Homebrew `doas`.
- **A `core_pattern` pipe handler that ignores the core size** is the machine's own choice.

## When it lands

- **An ADR**: process limits are resource limits set inside the run, never on the sidecar; the
  process limit is a margin over a base, 0 in a Linux 5.14 namespace the probe confirmed as the
  run's own and the user's count elsewhere, not a cgroup; bwrap is told `--unshare-user-try`,
  so a machine with user namespaces off keeps its sandbox; on Linux `sandbox-shell` sets the
  process limit, since a thread counts and the Go forwarder would die under it, and on macOS
  the forwarder does; the margin is in the kernel's unit, 512 processes on macOS and 128 tasks
  per CPU, at least 1024, on Linux; the CPU limit is `MaxTimeout` plus `killGrace` times the
  CPU count, so it binds only a process that outlives the clock, and on macOS only one that
  does not ignore `SIGXCPU`; a background run has no CPU limit; memory is 16 GiB on Linux,
  which several WebAssembly memories exceed, with what the `java` and `pwsh` checks found, and
  bounded by the clock alone on macOS, where every process already maps past any useful
  `RLIMIT_AS`; memory on Linux is `sandbox-shell`'s to set; macOS names the setuid programs
  that escalate, since `file-mode` also caught setgid files and no later `allow` won `ps` and
  `top` back.
- **`sidecar/CLAUDE.md`**: the `Policy` paragraph gains `Limits`, its `Check` rules and
  `CountedProcesses`; the Linux paragraph gains `--unshare-user-try` and the probe's namespace
  check; the forwarder paragraph gains `--limits`, what it applies on each platform, the clamp
  and the core size; the `sandbox-shell` paragraph gains `--memory`, `--processes`, the raw
  `execve` and the nine refused calls; the macOS paragraph says the forwarder is every run's first
  process, the profile's setuid rule and the 5 s probe; the Bash paragraph gains the Workspace
  policy's limits, the per-platform margin, the base read first, CPU in the foreground alone,
  and the prompt's three sentences.
- **`security-model.md`**: the four rows above.
- **The note's *Where this meets the code***: decision 16 says the CPU limit is `MaxTimeout`
  plus `killGrace` times the CPU count and holds on macOS only for a process that does not
  ignore `SIGXCPU`, and that macOS checks `RLIMIT_AS` but no process can set a useful one;
  decision 18 says macOS refuses the setuid programs that escalate, not every setuid program.
  Both are written already; check they still match what landed.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), with the sandbox's tests on
Linux and in CI's macOS job, `-race` included.

By hand, `pnpm tauri dev` on macOS and on Linux. Ask for
`ulimit -t; ulimit -n; ulimit -u; ulimit -c`:

- `-t` prints 605 times your CPU count; in a background command it prints `unlimited`.
- `-n` prints 4096 and `-c` prints 0.
- `-u` prints the larger of 1024 and 128 times your CPU count on Linux from 5.14 with user
  namespaces on. On macOS it prints a number 512 past your process count, and on older Linux
  one that margin past your thread count.

On Linux ask for `ulimit -v`, which should print 16777216, and for
`node -e 'new WebAssembly.Memory({initial: 1})'`, which should exit 0 where `node` is installed.

Ask for `sudo -n id -u`, which should fail on both, with *Operation not permitted* on macOS,
and on macOS for `ps -o pid= -p $$`, which should print a pid. On
Linux ask for `sleep 30 & strace -p $!`, which should fail with *Operation not permitted*, and
for `keyctl show @s`, which should fail where `keyctl` is installed; on macOS ask for
`sample $KSTACK_SIDECAR_PID 1`, which should fail. A sandboxed `kubectl get ns` and `helm list`
should behave as before. On a Linux machine with at least 64 GiB, so should `java -version`
and `pwsh -c 1` where they are installed: each reserves address space for its heap at start,
and the ADR records whether it runs at 16 GiB.
