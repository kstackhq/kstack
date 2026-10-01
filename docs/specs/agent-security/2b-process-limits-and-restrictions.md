---
title: Process limits and restrictions
scope: sidecar
status: Planned
---

# Process limits and restrictions

**Needs:** step 1A, whose `Policy` this step extends. **Unblocks:** nothing directly.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command is bounded in time alone: the call's wall-clock timeout, capped at
`MaxTimeout` (600 s), and the process-group kill that follows it. Nothing bounds what the
command's processes use while the clock runs. A `while :; do :; done` under a fork, or a
`kubectl get --watch` that leaks descriptors, can take the machine for as long as the timeout
allows. Of the note's *Process and kernel restrictions*, two hold on one platform and not the
other: Linux has `no_new_privs`, macOS lets `sudo` be started; macOS reads no other process's
information, Linux's filter leaves `ptrace` open.

After this step the note's three remaining bullets are true of the code:

- *"Resource limits per command: CPU time, memory, open files, process count, and a wall-clock
  timeout."* A run carries **`Policy.Limits`**, set by Bash's Workspace policy: 600 s of CPU
  time, 8 GiB of address space (Linux), 4096 open files, and the user's own process count plus
  256, counted as the kernel counts it: threads on Linux, processes on macOS. The run's first process, `sandbox-init`, sets them with `setrlimit` before it starts the
  shell, so they hold the shell and everything under it and never the sidecar. On macOS the
  forwarder becomes every run's first process, as it is on Linux.
- *"No privilege escalation: `no_new_privs` on Linux; deny execution of `sudo` and other setuid
  binaries explicitly on both platforms."* Linux has `no_new_privs`. macOS gains a fixed rule in
  `profile_darwin.sb` refusing `sudo`, `su`, `login` and `doas`.
- *"No `ptrace` or `task_for_pid`; no reading other processes' environment."* Linux's filter
  answers `EPERM` to `ptrace`, `process_vm_readv` and `process_vm_writev`. macOS's profile
  already refuses the task port and other processes' information.

The wall-clock timeout and the group kill stay as they are. A limit is a floor under a runaway
command, not a gate: nothing asks, and nothing is logged that is not logged today. It is a step
of [the sandbox, credentials and permissions note](../../notes/sandbox-credentials-and-permissions.md).

## What is not in this step

- **No setting.** The values are constants in `tools/bash`. No step of the sequence puts them in
  `sandboxconfig`; one may, once a user needs to.
- **No limit outside the sandbox.** A command the user switched out of the sandbox (step 1B) runs
  as the user, with the user's limits, as today.
- **No limit on the login shell.** Step 4A builds its policy and may set `Limits` there the same
  way.
- **No cgroup.** A process count and a memory bound through a cgroup need a delegated one, which
  a desktop session does not give an unprivileged app (§8).
- **No change on Windows**, which has no sandbox.

## Design

### 1. `Policy.Limits`

`sandbox/policy.go`:

```go
// Limits bounds what a run's processes may use. Zero is the platform's
// default for that resource. Each holds per process, as the kernel counts
// it, but Processes, which counts everything the user's real uid runs:
// every thread on Linux, every process on macOS.
type Limits struct {
	CPUSeconds  int // CPU time per process
	MemoryBytes int // address space per process; Linux alone
	OpenFiles   int // open descriptors per process
	Processes   int // the user's threads (Linux) or processes (macOS), all told
}
```

`Policy` gains `Limits Limits`. `Check` refuses a negative limit.

Each limit is one resource limit, set soft and hard so a process cannot raise it back:

| Limit | Resource | Soft | Hard | Past it |
| --- | --- | --- | --- | --- |
| `CPUSeconds` | `RLIMIT_CPU` | the value | the value plus `cpuGrace` (5 s) | `SIGXCPU`, then `SIGKILL` at the hard limit |
| `MemoryBytes` | `RLIMIT_AS` | the value | the value | `mmap` and `brk` fail with `ENOMEM` |
| `OpenFiles` | `RLIMIT_NOFILE` | the value | the value | `open` fails with `EMFILE` |
| `Processes` | `RLIMIT_NPROC` | the value | the value | `fork` fails with `EAGAIN` |

The CPU limit has a grace for the same reason the timeout does: a process that traps `SIGXCPU`
can say something, and the kernel kills it five seconds of CPU later whatever it does. A shell
whose child was killed by `SIGXCPU` exits 152 (`ExitCode`: 128 plus the signal).

