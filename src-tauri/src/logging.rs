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

//! The host's subscriber: stderr always, plus two files once a path is known.
//! `main.log` holds the host's own events and `webview.log` the records
//! `webview_log` forwards; stderr carries all three processes, tagged, so a dev
//! run stays one stream on one clock.

use std::fmt;
use std::io::IsTerminal;
use std::path::Path;

use tauri::{AppHandle, Runtime};
use tracing::{Event, Level, Metadata, Subscriber};
use tracing_appender::non_blocking::{NonBlocking, WorkerGuard};
use tracing_subscriber::fmt::format::{FormatEvent, FormatFields, Writer};
use tracing_subscriber::fmt::time::{FormatTime, SystemTime};
use tracing_subscriber::fmt::writer::{MakeWriter, MakeWriterExt};
use tracing_subscriber::fmt::FmtContext;
use tracing_subscriber::layer::{Layer, SubscriberExt};
use tracing_subscriber::registry::LookupSpan;
use tracing_subscriber::util::SubscriberInitExt;
use tracing_subscriber::{reload, EnvFilter, Registry};

use crate::paths::log_dir;

/// Swaps the file layer in once a path is known. Boxed because a
/// `reload::Layer` implements `Layer<S>` for exactly the `S` it names, and
/// `Option<L>` is itself a `Layer`, which is what lets it start empty.
type FileLayer = Option<Box<dyn Layer<Registry> + Send + Sync>>;
pub type ReloadHandle = reload::Handle<FileLayer, Registry>;

use crate::error::{AppError, Result};

/// Where the live file rotates and how many archives (`main.log.1`…) survive.
/// Rotation is by size, so a bad day costs the same disk as a quiet week.
const MAX_FILE_BYTES: usize = 2 * 1024 * 1024;
const MAX_ARCHIVES: usize = 5;

/// The target every `webview_log` record carries, and the key the file layer
/// routes on. The webview is the loudest producer here and the least trusted,
/// so its records get their own file: sharing `main.log`'s rotation would let
/// one render-error storm evict the startup lines a user is asked for.
pub const WEBVIEW_TARGET: &str = "webview";

/// The target every line off the sidecar's pipes carries (`services::sidecar::logs`).
pub const SIDECAR_TARGET: &str = "sidecar";

/// The tag naming which part of the app wrote a line, padded to one width so
/// the terminal's columns line up.
const TAG_MAIN: &str = "[main]    ";
const TAG_SIDECAR: &str = "[sidecar] ";
const TAG_WEBVIEW: &str = "[webview] ";

/// What a message cut by [`sanitize_text`] ends with.
pub const TRUNCATED: &str = "…";

/// Makes `text` fit on one log line: control characters are escaped, and the
/// result is capped at `max_bytes`. The fmt layer writes a message verbatim,
/// so an embedded newline would forge a line and an escape sequence would move
/// a reader's cursor.
///
/// The cap counts escaped bytes — escaping expands, and a cap counted before
/// it lets a line reach several times its budget. A cut lands on a character
/// boundary and ends with [`TRUNCATED`].
pub fn sanitize_text(text: &str, max_bytes: usize) -> String {
    let mut out = String::with_capacity(text.len());
    for ch in text.chars() {
        match ch {
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if c.is_control() => out.push_str(&format!("\\u{{{:x}}}", c as u32)),
            c => out.push(c),
        }
    }
    if out.len() <= max_bytes {
        return out;
    }
    let mut end = max_bytes.saturating_sub(TRUNCATED.len());
    while !out.is_char_boundary(end) {
        end -= 1;
    }
    out.truncate(end);
    out.push_str(TRUNCATED);
    out
}

/// The file layer and the guards that must outlive it.
pub struct FileLog {
    pub layer: Box<dyn Layer<Registry> + Send + Sync>,
    /// Dropping these flushes the worker threads. Held for the process's life,
    /// or the tail of each log is lost at exit.
    pub guards: Vec<WorkerGuard>,
}

/// Whether the event was forwarded from another part of the app rather than
/// emitted by the host itself.
fn is_forwarded(meta: &Metadata<'_>) -> bool {
    matches!(meta.target(), WEBVIEW_TARGET | SIDECAR_TARGET)
}

