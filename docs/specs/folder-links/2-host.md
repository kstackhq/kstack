# Step 2: The host

The host resolves its folders once, at start, and keeps a linked config folder as the user made
it. Part of [the sequence](README.md), whose shared rules apply. It needs step 1, and the
Windows fix if step 1's check asked for one.

Paths are relative to `src-tauri/src/` unless rooted.

## Today

Each function in `paths.rs` computes the layout again and makes its folder on every call:
`app_state_dir`, `app_config_dir`, `log_dir`, `webview_dir` and `runtime_dir`. Each follows
whatever links are there at that moment. `host_file.rs` calls `ensure_config_dir` on every write.

## The resolve

`paths.rs` gains the host's folders, built once:

```rust
/// The host's folders, every path with its links followed.
pub struct Folders {
    pub state: PathBuf,
    /// `None` when the log folder cannot be made: the host logs to stderr.
    pub logs: Option<PathBuf>,
    /// `None` when the platform names no config root.
    pub config: Option<PathBuf>,
    #[cfg(not(target_os = "macos"))]
    pub webview: PathBuf,
    /// As `runtime_dir` names it: the sidecar resolves it.
    pub runtime: PathBuf,
}

impl Folders {
    pub fn make(roots: &Roots) -> Result<Self>
}
```

- `app.rs` calls `Folders::make` in `setup`, straight after `Roots::of` and before the log's file
  layer. Everything that takes `&Roots` for a folder today takes `&Folders`: `logging`,
  `host_file`, `webview_profile`, `window_state` and `SidecarService::spawn`.
- `make` runs [follow, check, then make](README.md#follow-check-then-make). Its check is
  [the Windows profile](README.md#on-windows-a-layout-folder-stays-in-the-profile); the layout
  check is the sidecar's alone, so the host can have written before the sidecar refuses a layout
  ([each program's check covers its own writes](README.md#follow-check-then-make)). It makes the folders in the order the functions above make them
  today: the app folder above a kind first
  (`ensure_app_dir_above`), and the state before the logs it holds.
- The five functions go, or become private steps of `make`. No caller names a folder twice.
- The host only needs the links followed. It records no link parent: the sidecar resolves the same
  folders and owns the fence.
- All of the host's folders exist before `SidecarService::spawn`, so the sidecar finds each one.

`make` fails the launch for what this sequence refuses: a link whose target is missing, a
linked `<state>/webview`, a folder outside the Windows profile. A folder that passes those and
then cannot be made does what it does today:

- **The logs**: `logs` is `None`, the error is logged, and the host goes on, logging to stderr.
- **The config**: no config root is `config: None`, with the preferences unavailable.
- **The state**: the launch fails where it fails today.

### Windows

`std::fs::canonicalize` answers a verbatim path (`\\?\C:\…`) on Windows.

- Check first whether WebView2 takes one as its profile folder.
- Whatever the answer, the host keeps no verbatim path: use `dunce::canonicalize`, which is in
  `Cargo.lock` through Tauri, as a direct dependency. A path only a verbatim prefix can spell,
  which `dunce` leaves verbatim, fails the launch with a message that names it.

### The runtime folder

This step does not change it. `runtime` is the path `runtime_dir` answers today, on every
platform: the host places its socket under it and passes it as `--runtime-dir`, unresolved. The
sidecar resolves it, and so sees every link above it.

On Unix `ensure_private_dir` still refuses a link at the folder itself, and
`private_dir_or_fresh` still takes a folder beside it.

## The linked config folder

A config folder that is itself a link at start is followed, and its mode is left alone: a
dotfiles repository's mode is the user's. Every other case keeps today's behaviour:

| The config folder, at start or at a write | Kstack |
| --- | --- |
| Was a link at start | Leaves its target's mode alone. |
| A folder that is owner-only | Leaves it untouched, so a read-only mount still yields its `host.json`. |
| A folder with any other mode | Tightens it to owner-only. |
| Missing, at start or because it was removed since | Makes it owner-only, at the resolved path. |

- `make` reads this once, before following, and keeps it beside `config`. `host_file`'s write
  reads it in place of `ensure_config_dir`'s own `symlink_metadata`, which would now see a folder.
- "Itself" is the layout's own name for the folder: `~/.config/kstack` on Linux, `<app>/config`
  on macOS and Windows. A link above it does not count: the folder below is one Kstack made.
- Every other host folder is made owner-only where it leads.
- `host.json` is read and written in the resolved folder.

## The webview's profile

`<state>/webview` is a subfolder, so a link there still fails the launch. The check uses the
meaning of "link" step 1 pinned, so a junction is refused on Windows.

## Tests

- A linked state, log and config folder each launch, and `Folders` names the target: a symlink
  on Unix, a junction on Windows.
- A layout folder that is a link to nothing fails the launch, and the message names both ends.
- A linked config folder keeps its target's mode, and `host.json` lands in the target
  (`a_linked_config_dir_keeps_its_targets_mode`, rewritten over `Folders`).
- A config folder under a linked `~/.config` is made owner-only.
- A config folder with an open mode is tightened; an owner-only one is not chmodded
  (`an_owner_only_config_dir_is_not_chmodded`); one removed after start is made owner-only by the
  next write of `host.json`.
- On Windows, a layout folder joined to a folder outside the profile fails the launch.
- A log folder that is no link and cannot be made leaves `logs` `None` and the launch going
  (`log_dir_fails_under_a_state_directory_that_cannot_be_made`, rewritten over `Folders`). A log
  folder that is a link to nothing still fails the launch.
- A linked `<state>/webview` is refused, on Unix (`a_linked_webview_dir_is_refused`) and as a
  junction.
- `--runtime-dir` carries the path `runtime_dir` answers, unresolved
  (`the_sidecar_is_handed_no_folder_but_the_runtime_directory`).
- **Both programs reach one folder**: with a linked layout folder, the host's `Folders` and the
  sidecar's name the same target. The sidecar's half is step 1's; this step adds the host's, over
  the same arrangement.
- No `Folders` path is verbatim on Windows.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | Check WebView2 against a verbatim profile path. | Planned |
| 2 | `paths.rs`: `Folders::make`, with the Unix and Windows resolve. | Planned |
| 3 | `app.rs` and the callers: take `&Folders`. | Planned |
| 4 | `paths.rs`, `host_file.rs`: the linked config folder. | Planned |
| 5 | The webview junction test, and the docs below. | Planned |

## When it lands

- **`src-tauri/CLAUDE.md`**: the host's folders are resolved once into `Folders`; what a linked
  config folder means.
- **[`security-model.md`](../../security-model.md)**: the rows that cite `paths.rs` name the new
  tests, and the linked-subfolder row names the junction test for `<state>/webview`.
- The ADR from step 1 gains nothing: it already states the rule for both programs.
