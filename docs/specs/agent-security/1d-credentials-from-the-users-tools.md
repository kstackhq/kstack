---
title: Credentials from the user's tools
scope: sidecar
status: Landed
---

# Credentials from the user's tools

**Needs:** nothing beyond `main`. It is host-side code that runs no proxy. **Unblocks:** step
2D, which draws what this step finds and knows; steps 5C, 6B and 6C, whose proxies borrow
through it; and step 7B, whose connections sit beside the borrows.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the sidecar borrows one credential from the user's tools: the kubeconfig's, whose `exec`
plugins the connection pool (`clustersvc/internal/kubeconn`) runs on the host. Nothing asks
`aws`, `gh`, `gcloud` or `az` for what they hold, and the sandbox's closed home (step 2A) keeps
their stores out of every command's reach.

After this step there is one package, **`credentials`**, that does what [the note](../../notes/sandbox-credentials-and-permissions.md)'s
*Credential sources* section asks: it obtains a credential the way the tool would, by running
the tool's own command on the host, and keeps it in memory until it expires.

- **`Store`** has one borrow per provider — `AWS`, `GitHub`, `Google`, `Azure` — and, for AWS,
  `Region`. An AWS credential carries its own account, read with its own keys. Each runs the tool once, caches the answer to the expiry it reports or
  a default TTL, and runs one borrow per key at a time.
- **`Discover`** asks each tool what it holds, reading identities and never a secret, and the
  store keeps its last answer (`Found()`).
- **`State(key)`** is an in-memory verdict per identity — `valid`, `expired`, `missing` — written
  by the borrows, by `Discover` where it proves something, and by a proxy whose upstream refused
  the credential, and served whole by `Subscribe`.
- **Nothing is written to disk.** The note's eleventh invariant gets its test here, with a case
  per provider.

Nothing calls the store in this step: `app` builds it, and steps 2D, 5C, 6B and 6C take it by
field. The user sees no change. Nothing changes on Windows: the package builds there and nothing
borrows, since no proxy runs.

## What is not in this step

- **No proxy.** Step 5C re-signs AWS requests, step 5D terminates TLS, and steps 6B and 6C
  inject what this step borrows.
- **No status on screen, no notice, no re-login, no exclusion list.** Step 2D draws the status,
  runs the tool's login command on a click, and adds `Settings.Credentials.Excluded`, passing
  it as the `excluded` argument this step leaves nil (§1).
- **No OAuth.** Step 7B keeps app tokens in the keyring, read by this package alone.
- **No kubeconfig borrow.** The connection pool keeps running `exec` plugins as it does today.

## Design

### 1. The package

`credentials/`, a leaf importing `safe`, `gochan/watch` for its gauge, `golang.org/x/sync`'s
`singleflight`, and the standard library, and nothing else of ours. What it needs from elsewhere
— the kube contexts, the exclusion list — it takes as a function, so no later step's package
has to import it back.

```go
// Provider names one tool's credential. The strings are the identity id's
// first part, the one spelling Settings and the wire use (step 2D), and the
// same as permissions.Provider's (step 3B).
type Provider string

const (
	AWS    Provider = "aws"
	GitHub Provider = "github"
	Google Provider = "gcp"
	Azure  Provider = "azure"
)

// Key names one identity: the provider and, for AWS, the profile. String is
// "aws:dev", or the provider alone ("github", and "aws" for the bare key a tool
// that is missing or signed into nothing is reported under).
type Key struct {
	Provider Provider
	Name     string
}

// Binaries is where each tool is, "" for one not found. The caller decides.
type Binaries struct{ AWS, GH, Gcloud, Az string }

// Store borrows credentials from the user's tools and keeps them in memory.
type Store struct {
	// unexported: the binaries, the contexts, the exclusion test, the runner,
	// the clock, the timeout, the caches, the status table, the last Found,
	// the subscribers
}

// NewStore borrows from the tools b names, running each in home. contexts
// is the kube context names, listed by Discover. excluded is step 2D's list,
// nil excluding nothing; it is read on every borrow and must be safe for
// concurrent use.
func NewStore(b Binaries, home string, contexts func() []string, excluded func(Key) bool) *Store

// Close stops every tool run in flight and makes every later borrow answer
// ErrClosed. app calls it on shutdown.
func (s *Store) Close()
```

`NewStore` calls `newStoreWithOptions(b, home, contexts, excluded, opts ...option)`, the
sidecar's idiom for a test seam (`poke.newWithOptions`): the clock, the per-borrow bound
(`borrowTimeout`, 30 s), the backoff after an expiry (`expiredBackoff`, 30 s, §2) and the runner
are production values there, and the tests pass `withClock`, `withTimeout`, `withBackoff` and
`withRunner`. `excluded` is an argument, not a field set later, so no borrow can race a write of
it; in this step `app` passes nil, and step 2D passes a read of its list. `home` is
`os.UserHomeDir()`'s, read by `app`. An empty `home` makes the store treat every binary as not
found, so every borrow answers `ErrNoCLI` and `Discover` reports each tool not installed: a
tool run with no home finds no config, and Bash is not offered without one either.

