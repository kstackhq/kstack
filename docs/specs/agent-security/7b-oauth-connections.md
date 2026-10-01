---
title: OAuth connections
scope: sidecar, host, webview
status: Planned
---

# OAuth connections

**Needs:** step 2D, whose `credentials` statuses and Settings rows this step extends, and step
6D, whose monitor session this step scopes a credential down for. **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

After steps 2D, 5C, 5D and 6C, every cloud and GitHub credential the proxies inject is borrowed from
the user's own tools (`aws configure export-credentials`, `gh auth token`, `gcloud auth
print-access-token`, `az account get-access-token`). That stays the default. [The
note](../../notes/sandbox-credentials-and-permissions.md) names three cases it does not
cover: a user with no CLI login on this machine; one who wants a token scoped to Kstack and
revocable from the provider's console, without touching their CLI login; and scoping down for
the monitoring session, so its read-only guarantee is the provider's as well as the proxy's.

After this step:

- **Settings: Credentials offers *Connect* per provider**: GitHub, Google, Azure, AWS. Each
  runs that provider's OAuth flow in the sidecar, opens the system browser, and keeps the
  tokens in the OS keychain under `internal/auth`'s keyring, read by `credentials` alone.
- **A connection is optional and per provider.** `Credentials.Sources` in `sandboxconfig`
  says, per provider, whether the injectors borrow the CLI's credential or use the connection.
  The default is `cli`, and a connection does not change it until the user picks it.
- **The monitor session gets a read-only credential where the provider can express one**: a
  read-only GitHub App, Google's `cloud-platform.read-only` scope, and for AWS an `AssumeRole`
  into a role the user names with `ReadOnlyAccess` as a session policy. `credentials.ForSession`
  never hands a monitor session a full credential in place of a scoped one that failed.
- **Disconnect** revokes at the provider where a public client can, then deletes the entry.

The note's invariant this step pins: *OAuth tokens are stored in the OS keychain and read only
by the proxy.* Nothing writes a token to disk, and `TestNoCredentialIsWrittenToDisk` grows a
case per provider.

On Windows the flows run, since the sidecar does, but no proxy confines anything there, so a
connection is kept and shown and reaches no command.

## What is not in this step

- **No borrowing or injection changes.** Steps 1D, 2D, 5C, 6B and 6C's borrowing, statuses and expiry
  surfacing stay; the injectors and `awsproxy` keep their one call into `credentials`, and
  this step changes what it answers.
- **No Kubernetes connection.** The cluster's credential is the kubeconfig's.
- **No GitHub Enterprise Server.** A connection is to `github.com`; `GH_HOST` stays open.

## Design

### 1. The registered apps, and where their ids live

Each provider needs an app registered before a real flow can run (Decisions, 1):

| Provider | App | Why this kind | Ids the host passes |
| --- | --- | --- | --- |
| GitHub | A GitHub App with the device flow enabled and user-to-server tokens | An OAuth App's scopes cannot make private repositories read-only: `repo` is read and write, and nothing narrower reaches a private repository. A GitHub App's user token holds the intersection of the app's fine-grained permissions and the user's access, and a device-flow token refreshes with no client secret | `GITHUB_APP_CLIENT_ID`, and `GITHUB_READONLY_APP_CLIENT_ID` for a second App whose every permission is read (§5) |
| Google | An OAuth client of type *Desktop app* | The loopback flow the app's own login already uses. Google issues a Desktop client a secret it documents as not confidential, and its token endpoint requires it | `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET` |
| Azure | A public client application with *Allow public client flows* on | The device code grant needs a public client | `AZURE_APP_CLIENT_ID` |
| AWS | None registered ahead: IAM Identity Center's `RegisterClient` registers a public client at first use, per Identity Center instance | That is what `aws sso login` does; the client secret it answers is one more keyring entry (§3) | none |

The ids are constants in `src-tauri/src/services/sidecar/service.rs`, beside `OAUTH_CLIENT_ID`,
passed by `cmd_args` as `--github-client-id`, `--github-readonly-client-id`,
`--google-client-id`, `--google-client-secret` and `--azure-client-id`, and read by
`configFromArgs` in `sidecar/config.go` into `app.Config`. **None is read from the
environment**, as `src-tauri/CLAUDE.md` has it for every endpoint and id. The providers'
endpoints (`github.com`, `accounts.google.com`, `login.microsoftonline.com`,
`oidc.<region>.amazonaws.com`) are code constants in `credentials`, each flow taking an
`Endpoints` value a test points at `httptest`. A build whose id for a provider is empty
offers no *Connect* for it (*Not available in this build*), so the code lands and its tests
run before any registration exists.

### 2. The flows

`credentials/oauth.go` holds what the four share; each provider is its own file. A flow is a
`Connect(ctx, opts) (*Flow, error)` that starts at once and reports through a state the wire
reads (§7):

```go
// Flow is one connection being made: what to show the user, and where it is.
type Flow struct {
	Provider                  credentials.Provider
	ReadOnly                  bool      // the monitor's scoped-down connection (§5)
	UserCode, VerificationURL string    // a device flow's; empty for the loopback flow
	ExpiresAt                 time.Time
	State                     FlowState // Waiting, Done, Failed or Expired
	Reason                    string    // names a failure
}
```

The sidecar opens the browser itself, with the opener `internal/auth` has for the app's own
login (`login.go`'s `defaultOpenBrowser`, exported as `auth.OpenBrowser`), on a URL the flow
built from the provider's constant endpoint; the webview supplies no URL, and the host's
`open_account_url` stays what it is (Decisions, 2). One flow per provider at a time: a second
`Connect` while one waits is `KSTACK_VALIDATION_ERROR`. A flow ends at `ExpiresAt`, or when
the user closes the dialog, which cancels it.

| Provider | File | Flow |
| --- | --- | --- |
| GitHub | `oauth_github.go` | Device flow: `POST /login/device/code` with the client id, then `POST /login/oauth/access_token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code` every `interval` seconds, five more on `slow_down`, until the token, `expired_token` or `access_denied`. A refresh is the same endpoint with `grant_type=refresh_token`, no client secret, since the token came from the device flow |
| Google | `oauth_google.go` | The loopback flow `internal/auth` runs: PKCE (S256), a `state` the loopback callback checks before it consumes a code, a listener on `127.0.0.1:0` bound before the URL is built, `access_type=offline` and `prompt=consent` so a refresh token comes back. Endpoints: `accounts.google.com/o/oauth2/v2/auth`, `oauth2.googleapis.com/token`, `oauth2.googleapis.com/revoke`. Scope `cloud-platform`, or `cloud-platform.read-only` for the monitor's |
| Azure | `oauth_azure.go` | Device code grant on `login.microsoftonline.com/<tenant>/oauth2/v2.0/devicecode` and `/token`, polling as GitHub's does (`authorization_pending`, `authorization_declined`, `expired_token`). The one sign-in asks consent for both resources step 6C injects: scope `https://management.azure.com/user_impersonation`, the registration's delegated Microsoft Graph permissions (`azureGraphScopes`), and `offline_access`. A token is for one resource, so each resource's token is its own refresh-token grant with `scope=<resource>/.default` (§3). The tenant is `organizations` unless `Credentials.AzureTenant` in `sandboxconfig` names one |
| AWS | `oauth_aws.go` | IAM Identity Center's device authorization, as `aws sso login` runs it: `RegisterClient` (`clientType: public`, grant types device code and refresh token) once per start URL and region, its client id and secret kept in the keyring until `clientSecretExpiresAt`; `StartDeviceAuthorization` with the user's start URL, opening `verificationUriComplete`; `CreateToken` with the device code grant, polling on `authorization_pending` and `slow_down`. The SSO access token and its refresh token are the connection. Credentials for a profile are `GetRoleCredentials(accessToken, accountId, roleName)`, the account and role picked in Settings from `ListAccounts` and `ListAccountRoles` |

