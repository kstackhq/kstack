---
title: Path grants
scope: sidecar, webview
status: Planned
---

# Path grants

**Needs:** step 2C, whose session carries the folders; step 3B, whose `permissions.Rule` a grant
is and whose `chat_grants` and `securityconfig` rules keep it; step 1A, whose `Always` part is
the floor a grant never lowers; step 3A, whose zones and stored `PATH` a grant is checked
against; step 3C, whose verdict a folder rule must never reach (§1). **Unblocks:** steps 5B and
6A, which offer a grant from a denial and from a probe.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command reads the system, the toolchain folders and its workspace, and nothing
else of the user's. A tool that keeps its data under the home, or a repository the user wants
the agent to read, is out of reach until the user switches the chat out of the sandbox (step 1B),
after which every command asks.

After this step, on macOS and Linux:

- **The user grants a folder**, read or read-write, **for a chat or always**
  ([the note](../../notes/sandbox-credentials-and-permissions.md)'s *User grants*: "let the agent
  see `~/code/my-service`"). A grant is a `permissions.Rule` of class 1 (read) or 2 (read-write):
  a chat's is a `chat_grants` row, an always one is in `securityconfig`'s rules.
- **A grant is a Files rule on the run's policy**, Read or Write, and **the Always part still
  wins**: a grant of `~` reads the home and not `~/.ssh`, `~/.kube` or Kstack's directories.
  This is the note's first invariant, "the single most important invariant in the sandbox",
  which `TestTheDeniedAlwaysListWinsOverARead` pins for the policy and this step pins for a
  grant. `~/Documents`, `~/Desktop` and `~/Downloads` are step 2A's `Closed` folders: a grant of
  `~` leaves them shut, a grant of a folder inside one opens that folder, and a grant of exactly
  one is refused (§3).
- **A grant is checked when written and every time it is read**: absolute, existing, no link on
  it or on its way, not `/`, not on or under a mount every run has, not inside a never-readable
  path or Kstack's directories, not exactly a closed folder, and, read-write, not on, under or
  over a path Kstack reads tools from or a path Kstack knows runs code outside the sandbox. The
  home itself may be granted, read-only, with a warning. `foldersFor` is the one builder of a
  session's folders, and it answers only folders that pass, so the sandbox and the file tools
  read one checked list.
- **No folder applies where no sandbox confines the run**: on Windows, on Linux with no usable
  `bwrap` or user namespaces, after a failed probe, and in a chat switched outside the sandbox.
  There the file tools ask outside the workspace as today.
- **`Read`, `Write` and `Edit` in a granted folder run unasked**, as they do in the workspace
  (Decisions, 1). The Always part holds there too: the tool walks the path one handle at a time
  from `/`, through the granted folder, so a link inside a granted `~` to `.ssh` reads nothing.
- **Settings: Sandbox gains *Folders***, the always grants with Add and Remove, and the
  denied-always list read-only under *Never readable*. **The composer's *Allowed for this chat*
  list** (the seam shared with step 4B) shows the chat's folder grants beside its other
  rules, each with Remove. The chat's grants ride the question's context, so the model knows what
  it can read.

Nothing prompts for a folder: a command that cannot read one fails, and step 5B turns the
failure into the offer.

## What is not in this step

- **No denial found or drawn.** Step 5B finds the path a run was refused and draws *Grant…*
  under the call; it calls this step's mutation.
- **No probe.** Step 6A runs the curated tools and offers a grant per denied path.
- **No monitor session.** Step 6B builds it, with no folders, and adds the test that no chat's
  grant reaches it (`TestAChatsFoldersNeverReachTheMonitor`). This step makes a session with no
  `Folders` function read none, and pins that (`TestASessionWithNoFoldersGetsNone`).
- **No grant of a file.** A grant is a folder.
- Nothing changes where no sandbox confines the run — Windows, a Linux machine with no sandbox,
  a failed probe, a chat switched outside the sandbox: no grant applies, and `Read`, `Write` and
  `Edit` ask outside the workspace as today. On a machine with no sandbox no grant is offered
  either.

## Design

### 1. A grant is a rule

`permissions.Rule` gains one field:

```go
Folder string `json:"folder,omitempty"` // classes 1 and 2: the folder, absolute and resolved
```

A folder grant is `permissions.Rule{Effect: Allow, Class: ReadInside, Folder: path}` for read,
and `Class: WriteInside` for read-write. A grant is never an action: no command asks
`Authorize` about a folder, and the rules are read by `foldersFor` alone. `Rule.Matches` answers
false for a rule naming a `Folder` against any action, so a folder rule never reaches a verdict
`Authorize` gives, whatever its effect — today `Authorize` checks `Deny` and `AskFor` rules before
it permits classes 1 and 2, and a folder rule must not match there (step 3C §2);
`TestAFolderRuleMatchesNoAction` pins it. The path is stored as given, and §3 refuses one whose
resolution (`filepath.EvalSymlinks`) differs from it, so the folder the user sees is the folder
the sandbox checks, and every later check compares the path's resolution with it.

**Where it lives.** A chat's grant is a `chat_grants` row, read live by `grantsFor` (step 3B).
The always grants are rules in `securityconfig`'s `Rules`, beside the cluster rules: there is no
second list. `ruleClasses` gains classes 1 and 2, and `ruleRefusal` their case — `Effect`
`Allow`, `Folder` absolute, clean and not `/`, every other field unset — and the cluster
classes' case refuses a `Folder`. Its message for an unknown class names 1 and 2 beside 4 and 5
(today *only 4 (cluster writes) and 5 (destructive cluster writes)*), and the comment on
`permissions.Class` stops saying 2 is still to come. That check reads the value alone, never
the disk, as every read-back check does; the checks that need the disk run whenever `foldersFor`
reads the rules (below), at the run's start (§2) and when Settings reads (§4). While the store holds
`rules`, `Store.Rules()` drops every `Allow`, so a file Kstack cannot read grants no folder.

`Rule.Line()` spells a grant *Allow reads of /Users/me/code*, or *Allow reads and writes of …*.
The folder is drawn as step 4B draws a literal value: bare when it holds no space, `"` or `\`,
else in quotes with `"` and `\` escaped (*Allow reads of "/Users/me/My Code"*). If this step
lands first, it adds that quoting for `Folder` alone, and step 4B's extends it to the pattern
fields.
The Permissions section lists every rule by its line, so a folder grant shows there too; its Add
form keeps offering the cluster classes alone, and a folder is granted in the Folders section.

`session.Session` gains a field:

```go
// Folder is one folder a session's commands may read, or read and write.
type Folder struct {
	Path  string
	Write bool
}

// Folders is the session's grants, the chat's joined with the always ones.
// Nil reads none.
Folders func(context.Context) []Folder
```

`chatsvc` sets it to a function calling `foldersFor(ctx, chatID)`: the class 1 and 2 rules from
`grantsFor(chatID)`,
then those from `security.Rules()`, each as a `Folder`, **less every folder that fails
`CheckFolder`** (§3) against the service's zones snapshot. A folder left out is logged with its
reason, and Settings and the composer's list draw the same reason (§4, §6). So `bash` and the
file tools read one checked list, and a rule hand-edited into `security.json` — a read-write
`~`, a read of `/proc` — reaches neither. The checks run under `SyncTimeout`, and a folder whose
check runs out is left out.

**No folder applies where no sandbox confines the run.** `foldersFor` answers none when the
service's `sandboxStatus` is not `Available` (Windows, Linux with no usable `bwrap` or user
namespaces, a failed probe) or the service has no zones snapshot, and `sessionFor` sets a
`Folders` that answers none for a chat switched outside the sandbox (`Outside`). The file tools
then skip for the workspace alone, as today.

