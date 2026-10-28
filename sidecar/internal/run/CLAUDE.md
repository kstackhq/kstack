# internal/run — what confines one run

Five leaves that know no tool and no record: `loginshell` (the user's shell, read once per launch),
`session` (one run's policy), `sandbox` (the machine's sandbox), `permissions` (the decision over a
cluster action) and `kubeproxy` (the proxy a sandboxed command reaches its cluster through). A tool
imports these; none imports a tool. Posture and the tests that pin it:
[`docs/security-model.md`](../../../docs/security-model.md).

## The login shell (`loginshell`)

`app.New` runs the user's login shell once per launch and on Refresh PATH, **in the sandbox**
(macOS and Linux; never on Windows). `Resolve` answers `Path` (for the settings sync), `Env` (the
allowlisted environment, set process-wide on macOS, so `services/auth`'s browser opener resolves
`open` against it too) and `Keys` (provider keys, never set on the
process). The shell is the account record's (`accountShell`), never `$SHELL`. The environment is
scrubbed and `PATH` starts at `DefaultPath`. **`loginshell.In`** (`sandboxed.go`) is the one place
its sandbox policy is built; a failed start is a `Fault`, so it never runs unconfined where a
sandbox exists. → [ADR: the login shell runs in the
sandbox](../../../docs/adr/2026-10-04-the-login-shell-runs-in-the-sandbox.md), [security
record](../../../docs/security/2026-10-04-the-login-shell-in-the-sandbox.md).

**The allowlist (`imported`) is the security boundary**; adding a row is a security change, since
credential plugins inherit it. No wildcards, no secrets (`TestImportedIsExactlyTheAllowlist`).
Nothing from a kubeconfig, cluster data or the socket may reach the shell's command. → [ADR:
shell-environment allowlist](../../../docs/adr/2026-09-07-shell-environment-allowlist.md).

Traps (→ [ADR: the login shell is read by
markers](../../../docs/adr/2026-10-17-the-login-shell-is-read-by-markers-in-a-session-of-its-own.md)):
run it in a **session**, not a process group; the marker starts with a letter and the separator
is `\000`; stop at the last marker, never EOF; keep each value byte for byte; follow `printenv`
with `|| true`. Any failure keeps the inherited environment, logs one fixed reason and never a
value, and surfaces as `sandboxPathFault`.

## The session (`session`)

**A `session.Session` is one agent run's policy** (`internal/run/session`, a leaf a proxy
imports, never the reverse): `Kind`, `Outside`, and live readers `Policy`, `Network` (a failed
read is `NoNetwork`) and `Folders` (read only through `GrantedFolders`). Only the monitor sets
`NoPrompts` and `NoSecretData`. **`session.Narrow`** builds a subagent's from its parent's: identity
copied at spawn, each live field handed on or tightened, never widened. A new field is classed by
that rule and gets its line in `TestNarrowKeepsTheParentsIdentity`.

## The sandbox (`sandbox`)

**`internal/run/sandbox` is the machine's sandbox.** →
[Bash runs in a sandbox](../../../docs/security/2026-09-28-bash-runs-in-a-sandbox.md).

- **`Probe(ctx)`** answers a `*Sandbox` (nil for none) and a `Status`, or `ctx`'s error: a probe
  cut short is no verdict.
- **`(*Sandbox).Command(ctx, Run)`** builds the unstarted process for a `Run`. **It fails rather than narrows or widens**: a run failing `Run.check`
  (its `Policy.Check`, or an `Env` entry `Unpassable` matches) or one its platform cannot enforce
  is an error the model reads.
- **Windows has none**: `Probe` answers none, `Command` errors →
  [ADR](../../../docs/adr/2026-09-28-native-windows-has-no-sandbox.md).

**A `Policy` is everything enforced for a run** (`policy.go`):

- **`Files`**: `Read`, `Write`, `Deny`, each covering a subtree. **The deepest rule wins; a tie
  goes to the narrower** (Deny, Read, Write). **Nothing may sit beneath a Write rule** (a run can
  rename what it writes). An uncovered path is off limits.
