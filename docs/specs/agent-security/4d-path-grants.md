---
title: Path grants
scope: sidecar, webview
status: Planned
---

# Path grants

**Needs:** step 2C, whose session carries the folders; step 3B, whose `permissions.Rule` a grant
is and whose `chat_grants` and `securityconfig` rules keep it; step 1A, whose `Always` part is
the floor a grant never lowers; step 3A, whose zones and adopted `PATH` a grant is checked
against. **Unblocks:** steps 5B and 6A, which offer a grant from a denial and from a probe.

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
- **A grant is checked when written and at every run**: absolute, existing, not `/`, not inside
  a never-readable path or Kstack's directories, not exactly a closed folder, and, read-write,
  not over a rule Kstack needs. The home itself may be granted, read-only, with a warning.
- **`Read`, `Write` and `Edit` in a granted folder run unasked**, as they do in the workspace
  (Decisions, 1). The Always part holds there too: the tool walks the path one handle at a time
  from the granted folder, so a link inside a granted `~` to `.ssh` reads nothing.
- **Settings: Sandbox gains *Folders***, the always grants with Add and Remove, and the
  denied-always list read-only under *Never readable*. **The composer's sandbox switch** lists the
  chat's own grants, each with Remove. The chat's grants ride the question's context, so the
  model knows what it can read.

Nothing prompts for a folder: a command that cannot read one fails, and step 5B turns the
failure into the offer.

## What is not in this step

- **No denial found or drawn.** Step 5B finds the path a run was refused and draws *Grant…*
  under the call; it calls this step's mutation.
- **No probe.** Step 6A runs the curated tools and offers a grant per denied path.
- **No monitor session.** Step 6B builds it, with no folders, and keeps the test that no chat's
  grant reaches it. This step makes a session with no `Folders` function read none.
- **No grant of a file.** A grant is a folder.
- Nothing changes on Windows, which has no sandbox: no grant is offered or applied, and `Read`,
  `Write` and `Edit` ask outside the workspace as today.

## Design

### 1. A grant is a rule

`permissions.Rule` gains one field:

```go
Folder string `json:"folder,omitempty"` // classes 1 and 2: the folder, absolute and resolved
```

A folder grant is `permissions.Rule{Effect: Allow, Class: ReadInside, Folder: path}` for read,
and `Class: WriteInside` for read-write. A grant is never an action: `Decide` already allows
classes 1 and 2 without reading a rule, and nothing asks `Decide` about a folder. The rules are
read by `foldersFor` alone, and `Rule.Matches` answers false for a rule naming a `Folder`
against any action, so a folder rule never meets a cluster read's verdict, whatever its effect
(step 3C §2); `TestAFolderRuleMatchesNoAction` pins it. The path is absolute and resolved (`filepath.EvalSymlinks`) when
written, so a grant names the folder the sandbox will check.

**Where it lives.** A chat's grant is a `chat_grants` row, read live by `grantsFor` (step 3B).
The always grants are rules in `securityconfig`'s `Rules`, beside the cluster rules: there is no
second list. `ruleClasses` gains classes 1 and 2, and `ruleRefusal` their case — `Effect` `Allow`, `Folder` absolute,
clean and not `/`, every other field unset — and the cluster classes' case refuses a `Folder`.
That check reads the value alone, never the disk, as every read-back check does; the checks that
need the disk run at the run's start (§2) and when Settings reads (§4). (Step 4C adds the host
class's case beside this one; whichever lands second adds its own.) While the store holds
`rules`, `Store.Rules()` drops every `Allow`, so a file Kstack cannot read grants no folder.

`Rule.Line()` spells a grant *Allow reads of /Users/me/code*, or *Allow reads and writes of …*.
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

`chatsvc` sets it to `foldersFor(chatID)`: the class 1 and 2 rules from `grantsFor(chatID)`,
then those from `security.Rules()`, each as a `Folder`. It is the one builder: a session made any
other way has a nil `Folders`, which every reader takes as none. `Narrow` copies it, as it copies
`Policy`. `sandboxedRunFor` reads it once when the run starts, so a grant written mid-run applies
to the next command, since a sandbox is per command.

