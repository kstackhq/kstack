# src-tauri — Tauri/Rust native host

The desktop app's native host (Tauri 2). It owns windows, the tray and menus, the Go sidecar's lifecycle, and bridges GraphQL from the webview to the sidecar over a Unix socket (named pipe on Windows).

## Distribution & updates

**Not distributed through the Mac App Store**, and **there are no in-app updates**. `release.yml` signs what it ships: a notarized `.dmg` with the sidecar signed inside, an `.msi` signed through SignPath, unsigned `.deb`/`.rpm`/`.AppImage`, and `SHA256SUMS` with a detached GPG signature. The app and its bundled sidecar (`externalBin`) are versioned and released together.

**The Linux packages carry Kstack's own bwrap** at `/usr/lib/kstack/bwrap`, the sidecar's sandbox (`sidecar/CLAUDE.md`). `make bwrap` (`scripts/build-bwrap.sh`) builds a pinned, SHA-256-checked bubblewrap into `src-tauri/linux/` (git-ignored). The release runs it on the bundle job's own runner before `tauri build`, so bwrap links the app's glibc; it links `libcap`, which the packages depend on. bwrap is never setuid. `tauri.linux.conf.json` lists it in each format's `files`; **a Linux `tauri build` without `make bwrap` first fails**. bubblewrap is LGPL-2.0-or-later: the packages carry its license under `/usr/share/doc/kstack/`, and the release attaches the tarball.

**The `.deb` carries an AppArmor profile for it**, `src-tauri/linux/kstack-bwrap`: AppArmor's `bwrap-userns-restrict`, renamed `kstack_bwrap`/`kstack_unpriv_bwrap` and attached to `/usr/lib/kstack/bwrap`. `postinst.sh` loads it only where `apparmor_parser`, a running AppArmor and abi 4.0 (`/etc/apparmor.d/abi/4.0`) are present — **a profile the parser cannot read fails `apparmor.service` on every boot**. `prerm.sh` removes it, and neither ever fails the install. The `.deb` carries AppArmor's GPL-2.0 license too. The AppImage's bwrap sits under a mount no profile names. → [ADR: Kstack ships its own bwrap](../docs/adr/2026-09-28-kstack-ships-its-own-bwrap.md).

**The Linux packages carry Kstack's own pasta too** at `/usr/lib/kstack/pasta`, which gives a sandboxed run the internet. `make pasta` (`scripts/build-pasta.sh`) builds a pinned, SHA-256-checked passt snapshot into `src-tauri/linux/` as `pasta` (passt runs as pasta by its name), no AVX2 build. The release runs it beside `make bwrap`. passt is GPL-2.0-or-later: both its licenses go under `/usr/share/doc/kstack/`, and the release attaches the tarball.

**The `.deb` carries an AppArmor profile for it**, `src-tauri/linux/kstack-pasta`: passt's own `usr.bin.pasta` with its abstractions written out, renamed `kstack_pasta`. It starts Kstack's bwrap under `kstack_bwrap` (`Px`, so **the bwrap profile must be loaded first**) or the system's (`PUx`). `postinst.sh` loads it after `kstack-bwrap`; `prerm.sh` removes it first. → [ADR: Kstack ships its own pasta](../docs/adr/2026-10-04-kstack-ships-its-own-pasta.md).

**The profiles are checked on a stock Ubuntu 24.04** by `scripts/check-sandbox-chain.sh`: the bundled bwrap and pasta through `sandbox-init`/`sandbox-shell`/`sandbox-pasta`, reaching `https://1.1.1.1/`, after asserting `kernel.apparmor_restrict_unprivileged_userns` is 1. The release runs it after installing the `.deb` (`check-bundle-linux`, gating the release); a pull request touching the Go paths runs it as `sandbox-apparmor` in `ci.yml`.

## Layout

