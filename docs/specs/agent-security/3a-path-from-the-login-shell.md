---
title: "`PATH` from the login shell"
scope: sidecar, webview
status: Planned
---

# `PATH` from the login shell

**Needs:** step 2A, whose `System(home, shell)` decides which new entries are adopted unasked and
whose environment table this step's `PATH` row fills, and step 1C, whose store keeps the list.
**Unblocks:** 4A (the resolution runs confined), 4D (grants join the settings file), 6A (probes
follow a refresh) and 7A (onboarding shows the list).

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command's `PATH` is the sidecar's own: on macOS what `loginshell.Import` read
from `$SHELL` at launch, on Linux whatever the process was started with. So a line in `~/.zshrc`
changes what every sandboxed command finds at the next launch, with nobody told, and a dev run
from a terminal and a Finder launch find different tools.

After this step, on macOS and Linux:

- **`PATH` comes from the user's login shell**, the one in the account record, run once at each
  launch with a scrubbed environment and a timeout. `PATH` is the one thing taken from it for the
  sandbox ([the note](../../notes/sandbox-credentials-and-permissions.md)'s *Resolution*). On
  Linux this is a new shell run before READY, bounded by the same 5 second timeout macOS has.
- **It is filtered.** Empty, relative, missing, world-writable and project-local entries go, and
  so does anything under the denied-always list or Kstack's directories. The shell's order stays
  (the note's *Filtering*).
- **It is frozen** in the settings file, `<data>/security.json`, which step 1C's
  `securityconfig` keeps. A run reads the list once when it starts. At each launch the fresh list is
  diffed against the stored one: an entry that adds no readable surface is adopted, every other
  waits for the user, one that disappeared is dropped unless the user removed it (the note's
  *Freezing and syncing*). Each entry keeps the folder it resolved to, and one that resolves
  elsewhere is filed again as if new.
- **The sandbox's `PATH` and its Read rules are one list**, so a program on the path is readable,
  and **the user sees it** in Settings: each entry, its state, Include, Remove and Refresh PATH.

## What is not in this step

- **The login shell still runs unconfined.** Step 4A runs it in a read-only sandbox.
- **No probing.** The note's *Dependency probing* is step 6A, which follows this step's refresh.
- **No grants.** A folder to read rather than search is step 4D's grant, in the same file.
- **No change on Windows**, which has no sandbox.

## Design

### 1. Resolution: one run of the login shell

`loginshell.Launch` runs one command in the user's login shell, and `Import` is its one reader
today: the allowlist for the sidecar's own process on macOS. This step gives the package one
resolution and two readers:

```go
// Result is what one run of the login shell answered.
type Result struct {
	Path []string          // PATH as the shell exported it, split on ":", unfiltered
	Env  map[string]string // the imported allowlist, resolved; what Import installs on macOS
}

// Resolve runs the login shell once and reads both.
func Resolve(ctx context.Context) (Result, *Fault)

// Path is Resolve's Path alone: the refresh's reader. Its error is a *Fault.
func Path(ctx context.Context) ([]string, error)
```

`*Fault` gains `Error() string`, its `Reason`, so a caller off this package holds it as an
`error`.

**`loginshell` is Unix only**, and `securityconfig`, `app` and `main_default.go` build on Windows
too (CI runs `GOOS=windows go vet`). So nothing outside a Unix file imports it:

- `main.go` calls `launchShell(ctx) (path []string, fault string)`, whose two bodies are
  platform files, so `main.go` names no `loginshell` type. `main_unix.go` (`//go:build unix`) has
  `launchShell(ctx) { return runShell(ctx, loginshell.Resolve) }`, and `runShell` calls its
  resolver once, hands its `Env` to `installShellEnv` and returns its `Path`, or the `Fault`'s
  reason. `installShellEnv`
  is `main_darwin.go`'s (today's `importShellEnv` body) and a no-op in `main_default.go`.
  `main_windows.go`'s `launchShell` returns nil and runs nothing.
- `app` takes the refresh's resolver from `app/shellpath_unix.go` (`loginshell.Path`) and
  `app/shellpath_windows.go` (nil).
- `securityconfig` holds the resolver as `func(context.Context) ([]string, error)`.

**One run at launch prints both.** `main` calls `launchShell` once, before the app exists, on
macOS and Linux. On macOS it installs `Env` process-wide as `importShellEnv` does today; on Linux
it installs nothing. Either way it hands `Path` to the app as `app.Config.ShellPath`, nil after a
`Fault`, which is logged with its fixed reason and never a value, as today, and handed to the app
as `app.Config.ShellFault` so Settings can say it (§6). So one run answers both
readers, where today's `Import` would be a second beside it on Linux. The bash tool's snapshot
(`app.go`'s *shell snapshot* part) still runs the startup files once more at `Start`, as today:
it reads functions and aliases for a command outside the sandbox, which is not this step's
question. The refresh (§4) is a further run, on request, and takes `PATH` alone. `Import` goes.

**The shell is the account record's**, never `$SHELL`: `/usr/bin/dscl /Search -read
/Users/<name> UserShell` on macOS, which answers for a network account as well as a local one;
on Linux `/usr/bin/getent passwd <name>`, which answers for a directory
account (LDAP, SSSD) too, then the user's line in `/etc/passwd` when `getent` is missing;
`<name>` from `user.Current()`.
Each lookup runs through `lookupCommand`, a package variable holding the command's path, so a
test points it at a fake `dscl` or `getent`. `AccountShell()` answers it when it names an
executable file by absolute path; else the platform's
own (`/bin/zsh`, or `/bin/bash` then `/bin/sh` on Linux), and `Fault{Reason: "no shell"}` when
none is. `Find()` stays for the Bash tool, which picks the shell a command runs in; that is not
this step's question.

**The command follows the shell's kind**, read off its base name:

| Kind | Shells | Flags | Command |
| --- | --- | --- | --- |
| `posix` | `zsh`, `bash`, `sh`, `dash`, `ksh`, `fish` (it parses today's command), and any name not `nu` | `-i -l -c` | today's `command`: the cwd frame, then one `printenv` per imported variable, between the markers |
| `nu` | `nu` | `-l -c` | two frames between the same markers: `^/bin/pwd`, then `$env.PATH \| str join ":"` |

`parse` takes the number of frames the kind prints, so a nushell run yields `Path` and an empty
`Env`. `-l` is what makes nushell read `env.nu`, `config.nu` and `login.nu` under `-c`; the by-hand
verification confirms it on the nushell release current when this lands, since a fake shell cannot.
`Launch` keeps the markers, `maxOutputBytes`, `DefaultTimeout`, the session, the kill and the reap.

**The resolution's environment is scrubbed.** `Launch` takes the shell's environment as an
argument. `Resolve` passes a fixed list and nothing else: `HOME`, `USER`, `LOGNAME`, `SHELL` (the
shell being run), `TMPDIR`, `LANG` and `TZ` copied from the process when set, `TERM=dumb`,
`DISABLE_AUTO_UPDATE=true`, and `PATH` set to the platform's login default
(`/usr/bin:/bin:/usr/sbin:/sbin` on macOS, `/usr/local/bin:/usr/bin:/bin` on Linux), where a
terminal's login shell starts from too. So a dev run from a terminal and a Finder launch resolve
the same list, and no startup file sees the sidecar's variables. The bash tool's snapshot
(`tools/bash/snapshot_unix.go`) passes what `Launch` builds today, the process's environment
plus `DISABLE_AUTO_UPDATE=true`, so the functions and `PATH` a command outside the sandbox gets
do not change in this step.

**Failure keeps the stored list.** A `Fault` — no shell, exit, bad output, output limit, timeout —
leaves `security.json` as it was and logs one line with the reason, not a startup error.

### 2. Filtering

`securityconfig.FilterPath(entries, never []string) (kept []PathDir, dropped map[string]int)`
takes the raw list and `never`, `Sandbox.Never(home)` plus Kstack's three directories
(`bash.Paths.DeniedDirs`): the paths no rule opens. It drops, in this order, counting each rule's
drops:

| Rule | Drops | Why |
| --- | --- | --- |
| empty | `""` | nothing to search |
| relative | `.`, `..`, anything not absolute, a leading `~` | it would resolve to the workspace, which is writable |
| missing | a path that is not a directory after following links | nothing to read |
| world | a directory writable by others, sticky bit or not | anyone on the machine can put a program in it |
| never | a path under one of `never`, or one of them | the denied-always list wins; Kstack's files stay Kstack's |
| project | a path with a `node_modules` component | a project folder is not user binaries (see Decisions) |
| duplicate | an entry whose `Target` an earlier kept entry has | it finds nothing the first does not |

The shell's order is kept. The count per rule goes on one log line, never an entry. Every check
is on the resolved path. A kept `PathDir` holds `Dir`, the entry as the shell gave it, `Target`,
the path it resolved to, and `Shared`, set when the directory is group-writable by a group that
is not an administrators' group: the group's other members can put a program in it, so the sync
never adopts it unasked (§4). A sticky bit does not change that, since it stops a member deleting
another's file, not adding one.

**An administrators' group shares nothing.** Its members can already write anywhere through
`sudo`, so a folder they can write adds no one who could not. The group is read by name with
`user.LookupGroupId`: `admin` and `wheel` on macOS, `root`, `wheel`, `sudo` and `admin` on
Linux, and gid 0 on both. Homebrew's installer leaves `/opt/homebrew/bin` (and `/usr/local/bin`
on Intel) group-writable by `admin`, so this is what keeps it adopted on a default Mac. A group
that cannot be looked up is shared.

### 3. `Settings.Path`

The store is `securityconfig`
([`sidecar/CLAUDE.md`](../../../sidecar/CLAUDE.md#security-settings-internalsecurityconfig)). This
step adds the `Path` field to its `Settings`, in `securityconfig/path.go`:

```go
// In securityconfig.Settings.
Path []PathEntry `json:"path,omitempty"` // in the shell's order

// PathEntry is one folder of the user's PATH and what the sandbox does with it.
type PathEntry struct {
	Dir    string    `json:"dir"`    // as the shell gave it
	Target string    `json:"target"` // what Dir resolved to when the state was set
	State  PathState `json:"state"`  // adopted, pending or gone
	Source Source    `json:"source"` // shell or user
	Shared bool      `json:"shared,omitempty"` // Target was writable by a non-admin group when the state was set
}
```

| State | Meaning |
| --- | --- |
| `adopted` | on the sandbox's `PATH` and readable in it |
| `pending` | the shell lists it, adopting it would add readable surface or it is shared, and the user has not said yes |
| `gone` | the user removed it; kept, whether or not the shell lists it, so a sync does not adopt it again, and off the sandbox's `PATH` |

`Source` says whose decision the state is: `shell` when the sync set it, `user` when Include or
Remove did. A state holds for `Target` alone: the approval is of the folder the entry resolved
to, not of its spelling. The field's line in step 1C's `checks` refuses an entry whose `dir` or
`target` is not absolute, whose `dir` an earlier entry has, or whose state or source is not one
of the names above, so a hand-edited entry is left out and shown, as every field is.

**The field restricts**, since a refused entry may have been a removal. So it has a line in
`strictest`, which leaves the entries that passed as they are: what makes the field strict is
the sync's, not a value in `Settings`. The line is what makes the store keep the field's raw
JSON; without one a refused entry is dropped at the next write. The store gains
`Held(field string) bool`, whether it still keeps that field's raw JSON.

**The store guards a held field.** Today any `Update` that changes a held field drops its raw
JSON (`stillHeld`), so whichever write came first would lose the value the store could not
read. So `Update` refuses, with `ErrHeld`, a change to a held field its `fields` does not name.
A write that means to end the hold names the field. The guard is in the store, not in a
resolver, so a writer a later step adds is held to it too.

While `Held("path")`, the sync files every new entry `pending`, whatever its zone (§4). The sync
names `path`, so its write replaces the held JSON and ends the hold, and the entry it could not
read comes back, if the shell still lists it, waiting for the user.

**Include waits for that sync; Remove does not.** Include widens, so while `Held("path")` it is
refused with `KSTACK_VALIDATION_ERROR`, *Your PATH settings hold an entry Kstack cannot read.
Refresh PATH first.* Remove narrows, and the user must be able to narrow when the shell does not
answer, so it names `path` and writes. That ends the store's hold, so the `Service` keeps it in
memory: from a hold's end by any write but a sync, until the next sync, the sync files every new
entry `pending` as if the field were still held. The entry the store could not read is lost
from the file either way, and so cannot re-adopt itself unasked.

`Held`, `ErrHeld` and the guard are shared with step 3B, which needs them for the same reason:
whichever step lands first adds them to `store.go` with their tests, and the other uses them.

### 4. Freezing and syncing

**A run reads the list once.** `bash.Paths` gains `Path func() []securityconfig.PathEntry`,
which `app.New` sets to the `Service`'s `AdoptedPath`. `sandboxedRunFor` in `tools/bash/bash.go`
calls it at the run's start and checks each entry with `securityconfig.CheckRun(entry,
sys.Files, always)`, the sync's checks over one folder against the run's own `System` and
`Always` paths, which answers the folder to run on or none:

- An entry whose `Target` is on or inside one of the run's `Always` paths, or is now
  world-writable, is left out. The filter drops such an entry at the sync, but the stored list
  can predate a path the denied-always list gained (an upgrade, a new user's home) or a `chmod`,
  and a Read rule inside an `Always` path would fail `Policy.Check` and with it every sandboxed
  command.
- An entry the shell adopted (source `shell`) whose `Target` is now shared, or no longer open
  (an upgrade can narrow `System`), is left out: the sync would file it `pending`. One the user
  adopted runs, since the user approved the folder itself.
- An entry that resolves elsewhere than its `Target` runs on the folder it resolves to now when
  that folder is open and not shared, which is what the sync would adopt unasked: a version
  manager's `current` link that moved with a version switch keeps working. Otherwise it is left
  out, and the next sync files it again.

`CheckRun` reads the disk, so it runs inside the goroutine `sandboxedRunFor` already builds a run
on, which is abandoned if the run's context ends first, since a stat on a dead network mount does
not return. It looks a group's name up once per gid for the sidecar's life, since a run checks every
entry. Each entry left out, or run on a new folder, gets one log line. The stored list is not
written: the next sync records the move. The run uses the rest, one list, for both:

- `PATH` in `sandboxedRunEnv` (`tools/bash/env.go`), the row step 2A left as the sidecar's:
  the folders the check answered, joined in order. Each is the folder the approval holds and the
  Read rule names, so a link repointed while the run lives changes nothing it finds. With none
  adopted — a first launch whose resolution failed — the platform's login default from §1, which
  `System` already reads, so `ls` still runs.
- One Files Read rule per folder the check answered that `System(home, shell)` does not already
  make readable: the folder itself and not its parent, since step 2A's toolchain list covers the
  wider trees. A moved link's new folder is open, so it never needs one.

Nothing re-reads the store while the run lives, a background task included; a refresh or an
Include changes the next run.

**The sync.** `(*Service).SyncPath(ctx, resolved []string) (PathReport, error)` in
`securityconfig/service.go`, where `Service` is the store plus what the sync needs: the filter's
`never` list; `System(home, shell).Files`, with `shell` the Bash tool's (`loginshell.Find`, the one
a run's own `System` is built with, not `AccountShell`), whose Read paths are the **open** folders
and whose Deny paths (Homebrew's `var`, the `Closed` folders) close what lies under them again; and
`Resolve func(context.Context) ([]string, error)` for the refresh. An entry is **open** when its
`Target` is under one of the Read paths and under none of the Deny paths: adopting it adds no
readable surface. It filters `resolved`, then diffs against the stored entries:

| Case | What happens |
| --- | --- |
| an `adopted` or `pending` entry the shell no longer lists | dropped from the list |
| a `gone` entry the shell no longer lists | kept, after the listed entries: the removal outlives the entry's absence |
| a new entry that is open and not shared | `adopted`, source `shell` |
| any other new entry: under the home, in a `Closed` folder, outside every open folder (`/Users/Shared/bin`, `/var/lib/flatpak/exports/bin`), or shared | `pending`, source `shell` |
| an `adopted` or `pending` entry whose `Target` changed | filed again as a new entry with the new `Target`, by the two rows above |
| an `adopted` entry, source `shell`, whose `Target` is now shared or no longer open | `pending`, source `shell` (shared when it is) |
| a `gone` entry whose `Target` changed | stays `gone`, with the new `Target` |
| the order changed | the stored entries are put in the shell's order; states are kept |

While `Held("path")` (§3), every new entry is `pending`.

After the diff, if the first adopted folder holding `kubectl` or `helm` differs
from before, one log line names the tool and both folders. The report carries the four lists.

`app.New` builds the `Service` over the store it opened (step 1C), with the resolver from
`app/shellpath_*.go` (§1), and runs `SyncPath(cfg.ShellPath)` in `Start`, before the shell
snapshot's part, when `ShellPath` is not nil. The sync reads the disk (`Never`, a stat per entry,
group lookups) on a goroutine it abandons when its context ends, as a run is built; `Start` bounds
it with `loginshell.DefaultTimeout`, and one that times out changes nothing. The `Service` keeps the
reason the last resolution failed, `cfg.ShellFault` at launch, then the refresh's, and clears it
when one answers. **Refresh PATH** is `(*Service).RefreshPath(ctx)`: `Resolve`, then `SyncPath`; an
error answers one naming the `Fault`'s reason and changes nothing. Include is `AdoptPath(dir)`: a
`pending` or `gone` entry becomes `adopted`, source `user`, for the `Target` it holds, never one
resolved again, so Include approves the folder the user was shown. Remove is `DropPath(dir)`: an
`adopted` or `pending` entry becomes `gone`, source `user`. A `dir` not listed, or already in the
state asked for, is refused.

### 5. The wire

```graphql
enum SandboxPathState { Adopted Pending Gone }
enum SandboxPathSource { Shell User }

"One folder of the user's PATH, and what the sandbox does with it."
type SandboxPathEntry {
  dir: String!
  "The folder dir resolved to when its state was set; the one the sandbox reads."
  target: String!
  state: SandboxPathState!
  source: SandboxPathSource!
  "Whether target was writable by a group other than an administrators' one, so others in it can add programs to it."
  shared: Boolean!
}

extend type Query {
  "The frozen PATH, in the shell's order. Empty on a machine with no sandbox."
  sandboxPath: [SandboxPathEntry!]!
  "Why the last read of the login shell's PATH failed, at launch or on refresh; null once one answers, and on a machine with no sandbox."
  sandboxPathFault: String
}

extend type Mutation {
  "Include a pending or removed entry. Refused KSTACK_VALIDATION_ERROR for a dir not listed or already included, or while the file holds an entry Kstack cannot read."
  sandboxPathAdopt(dir: String!): [SandboxPathEntry!]!
  "Remove an entry. Refused KSTACK_VALIDATION_ERROR for a dir not listed or already removed."
  sandboxPathDrop(dir: String!): [SandboxPathEntry!]!
  "Run the login shell again and fold its PATH in. Refused KSTACK_VALIDATION_ERROR with the shell's fault, or on a machine with no sandbox."
  sandboxPathRefresh: [SandboxPathEntry!]!
}
```

Each mutation answers the whole list, so the section redraws from one result. `Service` embeds
the `Store`, and `graph.Resolver`'s `SecurityCfg` (step 1C) becomes a `*securityconfig.Service`:
the embedding promotes every `Store` method, so resolvers step 3B wrote against the store
keep compiling in either order, and only `app`'s construction changes;
on a machine with no sandbox (`sandbox.available` false, step 1B) the query answers an empty
list and the mutations are refused.

### 6. Settings

The Settings dialog (`src/components/widgets/settings-dialog.tsx`) gains a **Sandbox** section,
its own component beside it, `sandbox-settings.tsx`, drawn only while step 1B's `sandbox.available`
is true. `useSandboxPath()` in `src/lib/sandbox-path.tsx` is the one reader of the queries and the
mutations, and reads `sandboxPathFault` again after each refresh.

- The entries in order, each with its `dir` in mono through `VisibleText` (a folder name is text
  the shell produced), its `target` under it the same way when the two differ, and its state as a
  tag: *included*, *waiting for you*, *removed*. A shared entry adds a muted *shared with its
  group*, since that is why it waits.
- **Include** on a *waiting* or *removed* entry, **Remove** on an *included* or *waiting* one,
  and **Refresh PATH** above the list. Each calls its mutation, is disabled in flight, and is
  handed back on an error, which `errorReportExchange` reports; a refused refresh draws *Your
  shell did not answer: <reason>.* under the button, and a refused Include or Remove draws the
  refusal's message under the list.
- While `sandboxPathFault` is set, one line above the list: *Kstack could not read your shell's
  PATH: <reason>. The list is from the last time it could.* (or, with no entries, *Sandboxed
  commands use the system's default PATH.*) A shell whose language Kstack does not speak, such as
  xonsh or PowerShell, lands here every launch.
- One line under the list: *Sandboxed commands find programs in these folders, in this order.
  Kstack reads them from your shell at each launch; a new folder that would open more to
  commands waits for you.*

### 7. Windows

No sandbox: `main_windows.go`'s `launchShell` runs nothing, `Config.ShellPath` stays nil, the
`Service` has no resolver, the store opens with no
`Path` (step 1C opens it everywhere), the query answers an empty list and the section is not
drawn.

## Decisions this step asks for

- **`node_modules/.bin` is dropped.** The note leaves it open. A project folder is not user
  binaries: its programs are whatever the last `npm install` fetched. Step 4D grants one.
- **One resolution at launch, not two.** `Import` and `Path` read one `Result`, so the
  resolution runs the startup files once; the snapshot's run is the bash tool's, as today, and
  the refresh runs only on request.
- **A removed entry is kept as `gone`, whether or not the shell lists it.** Without a tombstone
  the next launch would adopt an open-zone entry back, and Remove would mean nothing. An entry
  that leaves the shell's `PATH` for a launch and comes back is the same case. The tombstones
  stay in `Path` rather than a list of their own, so a removal has one record; the cost is that
  `Path` can hold folders the shell no longer lists, drawn *removed*. Recommended.
- **An entry's approval is bound to the folder it resolved to.** A `PATH` entry is often a link
  (`/usr/local/bin`, a version manager's `current`), and repointing it must not change what the
  sandbox reads unasked. So each entry stores its `Target`, and a run leaves out an entry that
  resolves elsewhere, unless the new folder is one the sync would adopt unasked anyway, open and
  not shared, so a version switch through a `current` link does not take the tool away until a
  Refresh. The sync files a moved entry again. The alternative, storing the resolved path in
  place of the entry, loses the shell's spelling the user recognises and still needs a check at
  the run. Recommended.
- **A group-writable folder waits for the user rather than being dropped, unless the group is
  an administrators' one.** The note drops one without the sticky bit, and the sticky bit does
  not stop a group member adding a program anyway. Homebrew's installer leaves its prefix
  group-writable by `admin`, so dropping it, or holding it for the user, would take `kubectl`
  and `helm` off a default Mac's sandboxed `PATH` until the user found Settings. An
  administrators' group's members can write anywhere through `sudo`, so their folder adds no
  writer; it is not shared. For any other group only the user knows who else is in it, so a
  shared folder is `pending`, tagged so, and Include adopts it. A world-writable folder is still
  dropped. Recommended; the note's *Filtering* is amended to match when this step lands.
- **The list is frozen per run, not per session.** The note keeps a session's `PATH` for its
  life, so a session cannot widen its own. Only the user's Include, Remove and Refresh PATH
  change the list, through the sidecar's socket, which no sandboxed command reaches; so per run
  gives the same guarantee, and an Include applies to the chat the user is in. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Resolve`, `Path`, `Fault.Error`, `AccountShell`, the shell kinds, `Launch`'s environment argument and the scrubbed one; `main` resolves once | `loginshell/`, `tools/bash/snapshot_unix.go`, `sidecar/main.go`, `sidecar/main_unix.go`, `sidecar/main_windows.go`, `sidecar/main_darwin.go`, `sidecar/main_default.go`, `app/app.go` (`ShellPath`, `ShellFault`), their tests | — | Planned |
| 2 | `Settings.Path`, its check and `strictest` lines, `Held`, `ErrHeld` and the store's guard (unless step 3B added them), and the filter | `securityconfig/path.go`, `securityconfig/check.go`, `securityconfig/store.go`, their tests; `TestAnUpdateKeepsARefusedRestrictionInTheFile`'s second half, which changes a held field unnamed, now names it | — | Planned |
| 3 | The sync, the refresh, Include and Remove | `securityconfig/service.go`, its tests | 1, 2 | Planned |
| 4 | The run freezes the list: `PATH` and the Read rules | `tools/bash/bash.go`, `tools/bash/env.go`, `app/app.go` (`bash.Paths.Path`), their tests | 3 | Planned |
| 5 | The wire and the resolvers | `sidecar/graph/schema.graphqls`, `graph/`, `app/app.go`, `app/shellpath_unix.go`, `app/shellpath_windows.go`, generated code | 3 | Planned |
| 6 | Codegen, `useSandboxPath`, the Settings section | `src/gql/`, `src/lib/sandbox-path.tsx`, `src/components/widgets/sandbox-settings.tsx`, `settings-dialog.tsx`, their tests | 5 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4 and 5 at the same time, then 6, then 7.

## Tests

**`loginshell`** (`loginshell_test.go`, `//go:build unix` as the package is)

- `TestResolveReadsEachShellKind`: a fake shell script named `zsh` and `nu` in turn, which
  checks its flags, prints the frames its kind prints and a canned `PATH`; `Result.Path` is that
  list split, and `Env` is empty for `nu`.
- `TestResolveRunsTheAccountShell`: with `$SHELL` naming another script, the account record's
  shell runs; `TestAccountShellFallsBack` for a record naming nothing executable.
  `TestAccountShellAsksGetent` on Linux: a fake `getent` answers a shell `/etc/passwd` lacks;
  `TestAccountShellAsksDscl` on macOS: a fake `dscl` answers the shell. Both through
  `lookupCommand`.
- `TestTheShellsEnvironmentIsScrubbed`: the resolution's shell sees §1's list and nothing else;
  `TestTheSnapshotKeepsTheProcessEnvironment`: the snapshot's shell still sees the process's.
- `TestAFaultAnswersNoPath`, for each fixed reason, and the `Fault` is the `error` `Path`
  answers; and `TestResolveIsRunOnceAtLaunch` (`main_unix_test.go`): `runShell` with a
  counting resolver calls it once and answers its `Path`.
- Every existing test, `Import`'s renamed to `Resolve`'s.

**`securityconfig`**

- `TestFilterPathDropsEachRule`: one case per row of §2's table, over a fixture folder with a
  world-writable directory with and without the sticky bit, a link into a `never` path, a
  `node_modules/.bin`, and two entries resolving to one folder; the kept list is in the shell's
  order, as given; a group-writable directory, with and without the sticky bit, is kept and
  `Shared`; one group-writable by an administrators' group (a fake `LookupGroupId` answering
  `admin`, and gid 0) is kept and not `Shared`, and one whose group cannot be looked up is.
- `TestSyncPathDiffsFourWays`: an adopted or pending entry that disappeared is dropped, a new
  open one is adopted, a new one under the home, in a `Closed` folder, under Homebrew's `var`,
  outside every open folder, or shared is pending, and a reorder keeps every state; a reorder
  that moves `kubectl` logs once, naming both folders.
- `TestARefusedEntryHoldsTheSync`: with a `path` entry refused at open, `Held("path")` is true,
  a write that leaves `path` alone keeps the refused entry in the file, Include is refused and
  writes nothing, a new open entry is pending, and the sync's write ends the hold and leaves the
  file readable; the next sync adopts a new open entry again.
- `TestARemovalWhileHeldKeepsTheSyncStrict`: with `path` held, Remove writes and ends the store's
  hold, and the next sync still files a new open entry `pending`; the one after adopts it.
- `TestTheLastFaultIsKept`: a launch fault is answered until a refresh answers, a refresh's fault
  replaces it, and a timed-out sync changes nothing.
- `TestAHeldFieldRefusesAnUnnamedChange` (`store_test.go`): an `Update` that changes a held
  field without naming it answers `ErrHeld` and writes nothing; one that names it writes and ends
  the hold; one that leaves the field alone writes and keeps it.
- `TestAnAdoptedEntryThatBecameSharedWaits`: a shell-adopted entry made group-writable by a
  non-admin group comes back `pending` and shared; a user-adopted one stays `adopted`.
- `TestAnAdoptedEntryNoLongerOpenWaits`: with `System` narrowed under a shell-adopted entry, the
  sync files it `pending`; a user-adopted one stays `adopted`.
- `TestARemovalOutlivesTheEntrysAbsence`: an entry under `open` removed, then a sync without it,
  then a sync with it again: it is still `gone`, source `user`, and not on the run's `PATH`.
- `TestSyncPathRefilesAMovedEntry`: an adopted link repointed from an open folder into the home
  comes back `pending`, source `shell`, with the new `Target`; one repointed to another open folder
  stays `adopted` with the new `Target`; a gone one stays `gone`.
- `TestAFailedResolutionKeepsTheList`: `RefreshPath` with a resolver answering a `Fault` changes
  nothing and answers an error naming the reason.
- `TestAdoptAndDropMoveOneEntry`: Include on a pending and on a gone entry, Remove on an adopted
  and on a pending one, each refused when the entry is not listed or already there; Include keeps
  the stored `Target` even when the link now resolves elsewhere; and a gone entry survives the
  next sync.
- `TestPathEntriesPersist`: entries written survive a reopen, in order, with their states and
  sources; an entry with a relative `dir`, a repeated `dir` or an unknown state is left out and
  listed by `Refused()`, as step 1C's convention says.

**`bash`**

- `TestTheRunFreezesThePath`: over a fake sandbox, the run's `PATH` is the folders `CheckRun`
  answered, joined, and its Read rules are those folders less the ones `System` covers; a
  store changed after `sandboxedRunFor` changes neither; and with none adopted, the default.
- `TestTheRunLeavesOutAMovedEntry`: an adopted entry whose link is repointed after the sync into
  a folder that is not open is neither on the run's `PATH` nor a Read rule, and one log line
  names it; the other entries run.
- `TestTheRunLeavesOutAnEntryNoRuleOpens`: a stored adopted entry inside a `Never` path, or now
  world-writable, is left out and logged, and the run's policy passes `Check`; a shell-adopted
  entry now shared or no longer open is left out, a user-adopted one runs; a group's name is
  looked up once across runs.
- `TestTheRunFollowsALinkIntoAnOpenFolder`: an adopted `current` link repointed from one open
  folder to another runs on the new one, logged, with the stored list unchanged; repointed into
  the home it is left out.
- `TestAPendingEntryIsNotOnThePath`: a program in a pending folder is not found in a sandboxed
  `command -v`, and is found once the entry is adopted.

**`graph`**: `TestSandboxPathMutationsAnswerTheList`, each refusal a `KSTACK_VALIDATION_ERROR`;
`sandboxPathFault` answers the `Service`'s reason, and null on a machine with no sandbox.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-path.test.tsx`)

- The entries draw in order with their tags and the shared line, a `target` only where it
  differs from the `dir`,
  Include on waiting and removed entries, Remove on included and waiting ones, and nothing while
  `sandbox.available` is false.
- Include, Remove and Refresh PATH call their mutations, are disabled in flight, and redraw from
  the answer; a refused refresh draws its reason, and a refused Include its message.
- A set `sandboxPathFault` draws its line above the list, the empty-list wording with no entries,
  and `useSandboxPath` reads it again after each refresh.

## Security

**Narrowed.** Before this step a startup file changed every sandboxed command's `PATH` at the
next launch, unseen. After it, `PATH` is resolved from the account's shell with a scrubbed
environment, filtered, and frozen: only an entry already readable to every run is adopted
unasked, a world-writable folder never gets in, one writable by a group that is not an
administrators' one waits for the user, the
denied-always list and Kstack's directories filter at the sync and again at the run, and `Check`
still refuses a rule inside them. An entry's approval holds for the folder it resolved to: a link
repointed after it grants nothing until a sync files it again, and a removal holds while the entry
is off the shell's `PATH`, and through a hand edit the store cannot read.

**Widened.** An entry the user includes is a Read rule: a folder that step 2A's lists do not
open — under the home, in a `Closed` folder, or outside every open folder — becomes readable to
every sandboxed run, the folder and what is under it. Only Include opens one.

**Residuals.** The shell runs unconfined until step 4A. A startup file can put a folder first on
`PATH` and so choose which `kubectl` runs, among the open folders; the log line says so once. A
program in an adopted folder that links into a folder no rule opens is found but cannot run;
step 6A's probe names the denial. A command outside the sandbox (step 1B) can edit a startup
file, as it can edit anything of the user's. A folder an administrators' group can write is
adopted unasked: another administrator, or a process running as one, can plant a program in it
with no password, where `sudo` would ask for one. On a machine with one administrator, the
common case, that is the user.

The record, `docs/security/<date>-path-from-the-login-shell.md`, argues each of these.

## When it lands

- **The security record** above, and **an ADR**: `PATH` is the user's shell's, filtered and
  frozen; a new entry that would open more waits for the user, a shared one included.
- **The note**: *Filtering* says a group-writable folder waits for the user unless the group is
  an administrators' one, and *Freezing and
  syncing* says the list is frozen per run.
- **`security-model.md`**: the shell-import row says the account record's shell, one run, a
  scrubbed environment; the sandboxed-command row says the frozen list.
- **`sidecar/CLAUDE.md`**: *Shell environment on macOS* becomes the login shell resolution
  (`Resolve`, `Path`, `AccountShell`, the kinds, the scrubbed environment, `Config.ShellFault`);
  `Settings.Path`, the filter and the sync under `securityconfig`, and `Held` with the store's guard
  on a held field in its store's paragraph; the run's frozen `PATH` and Read rules in the Bash
  tool's paragraph; `Config.ShellPath`.
- **Root `CLAUDE.md`**: the Settings dialog's Sandbox section and `useSandboxPath`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on macOS and on Linux, on a machine whose startup file puts
`~/.local/share/mise/shims` and `~/scripts` (holding a `hello` script) on `PATH`:

- Settings shows the mise shims *included*, since step 2A's toolchain list already opens mise's
  folder, and `~/scripts` *waiting for you*. A sandboxed `command -v hello` finds nothing, and
  finds the script after Include.
- `/opt/homebrew/bin` (or `/usr/local/bin`) is *included*, group-writable by `admin` or not, and
  a sandboxed `command -v kubectl` finds Homebrew's.
- A folder made group-writable by a group of its own (`chgrp staff ~/bin2 && chmod g+w ~/bin2`)
  and put on `PATH` is *waiting for you* and *shared with its group*.
- `export PATH=~/Documents/bin:$PATH` in the startup file, then Refresh PATH, lists the entry
  *waiting for you*; `export PATH=~/.ssh:$PATH` lists nothing.
- Homebrew's `bin` removed stays *removed* across a restart.
- With `nu` as the account's shell and `PATH` set in its `env.nu`, Settings lists that entry.
- A Windows build (`GOOS=windows go vet ./...`) passes.