### 2. The policy

`sandboxedRunFor` in `tools/bash/bash.go` joins the folders into the Workspace policy's Files
(`workspacePolicy`), after checking each with `CheckFolder` (§3) against the zones and `PATH` the
run uses:

1. A folder that fails the check — moved, deleted, turned into a link into a never-readable path
   since it was written — is left out and logged with its reason.
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
the zones step 3A already builds (`Zones`: `Never`, the denied-always list with Kstack's three
directories; `Open`, the System policy's Files, whose Read paths are the toolchain and system
folders and whose Deny paths are the `Closed` folders; `Home`) and the adopted `PATH` entries. It
resolves the path, then:

| Rule | Refused when | The reason shown |
| --- | --- | --- |
| absolute | the path is not absolute, or starts with `~` | *Name the folder by its full path.* |
| exists | it is not a directory after following links | *This folder does not exist.* |
| not the root | it is `/` | *The root cannot be granted.* |
| not never-readable | it is one of `z.Never`, or under one | *This folder is never readable in the sandbox.* |
| not a closed folder | it is one of `z.Open.Deny` itself, whose Deny a grant of the same path does not outrank | *Grant a folder inside it.* |
| not over a rule, read-write | `write` is set and a path of `z.Open.Read`, `z.Open.Deny` or `pathEntries` is under it | *Kstack reads tools from `<path>`; grant it read-only, or a folder beside it.* |
| not over a never path, read-write | `write` is set and a path of `z.Never` is under it | *`<path>` is never readable in the sandbox, so this folder can be granted read-only.* |
| the home | it is `z.Home` and `write` is set | *Your home can be granted read-only.* |

The home itself passes as a read grant, and the UI draws a warning line beside it: *This lets
commands read everything in your home folder except credentials and Kstack's own files.*

The two "over" rules exist because `Check` refuses any rule beneath a Write rule, and the second
is also a security rule: a command that can write a folder can rename what is under it, and on
macOS a Seatbelt rule for the old path then matches nothing, so a read-write grant of `~/Library`
would let a command move `Keychains` out from under the denied-always list. A read grant cannot
rename, which is why the home may be granted read-only.

When a grant is written, a folder already granted in the same place (the chat's rows, or the
always rules) is refused: *`<path>` is already granted.* Two grants that merely nest are allowed:
§2 resolves them at the run's start.

