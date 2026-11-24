// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! Where the app keeps its files on disk.
//!
//! One place resolves the leaf ([`app_dir_name`]), because every directory
//! the app keeps needs it, and a second copy of the join is what silently drops
//! the `-dev` split.

use std::ffi::OsString;
use std::path::{Component, Path, PathBuf};

use tauri::{AppHandle, Manager, Runtime};

use crate::error::{AppError, Result};

/// The platform a layout is for. Each platform's [`layout`] is a pure function
/// of it, so every platform's paths are tested on every OS.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[cfg_attr(test, derive(serde::Deserialize), serde(rename_all = "lowercase"))]
enum Platform {
    Linux,
    MacOs,
    Windows,
}

impl Platform {
    /// The platform this build runs on.
    const CURRENT: Self = if cfg!(target_os = "linux") {
        Self::Linux
    } else if cfg!(target_os = "macos") {
        Self::MacOs
    } else {
        Self::Windows
    };
}

/// The leaf each of Kstack's directories takes under its platform root. A
/// dev build uses a `-dev` sibling so a dev run never collides with an
/// installed release. Linux names folders in lowercase; macOS and Windows show
/// the product name.
const fn app_dir_name(platform: Platform, dev: bool) -> &'static str {
    match (platform, dev) {
        (Platform::Linux, true) => "kstack-dev",
        (Platform::Linux, false) => "kstack",
        (Platform::MacOs | Platform::Windows, true) => "Kstack-dev",
        (Platform::MacOs | Platform::Windows, false) => "Kstack",
    }
}

/// Where each kind of directory sits on one platform, whether or not it
/// could be made.
#[derive(Debug, PartialEq, Eq)]
#[cfg_attr(test, derive(serde::Deserialize), serde(deny_unknown_fields))]
struct Layout {
    /// The folder macOS and Windows keep several kinds in. `None` on Linux,
    /// where each kind has a root. A per-user Windows install puts the
    /// release's executable in it too, so it is never deleted whole.
    app: Option<PathBuf>,
    /// Windows' leaf on the config root, which holds the config alone.
    config_app: Option<PathBuf>,
    data: PathBuf,
    /// `None` when the platform names no config root.
    config: Option<PathBuf>,
    state: PathBuf,
    cache: PathBuf,
    logs: PathBuf,
    /// Windows' alone: Unix keeps it under the session's runtime root
    /// ([`runtime_dir`]).
    #[cfg_attr(unix, allow(dead_code))]
    runtime: Option<PathBuf>,
}

/// Each kind takes a folder of its own, and none holds another's, so the
/// sidecar fences each and `make clean-data` deletes each by its path.
///
/// - Linux gives every kind a root (XDG's), so each is a leaf on its root.
/// - macOS has a root for the cache (`~/Library/Caches`, which backups skip)
///   and the logs (`~/Library/Logs`, which Console.app lists). The data and
///   config roots are one folder and there is no state root, so those three
///   are subfolders of one app folder.
/// - Windows' cache root is its local data root, and it has no state or
///   runtime root, so those kinds are siblings in one app folder under
///   `%LOCALAPPDATA%`, whose ACL covers them. The logs sit in the state, as on
///   Linux. The config roams, in `%APPDATA%`, as a subfolder of the leaf
///   there: were that root the local data root, the config would still be a
///   sibling of the other kinds.
fn layout_for(platform: Platform, dev: bool, roots: &Roots) -> Layout {
    let leaf = app_dir_name(platform, dev);
    let app = roots.local_data.join(leaf);
    match platform {
        Platform::Linux => {
            let state = roots.state.join(leaf);
            Layout {
                app: None,
                config_app: None,
                data: app,
                config: roots.config.as_ref().map(|dir| dir.join(leaf)),
                logs: state.join("logs"),
                state,
                cache: roots.cache.join(leaf),
                runtime: None,
            }
        }
        Platform::MacOs => Layout {
            data: app.join("data"),
            // In the app folder whatever the config root is, which decides
            // only whether there is a config directory.
            config: roots.config.as_ref().map(|_| app.join("config")),
            state: app.join("state"),
            cache: roots.cache.join(leaf),
            logs: roots.home.join("Library/Logs").join(leaf),
            runtime: None,
            config_app: None,
            app: Some(app),
        },
        Platform::Windows => Layout {
            data: app.join("data"),
            config: roots
                .config
                .as_ref()
                .map(|dir| dir.join(leaf).join("config")),
            config_app: roots.config.as_ref().map(|dir| dir.join(leaf)),
            state: app.join("state"),
            cache: app.join("cache"),
            logs: app.join("state").join("logs"),
            runtime: Some(app.join("run")),
            app: Some(app),
        },
    }
}

/// `platform`'s layout for this build's profile.
fn layout(platform: Platform, roots: &Roots) -> Layout {
    layout_for(platform, cfg!(debug_assertions), roots)
}

/// This platform's layout.
fn current_layout(roots: &Roots) -> Layout {
    layout(Platform::CURRENT, roots)
}

/// The platform directories Kstack's own hang off, resolved once from the
/// app. A test builds one over a scratch directory: Tauri's directory override
/// does not reach these. The sidecar reads the same roots from the
/// environment (`sidecar/internal/app/layout`).
#[cfg_attr(test, derive(serde::Deserialize), serde(deny_unknown_fields))]
pub struct Roots {
    /// `None` when the platform cannot name one: the preferences are then
    /// unavailable, never a failed launch.
    pub config: Option<PathBuf>,
    pub local_data: PathBuf,
    pub cache: PathBuf,
    pub home: PathBuf,
    /// XDG's state root, which only Linux reads.
    pub state: PathBuf,
}

impl Roots {
    pub fn of<R: Runtime>(app: &AppHandle<R>) -> Result<Self> {
        let path = app.path();
        let home = resolved(path.home_dir(), "home_dir")?;
        let state = state_home(std::env::var_os("XDG_STATE_HOME"), &home);
        Self::new(
            path.config_dir().ok(),
            resolved(path.local_data_dir(), "local_data_dir")?,
            resolved(path.cache_dir(), "cache_dir")?,
            home,
            state,
        )
    }

