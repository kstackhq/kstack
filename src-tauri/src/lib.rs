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

//! The Tauri host crate: windows, tray/menus, the Go sidecar's lifecycle, and
//! the webview→sidecar GraphQL bridge over an AF_UNIX socket.
//!
//! [`run`] (`app.rs`, the wiring) is the single entry point — `main.rs` (and the
//! mobile entry point) call it. Module map: [`app_menu`], [`commands`],
//! `dock_menu` (macOS), [`error`], [`services`] (`SidecarService`), [`state`]
//! ([`AppState`]), [`quit`], [`tray`], [`window_manager`].

#![warn(clippy::unwrap_used)]
// `unwrap()` is idiomatic in tests.
#![cfg_attr(test, allow(clippy::unwrap_used))]

mod app;
mod app_menu;
mod cloud;
mod commands;
#[cfg(target_os = "macos")]
mod dock_menu;
mod error;
mod host_file;
mod logging;
mod os_theme;
mod paths;
mod quit;
mod services;
mod state;
mod tray;
mod wake;
mod webview_log;
mod window_manager;

#[cfg(test)]
mod testutil;

use std::time::Duration;

use tauri::ipc::Invoke;
use tauri::{AppHandle, Manager, Runtime};

use crate::quit::ExitDecision;
use crate::state::AppState;

pub use app::run;

/// Quit-path wait before `RunEvent::Exit` force-kills the sidecar: the
/// sidecar's `shutdownBudget` (20s, `sidecar/shutdown.go`) plus the time a
/// goroutine dump and its exit take, so its watchdog, not this kill, ends a
/// wedged stop.
const SIDECAR_SHUTDOWN_GRACE: Duration = Duration::from_secs(23);

#[cfg(all(desktop, unix))]
type TerminationSignals = [tokio::signal::unix::Signal; 3];

/// Takes over `SIGTERM`, `SIGINT` and `SIGHUP`: from here on they no longer
/// end the process, and [`first_signal`] reports them.
#[cfg(all(desktop, unix))]
fn termination_signals() -> std::io::Result<TerminationSignals> {
    use tokio::signal::unix::{signal, SignalKind};

    Ok([
        signal(SignalKind::terminate())?,
        signal(SignalKind::interrupt())?,
        signal(SignalKind::hangup())?,
    ])
}

/// The name of the first termination signal received, or `None` once
/// `shutdown` is cancelled.
#[cfg(all(desktop, unix))]
async fn first_signal(
    signals: &mut TerminationSignals,
    shutdown: &tokio_util::sync::CancellationToken,
) -> Option<&'static str> {
    let [term, int, hup] = signals;
    tokio::select! {
        // A quit already under way wins over a signal.
        biased;
        _ = shutdown.cancelled() => None,
        _ = term.recv() => Some("SIGTERM"),
        _ = int.recv() => Some("SIGINT"),
        _ = hup.recv() => Some("SIGHUP"),
    }
}

/// `ExitRequested`'s answer to `code`: whether to prevent the exit.
///
/// No code means the last window closed, and the app stays in the background.
/// A code is a Quit: the first starts the graceful shutdown, those during it
/// are prevented, and the one [`start_quit`] makes at its end goes through.
fn exit_requested<R: Runtime>(
    app: &AppHandle<R>,
    code: Option<i32>,
    exit: impl FnOnce(i32) + Send + 'static,
) -> bool {
    let Some(code) = code else {
        return true;
    };
    let state = app.state::<AppState>();
    match state.quit.request() {
        ExitDecision::Start => {
            tracing::info!(code, "exit requested; initiating graceful shutdown");
            // Cancel app-lifetime background tasks first so they don't retry
            // against a dying sidecar. Also stops any window from opening.
            state.shutdown.cancel();
            start_quit(app, code, exit);
            true
        }
        ExitDecision::Prevent => true,
        ExitDecision::Allow => false,
    }
}

