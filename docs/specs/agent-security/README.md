# Sandbox, credentials and permissions

The build order for [the sandbox, credentials and permissions note](../../notes/sandbox-credentials-and-permissions.md):
how a command is confined, how the cluster's credentials stay out of it, and how the user decides
what the agent may do. Each spec here says how one step gets built. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply, and so do the decisions
in the note's [Where this meets the code](../../notes/sandbox-credentials-and-permissions.md#where-this-meets-the-code).

**Where the series ends.** When the last step lands:

- every command the model runs is confined by the OS sandbox, unless the user switched that chat
  to run outside it, and then every command asks;
- the sandbox reads the system and the user's tools, writes the workspace and the folders the
  user granted, and reaches no network unless the user turned it on — for the chat, for one
  turn, or for one command they approved — and never the machine's own loopback;
- the cluster proxy holds the cluster's credentials, read from the kubeconfig, and no command
  ever holds one; any other host a command with network reaches, it reaches with no credential;
- every cluster write the proxy forwards is classified, and the user's approval mode and rules
  decide whether it runs, asks or is refused;
- a prompt names the action, not the shell string, and the user can answer once, for this
  command, for this chat, or always;
- a monitoring session can read everything and change nothing, and never asks;
- the user sees the sandbox in their own terms: folders, the network switch, contexts.

## The shape of the work

**Steps are numbered by wave.** A step's id is a wave number and a letter: `1A`, `3B`. Every
step in one wave can be built at the same time, and a step needs only steps of earlier waves.
The letter orders a wave's steps for reading, not for building. Files are named by the id
(`3b-the-permissions-engine.md`); prose says "step 3B". Seven waves, 18 steps.

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
| `sandbox` | the OS sandbox: `Policy`, the zone `Lists`, `Limits`, and reading a probe's denials | landed; 1A, 2A, 2B, 6A |
| `securityconfig` | the settings file `<data>/security.json`: the store, and the fields later steps add — the frozen `PATH`, the permission rules and modes, the folders granted always, the registered tools, the monitor's switch, the onboarding flag | 1C |
| `session` | a `Session`: one agent run's kind and sandbox switch, then its approval mode, network, folder grants and rules; and how a subagent's is narrowed from its parent's | 2C |
| `permissions` | the action classes, the approval modes, the rules, and `Decide` | 3B, 3C, 4B |
| `kubeproxy` | the cluster proxy | landed; 3B, 5A |
| `monitor` | the monitoring session and its proposal cards | 6B |
| `tools/bash` | the Bash tool, which builds each run from its session | landed; every wave |

**Types.**