    /// The roots, each refused when it holds `..` ([`plain_root`]), which
    /// fails the launch: the sidecar fences the config, state and log
    /// directories by their paths, so one it is not given is unfenced. A root
    /// is checked where a directory hangs off it: the state root on Linux,
    /// the home on macOS, whose logs it holds.
    pub(crate) fn new(
        config: Option<PathBuf>,
        local_data: PathBuf,
        cache: PathBuf,
        home: PathBuf,
        state: PathBuf,
    ) -> Result<Self> {
        Ok(Self {
            config: config.map(plain_root).transpose()?,
            local_data: plain_root(local_data)?,
            cache: plain_root(cache)?,
            home: if cfg!(target_os = "macos") {
                plain_root(home)?
            } else {
                home
            },
            state: if cfg!(target_os = "linux") {
                plain_root(state)?
            } else {
                state
            },
        })
    }
}

/// XDG's state root: `$XDG_STATE_HOME` when it is absolute, which the spec
/// requires, else `~/.local/state`.
fn state_home(xdg_state_home: Option<OsString>, home: &Path) -> PathBuf {
    xdg_state_home
        .map(PathBuf::from)
        .filter(|dir| dir.is_absolute())
        .unwrap_or_else(|| home.join(".local/state"))
}

fn resolved(dir: tauri::Result<PathBuf>, name: &str) -> Result<PathBuf> {
    dir.map_err(|e| AppError::Io(std::io::Error::other(format!("resolve {name}: {e}"))))
}

/// `dir`, refused when it holds `..`. On Unix a `..` after a link names the
/// link target's parent, while the sidecar and `make clean-data` take it out
/// by name, so the host's files and their fences could be different folders.
/// The check reads the path alone, so it answers the same at every call.
/// Windows reads `..` by name itself.
fn plain_root(dir: PathBuf) -> Result<PathBuf> {
    if cfg!(unix) && dir.components().any(|c| c == Component::ParentDir) {
        return Err(AppError::Io(std::io::Error::other(format!(
            "{} holds `..`; set it without one",
            dir.display()
        ))));
    }
    Ok(dir)
}

/// The app's per-machine data directory, created owner-only.
///
/// Human-readable leaf on `local_data_dir`, not Tauri's bundle-id
/// `app_local_data_dir` (Application Support convention is a display name).
pub fn app_data_dir(roots: &Roots) -> Result<PathBuf> {
    let layout = current_layout(roots);
    ensure_app_dir_above(&layout, &layout.data)?;
    owner_only(layout.data)
}

/// The app's state directory, created owner-only: what one machine keeps
/// between runs and a user would not miss, the window frame and, on Linux and
/// Windows, the logs and the webview's profile.
pub fn app_state_dir(roots: &Roots) -> Result<PathBuf> {
    let layout = current_layout(roots);
    ensure_app_dir_above(&layout, &layout.state)?;
    owner_only(layout.state)
}

/// Makes the app folder owner-only when `dir` is in it, before `dir` is made
/// or read: whoever can write a folder can replace what is in it, so a private
/// kind in an open app folder is not private. One already owner-only is left
/// untouched, as [`ensure_config_dir`] leaves the config.
fn ensure_app_dir_above(layout: &Layout, dir: &Path) -> Result<()> {
    let Some(app) = layout.app.as_ref().filter(|app| dir.starts_with(app)) else {
        return Ok(());
    };
    if std::fs::metadata(app).is_ok_and(|meta| is_owner_only_dir(&meta)) {
        return Ok(());
    }
    ensure_data_dir(app)
}

/// The folders macOS and Windows keep the kinds in: the app folder, and on
/// Windows the config's too. The sidecar fences them beside the kinds, so
/// nothing is written among them.
pub fn app_dirs_for(roots: &Roots) -> Vec<PathBuf> {
    let layout = current_layout(roots);
    layout.app.into_iter().chain(layout.config_app).collect()
}

/// Where the state directory sits, whether or not it could be made: the
/// sidecar fences it either way.
pub fn state_dir_for(roots: &Roots) -> PathBuf {
    current_layout(roots).state
}

/// The app's config directory, created owner-only: what a user sets, which
/// Windows roams with the profile.
pub fn app_config_dir(roots: &Roots) -> Result<PathBuf> {
    let layout = current_layout(roots);
    let dir = layout
        .config
        .clone()
        .ok_or_else(|| AppError::Io(std::io::Error::other("resolve config_dir: none")))?;
    ensure_app_dir_above(&layout, &dir)?;
    ensure_config_dir(&dir)?;
    Ok(dir)
}

/// Creates the config directory owner-only, and tightens an existing one
/// unless it is a link. A dotfiles manager links `~/.config/<app>` into a
/// repository, whose mode is the user's to keep. One already owner-only is
/// left untouched, so a read-only mount still yields its `host.json`.
pub fn ensure_config_dir(path: &Path) -> Result<()> {
    let keep = std::fs::symlink_metadata(path)
        .is_ok_and(|meta| meta.file_type().is_symlink() || is_owner_only_dir(&meta));
    if keep {
        return Ok(());
    }
    ensure_data_dir(path)
}

#[cfg(unix)]
fn is_owner_only_dir(meta: &std::fs::Metadata) -> bool {
    use std::os::unix::fs::PermissionsExt;

    meta.is_dir() && meta.permissions().mode() & 0o777 == 0o700
}

/// Windows has no mode bits: the profile's ACL is the gate.
#[cfg(not(unix))]
fn is_owner_only_dir(_meta: &std::fs::Metadata) -> bool {
    false
}

/// Where the config directory sits, whether or not it could be made: the
/// sidecar fences a directory that exists but could not be tightened too.
pub fn config_dir_for(roots: &Roots) -> Option<PathBuf> {
    current_layout(roots).config
}

/// Creates the sidecar's data directory owner-only, and tightens one an earlier build
/// already created under the umask. On Windows the per-user `%LOCALAPPDATA%` ACL
/// already restricts it, so the plain create is enough.
pub fn ensure_data_dir(path: &Path) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::{DirBuilderExt, PermissionsExt};

        std::fs::DirBuilder::new()
            .recursive(true)
            .mode(0o700)
            .create(path)
            .map_err(AppError::Io)?;
        // A mode on DirBuilder applies only to directories it creates.
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))
            .map_err(AppError::Io)?;
    }
    #[cfg(not(unix))]
    std::fs::create_dir_all(path).map_err(AppError::Io)?;
    Ok(())
}

/// `dir`, created owner-only ([`ensure_data_dir`]).
fn owner_only(dir: PathBuf) -> Result<PathBuf> {
    ensure_data_dir(&dir)?;
    Ok(dir)
}