`securityconfig` already imports `sandbox`, and `app` already builds the zones for step 3A, so
both writers and `bash` read them from there.

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
  "The chat's own grants; empty when no chat was named."
  chat: [SandboxFolder!]!
  "The denied-always list for this machine, with ~ expanded, read-only."
  never: [String!]!
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
  "Grant a folder. CHAT writes a chat_grants rule and needs chatID; ALWAYS a securityconfig rule, and ignores it. Refused KSTACK_VALIDATION_ERROR with §3's reason."
  folderGrant(chatID: ChatID, path: String!, write: Boolean!, duration: GrantDuration!): SandboxFolders!
  "Revoke a grant by id, a chat's or an always one."
  folderRevoke(chatID: ChatID, id: String!): SandboxFolders!
}
```

`GrantDuration` is shared with step 4C's `networkHostGrant`: whichever of the two lands first
adds the enum, and the other uses it. Rule ids are `String!`, as the Permissions section's are.

- A `Chat` grant with no `chatID` is `KSTACK_VALIDATION_ERROR`; one on a chat that is gone is
  `KSTACK_RECORD_NOT_FOUND`. An `Always` grant, or its revoke, while the store holds `rules` answers `ErrHeld` as
  a validation error naming the file.
- `folderRevoke` looks the id up in the chat's `chat_grants` rows when `chatID` is given, else in
  the always rules; an id it finds in neither is `KSTACK_RECORD_NOT_FOUND`.
- `refused` is §3's check run when the query answers, so Settings shows what the next run will
  leave out with no store of its own. Every answer is the whole `SandboxFolders`, so a view
  redraws from one result.
- On a machine with no sandbox the query answers empty lists and the mutations are refused.

Nothing prompts for a folder. A command that cannot read one fails with the tool's own error,
and until step 5B lands the model reads the sandbox's line and says so.

### 5. `Read`, `Write` and `Edit` in a granted folder

Today the three file tools skip the request for a path under the workspace by name
(`fileguard.Under(tools.WorkspacePath(rt.Dir), path)`) and reach it through the workspace's
root, so a link cannot carry a write out. This step treats a granted folder the same way
(Decisions, 1), with a walk that also keeps the denied-always list out.

**Two sets, two jobs.** `fileguard.Fence` stays Kstack's directories: no file tool opens a path
inside one, asked or not, and its `stat` makes them when they are missing, as today. What the
sandbox keeps shut is a second set, `fileguard.Hidden`, built from a function `app` passes that
answers step 3A's zones, read on every check so a home added since counts:

- `Never`: the denied-always list with Kstack's directories (`Zones.Never`);
- `Shut`: the closed folders (`Zones.Open.Deny`: `~/Documents`, `~/Desktop`, `~/Downloads`).

It only stops a skip: a path it hides is never opened unasked, and asks as today. It is never
created, and a path in it that does not exist matches nothing. A path is hidden when it is under
a `Never` path, or under a `Shut` folder that lies inside the grant, as the sandbox's deepest
rule decides: a grant of `~` hides `~/Documents/x`, and a grant of `~/Documents/project` does not
hide what is in it.

**Approval, by name.** `Fence.Granted(folders []session.Folder, path string) (session.Folder,
bool)` answers the folder a path is under by name, and false for a path `Hidden` hides by
name. `Write.Approval` and `Edit.Approval` `Skip` for a path under a read-write folder;
`Read.Approval` for a path under any folder. Each reads `rt.Session.Folders(ctx)`.

**Run, by handle.** `Run` takes the same route `Approval` did, by the same name test, and never
falls back to the path form: a path `Approval` skipped is reached through the walk or not at all.
`fileguard.Walk(folder, rel)` (`walk_unix.go`, over `golang.org/x/sys/unix`, already a
dependency):

1. Open the granted folder `O_DIRECTORY`.
2. For each component but the last, `openat(dir, name, O_NOFOLLOW|O_DIRECTORY)`. After each open,
   `fstat` the handle and `stat` each `Never` and `Shut` path now; a handle that is one of them is
   `ErrHidden`. The walk starts at the grant, so a `Shut` folder it meets lies inside the grant.
   Since the walk passes through every directory between the folder and the file, a path through
   `.ssh` meets `.ssh` on the way, wherever a link pointed into it.
3. A component that is a link (`ELOOP` or `ENOTDIR` on `O_NOFOLLOW`, confirmed by `fstatat`) is
   read with `readlinkat`. A target that leaves the folder is refused, as `os.Root` refuses it;
   one inside it is walked again from step 1, as the folder-relative path, under a bound of 40
   links.
4. The last component: `Read` opens it `O_NOFOLLOW` from the last directory's handle and checks
   it as step 2 does (a `Never` path can be a file, `~/.netrc`); a link there is followed as in
   step 3. `Write` and `Edit` write a temporary file in that directory with `openat` and
   `renameat` it over the name, both on the handle, so the name is never resolved again.

Every check is on a handle already open, so a link a command swaps in while the tool runs leads
nowhere closed (Decisions, 4). The stamp rule holds: `Write` replaces and `Edit` changes only a
file the chat read and unchanged since. `Read`'s granted route is new; today it reaches a path
outside the chat's directory through `fetchWithin`, which it keeps for a path it asked about.

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
  and the home draws the warning line above Add before it is pressed.
- **Never readable**: the denied-always list, read-only, under one line: *A sandboxed command
  never reads these, whatever you grant. Credentials, private files and Kstack's own data live
  here.*

**The composer.** `SandboxSwitch` gains a count beside it, *2 folders*, while the chat has any
grant, opening a list of the chat's grants drawn as Settings draws them, each with Remove. It is
the one place a chat's grant is seen and revoked; it reads the same query with the chat's id.

`useSandboxFolders(chatID?)` in `src/lib/sandbox-folders.tsx` is the one reader of
`sandboxFolders`, `folderGrant` and `folderRevoke`, and asks the query again after each mutation
and when the window takes focus, as `useSandboxPath` does.

**The question's context**: the `Sandbox` section step 1B added gains the chat's grants, so the
model knows what it can read before it tries: `read` and `readWrite`, each a list of paths, the
chat's and the always ones together, at most 20 in all, then `more`, how many it left out.
`withSection` takes a `map[string]any` for it. `withSandboxReplaced` finds the stale section by
its heading, the card's last, rather than by its exact text, since the folders can change
between turns. `contextOf` in `chats.tsx` draws the section's text as it does today, so the
webview needs no change there.

### 7. The prompt

`prompts/sandbox.md` says the sandbox reads the folders the user granted, listed in the
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
2. **A grant is checked at every run, not only when written.** A folder can move or become a
   link into `~/.ssh` after the grant; leaving it out then, with a reason in Settings, costs one
   stat per grant per run. Recommended.
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

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Rule.Folder` and its line; the folder classes in `ruleRefusal`; `CheckFolder` | `permissions/permissions.go`, `securityconfig/permissions.go`, `securityconfig/folders.go`, their tests | — | Planned |
| 2 | `Session.Folders`; `foldersFor`; the grant and revoke writes, `addGrant` unless 4B or 4C added it, and `removeGrant`; the context section | `session/session.go`, `chatsvc/grants.go`, `chatsvc/statements.go`, `chatsvc/workspace.go`, their tests | 1 | Planned |
| 3 | The policy: grants as Files rules, checked and resolved per run | `tools/bash/bash.go`, its tests | 2 | Planned |
| 4 | `Hidden`, `Fence.Granted`, `Walk`; the file tools skip and go through the walk | `tools/internal/fileguard/`, `tools/read/`, `tools/write/`, `tools/edit/`, `app/app.go`, their tests | 2 | Planned |
| 5 | The wire, the resolvers, codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 2 | Planned |
| 6 | `useSandboxFolders`, the Settings rows, the composer's list, the transcript's comment | `src/lib/sandbox-folders.tsx`, `src/components/widgets/sandbox-settings.tsx`, `src/components/widgets/sandbox-switch.tsx`, `src/components/widgets/chat-transcript.tsx`, their tests | 5 | Planned |
| 7 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 3 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1, then 2, then 3, 4 and 5 at the same time, then 6 and 7 at the same time, then 8.

