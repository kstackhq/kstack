# Sandbox, credentials and permissions

The build order for [the sandbox, credentials and permissions note](../../notes/sandbox-credentials-and-permissions.md):
how a command is confined, how the user's credentials stay out of it, and how the user decides
what the agent may do. Each spec here says how one step gets built. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply, and so do the decisions
in the note's [Where this meets the code](../../notes/sandbox-credentials-and-permissions.md#where-this-meets-the-code).

**Where the series ends.** When the last step lands:

- every command the model runs is confined by the OS sandbox, unless the user switched that chat
  to run outside it, and then every command asks;
- the sandbox reads the system and the user's tools, writes the workspace and the folders the
  user granted, and reaches the network only through Kstack's proxies;
- the proxies hold the user's credentials, borrowed from the user's own tools, and no command
  ever holds one;
- every action a proxy forwards is classified, and the user's approval mode and rules decide
  whether it runs, asks or is refused;
- a prompt names the action, not the shell string, and the user can answer once, for this chat,
  or always;
- a monitoring session can read everything and change nothing, and never asks;
- the user sees the sandbox in their own terms: folders, hosts, contexts, profiles.

## The shape of the work

**Steps are numbered by wave.** A step's id is a wave number and a letter: `1A`, `3B`. Every
step in one wave can be built at the same time, and a step needs only steps of earlier waves.
The letter orders a wave's steps for reading, not for building. Files are named by the id
(`3b-the-permissions-engine.md`); prose says "step 3B". Seven waves, 25 steps.

**What has landed.** The sandboxed Bash work built the
Workspace sandbox on macOS and Linux, the cluster proxy with its Kubernetes classifier and
Secret redaction, a per-run token, a generated kubeconfig, and a request for each cluster write.
That is where this sequence starts. `sidecar/CLAUDE.md` describes it as it is.

**Every step can ship.** No step leaves a command with no way to run: from step 1B the user can
switch a chat to run outside the sandbox, which covers whatever the sandbox cannot yet do. What
a step degrades is convenience, never what is asked: a call that asks today keeps asking until
a later step gives its class a rule.

**Two steps in one wave may meet at a seam.** Where they do, the seam is named below, and the
rule is one: the step that lands second adapts to the one that landed first, and neither waits.
A step that touches a seam says in its own text what it does in each order.

**Native Windows has no sandbox** ([ADR](../../adr/2026-09-28-native-windows-has-no-sandbox.md)).
Every command there runs outside it and asks; none of the proxies confine anything there. Each
step says what it changes on Windows, which is usually nothing.

**Every spec has the same sections, in the same order.** *In short* says what the step changes,
for the user and for the code, in a few bullets. *What is not in this step* names what a reader
might expect here and says which step has it. *Design* is numbered sections an implementer works
through. *Decisions this step asks for* lists each choice the spec makes and why, so a reviewer
can disagree with one line. *Tasks* is the order of assignment, with the files each one touches.
*Tests* names every test the step adds and what it pins. *Security* says what the step widens,
what it narrows, what holds it, and what stays as a residual. *When it lands* lists the docs to
update and the record or ADR to write. *Verification* is the commands to run, then what to check
by hand. An implementer reads *In short* and *Design*, works the *Tasks* one at a time, and
treats *Tests* as the acceptance list.

## Shared vocabulary

The names below are fixed here so the specs agree. A spec that first introduces one says what
it holds; a later spec uses it by name. Go paths are under `sidecar/internal/`.

**Packages.**

| Package | Holds | First in |
| --- | --- | --- |
| `sandbox` | the OS sandbox: `Policy`, the zone `Lists`, `Limits`, and reading a run's denials | landed; 1A, 2A, 2B, 5B |
| `sandboxconfig` | the settings file `<data>/sandbox.json`: the store, and the fields later steps add — the frozen `PATH`, the permission rules and modes, the host rules, the folders granted always, the registered tools, the credential exclusions, the monitor's switch, the onboarding flag | 1C |
| `credentials` | credentials borrowed from the user's tools, cached in memory to expiry; what was discovered; each provider's status | 1D |
| `session` | a `Session`: one agent run's kind, workspace, approval mode, host allowlist, folder grants and rules; and how a subagent's is narrowed from its parent's | 2C |
| `permissions` | the action classes, the approval modes, the rules, and `Decide` | 3B |
| `kubeproxy` | the cluster proxy | landed; 3B, 5A |
| `egress` | the egress proxy: the host allowlist, and from 5D TLS termination with credential injection | 4C |
| `awsproxy` | the AWS endpoint: SigV4 checked, stripped and re-signed | 5C |
| `monitor` | the monitoring session and its proposal cards | 6D |
| `tools/bash` | the Bash tool, which builds each run from its session | landed; every wave |
| `tools/kube` | the first-class cluster tools | 6E |

