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

//! The in-app updater, on macOS and Windows. The app trusts two keys: the
//! current one, `plugins.updater` in `tauri.conf.json`, and an offline standby,
//! `updater-standby.json`. The host checks each key's manifest,
//! `https://updates.kstack.sh/desktop/stable/latest-<key id>.json`, at launch
//! and once a day, and offers what it finds as a tray item; the user's click
//! downloads it, checks its signature against the key whose manifest named it,
//! installs it, stops the sidecar as a quit does and restarts, so the old
//! sidecar stops before the new one starts. The webview is granted nothing from
//! the updater plugin. Linux applies no update: its package manager owns the
//! install. → ADR
//! `docs/adr/2026-11-08-the-updater-trusts-a-current-and-a-standby-key.md`.
//!
//! This file is the part every platform compiles and tests; `platform.rs`
//! holds the plugin.

// Linux tests this file but runs no updater.
#![cfg_attr(target_os = "linux", allow(dead_code))]

#[cfg(any(target_os = "macos", windows))]
pub mod platform;

use std::fmt::Display;
use std::future::Future;
use std::sync::Arc;
use std::time::Duration;

use serde::Deserialize;
use tauri::{AppHandle, Manager, Runtime};
use tokio::sync::Notify;
use tokio_util::sync::CancellationToken;

use crate::state::AppState;

/// How long after a check that found nothing the next one runs.
pub const CHECK_INTERVAL: Duration = Duration::from_secs(24 * 60 * 60);

/// How long one key's check may take. A check that runs out fails like any
/// other, so the next key and the next day's check still run.
pub const CHECK_TIMEOUT: Duration = Duration::from_secs(30);

/// How long a download may go without receiving a byte before it is dropped.
pub const DOWNLOAD_STALL: Duration = Duration::from_secs(60);

/// A key the app trusts, and the manifest signed with it.
#[derive(Deserialize)]
pub struct Key {
    pub pubkey: String,
    pub endpoint: String,
}

/// The standby key: kept offline, it signs nothing until a rotation promotes
/// it to `plugins.updater`, and then the apps already installed trust it.
pub fn standby() -> Key {
    // Parsed by `the_updater_pins_its_keys_manifests_and_signed_version`.
    serde_json::from_str(include_str!("../../updater-standby.json")).expect("updater-standby.json")
}

/// Runs each key's check in order and returns the first update found. A failed
/// check does not hide a later key's update; when no key finds one, the first
/// failure is returned.
pub async fn check_each<U, E, Fut>(checks: impl IntoIterator<Item = Fut>) -> Result<Option<U>, E>
where
    Fut: Future<Output = Result<Option<U>, E>>,
{
    let mut failed = None;
    for check in checks {
        match check.await {
            Ok(Some(update)) => return Ok(Some(update)),
            Ok(None) => {}
            Err(err) => {
                failed.get_or_insert(err);
            }
        }
    }
    failed.map_or(Ok(None), Err)
}

/// Runs `check` now and then every `interval` until it finds an update, which
/// it returns, or until `shutdown`. A failed check is logged and waits out the
/// interval like an empty one: not knowing of an update is where the user
/// already was.
pub async fn find<U, E, Fut>(
    shutdown: CancellationToken,
    interval: Duration,
    mut check: impl FnMut() -> Fut,
) -> Option<U>
where
    E: Display,
    Fut: Future<Output = Result<Option<U>, E>>,
{
    loop {
        let checked = tokio::select! {
            biased;
            _ = shutdown.cancelled() => return None,
            checked = check() => checked,
        };
        match checked {
            Ok(Some(update)) => return Some(update),
            Ok(None) => tracing::debug!("no update"),
            Err(err) => tracing::warn!(%err, "update check failed"),
        }
        tokio::select! {
            biased;
            _ = shutdown.cancelled() => return None,
            _ = tokio::time::sleep(interval) => {}
        }
    }
}

/// Runs `download`, which calls `progress.notify_one()` on each chunk, and
/// drops it once `stall` passes with no chunk: `None`. The plugin sets no
/// timeout, and a slow download may rightly take long.
pub async fn unless_stalled<T, Fut>(
    stall: Duration,
    download: impl FnOnce(Arc<Notify>) -> Fut,
) -> Option<T>
where
    Fut: Future<Output = T>,
{
    let progress = Arc::new(Notify::new());
    let download = download(progress.clone());
    tokio::pin!(download);
    loop {
        tokio::select! {
            done = &mut download => return Some(done),
            // `notify_one` keeps a permit, so a chunk between polls still counts.
            _ = progress.notified() => {}
            _ = tokio::time::sleep(stall) => return None,
        }
    }
}

