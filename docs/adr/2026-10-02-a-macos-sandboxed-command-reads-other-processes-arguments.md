---
title: Accept that a sandboxed command on macOS reads other processes' arguments
date: 2026-10-02
scope: sidecar
status: Accepted
---

# Accept that a sandboxed command on macOS reads other processes' arguments

## Context

A command the sandbox confines runs without asking, and what it prints reaches the model and then
the provider. On Linux the run is a PID namespace of its own, so it sees no process outside it. On
macOS there is no PID namespace: the Seatbelt profile allows process information only within the
run (`process-info* (target same-sandbox)`), but `kern.procargs2`, which `ps` reads, still answers
for any of the user's processes. So a sandboxed command can read another process's command line,
and a secret passed on one (`mysql -p…`, `curl -H "Authorization: …"`) can reach the model.

Before macOS 27 the read also returns another process's environment, where credentials usually
ride: CI on macOS 15.7 and 26.6, Apple silicon and Intel, reads it from inside the sandbox. macOS
27 withholds it. That half is not accepted here; it is an open gap in `docs/TODO.md`.

No profile can close either half before macOS 27, because Seatbelt is never asked about the read.
Under `(allow default)` plus one deny at a time, CI read another process's arguments and
environment under each of `(deny process-info*)`, `(deny sysctl-read)` whole,
`(deny sysctl-read (sysctl-name "kern.procargs2"))` and
`(deny sysctl-read (sysctl-name-prefix "kern.procargs"))`. With `sysctl-read` denied, the
Sandbox log recorded denials for `kern.bootargs`, `kern.hostname` and the like, and none for this
read. Formwork's characterization reached the same verdict on macOS 14 and 15 and calls the
environment unenforceable there
([C5](https://github.com/brianv0/formwork/blob/main/docs/macos-characterization.md)), and the same
report against Claude Code's sandbox is open
([anthropics/sandbox-runtime#160](https://github.com/anthropics/sandbox-runtime/issues/160)).

## Decision

We accept the arguments. A sandboxed command on macOS reads the arguments of the user's
processes, `TestAnotherProcessesArgumentsAreRead` pins it, and the row in
`docs/security-model.md` is **By decision**. The environment before macOS 27 has a row of its own
there, **Not built**.

Unix treats a command line as public: on Linux every user reads `/proc/<pid>/cmdline` unless
`/proc` is mounted `hidepid`, and on macOS every process of the same user reads it. Tools warn
against passing a secret there for that reason. The sandbox reaches no network but the chat's
cluster, and each change to the cluster asks, so what a command reads leaves the machine only
through the model.

## Alternatives considered

- **Deny `kern.procargs` and `kern.procargs2` by name** (`sysctl-read` with `sysctl-name` or
  `sysctl-name-prefix`). It does not refuse them on macOS 15, 26 or 27.0.1: the read still
  succeeds.
- **Deny `sysctl-read`, whole or but for an allowlist of names.** It does not refuse the read on
  macOS 15 or 26, since the read is not a `sysctl-read` check there. An allowlist is narrower
  still than the whole deny, so a profile that claims to close the read with one is not relied on.
- **Deny `process-info*` outright.** On macOS 27.0.1 it refuses the read, and also stops every Go
  program, which traps at start when it cannot read its own process information. On macOS 15 and
  26 it does not refuse the read.
- **Hide the run's processes from the rest some other way.** macOS has no PID namespace, and no
  rule in the profile reaches this read.

## Consequences

- A secret on the command line of any of the user's processes is readable by a command that runs
  without asking, and can reach the provider. The macOS sandbox is weaker than Linux's here.
- On macOS 15 and 26 the same read returns the environment, which this decision does not cover
  and no profile can close. What remains is to keep secrets out of the exec-time environment of
  Kstack's own processes, which a process can do for itself: one that overwrites its exec-time
  strings is read as blank (Formwork's C5), and one that execs again without a variable reports
  only the environment of the new exec.
- If a later macOS refuses the read, `TestAnotherProcessesArgumentsAreRead` fails, and its
  assertion and the security model's row move to **Enforced** together.

## Revisit when

A Seatbelt rule refuses `kern.procargs2` for processes outside the run, or a secret on a command
line turns out to be common in what Kstack's users run beside it.