`internal/auth`'s flow is not reused whole, since it verifies an ID token against Hydra and
stores one token set; its loopback server and opener are exported (`auth.NewLoopback`,
`auth.OpenBrowser`), and `auth/oauth` stays a leaf `credentials` does not import.

### 3. Tokens: the keyring, refresh, never disk

`auth.Keyring` is the store step 5D exported for the CA's key (`NewKeyring(service)`, with
`Get`, `Set` and `Delete` by account name), under the `--keychain-service` the host already
passes, so a dev build's connections sit apart from a release's. The accounts:

| Account | Holds |
| --- | --- |
| `connection-<provider>` | access token, refresh token, expiry, the identity the provider reported, the scopes or permissions granted, `connectedAt` |
| `connection-azure` | as above, but an access token and expiry per resource: ARM's and Graph's, off the one refresh token |
| `connection-<provider>-readonly` | the monitor's scoped-down connection (§5) |
| `connection-aws-client` | the registered client id and secret, with `clientSecretExpiresAt` |
| `connection-aws-roles` | nothing secret: the account and role per profile; kept here so the keyring is the one place a connection lives |

`credentials` is the one reader. A token is refreshed by the sidecar before expiry, as
`auth/grant.go` refreshes: a reader that finds the access token within two minutes of expiry
refreshes it first, one refresh at a time per connection, and writes the keyring before it
hands the token out. A refresh the provider refuses marks the connection `expired` (§4).

