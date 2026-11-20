---
title: The services restore in the background
scope: sidecar, host, webview
status: Planned
---

# The services restore in the background

**Answers:** [#1094](https://github.com/kstackhq/kstack/issues/1094), step 2 of
[the sequence](README.md). **Needs:** [step 1](1-the-read-that-waits.md). **Unblocks:** step 3.

Go paths below are under `sidecar/internal/` unless they say otherwise. `graph/`, `grpc/` and
`gqlgen.yml` are under `sidecar/`.

## In short

- **No `New` calls the keyring.** Each service publishes `Restoring`, reads on a goroutine through
  step 1's `oskeyring.Read`, and publishes what it read.
- **While `Restoring`, a request that needs the credential waits; a request that would change it is
  refused.**
- `Restoring` is on the wire: a `ConnectorPhase`, a `ProviderCredentialSource`, and
  `AuthState.restoring` on GraphQL and gRPC.
- A turn's idle clock stops while its request is being signed, so a turn can wait out the unlock
  prompt.
- The webview and the host change only enough to build.

After it the launch no longer waits on the keyring. The windows draw a `Restoring` row with its
default detail until step 3.

## Not in this step

- What the windows draw for `Restoring`. Step 3.
- A deadline on `Set` and `Delete`, and reading again after an unreadable keyring
  ([the sequence](README.md#not-in-this-sequence)).

## Design

### 1. The vocabulary

```go
// connectors
Restoring Phase = "Restoring" // the launch has not read the keyring yet
ErrRestoring = errors.New("connectors: the keyring has not been read yet")

// providerkeys
SourceRestoring Source = "Restoring"
ErrRestoring = errors.New("providerkeys: the keyring has not been read yet")
```

`Restoring` is entered only by `New` and left only by the restore. Nothing returns to it.

### 2. What every service does

The five services are `oauthconn` (which `chatgpt`, `google` and `kstackcloud` run), `openrouter`,
`github`, `aws` and `providerkeys`. Each follows the same shape; §3 and §4 state only what is its
own.

**`New`** publishes `Restoring`, starts the restore on the service's `wg` and returns. It reaches
no network and makes no keyring call. Reading `app.db` stays in `New`.

**The restore**, in each package's `keyring.go`:

1. `oskeyring.Read(s.ctx, s.keyring, account, oskeyring.SlowAfter)`, holding no lock. The retry
   and the one prompt are the gate's (step 1), behind the keyring `build` passed.
2. If `s.ctx` has ended, return. Nothing is published.
3. Judge what was read, holding no lock, by the package's own rules, which do not change. In
   every package no entry is signed out, an unreadable keyring is signed out with a warning and
   **nothing written**, and a usable entry is connected. What happens to any other entry is the
   package's: `openrouter`, `github`, `aws` and `providerkeys` leave a newer `v` and delete what
   they cannot parse; `oauthconn` deletes both (§3).
4. If the entry is to be deleted: `oskeyring.Delete(s.ctx, s.keyring, account)`, still holding no
   lock. A failure is a warning, as today. If `s.ctx` has ended, return.
5. Under `mu`: register a usable secret with `safe.AddSecret`, take a new generation for it,
   publish the state, and close `restored`.

**No keyring call of the restore runs under `mu`, and every one ends with `s.ctx`.** A delete that
never answers is a read that never answers: the service stays `Restoring`, `State` still answers,
and `Close` returns. The delete comes before the state is published because every change is
refused until then (below), so no `Connect` can write the entry the delete is about to remove.

**Nothing moves the generation while `Restoring`**, for the same reason, so the restore checks the
phase and not a generation.

**`Restored() <-chan struct{}`** is closed once the restore has published. It is what
`providerkeys`, `kstackcloud` and the tests wait on. A service that waits on another's
`Restored()` waits on its own `ctx` too: the other's restore may never answer, and `Close` must
still return.

**`Close`** cancels `ctx` and waits for the service's goroutines. The restore returns as soon as
`ctx` ends, so `Close` never waits on the keyring: a `Get` that never answers is left behind, and
ends with the process.

**While `Restoring`:**

| Call | Answer |
| --- | --- |
| `Sign`, `Token`, `Retrieve` | Waits for `restored`, then answers as it does today. The caller's context ends the wait with its error; `s.ctx` ends it with `llm.ErrSignedOut`, or the package's own `ErrSignedOut`. |
| `Bind` | A `signer.Pending` (below), which ends the same two ways. |
| `Refused` | Nothing. |
| `Connect`, `Start`, `Disconnect`, `Save`, `Delete` | `ErrRestoring`. No keyring call is made. |
| `Cancel` | Nothing: there is no attempt, and the generation stays. |
| `State`, `Subscribe` | `Restoring`. |
| `WatchSession` | `0`. |

Refusing, not waiting, is the one rule for every change. A wait would hold a mutation open behind
a password prompt, and after step 3 no view offers these actions while it draws *checking*.

**A binding made while `Restoring` is the restored session's.** `Bind` answers
`signer.Pending(s.restored, s.ctx.Done(), resolve)`. `resolve` must answer the binding of the session the
restore published, never of whichever is current at the first `Sign`: a binding made at launch
must not come alive under an account connected later. So each service records the generation its
restore published (`restoredGen`, `0` for none) and resolves to `sessionBinding{s, restoredGen}`,
or `signer.Ended{}` for `0`.

### 3. The connectors

**`oauthconn`.** `New` loads the row, then publishes `Restoring` with `Registered` from it. **A row
that cannot be read restores too**: the read is made so the service stays `Restoring` until the
keyring has answered, then it publishes `SignedOut` and writes neither store, whatever was read.
No service leaves `Restoring` before its own read, which is why no write can arrive while the gate
is opening (step 1, §3).
The restore reads `refreshAccount()`. `restore()` splits in two: the judgement, and
`applyRestoreLocked`, which writes the row and publishes. The row's write is `app.db`'s and stays
under `mu`; the entry's delete is §2's step 4, so it now comes before the row's write.

The engine's rules for its two stores, as on `main`:

| The read | The row's `session_id` | The entry | State |
| --- | --- | --- | --- |
| failed | left | left | `SignedOut` |
| any, with a row that could not be read | left | left | `SignedOut` |
| an entry whose `session` is the row's, with a token and a client | left | left | `Connected` |
| no entry | cleared if set | — | `SignedOut` |
| any other entry | cleared if set | deleted | `SignedOut` |

**"Any other entry" includes one whose `v` this build does not know.** Unlike the other
connectors, the engine does not leave a newer entry: a session needs the row and the entry to
agree, and it cannot tell that they do. This spec keeps that. If the delete is cancelled, or the
row's write fails, the next launch finds the same mismatch and finishes it.

`sign` waits after its `signer.Covers` check, so an off-resource request is still refused at
once. `chatgpt` and `google` embed the engine and do not change.

**`openrouter`, `github`, `aws`.** Each `restore()` splits the same way. `github.Token(ctx)` and
`aws.Retrieve(ctx)` wait under `ctx`; `Tokens(fallback).Token` then falls back to `gh` only when
the connector is signed out, as today. `openrouter.Bind` answers a `Pending` while `Restoring`,
which `providerkeys` takes into its own (§4).

**`kstackcloud`.**

- `Service` gains a context, its cancel and a `WaitGroup`, as the other connectors have; `Close`
  cancels, waits, then closes the engine.
- `New` no longer deletes `auth-token`. A goroutine of the service's waits for the engine's
  `Restored()` or its own `ctx`, then runs `oskeyring.Delete(ctx, k, oldAccount)` **only if
  `oskeyring.Opened(k)` answers nil**. The engine's read has passed the gate by then, so `Opened`
  answers at once. After a dismissed prompt or an unreadable keyring it deletes nothing: a
  `Delete` passes a failed gate, and would raise the prompt again. The next launch tries, as a
  failed delete does today. The `engine` interface gains `Restored()`.
- `Subscribe` sends only a change of account, and `account` gains `restoring`, so the frame that
  leaves `Restoring` for signed out is sent.
- `Connect` answers the engine's `ErrRestoring`.

### 4. `providerkeys`

**A provider is `Restoring` until its own read has answered and its connector's restore has
landed.** One read per provider runs on the service's `wg`, all started together by `New`.

- `Connector` gains `Restored() <-chan struct{}` and `RestoredSession() uint64`, the session
  its restore published, 0 for none, which never changes after `Restored()` closes. A connector
  publishes its session on `WatchSession` before it closes `restored`. Its doc's rule for `Bind` becomes: `signer.Ended{}`
  whenever it has no session and is not `Restoring`, and nothing else then; a `Pending` while
  `Restoring`.
- `New` no longer receives from `WatchSession`. Each follower waits for the connector's
  `Restored()` or the service's `ctx`, and **only then subscribes to `WatchSession`**. A receiver
  made before the restore published can deliver the `Restoring` `0` after it, since `Chan`'s
  feeder may already hold that value; one made after delivers the current session first, which a
  `Disconnect` or `Connect` may already have moved from the restored one. On that first value,
  under `mu`, the follower records `RestoredSession()` as `landedGen`, marks the connector
  landed, then applies the value as any later one: **the landing itself does not move
  `keyGen`**; a session other than the restored one moves it as today. `landedGen` never
  changes after.
- A read that answers sets `saved` and `newer` under `mu`. It does not move `keyGen` either: no
  binding under a key exists before the provider has landed.
- When both have landed the provider's `restored` closes, in the same critical section that
  publishes its state.

`source` gains a first row, and `State` one method:

| Restoring | Connector's session | Saved key | Environment key | `Source` |
| --- | --- | --- | --- | --- |
| yes | any | any | any | `SourceRestoring` |
| no | non-zero | any | any | `SourceConnector` |
| no | 0 or none | held | any | `SourceSavedKey` |
| no | 0 or none | none | held | `SourceEnvironmentKey` |
| no | 0 or none | none | none | `SourceNone` |

```go
// HasCredential says a credential is known to be present: while Restoring,
// the environment key alone.
func (st State) HasCredential() bool
```

`WatchHasCredential` sends `HasCredential()`. **A launch with an environment key lists that
provider's models at once**, locked keyring or not; one with a saved key alone lists them when the
read answers. `catalog.New` keeps a keyed provider's written catalog when `HasCredential()`, in
place of its `Source` check.

`failure` reports nothing while `SourceRestoring`.

**Signing while `Restoring`.** A saved key must not sign and then be replaced by the connector a
moment later, so nothing signs until both are known.

- `Bind` takes the connector's binding `cb` (`signer.Ended{}` with no connector) and a
  `keyBinding` of the current `keyGen`, and answers
  `signer.Pending(p.restored, s.ctx.Done(), resolve)`. `resolve` answers `cb` when the connector
  had a session at the bind, else the `keyBinding`, which signs with the saved key, then the
  environment key, or fails `llm.ErrSignedOut` with neither. Once the connector has restored, `cb`
  says which, as outside `Restoring`; before, `cb` is a `Pending`, and `landedGen` says. It never
  reads the current `connectorGen`: a session started after the bind is not the bind's, and a
  binding that resolved to the connector fails after a `Disconnect`, as one made while connected
  does.
- The unbound `Sign` waits for `p.restored`, then runs as today. The wait ends with the request's
  context, and with the service's.
- `Save` and `Delete` answer `ErrRestoring` and make no keyring call.

The restore holds neither `writes` nor `mu` across a keyring call, its delete of an unusable entry
included (§2, step 4). It takes `mu` only to apply what it read.

### 5. Wiring

`app/runtime.go`, `build`, adds no part: each service's restore is its own goroutine, joined by
its existing close. `providerkeys.Config.Connectors` is unchanged, since `openrouter.Service`
gains `Restored()`.

The catalog loops need no change. `chatgpt` and `google` follow `WatchSession`, which is `0` until
the restore lands, and `providerkeys` follows `WatchHasCredential`.

### 6. The idle clock stops while a request is signed

Each dialect starts its idle guard (`llm/helpers.go`, `newIdleGuard`) before the SDK builds the
request, and `send` signs under the guard's context. A `Sign` that waits for the keyring past the
dialect's bound would therefore end as `ErrStreamIdle`.

- `newIdleGuard` puts the guard in the context it returns.
- `send` finds it there and brackets `authorize`: `guard.pause()` stops the timer, and
  `guard.resume()` restarts it at the full bound once `authorize` returns, whatever it returned. A
  request with no guard in its context, a catalog read, signs as today.
- The user's *Stop* still ends the wait: `pause` stops the timer, not the context.

Signing is bounded on its own: a refresh by the connector's `issuerTimeout`, and the restore by
design only by the user.

### 7. GraphQL

```graphql
enum ConnectorPhase {
  "The launch has not read the keyring yet. Neither signed out nor connected."
  Restoring
  SignedOut
  …
}

enum ProviderCredentialSource {
  "The launch has not read the keyring yet, or the provider's connector has not."
  Restoring
  None
  …
}

type AuthState {
  "True until the launch has read the keyring. `authenticated` is false then, and says nothing."
  restoring: Boolean!
  authenticated: Boolean!
  identity: Identity
}
```

- `gqlgen.yml` binds both new members, as the others are. `AuthState.restoring` is
  `Phase == connectors.Restoring`.
- `connectorErr` adds `connectors.ErrRestoring`, and `providerKeyErr` adds
  `providerkeys.ErrRestoring`, both as `KSTACK_VALIDATION_ERROR`. Each connect, disconnect,
  `authLoginStart`, `authLogout`, `providerKeySave` and `providerKeyDelete` description says it is
  refused while `Restoring`.
- Every connection type's field resolvers already key on `Connected`, `SigningIn` or `Failed`, so
  each answers null while `Restoring`.
- `ProviderCredential.hasEnvironmentKey` is true while `Restoring` when the launch held the
  variable; `hasSavedKey` is false until the read answers.

### 8. gRPC

`proto/auth.proto`: `AuthState` gains `bool restoring = 3;`, then `make generate-proto`.
`grpc/auth_server.go`'s `toAuthState` sets it for `connectors.Restoring`. `StartLogin` and `Logout`
answer `connectors.ErrRestoring` as `FailedPrecondition`; every other error stays `Internal`.

### 9. What the webview and the host need to build

- **Webview.** After `pnpm codegen`, each `switch` over `ConnectorPhase` in
  `settings-dialog/connectors.tsx` and over `ProviderCredentialSource` in
  `settings-dialog/providers.tsx` gains a `Restoring` case that answers what `SignedOut` or `None`
  answers. `providersFrom`'s `configured` holds for neither `None` nor `Restoring`. Step 3
  replaces each.
- **Host.** `src-tauri` builds `AuthState` literals in its tests; each gains `restoring: false`.
  The tray reads `authenticated` alone, so a restoring account draws as signed out until step 3.

## Security

No boundary moves and no `security-model.md` row changes status. Three things the change must
keep, each already pinned by the package's own test, which now waits for `Restored()` first:

- A restored secret is registered with `safe.AddSecret` before its state is published.
- No log line holds what `oskeyring.Read` answered.
- An unreadable keyring writes nothing. The retry does not change that: after the last retry the
  service is signed out and the entry is left.

A request made while `Restoring` is never sent unsigned: `Sign` returns an error or sets the
header.

## Docs

- **ADR** `docs/adr/<date>-a-launch-restores-keyring-credentials-in-the-background.md`, written
  first so the review reads it before the code: why a deadline on the read was rejected (a short
  one fails the user typing a password, and nothing reads again; a long one stalls as long); why a
  change is refused and a signature waits; why the first call runs alone; why `Close` leaves a read
  that never answers. It amends the Decision's "ignored and deleted at launch" in
  [the engine's ADR](../../adr/2026-11-18-an-oauth-connection-is-a-row-matched-to-its-keyring-entry.md):
  the match is now made by the restore, after the launch. That ADR gets its `amended_by` line.
- `CLAUDE.md`s: root (a connection gauge's and `providerCredentialsWatch`'s first frame can be
  `Restoring`); `sidecar/` (*Connectors*); `oauthconn`, `openrouter`, `github`, `aws` (*`New`
  reads it* becomes the restore, what waits and what is refused, what `Close` leaves);
  `kstackcloud` (the old entry's delete, `Subscribe`); `providerkeys` (`SourceRestoring`,
  `HasCredential`, which values leave `keyGen`); `internal/llm` (the idle clock stops while a
  request is signed).
- `docs/TODO.md`: the `auth-token` line names the goroutine in place of `kstackcloud.New`.
- `docs/persisted-data.md` is unchanged. A pinning test it names keeps its name.

## Tasks

| # | Task | Status |
| --- | --- | --- |
| 1 | The ADR; `connectors.Restoring` and `ErrRestoring`; `oauthconn`'s restore, waits and refusals | Planned |
| 2 | `openrouter`, `github`, `aws`: the same | Planned |
| 3 | `kstackcloud`: the old entry's delete, `Subscribe`; `proto/auth.proto`, `toAuthState`; the host's literals | Planned |
| 4 | `providerkeys`: `SourceRestoring`, `HasCredential`, the followers, the pending binding; `catalog.New` | Planned |
| 5 | `llm`: the guard in the context, `pause` and `resume` around `authorize` | Planned |
| 6 | GraphQL schema, `gqlgen.yml`, the two error maps, resolver tests; `pnpm codegen`; the webview's cases; `app`'s launch tests | Planned |
| 7 | macOS: serialize the restores' reads, only if step 1's manual check finds a dialog per read | Waits on step 1's manual check |

Tasks 1–6 do not depend on the macOS check and can start now. Task 7 is dropped if the check
finds one dialog.

Verification is `docs/specs/README.md`'s *Verification commands*: `make test-changed` while
working, `make check-changed` before the pull request, the wire checks, and `make generate-proto`
in task 3.

## Acceptance

Every test uses `oskeyringtest`, waits on `Restored()` or a watch, and never sleeps.

- `app.New` returns while the keyring holds every call, with every connector `Restoring`, every
  keyed provider `SourceRestoring` and `authState.restoring` true
  (`TestTheLaunchDoesNotWaitForTheKeyring` in `app`).
- When the keyring is released each service publishes what it read, with no relaunch
  (`TestTheRestorePublishesWhatItRead` in `oauthconn`, `openrouter`, `github`, `aws` and
  `providerkeys`, with a connected, a signed-out and an unreadable case each).
- A `Sign` made while `Restoring` carries the restored credential once the read answers, and ends
  with its context when the read never does (`TestSignWaitsForTheRestore` in `oauthconn`,
  `openrouter` and `providerkeys`; `TestTokenWaitsForTheRestore` in `github`;
  `TestRetrieveWaitsForTheRestore` in `aws`).
- A binding made while `Restoring` signs for the restored session, and fails `llm.ErrSignedOut`
  after a `Disconnect` and a new `Connect` (`TestABindingMadeWhileRestoringIsTheRestoredSessions`
  in `oauthconn` and `openrouter`).
- `Connect`, `Disconnect`, `Save` and `Delete` made while `Restoring` answer `ErrRestoring`, and
  `Calls()` holds no `Set` or `Delete` before the read answered
  (`TestAChangeIsRefusedWhileRestoring`, per service).
- With a saved key and a connector that restores connected, a request made while `Restoring`
  carries the connector's key and never the saved one; with a connector that restores signed out,
  the saved key. A binding made while `Restoring` that resolved to the connector fails
  `llm.ErrSignedOut` after a `Disconnect`, and never signs with the saved key
  (`TestNothingSignsBeforeBothAreKnown` in `providerkeys`).
- A provider with an environment key has `HasCredential()` while `Restoring`, and its models are
  listed before the keyring answers; one with a saved key alone lists them once it does
  (`TestAnEnvironmentKeyListsItsModelsAtOnce` in `app`).
- `Close` returns while the keyring holds a read, and the state is still `Restoring` after it
  (`TestCloseLeavesAReadThatNeverAnswers`, per service).
- With an entry the build cannot use and the keyring holding its delete (`Hold(Delete)`), `State`
  answers `Restoring` and a change is refused. Then, per service:
  - released before `Close`, the service publishes signed out and closes `Restored()`
    (`TestTheRestoreWaitsForItsCleanup`);
  - closed before the release, `Close` returns, nothing is published, and `Restored()` stays open;
    the test releases the delete in its cleanup (`TestCloseLeavesACleanupThatNeverAnswers`).
- `oauthconn` restores by its table: each of its rows, with an entry of a newer `v` deleted
  and the row signed out (`TestTheRestoreTable`). With a row it cannot read it stays `Restoring`
  while the keyring holds the read, then is `SignedOut` with both stores as they were
  (`TestAnUnreadableRowWaitsForTheKeyring`).
- **One bad entry, through a whole launch.** `app.New` over a gated fake with `FailGet` set for one
  connector's account: that connector is `SignedOut`, and every other service restores what
  the keyring holds (`TestABadEntrySignsOutItsOwnServiceAlone` in `app`).
- **A dismissed prompt, through a whole launch.** `app.New` over `oskeyring.Gate` of a fake whose
  first `Get`, the probe, answers an error that is not `ErrUnavailable`: once every service's
  `Restored()` is closed, every connector is `SignedOut`, no keyed provider holds a saved key,
  `Calls()` is that one `Get`, with no `Delete` of `auth-token`. A `providerKeySave` then reaches the keyring, and
  the app closes (`TestADismissedPromptIsAskedOnce` in `app`).
- Over `oskeyring.Gate` of a fake: with the probe answered, `auth-token` is deleted after the
  restore; with the probe failed it is not; with the engine's read held, `Close` returns and
  nothing is deleted (`TestTheOldEntryIsDeletedOnlyFromAKeyringThatOpened` in `kstackcloud`).
- With the connector's restore held, a `providerkeys` provider stays `Restoring`, and `Close`
  returns. Released, the provider lands on the connector's restored session, never on the `0`
  published while `Restoring`: a connector that restores connected gives `SourceConnector`, with
  `keyGen` unmoved (`TestAFollowerLandsOnTheRestoredSession` in `providerkeys`). A connector that
  restores connected and disconnects before the follower subscribes still lands it on the restored
  session: a binding made while `Restoring` fails `llm.ErrSignedOut` and never signs with the
  saved key (`TestNothingSignsBeforeBothAreKnown`).
- A binding made while `Restoring`, signing under a context that never ends, answers
  `llm.ErrSignedOut` when the service closes with the read still held; so do an unbound `Sign`,
  `Token` and `Retrieve`, each with its package's error (`TestAWaitEndsWithClose`, per service).
- A turn whose signer holds `Sign`, as a pending binding does, while the guard's clock is moved
  past the idle bound does not end; released, it streams its answer, and the clock runs again from the
  full bound. *Stop* ends it while it is held (`TestTheIdleClockStopsWhileARequestIsSigned`, per
  dialect in `llm`, on the test clock). The test is `llm`'s, with a signer of its own, because the
  bound and the clock are its parameters (`service.go`) and `signer` imports `llm`.
- `kstackcloud`'s `Subscribe` sends the frame that leaves `Restoring`
  (`TestSubscribeSendsTheEndOfTheRestore`).
- The resolvers map both `ErrRestoring`s to `KSTACK_VALIDATION_ERROR`; `authStateWatch` and the
  gRPC watch carry `restoring`; `StartLogin` and `Logout` answer
  `FailedPrecondition` (`graph/schema.resolvers_test.go`, `grpc/auth_server_test.go`).

`coverage / Gate` is green.

## Things to watch

- **Tests that read the state straight after `New`.** Each package's rig gains a helper that waits
  on `Restored()`; every existing test that builds a service calls it, or asserts `Restoring`.
- **macOS has one dialog to wait on: the keychain's unlock.** `go-keyring` reads through
  `/usr/bin/security`, so an item's access is decided for that tool, not for Kstack, and a change
  of Kstack's signature raises nothing. Whether the probe raises the unlock dialog is step 1's manual check. If it
  does, the reads wait behind it and one dialog is shown. If it does not, the gate opens at once,
  the reads run together, and whether they share one dialog is the same check's to answer. **If it
  finds several, the one-prompt promise does not hold on macOS** until task 7 serializes the reads;
  the README and step 1 are corrected to say so.
- **A turn sent at launch on a listed model waits in `Sign`.** A provider with a saved key alone,
  and ChatGPT and Google, list no model until their restore lands, so a turn on one of theirs is
  refused as an unknown model until then. It shows as a turn with no first token yet, and stops
  with the user's *Stop*. The idle clock is stopped for it (§6), and nothing else times it.
- **A dismissed prompt fails every read** (step 1's gate), whenever it arrives. Each service then
  reports an unreadable keyring and makes no keyring call; a turn that was waiting fails signed
  out, or runs on its environment key.
- **The first frame is no longer the answer.** A consumer that treated the first frame of a
  connection gauge as signed in or out now sees `Restoring` first. `catalogsync` and the followers
  already take each change in order.
- **Between this step and step 3 a restoring row reads as signed out, with no button.** The
  rows draw their actions for `SignedOut` and `Failed` alone, so a `Restoring` row offers none.
  The providers pane still offers *Save key*, which answers `KSTACK_VALIDATION_ERROR`. Both steps are on `wip/keyring-restore` before either reaches `main`.

## When it lands

The guides already say what the pull request built. Step 1's spec is deleted with this one, and
both rows leave [the sequence's README](README.md).
