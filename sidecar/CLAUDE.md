# sidecar — Go backend

A Go binary the Tauri host starts. It serves GraphQL and a gRPC control channel and owns all
Kubernetes logic. **No TCP**: it listens on a Unix socket (named pipe on Windows), prints
`READY unix:<path>`, and exits on `SIGINT`/`SIGTERM` or **stdin EOF**. `--data-dir`, `--cache-dir`
and `--runtime-dir` are required and absolute; tests pass `t.TempDir()`.

**`<data-dir>/app.db` belongs to `internal/appdb`, and the app owns it.** `build`
(`internal/app/runtime.go`) opens it before any service, under the data directory's lock, and hands the `*appdb.DB` to each service that uses it;
a service closes only the statements it prepared. `appdb.Open` gives a one-connection migrated
writer and a `query_only` reader, plus a janitor. A service's statements are a `sqlstmt.Set` on
`db.Write, db.Read`. A table change is the next numbered migration (*Persisted formats*). Tests open the
DB through `appdb/appdbtest`, never `appdb.Open`. **The change bus**: `Notify(key)` after a commit,
never inside it; `Subscribe` before the first read; pings coalesce, so never count them. The keys
are all named in `appdb.go`. `NewID()` mints UUIDv7; `ValidateUUID` accepts only canonical v4/v7.
→ [ADR: the app owns app.db](../docs/adr/2026-09-16-the-app-owns-app-db.md),
[ADR: a shipped schema changes only by a new migration](../docs/adr/2026-11-08-a-shipped-schema-changes-only-by-a-new-migration.md).

## Directories

**`app/paths.go` names every path under the three directories** (the doc comment on `paths` is
the tree). Each owner gets its own `Paths` (`cluster.Paths`, `bash.Paths`) and names nothing else.
**Each subtree has one owner**, which makes it 0700, sweeps it and removes it. `<data>` is what a
user would lose, `<cache>` what Kstack rebuilds, `<runtime>` what lives for a session. The host's
log directory is fenced too (`bash.Paths.DeniedDirs`, `TestTheLogDirectoryIsKstacks`). → [ADR:
each file in the platform's directory for its
kind](../docs/adr/2026-09-27-kstacks-files-follow-the-platforms-directory-kinds.md).

**One sidecar per data directory.** `build` locks `<data>/sidecar.lock` (`internal/lib/filelock`:
`flock` on Unix, `LockFileEx` on Windows) before anything opens a file there. The lock is the
runtime's first part, so it closes last. A second sidecar fails `app.New` with *another Kstack is
using `<data>`*, which `run` prints as `FATAL`. The kernel drops the lock when the process ends, a
crash included. `build` runs after the `--keys-fd` re-exec, which a lock would not survive, and the
`sandbox-*` subcommands never take it. **Never delete `sidecar.lock`**: a second process would lock
a new file under the same name while the first still holds the old one.

## Guides

This file is the whole sidecar: the process, its directories, the layering, the socket, the
security invariants, the small services, and the conventions every package follows. The larger
packages each have a `CLAUDE.md` of their own, loaded when you work in them:

| Guide | Covers |
| --- | --- |
| `internal/services/cluster/CLAUDE.md` | clusters, caches, kinds, cached data; the private leaves `kubeconn`, `kubesync`, `kubestore`; streams and watches |
| `internal/services/logs/CLAUDE.md` | the log cache (`logstore`), the line vocabulary (`logline`), refusals (`refusal`), the kubelet reader (`kubelet`), pages, follows, tailers |
| `internal/services/settings/CLAUDE.md` | `settings.json`, `jsonsettings`, the frozen `PATH`, folder grants, executables |
| `internal/llm/CLAUDE.md` | dialects, providers, the record's blocks, the wires, the fake |
| `internal/run/CLAUDE.md` | the login shell, the session, the sandbox, permissions, the cluster proxy |
| `internal/tools/CLAUDE.md` | the box, actions and results, each tool (Bash, Read/Write/Edit, WebFetch, KubeQuery, LogsView, LogsRead, LogsWatch, web search), `fileguard`, `logsinput` |
| `internal/agent/CLAUDE.md` | the catalog, the loop, the chat service (turn, rows, approvals, tasks, the wire errors) |

## Layout

`main.go` is lifecycle only; `internal/app` is the composition root; GraphQL is `graph/`.

