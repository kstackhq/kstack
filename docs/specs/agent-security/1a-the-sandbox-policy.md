---
title: The sandbox policy
scope: sidecar
status: Done
---

# The sandbox policy

**Needs:** nothing beyond `main`. **Unblocks:** every later step; 2A, 2B, 4A and 4D build on the
`Policy` directly.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today, a sandboxed run describes what it may reach with fields that each have one purpose:
`Workspace`, `Readable`, `Writable`, `Home`, `Denied`, `Port` and `Socket`. Linux and macOS then
each add rules of their own on top. To know what a sandboxed command can reach, you have to read
both platforms' code.

This step replaces those fields with one value, a **policy**: everything the OS sandbox enforces
for a run, in one place. It has three parts in this step:

- **Files**: three lists of paths. **Read**: the command can read these, and not write them.
  **Write**: it can read and write these. **Deny**: it can do neither.
- **Always**: the paths no Files rule and no user grant ever opens: the denied-always list of
  credentials and private files, and Kstack's data, cache and runtime directories. Beside them,
  the run's own paths inside Kstack's directories, which it can reach. Nothing else reaches
  inside an Always path, and nothing at all reaches inside the denied-always list.
- **Network**: which loopback ports the run's forwarder relays to a socket outside. Today that is
  the cluster proxy, when the chat has a cluster. Nothing else reaches the network.

Each platform turns the policy into its own sandbox settings. Neither platform needs to know what
a path or a relay is for.

The paths that differ between macOS and Linux, like `/System` or the Keychain, live in one file
per platform, beside one file of the paths both share.

**This step changes no behavior.** A sandboxed command reaches exactly what it reaches today,
less one path a Deny missed on macOS and a `PATH` entry that covered a fixed mount on Linux, and
with a missing Read or Write path skipped rather than failing or made (§5). The
point is to make the next steps simple: step 2A changes how the base rules are built, step 4A
builds a policy for the login shell, step 2B adds limits, and step 4D adds the user's grants, each
from the same parts. It is the first step of
[the sandbox, credentials and permissions note](../../notes/sandbox-credentials-and-permissions.md).

**Both platforms answer alike.** One table of rule pairs runs through the real sandbox on Linux
and on macOS, and both must give the answers the rules below give. The goldens of §8 show that
the refactor changed nothing; the table shows that the rules hold.

## What is not in this step

- **No new kind of permission.** Limits, Unix sockets and the egress relay arrive with the steps
  that first need them (see §1).
- **No change to what is on the lists.** The note's longer denied-always list, and its zones, are
  step 2A's. This step moves today's lists into the new shape.
- **No change on Windows**, which has no sandbox.

## Design

### How file rules combine

A rule covers a path and everything under it, and decides everything a command may do there:
Read lets it read and not write, Write lets it read and write, and Deny lets it do neither. When
more than one Files rule covers a path:

- **The deepest rule wins.**
- **If two rules name the same path, the narrower one wins**: Deny, then Read, then Write.
- **Nothing sits beneath a Write rule**, neither a Files rule nor an Always path. A command that
  can write a folder can rename what is in it, and on macOS a rule for a path inside it would
  then match nothing: a Write rule on `~/.config` would let a command move `gcloud` out from under
  its Deny. `Check` refuses such a policy. Beneath means strictly inside: a rule on the Write
  rule's own path is a tie, decided by the rule above, and renames nothing away.
- **A path no rule covers is off limits.**

For example:

