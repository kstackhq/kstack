# internal/services/connectors/kstackcloud — the Kstack Cloud connector

**The Kstack Cloud account, signed in to by the OAuth engine under a fixed client.** It backs the
wire's `authState`, `authLoginStart`, `authLogout` and the gRPC `AuthService`, and signs no
request: the engine is an unexported field, and the provider has no API base.
→ [ADR](../../../../../docs/adr/2026-11-19-the-kstack-sign-in-is-a-connector-on-the-oauth-engine.md),
[security record](../../../../../docs/security/2026-11-19-the-kstack-sign-in-on-the-engine.md).

- **`New` deletes `auth-token`, then builds the engine.** That entry is the whole token
  `desktop/v0.0.1` kept; its user signs in again. A failed delete is logged at `Info` and tried
  again at the next launch.
- **`Connect` cancels, then starts fresh**, so a second request opens a new tab and a sign-in may
  change accounts. A mutex runs one at a time, so overlapping requests (the avatar's, the tray's)
  run in turn and never answer `ErrSigningIn`. A failure `Start` answers is `Connect`'s error.
- **`Subscribe` sends only a change of account**: connected, subject, email, name. The wire draws
  a sign-in under way, failed or cancelled as signed out. `State` is the engine's whole.
- Tests fake the engine (`engine`) and the keyring (`testutil_test.go`); the flow itself is
  `oauthconn`'s to test.
