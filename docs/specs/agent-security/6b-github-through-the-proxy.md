---
title: GitHub and git through the proxy
scope: sidecar, webview
status: Planned
---

# GitHub and git through the proxy

**Needs:** step 5D, whose termination this step's injector runs under; step 1D, whose
`credentials.GitHub` borrows the token; and step 4B, whose request the prompt is drawn through.
**Unblocks:** step 7B, whose GitHub connection replaces the borrow behind the same injector.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today `gh` and `git` over HTTPS reach GitHub through a tunnel (step 4C) and fail there: the
sandbox holds no token, and the tunnel can add none. The proxy can terminate TLS (step 5D) but
has no injector to do it for.

After this step, on macOS and Linux:

- **A GitHub injector** (`egress/github.go`) claims `api.github.com`, `github.com` and
  `uploads.github.com`, and puts the user's token on each request there — borrowed on the host
  through `credentials.GitHub` (step 1D), so the sandbox never contains a real token. `gh` runs
  with step 5D's `Placeholder` as `GH_TOKEN`, which the injector replaces.
- **GitHub calls are classified**: a `GET` or `HEAD` is class 1, any other method class 4, and a
  curated list class 5. A `POST /graphql` is read by its document: queries alone are class 1,
  anything else class 4 or 5. A `git push` is class 4 naming the refs it updates, and a push that
  deletes a ref is class 5. `Decide` and step 4B's prompt do the rest: `gh pr list` works in
  the sandbox, and `gh pr create` asks.

Commands outside the sandbox are unchanged: `gh` and `git` run there as the user, with the
user's stores. Nothing changes on Windows.

## What is not in this step

- **No CA, no termination, no trust file.** Step 5D's; this step registers one injector with it.
- **No status, expiry notice or re-login.** An expired token is a `403` the command reads
  (step 5D §1's line); step 2D draws it.
- **No GitHub Enterprise Server** (Decisions), and **no OAuth**: step 7B.
- **No Google Cloud or Azure**: step 6C, built beside this step over the same termination.

## Design

### 1. The credential

`credentials.GitHub(ctx, "github.com")` (step 1D §2): `gh auth token --hostname github.com` on
the host, cached for an hour, `ErrExpired` when `gh` says it is logged out, `ErrNoCLI` with no
`gh`. A `401` from GitHub on an injected request calls `Store.MarkExpired(Key{GitHub, ""}, "github.com", token)`, with the token it injected,
through the injector's `Unauthorized`, so the next request borrows again and step 2D's status
says `expired` meanwhile. The `gh` manual says the command prints the account's token on the
given host, so nothing else is read.

### 2. The injector

`egress/github.go`, `NewGitHub(creds Source) *GitHub`, where `Source` is the one method
`GitHub(ctx, host) (string, error)` and `credentials.Store` is the one. `tools/bash/proxy.go`
registers it in the run's `Handler.Injectors` under its three hosts, only while
`egressStatus.terminating` holds (step 5D §3). It edits `Authorization` alone, by step 5D's
rule — the placeholder replaced, a missing header added, any other kept:

| Request | Header set |
| --- | --- |
| `api.github.com`, `uploads.github.com` | `Authorization: Bearer <token>` |
| `github.com`, a git path (`/{o}/{r}.git/info/refs`, `…/git-upload-pack`, `…/git-receive-pack`, with or without `.git`) | `Authorization: Basic base64("x-access-token:<token>")` |
| `github.com`, any other path | none; forwarded as it came |

`gh` sends the placeholder as `token kstack-placeholder` or `Bearer kstack-placeholder`; both
are replaced. `git` sends nothing on its first request and, with no `401` coming back for a
private repository, never asks for a name. `objects.githubusercontent.com` and
`codeload.github.com` stay step 4C's tunnels: what `gh` fetches there is signed into the URL.

A refusal implements step 5D's `Refuser`: on `api.github.com` and `uploads.github.com` a JSON
body, `{"message": "kstack: …"}`, which `gh` prints; on `github.com` one line of text, which
`git` prints.

**The run's environment** (step 2A's table) gains, while `terminating` holds:

| Variable | Value |
| --- | --- |
| `GH_TOKEN` | `kstack-placeholder` (`egress.Placeholder`) |
| `GIT_TERMINAL_PROMPT` | `0`, so a `git` that meets a `401` fails instead of waiting on a terminal it has not got |

`gh` needs no store in the sandbox: with `GH_TOKEN` set it reads no `hosts.yml` and asks no one
to log in (the manual: *an authentication token that will be used when a command targets
`github.com`*). The never-list's `GH_TOKEN` row (step 2A) becomes "never the user's", its test
checking the value.

### 3. The classifier

`GitHub.Classify`, over the method, the path and, for a push, the body's head:

| Request | Class |
| --- | --- |
| `GET`, `HEAD`; a `POST` to `…/git-upload-pack` (a fetch or clone) | 1 |
| `POST api.github.com/graphql` whose document holds queries alone | 1 |
| any other method | 4 |
| `POST api.github.com/graphql` with a mutation naming `deleteRef`, `createBranchProtectionRule`, `updateBranchProtectionRule` or `deleteBranchProtectionRule` | 5 |
| `DELETE /repos/{o}/{r}` | 5 |
| `DELETE /repos/{o}/{r}/git/refs/*` | 5 |
| `PUT` or `DELETE` on `/repos/{o}/{r}/branches/{b}/protection` and anything under it | 5 |
| `POST /repos/{o}/{r}/transfer` | 5 |
| `DELETE /orgs/{o}` and anything under it | 5 |
| `POST …/git-receive-pack` | 4 per ref it updates; 5 when a command's new id is all zeros |

**A push's commands** are pkt-lines at the body's start (`egress/pktline.go`), `<old-id>
<new-id> <ref>`, the first with capabilities after a NUL, ended by a flush packet. `HeadBytes`
is 64 KiB: `Classify` reads the commands up to the flush and refuses a body whose commands do
not end inside the head. A force-push cannot be told from a push without the repository's
history (Decisions).

**A GraphQL call** is a JSON body, `{query, variables, operationName}`, read up to the same
64 KiB head. `Classify` parses `query` with `gqlparser`'s parser, the one the sidecar's schema
already uses, and reads each operation's type. Class 1 needs all of: the body ends inside the
head, is one JSON object, carries a `query` string that parses, and every operation in it is a
`query`. Anything else is class 4, or 5 by the row above: a mutation, a subscription, a
document with both kinds whatever `operationName` picks, a batch array, a persisted query (an
`id` or `extensions.persistedQuery` in place of `query`), or a body that does not parse.

`Scope{Org, Repo}` comes from the route: `/repos/{o}/{r}`, `/orgs/{o}`, or the git path's
`{o}/{r}`; a GraphQL call has none, since its variables name nodes by id. `Verb` is the
method, `Kind` the route with its parameters replaced, `Name` the ref
or the resource's name. The `Summary` is one line: *Create a pull request in
`kstackhq/kstack`* for the routes a table in `github.go` names (pulls, issues,
comments, releases, refs, contents, workflow dispatches, repos), *Push `refs/heads/fix-api` to
`kstackhq/kstack`* (more refs as *and 2 more*), *Delete `refs/heads/old` in …*, *GraphQL
mutation `createPullRequest`* naming the top-level fields (more as *and 2 more*), else
*`POST` `/repos/…`*. Every string in it came from the command and is drawn through
`VisibleText`.

**Streaming.** A clone's `git-upload-pack` body and a push's packfile are large. `Classify`
reads the head it asked for and nothing more; step 5D's forward sends the head, then streams
the rest once `Decide` allows.

### 4. The request

Step 4B's, as an AWS request's (step 5C §3): the summary as heading, then *Show the request*
folding the method, the path with its query, the media type and the body up to the head through
`VisibleText` — a push shows its refs in the summary alone, its packfile never. The
`ApprovalRequest`'s `aria-label` reads *GitHub action awaiting approval*, keyed on the action's
provider `github`. The call's disclosure lists each hop as it lists a cluster write, tagged by
its decision.

### 5. The prompt

`prompts/sandbox.md` gains one paragraph: `gh` and `git` over HTTPS work in the sandbox as the
user, through Kstack's proxy; a read runs as it is, and a push or any other change to GitHub
waits for the user or comes back `403` naming why; `git` over SSH does not work; GitHub
Enterprise Server does not yet. On a Mac whose CA is not trusted it says instead that `gh` and
`git` do not yet work in the sandbox, and that the user can turn them on in Settings.

## Decisions this step asks for

1. **A force-push is a push.** The note lists force-push under class 5, but the proxy sees the
   old and new ids and not whether one descends from the other, which needs the repository's
   history. So every push asks as class 4 naming its refs, a ref deletion is class 5, and the
   record says a force-push is covered by the push's prompt. Recommended.
2. **GitHub Enterprise Server is not in this step.** `GH_HOST` needs a host list per enterprise
   and a token per host; the note leaves its design open. Recommended.
3. **A command that spells the placeholder gets what `gh` gets**, classified the same; a token
   the model invents fails at GitHub. This is step 5D's rule applied; recommended.
4. **A GraphQL call is classified by parsing its document.** `gh pr list`, `gh issue list` and
   most `gh` reads are a `POST /graphql`, so reading every `POST` as a write makes them ask and
   fails them under `ReadOnly`. Matching `gh`'s own query names would break on any `gh` release
   and pass a query the model writes by hand. So the proxy parses the document: queries alone
   are a read, and every form it cannot read whole is class 4. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The injector: hosts, the header rule, the refusal body, `Unauthorized` | `egress/github.go`, its test | — | Planned |