`foldersFor(ctx, "")`, with no chat, answers the always folders alone, checked the same way. The
service exports it as `FoldersFor(ctx, chatID)`, and `app` hands step 6A's probe
`FoldersFor(ctx, "")`, so a probe sees what a chat's run would.

It is the one builder: a session made any other way has a nil `Folders`, which every reader
takes as none. `Narrow` copies it, as it copies `Policy`. `sandboxedRunFor` reads it once when
the run starts, so a grant written mid-run applies to the next command, and a grant revoked
mid-run still holds for a command already running, background ones included, since a sandbox
is per command.

### 2. The policy

`sandboxedRunFor` in `tools/bash/bash.go` joins the folders into the Workspace policy's Files
(`workspacePolicy`). The folders `foldersFor` answered passed the check against the service's
snapshot; the run checks each again with `CheckFolder` (§3) against what this run will enforce.
It already builds its zones from `boxer.System` and `boxer.Never`; it passes those, with
Kstack's directories, as a `securityconfig.Zones`, and the stored `PATH` entries:

1. A folder that fails the check — moved, deleted, or replaced by a link since `foldersFor`
   read it — is left out and logged with its reason.
2. Of the rest, a folder listed twice keeps the wider grant, and a folder under a read-write one
   is dropped, since the read-write rule already covers it and `Check` refuses any rule beneath a
   Write rule. A read-write folder under a read one stays: the deeper rule wins.
3. Each is added as `Read` for a read grant, `Write` for a read-write one.

The Always part is untouched, so `Policy.Check` and both compilers give the note's answer: a Read
rule over `~` opens the home, and `~/.ssh`, `~/.kube` and the rest of `Never`, and Kstack's
directories, stay closed inside it. `Check` is still behind it: a run is never started with a
policy other than the one checked, and a grant cannot make `Check` refuse the run, since step 1
drops what it would refuse.

### 3. The rules for a grant

`securityconfig.CheckFolder(path string, write bool, z Zones, pathEntries []string) error`, over
the zones step 3A already builds (`Zones`: `Never`, the denied-always list with Kstack's
directories; `Open`, the System policy's Files, whose Read paths are the toolchain and system
folders and whose Deny paths are the `Closed` folders; `Home`), a new `Zones.NoWrite` (below) and
the `PATH` entries. It resolves the path, then compares it with `resolveZones(z)`, the zones
with every path resolved, so a never path that is itself a link (`~/.ssh` → `~/dotfiles/ssh`) is
caught at its target:

| Rule | Refused when | The reason shown |
| --- | --- | --- |
| absolute | the path is not absolute, or starts with `~` | *Name the folder by its full path.* |
| exists | it is not a directory after following links | *This folder does not exist.* |
| no link, a grant being written | the path resolves to another path: a link on it, or on its way | *`<path>` is a link to `<target>`; grant `<target>` instead.* |
| unmoved, a stored grant | the path resolves to another path: a link was put in its place, or on its way, since it was written | *This folder has moved or become a link. Grant it again.* |
| not the root | it is `/` | *The root cannot be granted.* |
| not a fixed mount | `sandbox.FixedMount(path)` is true: on or under `/proc`, `/dev` or `/tmp` on Linux; on or under `/dev` on macOS | *Every sandboxed command has its own `<mount>`; it cannot be granted.* |
| not never-readable | it is one of `z.Never`, or under one | *This folder is never readable in the sandbox.* |
| not a closed folder | it is one of `z.Open.Deny` itself, whose Deny a grant of the same path does not outrank | *Grant a folder inside it.* |
| not on a tool, read-write | `write` is set and the path is on, under or over a path of `z.Open.Read` or `pathEntries` | *Kstack reads tools from `<path>`; grant it read-only, or a folder beside it.* |
| not over a closed folder, read-write | `write` is set and a path of `z.Open.Deny` is under it | *`<path>` is closed in the sandbox; grant a folder inside it.* |
| not on code that runs, read-write | `write` is set and the path is on, under or over a path of `z.NoWrite` | *Something outside the sandbox runs what is in `<path>`; grant it read-only.* |
| not over a never path, read-write | `write` is set and a path of `z.Never` is under it | *`<path>` is never readable in the sandbox, so this folder can be granted read-only.* |
| the home | it is `z.Home` and `write` is set | *Your home can be granted read-only.* |

A read-write grant *under* a closed folder passes: `~/Documents/project` read-write is what a
grant inside a closed folder is for.

**`pathEntries`** is every stored entry's `Dir` and `Target`, whatever its state, each resolved,
and, in the service's check, every entry of the `PATH` the login shell last answered, unfiltered.
Credential plugins and commands outside the sandbox search the whole login `PATH`, the entries
step 3A filtered out and the ones the user removed (`PathGone` rows keep their `Target`)
included, so a write grant on any of them plants a binary something outside the sandbox runs.
The service keeps that list in its snapshot, read from the launch's `PATH` (`SyncPath`) and each
refresh. `bash`'s own check (§2) passes the stored entries alone; a folder reaches a run only
when both checks pass.