**Types.**

- **`sandbox.Policy`**: everything the OS sandbox enforces for one run. `Files` (Read, Write and
  Deny rules), `Always` (the denied-always list, which nothing opens, and Kstack's directories,
  which no rule and no grant opens, and the run's own paths inside those), `Network` (the relays), `Limits`.
- **`sandbox.Lists`**: the zones on a platform. `System` (readable by default), `Toolchain`
  (readable under the home by default), `Never` (denied always). One shared file and one per
  platform.
- **`session.Session`**: `Kind` (`chat`, `monitor` or `subagent`), the chat, the workspace, the
  approval `Mode`, `NoPrompts` and `NoSecretData`, `Hosts`, `Folders`, and `Rules`, the rules
  that apply to it, read live. `tools.Runtime.Session` carries it to every tool.
- **`permissions.Class`**: 1 to 6 as the note numbers them. **`permissions.Mode`**: `ReadOnly`,
  `Ask`, `Auto` (step 3B says why the note's *Trusted scopes* is `Ask` with rules).
  **`permissions.Rule`**: `Effect` (`Allow`, `Deny` or `AskFor`), `Class`, `Provider`, `Scope`,
  and where it lives: a chat's rules are `chat_grants` rows, the always rules are in
  `sandboxconfig`, and the shipped rules are code. **`permissions.Decision`**: `Allowed`,
  `Prompted` or `Denied`, with a `Reason`.
- **`permissions.Action`**: one classified action: its `Provider` (`k8s`, `aws`, `github`,
  `gcp`, `azure`, `net`, `path`), `Class`, `Scope` (context and namespace, account and region,
  organization and repository, host, or folder), a one-line `Summary` for the prompt, and the
  request behind it.
- **The run's token**: one per run, as today, mapped to the run's `Session`. Every proxy served
  on the run's socket reads the session through the token, and one server on that socket serves
  them all: the cluster proxy by its `Host`, the egress and cloud proxies by `CONNECT` and the
  absolute-form requests the proxy variables send.

**The record.** `approvals` is the one table a prompt writes: a call's own (`kind: call`) and,
from step 4B, any classified action (`kind: action`, carried to the user as a
`tools.ActionRequest` through the runtime's `ActionAsker`), with the decision's duration.
`tool_calls.sandboxed` stays what it is. `conversations.sandbox_disabled` is step 1B's switch.

**The wire.** `approvalDecide` takes the decision. Settings are read and written through
queries and mutations named `sandbox…`, `permission…`, `credential…`, `network…`, `folder…`,
`egress…`, `monitor…` and `proposal…`, each introduced by the step that needs it; the chat's
switch is `chatSandboxDisabledSet`. An enum's members are spelled as the schema's are, in
PascalCase (`Pending`, `Bookmark`).

**The webview.** The request is `ApprovalRequest` in `chat-transcript.tsx`. The Settings dialog
gains one section per step that has settings, each its own component beside
`settings-dialog.tsx`.

## Steps

**Wave 1** — nothing beyond `main`.

| Spec | Step | After it |
| --- | --- | --- |
| 1A | **The sandbox policy.** What a run may read and write becomes one `Policy`: Read, Write and Deny rules, and an `Always` part for the denied-always list and Kstack's directories, which no rule opens. Linux and macOS each compile it, and one table of cases pins that both answer alike. No behavior changes. **Landed**; `sidecar/CLAUDE.md` describes it. | Any access decision can be explained from the policy, and granting `~` cannot expose `~/.ssh`. |
| [1B](1b-outside-the-sandbox-is-the-users-choice.md) | **Outside the sandbox is the user's choice.** The model's `dangerouslyDisableSandbox` flag goes. A per-chat switch, off by default, runs that chat's commands outside the sandbox, each asking as today. | No chain of approvals leaves the sandbox; the user does, for one chat, on purpose. |
| [1C](1c-the-settings-file.md) | **The settings file.** `sandboxconfig` and `<data>/sandbox.json`: the store every later setting lives in, with no fields yet, opened on every platform. | Every setting of the sandbox, the proxies and the permissions has one home no sandboxed command reads. |
| [1D](1d-credentials-from-the-users-tools.md) | **Credentials from the user's tools.** The `credentials` package: each provider's credential borrowed through the tool's own command on the host, cached in memory to expiry, never written to disk; what was discovered; each provider's status. | Every proxy has one place to borrow a credential from, and the sandbox never sees one. |

Seam: 1A and 1B both change `tools/bash`, 1A how a sandboxed run's policy is built and 1B which
calls are sandboxed; they touch different functions, and 1B's `sandboxerFor(rt)` answers the
sandboxer whose `Command` 1A changes. 1B, 1C and 1D each add a few lines to `app.New`.

**Wave 2** — needs wave 1.

| Spec | Step | After it |
| --- | --- | --- |
| [2A](2a-the-sandboxs-own-environment.md) | **The sandbox's own environment.** The zone lists become `Lists`; the environment is built from the note's pass-through list and nothing else, with a test that the never-list stays out; the `*_HOME` redirects and a per-cluster tool cache; no snapshot in the sandbox. Needs 1A. | Nothing in the user's environment reaches a sandboxed command, and a tool that writes under the home writes a cache Kstack owns. |
| [2B](2b-process-limits-and-restrictions.md) | **Process limits and restrictions.** CPU time, memory, open files and process count, set on the run by `Policy.Limits` and applied by the forwarder, which becomes every run's first process; `sudo` and `ptrace` refused on both platforms. Needs 1A. | A runaway command cannot take the machine, and no command escalates. |
| [2C](2c-sessions.md) | **Sessions.** A `Session` binds an agent run to its kind, workspace and switch; a chat's turn makes one, a subagent narrows its parent's, and the run's token maps to it. No behavior changes. Needs 1B. | Every policy question has one place to ask, and a subagent can never hold more than its parent. |
| [2D](2d-credential-status-and-re-login.md) | **Credential status, expiry and re-login.** Each provider's status drawn from 1D; an expired credential surfaces in chat with the command that renews it, run on the host; the Settings section with exclusions and the per-cluster AWS profile. Needs 1C and 1D. | An expired SSO session is one click to renew, and a monitoring session pauses rather than asks. |

Seam: 2A and 2B both change the compiled arguments and profile and their goldens.

**Wave 3** — needs waves 1 and 2.

| Spec | Step | After it |
| --- | --- | --- |
| [3A](3a-path-from-the-login-shell.md) | **`PATH` from the login shell.** The user's `PATH` is resolved from their login shell, filtered, frozen in `sandboxconfig`, diffed at each launch with a confirmation for new entries under the home, refreshed on request, and shown in Settings. Needs 2A and 1C. | The sandbox finds the tools the user's shell finds, and no startup file widens it unseen. |
| [3B](3b-the-permissions-engine.md) | **The permissions engine.** Classes, modes, rules, the shipped deny rules, and `Decide`; the Kubernetes classifier assigns class 4 and 5; the cluster proxy asks `Decide` before each write; modes per context in `sandboxconfig`, `prod*` read-only by default; the Settings section. Needs 2C and 1C. | A cluster write runs, asks or is refused by the user's mode and rules, and class 5 is never allowed unasked. |

Seam: both add fields to `sandboxconfig.Settings`.

**Wave 4** — needs waves 1 to 3.

| Spec | Step | After it |
| --- | --- | --- |
| [4A](4a-the-login-shell-runs-in-the-sandbox.md) | **The login shell runs in the sandbox.** The `PATH` resolution and the shell snapshot run confined: everything but Kstack's folders readable, nothing writable, no network. Needs 1A and 3A. | A startup file cannot read `app.db` or reach the network while Kstack runs it. |
| [4B](4b-the-prompt-names-the-action.md) | **The prompt names the action.** A request draws the classified action, a diff for an apply or a patch, and four answers: once, this chat, always, deny. "Always" writes a rule. Needs 3B. | The user reads "Delete pod `api-7f9c` in `team-a` on `dev-eks`" and decides for the scope they see. |
| [4C](4c-the-egress-proxy.md) | **The egress proxy and the host allowlist.** One server on every run's socket; an HTTP proxy that lets a command reach the listed hosts and asks for an unlisted one (class 3), resolving names outside the sandbox; the list's sources, a cloud host joining only with the proxy that classifies it; `trustd` allowed on macOS; the Settings section. Needs 2B, 2C and 3B. | `helm repo update` reaches the registries the user listed, and a hijacked command reaches nothing else. |
| [4D](4d-path-grants.md) | **Path grants.** The user grants a folder, read or read-write, for a chat or always; the denied-always list still wins; the Settings section. Needs 1A, 2C and 3B. | The agent can see `~/code/my-service` because the user said so, and `~/.ssh` under a granted `~` stays hidden. |

Seam: 4B replaces the asker 4C asks a new host through; 4C says what it does in each order.

**Wave 5** — needs waves 1 to 4.

| Spec | Step | After it |
| --- | --- | --- |
| [5A](5a-secret-data-is-a-permissioned-read.md) | **Secret data is a permissioned read.** Class 6: a read of Secret data asks, and the proxy redacts unless the session holds the grant; a session can be one that never reads it. Needs 3B and 4B. | A Secret's values reach the model only after the user says so, and never in a monitoring session. |
| [5B](5b-denials-in-context.md) | **Denials in context.** A path, host or write a run was refused is found per command and drawn under the call in plain words, with the grant that resolves it. Needs 4C and 4D. | A blocked command explains itself, and the closed home becomes how users find the permission system. |
| [5C](5c-aws-through-the-proxy.md) | **AWS through the proxy.** `AWS_ENDPOINT_URL` names the run's proxy; placeholder keys sign inside; the proxy checks and strips the signature, borrows through 1D, re-signs and forwards; the AWS classifier and its class 5 list. Needs 1D, 2D, 3B, 4B and 4C. | `aws eks describe-cluster` runs in the sandbox with no credential in it, and `aws iam` writes ask. |
| [5D](5d-the-intercepting-proxy.md) | **The intercepting proxy: the CA and termination.** The egress proxy terminates TLS for a host an injector is registered for, under a CA the sandbox trusts; the trust file; the macOS keychain step behind a spike. Needs 4C. | A tool that speaks HTTPS to a listed host can be given a credential it never holds. |

Seam: 5C and 5D both extend the egress handler.

**Wave 6** — needs waves 1 to 5.

| Spec | Step | After it |
| --- | --- | --- |
| [6A](6a-tool-probing.md) | **Tool probing.** The curated tools are probed in the real sandbox at onboarding and after a `PATH` refresh; each reports the binary it resolved to and the paths it was denied, with a grant button; the user registers more tools; a missing `kubectl` is said plainly. Needs 3A and 5B. | `gcloud` works in the sandbox after one click, and the user knows which `kubectl` the agent runs. |
| [6B](6b-github-through-the-proxy.md) | **GitHub and git through the proxy.** The GitHub injector over 5D's termination, with `gh auth token` from 1D; the GitHub classifier and its class 5 list; a push names its refs. Needs 1D, 4B and 5D. | `gh pr list` works in the sandbox, and a push asks. |
| [6C](6c-google-cloud-and-azure.md) | **Google Cloud and Azure through the proxy.** The two injectors over 5D's termination, with tokens from 1D, and their classifiers. Needs 1D, 4B and 5D. | `gcloud` and `az` reads run in the sandbox, and their writes ask. |
| [6D](6d-the-monitoring-session.md) | **The monitoring session.** A `monitor` session: read-only at every proxy, Secret data never, no prompts, its own workspace, no folder grants; a proposal card into chat, whose "Do it" runs the action in a chat session. Needs 2D, 4C and 5A. | A monitoring agent can plug in and change nothing without a human. |
| [6E](6e-first-class-cluster-tools.md) | **First-class cluster tools.** `k8s_read`, `k8s_apply`, `k8s_delete`, `k8s_scale` and `k8s_rollout_restart`, each a legible prompt, with raw Bash as the fallback. Needs 4B and 5A. | Most cluster changes ask with a prompt the sidecar wrote, not one it parsed. |

Seam: 6B and 6C both register injectors on the egress handler.

**Wave 7** — needs waves 1 to 6.

| Spec | Step | After it |
| --- | --- | --- |
| [7A](7a-onboarding.md) | **Onboarding.** One flow on first launch: the `PATH` list, the probes, the credentials found, and the approval mode with `prod*` read-only. Needs 2D, 3A, 3B, 5B and 6A. | A new user's tools work in the sandbox with nothing typed and nothing signed into. |
| [7B](7b-oauth-connections.md) | **OAuth connections.** Connect with GitHub, AWS, Google or Azure from Settings, tokens in the OS keychain read by the proxy alone, and scoped-down tokens for the monitoring session. Needs 2D and 6D. | A user with no CLI login, or one who wants a revocable app token, connects once. |

## What each step records

A step that moves a security boundary writes a security record in `docs/security/` and, when it
chose between real alternatives, an ADR; a step that only adds tests to what exists writes
neither and says so. A risk a step accepts on purpose is a **By decision** row in
`security-model.md` with the ADR that accepted it, as the root `CLAUDE.md` requires. Each spec's
*Security* section is the authority; this table is the index.

| Step | Security record | ADR | `security-model.md` |
| --- | --- | --- | --- |
| 1A | no | no | the sandbox rows gain the new tests; a row for the denied-always list winning over a Read |
| 1B | yes | yes | the rows naming `dangerouslyDisableSandbox` say the switch is the chat's |
| 1C | no | no | `sandbox.json` joins the owner-only files row |
| 1D | yes | no | rows for no credential under Kstack's directories, and for the borrows being the tools' own commands |
| 2A | yes | yes | the sandbox rows, the environment row, a row for the tool cache |
| 2B | no | yes | rows for the limits, the setuid programs and tracing |
| 2C | no | no | the subagent rows gain the narrowing test |
| 2D | yes | yes | rows for the fixed renewal command and the exclusion list |
| 3A | yes | yes | the shell-import and sandboxed-command rows say the frozen list |
| 3B | yes | yes | the cluster-write rows say `Decide`; a row for the shipped rules; the bash tool record's consent line amended |
| 4A | yes | no | the snapshot and shell-import rows; a **By decision** row for what the shell's output shapes outside the sandbox |
| 4B | yes | yes | the request row; the rules row |
| 4C | yes | yes | the network row; rows for the allowlist and the address refusal; the Mach services row |
| 4D | yes | yes | the file row; the denied-always row; the file tools' rows; a row for the monitor holding no folder |
| 5A | yes | yes | the Secret redaction row; the helm release row; the cluster-reads **By decision** row; a row for `NoSecretData` |
| 5B | no | no | a row: a denial is drawn and offered, never granted |
| 5C | yes | yes | rows for no AWS credential in the sandbox and for AWS writes through `Decide`; a **By decision** row for reads leaving the machine |
| 5D | yes | yes | rows for the CA's key, the trust file and termination's one-host rule |
| 6A | no | no | a row: the probe runs only the listed invocations |
| 6B | yes | a line in 5D's | rows for the injection and the classifier |
| 6C | yes | a line in 5D's | rows for the two injectors and classifiers |
| 6D | yes | yes | rows for the monitor session, its folder and the proposal |
| 6E | yes | amends the cluster ADR | a row for the five tools |
| 7A | no | no | one line under the sandbox rows |
| 7B | yes | yes | rows for the keyring, the credential source and the monitor's scoped credential |

## When it lands

Once wave 7 has landed, the design is true of the code. Fold what each spec's *When it lands*
lists into the `CLAUDE.md` files, write the ADRs the specs name, update the note's *Where this
meets the code* to say it has landed, delete this directory, and remove the sequence's row from
the index.
