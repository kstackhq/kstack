---
title: Tool probing
scope: sidecar, webview
status: Planned
---

# Tool probing

**Needs:** step 3A, whose frozen `PATH` the probe resolves names on and whose `securityconfig`
keeps the registered tools, and step 5B, whose denial finding says what a probe was refused.
**Unblocks:** step 7A, which shows the probe's report at onboarding.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the first sign that a tool does not run in the sandbox is a failed command in a chat:
`gcloud` cannot read its install tree, an `asdf` shim execs something the sandbox hides, or
`kubectl` is simply not there. The model reads a cryptic error and the user learns nothing
until they ask.

After this step, Kstack **probes** the tools the agent will call before any chat does, as
[the note](../../notes/sandbox-credentials-and-permissions.md)'s *Dependency probing* asks:

- **A curated list** of tools, each with one safe invocation (`kubectl version --client`), plus
  the tools the user registers in Settings with an invocation of their own.
- **Each name is resolved on the frozen `PATH`**, the list step 3A keeps, so the report says which
  binary the agent runs when several are installed. A shim is reported as one, with the binary
  behind it.
- **Each invocation runs in the real sandbox**, with no cluster and no network, under step 5B's
  denial finding, so the report says what the tool could not read.
- **The user sees it in Settings**: the resolved path, the version, and each denied path with
  step 5B's grant, always only. A missing `kubectl` is said plainly, in Settings and at
  onboarding (step 7A).

The probe runs at onboarding, after a `PATH` refresh, on demand from Settings, and in the
background at launch when the `PATH` sync changed anything. The report lives in memory, not in
the settings file: a probe answers the machine as it is now.

## What is not in this step

- **No onboarding screen.** Step 7A draws the report there; this step gives it the wire.
- **No grant of its own.** A denied path is granted through step 4D's always grants, offered by
  step 5B's popover.
- **No probe of a chat's tools.** A command in a chat is confined and reported as step 5B has it;
  the probe is the same run, started by the app rather than the model.
- **No change on Windows**, which has no sandbox.

## Design

### 1. The list

`tools/bash/probe.go` holds the curated list and the probe. It lives in the Bash tool because a
probe is a Workspace run with no cluster: the policy `sandboxedRunFor` builds, the environment
`sandboxedRunEnv` builds, the frozen `PATH`. A tool that runs in the probe runs in a chat, and
the reverse; a package of its own would rebuild both, and the two would drift.

```go
// ToolSpec is one tool the probe may run: a plain program name and the one
// invocation it runs, never through a shell.
type ToolSpec struct {
	Name       string
	Invocation string // split on whitespace; its first field is Name
	Registered bool   // by the user, in Settings
}
```

The curated list, in this order:

| Name | Invocation |
| --- | --- |
| `kubectl` | `kubectl version --client` |
| `helm` | `helm version` |
| `kustomize` | `kustomize version` |
| `aws` | `aws --version` |
| `gcloud` | `gcloud --version` |
| `az` | `az version` |
| `gh` | `gh --version` |
| `git` | `git --version` |
| `jq` | `jq --version` |
| `yq` | `yq --version` |
| `aws-iam-authenticator` | `aws-iam-authenticator version` |
| `kubelogin` | `kubelogin --version` |
| `gke-gcloud-auth-plugin` | `gke-gcloud-auth-plugin --version` |

Then the user's registered tools, from `securityconfig.Settings`, which gains:

```go
type Settings struct {
	// …
	Tools []Tool `json:"tools,omitempty"` // the user's registered tools, in the order added
}

// Tool is a program the user asked the probe to check.
type Tool struct {
	Name       string `json:"name"`
	Invocation string `json:"invocation"` // "<name> --version" when the user gave none
}
```

`securityconfig.CheckTool(name, invocation)` is the one shape check, run on register and on
read-back: a name is one to 64 bytes of `[A-Za-z0-9._+-]`, not starting with `-`, with no path
separator; an invocation is one to 8 fields, split by `strings.Fields`, each under 256 bytes with
no control character, whose first field is the name. A curated name is refused, since it is
already probed. A tool the file holds that fails the check is left out, logged, and shown in
Settings with its reason, as step 3B's rules are. The invocation is split once and never run
through a shell: the probe runs the resolved binary with the remaining fields as its arguments.

Beside the list, two **resolvers** the probe may run and never probes: `asdf which <name>` and
`mise which <name>` (§2). `probeCommands` is the one function that turns a spec into the argv
the sandbox runs, and it answers nothing for a name off the list.

### 2. Resolution

