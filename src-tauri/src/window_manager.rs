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

//! Window lifecycle. Every window is built in code by [`WindowManager`]
//! (`tauri.conf.json` declares none — a test pins this), so all chrome lives in
//! `build_window`; new windows cascade down-right of their anchor
//! ([`cascade_position`]). See
//! docs/adr/2026-08-09-per-platform-window-chrome.md.
//!
//! Windows are visible from creation, painted with the resolved scheme at t0
//! ([`background_color_for`]) — **no reveal step**; do not add one. On macOS
//! this depends on `tauri/macos-private-api` (clears `WKWebView`'s opaque white
//! backing), which in turn requires the *document* to stay opaque. See
//! docs/adr/2026-08-09-first-paint-theming.md.
//!
//! Closing the last window does not exit the process (see `lib.rs`), so every
//! entry point here recreates `"main"` on demand.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Mutex;

use tauri::webview::NewWindowResponse;
use tauri::window::{Color, Monitor};
use tauri::{AppHandle, Manager, Runtime, Url, WebviewUrl, WebviewWindow, WebviewWindowBuilder};
use tokio_util::sync::CancellationToken;
use tracing::warn;

use crate::error::Result;
use crate::host_file::{self, ColorSchemePreference};

/// Label of the primary window (created at startup by `lib.rs`'s `setup`).
const MAIN_LABEL: &str = "main";

/// Title shown on every window the host creates.
const WINDOW_TITLE: &str = "kstack";

/// Logical size every window opens at.
const WINDOW_WIDTH: f64 = 800.0;
const WINDOW_HEIGHT: f64 = 600.0;

// Traffic-light geometry: used only on macOS, but compiled and unit-tested on
// every platform so CI covers the arithmetic (hence `allow(dead_code)`).

/// App-bar height, the strip the macOS traffic lights sit in. Kept in sync with
/// `src/components/widgets/app-bar.tsx`.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const APP_BAR_HEIGHT: f64 = 48.0;

/// Inset from the window's left edge to the first traffic light. The webview's
/// `TRAFFIC_LIGHT_GUTTER` (`app-bar.tsx`) reserves this plus the buttons — bump
/// this and check the bar's first control still clears them.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const TRAFFIC_LIGHT_LEFT_INSET: f64 = 20.0;

/// macOS traffic-light button diameter (logical px).
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const TRAFFIC_LIGHT_DIAMETER: f64 = 12.0;

/// Height of the native title bar; the buttons sit centered in it.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const NATIVE_TITLE_BAR_HEIGHT: f64 = 28.0;

/// The buttons' inset inside the native title bar.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const TRAFFIC_LIGHT_NATIVE_INSET: f64 = (NATIVE_TITLE_BAR_HEIGHT - TRAFFIC_LIGHT_DIAMETER) / 2.0;

/// Logical `(x, y)` for the macOS traffic lights: `x` insets the first button from
/// the window's left edge, `y` centers the row in the app bar.
///
/// `y` is not the gap that shows above the buttons. The runtime grows the native
/// title bar to `button + y` and anchors it to the window's top, but only moves the
/// buttons horizontally (tao's `inset_traffic_lights`), so they keep their native
/// inset from that bar's bottom. What shows above them is `y` less that inset, and
/// centering adds it back.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
fn traffic_light_position(bar_height: f64) -> (f64, f64) {
    let gap_above = (bar_height - TRAFFIC_LIGHT_DIAMETER) / 2.0;
    (
        TRAFFIC_LIGHT_LEFT_INSET,
        gap_above + TRAFFIC_LIGHT_NATIVE_INSET,
    )
}

/// `@kubetail/ui`'s `--background` token per scheme, tracked by eye — nothing
/// pins them, but the webview paints the real token over this on first frame.
#[cfg_attr(target_os = "linux", allow(dead_code))]
const LIGHT_BACKGROUND: Color = Color(255, 255, 255, 255);
#[cfg_attr(target_os = "linux", allow(dead_code))]
const DARK_BACKGROUND: Color = Color(9, 9, 11, 255);

/// Native window background for a persisted preference — the anti-launch-flash
/// color on screen until the webview's first paint (and during live resize).
///
/// Must always return a concrete color: wry only clears the webview's opaque
/// white backing when one is set, so `system` resolves against
/// `os_prefers_dark` rather than opting out. See
/// docs/adr/2026-08-09-first-paint-theming.md. Called only on opaque platforms,
/// but compiled/tested everywhere.
#[cfg_attr(target_os = "linux", allow(dead_code))]
fn background_color_for(preference: Option<ColorSchemePreference>, os_prefers_dark: bool) -> Color {
    let dark = match preference {
        Some(ColorSchemePreference::Light) => false,
        Some(ColorSchemePreference::Dark) => true,
        // `system` — and a file that predates the setting — follow the OS.
        Some(ColorSchemePreference::System) | None => os_prefers_dark,
    };
    if dark {
        DARK_BACKGROUND
    } else {
        LIGHT_BACKGROUND
    }
}