**`sandbox.FixedMount(p string) bool`** is new and exported, and `CheckFolder` is its one
caller: on Linux it is true on or under `/proc`, `/dev` and `/tmp`, since every run mounts its
own and the policy's rules are mounted after them (`args` in `sandbox_linux.go`), so a grant of
one, or of a folder under one, would bind the host's over the run's. Under the host's `/proc`, a
process's `root` link reaches the host's mount namespace, where the denied-always list is not
mounted over, and the sidecar's `environ` holds its API keys; the host's `/dev/pts` reaches
other terminals. On macOS it is true on or under `/dev`. Windows answers false. The unexported
`overFixedMount` stays as it is — `/tmp` and `/dev` themselves, and on or under `/proc` — since
`Command`, `System` and `SearchFolder` read it, and a run's own runtime directory is
`/tmp/kstack-<uid>` on a Linux with no `XDG_RUNTIME_DIR` (`tools/bash/rundir_unix.go`).

**`Lists.NoWrite`** is a new zone list beside `Never` and `Closed`, one shared file and one per
platform: what something outside the sandbox runs. On macOS `~/Library/LaunchAgents`; on Linux
`~/.config/systemd`, `~/.local/share/systemd/user`, `~/.config/environment.d` and
`~/.config/autostart`; on both, the shell startup folders `~/.oh-my-zsh`, `~/.zsh`,
`~/.config/zsh`, `~/.config/fish`, `~/.config/nushell`, `~/.bashrc.d` and `~/.config/direnv`,
and the startup files `~/.zshenv`, `~/.zprofile`, `~/.zshrc`, `~/.zlogin`, `~/.bash_profile`,
`~/.bash_login`, `~/.profile` and `~/.bashrc`. A Read rule may name one, a Write rule may not.
`Zones.NoWrite` is it with `~` expanded, and `resolveZones` resolves each, so a startup file that
is a link into a dotfiles repository (`~/.zshrc` → `~/dotfiles/zshrc`) refuses a read-write
grant of that repository. Step 4A's login shell runs these files, and its snapshot shapes every
command outside the sandbox and every credential plugin, so a write here reaches past the
sandbox. It is curated, not complete: a file a startup file sources from elsewhere is not on it
(Security, residuals).

The "on, under or over" rules exist for three reasons. `Check` refuses any rule beneath a Write
rule. A write grant equal to, or under, a `PATH` entry or a system Read path (`/usr/local/bin`
under `/usr`) would let a command plant a binary that a command outside the sandbox, or a
credential plugin, runs. And a command that can write a folder can rename what is under it: on
macOS a Seatbelt rule for the old path then matches nothing, so a read-write grant of `~/Library`
would let a command move `Keychains` out from under the denied-always list. A read grant cannot
rename, which is why the home may be granted read-only.

**The wide folders.** A read grant of the home, of a folder over it (`/home`, `/Users`), or of
`/Volumes` on macOS passes, and the UI draws a warning beside it. `SandboxFolders.wide` (§4) is
that list, resolved; the webview draws the line while the path typed, cleaned, is one of them,
which is exact for every path Add can take, since the sidecar refuses one that is not resolved.
For the home and over it: *This lets commands read everything in your home folder except the
credential folders Kstack knows of, listed under Never readable, and Kstack's own files. Any
other secret kept in your home becomes readable.* For `/Volumes`: *This lets commands read every
disk mounted on this Mac.*

