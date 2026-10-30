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

use std::time::Instant;

use tauri::ipc::Channel;
use tauri::{AppHandle, Emitter, Manager, Runtime, State, Webview};

use crate::error::Result;
use crate::host_file::{self, HostFile, HostFilePatch};
use crate::services::sidecar::GraphqlResponse;
use crate::state::AppState;
use crate::webview_log::{self, WebviewRecord};

/// Probes the sidecar's IPC listener; the frontend's startup gate. `Err`
/// (frontend `networkError`) once the connect budget elapses.
#[tauri::command]
pub async fn ready(state: State<'_, AppState>) -> Result<()> {
    state.sidecar.ready().await
}

/// Opens a new application window (webview `AppMenu` "New Window" / `Ctrl+N`
/// on Linux/Windows; macOS uses its native menu, `app_menu.rs`).
///
/// Must stay `spawn_blocking`, never `spawn` or the main thread: `build()`
/// parks its thread until the webview exists — a main-thread build deadlocks
/// Windows (WebView2's controller is created by the main-thread event loop),
/// and parking an async worker would starve [`graphql_query`].
#[tauri::command]
pub async fn new_window<R: Runtime>(app: AppHandle<R>) -> Result<()> {
    tauri::async_runtime::spawn_blocking(move || {
        app.state::<AppState>().window_manager.new_window(&app)?;
        Ok(())
    })
    .await
    .map_err(|err| std::io::Error::other(format!("window build task failed: {err}")))?
}

/// Applies a partial update to `host.json` (see `host_file`); adding a setting
/// extends `HostFilePatch`, not the command list. Broadcasts the merged file to
/// every window as `host_file::UPDATED_EVENT` and returns it. See
/// docs/adr/2026-08-09-host-json-settings.md.
#[tauri::command]
pub fn update_host_file<R: Runtime>(app: AppHandle<R>, patch: HostFilePatch) -> Result<HostFile> {
    let file = host_file::update(&host_file::path(&app)?, patch)?;
    app.emit(host_file::UPDATED_EVENT, &file)?;
    Ok(file)
}

/// Writes one webview error record to the host's log (`src/lib/error-log.ts`).
/// Thin: the label is read off the `Webview` — never the payload, since the
/// label is the webview's identity — and `webview_log` does the rest.
///
/// `async` so a hot error path runs on the runtime, not the main thread.
#[tauri::command]
pub async fn log_webview<R: Runtime>(
    state: State<'_, AppState>,
    webview: Webview<R>,
    record: WebviewRecord,
) -> Result<()> {
    // Each window holds one webview under the same label, so the window is
    // what resolves it.
    let now = Instant::now();
    webview_log::record_from(&state.webview_log, webview.label(), record, now, |label| {
        webview.get_webview_window(label).is_some()
    });
    Ok(())
}

/// Forwards a GraphQL query/mutation to the sidecar over UDS HTTP. Returns
/// body + real HTTP status; only a transport failure is `Err` (frontend
/// `networkError`/retry). Shape matches `src/lib/graphql/invoke-fetch.ts`.
#[tauri::command]
pub async fn graphql_query(state: State<'_, AppState>, body: String) -> Result<GraphqlResponse> {
    state.sidecar.query(body).await
}

/// Registers a GraphQL subscription (one SSE connection to the sidecar).
/// Returns the op id for [`graphql_unsubscribe`]. Shape matches
/// `src/lib/graphql/subscribe-exchange.ts`.
///
/// Tagged with the calling webview's label so the host can tear the
/// subscription down when that page goes away — a reload or a window close
/// runs no JS teardown (see `lib.rs`).
#[tauri::command]
pub async fn graphql_subscribe<R: Runtime>(
    state: State<'_, AppState>,
    webview: Webview<R>,
    query: String,
    variables: serde_json::Value,
    channel: Channel<String>,
) -> Result<u64> {
    state
        .sidecar
        .subscribe(query, variables, channel, webview.label().to_string())
        .await
}

/// Cancels a subscription. Tolerant of unknown ids — the frontend can race
/// teardown with the subscribe resolve (subscribe-exchange.ts).
#[tauri::command]
pub async fn graphql_unsubscribe(state: State<'_, AppState>, id: u64) -> Result<()> {
    state.sidecar.unsubscribe(id).await;
    Ok(())
}

#[cfg(test)]
mod tests {
    use serde_json::json;
    use tauri::{Listener, Manager};

    use crate::host_file;
    use crate::services::sidecar::SidecarService;
    use crate::state::AppState;
    use crate::testutil::{invoke, mock_app, window, Scratch};
    use crate::webview_log::{Limiter, LimiterConfig};

    #[test]
    fn ready_answers_once_the_sidecar_is_ready() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");

