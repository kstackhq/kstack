---
title: Path grants
scope: sidecar, webview
status: Planned
---

# Path grants

**Needs:** step 2C, whose session carries the folders; step 3B, whose `permissions.Rule` a grant
is and whose `chat_grants` and `sandboxconfig.Settings` keep it; step 1A, whose `Always` part is
the floor a grant never lowers. **Unblocks:** steps 5B and 6A, which offer a grant from a
denial and from a probe.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command reads the system, the toolchain folders and its workspace, and nothing
else of the user's. A tool that keeps its data under the home, or a repository the user wants
the agent to read, is out of reach until the user switches the chat out of the sandbox (step 1B),
after which every command asks.

After this step, on macOS and Linux:

- **The user grants a folder**, read or read-write, **for a chat or always**
  ([the note](../../notes/sandbox-credentials-and-permissions.md)'s *User grants*: "let the agent
  see `~/code/my-service`"). A grant is a `permissions.Rule`: a chat's is a `chat_grants` row,
  an always one is in `sandboxconfig`.
- **A grant is a Files rule on the run's policy**, Read or Write, and **the Always part still
  wins**: a grant of `~` reads the home and not `~/.ssh`, `~/.kube`, `~/Documents` or Kstack's
  directories. This is the note's first invariant, "the single most important invariant in the
  sandbox", which step 1A pinned for the policy and this step pins for a grant.
- **A grant is checked when written and when read**: absolute, existing, not `/`, not inside an
  Always path or Kstack's directories, not inside or over a listed folder. The home itself may be
  granted, read-only, with a warning.
- **`Read`, `Write` and `Edit` in a granted folder run unasked**, through a root on the folder,
  as they do in the workspace (Decisions, 1). The Always part holds there too: a path is judged
  after its links are followed, so a link inside a granted `~` to `.ssh` reads nothing.
- **The monitor gets no folder from any chat.** The note's ninth invariant is pinned here.
- **Settings: Sandbox gains *Folders***, the always grants with Add and Remove, and the
  denied-always list read-only under *Never readable*; the chat's grants ride the question's
  context, so the model knows what it can read.

Nothing prompts by itself for a folder: a command that cannot read one fails, and step 5B turns
the failure into the offer.

## What is not in this step

- **No denial found or drawn.** Step 5B finds the path a run was refused and draws *Grant…*
  under the call; it calls this step's mutation.
- **No probe.** Step 6A runs the curated tools and offers a grant per denied path.
- **No grant to the monitor.** The note's "extending them to monitoring is a separate, explicit
  switch" is step 6D's to add if it wants it; this step gives the monitor none.
- **No grant of a file.** A grant is a folder.
- Nothing changes on Windows, which has no sandbox: `Read`, `Write` and `Edit` ask outside the
  workspace as today.

## Design

### 1. A grant is a rule

A folder grant is `permissions.Rule{Effect: Allow, Provider: Path, Class: WriteInside,
Scope{Folder: path}}` for read-write, and `Class: ReadInside` for read. The path is absolute and
resolved (`filepath.EvalSymlinks`) when written, so a grant names the folder the sandbox will
check. A chat's grant is a `chat_grants` row, read live by `grantsFor` (step 3B §3); the always
grants are:

```go
// In sandboxconfig.Settings.
Folders []Folder `json:"folders,omitempty"`

// Folder is one folder the user granted every chat.
type Folder struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Write bool   `json:"write"`
}
```

`session.Session` gains the field step 2C reserved:

```go
// Folder is one folder a session's commands may read, or read and write.
type Folder struct {
	Path  string
	Write bool
}

// Folders is the session's grants, the chat's joined with the always ones,
// read once at a run's start.
Folders func(context.Context) []Folder
```

`chatsvc` sets it to `foldersFor(chatID)`: the chat's class 1 and 2 `Allow` rules of provider
`Path` from `grantsFor`, then `sandboxconfig.Settings.Folders`, each as a `Folder`; a folder
listed twice keeps the wider grant. A session with no chat (the monitor) gets none (§6). `Narrow`
copies it. `sandboxedRunFor` reads it once when the run starts, so a grant written mid-run applies
to the next command, since a sandbox is per command.

### 2. The policy

`sandboxedRunFor` in `tools/bash/bash.go` adds each folder to the Workspace policy's Files
(step 1A §6): `Read` for a read grant, `Write` for a read-write one. The Always part is untouched,
so `Policy.Check` and both compilers give the note's answer: a Read rule over `~` opens the home,
and `~/.ssh`, `~/.kube` and the rest of `Never`, and Kstack's directories, stay closed inside it.
That is `TestTheDeniedAlwaysListWinsOverARead` (step 1A), and this step adds the test over a real
grant (§Tests).

A grant reaches the policy only through the check in §3, run again at the run's start: a folder
that fails it then — moved, deleted, turned into a link into an Always path since it was written
— is left out, logged with its reason, and shown in Settings with that reason (as step 3A shows a
`PATH` entry the file refused). `Check` is still behind it: a Files rule inside an Always path is
refused there too, and a run is never started with a policy other than the one checked.

### 3. The rules for a grant

`sandboxconfig.CheckFolder(path string, write bool, listed []Folder, closed []string, read
[]string) error`, where `closed` is `sandbox.Never(home)` plus Kstack's three directories and
`read` the Read paths of `Sandbox.System(home)` (the toolchain folders among them) and the
adopted `PATH` entries. Run on every write (§4) and on every read (§2), on the resolved path:

| Rule | Refused when | The reason shown |
| --- | --- | --- |
| absolute | the path is not absolute, or starts with `~` | *Name the folder by its full path.* |
| exists | it is not a directory after following links | *This folder does not exist.* |
| not the root | it is `/` | *The root cannot be granted.* |
| not closed | it is one of `closed`, or under one | *This folder is never readable in the sandbox.* (an Always path) or *Kstack's own folders cannot be granted.* |
| not nested | it is inside a listed folder, or a listed folder is inside it | *`<other>` is already granted; remove it first.* |
| not over a read | `write` is set and a path of `read` is under it | *Kstack reads tools from `<path>`; grant it read-only, or a folder beside it.* |
| not over a never path | `write` is set and a path of `closed` is under it | *`<path>` is never readable in the sandbox, so this folder can be granted read-only.* |
| the home | it is the home and `write` is set | *Your home can be granted read-only.* |

The home itself passes as a read grant, and the UI draws a warning line beside it: *This lets
commands read everything in your home folder except credentials and Kstack's own files.* The
nesting rule exists because a Files rule beneath a Write rule is refused by `Check` (nothing sits
beneath a Write rule) and two Read rules nested would say nothing the wider one does not; the
"not over a read" rule for the same reason, since a toolchain folder is a Read rule the user did
not write. The "not over a never path" rule is the same refusal for the Always part, and it is a
security rule: a command that can write a folder can rename what is under it, and on macOS a
Seatbelt rule for the old path then matches nothing, so a read-write grant of `~/Library` would
let a command move `Keychains` out from under the denied-always list. A read grant cannot
rename, which is why the home may be granted read-only. `listed` is the joined list for the
chat, so a chat grant and an always grant cannot nest either.

### 4. Where a grant comes from

Two places write a grant, both through `CheckFolder`:

- **Settings** (§7) writes an always grant.
- **A mutation** that step 5B's denial line and step 6A's probe call:

```graphql
enum GrantDuration { Chat Always }

type SandboxFolder {
  id: ID!
  path: String!
  write: Boolean!
  "Set on an entry the last run left out, with why."
  refused: String
}

type SandboxFolders {
  "The always grants."
  always: [SandboxFolder!]!
  "The denied-always list for this machine, with ~ expanded, read-only."
  never: [String!]!
}

extend type Query {
  sandboxFolders: SandboxFolders!
}

extend type Mutation {
  "Grant a folder. CHAT writes a chat_grants rule; ALWAYS a sandboxconfig entry. Refused KSTACK_VALIDATION_ERROR with §3's reason."
  folderGrant(chatID: ChatID!, path: String!, write: Boolean!, duration: GrantDuration!): SandboxFolders!
  "Revoke a grant by id, a chat's or an always one."
  folderRevoke(id: ID!): SandboxFolders!
}
```

`GrantDuration` is shared with step 4C's `networkHostGrant`: whichever of the two lands first
adds the enum, and the other uses it (a seam of this wave). A chat's grant carries
its rule's id, so `folderRevoke` finds it in `chat_grants` or in `Settings.Folders`. A `Chat`
grant on a chat that is gone is `KSTACK_RECORD_NOT_FOUND`. Every answer is the whole
`SandboxFolders`, so Settings redraws from one result; the chat's own grants are not in it, since
they belong to the chat (§7).

Nothing prompts for a folder by itself. A command that cannot read one fails with the tool's own
error, and until step 5B lands the model reads the sandbox's line and says so.

### 5. `Read`, `Write` and `Edit` in a granted folder

Today the three file tools skip the request for a path under the workspace by name
(`fileguard.Under(tools.WorkspacePath(rt.Dir), path)`) and reach it through the workspace's
root, so a link cannot carry a write out. This step treats a granted folder the same way
(Decisions, 1):

- `fileguard.Fence` gains the denied-always list beside Kstack's directories (`app` passes
  `sandbox.Never(home)` as it passes the data directories), and `Fence.Granted(folders
  []session.Folder, path string) (session.Folder, bool)` answers the folder a path is under by
  name, and false for a path under an Always path or Kstack's directories by name or on disk.
  On disk is `Holds`' check: the longest prefix that exists, every link on the way followed,
  and each of its ancestors compared with the closed paths by `os.SameFile`. So a granted `~`
  never skips `~/.ssh/config`, nor `~/alias/config` where `~/alias` links to `.ssh`: both ask as
  today. A path whose resolved form leaves the folder is false too.
