# Step 1: the shared pieces of the cloud proxies

Answers [#935](https://github.com/kstackhq/kstack/issues/935). Step 1 of
[the cloud proxies](README.md), on `wip/cloud-proxies`. The four cloud platform steps of wave 2
([2A](2a-aws.md) AWS, [2B](2b-github.md) GitHub, [2C](2c-gcp.md) Google Cloud,
[2D](2d-azure.md) Azure) build on it and restate none of it.

This step changes a security boundary: what a run's one relay leads to, what the permissions
engine decides, and what the user's approval request draws. It lands with an ADR, a
`docs/security/` record and its rows in [`security-model.md`](../../security-model.md)
([*When it lands*](#when-it-lands)).

Go paths are under `sidecar/internal/` unless rooted elsewhere. **Cloud platform** means one of the
four; **grant** means one run's access to one cloud platform or to its cluster.

## What is true now

Each line is read from the code.

- **A run reaches one thing outside itself, and only with a cluster.** `sandboxedRunFor`
  (`tools/bash/bash.go`) takes a port (`boxer.Port()`: 6443 on Linux, a free loopback port on
  macOS) only when `rt.ClusterID` is set, serves a `kubeproxy.Grant` on `<run dir>/proxy.sock`
  (`tools/bash/proxy.go`, `startProxy`) and sets one `sandbox.Relay`.
- **The sandbox relays one port to one socket and reads nothing.** `Policy.Check`
  (`run/sandbox/policy.go`) refuses a second relay. `ForwarderArgs` (`run/sandbox/forward.go`)
  writes one `--socket` and `--port`. On macOS `relayRules` (`run/sandbox/sandbox_darwin.go`)
  opens TCP on `127.0.0.1` at that port and that socket alone.
- **The grant is the whole handler.** `kubeproxy.NewServer` takes a `*Grant`
  (`run/kubeproxy/server.go`). `Grant.ServeHTTP` answers 401 unless `Host` is
  `cluster.kstack.invalid` and `Proxy-Authorization` carries the grant's 256-bit token, compared
  in constant time (`run/kubeproxy/kubeproxy.go`).
- **The run's kubeconfig is plain HTTP.** It names `http://cluster.kstack.invalid` and a
  `proxy-url` on the loopback port whose password is the token (`tools/bash/kubeconfig.go`).
- **A write is decided by `permissions.Policy.Authorize`** (`run/permissions/permissions.go`)
  over the session's policy for the grant's kube-context; a session with no `Policy` is
  read-only (`run/kubeproxy/write.go`, `policy`). `Rule.Matches` compares class, context,
  namespace, verb, group and kind, and an unset field matches anything. `Auto` permits class 4.
  `permissions.Action` has no field saying what it acts on.
- **The request is the cluster's.** `tools.ActionRequest` holds the action and
  `Write *ClusterWrite` (`tools/tool.go`). It is stored as JSON in the approval's request
  column, kept per call in `toolCallEntry.ClusterWrites` (`agent/chat/store.go`,
  `approval.go`), and served as `ToolCall.clusterWrites` (`sidecar/graph/schema.graphqls`).
  The read decodes it with `json.Unmarshal`, so a key a build does not know is dropped.
  The webview reads it in `src/lib/chats.tsx` (`waitingRequestsOf`) and draws it in
  `src/components/chat/chat-transcript/transcript.tsx`.
- **Who can be asked** is `writesFor` (`tools/bash/proxy.go`): a foreground call's
  `rt.ActionAsker`, or a refusal for a background command and a runtime with no asker. A
  monitor's asker records and never asks (`agent/chat/monitor.go`), and its session sets
  `NoPrompts` and `NoSecretData`.
- **The environment is one table** (`sandboxedRunEnv`, `tools/bash/env.go`), and `Run.check`
  refuses any name `sandbox.Unpassable` matches: `GITHUB_TOKEN`, `GH_TOKEN` and every `AWS_`
  among them (`run/sandbox/env.go`).
- **`settings.json` has three groups**, `sandbox`, `permissions` and `executables`
  (`services/settings/settings.go`). A field that only grants drops a refused value.
- **`safe.AddSecret` only adds** (`lib/safe/safe.go`): its list is the process's, read on every
  render, and nothing leaves it. The cluster grant's token is not registered.
- [`docs/TODO.md`](../../TODO.md#sidecar-go) holds *Draw every approval from an action once a
  second target asks*: fold `ClusterWrites` into one list, and make each target's request a
  member of one family.

## What this step builds

1. **The door.** The run's socket serves one mux, keyed by `Host`, in front of the cluster
   grant and each cloud platform's grant. One port, one socket, one relay.
2. **The grant core** (`run/runproxy`): the per-grant token, the grant's life, its limits, and
   the one ask path every cloud platform's proxy calls.
3. **The gate.** `permissions.Action` gains `Target`. For a cloud platform's action the engine has
   one verdict: it asks once. A session that never asks refuses. No rule and no mode decides
   one.
4. **The request.** `tools.ActionRequest` gains `Cloud`. The wire serves one list,
   `ToolCall.sandboxActions`, whose members are a cluster request or a cloud request. This
   closes the `TODO.md` item.
5. **The consent switch.** One boolean per cloud platform under `sandbox.` in `settings.json`, off
   by default, one wire for all four, one switch component with one confirm.
6. **The cloud platform seam** in `tools/bash`: how a cloud platform adds its routes, files,
   variables and end to a run.

No cloud platform is built here. After this step a run behaves as it does today, and the tests drive
the shared code with a fake cloud platform.

### Not in this step

- Each cloud platform's endpoints, signing, service table, classifier and credential source.
- Rules, modes and durations for a cloud platform's actions beyond asking once.
- TLS. The door is plain HTTP; a TLS route is [step 2D's to build](2d-azure.md).
- Any change to `run/sandbox`. The relay, the forwarder and both profiles stay as they are.
- Any change to how the cluster proxy decides a request.
- Windows, which has no sandbox.

## The decisions

Each row is a disagreement the issue lists. The sections below give the detail.

| Question | Decision | Why |
| --- | --- | --- |
| Routing | One relay; the door routes by `Host` | The sandbox package does not change, and macOS opens one loopback port per run, not one per cloud platform |
| Transport | Plain HTTP. A cloud platform whose client refuses it adds a TLS route to the same listener, in its own spec | `gh` cannot trust a per-run certificate, so TLS alone serves no one; only `az` needs it, and whether `az` can trust the run's certificate is step 2D's spike |
| Request shape | One action family: `Action.Target`, `ActionRequest.Cloud`, `ToolCall.sandboxActions` | It is the change `TODO.md` already names for a second target, and a second list would lose the order requests were asked in |
| Placeholder hosts | `<name>.<platform>.kstack.invalid` where Kstack picks the name; the client's own name where the client dictates it | `.invalid` resolves nowhere; `gh`, `aws` and `az` leave no choice |
| Consent | `sandbox.<platform>`, off by default, for every cloud platform | A cluster credential and a cloud login are often one identity, so a login on the machine is not consent |
| The gate | One verdict in `permissions`, with the cluster's classes, read from no kube-context | Four hand-ordered copies of one rule would drift, and a mode is the user's word on one cluster, which no login is tied to |
| The run's token | One 256-bit token per grant, in one slot the cloud platform's spec names | A cloud platform's client prints and logs its credential slot, so one cloud platform's token must not open another |

## Design

### 1. The door

`tools/bash/proxy.go`'s `runProxy` holds one listener, one server and a route table in place of
one `kubeproxy.Grant`.

**A route is a `Host`.** The door looks it up and hands the request to that route's handler.
It reads nothing else of the request.

| `Host` | Handler | Why this name |
| --- | --- | --- |
| `cluster.kstack.invalid` | the cluster grant | as today |
| `<service>.gcp.kstack.invalid` | Google Cloud | `gcloud` takes an endpoint per service and a proxy of its own, so Kstack picks the name |
| `github.localhost`, `api.github.localhost` | GitHub | `gh` speaks plain HTTP to this host name alone (step 2B's hand check) |
| `127.0.0.1:<port>` | AWS | `AWS_ENDPOINT_URL` is dialled directly, so `Host` is the loopback authority |

- **Anything else is `401 kstack: unauthorized`**, the body the cluster grant writes today:
  an unknown host, a `CONNECT`, a cloud platform that is off. A cloud platform that is on answers a
  wrong token itself, a `407` where it challenges, so the door does not hide which cloud platforms
  are on; the run's environment names them already.
- **A cloud platform's route reads its switch on every request.** One turned off while a run lives
  answers the door's `401` to every new request, a background command's included. A request
  already past the door ends as it would have: a download runs on, and a change waiting on
  the user is still forwarded if approved.
- **No two routes share a `Host`.** `sandboxedRunFor` fails the run when two mounts name one,
  or a mount names the cluster's.
- **The door routes; it does not authenticate.** Each grant checks its own token on every
  request. A request sent to the wrong route is a 401 from that grant. No route is trusted
  because of its `Host`: a local process on macOS can send `127.0.0.1:<port>`.
- **The loopback authority has one route.** A later cloud platform that also needs it must solve
  that in its own spec, as Azure's does by adding the transport to the key.
- **A match is exact**, on `r.Host` as Go's server reads it from an absolute-form request or
  the `Host` header. `localhost:<port>` matches nothing.
- **The naming rule.** Where the client lets Kstack choose the host (a proxy setting of the
  tool's own, with the request in absolute form), the name is `<name>.<platform>.kstack.invalid`:
  it resolves nowhere (RFC 2606), so a request that misses the proxy reaches nothing. Where the
  client dictates the host, the route takes the client's name and the cloud platform's spec says
  why.
- **One word per cloud platform**, everywhere a cloud platform is named in code, a key or a host:
  `aws`, `github`, `gcp`, `azure`.

**The server** moves from `kubeproxy.NewServer(*Grant)` to `runproxy.NewServer(http.Handler)`,
with the same header timeout, idle timeout and header cap, and no read or write timeout. HTTP/1.1
alone.

**A run takes a port and a socket when it has a cluster or at least one cloud platform.**
`sandboxedRunFor` calls `boxer.Port()`, listens and sets the one `sandbox.Relay` in either case.
A run with neither has no relay, as today.

**The run ends in today's order**, widened: every grant ended, the server and listener closed,
every grant's handlers joined, the claim, the directory. No request reaches the user once `Run`
returns.

### 2. The transport

**Plain HTTP.** The hop is the run's own loopback to a Unix socket in the run's directory: on
Linux inside the run's network namespace, on macOS a port only local processes dial. TLS on
that hop protects nothing the token does not. The cluster's kubeconfig stays as it is.

**Why not TLS for everyone.** `gh` is a Go program. On macOS it verifies through the system's
verifier and reads no certificate file, so it would trust only a certificate in the user's
keychain, and a run without network has no trust daemon
(`TestTrustFetchesNothingWithoutTheInternet`). `gh` also picks plain HTTP by host name.

**A client that refuses plain HTTP** gets a TLS route on the same listener and port, built by
its cloud platform's spec. Only `az` is known to need one, and [step 2D](2d-azure.md) holds the
design and the spike that decides it. Nothing in this step
waits on it: a route gains a transport in its key, and the door's other routes do not change.

### 3. The grant core (`run/runproxy`)

A new leaf beside `kubeproxy`. It imports `permissions` and `session`, and nothing of `tools`
or `services`; `kubeproxy` does not import it and keeps its own grant, so this step changes
nothing in how a cluster request is decided. Building the cluster grant on this one is a
`TODO.md` line ([*When it lands*](#when-it-lands)). `TestInternalFoldersAreLayers` holds as
is. Each cloud platform's proxy is its own leaf
(`run/<platform>proxy`) over a `runproxy.Grant`.

```go
// Grant is one run's access to one cloud platform, dead once End is called.
type Grant struct{ /* target, session, asker or refusal, token, limits */ }

func NewGrant(target permissions.Target, sess session.Session,
	asker Asker, refusal string, limits Limits) *Grant

// Challenge is the answer to a request with no token: 407 and
// Proxy-Authenticate when the grant challenges, else 401.
func (g *Grant) Challenge(w http.ResponseWriter)

func (g *Grant) Token() string                 // 256 bits, hex
func (g *Grant) Holds(candidate string) bool   // constant time; false once ended
func (g *Grant) Enter(r *http.Request) (*http.Request, func(), bool) // joins Wait; r ends with the grant
func (g *Grant) Body(w http.ResponseWriter, r *http.Request) (body []byte, release func(), status int) // a body slot, then the read; nothing for no body
func (g *Grant) Slot(ctx context.Context) (release func(), status int) // open-request cap, then limiter
func (g *Grant) Decide(ctx context.Context, req Request, forward func() error) Outcome // req.Action.Target is set to the grant's
func (g *Grant) End()
func (g *Grant) Wait()

// Outbound is the headers a cloud platform forwards: those of in that allowed names,
// never Authorization or Proxy-Authorization.
func Outbound(in http.Header, allowed []string) http.Header

// Limits bound one grant.
type Limits struct {
	QPS    rate.Limit
	Burst  int
	Open   int // requests forwarded at once
	Bodies int // request bodies held at once
	// Challenges answers a request with no token 407, for a client that sends
	// proxy credentials only once asked.
	Challenges bool
}

// Request is one classified action, held for the user or refused unasked.
type Request struct {
	Action permissions.Action
	Sent   Sent
}

// Sent is the request as the user is shown it: tools.CloudRequest's fields,
// the body as bytes.
type Sent struct {
	Host, Method, Path, ContentType string
	Body                            []byte
	Unshown                         string
	Details                         []Detail // Label and Value, as tools.Detail
}

// Asker puts a request to the user and records one refused unasked. From Ask,
// a context error is a wait that ended with no decision.
type Asker interface {
	Ask(ctx context.Context, r Request) (approved bool, err error)
	Record(ctx context.Context, r Request, d permissions.Decision, reason string) error
}

// Outcome is how Decide ended.
type Outcome struct {
	Forwarded bool   // forward was called
	Err       error  // forward's error
	Status    int    // what to answer when nothing was forwarded: 403 or 429
	Message   string // the refusal in the user's words, starting "kstack:"
}
```

A cloud platform writes `Status` and `Message` in the shape its client prints. Where its client
retries a `429`, it answers that case with a status the client does not retry, and its spec
names it.

`refusal` is `tools/bash`'s sentence, as the cluster's two are: one for a background command
and one for a runtime with no asker, each naming the cloud platform.

**The token.**

- One per grant, 32 bytes from `crypto/rand`, hex, as `kubeproxy.NewGrant` mints its own. It is
  not a cloud credential. It opens that grant alone, from this machine, until the run ends.
- **One token per cloud platform, not one per run.** Each lands in a different client's credential
  slot, and clients print, log and embed what sits there: an AWS key id appears in error
  text and in a presigned URL, `gh auth token` prints its login. A token read out of one
  cloud platform's output opens that cloud platform's reads and nothing else.
- **Where it rides is the cloud platform's, by one rule.** Each cloud platform names the one slot
  its client sends on every request, and its grant accepts no other. A client that carries one
  credential in two places (a header and a presigned query, an identity header then a bearer) has
  each named in its spec, with the requests that use it. `Proxy-Authorization` is preferred where
  the client has a proxy setting of its own, since no browser page can set a `Proxy-` header.
  Otherwise it is the cloud platform's own credential slot: the AWS key id, a bearer token, an
  identity header.
- **The proxy deletes it, and any placeholder credential, before it forwards**
  ([the headers forwarded](#what-the-user-approves-is-what-is-sent)). Nothing the sandbox sent
  as a credential reaches the cloud platform.
- A request without it is `401`. A cloud platform whose client sends proxy credentials only after a
  challenge (`git`) answers `407` with `Proxy-Authenticate: Basic`: `Limits.Challenges`, off
  by default, which `Grant.Challenge` reads.
- It is never logged. It is not registered with `safe.AddSecret`, as the cluster grant's is
  not: the command that could print it already holds it, and it opens nothing once the run
  ends.

**The life.** `End` makes every later `Holds` and `Enter` false and cancels every request in
flight, a request waiting on the user included. `Wait` joins every handler `Enter` let in.

**The limits** are `tools/bash/proxy.go`'s numbers (20 requests a second past a burst of 50, 32
open at once), and 16 bodies held at once, counted per grant. No slot is a `429` with no
`Retry-After`.

**The grant says what its actions act on.** `NewGrant` panics on `Cluster`, a wiring mistake
as `NewBox`'s are, and on a limit below one, which would refuse every approved request.
`Decide` replaces `req.Action` with the grant's target and the classifier's `Class` and
`Summary` before anything reads it, so an action a classifier left unset is never decided,
recorded or drawn as the cluster's, and no field naming a cluster's object reaches a record.

**The ask path** (`Decide`) is `kubeproxy/write.go`'s, less the cluster's parts (no command
rules, no helm gate, no diff, no mode):

1. A grant with no asker answers its refusal, before any queue.
2. A request that cannot be shown is refused, before any queue
   ([*what the user approves*](#what-the-user-approves-is-what-is-sent)).
3. The request takes the grant's write lock: one slot, at most the cluster's `maxQueuedWrites`
   waiting, one more a `429`. A change holds the lock until `forward` returns, so a cloud platform
   sees changes in the order decided. A class 6 read gives it up once approved, before
   `forward`, as the cluster's Secret read does, so a long download holds no other request.
4. The verdict is `Authorize` ([§4](#4-the-gate)) over a `Policy` that holds the session's
   `NoPrompts` and `NoSecretData` and nothing else. It reads no mode and no rule.
5. A refusal is recorded through `Asker.Record` and answered `403` with the reason. A failed
   record answers `403` too. No session that gets a grant sets either flag today
   ([§4](#4-the-gate)); the branch is for one that does.
6. Otherwise the request is put to the user through `Asker.Ask`, never grantable. Only an
   approval calls `forward`, taking a slot and the limiter then, so a request waiting on the
   user holds neither. A denial, a wait that ends and a failed write of the answer forward
   nothing.

Each grant has its own lock, so one command's cluster request and cloud platform request are decided
side by side. The user still sees one at a time: the chat's `askAction` holds `askMu` across
the wait, so the second is opened and drawn once the first is answered. The call's one list
holds both, in the order asked.

#### What the user approves is what is sent

- **A body a classifier reads, or a request shows, is read through `Grant.Body`** before
  `Decide`. A request the cloud platform classes without its body (a Git fetch, by its path) it may
  stream instead, a read's included. A request with no body takes
  nothing. `Body` refuses one with a `Content-Encoding` (`403`, *the request cannot be shown*),
  a read's included, since no classifier reads an encoded body. It then takes one of the
  grant's 16 body slots, none free a `429`, and reads at most 1 MiB (`413` past it). The slot
  is held until the request ends. So, unlike a cluster write, a queued request holds its body,
  and a grant holds at most 16 MiB however many connections a command opens.
- **The headers forwarded are the cloud platform's allowlist**, built by `runproxy.Outbound`: exact
  names, a constant of the cloud platform's package, and nothing else of the request's. `Outbound`
  never copies `Authorization` or `Proxy-Authorization`, whatever the list says. A token that
  rides anywhere else (a header of the cloud platform's own, a query parameter) is the cloud
  platform's to keep off its list and strip from the query. The cloud platform sets the user's
  credential on the result. A header that changes what the request does (a method override, an
  operation name, a copy source) is on the list only when the classifier reads it and the request
  draws it as a detail. So no header the user was not shown changes what an approval sends, or turns
  a read into a change.
- **A query `url.ParseQuery` refuses is refused** (`403`), as the cluster proxy refuses one:
  the query shown would not be the query that runs. So is a body sent as
  `application/x-www-form-urlencoded` that `url.ParseQuery` refuses, for the same reason.
- **A shown body is valid UTF-8**, and is drawn as sent.
- **`Decide` refuses any other** (`403`, *the request cannot be shown*), unless the cloud platform
  sets `Unshown`: one fixed sentence of its own saying what bytes the request does not show
  (*a Git pack of the commits pushed*). The request then draws that sentence where the body
  would be, and the cloud platform streams the body itself, not through `Body`: past 1 MiB, in no
  body slot and whatever its encoding. It may read a bounded opening of the stream to class the
  request and fill its details (a push's refs), the bound stated in its spec. A
  cloud platform that sets `Unshown` says in its spec why the rest of the request is enough to
  approve.

`tools/bash` adapts `Asker` to `tools.ActionAsker` (`cloudAsker`, beside `runtimeAsker`), so
neither package imports the other. It writes `Sent` into `ActionRequest.Cloud` and answers
`Ask` with the answer's `Approved`.

**What every cloud platform's proxy does around the core**, each in its own leaf:

- The upstream host comes from the cloud platform's own table, never from the request, and is
  dialled over HTTPS with the sidecar's roots. The proxy follows no redirect.
- A path that is not canonical, an `Upgrade` and a `Connection: upgrade` are refused.
- A refusal is written in the shape the cloud platform's client prints, starts `kstack:`, and uses a
  status the client does not retry. It never carries an error's text.
- A read is not recorded, as a cluster read is not.

### 4. The gate

**`permissions.Action` gains `Target`** (`json:"target,omitempty"`):

```go
// Target is what an action acts on. The zero value is the chat's cluster.
type Target string

const (
	Cluster Target = ""
	AWS     Target = "aws"
	GitHub  Target = "github"
	GCP     Target = "gcp"
	Azure   Target = "azure"
)
```

**A cloud platform's action carries `Target`, `Class` and `Summary` alone.** Every other field
(`Context`, `Namespace`, `Verb`, `Group`, `Kind`, `Name`, `DryRun`) names a cluster's object,
and rules and modes read them as that. What a classifier reads from a request (a service, an
operation, a resource group) goes in `Sent.Details`, which the request draws under its own
label.

**The classes are the cluster's**, and every cloud platform's classifier puts a request in exactly
one row:

| A request that | Is | What happens |
| --- | --- | --- |
| reads metadata or state | a read | forwarded unasked, unrecorded |
| changes something | class 4, `UpstreamWrite` | asks once |
| is on the cloud platform's own high blast-radius list | class 5, `Destructive` | asks once, drawn as destructive |
| reads a secret's value, or the stored data its cloud platform's spec names (a bucket object's contents) | class 6, `SecretRead` | asks once; refused under `NoSecretData` |
| answers a credential (a token, a key, a password, a login to a cluster or a registry) | refused | `403`, never asked |

- **A classifier fails toward asking.** What it cannot prove a read is class 4.
- **A credential is refused, not asked for.** An approved answer lands in the command's output
  and from there with the model and its cloud platform, and it outlives the run. The cluster proxy
  refuses a ServiceAccount's `token` for the same reason. Where a cloud platform cannot tell a
  credential from data by name, it refuses.
- **Which stored data asks is the cloud platform's to name.** A bucket's objects ask. A
  repository's content does not: reading it is what GitHub's switch is turned on for, and a
  prompt on every fetch would be approved unread. Each cloud platform's spec says which of its
  stored data is class 6 and which is a read, and its line of the confirm says the same.
- **Metadata that holds secrets is class 6, or is blanked.** Each cloud platform's spec lists the metadata reads
  whose answer its documentation says can hold a secret value (a function's environment, an
  instance's user data, a template's parameters). It classes each 6, or, where asking would
  stop the cloud platform's everyday reads, forwards the read unasked and blanks the named fields in
  the answer, failing closed as the cluster proxy's Secret rewriter does. A secret a user put
  somewhere no list names is read unasked; that is a residual
  ([*Security*](#security)).
- **Class 5 is optional.** A cloud platform with no list uses class 4 for every change. The verdict
  is the same; the request reads differently.

**One verdict for a cloud platform's action**, in `Policy.verdict`, taken first when `act.Target` is
not `Cluster`: `Forbid`, *a cloud platform's request always asks*. `Authorize`'s two flags stand around it unchanged:

1. `NoSecretData` and class 6: refused.
2. Otherwise `Forbid`: a prompt.
3. `NoPrompts` turns the prompt into a refusal.

**`Rule.Matches` is false for any action whose `Target` is not `Cluster`.** No `Allow`, `Deny`
or `AskFor` the user wrote for a cluster reaches a cloud platform, and neither do `Refused` and
`RefusedSecrets`.

So:

- **No mode decides a cloud platform's action.** A mode is the user's word on one kube-context, and
  nothing ties a login on the machine to that cluster: GitHub has no cluster, and the AWS
  login need not be the account the chat's cluster runs in. `Auto` permits nothing: it rests on
  the cluster classifier's curated class 5 list, and no cloud platform's classifier has one complete
  enough to carry that. `ReadOnly` refuses nothing: every change already asks.
- **A cloud platform is the same in every chat**, with a cluster or without.
- **The answers are Once and Deny.** `Grantable` is false for `Forbid` already, so
  `service.Approve` takes those alone and no answer writes a rule.

**Who gets a cloud platform at all.** A run is given a cloud platform's grant when the cloud
platform's switch is on at the run's start (and its route reads it again on every request), the
session's `Kind` is `Chat` or `Subagent`, and the run is not the executable probe's. A monitor gets
none: no turn of the user's started it. A background command's grant has no asker: it reads as a
foreground command does, and every request that would ask is refused. It reads on after its turn
ends, as it reads the cluster, until the command ends or the switch turns off, which stops its next
request ([*Security*](#security)). `session.Session` gains no field, so `Narrow` does not change.

### 5. The request

**Stored.** `tools.ActionRequest` gains one optional field beside `Write`; at most one is set.

```go
// Cloud is a request to a cloud platform as sent; nil for a cluster request.
Cloud *CloudRequest `json:"cloud,omitempty"`

// CloudRequest is what a sandboxed command sent to a cloud platform, as the user is
// shown it.
type CloudRequest struct {
	Host        string   `json:"host"`   // the upstream host dialled
	Method      string   `json:"method"`
	Path        string   `json:"path"`   // path and raw query, as forwarded
	ContentType string   `json:"contentType"`
	Body        string   `json:"body"`   // as sent; empty with Unshown
	// Unshown is the cloud platform's sentence for a body the request does not show.
	Unshown string   `json:"unshown,omitempty"`
	Details []Detail `json:"details,omitempty"`
}

// Detail is one line the cloud platform's classifier read from the request.
type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
```

- The cloud platform is `Action.Target`; `CloudRequest` does not repeat it.
- **`Details` is how a cloud platform says what is its own without a type of its own**: AWS's
  service, operation and region; the account a request runs as; each ref a push names and what
  happens to it. A label and `Unshown` are constants of the cloud platform's package. A value is the
  command's text.
- **`Action.Summary` names the cloud platform in words** (*AWS: call ec2:TerminateInstances in
  eu-west-1*), so a reader that knows no `Target` still says what the row is.
- **No migration and no contract bump.** Both additions are optional keys, so no stored row
  changes and a migration would have nothing to rewrite. A stored row with neither reads as a
  cluster's, as it does today. The register says a JSON column changes by an `app.db`
  migration, so the commit that adds the keys also adds the exception to its *How a change
  ships* (a key a row may lack, read as its zero value, needs none) and names both keys in the
  JSON-columns row.
- **An older build reads a cloud row as an action with no request**: its decode drops the keys
  it does not know. It draws the summary, which names the cloud platform, and no method, path or
  body. That is history alone: an approval waits in its sidecar's memory, so a row a dead
  sidecar left pending has nothing waiting on its answer.

**Kept.** `toolCallEntry.ClusterWrites` becomes `SandboxActions`, the one list `recordAction` and
`openAction` append to, in the order asked.

**Served.** `ToolCall.clusterWrites` becomes `ToolCall.sandboxActions`:

```graphql
"A request a sandboxed command sent that a proxy decided, in the order asked."
type SandboxAction {
  approval: ToolCallApproval!
  "Null when this build cannot read the stored request; it then offers Deny alone."
  action: PermissionAction
  "The request as sent. Null with a null action, and for an action with no request."
  request: SandboxRequest
  reason: String
}

union SandboxRequest = ClusterWrite | CloudRequest

type CloudRequest {
  host: String!
  method: String!
  path: String!
  contentType: String!
  body: String!
  "The cloud platform's sentence for a body the request does not show; empty for a shown body."
  unshown: String!
  details: [RequestDetail!]!
}

type RequestDetail { label: String!  value: String! }

enum PermissionTarget { Cluster AWS GitHub GCP Azure }
```

The list is `sandboxActions`, not `actions`: `ToolCall.action` is the call's own `ToolAction`.
`ClusterWrite` keeps its request and diff fields and loses `approval`, `action` and `reason`,
which move up. `PermissionAction` gains `target: PermissionTarget!`. `contentType` and `body`
are served while the request waits, as today. The sidecar and the webview ship together, so
the old field is removed in the same change.

**Why one list and one cloud member.** One command can change the cluster and a cloud platform in
turn, and two lists cannot say which came first. The four cloud platforms' requests are one thing to
the user, an HTTP request to a named host as their identity, so they draw one way; what
differs is lines of text. A fifth cloud platform adds an enum value.

**Drawn** (`src/lib/chats.tsx`, `chat-transcript/transcript.tsx`). `waitingRequestsOf` reads
`sandboxActions`. A cluster request draws as today. A cloud request draws the summary, the
host, each detail, then the raw request open (method, path, media type, and the body or the `unshown`
sentence) and Once and Deny. Its label is *`<Cloud platform>` change awaiting approval*, or
*`<Cloud platform>` data read awaiting approval* for class 6; a settled one is tagged with the
cloud platform's name. The webview holds each cloud platform's name (*Google Cloud* for `GCP`).
There is no diff. **Every string of it is the command's text and is drawn through `VisibleText`**,
labels included. Approve stays one click on the drawn request: this adds a thing the request shows,
not an approval path.

### 6. The consent switch

- **The keys are `sandbox.aws`, `sandbox.github`, `sandbox.gcp` and `sandbox.azure`**:
  booleans in the existing `sandbox` group, each with its `Field*` constant. Each only grants,
  so a refused value drops to off and `strictest` needs no entry. A new field needs no
  `schemaVersion` bump. The group is `sandbox` because the switch widens what the sandbox
  reaches, and one group keeps four cloud platforms from adding four.
- **This step adds all four fields**, the table that joins them (`cloudSwitch`, target to
  field), and `Store.CloudOn(target)` and `SetCloud(target, on)` over it. A cloud platform's step
  adds nothing to `settings`.
- **A switch exists only for a cloud platform the build has.** `app` hands one list of
  `CloudPlatform`s to `bash.New` and its targets to the resolver. A field whose cloud platform is
  not built is read by nothing. In production that list is empty after this step, so Settings
  draws no switch and a run gets no grant.
- **Off by default, for every cloud platform.** On EKS, GKE and AKS the cluster
  credential and the cloud login are one identity, so a login on the machine says nothing of
  whether the user wants an agent reading the account. This is the rule `TODO.md` already
  states for cloud model platforms.
- **One wire for all four.** `sandboxCloudAccess: [CloudAccess!]!` (`target`, `on`), a row
  per cloud platform the build has, and `sandboxCloudAccessSet(target: PermissionTarget!, on:
  Boolean!): [CloudAccess!]!`, which answers the list as `sandboxPathInclude` does. The
  mutation names the field it writes, and refuses `Cluster` and a cloud platform the build lacks,
  so no row's `target` is `Cluster`.
- **One switch.** The Sandbox section of Settings gains *Cloud access*: a row per cloud platform the
  wire lists, a switch, and a confirm before it turns on, as the chat's network switch has.
  The confirm says, for the named cloud platform: sandboxed commands read your account without
  asking; what they read is sent to the model's cloud platform; with network on a command can send
  it anywhere; a change asks first; nothing that answers a credential works. Then one line
  that is the cloud platform's own, held by the webview beside its name: what of its stored data is
  read unasked and what asks (*Private repositories are read without asking*; *An object's
  contents ask first*).
- **The confirm is the webview's.** The mutation takes no proof of it, as no settings mutation
  does: code running in the webview already holds every mutation (root `CLAUDE.md`, *Security
  invariants*).
- **The switch is global.** A cloud platform on is on for every chat. See
  [*Open questions*](#open-questions), 1.
- **Off blocks new requests at once.** A run mounts a cloud platform only when its switch is on at
  the run's start, and the cloud platform's route reads the switch again on every request
  ([§1](#1-the-door)). A request already admitted finishes, an approval the user then gives
  included. A run started while it was off has no route, whatever the switch does
  later.
- With the switch on and no login found, the run still starts and each request answers the
  cloud platform's one fixed sentence, as a run whose cluster claim cannot be made still runs.

### 7. The cloud platform seam (`tools/bash`)

A cloud platform joins a run through one interface, built in `app` and handed to `bash.New` as a
list. The executable probe passes none. `app.Config` gains an unexported field a test sets to
add a fake, beside `fake`.

```go
// A CloudPlatform is one cloud platform a sandboxed run can reach.
type CloudPlatform interface {
	Target() permissions.Target
	// Mount builds the cloud platform's part of one run: its grant, and the files
	// it writes under the run directory.
	Mount(ctx context.Context, run CloudRun) (CloudMount, error)
}

// CloudMount is what a cloud platform adds to one run.
type CloudMount struct {
	Routes []Route  // Host, to the cloud platform's handler
	Env    []string // rows for sandboxedRunEnv
	Grant  *runproxy.Grant
}
```

`CloudRun` carries what a cloud platform may read of the run: the port, the run directory, the tool
home, the run's `TMPDIR`, and the grant's inputs (session, asker or refusal).

**A cloud platform's `CloudPlatform` is a file of `tools/bash`** (`gcp.go`), since it answers this
package's types: a constructor `app` calls with the cloud platform's service, over the cloud
platform's `run/` leaf. The leaf holds the handler, the classifier and the table, and imports
nothing of `tools`.

Rules every cloud platform's mount follows:

- **Every value comes from the mount, never from the sidecar's environment.** `Unpassable`
  checks a name, not a value, so the table is what keeps a value Kstack's. A cloud platform that
  must set a name `neverEnv` lists changes `run/sandbox/env.go` in its own step, naming exact
  names, and that is a security change of its own.
- **A file a cloud platform writes** goes under the run directory (read-only to the run), the tool
  home, or, for one its client must write per run, the run's `TMPDIR`. It is 0600, created with `O_EXCL`, and holds the grant's token or a fixed placeholder, never
  a credential. User text in it is written by an encoder, never a template.
- **A tool-scoped setting is preferred to a variable other tools read.** A cloud platform that sets
  one of those (`HTTP_PROXY`, `REQUESTS_CA_BUNDLE`) states its cost under network. No two
  cloud platforms set one name to different values; `sandboxedRunFor` fails the run if they do.
- **A mount that fails fails the run, and leaves nothing behind.** So does a clash of routes
  or variables. `sandboxedRunFor` then ends the run as a finished one ends: every grant
  already mounted ended and joined, the listener closed, the claim released, the run directory
  and `TMPDIR` removed. No login is not a failure: that mount succeeds and its requests answer
  the cloud platform's sentence.
- **A run in which any cloud platform sets a proxy variable also holds `NO_PROXY=127.0.0.1` and
  `no_proxy=127.0.0.1`**, so a client dialling the loopback authority is not sent round
  through the proxy setting.
- **The credential lives in a service under `services/`**, since it outlives a run, and the
  cloud platform's `run/` leaf reaches it through an interface. It is resolved outside the sandbox,
  no earlier than the first mount with the switch on, never at startup and never while the
  switch is off. It is kept in memory, never written by Kstack, and dropped when the upstream
  says it is dead: a `401`, or the cloud platform's own code for it. Its failure is one fixed
  sentence in the user's words, never a tool's output. A cloud platform that runs the user's own
  CLI for it runs a program off the sidecar's `PATH` with fixed arguments, the position a
  kubeconfig's `exec` plugin already holds.
- **A credential is registered while it is held.** The service calls `safe.AddSecret` when it
  takes one and `safe.ForgetSecret`, which this step adds, when it drops one, so a token that
  rotates hourly does not grow the list for the sidecar's life.
- **The context says which cloud platforms are on.** The Sandbox section `agent/chat/workspace.go`
  writes (`withSandbox`) gains a `cloud` key beside `network`: the words of the cloud platforms the
  build has whose switch is on (`["gcp"]`), left out when there is none. The section goes
  again when a switch changes, as it does for `network`. This step builds the key; with no
  cloud platform built it is never written.
- **The prompt.** This step rewords one sentence of `tools/bash/prompts/sandbox.md`, the one
  saying `gh`, `aws` and a private registry have no login, so it is true whatever is on:
  *With the internet a command still holds no credential, so a tool has a login only where the context's `cloud` names its cloud platform, and a private registry has none.*
  Each cloud platform's step adds one paragraph, static text that tells the model to read it only
  when the context's `cloud` names the cloud platform, in these words: the tool works as the user
  through a proxy; a read runs; a change waits for the user each time or is refused; a
  background command changes nothing; nothing that answers a credential works.

## Tasks

All start Planned. Each ends green under `make test-changed`.

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Target`, the cloud platform verdict, `Rule.Matches` | `run/permissions/` | — | Done |
| 2 | The server moves | `run/runproxy/server.go`; `run/kubeproxy/server.go` and its test removed; `tools/bash/proxy.go`'s one call | — | Done |
| 3 | The grant core, the body slots, the outbound headers and the ask path | `run/runproxy/grant.go`, `ask.go` | 1 | Done |
| 4 | The action family: `Cloud`, `SandboxActions`, `ToolCall.sandboxActions`, the drawing | `tools/tool.go`, `agent/chat/record.go`, `approval.go`, `store.go`, `sidecar/graph/schema.graphqls`, generated code, `src/gql/`, `src/lib/chats.tsx`, `src/components/chat/chat-transcript/`, `docs/persisted-data.md` | 1 | Done |
| 5 | The switch: the four fields, `cloudSwitch`, the wire, the Settings row and confirm | `services/settings/`, `sidecar/graph/`, `src/lib/`, `src/components/settings/settings-dialog/`, `design/components.json` if a component is added | 4 | Done, but the Settings row and confirm, which wait for the new settings panel |
| 6 | The door and its switch read, the seam, `cloudAsker`, a port for a run with a cloud platform, the end order, `NO_PROXY`, the context's `cloud` key, the prompt's one sentence, `safe.ForgetSecret` | `tools/bash/proxy.go`, `bash.go`, `env.go`, `rundir.go`, `prompts/sandbox.md` and its golden, `agent/chat/workspace.go`, `lib/safe/safe.go`, `app/` | 2, 3, 4, 5 | Done |
| 7 | A fake cloud platform through the real sandbox | `app/` (`Config`'s test field, `startE2E`, `curl` in its `toolsFolder`) | 6 | Done |
| 8 | Docs, per *When it lands* | see there | 1–7 | Done |

1 and 2 can start together; 3 and 4 after 1; 5 after 4, which adds `PermissionTarget`; 6 after
all of them, since `cloudAsker` writes `ActionRequest.Cloud` (4) and a run reads `CloudOn` (5).

## Acceptance

Each is a test beside its file. The cloud platform is a fake: a `CloudPlatform` in the test, with
`Target` `AWS` since the wire's enum is closed, whose upstream is an `httptest` TLS server.

**`run/permissions`**

- `TestACloudPlatformActionAlwaysAsks`: under `ReadOnly`, `Ask` and `Auto`, classes 4, 5 and 6 are
  `Forbid`, and never `Grantable`.
- `TestNoRuleReachesACloudPlatformAction`: an `Allow`, a `Deny` and an `AskFor` with every field
  unset, `Refused` and `RefusedSecrets`, each leave the verdict as it was.
- `TestNoPromptsRefusesACloudPlatformAction`, `TestNoSecretDataRefusesACloudPlatformDataRead`.
- The cluster's tables pass unchanged.

**`run/runproxy`**

- `TestOnlyTheGrantsTokenHolds`: none, another grant's and an ended grant's are false.
- `TestAChallengingGrantAnswers407`, and one that does not challenge answers `401`.
- `TestOnlyAnApprovalForwards`, `TestADenialForwardsNothing`,
  `TestAWaitThatEndsForwardsNothing`, `TestAFailedRecordForwardsNothing`,
  `TestRequestsAskOneAtATime`, `TestAFullQueueIs429`.
- `TestAGrantWithNoAskerRefuses`, before any queue and unrecorded.
- `TestABodyThatIsNotTextIsRefused`, before any queue; `TestAnEncodedBodyIsRefused`, by
  `Body`, unread and in no slot; `TestAnUnshownBodyAsksWithItsSentence`.
- `TestAQueryThatDoesNotParseIsRefused`, before any queue.
- `TestBodiesHeldAreCapped`: with every body slot held, one more body is a `429` and is not
  read; a slot comes free when its request ends; a request with no body takes none.
  `TestABodyPastTheCapIs413`.
- `TestOnlyAllowedHeadersAreForwarded`: a header off the list, `X-HTTP-Method-Override` among
  them, never reaches the upstream, and neither do `Authorization` and `Proxy-Authorization`
  when the list names them.
- `TestDecideKeepsACloudPlatformsFieldsAlone`: an action with no target, and one with another
  cloud platform's and every cluster field set, are each decided, recorded and asked as the grant's,
  with its class and summary alone.
  `TestAGrantForTheClusterPanics`.
- `TestADataReadForwardsOutsideTheLock`: while an approved class 6 read's `forward` runs, a
  second request is decided. `TestAChangeHoldsTheLockUntilItIsForwarded`.
- `TestAWaitingRequestHoldsNoSlot`; `TestEndCancelsAWaitingRequest`, and `Wait` joins it.
- `server_test.go`'s tests move here with the server.

**`run/kubeproxy`**: its tests pass unchanged but for `server_test.go`'s, which move;
`TestAnotherHostIsUnauthorized` and `TestAWrongOrDeadTokenIsUnauthorized` among them.

**`tools/bash`**

- `TestTheDoorRoutesByHost`: each host reaches its handler; an unknown host, `localhost:<port>`
  and a `CONNECT` are each the same `401`.
- `TestAWrongRouteIsUnauthorized`: one cloud platform's token on another's route.
- `TestARunWithACloudPlatformAndNoClusterHasARelay`; `TestARunWithNeitherHasNoRelay`.
- `TestACloudPlatformThatIsOffHasNoRoute`; `TestAMonitorHasNoPlatform`, `TestTheProbeHasNoPlatform`,
  `TestABackgroundCommandsGrantHasNoAsker`.
- `TestSwitchingACloudPlatformOffClosesItsRoute`: a run's next request is the door's `401`, and one
  after the switch turns back on is served. `TestARunStartedWhileOffNeverGetsARoute`.
- `TestARequestWaitingOnTheUserOutlivesTheSwitch`: a change asked before the switch turns off
  is forwarded once approved, and a read already streaming finishes.
- `TestTwoMountsCannotNameOneHost`: two cloud platforms' routes, and a cloud platform's on the
  cluster's host, each fail the run.
- `TestAFailedMountEndsTheEarlierOnes`: the first cloud platform's grant is ended, its token holds
  nothing, and the run directory is gone.
- `TestACloudRequestReachesTheAskerWhole`: `cloudAsker` hands every field of `Sent` to the
  runtime's asker as `ActionRequest.Cloud`.
- `TestTwoCloudPlatformsCannotSetOneVariableTwoWays`; `TestAProxyVariableBringsNoProxy`.
- `TestTheRunEndsEveryGrant`: no request is asked once `Run` returns.

**`lib/safe`**: `TestAForgottenSecretIsNotBlanked`, and one value added twice and forgotten
once is still blanked.

**`agent/chat`, `graph`**

- `TestACallsActionsKeepTheirOrder`: a cluster write, a cloud request and a cluster write
  under one call are served in that order.
- `TestTheSandboxSectionNamesTheCloudPlatformsOn`: `cloud` lists a cloud platform that is built and
  on, is left out with none, and the section goes again when a switch changes.
- `TestTwoProxiesAskOneAtATime`: a cluster request and a cloud request asked together are
  opened one after the other, the second once the first is answered.
- `TestAStoredClusterRequestReadsTheSame`: a row written before this change serves the action
  and request it served.
- `TestAnUnreadableRequestOffersDenyAlone` covers a cloud row.

**`services/settings`, `graph`**: each of the four fields is off when unset and when refused;
`sandboxCloudAccess` lists only the cloud platforms the resolver was given;
`sandboxCloudAccessSet` names the field it writes, and refuses `Cluster` and a cloud platform the
resolver was not given.

**Webview**: a cloud request draws its summary, host, details and raw request through
`VisibleText`, its two labels, and Once and Deny alone; an `unshown` sentence draws in the
body's place; `waitingRequestsOf` orders a call's cluster and cloud requests by approval id;
the confirm precedes the switch turning on.

**`app`** (`startE2E`, `testutil.RequireSandbox`), network off, the fake in `Config`'s test
field. The run has `startE2E`'s cluster, so it holds a port whatever the switch says. With the
switch turned on through `sandboxCloudAccessSet`, a sandboxed `curl` to the
fake's route reads unasked; a change waits on one request served in `sandboxActions` and reaches the
fake once approved; with the switch off the route is `401`.

**Integration gate:** the full checks in
[`../README.md`](../README.md#verification-commands), with the wire checks.

## Security

**What this step widens, with no cloud platform built:** nothing a command can reach. It changes
the code three protections rest on.

- **The relay leads to a mux, not one grant.** A routing mistake could hand a request to the
  wrong handler. Each grant authenticating for itself is what bounds that: the door is not
  trusted, and `TestAWrongRouteIsUnauthorized` pins it.
- **The engine decides a second kind of action.** A mistake could let a cluster rule or
  `Auto` permit a cloud platform's change. `Rule.Matches` refusing every non-cluster target, and the
  one verdict, are pinned by the `permissions` tests above.
- **The request draws a second shape.** Its strings are attacker-controlled text. No HTML
  sink; `VisibleText` throughout.

**What it sets up for the cloud platforms to widen**, each recorded in that cloud platform's own
step: with a switch on, a sandboxed command reads the metadata and state the user's login reads, on
the model's word, unasked, and the answer reaches the model's cloud platform; with network on a
command can send it anywhere. This is the cluster-read decision ([the sandbox is the
gate](../../adr/2026-09-28-the-sandbox-is-the-gate-for-a-sandboxed-command.md)) applied to an
account, behind a switch the cluster does not need.

**What holds.**

- A command cannot reach the sidecar's API: every route's upstream is a fixed host of its
  cloud platform, and the door serves nothing else.
- No credential is in the sandbox. The grant's token opens one cloud platform's proxy for one run.
- An approval is a click on the drawn request. No approval path or rule is added, and a
  cloud platform's action is never grantable.
- The one new way to widen what a run reaches is the switch: a settings mutation, behind the
  same boundary as every other (the CSP).
- `run/sandbox` is untouched: one relay, one port, the same profiles and golden files.

**Residuals**, each a **By decision** row:

- On macOS a run with a cloud platform and no cluster now holds a loopback port any local process
  can dial, as a run with a cluster does. Without a token every route answers `401`.
- A browser page can send the loopback authority as `Host`. It cannot send the token.
- A read of metadata is unasked, so a secret a user put in metadata no cloud platform's list names
  (a tag, a description) reaches the model.
- A background command reads the account after its turn ends, as it reads the cluster, until
  it ends or the user turns the switch off.
- No kube-context's mode reaches a cloud platform, so a read-only context does not stop a
  cloud platform's change. Each one asks.
- A request the user approves is the method, host, path, media type and body; the summary and
  details are a reading of it, and the body is there to open. A forwarded header is drawn only
  when it changes what the request does. A request with an `Unshown` body is approved on the
  cloud platform's sentence for it.

## When it lands

Task 4 updates [`docs/persisted-data.md`](../../persisted-data.md) in its own commit: the
JSON-columns row names `ActionRequest`'s `cloud` and the action's `target`, and *How a change
ships* says a key a row may lack, read as its zero value, needs no migration.

When this step is complete on `wip/cloud-proxies`:

- **ADR**: *a sandboxed command reaches a cloud platform through a proxy on its run's one relay*.
  It records the table in [*The decisions*](#the-decisions) and what was rejected: a relay per
  cloud platform, TLS for every cloud platform, a second list on the call, a type per cloud
  platform, a token per run, consent read from a login, a gate per proxy, and a kube-context's mode
  deciding a cloud platform's change.
- **`docs/security/<date>-the-runs-door.md`**, from *Security* above, naming the commit.
- **`docs/security-model.md`**:
  - The **Not built** row this spec adds stays. Each cloud platform's step takes its cloud platform
    off it for that cloud platform's **Enforced** rows, and the last removes it.
  - *The forwarder's one relay leads to the cluster proxy alone* becomes *leads to the run's
    door, which serves the cluster proxy and each cloud platform the user turned on, each behind its
    own token*, adding `TestTheDoorRoutesByHost` and `TestAWrongRouteIsUnauthorized`.
  - *On macOS a sandboxed run's forwarder holds a loopback port* names a run with a cloud platform.
  - The row on a cluster change gains: no rule or mode decides a cloud platform's action
    (`TestNoRuleReachesACloudPlatformAction`, `TestACloudPlatformActionAlwaysAsks`).
  - New **By decision** rows for the residuals above, linking the ADR.
- **`docs/TODO.md`**: *Draw every approval from an action once a second target asks* is
  removed. The security line this spec adds stays; each cloud platform's step takes its cloud
  platform off it, and the last removes it. New lines: rules, modes and durations for a cloud
  platform's actions, a read-only mode among them; a switch per chat; build the cluster grant on
  `runproxy.Grant`, so the token, the life, the limits and the queue have one copy.
- **`CLAUDE.md`**: `sidecar/internal/run/CLAUDE.md` (six leaves, a `runproxy` section,
  `Target` and the cloud platform verdict under *Permissions*, the server's new home);
  `sidecar/internal/tools/CLAUDE.md` (the door, the seam, the end order, `ActionAsker`'s
  wording); `sidecar/internal/agent/CLAUDE.md` (`sandboxActions`, the Sandbox section's `cloud` key) and
  `src/components/chat/CLAUDE.md` (`sandboxActions`);
  `sidecar/internal/services/settings/CLAUDE.md` and `src/components/settings/CLAUDE.md` (the
  switch); `src/CLAUDE.md`'s security line on cluster changes. Raise a budget in
  `.github/ci-config/claude-md-budgets.yml` only with the reason in the commit body.
- This file stays, marked built, while steps 2A to 2D cite it, and is deleted with the last of
  them; its row leaves [`README.md`](README.md) then.

## What each cloud platform step owes

The four drafts were rewritten on this step as [2A](2a-aws.md), [2B](2b-github.md),
[2C](2c-gcp.md) and [2D](2d-azure.md). Each restates none of it, and each states:

- its routes, and why its hosts are the ones they are;
- its token's slot, and its `Limits`;
- its classifier: what is a read, its class 5 list or none, which stored data is class 6,
  the metadata its cloud platform documents as holding secret values and whether each asks or is
  blanked, and what it refuses as a credential;
- its line of the switch's confirm;
- the headers it forwards, by exact name, and the detail that draws each one that changes what
  a request does;
- which bodies are `Unshown`, the sentence for each, and why the rest of the request is enough
  to approve;
- the status it answers where its client retries a `429`;
- its `CloudPlatform`, a file of `tools/bash` over its `run/<platform>proxy` leaf, added to the
  list `app` builds;
- its credential's service under `services/`, and when a credential is dropped;
- every variable it sets that another tool reads, and its cost under network;
- its paragraph of the prompt;
- its own ADR, security record and model rows.

Step 2D also adds the door's TLS route: a transport in a route's key, plain for every route
this step builds.

## Open questions

1. **Global or per chat?** The switch here is global, the least to build. Network is the
   chat's, and a chat about one cluster has no need of another cloud. Recommended: global
   first, a per-chat switch as a follow-up, recorded in `TODO.md`.
2. **A status line under each switch?** #934 wants one (the account, or what is missing).
   Finding it out runs the user's CLI when Settings opens or the switch turns, not when a
   command asks. Left out here; if wanted, it should be one gauge for all four.
3. **Is `Details` enough for a push?** #932's request names each ref with its old and new
   commit. As label and value lines that reads plainly but is not a table. If the review wants
   more, the family gains a member; nothing else here changes.
4. **The names.** `wip/cloud-proxies`, `run/runproxy`, `SandboxAction` and `sandbox.<platform>`
   are this spec's picks. GitHub is not a cloud; *cloud* here means a cloud platform behind the
   door.
