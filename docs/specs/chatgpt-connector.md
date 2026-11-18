---
title: The ChatGPT connector
scope: sidecar, webview
status: Planned
---

# The ChatGPT connector

**Answers:** [#1032](https://github.com/kstackhq/kstack/issues/1032). **Needs:** nothing unbuilt.
**Unblocks:** the cloud platforms (Bedrock, Vertex, Foundry), which sign through the seam §1 adds.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

The Connectors tab lists ChatGPT as *Coming soon*. After this spec, a user with a ChatGPT plan
connects it and runs Kstack's agent on OpenAI models billed to that plan, with no API key.

- **Sign-in is OpenAI's [Sign in with ChatGPT](https://developers.openai.com/siwc/token-sharing-open-source).**
  The sidecar runs the flow: system browser, PKCE, a loopback redirect. OpenAI registers an OAuth
  client for the user during the first sign-in, so Kstack ships no client id and no secret.
- **A provider's credential is a key or a signer.** `llm.Provider` gains a `Signer`, which
  authorizes each request. ChatGPT's signer is the sign-in service. The cloud platforms use the
  same seam later.
- **`chatgpt` is a provider of its own**, on the Responses wire at `https://api.openai.com/v1`.
  Its catalog is the account's `GET /v1/models`, read when the connection changes.
- **The tokens stay in the sidecar.** The refresh token lives in the OS keyring and the access
  token in memory. No token reaches a log, a GraphQL response, `app.db` or a file.
- **The ChatGPT row on the Connectors tab is real.** *Continue with ChatGPT* opens the browser,
  the connected row shows the account's email, and *Disconnect* signs out.
- **The user is told when their plan is in use**, where to manage it, and what to do at a limit.

Requests go to the public Responses API with the access token as the bearer. Kstack never calls
ChatGPT's `backend-api`.

**Nothing is built before the probe (§0) answers.** The route's documented tool shape is not the
one Kstack sends.

## What is not in this spec

- **The other connectors.** Their rows stay *Coming soon*. Only the signer (§1) is shared.
- **More than one ChatGPT account.** One registration and one session. *Use a different account*
  (§6) replaces the registration.
- **The account's plan on the row.** No documented claim states it. The row shows the email.
- **A usage summary, and the banner inviting users of another sign-in.** Kstack has no usage page
  and no other sign-in that reaches models. §6 ships the rest of OpenAI's
  [UI guidelines](https://developers.openai.com/siwc/ui-ux-guidelines).
- **Paid or hosted distribution.** Plan usage is open to open-source apps that run locally. If
  Kstack ships paid features, it applies to OpenAI before keeping this connector.
- **Running Codex as the harness.** Kstack's own loop runs the agent.

## Design

### 0. The probe comes first

OpenAI's [preview page](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
says to "group function/custom tools in namespaces or supply them through `additional_tools`
input items". The Responses wire sends plain function tools in top-level `tools`. Whether the
route takes them decides the wire's shape, so task 1 finds out before any code is written.

The probe needs a ChatGPT account and no Kstack code. OpenAI's
[devkit](https://github.com/openai/sign-in-with-chatgpt-devkit) signs in and saves an access
token. The requests are `curl`, each body built as `dialect_responses.go` builds it, with
`max_output_tokens` left out. The PR for task 1 records every request body, status and event
sequence, with tokens and account values removed.

| # | Question | How |
| --- | --- | --- |
| 1 | Does a whole tool round work with plain function tools? | Three requests. One offers a function tool with `strict: false` and gets a `function_call`. The next replays the reasoning item, the call and a `function_call_output`, and reaches `response.completed`. The third replays that whole history with a new user message. Run it twice, as below |
| 2 | Are `reasoning.summary: auto` and `include: reasoning.encrypted_content` taken? | On every request of 1 |
| 3 | What does `GET /v1/models` return beyond `slug`, `display_name` and `visibility`? | Save the body, ids only, as the decoder's fixture |
| 4 | Which listed ids does the `openai` row also have? | From 3 |
| 5 | How large is the refresh token? | Against the 2,560-byte keyring limit (§2) |
| 6 | Is a refresh before `earliest_refresh_at` refused? The field is in the token response, per OpenAI's [token reference](https://developers.openai.com/siwc/token-sharing-open-source/token-reference), which does not say what it binds | Refresh straight after sign-in |
| 7 | What does a `client_id` the user deleted in ChatGPT's settings answer? | At the token endpoint and on the authorize page |

**Probe 1 runs on both bodies the wire can send**, and the PR names the model and effort of each:

- **A model the `openai` row lacks**, with the fallback's body (§3): no `reasoning.effort`, and
  `include: ["reasoning.encrypted_content"]` and `reasoning.summary: auto` from the row's `Extra`.
  This is the common case, and the one a model with efforts would hide.
- **A model the `openai` row has**, if the account lists one, at one of its efforts: `reasoning`
  with `summary: auto`, and the same `include`.

**What each answer does:**

- **1 passes**: the wire is unchanged.
- **1 fails**: repeat it with the tools in one namespace, then through `additional_tools`. This
  spec is amended with the shape that worked before task 3 starts. The planned form is one line
  on the row, `Provider.ToolNamespace`, under which the Responses wire wraps its function tools
  on the way out and reads the call's bare name on the way in. The record keeps the app's tool
  names either way.
- **2 fails**: the refused field leaves the row's `Extra` and joins its `Omit`, and all of 1 runs
  again without it.
  Dropping `reasoning.encrypted_content` can break the replay of a tool round under
  `store: false`. If the round then fails, stop: the wire's replay needs a rule for this row, and
  this spec is amended with it.
- **3 states efforts or a context window**: the decoder reads them (§3).
- **5 is over the limit**: stop. The storage design (§2) is amended first.
- **6 is yes**: stop. A launch refreshes at once (§2), so the access token would have to outlive
  a restart. This spec is amended with a second keyring entry for it, and the rules that entry
  needs, before task 5 starts. `Sign` also waits for that time.
- **7** fixes how a dead registration is detected (§2, *How an attempt ends*).

**The gate lifts on the final payloads.** After every change an answer forces, the three requests
of 1 run once more on each of the two bodies, exactly as the wire will send them, and all reach
`response.completed`. That run is the one the PR records as passing.

### 1. A provider's credential is a key or a signer

`llm.Provider.Key` is fixed at launch. A token that expires needs a seam, and so does a request
signed over its body (SigV4). `llm/provider.go` gains:

```go
// Signer authorizes the requests of a provider whose credential is not a fixed key.
type Signer interface {
	// Sign sets r's authorization. r's context bounds any wait.
	Sign(r *http.Request) error
	// Refused is told the API answered 401 to a request Sign signed.
	Refused(r *http.Request)
}
```

- `Provider.Signer` is set, or `Provider.Key` is, never both. `llm.New` panics on a row with both.
- **Every wire applies it in one place.** `helpers.go` gains one middleware, added by
  `openAIOptions` and by the Messages wire's options. It calls `Sign`, sends, and calls `Refused`
  on a `401`. With a signer the SDK is given no key.
- **A signed request follows no redirect.** The middleware runs once, before `http.Client`, and
  Go resends `Authorization` to a redirect's target on the same domain or a subdomain. So a
  provider with a signer gets an `http.Client` whose `CheckRedirect` refuses every redirect, and
  a `3xx` fails the call as any other status does.
- **A turn is still never retried.** A `401` fails the model call. The next call signs afresh.
- **A `Sign` that fails is an `llm.Error` with no response.** `llm.ErrSignedOut` gives the kind
  `signed out`. Any other error gives `credential unavailable`, and its cause is logged. Neither
  holds a token.
- `llm` still names no vendor. The signer is a value on the row, as `Provider.fake` is.

**A target's signer is bound to the session it was resolved under.** A turn keeps its `Target`
across tool rounds, and the account can change while a tool runs. So:

```go
// SessionSigner is a Signer whose credential belongs to a session that can end.
type SessionSigner interface {
	Signer
	// Bind is a Signer for the current session alone: once that session ends it
	// fails with ErrSignedOut, whatever session has replaced it.
	Bind() Signer
}
```

- **`Resolve` binds.** The `Target` it returns holds `Bind()`'s signer, never the row's.
- **A binding made with no session is dead for good.** The catalog empties after the session
  ends, not with it, so `Resolve` can run signed out or during a sign-in. `Bind` then returns a
  signer that always fails `ErrSignedOut`, even if that sign-in goes on to succeed.
- **A subagent on its parent's provider keeps the parent's binding.** `agent/chat` resolves a
  subagent's model afresh; `Service.ResolveUnder(parent, …)` does that and carries the parent's
  bound signer when the provider is the same. A subagent on `chatgpt` under a parent on another
  provider binds when it is spawned: its conversation starts there.
- **Every other turn binds where it resolves**: a send, and a notice turn, which resolves its own
  target (`agent/chat/notices.go`). `RunMonitor` takes its `Target` from its caller, so a
  monitor's run is bound when that caller resolves.
- So a turn that began under one account never sends under another. Its next model call fails
  `signed out`, and the answer is stored as failed.

`dialect_test.go`'s conformance table runs every case under a signer as well as under a key.

### 2. Sign-in: `services/chatgptauth`

A flat package with a section in `sidecar/CLAUDE.md`. It owns the registration, the session and
the tokens, and implements `llm.Signer`.

```go
type Service interface {
	State() State
	Subscribe() (<-chan State, func())
	Connect(ctx context.Context, fresh bool) error // returns once the browser opens
	Cancel()
	Disconnect(ctx context.Context) error
	llm.Signer
}

type State struct {
	Phase      Phase   // SignedOut, SigningIn, Connected, Failed
	Session    uint64  // the generation this state belongs to
	Email      string  // display only; set when Connected
	Failure    Failure // why, when Failed
	Registered bool    // a saved client id exists
	// RevocationUnconfirmed is a revocation OpenAI did not confirm, of a sign-out or of
	// a sign-in that could not be saved. Cleared by the next Connect.
	RevocationUnconfirmed bool
}
```

**The protocol**, as OpenAI's docs and its reference
[`oauth.ts`](https://github.com/openai/sign-in-with-chatgpt-devkit/blob/0a36fefeb913055c8c7a1a29b63d82b96b2e841a/packages/local/src/oauth.ts)
have it:

| Step | Rule |
| --- | --- |
| Issuer | `https://auth.openai.com`, a constant. Endpoints come from `/.well-known/openid-configuration`, read on the first sign-in or refresh of a launch, never at startup. The document's `issuer` must equal the constant, and each endpoint's origin must be the issuer's |
| Host id | `urn:uuid:<v4>`, minted and saved before the first sign-in. Sent as `ext_agent_host_id` on every authorize request |
| Authorize | `response_type=code`, PKCE `S256`, a fresh `state` and `nonce`, `scope=openid profile email offline_access resource.invoke chatgpt.tokens.use.direct`, `resource=https://api.openai.com/v1`, `redirect_uri=http://127.0.0.1:<port>/auth/callback` |
| First sign-in | `client_id=dynamic_agent_client` and `agent_name_hint=Kstack` |
| Later sign-ins | The saved `client_id`, no `agent_name_hint`, and the saved email as `login_hint` |
| Callback | `state` is checked first, so a stray local request neither ends nor consumes the attempt. Exactly one `code`. A first sign-in must return an issued `client_id` that is not `dynamic_agent_client`. A later one may omit it, and a different one fails the attempt |
| Registration | The issued `client_id` is saved before the code exchange, so a failed exchange does not register a second client |
| Exchange | `grant_type=authorization_code` with the issued `client_id`, the code, the verifier, the same `redirect_uri` and `resource` |
| ID token | Verified against the JWKS: signature, issuer, audience (the issued `client_id`), expiry and nonce. Its `sub` is the account |
| Plan permission | The token response's `scope` must hold `chatgpt.tokens.use.direct`. Identity alone grants no plan usage |
| Refresh | `grant_type=refresh_token` with the saved `client_id`, the refresh token and `resource`, no `scope`. Every refresh returns a replacement refresh token |
| Sign-out | `POST` the revocation endpoint with the refresh token, `token_type_hint=refresh_token` and the `client_id`. The registration is kept |

**The issuer's client follows no redirect either.** Discovery, the JWKS, the token endpoint and
revocation go through one `http.Client` that refuses every redirect: a `307` or `308` would
resend a form holding a code or a refresh token. `auth/oauth`'s client refuses them for every
caller.

**The loopback port is whichever is free.** The listener binds `127.0.0.1:0` before the URL is
built. Only the port may vary: the host is `127.0.0.1`, never `localhost`, and the path is
`/auth/callback`.

#### Sessions and their races

**Every session has a generation.** One counter, under the service's one lock. `Connect`,
`Cancel`, `Disconnect` and a session's end each bump it and publish a state carrying the new
value. A code exchange, a refresh and a revocation each remember the generation they began
under.

**A completion applies only to its own generation.** Whatever finishes takes the lock, compares,
and only then writes the keyring, the file, memory and the state. The compare and the writes are
one critical section, so nothing publishes between them.

| What overlaps | What happens |
| --- | --- |
| `Connect` during an attempt | Refused. `Cancel` first |
| `Connect` while `Connected`, `fresh` or not | Refused. `Disconnect` first |
| `Cancel` before the callback | The listener closes. Nothing was issued, nothing is saved |
| `Cancel` or `Disconnect` during the exchange | The exchange finishes into a stale generation. Its tokens are revoked and never saved. The `client_id` the callback issued stays saved |
| `Disconnect` during a refresh | The refresh finishes into a stale generation. Its replacement tokens are revoked and never saved. Every `Sign` waiting on it fails `signed out` |
| `Connect(fresh)` after a `Disconnect`, while that late refresh or exchange still runs | The same: the late result belongs to no current generation and is revoked under the `client_id` it was issued to |
| A turn resolved under an ended session makes its next model call | `Sign` fails `signed out`, even when another account is connected by then (§1). Subagents, notice turns and monitors likewise |
| A session ends (refresh refused) during a `Disconnect` | Both clear the same entry. `Disconnect`'s state wins, since it bumps last |
| A catalog read finishes after its session ended | Dropped (§3) |

`Connect(fresh=true)` forgets the saved `client_id`, subject and email first, and keeps the host
id. It is the one way to change accounts.

**One attempt at a time.** `Connect` binds the listener, opens the browser and returns. The
exchange finishes on a service goroutine under a five-minute bound.

**How an attempt ends:**

| Outcome | State | What is kept |
| --- | --- | --- |
| Verified, plan permission granted | `Connected` | tokens, `client_id`, `sub`, email |
| `error=access_denied` | `Failed`, `Declined` | the `client_id`, if one was issued |
| No `chatgpt.tokens.use.direct` in the granted scopes | `Failed`, `PlanUsageNotGranted` | the `client_id`. The tokens are revoked, and the next `Connect` sends `prompt=consent` |
| A saved `sub` and a different verified `sub` | `Failed`, `AccountMismatch` | the saved registration. The new tokens are revoked |
| Five minutes pass | `Failed`, `TimedOut` | the `client_id`, if one was issued |
| Discovery, the exchange or the JWKS cannot be reached | `Failed`, `Unavailable` | the `client_id`, if one was issued |
| A write of the sign-in fails (*Storage*) | `Failed`, `NotSaved` | the `client_id`. The tokens are revoked |
| Anything else | `Failed`, `Refused` | the `client_id`, if one was issued |

#### Tokens

The access token lasts an hour and the refresh token thirty days, renewed by each refresh.

- **`Sign` answers the access token in memory while it is valid**, by `oauth.Token.Valid`'s
  leeway. Otherwise it refreshes first, so a launch that finds a session refreshes at its first
  `Sign`.
- **`Sign` signs only its resource.** The request must match the provider's base URL: the same
  scheme, the same host compared without case, the same effective port, and a path that is the
  base's or continues it after a `/`. `https://api.openai.com/v1x` and
  `https://api.openai.com.example/v1` are refused unsigned.
- **One refresh at a time.** Callers that arrive during one wait for its result. The refresh runs
  under the service's own context with a 30-second bound, never a caller's: a cancelled turn must
  not drop a token OpenAI has already rotated. A waiter stops waiting on its own context.
- **`Refused` drops the access token** it reads off the request, if it is still the current one. The
  next `Sign` refreshes.
- **A refresh the issuer refuses ends the session.** `invalid_grant`, `invalid_refresh_token`,
  `token_expired`, `refresh_token_expired`, `refresh_token_invalidated` and `refresh_token_reused`
  clear the session and publish `Failed` with `SessionEnded`. `invalid_client` does the same and
  forgets the registration, so the next `Connect` registers anew.
- **`Bind` captures the generation, and only of a session.** Under the lock, a phase other than
  `Connected` binds the dead signer (§1): an attempt's generation becomes its session's, so
  capturing it during `SigningIn` would come alive later. The bound signer's `Sign` compares the
  generation under the lock before it reads a token, and its `Refused` does nothing once it has
  moved.
- **A refresh that cannot reach the issuer clears nothing.** `Sign` fails, the turn fails with
  `credential unavailable`, and the next `Sign` tries again.
- **A refresh's ID token is not read.** The account is the sign-in's.
- **Every token is registered with `safe.AddSecret` as it is read or received**, and forgotten
  with `safe.ForgetSecret` once replaced or cleared.

#### Storage

| What | Where | Holds |
| --- | --- | --- |
| Registration | `<data>/chatgpt.json`, the package's own store (`registration.go`), `schemaVersion` 1 | `hostID`, `clientID`, `subject`, `email`, `connected`. No token |
| Refresh token | OS keyring, service `Config.KeychainService`, account `chatgpt-refresh-token` | the token, bare |

The access token is kept in memory alone. It lasts an hour and a refresh replaces it, so storing
it buys one request per launch. `app/paths.go` names the file, and `New` takes it and the keyring
service as arguments.

**Every write of the file is unconditional and synced.** Neither existing writer does that:
`atomicjson.Save` renames without syncing, and `jsonsettings.Update` skips a write whose value
equals the one it holds in memory, which is the very case a failed write leaves behind.

- **`lib/atomicjson` gains `SaveSynced`**: `Save`, with the temporary file synced before the
  rename and, on Unix, the directory synced after it. The directory sync is in a
  platform-named file, and its test beside it. `Save` and `SaveIndent` are unchanged.
- **`registration.go` writes through `SaveSynced` on every call**, with no comparison against
  memory. Memory changes only after the write returns.
- It reads the file as a preference: a newer `schemaVersion` is read, and a write keeps the keys
  this build does not know and the stamp it found.
- The writer is a field on the store, so a test fails a chosen write.

**`connected` in the file says whether a session exists; the keyring only holds it.** A sign-out
writes `connected: false` first. So a keyring entry left behind by a failed delete is never a
session.

**A sign-in starts by writing `connected: false`.** `Connect` writes it before it binds the
listener, whatever the file is believed to hold, and the write is synced to disk. If it fails,
`Connect` fails with `NotSaved` and no browser opens. A sign-out whose own write failed can leave
`connected: true` on disk; without this write, a crash after the new tokens were saved would restore
them at launch as a session, under the previous sign-in's `subject` and `email`.

**It then checks the keyring.** `Connect` writes and deletes a throwaway entry,
`chatgpt-keyring-check`, and fails with `NotSaved` if either does. A machine with no keyring (Linux
with no Secret Service) learns so before the user signs in, not after.

**The sign-in saves in three steps**, after the ID token and the plan permission are checked, in one
critical section under its generation:

| Step | If it fails |
| --- | --- |
| 1. Write the refresh token's entry | `Failed`, `NotSaved`. The new tokens are revoked |
| 2. Write the file: `subject`, `email`, `connected: true` | `Failed`, `NotSaved`. The entry is deleted and the new tokens are revoked |
| 3. Take the tokens into memory and publish `Connected` | — |

- **`Connected` is published only after step 2 lands.** No `Sign` answers before it.
- **A failed sign-in is not retried from what it holds.** The tokens are revoked, and the user
  presses *Continue with ChatGPT* again. The issued `client_id` stays saved.
- **A cleanup that fails too leaves no session.** An entry beside `connected: false` is never
  read as one, and the next launch deletes it. The revocation is attempted as `Disconnect`'s
  is. If it also fails, the tokens stay live at OpenAI until they expire, and the state carries
  `RevocationUnconfirmed`.
- A crash between steps 1 and 2 is the same case: the file says `connected: false`, and `New`
  deletes the entry. Nothing revokes those tokens; the refresh token expires in thirty days.

**A refresh that the issuer answered has spent the old refresh token.** From that moment:

1. Memory holds the replacement and never the old token again, whatever the writes do.
2. The refresh token's entry is written. **Until it lands, `Sign` hands out no access token**: it
   fails `credential unavailable`, and each later `Sign` retries the write, never the refresh. A
   session whose replacement never lands is lost with the process, and the next launch ends it
   (`refresh_token_reused`, `SessionEnded`).
3. `Sign` answers.

**Clearing a session**, on `Disconnect` or a session's end, in this order: memory and the
generation, `connected: false`, the refresh token's entry. Each step runs whatever the one before
did. `Disconnect` returns an error when the file write failed and the refresh token's entry is still
there: that session would return at the next launch.

**`New` reads both stores and reaches no network:**

| What it finds | What it does |
| --- | --- |
| `connected`, a `clientID` and a refresh token | `Connected`. The first `Sign` refreshes |
| `connected` and no refresh token | `SignedOut`. Writes `connected: false` |
| A refresh token and `connected: false` | `SignedOut`. Deletes it: a sign-in that crashed before its last write, or a delete that failed |
| A refresh token and no `clientID` | `SignedOut`. Deletes it: a refresh token is useless without its client |
| A file it cannot read | `SignedOut`, logged. The entry is left for the next launch that can read it |

**`Disconnect`** clears the session, publishes `SignedOut`, then revokes the refresh token under
a ten-second bound, once more on a network failure or a `5xx`. The revocation runs on a service
goroutine, and the mutation does not wait for it. One that OpenAI never confirmed sets
`RevocationUnconfirmed` on the state, if the generation still stands, so every window can say so
(§6). The registration and host id stay.

#### Shared with `services/auth`

Two moves keep `auth/oauth` vendor-neutral and stop the flow being written twice:

- **`services/auth/browser`**, a new leaf, takes `login.go`'s loopback server and browser opener:
  `browser.Listen(state, path)` whose `Wait` answers the callback's query, and `browser.Open(url)`.
  `auth` passes `/oauth/callback` and reads `code`.
- **`auth/oauth` gains what the standards define and Hydra ignores**: `Config.Resource`
  (RFC 8707), sent on the authorize, exchange and refresh requests when set; extra authorize
  parameters on `AuthCodeURL`; the nonce in `Verify`; `token_type_hint` on `Revoke`; the granted
  scopes on `Token`; and a typed error carrying the token endpoint's `error` code. `Refresh`
  posts its own form, since `x/oauth2`'s token source carries no extra parameter. `Token`'s
  comment stops saying the refresh token is static.

A first sign-in builds two `oauth.Client`s: one under `dynamic_agent_client` for the authorize
URL, and one under the issued id for the exchange, `Verify`, `Refresh` and `Revoke`.

### 3. The `chatgpt` provider

`agent/catalog/chatgpt.go` holds the row and its model decoder.

```go
llm.Provider{
	ID: "chatgpt", Label: "ChatGPT plan", Dialect: llm.DialectResponses,
	BaseURL: "https://api.openai.com/v1",
	Signer:  cfg.ChatGPT,
	Extra: map[string]any{
		"include":           []string{"reasoning.encrypted_content"},
		"reasoning.summary": "auto",
	},
	Omit:    []string{"max_output_tokens"},
	Codes:   chatGPTCodes,
}
```

- **Listed after the keyed providers, before the fake**, whenever `catalog.Config.ChatGPT` is
  set. Its catalog starts empty, so a signed-out launch shows no ChatGPT model.
- **Its tools are `ours`** (`lists["chatgpt"]`). A list is policy, so the security record names it.
- **`openai` and `chatgpt` stay two providers.** A payload replays only to the provider that wrote
  it (`stripForeign`), so a chat that moves between them replays as foreign.
- **Every request asks for the reasoning, effort or not.** The wire sends `include` and
  `reasoning.summary` only when a request names an effort. A model listed with no efforts still
  reasons. Under `store: false` its reasoning item replays only with the encrypted copy, which a
  tool round needs back, and its thinking shows only with a summary. So the row's `Extra` sets
  both on every request, and the wire is unchanged.
- **The route is a preview and its field list moves.** Every field it refuses goes in the row's
  `Omit`, never in the wire. Today the wire sends one refused field, `max_output_tokens`.
- **A debug build moves it** with `KSTACK_CHATGPT_BASE_URL` and `KSTACK_CHATGPT_ISSUER_URL`,
  through `applyEnvOverrides`. `docs/development-switches.md` lists both.

**Codes.** `llm.Error` keeps one code today and drops the rest as body text. `Provider.Codes` is
the codes a row's endpoint defines, which the OpenAI wires keep beside their own, on an HTTP error
and on `response.failed`. Each code states its class:

```go
// Codes is the error codes this provider's endpoint defines, and what each means.
Codes map[string]llm.CodeClass // CodeOther, CodeUsageLimit
```

ChatGPT's are the `subscription_sharing_*` codes of OpenAI's
[errors page](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery),
`chatpass_v2_scope_not_authorized` and `chatpass_v2_invalid_authorization_context`.
`subscription_sharing_usage_limit_exceeded` is `CodeUsageLimit`.

- `llm.Error.UsageLimit()` reads the class, as `ContextFull()` reads its code.
- `agent/chat`'s `runOutcome` stores a usage-limit failure as fixed text, *usage limit reached*,
  as it stores a context overflow. The model call's row keeps the code.
- OpenAI bills nothing elsewhere, and Kstack retries nothing.

**The catalog is the account's.** `GET <base>/models`, signed by the signer, through a client that
follows no redirect:

- The body is `{"models": [{"slug", "display_name", "visibility"}, …]}`. A model is listed when
  `visibility == "list"`, in the server's order: `slug` is the id and `display_name` the label.
  A body of another shape lists nothing.
- **Everything else about a model comes from the `openai` row's entry of the same id**, unless
  the probe found it in the body. An id that row lacks is the fallback: no efforts, `Tools: true`,
  `writtenCap` (the wire refuses a model with no cap, and `Omit` deletes the field), no context
  window and `CacheAuto`. OpenAI's examples name ids the row lacks, so expect most models here.
- **A fallback model shows its thinking** through the row's `Extra`, at the model's own default
  effort. The picker offers it no effort to choose.
- **A fallback model is never refused ahead.** With no context window, `agent/chat`'s
  `contextCeiling` is 0 and `roomFor` lets every send through. An overflow arrives as the
  provider's own error, which the wire classes as a context overflow where OpenAI codes it so.
- **It is read when the connection changes**: on `Connected`, at a launch that finds a stored
  session (off the path to `READY`), and on a poke. A read that fails is logged by failure kind
  and tried again at a paced interval while the session lasts. The interval is a parameter.
- **Any other phase empties the catalog.**

`app/chatgptcatalog.go` is the loop, and **the catalog's only writer**. It takes the service's
states in order on one goroutine. A read runs under a context the next state cancels, and its
result is set only if `State.Session` is still the one it began under. So a list read for one
account never outlives a frame for the next.

### 4. A catalog can change

`llm.Service` holds a fixed provider list, in picker order, and that stays. What changes is one
provider's models.

- **`Service.SetCatalog(providerID, models)`** replaces a held provider's catalog under a lock.
  `Providers`, `Provider` and `Resolve` read under it.
- **`Service.Subscribe()`** is a gauge of the providers, sent on each `SetCatalog`.
- A send naming a model that has gone is refused `ErrUnknownModel`, as today. A stored message
  still resolves its provider's label, since the provider is held with or without models.

### 5. GraphQL

```graphql
enum ChatGPTConnectionPhase { SignedOut SigningIn Connected Failed }

enum ChatGPTConnectionFailure {
  Declined PlanUsageNotGranted AccountMismatch TimedOut Unavailable NotSaved SessionEnded Refused
}

"The ChatGPT connector's state. No token is ever here."
type ChatGPTConnection {
  phase: ChatGPTConnectionPhase!
  "The account's email, from the ID token verified at sign-in. Display only. Null unless Connected."
  email: String
  "Why the last attempt or session ended. Null unless Failed."
  failure: ChatGPTConnectionFailure
  "Whether OpenAI has registered this installation, so Connect reuses its client."
  registered: Boolean!
  "Tokens this machine dropped, at a sign-out or a sign-in that could not be saved, which OpenAI did not confirm it revoked. False again at the next connect."
  revocationUnconfirmed: Boolean!
}

type Query        { chatGPTConnection: ChatGPTConnection! }
type Subscription { chatGPTConnectionWatch: ChatGPTConnection!  modelsWatch: [Model!]! }
type Mutation {
  "Open the browser on OpenAI's sign-in. Returns once it opens; the outcome arrives on the watch. `fresh` forgets the saved registration first. Refused during an attempt and while connected."
  chatGPTConnect(fresh: Boolean!): Boolean!
  chatGPTConnectCancel: Boolean!
  "Sign out on this machine, then ask OpenAI to end the session. Returns once this machine is signed out; an unconfirmed revocation arrives on the watch."
  chatGPTDisconnect: Boolean!
  "Open ChatGPT's usage settings in the browser. The address is the sidecar's constant."
  chatGPTUsageOpen: Boolean!
}
```

Both watches are gauges: current on subscribe, no `Bookmark`. `models` stays a query, and its
comment stops saying the set is fixed. A refused `Connect` is `KSTACK_VALIDATION_ERROR`.

`chatGPTUsageOpen` opens `https://chatgpt.com/settings/usage` through `browser.Open`. The webview
cannot open a link itself, and the mutation takes no address.

### 6. The webview

**`src/lib/connectors.tsx`**: `useConnectors()` folds `chatGPTConnectionWatch` into the ChatGPT
entry and returns its actions. The other entries stay placeholders.

**The Connectors tab** (`src/components/settings/settings-dialog/connectors.tsx`), by phase:

- `SignedOut`: the row in Available with **Continue with ChatGPT**, the label OpenAI's guidelines
  name.
- `SigningIn`: *Waiting for your browser…* and **Cancel**.
- `Connected`: the row in Connected, the email as its detail, and a menu with **Manage usage**
  and **Disconnect**.
- `Failed`: the row in Available, one line saying why, and the button again.
- `revocationUnconfirmed`: a line under the row. *Signed out on this computer. ChatGPT did not
  confirm it. To be sure, disconnect Kstack in ChatGPT's settings.* **Open ChatGPT settings** is
  `chatGPTUsageOpen`, the page that manages each app's access.
- When `registered` and not `Connected`, the row's menu offers **Use a different account**, which
  is `chatGPTConnect(fresh: true)`.
- Nothing draws before the watch's first frame.

**The email goes through `VisibleText`.** It is OpenAI's text.

**The first connection is confirmed once, in the window that started it.** The Connectors tab
that sent `chatGPTConnect` remembers that it did. When its phase becomes `Connected`, it reads
the key `chatgpt-plan-confirmed` from `localStorage` at that moment. If it is unset, it sets it
and shows a dialog: *You're using your ChatGPT plan. Requests Kstack sends on ChatGPT models use
your plan. You can manage usage in ChatGPT's settings.* with **Got it**. The read is direct,
through `storageKey`, not `usePersistedFlag`: that hook keeps the value it read at mount, and
another window may have set the key since. No other window shows the dialog, so nothing needs
live sync. A window closed before the sign-in finishes shows nothing, and the next first
connection does.

**The composer says when the plan is in use, without opening anything.** While the picked
model's provider is `chatgpt`, a line sits beside the closed model selector: *Using ChatGPT plan*
and **Manage usage**. It follows the pick, so it goes when the user picks another provider. The
picker's group heading is the provider's label, *ChatGPT plan*.

**A failed answer on `chatgpt` offers the way out.** Under the failure's text the transcript
draws **Manage usage**. At a limit the text is *usage limit reached*, and the line beside it
says the limit may be the plan's or the one set for Kstack.

**`src/lib/models.tsx`**: `useModels()` reads `modelsWatch`, so every window's picker follows a
connect or a disconnect. `loaded` is the first frame.

`design/components.json` gains or updates the entries these touch, as `design/CLAUDE.md` has it.

## Decisions

1. **The sidecar opens the browser, and no mutation carries a URL.** The issue proposed
   returning the authorize URL for the host to open. That needs a host command that opens a URL
   the webview supplies, which is a new capability for anything running in the webview.
   `services/auth` already opens the browser from the sidecar on a URL it built itself.
2. **No `id_token_hint`.** The ID token would ride the opener's command line, which other
   processes of the user can read. `login_hint` carries the email, as the reference client does.
   So the ID token is not stored at all.
3. **A sign-in without plan permission keeps no tokens.** OpenAI's docs suggest keeping the
   sign-in and marking plan usage off. Kstack has no other use for the sign-in.
4. **One keyring entry, and the file says whether it is a session.** The registration is not a
   credential and must outlive a sign-out. The keyring holds the refresh token alone: a stored
   access token saves one refresh per launch and costs a second entry that can disagree with the
   first. Probe 6 is the one answer that would bring it back.
5. **An unsaved replacement token is not used.** Using it would keep the turn alive and lose the
   session at the next crash without a sign. Failing `Sign` shows the keyring's fault while the
   user can still act on it.
6. **`Connect` while connected is refused.** Replacing a live session silently is how one
   account's catalog and another's token would meet.
7. **A turn is bound to its session, not cancelled with it.** Cancelling needs `agent/chat` to
   watch a connector. A bound signer fails the turn at its next model call with no new edge.
8. **A failed sign-in save revokes and starts over.** Holding live tokens to retry a write keeps
   a credential nothing has recorded.
9. **The provider is always held and its catalog comes and goes.** The picker's order stays fixed
   at launch, and `llm.Service` gains one setter, not a mutable provider list.
10. **A UUID host id**, not the JWK thumbprint OpenAI prefers. OpenAI verifies no key today, so a
    key pair would be a second secret that protects nothing.

## Tasks

All start Planned. Each lands as its own PR into `wip/chatgpt-connector`, in this order. **Task 1
gates the rest**: no later task starts until its answers are recorded and this spec says what
they changed.

| # | Task | Status | PR |
| --- | --- | --- | --- |
| 1 | The probe (§0): the seven answers, and this spec amended by them | Planned | — |
| 2 | The security record, the ADRs and the `docs/security-model.md` rows (*When it lands*), written before the code and corrected as the work moves | Planned | — |
| 3 | `llm.Signer`, `SessionSigner` and `ResolveUnder`, `Provider.Codes`, the middleware and the no-redirect client (§1, §3); the conformance table under a signer; the tool encoding, if the probe changed it | Planned | — |
| 4 | `services/auth/browser` and the `auth/oauth` additions (§2), with `services/auth` moved onto them and behaving as before | Planned | — |
| 5 | `atomicjson.SaveSynced`; `services/chatgptauth` (§2): registration, flow, generations, grant, storage, `Disconnect`; a fake issuer in its `testutil_test.go` | Planned | — |
| 6 | The `chatgpt` row, the model decoder, `SetCatalog`, the catalog loop, the usage-limit text and the runtime wiring (§3, §4) | Planned | — |
| 7 | The GraphQL surface (§5) | Planned | — |
| 8 | The webview (§6) | Planned | — |

**Tests.** A fake issuer serves discovery, the authorize redirect, the token endpoint with
rotation, the JWKS and revocation. The browser opener is a parameter, and the fake's "browser"
follows the authorize URL. The Responses endpoint is `llm`'s own wire fixture. The keyring is a
fake whose writes and deletes fail on request. Overlaps are made determinate by holding the fake
issuer's answer on a channel, never by sleeping.

- **Rotation.** A rotated refresh token is in the keyring before `Sign` answers. A service built
  over the same stores after a simulated crash signs with it.
- **One refresh.** Concurrent `Sign`s make one. A caller's cancel does not cancel it.
- **Storage faults on a refresh**, one test each: the refresh entry's write fails, then lands on
  a later `Sign`, and the old token is never sent again; the delete fails; and every row of
  `New`'s table.
- **Storage faults on a sign-in**, one test each: the keyring check, the refresh entry's write
  and the file's write. A failed check opens no browser. Each asserts the state, what the
  stores hold, what was revoked, and that `Connected` was never published before the file landed.
- **The registration's writes.** A write of a value equal to the one in memory still reaches
  the writer. `SaveSynced` syncs the file before the rename, pinned through its sync seam.
- **A sign-out that could not write.** `Disconnect`'s file write fails and its deletes land. A
  `Connect` whose first write also fails is refused and opens no browser. One whose first write
  lands, then crashes after step 1: a service built over the same stores is `SignedOut` and
  holds no entry.
- **A sign-in whose cleanup cannot revoke.** The state carries `revocationUnconfirmed`.
- **A binding with no session.** A target resolved while signed out, and one resolved during a
  sign-in that then succeeds: both fail `signed out` on every call.
- **A launch.** A service built over a stored session makes one refresh at its first `Sign`.
- **A fallback model's round.** A tool round on a model with no efforts sends `include` on both
  requests and replays the reasoning item whole.
- **A turn across accounts.** A turn on `chatgpt` pauses on a gated tool call. The account
  disconnects and a second one connects. The call is released: the turn's next model call fails
  `signed out`, and the fake API saw no request of that turn under the second account's token.
  The same for a subagent on `chatgpt`, a notice turn and a monitor's run.
- **Overlaps**, one test each: `Disconnect` during a refresh; `Cancel` and `Disconnect` during an
  exchange; `Connect(fresh)` while a late exchange finishes; a catalog read that finishes after a
  `Disconnect`, and after a connect to a second account. Each asserts the keyring, the file, the
  state and the catalog hold nothing of the stale session, and that the stale tokens were revoked.
- **Refusals.** `Connect` during an attempt and while connected.
- **Sign-out.** `Disconnect` revokes, clears the session, and a later `Connect` sends the same
  `client_id` and host id. A revocation the fake refuses twice sets `revocationUnconfirmed`.
- **Confinement** (`TestTheChatGPTBearerGoesNowhereElse`). `Sign` refuses each near-miss URL of
  §2. The fake API answers `302`, `307` and `308` to another host and to a subdomain, and no
  request reaches either. The fake issuer's token and revocation endpoints answer `307` and
  `308`, and no form is sent twice.
- **No leak** (`TestNoChatGPTTokenLeavesTheKeyring`). After a sign-in, a refresh and a turn, no
  token is in any log line, any GraphQL response, `app.db` or any file under the three
  directories.
- **A turn.** One with a tool call and its replay runs on `chatgpt` with no `OPENAI_API_KEY`.
- **The webview.** Each phase's row; the confirmation shown once and only by the window that
  connected; the composer's line with the selector closed; the failed answer's action; the
  unconfirmed-revocation line.

## When it lands

- **Security record**, `docs/security/<date>-the-chatgpt-connector.md`: a refresh token that
  spends the user's plan, kept by the sidecar. Where the tokens live, that they are registered
  with `safe` and never logged or projected, that the bearer goes only to `api.openai.com` and
  follows no redirect, that the webview never holds one, the new egress to `auth.openai.com`,
  `chatgpt`'s tool list, the email at rest in `chatgpt.json`, the sidecar opening one constant
  address for the webview, and that the keyring entry is as reachable as the sign-in token's.
- **`docs/security-model.md`**: the tokens join *Assets*; `auth.openai.com` joins the sidecar's
  egress boundary; one **Enforced** row per pinning test above.
- **ADRs**:
  - *The ChatGPT connector signs in with ChatGPT's dynamic registration*, over Codex's client and
    `~/.codex/auth.json`. It also records Decisions 1 to 8.
  - *A provider's credential is a key or a signer*. It supersedes [a cloud platform is auth on a
    vendor's wire](../adr/2026-10-17-a-cloud-platform-is-auth-on-a-vendors-wire.md), restating what
    stands with the signer in place of `Provider.Auth`. `llm/CLAUDE.md` repoints.
  - *A signed-in provider's catalog follows its connection*. It amends [a discovered model is listed
    on its catalog's word](../adr/2026-10-17-a-discovered-model-is-listed-on-its-catalogs-word.md),
    which gains `amended_by`. It records three things that ADR answers otherwise: discovery there
    runs once; `visibility == "list"` is taken as this catalog's word that a model can chat; and a
    listed model with no metadata of its own is admitted on the fallback, `Tools: true` included,
    where that ADR says absent metadata admits none.
- **`docs/persisted-data.md`**: a row for `chatgpt.json` (preference, `schemaVersion` 1, written
  synced), a row for the keyring entry (disposable: sign in again), the keyring line under *Where
  it lives*, and `kstack:chatgpt-plan-confirmed` on the pane-state row.
- **Guides**: `sidecar/CLAUDE.md` (a `chatgptauth` section, `auth/browser`, the `paths` tree);
  `llm/CLAUDE.md` (the signer and its binding, `Codes`, `SetCatalog`, the provider count, *Decided,
  not built*); `agent/CLAUDE.md` (the row, the decoder, the list, the usage-limit text);
  `src/components/settings/CLAUDE.md` (Connectors); `src/CLAUDE.md` and the chat guide for
  `useModels` and the composer's row; root `CLAUDE.md`'s stream kinds, for the two gauges.
- **`docs/development-switches.md`**: the two variables.
- **This spec is deleted.**

## Done when

- The probe's answers are recorded, and the wire sends the tool shape the route takes.
- A user with a Plus or Pro plan connects ChatGPT from the Connectors tab, picks one of the
  account's models and runs an agent turn with tool calls, with no `OPENAI_API_KEY` set.
- Restarting the app keeps the connection. A rotated refresh token survives a crash between the
  refresh and the next request.
- *Disconnect* revokes the refresh token and clears the session. A later connect reuses the same
  `client_id` and host id. A sign-out OpenAI did not confirm says so.
- No credential or catalog of an ended session returns, under any overlap the tests name, and no
  turn sends under an account it did not start with.
- The bearer reaches no host but `api.openai.com`, redirects included.
- No token appears in logs, GraphQL responses or `app.db`, and a test pins it.
- The user sees the confirmation once, *Using ChatGPT plan* beside the model selector, and
  *Manage usage* at a limit.
- Everything under *When it lands* is written.
- No test reaches OpenAI.
