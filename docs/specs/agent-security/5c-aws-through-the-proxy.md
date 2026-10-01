---
title: AWS through the proxy
scope: sidecar, webview
status: Planned
---

# AWS through the proxy

**Needs:** step 3B, whose `Decide` every AWS write goes through; step 4C, whose server on the
run's socket routes the request and whose `HTTP_PROXY` variables carry the run's token; step
1D, whose `credentials.AWS` (a `credentials.AWSCredential` carrying its own account) and `Region` borrow what the proxy re-signs with; and
step 4B, whose request the prompt is drawn through. It reads step 2D's per-cluster profile
setting, which landed in wave 2. **Unblocks:** nothing directly; step 7B's AWS connection
replaces the borrow behind the same proxy.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today `aws` does not work in the sandbox: `~/.aws` is a Never path, the run's environment holds
no `AWS_*` variable, and the run has no network. A chat that needs `aws eks describe-cluster`
needs step 1B's switch.

After this step, on macOS and Linux, `aws` works inside the sandbox with no credential in it, as
[the note](../../notes/sandbox-credentials-and-permissions.md)'s *Tool redirection* and
*Credential sources* say:

- **The CLI is pointed at the run's proxy** by `AWS_ENDPOINT_URL`, and signs each request with a
  **placeholder** key pair the run alone holds. The placeholder signs for nothing at AWS.
- **`awsproxy`** checks the placeholder signature, reads the service and region off the
  credential scope, finds the endpoint in its own table, classifies the action, asks `Decide`,
  borrows the user's real credentials through step 1D's store, re-signs with a fresh timestamp
  and forwards to the real endpoint.
- **The profile is the cluster's**: the one its kubeconfig's `exec` entry names, unless step 2D's
  Settings override it.
- **The AWS classifier** assigns class 1, 4, 5 and 6 by action name, refuses the actions that mint
  credentials outright, and step 3B's shipped `iam:*` rule now matches. Class 1 is a table of
  named reads; an action the table does not name is class 4. A write asks as a cluster write
  does, by the user's mode and rules.

Commands outside the sandbox are unchanged: `aws` runs there as the user, with the user's files.

## What is not in this step

- **No borrow.** `credentials.AWS` and `Region`, the credential's account, their cache and their expiry lines
  are step 1D's; this step calls them.
- **No TLS interception.** `gh`, `gcloud` and `az` are steps 6B and 6C. AWS needs none: the CLI
  has its own endpoint override.
- **No credential status, and no profile control.** Step 2D shows each profile's state, the
  re-login button and the per-cluster profile select; this step reads the setting and answers an
  expired credential with an error the CLI prints.
- **No change on Windows**, which has no sandbox: `aws` runs outside it, as the user.

## Design

### 1. Redirection