/// The app's cache directory, created owner-only: what Kstack rebuilds, the
/// mirror, the kubectl cache and a sandboxed run's `TMPDIR`.
pub fn app_cache_dir(roots: &Roots) -> Result<PathBuf> {
    let layout = current_layout(roots);
    ensure_app_dir_above(&layout, &layout.cache)?;
    owner_only(layout.cache)
}

/// The app's log directory, created owner-only. `app.rs` asks once and hands
/// the answer to both the host's log and the sidecar's, so the sidecar fences
/// the directory the host writes in.
///
/// Not Tauri's `app_log_dir`: it names the leaf by bundle id rather than the
/// display name everything else here uses, and it has no debug variant, so a
/// dev run would write into an installed release's logs.
pub fn log_dir(roots: &Roots) -> Result<PathBuf> {
    let layout = current_layout(roots);
    ensure_app_dir_above(&layout, &layout.logs)?;
    // `owner_only` tightens the leaf alone, and on Linux and Windows the logs
    // sit in a state directory an earlier run may have left open.
    if layout.logs.starts_with(&layout.state) {
        ensure_data_dir(&layout.state)?;
    }
    owner_only(layout.logs)
}

/// The webview's profile directory on Linux and Windows: a leaf on the state
/// directory, so a dev build keeps its own and the sidecar's fence covers it.
/// The webview makes the directory itself, inside a state directory this
/// user has made owner-only: whoever owns a directory can replace what is in
/// it. WebKitGTK takes the path as a string, so on Linux one that is not
/// UTF-8 is refused: its lossy spelling would be a folder outside the fence.
/// So is a link, which would lead the profile out of it.
#[cfg(not(target_os = "macos"))]
pub fn webview_dir(roots: &Roots) -> Result<PathBuf> {
    let dir = app_state_dir(roots)?.join("webview");
    if std::fs::symlink_metadata(&dir).is_ok_and(|meta| meta.file_type().is_symlink()) {
        return Err(AppError::Io(std::io::Error::other(format!(
            "{} is a link; link the state directory instead",
            dir.display()
        ))));
    }
    if cfg!(target_os = "linux") && dir.to_str().is_none() {
        return Err(AppError::Io(std::io::Error::other(format!(
            "{} is not UTF-8, which the webview's profile needs",
            dir.display()
        ))));
    }
    Ok(dir)
}

/// The directory for what lives for a session, owner-only: the IPC endpoint,
/// the shell snapshot, and each sandboxed run's kubeconfig and socket.
///
/// The gain is on Linux, where the fallback would be the shared, sticky-bit
/// `/tmp`: `$XDG_RUNTIME_DIR` is a per-user directory the session manager
/// already creates `0700`. On macOS `$TMPDIR` is per-user and `0700` too, so
/// there the subdirectory is tidiness rather than a fix.
///
/// A directory left behind by a crashed run is adopted, not refused. Nothing
/// sweeps it, because a second window of a running app shares it and a
/// concurrent copy owns its own. Outside `$XDG_RUNTIME_DIR` a name that is
/// refused gives way to one beside it ([`private_dir_or_fresh`]).
#[cfg(unix)]
pub fn runtime_dir(_roots: &Roots) -> Result<PathBuf> {
    // SAFETY: `geteuid` reads process state and cannot fail.
    let uid = unsafe { libc::geteuid() };
    let xdg = runtime_home(std::env::var_os("XDG_RUNTIME_DIR"));
    let dir = runtime_dir_path(xdg.as_deref(), uid)?;
    if xdg.is_none() {
        return private_dir_or_fresh(&dir, uid);
    }
    ensure_private_dir(&dir, uid)?;
    Ok(dir)
}

/// `dir`, made private by [`ensure_private_dir`], or when that refuses it a
/// directory beside it: the one an earlier launch made, else a fresh one. The
/// name under the temp dir is predictable, so another user can claim it first;
/// a random suffix cannot be claimed ahead. Launches keep to one directory
/// because the sidecar sweeps what a crashed run left in the one it is given:
/// a fresh one per launch would never be swept.
#[cfg(unix)]
fn private_dir_or_fresh(dir: &Path, uid: u32) -> Result<PathBuf> {
    match ensure_private_dir(dir, uid) {
        Ok(()) => Ok(dir.to_path_buf()),
        Err(err) => {
            tracing::warn!(%err, "runtime directory refused; using one beside it");
            match earlier_fresh_dir(dir, uid) {
                Some(earlier) => Ok(earlier),
                None => fresh_private_dir(dir),
            }
        }
    }
}

/// The first directory beside `dir` named as [`fresh_private_dir`] names one
/// that [`ensure_private_dir`] adopts: a directory this user owns, never a
/// link. Another user cannot make one this user owns, and the temp dir is
/// sticky, so none is swapped in after the check.
#[cfg(unix)]
fn earlier_fresh_dir(dir: &Path, uid: u32) -> Option<PathBuf> {
    let parent = dir.parent()?;
    let prefix = format!("{}-", dir.file_name()?.to_str()?);
    let mut names: Vec<String> = std::fs::read_dir(parent)
        .ok()?
        .filter_map(|entry| entry.ok()?.file_name().into_string().ok())
        .filter(|name| {
            name.strip_prefix(&prefix)
                .is_some_and(|suffix| suffix.len() == FRESH_SUFFIX_LEN)
        })
        .collect();
    names.sort();
    names
        .into_iter()
        .map(|name| parent.join(name))
        .find(|candidate| ensure_private_dir(candidate, uid).is_ok())
}

/// The length of the random suffix `mkdtemp` writes over `XXXXXX`.
#[cfg(unix)]
const FRESH_SUFFIX_LEN: usize = 6;

/// A new directory named `dir` plus a random suffix, made `0700` by `mkdtemp`,
/// which fails rather than reuse anything already there.
#[cfg(unix)]
fn fresh_private_dir(dir: &Path) -> Result<PathBuf> {
    use std::os::unix::ffi::{OsStrExt, OsStringExt};

    let mut template = dir.as_os_str().as_bytes().to_vec();
    template.extend_from_slice(b"-XXXXXX\0");
    // SAFETY: the template is NUL-terminated, and mkdtemp rewrites only its
    // trailing XXXXXX, in place.
    let made = unsafe { libc::mkdtemp(template.as_mut_ptr().cast()) };
    if made.is_null() {
        return Err(AppError::Io(std::io::Error::last_os_error()));
    }
    template.pop();
    Ok(PathBuf::from(std::ffi::OsString::from_vec(template)))
}