`app/app.go` builds one store at start and keeps it on `App` as `creds`: `App.Close` closes it
after the servers stop, the package's tests reach it there, and step 2D's resolvers are handed
it from there. It fills `Binaries` with `exec.LookPath` on the sidecar's
own `PATH`: on macOS the one `main` imported from the user's login shell (`internal/loginshell`),
so the store runs the `aws` the user's terminal runs; on Linux the one the desktop session
started the app with. `loginshell`'s allowlist gains `GH_CONFIG_DIR`, beside the AWS, `gcloud`
and Azure config variables it imports today, so a `gh` whose config the user moved is found on
macOS as it is in their terminal. `contexts` is a
function in `app` over `kubeconfig.Service.Get`, answering the context names sorted and none
before the kubeconfig is read. An empty binary makes every borrow of that provider `ErrNoCLI`.

Every tool runs on the host, outside every sandbox, with the sidecar's own environment, stdin
the null device, `home` as its working directory, bounded by `borrowTimeout`, in a process
group of its own (a kill-on-close job object on Windows) killed whole when the run is stopped and
again once the tool is reaped. On Windows the tool starts suspended and is resumed once it is in
the job. `Close` waits for every run it cancels to be reaped. On Windows a batch wrapper (`gcloud.cmd`, `az.cmd`) runs through
the system's `cmd.exe`, each argument double-quoted and one holding `"`, `%` or a line break
refused. The
sidecar's own working directory can be one of Kstack's, and a tool that caches beside where it
runs would write there. Every run's context descends from one the store owns, which `Close`
cancels, so no run outlives the sidecar's shutdown. The run goes through one unexported seam,
`run(ctx, env, bin, args...) (stdout, stderr []byte, exit int, err error)`, which the tests
replace; `env` is nil for the sidecar's own environment, and set only for the account read (§2). A
non-zero exit is an error naming the provider and the exit code — *gcloud exited 1* — and
**never the output**; the log line says the same, at info. `stderr` is read for the expiry
table (§3) and then dropped.

### 2. The borrows

| Method | Command | Cached |
| --- | --- | --- |
| `AWS(ctx, profile) (AWSCredential, error)` | `aws configure export-credentials --profile <p> --format process`, then `aws sts get-caller-identity --output json` with those keys, its `Account` | per profile, to `Expiration` less a minute; `defaultTTL` (15 min) for one with none |
| `Region(ctx, profile) (string, error)` | `aws configure get region --profile <p>`; `""` on exit 1 | per profile, `defaultTTL` |
| `GitHub(ctx, host) (string, error)` | `gh auth token --hostname <host>` | per host, `githubTTL` (1 h): a GitHub token reports no expiry, and a re-borrow is how a logout is seen |
| `Google(ctx) (Token, error)` | `gcloud config config-helper --min-expiry=15m --format=json`, its `credential.access_token` | one, to `credential.token_expiry` less a minute: `--min-expiry` makes `gcloud` refresh a cached token with less than 15 minutes left |
| `Azure(ctx, resource) (Token, error)` | `az account get-access-token --resource <r> --output json` | per resource, to `expires_on` (a POSIX time, from azure-cli 2.54) less a minute; `defaultTTL` when an older CLI prints only the local-time `expiresOn` |

`export-credentials` is in the AWS CLI from 2.9; an older CLI refuses the subcommand, which is an
ordinary failed borrow. Static keys print no `SessionToken` or `Expiration`.

**The account is the identity of the keys the borrow answered**, never of the profile resolved a
second time: a profile's config, a `credential_process` or an SSO role can answer another account
on another run, and the account is what step 5C scopes a request's permissions by. So the AWS
borrow reads it inside the same run, with the credential itself. `sts get-caller-identity` runs
with the sidecar's environment less every variable that names an identity (`AWS_PROFILE`,
`AWS_DEFAULT_PROFILE`, the key, secret, session token and expiration variables, `AWS_REGION`),
plus the credential's `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN`, and
with no `--profile`. The CLI reads keys in the environment first, and with no profile named
nothing makes it skip them. The profile's `Region`, when it has one, is set as `AWS_REGION`: it
picks the endpoint and names no identity. The account rides on the credential
(`AWSCredential.Account`), cached with it and read again with every new credential, so a caller
never pairs an account with keys from another borrow. An expiry line on `sts`'s stderr is
`ErrExpired` and `Expired` as the export's would be; a failure that leaves no account fails the
borrow, so no credential is served without one. An export that fails never reaches `sts`.
`Region` reads the config file alone: it has no credential and no expiry, and is cached for
`defaultTTL`. `MarkExpired` for a profile drops it beside the credential (§3).

