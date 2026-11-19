---
title: The Kstack connector
scope: sidecar, host
status: Planned
---

# The Kstack connector

**Answers:** [#1046](https://github.com/kstackhq/kstack/issues/1046). **Needs:** the connectors
as one package each (#1056, on `main`). **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the sidecar has two OAuth sign-ins. `services/auth` signs in to Kstack cloud;
`services/connectors/internal/oauthconn` is the engine under the ChatGPT connector. Both are an
authorization code with PKCE on a loopback redirect, a refresh token in the OS keyring, a lazy
refresh and a revocation on sign-out, and each has its own code for all of it.

After this spec the Kstack sign-in is a connector, `services/connectors/kstack`, run by the
engine, and `services/auth` is deleted. The wire does not change: the webview, the tray and the
host keep `authState`, `authLoginStart`, `authLogout` and the gRPC `AuthService`.

A user of an installed Kstack signs in once more after the update (§5).

## The answers to #1046

1. **One engine?** Yes. The engine has what `services/auth` lacks: a nonce on the ID token, the
   refresh token alone in the keyring, tokens registered with `safe.AddSecret`, a refresh under
   the service's own bound, a keyring check before the browser opens, goroutines joined on
   `Close`, and a failure the state carries. What would stay duplicated if only the small pieces
   were shared is the generation and ordering code, which is the part that drifts.
2. **Which way?** The Kstack sign-in moves onto `oauthconn`. Nothing reads its token today
   (`TokenSource` and `State.Tokens` have no caller outside `services/auth`), so it is the cheap
   side to move.
3. **The fixed client id.** `Provider` gains `ClientID`, and one method resolves the client
   (§2). The row's `client_id` stays NULL, as migration `0002`'s comment already says.
4. **What consumes the token?** Nothing yet. The connector exports no signer and hands out no
   token (§3). The cloud API, when it exists, is one API base, which is what the engine signs.
5. **The installed sign-in.** The `auth-token` keyring entry is deleted and the user signs in
   again (§5).
6. **Degraded mode and gRPC.** Degraded mode goes: the keyring service, issuer and client id all
   have defaults, and a machine with no keyring fails `NotSaved`. The gRPC and GraphQL surfaces
   stay, mapped from the connector's state (§4).
7. **The name and place.** `oauthconn` stays where it is. The Kstack sign-in is a connector, so
   it is under `services/connectors` and imports the engine like `chatgpt` does.

## Not in this spec

- **A consumer of the Kstack token.** No cloud API call is added.
- **A proactive refresh.** A session nothing signs with is never refreshed, as today.
- **The sign-in's phase and failure on the wire.** `AuthState` keeps its two fields (§4).
- **The Kstack account in the Connectors tab.** It stays the avatar and the tray's account menu.
- **The AWS and GitHub connectors.** §1's definition is written to admit them and builds neither.

## Design

### 0. Checks against the live issuer, first

No test reaches `oauth.kstack.sh`, so each is checked by hand once and its answer written here
with the date. **Tasks 4 and 5 wait until every row has one**: a wrong issuer, a missing endpoint
or a nonce that is not echoed fails every sign-in. An answer that differs from what a section
assumes changes that section in the same commit.

| Question | Why it matters | Answer |
| --- | --- | --- |
| What is `issuer` in `/.well-known/openid-configuration`, to the byte? | `oauth.Discover` refuses a document whose issuer is not the configured one exactly. `auth.go` says Hydra's `iss` ends in a slash; the default `--oauth-issuer` does not. If they differ, `sidecar/config.go`'s default and the host's `OAUTH_ISSUER` (`src-tauri/src/services/sidecar/service.rs`, with the test that asserts the arguments) become the document's value, in task 5. | Open |
| Does the document name `revocation_endpoint`, with every endpoint on the issuer's origin? | `Discover` refuses an endpoint on another origin, and `Disconnect` revokes. | Open |
| Does the ID token echo the authorize request's `nonce`? | The engine keeps no sign-in whose nonce does not match. | Open |
| Which code does the token endpoint answer for a revoked or expired refresh token? | It is the provider's one `SessionEnders` entry, expected `invalid_grant`. | Open |

### 1. What a connector is

**A connector is an account on a service that Kstack signs in to, and whose requests it may then
sign.** A model provider is one case; the Kstack account is one that signs nothing yet (§3). ChatGPT and OpenRouter have a catalog row and `SyncCatalog`
because their service is a model provider, not because they are connectors.

The surface every connector has is `New`, `State`, `Subscribe`, `Connect`, `Disconnect` and
`Close`, over `connectors`' vocabulary. `Cancel`, `SyncCatalog` and `llm.SessionSigner` belong to
the connectors that need them.

`oauthconn`'s `Provider.ID` is the row's key in `oauth_connections` and the prefix of the
keyring's accounts. For a model provider it is also the catalog's provider id.

### 2. The engine takes a fixed client

`Provider` gains one field:

```go
// ClientID is a client fixed in code. Set, the provider registers none:
// FirstClientID and ClientGone are unused and the row's client_id stays NULL.
ClientID string
```

One method answers the client a registration runs under, `cmp.Or(s.p.ClientID, reg.ClientID)`.
Its callers are the three places that read `reg.ClientID` today:

- `restore`: a session needs a client, the provider's or the row's.
- `authorizeURL`: a sign-in registers only when neither is set.
- `finish`: a fixed client skips `saveClientID`, and `issuedClient` accepts a redirect that names
  no client or the fixed one.

`State.Registered` stays "a saved client id", so it is false for a fixed client.

**The account's name is kept.** The avatar and the tray title the account by name before email.
Migration `0003` adds `name TEXT` to `oauth_connections`; the engine writes the verified ID
token's `name` beside `subject` and `email`, and `forgotten` drops it with them.
`connectors.State` gains `Subject` and `Name`, set when `Connected`, display only. ChatGPT's wire
type does not expose them.

**An empty `APIBaseURL` signs nothing.** `signer.Covers` answers false for a base with no host.
Today it matches a relative URL (`/anything`) against an empty base, since both have no scheme and
no host. No `resource` parameter is sent for an empty base, as now.

**A sign-in that could not start is answered, not only published.** The engine gains
`Start(ctx, fresh) (connectors.Failure, error)`: `Connect`'s body, also answering the failure of
the steps that run before it returns (the stores' check, discovery, the loopback, the browser).
`Connect` calls `Start` and drops the failure, so ChatGPT's mutation answers as it does now.

`Start` answers a failure only when it made the state `Failed` with it. A `Disconnect` or a
`Close` during those steps ends the attempt first; the step it cancelled then fails, and `Start`
answers no failure and no error, since the state already says what happened. `endAttempt` reports
whether it applied. Like `Connect`, `Start` runs its steps under the engine's context, not the
caller's.

### 3. `services/connectors/kstack`

```go
// ID is the row's key and the keyring accounts' prefix.
const ID = "kstack"

type Config struct {
	DB              *appdb.DB
	KeychainService string
	Issuer          string
	ClientID        string
}

func New(Config) (*Service, error)
func (s *Service) State() connectors.State
func (s *Service) Subscribe() (<-chan connectors.State, func())
func (s *Service) Connect(ctx context.Context) error
func (s *Service) Disconnect(ctx context.Context) error
func (s *Service) Close()
```

The provider:

| Field | Value |
| --- | --- |
| `ID` | `kstack` |
| `Issuer`, `ClientID` | `Config`'s, from `--oauth-issuer` and `--oauth-client-id` |
| `Scopes` | `openid`, `offline_access`, `email`, `profile` |
| `CallbackPath` | `/oauth/callback` |
| `SessionEnders` | §0's answer |
| `APIBaseURL`, `RequiredScope`, `FirstClientID`, `NewData`, `Authorize`, `ClientGone` | unset |

**The service holds the engine in an unexported field.** It exports no `Sign`, `Bind` or
`Refused`: the Kstack credential has no reader, and none can be added without a change to this
package.

**`Connect` ends an attempt under way, then signs in fresh**: `Cancel`, then the engine's
`Start(ctx, true)`. A user who closed the browser tab and asks again gets a new tab, as today.
Fresh, because the Kstack sign-in is how a user changes accounts, so no sign-in is held to the
last one's subject. `Connect` while `Connected` is refused `ErrConnected`.

**`Connect` runs one at a time.** The service holds a mutex of its own across `Cancel` and
`Start`, so two requests that overlap (the avatar's and the tray's) run in turn: the second waits
for the first's browser to open, ends that attempt and opens its own. The last request wins, as
when one follows the other, and `Connect` never answers `ErrSigningIn`. The wait is bounded by
`Start`'s own steps, the longest being discovery's `issuerTimeout`.

**`Connect` errs when the sign-in could not start.** A failure `Start` answers becomes an error
that names it (`kstack: the sign-in could not start: Unavailable`), so `authLoginStart` and the
gRPC `StartLogin` fail as they do today when no browser opens: the webview's error bus shows it
and the tray logs it. A failure after the browser opens is the state's alone, as today's is the
log's.

**`Subscribe` sends a state only when the account changes**: whether it is connected, and its
subject, email and name. The engine also publishes `SigningIn`, `Failed` and a cancelled attempt,
which the wire draws alike as signed out, and the tray rebuilds its menu on every frame. `State`
answers the engine's state whole.

**Where the tests are.** The engine's seams and its fake issuer are unexported, and stay so.

- The flow is tested in `oauthconn`, under a fixed-client test provider beside the registering
  one in `testutil_test.go`.
- `kstack` builds its service through an unexported constructor that takes the engine as a small
  interface (`State`, `Subscribe`, `Start`, `Cancel`, `Disconnect`, `Close`) and the keyring. Its
  tests fake both and cover what is its own: the old entry's delete (§5), the mutex, cancel then
  start, the error a failed start becomes, and `Subscribe`'s filter. `New` passes
  `oauthconn.New`'s service and `oskeyring.New`'s keyring.

*Acceptance* names each test's package.

The package has its own `CLAUDE.md` and `AGENTS.md`, listed in `sidecar/CLAUDE.md`'s *Guides*.

### 4. The wire stays

`AuthState` binds to `connectors.State` in `gqlgen.yml`, with resolvers for its two fields, as
`ChatGPTConnection` has:

- `authenticated` is `Phase == Connected`.
- `identity` is `{sub: Subject, email: Email, name: Name}` when connected, null otherwise.

`grpc/auth_server.go`'s `toAuthState` maps the same way. `graph.Resolver.Auth` and the gRPC
server take a small interface over the connector (`State`, `Subscribe`, `Connect`, `Disconnect`),
which their tests fake, as they fake `auth.Service` today. `authLoginStart` and `authLogout` pass
the connector's error through `connectorErr`, so `ErrConnected` is a validation error and a
sign-in that could not start is answered as it is. `proto/auth.proto` and
`graph/schema.graphqls` change only comments: the ones that name `auth.State`, and `Identity`'s,
which is restored from the row.

`app/runtime.go` builds the connector beside ChatGPT's and adds its `Close` to the stop sequence.

What a user or the host sees change:

| | Today | After |
| --- | --- | --- |
| Offline sign-in | The browser opens on a page that cannot load | No browser opens; the mutation and the RPC err `Unavailable` |
| A machine with no keyring | Found after the browser round-trip | No browser opens; the mutation and the RPC err `NotSaved` |
| A browser that will not open | The mutation and the RPC err | The same, naming `Refused` |
| Sign-out whose keyring delete fails | Errors and stays signed in | Signed out in this process at once; errors only when the row's write fails too. A row marked signed out reads as signed out at the next launch, whatever the keyring holds |
| Revocation | One attempt, on a goroutine nothing stops | Two attempts while the app runs, an unconfirmed one logged. `Close` cancels a revocation under way and waits for its goroutine to end, so a quit can still leave the token live at the issuer |
| A sign-in that fails after the browser opens | Logged | Logged, and on the connector's state; the wire still shows signed out |

### 5. The installed sign-in

`desktop/v0.0.1` keeps the whole token as JSON under the keyring account `auth-token`.
`docs/persisted-data.md` classes it disposable, and nothing reads the token.

`kstack.New` deletes `auth-token` on every launch, then builds the engine. The entry is not
converted and its refresh token is not revoked. The user is signed out after the update and signs
in again.

A failed delete is logged at `Info`, since a machine with no keyring fails it at every launch,
and tried again at the next. It fails nothing else: `New` still builds the engine, which restores
a session the new entry and the row hold, and a sign-in works. Until a delete succeeds
the old token is still in the keyring and still live at the issuer; no code reads it.

**A downgrade does not start.** `desktop/v0.0.1` carries migration `0001` alone, so `0002`, on
`main`, already makes `app.db` newer than it supports, and `sqlitemigrate.Apply` refuses it, as
`docs/persisted-data.md` says of every downgrade. `0003` changes nothing there. The name is a new
migration, not an edit of `0002`, because development databases have run `0002`.

### 6. What is deleted

- `services/auth`, whole, with its tests.
- `oauth.Client.Verify`, `oauth.ParseIdentityUnverified`, `UnverifiedIdentity` and `DisplayOnly`.
  `services/auth` is their only caller: the engine verifies with `VerifyNonce` and restores the
  account from the row.
- `gqlgen.yml`'s `Identity` binding to `auth.Identity`; it binds to `oauth.Identity`.

## Security

The record is `docs/security/<date>-the-kstack-sign-in-on-the-engine.md`, written in task 1,
before the code. It states:

- **The keyring holds the refresh token alone**, account `kstack-refresh-token`, as
  `{"v":1,"token","session"}`. The access and ID tokens are memory's. Before, all three were in
  the entry.
- **The ID token's nonce is checked.** Before, it was not sent.
- **No token is logged**: each is registered with `safe.AddSecret`.
- **The Kstack credential reaches no request.** The package exports no signer, the provider has
  no API base, and the engine signs nothing without one. A connector that hands credentials to
  tool runs never includes this one.
- **Sign-out is local first**, and a failed keyring delete no longer keeps the session (§4).
- **Revocation is not a promise.** It is tried twice while the app runs and cancelled by a quit,
  so a signed-out token can stay live at the issuer until it expires.
- **Residual risk.** The old entry's refresh token is not revoked, so it stays live at the issuer
  until it expires. Once the entry is deleted it is in no store; while the delete keeps failing
  it is still in the keyring, as reachable as before the update. A session nothing signs with is
  never refreshed, so the app can show an account whose refresh token the issuer has already
  expired.

`docs/security-model.md`: the *Sidecar → cloud* boundary names the connector. Two rows cite
`services/auth` and tests the cutover deletes, and each is rewritten to the engine's behaviour and
the tests that pin it: *A login completing after sign-out cannot restore the session*
(`TestLogoutBeatsAPendingLogin`, `TestSupersededLoginRevokesItsToken`,
`TestASecondLoginSupersedesTheFirst`) and *Sign-out clears the local credential*. The ChatGPT rows
the engine enforces say they hold for the Kstack sign-in too.

## Tasks

Three pull requests into `wip/kstack-connector`, each on the one before. Each updates the guides
and registers for its own change.

| PR | # | Task | Status |
| --- | --- | --- | --- |
| — | 0 | §0's live checks, every row answered. Blocks tasks 4 and 5 | Planned |
| A | 1 | The security record; the ADR (§1's definition, the Kstack sign-in as a connector, the old entry dropped); the `docs/TODO.md` entry points here | Done |
| A | 2 | `Provider.ClientID` and the one method that resolves the client; `Start`; `signer.Covers` refuses a base with no host; the flow's tests under a fixed-client provider | Done |
| A | 3 | Migration `0003`, its test and its `shippedMigrations` line; the row's `name`; `connectors.State`'s `Subject` and `Name` | Done |
| B | 4 | `connectors/kstack`: the provider, `Connect`, `Subscribe`'s filter, the old entry's delete, against a faked engine and keyring | Done |
| B | 5 | `runtime.go` wiring; `gqlgen.yml`, the resolvers and `grpc/auth_server.go` onto the connector; `services/auth` deleted; the issuer's default and the host's `OAUTH_ISSUER`, if §0 moves them | Done |
| C | 6 | §6's leftovers in `lib/oauth`; the docs listed under *When it lands* | Planned |

- **A, the engine.** Changes nothing a user sees: ChatGPT runs as before, with a name in its row.
- **B, the cutover.** One pull request, because the wire is served by one sign-in or the other.
- **C, the cleanup.**

## Acceptance

By hand, once, against the live issuer: sign in from the avatar and from the tray, quit, relaunch
offline, and the account shows with its name.

In `oauthconn`, under a fixed-client provider:

- A session survives a restart only when the row and the entry name it
  (`TestNewRestoresWhatTheStoresHold`).
- A sign-in writes no `client_id`, and a redirect naming another client is refused
  (`TestAFixedClientRegistersNothing`).
- A fresh sign-in as another account connects, with its name in the row and on the state
  (`TestAFreshSignInTakesAnotherAccount`).
- `Start` answers the failure, opens no browser and leaves the state `Failed` with it when
  discovery fails, when the keyring refuses writes, and when the browser will not open
  (`TestStartAnswersWhyItCouldNotStart`). `Connect` answers nil for the same three.
- A `Disconnect` during `Start`'s discovery leaves the state `SignedOut`, and `Start` answers no
  failure (`TestASignOutDuringAStartIsNotAFailure`, the discovery held by the fake issuer).
- An engine with no API base signs no request, absolute or relative
  (`TestNoAPIBaseSignsNothing`).
- `Close` cancels a sign-in and a revocation under way and returns only once their goroutines
  have ended. It does not wait for a revocation to succeed.
- No token appears in logs, `app.db` or any file (`TestNoTokenLeavesTheKeyring`).

In `connectors/internal/signer`: `Covers` answers false for a base with no host.

In `appdb`: a file `0002` built keeps its `oauth_connections` rows across `0003` and gains `name`
(`TestMigration0003AddsTheName`).

In `connectors/kstack`, over a faked engine and keyring:

- `Connect` cancels, then starts fresh (`TestConnectSupersedesAnAttempt`).
- Two `Connect` calls that overlap run in turn and both answer nil, neither `ErrSigningIn`
  (`TestOverlappingConnectsRunInTurn`, the first held inside the fake's `Start`).
- A failure `Start` answers becomes an error that names it; none becomes nil
  (`TestConnectErrsWhenTheSignInCannotStart`).
- `Subscribe` sends nothing for `SigningIn`, `Failed` or a cancelled attempt, and one frame for a
  sign-in and for a sign-out (`TestSubscribeSendsOnlyAccountChanges`).
- An `auth-token` entry is gone after `New` (`TestNewDeletesTheOldEntry`).
- With an `auth-token` entry the keyring will not delete, `New` still builds the engine and
  answers no error, and the next `New` tries the delete again
  (`TestAFailedDeleteOfTheOldEntryIsRetried`).

In `graph` and `grpc`, over a faked connector:

- `authState`, `authStateWatch` and the gRPC `AuthStateWatch` answer the same frames for signed
  out and signed in as before.
- `authLoginStart` and the gRPC `StartLogin` answer the error of a sign-in that could not start,
  and `authLoginStart` answers a validation error for `ErrConnected`.
- No token appears in a GraphQL response.

## When it lands

The `wip/kstack-connector` pull request checks that nothing in §1–§6 is left only here, and:

- `sidecar/CLAUDE.md`: *Auth / identity* is removed, *Connectors* states §1 and lists `kstack`.
- `sidecar/internal/run/CLAUDE.md`: the browser opener it names is the connectors'.
- `oauthconn/CLAUDE.md` and the package's doc comment: the engine signs in to a service, not only
  a model provider; a provider is a registered client or a fixed one; `Provider.ID`; a signer in
  `catalog.Config.Signers` and a row in `signedProviders` are a model provider's alone.
- `docs/persisted-data.md`: the sign-in token's row becomes the `kstack-refresh-token` entry;
  the `oauth_connections` row gains `name`, migration `0003` and the `kstack` row.
- `docs/security-model.md`, as *Security* lists.
- ADRs: the new one amends
  [local-first auth](../adr/2026-08-09-local-first-auth-settings.md),
  [an OAuth connection is a row](../adr/2026-11-18-an-oauth-connection-is-a-row-matched-to-its-keyring-entry.md)
  and [a connector is one package](../adr/2026-11-19-a-connector-is-one-package-and-its-machinery-is-internal.md),
  each marked `amended_by`.
- `docs/TODO.md`: the entries *Make the Kstack sign-in an `oauthconn` provider* and
  *`auth.StartLogin` outlives the stop sequence* are removed; *SSO failure didn't retry* and the
  proactive refresh are rewritten against the connector; a new entry removes the `auth-token`
  delete once no supported build wrote that entry.
- This spec is deleted.
