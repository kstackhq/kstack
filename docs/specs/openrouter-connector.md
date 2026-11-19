---
title: The OpenRouter connector
scope: sidecar, webview
status: Planned
---

# The OpenRouter connector

**Answers:** [#1033](https://github.com/kstackhq/kstack/issues/1033). **Needs:** the ChatGPT
connector (#1038, on `main`): `llm.Signer`, `services/connectors/catalogsync`, and the settings
dialog's Connectors and Providers lists; and the OAuth leaves under `lib` (#1045, on `main`):
`lib/oauth` and `lib/oauth/browser`. **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today OpenRouter is reached only through `OPENROUTER_API_KEY`, read once per launch. The
Connectors tab lists it as *Coming soon*.

After this spec, **Connect** on the OpenRouter row opens OpenRouter's sign-in in the browser. The
sidecar gets back an ordinary OpenRouter API key (`sk-or-…`) labelled `Kstack`, keeps it in the
OS keyring, and the `openrouter` provider answers on the next turn, with no restart. **Disconnect**
deletes the local copy. An `OPENROUTER_API_KEY` from the environment still wins.

OpenRouter's flow is PKCE alone: no client registration, client id or secret
([OpenRouter's OAuth guide](https://openrouter.ai/docs/guides/overview/auth/oauth)):

1. Mint a PKCE verifier (32 random bytes, base64url) and its `S256` challenge.
2. Open `https://openrouter.ai/auth?callback_url=<url>&code_challenge=<c>&code_challenge_method=S256`
   (plus `key_label=Kstack`).
3. OpenRouter redirects to `callback_url` with `?code=`.
4. `POST https://openrouter.ai/api/v1/auth/keys` with `{"code", "code_verifier",
   "code_challenge_method": "S256"}` answers `{"key": "sk-or-…"}`.

The key does not expire, so there is no refresh, no discovery and no ID token. OpenRouter calls
this OAuth; what it grants is an API key, and the sidecar treats it as one (§1).

## Not in this spec

- A credit limit or expiry chosen in Kstack. The user sets both on OpenRouter's key settings.
- OpenRouter's model list. The row keeps its written catalog; discovery is the general work in
  `internal/llm/CLAUDE.md`, *Decided, not built*.
- Revoking the key from Kstack (§7).
- The other connectors.

## Design

### 0. Checks against the live service, first

No test reaches OpenRouter, so these are checked by hand once, against a real account, and each
answer is written into the table with the date. **Tasks 4 to 7 wait until every row has one**:
the answers fix the callback address, where `state` goes, when a key is dropped, and what
Disconnect tells the user. An answer that differs from what §2 or §7 assumes changes that section in the same commit.

| Question | Why it matters | Answer |
| --- | --- | --- |
| Does a `callback_url` of `http://127.0.0.1:<random port>/…` work? | Some clients pin `localhost:3000`. | Open |
| If a random port is refused: exactly which host (`127.0.0.1` or `localhost`) and which ports are accepted? | The listener then binds those ports in order, on that host. A fixed port is used only once it is seen to work. | Open |
| Does OpenRouter keep a query already on `callback_url` when it appends `code`? | Decides where our `state` goes (§2). | Open |
| What does the redirect carry when the user declines, if anything? | Decides whether `Declined` is a failure the row can name, or only a timeout. | Open |
| Is the parameter `key_label`, and does the key list show it? | The user finds and revokes the key by it. | Open |
| Can a user key delete itself? | Expected no (deleting takes a provisioning key). Confirms the Disconnect text (§7). | Open |
| Does `GET /api/v1/key` answer a live key `200` and a deleted one `401`? | The check that confirms a key is dead before it is dropped (§5). | Open |

### 1. A key service of its own: `services/connectors/openrouterkey`

**The connectors are two kinds, and each package says which it is.**

| Kind | What a sign-in yields | Package |
| --- | --- | --- |
| An OAuth session | A refresh token that rotates, under a client, with discovery, an ID token and revocation | `oauthconn`, one `Provider` value per provider (`chatgptauth`) |
| A granted key | One API key that does not expire | A service per provider (`openrouterkey`) |

`oauthconn` stays the engine of every OAuth session, and the Kstack sign-in is still to join it
(`docs/TODO.md`). OpenRouter is not one: widening `oauthconn.Provider` to a flow with no issuer, no
client and no refresh token would put a branch on every path it has. A new ADR records the two
kinds. [The OAuth connection ADR](../adr/2026-11-18-an-oauth-connection-is-a-row-matched-to-its-keyring-entry.md)
stands: its one engine runs every OAuth sign-in.

**What both kinds use moves out of `oauthconn` first**, in a change of its own that alters no
behaviour (tasks 2 and 3):

- `lib/oskeyring`: the `Keyring` interface, its OS implementation, `ErrNoEntry` and the
  write-and-delete check, from `oauthconn/keyring.go`. `oauthconn` keeps its accounts and its
  entry format.
- `catalogsync.Start` takes a subscription to the connected session (`oauthconn`'s
  `WatchSession`), not an `*oauthconn.Service` (§5).

Nothing outside `oauthconn`, `chatgptauth`, the ChatGPT wiring in `app/runtime.go` and the ChatGPT
resolvers then imports `oauthconn`. Its guide says so: a new **OAuth** provider is a `Provider`
value; a provider whose sign-in grants a key is a service beside `openrouterkey`.

`openrouterkey` uses `lib/oauth/browser` (the loopback listener and the opener) and
`lib/oskeyring`. It does not import `lib/oauth`: it has no token endpoint, and the PKCE verifier
and challenge are `x/oauth2`'s `GenerateVerifier` and `S256ChallengeFromVerifier`, as `oauthconn`
mints its own. It has its own `CLAUDE.md` and `AGENTS.md`, listed in `sidecar/CLAUDE.md`'s
*Guides*.

Its surface has the shape of `oauthconn.Service`'s, so the wiring and the resolvers read alike.
The types are its own:

```go
type Phase string // SignedOut, SigningIn, Connected, FromEnvironment, Failed

type Failure string // Declined (if §0 allows), TimedOut, Unavailable, Refused, NotSaved, NotDeleted, KeyRejected

// State is the connector as every window draws it. It holds no key.
type State struct {
	Phase   Phase
	Session uint64 // the generation this state belongs to
	Failure Failure
}

func New(Config) (*Service, error)
func (s *Service) State() State
func (s *Service) Subscribe() (<-chan State, func())
func (s *Service) Connect(ctx context.Context) error // ErrSigningIn, ErrConnected, ErrFromEnvironment
func (s *Service) Cancel()
func (s *Service) Disconnect() error // the keyring's error when the entry is not deleted (§7)
func (s *Service) Close()

// llm.SessionSigner
func (s *Service) Sign(r *http.Request) error
func (s *Service) Refused(r *http.Request)
func (s *Service) Bind() llm.Signer
```

`Config` holds the production knobs alone: the keyring service, the auth origin and the API base
(constants, each moved only by a debug build, §5), and whether the environment holds a key. Test seams are unexported options.

**Every change runs under one lock against one generation**: `Connect`, `Cancel`, `Disconnect` and
a rejected key bump it, and an exchange that finishes applies only to the generation it began
under. A key that arrives late is dropped and never saved; it cannot be revoked (§7, residual
risk).

### 2. The sign-in

`Connect`:

1. Refused while signing in, while connected, while the environment holds a key, and in
   `Failed{NotDeleted}`, whose entry only a Disconnect clears (§7).
2. Checks the keyring before any browser opens: `oskeyring.Check` writes and deletes a throwaway
   entry under `openrouter-keyring-check`. A failure is `NotSaved`, and no browser opens, so a
   machine with no keyring never has a key issued that it cannot keep.
3. Mints the verifier, its challenge and a random `state`; registers all three with
   `safe.AddSecret`.
4. Binds `127.0.0.1` on a free port (`browser.Listen`), with `state` in `callback_url`.
   **Where `state` goes follows §0:** in the query if OpenRouter keeps it, which `browser.Listen`
   checks today; otherwise in the callback's path (`/callback/<state>`). `Listen` already takes
   the path, and gains a way to check the state there in place of the query's. OpenRouter's flow
   has no `state` of its own; ours is what ties the callback to this sign-in.
5. Opens the auth page in the system browser and returns. The outcome arrives on the watch.
6. Waits up to five minutes for the callback (below). No callback is `TimedOut`.
7. Exchanges the code (`issuerTimeout`, 30 s), with an `http.Client` that follows no redirect, so
   the code and verifier are posted once and only to `openrouter.ai` (below).
8. Registers the key with `safe.AddSecret` before anything else touches it, writes the keyring
   entry, then publishes `Connected`. A failed keyring write is `NotSaved`, and the key is dropped.

**The callback.** Only a request carrying our `state` can end the attempt:

| Request to the listener | Outcome |
| --- | --- |
| Wrong or missing `state` | `400`; the attempt stays pending, as `browser.Listen` does today. A stray local request can neither finish nor abort a sign-in. |
| Our `state`, no `code` or an empty one, no `error` | `400`; the attempt stays pending. |
| Our `state` and an `error` | The attempt fails: `Declined` if §0 shows this is what a decline sends, otherwise `Refused`. |
| Our `state` and more than one `code` | The attempt fails `Refused`; nothing is exchanged. |
| Our `state` and exactly one `code` | The code is exchanged. |
| Any request after the first that ended the attempt | Dropped; the listener answers once. |

**The exchange's answer.** Only a `200` whose body is JSON with a non-empty string `key` connects.
Every other answer saves no entry and never publishes `Connected`:

| Answer | Failure |
| --- | --- |
| No answer within `issuerTimeout`, or a transport error | `Unavailable` |
| `3xx` (not followed) | `Unavailable` |
| `4xx` | `Refused` |
| `5xx` | `Unavailable` |
| `200` with a body that is not JSON, or has no `key`, or an empty one | `Unavailable` |

The body is read up to a fixed bound. The key's form is not checked beyond non-empty: OpenRouter
owns its prefix.

### 3. Storage: the keyring alone

The key is one entry: service `Config.KeychainService`, account `openrouter-api-key`, value
`{"v":1,"key":"sk-or-…"}`. There is no `app.db` row. The OAuth connection ADR pairs a row with an
entry because a registration must outlive a sign-out; OpenRouter has no registration, and the label
is our constant, so one store says it all: **an entry present is connected.**

- `New` reads the entry and reaches no network. A readable entry is `Connected`. A locked keyring
  is `SignedOut`, writing nothing (no proof of a sign-out).
- An entry with a newer `v` is `SignedOut` and **left in place**: a newer Kstack wrote it and can
  read it again, and deleting it would strand a live key on OpenRouter. A later Connect on this
  version replaces it.
- An entry that does not parse is `SignedOut` and deleted.
- With an environment key (§4), `New` does not read the entry at all and the key stays where it is.

`docs/persisted-data.md` gains the entry's row, pinned by `TestNewRestoresWhatTheKeyringHolds`.

### 4. The environment key wins

`config.go` already reads `OPENROUTER_API_KEY` from the launch and the login shell. When it holds
one:

- The `openrouter` row carries that key, as today.
- The service starts in `FromEnvironment`, and `Connect` and `Disconnect` are refused
  (`ErrFromEnvironment`, `KSTACK_VALIDATION_ERROR` on the wire).
- A stored key is left in the keyring, unread. It returns on a launch with no environment key.

This keeps one rule for every keyed provider: what the launch sets wins. The security record
states it.

### 5. The `openrouter` row follows the connection

`llm.Service` holds a fixed provider list in picker order, so the row must exist from launch even
when nothing is connected yet.

**The row keeps its place among the keyed providers.** `catalog.Config` gains the OpenRouter
connector's signer. In `catalog.New`, a keyed provider whose key is absent but whose connector
signer is given is listed in its place with the signer and an empty catalog. So `keyedProvider`
gains an optional connector id, set on OpenRouter's entry alone. The picker order does not move
between the two sources.

**The key signs as a session signer.** The
[credential ADR](../adr/2026-11-18-a-providers-credential-is-a-key-or-a-signer.md) refused "a key
as one more `Signer`" because a key's header follows the model's dialect. OpenRouter's row is one
dialect (Chat Completions, a bearer), and no entry overrides it, so `Sign` sets
`Authorization: Bearer <key>` and is correct. `catalog_test.go` pins that no OpenRouter entry
states a dialect or base URL of its own. The new ADR records the exception. `Sign`:

- refuses a URL that is not the API base or under it, a check of its own beside `oauthconn`'s
  `covers` (`TestTheKeyGoesNowhereElse`);
- fails `llm.ErrSignedOut` outside `Connected`.

`Bind` is a signer for the current generation, dead once it ends, so a turn never sends a key it
did not start with. A `401` under an older generation is ignored.

**A `401` is confirmed before the key is dropped.** Dropping a key cannot be undone from Kstack:
it stays live on OpenRouter, and connecting again issues a second one. So `Refused` (a `401` under
the current generation) starts one check, `GET /api/v1/key` under the key, on the exchange's
client and bound (one at a time; a `401` while a check runs joins it):

| Check's answer | Outcome |
| --- | --- |
| `401` | The key was deleted on OpenRouter. The entry is deleted and the state is `Failed{KeyRejected}`. |
| `200` | The key is live; the `401` was not about it. Nothing changes. |
| Anything else, or no answer | Nothing changes. The next `401` checks again. |

The check reads the status alone, never the body.

**The catalog follows the connection.** `catalogsync` is the only writer of a signed provider's
catalog. `Start` takes a stream of the connected session (`uint64`, `0` for none), and each
connector maps its own state onto it (§1). OpenRouter's reader
answers the row's written catalog and reaches no network, so a poke or the six-hour refresh sets
the same list and `SetCatalog` sends nothing. `Connected` fills the picker; any other phase
empties it. With an environment key, `catalogsync` is not started for OpenRouter and the row keeps
its written catalog.

`provider.data_collection: deny` stays on the row's `Extra`, so it rides every request whichever
key signs it.

**Debug builds** move two things, in `applyEnvOverrides`, as ChatGPT's are moved
(`movedProvider`). `KSTACK_OPENROUTER_AUTH_URL` is the auth origin, `https://openrouter.ai`
in a release: the auth page is its `/auth`, and the exchange its `/api/v1/auth/keys`. The existing
`KSTACK_OPENROUTER_BASE_URL` moves the API base, and with it what `Sign` covers and the key check
(`<base>/key`). A release reads neither.

### 6. Wiring and GraphQL

`app/runtime.go` builds the service after the ChatGPT one and before the catalog, adds it to the
runtime, passes it to `newCatalog`, and starts its `catalogsync` unless `FromEnvironment`.

The wire follows ChatGPT's, so the webview's hooks have one shape:

```graphql
enum OpenRouterConnectionPhase { SignedOut SigningIn Connected FromEnvironment Failed }
enum OpenRouterConnectionFailure { Declined TimedOut Unavailable Refused NotSaved NotDeleted KeyRejected }

"The OpenRouter connector's state. No key is ever here."
type OpenRouterConnection {
  phase: OpenRouterConnectionPhase!
  failure: OpenRouterConnectionFailure
}

extend type Query {
  "The OpenRouter connector's state; live updates via openRouterConnectionWatch."
  openRouterConnection: OpenRouterConnection!
}

extend type Mutation {
  "Open the browser on OpenRouter's sign-in. Returns once it opens; the outcome arrives on openRouterConnectionWatch. Refused (KSTACK_VALIDATION_ERROR) during an attempt, while connected, and while the environment holds a key."
  openRouterConnect: Boolean!
  openRouterConnectCancel: Boolean!
  "Delete the key on this machine. The key stays live on OpenRouter until the user deletes it there. Errors when the key could not be deleted; the state is then Failed with NotDeleted, and calling again retries."
  openRouterDisconnect: Boolean!
  "Open OpenRouter's key settings in the browser. The address is the sidecar's constant."
  openRouterKeysOpen: Boolean!
}

extend type Subscription {
  "GAUGE. The OpenRouter connector's state, a fresh value on every change."
  openRouterConnectionWatch: OpenRouterConnection!
}
```

The issue asked for a sign-in mutation that returns the authorize URL. ChatGPT's settled the
other way: the sidecar opens the browser and the webview opens no URL. This follows ChatGPT.
`OpenRouterConnection` binds to `openrouterkey.State`, which holds no key. Root `CLAUDE.md`
lists `openRouterConnectionWatch` among the gauges.

### 7. Disconnect

A user key cannot delete itself (to confirm in §0; deleting takes a provisioning key). So
**Disconnect deletes the local copy and says where to revoke**: the confirm reads *Kstack will
forget this key. It stays active on OpenRouter until you delete the key labelled "Kstack" in your
OpenRouter settings*, with **Open OpenRouter settings** (`openRouterKeysOpen`).

`Disconnect` bumps the generation and drops the key from memory first, so every binding is dead
whatever the keyring answers. Then it deletes the entry:

- **Deleted:** `SignedOut`, and `Disconnect` returns nil.
- **Not deleted:** `Failed{NotDeleted}`, and `Disconnect` returns the error, which the mutation
  returns too. The process signs nothing more with the key, but the entry is still there.

**A failed delete is never reported as a sign-out.** `NotDeleted` stays until it is resolved:
`Disconnect` is allowed in it and tries the delete again, and the row offers **Retry** and **Open
OpenRouter settings** beside the line *Kstack could not remove the key from this machine. It will
be used again after a restart. Retry, or delete the key labelled "Kstack" on OpenRouter.* A
restart in this state reads the entry and is `Connected`, as the line says. There is no sign-out
marker in another store: the only sign-out is a deleted entry, and the user is told when there is
none.

### 8. The webview

- `src/lib/connectors.tsx`: the OpenRouter entry folds `openRouterConnectionWatch`, beside
  ChatGPT's, with `openrouter.actions` (connect, cancel, disconnect, open keys). The pane draws
  nothing until both watches have their first frame.
- `connectors.tsx`: an `OpenRouterRow`. Signed out: **Connect**. Signing in: *Waiting for the
  browser…* and **Cancel**. Connected: *Key "Kstack"* and a menu with **Disconnect** (§7's
  confirm) and **Open OpenRouter settings**. From the environment: *Using `OPENROUTER_API_KEY`*,
  no menu. Failed: the failure's line and **Connect**; for `NotDeleted`, §7's line with **Retry**
  and **Open OpenRouter settings**. No first-connection confirm: an API key
  spends credits the user bought for that purpose, unlike a plan.
- `provider-setup-form.tsx`: an `openrouter-connector` source on OpenRouter's dialog, which selects
  the Connectors tab. It is the form's first real connector source; ChatGPT's stays *Coming soon*
  until its own row is wired. `providers.tsx`'s `CONNECTOR_SOURCES` gains it.
- `providers.tsx`'s row says where OpenRouter's key comes from. `Detail` prints *Found
  `OPENROUTER_API_KEY`* for any served provider with a key variable, which is wrong once the
  connector serves it. OpenRouter's detail reads the connector's phase from `useConnectors()`:
  `Connected` is *Connected on the Connectors tab*, `FromEnvironment` keeps *Found
  `OPENROUTER_API_KEY`*. Every other provider's detail is unchanged.
- `src/components/settings/CLAUDE.md`: Connectors has two real rows.

## Security

The record is `docs/security/<date>-the-openrouter-connector.md`, written in task 1, before the
code. It states:

- **The key is in the OS keyring**, account `openrouter-api-key`, as reachable as the ChatGPT
  refresh token: any process of the user can ask for it; a sandboxed command cannot reach the
  keyring.
- **No key, code or verifier is logged, projected, stored in `app.db` or written to a file.** Each
  is registered with `safe.AddSecret` on receipt and forgotten when cleared.
  `TestNoKeyLeavesTheKeyring` reads the raw log and every file under the data directory after a
  sign-in, a turn and a disconnect.
- **The key goes to `openrouter.ai/api/v1` alone, and follows no redirect** (`Sign`'s check, the
  wires' `noRedirectClient`, the exchange's client).
- **The callback is checked**: our `state` before anything else, exactly one `code`, on the
  listener this sign-in started, bound to `127.0.0.1`.
- **The webview holds no key and opens no URL.** The sidecar opens the auth page it built and the
  key settings page, a constant.
- **The environment key wins**, and a stored key is then never read.
- **The keyring is checked before the browser opens**, so a machine that cannot keep a key is not
  issued one.
- **A failed delete is not a sign-out.** The process stops using the key at once, `Disconnect`
  errors, and the row says the key returns on restart until the delete succeeds.
- **Residual risk.** A key OpenRouter issued after a Cancel, or one whose keyring write failed
  after the check passed, is live on the account and in no store; the user deletes it by its
  label. A disconnected key stays live until the user deletes it. A key whose delete failed is
  used again after a restart.

`docs/security-model.md` gains a row for the connector key.

## Tasks

The work is three pull requests into `wip/openrouter-connector`, each on the one before. Each
updates the guides and registers its own code changes, in the same change.

| PR | # | Task | Status |
| --- | --- | --- | --- |
| — | 0 | §0's live checks, every row answered. Blocks tasks 4 to 7 | Planned |
| A | 2 | `lib/oskeyring` out of `oauthconn`, with its tests | Planned |
| A | 3 | `catalogsync` takes a session stream; ChatGPT maps onto it unchanged | Planned |
| B | 1 | Security record; the ADR (two kinds of connector, the key as a session signer, one store); `docs/TODO.md` line | Planned |
| B | 4 | `openrouterkey`: state, keyring, `Connect`/`Cancel`/`Disconnect`, `Sign`/`Bind`/`Refused` and the key check, against a fake auth page and exchange; `browser.Listen`'s state-in-path check if §0 needs it | Planned |
| B | 5 | `catalog`: the connector signer on OpenRouter's keyed row; `runtime.go` wiring; `config.go`'s debug variable | Planned |
| B | 6 | GraphQL schema, resolvers and tests; `pnpm codegen` | Planned |
| C | 7 | The webview: hook, row, setup source, the Providers tab's detail, tests | Planned |

- **A, the shared parts.** Changes no behaviour and needs no §0 answer, so it can land first.
  Guides: `oauthconn`'s states its kind; `sidecar/CLAUDE.md`'s *catalogsync* section.
- **B, the sidecar.** Opens with the security record and the ADR, so the review reads them before
  the code. Guides and registers: `openrouterkey`'s `CLAUDE.md` and `AGENTS.md`,
  `sidecar/CLAUDE.md`'s *Guides*, `internal/agent/CLAUDE.md`, root `CLAUDE.md`'s gauges,
  `persisted-data.md`, `security-model.md`.
- **C, the webview.** Guide: `src/components/settings/CLAUDE.md`.

## Acceptance

- With no `OPENROUTER_API_KEY`, Connect, finish the sign-in in the browser, and run an agent turn
  on an OpenRouter model without restarting (by hand, once).
- The key survives a restart (`TestNewRestoresWhatTheKeyringHolds`).
- Disconnect deletes the entry, empties the row's catalog, and a turn bound before it fails
  `signed out` (`TestABindingEndsWithItsSession`).
- A `401` drops the key and shows `KeyRejected` only when `GET /key` answers `401` too; a `200`
  or no answer keeps it (`TestARejectedKeyIsConfirmedFirst`).
- Connect is refused in `NotDeleted` (`TestAFailedDeleteIsNotASignOut`).
- With a connected key, the Providers tab names the connector, not the environment variable.
- With a keyring that refuses writes, Connect fails `NotSaved` and opens no browser
  (`TestNoBrowserOpensWithoutAKeyring`).
- A Disconnect whose delete fails errors, shows `NotDeleted`, kills every binding, and succeeds on
  a retry (`TestAFailedDeleteIsNotASignOut`).
- An entry with a newer `v` is left in the keyring (`TestANewerEntryIsLeft`).
- No callback without our `state` ends an attempt, and no exchange answer but a `200` with a key
  connects (`TestTheCallbackTable`, `TestTheExchangeTable`).
- With `OPENROUTER_API_KEY` set, the row uses it, the state is `FromEnvironment`, and Connect is
  refused (`TestTheEnvironmentKeyWins`).
- No key appears in logs, GraphQL responses, `app.db` or any file (`TestNoKeyLeavesTheKeyring`,
  the resolver tests).
- Every test uses a fake auth page and exchange; none reaches OpenRouter.

## When it lands

The guides already say what each pull request built. The `wip/openrouter-connector` pull request
checks that nothing in §1–§8 is left only here, marks the connector-key row in
`security-model.md` **Enforced**, pinned by the tests above, and deletes this spec.