```go
// AWSCredential is one borrowed credential: the process-credentials JSON's fields,
// and the account those keys belong to.
type AWSCredential struct {
	AccessKeyID     string    `json:"AccessKeyId"`
	SecretAccessKey string    `json:"SecretAccessKey"`
	SessionToken    string    `json:"SessionToken"`
	Expires         time.Time `json:"Expiration"`
	Account         string    `json:"-"`
}

// Token is one bearer token and when it stops working.
type Token struct {
	Value   string
	Expires time.Time
}
```

A borrow answers the cache while it holds; past the expiry it runs the tool again. **An expiry is
remembered for `expiredBackoff`**, per cache key: a borrow that answered `ErrExpired` answers it
again, running nothing and writing no status, for 30 s, so a proxy meeting an expired SSO session
does not start `aws` on every request. `Recheck` (§3) runs past it, and a `Discover` that proves
a login works clears it (§4). **One borrow per
key at a time**: a second caller for the same key waits on the first's answer rather than running
the tool twice (`singleflight.Group.DoChan`, keyed by the cache key). **A cache key names the
method and its argument** — `aws:dev`, `region:dev`, `github:github.com`, `gcp`,
`azure:<resource>` — so `AWS` and `Region` for one profile never share a run or an answer. The shared run is bounded by `borrowTimeout` on a
context of its own, descending from the store's and never the first caller's, so a caller that
gives up ends its own wait and leaves the run to the others; each waiter returns on its own
`ctx`. The shared function stores its answer in the cache, and writes the status, before it
returns, so a caller arriving as the flight ends finds the cache filled and runs nothing.
Different keys run at once. Every secret string — the secret access key, the session token, each bearer token — goes through `safe`
the moment it is read, so no rendered log line can hold it. A borrow re-runs for as long as the
sidecar runs, and `safe.AddSecret` only appends, so `safe` gains `SetSecrets(slot string, values
...string)`, which replaces what one slot held; the store's slot is the cache key, so the registry
holds each identity's current credential and no more. A replaced value has expired or been
dropped. A refused one is different: the tool may still hold it and hand it to something else,
so `MarkExpired` moves it into a slot of its own, `refused:<cache key>`, which keeps the last 16
distinct refused values beside the hashes (§3) and is never cleared while the sidecar runs.
`safe`'s registry is one slice today, so `SetSecrets` keys it by slot, with `AddSecret`'s values
in a slot of their own; a value under sixteen bytes registers nothing, as with `AddSecret`.
`ResetSecrets`, which exists for tests, clears every slot, the refused ones included.
`blankSecrets` reads the flattened slice outside the lock, so `SetSecrets` builds a new slice and
swaps it in, never changing one a reader may hold.
An AWS borrow registers the secret key and session token under `read:<cache key>` as soon as it
decodes them, before `sts` runs with them, so a failure that echoes them is blanked whatever the
borrow comes to; a slot of its own leaves the cached credential's registered.
The access key id, the region and the account are not secrets and are not registered.

**The identity** a borrow writes status for (§3) is `Key{AWS, profile}`, `Key{GitHub, ""}`,
`Key{Google, ""}` or `Key{Azure, ""}`: GitHub's host and Azure's resource are the cache's key,
not the identity's, since there is one GitHub login and one Azure login across its resources.
Step 6B borrows for `github.com` alone (GitHub Enterprise Server is out of its scope), so the one
GitHub status is `github.com`'s.

Every borrow — `AWS`, `Region`, `GitHub`, `Google`, `Azure` — consults
`excluded(identity)` first, with the identity above, never the cache key, and answers
`ErrExcluded` for a true, running nothing and writing no status: which status an excluded
identity shows is step 2D's.

### 3. Status

`State(key) State` reads one in-memory table; `MarkExpired(key, arg, sent)` writes it from outside.

```go
type Status string

const (
	Valid   Status = "valid"   // borrowed, or discovered and not known to have expired
	Expired Status = "expired" // the tool said so, or a proxy met a 401
	Missing Status = "missing" // no binary, or signed into nothing
)

// State is one identity's verdict: when it was entered, and the tool's one
// line through safe.String, "" for Valid.
type State struct {
	Key    Key
	Status Status
	Since  time.Time
	Detail string
}

var (
	ErrExpired  = errors.New("credentials: expired")
	ErrNoCLI    = errors.New("credentials: tool not installed")
	ErrExcluded = errors.New("credentials: excluded") // answered only through excluded
	ErrClosed   = errors.New("credentials: closed")
)
```

