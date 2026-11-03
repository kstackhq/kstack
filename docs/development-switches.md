# Development switches

Environment variables that change how a dev run behaves. Each one is off unless set. **Add a row here when you add a switch.**

## Webview

Read by Vite, which bakes them into the bundle at build time. Set them in the shell that runs `pnpm tauri dev`, or in `.env.local` (git-ignored).

| Variable | Values | Effect |
| --- | --- | --- |
| `VITE_ROUTER_DEVTOOLS` | `true` | Mounts TanStack Router's devtools (`src/routes/__root.tsx`). `.env.development` sets it to `false`. |
| `VITE_READY_GATE_HOLD` | `pending`, `error` | Keeps `ReadyGate` on screen: `pending` never settles, `error` shows *Failed to start*, and Retry fails again (`src/lib/ready-gate.tsx`). A production build ignores it. |

## Host and sidecar

| Variable | Read by | Effect |
| --- | --- | --- |
| `KSTACK_LOG_LEVEL` | host, sidecar | Log level, in every build. The host takes an `EnvFilter` directive (`src-tauri/src/logging.rs`); the sidecar takes `debug`, `info`, `warn` or `error`. |
| `NO_COLOR` | host | Turns off colour in terminal logs, which a debug build otherwise always uses. |

## Debug sidecar only

These work only in a sidecar built with `-tags debug`, which `tauri dev` and `make sidecar-dev` build. A release build ignores them (`applyEnvOverrides` in `sidecar/config.go`).

| Variable | Effect |
| --- | --- |
| `KSTACK_OAUTH_ISSUER`, `KSTACK_OAUTH_CLIENT_ID` | Point sign-in at another OAuth server. |
| `KSTACK_DATA_DIR`, `KSTACK_CACHE_DIR`, `KSTACK_RUNTIME_DIR` | Replace the directories the host passes. |
| `KSTACK_KEYCHAIN_SERVICE` | Use another keychain entry. |
| `KSTACK_<PROVIDER>_BASE_URL` | Move a model provider's base URL, the provider id upper-cased with `-` as `_` (`baseURLVar` in `sidecar/internal/agent/catalog/providers.go`). |
| `KSTACK_FAULTS` | Fail cluster requests inside every connection, so the failure states draw from real data. See [Injected faults](#injected-faults). |

A debug sidecar also adds a fake model to the catalog, so a dev run can chat without an API key.

### Injected faults

`KSTACK_FAULTS` holds rules split by `;`. A rule is `<verbs> <resource>=<fault>`, or `store=fail`. Verbs are a comma-separated list of `get`, `list` and `watch`. The resource is the plural lower-case name in the request path (`secrets`, `deployments`), `*` for every resource, or `api` for the discovery root the connection probe dials. The first matching rule wins. Every rule applies to every cluster.

A matching request never leaves the connection, so everything above it runs for real: the probe ladder, the kind verdicts, the health rollup, and the agent's kubectl. Only a `GET` matches; a subresource (`pods/<name>/log`) and a group or version listing never do. The rules are read at start: change them by restarting.

| Fault | The connection answers | What the app shows |
| --- | --- | --- |
| `401`, `403`, `404`, `429`, `500`, `503` | That status, with a Kubernetes `Status` body | `Unauthorized`, `Forbidden`, … on the attempts or the kind's events |
| `refused` | A refused dial | `Unreachable` |
| `tls` | An unknown certificate authority | `TLSInvalid` |
| `timeout` | Nothing until the request's context ends; `get` and `list` only | `Timeout` on a probe; *server stopped sending data* on a list, after two to four minutes |
| `quiet` | Per collection, a first watch that sends nothing and a reopen that hangs; `watch` only | *Resuming*, after ten minutes |
| `stalled` | A watch that sends one bookmark, then nothing; `watch` only | *Stale*, after three minutes, until the reopen two minutes later |
| `store=fail` | Every cache's store refuses to open | *Storage error* on the cache |

A rule the sidecar cannot read stops its start, quoting the rule.

```sh
KSTACK_FAULTS='list,watch secrets=403' pnpm tauri dev  # a kind refused for its permissions
KSTACK_FAULTS='get api=refused' pnpm tauri dev         # an unreachable server
KSTACK_FAULTS='watch pods=quiet' pnpm tauri dev        # a reopen that hangs
KSTACK_FAULTS='watch pods=stalled' pnpm tauri dev      # a stale watch
```

## Build and test

| Variable | Effect |
| --- | --- |
| `KSTACK_HOST_TRIPLE` | Names the Rust host triple for `scripts/build-sidecar.go`, so a build can run without Rust installed. CI sets it. |
| `SIDECAR_VERSION` | Stamped into the sidecar by `scripts/build-sidecar.go` (`internal/lib/version`). |
| `KSTACK_REQUIRE_SANDBOX` | `1` fails a Go test that needs a sandbox the machine lacks, instead of skipping it. CI sets it. |
