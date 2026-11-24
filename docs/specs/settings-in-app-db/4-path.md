# Step 4: PATH

Replaces the frozen `PATH` with one setting: a `PATH` string the user writes as in a `.zshrc`,
where `$PATH` stands for the login shell's. Part of [the sequence](README.md), whose shared rules
apply.

## What moves

| In `settings.json` today | Becomes |
| --- | --- |
| `sandbox.path` | `sandbox_settings.path`, a string |
| `sandbox.pathResolved` | nothing |
| `sandbox.pathStrict` | nothing |

Nothing is stored per folder. There is no sync, no adopted, pending or removed entry, and no
Include or Remove. Beside the setting, `app.db` keeps the login shell's last `PATH`, for a launch
whose read of the shell fails or times out. The setting describes one machine, so it has no
cluster scope ([Not in this sequence](README.md#not-in-this-sequence)).

## The tables

Migration `0009_path.sql`:

```sql
-- path: the PATH of every sandboxed command, written as in a shell's startup
-- file: folders joined by ':', where $PATH stands for the login shell's PATH.
ALTER TABLE sandbox_settings ADD COLUMN path TEXT NOT NULL DEFAULT '$PATH';

-- shell_path: the login shell's PATH as the last read that answered gave it,
-- for a launch whose own read fails. Written by Kstack alone. No row before a
-- first read answers.
CREATE TABLE shell_path (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  path TEXT    NOT NULL
) STRICT;
```

The default searches what the login shell's `PATH` lists, under the rules below.

**`shell_path` holds the shell's answer joined by `:`, and is split on read.** It is a copy, never
a decision, and no row of it is refused: each folder from it is checked again at each run, by the
rule for a shell folder. A folder whose name holds `:` splits differently on read, but
`sandbox.SearchFolder` refuses such a folder anyway.

## The setting

### Its grammar

The value is split on `:` first, and only then expanded. Each element is one of:

- **The shell's `PATH`**: exactly `$PATH` or `${PATH}`.
- **A typed folder**: an absolute path, or one starting with `~/` or `$HOME/`.

A value is refused, whole, when any element is:

- empty, which a shell would search as the working directory;
- relative;
- exactly `~` or `$HOME`, since the home is never searched;
- holding a `..` component, which a shell resolves through links and Kstack would resolve as
  text;
- `$PATH` mixed with other text (`$PATH/bin`, `x$PATH`);
- a `$` other than the forms above, since Kstack expands no other variable;
- holding a NUL.

The refusal names the element and why, in the user's words. These checks read no disk, so
`SetPath` runs them inside its transaction.

There is no escape for `$`. A folder whose path holds one cannot be typed.

### Its expansion

- **`$PATH`** becomes the folders of `shell_path`, already split. With no row, it is the
  platform's default folders, which `NewService` takes as a parameter. The shell's output never
  passes through the grammar, so no folder it lists is ever a typed one.
- **`~` and `$HOME`** become the home the zones use (`Zones.Home`), canonical. Never a value from
  the shell's output.
- **A typed folder is cleaned** (`filepath.Clean`), so `~/bin/` and `//opt/bin` are the folder
  a shell would search. With `..` refused, cleaning only drops slashes and `.` components, which
  never changes the folder.

Each folder carries its **origin**: `shell` or `typed`.

## The shell's `PATH`

The launch already runs the login shell once, for the environment, the keys and `PATH`
(`launchShell`). Nothing runs it a second time at launch.

- **`SetShellPath(ctx, path)`** takes a `PATH` already read and writes it to `shell_path`. It
  answers whether the row changed. The launch calls it with `login.path`, before `Serve`, in
  place of `syncPath`.
- **`RefreshPath(ctx)`** runs the login shell, then calls `SetShellPath`. A read that fails or
  times out writes nothing and sets the fault, as today.
- **An empty `PATH` is a failed read.** Both shell readers already answer one as a failure
  (`readPosix`, `readNu`), at launch and on refresh alike, and that stays: no shell that
  answers can run without a `PATH`, so an empty one is a broken startup file, not a choice. It
  keeps the last row and sets the fault. Keeping it widens nothing, since a shell folder is
  searched only when every run reads it already. `SetShellPath` refuses an empty list, so no
  `shell_path` row is ever empty. A user who wants no shell folders writes a value without
  `$PATH`.
- **They run one at a time**, as syncs do today, so an older read never lands after a newer one.
- **A write that changes the row** logs each tracked tool whose first open shell folder moved
  (`logToolMoves`, on the old and new rows, after the write and on a goroutine), and starts a
  probe.
- **Every call retakes the zones' snapshot**, changed row or not, bounded by `SyncTimeout`, as a
  sync does today. So a launch and each Refresh PATH pick up a toolchain folder installed since.
  The snapshot holds the zones alone: its `loginPath` goes, since the folder checks read
  `shell_path`.
- **Every call that retakes the snapshot notifies `KeySandboxPath`**, changed row or not. New
  zones can change whether a folder grant passes, so an open Settings window reads the grants
  again.

A launch whose read fails keeps the last row, so its runs search what the last good read listed.

## What a run searches

Each run expands the setting once, at its start, and checks each folder in order:

- **Every folder** passes `filterDir`: `sandbox.SearchFolder` (missing, world-writable, broad,
  denied-always, a fixed mount, a name holding the separator) and the `node_modules` rule.
- **A shell folder** is searched only when it is open to every run and not shared, as the sync
  adopts one unasked today. A startup file can change which open folder's program runs first.
  It cannot make a folder readable.
- **A typed folder** is searched when it passes `checkFolder` for a read, unstored, as a folder
  grant does when it is granted. So its cleaned, expanded path must be the folder's canonical
  path:
  - **A link is refused** with its target, since the target is what a run would read and the
    user did not type it.
  - **Another spelling is refused** with the disk's own spelling. On a case-insensitive volume
    `/Users/ME/bin` opens, but the sandbox's profile matches the spelling the volume stores.

  A typed folder is the user's read grant. One the run's open folders do not read gets a Read
  rule. A shared typed folder is searched, and Settings marks it shared.

A folder searched earlier in the list wins over a later one that is the same folder, compared as
files. A folder that fails is left out of the run and logged. Leaving one out only narrows what a
run can read.

**A folder listed twice is judged at each place it is listed.** With `$PATH:~/tools` and
`~/tools` closed, the shell's occurrence is left out and the typed one is searched. Settings
gives the shell's row the reason "searched where you typed it". A later occurrence of a folder
already searched reads "searched earlier in the list".

`RunCheck` keeps these rules, so Settings' list and a run answer alike.

**A folder in `PATH` is never granted read-write.** To the folder grant check, the `PATH`
folders (`NoWrite`) are every folder of `shell_path`, or the default folders with no row, plus
the typed folders of the value. The shell's folders count whatever the value says: a value without `$PATH`
stops a run from searching them, but the user's terminal still runs what they hold. Both
`pathFolders`, for Settings' checks, and the run's own `grantRules` read this list, in place of
`PathEntryFolders` and the snapshot's `loginPath`.

**A run reads its `PATH` once.** One call answers both what the run searches and its `NoWrite`
folders, from one read transaction. So a setting changed after the run's start cannot let the
run search a typed folder while its grant check lets a write grant on that folder through. Each folder counts as expanded, unfiltered, as
the login shell's answer does today.

## A refused value

The read-back runs the grammar. A refused value stays in the row, and Settings shows it with its
refusal. While it is refused:

- **A run does not start.** Its error names the refusal and points to Settings.
- The probe runs nothing.
- The folder checks count the shell's folders alone, since no typed folder can be read.

The user fixes the refused value by saving a new one, or by Reset, which saves `$PATH`.

## A read that fails

A failed read of the setting or of `shell_path` is an error. It is never `$PATH` and never the
platform's default folders.

| Reader | A failed read |
| --- | --- |
| A run (`pathList`) | does not start |
| The probe | probes nothing, and each report says why |
| Settings' folder checks (`checkWithin`) | an error, never a refusal on the grant: `FolderGrants` answers it |
| A run's grant check (`grantRules`) | cannot fail alone: it reads the run's one `RunPath` |
| `sandboxPath` | answers the error |
| `sandboxPathSet`, after its write committed | `KSTACK_SAVED_UNREAD` (`savedUnread`) |
| `sandboxPathRefresh`, after its write committed | `KSTACK_SAVED_UNREAD` |
| `sandboxFoldersWatch` | ends the watch with its reason, as it does for the grants |
| The probe after the launch's `SetShellPath` (`app`) | starts no probe, and logs a warning |

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`appdb`**: migration `0009` and `KeySandboxPath`, notified after a set commits and after
  each `SetShellPath`.
- **`services/settings`**:
  - `Path(ctx) (PathSetting, error)`. A `PathSetting` is the `Value` and its `Refusal`, empty
    for a value Kstack uses. The error is a failed read alone.
  - `SetPath(ctx, value) error` checks the grammar and updates the one row. It never inserts the
    row, as `SetCloudPlatformSwitch` does.
  - `CheckPath(value) error` is the grammar, shared by `SetPath` and the read-back.
  - `ExpandPath(value, shell, home) []PathFolder` is the expansion. A `PathFolder` is a `Path`
    and its `Origin` (`OriginShell`, `OriginTyped`).
  - `NewService` takes the platform's default folders. `app` passes the list `tools/bash` uses
    today (`defaultPath`).
  - `RunPath(ctx) (RunPath, error)`, from one read transaction over both rows. A `RunPath` is
    `Search`, the value expanded over `shell_path` as `[]PathFolder`, and `NoWrite`, the `PATH`
    folders of the read-write refusal. A refused value is an error.
  - `PathNoWrite(ctx) ([]string, error)` is `NoWrite` alone, for Settings' checks.
  - `CheckFolder` and `CheckStoredFolder` answer a `FolderRefusal` for a folder that fails its
    check, and any other error for a read that failed.
  - `SetShellPath` and `RefreshPath`, above.
  - `PathFolders(ctx)` is each folder as Settings shows it: path, origin, target, searched, the
    reason one is left out, whether Add applies, shared. Its disk reads are bounded by
    `SyncTimeout` (`readWithin`).
  - `RunCheck.Folder` takes a `PathFolder`.
  - Deleted, whole: `PathEntry`, `PathState`, `Source`, `checkPath`, the `FieldPath*` keys and
    their `strictest` lines, `SandboxSettings`, `SyncPath`, `diffPath`, `AdoptPath`, `DropPath`,
    `setState`, `RunPath`'s `Resolved`, `PathEntryFolders`, the snapshot's `loginPath`, and every
    `PathRefusal` but `ErrNoSandbox`. `checks` and `strictest` are left empty for step 5.
- **`tools/bash`**: `pathList` is `func(context.Context) (settings.RunPath, error)`, called once
  at a run's start. A run that gets an error does not start. `runPath` checks each folder of
  `Search` by its origin, and `grantRules` takes `NoWrite` from the same `RunPath`. The default
  folders arrive through the expansion of `$PATH`, so `fallbackPath` goes.
- **`agent/chat`**: `checkedFolderGrants` keeps a `FolderRefusal` on its grant, and answers any
  other error from the check as an error. `FolderGrants` and `GrantedFolders` pass it on, so a
  failed read ends `sandboxFoldersWatch` and fails the run, never a frame of refused grants.
- **`app`**: the launch calls `SetShellPath` with the `PATH` it read, in place of `syncPath`, and
  starts a probe when the row changed. A set that changes the value starts one too.
- **`graph`**: `sandboxFoldersWatch` subscribes to `KeySandboxPath` in place of
  `Settings.Subscribe`.

## The wire

- `sandboxPath: SandboxPath!`, where `SandboxPath` is:
  - `value: String!`
  - `refusal: String`
  - `folders: [SandboxPathFolder!]!`
- `SandboxPathFolder` is:
  - `path: String!`: the folder as expanded.
  - `origin: SandboxPathOrigin!`: `Shell` or `Typed`.
  - `target: String`: the folder `path` resolves to, canonical, null when it does not resolve.
  - `searched: Boolean!`
  - `reason: String`: why it is left out, in the user's words. A typed link names its target.
  - `addable: Boolean!`: true for a shell folder left out only because it is not open or is
    shared, that the value does not type, and whose `target` passes the typed-folder rules and
    holds no `$` and no `:`. Typing its `target` would include it.
  - `shared: Boolean!`
- `sandboxPathSet(value: String!): SandboxPath!`. It is refused `KSTACK_VALIDATION_ERROR` with
  the grammar's refusal, or on a machine with no sandbox.
- `sandboxPathRefresh` answers `SandboxPath!`.
- `sandboxPathFault` stays.
- On a machine with no sandbox, `sandboxPath` answers the stored value and its refusal, with no
  folders, and `sandboxPathRefresh` is refused as `sandboxPathSet` is.
- Deleted: `SandboxPathEntry`, `SandboxPathState`, `SandboxPathSource`, `sandboxPathInclude`,
  `sandboxPathRemove`, `sandboxPathResolved`.

Every path and reason is user or shell text: draw it through `VisibleText`.

The webview follows in `src/lib/sandbox-path.tsx` and the Settings dialog's `sandbox-path.tsx`.
The card is a text field with Save, Reset and Refresh PATH, over the list of folders. A folder
with `addable` has an Add action: it appends `:` and the folder's `target` to the field, unsaved,
so the user sees the text before Save. A text input draws no hidden character, so when the draft
holds one (`visibleSegments` spells it), the card draws the exact draft through `VisibleText`
under the field. The user sees every character of the read grant before Save. It is done when
these hold, each with a test:

- Each folder shows whether it is searched, and if not, why. A shell folder and a typed one are
  told apart, and a shared one is marked.
- Add appears exactly when `addable` is true, appends the target to the field and saves nothing.
- A draft holding an invisible, bidirectional or control character is drawn through
  `VisibleText` under the field, with each such character spelled. A draft with none draws
  nothing extra.
- A refused value shows its refusal and stays editable. Reset saves `$PATH`.
- A save refused by the grammar keeps the typed text and shows the refusal.
- A save answered `KSTACK_SAVED_UNREAD` was taken, as `permission-settings.tsx` treats one.

## Tests

- **The grammar.** `TestCheckPathRefusesEachBadElement` covers each refusal above.
- **Origin by construction.** A shell `PATH` holding `/x`, `~/y` or `$HOME` never yields a typed
  folder, and never expands again (`TestTheShellsPathIsNeverTyped`).
- **A shell folder** that is not open, or is shared, is not searched. An open one is
  (`TestAShellFolderIsSearchedOnlyWhenOpen`).
- **A typed folder** that is closed is searched with a Read rule. A trailing slash or `..` is
  cleaned, and `..` is refused by the grammar. A link is left out naming its target, and so is
  another spelling naming the disk's.
  One that became a link after the save is left out at the run (`TestATypedFolderIsARead`,
  `TestATypedFolderIsCleaned`, `TestATypedLinkIsLeftOut`, and on macOS
  `TestATypedFolderInAnotherCaseIsLeftOut`).
- **A value without `$PATH`** searches the typed folders alone
  (`TestAValueWithoutPathDropsTheShells`).
- **An empty shell `PATH` is a failed read.** Through the shell reader, with an older row
  stored: a posix shell and nushell that each export an empty `PATH` leave the row as it was
  and set the fault, at launch and on refresh (`TestAnEmptyShellPathKeepsTheLastRow`).
  `SetShellPath` refuses an empty list.
- **Order and duplicates.** The first searched occurrence of a folder wins. A closed folder both
  in the shell's `PATH` and typed is searched at its typed place, and the shell's row says so
  and is not addable (`TestAFolderListedTwiceIsJudgedAtEachPlace`).
- **Addable.** A closed or shared shell folder is addable. A world-writable, broad,
  denied-always or duplicate one is not, nor one whose target holds `$` (`TestAddableFolders`).
- **The last shell `PATH` survives a restart.** A store opened again expands `$PATH` from
  `shell_path`. A read that fails or times out leaves the row as it was. With no row, `$PATH` is
  the platform's default folders (`TestTheShellsPathOutlivesARestart`,
  `TestAFailedShellReadKeepsTheLastPath`).
- **Reads of the shell run one at a time.** An older read never lands after a newer one
  (`TestShellPathWritesRunOneAtATime`).
- **The launch runs the shell once.** It hands its `PATH` to `SetShellPath`
  (`TestTheLaunchRunsTheShellOnce`).
- **The read-write refusal** covers a typed folder, and every shell folder even when the value
  has no `$PATH`, in Settings' checks and in a run's `grantRules`
  (`TestAWriteGrantOnATypedPathFolderIsRefused`,
  `TestAWriteGrantOnAShellFolderIsRefusedWithoutPath`).
- **The snapshot is retaken** at each `SetShellPath`: a toolchain folder added between two calls
  reaches the folder checks after the second (`TestARefreshRetakesTheZones`).
- **One read per run.** `RunPath` reads the value and `shell_path` in one read transaction
  (`TestARunReadsBothRowsAtOnce`). A run whose typed folder the user removes after its start
  still refuses a write grant on that folder (`TestARunKeepsItsNoWriteAfterTheSettingChanges`).
- **A failed check read is an error.** A database error in a folder check ends
  `sandboxFoldersWatch` with its reason and fails the run. A policy refusal stays on its grant
  (`TestAFailedFolderCheckReadEndsTheWatch`).
- **Every refresh notifies.** A Refresh PATH whose shell answer is unchanged, after a toolchain
  folder was installed, pings an existing `sandboxFoldersWatch` subscriber, and its next frame
  carries the new answer for a write grant (`TestAnUnchangedRefreshStillNotifies`).
- **A refused value** starts no run, probes nothing, and is fixed by a set
  (`TestARefusedPathStartsNoRun`).
- **Each reader** in the failed-read table, with a failed query.
- **The migration test** builds the schema before `0009`, seeds a row in each earlier table,
  migrates, and checks that every seed survives, that the row's `path` is `$PATH`, and that
  `shell_path` is empty and holds one row at most.

Today's sync tests go with the sync: `TestSyncPathDiffsFourWays`, the removal and strict tests,
`TestEveryStateCombinationKeepsTheInvariants`, and the exact-spelling tests. The filter tests
(`TestFilterPathDropsEachRule`, `TestSearchFolder…`, the broad-entry tests), the run's
link-following tests and the `logToolMoves` tests stay, moved to the new entry points.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0009` and its test. | Planned |
| 2 | `services/settings`: the grammar, the expansion, `SetPath`, `SetShellPath`, `RefreshPath`, `RunPath`, `PathFolders`, and the sync's deletion, with the tests above. | Planned |
| 3 | `tools/bash`, `agent/chat`, `app`, `graph`: the run's one read, its grant check, the folder checks' errors, the launch, the probe and the watch on the new service, with each reader's failed read. | Planned |
| 4 | `graph`, `src/`: the wire and the webview. | Planned |

## When it lands

- **ADR**: a sandboxed command's `PATH` is one setting the user writes. It supersedes
  [`PATH` is the login shell's, filtered and frozen](../../adr/2026-10-02-path-is-the-login-shells-filtered-and-frozen.md)
  and [a stored `PATH` decision holds for its exact path](../../adr/2026-11-24-a-stored-path-decision-holds-for-its-exact-path.md).
  It records the two origins, the split-then-expand rule, the typed-folder rule, `shell_path` as
  a copy, and the loss of Remove for one shell folder.
  [Two mounts of one folder are one `PATH` folder](../../adr/2026-11-23-two-mounts-of-one-folder-are-one-path-folder.md)
  stands. Its `amended_by` repoints to the new ADR.
- **Security record** under `docs/security/`:
  - **`sandboxPathSet` is a grant path.** It gives every run read access to a folder tree, from
    text the user types rather than a click on a drawn target. It holds the folder grants'
    limits: a link or another spelling is refused, and the denied-always list, Kstack's
    directories and the closed folders stay shut.
  - Add copies a folder from the login shell's `PATH`, never from command output, and saves
    nothing. A draft holding a hidden character is drawn through `VisibleText` before Save.
  - A startup file still chooses among open folders, as before, and opens none. `logToolMoves`
    still logs when it changes which program a name finds.
  - `shell_path` is a copy that every run checks again.
- [`security-model.md`](../../security-model.md): rewrite the frozen `PATH` row (274) with the
  tests above. Row 263's "a `PATH` entry stored or answered by the login shell" becomes "a folder
  of the `PATH` setting, typed or from the login shell".
- [`persisted-data.md`](../../persisted-data.md): the `sandbox_settings` row gains `path`; add a
  row for `shell_path`, durable, riding `app.db`'s version stamp, pinned by the migration test. Take the `PATH` fields out of the *Sidecar settings* row.
- `sidecar/internal/services/settings/CLAUDE.md`: the `PATH` paragraphs, and the `Service`
  paragraph's sync.
- `src/components/settings/CLAUDE.md`: the `PATH` bullet.

## Not in this step

- **Removing one folder of the shell's `PATH`.** The only way is a value without `$PATH` that
  lists the folders it keeps, which stops following the shell. An open folder stays readable
  either way, so Remove only chose which program a name finds; a typed folder placed before
  `$PATH` does that. The ADR names this loss.
- **An escape for `$`.** A folder whose path holds one cannot be typed, and is never addable.
- A preview of a value before it is saved. Settings shows the saved value's folders, and Reset
  undoes a bad save.
- A `PATH` per cluster or per chat.