`resolveTool(name, path []string) (resolved string, ok bool)` looks the name up on the frozen
`PATH` as step 3A resolves it for a run — each adopted entry in order — and answers the first
regular, executable file named `<entry>/<name>`. It never walks an entry's contents. A name found
in more than one entry reports the one that wins, so a user with several `kubectl` installs
knows which one the agent uses.

A resolved file is a **shim** when it lies under `~/.asdf/shims/` or
`~/.local/share/mise/shims/`, or when its first two bytes are `#!`. For a shim under a version
manager's folder the probe runs that manager's resolver inside the sandbox — `asdf which <name>`
or `mise which <name>`, the manager found on the same frozen `PATH` — and takes the first line of
its output as `target`, the binary the shim execs. A `#!` script anywhere else is a shim with no
target. The resolver runs under the probe's policy (§3), so a manager that cannot read its own
data directory reports a denial like any tool.

### 3. The probe

```go
// ToolReport is what one probe of one tool answered.
type ToolReport struct {
	Name       string
	Invocation string
	Registered bool
	Resolved   string   // the binary on the frozen PATH; "" when none
	Shim       bool
	Target     string   // the binary behind a shim; "" when unknown
	OK         bool     // the invocation exited 0
	Version    string   // the first non-empty line of its output, cut to 200 characters
	Denied     []string // the paths the sandbox refused, as step 5B found them
	Error      string   // why it did not run or did not pass; "" when OK
}

// ProbeTools runs each spec's invocation in the sandbox, one after another,
// and answers one report per spec in the list's order.
func (t *Tool) ProbeTools(ctx context.Context, specs []ToolSpec) []ToolReport
```

Each invocation is one Workspace run with no cluster:

- **Its policy is `sandboxedRunFor`'s** (`workspacePolicy`, as steps 2A, 2B, 3A and 4D leave it), built
  for a throwaway workspace `<cache>/tmp/<pid>-probe-*/workspace` and a `TMPDIR` beside it,
  both under the cache directory so `Check` accepts them, removed after the run through
  `rootdir.RemoveAll`. It names no relay: no cluster, no egress, so a tool's update check gets
  nothing by the policy, not by the tool's manners.
- **Its environment is `sandboxedRunEnv`'s** with no cluster, `HOME` the throwaway workspace, the
  frozen `PATH`, and none of step 4C's or step 5C's variables, since the run has no relay.
- **Its argv** is the resolved binary and the invocation's remaining fields, bounded by
  `probeTimeout` (15 s) and step 2B's limits.
- **Denials are read** as step 5B reads them for a command, so `Denied` is what a chat would
  have been told, explained by `sandbox.Policy.Explain` into the folder step 5B's popover offers.

`Version` and `Error` are the binary's own text: through `safe.Redact`, cut, and drawn through
`VisibleText` in Settings. A name not resolved is `Error: "not found on the sandbox's PATH"`;
a non-zero exit is `OK` false with the sidecar's first line as Bash writes it (`Exit code N`,
`Command timed out …`); one that could not start is `could not start: <reason>`.

The runs go one at a time, since step 5B finds denials per run and a dozen tools take seconds.
`Tool.probe` (a mutex and the last report) lets one probe run at once: a second `ProbeTools`
joins the one in flight and answers its result.

**When it runs.** The last report is `Tool.LastProbe()`, nil before the first, and a probe starts:

| When | Who | How |
| --- | --- | --- |
| at onboarding | step 7A | `sandboxToolsProbe` |
| after Refresh PATH | the Settings section | `sandboxToolsProbe`, once `sandboxPathRefresh` answers |
| on demand | the Settings section's *Probe again* | `sandboxToolsProbe` |
| at launch | `app.Start`, after `SyncPath` | on a goroutine under the app's lifecycle, only when the sync's `PathReport` changed anything |

Nothing is written to `security.json` by a probe. A restart starts with no report, and the launch
probe runs only when the `PATH` moved, so nothing runs unasked on a machine whose tools did not
change.

### 4. The grant

A denied path is drawn with step 5B's `grant-popover.tsx`, offered **always** alone: a tool's
need is the machine's, not a chat's. The popover's grant writes step 4D's always rule, and
*Probe again* runs the list once more, so the user reads the tool go from denied to `OK` in the
same section.

A missing `kubectl` is one line at the top of the Tools part, *kubectl was not found on the
sandbox's PATH. Install it, or include its folder above.*, and the same line at onboarding
(step 7A) — said plainly, as the note asks, rather than at the first failed chat.

### 5. The wire