/// Creates `dir` `0700`, or adopts one an earlier run left behind — but only
/// once this user is proved to own it, and never through a symlink.
///
/// The order is the whole protection: tightening first would let another user
/// point `/tmp/kstack-<uid>` at a directory of ours and have the app chmod it
/// on their behalf. Past the check there is no swap to lose to — `/tmp` is
/// sticky, so only the owner can replace the entry.
#[cfg(unix)]
fn ensure_private_dir(dir: &Path, uid: u32) -> Result<()> {
    use std::os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt};

    match std::fs::DirBuilder::new().mode(0o700).create(dir) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {
            // A name is not ownership, and `symlink_metadata` is what refuses a
            // link: whoever planted it owns where it leads.
            let meta = std::fs::symlink_metadata(dir).map_err(AppError::Io)?;
            if !meta.is_dir() || meta.uid() != uid {
                return Err(AppError::Io(std::io::Error::other(format!(
                    "{} is not a directory owned by this user",
                    dir.display()
                ))));
            }
        }
        Err(e) => return Err(AppError::Io(e)),
    }
    // mkdir's mode is masked by the umask, and an adopted directory carries
    // whatever mode the earlier run left it with.
    std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700)).map_err(AppError::Io)
}

/// The directory for what lives for a session. Windows has no per-user runtime
/// directory, and `TEMP` can point at one users share, so it sits in the app
/// folder under `%LOCALAPPDATA%`, which the profile ACL covers ([`layout`]).
/// The named pipe ignores it: its namespace is flat, so the DACL and the peer
/// check are its whole policy.
#[cfg(not(unix))]
pub fn runtime_dir(roots: &Roots) -> Result<PathBuf> {
    let layout = current_layout(roots);
    let dir = layout
        .runtime
        .clone()
        .ok_or_else(|| AppError::Io(std::io::Error::other("no runtime directory")))?;
    ensure_app_dir_above(&layout, &dir)?;
    owner_only(dir)
}

/// XDG's runtime root: `$XDG_RUNTIME_DIR` when it is absolute, which the spec
/// requires, and a directory. A relative one would name a different folder
/// from each working directory.
#[cfg(unix)]
fn runtime_home(xdg_runtime_dir: Option<OsString>) -> Option<PathBuf> {
    xdg_runtime_dir
        .map(PathBuf::from)
        .filter(|dir| dir.is_absolute() && dir.is_dir())
}

/// Where [`runtime_dir`] puts the directory: under `$XDG_RUNTIME_DIR` when the
/// session manager gave us one, else under the temp dir.
///
/// The temp dir is shared between users on Linux, so the uid goes in the name:
/// one directory for all of them means the first user to run owns it `0700` and
/// every later user's chmod fails with EPERM.
#[cfg(unix)]
fn runtime_dir_path(xdg: Option<&Path>, uid: u32) -> Result<PathBuf> {
    Ok(match xdg {
        Some(base) => plain_root(base.to_path_buf())?.join(RUNTIME_DIR_NAME),
        None => plain_root(std::env::temp_dir())?.join(format!("{RUNTIME_DIR_NAME}-{uid}")),
    })
}

/// Leaf for [`runtime_dir`]. A `-dev` sibling like [`app_dir_name`]'s, so a dev
/// run and an installed release never share a socket directory.
#[cfg(unix)]
const RUNTIME_DIR_NAME: &str = if cfg!(debug_assertions) {
    "kstack-dev"
} else {
    "kstack"
};

#[cfg(test)]
mod tests {
    use super::*;

    /// The data directory is the outer wall around the sidecar's caches, and the
    /// only one Windows has. Unix only — Windows has no POSIX mode bits.
    #[cfg(unix)]
    #[test]
    fn ensure_data_dir_creates_and_tightens_to_0700() {
        use std::os::unix::fs::PermissionsExt;

        let base = std::env::temp_dir().join(format!("kstack-ensure-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);

        let fresh = base.join("fresh");
        ensure_data_dir(&fresh).expect("create");
        assert_eq!(
            std::fs::metadata(&fresh).unwrap().permissions().mode() & 0o777,
            0o700
        );

        // Every install before this landed has a 0755 directory, and a mode on
        // DirBuilder applies only to directories it creates.
        let existing = base.join("existing");
        std::fs::create_dir(&existing).unwrap();
        std::fs::set_permissions(&existing, std::fs::Permissions::from_mode(0o755)).unwrap();
        ensure_data_dir(&existing).expect("tighten");
        assert_eq!(
            std::fs::metadata(&existing).unwrap().permissions().mode() & 0o777,
            0o700
        );

        std::fs::remove_dir_all(&base).unwrap();
    }

    /// The log file holds cluster names and server hostnames, the same class of
    /// text as the data directory beside it, so the directory is the gate:
    /// files inside it are created under the umask. Unix only — Windows has no
    /// POSIX mode bits.
    #[cfg(unix)]
    #[test]
    fn log_dir_is_created_0700() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let dir = log_dir(&roots).expect("create");
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
    }

    /// The cache holds what Kstack rebuilds, so it is created owner-only like
    /// the data it was built from. Unix only — Windows has no POSIX mode bits.
    #[cfg(unix)]
    #[test]
    fn cache_dir_is_created_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let dir = app_cache_dir(&roots).expect("create");
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
    }

