---
title: Denials in context
scope: sidecar, webview
status: Planned
---

# Denials in context

**Needs:** step 4C, whose refused hosts this step draws and whose `networkHostGrant` it calls;
step 4D, whose `folderGrant` it calls; step 1A, whose `Policy` answers what it hid. **Unblocks:**
steps 6A and 7A, which reuse the grant popover.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command that needs a file the sandbox hides dies with `Permission denied` or
`No such file or directory`, and the model reads one line, *(Ran in the sandbox: …)*, that says
the sandbox may be why. Neither the user nor the model learns which path, and the user has no
button to press. [The note](../../notes/sandbox-credentials-and-permissions.md) calls this "the
biggest usability failure of sandboxes", and asks for every denied path, host and write to be
shown in plain terms with the action that resolves it.

After this step, on macOS and Linux:

- **A denial is found per command**: a path the run tried and the policy hid, found from
  Seatbelt's report on macOS and from the command's error output on Linux; a host the egress
  proxy refused and a write or Secret read the engine refused, which steps 3B, 4C and 5A already
  record.
- **`Policy.Explain(path)`** answers what the policy says of a path and which rule decided.
- **The record**: `tool_calls.denials`, written when the call settles, on the wire as
  `ToolCall.denials`.
- **The model reads one line per denial** after the output, in place of the sandbox line.
  Nothing is retried on its behalf.
- **The user sees one line per denial under the call**, with the grant that resolves it: *Grant…*
  for a folder, *Allow for this chat* / *Always allow* for a host, and the reason alone for a
  refused write. A grant never re-runs the command.
- **One popover** draws every grant offer, step 3A's pending `PATH` entries included.

## What is not in this step

- **No new grant.** The mutations are steps 4C's and 4D's; this step calls them.
- **No probe.** Step 6A runs the curated tools and reads their denials through this step's
  finder.
- **No denial for a command outside the sandbox** (step 1B): it runs as the user and nothing hides
  anything from it.
- **No change to what a denial is on the wire for hosts and writes** beyond joining them into one
  list: their rows are step 4B's.
- Nothing changes on Windows, which has no sandbox.

## Design

### 1. What a denial is

| Kind | Found by | Verdict |
| --- | --- | --- |
| `path` | Seatbelt's report (macOS), the error output (Linux), against `Explain` | `hidden` (no rule opens it: a grant resolves it), `never` (an Always path: nothing does) |
| `host` | the egress handler's refusal (step 4C) | `refused` (a deny rule, the mode, or the user's no) |
| `write` | the cluster proxy's refusal (step 3B) | `refused` |
| `secret` | the redaction the session did not hold the grant for (step 5A) | `refused` |

A path inside Kstack's directories is not reported at all: every run probes its own data
directory's ancestors, and nothing the user can grant opens one. A path a `Noise` list names
(§2) is not reported either.

```go
// Denial is one thing a run asked for and did not get.
type Denial struct {
	Kind    Kind    // path, host, write, secret
	Subject string  // the path with the home as ~, the host, or the action's summary
	Verdict Verdict // hidden, never, refused
	Reason  string  // the rule or mode, in the engine's words; "" for hidden
}
```

`tools.Denial` is the type; `tools.Runtime` gains `Denials DenialRecorder`, whose
`Note(ctx, callID, []Denial)` `chatsvc` keeps on the call's `toolCallEntry` until settlement.

### 2. Finding path denials

**`Explain`.** `sandbox/policy.go`:

```go
// Explain answers what the policy says of path, resolved as the compilers
// resolve it, and the rule that decided: Never for an Always path the run's
// own paths do not open, Read or Write for a rule that covers it, Hidden for a
// Files Deny or no rule at all.
func (p Policy) Explain(path string) (Verdict, Rule)
```

It applies the policy's combination rules (`sidecar/CLAUDE.md`) — the deepest rule wins, the
narrower wins a tie, an Always path wins over everything — over the same resolved lists the
compilers use, so its answer is the sandbox's. `Rule` is `{Kind: Read | Write | Deny | Always | None, Path}`.

