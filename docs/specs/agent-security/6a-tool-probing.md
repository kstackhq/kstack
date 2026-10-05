---
title: Tool probing
scope: sidecar, webview
status: Planned
---

# Tool probing

**Needs:** step 3A, whose frozen `PATH` the probe resolves names on and whose `securityconfig`
keeps the registered tools; step 4D, whose always grants a denied path is granted through; and
step 5B, whose `FolderGrantForm` a denied path opens.
**Unblocks:** step 7A, which shows the probe's report at onboarding.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the first sign that a tool does not run in the sandbox is a failed command in a chat:
a `kubectl` plugin cannot read its install tree, an `asdf` shim execs something the sandbox hides, or
`kubectl` is simply not there. The model reads a cryptic error and the user learns nothing
until they ask.

After this step, Kstack **probes** the tools the agent will call before any chat does, as
[the note](../../notes/sandbox-credentials-and-permissions.md)'s *Dependency probing* asks:

- **A curated list** of tools, each with one safe invocation (`kubectl version --client`), plus
  the tools the user registers in Settings with an invocation of their own.
- **Each name is resolved on the frozen `PATH`**, the list step 3A keeps, so the report says which
  binary the agent runs when several are installed. A shim is reported as one, with the binary
  behind it.
- **Each invocation runs in the real sandbox**, with no cluster and no network, and the probe
  finds what the sandbox refused it: from Seatbelt's reports on macOS and from the tool's output
  on both platforms, explained by a new `Policy.Explain`.
- **The user sees it in Settings**: the resolved path, the version, and each denied path with
  step 5B's grant form on the folder that would open it, always only. A missing `kubectl` is
  said plainly, in Settings and at onboarding (step 7A).

The probe runs at onboarding, after a `PATH` refresh, on demand from Settings, and in the
background at launch when the `PATH` sync changed anything. The report lives in memory, not in
the settings file: a probe answers the machine as it is now.

## What is not in this step

- **No onboarding screen.** Step 7A draws the report there; this step gives it the wire.
- **No grant of its own.** A denied path is granted through step 4D's always grants, in step
  5B's form.
- **No denial finding for a chat's commands.** A failed command in a chat offers step 5B's grant
  and its model reads its tool's own error; the probe is the same run, started by the app, and
  its findings reach the user alone.
- **No change on Windows**, which has no sandbox.

## Design

### 1. The list

`tools/bash/toolprobe.go` holds the curated list and the probe. It lives in the Bash tool because a
probe is a Workspace run with no cluster: the policy `sandboxedRunFor` builds, the environment
`sandboxedRunEnv` builds, the frozen `PATH`. A tool that runs in the probe runs in a chat, and
the reverse; a package of its own would rebuild both, and the two would drift.

"Probe" here is the tool probe. `sandbox/probe.go` is a different one, the probe of whether the
sandbox works at all; the tool probe's files are `toolprobe*.go` so the two stay apart.

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
| `git` | `git --version` |
| `jq` | `jq --version` |
| `yq` | `yq --version` |

A kubeconfig's `exec` credential plugins (`aws`, `gke-gcloud-auth-plugin`, `kubelogin`) run in
the sidecar, outside the sandbox, so they are not on the list: a probe says what a sandboxed
command can run, and a command never runs them.

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
`~/.local/share/mise/shims/`. Many real tools are scripts, so a `#!` line alone makes nothing a
shim. For a shim the probe runs its manager's resolver inside the sandbox — `asdf which <name>`
or `mise which <name>`, the manager found on the same frozen `PATH` — and takes the first line of
its output as `target`, the binary the shim execs. The resolver runs under the probe's policy
(§3), so a manager that cannot read its own
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
	Denied     []sandbox.Denial // what the sandbox refused, as §4 finds it
	Error      string   // why it did not run or did not pass; "" when OK
}

