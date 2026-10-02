---
title: The egress proxy and the host allowlist
scope: sidecar, webview
status: Planned
---

# The egress proxy and the host allowlist

**Needs:** step 2C, whose session the handler reads through the run's token; step 3B, whose
`permissions.Action`, `Decide`, `chat_grants` and `securityconfig.Settings` a new host is decided
with; step 2B, whose forwarder is every run's first process. **Unblocks:** steps 5B and 6B.

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
  proxy credentials, the one already in the run's kubeconfig. One relay carries both.
- **`egress`** is a new leaf package: a host **policy** (allow and deny rules, each a host glob,
  a port and a source), and a **handler** that checks the host, resolves the name outside the
  sandbox, and tunnels the bytes (`CONNECT`) or forwards the request (plain HTTP).
- **The allowlist has sources**: every kube context's API server from the kubeconfig, the user's
  entries, and the chat's rules. No cloud provider's host is listed (Decisions, 4). A `Deny` entry
  wins over every source, and so does a permission rule: a class 3 `net` rule that denies a host refuses it, and
  one that asks for it asks, whatever lists it (Decisions, 5).
- **An unlisted host is class 3.** The handler builds a `permissions.Action` and asks `Decide`:
  the `CONNECT` is held while the user answers ([the note](../../notes/sandbox-credentials-and-permissions.md)'s
  *Network*: "the agent wants to reach `example.com`; allow once or always?"), a rule or `Auto`
  lets it through, and a refusal is a `403` the command reads. A session nobody can ask never asks.
- **A host is a name.** An IP literal is refused, and a name that resolves to the machine or the
  local network is refused, except a kube API server the kubeconfig names there, by name or by
  address and port.
- **The environment** points every tool at the proxy: `HTTPS_PROXY` and `HTTP_PROXY`, with
  `NO_PROXY` empty. A tool that ignores them reaches nothing, since the sandbox has no other
  network. On macOS the one Mach service TLS verification needs is allowed by name.
- **Settings gains a Network section**: the hosts with their sources, the user's entries and the
  deny list.

This is the note's "mechanism that makes prompt injection mostly harmless": a hijacked command
holds no credential and reaches only the listed hosts. Every host is a tunnel the proxy cannot
see into, and the command holds no credential to send through it: `gh` reaches `api.github.com`
and gets a 401.

## What is not in this step

- **No credential for any host.** The cluster proxy is the one place a credential is put on a
  request; a tunnelled connection carries whatever the tool sends, and the tool holds none.
- **No denial drawn in the transcript.** A refused host is recorded here; step 5B draws it under
  the call with the grant that resolves it.
- **No monitor.** Step 6B builds the session; this step says what its `NoPrompts` does here.
- Nothing changes on Windows: a command there runs outside the sandbox and reaches no proxy.

## Design

### 1. One server, one relay

`sandboxedRunFor` in `tools/bash/bash.go` asks the sandbox for a port and starts the run's server
for every run, not only one with a cluster. The Workspace policy always has its one relay, the
port to the run's `proxy.sock`. `Policy.Check` keeps refusing more than one relay: one is enough, as the README's shared
vocabulary says — one server on the socket serves every proxy.

`startProxy` in `tools/bash/proxy.go` serves `route(cluster, egress)` instead of the grant alone:

| Request | Goes to |
| --- | --- |
| `Host` is `kubeproxy.Host` (`cluster.kstack.invalid`) | the `kubeproxy.Grant`, as today; a run with no cluster has none and answers a 503 *kstack: this chat has no cluster* |
| `CONNECT host:port` | the egress handler |
| an absolute-form request (`GET http://host/path`) | the egress handler |
| anything else | a 400 *kstack: not a proxy request* |

Both handlers compare `Proxy-Authorization` to the run's token in constant time. The token stays
the grant's; a run with no cluster has no grant, so `egress.NewToken` mints one the same way,
and it still writes no kubeconfig: the proxy URL rides the environment (§6). The egress handler
answers a missing or wrong token with a 407 and `Proxy-Authenticate: Basic realm="kstack"`, so a
tool reads the proxy's refusal and not the origin's; the cluster proxy keeps its 401. The
server ends as today (`runProxy.end`): open tunnels are cut with the grant's context, and the
handler's `Wait` joins them before the socket goes.

### 2. The `egress` package

`egress/`, a leaf that imports `permissions` and the standard library:

