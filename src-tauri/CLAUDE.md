# src-tauri — Tauri/Rust native host

The desktop app's native host (Tauri 2). It owns windows, the tray and menus, the Go sidecar's lifecycle, and bridges GraphQL from the webview to the sidecar over a Unix socket (named pipe on Windows).

## Distribution & updates

**Not distributed through the Mac App Store** — direct downloads, no MAS build target. **There are no in-app updates** — nothing checks for a newer build, and updating means downloading one by hand. `release.yml` signs what it ships: a notarized `.dmg` with the sidecar signed inside it, an `.msi` signed through SignPath, unsigned `.deb`/`.rpm`/`.AppImage`, and `SHA256SUMS` with a detached GPG signature beside all of them. The app and its bundled sidecar (`externalBin`) are versioned and released together — a release bump covers either side changing.

**The Linux packages carry Kstack's own bwrap**, the sidecar's sandbox (`sidecar/CLAUDE.md`), so the sandbox needs no package of the user's and every flag it passes is there whatever the distribution ships. `make bwrap` (`scripts/build-bwrap.sh`) builds bubblewrap 0.13.0 from its release tarball, checked against the SHA-256 the script pins, with meson (selinux, man pages, tests and both completions off) into `src-tauri/linux/`: `bwrap`, its `COPYING` as `bubblewrap-COPYING`, and the tarball, all ignored by git. The release's Linux bundle job runs it on its own runner before `tauri build`, so bwrap links the glibc the app does; it links `libcap`, which the packages depend on (`libcap2`, `libcap`). bwrap is never setuid. `tauri.linux.conf.json`, which Tauri merges on Linux alone, lists it in each format's `files` at `/usr/lib/kstack/bwrap`, beside the sidecar's `/usr/bin/kstack-sidecar`, where the sidecar looks when the system has no bwrap that runs; a Linux `tauri build` without `make bwrap` first fails for want of it. bubblewrap is LGPL-2.0-or-later: the packages carry its license under `/usr/share/doc/kstack/`, and the release attaches the tarball.

**The `.deb` carries an AppArmor profile for it**, `src-tauri/linux/kstack-bwrap` at `/usr/lib/kstack/apparmor/kstack-bwrap`: AppArmor's `bwrap-userns-restrict` (`profiles/apparmor/profiles/extras/` at v4.1.8, `abi <abi/4.0>`), the profile Ubuntu 25.04 and later load for `/usr/bin/bwrap`, with its profiles renamed `kstack_bwrap` and `kstack_unpriv_bwrap`, every name it carries renamed with them, and the first attached to `/usr/lib/kstack/bwrap`. bwrap may make a user namespace and use its capabilities there, and everything it starts runs stacked under a profile that denies every capability, which is what lets it run on Ubuntu 23.10 and 24.04, where AppArmor restricts unprivileged user namespaces. `postinst.sh` copies it into `/etc/apparmor.d` and loads it, only where `apparmor_parser`, a running AppArmor and the parser's abi 4.0 (`/etc/apparmor.d/abi/4.0`) are present — a profile there that the parser cannot read fails `apparmor.service` on every boot, as on 22.04's AppArmor 3. `prerm.sh` unloads and deletes it on removal, and neither ever fails the install. The profile is GPL-2.0, and the `.deb` carries AppArmor's license beside bubblewrap's. The AppImage's bwrap sits under its mount, which no profile names, so on those releases it has no sandbox, and from 25.04 the sidecar uses the system's profiled `/usr/bin/bwrap`, as it does wherever the system's runs. → [ADR: Kstack ships its own bwrap](../docs/adr/2026-09-28-kstack-ships-its-own-bwrap.md).

**The release checks the profile on a stock Ubuntu 24.04** (`check-bundle-linux`, amd64 and arm64, gating the release): it asserts `kernel.apparmor_restrict_unprivileged_userns` is 1, installs the `.deb`, and runs, as the runner's own user, what the probe runs — the bundled bwrap over `kstack-sidecar sandbox-init -- kstack-sidecar sandbox-shell -- /bin/sh -c /bin/true` — so it covers each exec of the profile's stack, the `/proc` mount and the filter, not the user namespace alone.

## Layout

