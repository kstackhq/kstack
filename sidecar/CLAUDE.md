# sidecar — Go backend

A standalone Go binary started by the Tauri host. It serves the app's GraphQL API and a gRPC control channel and owns all Kubernetes logic. **No TCP**: it listens on a Unix socket (named pipe on Windows), prints `READY unix:<path>` to stdout, and shuts down on `SIGINT`/`SIGTERM` or **stdin EOF**. A sandboxed run's forwarder, a process of its own (`kstack-sidecar sandbox-init`), holds a loopback port for the run's life: on Linux the loopback of the run's own network namespace, on macOS the host's, since Seatbelt has no private loopback.

`--data-dir`, `--cache-dir` and `--runtime-dir` are **required** and absolute; `app.New` errors on an empty or relative one, makes a missing one 0700, and tests pass `t.TempDir()`. `<data-dir>/app.db` is `internal/appdb`'s: one migration sequence, numbered files in `appdb/migrations/`, never a second embed against the same file. `appdb.Open` hands back two pools — the one-connection writer, migrated, and a `query_only` reader — so a consumer's reads never queue behind its writes — and runs the file's janitor, one per open `DB` (`appdb/janitor.go`): a freelist-gated bounded vacuum, then a `TRUNCATE` `wal_checkpoint` after a vacuum or when the log outweighs the file; a zero interval runs none, and `Close` joins it before the pools close. **A service's statements are a `sqlstmt.Set`** prepared on `db.Write, db.Read` (see `sqlstmt` under `internal/`); the SQL stays the service's. Nothing has shipped, so a table change edits `0001_init.sql` rather than adding a file. → [ADR: schema edit, not migration](../docs/adr/2026-08-29-schema-edit-not-migration.md).

**The app owns app.db.** `app.New` opens the file once and hands the `*appdb.DB` to every service that writes or watches it (`clustersvc.New(db, …)`, `memorysvc.New(db, …)` and `chatsvc.New(db, …)`). The file opens right before the first such service, so an earlier constructor failing leaves nothing to close, and a later one failing closes it before `New` returns. It is the first of `App.parts`, a `lifecycle.CloseFunc` (nothing to start), so the reverse close order releases it after every service, and a failed `Start` in `main.run` still reaches `application.Close`. A service prepares its statements on the pools it is given and closes only those; its tests build it over a temporary DB of their own (`openTestDB` in `chatsvc`) and simulate a failed store by closing that DB. **The DB carries the change bus** (`appdb.go`): `Notify(key)` after a commit, never inside the transaction, and `Subscribe(keys…)` before the first read — a `gobus/conflate` receiver, one coalesced ping per key, ended by `DB.Close`; no keys is a panic. **The keys are named there** (`KeyClusters`, `KeyChats`, `KeyMemories`, `MessagesKey`, `StreamKey`), so a writer in one service and a watcher in another cannot spell one apart; a service wraps them under its own id type and adds none of its own. `clustersvc` and `chatsvc` both notify and subscribe to `KeyClusters` (*Cluster subsystem*, below), and `memorysvc`'s watch re-reads on it too, since a cluster's delete cascades to its memories. A service joins its own pumps in its stop, before that close runs. **Row ids are `appdb`'s**: `NewID()` mints a canonical lowercase UUIDv7, increasing in the order minted within a process, and `ValidateUUID` accepts the canonical spelling of a v4 or v7 with the RFC variant and nothing else — not the nil UUID, braces, `urn:`, uppercase, bare hex or a ULID. It validates a client's request key as well as a row id, and says nothing about who minted it. → [ADR: the app owns app.db](../docs/adr/2026-09-16-the-app-owns-app-db.md).

## Directories

The host resolves three directories and passes them in (`src-tauri/CLAUDE.md`). **`app/paths.go`
names every path under them** (`pathsOf`; the doc comment on `paths` is the tree), grouped into
each owner's own `Paths` (`clustersvc.Paths`, `cloud.Paths`, `bash.Paths`), and each service is
handed its own and names nothing else. A directory's field ends in `Dir` and a file's in `File`. **Each subtree has one owner**, which makes it 0700,
sweeps it and removes it:

```
<data>/                                what a user would lose
  app.db                               app
  security.json                        app: the security settings
  beehive.db                           clustersvc
  settings.json, settings-queue.json   cloud
  chats/<chat id>/                     chatsvc: results/, tasks/, workspace/, toolhome/
<cache>/                               what Kstack rebuilds
  kubestore/<cache id>.db              clustersvc: the mirror
  kubectl/<cluster id>/<server>/       bash: the kubectl cache
  tmp/<pid>-*/, tmp/<pid>.lock         bash: a sandboxed run's or the login shell's TMPDIR, the sidecar's lock
<runtime>/                             what lives for a session
  kstack-sidecar-<host pid>-<n>.sock   the host
  shell/                               bash: the snapshot, and Windows' scripts
  runs/<pid>-*/, runs/<pid>.lock       bash: a run's kubeconfig and socket, the lock
```

The app makes the three directories and nothing under them. `beehive.db` stays with the data: it
names each mirror file by cache id, so it outlives a cleared cache. **The host's log directory is
Kstack's too**: `main` sets `app.Config.LogDir` to the directory of `--log-file`, and `pathsOf` adds
it to `bash.Paths.DeniedDirs` when it lies outside the three (macOS's `~/Library/Logs/Kstack`;
Linux's `<data>/logs` adds nothing), so a grant of the home does not open it
(`TestTheLogDirectoryIsKstacks`). Read, Write and Edit fence every one of Kstack's directories, the
same list (`chatTools`). → [ADR: each file in the platform's directory for its
kind](../docs/adr/2026-09-27-kstacks-files-follow-the-platforms-directory-kinds.md).

This file states what is true now. Why it is that way lives in `docs/adr/`; every section links its ADRs. Rationale goes there, not here.

## Layout

`main.go` is lifecycle only; `internal/app` is the composition root and routing; GraphQL lives in `graph/`. No `server` package.

`main()` first hands `os.Args` to `sandbox.Main`, which runs a `sandbox-pasta`, `sandbox-init` or
`sandbox-shell` command line (*Tools*, below), and exits with its code. Otherwise it does three things before `run`, in this order: tighten the
umask, parse the command line, and
install the logger. Parsing comes first
because `--log-file`/`--log-stderr` decide where records go. The shutdown signals are listened for
before `run`, and their context reaches `app.New` too, which runs the login shell under it: a quit
during it cancels it, so `loginshell.Resolve` kills and reaps the login shell's session, and `run`
exits cleanly before `READY`. Tightening the umask returns the one
the process started with, which `main` sets as `app.Config.UserUmask`: a file Write makes for the
user takes it, not the sidecar's owner-only one. `main` also sets `app.Config.RunLoginShell`, outside
`run`: a test's `run` never spawns the developer's login shell.
`run(ctx, cfg, …)` is the tested seam and takes the parsed config;
a bad flag exits 2 from `main`. `main` also calls the logger's `Close` by hand — `os.Exit` runs no
deferred call.

**Logging** (`internal/logging`): records go to stderr by default, to a rotating file with
`--log-file`, and to both with `--log-stderr` as well; the host passes the file and adds stderr in
debug builds. `Open` builds the writers and probes the path up front — lumberjack opens on its
first write and `slog` drops a handler's error, so an unopenable path would swallow every record
silently; a failure returns the stderr-only logger *and* the error, and never stops the sidecar.
The file is **JSON**, one object per record, keyed the way the host keys its own — `hostKeys`
renames slog's `time`/`msg` to `timestamp`/`message` and stamps the time. Nothing names the
process: `sidecar.log` holds nothing else, and the host labels the pipe lines it renders itself.

```json
{"timestamp":"2026-09-08T10:28:29.677488Z","level":"INFO","message":"sidecar starting","pid":95679}
```

So both log files are one shape, and **nothing here formats a human-readable line** — the host
parses this back and renders the terminal line for both processes (`src-tauri/CLAUDE.md`, Logging).
**One record is one line** comes free from the JSON encoder, which escapes control characters, and
that is what keeps cluster-controlled text from forging a line (`TestInitWritesOneLinePerRecord`).
→ [ADR: JSON logs rendered by the
host](../docs/adr/2026-09-08-json-logs-rendered-by-the-host.md), [ADR: two processes, two log
files](../docs/adr/2026-09-08-two-processes-two-log-files.md).

- `internal/app/` probes the sandbox once (`probeSandbox`, a test's seam, under the context `New` is handed, the shutdown signal's; a probe that context cut short answers its error, which `New` returns, and `run` exits cleanly on it), then, with `RunLoginShell`, runs the login shell in it (`launchShell`, *The login shell*, below), **before anything that reads the environment**: on macOS it sets the environment process-wide, and `chatTools` reads the proxy variables. Then it builds `poke`, `kubeconfig`, opens the security settings (`securityconfig.Open`, before `app.db`, since the store holds no handle) and `app.db`, builds `clustersvc`, `memorysvc` and `chatsvc` over it, `auth`, `cloud`, wires `graph.NewServer` + `grpcserver.NewServer`, and multiplexes both onto one h2c handler. `App.parts` is start order (app.db → poke → kubeconfig → cluster → cloud → memory → chat, then the `PATH` sync when the launch read a `PATH` on a machine with a sandbox, then the shell snapshot with `RunLoginShell`); stop and close reverse it. The sync folds the launch's `PATH` into the stored list, and one that fails is a warning, not a startup error. **kubeconfig before cluster is load-bearing** (`app_test.go` pins it). The transports stay out of the slice; `grpcServer.Stop()` runs first in `Close`.

  **`READY` promises a socket, not a finished startup.** `run` prints it after the bind and before `Start`; the first request is answered after `Start`, and every part completes its startup work inside `Start`. Everything that reads the environment does so after the shell import at the top of `app.New`, which finishes before `READY` — nothing sends before it, and `net/http` reads the proxy variables once per process on the first request.
- `graph/` — `schema.graphqls`, generated code, resolvers, `server.go`. Resolver deps are non-nil; tests wire fakes. `Resolver.SecurityCfg` is `securityconfig.Service`, the settings store with the frozen `PATH` kept in it; its resolvers are `securityRefused`, and `sandboxPath`, `sandboxPathFault` and the three `sandboxPath*` mutations, which answer an empty list, no fault and a refusal on a machine with no sandbox. A `securityconfig.PathRefusal` reaches the wire as `KSTACK_VALIDATION_ERROR` carrying its words (`sandboxPathErr`). Its permission resolvers are `permissionSettings` and the six `permission*` mutations, each answering the settings it left (`permissionSettings` in `util.go`, the known contexts read off `Clusters().List`), and a refused one `KSTACK_VALIDATION_ERROR` with the reason (`permissionsAfter`); a rule's id is `appdb.NewID()`. The folder resolvers are `sandboxFolders(chatID)` and `folderGrant` and `folderRevoke`, each answering the whole `SandboxFolders` (the always grants and the chat's with each `refused` reason, the never-readable list and the wide folders; empty lists on a machine with no sandbox), through `chatsvc`, where an empty chat id is no chat: a `Chat` grant with no `chatID`, or an empty one, is `KSTACK_VALIDATION_ERROR`, so it never widens to every chat, and an `Always` one ignores it. A `FolderRefusal` reaches the wire as `KSTACK_VALIDATION_ERROR` whose `rule` is the check's, with a link's `target` beside it (`folderErr`).
- `grpc/` — `AuthService`, `PokeService`, committed protoc output in `authpb/`, `pokepb/`. Regenerate with `make proto`; **never hand-edit `*.pb.go`**. `IsGRPCRequest` lives here.
- `internal/` — `ipc`, `atomicjson`, `logging`, `safe` (an error rendered for a log line, and a command's output redacted: `Redact` line by line, `RedactJSON` a JSON text by its structure, for text that is one line with its newlines escaped; the field and flag rules read a credential's name off `credentialNames`, with or without the separator inside it, so camelCase keys match), `sqlitemigrate` (the migration runner, `Apply`), `sqlitepool` (the one home of the SQLite open contract: `OpenWriter` a store's one writer connection, `OpenReader` a reader pool, `OpenQuery` read-only connections through a caller's driver with none kept idle), `sqlstmt` (a store's statement table, prepared once on a file's writer and reader pools and routed per call: a `[]sqlstmt.Statement` indexed by the store's own id type, each entry its text and pool — `OnWriter`, `OnReader`, or `OnBoth` for a read some caller runs inside a write transaction; `Prepare[ID]` compiles it at open, since modernc caches nothing and a text handed to a pool at a call site is compiled every time; `Set.Stmts()` issues on the pools, `Set.InTx` inside one write transaction, `Set.InReadTx` inside one read-only transaction on the reader, always rolled back; inside a transaction the copy rebound, once per id, is the one prepared on that transaction's pool, and an id its pool does not hold panics; `Set.Close` finalizes the statements alone, and a closed set refuses `InTx`/`InReadTx` with `ErrClosed`, since `Tx.StmtContext` would quietly re-prepare a closed statement; imports nothing of ours; → [ADR: one statement set](../docs/adr/2026-09-16-sqlstmt-prepares-a-services-statements.md)), `appdb`, `rawjson`, `apimeta` (wire vocabulary no service owns — the delta-frame type, `ObjectID`, `ClusterID`, which `clustersvc` aliases, and `ChatID`, which `chatsvc` aliases and `memorysvc` and `tools.Runtime` name), `deltafold` (a watch's memory: `Snapshot`/`Diff`/`Upsert`/`Has` over a caller's key, equality and frame, plus `Send`; imports `apimeta` alone), `version` (`Version` is `dev` unless the linker stamped it: `scripts/build-sidecar.go` passes `-X …/internal/version.Version=$SIDECAR_VERSION` when set, `release.yml` sets it to the release version, and `main` logs it on the `sidecar starting` line; nothing reads a version from the environment or a file), `poke`, `kubeconfig`, `drain`, `lifecycle`, `loginshell`, `workqueue`, `supervisor`, `clustercard` (the cluster card a chat send will carry), `rootdir` (a directory opened and removed through an `os.Root`, under *Tools*), `sandbox` (the machine's sandbox and a run's forwarder, under *Tools*), `kubeproxy` (the cluster proxy a sandboxed run reaches its cluster through, under *Tools*), `permissions` (the classes, modes and rules a cluster write is decided by, under *Tools*), `session` (one agent run's identity and policy, under *Tools*), `memorysvc` (the notes a chat's cluster sees, below), `securityconfig` (the security settings, below), `catalog` (the providers and the tools each is offered, below), `testutil` (test-only, imported by no production code), plus the subsystems below.

## gRPC + GraphQL over one socket (h2c)

`internal/app` owns the topology; `grpc/` owns the predicate. The cluster surface is GraphQL-only. → [ADR: single-socket h2c](../docs/adr/2026-08-09-single-socket-h2c.md).

Shutdown order from `main.go`: `app.NotifyShutdown()` → `srv.Shutdown` → `app.DrainWithContext` → `stop(ctx)` → `app.Close()`. Two traps: grpc-go's `GracefulStop` **panics** on the h2c path, so `Stop` runs only after the drain; and never cancel via `http.Server.BaseContext`, which would tear down the shared connection mid-stream. `NotifyShutdown` cancels a stream's context, so it flushes its terminal frame, while an ordinary request keeps `srv.Shutdown`'s grace.

## Security invariants

Full picture: [`docs/security-model.md`](../docs/security-model.md). The sidecar holds every credential in the system, so these are load-bearing:

- **Every endpoint is an argument.** `configFromArgs` (`config.go`) parses the whole command line, including `--cloud-url`, `--oauth-issuer`, `--oauth-client-id` and `--keychain-service`; the host passes them. The environment reaches the config only through `applyEnvOverrides` (`config.go`), a no-op unless the binary is built with `-tags debug` (`make sidecar-dev`, for a standalone dev run with no host) — and only as the `getenv` the call is handed: `os.Getenv` from `main`, a map of the test's own in every test, so no test reads a key out of the shell running it. A model provider's base URL is one of those overrides: `KSTACK_<ID>_BASE_URL`, one per id in `catalog.BaseURLVars()`, lands in `Config.LLMBaseURLs`, so a release build never takes a model endpoint from the environment. `KSTACK_LOG_LEVEL` is the one variable a release build still reads — a log level redirects nothing. **The model-provider keys are the exception, and a narrow one**: every key variable in `catalog.KeyVars()` (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and the eight Chat Completions vendors') is read by `configFromArgs` in every build, because a key selects an account and never an endpoint, so it cannot redirect where anything is sent. A key that is set is kept under its provider's id in `Config.LLMKeys` and registered with `safe.AddSecret` before the logger exists; one that is not is left out, and `catalog.New` lists no row for it. `main` then calls `takeProviderKeys` — after the parse, before the shell import starts — which removes every key variable in the table, set or not, and nothing else: the sidecar spawns kubeconfig credential plugins and a child inherits the environment. `config_test.go` pins the boundary; `go test` builds untagged, and the coverage gate runs both builds.

- **Only the host process may connect.** `ipc.Authenticated` checks each accepted connection's peer pid against `--host-pid` (the kernel stamps it, so a client cannot claim another's) and closes anything else without ending the accept loop; zero, the standalone-run default, falls back to the uid alone. The file mode carries the rest: `ipc.Listen` tightens the umask *before* `net.Listen` so the socket is never briefly world-accessible, then chmods 0600; Windows binds the pipe owner-only (`D:P(A;;GA;;;OW)`). Both are pinned by tests. Authentication runs the other way too: the host verifies on every dial that the process serving the endpoint is the sidecar it spawned (`src-tauri/src/services/sidecar/{ipc,peer}.rs`), and on Unix places the endpoint in an owner-only runtime directory. Never widen access or add a TCP listener — the GET transport is registered alongside POST and SSE and is only harmless because the transport is local.
- **Redaction happens at write time, keyed off the body's own group and kind,** so it cannot be bypassed by how an object was addressed (`kubestore/objects.go`). A new read path serves the stored body; it does not get to re-derive what to hide. It fails closed: a path occupied by the wrong type is dropped, not skipped — the discrimination is the `err` a `Nested*` read returns, since "absent" and "there but unreadable" share the `found` boolean. The table is deliberately incomplete, so treat a cache file as holding cluster data in the clear — that is what makes its file mode and its lifetime security properties. Storing it in the clear is a decision, not an oversight. → [ADR: the cache is ordinary application data](../docs/adr/2026-09-02-the-cache-is-ordinary-application-data.md).
  → [ADR: secret redaction](../docs/adr/2026-08-30-secret-redaction-at-write-time.md).
- **Reading a kubeconfig can execute code.** `clientcmd` honours `exec` credential plugins, and the connection probe dials every declared context on startup and on every file change. Anything that widens what gets probed widens what runs. On macOS the process environment is an allowlisted import of the user's login shell (`internal/loginshell`), so what a plugin name resolves to — and which identity it picks — is what their shell would produce.
- **GraphQL diagnostics exclude request data.** The error presenter logs only operation type, Go error type and — for a coded error — its `code`, which is server-chosen vocabulary and the only thing that says which refusal it was (`TestErrorLogOmitsRequestData`, `TestErrorLogNamesTheCode`); variables, inline literals, aliases, names and error messages can contain sensitive values. Every other diagnostic is rendered by `internal/safe`, which bounds a message and strips what a credential looks like in an error — a URL's query and userinfo, an echoed `Authorization`/`Set-Cookie`, a bearer/JWT token — and **blanks every value registered with `safe.AddSecret`** wherever it appears: `readProviders` registers each provider key it read before the logger exists, since the vendors' keys share no prefix a shape rule could match. Matches are found in the original text and merged, so a short key inside a long one leaves no tail in the clear; a value under sixteen bytes registers nothing. `safe.HasSecret` asks whether a memory note's name or body holds any of it, less a URL's query and the `.netrc` rule, which suit an error and not a note. **Logs are rendered at the sink**, by `logging.Init`'s handler, so an ordinary `slog.Error("…", "err", err)` is correct and the loggers we don't own (beehive's verdicts, client-go, oauth2) are covered too. **A value that is neither string nor error is logged as its type** (`<unrendered pkg.T>`) — the renderer cannot read inside it and the encoder would write every field, so a caller who wants the contents renders them and logs a string. **A message that is persisted rather than logged is rendered where it is recorded** — `supervisor.Fail` and `kubesync`'s store failure — because a condition message outlives its log line and is served to the UI. GraphQL and gRPC auth projections omit tokens. `TestAuthProjectionCarriesNoTokens` pins the GraphQL fields. OAuth credentials necessarily cross the network to the issuer.
- **Restored identity is display-only.** Login verifies the ID token; startup decodes the stored token without verification. `UnverifiedIdentity.DisplayOnly` returns an ordinary `Identity`, so review must keep identity and the local `Authenticated` flag out of authorization decisions. The cloud verifies access tokens independently. The loopback callback checks state before consuming a code or error (`TestLoopbackRejectsInvalidCallbackWithoutConsuming`).


## The login shell (`internal/loginshell`)

`app.New` runs the user's login shell **once per launch**, on macOS and Linux (`launchShell` in
`app/launchshell_unix.go`; `launchshell_windows.go`'s runs nothing), **in the sandbox** (below). On
Linux with no sandbox it is not run at all (`skipResolution` in `launchshell_other.go`), since
nothing reads its answer there; on macOS with none it runs unconfined and logs that once. `loginshell.Resolve` answers a `Result` with
two readers: `Path`, the shell's `PATH` split and unfiltered, which the app hands to the sync
(*Security settings*, below), and `Env`, the allowlisted environment, which `setShellEnv`
sets process-wide on macOS (`launchshell_darwin.go`; a no-op in `launchshell_other.go`). A GUI launch
inherits launchd's minimal environment, so without it a kubeconfig `exec` credential plugin
(`aws`, `gke-gcloud-auth-plugin`) is not found even though the same kubeconfig works in a
terminal — and an exported `KUBECONFIG` or `AWS_PROFILE` is invisible. It is installed
process-wide because Go resolves a command name against the process PATH. `loginshell.Path` is
`Resolve`'s `Path` alone, the refresh's reader, its error the `*Fault`, which is an `error`
naming its reason. The package is `//go:build unix` throughout, so nothing outside a Unix file
imports it: the app takes the refresh's resolver from `app/shellpath_unix.go`
(`shellPathResolver`, over `In` with the sandbox's `Never` list).

**The shell is the account record's** (`accountShell`), never `$SHELL`: `dscl /Search -read
/Users/<name> UserShell` on macOS, `getent passwd <name>` then the user's line in `/etc/passwd`
elsewhere, each through `lookupCommand`, which a test points at a fake. A record naming no
executable by absolute path falls back to the platform's own (`/bin/zsh`; `/bin/bash`, then
`/bin/sh`), else `no shell`. Finding it stats a file and reads the account, which can block on
a dead network mount past any cancel, so `Resolve` runs it on a goroutine and answers `timeout`
at its deadline (`findShellOrTimeout`). **The command follows the shell's kind**, by base name: `nu` runs
`-l -c` with two frames (the cwd, then `$env.PATH | str join ":"`), and any other shell `-i -l -c`
with the posix command below, so a nushell run answers `Path` and an empty `Env`. **The
environment is scrubbed**: `HOME`, `USER`, `LOGNAME`, `TMPDIR`, `LANG` and `TZ` copied when set,
`SHELL` the shell run, `TERM=dumb`, `DISABLE_AUTO_UPDATE=true`, and `PATH` at
`loginshell.DefaultPath`, the platform's login default, so a terminal launch and a Finder launch
resolve the same list. `Launch` takes its arguments and environment from its caller; the bash
tool's snapshot passes `InteractiveLogin` and `ProcessEnv`, the process's environment, as before.
`Find()` stays for the bash tool, which picks the shell a command runs in.

**Every run of the login shell goes through a `Start`**, which builds the command: `Launch`,
`Resolve` and `Path` all take one, and `Launch` sets the pipes, `Setsid` and the kill on what it
returns, then calls its cleanup once the shell has exited. `Launch` never sets the command's
`Env` or `Dir`. **`loginshell.In(sb, deny, kstackDirs, tmp)`** (`sandboxed.go`) is the one place
that builds the login shell's policy, over a `Commander` (`*sandbox.Sandbox` or a test's fake;
every caller converts a nil pointer to a nil interface first): a Read of `/` and of this
executable (a run's private `/tmp` would hide one there), Kstack's three directories closed,
`deny` as the denied-always list, one write — the `TMPDIR` `tmp` makes for the run, removed by
the cleanup — and no network. **`deny` follows where the output goes**: the launch resolution
and Refresh PATH pass `sb.Never(home)`, since their answer leaves the sandbox; the snapshot
passes nil, since its answer reaches only commands that read the home themselves. The
environment loses every name `sandbox.Unpassable` matches, `SSH_AUTH_SOCK` included, and
`TMPDIR` is replaced. **The `TMPDIR` is a sandboxed run's** (`bash.TempDir`, a
`loginshell.TempDir`): a `<pid>-*` folder under `<cache>/tmp` (`Paths.TmpDir`), taken under the
sidecar's lock so the sweep never removes it mid-run, seeded by `sandbox.SeedTmpDir`, removed
through `rootdir.RemoveAll`, and swept with the bash runs' when a sidecar is gone. With a nil
`sb` the `Start` is a plain `exec.Command` in the home with the environment as given. **A failed
start is a `Fault`**: `timeout` when the context has ended, whatever the error (Darwin's
`Command` answers `ctx.Err()` while it builds the profile), `no scratch` when the `TMPDIR` could
not be made, and `sandbox refused` otherwise, so the shell never runs unconfined on a machine
whose sandbox answered the probe. → [ADR: the login shell runs in the sandbox](../docs/adr/2026-10-04-the-login-shell-runs-in-the-sandbox.md),
[security record](../docs/security/2026-10-04-the-login-shell-in-the-sandbox.md).

**The allowlist is the security boundary.** `imported` names every variable and its kind, the shell
command is built from that list, and adding a row is a security change: we spawn credential plugins
as children, so an import is an import into them. Deny by default, no prefix wildcards, and nothing
that is itself a secret. → [ADR: shell-environment allowlist](../docs/adr/2026-09-07-shell-environment-allowlist.md).

| Kind | What it means |
| --- | --- |
| `plain` | copied through as-is |
| `path` | one filesystem path, resolved |
| `pathList` | `:`-separated paths, each resolved, survivors rejoined |

The list is `PATH`, `KUBECONFIG`, the AWS profile/region/config variables, `CLOUDSDK_*`,
`GOOGLE_APPLICATION_CREDENTIALS`, `AZURE_CONFIG_DIR`, the proxy variables in both spellings,
`SSL_CERT_FILE`/`SSL_CERT_DIR`, and `OLLAMA_HOST` (an endpoint, not a credential) — read
`imported` for the current set and the reason beside each row; `TestImportedIsExactlyTheAllowlist`
pins every row.
The two `SSL_CERT_*` rows serve the children we spawn, not our own TLS: Go reads them in
`crypto/x509/root_unix.go`, which is not built on darwin.

Three rules resolve a `path` or `pathList` value, **in this order**:

- **A leading `~` passes through untouched.** First, because `filepath.IsAbs("~/.kube/config")` is
  false and the next rule would otherwise invent `/Users/me/~/.kube/config`. It reaches us only from
  a quoted `export`, so it is a literal the user's own `kubectl` also fails on.
- **A relative entry is prefixed with the directory the shell ended in**, which the shell reports
  itself in the first frame — a startup file may `cd`, so the directory we start it in is not the
  one `printenv` runs in. The entry's own components survive: `filepath.Join` would clean `link/..`
  away, naming a different file than the kernel resolves when `link` is a symlink. A cwd the shell
  could not report is no base, and the entry passes through as given.
- **An empty entry is dropped**, and a variable left empty is not installed at all.

`Launch` runs the shell with the flags its kind takes (three for posix: fish does not cluster
them) and one built command, started in the user's home, stdin at `/dev/null`, in a **session**
of its own, and reads stdout until the caller's `done(buf, from)` says the answer is whole. Nothing from a kubeconfig,
cluster data, or the socket may ever reach that command.

Traps worth knowing:

- **A session, not just a process group.** One kill has to reach whatever the startup files
  spawned, but the session is what keeps the shell off our controlling terminal: an interactive
  shell in a background process group is stopped by SIGTTIN the first time it touches the tty, and
  the answer never comes. A GUI launch inherits no terminal, so the hang shows up only when the app
  is run from one — `pnpm tauri dev`.
- **The marker must begin with a letter and the escape is `\000`.** `printf` reads `\0` as the
  start of an octal escape, so a marker starting `0`-`7` is swallowed into it and simply vanishes.
- **Resolution stops at the last marker, not at EOF.** `parse` takes the number of frames the
  kind prints. The shell's cwd leads, so N variables are framed by N+2 markers: one before `pwd`, one before each `printenv`, and one after the last. A startup file that backgrounds a daemon leaves it
  holding stdout; waiting for the pipe to close would hand it the power to stall every launch. The
  shell's exit status gates only when nothing usable was captured.
- **Each surviving entry is byte-for-byte.** A directory name may hold anything but NUL and the
  separator, newlines included, so only the single trailing newline `printenv` itself adds is
  removed and nothing else is judged. Empties are dropped and relatives are prefixed with the
  shell's cwd, never cleaned; nothing is appended or merged, and no Homebrew directory is added —
  the shell decides command precedence.
- **An empty frame is an unset variable**, indistinguishable from a variable set to the empty
  string, and both install nothing. `PATH` is the exception: missing, or resolving to nothing, is
  `bad output` — installing it would leave the process unable to find any command at all.
- **`printenv` exits 1 for an unset variable**, so each one is followed by `|| true` in case a
  startup file ran `set -e`. It is the only shell syntax beyond the original command, and it sets a
  floor: fish grew `||` in 3.0.

Ten seconds bounds the whole thing (`loginshell.DefaultTimeout`), after which the group is killed
and reaped. Any failure keeps the inherited environment and the stored `PATH` list, and logs one
warning naming a fixed reason —
`no shell`, `shell exited`, `bad output`, `output limit`, `timeout`, `no scratch`, `sandbox refused` — and never a value, the
shell's output, or anything else the user's environment holds; the reason reaches Settings as
`sandboxPathFault`. A success logs at `Info` how long the shell took, so the timeout can be
judged against real startup files, and on macOS the names it set. Falling back is not a
startup error. Resolution runs **once per launch**, and again only on Refresh PATH, so a change to
a startup file takes effect on the next launch.

Process-wide means process-wide: `internal/auth`'s browser opener resolves `open`/`xdg-open`
against the imported PATH too.

## Cluster subsystem (`internal/clustersvc`)

```
internal/clustersvc/
  service.go           Service + the four family interfaces, accessors, beehive bootstrap,
                       registerControllers
  clusters.go          ┐ one per family: its shapes, GraphQL binds, *WatchFrame,
  caches.go            │ controller, and the machinery that controller owns
  cachedkinds.go       │
  cacheddata.go        ┘ (no controller — the one family that isn't a beehive kind)
  clusterrows.go       the clusters table: ClusterRow and the reads and writes over it
  statements.go        every statement this package issues against app.db
  mirror.go            one runtime Cluster object per row; the only writer of its spec
  clustersources.go    the ClusterSource kind: one discovery anchor per source variant,
                       whose pass is the importer
  triggers.go          feed→wake bridge onto beehive names
  shared.go            shared vocabulary, the app's services as this package sees them, scalars
  stream.go            Stream[T], deltaWatch, sendFrame
  events.go            the one events read path
  internal/kubeconn/   connections and the five probes (leases, probe.go, service.go)
  internal/kubesync/   what fills a cache: arming seam, one session per cache, discovery.go,
                       kinds.go
  internal/kubestore/  one SQLite file per cache behind a refcounted manager; statements.go,
                       a file per table, the change ping bus
```

**A cluster is a row in `app.db`; beehive holds its runtime.** The `clusters` table owns a cluster's identity (`ClusterID`, a UUIDv7 from `appdb.NewID`), its source and source key, display name, three toggles, two stamps and the deletion mark. The mirror (`mirror.go`) keeps one `Cluster` object per unmarked row, **named by the row's id**, whose `ClusterRuntimeSpec` holds only the four fields the passes act on: source, source key, enabled, sync enabled. `runtimeSpecOf(row)` is the one converter and the mirror the one writer. A served record is `toCluster(row, obj)`: the row, plus the object's status and conditions when it has one; a row the mirror has not reached yet serves zero status. A cluster's object is found with `GetByName(string(id))`; its `ObjectID` stays internal, for the owner edges its caches hang off. `updated_at` moves on a user edit and never on a reconcile. → [ADR: a cluster is a row in app.db](../docs/adr/2026-09-16-clusters-are-rows-mirrored-into-beehive.md).

**Direction.** The leaves speak native vocabulary (GVRs, `rest.Config`, cache rows), never records; the controllers translate. A leaf importing a record type is an import cycle. Put a mechanism in a leaf, never in a controller: if `go test ./internal/clustersvc` stops being fast, one has leaked back in. → [ADR: one package over private leaves](../docs/adr/2026-09-30-the-cluster-service-is-one-package-over-private-leaves.md).

**The chain.** The `ClusterSource` anchor's pass is the importer: one `clusters` row per kube-context, inserted once per `(source, source_key)`, never updated or deleted — a departed context keeps its row and toggles, a returning one finds them. The mirror gives each unmarked row its runtime object; the cluster pass holds a `kubeconn` claim and folds what its probe found; the same pass creates the `ClusterCache` for the identity the probe recorded; the cache pass arms discovery and mirrors `kind_catalog` into `ClusterCachedKind` records; each kind record's pass arms that kind's sync. **kubesync decides what exists; the records decide what is mirrored.** → [ADR: beehive control plane](../docs/adr/2026-08-09-beehive-control-plane.md), [ADR: discovery as a beehive kind](../docs/adr/2026-08-18-discovery-as-a-beehive-kind.md), [ADR: arming is policy](../docs/adr/2026-08-28-arming-is-policy-never-interest.md).

### Controllers and passes

- A pass returns a verdict, never an error: `Settled()`, `Unsettled()`, or `Fail(err)`. `.RequeueAfter(d)` is for a wait this pass knows the length of; a cadence a kind depends on goes at registration. A no-op pass still settles.
- A status write is unconditional; beehive suppresses equal bytes. Don't add a guard in the pass.
- Only the values, never the timing. No timestamp or counter goes on a record's status; the steady state must be silent. → [ADR: two conditions, no timing](../docs/adr/2026-09-02-cluster-conditions-two-subjects.md).
- A parent controller creates the child kinds it owns, in the child kind's file, as one write with no read in front (`CreateOrUpdate(name, spec, WithOwner(parent))`, or `GetOrCreate` when the spec is the identity). Both refuse a deletion-pending row. A pass whose object or owner is deletion-pending writes nothing. **A cache carries `cacheFinalizer` from creation.** The cache pass's deleting arm clears it only after `ForgetCache` and `kubestoreMgr.Remove` succeed; a failed removal fails the pass with the finalizer intact, so beehive retries and the cluster's collection waits behind it. `WithFinalizers` needs the kind's controller registered in-process, which `New` does before any pass; a test that creates caches over a beehive with no controllers registers `finalizerClearingCacheController`.
- A relayed value needs a `depends_on` edge; the owner edge is not one. `clusterCacheController` declares `AddDependency(cluster)`; the rest of the chain relays into the child's spec, which is already a wake.
- All four controllers register `startupPass`. `ClusterSource` and `Cluster` also take a resync interval: each observes something the store cannot see move (a file, a remote server). A fold whose answer the store cannot see move owes a resync.
- Both reconciles defer with `Unsettled` until the kubeconfig has been read. **Keep those guards if you reorder startup**; the pre-read config is indistinguishable from a file with no contexts.
- A pass tells a kubeconfig row by `kubeconfigContextOf(spec)`, never by name. The import runs ahead of the fingerprint gate.
- **The mirror** (`mirror.go`) is a `lifecycle.Part` after the controllers. It subscribes to `KeyClusters` and the cluster kind's `WatchList` before its first pass, then passes on every row signal, every runtime `Deleted`, and a ten-minute resync. A runtime watch beehive ends (`ErrWatchTooOld`) is reopened, retried every second while the store refuses, with a pass once it is back; the rows and the resync drive passes throughout. A pass reads every row and object. An unmarked row gets `CreateOrUpdate(id, runtimeSpecOf(row))`; beehive suppresses matching bytes, so an unchanged row wakes no controller. A marked row's object is asked to go, and once it is gone the row is deleted (guarded on no chat referencing it) with `KeyClusters` notified and the row's source anchor requeued — the importer's insert is a no-op against a marked row, so a context that came back mid-teardown gets its row from that pass rather than the source's resync. An object with no row is torn down. A pass never touches `updated_at`.
- **Deletion is the mark.** `Clusters().Delete` marks the row, notifies and returns; the mirror and the chat sweeper do the rest, and the row goes last. It refuses a row its source still declares (`ErrDeclaredBySource`), reading the kubeconfig, or the runtime object's `IsPresent` while the file is unread; a row already gone or already marked succeeds before that check, so a repeat is never refused by a context that came back. The mark keeps the `(source, source_key)` claim, so the importer cannot re-create the context mid-teardown, and the toggles refuse a marked row as `ErrNotFound`. `AcquireConnection`/`RetryConnection`/`WatchSchedule` read the mark, the enabled toggle and the source off the **row** (`connectableContext`): the object is marked only on the mirror's next pass.
- The kubeconn trigger wakes the record holding a claim on the context that moved, through `clusterLeases.clusterFor`; a record's name says nothing about its context. A context no record is probed under wakes nothing.
- Neither cache controller writes a condition; the verdict is the gauge's. `Paused` is the user's field; the catalog owns the other four. Pause keeps the rows. → [ADR: kind records mirror the catalog](../docs/adr/2026-09-02-kind-records-mirror-the-catalog.md).
- Shared dependencies travel in `deps`, embedded by `service` and every controller. A new kind or service is a field, never a constructor parameter. Tests build the same struct via `newTestDeps` / `newRunningDeps` / `newRunningRegisteredDeps` (`testutil_test.go`).
- One lifecycle shape at every level: `lifecycle.StartCloser`, composed through `StartAll`/`CloseAll`. Add a participant as a named `lifecycle.Part` in the slice, never a stop closure. → [ADR: lifecycle composition](../docs/adr/2026-08-16-lifecycle-composition.md).
- `clustersvc.New(db, Paths{BeehiveDBFile, KubestoreDir}, kubeconfigSvc, pokeSvc)` grows a parameter only for a new process-wide service. The package only reads `kubeconfig.Service`; only the app closes it. Its statements (`statements.go`) are a `sqlstmt.Set`, the first `Part` so the reverse close order releases it last; tests build the same deps over a temporary DB (`newTestDepsOver`).

### Identity

A context is not an identity. `Connection` carries a set-once `serverUID`; `Lease.ConnFor(ctx, serverUID)` answers from it. **Never correlate a connection with `State.ServerUID`**: it is a separate probe's observable and lags a rebuilt connection by a round-trip. A second, different UID over one connection makes it vouch for nobody, and the conflict rebuilds the connection. `ConnFor` never waits; a run that cannot get a connection records `NoConnection`/`IdentityMismatch` and `Suspend`s. → [ADR: connection-carried identity](../docs/adr/2026-08-25-connection-carried-identity.md), [ADR: identity-driven retirement](../docs/adr/2026-08-27-identity-driven-retirement.md).

### Events

Three timelines, each `(ObjectID, category)`: `Cluster`/`connection`, `ClusterCache`/`discovery`, `ClusterCachedKind`/`sync`. Every pass writes unconditionally. One read path (`events.go`) by id alone. A nil `category` adds no option (the empty string is the default timeline, which answers nothing). `terminalErr` drops `ErrNotFound` and forwards the rest. `Event.id` is unique within one timeline; never hand one where an object id is expected. → [ADR: event timelines](../docs/adr/2026-09-02-event-timelines.md).

### Streams and watches

- Every send goes through `sendFrame` (`stream.go`). A bare channel send leaks the goroutine and the beehive watch once the consumer stops draining.
- One pump serves every record watch: `deltaWatch[Spec, Status, Frame]`. A kind supplies a `frame` projection, a `departed` builder, and a `bookmark` value. Never a fourth pump. The pump's rules are tested once in `stream_test.go`.
- A watch whose source can die returns `*Stream[T]` (`Frames` + `Err()`); `Err` is set before `Frames` closes. Gauges included. `NewStream` drops whatever a pump returns once ctx is done.
- A read reports the store as it is and never filters; a marked row is an ordinary `Modified`. **The cluster list watch rereads on two signals**, `KeyClusters` and the runtime kind's `WatchList`, because the record has two sources: a toggle moves a row and no object, a probe moves an object and no row. Both are subscribed before the first read; each reread joins rows and objects and folds through `deltafold`, comparing records as JSON bytes; `Watch(id)` filters that stream. A mark and a removal landing before one reread surface as the `Deleted` alone, so the mark is never promised and the departure always is. The frontend drops those rows once. → root `CLAUDE.md`.
- The gauges (`WatchStats`, `WatchHealth`, `WatchSyncStatus`, `WatchSchedule`) are read-side folds on a cadence, current-on-subscribe, no `Bookmark`, nothing emitted before the first measurement. `Caches().Health` and `SyncStatus` are one reading each of the first two, and `CachedData().ListObjects` is one kind's identity rows with no body — its `ok` is false for a cache the pair does not resolve to or one with no file, so a reader that wants a count never reads zero off nothing. The cluster card reads all three. Paused kinds resolve from the record ahead of every `GetKindState`; an unanswered kind is not an offender; `LastLiveAt` is the oldest proof. → [ADR: cache health fold](../docs/adr/2026-09-02-cache-health-fold.md).
- **A cache at its size ceiling stops syncing, and its record is what holds it there.** The janitor's `OverSizeLimit` is the only way in; the pass then writes `Synced=False` / `ReasonSizeLimit` and calls `ForgetDiscovery` through `armSync`'s existing switch. That closes the file, so the verdict is gone — while the condition stands the pass decides on bytes against `Stats.SizeLimitBytes` instead, and a failed measurement decides nothing. `readCacheHealth` reports the condition above every other arm, the one stored condition the fold reads. `clusterCacheClear` requeues the record itself (backoff reset), since a stopped cache holds no claim and `Manager.Clear` reopens nothing. Nothing evicts, so a stopped cache stays stopped until the user clears it. → [ADR: a stopped cache is held by its record](../docs/adr/2026-09-03-a-stopped-cache-is-held-by-its-record.md).
- `clusterScheduleWatch` reads the pool's cadence, never beehive's: the connection probe's next run alone.

### Families and the GraphQL surface

The four families are `Clusters()`, `Caches()`, `CachedKinds()`, `CachedData()`. The `Cached*` prefix marks the cache subtree; the `Data` infix marks content the store serves, as against control-plane records (`CachedKinds()` vs `CachedData().*Kinds`, `clusterCachedKindsWatch` vs `clusterCachedDataKindsWatch`). Method names are VerbNoun with the noun elided when it equals the family's subject. A family owns a read only when it differs per record type; `RetryConnection`/`AcquireConnection`/`ListEvents`/`WatchEvents` stay top-level. The scope is the entry point, never an argument: `Get(id)`/`Watch(id)`, `List()`/`WatchList()`, `ListBy*(id)`/`WatchBy*(id)`. Every family is asserted separately (`var _ Caches = cachesAPI{}`), in the resolver tests' fake too. → [ADR: one package over private leaves](../docs/adr/2026-09-30-the-cluster-service-is-one-package-over-private-leaves.md).

The schema **is** the Go shape: every GraphQL type binds 1:1 by name in `gqlgen.yml`; resolvers are one-liners on `r.ClusterSvc`.

- Every delta watch closes its snapshot with one `FrameBookmark` carrying a nil entity, which is why every `*WatchFrame` holds its entity by pointer. A per-cache watch sends it after the first read or the first bind that finds no cache; an unopened cache is empty, not pending. → [ADR: delta-watch protocol](../docs/adr/2026-08-09-delta-watch-protocol.md).
- Gauges are their own subscriptions, never a field on the record they describe. `clusterCacheSyncStatusWatch` is the only wire field carrying a per-kind verdict; its fold answers `Paused`, then `StoreFailed`, before the per-kind loop.
- Cache-data watches are keyed by cluster id + cache id and carry `cacheID` provenance (objects also `apiVersion`/`resource`).
- Point reads hang off the record that owns them (`Cluster.events`, `ClusterCache.kinds`, `Cluster.caches`, `ClusterCache.cachedKinds`). Every noun has the same root pair, `<noun>(id)` and `<nouns>(<parent>ID)`, the scope argument optional. Keep the shape when adding a noun.
- `Cluster.caches` is the set, never "the" cache; activeness is the live join on `status.server.uid` (`CacheIsActive`).
- **`Clusters().ReadActive(ctx, id, read)` is the one reading of a cluster at one identity**, for a reader that reads a cache's contents: it hands `read` an `ActiveCluster` — the record, and the cache `CacheIsActive` picks among the runtime objects, nil for a cluster never identified or one whose identity has no cache yet — then reads the identity again and runs the whole reading once more when it moved, since a cache read names its cache and never the identity. `read` may run twice, so it sets what it returns and never appends to it. A gone record is `ErrNotFound`, an identity that moves under both attempts `ErrIdentityMoved`, and `read`'s own error is returned as it is. The card and KubeQuery read through it. `Cluster.KubeContext()` is the kube-context a record comes from, empty for another source. → [ADR: the cluster service resolves a cluster](../docs/adr/2026-09-27-the-cluster-service-resolves-a-cluster.md).
- Mutations: `clusterConnectionRetry` is held open for the probe's round trip; `clusterCacheClear` takes the cache's own id, stops its workers, deletes the file, then requeues its kinds; `clusterCachedKindSyncEnabledSet` pauses one kind and keeps the rows; the three `cluster*EnabledSet` mutations each write one column with `UPDATE … RETURNING`, refusing a marked row; `clusterDelete` marks the row and returns. **Every cluster mutation returns through `clusterErr`** (`graph/util.go`): `ErrNotFound` → `KSTACK_RECORD_NOT_FOUND`, `ErrDeclaredBySource` and `ErrNotConnectable` → `KSTACK_CONFLICT`.
- `clusterEventsWatch(id: ClusterID!)` and `Cluster.events` are the cluster's timeline (`Clusters().ListEvents`/`WatchEvents`, which read the runtime object's log); `eventsWatch(id: ObjectID!)` serves a cache's or a kind's.
- Timestamps are nullable `Time` autobound to value `time.Time`; the delta-watch diff compares frames with `==`.
- A watch that dies reports why through `WatchFailureExtension` (`graph/watch_failure.go`). A resolver over a `*clustersvc.Stream` goes through `watchStream`, never `ptrStream`. → [ADR: watch-failure reporting](../docs/adr/2026-08-14-watch-failure-reporting.md).

`RawJSON` (`internal/rawjson`) is what the `JSON` scalar binds to — its own package because gqlgen binds one scalar to one Go type and more than one service serves a JSON field.

Types: `ClusterID` is `apimeta.ClusterID`, a string with its own `ClusterID` scalar; the `ObjectID` scalar carries a cache's or a kind's id. `RecordMeta` (`shared.go`) is the metadata half of the cache and kind records, embedded and autobound; `Cluster` carries its own (`ID`, `CreatedAt`, `UpdatedAt`, `DeletionRequestedAt`, `Conditions`), since its id and stamps are the row's. `ClusterSpec` is the served projection of the row's choices; `ClusterRuntimeSpec` is what beehive stores. `ClusterCacheSpec.ClusterID` is the join key a client folds caches onto clusters by, and what `ListByCluster`/`WatchByCluster` filter on; the owner edge onto the cluster object is what GC cascades along. `ClusterCache.Spec.ServerUID` is the identity a cache mirrors; active-ness is not a field. Every condition is a liveness condition (`LiveCondition` is the only constructor); `Unconfirmed` is load-bearing on the wire. `Condition` aliases `beehive.Condition`. → [ADR: liveness conditions](../docs/adr/2026-08-09-liveness-conditions.md).

### The connection pool (`internal/kubeconn`)

A cluster is the only way to address a connection; the pool sits behind `clustersvc`. → [ADR: addressed by ClusterID](../docs/adr/2026-08-22-connections-addressed-by-cluster-id.md), [ADR: one connection per context](../docs/adr/2026-08-23-one-connection-per-context.md).

- `Acquire(contextName)` never fails and never waits. `Lease` is `Conn` / `ConnFor` / `State` / `WatchState` / `Departed` / `Release`. `Conn` never dials; a connection whose last probe failed is still handed out.
- `RetryAndWait` wakes all five probes and returns once the connection probe it asked for has finished (`LastRunAt` at or after the ask). Nothing cancels the run. → [ADR: retry resolves with its probe](../docs/adr/2026-08-30-retry-resolves-with-its-probe.md).
- A `Connection` carries `Dynamic`, `HTTPClient`, `Discovery` over one pool. `Discovery` gets its own `http.Client` with a timeout because client-go's discovery calls take no context.
- Every request but a stream carries an idle-read bound (`idletimeout.go`): progress, never a deadline; `watch=true` and `follow=true` exempt; a cancel reports `ErrIdleTimeout`. → [ADR: idle-read bound](../docs/adr/2026-09-02-idle-read-bound.md).
- The boundary gate (`AcquireConnection`/`RetryConnection`/`WatchSchedule`) answers `ErrNotFound` or `ErrNotConnectable` off the row's own state, never off whether the server answers.
- A claim outlives what it is a claim on; an unread kubeconfig is not a departure. `stateHub` publishes before `signalHub`.
- The pool publishes per claim on `WatchState` and per context on `Subscribe` (a `gobus/conflate` bus; `conflate`, not `WatchAcross`, which collapses a burst). `WatchState` delivers nothing on attach; pair it with `State()`. Every value is a level, never an edge.
- `State.Identity()` is what the probes last read; `Connection.ServerUID()` is what one connection vouches for. Both exist and answer different questions.
- `State.Phase()` and `State.Identity()` are the pool's readings; condition types, reasons, and `Inactive` are the record's vocabulary.
- `configureHTTP2Keepalive` is called from `New`, not the composition root.
- Five independent `Observation[T]`s: `Connection`, `Readiness`, `ServerUID`, `ServerVersion`, `Principal`. An `Observation` keeps its value through a failure; `LastSeen` dates the value, not the verdict. `Attempt` is one run at any stage; a zero `NextAttempt` means suspended, and why is `LastAttempt.Reason`. `Reason` is our own vocabulary styled as a condition reason, assigned when the attempt ends, spanning layers on purpose; free text goes in `Message`. Two traps: `NotFound` and `Unsupported` both arrive as a 404, and `Dynamic` returns `*apierrors.StatusError` while the raw endpoints leave only a status code. A `State` copy is shallow. → [ADR: probe engine](../docs/adr/2026-08-24-probe-engine.md).
- Reaching the server is one `GET /api`; empty `versions` is `ReasonMalformed`. The probe builds a connection; the pool retires one, on a changed fingerprint *or* no connection *or* a conflict. → [ADR: the connection probe dials /api](../docs/adr/2026-08-25-connection-probe-dial.md).
- Publishing is `OnPass`: `stateHub` carries every pass, `signalHub` only when the news changed. A conflict's rebuild wake is edge-gated on the news moving.

### The supervisor (`internal/supervisor`)

Kubernetes-free scheduling: a work queue, a level-triggered pass, a schedule derived from what the last run recorded. → [ADR: probe engine](../docs/adr/2026-08-24-probe-engine.md), [ADR: supervisor vocabulary](../docs/adr/2026-08-28-supervisor-vocabulary.md), [ADR: jobs and workers](../docs/adr/2026-08-28-jobs-and-workers.md).

- Two kinds of thing. A **job** runs, returns, and is quiet until due; a **worker** blocks until stopped or dead and reports while it runs. Every probe and discovery read is a job; the kind sync is the one worker.
- The registration name is the whole public identity. A `supervisor.Key[T]` states a name↔type pairing once; read through `keyConnection.From(snap)`.
- A `Result` is its schedule: `Succeeded` (interval), `Fail` (ladder), `Suspend` and `Skip` (wait for a `Wake`). `Succeeded().RequeueAfter(d)` can only bring a run forward.
- `WithStartConcurrency(n)` bounds what is *starting*: a job for its whole run, a worker until `Ready`.
- `JobPass.Commit` is buffered and applied on return; `WorkerPass.Commit` is applied at once, so a worker's `T` is what a reader reacts to, never what arrives. Commit only on a change; the supervisor never compares.
- `Known()` is has-ever-answered; use it for a probe whose zero `T` is an answer.
- The supervisor hands back every value it stops holding, through `Discard(T)`, outside its lock. A commit's replaced value is not handed back.
- A worker: `Ready` means started, not proven; a worker that never calls it is recorded `NeverReady`; a stop records nothing; the ladder paces failures and the floor (`WithInterval`) paces clean restarts.
- `Wake` never tears a live worker down; `Restart` does. Neither waits. `Remove` and `Close` wait and must not be called from inside a `Run`. A watch edge onto a worker is a `Restart`.
- A body that panics or returns the zero `Result` is recorded `Internal`; the supervisor logs it through `slog` and nothing else logs.
- `Suspended()` is the narrower read (nothing due, nothing running, a suspension last); gate a revival wake on it, not on `Scheduled()`.

### The sync engine (`internal/kubesync`)

Speaks cache ids, contexts, server UIDs, GVRs; never records. Its dependencies are `Acquire(contextName)` and `OpenOrCreate(cacheID)`. A `lifecycle.Part` between `kubestore` and beehive. `Start` refuses a second start. It subscribes to `poke` for `RestartAll`. `withKindSync` substitutes the kind worker in arming tests.

- A clear runs inside kubesync: `RunWithCacheSyncStopped` / `RunWithKindSyncStopped` take `armMu`, stop and join the workers, run `fn` once, re-arm. The store work stays with the caller. `fn` must not call back into the Service.
- Two levels of arming that AND: `TrackDiscovery` (and it supplies the session) and `TrackKind`. A kind's registration outlives its cache being forgotten. `ForgetDiscovery` is a pause; `ForgetCache` is a teardown. Forgetting is synchronous.
- Nothing syncs into a cache whose connection does not vouch for its `ServerUID`; the session's connection bridge brings both levels back. It wakes off the session's own waiting marks, never the supervisor's snapshot: `session.connFor` marks a run before it asks the lease and clears the mark only when the answer is yes, so a connection arriving while a run is still deciding to park still wakes it. A caller that turns a refused gate into a `Fail` rather than a park clears the mark itself, which the sweep does and the kind sync — suspending under every gate error — never needs to. Each mark carries the reason its run recorded, so the bridge asks two questions on a frame that still refuses: whether the sweep's settled verdict outlived the connection it was drawn over, and whether anything at the gate names a cause the pool has since replaced. A kind's reason is its own — never inferred from the sweep's, which can already say what the frame says while a kind that was never asked still names the older cause.
- A cache whose file will not open reports `StoreFailed` via `Service.storeFailures`, which holds exactly the caches whose most recent arm failed.
- A verdict is a gauge, never a stored condition. `false` from `GetDiscoveryState`/`GetKindState` means nothing observed yet, not empty.
- News is not data: two feeds, one per level; the reader answers by re-reading. A kind is keyed by `(APIVersion, Resource)`; the singular is data.
- Every walk over `s.tracked` that ends in a `Remove` snapshots under `s.mu` and acts outside it.

**The sweep** (`discovery.go`): three jobs per cache. Writes only on a changed fingerprint. Four filters, none optional; `notMirrored` is the explicit drop list. `Partial` blocks the prune. `IsCRD` by (group, plural); printer columns by (group, version, plural), kept as a string on `KindRow`. Two wake loops: connection change and catalog change (whole sweep). → [ADR: discovery sweep rules](../docs/adr/2026-09-02-discovery-sweep-rules.md).

**The kind sync** (`kinds.go`): the run is the stream. `Ready` when the watch is open, never on a frame. Clean exit at the floor is a rotation; a close with nothing proved is a failure. **A window with no frame reopens the watch from the cookie inside the run**, since a collection the server keeps out of its watch cache (core events) sends no bookmarks: the verdict stays `Watching`, a frameless stream that stood the window stamps `LastLiveAt` at its open (never at the reopen, since an expired position is refused on the stream), each stream proves itself afresh, and `Stale` is a reopen that takes longer than the window. A reopen refused as expired relists as `Resyncing` inside the run, never a failure: etcd compacts on the window's own five minutes, so a cookie with no bookmarks to move it routinely expires. The cookie decides cold versus resume; an expired position relists as `Resyncing`. A resume commits only when its reason moved, except a run's first report. `KindState` is assembled at read; a run in flight speaks for itself and a last exit describes a kind only while it is down. A run lasts as long as its connection, and a retirement is read off the connection rather than off the context: `Retire` closes `Done` at once while the cancel it triggers waits on a goroutine, so a run can read an error over a dead connection with its own context still live — reported as the kind's own failure, that would put a kind the pool just moved onto the ladder. Every duration is a `pacing` field. → [ADR: kind sync verdicts](../docs/adr/2026-09-02-kind-sync-verdicts.md), [ADR: a quiet watch is reopened](../docs/adr/2026-09-27-a-quiet-watch-is-reopened-from-its-cookie.md), [ADR: an expired quiet reopen relists in place](../docs/adr/2026-09-29-an-expired-quiet-reopen-relists-in-place.md).

### The store (`internal/kubestore`)

One SQLite file per cache behind a refcounted `Manager`; a `Store` is a claim. → [ADR: one store per cache](../docs/adr/2026-08-26-cache-store-per-cache.md).

- Nothing on the read side creates a file. `OpenOrCreate` (writers), `OpenExisting` (the door to a cache's contents), `Subscribe` (borrow a feed, no claim), `Clear`, `Remove`, `Stats` (no claim, read-only open), `WatchOpen`. A claim is bound to the file it opened; a `Clear` swaps it and holders answer `ErrClosed`.
- `ForgetCache` before `Manager.Remove`; `ForgetKind` before `Store.ClearKind`.
- Every write takes a strictly increasing stamp; a relist reconciles by mark and sweep on `updated_at`, never `generation`.
- Every row carries `write_seq`; the stamp moves only when `resource_version` does, and unchanged means unchanged in full. `objects.changed_at` is that write's time, under the same rule, the first list counting as a change. Every delete logs to `deletes` first, in the same transaction; a row leaving a kind logs one too. A reader applies deletes before writes. → [ADR: write positions and the deletes log](../docs/adr/2026-08-30-write-positions-and-the-deletes-log.md).
- Core `v1` events go to the `events` table, routed by api version and plural. Nothing ages them out.
- Bodies are sanitized on the way in; redaction is the `redactions` table keyed by (api group, Kind), looked up on the body's own apiVersion and kind. Nothing derived from a secret is ever stored. → [ADR: secret redaction](../docs/adr/2026-08-30-secret-redaction-at-write-time.md).
- Every statement is named in `statements.go`, a `[]sqlstmt.Statement` of `OnWriter`/`OnReader` prepared by `openFile` into the file's `set` — no `OnBoth`: no read runs inside a write transaction, and adding one is a design decision, not a flag flip. A write transaction is `f.set.InTx`; a read that pairs rows with a position is `inReadTx` (`reads.go`), so both come off one snapshot. Collections bind as one JSON argument; the prune uses `RETURNING` and drains it. Reads ride their own `query_only` pool. `sqlitepool` owns the open contract, and `sqlitemigrate` applies the migrations. → [ADR: SQL discipline](../docs/adr/2026-09-02-kubestore-sql-discipline.md), [ADR: one statement set](../docs/adr/2026-09-16-sqlstmt-prepares-a-services-statements.md). `chatsvc` keeps the same discipline over `app.db`, with the reads its sends run inside a transaction declared `OnBoth`.
- **Every SQLite file is owner-only.** `main` sets the process umask to 0o077 (`umask_unix.go`), so a new file is born 0600; `sqlitepool.OpenWriter` pings and then chmods the database and its `-wal`/`-shm` siblings, which is what catches a file an older build wrote at 0644, then rewrites a file that predates the DSN's `auto_vacuum=INCREMENTAL` (`PRAGMA auto_vacuum=INCREMENTAL; VACUUM;`), since SQLite ignores the pragma once a table exists and a janitor's `incremental_vacuum` would be a no-op on it forever. The host creates the three directories 0700 before spawning us. Both are pinned by tests. None of it applies on Windows, where the inherited `%LOCALAPPDATA%` ACL is the whole protection. → [ADR: the profile ACL protects the Windows files](../docs/adr/2026-09-02-windows-cache-files-rely-on-the-profile-acl.md).
- One janitor per open file: freelist-gated bounded vacuum, per-kind deletes trim marks, nothing waits under `m.mu`. Zero `Interval` runs none. It sweeps on its interval **and** on every commit — `file.notify` wakes it through a capacity-one channel, so a burst owes one sweep. → [ADR: janitor](../docs/adr/2026-09-02-kubestore-janitor.md).
- **`Retention.SizeLimit` is the cache's ceiling** — the three files summed, judged at the end of each sweep, after the vacuum and after a checkpoint when the WAL outweighs the file. Soft by one interval: a sweep is what notices. The verdict is a tri-state memo on the `file` (`unknown` is not `under`, so the fresh file a `Clear` swaps in still reports its first answer), published as an edge on `WatchSizeLimitNews`. `Stats.OverSizeLimit` reads that memo and never recomputes; it is false for a cache nobody has open and for a manager with no janitor. The cache pass is what acts on it. → [ADR: bound the cache by total size](../docs/adr/2026-09-03-bound-the-cache-by-total-size.md).
- All-key tables are `WITHOUT ROWID`. Nothing has shipped, so a form change edits `0001_init.sql`. → [ADR: schema edit, not migration](../docs/adr/2026-08-29-schema-edit-not-migration.md).
- **A Pod's containers are rows written with the Pod** (`containers.go`), as its labels are: `projectObject` reads them off the sanitized body — every side table read from a body is read from the sanitized copy, since a table a query reads must hold nothing redaction removed — and `insertObjectRow` rewrites them, a DELETE and one JSON-bound INSERT, for a core `v1` Pod alone (`isCorePod`, off the body's own apiVersion and kind). `cascadeTables` and `ClearKind` take them with the Pod. Init containers come first, each at its `position` in its own list; `sidecar` is an init container whose `restartPolicy` is `Always`; each status joins by name within its list; requests and limits go through `parseQuantity` (`quantity.go`). → [ADR: KubeQuery reads tables written with the object](../docs/adr/2026-09-27-kubequery-reads-tables-written-with-the-object.md).
- **An object's references and Pod selector are rows written with it**, the same way, off the sanitized body and the body's own group and kind. `refs` (`refs.go`) is one row per by-name reference in `refPaths` and `podSpecRefs` — the Pod spec in a Pod, a workload's template or a CronJob's job template — walked by a small path form where `[]` walks an array: `path` names the field holding the name, or the whole reference object for a key reference, `claimRef`, `roleRef`, a subject or `scaleTargetRef`; a target in `clusterScoped` has no namespace, and any other is in the referrer's unless the reference names one (a RoleBinding's subject without one takes the binding's, as the authorizer does); `optional` is read only where a reference can carry it. `selectors` and `selector_terms` (`selectors.go`) are a Service's map through `labels.SelectorFromSet` and a workload's, a PodDisruptionBudget's or a NetworkPolicy's `LabelSelector` through `LabelSelectorAsSelector`, each requirement a term (`In`, `NotIn`, `Exists`, `DoesNotExist`, `vals` a JSON array). **A selector that is not selectable writes no row** — read the flag `Requirements()` returns, never the empty list, since a nil selector has none and selects nothing — and neither does a `policy/v1beta1` PodDisruptionBudget's `{}`; any other selector with no requirements is a row with no terms. `insertObjectRow` rewrites the three tables only for a kind that can have rows (`refersByKind`, `selectsByKind`), as it does containers for a Pod; `cascadeTables` and `ClearKind` take them with the object.
- **KubeQuery reads `Store.Query`** (`query.go`), on each file's query pool (`sqlitepool.OpenQuery`, 2 connections): `mode=ro`, through `kubestore`'s own `sqlite.Driver` so `body()` and the views reach query connections alone, and no idle connection. `body()` inflates a blob at most 16 MiB (`decompressRawUpTo`) and fails the statement past it: a statement can hand it any blob, SQLite checks the length only of a value already built, and a Go callback is not interrupted. `quantity()` is a text through `parseQuantity`, a number as a real, and NULL for anything else; `parseQuantity` refuses text past 64 bytes or an exponent past ±1,000 before it parses, since `ParseQuantity` builds a power of ten as long as the exponent. No function a statement can call parses a body. A connection hook creates the views (`query_views.sql`) as `TEMP` views, which shadow the tables: they are the contract the tool's prompt states. `selects` is each selector beside each Pod of its namespace no term fails; `refs` is each reference beside `to_uid`, its target when the cache holds it, and `to_listed`, whether the target's kind has its cookie — `cookie/<api_version>/<resource>`, which `cookieKey` spells too, on disk exactly while a completed list is — so a null `to_uid` is a missing target only beside a 1. **The target lookup is a `CROSS JOIN` from the catalog**: the files carry no statistics, and left to itself the planner walks every object of the namespace per reference (`TestTheRefsViewFindsATargetByItsIndex`). `queryConn` sets no attached database and a 16 MiB length limit. The statement runs wrapped, `WITH q AS MATERIALIZED (SELECT * FROM (<sql>\n) LIMIT n+1) SELECT * FROM q`: **the wrap is prepared first**, since SQLite applies some pragmas while preparing and a subquery holds none; then the statement alone, so a trailing `/*` cannot hide the wrap's tail; a `;` left after a trailing one is refused. Materializing runs the whole statement in its first step, the one modernc interrupts — `rows.Next` never looks at the context. A statement SQLite refuses is a `*QueryError`, a syntax error's message naming what runs (`onlyOneQuery`). A query registers on its file under the manager's lock (`startQuery`); the file's `close` cancels its queries and waits for them before the pools close, so a clear interrupts one rather than waiting it out. → [ADR: KubeQuery reads views on a connection of its own](../docs/adr/2026-09-27-kubequery-reads-views-on-a-connection-of-its-own.md).
- The change signal is a coalesced ping per kind or on the events bus, never a row delta. Closing the store closes the bus. → [ADR: ping bus](../docs/adr/2026-08-26-store-change-ping-bus.md).

**Cached-data watches** (`cacheddatawatch.go`): subscribe, snapshot, `Bookmark`, then one read per debounced burst. Objects and events read past a `Cursor` (a position **and** the Kind it was read under); the kinds watch re-reads and diffs. A cursor below the trim mark or under a different Kind goes back to the full read. A failed re-read retries in place. A cache that goes away ends the watch cleanly (`Err()` nil); any other open failure is a fault, never an empty cache. No read loads a body; `Store.ObjectBody` is fetched per `Added`/`Modified` row and a body that will not load is a null field. → [ADR: cached-data reads](../docs/adr/2026-08-26-cached-data-read-loop.md), [ADR: objects read split](../docs/adr/2026-08-29-object-read-split.md).

## Models (`internal/llm`)

> **This code is being rebuilt, and this section is the spec for it.** The model code was
> removed for a rewrite and is being rebuilt into four layers, `llm` → `tools` →
> `agent` → `chatsvc`. What exists:
>
> - `Dialect` (`dialect.go`): `messages`, `responses`, `chatcompletions`, `fake`, named for
>   the API and never the vendor. `Dialects` is the list, and the GraphQL `LLMDialect`
>   enum binds member by member onto it.
> - **One package, grouped by file prefix**: `dialect_*.go` is one wire, as one function
>   (`streamMessages`, `streamResponses`, `streamChatCompletions`, each
>   `(ctx, req, idle, emit)`), and those and `helpers.go` are the only files of `llm` that import
>   a model SDK (the boundary, below); `provider.go` is
>   the `Provider` type alone, since the rows are `catalog`'s. **Inside a wire's file, every helper is a
>   method on the wire's type** (`messagesWire`, `responsesWire`, `chatWire`: one request's
>   view of the API, holding what the helpers read of it, the provider id and whether the
>   request thinks), so `tools`, `conversation` and `failure` exist on every wire without
>   colliding, and every other top-level name in the file carries the wire's word
>   (`messagesToolInputs`, `responsesSection`, `chatCalls`). `New(providers...)` holds the
>   providers it is given, in order; `catalog` builds them. **`llm` names no vendor and no
>   tool.**
>   **A provider's dialect is the name that selects the code**: `Target.Stream` is one
>   `switch` on it, and `spoken` beside it is what `Resolve` refuses by. No interface, and
>   no code on a provider — except `Provider.fake`, the `Fake` a test steers, set by
>   `FakeProvider(f)` alone and read back by `Provider.Fake()`. The fake stays in `llm` as its
>   own test double: a dev build and a test each build a `Fake` and hand `FakeProvider(f)` over,
>   the dev build's paced at `FakeChunkDelay`.
>   → [ADR: the llm package wires itself](../docs/adr/2026-09-21-llm-is-one-package.md).
> - **The effort is sent on every wire; the disclosure is drawn where the provider sends
>   one.** `Resolve` checks the effort against the model and carries it as `Target.Effort`,
>   which rides the request as `Request.Effort`; a wire asks for thinking exactly when it is
>   set, so a model that lists no efforts is asked for none. Messages sends `thinking: {type:
>   adaptive, display: summarized}` with `output_config.effort`; Responses sends `reasoning:
>   {effort, summary: auto}` and leaves `Include` unset (the `encrypted_content` that arrives
>   anyway is dropped with its item); **Chat Completions has no field to ask with**, so it
>   sends `reasoning_effort` alone and reads what a vendor volunteers off
>   `delta.JSON.ExtraFields`, `reasoning_content` or `reasoning`, the first non-empty one
>   (raw JSON, decoded; DeepSeek's `null` beside each text delta is nothing). No entry on that
>   wire has been checked, so a model there can take an effort and show no thinking.
> - **A `Chunk` is of one `Kind`**, `ChunkText` (the zero value) or `ChunkThinking`, and
>   `block.go` holds the stored kinds: `BlockThinking`, `ThinkingBlock`, `AnswerBlocks(thinking,
>   text)` (each left out when empty) and `Thinking(blocks)`, the one reader, joining with
>   `ThinkingSeparator`, a blank line; and the round's own, `BlockToolUse`
>   (`ToolUseBlock(id, name, input)`, the model asking) and `BlockToolResult`
>   (`ToolResultBlock(id, text, isError)`, the sidecar answering by id). Every field past
>   `Type` and `Text` is `omitempty`, so a stored text block's bytes are unchanged.
>   `ToolUseBlock` panics on input that is not valid JSON — a caller's bug, never the
>   model's — which is why a `[]Block` always marshals. A wire forwards chunks in the order
>   received and **assembles the record in the reply's order**, one thinking block per
>   provider item, since a payload must sit where the wire placed it. `helpers.go`'s
>   `sectionJoiner[K]` puts the separator before a new section's first text, never before the
>   first section and never for an empty delta, so the streamed summary reads as the stored one
>   does: a section is a block index on Messages and an `(item_id, summary_index)` pair on
>   Responses, since that index restarts per item, and `Thinking` joins the blocks the same way.
>   `Prompt` leaves a thinking block out: nothing replays what was shown of it. **`Text(blocks)`
>   is the reader's view**: the text blocks, adjacent ones joined as they stand and a blank line
>   across a tool round or a server call, the rule the webview's `textOf` follows.
> - **A block can carry the provider's own item, verbatim: its `Payload`.** With thinking on, the
>   Messages API wants a round's signed thinking block back exactly as it came and the
>   Responses API its reasoning item with the encrypted content, so a thinking block keeps the
>   item it was read off, a `tool_use` keeps an item that holds more than the app's fields, and
>   a wire replays a payload where it has one. **The writer is the message's**: `Message.ProviderID`
>   is the provider its run was on, and `stripForeign(providerID, m)` is the blocks as any other
>   provider may send them, every app field kept and the payloads gone, since a signature is
>   verified only by the API that signed it. A message naming no writer is foreign to everyone.
>   `Message.Effort` is the level the run recorded, which is how a wire knows whether the row
>   thought. `toolInput(raw)` is what the wires keep of a call's arguments, the object trimmed
>   or nothing: a call the cap cut off inside them is stored `{}` and answered not-run, one that
>   ended normally with arguments that are not an object fails the reply, and **a call whose
>   arguments were repaired carries no payload**, since the payload would resend them. **A
>   payload is always a JSON object** (`Block.replayable`): a wire fails a reply carrying
>   anything else as unreadable, and `Target.Stream` sends a stored message holding one as
>   foreign to every provider (`disownUnreplayable`), since a row's payloads pair up and an API
>   refuses a pair with one side missing. It logs that at `Warn`, by message index and block
>   type.
> - **A payload never leaves the sidecar.** `WithoutPayloads(blocks)` is what a reader is shown, a
>   slice holding none coming back as it is. `chatsvc` applies it in `Progress`, before the
>   overlay's marshal, and in `forReader`, which every row leaving the service passes through:
>   `readMessages` behind the watch and the query, and the answer a retried send returns.
>   `history` reads `listMessages` directly and keeps the payloads for the replay. The webview
>   never sees a `payload` key.
> - **What the wires share is `helpers.go`**: the idle guard every stream runs under, the
>   section joiner, `providerOptions` (below), the two OpenAI-SDK wires' `openAIOptions` — a
>   client from the provider alone (base URL, key, retries off, then `providerOptions`; an empty
>   key sends no `Authorization`) — and `openAIFailure`, the SDK's error read into an
>   `llm.Error` the way the Messages wire's `failure` does. `Prompt` (`block.go`) is a
>   message as a wire that takes one string reads it.
> - **A provider carries four lines of data every wire applies**, each with the SDK's own
>   option through `providerOptions`, so a vendor's quirk is a line on its row, never a branch in
>   a wire: **`Extra`**, JSON paths set on the body (`WithJSONSet`); **`KeyHeader`**, the header
>   the key goes in instead of the dialect's own (`WithHeader`); **`Query`**, added to every URL
>   (`WithQueryAdd`); and **`Omit`**, JSON paths deleted from the body (`WithJSONDel`). A wire's
>   own options (`cacheOptions`, below) go after `Extra` and before `Omit`. `Omit`
>   goes last, so it deletes what the wire wrote too — all but `stream`, which each SDK sets after
>   every option — and a path the body lacks is a no-op. `Query` and `Extra` go in sorted order,
>   so a request is the same on every run. **The key goes one way only**: with `KeyHeader` set,
>   the Messages wire leaves `WithAPIKey` out and the OpenAI wires pass it empty, so neither
>   `x-api-key` nor `Authorization` rides beside the named header, and with no key no header goes
>   at all. OpenRouter's `Extra` is the one line a row sets.
> - **The Messages wire caches the prompt for an hour.** The APIs are stateless, so every turn
>   resends the system prompt and the whole transcript, and a provider bills a prefix it has seen
>   at a fraction. Anthropic caches only on request, so the wire marks two places with
>   `cacheHour` (`ttl: 1h`): the system block, a fixed read point every chat shares (under the
>   model's minimum on its own until the system prompt grows), and the request-level
>   `cache_control`, which the API places after the last block and moves forward each turn, so a
>   turn reads the prior transcript and writes only what the last one added. An hour, since a chat
>   is often picked up again within it and a long think would spend the 5-minute default; a
>   one-hour write costs more and a read the same.
> - **Every catalog entry states how it caches** (`Model.Cache`, a `CacheKind`): `CacheAuto`, the
>   provider caches on its own; `CacheMarks`, only what the request marks; `CacheNone`, its
>   provider documents no cache. No entry keeps the zero value
>   (`TestEveryModelStatesItsCaching`): every Anthropic entry and OpenRouter's Claude are
>   `CacheMarks`, Groq's Qwen entries `CacheNone`, the rest `CacheAuto`. **The Chat Completions
>   wire marks a `CacheMarks` model** with the request-level `cache_control` at one hour
>   (`requestMark`), and no other, since an endpoint that does not know the field may refuse it.
>   **A request carries an affinity key** (`Request.AffinityKey`, from the `Stream` argument):
>   the conversation's name, which a vendor that caches per machine routes by. A chat's turn
>   sends its chat id and a subagent its run id, since a subagent's prefix is its brief.
>   `Provider.AffinityRoute` says where it goes, a `Header` or a body `Field`: `mistral`
>   `prompt_cache_key`, `xai` `x-grok-conv-id`, `openrouter` `session_id`, `fireworks`
>   `x-session-affinity`, and nowhere on any other row. `cacheOptions` (`helpers.go`) spells
>   both for the Chat Completions wire. The Messages wire marks every request itself, and the
>   Responses wire sends no key (below). **`make check-cache` is how a row is confirmed**: with
>   real keys it sends each model two turns and prints whether the second read the cache
>   (`catalog/cache_live_test.go`, behind the `livecache` tag, which `make vet-go` vets).
> - **The conformance table is `dialect_test.go`**: each real dialect contributes one `wire`
>   fixture and every case runs once per wire — a reply streamed, an idle stream bounded
>   before and after its headers, a cancel honoured, a failure rendered without the body and
>   without a response, the context block ahead of the text, a turn's rendering a prefix
>   of the next's, the effort where that wire puts it, the record reading back as the stream
>   said it (`assertRecordReadsTheStream`, over a reply that thinks and one that thinks again
>   after its first text), the definitions offered as functions, a call read whole into the
>   record with no chunk carrying it, a malformed call that ended normally failing the reply,
>   a round's rendering a prefix of the next turn's, and a provider's key header, query and
>   omits sent the same way. A fixture carries the canned replies
>   (`text`, `thinks`, `thinksAgain`, `call`, `malformed`), readers of the conversation, the
>   effort and the offer off a recorded body, the dialect's own key header (`ownKey`), and a
>   body path the wire always sends (`sent`). The truncation arm is each wire's own case, since
>   each API says "cut off" in its own words. A later step adds a wire there, not a test per
>   dialect. The fake is not one of them: it has no transport, no body and no idle guard.
> - **Ten keyed providers**, in picker order: `anthropic` (Messages), `openai` (Responses), then
>   `gemini`, `groq`, `mistral`, `deepseek`, `xai`, `openrouter`, `together` and `fireworks`
>   (Chat Completions), each keyed by the vendor's own variable (`catalog.KeyVars()` is the
>   table). The rows are `catalog/providers.go`'s.
>   **Every catalog is written**, no discovery. **Five entries list efforts**, in the vendor's
>   own words, least to most, each with the default a picker starts on: `anthropic`
>   (`low`…`max`, not Haiku 4.5, which refuses adaptive thinking), `openai`, `gemini`, `groq`
>   and `xai`. The five whose ids are UNVERIFIED list none. A model states an
>   output cap where its wire requires one (Messages, Responses) or where the vendor states
>   one; on Chat Completions a zero sends none and the vendor's default applies. OpenRouter's
>   `Extra` is `provider.data_collection: deny`. No test reaches a vendor, so each provider
>   file says whether its ids were checked against `GET /models`.
> - The seam `agent.Run` reads: `Block` (the `text` and `context` kinds, with the stored
>   format's JSON tags; a `context` block is the cluster card, first on a question, which
>   every dialect sends as text ahead of the message's own, and the two tool kinds),
>   `Message`, `Request` (the `Provider` the call goes to, the `Model` entry, the system
>   prompt, the messages, the affinity key, and `Tools`, what the model may ask for), `Chunk` and `Response`
>   (`StopReason` is the provider's own word; `StopToolUse` is the one word the loop keys
>   on; `Model` is the model the provider said it served, empty when it said none — Messages
>   reads it off `message_start`, Responses off the final response, Chat Completions off the
>   first chunk naming one; `Usage` is what the request read and wrote: `Reported` says a usage object came, and
>   `InputTokens` is everything read, the two cache counts part of it, each wire adding its
>   API's own fields up — Chat Completions asks for them with `stream_options.include_usage`,
>   keeps the last report whichever chunk carries it, reads a hit as `cached_tokens` or, where
>   that is absent, DeepSeek's `prompt_cache_hit_tokens`, and a write as OpenRouter's
>   `cache_write_tokens`). A stream that fails reports none. The agent never builds a `Request`:
>   `Target.Stream(ctx, systemPrompt, messages, affinityKey, tools, native, emit)` builds it from
>   the target's own provider and model and switches on the provider's dialect.
> - **The offer is `ToolDefinition`s and `NativeOffer`s** (`request.go`). A `ToolDefinition` is a
>   tool of the app's, sent as a function: `Name` is what a `tool_use` block carries and the box
>   is looked up by. A `NativeOffer` is a vendor's tool: its `Tool` (an `llm.NativeTool`, whose
>   `Name` is its identity), `Server` when the provider
>   runs it, and `MaxUses`, its cap, at least 1 for a server tool and 0 for a client one. **Which models take tools
>   is the catalog's word** (`Model.Tools`): every Messages and Responses entry does, and on
>   Chat Completions, every vendor's wire, it is per entry (`deepseek-reasoner` and
>   `openrouter/auto` do not, nor does any Together entry until its listing is checked). The
>   fake's catalog carries `fake-no-tools` beside `fake`, so a consumer can test both arms.
>   `Target.Stream` refuses an offer the target cannot carry before anything is sent, as
>   `ErrToolsUnsupported`: any offer on a model that takes none, a native offer its target does
>   not `Take` or whose cap does not fit who runs it, and a request that spells a name twice — two
>   offers of one name, or one call name on the wire read from two offers (`distinct`).
>   **A request offering nothing replays no round**: `Stream` strips every tool block from the
>   history it passes on (`WithoutRounds`), since the Messages API refuses a history holding
>   them unless `tools` is defined, and one rule serves every wire. **A payload goes with the
>   round it belongs to**, wherever the rounds are dropped — here, and in `chatsvc`'s history
>   for a row that did not settle Complete — since the Responses API refuses a reasoning item
>   whose following call is gone, which is what a turn cancelled inside a tool would leave
>   behind. The text stays, being what a reader and the model see. `chatsvc` hands the box over only for a model
>   that takes tools, so the prompt's `# What you can do` and the offer agree.
> - `error.go`: `Error` is a failure as the record keeps it — the provider's id, then
>   `Status`, `Type`, `Code` and `Kind`, never a body's text, since a body can echo the
>   conversation and redaction knows the keys, not a prompt. `ResponseError` keeps what the
>   API said in its fields; `TransportError` is a failure with no response, which logs its
>   cause (a host, never a body) and renders its kind; `ReadError` is a reply that could not
>   be read, which keeps and logs nothing. `Code` is the client's classification
>   (`CodeContextLengthExceeded`, `ContextFull()`); `ErrStreamIdle` names a stream the
>   client's bound ended. `IncompleteError` is a stream that ended clean with no stop
>   reason, `io.ErrUnexpectedEOF` in its chain.
> - The catalog's types: `Provider` (`ID`, `Label`, `Dialect`, `BaseURL`, `Key`, `Catalog`,
>   the four lines above, and `AffinityRoute`) and
>   `Model` (`ID`, `Label`, `Efforts`, `DefaultEffort`, `MaxOutputTokens`, `ContextWindow`,
>   `Tools`, `Cache`; a window of 0 is unstated, and such a model is never refused ahead). The rows
>   are `catalog`'s (below); the fake's, `FakeProvider(f)`, sits beside its client in
>   `dialect_fake.go`.
> - `Service`: the providers, in the order given. `Providers()` is them in that order, which
>   is the picker's, and `Provider(id)` one of them. `Resolve(providerID, modelID, effort)` is
>   what a send runs on — one `Target`: the provider and the model — and refuses a
>   provider it does not hold, a model the catalog lacks, or a provider on a dialect
>   `Stream` does not speak (`ErrUnknownModel`), and an effort the model does not list
>   (`ErrBadEffort`; a model with no efforts takes the empty string alone). The `models`
>   query flattens `Providers()`, each model under its provider's id, label and dialect.
> - **A native tool spells itself on each wire it rides.** `llm` defines one interface per wire,
>   in that dialect's file
>   — `MessagesServerTool` in `dialect_messages.go`: the offer at a cap, the call name its
>   `server_tool_use` blocks carry, its result's block type, its share of the usage — and a tool's
>   package implements it (`tools/anthropicwebsearch/messages.go`). `llm` names no tool and imports
>   no tool package. `Target.Takes(tool, server)` is whether the wire has an interface the tool
>   implements: on Messages a `MessagesServerTool` run by the provider, no client tool on any wire
>   yet, and any tool on the fake. Which tools a provider is offered is the catalog's lists, never
>   `llm`'s. A later wire's tool is
>   an interface in its dialect's file and a method set in the tool, never a branch keyed on a tool.
> - **The SDK boundary**: inside `llm` a model SDK is imported by a `dialect_*.go` file or the
>   helpers; outside it, only by a tool's wire file named for its dialect (`messages.go`,
>   `responses.go`, `chatcompletions.go`) and that file's test, for the SDK's types alone — its root
>   package and `shared/…`, never `option` — and neither builds a client or a service. A client is
>   `llm`'s alone, since only `llm` builds one that reads nothing from the environment.
>   `TestOnlyAWireFileImportsAModelSDK` walks every file of the module against `sdkImportAllowed`,
>   and `TestNoDialectReadsTheSDKsEnvironmentDefaults` bans the OpenAI SDK's `NewClient` and
>   `DefaultClientOptions` in a dialect file and every `New…Client`, `New…Service` and
>   `DefaultClientOptions` of either SDK in a wire file, each SDK found by its import's own name.
>   → [ADR: every tool is in the box](../docs/adr/2026-09-24-every-tool-is-in-the-box.md).
> - **A server call is recorded, never run.** A server offer is a tool the provider may run at
>   its `MaxUses` for this request. A request
>   offering no server tool strips server calls and their results from the history and sends
>   cited text as plain text (`withoutServerCalls`); one offering no tool at all strips every
>   round. In the record a `server_use` block is one call, in a `tool_use`'s fields (`ID`,
>   `Name` its tool's name, `Input`), with the provider's call as its payload; a `native` block
>   is a provider item kept for the replay alone, a search result under its call's `ID`; a
>   text block that cites carries its provider's block as its payload, the one copy of the
>   citations, since the Messages API refuses a replay that lost an `encrypted_index`.
>   `WithoutPayloads` drops a `native` block whole. `Partial` folds a stream's chunks into
>   blocks as a wire would, so a `ChunkServer` (a call, sent once its input is whole) lands
>   between the texts around it. `Response.ServerUses` is the provider's count by tool name,
>   present only where the usage reported one; `StopPauseTurn` is a reply the provider
>   paused. `Citations(dialect, blocks)` reads the citations out of the payloads by the
>   dialect of the run that wrote them, never the row's provider.
> - `dialect_messages.go`, the Messages API over `anthropic-sdk-go` at `v1.72.0`. **The wire
>   names no server tool**: it asserts `MessagesServerTool` on each server offer (the offer, the
>   name its calls carry, its result's block type, its share of the usage), and a server block is
>   read under the name of the offered tool whose call name it carries, so one not offered is
>   kept out. A payload
>   goes back as its item, ahead of the plain-text arm, so a citation keeps its
>   `encrypted_index`; a server call with no result in its row is left out, except in the
>   request's last message, a paused reply being resumed. The count is read off the stream's
>   events, never the accumulated message, which cannot tell a reported zero from none. Text and tool rounds in, thinking, text and calls out: the client is built from the provider alone
>   (`WithoutEnvironmentDefaults`, retries off, its base URL and key), the system
>   prompt goes in `system`, a `context` block is a text block ahead of the question's, in
>   its `<context>` tags, a
>   `text_delta` is a chunk, and the response is the text blocks with the stop reason — any
>   other block kind is kept out and counted in the log at `Debug` by index, never by its
>   type, which is the reply's own text. The loop ends at `message_stop`, so a body held
>   open past it is not waited on. `newMessagesClient(idle)` bounds a quiet stream
>   (`idleGuard`, in the same file; `messagesIdle` a minute, generous next to the API's
>   ping cadence of a few seconds): every
>   event and every read off the wire resets it, since the SDK drops a `ping` before the
>   loop sees it, and the guard cancels with `ErrStreamIdle` as the cause, so its trip and
>   a caller's cancel read apart through `context.Cause`. A failure is mapped once: the
>   context's cause; an `*anthropic.Error` to `ResponseError` with the type kept only when
>   it is one the API defines (`errorTypes`) and the context-length refusal classified off
>   the message (read for that and dropped); a failure with no response — a `net.Error` or
>   a certificate failure inside the client's `*url.Error`, never a response the client
>   could not parse, whose text carries the response's bytes — to `TransportError`;
>   anything else, an error event that is not the API's shape included, to `ReadError`.
>   No stop reason is `IncompleteError`, an `io.ErrUnexpectedEOF` in the chain. A
>   `thinking_delta` is a thinking chunk, and each `thinking` or `redacted_thinking` block is
>   one record block carrying the SDK's accumulated JSON, signature included, as its payload. **A
>   call's arguments are read off the deltas** (`messagesToolInputs`, by block index), never off the
>   accumulated block: the SDK replaces input it cannot parse with `{}`, and `{}` is a
>   legitimate input a tool runs on its defaults, so only the deltas tell the two apart. A call
>   that carried no arguments at all, an empty piece included, is one made with none. **The
>   offer is the schema whole**: `type`, `properties` and `required` in the param's own fields,
>   every other top-level key through `ExtraFields`, so a `$ref` still finds its target; a
>   schema that is not an object fails the send, naming the tool. **An assistant row unfolds
>   into the wire's alternation** (`messagesWire.unfold`, a result the tool left empty going as a
>   `tool_result` with no content at all, since the API refuses an empty text block): each run
>   of `tool_result` blocks is a
>   user message and the blocks between two runs an assistant message, so a row without tool
>   blocks is one message and a turn's rendering stays a prefix of the next's. **The row holding the lastmost
>   round loses its tool blocks when it cannot supply a thinking block**: with thinking on the
>   API refuses the lastmost `tool_use` having none ahead of it, and neither a row another
>   provider wrote, its payloads stripped, nor one written with thinking off can supply one.
>   Its text stays, and the row is found by its blocks — a text-only answer after it does not
>   shadow it — while every earlier round goes as it was. The facts are the message's
>   `ProviderID` and `Effort`, never read off its blocks: a model that chose not to think
>   before a call is not a row written with thinking off. **A request that does not think
>   sends no thinking block at all**, formed or not. No caching yet.
> - `dialect_responses.go`, the Responses API. **The offer** is one function tool per
>   definition, the schema as written and `strict` explicitly false: left to the API a schema
>   is normalised toward strict, and strict wants every property required. **With an effort
>   set, `Include` asks for `reasoning.encrypted_content`**: `store` is false, so the item is
>   the only copy and a round needs it back. The reply is read in the order the API wrote it: a
>   reasoning item is a thinking block carrying the item as its payload, a `function_call` a
>   `tool_use` carrying its item when it says `completed`, and a completed response holding a
>   call stops on `tool_use`, the one word the loop keys on. **An incomplete response keeps its
>   own word**, its reason or the bare status, over any call it carries: it was cut off, so the
>   loop answers the call not-run. Whether unreadable arguments may be repaired to `{}` is the
>   response's word, never the item's, which a compatible endpoint can leave out. **The replay
>   walks a row's blocks** (`responsesWire.input`): a block with a payload is its item verbatim, a call
>   without one is rebuilt from the fields, a result is a `function_call_output` (no error flag;
>   the loop's refusals are `{"error":…}` text either way), and each run of context and text is
>   one message of the row's role, so a row with rounds replays as `reasoning, function_call,
>   function_call_output, reasoning, message`, the order the API pairs them by.
> - `dialect_chatcompletions.go`, the Chat Completions API. **The offer** is one function tool
>   per definition, the schema as written, `strict` unset: false where a vendor knows the field
>   and absent where it does not. `chatCalls` folds the `tool_calls` fragments into calls,
>   keyed by `index`, in the order they were opened. **Every fragment of a call counts**: each
>   appends its name and its arguments, and an id or a vendor's own key on any of them is kept,
>   since a vendor may stream the name in pieces and send the id, or a signature, after the
>   fragment that opened the call. A call sent with no id gets `call-<n>`, since the record
>   needs one to pair the result. **A call carries a payload here too**: Gemini hangs
>   `extra_content.google.thought_signature` on a call and wants it back on the same turn, so a
>   call the vendor put any key of its own on keeps the folded entry, the app's fields with the
>   vendor's beside them and no `index`, and replays as that entry. **A reply that finished
>   asking stops on `tool_use`**: `tool_calls`, the wire's own word, and `stop`, which a few
>   vendors say with calls present. Every other finish stands (`length`, `content_filter`, a
>   word of the vendor's own) and the loop answers a call under it not-run, so a call in a
>   filtered reply never runs. **The replay** walks an assistant row into this wire's
>   alternation: each run of text and the calls after it is one assistant message (`content`
>   left off when the run is empty), each result a `tool` message by `tool_call_id`. **A round
>   another provider wrote replays under ids of the wire's own** (`replayIDs`, as on every
>   wire — see *Errors and payloads* below), since Mistral validates `tool_call_id` as nine
>   alphanumerics and refuses another vendor's spelling. The reader here mints `call-<n>` per
>   reply, so one chat can hold two calls under one id, and the second is minted again; a
>   minted call that keeps its entry goes with the minted `id` in it. The reader keeps them apart on the way in too: a fragment
>   naming an id the open call does not carry opens the next one, since a vendor that sends
>   parallel calls whole may leave `index` off them all. A thinking block is not sent and does not split a run: nothing on this wire
>   goes back verbatim but a call a vendor signed.
> - `dialect_fake.go`, the other dialect: a thought then a sentence from a fixed bank, a word
>   per chunk, paced by `chunkDelay`. **Every reply thinks**, so a dev build draws the
>   disclosure with no key, and `FailAfter(n)` and the gate count chunks of either kind: "after
>   the first chunk" is inside the thought. `SetReply(chunks…)` stages the next reply exactly,
>   which is how a consumer's test gets one that thinks again after its first text; with no
>   chunks it stages a reply that streams nothing and holds no block. `FailAfter(n, err)` past a
>   reply's last chunk fails it at its end, so `SetReply()` with `FailAfter(0, err)` is a reply
>   refused before any chunk. **The fake has no idle guard**: a quiet turn is staged as
>   `FailAfter(n, ErrStreamIdle)`, what a wire returns when its guard trips.
>   **It stages tool calls too**: `SetToolCalls(StagedCall(name, input)…)` makes the next
>   reply ask for them and stop on `tool_use`, `RepeatToolCalls` every reply from now on
>   (nil stops it), and `SetStop(reasons…)` ends the next replies on those reasons, one
>   each, whatever they hold — with calls staged a call the cap cut off, with none a
>   malformed `tool_use` or a pause. `SetServerUses` stages each next reply's count,
>   `SetServedModel` each next reply's served model,
>   `SetUsage` each next reply's report in place of the fake's own (the request's words plus
>   3, cache read 2 and write 1, and the reply's words; the system prompt is not counted), and
>   `SetCitations` the next reply's citations, in a payload shape of the fake's own that
>   `Citations` reads back; a reply folds its chunks through `Partial`, so a staged
>   `ChunkServer` lands in place. The calls
>   ride after the reply's blocks, no chunk carries one, and the ids are `call-N` across
>   the fake's life; `StagedCallWithID(id, name, input)` keeps its own, so a reply can repeat an
>   id the way a provider must not. **`SetGate` holds the next reply alone** and is consumed
>   at the top of `Stream`, so a test that wants the second reply held arms the gate from inside a tool's
>   `Run`, the one point between the rounds it controls. It holds the reply after its first
>   chunk, or at its end when it has fewer than two, and **releases on close alone**: a value
>   sent on it panics, since it would let the whole rest of the reply through. A test knows the
>   reply is held by waiting on the live text. **`Route(prompt)` is a fake of its own**
>   for the requests whose first message ends in the text `prompt` — a subagent's, whose one
>   message is its brief — with staging and a bank of its own, so a test stages a subagent's
>   replies apart from its parent's, which ask at the same time; the root still logs every
>   request and mints every id. Every method of a `Fake` but `Stream` is for tests, and the
>   fake is the one scripted dialect there is — behaviour a consumer's test
>   needs from the wire is a knob here, never a second fake. `app.go` builds a `Fake` in a debug
>   build alone (`Config.AddFake`) and hands it, with `Config.LLMKeys` and `Config.LLMBaseURLs`, to
>   `catalog.New` (`newCatalog`), whose `Providers()` go to `llm.New`. **Its catalog is two models**: `fake`, which takes tools and
>   lists efforts, and `fake-no-tools`, which does neither, so a consumer can test what a model
>   that takes no tools is offered. The fake's tool list is the catalog's, and names the search.
>
> Every paragraph below describes the code as it was
> before the rewrite, kept as the list of invariants the rebuild has to reproduce and rewritten as each
> lands. A paragraph still in the old names is an invariant not yet rebuilt.

**Everything a model call needs lives here**, and **a model SDK is imported only where the
boundary above allows.** An investigation or an audit that needs a model calls `llm` the way chat does, and gets the
same cancellation, error rendering and key handling.

**Dialects are code, providers are data.** An `Encoder` is one wire, in the one Go file
that imports its SDK. A `Provider` is one endpoint as plain data: id, label, dialect, base URL,
key, how it is authenticated (`Auth`), catalog, and how the catalog is discovered. Two providers
can share an encoder, so adding a provider is a file of data in `providers/` (or, for a cloud
platform, its file in `platforms/`), never a change to an encoder. Four dialects: `anthropic` (Messages), `openai` (Responses), `chatcompletions`
(OpenAI Chat Completions — every local runtime's wire, and most cloud vendors'), and `fake`. **A
platform is not a dialect**: Bedrock, Vertex AI and Foundry serve the same three wires, and what
each changes is how a request is authenticated and where it goes — a `switch` on `Auth` in each
encoder's client builder, calling into `platforms`, which holds each platform's entries beside the
code that fills and signs them.
→ [ADR: the llm package wires itself](../docs/adr/2026-09-21-llm-is-one-package.md).

```
internal/llm/
  dialect.go                 package doc, Dialect
  service.go                 Service, New, Target (Stream: the switch on Dialect)
  provider.go                Provider
  model.go                   Model
  request.go response.go     Request, Message; Chunk, Response
  block.go                   the schema a message is stored in
  error.go                   llm.Error — what a failure is stored as
  dialect_messages.go        ┐
  dialect_responses.go       │ one function per dialect, each file the only importer of its SDK
  dialect_chatcompletions.go │ (the fake's provider sits beside its client here)
  dialect_fake.go            ┘
  helpers.go                 what the wires share: the idle guard, replayed call ids, a provider's options, the OpenAI client and error mapping
```

One package: `dialect_*.go` is code, the data is `catalog`'s, and
`TestOnlyAWireFileImportsAModelSDK` (`dialect_test.go`) is what holds the SDK boundary — it
parses every file of the module and fails any importer of a path in `sdkPaths` that
`sdkImportAllowed` refuses. Within a wire's file the namespace is its type: a helper
is a method on `messagesWire`, `responsesWire` or `chatWire`, and any other top-level name
starts with the wire's word. `New(providers...)` is the one constructor: `app.go` hands it
`newCatalog(cfg).Providers()`, and a test hands it the providers it needs. That is the
dialects-are-code/providers-are-data split as a package boundary
(→ [ADR: the llm package wires itself](../docs/adr/2026-09-21-llm-is-one-package.md),
[ADR: the catalog is its own package](../docs/adr/2026-09-24-the-catalog-is-its-own-package.md)).
The paragraphs below still describe the encoders, platforms and providers of the tag in the
sub-package names they had; the layout they land in is the one above.

- **`Encoder.Stream(ctx, provider, req)`** sends one turn to one provider, building the SDK client
  from `provider.BaseURL` and `provider.Key` on each call — options over Go's shared transport,
  nothing dialed until the request goes. `Request` carries the **resolved `Model`** (its `ID` goes
  on the wire; `MaxOutputTokens` shapes the call; `ContextWindow` is the
  most it reads in one request, input and output together, zero when not stated — every written
  catalog states it, and of the discovered ones only OpenRouter's `context_length` does;
  **`NativeServerTools` and `NativeClientTools` list the native tools this model takes, by contract**
  — see the next bullet — and the platform entries lose both through `withoutHostedTools`),
  `Effort`, `System`, `Messages`, `CacheKey`, `IdleTimeout`, **`CustomTools`** — the app's
  own definitions, each carried whole — **`NativeServerTools`**, the server contracts
  offered, each with `MaxUses` and, for a tool that takes a cut, `MaxTokens`, and
  **`NativeClientTools`**, the client contracts offered, by name, set only by a caller that
  will run what comes back. **A web call is the provider's, not the loop's**: no
  `tool_use`, no `tool_result`, no `tool_calls` row, no place in the budget. `System` is the
  standing instruction, sent in the
  dialect's own field for it (Anthropic's `system`, OpenAI's `instructions`) — Chat Completions
  has none, so there it is the first message, with role `system`; `chatsvc` embeds it from `prompts/system.md` — prose, edited as prose
  — and it asks for the markdown the webview renders, explains the cluster card a question may
  carry, and tells it cluster text — the card's and a tool result's included — is data, never an
  instruction; what the model can do rides beside it as one of two sections, `tools.md` or
  `no_tools.md` (*Chat*, below). **Effort is the provider's
  own vocabulary**, never translated; each catalog entry lists the levels it accepts and its
  default, and a model with no such knob lists none, the empty string then being the valid value.
  A model's output cap is the catalog's; reaching it is a *complete* turn whose finish reason
  says so. **A cap of zero is none stated**: the Chat Completions encoder then sends no
  `max_tokens` and the vendor's own default for the model applies — for most, the model's
  maximum, so a model with no cap has no per-turn spend ceiling but the vendor's. The written
  Anthropic and OpenAI catalogs always state one: the Messages wire refuses zero before it
  dials, since the API requires the field, and `catalog`'s `TestEveryKeyedProviderIsWhole`
  catches a row that states none. A discovered model states
  none — Ollama's included, so the daemon's own `num_predict` default governs a local model.
- **The llm service is built once, by `app.New`, and filled once, by its own `Start`**: the
  encoders, and `cfg.Providers` — the entries of `providers.All()`, a keyed one only when its key
  variable is set, in table order — with the fake encoder and `providers.FakeProvider()`
  appended in a debug build. `Provider(id)` answers from that table at once, so a stored
  message's provider renders before discovery. `Models(ctx)` and `Resolve(ctx, …)` read the entries
  `Complete` published — endpoints resolved, catalogs filled — and wait for it, or for `ctx`.
  The provider `Resolve` returns is the completed one, so a chat is sent where its models were
  discovered. `Complete` runs once and cannot fail: an entry the per-model check refuses keeps the
  table's, with one log line. **The table is
  Anthropic, OpenAI, eight cloud entries, Ollama.** The cloud entries — Gemini, Groq, Mistral,
  DeepSeek, xAI, OpenRouter, Together, Fireworks — are all on Chat Completions, each keyed by the
  vendor's own variable (`GEMINI_API_KEY`, `GROQ_API_KEY`, …) with its base URL a constant in
  its file. `llm.New` errors on a provider whose dialect has no encoder, two providers
  with one id, or two encoders for one dialect, and the sidecar does not start. **Every `Model`
  the service serves is a copy whose `ProviderID` the service stamps**, whatever the catalog
  entry held, so one catalog can serve two providers. **In Go a provider is named by id
  everywhere** — `Model`, `Block`, `Error` and the chat row all carry a `ProviderID`
  string — and only the wire expands it into a `Provider`, through `Service.Provider(id)`.
  `Resolve` returns the encoder, the provider and the model; an unknown provider or model is
  `ErrModelUnavailable`, an effort the model does not list `ErrBadRequest`.
- **Discovery runs once, from `Service.Start`, off the path to READY.** `Service.Start` runs each entry's
  `BaseURLFromEnv` (the Ollama entry's reads `OLLAMA_HOST`), then `llm.DiscoverAll(ctx, providers)` fills the catalog of every provider with a
  `Discover` function, all entries at once, each under its own `DiscoveryTimeout`: five seconds
  for a cloud entry, two for Ollama, set in `providers`. A discovering entry with no deadline is
  refused by `llm.New`, so the sidecar does not start; what an entry discovers is its own
  function, so there is no kind to be unknown. A `models` query or a send parked on it waits at
  most the discovery deadline, under the host's 30s `REQUEST_BUDGET` (`src-tauri/CLAUDE.md`);
  `chatsvc.Send` waits outside its transaction, so the one-writer pool is free meanwhile. A catalog that cannot be read —
  refused, timed out, not JSON, another shape — is an empty one, logged at `Info` with the provider and the failure's kind, never a
  URL, a body or a key; the provider keeps its label in the picker. Discovery does not run
  again: a daemon started or a model pulled after launch needs a restart, and the composer's
  empty-catalog placeholder says so.
- **A model is listed only when its catalog says it can chat; absent metadata never admits one.**
  Every cloud entry is `GET {BaseURL}/models` with the key as a bearer, one page, decoded in the
  vendor's own shape by the decoder in its file — a body of another shape lists nothing rather
  than everything. `everyID` admits every id, so it is only for a vendor that serves chat models
  and nothing its catalog could tell apart (DeepSeek); Mistral's reads
  `capabilities.completion_chat`; OpenRouter's wants `text` in both `architecture` modality
  lists and takes `top_provider.max_completion_tokens` as the cap; Together's is a bare array
  with `type: chat`; Fireworks' reads `supports_chat`. **`intersect` is for a catalog that does
  not say** (Gemini, Groq, xAI): the entry's written catalog is the models — label, efforts, cap —
  and discovery lists the entries the vendor still serves, in the written order; Gemini's
  `models/` prefix is stripped before the comparison and the bare name goes on the wire. A
  vendor whose catalog stops showing the field its decoder reads moves to `intersect`, never to
  a naming rule. Ollama's is the daemon's own API above the base URL's `/v1`:
  `GET /api/tags`, then `POST /api/show` for a model listing no `capabilities` (a daemon before
  v0.34). A model is listed only with **no `remote_host`** (a cloud-backed model runs on
  Ollama's servers) and **`completion` among its `capabilities`** (an embedding model cannot
  chat). A discovered model is its id as label with no efforts, no cap (unless its decoder read
  one) and no cache retention. **No test reaches a
  vendor**: every discovery and encoder test serves a synthetic body from an `httptest` server.
  `make llm-catalogs` (`scripts/catalogs.go`) is the one thing that does — for every entry whose
  key is set it prints what it would list beside what the vendor serves, so a written
  catalog is checked by hand, by running it — the script ranges over `providers.All()`, the same
  table the sidecar reads; integration tests are
  [TODO](../docs/TODO.md#testing). A vendor that needs a parameter added or left out gets a
  line of data on its entry (`Extra`), never a branch keyed on its id.
- **Errors and payloads name the provider, never the dialect.** `llm.Error.ProviderID` is the
  provider's id, and a message's `ProviderID` is the id of the provider that wrote its
  blocks; `stripForeign(providerID, m)` is those blocks as any other provider reads them,
  on its dialect or another, since a chat can switch to any model: every app field kept,
  every payload gone, so a `tool_use` keeps its id, name and input and loses the item it was
  read off, and a thinking block keeps its text and loses what was signed.
  **Every wire replays a call under an id it hands out** (`replayIDs` in `helpers.go`, over the
  whole conversation, so the same history mints the same ids and a turn's rendering stays a
  prefix of the next's): every call of a foreign row, and any own id already handed out, is
  minted as `call%05d`, which every API takes, and no id goes out twice. A result answers by
  `of`. An own call that is minted and keeps a payload has the minted id written into it
  (`withReplayID`: `id` on Chat Completions, `call_id` on Responses). The record keeps the
  writer's ids. **What a switch costs** is the other provider's thinking, the prefix cache and a
  provider-run search's results; on a Messages target with thinking on, a foreign row holding
  the chat's last tool round goes without its rounds until the target runs one of its own.
  `TestEveryDialectReplaysEveryOtherDialectsTurn` walks every ordered pair of wires, and
  `make check-switch` walks one chat across the real APIs by hand.
  **A block with a payload is replayed as that payload by the provider that wrote it, and as its
  app fields by any other.** A thinking block's signature is verified only by the API that
  signed it, and a reasoning item's ciphertext is encrypted to its keys, so a second provider
  on the same dialect would refuse the replay.
- **OpenAI caches the prompt prefix on its own** (the Messages wire's marks are in *What exists*,
  above), and from GPT-5.6 on it routes the cache itself: `prompt_cache_key` there only separates
  cache accounting between groups of requests, so the Responses wire sends none. From GPT-5.6 on
  the API takes no retention either: the prefix lives thirty minutes past its last use wherever
  it was written. **`Usage.InputTokens`
  is everything the model read on every wire**, though the wires differ — Anthropic's
  `input_tokens` is the uncached remainder and the wire sums it with the two cache counts;
  OpenAI's `input_tokens` and Chat Completions' `prompt_tokens` already include `cached_tokens`.
  `CacheReadTokens` and `CacheWriteTokens` ride to the model call's row
  (`llm_calls.cache_read_tokens`, `cache_write_tokens`, beside the **uncached** remainder in
  `input_tokens`), and they are the only evidence caching works: a
  regression is no error, just a higher bill, so check them by hand after a change to prompt
  assembly. **The prefix is only reusable if it is stable, so append, never edit**: the system
  prompt is a file with nothing per-turn in it, `buildRequest` sends complete rows only (a failed
  answer drops out without moving what came before it), native blocks replay verbatim, and anything
  per-turn goes into the messages at the end. A model or effort switch starts a new cache; the
  picker allows it, and the cost is one full-price turn. Each encoder's test pins a turn's rendering
  as a leading slice of the next's.
- **`llm.Prompt` is what the model reads of a message**: its `context` and text blocks in
  order, a blank line between, for the wires that take a message as one string (Responses,
  Chat Completions). The Messages dialect sends a `context` block as a text block ahead of the
  message's own instead. **The `<context>` tags are the wire's** (`wireText`, used by both):
  a stored `context` block holds its sections alone, and every wire wraps it, so the record
  carries no prompt convention and the webview draws the sections as they are.
- **The stored block schema is the app's, not a provider's**: `text` blocks; `context` blocks,
  the cluster card `chatsvc` attaches to a question (`ContextContent`); `native` ones that
  carry a provider's own block verbatim under that provider's id, dropped from a turn to any other
  provider, and a message the drop empties is left out of the request; and `thinking` blocks, the summary of an answer still streaming or cut
  short (and of the fake's, which has no native shape, and of every Chat Completions answer:
  nothing on that wire is signed or has to be resent verbatim, so it writes no native block),
  which no encoder sends; `ThinkingContent` writes a summary and a text as that pair, the
  thinking block left out when the summary is empty; and the two **tool blocks** — `tool_use`
  (the model asking: `ID`, `Name`, `Input`, always a JSON object) and `tool_result` (the
  sidecar's answer by `ID`: `Text`, and `IsError` for a refusal) — the app's own, which
  `stripForeign` keeps and `Prompt`/`Thinking` ignore — a `tool_use` the Responses API wrote
  carries its `function_call` item as its form, since the item holds more than the app's
  fields (`id`, `status`) and goes back verbatim. Server calls, their results and cited text are in *What exists*
  above.
  → [ADR: the record is the app's](../docs/adr/2026-09-11-the-record-is-the-apps-not-the-providers.md).
- **A native tool is a contract, and which list it is in says who runs it.** A contract is
  one input schema and one result schema under one name, typed `llm.ContractName`
  so a wire word or a custom tool's name cannot stand where one goes without a conversion
  saying so — the loop converts a `tool_use`'s `Name` exactly where it looks the call up
  (`knownTool`, `callRows`, `runCall`, `isCommandTool`) — and every one is a provider's: the
  vendor, then the vendor's own identifier (`AnthropicWebSearch` `anthropic_web_search_20260318`,
  `AnthropicBash` `anthropic_bash_20250124`,
  `OpenAIWebSearch` `openai_web_search`, `OpenAIShell` `openai_shell`). **Custom versus native
  is definition versus contract**: a custom tool carries its schema on the request
  (`CustomTools`), a native one is a name the encoder spells — so a tool the app defines
  itself, whoever runs it, is a custom tool, never a contract.
  `Model.NativeServerTools` names the tools the provider runs, in wire order;
  `Model.NativeClientTools` the tools the sidecar runs; `llm.New` refuses a name in
  both or twice. **An encoder reads the request's offers alone**, never the
  model's lists, spells each contract on its wire — Anthropic: the search with `max_uses` and
  `allowed_callers: ["direct"]`, the bash as type and name alone; Responses: `web_search` with `max_tool_calls` (a
  cap on built-in calls, which a function call does not spend), the `shell` tool with a local
  environment; Chat Completions: `ErrBadRequest` for any offer — in the order cluster tools,
  server, client, and refuses a contract it cannot spell, a `MaxUses` under one, and **one
  wire name spelled twice** (`wireNames`: an app tool named `bash` or `shell` beside its
  native). **The reader writes a client call under the wire's own contract, offered or not**
  (a `tool_use` named `bash` is `anthropic_bash_20250124`, a `shell_call` is `openai_shell`),
  so an unsolicited call keeps its identity and shape and the loop refuses it by the offer —
  and **a plain call that spells the contract's name fails the reply** (a `tool_use` named
  `anthropic_bash_20250124`, a `function_call` named `openai_shell`), since read as the
  contract the loop would run it as one and the OpenAI replay would answer a function item
  with a `shell_call_output` (`TestAnthropicRefusesAToolUseSpellingTheContract`,
  `TestOpenAIRefusesAFunctionCallSpellingTheContract`);
  the walk spells a contract back as its wire word (`anthropicWireName`:
  `anthropic_bash_20250124` goes as `bash`). **A `tool_use` whose contract the request offers replays
  in that shape, one it does not offer as a plain function round** — name and JSON in, text
  out, ahead of any verbatim form the block holds
  (`TestOpenAIRendersAShellRoundAsAFunctionWithoutTheShell`,
  `TestAnUnsolicitedShellCallReplaysAsAFunctionRound`) — under each dialect's own history
  filtering. **A shell call is a client call with a list inside**: a `shell_call` item is a
  `tool_use` named `openai_shell` whose `Input` is the action as the API sent it —
  `ShellAction`, the commands and the two caps — and whose form is the item; its result's
  `Text` is a JSON array of `ShellOutput`, one per command in command order (`stdout`,
  `stderr`, an `exit` outcome with its code or a `timeout`; `ShellRefusedExit`, 126, for a
  command that did not run, its refusal in `stderr`). On replay the item goes back verbatim
  to the provider that wrote it, a call without its form is rebuilt as a `shell_call` from
  the action (local environment, `status: completed`, no id), and a result answering a shell
  call of the row is a `shell_call_output` carrying the entries and the action's
  `max_output_length`. The SDK's input item has no slot for `created_by` or a null `caller`,
  so a verbatim replay drops those two. → [ADR: the catalog is its own
  package](../docs/adr/2026-09-24-the-catalog-is-its-own-package.md).
- **Tools are data, and the catalog says which models take them.** `llm.ToolDefinition` is a
  name, a description and a JSON Schema; `Request.Tools` carries the definitions, and
  `Model.Tools` says whether the model takes an offer of them — every Messages and Responses
  entry does, and on Chat Completions it is the vendor's word per entry. `Target.Stream`
  refuses an offer the target cannot carry before anything is sent, and `chatsvc` hands the
  box over only for a model that takes tools, so the prompt and the offer agree. **A finish
  reason is the provider's vocabulary, stored as it arrives, with one translation**: a reply
  that ended by asking for a tool is `tool_use`, the one word the loop keys on, on every wire
  — the Messages API's own word, what the Responses wire reports for a response holding a
  `function_call`, and what Chat Completions reports for `tool_calls`, or for `stop` with a
  call present. `toolInput` (`block.go`) is what the wires keep of a call's arguments: the
  object, trimmed, since they refuse a non-object on replay and the loop answers such a call
  `not-run`. The GPT platform entries take the cluster tools like every entry on the wire and
  no bash; no platform is on in any build, so nothing is verified for them until the setting
  that turns one on.
  **The fake** stages calls: `SetToolCalls` rides the next reply (after its thought and text,
  finish `tool_use`, ids `call-N`), `RepeatToolCalls` every reply, `SetFinish` ends the next
  reply on any reason, and `LastRequest` is what the caller sent back. `protocols.ToolCall(name,
  input)` is a staged call.
- **Both cloud encoders ask for a summary of the thinking, and both stream it.** Anthropic's request
  names `thinking: adaptive` with `display: summarized` (the default display is `omitted`) beside
  `output_config.effort`, **for a model that lists efforts** — the two are one feature there, and a
  model listing none (Haiku 4.5, whose thinking is the manual budgeted kind) is sent neither, since
  it refuses `adaptive`. The summary arrives as `thinking_delta`s and ends up in the signed thinking block's `thinking`
  field, which goes back unchanged. OpenAI's sends `reasoning.summary: auto` and the summary
  arrives as `reasoning_summary_text.delta`s, one part per section, stored in the reasoning
  item's `summary` list beside the ciphertext. It is never the raw reasoning on either. **A
  `Chunk` is of one `Kind`** (`ChunkText`, the zero value; `ChunkThinking`; `ChunkServer`, a
  server call's queries or URL under its word), and **sections are kept apart by one
  separator** (`ThinkingSeparator`, a blank line): each reader's `sectionJoiner` puts it before
  the first text of a new section — a thinking block's index, an OpenAI `(item_id,
  summary_index)` pair, since the index restarts per item — never before the first, and never for
  an empty section, so the streamed text and the stored text read the same. **`Thinking` is the
  one reader of a summary**, over all three shapes, joining with the same separator. An
  organization OpenAI has not verified may be refused the summary as a 400 before the stream
  exists, which is a `Failed` row on every send until it is; the fix is on the provider's side.
  Chat Completions asks for nothing: a server that summarizes sends it as a field the SDK's delta
  does not know, read off `delta.JSON.ExtraFields` as `reasoning_content` (DeepSeek) or
  `reasoning` (Ollama, OpenRouter). It sends `reasoning_effort` only for a model whose catalog
  entry lists efforts — the vendor's own words, so Gemini's `none`/`low`/`medium`/`high` is a
  thinking budget and Groq's `qwen/qwen3-32b` takes `none`/`default`.
- **Neither SDK deadlines a stream**, so `llm` does, and it measures **the wire, not the text**: a
  high-effort turn can think for minutes and write nothing, and only the encoder can tell that
  from a dead socket. The guard is reset by every event *and* by every read off the response body,
  because an SDK drops a heartbeat before a caller sees it — anthropic's SSE decoder skips `ping`
  outright — and a heartbeat is the only traffic some minutes of a turn carry. A stream with
  neither for the bound ends with `ErrStreamIdle`. **The bound is the dialect's own**, a
  constant beside its wire that `Target.Stream` passes in, since only the wire knows it:
  Anthropic pings every few seconds through a think (a minute), while the Responses stream sends
  nothing between `response.in_progress` and the first summary part, which is not promised early
  and at high effort can be minutes (ten). Chat Completions is five: a cold local model loads
  its weights and reads the whole prompt before its first token, sending nothing until then, not
  even response headers. **A Chat Completions stream ends on `[DONE]` and on a closed socket
  alike**, so the encoder decides from what it saw: no `finish_reason` is `io.ErrUnexpectedEOF`,
  and a finish with no usage completes unreported — the usage the request asks for rides a
  trailing chunk with `choices: []`, and on some vendors other chunks too, and the last report
  stands. It
  sends `max_tokens`, not `max_completion_tokens`: Ollama, llama.cpp server and vLLM read only
  the older name, and every cloud vendor still accepts it. An error inside the stream
  (`data: {"error": …}`) is the SDK's `StreamError`, not an `openai.Error`, so it renders as a
  connection failure and nothing leaks. The fake counts its chunks as events, so a parked fake trips the bound the way a
  silent socket does, and `SetIdleTimeout` shrinks it for a caller's test.
- **The SDKs' own retries are off** (`option.WithMaxRetries(0)`): a turn is never retried, and a
  retry inside the SDK is one below that rule.
- **A client takes nothing from the environment.** Both SDKs read a set of variables by default —
  Anthropic also an on-disk profile — so every client is built with those defaults off and every
  input stated: the provider's base URL (the production constant, redirectable only under `-tags
  debug`; Ollama's from `OLLAMA_HOST`), the provider's key, nothing else. OpenAI's switch is
  internal at v3.61.0, so the Responses and Chat Completions encoders build
  `responses.NewResponseService` / `openai.NewChatCompletionService` directly rather than calling
  `NewClient`. With an empty key the SDK sends no `Authorization` header at all, which is what a
  local endpoint needs. **Bumping either SDK re-reads its `DefaultClientOptions`**, and the
  boundary tests' variable list moves with it. **A conformance suite** (`protocols/conformance_test.go`)
  runs every network encoder through one fake server — a reply streams, the idle guard trips on
  a silent wire and before the headers, a cancel is not an idle stream, an error body echoing the
  key renders without it, a turn's rendering is a prefix of the next's — and a new dialect
  registers a `wire` there before anything else, and a new auth a fixture; what is specific to
  one wire stays in its own test file.
- **A failure is `llm.Error`**: the provider's id, the HTTP status, the API's error `type` and
  `code` — and **never the response body's message, on any path**, which can echo the request.
  The type has no message field, so that is a property of its shape. A failure with no response
  renders from the Go error's kind alone (`anthropic: connection failed`) and logs its cause
  at `Error` — the one place the reason can be read, since the text never carries it; the
  sink renders it through `safe` like any other `err`.
- **Keys are one environment variable per provider** (ten of them; the list is in *Every
  endpoint is an argument* above), read by `configFromArgs`, registered with `safe.AddSecret`,
  and cleared by `main`. On macOS a GUI launch sees none of them — the
  shell-environment allowlist never imports a secret — so a dev run from a terminal is where a
  key arrives. → [security records](../docs/security/2026-09-12-cloud-providers-from-the-environment.md).
  **OpenRouter's entry carries `Extra`**: `provider.data_collection: deny`, so OpenRouter routes
  only to downstream providers it classifies as not collecting user data for training or
  retention — its classification, not one we verify, and not zero-data-retention, which is the
  account's stricter setting.
- **A platform is entries that are on in no build yet** (`platforms.Platform`: `Bedrock`, `Vertex`,
  `Foundry`, in `platforms.All()` in picker order; `Rows()` is what a setting will call, and
  nothing calls it in a release or debug build). A platform entry states no value of its own —
  no key, and a base URL that is a pattern over the platform's conventions
  (`https://{resource}.services.ai.azure.com/anthropic`) or empty where the SDK derives it. **The
  platform's own conventions supply the rest**, read on the entry's turn, never at startup, so a
  platform behaves as every other tool on the machine does — and no `KSTACK_` variable exists
  for any of it. Bedrock's are read by the SDKs themselves: the region (`AWS_REGION`, the
  profile's `region`), a bearer from `AWS_BEARER_TOKEN_BEDROCK`, else the default chain — files,
  SSO, the metadata probe — to sign with. Vertex's are the Anthropic SDKs' Vertex conventions,
  read in `platforms/vertex.go` since the Go transport takes them as arguments: `CLOUD_ML_REGION`;
  `ANTHROPIC_VERTEX_PROJECT_ID`, then `GOOGLE_CLOUD_PROJECT`, then the project ADC carries.
  Foundry's are the Anthropic SDKs' `ANTHROPIC_FOUNDRY_RESOURCE` and `ANTHROPIC_FOUNDRY_API_KEY`;
  without a key the encoder signs with Entra ID's default credential. A convention that names
  nothing is a refused turn (`llm.TransportError`), tried again on the next. **Eight entries, one
  per dialect a platform serves.** Bedrock is Mantle (`https://bedrock-mantle.<region>.api.aws`,
  under `/anthropic` and `/openai/v1`): the vendor's own wire, so the legacy `bedrock-runtime`
  path is never used; `bedrock-gpt` omits `store`, `include` and `prompt_cache_key`. Vertex is
  `vertex-claude`, with no base URL — the SDK's Vertex transport derives the host from the
  region and rewrites the Messages call onto `…/publishers/anthropic/models/<id>:streamRawPredict`
  — and `vertex-chat` at `…/endpoints/openapi`; Vertex has no Responses API. Foundry is
  `<resource>.services.ai.azure.com/anthropic`, `<resource>.openai.azure.com/openai/v1`, and
  `…/models` with the key as `api-key` and an `api-version` on every request. **The Claude and
  GPT entries reuse the vendor catalogs under the platform's spelling** (`renamed`, one explicit
  map per entry; `TestPlatformCatalogsAreTheVendorsRenamed` pins one id per vendor entry), so
  efforts and caps stay in one place; the `chatcompletions` entries are written per platform.
  Nothing discovers: the platforms' lists sit behind their own APIs and grants.
- **`Provider.Auth` decides how a request is signed** — `AuthKey` (the zero value, every vendor
  entry), `AuthAWS`, `AuthGoogle`, `AuthAzure` — and each encoder's client builder is a `switch`
  on it yielding the service it streams on: the Anthropic encoder builds the SDK's Mantle
  client, its Vertex transport, or the ordinary client with an Entra ID token; the two openai-go
  encoders share `openAIOptions`, the SDK's Bedrock client on AWS and otherwise the ordinary
  client with the chain's token as the bearer — each built in `platforms`. A key on a platform entry goes as a bearer, except
  as `x-api-key` on Foundry Claude and in the header an entry's `KeyHeader` names; an Entra ID
  token is always the bearer. A stated base URL — the entry's, or the debug override
  `KSTACK_<ID>_BASE_URL` — is applied after the transport, so it wins over one the transport
  derived or the SDK's own whole-URL variable names. **A chain is walked once per entry and
  endpoint, on the first turn** — `resolveOnce` keeps what a walk returned for the life of the
  process, `ResetAuth()` clears it for tests — and the credential's own cache refreshes tokens
  after that. The walk runs on its own goroutine off the turn's cancel, since the credential
  outlives the turn; the lock covers the bookkeeping alone, so a slow chain holds up its own entry
  and no other; a turn waits for it on its own context and for at most `chainTimeout`, and is
  refused rather than held — the walk runs on and serves the next turn. **A refresh is bounded
  on its own**: Google's credentials keep the context they were found with and refresh through
  the client it carries, so `FindGoogleCredentials` puts one there with `refreshTimeout` — it
  holds for the Gemini entry's bearer and inside the Claude entry's transport alike — and
  `VertexRow` waits for `Token()` on the turn's context, `Token` taking none. The AWS chain is
  the SDK's, walked inside the client it builds, so what is kept is the built client; the Mantle
  client is handed the shared config's region, since it wants one before it would read the
  profile. ADC and Entra ID are reached through a seam each (`platforms.FindGoogleCredentials`,
  `platforms.NewAzureCredential`), since a test can drive neither offline through the
  environment. Each platform's file in `platforms/` holds its entries as data and, beside them,
  what fills and signs them — `BedrockMessages`, `BedrockOpenAI`, `VertexTransport`,
  `VertexRow`, `FoundryRow`, `AzureToken` are what the encoders call. **The conformance suite runs every
  case under every auth** (`cells()`: a wire × each fixture with an entry on it), each fixture
  stating the platform's conventions, the path, the credential header and the body fields the
  platform changes; `TestEveryCellTakesNothingFromTheEnvironment` pins that the vendors'
  variables reach no request under any auth. → ADR: a platform is a setting, and its SDK's
  conventions,
  [security record](../docs/security/2026-09-13-cloud-platforms.md).
- **Ollama is the one keyless entry**, always present and last: dialect `chatcompletions`,
  discovery `ollama`, base URL `llm.OllamaBaseURL` (loopback, port 11434) unless `OLLAMA_HOST`
  says otherwise. `llm.ParseOllamaHost` reads the variable the way Ollama's own client does, so
  the daemon dialed is the one `ollama run` would dial: a host or `host:port`, or an http(s) URL
  with its path prefix kept; no scheme means http on 11434, a scheme its own default port; a
  wildcard host (`0.0.0.0`, `::`) dials loopback, since the same variable sets the daemon's bind
  address. A value it cannot parse warns once — naming the variable, never the value — and keeps
  the default. The debug override `KSTACK_OLLAMA_BASE_URL` replaces the URL whole, so it ends in
  `/v1`. A cloud-backed Ollama model is never listed: a user who wants a cloud model sets a
  vendor's key. → ADR: Ollama is local, and `OLLAMA_HOST` says
  where.

## Catalog (`internal/catalog`)

The providers the app holds and, **for each provider, every tool its turns are offered**, ours
and the vendor's, by tool name. It sits above both `llm` and `tools`: it imports `llm` and each
tool's package for its `Name`; `config.go` and `app` import it, and no production code below it
does.

- `catalog.go`: `New(Config)` is every keyed provider whose key `Config.APIKeys` holds, in picker
  order, each at the base URL `Config.BaseURLs` moves it to, then `Config.Fake` when it is
  non-nil. A base URL for an unlisted id moves nothing, and only a debug build sets one.
  `Providers()` goes to `llm.New`, and `ToolsFor(target)` to `Box.For`:
  `lists[target.Provider.ID]`, nil for a provider with no list.
- `providers.go`: the vendor rows, one constructor each, its comment saying whether its ids were
  verified (`newAnthropicProvider()` is the Messages API's four models, each at a `writtenCap` of
  64k output tokens). `keyedProviders()` is the rows with their key variables, in picker order,
  and `KeyVars()` the same table by provider id, which `config.go` reads keys from and clears
  out of the environment. `BaseURLVars()` is the variable that moves each row's base URL in a
  debug build, `KSTACK_<ID>_BASE_URL` with the id upper-cased and each `-` as `_`. The fake is
  in none of them. A new vendor row goes here, never in `llm`. **Every entry states its
  `Cache`** and a row whose vendor routes by a key its `AffinityRoute`, as the vendor documents
  them; a new entry lands with the line `make check-cache` prints for it.
- **A list is policy.** `lists` is static data by provider id: `ours` (bash, Read, Memory, Write, Edit,
  WebFetch, TaskStop, Agent, KubeQuery) for every provider, and the Anthropic search added for `anthropic` and `fake`. It
  decides which code runs each kind of action for a provider, and what leaves the machine for
  it, so adding a vendor's tool to a list, or leaving one of ours off, is a security change.
  Where a vendor's tool and one of ours do the same kind of thing, the list names one of them.
  The lists do not depend on API keys, so every tool stays in the box whichever providers are
  keyed. `ToolsFor` takes the whole target, so keying by model or dialect later changes
  `catalog` alone.
- The tests that hold it: `TestOnlyAnthropicAndTheFakeListAVendorTool` (`catalog_test.go`), and in
  `app_test.go`, where the lists meet the tools, `TestEachTargetIsOfferedItsList`,
  `TestEachListIsOneToolOfEachKind` and `TestEveryToolIsOnAList`.

→ [ADR: the catalog is its own package](../docs/adr/2026-09-24-the-catalog-is-its-own-package.md).

## Tools (`internal/tools`)

Every tool the model can call. **A tool's kind is what it implements** (`tool.go`): every tool is
a `Reader` — `Name()`, its identity, unique in the box and what every stored call and count names;
`ActionKind()`, what its calls do, one of `ActionCommand`, `ActionRead`, `ActionWrite`,
`ActionEdit`, `ActionSearch`, `ActionFetch`, `ActionStop`, `ActionMemory`, `ActionDelegate`, `ActionKubeQuery`; and `Action(input, cwd, sandboxed)`, `cwd` and `sandboxed` the row's — with a `Prompt()`, its markdown section
of "What you can do", opening with its own `##` heading. A `Runner` adds
`Run(ctx, rt, input) (text, isError)`, which answers one call and never returns a Go error's
text. A `Custom` tool is a `Runner` offered by its `Definition()`, named as the tool is; a
`PerTarget` one adds `For(target)`, the copy a turn on that target is offered (`Agent`'s model
enum). A
`Native` tool is a vendor's: an `llm.NativeTool` (`Name`) with `ContractName()`. It has three
names: `Name()`, our identity for it, vendor-qualified and unique in the box
(`anthropic_web_search_20260318`); `ContractName()`, the vendor's own identifier for its shape
(`web_search_20260318`), recorded on each of its rows and matched by nothing; and each wire's
call name (`MessagesCallName`, `web_search`), which its calls arrive under there. The sidecar runs
it when it is a `Runner`, and the provider runs it when it is `Budgeted` (`MaxUses`, its cap per
turn).
A `Gated` runner adds `Approval(ctx, rt, input)`, which reads `rt` and never starts or stops a task or
writes a stamp; an error it returns may be a `*tools.Refusal`, carrying the result the model reads
in place of `bad-input`. `Gated` and `Bounded` embed `Runner`. A tool is one type, built once: the loop
finds `Gated` and `Bounded` on it by assertion, and the chat reaches it as `rt` at each call.

**One `Box` holds every tool** (`box.go`), in offer order, and the readers of tools this machine
cannot offer: `NewBox(tools, alsoReads...)`, which panics on a wiring mistake — a tool that is
neither `Custom` nor `Native` or both, a native tool run by neither side or both, a cap under one,
a kind outside the set, a custom tool defined under another name, two tools or readers of one
name. `For(target, names)` is a turn's box: none for a model that takes no tools, else each box
tool named in `names`, the target's list from `catalog`, in box order — every custom tool, as
`For(target)` makes it when it is `PerTarget`, each native tool `target.Takes` — and no readers.
`Without(kinds...)` is the box less every tool of those kinds, which is how a subagent is offered
no `Agent`. A name the box holds no tool of is skipped, which is
how a machine with no shell drops bash. `For` keeps no kind rule: **the list decides**, one tool
per kind, which `app`'s tests hold every list to. `Offer()` is the
definitions and native offers, `Runner(name)` the tool that answers a `tool_use` (never the
provider's), `Budgeted()` the provider's tools, `Prompts()` every section, `Empty()` no tools.
**Every stored call is read through `Action(name, input, cwd)`**: the tool or reader of the row's
name alone, since every row is written under its tool's `Name()`, unique in the box, and false when
it refuses the arguments or reads an action of another kind than its own. Callers ask the box rather than range over it, so the kinds never leak past
`tools`. → [ADR: every tool is in the box](../docs/adr/2026-09-24-every-tool-is-in-the-box.md).

**A tool can need its chat's directory or tasks.** A `ChatDir` is a chat's own directory, deleted
with the chat: `results/`, the output tools saved; `tasks/`, its background tasks' output; and
`workspace/`. It is `Path()`, the directory as a result names it, and `Root(create)`, that
directory as an `os.Root`. **`WorkspacePath(dir)` is the chat's workspace**, where every command
starts and whose files last for the rest of the chat, and `ToolHomePath(dir)` the tool home beside
it, where a sandboxed command's tools write (`OpenToolHome` opens it as the workspace is opened). `OpenWorkspace(dir, create)` opens it
through the chat's root with `rootdir.Open` (`internal/rootdir`, a leaf), which is how a chat's
directory, every level above it and its `results/` are opened too: `Lstat` through the
parent, make it 0700 with `create`, refuse anything but a directory (`rootdir.ErrNotADirectory`, a link
included), and `OpenRoot` it only if it is the directory the `Lstat` saw, since `OpenRoot`
follows a link that stays inside the parent. `rootdir.RemoveAll(root, name)` is how every such
directory goes: the root's `RemoveAll`, which removes a link and never its target and is nil for a
name already gone, and on a failure each directory under the name made `u+rwx` before it is read
(`fs.WalkDir` over the root, links skipped, a name that is a link never walked, since `fs.WalkDir`
follows its starting point) and once more — a command can leave a directory its owner cannot
write, such as a Go module cache. `rootdir.Sweep(root, entries, keep)` is every start sweep's
removal — the entries the caller listed from the root, less those `keep` passes, each through
`RemoveAll`, a failure logged and left — and the caller lists before reading what `keep` decides
by, since an owner commits what names an entry before making it. `rootdir.MakeRoot(dir)` makes a
directory 0700 with its parents and opens it as a root. `Tasks` is the chat's background tasks:
`Start(start)` records a task for the call now running, hands `start` its output file and keeps
the `Task` it returns, answering the task's id and path, or `ErrChatTaskLimit` / `ErrTaskLimit`;
`Stop(id)` is the model's stop of one of its chat's tasks. `FileStamps` is what the chat's turns
have seen of the files they read: `Stamp(path)` and `SetStamp(path, s)`, keyed by the path as
`fileguard.Abs` spells it. A `Stamp` is the SHA-256 of the bytes read and `Whole`, set only when the
model was shown every byte as it is — from line 1 to the end, nothing cut, redacted or stripped —
so Write can tell a file seen whole from one seen in part. A `Runtime` is what a tool gets of the chat its call
runs in: `ClusterID`, the chat's stored cluster, and `ChatID`, which a model names neither of,
`Session`, then `Dir`, `Tasks`, `Files`, `Agent` and `ActionAsker`, which puts a sandboxed command's
classified action (`ActionRequest`: the `permissions.Action`, whether a rule may allow it, the
`CommandRule` and `ChatRule` lines each allow answer adds, the `ClusterWrite` as sent, and the diff) to the user and
answers an `Answer` (approved, and the `permissions.Duration` the user chose), or records one the
proxy decided with nobody asked (`Record`, with the reason in the user's words), nil where nobody
can be asked. `chatsvc` sets every
field; a test sets the ones its tool
reads. **A `session.Session`** is one agent run's policy: its `Kind` (`Chat`, `Subagent` or
`Monitor`, the last built by nothing yet), `Outside`, the chat's switch as its turn read it, and
`Policy`, a function of a kube-context answering a `permissions.Policy` — the context's mode and the
rules — read live on every write, which `chatsvc` sets to the security store's `ModeFor` and the
chat's grants joined with the store's `Rules()` (`sessionFor`, `grants.go`), so a mode or rule
changed in Settings applies to the next write, a running subagent's included; `Network`, the
`session.Network` a sandboxed command starting now has — `NetworkChat` while the chat's
`network_enabled` is set, read at each call, else `NetworkTurn` for the turn's toggle, else
`NoNetwork`, a read that fails answering `NoNetwork` — nil for a session that never has it, which
`sessionFor` alone sets, so a session built any other way (the monitor's) has none; and `Folders`, a
function answering the session's folder grants (`session.Folder`: `Path`, `Write`), read live,
whose one builder is `chatsvc`'s `foldersFor` (*Chat*, below); and `NoPrompts` and `NoSecretData`,
a session that never asks and one that never reads Secret data, the one source of the policy's two
flags, which `chatsvc` never sets and `Narrow` copies (step 6B's monitor will set both). A nil
`Folders` reads none:
`Session.GrantedFolders(ctx)` is the reader that says so, and every reader of the folders goes
through it (`TestASessionWithNoFoldersReadsNone`).
The chat, the cluster and the workspace are not on it: the runtime's `ChatID`, `ClusterID` and
`tools.WorkspacePath(rt.Dir)` are their one source, and policy that depends on the cluster is a
function `chatsvc` builds knowing it. A turn builds its session (`turn.session()`); a
subagent's is **`session.Narrow`** of its parent turn's: identity and the switch are copied at spawn,
and a field of policy the user can change while it runs is a `func(context.Context) T` read live,
which `Narrow` hands on or tightens and never widens, so a subagent never holds more than its
parent now does. A later field is classed by that rule and gets its line in
`TestNarrowKeepsTheParentsIdentity`. `session` is a leaf: it imports nothing of ours, and a proxy
imports it, never the reverse. Each tool reads the fields it uses (Read `Dir` and `Files`, Write and Edit `Dir` and `Files`,
WebFetch `Dir`, TaskStop `Tasks`, bash `Dir` and `Tasks`, Memory `ClusterID` and `ChatID`,
KubeQuery `ClusterID` and `Dir`). **A tool that acts through a service is built with it**:
`memory.New(memorySvc)` and `kubequery.New(clusterSvc)` in `app`'s `chatTools`, each calling its
service on the runtime's cluster and mapping the service's own errors to the model's codes. The
rules a note keeps are `memorysvc`'s. → [ADR: a tool calls its service
directly](../docs/adr/2026-09-27-a-tool-calls-its-service-directly.md). `InlineLimit` (30,000) is the most a result carries, header included;
`FileLimit` (8 MiB) is the most output kept and the largest file Read opens. `GitBashDrive`
turns Git Bash's `/c/…` into the native path on Windows, and answers false elsewhere. A `Task`
is `Wait() Exit` — the code, whether it could be read, and whether a stop reached it before the
reap — and `Stop(now)`, a no-op after the reap.
`TaskOutputLimit` is `FileLimit` less room for the line that ends a task's file.

**A tool that can show a call exports `ActionOf(arguments, cwd) (tools.Action, error)`** (bash's
also takes the row's `sandboxed`), a package-level function over the same `parse` its `Run` uses, which its `Action` method calls, and
its offered name as `Name`. A `tools.Action` is the model's `Description` and exactly one kind —
`Command` (`Text`, `Cwd`, `Background`, `Sandboxed` off the row), `Read` (`Path`), `Write` (`Path`, `Content`),
`Edit` (`Path`, `OldString`, `NewString`, `ReplaceAll`), `Search` (`Query`), `Fetch` (`URL`, `Host`) or
`Memory` (`Op`, `Name`, `Body`, `Scope`; the call's own `Description` stays empty) or `KubeQuery` (`SQL`, `Limit`) — `Kind()` naming it, and carries JSON tags, since it rides the wire inside
`ToolCall` and gqlgen binds `ToolAction` and the kinds onto it. TaskStop's calls show none.
`bash.Reader` and a `taskstop` reader read those tools' calls where no shell was found, passed to
`NewBox` as `alsoReads`, so a stored call still shows. **A call's kind is its tool's**:
`Box.ActionKind(name)` finds the row's reader as `Box.Action` does and answers its
`ActionKind`, whatever the arguments hold, and `ToolCall.ActionKind` carries it, nil for a tool the
box does not know. gqlgen binds `ToolActionKind` onto `tools.ActionKind` member by member, and
`TestEveryActionKindIsServed` serves each of `tools.ActionKinds`, since a kind the binding lacks is
served as `""` with no error. **A call that asks has an action the approval request can draw**: a
`Command`, a `Read`, a `Write`, an `Edit`, a `Fetch`, or a `Memory` call with `Scope`
`everywhere` — for Memory the call decides, not the kind, since its other calls skip the gate. An error is arguments the tool refuses; how a tool reads its input never changes
for rows it has written (`TestActionOfReadsOldRowsTheSame`), since every read recomputes it.
→ [ADR: a tool call shows itself from its arguments](../docs/adr/2026-09-23-a-tool-call-shows-itself-from-its-arguments.md).

**A result too large to come back whole is saved, one way for every tool** (`save.go`).
`Fit(save, header, text, trailer, discarded, what)` is the header, the text and the trailer whole
up to `InlineLimit`; past it, the text cut to `FileLimit` on a rune goes to `save`, and the result
is the header, then
the reference's `<persisted-output>` block: `<What> too large (<size>)` (`FormatSize`,
1024-based, of the text plus `discarded`), `Full <what> saved to: <path>`, `; N bytes past the
limit were not kept` when the file lacks some, and the first `PreviewLen` (2 KB), then the
trailer. A failed save falls back to `Cut` at `InlineLimit` less the header and the trailer, never
splitting a rune, its note ending `; the <what> could not be saved`, then the trailer. Only bash
passes a trailer. A `Saver` writes the text and answers the path;
`SaveTo(dir)` is the one production saver, writing `results/<rand.Text()>.txt` under the chat's
directory (`O_EXCL`, 0600 — a name of the sidecar's own, never a provider's id), and failing with
no directory. Bash passes `output`, WebFetch `page`; a test passes a saver of its own. `Persisted`
is the block alone, for a tool that saves on its own: KubeQuery does, since its fallback for a
failed save is a render of whole rows, where `Fit`'s would cut one.

**`internal/sandbox` is the machine's sandbox**, a leaf that knows no tool and no cluster.
`Probe(ctx)` answers a `*Sandbox`, nil for none, and a `Status` (`Available`, `Reason`, and
`NetworkAvailable` and `NetworkReason`: whether a sandboxed command can be given the internet,
true wherever the sandbox is on macOS and only with a `pasta` that passed the probe on Linux),
which `app` logs, or `ctx`'s error when `ctx` ended first: a probe cut short is no verdict, and read as
one it would say there is no sandbox. `(*Sandbox).Command(ctx, Run)` is the process that runs a `Run` (`Shell`, `Args`,
`Dir`, `Env`, the whole environment, and `Policy`) sandboxed, made by `exec.CommandContext` and
not yet started, or an error and no command. `Confines()` is whether that confines it, and
`Port()` the port a run's relay listens on. `NetworkStatus()` is the `Status`'s network pair, and
`NeedsResolver()` whether a run with the internet needs a `resolv.conf` of its own (Linux), naming
`ResolverAddress`. Whether a sandbox confined a call is its row's
`tool_calls.sandboxed`, beside `cwd` and kept the same way, since it is not in the arguments. On
Windows `Probe` answers none, `Command` answers `errNone`, and `System` and `Never` answer
nothing: native Windows has no sandbox, and a Windows user who wants one runs the Linux build in
WSL2 → [ADR](../docs/adr/2026-09-28-native-windows-has-no-sandbox.md).

**A `Policy` is everything the sandbox enforces for a run** (`policy.go`), in four parts, and
each platform compiles it without knowing what a path or a relay is for:

- **`Files`** (`FilePolicy`): `Read` (readable, not writable), `Write` (readable and writable)
  and `Deny` (neither). A rule covers a path and everything under it. **The deepest rule wins,
  the narrower wins a tie** (Deny, then Read, then Write), **nothing sits beneath a Write rule**
  (a run can rename what it writes, so a deeper rule could be moved out from under), and a path
  no rule covers is off limits.
- **`Always`** (`AlwaysPolicy`): `Deny`, the denied-always list, where nothing opens; `Kstack`,
  Kstack's data, cache and runtime directories, where only the run's own `Read` and `Write`
  paths open. **No Files rule opens an Always path**: it is not a Files Deny, which a deeper rule
  opens. So a Read of `~` never exposes `~/.ssh`.
- **`Network`** (`NetworkPolicy`): the `Relays`, each a loopback port the forwarder connects to a
  Unix socket outside the run, a run having at most one; `Internet`, the internet with the host's
  loopback shut; and `Resolver`, a `resolv.conf` the caller wrote for a run with `Internet`, bound
  over the system's on Linux, `""` on macOS. The zero value is no network; a run can have the relay
  and the internet both.
- **`Limits`**: `CPUSeconds`, `MemoryBytes` (Linux alone; macOS's `Command` refuses one),
  `OpenFiles` and `Processes`, each a resource limit set soft and hard, zero for the platform's
  own. `CPUSeconds` has a hard limit `cpuGrace` (5 s) above its soft one. `Processes` is counted
  over the base **`Sandbox.CountedProcesses()`** answers, and the sandbox adds `forwarderTasks`
  for the forwarder, which starts before the limit is set (32 tasks on Linux, whose kernel counts
  its threads and which runs with one P, `TestTheForwarderStaysUnderItsTasks`; 1 process on
  macOS). The base is 0 on Linux from 5.14 in a user namespace of the run's own, else the user's
  count (`procs_linux.go` scans `/proc` by real uid and sums `Threads:`,
  `procs_darwin.go` counts `kern.proc.ruid`). → [ADR: process limits are set inside the run](../docs/adr/2026-10-02-process-limits-are-set-inside-the-run.md).

**`Check` refuses** a relative path; any rule or Always path strictly beneath a Write rule, Files
or Always; a Files rule on or inside an Always path; a run's own path outside every Kstack path,
on or inside a Deny, holding a Deny (the run's own paths compile last), or that is a link at its last component, since the profile resolves it and
would open the link's target (`TestARunsOwnPathThatIsALinkIsRefused`); a `Resolver` without
`Internet`, or one failing a run's own path's checks (`TestTheResolverIsChecked`); a second relay; a negative
limit; and a memory or process limit with no open-files limit, since `sandbox-shell` execs under
them without restoring the open-files limit the Go runtime raised. Every path is compared resolved, a missing one through
its deepest folder that exists (`resolved`), since both sandboxes check a file at its real
location. **`Command` fails rather than narrows or widens**: a run that fails `Run.check` — its policy's
`Check`, or an `Env` entry `Unpassable` matches — or that its platform cannot enforce, is
`Command`'s error, which the model reads. **What may never pass is one list**, `neverEnv`
(`env.go`): an `EnvRule` names a variable or a prefix; `NeverEnv()` hands out a copy and
`Unpassable(name)` matches. It holds what reaches past the sandbox (`SSH_AUTH_SOCK`,
`DOCKER_HOST`, the GitHub tokens), what changes what a program loads (`LD_*`, `DYLD_*`), what a
shell runs on its own (`BASH_ENV`, `ENV`, `PROMPT_COMMAND`) and every `AWS_` name.

**Both platforms compile one merged list** (`Policy.rules`), where the last matching rule wins:
the Files rules, sorted by resolved path, shallowest first and on one path Write, Read, Deny;
every Always path as a Deny; then the run's own paths, shallowest first. A Read or Write whose
path does not exist opens nothing and is left out. A Deny is left out where no Files Read or Write
reaches it, since its path is off limits already.

**The zones are lists, one shared file and one per platform** (`lists.go`, `lists_linux.go`,
`lists_darwin.go`), a `~/` path under the home and none with no home. A `Lists` holds `System`,
the folders every run reads (`/etc` and `/private/etc` whole); `Toolchain`, the `Location`s under
the home that hold the user's tools, each read where its folders exist, with the variables that
point its tool at them (`Env`, set when the first folder is read), since `HOME` is the workspace
(asdf's sets `ASDF_DATA_DIR` alone, since its scripts may live under a package prefix; rustup's
sets `RUSTUP_HOME`, since `~/.cargo/bin` holds only its proxies);
`Never`, what no run reads whatever else is granted — the credentials, the histories, the
container sockets, the browsers' profiles and `/etc`'s secret files; and `Closed`, `~/Documents`,
`~/Desktop` and `~/Downloads`, a Files Deny, so a Read of the home leaves them shut and a Read of a
folder inside one opens it. **The zones open no `PATH` entry** (`TestAFolderOnThePathIsNotRead`):
a run's adopted `PATH` folders are Read rules of its own (*Tools*, the bash tool).
Adding a location is a security change: every run reads it. **`Sandbox.System(home, shell)`
answers what every run starts from**, from one look at the home: a `System` of `Files` — Read on
the System folders, each Toolchain folder that exists and resolves below the home and every
app-data folder (`appDataDirs`: `~/.config`, `~/.local`, `~/.local/share`, `~/Library`), so a link
to one of them reads nothing (`TestAToolchainLinkToABroadFolderIsNotRead`), the shell's folders
(`shellReads`: the shell's own and that of each link on its way to the program, each as
`shellFolder` names it — a `bin` outside the home reads its parent unless that is `/` or holds the
home, and any other folder is read alone — or the program alone where that folder is broad: `/`,
the home or an app-data folder; `TestAShellInABroadFolderReadsOnlyItself`,
`TestAShellLinkReadsWhereItLeads`, `TestALinkedShellRuns`) and this executable at its resolved
path, and Deny on Homebrew's `var` (`brewVar`, Linuxbrew's on Linux) and the Closed folders —
`Env`, each found location's variables, and `Asdf`, whether asdf's was found. It holds no Read on
or inside a `neverPaths` path or another user's home (`FilePolicy.Outside`), so Docker Desktop's `~/.docker/bin` opens nothing
(`TestDockerDesktopsBinStaysDenied`), and on Linux none over a fixed mount (`overFixedMount`;
macOS answers false). **`Sandbox.Never(home)`** is the Never lists under the home, `/root`, every
other entry of `homesParent` (`/home` or `/Users`, but `notHomes`: `linuxbrew`, `Shared`, and an
entry holding the home, so a nested home is not another user's) and on
Linux the rootless container sockets in `/run/user/<uid>`, less any path on or above the home.
**`NoWrite(home)`** is the `NoWrite` lists under the home: what something outside the sandbox
runs — the shells' startup files and folders on both platforms, `~/Library/LaunchAgents` on macOS,
the user's systemd units, environment and autostart on Linux — which a read grant may name and a
read-write one may not. **`FixedMount(p)`** is whether `p` is on or under a mount every run has its
own of (`/proc`, `/dev` and `/tmp` on Linux, `/dev` on macOS, nothing on Windows), which no grant
may name; `overFixedMount` stays the narrower test a rule is refused by, since a run's own paths
may lie under `/tmp`.
`Never` and `System` stat and list folders, which can hang on a network mount, so both run under a
bound: bash's policy goroutine, and the probe's (`probePolicy`, `probe.go`: `System`, its folder
written, `Never` denied, built through `probePolicyWithin` and `buildProbePolicy`, a test's
seam).
→ [ADR: the sandbox's zones are Kstack's lists](../docs/adr/2026-10-02-the-sandboxs-zones-are-kstacks-lists.md).

**What no policy changes stays in each compiler**: on Linux the `/proc`, `/dev` and private `/tmp`
mounts, the namespaces, `--die-with-parent --new-session --as-pid-1`, the closing `--remount-ro /`
and the seccomp filter; on macOS the fixed reads, `setsid` and `setpgid` refused, signals within
the sandbox, the setuid programs that escalate refused, no `/dev/tty` and the one Mach service.

**On Linux it is bubblewrap** (`sandbox_linux.go`). `Probe` tries the system's bwrap first, the
first of `/usr/bin/bwrap`, `/bin/bwrap`, `/usr/local/bin/bwrap` and NixOS's
`/run/current-system/sw/bin/bwrap` that exists, then Kstack's own, `../lib/kstack/bwrap` beside the
executable (`src-tauri/CLAUDE.md`) (`programPaths`; never off `PATH`), and answers the
first that runs a shell through `Command` within five seconds (`probeBound`: it starts
this executable twice). Each probe, bwrap's and pasta's, runs in a process group of its own,
killed whole at the bound (`runProbe`): pasta killed during setup leaves the child it made for
the run's namespaces spinning and holding the probe's pipes, and the probe would wait on them
forever. The probe's shell prints the first line of its `/proc/self/uid_map`, with
builtins alone; a line that differs from the sidecar's means the run had a user namespace of its
own (`ownUserNS`), and `Probe` keeps whether the kernel's release is 5.14 or later
(`perNamespace`), the two `CountedProcesses` reads. A failure's reason is the first line it wrote, naming the cause
(`setting up uid map: Permission denied` where AppArmor restricts user namespaces, `Unknown
option` from a bwrap too old for a flag), and both reasons when both fail. The system's comes
first because the distribution patches it, while Kstack's own changes only when the user installs
a new release. Kstack's own stands in where the system has none, or one that fails, as on Ubuntu
23.10 and 24.04, where AppArmor blocks the system's and profiles Kstack's own alone. `Confines` is true, and `Port` the fixed
`forwarderPort`, since the namespace's loopback is the run's own. **`Command` refuses a rule on
`/tmp` or `/dev`, or on or under `/proc`**, a Files rule and a run's own path alike, since it
would replace a fixed mount; a rule under `/tmp` or `/dev` is bound over it. The arguments come
in the order that lets each mount lie over the last: the namespaces (`--unshare-user-try`, so a
setuid bwrap makes the run a user namespace where the machine allows one, `--unshare-net`, `-pid`,
`-ipc`, `-uts`, `--unshare-cgroup-try`, `--die-with-parent --new-session --as-pid-1`); a rule on
`/` itself; `/proc`, `/dev` and a fresh `/tmp`; the merged rules, one mount each (`mounter`): a
Read or Write bound at its resolved path and, where that differs from the path as written,
recreated as a link there (a merged `/usr` makes `/bin` one, and Homebrew's
`/home/linuxbrew/.linuxbrew` lies under Fedora Atomic's linked `/home`) unless the last mount over it is a bind, which
holds the tree's own link — a denied folder's tmpfs hides that link, so it is recreated in the
tmpfs, whichever of the two is mounted first (`TestALinkInsideADenialLeadsToItsRead`), a Read already readable
under an earlier Read not bound again (the fixed mounts hide what lies under them, as a denial does), the run's own paths bound as written, a denied folder an
empty tmpfs and a denied file `/dev/null`; `--remount-ro /`, then each denied folder remounted read-only after the binds
made inside it — the resolver's bind just before them for a run with `Internet` — then the chain, `<self> sandbox-init [--socket <S> --port <P>] -- <self>
sandbox-shell [--cpu <C>] [--files <F>] [--memory <M>] [--processes <N>] -- <Shell> <Args…>`. A Deny whose path is missing when the run starts covers
nothing, since a mount needs a path. `/run` and `/var` are never mounted, so the runtime
directory, the host's socket and a sibling run's kubeconfig are out of reach.

**A run with `Internet` starts under `pasta`** (`network_linux.go`): `<self> sandbox-pasta --
<pasta> --config-net --quiet --no-map-gw -t none -u none -T none -U none --dns-forward
169.254.1.53 -- <bwrap> …`, its bwrap arguments a run's less `--unshare-user-try` and
`--unshare-net`, with `--unshare-user --uid <uid> --gid <gid> --cap-drop ALL` in their place and
`--stderr-on-stdin` on `sandbox-init`: pasta makes a user and network namespace and starts
bwrap as its uid 0, so bwrap makes the run a user namespace of its own, as the user, holding no
capability. `--no-map-gw` and `-T none -U none` map and forward nothing to the host's loopback;
the namespace's own loopback is the run's, as without `Internet`. `Command` refuses `Internet`
without a pasta that passed the probe, and without a `Resolver`, which it binds read-only
(`--ro-bind`) after every rule and fixed mount and before `--remount-ro`, at the host's
`/etc/resolv.conf` as resolved now (`resolverTarget`, `hostResolvConf` the test's seam), so the
link the run reads leads to it; a target that would replace a fixed mount or lies under a denial
refuses the run. **`sandbox-pasta` keeps pasta's own output out of the run's**
(`sandbox.PastaMain`, `pasta_linux.go`), since an older pasta writes notices on stderr whatever
`--quiet` says: it starts pasta with the run's stderr on stdin, which pasta and bwrap pass on as
they pass every standard stream (pasta closes any other), and pasta's stderr on a pipe it reads.
`sandbox-init` puts the run's stderr back on fd 2 and the null device on stdin, then writes
`runStarted` on the pipe; what pasta and bwrap wrote reaches the run's stderr, up to 64 KiB, only
when that never came. It exits with pasta's code, catches and drops the stop signals as the
forwarder does, and takes pasta with it when killed alone (`Pdeathsig`). A group stop ends
pasta, bwrap and the command, and pasta killed alone takes the rest with it; pasta passes the
command's exit on, but a group SIGTERM leaves it exiting 0.
**The probe finds `pasta`** as it finds bwrap (`programPaths`): the first of `systemPastas`
(`/usr/bin/pasta`, `/bin/pasta`, `/usr/local/bin/pasta`, `/run/current-system/sw/bin/pasta`) that
exists, then Kstack's own, `../lib/kstack/pasta` beside the executable (`src-tauri/CLAUDE.md`),
never off `PATH`, once bwrap has passed. It runs a shell through each in turn, and the first that
passes is the run's: a system pasta whose AppArmor profile lets it start nothing outside
`/usr/bin`, as Ubuntu's does, fails wherever the run's bwrap is Kstack's own. Each runs the shell
under `probeBound` with every flag a run passes but
`--config-net`, and `probeNet` in its place — an interface, addresses and a MAC of its own — so
it reads no route, on a pasta that will not start without one too, and a Kstack started offline
still offers network (`probePasta`, `tryPasta`). The shell prints its `uid_map`, `Uid`, `Gid` and `CapEff` with
builtins alone, and the probe fails unless they are the user's and zero; its `uid_map` is
`pastaOwnUserNS`, which `CountedProcesses(true)` reads. A failure is `NetworkReason`, the first line
each pasta wrote, joined (`pasta not found` with none). pasta forwards a run's queries to the host's first
resolver, loopback included (systemd-resolved's stub), read from `/etc/resolv.conf`.

**On macOS it is Seatbelt** (`sandbox_darwin.go`): `Probe` finds `/usr/bin/sandbox-exec` and runs
`/usr/bin/true` under `probePolicy`, bounded by five seconds (`probeTimeout`: the forwarder
starts this executable once more), a failure's reason the
first line of its stderr. A probe that runs out of time keeps the sandbox, its reason saying so:
it runs once, at startup, so a slow start must not leave the session unconfined. `Confines` is
true; `Port` is a free loopback port, picked by listening on `127.0.0.1:0` and closing, since
Seatbelt has no private loopback; and `Command` is `sandbox-exec -p <profile> -D …` then the argv
(`Sandbox.argv`: the forwarder, with its relay, and `sandbox-shell` its child, with the limits,
for every run) — its ctx bounds building the profile too, since resolving a path can hang on a
network mount, and a ctx that ends first is its error — which `sandbox-exec` execs into, so the session the Bash tool starts is the run's process group and
the profile holds every descendant. **The profile is `profile_darwin.sb`**, embedded: fixed rules
with a marker line each for the policy's rules, the ancestors and the network (`;; RULES`,
`;; ANCESTORS`, `;; NETWORK`), which `profile` replaces. **Every path is a parameter** (`-D
RULE_3=…`, read as `(param "RULE_3")`), never text, since a path can hold `"` or `)`; the relay's
port is the one value written into the text. **Every path is resolved** before it is passed,
since Seatbelt checks a file's real path and `/var` and `/tmp` are links into `/private`; the
socket is passed both as given and resolved. A Deny holds for its path whether or not it exists.
In order, a later rule winning: `deny default`; processes, signals and process info within the
sandbox, but no exec of `sudo`, `su`, `login` or `security_authtrampoline`, by path
(`TestTheProfileRefusesSetuidPrograms`; macOS 27 runs no setuid program under a profile, `ps` and
`top` included, `TestEverySetuidProgramIsRefused`, and `kern.procargs2` still reads any of the
user's processes' arguments, and before macOS 27 their environment → [ADR](../docs/adr/2026-10-02-a-macos-sandboxed-command-reads-other-processes-arguments.md)), `setsid` and `setpgid` refused (`syscall-unix`) so a process stays in the run's group,
`sysctl-read`; **the merged rules**, a Read as `allow file-read*` then `deny file-write*`, so it
decides a tie with a Write as a read-only mount does, a Write as `allow file-read* file-write*`,
**a run's own Write followed by a denial of unlinking or creating its root's own entry**
(`literal`, so what lies inside stays writable), since a run that could remove its workspace or
kubectl cache and put a link there would have the next run's profile resolve it into Kstack's
data (`TestARunCannotReplaceItsWorkspace`; on Linux each is a mount point, and the same test holds
with no rule), a Deny as `deny file-read* file-write*`; **then the fixed file rules**, so no rule of a policy
undoes them: reads of `/private/var`'s named files and `/dev`'s, never `/dev/tty`, since input
pushed into a terminal (`TIOCSTI`) is run outside the sandbox by whatever reads it
(`TestATerminalIsUnreachable`), a listing of `/` itself (dyld opens it at launch and aborts every
program when it cannot), and writes to `/dev/null` and `/dev/fd`; `file-read-metadata` on every
ancestor of a Read or Write rule, after every Deny, so the workspace resolves under the denied
data directory; one Mach service (`com.apple.system.opendirectoryd.libinfo`); for a run with
`Internet`, `internetRules`: outbound to every address over `ip4`, then a deny of `localhost`
under `ip4`, which Seatbelt matches against every address the host holds, then a deny of all of
`ip6`, since Seatbelt cannot name the IPv4-mapped forms of the host's addresses — so such a run
has IPv4 alone, and the `localhost` deny holds only with the `ip6` deny after it
(`TestTheInternetReachesNoHostLoopback`, `TestTheInternetIsIPv4Only`, [the
record](../docs/security/2026-10-04-a-macos-run-with-the-internet-has-ipv4-only.md)) — then the
resolver's socket and the two services a run with the internet needs, `com.apple.dnssd.service`
and `com.apple.trustd.agent`, which fetches a certificate's issuer and OCSP URLs from outside the
sandbox and so opens with the network alone (`TestTrustFetchesNothingWithoutTheInternet`;
`Command` refuses a `Resolver`, since the run resolves through mDNSResponder); and for a run with
a relay, after them, bind, inbound and outbound on `localhost:<port>` over TCP on IPv4 alone (`tcp4`, the
one endpoint the forwarder holds; `ip` would take in UDP and IPv6 at that number) and outbound to
the socket (`relayRules`), and no other network. Seatbelt's remote filters name `localhost` or `*`
and no other host, so the rest of the local network is open with the internet. So the per-user temp directories and `/tmp` are unreadable, and a run with no
relay and no internet has no network; xcrun's cache reaches a run as a copy in its `TMPDIR` instead. **`refusedServices` is what no profile names but a run with the internet's**, by name or by prefix:
lookups, the Keychain, LaunchServices, Apple Events, the pasteboard, Spotlight and the services
that fetch for their caller; `user-preference-read` and `-write` are never allowed either. A
process spawned out of its group (`posix_spawn` with `POSIX_SPAWN_SETSID`, which Seatbelt cannot
refuse) outlives the run's group kill, still confined: macOS has no PID namespace → [ADR: a macOS
run keeps its group](../docs/adr/2026-09-28-a-macos-run-keeps-its-group-by-refusing-setsid.md).

**`search.go` is the one rule for a folder a run's `PATH` may search** (`SearchFolder(dir,
never, broad)`): absolute, resolving to a directory, not world-writable (sticky bit or not), not on
or under an Always path, not broad — `/`, the home or an app-data folder under it (`Broad(home)`,
the same `broadDirs` that keeps a toolchain folder out of `System`), or a folder holding one —
not a fixed mount (`overFixedMount`, which `Command` refuses), and no list
separator in its name, which a joined `PATH` would split — each refusal a reason of its own, so a
folder that passes never stops a run from starting. `securityconfig`'s filter and its per-run
check both call it, then add only the list's own rules. `FilePolicy.WithSearch(folders)` is the
run's Read rules for them, a Files Deny on one of those very folders dropped, since it would win
the tie.

**`paths.go` is how paths compare**: `resolved` (links followed, a missing path through its
deepest folder that exists), `resolvedAll`, `within` and `inAny`, by text, which the policy and
both compilers use. A Toolchain folder that is a link (`~/.nix-profile`) is bound at its target
and recreated as a link by `mounter.link` on Linux (`TestAToolchainLinkIsRecreated`); Seatbelt
checks the resolved path.

**`sandbox-shell` sets the run's limits and confines the shell** (`sandbox.ShellMain`;
`shell.go` writes and reads its command line through `shellCommand` and `parseShellArgs`). It sits
between the forwarder and the shell, so the limits hold the shell and everything under it and
never the forwarder, which lives as long as the run and relays every connection. **On macOS**
(`shell_darwin.go`) it refuses `--memory`, sets the CPU, open-files and process limits and execs
the shell; `shell_windows.go` refuses it. **On Linux** (`seccomp_linux.go`) it locks its thread, sets
`PR_SET_NO_NEW_PRIVS`, installs `filter()` on every thread (`SECCOMP_FILTER_FLAG_TSYNC`) and execs
the shell, so the filter holds the shell and everything under it while the forwarder, which dials
the run's socket, stays outside. `filter()` is a classic BPF program built with
`golang.org/x/net/bpf`, no cgo: another architecture is killed (and on amd64 an x32 number
answers `ENOSYS`, `seccomp_linux_amd64.go`), `socket` answers `EPERM` for any family but `AF_INET` and `AF_INET6`
— a Unix socket reaches the host's resolvers and buses, and vsock the hypervisor and other VMs,
past the network namespace — and so does
`socketpair` unless it is a stream or seqpacket pair, since a datagram end can send to a host's
pathname socket in any directory the run reads; io_uring
`ENOSYS`, `unshare` and `clone` with `CLONE_NEWUSER` `EPERM`, and `clone3`, whose flags it cannot
read, `ENOSYS`, on which glibc falls back to `clone`; tracing (`ptrace`, `process_vm_readv`,
`process_vm_writev`, `pidfd_getfd`, `kcmp`, `process_madvise`) and the kernel keyring (`keyctl`,
`add_key`, `request_key`), which a run inherits from the user's session, `EPERM`; and every mount
syscall (`mount`, `umount2`, `pivot_root`, `open_tree`, `move_mount`, `fsopen`, `fsconfig`,
`fsmount`, `mount_setattr`) `EPERM`, on every run, since bwrap has made every mount before the
filter and a capability that slipped through must not unmount a denial. After the filter
it sets `--cpu` and `--files` (`setTimeAndFiles`, `shell_unix.go`), each clamped to its own hard
limit (`setClamped`: a stricter machine stays stricter), CPU's hard one `cpuGrace` above. **Given
`--memory` or `--processes`** it then sets `RLIMIT_NPROC`, then `RLIMIT_AS`, clamped the same way,
and execs through a raw `execve` (`execLimited`): the
collector is off and the exec's arguments and failure line are built first, since an allocation
past the address-space limit is a runtime crash; a failed exec writes `sandbox-shell: cannot start
<argv0>: errno <n>` with raw `write`s and exits 125. Its tests run it on `bpf.VM` over a
`seccomp_data` laid out big-endian word by word, and `ShellMain` in a child.

**Tests that start the chain call `sandbox.Main` from their `TestMain`**: `sandbox`, `loginshell`
(whose tests run the login shell through the machine's sandbox, over a fixture under the package's
own `testdata/`, since a Linux run's private `/tmp` would hide one under `t.TempDir()`), `bash`
(whose proxy tests run on the machine's sandbox, through `sandboxed` in
`testutil_unix_test.go`, and write their coverage through `Tool.extraWritable`, a test's seam)
and `app` (whose tests reach the real probe through `New`). A test binary that did not would
run its own suite inside the probe until its bound. A test that needs a sandbox, or a confining
one, and finds none calls `testutil.RequireSandbox`, which fails under `KSTACK_REQUIRE_SANDBOX=1` and skips otherwise,
saying why; CI's Linux Go jobs set it through `setup-environment`'s `setup-sandbox`, with
bubblewrap installed and the AppArmor restriction lifted, and on macOS, where Seatbelt needs
nothing installed, it sets the variable alone; on both it installs `kubectl` and `jq`.

**`app`'s end-to-end tests run a chat through the real sandbox** (`startE2E`,
`testutil_unix_test.go`): an app over a fake API server its kubeconfig names, which answers the
probes, discovery and a list of two pods and records every request; the fake model, handed in
through `Config.fake` and staged per chat with `Fake.Route(question)`; a chat sent over GraphQL,
and its Bash call read back from `app.db`. `kubectl` and `jq` reach the run through a folder of
links to them that the launch's `PATH` names and the test includes (`toolsFolder`), since the
folder a runner keeps them in may be one no run searches. A missing sandbox, `kubectl` or `jq`
goes through `testutil.RequireSandbox`.

**The forwarder is `kstack-sidecar sandbox-init`** (`sandbox.InitMain`, `forward.go`), which
`main` reaches through `sandbox.Main` before it reads a flag of its own; `sandbox.ForwarderArgs`
is the one writer of its command line. `sandbox.ExitCode` is how a process ended as a shell reports it,
bash's included. `--socket` and `--port` come together or not at all: a run with no cluster
passes neither, and its forwarder listens on nothing. `--stderr-on-stdin`, set under
`sandbox-pasta`, takes the run's stderr from stdin before anything else (`takeStderr`). It sets the core size to zero before the
child starts, so no process of a run dumps a core, and no other limit: those are
`sandbox-shell`'s. `sandbox.Main` gives it one P (`GOMAXPROCS(1)`), which keeps its threads
within `forwarderTasks`. With a socket it listens on
`127.0.0.1:<port>` before the child starts, so a command never races it,
relays each connection to the socket byte for byte, half-closing each side as the other ends,
retries a failed accept after a doubling wait (5ms to 1s, as `net/http`'s server does), since a
burst of relays can run it out of descriptors for a moment, and runs the argv as its child with its own stdio, directory and environment, started with `syscall.ForkExec` and waited for by its own `wait4` loop. **As a PID namespace's first process** (bwrap's `--as-pid-1`) it reaps every process that ends under it until the child does, since orphans come to it, and makes itself non-dumpable first (`guardMemory`, `forward_linux.go`), so nothing under the filter can read its memory; anywhere else it waits on the child alone, since a test calls `InitMain` in its own process. It reads nothing,
holds no token and checks nothing: every check is the proxy's. It **catches and drops** `SIGTERM`,
`SIGINT` and `SIGHUP` with `signal.Notify`, never `signal.Ignore` — an ignored signal stays
ignored across `exec`, and a shell cannot trap one ignored when it started — so a stop to the
group ends the child and the forwarder exits with its status, 128 plus a signal that killed it.
Under bwrap the group is bwrap's alone (`--new-session`), and bwrap forwards no signal: a stop to
it ends bwrap, `--die-with-parent` kills the forwarder, and the kernel ends everything in the
namespace, so a sandboxed command has no grace on a timeout.
**Its own failures exit 125** with one `sandbox-init:` line on stderr (bad arguments, a port it
cannot listen on, a core size it cannot set, a command it cannot start), and `sandbox-shell`'s with one `sandbox-shell:`
line; a command can exit 125 itself, so the line is what tells them apart. `forward_unix.go`
runs the child; `forward_windows.go` answers 125 with *no sandbox on this platform*.

**`internal/permissions` is the decision** (`permissions.go`, `match.go`), a leaf that
imports nothing of ours: the six `Class`es, numbered as the note numbers them, the `Mode`s
(`ReadOnly`, `Ask`, `Auto`), a `Rule` (an `Effect`, a class, and the patterns `Context`,
`Namespace`, `Verb` and `Kind` with an exact `Group`, each unset matching anything, and `Command`, a
rule one command's answer added, never stored), an `Action` (a classified request: its class,
context, namespace, verb, group, kind, name, one-line `Summary` and `DryRun`, stored with its
approval under camelCase JSON keys),
and a `Policy` — a mode, the rules, and the session's `NoPrompts` and `NoSecretData`, which the
proxy copies on (`Grant.policy`) — whose `Decide(act)` answers a `Decision` and the reason in
the user's words. **`Decide` is `Authorize` then `Outcome`.** `Authorize(act)` refuses a class 6
action first under `NoSecretData` (*this session never reads Secret data*), ahead of every rule,
and under `NoPrompts` turns a verdict that would ask into `Refuse` last (*this session never asks:*
then the reason). Between them it is a `Verdict`, the
strongest of what matched: `Refuse` (a matching `Deny`, or a read-only mode on class 3, 4 or 5),
then `Forbid` (a matching `AskFor`, or class 5), then `Permit` (class 1 or 2, a matching `Allow`, or
`Auto`), else `Unmatched`, the default denial and the zero value. Its reason is the first source of
that verdict in that order, a rule's line when a rule decided it. **The verdict is
order-independent**: the rules' order picks the reason alone, so adding a rule never changes what
another means. `Verdict.Outcome()` is the `Decision`: `Permit` runs, `Refuse` is denied, and
`Unmatched` and `Forbid` prompt — a prompt is a denial the user may lift. A rule's class covers its own, and class 4 covers
class 5; a bare `*` `Kind` matches every kind, and any other `Kind` with no `/` matches the resource alone, though `*` crosses `/`, and covers its `scale` and no other subresource; a `Namespace` pattern never matches a cluster-scoped action, and `ClusterScope` (`[cluster]`, a word no namespace name can be) matches nothing else. `Match` is the one
matcher, a glob compiled to a regexp: `*` crosses `/` and `:`, `?` is one character, `\` escapes.
`Literal` escapes a value into the pattern that matches it alone, and every mode Kstack writes from
a value goes through it. `Rule.Line` is the rule in the user's words, a class 5 rule's naming it *destructive* and a class 6 rule naming a group or kind *reads of*, where the others read *writes of*, which
Settings shows, a reason names and a request draws under its allow answers, and a command rule's
ending *for this command*. **It draws each pattern field one way**: name characters alone bare, a
`*` or `?` in them a glob; a literal holding a glob character, a space, a `"` or a `\` unescaped in
quotes; anything else in quotes after *matching*; inside quotes a `"` or a `\` follows a `\`. So a
` / ` outside quotes always separates the context from the namespace. **`Grantable(v, act)`** is
whether an answer may write a rule: an `Unmatched` verdict, an action that is not a `DryRun`, with
a `Context`. **`GrantRule(act)`** is the rule a chat or always answer writes: an `Allow` of the
action's class in its context and namespace, each through `Literal`, and for an action in no
namespace or on a core Namespace the group and the resource too; any other is `Inside`, which
never matches a Namespace object and whose line reads *inside*. A class 6 action's names the
context and namespace alone, never `Inside` (*Allow Secret reads in dev-eks / team-a*, or *… in
dev-eks* for a read across the cluster), since no Secret is a Namespace object. **`CommandRule(act)`** adds the
verb and the resource, sets `Command` and clears `Inside`. Neither sets an `ID`; the writer does. A **`Duration`**
is how long an approval holds: `once`, `command`, `chat` or `always`. `Refused` is the `Deny` of every cluster write that stands in for a rule Kstack could
not read, and `RefusedSecrets` the `Deny` of every Secret read beside it, since a class 4 rule does
not cover class 6. Class 6 asks under `ReadOnly` and `Ask` and is `Permit` under `Auto`.

**`kubeproxy/classify.go` classifies a request**: a `GET`'s verb is the API server's — `watch`
for a watch, else `get` with a name, else `list` — and a `GET` of core `secrets` is class 6, its
summary naming what it shows (*Show Secret db-creds in team-a on dev-eks*, *Show Secret data on
dev-eks*, *Watch Secret data in team-a on dev-eks*); any other
read, a self review and a dry run on a group version in `honorsDryRun` (the stable ones the API server
serves itself; an aggregated API, routed by group and version, may ignore `dryRun`) class 1, a write on the class 5 list class 5 (`destructive`: a
delete of a namespace, node, PV, PVC or CRD, or a namespace's `finalize`, which completes one; any `deletecollection`; any write of RBAC, of the
admission webhooks and policies, of a CSR's `approval` or of `ephemeralcontainers`; and a replica
write — a `PUT` or `PATCH` of a `scale` subresource, of an `apps` deployment, stateful set or
replica set, or of a core replication controller — in any form but the plain ones `keepsReplicas` reads whole: an object body with no `$`
directive on it or its spec and a literal positive `spec.replicas`, or on a workload none, which the
API server keeps or defaults to one, or a JSON Patch, its keys read exactly, that only adds or
replaces `/spec/replicas` with a positive count or, on a workload, touches paths under `/metadata/`
or `/spec/` beside `replicas`, never a `move` or `copy`; an apply is not plain on a `scale`), and
any other write class 4. Its `Summary` is one line naming the target as kubectl does (*Delete
pods/api-7f9c in team-a on dev-eks*). A write to a core Namespace is in that namespace's scope, a create's
read off the body's `metadata.name`, so a rule scoped to it matches. `Destructive` is the class 5 list in words, which
Settings shows. → [ADR: permissions are classes, modes and rules decided at the proxy](../docs/adr/2026-10-02-permissions-are-classes-modes-and-rules-decided-at-the-proxy.md), [ADR: a prompt is a denial the user may lift](../docs/adr/2026-10-03-authorization-is-binary-and-a-prompt-is-a-denial-the-user-may-lift.md).

**`internal/kubeproxy` is the cluster proxy**, a leaf beside `sandbox` that imports nothing of
`tools` or `clustersvc`: `kubeproxy.go` the grant and the handler, `policy.go` the path parse
and the read policy, `redact.go` and `redact_helm.go` the Secret rewriter, `status.go` the
`Status` every refusal writes, `server.go` the server a run serves a grant on, `write.go` the
write path, `classify.go` the classifier. **A `Grant`** (`NewGrant(up, sess, kubeContext, asker, refusal, qps, burst, maxInFlight)`) holds the
session of the run it serves, the kube-context its writes are decided in (the chat's cluster record's `KubeContext()`, uncut), answered by `Session()` (what the token maps to), a 256-bit
token, an `Upstream` (`Endpoint(ctx)`: an `Endpoint` per request, the connection's base URL and client
and a `Done` that closes when they no longer reach the chat's cluster, which cancels the request
still open, a watch or a follow included), a
`rate.Limiter`, a cap on requests open at once, and the `Asker` its writes are put to, or with
none the refusal every write answers; `End` kills it, so every later request is 401 and those in
flight are cancelled, and `Wait` returns once every handler it let in has (counted under a mutex
only while the grant lives, so none joins after `End`). **The token is proxy credentials**: a run's `proxy-url` is
`http://kstack:<token>@127.0.0.1:<port>`, which Go's transport sends as `Proxy-Authorization`
on every request, since clientcmd applies a kubeconfig user's credentials only to a server
reached over TLS; and no browser page can set a `Proxy-` header. The handler, in order: `Host`
must be `cluster.kstack.invalid` and the credentials the grant's (constant-time), else 401; the
policy, else a 403 naming why; a write goes to the write path (below); a body read whole under
64 KiB, else 413; a slot, else 429 with
no `Retry-After` (a watch holds its slot until it closes, so nothing waits for one); the
limiter's `Wait`; `Endpoint`, each sentinel a 503 with its own message and never the error's text
(`ErrIdentityMismatch`, `ErrNotIdentified`, `ErrNotConnectable`; any other error *the cluster
is not reachable*); then an
`httputil.ReverseProxy` made per request — `SetURL(base)`, which keeps a base path prefix,
`Authorization` and `Proxy-Authorization` deleted so the connection's transport adds the user's
own, the connection's `Transport`, `FlushInterval: -1`, and a 502 *the cluster did not answer*
for an upstream that fails. **The policy** refuses a path that is not canonical first (a
`RawPath`, or one `path.Clean` changes), since the parse reads the decoded path and the proxy
forwards the raw one; then parses as the API server's `RequestInfoFactory` does, never by a
segment anywhere in the path. `GET` of a resource, a discovery path, `/api`, `/apis`,
`/version`, `/openapi/…` and the health paths pass, and so does a `POST`, `PUT`, `PATCH` or
`DELETE` of a resource, which `isWrite` sends to the write path unless it is a `POST` of a self
review (`selfsubjectaccessreviews`, `selfsubjectrulesreviews`, `selfsubjectreviews`,
cluster-scoped, unnamed), which passes unasked; a core `secrets` request is redacted (below),
and a read of one that can carry data is decided first (*The Secret read*, below);
refused are `exec`, `attach`, `portforward`, every `proxy` path and an `Upgrade`, `Connection:
upgrade` or `Impersonate-*` header, the `token` subresource of core `serviceaccounts` whatever
the method (its answer is a credential the model would read), any other method on a resource,
and any other path — each refusal a `Status` whose message says which. **The write path**
(`write.go`) refuses unasked, before any queue, a grant with no asker, and a query `url.ParseQuery` refuses, since
a pair that does not parse is dropped on its way to the API server and a selector shown on the
request would not run; then takes the write lock, an `x/sync/semaphore` of one
taken on the request's context and held until the forward returns, with at most
`maxQueuedWrites` (8) writes and Secret reads waiting for it and one more a 429 with no
`Retry-After` (*too many requests are waiting on the user*); reads the body
only then, under `maxWriteBody` (1 MiB, else a 403); then **the helm gate** refuses a helm
release write (`writesRelease`: a `PUT`, `PATCH` or `DELETE` of a Secret named
`sh.helm.release.v1.*`, or a `POST` to `secrets` that `mayBeRelease` — anything but a JSON object
typed, by exact key, as something else) when the grant's `redactedReads` holds its namespace or
`""`, since helm rebuilds a release from the Secrets it read (`refusedHelm`) — ahead of the body
check, whose refusal of the marks such a release carries would not say what allows them; refuses
(`checkBody`, each a 403) a body
with a `Content-Encoding`, not valid UTF-8, or whose media type, parameters aside, is not JSON
or apply YAML (a `DELETE` may carry none), one that does not decode as its media type says
(`decodeBody`: an apply patch through `sigs.k8s.io/yaml`, as the API server reads one, anything
else as one JSON value), one repeating a key in one object (`uniqueKeys`), since `decodeBody`
keeps the last where the API server's typed decoder merges, so the body would be classified and
shown as other than it runs, one with `[redacted]` or its base64 in any decoded string, a key
included, so an escape that spells the mark differently does not hide it, and for a helm
release write a body read inside (`checkRelease`): a JSON patch, and one
setting `stringData`, refused as unshowable, and a `data.release` decoded with `decodeRelease` and
refused when it does not decode or carries the mark; then, still under the lock, so writes reach the cluster in the order they were decided,
classifies it (`classify`, below) into a `Write` — the method, the path and raw query, the
policy's subresource, the media type, the body, `DryRun`, set only for a `POST`, `PUT` or `PATCH`
whose every `dryRun` is `All`, since the API server reads a `DELETE`'s options from its body when it
has one — and takes the verdict once: `Authorize` over the session's `Policy` for the grant's
context, joined by the grant's own `commandRules` (a session with no `Policy` is read-only). Every
`Asker` call carries a `Request`: the `Action`, `Grantable`, the `CommandRule` and `ChatRule` lines
each allow answer adds (`CommandRule(act).Line()` and `GrantRule(act).Line()`, empty when not
grantable), the `Write`, and the diff. `Allowed` is recorded through `Asker.Record`, then forwarded, and a record that fails
forwards nothing (*this change could not be recorded*); `Denied` is recorded, a failed record
logged, and answered 403 *kstack: <summary> is not allowed: <reason>*; `Prompted` is previewed
(below), then `Ask`ed, which answers an `Answer`: approved, and the `Duration` the user chose. A
denial is a 403 *the user did not approve this change*, a wait that ended a 403 *the user did
not answer this change*, and an approval forwards the bytes read, taking a slot and the limiter
only then, so a write waiting on the user holds neither. **An approval for the command adds
`CommandRule(act)` to the grant's `commandRules`**, so the rest of the
command's same change is allowed, recorded `allowed` with the rule's line; it ends with the grant.
**The preview** (`diff.go`) runs for a write that asks alone, a `PUT` or `PATCH` of a named object on
a group version in `honorsDryRun` (else its `DiffError` says so): a `GET` of the path through the
endpoint's client, a 404 no preview, then the same request with `dryRun=All` appended to the query,
each in a slot taken with `TryAcquire`, through the limiter, ended by the endpoint's `Done`, the
grant, or `diffTimeout` (10s, a field), and read to `maxDiffObject` (3 MiB). Both answers lose
`managedFields`, `resourceVersion` and `generation`; on core `secrets` each `data` and
`stringData` value is compared, then `redact`ed and marked `[redacted]` or `[redacted: changed]`;
on every kind each last-applied annotation is compared by its location, a pod template's included, and blanked (`redactLastApplied`). Then
`sigs.k8s.io/yaml` and `go-difflib`'s unified diff, three lines of context and no header, cut past
`maxDiffLines` (2,000) with `DiffCut` set, or *No change.* A failure is a `DiffError` in the
user's words, a status message through `safe.String`. **The Secret read** (`secretread.go`): a
read of core `secrets` that is not `metadataOnly` — the first JSON type its `Accept` names a Table
or `PartialObjectMetadata` of `meta.k8s.io`, with no `includeObject=Object`, as kubectl's `get`
sends with no `-o` — goes to `serveSecretRead`; a metadata-only read is forwarded redacted and asks
no one. With no asker it is forwarded redacted and records nothing. Otherwise it takes the write
lock through `takeWriteLock`, as a write does, and `decideSecretRead` classifies it, takes the
verdict and builds the `Request` as a write's is, its `Write` the method and the path with no body;
`Allowed` is recorded and shows the data, a record that fails keeps it redacted; `Denied` is
recorded and redacted; `Prompted` is asked, and only an approval shows the data, a `Command` answer
adding `CommandRule(act)`. A read that passes redacted marks its namespace (`""` across the
cluster) in the grant's `redactedReads`, which the helm gate reads under the same lock. The lock is
released before the forward, so an allowed watch never holds it, and every answer is a 200. **`forward` takes `redact`**: every
caller passes `p.onSecrets()` but an allowed Secret read, so a write's answer on `secrets` stays
redacted. **A request
on core `secrets` is rewritten both ways** (`rewriteSecrets`, set on that request's proxy when it
is redacted): its
`Accept` keeps only the `application/json` types and its `Accept-Encoding` goes, so the transport
hands back plain JSON; a response of any other type or with a `Content-Encoding` is a 502 in its
place, its body closed unread. The body is rewritten as a stream through a pipe, a `json.Decoder`
(`UseNumber`) reading a value at a time, so an object, a list, a table and a watch are one loop:
a top-level object's `items` or `rows` stream an element at a time, each redacted alone, and
its other keys, any other array included, are held and redacted together when it ends. The walk (`redact`) sets each
`data` value beside a `metadata` to the base64 of `[redacted]` and each `stringData` value to
`[redacted]`, fails on either that is neither a map nor null, and sets every
`kubectl.kubernetes.io/last-applied-configuration` to `[redacted]` in any map, since a table
row's metadata carries a Secret's whole. **A helm release is redacted inside**
(`redact_helm.go`): a Secret typed `helm.sh/release.v1` has its `data.release` decoded (base64,
base64, gzip to at most 64 MiB, then the JSON's top level as raw fields), every scalar of
`config` replaced, and every map whose `kind` is `Secret` in `manifest` and each hook's redacted
as above, with every `last-applied-configuration` in its document, and every item of a
`SecretList`, whatever kind it names — the manifest split on helm's own separator, each piece refused unless go-yaml's
decoder finds one document, and a piece holding a Secret encoded again with its `# Source:`
line alone of its comments — then encoded again; the chart, `info` and every other field pass
as they came. **A failure fails closed**: the status line waits for the first redacted piece,
except on a watch (`watch` read as the API server reads it, or the legacy `/watch/` path), so a
failure before it answers a 502 and one after it cuts the body. **The server**
(`NewServer`) sets `ReadHeaderTimeout` 10s, `IdleTimeout` 60s and `MaxHeaderBytes` 64 KiB and no
read or write timeout, which would cut a watch.

**`internal/tools/internal/fileguard` is the checks every file tool makes on a path**, so the tools cannot
disagree on them; it imports `tools` alone. It sits under `tools/internal` because it is not a
tool: every package directly under `tools/` is one, and Go's `internal` rule keeps anything
outside `tools/` from importing it. `Abs` is the path a tool
opens: one line of at most `MaxPath` (4,096) bytes with no control character, since the request
draws it whole (`ErrNotOneLine`), absolute and plain (no empty, `.` or `..` component, so the path
the request draws is the path opened; `ErrNotPlain` carries the plain form), a Git Bash drive path
read as native, and on Windows a UNC or device path refused (`ErrNotLocal`). `Under` is a path
relative to a directory it is under by name. `Fence` is Kstack's directories (`NewFence(dirs...)`,
`ErrNoFence` for none, since a fence around nothing holds nothing),
which no file tool opens but the chat's workspace: `Named` by name alone, `Holds` on disk — the longest existing prefix, links followed, and each ancestor
compared with each directory by `os.SameFile`, `ErrUnresolved` for an ancestor it cannot resolve.
**`Holds` reads each directory again on every call**, and makes one that is missing 0700, as the
app made it: the OS or the user may clear or recreate one while Kstack runs, and a directory read
once would stop matching its replacement, while a missing one matches nothing. One it can neither
read nor make is left out, since a write could make nothing under it either.
`NamedOutsideWorkspace(results, path)` is `Named` less the workspace by name. **`Fence.File(results,
path, create)` is the file Write and Edit change** (`file.go`): a path under the workspace by name
is a name under `OpenWorkspace`'s root, made first with `create`, a missing workspace `ErrMissing`;
any other path is the path, once `Holds` finds it outside (`ErrFenced` when not). A path that
reaches the workspace by another spelling is not under it by name, so `Holds` refuses it. A
`File`'s `Lstat`, `Open`, `Replaceable`, `Replace`, `Creatable` and `Create` are the path forms, or
the root forms under the workspace or a walked folder, and `Close` closes the root.

**A granted folder is reached by handle** (`grant.go`, `walk.go`). `Fence.WithHidden(hidden)` gives
a fence what the sandbox keeps shut under a grant: `hidden` answers `Hidden`'s `Never` (the
denied-always list with Kstack's directories) and `Closed` (the closed folders), as the zones list
them, from `securityconfig`'s snapshot, so asking touches no disk; a fence with none skips nothing.
**Both are resolved each time they are compared** (`Hidden.resolved`), never kept resolved, so a
never path that is a link retargeted after the sync is caught at its target of now
(`TestARetargetedNeverPathStaysHidden`, `TestACheckResolvesTheZonesNow`).
**`Granted(ctx, folders, path)`** is the folder a path is under, compared resolved — the path through
its deepest folder that exists, as `sandbox.Resolved` does — the deepest folder answering, a
read-write one first; false for a path under a `Never` path, or under a `Closed` folder that lies
inside the grant (one over it is the folder the grant opens). The gate calls it before the loop's
bound on the call, and a stat on a dead network mount blocks in a syscall no context reaches, so
the resolving runs on a goroutine abandoned when `ctx` ends and the call then answers false, so
the tool asks; with no folder granted nothing is resolved
(`TestGrantedAnswersTheCancelWhileResolvingBlocks`). **`Walk(folder, path)`** is the
`File` a tool changes there: `/` opened as an `os.Root`, then each folder down to the grant
`Lstat`ed and opened without following a link — a link on the way, the grant's own included, is
`ErrMoved` — then inside the grant each folder the same way, a link read and walked again from the
grant's handle as a folder-relative path (one that leaves is `ErrLeaves`, past `maxLinks`, 40,
`ErrTooManyLinks`). Each opened handle is `Stat`ed and compared with the closed paths' info, read
once as the walk starts, by `os.SameFile`, so a path through a closed folder is `ErrHidden`
wherever a link pointed; a `Closed` folder counts only below the grant. A missing part is left to
the `File`, whose `Create` makes it on the deepest handle and refuses a link in its way; a `File`
whose folder was missing (`ancestorMissing`) answers `ErrMissing` to `Lstat` and `Open` without
opening anything, since the root would follow a link made there since, past the walk's checks
(`TestAnAncestorMadeAfterTheWalkIsNotFollowed`). The `File` checks what `Lstat` and `Open` find
against the same infos, since a `Never` path can be a file (`~/.netrc`).
`walkHook` is the walk's test seam. `WalkRefusal` is the one wording of the walk's errors for the
model.
`Open` refuses anything but a regular file of at most `FileLimit` (`Check`) by `Lstat` before
opening and again by the open file's `Stat` after; a link in the last component is `ErrLink` with its target,
absolute and plain (on Windows a symbolic-link or junction reparse tag; any other tag is judged by
`Stat`). `Lstat` is the same classification by type alone, of any size, opening nothing.
`OpenFlags` is `O_NONBLOCK` on Unix, so a FIFO never blocks the open; `ReadAll` reads
through a `LimitReader`; `Binary` is a NUL in the first 8 KiB. The errors are values, so each
tool writes its own words for the model.

**The write half** (`write.go`, with `write_unix.go`, `write_linux.go`, `write_darwin.go`,
`write_windows.go`) is what Write and Edit share. `Replaceable(path, info)` refuses a file the
user does not own (`ErrOtherUser`), one whose group the new file could not be given
(`ErrOtherGroup`: a group is given when it is one of the user's, or the directory's where a new
file takes it anyway — always on macOS, under a setgid directory on Linux), one the user cannot
write (`ErrReadOnly`) and one in a directory they cannot write (`ErrDirReadOnly`), all through
`access(2)` and `Stat`, opening nothing; on Windows only the read-only attribute is checked. It
calls `replaceable` with an `owner` (uid, groups, the platform's inheritance rule), which a test
stubs. `Creatable(path)` needs the deepest existing part of a new file's path to be a directory
the user can write and search (`ErrNotDir`, `ErrDirReadOnly`, each carrying that `Prefix`).
`Replace(ctx, path, content, old)` writes a synced temporary file beside the target
(`.<name>.kstack-*`, the name cut to 200 bytes), gives it `old`'s group when a new file did not
take it and `old`'s permission bits, and renames it over the path. `Create(ctx, path, content,
umask)` makes each missing directory (`Mkdir`, then `Chmod` to `0o777 &^ umask`, keeping setgid —
which Linux drops anyway for a user outside the parent's group;
an `Lstat` that finds anything but a directory where one was missing is `ErrWrite`, so a planted
link is never followed), then puts the file (`0o666 &^ umask`) in place by a rename that refuses
to replace, a `link` where the filesystem has none, and an `O_EXCL` write where it has no hard
links either (`renameNoReplace` and `link` are package variables a test swaps). Both check `ctx`
before their first change on disk and before the rename, remove what they made on any failure,
sync the directory on Unix, and answer `ErrCancelled`, `ErrExists`, `ErrReplace` or `ErrWrite`.

**The root forms**, beside their path forms in `fileguard.go` and `write.go`, take an `os.Root`
and a name under it — `LstatIn`, `OpenIn`, `ReplaceableIn`, `CreatableIn`, `ReplaceIn`,
`CreateIn` — and refuse what their path forms refuse. The root refuses a name that leads out of it; a link at the name is `ErrLink`, and
`CreatableIn` refuses a link anywhere on the way. `OpenIn` refuses a file that is not the one its
`Lstat` saw (`ErrChanged`), since the root follows a link that stays inside it. `os.Root` exposes
no descriptor, so each step is a call the root offers: the temporary file an `O_EXCL` open under
`.<name>.kstack-<rand.Text()>`; a new file placed by `Link` from it (`linkIn`, which a test swaps),
which fails on a file there, then an `O_EXCL` write where `linkUnsupported` (on Windows,
`ERROR_NOT_SUPPORTED` and `ERROR_INVALID_FUNCTION`); a replace by `Rename`; each missing directory
by one `Mkdir` with an `Lstat` after it; and the sync by opening the directory through the root.
**Writability is the owner's mode bits** (`replaceableIn`, `dirWritableIn`): the file and its
directory the user's, `u+w` and `u+wx`, stricter than `access(2)`, since everything in the
workspace is the user's. Windows checks the read-only attribute alone.

**`internal/tools/anthropicwebsearch` is the Messages API's web search**, a `Budgeted` tool the
provider runs, five a turn: `Name()` is its contract's string, `Prompt()` is `prompts/prompt.md`
with the month its clock reads at each call (the sidecar outlives a month), `Action` is the query
off the provider's arguments, and `messages.go` is its `llm.MessagesServerTool` —
`web_search_20260318` with `allowed_callers: ["direct"]`, and the SDK's constants for the names it
reads. A server tool's package is named for its vendor, whose SDK it imports.

**KubeQuery (`tools/kubequery`) is read-only SQL over the chat's cluster's cache, run without
asking.** It is `Custom` and `Bounded` (10 s, whatever the input), never `Gated`. `parse` is the
strict walk: `sql`, `limit` (200 unless given, read as 2000 past it) and `description`; a `;`
may only end the statement, and is dropped there (`semicolonMessage`, the first `bad-input` to
carry a `message`, names `char(59)`). `Run` reads the cluster service on `Runtime.ClusterID` (`cluster.go`): one
`Clusters().ReadActive`, the active cache's health, and `CachedData().Query` unless
`Freshness(&health).Withholds()` — `syncing` or `unknown`, and a cluster with no cache — so the
rows and the verdict describe one cache. It answers a `*clustersvc.QueryError` as
`{"error":"sql","message":…}` through `safe.String`, a gone record, a moved identity or a gone
cache as `no-cache`, and anything else as `read-failed` with none of its text. **The answer** is JSON:
`cluster`, `freshness` (the card's section), `columns`, `more`, then `rows` last and one to a
line; a withheld result is the first two alone. Each cell renders by its type: a text cell whose
first byte is `{` or `[` goes through `safe.RedactJSON` and is nested when it is JSON, any other
through `safe.Redact`; a blob is `<blob N bytes>`, NaN and ±Inf `null`. The render stops before a
row past its bound and checks its context between rows; past `InlineLimit` it is saved and
answered through `Persisted`, and a failed save renders again within `InlineLimit`, so no answer
ends mid-row. A head that passes the bound before any row — column names too long — answers
`{"error":"too-large",…}` instead, since dropping rows cannot bring it within. The section (`prompts/kubequery.md`) lists the views that are built and nothing
a later step adds. → [ADR: the cluster is kubectl in Bash, and KubeQuery over the mirror](../docs/adr/2026-09-27-the-cluster-is-kubectl-in-bash-and-kubequery.md),
[ADR: KubeQuery reads views on a connection of its own](../docs/adr/2026-09-27-kubequery-reads-views-on-a-connection-of-its-own.md),
[ADR: KubeQuery reads tables written with the object](../docs/adr/2026-09-27-kubequery-reads-tables-written-with-the-object.md),
[ADR: KubeQuery reads without asking](../docs/adr/2026-09-27-kubequery-reads-without-asking.md).

**A binding is a `Tool` over a capability.** The capability runs something and knows no
contract; the binding parses its contract's input into the capability's request and renders
the outcome in the contract's shape. Two bindings of one capability share its code by
construction. No capability ships yet.

## Agent (`internal/agent`)

The loop that takes a turn. `agent.Run(ctx, Turn, Recorder, Approver) (Result, error)` runs on the
calling goroutine: the model is asked, the calls it asks for are run and answered, and it is
asked again with the rounds so far as the last assistant message. A `Turn` is the `llm.Target`,
the caller's system prompt, the messages, the `AffinityKey` every round's request carries, the
`tools.Box`, the `tools.Runtime` handed to every
call's `Approval` and `Run`, `MaxToolCalls` and `DefaultToolTimeout`;
the type is `Turn` and the verb `Run`, since a package's types and functions share one
namespace. The box is already chosen for the target (`tools.Box.For`): each round's offer is its
`Offer()`, a call's tool is `Runner(name)`, and "What you can do" is its `Prompts()`. A `Result` is the rounds — every reply's blocks and every result, in order — and
the last reply's stop reason, valid on every path but a refused start.

**Every `tool_use` in the rounds has its `tool_result`, in the reply's order.** A stop other
than `tool_use` ends the turn, a call left in it answered `not-run`; a `tool_use` with nothing
to run is malformed and ends it too. A refusal the loop writes is `{"error":"<code>"}` with
`IsError`, never a Go error's text: `budget`, `not-run`, `unknown-tool`, `timeout`,
`cancelled`, `bad-input`, `denied`. Each is an exported `agent.Code`, whose `Text()` is that
object and which `RefusalOf` reads back, so chat keys on the constant rather than a spelling of
its own.

**The budget counts calls, not rounds.** A reply asking for more than remain is refused whole,
so the model is never answered for half of what it asked in one breath, and gets one more
round, the synthesis; a second such reply ends the turn on `tool_use`. Every round that
continues spends a call, so a turn streams at most `MaxToolCalls + 2` times.

**`DefaultToolTimeout` is cooperative**, and a `tools.Bounded` tool names its own bound in its place
(`CallTimeout`): the loop waits for `Run` to return, and a tool must honour
its context. A result with no error is kept whatever the clock says; an error after the turn's
cancel is answered `cancelled`, after the call's deadline alone `timeout`, the cancel checked
first. A turn cancelled between two calls starts no more: the rest are `cancelled` unstarted.
A name the box lacks is answered `unknown-tool` with no `ToolCallStarted`.

**A reply's calls run one at a time, in reply order, and a gated call waits on the user.** A
`tools.Gated` tool (found by type assertion at the call) is asked for its `Approval` after the
lookup and before `ToolCallStarted`: an error is answered `bad-input` with nothing started —
or, for a `*tools.Refusal`, with the result it carries — and otherwise `Approver.Approve(ctx, call, approval)` decides. A `tools.Approval` is what the gate
records on the call's row: `Cwd`, where it will start (`''` for a tool that runs nowhere),
`Sandboxed`, whether a sandbox confines it, `Skip`, which runs the call with nothing put to the approver, so its row is an ungated call's —
no approval, `is_mutating` 0 — `Network`, the `session.Network` a sandboxed call runs with
(`NetworkChat`, `NetworkTurn`, or `NetworkApproved`, which holds once the user approves; `NoNetwork`
for none), and `Folder`, the granted folder a skip rests on, nil for any other, so a file tool
reaches the folder its approval named and never reads the session's folders again. `gate` hands the approval on to `ToolCallStarted`, asked or
skipped (the zero value for an ungated tool), so a skipped call's row still says where and how it
ran, and to the run: **a `tools.ApprovedRunner`** (`RunApproved(ctx, rt, input, approval)`) is run
by it in place of `Run`, so what runs is what the gate decided whatever the session says by then;
Bash is one. The zero value asks, so a tool that forgets asks; Bash,
`Read`, `Write`, `Edit`, `WebFetch` and `Memory` set it. What the user decides on is the call's action, read
from its arguments by the tool (*Tools*, above). A
no is answered `denied` and the next call is asked; a yes checks the turn's cancel once more,
so an approved call whose turn was cancelled in that moment is answered `cancelled` and never
starts. A decision stands whatever the clock says, since the approver committed it. An error
after the turn's cancel is the wait answering it — this call and the rest `cancelled` — and any
other is a write that did not land — this call and the rest `not-run`, the turn over. One
approval request is before the user at a time, and a denied first command never holds a second.
→ [ADR: the gate is the tool's card](../docs/adr/2026-09-22-the-gate-is-the-tools-card.md),
[ADR: a tool call shows itself from its arguments](../docs/adr/2026-09-23-a-tool-call-shows-itself-from-its-arguments.md).

**A write that fails stops what would have followed it.** A failed `ToolCallStarted` runs nothing:
that call and every later one in the reply are `not-run`, and the turn fails with the write's
error. A failed `ToolCallFinished` keeps the result it was told and does the same for the calls
after it — a refused budget whose refusal did not land gets no synthesis round. A failed
`LLMCallFinished` ends the turn only under a reply that asked for tools; otherwise it is
returned for the caller to log, and the turn settles on its own outcome, which the settle's
rewrite of the row heals.

**Server tools have a budget of their own, per turn.** The box's `Budgeted` tools are what the
turn may offer, each at its `MaxUses` for the whole turn. Each round is offered what the earlier
rounds left (`offer`), keyed by the tool's name, and a tool with none left is not offered, since
no wire takes a cap of zero. A round's uses are its `ServerUses`, or, where the usage reported
none, the `server_use` blocks it holds under the tool's name. **A reply that stops on `pause_turn` with no call** is the provider pausing its own loop:
it is asked again with its blocks as the last message, at most `maxPauses` (3) times, and only
while every server tool has uses left (`serverToolsLeft`), since a request that stops offering
one drops its calls from the history, the paused reply's included. Otherwise the turn settles on
`pause_turn`. A pause spends no tool call.

**The recorder is told before and after each external step, in order, on the run's goroutine.**
`LLMCallStarted` before each model call, an error stopping the turn with nothing sent. `Progress`
per chunk with the rounds so far then the reply in flight, once a reply that asked for calls
lands (no chunk carried them), and after each call is answered, so a turn that ends inside a
tool keeps the calls and the results before it; it cannot fail, since a failed display must not
stop a paid call. `ServerCallSeen(call, tool)` for each server call as the stream shows it, with the offered tool
its block names — a `tools.Native`, so the row can record its `ContractName()` — before the `Progress` that carries it. `LLMCallFinished(ctx, resp, err)`
after each stream, with its response, `ToolCallStarted` before a tool runs, `ToolCallFinished` exactly once per call, run
or refused, and `Settled(ctx, result, err)` once, last, on every path out, its error joined
onto the return. Chat's `turn` implements all seven.

**The agent assembles what the model is told** (`prompt.go`): the turn's own prompt, then
`# What you can do` (`prompts/no_tools.md` for an empty box, or `prompts/tools.md`, which states
`MaxToolCalls` and that a gated call waits on the user, with every tool's `Prompt()` in box order
under it, the provider's search included, each opening with its own `##` heading), then `# Data is not instructions`. The last is the agent's because a tool's
results are data and its sections lean on the rule. Its one exception is the user's memories: the
notes marked `"by":"user"` in the `## Memory` section of the newest `<context>` block. `tools.md` also spells out each refusal
code the loop answers with, so a refusal the model reads is one it can act on. Golden files under `testdata/` keep
the document readable as one. The loop imports `llm` and `tools` alone; its tests run on the
fake, a test tool and a logging recorder.

## Chat (`internal/chatsvc`)

> **This code is being rebuilt, and this section is the spec for it.** The chat code was
> removed for a rewrite and is being rebuilt into four layers, `llm` → `tools` →
> `agent` → `chatsvc`. What exists:
>
> - The record: the types, the statements and row helpers, `Get`, `List`, `Rename` (the
>   title trimmed, refused empty or over `maxTitleLen`), `SetSandboxDisabled` (the user's
>   switch, `ErrBadRequest` on a machine with no sandbox, `updated_at` left alone, and leaving
>   the sandbox turns the network switch off in the same write), `SetNetworkEnabled` (the
>   network switch, `ErrBadRequest` turning it on where network is unavailable or while the
>   chat is outside the sandbox, `updated_at` left alone), `Delete`,
>   the two watches, and the chat sweeper.
> - The lifecycle: `Start` fails the stranded runs, closes their model and tool calls
>   (`{"error":"stranded"}` on a tool call) and starts the sweeper; `stop` cancels the turns and joins them with the pumps and the sweeper.
> - The turn: `Send` resolves the provider and model it names through the llm
>   service (`New(db, chatsDir, llmSvc, clusterCards, memories, box, lists, sandbox)`: the box every turn is offered
>   from, which reads every stored call, and `ToolLists`, the one method it calls of the
>   catalog, `ToolsFor(target)`, taken the way it takes `ClusterCards` so its tests list their own
>   tools), writes its rows and runs one
>   `agent.Run` on them (`Turn.Target`, with `maxToolCalls` and `defaultToolTimeout`, and the
>   chat id as `Turn.AffinityKey`; a subagent's turn carries its run id);
>   `Cancel` cancels it; every read overlays the live answer. The question is its
>   text, with the cluster card ahead of it as a `context` block when the card changed
>   (*A question carries a cluster card when the card has changed*, below). `Turn.SystemPrompt`
>   is `prompts/system.md` (`prompt.go` embeds it): who the agent is, the card, how to
>   answer — every cluster command naming the card's `cluster.context`, since the app's
>   cluster need not be the kubeconfig's current one — how to make a change (a GitOps-owned
>   object through its repository, on a branch, validated and committed, pushed only when
>   asked), and the format the webview renders. It says only what is true of the app now, and
>   it is the opening of what the model reads — the agent appends what the turn can do and
>   the standing rule that data is not instructions. **Every send
>   names its model and effort, checked against the llm service after the replay lookup**
>   — a replay answers by `requestID` alone, and a check ahead of it would refuse a retry
>   of a send that already ran because the catalog moved in between; a refusal is
>   `ErrBadRequest` with nothing written. What is stored is what the send named, and the
>   run records the provider's dialect. On the wire a message's provider is labelled
>   through the llm service; one it no longer holds is labelled by its id with no dialect.
>   **A provider that refused to read the chat** (`llm.Error.ContextFull()`) settles the
>   answer failed with `contextFullText`, the one spelling the transcript draws, while the
>   call's row keeps the provider's own error (`callError`); `roomFor` refuses a send ahead
>   (below). An answer that failed with no text is left
>   out of the next turn's history, so two questions can follow each other; the APIs fold
>   consecutive same-role messages into one. **A row's tool blocks go into the history only
>   when it settled `Complete`**, since only such a row holds a result for every call it
>   asked; a cancelled or failed row is sent as its text alone.
> - The rows the recorder writes: an `llm_calls` row per round, `seq` from 0 within the run,
>   the run claimed on the first alone (`messageReadColumns` serves the latest round's stop
>   reason, so `finishReason` is the last's); a `tool_calls` row per call, `running`
>   **committed before the tool runs** and written whole again with its outcome — a call the
>   loop refused gets its one row at `ToolCallFinished`, `failed` with the code and no
>   `started_at`. **`result` is what the model read on every closed row**, a refusal
>   included, and `error` is a JSON object: the loop's refusal when the text is one, else
>   `{"error":"tool"}` (`toolErrorOwn`), so a tool's own error is not stored twice. Its `seq` is a counter reset at `LLMCallStarted` and advanced at
>   `ToolCallFinished`, the call's place among the reply's `tool_use` blocks; a running row the
>   store refused is not kept, so the refusal that follows writes the call's one row.
>   **The settle writes every call row again**, after the run's status, so a write a trigger
>   refused while the run was live is healed; a model call's row keeps its own stop reason
>   or stream error, never the turn's, and a tool row still open — a tool that panicked —
>   is closed `failed`, `{"error":"interrupted"}` (`closeOpen`). A round's row names the model
>   the provider said it served (else the one asked for; the run keeps the catalog id the
>   send named) and its `first_chunk_at` (`LLMCallFirstChunk`, the first text or thinking
>   chunk), and a round the run's cancel ended reads `cancelled` (`callError`); the run's own
>   error for a cancel stays empty.
> - Server tools: a turn's box is `s.tools.For(target, s.lists.ToolsFor(target))`
>   (`service.box`), so it is offered the tools its provider's list names that its target takes,
>   each at the tool's own cap and with its own prompt section, and none for a model that takes
>   no tools. **A server call is a `tool_calls` row** with `runs_on = 'provider'`, `tool_name` the
>   tool's name — `anthropic_web_search_20260318` for the search — `contract_name` the vendor's
>   own identifier for its shape (`web_search_20260318`) and `arguments` its input, and no status, span or result, which two CHECKs
>   hold: `ServerCallSeen` keeps it the moment the stream shows it, `LLMCallFinished` writes
>   it with its model call's row (`server_uses`, the count), and the settle writes it again
>   and closes nothing of it; the stranded sweep leaves it alone. Its action is read through
>   the box by its name alone (`anthropicwebsearch.ActionOf`), and `server_uses` is keyed by the
>   tool's name. `ChatMessage.Citations` is `llm.Citations` over
>   the content by its run's dialect (`agent_runs.dialect`), on the stored read and the live one. On the wire a
>   `ToolCall` carries `runsOn` and `contract`, and a provider's has a null `status`.
> - The gate and the list: a gated call's rows and the wait (`approval.go`), `Approve` on
>   the service, the bash tool (`internal/tools/bash`), `Read` (`internal/tools/read`), `Write`
>   (`internal/tools/write`), `Edit` (`internal/tools/edit`), the
>   chat's directory (`chatdir.go`), and `ChatMessage.ToolCalls` — each described below under
>   its own name.
>
> - The subagent: the `Agent` tool (`internal/tools/agent`), the turn as its spawner, the
>   subagent's rows, approvals and notices (`subagent.go`) — described below under *A turn can hand a
>   task to a subagent*.
>
> Not yet: the native bash and shell contracts, and the prompt sections that describe them. Every paragraph below describes the code as it was
> before the rewrite, kept as the list of invariants the rebuild has to reproduce and rewritten as each
> lands. A paragraph still in the old names is an invariant not yet rebuilt.

**Sending a message is a save, not a phone call.** `Send` writes three rows — the question, its
queued run, the empty answer — and returns; the turn runs on a goroutine the service owns and the
answer shows up in `app.db`, where every window watching that chat sees it. → [ADR: the answer is a
record](../docs/adr/2026-09-10-the-answer-is-a-record.md), [ADR: chats live in
`app.db`](../docs/adr/2026-09-10-chats-live-in-app-db.md), [ADR: a turn is a run over its
message](../docs/adr/2026-09-17-a-turn-is-a-run-over-its-message.md).

```
internal/chatsvc/
  record.go      the value-typed records the wire carries: the ids, the enums, Chat,
                 ChatMessage
  service.go     every method on *service: the errors, New/Start/Close, the
                 mutations, the two watch pumps
  turn.go        the turn: the per-chat reservation, the goroutine that runs
                 agent.Run, the agent.Recorder it reports to, the live overlay
  subagent.go    the subagent an Agent call starts as a task: the turn as its
                 tools.Spawner, the agentTask that runs it, its recorder and its end
  approval.go    the gate: the agent.Approver a run is, the waiters, Approve
  prompt.go      the system prompt, embedded from prompts/system.md
  store.go       one function per statement over a stmts (sqlstmt.Stmts) that reads
                 or writes the records, the scanners, the run-status mapping
  statements.go  the table: every statement's text and the pool it is prepared on
  stream.go      Stream[T], the frame types, the two deltafold folds
  sweep.go       the chat sweeper: a marked cluster's chats go, on the clusters signal
                 and on its own retry; the chats' directory's start sweep
  chatdir.go     the chats' directory: its root, a chat's `chatDir`, its removal
  files.go       each chat's file stamps, in memory, dropped with the chat
  tasks.go       background tasks: their slots, start, watcher and stops
  notices.go     how a task ended, told to the model: on a question, or a turn of its own
```

**Each chat's files live in `<data>/chats/<chatID>`** (`chatdir.go`): `results/`, `tasks/`,
`workspace/` and `toolhome/` (*Tools*, above), so all of them go with the chat, and none names the chat's
cluster. `New` makes the chats' directory 0700 and opens it as an `os.Root` (`openChats`), closed on
`Close`; its path is absolute, since a result names its file under the root's name and `Read` takes
only an absolute path. `chatDir` is a chat's `tools.ChatDir`, built from its id: `Root` is
`rootdir.Open` of the chat's entry, so a link a command swaps in reaches no other directory. The
turn's box is `s.boxFor(t.target)`, and its runtime is `tools.Runtime{ClusterID: t.clusterID, ChatID: chatID, Session: t.session(), Dir: s.chatDir(chatID), Tasks: s.chatTasks(chatID, t.runJournal), Files: s.chatFiles(chatID), Agent: t}`, `t.clusterID` read once as the run starts (`chatOf`) and `t.outsideSandbox` once in the transaction that reserves the turn, beside the context block that tells the model, so a switch flipped mid-turn changes the next turn; a subagent and a task take both from the turn that started them, a subagent's session being `session.Narrow(t.session())`.
**A chat's file stamps** (`files.go`) are one map per chat under `stampsMu`, never persisted:
`Delete` drops them once the rows are gone, and a restart forgets them all, which fails closed.
**`Delete` refuses an id that is not a UUID** (`ErrBadRequest`) before touching anything: the id
names the directory to remove, and `<chat>/out.txt` would reach into a live chat's files. It reads
nothing before its write, and **removes the entry once the row's write succeeds**
(`removeChatDir`, `rootdir.RemoveAll` under the chats' root); the turn is already joined, so
nothing of the sidecar's writes there after. An entry already gone is a removal done, and a
removal that fails is logged. **The start sweep** (`sweepChatDirs`, in the sweeper's loop after its
first sweep, so neither `Start`, which the first request waits on, nor that sweep waits on a gone
chat's large workspace) lists the chats' directory, **then** reads the chats, and removes every entry that is not a
chat (`rootdir.Sweep`): a send can run before `Start`, and it commits its row before its turn
writes anything.

**Nine tables** in `0001_init.sql`, the only schema authority: `clusters` (`clustersvc`'s),
then this service's `chats`, `messages`, `agent_runs`, `llm_calls`, `tool_calls`,
`approvals` — the user's decisions on a call: its own, one per gated call, and each cluster
write its sandboxed command sent (below) — and
`background_tasks`, one row per command started in the background (*Background commands*, below),
and `chat_grants`, the rules that last for a chat (below). In Go and on the wire a chat is a `Chat` with a `ChatID`, and a message a
`ChatMessage` with a `MessageID`. A chat carries `sandbox_disabled`, the user's switch
(`Chat.SandboxDisabled`, 0 at creation), `network_enabled`, the user's network switch
(`Chat.NetworkEnabled`, 0 at creation, written by `SetNetworkEnabled`, which refuses turning it on
with `ErrBadRequest` where `sandbox.Status.NetworkAvailable` is false or while `sandbox_disabled`
is set, and always accepts off; `SetSandboxDisabled(true)` clears it in the same statement, so the
switch is on only while the chat is in the sandbox and a chat back in it starts without network), and a `mode` column — which of the app's two modes lists it, fixed at creation and checked by
the column, since each mode shows only its own chats — and a `cluster_id`, the `clusters` row it
was started under, fixed at creation too: each cluster lists only its own chats. It references
`clusters(id)` with `ON DELETE CASCADE` as a backstop; the sweeper (below) empties a marked cluster
before its row goes. `title` is nullable and read as `''`. **Ids are UUIDv7 from `appdb.NewID`,
identity alone**: the transcript's order is `messages.seq`, a per-chat counter the send
transaction assigns from `MAX(seq)` inside the writer's transaction (`_txlock=immediate`), and the
list's is `updated_at` — v7 orders within one process only, and a clock moved back between runs
would file a new message before a persisted one. Times are unix millis. `content` is the Messages
API's content blocks **verbatim** — the API is stateless, so every turn resends the whole
conversation and a prettified copy would have to be turned back into a request each time. What
to draw comes from the blocks, never from the role alone: a `user` message carrying `tool_result`
blocks is machinery, and thinking blocks have to go back unchanged. The roles are `user` and
`assistant`; there is no system role. `chats.updated_at` moves when a row is created or
settles, and is never worked out from the messages later. Two things do not move it: a
checkpoint, which notifies nothing, so a timestamp it moved would be one the list never hears
about; and the startup reconcile, because the send that stranded the run already stamped the
chat — stamping it again would jump every interrupted chat to the top of the sidebar for
work the user did not do.

**A message is what a client posts; a run is what the server does about it.** `agent_runs` is
one row per execution: for a chat turn, `trigger = 'chat'`, `trigger_message_id` the user message
it answers (unique, so a message starts at most one run), and the assistant message's `run_id`
pointing back at it — plain references both ways, cascaded off the chat alone, so a
chat goes with **one `DELETE`** (`stmtDeleteChat`), which is what lets SQLite check
the two references after both sides are gone. **`messages` has no status and no model columns.**
An assistant message's public `Status` is its run's (`messageStatusOf`: `queued` and `running` →
Streaming, `waiting_approval` → WaitingApproval, `succeeded` → Complete, `failed` → Failed,
`cancelled` → Cancelled; `inFlight` is the first two); `Error`, `ProviderID`, `ModelID`, `Effort` and `FinishedAt` are the run's `error`,
`provider`, `model`, `effort` and `finished_at`, and `RunID` is the join. A user message has no
run and reads Complete with all of those empty. `effort` is stored NULL where the model has no
such knob (`nullString`) and read as `''`; `app_version` is `version.Version`, the build whose
prompt and tools ran. **`dialect` is the `llm.Dialect` of the provider the run was sent to**,
written from the target for a chat's run and a subagent's alike and `CHECK`ed non-empty alone,
since the set is `llm.Dialects`' to list: it is what reads the run's content (its citations)
after that provider has left the catalog, and a message's `dialect` is its run's, `''` on a
user message. A chat has no dialect of its own. The read is `messages LEFT JOIN agent_runs` (`messageReadColumns` +
`messageReadFrom`), and `stmtSelectAnswerByRequestKey` aliases the answer `m` so a replay scans through the
same `scanMessage`. **`FinishReason` is the run's latest non-null `llm_calls.stop_reason` by
`seq`**, a correlated subselect, on every run status.

**A send is one transaction** (`Send` → `writeTurnRows`): after the replay lookup — and, outside
the transaction, `ErrBadRequest` for `networkThisTurn` where network is unavailable — `checkChat`,
which answers the chat, and the send refuses one whose `sandboxDisabled` differs with
`ErrChatSandboxChanged` and one whose `networkEnabled` differs with `ErrChatNetworkChanged`, so the
turn runs where and with what its sender saw — and `resolveChat`, it reserves the turn with a fresh
`RunID`, pins its switch and its toggle (`turn.outsideSandbox`, `turn.networkThisTurn`), which
`turn.session()` hands `sessionFor`, reads `nextSeq`, and inserts the user
message carrying the client's `request_key`, the queued run, and the assistant message with
`emptyContent` (`[]`) and the run's id — in that order, since each references the last. IDs are
minted inside the transaction. **The turn is the agent's recorder** (`turn.go` implements
`agent.Recorder`): `LLMCallStarted` claims the run (`claimRun`, `queued` → `running`,
`errRunNotQueued` when it was not) and opens the call row in one transaction before the model
is asked, so a cancel that landed first, or a claim that fails, asks no model and the run is
settled by the goroutine — cancelled from queued, or failed. **The run is recovered** (`run`):
the stream is third-party code on a goroutine of the service's own, so a panic in it settles
this turn failed with the panic as its error rather than ending the process; `settle` marks the
turn `settled`, and the goroutine settles whatever the run did not. `Progress` sets the live
message and pings the chat's messages key, and every `checkpointEvery` (3s, counted from the
answer's creation) writes the live content to the row, notifying nothing; a checkpoint that
fails is logged and skipped, since memory is the truth until the settle. The turn's own
`LLMCallFinished` also gives the live message the round's stop reason, as the stored read will
serve it; a subagent's run has no live message. `Settled` is the settle. **Settlement is one
transaction** (`settleRow`, behind the `settleWrite` seam): `writeContent` (the result's blocks,
else the text so far, so a turn that broke off keeps what the reader saw), then `settleRun`
(`cancelled` on a context error, `failed` with the error's text otherwise, `succeeded` on a
clean stream; `error` NULL when empty, `finished_at`), then every call row (`writeCalls`), then
`touchChat`. `turn.settle` fixes that outcome and its time once and shows it on the live
message, still `Streaming`, before the first attempt; every attempt writes the same thing, on the
turn's context without its cancel, so a cancelled turn still lands. **A settle that fails is
retried** (`settleUntilLanded`): the first failure is logged and closes `retrying`, each wait
doubles from `writeBackoff` (250ms) up to `maxBackoffSteps` (16) times it, and the landing logs
how many attempts it took. The slot is held throughout, so a reader keeps the answer and a send
is refused. The retry ends when the row lands, when the service stops — the row stays streaming
and the next `Start` fails it as stranded — or when a delete's write takes the chat (`gone`).
There is no worker queue; the claim is the turn's own until a second kind of run exists.

**A call is a row before it happens.** `llm_calls` holds one row per model call, under its run
(`run_id`); `tool_calls` one row per tool call, under the model call that asked (`llm_call_id`)
— the audit trail of what touched the cluster. Ids are `LLMCallID`s and `ToolCallID`s, minted as each call
is about to happen, and **order nothing**: a model call's `seq` is its position in the run, a tool call's
its position among the reply's `tool_use` blocks (a refused call keeps its place), both assigned
by the loop and unique under their parent, so a run resumed in another process orders the same.
**`callRows` (`tools.go`) is the one source of a tool row's `seq`**: it walks the reply's
`tool_use` blocks once and gives each its rows — one, or one per command for a shell call
whose action parses — and every path that writes a row (`runCalls`, `refuse`, `refuseUnrun`,
a pre-run write failure) takes its slice, so a shell call's rows and a refused call's never
collide on the unique index. The stop-reason read takes the newest by `seq`. Every write is a full-row upsert by id (`upsertModelCall`, `upsertToolCall`), so a later write heals an
earlier one that never landed. A model call's row goes in before `Stream` and is written again
when its reply ends, with `stop_reason`, the model the provider said it served, `first_chunk_at`
and the usage — `server_uses` among it, one JSON object from behaviour word to the count the
provider reported, NULL where it reported none. `first_chunk_at` is the first text or thinking
chunk; a server call is neither. A tool call's row goes in `running` immediately before `Call` and is written
again with `succeeded` + `result` or `failed` + `error`. A call the loop refuses (`budget`,
`not-run`, `unknown-tool`) never runs: its row is `failed` with no `started_at`, written at
settlement (`refused` over the row `callRows` minted). A tool row's `error` is always a JSON object (`toolErrorJSON`,
the same text the result block carries); a model row's is safe plain text. Every row carries
`tool_use_id`, the provider's id of the block it answers. `is_mutating` is 1 on a bash or shell
row and 0 on every other; `spawned_run_id` is set on an `Agent` call alone.

**A turn can hand a task to a subagent, which runs in the background.** `Agent`
(`internal/tools/agent`, imported as `agenttool`) is a tool in the box like any other: `Custom` and
`PerTarget` — each turn is offered a copy whose `model` enum is its provider's models that take
tools — and not `Bounded`, since the call only starts the agent. It is not `Gated`: each call the
subagent makes is gated on its own. Every provider's list names it. A call hands a
`tools.Delegation` to `rt.Agent`, the `Spawner` the parent's runtime carries and a subagent's does
not, and answers at once with `Agent launched: <id>. …`, the reference's words; `ErrUnknownModel`
answers `bad-input` on `model`, and any other error `tools.StartRefusal`'s words, which bash's
share: each limit naming commands and agents, else `could not start: …`. **The turn is the spawner** (`subagent.go`): `turn.Start` resolves the
subagent's target (`subagentTarget`: the parent's, effort included, for no model or its own; else
that model of the parent's provider at its default effort, when it takes tools), reads the chat's
newest card (`newestContext`), then starts a task through `startTask` with a `taskRecord` that
writes the subagent's run `running` (`stmtInsertSubagentRun`: `trigger 'agent'`, `parent_run_id`,
`task` the prompt) and the open `Agent` row's `spawned_run_id` in the task row's transaction. Its
start checks the call's context first, so a Cancel during the row write starts nothing, and a start
that fails takes the run back (`deleteRun`; the link goes with it, `ON DELETE SET NULL`) with the
task row; the turn goes on. The link lands on the parent's `openTool` only once the start
succeeded, since the parent writes that row again when the call finishes. **The subagent is
`agentTask`**, a `tools.Task` whose goroutine runs one more `agent.Run` under a context only a stop
cancels — so a parent's Cancel leaves it running — then writes the report into the task's file, up
to `FileLimit`, and closes it; a panic in the stream fails it. `Wait` blocks until then and answers
a zero `Exit`; `Stop` cancels at once. The loop's spec: `subagentSystemPrompt()` (`system.md`, then
`prompts/general_purpose.md`), one message of the card then the prompt (`subagentMessage`),
`boxFor(target).Without(tools.ActionDelegate, tools.ActionMemory)` — so depth is one, a subagent's
`Agent` call is `unknown-tool`, and a subagent writes no note — `maxSubagentToolCalls` (16), a `runStamps` of its own, since a stamp says the model
saw the file, and `chatTasks` over its own calls. **Each run owns its own calls**: `runJournal`
holds its service, chat, run, target, the `Agent` call a subagent's run is under (`""` on a
turn's own, which `isTurns` reads), the bound on its waits for the user (`unansweredLimit`, zero on
a turn's own), its model and tool calls, open call and next seq, and a `publish` func — a turn's publishes into its
live message (`publishLive`), a subagent's notifies the chat's watchers, since every row it writes
has landed first. The parent and its subagents share no memory. A `subagent` publishes no
`Progress`, and its `Settled` writes nothing: it keeps how the loop ended — the report is the text of
its last reply, `llm.Text` of the blocks after its last result (`lastReplyText`) — for the task's end. **`watchTask` is the one writer of the end**:
after `Wait`, `subagent.end` names it off the run's outcome — `stopped` with `stopped_by` for a run
that settled cancelled, since only a stop cancels it; `completed` for one that answered; `failed`
for any other, its own error or nothing to say — and `finishRow` writes the run terminal with its
report (`taskEnd.Run`), every row of the run whole again (`taskEnd.Rows`, `writeCalls`, which heals a
write that did not land and closes a call a panic left open) and the task's end, in one transaction;
when that fails, once more without the rows, so the task and its run still end. A write that fails
inside the subagent ends it alone. The stored read attaches a subagent's calls to the answer whose
`Agent` call links them (`withToolCalls`), and so does a repeated send's (`stmtSelectRunToolCalls`).
→ [ADR: an agent is a tool in the box](../docs/adr/2026-09-25-an-agent-is-a-tool-in-the-box.md),
[ADR: an agent runs in the background](../docs/adr/2026-09-25-an-agent-runs-in-the-background.md),
[security records: the Agent tool](../docs/security/2026-09-25-agent-tool.md),
[background agents](../docs/security/2026-09-25-background-agents.md).

**A reply's calls run one at a time, in reply order** (`agent`). An `Agent` call only starts its
subagent, so a subagent's requests wait beside the parent's and each other's.
→ [ADR: the gate is the tool's card](../docs/adr/2026-09-22-the-gate-is-the-tools-card.md).

**The native tools a run gets are the box's** (`tools.Box.For`): each vendor tool a target takes,
beside the app's, one per `ActionKind`, a server tool at its own cap with its own prompt section,
and a client tool — none yet — an adapter in its runner's package sharing that runner's gate.
The rebuild's native bash and shell contracts arrive as tools in that box, never as tables in
chat. → [ADR: every tool is in the box](../docs/adr/2026-09-24-every-tool-is-in-the-box.md).

**A turn can run a command, once the user says so.** Bash is one tool in the box `chatsvc.New`
takes, like any other, and every turn on a model that takes tools is offered the same `bash.Tool`,
given its chat. **`app.go` offers it wherever `bash.New` finds a shell** (over the sandbox `New` probed, which it logs; `sandboxStatusOf`
builds the one `sandbox.Status` from both, available only with a shell and a sandbox — a sandbox
with no shell clears the network answer with it (`TestNoShellOffersNoNetwork`) — which
`chatsvc.New` and `graph.Resolver` take; `chatTools`, the one
`tools.NewBox`), then Read, Memory, Write, Edit, WebFetch, TaskStop, the provider's web search and KubeQuery; a machine with none is
offered Read, Memory, Write, Edit, WebFetch, the search and KubeQuery, and reads bash's stored calls through `bash.Reader`. Read, Write and Edit take Kstack's
directories (`Paths.DeniedDirs`) and build their own fence around them, with the security service's
`Hidden` as what the sandbox keeps shut under a grant; `chatTools` runs once `makeDirs` has made them, and
`app.New` fails to start without them. For bash, `New` reads the version once with `bash --version` (never
`-c`, which sources `BASH_ENV`), and the prompt's `- Shell:` line names it — on a bash older than
4, macOS's own 3.2, followed by what it lacks; a version that cannot be read reads `bash` alone and
the tool is still offered. Under zsh the line reads `zsh`. The tests offer a fake that embeds the real `bash.Tool` for its offer and approval
over a scripted `Run`, and its own `At`, since the embedded `Tool`'s would run the real bash. Bash is `tools.Bounded`, **per call**: `CallTimeout(input)` answers the
call's own timeout plus `killGrace` (5s) plus `callMargin` (5s), so a command stopped at its
timeout answers with its own result rather than the loop's `timeout`, while every other tool
keeps chat's 2s `defaultToolTimeout`. → [security records: the bash tool](../docs/security/2026-09-18-bash-tool.md), [bash offered to every turn](../docs/security/2026-09-22-bash-offered-to-every-turn.md).

**`internal/tools/bash` is the tool.** `New(Paths{ShellDir, RunsDir, TmpDir, KubectlDir, DeniedDirs}, hostPID, sandbox, clusterSvc, pathList)` finds the shell — on Unix
`loginshell.Find()` when its base name is `zsh` or `bash` (`Tool.kind`), else bash on `PATH`;
on Windows only Git for Windows', through Git's own install record (`SOFTWARE\GitForWindows`,
both `HKLM` registry views then `HKCU`, `readInstallPath` the test seam) and never off `PATH`,
where `bash.exe` may be the WSL launcher — and the user's home directory; `ok` false offers no
tool. Tests that want bash whatever the machine's login shell clear `SHELL` (the `tool` helper
does). **The sandbox is reached through `Tool.sandboxer`**, a `sandboxer` (`Command`, `System`, `Never`, `Confines`, `Port`) so a test can stand
in for it, set only for a non-nil `*sandbox.Sandbox`. A call is **sandboxed** when the tool has one
and its runtime's `Session.Outside` is false: the model has no say. `sandboxerFor(rt)` is that test, which `Approval`, `startDir`, `runCall` and `runTask` all ask, and a sandboxed
call's `spec.sandboxedRun` (a `sandboxedRun`: the sandboxer, the run's directory and the `sandbox.Run`) makes
`shellCmd` build the command through `Command` (foreground and background alike). `Approval`'s `Sandboxed` is true only for a sandboxed call on a sandbox that
`Confines`, and so is its `Skip`: such a call runs unasked, and every other call asks — but for
the network. **`Approval.Network` is decided for a sandboxed call alone** (`networkFor`, first row
wins): where the sandbox's `NetworkStatus` says none, a call that asks is a `*tools.Refusal`
naming why and any other skips with none, so a switch left on where network is gone gives none; a
session with a nil `Network` refuses a call that asks (*This session never has network.*) and skips
any other; the session's `NetworkChat` or `NetworkTurn` skips with it; and a call that asks with
neither asks the user, its network `NetworkApproved`. A call outside the sandbox records none. **The run takes the
approval** (`RunApproved`, the loop's call; `Run` runs as if no gate gave network), and
`sandboxedRunFor` sets `Policy.Network.Internet` from it, reads `CountedProcesses(internet)`, and
where `NeedsResolver` says so writes `resolv.conf`, `nameserver <sandbox.ResolverAddress>`, in the
run's own directory as its `Resolver`. A background command keeps the network it started with.
→ [ADR: the sandbox is the gate for a sandboxed command](../docs/adr/2026-09-28-the-sandbox-is-the-gate-for-a-sandboxed-command.md).
**A sandboxed run carries its own directory, environment and kubeconfig** (`sandboxedRunFor`; a
`sandboxedRun` on the `spec`). Its directory (`rundir.go`) is
`<runtime>/runs/<pid>-*` (`Tool.runsDir`), 0700 through `MkdirTemp`, holding `kubeconfig` and, for
a run with a cluster, its proxy's `proxy.sock`: outside the workspace, so a sandbox that confines can keep a
command from rewriting them (bwrap and Seatbelt both leave it read-only), and
under the runtime directory because a socket's path must fit (`maxSocketPath`, 103 on macOS and
108 elsewhere, `rundir_darwin.go`/`rundir_other.go`); `checkSocketPath` refuses a runs directory
too long for the longest name (`maxRunDirName`, 18) before anything is made, the path taken as
given, never resolved. **Its `TMPDIR` is `<cache>/tmp/<pid>-*`** (`Tool.tmpDir`), the one directory
of its own the command may write: on disk, so a command fills it no further than it can fill its
workspace. On macOS it starts with a copy of the user's xcrun lookup cache (`sandbox.SeedTmpDir`),
since `/usr/bin`'s developer tools are xcrun shims and a miss with Xcode.app selected runs
`xcodebuild` in the sandbox → [security record](../docs/security/2026-09-30-a-run-starts-with-xcruns-cache.md). `newRunDir` makes `runs/` and `tmp/` 0700 first, and refuses a runs directory whose
parent is not this user's alone (`checkPrivate`: a directory, not a link, the user owns and no one
else can write); every command refuses to start while the shell directory's parent is not either
(`snapshotFor`), since it sources the snapshot there. Without `$XDG_RUNTIME_DIR` the runtime
directory is `/tmp/kstack-<uid>`, whose name another user can take once it is removed. A task's is made inside its start,
with the process, so a start the chat refuses makes none. Both go at the reap for a call and at
`Wait` for a task (`runDirTask`), each through `rootdir.RemoveAll` on a root at its parent; `New`
sweeps the entries of `runs/` and `tmp/` whose sidecar is gone, on a goroutine of its own, since
`New` runs before `READY` and a crashed run's `TMPDIR` can hold a whole build. The same goroutine
sweeps the kubectl cache (`sweepKubectlCache`): it lists it, then the cluster service's
`Clusters().List`, and removes every entry that names no cluster (`rootdir.Sweep`), so a deleted
cluster's cache lasts until the next start. A cache is made only for a cluster already read, so the
order never takes a new one's. **A sidecar is gone
when it no longer holds its lock** (`rundir_unix.go`): `newRunDir` takes `<pid>.lock` in each
directory with `flock` before making anything there (`holdRunLock`, once per process, kept until
exit), and the sweep takes each `<pid>`'s lock it can, removes that pid's directories while holding
it, then the lock file. A lock dies with its process, where a pid passes to later ones, across a
reboot too, so a reused pid never keeps a gone sidecar's `TMPDIR`. A sidecar that waited on a
sweep's hold takes the lock again when the sweep removed the file. **Its `PATH` is frozen at its start** (`path.go`): `New`'s `pathList` is the
stored settings' `RunPath`, the list and whether a sync has written it, read
once before the run is built, and
`runPath` checks each adopted entry with a `securityconfig.RunCheck` over the run's `System` and its `Always` paths, each list
resolved once per run — the
folder it resolves to now when the sync would adopt that unasked, the stored `Target` for one the
user adopted, nothing for one inside an `Always` path, broad, world-writable, or no longer open or
shared under the shell's adoption — logging each entry it leaves out or moves. The folders that
pass are the run's `PATH`, joined in order without repeats, or `emptyPath` (`/nonexistent`) with
none, since an empty `PATH` searches the writable working directory. **Only a list never
resolved** (`Settings.PathResolved` unset: no sync has run, as after a first launch whose shell
failed) searches `loginshell.DefaultPath` instead, each folder an entry the shell adopted under the
same checks; a resolved list that is empty, or whose every entry fails them, searches nothing. Each folder
`System` does not open is a Files Read rule of the run, which takes the place of a Files Deny on
that very folder, so an included closed folder opens (`FilePolicy.WithSearch`). Nothing re-reads the store while the run
lives. **Its environment is one table** (`sandboxedRunEnv`, `env.go`): that `PATH`;
`HOME` the workspace and `PWD` the start directory; `TMPDIR` its own; `ZDOTDIR` the run's
directory, which holds no startup file, so `zsh -c` never sources a `.zshenv` a command left in
the workspace; `KUBECONFIG` and `KUBECACHEDIR` for a run with a cluster; `LANG` the process's, else
`en_US.UTF-8` on macOS and on Linux `C.UTF-8` where the system has it, else `C` (`defaultLang`);
`TZ` the process's; `TERM=dumb`; the tool home's variables; `System`'s `Env`; asdf's global
versions as `ASDF_<TOOL>_VERSION` when `System` found asdf (`toolVersions`, read from the user's
`~/.tool-versions`, a plain file of at most 64 KiB, each line whose tool and version are plain);
and `kstackEnv`. Nothing else of the process's passes, `LC_*` included
(`TestTheSandboxedEnvironmentIsFixed`). **The tool home** (`toolhome.go`) is
`tools.ToolHomePath(dir)`, `<chat>/toolhome/`, beside the workspace: `xdg/`, `helm/`, `npm`,
`pip`, `go/` and `cargo` folders, each named by its variable (`toolHomeVars`: the `XDG_*_HOME`,
`HELM_*_HOME`, `NPM_CONFIG_CACHE`, `PIP_CACHE_DIR`, `GOCACHE`, `GOMODCACHE`, `CARGO_HOME`). `makeToolHome` makes
it before each sandboxed run through `tools.OpenToolHome`, one level at a time through
`rootdir.Open`, so a link a command swaps in is refused; it goes with the chat's directory, and a
subagent shares it. **`System`, `Never`, `toolVersions` and the policy are built on one
goroutine** abandoned if the call's context ends first, since each can hang on a network mount
(`TestTheEnvironmentIsBuiltOnThePolicyGoroutine`). **A run with a cluster** (`rt.ClusterID`) reads it
through `Tool.clusterSvc`, the cluster service, as KubeQuery does: `target` (`target.go`) reads
the record with `Clusters().Get`, and a record that is gone, marked, or fails to read is `could
not start:` before anything is made, since a sandboxed kubectl aimed at nothing would read as the
cluster being down. It names the context `clustercard.ContextName` of the record and makes **the
kubectl cache**, `<cache>/kubectl/<id>/<server>` (`kubectlcache.go`, `Tool.kubectlDir`), level by
level through `rootdir.Open`, an id that is not one plain name refused. `<server>` is
`serverKey(uid)`: the first 16 bytes of the SHA-256 of the record's `Status.Server.UID` in hex, or
`unknown` for none — a context repointed at another cluster keeps its record, and the UID is the
cluster's text, so it names the directory only through the hash. The directory is opened on each
call, since the OS or the user may clear the cache directory while Kstack runs. **Its kubeconfig** (`writeKubeconfig`, `kubeconfig.go`) is 0600, encoded
by client-go's `clientcmd.Write`, since the name is the user's kubeconfig text: one context, named as the
card names it (`kstack` for a record that names no kube-context, which the card leaves empty) and
current, whose cluster is `http://cluster.kstack.invalid` — fixed, so kubectl's
cache stays warm while the port moves, and never resolved, since client-go sends each request to
`proxy-url` in absolute form — dialled through `http://kstack:<token>@127.0.0.1:<Port()>`, the
grant's token as the proxy's password, and a user holding nothing, since clientcmd applies a
user's credentials only over TLS. `safe.Redact` blanks the token wherever a command prints the
kubeconfig. **The `Run`'s policy is the Workspace policy** (`workspacePolicy`): its Files are the
sandbox's `System` `Files`, less every rule on or inside Kstack's directories (`Outside`), then
the session's folders (`grantRules`, `grants.go`) and `extraWritable` as a Files Write; its Always part is `Never` as its Deny, `Paths.DeniedDirs`
(`app` passes Kstack's directories, the log directory among them) as its Kstack paths, the run's directory
as its own Read, and the workspace, the tool home, its `TMPDIR` and the kubectl cache as its own
Write; and a run with a cluster has one
relay, from `Port()` to its proxy socket. **A grant is a Files rule**: `grantRules` reads the
session's folders once, as the run is built, and checks each again with
`securityconfig.CheckStoredFolder`
against what this run enforces — its `System` files, `Never` with Kstack's directories, the home,
`sandbox.NoWrite` and the stored `PATH` (`securityconfig.PathEntryFolders`) — so a folder that moved or
became a link since `foldersFor` read it is left out, and logged (`TestAGrantThatFailsTheCheckIsLeftOut`).
A folder granted twice keeps the wider grant, and one under a read-write folder is dropped, since
`Check` refuses any rule beneath a Write rule; a read-write folder under a read one stays
(`TestNestedGrantsResolveToTheWider`). A read grant is a Read rule, a read-write one a Write rule,
and the Always part is untouched (`TestAGrantIsAFilesRule`, and through the real sandbox
`TestADeniedAlwaysPathStaysHiddenUnderAGrantedHome`, `TestAReadWriteGrantIsWritten`). A grant
revoked mid-run holds for a command already running, since a sandbox is per command. **Its `Limits`** are `limitCPU` (`MaxTimeout` plus
`killGrace`) in seconds times `runtime.NumCPU()` on a foreground run and none on a background one,
which has no clock; `limitMemory` (`limits_linux.go`: 16 GiB; `limits_other.go`: 0, since macOS
sets none); `limitOpenFiles` (4096); and the sandbox's `CountedProcesses` plus
`processMargin(runtime.NumCPU())` (`limits_linux.go`: `max(1024, 128 × cpus)`, since Linux counts
threads; `limits_other.go`: 512) on a foreground run and none on a background one, since a
machine-wide count drifts over hours as other programs start. `sandboxedRunFor` reads a foreground
run's base first, through the `sandboxer` seam, and its error fails the call before anything is
made, so a run never starts under a limit other than its policy's. Bash's tests lay their folders out under Kstack's three
as `app/paths.go` does (`kstackDirs`), so a test's policy passes `Check` over a real sandbox.
**A run with a cluster claims its connection and serves a grant over it** (`upstream.go`,
`proxy.go`). `claim` acquires the lease with `AcquireConnection`, which does not dial, and the
claim's `Endpoint` is `Lease.ConnFor(ctx, uid)` by the server UID `target` read (`target.serverUID`,
the one the kubectl cache is keyed on), mapping `clustersvc`'s `ErrIdentityMismatch` to
`kubeproxy`'s, and its `Done` closes with the connection's own `Done` or the claim's revocation.
**The claim watches the record** (`Clusters().Watch`): a frame showing it disabled, marked for
deletion or deleted, or the watch ending before the claim does, revokes it, and every request
from then on answers `ErrNotConnectable`, since `AcquireConnection` checks the record once and
the run's own lease keeps the connection alive. **A claim that cannot be made still runs the command**,
since most commands never touch the cluster: a cluster never identified (no UID) and one
`AcquireConnection` refuses as `ErrNotConnectable` get a claim whose every request answers
`kubeproxy.ErrNotIdentified` or `ErrNotConnectable`; `ErrNotFound` is `errClusterGone`, and any
other error `could not start:`. `sandboxedRunFor` asks the sandbox for a port only for a run
with a cluster, then `startProxy` listens on the run's `proxy.sock` and serves a
`kubeproxy.NewGrant(claim, rt.Session, target.scopeContext, asker, refusal, 20, 50, 32)` on `kubeproxy.NewServer`, and `Run.Socket`
names the socket. `writesFor` picks the grant's writes: a foreground call's asks through its
runtime's `ActionAsker` (`runtimeAsker`, which turns a `kubeproxy.Request` into a
`tools.ActionRequest` for `Ask` and `Record` alike, and its `tools.Answer` back), or refuses with *this sandbox reads the cluster and changes nothing*
when it has none; a background task's refuses every write with *a background command cannot change
the cluster*. The `sandboxedRun` owns the claim, the proxy and the directory, so every failure while
it is made is its `end`. **The run ends in order** (`sandboxedRun.end`), at the reap for a call
and at `Wait` for a task (`sandboxedTask`): the grant ends, cancelling what is in flight, the
server and its socket close, then the grant's `Wait` joins every handler — after the close, since
a handler reading a body returns only once its connection closes — the claim is released, and only
then does the run's directory go. So no write is put to the user once `Run` has returned. A run with no
cluster has no grant, no socket and no forwarder. `prompts/sandbox.md` says a sandboxed command waits
for the user only for a change to the cluster, which runs at once under the user's rules, waits for
them, or comes back `Forbidden` because their mode or a rule refuses it — the user's decision, not an
error to work around — that a change the user allowed for the chat or always runs at once the next time, and one allowed for the command for the rest of that command alone; that a dry run of a built-in resource runs at once and one of a custom resource waits, and the wait counts against its `timeout`; that `exec`, `attach`, `port-forward`, a service account
token, a change past 1 MiB and a background command's change come back
`Forbidden`; that a Secret changes with `kubectl apply --server-side`; that a Secret reads
`[redacted]` unless the user allows showing it, which a read of Secret data asks for, that
listing names asks for nothing, that a background command reads it redacted and that
`[redacted]` after a request is the user's answer; that every helm command asks, and a helm change
is refused when the command read Secret data redacted; and that `sudo` does not work, a command's processes are limited, one past its CPU
time is killed with exit 152, and a crash that cannot create a thread hit the count.
**A `Tool` is the `tools.Gated` a turn is offered**, matched to Claude Code's `Bash`: `Definition` is a function named `Bash` whose
schema (`prompts/schema.json`), one with a sandbox or without, takes `command`, `description`, `timeout` (milliseconds), `run_in_background`, `network` (a sandboxed command asking for the internet, which `CommandAction.Network` carries) and `workdir` —
never the reference's `dangerouslyDisableSandbox`, so leaving the sandbox is the user's switch — and whose description is `prompts/description.md`. The schema's `description` property is Kstack's
own: the user reads the description *above the command* on the approval request, so it names
what the command changes and where, and never calls a command safe — the model's claim is never
the app's. `Prompt` is `prompts/bash.md`, Kstack's own lines, where the tool has no sandbox. Where it has
one, `prompts/sandbox.md` takes the place of bash.md's first paragraph, which says every command
waits (what the sandbox reaches, that a tool which cannot find its files under the home needs a folder the user grants from under the command that failed or in Settings, so the model says which and why and runs it again once they have, that the user can switch the chat outside it, which the context says, and that only a command outside it waits for
the user), then on Linux `prompts/sandbox_linux.md` (a snap's program needs the chat run outside the sandbox),
then the rest of bash.md; then `- Platform:` (`runtime.GOOS`) and `- Shell:`, and on Windows a line on Git Bash's paths.
`Approval`, `Run`, `CallTimeout` and `ActionOf` read the input through one `parse` that walks the
tokens — `dangerouslyDisableSandbox` refused like any unknown key — each key
spelled exactly and at most once, nothing after the object, **each value's type checked off its
token** (a typed decode takes `null` as the zero value), a command never empty (it would still
start bash, which sources `BASH_ENV`) — since a struct decode matches a case variant and lets a
duplicate win, and the command that runs must be the one approved. A timeout defaults to
`DefaultTimeout` (120s), is capped at `MaxTimeout` (600s) before any conversion, and rounds up
to a whole millisecond; 0 or below is refused, and so is a number past a float64's range
(`1e999`), which is not finite. A `workdir` is refused empty, over 4,096 bytes, or holding a
control character, so it is one readable line on the approval request. `ActionOf`'s command
`Text` is the command alone and its `Description` the input's — the description never reaches
`Text`, and the timeout neither — and its `Cwd` the row's, never resolved again. `Approval`'s
`Cwd` is the resolved directory. `ActionOf` applies `checkWorkdir` as well, so every input `Run`
answers `bad-input`, it refuses; `Run` on such an input starts nothing.

**A command runs in the directory its approval request shows**: its `workdir`, else the chat's
workspace. `startDir` resolves one into the other through `resolveWorkdir(home, workspace,
workdir, sandboxed)` (`workdir_unix.go`, `workdir_windows.go`), and `Approval`, `runCall` and
`runTask` all call it on the same input. **A call is sandboxed there when it runs through the
tool's sandbox**, whether or not that confines it, since that is what the model was told. It
calls `checkWorkdir` first, the refusals no directory can fix, which `ActionOf` calls too.
`~user` is refused; `~` and `~/…` are under the workspace for a sandboxed call and under the home
outside; a relative path is joined to the workspace in both; an absolute path is itself; all
through `filepath.Clean`. **A sandboxed call must start under the workspace by name**
(`fileguard.Under`), whatever produced the directory, so `~/..` and `../x` are refused like
`/etc` (`errOutsideWorkspace`), and `Approval` answers a `*tools.Refusal` naming the workspace and
the flag. The check is guidance, not a boundary: a link in the workspace can start a command
elsewhere. On Windows `~\…` resolves as `~/…` does, Git Bash's `/c/x` becomes `C:\x`, and any
other Git Bash path, a rooted path with no drive (`\x`) and a drive-relative one (`C:x`) are
refused; Windows resolves the sandboxed column too, though native Windows has no sandbox.
**`resolveWorkdir` never touches the filesystem**: it runs before the user decides, and a UNC path
touched then would start an SMB login. `Run` makes the workspace (`makeWorkspace`), answering
`could not start:` when it cannot, and checks the directory exists before the snapshot
wait and answers `could not start: <dir> is not a directory` without starting the shell. **The
check is cancellable** (`checkDir`): a stat on a dead network mount can block in the kernel, so it
runs on a goroutine of its own and `Run` answers the context without it, leaving the goroutine
to return whenever the filesystem does. The start itself cannot be cancelled, so a mount that
dies between the two still holds the call. `Tool.statDir` is the test's seam. Nothing
carries between calls, so a `cd` ends with its command. On Unix the environment gains
`PWD=<dir>`, since `exec` sets it only when `Env` is nil, so `pwd` prints the directory the
request showed. Outside the sandbox the environment is `os.Environ()` plus `KSTACK=1`,
`KSTACK_SIDECAR_PID` and, when `--host-pid` was given, `KSTACK_HOST_PID` — nothing taken away
(`outsideEnv`); a sandboxed run's is built (above) —
stdin the null device, stdout and stderr one pipe, keeping `tools.FileLimit` (8 MiB). **What the
shell runs is the wrapper** (`wrapper.go`): `source` the snapshot (outside the sandbox, when there is one), under zsh
`setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL NO_NOMATCH SH_WORD_SPLIT` so bash-style text globs
and splits as bash has it (an unmatched `{.items[*]}` stays a word), then `eval
'<command>' < /dev/null`, the command through `quote` — single quotes, each inner quote closed,
escaped and reopened. The approval's text is still the command alone. On Unix it is `exec.Cmd`
running `<shell> -c <wrapper>` in a process group of its own (`Setpgid`), and a sandboxed run in a
session of its own (`Setsid`), so it has no controlling terminal to push input into
(`TestASandboxedRunLeadsItsOwnSession`); on Windows the wrapper
is written byte-for-byte to a read-only file under `<runtime>/shell` and bash runs the file, so the approved bytes never ride a Windows command line for the MSYS runtime
to reparse, and the child is built with `CreateProcess` directly — started suspended, assigned to
a kill-on-close job object, then resumed. `New` sweeps `<runtime>/shell` for a script a crash
left behind.

**The snapshot is the user's profile, taken at most once per start, for commands outside the
sandbox alone** (`snapshot.go`). A sandboxed run neither sources nor waits for it: `snapshotFor`
answers none for it. It is the `shell snapshot`
`lifecycle.Part`, which `app.New` adds last when `Config.RunLoginShell` is set. `StartSnapshot`
makes the context the login shell runs under, since Start's bounds startup only, and starts it
only on a machine with no sandbox; with one, `snapshotFor` starts it on the first run outside the
sandbox (`startSnapshot`, under a mutex, so concurrent first runs share one). Its stop is final:
under that mutex it cancels the context the start reads, then waits for the reap of a shell
already started, so no shell outlives the sidecar and none starts after it. A
call outside the sandbox waits for the snapshot or for its own context (`awaitSnapshot`), and
`CallTimeout` adds `snapshotTimeout` (10s) to its sum; a tool whose snapshot never started does
not wait. **The dump** (`dumpCommand`) turns alias expansion off with
a line no alias can match, then `eval`s the rest with every command through `builtin`, so the
profile's names cannot steer it. Between two NUL-framed markers it prints `unalias -a`, the
options, the functions, the regular aliases and `export PATH`. **PATH is resolved by
`loginshell`'s rules** before it is printed, in the dump itself: a command runs in its own
directory, not where the profile may have `cd`'d, so a relative entry is prefixed with the dump's `$PWD`, a
leading `~` passes through, an empty entry is dropped, and a PATH left empty is not exported —
on macOS the replay would otherwise overwrite the PATH `main` already installed. Left out: the
options that say how the shell was started (`monitor` above all — replayed, it moves a command's jobs out of its group —
and bash's read-only `login_shell` and `restricted_shell`) and global and suffix aliases, which
expand anywhere in a line. Options come before functions because a body is parsed under the
options in force (bash reads `+(...)` only with `extglob` on), and a line that fails to parse
stops the `source` there, shims included. zsh's options are read with `kshoptionprint` off,
since under it `setopt` lists every option beside on or off. `Tool.launch` runs the shell —
`launchDump`, or a test's stand-in: on Unix through `loginshell.Launch` over `loginshell.In` — the
tool's sandbox, no denied-always list, `Paths.DeniedDirs` closed and its `TMPDIR` a run's under
`Paths.TmpDir`, or unconfined on a machine with none, which it logs — capped at `snapshotLimit`
(4 MiB) with the end marker looked for in the new bytes alone; on Windows through the same
`start` as a command, with the dump in a file (removed after) and only its path on the command
line. **The file** is `<runtime>/shell/snapshot.sh`, 0400 (read-only on Windows) in a 0700
directory, its path in forward slashes, rewritten every start and stale until the next: an rc
edit is not seen until a restart. **The kill shims** close it on Unix — `kill` and `pkill`
refuse `KSTACK_SIDECAR_PID` and `KSTACK_HOST_PID`, a courtesy that `/bin/kill` passes; `pkill`
asks `pgrep` first, less the flags `pgrep` does not take, and refuses when `pgrep` cannot answer.
A dump that fails leaves the shims alone in the file (no file on Windows, which has none) and
logs a reason from a fixed set, never the shell's output. zsh's autoload stubs come through as
stubs and `fpath` does not, so calling one fails; the reference has the same limit.
→ [security record](../docs/security/2026-09-23-bash-runs-the-login-shell.md).

**A command is stopped two ways.** The call's own timeout sends SIGTERM to the group, then
SIGKILL after `killGrace` (5s), or at once if the loop's context ends during the grace. The
loop's context ending (the user's Cancel, a deleted chat, shutdown) sends SIGKILL at once, with
no grace: the host allows the sidecar 6s, the drain spends up to 3s before `stop` cancels the
turns, and a Unix group outlives the sidecar. Windows terminates the job with 143 on a timeout
and 137 on a cancel. **The stop is the runner's act** (`result.Stop`), recorded as it signals
and never read off the exit code or the context alone: a trap that exits 0 was still stopped,
and an exit of 143 by itself was not. The record sits under a lock (`guard`) the reap closes,
so a stop that finds bash reaped sends and records nothing, and a late escalation never signals
a reused group id. On Unix `exec` can run the stop after the OS has collected bash and before
`Wait` returns, so `stopBash` asks the `os.Process` first and stands aside for a bash already
collected. **A stopped command is always an error result**, since the loop keeps any
result that is not an error and must never keep a cancel as a success. Group and job are killed
again after bash exits, so a `sleep &` left behind is gone before the result is read; a
descendant that left the Unix group is outside the guarantee, and nothing can leave the job.
The exit code is `exitCode`: 128 plus the signal that killed bash, else its status. `run` takes
a `spec` (the bounds as fields, `pipeGrace` among them) and `hooks`, zero in production, so a
test fires the deadline by hand and orders each race rather than hoping for it.

What the model reads is `resultText`. **An error result's first line is the sidecar's** and the
command's output starts on the next, so nothing it prints can pass for a header: `Exit code N`,
`Command timed out after <d> (exit code N)` (`<d>` in whole seconds, else milliseconds), or
`Command ended; its exit code could not be read`. A success is the output alone. The output is
the capture redacted whole (`safe.Redact`: the error shapes plus what a cluster command prints,
a PEM private key, a credential field by its format's name and the lines its value continues
onto, a long flag's value, a `.netrc` password, an AWS access key id and the tokens with an
issuer prefix; **hygiene, never the boundary**, since a command can re-encode its output past
any rule, the approval request is the bound, and an interrupted printed form is the model row's
stated residual). With the header it comes back whole up to `tools.InlineLimit`. **Past it, the
output is saved** through the shared save (*Tools*, above): `resultText` hands `tools.Fit` the
header, the redacted output, the bytes the capture discarded and the noun `output`, with
`tools.SaveTo(dir)` as its saver. The file holds the redacted output, so a key across the
preview's edge is redacted in both. **A failed save, or a `Tool.Run` with no place** (never
offered), falls back to the cut, its note ending `; the output could not be saved`. A
bash that could not start answers `could not start: <reason>` alone, the reason through
`safe.String`, so the model can tell a command that never began from one that ran. **A confined
run that ran, was not stopped, and failed** ends with `sandboxLine`, `(Ran in the sandbox: no
network, …)` or `with network` for a run with the internet, its cluster changes going *as the
user's permissions decide*, as `Fit`'s trailer, so the model knows the sandbox may be why; a
timeout, a cancel and a `could not start` carry none. `resultText` reads a `confinement`
(`unconfined`, `confined`, `confinedWithInternet`): **a stopped run with the internet has no exit
code in its header** (*Command timed out after 30s*, *Command cancelled*), since it ended as pasta
did, which a group stop ends with 0. Redaction
over 8 MiB takes seconds (`docs/TODO.md`), inside `callMargin` and the snapshot's allowance.

**`internal/tools/read` is `Read`**, the reference's: any regular text file the user approves,
by its plain absolute path, and the chat's own files without asking. `fileguard.Abs` takes a path
of one line — no control character, at most `MaxPath` (4,096) bytes — since the request draws
it whole and unfolded. `read.New(hidden, fenced...)` is
`Gated` and `Bounded` (`callTimeout`, 30s, above the turn's 2s, which reading and
redacting 8 MiB can pass). `parse` is bash's strict walk over `file_path`, `offset` and `limit`,
the numbers whole and at least 1, and it and `ActionOf` are unchanged for stored rows.
**`Approval` classifies the path by name alone**, touching nothing on disk: `Skip` for a path
`fileguard.Abs` refuses, one in the chat's directory, and one elsewhere in Kstack's directories
(`Fence.Named`); `Skip` with its `Folder` for a path in a folder the session was granted that
the sandbox does not keep shut there (`Fence.Granted` over `Session.GrantedFolders`); ask, with no `Cwd`,
for anything else. So the user is never asked about a read `Run` would refuse by its name.
**`RunApproved` with a `Folder` reads through `Fence.Walk`** of that folder alone (`fetchGranted`,
a field a test swaps), on a goroutine as `fetch` is, and stamps as a read outside does; with none it
is `Run`. A hidden path under a granted folder asks, and once approved is read as any approved
path is. **`Run` takes one of three branches** after `Abs`: the chat's
directory, through `Dir.Root(false)` and `OpenFile(rel, fileguard.OpenFlags)` with a `Stat` of the open file,
every refusal `notHere`; Kstack's directories, by name or on disk (`Fence.Holds`), `notHere` — one
sentence naming nothing about the path; anywhere else, `fileguard.Open`, `fileguard.ReadAll` and
`fileguard.Binary`, each refusal in words that say what is wrong and never a Go error's text (an
ancestor `Holds` cannot resolve reads as a file Kstack cannot read). **The file work outside the
chat's directory runs on a goroutine** (`fetch`, a field a test swaps): a stat or open on a dead mount
blocks in a syscall no context reaches, so `Run` answers the context at once and drops a late
result. The whole file goes through `safe.Redact` before the range is taken, whoever wrote it,
the context checked on either side. `numbered` strips a byte order mark and each `\r` before a
`\n`, then is `cat -n`'s `%6d\t` from `offset` for `limit` lines (2000 by default; a final `\n`
starts no line; a line past 2000 runes cut with `…`), cut at a line to `InlineLimit` with
`… [cut; continue with offset N]`, a line kept only with room for that note; it reports whether
it showed every byte as it is. **A read outside Kstack's directories sets the path's stamp**: the
raw bytes' hash, `Whole` when neither `numbered` nor the redaction changed anything, and `Whole`
kept when the bytes match a stamp already `Whole`. **So does a read in the chat's workspace**,
so Write and Edit can change a file a command made; the rest of the chat's directory is never
stamped.
A call whose context ended stamps nothing.
→ [security record: read any approved file](../docs/security/2026-09-23-read-any-approved-file.md).

**`internal/tools/write` is `Write`**, the reference's: a new file, with any missing directories,
or an existing one replaced whole: in the chat's workspace unasked, anywhere else once the user
approves the path and the content.
`write.New(umask, hidden, fenced...)` is `Gated` and `Bounded` (`callTimeout`, 30s), shares
Read's fence, and takes `Config.UserUmask`. `parse` is Read's strict walk over `file_path` and
`content`, both strings, the content at most `FileLimit` bytes and possibly empty. **`Approval`
reads the path by name alone**: `Skip` for a path `fileguard.Abs` refuses, one under Kstack's
directories by name but the chat's workspace (`Fence.NamedOutsideWorkspace`), and content holding a NUL, which `Run` refuses before touching anything;
`Skip` too for a path under the workspace by name (`fileguard.Under`), the test `Fence.File` opens
the workspace's root by, so an unasked write cannot leave it; `Skip` with its `Folder` for a path in
a folder granted read-write that `Fence.Granted` passes, which `RunApproved` reaches through
`Fence.Walk` alone; ask, with no `Cwd`, for anything else. So a Write over a file the chat has not read is asked, then
refused, and the description and prompt tell the model to Read first. **`Run`** does the string
checks, then the file work on a goroutine (`write`, a field a test swaps), answering the context
without it: `Fence.File(results, path, true)` — the workspace's root, made if missing, or the path
past `Holds` — then the `File`'s `Lstat` (a link refused with its target, a directory, not a
regular file), then for a missing path `Creatable` and `Create` under the umask, and for an
existing one `Replaceable`, `Open`, `ReadAll`, `Binary`, and the chat's stamp — none, a different
hash, or not `Whole`, each refused in its own words — then `Replace`. A write answered as done
stamps the path `Whole` with the content's hash; one answered as cancelled stamps nothing. The
refusals the tool words itself are one type, `refused`, whose text is the model's message; the
rest map from `fileguard`'s errors, never a Go error's text.
→ [security record: the write tool](../docs/security/2026-09-24-write-tool.md).

**`internal/tools/edit` is `Edit`**, the reference's: one exact occurrence of `old_string` replaced
with `new_string`, or every one with `replace_all`: in the chat's workspace unasked, anywhere
else once the user approves the path and both strings. `edit.New(hidden, fenced...)` is `Gated` and `Bounded` (`callTimeout`, 30s) and builds its own
fence. `parse` is the strict walk over `file_path`, `old_string`, `new_string` and an optional
boolean `replace_all`, each string at most `FileLimit` bytes. **`check` is Write's plus three**:
`fileguard.Abs`, `Fence.NamedOutsideWorkspace`, then an empty `old_string`, one equal to `new_string`, and a
`new_string` holding a NUL or `safe.Redacted` — the mark Read shows in place of a secret, which
Edit would write as text. `Approval` answers `Skip` for any call `check` refuses, for one in the
workspace, and with its `Folder` for one in a folder granted read-write, as Write's does, and asks, with no
`Cwd`, for the rest. **`Run`** does `check`, then the file work on a goroutine (`edit`, a field a
test swaps): `Fence.File(results, path, false)` — an edit makes no workspace, and a missing one is
a missing file — then `Lstat`, `Replaceable`, `Open`, `ReadAll`, `Binary`, and the chat's
stamp, which must match the bytes but need not be `Whole`. In a file whose every line break is
`\r\n` (`crlf`), each `\n` in both strings not already after a `\r` becomes `\r\n`, and two strings
that conversion makes equal are refused; nothing else is folded. **`matches` counts with
`strings.Count`** and, without `replace_all`, refuses more than one and one that another overlaps —
one more `strings.Index` from a byte past the first match, never a walk of every overlap, which a
long repetitive string makes quadratic. The size is checked from the count before the result is
built. `Replace` writes it, and the new stamp keeps the old one's `Whole` only when the strings went
in as sent. A miss echoes `old_string`, cut at 2,000 runes, with one `hint` when a cause can be
named: text Read redacted (`old` holds the mark, or is found in `safe.Redact` of the file), line
endings the file mixes, or a line Read cut. The refusals are `refused` values, as Write's.
→ [security record: the edit tool](../docs/security/2026-09-24-edit-tool.md).

**`internal/tools/webfetch` is `WebFetch`**: one `https` GET of a URL the user approves, from
the user's machine, the page returned as markdown. `webfetch.New(transport, bound)` is `Gated` and
`Bounded` (`callTimeout`, 45s: the fetch's `FetchTimeout`, 30s, then the conversion), reading
`rt.Dir`. `parse` is a strict walk over `url`. **`target` reads the URL by name**: `http` or
`https` with a host and no userinfo; a host that is not an IP literal through
`idna.Lookup.ToASCII` (which refuses every IPv6 address's colons); `http` upgraded, an explicit
`:80` dropped; at most 8,192 bytes; an IP literal through `Public`, any other host with a dot and
a last label that is not a number (WHATWG's rule, so `127.1` and `0x7f.1` never reach a
resolver). Each refusal is a `refusal`, whose text is the model's. `Approval` skips a URL
`target` refuses and asks, with no `Cwd`, about any other. `ActionOf` is `rebuild` alone —
`target` without its refusals by name, and a host `idna` rejects kept as spelled — so a
refusal added later or an `x/net` update changes no stored call's action: `Fetch.URL` as
fetched and `Fetch.Host`, `u.Host` less `:443`, which the request draws.
**`NewTransport(Dialing)` is the network**: a clone of the default transport, which follows no redirect and keeps no cookie, with no handshake
timeout (the fetch's bound is its one clock), `Dialing.RootCAs` as its trust, and a
`net.Dialer` whose `ControlContext` refuses an address `Dialing.Public` rejects
(`errPrivate`) — the address actually dialled, after DNS, on every connection. The one
destination let through is the proxy: `Dialing.Proxy` is an `httpproxy.Config` (`app` passes
`FromEnvironment()`, which on macOS holds what `setShellEnv` set), and a dial to its
`HTTPSProxy` address — every fetch is https, so no other proxy carries one — spelled as
`net/http`'s `canonicalAddr` spells it (`proxyAddr`), skips the control. `Public` refuses loopback, private, link-local, multicast and unspecified addresses,
`0.0.0.0/8`, `100.64.0.0/10`, `192.0.0.0/24`, `198.18.0.0/15` and `240.0.0.0/4`, and judges NAT64,
6to4 and IPv4-compatible addresses by the IPv4 they carry. **`Run` follows redirects itself**
(`follow`): a 301, 302, 303, 307 or 308 is a redirect,
its `Location` resolved; missing or unparseable is nowhere to go, not `https` is refused, a
URL `target` refuses answers that refusal, and a host other than the approved one, less one
leading `www.` either side, or another port is returned to the model (`REDIRECT DETECTED`);
else the hop is followed, five at most. Any other status past 2xx answers its code and
`http.StatusText`, never the server's reason phrase, with the body unread. The body is read to
`FileLimit`; `readPage` takes it by its media type (sniffed when the `Content-Type` is missing
or does not parse): HTML decoded through `x/net/html/charset` (the header's charset, else the
page's own declaration), parsed by `x/net/html`, its non-text elements removed, converted by
`html-to-markdown/v2` with links made absolute, its `<title>` one line of at most 200
characters; the other text types as they came, decoded from the header's charset alone and UTF-8
when it names none, since `charset`'s HTML guess reads an ASCII first kilobyte as Windows-1252;
anything else refused. The type, accepted or refused, is capped at 100 bytes, since the result's
header carries it and `Fit` never cuts a header. The conversion runs on a goroutine `Run`
leaves when its context ends (`convertWithin`; `convert` is the field a test swaps). The result
is `Fetched <url> (<type>, <size>)`, the `Title:` line, then the text, through `tools.Fit`
with `page`; it is not redacted, and a saved page read back through `Read` is. A failure to
reach the page names its kind — `timeout`, `tls`, `dns`, `connection` — never the error's text.
→ [security record: the web fetch tool](../docs/security/2026-09-24-web-fetch-tool.md).

**Background commands.** A bash call with `run_in_background` is approved like any other, its
action's `Background` set, so the request asks *Run this command in the background?*.
`Run` checks the directory as the foreground does and calls `Tasks.Start` with
bash's `startTask` (`task_unix.go`, `task_windows.go`): the wrapper in its own group or job, its
output copied to the file up to `TaskOutputLimit` and the rest drained, answering at once in the
reference's words (`Command running in background with ID: …`). **The call's context bounds the
start alone**: `start` checks it before making the run's directory, so a cancel during the
snapshot wait or the row write starts nothing, and `startTask` passes it to the sandbox's
`Command`, so a profile hanging on a path gives way to a cancel, then clears the `exec.Cmd`'s
`Cancel`, so the process outlives the call. `timeout` is ignored. The `Task` owns what the foreground's `run` releases on return — on Windows the script
and the job, kept until the reap. `Stop(false)` is SIGTERM, then SIGKILL after `spec.killGrace`;
`Stop(true)`, and any stop on Windows, kills at once. Bash's exit kills its group, as in the
foreground.

**`chatsvc` holds the tasks** (`tasks.go`): `tasks map[ChatID]map[TaskID]*task` under `turnsMu`,
every task that holds a slot — a command's, or an agent's, which share the slots. Each records the
run whose call started it. `startTask` runs in order: a `wg` slot through `enter`; registration under `turnsMu`, refused once `stopped` is
closed, for a chat being deleted, and at the limits (`maxTasksPerChat` 4, `maxTasks` 16);
`tasks/<id>.output` through the chat's root, 0600 `O_EXCL`; the `background_tasks` row
`running` under the call `turn.openTool` names; then `start`. A step that fails undoes the
ones before it, and a stop that arrived before the process existed is applied the moment it
does. **The watcher** waits on the task — for a command, appends `[exited with code N]` or
`[stopped]` on a line of its own; for an agent, names its end off the run through its
`taskRecord`'s `end`, whose `taskEnd.Run` and `taskEnd.Rows` write the run and its rows beside the task's — writes the row
(`finishWrite`, on a context without the service's cancel), and only then releases the slot, so
the count matches the rows still running; every end pings the chat list, since an agent's run
may have been waiting on the user, and kicks the chat, `startsTurn` deciding. A row `stopped`
by `model` is written notified; every other finished row is a **waiting notice**. **Five
stops**: `chatTasks.Stop` (the model's, with the grace; a subagent's reaches only the tasks its own
run's calls started, since only the model that called it reads the result that stands for the
notice), `StopBackgroundTask(toolCallID)` (the user's *Stop*, with the grace), a subagent's request
unanswered for `unansweredLimit` (`llm.UnansweredLimit`, 30 minutes; `stopped_by` `unanswered`), `Delete` (after joining the turn, every task of the chat at once,
joined before `deleteWrite`) and `stop` (after closing `stopped`, every task at once, before
`wg.Wait`); the first to land names `stopped_by`. Once `Wait` answers, the task is `reaped`: it holds its slot until
the row is written, but the model's and the user's stops answer false, since the exit is what
the row will say. `Start` marks a row still `running` `lost`
and notifies its chat. **The transcript reads a task off its call**: the tool-call reads join
`background_tasks` and the run an `Agent` call started, `ToolCall.Background` carries status,
exit code and a completed agent's report cut to `InlineLimit`, and `overlay` gives the live answer
what its own entries know nothing of, off the stored one (`withStoredCalls`): each task's state,
and every call that ran under one of its `Agent` calls, the store being the one source of a
subagent's calls whichever message is live.

**Notices** (`notices.go`) are `llm.BlockTaskNotification` blocks carrying an `llm.TaskNotice` of
kind `command` or `agent`, the description and command read off the starting call's arguments by
its action and each cut by `noticeLine` (the transcript's `descriptionLine` rule), sent by every
dialect as the reference's escaped `<task-notification>`. **An agent's notice is its report**: on
`completed`, the task's file and the report through `tools.Fit` with a saver that answers that
file, which already holds it, so a long report is previewed and building a notice writes nothing;
on `failed`, the run's error on one line, or *it had nothing to say*; no file otherwise. The report
rides in `<result>`, escaped like every field. **A subagent's command tells
the parent**, the model that reads the notice: `stmtSelectWaitingNotices` joins the `Agent` row
whose subagent ran the call, so the notice's `tool_use_id` is that call's, the one the parent saw,
and `AgentDescription` is its description, which the summary names (`started by agent "…"`). They reach the model
two ways. **Riding a send**: `Send` puts every waiting notice of an existing chat after the
question's context block and marks them told, in its transaction. **A turn of their own**: after a
watcher writes an `exited`, `completed` or `failed` row and after a turn settles succeeded, `kick`
starts one if the chat's slot is free and a notice waits that `startsTurn` passes — an exit, or the
end of an agent whose `Agent` call is in a turn a user's send started (its trigger carries a
request key). `Agent` is ungated, so an agent a sidecar-started turn launched rides the next
question instead: no chain runs past the turn an agent's end starts. That is `startNoticeTurn`, a send
without a sender: one transaction that reads the notices, resolves the last answer's provider,
model and effort, makes every check a send makes, reserves the turn, files a user message of
the notices alone (no request key, no card), marks them told and touches the chat. It carries a
context block only when the chat's switches moved or its folders changed since the newest one:
that block with its `## Sandbox` section replaced (`withSandboxReplaced`, `workspace.go`, which
finds the section by its heading), so the model knows where the turn's commands run and what they
reach and read; a notice turn has no toggle. A cancelled
or failed turn kicks nothing, and nothing kicks at startup.

**`internal/tools/taskstop` is `TaskStop`**: ungated, reading `rt.Tasks`, `task_id` read by a strict
walk, `Stopped <id>.` on a task of its chat — a command or an agent — one `notRunning` text for
anything else.
→ [security record: background commands](../docs/security/2026-09-23-background-commands.md).

**A command is asked before it runs** (`approval.go`). The turn is the loop's
`agent.Approver`. `Approve` mints the `ApprovalID` and registers its waiter (`service.pending`,
under `turnsMu`) before anything is written, so no id is decidable ahead of its request; then one
transaction writes the call's row `awaiting_approval` with `is_mutating` 1, the approval's
`Cwd` as `cwd` and `Sandboxed` as `sandboxed`, and no `started_at`, its `approvals` row `pending`, and **this run's** flip to
`waiting_approval` (`flipRun`) — a subagent's wait leaves its parent's alone; the request is
published and the chat list pinged (`Chat.awaitingApproval`). A turn's own wait has no bound —
Cancel, Delete and shutdown end it; a subagent's is bounded by `unansweredLimit` too, past which
its agent is stopped as the user's Stop would and the wait ends on that cancel.
`ChatMessage.AwaitingApproval` is whether a run of the answer waits — its own or a subagent's under
one of its `Agent` calls — read off the runs by the stored read, and ORed with the live status by
`overlay`. `service.Approve(id, approve)` takes the
waiter out and sends on its channel of one, buffered, so a decision that lands before the
turn's `select` is kept and a second finds no one (false). **The commit is the line a cancel
stops at**: a cancel that wins the wait takes the waiter back and leaves the approval `pending`;
a decision that wins is written — the approval's status and `decided_at`, the run back to
`running` — on a context without the cancel, and then stands. A decision write that fails
refuses the call `not-run`, and the in-memory approval keeps `pending`, since **the turn holds
only what landed** and `Settled` writes every row and approval again from it. **One row per
call**: `t.openTool` is the row `Approve` or `ToolCallStarted` opened — a row `ToolCallStarted`
mints takes `cwd` and `sandboxed` off the approval it is handed; `ToolCallStarted` writes
a copy `running` with `started_at` and the approval's `network` — `tool_calls.network`, `chat`,
`turn` or `approved`, NULL for none, written on this row alone, so a call that waited and was denied
keeps NULL, and `stmtUpsertToolCall` updates it — and takes it only once it lands, so a failed start write
leaves `started_at` NULL; `ToolCallFinished` closes whichever row is open, or mints one for a
call refused before any row existed, `denied` for the loop's `denied` refusal on a gated row.
The sweep closes a stranded
`awaiting_approval` row like a `running` one and leaves its approval `pending`: the record of a
question nobody answered.

**A sandboxed command's action is asked under the call that sent it** (`approval.go`). The
turn and a subagent set `Runtime.ActionAsker` to `actionAsker`, their journal bound to their run's
context, so a wait ends with the request or the run, whichever ends first. `askAction` runs on a
proxy's goroutine while the loop waits in the call's `Run`, which returns only once the grant's
`Wait` has joined it, so the loop and the proxies never touch the journal at once. **Two locks
keep the proxies' requests apart**: `journalMu` serializes every journal write an ask or a record
makes and is never held across a wait; `askMu` holds one ask from its pending row to its end, so a
run puts one request to the user at a time, and a record never takes it, so a write a rule allows
lands while a request waits. `asking`, under `journalMu`, makes a record publish
`StatusWaitingApproval` while an ask waits. An ask is `Approve`'s sequence over the call `openTool`
names — the waiter registered, the approval written `pending` with the run flipped to
`waiting_approval` in one transaction, published — with the action's approval in place of the
call's: an `approvals` row of `kind` `action` whose `request` is the `tools.ActionRequest` as JSON,
the action and the body as sent. **Every end of the wait writes the approval and flips the run
back**, in one transaction without the cancel, then publishes: a decision writes `approved` with
its `duration` or `denied`, and a wait that ends without one — the request's context, the run's, or
past a subagent's `unansweredLimit`, which also stops its agent — writes `abandoned`, a status only
an action takes. The call's row keeps `running` throughout. An end the store refuses still comes
down: the journal takes it for the settle to write, and the command reads a 403 *this change could
not be recorded, so it was not sent* — as it does when the request itself cannot be written —
never the decision. A call keeps at most one approval of its own (`approvals_call_idx`, partial on
`kind = 'call'`), and the call reads join only that one; its actions, `toolCallEntry.ClusterWrites`,
are read by statements of their own over the same scope (`callReads`), in `created_at, id` order,
and `writeCalls` writes them again at the settle. On the wire each is a `ClusterWrite` in
`ToolCall.clusterWrites` — a Secret read too, a `GET` with no body — with its `PermissionAction`, carrying its body, media type and diff only
while it waits — pending on a running call — since each publish sends the list whole, and its
`reason`. **An action the policy decided is recorded with no wait** (`recordAction`,
`actionAsker.Record`): one approval of `kind` `action`, written already decided, `allowed` or
`refused` — statuses only an action takes — with its `reason` in the user's words, against the
open call; the run stays `running`. **`approvals.duration`** is how long an approval holds, what the
user chose — `once`, `command`, `chat` or `always` — on `approved` alone, and `once` on a call's own.

**A decision claims its waiter first** (`service.Approve`). `pending[id]` holds a `*waiter`: its
channel, the chat, the `ActionRequest` it waits for (nil for a call's own) and `forgotten`, which
the turn sets when it stops waiting — `forget(w)` is the one way it does, deleting `pending[id]`
only if it is still `w`. Under `turnsMu`, `Approve` reads the waiter and asks it for the
`decision` (`waiter.decide`: a call's own, and an action whose `Grantable` is false, take `Once`
and `Deny` alone, anything else `ErrBadRequest` with the waiter left in place), removes it, then
lets go:
`Command` delivers approved with `command`, the proxy keeping the rule; `Chat` writes
`permissions.GrantRule(act)` through `addGrant`, and `Always` into `securityconfig`'s rules under a
fresh `appdb.NewID()`, each unless the same rule is held there already (`sameRule`: equal once the
ids are cleared), then delivers. The rule lands before the decision, outside `turnsMu`, through
`ruleWrite` (`writeRule`, a test's seam). A write that fails takes `turnsMu` again and puts the
waiter back unless it is `forgotten`, then answers the error; `Always` while the settings hold
rules Kstack cannot read is `securityconfig.ErrHeld`, which `approvalErr` (`graph/util.go`) maps to
`KSTACK_VALIDATION_ERROR` naming `security.json`.

**A chat's rules are `chat_grants` rows** (`grants.go`), each a `permissions.Rule` as JSON whose
`ID` is the row's, cascading with the chat. `grantsFor` reads them on every decision, never cached;
a row that does not decode, one with a key `Rule` does not name included, or a read that fails, is
logged and read as `permissions.Refused`. `addGrant(ctx, chatID, rule)` writes one in a
transaction that refuses a chat that is gone (`ErrChatGone`): a rule with no `ID` under a fresh one,
the rule the chat already holds answered instead; a rule with an `ID` replaces the chat's rule under
it, so the id outlives the change, and an `ID` the chat does not hold is `ErrGrantGone`.
`RemoveChatGrant` deletes one by id, `ErrGrantGone` for an id the chat does not hold. On the wire they are
the `chatGrants(chatID)` query and the `chatGrantRemove(chatID, id)` mutation, which answers the
rules left; `ErrGrantGone` is `KSTACK_RECORD_NOT_FOUND`.

**`foldersFor(ctx, chatID)` is the one builder of a session's folders** (`grants.go`): the chat's
folder grants, then the always ones (`folderRules`, over `security.Rules()`, so a held `rules`
grants none), each checked again by `securityconfig.Service.CheckStoredFolder` — a folder that
fails is left out and logged with its reason — and none at all where `sandboxStatus` is not
`Available`.
`sessionFor` sets `Folders` to it, and to none for a chat switched outside the sandbox, so `bash`
and the file tools read one checked list (`TestFoldersForAnswersOnlyCheckedFolders`,
`TestNoFolderAppliesWithoutASandbox`). `FoldersFor(ctx, "")` is the always folders alone, for a run
with no chat. **`GrantFolder(ctx, chatID, path, write)`** checks the path as a new grant
(`CheckFolder`, a `securityconfig.FolderRefusal` on refusal), then writes a `Rule{Allow,
ReadInside|WriteInside, Folder}`: a chat's through `putGrant` in one transaction with the lookup
(`readGrants`, the chat's rules read on the transaction's own statements), and with no chat (`""`)
an always one through the store's `PutRule`, one update; a folder already granted in the same
place takes that grant's id, so its mode changes in place, and two grants of one folder at once
leave one rule (`TestAGrantInTheSamePlaceChangesItsMode`,
`TestConcurrentGrantsOfOneFolderLeaveOneRule`). `RevokeFolder(id)` removes an always folder grant,
`ErrGrantGone` for an id that is not one. `FolderGrants(ctx, chatID)` is the always grants and the
chat's, each a `FolderGrant` with `Refused`, its check's reason now, for Settings and the composer;
while the rules field is held every always grant is refused with `RulesHeldReason`, since `Rules()`
grants none then, and stays listed for Remove (`TestAHeldRulesFieldRefusesEveryAlwaysGrant`).

**The live message lists every call, off its rows.** `ChatMessage.ToolCalls` is the turn's
calls as one JSON string of `ToolCall` (`id`, `toolUseID`, `name`, `arguments`, `status`,
`actionKind`, `action`, `approval`, `output`, `isError`, `background`, `clusterWrites`) — a string so the record stays comparable and a
status change with the content unchanged is a `Modified` frame. **One function spells it**,
`marshalToolCalls` over the rows, on both paths: the turn rebuilds it under `turnsMu` on every
row write (`publishLive`), and the stored read (`toolCallsByRun`, in `listMessages` and
`answerByRequestKey`) reads `tool_calls` joined to `approvals` and marshals each run's rows the
same way, since SQLite's `json_object` and Go's encoder escape differently. Every message built
in Go starts at `[]` (`emptyToolCalls`), what the stored read gives one with no rows. The
tool-calls statements are `OnBoth` and issue on the caller's `st`, since `seenBefore` also runs
inside `Send`'s write transaction; a caller on the pools goes through `transcript` (or
`InReadTx` directly), one read transaction so the messages and their calls cannot tear.
`toolCallStatus` is the one mapping: `awaiting_approval` → `AwaitingApproval`; `denied`,
`running`, `succeeded` → their own; `failed` with `started_at` NULL → `NotRun`, set with the
error `cancelled`, `timeout`, `stranded` or `interrupted` → `Interrupted`, else `Failed`.
`action` is the call's arguments and `cwd` read through the service's box (`Box.Action`,
`marshalToolCalls(rows, box)`), null when nothing reads it or its tool refuses them —
so a Bash call that never reached the gate still shows its command, with an empty `cwd`.
`approval` is null on a call nobody was asked about and otherwise the approvals row as `ToolCallApproval` —
`id` and `status`, the typed `ApprovalStatus` the row stores and the GraphQL enum binds onto —
so the record can say a call was approved and never started.

**No external operation follows a failed write.** A failed model start write is logged and the
call proceeds; the finish write heals it. A failed model finish write ends the turn only when
the reply asked for tools, since only then would something follow; otherwise the turn settles
`Complete` and the settle write heals the row. A failed tool write ends the turn either way
(`runCalls`), by two rules. **Running rows land before any call runs**: a running write that
fails runs nothing, and every call of the reply settles `not-run` on the row it was to run on.
**A failed write under one call cuts its siblings**: a write that fails in flight — a child's
run insert, or a write inside a child — cancels the reply's context as soon as `spawn` sees
it, ahead of the child's settle write, so no external operation follows it in that call, in a
sibling, or in the parent after the reply; a cut sibling ran, so
its row stays as the call closed it (`succeeded` when the tool had already returned,
`cancelled` otherwise) and the record answers it `{"error":"cancelled"}` — `not-run` means
nothing started. A tool that returned keeps its real result — never a database error in its
place — and a finished row that did not land keeps it too, with nothing following. The fold is
the one place that tells the turn's cancel from a sibling's cut; `runCall`, `runTool` and
`spawn` see one context, and `spawn` decides a failed write ahead of it, since its own cut has
ended that context too. **Settlement holds the one retry**: `closeOpen` closes in memory what the
recorder never closed, and `settleRow` writes every call row of the run again in its
transaction, which `settleUntilLanded` retries until it lands, the service stops, or a delete's
write takes the chat. A tool the cancel cut is closed by what it did: an error
under the ended context is `{"error":"cancelled"}`; a real result is `succeeded`, though the
turn ends before its block joins the record.

**Usage is inclusive in public and uncached on disk.** `llm.Usage.Reported` says the provider
sent a usage object; a count a present report omits is a known zero. `llmCallEntry.setUsage` is
the one conversion, run by `LLMCallFinished`: `input_tokens` stores `InputTokens −
CacheReadTokens − CacheWriteTokens`, and an absent report, or one whose cache counts exceed its
input (logged, never clamped), stores NULL in all four token columns. An interrupted or stranded
call's usage is NULL. `inputTokens()` sums the three columns back into everything the model
read, and **unknown differs from zero**: four NULLs for a call that reported nothing, four zeros
for a reported zero. A call's span is `finished_at − started_at`, and a
recovered one (`error = 'stranded'`) has none, since its finish time is the recovery's. **The
message row serves none of these sums**: `ChatMessage` carries what the send asked for and the
turn's status, and the call rows are where usage and timing are read.

- **`Chat` and `ChatMessage` are comparable by value**: `sql.NullInt64`/`sql.NullTime`, never
  pointers, and every time is built from the stored millis with `time.UnixMilli(...).UTC()`. The
  watch diff is `==`, and two freshly scanned pointers to equal values compare unequal — which
  would re-send every historical message as `Modified` on every ping.
- **One turn per chat**, enforced by a mutex-guarded reservation in `Send`, with the in-flight
  check and the reservation in one critical section. Never in a composer: two windows on one chat
  both pressing send is ordinary.
- **The turns mutex is never held across a database call.** The writer has one connection, so a
  holder that then needed it would deadlock the service. Every holder works in memory and lets go.
- **Every statement is named in `statements.go` and prepared at open** through `sqlstmt.Set`. Each entry declares its pool: a write `OnWriter`, a read `OnReader`, and a read a
  send runs inside its transaction `OnBoth`, since on the reader it would miss the transaction's
  own writes. `InTx` hands out a `stmts`, never a `*sql.Tx`; a read outside a transaction lands
  on the reader, so a watcher's re-read never waits for a turn's checkpoint.
  → [ADR: app.db SQL discipline](../docs/adr/2026-09-14-app-db-sql-discipline.md).
- **The in-memory copy is the truth while a turn runs.** Every read overlays the in-flight
  message on its stored row — rows first, then the mutex only to copy.
- **A turn ends in one order** (`runTurn`): settle, then remove the overlay and release the
  reservation under the mutex, then notify. Reversed, a watcher re-reading on the completion
  ping copies the stale `streaming` overlay over the settled row and no later ping corrects it.
- **`Delete` cancels and joins the chat's turn** off the mutex before its rows go, so the settle
  lands first — or waits only until the settle's first attempt fails (`retrying`): the writer has
  one connection, so a later attempt either lands before the `DELETE` or finds nothing to write,
  and the delete's write closes `gone`, which ends the retry. A delete whose own write fails
  returns at once and leaves the turn to finish its own settle. From the moment it looks the turn up until
  its write returns, the chat is marked `deleting` (under `turnsMu`, a count so overlapping
  deletes agree), and `reserveTurn` refuses a send on it as `ErrChatGone`: without the mark a
  send could reserve between the join and the commit and run on rows about to vanish. Everything
  after the join runs on a context bounded by the service's stop, not the caller's.
- **Anything that outlives its caller joins the service's one `WaitGroup`** — the sweeper, the
  pumps, a delete's write, a send and its turn — and it joins through `enter()`,
  never a bare `wg.Add`, which races the stop func's `Wait` and admits work `Close` then pulls
  the connection out from under. `enter` refuses once the service is stopping: `Delete` answers
  `ErrStopping`, so does `Send`, and a watch is an already-ended stream. **A delete's write runs on a context
  of the service's own, not the caller's**: a client that gave up still asked for the chat to
  go, and stop cancels a write parked behind the one writer connection rather than waiting on
  it. The service's context exists from construction, so a watch that arrives before `Start`
  is answered — and **a failed `Start` joins those watches itself**, since the lifecycle stops
  only what started. `Close` runs after the stop func, so no reader is left holding the
  connection.
- **On startup every run still `queued`, `running` or `waiting_approval` becomes `failed`**
  (`failStrandedRuns`, `strandedReason`), in `Start`, before the sweeper runs; a start whose
  reconcile fails is a failed start. The statement returns each run's chat, and `Start` pings
  those chats' message keys after the commit, so a watch opened before it sees the answers
  fail rather than stream forever, and the chat list, since a stranded run may have been
  waiting on the user. Nothing resumes a queued run. **Then every open call is
  closed** (`closeStrandedLLMCalls`, in the same transaction): a model call with no `finished_at`
  gets `error = 'stranded'` and `finished_at` at the recovery — under a terminal run too, since
  a crash can land between a run's settlement and its call's. A repeat closes nothing. The tool
  rows' close returns with the tools.
- **A send is idempotent on its `requestID`**, a UUID the client minted (`appdb.ValidateUUID`, else
  `ErrBadRequest` before anything is read). The key is the user message's `request_key`, and the
  lookup (`seenBefore`) is **one joined statement** (`stmtSelectAnswerByRequestKey`: message → run →
  answer), so a delete committing between its parts cannot show a message with no answer. It runs
  before every other check, and again inside the send's transaction (the first read was a reader's
  snapshot, and a retry can race the first attempt): a retry of the very send that started the
  turn must be answered, never refused as a second one. On a hit the key is the whole identity:
  the retry's `chatID`, model and content are ignored, not compared, and the answer comes back
  overlaid, so a retry landing mid-answer carries the text so far. **The key goes with its
  message**: a retry after the chat is deleted is a fresh send — a create-shaped one makes a new
  chat, and one naming the deleted chat is `ErrChatGone` from `checkChat`.
- Eight named errors, each mapped to a stable code by the GraphQL layer (below): `ErrBadRequest`,
  `ErrTurnInFlight`, `ErrChatGone`, `ErrClusterGone`, `ErrStopping`, `ErrChatContextFull`,
  `ErrChatSandboxChanged`, `ErrChatNetworkChanged`.
  `Cancel` and `Delete` on an unknown chat are no-ops.
- **Every send into an existing chat checks the chat fits the model it names** (`roomFor` in
  `context.go`, inside the transaction after `checkChat` and before anything is written), and so
  does the turn a finished background task starts (`startNoticeTurn`, which `kick` lets pass
  quietly when it is refused). Two rules, on the box the turn would get (`boxFor`):
  - **The chat's newest turn failed with `contextFullText` on its first request, on the same
    provider and model** (`newestRun`: the call with `seq = 0` has an error). A refused request
    reports no usage, so the count alone would admit it; the model stays refused until a turn on
    another model succeeds, Ask again included. A turn that overflowed on a later request was
    pushed over by its own tool results, which the next request drops, so it refuses nothing.
  - **The tokens the newest reported call of a succeeded turn on the same provider read and
    wrote** (`lastContextUse`: `inputTokens()` plus the output) exceed `contextCeiling(model,
    allowance)` — the window less `MaxOutputTokens` less `turnInputAllowance(box, maxToolCalls)`,
    a sum of named terms in `context.go`: the context block at its budgets and a long question,
    `maxToolCalls` results at `typicalResultBytes` when the box `HasRunner`, and each `Budgeted`
    tool's `Allowance()` (10K for the searches' results), all at `bytesPerToken`, so a change to
    a budget moves it. **A call that ran a server tool is not counted**: inside one request the
    provider samples again after each server call, and whether its usage counts the chat once
    or once per sampling is unverified, so an older call answers instead.

  Either is `ErrChatContextFull`, and nothing is written.
  The count is read per provider because each replays the transcript in its own shape, so only
  a turn on the same provider is a prefix of the request it gates, and through
  `messages.run_id`, so a subagent's calls do not count against its parent's chat. A model with
  no stated window, one under the cap and the allowance, or one whose provider has no counted
  call on the chat is never refused ahead; results past the typical size, notices and failed
  turns' text are the provider's refusal to catch. The estimate is the provider's own count;
  the sidecar has no tokenizer.
- **Every send checks its cluster inside the writer's transaction** (`checkChat`, after the replay
  lookup and before the insert): a create-shaped send checks the cluster it names, an
  existing-chat send the chat's own, and a cluster that is missing or marked for deletion is
  `ErrClusterGone`. That serializes acceptance against the mark: whatever committed before it is
  the sweeper's, and nothing lands after it. **A chat has no dialect to check**: a send may name
  any model in the catalog, and its run records the provider's dialect (`writeTurnRows`); a
  replay is answered before any of it. `roomFor` counts per provider, so a switch to a provider
  with no count on the chat is not checked ahead. → [ADR: a chat can switch to any
  model](../docs/adr/2026-09-25-a-chat-can-switch-to-any-model.md).
- **The chat sweeper** (`sweep.go`, `startSweeper` from `Start`) subscribes to `KeyClusters`
  before its first sweep, then sweeps once at startup and on every signal: `markedClusterIDs`, then
  `deleteByCluster` for each, cancelling and joining turns. **A delete that removed a row pings
  `KeyClusters`, whoever asked** — the sweeper, or a user deleting the last chat of a marked
  cluster ahead of it — and one that found nothing pings nobody, since a no-op ping would wake
  the mirror and the mirror's pass would wake the sweeper again. A failed sweep re-arms its own retry after `sweepRetry` (a field a test
  shrinks), since no signal follows a failure; a sweep cut short by the service's own stop
  warns nothing. `markedClusterIDs` is this service's one cluster statement, and `checkChat`'s
  `clusterAccepts` the other, when the send lands.
- **Never retry a failed turn.** It costs money and the user did not ask twice. A refusal is a
  *complete* turn whose finish reason says so. A chat the provider refused to read
  (`llm.Error.ContextFull()`, code `context_length_exceeded` — OpenAI's spelling, which the
  Anthropic encoder classifies its two "too long" messages under while keeping nothing of the
  message) settles `failed` with `contextFullText` as the message's and the run's error — one
  string, which `roomFor` compares whole — so the transcript reads a sentence and, when the
  turn's first request overflowed, a repeat on that model is refused ahead; the call's own row
  keeps the redacted provider error.

**Two watches, two bus keys.** `WatchList` and `WatchMessages` return `chatsvc`'s own
`Stream[T]`; the bus is the DB's (`Notify`/`Subscribe`) and the keys are `appdb`'s:
`chats` and `messages/<chatID>` (the rows changed — re-read and diff). A watcher
subscribes, then reads, then sends `Added` frames and one `Bookmark`; a re-read that fails ends
the watch with the reason on `Err`, and a consumer leaving ends it cleanly wherever the pump
had got to. Writers notify **after** commit: `Rename` the list key, `Delete` both. Every send
goes through `deltafold.Send`, every pump reads through `RecvContext` under a context derived
from the service's own, and each watch has its own `deltafold.Fold` (`newChatFold`,
`newMessageFold`, equality `==`). `WatchMessages` reads through `readMessages`, the stored
rows overlaid, and a turn's `Progress` pings the messages key, so a chunk is a re-read and a
diff like any other change; a burst coalesces on the receiver. The third key,
`stream/<chatID>` (the answer grew — rebuild the one overlaid message from memory, no read, no
diff) is not wired yet: **a stream ping that finds no live message sends nothing**, and `Has`
turns one for an answer the fold has not met into a re-read, so the question and the answer go
out in order.

**The model is `internal/llm`'s**, and the service holds the llm service rather than one encoder.
**Every send names its model and effort**, checked against the llm service **after the replay
lookup** — a replay answers by `requestID` alone, and a check ahead of it would refuse a retry of a
send that already ran because the catalog moved in between. `agent_runs` carries `provider` (the
provider's id), `model`, `effort` and `dialect`, all written **when the send is accepted**, so a turn
that fails or is interrupted still says what it was asked to run; the run stores the catalog id the send named, and
the name the provider reports back goes on each model call's row. On the wire the id resolves to
a `Provider { id label }` through the llm service, and a provider it no longer holds is
labelled by its id: a message outlives its key. The chat itself carries none of
them: what a chat "is on" is whatever its last answer ran at, and any send may name any model. `Chunks` is
for display and `Content()` is for the record: a clean turn stores the provider's own blocks, a
failed or cancelled one keeps the live content — and so does a clean turn whose reply carries
no blocks, since an empty `content` column is not JSON and every later turn of that chat would
resend it. **The live content is the settled rounds' blocks in order, then the current round's web
calls seen on the stream as `server_use` blocks with no native form, then its
thinking summary so far and its text so far** (`liveContent(rounds, seen, thinking, text)`,
over `llm.AnswerBlocks`). `stream` keeps a builder per text chunk kind and a list of the
web calls, and that
content is what a checkpoint writes, what a cancel or failure keeps, and what a stranded row
still holds when the next start fails it, so the summary a reader saw survives every path the
provider did not finish; a completed turn's summary lives in the native block instead. The
fake's bank is fixed and taken in order, so a test knows each reply's text, and its
`chunkDelay` is a parameter, never a constant (~30ms by hand, `0` in tests). `SetGate` holds a
reply after its first chunk, so "mid-answer" is waiting on the live overlay, then closing the
gate; it thinks a sentence before it answers, so `FailAfter(n)` and the gate count thinking
chunks too, and the live overlay a gated reply shows is the thought so far with no text yet.

**A turn is a loop over `Stream`, and its tool rounds live in the answer's row.** `answer`
streams a reply, appends its blocks to the record, answers every `tool_use` in it, and streams
again with the rounds so far as the request's last assistant message. One row, `Streaming` until
it settles, so `Send`, the overlay, the checkpoint, the reservation and the replay are untouched;
the encoder unfolds the row into the wire's messages. **Every call gets a result, whatever the
finish reason**, and the finish reason decides what the result is (`toolLoop.take`,
`chatsvc/tools.go`): `tool_use` with calls that fit the budget runs each under `toolTimeout` and
goes round again; `tool_use` with no call is a malformed reply and settles; calls beyond the
budget run **none** — each answered `budget` — and one synthesis round follows, whose own calls
are answered `budget` before the row settles `Complete` on `tool_use`; any other finish settles,
a call left in it answered `not-run` — except **`pause_turn` with no call**, the provider
pausing its own search loop: the next round resends the rounds so far unchanged and it
resumes, at most `maxPauses` (3) times per turn and only while every server tool the turn
offers has uses left, otherwise the turn settles `Complete` on `pause_turn` — a request that
stops offering a tool drops its calls from the history, the paused reply's included
(`TestAPausedReplyIsResumed`, `TestPausesAreCapped`, `TestAPauseIsNotResumedOnceAServerToolIsSpent`). **The budget counts calls,
not rounds** (`maxToolCalls`, a field, 8 in production), and a refused call spends it too, so a
turn streams at most `maxToolCalls + 2 + maxPauses` times (`TestTheStreamCountIsBounded` pins
the first two terms). A refusal the loop writes is
`{"error":"<code>"}` with `IsError`, never a Go error's text: `budget`, `not-run`, `unknown-tool`
(a name the definitions do not list; `Call` never sees it), `timeout`. **A timeout is not a
cancel**: `runTool` hands `Call` a context derived from the turn's with `toolTimeout`. The turn's
context ending settles the row `Cancelled` with the rounds so far; the call's deadline with an
error result in hand writes `timeout`; a real result is kept whatever the clock says. A
`Stream` error on any round settles `Failed`, and so does a call-row write that would otherwise
be followed by an external operation (above). A cancelled or failed row keeps its rounds and
sends none of them — `buildRequest` sends `Complete` rows only, and a `Complete` row holds a
result for every call, so no request carries an unanswered `tool_use`. Usage and model time
are summed across model calls by the read (above), a spawned child's included; the finish
reason is the run's own latest one.
→ [ADR: tool rounds live in the answer's row](../docs/adr/2026-09-14-tool-rounds-live-in-the-answers-row.md),
[ADR: a tool call is a committed row before it runs](../docs/adr/2026-09-17-a-tool-call-is-a-committed-row-before-it-runs.md).

**`chatsvc` takes cluster tools the way it takes cards.** `ClusterTools` is
`Definitions() []llm.CustomTool` and `Call(ctx, clusterID, name, input) (text, isError)`;
`chatsvc.New(db, llmSvc, cards, clusterTools, shell)` takes one, nil offering none. A turn's
custom tools are the cluster tools plus `spawn_agent` — only when the service holds a
`ClusterTools` **and** the target's model takes tools (`customToolsFor`), and the system prompt follows the same test: the base file, then `tools.md`
or `no_tools.md`, then a third section — what a parent may delegate (`spawn.md`), or who a
child answers (its agent's file, `general.md`). All under `prompts/`. Per-run stable either
way, so the prefix cache holds.

**A question carries a cluster card when the card has changed.** `chatsvc.New(db, chatsDir,
llmSvc, clusterCards, memories, box, lists, sandbox, security)` takes a `ClusterCards` — `ClusterCard(ctx, clusterID)
string`, the one thing this package asks about a cluster — which `internal/clustercard` implements
over `clustersvc.Service`, and a `Memories` — `Section(ctx, clusterID)`, every note the cluster
sees — which `memorysvc` implements. `sandbox` is whether the machine offers sandboxed Bash, which
the switch and the `## Sandbox` section need, and `security` the service each session's modes,
rules and folders are read from, and a folder checked against. The
question's `context` block is the card, then the notes as its `## Memory` section
(`withMemory`, through `clustercard.WithSection`; a section that is not one JSON value is sent
`{"unavailable":true}`), then the chat's workspace as its `## Workspace` section, `{"path": …}`
(`withWorkspace`, `workspace.go`), since the file tools take absolute paths alone, then, on a
machine with a sandbox, where its commands run as its `## Sandbox` section,
`{"commands":"sandboxed","network":…}` or `{"commands":"outside"}` (`withSandbox`, beside it, over a
`sandboxState`), `network` being `off`, `on for this chat`, `on for this message` or `unavailable on
this machine` (`networkValue`, the switch over the toggle) and absent outside the sandbox, where the
switch changes nothing; while it is sandboxed the section also lists the folders `foldersFor`
answered as the send began: `read` and `readWrite`, at most `maxListedFolders` (20) in all, then
`more`, how many it left out (`TestTheContextListsTheGrants`). Those two are
appended inside the transaction, once `resolveChat` has answered the chat's id, under the cluster
`checkChat` answers the chat is filed under, never the send's argument, with the switches it read off
the same row (off for a chat the send creates) and the send's toggle; the path is fixed
for the chat, so it changes the block on the chat's first send alone, and a switch or a toggle
changes it on the next question, a question after one sent with the toggle saying `off` again.
A nil `Memories`, which only tests pass, sends the card alone. `Send`
renders both before its transaction (`service.contextText`), for the chat's stored cluster,
which a turn's tools also reach (`Runtime.ClusterID`, from `chatOf`, one read of the
chat), and which the subagents it spawns reach too
(the send's `clusterID` is only what a create files under), under `clusterCardTimeout` — the
render's alone, so the rows go in on the send's own context, and a render that outlives it is
the unavailable card. The replay lookup is ahead of it, so a retry renders none. Inside the
transaction `questionBlocks` compares the card to the newest `context` block in the chat's
record (`newestContext`, one `json_extract` query): equal, the user row is `[text]`; different,
`[context, text]`. So the first message of a chat always carries one, and a send that lands
after another compares against the row that one wrote. **A context block is always its
message's first block** — that is where the query reads it. A card that cannot be rendered is
`clustercard.Unavailable`, a fixed text deduplicated like any other, which the system prompt
reads as withdrawing every earlier card; the send is never refused over it. The history hands
the rows to the wire as they are, so a replay sends the cards where they were.

The card is the `## Cluster` section of a `context` block, a fenced JSON object on one line, with
no tags of its own — `llm` wraps the block in `<context>` tags on the wire
(`clustercard.WithSection` appends a second section to it, re-marshalled through the card's own
escaping — how a child agent's message carries its parent's context), **one section per question
the model asks**: `cluster` (display `name`, kube-`context`, server
version — the context is `ContextName`, the record's kube-context cut as the card cuts it and
empty for none, the one spelling a sandboxed run's kubeconfig names its context by; its cap,
`contextMax`, is 192 bytes, past the longest real context, since outside the sandbox a command names
it in the user's own kubeconfig, where a cut one names nothing), `connection` (the record's verdict as `status`, and `tls` — `verified`, `unverified`,
`none` — off the kubeconfig entry the status mirrors, never its server URL; the scheme is resolved
through `rest.DefaultServerURL` as client-go dials it, so a schemeless server is `none` unless the
entry names a CA, the user a client certificate, or verifying is skipped — which is why the mirror
carries `HasCertificateAuthority` and `HasClientCertificate`, presence alone), `freshness` (one
verdict over the active cache) and `inventory` (`namespaces` and `apiGroups`, each under its own
status; absent with no cache). Every value is an escaped JSON string, so nothing the server
spells can close the block or end the fence. `Render(Facts)` is pure and never over `Budget`
(4 KiB): four parts with fixed shares — the head, `notWatching`, the namespaces, the groups —
every list sorted and cut to the longest prefix that fits with a `more` count, every single value
cut to its cap on a rune boundary. `ClusterCard` is `Clusters().ReadActive`
around `Read(ctx, svc, ActiveCluster)`, which gathers the facts of the reading it is handed — the
record's fields, and with a cache its health, sync rows, kind catalog and namespace names — and
resolves nothing; any error is `Unavailable`. KubeQuery reads a cluster through `ReadActive` too
(`tools/kubequery/cluster.go`): its answer's `cluster` is the display name, else
`Cluster.KubeContext()`, and `clustercard.MarshalFreshness` marshals its freshness section as the
card does.

**`Freshness` restates the health rollup for a reader with one question**, and is `syncing` for
a cluster with no cache. The cache-wide arms
read the rollup's reason: `syncing` (none yet, or `Connecting`), `watching`, `paused`, `unknown`
(`SizeLimit`, `StoreFailed`, `IdentityMismatch`). A cluster with sync switched off reads `Paused`
with no counts, which answers before any count is read. Past them the reason is only the first
offender's, so **the arm is decided by count**: every unpaused kind behind the verdict
(`len(UnhealthyKindRefs) >= TotalKinds − PausedKinds`, off the health reading, which
`CacheFacts.Health` carries whole) is `last-known` with the
reason and `since`; fewer is `partial` with the `notWatching` list. `since` is the health
reading's `LastLiveAt` and rides `last-known` alone — with every kind behind, no watch can move
the stamp, so the card's text holds still, which is what the dedupe needs. **The card carries no
counts** (`TestRenderCarriesNoCounts` allows a number only under `more`): one that moves on its
own either resends the card every turn or goes stale with nothing saying so. A list's status is
its own kind's verdict after the cache-wide arms; a non-`Watching` kind with rows lists them as
`last-known`, one with none is `unknown`, so an empty list is never the answer for a kind that
is not watching.
→ [ADR: the cluster card rides the user message](../docs/adr/2026-09-13-the-cluster-card-rides-the-user-message.md),
[ADR: the cluster card carries no counts](../docs/adr/2026-09-14-the-cluster-card-carries-no-counts.md),
[ADR: the cluster service resolves a cluster](../docs/adr/2026-09-27-the-cluster-service-resolves-a-cluster.md).

**Memory is `internal/memorysvc`'s, one row per fact.** The `memories` table holds each note — a
name and a body — for one cluster (`cluster_id`) or every cluster (`NULL`), with `written_by`, the
`chat_id` that last wrote it (`SET NULL` with the chat; the cluster's delete cascades), and the
`server_uid` the cluster had at the last write (`ServerUIDReader`, which `app` reads off
`Clusters().Get`), which nothing reads yet. A note for every cluster carries no server UID: a
CHECK refuses one that does. A name is unique within its scope (`ifnull(cluster_id, '')` in
the index), so a cluster's note and a note for every cluster may share one. Every write runs
`checkNote` (`note.go`: the name's pattern and `NameMax` (48), the body's `BodyMax` (500
bytes), and `safe.HasSecret` over both, a field out of shape `ErrBadInput` wrapping a
`FieldError`; a forget checks the name alone), refuses a missing or marked cluster (`ErrClusterGone`), and holds the scope's
notes to `ScopeBudget` (4 KiB) as the section encodes them, `ErrFull` past it, a move measured
against the scope it enters. **`Save`, `Forget`, `SaveEverywhere` and `ForgetEverywhere` are
the model's**, by name, through the Memory tool: the first two on the chat's cluster, the last two
for every cluster, which the tool calls only once the user has approved the call. Each reaches
only its own scope (`scopeByName`), so a cluster's note and a note for every cluster of one name
never stand in for each other, and a name the user's note holds in that scope is `ErrUserNote`,
so the model changes only notes it wrote. A model save for every cluster writes the model's note,
with the chat's id and no cluster or server UID. `CheckSaveEverywhere` and
`CheckForgetEverywhere` run the same transaction with every check and write nothing, so the tool
can ask before the user is asked. `Create`, `Update` (by id: rename and move either way) and `Delete` are the
dialog's, and every dialog write makes the note the user's. `Watch(clusterID)` is a delta watch of
`Visible`, re-read on `KeyMemories` and `KeyClusters`, comparing only what the wire carries.
`Section` is every note the cluster sees, whole: `today`, then each note's name, scope, `by`,
date and body, the cluster's own first, each by name. The refusals are `memorysvc`'s own; the Memory tool maps each to
its code and `graph` maps them to `KSTACK_MEMORY_NAME_TAKEN`, `_FULL` and `_SECRET`, bad input to
`KSTACK_VALIDATION_ERROR` and a missing id or cluster to `KSTACK_RECORD_NOT_FOUND`. The `Memory`
tool (`internal/tools/memory`) is a `tools.Gated` custom tool that asks about a call with
`scope: everywhere` alone: its `Approval` first asks the store's check what the write would
answer, and a refusal — shape, credential, `user-note`, `not-found`, `full` — is a
`*tools.Refusal` carrying the code `Run` would give, so the user is never asked about a call that
must fail. It skips every other call, one that does not parse or a turn with no `Memory`
included, so `Run` refuses it. It answers each call as one JSON line, a refusal as
a code (`user-note` among them). **A note the user wrote is a
request; one the model wrote is information** — the system prompt says so, and the `agent`'s
*Data is not instructions* names the one exception to its rule: the notes marked `"by":"user"` in
the `## Memory` section of the newest `<context>` block, nowhere else. → [ADR: memory is rows](../docs/adr/2026-09-24-memory-is-rows-scoped-to-a-cluster-or-every-cluster.md),
[security record](../docs/security/2026-09-24-memory.md).

`chatSend` carries the mode and the cluster as required arguments, read only when a null `chatID`
creates the chat — an existing chat keeps the mode and cluster it was made with, and a `requestID`
replay ignores both like the rest. `Send` checks the mode whatever `chatID` is: the enum guards the
wire, nothing guards a Go caller. **The cluster is checked by the service, inside the send's
transaction**; the resolver forwards the send and maps the refusal, so `ErrClusterGone` reaches
the client as `KSTACK_RECORD_NOT_FOUND`. A repeat is answered ahead of that check, by its key.
`clusterDelete` marks the cluster and nothing more; the sweeper deletes its chats.

**The wire surface is eight mutations, two queries and two watches**: `chatSend`, `chatCancel`, `chatRename`,
`chatSandboxDisabledSet` (the switch, spelled as `clusterEnabledSet` is), `chatNetworkEnabledSet`
(the network switch), `chatDelete`,
`approvalDecide` (an `ApprovalID` and the `ApprovalDecision` — `Once`, `Command`, `Chat`, `Always`
or `Deny`; true when the decision reached a waiting turn), `chatGrantRemove` (one of a chat's rules,
with the `chatGrants` query), `sandbox` (the `sandbox.Status` the app built, fixed for the sidecar's life, which
`graph.Resolver.SandboxStatus` holds, its `networkAvailable` and `networkReason` the network pair), `chatsWatch` and `chatMessagesWatch(chatID)`. A mutation is named for what it
acts on, so the decision is the approval's, not the chat's, though chat's turns are what wait on
one today. A cluster's deletion reaches here
through the sweeper: the chats filed under a marked cluster go with it. Every window reads the list
and a transcript off the watches, and a field exists because a call site needs it. `Chat`,
`ChatMessage` and both frame wrappers bind 1:1 in `gqlgen.yml`; `ChatID` and `MessageID` are
separate scalars because they are separate Go types (`codegen.ts` maps both to `string`). Both
watches go through **`watchStream`** (`graph/watch_failure.go`), never `ptrStream`, so the terminal
reason rides the same `watchFailed` extension the cluster watches use.

**`ChatMessage`'s field resolvers are the whole distance between the row and the wire**:
`finishedAt` unwraps the `sql.NullTime` the record holds so a watch can diff with `==`, and
gqlgen has no marshaler for it; `toolCalls` parses the one string the record carries
(`ChatMessage.ToolCallList()`) into `ToolCall`s, which bind 1:1, with `ToolCallID` a scalar
of its own and `ToolCallStatus` and `ToolCallNetwork` (`ToolCall.network`, why a sandboxed call
reached the internet, null for none) bound member by member like the other enums; `thinking` is `llm.Thinking` over the message's content blocks —
`ChatMessage.Thinking()`, empty for a question and for content that does not parse — so the
webview reads a summary without learning a provider's block shape; `provider` is the row's id labelled by itself, the
stand-in for a provider the catalog lacks, which every provider is until the catalog exists —
it becomes a lookup in the llm service, as `Model.provider` is the model's. The
GraphQL `Provider` is a generated model, of which only `id`, `label` and `llmDialect` are declared — the key
and the endpoint never reach the wire; its `llmDialect` is nullable, null on
the `{ID, Label}` stand-in `ChatMessage.provider` synthesizes for a provider the catalog lacks.
They are
declared in `gqlgen.yml` rather than inferred, so a regen never reintroduces them as stubs. `role`, `status` and
`mode` need none, nor does an `LLMDialect`: the enums bind member by member (`enum_values`) onto
`chatsvc.Role`/`MessageStatus`/`Mode` and `llm.Dialect`,
whose constants carry the stored lower-case vocabulary. gqlgen refuses the binding unless every
schema member is named, which is what stops a new value reaching the wire with nothing behind it.
The other direction — a constant with no member — regenerates cleanly, so `llm.Dialects` is the
one list of the set and `TestLLMDialectBindsEveryMember` walks it against the schema.

**A refused mutation names its reason in `extensions.code`**, mapped by `chatErr` (`graph/util.go`)
— a Go error's identity does not cross GraphQL, and the code is what the log and a client can branch
on. Anything absent from the table stays opaque. **Return `errors.Clone(…)`, never the `graph/errors` value
itself**: gqlgen stamps `Path` and `Locations` onto the error a resolver hands it, so a shared one
would report the first request's field ever after, and two requests failing at once would write to
it concurrently.

| Service error | Wire error (`graph/errors`) | `extensions.code` |
| --- | --- | --- |
| `ErrBadRequest` | `ErrValidationError` | `KSTACK_VALIDATION_ERROR` |
| `ErrChatGone` | `ErrRecordNotFound` | `KSTACK_RECORD_NOT_FOUND` |
| `ErrClusterGone` | `ErrRecordNotFound` | `KSTACK_RECORD_NOT_FOUND` |
| `ErrTurnInFlight` | `ErrConflict` | `KSTACK_CONFLICT` |
| `ErrStopping` | `ErrServiceUnavailable` | `KSTACK_SERVICE_UNAVAILABLE` |
| `ErrChatContextFull` | `ErrChatContextFull` | `KSTACK_CHAT_CONTEXT_FULL` |
| `ErrChatSandboxChanged` | `ErrChatSandboxChanged` | `KSTACK_CHAT_SANDBOX_CHANGED` |
| `ErrChatNetworkChanged` | `ErrChatNetworkChanged` | `KSTACK_CHAT_NETWORK_CHANGED` |
| `llm.ErrBadRequest` | `ErrValidationError` | `KSTACK_VALIDATION_ERROR` |
| `llm.ErrModelUnavailable` | `ErrConflict` | `KSTACK_CONFLICT` |

## Security settings (`internal/securityconfig`)

**`<data>/security.json` is the security settings**, 0600 through `atomicjson`, in the data
directory no sandboxed command reads, and never synced. `app.New` opens it on every platform;
`Store` has `Get`, `Update`, `Subscribe` (a `gochan/watch` receiver, current on subscribe) and
`Refused`. Each step of the agent-security sequence adds its own field to `Settings`,
`omitempty` (`omitzero` for a struct), and names it in its spec. `Path` is the first. `Store` wraps the generic
`store[Settings]` so it can carry methods of `Settings`' own.

- **Every value crosses a JSON copy** (`clone`): `Get` and each send are copies, so a caller
  never reaches the store's value; receivers share one delivery and treat it as read-only.
- **Equal means the same file.** An `Update` that leaves the file's JSON as it is writes and
  publishes nothing, so a nil slice swapped for an empty one is no change. The first write
  that changes something creates the file; `Open` writes nothing.
- **Decoding is per field, and a list per element.** `Open` reads the file as a JSON object and
  decodes each key into the field `encoding/json` writes under it. A `null` is refused, since
  `encoding/json` would read it as the zero value. One bad list element is refused
  alone; any other value of the wrong type is refused whole, and the other fields load. A key no
  field names is ignored and kept: every write puts it back as read, so an older Kstack's write
  keeps a newer one's setting. Only a file that is not a JSON object, `null` included, fails
  `Open`, naming the file.
- **Every write stamps `schemaVersion`**: `schemaVersion` in `store.go`, or the file's own when a
  newer Kstack wrote it, since this build cannot upgrade what it does not know. A step that
  changes a field's layout bumps it and upgrades an older file in `Open`.
- **A hand edit may cost a permission, never a restriction.** `strictest` (`check.go`) maps each
  field that restricts to what sets it to its most restrictive state. A refusal on such a field
  sets that state, never the zero value, and the store keeps the field's raw JSON, which every
  `Update` writes back until one names the field in `fields` (its JSON key), which ends the hold.
  **The store guards a held field**: an `Update` that changes one without naming it is `ErrHeld`
  and writes nothing, so no writer drops the value by accident; `Held(field)` says whether the
  store still keeps it. The fix can be the strictest state the field already answers, which
  changes nothing in memory, so a Settings section's mutation names the field it writes. A field
  not listed only grants, and a refused value of it is dropped.
- **The read-back is `checks`** (`check.go`), one per field, each added by its field's step. A
  check reads the value alone, removes what it refuses, and answers a `Refusal` (field, value,
  reason in the user's words) for each. On `Open` every refused value is logged and kept for the
  store's life in `Refused()`, which the `securityRefused` query serves. On `Update` a refusal is
  the error (`Refusal` is an `error`), and nothing is written. `WithChecks` is the test seam that
  swaps the list.
- **The core is generic** (`store[T]`), so the tests run it over their own settings type.
- **The approval modes and the always rules** (`permissions.go`): `DefaultMode` (`Ask` when
  empty), `Modes` (`ContextMode`s, first match wins) and `Rules`. `ModeFor(context)` is a
  `ContextModeState`: read-only while `modes` is held (source `refused`), else the first entry
  whose pattern matches (`entry`, with the entry's pattern and whether it is the context's own,
  `Own`, what `ClearMode` removes), else the default (`default`); `DefaultMode()` is the default,
  `Ask` when unset. `Rules()` is the always rules, or while `rules` is held those less every
  `Allow`, plus `permissions.Refused` and `permissions.RefusedSecrets`, which a session reads per
  write. `FieldDefaultMode`,
  `FieldModes` and `FieldRules` are the fields' keys. The mutations: `SetDefaultMode` (names
  `defaultMode`, its fix), `SetMode` (the context's literal, first, replacing one already there),
  `ClearMode`, `AddRule`, `PutRule` (the first rule a predicate passes swapped in place under its
  id, else appended), `RemoveRule` (`ErrNoRule`) and `DiscardRefused` (`modes` or `rules`,
  naming it; `ErrNotHeld` otherwise). The checks refuse a mode that is not one of the three, an
  entry with no context, and a rule with an unknown effect, a class other than 1, 2 (a folder
  grant), 4, 5 (the two a cluster write is decided as) or 6 (a Secret read), an `Allow` of class 5, a cluster rule
  naming a `Folder`, a folder rule that does not allow, names no folder or one not absolute and
  clean or `/`, or names a cluster field (`folderRuleRefusal`, the value alone, never the disk), a
  class 6 rule marked `Inside`, naming `[cluster]`, or a verb, group or kind other than `get`,
  `list` or `watch`, `core` and `secrets` (`secretRuleRefusal`), or
  an empty, repeated, `refused` or `refused-secrets` id; a
  list element with a key its type does not name is refused before them, since a misspelled
  narrowing field dropped would widen the rule. `defaultMode`'s strictest line sets `ReadOnly`;
  `modes`' and `rules`' leave what passed and only hold the field, whose strict state `ModeFor`
  and `Rules` apply where they read it.

**`Settings.Path` is the user's `PATH`, frozen** (`path.go`): a `PathEntry` per folder, in the
shell's order — `Dir` as the shell gave it, `Target` the folder it resolved to when its state was
set, `State` (`adopted`, `pending`, `gone`), `Source` (`shell` for the sync's decision, `user` for
Include's or Remove's) and `Shared`. A state holds for `Target` alone. Its check refuses an entry
whose `dir` or `target` is not absolute, a repeated `dir`, or an unknown state or source; its
`strictest` line keeps the entries that passed, since a refused one may have been a removal.
`filterPath` (`filter.go`) drops, by the resolved path, what `sandbox.SearchFolder` refuses, then
`node_modules` and duplicate entries, and marks `Shared` a folder writable by a group that is not an administrators'
one (`admin` and `wheel` on macOS, `root`, `wheel`, `sudo` and `admin` elsewhere, gid 0 on both;
a group that cannot be looked up is shared, and each gid is looked up once per process).

**`Service` (`service.go`) is the store plus the sync**, built by `app.New` over the store with
`Zones` (`Never`; `Open`, the bash tool's shell's `System(home, shell).Files`; and `Home`, for the
broad folders), the refresh's
resolver and the launch's fault; on a machine with no sandbox it has neither and syncs nothing.
`Service` embeds the `Store`, so every store method is its own. An entry is **open** when its
`Target` is under one of `Open`'s Read paths and none of its Deny paths, every list resolved once
per sync (`sandbox.Resolved`) and compared by text (`sandbox.Under`). `SyncPath`
reads the disk on a goroutine abandoned when its context ends or `SyncTimeout` (5s) passes, at
launch and on refresh alike, then diffs in one `Update` naming
`path`: a new entry open and not shared is `adopted`, any other new one `pending`; an entry whose
`Target` changed is filed again as new; every entry whose `Target` held takes the folder's
`Shared` as it is now; a shell-adopted entry now shared or no longer open goes
`pending`; an `adopted` or `pending` entry the shell no longer lists is dropped, a `gone` one kept
after the listed ones; and the shell's order wins; and it sets `PathResolved` and clears
`PathStrict`. While the field is held, or while `PathStrict` is set — by a Remove that ended the
hold, stored so it survives a restart — every new entry is `pending`. Both marks restrict: a
refused value answers resolved and strict. A move of the
first adopted folder holding `kubectl` or `helm` logs one line; the probes are a diagnostic, so
they look at adopted folders alone and run on a goroutine after the write, and one that hangs
never holds a sync. `RefreshPath` resolves again,
keeping or clearing the fault (`PathFault`), and syncs; `AdoptPath` (Include) sets a `pending` or
`gone` entry `adopted` for the target the user was shown, refused `ErrPathChanged` when a refresh
has moved the entry since and `ErrPathHeld` while the field is held;
`DropPath` (Remove) sets an `adopted` or `pending` entry `gone`. Each refusal is a `PathRefusal`
in the user's words; each path method, and `Path`, answers `ErrNoSandbox` or nothing on a machine
with no sandbox, so the resolvers ask the service alone. The refresh's resolver,
`loginshell.Path` in the sandbox (`app/shellpath_unix.go`), is bounded by `DefaultTimeout` itself. `RunCheck` (`checkrun.go`) is `filterDir` and the
adoption rule over one folder for a run, and says whether the run's open folders read it already.
→ [ADR: `PATH` is the login shell's, filtered and frozen](../docs/adr/2026-10-02-path-is-the-login-shells-filtered-and-frozen.md),
[security record](../docs/security/2026-10-02-path-from-the-login-shell.md).

**A folder grant is checked against the zones** (`folders.go`). A grant is a rule
(`permissions.Rule.Folder`, class 1 read or 2 read-write; it matches no action, so it never
reaches a verdict, and its `Line` is *Allow reads of …* or *Allow reads and writes of …*, the
folder quoted when it holds a space, `"` or `\`). `CheckFolder(path, write, z, pathEntries)` is
nil or a `FolderRefusal` (`Rule`, `Reason` in the user's words, and for `link` and `spelling` the
canonical `Target`): not absolute or clean, `/`, on or under a fixed mount (`sandbox.FixedMount`),
not a folder, a link on it or its way (`link`), or spelled otherwise than the disk lists it
(`spelling`: a case-insensitive filesystem opens `/Users/ME` as `/Users/me`, and the zones are
compared by text; `TestAFolderSpelledInAnotherCaseIsRefused`, on macOS) — for a stored grant,
`CheckStoredFolder`, either is `moved` — under a
never path (`Zones.Never`, resolved, so a never path that is a link is caught at its target), or
exactly a closed folder; read-write also not the home, nor on, under or over a `Zones.Open` Read
path or a `PATH` entry (`tool`), nor over a closed folder, nor on, under or over a `NoWrite` path
(`code`), nor over a never path (`TestCheckFolderRefusesEachRule`, `TestALinkIsRefusedWithItsTarget`,
`TestAMovedGrantIsRefused`, `TestAFixedMountCannotBeGranted`, `TestAWriteGrantOnCodeThatRunsIsRefused`).
**Every path compared is canonical** (`canonical`, `filter.go`): its links followed
(`sandbox.Resolved`), then each component spelled as its directory lists it (`sandbox.Spelled`,
by `os.SameFile` against the listing), so two names of one folder compare equal by text.
**The service holds the zones as a snapshot, as the zones function listed them**: `SyncPath` takes
it with each read of the disk, the login shell's `PATH` unfiltered beside it, and the first check
takes one when no sync has (`TestTheZonesAreASnapshot`), so no check runs the zones function's own
reads again. `Service.CheckFolder` and `Service.CheckStoredFolder` resolve the snapshot's zones
again on every check (`TestACheckResolvesTheZonesNow`) and check against them and every stored
entry's `Dir` and `Target` with the login `PATH` (`PathEntryFolders` is the stored half), and
refuse every folder on a machine with no sandbox (`TestNoSnapshotRefusesEveryFolder`). **Every
disk read of theirs goes through `readWithin`**, bounded by `SyncTimeout` and the caller's context
(*Kstack could not check this folder in time.* for a check): taking the snapshot, a check, and
resolving the home for `WideFolders`. `Hidden()` is the snapshot's never and closed folders for
the file tools, as listed and never taking a snapshot; `NeverReadable` and `WideFolders` (the home
and each folder over it, and `/Volumes` on macOS; nil past the bound) are what Settings draws.
→ [ADR: a folder grant is a rule](../docs/adr/2026-10-04-a-folder-grant-is-a-rule.md),
[security record](../docs/security/2026-10-04-path-grants.md).

**`Executables` is the user's registered executables** (`executables.go`), in the order added, each an `Executable{Name,
Invocation}` the probe checks beside `CuratedExecutables`. `CheckExecutable(name, invocation)` is the one
shape check, run on register and on read-back: a name is 1 to 64 bytes of `[A-Za-z0-9._+-]`, not
starting with `-`, not `.` or `..`, and no curated name; an invocation is 1 to 8 whitespace-split
fields, each under 256 bytes with no control character, its first the name. Its refusals are
`ExecutableRefusal`s, in the user's words. `RegisterExecutable` defaults the invocation to `<name> --version`
and refuses a name listed already; `RemoveExecutable` refuses one not registered. An executable only widens
what is probed, so the read-back drops a refused one, or a name listed twice, and holds nothing.

## Auth / identity (`internal/auth`)

Local-first accounts against kstack-cloud's Hydra: system browser (auth-code + PKCE, loopback redirect), verification via go-oidc, refresh token in the OS keyring. Signed-in ⇔ refresh token present; works offline; degrades to signed-out when unconfigured. → [ADR: local-first auth & settings](../docs/adr/2026-08-09-local-first-auth-settings.md).

- Flat root package by file: `auth.go`, `grant.go` (token set as source of truth, `Authenticated`/`Identity` derived, lazy refresh with burst-dedup, persist-before-cache), `login.go` (synchronous setup, bounded detached tail), `keyring.go`. `auth/oauth` is a leaf that must not import `auth`.
- `Config` carries production knobs only; test seams are unexported functional options on `newWithOptions`. No `Start`/`Close`. `Logout` clears locally first, revokes fire-and-forget.
- `TokenSource(ctx)` is nil when degraded; consumers read `AccessToken` only. The GraphQL projection drops tokens.

## Cloud settings sync (`internal/cloud`)

An edit applies to a local JSON file immediately and queues durably for the cloud. **`cloud` depends on `auth`, never the reverse**, tracking only the `Authenticated` bit. Degrades without its paths (`cloud.Paths{SettingsFile, QueueFile}`) or a cloud URL. `Start` is idempotent. Sub-packages leaf-first: `syncstore`, `prefs` (pointer fields + omitempty so absent ≠ cleared), `mutationqueue`, `api`, `prefsync` (the reconcile `Engine`; `Watch` returns data plus a buffered terminal-error channel). Test seams as in `auth`.

## Kubeconfig (`internal/kubeconfig`)

**The one reader of the user's kubeconfig.** Nothing else watches the file, calls `clientcmd`, or builds a `rest.Config`. `New` reads nothing, not even `KUBECONFIG`: `Start` builds the loading rules, the watchers and the poke subscription and reads once, and `app.New`'s shell import has finished by then, so a GUI launch reads the file list the login shell exports. `Get()` returns the last snapshot plus whether a read has happened; `Subscribe()` is current-on-subscribe; `Close()` ends every subscription in the process, so only the app calls it.

`RESTConfig(contextName)` resolves one context to credentials and the pool's cache key. Three rules: one snapshot per call; only a config the loading rules produced (a hand-built `api.Config` yields CA paths that cannot open); the key excludes the context name, covers the static exec/auth-provider config and `proxy-url`, and length-prefixes every value. Two sentinels acted on, not logged: `ErrContextNotFound` (also for an empty name) and `ErrNotRead`. Resolution is not memoized.

A zero-length kubeconfig loads as a valid config with no contexts, so it is never published while the merge has no contexts at all: `clientcmd` truncates before it writes, and a reload landing in that window would read downstream as every cluster vanishing. A reload stats the chain before and after its load and drops a load any file changed under, since the window can open and close between two stats; the change's own event, or the next tick, reloads.

Watches are pull-first: a 30-minute backstop tick under fsnotify and a poke subscription. **Keep the tick under any new push layer.** The service watches directories and follows symlinks; use `resolvePaths` and keep every path in one namespace (no `filepath.EvalSymlinks`, which rewrites every component and matches nothing on macOS).

## Resync broadcaster (`internal/poke`)

A leaf: wall-clock gap detector (15s tick, 2× factor) plus a `gochan/broadcast` hub. `Poke(src)` never blocks. A poke is a fan-out, never a cascade through spec counters or conditions. → [ADR: poke resync fan-out](../docs/adr/2026-08-09-poke-resync-fanout.md).

## GraphQL via gqlgen

`graph/schema.graphqls` is authoritative and also feeds the frontend's codegen. One file, sectioned by noun, `Query`/`Mutation`/`Subscription` at the end. After editing:

```sh
cd sidecar && go run github.com/99designs/gqlgen generate
```

Implement the panicking stubs it appends to `schema.resolvers.go`. **Never hand-edit `generated.go`/`models_gen.go`.** Re-run the frontend `pnpm codegen`. Renaming a schema file is a two-pass regen: delete the old `*.resolvers.go` between passes and check no body came through as `panic("not implemented")`.

## Patterns

- A type's methods live in the type's file; a helper belongs to whoever calls it, and only what more than one needs goes on the service.
- Pub/sub: unkeyed → `gochan` (`watch` for latest-value with a seed, `broadcast` for fan-out); keyed → `gobus` (`watch` delivers nothing until the next send; `conflate` for bursts). Never hand-roll a subscriber map.
- Work to do is a queue, not a bus: `internal/workqueue`, one `Queue` per job. `Done` is owed for every key taken.
- Subscription resolvers emit the current snapshot first, then deltas (`mapStream`), and honor `ctx.Done()`.
- Unexported functional options for test seams; exported `New` takes production knobs only.

## Tests & checks

- testify + `httptest`. Resolver tests stand up `graph.NewServer(&graph.Resolver{...})` + `POST /graphql`; lifecycle tests stand up `app.New(...)`. Filesystem via `t.TempDir()`.
- A fixture that needs a stored status writes it with `beehive.NewAdminClient`, never by registering a controller. A controller's own writes are asserted by calling `Reconcile` against a stubbed `ControllerClient`.
- White-box tests by default (`package foo`). External `package foo_test` only to pin a public contract, and say so.
- No magic sleeps (root `CLAUDE.md`). A cadence becomes a parameter whose production value is the constant.
- Wait on channels through `internal/testutil` (`Wait`, `Recv`, `RecvClosed`, `WaitClosed`); the one failsafe is `testutil.Timeout`. A negative assertion gets its own short window.
- A fake that notifies the test uses `testutil.Signal` (single-shot, idempotent `Fire`) or `testutil.Probe[T]` (repeating, non-blocking, drops oldest). Exception: a consumer doing edge detection needs a lossless fan-out (`internal/cloud`'s `fakeAuth`).
- `make test-changed` while working (the changed packages), `make test-go` for the whole suite, `make lint-go` (gofmt), `make vet-go`. Run `gofmt -w` before committing.

**Coverage is gated.** `make cover-go` (CI's `Go · Coverage` job) runs the suite twice —
untagged and `-tags debug`, the only build that compiles `applyEnvOverrides` — merges the
profiles with `-coverpkg=./...` so a helper exercised from another package counts, drops
the generated files, and fails below `scripts/coverage-threshold`. `make cover-go
ARGS=-report` lists every file with a gap. The gate is one number in one file: **raise it
when a change lifts coverage, and never lower it without saying why in the commit body.**

It measures what the platform builds, so run it on one OS — a `_windows.go` file is not
compiled on Linux and never enters the denominator. What stays uncovered is deliberate:
`main()` itself, the `serve returned` arm main can't provoke from a test, `testutil`'s
own failsafe-timeout arms, and the I/O failures nothing can inject (a `Write` to a
successfully-created temp file, a `BeginTx` on a healthy pool).

When you change the sidecar's wiring or conventions, update this file in the same change. When you change *why*, write an ADR.