A borrow that succeeds sets `Valid` unless another of the identity's cache keys holds a standing refusal (below), and for AWS removes `Key{AWS, ""}`'s state, which a profile's borrow has just proved stale. One whose tool exits non-zero with an expiry line on stderr
answers `ErrExpired` and sets `Expired` with that line as `Detail`. **The expiry lines live in
this package**, one table per provider beside the command that produces them
(`credentials/aws.go`, `github.go`, `google.go`, `azure.go`), each with a table test; step 2D
draws the result and adds no line:

| Provider | An exit with one of these on stderr is `ErrExpired` |
| --- | --- |
| `aws` | *The SSO session associated with this profile has expired or is otherwise invalid*, *Error loading SSO Token*, *Error when retrieving token from sso*, `ExpiredToken` |
| `github` | `gh auth token` exiting non-zero with *no oauth token* on stderr (it prints *no oauth token found for github.com*). A GitHub token has no expiry; a logout is what ends it, and `gh auth login` renews it as a login command renews an SSO session, so a borrow that finds it gone is `Expired` and gets step 2D's notice. Discovery says `Missing` for the same state (§4): it has not seen a borrow fail |
| `gcloud` | *Reauthentication required*, *Reauthentication failed*, `invalid_grant` |
| `azure` | `AADSTS700082`, `AADSTS70008`, `AADSTS50173`, *Interactive authentication is needed*, *Please run 'az login'*. A code matches as a whole word, so `AADSTS70008` is not also a match for `AADSTS700084` |

*Error loading SSO Token* also means a profile that was never signed in. It is `Expired` all the
same: the fix is the same `aws sso login`, which step 2D's notice offers. A line off the table is
an ordinary error and leaves the status alone. `ErrNoCLI` sets `Missing` under the provider's
bare key (`Key{AWS, ""}`), the key `Discover` reports a missing tool under, so one fact has one
key.

**`MarkExpired(key Key, arg, sent string)`** is for a proxy whose upstream refused a credential
it injected: a `401` from GitHub, Google or Azure (steps 6B and 6C), or from AWS any `4xx` whose
error code is `ExpiredToken` or `ExpiredTokenException` (step 5C) — STS and IAM answer `403`, S3
and the JSON-protocol services `400`. `arg` is what the proxy borrowed with: the AWS profile, the
GitHub host, the Azure resource, or `""` for Google. `sent` is the secret the proxy used: the
bearer token, or the AWS secret access key. It drops every cache entry the identity holds —
every host or resource entry, and a profile's `Region` beside its credential — and
sets `Expired` with `Detail` *the provider refused the credential*, so the next borrow runs the
tool and the status is on screen meanwhile.

**A refused credential stays refused.** `gh auth token` and `gcloud` hand back the token they
hold, so after a server-side revocation the tool answers the same token again, and a store that
took it as `Valid` would flap: borrow, `401`, `MarkExpired`, borrow, on every request. So
`MarkExpired` adds a SHA-256 of `sent` to the refused set of the cache key `arg` names, in
memory, and marks that refusal **standing**. It hashes what the proxy sent, never what the cache
holds: by the time a `401` lands the cache may hold a newer credential, which was not refused.

- A borrow whose answer hashes to a member of its cache key's set is `ErrExpired`: nothing is
  cached, the refusal is standing, and the status is `Expired` — again, when a credential that
  worked came in between.
- Any other answer clears that cache key's standing refusal and is cached. The identity is set
  `Valid` only when none of its cache keys has a standing refusal. So a fresh Graph token never
  reads as a renewed Azure login while the ARM token the provider refused is still what `az`
  hands back, and the status does not flip between the two.
- The set keeps the last 16 distinct hashes per cache key, since a refreshed credential never
  hashes the same; a value refused again moves to the newest place, so repeated reports of one
  refusal cannot push another out.

The check runs under the lock when the answer is stored, so a borrow already in flight when
`MarkExpired` lands, returning the refused credential, is refused too, and cannot write it
back. The fix is the tool's login command, which step 2D offers.

**`Recheck(ctx, key) State`** borrows the identity again past the cache and past
`expiredBackoff`, once for every cache key it holds a standing refusal for, or, when it holds
none, once for every cache key of the identity borrowed since start (for AWS the profile's
credential alone), and answers the resulting state. For an identity never borrowed it borrows
its one credential (`AWS`, `GitHub` for `github.com`, `Google`); an Azure login never borrowed
has no resource to name, so its state is answered as it stands. Step 2D calls it after a renewal exits 0, in
place of a borrow of its own.

`Subscribe() *watch.Receiver[[]State]` is a gauge, as `cloud/prefs`' is: the whole table on
subscribe and again on every change, which step 2D's `credentialsWatch` serves and step 6D's
monitor reads. A delivery is one slice shared by every receiver, so a receiver treats it as
read-only.

