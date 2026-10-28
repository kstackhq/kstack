# Kstack

A Kubernetes desktop app. Three parts that talk to each other:

- **`src/`** — React webview. See `src/CLAUDE.md`.
- **`src-tauri/`** — Tauri/Rust native host (windows, tray, sidecar lifecycle). See `src-tauri/CLAUDE.md`.
- **`sidecar/`** — Go sidecar (GraphQL API, Kubernetes + cloud logic). See `sidecar/CLAUDE.md`.
- **`tools/`** — a Go module holding CI's Go tools (`govulncheck`, `staticcheck`) as `tool` dependencies, so Dependabot moves them. Run one with `go tool <name>` from `tools/`.
- **`proto/`** — shared gRPC contract (host↔sidecar). Single source of truth, compiled to Go (committed, via `protoc`) and Rust (via `src-tauri/build.rs`). Run `make proto` after editing.

The **`Makefile` is the polyglot entry point** (`make test`, `make lint`, `make vet`, `make proto`, `make cover-go`, `make cover-js`). The Go sidecar lives outside `package.json`. Its build is `scripts/build-sidecar.go` (`make sidecar` runs it); `tauri dev`/`tauri build` call it with `go run`, never `make` or `sh`, because on Windows Tauri runs them through `cmd.exe`. Other recipes may be `sh`. Environment variables that change a dev run are listed in [`docs/development-switches.md`](docs/development-switches.md). `make clean-data` deletes the dev app's `Kstack-dev` data and cache directories (refuses while a dev sidecar runs; `scripts/clean-dev-data.go`).

**Building inside a Linux sandbox (macOS host).** `scripts/sandbox-dev-setup.sh` **must run before any build, test, or install command**. A `SessionStart` hook in `.claude/settings.json` runs it; it is idempotent, so run it yourself if unsure. It keeps Linux build outputs out of the host's paths, so a `pnpm install` here does not overwrite the host's macOS native binaries. `node_modules` (one per worktree) and Cargo's `target/` go on the sandbox overlay under `/home/agent/kstack-build`; `dist` and the shared pnpm store stay on the host disk under `.sandbox-linux/`. Dev builds there set `CARGO_PROFILE_DEV_DEBUG=line-tables-only`, as CI does, to keep `target/` small. The overlay is 20G and tight: the script warns under 2G free. After the script remounts `node_modules`, run `pnpm install`. It no-ops outside the sandbox. → [ADR: sandbox node_modules on the overlay](docs/adr/2026-10-27-sandbox-node-modules-on-the-overlay.md). README has the one-time toolchain install.

## Host↔sidecar transport

Host and sidecar share one Unix socket / named pipe. **GraphQL** (webview↔sidecar) rides HTTP/1.1 (POST + SSE); **gRPC** (host↔sidecar control: tray auth watch, login/logout, resync poke) rides HTTP/2. Both are multiplexed in the sidecar's `internal/app`, served by `net/http` as HTTP/1.1 plus **h2c** (prior knowledge only); no TLS. The cluster surface is **GraphQL-only**. → [ADR: single-socket h2c from the standard library](docs/adr/2026-10-26-single-socket-h2c-from-the-standard-library.md).

**The caches fill.** A reachable cluster whose credentials can read `kube-system` gets a cache; the cache controller arms `kubesync`, which mirrors each served kind into one `ClusterCachedKind` record, and each record arms that kind's sync (cold list, then watch). Every record's `events(...)` field serves its control-plane timeline, as do `clusterEventsWatch(id: ClusterID!)` and `eventsWatch(id: ObjectID!)`. → `sidecar/CLAUDE.md`.

Cluster data streams as **Kubernetes-style delta watches**: an `Added` snapshot, one `Bookmark` closing it, then per-object `Added`/`Modified`/`Deleted`. They are `clustersWatch` and `clusterCachesWatch` (unscoped) and the cache-scoped `clusterCachedKindsWatch`. `clusterCacheHealthWatch` is **a gauge, not a delta watch** — current-on-subscribe, no `Bookmark`. The webview keys each stream into a map and **joins client-side**: caches onto clusters by `clusterID`, kinds onto caches by `owner.id`; the gauge keys on `cacheID`. **A cluster's id is the `ClusterID` scalar, every other record's is `ObjectID`; the webview never builds or parses either.** Rules for consumers:

- **Never render an empty state before the `Bookmark` lands.**
- **Detect it as `type === 'Bookmark'`, never as a missing entity** — a nested non-null field erroring nulls its parent, so a null entity also arrives on ordinary changes.
- A change with no entity is dropped, not folded.