**Azure's tokens are per resource**, as step 1D's `Azure(ctx, resource)` borrow is. The reader
keys the refresh on the resource: a Graph token is never answered for ARM, or ARM's for Graph.
Each resource refreshes on its own, one at a time per connection and resource, and each grant's
new refresh token replaces the kept one. A grant refused for want of consent
(`consent_required`) fails that resource alone: the other keeps working, and the row says
*Connect again to grant Microsoft Graph*. Every
provider call goes through one `http.Client` with a 15 s timeout, as `auth/oauth` has.

**Nothing writes a token anywhere but the keyring.** Not `sandbox.json`, not `app.db`, not a
log: `safe.AddSecret` registers each token as it is read. Step 1D's
`TestNoCredentialIsWrittenToDisk` gains a case per provider that runs the flow against a fake
and the keyring fake, then walks the data, cache and runtime directories.

### 4. Precedence: the source is a setting

`sandboxconfig.Settings.Credentials` (step 2D) gains:

```go
type CredentialSettings struct {
	// … step 2D's exclusions
	Sources        map[credentials.Provider]credentials.Source `json:"sources,omitempty"` // cli when absent
	AzureTenant    string                                      `json:"azureTenant,omitempty"`
	AWSStartURL    string                                      `json:"awsStartUrl,omitempty"`
	AWSRegion      string                                      `json:"awsRegion,omitempty"`
	MonitorRoleARN map[string]string                           `json:"monitorRoleArn,omitempty"` // by AWS profile
}
```

`credentials.Source` is `cli` or `connection`. The one call the injectors and `awsproxy` make,
step 5C's reader, follows it: `cli` borrows as today; `connection` answers the keyring's
token, refreshed as §3 says. A provider set to `connection` with none reads `missing` in step
2D's statuses, and its row says *Connect*; one whose refresh was refused reads `expired`, and
the row's button is *Connect again*, the flow again into the same account. Picking either
source never touches the other: the CLI's login is not changed, and a connection stays in the
keyring. `Sources` and `Flow` are keyed by step 1D's `credentials.Provider`, which names a
tool's login (`aws`, `github`, `gcp`, `azure`), not by `permissions.Provider`, which names
whose action a request is; the two spell each provider alike.

### 5. Scoping down for the monitor

A monitor session (step 6D, `Kind: Monitor`) reads, and the proxies refuse its writes. This
step adds the provider's own refusal beside the proxy's, where one can express it:

| Provider | The scoped-down credential | What the app can and cannot do |
| --- | --- | --- |
| GitHub | A second connection through the read-only GitHub App (§1), whose every permission is read. Its row is *Connect for monitoring* | A GitHub App has one permission set, chosen at registration, and the user cannot narrow it at authorization. So read-only is a second App, not a choice on the first |
| Google | A second connection with the scope `https://www.googleapis.com/auth/cloud-platform.read-only` | The scope is the provider's, so a write with this token fails at Google |
| Azure | None | A token's rights are the user's Azure RBAC role assignments; `Reader` is assigned to the user by an administrator, not requested by an app. Settings says so under the Azure row: *Azure enforces read-only by the roles assigned to you; Kstack's proxy refuses writes for the monitor either way* |
| AWS | `AssumeRole` into `MonitorRoleARN[profile]` with `PolicyArns` holding `arn:aws:iam::aws:policy/ReadOnlyAccess`, so the session's rights are the intersection of the role's and read-only. The credentials assuming it are the profile's, from either source, so this works with a CLI login too | The user names the role in Settings; a profile with none named gets no scoped credential |