| Rule | Path |
| --- | --- |
| Read | `/opt/homebrew` (Homebrew's programs) |
| Deny | `/opt/homebrew/var` (its services' databases) |

The command can read and run Homebrew's programs, and cannot read its databases, because the Deny
rule is deeper. It can write neither.

**The Always part is not a Deny rule.** A Files Deny has one meaning: a deeper Read or Write
opens a path beneath it. An Always path is closed whatever Files says: inside Kstack's
directories only the run's own paths open anything, and inside the denied-always list nothing
does. That is the note's single most important invariant: a user
who grants `~` (step 4D) does not expose `~/.ssh`, because `~/.ssh` is an Always path and the
grant is a Files rule. `Check` refuses a Files rule inside an Always path, and the table of cases
runs a Read rule over an Always path on both platforms and reads nothing.

### 1. The `Policy` type

`sandbox/policy.go`:

```go
// Policy is everything the OS sandbox enforces for a run.
type Policy struct {
	Files   FilePolicy
	Always  AlwaysPolicy
	Network NetworkPolicy
}

// FilePolicy is what a run may do with files. A rule covers a path and
// everything under it, and decides all a run may do there. The deepest rule
// wins, the narrower rule wins a tie, and nothing sits beneath a Write rule. A
// path no rule covers is off limits.
type FilePolicy struct {
	Read  []string // readable, not writable
	Write []string // readable and writable
	Deny  []string // neither
}

// AlwaysPolicy is what no FilePolicy rule opens: the denied-always list, where
// nothing opens, and Kstack's own directories, where only the run's own paths
// open.
type AlwaysPolicy struct {
	Deny   []string // the denied-always list
	Kstack []string // the data, cache and runtime directories
	Read   []string // the run's own paths it reads, each inside a Kstack path
	Write  []string // and reads and writes
}

// NetworkPolicy is how a run reaches past the machine. The zero value is no
// network at all.
type NetworkPolicy struct {
	Relays []Relay
}

// Relay is a loopback port inside the run that the forwarder connects to a
// Unix socket outside it.
type Relay struct {
	Port   int
	Socket string
}
```

**Naming.** A part of the policy is named for what it covers, with a `Policy` suffix, since each
part is also used on its own: each platform turns a `FilePolicy` into mounts or Seatbelt rules.
`NetworkPolicy` is the sandbox's, not Kubernetes': in code the package keeps them apart
(`sandbox.NetworkPolicy`), and docs say "the sandbox's network policy".

**Where the policy is going.** Later steps add fields as they first need them:

| Field | Added by | What it holds |
| --- | --- | --- |
| a second `Relay` | step 4C | the egress proxy, which checks each host |
| `Policy.Limits` | step 2B | CPU time, memory, open files and process count |

No policy gives a run the machine's network: a run's network is always its relays. Host rules and
cluster rules are not in `Policy`. The OS cannot check a hostname or a Kubernetes request, so
Kstack's proxies do.

`Policy.Check` rejects:

- a path that is not absolute;
- any rule or Always path beneath a Write rule, Files or Always;
- a Files rule on or inside an Always path (a Deny or a Kstack path);
- an Always Read or Write path that is not inside a Kstack path, or that is on or inside a Deny
  path;
- more than one relay, until step 4C teaches the forwarder several.

"Always path" means a Deny or a Kstack path; the run's own Read and Write paths are named as
such.

`Command` gains an error result. It answers an error, and starts nothing, for a policy that fails
`Check` or that its platform cannot enforce, and the call fails with that error, which the model
reads. A run is never made narrower or wider than its policy to get it started. On macOS a
context that ends while the profile is built, which today answers a command whose `Start` fails,
is `Command`'s error too. On Windows
`Command` answers `errNone`, which today's command carries in `cmd.Err`, and makes no command.
Bash's `sandboxer` interface and its tests' `fakeSandboxer` take the new signature.

Both sandboxes check a file at its real location, so every path is compared after following
symlinks; a path that does not exist is resolved through its deepest folder that does. A policy
holds each path as its builder wrote it, and `Check` and the compilers resolve it, since Linux
recreates a link at the path as written (§7).

A Read or Write rule whose path does not exist opens nothing, and is skipped. Linux already
skips a missing root, `PATH` tree or denial; today only a missing workspace, `Readable` or
`Writable` path fails bwrap, and on macOS a run can make a missing Write path. A run whose directory is missing still does not start: Bash's
`checkDir` refuses it first, and bwrap's `--chdir` fails after. A missing workspace cannot
happen, since Bash makes it before the policy.

A Deny rule, Files or Always, is never skipped for being missing:

- On macOS it holds for its path whether or not the path exists.
- On Linux a mount needs something to cover, so a Deny whose path is missing when the run starts
  covers nothing. It lies beneath a Read rule or nowhere reachable, since nothing sits beneath a
  Write rule, so the run cannot make that path; a file something outside the run makes there
  while it runs is visible to it. This is a residual (see *Security*).

A compiler may still leave out a Deny that no Read or Write rule reaches, since its path is off
limits anyway (§7).

`sandbox.Run` drops `Workspace`, `Readable`, `Writable`, `Home`, `Denied`, `Port` and `Socket`,
and gains a `Policy` field instead.

### 2. The lists, shared and per platform

The zones are the same on macOS and Linux. The folders under them are not: `/System` exists only
on macOS, `/lib` only on Linux, and the Keychain and the GNOME keyring sit in different places. So
every list of paths comes in two parts:

- **`sandbox/lists.go`**: what is the same on both platforms.
- **`sandbox/lists_darwin.go`** and **`sandbox/lists_linux.go`**: what is only on one.

Each file holds one value of the same type, so the three read alike:

```go
// Lists is what the sandbox reads and hides on a platform, before a run adds
// its own paths. A path starting with ~/ is under the user's home.
type Lists struct {
	System []string // folders every sandboxed run can read
	Never  []string // paths no sandboxed run can read, whatever else is granted
}
```

What moves where:

| List | `lists.go` (both) | `lists_darwin.go` | `lists_linux.go` |
| --- | --- | --- | --- |
| System | — | today's macOS `roots` | today's Linux `roots` |
| Never | the credential paths both platforms have today (`credentials` in `paths.go`) | `~/Library/Keychains` | `~/.local/share/keyrings` |

Homebrew's `var` is a Files Deny, not a Never path: it is a folder inside a readable root that
holds no credential, and a later rule may open something under it. `Lists` holds no Deny, so
`brewVar` moves to `lists_darwin.go` as it is, and macOS's `System` function (§4) returns it as
a Files Deny in place of the compiler emitting it.

The build tag on each file picks the platform, as `forward_darwin.go` and `forward_linux.go` do
today. A platform's lists are the shared ones plus its own. Step 2A adds the `Toolchain` list and
the note's longer `Never` list.

### 3. Not in the policy: how the sandbox stays a sandbox

Some settings are how a platform runs a program at all, or how it keeps the sandbox closed. No
policy changes them, so they stay in each platform's compiler:

- **Linux**: the `/proc`, `/dev` and private `/tmp` mounts; the network, PID, IPC and UTS
  namespaces, and the cgroup one where the kernel has it (`--unshare-cgroup-try`); `--die-with-parent`, `--new-session` and `--as-pid-1`; the closing
  `--remount-ro /`, which makes the empty root read-only; and the seccomp filter
  (`seccomp_linux.go`), which allows only IP sockets and refuses datagram socket pairs,
  `io_uring`, new user namespaces and other architectures.
- **macOS**: the fixed reads in `profile_darwin.sb` (`/dev/null`, the dyld cache, the time zone
  files); `setsid` and `setpgid` refused; signals only to the run's own processes; no `/dev/tty`;
  and the one Mach service every run needs, allowed by name.

No rule of a policy undoes them:

- **On macOS** the fixed file rules — the named system reads, the root's own listing, the writes
  and `ioctl`s on `/dev/null` and `/dev/fd`, and the metadata of `/etc`, `/var` and `/tmp` — move below the
  policy's in `profile_darwin.sb`, so the last matching rule is always the fixed one. The
  ancestors' metadata rules follow them, after every Deny, as today.
- **On Linux** a later mount lies over an earlier one, and a policy has to reach paths under the
  private `/tmp`: without `$XDG_RUNTIME_DIR` the runtime directory falls back to a folder under
  the temporary directory, `/tmp` on most machines, and every test's folder is there. So the
  order is a rule on `/` itself, when a policy has one (no Bash run does; step 4A's login shell
  does), then the fixed mounts, then the policy's other rules, then `--remount-ro /` and each
  Deny folder's remount. Linux cannot enforce a rule on `/tmp` or `/dev` itself, or one on or
  under `/proc`, since it would replace a fixed mount, so `Command` answers an error for one, a
  Files rule and a run's own path alike. A rule under `/tmp` or `/dev` is bound over the fixed
  mount, as the run's own paths are today, so a `TMPDIR` under `/dev/shm` still works.

### 4. The base file policy: `System`

`Sandbox.System(home, shell string, env []string)` returns the `FilePolicy` every sandboxed run
starts from on this machine:

- **Read**: the lists' System folders, the folders of the programs on the `PATH` `env` sets
  (`pathTrees`, unchanged until step 2A, with each platform's arguments as today: macOS passes
  its shared home folders, `~/Library` and `~/.config`, where a `PATH` entry names only itself),
  and Kstack's own executable, resolved through its links, so it never needs one recreated. On
  Linux the shell's own folder is one of the `PATH` entries, as it is in `args` today; macOS adds
  nothing for it.
- **Deny**: Homebrew's `var` on macOS.

And `Sandbox.Never(home)` returns the lists' Never paths with `~/` replaced by the home, which
Bash puts in the policy's Always part beside Kstack's directories. With an empty home it returns
the absolute paths alone: there is no home for a `~/` path to hide under, and a relative path
would fail `Check`. Linux's probe ignores `os.UserHomeDir`'s error today and must keep passing
on a machine with no `$HOME`.

**`System` leaves out every Read rule on or inside a Never path**, so any policy built from it
and `Never` passes `Check` whatever the `PATH` holds. On Linux it also leaves out a `PATH` entry
on `/tmp` or `/dev`, or on or under `/proc`: a rule there is one `Command` refuses (§3), so no
`PATH` can make every run fail. It drops such an entry before `pathTrees` makes trees of the
`PATH`, since `pathTrees` folds a tree into any tree that holds it, and a `/tmp` entry dropped
after would take `/tmp/x/bin` with it; it drops a tree `pathTrees` answers there too, since a
link can lead one there. This lives in the `sandbox`
package, not in Bash, because macOS's probe (§6) builds a policy from the sidecar's own `PATH`
too: on a Mac with
`~/.docker/bin` on `PATH`, a probe policy holding a Read on `~/.docker` would fail `Check` and
report no sandbox. `FilePolicy.Outside(paths ...string) FilePolicy`, the policy less every Read
or Write rule on or inside one of `paths`, is the one helper that does it; `System` calls it with
the Never paths, and Bash calls it again with Kstack's directories.

This is the logic Linux's `args` and macOS's `profile` run today, moved into two functions. The
helpers they use stay as they are. On Windows both return nothing.

Splitting the credential list takes from each platform only the store the other platform's
keyring uses, which nothing on it writes, so no run reaches anything new.

### 5. What changes, and what only looks like it does

**A `PATH` folder inside a credential path.** Today a credential path always hides a `PATH`
folder, whatever their depth. That is exactly what the Always part says, so nothing changes: a
`PATH` folder inside a credential path stays hidden, since a Files rule inside an Always path is
refused by `Check`, and `System` leaves such a `PATH` tree out (§4).

Docker Desktop is the case to know: it puts its programs in `~/.docker/bin`, `pathTrees` makes
the tree `~/.docker`, and `~/.docker` is a Never path. A tree on an Always path counts as inside
it, so the tree is left out and `~/.docker/config.json` stays hidden, as today. Docker Desktop's
`kubectl` is still found where another `PATH` entry links into `/Applications/Docker.app` on
macOS. A test checks this case.

**A missing Deny under a link.** Today a path that does not exist is only cleaned, never
resolved, so on macOS a Deny for a missing path under a link (`/var` is `/private/var`) names a
path Seatbelt never checks. Resolving through the deepest folder that exists (§1) makes that Deny
hold. It is the first of four changes of behavior in this step, and none widens what a run
reaches. No path in the goldens'
fixture is missing, so they do not show it; `TestADenyHoldsForAPathMadeLater` does, since
`t.TempDir()` on macOS is under `/var`.

**A `PATH` entry on `/tmp` or `/dev`, or on or under `/proc`, on Linux.** Today such a tree is
bound over the fixed mount: a `PATH` entry of `/tmp` shows the host's `/tmp` in place of the run's
private one. `System` now leaves it out (§4). This is the second change, and it only narrows.

**A missing Read or Write path.** Today a missing workspace, `Readable` or `Writable` path fails
bwrap on Linux, and on macOS a run can make a missing Write path. Both are now skipped (§1): on
Linux such a run starts, reaching nothing the missing path would have held, and on macOS it can
no longer make the path. These are the third and fourth changes. Bash makes every path it names
before the policy, so no production run meets either. A missing macOS root is the same rule: it
is emitted today whether it exists or not, and is now skipped, with any Deny inside it, since it
holds nothing to read.

### 6. The Workspace policy, built by Bash

`sandboxedRunFor` in `tools/bash/bash.go` builds the policy. Its Files are `System`, less any
rule on or inside Kstack's directories (`Outside`, §4). Its Always part is `Never` as its Deny, Kstack's data,
cache and runtime directories (`Paths.DeniedDirs`) as its Kstack paths, and the run's own paths
inside them:

| Path | Always rule | Why |
| --- | --- | --- |
| The shell snapshot, when there is one | Read | Every command sources it, until step 2A. |
| The run's own folder | Read | It holds the run's kubeconfig. |
| The workspace | Write | Where the command works. |
| The run's `TMPDIR` | Write | Temporary files. |
| The cluster's kubectl cache | Write | Only when the chat has a cluster. |

Each lies inside a Kstack directory, as `app/paths.go` lays them out, and nothing lies beneath
the workspace, `TMPDIR` or the kubectl cache, so the policy passes `Check`. `Check` does not care
whether Kstack's directories nest in one another, which the host decides. Today nothing checks
that; `Check` now holds it for every policy: nothing but the run's own paths opens inside
Kstack's directories.

A consequence to know: `Outside` drops every Files rule inside Kstack's directories, so a `PATH`
entry there, such as a tool the user installed under Kstack's data directory, is not readable by
a run. Its programs are then not found, as a program in any unreadable folder is not. No
installer puts one there.

Its network is one relay when the chat has a cluster: the port `Sandbox.Port()` answers, to the
run's proxy socket. With no cluster it has none.

The `sandboxer` interface in Bash gains `System` and `Never`, so the fake sandbox in Bash's tests
can answer them.

The tool's `extraWritable`, a test's seam for its coverage folder, is a Files Write rule: it lies
inside none of Kstack's directories, so it cannot be an Always one.

**Bash's tests change layout.** Today `tool()` in `bash_test.go` puts the home, the shell folder,
the temporary folder and the kubectl cache each in a `t.TempDir()` of its own, and the runs in a
short folder of their own (`runsIn`, over `shortTemp`), and names no denied directories; the
workspace comes from `testChatDir` (through `testRuntime`) and `clusterRuntime`
(`proxy_unix_test.go`), which are called apart from `tool()`. So the runs' own paths lie inside no
Kstack path and would fail `Check` on a real sandbox (a `fakeSandboxer` never runs `Check`).

One helper, `kstackDirs(t)` in `bash_test.go`, answers a test's three folders, data, cache and
runtime, with the paths laid out under them as `app/paths.go` does, as the macOS sandbox tests'
`standIn` already does. It makes them on its first call in a test and answers the same ones on
every later call in that test: it keeps them in a map keyed by the `*testing.T`, under a mutex,
and deletes the entry in a `t.Cleanup`. So `tool()`, `testChatDir`, `testRuntime`,
`clusterRuntime` and `proxyTool` (which also sets `extraWritable` over a real sandbox) each call it
and agree, and no call site changes. A subtest is a `*testing.T` of its own, so a test that makes
its tool in one and its runtime in the other gets two layouts, and over a real sandbox its
workspace fails `Check`; the helper's doc says so. The tests that make both today make them in the same `t`. The runtime folder is `shortTemp`'s, since the relay's socket
path has a length limit; data and cache are `t.TempDir()`s. The home stays a `t.TempDir()` of its
own, outside all three. The Linux sandbox tests that bind a workspace, or `Readable` and `Writable` paths, outside any
Kstack path (`sandbox_linux_test.go`, `TestTheCommandStartsBwrapOverTheChain` among them) give
them as Files Read and Write rules instead.

**The probe's policy.** Each platform's `Probe` runs a command through the sandbox before any
run exists, over the shell and `PATH` it uses today: `/bin/sh` and `PATH=/usr/bin:/bin` on Linux,
`/usr/bin/true` and the sidecar's own `PATH` on macOS. Its policy is `s.probePolicy(shell, env,
dir, home)` in `sandbox/probe.go`, a method since `System` is one and reads the sandbox's own
executable: `System` for that shell and `PATH`, the probe's temporary folder as a
Files Write rule, and `Never` as the Always Deny, with no Kstack paths and no relay: what today's
probe `Run` reaches, credential paths still hidden. `System` has already left out any `PATH` tree
on a Never path (§4), so the probe's policy passes `Check` on every machine. Both probes call
the one function, so its test covers both.

### 7. Turning a policy into sandbox settings

**Files.** Both platforms merge the Files and Always parts into one list of rules:

1. the Files rules, sorted by their resolved paths, shallowest first, and among rules on one path
   Write, then Read, then Deny;
2. each Always path, Deny and Kstack alike, as a Deny rule, so nothing outranks it;
3. the run's own Read and Write paths, shallowest first, which open what they name inside a
   Kstack path.

On both platforms the last matching setting wins, so this order makes the deepest rule win, the
narrower rule win a tie, and an Always path win over every Files rule. The sort uses resolved
paths because the path as written can mislead: `/bin`, a link to `/usr/bin`, would sort before
`/usr`, and Linux would bind `/usr/bin` again under `/usr` where `rootArgs` binds it once.

On Linux (bwrap), one mount per rule:

| Rule | bwrap argument |
| --- | --- |
| Read | `--ro-bind <path> <path>` |
| Write | `--bind <path> <path>` |
| Deny, folder | `--tmpfs <path>` (an empty folder), made read-only at the end |
| Deny, file | `--ro-bind /dev/null <path>` |

A Read or Write rule is bound at its resolved path, and where that differs from the path as
written, the link is recreated there: what `rootArgs` does for a root today, done for every rule,
so a merged `/usr` still has `/bin`. The run's own Always Read and Write paths are bound as
written, as today: bwrap resolves a destination through the links the tree already holds, and
they do lie under links in production — on Fedora Atomic `/home` is a link to `/var/home` — so resolving
them would add `--symlink`s that could clash with `rootArgs`' own. A Read rule whose resolved path
lies inside an earlier Read rule, with no Deny between them that covers it, is not bound again,
as `rootArgs` skips a root inside one already bound; a Write rule is always bound, since inside a
Read rule it opens what the Read rule does not. `pathLinks` stays as it is, run over the `PATH` entries and the shell's folder
from the run's `Env` and `Shell` and the policy's Read rules: it recreates links, and grants
nothing.

On both platforms a Deny rule, Files or Always, is compiled only where it overlaps a Files Read or
Write rule — lies inside one, or holds one. Anywhere else the path is already off limits: on
Linux it is not in the sandbox's tree at all, and on macOS the profile denies by default. This is
what Linux does today (`overlapping` over the roots and the `PATH` trees), and what macOS does
today for the credential paths. macOS today also emits every Kstack directory and Homebrew's
`var`; `var` lies inside the `/opt` and `/usr` roots, so it is still compiled, and a Kstack
directory no Files rule reaches is dropped from the profile, which changes nothing a run
reaches.

On macOS (Seatbelt), each path passed as a parameter, as today:

| Rule | Seatbelt rules |
| --- | --- |
| Read | `(allow file-read* (subpath …))`, then `(deny file-write* (subpath …))` |
| Write | `(allow file-read* file-write* (subpath …))` |
| Deny | `(deny file-read* file-write* (subpath …))` |

A Write rule on one of the run's own paths (`Always.Write`) is also followed by
`(deny file-write-unlink file-write-create (literal …))`, so the run cannot replace the root with a
link for the next run's profile to resolve
([a run cannot replace its own paths](../../security/2026-09-30-a-run-cannot-replace-its-own-paths.md)).
`Policy.Check` refuses an own path whose last component is a link.

A Read rule carries its refusal to write, so it decides a tie with a Write rule the way Linux's
read-only mount does. In `profile_darwin.sb`, the `TREES`, `DENIED` and `OWN` markers become one
`RULES` marker, with the fixed file rules below it (§3), then `ANCESTORS`, which is still made
from every Read and Write rule, Files and Always, the run's own paths among them, and still
follows every Deny, so a program can look up the
folders above an allowed path. The parameters are named for their rule, `RULE_n`, in place of
today's `TREE_n`, `DENY_n`, `WRITE_n` and `READ_n`.

**Network.** On both platforms, `ForwarderArgs` (`forward.go`) reads the relay from the policy
instead of `Run.Port` and `Run.Socket`. On Linux the run always has its own network namespace. On
macOS each relay gets today's rules for its port and socket; the constant `networkRules` becomes
`relayRules`, since it is one relay's rules.

### 8. Save today's output first

Before any code changes, save what today's code produces as "golden" files in
`sandbox/testdata/`: bwrap's argument list, and Seatbelt's profile text. They hold the network
settings as well as the file rules. Build them over a fixture folder that has:

- a home with programs on `PATH`, one of them a symlink, and one `PATH` entry at `~/.docker/bin`;
- a system folder that is a symlink;
- every credential path of its platform, and the other platform's keyring folder;
- on macOS, Homebrew's `var` inside a fixture root, so its Deny overlaps a Read and is compiled
  before and after;
- Kstack's three folders, the runtime one reached through a link, as the macOS temporary folder
  is under `/var`;
- a run with a cluster, and a run without one.

The goldens take the platform's lists with every path moved into the fixture, and the fixture
holds each of them, so what exists does not depend on the machine: no root is skipped for being
missing on CI. Task 1 moves them by swapping today's package variables — `roots`, `credentials`
and, on macOS, `brewVar` — for the test's life, as `sandbox_linux_test.go` already swaps
`roots`. After the refactor the golden tests swap the `Lists` values of `lists.go` and the
platform's file, and `brewVar`, the same way, so both runs of each golden read one fixture. Replace the fixture's folder, the test binary's path and the relay's
port with placeholders, so the files are the same on every machine.

After the refactor, the goldens may change only in these ways, each of which §5 or §7 explains:

- the order of the rules and the parameters' names;
- on macOS, the `deny file-write*` each Read rule now carries, and the Kstack directories' Deny
  rules no Files rule reaches, dropped;
- the `~/.docker` tree and its Deny, dropped on both platforms, since `System` leaves the tree out;
- Kstack's own executable, moved among the Files Reads at its resolved path, with no
  `--symlink`;
- the other platform's keyring Deny, dropped.

No other rule may be added or removed, and the run's own paths keep their link: the Linux golden
gains no `--symlink` for them. The reviewer reads the diff. The fixture's run keeps its
workspace, `TMPDIR`, run folder, snapshot and kubectl cache inside Kstack's three folders, where
`app` puts them, and after the refactor the golden tests build the policy §6 describes over the
same fixture.

## Decisions this step asks for

1. **The Always part is its own part, not a Files Deny.** A Files Deny can be opened by a deeper
   rule, which is what step 4D's grants are. The denied-always list and Kstack's directories must
   never be, so they sit where no Files rule can reach, and `Check` refuses one that tries.
   Recommended.
2. **Nothing sits beneath a Write rule.** A run can rename what a Write rule covers, and on macOS
   a deeper rule then matches nothing. Refusing the policy is simpler than defending each nested
   rule. Recommended.
3. **`Command` fails rather than narrows or widens.** A policy a platform cannot enforce is an
   error the model reads, never a run with different access than the policy says. Recommended.
4. **A missing Read or Write path is skipped; a missing Deny is not.** A missing folder holds
   nothing to read, and skipping it keeps Linux and macOS alike. A Deny must hold for a path
   made later, as far as each platform can. Recommended.
5. **A Deny is compiled only where a Read or Write rule reaches it.** Anywhere else the path is
   off limits already, and the profile and argument list stay short. Recommended.
6. **The lists are one shared file and one per platform.** A path that differs by platform lives
   in that platform's file, so neither platform carries the other's folders. Recommended.
7. **Homebrew's `var` is a Files Deny, not a Never path.** It holds no credential, and a later
   rule may open something under it. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | Save today's output as goldens | `sandbox/sandbox_linux_test.go`, `sandbox/sandbox_darwin_test.go`, `sandbox/testdata/` | — | Done |
| 2 | The new types and every signature, with no compiler moved yet. `Policy` and its parts, `Check`, `Outside`, the lists, `System`, `Never` and `probePolicy`, beside `Run`'s old fields, which the compilers still read. `Command` answers `(*exec.Cmd, error)` on all three platforms: Linux and macOS answer nil but for macOS's context that ends, Windows answers `errNone`. `ForwarderArgs` takes a `Relay`, and Linux's `args` and macOS's `argv` pass one made from `Run.Port` and `Run.Socket`. Bash's `sandboxer` interface gains `System`, `Never` and the new `Command`, its callers fail the call on the error, and `fakeSandboxer` follows | `sandbox/sandbox.go`, `sandbox/paths.go`, `sandbox/policy.go`, `sandbox/probe.go`, `sandbox/lists.go`, `sandbox/lists_darwin.go`, `sandbox/lists_linux.go`, `sandbox/sandbox_linux.go`, `sandbox/sandbox_darwin.go`, `sandbox/sandbox_windows.go`, `sandbox/forward.go`, `tools/bash/bash.go` (the `sandboxer` interface), `tools/bash/bash_unix.go` (`shellCmd` and its caller), `tools/bash/task_unix.go` (which calls `shellCmd`), `tools/bash/task.go`, their tests | 1 | Done |
| 3 | The Linux compiler reads the `Policy`, and its probe runs `probePolicy`; the Linux tests' workspaces become Files Write rules | `sandbox/sandbox_linux.go`, `sandbox/sandbox_linux_test.go` | 2 | Done |
| 4 | The macOS compiler reads the `Policy`, and its probe runs `probePolicy` | `sandbox/sandbox_darwin.go`, `sandbox/profile_darwin.sb`, `sandbox/sandbox_darwin_test.go` | 2 | Done |
| 5 | Bash builds the Workspace policy, filling `Run.Policy` beside the old fields until task 6; `kstackDirs` lays the tests' folders out under Kstack's three | `tools/bash/bash.go` (`sandboxedRunFor`), `tools/bash/bash_test.go`, `tools/bash/bash_unix_test.go`, `tools/bash/proxy_unix_test.go` | 2 | Done |
| 6 | `Run`'s old fields go, and Bash stops filling them | `sandbox/sandbox.go`, `tools/bash/bash.go` | 3, 4, 5 | Done |
| 7 | The table of rule pairs, on both platforms | `sandbox/policy_unix_test.go` | 6 | Done |
| 8 | Docs, per *When it lands* | see there | 1–7 | Done |

**Order:** 1, then 2, then 3, 4 and 5 at the same time, then 6, then 7, then 8. Task 2 makes
every signature change on every platform, so 3, 4 and 5 touch disjoint files and each task leaves
the tree building on Linux, macOS and Windows. Tasks 2 to 6 still land on the spec's branch
together: between 3 and 5 a compiler reads a `Policy` Bash does not yet fill.

## Tests

**`sandbox`**

- `TestAPolicyChecks`, in `policy_test.go`: a relative path fails, and so do a Files rule beneath a Write rule, a
  `~/.config/gcloud` Deny beneath a Files Write on `~/.config`, a Files rule inside a Deny or a
  Kstack path, a run's own path outside every Kstack path or inside a Deny path, and a second
  relay. The Workspace policy passes.
- `TestEveryPairOfRulesAnswersAlike`, in `policy_unix_test.go`, through the real sandbox on Linux
  and on macOS: for an outer rule of Read or Deny with an inner rule of Read, Write or Deny, for
  each tie, and for a Kstack path with a run's own Read and Write path inside it, whether a file
  at the inner path, and one beside it, can be read and written. One table holds the answers the
  rules give, and both platforms must give them. A read counts as allowed only when it returns
  the file's contents: on Linux a denied file reads as empty (`/dev/null`) and a denied folder
  lists as empty, where macOS answers an error, so the table compares what was read, never the
  exit status.
- `TestTheDeniedAlwaysListWinsOverARead`, in `policy_unix_test.go`: a policy whose Files Read
  names the home, and whose Always Deny names `~/.ssh`, reads `~/.zshrc` and not
  `~/.ssh/id_ed25519`, on both platforms; and a policy whose Files Read names `~/.ssh/keys`,
  inside that Always path, is refused by `Check`. This is the note's first invariant, and what
  step 4D's grants rest on. The existing `TestACredentialPathInsideAReadableTreeIsUnreadable`
  pins the same through a `PATH` tree, and stays.
- `TestADenyHoldsForAPathMadeLater`, in `sandbox_darwin_test.go`: a file made at a missing Deny's
  path after the run starts cannot be read by it.
- `TestAPolicyThatFailsCheckStartsNothing`, in `sandbox_linux_test.go` and
  `sandbox_darwin_test.go`: `Command` answers an error and no command. `TestCommandAnswersErrNone`,
  in `sandbox_windows_test.go`, the same for Windows' `errNone`.
- `TestOutsideDropsWhatIsOnOrInsideItsPaths`, in `policy_test.go`: `Outside` drops a Read and a
  Write rule on and inside the paths it is given, and keeps one beside them and one above them.
- `TestARuleUnderTmpOrDevIsReachable` and `TestARuleOverAFixedMountIsRefused`, in
  `sandbox_linux_test.go`: a Write rule on a folder under `/tmp`, and a run's own Write path
  under `/dev/shm`, are written through the real sandbox; a rule on `/tmp` or `/dev`, or on or
  under `/proc`, makes `Command` answer an error.
- `TestTheProbeKeepsTheCredentialsHidden`, in `probe_unix_test.go`: `probePolicy` has its
  folder as a Files Write rule and `Never` as its Always Deny, and passes `Check`, with
  `~/.docker/bin` on the `PATH` it is given; with an empty home it still passes `Check` and its
  Always Deny holds the absolute Never paths alone.
- `TestAProbeWithNoHomeFindsTheSandbox`, in `sandbox_linux_test.go`: with `HOME` unset, Linux's
  probe answers a sandbox, as it does today.
- `TestSystemLeavesOutTheFixedMounts`, in `sandbox_linux_test.go`: with `/tmp`, `/tmp/x/bin`,
  `/proc/1` and `/dev` on `PATH`, `System` holds no rule on `/tmp`, `/dev` or `/proc/1`, and one
  for `/tmp/x/bin`.
- `TestSystemLeavesOutTheNeverPaths`, in `sandbox_linux_test.go` and `sandbox_darwin_test.go`:
  with a `PATH` entry inside a Never path, `System` holds no rule on or inside it.
- `TestSystemIsTheListsAndTheTrees`, in `sandbox_linux_test.go` and `sandbox_darwin_test.go`:
  each platform's `System` holds the shared lists and its own, and `Never` too; macOS's holds
  Homebrew's `var` as a Deny. `TestNeverWithNoHomeIsTheAbsolutePaths` beside it.
- `TestNoPathIsInTwoLists` and `TestEveryListPathIsAbsoluteOrInTheHome`, in `lists_test.go`:
  no path is in both the shared lists and the platform's, and each starts with `/` or `~/`.
- `TestTheCompiledArgumentsMatchTheGolden` (Linux) and `TestTheCompiledProfileMatchesTheGolden`
  (macOS).
- Every existing sandbox test, building a `Policy` in place of a `Run`'s fields; what it checks
  stays the same. Three change what they assert, since what they pin is the order this step
  replaces: `TestTheCommandMountsInOrder` (Linux) and
  `TestTheProfileDeniesBetweenTheReadsAndTheRunsOwn` (macOS) pin §7's order, and
  `TestTheCommandStartsBwrapOverTheChain` binds folders that exist, since a missing one is now
  skipped. `TestForwarderArgsRoundTrip` builds a relay in place of `Run.Socket` and `Run.Port`.
  `TestARunWithNoSocketStartsTheForwarderWithNoFlags` reads only the closing arguments, and
  stays as it is.
- `TestAMissingReadOrWritePathIsSkipped`, in `sandbox_linux_test.go` and
  `sandbox_darwin_test.go`: a policy naming a missing Read and a missing Write path starts, and
  the run cannot make either path.

**`bash`**

- `TestTheWorkspacePolicyIsSystemAndTheRunsOwn`, replacing `TestASandboxedRunNamesWhatItMounts`:
  over a fake sandbox, the policy's Files are `System` less the rules on or inside Kstack's
  directories, and its Always part is `Never`, Kstack's three directories and the rows of the table in
  §6. `TestASandboxedRunWritesTheExtraWritable` checks the extra folder as a Files Write rule.
- `TestDockerDesktopsBinStaysDenied`, in `bash_unix_test.go` through the real sandbox, end to end
  over `System`, which leaves the tree out: with `~/.docker/bin` on `PATH`, the policy passes `Check`, and
  neither `~/.docker/config.json` nor `~/.docker/bin` can be read.
- `TestTheRunsKubeconfigNamesTheCardsContextAndPort` checks one relay to the run's proxy socket
  with a cluster, and a new case none without.
- `TestASandboxedRunThatCannotBePreparedCouldNotStart` (`bash_unix_test.go`) and
  `TestASandboxedTaskThatCannotStartEndsItsRun` (`proxy_unix_test.go`) gain a case where
  `Command` answers an error: nothing runs, and the model reads the error, in the foreground and
  the background. The fake already records each `Command` call before it answers, so the new
  case asserts that nothing was started rather than that `boxer.seen()` is empty.

## Security

No security boundary moves: a sandboxed command reaches what it reached before, less the missing
Deny, the Linux `PATH` trees over fixed mounts and the missing Write paths macOS let a run make,
which §5 closes. The goldens and the existing tests show it. `Check` now holds for every policy
what nothing checked before: nothing but the run's own paths opens inside Kstack's directories,
nothing at all opens inside a credential path, and no Write rule holds an Always path it could
rename away. No security record is needed. The sandbox's rows in `security-model.md` gain
the new tests' names, and `TestTheDeniedAlwaysListWinsOverARead` gets a row of its own, since
step 4D's grants rest on it.

**Residual.** On Linux a Deny is a mount, and a mount needs a path to cover. A denied-always path
that does not exist when a run starts is not mounted over, so if something outside the run makes
it while the run lives — the user running `ssh-keygen` while a background command reads `~` —
the run can read it. The run itself cannot make the path (§1). Today the same holds for a Never path inside a `PATH`
tree under the home (a missing `~/.local/share/keyrings` under `~/.local`); step 4D's grant of `~`
makes every one reachable this way, and 4D's *Security* carries it on. macOS holds a Deny for a path whether or not it exists, and has no such
gap. The row for `TestTheDeniedAlwaysListWinsOverARead` in `security-model.md` names it.

## When it lands

- **`sidecar/CLAUDE.md`**: describe `sandbox.Run` as a `Policy` of a `FilePolicy`, an
  `AlwaysPolicy` and a `NetworkPolicy` that each platform compiles; how rules combine, what the
  Always part means and what `Check` refuses; that `Command` fails rather than narrows or widens;
  what stays in the compilers (§3); the shared and per-platform lists; `System` and `Never`; and
  the Workspace policy Bash builds.
- **`security-model.md`**: the new tests on the sandbox's rows, and the denied-always row.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), with the sandbox's tests on
Linux and in CI's macOS job.

By hand, run `pnpm tauri dev` on macOS and on Linux. A sandboxed `kubectl get ns` and `ls ~`
should behave as they did before.