/// Whether the webview may navigate to `url`. The app is a local single-page
/// bundle: every legitimate navigation is client-side routing (`pushState`,
/// which never reaches this), so a document navigation is a page trying to
/// leave — and a window that has left still holds every host command.
///
/// An allowlist that ends in `false`, never a denylist of bad schemes: a scheme
/// nobody thought of is refused rather than admitted.
fn is_app_origin(url: &Url) -> bool {
    // Handed to the callback by some engines during startup.
    if url.as_str() == "about:blank" {
        return true;
    }

    // `pnpm tauri dev` serves the webview off localhost. Behind `cfg` so a
    // release build cannot admit it, whatever port the dev server picked.
    if cfg!(debug_assertions) && (url.scheme(), url.host_str()) == ("http", Some("localhost")) {
        return true;
    }

    // A port would make it a different origin, and the bundle is served on none.
    // `Url::origin` is no help: a non-special scheme like `tauri` is opaque, so
    // every custom-protocol URL serializes to the same "null".
    if url.port().is_some() {
        return false;
    }

    // The bundle's own origin, per platform: macOS/Linux serve it over the
    // `tauri` custom protocol, Windows over a virtual host. Both are admitted
    // everywhere — a build only ever loads its own.
    matches!(
        (url.scheme(), url.host_str()),
        ("tauri", Some("localhost")) | ("http", Some("tauri.localhost"))
    )
}

/// Answers a page's `window.open`: always no. Nothing in the app opens a
/// window this way, and a popup is a webview the navigation policy does not
/// govern. An external link stays host-owned — the opener plugin, as the
/// tray's account item does.
fn refuse_new_window<R: Runtime>(url: &Url) -> NewWindowResponse<R> {
    warn!(%url, "refused a new-window request from the webview");
    NewWindowResponse::Deny
}

/// Logs a window call that failed. None of them is worth failing the caller.
fn warn_on_error<T>(result: tauri::Result<T>, what: &str) {
    if let Err(err) = result {
        warn!(%err, "{what}");
    }
}

/// Down-right step per cascade — about a title bar, so the window beneath
/// stays grabbable.
const CASCADE_STEP: f64 = 28.0;

/// Logical `(x, y)` for a new window: one [`CASCADE_STEP`] down-right of
/// `anchor` within `work_area` (`(x, y, w, h)` logical px, monitor minus
/// taskbar/dock); no anchor → centered.
///
/// The step is taken only if the result fits entirely inside the work area,
/// else the cascade restarts from center. That single fit check bounds the walk
/// (no step cap) and absorbs a pathological anchor (dragged off-screen or onto
/// another monitor). A window larger than the work area pins to its top-left.
fn cascade_position(
    anchor: Option<(f64, f64)>,
    window: (f64, f64),
    work_area: (f64, f64, f64, f64),
) -> (f64, f64) {
    let (win_w, win_h) = window;
    let (area_x, area_y, area_w, area_h) = work_area;

    // Center, but never above/left of the work area's origin — an oversized
    // window would otherwise be centered into negative coordinates, hiding its
    // title bar (and with it the traffic lights / custom window controls).
    let centered = (
        area_x + ((area_w - win_w) / 2.0).max(0.0),
        area_y + ((area_h - win_h) / 2.0).max(0.0),
    );

    let Some((anchor_x, anchor_y)) = anchor else {
        return centered;
    };

    let (x, y) = (anchor_x + CASCADE_STEP, anchor_y + CASCADE_STEP);
    let fits =
        x >= area_x && y >= area_y && x + win_w <= area_x + area_w && y + win_h <= area_y + area_h;

    if fits {
        (x, y)
    } else {
        centered
    }
}

/// Unminimizes, shows and focuses `window`.
fn surface<R: Runtime>(window: &WebviewWindow<R>) -> Result<()> {
    // A minimized window won't surface on show()/set_focus() alone.
    let minimized = window.is_minimized().unwrap_or(false);
    minimized.then(|| window.unminimize()).transpose()?;
    window.show()?;
    window.set_focus()?;
    Ok(())
}