`sandboxedRunEnv` (`tools/bash/env.go`, step 2A's table) gains, for every sandboxed run:

| Variable | Value |
| --- | --- |
| `AWS_ENDPOINT_URL` | `http://aws.kstack.invalid` |
| `AWS_ENDPOINT_URL_<ID>` | `http://<prefix>.aws.kstack.invalid`, one per row of §2's endpoint table whose endpoint prefix differs from its signing name: `AWS_ENDPOINT_URL_BEDROCK_RUNTIME` is `http://bedrock-runtime.aws.kstack.invalid` |
| `AWS_ACCESS_KEY_ID` | the placeholder id: `KSTACK` then 14 characters of `[A-Z2-7]`, random per run |
| `AWS_SECRET_ACCESS_KEY` | the placeholder secret: 40 random characters, per run, held by the run's proxy |
| `AWS_DEFAULT_REGION`, `AWS_REGION` | the profile's region (§4), left out when none is known |
| `AWS_EC2_METADATA_DISABLED` | `true`, so no request waits on the instance metadata service |
| `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE` | `/dev/null`, so the CLI never looks for a file |

Step 2A's never-list keeps every other `AWS_*` out; its test grows these rows. The CLI honours
`HTTP_PROXY` for an `http` endpoint, so each request reaches step 4C's server on the run's
socket in absolute form, `GET http://aws.kstack.invalid/… HTTP/1.1`, with `Host:
aws.kstack.invalid` and the run's token as proxy credentials. The server checks the token first,
as the cluster proxy does, and routes that host and every `*.aws.kstack.invalid` to the run's
`awsproxy.Handler`. **The token cannot ride in `AWS_ENDPOINT_URL`**: SigV4 signs the `Host`
header, and botocore sends no userinfo as credentials in any case. `sandboxedRunFor` mints the pair
(`awsproxy.NewPlaceholder()`), puts it in the environment and hands it to the handler.

### 2. `awsproxy`

A new leaf, `awsproxy`, importing `permissions`, `session`, `credentials` and `safe`:

```go
// Handler serves one run's AWS requests: checks the placeholder signature,
// classifies, decides, re-signs with borrowed credentials and forwards.
func New(p Placeholder, s session.Session, profile string, creds Source, ask Asker, forward Forward) *Handler

// Source borrows the profile's credentials; credentials.Store (step 1D) is the one.
type Source interface {
	AWS(ctx context.Context, profile string) (credentials.AWSCredential, error)
	MarkExpired(key credentials.Key, arg, sent string)
}

// Forward sends the re-signed request out, through step 4C's host check.
type Forward func(ctx context.Context, r *http.Request) (*http.Response, error)
```

The handler, in order:

1. **Verify.** `sigv4.go` parses `Authorization` (`AWS4-HMAC-SHA256 Credential=<id>/<date>/
   <region>/<service>/aws4_request, SignedHeaders=…, Signature=…`), refuses an id that is not
   the placeholder's, rebuilds the canonical request from the signed headers as received, and
   compares the signature in constant time. The payload hash is the request's
   `x-amz-content-sha256` when present — a hex hash, `UNSIGNED-PAYLOAD`, or a streaming marker —
   else the SHA-256 of the body, read up to `maxBody` (8 MiB; past it, 413). A verified
   signature came from the run's environment and was not altered; one that does not verify is
   403 (§5), logged with the service and operation, never the body.
2. **The endpoint.** A signing name does not name an endpoint: `bedrock` signs both
   `bedrock` and `bedrock-runtime`. So `endpoint.go` holds a table of the services the proxy
   reaches, one row each: its endpoint prefix, its signing name and its host. The row is found
   by the request's `Host`, which SigV4 signs: `<prefix>.aws.kstack.invalid` is that prefix's
   row, and the bare `aws.kstack.invalid` is the row whose prefix is the scope's service. The
   row's signing name must be the scope's service. The host is the standard shape
   `<prefix>.<region>.amazonaws.com` unless the row says otherwise — `iam`, `route53`,
   `cloudfront` and `organizations` global at `<prefix>.amazonaws.com`; `sts` regional, and
   `sts.amazonaws.com` for `us-east-1`; `s3` at `s3.<region>.amazonaws.com`, path style, as the
   CLI signs for a custom endpoint; `ses` at `email.<region>.amazonaws.com`. No row, or a
   signing name that does not match, is a 403 (§5): a service the table does not name is never
   sent to a guessed host. The table grows a row as a service is met. Only `amazonaws.com` is
   produced: the China and GovCloud partitions and any custom endpoint are out of this step.
3. **Borrow** `creds.AWS(ctx, profile)` (§4), then **classify** (§3), scoped by the credential's
   account, and **decide**: `permissions.Decide(policy, s.Rules(ctx), act)`, the
   policy the session's as the cluster proxy builds it. `Allowed` forwards and records;
   `Prompted` asks through `Asker` as step 4B's prompt; `Denied` is a 403 naming the mode or the
   rule, as step 3B words it.
4. **Re-sign** with the credential the handler borrowed before it classified (§4), never a
   second borrow, after any wait on the user and immediately before the forward: the canonical
   request rebuilt with `Host` the endpoint, the real key, `X-Amz-Security-Token` added to the
   signed headers when the credential has one, `X-Amz-Date` and the scope's date set to the time
   of the re-sign, and `x-amz-content-sha256` kept as sent, so a body signed `UNSIGNED-PAYLOAD`
   or by a precomputed hash streams through unread. AWS refuses a signature more than 15 minutes
   old, and an approval can take longer.
5. **Forward** with `Host` the endpoint, over HTTPS, through `Forward`: the sidecar's own dial,
   which checks the endpoint's address as step 4C's handler checks a host's (§5 there) and
   honours the user's `Deny` entries. No AWS host is on the run's allowlist, so a raw `CONNECT`
   to one from the command is class 3 and asks (step 4C, Decisions 4): every AWS call goes
   through this handler or not at all. The response streams back as it comes. A `4xx` whose
   error code is `ExpiredToken` or `ExpiredTokenException` — the `x-amzn-ErrorType` header, or
   the `Code` of a Query or S3 error's XML, read from the first 4 KiB alone — calls
   `MarkExpired(Key{AWS, profile}, profile, secret)`, with the secret access key it signed with
   (step 1D §3), before it streams back. AWS refuses an expired credential with a `4xx`, not a
   `401`: STS and IAM with a `403`, S3 and the JSON-protocol services with a `400`.

A body with chunked signatures (`STREAMING-AWS4-HMAC-SHA256-PAYLOAD`) cannot be re-signed
without re-chunking and is refused 403; the CLI does not send one by default. `aws s3 presign`
signs a URL with the placeholder, which AWS refuses; the prompt says so. S3 goes through this
proxy like every service (Decisions).

### 3. The classifier

`awsproxy/classify.go`: `classify(r *http.Request, scope credScope, body []byte, account
string) permissions.Action`. The action name is `<service>:<Operation>`, read from:

| Where | Form | Services |
| --- | --- | --- |
| `X-Amz-Target` | `Service_Version.Operation`, the operation after the last `.` | the JSON services: `dynamodb`, `logs`, `secretsmanager`, `ssm`, … |
| `Action` in the query or the form body | the operation as given | the Query services: `ec2`, `iam`, `sts`, `rds`, `cloudformation`, `sqs`, … |
| the method and path | `rest.go`'s route table for `eks` and `s3`; elsewhere the method alone | the REST services |

A REST service the table does not know gets `Verb` the method, `Kind` the service, and the
method and path as its operation. It is class 4 whatever the method, since no read table names
it.

**The class**, first that applies:

1. **Refused outright**, before `Decide`, with a 403 naming the action: `sts:GetFederationToken`,
   `sts:GetSessionToken`, `sts:AssumeRole*`, `iam:CreateAccessKey`, `iam:CreateLoginProfile`,
   `iam:CreateServiceSpecificCredential`, `iam:ResetServiceSpecificCredential`,
   `ecr:GetAuthorizationToken`, `ecr-public:GetAuthorizationToken`,
   `codeartifact:GetAuthorizationToken`, `redshift:GetClusterCredentials`,
   `redshift:GetClusterCredentialsWithIAM`, `redshift-serverless:GetCredentials`,
   `ec2:GetPasswordData` and `lightsail:GetInstanceAccessDetails`. Each answers a credential
   the model would read, like the service account token the cluster proxy refuses.
2. **Class 6**: `secretsmanager:GetSecretValue`, `secretsmanager:BatchGetSecretValue`, and
   `ssm:GetParameter`, `GetParameters` and `GetParametersByPath` with `WithDecryption` true. A
   secret's value is a read that carries a write's risk; step 5A's grant applies by provider.
3. **Class 5**: any `iam:*` write, `ec2:TerminateInstances`, `rds:Delete*`, `s3:DeleteBucket`,
   `eks:Delete*`, `dynamodb:DeleteTable`, `cloudformation:DeleteStack`,
   `kms:ScheduleKeyDeletion`, any `organizations:*` write. Step 3B's shipped `AskFor` rule on
   `iam:*` writes matches here.
4. **Class 1**: an action `reads.go` names. The table lists each read by its full action name,
   service by service — `ec2:DescribeInstances`, `eks:ListClusters`, `s3:GetObject`,
   `sts:GetCallerIdentity`, which the store calls itself (step 1D §2) — and a row is added only
   once the operation is known to answer no credential. S3's `GetObject` is a read: data leaves
   the machine on the model's word, as a cluster read does.
5. **Class 4**: everything else, a read the table does not name included. It asks, or runs or
   is refused by the user's rules, as a write does.

`Scope` is `{Account: cred.Account, Region: scope.region}`, the account of the credential the
request is re-signed with.
`Verb` is the operation, `Kind` the service, `Name` the resource when the parameters name one.
`Summary` is *Run `eks:UpdateNodegroupConfig` in `123456789012` / `us-east-1`*.

**The prompt** is step 4B's, as a cluster write's: the summary, then *Show the request* folding
the request as sent — the method, the path with its query, the media type and the body (a
form body decoded one pair per line) — through `VisibleText`. The request is an `approvals`
row of `kind` `action` (step 4B), its `request` a `tools.ActionRequest` whose `Action` is the
classifier's and whose `Write` holds the method, path, media type and body, as a cluster
write's does; there is no diff. The call's disclosure lists it as it lists a cluster write,
tagged by its decision, and the webview draws it with the same `ApprovalRequest`, keyed on the
action's provider for its `aria-label`, *AWS action awaiting approval*.

### 4. The profile

The borrow is step 1D's: `credentials.AWS(ctx, profile)` runs `aws configure
export-credentials` on the host, reads the account with those keys (`sts:GetCallerIdentity`, once
per credential) and caches both to the expiry; `Region` is `aws configure get region`, cached for
step 1D's `defaultTTL`. The handler borrows once, before it classifies (§2, step 3), and both
scopes the request by that credential's account and re-signs with that credential (step 4), so
the account a rule matched is the account the request reaches, and an excluded, expired or
refused profile is answered before anything is classified. This step adds **which profile**.

`tools/bash/aws.go`'s `awsProfileFor(rec, cfg, settings)` picks it, first that applies:

1. **The override**: `securityconfig.Settings.Credentials.Profiles[clusterID]` (step 2D §6, the
   Settings control step 2D draws under its Credentials section), when set and still among the
   profiles the store's last `Found()` lists; one that has since gone falls back to the next rule, and step
   2D's row says so.
2. **The kubeconfig's**: the record's context (`clustercard.ContextName`) in the kubeconfig
   `api.Config` (`Tool.kubeconfig`, a `func() (*api.Config, bool)` `app` passes from
   `kubeconfig.Service.Get`), and its user's `Exec` entry: `--profile <p>` in `Args`, else
   `AWS_PROFILE` in `Env`.
3. `default`: a chat with no cluster, or a context with no `exec` entry.

The region: `--region <r>` in the same `Args`, else `Region(ctx, profile)`, else unset. The choice is
made once per run and kept on the handler, so a Settings change applies to the next command; a
subagent's run reads its parent's session and gets the same. The CLI's own `--profile` cannot
work in the sandbox: the config file is `/dev/null`, so the CLI answers *The config profile (x)
could not be found*. The prompt says so.

### 5. Refusals and errors

Every refusal is a 403 the CLI prints as *An error occurred (AccessDenied) when calling the
<Operation> operation: kstack: <reason>*, so the model reads why. `status.go` writes the shape
the service parses: `{"__type":"AccessDeniedException","message":…}` for a request with
`X-Amz-Target` or a JSON body, else the Query protocol's `<ErrorResponse>` XML.

| Case | Reason |
| --- | --- |
| a signature that does not verify | `the request was not signed by this sandbox` |
| a host or service the endpoint table has no row for | `<service> in <region> has no endpoint the proxy reaches` |
| a chunk-signed body | `a streaming upload with chunked signatures cannot be forwarded` |
| a credential-minting action | `<action> answers a credential and is not allowed` |
| `Denied` by mode or rule | step 3B's wording |
| the borrow answered `ErrExpired` | step 2D's line: `the AWS session for profile <p> has expired; the user can renew it with aws sso login --profile <p>` — and the run's `Credentials` notifier (step 2D §3) is told |
| the borrow answered `ErrExcluded` | step 2D's line naming the setting |
| the borrow failed otherwise | `the AWS credential for profile <p> could not be read (exit N)` |
| no `aws` on the host (`ErrNoCLI`) | `aws is not installed on this machine` |

A response from AWS passes through as it is, its own errors included.

### 6. The prompt

`prompts/sandbox.md` gains one paragraph: `aws` works in the sandbox with the user's own
credentials for the cluster's profile; a common read runs as it is and any other action asks
the user, or runs or is refused by their rules, a refusal `AccessDenied` naming why; `--profile` and
presigned URLs do not work; an action that answers a credential is refused; do not try to read
`~/.aws`. The question's context names the profile under *Sandbox*.

## Decisions this step asks for

1. **S3 goes through the same proxy, path style.** The note asks whether S3 should take the
   general HTTPS path. It is step 5D's, built in the same wave, and after it S3 would still
   need re-signing. Recommended: keep S3 here, refuse chunk-signed uploads, revisit if `aws s3
   cp` of a large file is asked for.
2. **The profile is the kubeconfig's, unless Settings say otherwise.** The cluster's `exec`
   entry names the profile that reaches it, and step 2D's override is for the user whose entry
   does not. Recommended; `default` always breaks every EKS user with two accounts.
3. **AWS secret reads are class 6.** A Secrets Manager value is a Kubernetes Secret's risk on
   another provider, and `Decide` already has the class. Recommended.
4. **SigV4 is Kstack's own code, checked against the SDK in tests.** Verification rebuilds the
   canonical request from a received one, which no SDK signer exposes, and the signer is a
   hundred lines. `go.mod` gains `github.com/aws/aws-sdk-go-v2` (the core module alone) as a
   test dependency. Recommended.
5. **Class 1 is a table of named reads.** A read prefix such as `Get` also covers
   `redshift:GetClusterCredentials`, so a prefix with a list of exceptions fails open for every
   credential-minting read the list misses. Naming each read fails closed: a read the table
   misses asks. Recommended; the cost is a prompt for each unlisted read until its row is added.
6. **The endpoint comes from the table, chosen by the host the CLI was pointed at.** The
   signing name alone is ambiguous, and guessing a host sends a request to AWS the proxy did not
   choose. A service whose prefix differs from its signing name gets its own
   `AWS_ENDPOINT_URL_<ID>` variable; one the table lacks is refused. Recommended; the
   alternative, a variable for every row, grows the run's environment for no gain.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `awsproxy`: SigV4 verify and re-sign, the endpoint table, the status body | `awsproxy/sigv4.go`, `awsproxy/endpoint.go`, `awsproxy/status.go`, their tests | — | Planned |
| 2 | The classifier, the refused list, the read table, class 5 and 6, the summary | `awsproxy/classify.go`, `awsproxy/reads.go`, `awsproxy/rest.go`, their tests | — | Planned |
| 3 | The handler: verify, decide, borrow, re-sign, forward | `awsproxy/awsproxy.go`, its tests | 1, 2 | Planned |
| 4 | The run: the placeholders, the variables, `awsProfileFor` with the override, the route on the run's server | `tools/bash/env.go`, `tools/bash/aws.go`, `tools/bash/proxy.go`, `tools/bash/bash.go`, `app/app.go`, their tests | 3 | Planned |
| 5 | The request through step 4B's `ActionRequest`; the `aria-label` and the disclosure line | `chatsvc/approval.go`, `sidecar/graph/schema.graphqls`, `graph/`, `src/gql/`, `src/lib/chats.tsx`, `chat-transcript.tsx`, their tests | 3 | Planned |
| 6 | The prompt and the context's profile line | `tools/bash/prompts/sandbox.md`, `clustercard/`, their tests | 4 | Planned |
| 7 | End to end through the real sandbox against a fake STS | `app/app_unix_test.go`, `.github/actions/setup-environment` | 4, 5 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4 and 5 at the same time, then 6 and 7 at the
same time, then 8.

## Tests

**`awsproxy`**

- `TestAPlaceholderSignatureVerifies`: a request signed by the SDK's signer with the placeholder
  verifies; one altered after signing, one signed with another secret, and one whose id is not
  the placeholder's are each 403.
- `TestTheReSignedRequestVerifies`: the forwarded request, checked by the SDK's signer with the
  real credentials, verifies, carries `X-Amz-Security-Token` in its signed headers and `Host`
  the endpoint, and keeps `x-amz-content-sha256`.
- `TestALongApprovalIsReSignedFresh`: over a fake clock, a request approved 20 minutes after it
  was signed forwards with `X-Amz-Date` and the scope's date set to the forward's time, and the
  fake endpoint's 15-minute check accepts it.
- `TestTheEndpointTable`: one case per exception, the standard shape, a partition refused;
  `bedrock` on the bare host reaches `bedrock.<region>.amazonaws.com` and `bedrock` on
  `bedrock-runtime.aws.kstack.invalid` reaches `bedrock-runtime.<region>.amazonaws.com`; a
  service with no row, and a prefix whose row signs as another service, are each 403 and reach
  nothing.
- `TestEveryActionIsClassified`: `ec2:DescribeInstances` (1), `s3:GetObject` (1),
  `eks:UpdateNodegroupConfig` (4), `iam:CreateUser` (5), `ec2:TerminateInstances` (5),
  `secretsmanager:GetSecretValue` (6), `sts:GetSessionToken` refused; each of the three places
  the name is read from; a REST route for `eks` and `s3`; an unknown REST service's `GET` (4).
- `TestAReadTheTableDoesNotNameAsks`: `redshift:GetClusterCredentials` is refused, and a `Get`,
  `Describe` and `List` operation absent from `reads.go` are each class 4; no action in the
  refused list is in `reads.go`.
- `TestASummaryReadsAsTheNoteSays`.
- `TestDecideIsWired`: an `Auto` session's write forwards unasked and is recorded `allowed`; an
  `Ask` one waits on the asker and forwards once approved; a `ReadOnly` one is 403 naming the
  mode; a session with `NoPrompts` never asks; a denied one forwards nothing.
- `TestAStreamingPutObjectIsNotBuffered`: a `PutObject` signed `UNSIGNED-PAYLOAD` with a body
  past `maxBody` forwards, read by the fake endpoint alone; `TestAChunkSignedBodyIsRefused`.
- `TestARefusalIsAnAWSShapedError`: the JSON shape for a targeted request, the XML shape for a
  Query one; `TestEachBorrowErrorIsAForbiddenNamingTheProfile`: over a fake `Source`,
  `ErrExpired`, `ErrExcluded`, `ErrNoCLI` and a plain error each print §5's line, and the
  expired one reaches the notifier once.
- `TestAnExpiredTokenMarksTheProfile`: an upstream `403` with `ExpiredToken` in a Query error's
  XML, a `400` with it in an S3 error's XML, and a `400` with `ExpiredTokenException` in the
  header, each call `MarkExpired` for the profile once, with the key the request was signed
  with, and stream the answer back whole; an `AccessDenied` 403 marks nothing.

**`bash`**

- `TestTheSandboxedEnvironmentIsFixed` grows §1's rows, and the never-list test pins that a real
  `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` and `AWS_PROFILE` in the
  sidecar's environment stay out. The note's third invariant.
- `TestThePlaceholderIsTheRunsOwn`: two runs get different pairs, and each handler holds its own.
- `TestTheProfileComesFromTheSettingsThenTheExecEntry`: an override in `Profiles` wins; one
  naming a profile no longer listed falls through; `--profile dev` in the args, `AWS_PROFILE` in
  the env, and neither, over a fake kubeconfig; `--region` read the same way, else the store's.
- `TestTheRunsServerRoutesTheAWSHost` (`proxy_unix_test.go`): a request to `aws.kstack.invalid`
  or `bedrock-runtime.aws.kstack.invalid` with the run's token reaches the handler; without it,
  401 before any signature is read.
- `TestNoCredentialIsWrittenToDiskByARun` (`bash_unix_test.go`; step 1D's invariant over a real
  run): after a borrow and a forwarded request, no file under the data, cache and runtime
  directories, and no log line, holds the secret or the session token.

**`app`** (`app_unix_test.go`, through the real sandbox, `testutil.RequireSandbox`, skipped
where `setup-environment` did not install the AWS CLI): `TestASandboxedAWSReadRunsUnasked`,
`aws sts get-caller-identity` in a chat against a fake STS the `Forward` is pointed at and a
fake `aws` on the host that exports fixed credentials, answers the fake's account with no
request, and the fake saw the real key and none of the placeholder;
`TestASandboxedAWSWriteAsks`, `aws iam create-user` waits on one request and reaches the fake
once approved, and denied, the CLI prints `AccessDenied`.

**Webview** (`chat-transcript.test.tsx`): the AWS request draws its summary, its `aria-label`,
and *Show the request* with the method, path and body as text, and the disclosure's line with
its tag.

## Security

**Widened.** The sandbox can now reach AWS, for everything the borrowed profile can do, and a
class 1 read runs with nobody asked: an S3 object, a parameter without decryption, every
`Describe` the read table names, and the answer leaves the machine on the model's word, as a
cluster read does.

**What bounds it.** No credential enters the sandbox: the placeholder signs for nothing, the
real key is borrowed at use time by step 1D's store and lives in memory, and nothing under
Kstack's directories holds it. Every request is verified before it is classified, and the run's
token is checked before that. Every write goes through `Decide`: class 5 asks in every mode, the
shipped `iam:*` rule cannot be removed, and the actions that mint credentials are refused before
any rule, and a read the table does not name asks. The endpoint is the table's row, found by
the signed `Host` and checked against the scope, never a host the request names, and `Forward`
checks it; a service with no row is refused. A command cannot reach an AWS host around the
handler, since none is on the run's allowlist.

**Residuals.** An allowed write reaches everything the profile can, and a rule's scope, an
account and a region, is wider than a write. A read of S3 objects or `ssm` parameters leaves
the machine unasked. On macOS the run's loopback port carries placeholder-signed requests any
local process could send, which the token check answers 401. A credential-minting action that
is added to the read table by mistake runs unasked; a test keeps the refused list out of the
table, and a row is added only once its answer is known.

The record, `docs/security/<date>-aws-through-the-proxy.md`, argues these and points at step
1D's for the borrow.

## When it lands

- **The security record** above, and an ADR: AWS goes through a re-signing proxy with a
  placeholder key; the profile is the cluster's `exec` entry's, overridable in Settings; S3
  through the same proxy; AWS secret reads are class 6; class 1 is a table of named reads; the
  endpoint is a table row found by the signed host.
- **`security-model.md`**: a row for the sandbox holding no AWS credential
  (`TestTheSandboxedEnvironmentIsFixed`, `TestNoCredentialIsWrittenToDiskByARun`); a row for AWS
  writes through `Decide`, the refused list and the read table (`TestDecideIsWired`,
  `TestEveryActionIsClassified`, `TestAReadTheTableDoesNotNameAsks`); a **By decision** row for reads leaving the machine.
- **`sidecar/CLAUDE.md`**: `awsproxy`, the run's AWS variables and placeholder,
  `awsProfileFor` and its three rules, the route on the run's server, the prompt and context.
- **Root `CLAUDE.md`**, *Chat*: the AWS request and its disclosure line.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the end-to-end tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS or Linux, signed in with `aws sso login` on a profile an EKS
context's `exec` entry names: `aws sts get-caller-identity` in a chat on that cluster answers
with no request; `aws iam create-user --user-name kstack-test` asks, naming the action, the
account and the region, and denied reads `AccessDenied`; `env | grep AWS` prints the
placeholder pair, `/dev/null` for both files, and nothing of yours; after `aws sso logout` the
refusal names the profile; set another profile for the cluster in Settings and read
`get-caller-identity` answer as that profile.