### 4. Discovery

`Discover(ctx) Found` runs every tool at once, each under a 15 s bound, and reads identities:

| Provider | Command | Read |
| --- | --- | --- |
| `github` | `gh auth status --hostname github.com` | signed in when it exits 0. `gh` checks the token with GitHub, so exit 0 proves it works. The user is the active account: since `gh` 2.40 a host can hold several, each a *Logged in to github.com account <user>* line (stdout) followed by *Active account: true* or *false*, and the one marked `true` is read; an older `gh` prints one *Logged in to github.com as <user>* (stderr). Blank when neither is there. Signed into nothing when the output holds *You are not logged into any GitHub hosts*; any other non-zero exit — a token GitHub refused, no network — proves nothing |
| `aws` | `aws configure list-profiles` | one identity per profile; no credential is read. Exit 0 with no profiles is signed into nothing |
| `gcloud` | `gcloud config get-value account` and `project` | the account and the project; an empty account, which `gcloud` answers with exit 0 and `(unset)` on stderr, is signed into nothing |
| `azure` | `az account show --output json` | the subscription's name and id, the tenant, the user; a non-zero exit with *Please run 'az login'* is signed into nothing |
| Kubernetes | the `contexts` function | the context names, listed for the screen alone |

`Found` is what the tools hold without their secrets:

```go
// Found is what Discover read, never a secret.
type Found struct {
	Tools    []Tool   // one per Provider, in the order of the constants
	Contexts []string
}

// Tool is one provider's tool as Discover found it.
type Tool struct {
	Provider   Provider
	Installed  bool // its binary is named
	Present    bool // installed and signed in
	Identities []Identity
}

// Identity is one identity a tool holds. Label is what a row shows; the other
// fields are the provider's own, empty where they do not apply.
type Identity struct {
	Key                          Key
	Label                        string // "@alice", "dev", "alice@example.com · my-project"
	User                         string // GitHub, Google and Azure
	Project                      string // Google
	Subscription, SubscriptionID string // Azure
	Tenant                       string // Azure
}
```

A tool that is absent is `Installed` false; one signed into nothing is `Present` false with no
identities. Any other non-zero exit proves nothing about the tool: its exit is logged, never its
output, and that tool's entry in `Found` is the last `Discover`'s, or `Present` false with no
identities on the first. Step 2D's row joins a `Tool` and its identities with `State`: the label
comes from the one, the status from the other, and a tool with no identities is one row under
its bare key.

**The store keeps the last `Found`**: `Found() Found` answers it, the zero value before the
first `Discover`, and `WaitFound(ctx) (Found, error)` answers it once the first `Discover` has
ended, waiting until then or until `ctx` ends. Step 2D's re-login and step 5C's profile check
read `Found()`, since each runs well after start and a zero value only narrows what they offer;
step 6C reads the project and subscription once per start and so reads `WaitFound`. Each reads
one answer rather than running the tools again.

**What `Discover` writes to the status table** depends on what the look proved:

| The look | The status table |
| --- | --- |
| The tool is not installed, or is signed into nothing | every state of the provider but an `Excluded` one is replaced by one `Missing` under its bare key (`Key{AWS, ""}` for AWS) |
| `gh auth status` exits 0 | `Key{GitHub, ""}` is set `Valid`, over `Expired` too, and `github.com`'s refused set, standing refusal and backoff are cleared: GitHub has just accepted the token `gh` holds. An expiry or refusal written after the looks began is newer than that proof and is left as it is |
| Any other identity found | set `Valid` when it has no status or is `Missing`; an `Expired` stays, since a listed profile or a configured account does not prove its session works |
| An identity the table holds that the tool no longer lists (a removed AWS profile) | its state is removed |
| AWS with profiles found | `Key{AWS, ""}`'s state, if any, is removed (for the other three the identity is the bare key) |
| A non-zero exit that proves nothing | the provider's states are left alone |

An `Excluded` state (step 2D) is never changed by `Discover`. So a logout or a login in the
user's terminal shows on the next `Discover`, and a status a borrow wrote is overwritten only by
a look that proved it wrong. `Discover` borrows nothing: `valid` means found and not known to
have failed, and the first borrow proves it. Step 2D runs it at start and on refresh; nothing
runs it in this step.

## Decisions this step asks for

1. **The caller names the binaries.** The store takes four paths, not a `PATH`, so `app` decides
   where a tool comes from, in one place: today the sidecar's `PATH`, the login shell's on macOS
   and the desktop session's on Linux. Step 3A's frozen `PATH` can feed it later without the
   store changing. Recommended.
2. **A GitHub token is cached for an hour, then re-borrowed.** `gh auth token` reports no
   expiry, and a re-run is the one way to see a logout. An hourly `gh auth status` beside it
   would run a second command for the same answer. Recommended.