- `main.rs` — thin; calls `lib::run()`.
- `lib.rs` — **entry point** (`run()`): builds the app, registers plugins + commands, runs `setup` (spawn sidecar, menu/tray, background tasks), owns the shutdown event loop.
- `commands.rs` — `#[tauri::command]` handlers (`graphql_query/subscribe/unsubscribe`, `ready`, `new_window`, `update_host_file`, `quit`, `log_webview`, `open_account_url`). Keep thin — delegate to services. `quit` routes through `AppHandle::exit`; `open_account_url` names its own URL, since a command that opened whatever it was handed would be an exfiltration channel.
- `cloud.rs` — the kstack cloud pages the app links out to. One home for the base URL, so the tray's account item and the webview's account menu can't drift.
- `host_file.rs` — **`host.json`**, the persisted-settings source of truth (color-scheme preference today). Versioned JSON, all-`Option` partial patches (`HostFilePatch`) through the one `update_host_file` command; defensive reads, atomic writes (temp + rename). Reaches the webview via the `window.__KSTACK_HOST__` initialization script and the `host-file-updated` broadcast, both fed from one read in `build_window`. Persistence only — how a setting is *used* lives with its consumer. Webview counterpart: `src/lib/host-file.ts`. → [ADR: host.json settings](../docs/adr/2026-08-09-host-json-settings.md).
- `logging.rs` — the process subscriber and `sanitize_text` (below). `webview_log.rs` — the webview error sink: validation, caps, the per-webview `Limiter`, and the page-load marker (below). `paths.rs` — `app_data_dir`, `app_cache_dir`, `runtime_dir` and `log_dir`, the one place the
  `Kstack`/`Kstack-dev` leaf is resolved. **The sidecar gets three directories**, each created owner-only and passed as a flag (`--data-dir`, `--cache-dir`, `--runtime-dir`, in `cmd_args`): data is `local_data_dir/Kstack`; cache is `cache_dir/Kstack` (`~/Library/Caches`, `$XDG_CACHE_HOME`), or `<data>/cache` on Windows, whose cache root is the data directory's own parent; runtime is below. → [ADR: each file in the platform's directory for its kind](../docs/adr/2026-09-27-kstacks-files-follow-the-platforms-directory-kinds.md).
- `state.rs` — `AppState` (sidecar, window_manager, tray, shutdown, log_guards, webview_log) via `app.manage`. `shutdown` is a `CancellationToken` cancelled once on Quit (see Graceful shutdown).
- `app_menu.rs` — **macOS-only** global menu bar; `build_app_menu` is a no-op on Linux/Windows, where the webview's `AppMenu` drives the same actions via `new_window`/`quit`.
- `tray/` — tray icon/menu + the gRPC auth-state watch supervisor. `tray/mod.rs` is Tauri wiring (`spawn_authstate_subscription`, `rebuild_tray_menu` — reads the snapshot in `AppState::tray`, rebuilds on main thread); `tray/account_menu.rs` is the pure, unit-tested account-section logic. Signed out → "Login / Create Account"; signed in → name-titled submenu (Account Settings → the account page from `cloud.rs`, Sign out). The tray does not surface kube-context (webview-only GraphQL feature).
- `window_manager.rs`, `dock_menu.rs` (macOS), `error.rs` — see Windows below.
- `os_theme.rs` — `prefers_dark()`, resolving the `system` preference before any window exists (macOS: `AppleInterfaceStyle` via `NSUserDefaults`; Windows: `AppsUseLightTheme` registry). Not compiled on Linux. Every failure path degrades to light.
- `services/sidecar/` — sidecar process + IPC (below).
- `wake/` — fires the sidecar's `Poke` on OS wake-from-sleep and network-return. `wake/core.rs` is the pure, unit-tested heart (`classify` rising-edge + `run_coalescer` trailing 3s debounce; injectable clock/sink); `wake/supervisor.rs` owns the channel and `#[cfg]`-spawns the platform sources (`macos.rs`/`windows.rs`/`linux.rs` — thin FFI glue; the "which integer means online" mappings stay in `core.rs` so they test everywhere). Poke is best-effort — failures logged and swallowed. → [ADR: poke resync fan-out](../docs/adr/2026-08-09-poke-resync-fanout.md).

## Logging

