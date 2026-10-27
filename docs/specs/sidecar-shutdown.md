# Spec: shut the sidecar down like a daemon

Grows out of [#448](https://github.com/kstackhq/kstack/issues/448). Read `docs/specs/README.md`
first, then `sidecar/CLAUDE.md` (the "Shutdown" paragraph) and the ADR
`docs/adr/2026-10-26-single-socket-h2c-from-the-standard-library.md`. Go paths are rooted at
`sidecar/`; every other path at the repository.

## Problem

Today the sidecar belongs to the desktop app alone. Soon the CLI and the TUI will share it. That
changes what shutdown means: it is no longer "the app quit", it is "the daemon is stopping", and
another user may be waiting on a request in another terminal.

The shutdown sequence we have is the right shape for that — end the streams at once, let
in-flight requests finish, then stop the background work and close — but four things are wrong
with it:

- **The numbers do not add up.** The sidecar gives requests 30 s and then its background loops
  another 30 s, while the host kills it after 6 s (`src-tauri/src/lib.rs`,
  `SIDECAR_SHUTDOWN_GRACE`). Neither sidecar budget can run out before the kill.
- **A timed-out wait is not followed up.** When `http.Server.Shutdown` gives up, its handlers
  are still running, and the code goes on to close the services under them.
- **A wedge is silent.** A handler or a loop that ignores its cancellation hangs the process
  until the host kills it, and the kill leaves no record of what was stuck.
- **Two copies of one idea.** `graph.Server` and `grpc.Server` each hand-roll "a signal that
  fires at shutdown" with their own `sync.Once`.

## What this builds

1. **Two contexts and a counter in `internal/app`.** `streamsCtx` ends every stream: SSE
   subscriptions and gRPC watches. `requestsCtx` ends every request still running when the
   grace runs out. A `WaitGroup` counts requests in flight, so the sequence can wait for the
   handlers to return. Both transports read the contexts; neither keeps its own signal.
2. **A grace for in-flight requests, then a join.** Unary requests get `requestGrace` to
   finish. Whether or not it runs out, admission then closes, every running handler's context
   is cancelled, and the sequence waits for the handlers to return before it stops anything
   under them. A connection is cut only when the grace ran out.
3. **One watchdog, no other deadline.** If the sequence has not finished at `shutdownBudget`,
   the sidecar logs every goroutine's stack and exits 1. The join, the drain and the close have
   no deadline of their own; a wedge gets a stack trace, not a number.
4. **The host waits off its event-loop thread, with its windows hidden,** logs an unclean
   exit, and keeps its kill as a backstop, sized above the budget.

Nothing changes on the wire.

## The design

### The sequence

`run` in `sidecar/main.go` does this once it has decided to shut down:

```
srv.Shutdown(graceCtx)            stop listening; then every stream ends and flushes its
                                  terminal frame; wait up to requestGrace for requests
app.CloseRequests()               no new request is admitted; every handler still running sees a cancelled context
  if the grace ran out:
    srv.Close()                   its connection is cut too, so a blocked write fails
app.WaitRequests()                wait for every handler to return
stop(context.Background())        cancel each part's loops and wait for them, newest first
app.Close()                       release stores and handles, newest first
exit 0
```

The streams end from inside `Shutdown`: `app.CancelStreams` is registered with
`srv.RegisterOnShutdown`, and Go runs those hooks only after `Shutdown` has closed the
listeners. Ending them before that would let a client reconnect its stream in the gap, see it
end at once on the cancelled context, and reconnect again until `Shutdown` starts.

The close and the join run after **every** `Shutdown`, not only a timed-out one. `Shutdown`
tracks connections and HTTP/2 streams, not handlers: when an HTTP/2 client resets a stream or
disconnects, the stream leaves `Shutdown`'s count while its handler goroutine runs on. So a
`Shutdown` that returns in time still says nothing about a handler that ignores its context,
and only the counter does.

A watchdog started before the first step fires at `shutdownBudget`. It logs every goroutine's
stack (see *Where the dump goes*) and calls `os.Exit(1)`.

Only the request grace is a deadline inside the sequence. The join, the drain and the close
run with no deadline: each is a cancel-and-wait that returns at once when its work honours its
cancellation, and when it does not, the watchdog is what ends the process. The sequence never
reaches `stop` with a handler running, nor `app.Close` with a loop that a part's stop owns
still running, except by exiting first. On a normal stop the sequence finishes in milliseconds
and the watchdog never fires.

Each step logs how long it took, at `INFO`. After a full grace, the join, the drain and the
close share the 5 s left before the watchdog; every stop today is a cancel-and-join and should
take milliseconds, but nothing measures that, and the log is what shows a slow one before it
trips the watchdog.

One goroutine lives outside that invariant today: `auth.StartLogin`
(`internal/services/auth/auth.go`) finishes a sign-in on a goroutine of its own, on
`context.Background()` for up to `loginTimeout`, and no stop joins it, so it can write the
keyring after `Close`. That predates this spec and stays a known gap; fixing it is a change to
the auth service, not to the sequence.

Two numbers, with a fixed relationship:

| Constant | Where | Value | Meaning |
| --- | --- | --- | --- |
| `requestGrace` | `sidecar/shutdown.go` | 10 s | How long a request in flight may hold up the stop. |
| `shutdownBudget` | `sidecar/shutdown.go` | 15 s | How long the whole stop may take before it is a wedge. |
| `SIDECAR_SHUTDOWN_GRACE` | `src-tauri/src/lib.rs` | 18 s | `shutdownBudget` plus time for the dump and the exit. |

### Why streams end at once and requests get a grace

A stream never finishes on its own, so waiting for one is pointless. It ends the way a stream
ends normally: SSE sends `event: complete`, gRPC sends OK trailers. Nothing on the wire says
"the sidecar is stopping"; a client that wants to carry on reconnects, and finds the socket
gone. A distinct shutdown reason is a wire change, and belongs to the daemon spec, which will
have the clients that need it.

A unary request does finish on its own, and once the sidecar is shared by the CLI and the TUI, a
person in another terminal may be waiting on it. `requestGrace` is how long a stop may make that
person wait. It is 10 s from the start, the number people know from `docker stop`, so there is
nothing to remember to raise when the sidecar becomes shared. While the desktop app alone owns
the process, the cost is a Quit that takes up to 10 s when a request happens to be in flight.
Today the host waits for the sidecar on its event-loop thread, so that would be 10 s of a
frozen app with its windows open; Task 3 hides the windows and moves the wait off that thread,
so a slow Quit is an app that has gone, not one that has hung.

### Where the contexts live

`App` owns both contexts, their cancels, and the counter, behind one gate. Its dispatching
handler wraps every request:

```go
a.mu.Lock()
if a.closed {
	a.mu.Unlock()
	http.Error(w, "shutting down", http.StatusServiceUnavailable)
	return
}
a.inflight.Add(1)
a.mu.Unlock()
defer a.inflight.Done()

ctx, cancel := context.WithCancel(r.Context())
defer cancel()
defer context.AfterFunc(a.requestsCtx, cancel)()
next.ServeHTTP(w, r.WithContext(ctx))
```

`CloseRequests()` sets `closed` and cancels `requestsCtx` under the same lock. The gate is what
makes `WaitRequests()` mean something: `net/http` dispatches a handler on its own goroutine
before our wrapper runs, so without it a handler could reach `Add` after the wait saw zero. A
request that arrives after the close is refused at the gate and touches no service. The refusal
is a plain-text 503 for gRPC too, on purpose: grpc-go maps an HTTP 503 to `Unavailable`, which
is the right answer, and a gRPC-shaped refusal would need the gate to know the protocol.

`graph` takes `streamsCtx` and derives a per-request context from it for SSE requests only,
the same way. `grpc` hands `streamsCtx` to `authServer` as today; `AuthStateWatch` returns
`nil` when it ends, so the client sees OK trailers.

Per request, never `http.Server.BaseContext`: cancelling the base context tears down the shared
h2c connection that carries gRPC mid-stream. The ADR records this.

### Where the dump goes

The watchdog logs through the sidecar's own logger, so the dump lands wherever its records go:
`sidecar.log` in every build, and the developer's terminal too in a debug build. It does not
write to stderr: in a release build stderr is the only path into the host's `main.log`, but in
a debug build the host shows forwarded sidecar lines and does not keep them, so a dump there
would reach no file.

The logger cuts every string at 2 KiB, and a whole dump is far larger than that, so the
watchdog logs **one record per goroutine**: it splits `runtime.Stack(buf, true)` at the blank
line between goroutines and logs each as
`log.Error("shutdown wedged", "goroutine", header, "stack", frames)`, after one record naming
the budget. Each record passes through the redacting handler like any other, and a goroutine's
stack rarely exceeds 2 KiB; one that does loses its bottom frames, and the blocked frame is at
the top. One record before the stacks says how many follow, so a reader knows the dump is
complete.

`main.log` gets the host's own record of the exit: the code, at `warn` (Task 3).

## Facts the implementation relies on

Check each before relying on it; they were true when this was written.

- **Neither `http.Server.Shutdown` nor `Close` cancels a request context or waits for a
  handler.** `Shutdown` closes the listeners, sends GOAWAY on HTTP/2, and waits for connections
  and HTTP/2 streams to end — not handlers. An HTTP/2 stream the client reset ends at once
  while its handler runs on, so a successful `Shutdown` can leave a handler running, and a
  timed-out one always does. `Close` closes every connection and waits for nothing. That is
  why the streams and the requests have contexts of their own, and why the sequence joins
  handlers itself, through the counter, after every `Shutdown`. A handler blocked on a write
  to a client returns when its connection closes under it.
- **gqlgen's SSE transport writes `event: complete` when the request context ends**
  (`graphql/handler/transport/sse.go`, the `writeEvent(eventCtx, …)` after the loop).
  `TestGracefulShutdownEndsSSEStream` pins it today.
- **The sidecar's records go to the file `--log-file` names; `--log-stderr` copies them to
  stderr and the host passes it in debug builds only** (`cmd_args` in
  `src-tauri/src/services/sidecar/service.rs`). The host forwards stderr lines into `main.log`
  in release builds and only to the terminal in debug builds (`src-tauri/src/logging.rs`,
  the `SIDECAR_TARGET` arm). A forwarded line is capped at 16 KiB; a per-goroutine record
  carries one 2 KiB-capped string and a few short fields, so the cap never cuts one.
- **The file log handler writes each record as it comes**, so the watchdog's records are on
  disk before `os.Exit`. Check `sidecar/internal/lib/logging`; if it buffers, flush first.
- **The redacting handler cuts every string attribute at `safe.MaxLen`, 2 KiB**
  (`internal/lib/logging/redact.go`, `internal/lib/safe/safe.go`), and logs the type, not the
  contents, of a `[]byte`. **The rotating file refuses a single write over its 2 MiB
  `MaxSize`.** Both are why the dump is one record per goroutine and never one record.
- **A stop func drains with `drain.WithContext`**, which on `context.Background()` is a plain
  join. Its contract is unchanged. `chat.service.Close` expects its readers joined; this
  sequence joins them, or exits before `Close` runs.
- **`os.Exit` runs no deferred call.** The watchdog closes nothing. The OS reclaims sockets and
  files, SQLite recovers its WAL on the next open, and the host's kill would have left the
  same state.
- **An abrupt exit leaves what a crash leaves**, including a login shell the Bash tool had not
  reaped. That is already the case under the host's kill; this spec does not widen it.
- **`callDeadline` in `internal/app.Config` is the precedent for a timer seam**: a
  `func(time.Duration) <-chan time.Time`, nil for `time.After`. Every timer in `shutdown` comes
  from one such seam.

## Task 1 — two contexts and a counter in `internal/app`

Status: Done. The gate is its own unit (`internal/app/requests.go`), so its tests (`requests_test.go`) wrap a plain handler; the real app's handler reaches only real services. `graph.NewServer` takes `streamsCtx` first, as `grpc.NewServer` does.

- `App` gains `streamsCtx`, `requestsCtx`, their cancels, `inflight sync.WaitGroup`, and the
  gate (`mu sync.Mutex`, `closed bool`), made in `New`. Methods: `CancelStreams()`,
  `CloseRequests()`, `WaitRequests()`. `NotifyShutdown` goes. `CancelFunc` is idempotent and
  `closed` is set under the lock, so no `sync.Once`.
- The dispatching handler in `New` wraps every request as above. Keep the gRPC/mux dispatch as
  it is inside the wrapper.
- `graph.NewServer(r, streamsCtx)`: the server keeps the context and loses `shutdownCh`, `once`
  and `NotifyShutdown`. `ServeHTTP` keeps its SSE branch, written with `context.AfterFunc` and
  no goroutine.
- `grpc.NewServer(streamsCtx, authSvc, pokeSvc)`: the wrapper loses `cancel`, `once` and
  `NotifyShutdown`. If it then holds only the `*grpc.Server`, collapse it and return the
  `*grpc.Server` itself. `App.Close` still calls `Stop()` first.
- `main.go` keeps `srv.RegisterOnShutdown`, now with `application.CancelStreams`.

Tests, each beside its unit:

- `graph/server_test.go`: `TestGracefulShutdownEndsSSEStream` cancels the context it passed to
  `NewServer` instead of calling `NotifyShutdown`. Add a case: cancelling it does not end a
  plain query in flight.
- `grpc/auth_server_test.go`: `TestAuthStateWatchEndsOnShutdown` and
  `TestShutdownEndsWatchesAndFinishesCalls` cancel the context they passed to `NewServer`. Their
  assertions hold: the watch ends OK, the call in flight finishes.
- `internal/app/app_test.go`: `TestAppShutdownEndsBothTransports` registers `CancelStreams`
  with `RegisterOnShutdown`, as `main.go` does, and keeps its assertions. Add `TestCloseRequestsEndsAQueryAndWaitRequestsJoinsIt`:
  a query blocked in a fake service until its context ends; `WaitRequests` blocks while it
  runs (a negative assertion, with the comment the conventions require) and returns once
  `CloseRequests` has ended it. Add `TestARequestAfterCloseRequestsIsRefusedAtTheGate`: after
  `CloseRequests`, a query gets 503 and the fake service records no call. The gate is checked
  under the lock, so this one case pins the late-dispatch ordering too: a handler that reaches
  the gate after the close is the same path as a request that arrives after it. Add
  `TestShutdownReturnsWhileAResetStreamsHandlerRuns`: an h2c client (prior knowledge, as the
  host dials) opens a query whose handler blocks on a channel and ignores its context; the
  client cancels the request, which resets the stream; `Shutdown` returns nil at once;
  `WaitRequests` still blocks until the test closes the channel. This is the case the join
  after a successful `Shutdown` exists for.

## Task 2 — the sequence and the watchdog in `sidecar/`

Status: Done. `srv` and `app` are small interfaces (`server`, `application`), so the tests drive the sequence with fakes; `run`'s tests in `main_unix_test.go` cover the real App. The losing-sequence test holds the `exit` seam rather than the log sink, since the sink's handler lock would also hold the sequence's own step records. The outcome is an `atomic.Bool` claimed by compare-and-swap, and `run` joins the watchdog before it returns, which is how a losing sequence waits for the dump; with that join, the watchdog-cancelled case is the order test's no-exit check. `shutdownBudget` is 20 s, the grace plus 10 s, so a request that holds up the stop for the whole grace still leaves the services 10 s to stop; `SIDECAR_SHUTDOWN_GRACE` is 23 s. The watchdog removes the socket file before the dump, since `os.Exit` skips `run`'s defers and the dump can outlast the host's kill. The sequence calls `CancelStreams` itself once `Shutdown` returns: `Shutdown` starts its hooks on goroutines of their own and does not wait for them, so with no connection open it can return first. The stdin watcher starts before `app.New`, so a quit during the login shell cancels it, and a quit during `Start` exits 0; an error from `New` or `Start` is still logged, at `WARN`.

Move the sequence out of `run` into `sidecar/shutdown.go`:

```go
// requestGrace is how long a request in flight may hold up the stop: a person in
// another terminal may be waiting on it once the sidecar is shared.
const requestGrace = 10 * time.Second

// shutdownBudget bounds the whole stop. The host's SIDECAR_SHUTDOWN_GRACE
// (src-tauri/src/lib.rs) exceeds it by the time a goroutine dump and an exit take.
const shutdownBudget = 15 * time.Second

// shutdown is the stop sequence and its seams. Production fills srv, app and
// stop and leaves the rest zero: grace and budget take the constants, after
// time.After, exit os.Exit, and the dump goes to slog.Default.
type shutdown struct {
	srv  *http.Server
	app  *app.App
	stop func(context.Context) error

	grace, budget time.Duration
	after         func(time.Duration) <-chan time.Time
	log           *slog.Logger
	exit          func(int)
}

// run runs the sequence and returns the exit code.
func (s *shutdown) run() int
```

- Every timer comes from `after`, including the grace: build `graceCtx` from
  `context.WithCancel` and cancel it from a goroutine selecting on `after(grace)` and the
  sequence's `done` channel. `context.WithTimeout` would not answer the seam.
- Start the watchdog goroutine first. On `after(budget)` it logs the dump as described under
  *Where the dump goes*, then calls `exit(1)`.
- **The sequence and the watchdog settle who exits under one lock.** A `select` between `done`
  and the timer picks at random when both are ready, so `done` is not enough. Instead, one
  mutex guards an `outcome` field: the sequence sets it to *completed* when its last step
  returns, and the watchdog sets it to *wedged* when its timer fires; whichever finds it
  already set does nothing. A watchdog that lost returns without logging or exiting.
- **A sequence that lost waits for the watchdog.** The watchdog claims the outcome before it
  collects and logs the stacks, so a sequence that finishes in the meantime must not return:
  `run` in `main.go` would close the logger and call `os.Exit`, cutting the dump off. The
  watchdog closes a `wedged` channel once its dump is logged and its `exit` call has returned
  (in production it never returns); the losing sequence waits on that channel, then returns 1.
  A test with an injected `exit` therefore sees the whole dump before `run()` returns.
- Then, in order: `srv.Shutdown(graceCtx)` (whose hook runs `app.CancelStreams`);
  `app.CloseRequests()`; `srv.Close()` if `Shutdown` erred; `app.WaitRequests()`;
  `stop(context.Background())`; `app.Close()`. Log each step's duration at `INFO` and each
  error at `WARN`, both with the step's name. No error changes the exit code, which is 0 when
  the sequence completes first.
- The grace timer's goroutine selects on `after(grace)` and `done` too. That one may pick either
  when both are ready, and it does not matter: cancelling `graceCtx` after `Shutdown` has
  returned changes nothing.
- `run` in `main.go` builds the struct with the three production fields and returns what
  `run()` returns. The "Order matters" comment moves with the code and names the ADR.

Tests in `sidecar/shutdown_test.go`. Each runs the sequence in a goroutine. The `exit` seam
records its code on a channel and returns, so the sequence runs on after a wedge; a test that
then asserts on `run()`'s result releases what it blocked first, since `run()` cannot return
over a blocked step. Cleanup joins the goroutine, and releases anything a failed assertion left
blocked, so no test leaks the sequence.

- **The order.** A stop func and the real `App` over a test `Config`, with `CancelStreams`
  registered on the server, see `Shutdown` (and `CancelStreams` inside it) → `CloseRequests` →
  `WaitRequests` → `stop` → `Close`; the code is 0 and `exit` is never called. The log sink holds
  one duration record per step.
- **The grace.** A handler blocked until its context ends; the test fires the grace timer
  through `after`; `CloseRequests` ends the handler, `WaitRequests` returns, and the code is 0.
- **The watchdog on a drain.** A stop func that drains with `drain.WithContext` over a goroutine
  blocked on a channel, so the drain is the real cancel-and-wait and not a stub that blocks
  unconditionally; the test fires the budget timer and `exit` sees 1. Then the test closes the
  channel, so the drain finishes and `run()` returns 1 — `run()` cannot return while the drain
  is blocked, so a test that left the release to cleanup would wait on it forever. Cleanup
  joins the goroutine.
- **The dump survives the production logger.** The `log` seam is `logging.Init` over a buffer,
  not a plain `slog` handler, so the redacting handler and its 2 KiB cut are in the path. The
  blocked goroutine in the test above sits under enough frames that the whole dump exceeds
  2 KiB. Assert one record per goroutine, that the blocked goroutine's record names the
  blocked function in its first frame, and that the count record matches the number of stack
  records. The first-frame assertion also covers the redaction rules: a frame is file paths,
  function names and hex, and if a credential-shaped rule ever matched one, the frame would
  come out altered.
- **The watchdog on a handler.** A handler that ignores its context, blocked on a channel; the
  test fires the grace, then the budget; `exit` sees 1. The test closes the channel, `run()`
  returns 1, and cleanup joins.
- **A losing sequence waits for the dump.** The log sink is a writer that blocks on a channel
  the test holds. A stop func blocked on a channel; the test fires the budget, so the watchdog
  claims the outcome and blocks in its first write; the test then releases the stop func, so
  the sequence completes and loses. Assert `run()` has not returned (a negative assertion: a
  `select` on its result channel against a short `time.After`, with the comment the conventions
  require). Release the sink; `run()` returns 1 and the sink holds every record.
- **The watchdog is cancelled.** With everything returning at once, firing the budget timer after
  `run()` has returned 0 never calls `exit`, because the sequence claimed the outcome first.
  Since the claim is under the lock, the assertion is immediate: after firing the timer, join
  the watchdog goroutine through a seam (its own `done` channel) and then read the exit channel
  with a non-blocking receive.

## Task 3 — the host

Status: Done. `SIDECAR_SHUTDOWN_GRACE` is 23 s, above the 20 s `shutdownBudget` Task 2 records. The quit state is `quit.rs`'s `Quit`, on `AppState`. The window methods check the app-wide `shutdown` token instead, which the first `ExitRequested` cancels, so one fact is not held twice. `new_window` returns `Result<()>`.

- `SIDECAR_SHUTDOWN_GRACE` becomes 18 s. Its comment names `shutdownBudget` in
  `sidecar/shutdown.go` and says what the margin covers.
- **The wait leaves the event-loop thread.** `graceful_shutdown` runs today inside the
  `RunEvent::ExitRequested` callback (`src-tauri/src/lib.rs`), which is the event loop, so a
  slow sidecar is a frozen app. Instead, `WindowManager` owns a quit state with three values:
  *running*, *waiting*, *done* — it owns it because it is the one reader, and it already sits
  in `AppState` where the `ExitRequested` handler can move it, so nothing else needs a handle
  on `AppState`.
  - The first `ExitRequested` with a code moves *running* → *waiting*, keeps the existing
    `state.shutdown.cancel()` (before the windows hide, as today), `api.prevent_exit()`s,
    hides every window, and spawns a thread that runs `graceful_shutdown`, moves *waiting* →
    *done*, and calls `app_handle.exit(code)`.
  - Every `ExitRequested` that arrives while *waiting* is prevented. Every Quit path — the
    tray, the app menu, the Dock's re-drive, the webview's command — reaches the same
    `app.exit(0)`, so a second Cmd+Q during the wait is one of them, and letting it through
    would run `RunEvent::Exit` and kill the sidecar mid-stop. **A second Quit does not force
    quit; it is ignored.** Force Quit from the OS is the way out of a stuck wait.
  - Only the thread's own exit arrives in *done*, and that one goes through.
  - `RunEvent::Exit` keeps the kill as the backstop.
- **Nothing reopens a window while waiting.** Every path that opens or shows a window — the
  tray, the app menu, the Dock menu, the Dock icon (`RunEvent::Reopen`), the webview's command,
  and the single-instance callback for a second launch — ends in `WindowManager::new_window`
  or `WindowManager::show_main_window` (`src-tauri/src/window_manager.rs`). The check goes in
  those two methods, and nowhere else: unless the quit state is *running* they return `Ok(())`
  without opening anything. A window path added later is covered without a check of its own.
  Otherwise a window would come back over a sidecar that is stopping and vanish when the exit
  lands. `new_window` returns `Result<()>` instead of `Result<WebviewWindow>`: no caller uses
  the window, and an ignored click must not come back as an error, or the tray, the app menu
  and the Dock menu would log it as a failure and the webview's command would raise one.
- A `cargo test` covers the state machine: an `ExitRequested` while *waiting* is prevented;
  the one in *done* is not; `new_window` and `show_main_window` open nothing while *waiting*.
- A SIGTERM to the host during the wait does nothing: the signal task has already returned on
  `shutdown.cancelled()`, and the registration it leaves installed swallows the signal. Only
  SIGKILL ends the wait early. That is true today for 6 s and becomes true for 18 s.
- A second launch during the wait is dropped: the single-instance plugin hands it to the
  instance that is quitting, which opens nothing, and the user launches again once it has
  exited. Accepted; it is not a bug.
- `CommandEvent::Terminated` in `src-tauri/src/services/sidecar/service.rs` logs at `warn` when
  the code is not `Some(0)` or a signal is set, and at `info` otherwise. The quit path in
  `lib.rs` already warns when the grace lapses; after the kill, log that it was needed.

## Acceptance

- With an SSE subscription, a gRPC watch, and a GraphQL query blocked in a fake service all
  open: the streams end with their terminal frames at once, `Shutdown` waits for the query, and
  the sidecar exits 0 as soon as it returns.
- A query that returns only when its context ends: at 10 s the sidecar cancels it, joins it,
  finishes the rest of the sequence, and exits 0 with a `WARN` naming the step. For those 10 s
  the app's windows are hidden, its menu bar and tray respond, nothing reopens a window, and a
  second Quit changes nothing.
- `sidecar.log` holds one `INFO` record per step with its duration, on every stop.
- A query that ignores its context: the sidecar exits 1 at 15 s. It is a wedge, and it is
  reported as one.
- A part whose stop blocks forever: the sidecar exits 1 at 15 s. In every build `sidecar.log`
  holds the `ERROR` records — one naming the budget and the goroutine count, then one per
  goroutine, the blocked one's first frame intact — and `main.log` holds the host's `warn` with
  exit code 1. The host's kill does not run.
- `make test-go`, `make lint-go`, `make vet-go`; `cargo test` in `src-tauri`.

## Out of scope, named so it is not lost

**A macOS logout or system shutdown skips the sequence.** The host answers `NSTerminateNow`
(`src-tauri/src/dock_menu.rs`), because cancelling would abort the user's logout, and its
cleanup runs in `RunEvent::Exit`, which kills the sidecar at once. The grace and the watchdog
never run on that path. That is the right call and stays as it is.

When the sidecar becomes a shared daemon, three things need their own spec:

- **Who owns its life.** Who starts it, who may stop it, and an idle stop that counts in-flight
  requests and open streams as "not idle" — the `inflight` counter is the start of that count.
  The stdin-EOF parent-died signal stops being the shutdown trigger; the desktop app becomes a
  client that sends the same stop the CLI does.
- **Who kills a wedge.** The host's kill goes away with its ownership of the process, or moves
  to launchd / systemd. The watchdog is already written to be the only bound.
- **A shutdown reason on the wire.** A TUI that should reconnect after a restart, but exit
  after a stop, needs the terminal frame to say which. That is a wire change for the clients
  that will need it.

## When it lands

- `sidecar/CLAUDE.md`: rewrite the "Shutdown" paragraph to the sequence above and its traps —
  cancel per request, never `BaseContext`; handlers are joined before `stop`, loops before
  `Close`; the watchdog is the only bound after the grace.
- `docs/adr/2026-10-26-single-socket-h2c-from-the-standard-library.md`: if #448 has not merged,
  rewrite its shutdown paragraph. If it has, write a new ADR for why streams end at once and
  requests get a grace, why one watchdog replaces per-step deadlines, and where each number
  lives; point the old paragraph at it.
- Root `CLAUDE.md` needs no change unless the ADR link moves.
- Delete this spec.