**macOS.** Seatbelt reports every denial to the unified log by default: `(with report)` is not a
modifier a `deny` rule takes (`sandbox-exec` refuses it: *report modifier does not apply to deny
action*), and `(with no-report)` is what would silence one. What a `deny` rule does take is
`(with message "…")`, whose text is appended to each report. Checked on macOS 27.0.1:

```
Sandbox: cat(45605) deny(1) file-read-data /Users/x/.ssh/config
kstack:0193b2e4-…
```

is one log event, sender `Sandbox` (the kernel extension), type `Error`, `eventMessage` holding
both lines. `log show` does not return these events, even minutes later; `log stream` delivers
them at once. So the sidecar reads a stream, not a window:

- **The profile tags every deny rule.** `Run` gains `Tag`, a random id per run, and
  `profile_darwin.sb`'s `(deny default)` and every `deny` rule the compiler writes carry
  `(with message "kstack:<tag>")`. The tag is hex, so it is safe as text; it is not the token.
- **One `log stream` per sidecar**, `sandbox.Reports` in `sandbox/reports_darwin.go`, started by
  `app` at launch on macOS as a `lifecycle.Part` and stopped with it: `/usr/bin/log stream
  --style ndjson --predicate 'sender == "Sandbox" AND eventMessage CONTAINS "kstack:"'`, its
  stdout read a line at a time. The predicate is logd's, so the process costs nothing between
  denials. `Subscribe(tag) (<-chan Report, func())` hands a run its own reports; a tag nobody
  subscribed to is dropped, and a subscriber that does not read has its channel's buffer (64)
  overflow dropped with a count. `sandbox.ParseReport(eventMessage) (Report, bool)` reads
  `Sandbox: <name>(<pid>) deny(<n>) <operation> <path>` and the tag on the next line, and
  rejects the `N duplicate reports for …` form, which repeats a report already delivered.
- **A run subscribes before it starts** and reads until its reap, so no denial is missed at
  either end. It keeps the reports whose operation starts with `file-read` or `file-write` and
  names a path, and asks `Explain` of each: `Hidden` is a `path` denial with verdict `hidden`,
  `Never` one with verdict `never`, and `Read` or `Write` (a report on a path the policy opens,
  which a stale link or a race can produce) is dropped. `mach-lookup`, `system-info` and every
  other operation are dropped: a tool asks for those on every launch and needs none of them.
- **Only for a run that ended non-zero.** A command that succeeded needed nothing it lacked, and
  a Foundation program probes `~/Library/Preferences` on every launch. The reports are read as
  they come and discarded at the reap when the exit code is 0.

Two runs' reports never mix: each carries its own tag. The stream is the sidecar's, so a run
started while `log` is not yet reading (the first seconds after launch) reports nothing; the
sidecar logs once when the stream starts and when it dies, and does not restart it, since a
missing denial costs a line under the call and nothing else.

**Linux.** No unprivileged process reads seccomp's or the kernel's log, and bubblewrap's mounts
deny nothing that logs: a hidden path is simply not there. So a denial is found from the
command's error output, `sandbox.ExplainOutput(policy, stderr) []Denial` in
`sandbox/denials.go`, over the capture:

- a line holding `Permission denied`, `No such file or directory`, `Operation not permitted`,
  `EACCES`, `ENOENT` or `EPERM`;
- with a path token on it: `/…`, `~/…` or `$HOME/…`, taken to the next whitespace, quote or
  colon that ends the line's message, `~` and `$HOME` read as the user's home;
- where the path exists on the machine outside the sandbox (`os.Lstat` by the sidecar), so a typo
  and a file that is missing everywhere are not offered;
- and `Explain` says `Hidden` or `Never`.