```go
// Source is where a host rule came from.
type Source string

const (
	Kubeconfig Source = "kubeconfig" // a kube context's API server
	User       Source = "user"       // the user's entry, in securityconfig
	Chat       Source = "chat"       // a chat's rule, in chat_grants
)

// HostRule is one host a run may, or may not, reach. Host is a glob over the
// lowercase ASCII name ("*.example.com"; one leading "*." at most), or, on a
// Kubeconfig rule alone, an IP address, which matches only itself. Port 0 is
// any port.
type HostRule struct {
	ID     string
	Host   string
	Port   int
	Source Source
	Deny   bool
}

// Policy is a run's allowlist. Ask holds the hosts a permission rule asks for.
// Deny wins over Ask, and Ask over Allow, as in permissions.Decide.
type Policy struct {
	Allow, Ask, Deny []HostRule
}

// Match answers what the policy says of host and port: Denied, Asked or
// Listed with the rule, or Unlisted.
func (p Policy) Match(host string, port int) (Verdict, HostRule)

// Resolver looks a name up outside the sandbox. net.DefaultResolver is one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Handler is one run's egress proxy.
type Handler struct {
	Token    string                       // the run's, as proxy credentials
	Hosts    func(context.Context) Policy // the run's allowlist, read per request
	Decide   func(context.Context, permissions.Action) (permissions.Decision, permissions.Reason)
	Resolver Resolver
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error) // a net.Dialer's, or a test's
	Tunnels  int                          // open at once; maxTunnels (32) in production
}
```

`ServerRule(server string) (HostRule, bool)` turns a kubeconfig `server` URL into a rule on its host and port (443
or 80 when the URL names none), source `Kubeconfig`, false for a URL that does not parse. A
server at an IP address gives a rule on that address in `netip`'s form, brackets stripped.

**The handler**, for a `CONNECT host:port` and a plain absolute-form request alike:

1. The token, else 407.
2. The host, lowercased with a trailing dot dropped and brackets stripped: a 403 when it is
   empty, over 253 bytes, or neither an IP literal nor a name of ASCII letters, digits, `-`, `_`
   and dots. So a host holds no glob character, and a rule written from it (§4) matches it
   alone.
3. `Hosts(ctx).Match(host, port)`: `Denied` is a 403 naming the rule. An IP literal
   (`netip.ParseAddr` accepts it) goes on only when it is `Listed` by a `kubeconfig` rule on
   that address and port; any other answer is a 403, and an IP literal is never asked. For a
   name, `Listed` goes on, and `Asked` and `Unlisted` are class 3 (§4), which go on only when the
   decision is `Allowed`.
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

### 3. The allowlist's sources

`Session.Hosts func(context.Context) session.HostPolicy` is a field this step adds, a live read by
step 2C's narrowing rule. By 2C's leaf rule `session` imports no proxy, so `HostRule` and the
policy, with its `Match` method, are declared there as `session.HostRule` and
`session.HostPolicy`, and `egress` aliases them (`type HostRule = session.HostRule`,
`type Policy = session.HostPolicy`); the block above shows their shape. `chatsvc` sets it to
`hostsFor(chatID)`, built from an `egress.Sources` that `app` wires:

| Source | Read from | Rule |
| --- | --- | --- |
| `kubeconfig` | `kubeconfig.Service.Get()`, every `Clusters[*].Server` | `ServerRule` of each, so a reload is seen by the next request |
| `user` | `securityconfig.Settings.Hosts []egress.HostRule` | each entry as written, Allow or Deny; and every class 3 rule of provider `net` in `Settings.Rules`, by its effect |
| `chat` | `chat_grants` | every class 3 rule of provider `net` for the chat, by its effect |

A rule's `Scope.Host` is the glob and its `Kind` the port as decimal (`""` any), so a class 3
`net` rule and a `HostRule` say the same thing; `egress.FromRule` is the one conversion. An
`Allow` rule joins `Allow`, a `Deny` rule `Deny`, and an `AskFor` rule `Ask`. So a permission rule
outranks every source: a `Deny` refuses a kubeconfig server, and an `AskFor` sends
one to `Decide` as if it were unlisted, where the same rule makes it `Prompted` (Decisions, 5).
Settings lists a user `Deny` rule among the denied hosts; an `AskFor` rule is not a row there, and
stays in the permission rules step 3B lists. The
policy is read per request, never cached: a grant written mid-run applies to the command's next
connection, and a context added to the kubeconfig too. `Narrow` hands the subagent the parent's `Hosts`.

