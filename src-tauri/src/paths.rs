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
//! One place resolves the leaf ([`APP_DIR_NAME`]), because every directory
//! the app keeps needs it, and a second copy of the join is what silently drops
//! the `-dev` split.

use std::ffi::OsString;
use std::path::{Component, Path, PathBuf};

use tauri::{AppHandle, Manager, Runtime};

use crate::error::{AppError, Result};

/// The leaf each of Kstack's directories takes under its platform root. Debug
/// builds use a `-dev` sibling so a dev run never collides with an installed
/// release. Linux names folders in lowercase; macOS and Windows show the
/// product name.
const APP_DIR_NAME: &str = match (cfg!(target_os = "linux"), cfg!(debug_assertions)) {
    (true, true) => "kstack-dev",
    (true, false) => "kstack",
    (false, true) => "Kstack-dev",
    (false, false) => "Kstack",
};

/// The platform directories Kstack's own hang off, resolved once from the
/// app. A test builds one over a scratch directory: Tauri's directory override
/// does not reach these.
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
    let dir = data_dir_for(roots);
    ensure_data_dir(&dir)?;
    Ok(dir)
}

/// Where the data directory sits, whether or not it could be made.
fn data_dir_for(roots: &Roots) -> PathBuf {
    roots.local_data.join(APP_DIR_NAME)
}

/// The app's state directory, created owner-only: what one machine keeps
/// between runs and a user would not miss, the logs and the window frame.
/// Linux keeps it under XDG's state root; macOS and Windows have none, so
/// there it is the data directory.
pub fn app_state_dir(roots: &Roots) -> Result<PathBuf> {
    let dir = state_dir_for(roots);
    ensure_data_dir(&dir)?;
    Ok(dir)
}

/// Where the state directory sits, whether or not it could be made: the
/// sidecar fences it either way.
pub fn state_dir_for(roots: &Roots) -> PathBuf {
    if cfg!(target_os = "linux") {
        roots.state.join(APP_DIR_NAME)
    } else {
        data_dir_for(roots)
    }
}

