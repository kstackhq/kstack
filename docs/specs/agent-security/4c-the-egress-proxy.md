---
title: The egress proxy and the host allowlist
scope: sidecar, webview
status: Planned
---

# The egress proxy and the host allowlist

**Needs:** step 2C, whose session the handler reads through the run's token; step 3B, whose
`permissions.Action`, `Policy.Decide`, `chat_grants` and `securityconfig` rules a new host is
decided with; step 2B, whose forwarder is every run's first process. **Shares task 1 with step
4B** (the record, the action on the request, the journal's ask lock): whichever of the two lands
first does it, and the other builds on it. **Unblocks:** steps 5B and 6B.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command reaches one thing past its loopback: the cluster proxy, over the run's
socket, and only when the chat has a cluster. Nothing resolves a name inside the sandbox, and
nothing else can be dialled. `helm repo update`, `gh`, `curl` and `git clone` over HTTPS all fail
there, and the user's only way out is to switch the chat out of the sandbox (step 1B).

After this step, on macOS and Linux:

- **Every run has the socket and the forwarder**, cluster or not, and one server on the socket
  routes each request: `Host: cluster.kstack.invalid` to the cluster proxy, a `CONNECT` or an
  absolute-form request to any other host to the **egress proxy**. Both check the run's token as
  proxy credentials. One relay carries both.
- **`egress`** is a new package: a host **policy** (allow, ask and deny rules, each a host glob
  and a port), and a **handler** that checks the host, resolves the name outside the sandbox, and
  tunnels the bytes (`CONNECT`) or forwards the request (plain HTTP).
- **A host is listed** when a cluster entry's server in the kubeconfig names it, or an
  `Allow` rule of class 3 does: the user's, in `securityconfig`'s rules, or the chat's, in
  `chat_grants`. A `Deny` rule refuses a host whatever lists it, and an `AskFor` rule asks for it
  (Decisions, 5). No cloud provider's host is listed (Decisions, 4).
- **An unlisted host is class 3.** The handler builds a `permissions.Action` and asks `Decide`:
  the `CONNECT` is held while the user answers ([the note](../../notes/sandbox-credentials-and-permissions.md)'s
  *Network*: "the agent wants to reach `example.com`; allow once or always?"), and a refusal is
  a `403` the command reads. A new host asks in `Auto` mode too (Decisions, 8). A command nobody
  can ask — a background one — never asks, and is refused.
- **A host is a name.** An IP literal is refused, and a name that resolves to the machine or the
  local network is refused, except a kube API server the kubeconfig names there, by name or by
  address and port.
- **The environment** points every tool at the proxy: `HTTPS_PROXY` and `HTTP_PROXY`, with
  `NO_PROXY` empty. A tool that ignores them reaches nothing, since the sandbox has no other
  network. On macOS the one Mach service TLS verification needs is allowed by name.
- **Settings gains a Network section**: the kubeconfig's servers, the user's allowed hosts and
  the user's denied hosts.

This is the note's "mechanism that makes prompt injection mostly harmless": a hijacked command
holds no credential and reaches only the listed hosts. Every host is a tunnel the proxy cannot
see into, and the command holds no credential to send through it: a request to
`api.github.com` goes with no token.

## What is not in this step

- **No credential for any host.** The cluster proxy is the one place a credential is put on a
  request; a tunnelled connection carries whatever the tool sends, and the tool holds none.
- **No grant offered from a refusal.** A refused host is recorded and drawn as a line of the
  call's disclosure, as a refused cluster write is; step 5B adds the grant that resolves it.
- **No monitor.** Step 6B builds the session and its host policy; `NoPrompts` is step 5A's.
- Nothing changes on Windows: a command there runs outside the sandbox and reaches no proxy.

## Design

### 1. One server, one relay

`sandboxedRunFor` in `tools/bash/bash.go` asks the sandbox for a port and starts the run's server
for every run, not only one with a cluster. The Workspace policy always has its one relay, the
port to the run's `proxy.sock`. `Policy.Check` keeps refusing more than one relay: one server on
the socket serves every proxy.

`startProxy` in `tools/bash/proxy.go` serves `route(cluster, egress)` instead of the grant alone.
The route reads the request's host with any port stripped:

| Request | Goes to |
| --- | --- |
| host `kubeproxy.Host` (`cluster.kstack.invalid`), origin-form or absolute-form | the `kubeproxy.Grant`, as today; a run with no cluster has none and answers a 503 *kstack: this chat has no cluster* |
| `CONNECT` to the cluster host | a 400 *kstack: not a proxy request*: the cluster is reached over plain HTTP |
| `CONNECT host:port` to any other host | the egress handler |
| an absolute-form request (`GET http://host/path`) to any other host | the egress handler |
| anything else | a 400 *kstack: not a proxy request* |

The run's kubeconfig names `http://cluster.kstack.invalid` with a `proxy-url`, so `kubectl` sends
absolute-form requests to the cluster host, and the route sends them to the grant by their host.

**The token** is minted once per run, by `egress.NewToken` the way `NewGrant` mints it today,
and handed to both: the grant takes it rather than minting its own. Both compare
`Proxy-Authorization` to it in constant time. The egress handler answers a missing or wrong
token with a 407 and `Proxy-Authenticate: Basic realm="kstack"`, so a tool reads the proxy's
refusal and not the origin's; the cluster proxy keeps its 401.

**The run's end.** `runProxy` holds the grant (nil for a run with no cluster) and the handler.
`end` ends the grant when there is one, then the handler — which cancels its context and closes
every hijacked connection itself, since `http.Server.Close` does not — closes the server and the
socket, then waits for both.

### 2. The `egress` package

`egress/` imports `permissions`, `session`, `publicip` (§5) and the standard library, and no
proxy or tool package: the asker it calls is its own interface, which Bash adapts.

```go
// Request is a host held for the user, or decided with nobody asked.
type Request struct {
	Action    permissions.Action
	Grantable bool
	Rules     struct{ Command, Chat string } // the lines of CommandRule and GrantRule
}

// Answer is the user's: approved, and for how long.
type Answer struct {
	Approved bool
	Duration string // once, command, chat or always
}

// Asker puts a host to the user, and records one decided with nobody asked.
type Asker interface {
	Ask(ctx context.Context, r Request) (Answer, error)
	Record(ctx context.Context, r Request, d permissions.Decision, reason string) error
}
```

The rest:

```go
// Source is where a host rule came from.
type Source string

const (
	Kubeconfig Source = "kubeconfig" // a kube context's API server
	User       Source = "user"       // a class 3 rule in securityconfig
	Chat       Source = "chat"       // a class 3 rule in chat_grants
)

// HostRule is one host a run may, or may not, reach. Host is a glob over the
// lowercase ASCII name ("*.example.com"; one leading "*." at most), "" for
// every host, or, on a Kubeconfig rule alone, an IP address, which matches
// only itself. Port 0 is any port.
type HostRule struct {
	ID     string
	Host   string
	Port   int
	Source Source
}

// HostPolicy is a run's allowlist. Deny wins over Ask, and Ask over Allow, as
// in permissions.Decide.
type HostPolicy struct {
	Allow, Ask, Deny []HostRule
}

// Match answers what the policy says of host and port: Denied, Asked or
// Listed with the rule, or Unlisted.
func (p HostPolicy) Match(host string, port int) (HostMatch, HostRule)

// KubeServer reports whether a Kubeconfig rule in Allow names host and port:
// the one test of §5's exception, whatever rule Match answered with.
func (p HostPolicy) KubeServer(host string, port int) bool

// Resolver looks a name up outside the sandbox. net.DefaultResolver is one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Handler is one run's egress proxy.
type Handler struct {
	Token    string                         // the run's, as proxy credentials
	Hosts    func(context.Context) HostPolicy        // the run's allowlist, read per request
	Policy   func(context.Context) permissions.Policy // the session's, for the run's context
	Asker    Asker                                   // nil: nobody can be asked (§4)
	Resolver Resolver
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error) // a net.Dialer's, or a test's
	Tunnels  int                            // open at once; maxTunnels (32) in production
	// commandRules is what an approval allowed for the rest of the command,
	// under mu.
	mu           sync.Mutex
	commandRules []permissions.Rule
}
```

By step 2C's leaf rule `session` imports no proxy, so `HostRule`, `HostPolicy` and their methods
are declared in `session` and `egress` aliases them (`type HostRule = session.HostRule`).

`ServerRule(server string) (HostRule, bool)` turns a kubeconfig `server` URL into a rule on its
host and port (443 or 80 when the URL names none), source `Kubeconfig`, false for a URL that does
not parse. A server at an IP address gives a rule on that address in `netip`'s form, brackets
stripped.

**The handler**, for a `CONNECT host:port` and a plain absolute-form request alike:

1. The token, else 407.
2. The host, lowercased with a trailing dot dropped and brackets stripped: a 403 when it is
   empty, over 253 bytes, or neither an IP literal nor a name of ASCII letters, digits, `-`, `_`
   and dots. So a host holds no glob character, and a rule written from it (§4) matches it
   alone.
3. `Hosts(ctx).Match(host, port)`: `Denied` is a 403 naming the rule.
   - **An IP literal** (`netip.ParseAddr` accepts it) goes on only when `KubeServer` names that
     address and port and `Match` answers neither `Denied` nor `Asked`. Anything else is a 403: an
     address is never asked, since an answer would write a rule for it, so a rule that asks for
     an address refuses it.
   - **A name** that is `Listed` goes on. `Asked` and `Unlisted` are class 3 (§4), which go on
     only when the decision is `Allowed`.
4. `Resolver.LookupNetIP(ctx, "ip", host)`: no answer is a 502 *kstack: <host> does not
   resolve*; every address is checked (§5), and one that fails is a 403 with no retry on the rest.
   An IP literal is its own one address and is not looked up.
5. The dial goes to the addresses checked, in order, never to the name again, so the check and
   the dial agree; a slot from `Tunnels`, else a 429 with no `Retry-After`.
6. A `CONNECT` answers `200 Connection Established`, hijacks the connection and copies both ways,
   half-closing each side as the other ends, as the forwarder's `relay` does. A plain request
   goes through an `httputil.ReverseProxy` over a transport whose `DialContext` is pinned to the
   addresses checked, `Proxy-Authorization` and the hop-by-hop headers dropped, `FlushInterval:
   -1`, no redirect followed: a redirect goes back to the tool, whose next request is checked.

The sandbox does no DNS, today and after this step: Linux's namespace has no resolver and macOS's
`com.apple.dnssd.service` stays refused. The sidecar resolves, and the tool learns an address only
by connecting to it. Only a refusal is logged, at debug, with the host and the reason.

### 3. Host rules and the allowlist's sources

**A host rule is a `permissions.Rule` of class 3.** `permissions.Rule` and `permissions.Action`
gain two fields:

```go
Host string `json:"host,omitempty"` // class 3: a glob over the name, "" for any host
Port int    `json:"port,omitempty"` // class 3: 0 for any port
```

`Rule.Matches` compares them as it compares the cluster fields: `Host` through `Match`, unset
matching anything; `Port` equal, 0 matching anything. A cluster action holds neither, so a cluster
rule, which holds neither, matches it as today. `Rule.Line()` spells a host rule *Allow new host
charts.example.com:8443*, or *Deny new hosts \*.example.com*, the port only when it is set.

Every always host rule is in `securityconfig`'s `Rules`, beside the cluster rules: there is no
second list. `ruleClasses` gains class 3, and `ruleRefusal` its case — `Host` a glob over a name as step 2 above reads
one, or empty; `Port` 0 to 65535; no `Context`, `Namespace`, `Verb`, `Group` or `Kind` — and the
cluster classes' case refuses a `Host` or a `Port`. A host rule names no context: a host is the
machine's, not a cluster's. (Step 4D adds the folder classes' case beside this one; whichever
lands second adds its own.)

**Unreadable rules refuse hosts too.** `permissions.Refused` is a class 4 `Deny`, which matches no
host. `permissions.RefusedHosts`, a class 3 `Deny` with every field unset and its own reserved id
(`refused-hosts`), joins it: `grantsFor` and `Store.Rules()` append both wherever they append
`Refused` today, and the rules check refuses both ids in the file. So a chat whose grants or
whose file's rules cannot be read reaches no host but through the cluster proxy.

**`Session.Hosts`** is a field this step adds:

```go
// Hosts is the run's allowlist, read on every request, so a rule written or a
// context added mid-run applies to the command's next connection.
Hosts func(context.Context) HostPolicy
```

`Narrow` copies it, as it copies `Policy`. `chatsvc` sets it to `hostsFor(chatID)`:

| Source | Read from | Becomes |
| --- | --- | --- |
| `kubeconfig` | `kubeconfig.Service.Get()`, while its second answer says the file was read: every entry of `Clusters`, referenced by a context or not | `ServerRule` of its `Server`, in `Allow` |
| `chat` | `grantsFor(chatID)`, every class 3 rule | by its effect: `Allow`, `Ask` or `Deny` |
| `user` | `security.Rules()`, every class 3 rule | the same |

`egress.FromRule(rule, source)` is the one conversion. So a permission rule outranks every
source: a `Deny` refuses a kubeconfig server, and an `AskFor` sends one to `Decide` as if it were
unlisted (Decisions, 5). The policy is read per request, never cached.

### 4. An unlisted host is class 3

For `Unlisted` and `Asked` the handler builds the action:

```go
permissions.Action{
	Class: permissions.NewHost, Host: host, Port: port,
	Summary: "Reach charts.example.com", // "Reach charts.example.com:8443" off 443
}
```

The action carries no verb: a `CONNECT` and a plain `GET` to one host are the same reach.

The handler decides it itself: `Policy.Decide` under `Handler.Policy(ctx)` with its own command
rules appended after the session's. Bash sets `Handler.Policy` to the session's `Policy` for the
run's cluster context (the record's `KubeContext()`, the one its cluster grant names; `""` for a
run with no cluster, whose mode is whatever `ModeFor` answers for no context). A host rule sets no
context, so it matches under any. The same policy answers `permissions.Grantable`, and the
request's rule lines are `CommandRule(act).Line()` and `GrantRule(act).Line()`.

