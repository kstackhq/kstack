# internal/services/connectors/openrouter — the OpenRouter connector

**A sign-in that grants a key, not an OAuth session.** OpenRouter's flow is PKCE alone; its
exchange answers an API key that does not expire. `Service` keeps it in the OS keyring and signs
the `openrouter` row with it (`llm.SessionSigner`). It shares `lib/oskeyring` and
`lib/oauth/browser` with the OAuth engine, and imports neither it nor `lib/oauth`.
→ [ADR: one package](../../../../../docs/adr/2026-11-19-a-connector-is-one-package-and-its-machinery-is-internal.md),
[ADR: a connector is an OAuth session or a granted key](../../../../../docs/adr/2026-11-19-a-connector-is-an-oauth-session-or-a-granted-key.md),
[security record](../../../../../docs/security/2026-11-19-the-openrouter-connector.md).

- **One store: an entry present is connected.** Account `openrouter-api-key`, `{"v":1,"key"}`.
  `New` reads it and reaches no network. A newer `v` is left in place; an entry that does not
  parse is deleted; a keyring that cannot be read writes nothing.
- **The environment key wins.** With `OPENROUTER_API_KEY` set the state is `FromEnvironment`, the
  entry is never read, and `Connect` and `Disconnect` are refused.
- **Every change runs under one lock against one generation.** `Connect`, `Cancel`, `Disconnect`
  and a rejected key bump it. A key that arrives after its attempt ended is dropped, never saved.
- **`Connect` checks the keyring before any browser opens.** It is refused during an attempt,
  while connected, from the environment, and in `Failed{NotDeleted}`. Our `state` rides the
  callback's query; only exactly one `code` is exchanged. The exchange follows no redirect, and
  only a `200` with a non-empty `key` connects (a `4xx` is `Refused`, the rest `Unavailable`).
- **`Sign` signs the API base alone**, while `Connected`. `Bind` is dead once its generation ends.
- **A `401` is confirmed before the key is dropped**: one `GET <base>/key` at a time, and only its
  own `401` deletes the entry (`KeyRejected`).
- **`Disconnect` drops the key from memory first**, then deletes the entry; with no key held it
  deletes nothing. A refused delete is `Failed{NotDeleted}` and returns the error; a restart then
  reconnects, and a retry deletes.
- Every key, code verifier and challenge is registered with `safe.AddSecret` and forgotten when
  cleared. Test seams are unexported options; tests run against a fake OpenRouter
  (`testutil_test.go`).
- **Not yet checked against OpenRouter**: the assumptions `docs/TODO.md` lists.