This is a heuristic, and it is safe as one: it only ever offers a grant, which the user decides on
with the path on screen; it never grants. A command can print a misleading line to lure one,
which is the residual (§Security). The same matcher runs on macOS after the stream's reports, so a
tool that prints its path but never opened it (a check on `os.Stat` that failed with `EACCES`
before the read) is found there too; a path both name is one denial.

**Both.** At most `maxDenials` (8) per call, first seen, deepest path first among those found
together. A `path` denial's `Subject` is the path with the home spelled `~`. A `never` verdict's
`Reason` is *never readable in the sandbox*.

### 3. The record

`tool_calls` gains `denials TEXT` (a JSON list of `Denial`, NULL for none), in
`appdb/migrations/0001_init.sql` under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md), written at the
call's settle from what `Note` kept, path denials alone: a host, a write and a Secret read are
`approvals` rows already. On the wire:

```graphql
enum DenialKind { Path Host Write Secret }
enum DenialVerdict { Hidden Never Refused }

"One thing a call's command asked for and did not get."
type Denial {
  kind: DenialKind!
  "The path with the home as ~, the host, or the action's summary. Cluster or command text: draw it through VisibleText."
  subject: String!
  verdict: DenialVerdict!
  "The rule or mode that refused it; empty for a hidden path."
  reason: String!
}

extend type ToolCall {
  "Every denial of the call, paths off its row and hosts, writes and Secret reads off its refused approvals, in the order found."
  denials: [Denial!]!
}
```

The resolver joins the row's list with the call's `approvals` of status `refused` and `denied`
whose action is `net`, `k8s` class 4 or 5, or class 6, each as a `Denial` of its kind with the
action's `Summary` as `subject` and the row's `reason`.

### 4. What the model reads

`resultText` in `tools/bash/bash.go` replaces `sandboxLine` with `denialLines`: after the output,
one line per denial, in the order found, and nothing for none:

| Denial | Line |
| --- | --- |
| path, `hidden` | `The sandbox blocked reading ~/code/foo; the user can grant it.` (`writing` for a `file-write*` report) |
| path, `never` | `~/.ssh is never readable in the sandbox.` |
| host | `The sandbox blocked reaching charts.example.com; the user can allow it.` |
| write | `The user's settings refused: Delete pod api-7f9c in team-a on dev-eks (this context is read-only).` |
| secret | `Secret values are redacted until the user allows reading them.` |

The lines follow the output so nothing a command prints can pass for one, and they are the
sidecar's, so a subject goes through `safe.String`. Nothing is retried on the model's behalf; the
prompt (`prompts/sandbox.md`) says a blocked line names something the user can grant, that the
model should say what it needs the folder or host for, and that it must not work around the
sandbox.

### 5. What the user sees

`chat-transcript.tsx`, under the call's disclosure summary and outside the closed disclosure, as
an unasked `Write`'s content is drawn: one muted line per denial, `DenialLine`, the subject
through `VisibleText`, with `denialsOf(call)` in `chats.tsx` the one reader of the field:

| Denial | Line | Button |
| --- | --- | --- |
| path, `hidden` | *Blocked: reading `~/code/foo`* | **Grant…**, opening the popover: *Read* or *Read and write*, *For this chat* or *Always*, then Grant → `folderGrant(chatID, path, write, duration)` with the folder of the path (the path itself when it is a directory, else its parent) |
| path, `never` | *Blocked: reading `~/.ssh/config` — never allowed* | none |
| host | *Blocked: reaching `charts.example.com`* | **Allow for this chat** → `networkHostGrant(chatID, host, port, Chat)`; **Always allow** → the same with `Always` |
| write, secret | *Blocked: Delete pod `api-7f9c` in `team-a` on `dev-eks` — this context is read-only* | none: the user changes Settings |

A press is disabled in flight and handed back on an error, which `errorReportExchange` reports.
Once the mutation answers, the line reads *Granted* (or *Allowed*) in place of the button, kept in
the line's own state for as long as the pane is mounted; the command is never re-run, and the
user asks again. A subagent's call draws its lines inside its `Agent` call's disclosure, where its
calls are drawn.