```go
// Request names one credential: the provider, and the argument its step 1D
// reader keys on. The field for another provider is empty.
type Request struct {
	Provider Provider
	Profile  string // AWS: the profile
	Host     string // GitHub: the host
	Resource string // Azure: the token's audience, ARM's or Graph's
}
```

`credentials.ForSession(ctx, monitor bool, req credentials.Request) (Credential, error)` is what
every injector calls in place of the plain reader once sessions exist, passing
`s.Kind == session.Monitor` and the request it would have passed that reader: `awsproxy` the
profile, the GitHub injector the host, the Azure injector the resource of the host it injects.
Both sources key on it alike: the `cli` source calls step 1D's reader with it, the `connection`
source reads the profile's picked account and role (§2) or the resource's token (§3), and the
monitor's AWS role is `MonitorRole(req.Profile)`. `credentials` stays a leaf (step 1D): it imports neither `session`
nor `sandboxconfig`, and reads the sources and the monitor's roles through hooks on the store,
`Source func(Provider) Source` and `MonitorRole func(profile string) string`, which `app` sets to
reads of `Settings.Credentials`, as step 2D sets `Excluded`. `sandboxconfig` importing
`credentials` for the two types is the direction that leaves no cycle.

- A chat's or a subagent's session: the provider's credential by its source (§4).
- A monitor's session: the scoped-down credential where one is configured. **A configured
  scoped-down credential that fails answers `ErrNoScopedCredential`, never the full one.**
  With none configured the monitor gets the full credential and the proxy alone holds
  read-only, as step 6D has it, and step 2D's status says *read-only by the proxy alone*.

### 6. Settings: Credentials

`credential-settings.tsx` (step 2D) gains, per provider row:

- **Source**: *Use my CLI login* / *Use the connection*, calling `credentialSourceSet`;
  the second is disabled while there is no connection.
- **Connect with GitHub / Google / Azure / AWS**: opens a dialog that calls
  `credentialConnect` and subscribes to `credentialConnectWatch`. A device flow shows the
  user code large, the verification URL as a link the sidecar's opener opens again on click,
  and *Waiting for you to approve in the browser…*; the loopback flow shows the waiting line
  alone. Done closes it; a failure names its reason.
- **Once connected**: the identity (`@alice`, the Google account, the Azure UPN, the AWS
  start URL with the picked account and role), the scopes or permissions, *connected <date>*,
  and **Disconnect**. AWS's row picks the account and role per profile and names the monitor
  role ARN; GitHub's and Google's add *Connect for monitoring* with its own identity.
- **Disconnect** revokes where an endpoint takes a public client's request, then deletes the
  keyring entry: Google's `/revoke` with the refresh token; AWS's `Logout` with the access
  token, as `aws sso logout` does. Azure has no such endpoint, and GitHub's `DELETE
  /applications/{client_id}/token` wants the app's client secret, which a public client does
  not hold: both delete the entry, and the row says *Revoke it in <provider>'s settings too*.

### 7. The wire

```graphql
enum CredentialSource { Cli Connection }
enum CredentialFlowState { Waiting Done Failed Expired }
"A device flow's code and URL; both null for the loopback flow."
type CredentialConnectStart { userCode: String, verificationUrl: String, expiresAt: Time! }
"reason names a failure in the sidecar's words, empty otherwise."
type CredentialFlow { provider: CredentialProvider!, readOnly: Boolean!, state: CredentialFlowState!, reason: String! }
"expired: the refresh was refused; Connect again."
type CredentialConnection { identity: String!, scopes: [String!]!, connectedAt: Time!, expired: Boolean! }