    /// Settings are the user's, like the data. Unix only — Windows has no POSIX
    /// mode bits.
    #[cfg(unix)]
    #[test]
    fn config_dir_is_created_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let dir = app_config_dir(&crate::testutil::roots(scratch.path())).expect("create");
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
    }

    #[cfg(unix)]
    #[test]
    fn a_linked_config_dir_keeps_its_targets_mode() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());
        let target = scratch.path().join("dotfiles");
        std::fs::create_dir(&target).unwrap();
        std::fs::set_permissions(&target, std::fs::Permissions::from_mode(0o755)).unwrap();
        let link = config_dir_for(&roots).unwrap();
        std::fs::create_dir_all(link.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(&target, &link).unwrap();

        assert_eq!(app_config_dir(&roots).unwrap(), link);
        assert_eq!(
            std::fs::metadata(&target).unwrap().permissions().mode() & 0o777,
            0o755
        );
    }

    #[test]
    fn no_config_root_is_an_error_not_a_guess() {
        let scratch = crate::testutil::Scratch::new();
        let roots = Roots {
            config: None,
            ..crate::testutil::roots(scratch.path())
        };
        let err = app_config_dir(&roots).unwrap_err();
        assert!(err.to_string().contains("config_dir"), "got: {err}");
    }

    /// The endpoint's directory is the wall around the socket. `/tmp` is shared
    /// and sticky-bit only, so anything that can enter the directory can reach
    /// the address the host is about to dial.
    #[cfg(unix)]
    #[test]
    fn runtime_dir_is_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let dir = runtime_dir(&crate::testutil::roots(scratch.path())).expect("runtime dir");
        let mode = std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o700, "the runtime dir must be owner-only");
    }

    #[cfg(unix)]
    #[test]
    fn the_runtime_root_is_an_absolute_directory() {
        let scratch = crate::testutil::Scratch::new();
        let dir = scratch.path().to_path_buf();

        assert_eq!(runtime_home(Some(dir.clone().into())), Some(dir.clone()));
        // The working directory exists, and is still not taken by a relative name.
        assert_eq!(runtime_home(Some(".".into())), None);
        assert_eq!(runtime_home(Some(dir.join("missing").into())), None);
        assert_eq!(runtime_home(None), None);
    }

    /// Without `$XDG_RUNTIME_DIR` the fallback lands in a temp dir every user
    /// shares, so two users must not name the same directory: the first to run
    /// owns it `0700` and the second cannot chmod it.
    #[cfg(unix)]
    #[test]
    fn runtime_dir_path_is_per_user_outside_xdg() {
        let mine = runtime_dir_path(None, 1000).unwrap();
        let theirs = runtime_dir_path(None, 1001).unwrap();
        assert_ne!(mine, theirs);

        // $XDG_RUNTIME_DIR is already per-user, so the uid stays out of the name.
        let xdg = Path::new("/run/user/1000");
        assert_eq!(
            runtime_dir_path(Some(xdg), 1000).unwrap(),
            xdg.join(RUNTIME_DIR_NAME)
        );
    }

    /// Another user can plant the fallback name as a symlink into a directory of
    /// ours. Refusing it is half the fix; the other half is refusing it *before*
    /// the chmod, or the app tightens the target on the attacker's behalf.
    #[cfg(unix)]
    #[test]
    fn ensure_private_dir_refuses_a_symlink_without_touching_its_target() {
        use std::os::unix::fs::PermissionsExt;

        let base = std::env::temp_dir().join(format!("kstack-symlink-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let target = base.join("victim");
        std::fs::create_dir_all(&target).expect("create victim");
        std::fs::set_permissions(&target, std::fs::Permissions::from_mode(0o755))
            .expect("loosen victim");

        let planted = base.join("planted");
        std::os::unix::fs::symlink(&target, &planted).expect("plant symlink");

        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };
        assert!(ensure_private_dir(&planted, uid).is_err());
        assert_eq!(
            std::fs::metadata(&target).unwrap().permissions().mode() & 0o777,
            0o755,
            "the symlink's target must be left alone"
        );

        let _ = std::fs::remove_dir_all(&base);
    }

    /// Another user can claim the fallback name before this user's first run.
    /// That must not keep the app from starting: a fresh directory of a random
    /// name, which nobody can claim ahead, takes its place, and what the squatter
    /// planted is left alone.
    #[cfg(unix)]
    #[test]
    fn a_squatted_fallback_gives_way_to_a_fresh_directory() {
        use std::os::unix::fs::PermissionsExt;

        let base = std::env::temp_dir().join(format!("kstack-squat-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let victim = base.join("victim");
        std::fs::create_dir_all(&victim).expect("create victim");
        std::fs::set_permissions(&victim, std::fs::Permissions::from_mode(0o755))
            .expect("loosen victim");
        let planted = base.join("kstack-1000");
        std::os::unix::fs::symlink(&victim, &planted).expect("plant symlink");

        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };
        let dir = private_dir_or_fresh(&planted, uid).expect("a fresh directory");

        assert_eq!(dir.parent(), Some(base.as_path()));
        let name = dir.file_name().unwrap().to_string_lossy().into_owned();
        assert!(name.starts_with("kstack-1000-"), "{name}");
        let meta = std::fs::symlink_metadata(&dir).unwrap();
        assert!(meta.is_dir());
        assert_eq!(meta.permissions().mode() & 0o777, 0o700);
        assert_eq!(
            std::fs::metadata(&victim).unwrap().permissions().mode() & 0o777,
            0o755,
            "the squatter's target must be left alone"
        );

        let _ = std::fs::remove_dir_all(&base);
    }

    /// A later launch under a squatted name keeps to the directory an earlier
    /// one made beside it, so the sidecar's sweep reaches what a crashed run
    /// left there and launches do not pile directories up in the temp dir.
    #[cfg(unix)]
    #[test]
    fn a_later_launch_keeps_the_directory_an_earlier_one_made() {
        let base = std::env::temp_dir().join(format!("kstack-again-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let victim = base.join("victim");
        std::fs::create_dir_all(&victim).expect("create victim");
        let planted = base.join("kstack-1000");
        std::os::unix::fs::symlink(&victim, &planted).expect("plant symlink");

        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };
        let first = private_dir_or_fresh(&planted, uid).expect("a fresh directory");
        let second = private_dir_or_fresh(&planted, uid).expect("the same directory");

        assert_eq!(first, second);
        let _ = std::fs::remove_dir_all(&base);
    }

    /// A link named as a fresh directory is named is passed over, never
    /// adopted: whoever planted it owns where it leads.
    #[cfg(unix)]
    #[test]
    fn a_link_named_like_an_earlier_directory_is_passed_over() {
        use std::os::unix::fs::PermissionsExt;

        let base = std::env::temp_dir().join(format!("kstack-lookalike-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let victim = base.join("victim");
        std::fs::create_dir_all(&victim).expect("create victim");
        std::fs::set_permissions(&victim, std::fs::Permissions::from_mode(0o755))
            .expect("loosen victim");
        let planted = base.join("kstack-1000");
        std::os::unix::fs::symlink(&victim, &planted).expect("plant symlink");
        let lookalike = base.join("kstack-1000-aaaaaa");
        std::os::unix::fs::symlink(&victim, &lookalike).expect("plant lookalike");

        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };
        let dir = private_dir_or_fresh(&planted, uid).expect("a fresh directory");

        assert_ne!(dir, lookalike);
        let meta = std::fs::symlink_metadata(&dir).unwrap();
        assert!(meta.is_dir());
        assert_eq!(
            std::fs::metadata(&victim).unwrap().permissions().mode() & 0o777,
            0o755,
            "the lookalike's target must be left alone"
        );

        let _ = std::fs::remove_dir_all(&base);
    }

    /// A fallback name this user owns is kept, since the sidecar sweeps what a
    /// crashed run left in it.
    #[cfg(unix)]
    #[test]
    fn a_fallback_this_user_owns_is_kept() {
        let base = std::env::temp_dir().join(format!("kstack-owned-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let dir = base.join("kstack-1000");
        std::fs::create_dir_all(&dir).expect("create");

        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };
        assert_eq!(private_dir_or_fresh(&dir, uid).expect("adopt"), dir);

        let _ = std::fs::remove_dir_all(&base);
    }

    #[cfg(windows)]
    #[test]
    fn runtime_dir_is_created_in_the_app_folder() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let dir = runtime_dir(&roots).expect("runtime dir");

        assert_eq!(Some(&dir), current_layout(&roots).runtime.as_ref());
        assert!(dir.is_dir());
    }

    #[test]
    fn roots_resolve_on_the_app() {
        let scratch = crate::testutil::Scratch::new();
        let app = crate::testutil::mock_app(
            scratch.path(),
            crate::services::sidecar::SidecarService::without_process(None),
        );
        let roots = Roots::of(app.handle()).unwrap();
        assert!(roots.config.unwrap().is_absolute());
        assert!(roots.local_data.is_absolute());
        assert!(roots.cache.is_absolute());
        assert!(roots.home.is_absolute());
        assert!(roots.state.is_absolute());
    }

    /// A `..` is refused whatever is on disk: a link before it would make the
    /// folder the filesystem opens differ from the one the sidecar fences.
    #[cfg(unix)]
    #[test]
    fn a_root_holding_dotdot_is_refused() {
        let err = plain_root(PathBuf::from("/home/x/link/../conf")).unwrap_err();
        assert!(err.to_string().contains("link/../conf"), "got: {err}");
        assert!(plain_root(PathBuf::from("../conf")).is_err());
    }

    /// Windows takes `..` out by name, as the sidecar does.
    #[cfg(windows)]
    #[test]
    fn a_root_holding_dotdot_stands_on_windows() {
        let root = PathBuf::from(r"C:\Users\x\link\..\conf");
        assert_eq!(plain_root(root.clone()).unwrap(), root);
    }

    /// A root with no `..` stands as given, link or missing: the sidecar
    /// resolves its links itself.
    #[test]
    fn a_root_without_dotdot_stands_as_given() {
        let scratch = crate::testutil::Scratch::new();
        let missing = scratch.path().join("missing");

        assert_eq!(plain_root(missing.clone()).unwrap(), missing);
    }

    /// A refused root fails the launch, the config root included, since a
    /// directory the sidecar is not given is not fenced. Only macOS hangs a
    /// directory off the home.
    #[cfg(unix)]
    #[test]
    fn a_refused_root_fails_the_launch() {
        let scratch = crate::testutil::Scratch::new();
        let fine = crate::testutil::roots(scratch.path());
        let dotted = scratch.path().join("link/../root");
        let with = |config: Option<PathBuf>, local_data: PathBuf, home: PathBuf| {
            Roots::new(
                config,
                local_data,
                fine.cache.clone(),
                home,
                fine.state.clone(),
            )
        };

        assert!(with(
            Some(dotted.clone()),
            fine.local_data.clone(),
            fine.home.clone()
        )
        .is_err());
        assert!(with(fine.config.clone(), dotted.clone(), fine.home.clone()).is_err());
        assert!(Roots::new(
            fine.config.clone(),
            fine.local_data.clone(),
            dotted.clone(),
            fine.home.clone(),
            fine.state.clone()
        )
        .is_err());
        assert_eq!(
            with(fine.config.clone(), fine.local_data.clone(), dotted).is_err(),
            cfg!(target_os = "macos")
        );
        assert!(with(None, fine.local_data.clone(), fine.home.clone())
            .unwrap()
            .config
            .is_none());
    }

    /// `$XDG_RUNTIME_DIR` is a root the host hands the sidecar too.
    #[cfg(unix)]
    #[test]
    fn a_runtime_root_holding_dotdot_is_refused() {
        assert!(runtime_dir_path(Some(Path::new("/run/link/../user")), 1000).is_err());
    }

    /// An owner-only config directory is not chmod-ed again: a setgid bit,
    /// which a chmod to `0700` would clear, survives.
    #[cfg(unix)]
    #[test]
    fn an_owner_only_config_dir_is_not_chmodded() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let dir = scratch.path().join("config");
        std::fs::create_dir(&dir).unwrap();
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o2700)).unwrap();
        let mode = || std::fs::metadata(&dir).unwrap().permissions().mode() & 0o7777;
        assert_eq!(mode(), 0o2700, "the setgid bit must be settable here");

        ensure_config_dir(&dir).unwrap();

        assert_eq!(mode(), 0o2700);
    }

    #[test]
    fn an_unresolvable_root_is_named_in_the_error() {
        let err = resolved(Err(tauri::Error::UnknownPath), "home_dir").unwrap_err();
        assert!(err.to_string().contains("resolve home_dir"), "got: {err}");
    }

    /// XDG ignores a base directory that is not absolute.
    #[test]
    fn the_state_root_is_xdg_state_home_else_under_the_home() {
        let home = Path::new("/home/x");
        let fallback = home.join(".local/state");
        let absolute = std::env::temp_dir();

        assert_eq!(state_home(Some(absolute.clone().into()), home), absolute);
        assert_eq!(state_home(Some("state".into()), home), fallback);
        assert_eq!(state_home(Some("".into()), home), fallback);
        assert_eq!(state_home(None, home), fallback);
    }

    #[cfg(unix)]
    #[test]
    fn state_dir_is_created_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let dir = app_state_dir(&crate::testutil::roots(scratch.path())).unwrap();

        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
    }

    /// A state directory the sidecar is not given is not fenced, so on Linux
    /// a refused state root fails the launch. Elsewhere it is not read.
    #[cfg(unix)]
    #[test]
    fn a_refused_state_root_fails_the_launch_on_linux() {
        let scratch = crate::testutil::Scratch::new();
        let fine = crate::testutil::roots(scratch.path());

        let roots = Roots::new(
            fine.config,
            fine.local_data,
            fine.cache,
            fine.home,
            scratch.path().join("link/../state"),
        );

        assert_eq!(roots.is_err(), cfg!(target_os = "linux"));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn a_linked_webview_dir_is_refused() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());
        let target = scratch.path().join("elsewhere");
        std::fs::create_dir(&target).unwrap();
        let link = app_state_dir(&roots).unwrap().join("webview");
        std::os::unix::fs::symlink(&target, &link).unwrap();

        let err = webview_dir(&roots).unwrap_err();

        assert!(err.to_string().contains("is a link"), "got: {err}");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn a_webview_dir_that_is_not_utf8_is_refused() {
        use std::os::unix::ffi::OsStrExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = Roots {
            state: scratch
                .path()
                .join(std::ffi::OsStr::from_bytes(b"st\xffate")),
            ..crate::testutil::roots(scratch.path())
        };

        let err = webview_dir(&roots).unwrap_err();

        assert!(err.to_string().contains("not UTF-8"), "got: {err}");
    }

    /// The sidecar fences the state directory by the path the host keeps its
    /// files under.
    #[test]
    fn the_state_dir_is_named_before_it_is_made() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let named = state_dir_for(&roots);

        assert!(!named.exists());
        assert_eq!(app_state_dir(&roots).unwrap(), named);
    }

    /// The roots Tauri resolves on `platform`, so each platform's layout is
    /// checked on every OS. Windows' are written with `/`, which it reads as
    /// a separator too.
    fn platform_roots(platform: Platform) -> Roots {
        let roots = |config: &str, local_data: &str, cache: &str, home: &str, state: &str| Roots {
            config: Some(PathBuf::from(config)),
            local_data: PathBuf::from(local_data),
            cache: PathBuf::from(cache),
            home: PathBuf::from(home),
            state: PathBuf::from(state),
        };
        match platform {
            Platform::Linux => roots(
                "/home/x/.config",
                "/home/x/.local/share",
                "/home/x/.cache",
                "/home/x",
                "/home/x/.local/state",
            ),
            Platform::MacOs => roots(
                "/Users/x/Library/Application Support",
                "/Users/x/Library/Application Support",
                "/Users/x/Library/Caches",
                "/Users/x",
                "/Users/x/.local/state",
            ),
            Platform::Windows => roots(
                "C:/Users/x/AppData/Roaming",
                "C:/Users/x/AppData/Local",
                "C:/Users/x/AppData/Local",
                "C:/Users/x",
                "C:/Users/x/.local/state",
            ),
        }
    }

    /// One case of `sidecar/internal/app/layout/testdata/layouts.json`, which the sidecar's
    /// `layout` package reads too, so the two layouts cannot drift.
    #[derive(serde::Deserialize)]
    #[serde(deny_unknown_fields)]
    struct LayoutCase {
        name: String,
        platform: Platform,
        dev: bool,
        roots: Roots,
        layout: Layout,
    }

    #[test]
    fn each_platforms_layout_is_the_fixtures() {
        let cases: Vec<LayoutCase> = serde_json::from_str(include_str!(
            "../../sidecar/internal/app/layout/testdata/layouts.json"
        ))
        .unwrap();
        for case in &cases {
            assert_eq!(
                layout_for(case.platform, case.dev, &case.roots),
                case.layout,
                "{}",
                case.name
            );
        }
        for platform in [Platform::Linux, Platform::MacOs, Platform::Windows] {
            assert!(cases.iter().any(|case| case.platform == platform));
        }
    }

    #[test]
    fn linux_gives_each_kind_a_leaf_on_its_own_root() {
        let roots = platform_roots(Platform::Linux);
        let leaf = app_dir_name(Platform::Linux, cfg!(debug_assertions));

        let layout = layout(Platform::Linux, &roots);

        assert_eq!(leaf.trim_end_matches("-dev"), "kstack");
        assert_eq!(leaf.ends_with("-dev"), cfg!(debug_assertions));
        assert_eq!(layout.app, None);
        assert_eq!(layout.data, roots.local_data.join(leaf));
        assert_eq!(layout.config, Some(roots.config.unwrap().join(leaf)));
        assert_eq!(layout.state, roots.state.join(leaf));
        assert_eq!(layout.cache, roots.cache.join(leaf));
        assert_eq!(layout.logs, roots.state.join(leaf).join("logs"));
        assert_eq!(layout.runtime, None);
    }

    /// macOS's config root is its data root, so the two kinds and the state,
    /// which has no root of its own there, are subfolders of one app folder.
    /// The cache and the logs keep the platform's own folders.
    #[test]
    fn macos_keeps_data_config_and_state_in_one_app_folder() {
        let roots = platform_roots(Platform::MacOs);
        let leaf = app_dir_name(Platform::MacOs, cfg!(debug_assertions));
        let app = roots.local_data.join(leaf);

        let layout = layout(Platform::MacOs, &roots);

        assert_eq!(leaf.trim_end_matches("-dev"), "Kstack");
        assert_eq!(leaf.ends_with("-dev"), cfg!(debug_assertions));
        assert_eq!(layout.data, app.join("data"));
        assert_eq!(layout.config, Some(app.join("config")));
        assert_eq!(layout.state, app.join("state"));
        assert_eq!(layout.cache, roots.cache.join(leaf));
        assert_eq!(layout.logs, roots.home.join("Library/Logs").join(leaf));
        assert_eq!(layout.runtime, None);
        assert_eq!(layout.app, Some(app));
    }

    /// macOS's config directory is in the app folder even under a config root
    /// that is not the data root, and there is none without a config root.
    #[test]
    fn macos_config_follows_the_app_folder() {
        let mut roots = platform_roots(Platform::MacOs);
        let app = roots
            .local_data
            .join(app_dir_name(Platform::MacOs, cfg!(debug_assertions)));

        roots.config = Some(PathBuf::from("/Users/x/elsewhere"));
        assert_eq!(
            layout(Platform::MacOs, &roots).config,
            Some(app.join("config"))
        );

        roots.config = None;
        assert_eq!(layout(Platform::MacOs, &roots).config, None);
    }

    /// A state directory that cannot be made fails the logs it would hold.
    #[cfg(target_os = "linux")]
    #[test]
    fn log_dir_fails_under_a_state_directory_that_cannot_be_made() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());
        let state = state_dir_for(&roots);
        std::fs::create_dir_all(state.parent().unwrap()).unwrap();
        std::fs::write(&state, "").unwrap();

        assert!(log_dir(&roots).is_err());
    }

    #[test]
    fn the_running_os_takes_its_own_layout() {
        #[cfg(target_os = "linux")]
        assert_eq!(Platform::CURRENT, Platform::Linux);
        #[cfg(target_os = "macos")]
        assert_eq!(Platform::CURRENT, Platform::MacOs);
        #[cfg(windows)]
        assert_eq!(Platform::CURRENT, Platform::Windows);
    }

    /// The logs sit in the state directory on Linux, so asking for them
    /// tightens one an earlier run left open.
    #[cfg(target_os = "linux")]
    #[test]
    fn log_dir_tightens_the_state_directory() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());
        let state = state_dir_for(&roots);
        std::fs::create_dir_all(&state).unwrap();
        std::fs::set_permissions(&state, std::fs::Permissions::from_mode(0o755)).unwrap();

        log_dir(&roots).unwrap();

        let mode = std::fs::metadata(&state).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o700);
    }

    /// Windows' cache root is its local data root, and it has no state or
    /// runtime root, so those kinds are siblings in one app folder, the logs
    /// in the state as on Linux. The config roams, in a root of its own.
    #[test]
    fn windows_keeps_every_local_kind_side_by_side_in_one_app_folder() {
        let roots = platform_roots(Platform::Windows);
        let leaf = app_dir_name(Platform::Windows, cfg!(debug_assertions));
        let app = roots.local_data.join(leaf);

        let layout = layout(Platform::Windows, &roots);

        assert_eq!(leaf.trim_end_matches("-dev"), "Kstack");
        assert_eq!(leaf.ends_with("-dev"), cfg!(debug_assertions));
        assert_eq!(layout.data, app.join("data"));
        assert_eq!(layout.cache, app.join("cache"));
        assert_eq!(layout.state, app.join("state"));
        assert_eq!(layout.logs, app.join("state").join("logs"));
        assert_eq!(layout.runtime, Some(app.join("run")));
        let config_app = roots.config.unwrap().join(leaf);
        assert_eq!(layout.config, Some(config_app.join("config")));
        assert_eq!(layout.config_app, Some(config_app));
        assert_eq!(layout.app, Some(app));
    }

    /// Windows' roaming and local roots resolved to one folder.
    fn windows_shared_roots() -> Roots {
        let mut roots = platform_roots(Platform::Windows);
        roots.config = Some(roots.local_data.clone());
        roots
    }

    /// The config is a subfolder of its leaf, so on the local data root it is
    /// a sibling of the other kinds, not the app folder that holds them.
    #[test]
    fn windows_config_on_the_local_root_is_a_subfolder_of_the_app_folder() {
        let roots = windows_shared_roots();
        let app = roots
            .local_data
            .join(app_dir_name(Platform::Windows, cfg!(debug_assertions)));

        let layout = layout(Platform::Windows, &roots);

        assert_eq!(layout.config, Some(app.join("config")));
    }

    /// Each kind is fenced and deleted on its own, so none may be another's
    /// folder or sit inside one.
    #[test]
    fn no_kind_shares_or_holds_another_kinds_folder() {
        let cases = [
            (Platform::Linux, platform_roots(Platform::Linux)),
            (Platform::MacOs, platform_roots(Platform::MacOs)),
            (Platform::Windows, platform_roots(Platform::Windows)),
            (Platform::Windows, windows_shared_roots()),
        ];
        for (platform, roots) in cases {
            let layout = layout(platform, &roots);
            let mut kinds = vec![
                ("data", layout.data),
                ("state", layout.state),
                ("cache", layout.cache),
            ];
            kinds.extend(layout.config.map(|dir| ("config", dir)));
            kinds.extend(layout.runtime.map(|dir| ("runtime", dir)));
            for (name, dir) in &kinds {
                for (other, other_dir) in &kinds {
                    assert!(
                        name == other || !other_dir.starts_with(dir),
                        "{platform:?}: {other} {other_dir:?} is in {name} {dir:?}"
                    );
                }
            }
        }
    }

    #[test]
    fn the_app_directories_hang_off_the_roots() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let data = app_data_dir(&roots).unwrap();
        assert_eq!(data, current_layout(&roots).data);
        assert!(data.is_dir());
        let cache = app_cache_dir(&roots).unwrap();
        assert_eq!(cache, current_layout(&roots).cache);
        assert!(cache.is_dir());
        let logs = log_dir(&roots).unwrap();
        assert_eq!(logs, current_layout(&roots).logs);
        assert!(logs.is_dir());
        let config = app_config_dir(&roots).unwrap();
        assert_eq!(current_layout(&roots).config.as_ref(), Some(&config));
        assert!(config.is_dir());
    }

    /// An open app folder is tightened before a kind in it is made, and one
    /// already owner-only keeps its other bits.
    #[cfg(unix)]
    #[test]
    fn the_app_folder_is_made_owner_only_before_a_kind_in_it() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());
        let layout = layout(Platform::MacOs, &roots);
        let app = layout.app.clone().unwrap();
        let mode = || std::fs::metadata(&app).unwrap().permissions().mode() & 0o7777;
        std::fs::create_dir_all(&app).unwrap();
        std::fs::set_permissions(&app, std::fs::Permissions::from_mode(0o775)).unwrap();

        ensure_app_dir_above(&layout, &layout.config.clone().unwrap()).unwrap();
        assert_eq!(mode(), 0o700);

        std::fs::set_permissions(&app, std::fs::Permissions::from_mode(0o2700)).unwrap();
        ensure_app_dir_above(&layout, &layout.data).unwrap();
        assert_eq!(mode(), 0o2700);
    }

    /// A kind outside the app folder leaves it alone.
    #[test]
    fn a_kind_outside_the_app_folder_does_not_make_it() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());
        let layout = layout(Platform::MacOs, &roots);

        ensure_app_dir_above(&layout, &layout.cache).unwrap();

        assert!(!layout.app.unwrap().exists());
    }

    #[cfg(unix)]
    #[test]
    fn a_private_dir_is_made_where_none_was() {
        let scratch = crate::testutil::Scratch::new();
        let dir = scratch.path().join("run");
        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };

        ensure_private_dir(&dir, uid).unwrap();

        assert!(dir.is_dir());
    }

    /// A parent that is not there fails both ways of making the directory.
    #[cfg(unix)]
    #[test]
    fn a_private_dir_under_a_missing_parent_is_an_error() {
        let scratch = crate::testutil::Scratch::new();
        let dir = scratch.path().join("missing").join("run");
        // SAFETY: `geteuid` reads process state and cannot fail.
        let uid = unsafe { libc::geteuid() };

        assert!(ensure_private_dir(&dir, uid).is_err());
        assert!(fresh_private_dir(&dir).is_err());
    }
}