/// Hides every window and waits for the sidecar on a thread of its own, then
/// calls `exit` with `code`. The wait can take the whole grace, and on the
/// event loop it would be a frozen app with its windows open.
fn start_quit<R: Runtime>(app: &AppHandle<R>, code: i32, exit: impl FnOnce(i32) + Send + 'static) {
    app.state::<AppState>().window_manager.hide_all(app);
    let app = app.clone();
    std::thread::spawn(move || {
        let state = app.state::<AppState>();
        if !state.sidecar.graceful_shutdown(SIDECAR_SHUTDOWN_GRACE) {
            tracing::warn!("sidecar did not exit within grace period");
        }
        state.quit.finish();
        exit(code);
    });
}

/// `RunEvent::Exit`: kills a sidecar the graceful path left running, then
/// flushes the log.
fn exited<R: Runtime>(app: &AppHandle<R>) {
    let state = app.state::<AppState>();
    if state.sidecar.kill() {
        tracing::warn!("sidecar was still running at exit; killed it");
    }

    tracing::info!("shutdown complete");

    // Last: the process exits without dropping managed state, so nothing after
    // this reaches the log file.
    state.flush_log();
}

/// Drops every subscription a webview opened. A reload replaces the page's JS
/// without running any teardown, and a closed window runs none either, so
/// without this the host keeps streaming SSE frames at a webview that can no
/// longer route them — leaking a sidecar connection and a cluster watch per
/// subscription, and (on reload) filling the console with the Tauri runtime's
/// "Couldn't find callback id N" for every frame.
fn cancel_webview_subscriptions<R: Runtime>(app: &AppHandle<R>, label: &str) {
    // `try_state`: the first page load can beat `setup`'s `manage`.
    if let Some(state) = app.try_state::<AppState>() {
        state.sidecar.cancel_webview(label);
    }
}

/// Forgets a destroyed webview's log pacing, reporting what it withheld.
fn forget_webview_log<R: Runtime>(app: &AppHandle<R>, label: &str) {
    if let Some(state) = app.try_state::<AppState>() {
        webview_log::destroyed(&state.webview_log, label);
    }
}

/// A navigation began in `label`'s webview: a load, or a reload, which runs no
/// JS teardown.
fn page_started<R: Runtime>(app: &AppHandle<R>, label: &str) {
    cancel_webview_subscriptions(app, label);
    // A reload keeps the label and replaces the JS context; the marker is how
    // two crashes across it read as two.
    webview_log::page_loaded(label);
}

/// Every command the webview may invoke.
fn invoke_handler<R: Runtime>() -> impl Fn(Invoke<R>) -> bool + Send + Sync + 'static {
    tauri::generate_handler![
        commands::ready,
        commands::graphql_query,
        commands::graphql_subscribe,
        commands::graphql_unsubscribe,
        commands::new_window,
        commands::update_host_file,
        app::quit,
        commands::log_webview,
        app::open_account_url,
    ]
}

#[cfg(test)]
mod tests {
    use std::sync::mpsc;

    use tauri::Manager;

    use super::*;
    use crate::logging::{captured, file_layer, FileLog};
    use crate::services::sidecar::SidecarService;
    use crate::testutil::{mock_app, Scratch};
    use crate::webview_log::{Limiter, LimiterConfig, WebviewRecord};

