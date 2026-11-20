---
title: Saved provider API keys
scope: sidecar, webview
status: Planned
---

# Saved provider API keys

**Answers:** [#1077](https://github.com/kstackhq/kstack/issues/1077). **Needs:** the OpenRouter
connector, the one connector vocabulary and the Google connector (#1074), all on `main`.
**Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a keyed provider's API key reaches Kstack only through its environment variable, read once
per launch (`sidecar/config.go`, `readProviders`, and the login shell). The Providers pane draws a
*Saved key* radio with a disabled input and a disabled *Save*, and guesses each row's state from a
hard-coded list.

After this spec:

- A key typed into the Providers pane is saved in the OS keyring and signs that provider's
  requests at once, in every window, with no restart. *Remove key* deletes it.
- **A provider's credential follows one order, lowest to highest: environment key < saved key <
  connector.** The highest one present signs. The order is per provider row: a connector is in a
  row's order only when it signs that row's own endpoint, which today is OpenRouter's alone.
- Every keyed row is in `llm.Service` from launch, signed by one new service, and lists its
  written catalog only while it has a credential.
- The OpenRouter connector loses its `FromEnvironment` phase: it can connect whatever keys exist,
  and signs while connected.
- The `google` row (Vertex AI) is listed whatever Gemini key exists. `gemini` and `google` are
  separate providers, as `anthropic` and Bedrock are, so `GEMINI_API_KEY` no longer hides it.
- The pane reads a new gauge, `providerCredentialsWatch`, in place of its guesses.

## Not in this spec

- Bedrock, Foundry and Ollama. Their rows stay placeholders.
- Checking a key against its vendor on save. The eleven vendors share no such call.
- Noticing an entry deleted in the OS keychain while Kstack runs. Entries are read at launch.
- Choosing a lower source over a higher one. To use the environment key, remove the saved key.
- Removing `llm.Provider.Key`. After this spec no production row sets it; the wires' tests still
  do. Removing it is a follow-up issue, opened when this lands.
- The *Disable* menu item, still a placeholder.

## Design

### 1. The service: `services/providerkeys`

One service holds every keyed row's environment key and saved key, knows the row's connector if it
has one, and is the row's `llm.SessionSigner`. It is not a connector: nothing signs in. It has its
own `CLAUDE.md` and `AGENTS.md`, listed in `sidecar/CLAUDE.md`'s *Guides*, and a budget line in
`.github/ci-config/claude-md-budgets.yml`.

Files: `providerkeys.go` (`Service`, `New`, state, the watches), `keyring.go` (the entry),
`save.go` (`Save`, `Delete`), `sign.go` (the signers), each with its `_test.go`, and
`testutil_test.go`.

```go
// Source is which credential signs a row.
type Source string

const (
	SourceNone        Source = "None"
	SourceEnvironment Source = "Environment"
	SourceSavedKey    Source = "SavedKey"
	SourceConnector   Source = "Connector"
)

// Failure is what is wrong with a row's key.
type Failure string

const (
	// NotDeleted is a Delete the keyring refused: the key is out of memory,
	// still in the keyring, and returns at the next launch. It is reported
	// over KeyRejected.
	NotDeleted Failure = "NotDeleted"
	// KeyRejected is a key the provider answered 401 to. It is kept, and signs.
	KeyRejected Failure = "KeyRejected"
)

// State is one row as every window draws it. It holds no key.
type State struct {
	ID, Label, KeyVar string
	Source            Source
	// EnvironmentKey and SavedKey say which keys are held, whichever signs.
	EnvironmentKey bool
	SavedKey       bool
	// Failure is derived from the row's two flags (below); "" for none.
	Failure Failure
}

// Row is a keyed provider as the service needs it. catalog.KeyRows builds them.
type Row struct {
	ID, Label, KeyVar string
	// Routes is every base URL the row's models are sent to, each with the
	// header its key goes in.
	Routes []Route
}

type Route struct {
	BaseURL string
	Header  string // "Authorization", "X-Api-Key"
	Prefix  string // "Bearer " or ""
}

// Connector is a row's connector: it signs in the keys' place while it has a session.
// Its Bind must answer signer.Dead{} whenever it has no session, and nothing
// else then: that value is how the row knows to bind a key.
type Connector interface {
	llm.SessionSigner
	// WatchSession is the connected session's id, 0 while there is none:
	// current on subscribe, then each change.
	WatchSession() (<-chan uint64, func())
}

type Config struct {
	Keyring oskeyring.Keyring
	// Rows is every keyed row, in picker order.
	Rows []Row
	// EnvKeys is the launch's key per provider id (app.Config.LLMKeys).
	EnvKeys map[string]string
	// Connectors is a row's connector, by provider id.
	Connectors map[string]Connector
}

func New(Config) (*Service, error)
func (s *Service) States() []State                        // in Rows order
func (s *Service) State(id string) (State, bool)
func (s *Service) Subscribe() (<-chan []State, func())    // a gauge: current, then each change
func (s *Service) Save(id, key string) error              // ErrUnknownProvider, ErrBadKey, ErrNotSaved
func (s *Service) Delete(id string) error                 // ErrUnknownProvider, ErrNotDeleted
func (s *Service) Signer(id string) llm.SessionSigner     // nil for an unknown id
func (s *Service) WatchCredential(id string) (<-chan uint64, func())
func (s *Service) SyncCatalogs(pokes *poke.Service, models *llm.Service, read func(context.Context, llm.Provider) ([]llm.Model, error)) (stop func())
func (s *Service) Close()
```

`Config.Keyring` is exported, unlike the connectors' `KeychainService`, because `agent/catalog`,
`agent/chat`, `app` and `sidecar/` build the service in their tests and must not read the real
keyring. Every other seam is an unexported option.

**Two locks guard the service, taken in this order.**

- `writes` serializes `Save` and `Delete`, and is the only lock held across a keyring call.
- `mu` guards every row's state. The list is built and sent to the gauge under it, so a frame
  never carries one row's older state after a newer one went out: the per-row change, the build of
  `[]State` and the `Send` are one critical section. A row's `WatchCredential` value is sent in
  the same section.

**`mu` is never held across a call to the keyring or to a connector.** `Bind` and `Sign` call the
connector before they take `mu`, and the session-following goroutine takes `mu` only after its
receive. The connector never calls into this service, so there is no order between `mu` and the
connector's own lock to get wrong. `TestConcurrentChangesPublishTheLatestList` saves to two rows
at once, many times, and asserts the last frame equals `States()`.

**Each row has its own generation.** It moves when the saved key held in
memory changes, and only then: a `Save` that is saved, and a `Delete` that drops a held key. A
refused `Save`, a `Delete` with no key held and a retried `Delete` leave it, and every binding,
as they were. The hubs are `gochan/watch`: one `Hub[[]State]` for the service, one
`Hub[uint64]` per row for `WatchCredential`.

**The source** is computed in one function from three facts, and nowhere else:

| Connector's session | Saved key | Environment key | `Source` |
| --- | --- | --- | --- |
| non-zero | any | any | `SourceConnector` |
| 0 or no connector | held | any | `SourceSavedKey` |
| 0 or no connector | none | held | `SourceEnvironment` |
| 0 or no connector | none | none | `SourceNone` |

**A row keeps two flags, and `State.Failure` is derived from them in one function.** They are
separate because they are about different keys: `undeleted` is a saved key the keyring would not
delete, and `rejected` is a `401` to the key that signs, which after a failed delete is the
environment key.

| `undeleted` | `rejected`, and `Source` is `SourceEnvironment` or `SourceSavedKey` | `Failure` |
| --- | --- | --- |
| set | either | `NotDeleted` |
| clear | yes | `KeyRejected` |
| clear | no | `""` |

Nothing but a `Delete` whose keyring delete succeeds, or a `Save`, clears `undeleted`. A `401`
never does.

**The connector's session is followed on one goroutine per connector.** `New` subscribes and reads
the current session before it returns, so the launch's `States()` is already right; the goroutine
then republishes on each change. `Close` cancels and joins them.

**`WatchCredential(id)` is `1` while `Source` is not `SourceNone`, else `0`.** It is what `catalogsync`
follows (§4). It is not a generation: a row's catalog is the same list under every credential, so a
change of source must not empty the picker.

### 2. Storage: one keyring entry per row

Service `Config.Keyring`'s (`Kstack` in a release), value `{"v":1,"key":"…"}`. The account is
`<id>-api-key`, except OpenRouter's, since its connector owns `openrouter-api-key`:

| Provider id | Account |
| --- | --- |
| `anthropic` | `anthropic-api-key` |
| `openai` | `openai-api-key` |
| `gemini` | `gemini-api-key` |
| `groq` | `groq-api-key` |
| `mistral` | `mistral-api-key` |
| `deepseek` | `deepseek-api-key` |
| `xai` | `xai-api-key` |
| `openrouter` | `openrouter-saved-api-key` |
| `together` | `together-api-key` |
| `fireworks` | `fireworks-api-key` |
| `opencode-go` | `opencode-go-api-key` |

`account(id)` in `keyring.go` is the one spelling: `id + "-api-key"`, with the OpenRouter exception
as a named constant. `TestTheAccountNames` pins the whole table as a literal, since an account name
is a persisted format.

`New` reads each row's account by name and reaches no network. The rules are
`connectors/openrouter/keyring.go`'s (`restore`), per account:

- `oskeyring.ErrNoEntry`: no saved key.
- Any other read error: no saved key, a warning naming the provider id, **nothing written**.
- A `v` above 1: no saved key, the entry **left in place** and marked a newer Kstack's, a warning.
- Anything that does not parse, a `v` that is not 1, or a `key` `Save` would refuse (step 2
  below): no saved key, the entry deleted (a failed delete is a warning).
- Otherwise the key is held and registered with `safe.AddSecret`.

`New` returns an error only for a `Row` it cannot use (an empty id, a duplicate id, a route whose
`BaseURL` does not parse or has no host), naming the provider id. A release's routes are
constants, so only a debug build's `KSTACK_<PROVIDER>_BASE_URL` can cause it: such a value now stops the
debug sidecar at launch (`FATAL`), where today it fails only that row's requests. That is accepted
for a debug switch, and `docs/development-switches.md` says so.

### 3. `Save` and `Delete`

**`Save(id, key)`**, holding `writes`:

1. An id with no row is `ErrUnknownProvider`.
2. The key is trimmed of surrounding whitespace, then refused (`ErrBadKey`) when it is shorter than
   `safe.MinSecretLen` (16, exported from `lib/safe` by this spec, so every saved key is one the
   redactor can register), longer than 512 bytes, or holds a byte outside `0x21`–`0x7E`. Nothing
   else about its form is checked.
3. `safe.AddSecret(key)`, before anything else touches it.
4. `Keyring.Set(account, entry)`, with `mu` not held. A failure is `ErrNotSaved`: the warning is
   logged, then `safe.ForgetSecret(key)`, so an error that quotes the key is redacted; the row is
   unchanged, the previous saved key included.
5. Under `mu`: the previous saved key is forgotten (`safe.ForgetSecret`), the new one held, the
   generation bumped, `undeleted`, `rejected` and the newer mark cleared, and the state published.

The key is saved unchecked. Saving the key already held is a save like any other.

**`Delete(id)`**, holding `writes`, with `mu` taken for steps 2, 3 and the end of 4 and released
across the keyring call:

1. An id with no row is `ErrUnknownProvider`.
2. With the entry marked a newer Kstack's, it deletes nothing and returns nil. With no key held
   otherwise, the keyring delete still runs: a keyring unread at launch may hold one.
3. Memory first, when a saved key is held: it is forgotten and dropped, `rejected` cleared (the
   mark was that key's), the generation bumped and the state published, so every binding made
   under a key (§4) is dead, and every window says so, whatever the keyring answers. With no key held, which is a retry while `undeleted`, memory is
   left alone: the generation stays, and so does `rejected`, which is then the environment key's.
4. `Keyring.Delete(account)`. A failure sets `undeleted`, publishes and returns `ErrNotDeleted`;
   calling again retries, whatever `rejected` has become since. Success clears `undeleted` and
   publishes.

`Save` is allowed while `undeleted` and clears it: its `Set` overwrites the entry.

Neither returns the key, and no error wraps it: all four errors are sentinels.

### 4. The signers

`Signer(id)` is the row's `llm.SessionSigner`. It needs two things from elsewhere.

**`llm.Provider.AuthHeader() (name, prefix string)`** is the header a static key goes in: the row's
`KeyHeader` with no prefix, else `X-Api-Key` with none on `DialectMessages`, else `Authorization`
with `Bearer `. `authorize` (`llm/helpers.go`) calls it, so the rule has one spelling.

**`catalog.KeyRows(baseURLs map[string]string) []providerkeys.Row`** builds each row's `Routes`:
for the row and each model `m`, `pm := p.ForModel(m)` gives `Route{pm.BaseURL, pm.AuthHeader()}`,
de-duplicated. **One helper moves a debug build's base URLs**: `keyedRows(baseURLs)
[]keyedProvider` in `catalog` returns every keyed row with its base moved and its models'
overrides cleared, and `KeyRows` and `catalog.New` both read it, so the routes and the rows cannot
disagree. In a release every row has one route, and `opencode-go`
two: `…/inference/go/openai/v1` with `Authorization: Bearer`, and `…/inference/go/anthropic` with
`X-Api-Key`.

**`sign(r, gen)`**:

1. With `gen` not the row's current one (a binding whose key was replaced or removed):
   `llm.ErrSignedOut`.
2. The key is the saved key, else the environment key. With neither: `llm.ErrSignedOut`.
3. For **every** route whose base `signer.Covers(base, r.URL)`, set `Header` to `Prefix + key`.
   With no such route: `errOffBase`, and nothing is set.

A release sets exactly one header: `TestEachBaseOfAKeyedRowTakesOneHeader` (`agent/catalog`)
fails when two routes of one row with different headers have one base covering the other. A debug
build that moves `opencode-go` onto one stub sets both, which is what lets one stub answer the row.

**`Bind()`**:

1. With a connector: `b := connector.Bind()`; unless `b` is a `signer.Dead`, return `b`
   (`Connector`'s contract, which `openrouter.Service.Bind` meets and a test there pins). The
   connector's own binding then hears the turn's `401`s, as today.
2. With no key: `signer.Dead{}`.
3. Else a binding of the row's current generation, which signs with whichever key is current at
   each request.

A binding made under a key keeps signing with it after the connector connects; the key is still
good. It ends when the generation next moves (§1). **A connector's binding is the connector's own**:
`Save` and `Delete` do not end it, and only the connector's session does.

**The unbound `Sign`** (the row's own signer, which nothing calls for a keyed row today, since its
catalog read reaches no network) follows the same order: the connector's `Sign`, and on
`llm.ErrSignedOut` the key under the current generation.

**`Refused(r)`** (a `401`): read the value back from the first covering route's header, strip the
prefix, and compare it with the current key under the binding's generation. On a match, set
`rejected` and publish. One `401` marks it; there is no check call. The key is kept and still
signs. A `Save` that is saved and a `Delete` that drops a held key clear it, since the key that
signs has changed; a refused `Save` and a `Delete` with no key held do not. The environment key
is marked the same way; while it signs, its mark stays until a key is saved or the next launch.
`Refused` never touches `undeleted`.

### 5. What moves first, changing no behaviour

`providerkeys` needs the API-base check, the dead binding and the catalog loop, which are under
`services/connectors/internal/` and so closed to it. Three moves, in a pull request of their own:

- `services/connectors/internal/signer` → `services/internal/signer`.
- `services/connectors/internal/catalogsync` → `services/internal/catalogsync`.
- `lib/oskeyring/oskeyringtest`: `New() *Keyring`, an in-memory `oskeyring.Keyring` whose
  `FailGet`, `FailSet` and `FailDelete` fields are the error each call answers. The five
  per-package `fakeKeyring`s stay as they are.

`oauthconn` stays under `connectors/internal`. The same pull request adds `Provider.AuthHeader`
and `safe.MinSecretLen`.

### 6. The catalog

`catalog.Config` loses `APIKeys` and gains `Keys *providerkeys.Service`. `Signers` stays, for the
signed-in providers alone (`chatgpt`, `google`), and so does `VertexBaseURL`.
`keyedProvider.connector` goes.

**`signedProvider.keyedBy` goes, and with it the rule that a Gemini key hides the `google` row.**
`New` lists a signed-in provider when `Signers` holds its signer and its row has a base URL,
whatever keys exist. So `google` is listed whenever the launch found a project, and its models
follow its connection through its own `SyncCatalog`, as on `main`.

`catalog.New`, for each keyed row of `keyedRows(cfg.BaseURLs)` (§4), in picker order:

- With `cfg.Keys` nil the row is left out (a test that wants no keyed row).
- `Signer` is `cfg.Keys.Signer(id)`; `Key` is never set.
- **The written catalog is kept when the row's `Source` is not `SourceNone` at build time, else nil.** A
  launch with a key therefore answers `models` at once, with no wait for the sync; the sync's first
  read then sets the same list and `SetCatalog` sends nothing.

`ReadKeyedModels(ctx, p llm.Provider) ([]llm.Model, error)` replaces `ReadOpenRouterModels`: the
written catalog of the keyed row `p.ID`, each model's base override cleared when `p.BaseURL` is not
the written row's (a debug build moved it). An id that is no keyed row is an error. It reaches no
network.

`Service.SyncCatalogs` starts one `catalogsync.Start(id, func() { return s.WatchCredential(id) },
pokes, models, read)` per row and returns one stop for all. It is each keyed row's only catalog
writer: a row's models appear when its `WatchCredential` goes to `1` and go when it returns to `0`,
and `modelsWatch` moves every window's picker. `openrouter.Service.SyncCatalog` is deleted.

`catalog.KeyVars()` and `BaseURLVars()` are unchanged. `lists` is unchanged: a list never depended
on a key.

### 7. The OpenRouter connector ranks highest

- `openrouter.Config.FromEnvironment` goes. `New` always runs `restore()`.
- `Connect` and `Disconnect` no longer refuse for the environment. `connectors.FromEnvironment`,
  `connectors.ErrFromEnvironment`, the GraphQL `ConnectorPhase.FromEnvironment` member, its
  `gqlgen.yml` binding and `connectorErr`'s entry are deleted. No other connector uses them.
- The connector's key signs while it is `Connected`, through `providerkeys`' `Bind` (§4).
- *Disconnect* deletes `openrouter-api-key` alone, so the row falls back to the saved key, then the
  environment key. *Remove key* deletes `openrouter-saved-api-key` alone. Both entries can exist at
  once.

### 8. Wiring

`app/runtime.go`, `build`:

1. `openrouter.New` loses `FromEnvironment`.
2. After the connectors and before the catalog:

   ```go
   rt.ProviderKeys, err = providerkeys.New(providerkeys.Config{
   	Keyring:    oskeyring.New(keychainService),
   	Rows:       catalog.KeyRows(cfg.LLMBaseURLs),
   	EnvKeys:    cfg.LLMKeys,
   	Connectors: map[string]providerkeys.Connector{openrouter.ID: rt.OpenRouter},
   })
   ```

   added as `"provider keys service"`, whose close is `rt.ProviderKeys.Close`. `Runtime` gains the
   `ProviderKeys` field in build order.
3. `newCatalog(cfg, rt.ProviderKeys, map[string]llm.SessionSigner{chatgpt.ID: rt.ChatGPT,
   google.ID: rt.Google})`, which still passes `VertexBaseURL: cfg.vertexBaseURL`. Only
   `openrouter.ID` leaves the map.
4. The `"openrouter catalog"` part is replaced by `"provider catalogs"`, a `StartFunc` over
   `rt.ProviderKeys.SyncCatalogs(rt.Poke, rt.LLM, catalog.ReadKeyedModels)`.
5. The `"google catalog"` part stays. Its comment drops *or `GEMINI_API_KEY`*: the row is absent
   only with no project.
6. `resolver()` passes `ProviderKeys: rt.ProviderKeys`.

`sidecar/config.go` is unchanged: the environment keys are read, registered and cleared as today,
and the login shell's still join `cfg.LLMKeys` in `build`.

Tests that build a catalog with `APIKeys` (`agent/catalog`'s, `agent/chat/context_test.go`,
`app/app_test.go`, `sidecar/scrubexec_unix_test.go`, and the `livecache` and `liveswitch` tests)
build a `providerkeys.Service` over `oskeyringtest.New()` with the keys as `EnvKeys`. Each package
that does so more than once has one helper in its `testutil_test.go`.

### 9. GraphQL

A new section, `# --- Provider credentials ---`, after `# --- Models ---`:

```graphql
"Which credential signs a provider's requests. The highest one present does: Connector, then SavedKey, then Environment."
enum ProviderCredentialSource {
  None
  "The key variable, from the launch or the login shell."
  Environment
  "A key saved from the Providers pane, in the OS keyring."
  SavedKey
  "The provider's connector, while it is connected."
  Connector
}

"What is wrong with a provider's key."
enum ProviderKeyFailure {
  "The keyring would not delete the saved key. It is out of use now and returns after a restart; providerKeyDelete retries."
  NotDeleted
  "The provider answered 401 to the key that signs. The key is kept and still used. Never reported while NotDeleted is."
  KeyRejected
}

"A provider whose key can be set, and where its credential comes from. No key is ever here."
type ProviderCredential {
  "The provider's id, as Provider.id."
  id: String!
  label: String!
  "The environment variable its key is read from."
  keyVariable: String!
  source: ProviderCredentialSource!
  "Whether the launch held the key variable."
  environmentKey: Boolean!
  "Whether a saved key is held."
  savedKey: Boolean!
  "Null when nothing is wrong."
  failure: ProviderKeyFailure
}
```

```graphql
# Query
"Every provider whose key can be set, in picker order; live updates via providerCredentialsWatch."
providerCredentials: [ProviderCredential!]!

# Mutation
"Save a provider's API key in the OS keyring, replacing any saved one. Always true when it does not error. It signs the provider's requests from the next one, unless its connector is connected. The key is not checked against the provider. Never returns the key. Refused (KSTACK_VALIDATION_ERROR) for an unknown provider and for a key that is under 16 or over 512 bytes or holds anything but printable ASCII; KSTACK_PROVIDER_KEY_NOT_SAVED when the keyring refused, and nothing changed."
providerKeySave(providerID: String!, key: String!): Boolean!
"Delete a provider's saved key. Always true when it does not error, including when none was saved. KSTACK_PROVIDER_KEY_NOT_DELETED when the keyring refused: the key is out of use, returns after a restart, and calling again retries. Refused (KSTACK_VALIDATION_ERROR) for an unknown provider."
providerKeyDelete(providerID: String!): Boolean!

# Subscription
"GAUGE. Every provider whose key can be set, a fresh list on every change."
providerCredentialsWatch: [ProviderCredential!]!
```

- `gqlgen.yml` binds `ProviderCredential` to `providerkeys.State` (`keyVariable` to `KeyVar`), with
  `failure` a resolver (null for `""`), and both enums member by member, as `ConnectorPhase` is.
- `graph/errors` gains `ErrProviderKeyNotSaved` (`KSTACK_PROVIDER_KEY_NOT_SAVED`) and
  `ErrProviderKeyNotDeleted` (`KSTACK_PROVIDER_KEY_NOT_DELETED`).
- `graph/util.go` gains `providerKeyErr`, mapping `ErrUnknownProvider` and `ErrBadKey` to
  `ErrValidationError`, and the other two to their own, each through `errors.Clone`.
- `graph/resolver.go` gains `ProviderKeys ProviderKeyStore`, an interface of `States`, `Subscribe`,
  `Save` and `Delete`, so resolver tests wire a fake.
- The watch is `mapStream` over `Subscribe`, as the connection watches are.

Root `CLAUDE.md` lists `providerCredentialsWatch` among the gauges.

### 10. The webview

**`src/lib/provider-credentials.tsx`**, shaped as `src/lib/connectors.tsx`:

```ts
export type ProviderCredential = ProviderCredentialsWatchSubscription['providerCredentialsWatch'][number];
export type SaveKeyResult = 'saved' | 'bad-key' | 'not-saved' | 'failed';
export type ProviderCredentials = {
  credentials: ProviderCredential[];
  loaded: boolean; // the watch's first frame
  saveKey: (providerID: string, key: string) => Promise<SaveKeyResult>;
  removeKey: (providerID: string) => Promise<boolean>;
};
export function useProviderCredentials(): ProviderCredentials;
```

`saveKey` and `removeKey` call `client.mutation(...).toPromise()` and return only the outcome,
read off `error.graphQLErrors[0].extensions.code`. **Neither uses `useMutation`**: its result keeps
the operation, and with it the key, in React state. The key is never written to `localStorage`,
`sessionStorage`, a log or the error bus.

**`provider-setup-form.tsx`**:

- New props: `credential?: ProviderCredential` (replacing `keyVar`) and
  `onSaveKey: (key: string) => Promise<SaveKeyResult>`.
- It opens on the source that signs (`SavedKey` is `api-key`, `Environment` is `environment`, a
  connector its connector source), else the first of `sources`.
- `ApiKeyField` is enabled: `type="password"`, `autoComplete="off"`, `spellCheck={false}`, its
  value in the form's own state.
- *Save* is drawn for `api-key` alone, enabled when the trimmed value is not empty and no save is
  in flight. On `saved` the field is cleared and the dialog closes. On any other result the field
  is kept and one line is drawn under it, `role="alert"`:

  | Result | Line |
  | --- | --- |
  | `bad-key` | *That does not look like an API key.* |
  | `not-saved` | *Your system keychain refused the key. Nothing was saved.* |
  | `failed` | *Kstack could not save the key.* |

- Notes: `environment` reads *Kstack uses `X` from your environment.* when `environmentKey`, with
  *A saved key or a connector is used over it.* appended when `source` is not `Environment`; else
  the existing *`X` is not set…* line. `api-key` reads *A key is saved. Saving another replaces
  it.* when `savedKey`, else the existing note.

**`providers.tsx`**:

- `PROVIDERS` keeps its four entries with no key variable: the placeholders Bedrock, Foundry and
  Ollama, and the real `GOOGLE_ID` row (*Vertex AI*), set up while the catalog serves it. The keyed
  rows come from the gauge, each with sources `['environment', 'api-key']` plus its connector's
  from a small map by id (`openai: 'chatgpt-connector'`, `openrouter: 'openrouter-connector'`).
  Gemini loses its `google-connector` source: a Google sign-in signs Vertex AI, which is the
  *Vertex AI* row's.
- A keyed row is set up when `source !== 'None'`. A provider the catalog serves that is in neither
  list (`chatgpt`) stays a set-up row with no sources.
- Nothing draws before the gauge's, the catalog's and the connectors' first frames.
- The detail line, first match:

  | State | Line |
  | --- | --- |
  | `failure: NotDeleted` | *Kstack could not remove the saved key. It returns after a restart.* |
  | `failure: KeyRejected`, `source: SavedKey` | *The saved key was rejected. Replace or remove it.* |
  | `failure: KeyRejected`, `source: Environment` | *`X` was rejected.* |
  | `source: Connector` | *Connected on the Connectors tab* |
  | `source: SavedKey` | *Using a saved key* |
  | `source: Environment` | *Found `X`* |
  | `source: None` | *No API key found* |

- The row menu gains **Remove key**, drawn when `savedKey` or `failure` is `NotDeleted`, behind a
  confirm `Dialog`: *Remove the saved key for `<label>`?* with what follows, first match:
  *`<label>` stays connected on the Connectors tab.* when `source` is `Connector`, *Kstack will
  use `X` from your environment.* when `environmentKey`, else *`<label>`'s models leave the
  picker.* A failed remove leaves the row's `NotDeleted` line to say so.

**`connectors.tsx` and `src/lib/connectors.tsx`**: every `FromEnvironment` case goes. OpenRouter's
`SignedOut` detail reads the gauge: *Using a saved key* for `SavedKey`, *Using
`OPENROUTER_API_KEY`* for `Environment`, else the offer. **Connect** is offered in each.

Every string from the gauge goes through `VisibleText`. The design manifest
(`design/components.json`) is updated if `make lint-design` asks.

## Security

The record is `docs/security/<date>-saved-provider-keys.md`, written before the code. It states:

- **This is the first secret typed into the webview.** Its path is the input's state, one urql
  mutation's variables, the `graphql_query` body, the sidecar's resolver, the service. Checked
  before the record is written, and each pinned by a test:
  - the host forwards the body unexamined and logs none of it, on success, failure and timeout
    (`src-tauri/src/services/sidecar/graphql/query.rs`);
  - the sidecar's error presenter logs the operation type, Go error types and `code` alone
    (`graph/server.go`), and no resolver or service error wraps the key;
  - the webview's error exchange (`src/lib/graphql/error-exchange.ts`) hands the error bus the
    message and, as `cause`, the whole `CombinedError`. A failed save reaches it. A test sends a
    failing `providerKeySave` through the real client and asserts the key is nowhere in what the
    bus receives, `cause` and every field under it included, nor in what `src/lib/error-log.ts`
    sends the host. If the key is found there, the two credential mutations are reported with
    their message and code alone, and the test pins that instead. The protection is not marked
    **Enforced** before this test exists.
- **No log line, file, `app.db` row or GraphQL field holds a saved key.** Each is registered with
  `safe.AddSecret` on receipt and on restore, and forgotten when replaced or removed.
- **A key signs its row's own base URLs alone**, and follows no redirect (`Sign`'s check, the
  wires' `NoRedirectClient`).
- **The keyring entry is as reachable as the connectors' entries**: any process of the user can
  ask for it; a sandboxed command cannot reach the keyring.
- **Code running in the webview can replace or remove any provider's key**, as it can call every
  other mutation. It cannot read one back. A replaced key sends later chats under an account the
  attacker chose, at the provider's real endpoint. The CSP is the containment, as for every
  mutation.
- **A key is used unchecked**, and a rejected one keeps signing until the user removes it.
- **Residual risk.** A removed key stays live at its vendor. A key whose delete failed returns at
  the next launch, and the row says so.

`docs/security-model.md`: a line under what is protected (the saved keys in the keyring), and a
row *A saved provider key stays in the keyring and memory, signs its row's base URLs alone, and no
log line, file, `app.db` row or GraphQL field holds one*, **Pending** until the tests below pin it.
The OpenRouter connector's row drops *is never read while `OPENROUTER_API_KEY` is set*.

## Docs

- **ADR** `docs/adr/<date>-a-providers-credential-follows-one-order.md`: the order; every keyed row
  under one signer, with the header chosen by base URL (which amends *a key as one more `Signer`*
  in [the credential ADR](../adr/2026-11-18-a-providers-credential-is-a-key-or-a-signer.md)); the
  move of `signer` and `catalogsync` to `services/internal` (which amends [a connector is one
  package](../adr/2026-11-19-a-connector-is-one-package-and-its-machinery-is-internal.md)); and
  *the environment key wins*, replaced (in [a connector is an OAuth session or a granted
  key](../adr/2026-11-19-a-connector-is-an-oauth-session-or-a-granted-key.md)); and *when
  `GEMINI_API_KEY` is set the `google` row is left out*, replaced (in [the Google connector signs
  Vertex AI](../adr/2026-11-19-the-google-connector-signs-vertex-ai-in-the-users-project.md)):
  the order is per provider row, and `gemini` and `google` are two. Each amended ADR
  gets its `amended_by` line, and every `CLAUDE.md` link is repointed in the same commit.
- `docs/persisted-data.md`: a row *Saved provider keys*, OS keyring, service `Kstack`, account
  `<id>-api-key` (`openrouter-saved-api-key` for OpenRouter), `{"v":1,"key"}`, owner
  `services/providerkeys/keyring.go`, disposable (save again; the key stays live at its vendor),
  `v` 1, pinned by `TestTheAccountNames` and `TestNewRestoresWhatTheKeyringHolds`. The summary line
  for the keyring and the sentence *passed in at launch, never written* are corrected.
- `CLAUDE.md`s: root (the gauge), `sidecar/` (*Guides*, *Connectors*, the provider-keys security
  bullet), `internal/llm` (*Credentials*, *Providers and the catalog*: a key needs no restart),
  `internal/agent` (*Catalog*), `connectors/openrouter` (*The environment key wins* goes), `connectors/google` (*`GEMINI_API_KEY`
  wins* goes),
  `services/providerkeys` (new), `src/components/settings` (*Providers*, *Connectors*), `src/`
  (the new hook, where it lists `src/lib`).

## Tasks

Three pull requests into `wip/provider-api-keys`, each on the one before. Each updates the guides
and registers named above for the code it changes, in the same change.

| PR | # | Task | Status |
| --- | --- | --- | --- |
| A | 1 | Move `signer` and `catalogsync` to `services/internal`; fix every import | Planned |
| A | 2 | `lib/oskeyring/oskeyringtest`; `llm.Provider.AuthHeader`, used by `authorize`; `safe.MinSecretLen` | Planned |
| B | 3 | The security record, with its three checks made; the ADR; the `security-model.md` row as **Pending** | Planned |
| B | 4 | `providerkeys`: rows, keyring restore, `Save`, `Delete`, the signers, the two watches, `SyncCatalogs` | Planned |
| B | 5 | `catalog`: `keyedRows`, `KeyRows`, `ReadKeyedModels`, `New` over `Config.Keys`; `keyedBy` deleted, so `google` is listed beside a Gemini key; every test that passed `APIKeys` | Planned |
| B | 6 | `openrouter`: drop `FromEnvironment` and `SyncCatalog`; the `connectors` vocabulary; `runtime.go` | Planned |
| B | 7 | GraphQL schema, `gqlgen.yml`, resolvers, errors and tests; `pnpm codegen`; the webview's `FromEnvironment` cases removed so it builds | Planned |
| C | 8 | The webview: the hook, the form, the Providers rows and *Remove key*, the Connectors detail, tests | Planned |
| C | 9 | The host's test that a query logs no body; the error-bus test of a failed save; `security-model.md` row to **Enforced** | Planned |

- **A** changes no behaviour and lands first.
- **B** opens with the record and the ADR, so the review reads them before the code. After it the
  sidecar is whole and the pane still shows its placeholders, minus `FromEnvironment`.
- **C** is the pane.

Verification is `docs/specs/README.md`'s *Verification commands*: `make test-changed` while
working, `make check-changed` before each pull request, and the wire checks for B. No prompt or
offer changes, so the goldens under `app/testdata/prompt/` must not move.

## Acceptance

Sidecar, each a test with a fake keyring and no vendor:

- A saved key survives a restart; a newer `v` is left; an unparsable entry is deleted; an
  unreadable keyring writes nothing (`TestNewRestoresWhatTheKeyringHolds`, `TestANewerEntryIsLeft`,
  `TestAnUnreadableKeyringWritesNothing`).
- The source follows the table in §1, for every combination, with and without a connector
  (`TestTheSourceTable`).
- With an environment key and a saved key, a request carries the saved key; with the connector
  connected too, the connector's (`TestTheHighestCredentialSigns`).
- Removing the saved key falls back to the environment key; with none, `WatchCredential` goes to
  `0` (`TestDeleteFallsBack`).
- A binding made under an environment or saved key fails `llm.ErrSignedOut` after a `Save` that
  is saved or a `Delete` that drops a held key. A refused `Save`, a `Delete` with no key held and a
  retried `Delete` leave it signing, and one made while the connector was connected is the
  connector's and keeps signing (`TestABindingEndsWithItsKey`).
- A key is set on its row's bases alone, in each base's header, and a request elsewhere is refused
  with no header set (`TestTheKeyGoesNowhereElse`, with an `opencode-go` case for both routes).
- A `401` under the current key marks `KeyRejected` and the key still signs; one under a replaced
  key marks nothing (`TestARejectedKeyIsKept`).
- A refused `Set` is `ErrNotSaved` with the previous key still held; a refused `Delete` is
  `NotDeleted`, ends every binding made under a key and succeeds on a retry
  (`TestARefusedSaveChangesNothing`, `TestAFailedDeleteIsNotARemoval`).
- A failed delete, then a `401` to the environment key, still reports `NotDeleted`; the retried
  `Delete` reaches the keyring, succeeds, and the row then reports `KeyRejected`
  (`TestARejectionDoesNotHideAFailedDelete`).
- A key under 16 or over 512 bytes, or with a space or control byte inside, is `ErrBadKey`
  (`TestSaveRefusesWhatIsNotAKey`).
- No saved key is in the raw log, in any file under the data directory or in any GraphQL response,
  after a save, a signed request, a `401`, a replace and a delete; each is registered with the
  redactor while held and forgotten after (`TestNoSavedKeyLeavesTheKeyring` in `providerkeys`;
  `TestProviderKeySaveReturnsAndLogsNoKey` in `graph`, which also sends a failing save).
- Every keyed row is in `llm.Service` from launch; one with no credential lists no model; a save
  lists its written catalog on `modelsWatch` and a delete empties it
  (`TestAKeyedRowListsItsModelsWhileItHasACredential` in `app`).
- With a project, a Google signer and a Gemini key, both `gemini` and `google` are listed
  (`TestTheGoogleRowIsListedBesideAGeminiKey` in `agent/catalog`, replacing the keyed half of
  `google_test.go`'s left-out test; `app/runtime_test.go`'s Gemini-key case expects the row).
- Two rows changed at once never leave the gauge on an older list
  (`TestConcurrentChangesPublishTheLatestList`).
- `Connect` works with `OPENROUTER_API_KEY` set and with a saved key; after `Disconnect` the row
  signs with the saved key, then the environment key (`TestOpenRouterFallsBackAfterADisconnect` in
  `app`).
- The resolvers map each refusal to its code, and the watch sends a frame per change
  (`graph/schema.resolvers_test.go`).

Webview:

- The form saves through the hook, clears the field and closes on `saved`, and draws each failure's
  line with the field kept.
- The key is in no rendered text, and `saveKey` keeps no operation.
- A failed save puts the key nowhere in the error bus's payload, `cause` included, nor in what the
  error log sends the host.
- Each row's detail follows §10's table; *Remove key* is behind its confirm and names what follows.
- The Connectors row for OpenRouter names the key that signs while signed out.

By hand, once, in a dev build started with no key variable set: save a key, see that provider's
models in the picker of two windows with no restart, send a turn, restart, send another, remove the
key, and see the models leave.

`coverage / Gate` is green on each pull request.

## Things to watch

- **Two OpenRouter entries.** `openrouter-api-key` is the connector's, `openrouter-saved-api-key`
  the saved key's. Nothing but its owner reads, writes or deletes either.
- **The catalog at launch is synchronous, afterwards a sync.** `catalog.New` fills a row that has a
  credential so the first `models` answer is whole; a test that saves a key waits on
  `llm.Service.Subscribe`, never a sleep.
- **A save mid-turn ends that turn's binding, unless the connector signs it.** The turn fails
  *signed out*; the next send runs under the new key.
- **A Gemini key no longer hides Vertex AI.** `main` leaves the `google` row out when
  `GEMINI_API_KEY` is set (`signedProvider.keyedBy`). This spec deletes that rule (§6): a chat on
  `google/…` keeps working when a Gemini key is saved, and both rows can be in the picker.

## When it lands

The guides already say what each pull request built. The `wip/provider-api-keys` pull request
checks that nothing in §1–§10 is left only here, confirms the `security-model.md` row is
**Enforced** and names its tests, opens the follow-up issue for `llm.Provider.Key`, and deletes
this spec.