→ [ADR: delta-watch protocol](docs/adr/2026-08-09-delta-watch-protocol.md). The kube-context picker derives its list from `clustersWatch` (`src/lib/kube-config.tsx`).

**The sidecar never filters what it streams.** A record awaiting deletion arrives carrying `deletionRequestedAt` (an ordinary `Modified`); `Deleted` means its row is gone. `clusterDelete` returns once the row is marked and the teardown follows. **Only the `Deleted` is promised** — a fast teardown folds the mark into it, so never gate a removal on having seen the mark. `ClustersProvider`'s fold folds a tombstoned row as a removal, once, for every view; change that there (e.g. to draw "Deleting…"), never in views.

> **Keep these docs current.** There is a `CLAUDE.md` per area (`src/`, `src-tauri/`, `sidecar/`, and one per larger sidecar package, listed in `sidecar/CLAUDE.md`); this file holds what crosses them. When you change architecture, conventions, commands, or add a pattern worth knowing, update the relevant `CLAUDE.md` in the same change.
>
> **Rationale lives in ADRs, not here.** These files state *what is true now* — invariants, conventions, commands, traps. *Why* a design was chosen, and what was rejected, goes in `docs/adr/` (see [`docs/adr/README.md`](docs/adr/README.md)), linked from here. When a decision changes, write a new ADR, flip the old one's status, and repoint every link in the same commit — a `CLAUDE.md` never links a superseded ADR.
>
> **Security is recorded in two places.** [`docs/security-model.md`](docs/security-model.md) states what is protected now and which protections a test pins; [`docs/security/`](docs/security/) holds dated review records, append-only. Gaps live in [`docs/TODO.md`](docs/TODO.md#security); an accepted risk becomes an ADR plus a **By decision** row in the model. When you change a boundary or land a protection, move its row in the same change.
>
> **Keep comments current — describe the present, not the past.** Code comments and these docs describe only the *current* code. **`docs/adr/` is the sole exemption** (append-only history). Don't leave notes about how code *used to* work, what was *renamed from* or *removed*, or which refactor got you here. Drop "used to", "no longer", "formerly", "superseded". Contrasts with *external* systems ("unlike apimachinery's default") are fine.

## Writing standards

Applies to every hand-written file — Go, Rust, TypeScript, Markdown. Not to generated output (`src/gql/`, `sidecar/graph/generated*`, lockfiles).

1. **Code — simple, idiomatic, easy for a human to follow.** Prefer the boring construction. Match the idiom of the file you are in over the one you would pick on a blank page. Cleverness that needs a comment to survive review is usually the wrong trade.
2. **Comments — terse, necessary, easy for a human to read.** Say what the code cannot: the *why*, the invariant, the trap that made this shape necessary. Never restate what the line already says. A comment justifying a choice the code no longer contains is dead weight — state the current design, don't argue against the alternatives you rejected (that is what `docs/adr/` is for).
3. **Documentation — simple, concise, easy for a human to read.** Lead with what is true now. One idea per sentence.

Watch for comments addressed to a *reviewer* rather than a reader opening the file cold; the tell is a comment that spends its length on the option **not** taken.

A `UserPromptSubmit` hook (`scripts/writing-standards-hook.sh`, wired in `.claude/settings.json`) restates this every turn. Keep its short form in sync with this section.

## Code ported from kubetail

Both projects are Apache 2.0; kubetail's files are copyright "The Kubetail Authors". A file carrying kubetail's code, whole or reshaped, opens with both lines, kstack's first, over the one license block (`// Copyright 2026 The Kstack Authors`, then `// Copyright 2024 The Kubetail Authors`). A file written fresh carries kstack's line alone. There is no `NOTICE` file. Ported so far: the log viewer (`src/lib/log-viewer/`, `components/widgets/log-viewer/`) and `services/logs/kubelet`'s reader (`read.go`).

## Testing conventions (all three languages)

Apply to `src/`, `sidecar/` and `src-tauri/`; each area's `CLAUDE.md` adds its tooling. → [ADR: test conventions](docs/adr/2026-10-17-tests-split-by-platform-take-seams-as-parameters-and-never-sleep.md).

- **While working, run `make test-changed`** (`scripts/test-changed.sh`). Don't run `make test`, `make cover-js` or `make cover-go` locally unless asked: CI runs every suite and both coverage gates. Raise a coverage threshold when CI reports the number rose.
- **Every test file sits beside the file it covers** (`foo_test.go` / `foo.test.ts`). A test file with no counterpart means the unit is buried in a larger file — split the *implementation* out to match. Exception: a fixtures-only file (`testutil_test.go`).
- **In Go, a platform-specific test goes in a platform-named file** (`foo_unix_test.go`, `//go:build !windows`), **never behind a `runtime.GOOS` check**. Split a partly platform-specific test rather than skip it (`atomicjson`'s `TestSaveCreatesMissingDirs` / `TestSaveIsOwnerOnly`). Unix paths (`/tmp`) and rename-over-open-file belong to the Unix half.
- **A test closes what it opens before it ends.** Windows removes no open file or directory, so a leaked handle fails `t.TempDir`'s cleanup there alone. Close it in a `t.Cleanup` registered after the `TempDir`; wait out a stray goroutine through the unit's test seam (`write`'s `TestWriteSetsTheStamp`).
- **No magic sleeps.**
  - **TypeScript** — `vi.useFakeTimers()` + `advanceTimersByTimeAsync`, `await waitFor(...)`, or a `flush()`/`act` helper.
  - **Go** — wait on a channel: `testutil.Probe`/`Signal`, `testutil.Recv`/`Wait`, or `require.Eventually`. Never `time.Sleep`.
  - **Rust** — `#[tokio::test(start_paused = true)]`; reference: `connect_retries_until_endpoint_appears` in `src-tauri/src/services/sidecar/ipc.rs`.
- **Pace a timing-dependent unit by parameter, not constant** — production passes the constant (`appdb`'s `runJanitor`).
- **A test seam is a parameter or a field, never a swapped package variable.** In Go, `agent/chat`, `kubestore` and `kubeconn` call `t.Parallel()` in every test; one that must touch process state (`t.Setenv`, `syscall.Umask`) stays serial and says why.

Two bounded waits are allowed, and each must say so in a comment:

- **A negative assertion** — a `select` on the tripwire channel versus `time.After`, the window a multiple of the (shrunk) cadence. Sample the baseline off the same channel, never by polling a counter.
- **Latency injected into the code under test** — a fake that pauses on purpose to make a race determinate. The test's own assertion path stays immediate.

## Commits, pull requests and issues

- **Commits are terse conventional commits**: `type(scope): subject` — `feat`/`fix`/`perf`/`refactor`/`test`/`docs`/`chore`, scope only when it adds information (`sqlite`, `edges`, `watch`), `!` for breaking. Subject imperative, lower-case, no period, ≤72 chars, says what the change does (`feat(edges): enqueue an edge's source when the declaration commits`). No body unless the why isn't obvious — then 1–3 plain sentences, no bullets; longer rationale is an ADR the body links.
- **Pull requests follow [`.github/pull_request_template.md`](.github/pull_request_template.md)**: keep `Summary` (why), `Key Changes` (what), `Checklist`. The title is a natural, capitalized title (not a conventional-commit heading) led by the change type's emoji — 🎣 bug fix, 🐋 new feature, 📜 documentation, ✨ general improvement.
- **A spec never reaches `main`.** Propose it as a PR labelled `spec`, titled `📜 Spec: …`, from [`.github/PULL_REQUEST_TEMPLATE/spec.md`](.github/PULL_REQUEST_TEMPLATE/spec.md) (`gh pr create --template spec.md`). Accepted, it merges into `wip/<topic>`; implementation PRs target that branch; a PR from it lands the work on `main` and deletes the spec. → [`docs/specs/README.md`](docs/specs/README.md), [ADR: a spec never reaches main](docs/adr/2026-10-03-a-spec-never-reaches-main.md).
- **An issue describing work to be done follows [`.github/ISSUE_TEMPLATE/task.md`](.github/ISSUE_TEMPLATE/task.md)**: keep `Summary` and `Done when`. Title imperative, capitalized, no emoji. A bug not yet understood uses `bug_report.md`; a request with no plan, `feature_request.md`.

## Security invariants

The whole picture is [`docs/security-model.md`](docs/security-model.md); each area's guide lists what an ordinary change there can break. Three hold everywhere:

- **Anything running in the webview holds the full cluster surface, and the shell behind it.** `graphql_query` forwards operations unexamined, so executing code can call every mutation, `approvalDecide` included. The CSP in `src-tauri/tauri.conf.json` is the containment.
- **The approval request is the user's eye, and the eye is the whole gate.** Approve is a click on the drawn request and nothing else. Adding an approval path, a grant path, or a way for the model to make a grant is a security change, recorded under `docs/security/`.
- **Cluster data, model output and command output are attacker-controlled text.** No HTML sink, control sequences stripped, every untrusted string drawn through `VisibleText`; the sidecar redacts Secret data at write time and never logs request data.
