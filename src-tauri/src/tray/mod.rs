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

//! System tray menu + the gRPC auth-state watch that keeps the account section
//! live. The pure account-section logic lives in [`account_menu`]; installing
//! the tray icon and starting the watch is wiring, in `app.rs`.

pub mod account_menu;

use std::future::Future;
use std::time::Duration;

use tauri::async_runtime::JoinHandle;
use tauri::menu::{IsMenuItem, Menu, MenuItem, PredefinedMenuItem, SubmenuBuilder};
use tauri::{AppHandle, Manager, Runtime};

use crate::state::AppState;
use account_menu::{
    account_snapshot_from, build_account_descriptor, AccountMenuDescriptor, ACCOUNT_LOGIN_ID,
    ACCOUNT_LOGOUT_ID, ACCOUNT_SETTINGS_ID,
};

/// Latest account-watch snapshot; `None` renders as SignedOut until the stream
/// delivers. Plain `std::sync::Mutex` — no `.await` is ever held under the
/// lock.
#[derive(Default)]
pub(crate) struct TraySnapshots {
    account: Option<account_menu::AccountSnapshot>,
}

/// Stable tray id so the watch supervisor can swap the menu via
/// [`Manager::tray_by_id`] without owning the handle.
pub const TRAY_ID: &str = "main";

/// Accounts are not part of the MVP, so the menu leaves the account section
/// out. The auth-state watch and the menu handlers stay; set this to `true` to
/// bring the section back.
pub const SHOW_ACCOUNT_SECTION: bool = false;

const NEW_WINDOW_ID: &str = "tray_new_window";
const SHOW_MAIN_ID: &str = "tray_show_main";
/// Handled in `app.rs`, with the account-settings item: both leave the app.
pub const QUIT_ID: &str = "tray_quit";

/// What a tray menu item does, for the items the app handles itself.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TrayAction {
    Login,
    Logout,
    NewWindow,
    ShowMain,
}

impl TrayAction {
    pub fn from_id(id: &str) -> Option<Self> {
        match id {
            ACCOUNT_LOGIN_ID => Some(Self::Login),
            ACCOUNT_LOGOUT_ID => Some(Self::Logout),
            NEW_WINDOW_ID => Some(Self::NewWindow),
            SHOW_MAIN_ID => Some(Self::ShowMain),
            _ => None,
        }
    }
}

/// Carries out `action` off the main thread the tray event fires on, and
/// returns the task it started.
pub fn run_action<R: Runtime>(app: &AppHandle<R>, action: TrayAction) -> JoinHandle<()> {
    let app = app.clone();
    // Auth actions are fire-and-forget RPCs; the result comes back through the
    // watch stream and rebuilds the menu. Window builds go to the blocking
    // pool — never the main thread (WebView2 deadlock) or an async worker (see
    // `commands::new_window`).
    match action {
        TrayAction::Login => tauri::async_runtime::spawn(async move {
            let started = app.state::<AppState>().sidecar.start_login().await;
            log_failure(started, "failed to start login from tray");
        }),
        TrayAction::Logout => tauri::async_runtime::spawn(async move {
            let ended = app.state::<AppState>().sidecar.logout().await;
            log_failure(ended, "failed to logout from tray");
        }),
        TrayAction::NewWindow => tauri::async_runtime::spawn_blocking(move || {
            let opened = app.state::<AppState>().window_manager.new_window(&app);
            log_failure(opened, "failed to open window from tray");
        }),
        TrayAction::ShowMain => tauri::async_runtime::spawn_blocking(move || {
            let shown = app
                .state::<AppState>()
                .window_manager
                .show_main_window(&app);
            log_failure(shown, "failed to show main window");
        }),
    }
}

/// Logs a menu action's failure: a menu callback has no caller to return it to.
fn log_failure(result: crate::error::Result<()>, what: &str) {
    if let Err(err) = result {
        tracing::error!(%err, "{what}");
    }
}