`MemoryBytes` is Linux's alone: macOS accepts `RLIMIT_AS` and enforces nothing, so the macOS
compiler passes it to nothing (§8). `RLIMIT_NPROC` counts the user's processes on the whole
machine, not the run's, so `Processes` is an absolute count and Bash computes it (§2). Linux
counts tasks, so every thread of a process is one against the limit; macOS counts processes.
Both count by the real uid.

**`sandbox.UserProcesses() (int, error)`** answers the count the kernel holds the limit against:
`procs_linux.go` reads `/proc/<pid>/status` for each process and sums the `Threads:` line of
those whose `Uid:` line starts with `os.Getuid()`, the real uid. It never reads the owner of
`/proc/<pid>`, which is the effective uid, and root for a non-dumpable process such as the
forwarder. A process that exits between the listing and the read is skipped. `procs_darwin.go`
reads `kern.proc.ruid.<uid>` through `unix.SysctlKinfoProcSlice`, `procs_windows.go` answers
`errNone`. It is read outside the sandbox: inside, Linux's `/proc` shows the run's own processes
alone.

### 2. The Workspace policy's limits

`sandboxedRunFor` in `tools/bash/bash.go` sets `Limits` on the policy it builds (`workspacePolicy`):

| Limit | Value | Why |
| --- | --- | --- |
| `CPUSeconds` | `MaxTimeout` in seconds, 600 | No process may use more CPU than the longest call may last. |
| `MemoryBytes` | 8 GiB | Room for `aws`, `gcloud` and a large `helm template`; not for a leak. |
| `OpenFiles` | 4096 | The system's usual default; `kubectl` and `helm` need far fewer. |
| `Processes` | `UserProcesses()` plus 256 | A fork loop stops before the user's other programs cannot start. |

The count is read by each call, through the `sandboxer` seam, which gains `UserProcesses`, so
Bash's fake answers it. A count that cannot be read fails the call with the error, and nothing
runs: as with a policy that fails `Check`, a run is never started with a limit other than its
policy's. The constants are `limitCPU`, `limitMemory`, `limitOpenFiles` and
`limitProcessMargin` in `bash.go`.

### 3. Where the limits are applied

**The forwarder applies CPU, files and processes, on both platforms.** `ForwarderArgs`
(`sandbox/forward.go`) writes one argument after the socket and port:

```
sandbox-init [--socket <S> --port <P>] [--limits cpu=600,files=4096,processes=1300] -- <argv…>
```

Every key is present, `0` for the platform's default; the flag is left out when every limit is
zero, so a probe's line is what it is today. `parseInitArgs` reads it into `initArgs.limits`:
an unknown key, a missing one, or a value that is not a non-negative integer is *bad arguments*,
exit 125. `InitMain` (`forward_unix.go`) applies each nonzero one with `unix.Setrlimit` after it
listens and before `ForkExec`, so the child inherits them. **Each is set to the lower of the
run's value and the forwarder's own hard limit**: an unprivileged process cannot raise a hard
limit, the run's value is a ceiling, and a machine whose limit is stricter stays stricter. A
kernel that refuses the call anyway is *cannot set limits*, exit 125. `unix.Setrlimit` on
`RLIMIT_NOFILE` goes through `syscall.Setrlimit`, so the runtime's own copy of that limit, the
one it hands an exec'd child, is the value set.

The forwarder runs under them too. That is fine: it forks once, relays a few dozen connections
and spends no CPU.

**`sandbox-shell` applies memory, on Linux.** `Sandbox.args` (`sandbox_linux.go`) writes
`--memory <bytes>` before the `--` of `sandbox-shell` when `MemoryBytes` is set, `parseShellArgs`
(`shell.go`) reads it, and `ShellMain` (`seccomp_linux.go`) sets `RLIMIT_AS` as its last act
before `unix.Exec`, after `no_new_privs` and the filter. The forwarder does not set it: it is a
Go program that keeps running to relay, and it must keep its address space (§8).

**On macOS the forwarder is every run's first process.** `Sandbox.argv` (`sandbox_darwin.go`)
answers `<self> sandbox-init … -- <shell> <args…>` for every run, a run with no socket included;
today it does so only for a run with one. `sandbox-exec` execs into the forwarder, so the profile
holds it and the shell under it, as today. The probe's run then starts this executable once more,
so `probeTimeout` becomes 5 s, as Linux's `probeBound` is for the same reason.

### 4. `sudo` and setuid

