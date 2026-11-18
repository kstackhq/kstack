# internal/services/connectors/chatgptauth — Sign in with ChatGPT

**Sign in with ChatGPT, whose tokens spend the user's plan.** It signs the `chatgpt` provider
(`llm.SessionSigner`). Files: `chatgptauth.go` (Service, State, the session's end),
`registration.go` (`<data>/chatgpt.json`), `keyring.go`, `connect.go`, `sign.go`,
`disconnect.go`. `sidecar/CLAUDE.md` is the whole sidecar.
→ [ADR](../../../../docs/adr/2026-11-18-the-chatgpt-connector-signs-in-with-chatgpts-dynamic-registration.md),
[security record](../../../../docs/security/2026-11-18-the-chatgpt-connector.md).

- **The file says whether a session exists; the keyring holds its refresh token.** Every save of
  the file is synced (`atomicjson.SaveSynced`) and unconditional. `New` reads both and reaches no
  network; an entry beside `connected: false` is deleted. The access token is memory's alone.
- **Every change runs under one lock against one generation.** `Connect`, `Cancel`, `Disconnect`
  and a session's end bump it; a sign-in, a refresh or a revocation that finishes applies only to
  the generation it began under. Late tokens are revoked; a failed revocation flags the state that ended them, never a later `Connect`.
- **`Connect` writes `connected: false` and checks the keyring before any browser opens**, and is
  refused during an attempt or while connected. Discovery happens at the first sign-in or
  refresh of a launch. The client id the callback issues is saved before the code exchange.
- **`Sign` signs the API base alone**, refreshing first when the token has expired: one refresh at
  a time, under the service's own bound, never a caller's. A rotated refresh token is in the
  keyring before any access token is handed out. A refused refresh ends the session; an
  unreachable issuer clears nothing.
- **`Bind` is a signer for the current session**, dead outside `Connected`. `Disconnect` clears,
  then revokes on a goroutine; one OpenAI never confirms sets `RevocationUnconfirmed`.
- Every token is registered with `safe.AddSecret` and forgotten once replaced. Test seams are
  unexported options (`withKeyring`, `withBrowser`, `withAttemptTimeout`,
  `withRegistrationWriter`); tests run against a fake issuer (`testutil_test.go`).