3. **Google's token comes from `gcloud config config-helper --min-expiry=15m`**, not the note's
   `gcloud auth print-access-token`. Both hand back `gcloud`'s cached token; only
   `config-helper` says when it ends and can be told to refresh one near its end, so the cache
   never serves a token with minutes left until a proxy meets a `401`. Recommended.
4. **A credential the provider refused stays refused until the tool answers another, per cache
   key.** The proxy names what it sent and what it borrowed with; the store keeps a hash of the
   value and refuses the same answer for that key, and the identity reads `valid` again only when
   no key's refusal stands. So a revoked token the tool still holds reads `expired` rather than
   flapping, and one Azure resource's fresh token never hides another's refusal. Recommended.
5. **An expiry is remembered for 30 seconds.** A proxy that meets an expired SSO session would
   otherwise start the tool on every request. A user who signs in from their own terminal waits
   at most that long; a renewal from step 2D's button runs `Recheck`, past it. Recommended.
6. **`Discover` overwrites a status only with what it proved.** A tool gone or signed into
   nothing is `missing`, and a GitHub login `gh` just checked is `valid`, whatever the table
   said; a listed profile or a configured account proves nothing about its session, so it only
   fills a gap. So *Refresh* shows a login or a logout made in the terminal, and never hides an
   expiry a borrow saw. Recommended.
7. **The account is read with the borrowed keys, and rides on the credential.** Reading it
   through the profile again could name another account than the one the keys reach, and step 5C
   scopes permissions by it. One value holding both means no caller pairs an account with keys
   from another borrow. It costs one `sts` run per credential. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The store: `Key`, `Binaries`, `NewStore` and its options, `Close`, the cache, one borrow per key, the expiry backoff; the runner and its exec half | `credentials/credentials.go`, `credentials/run.go`, their tests | — | Done |
| 2 | The four borrows, the AWS credential's account, `Region`, each expiry table; `safe.SetSecrets` | `credentials/aws.go`, `github.go`, `google.go`, `azure.go`, `safe/safe.go`, their tests | 1 | Done |
| 3 | Status: the table, `MarkExpired`, the refused hashes and standing refusals, `Recheck`, `Subscribe` | `credentials/status.go`, its test | 1 | Done |
| 4 | `Discover`, `Found`, `Found()` and `WaitFound` | `credentials/discover.go`, its test | 2, 3 | Done |
| 5 | `app` builds the store, keeps it as `App.creds` and closes it; `GH_CONFIG_DIR` joins the login shell's import; the no-disk test | `app/app.go`, `app/app_unix_test.go`, `loginshell/loginshell.go`, their tests | 2, 3 | Done |
| 6 | Docs, per *When it lands* | see there | 1–5 | Done |

**Order:** 1, then 2 and 3 at the same time, then 4 and 5 at the same time, then 6.

## Tests

Each test file sits beside the file it covers. The borrows, the status table and discovery are
tested over the runner seam (`withRunner`), on every platform, with a clock set through
`withClock`: a fake runner checks the argv and answers stdout, stderr and an exit code. What
needs a real process — the working directory, the environment, `Close` — is tested in
`run_unix_test.go` over fake tools, each a script written to a `t.TempDir()` of its own. A fake
holds the token it prints, so it never sits under a folder a test scans for secrets.

**`credentials.go`** (`credentials_test.go`)

- `TestTheCacheExpires`: a second call within the TTL runs nothing; one past the expiry runs
  the tool again; a credential with no expiry keeps `defaultTTL`; each provider's TTL.
- `TestOneBorrowPerKeyAtATime`: two callers for one key run the tool once and both get its
  answer; two keys run at once. A caller arriving as the flight ends finds the cache and runs
  nothing.
- `TestAWaiterThatGivesUpLeavesTheRun`: of two callers for one key, the first cancelling ends
  its own wait alone, and the second gets the tool's answer.
- `TestAnExpiryIsRememberedForTheBackoff`: after an `ErrExpired`, a borrow of that key within
  `expiredBackoff` answers it, runs nothing and writes no status; a borrow of another key runs;
  one past the backoff runs the tool.
- `TestAnExcludedIdentityIsNeverBorrowed`: with `excluded` answering true for `aws:dev`, `AWS`
  and `Region` for `dev` are `ErrExcluded`, run nothing and write no status;
  `excluded` is called with the identity (`github`, `azure`), never the host or resource;
  `aws:prod` borrows.
- `TestNoCLIIsMissing`: an empty binary is `ErrNoCLI` and `Missing` under the bare key, the key
  `Discover` uses.