Linux is done: `sandbox-shell` sets `PR_SET_NO_NEW_PRIVS` before it execs the shell, so a setuid
bit under it grants nothing, and `sudo` says so and exits 1.

macOS gains one fixed rule in `profile_darwin.sb`, after `(allow process-exec)` and before the
policy's rules, so no rule of a policy undoes it, like everything else the compiler holds fixed:

```scheme
;; No privilege escalation. Seatbelt cannot name "setuid", so this names the
;; programs that escalate (TestSudoCannotGainRoot).
(deny process-exec
  (literal "/usr/bin/sudo")
  (literal "/usr/bin/su")
  (literal "/usr/bin/login")
  (literal "/usr/bin/doas"))
```

The shell reports a refused exec as *Operation not permitted*, exit 126. Seatbelt has no filter
for a program's setuid bit, so this list is what the note's "explicitly" means: the programs
that exist to escalate, at the paths the system installs them. A setuid program elsewhere is a
residual (Security).

### 5. `ptrace`

`filter()` in `seccomp_linux.go` answers `EPERM` to `ptrace`, `process_vm_readv` and
`process_vm_writev`, beside the sockets and namespaces it refuses today. A command in the sandbox
cannot attach to, single-step or read the memory of any process, its own children included, so
`strace` and `gdb` do not run there. The forwarder is outside the filter, and stays non-dumpable
(`guardMemory`), so nothing under the filter reads its memory or environment either way.

macOS needs no new rule. `task_for_pid` needs the `mach-priv-task-port` operation and
`task_name_for_pid` the `mach-task-name` one, and `(deny default)` refuses both; `process-info*`
is allowed for `(target same-sandbox)` alone, so `ps`, `proc_pidinfo` and `sysctl kern.proc`
see the run's own processes and nothing else. `TestNoOtherProcessIsRead` pins the information
half today; this step adds a test for the task port.

### 6. What stays

The wall-clock timeout, `killGrace`, the group kill after every run and the cancel's immediate
`SIGKILL` in `tools/bash` are unchanged: the clock bounds the run, the limits bound each process.
Windows is unchanged too; `forward_windows.go` answers 125 as today.

### 7. What the model is told, and what the user sees

`prompts/sandbox.md` gains two sentences: `sudo` does not work in the sandbox; and a command's
processes are limited in CPU time (600 s each), memory, open files and count, so a process past
its CPU time is killed, exit 152. The user sees nothing new: a killed command's disclosure shows
its exit code, as today.

### 8. Decisions this step asks for

**Memory on macOS is bounded by the wall clock alone.** macOS enforces neither `RLIMIT_AS` nor
`RLIMIT_DATA` nor `RLIMIT_RSS`; a Jetsam limit needs an entitlement or root. So a macOS run's
memory is bounded by the timeout and the group kill, and the macOS compiler passes
`MemoryBytes` to nothing rather than set a limit that holds nothing. Recommended: accept it and
record it in the ADR.

**The process limit is the user's count plus 256, read when the run is built.** `RLIMIT_NPROC`
counts every process of the user, and on Linux every thread, so a fixed value would either
refuse the shell on a busy machine or allow a fork loop thousands of processes on a quiet one.
A desktop's browsers and editors run thousands of threads, so the count is read the kernel's
way; a count of processes alone would set the limit below what the user already runs. A margin over the count at
the run's start does neither. The count can move between the read and the shell's start; the
margin covers it, and a fork that fails anyway is *cannot start*, exit 125, which the model
reads. The alternative, a cgroup's `pids.max`, needs a cgroup delegated to the app, which
systemd's user session gives no unprivileged program by default, and which bwrap's
`--unshare-cgroup-try` does not make. Rejected.

**Memory is `sandbox-shell`'s to set, not the forwarder's.** The forwarder keeps running after
the shell starts, and a Go program under an address-space limit fails on its next arena; the
test binaries CI runs as the forwarder under `-race` map far more than 8 GiB before `main`.
`sandbox-shell` sets the limit and execs, so nothing of Kstack's runs under it. macOS enforces
no memory limit, so it has no such process and needs none.