extend type Mutation {
  credentialConnect(provider: CredentialProvider!, readOnly: Boolean!): CredentialConnectStart!
  credentialDisconnect(provider: CredentialProvider!, readOnly: Boolean!): Boolean!
  credentialSourceSet(provider: CredentialProvider!, source: CredentialSource!): Boolean!
}
extend type Subscription {
  "A gauge, not a delta watch: the flow's state now, then each change."
  credentialConnectWatch(provider: CredentialProvider!, readOnly: Boolean!): CredentialFlow!
}
```

`CredentialProvider` is step 2D's enum, and step 2D's `CredentialState` gains `connection:
CredentialConnection` (null for none) and `source: CredentialSource!`. No field carries a token: `TestCredentialProjectionCarriesNoTokens`, as
`TestAuthProjectionCarriesNoTokens` does for sign-in.

## Decisions this step asks for

1. **Which apps exist, and who registers them.** This is the gate for the step. The flows
   land and test against fakes without any; a provider whose id constant is empty is not
   offered. Recommended: the GitHub Apps and the Google client first, since those two give
   scoping down; Azure when a user asks; AWS needs no registration.
2. **The sidecar opens the browser, not the host.** The host is the gRPC client, so the
   sidecar has no call into it, and `internal/auth` already opens the browser from the sidecar
   for the app's login. The invariant is the same: the URL is the sidecar's, never the
   webview's. Recommended.
3. **What was checked against the providers' docs.** GitHub: an OAuth App's scopes cannot
   make a private repository read-only, a GitHub App's permissions are fixed at registration,
   a device-flow user token refreshes with no client secret, and revoking one needs the
   secret — so read-only is a second App. Google: the `cloud-platform.read-only` scope exists,
   and a Desktop client's secret is documented as not confidential and asked for by the token
   endpoint, so it rides `cmd_args` as its id does. AWS: `AssumeRole`'s session is the
   intersection of the role's policy and `PolicyArns`, and works from a borrowed CLI
   credential too, so it is the first scoped-down credential most users get; the device
   authorization and `GetRoleCredentials` shapes are the OIDC and Portal references'. Azure:
   the device code grant as documented; `Reader` is the user's assignment, not the app's
   request, so Azure gets no scoped credential and Settings says so.
4. **Azure's connection is one sign-in for both resources.** The device code request asks
   consent for ARM and Microsoft Graph together, and each resource's token comes off the one
   refresh token. The alternative is a sign-in per resource, which Settings would show as two
   Azure connections. Recommended; the implementation first confirms against a real tenant
   that the device code grant takes both resources' scopes in one request, and falls back to
   a sign-in per resource if not.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The host passes the ids; the config reads them | `src-tauri/src/services/sidecar/service.rs`, `sidecar/config.go`, `app/app.go`, their tests | — | Planned |
| 2 | `auth.NewLoopback` and `auth.OpenBrowser` exported, beside step 5D's `auth.Keyring` | `auth/login.go`, its tests | — | Planned |
| 3 | `credentials/oauth.go`: `Flow`, the keyring accounts, refresh | `credentials/oauth.go`, `credentials/oauth_test.go` | 2 | Planned |
| 4 | The four flows, each against a fake | `credentials/oauth_github.go`, `oauth_google.go`, `oauth_azure.go`, `oauth_aws.go`, their tests | 3 | Planned |
| 5 | `Sources` and the AWS and Azure settings; the reader follows the source; `ForSession` | `sandboxconfig/`, `credentials/`, `awsproxy/`, `egress/`, their tests | 3 | Planned |
| 6 | The wire and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 4, 5 | Planned |
| 7 | The Settings rows and the connect dialog | `src/components/widgets/credential-settings.tsx`, its test | 6 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4 and 5 at the same time, then 6, 7, 8.

## Tests

**`credentials`**

- `TestTheGitHubDeviceFlowPollsUntilTheToken`: against an `httptest` provider, the code
  request, polling at `interval`, `slow_down` adding five seconds, `expired_token` ending
  `Expired`, `access_denied` ending `Failed`, and the token landing in the keyring fake. No
  sleeps: the poller's clock is a parameter.
- `TestTheGoogleFlowChecksPKCEAndState`: the challenge is S256 of the verifier, a callback
  with another `state` is refused without consuming the code, and the exchange carries the
  verifier.
- `TestTheAzureDeviceFlowNamesItsTenant`, and polls as GitHub's does.
- `TestAnAzureConnectionAnswersATokenPerResource`: ARM's request and Graph's each answer a
  token from a grant naming that resource's `.default`, each cached and refreshed on its own; a
  grant refused `consent_required` for Graph leaves ARM's answering.
- `TestForSessionKeysOnTheRequest`: at both sources, AWS profiles `dev` and `prod` each answer
  their own credential, the monitor's role is the one named for the request's profile, and
  Azure's ARM and Graph requests each answer their own resource's token.
- `TestTheAWSFlowRegistersOnceAndKeepsTheClient`: `RegisterClient` once per start URL and
  region, its secret kept until it expires, and `GetRoleCredentials` with the picked account
  and role.
- `TestARefreshRunsBeforeExpiryAndWritesTheKeyringFirst`, one at a time, and
  `TestARefusedRefreshMarksTheConnectionExpired`.
- `TestTheSourceDecidesWhatIsInjected`: `cli` borrows, `connection` reads the keyring, and a
  connection set with none is `missing`.
- `TestTheMonitorGetsTheScopedCredential`, per provider that has one, the AWS case asserting
  `PolicyArns` holds `ReadOnlyAccess`; and `TestTheMonitorNeverFallsBackToTheFullCredential`:
  a configured scoped-down credential that fails answers `ErrNoScopedCredential`, and with
  none configured the full one is answered.
- `TestDisconnectRevokesThenDeletes`: Google's revoke with the refresh token before the
  delete; GitHub's and Azure's delete alone.
- `TestNoCredentialIsWrittenToDisk` (step 1D's), a case per provider, and
  `TestAnEmptyClientIdOffersNoConnect`.

**`auth`** and **`app`**

- Every existing keyring test, unchanged under the exported name; and
  `TestCredentialProjectionCarriesNoTokens`.

**Webview** (`credential-settings.test.tsx`)

- The source picker calls the mutation and is disabled at *connection* with none.
- The connect dialog shows a device flow's code and URL, the loopback flow's waiting line,
  closes on `Done`, names a `Failed` reason, and *Connect again* on an expired connection.
- Disconnect names the provider's settings page where Kstack could not revoke.

## Security

This step widens one thing: the sidecar holds long-lived provider tokens of its own, where
before it only borrowed the CLI's at use time. What holds it: the tokens are in the OS keychain
under the app's own service, read by `credentials` alone, refreshed by the sidecar, never on
disk (`TestNoCredentialIsWrittenToDisk`), never on the wire
(`TestCredentialProjectionCarriesNoTokens`), and blanked in logs by `safe.AddSecret`; the
browser opens on a URL the sidecar built, never the webview's. It narrows one: the monitor's
read-only is GitHub's, Google's and AWS's beside the proxy's, and `ForSession` never
substitutes a full credential for a scoped one that failed.

Residuals: a GitHub or Google token's scope is what the user granted, and the full connection
is as wide as the CLI's login; Azure's monitor credential is read-only by the proxy alone; a
keyring the OS unlocks is readable by any process running as the user, as the sign-in token
already is; a revoke Kstack cannot make (GitHub, Azure) leaves a deleted token live at the
provider until the user revokes it there.

The record, `docs/security/<date>-oauth-connections.md`, argues this.

## When it lands

- **The security record** above, and an ADR: a connection is an optional second source,
  never the entry ticket; read-only for the monitor is the provider's where it can be.
- **`security-model.md`**: the keyring row, the credential-source row, the monitor's
  scoped-credential row with `TestTheMonitorNeverFallsBackToTheFullCredential`, and the
  endpoints-are-arguments row gaining the new ids.
- **`sidecar/CLAUDE.md`**: `credentials`' flows, the keyring accounts, refresh, `Sources`,
  `ForSession`; `auth`'s exported keyring, loopback and opener. **`src-tauri/CLAUDE.md`**:
  the new `cmd_args` constants. **Root `CLAUDE.md`**, the Settings dialog: the rows.
- **`docs/TODO.md`**: the OAuth proactive-refresh item is answered for the connections here.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire
checks; the flows' tests run against fakes in every job.

By hand, `pnpm tauri dev` with a dev build whose constants name a registered GitHub App: in
Settings, *Connect with GitHub* shows a code, the browser opens on GitHub's device page, and
the row reads `@you` once approved; set the source to the connection and read `gh api user`
in a sandboxed chat answer as you; Disconnect, and read the row empty and the CLI's login
still working at `cli`. With a monitor role ARN named for an AWS profile, read the monitor
session's `aws sts get-caller-identity` answer the assumed role.