- `TestASecretIsRegisteredWithSafe`: a borrowed key rendered through `safe.Redact` is blanked,
  a re-borrow replaces its slot rather than adding one, and a refused value stays blanked after
  its slot is replaced.

**`run.go`** (`run_unix_test.go`)

- `TestAToolRunsInTheHome`: the fake records its working directory, which is `home`, whatever
  the test process's own is.
- `TestAFailedBorrowNamesTheProviderAndNeverTheOutput`: the error and the log line
  (`testutil.CaptureLogs`) hold the provider and the exit code and none of what the fake
  printed.
- `TestCloseStopsARun`: a fake that blocks is killed by `Close`, its borrow answers an error,
  and a later borrow is `ErrClosed`.

**`aws.go`, `github.go`, `google.go`, `azure.go`** (`aws_test.go`, `github_test.go`,
`google_test.go`, `azure_test.go`)

- `TestTheAWSBorrowRunsExportCredentials`: the argv, the process JSON's fields, `Expires` and
  the account; the export runs in the sidecar's environment.
- `TestGitHubBorrowsFromGh`, `TestGoogleBorrowsFromGcloud`, `TestAzureBorrowsPerResource`: each
  argv, the token as printed; Google's cached to `token_expiry` less a minute; two Azure
  resources are two runs, each cached to its own `expires_on` less a minute, and an Azure answer
  with only `expiresOn` cached for `defaultTTL`.
- `TestTheAccountIsTheBorrowedCredentialsOwn` (`aws_test.go`): with `AWS_PROFILE`, a static key
  and a profile-based `sts` all answering another account, the account is the one the borrowed
  keys' `sts` answers; `sts` runs with no `--profile`, and its `AWS_` variables are the
  credential's keys and the profile's region alone, and no session token for static keys.
- `TestANewBorrowRederivesTheAccount`: a credential that rotates to another account carries that
  account; `sts` runs once per credential.
- `TestAnAccountThatCannotBeReadFailsTheBorrow`: an expiry line on `sts` is `ErrExpired` and
  `Expired`; an answer with no account fails the borrow and writes no status; an export that
  fails never reaches `sts`.
- `TestRegion`: `""` for a profile with no region, cached for `defaultTTL`.
- `TestEachExpiryLineIsErrExpired`, in each provider's file: one case per line of §3's table; a
  line off the table is an ordinary error and the status stays; `AADSTS70008` does not match
  `AADSTS700084`.

**`status.go`** (`status_test.go`)

- `TestMarkExpiredDropsTheCacheAndSetsTheStatus`: the next borrow runs the tool, every cache
  entry of the identity is gone (both Azure resources; a profile's `Region`, and its account read
  again with the new credential), and
  the gauge delivered the change.
- `TestARefusedCredentialStaysRefused`: after `MarkExpired` with token A, a borrow whose tool
  prints A is `ErrExpired`, caches nothing and leaves `Expired`; one that prints B is `Valid`. A
  borrow in flight when `MarkExpired` lands, answering A, writes neither the cache nor `Valid`.
- `TestMarkExpiredRefusesWhatWasSent`: with the cache already refreshed to B, `MarkExpired`
  with A leaves B borrowable.
- `TestAStandingRefusalHoldsTheIdentity`: refuse the ARM token; a fresh Graph token is cached
  and the identity stays `Expired`; a fresh ARM token makes it `Valid`. The gauge delivers no
  change in between.
- `TestRecheckRunsPastTheCacheAndTheBackoff`: within the backoff, `Recheck` runs the tool once
  per standing refusal and answers the new state.
- `TestTheGaugeIsCurrentOnSubscribe`, and after a change holds it.

**`discover.go`** (`discover_test.go`)

- `TestDiscoverReadsEachTool`: `Found` holds the user, the profiles, the account and project,
  and the subscription; the `gh` user off either spelling, and the active one of two accounts;
  a tool absent is `Installed` false; `gcloud` answering `(unset)`, `gh` saying it is logged
  into no host, `az` asking for `az login` and `aws` listing no profile are each signed into
  nothing; no fake was asked for a token; the contexts are the function's.
- `TestDiscoverWritesWhatItProved`, one case per row of §4's table: a logout replaces a `Valid`
  and an `Expired` with `Missing` under the bare key; `gh auth status` exiting 0 turns an
  `Expired` `Valid` and clears the refusal and the backoff, so the next `GitHub` borrow runs `gh`;
  a logout leaves an `Excluded` state as it was; an AWS profile a borrow set `Expired` stays
  `Expired`; one with no status becomes `Valid`; a removed profile's state goes; `aws`'s
  bare-key `Missing` goes once a profile is found, and once a profile's borrow succeeds; `gh` exiting 1 with no such line leaves the table and
  the last `Found` alone, its output nowhere in the log.