## Tests

**`permissions`**: `TestAFolderRuleLine`; `TestAFolderRuleMatchesNoAction`, a folder rule of
either class and either effect against a class 1 cluster read with every cluster field unset.

**`securityconfig`**

- `TestCheckFolderRefusesEachRule`: one case per row of §3's table, over a fixture home with a
  never-readable path, a folder holding one (`~/Library` on macOS, `~/.config` on Linux) asked
  for read-write, a toolchain folder, a closed folder, a link into `~/.ssh`, and the home itself
  read and read-write.
- `TestAFolderRuleIsShapeChecked`: a relative folder, `/`, a `Deny` folder rule and a folder rule
  naming a context are refused on read-back, and a cluster rule naming a folder is too, none of
  them touching the disk; `TestHeldRulesGrantNoFolder`.

**`session`**: `TestNarrowKeepsTheParentsIdentity` grows its `Folders` case.

**`chatsvc`**

- `TestAChatsFoldersJoinTheAlwaysOnes`: the chat's rules and the file's, a chat's grant read
  live, and `folderRevoke` finding either by id.
- `TestAGrantIsRefusedTwiceInOnePlace`, and a chat grant beside an always one on the same folder
  is accepted.
- `TestAGrantGoesWithTheChat`: a `Chat` grant cascades on delete and reaches the chat's next
  command's session, not a running one's.