/// A monitor's work area as logical `(x, y, width, height)` — Tauri only
/// exposes it in physical pixels.
fn logical_work_area(monitor: &Monitor) -> (f64, f64, f64, f64) {
    let scale = monitor.scale_factor();
    let area = monitor.work_area();
    let position = area.position.to_logical::<f64>(scale);
    let size = area.size.to_logical::<f64>(scale);
    (position.x, position.y, size.width, size.height)
}

/// The monitors a new window is placed against. `app.rs` reads the real
/// runtime's; Tauri's mock runtime has none, so tests pass their own.
pub trait Monitors: Send + Sync {
    /// The monitor the window labelled `label` is on.
    fn of_window(&self, label: &str) -> Option<Monitor>;

    /// The system's primary monitor.
    fn primary(&self) -> Option<Monitor>;
}

/// Owns window creation and focus for the host.
pub struct WindowManager {
    /// Monotonic counter feeding unique window labels.
    next_id: AtomicU64,

    /// Most recently built window — the cascade's fallback anchor when no
    /// window is focused (tray/Dock menus). A label, not a `WebviewWindow`, so
    /// a closed window drops normally and is simply not found next lookup.
    last_built: Mutex<Option<String>>,

    /// The app-wide shutdown signal, cancelled once a quit begins. Every path
    /// that opens or shows a window ends in this type's two methods, which open
    /// nothing after it and hide what they presented as it began: a window
    /// would come back over a sidecar that is stopping and vanish when the exit
    /// lands.
    shutdown: CancellationToken,

    monitors: Box<dyn Monitors>,
}

impl WindowManager {
    pub fn new(shutdown: CancellationToken, monitors: impl Monitors + 'static) -> Self {
        Self {
            next_id: AtomicU64::new(0),
            last_built: Mutex::new(None),
            shutdown,
            monitors: Box::new(monitors),
        }
    }

    /// Hides every window, as a quit begins.
    pub fn hide_all<R: Runtime>(&self, app: &AppHandle<R>) {
        for window in app.webview_windows().values() {
            warn_on_error(window.hide(), "failed to hide a window on quit");
        }
    }

    /// Create a fresh window with a unique label (`window-1`, `window-2`, …) —
    /// never reuses an existing one. Opens nothing once a quit has begun.
    pub fn new_window<R: Runtime>(&self, app: &AppHandle<R>) -> Result<()> {
        if self.quitting() {
            return Ok(());
        }
        let label = self.next_label();
        let window = self.build_window(app, &label)?;
        self.hide_if_quitting(app, window);
        Ok(())
    }

    /// Whether a quit has begun, logged when it has: the ask that reached here
    /// came from a click or a second launch, and it is answered with nothing.
    fn quitting(&self) -> bool {
        let quitting = self.shutdown.is_cancelled();
        if quitting {
            warn!("a window was asked for while quitting; it opens nothing");
        }
        quitting
    }

    /// Hides `window` if a quit has begun, for a presentation that passed the
    /// entry check just before it. From another thread, a window call is a
    /// message to the event loop, so this check queues behind the presentation:
    /// either the quit came first and this hides the window, or the window was
    /// up when `hide_all` ran.
    fn hide_if_quitting<R: Runtime>(&self, app: &AppHandle<R>, window: WebviewWindow<R>) {
        let shutdown = self.shutdown.clone();
        let queued = app.run_on_main_thread(move || {
            if shutdown.is_cancelled() {
                let hidden = window.hide();
                warn_on_error(hidden, "failed to hide a window opened as the quit began");
            }
        });
        warn_on_error(queued, "failed to queue the quit check for a window");
    }