`securityconfig` reads `Hosts` back through its shape check as it reads `Rules`: an entry whose
host is not a glob over a name, or whose port is out of range, is left out, logged, and shown in
Settings with its reason.

### 4. An unlisted host is class 3

For `Unlisted` and `Asked` the handler builds the action and asks `Decide`:

```go
permissions.Action{
	Provider: permissions.Net, Class: permissions.NewHost,
	Scope: permissions.Scope{Host: host}, Verb: r.Method, Kind: strconv.Itoa(port),
	Summary: "Reach charts.example.com",           // "Reach charts.example.com:8443" off 443
}
```

`Handler.Decide` is what Bash builds from the session (`hostDecider` in `tools/bash/proxy.go`):
`permissions.Decide` under the session's `Policy{Mode, NoPrompts}` and `Rules`, the mode read for
the run's cluster context (its record's `KubeContext()`, the one its cluster grant names; `""` for
a run with no cluster), so a host is decided under that context's mode, then, for
`Prompted`, the runtime's asker, whose yes or no becomes `Allowed` or `Denied` with a reason
naming the user. Step 4B, in this wave, turns that asker into `ActionAsker` taking a
`tools.ActionRequest`; whichever of the two lands second adapts. Landing first, this step asks
through the landed `ClusterWriteAsker` with a request whose path is the host and port and whose
method is `CONNECT`, and the request draws the heading below with once and deny alone; landing
second, it hands an `ActionRequest` with the action and no `Write`. So:

| Decision | What happens |
| --- | --- |
| `Allowed` (a chat or always rule, or `Auto`) | resolve and tunnel; the record says `allowed` with the reason |
| `Prompted` | the `CONNECT` is held; the command blocks as it does on a cluster write, its `timeout` running; the user answers with step 4B's four answers, a *this chat* or *always* one writing an `Allow` class 3 `net` rule scoped to the host and port |
| `Denied` (`ReadOnly`, a `Deny` rule, `NoPrompts`, or the user's no) | a 403 whose body is *kstack: charts.example.com is not allowed: <reason>*; curl prints *Received HTTP code 403 from proxy after CONNECT* |

A grant with no asker, a background command's, refuses an unlisted host at once, before
`Decide`, as the cluster proxy refuses its writes (step 3B §6): there is no call open to record
against. The monitor's session sets `NoPrompts`, so a `Prompted` there is `Denied` at once. Neither
ever holds a `CONNECT`.

**The record** is step 4B's: an `approvals` row of kind `action` with the `Action` and its status
(`approved`, `denied`, `allowed` or `refused`, with the reason), so the transcript draws it as a
request while it waits and after as a line in the call's disclosure, tagged as a cluster write's
is. The request reads *Reach this host?*, then the host and, off 443, the port in mono through
`VisibleText`. A listed host is not recorded: reaching it is class 1. A host an `AskFor` rule
names is asked and recorded, listed or not.

### 5. Addresses

`egress.Public(addr netip.Addr) bool` is the one check, and the same list `webfetch`'s
`public.go` refuses: loopback, private, link-local unicast and multicast, unspecified, multicast,
and an IPv4-mapped IPv6 form of any of them. A listed name that resolves to a refused address is
a 403 *kstack: <host> resolves to a local address*, with one exception: a rule of source
`kubeconfig` may resolve anywhere, since kind, minikube and Docker Desktop put an API server at
`127.0.0.1:<port>`. The exception is the rule's, so only that host and that port get it. A
kubeconfig that names the server by address, as kind's does, gets the same exception through its
address rule (§2, step 3): that address and port are reached, and no other literal is. What a
command reaches there is what the server serves without a credential (`/version`, `/healthz`, the
OIDC discovery paths); the cluster proxy is still the way to the chat's cluster.

### 6. The environment

`sandboxedRunEnv` in `tools/bash/env.go` gains the rows step 2A's table left to this step:

| Variable | Value |
| --- | --- |
| `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`, `http_proxy` | `http://kstack:<token>@127.0.0.1:<port>` |
| `NO_PROXY`, `no_proxy` | `` (empty: nothing bypasses) |
| `ALL_PROXY` | unset, and an `EnvRule` in `sandbox.NeverEnv` |

The lowercase spellings are for curl, which reads `http_proxy` in lowercase alone, and the
uppercase for Go. `safe.Redact` blanks the token wherever a command prints its environment, as it
does for the kubeconfig. SSH is not routed: a `git` remote
over SSH fails in the sandbox with no route, and the prompt says to use HTTPS.

### 7. macOS: TLS trust in the sandbox

A Go program (`helm`, `gh`, `kubectl`) and the system `curl` verify a certificate through
Security.framework, which asks the user session's trust daemon over Mach. Today's profile refuses
that lookup (`refusedServices` names `com.apple.trustd` and `com.apple.trustd.agent`), so every
verification fails with `OSStatus -26276`. Checked on macOS 27.0.1: the service looked up is
**`com.apple.trustd.agent`**; allowed alone, a chain verifies; `com.apple.trustd`, the system
daemon, is never looked up.

So `com.apple.trustd.agent` leaves `refusedServices` and joins the profile's fixed `mach-lookup`
rule in `profile_darwin.sb`, beside `opendirectoryd.libinfo`, with a comment naming why. It is the
first service a policy step names, and it is a fixed rule of the profile, not a field of
`Policy`: every run verifies TLS the same way. The Keychain's services (`com.apple.trustd`,
`com.apple.SecurityServer`, `com.apple.securityd*`, `com.apple.secd`, `com.apple.security.agent`)
stay refused: a verification asks for a verdict and reads no key. On Linux nothing changes:
`/etc/ssl` is readable (step 2A).