**The CPU limit has a five-second grace.** Soft at the value, hard five seconds above it, so a
process that traps `SIGXCPU` is killed anyway.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Limits` on `Policy`, `Check`, `UserProcesses` | `sandbox/policy.go`, `sandbox/procs_linux.go`, `sandbox/procs_darwin.go`, `sandbox/procs_windows.go`, their tests | — | Planned |
| 2 | The forwarder: `--limits`, the lower-of rule, every macOS run first; the probe's bound | `sandbox/forward.go`, `sandbox/forward_unix.go`, `sandbox/sandbox_darwin.go`, their tests | 1 | Planned |
| 3 | `sandbox-shell`: `--memory`; `ptrace` in the filter | `sandbox/shell.go`, `sandbox/seccomp_linux.go`, `sandbox/sandbox_linux.go`, their tests | 1 | Planned |
| 4 | The profile's setuid rule; the task-port test | `sandbox/profile_darwin.sb`, `sandbox/sandbox_darwin_test.go` | — | Planned |
| 5 | Bash sets the limits; the `sandboxer` seam; the prompt | `tools/bash/bash.go`, `tools/bash/prompts/sandbox.md`, their tests | 1 | Planned |
| 6 | The real-sandbox tests and the goldens | `sandbox/policy_unix_test.go`, `sandbox/policy_linux_test.go`, `sandbox/testdata/` | 2, 3, 4 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1, then 2, 3 and 4 at the same time, then 5, then 6, then 7.

## Tests

Every test through the real sandbox calls `confining(t)`. A test that first checks a program
outside the sandbox, to prove the sandbox is what refuses it, skips when that check fails.

**`sandbox`**

- `TestLimitsAreChecked`: a negative limit fails `Check`; zero passes.
- `TestUserProcessesCountsTheUsers`, in `procs_unix_test.go`: with a `sleep` child running, the
  count is at least two, the test and its child.
- `TestUserProcessesCountsThreads`, in `procs_linux_test.go`: a helper in the test binary that
  holds 64 threads and makes itself non-dumpable raises the count by at least 64, though
  `/proc/<its pid>` is owned by root.
- `TestForwarderArgsWriteTheLimits`: `--limits` is written with every key when any limit is set,
  and left out when none is; `TestForwarderArgsRoundTrip` covers the new flag.
- `TestInitArgsReadTheLimits`: the value parses; an unknown key, a missing key, a negative value
  and a value that is not an integer are refused.
- `TestInitAppliesTheLimitsToItsChild`, in `forward_unix_test.go`: `sandbox-init --limits
  cpu=7,files=64,processes=<count+8> -- sh -c 'ulimit -t; ulimit -n; ulimit -u'` prints the three
  values.
- `TestInitLowersALimitToItsOwnHardOne`, in `forward_unix_test.go`: started by `sh -c 'ulimit -H
  -n 512; exec …'` with `files=4096`, the child's `ulimit -n` prints 512.
- `TestShellArgsReadTheMemoryLimit`: `--memory <n>` parses before the `--`; a value that is not
  a non-negative integer is refused.
- `TestTheShellAppliesTheMemoryLimit`, in `seccomp_linux_test.go`: `sandbox-shell --memory
  536870912 -- sh -c 'ulimit -v'` prints 524288.
- `TestTheFilterRefusesPtrace`, in `seccomp_linux_test.go`, on `bpf.VM`: `ptrace`,
  `process_vm_readv` and `process_vm_writev` answer `EPERM`.
- `TestTheShellCannotTraceAProcess`, in `seccomp_linux_test.go`: a helper in the test binary
  starts a `sleep` child and calls `PtraceAttach` on it, which Yama allows a parent outside the
  filter (checked first, else skipped); under `sandbox-shell` it answers `EPERM`.
- `TestTheFirstProcessCannotBeTraced`, in `sandbox_linux_test.go`: the same helper through the
  real sandbox, attaching to pid 1, answers `EPERM`.
- `TestEveryRunStartsAsTheForwarder`, in `sandbox_darwin_test.go`, in place of
  `TestARunWithASocketStartsAsTheForwarder`: a run with no socket is `<self> sandbox-init --
  <shell> …`, one with a socket carries `--socket` and `--port`, and one with limits `--limits`
  without a memory key.
- `TestTheProfileRefusesTheSetuidPrograms`, in `sandbox_darwin_test.go`: the profile text holds
  the `deny process-exec` rule naming the four programs after `(allow process-exec)`, and each
  of the four that exists on the machine exits 126 through the real sandbox.
- `TestATaskPortIsRefused`, in `sandbox_darwin_test.go`: `/usr/bin/sample <the test's pid> 1`
  succeeds outside the sandbox (else skipped) and fails inside it.
- `TestACPUSpinEndsWithSIGXCPU`, in `policy_unix_test.go`: with `CPUSeconds: 1`, `while :; do
  :; done` exits 152 within the test's own bound.