```graphql
"One tool the sandbox probe checked."
type SandboxTool {
  name: String!
  invocation: String!
  registered: Boolean!
  "The binary on the sandbox's PATH; empty when none."
  resolved: String!
  shim: Boolean!
  "The binary behind a shim; empty when unknown."
  target: String!
  ok: Boolean!
  version: String!
  "The paths the sandbox refused the tool, each one a grant resolves."
  denied: [String!]!
  error: String!
}

extend type Query {
  "The last probe's report, in the list's order. Empty before the first probe, and on a machine with no sandbox."
  sandboxTools: [SandboxTool!]!
}

extend type Mutation {
  "Probe every listed tool in the sandbox and answer the report; joins a probe already running. Refused KSTACK_VALIDATION_ERROR on a machine with no sandbox."
  sandboxToolsProbe: [SandboxTool!]!
  "Register a tool. Refused KSTACK_VALIDATION_ERROR for a name or invocation out of shape, one already listed, or on a machine with no sandbox."
  sandboxToolRegister(name: String!, invocation: String): [SandboxTool!]!
  "Remove a registered tool. Refused KSTACK_VALIDATION_ERROR for a name not registered."
  sandboxToolRemove(name: String!): [SandboxTool!]!
}
```

Register and remove write `Settings.Tools` through the store's `Update` and answer the last
report with the new tool appended unprobed (`resolved` and `version` empty, `ok` false, `error`
*not probed yet*), or removed, so the section redraws from one result; the user presses *Probe
again* to run it. `graph.Resolver` reads the report off `SecurityCfg` and the Bash tool it is
given; on a machine with no sandbox (step 1B's `SandboxStatus`) the query answers an empty list and the
mutations are refused, as step 3A's are.

### 6. Settings: Sandbox, Tools

`sandbox-settings.tsx` (step 3A) gains a **Tools** part under the `PATH` list, its reader
`useSandboxTools()` in `src/lib/sandbox-tools.tsx`, the one reader of the query and the three
mutations:

- The `kubectl` line of §4 when `kubectl` is not resolved.
- One row per tool, in the report's order: the name; the resolved path in mono through
  `VisibleText`, or *not found*; *shim → <target>* when it is one; the version through
  `VisibleText`; a tag, *ok* or *failed*, and the error under it; and under a row with denials
  one line per path, *could not read <path>*, each with step 5B's popover offering always.
- **Probe again** above the rows, disabled while the mutation is in flight, *Probing…* with a
  spinner in its place. Before the first probe the rows are the curated names, *not probed yet*.
- **Registered tools** under the rows: each with Remove; an Add form with a name and an
  invocation (placeholder `<name> --version`), calling `sandboxToolRegister`, its refusal drawn
  under the field.
- One line under the part: *Kstack runs each tool once in the sandbox, with no cluster and no
  network, to see what it needs.*

### 7. Windows

No sandbox: `sandboxTools` answers an empty list, the mutations are refused, and the part is
not drawn.

## Decisions this step asks for

1. **The probe lives in `tools/bash`, not in a package of its own.** A probe is a Workspace run
   with no cluster, and the run's policy and environment are built there. Recommended; §1 says
   why.
2. **A registered tool's name may not be a curated one.** The user may want `kubectl` probed
   with another flag; they can add a differently named copy instead. One row per name keeps the
   list and the report readable. Recommended.
3. **The report is memory, not the file.** A probe answers the machine now; a stored report
   would say what was true at some earlier launch. The cost is a launch with no report until
   something moves `PATH` or the user asks. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Settings.Tools`, `CheckTool`, the read-back check | `securityconfig/tools.go`, its test | — | Planned |
| 2 | The list, `resolveTool`, the shim rule, `probeCommands` | `tools/bash/probe.go`, its tests | — | Planned |
| 3 | `ProbeTools`: the run, the report, one at a time, `LastProbe` | `tools/bash/probe.go`, `tools/bash/bash.go`, their tests | 2 | Planned |
| 4 | The launch probe after `SyncPath` | `app/app.go`, its test | 3 | Planned |
| 5 | The wire and the resolvers | `sidecar/graph/schema.graphqls`, `graph/`, generated code | 1, 3 | Planned |
| 6 | Codegen, `useSandboxTools`, the Tools part | `src/gql/`, `src/lib/sandbox-tools.tsx`, `src/components/widgets/sandbox-settings.tsx`, their tests | 5 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4 and 5 at the same time, then 6, then 7.

## Tests

**`securityconfig`**

- `TestCheckToolRefusesEachBadShape`: a name with a slash, one starting with `-`, one past 64
  bytes, a curated name, an invocation of nine fields, one whose first field is not the name,
  one with a control character; and `kubectl-foo --version` passes.
- `TestABadToolIsLeftOutWithItsReason`: a file holding one is read back without it, and the
  reason is reported.

**`bash`**

- `TestTheProbeRunsOnlyTheList`: over a fake sandbox recording every `Run`, `ProbeTools` runs
  each curated invocation and the registered ones, and `probeCommands` answers nothing for a name
  off the list. A binary planted beside a listed one on the fixture `PATH` is never run.
- `TestAToolResolvesToTheFirstEntry`: a fixture `PATH` with `kubectl` in two entries reports the
  first; a non-executable file in the first entry is passed over.
- `TestAShimResolvesToItsTarget` (`probe_unix_test.go`): a fixture home with
  `.asdf/shims/kubectl` and an `asdf` whose `which` prints a path reports `Shim` and that
  `Target`; a `#!` script elsewhere reports `Shim` with no target.
- `TestAMissingKubectlIsReported`: a `PATH` with no `kubectl` reports it not found, `OK` false.
- `TestAProbeReportsWhatItWasDenied` (`probe_unix_test.go`, through the real sandbox,
  `testutil.RequireSandbox`): a fake tool registered on a fixture `PATH` that reads a file under
  the home is `OK` false with that path in `Denied`; granted always (step 4D), it is `OK`.
- `TestTheProbesSandboxHasNoClusterAndNoNetwork` (`probe_unix_test.go`): the run's policy names
  no relay, its environment holds no `KUBECONFIG`, `HTTP_PROXY` or `AWS_ENDPOINT_URL`, and a
  listener on loopback outside the sandbox gets no connection from a fake tool that dials it.
- `TestAProbeIsBounded`: a fake tool that sleeps past `probeTimeout` reports the timeout;
  `TestOneProbeRunsAtATime`: a second `ProbeTools` during the first answers the first's report;
  `TestTheProbesWorkspaceIsGone`: the throwaway folders are removed after each run.

**`app`**: `TestTheLaunchProbeFollowsAChangedPath`, a sync whose report changed something
starts a probe in the background, and one that changed nothing starts none.

**`graph`**: `TestSandboxToolMutationsAnswerTheReport`, each refusal a `KSTACK_VALIDATION_ERROR`.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-tools.test.tsx`)

- The rows draw the resolved path, the shim's target, the version and the tag; a denial draws
  its line with the popover offering always alone; the `kubectl` line draws only when it is not
  resolved.
- *Probe again*, Add and Remove call their mutations, are disabled in flight, and redraw from the
  answer; a refused register draws its reason; before the first probe only the names draw.

## Security

**Widened.** The app now runs the user's own binaries on its own initiative, not at the model's
asking. The note puts a malicious binary on the user's `PATH` out of scope — the sandbox runs the
user's tools and cannot protect against them — and the probe runs them where a chat would, in
the real sandbox, with less: no cluster, no relay, a throwaway workspace. What bounds it:
`probeCommands` answers only the curated invocations, the two resolvers and the registered
tools; a registered tool is one the user typed into Settings, shape-checked, never run through
a shell; nothing walks `PATH`; the report is text, redacted and drawn through `VisibleText`.

**Residuals.** A registered tool is whatever the user named, and the probe runs it. A path
granted from the report is granted always, for every chat; the popover says so.

No boundary moves, so no security record. `security-model.md` gains a row: the probe runs only
the listed invocations, in the sandbox, with no cluster and no network, pinned by
`TestTheProbeRunsOnlyTheList` and `TestTheProbesSandboxHasNoClusterAndNoNetwork`.

## When it lands

- **`security-model.md`**: the row above.
- **`sidecar/CLAUDE.md`**: the probe in the Bash tool's section (the list, `resolveTool`, the
  shim rule, `ProbeTools`, one at a time, `LastProbe`), `Settings.Tools` and `CheckTool` under
  `securityconfig`, and the launch probe in `app`.
- **Root `CLAUDE.md`**: the Sandbox section's Tools part and `useSandboxTools`.
- **`docs/TODO.md`**: nothing; the note's probing paragraph is done.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the probe's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS or Linux, on a machine with Homebrew's `kubectl`, `gcloud`
installed outside step 2A's list and an `asdf` shim for `helm`: open Settings, press *Probe
again*, and read `kubectl` resolved to Homebrew's with its version, `helm` as a shim with its
target, and `gcloud` failed with a denied path; grant it always from the popover, probe again,
and read `gcloud` ok. Register `kubectl-foo` with no invocation and read *not found*; rename
`kubectl` away and read the line at the top.