**Two sinks, one registry: text on stderr always, plus JSON files.** The file layer is
`.json().flatten_event(true)`, so a record's own fields sit beside the built-in ones and `main.log`
and `sidecar.log` are one shape; stderr keeps `tracing`'s text format, and the host is the only
process that renders one (below). Stderr is kept in release — running the
shipped binary from a terminal is how you get output from a user before a log directory helps them,
and on Windows those writes reach no console, which is harmless. The names are
stable, so "attach your log" is one path and a tail survives rotation — rotated
by size (2 MB, five numbered archives, `main.log.1` newest), in `~/Library/Logs/Kstack{,-dev}` on macOS and
`<local_data_dir>/Kstack{,-dev}/logs` elsewhere — macOS is the only platform with a standard log
location, and `app_log_dir` resolves to a data-dir leaf on the other two as well. Not `app_log_dir`
itself: it names the leaf by bundle id rather than the display name everything else here uses, and it
has no debug variant, so a dev run would write into an installed release's logs.

**The subscriber is built in two steps, because the log directory needs an `AppHandle` and
`init_process` runs before one exists.** `logging::init_process()` installs the registry, an empty
`reload::Layer`, the `EnvFilter` and the stderr layer, and returns the handle;
`logging::install_file_layer` swaps the file layer in from `setup`. Two rules hold it together:

- **The reload layer goes first, directly on the registry.** `reload::Layer<L, S>` implements
  `Layer<S>` for exactly the `S` it names, so after `.with(filter)` the accumulated subscriber type
  is no longer `Registry` and the handle's type cannot be written. Order costs nothing —
  `Layered::enabled` ANDs across the stack, so the filter still governs every layer.
- **The swap is the first statement in `setup`, before `SidecarService::spawn`.** Everything below
  it — the sidecar's whole startup, every window-creation line — is what a user is asked for when
  the app fails to start, and until the swap lands it reaches stderr alone.

**The host writes two files, split by target.** `main.log` holds the host's own events;
`webview.log` holds everything carrying `logging::WEBVIEW_TARGET`, the target `webview_log.rs`
stamps on every record it forwards. It is the routing key, not a field: `webview.log` is written
`.with_target(false)`, since the file holds nothing else. `main.log` keeps the target — there it
names the emitting module. The webview is the loudest producer here, and sharing one 2 MB
window would let a render-error storm evict the startup lines a user is asked for. The two files
are exclusive, and stderr is unsplit — it carries both, so a dev run stays one stream.
`main.log` also takes the sidecar's forwarded lines, but only in a release build — `is_host_log` is
where that is decided, because a debug build's pipes carry the whole log `sidecar.log` already
holds. **The split is made by the writers**, never by a per-layer filter: a `Filtered` layer
registers with the registry as the subscriber is built, and this layer arrives later through
`reload`, which documents that it cannot carry one.
→ [ADR: webview records get their own log file](../docs/adr/2026-09-08-webview-records-get-their-own-log-file.md).

**The terminal's format is the host's own** — `TerminalFormat` in `logging.rs`, a `FormatEvent`
impl, wired by `stderr_layer`:

```
2026-09-08T13:29:49.726026Z  INFO [sidecar] sidecar starting pid=95679 socket="/tmp/x.sock"
2026-09-08T13:29:49.725958Z  WARN [main]    kstack_lib::tray: auth-state tray watch failed
```

The tag names the part of the app that wrote the line, padded to one width so the column can be
scanned; a host line keeps its module after it, a forwarded one has none and would only repeat the
tag. It is hand-written because the tag sits *inside* what the formatter writes — a `MakeWriter`
can only prepend. What that costs is the stock format's span rendering: **the first `#[instrument]`
in the host needs it added here.** Levels and the target are painted by `paint`, guarded on
`Writer::has_ansi_escapes` — which `init_process` sets from `use_ansi()`, since the fmt layer does
not check and a stream redirected to a file would keep the escapes as garbage. **A debug build
always colours**: `tauri dev` relays our stderr through its own process, and that pipe is not a
terminal. `NO_COLOR` turns it off either way. → [ADR: JSON logs rendered by the
host](../docs/adr/2026-09-08-json-logs-rendered-by-the-host.md).

**Failing to open a log never stops the app**: `install_file_layer` reports to stderr and returns
no guards, and the app runs with stderr alone.