- `TestAllocatingPastTheMemoryLimitFails`, in `policy_linux_test.go`: with `MemoryBytes` 512 MiB,
  `dd if=/dev/zero of=/dev/null bs=1G count=1` fails saying memory is exhausted; with no limit it
  succeeds, so the test proves the limit.
- `TestOpeningPastTheFileLimitFails`, in `policy_unix_test.go`: with `OpenFiles: 64`, a helper
  in the test binary opens `/dev/null` until it cannot, fewer than 64 times, and the error is
  `EMFILE`.
- `TestAForkLoopStopsAtTheProcessLimit`, in `policy_unix_test.go`: with `Processes` the count
  plus 8, a helper starts `sleep` children until it cannot, fewer than 512 times (the count can
  fall while it runs, as other tests end), the error is `EAGAIN`, and it prints `full` and waits
  on its stdin. The test then starts `/bin/true` itself, which runs, since the limit is the
  run's, and closes the helper's stdin.
- `TestSudoCannotGainRoot`, in `policy_unix_test.go`: `sudo -n id -u` prints `0` outside the
  sandbox (else skipped), and inside it exits nonzero and prints no `0`, on both platforms.
- `TestTheCompiledArgumentsMatchTheGolden` (Linux) and `TestTheCompiledProfileMatchesTheGolden`
  (macOS), their goldens updated: the argument list gains `--limits` on `sandbox-init` and
  `--memory` on `sandbox-shell`; the profile gains the one `deny process-exec` rule. The
  reviewer reads the diff, and nothing else may change.

**`bash`**

- `TestTheWorkspacePolicyHasTheLimits`: over the fake sandbox answering a count of N, the
  policy's limits are 600, 8 GiB, 4096 and N plus 256.
- `TestACountThatCannotBeReadFailsTheCall`: when the fake's `UserProcesses` answers an error,
  the call fails with it and nothing runs, in the foreground and the background.
- `TestASandboxedCommandRunsUnderTheLimits`, in `bash_unix_test.go`, through the real sandbox:
  `ulimit -t; ulimit -n; ulimit -u` prints 600, 4096 and at least 256.
- `TestThePromptSaysSudoDoesNotWork`: the sandbox prompt names `sudo` and the limits.

## Security

This step narrows only. A sandboxed command's processes are bounded where they were bounded by
the clock alone; `sudo` cannot be started on macOS where today it can; a process in the Linux
sandbox cannot trace or read another where today it can trace its own children. No boundary
moves, so no security record. `security-model.md` gains three rows, each with the tests above:
the limits, the setuid programs, and tracing.

The residuals:

- **Memory on macOS** is bounded by the wall clock alone (§8).
- **A limit is per run**, so parallel runs multiply it: an answer's background commands and its
  agents' calls each get their own 600 s of CPU per process and their own 256 processes.
- **`RLIMIT_CPU` is per process.** A fork loop under the process limit can spend the process
  limit times 600 s of CPU before the clock stops it. The clock stops it.
- **A setuid program off the list** on macOS, such as a Homebrew `doas`, is not refused by the
  profile.

## When it lands

- **An ADR**: process limits are resource limits set by the run's first process; the process
  limit is the user's count plus a margin, not a cgroup; memory on macOS is bounded by the clock
  alone; memory on Linux is `sandbox-shell`'s to set.
- **`sidecar/CLAUDE.md`**: the `Policy` paragraph gains `Limits` and `UserProcesses`; the
  forwarder paragraph gains `--limits`, what it applies and the lower-of rule; the
  `sandbox-shell` paragraph gains `--memory` and `ptrace`; the macOS paragraph says the
  forwarder is every run's first process, the profile's setuid rule and the 5 s probe; the Bash
  paragraph gains the Workspace policy's limits and the prompt's two sentences.
- **`security-model.md`**: the three rows above.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), with the sandbox's tests on
Linux and in CI's macOS job, `-race` included.

By hand, `pnpm tauri dev` on macOS and on Linux. Ask for `ulimit -t; ulimit -n; ulimit -u`, which
should print 600, 4096 and a number past your own process count (thread count, on Linux), and on Linux `ulimit -v`, which
should print 8388608. Ask for `sudo -n id -u`, which should fail on both, with *Operation not
permitted* on macOS. On Linux ask for `strace -p 1`, which should fail with *Operation not
permitted*; on macOS ask for `sample $KSTACK_SIDECAR_PID 1`, which should fail. A sandboxed
`kubectl get ns` and `helm list` should behave as before.