- `Write.Approval` and `Edit.Approval` `Skip` for a path under a read-write folder;
  `Read.Approval` for a path under any folder. Each reads `rt.Session.Folders(ctx)`.
- A skipped call goes through an `os.Root` opened on the granted folder (`Fence.File` takes the
  root's path; `tools.OpenWorkspace`'s `openIn` becomes `openRoot(dir)` for both), so a link out
  of the folder is refused as one out of the workspace is. An `os.Root` follows a link that
  stays inside it, so in a granted folder the tool walks the path itself (`fileguard.Walk`): one
  component at a time from the root, each directory opened as a handle and compared with the
  closed paths by `os.SameFile` before the walk goes on, and the file opened with `noFollow`
  from the last directory's handle and compared the same way. A link met on the way is read, and
  its target walked the same way from the root. A closed handle is `ErrFenced`. Every check is on
  a handle already open, so a link a command swaps in after `Granted` answered leads nowhere
  closed (Decisions, 4). The stamp rule holds: `Write` replaces and `Edit` changes only a file
  the chat read and unchanged since.
- The transcript draws an unasked write in a granted folder open under its summary, as it draws
  one in the workspace: a `Write` or `Edit` with no `approval` that `Succeeded`, which is the
  same test `summaryOf` makes today, so nothing changes in `chat-transcript.tsx`.

