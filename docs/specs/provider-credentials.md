---
title: One provider table, one credentials service
scope: sidecar, webview
status: Planned
---

# One provider table, one credentials service

**Answers:** [#1095](https://github.com/kstackhq/kstack/issues/1095). **Needs:** saved provider
API keys (#1084), on `main`. **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today `agent/catalog` keeps two provider lists. `keyedProviders` holds the eleven providers that
take a key, signed by `services/providerkeys`. `signedProviders` holds ChatGPT and Vertex AI, wired
around that service: `catalog.Config.Signers`, `ReadSignedModels`, and a `SyncCatalog` call per
connector in `app/runtime.go`. The Providers pane repeats the split with a hard-coded Vertex AI
entry beside the rows the gauge sends.

The lists are not two kinds of provider. A provider's credential follows one order, environment
key < saved key < connector, and a ChatGPT or Vertex AI row is a provider whose order holds the
connector alone.

After this spec:

- `catalog` has one table. A row is an `llm.Provider`, an optional key variable and an optional
  models reader.
- One service, renamed `services/providercredentials`, signs every row and syncs every row's
  catalog. A provider with no key variable has its connector as its only source.
- `providerCredentialsWatch` sends every provider in the table. `keyVariable` is null for one that
  takes no key.
- The Providers pane draws the ChatGPT plan and Vertex AI rows from the gauge.

No keyring entry changes, and the credential that signs each provider today still signs it.

## Not in this spec

- Bedrock, Foundry and Ollama. Their pane rows stay placeholders.
- Renaming the `providerKeySave` and `providerKeyDelete` mutations. They act on a key.
- Sending a provider's connector in the gauge. The pane keeps its map from provider id to
  connector source.
- Removing `llm.Provider.Key`, which has its own follow-up.

## Design

### 1. The table

`agent/catalog/providers.go` replaces `keyedProvider`, `signedProvider` and their two lists with
one:

```go
// row is a provider, the environment variable that keys it and how its
// catalog is read.
type row struct {
	provider llm.Provider
	// keyVar is "" for a provider that takes no key: its connector alone signs it.
	keyVar string
	// models reads the provider's catalog off its listed row. Nil reads the
	// written catalog, with no request.
	models func(context.Context, llm.Provider) ([]llm.Model, error)
}

// rows is every provider but the fake, in picker order.
func rows(vertexBaseURL string) []row

// rowsAt is rows at the base URLs a debug build moves, less any row left with
// no base URL.
func rowsAt(baseURLs map[string]string, vertexBaseURL string) []row
```

- The order is today's: the eleven keyed rows, then `chatgpt`, then `google`.
- `chatgpt` is the one row with a reader, `ReadChatGPTModels`.
- `google`'s catalog is the row's written one: the `gemini` row's models under `google/` ids, set
  in `newGoogleProvider`. `ReadGoogleModels` is deleted. The catalog stays UNVERIFIED.
- `newChatGPTProvider` and `newGoogleProvider` take no signer; `New` sets every row's.
- `rowsAt` is the one place a debug base is applied. It clears each model's own base URL on the
  row it moves, as `keyedProvidersAt` does today. No row without a key variable writes one.
- The `google` row is left out when it has no base URL: no project found and no debug override.
  Nothing else is left out.

The exported functions read the table:

| Function | Is |
| --- | --- |
| `Providers(baseURLs, vertexBaseURL)` | every row of `rowsAt` as `providercredentials.Provider`. A row with no key variable has no `KeyVar` and no `Routes`. Replaces `KeyedProviders` |
| `Models(baseURLs, vertexBaseURL)` | one reader for `SyncCatalogs`, `func(ctx, p)`: the row's `models(ctx, p)`, else its written catalog; an error for an id not in the table. Replaces `KeyedModels` and `ReadSignedModels` |
| `KeyVars()` | each row's key variable by id, rows with none skipped |
| `BaseURLVars()` | `KSTACK_<ID>_BASE_URL` for every row of `rows("")` |

**The reader's `p` is the provider `catalogsync` hands it**, the one `llm.Service` lists, which
carries the signer. The table's own row has none, so `Models` uses the table only to find the row
by `p.ID` and for a written catalog, and never passes its row to `models`.

`New(Config)` is one loop over `rowsAt`: set `Signer` from `cfg.Credentials.Signer(id)`, empty the
catalog while the service reports `SourceNone`, then the fake. `Config.Signers` is deleted and
`Config.Keys` becomes `Config.Credentials`. A nil `Credentials` lists the fake alone, which
`graph`'s tests rely on. Because `New` and `Providers` read the same `rowsAt`,
the gauge never lists a provider the catalog left out.

### 2. The service holds a provider with no key

`services/providerkeys` becomes `services/providercredentials` (§4). A `Provider` with an empty
`KeyVar` takes no key:

- `New` skips its keyring read. It has no entry, and a read can start a process on macOS.
- `New` takes no environment key for it: an `EnvKeys` entry under its id is ignored.
- `Save` and `Delete` refuse it with a new sentinel, `ErrNoKey`, before any keyring call.
  `graph/util.go` maps it to `KSTACK_VALIDATION_ERROR`, beside `ErrUnknownProvider`.
- Its `State` has an empty `KeyVar`, `HasEnvironmentKey` and `HasSavedKey` false, and `Source`
  `None` or `Connector`.

`sign.go` needs no change. With no key, `providerSigner` answers the connector's `Sign` and `Bind`,
and `llm.ErrSignedOut` or `signer.Ended{}` once the connector has no session. The ChatGPT and
Google connectors already meet `Connector`: `oauthconn` has `WatchSession`, and its `Bind` answers
`signer.Ended{}` with no session.

**A provider with no key syncs its catalog per session.** `WatchHasCredential` is `0` or `1`
today, so a ChatGPT disconnect and reconnect as another account can conflate to a steady `1`, and
`catalogsync` would keep the first account's models. It becomes `WatchCredential`:

| The provider | Value |
| --- | --- |
| has no credential | `0` |
| takes a key and has a credential | `1`, whichever credential signs |
| takes no key and its connector has a session | that session's generation |

A provider that takes a key keeps today's rule: its catalog is the same under every credential, so
a change of source publishes nothing and its models never leave the picker. A provider that takes
no key gets what its connector's `SyncCatalog` gives it today: `catalogsync` empties the catalog
and reads again on each new session.

### 3. Wiring

`app/runtime.go`:

- `providercredentials.New` takes `catalog.Providers(cfg.LLMBaseURLs, cfg.vertexBaseURL)` and
  `Connectors: {openrouter.ID: rt.OpenRouter, chatgpt.ID: rt.ChatGPT, google.ID: rt.Google}`. A
  connector whose provider is not in the table is ignored, as an unknown id is today.
- `SyncCatalogs(rt.Poke, rt.LLM, catalog.Models(...))` is the only catalog sync. The `chatgpt
  catalog` and `google catalog` parts and the `rt.LLM.Provider(google.ID)` check are deleted.
- `newCatalog` loses its `signers` parameter.
- `Runtime.ProviderKeys` becomes `Runtime.ProviderCredentials`.

`chatgpt.Service.SyncCatalog` and `google.Service.SyncCatalog` are deleted.

### 4. The rename

A commit of its own, changing no behaviour:

- `services/providerkeys` → `services/providercredentials`, with its `CLAUDE.md`, `AGENTS.md`, the
  row in `sidecar/CLAUDE.md`'s *Guides* and the line in `.github/ci-config/claude-md-budgets.yml`.
- `graph.Resolver.ProviderKeys` → `ProviderCredentials`; `ProviderKeyStore` →
  `ProviderCredentialStore`.
- Error text prefixes follow the package name.
- The paths in `docs/security-model.md` and `docs/persisted-data.md`.

`keyring.go`'s account names are literals and do not move. `TestTheAccountNames` pins them.

### 5. GraphQL

```graphql
"A provider and where its credential comes from. No key is ever here."
type ProviderCredentialState {
  "The provider's id, as Provider.id."
  id: String!
  label: String!
  "The environment variable its key is read from. Null for a provider that takes no key: only its connector signs it."
  keyVariable: String
  source: ProviderCredentialSource!
  ...
}
```

`providerCredentials` and `providerCredentialsWatch` say *every provider*, not *every provider
whose key can be set*. The resolver sends null for an empty `KeyVar`. Then `go tool gqlgen
generate` and `pnpm codegen`.

### 6. The webview

`src/components/settings/settings-dialog/providers.tsx`:

- `PROVIDERS` loses its Vertex AI entry. Bedrock, Foundry and Ollama stay.
- `KEYED_CONNECTORS` becomes `CONNECTORS`: `chatgpt` → `chatgpt-connector`, `google` →
  `google-connector`, `openrouter` → `openrouter-connector`. The `openai` entry goes: connecting
  ChatGPT never sets up the `openai` row.
- A gauge row's sources are `environment` and `api-key` when `keyVariable` is set, then its
  connector.
- `onSaveKey` is passed only when `keyVariable` is set, so a connector-only provider's dialog has
  no key input. Such a row never offers *Remove key*.
- `Detail` reads *Not connected* for a connector-only row with source `None`.
- `keyFailureText` takes the nullable `keyVariable`. A failure needs a key, so a connector-only
  row has none.

A connector-only row already gets *Connect*, to the Connectors tab, from `SetUpButton`.

Two more files draw `keyVariable` and stop type-checking once it is nullable. Neither is reached
for a provider with no key:

- `connectors.tsx`, in the OpenRouter entry's detail.
- `provider-setup-form.tsx`, in `SourceNote` for the environment source.

Two things the user sees change:

- The ChatGPT plan row is always listed, not only while connected.
- With no Google Cloud project, the pane has no Vertex AI row. Today it shows *Needs a Google
  connector*.

## Security

No credential, route or approval path changes. The `chatgpt` and `google` rows' signer becomes the
service's `providerSigner` over the same connector, and a binding is still the connector's own. A
provider with no key reads and writes no keyring entry. The `security-model.md` rows naming
`providerkeys` or the signing path are repointed; no new record is added.

## Docs

- A new ADR, *every provider is one row, signed by one service*, amending [a provider's credential
  follows one order](../adr/2026-11-20-a-providers-credential-follows-one-order.md) and [a
  signed-in provider's catalog follows its
  connection](../adr/2026-11-18-a-signed-in-providers-catalog-follows-its-connection.md). Each of
  the two gains an `amended_by` pointer to it. Neither is superseded, so no link moves.
- `agent/CLAUDE.md` (*Catalog*), `services/providercredentials/CLAUDE.md`, `sidecar/CLAUDE.md`
  (*Guides*, *Connectors*), `llm/CLAUDE.md`, `connectors/internal/oauthconn/CLAUDE.md`,
  `src/components/settings/CLAUDE.md` and the root `CLAUDE.md`'s gauge list if its wording moves.
- `docs/security-model.md` and `docs/persisted-data.md`, as §4 and *Security* say.

## Tasks

Three pull requests into `wip/provider-credentials`, each on the one before.

| PR | # | Task | Status |
| --- | --- | --- | --- |
| A | 1 | The rename (§4) | Planned |
| B | 2 | The ADR | Planned |
| B | 3 | `providercredentials`: a provider with no key, `ErrNoKey`, `WatchCredential` (§2) | Planned |
| B | 4 | `catalog`: the table, `Providers`, `Models`, `New` (§1); `runtime.go` and the connectors' `SyncCatalog` removed (§3) | Planned |
| C | 5 | GraphQL: nullable `keyVariable`, descriptions, resolver, codegen (§5) | Planned |
| C | 6 | The Providers pane (§6) | Planned |

A changes no behaviour. After B the sidecar is whole and the gauge sends two more rows. Until C
the pane draws them as keyed, with a key input that answers `ErrNoKey`, and shows Vertex AI twice:
its hard-coded entry and the gauge's row. B and C reach `main` together.

Verification is `docs/specs/README.md`'s *Verification commands*. No prompt or offer changes, so
the goldens under `app/testdata/prompt/` must not move.

## Acceptance

Sidecar, each a test with a fake keyring, a fake connector and no vendor:

- `rowsAt` lists every provider once, in picker order, and leaves `google` out with no base URL.
- A debug base moves a row and clears its models' own base URLs.
- `Models` answers an error for an id not in the table, and `New` with no `Credentials` lists the
  fake alone.
- `Save` and `Delete` for a provider with no key are `ErrNoKey`, and the keyring is never called.
  `providerKeySave` for one answers `KSTACK_VALIDATION_ERROR`.
- `New` reads no keyring entry for a provider with no key, and an `EnvKeys` entry under its id
  leaves `HasEnvironmentKey` false, the source `None` and its requests unsigned.
- `Models` hands a row's reader the provider it was called with: the ChatGPT catalog request
  carries the connector's signature.
- A provider with no key has source `Connector` while its connector has a session and `None`
  otherwise; its request carries the connector's signature, and is `llm.ErrSignedOut` with none.
- `WatchCredential` follows the table in §2. A second session reads the ChatGPT catalog again with
  no `0` between. OpenRouter's stays `1` while its connector connects and disconnects over a key,
  and its models stay listed throughout.
- `chatgpt` and `google` list their models on connect and drop them on disconnect, with no
  restart.
- The gauge lists exactly the providers `llm.Service` holds, less the fake, with and without a
  Google Cloud project.

Webview:

- A row with a null `keyVariable` offers *Connect*, shows no key input in its dialog and no
  *Remove key*.
- The Vertex AI row is drawn from the gauge, and is absent when the gauge has none.
- The `openai` row offers no ChatGPT connector source.

## Done when

- `git grep -i signedProvider` finds nothing outside `docs/adr/`.
- `coverage / Gate` is green.

## When it lands

The guides already say what each pull request built. The `wip/provider-credentials` pull request
checks that nothing in §1–§6 is left only here and deletes this spec.