**The log file is flushed by hand on the way out.** `App::run` ends the process with
`std::process::exit`, so managed state is never dropped: holding the `non_blocking` guard in
`AppState` keeps the workers alive but their `Drop` never runs, and whatever is still queued — the
shutdown line included — dies with the process. `AppState::flush_log` takes and drops every guard, and
`RunEvent::Exit` calls it **last**, after the final log event; nothing logged after it reaches the
file. Writing off-thread is also why stderr is the synchronous sink of the two, and why the tests
drop the guards before reading a file instead of waiting.

**Every message is one line.** `logging::sanitize_text` escapes control characters and caps the
result — counted *after* escaping, since a newline becomes two bytes and a control character six —
cutting on a character boundary and ending with `…`. The fmt layer writes a message verbatim, so an
embedded newline would forge a log line and an escape sequence would move a reader's cursor. Every
webview string goes through it, and so does every line off the sidecar's pipes (`logs.rs`).

**`main.log` is the host's alone; the sidecar writes `sidecar.log` beside it.** `spawn` passes
`<log_dir>/sidecar.log` as `--log-file`, and `--log-stderr` too in debug builds; a log directory
that cannot be resolved omits both flags and is a warning, never a refused spawn. The host logs
the path it passed, so `main.log` says where the other file is, and it logs the sidecar's spawn,
its `READY`, and its exit — three lines that bound a startup failure from the host's side.
**`logs.rs` parses the pipes, because the sidecar logs JSON there too, and re-emits each line as an
event of the host's own** under `logging::SIDECAR_TARGET` — so `tracing` stamps, levels, filters and
renders both processes the same way, and one clock orders the terminal. `parse_record` takes
`timestamp`, `level` and `message`, drops the timestamp (the host stamps what it writes), and folds
the record's remaining fields into the message as `key=value`: `tracing` takes only static field
names, so they cannot be re-emitted as fields. The level is matched to a macro arm, since `tracing`
fixes it at compile time. Which file the event then reaches is `logging::is_host_log`'s decision,
not this module's — a debug build's pipes carry the whole log `sidecar.log` already holds, and only
a release build's pipes carry what the sidecar's own logger could not (a Go panic, its
cannot-open-file report, or the whole stream when it was given no path).

**A line that will not parse passes through at `warn`** — a Go panic is not JSON, and it is the one
sidecar line a user cannot get any other way; nothing says how serious it is, and neither of the two
that reach us is routine. Each arrives on its own pipe read, so a panic stays one line per line.
**A message is sanitized before it is emitted**: `serde_json` hands it back decoded, so its control
characters are real again and the fmt layer writes a message verbatim
(`a_forwarded_message_stays_on_one_line`). → [ADR: JSON logs rendered by the
host](../docs/adr/2026-09-08-json-logs-rendered-by-the-host.md), [ADR: two processes, two log
files](../docs/adr/2026-09-08-two-processes-two-log-files.md).

