---
title: "The intercepting proxy: the CA and termination"
scope: sidecar, webview
status: Planned
---

# The intercepting proxy: the CA and termination

**Needs:** step 4C, whose egress proxy answers every `CONNECT` and whose `Handler` this step
extends. **Unblocks:** steps 6B and 6C, which register the injectors this step's termination
serves.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the egress proxy (step 4C) relays a listed host through a tunnel it cannot see into. A
tool that needs a credential the sandbox has not got — `gh`, `git` over HTTPS, `gcloud`, `az` —
reaches its host and fails there, since the tunnel can add nothing.

After this step:

- **The proxy can terminate TLS for a host it holds an injector for.** An `Injector` is a
  small interface: it edits a request (a credential in), reads its head, and classifies it.
  For a host an injector claims, the proxy answers the `CONNECT`, speaks TLS to the command with
  a certificate it minted, reads each request, calls the injector, asks `Decide`, and forwards
  over a real TLS connection to the host. Every other host stays a tunnel. **No injector is
  registered in this step**: steps 6B and 6C bring them, and this step's tests use one of their
  own.
- **Kstack has a CA** the sandbox trusts by file: `egress/ca.go` mints a leaf per host, and the
  run's environment names a trust file holding the system's bundle plus Kstack's CA. The key
  lives in the sidecar's memory (Linux) or the OS keyring (macOS), never on disk.
- **On macOS, a Go program ignores the file**, so termination is offered there only once the
  user has trusted the CA in the login keychain, from Settings. A spike decides whether that
  works inside the sandbox before anything else on macOS is built.

The user sees one thing: a Settings line on macOS with a *Trust it* button. Everything else is
plumbing for steps 6B and 6C. Nothing changes on Windows: no sandbox, no proxies, no CA.

## What is not in this step

- **No injector, no credential, no classifier.** Step 6B is GitHub and git; step 6C is Google
  Cloud and Azure. Each brings its hosts, its placeholder, its class 5 list and its prompt.
- **No onboarding**: the macOS trust step is a Settings action here, and step 7A puts it in the
  flow.
- **No status or re-login** for what an injector borrows: step 2D.

## Design

### 1. Termination

`egress.Handler` (step 4C) answers every `CONNECT` on the run's socket. This step gives it two
fields, `CA *CA` (§2) and `Injectors map[string]Injector`, keyed by host glob spelled as a
`HostRule.Host` is (`api.github.com`, `container.googleapis.com`; one leading `*.` at most) and matched
the same way:

```go
// Injector holds a credential for its hosts and knows how to read their
// requests. Inject puts the credential on r; Classify reads r and the head of
// its body, HeadBytes long, and the rest streams once the action is allowed;
// Unauthorized is called when the host answers 401 to an injected request.
type Injector interface {
	Inject(ctx context.Context, r *http.Request) error
	Classify(r *http.Request, head []byte) (permissions.Action, error)
	HeadBytes() int
	Unauthorized(r *http.Request)
}

// Placeholder is the one credential every injector replaces. A command that
// sends it gets the user's; one that sends anything else keeps its own.
const Placeholder = "kstack-placeholder"
```

For a `CONNECT` whose authority matches an injector's glob, the handler answers `200`, wraps the
connection in `tls.Server` with the leaf `CA.Leaf(host)` and `NextProtos: ["http/1.1"]` alone,
so requests arrive one at a time, and serves an `http.Server` over it whose handler, per
request:

1. Refuses a request whose `Host` does not name the `CONNECT`'s host and port (`400`), so
   nothing rides a terminated tunnel to another host. Both are compared as a host and a port:
   the host lower-cased, and a missing port read as `443`. So `CONNECT api.github.com:443` takes
   `Host: api.github.com` and `Host: api.github.com:443`, and refuses `Host: api.github.com:8443`
   and a missing `Host`.
2. Calls `Inject`. An error is a `403` naming it: `credentials.ErrExpired` reads *kstack: the
   <provider> login has expired; the user can renew it*, `credentials.ErrNoCLI` *kstack: <tool>
   is not installed on this machine*, and step 2D adds `ErrExcluded`'s line.
3. Reads the head and calls `Classify`, then `Handler.Decide` (step 4C §4), which Bash's
   `hostDecider` builds from the session: `Allowed` forwards; `Prompted` goes to step 4B's
   `ActionAsker` as a `tools.ActionRequest` — the action, and its `Write` (method, path with
   query, media type, the body up to the head) as an AWS request's (step 5C §3); `Denied`
   refuses.