- `main.rs` — thin; calls `lib::run()`.
- `lib.rs` — **entry point** (`run()`): builds the app, registers plugins + commands, runs `setup` (spawn sidecar, menu/tray, background tasks), owns the shutdown event loop.
- `commands.rs` — `#[tauri::command]` handlers. Keep thin — delegate to services. `quit` routes through `AppHandle::exit`. **`open_account_url` names its own URL**: a command that opened whatever it was handed would be an exfiltration channel.
- `cloud.rs` — the one home for the kstack cloud base URL (tray and webview account menus).
- `host_file.rs` — **`host.json`**, the persisted-settings source of truth. Versioned JSON, all-`Option` patches (`HostFilePatch`) through the one `update_host_file` command; defensive reads, atomic writes. Reaches the webview via `window.__KSTACK_HOST__` and the `host-file-updated` broadcast, both fed from one read in `build_window`. Persistence only. Webview counterpart: `src/lib/host-file.ts`. → [ADR: host.json settings](../docs/adr/2026-08-09-host-json-settings.md).
- `logging.rs`, `webview_log.rs` — see Logging. `paths.rs` — `app_data_dir`, `app_cache_dir`, `runtime_dir`, `log_dir`, the one place the `Kstack`/`Kstack-dev` leaf is resolved. **The sidecar gets three owner-only directories** as flags (`--data-dir`, `--cache-dir`, `--runtime-dir`, in `cmd_args`); on Windows the cache is `<data>/cache`. → [ADR: each file in the platform's directory for its kind](../docs/adr/2026-09-27-kstacks-files-follow-the-platforms-directory-kinds.md).
- `state.rs` — `AppState` via `app.manage`. `shutdown` is a `CancellationToken` cancelled once on Quit; `quit` is where the quit is.
- `app_menu.rs` — **macOS-only** menu bar: File, and Edit, whose predefined items are what deliver ⌘C/⌘V/⌘X/⌘A/⌘Z to the webview. A no-op elsewhere, where the webview's `AppMenu` calls `new_window`/`quit`.
- `tray/` — tray icon/menu + the gRPC auth-state watch supervisor. `tray/mod.rs` is Tauri wiring (rebuilds on the main thread); `tray/account_menu.rs` is the pure, unit-tested account section, hidden for the MVP behind `SHOW_ACCOUNT_SECTION` (the webview app bar likewise leaves out `AccountAvatar`). The tray does not surface kube-context.
- `window_manager.rs`, `dock_menu.rs` (macOS), `error.rs` — see Windows below.
- `quit.rs` — the quit state (*running*, *waiting*, *done*); see Graceful shutdown below.
- `os_theme.rs` — `prefers_dark()` before any window exists (macOS, Windows; not compiled on Linux). Every failure degrades to light.
- `services/sidecar/` — sidecar process + IPC (below).
- `wake/` — fires the sidecar's `Poke` on wake-from-sleep and network-return. `wake/core.rs` is the pure, unit-tested heart (`classify`, `run_coalescer` 3s debounce); the platform sources are thin FFI glue, and **the "which integer means online" mappings stay in `core.rs`** so they test everywhere. Failures are logged and swallowed. → [ADR: poke resync fan-out](../docs/adr/2026-08-09-poke-resync-fanout.md).

## Logging

**Text on stderr always, plus JSON files** (`.json().flatten_event(true)`), rotated by size (2 MB, five archives, `main.log.1` newest), in `~/Library/Logs/Kstack{,-dev}` on macOS and `<local_data_dir>/Kstack{,-dev}/logs` elsewhere. **Not `app_log_dir`**: it names the bundle id and has no debug variant. → [ADR: JSON logs rendered by the host](../docs/adr/2026-09-08-json-logs-rendered-by-the-host.md).

**The subscriber is built in two steps**, since the log directory needs an `AppHandle`: `logging::init_process()` installs the registry, an empty `reload::Layer`, the `EnvFilter` and stderr; `logging::install_file_layer` swaps the file layer in.

- **The reload layer goes first, directly on the registry**, or its handle's type cannot be written.
- **The swap is the first statement in `setup`, before `SidecarService::spawn`**, so startup reaches the file.

**The host writes `main.log` and `webview.log`, split by target**: `webview.log` holds every record carrying `logging::WEBVIEW_TARGET`. `main.log` takes the sidecar's forwarded lines only in a release build (`is_host_log`). Stderr carries everything. **The split is made by the writers, never a per-layer filter** — `reload` cannot carry a `Filtered` layer. → [ADR: webview records get their own log file](../docs/adr/2026-09-08-webview-records-get-their-own-log-file.md).

**The terminal format is `TerminalFormat`** (`logging.rs`), a tag per process plus the host's module. **The first `#[instrument]` in the host needs span rendering added there.** Colour is guarded on `Writer::has_ansi_escapes`; a debug build always colours, and `NO_COLOR` turns it off.

**Failing to open a log never stops the app** — it runs on stderr alone.

**The log file is flushed by hand.** `App::run` ends with `std::process::exit`, so no `Drop` runs: `RunEvent::Exit` calls `AppState::flush_log` **last**, and nothing logged after it reaches the file. Tests drop the guards before reading a file.

**Every message is one line.** `logging::sanitize_text` escapes control characters and caps the result after escaping, on a character boundary. Every webview string goes through it, and every line off the sidecar's pipes.

**The sidecar writes `sidecar.log` itself**: `spawn` passes `--log-file` (and `--log-stderr` in debug); an unresolvable log directory omits both and warns. The host logs the path, the spawn, `READY` and the exit. **`logs.rs` parses the sidecar's JSON pipe lines** (`parse_record`) and re-emits each under `logging::SIDECAR_TARGET`, extra fields folded into the message as `key=value`. **A line that will not parse passes through at `warn`** (a Go panic). **A message is sanitized before it is emitted**, since `serde_json` decodes its control characters (`a_forwarded_message_stays_on_one_line`). → [ADR: two processes, two log files](../docs/adr/2026-09-08-two-processes-two-log-files.md).

**Webview errors reach `webview.log`** through `log_webview` (`webview_log.rs`). The command reads the label off its `tauri::Webview` argument, never the payload, and hands everything to `record_from`. `stack`, `componentStack` and `context` ride together under **`reported`**, never flattened into host fields; the sanitized message is the one top-level page value. Severity is derived from the source. **The host bounds what it writes**: `MAX_*_BYTES` per field, truncating before serializing, never cut JSON. The command is `async`, off the main thread.

**Each webview is rate limited** by `AppState::webview_log`, a `Limiter` keyed by label (`LimiterConfig::DEFAULT`): token buckets, an `error`-only reserve, and suppression by signature. Nothing is lost silently — a summary names what was withheld. `WindowEvent::Destroyed` evicts the entry; `PageLoadEvent::Started` writes a marker. Labels are `main` or `window-N` (`WindowManager::next_label`, never reused). **The glob in `capabilities/default.json` must admit every label minted**, or the window silently gets no permissions.

**The level is `KSTACK_LOG_LEVEL` only.** A runtime setting would need its own `reload::Layer` around the `EnvFilter`.

## Windows

All chrome decisions live in `build_window` (`window_manager.rs`); `tauri.conf.json` declares **no windows** (a unit test pins this). Per-platform: macOS native decorations with Overlay title bar + repositioned traffic lights; Windows frameless-opaque; Linux frameless-transparent (the webview's `WindowFrame` paints border/shadow). → [ADR: per-platform window chrome](../docs/adr/2026-08-09-per-platform-window-chrome.md).

Traps that bite:

- **Building a window is a blocking call — use the blocking pool.** `WebviewWindowBuilder::build()` parks its thread until the webview exists; on the main thread that deadlocks Windows. Every off-main-thread entry point uses `tauri::async_runtime::spawn_blocking`, never `spawn` (which would park a worker serving `graphql_query`). Such commands take `AppHandle` alone. The macOS-only `app_menu.rs`/`dock_menu.rs` call from the main thread — fine there.
- **Windows are visible from creation, painted with the resolved scheme at t0** (`background_color_for`). There is **no reveal step** — do not reintroduce one. On macOS this depends on **`tauri/macos-private-api`** (mirrored by `app.macOSPrivateApi`); `background_color` must always be passed on opaque platforms, and the document must stay opaque. → [ADR: first-paint theming](../docs/adr/2026-08-09-first-paint-theming.md).
- **New windows cascade** (`cascade_position`), positioned explicitly on all three platforms.
- The pure helpers (`traffic_light_position`, `background_color_for`, `cascade_position`) compile on every platform so CI covers them. Traffic-light constants stay in sync with `src/components/widgets/app-bar.tsx`. **The position's `y` is not the gap above the buttons** — they keep an 8pt inset from the container's bottom, so centring is `y = (bar - button) / 2 + 8`, which `traffic_light_position` derives. `LIGHT_BACKGROUND`/`DARK_BACKGROUND` track `@kubetail/ui`'s `--background` by eye.

## Sidecar IPC (`services/sidecar/`)

The sidecar is a bundled child process (`service.rs::spawn`) on a per-instance socket picked pre-spawn (`ipc.rs`), inside an owner-only runtime directory (`paths.rs::runtime_dir`: `$XDG_RUNTIME_DIR/kstack` on Linux, a `0700` per-uid temp subdirectory reused only if this user owns it, `<data>/run` on Windows). Readiness: it prints `READY ` on stdout, and `SidecarService::ready()` waits on `READY_BUDGET` (30s), **not** `DEFAULT_CONNECT_BUDGET`, since the sidecar runs the login shell before it binds; `ReadyGate` does not retry, so one timeout is an error screen. Shutdown: dropping the child closes stdin → EOF → sidecar runs its stop sequence (`sidecar/CLAUDE.md`). Closing the last window does **not** exit the app; only Quit (or a Unix signal, `lib.rs::spawn_signal_handler`) does. Stdout carries the readiness protocol and nothing else; everything else is forwarded by `logs.rs`.

- **Queries/mutations**: one fresh HTTP/1 connection per call (`graphql/query.rs`), 30s `REQUEST_BUDGET`.
- **Subscriptions**: one SSE connection per subscription (`graphql/subscribe.rs`) into `{type,payload}` channel envelopes. Graceful completion → `{"type":"complete"}`; abnormal drop → `{"type":"closed"}` — read off body framing, since gqlgen's `event: complete` is data-less. **`{"type":"open"}` is sent only after the 200**, never on a failed dial or the ack; the frontend keys its resets on it. → [ADR: transport-status generation](../docs/adr/2026-08-09-transport-status-generation.md). `FrameSink` is the delivery seam.
- **gRPC** (`grpc.rs`): auth (watch/login/logout) + resync poke, over the same socket via h2c, one cached `Channel` re-dialed on drop. Bindings from `build.rs` over `proto/`. → [ADR: single-socket h2c from the standard library](../docs/adr/2026-10-26-single-socket-h2c-from-the-standard-library.md).

## Graceful shutdown (`AppState::shutdown`)

Quit cancels the app-wide `CancellationToken` before `SidecarService::graceful_shutdown`. Every app-lifetime background task holds a clone and `tokio::select!`s on `shutdown.cancelled()` in each loop/await. **New background tasks must follow the same pattern.** Per-subscription teardown is the `oneshot` cancel table in `graphql/subscribe.rs`, keyed by op id and tagged by webview.

**The wait for the sidecar never runs on the event loop**, where it would freeze the app for up to `SIDECAR_SHUTDOWN_GRACE` (23s, above the sidecar's 20s `shutdownBudget`). The first `ExitRequested` with a code moves `AppState::quit` from *running* to *waiting*, cancels `shutdown`, prevents the exit, hides every window, and starts a thread that waits, moves to *done*, and calls `exit` again (`lib.rs::start_quit`). **Every `ExitRequested` while *waiting* is prevented**: every Quit path reaches the same `app.exit(0)`, and letting a second Quit through would run `RunEvent::Exit` and kill the sidecar mid-stop. **No window opens once a quit has begun**: every path that opens or shows one ends in `WindowManager::new_window` or `show_main_window`. Each checks the `shutdown` token it holds on entry, then queues a second check on the main thread behind what it presented, even when a step of it failed, which hides a window whose build or show was still in flight when the quit began. A new path needs no check of its own; a refused ask logs a `warn`. A SIGTERM during the wait is swallowed (the signal task has returned), and a second launch is one more refused ask. `RunEvent::Exit` keeps the kill as the backstop and warns when it was needed; an exit that is not code 0 with no signal is logged at `warn`. A macOS logout skips all of this: the host answers `NSTerminateNow` (`dock_menu.rs`) and the kill runs at once.

## Security invariants

Full picture: [`docs/security-model.md`](../docs/security-model.md). What the host owns:

- **The webview never navigates off the app origin.** `build_window` installs `on_navigation(is_app_origin)` — an allowlist ending in `false` (app origin, `about:blank`, the dev server in debug) — and `on_new_window`, always `Deny`. A feature that needs an external URL needs a host command (the opener plugin, Rust-side). Verified by hand, not by a test.
- **The webview is trusted by custom host commands.** `capabilities/default.json` grants core defaults and window controls, no shell or opener grants, pinned by `default_capability_grants_only_window_chrome`. Add a permission only when a call site needs it, scoped as narrowly as the plugin allows.
- **Every dial checks who answered.** An address and the pid that must serve it travel together as one `ipc::Target`. The pid is read from the kernel (`peer.rs`, `LOCAL_PEERPID` on macOS), published by `spawn`, cleared when the child exits; `None` or an unreadable peer refuses. Pinned by `connect_refuses_a_peer_that_is_not_the_expected_process` and `query_refuses_a_server_that_is_not_the_sidecar`.
- **Nothing the sidecar needs may come from this process's environment**, which it inherits. Every endpoint is passed by `cmd_args` from constants in `service.rs`; new configuration goes the same way. `KSTACK_*` overrides work only in a `-tags debug` sidecar (`make sidecar-dev`).
- **A webview's subscriptions are torn down by the host** (`lib.rs::cancel_webview_subscriptions`) on `PageLoadEvent::Started` and `WindowEvent::Destroyed`, since a reload runs no JS teardown. Pinned by `cancel_webview_drops_only_that_webviews_subscriptions`.
- **Structured text from the webview never becomes a host log field or a second log line** — `reported` alone, sanitized, capped and paced. Pinned by `page_supplied_fields_ride_under_one_key` and the sanitizer tests.
- **The macOS entitlements are exceptions.** `disable-library-validation` and `allow-unsigned-executable-memory` remain unverified pending signed macOS testing (H-3). Don't add an entitlement without saying in `entitlements.plist` what needs it.

## Conventions

- **No `unwrap`/`expect`** — `#![warn(clippy::unwrap_used)]`. Poisoned mutexes: `.lock().unwrap_or_else(|p| p.into_inner())`.
- **Errors** (`error.rs::AppError`): transport failures cross the command boundary as `AppError::Io` so urql sees a `networkError` and retries. **HTTP non-2xx is NOT an error** — return `Ok(GraphqlResponse{status, body})`.
- Menu/tray callbacks are sync and can't return errors — log failures; for async work `tauri::async_runtime::spawn`.
- Off-main-thread menu mutation: queue with `app.run_on_main_thread(...)`.

## Tests & checks

- Inline `#[cfg(test)] mod tests` with `#[tokio::test]`; UDS/pipe fixtures via `interprocess`. Factor pure logic into free functions to unit-test it.
- **No magic sleeps** (root `CLAUDE.md`). Synchronize on the actual signal, or use `#[tokio::test(start_paused = true)]` (`ipc.rs`'s `connect_retries_until_endpoint_appears` is the reference).
- `make test-rust` — **builds the sidecar first** (an integration test spawns the real binary).
- `make lint-rust` = `cargo fmt --check`; `make vet-rust` = `cargo clippy --all-targets -- -D warnings`.
- **The compiler is pinned** in `rust-toolchain.toml`; CI reads the channel from it. rustup resolves it from the working directory — **a `rustup`/`cargo` call from the repo root needs `working-directory: ./src-tauri`**, or it silently uses the default toolchain.

When you change the host's architecture, conventions, or IPC contract, update this `CLAUDE.md` in the same change.