/// Sets the version the tray offers to update to, or withdraws the offer. The
/// caller rebuilds the menu.
pub fn offer<R: Runtime>(app: &AppHandle<R>, version: Option<String>) {
    let state = app.state::<AppState>();
    state.tray.lock().unwrap_or_else(|p| p.into_inner()).update = version;
}

#[cfg(test)]
mod tests {
    use std::future::ready;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use super::*;
    use crate::logging::capture_thread;
    use crate::services::sidecar::SidecarService;
    use crate::testutil::{mock_app, Scratch};

    const INTERVAL: Duration = Duration::from_secs(60);

    #[tokio::test(start_paused = true)]
    async fn a_check_that_finds_nothing_or_fails_is_tried_again_after_the_interval() {
        let calls = AtomicUsize::new(0);
        let started = tokio::time::Instant::now();
        let check = || {
            let call = calls.fetch_add(1, Ordering::SeqCst);
            async move {
                match call {
                    0 => Ok(None),
                    1 => Err("offline"),
                    _ => Ok(Some("1.2.3")),
                }
            }
        };

        let (capture, guard) = capture_thread();
        let found = find(CancellationToken::new(), INTERVAL, check).await;
        drop(guard);

        assert_eq!(found, Some("1.2.3"));
        let out = capture.text();
        assert!(out.contains("update check failed"), "got: {out}");
        assert!(out.contains("offline"), "got: {out}");
        assert_eq!(calls.load(Ordering::SeqCst), 3);
        assert_eq!(started.elapsed(), INTERVAL * 2);
    }

    #[tokio::test(start_paused = true)]
    async fn a_shutdown_ends_the_wait_between_checks() {
        let shutdown = CancellationToken::new();
        let stop = shutdown.clone();
        let check = move || {
            let stop = stop.clone();
            // The first check's answer arrives with the quit.
            async move {
                stop.cancel();
                Ok::<Option<()>, &str>(None)
            }
        };

        assert_eq!(find(shutdown, INTERVAL, check).await, None);
    }

    #[tokio::test]
    async fn a_shutdown_ends_a_check_in_flight() {
        let shutdown = CancellationToken::new();
        shutdown.cancel();
        let check = || std::future::pending::<Result<Option<()>, &str>>();

        assert_eq!(find(shutdown, INTERVAL, check).await, None);
    }

    #[tokio::test]
    async fn the_first_key_to_find_an_update_wins() {
        let checks = [ready(Ok::<_, &str>(None)), ready(Ok(Some("standby")))];
        assert_eq!(check_each(checks).await, Ok(Some("standby")));

        let checks = [
            ready(Ok::<_, &str>(Some("current"))),
            ready(Ok(Some("standby"))),
        ];
        assert_eq!(check_each(checks).await, Ok(Some("current")));
    }

    #[tokio::test]
    async fn a_failed_check_does_not_hide_another_keys_update() {
        let checks = [ready(Err("offline")), ready(Ok(Some("standby")))];
        assert_eq!(check_each(checks).await, Ok(Some("standby")));

        let checks = [
            ready(Err("offline")),
            ready(Ok::<Option<()>, _>(None)),
            ready(Err("refused")),
        ];
        assert_eq!(check_each(checks).await, Err("offline"));

        let checks = [ready(Ok::<Option<()>, &str>(None)), ready(Ok(None))];
        assert_eq!(check_each(checks).await, Ok(None));
    }

    /// A minisign key's id, as its comment prints it: bytes 2..10 of the key,
    /// little-endian, under the base64 Tauri wraps the key file in. The same
    /// derivation as `scripts/sign-updater.sh`'s `key_id`.
    fn key_id(pubkey: &str) -> String {
        use base64::prelude::*;

        let file = BASE64_STANDARD.decode(pubkey).unwrap();
        let key = std::str::from_utf8(&file).unwrap().lines().nth(1).unwrap();
        let key = BASE64_STANDARD.decode(key).unwrap();
        format!(
            "{:016X}",
            u64::from_le_bytes(key[2..10].try_into().unwrap())
        )
    }

