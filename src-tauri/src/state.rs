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

use std::sync::{Arc, Mutex};

use tokio_util::sync::CancellationToken;
use tracing_appender::non_blocking::WorkerGuard;

use crate::quit::Quit;
use crate::services::sidecar::SidecarService;
use crate::tray::TraySnapshots;
use crate::webview_log::Limiter;
use crate::window_manager::WindowManager;

pub struct AppState {
    pub sidecar: SidecarService,
    pub window_manager: WindowManager,
    /// Latest-state holder for the tray's account watch stream, written by
    /// `spawn_authstate_subscription`.
    pub tray: Arc<Mutex<TraySnapshots>>,
    /// App-wide graceful-shutdown signal, cancelled once on Quit *before* the
    /// sidecar is torn down. Every app-lifetime background task (tray/wake
    /// supervisors, signal handler) must hold a clone and select on
    /// [`CancellationToken::cancelled`] so it doesn't retry against a dying
    /// sidecar.
    pub shutdown: CancellationToken,
    /// Where a quit is: `ExitRequested` starts the wait once and prevents every
    /// request during it.
    pub quit: Quit,
    /// The log files' worker-thread guards, taken by [`AppState::flush_log`].
    /// Empty when no log file could be opened.
    pub log_guards: Mutex<Vec<WorkerGuard>>,
    /// Per-webview pacing of `log_webview`, keyed by label; entries are
    /// evicted on `WindowEvent::Destroyed`.
    pub webview_log: Mutex<Limiter>,
}

impl AppState {
    /// Flushes the log files' worker threads, and must be called on the way out.
    ///
    /// Tauri's `App::run` ends the process with `std::process::exit`, so no
    /// managed state is ever dropped — a guard's own `Drop` never runs, and
    /// records still queued (the last of them the shutdown line itself) go with
    /// the process. Idempotent: the guards are taken, so a second call is a
    /// no-op.
    pub fn flush_log(&self) {
        drop(std::mem::take(
            &mut *self
                .log_guards
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner()),
        ));
    }
}