- `TestTheContextListsTheGrants`: the `Sandbox` section's `read`, `readWrite` and `more`, and a
  section replaced after the grants changed.

**`bash`**

- `TestAGrantIsAFilesRule`: over a fake sandbox, a read grant is a Read rule and a read-write
  grant a Write rule on the policy, resolved, and the Always part is unchanged.
- `TestNestedGrantsResolveToTheWider`: a folder granted read for the chat and read-write always
  is one Write rule; a folder under a read-write one is dropped; `Check` passes.
- `TestAGrantThatFailsTheCheckIsLeftOut`: a folder that became a link into `~/.ssh` since it was
  written adds no rule, is logged, and the run starts.
- `TestASessionWithNoFoldersGetsNone`: a nil `Folders` adds no rule.
- `TestADeniedAlwaysPathStaysHiddenUnderAGrantedHome`, in `bash_unix_test.go`, through the real
  sandbox on both platforms: with `~` granted read, `cat ~/.zshrc` reads and `cat
  ~/.ssh/id_ed25519`, `ls ~/.kube` and `ls ~/Documents` do not, and neither does Kstack's
  `app.db`. The note's first invariant, over a grant. A grant of `~/Documents/project` reads
  that folder and nothing else of `~/Documents` (`Closed`).
- `TestAReadWriteGrantIsWritten`, and a read grant is not, through the real sandbox.

**`read`, `write`, `edit`**

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

**`fileguard`**

- `TestGrantedAnswersTheFolderByName`, and false for a path `Hidden` hides by name, a `Shut`
  folder inside the grant included.
- `TestTheWalkRefusesALinkSwappedIn`: a link to `.ssh` swapped in, through the walk's test seam,
  after `Granted` answered is `ErrHidden`, and nothing under `.ssh` is read or changed.
- `TestTheWalkRenamesOnTheHandle`: a directory swapped for a link between the walk and the
  write changes nothing outside the folder.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-folders.test.tsx`, `sandbox-switch.test.tsx`):
the Folders rows with their tags, Remove, Add with the checkbox, a refusal's reason, a refused
entry's line, the home's warning line, the *Never readable* list; the composer's count and the
chat's list with Remove.

## Security

**Widened.** Before this step an unasked sandboxed command read nothing of the user's beyond the
toolchain, and wrote only its workspace, its `TMPDIR` and the tool cache. After it, it reads and
writes the folders the user granted, for the chat or always, and so do `Read`, `Write` and `Edit`
without a request. What holds it: the Always part is the floor — no grant opens a credential
path, a private folder or Kstack's directories, pinned over a real grant on both platforms; a
grant is the user's own act, in Settings or on a click step 5B offers, never the model's; it is
checked when written and at every run; a file tool walks a granted path by handle, so a link,
swapped or not, never leads it into a closed path; the home is read-only; a grant is listed in
Settings or under the composer, and in the question's context; an unreadable rules file grants
nothing; and an unasked write is drawn open in the transcript.

**Residuals.** A granted folder's contents leave the machine on the model's word, as cluster
reads do: a repository with a `.env` in it is readable once its folder is granted, and the
denied-always list names credential paths under the home, not inside a project. A read-write
grant lets a command plant a file a later command outside the sandbox (step 1B) or a `git` hook
runs, which is the bash tool record's residual over a wider folder. A folder granted always is
readable by every chat, and the user must remember it is. On Linux a denied-always path under a
granted folder that does not exist when a run starts is not mounted over, so a file something
outside the run makes there while it runs is readable to it (the residual on the denied-always
row of `security-model.md`); macOS holds the Deny whether or not the path exists.

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
  granted folder and the walk; a row that a session with no folders function reads none.
- **`sidecar/CLAUDE.md`**: `Rule.Folder`, the folder classes' check, `CheckFolder`,
  `Session.Folders`, the grants on the Workspace policy and how nesting resolves,
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
and `ls ~/Documents` and read both refused; ask for `cat ~/.zshrc` and read it. Read the chat's
context block list the grants. Grant a folder for the chat through the mutation, read it under
the composer's switch, and remove it there.