### 8. Settings: Network

`network-settings.tsx`, a Network section in the Settings dialog, drawn while `sandbox.available`
is true, over `useNetworkHosts()` in `src/lib/network-hosts.tsx`, the one reader of the query and
mutations:

- **Allowed hosts**: one row per rule, the host in mono through `VisibleText` (a kubeconfig's
  server name is the user's file's text), the port when not 443, and the source as a tag: *from
  your kubeconfig*, *added by you*. A source's row is read-only; a
  user row has Remove.
- **Add**: a host field, an optional port field and a *Deny* checkbox; disabled in flight, a
  refusal's reason under the field, and one line above it: *Sandboxed commands can send what
  they read to a host you allow.*
- **Denied hosts**: the user's `Deny` entries, each with Remove, under *A denied host is refused
  even when a rule or a source lists it.*
- Under the section: *Sandboxed commands reach these hosts through Kstack's proxy. An unlisted
  host asks you, in the chat.*

The wire:

```graphql
enum NetworkHostSource { Kubeconfig User Chat }
enum GrantDuration { Chat Always }

type NetworkHost {
  id: ID!
  host: String!
  port: Int!
  source: NetworkHostSource!
  deny: Boolean!
}

extend type Query {
  "The allowlist's sources and the user's entries. Empty on a machine with no sandbox. A chat's rules are not listed here."
  networkHosts: [NetworkHost!]!
}

extend type Mutation {
  "Refused KSTACK_VALIDATION_ERROR: not a name or a glob over one, an IP, a port out of range, or already listed."
  networkHostAdd(host: String!, port: Int, deny: Boolean!): [NetworkHost!]!
  "Removes a user entry, or a class 3 net rule, by id. Refused for a source's row."
  networkHostRemove(id: ID!): [NetworkHost!]!
  "Allow an unlisted host for a chat (a chat_grants rule) or always (a user entry). What step 5B's denial line calls."
  networkHostGrant(chatID: ChatID!, host: String!, port: Int, duration: GrantDuration!): [NetworkHost!]!
}
```