| Decision | What happens |
| --- | --- |
| `Allowed` (a command rule) | resolve and tunnel |
| `Prompted` | the `CONNECT` is held and put to the user through the asker; the command blocks as it does on a cluster write, its `timeout` running |
| `Denied` (`ReadOnly`, or the user's no) | a 403 whose body is *kstack: charts.example.com is not allowed: <reason>*; curl prints *Received HTTP code 403 from proxy after CONNECT* |

A listed host never reaches `Decide`, so a user's or a chat's `Allow` rule lists a host under any
mode (Decisions, 3). The only `Allow` rules `Decide` sees for a host are the command's own.

**`Auto` asks for a new host.** Step 3B's `Decide` allows every class but 5 under `Auto`. This
step narrows that to every class but 3 and 5: `Auto` stops asking about cluster writes, not about
where a command sends what it read (Decisions, 8).

**Nobody to ask.** A handler with no asker — a background command's, or a runtime with none —
refuses an unlisted host at once, before `Decide`, as the cluster proxy refuses its writes: there
is no call open to record against.

**Asking.** The handler asks through its `Asker`, which Bash's `runtimeAsker` adapts to the
runtime's `ActionAsker` (task 1, shared with step 4B): a `tools.ActionRequest` with the action,
`Grantable`, the rules' words, and no `Write`. The journal's `askMu` keeps the egress proxy's asks and the cluster
proxy's off the journal at once, and puts one in front of the user at a time.

**An approval holds for the rest of the command.** One command opens many connections to a host
— `helm repo update` several, `git clone` a few — and asking for each would ask the same thing
again. So the handler treats every approval as a `Command` one: on an approved answer it appends
`permissions.CommandRule(act)` (the host and port, literal) to its own command rules, under its
lock, and every later connection of the command to that host and port is `Allowed`. The request
for a host offers no *Approve once* for that reason:

- **when step 4B has landed**, it offers *Allow for this command*, *Allow for this chat*, *Always
  allow* and *Deny*; a `Chat` or `Always` answer writes `GrantRule(act)`, an `Allow` of class 3
  scoped to the host and port, each literal, so a grant for `example.com:443` reaches port 443
  alone;
- **when it has not**, `approvalDecide(id, approve)` is the landed boolean, so it offers
  *Allow for this command* and *Deny*, and the user lists a host for good in Settings.

The exception is a host an `AskFor` rule names. `Grantable` is false for it, since `Decide` reads
`AskFor` ahead of every `Allow`, so a command rule would never be read: its request offers
*Approve once* and *Deny*, and each connection asks, which is what the rule says.

A client's own connect timeout can end the wait before the user answers — curl's is 300s by
default — and the command then fails; a `Chat` or `Always` answer still applies to the next.

**The record** is task 1's: an `approvals` row of kind `action` with the action, for every
request put to the user, and for the first decision made with nobody asked of each host and port
in a run. Later connections the same command makes to a host it already decided, or allowed by
the command's rule, are not recorded again, so a `git clone` is one line. A listed host is not
recorded: reaching it is class 1. A host an `AskFor` rule names is asked and recorded, listed or
not.

**The request and the line.** A host's request rides `ToolCall.clusterWrites` as a
`ClusterWrite` whose `action.class` is `NewHost` and whose request fields are empty — step 5A
renames the list. `PermissionAction` gains `host: String!` and `port: Int!`. `ApprovalRequest`
draws, for a change whose class is `NewHost`: *Reach this host?*, then the host and, off 443, the
port in mono through `VisibleText`, `break-all`, then the buttons above with the rule's line under
each allow button. The group's `aria-label` reads *Host awaiting approval*. In the call's
disclosure a settled one is a line as a settled write is — `Reach charts.example.com`, through
`VisibleText`, tagged as a write is, `refused` with its reason included — so a refused host is on
screen; step 5B adds the grant under it.

### 5. Addresses

`publicip.Public(addr netip.Addr) bool` is the one check: `webfetch`'s `Public`, moved with its
ranges into a leaf `internal/publicip`, which `webfetch` and `egress` both call. It refuses
loopback, private, link-local, multicast, unspecified, CGNAT and the reserved ranges, and an IPv6
form that carries one of them. A listed name that resolves to a refused address is a 403
*kstack: <host> resolves to a local address*, with one exception: a name `KubeServer` answers for
may resolve anywhere, since kind, minikube and Docker Desktop put an API server at
`127.0.0.1:<port>`. The exception is a `Kubeconfig` rule's, so only that host and that port get
it, whatever rule `Match` answered with. A kubeconfig that names the server by address, as kind's
does, gets the same exception through its address rule (§2, step 3).

What a command reaches there is what the server serves without a credential (`/version`,
`/healthz`, the OIDC discovery paths); the cluster proxy is still the way to the chat's cluster.
Go's proxy-from-environment never sends a loopback address through a proxy, so a Go tool dials
`127.0.0.1` itself and the sandbox refuses it; the exception serves the tools that do send it, and
a name like `kubernetes.docker.internal`.

### 6. The environment

`sandboxedRunEnv` in `tools/bash/env.go` gains these rows:

| Variable | Value |
| --- | --- |
| `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`, `http_proxy` | `http://kstack:<token>@127.0.0.1:<port>` |
| `NO_PROXY`, `no_proxy` | `` (empty: nothing bypasses) |
| `ALL_PROXY` | unset, and a name `neverEnv` (`sandbox/env.go`) lists |

The lowercase spellings are for curl, which reads `http_proxy` in lowercase alone, and the
uppercase for Go. `safe.Redact` blanks the token wherever a command prints its environment, as it
does a URL's userinfo today. SSH is not routed: a `git` remote over SSH fails in the sandbox with
no route, and the prompt says to use HTTPS.

### 7. macOS: TLS trust in the sandbox

A Go program (`helm`, `gh`, `kubectl`) and the system `curl` verify a certificate through
Security.framework, which asks the user session's trust daemon over Mach. Today's profile refuses
that lookup (`refusedServices` names `com.apple.trustd` and `com.apple.trustd.agent`), so every
verification fails with `OSStatus -26276`. Checked on macOS 27.0.1: the service looked up is
**`com.apple.trustd.agent`**; allowed alone, a chain verifies; `com.apple.trustd`, the system
daemon, is never looked up.

So `com.apple.trustd.agent` leaves `refusedServices` and joins the profile's fixed `mach-lookup`
rule in `profile_darwin.sb`, beside `opendirectoryd.libinfo`, with a comment naming why. It is a
fixed rule of the profile, not a field of `Policy`: every run verifies TLS the same way. The
Keychain's services (`com.apple.trustd`, `com.apple.SecurityServer`, `com.apple.securityd*`,
`com.apple.secd`, `com.apple.security.agent`) stay refused: a verification asks for a verdict and
reads no key. On Linux nothing changes: `/etc/ssl` is readable (step 2A).