**`build` fills one `Runtime`** (`internal/app/runtime.go`), a field per service in build order.
A service never sees the runtime: it takes constructor arguments. Field order is build order is
start order (`rt.add`); stop and close reverse it. An edge back to a later service is a setter at
the end of `build` (`SetProbeFolders`, `SetCacheFiles`). **kubeconfig before cluster is
load-bearing** (`app_test.go`). → [ADR: the runtime is built in
order](../docs/adr/2026-10-06-the-runtime-is-built-in-order.md).

`main` runs `sandbox.Main` first (the `sandbox-*` subcommands), then `scrubExecEnvironment`
(`scrubexec_unix.go`): started with a provider key variable set, the sidecar execs itself once with
the keys on a pipe named by `--keys-fd` and the variables left out, so the launch strings the
kernel keeps hold no key; the flag is the re-exec's alone, and a flag naming anything but a
readable pipe stops the start. Then it tightens the umask (the original becomes
`Config.UserUmask`), parses flags, and installs the logger. `run(ctx, cfg, …)` is
the tested seam; `main` alone sets `RunLoginShell`, so tests never spawn a shell. `main` itself is
tested in the test binary started as a sidecar (`runMain`, `TestMain`'s `childEnv`), whose coverage
merges into the run's; such a test fails before `app.New`, which would run the real shell. `New` runs the
login shell **before anything reads the environment**. **`READY` promises a socket, not a finished
startup**: it prints after the bind, before `Start`.

**Logging** (`internal/lib/logging`): one JSON object per line, keyed as the host keys its own;
**nothing here formats a human-readable line**. A bad `--log-file` falls back to stderr and never
stops the sidecar. → [ADR: JSON logs rendered by the
host](../docs/adr/2026-09-08-json-logs-rendered-by-the-host.md), [ADR: two processes, two log
files](../docs/adr/2026-09-08-two-processes-two-log-files.md).

- `graph/` — schema, generated code, resolvers. Resolver deps are non-nil; tests wire fakes. A
  domain refusal reaches the wire as `KSTACK_VALIDATION_ERROR` through a mapper in `graph/util.go`.
  A `Chat` folder grant with no `chatID` is refused, never widened to every chat.
- `grpc/` — committed protoc output; regenerate with `make generate-proto`, **never hand-edit `*.pb.go`**. It runs `scripts/protoc.sh`, which fetches the pinned `protoc` and builds the plugins from `go.mod`'s `tool` block.
- `internal/` → [ADR: services, lib and the
  rest](../docs/adr/2026-10-06-sidecar-packages-group-into-services-lib-and-the-rest.md):
  `services/` (state outliving a request, reached from `graph/`/`grpc/`), `lib/` (no Kstack
  domain; `lib` is never a package name), `llm/`, `run/` (what confines one run), `agent/`
  (above `tools`), and `app`, `appdb` at the top. `sqlitepool` is the one home of the SQLite open
  contract: every pool is a `Pool`, whose close returns once its connections have closed, so
  a store deletes its file straight after closing; a file closes all its pools under one
  `CloseBudget`, and one a connection outlives is kept as stale until a retry waits it out
  (the cluster and logs guides); `apimeta` is the wire vocabulary no service owns (the delta-frame type, `ObjectID`,
  `ClusterID`, `ChatID`); `sqlstmt` is a store's statement table (→ [ADR: one statement
  set](../docs/adr/2026-09-16-sqlstmt-prepares-a-services-statements.md)).

**The folders are layers**; an import points only right to left:

```
lib → appdb → llm → run → services → tools → agent → app
```

`TestInternalFoldersAreLayers` fails on a backward edge: move the package, never add the edge. →
[ADR: folders are layers](../docs/adr/2026-10-07-sidecar-folders-are-layers.md).

## gRPC + GraphQL over one socket (h2c)

`internal/app` owns the topology; `grpc/` owns `IsGRPCRequest`. `main.go`'s `http.Server` speaks
`app.Protocols()` (HTTP/1.1 plus unencrypted HTTP/2). → [ADR: single-socket h2c from the standard
library](../docs/adr/2026-10-26-single-socket-h2c-from-the-standard-library.md).

**Shutdown** (`shutdown.go`): `srv.Shutdown` (stops listening, then its hook runs `CancelStreams`,
so SSE and gRPC streams end at once; requests get `requestGrace`, 10s) → `CancelStreams` again, since
`Shutdown` does not wait for its hooks → `CloseRequests` (a later
request gets a 503; every handler still running is cancelled, and its connection cut if the grace
ran out) → `WaitRequests` (every HTTP handler, then gRPC `Stop`, which waits for its method
handlers) → `stop` → `Close`. Each step logs its duration under the name it has in the log:
`drain requests`, `cancel requests`, `wait requests`, `stop services`, `close services`. One
watchdog bounds the whole stop at `shutdownBudget` (20s, the grace plus 10s): it removes the
socket file, logs every goroutine's stack, one record each, and exits 1. Traps: cancel per
request, never via
`http.Server.BaseContext`, which kills the shared connection; handlers are joined before `stop`, and
loops before `Close`, after every `Shutdown`, since `Shutdown` counts streams, not handlers, and a
gRPC method handler outlives its `ServeHTTP` once the client resets the stream; nothing after the
grace has a deadline but the watchdog. The host's kill (`SIDECAR_SHUTDOWN_GRACE`) sits above the
budget.