The lines are cluster data at one remove, since a command chose what to print: every subject is
spelled through `VisibleText`, no `title`, and the popover shows the folder or host verbatim
above its buttons so the user grants what they read, not what the line implied.

### 6. One popover

`grant-popover.tsx` is the one component every grant offer opens, over
`@kubetail/ui/elements/popover`, `GrantPopover({ kind, subject, onGrant })`:

| `kind` | Subject | Choices | Calls |
| --- | --- | --- | --- |
| `folder` | the folder | read / read and write; this chat / always | `folderGrant` |
| `host` | the host and port | this chat / always | `networkHostGrant` |
| `path-entry` | a `PATH` entry step 3A left *waiting for you* | include | `sandboxPathAdopt(dir)` |

Step 3A's Sandbox section moves its *Include* onto this popover with the note's wording, *your
shell added `~/.local/share/mise/shims` to PATH; include it?*, and step 6A's probe results open it
with `folder`. The popover holds the subject in mono through `VisibleText`, the choices as
segmented pickers, one Grant button, and a Cancel; Escape closes it; it is disabled in flight.

## Decisions this step asks for

1. **macOS reads a stream, not `log show`.** The first design read the reports with `log show`
   over the run's window, once per failed run; on macOS 27.0.1 the reports are not in what `log
   show` returns, and `log stream` with the predicate delivers them at once. A stream also lets
   the profile's `(with message)` tag attribute every report to its run, so overlapping runs
   never share one. The cost is one `log` process for the sidecar's life. Recommended.
2. **Only a run that ended non-zero reports its paths.** A successful command that probed a
   hidden path did not need it, and every program probes something. Recommended.
3. **A grant never re-runs the command.** The model's next command is the model's, with the
   grant in place; a re-run by the app would run a command the model did not ask for now.
   Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Explain` | `sandbox/policy.go`, its test | — | Planned |
| 2 | macOS: the tag on every deny rule, `Reports`, `ParseReport` | `sandbox/sandbox_darwin.go`, `sandbox/profile_darwin.sb`, `sandbox/reports_darwin.go`, their tests, `sandbox/testdata/reports.ndjson` | 1 | Planned |
| 3 | `ExplainOutput`, the `Noise` list | `sandbox/denials.go`, `sandbox/lists*.go`, their tests | 1 | Planned |
| 4 | Bash finds a run's denials and `Note`s them; `denialLines`; the prompt | `tools/bash/bash.go`, `tools/bash/task.go`, `tools/tool.go`, `tools/bash/prompts/sandbox.md`, their tests | 2, 3 | Planned |
| 5 | The column, the settle, the wire and its resolver | `appdb/migrations/0001_init.sql`, `chatsvc/store.go`, `sidecar/graph/schema.graphqls`, `graph/`, generated code | 4 | Planned |
| 6 | Codegen, `denialsOf`, `DenialLine`, `GrantPopover`, step 3A's Include on it | `src/gql/`, `src/lib/chats.tsx`, `src/components/widgets/chat-transcript.tsx`, `grant-popover.tsx`, `sandbox-settings.tsx`, their tests | 5 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1, then 2 and 3 at the same time, then 4, then 5, then 6, then 7.

## Tests

**`sandbox`**

- `TestExplainFollowsTheRuleTable`: every pair of `TestEveryPairOfRulesAnswersAlike`'s table, and
  an Always path under a Read rule, answers the verdict and the rule the sandbox gives (that test
  runs the table through the sandbox; this one asks the policy).
- `TestParseReportReadsSeatbeltsLine`, in `reports_darwin_test.go`: the two-line message, the
  tag, an operation with no path, and the *duplicate reports* form rejected.
- `TestReportsFanOutByTag`, in `reports_darwin_test.go`: over a fake `log` (a script printing a
  captured `testdata/reports.ndjson`), two subscribers each get their own tag's reports and none
  of the other's, an unsubscribed tag is dropped, and a full buffer drops with a count.