A `port` left out is 443. Each mutation answers the whole list, so the section redraws from one
result. `graph.Resolver` reads and writes through `securityconfig`, which is never nil (step 1C); on a
machine with no sandbox (step 1B's `SandboxStatus`) the query answers an empty list and the
mutations are refused.

### 9. The prompt

`prompts/sandbox.md` replaces *The sandbox reaches no network* with: the sandbox reaches the
network through Kstack's proxy, which lets a command reach the cluster's API servers and the
hosts the user allowed; a host not on that list waits for the user, and a refused one comes back
`403` from the proxy, which is the user's decision; the cluster is the one host a command reaches
with a credential, so `gh`, `aws`, `gcloud` and `az` have no login in the sandbox and a private
registry takes no token; a tool must read `HTTPS_PROXY`, and `git` over SSH does not work in the
sandbox — use an HTTPS remote.

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
   server is the one local thing a Kubernetes tool needs. Recommended; a user who wants a local
   registry grants it in step 4D's terms later, when a step gives a host rule an address.
3. **Reaching a listed host is class 1, whatever the mode.** Class 3 is a *new* host, so
   `ReadOnly` refuses the unlisted and not the listed. Recommended; the alternative makes
   read-only contexts unable to `helm repo update`.
4. **No cloud host is listed.** A tunnel to `*.amazonaws.com` carries whatever a command sends,
   and a bucket that takes anonymous uploads is an exfiltration path that needs no credential at
   all. That is the note's own second principle: a tool that talks to an upstream directly is
   outside both credential isolation and permission enforcement, and no proxy classifies what
   goes to a cloud host. So a `CONNECT` to one is class 3, which asks, and the user's answer is
   the user's to judge. Recommended.
5. **A permission rule on a host outranks the listing.** A class 3 `net` `Deny` rule refuses a
   host whatever source lists it, and an `AskFor` rule asks for it, under `Decide`'s own order, so
   a `ReadOnly` context refuses a host an `AskFor` rule names. The user's rules are the one
   place a user says *never this host* or *always ask*, and a built-in source must not overrule
   them. Recommended. The alternative, a listed host skipping the rules, makes a denial in
   Settings or in a chat say nothing about an API server.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `egress`: the policy, `Match`, `Public`, `ServerRule`, `FromRule`, the handler | `egress/policy.go`, `egress/handler.go`, `egress/sources.go`, their tests | — | Planned |
| 2 | `Settings.Hosts` and its shape check; `Session.Hosts`; `hostsFor` and `networkHostGrant`'s store writes | `securityconfig/`, `session/`, `chatsvc/`, their tests | 1 | Planned |
| 3 | Every run has the relay; `route`; `hostDecider`; the environment | `tools/bash/bash.go`, `tools/bash/proxy.go`, `tools/bash/env.go`, `sandbox/policy.go`, their tests | 1, 2 | Planned |
| 4 | macOS: `com.apple.trustd.agent` in the profile; the trust test | `sandbox/sandbox_darwin.go`, `sandbox/profile_darwin.sb`, `sandbox/sandbox_darwin_test.go`, `sandbox/testdata/chain.pem` | — | Planned |
| 5 | The wire, the resolvers, codegen | `sidecar/graph/schema.graphqls`, `graph/`, `app/app.go`, generated code, `src/gql/` | 2 | Planned |
| 6 | `useNetworkHosts`, the Settings section | `src/lib/network-hosts.tsx`, `src/components/widgets/network-settings.tsx`, `settings-dialog.tsx`, their tests | 5 | Planned |
| 7 | The prompt | `tools/bash/prompts/sandbox.md`, its test | 3 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1 and 4 at the same time, then 2, then 3 and 5 at the same time, then 6 and 7 at the
same time, then 8.

## Tests

**`egress`** (a fake resolver, `httptest` servers, a `net.Pipe` dialer where the bytes matter)

- `TestAListedHostTunnels`: a `CONNECT` to a listed host answers 200 and the bytes match both
  ways; a plain absolute-form `GET` is forwarded with `Proxy-Authorization` dropped.
- `TestAnUnlistedHostAsksAndFollowsTheDecision`: `Decide` gets the action of §4; `Allowed`
  tunnels, `Denied` is a 403 naming the reason, `Prompted` holds the `CONNECT` until the answer.
- `TestADeniedHostIsForbidden`: a `Deny` rule wins over an `Allow` of every source.
- `TestAPermissionRuleOutranksTheListing`: a class 3 `net` `Deny` rule on a host the user listed
  is a 403 naming the rule, and an `AskFor` rule on a kubeconfig server sends it to `Decide` and holds
  the `CONNECT`; `FromRule` puts each effect in its list, and `Match` answers Deny over Ask over
  Allow.
- `TestAHostIsAName`: `*.example.com`, `a?b.example.com` and `[x].example.com` are a 403,
  never asked, so no answer writes a rule wider than the host drawn.
- `TestAnIPLiteralIsRefused`: IPv4, IPv6 and bracketed, never resolved and never asked; and a
  `kubeconfig` rule from `https://127.0.0.1:6443` lets `127.0.0.1:6443` through with no lookup,
  while `127.0.0.1:6444` and `[::1]:6443` are refused.
- `TestALocalAddressIsRefused`: a listed name resolving to each refused family is a 403, and a
  `kubeconfig` rule on a name resolving to `127.0.0.1:6443` tunnels. The note's second invariant
  kept: the proxy does not open the machine.
- `TestTheDialGoesToTheAddressChecked`: the dialer sees the resolved address, never the name.
- `TestTheTokenIsRequired`: no token and a wrong one are a 407; `TestTunnelsAreBounded`, and
  end with the context.
- `TestMatchGlobsAndPorts`: `*.example.com` matches `charts.example.com` and not
  `example.com`; port 0 matches any. `TestServerRuleReadsAKubeconfigServer`: host, port, a
  default port, an IP address, a URL that does not parse.

**`securityconfig`**: `TestABadHostEntryIsLeftOutWithItsReason`; `TestHostsPersist`.

**`chatsvc`**: `TestTheSessionsHostsJoinEverySource`, a kubeconfig reload seen by the next read,
a chat's class 3 rule listed as `chat`, and a `Deny` or `AskFor` rule of the chat's or the file's
in the policy's `Deny` or `Ask`; `TestNetworkHostGrantWritesWhereTheDurationSays`.

**`bash`**

- `TestEveryRunHasTheRelay`: over a fake sandbox, a run with no cluster has one relay and a
  server; `Host: cluster.kstack.invalid` there is a 503.
- `TestTheRouteSplitsTheHost`: the cluster host goes to the grant, a `CONNECT` to egress, and
  anything else is a 400.
- `TestABackgroundCommandNeverAsksForAHost`: `NoPrompts` turns the prompt into a 403; and a run
  with no asker the same.
- `TestNoProxyIsEmpty` and `TestTheSandboxedEnvironmentIsFixed`, extended with §6's rows and
  `ALL_PROXY` on the never-list.
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

**Webview** (`network-settings.test.tsx`, `network-hosts.test.tsx`): the rows with their sources
and tags, Remove only on user rows, Add with a port and with Deny, each mutation, a refusal's
reason, and nothing while `sandbox.available` is false.

## Security

**Widened.** Before this step a sandboxed command reached the chat's cluster and nothing else.
After it, a command the model runs unasked reaches every listed host — an API server's
unauthenticated paths, whatever the user added — with no credential in hand, and a host the
user allowed on a prompt. What holds it: the sandbox still has
no network but the relay, so every connection passes the handler; the handler resolves and dials,
so the sandbox never learns an address it did not connect to; an unlisted host is the user's
decision, recorded and on screen; the user's `Deny` and `AskFor` rules hold over every source
(Decisions, 5); a background command and a monitor session never ask and are refused; and the
machine's other local services stay closed (Decisions, 2).

**Residuals.** A listed host that takes an upload with no credential is an exfiltration path
for what a command has read. No cloud host is listed (Decisions, 4); a host the user adds is the
user's to judge, and the Settings section says so. The names a command asks for reach the
sidecar's resolver, so a hijacked command can leak a few bytes per lookup to whoever runs the
machine's DNS; a listed name is resolved before its address is refused. A `Deny` entry is a glob
over a name, not an address, and an address reaches the proxy only as a kubeconfig's server.

The record, `docs/security/<date>-the-egress-proxy.md`, argues both.

## When it lands

- **The security record** above, and an ADR: one relay serves every proxy; an IP literal and a
  local address are refused, a kube API server excepted; a listed host is class 1; a permission
  rule outranks the listing; no cloud host is listed.
- **`security-model.md`**: the network row says the relay carries the cluster proxy and the
  egress proxy, with the tests; a row for the allowlist and its sources; the address refusal as
  its own row; a row that no credential rides to any host but the cluster; the macOS Mach
  services row gains `trustd.agent`.
- **`sidecar/CLAUDE.md`**: `egress`, the route on the run's socket, every run's relay, the
  allowlist's sources, `Session.Hosts`, the environment rows, `refusedServices` less the agent.
- **Root `CLAUDE.md`**, the Settings dialog: the Network section and `useNetworkHosts`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the sandbox's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS and on Linux: ask for `helm repo add bitnami
https://charts.bitnami.com/bitnami` and read the request *Reach this host?* naming
`charts.bitnami.com`; approve it for the chat and read `helm repo update` run with no request;
ask for `curl https://example.com`, deny it, and read the 403 line in the output; ask for `curl
http://127.0.0.1:11434` and read it refused with no request; `gh api /zen` asks for
`api.github.com` as a new host and, allowed, answers 401, since the sandbox holds no login;
`git clone` of an SSH remote
fails and the model names HTTPS; Settings lists the kubeconfig's servers.
