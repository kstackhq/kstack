---
title: Inject request failures into a debug sidecar's cluster connections
scope: sidecar
status: Planned
---

# Inject request failures into a debug sidecar's cluster connections

**Answers:** #661.

Go paths below are under `sidecar/internal/services/cluster/internal/` unless they say
otherwise; the cluster service itself is the directory above.

## In short

The cluster status popup, the cluster picker and the Clusters dialog draw a dozen failure
states: a refused or unauthorized connection with its attempts, countdown and downtime; a kind
refused for its permissions; a stale watch; a cache file that will not open. Seeing one today
means breaking a real cluster.

After this change, **a debug sidecar reads `KSTACK_FAULTS` and fails the requests it names,
inside the connection, so every layer above them runs for real**: the probe ladder, the attempt
count, the classification, the connection history, the kind verdicts and the health rollup.

```sh
KSTACK_FAULTS='list,watch secrets=403; get api=refused' pnpm tauri dev
```

A release binary carries none of it. There is no panel: a fault is set for a run and changed
by a restart.

## What is not in this change

- **No change while the app runs.** A debug-only mutation would widen what the webview can
  call, and the webview already holds the full cluster surface. A restart costs seconds.
- **No webview switch.** A switch that replaces the health value fakes the last link in the
  chain and runs nothing behind it. The one webview switch that stays is `VITE_READY_GATE_HOLD`,
  which holds a state the sidecar cannot produce.
- **No watch that drops mid-stream, and no partial discovery.** Add a fault when someone needs
  it.
- **No shorter stale window.** `watch pods=quiet` turns pods *Stale* after two of kubesync's
  `staleAfter` windows, ten minutes: one silent window proves the first watch alive, and the
  reopen that follows must hang a second. The wait is the behaviour under test.
- **No fault on the kubeconfig.** A context that will not resolve is made by editing the file.

## Design

### 1. The rules

One variable, `KSTACK_FAULTS`, holds rules split by `;`. A rule is `<verbs> <resource>=<fault>`
or `store=fail`. Verbs are a comma-separated list of `get`, `list` and `watch`. The resource is
the plural lower-case name as it appears in the request path (`secrets`, `deployments`), `*` for
every resource, or `api` for the discovery root `/api` the connection probe dials. Whitespace
around a rule, a verb or `=` is ignored.

**A rule applies to every cluster.** There is no cluster selector: the rules are read once and
handed to every connection the pool builds, so `get api=refused` takes every cluster down and
`store=fail` fails every cache. A developer who wants one healthy cluster beside a broken one
runs two sidecars, or picks a rule only one cluster's kinds match.

| Fault | What the connection answers | What the webview draws |
| --- | --- | --- |
| `401`, `403`, `404`, `429`, `500`, `503` | A response with that code whose body is a Kubernetes `Status` of the same code | `Unauthorized`, `Forbidden`, … on the connection's attempts or the kind's events |
| `refused` | The dial's error, a refused connection | `Unreachable` |
| `tls` | The handshake's error, an unknown certificate authority | `TLSInvalid` |
| `timeout` | Nothing until the request's context ends, then its error; `get` and `list` only | `Timeout` on a probe; *server stopped sending data* on a cold list |
| `quiet` | Per watched collection: the first open answers `200` and sends nothing, and the reopen after it hangs; `watch` only | *Stale* after two stale windows |
| `store=fail` | The cache's store will not open | *Storage error* on the cache |

`quiet` on `get` or `list`, and `timeout` on `watch`, are parse errors. A list that never ends
is not a state the webview draws, and a watch has no watchdog, so a hanging first open would
hold a kind's start slot until the run is cancelled. The two faults together cover every hang
that has a verdict.

The parser is strict. An unknown verb, fault or shape fails the sidecar's start with the rule
quoted, so a mistyped rule never passes as a healthy cluster. `run` prints the reason as
`FATAL`, which the host shows on the start screen.

### 2. Where the rules are read

