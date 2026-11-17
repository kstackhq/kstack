# Step 2C: Google Cloud through the token-injecting proxy

Answers [#933](https://github.com/kstackhq/kstack/issues/933). Step 2C of
[the cloud proxies](README.md), on `wip/cloud-proxies`. It needs step 1 alone, and can be built
beside the wave's other steps (`2a-aws.md`, `2b-github.md`, `2d-azure.md`).

[Step 1](1-shared-pieces.md) owns the door, the grant, the gate, the request's shape and
drawing, the consent switch and the cloud platform seam. This step states only what is Google's, and
links step 1 for the rest.

This step changes a security boundary: it lends the user's Google login to sandboxed commands.
It lands with an ADR, a `docs/security/` record and its rows in
[`security-model.md`](../../security-model.md) ([*When it lands*](#when-it-lands)).

Go paths are under `sidecar/internal/` unless rooted elsewhere.

## What is true now

Each line is read from the code. Step 1's
[*What is true now*](1-shared-pieces.md#what-is-true-now) covers the run, the relay and the
request.

- **A sandboxed `gcloud` finds no account.** `~/.config/gcloud` is on `Never`
  (`run/sandbox/lists.go`). The SDK itself is readable: `~/google-cloud-sdk` is a `Toolchain`
  location.
- **`HOME` is the workspace** (`sandboxedRunEnv`, `tools/bash/env.go`), so `gcloud` would keep
  its configuration in `<workspace>/.config/gcloud`. `toolHomeVars` (`tools/bash/toolhome.go`)
  has no row for it.
- **GKE works already.** The cluster proxy forwards on a connection whose `exec` plugin,
  `gke-gcloud-auth-plugin`, runs in the sidecar. The login shell imports what that plugin
  reads: `CLOUDSDK_CONFIG`, `CLOUDSDK_CORE_PROJECT` and `GOOGLE_APPLICATION_CREDENTIALS`
  (`run/loginshell/loginshell.go`, `imported`).
- **No `CLOUDSDK_*` name is on `neverEnv`** (`run/sandbox/env.go`).
- **The prompt says a tool has a login only where the context's `cloud` names its cloud platform**
  (`tools/bash/prompts/sandbox.md`). No build writes that key yet.
- **Step 1 built the shared pieces and no cloud platform.** `sandbox.gcp` exists and is off, and
  `permissions.GCP` is a `Target`. `app/runtime.go` hands `bash.New` a `bash.Cloud` whose
  `Platforms` is
  `cloudPlatformsFor`'s: the build's list, `builtCloudPlatforms` (`app/cloudplatforms_unix.go`), or a
  test's `Config.cloudPlatforms` in its place, and none on a machine with no sandbox. The
  wire lists a switch per cloud platform of that list, and Settings draws none yet
  ([`TODO.md`](../../TODO.md), *Draw each cloud platform's switch in Settings*).
- **Google's name and refusals are written.** `tools/bash/cloud.go` holds *Google Cloud*
  (`platformNames`) and fills a mount's `CloudRun.Asker` and `Refusal` (`cloudAskerFor`). The
  webview holds the name too (`CLOUD_PLATFORM_NAMES`, `src/lib/chats.tsx`).

## What this step builds

With `sandbox.gcp` on, on macOS and Linux, `gcloud` in a sandboxed run works as the user's
active `gcloud` account on six services, with network off and no Google credential in the
sandbox.

1. **The token service** (`services/gcptoken`): the active account's access token and
   project, read outside the sandbox and kept in memory.
2. **The proxy** (`run/gcpproxy`): the service table, the classifier, the handler and the
   rewriter that blanks secret-holding metadata in an answer, over a
   [`runproxy.Grant`](1-shared-pieces.md#3-the-grant-core-runrunproxy).
3. **The cloud platform** (`tools/bash/gcp.go`): Google's
   [`CloudPlatform`](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash), its routes and the
   run's `CLOUDSDK_*` variables. `app/runtime.go` adds it to the list it builds.
4. **The prompt's paragraph** for Google.

### Not in this step

- **Handing the sandbox a live token**, by `CLOUDSDK_AUTH_ACCESS_TOKEN_FILE` or a metadata
  server.
- **Client libraries, `gsutil`, `bq`, `terraform`.** A program that takes an endpoint and a
  bearer token can use the run's by hand. Nothing sets it up.
- **Any other Google service**: Secret Manager, Cloud Functions, Cloud Run, Deployment Manager
  and every token endpoint among them. Also regional and mTLS endpoints, Cloud Storage's XML
  API, batch requests and gRPC (`logging`'s tail).
- **Uploading an object that is not text under 1 MiB**, and resumable uploads.
- **Application Default Credentials.** The token is the active `gcloud` account's.
- **A narrower identity** (impersonating a service account).
- **Credential status or re-login in the app.** A missing login is a `503` the command prints.
- **Opening any part of `~/.config/gcloud`.** `Never` and `neverEnv` stay as they are.
- Anything of `settings`, the wire, the webview or `run/sandbox`. Step 1 built what Google
  uses of each.

## Design

### 1. The token service (`services/gcptoken`)

It follows step 1's [rules for a
credential](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash). `run/gcpproxy` reaches it
through an interface.

```go
// Source answers the active gcloud account's access token and project, read
// outside the sandbox and kept in memory.
type Source struct{ /* run func(ctx) ([]byte, error), now func() time.Time */ }

func (s *Source) Token(ctx context.Context) (Token, error)
func (s *Source) Drop(token string) // forget token, if it is still the cached one

type Token struct {
	Value   string
	Project string // core/project of the active configuration; "" for none
	Account string // core/account, drawn on a request
	Expiry  time.Time
}
```

- It runs `gcloud config config-helper --format=json --min-expiry=10m` as a child of the
  sidecar, with the sidecar's environment. `gke-gcloud-auth-plugin` runs the same command, so
  the account, the configuration and the refresh are the ones a GKE connection already uses.
  `gcloud` is found on the sidecar's `PATH`.
- The answer is cached until `token_expiry` less a minute. Concurrent callers share one run
  (`singleflight`). The child is bounded at 30 s and its output at 64 KiB.
- A held token is registered with `safe.AddSecret`. `Drop`, and a refresh that replaces a
  token, forget the old one with `safe.ForgetSecret`.
- Errors are sentinels the proxy words itself: `ErrNoCLI` (no `gcloud` on `PATH`),
  `ErrNoAccount` (the helper exits non-zero), `ErrTimeout`. The helper's stderr is never
  returned or logged.
- `app` builds one `Source`. Its `run` and clock are fields, so no test starts a real `gcloud`.

### 2. The services and their routes

One table, `run/gcpproxy/services.go`. A row is the name `gcloud` uses in an endpoint
override, the one upstream host, and the path prefixes served.

| Service | Upstream | Paths |
| --- | --- | --- |
| `compute` | `compute.googleapis.com` | `/compute/` |
| `container` | `container.googleapis.com` | `/v1/`, `/v1beta1/` |
| `iam` | `iam.googleapis.com` | `/v1/`, `/v2/`, `/v3/` |
| `logging` | `logging.googleapis.com` | `/v2/` |
| `storage` | `storage.googleapis.com` | `/storage/v1/`, `/download/storage/v1/`, `/upload/storage/v1/` |
| `cloudresourcemanager` | `cloudresourcemanager.googleapis.com` | `/v1/`, `/v2/`, `/v3/` |

- The first five are the issue's. `cloudresourcemanager` holds a project's IAM policy
  (`gcloud projects get-iam-policy`).
- **A row's route is `<service>.gcp.kstack.invalid`**, by step 1's
  [naming rule](1-shared-pieces.md#1-the-door): `gcloud` takes an endpoint per service and a
  proxy of its own, so Kstack picks the name. The mount returns one `Route` per row.
- **The upstream is read from the table by the `Host`'s first label**, never built from the
  request.
- **The paths keep the classifier's reading true.** `storage.googleapis.com` also serves the
  XML API, where `GET /<bucket>/<object>` answers an object's contents with no `alt=media`. A
  path off its row's list is refused, and so is any `/batch/` path, whose body holds requests
  the classifier does not read.

### 3. The run's environment

Google's mount returns these rows. Every value is the mount's.

| Variable | Value | Why |
| --- | --- | --- |
| `CLOUDSDK_AUTH_ACCESS_TOKEN` | the grant's `Token()` | The grant's one slot: `gcloud` sends it as `Authorization: Bearer` on every request and consults no credential store |
| `CLOUDSDK_API_ENDPOINT_OVERRIDES_<SERVICE>` | `http://<service>.gcp.kstack.invalid/`, one per table row | The request names a host the door routes |
| `CLOUDSDK_PROXY_TYPE`, `CLOUDSDK_PROXY_ADDRESS`, `CLOUDSDK_PROXY_PORT` | `http`, `127.0.0.1`, the run's port | `gcloud` alone is pointed at the relay. `HTTP_PROXY` stays unset, so no other tool changes |
| `CLOUDSDK_CORE_PROJECT` | the source's `Project`, left out when empty | A command needs no `--project` |
| `CLOUDSDK_CORE_DISABLE_PROMPTS` | `1` | stdin is the null device |
| `CLOUDSDK_CORE_DISABLE_USAGE_REPORTING`, `CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK` | `true` | Neither host is on the table |

- **`CLOUDSDK_CONFIG` is a `toolHomeVars` row**, `gcloud`, so every run has it: a writable
  configuration directory outside the workspace, made with the tool home's other folders.
- The override is the root of a host, so `gcloud` appends each API's own versioned path
  (`compute/v1/`, `compute/beta/`, `v1/`).
- The mount writes no file.
- **The project is read at the mount**, through `Source.Token`, under the run's context. This
  is the one borrow a request does not start ([*Open questions*](#open-questions), 1). A failed
  borrow leaves `CLOUDSDK_CORE_PROJECT` out, and the first request answers the `503` below.
- A `gcloud` command on a service off the table, with network, tunnels through the same proxy
  setting. It gets the door's `401` for a `CONNECT`, before any TLS and so before the bearer is
  sent.
- `gcloud container clusters get-credentials` writes a kubeconfig whose plugin answers the
  run's bearer, which no cluster accepts. The chat's cluster is reached through its own
  kubeconfig, as today.

These rows rest on documented `gcloud` properties and on four behaviours no test here has run.
Task 1 checks them against a real `gcloud` before anything else is built:

1. `CLOUDSDK_AUTH_ACCESS_TOKEN` gets every listed command past the auth check with an empty
   `CLOUDSDK_CONFIG`. Fallback: `CLOUDSDK_AUTH_ACCESS_TOKEN_FILE` naming a 0600 file in the run
   directory that holds the grant's token.
2. A root-path override over plain HTTP gets the API's versioned path appended, for `compute`
   in its `v1` and `beta` tracks and for `container`.
3. `CLOUDSDK_PROXY_*` sends a plain-HTTP request to the proxy in absolute form, and
   `gcloud storage` honours the override and the proxy as the other commands do.
4. No listed command sends a request body with a `Content-Encoding`, which
   [`Grant.Body`](1-shared-pieces.md#what-the-user-approves-is-what-is-sent) refuses.

If 2, 3 or 4 fails, the design changes and this spec is revised before task 2.

### 4. The proxy (`run/gcpproxy`)

A leaf over a [`runproxy.Grant`](1-shared-pieces.md#3-the-grant-core-runrunproxy) with `Target`
`GCP`. It imports `permissions`, `session` and `runproxy`, and nothing of `tools` or
`services`. The grant gives it the token, the life, the limits and the ask path. The mount
builds it with no challenge ([§5](#5-the-cloud-platform-toolsbashgcpgo)).

```go
// TokenSource is the user's token, borrowed at use.
type TokenSource interface {
	Token(ctx context.Context) (gcptoken.Token, error) // the fields, as the leaf's own type
	Drop(token string)
}

func New(grant *runproxy.Grant, src TokenSource, transport http.RoundTripper) *Handler
```

**The handler, in order:**

1. `Authorization` is `Bearer <token>` and `grant.Holds` it, else `grant.Challenge`. Then
   `grant.Enter`.
2. The request is refused `403` when its path is not canonical (`path.Clean` changes it, or it
   holds an encoded `/` or `.`), is off its service's paths, or is under `/batch/`; when it
   carries `Upgrade` or `Connection: upgrade`; when `url.ParseQuery` refuses its query; or when
   the query holds `access_token`, `oauth_token` or `key`, each a credential of the command's
   own choosing.
3. `grant.Body` reads the body.
4. `classify` (below) answers read, change, data read or refused. A refused request is `403`.
5. A read takes `grant.Slot` and forwards.
6. A change or a data read borrows the token first, for the account it draws, then calls
   `grant.Decide` with the action and the request below. Only an approval forwards.
7. Forwarding borrows through `src.Token`. A sentinel is a `503` with its own fixed sentence
   (*gcloud is not installed on this machine*; *gcloud has no signed-in account; the user can
   run `gcloud auth login`*).
8. The request goes over HTTPS to the table's host: the path and query as sent, the headers
   `runproxy.Outbound` keeps, and `Authorization` set to the user's token. The transport is one
   per sidecar with `Proxy: http.ProxyFromEnvironment`, so a corporate proxy the login shell
   imported applies. It follows no redirect. An upstream failure is a `502` with a fixed
   sentence.
9. A `401` from upstream calls `src.Drop` and passes through. The next request borrows again.

The answer streams back unread (`FlushInterval: -1`), but for `compute`'s and `container`'s,
which pass through the rewriter below. Every refusal is Google's error shape,
which `gcloud` prints and does not retry:
`{"error":{"code":403,"message":"kstack: …","status":"PERMISSION_DENIED"}}`.

**The headers forwarded**, a constant of the package:

| Header | Changes what the request does | Detail |
| --- | --- | --- |
| `Accept`, `Accept-Encoding`, `Content-Type`, `User-Agent`, `X-Goog-Api-Client` | no | — |
| `Range` | no: it narrows a read's answer | — |
| `X-Goog-User-Project` | yes: the project billed and whose quota is used | *Quota project* |

Nothing else of the request's is forwarded. `X-HTTP-Method-Override` is not on the list:
Google's APIs honour it, so a `GET` carrying it would be read as a read and run as a change.
Customer-supplied encryption keys (`x-goog-encryption-*`) and `X-Goog-Api-Key` are not on it
either.

**The classifier** (`classify.go`), first row that applies:

| Request | Answer |
| --- | --- |
| `POST` to `iam` whose path ends in `/keys`; any path ending in `:signBlob`, `:signJwt`, `:generateAccessToken` or `:generateIdToken`; `POST` to `storage` under `/hmacKeys` | refused: the answer is a credential |
| `storage` with `uploadType=resumable` | refused: the answer is a session URI that uploads with no token |
| `GET` with `alt=media` or `$alt=media`, on any service; `GET` on `storage` under `/download/` | data read, class 6 |
| A `GET` of what a guest wrote, below | data read, class 6 |
| `GET`, `HEAD` | read |
| `POST` whose last segment is `getIamPolicy` or ends in `:getIamPolicy` or `:testIamPermissions`; `POST /v2/entries:list` on `logging` | read: Google sends these reads as `POST` |
| A change on the destructive list below | change, class 5 |
| anything else | change, class 4 |

**Metadata that holds secrets is blanked, not asked for.** Google's documentation says
instance metadata and startup scripts are readable by anyone who can read the instance, and
warns against secrets there. An instance carries its metadata in every read of it, so asking
would stop `gcloud compute instances list`. The read runs unasked and the values do not pass.

**The rewriter** (`redact.go`) reads every answer from `compute` and `container`, as
`kubeproxy`'s `rewriteSecrets` reads a Secret's:

- The request's `Accept-Encoding` and `Range` are dropped and `alt`, when set, must be `json`,
  else `403`. An answer that is not `application/json`, or is encoded, is a `502`.
- The body is rewritten as a stream (`json.Decoder`, `UseNumber`), a list one element at a
  time, and found by key at any depth, so a list, an aggregated list and one object are read
  alike.
- **`compute`**: under any key named `metadata` or `commonInstanceMetadata`, each `items[]`
  element's `value` becomes `safe.Redacted`. That covers an instance, an instance template, a
  machine image and the project, and their lists. The keys stay, so the model sees that a
  startup script is set.
- **`container`**: under any key named `masterAuth`, `clientKey` and `password` become
  `safe.Redacted`. A cluster created with a client certificate or a password answers them on
  every read, and they are credentials.
- **A failure fails closed**: a `502` before the first byte, a cut body after.

**A change that would write a blanked value back is refused.** `gcloud compute instances
add-metadata` reads the instance, edits the list and sends it whole. A change whose body
holds `safe.Redacted` in any string is `403`, *kstack: this change would overwrite a value the
sandbox cannot read; make it outside the sandbox*, before `Decide`.

**What a guest wrote asks**, class 6: an instance's `serialPort`, `getGuestAttributes` and
`screenshot`. Their whole answer is the guest's text, and no everyday listing reads them.

Cloud Functions' and Cloud Run's environment variables, Secret Manager's `versions:access`
and Deployment Manager's and Runtime Config's values are also such reads. Their services are
off the table, so they reach nothing.

**The destructive list**, class 5: a `DELETE` of a project, a GKE cluster or a bucket, and any
`POST` whose last segment is `setIamPolicy` or ends in `:setIamPolicy`. The verdict is class
4's; the request draws as destructive.

**The action** for a change or a data read:

- `Target` `GCP` and `Class` as classified. No other field is set: the service and the method
  are in `Summary` and `Details` ([step 1](1-shared-pieces.md#4-the-gate)).
- `Summary` is written from the parsed path, never from the body: *Google Cloud: `DELETE`
  `container` `projects/p/locations/us-central1/clusters/dev`*.
- `Sent` is the upstream host, the method, the path and query, the media type and the body.
- `Details`: *Account*, the `Token`'s `Account`; *Service*; *Quota project* when the header is
  sent.

**No body is `Unshown`.** A body that is not UTF-8 is refused by `Decide`, and one past 1 MiB
by `Body`. So an upload of a text object under 1 MiB is drawn whole and asks, and any other
upload is refused. A multipart upload names its object inside the body, so no sentence could
stand for the bytes and leave the rest enough to approve
([*Open questions*](#open-questions), 2).

### 5. The cloud platform (`tools/bash/gcp.go`)

`NewGCP(src)` answers the `CloudPlatform` whose `Target` is `GCP`. `app/runtime.go` builds the
`Source`, calls it, and puts the result in the `bash.Cloud.Platforms` it hands `bash.New`.
`cloudTargets` then gives the wire Google's switch and lets the context's `cloud` key name
`gcp`, with no other change. A test's `Config.cloudPlatforms` stands in place of the built
list, not beside it.

`Mount` builds the grant, `runproxy.NewGrant(permissions.GCP, run.Session, run.Asker,
run.Refusal, limits)`, the handler over it, a route per table row and §3's rows. The limits are `cloudLimits` (`tools/bash/cloud.go`). It adds nothing to `settings`:
`sandbox.gcp` exists, off by default.

**Google's line of the switch's confirm** is *Log entries are read without asking; an object's
contents ask first.* Settings draws no switch yet, so the line goes in
[`TODO.md`](../../TODO.md)'s *Draw each cloud platform's switch in Settings*.

### 6. The prompt

`tools/bash/prompts/sandbox.md` gains one paragraph, read only when the context's `cloud`
names `gcp`:

- `gcloud` works in the sandbox as the user's signed-in account, through a proxy, for Compute
  Engine, GKE, IAM, Cloud Logging, Cloud Storage and Resource Manager.
- A read runs. A change, and a read of an object's contents, a serial port or guest
  attributes, waits for the user each time or is refused; a refusal comes back
  `PERMISSION_DENIED` naming why.
- Instance metadata values and a cluster's `masterAuth` secrets read as `[redacted]`, and a
  change that would write one back is refused: `add-metadata` and `remove-metadata` do not
  work here.
- A background command changes nothing.
- Nothing that answers a credential works: a service account key, `gcloud auth`, a signed URL.

The sentence on tools with no login is step 1's and does not change. The paragraph is static
text. `make generate-prompts` pins it in the goldens (`app/testdata/prompt/`).

## Tasks

All start Planned. Each ends green under `make test-changed`.

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | Spike §3's four behaviours against a real `gcloud` on macOS and Linux, in the real sandbox with a throwaway handler; record the `gcloud` version and the result in this spec | — | — | Planned |
| 2 | The token service: the borrow, the cache, the sentinels, `ForgetSecret` | `services/gcptoken/` | 1 | Planned |
| 3 | The proxy: the table, the classifier, the header list, the handler, the rewriter and the write-back refusal | `run/gcpproxy/` | 1 | Planned |
| 4 | The cloud platform on a run: the mount, the body limit, the variables, `CLOUDSDK_CONFIG`, the `Source` in `app`, the prompt | `tools/bash/gcp.go`, `tools/bash/proxy.go`, `tools/bash/toolhome.go`, `tools/bash/prompts/sandbox.md`, `app/runtime.go`, the prompt goldens | 2, 3 | Planned |
| 5 | End to end through the real sandbox | `app/app_unix_test.go` (`startE2E`) | 4 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

2 and 3 can be built at the same time.

## Acceptance

Each is a test beside its file. No test starts a real `gcloud` or reaches Google: the source's
`run` is a field, and the proxy's transport is pointed at an `httptest` TLS server.

**`services/gcptoken`**

- `TestTheTokenIsReadOncePerExpiry`: two calls run the helper once; a call past the expiry less
  a minute runs it again; concurrent callers share one run.
- `TestDropForgetsOnlyTheCachedToken`.
- `TestEachFailureIsItsSentinel`: no binary, a non-zero exit, a timeout, output past the bound,
  JSON without a token. No error's text holds the helper's output.
- `TestAHeldTokenIsARegisteredSecret`: `safe.Redact` blanks it, and no longer once it is
  dropped or replaced.

**`run/gcpproxy`**

- `TestARequestCarriesTheUsersToken`: a `GET` with the grant's bearer reaches the fake upstream
  with the user's token and none of the grant's; the answer streams back.
- `TestOnlyTheGrantsBearerPasses`: no header, another token and an ended grant are each `401`
  and reach nothing.
- `TestTheUpstreamIsTheTablesHost`: one case per service.
- `TestAPathOffTheTableIsRefused`: an encoded slash, a `..`, an XML API object path on
  `storage`, a `/batch/` path and a path off its service's list are each `403`.
- `TestACredentialInTheQueryIsRefused`: `access_token`, `oauth_token`, `key`.
- `TestEveryRequestIsClassified`: one row per line of §4's table, with `compute`'s
  `getIamPolicy`, Resource Manager's `:getIamPolicy`, `entries:list`, a key create, an HMAC key
  create, a resumable upload, `alt=media`, `$alt=media`, a `/download/` path, each read of
  what a guest wrote and each destructive change.
- `TestInstanceMetadataIsBlanked`: an instance, a list, an aggregated list, a template, a
  machine image and the project each answer every metadata key and no value.
  `TestMasterAuthSecretsAreBlanked`: a cluster and a list of clusters.
- `TestAnAnswerThatCannotBeRewrittenFailsClosed`: one that is not JSON, one encoded and one
  cut short; an `alt` other than `json` is `403`.
- `TestAChangeCarryingABlankedValueIsRefused`, never asked.
- `TestOnlyTheListedHeadersAreForwarded`: `X-HTTP-Method-Override`, `X-Goog-Api-Key` and
  `x-goog-encryption-key` never reach the upstream; a `GET` carrying the override is forwarded
  as a `GET`.
- `TestAChangeAsksWithItsAccount`: a `DELETE` waits on the asker with `Target` `GCP`, its
  summary and the *Account*, *Service* and *Quota project* details, forwards once approved and
  is `403` denied.
- `TestAnObjectReadAsks` and `TestAnInstanceReadAsks`, each class 6.
- `TestABinaryUploadIsRefused`; `TestATextUploadIsShownWhole`.
- `TestARefusalIsGooglesErrorShape`.
- `TestAnUpstream401DropsTheToken`; `TestEachSourceFailureIsA503NamingIt`.

**`tools/bash`**

- `TestGCPMountsARoutePerService` (`gcp_test.go`): each route's host is
  `<service>.gcp.kstack.invalid` and on the table.
- `TestTheSandboxedEnvironmentNamesGcloud`: §3's rows, each override's host a route, and none
  of them in a run with Google off.
- `TestCloudSDKConfigIsUnderTheToolHome` (`toolhome_test.go`).
- **`TestNoGCPCredentialEntersTheSandbox`** (`bash_unix_test.go`, the issue's pin): with a
  source answering a known token and a sidecar environment holding
  `GOOGLE_APPLICATION_CREDENTIALS` and `CLOUDSDK_CONFIG`, a real sandboxed run sends a request
  through the proxy. Then `env`, and a search of every file the run can read under the run
  directory, the tool home and the workspace, find neither the token nor either variable's
  value.

**`app`** (`startE2E`, `testutil.RequireSandbox`), network off, Google turned on through
`sandboxCloudAccessSet`. As `TestASandboxedCommandReachesACloudPlatformThroughTheDoor` does with its
fake, each test sets `Config.cloudPlatforms`: Google's cloud platform over a fake helper and a fake
upstream.

- `TestASandboxedGCPReadRunsUnasked`: a command's `GET` to `container.gcp.kstack.invalid`
  through the run's port answers the fake's body with no request.
- `TestASandboxedGCPChangeAsks`: a `DELETE` waits on one request, served in
  `sandboxActions` with its account, and reaches the fake once approved.
- `TestGCPOffServesNothing`: with the switch off the route is the door's `401`, and the
  helper never runs.

**Integration gate:** the full checks in
[`../README.md`](../README.md#verification-commands). The sequence's gate, one run with all
four cloud platforms on, is the sequence [README](README.md)'s.

**By hand**, `pnpm tauri dev` on macOS and on Linux, `gcloud` signed in, network off, and the
switch turned on through `sandboxCloudAccessSet`, since Settings draws none yet:

- `gcloud container clusters list` prints the project's rows with no request.
- `gcloud compute instances list` prints the rows with no request, and
  `gcloud compute instances describe <name> --zone <zone>` shows each metadata key with its
  value `[redacted]`.
- `gcloud compute instances get-serial-port-output <name> --zone <zone>` draws a data read
  naming the account; approved, it prints the output.
- `gcloud compute instances delete <name> --zone <zone> --quiet` draws one request naming the
  method and the instance; Deny prints `PERMISSION_DENIED`.
- `env | grep -i -e cloudsdk -e google` shows §3's rows and nothing of the user's.
  `ls ~/.config/gcloud` fails, and `gcloud auth print-access-token` prints the grant's token.
- `gcloud iam service-accounts keys create` is refused.
- After `gcloud auth revoke`, a command prints the `503`'s sentence.
- With the switch off, `gcloud` finds no account, as today.

## Security

Step 1's [*Security*](1-shared-pieces.md#security) covers the door, the gate and the request.
This is what Google adds.

**Widened**, with the switch on

- A sandboxed command reads what the user's Google account reads on six services, on the
  model's word, unasked, and the answer reaches the model's cloud platform: cluster definitions, IAM
  policies, log entries, bucket and object listings.
- With network on, what a command read can be sent anywhere
  ([network is the user's switch](../../adr/2026-10-04-network-is-the-users-switch.md)).
- The sidecar runs `gcloud` outside the sandbox when a run is built, where today it runs only
  as a kubeconfig's plugin. The arguments are constants; nothing of a request reaches them.
- Approving a request changes the user's project.

**What bounds it**

- No Google credential is in the sandbox. Its environment holds the grant's bearer, which
  opens Google's proxy alone and ends with the run. The user's token lives in the sidecar's
  memory. `TestNoGCPCredentialEntersTheSandbox` pins it.
- The token goes only to a host on the table, on a path on the table, over TLS the sidecar
  verifies.
- Every change asks, and no answer writes a rule. A read of an object's contents, or of what
  a guest wrote, asks. Instance metadata values and a cluster's `masterAuth` secrets are
  blanked in every answer. A request whose answer is a credential is refused.
- No header off the list reaches Google, so no header turns a read into a change.

**Residuals**, each a **By decision** row unless the review says otherwise

- The reads are as wide as the account.
- Log entries are read unasked, as a pod's logs are, and hold whatever a workload printed.
- The model never reads an instance's metadata values, and `add-metadata` does not work in
  the sandbox.
- A secret in a `compute` field no list names (a label, a description) is read unasked.
- A `POST` that reads and is not on the list asks. A `GET` that answers something sensitive
  and is on no list passes. The lists grow as cases are met.
- A command can ask repeatedly. Each request waits behind the grant's lock and counts against
  the command's timeout.
- `gcloud` keeps its logs and configuration under the chat's tool home.

## When it lands

When this step is complete on `wip/cloud-proxies`:

- **ADR**, Google's alone: *Google Cloud is reached with the active `gcloud` account's token*.
  It records the token's source, the six services and the path lists, what is class 6 and
  what is refused. It cites [step 1's
  ADR](../../adr/2026-11-17-a-sandboxed-command-reaches-a-cloud-platform-through-a-proxy-on-its-runs-one-relay.md)
  for the decision to lend a credential through a proxy and for what
  [#25](https://github.com/kstackhq/kstack/pull/25) decided.
- **`docs/security/<date>-google-cloud-through-the-proxy.md`**, from *Security* above, naming
  the commit.
- **`docs/security-model.md`**:
  - New **Enforced** rows: no Google credential enters the sandbox
    (`TestNoGCPCredentialEntersTheSandbox`); a credential-answering request is refused and
    a data read asks (`TestEveryRequestIsClassified`); only the listed headers reach Google
    (`TestOnlyTheListedHeadersAreForwarded`); a path off the table is refused
    (`TestAPathOffTheTableIsRefused`).
  - New **By decision** rows for the residuals above, linking the ADR.
  - The **Not built** row on the four cloud platforms loses Google Cloud. The step that builds the
    last cloud platform removes the row.
- **`docs/TODO.md`**: *Let a sandboxed command reach the user's cloud accounts without holding
  a credential* loses Google Cloud, the same way. *Draw each cloud platform's switch in Settings*
  gains Google's line of the confirm. New lines: a narrower identity, the services off the
  table, uploads that are not text.
- **`docs/persisted-data.md`**: the *Chat and monitor directories* row, which holds the tool
  home, names `gcloud`.
- **`CLAUDE.md`**: `sidecar/internal/run/CLAUDE.md` (`gcpproxy`);
  `sidecar/internal/tools/CLAUDE.md` (`gcp.go`, the `gcloud` variables, the body limit);
  `sidecar/CLAUDE.md` (`services/gcptoken`). Raise a budget in
  `.github/ci-config/claude-md-budgets.yml` only with the reason in the commit body.
- This file is deleted and its row leaves [`README.md`](README.md).

## Open questions

1. **A default project?** `CLOUDSDK_CORE_PROJECT` must be in the environment before the
   command starts, so the mount borrows once per run while the switch is on, served from the
   cache after the first, as step 1's seam allows. The alternative is no default project, and
   every command naming one.
2. **Binary uploads.** None is `Unshown` here, so `gcloud storage cp` of a binary file is
   refused. A media upload names its bucket and object in the query, so it could be `Unshown`
   with *the contents of the object uploaded*; a multipart one could not. Wanted now or later?
3. **Is the class 5 list right?** A project, a cluster, a bucket and any `setIamPolicy`. It
   changes only how the request reads.
4. **ADC as a fallback?** It would work without `gcloud` on the sidecar's `PATH`, but needs
   `golang.org/x/oauth2/google` and reads a key file the sidecar does not open today.
5. **A narrower identity.** The issue asks to consider impersonating a service account. It
   needs a setting, a UI and the user's own IAM binding. This work or a later issue?
6. **`cloudresourcemanager`** is not in the issue's list. Keep it?