| 2 | The classifier, the pkt-line reader and the GraphQL reader | `egress/github.go`, `egress/pktline.go`, `egress/githubgraphql.go`, their tests, `egress/testdata/` | 1 | Planned |
| 3 | The run: registration under `terminating`, `GH_TOKEN`, `GIT_TERMINAL_PROMPT` | `tools/bash/proxy.go`, `tools/bash/env.go`, their tests | 1 | Planned |
| 4 | The `aria-label` and the disclosure line | `src/lib/chats.tsx`, `chat-transcript.tsx`, their tests | 2 | Planned |
| 5 | The prompt; `gh api user` end to end | `tools/bash/prompts/sandbox.md`, `app/e2e_unix_test.go` | 2, 3 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1, then 2 and 3 at the same time, then 4 and 5 at the same time, then 6.

## Tests

**`egress`**

- `TestAGitHubRequestIsInjected`: through a run's socket with a fake `api.github.com` behind it,
  a client sending `Authorization: token kstack-placeholder` reaches the fake with `Bearer
  <token>`, and the placeholder never does; no header gets one; another is kept; `info/refs`
  and `git-receive-pack` on `github.com` carry `x-access-token:<token>` and a page path
  nothing; `codeload.github.com` is tunnelled.
- `TestEveryGitHubRequestIsClassified`: a table over §3's rows, including a receive-pack fixture
  (`testdata/receive-pack-*.bin`) with one update, one create, one delete, and one whose
  commands do not end within 64 KiB, which is refused; a two-ref push's `Summary` and `Name`.
- `TestAGraphQLCallIsClassifiedByItsDocument`: a document of queries alone, and two named
  queries, are class 1; a mutation is class 4 with its fields in the `Summary`; `deleteRef` is
  class 5; a document with a query and a mutation is class 4 whichever `operationName` names;
  a subscription, a batch array, a persisted query with no `query`, a document that does not
  parse, and a body past 64 KiB are each class 4. Under `ReadOnly` the queries pass and the
  rest are refused.
- `TestAGitHubRefusalIsShapedForItsTool`: under `ReadOnly` a `POST` is a 403 with `gh`'s JSON
  body on `api.github.com` and a text line on `github.com`, each naming the mode.
- `TestAPushStreamsOnceAllowed`: a 10 MiB packfile is forwarded whole after the head, and an
  upload-pack streams before its body ends.
- `TestA401MarksTheTokenExpired`: a fake answering 401 to an injected request makes the store's
  status `expired`, and the next request borrows again.

**`bash`**: `TestTheRunCarriesTheGitHubPlaceholder`: `GH_TOKEN` is the placeholder and
`GIT_TERMINAL_PROMPT` is `0` only while `egressStatus.terminating` holds, and a real `GH_TOKEN`
in the sidecar's environment never reaches the run. `TestNoCredentialIsWrittenToDisk` (step 1D)
grows a case: after a borrow and a terminated request through a real run, the token's bytes are
under no Kstack directory.

**`app`** (`e2e_unix_test.go`, CI's Linux job): `TestGhApiUserRunsInTheSandbox`: with `gh` on
`PATH`, a fake `api.github.com` behind the proxy and a fake host `gh auth token`, a chat's `gh
api user` prints the fake's answer, the fake saw the borrowed token, and the call's disclosure
tags the request `allowed`.

**Webview** (`chat-transcript.test.tsx`): the GitHub request draws its summary, its
`aria-label`, and *Show the request* with the method, path and head as text, and the
disclosure's line with its tag.

## Security

**Widened.** The proxy reads the plaintext of every request to the three GitHub hosts, and a
sandboxed command can read the user's private repositories through the user's token, on the
model's word, unasked — what the note accepts for cluster reads, with the same residual: what it
reads can leave through the answer.

**Narrowed.** A change to GitHub is a classified action the user's mode and rules decide, and a
ref, repository or organization deletion, a branch protection change and a transfer ask in every
mode. Before this step `gh` outside the sandbox could do anything the user approved as a shell
string.

**Residuals.** A force-push asks as a push (Decisions 1); a command that spells the placeholder
gets the token's reads (Decisions 3); `uploads.github.com` takes what the model uploads under
the user's name, asked as a class 4 write. A GraphQL write has no repository in its scope, so
a rule scoped to one repository never matches it, and it asks; its class 5 list names four
mutations and is a list.

The record, `docs/security/<date>-github-through-the-proxy.md`, is short and points at step
5D's for the mechanism.

## When it lands

- **The security record** above; the ADR step 5D writes gains a line: a force-push is a push.
- **`security-model.md`**: rows for the GitHub injection (with the placeholder test) and the
  classifier, and the no-credential-on-disk row gains this step's case.
- **`sidecar/CLAUDE.md`**: the GitHub injector and classifier, the pkt-line and GraphQL
  readers, the run's
  two variables. **Root `CLAUDE.md`**, *Chat*: the GitHub request and its `aria-label`. **The
  note's *Where this meets the code***: the force-push decision.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with `egress`'s tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on Linux with `gh` logged in: ask for `gh api user`, and read your
login with no request; `gh pr list` in a read-only chat, and read the list with no request;
`git push` of a branch in an HTTPS clone, and read the request *Push
`refs/heads/…` to `…`*, then the push once approved; `git push origin --delete <branch>` asks
whatever the mode; `env | grep GH_` prints the placeholder and nothing of yours. On macOS, click
*Trust it* in Settings first (step 5D), then repeat.
