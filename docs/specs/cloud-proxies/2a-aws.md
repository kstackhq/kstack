# Step 2A: AWS through the re-signing proxy

Answers [#931](https://github.com/kstackhq/kstack/issues/931). Step 2A of
[the cloud proxies](README.md), on `wip/cloud-proxies`. It needs step 1 alone and can be built
beside the wave's other steps (`2b-github.md`, `2c-gcp.md`, `2d-azure.md`).

[Step 1](1-shared-pieces.md) is the contract. This spec states only what is AWS's own and links
step 1 for the rest: the door, the grant, the gate, the request's shape and drawing, the switch
and the seam.

This step changes a security boundary: with its switch on, a sandboxed command reads an AWS
account as the user. It lands with an ADR, a `docs/security/` record and its rows in
[`security-model.md`](../../security-model.md) ([*When it lands*](#when-it-lands)).

Go paths are under `sidecar/internal/` unless rooted elsewhere.

## What is true now

Each line is read from the code, with step 1 built.

- **A sandboxed run holds nothing of AWS.** `~/.aws` is denied always (`run/sandbox/lists.go`).
  `neverEnv` holds `{Prefix: "AWS_"}`, and an `EnvRule` is a `Name` or a `Prefix` and nothing
  else (`run/sandbox/env.go`). `Run.check` (`run/sandbox/sandbox.go`) fails a run whose
  environment holds a name `Unpassable` matches.
- **The sidecar's process holds the user's AWS configuration.** `loginshell`'s `imported`
  (`run/loginshell/loginshell.go`) carries `AWS_PROFILE`, `AWS_REGION`, `AWS_DEFAULT_REGION`,
  `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE` and `AWS_SDK_LOAD_CONFIG`. On macOS
  `app` sets them process-wide (`app/launchshell_darwin.go`), so the EKS `exec` plugin
  authenticates outside the sandbox.
- **`sidecar/go.mod` has no AWS module.** `lib/safe` blanks `AKIA…` and `ASIA…` key ids
  (`awsKeyRE`, `lib/safe/safe.go`).
- **`tools/bash/prompts/sandbox.md` tells the model a tool has a login only where the context's
  `cloud` names its cloud platform.** No build writes that key yet.
- **Step 1 built the shared pieces and no cloud platform.** `sandbox.aws` exists and is off, and
  `permissions.AWS` exists. `app/runtime.go` hands `bash.New` a `bash.Cloud` whose `Platforms`
  is `Config.cloudPlatforms`: none in production, a test's fake otherwise. The wire lists a
  switch per cloud platform of that list, and Settings draws none yet
  ([`TODO.md`](../../TODO.md), *Draw each cloud platform's switch in Settings*).
- **AWS's name and refusals are written.** `tools/bash/cloud.go` holds *AWS* (`platformNames`)
  and fills a mount's `CloudRun.Asker` and `Refusal` (`cloudAskerFor`). The webview holds the
  name too (`CLOUD_PLATFORM_NAMES`, `src/lib/chats.tsx`).

## What this step builds

A sandboxed run calls the AWS API as the user, with no AWS credential inside the sandbox.

1. **The proxy** (`run/awsproxy`), a leaf over a `runproxy.Grant`. Per request it checks the
   run's key id, reads service and region from the signature's scope, classifies the operation,
   drops the sandbox's signature, signs with the user's credentials and forwards over HTTPS to
   a host from its own table.
2. **The credential service** (`services/awscreds`), which resolves the user's credentials
   outside the sandbox.
3. **The cloud platform** (`tools/bash/aws.go`): the route at the loopback authority, six variables,
   and AWS in the list `app/runtime.go` builds.
4. **`EnvRule.Except`** in `run/sandbox`, so the `AWS_` rule lets exactly those six names
   through.
5. **The prompt's paragraph** for AWS.

### Not in this step

- Credentials handed to the sandbox in any form: `AWS_CONTAINER_CREDENTIALS_FULL_URI`, a
  credentials file, a real presigned URL.
- Tools that ignore `AWS_ENDPOINT_URL`. They sign for the real host with the run's key id and
  get AWS's own refusal, or reach nothing with network off.
- A narrower role, a session policy, and a profile per kube-context
  ([*Open questions*](#open-questions)).
- Partitions other than `aws` (`aws-cn`, `aws-us-gov`), FIPS and dual-stack endpoints.
- Services outside [the table](#the-service-table).
- S3 user metadata (`x-amz-meta-*`) and an object's stored headers (`Cache-Control`,
  `Content-Disposition`, `Content-Language`, `Expires`).
- A compressed request body. Step 1's `Grant.Body` refuses a `Content-Encoding`.

## Design

### 1. The run (`tools/bash/aws.go`)

AWS's `CloudPlatform` ([step 1, §7](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash)) is one
file of `tools/bash`: a constructor `app/runtime.go` calls with the `awscreds` service. The
result goes in the `bash.Cloud.Platforms` it hands `bash.New`, so `cloudTargets` gives the wire
and the chat's context AWS with no other change. A test's `Config.cloudPlatforms` stands in
place of the built list, not beside it: step 1's `fakeCloud` (`app/app_unix_test.go`) has
`Target` `AWS` and the loopback route. Which runs get a grant, and when, is
[step 1's](1-shared-pieces.md#4-the-gate).

`Mount` answers:

- **One route**, `127.0.0.1:<port>`, the row
  [the door](1-shared-pieces.md#1-the-door) keeps for AWS. `AWS_ENDPOINT_URL` is dialled
  directly, so the client's `Host` is the loopback authority.
- **One grant**, `runproxy.NewGrant(permissions.AWS, run.Session, run.Asker, run.Refusal,
  limits)`, with no challenge. The limits are `tools/bash/proxy.go`'s `proxyQPS`, `proxyBurst`
  and `proxyMaxInFlight`, and 16 bodies, a constant beside them that the first cloud platform built
  adds.
- **Six variables**, every value from the mount:

  | Variable | Value |
  | --- | --- |
  | `AWS_ENDPOINT_URL` | `http://127.0.0.1:<port>` |
  | `AWS_ACCESS_KEY_ID` | `KSTACK` and the grant's token in upper-case hex, which no real key id's prefix matches |
  | `AWS_SECRET_ACCESS_KEY` | a constant that says it is not a secret |
  | `AWS_REGION`, `AWS_DEFAULT_REGION` | `awscreds.Region()`, both left out when it is `""` |
  | `AWS_EC2_METADATA_DISABLED` | `true`, so no client waits on the metadata address |

It writes no file and sets no proxy variable.

**The token's slot is the SigV4 credential's key id**
([step 1, the token](1-shared-pieces.md#3-the-grant-core-runrunproxy)). A client sends it in
`Authorization`, or in `X-Amz-Credential` for a presigned URL. The grant accepts it nowhere
else.

**`neverEnv` keeps refusing every `AWS_` name but these six.** `EnvRule` gains `Except`, the
exact names a prefix rule lets through, and the `AWS_` rule lists the six above. This is the
change to `run/sandbox/env.go` step 1's seam asks a cloud platform to make in its own step.

**The prompt.** `tools/bash/prompts/sandbox.md` gains AWS's paragraph, read only when the
context's `cloud` names `aws`, in step 1's words. It adds what is AWS's: `aws` and SDK clients
work, and `aws eks get-token`, `sts assume-role` and ECR logins do not. The sentence on tools
with no login is step 1's and does not change. `make generate-prompts` pins the words.

### 2. The identity (`services/awscreds`)

A small service owns the user's credentials, by
[step 1's rules for a credential](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash).

- It loads the AWS SDK for Go v2's default configuration (`config.LoadDefaultConfig`) on first
  use, in the sidecar's process environment: the profile, region and files the EKS plugin
  sees. Loading reads files and runs nothing.
- `Region() string` is the configuration's default region, `""` for none.
- `Credentials(ctx) (aws.Credentials, error)` resolves through the SDK's
  `aws.CredentialsCache`, so an expiring SSO or assumed-role credential refreshes. It runs on
  the first request that needs it, never at startup and never while the switch is off.
- `Drop()` empties the cache. The proxy calls it when AWS says the credential is dead (below).
- **It registers the secret key and the session token with `safe.AddSecret` while it holds
  them, and calls `safe.ForgetSecret` for each when a refresh replaces them or `Drop` empties
  the cache.** The key id is already blanked by `awsKeyRE`.
- **Resolving a profile can run a program** (`credential_process`) and reads the SSO token
  cache, as probing a kubeconfig runs its `exec` plugin. The SDK rewrites
  `~/.aws/sso/cache` when it refreshes an SSO token, as the AWS CLI does. Kstack itself writes
  no credential.
- A chain that fails to resolve is one fixed sentence (*kstack: no AWS login was found; run
  `aws sso login`*), never the SDK's error text.

New modules: `aws-sdk-go-v2`, its `config` and `credentials`, and what `config` pulls in (`sso`,
`ssooidc`, `sts`). The signer is the SDK's `aws/signer/v4`. No service client is imported.

### 3. The proxy (`run/awsproxy`)

A leaf beside `kubeproxy`. It imports `runproxy` and `permissions`, and nothing of `tools` or
`services`. Its `Upstream` is three seams the caller fills: `Credentials(ctx)`, `Drop()` and a
`*http.Client`. Files: `awsproxy.go` (the handler), `scope.go` (the signature's scope),
`services.go` (the table), `classify.go`, `headers.go` (the allowlist), `sign.go`,
`status.go` (refusals), each with its test beside it.

**The handler, in order.** Each `Grant` method is
[step 1's](1-shared-pieces.md#3-the-grant-core-runrunproxy).

1. **Enter.** `Grant.Enter`.
2. **The run's key.** The request names its credential in `Authorization: AWS4-HMAC-SHA256
   Credential=<id>/<date>/<region>/<service>/aws4_request, …` or in `X-Amz-Credential`. The
   handler takes the token out of `<id>`, lower-cases it, since `Token` is lower-case hex, and
   asks `Grant.Holds`. No key id, a wrong one, both
   places at once and any other scheme (SigV4A, SigV2) are `Grant.Challenge`'s `401`. The
   signature is not verified: the id is the token, and the secret beside it in the same
   environment would prove nothing more.
3. **The scope.** `service` must be a row of the table. `region` must match
   `^[a-z]{2}(-[a-z]+)+-[0-9]$` and not start `cn-` or `us-gov-`. Else `403`, naming what was
   read.
4. **Refused shapes.** Step 1's (a path that is not canonical, an `Upgrade`, a
   `Connection: upgrade`), a query `url.ParseQuery` refuses, a path the row cannot map, and
   [a header the list refuses](#the-headers).
5. **The class** for a row that needs no body: a REST row classifies from the method, path
   and query alone.
6. **The body** is read through `Grant.Body`. A query or JSON row is classified now.
7. **The verdict.** A refused operation is a `403`. A read takes `Grant.Slot` and forwards.
   Anything else is one call of `Grant.Decide`, with the forward as its `forward`.
8. **Forward.** `Credentials`, then the request rebuilt and signed.

**The request sent to AWS** is built, not passed through:

- The URL is `https://` + the row's host for the region + the path and query as received, less
  every `X-Amz-Algorithm`, `-Credential`, `-Date`, `-Expires`, `-SignedHeaders`, `-Signature`
  and `-Security-Token` query parameter. Nothing of the request's `Host` reaches the URL.
- The headers are `runproxy.Outbound` over [the list](#the-headers).
- The proxy sets `Host`, `X-Amz-Date`, `X-Amz-Content-Sha256`, `Authorization` and, for a
  temporary credential, `X-Amz-Security-Token`. It signs with the SDK's signer for the row's
  signing name and signing region: the scope's region, or `us-east-1` for a global row. S3's
  path escaping is off for `s3`.
- The client is the sidecar's: system roots, the process's `HTTPS_PROXY`, no redirect followed.
  A redirect is the client's to read.

**The answer** streams back as it arrives (`FlushInterval: -1`, as `kubeproxy` sets). An answer
of status 400 or above is read whole first, up to 64 KiB:

- One that holds the real key id or session token is replaced by a `502`. AWS echoes the key
  id in a signature error.
- One whose error code is `ExpiredToken`, `ExpiredTokenException`, `InvalidClientTokenId`,
  `UnrecognizedClientException` or `InvalidSignatureException` calls `Drop`. AWS answers a dead
  credential `400` or `403` with a code, not `401`, so the code is what the proxy reads.

An upstream failure is a `502` that never carries the error's text.

**A refusal is shaped as the row's protocol reads it**, so the CLI prints the message:
`<ErrorResponse><Error><Code>AccessDenied</Code><Message>kstack: …` for a query or XML row,
`{"__type":"AccessDeniedException","message":"kstack: …"}` with `x-amzn-errortype` for a JSON
one. A refusal is `401`, `403` or `413`, which no SDK retries. Two answers are retried, by the
SDK's own backoff: step 1's `429` for a full queue or no slot, and a `502`. A retried change
asks again.

#### The headers

`headers.go` holds the list `runproxy.Outbound` forwards, by exact name
([step 1](1-shared-pieces.md#what-the-user-approves-is-what-is-sent)).

**Headers that change what a request does.** The classifier reads each, and the request draws
each as a detail.

| Header | Detail's label |
| --- | --- |
| `X-Amz-Target` | *Operation* (the name after the `.`) |
| `X-Amz-Copy-Source` | *Copy source* |
| `X-Amz-Copy-Source-Range` | *Copy source range* |
| `X-Amz-Metadata-Directive` | *Metadata* |
| `X-Amz-Tagging-Directive` | *Tags from* |
| `X-Amz-Acl` | *ACL* |
| `X-Amz-Tagging` | *Tags* |
| `X-Amz-Storage-Class` | *Storage class* |
| `X-Amz-Server-Side-Encryption` | *Encryption* |
| `X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id` | *KMS key* |

**Headers that only narrow a request or check it**, forwarded and not drawn:

- `Range`, `If-Match`, `If-None-Match`, `If-Modified-Since`, `If-Unmodified-Since`,
  `X-Amz-Expected-Bucket-Owner`.
- `Content-MD5`, `X-Amz-Sdk-Checksum-Algorithm`, `X-Amz-Checksum-Crc32`,
  `X-Amz-Checksum-Crc32c`, `X-Amz-Checksum-Crc64nvme`, `X-Amz-Checksum-Sha1`,
  `X-Amz-Checksum-Sha256`, with a body read whole. `X-Amz-Checksum-Mode`.

**Headers that carry no meaning to the operation:** `Content-Type` (drawn as the media type),
`Accept`, `User-Agent`, `Amz-Sdk-Invocation-Id`, `Amz-Sdk-Request`, `X-Amzn-Query-Mode`.

**Headers the proxy reads and never forwards:** `Authorization`, `X-Amz-Date`,
`X-Amz-Security-Token` and `X-Amz-Content-Sha256`.

**A request with any other header whose name starts `x-amz` is refused** (`403`, naming the
header). Forwarding it without the header would run a different request from the one the
command sent: `X-Amz-Bypass-Governance-Retention`, `X-Amz-Mfa`, `X-Amz-Request-Payer`,
`X-Amz-Grant-*` and `X-Amz-Meta-*` are refused this way. Any other header is left out, as
`Outbound` leaves it.

Task 1 records every header the clients send, and this list is corrected there.

#### The body

- **A body is read through `Grant.Body`**: at most 1 MiB, in one of the grant's body slots,
  refused with a `Content-Encoding`. `Body` answers a status and no message (`403`, `429`,
  `413`, `400`), so the handler writes each in the row's shape. A query row's read is a `POST`
  with a form body, so it takes a slot as a change does.
- **No body is `Unshown`.** A sandbox with no network has no way out, and an upload the user
  cannot read would be one: bytes approved on a bucket and key the command chose, in any
  account that takes them. So a body is shown or the request is refused.
- **An object upload works only as shown text**: a `PutObject` of valid UTF-8, at most 1 MiB,
  sent whole. It is drawn and asks as any change does.
- **`aws-chunked` is refused**, by `Grant.Body`'s rule on `Content-Encoding`, with a sentence
  of its own: *kstack: the sandbox does not upload files to S3; do it outside the sandbox*.
  Recent CLIs frame every `PutObject` and `UploadPart` that way, so `aws s3 cp` to a bucket
  does not work in this version ([*Open questions*](#open-questions), 6).
- The payload hash of a body read whole is computed from the bytes read.

### 4. The service table

**`services.go` is Kstack's list, and adding a row is a security change**, as a sandbox zone
is. A row names the signing name, the protocol, the host (regional, or one global host signed
in `us-east-1`) and four lists of operations: reads beyond the prefixes, class 5, class 6 and
refused.

| Signing name | Protocol | Host |
| --- | --- | --- |
| `sts` | query | `sts.<region>.amazonaws.com` |
| `ec2` | query | `ec2.<region>.amazonaws.com` |
| `eks` | REST | `eks.<region>.amazonaws.com` |
| `elasticloadbalancing` | query | `elasticloadbalancing.<region>.amazonaws.com` |
| `autoscaling` | query | `autoscaling.<region>.amazonaws.com` |
| `iam` | query | `iam.amazonaws.com`, global |
| `s3` | REST | `s3.<region>.amazonaws.com`, path-style |
| `logs` | JSON | `logs.<region>.amazonaws.com` |
| `monitoring` | query or JSON | `monitoring.<region>.amazonaws.com` |
| `route53` | REST | `route53.amazonaws.com`, global |
| `cloudfront` | REST | `cloudfront.amazonaws.com`, global |
| `ecr` | JSON | `api.ecr.<region>.amazonaws.com` |
| `cloudformation` | query | `cloudformation.<region>.amazonaws.com` |

A service not in the table is a `403` that names it, so the model can say which service the
user would have to run outside the sandbox.

### 5. The class

The classes and the verdict are [step 1's](1-shared-pieces.md#4-the-gate). An AWS action is
`Target` `AWS`, its `Class` and its `Summary`, and no other field: no kube-context and no mode
is read. `Summary` is *AWS: call ec2:TerminateInstances in eu-west-1*, or the method and path for
a REST row; the service, region and operation are its *Details* below.

The classifier reads the operation the way the row's protocol carries it. It checks the lists
in this order: refused, class 6, class 5, read, and what is left is class 4.

**The operation.**

- **Query**: the `Action` parameter of the form body or the query, exactly one.
- **JSON**: the name after the `.` in `X-Amz-Target`.
- **REST**: the method, path and query.

**A read** is a query or JSON operation whose name starts `Describe`, `Get` or `List`, or a
REST `GET` or `HEAD`, or an operation the row lists:

| Row | Reads beyond the prefixes |
| --- | --- |
| `iam` | `Generate*Report`, `Simulate*Policy` |
| `logs` | `FilterLogEvents`, `StartQuery`, `StopQuery`, `TestMetricFilter` |
| `ecr` | `BatchGetImage`, `BatchCheckLayerAvailability` |
| `cloudformation` | `ValidateTemplate`, `EstimateTemplateCost` |

**Refused**, since the answer is a credential:

| Row | Refused |
| --- | --- |
| `sts` | every operation but `GetCallerIdentity` |
| `ec2` | `CreateKeyPair`, `GetPasswordData` |
| `iam` | `CreateAccessKey`, `CreateServiceSpecificCredential`, `ResetServiceSpecificCredential`, `CreateVirtualMFADevice` |
| `ecr` | `GetAuthorizationToken`, and `GetDownloadUrlForLayer`, whose answer is a presigned URL |

**Class 6**, stored data and metadata AWS documents as holding secret values:

| Row | Class 6 | What it holds |
| --- | --- | --- |
| `s3` | a `GET` whose path names a key, unless its query names `acl`, `attributes`, `legal-hold`, `retention`, `tagging` or `uploadId`; a `POST` with `select` | an object's data |
| `ec2` | `DescribeInstanceAttribute`, unless `Attribute` is present and not `userData` | user data |
| `ec2` | `DescribeLaunchTemplateVersions`, `GetLaunchTemplateData`, `DescribeSpotInstanceRequests`, `DescribeSpotFleetRequests` | user data in a launch specification |
| `ec2` | `GetConsoleOutput`, `GetConsoleScreenshot` | what an instance printed |
| `ec2` | `DescribeVpnConnections` | the tunnels' pre-shared keys |
| `autoscaling` | `DescribeLaunchConfigurations` | user data |
| `cloudformation` | `GetTemplate`, `GetTemplateSummary`, `DescribeStacks`, `DescribeStackSet`, `DescribeChangeSet`, `DescribeStackEvents` | a template, its parameters and outputs, and resource properties; `NoEcho` masks a parameter and nothing it was copied to |
| `logs` | `GetLogEvents`, `FilterLogEvents`, `GetLogRecord` and `GetQueryResults` with `unmask` true | data a protection policy masks |

So `aws s3 cp` down asks once per object.

**Class 5**, AWS's high blast-radius list:

- A query or JSON operation whose name starts `Delete` or `Terminate`.
- A REST `DELETE`, and S3's `POST` with `delete`.
- On `iam`: `Attach*Policy`, `Put*Policy`, `CreatePolicyVersion`, `SetDefaultPolicyVersion`,
  `UpdateAssumeRolePolicy`, `AddUserToGroup`, `CreateLoginProfile`, `UpdateLoginProfile`.

**Everything else is class 4.** The classifier fails toward asking: an operation it cannot
name, two `Action` parameters, and a `GET` on a query row with no `Action` are class 4.

**Details.** Every request that asks draws *Service*, *Region* and, for a query or JSON row,
*Operation*, then a line per header in [the first headers table](#the-headers). The labels
are constants of `awsproxy`.

### 6. What a run can and cannot do with it

- **Presigned URLs.** `aws s3 presign` signs inside the sandbox with the run's key id for the
  loopback endpoint. Fetched from inside the same run, the URL reaches the proxy, which reads
  its `X-Amz-Credential`, strips the presign parameters and signs a header-authenticated
  request. It reads an object, so it asks. Outside the run the URL is worth nothing. The proxy
  never mints a real one.
- **`aws eks get-token`** yields a token signed with the secret that signs nothing, which no
  cluster accepts. `kubectl` in the sandbox goes through the cluster proxy and never needs it.
- **Cross-region S3.** A bucket in another region answers with a redirect error naming its
  region. The client signs again for that region and the proxy maps the new scope.
- **No file upload to S3.** `aws s3 cp` and `sync` to a bucket are refused
  ([the body](#the-body)). Downloads ask, as class 6.
- **With network on**, real AWS hosts are reachable, and a client that ignores the endpoint
  sends the run's key id there. It identifies nothing.

## Tasks

All start Planned. Each ends green under `make test-changed`.

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | Measure the clients, and correct this spec where one disagrees | this file | — | Planned |
| 2 | `EnvRule.Except` and the six names | `run/sandbox/env.go` | — | Planned |
| 3 | The scope, the table, the classifier and the header list | `run/awsproxy/scope.go`, `services.go`, `classify.go`, `headers.go` | 1 | Planned |
| 4 | The handler: the key, reads, signing, the answer check, refusal shapes | `run/awsproxy/awsproxy.go`, `sign.go`, `status.go` | 3 | Planned |
| 5 | The ask path through `Decide`, the body, the upload refusal | `run/awsproxy/awsproxy.go` | 4 | Planned |
| 6 | `services/awscreds` and the modules; the sidecar's size before and after, in the PR | `services/awscreds/`, `sidecar/go.mod` | — | Planned |
| 7 | The cloud platform, the body limit, AWS in `app`'s list, the prompt | `tools/bash/aws.go`, `tools/bash/proxy.go`, `tools/bash/prompts/sandbox.md`, `app/runtime.go`, the prompt goldens (`app/testdata/prompt/`) | 2, 5, 6 | Planned |
| 8 | AWS through the real sandbox | `app/app_unix_test.go` (`startE2E`) | 7 | Planned |
| 9 | Docs, per *When it lands* | see there | 1–8 | Planned |

**Task 1's measurement.** Before any code, run AWS CLI v2 and one SDK (boto3) against a
throwaway listener at `AWS_ENDPOINT_URL=http://127.0.0.1:<port>` with the six variables.
Record here what they send for `sts get-caller-identity`, `ec2 describe-instances`,
`iam list-roles`, `s3 ls`, `s3 ls s3://bucket`, `s3 cp` both ways (small, and past the
multipart threshold) and `s3 presign`. Confirm what the design rests on:

- the `Host` sent, and path-style S3 at an IP endpoint;
- the scope's region for a global service;
- every header sent, against [the list](#the-headers);
- which uploads the CLI and boto3 frame as `aws-chunked`, and which they send whole;
- that a 70-character key id is accepted;
- the read timeout and retry while a request waits on the user;
- the shape of refusal each prints legibly, and what each does with a `429`;
- the same with `HTTP_PROXY` set and `NO_PROXY=127.0.0.1`, since botocore reads both and
  `2b-github.md` sets the first.

2, 3 and 6 can start together once 1 is done.

## Acceptance

Each is a test beside its file unless marked manual. The upstream is an `httptest` TLS server
behind the proxy's host seam, and the credentials are known test values.

**`run/sandbox`**

- `TestOnlyTheSixAWSNamesPass`: each of the six is passable, and `AWS_PROFILE`,
  `AWS_SESSION_TOKEN`, `AWS_CONTAINER_CREDENTIALS_FULL_URI` and `AWS_ENDPOINT_URL_S3` are not.
- `TestARunWithAnUnpassableVariableIsRefused` still fails a run holding any other `AWS_` name,
  on both platforms.

**`run/awsproxy`**

- `TestOnlyTheRunsKeyIdOpens`: none, a wrong one, a real-looking `AKIA…`, SigV4A, and the
  run's own after `End` are each `401` and reach no upstream. `TestAPresignedKeyIdOpens`.
- `TestTheHostComesFromTheTable`, for every row; the request's `Host`, an unknown service and
  each malformed region reach no upstream.
- `TestEveryRowsOperationsAreClassed`: a table with a row per operation this spec names, and
  per prefix, giving read, 4, 5, 6 or refused.
- `TestAnUnprovenReadAsks`: `DescribeInstanceAttribute` with no `Attribute`, two `Action`
  parameters, and an S3 `GET` of a key with `versionId`.
- `TestAReadIsSignedAnew`: the upstream verifies the signature with the test credentials, and
  nothing of the sandbox's signature, key id or date arrives.
- `TestAPresignedURLIsSignedAnew`: no presign parameter reaches the upstream.
- `TestOnlyAnApprovalForwardsAChange`; `TestARefusedOperationReachesNoUpstream`, for each
  named refusal, unasked.
- `TestTheDetailsDrawEveryHeaderThatChangesTheRequest`: each header of the first headers table
  arrives as a detail and at the upstream.
- `TestAnUnlistedAmzHeaderIsRefused`; `TestOnlyListedHeadersReachAWS`.
- `TestAChunkedUploadIsRefused`, with its sentence, unread and never asked;
  `TestABinaryUploadIsRefused`; `TestATextUploadAsksShown`: a `PutObject` of text sent whole
  waits with its body drawn.
- `TestARefusalIsShapedForItsProtocol`, query, XML and JSON.
- `TestAnErrorHoldingTheRealKeyIdIs502`; `TestADeadCredentialIsDropped`, for each code;
  `TestAnUpstreamFailureCarriesNoText`.

**`services/awscreds`**

- `TestNothingResolvesUntilAsked`: building the service and reading `Region` runs no
  `credential_process`.
- `TestAHeldCredentialIsBlanked`, and `TestADroppedCredentialIsForgotten`: after `Drop`, and
  after a refresh, the old secret key and session token are no longer blanked.
- `TestAFailedChainIsOneSentence`.

**`tools/bash`**

- `TestTheAWSMountSetsExactlyTheSixVariables`, with the table's values, and none when
  `Region` is `""` for the two region names.
- `TestNoRealCredentialIsInTheRun`: with test credentials resolved, no key id, secret or
  session token is in the run's environment, its run directory, its `TMPDIR` or its tool
  home.
- `TestTheSandboxPromptSaysWhenToReadAWS` (`bash_test.go`): the paragraph is static, and tells
  the model to read it only where the context's `cloud` names `aws`. The prompt goldens.

**`app`** (`startE2E`, `testutil.RequireSandbox`), network off. As
`TestASandboxedCommandReachesACloudPlatformThroughTheDoor` does with its fake, the test sets
`Config.cloudPlatforms`: AWS's cloud platform over a fake upstream and known credentials.

- With the switch turned on through `sandboxCloudAccessSet`, a sandboxed `curl --aws-sigv4`
  using the run's variables reads unasked, and `kubectl` still reads the cluster. A change
  waits on one request served in `sandboxActions` and reaches the upstream once approved.
- With the switch off, the run's environment holds no `AWS_` name.

**Manual**, on macOS and Linux, against a real account with network off and the switch turned
on through `sandboxCloudAccessSet`, since Settings draws none yet: `aws sts
get-caller-identity`, `aws ec2 describe-instances` and `aws s3 ls` work; `aws ec2
stop-instances` asks; `aws s3 cp` up and down each ask; `aws ecr get-login-password` is
refused. CI has no AWS account.

**Integration gate:** the full checks in
[`../README.md`](../README.md#verification-commands), with the wire checks. The run with every
built cloud platform on is the sequence's gate ([`README.md`](README.md)).

## Security

**What this step widens.** With `sandbox.aws` on, a sandboxed command reads, on the model's
word and unasked, the metadata and state the user's AWS identity can read in the listed
services. The answer reaches the model's cloud platform, and with network on a command can send it
anywhere. This is the widening [step 1](1-shared-pieces.md#security) sets up, and its
residuals hold here.

**What holds, beyond step 1's.**

- **No AWS credential is in the sandbox.** The key id is the grant's token, and the secret
  signs nothing AWS accepts. `TestNoRealCredentialIsInTheRun` pins it.
- **The sandbox's signature decides nothing.** It is dropped, and the proxy signs a request it
  built: its own host, its own date, the listed headers.
- **The upstream is a host of the table.** Service and region come from the scope, the region
  by pattern, so a command cannot name a host.
- **No answer is a credential.** STS but for `GetCallerIdentity`, key pairs, access keys, ECR
  logins and presigned layer URLs are refused, unasked.
- **`neverEnv` still refuses every `AWS_` name but six**, and each of the six is set from the
  mount.
- **The real key id does not come back in an error.**

**Residuals**, each a **By decision** row:

- Resolving the user's profile can run its `credential_process`, a program of the user's
  configuration, outside the sandbox.
- Metadata no list names can hold a secret: a tag, a description, a CloudFront origin's custom
  header, a log event.
- No file is uploaded to S3 from the sandbox.
- The service table and the class lists are hand-written. A read operation AWS adds under
  `Describe`, `Get` or `List` whose answer is a credential is read unasked until a row names
  it.
- The chat's AWS identity is the sidecar's default chain, which need not be the identity the
  chat's kubeconfig uses for its cluster.

## When it lands

When this step is complete on `wip/cloud-proxies`:

- **ADR**: *a sandboxed command reaches AWS through a re-signing proxy*. It covers AWS alone
  and cites [step 1's
  ADR](../../adr/2026-11-17-a-sandboxed-command-reaches-a-cloud-platform-through-a-proxy-on-its-runs-one-relay.md)
  for the door, the gate and the switch. It records why re-signing and not handed-out credentials,
  why the key id is the token, why the services are a list, why no upload is approved unshown, and
  why `neverEnv` gained an exception.
- **`docs/security/<date>-sandboxed-aws.md`**, from *Security* above, naming the commit.
- **`docs/security-model.md`**:
  - New **Enforced** rows: *a sandboxed command reaches AWS only with `sandbox.aws` on, only
    the listed services, as the user's identity and holding none of it*; *an AWS answer that
    is a credential is refused*; *no real AWS credential is in a run*, each with its tests
    above.
  - The **Not built** row on the four cloud platforms is narrowed to the cloud platforms not yet
    built, and removed by the last.
  - *No credential variable rides in, and no run holds a name `NeverEnv` lists* names the six
    variables and `TestOnlyTheSixAWSNamesPass`.
  - New **By decision** rows for the residuals above, linking the ADR.
  - *Assets* gains the user's AWS credentials.
- **`docs/TODO.md`**: *Let a sandboxed command reach the user's cloud accounts without holding
  a credential* is narrowed the same way. *Draw each cloud platform's switch in Settings* gains
  AWS's line of the confirm: *An object's contents ask first.* New lines: a narrower role per
  profile, a profile per kube-context, more services.
- **`docs/persisted-data.md`**: nothing. This step persists no format.
- **`CLAUDE.md`**: `sidecar/internal/run/CLAUDE.md` (the `awsproxy` leaf, the count of
  leaves, `neverEnv`'s exception); `sidecar/internal/tools/CLAUDE.md` (`aws.go`, its six
  variables, the body limit); `sidecar/CLAUDE.md` (`services/awscreds`). Raise a budget in
  `.github/ci-config/claude-md-budgets.yml` only with the reason in the commit body.
- **`docs/development-switches.md`** only if a debug override is added for the upstream host.
- This file is deleted and its row leaves [`README.md`](README.md).

## Open questions

1. **Which identity?** This spec uses the sidecar's default chain, the one the EKS plugin
   sees. A kubeconfig whose `exec` block names `--profile` or `--role-arn` authenticates the
   cluster as someone else. Reading the profile from the chat's kubeconfig is more faithful
   and more brittle. Recommended: the default chain, with the other as a follow-up.
2. **A narrower role or a session policy.** A session policy needs `AssumeRole` or
   `GetFederationToken`, so it cannot narrow an SSO role's credentials without a role to
   assume. Is a setting naming a read-only role per profile worth a follow-up, or is the
   classifier the gate for good?
3. **The first service list.** Are `ssm`, `secretsmanager`, `kms`, `rds`, `lambda`, `ecs` or
   `acm` wanted at once? Step 1's class 6 now fits them. Their rows would class as 6:
   `secretsmanager` `GetSecretValue` and `BatchGetSecretValue`; `ssm` `GetParameter`,
   `GetParameters`, `GetParametersByPath` and `GetParameterHistory` with `WithDecryption`;
   `lambda` `GetFunction`, `GetFunctionConfiguration` and `ListFunctions` (the environment);
   `ecs` `DescribeTaskDefinition` (the container environment). `lambda` `GetFunction` also
   answers a presigned URL to the code, so it may be a refusal instead.
4. **Is the class 6 list right?** Two calls are close. Log events are stored data, and this
   spec reads them unasked unless unmasked, as a cluster's pod logs are read.
   `cloudformation` `DescribeStacks` is class 6, so the commonest stack read asks.
5. **Is the class 5 list right?** It is by name (`Delete`, `Terminate`) and IAM's policy
   changes. The verdict is the same as class 4; only the drawing differs.
6. **Uploads to S3, later?** This version refuses them, since an unshown upload is a way out
   of a sandbox with no network. A follow-up could allow one to a bucket the user named in
   Settings, or show a digest and size for the user to check. Is either wanted?
7. **S3 user metadata.** `x-amz-meta-*` is a prefix, and step 1's list is exact names, so a
   request carrying one is refused. Is that acceptable for a first build?
8. **Request compression.** The CLI gzips a few large bodies (`PutMetricData`), which
   `Grant.Body` refuses. Setting `AWS_DISABLE_REQUEST_COMPRESSION` would be a seventh name.
9. **A query row's body is drawn as sent**, one url-encoded line. The details name the
   operation; the parameters are in the line. Is that legible enough to approve?
10. **The dependency.** The SDK's `config` module brings SSO and STS clients into the sidecar.
    If the size task 6 measures is too much, the alternative is running `aws configure
    export-credentials` outside the sandbox, which needs the user's CLI and runs it on each
    refresh.
11. **A waiting request and the client's timeout.** If task 1 shows the CLI gives up and
    retries while the user reads a request, the retry asks again. `AWS_MAX_ATTEMPTS=1` would
    be another name past the six. Do we set it, or accept a second request?
