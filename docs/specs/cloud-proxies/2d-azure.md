# Step 2D: Azure through the token-injecting proxy

Answers [#934](https://github.com/kstackhq/kstack/issues/934). Step 2D of
[the cloud proxies](README.md), on `wip/cloud-proxies`. It needs [step 1](1-shared-pieces.md)
alone. The other steps of wave 2 (`2a-aws.md`, `2b-github.md`, `2c-gcp.md`) are built
beside it, and none needs another.

Step 1 owns the door, the grant, the gate, the request's shape, the consent switch and the
cloud platform seam. This spec links to them and restates none. It builds what is Azure's, and the
door's TLS route, which only `az` needs.

This step changes a security boundary: the sidecar starts holding the user's Azure tokens, a
sandboxed command starts reaching Azure with them, and the run's door starts answering TLS. It
lands with an ADR and a `docs/security/` record ([*When it lands*](#when-it-lands)).

**The spike decides whether this step is built** ([*The spike*](#1-the-spike)). If it fails,
this step is retired unbuilt and the sequence lands without it.

Go paths are under `sidecar/internal/` unless rooted elsewhere.

## What is true now

Step 1's [*What is true now*](1-shared-pieces.md#what-is-true-now) holds. Azure adds:

- **A sandboxed command finds no Azure login.** `~/.azure` is on the `Never` list
  (`run/sandbox/lists.go`), and the run's environment is one built table (`sandboxedRunEnv`,
  `tools/bash/env.go`) that holds no Azure variable.
- **No Azure variable is refused by name.** `neverEnv` (`run/sandbox/env.go`) lists `GITHUB_TOKEN`,
  `GH_TOKEN` and the `AWS_` prefix, and nothing of Azure's.
- **The sidecar knows where the user's `az` keeps its login.** On macOS the login shell's
  `AZURE_CONFIG_DIR` is among the variables `loginshell` imports (`run/loginshell/loginshell.go`,
  `imported`).
- **A run's writable directory of its own is its `TMPDIR`** (`runDir.tmp`,
  `tools/bash/rundir.go`), removed when the run ends. The run directory is read-only to the
  run, and the tool home is the chat's, shared by its runs.
- **The door is plain HTTP, and a route is a `Host`.** `Route` is a `Host` and a `Handler`
  (`tools/bash/cloud.go`). The door keys its routes by `r.Host` (`door`, `newDoor`,
  `tools/bash/proxy.go`), and `runProxy.serve` listens on the run's socket and serves it on
  `runproxy.NewServer(http.Handler)`.
- **A mount is told the run's `TMPDIR`.** `CloudRun` carries `Port`, `Dir`, `ToolHome` and
  `TmpDir`, and step 1's seam lets a cloud platform write a file its client must write per run
  there.
- **Step 1 built the shared pieces and no cloud platform.** `sandbox.azure` exists and is off, and
  `permissions.Azure` is a `Target`. `app/runtime.go` hands `bash.New` a `bash.Cloud` whose
  `Platforms` is
  `cloudPlatformsFor`'s: the build's list, `builtCloudPlatforms` (`app/cloudplatforms_unix.go`), or a
  test's `Config.cloudPlatforms` in its place, and none on a machine with no sandbox. The
  wire lists a switch per cloud platform of that list, and Settings draws none yet
  ([`TODO.md`](../../TODO.md), *Draw each cloud platform's switch in Settings*).
- **Azure's name and refusals are written.** `tools/bash/cloud.go` holds *Azure*
  (`platformNames`) and fills a mount's `CloudRun.Asker` and `Refusal` (`cloudAskerFor`). The
  webview holds the name too (`CLOUD_PLATFORM_NAMES`, `src/lib/chats.tsx`).

## What this step builds

With `sandbox.azure` on:

- **`az` reads Azure Resource Manager in the sandbox, with network off**, as the account the
  user's own `az` is signed into. `az aks list` and `az vm list` work.
- **No Entra token enters the sandbox.** The run holds the grant's token. The proxy checks it,
  drops it, and adds a token the sidecar got from the user's `az` outside the sandbox.
- **A read passes, a change asks once, and an action that answers a credential is refused.**
- **Only Resource Manager is routed.** No data-plane host and no Microsoft Graph.
- **The door answers TLS on its one listener**, for Azure's route alone.

### Not in this step

- **No data plane and no Graph.** `az storage blob …`, `az keyvault secret …`, `az acr login` and
  `az ad …` fail with the proxy's message. `az role assignment list` prints ids without names.
- **No other tool.** Terraform, the Azure SDKs and `kubelogin` are not set up. A Go program on
  macOS verifies through the trust daemon, which a run with no network is refused
  ([ADR: network is the user's switch](../../adr/2026-10-04-network-is-the-users-switch.md)).
- **No AKS change.** `kubectl` in the sandbox reaches the cluster through the cluster proxy.
- **No narrower role** ([§8](#8-a-narrower-role)).
- **No `az` extensions.** The run's `AZURE_CONFIG_DIR` is its own, so the user's
  `cliextensions` are not in it.
- **No sign-in from Kstack.** A missing or expired login is the user's to fix with `az login`.
- **No cloud but the three in [the table](#the-upstream-table).** A cloud the user registered
  (Azure Stack) is not served.
- **No change to `run/sandbox` but `neverEnv`.** One relay, as step 1 leaves it.

## Design

### 1. The spike

Task 1, before anything is built. It is run by hand, on macOS and on Linux, with a real `az`
against a throwaway TLS listener that holds a self-signed certificate for the IP `127.0.0.1`.
Its script is not committed; its results go in the pull request.

Everything below about `az` was read in source on 2026-11-16 (`Azure/azure-cli` `dev`,
`azure-core` `main`, `msal` `dev`) and **not run**. The spike runs it.

**It must show:**

1. **`az` sends its bearer token to the listener over TLS**, with the certificate trusted
   through `REQUESTS_CA_BUNDLE` alone. azure-core's `_enforce_https` refuses a bearer token on a
   URL that is not `https`, and `az` sets no `enforce_https` option, so plain HTTP is not open
   to it.
2. **`az` gets that token as a managed identity**: with [§5](#5-what-a-run-holds)'s three files
   and four variables it asks `IDENTITY_ENDPOINT` once and runs no `az login`.
3. **`az aks list` and `az vm list` print a paged answer**, sent only
   [§6](#the-headers-forwarded)'s headers and given a rewritten `nextLink`.
4. **`az` starts in a sandboxed run with no network**: its own install is readable there, its
   config directory is writable, and the trust needs no daemon.

**It must state:**

- **What `REQUESTS_CA_BUNDLE` costs every other Python tool in the run**: which tools read it
  (`requests`, `pip`, botocore are expected), what each verifies against with it set, and what
  happens on a machine with none of [§3](#3-the-runs-certificate)'s three system bundles.
- **`az`'s cold start** in a fresh config directory.
- **Which statuses `az` retries, and for how long.** azure-core's retry policy is read as
  retrying `408`, `429` and `5xx` with a backoff.

**If it fails**, on TLS trust or on the managed identity, this step is retired unbuilt: this
file is deleted, its row leaves [`README.md`](README.md), and the sequence lands without Azure.
A hand-written MSAL token cache is a private format and would be a new spec.

### 2. The TLS route (`run/runproxy`, `tools/bash`)

Step 1's door is plain HTTP, keyed by `Host` ([step 1, §1](1-shared-pieces.md#1-the-door)).
`az` refuses plain HTTP, and its endpoint is dialled directly, so its `Host` is the loopback
authority, which is AWS's route. This step adds the transport to a route's key.

**A route is a transport and a `Host`.** `Route` (`tools/bash/cloud.go`) gains `TLS bool`,
false for every route there is. The door (`tools/bash/proxy.go`) keys `door.routes` by both,
and `newDoor` refuses two routes that share both. The cluster's route is plain. The table
gains one row:

| Transport | `Host` | Handler |
| --- | --- | --- |
| TLS | `127.0.0.1:<port>` | Azure |

A request on a transport and host with no route is the door's `401`, so the cluster's host over
TLS reaches nothing.

**One listener, both transports** (`run/runproxy/listen.go`), wrapped round the Unix listener
`runProxy.serve` opens. The first byte a connection sends decides: `0x16` is a TLS handshake
record, which no HTTP request starts with.

- The byte is read on the connection's first `Read`, under the server's header timeout, never
  in the accept loop. A connection that sends nothing holds nothing but itself.
- **`http.Server` does not know such a connection is TLS.** `Accept` did not return a
  `*tls.Conn`, so `r.TLS` is nil and no ALPN reaches the server. The wrapper runs the handshake
  itself and offers `http/1.1` alone.
- **The door learns the transport through `ConnContext`**, which puts the connection in the
  request's context. The door asks it once a request exists, when the first byte has been read.
- `runproxy.NewServer` takes the certificate beside the handler. With none, a connection that
  opens with `0x16` is closed.

**The certificate and its file** are [§3](#3-the-runs-certificate).

**`CloudRun` gains one field**: the path `ca.pem` will have. The run's `TMPDIR` is its
`TmpDir` already ([§5](#5-what-a-run-holds)).

### 3. The run's certificate

`cert.go` makes one certificate per run, only for a run with a TLS route.

- An ECDSA P-256 key and a self-signed certificate whose one name is the IP `127.0.0.1`.
- It carries the extensions a strict verifier wants: subject and authority key ids, critical
  basic constraints, `serverAuth`.
- It is valid from an hour before for 365 days. The key lives in the sidecar's memory and dies
  with the run, so expiry protects nothing.

**`<run dir>/ca.pem`**, 0600, created with `O_EXCL`, holds the certificate and then the
system's bundle: the first that exists of `/etc/ssl/cert.pem`,
`/etc/ssl/certs/ca-certificates.crt` and `/etc/pki/tls/certs/ca-bundle.crt`. A client pointed
at the file still verifies public hosts. `sandboxedRunFor` writes it once a mount returns a TLS
route, before the command starts.

**Azure sets `REQUESTS_CA_BUNDLE` to that file.** It is a variable other tools read, so
[step 1, §7](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash) asks for its cost under
network:

- With network off, nothing: no public host is reachable.
- With network on, every tool that reads the variable verifies public hosts against the
  system's bundle and the run's certificate, in place of the bundle it ships (certifi's).
- On a machine with none of the three bundles, those tools verify nothing but the run's
  certificate, so their public HTTPS fails.
- The spike replaces *every tool that reads the variable* with the tools it found.

### 4. The Azure service (`services/azure`)

The credential source, as [step 1, §7](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash)
places it. A part of the `Runtime` (`app/runtime.go`).

**It runs the user's `az`, outside the sandbox, with fixed arguments.** `az` is found on the
sidecar's `PATH` at each call and must resolve to an absolute path. It runs with the sidecar's
environment, under a 30 s bound, its stderr dropped. Nothing from a sandbox, a model or a
cluster reaches its arguments. The one argument that varies is a tenant id the service itself
read from `az`.

- **`Account(ctx) (Account, bool)`** is the account as last discovered. Discovery runs
  `az account list --output json` and `az cloud show --output json`, which read local files
  only.
  - It keeps, of the active cloud's enabled subscriptions, `id`, `name`, `tenantId` and
    `isDefault`, and of the cloud, its name and its endpoint and suffix tables.
  - The cloud's name must be in [the upstream table](#the-upstream-table) and every id a GUID,
    or there is no account.
  - Discovery runs when a run mounts Azure and there is none yet, and that run waits for it. It
    runs again in the background when a run mounts and the user's `azureProfile.json` has
    another mtime. It never runs at startup or while the switch is off.
- **`Token(ctx, tenant) (string, error)`** runs `az account get-access-token --resource <the
  table's resource> --tenant <tenant> --output json`, for a tenant among the account's alone.
  One call per tenant runs at a time. The token is kept in memory until five minutes before its
  `expires_on`.
- **`Drop(tenant, token)`** forgets that token, for an upstream `401`.
- **A token is registered while it is held**: `safe.AddSecret` when taken, `safe.ForgetSecret`
  when dropped or expired. It is never logged and never written.
- A failed `az` is one fixed sentence by its exit alone. Its output is never logged, stored or
  shown.

### 5. What a run holds

`tools/bash/azure.go` is Azure's `CloudPlatform`, over the `run/azureproxy` leaf.
`app/runtime.go` builds it with the service and puts it in the `bash.Cloud.Platforms` it hands
`bash.New`. `cloudTargets` then gives the wire Azure's switch and lets the context's `cloud` key
name `azure`, with no other change. A test's `Config.cloudPlatforms` stands in place of the
built list, not beside it. Who gets a grant is [step 1's](1-shared-pieces.md#4-the-gate).

`Mount` builds the grant, `runproxy.NewGrant(permissions.Azure, run.Session, run.Asker,
run.Refusal, limits)`, with no challenge. The limits are `cloudLimits` (`tools/bash/cloud.go`). It returns the TLS route, the grant, and the variables below.

`run/azureproxy` imports `runproxy`, `permissions` and `session`, and nothing of `tools` or
`services`. It reaches the service through two interfaces of its own, `Accounts` and `Tokens`.

**The config directory is `<TMPDIR>/azure`**, under `run.TmpDir`. `az` writes its command index
and logs beside its configuration, so the directory is writable and the run's own. Of the three
places [step 1's seam](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash) names for a cloud
platform's files, the run directory is read-only and the tool home is shared by a chat's runs, whose
ports differ on macOS. The run's `TMPDIR` is the one for a file a client must write per run.

The mount writes three files into it, each 0600, created with `O_EXCL`. None holds the token.

- `clouds.config`: one section, `[kstack]`, holding the account's cloud's endpoints and
  suffixes as `az cloud register` writes them (`endpoint_<name>`, `suffix_<name>`), with
  `endpoint_resource_manager = https://127.0.0.1:<port>`. A value that is not printable ASCII
  with no space is left out.
- `config`: `[cloud]` `name = kstack`, and `[core]` `collect_telemetry = false`.
- `azureProfile.json`: one entry per subscription, the user's replaced by a managed identity:

  ```json
  {"subscriptions": [{
    "id": "<subscription id>", "name": "<name>", "tenantId": "<tenant id>",
    "state": "Enabled", "isDefault": true, "environmentName": "kstack",
    "user": {"name": "systemAssignedIdentity", "type": "servicePrincipal", "assignedIdentityInfo": "MSI"}
  }]}
  ```

  A subscription's name is the user's text, so this file is written by `encoding/json`, and
  `clouds.config` holds no name.

**The variables:**

| Variable | Value |
| --- | --- |
| `AZURE_CONFIG_DIR` | `<TMPDIR>/azure` |
| `IDENTITY_ENDPOINT` | `https://127.0.0.1:<port>/kstack/identity` |
| `IDENTITY_HEADER` | the grant's token |
| `REQUESTS_CA_BUNDLE` | `<run dir>/ca.pem` |

Azure sets no proxy variable. Where another cloud platform sets one, step 1's `NO_PROXY=127.0.0.1`
keeps `az` dialling the door directly.

**How `az` comes to send the token.** An account marked `assignedIdentityInfo: MSI` makes `az`
build MSAL's `ManagedIdentityClient`. With the two `IDENTITY_` variables set, it sends
`GET <endpoint>?api-version=2019-08-01&resource=…` with `X-IDENTITY-HEADER`. The proxy answers
with the grant's token, and `az` sends that as its bearer token from then on. `az login` is
never run in the sandbox.

**`neverEnv` gains four exact names** (`run/sandbox/env.go`): `AZURE_CLIENT_SECRET`,
`AZURE_CLIENT_CERTIFICATE_PATH`, `AZURE_PASSWORD` and `AZURE_FEDERATED_TOKEN_FILE`. No run is
handed a real Azure credential by name. This narrows what a run may hold, and is a security
change of its own, in its own commit.

**With no account** (no `az`, or no login) the mount writes no file and sets no variable. `az`
in the run says it is not logged in. The route stays, and answers every request the fixed
sentence of [§6](#6-the-proxy-runazureproxy).

**The prompt.** This step adds Azure's paragraph to `tools/bash/prompts/sandbox.md`, in
step 1's words, read when the context's `cloud` names `azure`. It adds what is Azure's: only
Resource Manager is reached, so storage, Key Vault, `az ad` and `az login` do not work there.
The sentence on tools with no login is step 1's and does not change.

### 6. The proxy (`run/azureproxy`)

A handler over a `runproxy.Grant` with `Target` `Azure`. The token's mechanics, the grant's
life and its limits are [step 1's](1-shared-pieces.md#3-the-grant-core-runrunproxy), and so is
what every cloud platform's proxy does around the core.

**The token's slot is `Authorization: Bearer`**, which `az` sends on every Resource Manager
request. The identity endpoint is the one exception: MSAL sends the token there as
`X-IDENTITY-HEADER`, and that request is how `az` learns its bearer. Each path accepts its own
slot and no other.

**The handler, in order:**

1. The token, by `Holds`, else the grant's `Challenge`. Then `Enter`.
2. `GET /kstack/identity` is answered here. `resource` must be the cloud's resource or its
   Resource Manager endpoint, else `400` saying only Resource Manager is reachable. The answer
   is `{"access_token": <the grant's token>, "expires_on": <now + 24 h>, "resource": …,
   "token_type": "Bearer"}`. Nothing is dialled.
3. [The path policy](#the-path-policy), else `403`.
4. [The classifier](#the-classifier). A refusal is `403`. A read takes `Slot`. Anything else
   reads its body through `Body` and goes to `Decide`, which reads no mode and no kube-context.
5. The tenant is that of the subscription the path names, else the default subscription's. A
   subscription that is not the account's is `403`.
6. `Tokens.Token` for the tenant. A failure answers one fixed sentence, *kstack: Azure sign-in
   is needed. Ask the user to run `az login`.*, never `az`'s text.
7. Forward to the table's host with `Outbound`'s headers and the user's token.

**A refusal is Resource Manager's error shape**, `{"error": {"code": "KstackRefused",
"message": "kstack: …"}}`, which `az` prints. Every answer the proxy writes for itself is a
`403`, the sign-in sentence and an upstream failure included, since `az` is read as retrying
`5xx` with a backoff. The grant's `429` and `413` are step 1's; the spike says what a `429`
costs `az`.

#### The upstream table

The upstream host comes from this table, by the cloud's name, never from `az`'s answer or the
request.

| Cloud | Host | Token resource |
| --- | --- | --- |
| `AzureCloud` | `management.azure.com` | `https://management.core.windows.net/` |
| `AzureUSGovernment` | `management.usgovcloudapi.net` | `https://management.core.usgovcloudapi.net/` |
| `AzureChinaCloud` | `management.chinacloudapi.cn` | `https://management.core.chinacloudapi.cn/` |

#### The headers forwarded

`Outbound`'s list, a constant of the package:

| Header | Changes what the request does? | Drawn as |
| --- | --- | --- |
| `Accept`, `Accept-Language`, `Content-Type`, `User-Agent` | no | — |
| `x-ms-client-request-id` | no, it is a correlation id | — |
| `If-Match` | yes, the change applies only to that version | the detail *If-Match* |
| `If-None-Match` | yes, `*` makes a `PUT` create-only | the detail *If-None-Match* |

Nothing else of the request's is forwarded.

- **No other `x-ms-` header**, `x-ms-authorization-auxiliary` among them: it carries tokens for
  other tenants.
- **`X-HTTP-Method-Override`, `X-HTTP-Method` and `X-Method-Override` are never forwarded**, so
  a `GET` is a `GET` at Azure.
- **`Accept-Encoding` is not forwarded**, so the answer the proxy rewrites is plain.

#### The path policy

Resource Manager folds case in a path, so every comparison of a cloud platform namespace, a type or
an action folds case too.

- A path that is not canonical is refused, as is a query `url.ParseQuery` refuses, a read's
  included, since the classifier reads `$expand`.
- A path is read by Resource Manager's grammar:
  `/subscriptions/{id}[/resourceGroups/{g}][/platforms/{namespace}/{type}/{name}…][/{action}]`,
  `/platforms/…`, `/tenants`, `/subscriptions`. Any other path is refused, `/batch` among them:
  it carries other requests in its body.
- A method that is not `GET`, `HEAD`, `POST`, `PUT`, `PATCH` or `DELETE` is refused.

#### The classifier

`classify.go` puts a request in exactly one row of
[step 1's classes](1-shared-pieces.md#4-the-gate). The first row that matches decides.

| # | Request | Is |
| --- | --- | --- |
| 1 | `Microsoft.ContainerService/managedClusters/*/runCommand`, `…/commandResults/*`, any method | refused: it runs commands in the cluster, around the cluster proxy |
| 2 | `Microsoft.ContainerService/managedClusters/*/accessProfiles/*`, any method | refused: the answer is a login to a cluster |
| 3 | A read on [the secret-metadata list](#metadata-that-holds-secrets) | class 6 |
| 4 | `POST` whose last segment contains `key`, `token`, `sas`, `credential`, `password` or `connectionstring` | refused: the answer is a credential |
| 5 | `POST` whose last segment starts with `list` or `get` | refused: the name does not say what it answers |
| 6 | `GET`, `HEAD` | a read |
| 7 | A change on [the class 5 list](#the-class-5-list) | class 5 |
| 8 | Any other `POST`, `PUT`, `PATCH`, `DELETE` | class 4 |

Row 4 refuses `listKeys`, `regenerateKey`, `listAccountSas`, `listClusterAdminCredential` and
`generateCredentials`. Row 5 refuses the rest of the `list` and `get` actions, the harmless
ones included.

**Azure names no stored data on Resource Manager.** Blobs, queue messages, log rows and Key
Vault secret values live on data-plane hosts, which no route reaches. So class 6 is the
metadata list alone.

**Azure's line of the switch's confirm** says so: *Stored data is not reached; an app's
settings and a deployment's parameters ask first.* Settings draws no switch yet, so the line
goes in [`TODO.md`](../../TODO.md)'s *Draw each cloud platform's switch in Settings*.

##### Metadata that holds secrets

The reads whose answer Azure documents as able to hold a secret value. A constant table.

| Read | What it answers |
| --- | --- |
| `POST Microsoft.Web/sites/*[/slots/*]/config/appsettings/list` | an App Service or Function app's settings |
| `POST Microsoft.Web/sites/*[/slots/*]/config/connectionstrings/list` | its connection strings |
| `GET` under `Microsoft.Compute/` whose `$expand` names `userData` | a VM's or a scale set's user data |
| `GET` of `Microsoft.Resources/deployments` or `…/deployments/*`, at any scope; `POST …/deployments/*/exportTemplate` | a deployment's parameters, outputs and template |
| `GET` of `Microsoft.Automation/automationAccounts/*/variables` or `…/variables/*` | unencrypted Automation variables |
| `GET` of `Microsoft.App/containerApps` or `…/containerApps/*`; of `Microsoft.ContainerInstance/containerGroups` or `…/containerGroups/*` | a container's environment |

Three notes:

- A VM's custom data is never in an answer, so it needs no row.
- Resource Manager's own `Microsoft.KeyVault/vaults/*/secrets/*` answers a secret's properties
  and never its value.
- `…/deployments/*/operationStatuses/*` is not on the list, so `az`'s polling of a deployment
  it started asks nothing. Its last read of the deployment asks once.

##### The class 5 list

Recommended, since Azure can name what loses the most:

- `DELETE` of a resource group, and `DELETE` with no `resourceGroups` segment.
- Any change under `Microsoft.Authorization/` (role assignments, locks, policy).
- `DELETE` of `Microsoft.ContainerService/managedClusters/*`.
- Any `runCommand` or `runCommands` segment not already refused (a VM's).

##### The action

`Target` `Azure`, its `Class` and its `Summary`, and no other field: the resource group and the
resource are its details.

- **`Summary`** is one line and names the cloud platform: *Azure: delete resource group rg-dev in
  Pay-As-You-Go*, *Azure: read app settings of Microsoft.Web/sites/shop in rg-dev*, else
  *Azure: PUT Microsoft.Compute/virtualMachines/vm-1 in rg-dev*.
- **`Details`**: *Subscription* (its name and id), *Resource group* when the path names one,
  *Resource* (the cloud platform namespace, the type and the last named segment) when it names one,
  and *If-Match* and *If-None-Match* when sent. The labels are constants of the package.
- **No body is `Unshown`.** Resource Manager's bodies are JSON. One past step 1's 1 MiB is
  `413`, so a deployment template larger than that is not sent.

The request rides `ToolCall.sandboxActions` as a `CloudRequest` and takes step 1's labels
([step 1, §5](1-shared-pieces.md#5-the-request)).

### 7. Answer rewriting

`az` follows the links an answer gives it, so the proxy rewrites each that names the real host.

- A `Location`, `Azure-AsyncOperation` or `Operation-Location` header, and a JSON body's
  top-level `nextLink`, that start with `https://<the table's host>` start with
  `https://127.0.0.1:<port>` instead.
- The body is rewritten as a stream, as `kubeproxy`'s `rewriteSecrets` rewrites one
  (`run/kubeproxy/redact.go`). A `nextLink` inside an item is left alone.
- `WWW-Authenticate` is dropped.
- An upstream `401` calls `Drop` with the token that was sent.

### 8. A narrower role

None is built. Entra has no way to narrow a user's token per request, so a narrower role means
a second identity and a secret for Kstack to keep. The gate at the proxy is the narrowing. A
user who wants a hard bound signs a Reader identity into its own `AZURE_CONFIG_DIR` and exports
that variable from the login shell, which `loginshell` imports on macOS. The ADR says so.

## Tasks

All start Planned. Each ends green under `make test-changed`.

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The spike, per [§1](#1-the-spike) | none kept; results in the pull request | — | Planned |
| 2 | The TLS route: the sniffing listener, the certificate, the server's certificate | `run/runproxy/listen.go`, `cert.go`, `server.go` | 1 | Planned |
| 3 | `Route.TLS`, the door's transport key, `ca.pem`, `CloudRun`'s field for its path | `tools/bash/cloud.go`, `proxy.go`, `bash.go`, `rundir.go` | 2 | Planned |
| 4 | The Azure service | `services/azure`, `app/runtime.go` | 1 | Planned |
| 5 | The proxy: the identity endpoint, the path policy, the table, the headers, forwarding, rewriting | `run/azureproxy` | 1 | Planned |
| 6 | The classifier and the call of `Decide` | `run/azureproxy/classify.go` | 5 | Planned |
| 7 | `neverEnv`'s four names | `run/sandbox/env.go` | — | Planned |
| 8 | The cloud platform: the mount, the body limit, the three files, the variables, the prompt, the list `app` builds | `tools/bash/azure.go`, `proxy.go`, `prompts/sandbox.md`, `app/runtime.go`, the prompt goldens (`app/testdata/prompt/`) | 3, 4, 6, 7 | Planned |
| 9 | A real `az` through the real sandbox, and `az` on one CI leg | `app/app_unix_test.go`, `.github/` | 8 | Planned |
| 10 | Docs, per [*When it lands*](#when-it-lands) | see there | 1–9 | Planned |

After the spike, 2, 4, 5 and 7 can start together.

## Acceptance

Each is a test beside its file. Fakes are parameters: the service takes its `az` runner, the
proxy its `Accounts`, its `Tokens` and its upstream's roots.

**`run/runproxy`**

- `TestOneListenerServesBothTransports`: a plain request and a TLS one each reach a handler
  that reads its transport.
- `TestASilentConnectionEndsAtTheHeaderTimeout`, while a second connection is served.
- `TestATLSConnectionWithNoCertificateIsClosed`.
- `TestTheCertificateNamesTheLoopbackAlone`; `TestTheBundleKeepsTheSystemsRoots`, and holds
  the certificate alone where no system bundle exists.

**`run/sandbox`**: `TestARunWithAnUnpassableVariableIsRefused` gains the four names, on both
platforms.

**`services/azure`**

- `TestATokenIsAskedOncePerTenantAndKeptToItsExpiry`, `TestADroppedTokenIsAskedAgain`.
- `TestAHeldTokenIsBlankedAndADroppedOneIsNot`: `safe` blanks a token while it is held, and
  not once it is dropped or expired.
- `TestOnlyTheAccountsTenantsReachAz`: a tenant that is not the account's never runs `az`.
- `TestAnAccountWithABadIdOrAnUnknownCloudIsNone`.
- `TestDiscoveryWaitsForAMount`: nothing runs `az` until a run mounts.
- `TestAFailedAzSaysNothingOfItsOutput`: the error and the log hold none of it.

**`run/azureproxy`**

- `TestTheIdentityEndpointAnswersTheGrantsToken`: with the header, the token and no dial;
  without it, `401`; for another resource, `400`.
- `TestEachPathAcceptsItsOwnSlotAlone`: a bearer on the identity endpoint and an identity
  header on a Resource Manager path are each `401`.
- `TestARequestCarriesTheUsersTokenUpstream`: the upstream sees the service's token and never
  the grant's.
- `TestOnlyTheListedHeadersAreForwarded`: `x-ms-authorization-auxiliary`, each method-override
  header and `Accept-Encoding` never reach the upstream.
- `TestEveryRequestIsClassified`: one table over the path policy and the classifier's rows, in
  mixed case.
- `TestACredentialActionIsRefusedUnasked`: `listKeys`, `regenerateKey`, a `list*Credential`,
  `accessProfiles` and a `list` action no row names; none reaches the asker or Azure.
- `TestSecretMetadataAsksAsADataRead`: each row of the list is class 6, and
  `operationStatuses` is a read.
- `TestAConditionalHeaderIsDrawn`: `If-Match` and `If-None-Match` are details.
- `TestAChangeAsksAndOnlyAnApprovalForwards`.
- `TestAnotherSubscriptionIsRefused`, `TestAFailedTokenIsOneFixedSentence`,
  `TestARefusalIsNeverRetried`: every status the proxy writes for itself is `403`.
- `TestTheAnswersLinksNameTheDoor`: the three headers and a paged body's `nextLink`; a
  `nextLink` inside an item is left alone.
- `TestA401DropsTheToken`.

**`tools/bash`**

- `TestTheLoopbackAuthorityRoutesByTransport`: with a fake plain cloud platform on the loopback
  authority, a plain request reaches it and a TLS one reaches Azure; the cluster's host over
  TLS is the door's `401`.
- `TestARunWithNoTLSRouteHasNoCertificate`: no `ca.pem`, and a TLS connection is closed.
- `TestNoAzureCredentialIsInTheRun`: through the real sandbox, with a service whose token is a
  known string, a command that prints its environment and reads every file under the run
  directory, `TMPDIR`, the tool home and the workspace finds the string nowhere, while the
  upstream received it.
- `TestACurlTrustsTheRunsCertificate`: through the real sandbox, `curl --cacert` on `ca.pem`
  reaches the route.
- `TestTheSandboxedEnvironmentIsFixed` gains the four variables.
- `TestTheAzureFilesHoldNoName`: a subscription named with quotes, newlines and `]` leaves
  `clouds.config` and `config` unchanged and `azureProfile.json` valid JSON.
- `TestARunWithNoAccountHasTheRouteAlone`: no file, no variable, and the route answers the
  sign-in sentence.
- `TestTheSandboxPromptSaysWhenToReadAzure` (`bash_test.go`): the paragraph is static, and tells
  the model to read it only where the context's `cloud` names `azure`. The prompt goldens.

**`app`** (`startE2E`, `testutil.RequireSandbox`, skipped where `az` is absent), network off,
Azure turned on through `sandboxCloudAccessSet`. As
`TestASandboxedCommandReachesACloudPlatformThroughTheDoor` does with its fake, each test sets
`Config.cloudPlatforms`: Azure's cloud platform over a fake `az` runner for the service and a fake
Resource Manager for the upstream.

- `TestAzListsInTheSandbox`: `az aks list` and `az vm list` print the fake's rows across a
  paged answer.
- `TestAnAzChangeAsks`: `az group delete -n rg --yes` waits on one request in
  `sandboxActions` whose summary is *Azure: delete resource group rg in …*. Denied, it exits
  non-zero and the fake saw no `DELETE`.
- `TestAzureBesideEveryOtherPlatform`: one run with a cluster, Azure, and every cloud platform then
  built on `wip/cloud-proxies` turned on. `az` reads, `kubectl` reads, and no variable is set
  two ways.

**By hand**, `pnpm tauri dev` on macOS and on Linux, `az` signed in, network off, and the switch
turned on through `sandboxCloudAccessSet`, since Settings draws none yet:

- `az aks list` and `az vm list` print with no request.
- `az group create -n kstack-test -l westus` waits on *Azure change awaiting approval*; Deny
  fails the command.
- `az storage account keys list …` is refused with no request.
- `env | grep -i -e azure -e identity; cat "$AZURE_CONFIG_DIR"/*; ls ~/.azure` shows no JWT,
  and `~/.azure` is refused.
- With the switch off, `az account show` says to log in.

**Integration gate:** the full checks in
[`../README.md`](../README.md#verification-commands). This step changes no wire.

## Security

Step 1's [*Security*](1-shared-pieces.md#security) covers the door, the gate and the request.
This is what Azure adds.

**Widened.**

- With the switch on, a sandboxed command reads everything the user's login can `GET` from
  Resource Manager, in every subscription the login holds, unasked. Resource Manager's contract
  keeps secrets out of a `GET`; the secret-metadata list covers the documented exceptions.
- The sidecar runs the user's `az` from `PATH`, outside the sandbox, with fixed arguments, and
  holds Resource Manager tokens in memory for up to their lifetime.
- The run's door answers TLS. The handshake runs before any token is checked, on a port that on
  macOS any local process can dial.
- A run with Azure trusts a certificate Kstack made, through a variable every Python tool
  reads.

**Held.**

- No Entra token is in the run's environment, its files or the door's answers
  (`TestNoAzureCredentialIsInTheRun`).
- What the run holds is the grant's token. It dies with the run and opens Azure's route alone.
- The certificate's key never leaves the sidecar's memory, and the certificate names
  `127.0.0.1` alone. Trusting it lets nothing else pose as a public host.
- The upstream is one of three fixed hosts. Nothing `az` or the command says picks it.
- An action that answers a credential is refused, never asked.
- `az`'s arguments are fixed but for a tenant id that must be a GUID the service read itself.

**Residuals**, each a line in the record and a **By decision** row:

- The certificate is trusted by whatever reads `REQUESTS_CA_BUNDLE`, and with network on those
  tools verify against the system's bundle in place of their own.
- Every Azure SDK in the run believes it has a managed identity. With network on, one that
  calls the real Resource Manager sends it the grant's token, which opens nothing from there.
- A `POST` that only reads (`Microsoft.ResourceGraph`, cost queries) asks as a change.
- A `POST` that answers a secret under a name rows 4 and 5 do not catch is class 4: it asks as
  a change, is not refused, and is not refused under `NoSecretData`.
- A change that runs code on a machine (a VM extension, a deployment script) is class 4 and
  rests on the user's eye.
- A secret in metadata the list does not name is read unasked, as step 1 records.
- `az` is found on `PATH`, so whoever controls `PATH` controls what the sidecar runs, as with
  a kubeconfig's `exec` plugin.
- A long `az … create` asks once per request.

## When it lands

When this step is complete on `wip/cloud-proxies`:

- **ADR**, Azure's alone:
  `docs/adr/<date>-a-sandboxed-command-reaches-azure-through-a-token-injecting-proxy.md`. It
  records the grant's token over a managed-identity endpoint, the TLS route on the door's one
  listener and its certificate, Resource Manager alone, the three clouds, the config directory
  in `TMPDIR`, and no narrower role. Rejected: plain HTTP, a second listener, a written MSAL
  cache, a real token in the run, `HTTPS_PROXY` with interception. It cites
  [step 1's
  ADR](../../adr/2026-11-17-a-sandboxed-command-reaches-a-cloud-platform-through-a-proxy-on-its-runs-one-relay.md)
  for the decision to lend a credential through a proxy.
- **`docs/security/<date>-azure-through-the-proxy.md`**, from *Security* above, the TLS
  route's residual included, naming the commit.
- **`docs/security-model.md`**:
  - Azure leaves the **Not built** row on the four cloud platforms. No new one is added.
  - A new **Enforced** row: a sandboxed command reaches Azure Resource Manager alone, holds no
    Azure credential, and is refused every action that answers one
    (`TestNoAzureCredentialIsInTheRun`, `TestACredentialActionIsRefusedUnasked`,
    `TestAChangeAsksAndOnlyAnApprovalForwards`).
  - A new **Enforced** row: the door answers TLS for Azure's route alone, with a key that
    stays in the sidecar (`TestTheLoopbackAuthorityRoutesByTransport`,
    `TestTheCertificateNamesTheLoopbackAlone`).
  - New **By decision** rows for the residuals above, linking the ADR. One says the sidecar
    runs the user's `az` and holds its tokens in memory.
  - The environment row names the four variables and `neverEnv`'s four names.
- **`docs/TODO.md`**: Azure leaves *Let a sandboxed command reach the user's cloud accounts
  without holding a credential*. No new one is added for it. *Draw each cloud platform's switch in
  Settings* gains Azure's line of the confirm. New lines: Azure's data plane, Microsoft Graph,
  a registered cloud.
- **`CLAUDE.md`**: `sidecar/internal/run/CLAUDE.md` (the `azureproxy` leaf; `runproxy`'s TLS
  route and certificate); `sidecar/internal/tools/CLAUDE.md` (Azure's mount, `ca.pem`, the
  transport in a route's key, the body limit);
  `sidecar/CLAUDE.md` (`services/azure`, and a security invariant for the `az` it runs). Raise
  a budget in `.github/ci-config/claude-md-budgets.yml` only with the reason in the commit
  body.
- This file is deleted and its row leaves [`README.md`](README.md).

## Open questions

1. **Is the grant's token over `IDENTITY_ENDPOINT` inside the issue's "not in scope"?** The
   issue excludes serving tokens that way, because a command with network could send one
   anywhere. Here the endpoint serves the run's own token, never an Entra token. Recommended:
   accept it. The other way is a hand-written MSAL cache, a private format.
2. **A status line under the switch?** The draft drew the account, or what is missing, from a
   gauge. It is not part of the shared switch; see
   [step 1's open question 2](1-shared-pieces.md#open-questions). Without it, a user learns
   that `az` is not signed in from the command's answer.
3. **Is the class 5 list right, or should there be none?** The verdict is the same either way.
   Recommended: the list in [§6](#the-class-5-list).
4. **Connection strings: class 6 or refused?** An app's connection strings are the user's own
   text, as its settings are, but most hold a password. Recommended: class 6, as listed.
5. **How far does the secret-metadata list go?** The container rows make `az containerapp
   list` ask. Logic Apps parameters and Data Factory linked services are candidates not
   listed. Recommended: the listed rows, grown by a follow-up.
6. **Refuse every unnamed `list` and `get` action?** Step 1's rule says so, and it refuses
   harmless ones (`listUsages`-style actions). The other way is a table of actions known to be
   reads. Recommended: refuse, and add the table when a refusal is missed.
7. **Does `REQUESTS_CA_BUNDLE` belong in a run that has the internet?** The spike states its
   cost. If the cost is too high, the other way is to leave Azure out of such a run.
8. **Three clouds, or the public one alone?** Recommended: the three in the table.
9. **Is a config directory per run acceptable?** `az` rebuilds its command index in a fresh
   one, which the spike times. If it is slow, the index can be kept in the chat's tool home,
   at the cost of a file one run leaves for the next.
10. **CI needs an `az`** on one Linux leg for the `app` tests. Without it only the fakes run,
   and `az`'s private file formats ([§5](#5-what-a-run-holds)) are unpinned.