    /// Next unique window label; never reused for the process lifetime, even
    /// across concurrent callers.
    fn next_label(&self) -> String {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed) + 1;
        format!("window-{id}")
    }

    /// The cascade anchor: the focused window, else the last one built (the
    /// tray/Dock menus can be clicked while another app holds focus). `None`
    /// only when no windows are open.
    fn anchor_window<R: Runtime>(&self, app: &AppHandle<R>) -> Option<WebviewWindow<R>> {
        let focused = app
            .webview_windows()
            .into_values()
            .find(|window| window.is_focused().unwrap_or(false));

        focused.or_else(|| {
            let last_built = self.last_built.lock().unwrap_or_else(|p| p.into_inner());
            last_built
                .as_deref()
                .and_then(|label| app.get_webview_window(label))
        })
    }

    /// Logical `(x, y)` for the next window: a cascade step off the anchor on
    /// its monitor, else centered on the primary. `None` when no monitor can be
    /// read.
    fn placement<R: Runtime>(&self, app: &AppHandle<R>) -> Option<(f64, f64)> {
        // Anchor and its monitor resolve together — an anchor whose monitor
        // can't be read is no anchor at all. Anything unreadable degrades to a
        // centered window, never a failed build.
        let anchored = self.anchor_window(app).and_then(|window| {
            let monitor = self.monitors.of_window(window.label())?;
            let position = window
                .outer_position()
                .ok()?
                .to_logical::<f64>(monitor.scale_factor());
            Some((monitor, (position.x, position.y)))
        });

        let (monitor, anchor_position) = match anchored {
            Some((monitor, position)) => (monitor, Some(position)),
            // The session's first window, or an anchor that vanished mid-build.
            None => (self.monitors.primary()?, None),
        };

        Some(cascade_position(
            anchor_position,
            (WINDOW_WIDTH, WINDOW_HEIGHT),
            logical_work_area(&monitor),
        ))
    }

    /// Surface the main window: recreate it if the user closed it, unminimize it
    /// if minimized, then show and focus it. Opens nothing once a quit has begun.
    pub fn show_main_window<R: Runtime>(&self, app: &AppHandle<R>) -> Result<()> {
        if self.quitting() {
            return Ok(());
        }
        let window = match app.get_webview_window(MAIN_LABEL) {
            Some(window) => window,
            None => self.build_window(app, MAIN_LABEL)?,
        };
        // Queued whatever surface returns: a step may fail after show() went out.
        let surfaced = surface(&window);
        self.hide_if_quitting(app, window);
        surfaced
    }

    /// Build a webview window. Per-platform chrome: macOS keeps decorations
    /// (Overlay title bar, hidden title, repositioned traffic lights —
    /// `traffic_light_position` requires decorations on); Linux/Windows are
    /// frameless, Linux additionally transparent. See
    /// docs/adr/2026-08-09-per-platform-window-chrome.md.
    ///
    /// Positioned explicitly on every platform: AppKit auto-cascades an
    /// unpositioned window, Linux/Windows pile up.
    ///
    /// Every window it builds refuses to navigate off the app origin
    /// ([`is_app_origin`]) and refuses `window.open` outright.
    fn build_window<R: Runtime>(
        &self,
        app: &AppHandle<R>,
        label: &str,
    ) -> Result<WebviewWindow<R>> {
        let mut builder = WebviewWindowBuilder::new(app, label, WebviewUrl::default())
            .title(WINDOW_TITLE)
            .inner_size(WINDOW_WIDTH, WINDOW_HEIGHT)
            // Floor the window so the layout never renders below its narrowest design.
            .min_inner_size(600.0, 400.0)
            .on_navigation(is_app_origin)
            .on_new_window(|url, _features| refuse_new_window(&url));

        // No monitor to measure against — let the OS place the window.
        if let Some((x, y)) = self.placement(app) {
            builder = builder.position(x, y);
        }

        #[cfg(target_os = "macos")]
        {
            let (x, y) = traffic_light_position(APP_BAR_HEIGHT);
            builder = builder
                .title_bar_style(tauri::TitleBarStyle::Overlay)
                .hidden_title(true)
                .traffic_light_position(tauri::LogicalPosition::new(x, y));
        }

        // Non-macOS is frameless — the webview draws its own `WindowControls`.
        #[cfg(not(target_os = "macos"))]
        {
            builder = builder.decorations(false);
        }

        // Linux goes transparent so the webview's `WindowFrame` paints its own
        // border + shadow; Windows stays opaque (DWM already draws a borderless
        // window's shadow — a custom one would double-stack).
        #[cfg(target_os = "linux")]
        {
            builder = builder.transparent(true);
        }

        // One `host.json` read feeds both pre-paint paths below. A failed
        // config-dir lookup degrades to defaults, never a failed build.
        let host_file = host_file::path(app)
            .map(|path| host_file::read(&path))
            .unwrap_or_default();

        // 1. Expose the file as `window.__KSTACK_HOST__` before any page
        //    script runs.
        builder = builder.initialization_script(host_file::init_script(&host_file));

        // 2. Opaque platforms: paint the native surface with the resolved
        //    scheme. Must be set unconditionally — it also clears the webview's
        //    white backing (see module docs). Linux is transparent: background
        //    must stay unset.
        #[cfg(not(target_os = "linux"))]
        {
            let color = background_color_for(
                host_file.color_scheme_preference,
                crate::os_theme::prefers_dark(),
            );
            builder = builder.background_color(color);
        }

        let window = builder.build()?;

        // Anchor the next cascade step (for paths with no focused window).
        *self.last_built.lock().unwrap_or_else(|p| p.into_inner()) = Some(label.to_string());

        // `debug-prod` compiles the inspector into a release build for
        // examining production behavior; never enabled in shipped builds — see
        // `Cargo.toml`.
        #[cfg(feature = "debug-prod")]
        window.open_devtools();

        Ok(window)
    }
}

