# internal/services/providerkeys — keyed providers' keys

**One service holds every keyed provider's environment key and saved key, knows its connector, and
is its `llm.SessionSigner`.** It is not a connector: nothing signs in.
→ [ADR: a provider's credential follows one order](../../../../docs/adr/2026-11-20-a-providers-credential-follows-one-order.md),
[security record](../../../../docs/security/2026-11-20-saved-provider-keys.md).

- **The order, lowest to highest: environment key < saved key < connector.** `source` is its one
  spelling; `failure` derives `NotDeleted` over `KeyRejected` from the provider's two flags.
- **One entry per provider in the OS keyring**: account `<id>-api-key` (`openrouter-saved-api-key`
  for OpenRouter, whose connector owns `openrouter-api-key`), `{"v":1,"key"}`. `account` is the one
  spelling; `TestTheAccountNames` pins it. The restore reads as the connectors do: a newer `v` is
  left and marked, so `Delete` leaves it too; an entry that does not parse, or holds a key `Save` would
  refuse, is deleted; an unreadable keyring writes nothing, and `Delete` still reaches its entry.
- **A provider is `SourceRestoring` until its own read has answered and its connector has
  restored.** A follower subscribes to `WatchSession` only after the connector's `Restored()`, and
  lands on its `RestoredSession()`, `landedGen`, which moves no `keyGen`. Until then `Save` and `Delete` answer
  `ErrRestoring`, `Sign` waits, and `Bind` is a `signer.Pending` of the session at the bind: the
  connector's binding says, or `landedGen` while the connector restores. `HasCredential` holds
  for the environment key alone meanwhile.
- **Two locks, taken in this order.** `writes` serializes `Save` and `Delete` and is the only lock
  held across a keyring call. `mu` guards every provider; each change is published under it, so the
  gauge never ends on an older list. Neither is held across a call to a connector.
- **A provider's key generation (`keyGen`) moves when its saved key in memory changes or its
  connector starts a session, and only then.** A key's binding (`keyBinding`) signs until it moves.
  `Delete` drops the key from memory and publishes first, so its bindings end whatever the keyring
  answers.
- **`Bind` returns the connector's binding unless it is `signer.Ended{}`**, the connector's contract
  for no session; a connector's binding is the connector's own.
- **A key goes on every route whose base covers the request, in that route's header**
  (`catalog.KeyedProviders`), and nowhere else: a request under no base is `errOffBase`, unsigned.
- **One `401` to the key that signs marks it rejected**; it keeps signing until a key is saved.
- **`WatchHasCredential` is `1` while the provider has a credential**, not a generation: a change of
  source keeps the catalog. `SyncCatalogs` runs `catalogsync` over it for every provider.
- Every saved key is registered with `safe.AddSecret` while held. Tests use
  `lib/oskeyring/oskeyringtest` and a fake connector (`testutil_test.go`).
