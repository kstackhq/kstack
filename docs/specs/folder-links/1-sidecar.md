# Step 1: The sidecar

The sidecar resolves its folders once, refuses a link below them, checks the resolved layout, and
keeps a sandboxed command from repointing a link it followed. Part of
[the sequence](README.md), whose shared rules apply.

Paths are relative to `sidecar/internal/` unless rooted.

## The resolve

### What is resolved

- Every folder `layout.Of` names: `Data`, `Cache`, `Logs`, `Config`, `State`, `Runtime`, `App`
  and `AppOnConfigRoot`.
- `--runtime-dir`, by [its own rule](#the-unix-runtime-folder) on Unix.
- A debug build's `KSTACK_DATA_DIR`, `KSTACK_CACHE_DIR` and `KSTACK_RUNTIME_DIR`. A moved folder
  is followed as a layout folder is, and takes its kind's place in
  [the layout check](#the-layout-check). It is [made by its own rule](#what-is-made).

### How

`lib/realpath` gains the walk every resolve uses:

```go
// Link is a link Followed met.
type Link struct {
	Name   string // the link, as the path or a link's target named it
	Parent string // the folder that holds it, as the kernel names it
}

// Followed is p as the kernel names it, and each link met on the way.
func Followed(p string) (string, []Link, error)
```

`Followed` walks `p` from the root one name at a time. At a link (`IsLink`) it records the link
and the resolved folder that holds it, then continues from the link's target, so a link inside a
target is recorded too. The answer is named by `KernelPath`, so on macOS it carries the volume's
spelling. A name that is missing ends the walk: the rest is kept as given, as `canonical` does. A
link whose target is missing is an error that names both. A walk that follows more than 255
links is an error that names `p`. `Followed` writes nothing.

`lib/realpath` also gains the comparison the
[identity rule](README.md#one-folder-is-inside-another-by-identity) states:

```go
// Relation is how p sits to dir, as files: Same, Inside or Apart.
func Relation(p, dir string) Rel
```

`app/layout` gains `Resolve`, which is `Followed` for each field of a `Layout`, and
`ResolveRuntime` for the Unix runtime folder. Both stay apart from `Of`, which stays a pure
function that the fixture pins.

### Where

One function in `app` runs [follow, check, then make](README.md#follow-check-then-make) for
every folder. Its check is [the layout check](#the-layout-check):

```go
// Named is Kstack's folders as configFromArgs names them, unresolved.
type Named struct {
	Platform layout.Platform
	Layout   layout.Layout
	// Data, Cache and Runtime are the layout's, or a debug build's moved ones.
	Data, Cache, Runtime string
	// Profile is Windows' user profile.
	Profile string
}

// Folders is where Kstack keeps its files, every path resolved.
type Folders struct {
	Layout               layout.Layout
	Data, Cache, Runtime string
	// Links is each link followed to reach them.
	Links []realpath.Link
}

func ResolveFolders(n Named) (Folders, error)
```

- `Config` carries a `Folders` in place of `Layout`, `DataDir`, `CacheDir` and `RuntimeDir`.
  `build` and everything below it read paths from it alone, so no unresolved path reaches a
  service, the fence or a child process.
- `main` calls it straight after `configFromArgs` and before `openLog`: the log is the first file
  opened, and it opens in the resolved log folder. A failure is a `FATAL` line, as a bad root is.
- `configFromArgs` still reads no disk. It hands back a `Named`.
- `ResolveFolders` makes what `makeDirs` makes today, and `makeDirs` goes. The absolute-path
  check it holds moves too. `openLog` still makes the log folder.

### What is made

`ResolveFolders` keeps `makeDirs`' rule for each folder it makes:

| Folder | Made |
| --- | --- |
| The layout's data or cache folder | Owner-only, and the Kstack folder above it (`layout.MakeOwnerOnly`, on the resolved layout). |
| A debug build's moved data or cache folder | Only when it is missing. It may be a folder the developer shares, so its mode is left alone. |
| The runtime folder | Only when it is missing, as above. |

A moved folder is the layout's own when the two are one folder as files (`realpath.Same`), not
by their text: on a Linux folder that folds names, two spellings of one existing folder resolve to
different text, and a text comparison would skip the tightening. `sameDir` (`app/app.go`) goes
with `makeDirs`.

### The Unix runtime folder

`ResolveRuntime(dir)`:

- refuses a `dir` that exists and is a link, or anything else but a folder;
- answers `Followed(parent)` joined with `dir`'s last name, and the links met above it.

A runtime folder that is missing is no error. `ResolveFolders` then makes it under the resolved
parent, as `makeDirs` does today: a sidecar started with no host, on a `KSTACK_RUNTIME_DIR` that
does not exist yet, still starts.

On Windows the runtime folder is the layout's `Runtime`, and is followed as the others are.

### The socket's path grows on macOS

A run's socket is bound under `<runtime>/runs`. `checkSocketPath` (`tools/bash/rundir.go`) takes
that path as given, never resolved, because `/private/var` is eight bytes longer than `/var` and a
socket's path can be at most 103 bytes on macOS.

A run's longest socket path is the runtime folder plus 35 bytes (`/runs/`, an 18-byte name,
`/proxy.sock`). Computed for a 48-byte `$TMPDIR` and a three-digit uid:

| Runtime folder | Today | Resolved |
| --- | --- | --- |
| `kstack-501` | 94 | 102 |
| `kstack-dev-501` | 98 | 106 |
| `kstack-501-XXXXXX`, the fresh folder beside a refused one | 101 | 109 |
| `kstack-dev-501-XXXXXX` | 105 | 113 |

The last row does not fit today either. The others must keep fitting, so **this step takes at
least eight bytes out of the names below `runs`**. The socket's own name, ten bytes, is the place.
The path stays resolved.

Task 1 measures the four rows on a Mac before anything else, and sets the new name.
`checkSocketPath`'s comment then describes the resolved path.

### The IPC socket

`--socket` is the host's spelling, and the listener binds it as given: the host dials that
spelling, and on macOS it is the shorter one.

`ipc.SweepStale` compares the sockets it lists with the sidecar's own by text. `main` hands it
the resolved runtime folder, and its own socket as that folder joined with the socket's name when
the socket sits in the runtime folder. The host's `--socket` is under `/var` on macOS while the
folder swept is under `/private/var`, and the two must still compare equal.

## A link below the layout's folders

`pathsOf` loses `sub` and its "cannot be read" case. `build` makes each subfolder `pathsOf` names
before any service is built:

| Under | Subfolders |
| --- | --- |
| the data folder | `chats`, `monitor` |
| the cache folder | `kube`, `logs`, `kubectl`, `tmp` |
| the runtime folder | `shell`, `runs` |

- Each is made with `rootdir.Open(root, name, true)` under an `os.Root` on its resolved parent.
  The check and the creation are one step, and a link is `ErrNotADirectory`.
- A link fails the start with [the message](README.md#messages) that names the subfolder and its
  layout folder.
- It runs before `bash.New`, which empties `<runtime>/shell`: a linked `shell` is refused, not
  replaced.
- The services keep taking paths, and their own `MkdirAll` calls then find a real folder.
- A link deeper down, under a chat's or a monitor's folder, stays `rootdir`'s to refuse.

`rootdir.Open` has no Windows test. Add `rootdir_windows_test.go`: a junction is
`ErrNotADirectory`.

**Confirm while here:** list every path a sandboxed run can write (`Always.Write`, `Files.Write`)
and check that none is one of these eight folders' parents or a link parent. The security model states
the result.

## The layout check

`ResolveFolders` runs it on the resolved paths, before it makes a folder or changes a mode. It
covers every layout field and the runtime folder, with a debug build's moved folder in its kind's
place.

**No two folders may be one folder, and none may be inside another**, except the pairs this
table allows. The table is fixed per platform. It is not read off the unresolved paths: those
already hold whatever roots the environment named, so `XDG_CACHE_HOME` and `XDG_DATA_HOME` both
`/x` would pass as intended.

| Platform | Allowed |
| --- | --- |
| Linux | `Logs` inside `State`. |
| macOS | `Data`, `Config` and `State` inside `App`. |
| Windows | `Data`, `State`, `Cache` and `Runtime` inside `App`. `Logs` inside `State`, and so inside `App`. `Config` inside `AppOnConfigRoot`. `App` and `AppOnConfigRoot` one folder. |

Allowed is not required. Folders are compared
[by identity](README.md#one-folder-is-inside-another-by-identity), never by text.

When Windows' `App` and `AppOnConfigRoot` are one folder, they are one holder: the check joins
them before it looks inside, so what either may hold, the one folder may hold. `Config` is then
inside `App`, and `Data` inside `AppOnConfigRoot`, and both pass. The fixture's shared-roots
layout is this case.

On Windows the check also holds each layout folder
[strictly inside the profile](README.md#on-windows-a-layout-folder-stays-in-the-profile):
`Inside`, never `Same`.

A kind that was inside another and is linked out is fine: a `<state>/logs` linked to `/mnt/logs`
resolves outside the state.

```
the data directory and the cache directory are both /x/kstack; give each its own folder
the cache directory /x/kstack/cache is inside the data directory /x/kstack
```

## No write on or above a link parent

`Folders.Links` reaches the three places a write is decided:

- **`services/settings`**: `Zones` gains `Links []realpath.Link`. `checkFolder` refuses a
  read-write grant of a path that is a link parent or above one (`realpath.Under(parent, path)`), with
  rule `link-parent`:

  ```
  Kstack follows the link /home/u/.cache/kstack, and a command that can write /home/u/.cache
  could repoint it. Grant it read-only.
  ```

  A stored grant goes through the same function, so it is refused the same way. A read-only grant
  is unaffected.
- **`run/sandbox`**: `AlwaysPolicy` gains `LinkParents []string`, named with the policy's other
  paths when it compiles. `Policy.check` refuses a policy whose `Files.Write` or `Always.Write`
  holds a path on or above one.
- **`graph`**: the refusal rides the existing validation error, its code the rule. Check that the
  webview draws the reason of a rule it does not know.

Links above the runtime folder count. a system link's parent is `/`, which no grant reaches.

## One meaning of "link" on Windows

`realpath.IsLink` counts a symlink and every reparse point Go reports as irregular. Rust's
`is_symlink()` may count fewer.

The meaning to reach is a symlink or a junction, and nothing else: a OneDrive folder is a folder.

- **Tests, on CI's Windows runner.** A junction (`mklink /J`) and a directory symlink
  (`mklink /D`) are each a link to `IsLink`, to `rootdir.Open` and to Rust's `is_symlink()`. The
  Go tests sit beside `TestAJunctionIsALink`; the Rust test is in `src-tauri/src/paths.rs`. If
  one fails, the fix is a pull request of its own into `wip/folder-links`, before step 2.
- **By hand, once.** A folder OneDrive syncs, hydrated and not, against the same three. CI has no
  OneDrive. The answers go in the security record. This does not hold step 2; it is settled
  before the sequence lands, and a OneDrive folder that counts as a link is fixed the same way.

## Tests

Each link test has a Unix file and a Windows file.

A test that builds a `Config` gets its `Folders` from one helper, `testFolders(t)` in
`app/testutil_test.go`: `ResolveFolders` over folders under `t.TempDir()`, failing the test on an
error. `sidecar/`'s own tests reach `Folders` through `configFromArgs` and `ResolveFolders`, as
`main` does. No other package builds a `Config`.

- `Relation`: the same folder, a folder inside another, two apart, each through a link and with
  a missing tail. On Windows, two siblings that differ only in letter case are `Apart`, and a
  folder under one is not `Inside` the other.
- `Followed`: a link at the last name, one above it, one inside a link's target, a missing last
  name, and a link whose target is missing. On macOS, a path under `/var` answers `/private/var`
  with link parent `/`.
- A linked layout folder starts, and its fence entry is the resolved path.
- A root reached through a linked parent starts, and its fence entry is the resolved path.
- A linked runtime folder is refused; one under a linked parent starts; a missing one is made.
- A moved data folder that is missing is made, and one that exists keeps its mode. One that is
  the layout's own, reached through a link, is made owner-only.
- `SweepStale` leaves the sidecar's own socket when `--socket` reaches the runtime folder through
  a link.
- A link at each of the eight subfolders fails the start, and the message names it.
- Two kinds on one folder, by environment and by two links, fail the start. So does a kind linked
  inside another. A `logs` linked out of the state starts. Each platform's layout from the
  fixture passes, with its roots apart and, on Windows, with its two roots one.
- A layout `ResolveFolders` refuses is left as it was by the sidecar: it makes no folder and
  changes no mode.
- A cycle of links at a layout folder fails the start with an error that names the path.
- On Windows, a layout folder joined to a folder outside the profile fails the start; one joined
  inside it starts; a moved folder outside it starts. A layout folder that is the profile itself
  fails, and so does one under a sibling of the profile that differs only in letter case. If CI's
  Windows runner cannot make a case-sensitive directory, the comparison takes the file identity
  as a parameter and the test supplies it.
- A read-write grant of a link parent, and of a folder above one, is refused; the same grant read-only
  is accepted; a stored one is refused. A policy with a Write rule on a link parent does not compile.
- `TestALinkedSubfolderIsFenced`, `TestASubfolderThatCannotBeReadIsFenced` and
  `TestAJunctionedSubfolderIsFenced` go, with the code they cover.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | Measure a run's socket path under a resolved `$TMPDIR` on macOS, the four rows, and shorten the names below `runs`. | Built: the socket is `px`; the rows are pinned by `TestTheSocketPathFitsUnderAResolvedTMPDIR`, not yet measured on a Mac |
| 2 | `lib/realpath`: `Followed`, `Relation` and their tests. | Built |
| 3 | `app/layout`, `app`, `sidecar/main.go`: `Resolve`, `ResolveRuntime`, `ResolveFolders`, `Config.Folders`, `testFolders`, and the layout check. | Built |
| 4 | `app`, `lib/rootdir`: the subfolders made under a root at start; `sub` removed. | Built |
| 5 | `services/settings`, `run/sandbox`, `graph`: the link parents. | Built |
| 6 | The Windows tests of "link", in Go and Rust, and the OneDrive check, recorded. | In progress: the tests are built; the OneDrive check is open |
| 7 | The ADR, the security record and the docs below. | Built |

## When it lands

This step's pull request carries its own docs, since the old ADR stops being true with it.

- **A new ADR**, *Follow a link to a Kstack folder once, at start, and refuse one below it*. It
  supersedes
  [a linked subfolder of Kstack's directories is fenced](../../adr/2026-11-22-a-linked-subfolder-of-kstacks-directories-is-fenced.md),
  whose status flips. That ADR fenced instead of refusing because a released build's users might
  already have linked a subfolder.
  [Each folder kind has its own folder on every platform](../../adr/2026-11-22-each-folder-kind-has-its-own-folder-on-every-platform.md)
  records that `desktop/v0.0.1` reached no users: cite it as the reason refusing is safe now.
- **`sidecar/CLAUDE.md`**, *Directories*: `ResolveFolders` in place of `makeDirs`, the subfolders
  made at start, and the link to the new ADR.
- **[`security-model.md`](../../security-model.md)**:
  - the linked-subfolder row says a link is refused, and names the new tests;
  - the *Not built* row this spec added becomes **Enforced**, naming the grant and policy tests.
    It claims only that no sandboxed command can repoint a followed link, and states
    [the assumption](README.md#the-path-to-a-folder-is-the-users-alone) it rests on;
  - the row for Kstack's directories drops the gap for a link changed while the app runs, and
    states who can still plant one: a process of the user's outside the sandbox, and any
    account that can write a folder on the path;
  - the runtime-directory row names the sidecar's refusal of a linked runtime folder.
- The security record says which folder each runtime check reads and when
  ([the two checks](README.md#the-path-to-a-folder-is-the-users-alone)), and that neither reads a
  folder above it.
- **A security record** under [`docs/security/`](../../security/): the four gaps, the rule, the
  assumption about the path and who can still repoint a link, and the Windows answers from task
  6.
- **[`TODO.md`](../../TODO.md#security)**: this spec's line goes when step 3 lands. Two new
  lines: a Windows layout folder cannot leave the profile until Kstack sets an ACL itself, and
  Kstack does not check who can write the folders above its own.
- **[The profile-ACL ADR](../../adr/2026-09-02-windows-cache-files-rely-on-the-profile-acl.md)**
  stays true and is not amended. The new ADR cites it for the Windows rule.
