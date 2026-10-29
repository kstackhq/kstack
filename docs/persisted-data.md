# Persisted data

Everything Kstack keeps across a restart, how each format is versioned, and what a build does with
a file a newer build wrote. Present tense, like a `CLAUDE.md`: this file states what is true now.
The policy and its reasons are in the
[ADR](adr/2026-10-29-each-persisted-format-carries-its-own-version-stamp.md).

**A change to a persisted format updates its row here in the same commit.**

Nothing has shipped. Every SQLite schema is still one `0001_init.sql`, edited in place
([ADR](adr/2026-08-29-schema-edit-not-migration.md)). See *First release* below.

## Policy

A downgrade is unsupported. Each format's own stamp is the enforcement: a build whose formats match
the files on disk opens them, whatever its version. There is no app-level marker and no app-level
migration step; every format migrates when it opens.

- **Durable**: user data. Carries a version and migrates forward from every shipped version. A
  file written by a newer Kstack is refused with a message naming the file and both versions.
- **Preference**: a choice that is cheap to lose. A newer file is read, and a write keeps what
  this build does not know, so a dev build and a release can share it.
- **Disposable**: a store Kstack rebuilds. A bad value reads as the default.

A migration is never removed: a user may skip releases.

## Where it lives

| Root | Resolved by | Holds |
| --- | --- | --- |
| `<data>` | `paths.rs` `app_data_dir`: `local_data_dir`/`Kstack` (`Kstack-dev` in debug) | what a user would lose |
| `<cache>` | `paths.rs` `app_cache_dir` (`<data>/cache` on Windows) | the cluster and log caches |
| `<config>` | Tauri `app_config_dir`, keyed by bundle id `sh.kstack.desktop` | `host.json` |
| OS keyring | service `Kstack` | the sign-in token |
| Webview storage | the webview's profile, keyed by bundle id | `localStorage` under `kstack:*` |

`<config>` and the webview profile hang off the bundle id, not the `Kstack-dev` leaf, so a dev
build and a release share them. `make clean-data` does not clear them. The caches live under
`<cache>` because the OS may reclaim that root; they are still durable, since they hold history the
cluster no longer has.

## Register

| Format | Where | Owner | Class | Version stamp | Forward path | Newer file | Pinned by |
| --- | --- | --- | --- | --- | --- | --- | --- |
| App DB | `<data>/app.db` | `internal/appdb` | durable | `sqlitemigrate` | SQL migrations | refused; the sidecar does not start | `sqlitemigrate` `TestApplyRejectsNewerThanBinary` (runner only; per-store tests are open on #512) |
| App DB JSON columns | `app.db`: message content blocks, server-tool counts, tool-call input and error, `ActionRequest`, watch result | `agent/chat`, `tools` | durable | rides `app.db`'s | an `app.db` migration | with `app.db` | with `app.db` |
| Control-plane store | `<data>/beehive.db` | `github.com/amorey/beehive/sqlite` | durable | beehive's own runner | beehive's migrations, taken by a module bump | refused; the sidecar does not start | beehive's `internal/sqlitemigrate` tests |
| Chat and monitor directories | `<data>/chats/<chat id>/`, `<data>/monitor/<cluster id>/`: `results/`, `tasks/`, `workspace/`, `toolhome/` | `agent/chat`, `tools` | durable | rides `app.db`'s | an `app.db` migration; the layout only grows and the root never moves | with `app.db` | with `app.db` |
| Cluster cache | `<cache>/kube/<cache id>.db` | `kubestore` | durable | `sqlitemigrate` | SQL migrations | refused; the cache reports `StoreFailed` with the message and the rest of the app runs | `kubesync` `TestArmReportsAStoreThatWillNotOpen` (any open failure; the newer-file case is open on #512) |
| Cluster cache `raw_json` | `kube/*.db` `objects.raw_json` | `kubestore/rawcodec.go` | durable | first byte (zlib `0x78`, JSON `{`) | a `kube/*.db` migration | with `kube/*.db` | `rawcodec_test.go` |
| Log cache | `<cache>/logs/<cache id>.db` | `logstore` | durable | `sqlitemigrate` | SQL migrations | refused; `OpenOrCreate` returns the error to the caller | open on #512 |
| Sidecar settings | `<data>/settings.json` | `lib/jsonsettings`, `services/settings` | preference | `schemaVersion` 1 | upgrade in `Open` (none written yet) | read; a write keeps its unknown keys and its version | `TestAWriteStampsTheVersion`, `TestAWriteKeepsTheKeysNoFieldNames` |
| Host file | `<config>/host.json` | `src-tauri/src/host_file.rs` | preference | `schemaVersion` 1 | none | read; a write drops unknown fields and stamps 1 (accepted gap, ADR) | `update_stamps_current_version` |
| Sign-in token | OS keyring, service `Kstack` | `services/auth` | disposable (sign in again) | none | — | unknown fields ignored | — |
| Pane state | `localStorage` `kstack:sidebar-open`, `kstack:sidebar-width`, the right sidebar's per-mode open and width, `kstack:memory-popover-*`, `kstack:log-viewer-wrap` | `src/lib/persisted-flag.ts`, `persisted-width.ts` | disposable | none | a bad value reads as the default | — | `persisted-*.test.ts` |
| Log-viewer columns | `localStorage` `kstack:log-viewer-columns` | `log-viewer/columns.ts` | disposable | none | unknown names dropped, bad JSON reads as the default | — | `columns.test.ts` |

## Not persisted formats

Kept on disk but never read back as a format, or not kept across a restart:

- **Logs**: the host's and the sidecar's text logs. Appended, never parsed.
- **`<cache>/kubectl/`**: kubectl's own cache, in kubectl's format.
- **`<cache>/tmp/`, `<runtime>/`**: a run's scratch, swept at start.
- **`sessionStorage`** `kstack:history-forward-ceiling`: one window session.
- **Provider API keys**: passed in at launch (`cfg.LLMKeys`), never written.
- **Provider payloads** inside the message blocks: a cache the provider may invalidate, dropped
  when stale (#516). Kstack's own block format is what migrates.

## Open on #512

- A refused `app.db` or `beehive.db` fails the sidecar's start with a migrate error in the log and
  no message on the start screen.
- No per-store test opens a newer `app.db`, `kube/*.db` or `logs/*.db` and pins the refusal.

## First release

Done in the PR that tags the first release, and recorded here when it lands:

- Freeze every `0001_init.sql`. From then on a schema change is a numbered migration; the rename
  trap in the [edit-in-place ADR](adr/2026-08-29-schema-edit-not-migration.md) applies to
  `kubestore`'s triggers.
- A v1 fixture test per durable store: `app.db`, `kube/*.db`, `logs/*.db`; `beehive.db` by module
  version.
- A backup of each durable store before the first migration past `0001`. Without it, a user who
  reinstalls an older build after a migration can only upgrade again.
- The updater, when it lands: its restart waits for the old sidecar to exit before the new one
  migrates.
