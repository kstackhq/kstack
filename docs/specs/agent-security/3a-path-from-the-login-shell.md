---
title: "`PATH` from the login shell"
scope: sidecar, webview
status: Planned
---

# `PATH` from the login shell

**Needs:** step 2A, whose `System(home, shell)` decides which new entries are adopted unasked and whose
environment table this step's `PATH` row fills, and step 1C, whose store keeps the list.
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
  sandbox ([the note](../../notes/sandbox-credentials-and-permissions.md)'s *Resolution*).
- **It is filtered.** Empty, relative, missing, world-writable and project-local entries go, and
  so does anything under the denied-always list or Kstack's directories. The shell's order stays
  (the note's *Filtering*).
- **It is frozen** in the settings file, `<data>/security.json`, which step 1C's
  `securityconfig` keeps. A run reads the list once when it starts. At each launch the fresh list is
  diffed against the stored one: an entry under an open zone is adopted, one under the home waits
  for the user, one that disappeared is dropped unless the user removed it (the note's *Freezing
  and syncing*). Each entry keeps the folder it resolved to, and one that resolves elsewhere is
  filed again as if new.
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

// Path is Resolve's Path alone: the refresh's reader.
func Path(ctx context.Context) ([]string, *Fault)
```

**One run at launch prints both.** `main` calls `Resolve` once, before the app exists, on macOS
and Linux. On macOS it installs `Env` process-wide as `importShellEnv` does today; on Linux it
installs nothing. Either way it hands `Path` to the app as `app.Config.ShellPath` (nil after a
`Fault`, logged with its fixed reason and never a value, as today). So the startup files run once
per launch. The refresh (§4) is a second run, on request, and takes `PATH` alone. `Import` goes.

**The shell is the account record's**, never `$SHELL`: `/usr/bin/dscl . -read /Users/<name>
UserShell` on macOS, the user's line in `/etc/passwd` on Linux, `<name>` from `user.Current()`.
`AccountShell()` answers it when it names an executable file by absolute path; else the platform's
own (`/bin/zsh`, or `/bin/bash` then `/bin/sh` on Linux), and `Fault{Reason: "no shell"}` when
none is. `Find()` stays for the Bash tool, which picks the shell a command runs in; that is not
this step's question.

**The command follows the shell's kind**, read off its base name:

| Kind | Shells | Flags | Command |
| --- | --- | --- | --- |
| `posix` | `zsh`, `bash`, `sh`, `dash`, `ksh`, `fish` (it parses today's command), and any name not `nu` | `-i -l -c` | today's `command`: the cwd frame, then one `printenv` per imported variable, between the markers |
| `nu` | `nu` | `-l -c` | two frames between the same markers: `^/bin/pwd`, then `$env.PATH \| str join ":"` |

`parse` takes the number of frames the kind prints, so a nushell run yields `Path` and an empty
`Env`. `Launch` keeps the markers, `maxOutputBytes`, `DefaultTimeout`, the session, the kill and
the reap.

**The environment is scrubbed.** `Launch` builds the shell's environment from a fixed list and
nothing else: `HOME`, `USER`, `LOGNAME`, `SHELL` (the shell being run), `TMPDIR`, `LANG` and `TZ`
copied from the process when set, `TERM=dumb`, `DISABLE_AUTO_UPDATE=true`, and `PATH` set to the
platform's login default (`/usr/bin:/bin:/usr/sbin:/sbin` on macOS, `/usr/local/bin:/usr/bin:/bin`
on Linux), where a terminal's login shell starts from too. So a dev run from a terminal and a
Finder launch resolve the same list, and no startup file sees the sidecar's variables.

**Failure keeps the stored list.** A `Fault` — no shell, exit, bad output, output limit, timeout —
leaves `security.json` as it was and logs one line with the reason, not a startup error.

### 2. Filtering

`securityconfig.FilterPath(entries, closed []string) (kept []PathDir, dropped map[string]int)`
takes the raw list and `closed`, `Sandbox.Never(home)` plus Kstack's three directories
(`bash.Paths.DeniedDirs`). It drops, in this order, counting each rule's drops:

| Rule | Drops | Why |
| --- | --- | --- |
| empty | `""` | nothing to search |
| relative | `.`, `..`, anything not absolute, a leading `~` | it would resolve to the workspace, which is writable |
| missing | a path that is not a directory after following links | nothing to read |
| shared | a directory writable by others, or group-writable without the sticky bit | a persistence vector for anything else on the machine |
| closed | a path under one of `closed`, or one of them | the denied-always list wins; Kstack's files stay Kstack's |
| project | a path with a `node_modules` component | a project folder is not user binaries (see Decisions) |

The shell's order is kept; a duplicate keeps its first place. The count per rule goes on one log
line, never an entry. Every check is on the resolved path. A kept `PathDir` holds both: `Dir`, the
entry as the shell gave it, and `Target`, the path it resolved to.

### 3. `Settings.Path`

The store is `securityconfig` ([`sidecar/CLAUDE.md`](../../../sidecar/CLAUDE.md#security-settings-internalsecurityconfig)). This step adds the `Path` field to its
`Settings`, in `securityconfig/path.go`:

```go
// In securityconfig.Settings.
Path []PathEntry `json:"path,omitempty"` // in the shell's order

// PathEntry is one folder of the user's PATH and what the sandbox does with it.
type PathEntry struct {
	Dir    string    `json:"dir"`    // as the shell gave it
	Target string    `json:"target"` // what Dir resolved to when the state was set
	State  PathState `json:"state"`  // adopted, pending or gone
	Source Source    `json:"source"` // shell or user
}
```

| State | Meaning |
| --- | --- |
| `adopted` | on the sandbox's `PATH` and readable in it |
| `pending` | the shell lists it, it is under the home or another closed zone, and the user has not said yes |
| `gone` | the user removed it; kept, whether or not the shell lists it, so a sync does not adopt it again, and off the sandbox's `PATH` |

`Source` says whose decision the state is: `shell` when the sync set it, `user` when Include or
Remove did. A state holds for `Target` alone: the approval is of the folder the entry resolved
to, not of its spelling. The field's line in step 1C's `check` refuses an entry whose `dir` or
`target` is not absolute or whose state or source is not one of the names above, so a
hand-edited entry is left out and shown, as every field is.

### 4. Freezing and syncing

**A run reads the list once.** `sandboxedRunFor` in `tools/bash/bash.go` calls `AdoptedPath()`
on the store at the run's start and resolves each entry. An entry that no longer resolves to its
`Target` is left out of the run, and one log line names it; the next sync files it again. The
run uses the rest, one list, for both:

- `PATH` in `sandboxedRunEnv` (`tools/bash/env.go`), the row step 2A left as the sidecar's:
  the resolved entries joined in order. With none adopted — a first launch whose resolution
  failed — the platform's login default from §1, which `System` already reads, so `ls` still runs.
- One Files Read rule per resolved entry that no Read rule of `System(home, shell)` already covers: the
  entry's `Target` alone, never a tree, since step 2A's toolchain list covers the trees. An
  entry inside an Always path never reaches here: the filter dropped it.

Nothing re-reads the store while the run lives, a background task included; a refresh or an
Include changes the next run.

**The sync.** `(*Service).SyncPath(ctx, resolved []string) (PathReport, error)` in
`securityconfig/service.go`, where `Service` is the store plus what the sync needs: the filter's
`closed` list, `open` (the Read paths of `Sandbox.System(home, shell)`), and `Resolve func(ctx)
([]string, *loginshell.Fault)` for the refresh. It filters `resolved`, then diffs against the
stored entries:

| Case | What happens |
| --- | --- |
| an `adopted` or `pending` entry the shell no longer lists | dropped from the list |
| a `gone` entry the shell no longer lists | kept, after the listed entries: the removal outlives the entry's absence |
| a new entry under one of `open` | `adopted`, source `shell`: it adds no readable surface |
| a new entry under the home or another closed zone | `pending`, source `shell` |
| an `adopted` or `pending` entry whose `Target` changed | filed again as a new entry with the new `Target`: `adopted` under `open`, else `pending`, source `shell` |
| a `gone` entry whose `Target` changed | stays `gone`, with the new `Target` |
| the order changed | the stored entries are put in the shell's order; states are kept |

After the diff, if the first adopted folder holding `kubectl` or `helm` differs
from before, one log line names the tool and both folders. The report carries the four lists.

`app.New` builds the `Service` over the store it opened (step 1C), with `loginshell.Path`
as its resolver, and runs `SyncPath(cfg.ShellPath)` in `Start`, before the shell snapshot's part,
when `ShellPath` is not nil. **Refresh PATH** is `(*Service).RefreshPath(ctx)`: `Resolve`, then
`SyncPath`; a `Fault` answers an error naming its reason and changes nothing. Include is
`AdoptPath(dir)`: a `pending` or `gone` entry becomes `adopted`, source `user`, for the `Target`
it holds, never one resolved again, so Include approves the folder the user was shown. Remove is
`DropPath(dir)`: an `adopted` or `pending` entry becomes `gone`, source `user`. A `dir` not
listed, or already in the state asked for, is refused.

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
}

extend type Query {
  "The frozen PATH, in the shell's order. Empty on a machine with no sandbox."
  sandboxPath: [SandboxPathEntry!]!
}

extend type Mutation {
  "Include a pending or removed entry. Refused KSTACK_VALIDATION_ERROR for a dir not listed or already included."
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
is true. `useSandboxPath()` in `src/lib/sandbox-path.tsx` is the one reader of the query and the
mutations.

- The entries in order, each with its `dir` in mono through `VisibleText` (a folder name is text
  the shell produced), its `target` under it the same way when the two differ, and its state as a
  tag: *included*, *waiting for you*, *removed*.
- **Include** on a *waiting* or *removed* entry, **Remove** on an *included* or *waiting* one,
  and **Refresh PATH** above the list. Each calls its mutation, is disabled in flight, and is
  handed back on an error, which `errorReportExchange` reports; a refused refresh draws *Your
  shell did not answer: <reason>.* under the button.
- One line under the list: *Sandboxed commands find programs in these folders, in this order.
  Kstack reads them from your shell at each launch; a new folder under your home waits for you.*

### 7. Windows

No sandbox: `Resolve` is not called, `Config.ShellPath` stays nil, the store opens with no
`Path` (step 1C opens it everywhere), the query answers an empty list and the section is not
drawn.

## Decisions this step asks for

- **`node_modules/.bin` is dropped.** The note leaves it open. A project folder is not user
  binaries: its programs are whatever the last `npm install` fetched. Step 4D grants one.
- **One shell run at launch, not two.** `Import` and `Path` read one `Result`, so the startup
  files run once, and the refresh is the only second run.
- **A removed entry is kept as `gone`, whether or not the shell lists it.** Without a tombstone
  the next launch would adopt an open-zone entry back, and Remove would mean nothing. An entry
  that leaves the shell's `PATH` for a launch and comes back is the same case. The tombstones
  stay in `Path` rather than a list of their own, so a removal has one record; the cost is that
  `Path` can hold folders the shell no longer lists, drawn *removed*. Recommended.
- **An entry's approval is bound to the folder it resolved to.** A `PATH` entry is often a link
  (`/usr/local/bin`, a version manager's `current`), and repointing it must not change what the
  sandbox reads unasked. So each entry stores its `Target`, a run leaves out an entry that
  resolves elsewhere, and the sync files it again. The alternative, storing the resolved path in
  place of the entry, loses the shell's spelling the user recognises and still needs a check at
  the run. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Resolve`, `Path`, `AccountShell`, the shell kinds, the scrubbed environment; `main` resolves once | `loginshell/`, `sidecar/main.go`, `sidecar/main_darwin.go`, `sidecar/main_default.go`, `app/app.go` (`ShellPath`), their tests | — | Planned |
| 2 | `Settings.Path`, its check line, and the filter | `securityconfig/path.go`, `securityconfig/check.go`, their tests | — | Planned |
| 3 | The sync, the refresh, Include and Remove | `securityconfig/service.go`, its tests | 1, 2 | Planned |
| 4 | The run freezes the list: `PATH` and the Read rules | `tools/bash/bash.go`, `tools/bash/env.go`, their tests | 3 | Planned |
| 5 | The wire and the resolvers | `sidecar/graph/schema.graphqls`, `graph/`, `app/app.go`, generated code | 3 | Planned |
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
- `TestTheShellsEnvironmentIsScrubbed`: the shell sees §1's list and nothing else.
- `TestAFaultAnswersNoPath`, for each fixed reason; and `TestResolveIsRunOnceAtLaunch`
  (`main_unix_test.go`): the launch spawns the shell once and hands `Config.ShellPath` its answer.
- Every existing test, `Import`'s renamed to `Resolve`'s.

**`securityconfig`**

- `TestFilterPathDropsEachRule`: one case per row of §2's table, over a fixture folder with a
  world-writable directory, a group-writable one with and without the sticky bit, a link into a
  closed path, and a `node_modules/.bin`; the kept list is in the shell's order, as given.
- `TestSyncPathDiffsFourWays`: an adopted or pending entry that disappeared is dropped, a new one
  under `open` is adopted, a new one under the home is pending, and a reorder keeps every state;
  a reorder that moves `kubectl` logs once, naming both folders.
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
  sources; an entry with a relative `dir` or an unknown state is left out and listed by
  `Refused()`, as step 1C's convention says.

**`bash`**

- `TestTheRunFreezesThePath`: over a fake sandbox, the run's `PATH` is the adopted entries
  resolved and joined, and its Read rules are those entries less the ones `System` covers; a
  store changed after `sandboxedRunFor` changes neither; and with none adopted, the default.
- `TestTheRunLeavesOutAMovedEntry`: an adopted entry whose link is repointed after the sync is
  neither on the run's `PATH` nor a Read rule, and one log line names it; the other entries run.
- `TestAPendingEntryIsNotOnThePath`: a program in a pending folder is not found in a sandboxed
  `command -v`, and is found once the entry is adopted.

**`graph`**: `TestSandboxPathMutationsAnswerTheList`, each refusal a `KSTACK_VALIDATION_ERROR`.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-path.test.tsx`)

- The entries draw in order with their tags, a `target` only where it differs from the `dir`,
  Include on waiting and removed entries, Remove on included and waiting ones, and nothing while
  `sandbox.available` is false.
- Include, Remove and Refresh PATH call their mutations, are disabled in flight, and redraw from
  the answer; a refused refresh draws its reason.

## Security

**Narrowed.** Before this step a startup file changed every sandboxed command's `PATH` at the
next launch, unseen. After it, `PATH` is resolved from the account's shell with a scrubbed
environment, filtered, and frozen: a new entry under the home waits for the user, a
world-writable folder never gets in, the denied-always list and Kstack's directories still
filter before the policy sees the entry, and `Check` still refuses a rule inside them. An entry's
approval holds for the folder it resolved to: a link repointed after it grants nothing until a
sync files it again, and a removal holds while the entry is off the shell's `PATH`.

**Widened.** An adopted entry is a Read rule: a folder the shell put on `PATH`, outside the home,
that step 2A did not list, becomes readable — one folder, never a tree, unasked only in an open zone.

**Residuals.** The shell runs unconfined until step 4A. A startup file can put a folder first on
`PATH` and so choose which `kubectl` runs, in an open zone; the log line says so once. A command
outside the sandbox (step 1B) can edit a startup file, as it can edit anything of the user's.

The record, `docs/security/<date>-path-from-the-login-shell.md`, argues all three.

## When it lands

- **The security record** above, and **an ADR**: `PATH` is the user's shell's, filtered and
  frozen; new entries under the home wait for the user.
- **`security-model.md`**: the shell-import row says the account record's shell, one run, a
  scrubbed environment; the sandboxed-command row says the frozen list.
- **`sidecar/CLAUDE.md`**: *Shell environment on macOS* becomes the login shell resolution
  (`Resolve`, `Path`, `AccountShell`, the kinds, the scrubbed environment); `Settings.Path`,
  the filter and the sync under `securityconfig`; the run's frozen `PATH` and Read rules in the
  Bash tool's paragraph; `Config.ShellPath`.
- **Root `CLAUDE.md`**: the Settings dialog's Sandbox section and `useSandboxPath`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on macOS and on Linux, on a machine whose startup file puts
`~/.local/share/mise/shims` on `PATH`: Settings shows the entry *waiting for you* and Homebrew's
folders *included*; a sandboxed `command -v mise` finds nothing, and finds the shim after Include;
`export PATH=~/Documents/bin:$PATH` in the startup file, then Refresh PATH, lists no such entry;
Homebrew's `bin` removed stays *removed* across a restart.