/// Builds the full tray menu — the account section when `account` is given,
/// then the static items. Used for the initial build and every watch-triggered
/// rebuild.
pub fn build_tray_menu<R: Runtime>(
    app: &AppHandle<R>,
    account: Option<AccountMenuDescriptor>,
) -> tauri::Result<Menu<R>> {
    let new_window = MenuItem::with_id(app, NEW_WINDOW_ID, "New Window", true, None::<&str>)?;
    let show_main = MenuItem::with_id(app, SHOW_MAIN_ID, "Show Main Window", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, QUIT_ID, "Quit", true, None::<&str>)?;

    let separator = PredefinedMenuItem::separator(app)?;
    let tail: [&dyn IsMenuItem<R>; 4] = [&new_window, &show_main, &separator, &quit];
    let Some(account) = account else {
        return Menu::with_items(app, &tail);
    };

    // Account section: "Sign in / Sign up" when signed out, or a submenu
    // titled with the user's name containing "Account Settings" + "Sign out".
    let acct_menu_item: Box<dyn IsMenuItem<R>> = match account {
        AccountMenuDescriptor::SignedOut => {
            let item = MenuItem::with_id(
                app,
                ACCOUNT_LOGIN_ID,
                "Sign in / Sign up",
                true,
                None::<&str>,
            )?;
            Box::new(item)
        }
        AccountMenuDescriptor::SignedIn { title } => {
            let settings = MenuItem::with_id(
                app,
                ACCOUNT_SETTINGS_ID,
                "Account Settings",
                true,
                None::<&str>,
            )?;
            let sign_out =
                MenuItem::with_id(app, ACCOUNT_LOGOUT_ID, "Sign out", true, None::<&str>)?;
            let submenu = SubmenuBuilder::new(app, title)
                .item(&settings)
                .item(&sign_out)
                .build()?;
            Box::new(submenu)
        }
    };
    let account_separator = PredefinedMenuItem::separator(app)?;

    let mut items: Vec<&dyn IsMenuItem<R>> = vec![acct_menu_item.as_ref(), &account_separator];
    items.extend(tail);
    Menu::with_items(app, &items)
}

/// Rebuilds the tray menu from the snapshot in [`AppState::tray`]. muda
/// requires menu mutation on the main thread, hence
/// [`AppHandle::run_on_main_thread`].
fn rebuild_tray_menu<R: Runtime>(app: &AppHandle<R>) {
    // Snapshot under the lock, release before the main-thread dispatch.
    let account_snap = {
        let state = app.state::<AppState>();
        let guard = state.tray.lock().unwrap_or_else(|p| p.into_inner());
        guard
            .account
            .clone()
            .unwrap_or_else(account_menu::AccountSnapshot::signed_out)
    };
    let account = SHOW_ACCOUNT_SECTION.then(|| build_account_descriptor(&account_snap));

    let menu_app = app.clone();
    if let Err(err) = app.run_on_main_thread(move || {
        let Some(tray) = menu_app.tray_by_id(TRAY_ID) else {
            return;
        };
        match build_tray_menu(&menu_app, account) {
            Ok(menu) => {
                if let Err(err) = tray.set_menu(Some(menu)) {
                    tracing::error!(%err, "failed to set tray menu");
                }
            }
            Err(err) => tracing::error!(%err, "failed to build tray menu"),
        }
    }) {
        tracing::error!(%err, "failed to schedule tray menu rebuild");
    }
}