4. Forwards over a real TLS connection to the `CONNECT`'s host and port, through `Handler.Dial`
   to the addresses `Resolver` checked as a tunnel is (step 4C §2), with the sidecar's own
   trust, `FlushInterval: -1` and the body as a stream: the head already read, then the rest. A
   `401` from the host on a request `Inject` edited calls `Unauthorized(r)` and passes through.

A refusal is a `403` with one line of text, `kstack: <reason>`, the reason as step 3B spells it —
the mode, the rule, or *the user did not approve this change*. An injector that implements
`Refuser` (`Refusal(r, reason) (contentType string, body []byte)`) shapes the body for its
tool; step 6B's does, since `gh` prints JSON.

**The rule every injector follows**: a request carrying `Placeholder` as its credential has it
replaced; one carrying no credential gets one; one carrying anything else keeps its own. A
credential the model wrote is the model's, and the host answers it.

Every hop is recorded as an AWS request is: an `approvals` row of kind `action` (step 4B)
holding the `Action`, tagged `allowed`, `approved`, `denied`, `refused` or `not answered` in the
call's disclosure, and drawn by the same `ApprovalRequest`, whose `aria-label` each injector's
step names.

### 2. The CA and the trust file

`egress/ca.go`:

```go
// CA signs the leaf certificates the proxy presents for terminated hosts.
// NewCA mints the CA certificate when cert is nil; Leaf mints a host's once
// and caches it; PEM is the CA certificate alone, for the trust file.
func NewCA(key *ecdsa.PrivateKey, cert *x509.Certificate) (*CA, error)
func (c *CA) Leaf(host string) (*tls.Certificate, error)
func (c *CA) PEM() []byte
```

The key is ECDSA P-256. A leaf names the host as its one SAN, is valid for a year, and is
cached until an hour before it expires, so a sidecar left running keeps working. The CA certificate is self-signed, `IsCA`, and:

| Platform | Key | Certificate | Where the trust comes from |
| --- | --- | --- | --- |
| Linux | made at each start, in memory | valid for a year: the key dies with the sidecar, so a long validity adds nothing to what a leak could do, and a sidecar left running past a day keeps working | the trust file alone |
| macOS | made once, kept in the OS keyring under Kstack's service and the account `egress-ca` through `auth.Keyring`: the store `auth/keyring.go` already uses, exported with `Get`, `Set` and `Delete` by account name, which step 7B's connections use too | valid for five years, kept beside the key | the trust file, and the login keychain (§3) |

**The trust file** is built once per start by `egress.Trust(pem) ([]byte, error)`: the system's
bundle, then the CA's PEM. The system's bundle is, on Linux, the first that exists of
`/etc/ssl/certs/ca-certificates.crt`, `/etc/pki/tls/certs/ca-bundle.crt` and `/etc/ssl/cert.pem`;
on macOS, `/usr/bin/security find-certificate -a -p
/System/Library/Keychains/SystemRootCertificates.keychain`, run on the host at start, with
`/etc/ssl/cert.pem` as the fallback. The file holds both because `SSL_CERT_FILE` replaces a
program's bundle rather than adding to it (`crypto/x509/root_unix.go`: *If set this overrides
the system default*).

Each run's directory gains two read-only files beside its kubeconfig, `ca.pem` and `trust.pem`,
written by `sandboxedRunFor` from the bytes built at start, and the run's environment (step 2A's
table) gains:

| Variable | Value |
| --- | --- |
| `SSL_CERT_FILE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `AWS_CA_BUNDLE` | the run's `trust.pem` |
| `SSL_CERT_DIR` | empty, so OpenSSL adds no hashed directory to the file |

The files and the variables are written for every run, whether or not an injector is
registered: a trust file that names one more CA changes nothing for a tunnelled host. Each
injector's step adds the variables its tool reads (`GH_TOKEN`, `CLOUDSDK_*`, `AZURE_*`).

### 3. macOS: a Go program ignores the file

A Go program on macOS verifies TLS through the system trust store and reads no environment:
`crypto/x509/root_darwin.go` calls `systemVerify` through `crypto/x509/internal/macos`, and the
`SSL_CERT_FILE` and `SSL_CERT_DIR` reads are in `root_unix.go`, whose build line does not name
darwin (checked in Go 1.26.8's tree; `sidecar/CLAUDE.md`'s `loginshell` section says the same).
`gh` and `helm` are Go programs, so on macOS a terminated connection fails in them unless
Kstack's CA is in a keychain the system trusts, read by `trustd` outside the sandbox —
`com.apple.trustd.agent`, the service step 4C §7 allows so Go programs verify TLS at all. So:

- **The CA is per install on macOS** (§2), since a root trusted in the keychain has to stay the
  same root.
- **One action, `egressTrustCA`**: the sidecar writes `ca.pem` to a temporary file and runs
  `/usr/bin/security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db
  <file>` on the host, as the user, in its own session, under a 5 minute bound; macOS asks for
  the login password, once. The profile allows `trustd.agent` alone, never the Keychain files.
- **Termination is offered only while the CA is trusted.** `egressStatus` answers `terminating`
  and a `reason`; on macOS `terminating` holds once `security verify-cert -c ca.pem -p ssl`
  succeeds on the host, checked at start and after `egressTrustCA`; on Linux it is always true.
  While it is false every host stays a tunnel, `Injectors` is left empty, and step 4C's Network
  section draws one line on macOS — *Tools that use your logins in the sandbox need Kstack's
  certificate trusted on this Mac.* — with a **Trust it** button that calls `egressTrustCA`,
  and *Trusted* once it is. Each injector's step reads `terminating` to decide whether to set
  its tool's variables, and says in its prompt what is off while it is false.
- **A spike goes first** (task 1): on a Mac, a Go TLS client inside the real sandbox, with
  `~/Library/Keychains` unreadable and `trustd` allowed, must verify a leaf signed by a root in
  the login keychain: `TestAGoClientTrustsTheKeychainRootThroughTrustd`
  (`egress/ca_darwin_test.go`), run where the root is installed and skipped otherwise. Until it
  passes, this step lands on Linux, and macOS keeps tunnels.

## Decisions this step asks for

1. **Per start on Linux, per install on macOS.** The note says "per-install". Where the trust
   is by file a key that dies with the sidecar is stricter and costs nothing; where the keychain
   trusts the root it has to stay the same root, so the key lives in the keyring. Recommended.
2. **Build Linux first; macOS termination behind the spike.** If the spike fails, the macOS
   answer is a tunnel, the Settings line says so, and the record names the gap. Recommended.
3. **HTTP/1.1 alone inside a terminated connection**, so requests come one at a time and each
   is classified before the next is read; and **one placeholder for every injector**, so a
   tool's variables and the rule that replaces it are the same in steps 6B and 6C. Recommended.
4. **Termination is a mechanism with no host of its own.** A host is terminated only because an
   injector claims it, so the plaintext the proxy reads is always one it is putting a credential
   on. Recommended; the record leans on it.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The macOS spike | `egress/ca_darwin_test.go` | — | Planned |
| 2 | The CA: keys, leaves, the trust file; the keyring entry | `egress/ca.go`, `egress/ca_darwin.go`, `egress/ca_linux.go`, `auth/keyring.go`, their tests | — | Planned |
| 3 | Termination: `Injector`, `Placeholder`, the leaf server, the forward, the record | `egress/terminate.go`, `egress/server.go`, their tests | 2 | Planned |
| 4 | The run: `ca.pem`, `trust.pem`, the variables | `tools/bash/bash.go`, `tools/bash/env.go`, their tests | 2 | Planned |
| 5 | macOS trust: `egressTrustCA`, `egressStatus`, the Settings line | `egress/trust_darwin.go`, `sidecar/graph/schema.graphqls`, `graph/`, `src/lib/network-hosts.tsx`, `src/components/widgets/network-settings.tsx`, their tests | 1, 2 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1 and 2 at the same time, then 3 and 4 at the same time, then 5, then 6.

## Tests

**`egress`** (a test injector, `testInjector` in `terminate_test.go`, that claims
`fake.kstack.test`, sets `X-Kstack-Test: injected`, replaces `Placeholder`, and classifies
`GET` as class 1 and the rest as class 4)

- `TestALeafIsMintedOnceAndCached`: two `Leaf("fake.kstack.test")` calls answer one
  certificate, valid for the host, signed by the CA, and `Leaf` of another host answers another.
- `TestTheTrustFileHoldsTheBundleAndTheCA`: over a fixture bundle, `Trust` is the bundle's
  certificates then the CA's, and a `tls.Client` with that pool verifies a leaf.
- `TestATerminatedRequestReachesTheHostInjected`: through a run's socket with a fake
  `fake.kstack.test` behind it, a request reaches the fake carrying the injector's header; one
  sending `Placeholder` reaches it with what the injector put there, and the placeholder never
  does; one sending another credential keeps it; the leaf's ALPN is `http/1.1`.
- `TestARequestForAnotherHostIsRefused`: a request whose `Host` names another host, or another
  port, than the `CONNECT`'s is a 400 and reaches nothing, and so is one with no `Host`.
  `TestTheHostMatchesTheAuthorityWithItsDefaultPort`: after `CONNECT fake.kstack.test:443`,
  `Host: fake.kstack.test`, `Host: fake.kstack.test:443` and `Host: FAKE.kstack.test` each reach
  the fake. `TestATunnelHostIsUntouched`: a listed host with no injector is relayed
  byte for byte, and with `Injectors` empty every host is.
- `TestDecideIsAsked`: under `ReadOnly` a `POST` is a 403 naming the mode, one line of text;
  under `Ask` it waits on the asker with the head as its `Write`, and forwards once approved;
  under `NoPrompts` it is refused; a `GET` never asks. `TestAnInjectErrorIsAForbidden`:
  `ErrExpired` and `ErrNoCLI` each name their line.
- `TestABodyStreamsOnceAllowed`: a 10 MiB body is forwarded whole after the head, and the
  response streams before the body ends. `TestA401CallsUnauthorized`: the injector is told once,
  and the response passes through.
- `TestTheCAKeyIsNeverOnDisk` (`ca_linux_test.go`): after a start, no file under the data, cache
  and runtime directories holds the key's bytes, PEM or DER; `TestTheCAKeyLivesInTheKeyring`
  (`ca_darwin_test.go`), over a fake keyring: written there and nowhere else; and
  `TestAGoClientTrustsTheKeychainRootThroughTrustd` (`ca_darwin_test.go`), the spike of §3,
  through the real sandbox, skipped where `security verify-cert` fails.

**`bash`**: `TestTheRunTrustsTheCA`: the run's directory holds `ca.pem` and `trust.pem`
read-only, and the environment names `trust.pem` in every variable of §2's table, with
`SSL_CERT_DIR` empty.

**Webview** (`network-settings.test.tsx`): the macOS line draws *Trust it* while `terminating`
is false and *Trusted* after, and the button calls `egressTrustCA` once.

## Security

**Widened.** The proxy can read the plaintext of a request to a terminated host. What holds it:
a host is terminated only because an injector claims it (Decisions 4), so the plaintext read is
one the proxy is putting a credential on; nothing is terminated in this step, since no injector
is registered; a `Host` naming another host or port than the `CONNECT`'s is refused, so a
terminated tunnel reaches one host.

**The CA.** A leaked key lets its holder impersonate any host to any program that trusts the
CA. On Linux the key dies with the sidecar and only sandboxed commands trust it. On macOS the
login keychain trusts the root for every program of the user's, so the key is kept where the
OAuth tokens are, never on disk, exported by no mutation; the record argues that this is the
blast radius the trust prompt asks the user to accept, and the *Trust it* button is the user's
click, never the model's.

**Residuals.** A `Placeholder` is a public string, so a command that spells it gets what the
injector's tool gets, classified the same; each injector's step says so for its credential. The
trust file makes every tool in the sandbox trust Kstack's CA for every host, while the proxy
presents its leaf for the injected hosts alone.

The record, `docs/security/<date>-the-intercepting-proxy.md`, argues all of this; steps 6B and
6C's records point at it for the mechanism.

## When it lands

- **The security record** above, and **an ADR**: TLS is terminated only for injected hosts; the
  CA is per start on Linux, per install on macOS with its key in the keyring; HTTP/1.1 alone
  inside a terminated connection.
- **`security-model.md`**: rows for the CA's key, the trust file, and termination's one-host
  rule, each with its test.
- **`sidecar/CLAUDE.md`**: `egress`'s `Injector`, `Placeholder`, the CA, the trust file,
  termination's order, the run's two files and variables, `egressStatus` and the macOS trust
  action. **Root `CLAUDE.md`**: the Network section's macOS line.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with `egress`'s tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on Linux: `cat $SSL_CERT_FILE | tail -30` in a chat shows Kstack's
certificate after the system's; `curl -sS https://api.github.com/zen` still answers (a tunnel,
since no injector is registered); `ls <data>` holds no `.pem` or key file. On macOS, Settings
shows the line and *Trust it*; click it, enter the login password, and read *Trusted*; `security
find-certificate -c "Kstack" ~/Library/Keychains/login.keychain-db` finds the root.