/// The app's config directory, created owner-only: what a user sets, which
/// Windows roams with the profile. On macOS the config root is the data root,
/// so this is the data directory.
pub fn app_config_dir(roots: &Roots) -> Result<PathBuf> {
    let dir = config_dir_for(roots)
        .ok_or_else(|| AppError::Io(std::io::Error::other("resolve config_dir: none")))?;
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
    Some(roots.config.as_ref()?.join(APP_DIR_NAME))
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

/// Where the cache directory sits, given the two roots it can hang off.
///
/// The cache holds what Kstack rebuilds: the mirror, the kubectl cache, and a
/// sandboxed run's `TMPDIR`. macOS and Linux have a location for it
/// (`~/Library/Caches`, `$XDG_CACHE_HOME`) that backups skip. On Windows the
/// cache root is `%LOCALAPPDATA%`, the data directory's own parent, so there it
/// is a leaf on the data directory.
fn cache_dir_for(cache_base: &Path, data_dir: &Path) -> PathBuf {
    if cfg!(windows) {
        data_dir.join("cache")
    } else {
        cache_base.join(APP_DIR_NAME)
    }
}

/// Creates the cache directory owner-only. Split from [`app_cache_dir`] so the
/// mode is testable without an `AppHandle`.
fn ensure_cache_dir(cache_base: &Path, data_dir: &Path) -> Result<PathBuf> {
    let dir = cache_dir_for(cache_base, data_dir);
    ensure_data_dir(&dir)?;
    Ok(dir)
}

/// The app's cache directory, created owner-only. `data_dir` is
/// [`app_data_dir`]'s answer, which Windows hangs the cache off.
pub fn app_cache_dir(roots: &Roots, data_dir: &Path) -> Result<PathBuf> {
    ensure_cache_dir(&roots.cache, data_dir)
}

/// Where the log directory sits, given the two places it can hang off.
///
/// macOS has a real convention and we follow it: `~/Library/Logs`, which is
/// where a user is told to look and what Console.app lists. On Linux it is the
/// state directory itself, where XDG puts logs. Windows has no log location, so
/// there it is a leaf on the state directory, which is the data directory.
fn log_dir_for(home: &Path, state_dir: &Path) -> PathBuf {
    if cfg!(target_os = "macos") {
        home.join("Library/Logs").join(APP_DIR_NAME)
    } else if cfg!(target_os = "linux") {
        state_dir.to_path_buf()
    } else {
        state_dir.join("logs")
    }
}

/// Creates the log directory owner-only. Split from [`log_dir`] so the mode is
/// testable without an `AppHandle`.
fn ensure_log_dir(home: &Path, state_dir: &Path) -> Result<PathBuf> {
    let dir = log_dir_for(home, state_dir);
    ensure_data_dir(&dir)?;
    Ok(dir)
}

/// The app's log directory, created owner-only. `app.rs` asks once and hands
/// the answer to both the host's log and the sidecar's, so the sidecar fences
/// the directory the host writes in.
///
/// Not Tauri's `app_log_dir`: it names the leaf by bundle id rather than the
/// display name everything else here uses, and it has no debug variant, so a
/// dev run would write into an installed release's logs.
pub fn log_dir(roots: &Roots) -> Result<PathBuf> {
    ensure_log_dir(&roots.home, &app_state_dir(roots)?)
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
pub fn runtime_dir(_data_dir: &Path) -> Result<PathBuf> {
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
/// directory, and `TEMP` can point at one users share, so it is a leaf on the
/// data directory, which the profile ACL covers. The named pipe ignores it:
/// its namespace is flat, so the DACL and the peer check are its whole policy.
#[cfg(not(unix))]
pub fn runtime_dir(data_dir: &Path) -> Result<PathBuf> {
    let dir = runtime_dir_for(data_dir);
    ensure_data_dir(&dir)?;
    Ok(dir)
}

#[cfg(not(unix))]
fn runtime_dir_for(data_dir: &Path) -> PathBuf {
    data_dir.join("run")
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

/// Leaf for [`runtime_dir`]. A `-dev` sibling like [`APP_DIR_NAME`], so a dev
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

        let base = std::env::temp_dir().join(format!("kstack-logdir-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);

        let dir = ensure_log_dir(&base, &base).expect("create");
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );

        std::fs::remove_dir_all(&base).unwrap();
    }

    /// The cache holds what Kstack rebuilds, so it is created owner-only like
    /// the data it was built from. Unix only — Windows has no POSIX mode bits.
    #[cfg(unix)]
    #[test]
    fn cache_dir_is_created_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let base = std::env::temp_dir().join(format!("kstack-cachedir-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);

        let dir = ensure_cache_dir(&base, &base.join("data")).expect("create");
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );

        std::fs::remove_dir_all(&base).unwrap();
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

    /// macOS and Linux have a cache location of their own.
    #[cfg(not(windows))]
    #[test]
    fn cache_dir_is_a_leaf_on_the_platform_cache_dir() {
        let dir = cache_dir_for(Path::new("/home/x/.cache"), Path::new("/home/x/unused"));
        assert_eq!(dir, Path::new("/home/x/.cache").join(APP_DIR_NAME));
    }

    /// Windows' cache root is the data directory's own parent.
    #[cfg(windows)]
    #[test]
    fn cache_dir_is_a_leaf_on_the_data_dir() {
        let dir = cache_dir_for(
            Path::new(r"C:\Users\x\AppData\Local"),
            Path::new(r"C:\Users\x\AppData\Local\Kstack"),
        );
        assert_eq!(dir, Path::new(r"C:\Users\x\AppData\Local\Kstack\cache"));
    }

    /// macOS is the only platform with a standard log location.
    #[cfg(target_os = "macos")]
    #[test]
    fn log_dir_is_under_library_logs() {
        let dir = log_dir_for(Path::new("/Users/x"), Path::new("/Users/x/unused"));
        assert_eq!(dir, Path::new("/Users/x/Library/Logs").join(APP_DIR_NAME));
    }

    /// Windows has no log location of its own.
    #[cfg(windows)]
    #[test]
    fn log_dir_is_a_logs_leaf_on_the_data_dir() {
        let dir = log_dir_for(
            Path::new(r"C:\Users\x"),
            Path::new(r"C:\Users\x\AppData\Local\Kstack"),
        );
        assert_eq!(dir, Path::new(r"C:\Users\x\AppData\Local\Kstack\logs"));
    }

    /// XDG puts logs in the state directory.
    #[cfg(target_os = "linux")]
    #[test]
    fn linux_logs_in_the_state_dir() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        assert_eq!(log_dir(&roots).unwrap(), app_state_dir(&roots).unwrap());
    }

    /// The endpoint's directory is the wall around the socket. `/tmp` is shared
    /// and sticky-bit only, so anything that can enter the directory can reach
    /// the address the host is about to dial.
    #[cfg(unix)]
    #[test]
    fn runtime_dir_is_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let dir = runtime_dir(Path::new("/unused")).expect("runtime dir");
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

    /// Windows has no per-user runtime directory, so it hangs off the data
    /// directory.
    #[cfg(windows)]
    #[test]
    fn runtime_dir_is_a_leaf_on_the_data_dir() {
        let dir = runtime_dir_for(Path::new(r"C:\Users\x\AppData\Local\Kstack"));
        assert_eq!(dir, Path::new(r"C:\Users\x\AppData\Local\Kstack\run"));
    }

    #[cfg(windows)]
    #[test]
    fn runtime_dir_is_created_under_the_data_dir() {
        let scratch = crate::testutil::Scratch::new();

        let dir = runtime_dir(scratch.path()).expect("runtime dir");

        assert_eq!(dir, scratch.path().join("run"));
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

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_names_its_folders_in_lowercase() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        assert_eq!(APP_DIR_NAME.trim_end_matches("-dev"), "kstack");
        assert_eq!(APP_DIR_NAME.ends_with("-dev"), cfg!(debug_assertions));
        assert_eq!(
            app_data_dir(&roots).unwrap(),
            roots.local_data.join(APP_DIR_NAME)
        );
        assert_eq!(
            app_config_dir(&roots).unwrap(),
            roots.config.unwrap().join(APP_DIR_NAME)
        );
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

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_keeps_state_under_the_state_root_owner_only() {
        use std::os::unix::fs::PermissionsExt;

        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let dir = app_state_dir(&roots).unwrap();

        assert_eq!(dir, roots.state.join(APP_DIR_NAME));
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
    }

    /// macOS and Windows have no state root: state stays with the data.
    #[cfg(not(target_os = "linux"))]
    #[test]
    fn state_is_kept_in_the_data_dir() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        assert_eq!(
            app_state_dir(&roots).unwrap(),
            app_data_dir(&roots).unwrap()
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

    #[test]
    fn the_app_directories_hang_off_the_roots() {
        let scratch = crate::testutil::Scratch::new();
        let roots = crate::testutil::roots(scratch.path());

        let data = app_data_dir(&roots).unwrap();
        assert_eq!(data, roots.local_data.join(APP_DIR_NAME));
        assert!(data.is_dir());
        let cache = app_cache_dir(&roots, &data).unwrap();
        assert_eq!(cache, cache_dir_for(&roots.cache, &data));
        assert!(cache.is_dir());
        let logs = log_dir(&roots).unwrap();
        assert_eq!(
            logs,
            log_dir_for(&roots.home, &app_state_dir(&roots).unwrap())
        );
        assert!(logs.is_dir());
        let config = app_config_dir(&roots).unwrap();
        assert_eq!(config, roots.config.unwrap().join(APP_DIR_NAME));
        assert!(config.is_dir());
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