- `TestADeniedReadIsReported`, in `sandbox_darwin_test.go`, through the real sandbox on CI's
  macOS job: a run that reads `~/.ssh/config` and a hidden `~/code/foo` reports both with its
  tag, one `Never` and one `Hidden`.
- `TestExplainOutputFindsAHiddenPath`, over fixtures: a hidden path, a path that does not exist
  on the machine, a `Never` path, a Kstack path, a `~`-spelled path, a `$HOME` one, and a
  `Permission denied` line with no path; and `TestExplainOutputOnARealRun`, through the real
  sandbox on Linux: `cat ~/code/foo` under a policy that hides it.
- `TestNoiseIsNotReported`: `~/Library/Preferences/.GlobalPreferences.plist` on macOS.

**`bash`**

- `TestARunsDenialsAreNoted`: over a fake sandbox and a fake report source, a run that fails
  notes its denials on the runtime, at most `maxDenials`, and a run that exits 0 notes none.
- `TestTheResultEndsWithTheDenialLines`: one line per kind of §4, after the output, and no
  sandbox line; a run with none ends with the output alone.
- `TestABackgroundRunNotesItsDenials`, at its `Wait`.

**`chatsvc`**: `TestTheSettleWritesTheDenials`, and `TestDenialsJoinTheRefusedApprovals` on the
wire: a refused host and a refused write appear beside a hidden path, in order.

**Webview** (`chat-transcript.test.tsx`, `chats.test.tsx`, `grant-popover.test.tsx`,
`sandbox-settings.test.tsx`)

- Each kind draws its line and its button, or none, outside the disclosure; a `never` verdict
  draws *never allowed*; a subagent's inside the `Agent` disclosure.
- The popover's choices per kind, Grant calling the right mutation with the folder of a file
  path, the line reading *Granted* after, disabled in flight and handed back on an error.
- Step 3A's Include opens the popover with the entry and calls `sandboxPathAdopt`.
- `denialsOf` reads the field and nothing else.

## Security

Nothing widens: no grant is made but by the user's click on a popover that shows the folder or
host it grants, spelled through `VisibleText`, and the mutations are steps 4C's and 4D's with
their checks. The model reads one line per denial, which names a path or host the command
already named, and the lines follow the output so a command cannot forge one.

The residual is a command that prints a misleading path or host to lure a grant: `cat: ~/.aws
/credentials: Permission denied` printed by a hijacked script draws no button, since `~/.aws` is
`Never`; `cat: ~/code: Permission denied` printed by one draws *Grant…* for a folder the user
must still read and choose to grant, and the popover shows it verbatim. A `Never` path is never
offered, and Kstack's directories are never shown.

No security record. `security-model.md` gains a row: a denial is drawn and offered, never
granted, with the tests above.

## When it lands

- **`security-model.md`**: the row above.
- **`sidecar/CLAUDE.md`**: `Explain`; the tagged profile, `Reports` and `ParseReport`;
  `ExplainOutput` and `Noise`; `Runtime.Denials`; `denialLines` in `resultText` in place of
  `sandboxLine`; the column and the wire.
- **Root `CLAUDE.md`**, *Chat* and the Settings dialog: `denialsOf`, `DenialLine`,
  `GrantPopover`, step 3A's Include on it.
- **The note's *Where this meets the code***, decision 8: a denial on macOS is found from
  Seatbelt's report through a stream tagged per run, and on Linux from the output.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the sandbox's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS and on Linux: ask for `cat ~/code/my-service/README.md` and
read the call fail with *Blocked: reading `~/code/my-service`* under it and the model's line in
its answer; press *Grant…*, choose read and this chat, and read *Granted*; ask again and read the
file. Ask for `cat ~/.ssh/config` and read *never allowed* with no button. Ask for `curl
https://charts.example.com`, deny the request, and read *Blocked: reaching `charts.example.com`*
with its two buttons. On macOS, `log stream --predicate 'sender == "Sandbox"'` in a terminal
shows each report with the run's tag while the command runs.