### 8. Settings: Network

`network-settings.tsx`, a Network section in the Settings dialog, drawn while `sandbox.available`
is true, over `useNetworkHosts()` in `src/lib/network-hosts.tsx`, the one reader of the query and
mutations:

- **Allowed hosts**: one row per kubeconfig server and per user `Allow` rule, the host in mono
  through `VisibleText` (a kubeconfig's server name is the user's file's text), the port when set
  and not 443, and the source as a tag: *from your kubeconfig*, *added by you*. A kubeconfig row
  is read-only; a user row has Remove.
- **Add**: a host field, an optional port field and a *Deny* checkbox; disabled in flight, a
  refusal's reason under the field, and one line above it: *Sandboxed commands can send what
  they read to a host you allow.*
- **Denied hosts**: the user's `Deny` rules, each with Remove, under *A denied host is refused
  even when a rule or your kubeconfig lists it.*
- Under the section: *Sandboxed commands reach these hosts through Kstack's proxy. An unlisted
  host asks you, in the chat.*

The Permissions section's line for `Auto` gains *A new host still asks.*

A user `AskFor` host rule is not a row here: the Permissions section lists it with the other
rules. That section lists every rule, so a host rule shows there too, by its line; a host rule
the file holds but Kstack cannot read is held and discarded there, as any rule is. The
Permissions section's Add form keeps offering the cluster classes alone; a host is added here.

The wire:

```graphql
enum NetworkHostSource { Kubeconfig User }
enum GrantDuration { Chat Always }

type NetworkHost {
  "A user rule's id, or `kubeconfig:` and the cluster entry's name."
  id: String!
  host: String!
  "0 for any port."
  port: Int!
  source: NetworkHostSource!
  deny: Boolean!
}

type PermissionRule {
  # as landed, then:
  host: String!
  port: Int!
}

input PermissionRuleInput {
  # as landed, then:
  host: String! = ""
  port: Int! = 0
}

extend type Query {
  "The kubeconfig's servers and the user's class 3 Allow and Deny rules. Empty on a machine with no sandbox. A chat's rules are not listed here."
  networkHosts: [NetworkHost!]!
}

extend type Mutation {
  "Adds a class 3 Allow or Deny rule. Refused KSTACK_VALIDATION_ERROR: not a name or a glob over one, an IP, a port out of range, or already listed."
  networkHostAdd(host: String!, port: Int, deny: Boolean!): [NetworkHost!]!
  "Removes a user rule by id. Refused for a kubeconfig row."
  networkHostRemove(id: String!): [NetworkHost!]!
  "Allow one host for a chat (a chat_grants rule; chatID required) or always (a securityconfig rule; chatID ignored), host and port literal. What step 5B's denial line calls."
  networkHostGrant(chatID: ChatID, host: String!, port: Int, duration: GrantDuration!): [NetworkHost!]!
}
```

`GrantDuration` is shared with step 4D's `folderGrant`: whichever of the two lands first adds the
enum, and the other uses it. A `port` left out is 443 on `networkHostGrant` and 0 (any) on
`networkHostAdd`. A `Chat` grant with no `chatID`, or on a chat that is gone, is
`KSTACK_VALIDATION_ERROR` or `KSTACK_RECORD_NOT_FOUND`. Each mutation answers the whole list, so
the section redraws from one result. `graph.Resolver` gains `NetworkHosts`, an `egress.Sources`
`app` wires: the kubeconfig service, the security store and `chatsvc`'s grant write. On a machine
with no sandbox (step 1B's `SandboxStatus`) the query answers an empty list and the mutations are
refused.

### 9. The prompt

`prompts/sandbox.md` replaces *The sandbox reaches no network* with: the sandbox reaches the
network through Kstack's proxy, which lets a command reach the cluster's API servers and the
hosts the user allowed; a host not on that list waits for the user, and an allowed one stays
allowed for the rest of the command; a refused one comes back `403` from the proxy, which is the
user's decision; a background command reaches only the listed hosts; the cluster is the one host
a command reaches with a credential, so `gh`, `aws`, `gcloud` and `az` have no login in the
sandbox and a private registry takes no token; a tool must read `HTTPS_PROXY`, and `git` over SSH
does not work in the sandbox — use an HTTPS remote.

## Decisions this step asks for

1. **One relay and one server, not a second relay.** The note's proxy is one process, and the
   run already has a socket to it. A second socket would be a second listener, a second token
   check and a second `Relay` for both compilers, for no boundary the first does not draw.
   Recommended.
2. **An IP literal is refused, and a name that resolves locally is refused, unless it is a kube
   API server the kubeconfig names, by name or by address and port.** The note's "loopback stays
   open so `kubectl port-forward` works" would open the proxy to every local service — Ollama,
   Docker's API, a dev database, the sidecar's own socket — to a command the model runs unasked,
   and port-forward stays refused anyway (the note's *Where this meets the code*, 7). A local API
   server is the one local thing a Kubernetes tool needs. Recommended.
3. **Reaching a listed host is class 1, whatever the mode.** Class 3 is a *new* host, so
   `ReadOnly` refuses the unlisted and not the listed. Recommended; the alternative makes
   read-only contexts unable to `helm repo update`.
4. **No cloud host is listed by Kstack.** A tunnel to `*.amazonaws.com` carries whatever a command
   sends, and a bucket that takes anonymous uploads is an exfiltration path that needs no
   credential at all. That is the note's own second principle: a tool that talks to an upstream
   directly is outside both credential isolation and permission enforcement, and no proxy
   classifies what goes to a cloud host. So a `CONNECT` to one is class 3, which asks, and the
   user's answer is the user's to judge. The kubeconfig's own servers are listed, a managed
   cluster's endpoint on a cloud host included: that one host and port, which the user's own
   file names. Recommended.
5. **A permission rule on a host outranks the listing.** A class 3 `Deny` rule refuses a host
   whatever lists it, and an `AskFor` rule asks for it, under `Decide`'s own order, so a
   `ReadOnly` context refuses a host an `AskFor` rule names. The user's rules are the one place a
   user says *never this host* or *always ask*, and a built-in source must not overrule them.
   Recommended.
6. **Host rules live in `securityconfig`'s rules, with no separate host list.** A host the user
   allows in Settings and one allowed always from a request are the same rule, read, held and
   discarded the same way. Recommended.
7. **A host's approval holds for the rest of the command.** A connection is not a unit the user
   thinks in, and a request per connection would ask the same thing many times over. Recommended.
8. **A new host asks in `Auto` mode.** `Auto` is the user trusting the agent with the cluster;
   a new host is where a hijacked command would send what it read, and allowing it unasked would
   make the allowlist decorative in the mode most users will pick. Recommended, as the stricter
   start: loosening it later takes one condition in `Decide` and a line in Settings. The alternative
   keeps step 3B's `Decide` as it is, and states every host reachable unasked under `Auto` as a
   residual.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | Step 4B's task 1, if 4B has not landed it | see step 4B | — | Planned |
| 2 | `Rule.Host` and `Rule.Port`, their match and line; `Auto` asks for class 3; `RefusedHosts`; class 3 in `ruleClasses` and `ruleRefusal`; `HostRule`, `HostPolicy` and `Session.Hosts` | `permissions/`, `securityconfig/permissions.go`, `session/session.go`, their tests | — | Planned |
| 3 | `publicip`; `egress`: `Match`, `KubeServer`, `ServerRule`, `FromRule`, the handler, its asker and its command rules | `publicip/`, `tools/webfetch/public.go`, `egress/policy.go`, `egress/handler.go`, `egress/sources.go`, their tests | 1, 2 | Planned |
| 4 | `hostsFor`; the grant writes, and `addGrant` and its statement unless 4B or 4D added them | `chatsvc/grants.go`, `chatsvc/statements.go`, their tests | 3 | Planned |
| 5 | Every run has the relay; the shared token; `route`; the handler's policy and asker; the run's end; the environment | `tools/bash/bash.go`, `tools/bash/proxy.go`, `tools/bash/env.go`, `kubeproxy/kubeproxy.go`, `sandbox/env.go`, `sandbox/testdata/args_linux_no-cluster.golden`, `sandbox/testdata/profile_darwin_no-cluster.golden`, their tests | 4 | Planned |
| 6 | macOS: `com.apple.trustd.agent` in the profile; the trust test | `sandbox/sandbox_darwin.go`, `sandbox/profile_darwin.sb`, `sandbox/testdata/*.golden`, `sandbox/sandbox_darwin_test.go`, `sandbox/testdata/chain.pem` | — | Planned |
| 7 | The wire, the resolvers, codegen | `sidecar/graph/schema.graphqls`, `graph/`, `app/app.go`, generated code, `src/gql/` | 4 | Planned |
| 8 | `useNetworkHosts`, the Settings section; the host request and its disclosure line | `src/lib/network-hosts.tsx`, `src/components/widgets/network-settings.tsx`, `settings-dialog.tsx`, `src/components/widgets/chat-transcript.tsx`, `src/lib/chats.tsx`, their tests | 7 | Planned |
| 9 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 5 | Planned |
| 10 | Docs, per *When it lands* | see there | 1–9 | Planned |

**Order:** 1, 2 and 6 at the same time, then 3, then 4, then 5 and 7 at the same time, then 8
and 9 at the same time, then 10.

## Tests

**`egress`** (a fake resolver, `httptest` servers, a `net.Pipe` dialer where the bytes matter)

- `TestAListedHostTunnels`: a `CONNECT` to a listed host answers 200 and the bytes match both
  ways; a plain absolute-form `GET` is forwarded with `Proxy-Authorization` dropped.
- `TestAnUnlistedHostAsksAndFollowsTheDecision`: `Decide` gets the action of §4; `Allowed`
  tunnels, `Denied` is a 403 naming the reason, `Prompted` holds the `CONNECT` until the answer.
- `TestAnApprovalHoldsForTheRestOfTheCommand`: two `CONNECT`s to one host, the first approved;
  the second tunnels with no ask, and one to another port asks; under an `AskFor` rule on the
  host, the second asks again.
- `TestAHostIsRecordedOncePerRun`: three connections to an unlisted host, the first approved,
  write one record.
- `TestADeniedHostIsForbidden`: a `Deny` rule wins over an `Allow` of every source.
- `TestAPermissionRuleOutranksTheListing`: a class 3 `Deny` rule on a host the user listed is a
  403 naming the rule, and an `AskFor` rule on a kubeconfig server sends it to `Decide` and holds
  the `CONNECT`; `FromRule` puts each effect in its list, and `Match` answers Deny over Ask over
  Allow.
- `TestAHostIsAName`: `*.example.com`, `a?b.example.com` and `[x].example.com` are a 403,
  never asked, so no answer writes a rule wider than the host drawn.
- `TestAnIPLiteralIsRefused`: IPv4, IPv6 and bracketed, never resolved and never asked; a
  `kubeconfig` rule from `https://127.0.0.1:6443` lets `127.0.0.1:6443` through with no lookup,
  while `127.0.0.1:6444` and `[::1]:6443` are refused, and an `AskFor` rule on `*` refuses it.
- `TestALocalAddressIsRefused`: a listed name resolving to each refused family is a 403, and a
  `kubeconfig` rule on a name resolving to `127.0.0.1:6443` tunnels, also when a user `Allow`
  rule on the same name is the one `Match` answers with. The note's second invariant kept: the
  proxy does not open the machine.
- `TestTheDialGoesToTheAddressChecked`: the dialer sees the resolved address, never the name.
- `TestTheTokenIsRequired`: no token and a wrong one are a 407; `TestTunnelsAreBounded`;
  `TestEndClosesTheTunnels`: every hijacked connection is closed and `Wait` returns.
- `TestMatchGlobsAndPorts`: `*.example.com` matches `charts.example.com` and not
  `example.com`; port 0 matches any. `TestServerRuleReadsAKubeconfigServer`: host, port, a
  default port, an IP address, a URL that does not parse.

**`publicip`**: `webfetch`'s address tests move with it, unchanged.

**`permissions`**: `TestAHostRuleMatchesItsHostAndPort`, and a cluster rule never matches a host
action; `TestAHostRuleLine`; `TestAutoAsksForANewHost`: under `Auto` a class 3 action is
`Prompted` and a class 4 one `Allowed`.

**`securityconfig`**: `TestAHostRuleIsShapeChecked`: a bad glob, a port out of range, and a host
rule naming a context are refused with their reasons, and a cluster rule naming a host is too;
`TestHeldRulesRefuseEveryHost`: with `rules` held, `Rules()` holds `RefusedHosts`.

**`chatsvc`**: `TestTheSessionsHostsJoinEverySource`: a kubeconfig reload seen by the next read,
a chat's class 3 rule listed as `chat`, and a `Deny` or `AskFor` rule of the chat's or the
file's in the policy's `Deny` or `Ask`; an unreadable grant row refuses every host;
`TestNetworkHostGrantWritesWhereTheDurationSays`, a `Chat` grant with no chat refused;
`TestAGrantKeepsTheLiteralHost`: a grant for `example.com:443` matches port 443 and not 8443.

**`bash`**

- `TestEveryRunHasTheRelay`: over a fake sandbox, a run with no cluster has one relay and a
  server; `Host: cluster.kstack.invalid` there is a 503, and the run ends cleanly.
- `TestTheRouteSplitsTheHost`: the cluster host, with and without `:80` and in absolute form,
  goes to the grant; a `CONNECT` to another host to egress; anything else is a 400.
- `TestTheGrantAndTheHandlerShareTheToken`.
- `TestABackgroundCommandNeverAsksForAHost`: an unlisted host is a 403 and the asker is never
  called; and a run with no asker the same.
- `TestNoProxyIsEmpty`, new, and `TestTheSandboxedEnvironmentIsFixed`, extended with §6's rows
  and `ALL_PROXY` on the never-list.
- `TestAListedHostIsReachedThroughTheProxy`, through the real sandbox on CI: `curl -sS
  --insecure` to an `httptest` TLS server listed by name under a fake resolver reads its body,
  and `curl` to an unlisted one prints the 403 line.

**`sandbox`**

- `TestTLSVerifiesInTheSandbox`, in `sandbox_darwin_test.go`: a test program inside the sandbox
  verifies `testdata/chain.pem` (a captured leaf and intermediate) against the system roots at a
  `CurrentTime` inside its validity, and fails to under a profile without the service. On Linux
  the same program reads a non-empty `x509.SystemCertPool()`. (On macOS the pool holds no
  subjects: verification is Security.framework's, so the test verifies rather than counts.)
- `TestTheKeychainIsDenied` still holds, and `TestOnlyTheRunsPortAndSocketAreReached` and
  `TestNoListenerOutsideIsReachable` too: the relay is still the run's only way out.

**Webview** (`network-settings.test.tsx`, `network-hosts.test.tsx`, `chat-transcript.test.tsx`):
the rows with their sources and tags, Remove only on user rows, Add with a port and with Deny,
each mutation, a refusal's reason, and nothing while `sandbox.available` is false; the host
request's heading, host and port, its buttons (no *Approve once*) and `aria-label`; a settled
host's disclosure line with its tag and reason.

## Security

**Widened.** Before this step a sandboxed command reached the chat's cluster and nothing else.
After it, a command the model runs unasked reaches every listed host — an API server's
unauthenticated paths, whatever the user added — with no credential in hand, and a host the
user allowed on a request, for the rest of that command or longer. What holds it: the sandbox
still has no network but the relay, so every connection passes the handler; the handler resolves
and dials, so the sandbox never learns an address it did not connect to; an unlisted host is the
user's decision, recorded and on screen; the user's `Deny` and `AskFor` rules hold over every
source (Decisions, 5), and a rules file Kstack cannot read refuses every host; a background
command never asks and is refused; the asks of both proxies reach the user one at a time; and
the machine's other local services stay closed (Decisions, 2).

**Residuals.** A listed host that takes an upload with no credential is an exfiltration path
for what a command has read. No cloud host is listed by Kstack (Decisions, 4); a host the user
adds is the user's to judge, and the Settings section says so. The names a command asks for reach
the sidecar's resolver, so a hijacked command can leak a few bytes per lookup to whoever runs the
machine's DNS; a listed name is resolved before its address is refused. A `Deny` rule is a glob
over a name, not an address, and an address reaches the proxy only as a kubeconfig's server. An
approved host stays open for the rest of the command, to whatever the command sends it. A tunnel
to a listed host carries whatever TLS name and `Host` the command sends, so on a shared front end
— a CDN, a cloud load balancer — a command can reach another tenant behind the listed name; the
proxy checks the name it dials, not the one inside the tunnel.

The record, `docs/security/<date>-the-egress-proxy.md`, argues both.

## When it lands

- **The security record** above, and an ADR: one relay serves every proxy; an IP literal and a
  local address are refused, a kube API server excepted; a listed host is class 1; a permission
  rule outranks the listing; host rules are permission rules; an approval holds for the command;
  no cloud host is listed by Kstack.
- **`security-model.md`**: the modes row says `Auto` asks for a new host; the network row says the relay carries the cluster proxy and the
  egress proxy, with the tests; a row for the allowlist and its sources; the address refusal as
  its own row; a row that no credential rides to any host but the cluster; the macOS Mach
  services row gains `trustd.agent`.
- **`sidecar/CLAUDE.md`**: `egress`, `publicip`, the route on the run's socket, every run's relay
  and its token, the allowlist's sources, host rules and `RefusedHosts`, `Session.Hosts`, the
  environment rows, `refusedServices` less the agent.
- **Root `CLAUDE.md`**, the Settings dialog and *Chat*: the Network section and
  `useNetworkHosts`; the host request and its line.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the sandbox's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS and on Linux: ask for `helm repo add bitnami
https://charts.bitnami.com/bitnami` and read the request *Reach this host?* naming
`charts.bitnami.com`; allow it for the chat and read `helm repo update` run with no request; ask
for `curl https://example.com`, deny it, and read the 403 line in the output and the refused line
in the call's disclosure; ask for `curl http://127.0.0.1:11434` and read it refused with no
request; ask for `curl https://api.github.com/zen` and read a request for `api.github.com`, then
an answer with no token behind it; `git clone` of an SSH remote fails and the model names HTTPS;
Settings lists the kubeconfig's servers.