`applyEnvOverrides` in `sidecar/config.go` reads `KSTACK_FAULTS` into a new `Faults string` on
`app.Config`, beside the other debug-only fields. It is the one place the environment reaches
the config only in a debug build, and its `debugBuild` guard is what keeps a release build
from reading the variable.

`build` passes `cfg.Faults` to `cluster.New` as `cluster.WithFaults(spec)`, the one option `New`
takes. `New` parses it through `kubeconn.ParseFaults` and fails with `KSTACK_FAULTS: <reason>` on
an error.

### 3. The transport fault, in `kubeconn`

`newConnection` builds every client from one `rest.Config`, with the counting dialer set as its
`Dial` and the idle-timeout wrapper on its transport. A fault wrapper goes on the same config
through `own.Wrap`, **inside the idle-timeout wrapper**: it is installed first, since each later
`Wrap` is the outer one. A faulted request never reaches the transport, and the watchdog still
bounds it, so `list pods=timeout` ends as a stalled cold list does, with `ErrIdleTimeout` after
`listIdleTimeout`, and never holds a start slot past it. The wrapper covers everything built
from the config: the probe's raw requests on `HTTPClient`, every request `Dynamic` makes
(discovery, each kind's cold list and its watch), and the agent's kubectl, whose proxy serves
the same `HTTPClient` (`internal/tools/bash/upstream.go`). A `kubectl get secrets` under
`list secrets=403` is refused as the sync is, which is what a developer checking the tool's
error path wants.

**The service holds the rules.** `kubeconn.New` and `NewConnection` take variadic `Option`s;
`WithFaults(f Faults)` is the one, and the pool hands its options to every connection its probe
builds. `Faults` is a struct in both builds, and its zero value is no faults. A caller with no
rules passes nothing, so a fake pool in another package's tests and `clustertest`'s `Connection`
helper are untouched.

**Matching reads the request as the API server does.** Only a `GET` can match; every other
method passes through, so a write never reads as a list. The wrapper takes the request path,
drops the connection's `BaseURL` path prefix (a kubeconfig may name one), and reads what is left:

1. A path of exactly `/api` is the discovery root, which the rule name `api` matches with verb
   `get`. `/apis` and the group and version listings under either match nothing.
2. The versioned prefix is `/api/<version>/` or `/apis/<group>/<version>/`. The segments after
   it are the request.
3. If the first segment is `namespaces` and a third segment follows, the second is the
   namespace, and the request starts at the third segment, unless the third is `status` or
   `finalize` and nothing follows it: that is a namespace's own subresource, and matches
   nothing. A namespace itself is reached as `namespaces/<name>`, two segments, which the rule
   below reads as a `get` on `namespaces`.
4. The first segment of the request is the resource. A second segment is the object's name.
   A third segment is a subresource (`pods/<name>/log`), which matches nothing.

The verb is `watch` when the query has `watch=true`; otherwise `get` when a name is present
and `list` when it is not. A rule matches when its verbs hold the verb and its resource is the
request's or `*`. The first matching rule wins, in the order written.

| Request | Verb | Resource |
| --- | --- | --- |
| `GET /api` | `get` | `api` |
| `GET /api/v1/namespaces/kube-system` | `get` | `namespaces` |
| `GET /api/v1/namespaces` | `list` | `namespaces` |
| `GET /api/v1/namespaces/default/secrets` | `list` | `secrets` |
| `GET /api/v1/secrets?watch=true&resourceVersion=…` | `watch` | `secrets` |
| `GET /apis/apps/v1/namespaces/default/deployments/web` | `get` | `deployments` |
| `GET /api/v1/namespaces/default/pods/web/log?follow=true` | — | none: a subresource |
| `GET /api/v1/namespaces/default/status` | — | none: a namespace's subresource |
| `GET /api/v1/namespaces/default/finalize` | — | none: a namespace's subresource |
| `GET /apis/apps/v1` | — | none: a version listing |
| `POST /api/v1/namespaces/default/pods` | — | none: not a `GET` |

**Each fault answers as the transport would**, so `classify` names an injected fault exactly as
it names a real one:

- A status code is an `*http.Response` with that code, `Content-Type: application/json`, and a
  `metav1.Status` body carrying the code and the HTTP reason. The probe reads it as an
  `httpErr`; client-go reads it as a `StatusError`, which is what a kind's sync reports.
- `refused` is a `*net.OpError` with `Op: "dial"` wrapping `syscall.ECONNREFUSED`.
- `tls` is an `x509.UnknownAuthorityError`.
- `timeout` blocks on `req.Context().Done()` and returns the context's error. A probe's own
  deadline ends it, and `classify` reads `context.DeadlineExceeded` as it would from a silent
  server; a cold list's watchdog ends it, and the run reads `ErrIdleTimeout` as it would from a
  server that stopped mid-page.
- `quiet` plays one sequence per watched collection, and that sequence is what kubesync reads
  as *Stale*: a silent first window proves the watch alive and ends in a reopen from the
  cookie, and a reopen whose open hangs past a second window is what `openWatch` announces as
  `ReasonStale`. The wrapper cannot see a worker, but it can see the body it handed out and the
  context of the open that follows, which is enough. It keeps one state per **collection path
  on this connection** (the request path without its query, so `events` in the core group and
  in `events.k8s.io` are two), held on the wrapper, which lives and dies with the
  `Connection`:

  | State | A matching open | Leaves the state when |
  | --- | --- | --- |
  | Idle | Answers `200` with a silent body and starts a sequence; state becomes Open | — |
  | Open | Answers `200` with a silent body outside the sequence: closing it changes nothing. kubesync opens one watch per kind at a time, so this open is another client's, such as a `kubectl get pods -w` on the collection kubesync watches | The sequence's body is closed: state becomes Reopening |
  | Reopening | Hangs until its context ends | — |

  No open hangs outside Reopening, so a watch kubesync opens before `pass.Ready` returns at
  once whatever else is watching the collection. If kubectl's watch is the one that started
  the sequence, kubesync's first open is the extra silent body, and its reopen after a window
  is the extra as well, so the kind reads *Watching* until kubectl exits; the sequence restarts
  from kubesync's next open.

  **A sequence ends when the first open's context ends, and every open checks that first.**
  The state holds the context of the open that started the sequence. Under the state lock,
  before deciding to answer silently or hang, an incoming open reads that context's `Err`: if
  it is set, the open retires the sequence and starts a fresh one, synchronously, so a
  replacement worker never sees a Reopening left by a worker that is gone. A stopped worker
  ends that context whether its watch is still open, its body already closed, or its reopen
  parked in the store's cookie read and not yet at the wrapper, so every stop retires. In
  kubesync the reopen runs on the same worker context, so the same cancel also releases a hang.

  The wrapper also registers a reset on that context with `context.AfterFunc`, so a collection
  nothing opens again does not hold its state for the connection's life. That callback runs
  asynchronously and decides nothing: the synchronous check above is what an open relies on.

  Each sequence carries a number, taken when it starts. The asynchronous reset and the
  body-close transition apply only when the collection's state still carries that number, so
  a callback that runs late, after a fresh sequence has started on the collection, leaves the
  new one alone. The wrapper takes its `AfterFunc` as a field, `context.AfterFunc` in
  production, so a test can hold the callback.

  The silent `200` carries `Content-Type: application/json`, as the status responses do. Its
  body's `Read` blocks until `Close` or the context ends, then returns `io.EOF`; `Close` must
  unblock `Read`, because kubesync stops a watcher on rotation without cancelling the worker's
  context. The hang blocks as `timeout` does.

  No injected response carries `Retry-After`, so client-go never retries an injected `429` or
  `503` on its own, and the code reaches the verdict unchanged.

  What this scoping gives: two kinds under `watch *=quiet`, or one kind on two clusters, each
  run the sequence on their own, and every kind's first open returns at once, so no open holds
  a start slot. A worker cancelled while its reopen hangs, by a forget, a stop, or the cache's
  teardown, ends the hang's context, and the state returns to Idle, so the restarted worker's
  first open is silent again and reads *Resuming* for no longer than a real one. A rebuilt
  connection carries a fresh wrapper and starts from Idle, as a reconnected cluster would.

**Build tags keep it out of a release.** The parser, the `Faults` type's rules and the wrapper
live in `faults_debug.go` (`//go:build debug`). `faults_release.go` (`//go:build !debug`) holds
the stubs: `ParseFaults` returns the zero `Faults` for an empty string and an error for
anything else, and the zero `Faults` has no wrapper, so the config gains no layer. In the debug
build `Faults` holds the parsed rules; in both, the zero value is no faults. A release
build cannot reach the error, since `applyEnvOverrides` leaves the string empty there; the
stub refuses anyway, so a release-shaped test that sets the field sees it refused rather than
ignored. The debug and release pairs follow `sidecar/debug.go` and `sidecar/release.go`.

### 4. The store fault, in the cluster service

A cache file that will not open fails before any request, so it cannot come from the transport.
kubesync's session opens the store through its `storeManager` interface, `OpenOrCreate`, and a
failed open leaves the cache in `storeFailures`, which the cache controller reads as
`StoreFailed`.

`cluster.New` hands kubesync a store manager that fails every `OpenOrCreate` with
`injected by KSTACK_FAULTS` when the rules hold `store=fail`. The interface has that one
method, so the type is a few lines and embeds nothing. Every other reader of the manager keeps
the real one. The type sits in the cluster package's own `faults_debug.go`, and
`faults_release.go` returns the manager unchanged. `Faults` gains a `StoreFails() bool` for
it, `false` in a release build.

### 5. The document

`docs/development-switches.md` lists `KSTACK_FAULTS` under *Debug sidecar only*, with the
grammar, the fault table and three ready-to-paste examples: a forbidden kind, an unreachable
server, a quiet watch.

## Tests

- **Parser (`kubeconn/faults_debug_test.go`):** every verb and fault parses; `*` and `api`
  parse; a rule with an unknown verb, an unknown fault, no `=`, `quiet` on a non-watch verb or
  `timeout` on `watch` fails naming the rule; whitespace is ignored; an empty string is no
  rules; rules keep their order.
- **Matching:** every row of the table in section 3, each pinned by a request made through
  a real `Connection` against an `httptest` server: a matched request never reaches the
  server, and an unmatched one, a `POST`, a subresource and a version listing all do. A
  connection whose `BaseURL` carries a path prefix matches the same rows. `*` matches every
  resource, and the first of two matching rules wins.
- **Each fault class, through `classify`:** an injected `401` reads `Unauthorized`, `403`
  `Forbidden`, `429` `Throttled`, `500` `InternalError`, `503` `ServiceUnavailable`, and `404`
  `Malformed`, as a raw `404` does today; `refused` reads `Unreachable`; `tls` reads
  `TLSInvalid`; `timeout` reads `Timeout` under a deadline the test sets. The probes keep
  their own reading of a `404`: with `get namespaces=404` the server-UID probe fails
  `NotFound`, as it does when kube-system is absent. No change to `classify`.
- **`timeout` on a cold list:** a `list` with no caller deadline, through the connection's
  watchdog driven by the test's timer, ends with `ErrIdleTimeout`. Nothing waits the
  production bound.
- **`quiet`, on the wrapper:** the first watch opened through `Dynamic` delivers nothing and
  ends when the watcher is stopped, with the context still live; the open after it blocks
  until its context is cancelled; the open after that cancellation is silent again. A second
  open while the first body is still open gets a silent body of its own, and closing it leaves
  the state Open, so closing the first afterwards still moves it to Reopening. A first
  open whose context ends without a stop leaves the next open silent. A first body closed and
  its context then cancelled, with no reopen between, leaves a fresh context's open silent:
  the test closes, cancels, and asserts the `200` with the reset callback held, so the
  synchronous check alone is what passes it. A late reset leaves a newer sequence alone: the
  test cancels the old context at once, holds its callback through the test's `AfterFunc`,
  opens on a fresh context and gets the silent `200`, then runs the callback and asserts the
  next open on that collection hangs rather than answering silently. Two collections under
  `watch *=quiet` each get a silent first open with neither waiting on the other. Two
  connections built from the same `Faults` keep separate state. The test stops and cancels;
  nothing waits a window.
- **The kubesync verdict (`kubesync/kinds_test.go`):** a kind synced over a pool whose
  connection carries `watch pods=quiet`, under test pacing, reads `Watching` through the first
  window, reopens, and reads `Stale` once the reopen outlasts the second. Beside it: two kinds
  under `watch *=quiet` both reach `Watching` at once, pinned by both passing `Ready` before
  either window ends; the same kind on two caches over two connections likewise; and a worker
  forgotten while its reopen hangs, then tracked again, reaches `Watching` from a fresh silent
  open. Pinned there because the sequence is kubesync's, and the fault exists to drive it.
- **The probe ladder:** a connection built with `get api=refused` fails its probe with
  `Unreachable` and retries, as one against a stopped server does. Reuses the existing probe
  tests' harness.
- **Release stubs (`kubeconn/faults_release_test.go`, `//go:build !debug`):** `ParseFaults("")`
  is the zero `Faults`; a non-empty string is refused; the zero value has no wrapper and
  `StoreFails` is `false`.
- **Cluster service:** `New` fails on a bad spec with `KSTACK_FAULTS` in the error; with
  `store=fail`, the cache's health reads `StoreFailed` and the `kubesync` arm logs the injected
  error.
- **Config (`sidecar/config_test.go`):** a debug build reads `KSTACK_FAULTS` into
  `App.Faults`; the release-build test that ignores the environment covers it too.
- **Coverage:** CI runs the suite untagged and with `-tags debug`, so each pair's two files are
  judged by their own build. No marker is needed.

## Done when

- [ ] `KSTACK_FAULTS='list,watch secrets=403' pnpm tauri dev` against a healthy cluster shows
      secrets among *Kinds not syncing* with `Forbidden` in its events, and every other kind
      synced.
- [ ] `KSTACK_FAULTS='get api=refused'` shows the connection alert with a growing attempt
      count, a countdown and downtime, and the attempts list reads `Unreachable`.
- [ ] `KSTACK_FAULTS='get api=timeout'` reads *Retrying…* on *Retry now* for the ten seconds
      each probe takes, then `Timeout` in the attempts list.
- [ ] `KSTACK_FAULTS='watch pods=quiet'` turns pods *Stale* after ten minutes, pods reading
      *Synced* until then.
- [ ] `KSTACK_FAULTS='list pods=timeout'` fails the pods sync with *server stopped sending
      data* within two to four minutes, the watchdog's bound, and the other kinds sync
      meanwhile.
- [ ] `KSTACK_FAULTS='store=fail'` shows *Storage error* on the cache.
- [ ] A mistyped rule stops the sidecar's start with the rule quoted on the start screen.
- [ ] A release build ignores `KSTACK_FAULTS`, and its binary contains no fault code.
- [ ] `docs/development-switches.md` lists the variable and its faults.

## When it lands

- `sidecar/CLAUDE.md`: the *Security invariants* bullet on `applyEnvOverrides` names
  `KSTACK_FAULTS` among what a debug build reads.
- `sidecar/internal/services/cluster/CLAUDE.md`: *The connection pool* gains one bullet: the
  fault wrapper on the `rest.Config`, debug builds only, and the store wrapper beside it.
- `docs/development-switches.md` is updated by the work itself.
- No ADR: the decision is the issue's, and this spec's *What is not in this change* records it.
  If the no-panel rule is contested later, the ADR is written then.
- No row in `docs/persisted-data.md`: nothing is persisted.