#[cfg(test)]
mod tests {
    use super::{
        background_color_for, cascade_position, is_app_origin, refuse_new_window, warn_on_error,
        WindowManager,
    };
    use crate::host_file::ColorSchemePreference;
    use crate::services::sidecar::SidecarService;
    use crate::testutil::{mock_app, monitor, window, OneMonitor, Scratch};
    use std::collections::HashSet;
    use std::sync::Arc;
    use std::thread;
    use tauri::test::MockRuntime;
    use tauri::webview::NewWindowResponse;
    use tauri::Manager;
    use tauri::Url;
    use tokio_util::sync::CancellationToken;

    #[test]
    fn the_bundled_app_origins_are_admitted() {
        // What the window actually loads: `tauri://localhost` on macOS/Linux,
        // `http://tauri.localhost` on Windows. `about:blank` is handed to the
        // callback by some engines during startup.
        for url in [
            "tauri://localhost",
            "tauri://localhost/dashboard",
            "http://tauri.localhost",
            "http://tauri.localhost/dashboard",
            "about:blank",
        ] {
            let url = Url::parse(url).expect("test URL parses");
            assert!(is_app_origin(&url), "{url} must be admitted");
        }
    }

    #[test]
    fn everything_that_is_not_the_app_is_refused() {
        // The reason the policy exists — a remote origin — plus the schemes a
        // script reaches for to carry data out or run code in, and a host that
        // merely looks like the app's. The allowlist ends in `false`, so an
        // exotic scheme nobody listed here is refused with them.
        for url in [
            "https://example.com",
            "http://example.com",
            "https://tauri.localhost.example.com",
            "http://tauri.localhost:8080",
            "https://tauri.localhost",
            "file:///etc/passwd",
            "data:text/html,<h1>x</h1>",
            "javascript:alert(1)",
            "about:srcdoc",
            "kstack://localhost",
        ] {
            let url = Url::parse(url).expect("test URL parses");
            assert!(!is_app_origin(&url), "{url} must be refused");
        }
    }

    #[test]
    fn the_dev_server_is_admitted_only_by_a_debug_build() {
        // `pnpm tauri dev` serves the webview from localhost, so a debug build
        // must admit it — and a release build must not, which is what keeps the
        // shipped app's allowlist to the bundle alone. One assertion, so
        // whichever mode the suite is built in pins its own half.
        for url in ["http://localhost:1420", "http://localhost:1420/dashboard"] {
            let url = Url::parse(url).expect("test URL parses");
            assert_eq!(is_app_origin(&url), cfg!(debug_assertions), "{url}");
        }
    }

    #[test]
    fn build_window_installs_the_navigation_policy() {
        // Both callbacks are the containment for a page that tries to leave, and
        // nothing else in the process reinstates them — an edit to the builder
        // that drops one is silent at runtime, and the mock runtime never
        // navigates or opens a popup to call them. So read the source, the way
        // `config_declares_no_windows` reads the config.
        let source = include_str!("window_manager.rs");
        let (_, after) = source
            .split_once("fn build_window")
            .expect("window_manager defines build_window");
        // Stop at the test module, whose own assertions quote the strings below.
        let build_window = after
            .split_once("#[cfg(test)]")
            .expect("window_manager has a test module")
            .0;

        assert!(
            build_window.contains(".on_navigation(is_app_origin)"),
            "build_window must refuse navigation off the app origin"
        );
        assert!(
            build_window.contains("refuse_new_window"),
            "build_window must refuse `window.open`"
        );
    }

    #[test]
    fn a_new_window_request_is_refused() {
        let url = Url::parse("https://example.com").unwrap();
        assert!(matches!(
            refuse_new_window::<MockRuntime>(&url),
            NewWindowResponse::Deny
        ));
    }

    #[test]
    fn a_failed_window_call_is_logged() {
        let logged = crate::logging::captured(|| {
            warn_on_error(Ok(()), "not this");
            warn_on_error(Err::<(), _>(tauri::Error::UnknownPath), "failed to hide");
        });
        assert!(logged.contains("failed to hide"), "got: {logged}");
        assert!(!logged.contains("not this"), "got: {logged}");
    }