- `TestFoundIsTheLastDiscover`: `Found()` is the zero value before a `Discover`, then what it
  answered, with Google's project and Azure's subscription, id and tenant as fields.
  `WaitFound` blocks until the first `Discover` ends and answers it, and answers `ctx`'s error
  when it ends first.

**`safe`** (`safe_test.go`)

- `TestSetSecretsReplacesTheSlot`: a value set, then replaced, is blanked, then not, and the
  other slots and `AddSecret`'s values stay; `ResetSecrets` clears the slots.

**`app`** (`app_unix_test.go`)

- `TestNoCredentialIsWrittenToDisk`: `New` over data, cache and runtime directories in one
  `t.TempDir()`, with the test's working directory (`t.Chdir`) the data directory, `HOME`
  another, and fakes for all four tools on `PATH` from a third; the log captured by
  `testutil.CaptureLogs`, so the test does not run in parallel. After a borrow of every provider
  through `App.creds`, a `Discover` and a `MarkExpired`, no file under the first
  folder, and no log line, holds a secret access key, a session token or a bearer token. Each
  fake also writes what it prints to a file in its working directory, as a tool that caches
  would, so the test fails if `app` lets a tool run where the sidecar runs. One case per
  provider. **The note's eleventh invariant**; steps 5C, 6B and 6C grow it with a proxied
  request each.

## Security

**Moved.** The sidecar now runs four of the user's tools on the host and holds what they answer
in memory. What holds it: each command is fixed by provider, its one variable an argument the
caller names, never a shell string; the tool does its own resolution, so the sidecar never
parses `~/.aws`, `hosts.yml` or a token cache; the credential is read at use time and cached to
its expiry, never past it; nothing is written under Kstack's directories, and every secret is
registered with `safe` before anything can log it, a refused one included. A refused
credential's SHA-256 stays in memory: a hash, never the credential, where no command can reach
it.

**Not widened.** No sandboxed command can reach the store: nothing in this step serves it, and
the proxies that will (steps 5C, 6B, 6C) hand a command a placeholder and never the credential.

**Residuals.**

- A borrowed credential is whatever the user's tool holds, usually admin; step 7B's connections
  scope it down.
- The store runs the first `aws` on the sidecar's `PATH`, which on macOS is the login shell's: a
  startup file that puts another `aws` first decides what runs, as it already does for the
  kubeconfig's `exec` plugins. On Linux the `PATH` is the desktop session's, which often lacks
  `~/.local/bin` or the Google Cloud SDK, so a tool the terminal finds can be missing here.
- `GH_CONFIG_DIR` is a new row in `loginshell`'s allowlist, which is a security boundary: a
  startup file can now point `gh` at another config, as it can already point `aws`, `gcloud`
  and `az` at theirs.
- The tools see the sidecar's environment, not the terminal's. On macOS `loginshell` does not
  import `GH_TOKEN`, `GH_HOST`, `CLOUDSDK_ACTIVE_CONFIG_NAME` or static `AWS_ACCESS_KEY_ID`, so a
  user who sets one of those in their shell gets a different identity here than in their
  terminal. Importing a token into the sidecar's environment is its own decision, not this
  step's.
- A GitHub token is re-borrowed hourly. `gh auth logout` does not revoke the token and `gh auth
  switch` changes the account, so Kstack can use the old token, or the old account, for up to an
  hour after either. `Discover` shows the logout at once; the cache catches up at the hour.
- The account read hands the borrowed keys to one `aws sts` child in its environment. That
  child runs on the host as the user, who can already read the tool's own store.
- The tools inherit the sidecar's owner-only umask, so a file one writes into its own store is
  created `0600` or `0700`. That is narrower than the tool's default and harms nothing.

The record, `docs/security/<date>-credentials-from-the-users-tools.md`, is short: the sidecar
never parses a credentials file, reads at use time, and copies nothing.

## When it lands

- **The security record** above. No ADR: the design is the note's, taken as written.
- **`security-model.md`**: a row for no credential under Kstack's directories
  (`TestNoCredentialIsWrittenToDisk`), and a row for the borrows being the tools' own commands
  (`TestAFailedBorrowNamesTheProviderAndNeverTheOutput`).
- **`sidecar/CLAUDE.md`**: `credentials` in the package list, with the store, the borrows and
  their TTLs, the expiry backoff, the expiry tables, `Status`, `MarkExpired` and standing
  refusals, `Recheck`, `Subscribe`, `Discover` and what it overwrites, the `excluded` argument,
  `Found()` and `WaitFound`, `Close`, and a refused credential staying refused;
  `safe.SetSecrets` beside `AddSecret`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands); the package's tests run on
every platform, the fake-tool half on macOS and Linux.

By hand there is nothing to see until step 5C: `pnpm tauri dev` starts as before, and `ls
<data>` holds no new file.