- **`Always`**: `Deny` (denied-always) and `Kstack` (Kstack's directories, where only the run's
  own paths open). **No Files rule opens an Always path**, so a Read of `~` never exposes `~/.ssh`.
- **`Network`**: at most one `Relay` (loopback port → Unix socket outside), `Internet`, and a
  `Resolver` (Linux only). Zero value is no network.
- **`Limits`**: `CPUSeconds` (hard limit `cpuGrace` above), `MemoryBytes` (Linux only),
  `OpenFiles`, `Processes`, counted over `Sandbox.CountedProcesses()` plus `forwarderTasks` →
  [ADR: process limits are set inside the run](../../../docs/adr/2026-10-02-process-limits-are-set-inside-the-run.md).

**`Check`** refuses, among others, anything beneath a Write, a run's own path that is a link, and
a memory or process limit with no open-files limit (`sandbox-shell` cannot restore the one Go
raised). Paths compare resolved (`paths.go`), a missing one through its
deepest existing folder. Both platforms compile one merged list (`Policy.rules`), last match
winning, the run's own paths last.

**`neverEnv` (`env.go`) is the one list of variables that may never pass** (agent sockets,
`DOCKER_HOST`, tokens, loader variables, shell startup hooks, every `AWS_`) →
[the sandbox's own environment](../../../docs/security/2026-10-02-the-sandboxs-own-environment.md).

**The zones are Kstack's lists** (`lists.go`, `lists_linux.go`, `lists_darwin.go`): `System`,
`Toolchain`, `Never`, `Closed` and `NoWrite` (the home, which a read grant may name and a
read-write one may not). **Adding a location is a security change**: every
run reads it. The zones open no `PATH` entry. `Sandbox.System` never reads a broad folder (`/`,
the home, an app-data folder) through a link or the shell's location. `FixedMount(p)` is what no
grant may name. `System` and `Never` can hang on a network mount, so both run under a bound; a
test that cuts a probe short waits on `policyBuilds` before swapping the lists. →
[ADR: the sandbox's zones are Kstack's lists](../../../docs/adr/2026-10-02-the-sandboxs-zones-are-kstacks-lists.md),
[path grants](../../../docs/security/2026-10-04-path-grants.md).

**`search.go`'s `SearchFolder` is the one rule for a folder a run's `PATH` may search**; the
settings filter and the per-run check both call it. `FilePolicy.WithSearch` adds the Read rules.

**On Linux it is bubblewrap** (`sandbox_linux.go`): the system's bwrap first, then Kstack's own
beside the executable, never off `PATH` →
[ADR: Kstack ships its own bwrap](../../../docs/adr/2026-09-28-kstack-ships-its-own-bwrap.md). Each
probe runs in its own process group, killed whole at the bound (`runProbe`). `Command` refuses a
rule that would replace a fixed mount (`/tmp`, `/dev`, `/proc`). Mount order matters; see
`mounter`. `/run` and `/var` are never mounted. **A run with `Internet` starts under `pasta`**
(`network_linux.go`) as the user holding no capability; the probe reads no route, so Kstack
started offline still offers network → [ADR: Kstack ships its own pasta](../../../docs/adr/2026-10-04-kstack-ships-its-own-pasta.md).

**On macOS it is Seatbelt** (`sandbox_darwin.go`, `profile_darwin.sb`). A probe that times out
keeps the sandbox. `Port` is a free loopback port →
[ADR](../../../docs/adr/2026-09-28-a-sandboxed-forwarder-holds-a-loopback-port-on-macos.md). **Every
path is a `-D` parameter, never profile text** (a path can hold `"` or `)`), and resolved first.
**`Command` starts the run in its own session (`Setsid`); a caller replacing `SysProcAttr` must
keep it** →
[ADR](../../../docs/adr/2026-10-13-a-macos-run-leads-its-own-session-so-no-run-reads-another-processs-launch-strings.md).
The profile refuses `setsid`/`setpgid` →
[ADR](../../../docs/adr/2026-09-28-a-macos-run-keeps-its-group-by-refusing-setsid.md). Fixed file rules
come after the merged rules so no policy undoes them; `/dev/tty` is never readable. A run with
`Internet` has IPv4 alone →
[record](../../../docs/security/2026-10-04-a-macos-run-with-the-internet-has-ipv4-only.md).
`refusedServices` lists what no profile names.

**`sandbox-shell` sets the limits and confines the shell** (`sandbox.ShellMain`), between the
forwarder and the shell, so limits never hold the forwarder. On Linux it installs the seccomp
filter (`seccomp_linux.go`, classic BPF, no cgo) on every thread with `no_new_privs`, then sets
limits, clamped to existing hard limits. **Under `--memory` or `--processes` it execs through a
raw `execve` with allocation done first** (`execLimited`): an allocation past `RLIMIT_AS` crashes
the runtime.

**The forwarder is `kstack-sidecar sandbox-init`** (`forward.go`; on Linux a run with the internet
starts as `sandbox-pasta`, `sandbox.PastaMain` in `pasta_linux.go`, which keeps pasta's own output
out of the run's and hands the run's stderr to `sandbox-init` on stdin, `--stderr-on-stdin`), reached through `sandbox.Main`
before `main` reads a flag; `ForwarderArgs` is the one writer of its command line. It relays its
one port to the socket, reaps as PID 1, and checks nothing (every check is the proxy's). **It
catches and drops `SIGTERM`/`SIGINT`/`SIGHUP` with `signal.Notify`, never `signal.Ignore`**: an
ignored signal stays ignored across `exec`. Its own failures, and `sandbox-shell`'s, exit 125 with
one `sandbox-init:`/`sandbox-shell:` line; the line is what tells them from a command's 125.

**Every run ends with the sidecar, however it dies.** `Probe` makes one pipe, the lifeline;
the `Sandbox` holds its write end as a bare descriptor (no finalizer closes it, only the process's
end) and never writes to it. Each run inherits the read
end on fd 3 (`ExtraFiles`), and `--lifeline 3` tells the forwarder, which marks it close-on-exec
and, at EOF, kills its process group and exits (`watchLifeline`); on macOS a process that left the
group through `posix_spawn` survives (security model). On Linux a run with the internet hands it
to `sandbox-pasta` instead, since pasta closes every descriptor it did not open; at EOF it kills
its group, pasta and the child pasta makes during setup with it; `Command` starts every run in a
session of its own on both platforms, so that kill reaches nothing of the sidecar's. A failed or
cancelled probe closes the pipe; `Close` closes a probed `Sandbox`'s, ending its runs, once, and
`Command` refuses after it. **A test that probes registers it as cleanup** (`closing`,
`confining`).

**Tests.** A test binary that starts the chain calls `sandbox.Main` from its `TestMain`, or it
runs its own suite inside the probe. A test needing a sandbox calls `testutil.RequireSandbox`
(fails under `KSTACK_REQUIRE_SANDBOX=1`, which CI sets). **No test writes inside the module**
(Go's test cache keys on it); fixtures go under `testutil.GrantableDir`. `app`'s `startE2E` runs
a chat through the real sandbox.

## Permissions (`permissions`)

**`internal/run/permissions` is the decision** (`permissions.go`, `match.go`): six `Class`es, `Mode`s (`ReadOnly`, `Ask`,
`Auto`), `Rule`s, a classified `Action`, and a `Policy` whose `Decide(act)` is `Authorize` then
`Outcome`.

- `Authorize` refuses class 6 first under `NoSecretData`, and turns an ask into `Refuse` last under
  `NoPrompts`. Otherwise the verdict is the strongest match: `Refuse` (a matching `Deny`, or a
  read-only mode on class 3, 4 or 5) > `Forbid` (a matching `AskFor`, or class 5) > `Permit` (class
  1 or 2, a matching `Allow`, or `Auto`) > `Unmatched` (the zero value). Class 4 covers class 5;
  class 6 asks under `ReadOnly` and `Ask` and is `Permit` under `Auto`. A grant's `Duration` is
  `once`, `command`, `chat` or `always`. **The verdict is order-independent**; rule order picks the reason
  alone. `Unmatched` and `Forbid` prompt; a prompt is a denial the user may lift.
- `Match` is the one glob matcher. **Every pattern Kstack writes from a value goes through
  `Literal`.** `ClusterScope` (`[cluster]`) matches cluster-scoped actions alone.
- `Rule.Line` is the rule in the user's words, shown in Settings, reasons and requests; keep its
  quoting so ` / ` outside quotes always separates context from namespace.
- `Grantable`, `GrantRule` and `CommandRule` decide what an answer may write. `Refused` and
  `RefusedSecrets` stand in for rules Kstack could not read (class 4 does not cover class 6).

**`kubeproxy/classify.go` classifies a request** into a class, verb and one-line `Summary`. A
`GET` of core `secrets` is class 6. A dry run is class 1 only on a group version in `honorsDryRun`
(an aggregated API may ignore `dryRun`). Class 5 is the destructive list (`Destructive` in words),
including any replica write `keepsReplicas` cannot read as plain. A write to a core Namespace is in
that namespace's scope. →
[ADR: permissions are classes, modes and rules](../../../docs/adr/2026-10-02-permissions-are-classes-modes-and-rules-decided-at-the-proxy.md),
[ADR: a prompt is a denial the user may lift](../../../docs/adr/2026-10-03-authorization-is-binary-and-a-prompt-is-a-denial-the-user-may-lift.md),
[the permissions engine](../../../docs/security/2026-10-02-the-permissions-engine.md),
[Secret data is a permissioned read](../../../docs/security/2026-10-05-secret-data-is-a-permissioned-read.md).

## The cluster proxy (`kubeproxy`)

**`internal/run/kubeproxy` is the cluster proxy**; it imports nothing of `tools` or
`services/cluster`. Files: `kubeproxy.go` (grant, handler), `policy.go` (path parse, read
policy), `write.go` (write path), `classify.go`, `diff.go` (preview), `secretread.go`, `redact.go`
and `redact_helm.go` (Secret rewriter), `status.go` (every refusal's `Status`), `server.go`.
→ [ADR: the proxy reads a request as the API server will](../../../docs/adr/2026-10-17-the-cluster-proxy-reads-a-request-as-the-api-server-does.md).

- **A `Grant`** (`NewGrant`) holds the run's session, the kube-context its writes are decided in, a
  256-bit token, an `Upstream` (an `Endpoint` per request, whose `Done` cancels requests still open,
  watches included), a `rate.Limiter`, a cap on open requests, and an `Asker` (or a refusal every
  write answers). `End` makes every later request 401 and cancels those in flight; `Wait` joins
  every handler it let in.
- **The token is proxy credentials** (`Proxy-Authorization` via the run's `proxy-url`); the handler
  deletes it and `Authorization` before forwarding, so the connection's transport adds the user's.
- **The handler, in order:** `Host` must be `cluster.kstack.invalid` and the credentials match
  (constant-time), else 401; the policy, else 403; a write goes to the write path; a body over
  64 KiB is 413; no slot is 429 with no `Retry-After`; the limiter; `Endpoint` (each sentinel a 503
  with its own message, never the error's text); then a per-request `httputil.ReverseProxy` with
  `FlushInterval: -1`, an upstream failure a 502.
- **The policy** refuses a non-canonical path first, then parses as `RequestInfoFactory` does. `GET`
  of resources, discovery, `/version`, `/openapi/…` and health paths pass. `POST`/`PUT`/`PATCH`/
  `DELETE` of a resource go to the write path, except a `POST` of a self review, which passes.
  Refused: `exec`, `attach`, `portforward`, every `proxy` path, `Upgrade`/`Connection: upgrade`/
  `Impersonate-*` headers, a ServiceAccount's `token` subresource (its answer is a credential), and
  anything else.
- **The write path** (`write.go`) refuses, before any queue, a grant with no asker and a query
  `url.ParseQuery` refuses. It then takes the write lock (a one-slot semaphore, at most
  `maxQueuedWrites` waiting, one more a 429) and holds it until the forward returns, so writes reach
  the cluster in the order decided; reads the body under `maxWriteBody` (1 MiB); runs **the helm
  gate** (refuses a helm release write when `redactedReads` holds its namespace or `""`, since helm
  rebuilds a release from the Secrets it read — ahead of `checkBody`, so the refusal says why); then
  `checkBody` (encoding, UTF-8, media type, `decodeBody`, `uniqueKeys`, any `[redacted]` mark or its
  base64, and `checkRelease` inside a release). Then it classifies the request into a `Write` and
  takes one verdict: `Authorize` over the session's `Policy` for the grant's context plus the
  grant's `commandRules` (no `Policy` is read-only).
- **Verdicts.** `Allowed` is recorded through `Asker.Record`, then forwarded; a failed record
  forwards nothing. `Denied` is recorded and answered 403. `Prompted` is previewed, then `Ask`ed
  with a `Request` (action, `Grantable`, the rule lines each allow answer adds, the `Write`, the
  diff). Only an approval forwards, taking a slot and the limiter then, so a write waiting on the
  user holds neither. A `Command` approval adds `CommandRule(act)` to the grant for the rest of the
  command. → [cluster writes ask](../../../docs/security/2026-09-29-cluster-writes-ask.md),
  [the permissions engine](../../../docs/security/2026-10-02-the-permissions-engine.md),
  [ADR: a sandboxed command asks for each cluster write](../../../docs/adr/2026-09-29-a-sandboxed-command-asks-for-each-cluster-write.md),
  [ADR: permissions decided at the proxy](../../../docs/adr/2026-10-02-permissions-are-classes-modes-and-rules-decided-at-the-proxy.md).
- **The preview** (`diff.go`) runs only for a write that asks, a `PUT` or `PATCH` of a named object
  on a group version in `honorsDryRun`: a `GET`, then the request with `dryRun=All`, bounded by
  `diffTimeout` and `maxDiffObject`. Both answers drop `managedFields`, `resourceVersion` and
  `generation`; Secret values and every last-applied annotation are compared, then redacted, before
  the diff. A failure is a `DiffError` in the user's words.
- **The Secret read** (`secretread.go`): a read of core `secrets` that is not `metadataOnly` is
  decided like a write, under the same lock (`takeWriteLock`, `decideSecretRead`), and only
  `Allowed` or an approval shows the data. A read that passes redacted marks its namespace in
  `redactedReads`, which the helm gate reads. The lock is released before the forward, so an allowed
  watch never holds it. `forward` takes `redact`: only an allowed Secret read passes unredacted.
  → [Secret data is a permissioned read](../../../docs/security/2026-10-05-secret-data-is-a-permissioned-read.md).
- **A redacted `secrets` request is rewritten both ways** (`rewriteSecrets`): `Accept` narrowed to
  JSON and `Accept-Encoding` dropped; any other response type or encoding is a 502. The body is
  rewritten as a stream (`json.Decoder`, `UseNumber`), `items`/`rows` one element at a time.
  `redact` replaces each `data`/`stringData` value and every `last-applied-configuration`.
  **A helm release is redacted inside** (`redact_helm.go`). **A failure fails closed**: a 502
  before the first piece, a cut body after (a watch sends its status line at once).
  → [ADR: helm release Secrets pass redacted inside](../../../docs/adr/2026-09-28-helm-release-secrets-pass-redacted-inside.md).
- **The server** sets no read or write timeout, which would cut a watch.