    #[test]
    fn a_key_id_is_the_one_minisign_prints() {
        // A throwaway key from `cargo tauri signer generate`; its comment reads
        // "minisign public key: DEE18E44572B81F3".
        let pubkey = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IERFRTE4RTQ0NTcyQjgxRjMKUldUemdTdFhSSTdoM3RSTnBQWnN6QW43TGZ6MzNtcEkxZFlldEJ1KzJhR2x3VVpCTmc2bXRNTEcK";

        assert_eq!(key_id(pubkey), "DEE18E44572B81F3");
        assert_eq!(expected_id(pubkey), "DEE18E44572B81F3");
        assert_eq!(expected_id(""), "0000000000000000");
    }

    /// The id a key's manifest is named after; a placeholder until the key
    /// exists, which `scripts/sign-updater.sh` refuses to release.
    fn expected_id(pubkey: &str) -> String {
        if pubkey.is_empty() {
            "0000000000000000".to_owned()
        } else {
            key_id(pubkey)
        }
    }

    /// The id a manifest URL names: `latest-<id>.json` in the desktop's stable
    /// channel.
    fn endpoint_id(endpoint: &str) -> &str {
        endpoint
            .strip_prefix("https://updates.kstack.sh/desktop/stable/latest-")
            .and_then(|name| name.strip_suffix(".json"))
            .expect("updates.kstack.sh's latest-<key id>.json")
    }

    /// The app trusts what these files name: the current key and the standby,
    /// each with the manifest named after its id, and only a signature made
    /// for the manifest's version.
    #[test]
    fn the_updater_pins_its_keys_manifests_and_signed_version() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../../tauri.conf.json")).unwrap();
        let updater = &config["plugins"]["updater"];

        let endpoints = updater["endpoints"].as_array().unwrap();
        assert_eq!(endpoints.len(), 1);
        let current = Key {
            pubkey: updater["pubkey"].as_str().unwrap().to_owned(),
            endpoint: endpoints[0].as_str().unwrap().to_owned(),
        };
        let standby = standby();

        for (name, key) in [("current", &current), ("standby", &standby)] {
            assert_eq!(
                endpoint_id(&key.endpoint),
                expected_id(&key.pubkey),
                "the {name} endpoint names the {name} key"
            );
        }
        let placeholders = current.pubkey.is_empty() && standby.pubkey.is_empty();
        assert!(
            placeholders || expected_id(&current.pubkey) != expected_id(&standby.pubkey),
            "the standby is a second key"
        );

        assert_eq!(updater["requireSignedVersion"], true);
        assert!(updater.get("dangerousInsecureTransportProtocol").is_none());
        assert!(updater.get("dangerousAcceptInvalidCerts").is_none());
        assert!(updater.get("dangerousAcceptInvalidHostnames").is_none());
    }

    #[tokio::test(start_paused = true)]
    async fn a_download_that_keeps_receiving_is_never_dropped() {
        let download = |progress: Arc<Notify>| async move {
            for _ in 0..5 {
                tokio::time::sleep(DOWNLOAD_STALL / 2).await;
                progress.notify_one();
            }
            "bytes"
        };

        let started = tokio::time::Instant::now();
        assert_eq!(
            unless_stalled(DOWNLOAD_STALL, download).await,
            Some("bytes")
        );
        assert_eq!(started.elapsed(), DOWNLOAD_STALL * 5 / 2);
    }

    #[tokio::test(start_paused = true)]
    async fn a_download_that_stops_receiving_is_dropped() {
        let download = |progress: Arc<Notify>| async move {
            progress.notify_one();
            std::future::pending::<()>().await
        };

        let started = tokio::time::Instant::now();
        assert_eq!(unless_stalled(DOWNLOAD_STALL, download).await, None);
        assert_eq!(started.elapsed(), DOWNLOAD_STALL);
    }

    #[test]
    fn an_offer_reaches_the_tray_snapshot_and_withdraws() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let state = app.state::<AppState>();

        offer(app.handle(), Some("1.2.3".into()));
        assert_eq!(state.tray.lock().unwrap().update.as_deref(), Some("1.2.3"));

        offer(app.handle(), None);
        assert_eq!(state.tray.lock().unwrap().update, None);
    }
}
