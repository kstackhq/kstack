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

//! The app's wiring: what only the real app runs, on the real runtime, with a
//! real sidecar and a real exit. Every decision it makes is a function in
//! `lib.rs` or the module it belongs to, tested there; this file only connects
//! them, and coverage ignores it.

use tauri::webview::PageLoadEvent;
use tauri::{AppHandle, Manager, RunEvent, Runtime, WindowEvent};

use crate::services::sidecar::SidecarService;
use crate::state::AppState;
use crate::{
    app_menu, cancel_webview_subscriptions, cloud, exit_requested, exited, forget_webview_log,
    invoke_handler, logging, page_started, tray, wake,
};

/// Quits via [`AppHandle::exit`] so the `RunEvent::ExitRequested` teardown
/// runs — the same path as the tray and macOS-menu "Quit".
#[tauri::command]
pub fn quit<R: Runtime>(app: AppHandle<R>) {
    app.exit(0);
}

/// Opens the account page in the user's browser — the webview's avatar menu, the
/// same page the tray's account item opens.
///
/// Takes no URL. The webview holds the full cluster surface, so the destination
/// is the host's to name; a command that opened whatever it was handed would be
/// an exfiltration channel for anything executing in there.
#[tauri::command]
pub fn open_account_url<R: Runtime>(app: AppHandle<R>) -> crate::error::Result<()> {
    use tauri_plugin_opener::OpenerExt;
    app.opener().open_url(cloud::account_url(), None::<&str>)?;
    Ok(())
}

/// Routes Unix termination signals (`SIGTERM`/`SIGINT`/`SIGHUP`) through
/// [`AppHandle::exit`] — the same graceful path as menu/tray "Quit". Tao
/// installs no signal handlers, so without this a signal skips the `RunEvent`
/// shutdown hooks.
///
/// Unix-only by design: Windows session-end arrives as `WM_ENDSESSION`, which
/// tao already routes to `RunEvent::Exit`; macOS system shutdown is caught in
/// `applicationShouldTerminate:` (see `dock_menu`).
#[cfg(all(desktop, unix))]
fn spawn_signal_handler(app: &AppHandle) {
    use crate::{first_signal, termination_signals};

    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        // A Quit via another path cancels `shutdown`, ending this task.
        let shutdown = app.state::<AppState>().shutdown.clone();

        // A failed registration abandons signal handling entirely rather than
        // leaving a partial set wired up.
        let mut signals = match termination_signals() {
            Ok(signals) => signals,
            Err(err) => {
                tracing::error!(%err, "failed to install termination signal handlers");
                return;
            }
        };

        if let Some(received) = first_signal(&mut signals, &shutdown).await {
            tracing::info!(signal = received, "received termination signal; exiting");
            app.exit(0);
        }
    });
}

/// Builds, configures, and runs the Tauri application (the host's entry
/// point).
///
/// The event loop intercepts shutdown: closing the last window keeps the app
/// alive in the background; only an explicit Quit (or a Unix signal — see
/// [`spawn_signal_handler`]) exits and tears down the sidecar.
#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Lives here (not `main`) so it also covers the mobile entry point.
    let log_reload = logging::init_process();

    let mut builder = tauri::Builder::default();

    // Single-instance must be registered before all other plugins so a second
    // launch forwards into this process.
    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
            // Show/recreate the main window. Must be `spawn_blocking`, not the
            // main thread (WebView2 main-thread build deadlocks — see
            // `commands::new_window`) and not `spawn` (the build parks its
            // thread until the window exists).
            let app = app.clone();
            tauri::async_runtime::spawn_blocking(move || {
                if let Err(err) = app
                    .state::<AppState>()
                    .window_manager
                    .show_main_window(&app)
                {
                    tracing::error!(%err, "failed to show main window on second-instance launch")
                }
            });
        }));
    }

    let app = builder
        // `Started` fires as a navigation commits, so it covers the reload the
        // webview never tells us about.
        .on_page_load(|webview, payload| {
            if matches!(payload.event(), PageLoadEvent::Started) {
                page_started(webview.app_handle(), webview.label());
            }
        })
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(invoke_handler())
        .setup(move |app| {
            // First: everything below is what a user is asked for when the app
            // fails to start, and until this lands it reaches stderr alone.
            let log_guards = logging::install_file_layer(&log_reload, app.handle());

            let state = AppState::new(SidecarService::spawn(app.handle())?, log_guards);

            // Create the main window. All windows are built in code
            // (`tauri.conf.json` declares none); `build_window` owns chrome and
            // pre-paint theming — visible from creation, no reveal step. See
            // docs/adr/2026-08-09-first-paint-theming.md.
            state.window_manager.show_main_window(app.handle())?;

            app.manage(state);

            // Build menu and tray
            app_menu::build_app_menu(app.handle())?;
            tray::build_tray(app.handle())?;

            // Keep the tray's account section live off the sidecar's
            // AuthStateWatch stream.
            tray::spawn_authstate_subscription(app.handle());

            // Poke the sidecar on OS wake / network-return; see
            // docs/adr/2026-08-09-poke-resync-fanout.md.
            wake::spawn_wake_poke_supervisor(app.handle());

            // Custom macOS Dock menu (no Tauri API — see dock_menu module).
            #[cfg(target_os = "macos")]
            crate::dock_menu::install(app.handle());

            #[cfg(all(desktop, unix))]
            spawn_signal_handler(app.handle());

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while running tauri application");

    // Run with an event handler so we can intercept shutdown
    app.run(|app_handle, event| {
        match event {
            RunEvent::Ready => {
                // TODO: app is ready
            }
            #[cfg(target_os = "macos")]
            RunEvent::Reopen {
                has_visible_windows,
                ..
            } => {
                if !has_visible_windows {
                    let state = app_handle.state::<AppState>();
                    if let Err(err) = state.window_manager.show_main_window(app_handle) {
                        tracing::error!(%err, "failed to show main window on dock reopen");
                    }
                }
            }
            RunEvent::WindowEvent {
                label,
                event: WindowEvent::Destroyed,
                ..
            } => {
                cancel_webview_subscriptions(app_handle, &label);
                forget_webview_log(app_handle, &label);
            }
            RunEvent::ExitRequested { api, code, .. } => {
                let app = app_handle.clone();
                if exit_requested(app_handle, code, move |code| app.exit(code)) {
                    api.prevent_exit();
                }
            }
            RunEvent::Exit => exited(app_handle),
            _ => {}
        }
    });
}