**A grant of a folder already granted in the same place** (the chat's rows, or the always rules)
replaces that grant's mode and keeps its id, so read becomes read-write, or back, with one Add.
A chat's goes through `addGrant` with the grant's id, which replaces the rule under an id the
chat holds (the seam with step 4B); an always one through a new `Store.ReplaceRule(r)`, which
swaps the rule holding `r.ID` for `r` in place and answers `ErrNoRule` when none does. Two
grants that merely nest are allowed: §2 resolves them at the run's start.

**Kstack's log directory is one of its directories.** On macOS the host logs to
`~/Library/Logs/Kstack`, outside the data, cache and runtime directories, so a read grant of `~`
would open it. `main` sets `app.Config.LogDir` to the directory of `--log-file`, and
`pathsOf` adds it to `Paths.DeniedDirs` and to the file tools' fence when it lies outside the
other three; on Linux it is `<data>/logs` and adds nothing. Every reader of Kstack's directories
then closes it: the Workspace policy's `Always.Kstack`, `Zones.Never`, `fileguard.Fence`. Step
4A's `main` reads Kstack's directories from `cfg.App`; whichever of the two lands second adds
`LogDir` there.

**Where the zones come from.** `securityconfig` already imports `sandbox`. The two writers,
`foldersFor` and Settings take the zones from `securityconfig.Service`, which holds a snapshot:
`app`'s zones function is read when the service starts and again on each `PATH` refresh, and the
snapshot is what every check reads, so no check lists `/Users` again. A service with no zones
function — no sandbox — holds no snapshot, and refuses every folder. `bash` builds its own from
`boxer`, as it does today (§2). The disk reads `CheckFolder` makes (a `stat` and an
`EvalSymlinks` per grant) run under `SyncTimeout`, and a grant whose check runs out reads
*Kstack could not check this folder in time.*

### 4. The wire

```graphql
enum GrantDuration { Chat Always }

type SandboxFolder {
  "The rule's id."
  id: String!
  path: String!
  write: Boolean!
  "Why the folder fails the check now, so no run will take it; null when it passes."
  refused: String
}

type SandboxFolders {
  "The always grants."
  always: [SandboxFolder!]!
  "The chat's own grants, for their refused reasons; empty when no chat was named."
  chat: [SandboxFolder!]!
  "The denied-always list for this machine, with ~ expanded, read-only."
  never: [String!]!
  "The folders a read grant of which draws the wide warning, resolved: the home, each folder over it, and /Volumes on macOS."
  wide: [String!]!
}

type PermissionRule {
  # as landed, then:
  "The folder a class 1 or 2 rule grants; empty on every other rule."
  folder: String!
}

extend type Query {
  sandboxFolders(chatID: ChatID): SandboxFolders!
}

extend type Mutation {
  "Grant a folder, or change the mode of one granted in the same place. Chat writes a chat_grants rule and needs chatID; Always a securityconfig rule, and ignores it. Refused KSTACK_VALIDATION_ERROR with §3's reason."
  folderGrant(chatID: ChatID, path: String!, write: Boolean!, duration: GrantDuration!): SandboxFolders!
  "Revoke an always grant by id. A chat's grant is removed with chatGrantRemove."
  folderRevoke(id: String!): SandboxFolders!
}
```

A chat's folder grant is one of its rules, so it is listed and removed through the chat grants
seam shared with step 4B: `chatGrants(chatID)` lists it by its `Rule.Line()`, and
`chatGrantRemove(chatID, id)` removes it. Whichever of the two steps lands first adds that
query, that mutation, `addGrant`, `removeGrant` and the composer's list (§6).

`GrantDuration` is this step's, and step 5B calls `folderGrant` with it. Rule ids are
`String!`, as the Permissions section's are.

- A `Chat` grant with no `chatID` is `KSTACK_VALIDATION_ERROR`; one on a chat that is gone is
  `KSTACK_RECORD_NOT_FOUND`. An `Always` grant, or its revoke, while the store holds `rules` answers `ErrHeld` as
  a validation error naming the file.
- A path that is a link, or has one on its way, is refused with the *no link* row's reason as a
  validation error whose `rule` is `link` and whose extensions carry `target`, the resolved path,
  so the webview can offer to grant that instead.
- `folderRevoke` looks the id up in the always rules; an id that is not there, or is not a
  folder rule, is `KSTACK_RECORD_NOT_FOUND`.
- `refused` is §3's check run when the query answers, bounded as §3 says, so Settings shows what
  `foldersFor` leaves out with no store of its own. Every answer is the whole
  `SandboxFolders`, so a view redraws from one result.
- On a machine with no sandbox the query answers empty lists and the mutations are refused.

Nothing prompts for a folder. A command that cannot read one fails with the tool's own error,
and until step 5B lands the model reads the sandbox's line and says so. On Linux a closed or
never-readable folder is an empty directory inside the run, so `ls ~/Documents` lists nothing
rather than failing; on macOS it is refused.

### 5. `Read`, `Write` and `Edit` in a granted folder

Today the three file tools skip the request for a path under the workspace by name
(`fileguard.Under(tools.WorkspacePath(rt.Dir), path)`) and reach it through the workspace's
root, so a link cannot carry a write out. This step treats a granted folder the same way
(Decisions, 1), with a walk that also keeps the denied-always list out.

**Two sets, two jobs.** `fileguard.Fence` stays Kstack's directories: no file tool opens a path
inside one, asked or not, and its `stat` makes them when they are missing, as today. What the
sandbox keeps shut is a second set, `fileguard.Hidden`, built from the zones snapshot
`securityconfig.Service` holds (§3), which `app` hands the file tools as a function answering the
snapshot. Its paths are resolved in the snapshot, so reading it touches no disk:

- `Never`: the denied-always list with Kstack's directories (`Zones.Never`);
- `Shut`: the closed folders (`Zones.Open.Deny`: `~/Documents`, `~/Desktop`, `~/Downloads`).

It only stops a skip: a path it hides is never opened unasked, and asks as today. It is never
created, and a path in it that does not exist matches nothing. A path is hidden when it is under
a `Never` path, or under a `Shut` folder that lies inside the grant, as the sandbox's deepest
rule decides: a grant of `~` hides `~/Documents/x`, and a grant of `~/Documents/project` does not
hide what is in it.

**Approval, on resolved paths.** `Fence.Granted(folders []session.Folder, path string)
(session.Folder, bool)` answers the folder a path is under, and false for a path `Hidden` hides.
Both compare resolved paths: the folders are resolved already (§3 refuses any other), the
`Hidden` sets are resolved in the snapshot, and the asked path is resolved through its deepest
folder that exists, as `sandbox.Resolved` resolves one, so `/home/me/x` meets a grant of
`/var/home/me`, `/tmp/x` on macOS meets a zone at `/private/tmp`, and `~/alias/id_ed25519` with
`~/alias` a link to `.ssh` is hidden. `Write.Approval` and `Edit.Approval` `Skip` for a path
under a read-write folder; `Read.Approval` for a path under any folder. Each reads
`rt.Session.Folders(ctx)` once, and the skip rests on `foldersFor` having answered only checked
folders (§1): the file tools run no `CheckFolder` of their own, and a session whose `Folders`
answers none — no sandbox, a chat outside it, a session with no `Folders` — skips for the
workspace alone. The walk below still decides what the run reaches.

**Run takes the folder Approval decided on.** Today the loop calls `Approval`, then `Run` with the
input alone (`agent/run.go`'s `gate`, then `runOne`), so `Run` cannot know what `Approval`
decided, and reading the folders again could answer differently: a grant revoked in between, or
grants that failed to read. So:

- `tools.Approval` gains `Folder *session.Folder`: the granted folder a skip rests on, nil for any
  other skip and for a call that asks.
- A tool may implement `tools.ApprovedRunner`, `RunApproved(ctx, rt, input, a Approval)`; the
  loop keeps the gate's `Approval` and calls it in place of `Run` when the tool has it. `Read`,
  `Write` and `Edit` do; `Run` stays for callers with no gate. Step 4C hands `Approval.Network` to Bash the same way: whichever of
  the two lands first adds the interface and the loop's call, and the other adds its field.
- With `a.Folder` set, the tool reaches the path through the walk below and nothing else. With
  it nil, the tool takes its route of today (the workspace's root, or the path form for a call
  the user approved). It never reads `Session.Folders` in `Run`, and never falls back from the walk
  to the path form.

**Run, by handle.** `fileguard.Walk(folder session.Folder, rel string)` (`walk_unix.go`, over
`golang.org/x/sys/unix`, already a dependency):

1. **Reach the granted folder from `/`.** Open `/` `O_DIRECTORY`, then each component of the
   folder's stored path with `openat(dir, name, O_NOFOLLOW|O_DIRECTORY)`, checking each handle as
   step 3 does. The stored path is resolved (§1), so a link anywhere on it — the folder itself
   swapped for a link, or a parent — is `ErrMoved`, and the grant's own handle is checked like
   every other. Nothing on the way follows a link.
2. **Inside the grant**, each component but the last the same way.
3. **The check, after each open**: `fstat` the handle and `stat` each `Never` and `Shut` path now;
   a handle that is one of them is `ErrHidden`. A `Shut` folder counts only below the grant's own
   handle, since one above it (`~/Documents` over a grant of `~/Documents/project`) is the grant
   it opens. Since the walk passes through every directory between `/` and the file, a path
   through `.ssh` meets `.ssh` on the way, wherever a link pointed into it.
4. **A link inside the grant** (`ELOOP` or `ENOTDIR` on `O_NOFOLLOW`, confirmed by `fstatat`) is
   read with `readlinkat`. A target that leaves the folder is refused, as `os.Root` refuses it;
   one inside it is walked again from the grant's handle, as the folder-relative path, under a
   bound of 40 links.
5. **The last component.** `Read` opens it `O_NOFOLLOW` from the last directory's handle and
   checks it as step 3 does (a `Never` path can be a file, `~/.netrc`); a link there is followed
   as in step 4. `Write` and `Edit` keep today's write behaviour, on handles:
   - a missing parent is made with `mkdirat` from the deepest handle that exists, each new
     directory then opened `O_NOFOLLOW` and checked as step 3 does, and removed again if the
     write fails, as `makeDirsIn` does;
   - an existing file is replaced by a temporary file written with `openat` beside it and
     `renameat` over the name, both on the directory's handle;
   - a new file is placed without clobbering, as `placeNewIn` does: the temporary file is
     `linkat`ed to the name, which fails if something took the name meanwhile, then unlinked;
     where links are unsupported, an `O_CREAT|O_EXCL` open of the name.

   The name is never resolved again from a path.

The walk runs where today's route runs, inside `Read`'s `fetchWithin` and `Write`'s and `Edit`'s
`writeWithin` and `editWithin`, so a walk through a stale network mount answers at the call's
end as today's route does.

Every check is on a handle already open, so a link a command swaps in while the tool runs leads
nowhere closed (Decisions, 4). The stamp rule holds: `Write` replaces and `Edit` changes only a
file the chat read and unchanged since. `Read`'s granted route is new; today it reaches a path
outside the chat's directory through `fetchWithin`, which it keeps for a path it asked about.

**macOS privacy prompts.** A grant inside `~/Documents`, `~/Desktop` or `~/Downloads` is a folder
macOS's privacy control (TCC) guards for the app, not the sandbox. The first time a file tool or
a sandboxed command reads there, macOS may ask the user to let Kstack reach the folder; until
the user allows it, the read fails as a denial does. The Folders row for such a grant says so
under it: *macOS may ask you to let Kstack reach this folder.*

The transcript draws an unasked write in a granted folder open under its summary, as it draws one
in the workspace: a `Write` or `Edit` with no `approval` that `Succeeded`, which is the test
`summaryOf` makes today. The comment there that names the workspace alone says a granted folder
too.

A path under a read grant still asks `Write` and `Edit`, and a path under no grant asks every
tool, as today.

### 6. Settings, the composer and the context

**Settings: Sandbox** (`sandbox-settings.tsx`, step 3A's section) gains, under the `PATH` list:

- **Folders**: the always grants, each its path in mono through `VisibleText`, *read* or
  *read and write* as a tag, Remove, and a refused entry's reason in a muted line. An Add row:
  a path field, a *read and write* checkbox, Add; a refusal draws §3's reason under the field,
  and a refusal whose `rule` is `link` adds *Grant `<target>`*, which fills the field with the
  error's `target` for the user to Add. While the field, cleaned, is one of `wide`, the wide
  warning (§3) is drawn above Add before it is pressed. Adding a folder already listed changes
  its tag.
- **Never readable**: the denied-always list, read-only, under one line: *A sandboxed command
  never reads these, whatever you grant: the credential and private folders Kstack knows of, and
  Kstack's own data. A secret kept anywhere else is readable once its folder is granted.*

**The composer.** A chat's folder grants are rows of the composer's *Allowed for this chat* list
(`ChatGrants`, `chat-grants.tsx`, over `useChatGrants(chatID)` in `src/lib/chat-grants.tsx`),
mounted by `chat-composer.tsx`, the seam shared with step 4B: every chat rule by its
`Rule.Line()`, each with Remove, which calls `chatGrantRemove`. Whichever of the two steps lands
first builds the list; this step adds to it, on a folder row,
its refused reason in a muted line, read from `sandboxFolders(chatID)`'s `chat`. It is the one
place a chat's grant is seen and revoked.

`useSandboxFolders(chatID?)` in `src/lib/sandbox-folders.tsx` is the one reader of
`sandboxFolders`, `folderGrant` and `folderRevoke`, and asks the query again after each mutation
and when the window takes focus, as `useSandboxPath` does. `useChatGrants` asks its own query
again after a `folderGrant` for the chat.

**The question's context**: the `Sandbox` section step 1B added gains the chat's grants, so the
model knows what it can read before it tries: `read` and `readWrite`, each a list of paths,
`foldersFor`'s answer at the turn's start — checked, so a folder no run takes is never listed —
at most 20 in all, then `more`, how many it left out. A chat outside the sandbox lists none.
`withSection` takes a `map[string]any` for it, as step 4C's `network` key needs too; whichever
lands first makes the change. `withSandboxReplaced` finds the stale section by
its heading, the card's last, rather than by its exact text, since the folders can change
between turns, and takes the folders beside the switch. A notice turn calls it
(`chatsvc/notices.go`), so the folders reach the model there too. `contextOf` in `chats.tsx` draws the section's text as it does today, so the
webview needs no change there.

### 7. The prompt

`prompts/sandbox.md`, and `prompts/sandbox_linux.md` where it repeats the sandbox's reach, say
the sandbox reads the folders the user granted, listed in the
question's context, and writes those granted read-write; that a folder it cannot read is one the
user can grant, in Settings or from the chat, so a command refused a path should say which and
what for rather than work around it; and that `Read`, `Write` and `Edit` reach a granted folder
as they reach the workspace.

## Decisions this step asks for

1. **`Write` and `Edit` run unasked in a granted read-write folder, and `Read` in any granted
   folder.** A sandboxed command can already write there unasked, since the user granted it, so a
   `Write` asking would gate a change the shell does not; the transcript draws the write open as
   it draws a workspace write, so the user sees the bytes. Recommended. The alternative, asking
   for every write outside the workspace as today, keeps one gate the sandbox no longer has.
2. **A grant is checked every time `foldersFor` reads it and again at every run, not only when
   written, and a grant that resolves anywhere but where it did is left out.** A folder can
   move, a sandboxed command with a read-write grant above it can replace it with a link — to
   the home, or into `~/.ssh` — after the grant, and `security.json` can be edited by hand.
   Checking in the one builder means the file tools and the sandbox read the same checked list.
   Leaving a folder out, with a reason in Settings, costs a `stat` and an `EvalSymlinks` per
   grant per read, and the user grants it again if the move was theirs. Recommended.
3. **The home is read-only.** A read-write home would put every toolchain Read rule beneath a
   Write rule, which `Check` refuses, and would let a command plant a `.zshrc`. Recommended.
4. **A file tool in a granted folder follows links, and judges where each one leads by handle.**
   The sandbox checks a file at its real location, and the file tools must agree with it: a link
   inside a granted folder to a never-readable path is closed, and one to a file beside it is
   open. Walking by handle makes the answer hold against a link swapped in while the tool runs,
   which a check on the resolved path followed by an open cannot. Recommended. The alternative,
   refusing every link under a granted folder, is simpler and breaks ordinary repositories,
   whose `node_modules/.bin` and tool shims are links.
5. **Folder grants are permission rules, in the one rules store.** An always grant is read,
   held and discarded as any always rule is, and a file Kstack cannot read grants nothing.
   Recommended.
6. **Nested grants are resolved at the run's start, not refused when written.** A chat's grant
   and an always one are written in different places, and neither writer sees the other's.
   Recommended: the wider grant wins, which is what the user granted.
7. **A read-write grant is refused on, under or over anything Kstack knows runs outside the
   sandbox.** That is any `PATH` entry the login shell answered or the list stores, whatever its
   state, a system or toolchain Read path, equal included, and `Lists.NoWrite`: the folders
   launchd, systemd, the desktop's autostart and the shells read code from, and the startup
   files at their resolved targets. A write there plants code a command outside the sandbox, a
   credential plugin, step 4A's login shell or the login runs. Recommended, with `NoWrite`
   curated as the denied-always list is: what it misses is a residual, and a folder is added to
   it as `Never` is added to. A repository's own code (a `.git/config` hook, an `.envrc`) is
   not on it, since a repository is what a read-write grant is for; that is a residual. The
   alternative, a residual alone, leaves the most direct way out of the sandbox one click away.
8. **A fixed mount cannot be granted.** On Linux a grant of `/proc`, `/dev` or `/tmp` would bind
   the host's over the run's own, and the host's `/proc` reaches past every denial. Recommended;
   nothing a user needs lives there.
9. **A file tool's run takes the folder its approval decided on.** Reading the grants again in
   `Run` could answer otherwise than `Approval` did, and the route a skipped call fell back to
   would follow links anywhere. Recommended: one decision, carried to the run.
10. **A grant names its folder by its resolved path, and a link is refused with its target.** A
    grant stored as resolved while the UI shows what was typed lets a planted link turn
    `~/code/svc` into the home. Refusing the link and offering its target keeps what the user
    reads and what the sandbox checks one path. Recommended.
11. **No folder applies where no sandbox confines the run.** With no sandbox the zones are empty
    and `Hidden` hides nothing, so a grant would let `Read` open `~/.ssh` unasked. Recommended:
    the grant is a sandbox rule, and where there is none the file tools ask as today.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Rule.Folder` and its line; the folder classes in `ruleRefusal`, its unknown-class message and the `permissions.Class` comment; `Store.ReplaceRule`; `sandbox.FixedMount` and `Lists.NoWrite`; `CheckFolder`; the service's zones snapshot, with the login `PATH` it last read; `app.Config.LogDir` among Kstack's directories | `permissions/permissions.go`, `sandbox/sandbox_linux.go`, `sandbox/sandbox_darwin.go`, `sandbox/sandbox_windows.go`, `sandbox/lists*.go`, `securityconfig/permissions.go`, `securityconfig/folders.go`, `securityconfig/service.go`, `sidecar/main.go`, `app/app.go`, `app/paths.go`, their tests | — | Planned |
| 2 | `Session.Folders`; `foldersFor`, checked, none with no sandbox or outside it, and `FoldersFor`; the grant writes; the chat grants seam (`addGrant`, `removeGrant`, `chatGrants`, `chatGrantRemove`) unless 4B added it; the context section, notice turns included | `session/session.go`, `chatsvc/grants.go`, `chatsvc/statements.go`, `chatsvc/workspace.go`, `chatsvc/notices.go`, `sidecar/graph/schema.graphqls` (`chatGrants`, `chatGrantRemove`, unless 4B added them), their tests | 1 | Planned |
| 3 | The policy: grants as Files rules, checked and resolved per run | `tools/bash/bash.go`, its tests | 2 | Planned |
| 4 | `Hidden`, `Fence.Granted`, `Walk`; `Approval.Folder` and `ApprovedRunner`; the file tools skip and go through the walk | `tools/tool.go`, `agent/run.go`, `tools/internal/fileguard/`, `tools/read/`, `tools/write/`, `tools/edit/`, `app/app.go`, their tests | 2 | Planned |
| 5 | The wire, the resolvers, codegen | `sidecar/graph/schema.graphqls` (with `chatGrants` and `chatGrantRemove`, unless 4B added them), `graph/`, generated code, `src/gql/` | 2 | Planned |
| 6 | `useSandboxFolders`, the Settings rows, the folder rows of the composer's chat grants list, the transcript's comment | `src/lib/sandbox-folders.tsx`, `src/components/widgets/sandbox-settings.tsx`, `src/lib/chat-grants.tsx`, `src/components/widgets/chat-grants.tsx` and `src/components/widgets/chat-composer.tsx` (unless 4B added the list), `src/components/widgets/chat-transcript.tsx`, their tests | 5 | Planned |
| 7 | The prompt | `tools/bash/prompts/sandbox.md`, `tools/bash/prompts/sandbox_linux.md`, its test | 3 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1, then 2, then 3, 4 and 5 at the same time, then 6 and 7 at the same time, then 8.

## Tests

**`permissions`**: `TestAFolderRuleLine`, a folder with a space or a `"` drawn quoted;
`TestAFolderRuleMatchesNoAction`, a folder rule of either class and either effect against a class 1
cluster read with every cluster field unset.

**`securityconfig`**

- `TestCheckFolderRefusesEachRule`: one case per row of §3's table, over a fixture home with a
  never-readable path, a folder holding one (`~/Library` on macOS, `~/.config` on Linux) asked
  for read-write, a toolchain folder, a closed folder, a link into `~/.ssh`, a `~/.ssh` that is
  itself a link to `~/dotfiles/ssh` (a grant of `~/dotfiles/ssh` is refused as never-readable),
  and the home itself read and read-write; a read-write grant of `~/Documents/project` passes
  and one over `~/Documents` is refused.
- `TestALinkIsRefusedWithItsTarget`: a new grant of a link to the home, and of a folder whose
  parent is a link, are refused with the *no link* reason naming the resolved path, `rule`
  `link` and `target` set; the resolved path itself is accepted.
- `TestAFixedMountCannotBeGranted`: `/proc`, `/proc/1/root`, `/dev`, `/dev/pts` and, on Linux,
  `/tmp` and `/tmp/x` are refused, read or read-write (`sandbox.FixedMount` per platform in the
  plain file, the Linux cases in `folders_linux_test.go`).
- `TestAMovedGrantIsRefused`: a stored grant of `~/code/svc` whose folder was replaced by a link
  to the home, and one whose parent was, are refused with the moved reason, though the home
  itself would pass as a read grant.
- `TestAWriteGrantOnCodeThatRunsIsRefused`: a read-write grant equal to an adopted `PATH`
  entry, to a `PathGone` entry's `Target`, and to a login `PATH` entry step 3A filtered out, one
  under a system Read path (`/usr/local/bin`), one of each `NoWrite` folder and one over one,
  and `~/dotfiles` read-write with `~/.zshrc` a link into it, are refused, and each is accepted
  read-only.
- `TestNoSnapshotRefusesEveryFolder`: a service with no zones function refuses every folder.
- `TestTheZonesAreASnapshot`: checks after the service starts read no disk for the zones (a zones
  function that counts its calls is called once, and again after a `PATH` refresh), and a
  Settings check over a folder that hangs answers the timeout reason at `SyncTimeout`.
- `TestReplaceRuleKeepsItsPlace`: `ReplaceRule` swaps the rule with its id where it stands, and
  an id the settings do not hold is `ErrNoRule`.
- `TestAFolderRuleIsShapeChecked`: a relative folder, `/`, a `Deny` folder rule and a folder rule
  naming a context are refused on read-back, and a cluster rule naming a folder is too, none of
  them touching the disk; `TestHeldRulesGrantNoFolder`.

**`session`**: `TestNarrowKeepsTheParentsIdentity` grows its `Folders` case.

**`chatsvc`**

- `TestAChatsFoldersJoinTheAlwaysOnes`: the chat's rules and the file's, a chat's grant read
  live; `folderRevoke` removes an always grant and refuses a chat's id, which `chatGrantRemove`
  removes.
- `TestAGrantInTheSamePlaceChangesItsMode`: granting a folder read, then read-write, in one
  place leaves one rule with the first id, read-write; a chat grant beside an always one on the
  same folder is accepted.
- `TestFoldersForAnswersOnlyCheckedFolders`: with a hand-edited `security.json` granting `~`
  read-write and `/proc` read, neither is in `foldersFor`'s answer, each is logged and drawn
  `refused` by `sandboxFolders`, and through the session the real `Write.Approval` asks for
  `~/.zshrc` and `Read.Approval` for `/proc/self/environ`.
- `TestNoFolderAppliesWithoutASandbox`: with `sandboxStatus` not `Available`, an always grant of
  `~` is in no session's `Folders`, and `Read.Approval` of `~/.ssh/config` asks; a chat switched
  outside the sandbox gets none either.
- `TestFoldersForNoChatIsTheAlwaysOnes`: `foldersFor("")` answers the always folders, checked,
  and no chat's.
- `TestAGrantGoesWithTheChat`: a `Chat` grant cascades on delete and reaches the chat's next
  command's session, not a running one's.
- `TestTheContextListsTheGrants`: the `Sandbox` section's `read`, `readWrite` and `more`, a
  refused folder left out, a section replaced after the grants changed, on a question and on a
  notice turn.

**`bash`**

- `TestAGrantIsAFilesRule`: over a fake sandbox, a read grant is a Read rule and a read-write
  grant a Write rule on the policy, resolved, and the Always part is unchanged.
- `TestNestedGrantsResolveToTheWider`: a folder granted read for the chat and read-write always
  is one Write rule; a folder under a read-write one is dropped; `Check` passes.
- `TestAGrantThatFailsTheCheckIsLeftOut`: a folder that became a link into `~/.ssh` since it was
  written, and one that became a link to the home, add no rule, are logged, and the run starts.
- `TestASessionWithNoFoldersGetsNone`: a nil `Folders` adds no rule.
- `TestARunStartsWithItsRuntimeDirUnderTmp`, in `bash_linux_test.go`: with no
  `XDG_RUNTIME_DIR`, a run whose runtime directory is `/tmp/kstack-<uid>` starts, and
  `overFixedMount` still answers false under `/tmp` while `FixedMount` answers true.
- `TestTheLogDirectoryIsKstacks` (`app`): a `LogDir` outside the three directories is in
  `Paths.DeniedDirs` and the file tools' fence, and one inside the data directory adds nothing.
- `TestADeniedAlwaysPathStaysHiddenUnderAGrantedHome`, in `bash_unix_test.go`, through the real
  sandbox on both platforms: with `~` granted read, `cat ~/.zshrc` reads and `cat
  ~/.ssh/id_ed25519` and Kstack's `app.db` do not; `ls ~/.kube` and `ls ~/Documents` list
  nothing on Linux (an empty mount) and are refused on macOS. The note's first invariant, over a grant. A grant of `~/Documents/project` reads
  that folder and nothing else of `~/Documents` (`Closed`).
- `TestAReadWriteGrantIsWritten`, and a read grant is not, through the real sandbox.

**`agent`**: `TestTheRunTakesTheGatesApproval`: a tool with `RunApproved` is run with the
`Approval` its gate answered, and a tool without it is run with `Run`.

**`read`, `write`, `edit`**

- `TestARevokeBetweenApprovalAndRunChangesNothing`: a skip under a grant, the session's folders
  then emptied and then failing to read; the run still goes through the walk of the folder the
  approval named, and a link in it to `~/.ssh/id_ed25519` is `ErrHidden`, never read by path.
- `TestAWriteInAGrantedFolderMakesItsParents`, and a new file placed there does not replace one
  created under the same name meanwhile.

- `TestAWriteInAGrantedFolderAsksNoOne`, and `TestAWriteInAReadGrantStillAsks`, in `write` and
  `edit`; `TestAReadInAGrantedFolderAsksNoOne` in `read`.
- `TestAWalkThroughAShutFolderIsRefused`: with `~` granted, a link in the home to
  `Documents/notes.txt` read unasked is `ErrHidden`.
- `TestAGrantedWriteStaysInTheFolder`: a link out of the folder is refused, nothing outside
  changes.
- `TestAHiddenPathUnderAGrantedHomeStillAsks`: `~/.ssh/config` and `~/Documents/notes.txt`
  under a granted `~` are not skipped, and once approved each is read as today, never refused
  outright; under a grant of `~/Documents/project`, a file in it is skipped.
- `TestALinkToAHiddenPathUnderAGrantedHomeIsRefused`: with `~/alias` a link to `.ssh`, `~/deep`
  a link to `alias`, and `~/sub` a link to `.ssh/keys`, a skipped read or write through any of
  them is `ErrHidden`, and a link to a file beside them in the home is followed.
- `TestTheFenceCreatesNoClosedPath`: building and checking the fence leaves a missing `~/.ssh`
  missing.
- `TestAGrantedCallAnswersAtItsEnd`, in each: a walk held in a stalled `openat`, through the
  walk's test seam, answers when the call's context ends.

**`fileguard`**

- `TestGrantedComparesResolvedPaths`: a path under a link to a grant matches it, a path through
  a link into `.ssh` is hidden, and false for a path `Hidden` hides, a `Shut` folder inside the
  grant included.
- `TestTheWalkRefusesALinkSwappedIn`: a link to `.ssh` swapped in, through the walk's test seam,
  after `Granted` answered is `ErrHidden`, and nothing under `.ssh` is read or changed.
- `TestTheWalkChecksTheGrantItself`: the granted folder replaced by a link to `~/.ssh`, and a
  parent of it replaced by a link, are `ErrMoved`; `id_ed25519` under it is never opened.
- `TestTheWalkRenamesOnTheHandle`: a directory swapped for a link between the walk and the
  write changes nothing outside the folder.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-folders.test.tsx`, `chat-grants.test.tsx`):
the Folders rows with their tags, Remove, Add with the checkbox, a refusal's reason, a link's
refusal offering its target, Add of a listed folder changing its tag, a refused entry's line,
the wide warning for the home, a folder over it and `/Volumes` on macOS, and none for a folder
under the home, the TCC line under a grant inside a closed folder on
macOS, the *Never readable* list; a chat's folder row in the chat grants list with its refused
reason, and Remove calling `chatGrantRemove`.

## Security

**Widened.** Before this step an unasked sandboxed command read nothing of the user's beyond the
toolchain, and wrote only its workspace, its `TMPDIR` and the tool cache. After it, it reads and
writes the folders the user granted, for the chat or always, and so do `Read`, `Write` and `Edit`
without a request. What holds it: the Always part is the floor — no grant opens a credential
path, a private folder or Kstack's directories, pinned over a real grant on both platforms; a
grant is the user's own act, in Settings or on a click step 5B offers, never the model's; it
names its folder by its resolved path, a link refused with its target, so the folder the user
reads is the folder granted; it is checked when written, by the one builder every time a session
reads it and again at every run, so a hand-edited rule reaches neither the sandbox nor the file
tools, and one that resolves anywhere but where it did is left out; no folder applies where no
sandbox confines the run; no grant reaches a mount every run has its own of; a read-write grant never reaches what
runs outside the sandbox that Kstack knows of; a file tool walks a granted path by handle from
`/`, the grant's own folder checked like every other, and runs on the folder its approval
decided on, so a link, swapped or not, never leads it into a closed path; the home is read-only; a grant is listed in
Settings or under the composer, and in the question's context; an unreadable rules file grants
nothing; and an unasked write is drawn open in the transcript.

**Residuals.** A granted folder's contents leave the machine on the model's word, as cluster reads
do: a repository with a `.env` in it is readable once its folder is granted, and the denied-always
list names credential paths under the home, not inside a project. A read-write grant lets a command
plant a file a later command outside the sandbox (step 1B) or a `git` hook runs, which is the bash
tool record's residual over a wider folder. A repository also runs code with no command of the
user's: an IDE's background `git status` runs a `core.fsmonitor` from `.git/config`, and direnv runs
an `.envrc` on `cd`, so a read-write grant of a repository lets a sandboxed command plant code that
runs outside the sandbox. `NoWrite` closes the places Kstack knows of, startup files at their
resolved targets included, and any other folder something outside the sandbox runs code from is open
to a read-write grant: a file a startup file sources from elsewhere (`source ~/work/env.sh`) changes
what step 4A's login shell runs, and through its snapshot every command outside the sandbox and
every credential plugin. The home warning is honest about the same gap for reads: the denied-always
list is curated, so a secret in a folder it does not name (`~/.pgpass`, a password store, a browser
profile it misses) is readable under a grant of `~`. A folder granted always is readable by every
chat, and the user must remember it is. On macOS a read grant of `~` opens the host's own settings
folder (`host.json`, the color scheme) and its WebKit storage (the chrome's widths and open panes):
neither holds a credential or cluster data. A grant revoked while a command runs holds for that
command until it exits, a background one included, since its sandbox was built when it started. On
Linux a denied-always path under a granted folder that does not exist when a run starts is not
mounted over, so a file something outside the run makes there while it runs is readable to it (the
residual on the denied-always row of `security-model.md`); macOS holds the Deny whether or not the
path exists.

The record, `docs/security/<date>-path-grants.md`, argues both. The TODO item *Revisit how
little of the home a sandboxed command reads* closes with it: this is the allow-list it asks
for, in `security.json` rather than `host.json`, with the security record and the
`security-model.md` row it says the change needs.

## When it lands

- **The security record** above, and an ADR: a folder grant is a rule, read or read-write, for a
  chat or always, in the one rules store; the file tools treat a granted folder as the workspace
  and walk it by handle; the home is read-only.
- **`security-model.md`**: the sandbox's file row says the folders the user granted, with the
  tests; the denied-always row gains the grant test; the `Write`, `Edit` and `Read` rows say a
  granted folder and the walk; a row that a session with no folders function reads none; a row
  that `foldersFor` answers only checked folders and none where no sandbox confines the run; a
  row that no grant reaches a fixed
  mount, and one that no read-write grant reaches `NoWrite`, a `PATH` entry or a system Read path,
  with the tests.
- **`sidecar/CLAUDE.md`**: `Rule.Folder`, the folder classes' check, `Store.ReplaceRule`,
  `app.Config.LogDir` among Kstack's directories, `CheckFolder`,
  `sandbox.FixedMount`, `Lists.NoWrite`, the zones snapshot, `Approval.Folder` and
  `ApprovedRunner`,
  `Session.Folders`, `foldersFor` and `FoldersFor`, the grants on the Workspace policy and how
  nesting resolves,
  `fileguard.Hidden`, `Fence.Granted`, `fileguard.Walk`, the file tools' skip, the context
  section, the mutations.
- **Root `CLAUDE.md`**, the Settings dialog and *Chat*: the Folders rows, *Never readable*,
  the composer's list, `useSandboxFolders`.
- **`docs/TODO.md`**: the home item closes.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the sandbox's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS and on Linux: ask for `ls ~/code/my-service` and read it fail;
grant the folder read-only in Settings, ask again, and read the listing with no request; ask the
model to write a file there and read the request; grant it read-write and read the write run
unasked and drawn open; grant `~` read-only, read the warning, then ask for `cat ~/.ssh/config`
and `ls ~/Documents` and read the first refused and the second empty on Linux, refused on macOS;
ask for `cat ~/.zshrc` and read it; try to grant `/proc` and `/usr/local/bin` read-write and read
each refused with its reason; try to grant a link to `~/code/my-service` and read the refusal
offer its target. Switch the chat outside the sandbox and read a `Read` in the granted folder
ask. Hand-edit `security.json` to grant `~` read-write, and read Settings draw it refused and a
`Write` of `~/.zshrc` ask. Read the chat's
context block list the grants. Grant a folder for the chat through the mutation, read it in
the composer's *Allowed for this chat* list, and remove it there.