/// The tag a line is written under.
fn source_tag(meta: &Metadata<'_>) -> &'static str {
    match meta.target() {
        WEBVIEW_TARGET => TAG_WEBVIEW,
        SIDECAR_TARGET => TAG_SIDECAR,
        _ => TAG_MAIN,
    }
}

/// The level, padded to five columns and coloured as `tracing`'s own formatter
/// colours it.
fn level_style(level: &Level) -> (&'static str, &'static str) {
    match *level {
        Level::TRACE => ("TRACE", "35"),
        Level::DEBUG => ("DEBUG", "34"),
        Level::INFO => (" INFO", "32"),
        Level::WARN => (" WARN", "33"),
        Level::ERROR => ("ERROR", "31"),
    }
}

/// Writes `text` under an ANSI code, when the writer takes them.
fn paint(writer: &mut Writer<'_>, code: &str, text: &dyn fmt::Display) -> fmt::Result {
    if writer.has_ansi_escapes() {
        write!(writer, "\x1b[{code}m{text}\x1b[0m")
    } else {
        write!(writer, "{text}")
    }
}

/// The terminal's line: `<timestamp> <level> <tag> [<module>: ]<message>`.
///
/// Hand-written because the tag sits between the level and the message, which
/// is inside what the formatter writes — a `MakeWriter` only wraps the writer
/// the formatter is handed, so it can prepend and nothing else. What it costs
/// is the stock format's span rendering, which nothing in the host uses.
struct TerminalFormat;

impl<S, N> FormatEvent<S, N> for TerminalFormat
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    N: for<'a> FormatFields<'a> + 'static,
{
    fn format_event(
        &self,
        ctx: &FmtContext<'_, S, N>,
        mut writer: Writer<'_>,
        event: &Event<'_>,
    ) -> fmt::Result {
        let meta = event.metadata();
        let ansi = writer.has_ansi_escapes();

        if ansi {
            write!(writer, "\x1b[2m")?;
        }
        SystemTime.format_time(&mut writer)?;
        if ansi {
            write!(writer, "\x1b[0m")?;
        }

        let (level, colour) = level_style(meta.level());
        write!(writer, " ")?;
        paint(&mut writer, colour, &level)?;
        write!(writer, " {}", source_tag(meta))?;

        // The tag names the process, the target the module inside it. A
        // forwarded line has no module of its own, and its target is the name
        // the tag already carries.
        if !is_forwarded(meta) {
            paint(&mut writer, "2", &format_args!("{}:", meta.target()))?;
            write!(writer, " ")?;
        }

        ctx.field_format().format_fields(writer.by_ref(), event)?;
        writeln!(writer)
    }
}

/// Installs the process subscriber: stderr now, a file later.
///
/// The file's directory needs an `AppHandle`, which does not exist this early,
/// so the file layer arrives through [`install_file_layer`]. Until it does,
/// events reach stderr only.
///
/// The reload layer sits directly on the registry so its `S` is `Registry`;
/// after `.with(filter)` the accumulated subscriber type is something else and
/// the handle becomes unwriteable. Order costs nothing — `Layered::enabled`
/// ANDs across the stack, so the filter still governs every layer.
pub fn init_process() -> ReloadHandle {
    let (file, handle) = reload::Layer::new(None as FileLayer);
    let filter = EnvFilter::try_from_env("KSTACK_LOG_LEVEL")
        .or_else(|_| EnvFilter::try_from_default_env())
        .unwrap_or_else(|_| "info".into());

    tracing_subscriber::registry()
        .with(file)
        .with(filter)
        .with(stderr_layer(std::io::stderr, use_ansi()))
        .init();

    handle
}

/// Whether to colour the terminal. The fmt layer does not check anything, so a
/// stream redirected to a file would otherwise keep the escapes as garbage.
///
/// A debug build always colours: `tauri dev` relays our stderr through its own
/// process, and that pipe is not a terminal. `NO_COLOR` is the way out of both.
fn use_ansi() -> bool {
    if std::env::var_os("NO_COLOR").is_some_and(|v| !v.is_empty()) {
        return false;
    }
    cfg!(debug_assertions) || std::io::stderr().is_terminal()
}

/// The terminal's view of all three parts of the app. Both arguments are
/// parameters so a test reads back what a terminal would show; `ansi` is
/// decided by the caller, since whether the real stderr is one says nothing
/// about a test's writer.
fn stderr_layer<S, W>(writer: W, ansi: bool) -> impl Layer<S>
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    W: for<'a> MakeWriter<'a> + Send + Sync + 'static,
{
    tracing_subscriber::fmt::layer()
        .event_format(TerminalFormat)
        .with_ansi(ansi)
        .with_writer(writer)
}

/// Points the process's log at files under the app data directory, returning
/// the guards that must outlive them.
///
/// Never fails the caller: a desktop app that will not start because it cannot
/// log is worse than one that logs to stderr alone. Call before anything worth
/// logging — the sidecar's startup above all.
pub fn install_file_layer<R: Runtime>(
    handle: &ReloadHandle,
    app: &AppHandle<R>,
) -> Vec<WorkerGuard> {
    match log_dir(app)
        .and_then(|dir| file_layer(&dir))
        .and_then(|FileLog { layer, guards }| {
            // Fails only if the subscriber is gone, which cannot happen here —
            // but it shares the fallback rather than becoming this function's
            // one panic.
            handle.reload(Some(layer))?;
            Ok(guards)
        }) {
        Ok(guards) => guards,
        Err(err) => {
            tracing::error!(%err, "no log file; logging to stderr only");
            Vec::new()
        }
    }
}

fn is_webview(meta: &Metadata<'_>) -> bool {
    meta.target() == WEBVIEW_TARGET
}

fn is_host_log(meta: &Metadata<'_>) -> bool {
    match meta.target() {
        WEBVIEW_TARGET => false,
        // A debug build hands the sidecar `--log-stderr`, so its pipes carry
        // the whole log that `sidecar.log` already holds. A release build's
        // pipes carry only what its logger could not write, and that belongs
        // here.
        SIDECAR_TARGET => !cfg!(debug_assertions),
        _ => true,
    }
}

/// Builds the size-rotating file layer over `dir`: host events to `main.log`,
/// webview records to `webview.log`.
pub fn file_layer(dir: &Path) -> Result<FileLog> {
    file_layer_with(dir, MAX_FILE_BYTES, MAX_ARCHIVES)
}

/// The caps are parameters so a test rotates in tens of bytes.
fn file_layer_with(dir: &Path, max_bytes: usize, max_archives: usize) -> Result<FileLog> {
    let (main, main_guard) = rotating_writer(&dir.join("main.log"), max_bytes, max_archives)?;
    let (webview, webview_guard) =
        rotating_writer(&dir.join("webview.log"), max_bytes, max_archives)?;

    // JSON, and flattened so a record's own fields sit beside the built-in
    // ones: the sidecar's file is the same shape, so the three read as one
    // stream. `logs.rs` renders it back to text for the dev terminal.
    //
    // The split is made by the writers, which see the metadata too, and never
    // by a per-layer filter: a `Filtered` layer registers with the registry as
    // the subscriber is built, and this layer arrives later through `reload`.
    // A layer each, rather than one over a combined writer, so `webview.log`
    // can drop the target — it holds nothing else, and the routing key is not
    // worth repeating on every line. `main.log` keeps it: there the target is
    // the emitting module.
    let host = tracing_subscriber::fmt::layer()
        .json()
        .flatten_event(true)
        .with_writer(main.with_filter(is_host_log));
    let webview = tracing_subscriber::fmt::layer()
        .json()
        .flatten_event(true)
        .with_target(false)
        .with_writer(webview.with_filter(is_webview));

    Ok(FileLog {
        layer: Box::new(host.and_then(webview)),
        guards: vec![main_guard, webview_guard],
    })
}

/// One size-rotating file, written off-thread.
fn rotating_writer(
    path: &Path,
    max_bytes: usize,
    max_archives: usize,
) -> Result<(NonBlocking, WorkerGuard)> {
    Ok(tracing_appender::non_blocking(rotating_file(
        path,
        max_bytes,
        max_archives,
    )?))
}

/// The size-rotating file under [`rotating_writer`]'s worker.
fn rotating_file(
    path: &Path,
    max_bytes: usize,
    max_archives: usize,
) -> Result<file_rotate::FileRotate<file_rotate::suffix::AppendCount>> {
    // `FileRotate` swallows a failed open and then writes to nowhere, so an
    // unopenable file has to be caught here to reach the stderr fallback.
    std::fs::OpenOptions::new()
        .append(true)
        .create(true)
        .open(path)
        .map_err(|e| AppError::Io(std::io::Error::other(format!("open log file: {e}"))))?;

    // `BytesSurpassed`, not `Bytes`: the exact cap cuts mid-write, splitting a
    // log line across two files. Surpassed rotates after the write that
    // crosses it, so the file overshoots by at most one line.
    Ok(file_rotate::FileRotate::new(
        path,
        file_rotate::suffix::AppendCount::new(max_archives),
        file_rotate::ContentLimit::BytesSurpassed(max_bytes),
        file_rotate::compression::Compression::None,
        None,
    ))
}

/// A `tracing` sink a test reads back, one formatted line per event.
#[cfg(test)]
#[derive(Clone, Default)]
pub(crate) struct Capture(std::sync::Arc<std::sync::Mutex<Vec<u8>>>);

#[cfg(test)]
impl std::io::Write for Capture {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

/// Runs `f` under a capturing subscriber and returns what it wrote.
#[cfg(test)]
pub(crate) fn captured(f: impl FnOnce()) -> String {
    let capture = Capture::default();
    let sink = capture.clone();
    let subscriber = tracing_subscriber::fmt()
        .with_writer(move || sink.clone())
        .with_ansi(false)
        .finish();
    tracing::subscriber::with_default(subscriber, f);
    let bytes = capture.0.lock().unwrap().clone();
    String::from_utf8(bytes).expect("utf-8 output")
}

#[cfg(test)]
mod tests {
    use super::*;
    use tracing_subscriber::layer::SubscriberExt;

    /// Reads one of the appender's files under `dir`.
    fn read_log(dir: &Path, name: &str) -> String {
        std::fs::read_to_string(dir.join(name)).expect("log file readable")
    }

    /// Emits `event` through a file layer over `dir` and returns `main.log`'s
    /// contents. The guards are dropped before the read: the appenders write on
    /// their own threads, and dropping is what flushes — there is no sleep that
    /// would make this deterministic.
    fn emit_and_read(dir: &Path, event: impl FnOnce()) -> String {
        let FileLog { layer, guards } = file_layer(dir).expect("build file layer");
        let subscriber = tracing_subscriber::registry().with(layer);
        tracing::subscriber::with_default(subscriber, event);
        drop(guards);
        read_log(dir, "main.log")
    }

    fn temp_dir(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("kstack-log-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("temp dir");
        dir
    }

    /// The live names never change: they are the paths support asks for and
    /// the paths a log viewer follows across rotations.
    #[test]
    fn writes_to_a_stable_main_log() {
        let dir = temp_dir("name");
        emit_and_read(&dir, || tracing::info!("hello from the file layer"));
        assert!(dir.join("main.log").exists());
        assert!(dir.join("webview.log").exists());
        std::fs::remove_dir_all(&dir).unwrap();
    }

    /// A debug build hands the sidecar `--log-stderr`, so its pipes carry the
    /// whole log `sidecar.log` already holds; a release build's pipes carry
    /// only the panics that have nowhere else to go. Getting this backwards
    /// either stores the sidecar's log twice or loses a crash from `main.log`.
    #[test]
    fn a_forwarded_sidecar_line_reaches_main_log_only_in_a_release_build() {
        let dir = temp_dir("sidecar");
        let main = emit_and_read(&dir, || {
            tracing::warn!(target: SIDECAR_TARGET, "panic: runtime error");
        });

        assert_eq!(
            main.contains("panic: runtime error"),
            !cfg!(debug_assertions),
            "got: {main}"
        );
        std::fs::remove_dir_all(&dir).unwrap();
    }

    /// The split is the point: the webview is the loudest producer, and a
    /// record of its landing in `main.log` too would spend the rotation budget
    /// a user's startup lines live in.
    #[test]
    fn a_webview_record_goes_to_its_own_file() {
        let dir = temp_dir("split");
        let main = emit_and_read(&dir, || {
            tracing::info!(target: WEBVIEW_TARGET, "a webview record");
            tracing::info!("a host event");
        });

        assert!(main.contains("a host event"), "got: {main}");
        assert!(!main.contains("a webview record"), "got: {main}");

        let webview = read_log(&dir, "webview.log");
        assert!(webview.contains("a webview record"), "got: {webview}");
        assert!(!webview.contains("a host event"), "got: {webview}");
        // The file holds nothing but webview records, so the target that routed
        // them there says only what the filename does.
        assert!(!webview.contains(r#""target""#), "got: {webview}");
        assert!(main.contains(r#""target""#), "got: {main}");

        std::fs::remove_dir_all(&dir).unwrap();
    }

    /// Rotation is what a reader sees as history: the live file holds the
    /// newest lines, each numbered archive is one rotation older, and archives
    /// past the cap are deleted. The caps are parameters so the test rotates
    /// in tens of bytes, not megabytes.
    ///
    /// The file is written directly rather than through the worker: dropping
    /// a `WorkerGuard` waits at most about a second for the worker, so a slow
    /// rename could leave the last rotation unfinished when the files are read.
    #[test]
    fn rotates_by_size_and_keeps_a_bounded_history() {
        use std::io::Write;

        let dir = temp_dir("rotate");
        let mut file = rotating_file(&dir.join("main.log"), 64, 2).expect("open log file");
        // Each line surpasses the cap on its own, so every line after the
        // first begins with a rotation.
        for n in 1..=4 {
            let line = format!("line{n} {}\n", "x".repeat(64));
            file.write_all(line.as_bytes()).expect("write line");
        }
        // Windows removes no open file, and the cleanup below removes this one.
        drop(file);

        let mut names: Vec<String> = std::fs::read_dir(&dir)
            .expect("log dir readable")
            .map(|e| e.expect("entry").file_name().to_string_lossy().into_owned())
            .collect();
        names.sort();
        assert_eq!(names, ["main.log", "main.log.1", "main.log.2"]);

        let live = std::fs::read_to_string(dir.join("main.log")).expect("live file");
        assert!(live.contains("line4"), "got: {live}");
        let oldest = std::fs::read_to_string(dir.join("main.log.2")).expect("oldest archive");
        assert!(oldest.contains("line2"), "got: {oldest}");

        std::fs::remove_dir_all(&dir).unwrap();
    }

    /// Drops ANSI escapes so an assertion reads the text alone.
    fn plain(text: &str) -> String {
        let mut out = String::with_capacity(text.len());
        let mut chars = text.chars();
        while let Some(c) = chars.next() {
            if c == '\u{1b}' {
                for c in chars.by_ref() {
                    if c == 'm' {
                        break;
                    }
                }
            } else {
                out.push(c);
            }
        }
        out
    }

    /// Runs `f` under the terminal's own layer and returns what it wrote.
    fn on_stderr(f: impl FnOnce()) -> String {
        on_stderr_with_ansi(true, f)
    }

    fn on_stderr_with_ansi(ansi: bool, f: impl FnOnce()) -> String {
        let capture = Capture::default();
        let sink = capture.clone();
        let subscriber =
            tracing_subscriber::registry().with(stderr_layer(move || sink.clone(), ansi));
        tracing::subscriber::with_default(subscriber, f);
        let bytes = capture.0.lock().unwrap().clone();
        String::from_utf8(bytes).expect("utf-8 output")
    }

    /// The terminal carries all three parts of the app, so every line has to
    /// say which one wrote it — and say it in one column, or it cannot be
    /// scanned.
    #[test]
    fn a_line_is_tagged_with_the_part_of_the_app_that_wrote_it() {
        let sidecar = plain(&on_stderr(
            || tracing::info!(target: SIDECAR_TARGET, "hello"),
        ));
        let webview = plain(&on_stderr(
            || tracing::info!(target: WEBVIEW_TARGET, "hello"),
        ));
        let host = plain(&on_stderr(|| tracing::info!("hello")));

        assert!(sidecar.contains(TAG_SIDECAR), "got: {sidecar:?}");
        assert!(webview.contains(TAG_WEBVIEW), "got: {webview:?}");
        assert!(host.contains(TAG_MAIN), "got: {host:?}");

        assert_eq!(
            TAG_SIDECAR.len(),
            TAG_MAIN.len(),
            "the tags must share one width"
        );
        assert_eq!(TAG_WEBVIEW.len(), TAG_MAIN.len());
    }

    /// The timestamp leads, as it does in every other tool that prints logs,
    /// and the tag sits in the column after the level.
    #[test]
    fn a_line_leads_with_its_timestamp_then_level_then_tag() {
        let raw = on_stderr(|| tracing::warn!(target: SIDECAR_TARGET, "hello"));
        assert!(raw.contains('\u{1b}'), "the level lost its colour: {raw:?}");

        let out = plain(&raw);
        let (timestamp, rest) = out.split_once(' ').expect("a timestamp");

        assert!(
            timestamp.starts_with("20") && timestamp.ends_with('Z'),
            "got: {out:?}"
        );
        assert_eq!(rest, format!(" WARN {TAG_SIDECAR}hello\n"), "got: {out:?}");
    }

    /// The tag says which process wrote the line, so a forwarded line that also
    /// printed its target would say it twice. The host's own line keeps the
    /// target: there it names the module, which the tag does not.
    #[test]
    fn a_forwarded_line_does_not_repeat_its_tag_as_a_target() {
        let sidecar = plain(&on_stderr(
            || tracing::info!(target: SIDECAR_TARGET, "hello"),
        ));
        assert!(!sidecar.contains("sidecar:"), "got: {sidecar:?}");

        let host = plain(&on_stderr(|| tracing::info!("hello")));
        assert!(host.contains("kstack_lib::logging"), "got: {host:?}");
    }

    /// A redirected stream is not a terminal, and escape codes in a file a user
    /// mails us are noise they have to strip.
    #[test]
    fn a_line_is_plain_when_the_stream_is_not_a_terminal() {
        let out = on_stderr_with_ansi(false, || tracing::warn!("hello"));
        assert!(!out.contains('\u{1b}'), "got: {out:?}");
        assert!(out.contains(" WARN "), "got: {out:?}");
    }

    /// One event, one line: the formatter owns the newline now, so a missing or
    /// doubled one is ours to get wrong.
    #[test]
    fn each_event_is_written_once() {
        let out = on_stderr(|| {
            tracing::info!(target: SIDECAR_TARGET, "forwarded");
            tracing::info!("host");
        });
        assert_eq!(out.lines().count(), 2, "got: {out:?}");
    }

    /// `ansi` is a default feature and the fmt layer does not detect that its
    /// writer is a file, so the obvious spelling colours the log with escape
    /// codes.
    #[test]
    fn writes_no_ansi_escapes() {
        let dir = temp_dir("ansi");
        let contents = emit_and_read(&dir, || tracing::info!("hello from the file layer"));
        assert!(!contents.contains('\u{1b}'), "got: {contents:?}");
        std::fs::remove_dir_all(&dir).unwrap();
    }

    /// A log we cannot open must not stop the app, so the failure has to arrive
    /// as a value the caller can fall back on. Unix only — the mode bits are the
    /// mechanism — and skipped as root, which they do not refuse.
    #[cfg(unix)]
    #[test]
    fn an_unwritable_directory_is_an_error_not_a_panic() {
        use std::os::unix::fs::PermissionsExt;

        let dir = temp_dir("readonly");
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o500)).unwrap();

        // Root ignores the mode bits, so the assertion below would invert.
        if std::fs::write(dir.join(".probe"), b"").is_ok() {
            std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
            std::fs::remove_dir_all(&dir).unwrap();
            return;
        }

        assert!(file_layer(&dir).is_err());

        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
        std::fs::remove_dir_all(&dir).unwrap();
    }

    /// The fmt layer writes a message verbatim onto one line, so a newline in
    /// it forges a log line and an escape sequence moves a reader's cursor.
    #[test]
    fn sanitize_text_escapes_newlines_and_control_characters() {
        let out = sanitize_text("first\nsecond\x1b[31mred\r\t", 1024);
        assert_eq!(out, "first\\nsecond\\u{1b}[31mred\\r\\t");
        assert!(!out.contains('\n') && !out.contains('\u{1b}'));
    }

    /// The cap is on the escaped bytes: a newline costs two and a control
    /// character six, so a cap counted before escaping lets a line grow to
    /// several times its budget.
    #[test]
    fn sanitize_text_bounds_the_escaped_size() {
        let out = sanitize_text(&"\n".repeat(100), 20);
        assert!(out.len() <= 20, "got {} bytes: {out:?}", out.len());
        assert!(out.ends_with(TRUNCATED), "got: {out:?}");
    }

    /// A cap landing inside a multi-byte character truncates before it, so
    /// the result stays valid text.
    #[test]
    fn sanitize_text_truncates_on_a_character_boundary() {
        // Each "é" is two bytes; the cap of 8 leaves 5 bytes beside the marker.
        let out = sanitize_text("ééééé", 8);
        assert_eq!(out, "éé…");
    }

    #[test]
    fn sanitize_text_leaves_a_short_plain_message_alone() {
        assert_eq!(sanitize_text("hello, world", 64), "hello, world");
    }

    #[test]
    fn writes_an_event_to_a_file() {
        let dir = temp_dir("write");
        let contents = emit_and_read(&dir, || tracing::info!("hello from the file layer"));
        assert!(
            contents.contains("hello from the file layer"),
            "got: {contents}"
        );
        std::fs::remove_dir_all(&dir).unwrap();
    }
}