A path under a read grant still asks `Write` and `Edit`, and a path under no grant asks every
tool, as today.

### 6. The monitor

`session.Narrow` copies a parent's `Folders`, and a `Monitor` session has none by construction:
`foldersFor` takes a chat id, and a session with no chat is built with `Folders` answering nil.
Step 6D keeps that when it builds the session. `TestAChatsFoldersNeverReachTheMonitor` pins the
note's ninth invariant, "path grants attached to a chat session do not appear in the monitoring
session's profile": with a chat's grants in `chat_grants` and always grants in the file, the
policy built for a `Monitor` session has no Read or Write rule from either.

### 7. Settings and the context

**Settings: Sandbox** (`sandbox-settings.tsx`, step 3A's section) gains, under the `PATH` list:

- **Folders**: the always grants, each its path in mono through `VisibleText`, *read* or
  *read and write* as a tag, Remove, and a refused entry's reason in a muted line. An Add row:
  a path field, a *read and write* checkbox, Add; a refusal draws §3's reason under the field,
  and the home draws the warning line above Add before it is pressed.
- **Never readable**: the denied-always list, read-only, under one line: *A sandboxed command
  never reads these, whatever you grant. Credentials, private files and Kstack's own data live
  here.*

`useSandboxFolders()` in `src/lib/sandbox-folders.tsx` is the one reader of `sandboxFolders`,
`folderGrant` and `folderRevoke`.

**The question's context**: the `Sandbox` section step 1B added gains the chat's grants, so the
model knows what it can read before it tries: `folders` with `read` and `write` lists of paths,
the chat's and the always ones together, in the JSON the section holds. `contextOf` in
`chats.tsx` draws it with the rest as text.

### 8. The prompt

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
   link into `~/.ssh` after the grant; refusing it then, with a reason on screen, costs one stat
   per grant per run. Recommended.
3. **The home is read-only.** A read-write home would put every toolchain Read rule beneath a
   Write rule, which `Check` refuses, and would let a command plant a `.zshrc`. Recommended.
4. **A file tool in a granted folder follows links, and judges where each one leads.** The
   sandbox checks a file at its real location, and the file tools must agree with it: a link
   inside a granted folder to an Always path is closed, and one to a file beside it is open.
   Walking by handle makes the answer hold against a link swapped in while the tool runs, which
   a check on the resolved path followed by an open cannot. Recommended. The alternative,
   refusing every link under a granted folder, is simpler and breaks ordinary repositories,
   whose `node_modules/.bin` and tool shims are links.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Settings.Folders`, `CheckFolder`, the read-back check | `sandboxconfig/folders.go`, its test | — | Planned |
| 2 | `Session.Folders`; `foldersFor`; the grant and revoke writes; the context section | `session/session.go`, `chatsvc/`, their tests | 1 | Planned |
| 3 | The policy: grants as Files rules, re-checked per run | `tools/bash/bash.go`, its tests | 2 | Planned |
| 4 | `Fence.Granted`; the file tools skip and go through the root | `tools/internal/fileguard/`, `tools/workspace.go`, `tools/read/`, `tools/write/`, `tools/edit/`, their tests | 2 | Planned |
| 5 | The wire, the resolvers, codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 2 | Planned |
| 6 | `useSandboxFolders`, the Settings rows, `contextOf` | `src/lib/sandbox-folders.tsx`, `src/components/widgets/sandbox-settings.tsx`, `src/lib/chats.tsx`, their tests | 5 | Planned |
| 7 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 3 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1, then 2, then 3, 4 and 5 at the same time, then 6 and 7 at the same time, then 8.

## Tests

**`sandboxconfig`**

- `TestCheckFolderRefusesEachRule`: one case per row of §3's table, over a fixture home with an
  Always path, a folder holding one (`~/Library` on macOS, `~/.config` on Linux) asked for
  read-write, a toolchain folder, a listed folder, a link into `~/.ssh`, and the home itself
  read and read-write.
- `TestFoldersPersist`, and `TestABadFolderIsLeftOutWithItsReason` on read-back.

**`session`**: `TestNarrowKeepsTheParentsIdentity` grows its `Folders` case.

**`chatsvc`**

- `TestAChatsFoldersJoinTheAlwaysOnes`: the chat's rules and the file's entries, the wider grant
  winning a folder listed twice, a chat's grant read live, and `folderRevoke` finding either.
- `TestAGrantGoesWithTheChat`: a `Chat` grant cascades on delete and reaches the chat's next
  command's session, not a running one's.
- `TestTheContextListsTheGrants`: the `Sandbox` section's `folders`.

**`bash`**

- `TestAGrantIsAFilesRule`: over a fake sandbox, a read grant is a Read rule and a read-write
  grant a Write rule on the policy, resolved, and the Always part is unchanged.
- `TestAGrantThatFailsTheCheckIsLeftOut`: a folder that became a link into `~/.ssh` since it was
  written adds no rule, is logged, and the run starts.
- `TestADeniedAlwaysPathStaysHiddenUnderAGrantedHome`, in `bash_unix_test.go`, through the real
  sandbox on both platforms: with `~` granted read, `cat ~/.zshrc` reads and `cat
  ~/.ssh/id_ed25519`, `ls ~/.kube` and `ls ~/Documents` do not, and neither does Kstack's
  `app.db`. The note's first invariant, over a grant.
- `TestAReadWriteGrantIsWritten`, and a read grant is not, through the real sandbox.
- `TestAChatsFoldersNeverReachTheMonitor`: the policy built for a `Monitor` session holds no
  rule from `chat_grants` or `Settings.Folders`. The note's ninth invariant.

**`read`, `write`, `edit`**

- `TestAWriteInAGrantedFolderAsksNoOne`, and `TestAWriteInAReadGrantStillAsks`, in `write` and
  `edit`; `TestAReadInAGrantedFolderAsksNoOne` in `read`.
- `TestAGrantedWriteGoesThroughTheRoot`: a link out of the folder is refused, nothing outside
  changes.
- `TestAnAlwaysPathUnderAGrantedHomeStillAsks`: `~/.ssh/config` under a granted `~` is not
  skipped.
- `TestALinkToAnAlwaysPathUnderAGrantedHomeStillAsks`: with `~/alias` a link to `.ssh`, and
  `~/deep` a link to `alias`, neither `~/alias/config` nor `~/deep/config` is skipped, and a
  link to a file beside it in the home is.

**`fileguard`**

- `TestGrantedAnswersTheFolderByName`, and false under an Always path, by name and through a
  link on the way.
- `TestTheWalkRefusesALinkSwappedIn`: a link to `.ssh` swapped in, through the walk's test seam,
  after `Granted` answered is `ErrFenced`, and nothing under `.ssh` is read or changed.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-folders.test.tsx`, `chats.test.tsx`): the