    #[test]
    fn explicit_preference_ignores_the_os_scheme() {
        // An explicit choice wins over the OS in both directions — that's what
        // makes the app's scheme, not the OS's, the one on screen at t0.
        for os_prefers_dark in [false, true] {
            assert_eq!(
                background_color_for(Some(ColorSchemePreference::Light), os_prefers_dark),
                super::LIGHT_BACKGROUND
            );
            assert_eq!(
                background_color_for(Some(ColorSchemePreference::Dark), os_prefers_dark),
                super::DARK_BACKGROUND
            );
        }
    }

    #[test]
    fn system_preference_follows_the_os_scheme() {
        // `system`, and a file predating the setting, defer to the OS. Never
        // `None`: wry only clears the webview's white backing when a color is
        // set, so opting out here would reinstate the launch flash.
        for preference in [Some(ColorSchemePreference::System), None] {
            assert_eq!(
                background_color_for(preference, true),
                super::DARK_BACKGROUND
            );
            assert_eq!(
                background_color_for(preference, false),
                super::LIGHT_BACKGROUND
            );
        }
    }

    /// A manager on no readable monitor.
    fn manager() -> WindowManager {
        WindowManager::new(CancellationToken::new(), OneMonitor(None))
    }

    #[test]
    fn labels_start_at_one() {
        let wm = manager();
        assert_eq!(wm.next_label(), "window-1");
    }

    /// The first window centers on the primary monitor; the next steps off it,
    /// on its monitor, in logical px.
    #[test]
    fn windows_open_centered_then_cascade() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        // A 1920x1080 logical work area at scale 2.
        let wm = WindowManager::new(
            CancellationToken::new(),
            OneMonitor(Some(monitor((0, 0, 3840, 2160), 2.0))),
        );

        assert_eq!(wm.placement(app.handle()), Some((560.0, 240.0)));
        wm.new_window(app.handle()).unwrap();
        assert!(app.get_webview_window("window-1").is_some());