        assert_eq!(invoke(&main, "ready", json!({})), Ok(json!(null)));
    }

    #[test]
    fn ready_fails_with_the_sidecars_own_reason() {
        let scratch = Scratch::new();
        let sidecar = SidecarService::without_process(Some("database is locked"));
        let app = mock_app(scratch.path(), sidecar);
        let main = window(&app, "main");

        let err = invoke(&main, "ready", json!({})).unwrap_err();
        assert!(
            err.as_str().unwrap().contains("database is locked"),
            "got {err}"
        );
    }

    /// A transport failure is the webview's `networkError`: an `Err` carrying
    /// the reason, never a response.
    #[test]
    fn a_query_with_no_sidecar_fails_as_a_transport_error() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");

        let err = invoke(&main, "graphql_query", json!({ "body": "{}" })).unwrap_err();
        assert!(
            err.as_str().unwrap().contains("no sidecar is running"),
            "got {err}"
        );
    }

    #[test]
    fn a_call_missing_an_argument_is_refused_by_name() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");

        let err = invoke(&main, "graphql_query", json!({})).unwrap_err();
        assert!(err.as_str().unwrap().contains("body"), "got {err}");
    }

    /// Every command refuses an argument it cannot read, naming it.
    #[test]
    fn a_malformed_argument_is_refused_by_name() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");

        for (cmd, args, name) in [
            ("log_webview", json!({ "record": 1 }), "record"),
            (
                "graphql_subscribe",
                json!({ "query": "", "variables": {}, "channel": 1 }),
                "channel",
            ),
            ("graphql_unsubscribe", json!({ "id": "x" }), "id"),
        ] {
            let err = invoke(&main, cmd, args).unwrap_err();
            assert!(err.as_str().unwrap().contains(name), "{cmd}: got {err}");
        }
    }

    /// The commands read their state from the app; before `setup` manages it
    /// there is none to read.
    #[test]
    fn a_command_before_the_state_is_managed_is_refused() {
        let scratch = Scratch::new();
        let app = crate::testutil::bare_app(scratch.path());
        let main = window(&app, "main");

        let err = invoke(&main, "ready", json!({})).unwrap_err();
        assert!(err.as_str().unwrap().contains("state"), "got {err}");
    }

    /// A subscription answers with its id at once; a failed dial reaches the
    /// channel, not the call. Unsubscribing tolerates any id.
    #[test]
    fn subscribe_returns_fresh_ids_and_unsubscribe_tolerates_any() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");
        let subscribe = || {
            let args = json!({
                "query": "subscription { x }",
                "variables": {},
                "channel": "__CHANNEL__:7",
            });
            invoke(&main, "graphql_subscribe", args).unwrap()
        };

        let first = subscribe();
        let second = subscribe();
        assert_ne!(first, second);

        for id in [first, json!(999)] {
            assert_eq!(
                invoke(&main, "graphql_unsubscribe", json!({ "id": id })),
                Ok(json!(null))
            );
        }
    }

    #[test]
    fn new_window_opens_nothing_once_a_quit_has_begun() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");
        app.state::<AppState>().shutdown.cancel();

        assert_eq!(invoke(&main, "new_window", json!({})), Ok(json!(null)));
        assert_eq!(app.webview_windows().len(), 1);
    }

    #[test]
    fn update_host_file_writes_the_merged_file_and_broadcasts_it() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let main = window(&app, "main");
        let (tx, rx) = std::sync::mpsc::channel();
        app.listen_any(host_file::UPDATED_EVENT, move |event| {
            tx.send(event.payload().to_string()).unwrap();
        });

        let patch = json!({ "patch": { "colorSchemePreference": "dark" } });
        let file = invoke(&main, "update_host_file", patch).unwrap();

        assert_eq!(file["colorSchemePreference"], "dark");
        let path = host_file::path(app.handle()).unwrap();
        assert!(path.starts_with(scratch.path()), "{}", path.display());
        assert_eq!(serde_json::to_value(host_file::read(&path)).unwrap(), file);
        // Emitted before the command returned.
        let broadcast: serde_json::Value = serde_json::from_str(&rx.try_recv().unwrap()).unwrap();
        assert_eq!(broadcast, file);
    }

    /// The label records are paced under is the calling webview's.
    #[test]
    fn log_webview_paces_records_under_the_calling_webviews_label() {
        let scratch = Scratch::new();
        let app = mock_app(scratch.path(), SidecarService::without_process(None));
        let state = app.state::<AppState>();
        *state.webview_log.lock().unwrap() = Limiter::new(LimiterConfig {
            burst: 1,
            error_reserve: 0,
            ..LimiterConfig::DEFAULT
        });
        let other = window(&app, "window-1");

        for message in ["one", "two"] {
            let args = json!({ "record": { "source": "graphql", "message": message } });
            assert_eq!(invoke(&other, "log_webview", args), Ok(json!(null)));
        }

        let withheld = state.webview_log.lock().unwrap().evict("window-1");
        assert_eq!(withheld, 1, "the second record is withheld");
    }
}