**Webview errors reach `webview.log`** through the `log_webview` command, under
`logging::WEBVIEW_TARGET` (`webview_log.rs`). The command is a thin wrapper: it reads the label off its `tauri::Webview`
argument — never the payload; the label is the webview's identity — and hands everything to
`record_from`, which the tests drive. Every line carries `window` (windows interleave) and `source`
(validated against the bus's set, else `unknown`); `stack`, `componentStack` and `context` ride
together under **`reported`**, one JSON value the host serializes and the record's only
page-controlled key, never flattened into host fields — a reader has to be able to tell
page-controlled text from a host-recorded field. The message is the one exception, and it is
sanitized. Severity is derived from the
source, not sent: `render`/`auth` are `error`, the rest — `unknown` included, since an unrecognized
source is most likely a newer frontend — are `warn`. **The host bounds what it writes, not what it
receives**: the request is parsed before the command body runs, so the frontend caps first and the
host re-applies `MAX_*_BYTES` per field, truncating before serializing (an over-long `context`
becomes `{"truncated": true}`, never cut JSON, and a `reported` over its own cap becomes that
whole). The command is `async` so the hot path during an
outage never runs on the main thread.

**Each webview is rate limited** by `AppState::webview_log`, a `Limiter` behind a mutex keyed by
label: a token bucket per webview (count and bytes, `LimiterConfig::DEFAULT`), a reserve of both
records and bytes only `error` records may draw on, and suppression by signature (source plus the
message's first line) with a window and an LRU ceiling. Nothing is lost silently: a summary at
`warn` names the count withheld on the next record accepted, and a suppressed signature's next
occurrence past its window carries `webview.suppressed`. `WindowEvent::Destroyed` evicts the entry
(reporting what it withheld); a record whose label no longer resolves to a live window is dropped,
and an entry a straddling record recreates is swept on the next lock. `PageLoadEvent::Started`
writes an `info` marker with the label — a reload keeps the label and replaces the JS context, so
the marker is how two crashes across it read as two. A webview's label is its identity here; each
window holds exactly one, so today that label is the window's own — `main` or `window-N`
(`WindowManager::next_label`, never reused). The glob in `capabilities/default.json` must admit
every one minted, or the window silently gets no permissions.

**The level is still `KSTACK_LOG_LEVEL` only.** Making it settable at runtime needs the `EnvFilter`
wrapped in its own `reload::Layer` and that handle kept — a different handle from this one, which
reloads a layer, not a filter.

## Windows

All chrome decisions live in `build_window` (`window_manager.rs`); `tauri.conf.json` declares **no windows** (a unit test pins this). Per-platform: macOS native decorations with Overlay title bar + repositioned traffic lights; Windows frameless-opaque; Linux frameless-transparent (the webview's `WindowFrame` paints border/shadow). → [ADR: per-platform window chrome](../docs/adr/2026-08-09-per-platform-window-chrome.md).

Traps that bite:

- **Building a window is a blocking call — use the blocking pool.** `WebviewWindowBuilder::build()` parks its thread until the webview exists; on the main thread that deadlocks Windows (WebView2's controller is created *by* the main-thread event loop). Every off-main-thread entry point (`commands::new_window`, the single-instance handler, the tray's window items) uses `tauri::async_runtime::spawn_blocking`, never `spawn` (which would park a worker serving `graphql_query`). Such commands take `AppHandle` alone and resolve state inside the closure. The macOS-only `app_menu.rs`/`dock_menu.rs` call straight from the main thread — fine there.
- **Windows are visible from creation, painted with the resolved scheme at t0** (`background_color_for`). There is **no reveal step** — no `visible(false)`, no `show_window`, no page-load handler, no fallback timer. Do not reintroduce one. On macOS this depends on **`tauri/macos-private-api`** (mirrored by `app.macOSPrivateApi` in `tauri.conf.json`); `background_color` must always be passed on opaque platforms, and the document must stay opaque. → [ADR: first-paint theming](../docs/adr/2026-08-09-first-paint-theming.md).
- **New windows cascade** (`cascade_position`): 28px down-right of the anchor (focused window, else last built), restarting from center when the step wouldn't fit the work area. Positioned explicitly on **all three platforms** (AppKit auto-cascades unpositioned windows; Linux/Windows pile up).
- The pure helpers (`traffic_light_position`, `background_color_for`, `cascade_position`) are free functions compiled on every platform so CI covers them. Traffic-light pixel constants stay in sync with `src/components/widgets/app-bar.tsx`, where the lights sit. The position's `y` is **not** the gap above the buttons: the runtime grows the native title-bar container to `button + y` and anchors it to the window's top, but sets only the buttons' horizontal origin — they keep their 8pt inset from the container's bottom, so what shows above them is `y - 8`. Centering in a bar means `y = (bar - button) / 2 + 8`; `traffic_light_position` derives it, and its test asserts the gaps above and below match rather than restating the formula. `LIGHT_BACKGROUND`/`DARK_BACKGROUND` track `@kubetail/ui`'s `--background` token by eye — the webview paints the real token over them on first frame.

## Sidecar IPC (`services/sidecar/`)

The sidecar is a bundled child process (`service.rs::spawn`) on a per-instance socket picked pre-spawn (`ipc.rs`), inside an owner-only runtime directory (`paths.rs::runtime_dir`: `$XDG_RUNTIME_DIR/kstack` on Linux, a `0700` per-uid temp subdirectory otherwise, reused only if this user owns it, else one beside it: the one an earlier launch made and this user owns, or a fresh `mkdtemp` one; on Windows `<data>/run`, since `TEMP` can be shared, which the named pipe ignores). The sidecar keeps its snapshot and each sandboxed run's directory there too. Readiness: it prints `READY ` on stdout; `SidecarService::ready()` gates the first call, on its own `READY_BUDGET` (30s) rather than the dial budget — a sum: on macOS and Linux the sidecar runs the user's login shell before it binds (`sidecar/internal/loginshell`, up to 10s), then migrates and binds; the model catalogs are discovered after `READY`, and the `models` query waits for them within the 30s `REQUEST_BUDGET` of `graphql/query.rs`; `ReadyGate` does not retry on its own, so one timeout is an error screen. `DEFAULT_CONNECT_BUDGET` still paces reconnects and has nothing to do with startup. Shutdown: dropping the child closes stdin → EOF → sidecar drains. Closing the last window does **not** exit the app; only Quit (or a Unix signal, `lib.rs::spawn_signal_handler`) does. Its stdout carries the readiness protocol and nothing else; anything else on either pipe is forwarded by `logs.rs` — to `main.log` in a release build, to the host's stderr in a debug one (see Logging).

- **Queries/mutations**: one fresh HTTP/1 connection per call (`graphql/query.rs`). No pooling — UDS connect is sub-ms.
- **Subscriptions**: one SSE connection per subscription (`graphql/subscribe.rs`), parsed via `eventsource-stream` into `{type,payload}` channel envelopes. Graceful completion → `{"type":"complete"}` (silent reconnect); abnormal drop → `{"type":"closed"}` (reconnect + report) — the split is read off body framing, since gqlgen's `event: complete` is data-less and discarded by SSE dispatch. On a successful dial the host emits `{"type":"open"}` before the snapshot — **only after** the 200, never on a failed dial. The frontend keys its accumulator resets on it; don't move that signal to the ack. → [ADR: transport-status generation](../docs/adr/2026-08-09-transport-status-generation.md). The `FrameSink` trait is the delivery seam (production: `TauriChannelSink`). **The host tears a webview's subscriptions down itself** (`lib.rs::cancel_webview_subscriptions`, on `PageLoadEvent::Started` and `WindowEvent::Destroyed`): a reload or a window close runs no JS teardown, so every handle is tagged with its webview label — otherwise each orphaned subscription keeps streaming into a callback id the new page never registered ("Couldn't find callback id N", once per frame) and leaks a sidecar connection and a cluster watch.
- **gRPC** (`grpc.rs`): auth (watch/login/logout) + resync poke, over the same socket via h2c — tonic dials the `interprocess` stream with HTTP/2 prior knowledge, one cached `Channel`, re-dialed on drop. Bindings generated by `build.rs` from `proto/auth.proto`/`proto/poke.proto`. Entry points: `SidecarService::{watch_auth_state, start_login, logout, poke}`. → [ADR: single-socket h2c](../docs/adr/2026-08-09-single-socket-h2c.md).

## Graceful shutdown (`AppState::shutdown`)

Quit cancels the app-wide `CancellationToken` before `SidecarService::graceful_shutdown`. Every app-lifetime background task holds a clone and `tokio::select!`s on `shutdown.cancelled()` in each loop/await (tray supervisor, wake supervisor, signal handler). **New background tasks must follow the same pattern.** Per-subscription teardown is separate — the `oneshot` cancel table in `graphql/subscribe.rs`, keyed by op id and tagged by webview.

## Security invariants

Full picture: [`docs/security-model.md`](../docs/security-model.md). What the host owns:

- **The webview never navigates off the app origin.** `build_window` installs two default-deny
  callbacks: `on_navigation(is_app_origin)` — an allowlist ending in `false`, admitting the bundle's
  own origin, `about:blank`, and (behind `cfg(debug_assertions)`) the dev server — and
  `on_new_window`, which always answers `Deny`. Client-side routing is `pushState` and never reaches
  either. A feature that seems to need an external URL needs a host command instead: the opener
  plugin, as the tray's account item does. Which engine fires which callback when is verified by
  hand, not by a test.
- **The webview is trusted by custom host commands.** `capabilities/default.json` grants core defaults and window controls, without shell or opener grants; custom commands also expose GraphQL, host preferences and app lifecycle, pinned by `default_capability_grants_only_window_chrome`; the opener and shell plugins are registered for Rust-side use and grant the page nothing. Add a permission only when a call site needs it, scoped as narrowly as the plugin allows — a grant with no consumer is standing authority for injected script.
- **Every dial checks who answered.** An address and the pid that must be serving it travel together as one `ipc::Target`, so there is no way to dial without checking — which matters because gRPC re-dials on loss and every subscription reconnects. The pid is read from the kernel (`peer.rs`), published on a `watch` channel by `spawn`, and cleared when the child terminates; `None` refuses, since a pid the OS has reassigned is exactly what must not pass. An unreadable peer is refused too. macOS reads `LOCAL_PEERPID` itself — its `xucred` carries no pid, so `interprocess` reports `None` there. This stops another process of the same user impersonating the sidecar; it is no defence against code already inside the host process. Pinned by `connect_refuses_a_peer_that_is_not_the_expected_process` and `query_refuses_a_server_that_is_not_the_sidecar`.
- **The sidecar inherits this process's environment, so nothing it needs may come from there.** Every endpoint — cloud URL, OAuth issuer, client id, keychain service — is passed by `cmd_args` from constants in `service.rs`, never read from our own environment (that would move the redirection risk, not close it). New configuration goes the same way. The sidecar honours `KSTACK_*` overrides only when built with `-tags debug` (`make sidecar-dev`), which is a standalone dev seam, not a dev-run one.
- **A webview's subscriptions are torn down by the host** on `PageLoadEvent::Started` and `WindowEvent::Destroyed` — a reload runs no JS teardown, and an orphaned subscription keeps a sidecar connection and a cluster watch alive. Pinned by `cancel_webview_drops_only_that_webviews_subscriptions`.
- **Structured text from the webview never becomes a host log field or a second log line.** `webview_log.rs` writes it under `reported` alone, sanitized and capped, and paces it per webview; the message is the one page-controlled value at top level, and it is sanitized too. Pinned by `page_supplied_fields_ride_under_one_key` and the sanitizer tests in `webview_log.rs`.
- **The macOS entitlements are exceptions, not defaults.** `disable-library-validation` permits loading libraries without Apple's or the app's Team ID signature; it does not govern spawning a separate sidecar. Its necessity, and that of `allow-unsigned-executable-memory`, remain unverified pending signed macOS testing (H-3). Don't add an entitlement without saying in `entitlements.plist` what needs it.

## Conventions

- **No `unwrap`/`expect`** — `#![warn(clippy::unwrap_used)]`. Poisoned mutexes: `.lock().unwrap_or_else(|p| p.into_inner())`.
- **Errors** (`error.rs::AppError`): transport failures cross the command boundary as `AppError::Io` so urql sees a `networkError` and retries. **HTTP non-2xx is NOT an error** — return `Ok(GraphqlResponse{status, body})`; don't map 4xx/5xx to `Err`.
- Menu/tray callbacks are sync and can't return errors — log failures; for async work `tauri::async_runtime::spawn`.
- Off-main-thread menu mutation: queue with `app.run_on_main_thread(...)` (muda requires the main thread).

## Tests & checks

- Inline `#[cfg(test)] mod tests` with `#[tokio::test]`; UDS/pipe fixtures via `interprocess`. Factor pure logic into free functions to unit-test it.
- **No magic sleeps** (repo-wide — see the root `CLAUDE.md` for the rule and its two carve-outs). Synchronize on the actual signal (`watch`/`mpsc`, `wait_for`, polling a condition), or run the test on a paused clock — `#[tokio::test(start_paused = true)]` auto-advances virtual time between parked timers, so a `tokio::time::sleep` inside it costs nothing real (`ipc.rs`'s `connect_retries_until_endpoint_appears` is the reference). Never a real `sleep` to let work settle.
- `make test-rust` — **builds the sidecar first** (an integration test spawns the real binary).
- `make lint-rust` = `cargo fmt --check`; `make vet-rust` = `cargo clippy --all-targets -- -D warnings` (enforced in CI).
- **The compiler is pinned** in `rust-toolchain.toml` (channel + `rustfmt`/`clippy`), so clippy's verdict is the same locally and in CI. Upgrading is a deliberate one-line PR; CI reads the channel out of that file, so nothing else needs bumping with it. rustup resolves it by walking up from the working directory — a `rustup`/`cargo` call from the repo root needs `working-directory: ./src-tauri` (as `release.yml`'s `rustup target add` steps do) or it silently uses rustup's default toolchain.

When you change the host's architecture, conventions, or IPC contract, update this `CLAUDE.md` in the same change.
