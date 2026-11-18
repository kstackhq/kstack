# internal/services/connectors/oauthconn — signing in to a model provider

**A `Service` is one provider's OAuth sign-in, and signs that provider's requests**
(`llm.SessionSigner`). The engine names no provider: what one has of its own is its `Provider`
(`provider.go`), and `services/connectors/chatgptauth` is ChatGPT's. Files: `oauthconn.go` (Service, State),
`registration.go` (the row in `app.db`'s `oauth_connections`), `keyring.go`, `connect.go`,
`sign.go`, `disconnect.go`.
→ [ADR: the engine and its stores](../../../../../docs/adr/2026-11-18-an-oauth-connection-is-a-row-matched-to-its-keyring-entry.md),
[ADR: ChatGPT](../../../../../docs/adr/2026-11-18-the-chatgpt-connector-signs-in-with-chatgpts-dynamic-registration.md),
[security record](../../../../../docs/security/2026-11-18-the-chatgpt-connector.md).

- **A new provider is a `Provider` value**, a signer in `catalog.Config.Signers`, a row in
  `catalog`'s `signedProviders`, and a `catalogsync.Start`. Widen `Provider` only by what
  that provider's flow needs. The row's key and the keyring's accounts come from `Provider.ID`.
- **A session exists only when the row's `session_id` and the keyring entry's `session` match.**
  A sign-in mints the id and writes the entry, then the row, on every save. `New` reads both and
  reaches no network; any other entry is deleted. The access token is memory's alone.
- **Every change runs under one lock against one generation.** `Connect`, `Cancel`, `Disconnect`
  and a session's end bump it; whatever finishes applies only to the generation it began under.
  Late tokens are revoked; a failed revocation flags the state that ended them.
- **`Connect` writes a NULL `session_id` and checks the keyring before any browser opens**, and is
  refused during an attempt or while connected. The client id the callback issues is saved
  before the code exchange.
- **`Sign` signs the API base alone**, one refresh at a time under the service's own bound. A
  rotated refresh token is in the keyring before any access token is handed out. A refused
  refresh ends the session; an unreachable issuer clears nothing.
- **`Bind` is a signer for the current session**, dead outside `Connected`. `Disconnect` clears,
  then revokes on a goroutine.
- Every token is registered with `safe.AddSecret`. Test seams are unexported options; tests run
  a test provider against a fake issuer (`testutil_test.go`).