        // The mock puts every window at the origin.
        assert_eq!(wm.placement(app.handle()), Some((28.0, 28.0)));
        wm.new_window(app.handle()).unwrap();
        assert!(app.get_webview_window("window-2").is_some());
    }

    /// With no monitor to measure against, the OS places the window.
    #[test]
    fn a_window_opens_with_no_monitor_readable() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let wm = manager();

        assert_eq!(wm.placement(app.handle()), None);
        wm.new_window(app.handle()).unwrap();
        assert!(app.get_webview_window("window-1").is_some());
    }

    #[test]
    fn a_closed_main_window_is_rebuilt() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));

        manager().show_main_window(app.handle()).unwrap();

        assert!(app.get_webview_window("main").is_some());
    }

    #[test]
    fn an_open_main_window_is_surfaced_not_rebuilt() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        window(&app, "main");

        manager().show_main_window(app.handle()).unwrap();

        assert_eq!(app.webview_windows().len(), 1);
    }

    /// A quit hides windows and closes none.
    #[test]
    fn hiding_for_a_quit_closes_no_window() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");
        let shutdown = CancellationToken::new();
        let wm = WindowManager::new(shutdown.clone(), OneMonitor(None));
        shutdown.cancel();

        wm.hide_if_quitting(app.handle(), main);
        wm.hide_all(app.handle());

        assert_eq!(app.webview_windows().len(), 1);
    }

    /// With no window focused (a tray click), the cascade anchors on the last
    /// window built, while it is still open.
    #[test]
    fn the_anchor_is_the_last_window_built_while_it_is_open() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let wm = manager();
        window(&app, "window-1");
        assert!(wm.anchor_window(app.handle()).is_none(), "none built yet");

        *wm.last_built.lock().unwrap() = Some("window-1".into());
        let anchor = wm.anchor_window(app.handle()).unwrap();
        assert_eq!(anchor.label(), "window-1");

        *wm.last_built.lock().unwrap() = Some("window-2".into());
        assert!(wm.anchor_window(app.handle()).is_none(), "closed");
    }

    #[test]
    fn traffic_lights_center_vertically_in_the_app_bar() {
        let (x, y) = super::traffic_light_position(super::APP_BAR_HEIGHT);
        assert_eq!(x, super::TRAFFIC_LIGHT_LEFT_INSET);

        // Assert what shows on screen, not the formula: the gap above the buttons
        // is `y` less their native inset, and it must match the gap below.
        let above = y - super::TRAFFIC_LIGHT_NATIVE_INSET;
        let below = super::APP_BAR_HEIGHT - above - super::TRAFFIC_LIGHT_DIAMETER;
        assert_eq!(above, below);
    }

    #[test]
    fn config_declares_no_windows() {
        // Every window is created in code (`build_window`) so the chrome — and
        // the pre-paint background/init-script it will carry — is defined in
        // exactly one place. A window declared in the config would bypass all
        // of it, so pin the config to declaring none.
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).expect("valid config JSON");
        assert_eq!(
            config["app"]["windows"].as_array().map(Vec::len),
            Some(0),
            "windows must be created via build_window, not declared in tauri.conf.json"
        );
    }

    #[test]
    fn production_csp_admits_no_remote_code() {
        // The CSP is the containment for a compromised page: the host forwards
        // whatever operation the webview sends, so script execution there carries
        // the whole cluster surface. See
        // docs/adr/2026-09-03-no-graphql-operation-allowlist.md. It is pinned here
        // because `build_window` creates every webview the policy governs.
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).expect("valid config JSON");
        let csp = &config["app"]["security"]["csp"];

        assert_eq!(
            csp["script-src"].as_str(),
            Some("'self'"),
            "script-src must admit the bundle alone — no remote origin, no 'unsafe-inline', no 'unsafe-eval'"
        );
        assert_eq!(csp["default-src"].as_str(), Some("'self'"));
        for directive in ["object-src", "base-uri", "frame-src", "form-action"] {
            assert_eq!(
                csp[directive].as_str(),
                Some("'none'"),
                "{directive} must admit nothing"
            );
        }

        // Every directive, so a source added to one nobody thought to name above is
        // still caught. Tauri's asset and IPC hosts are the only origins with a
        // host part; everything else is a keyword or a scheme (`data:`, `ipc:`).
        for (directive, value) in csp.as_object().expect("csp is a directive map") {
            let value = value
                .as_str()
                .unwrap_or_else(|| panic!("{directive} must be a string"));
            assert!(
                !value.contains("unsafe-eval"),
                "{directive} admits 'unsafe-eval'"
            );
            for source in value.split_whitespace() {
                assert!(
                    !source.contains("//")
                        || source == "http://asset.localhost"
                        || source == "http://ipc.localhost",
                    "{directive} admits the remote origin {source}"
                );
            }
        }
    }

    #[test]
    fn default_capability_grants_only_window_chrome() {
        // A capability is authority handed to a page that already holds the whole
        // cluster surface, so it is granted when a consumer needs it and not before.
        // Nothing judges that but a reader, so pin the list: a new permission has to
        // be added here too, where the diff is about authority rather than a feature.
        // Pinned beside the CSP because `build_window` creates the windows this
        // capability names.
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/default.json"))
                .expect("valid capability JSON");
        let permissions: Vec<&str> = capability["permissions"]
            .as_array()
            .expect("permissions is a list")
            .iter()
            .map(|p| p.as_str().expect("permission is a string"))
            .collect();
        assert_eq!(
            permissions,
            [
                "core:default",
                "core:window:allow-start-dragging",
                "core:window:allow-start-resize-dragging",
                "core:window:allow-minimize",
                "core:window:allow-toggle-maximize",
                "core:window:allow-close",
            ],
            "the webview holds window chrome and nothing else — add a permission here only with the consumer that needs it"
        );
    }

    #[test]
    fn labels_increase_monotonically() {
        let wm = manager();
        assert_eq!(wm.next_label(), "window-1");
        assert_eq!(wm.next_label(), "window-2");
        assert_eq!(wm.next_label(), "window-3");
    }

    #[test]
    fn each_manager_has_its_own_counter() {
        let a = manager();
        let b = manager();
        assert_eq!(a.next_label(), "window-1");
        assert_eq!(a.next_label(), "window-2");
        // `b` is independent — it is not advanced by calls on `a`.
        assert_eq!(b.next_label(), "window-1");
    }

    #[test]
    fn concurrent_labels_are_unique() {
        const THREADS: usize = 8;
        const PER_THREAD: usize = 250;

        let wm = Arc::new(manager());
        let handles: Vec<_> = (0..THREADS)
            .map(|_| {
                let wm = Arc::clone(&wm);
                thread::spawn(move || (0..PER_THREAD).map(|_| wm.next_label()).collect::<Vec<_>>())
            })
            .collect();

        let mut seen = HashSet::new();
        for handle in handles {
            for label in handle.join().expect("worker thread panicked") {
                assert!(
                    seen.insert(label.clone()),
                    "label {label} was handed out twice"
                );
            }
        }
        assert_eq!(seen.len(), THREADS * PER_THREAD);
    }

    // A roomy 1920x1080 work area at the origin, and the size every window
    // opens at — the common case the cascade is tuned for.
    const WINDOW: (f64, f64) = (super::WINDOW_WIDTH, super::WINDOW_HEIGHT);
    const ROOMY: (f64, f64, f64, f64) = (0.0, 0.0, 1920.0, 1080.0);
    const STEP: f64 = super::CASCADE_STEP;

    #[test]
    fn the_first_window_of_the_session_is_centered() {
        // Nothing to cascade off, so the app opens where the user expects
        // rather than pre-offset toward one corner.
        assert_eq!(cascade_position(None, WINDOW, ROOMY), (560.0, 240.0));
    }

    #[test]
    fn a_window_opens_one_step_down_right_of_its_anchor() {
        // The whole point: cascade off wherever the anchor actually is — including
        // somewhere the user dragged it, nowhere near the centered position — and
        // not off a position derived from how many windows have been opened.
        for anchor in [
            (100.0, 80.0),
            (12.0, 400.0),
            cascade_position(None, WINDOW, ROOMY),
        ] {
            assert_eq!(
                cascade_position(Some(anchor), WINDOW, ROOMY),
                (anchor.0 + STEP, anchor.1 + STEP)
            );
        }
    }

    #[test]
    fn the_cascade_restarts_from_the_center_when_the_next_step_would_not_fit() {
        // An anchor flush against the work area's bottom-right corner: stepping
        // again would hang the window off-screen, so wrap to the center.
        let centered = cascade_position(None, WINDOW, ROOMY);
        let corner = (1920.0 - WINDOW.0, 1080.0 - WINDOW.1);
        assert_eq!(cascade_position(Some(corner), WINDOW, ROOMY), centered);
    }

    #[test]
    fn an_anchor_dragged_off_screen_cannot_drag_the_new_window_with_it() {
        // A window mostly off the right edge, or on a monitor we are not
        // measuring against: the step does not fit, so the new window lands
        // centered and visible instead of following it into the void.
        let centered = cascade_position(None, WINDOW, ROOMY);
        for anchor in [(1900.0, 40.0), (-780.0, 40.0), (3000.0, 3000.0)] {
            assert_eq!(cascade_position(Some(anchor), WINDOW, ROOMY), centered);
        }
    }

    #[test]
    fn a_chain_of_cascades_never_leaves_the_work_area() {
        // The invariant the function exists for, exercised the way the app uses
        // it: each window anchors on the one before it, for many windows, on
        // several monitor shapes — including one too short to step even once.
        for area in [
            ROOMY,
            (0.0, 0.0, 1024.0, 700.0),
            (100.0, 50.0, 1280.0, 800.0),
            // 640px tall leaves 20px below the centered window — less than one
            // step, so this wraps on every single window.
            (0.0, 0.0, 1920.0, 640.0),
        ] {
            let (area_x, area_y, area_w, area_h) = area;
            let mut anchor = None;
            for window in 0..32 {
                let (x, y) = cascade_position(anchor, WINDOW, area);
                assert!(
                    x >= area_x && y >= area_y,
                    "window {window} at ({x}, {y}) starts before {area:?}"
                );
                assert!(
                    x + WINDOW.0 <= area_x + area_w && y + WINDOW.1 <= area_y + area_h,
                    "window {window} at ({x}, {y}) overflows {area:?}"
                );
                anchor = Some((x, y));
            }
        }
    }

    #[test]
    fn a_window_larger_than_the_work_area_pins_to_the_origin() {
        // Nowhere to cascade into, and centering would push the title bar (with
        // the traffic lights / window controls) off the top-left of the screen.
        let tiny = (0.0, 0.0, 640.0, 480.0);
        assert_eq!(cascade_position(None, WINDOW, tiny), (0.0, 0.0));
        assert_eq!(cascade_position(Some((0.0, 0.0)), WINDOW, tiny), (0.0, 0.0));
    }

    #[test]
    fn the_cascade_is_relative_to_the_work_areas_origin() {
        // A second monitor to the right of the primary, with a 30px menu bar:
        // positions are absolute in the virtual desktop, so the work area's
        // origin must carry through instead of being dropped.
        let secondary = (1920.0, 30.0, 1920.0, 1050.0);
        let centered = cascade_position(None, WINDOW, secondary);
        assert_eq!(centered, (2480.0, 255.0));
        assert_eq!(
            cascade_position(Some(centered), WINDOW, secondary),
            (2480.0 + STEP, 255.0 + STEP)
        );
    }
}