// ProbeTools runs each spec's invocation in the sandbox, one after another,
// and answers one report per spec in the list's order.
func (t *Tool) ProbeTools(ctx context.Context, specs []ToolSpec) []ToolReport
```

Each invocation is one Workspace run with no cluster, built by `sandboxedRunFor` itself from a
probe runtime:

- **Its runtime** is `tools.Runtime{Session: s, Dir: d}`, with no `ClusterID` and no `ChatID`, and
  `network` `session.NoNetwork`. `d` is a `probeDir`, a throwaway `tools.ChatDir` at
  `<cache>/tmp/<pid>-probe-*/`, so `tools.WorkspacePath(d)` and `tools.ToolHomePath(d)` are fresh
  for each run, as a new chat's are; the run's own `TMPDIR` is its run directory's, as for any
  run. Both lie under the cache directory, so `Check` accepts them, and the probe removes `d`
  through `rootdir.RemoveAll` after the run.
- **Its policy is `sandboxedRunFor`'s** (`workspacePolicy`, as steps 2A, 2B, 3A and 4D leave
  it). With no cluster it makes no claim, no proxy and no relay, and with `NoNetwork` it leaves
  `Internet` false (step 4C): a tool's update check gets nothing by the policy, not by the
  tool's manners.
- **Its session** `s` is `session.Session{Folders: t.probeFolders}`: no `Policy`, since the run
  has no cluster, and no `Network`. `probeFolders` is the folders granted always, and no chat's,
  since a probe has no chat: `chatsvc.AlwaysFolders(ctx)`, which is `foldersFor(ctx, "")` (step
  4D) exported, handed to the Bash tool by `app` through `SetProbeFolders` once `chatsvc` is
  built, since the tool is built first. So a folder granted from a report reaches the next
  probe.
- **Its environment is `sandboxedRunEnv`'s** with no cluster: `HOME` the throwaway workspace,
  the tool home's variables, the frozen `PATH`, and no cluster variables, since the run has no
  relay.
- **Its argv** is the resolved binary and the invocation's remaining fields, bounded by
  `probeTimeout` (15 s) and step 2B's limits.
- **Denials are found** as §4 says, each with the folder a grant would open.

`Version` and `Error` are the binary's own text: through `safe.Redact`, cut, and drawn through
`VisibleText` in Settings. A name not resolved is `Error: "not found on the sandbox's PATH"`;
a non-zero exit is `OK` false with the sidecar's first line as Bash writes it (`Exit code N`,
`Command timed out …`); one that could not start is `could not start: <reason>`.

The runs go one at a time, since §4 finds denials per run and a dozen tools take seconds.
One probe runs at once. A `ProbeTools` asked for while one runs waits for it to end, then every
caller that waited shares one more run, which starts after all of them asked. So a press after
a grant or a register never answers a report begun before it, and a burst of presses runs
twice, not once per press.

**When it runs.** The last report is `Tool.LastProbe()`, nil before the first, and a probe starts:

| When | Who | How |
| --- | --- | --- |
| at onboarding | step 7A | `sandboxToolsProbe` |
| after Refresh PATH | the Settings section | `sandboxToolsProbe`, once `sandboxPathRefresh` answers |
| on demand | the Settings section's *Probe again* | `sandboxToolsProbe` |
| at launch | `app.Start`, after `SyncPath` | on a goroutine under the app's lifecycle, only when the sync changed the stored list |

`SyncPath` answers `(changed bool, err error)`: `changed` is whether the list it wrote differs from
the one it read, which it already holds as `before` and `after`. `RefreshPath` ignores it.

Nothing is written to `security.json` by a probe. A restart starts with no report, and the launch
probe runs only when the `PATH` moved, so nothing runs unasked on a machine whose tools did not
change.

### 4. What a probe was denied

A probe's denials reach the user alone: the run is the app's, its report goes to Settings and
onboarding, and no model reads it. So the probe may read the disk to name a folder, and may
read the machine's log, where a chat's command does neither (step 5B).

**`Explain`.** `sandbox/policy.go`:

```go
// Explain answers the rule that decides path, resolved as the compilers
// resolve it: Kstack for one of Kstack's directories the run's own paths do
// not open, Never for a denied-always path, Read or Write for a rule that
// opens it, Deny for a Files Deny, None for no rule at all.
func (p Policy) Explain(path string) Rule
```

It walks `allRules()`, the rules in the order both platforms compile them, so its answer is the
sandbox's: the last matching rule wins. `rules()` becomes `allRules()` with its Deny pruning:
the compilers need no Deny that no Read or Write reaches, but `Explain` does, since under the
default policy that is how the real home's `.ssh` and Kstack's directories are denied.
`allRules()` holds `Always.Deny` and `Always.Kstack` alike as a Deny, so `rule` gains `always`,
which says which list a Deny came from. `Rule` is `{Kind, Path}`, `Kind` one of `Read`, `Write`,
`Deny`, `Never`, `Kstack` and `None`. A path is denied where the kind is `Deny`, `None` or
`Never`, or `Read` for a write.

**macOS: Seatbelt's reports.** Seatbelt reports every denial to the unified log, and a `deny`
rule takes `(with message "…")`, whose text rides the report. Checked on macOS 27.0.1:

```
Sandbox: cat(45605) deny(1) file-read-data /Users/x/.ssh/config
kstack:0193b2e4-…
```

is one log event, sender `Sandbox`, `eventMessage` holding both lines. `log show` does not
return these events; `log stream` delivers them at once.

- **The profile tags every deny.** `Run` gains `Tag`, a random hex id per run, and every `deny`
  rule the compiler writes carries `(with message "kstack:<tag>")`. The compiled rules that
  replace `;; RULES` open with `(deny file-read* file-write* (with message "kstack:<tag>"))`.
  No file allow comes before the marker, so every allow after it overrides that deny as the
  last match does, and a file no rule names is denied with the tag whether or not `(deny
  default)` takes a message. A run with no `Tag` compiles as today.
- **One `log stream` per sidecar**, `sandbox.Reports` in `sandbox/reports_darwin.go`, started
  with the first probe and stopped with the app: `/usr/bin/log stream --style ndjson
  --predicate 'sender == "Sandbox" AND eventMessage CONTAINS "kstack:"'`. `Subscribe(tag)
  (<-chan Report, func())` hands a run its own reports; a tag nobody subscribed to is dropped,
  and a full buffer (64) drops with a count. `sandbox.ParseReport(eventMessage) (Report, bool)`
  reads `Sandbox: <name>(<pid>) deny(<n>) <operation> <path>` and the tag on the next line, and
  rejects the `N duplicate reports for …` form.
- **A probe subscribes before it starts**, reads until its reap, then for `reportDrain` more
  (500ms in production, a parameter in tests), since a report reaches the stream through `logd`
  after the denial and a tool that exits on one is reaped first. It keeps reports whose
  operation is `file-read-data` or starts with `file-write`: a tool stats and looks up what it
  never opens on every launch, and those would crowd out what it needed.
- A `log` that will not stream — it may refuse a user who is not an admin — is logged once,
  and probes then find their denials from the output alone.

**Both platforms: the output.** `sandbox.ExplainOutput(policy, output) []Denial` in
`sandbox/denials.go` reads the probe's stdout and stderr, at most the last 256 KiB. On Linux it
is the only source: bubblewrap's mounts deny nothing that logs, and a hidden path is simply not
there. A denial is a line holding `Permission denied`, `No such file or directory`, `Operation
not permitted`, `Read-only file system`, `EACCES`, `ENOENT`, `EPERM` or `EROFS`, with an absolute
path token on it — one that starts the line or follows whitespace, a quote, `(`, `=` or `: `,
never inside `://`, and runs to the next whitespace, quote or `:` — where `Explain` says the path
is denied. A read-only line is a write. A `~` or `$HOME` token is the probe's throwaway
workspace, and a relative path names a directory the line does not say, so neither is read.

**What is never reported:** a path in Kstack's directories, a path under `sandbox.FixedMount`
(the run's own `/tmp`, `/proc` and `/dev`), and a path a `Noise` list names: a zone list beside
`Never` in `sandbox/lists_darwin.go` and `lists_linux.go` of paths a platform's programs probe on
every launch, on macOS the real home's `Library/Preferences`, on Linux none.

**A path missing from the disk is never a denial.** The sidecar stats each path outside the
sandbox, and drops one that is not there: the tool's error is then its own, whatever the
sandbox would have said, and offering a grant of the nearest folder above it would offer the
home for a tool looking for an optional `~/.foorc`. Seatbelt reports a lookup of a missing path
under a deny like any other, so the rule applies to both sources.

**A denial.** Each is `sandbox.Denial{Path, Folder, Never, Write}`, the path absolute; at most
`maxDenials` (8) per probe, first seen, and a path both sources name is one. `Folder` is the
folder a grant names, resolved: the path itself when it is a directory, else the directory
holding it; `""` for a `Never` path. The probe then runs it through
`securityconfig.Service.CheckFolder(ctx, folder,
write)` and blanks one it refuses; `sandbox` cannot, since `securityconfig` imports it.

### 5. The grant

A denied path with a folder draws *Grant…*, which opens step 5B's `FolderGrantForm` in a popover
with the folder filled in, *read and write* checked for a write, and **always** alone: a tool's
need is the machine's, not a chat's. The user reads the folder and can change it before
granting; a wide one (the home, say, for a hidden file directly under it) draws the form's wide
warning before Add, as it does in Settings. The grant writes step 4D's always rule, and
*Probe again* runs the list once more, so the user reads the tool go from denied to `OK` in the
same section.

A missing `kubectl` is one line at the top of the Tools part, *kubectl was not found on the
sandbox's PATH. Install it, or include its folder above.*, and the same line at onboarding
(step 7A) — said plainly, as the note asks, rather than at the first failed chat.

### 6. The wire

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
  "What the sandbox refused the tool."
  denied: [SandboxToolDenial!]!
  error: String!
}

"One path the sandbox refused a probed tool."
type SandboxToolDenial {
  "The path, absolute."
  path: String!
  "The folder a grant would name, absolute; null when no grant opens it."
  folder: String
  "On the denied-always list: nothing opens it."
  never: Boolean!
  "A write was refused."
  write: Boolean!
}

extend type Query {
  "The last probe's report, in the list's order. Empty before the first probe, and on a machine with no sandbox."
  sandboxTools: [SandboxTool!]!
}

extend type Mutation {
  "Probe every listed tool in the sandbox and answer the report; waits out a probe already running, then runs. Refused KSTACK_VALIDATION_ERROR on a machine with no sandbox."
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

### 7. Settings: Sandbox, Tools

`sandbox-settings.tsx` (step 3A) gains a **Tools** part under the `PATH` list, its reader
`useSandboxTools()` in `src/lib/sandbox-tools.tsx`, the one reader of the query and the three
mutations:

- The `kubectl` line of §5 when `kubectl` is not resolved.
- One row per tool, in the report's order: the name; the resolved path in mono through
  `VisibleText`, or *not found*; *shim → <target>* when it is one; the version through
  `VisibleText`; a tag, *ok* or *failed*, and the error under it; and under a row with denials
  one line per path through `VisibleText`, *could not read <path>* (*could not write* for a
  write, *never readable* for a `never` one), each with *Grant…* when it has a folder.
- **Probe again** above the rows, disabled while the mutation is in flight, *Probing…* with a
  spinner in its place. Before the first probe the rows are the curated names, *not probed yet*.
- **Registered tools** under the rows: each with Remove; an Add form with a name and an
  invocation (placeholder `<name> --version`), calling `sandboxToolRegister`, its refusal drawn
  under the field.
- One line under the part: *Kstack runs each tool once in the sandbox, with no cluster and no
  network, to see what it needs.*

### 8. Windows

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
4. **Only the probe finds denials.** Its report reaches the user alone, so reading the disk to
   name a folder tells no model anything; a chat's command offers step 5B's grant instead.
   Recommended.
5. **macOS reads a stream, tagged per run.** `log show` does not return Seatbelt's reports;
   `log stream` does, and the tag attributes each to its run. The cost is one `log` process
   once a probe has run, and `reportDrain` on each probe. Recommended.
6. **macOS keeps data reads and writes alone.** A metadata read is how a tool looks for what it
   may not need. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Settings.Tools`, `CheckTool`, the read-back check | `securityconfig/tools.go`, its test | — | Planned |
| 1a | `Explain`, `allRules`, `rule.always`, `sandbox.Denial` | `sandbox/policy.go`, its test | — | Planned |
| 1b | macOS: the tagged denies, `Reports`, `ParseReport`, `reportDrain` | `sandbox/sandbox_darwin.go`, `sandbox/reports_darwin.go`, their tests, `sandbox/testdata/reports.ndjson` | 1a | Planned |
| 1c | `ExplainOutput`, the folder a denial names, the fixed mounts and `Noise` left out | `sandbox/denials.go`, `sandbox/lists*.go`, their tests | 1a | Planned |
| 2 | The list, `resolveTool`, the shim rule, `probeCommands` | `tools/bash/toolprobe.go`, its tests | — | Planned |
| 3 | `ProbeTools`: `probeDir`, the run, its denials and their checked folders, the report, one at a time, `LastProbe`; `SetProbeFolders` and `chatsvc.AlwaysFolders` | `tools/bash/toolprobe.go`, `tools/bash/bash.go`, `chatsvc/grants.go`, their tests | 1b, 1c, 2 | Planned |
| 4 | `SyncPath` answers `changed`; the probe's folders wired; the launch probe after the sync | `securityconfig/service.go`, `app/app.go`, their tests | 3 | Planned |
| 5 | The wire and the resolvers | `sidecar/graph/schema.graphqls`, `graph/`, generated code | 1, 3 | Planned |
| 6 | Codegen, `useSandboxTools`, the Tools part | `src/gql/`, `src/lib/sandbox-tools.tsx`, `src/components/widgets/sandbox-settings.tsx`, their tests | 5 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1, 1a and 2 at the same time, then 1b and 1c, then 3, then 4 and 5 at the same
time, then 6, then 7.

## Tests

**`securityconfig`**

- `TestCheckToolRefusesEachBadShape`: a name with a slash, one starting with `-`, one past 64
  bytes, a curated name, an invocation of nine fields, one whose first field is not the name,
  one with a control character; and `kubectl-foo --version` passes.
- `TestABadToolIsLeftOutWithItsReason`: a file holding one is read back without it, and the
  reason is reported.

**`sandbox`**

- `TestExplainFollowsTheRuleTable`: every pair of `TestEveryPairOfRulesAnswersAlike`'s table,
  and an Always path under a Read rule, answers the rule the sandbox gives; a denied-always path
  answers `Never` and one of Kstack's directories `Kstack`.
- `TestExplainSeesAPrunedDeny`: with no Read reaching the real home, its `.ssh/config` answers
  `Never` and a path in Kstack's data directory `Kstack`, though `rules()` holds neither Deny.
- `TestParseReportReadsSeatbeltsLine` and `TestReportsFanOutByTag` (`reports_darwin_test.go`,
  over a fake `log` printing `testdata/reports.ndjson`): the two-line message and its tag, the
  *duplicate reports* form rejected, each subscriber its own tag's reports, an unsubscribed tag
  dropped, a full buffer dropped with a count.
- `TestEveryFileDenyIsTagged` (`sandbox_darwin_test.go`): a tagged run's profile opens its rules
  with the tagged file deny and every `deny` carries the tag; an untagged run's is unchanged.
- `TestOnlyDataAndWriteReportsAreKept`: a `file-read-data` and a `file-write-create` on hidden
  paths are denials, a `file-write-data` under a Read rule one with `Write`; a
  `file-read-metadata`, a `file-read-xattr` and a `mach-lookup` are dropped.
- `TestExplainOutputFindsAHiddenPath`: a hidden path, a `Never` one, a Kstack one (dropped), a
  read-only line under a Read rule (a write), a `~` and a `$HOME` token, a relative path, a
  `https://` URL and a path past the last 256 KiB (none read), and a `Permission denied` line
  with no path.
- `TestTheRunsOwnMountsAreNotReported` and `TestNoiseIsNotReported`.
- `TestADenialsFolderIsItsDirectory`: a hidden file's folder is its parent, a hidden directory's
  itself, a `Never` path's empty.
- `TestAMissingPathIsNotADenial`: an `ENOENT` line, and a Seatbelt report, naming a path absent
  from the disk report nothing, though `Explain` says the path is denied.

**`bash`**

- `TestTheProbeRunsOnlyTheList`: over a fake sandbox recording every `Run`, `ProbeTools` runs
  each curated invocation and the registered ones, and `probeCommands` answers nothing for a name
  off the list. A binary planted beside a listed one on the fixture `PATH` is never run.
- `TestAToolResolvesToTheFirstEntry`: a fixture `PATH` with `kubectl` in two entries reports the
  first; a non-executable file in the first entry is passed over.
- `TestAShimResolvesToItsTarget` (`toolprobe_unix_test.go`): a fixture home with
  `.asdf/shims/kubectl` and an `asdf` whose `which` prints a path reports `Shim` and that
  `Target`; a `#!` script elsewhere is no shim.
- `TestAMissingKubectlIsReported`: a `PATH` with no `kubectl` reports it not found, `OK` false.
- `TestAProbeReportsWhatItWasDenied` (`toolprobe_unix_test.go`, through the real sandbox,
  `testutil.RequireSandbox`): a fake tool registered on a fixture `PATH` that reads a file under
  the home by absolute path is `OK` false with that path in `Denied`, its folder set; granted
  always (step 4D), it is `OK`. On macOS the report source is the real stream, with a drain the
  test passes.
- `TestAProbesFolderIsChecked`: a denial whose folder `CheckFolder` refuses is kept with an
  empty folder.
- `TestALateReportIsDrained`: over a fake report source, a report delivered after the reap
  within the drain is kept, and one after it is not.
- `TestAProbeReadsTheAlwaysFoldersAlone`: a folder granted always is in the probe run's
  policy, and one granted to a chat is not.
- `TestTheProbesSandboxHasNoClusterAndNoNetwork` (`toolprobe_unix_test.go`): the run's policy names
  no relay, its environment holds no `KUBECONFIG` or `HTTP_PROXY`, and a
  listener on loopback outside the sandbox gets no connection from a fake tool that dials it.
- `TestAProbeIsBounded`: a fake tool that sleeps past `probeTimeout` reports the timeout;
  `TestOneProbeRunsAtATime`: a second and a third `ProbeTools` asked during the first share one
  run after it, which starts after both asked;
  `TestTheProbesWorkspaceIsGone`: the throwaway folders are removed after each run.

**`securityconfig`**: `TestSyncPathSaysWhetherItChanged`, a sync adding or dropping an entry
answers `changed`, and one that leaves the list as it was does not.

**`app`**: `TestTheLaunchProbeFollowsAChangedPath`, a sync that changed the list starts a probe in
the background, and one that changed nothing starts none.

**`graph`**: `TestSandboxToolMutationsAnswerTheReport`, each refusal a `KSTACK_VALIDATION_ERROR`.

**Webview** (`sandbox-settings.test.tsx`, `sandbox-tools.test.tsx`)

- The rows draw the resolved path, the shim's target, the version and the tag; a denial draws
  its line, and *Grant…* opens step 5B's form with its folder filled in, read and write checked
  for a write, and always alone; a `never` denial and one with no folder draw no button; the `kubectl` line draws only when it is not
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

The probe's denials reach the user alone: no model reads the report, so naming a folder by
reading the disk tells no model anything about the machine. The `log stream` reads only
Seatbelt's reports carrying `kstack:`, and the tag is not the run's token.

**Residuals.** A registered tool is whatever the user named, and the probe runs it, and its
output can print a path to lure a grant; the form shows the folder it grants, and the user can
change it. A path granted from the report is granted always, for every chat; the form says so,
and draws its wide warning for a wide folder.

No boundary moves, so no security record. `security-model.md` gains a row: the probe runs only
the listed invocations, in the sandbox, with no cluster and no network, pinned by
`TestTheProbeRunsOnlyTheList` and `TestTheProbesSandboxHasNoClusterAndNoNetwork`.

## When it lands

- **`security-model.md`**: the row above.
- **`sidecar/CLAUDE.md`**: the probe in the Bash tool's section (the list, `resolveTool`, the
  shim rule, `ProbeTools`, its throwaway `probeDir` and always folders, one at a time,
  `LastProbe`, its denials and their checked folders), `SyncPath`'s `changed`,
  `Explain` over `allRules`, the tagged profile, `Reports`, `reportDrain`, `ExplainOutput` and
  `Noise` under `sandbox`, `Settings.Tools` and `CheckTool` under `securityconfig`, and the launch
  probe in `app`.
- **Root `CLAUDE.md`**: the Sandbox section's Tools part and `useSandboxTools`.
- **`docs/TODO.md`**: nothing; the note's probing paragraph is done.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the probe's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS or Linux, on a machine with Homebrew's `kubectl`, an `asdf`
shim for `helm` and a `kubectl` plugin whose data lives outside step 2A's list: open Settings,
press *Probe again*, and read `kubectl` resolved to Homebrew's with its version and `helm` as a
shim with its target. Register the plugin, probe again, and read it failed with a denied path;
press *Grant…*, read the folder in the form, grant it, probe again, and read it ok. On macOS,
on a standard (non-admin) account too, check that the sidecar's `log stream` runs, and that a
path the tool prints is still reported if it does not. Register `kubectl-foo` with no
invocation and read *not found*; rename `kubectl` away and read the line at the top.