## Security invariants

Full picture: [`docs/security-model.md`](../docs/security-model.md).

- **Every endpoint is an argument** (`configFromArgs`). The environment reaches the config only
  through `applyEnvOverrides`, a no-op without `-tags debug` (`make build-sidecar-dev` builds one, for a
  run with no host), via the `getenv` it is handed; `KSTACK_FAULTS`, which fails cluster requests,
  is among what it reads. `KSTACK_LOG_LEVEL` is the one variable a release build reads directly:
  a log level redirects nothing.
  **Provider keys are the exception**: read in every build, registered with `safe.AddSecret`
  before the logger exists, then stripped from the process (`takeProviderKeys`); the shell's keys
  join `Config.LLMKeys` and are never set on the process. `config_test.go` pins it. → [provider
  keys from the login shell](../docs/security/2026-10-07-provider-keys-from-the-login-shell.md).
- **Only the host process may connect.** `ipc.Authenticated` checks the peer pid against
  `--host-pid`; the socket is 0600 from birth, the pipe owner-only. Never widen it or add a TCP
  listener. The GET transport registered beside POST and SSE (`server.go`) is harmless only
  because the transport is local.
- **Redaction happens at write time, off the body's own group and kind**
  (`kubestore/objects.go`); a read path never re-derives it. It fails closed (check a `Nested*`
  read's `err`, not `found`). A cache file still holds cluster data in the clear, by decision. →
  [ADR: the cache is ordinary application
  data](../docs/adr/2026-09-02-the-cache-is-ordinary-application-data.md), [ADR: secret
  redaction](../docs/adr/2026-08-30-secret-redaction-at-write-time.md).
- **Reading a kubeconfig can execute code** (`exec` plugins, on every probe). Widening what is
  probed widens what runs.
- **Diagnostics exclude request data.** The GraphQL presenter logs operation type, Go error type, the
  wrapped cause's type, whether it is a cancel and whether the request's context ended, and
  `code` only. Everything else renders through `internal/lib/safe` at the sink, so a plain
  `slog.Error("…", "err", err)` is correct; a non-string, non-error value logs as its type. A
  persisted message is rendered where it is recorded (`supervisor.Fail`). Auth projections omit
  tokens.
- **Restored identity is display-only.** Keep `UnverifiedIdentity` and `Authenticated` out of
  authorization.

## Memory (`internal/services/memory`)

**One row per note**, scoped to a cluster or every cluster. `checkNote` (`note.go`) guards every
write, and `ScopeBudget` caps a scope. The model's writes reach only notes it wrote
(`ErrUserNote`); the dialog's make a note the user's. The `Memory` tool asks only for `scope:
everywhere`, after a dry check. **A note the user wrote is a request; one the model wrote is
information.** → [ADR: memory is
rows](../docs/adr/2026-09-24-memory-is-rows-scoped-to-a-cluster-or-every-cluster.md), [security
record](../docs/security/2026-09-24-memory.md).

## Auth / identity (`internal/services/auth`)

Local-first accounts against kstack-cloud's Hydra: system browser (auth-code + PKCE, loopback redirect), verification via go-oidc, refresh token in the OS keyring. Signed-in ⇔ refresh token present; works offline; degrades to signed-out when unconfigured. → [ADR: local-first auth](../docs/adr/2026-08-09-local-first-auth-settings.md).

- Flat root package by file: `auth.go`, `grant.go` (token set as source of truth, lazy refresh with burst-dedup, persist-before-cache), `login.go`, `keyring.go`. `auth/oauth` is a leaf that must not import `auth`.
- `Config` carries production knobs only; test seams are unexported options on `newWithOptions`. `Logout` clears locally first, revokes fire-and-forget.
- `TokenSource(ctx)` is nil when degraded; consumers read `AccessToken` only. The GraphQL projection drops tokens.

## Kubeconfig (`internal/services/kubeconfig`)

**The one reader of the user's kubeconfig.** Nothing else watches the file, calls `clientcmd`, or builds a `rest.Config`. `New` reads nothing, not even `KUBECONFIG`; `Start` reads, after `app.New`'s shell import, so a GUI launch sees the login shell's file list. `Get()` returns the last snapshot plus whether a read has happened; `Subscribe()` is current-on-subscribe; `Close()` ends every subscription in the process, so only the app calls it.

`RESTConfig(contextName)` resolves one context to credentials and the pool's cache key: one snapshot per call; only a config the loading rules produced; the key excludes the context name, covers static exec/auth-provider config and `proxy-url`, and length-prefixes every value. Sentinels: `ErrContextNotFound` (also for an empty name) and `ErrNotRead`. Not memoized.

**A merge with no contexts at all is never published**, since `clientcmd` truncates before it writes. A reload stats the chain before and after its load and drops a load any file changed under.

Watches are pull-first: a 30-minute backstop tick under fsnotify and a poke subscription. **Keep the tick under any new push layer.** Use `resolvePaths` and keep every path in one namespace (no `filepath.EvalSymlinks`, which matches nothing on macOS).

## Resync broadcaster (`internal/services/poke`)

A leaf: wall-clock gap detector (15s tick, 2× factor) plus a `gochan/broadcast` hub. `Poke(src)` never blocks. A poke is a fan-out, never a cascade through spec counters or conditions. → [ADR: poke resync fan-out](../docs/adr/2026-08-09-poke-resync-fanout.md).

## GraphQL via gqlgen

`graph/schema.graphqls` is authoritative and also feeds the frontend's codegen. One file, sectioned by noun, `Query`/`Mutation`/`Subscription` at the end. After editing:

```sh
cd sidecar && go tool gqlgen generate
```

Implement the panicking stubs it appends to `schema.resolvers.go`. **Never hand-edit `generated.go`/`models_gen.go`.** Re-run the frontend `pnpm codegen`. Renaming a schema file is a two-pass regen: delete the old `*.resolvers.go` between passes and check no body came through as `panic("not implemented")`.

## Patterns

- A type's methods live in the type's file; a helper belongs to whoever calls it, and only what more than one needs goes on the service.
- Pub/sub: unkeyed → `gochan` (`watch` for latest-value with a seed, `broadcast` for fan-out); keyed → `gobus` (`watch` delivers nothing until the next send; `conflate` for bursts). Never hand-roll a subscriber map.
- Fan-in from producers that come and go → `gochan` `mpsc` with `WithKeepOpen`, a sender per producer; without the option the hub closes for good once no sender is open.
- Work to do is a queue, not a bus: `internal/lib/workqueue`, one `Queue` per job. `Done` is owed for every key taken.
- Subscription resolvers emit the current snapshot first, then deltas (`mapStream`), and honor `ctx.Done()`.
- Unexported functional options for test seams; exported `New` takes production knobs only.
- **Production code reaches the API server through `kubeconn.Connection`'s `Dynamic` client or raw requests on its `HTTPClient`.** Never import `k8s.io/api/...`, `client-go/discovery` or the typed clientset (`client-go/kubernetes`): `kubernetes/scheme` links every API group, about 16 MB. Tests may use them. `make vet-go` and CI fail on them (`scripts/check-sidecar-deps.sh`).

## Persisted formats

Every file the sidecar keeps has a row in [`docs/persisted-data.md`](../docs/persisted-data.md),
with its class, its version stamp and the test that pins it, and that file's *How a change
ships* is the procedure for each kind of change. **A change to a format updates its row in the
same commit.** What the sidecar's side of it looks like:

- **A SQLite schema** (`appdb`, `kubestore`, `logstore`): the next numbered `*.sql` plus its
  SHA-256 line in the package's `shippedMigrations`. **A shipped migration is never edited or
  removed**: `TestAShippedMigrationIsNeverEdited` fails on a changed, added or missing file. A
  migration past `0001` ships with a test that builds the previous schema from the frozen files,
  seeds it, migrates and reads the seed back. **Rebuilding a `kubestore` table a trigger names**
  fails at `ALTER TABLE … RENAME` (*no such table* in the trigger): drop the triggers first and
  recreate them after.
- **A JSON column**: rides `app.db`'s version, so the change is an `app.db` migration.
- **A path under `<data>` or `<cache>`**: never renamed, moved or removed. There is no app-level
  data migration yet, so the layout only grows: a new path is its owner's to create.
- **`settings.json`**: a field's layout change bumps `schemaVersion` and upgrades in
  `jsonsettings.Open`; a newer file is read and its keys kept.

A refusal reaches the user. `sqlitemigrate` refuses a newer file with `*NewerSchemaError`, which `build`
turns into *app.db was written by a newer Kstack (…)*; the error's own text is the reason, logged
as `app init`. When `app.New` fails, `run` also prints `FATAL <reason>` on stdout for whatever
spawned it; the host shows it on the start screen. Tests make a newer file with
`sqlitemigrate/sqlitemigratetest`'s `StampNewer` and checksum a migration set with its
`Checksums`.

→ [ADR: each persisted format carries its own version stamp](../docs/adr/2026-10-29-each-persisted-format-carries-its-own-version-stamp.md),
[ADR: a shipped schema changes only by a new migration](../docs/adr/2026-11-08-a-shipped-schema-changes-only-by-a-new-migration.md).

## Tests & checks

- testify + `httptest`. Resolver tests stand up `graph.NewServer(ctx, &graph.Resolver{...})` + `POST /graphql`; lifecycle tests stand up `app.New(...)`. Filesystem via `t.TempDir()`.
- A fixture that needs a stored status writes it with `beehive.NewAdminClient`, never by registering a controller. A controller's own writes are asserted by calling `Reconcile` against a stubbed `ControllerClient`.
- White-box tests by default (`package foo`). External `package foo_test` only to pin a public contract, and say so.
- No magic sleeps (root `CLAUDE.md`). Wait on channels through `internal/lib/testutil` (`Wait`, `Recv`, `RecvClosed`, `WaitClosed`); the one failsafe is `testutil.Timeout`.
- A fake that notifies the test uses `testutil.Signal` (single-shot) or `testutil.Probe[T]` (repeating, drops oldest). A consumer doing edge detection needs a lossless fan-out instead.
- `make test-changed` while working, `make test-go` for the whole suite, `make lint-go` (gofmt), `make vet-go` (go vet and staticcheck on every GOOS, linux/arm64 and each build tag; silence a staticcheck finding with `//lint:ignore <check> <reason>`). `make check-changed` runs `make vet-go-changed` instead: go vet and go fix on the changed packages and go vet on their importers, on this machine's platform. `ALL_PLATFORMS=1` adds staticcheck and every other GOOS and tag. CI checks the rest. Run `gofmt -w` before committing.
- **`make vet-go` and CI fail on code `go fix` would rewrite** (`scripts/check-go-fix.sh`), on every GOOS, on linux/arm64, and on the `debug`, `livecache` and `liveswitch` tags. Run `go fix -stringsbuilder=false ./...` and review the result; `stringsbuilder` stays off.
- **CI's race step runs `go test -race -short ./...`.** A sweep too slow for the race detector shrinks under `testing.Short()`; a test never skips under it. The nightly stress run passes no `-short`, so the full sweep still runs.
- A test outside `services/cluster` that needs a real cache store uses `cluster/clustertest` (`NewStore`, `Apply`, `Delete`, `Mirror`, `Read`); it carries the query views. Its `Connection` is one a test can `Retire`.

**A nightly stress run hunts concurrency bugs** (`.github/workflows/stress.yml`): every CI
runner, `-shuffle=on -count=3`, one job per `-cpu` value (1 and 4; the `cpu` dispatch input is a
JSON array, `[1,4]`), each with `-race` (keeping `checkptr` on for modernc's SQLite) and without.
A test that hangs under `-cpu 1` is a finding.
`sidecar/scripts/stress.go` files one issue per failing test, labelled `stress`, with the command that
reproduces it from `sidecar/`:

```sh
go test -race -cpu 1 -shuffle=<seed> -count=3 ./internal/agent/chat
```

**Coverage is measured in `coverage / Go (<os>)`**, one runner per OS, beside the tests: the suite
untagged and with `-tags debug` (the only build with the environment overrides and
`KSTACK_FAULTS`; a test only that build can pass carries `//go:build debug`), both with
`-coverpkg=./...` and `CGO_ENABLED=0`, as the release is built.
`tools/coverignore` merges the two profiles and drops every block a marker exempts: the statement
or function whose first line ends in `// coverage:ignore <reason>`, body included, and the lines
from `// coverage:ignore-start <reason>` to `// coverage:ignore-stop`. Codecov merges the three
OSes, so a `_windows.go` line is judged by the Windows upload. The gate is the root `CLAUDE.md`'s *Testing conventions*. Generated files are
in `.github/codecov.yml`'s `ignore`. `make cover-go` prints this OS's numbers per function; `make cover-changed`, the changed lines this OS leaves uncovered.

When you change the sidecar's wiring or conventions, update this file or the package's guide in the
same change. When you change *why*, write an ADR.