- **`sandbox.Policy`**: everything the OS sandbox enforces for one run. `Files` (Read, Write and
  Deny rules), `Always` (the denied-always list, which nothing opens, and Kstack's directories,
  which no rule and no grant opens, and the run's own paths inside those), `Network` (the relays, and `Internet`, 4C), `Limits`.
- **`sandbox.Lists`**: the zones on a platform. `System` (readable by default), `Toolchain`
  (readable under the home by default), `Never` (denied always), `Closed` (denied, but a grant
  inside one opens what it names). One shared file and one per platform.
- **`session.Session`**: `Kind` (`chat`, `monitor` or `subagent`) and `Outside` (2C); `Policy`,
  a function answering the `permissions.Policy` for a kube context, read live on every decision
  (3B); `Network`, why a command has the internet — `chat`, `turn` or `""` — nil for never (4C); `Folders` (4D); `NoPrompts` and `NoSecretData` (5A). The chat, the cluster
  and the workspace are not on it: they are the runtime's `ChatID`, `ClusterID` and
  `tools.WorkspacePath(rt.Dir)`. `Narrow` copies the whole session and sets `Kind`, so a subagent
  holds its parent's switch and reads its parent's functions (step 2C); a later field that must
  differ for a subagent says so. `tools.Runtime.Session` carries it to every tool.
- **`permissions.Class`**: 1 to 6 as the note numbers them. **The class names the provider**:
  1 and 2 are folders, 4 to 6 the cluster. Class 3 (a new host) stays defined and is unused:
  the network is a switch, not a list of hosts (4C). There is no separate provider field.
  **`permissions.Mode`**: `ReadOnly`, `Ask`, `Auto` (step 3B says why the note's *Trusted
  scopes* is `Ask` with rules).
- **`permissions.Rule`**: flat, one field per thing it can name. `ID`, `Effect` (`Allow`, `Deny`
  or `AskFor`) and `Class`; for the cluster `Context`, `Namespace`, `Verb`, `Group` and `Kind`
  (3B); for a folder `Folder` (4D); and `Command`, never
  stored (4B). Each new field is `omitempty`, so a row written before it decodes unchanged.
  A chat's rules are `chat_grants` rows. **Every always rule is in `securityconfig`'s `Rules`**:
  cluster writes and folders alike. A class is accepted once its step adds it to
  `ruleClasses`, and shape-checked by the case it adds to `ruleRefusal`. `Rule.Line()` is the rule in the
  user's words.
- **`permissions.Policy`**: `Mode` and `Rules` (3B), then `NoPrompts` and `NoSecretData` (5A).
  `Policy.Decide(act)` answers a **`permissions.Decision`** (`Allowed`, `Prompted` or `Denied`)
  and the reason in the user's words, a string. It is two layers (3C): `Authorize(act)` answers
  a **`permissions.Verdict`** — `Permit`, or one of three denials, `Unmatched` (nothing matched;
  a grant lifts it), `Forbid` (an `AskFor` rule or class 5; an answer lifts it once, no grant
  does) and `Refuse` (a `Deny` rule or the mode; nothing lifts it) — and `Verdict.Outcome()` is
  the `Decision`: a permit runs, a refusal is refused, either other denial is put to the user.
  A prompt is a denial the user may lift, and an approval that outlasts the request is a grant.
- **`permissions.Action`**: one classified action: `Class`, `Context`, `Namespace`, `Verb`,
  `Group`, `Kind`, `Name` and a one-line `Summary` (3B);
  `DryRun`, set by `classify`, since a dry run is never grantable (4B). A
  folder grant is never an action: it is decided by the user in Settings or on a denial, and no
  command asks for one (4D).
- **The run's token**: one per run, as today, mapped to the run's `Session`. The cluster proxy,
  the one proxy on the run's socket, reads the session through it. A run with network reaches
  the internet directly, never through a Kstack proxy (4C).

**The record.** `approvals` is the one table a prompt writes: a call's own (`kind: call`) and,
from step 4B, any classified action (`kind: action`, carried to the user as a
`tools.ActionRequest` through the runtime's `ActionAsker`), with the decision's duration.
A run's asks go to the user one at a time under the journal's `askMu`, held across the wait,
since the proxy asks from its own goroutines; a record never takes it, and `journalMu` serializes every
journal write, an ask's and a record's (step 4B's record task). A Secret read (5A) is one more
ask, decided under the proxy's write lock like a write.
`tool_calls.sandboxed` stays what it is, and `tool_calls.network` says whether a command had the
internet and why (4C). `chats.sandbox_disabled` is step 1B's switch, `chats.network_enabled` 4C's.

**The wire.** `approvalDecide` takes the decision. Settings are read and written through
queries and mutations named `sandbox…`, `permission…`, `network…`, `folder…`, `monitor…` and
`proposal…`, each introduced by the step that needs it; the chat's
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
| 1B | **Outside the sandbox is the user's choice.** The model's `dangerouslyDisableSandbox` flag goes. A per-chat switch, off by default, runs that chat's commands outside the sandbox, each asking as today. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | No chain of approvals leaves the sandbox; the user does, for one chat, on purpose. |
| 1C | **The settings file.** `securityconfig` and `<data>/security.json`: the store every later setting lives in, with no fields yet, opened on every platform. **Landed**; `sidecar/CLAUDE.md` describes it. | Every setting of the sandbox, the proxies and the permissions has one home no sandboxed command reads. |

Seam: 1A and 1B both change `tools/bash`, 1A how a sandboxed run's policy is built and 1B which
calls are sandboxed; they touch different functions, and 1B's `sandboxerFor(rt)` answers the
sandboxer whose `Command` 1A changes. 1B and 1C each add a few lines to `app.New`.

**Wave 2** — needs wave 1.

| Spec | Step | After it |
| --- | --- | --- |
| 2A | **The sandbox's own environment.** The zone lists become `Lists`, with the other users' homes on `Never` and `~/Documents`, `~/Desktop` and `~/Downloads` `Closed`; the environment is built from one table, the note's pass-through list plus the run's own paths and the toolchains' variables, and `sandbox.NeverEnv` refuses a run that would carry a name it lists; the `*_HOME` redirects into a per-chat tool home; a sandboxed run neither waits for nor sources the snapshot. **Landed**; `sidecar/CLAUDE.md` describes it. | Nothing in the user's environment reaches a sandboxed command, and a tool that writes under the home writes a cache Kstack owns. |
| 2B | **Process limits and restrictions.** Memory, open files, core size, a process count over what the kernel counts against the run (512 processes on macOS, 128 tasks per CPU and at least 1024 on Linux), and CPU time for a foreground run alone, set on the run by `Policy.Limits` and applied by the run's first processes: the forwarder, which becomes every run's first process, sets CPU, files and core size, and on macOS the process count; `sandbox-shell` sets memory and, on Linux, the process count; `sudo` refused on both platforms, tracing refused, and the kernel keyring on Linux. **Landed**; `sidecar/CLAUDE.md` describes it. | A runaway command cannot take the machine, and no command escalates. |
| 2C | **Sessions.** A `Session` holds an agent run's kind and switch; a chat's turn makes one, a subagent narrows its parent's, and the run's token maps to it through its grant. No behavior changes. **Landed**; `sidecar/CLAUDE.md` describes it. | Every policy question has one place to ask, and a subagent can never hold more than its parent. |

Seams: 2A and 2B both change the compiled arguments and profile and their goldens; the step
that lands second regenerates them. 2A, 2B and 2C each change `sandboxedRunFor`. Each spec says
what it does in either order.

**Wave 3** — needs waves 1 and 2.

| Spec | Step | After it |
| --- | --- | --- |
| 3A | **`PATH` from the login shell.** The user's `PATH` is resolved from their login shell, filtered, frozen in `securityconfig`, diffed at each launch with a confirmation for a new entry that would open more, refreshed on request, and shown in Settings. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | The sandbox finds the tools the user's shell finds, and no startup file widens it unseen. |
| 3B | **The permissions engine.** Classes, modes, rules and `Decide`; the Kubernetes classifier assigns class 4 and 5; the cluster proxy asks `Decide` before each write; modes per context in `securityconfig`; the Settings section. Needs 2C and 1C. **Landed**; `sidecar/CLAUDE.md` describes it. | A cluster write runs, asks or is refused by the user's mode and rules, and class 5 is never allowed unasked. |
| 3C | **A prompt is a denial the user may lift.** `Decide` becomes `Authorize`, a binary verdict in which a forbid wins and nothing matching denies, then `Outcome`, which puts a denial the user may lift to them and refuses one they may not. No answer a rule Kstack reads today can get changes. Needs 3B. **Landed**; `sidecar/CLAUDE.md` describes it. | Steps 4B, 4C, 5A and 6B name a verdict where they now reason about `Decide`'s branch order, and no answer can write a rule that reaches past a forbid. |

Seam: 3A and 3B both add fields to `securityconfig.Settings`, and both need the store's `Held`:
the step that lands first adds it, the other uses it.

**Wave 4** — needs waves 1 to 3.

| Spec | Step | After it |
| --- | --- | --- |
| 4A | **The login shell runs in the sandbox.** The `PATH` resolution and the shell snapshot run confined: everything but Kstack's folders readable, and for the resolution, whose answer leaves the sandbox, the denied-always list shut too; nothing writable but a scratch folder, no network. Needs 1A and 3A. **Landed**; `sidecar/CLAUDE.md` describes it. | A startup file cannot read `app.db` or reach the network while Kstack runs it. |
| 4B | **The prompt names the action.** A request draws the classified action, a diff for an apply or a patch, and five answers: once, this command, this chat, always, deny. "Always" writes a rule. Needs 3B and 3C. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | The user reads "Delete pods/api-7f9c in team-a on dev-eks" and decides for the scope they see. |
| 4C | **Network is the user's switch.** A sandboxed command has no network unless the user turned it on: a per-chat switch, a toggle for one turn, or an approval of one command that asks for it. On, it reaches the internet with nothing blocked but the machine's loopback; `trustd` and DNS on macOS only then; `pasta` on Linux. Every call records whether it had network and why. Needs 1B, 2B, 2C and 3B. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | `helm repo update` works in a chat the user opened to the network, and a chat they did not reaches nothing. |
| 4D | **Path grants.** The user grants a folder, read or read-write, for a chat or always; the denied-always list still wins; the Settings section. Needs 1A, 2C, 3A, 3B and 3C. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | The agent can see `~/code/my-service` because the user said so, and `~/.ssh` under a granted `~` stays hidden. |

Seams, each said in both specs' own text:

- 4B and 4D each write a `chat_grants` row: 4B landed `addGrant`, `removeGrant` and their
  statements, the `chatGrants` query and `chatGrantRemove` mutation, and the composer's
  *Allowed for this chat* list, where every chat rule is seen and removed. `addGrant` inserts a
  rule with no id and replaces the rule under an id the chat holds, which is how 4D changes a
  folder's mode in place. 4D's folder rows in that list add a refused folder's reason.
- 4B and 4D each add fields to `permissions.Rule`: 4B landed `Command`, and 4D adds `Folder`
  beside it, its classes to `ruleClasses` and a case to `ruleRefusal`.
- 4C and 4D both change `sandboxedRunFor` and `workspacePolicy`: 4C the policy's `Internet`, 4D
  the Files rules a grant adds. They touch different parts of the policy, and the second keeps
  the first's.
- 4B and 4C both change the request: 4B the cluster write's, 4C a command's when it asks for
  network (*Run this command with network access?*), a call's own request with Once and Deny
  alone. Neither changes what the other draws.
- 4C and 4D both draw beside the composer's sandbox switch: 4C the network switch and the
  turn's toggle, 4D (through the chat grants list) the chat's folders. The second keeps the
  first's place.
- 4C and 4D both hand the gate's decision to the run: 4C `Approval.Network`, 4D
  `Approval.Folder`. Whichever lands first adds `tools.ApprovedRunner` and the agent loop's call
  to it; the second adds its field.
- 4C and 4D both widen the question's `Sandbox` context section: 4C its `network`, 4D the
  chat's folders. Whichever lands first makes `withSection` take a `map[string]any` and
  `withSandboxReplaced` find the section by its heading; the second adds its keys.
- 4A and 4C both touch `trustd`: 4C allows it only under `sandbox.NetworkPolicy.Internet`, and
  4A's login shell leaves that false. Whichever lands second sets or leaves the field.
- 4A and 4D both edit the denied-always row of `security-model.md`, 4A for the snapshot's run
  and 4D for grants; the second keeps the first's words. 4D refuses a read-write grant over the
  startup files 4A's shell reads, so no grant lets a command write what runs outside the sandbox.
- 4D makes Kstack's log directory one of its directories (`app.Config.LogDir`); 4A's `main`
  reads Kstack's directories from `cfg.App`, and whichever lands second adds `LogDir` there.

**Wave 5** — needs waves 1 to 4.

| Spec | Step | After it |
| --- | --- | --- |
| 5A | **Secret data is a permissioned read.** Class 6: a read of Secret data asks, and the proxy redacts unless the session holds the grant; a session can be one that never reads it. Needs 3B, 3C and 4B. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | A Secret's values reach the model only after the user says so, and never in a monitoring session. |
| 5B | **Grant a folder from a failed command.** A failed sandboxed command offers *Grant a folder…* under the call: the user types the folder, for the chat or always, in the form Settings' Add row becomes. Nothing parses the output, and the model's result is unchanged. Needs 4D. **Landed**; the root and `sidecar/` `CLAUDE.md` describe it. | A user who reads a blocked command's error can open the folder where it failed, and the closed home becomes how users find the permission system. |

Seam: 5A and 5B both edit `chat-transcript.tsx` and `prompts/sandbox.md`, in different
places — 5A the cluster-change request's label and the prompt's Secret lines, 5B the offer
after a call's disclosure and the prompt's folder line; the second keeps the first's lines.

**Wave 6** — needs waves 1 to 5.

| Spec | Step | After it |
| --- | --- | --- |
| [6A](6a-tool-probing.md) | **Tool probing.** The curated tools are probed in the real sandbox at onboarding and after a `PATH` refresh; each reports the binary it resolved to and the paths it was denied — found from Seatbelt's reports on macOS and the output on both, through a new `Policy.Explain` — with a grant button; the user registers more tools; a missing `kubectl` is said plainly. Needs 3A, 4D and 5B. | A `helm` plugin works in the sandbox after one click, and the user knows which `kubectl` the agent runs. |
| [6B](6b-the-monitoring-session.md) | **The monitoring session.** A `monitor` session: read-only at the cluster proxy, no network, Secret data never, no prompts, its own workspace, no folder grants; a proposal card into chat, whose "Do it" runs the action in a chat session. Needs 4C and 5A. | A monitoring agent can plug in and change nothing without a human. |

6A meets 4D across waves: the probe has no chat, so its run reads `foldersFor(ctx, "")`, the folders
granted always alone, and a folder granted from its report reaches the next probe.

**Wave 7** — needs waves 1 to 6.

| Spec | Step | After it |
| --- | --- | --- |
| [7A](7a-onboarding.md) | **Onboarding.** One flow on first launch: the `PATH` list, the probes, and the approval mode. Needs 3A, 3B, 5B and 6A. | A new user's tools work in the sandbox with nothing typed and nothing signed into. |

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
| 1C | no | no | `security.json` joins the owner-only files row |
| 2A | yes | yes | the sandbox rows, the environment row, the snapshot row, a row for the tool home |
| 2B | no | yes | rows for the limits and the core size, the setuid programs, tracing with its `/proc` residual, and the keyring |
| 2C | no | no | a row for a subagent's session never being looser than its parent's; the KubeQuery and outside-the-sandbox rows' citations |
| 3A | yes | yes | the shell-import and sandboxed-command rows say the frozen list |
| 3B | yes | yes | the cluster-write rows say `Decide`; the bash tool record's consent line amended |
| 3C | no | yes | the cluster-write rows cite the verdict's tests |
| 4A | yes | yes | the snapshot and shell-import rows; the denied-always row names the snapshot's run; **By decision** rows for what the shell's output shapes outside the sandbox and the `-c` startup files; the note's denied-always paragraph |
| 4B | yes | yes | the request row; the rules row |
| 4C | yes | yes | the network row says the user's switch, with the tests; a **By decision** row: a command with network can send what it read to any server; rows for the loopback refusal and no credential for any host; the Mach services row (`trustd.agent` and DNS only with network); a **By decision** row for the trust daemon's GET to the loopback under network |
| 4D | yes | yes | the file row; the denied-always row; the file tools' rows; a row for the monitor holding no folder |
| 5A | yes | yes | the Secret redaction row; the helm release row; the cluster-reads **By decision** row; a row for `NoSecretData` |
| 5B | no | no | the path-grants row names the transcript's offer |
| 6A | no | no | a row: the probe runs only the listed invocations, and its denials reach the user alone |
| 6B | yes | yes | rows for the monitor session, its folder and the proposal |
| 7A | no | no | one line under the sandbox rows |

## When it lands

Once wave 7 has landed, the design is true of the code. Fold what each spec's *When it lands*
lists into the `CLAUDE.md` files, write the ADRs the specs name, update the note's *Where this
meets the code* to say it has landed, delete this directory, and remove the sequence's row from
the index.
