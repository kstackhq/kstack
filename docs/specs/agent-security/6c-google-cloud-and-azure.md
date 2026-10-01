---
title: Google Cloud and Azure through the proxy
scope: sidecar
status: Planned
---

# Google Cloud and Azure through the proxy

**Needs:** step 5D, whose termination this step registers two injectors with; step 1D, whose
`credentials.Google` and `Azure` borrow the tokens; and step 4B, whose request the prompt is
drawn through. **Unblocks:** nothing directly; step 7B's Google and Azure connections replace
the borrows behind the same injectors.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today `gcloud` and `az` in the sandbox reach their APIs through a tunnel and fail there: the
sandbox holds no access token, and the closed home hides `~/.config/gcloud` and `~/.azure`.

After this step:

- **Two injectors** (`egress/google.go`, `egress/azure.go`) put the token step 1D's store
  borrows on each request to a closed list of Google API hosts, `management.azure.com` and
  `graph.microsoft.com`, over step 5D's termination. Nothing is copied from either tool's store.
- **Two classifiers** put every request in a class: a `GET` is class 1, any other method class
  4, a curated list of deletions and IAM changes class 5, and a Secret Manager value class 6.
  `Decide` and step 4B's prompt do the rest; a monitor session (step 6D) has `NoPrompts`, so
  its writes are refused, and `NoSecretData`, so it reads no secret's value.
- **The CLIs run in the sandbox as the user's active account**: `gcloud` sends a placeholder the
  proxy replaces; how `az` does is the one open question (Decisions).

`gcloud container clusters list` and `az aks list` work in the sandbox with no credential in it,
and `gcloud container clusters delete` and `az aks delete` ask in every mode.

## What is not in this step

- **No borrow.** `credentials.Google` and `credentials.Azure`, their TTLs and their expiry lines
  are step 1D's; this step calls them.
- **No status or re-login.** An expired token is a `403` the command reads; step 2D draws it.
- **No OAuth.** Step 7B adds app-scoped tokens; this step borrows the CLIs'.
- **No GKE or AKS kubeconfig work.** `gke-gcloud-auth-plugin` and `kubelogin` are `exec` entries
  of the user's kubeconfig, which the connection pool (`clustersvc/internal/kubeconn`) already
  runs on the host; `kubectl` in the sandbox reaches the cluster through the cluster proxy and
  never runs either.
- **No data-plane hosts.** `storage.googleapis.com`, `*.blob.core.windows.net` and
  `*.azurecr.io` are not injected; a later step may add them with their own classifiers.
- **No other Google API.** A host off §1's list is unlisted, asks as class 3, and is tunnelled
  with no token; a later step adds a host with its classifier rows.
- Nothing changes on Windows.

## Design

### 1. Google Cloud