    #[test]
    fn closing_the_last_window_keeps_the_app_running() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));

        assert!(exit_requested(app.handle(), None, |_| panic!("no exit")));
        assert!(!app.state::<AppState>().shutdown.is_cancelled());
    }

    /// The first Quit is held while the sidecar stops; the exit it makes at the
    /// end carries its code and goes through.
    #[test]
    fn a_quit_stops_the_sidecar_then_exits_with_its_code() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let (tx, rx) = mpsc::channel();

        let prevented = exit_requested(app.handle(), Some(3), move |code| tx.send(code).unwrap());

        assert!(prevented);
        assert!(app.state::<AppState>().shutdown.is_cancelled());
        assert_eq!(rx.recv().unwrap(), 3);
        assert!(!exit_requested(app.handle(), Some(3), |_| panic!(
            "no exit"
        )));
    }

    #[test]
    fn a_quit_while_one_is_under_way_is_prevented() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let state = app.state::<AppState>();
        state.quit.request();

        assert!(exit_requested(app.handle(), Some(0), |_| panic!("no exit")));
        // Only the first Quit starts anything.
        assert!(!state.shutdown.is_cancelled());
    }

    #[test]
    fn the_exit_flushes_the_log() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let FileLog { guards, .. } = file_layer(scratch.path()).unwrap();
        let state = app.state::<AppState>();
        *state.log_guards.lock().unwrap() = guards;

        exited(app.handle());

        assert!(state.log_guards.lock().unwrap().is_empty());
    }

    /// A sidecar the graceful path left running is killed, and the kill is
    /// logged. The "sidecar" is a `sleep` this test starts, so the kill lands
    /// on nothing else.
    #[cfg(unix)]
    #[test]
    fn the_exit_kills_a_sidecar_still_running() {
        use std::os::unix::process::ExitStatusExt;

        let mut child = std::process::Command::new("sleep")
            .arg("60")
            .spawn()
            .unwrap();
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::running_as(child.id()));

        let out = captured(|| exited(app.handle()));

        assert!(
            out.contains("sidecar was still running at exit"),
            "got: {out}"
        );
        assert!(
            child.wait().unwrap().signal().is_some(),
            "killed by a signal"
        );
    }

    /// What a destroyed webview withheld is reported, and its pacing starts
    /// over if the label comes back.
    #[test]
    fn a_destroyed_webview_is_forgotten() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let state = app.state::<AppState>();
        *state.webview_log.lock().unwrap() = Limiter::new(LimiterConfig {
            burst: 1,
            error_reserve: 0,
            ..LimiterConfig::DEFAULT
        });
        let record = |message: &str| WebviewRecord {
            source: "graphql".into(),
            message: message.into(),
            stack: None,
            component_stack: None,
            context: None,
        };
        for message in ["one", "two"] {
            webview_log::record_from(&state.webview_log, "main", record(message), |_| true);
        }

        let out = captured(|| {
            cancel_webview_subscriptions(app.handle(), "main");
            forget_webview_log(app.handle(), "main");
        });

        assert!(out.contains("dropped=1"), "got: {out}");
        assert_eq!(state.webview_log.lock().unwrap().evict("main"), 0);
    }

    /// The first page load can beat `setup`, so it finds no state to touch.
    #[test]
    fn a_page_load_before_setup_is_still_marked() {
        let app = tauri::test::mock_app();

        let out = captured(|| page_started(app.handle(), "main"));
        forget_webview_log(app.handle(), "main");

        assert!(out.contains("window=\"main\""), "got: {out}");
    }

    /// Registering takes the signals over first, so the one sent here reaches
    /// the stream rather than ending the test process.
    #[cfg(all(desktop, unix))]
    #[tokio::test]
    async fn a_termination_signal_is_named() {
        let mut signals = termination_signals().unwrap();
        let shutdown = tokio_util::sync::CancellationToken::new();

        // SAFETY: `kill` on this process's own pid only delivers the signal.
        assert_eq!(unsafe { libc::kill(libc::getpid(), libc::SIGHUP) }, 0);

        assert_eq!(first_signal(&mut signals, &shutdown).await, Some("SIGHUP"));
    }

    #[cfg(all(desktop, unix))]
    #[tokio::test]
    async fn a_quit_under_way_wins_over_a_signal() {
        let mut signals = termination_signals().unwrap();
        let shutdown = tokio_util::sync::CancellationToken::new();
        shutdown.cancel();

        assert_eq!(first_signal(&mut signals, &shutdown).await, None);
    }
}
