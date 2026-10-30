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

//! The wake → `Poke` driver over Tauri: runs the coalescer on the platform
//! sources' events. `app.rs` owns the channel and spawns the sources.

use std::time::Duration;

use tauri::{AppHandle, Manager, Runtime};
use tokio::sync::mpsc;

use super::core;
use crate::state::AppState;

/// Trailing-edge debounce: one poke per burst (wake + network-return collapse),
/// well under the sidecar's ~30s wall-clock backstop.
const DEBOUNCE_WINDOW: Duration = Duration::from_secs(3);

/// Source→coalescer channel depth. A saturated channel drops the excess, which
/// the coalescer would collapse anyway.
pub const CHANNEL_DEPTH: usize = 16;

/// Pokes the sidecar once per burst of `rx`'s events, for the app's lifetime;
/// ends when [`AppState::shutdown`] cancels or every source drops. Poke is
/// best-effort — failures logged and swallowed (the wall-clock detector covers
/// sleep).
pub async fn supervise<R: Runtime>(app: AppHandle<R>, rx: mpsc::Receiver<core::RawEvent>) {
    let shutdown = app.state::<AppState>().shutdown.clone();
    core::run_coalescer(rx, DEBOUNCE_WINDOW, shutdown, move || {
        let app = app.clone();
        async move {
            if let Err(err) = app.state::<AppState>().sidecar.poke().await {
                tracing::warn!(%err, "wake poke failed");
            }
        }
    })
    .await;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::logging::capture_thread;
    use crate::services::sidecar::SidecarService;
    use crate::testutil::{mock_app, Scratch};
    use crate::wake::core::RawEvent;

    /// One poke per burst, `DEBOUNCE_WINDOW` after its last event; a failed
    /// poke is logged and the supervisor carries on until its sources drop.
    #[tokio::test(start_paused = true)]
    async fn a_burst_pokes_once_after_the_window_and_a_failure_is_logged() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let (capture, _guard) = capture_thread();
        let (tx, rx) = mpsc::channel(CHANNEL_DEPTH);
        let supervisor = tokio::spawn(supervise(app.handle().clone(), rx));

        tx.send(RawEvent::Resumed).await.unwrap();
        tx.send(RawEvent::Resumed).await.unwrap();
        tokio::time::sleep(DEBOUNCE_WINDOW - Duration::from_millis(1)).await;
        assert!(!capture.text().contains("wake poke failed"));

        tokio::time::sleep(Duration::from_millis(2)).await;
        drop(tx);
        supervisor.await.unwrap();

        assert_eq!(capture.text().matches("wake poke failed").count(), 1);
    }

    #[tokio::test(start_paused = true)]
    async fn the_shutdown_signal_ends_the_supervisor() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let (_tx, rx) = mpsc::channel(CHANNEL_DEPTH);
        app.state::<AppState>().shutdown.cancel();

        supervise(app.handle().clone(), rx).await;
    }
}