**The credential** is step 1D's `Store.Google(ctx) (Token, error)`: `gcloud config
config-helper --min-expiry=15m` on the host, cached to its `token_expiry` less a minute. A `401` from a Google
host on an injected request calls `Store.MarkExpired(Key{Google, ""}, "", token)`, with the token it injected, through the injector's
`Unauthorized`. The configured project is `Identity.Project` off `WaitFound` (step 1D §4), which waits for
the first `Discover`, read once per start for the placeholder's project.

**The injector.** Hosts, each named, never a wildcard: `container.googleapis.com`,
`compute.googleapis.com`, `cloudresourcemanager.googleapis.com`, `iam.googleapis.com` and
`secretmanager.googleapis.com`. Every host on it has classifier rows below, and a host joins
only with its rows. `oauth2.googleapis.com` and `sts.googleapis.com` are neither injected nor
listed: a token exchange needs a credential the sandbox has not got, and a `gcloud` given an
access token asks for none. The spike (task 1) checks that, and which hosts `gcloud container
clusters list` and `gcloud projects get-iam-policy` reach; a host they need off the list is a
change to this spec, not a tunnel with a token.
Every request gets `Authorization: Bearer <token>`, by step 5D's rule: the placeholder
replaced, a missing header added, any other left alone.

**The CLI.** The run's environment (step 2A's table) gains, while `egressStatus.terminating`
holds (step 5D §3):

| Variable | Value | Why |
| --- | --- | --- |
| `CLOUDSDK_AUTH_ACCESS_TOKEN` | `kstack-placeholder` (`egress.Placeholder`) | `gcloud` sends it as the bearer token and skips its credential store: *Using an access token bypasses the need to set an active principal, and overrides any active principal set in the active gcloud CLI configuration* (`docs.cloud.google.com/sdk/docs/authorizing`) |
| `CLOUDSDK_CONFIG` | `<tool cache>/gcloud` | a writable configuration directory, since `~/.config/gcloud` is on `Never` |
| `CLOUDSDK_CORE_PROJECT` | the host's configured project | so a command needs no `--project` |
| `CLOUDSDK_CORE_CUSTOM_CA_CERTS_FILE` | the run's `trust.pem` | `gcloud` reads its own variable, not `SSL_CERT_FILE` |
| `CLOUDSDK_CORE_DISABLE_USAGE_REPORTING` | `true` | |
| `CLOUDSDK_CORE_DISABLE_PROMPTS` | `1` | stdin is the null device |

`gcloud` reads the standard proxy variables step 4C sets; task 1 checks this on a real install
and adds `CLOUDSDK_PROXY_TYPE`, `CLOUDSDK_PROXY_ADDRESS` and `CLOUDSDK_PROXY_PORT` if it does not.

**The classifier** (`egress/google.go`):

| Request | Class |
| --- | --- |
| `GET`; a `POST` whose last segment is `:getIamPolicy` or `:testIamPermissions` | 1 |
| any other method | 4 |
| `GET` of `secretmanager.googleapis.com/v1*/projects/*/secrets/*/versions/*:access`, and the `locations/*` form | 6 |
| `DELETE` of `container.googleapis.com/v1*/projects/*/locations/*/clusters/*`, of `…/clusters/*/nodePools/*`, and the `zones/*` forms | 5 |
| `DELETE` of `cloudresourcemanager.googleapis.com/v*/projects/*` | 5 |
| any request whose last segment is `:setIamPolicy` | 5 |
| `DELETE` of `compute.googleapis.com/…/instances/*` | 4 (Decisions) |

`Scope{Account, Region}` is the path's `projects/{p}` and `locations/{l}` or `zones/{z}`, else
the configured project and no region. `Verb` is the method, `Kind` the host and the path with
its parameters replaced, `Name` the last named segment. The `Summary` is *Delete GKE cluster
`dev` in `my-project` / `us-central1`*, *Set IAM policy on `my-project`*, *Show secret
`db-pass` version `latest` from `my-project`?*, *Delete node pool
`default-pool` of `dev` in `my-project` / `us-central1`*, else *`POST`
`container.googleapis.com/v1/projects/…`*, the path cut to one line.

A secret version's `:access` is class 6 as AWS's `GetSecretValue` is (step 5C): the answer is
the value, so `Allowed` passes it, `Prompted` asks, and `Denied` is a `403`, since there is
nothing to redact around it. Step 5A's grant applies by provider, `Allow` class 6 `gcp` scoped
to the project, and `NoSecretData` (step 5A §4) denies it ahead of every rule. Listing
secrets and reading their metadata stay class 1: neither holds a value.

### 2. Azure

**The credential** is step 1D's `Store.Azure(ctx, resource) (Token, error)`: `az account
get-access-token --resource <resource> --output json` on the host, one entry per resource, kept
to its `expires_on` less a minute. The resources are `https://management.azure.com/` for ARM and
`https://graph.microsoft.com/` for Graph. A `401` from either host calls
`Store.MarkExpired(Key{Azure, ""}, resource, token)`, with the resource and the token it
injected, which drops both resources' entries and refuses that token for that resource alone;
the login reads `expired` until that resource's borrow answers another token. The subscription,
its id, the tenant and the user are `Identity`'s fields off `WaitFound` (step 1D §4), read once
per start.

**The injector.** Hosts: `management.azure.com` and `graph.microsoft.com`, each with the token
for its resource. `login.microsoftonline.com` is neither injected nor listed, for the same
reason as Google's token endpoints.

**The CLI.** The run's environment gains `AZURE_CONFIG_DIR=<tool cache>/azure` (the Azure CLI
docs: the configuration file, logs and token cache live under it) and
`AZURE_CORE_COLLECT_TELEMETRY=false`. `az` reads `REQUESTS_CA_BUNDLE` and `HTTPS_PROXY`, both set
by steps 4C and 5D. How `az` itself sends a placeholder is under Decisions; whatever is chosen,
`curl` and `az rest --skip-authorization-header` against the two hosts are injected from the
day this step lands.

**The classifier** (`egress/azure.go`), over ARM's path grammar
`/subscriptions/{s}[/resourceGroups/{g}]/providers/{ns}/{type}/{name}…` and Graph's `/v1.0/…`:

| Request | Class |
| --- | --- |
| `GET`, `HEAD` | 1 |
| `PUT`, `PATCH`, `POST`, `DELETE` | 4 |
| `DELETE /subscriptions/{s}/resourceGroups/{g}` | 5 |
| `DELETE` of a resource with no `resourceGroups` segment (subscription-level) | 5 |
| any method but `GET` on `Microsoft.Authorization/roleAssignments` or `roleDefinitions` | 5 |
| `DELETE …/Microsoft.ContainerService/managedClusters/{n}` | 5 |

`Scope{Account, Region}` is the path's subscription and, for a `PUT` or `PATCH` whose JSON body
has a top-level `location`, that location (the head `Classify` asks for is 64 KiB); a request
with no subscription in its path takes the one `WaitFound` answered at start. `Kind` is the resource
type (`Microsoft.ContainerService/managedClusters`) or the Graph collection, `Name` the last
named segment. The `Summary` is *Delete AKS cluster `dev` in resource group `rg-dev`*, *Delete
resource group `rg-dev`*, *Assign a role in `rg-dev`*, else *`PUT`
`Microsoft.Compute/virtualMachines/vm-1` in `rg-dev`*. Graph writes are class 4 with *`PATCH`
`applications/…`*; a Graph class 5 list is a later step's.

### 3. Both

`tools/bash/proxy.go` registers both injectors in the run's `Handler.Injectors` under their
hosts, only while `egressStatus.terminating` holds, and the same hosts join `egress.Cloud`
(step 4C §2) for exactly as long: a `CONNECT` to one is terminated and classified, never
tunnelled, and while termination is off the hosts are unlisted and ask as class 3. Each request
goes through `Handler.Decide` and step 4B's `ActionAsker` (step 5D §1), and each hop is recorded
as an AWS request is, the
`ApprovalRequest`'s `aria-label` *Google Cloud action awaiting approval* or *Azure action
awaiting approval* by the action's provider. A monitor session's `NoPrompts` turns every
`Prompted` into `Denied` (step 3B), so a monitor never changes a project or a subscription,
and its `NoSecretData` refuses every Secret Manager value.
Both providers borrow at use time and write nothing to disk; step 1D's
`TestNoCredentialIsWrittenToDisk` grows a proxied case for each.

### 4. The prompt

`prompts/sandbox.md` gains: `gcloud` and `az` reads work in the sandbox for the account the
user's own CLI is signed into, through Kstack's proxy; a change waits for the user or comes back
`403` naming why; `gcloud auth login` and `az login` do not work in the sandbox and are the
user's to run. If `az` itself is not offered (Decisions), the paragraph says `az rest
--skip-authorization-header` and `curl` reach Azure, and `az` does not yet. On a Mac whose CA is
not trusted it says instead that neither works yet, and that Settings turns them on.

## Decisions this step asks for

1. **How `az` sends a placeholder.** Unlike `gcloud`, the Azure CLI has no documented variable
   or file that supplies an access token: its docs describe interactive, managed identity and
   service principal sign-in, each of which mints tokens into the MSAL cache under
   `AZURE_CONFIG_DIR`, and a request for `az rest` to take a token by flag has been open on the
   CLI's tracker since 2023 (Azure/azure-cli#25734). Three ways, checked against the docs:
   - **`az rest` alone.** `az rest --skip-authorization-header` sends no token of its own
     (*If Authorization header isn't set, it attaches `Authorization: Bearer <token>`*, and the
     flag turns that off), so the proxy adds one. Every other `az` command needs a login. The
     floor: it lands with this step whatever else does.
   - **A written token cache.** The sidecar writes `azureProfile.json` and `msal_token_cache.json`
     under the run's `AZURE_CONFIG_DIR` holding a placeholder access token with a far expiry. The
     format is MSAL's internal one and the CLI encrypts it where it can; nothing documents it.
     Rejected: brittle, and a token cache written by Kstack is a file a credential looks like.
   - **The proxy plays the instance metadata service.** `az login --identity` asks
     `http://169.254.169.254/metadata/identity/oauth2/token?resource=…` for a token and stores
     no secret, only that the account is a managed identity; every later command asks the same
     endpoint again. The endpoint's protocol is documented (Azure Instance Metadata Service),
     the request rides the run's `HTTP_PROXY`, and `egress.Handler` answers that one address
     itself, ahead of step 4C's IP-literal refusal and never dialling it, with the placeholder
     and a one-hour expiry per resource, one to one with `Store.Azure`'s cache. The one login
     runs once per cluster's tool cache.
   Recommended: the metadata service, behind a spike (task 1) that checks `az login --identity`
   in the real sandbox sends its metadata request through the proxy and that `az aks list` then
   carries the placeholder; `az rest` as the floor if it does not.
2. **A Compute instance delete is class 4.** One VM is not the note's blast radius, which names
   clusters, projects and IAM; the user's mode decides it. Recommended.
3. **`:getIamPolicy` and `:testIamPermissions` are reads.** Google's API sends them as `POST`;
   classifying them as writes would make `gcloud projects get-iam-policy` ask. Recommended.
4. **The data-plane hosts are not injected.** Storage, container registries and Key Vault each
   need a classifier that reads a different grammar, and a Key Vault read is a Secret read. A
   later step. Recommended.
5. **Google's hosts are a closed list, and Secret Manager is on it as class 6.** A wildcard
   reaches every Google API, Storage objects included, with every `GET` class 1. A list of five
   named hosts reaches what `gcloud`'s cluster, project and IAM commands need, and each host's
   classifier rows are written with it. Secret Manager joins so a secret's value asks, as
   AWS's does, rather than failing with no token; leaving it off is the other way, and makes
   `gcloud secrets versions access` a class 3 tunnel that fails at Google. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The spikes: `gcloud` sends the placeholder and honours the proxy variables; `az login --identity` through the proxy | `egress/google_test.go`, `egress/azure_test.go` (skipped without the CLIs) | — | Planned |
| 2 | The Google injector and classifier | `egress/google.go`, its test | — | Planned |
| 3 | The Azure injector and classifier; the metadata endpoint if chosen | `egress/azure.go`, `egress/imds.go`, their tests | 1 | Planned |
| 4 | The run's variables and registration; the `aria-label`s; the prompt | `tools/bash/env.go`, `tools/bash/proxy.go`, `tools/bash/prompts/sandbox.md`, `chat-transcript.tsx`, their tests | 2, 3 | Planned |
| 5 | End to end where CI has the CLIs | `app/e2e_unix_test.go` | 4 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4, then 5, then 6.

## Tests

**`egress`**

- `TestAGoogleRequestIsInjected`: through a run's socket with a fake `container.googleapis.com`
  behind it, a request carrying `Bearer kstack-placeholder` reaches the fake with the borrowed
  token, and `oauth2.googleapis.com` is unlisted, so a `CONNECT` there asks as class 3.
- `TestOnlyTheListedGoogleHostsAreInjected`: each of §1's five hosts is injected;
  `storage.googleapis.com`, `bigquery.googleapis.com` and `sts.googleapis.com` are unlisted,
  ask as class 3, and reach their fakes with no token.
- `TestASecretVersionAccessIsClassSix`: a `GET …/versions/latest:access`, in both path forms,
  is class 6 under every mode and never class 1; under `ReadOnly` and `Ask` it asks, a denial
  is a `403` the fake never sees, an `Allow` class 6 `gcp` rule for the project passes it, and
  `NoSecretData` refuses it under that rule; listing secrets stays class 1.
- `TestAnAzureRequestIsInjectedForItsResource`: `management.azure.com` gets ARM's token and
  `graph.microsoft.com` Graph's; `login.microsoftonline.com` is unlisted.
- `TestA401MarksTheProviderExpired`: a fake answering 401 on either provider's host sets the
  store's status `expired` and the next request borrows again.
- `TestEveryGoogleRequestIsClassified` and `TestEveryAzureRequestIsClassified`: one table each
  over the rows of §1 and §2, the `zones/*` forms, a `PUT` whose body sets `location`, and a
  path with no project or subscription.
- `TestASummaryReadsAsTheNoteSays`: the summaries named in §1 and §2.
- `TestAMonitorSessionsWriteIsRefused`: under `NoPrompts`, a `DELETE` on either host is a `403`
  saying nobody could be asked, and a `GET` passes.
- `TestTheMetadataEndpointAnswersThePlaceholder` (if Decisions 1 is taken): a request to
  `169.254.169.254` through the proxy with `Metadata: true` answers the placeholder for its
  resource and a one-hour expiry, nothing is dialled, and any other path is a `404`.

**`bash`**

- `TestTheRunNamesTheCloudVariables`: the environment holds §1's and §2's rows only while
  `terminating` holds, and the `CLOUDSDK_CONFIG` and `AZURE_CONFIG_DIR` folders are under the
  tool cache. `TestNoCredentialIsWrittenToDisk` (step 1D) grows a proxied case for each
  provider through a real run.

**`app`** (`e2e_unix_test.go`, skipped where the CLI is absent)

- `TestGcloudListsClustersInTheSandbox`: `gcloud container clusters list` against a fake
  `container.googleapis.com`, with a fake host `gcloud` for the borrow, prints the fake's rows.
- `TestAzAksListRunsInTheSandbox`: the same for `az aks list`, through whichever way Decisions 1
  settled.

## Security

**Widened.** A sandboxed command can read what the user's Google and Azure accounts read on the
injected hosts, on the model's word, unasked: step 6B accepts the same for GitHub. A secret's
value is not among those reads: it is class 6, which only step 5A's grant or `Auto` passes
unasked (`TestASecretVersionAccessIsClassSix`). The proxy reads the plaintext of the injected hosts and
no other, and Google's are five named hosts (`TestOnlyTheListedGoogleHostsAreInjected`).

**Narrowed.** Before this step neither CLI worked in the sandbox, and outside it every call ran
as the user with the user's stores. After it a change is a classified action the user's mode
and rules decide, cluster, project, resource group and IAM changes ask in every mode, and a
monitor session changes nothing.

**Residuals.** A `POST` that reads (a `:search`, a `:list`) asks under `Ask` as a class 4
would, which is a nuisance and not a hole. The Azure region is read from a body's `location`
alone, so a rule scoped by region matches only creates and updates. The metadata endpoint, if
built, is a second place a placeholder is minted; it answers only to the run's socket.

The record, `docs/security/<date>-google-cloud-and-azure.md`, is short and points at step 5D's
for the mechanism and step 1D's for the borrow.

## When it lands

- **The security record** above; the ADR step 5D writes gains a line for each classifier's
  class 5 list and for how `az` is signed in.
- **`security-model.md`**: rows for the two injectors and classifiers, Google's host list and
  its class 6 row among them, and the
  no-credential-on-disk row gains the two proxied cases.
- **`sidecar/CLAUDE.md`**: the two injectors and classifiers, the run's variables, the metadata
  endpoint. **Root `CLAUDE.md`**, *Chat*: the two `aria-label`s.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands).

By hand, `pnpm tauri dev` on Linux with `gcloud` and `az` signed in: ask for `gcloud container
clusters list`, and read the list with no request; `gcloud container clusters delete dev
--region us-central1 --quiet`, and read the request *Delete GKE cluster `dev` in …* whatever
the mode; `az aks list`, and read the list; `az group delete -n rg-test --yes`, and read *Delete
resource group `rg-test`*. `ls ~/.config/gcloud ~/.azure` in the sandbox should fail.