Folders rows with their tags, Remove, Add with the checkbox, a refusal's reason, the home's
warning line, the *Never readable* list, and `contextOf` drawing the grants.

## Security

**Widened.** Before this step an unasked sandboxed command read nothing of the user's beyond the
toolchain, and wrote only its workspace, its `TMPDIR` and the tool cache. After it, it reads and
writes the folders the user granted, for the chat or always, and so do `Read`, `Write` and `Edit`
without a request. What holds it: the Always part is the floor — no grant opens a credential
path, a private folder or Kstack's directories, pinned over a real grant on both platforms; a
grant is the user's own act, in Settings or on a click step 5B offers, never the model's; it is
checked when written and at every run; a file tool judges a path where its links lead, on
handles a swapped link cannot redirect; the home is read-only; a grant is listed in Settings and
in the question's context; and an unasked write is drawn open in the transcript.

**Residuals.** A granted folder's contents leave the machine on the model's word, as cluster
reads do: a repository with a `.env` in it is readable once its folder is granted, and the
denied-always list names credential paths under the home, not inside a project. A read-write
grant lets a command plant a file a later command outside the sandbox (step 1B) or a `git` hook
runs, which is the bash tool record's residual over a wider folder. A folder granted always is
readable by every chat, and the user must remember it is. On Linux a denied-always path under a
granted folder that does not exist when a run starts is not mounted over, so a file something
outside the run makes there while it runs is readable to it (step 1A's residual); macOS holds
the Deny whether or not the path exists.

The record, `docs/security/<date>-path-grants.md`, argues both. The TODO item *Revisit how
little of the home a sandboxed command reads* closes with it: this is the allow-list it asks
for, in `sandbox.json` rather than `host.json`, with the security record and the
`security-model.md` row it says the change needs.

## When it lands

- **The security record** above, and an ADR: a folder grant is a rule, read or read-write, for a
  chat or always; the file tools treat a granted folder as the workspace; the home is read-only.
- **`security-model.md`**: the sandbox's file row says the folders the user granted, with the
  tests; the denied-always row gains the grant test; the `Write`, `Edit` and `Read` rows say a
  granted folder; a row for the monitor holding no chat's folder.
- **`sidecar/CLAUDE.md`**: `Settings.Folders`, `CheckFolder`, `Session.Folders`, the grants on
  the Workspace policy, `Fence.Granted`, `fileguard.Walk`, the file tools' skip, the context
  section, the mutation.
- **Root `CLAUDE.md`**, the Settings dialog and *Chat*: the Folders rows, *Never readable*,
  `useSandboxFolders`, `contextOf`'s section.
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
context block list the grants.