/// Supervises the gRPC auth-state watch for the app's lifetime, until
/// [`AppState::shutdown`] is cancelled. First frame is a full snapshot; failed
/// opens retry with capped backoff; a stream that ends after delivering data
/// reconnects promptly, one that ends without data backs off (no busy-loop).
pub fn watch_auth_state<R: Runtime>(app: AppHandle<R>) -> impl Future<Output = ()> {
    /// First retry delay; doubles each failure up to `MAX_BACKOFF`.
    const BASE_BACKOFF: Duration = Duration::from_millis(500);
    const MAX_BACKOFF: Duration = Duration::from_secs(10);

    async move {
        let shutdown = app.state::<AppState>().shutdown.clone();
        let mut backoff = BASE_BACKOFF;
        loop {
            let state = app.state::<AppState>();
            let open = tokio::select! {
                biased;
                _ = shutdown.cancelled() => return,
                open = state.sidecar.watch_auth_state() => open,
            };

            let delay = match open {
                Ok(mut stream) => {
                    let mut saw_snapshot = false;
                    loop {
                        let msg = tokio::select! {
                            biased;
                            _ = shutdown.cancelled() => return,
                            msg = stream.message() => msg,
                        };
                        match msg {
                            Ok(Some(auth_state)) => {
                                saw_snapshot = true;
                                {
                                    let app_state = app.state::<AppState>();
                                    app_state
                                        .tray
                                        .lock()
                                        .unwrap_or_else(|p| p.into_inner())
                                        .account = Some(account_snapshot_from(&auth_state));
                                }
                                rebuild_tray_menu(&app);
                            }
                            Ok(None) => break,
                            Err(err) => {
                                tracing::warn!(%err, "auth-state tray watch stream error");
                                break;
                            }
                        }
                    }
                    if saw_snapshot {
                        backoff = BASE_BACKOFF;
                        tracing::info!("auth-state tray watch ended; reconnecting");
                        BASE_BACKOFF
                    } else {
                        tracing::warn!(
                            ?backoff,
                            "auth-state watch ended before any snapshot; backing off"
                        );
                        let delay = backoff;
                        backoff = (backoff * 2).min(MAX_BACKOFF);
                        delay
                    }
                }
                Err(err) => {
                    tracing::warn!(%err, ?backoff, "auth-state tray watch failed; retrying");
                    let delay = backoff;
                    backoff = (backoff * 2).min(MAX_BACKOFF);
                    delay
                }
            };

            tokio::select! {
                biased;
                _ = shutdown.cancelled() => return,
                _ = tokio::time::sleep(delay) => {}
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::logging::{capture_thread, captured};
    use crate::services::sidecar::SidecarService;
    use crate::testutil::{mock_app, window, Scratch};

    #[test]
    fn each_menu_id_the_app_handles_names_its_action() {
        for (id, action) in [
            (ACCOUNT_LOGIN_ID, TrayAction::Login),
            (ACCOUNT_LOGOUT_ID, TrayAction::Logout),
            (NEW_WINDOW_ID, TrayAction::NewWindow),
            (SHOW_MAIN_ID, TrayAction::ShowMain),
        ] {
            assert_eq!(TrayAction::from_id(id), Some(action), "{id}");
        }
        // `app.rs` handles these two before asking.
        assert_eq!(TrayAction::from_id(ACCOUNT_SETTINGS_ID), None);
        assert_eq!(TrayAction::from_id(QUIT_ID), None);
    }

    #[test]
    fn a_failed_action_is_logged_and_a_done_one_is_not() {
        let failed = std::io::Error::other("no sidecar");
        let out = captured(|| {
            log_failure(Err(failed.into()), "failed to start login from tray");
            log_failure(Ok(()), "never written");
        });

        assert!(
            out.contains("failed to start login from tray"),
            "got: {out}"
        );
        assert!(out.contains("no sidecar"), "got: {out}");
        assert!(!out.contains("never written"), "got: {out}");
    }

    /// Opening the watch fails at once with no sidecar, so each attempt waits
    /// out the next backoff; the shutdown ends the task in the middle of one.
    #[tokio::test(start_paused = true)]
    async fn the_auth_state_watch_backs_off_until_shutdown() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let shutdown = app.state::<AppState>().shutdown.clone();
        let (capture, _guard) = capture_thread();

        let watch = tokio::spawn(watch_auth_state(app.handle().clone()));
        // Paused time jumps through the backoffs: 0.5 + 1 + 2 + 4 seconds, then
        // into the fifth wait.
        tokio::time::sleep(Duration::from_secs(8)).await;
        shutdown.cancel();
        watch.await.unwrap();

        let out = capture.text();
        assert_eq!(
            out.matches("auth-state tray watch failed").count(),
            5,
            "{out}"
        );
        assert!(out.contains("backoff=8s"), "the fifth waits 8s: {out}");
    }

    #[test]
    fn a_rebuild_with_no_tray_installed_changes_nothing() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));

        rebuild_tray_menu(app.handle());

        assert!(app.tray_by_id(TRAY_ID).is_none());
    }

    #[test]
    fn a_login_or_logout_with_no_sidecar_fails_quietly() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));

        for action in [TrayAction::Login, TrayAction::Logout] {
            tauri::async_runtime::block_on(run_action(app.handle(), action)).unwrap();
        }
    }

    #[test]
    fn show_main_surfaces_the_open_main_window() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        window(&app, "main");

        tauri::async_runtime::block_on(run_action(app.handle(), TrayAction::ShowMain)).unwrap();

        assert_eq!(app.webview_windows().len(), 1);
    }

    #[test]
    fn new_window_opens_nothing_once_a_quit_has_begun() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        app.state::<AppState>().shutdown.cancel();

        tauri::async_runtime::block_on(run_action(app.handle(), TrayAction::NewWindow)).unwrap();

        assert!(app.webview_windows().is_empty());
    }

    // AppKit builds menus on the main thread alone, which no test runs on.
    #[cfg(not(target_os = "macos"))]
    mod menu {
        use tauri::menu::MenuItemKind;
        use tauri::test::MockRuntime;

        use super::*;

        /// The ids of `items`, separators left out.
        fn ids(items: &[MenuItemKind<MockRuntime>]) -> Vec<String> {
            items
                .iter()
                .filter(|item| item.as_predefined_menuitem().is_none())
                .map(|item| item.id().0.clone())
                .collect()
        }

        #[test]
        fn without_the_account_section_the_menu_is_the_static_items() {
            let scratch = Scratch::new();
            let app = mock_app(scratch.path(), SidecarService::without_process(None));

            let menu = build_tray_menu(app.handle(), None).unwrap();

            let items = menu.items().unwrap();
            assert_eq!(ids(&items), [NEW_WINDOW_ID, SHOW_MAIN_ID, QUIT_ID]);
        }

        #[test]
        fn signed_out_the_section_offers_sign_in() {
            let scratch = Scratch::new();
            let app = mock_app(scratch.path(), SidecarService::without_process(None));

            let menu = build_tray_menu(app.handle(), Some(AccountMenuDescriptor::SignedOut));

            let items = menu.unwrap().items().unwrap();
            let expected = [ACCOUNT_LOGIN_ID, NEW_WINDOW_ID, SHOW_MAIN_ID, QUIT_ID];
            assert_eq!(ids(&items), expected);
        }

        #[test]
        fn signed_in_the_section_is_a_submenu_titled_by_the_user() {
            let scratch = Scratch::new();
            let app = mock_app(scratch.path(), SidecarService::without_process(None));
            let account = AccountMenuDescriptor::SignedIn {
                title: "Ada".into(),
            };

            let menu = build_tray_menu(app.handle(), Some(account)).unwrap();

            let items = menu.items().unwrap();
            let submenu = items[0].as_submenu().unwrap();
            assert_eq!(submenu.text().unwrap(), "Ada");
            let inner = submenu.items().unwrap();
            assert_eq!(ids(&inner), [ACCOUNT_SETTINGS_ID, ACCOUNT_LOGOUT_ID]);
        }
    }
}
