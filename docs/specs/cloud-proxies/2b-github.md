# Step 2B: GitHub through the token-injecting proxy

Answers [#932](https://github.com/kstackhq/kstack/issues/932). A wave 2 step of
[the cloud proxies](README.md), on `wip/cloud-proxies`. It needs [step 1](1-shared-pieces.md)
alone and can be built beside the wave's other steps (`2a-aws.md`, `2c-gcp.md`,
`2d-azure.md`).

Step 1 owns the door, the grant, the gate, the request's shape and drawing, the consent switch
and the cloud platform seam. This step links to them and states only what is GitHub's.

It changes a security boundary: with the switch on, a sandboxed command reads GitHub as the
user. It lands with an ADR and a `docs/security/` record ([*When it lands*](#when-it-lands)).

Go paths are under `sidecar/internal/` unless rooted elsewhere.

## What is true now

Each line is read from the code, beyond what step 1 lists.

- **No GitHub login is readable.** `~/.config/gh`, `~/.git-credentials` and `~/.netrc` are
  denied always (`run/sandbox/lists.go`).
- **No GitHub credential variable passes.** `neverEnv` holds `GITHUB_TOKEN`, `GH_TOKEN` and
  `SSH_AUTH_SOCK` (`run/sandbox/env.go`).
- **The prompt says so.** `tools/bash/prompts/sandbox.md`: *With the internet a command still
  holds no credential, so a tool has a login only where the context's `cloud` names its
  cloud platform, and a private registry has none.* No build writes that key yet.
- **`gqlparser` is a dependency** (`sidecar/go.mod`), used by the wire.
- **Step 1 built the shared pieces and no cloud platform.** `sandbox.github` exists and is off, and
  `permissions.GitHub` is a `Target`. `app/runtime.go` hands `bash.New` a `bash.Cloud` whose
  `Platforms` is
  `cloudPlatformsFor`'s: the build's list, `builtCloudPlatforms` (`app/cloudplatforms_unix.go`), or a
  test's `Config.cloudPlatforms` in its place, and none on a machine with no sandbox. The
  wire lists a switch per cloud platform of that list, and Settings draws none yet
  ([`TODO.md`](../../TODO.md), *Draw each cloud platform's switch in Settings*).
- **GitHub's name and refusals are written.** `tools/bash/cloud.go` holds *GitHub*
  (`platformNames`) and fills a mount's `CloudRun.Asker` and `Refusal` (`cloudAskerFor`). The
  webview holds the name too (`CLOUD_PLATFORM_NAMES`, `src/lib/chats.tsx`).

## What this step builds

On macOS and Linux, once `sandbox.github` is on:

- `git clone`, `git fetch` and `git ls-remote` of a `github.com` repository the user can read
  work in a sandboxed run with network off.
- `gh api user`, `gh pr list`, `gh run list`, `gh release view` and the rest of `gh`'s reads
  work there too.
- A `git push`, and every API request that changes something, waits on a request the user
  answers.
- No GitHub token is in the run's environment or files.

The pieces:

1. **`run/githubproxy`**: the handler over a `runproxy.Grant`, the upstream table, the
   classifier for REST, GraphQL and Git's smart HTTP, and the push reader.
2. **`services/githubauth`**: the user's token, read from `gh` outside the sandbox.
3. **`tools/bash/github.go`**: GitHub's `CloudPlatform`, which writes the run's `gitconfig` and
   `gh/hosts.yml` and sets its variables.
4. GitHub's paragraph in `tools/bash/prompts/sandbox.md`, and GitHub in the list
   `app/runtime.go` builds.

### Not in this step

- **Only `github.com`.** No GitHub Enterprise Server, no `ghe.com` tenant.
- **Only `git` and `gh`.** A client that dials `https://api.github.com` itself (octokit, a
  script's `curl`) reaches nothing with network off, and GitHub with no login with network on.
- **Nothing GitHub serves from another host.** Workflow logs, artifacts, release assets and
  source archives are answered with a link to another host. The proxy follows nothing
  ([§3](#3-the-classifier)), so with network off they fail.
- **A renamed repository.** GitHub answers its old name with a redirect to `github.com` or
  `api.github.com`, which the client follows past the proxy.
- **Git LFS**, the dumb Git protocol, and release-asset upload (`uploads.github.com`).
- **No repository allowlist.** Git and REST name the repository in the path, but GraphQL names
  it in the document, by name or node id. A user who wants a narrower reach logs `gh` in with a
  fine-grained token.
- **SSH.** A `git@github.com:` remote is rewritten to the proxy's HTTP route; nothing speaks
  SSH.
- Anything on Windows, and anything for a monitor
  ([step 1](1-shared-pieces.md#4-the-gate), *Who gets a cloud platform at all*).

## Design

### 1. The hosts, and how `git` and `gh` reach them

Checked by hand with git 2.47 and gh 2.103 against a logging proxy. Task 1 repeats the check
and the tests pin each line.

**The hosts are `gh`'s choice.** `gh` speaks plain HTTP to one host name alone: with
`GH_HOST=github.localhost` it sends REST to `http://api.github.localhost/` and GraphQL to
`http://api.github.localhost/graphql`. So GitHub is the exception to
[step 1's naming rule](1-shared-pieces.md#1-the-door): the client dictates the name, and
`<name>.github.kstack.invalid` would make `gh` dial HTTPS.

| `Host` | Upstream | Serves |
| --- | --- | --- |
| `github.localhost` (`GitHost`) | `https://github.com` | Git's smart HTTP protocol alone |
| `api.github.localhost` (`APIHost`) | `https://api.github.com` | REST and GraphQL |

Neither name is resolved. Both tools send the request to the proxy in absolute form, so the
name arrives as `Host`. The two are GitHub's routes on the door.

**No TLS route.** [Step 1](1-shared-pieces.md#2-the-transport) says why `gh` cannot trust a
per-run certificate. `git` follows `gh` onto plain HTTP, so one transport serves both.

**The token's slot is `Proxy-Authorization`**, `Basic` with the user `kstack` and the
[grant's token](1-shared-pieces.md#3-the-grant-core-runrunproxy) as the password. Both tools
have a proxy setting, and no browser page can set the header. `git` sends proxy credentials
only once challenged, so the grant is built with `Limits.Challenges`: a request with no token
is answered by `Grant.Challenge`, `407` with `Proxy-Authenticate: Basic`.

**`git`.** The mount writes `<run dir>/gitconfig`:

```ini
[url "http://github.localhost/"]
	insteadOf = https://github.com/
	insteadOf = git@github.com:
	insteadOf = ssh://git@github.com/
[http "http://github.localhost/"]
	proxy = http://kstack:<grant token>@127.0.0.1:<port>
```

Its variables name the file without replacing the system or global config:
`GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=include.path`, `GIT_CONFIG_VALUE_0=<run dir>/gitconfig`
and `GIT_TERMINAL_PROMPT=0`. Git applies `insteadOf` when it reads a remote back
(`git remote get-url`), so `gh` in a clone of `https://github.com/o/r` finds its repository.

**`gh`.** The mount writes `<run dir>/gh/hosts.yml`:

```yaml
github.localhost:
    oauth_token: kstack-placeholder
    git_protocol: https
```

Its variables are `GH_CONFIG_DIR=<run dir>/gh`, `GH_HOST=github.localhost`,
`GH_PROMPT_DISABLED=1` and `HTTP_PROXY=http://kstack:<grant token>@127.0.0.1:<port>`. `gh`
refuses to run with no login, so it reads the placeholder as one and sends it as
`Authorization`. The proxy never forwards it ([§2](#2-the-proxy-rungithubproxy)). `gh` works
with its config directory read-only.

Both files follow [the seam's rules for a
file](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash): 0600, `O_EXCL`, the grant's token or
the fixed placeholder, no user text.

**`HTTP_PROXY` is the one variable that is not GitHub's own.** `gh` has no proxy setting.
Upper-case `HTTP_PROXY` is read by Go and Python programs and ignored by `curl` and `wget`.
Its cost:

- **Network off:** nothing. Nothing else is reachable.
- **Network on:** a Go or Python tool's plain-HTTP request to any other host goes to the door
  and is answered its `401`. HTTPS is untouched. kubectl and helm use the kubeconfig's
  `proxy-url`.
- **Another cloud platform on:** the run holds `NO_PROXY=127.0.0.1` and `no_proxy=127.0.0.1`, which
  step 1 sets for any proxy variable, so a client dialling the loopback authority (the AWS CLI)
  stays direct.
- **The token is in the variable**, so any Go or Python program in the run sends it to the
  door. It opens GitHub's grant alone.

`GH_TOKEN` would give `gh` its login without a file, but `neverEnv` refuses it and that list
does not change.

### 2. The proxy (`run/githubproxy`)

A leaf beside `runproxy`, over a `runproxy.Grant`. It imports nothing of `tools` or
`services`. `New` takes the grant, a `TokenSource` and the two upstream origins: production
passes the constants, a test its fake's.

**The handler, in order.** The shared steps are
[step 1's](1-shared-pieces.md#3-the-grant-core-runrunproxy).

1. `Grant.Holds` on the `Proxy-Authorization` password, else `Grant.Challenge`. Then
   `Grant.Enter`.
2. A path that is not canonical, an `Upgrade` and a `Connection: upgrade` are refused.
3. The request is classified by its host, method, path and headers
   ([§3](#3-the-classifier)): refused, a read, or one that asks.
4. A body the classifier or the user must read is read through `Grant.Body`: a GraphQL
   request's, and an API change's. A push's is read as [§4](#4-what-a-request-shows) says.
   A fetch's (`git-upload-pack`) is streamed unread: its path says what it is.
5. A read takes `Grant.Slot` and is forwarded. Anything else goes through `Grant.Decide`.
6. **Forwarding** builds the headers with `runproxy.Outbound` and the list below, reads the token
   from the `TokenSource` and adds it: `Authorization: Bearer <token>` to the API,
   `Authorization: Basic` of `x-access-token:<token>` to Git.
7. The answer passes as sent, less `Set-Cookie`. An upstream `401` drops the held token
   ([§5](#5-the-credential-servicesgithubauth)). An upstream failure is a `502`.
8. **A redirect is never followed.** A `Location` on the request's own upstream passes
   unchanged. A `Location` to another host passes only on an approved class 6 request; on any
   other it is replaced by a refusal, so no signed link reaches a command unasked.

**Refusals** start `kstack:` and are `403`, which neither tool retries. The API host writes
GitHub's error shape, `{"message": "…"}`, which `gh` prints. The Git host writes `text/plain`.
With no login found the answer is one fixed sentence: *kstack: no GitHub login was found; run
`gh auth login` outside Kstack.*

**The headers forwarded**, by exact name. Nothing else of the request's reaches GitHub.

| Header | To | Changes what the request does? |
| --- | --- | --- |
| `Accept` | both | Yes: a media type picks the answer's form, and on a release asset the asset itself. The classifier reads it; a request that asks draws it as the detail *Accept* |
| `X-GitHub-Api-Version` | API | Yes: it picks the REST version that reads the body. Drawn as the detail *API version* |
| `GraphQL-Features` | API | Yes: it turns on preview fields. Forwarded on `/graphql` alone, drawn as the detail *Features* |
| `Git-Protocol` | Git | Yes: it picks the protocol version. Forwarded on a `git-upload-pack` request alone, which is a read in every version; never on `git-receive-pack`, so a push arrives in the form the push reader reads |
| `Content-Type` | both | Drawn as the request's media type |
| `Content-Encoding` | Git | Forwarded on `POST …/git-upload-pack` alone, whose body git compresses. Anywhere else a request carrying it is refused |
| `Content-Length`, `Accept-Encoding`, `User-Agent`, `If-None-Match`, `If-Modified-Since` | both | No |

`X-HTTP-Method-Override`, `X-GitHub-OTP`, `Cookie` and the placeholder `Authorization` are not
on the list.

### 3. The classifier

Every request lands in one row of [step 1's class table](1-shared-pieces.md#4-the-gate). What
the classifier cannot prove a read asks. An action has `Target` `GitHub`, its `Class` and
its `Summary`, and no other field; no kube-context and no mode is read.

**`GitHost`.** Only Git's smart protocol:

| Request | Is |
| --- | --- |
| `GET /{owner}/{repo}[.git]/info/refs?service=git-upload-pack` | read |
| `POST /{owner}/{repo}[.git]/git-upload-pack` | read |
| `GET /{owner}/{repo}[.git]/info/refs?service=git-receive-pack` | read (it lists refs) |
| `POST /{owner}/{repo}[.git]/git-receive-pack` whose body is one flush packet | read (git's probe; it names no ref) |
| `POST /{owner}/{repo}[.git]/git-receive-pack` that deletes no ref | class 4: a push |
| `POST /{owner}/{repo}[.git]/git-receive-pack` that deletes a ref | class 5 |
| anything else (the dumb protocol, LFS, GitHub's web pages) | refused |

**`APIHost`**, first match:

| Request | Is |
| --- | --- |
| any method on a path in `credentialPaths` | refused |
| `POST /graphql` whose document holds only `query` operations | read |
| `POST /graphql` whose document holds any `mutation` | class 4, or 5 for a field in `destructiveMutations` |
| `POST /graphql` with a `subscription`, a body that is not one JSON object with a string `query`, a duplicate key, or a document that does not parse | refused |
| any other method on `/graphql` | refused |
| `GET` or `HEAD` of a path in `dataPaths` | class 6 |
| any other `GET` or `HEAD` | read |
| `DELETE` of a path in `destructivePaths` | class 5 |
| any other `POST`, `PUT`, `PATCH` or `DELETE` | class 4 |
| any other method | refused |

**GraphQL.** The document is parsed with `gqlparser`, without a schema. Any mutation in the
document makes the request a change, whatever `operationName` selects, so the verdict does not
depend on reading the selection as GitHub does. The summary names each mutation's top-level
fields (*GitHub: GraphQL createIssue, mergePullRequest*), each is a detail *Mutation*, and
`operationName`, when sent, is the detail *Operation*. The body shown is the request's own:
the document and its variables.

**`credentialPaths`** are the requests whose answer is a credential:

- `/app/installations/*/access_tokens`
- `/applications/*/token` and below
- `/repos/*/*/actions/runners/registration-token`, `remove-token` and `generate-jitconfig`,
  and the same three under `/orgs/*` and `/enterprises/*`

**`dataPaths`** are class 6. First, the reads whose answer is a signed link to stored data:

- `/repos/*/*/actions/runs/*/logs`, `/repos/*/*/actions/runs/*/attempts/*/logs` and
  `/repos/*/*/actions/jobs/*/logs`
- `/repos/*/*/actions/artifacts/*/*`
- `/repos/*/*/tarball` and `/repos/*/*/zipball`, and below each
- `/repos/*/*/releases/assets/*` when `Accept` names `application/octet-stream`
- `/orgs/*/migrations/*/archive` and `/user/migrations/*/archive`

Second, the metadata whose answer GitHub documents as able to hold a secret value:

- **Secret scanning alerts**, whose answer is the leaked secret: `/repos/*/*/secret-scanning/`,
  `/orgs/*/secret-scanning/` and `/enterprises/*/secret-scanning/`, and below each.
- **Webhooks**, whose `config.url` often carries a token and whose deliveries hold request
  payloads: `/repos/*/*/hooks` and `/orgs/*/hooks` and below each, `/app/hook/config` and
  `/app/hook/deliveries` and below.
- **Actions variables**, which GitHub stores and prints unmasked:
  `/repos/*/*/actions/variables`, `/orgs/*/actions/variables`,
  `/repos/*/*/environments/*/variables`, and below each.

**What is a read, and why.**

- **A repository's content is a read**, by `git`, REST or GraphQL. It is what the switch is
  turned on to read. GitHub serves it from Git's protocol, dozens of REST routes (contents,
  blobs, commits, diffs, search) and GraphQL fields, so no list names it whole, and a gate on
  some routes would be drawn as a boundary and not be one. Step 1 leaves which stored data
  asks to the cloud platform. GitHub's line of the switch's confirm says it: *Private repositories
  are read without asking.* Settings draws no switch yet, so the line goes in
  [`TODO.md`](../../TODO.md)'s *Draw each cloud platform's switch in Settings*.
- **Secrets' listings** (Actions, Dependabot, Codespaces) answer names and a public key, never
  a value.
- **Deploy keys, SSH keys and GPG keys** answer the public half.

**`destructivePaths` and `destructiveMutations`** are GitHub's class 5 list, with a push that
deletes a ref: `DELETE /repos/{owner}/{repo}`, `DELETE /repos/*/*/git/refs/` and below, and
the mutation `deleteRef`. The proxy cannot tell a push that rewrites history, or one to the
default branch, from any other update, so those are class 4. See
[*Open questions*](#open-questions), 1.

### 4. What a request shows

The request is [step 1's `CloudRequest`](1-shared-pieces.md#5-the-request), drawn by the
shared drawing. Labels and the `Unshown` sentence are constants of `githubproxy`.

**An API request that asks** shows the method, the path and query, the media type and the body
as sent, with the details [§2](#2-the-proxy-rungithubproxy) and [§3](#3-the-classifier) name.
Its summary is *GitHub: `<METHOD>` `<path>`*, or the GraphQL form.

**A push** shows:

- the summary, *GitHub: push to `<owner>/<repo>`*;
- the detail *Repository*;
- a detail per ref, labelled *Creates*, *Updates* or *Deletes*, whose value is the ref's name
  and its old and new commit ids in full;
- `Unshown`: *a Git pack of the commits pushed; an update may rewrite history*.

The proxy reads the refs from the pkt-lines that open the body, at most 64 KiB, then streams
the pack to GitHub unread once the push is approved. A push whose opening it cannot read is
refused: one past 64 KiB, with a `Content-Encoding`, a push certificate, or a ref name that is
not printable UTF-8.

**Why that is enough to approve.** A push changes refs and nothing else. The request names the
repository and every ref changed, and a commit id is a hash of its whole content, so the new id
says exactly which commit the ref will name. The user who wants to read the commits has them in
the workspace. What the request cannot say is in the sentence.

### 5. The credential (`services/githubauth`)

A service `app` builds, following
[the seam's rules for a credential](1-shared-pieces.md#7-the-cloud-platform-seam-toolsbash).
`Token(ctx)` answers the user's token or one sentinel error. `githubproxy` reaches it through
its `TokenSource` interface.

- It runs `gh auth token --hostname github.com` outside the sandbox, as the sidecar: `gh`
  resolved on the sidecar's own `PATH` (on macOS the login shell's, set process-wide by
  `setShellEnv`, `app/launchshell_darwin.go`), with no stdin, `GH_PROMPT_DISABLED=1` and a
  10-second bound passed as a parameter.
- The answer is one line matching `^[A-Za-z0-9_]{1,255}$`, else the sentinel.
- It is held in memory for five minutes, a parameter, and never written. Concurrent reads
  share one run of `gh`.
- **It is registered while held**: `safe.AddSecret` when a token is taken, before it is
  returned, and `safe.ForgetSecret` when it expires or an upstream `401` drops it.
- It runs on the first request that needs it. Only a request past the door asks, and the door
  reads the switch.
- Nothing logs the token, `gh`'s output or its stderr. A failure logs one fixed reason.

The exec is a field (`run func(ctx) ([]byte, error)`), so tests never start `gh`.

### 6. The cloud platform (`tools/bash/github.go`)

GitHub's `CloudPlatform`, built by a constructor `app/runtime.go` calls with the `githubauth`
service.

- `Target` is `permissions.GitHub`.
- `Mount` builds the grant, `runproxy.NewGrant(permissions.GitHub, run.Session, run.Asker,
  run.Refusal, limits)`, with `Challenges` set. The limits are `cloudLimits` (`tools/bash/cloud.go`). It writes the two files of
  [§1](#1-the-hosts-and-how-git-and-gh-reach-them) under `run.Dir`, and returns the two
  routes, the variables and the grant.
- `app/runtime.go` puts GitHub in the `bash.Cloud.Platforms` it hands `bash.New`. `cloudTargets`
  then gives the wire GitHub's switch and lets the context's `cloud` key name `github`, with
  no other change. A test's `Config.cloudPlatforms` stands in place of the built list, not
  beside it.

**The prompt.** `tools/bash/prompts/sandbox.md` gains GitHub's paragraph, read when the
context's `cloud` names `github`: `git` and `gh` reach `github.com` as the user through a
proxy; a read runs; a change, a push included, waits for the user each time or is refused; a
background command changes nothing; nothing that answers a credential works; logs, artifacts,
archives and release assets ask first and then need network. The sentence on tools with no
login is step 1's and does not change.

## Tasks

All start Planned. Each ends green under `make test-changed`.

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The hand checks of [*Acceptance*](#acceptance), repeated and recorded here with the tool versions | this spec | — | Planned |
| 2 | `githubauth`: the token source | `services/githubauth/` | — | Planned |
| 3 | `githubproxy`: the handler, the upstream table, the header list, the classifier, the forward, the redirect rule | `run/githubproxy/githubproxy.go`, `classify.go`, `graphql.go`, their tests | 1 | Planned |
| 4 | The push reader and the requests that ask: summaries, details, `Unshown` | `run/githubproxy/push.go`, `request.go`, their tests | 3 | Planned |
| 5 | The cloud platform: the mount, the body limit, `gitconfig`, `gh/hosts.yml`, the variables, the prompt, GitHub in `app`'s list | `tools/bash/github.go`, `proxy.go`, `prompts/sandbox.md`, `app/runtime.go`, their tests, the prompt goldens (`app/testdata/prompt/`) | 2, 4 | Planned |
| 6 | `git` and `gh` through the real sandbox | `tools/bash/github_unix_test.go`, `app/app_unix_test.go` (`startE2E`) | 5 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

1 and 2 can start together. Nothing here touches `run/sandbox`, `run/permissions`,
`services/settings`, the wire or the webview.

## Acceptance

Each is a test beside its file. GitHub is an `httptest` TLS server whose origin the proxy takes
as a parameter.

**By hand (task 1)**, with the versions recorded:

- `gh` with `GH_HOST=github.localhost` and `HTTP_PROXY` sends REST and GraphQL to
  `http://api.github.localhost` in absolute form, with `Proxy-Authorization` on the first
  request, and runs with a read-only config directory.
- `git` sends `Proxy-Authorization` after a `407`, applies each `insteadOf`, and does not
  compress a push's body.
- Which headers each tool sends, against [§2's list](#2-the-proxy-rungithubproxy).
- What each tool prints for a `403` in each refusal shape.
- GitHub's GraphQL schema serves none of `dataPaths`' data. A field that does is classed 6 by
  name before task 3 is done.

**`run/githubproxy`** (`t.Parallel()`)

- `TestARequestWithoutTheGrantsTokenIsChallenged`: none, a wrong one and an ended grant's are
  `407` with `Proxy-Authenticate`.
- `TestTheUpstreamIsFixed`: no `Host`, path, header or query sends a request anywhere but the
  two origins.
- `TestOnlyTheListedHeadersReachGitHub`: the placeholder `Authorization`, `Cookie`,
  `X-HTTP-Method-Override` and `X-GitHub-OTP` do not; `Git-Protocol` rides a fetch and never a
  push. `TestTheTokenIsAddedOutside`: the fake sees the real token in the form for its host.
- `TestEveryRequestIsClassified`: a table over both hosts, every row of §3.
- `TestAnyMutationIsAChange`: a document with a query and a mutation, whichever
  `operationName`. `TestAGraphQLBodyThatDoesNotReadIsRefused`: duplicate keys, a non-string
  `query`, a subscription, a `GET`.
- `TestACredentialPathIsRefused`, never asked. `TestADataPathAsks`: class 6, each entry, and
  the release asset only with its `Accept`.
- `TestADeletedRefIsDestructive`, by push, REST and GraphQL.
- `TestARedirectIsNotFollowed`; `TestARedirectToAnotherHostNeedsAnApprovedDataRead`.
- `TestAnAPIChangeShowsWhatIsSent`: method, path, media type, body, and the details for
  `Accept`, the API version and the GraphQL features.
- `TestAPushNamesItsRefs`: create, update and delete from recorded pkt-lines, each a detail,
  the `Unshown` sentence set; once approved the pack reaches the fake byte for byte.
- `TestAPushThatDoesNotReadIsRefused`, `TestAPushProbePasses`,
  `TestAFetchIsStreamedWhateverItsEncoding`.
- `TestOnlyAnApprovedChangeReachesGitHub`: a denial and a grant with no asker forward nothing.
- `TestNoLoginIsOneFixedMessage`, `TestARefusedTokenIsReadAgain`.

**`services/githubauth`**

- `TestTheTokenIsReadOnceAndKept`, `TestATokenExpiresFromTheCache` (the clock a parameter),
  `TestAnAnswerThatIsNotATokenIsRefused`, `TestConcurrentReadsShareOneRun`,
  `TestTheTokenIsNeverLogged`.
- `TestAHeldTokenIsBlankedAndADroppedOneIsForgotten`.

**`tools/bash`**

- `TestNoGitHubTokenIsInTheRun`: with a known token behind the source, it is in no entry of
  `Run.Env` and no file under the run directory or the tool home. This is the issue's pin.
- `TestGitHubsMountWritesItsFilesAndVariables`: both files 0600, the grant's token in
  `gitconfig` and `HTTP_PROXY`, the placeholder in `hosts.yml`.
- `TestARunWithoutGitHubHasNoneOfIt`.
- `TestGitHubsProxyVariableLeavesTheLoopbackDirect`: a run with GitHub and step 1's
  `fakePlatform` (`cloud_test.go`) on the loopback authority, setting a variable of its own.
  The run holds `HTTP_PROXY`,
  `NO_PROXY=127.0.0.1` and `no_proxy=127.0.0.1`; a client that reads them reaches the fake's
  route directly and GitHub's through the proxy; no variable is set two ways.
- `TestTheSandboxPromptSaysWhenToReadGitHub` (`bash_test.go`): the paragraph is static, and
  tells the model to read it only where the context's `cloud` names `github`.
- Through the real sandbox (`testutil.RequireSandbox`), network off, where the tool is on the
  machine: `TestASandboxedCloneReachesGitHub` clones from a fake that runs `git http-backend`;
  `TestASandboxedPushAsksAndRuns` and `TestASandboxedPushDeniedIsForbidden`;
  `TestASandboxedGhReadsTheAPI` runs `gh api user` and checks that `gh auth token` prints the
  placeholder.

**`app`** (`startE2E`), network off. As `TestASandboxedCommandReachesACloudPlatformThroughTheDoor`
does with its fake, the test sets `Config.cloudPlatforms`: GitHub's cloud platform over a fake
upstream and a known token. With `sandbox.github` turned on through
`sandboxCloudAccessSet`, a sandboxed `git ls-remote` reads unasked and `kubectl` still reads
the cluster; a push waits on one request in `sandboxActions`. The same run is repeated with
every cloud platform then built on `wip/cloud-proxies` turned on, and checks that no two set one
variable to different values.

**Integration gate:** the full checks in
[`../README.md`](../README.md#verification-commands). Then by hand on `pnpm tauri dev`, macOS
and Linux, network off and GitHub turned on through `sandboxCloudAccessSet`, since Settings
draws no switch yet: `git clone` and `git fetch` of a private repository,
`gh api user`, and a `git push` that asks.

## Security

**Widened.** With GitHub on, a sandboxed command reads what the user's token reads on
`github.com` unasked: private repositories and their content, issues, Actions metadata, the
user's profile and private email addresses. The answer reaches the model's cloud platform, and with
network on the command can send it anywhere. The sidecar holds a GitHub token in memory and
runs `gh` outside the sandbox to get it. A prompt injection in a repository's text can ask for
a change; the request is the gate.

**What holds**, beyond [step 1's](1-shared-pieces.md#security).

- The command holds no GitHub credential. The denied-always list and `neverEnv` are unchanged;
  the run's files hold a placeholder and the grant's token.
- The upstream is one of two fixed origins. No redirect is followed.
- Every change waits on the user and shows the bytes sent, or for a push the refs.
- A credential path is refused, and a signed link reaches a command only through an approved
  data read.

**Residuals**, each a **By decision** row.

- **A repository's content is read unasked**, private ones included.
- **A push's content is not shown.** The user approves the repository and the refs.
- **The token's reach is the proxy's reach.** There is no repository fence.
- **An answer's body can hold a signed link** the lists do not name, such as a private file's
  `download_url`. It is short-lived and opens one download.
- **GraphQL is classified by Kstack's parser, not GitHub's.** The any-mutation rule and the
  strict body checks keep the two from disagreeing on a change. A query that reads `dataPaths`'
  data would be unasked; task 1 checks there is none.
- **`HTTP_PROXY` hands the grant's token to every Go and Python program in the run.** It opens
  GitHub's reads alone.
- **Reads spend the user's rate limit.** The grant bounds the rate, not the hour's total.
- **`gh` on the sidecar's `PATH` runs with the user's authority**, as a kubeconfig's `exec`
  plugin does.

## When it lands

When this step is complete on `wip/cloud-proxies`:

- **ADR**, GitHub's alone: *a sandboxed command reaches GitHub over plain HTTP at `gh`'s own
  host name*. It cites [step 1's
  ADR](../../adr/2026-11-17-a-sandboxed-command-reaches-a-cloud-platform-through-a-proxy-on-its-runs-one-relay.md)
  for the proxy and the relay, and records `HTTP_PROXY` over a `gh` wrapper on `PATH`, no repository
  allowlist, a repository's content as a read, redirects not followed, and the token from `gh auth
  token` alone.
- **`docs/security/<date>-a-sandboxed-command-reaches-github.md`**, from *Security* above, with
  task 1's hand checks and tool versions, naming the commit.
- **`docs/security-model.md`**:
  - The **Not built** row on the four cloud platforms is narrowed to the cloud platforms not yet
    built, or removed with the last. No new row of that kind is added.
  - *Assets* gains the GitHub token, held in memory. *Boundaries* gains sidecar → GitHub.
  - New **Enforced** rows: no GitHub token in the run (`TestNoGitHubTokenIsInTheRun`); the
    upstream is fixed (`TestTheUpstreamIsFixed`); a credential path is refused
    (`TestACredentialPathIsRefused`); a signed link needs an approved data read
    (`TestARedirectToAnotherHostNeedsAnApprovedDataRead`).
  - New **By decision** rows for the residuals above, linking the ADR.
- **`docs/TODO.md`**: *Let a sandboxed command reach the user's cloud accounts without holding
  a credential* is narrowed the same way. *Draw each cloud platform's switch in Settings* gains
  GitHub's line of the confirm. New lines for what the open questions defer.
- **`CLAUDE.md`**: `sidecar/internal/run/CLAUDE.md` (a `githubproxy` section: the hosts, the
  header list, the classifier's lists, the push reader); `sidecar/internal/tools/CLAUDE.md`
  (`github.go`, the two files, the variables, the body limit); `sidecar/CLAUDE.md`
  (`services/githubauth`).
  Raise a budget in `.github/ci-config/claude-md-budgets.yml` only with the reason in the
  commit body.
- This file is deleted and its row leaves [`README.md`](README.md).

## Open questions

1. **Is deletion the right class 5 list?** Recommended: yes, a deleted ref or repository
   alone. Drawing a push to the default branch as destructive needs a request to GitHub to
   learn which branch that is, and a rewritten history cannot be told from the push's opening.
2. **Should the proxy follow GitHub's own redirects?** Following a link to a short list of
   GitHub's download hosts, from the sidecar, would make `gh run view --log` and
   `gh release download` work with network off and keep the signed link out of the sandbox. It
   also makes the proxy a path to those hosts. Recommended: not in this step.
3. **Is `HTTP_PROXY` acceptable** at [§1's cost](#1-the-hosts-and-how-git-and-gh-reach-them)?
   The alternative is a `gh` wrapper on `PATH`, a Kstack-written folder on a `PATH` that is
   frozen and filtered by design. Recommended: `HTTP_PROXY`.
4. **Is `gh auth token` enough**, or should a user with no `gh` be served through
   `git credential fill`? That runs the user's credential helper outside the sandbox.
   Recommended: `gh` alone.
5. **Git LFS.** Its batch request is a `POST` that only reads, and its objects live on another
   host. Left out; is it needed for the first version?
