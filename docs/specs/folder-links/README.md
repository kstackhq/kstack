# Resolve Kstack's folders once at start

The build order for [#1145](https://github.com/kstackhq/kstack/issues/1145). Each spec here is one
pull request into `wip/folder-links`, built in order. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply.

## Why

Kstack keeps its files in folders it makes itself, such as `~/.local/share/kstack` and
`~/.cache/kstack`. A sandboxed command must never read them. The list of those folders is the
fence.

A symlink or a Windows junction gives a folder a second name. Today Kstack follows links in
several places and at several times, and that leaves gaps:

- **A linked subfolder is checked only at start.** `pathsOf`'s `sub` helper
  (`sidecar/internal/app/paths.go`) looks at `chats`, `monitor`, `kube`, `logs`, `kubectl`, `tmp`,
  `shell` and `runs` once. A link put at `~/.cache/kstack/kube` while Kstack runs is not fenced
  until the next start.
- **A folder inside another is fenced twice**, only because a link could lead the inner one
  elsewhere (`Layout.Dirs`, `pathsOf`).
- **A linked config folder keeps its mode** (`ensure_config_dir`, `src-tauri/src/paths.rs`), and
  nothing says what such a link means.
- **A linked folder's own name is not fenced.** With `~/.cache/kstack` linked to
  `/mnt/big/kstack`, `CheckFolder` accepts a read-write grant of `~/.cache`. A sandboxed command
  can then repoint the link, and the next start follows it into a folder the command wrote.

## The rule

A link is followed in one place, at one time, and refused everywhere else.

| Folder | Example | Rule |
| --- | --- | --- |
| A **layout folder**: one `layout.Of` names | `~/.cache/kstack`, `Kstack/data`, `<state>/logs`, `~/.config/kstack` | Its links are followed once, at start. The resolved path is used from then on. |
| The Unix runtime folder | `/tmp/kstack-501` | A link at the folder itself is refused. The path above it is resolved. |
| A **subfolder**: anything made later, below those | `chats`, `kube`, `tmp`, `runs`, `chats/<id>` | It must be a real folder, checked where it is made. |

A layout folder may be a link because it exists at start and no sandboxed command can put a link
there: step 1 keeps every one from writing the folder that holds one. That
[assumes the path is the user's alone](#the-path-to-a-folder-is-the-users-alone). The runtime
folder's parent can be shared between users, so another user could plant a link at it. A subfolder is made
while the app runs, some of it near where sandboxed commands write.

So these all start:

- macOS's `$TMPDIR` is under `/var`, a link to `/private/var`.
- On ostree Linux `/home` is a link to `/var/home`.
- A dotfiles manager links `~/.config/kstack` into `~/dotfiles/kstack`.
- A user moves `%LOCALAPPDATA%\Kstack` elsewhere in their profile with a junction.

And these stop the start:

- `~/.cache/kstack/kube` is a link.
- `%LOCALAPPDATA%\Kstack` is a junction to another drive.

## Words

- **Link**: a symlink or a Windows junction. Step 1 pins what else counts on Windows.
- **Resolved**: a path with every link followed. The sidecar's is also named as the kernel names
  it (`realpath.Canonical`), since it builds the fence.
- **Link parent**: the folder that holds a link Kstack followed, by its resolved path. `~/.cache` for a
  linked `~/.cache/kstack`; `~` for a linked `~/.cache`; `/` for macOS's `/var`.

## The steps

| Step | Spec | Builds | After it |
| --- | --- | --- | --- |
| 1 | [The sidecar](1-sidecar.md) | The resolve, the subfolder refusal, the layout check and the link parents, in Go. The ADR and the security docs. | The fence is built from resolved paths. The host still follows links each time it names a folder. |
| 2 | [The host](2-host.md) | The resolve in `paths.rs`, and the linked config folder's mode. | Neither program follows a link for a Kstack folder after start. |
| 3 | [Cleanup](3-cleanup.md) | Removes the handling for a folder nested in another by name. | The work is whole. |

The sidecar goes first: it owns the fence, and it also runs without a host. Every step leaves a
working app.

## Where the sequence ends

- A layout folder that is a link starts, and its fence entry is the resolved path.
- A root reached through a linked parent starts, and its fence entry is the resolved path.
- A link at any subfolder `pathsOf` names, or at `<state>/webview`, fails the start.
- A read-write grant on or above a link parent is refused, at the grant and at a run's start.
- The start fails when two kinds resolve to one folder, or when one sits inside another where
  the layout does not put it.
- A linked config folder keeps its mode, and `host.json` is read and written where the link leads.
- No code follows a link for a Kstack folder after start. A run's policy still resolves every
  rule path when it compiles (`Policy.compile`), Kstack's with the user's:
  [#1150](https://github.com/kstackhq/kstack/issues/1150) measures that.

## Rules every step follows

### Follow, check, then make

Each program resolves its folders next to where it makes them, in this order:

1. **Follow.** Walk the path from the root. At each link, record its link parent and continue from its
   target. A link met inside another link's target is recorded too. A name that is missing ends
   the walk, and the rest is kept as given. This step writes nothing.
2. **Check.** Refuse the start on the resolved paths: the sidecar's layout check, and on Windows
   [the profile](#on-windows-a-layout-folder-stays-in-the-profile). This program has made no
   folder and changed no mode yet.
3. **Make.** Make each folder this program owns where its path leads. Nothing after this step
   reads an unresolved path.

A walk that follows more than 255 links fails with an error that names the path, so a cycle of
links stops the start and does not hang it.

Both programs already make each folder at start, so the resolve adds no new place where a mount
that does not answer can hang the launch.

**Each program's check covers its own writes, not the other's.** The host checks the Windows
profile and nothing else; the layout check is the sidecar's. On a desktop launch the host has made
its folders and opened its log before the sidecar starts, so a layout the sidecar refuses can
already hold them, and a config folder the host tightened.

### The path to a folder is the user's alone

The sequence stops Kstack's sandboxed commands from repointing a link Kstack followed. It does
not check who else can.

**Kstack assumes that every folder on the path to a layout folder, and to a link's target, can be
written only by the user or by root.** Making a target owner-only does not change its parent. A
root the user points at a folder another account can write, such as an `XDG_CACHE_HOME` under a
shared directory, is outside what Kstack protects. So is a process of the user's own that runs
outside the sandbox.

Kstack checks one folder itself, the Unix runtime folder, because its default parent is shared.
It checks that folder and none above it:

- At launch the host adopts the runtime folder only when it is a folder, not a link, that this
  user owns (`ensure_private_dir`).
- Before each sandboxed run the sidecar refuses a runtime folder that is a link, that another
  user owns, or that group or others can write (`checkPrivate`).

### A link whose target is missing fails the start

A layout folder that is a link must lead to a folder that exists. The start does not make the
target. Today `os.MkdirAll` and `create_dir_all` both fail on such a link with an error that names
neither end. The new message names the link and its target.

A layout folder that is missing and is no link is made, as today.

### On Windows a layout folder stays in the profile

Windows has no mode bits. Kstack's files there are private because they inherit the user
profile's ACL
([ADR](../../adr/2026-09-02-windows-cache-files-rely-on-the-profile-acl.md)). A junction can lead
a folder out of the profile, to a drive whose ACL lets other accounts in, and telling that a
folder is a junction says nothing about who can read its target.

So on Windows a layout folder that a link leads elsewhere must resolve strictly inside the
resolved user profile (`USERPROFILE`), or the start fails:

```
C:\Users\u\AppData\Local\Kstack leads to D:\big\Kstack, outside your profile; keep it inside C:\Users\u
```

A folder reached through no link is where Windows names it. An administrator may redirect a
known folder off the profile, as Folder Redirection does, onto a mapped drive among others, and
the check leaves it alone.

The profile is the reason the ACL can be relied on, so the check is strict:

- The folder is a proper descendant. The profile itself is refused.
- It is inside the profile [by identity](#one-folder-is-inside-another-by-identity).

Both programs check it. A debug build's moved folder is exempt: it is the developer's own. A
test passes the profile as a parameter, so "outside" is another scratch folder.

### One folder is inside another by identity

The sequence asks in three places whether one of Kstack's folders is another, or is inside it:
the layout check, the Windows profile check, and step 3's drop of a fence entry. All three use
one rule, and text does not decide it.

- Walk up from the inner folder. It is inside the outer one when an ancestor that exists is the
  same file as the outer folder (`os.SameFile`), and is the outer one when it is that file itself.
- Names past the deepest folder that exists are compared exactly as given, with no folding.

`realpath.Under` and `realpath.Within` are not this rule: both accept matching text, and on
Windows `filepath.Rel` folds case. Windows has case-sensitive directories, where `State` and
`state` can be two folders side by side. Logs under the second are not inside the first.

### A folder the other program owns

The sidecar resolves and fences every layout folder, the host's included, and makes only its own.
A host folder that is missing when the sidecar starts is named through its deepest folder that
exists, as `realpath.Canonical` does. The host makes its folders before it spawns the sidecar, so
this happens only for a sidecar started with no host.

### The two programs reach the same folders

Each resolves the layout on its own, from the same roots.
`sidecar/internal/app/layout/testdata/layouts.json` pins the unresolved layout. A test with a
linked layout folder on each side pins the rest: both name the link's target.

They need not spell a folder the same. The only path one hands the other is `--runtime-dir`,
which the host passes as it names it and the sidecar resolves.

### Messages

A refusal names the folder and says what to do. A linked subfolder's says to link the layout
folder above it, as `webview_dir`'s does today:

```
/home/u/.cache/kstack/kube is a link; link /home/u/.cache/kstack instead
```

### Tests

A link test is a symlink on Unix and a junction on Windows, each in a platform-named file. A
junction is made with `mklink /J`, as `realpath`'s `TestAJunctionIsALink` makes one.

## Not in this sequence

- **Paths the user names**: a folder grant, a `PATH` entry, and the never-readable list
  (`~/.ssh`, `~/.kube`). A link there is legitimate. `realpath.Canonical`, `sandbox.Under` and
  `sandbox.Same` stay for them.
- **A link planted or changed while Kstack runs.** A sandboxed run writes only below the
  subfolders and in the folders it was granted, which step 1 keeps clear of every link parent, so
  no sandboxed command can do it. Step 1 confirms this and states it in the security model.
- **Who else can write the path to a folder.** Kstack
  [assumes](#the-path-to-a-folder-is-the-users-alone) it is the user alone and does not check
  the owner or mode of a folder above its own.
- **Anything the host and the sidecar would have to agree on at start.** Each resolves and checks
  on its own. The host passes `--runtime-dir` as it does today and runs no layout check, so it
  can write before the sidecar refuses a layout. The sidecar is being decoupled from the host,
  and this sequence adds nothing that ties them.
- **The `..` checks**: `refuseDotDot` (`sidecar/config.go`), `layout.RefuseDotDot` and
  `plain_root` (`paths.rs`). They read the text alone, and `make clean-data` takes a `..` out by
  name.
- **`host.json` itself being a link.**
- **`make clean-data`.** It works on the unresolved layout and removes a link without emptying its
  target (`sidecar/scripts/clean-dev-data.go`).
- **A layout folder outside the Windows profile**, kept private by an ACL Kstack sets itself. The
  profile-ACL ADR rejected setting one.
- **Skipping a run's path lookups for Kstack's folders** (#1150).
