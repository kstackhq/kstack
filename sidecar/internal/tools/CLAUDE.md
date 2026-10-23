# internal/tools — every tool the model can call

Each package directly under `tools/` is one tool; `box.go`, `tool.go`, `save.go` and
`workspace.go` are what they share, `tools/internal/fileguard` is the file tools' checks, and
`tools/internal/logsinput` is what the three log tools share. What
confines a run (sandbox, permissions, proxy, session) is `internal/run/CLAUDE.md`; who calls a tool
and files its rows is `internal/agent/CLAUDE.md`.

## The box

Every tool the model can call. **A tool's kind is the interfaces it implements** (`tool.go`):

- **`Reader`** (every tool): `Name()` (unique in the box; every stored call names it),
  `ActionKind()`, `Action(input, cwd, sandboxed)` and `Prompt()`.
- **`Runner`** adds `Run(ctx, rt, input) (text, isError)`, never returning a Go error's text.
  `Custom` is offered by its `Definition()`; `PerTarget` adds `For(target)`.
- **`Native`** is a vendor's tool: `Name()` is ours, `ContractName()` the vendor's shape id.
  The provider runs it when it is `Budgeted`, the sidecar when it is a `Runner`.
- **`Gated`** adds `Approval`, which only reads `rt`; an error it returns may be a `*Refusal`,
  carrying the result the model reads in place of `bad-input`. **`Shown`** adds `RunShown`, whose action
  the row keeps → [ADR](../../../docs/adr/2026-10-08-a-shown-tool-records-its-action-at-the-run.md).
  The loop finds these and `Bounded` by type assertion.

**One `Box` holds every tool** (`box.go`), in offer order, plus readers for tools this machine
cannot offer. `NewBox` panics on a wiring mistake. `For(target, names)` is a turn's box: the
target's catalog list, in box order; a name with no tool is skipped (how a machine with no shell
drops bash). `Without(kinds...)` is how a subagent gets no `Agent`. **Every stored call is read
through `Box.Action(name, input, cwd)`** by the row's name alone; callers ask the box rather than
range over it, so kinds never leak past `tools`. →
[ADR: every tool is in the box](../../../docs/adr/2026-09-24-every-tool-is-in-the-box.md).

**A tool's `Runtime`** (`rt`) is the chat its call runs in: `ClusterID`, `ChatID`, `Session`,
`Dir`, `Tasks`, `Files`, `Agent`, `Watches` and `ActionAsker`. `Agent` and `Watches` are nil in a
subagent's and a monitor's. `agent/chat` sets every field; a test sets
the ones its tool reads. The chat, cluster and workspace come from the runtime alone, never the
session.

- **`ChatDir`** is the chat's directory (`results/`, `tasks/`, `workspace/`), deleted with it.
  `WorkspacePath` is where every command starts; `ToolHomePath` where sandboxed tools write.
- **Open and remove these directories only through `rootdir`** (`internal/lib/rootdir`): it
  refuses links and anything but a directory, and `RemoveAll` never follows a link →
  [security record](../../../docs/security/2026-09-30-a-run-cannot-replace-its-own-paths.md).
- **`FileStamps`** keys by `fileguard.Abs`; a `Stamp`'s `Whole` is set only when the model saw
  every byte unaltered.
- **`ActionAsker`** puts a sandboxed command's classified `ActionRequest` to the user, or
  `Record`s one decided unasked. Nil where nobody can be asked.
- **A tool that acts through a service is built with it** (`memory.New`, `kubequery.New`,
  `logsview.New`, `logsread.New`, `logswatch.New`, in `app`'s `chatTools`) and maps the service's errors to the model's codes →
  [ADR](../../../docs/adr/2026-09-27-a-tool-calls-its-service-directly.md).
- `InlineLimit` (30,000) caps a result; `FileLimit` (8 MiB) caps saved output and Read.
- **The chat's `defaultToolTimeout` is 2 s.** A tool that does I/O is `Bounded` and names its own
  (Read/Write/Edit 30 s, WebFetch 45 s); one that forgets times out silently.

## Actions and results

**A tool that can show a call exports `ActionOf`** over the same `parse` its `Run` uses. A
`tools.Action` is a `Description` and exactly one kind; its JSON tags are the wire (gqlgen binds
`ToolAction` onto it). **A call's kind is its tool's** (`Box.ActionKind`), whatever the
arguments hold; `TestEveryActionKindIsServed` exists because a kind the gqlgen binding lacks is
served as `""` silently. **A call that asks has an action the request can draw**: a `Command`,
`Read`, `Write`, `Edit`, `Fetch`, or a `Memory` with `Scope` `everywhere`. **Never change how a
tool reads rows it has written** (`TestActionOfReadsOldRowsTheSame`): every read recomputes it. →
[ADR: a tool call shows itself from its arguments](../../../docs/adr/2026-09-23-a-tool-call-shows-itself-from-its-arguments.md).

**An oversized result is saved one way for every tool** (`save.go`). `Fit` returns the result
whole up to `InlineLimit`; past it, the text goes to the `Saver` and the result is a
`<persisted-output>` block with a `PreviewLen` preview. A failed save falls back to `Cut`, never
splitting a rune. `SaveTo(dir)` names the file itself, never with a provider's id.

## The file tools' checks (`tools/internal/fileguard`)

**`internal/tools/internal/fileguard` is the checks every file tool makes on a path**; it imports
`tools` alone, and sits under `tools/internal` because it is not a tool (every package directly
under `tools/` is one). Its errors are values, so each tool words its own refusal.

- `Abs` is the path a tool opens: one line of at most `MaxPath` with no control character, absolute
  and plain (no empty, `.` or `..` component), Git Bash drive paths read as native, UNC and device
  paths refused on Windows.
- `Fence` (`NewFence`) is Kstack's directories, which no file tool opens but the chat's workspace:
  `Named` by name, `Holds` on disk via `os.SameFile`. **`Holds` re-reads each directory on every
  call and makes a missing one 0700** — the OS or user may recreate one while Kstack runs.
- **`Fence.File(results, path, create)` is the file Write and Edit change** (`file.go`): under the
  workspace by name, a name under `OpenWorkspace`'s root; else the path, once `Holds` finds it
  outside. A `File`'s methods are the path forms or the root forms.
- **A granted folder is reached by handle** (`grant.go`, `walk.go`). `WithHidden` gives the fence
  the zones' `Never` and `Closed` paths, **resolved on every comparison, never cached**.
  `Granted(ctx, folders, path)` resolves on a goroutine abandoned when `ctx` ends (a dead network
  mount blocks in a syscall), answering false so the tool asks. `Walk(folder, path)` opens `/` as an
  `os.Root` and walks down without following links, compares each handle with the closed paths, and
  never reads through a folder that was missing at the walk (`ancestorMissing`). `walkHook` is its
  test seam; `WalkRefusal` its one wording for the model.
  → [path grants](../../../docs/security/2026-10-04-path-grants.md),
  [ADR: a folder grant is a rule](../../../docs/adr/2026-10-04-a-folder-grant-is-a-rule.md).
- `Open` accepts only a regular file of at most `FileLimit`, by `Lstat` before and `Stat` after;
  `OpenFlags` is `O_NONBLOCK` on Unix so a FIFO never blocks the open.
- **The write half** (`write*.go`) is what Write and Edit share: `Replaceable`/`Creatable` check
  access opening nothing; `Replace` renames a synced temp file over the target; `Create` never
  replaces and never follows a planted link. Both check `ctx` before changing the disk and remove
  what they made on failure.
- **The root forms** (`LstatIn`, `OpenIn`, `ReplaceableIn`, `CreatableIn`, `ReplaceIn`, `CreateIn`)
  take an `os.Root` and refuse what their path forms refuse; `OpenIn` refuses a file that is not
  the one its `Lstat` saw (`ErrChanged`). Writability under a root is the owner's mode bits.
  → [the write tool](../../../docs/security/2026-09-24-write-tool.md),
  [the edit tool](../../../docs/security/2026-09-24-edit-tool.md),
  [the workspace](../../../docs/security/2026-09-27-the-workspace.md).

## Web search (`tools/anthropicwebsearch`)

**`internal/tools/anthropicwebsearch` is the Messages API's web search**, a `Budgeted` tool the
provider runs, five a turn. `Prompt()` reads the month at each call (the sidecar outlives a month);
`messages.go` is its `llm.MessagesServerTool`. A server tool's package is named for its vendor,
whose SDK it imports.

## LogsView (`tools/logsview`)

**LogsView is a live view of logs the user looks at, opened by the model's call.** It is `Custom` and `Shown`, never `Gated`, and reads no log line: it checks the call
against the mirror, hands back a `LogsViewAction` the row keeps, and answers a one-line receipt
through `safe.Redact`. The action embeds `LogsScopeInput` (`services/logs`' `Scope`, aliased in
`tools`) plus an `Anchor`, `PinToEnd` and `ResolvedAt`, the moment it resolved on the tool's clock, which a frozen card reads up to; the enums bind member by member in `gqlgen.yml`.
It reads its own `anchor` and `pin_to_end` as `logsinput.Parse`'s `Fields`, refuses a call with
no sources, then resolves the view with `logsinput.Resolve`; its clock is a field its tests set.
**A source the mirror does not hold yet opens**: a view waits for it. A refusal is one JSON line with a code, never the model's text. It is in `catalog`'s `ours` and
`app.go`'s box, and out of `SubagentBox` and `MonitorBox`: a view is the user's window. The model
reads lines through `LogsRead`, and those the user selects and sends ride the question's View
section (`internal/agent/CLAUDE.md`). The service it checks against is
`internal/services/logs/CLAUDE.md`.

## LogsRead (`tools/logsread`)

**LogsRead is the model's read of a range of logs.** It is `Custom`, `Shown` and `Bounded` (30 s),
never `Gated`, in `catalog`'s `ours`, `app.go`'s box after LogsView, and in `SubagentBox` and
`MonitorBox`. Its read is rebuilt on [the log fetch API](../../../docs/specs/log-fetch-api/):
`RunShown` panics until then. Its action, `LogsReadAction`, holds the lines the model read, with
`viewAround`, the read's view at its first line's moment, unpinned.

→ [the model reads logs](../../../docs/security/2026-10-17-the-model-reads-logs.md).

## LogsWatch (`tools/logswatch`)

**LogsWatch is a follow of a view that runs as a background task and wakes the model once, when it
ends.** It is `Custom`, `Shown` and `Bounded` (30 s), never `Gated`, in `catalog`'s `ours`,
`app.go`'s box after LogsRead, and out of `SubagentBox` and `MonitorBox`: a watch's point is the
turn it starts. Its watch is rebuilt on the log service's new follow
([the log fetch API](../../../docs/specs/log-fetch-api/)): `RunShown` panics until then. Its
action is a `LogsWatchAction`; the chat holds a watch as a background task
(`internal/agent/CLAUDE.md`).

→ [the model watches logs](../../../docs/security/2026-10-18-the-model-watches-logs.md).

## The log tools' input (`tools/internal/logsinput`)

**`tools/internal/logsinput` is what the three log tools share.** `Parse(raw, own)` is the
strict walk (each key once, DNS names, at most 100 sources, `grep` compiled on one line of at most
`MaxGrep`); `own` is the tool's `Fields`, each key's typed destination (`*string`, `*bool`, `*int`
or `*logs.Padding`, lines or a span under the service's caps) or the refusal it earns. It takes a
call with no sources: each tool says whether it needs them. `Resolve(ctx, svc, now, id, in)` checks
each source inside one `ReadActive`, then runs `logs.Check`, so a scope a tool takes is one the
service reads. `MomentOf(s, now, end, dir)` reads an RFC 3339 time at any precision as the
interval it spells, `Since` its first nanosecond and `Until` its last (a date is UTC and takes no
offset), or an ISO 8601 duration (read by `services/logs/iso8601`), exact, applied on the calendar
in UTC in the caller's `Direction` (`Behind` or `Ahead`), a month end clamped to the target
month's last day, and nothing before the epoch or after 9999; `MomentMessage` words its refusal.
`SourcesText`, `SourcesOf` and `NotesText` are how a tool names its sources; `Refusal` and `Result`
how it answers, a service refusal by its own code. `sample.go` is how a line reads (`LineText`,
with a place mark and the bytes cut; `Stamp`, nine fraction digits always; `PlaceOf`), a header's
notices (`Notices`, under `NoticesBudget`), the caps `DefaultMessageBytes` and `SampleBudget`,
how an action keeps a line (`ReadLine`) and the view around it (`ViewAround`). `logsinputtest` is
the fake cluster and the fixtures the tools' tests run over (`cluster/clustertest` beneath).

## KubeQuery (`tools/kubequery`)

**KubeQuery is read-only SQL over the chat's cluster's cache, run without asking.** It is `Custom` and `Bounded` (10 s), never `Gated`. `parse` is strict (a `;` only at the
end). `Run` (`cluster.go`) reads one `ReadActive`, the cache's health, and queries only when
`health.Trusted()`, so the rows and the verdict describe one cache. Errors: `sql` (through
`safe.String`), `no-cache`, `read-failed` with none of its text. Cells go through
`safe.RedactJSON`/`safe.Redact`. **No answer ends mid-row**: past `InlineLimit` it is saved via
`Persisted`, and a head too large for any row is `too-large`. `prompts/kubequery.md` lists only the
views that are built.
→ [kubequery reads the mirror](../../../docs/security/2026-09-27-kubequery-reads-the-mirror.md),
[ADR: the cluster is kubectl in Bash, and KubeQuery over the mirror](../../../docs/adr/2026-09-27-the-cluster-is-kubectl-in-bash-and-kubequery.md),
[ADR: KubeQuery reads views on a connection of its own](../../../docs/adr/2026-09-27-kubequery-reads-views-on-a-connection-of-its-own.md),
[ADR: KubeQuery reads tables written with the object](../../../docs/adr/2026-09-27-kubequery-reads-tables-written-with-the-object.md),
[ADR: KubeQuery reads without asking](../../../docs/adr/2026-09-27-kubequery-reads-without-asking.md).

## Bindings

**A binding is a `Tool` over a capability.** The capability runs something and knows no
contract; the binding parses its contract's input into the capability's request and renders
the outcome in the contract's shape. Two bindings of one capability share its code by
construction. No capability ships yet.

## Bash (`tools/bash`)

**A turn can run a command, once the user says so.** `app.go`'s `chatTools` is the one
`tools.NewBox`; it offers Bash wherever `bash.New` finds a shell. `sandboxStatusOf` builds the one
`sandbox.Status`: `NetworkAvailable` is true wherever the sandbox is on macOS and only with a
`pasta` that passed the probe on Linux, and a sandbox with no shell clears the network answer with
it (`TestNoShellOffersNoNetwork`). Tests that want bash whatever the machine's login shell clear
`SHELL` (the `tool` helper does). The tool's words are `bash/prompts/` (`schema.json`,
`description.md`, `bash.md`, `sandbox.md`, `sandbox_linux.md`); `make prompts` pins them. Bash's per-call bound is its own timeout plus `killGrace` plus `callMargin`, so a
timed-out command answers with its own result. → [the bash
tool](../../../docs/security/2026-09-18-bash-tool.md), [bash offered to every
turn](../../../docs/security/2026-09-22-bash-offered-to-every-turn.md).

- **The shell**: on Unix the login shell if `zsh` or `bash`, else bash on `PATH`; on Windows Git
  for Windows' from its install record, never `PATH` (WSL's launcher). Read the version with
  `--version`, never `-c` (sources `BASH_ENV`).
- **Sandboxed = the tool has a sandbox and `Session.Outside` is false**; the model has no say. A
  sandboxed call on a sandbox that `Confines` runs unasked except for network (`networkFor`). →
  [ADR](../../../docs/adr/2026-09-28-the-sandbox-is-the-gate-for-a-sandboxed-command.md), [network is the
  user's switch](../../../docs/security/2026-10-04-network-is-the-users-switch.md).
- **Input goes through one token-walking `parse`** for `Approval`, `Run`, `CallTimeout` and
  `ActionOf`; never a struct decode, which lets case variants and duplicates through. → [the
  request reads the arguments](../../../docs/security/2026-09-23-the-request-reads-the-arguments.md).
- **The schema's `description`** names what the command changes and never calls it safe. → [the
  command description](../../../docs/security/2026-09-23-the-command-description.md).

**A sandboxed run carries its own directory, environment and kubeconfig** (`sandboxedRunFor`). →
[Bash runs in a sandbox](../../../docs/security/2026-09-28-bash-runs-in-a-sandbox.md), [a run cannot
replace its own paths](../../../docs/security/2026-09-30-a-run-cannot-replace-its-own-paths.md).

- Its directory (`<runtime>/runs/<pid>-*`) holds `kubeconfig` and `proxy.sock` outside the
  workspace; a socket path must fit (`checkSocketPath`). `TMPDIR` is `<cache>/tmp/<pid>-*`, on
  macOS seeded with xcrun's cache → [record](../../../docs/security/2026-09-30-a-run-starts-with-xcruns-cache.md).
  A gone sidecar's entries are swept by its lock → [ADR](../../../docs/adr/2026-10-17-a-run-directory-is-reclaimed-by-its-sidecars-lock.md).
- **`PATH` is frozen at start** (`path.go`); with no entry it is `/nonexistent`, never empty (that
  searches the working directory). → [ADR](../../../docs/adr/2026-10-02-path-is-the-login-shells-filtered-and-frozen.md).
- **The environment is one table** (`env.go`); nothing else of the process's passes. `ZDOTDIR` is
  the run directory, so `zsh -c` never sources a workspace `.zshenv`.
- **Whatever can hang on a network mount is built on a goroutine abandoned when the context ends.**
- The cluster's kubectl cache is keyed by `serverKey(uid)`; the kubeconfig names a fixed server →
  [ADR](../../../docs/adr/2026-10-17-a-sandboxed-kubeconfig-names-a-fixed-server.md).
- The policy is `workspacePolicy`; folder grants are rechecked at the run (`grantRules`) →
  [ADR](../../../docs/adr/2026-10-04-a-folder-grant-is-a-rule.md), [path
  grants](../../../docs/security/2026-10-04-path-grants.md). Limits →
  [ADR](../../../docs/adr/2026-10-02-process-limits-are-set-inside-the-run.md).
- Tests lay folders out under Kstack's three as `app/paths.go` does (`kstackDirs`).

**A run with a cluster serves a grant over its socket** (`upstream.go`, `proxy.go`). The claim
watches the record and revokes on disable, mark or delete. A claim that cannot be made still runs
the command, answering each request not connectable. A background command's writes are refused.
**The run ends in order** (`sandboxedRun.end`) — grant, server, handlers joined, claim, directory —
so no write reaches the user once `Run` returns. → [cluster writes
ask](../../../docs/security/2026-09-29-cluster-writes-ask.md), [the permissions
engine](../../../docs/security/2026-10-02-the-permissions-engine.md), [Secret data is a permissioned
read](../../../docs/security/2026-10-05-secret-data-is-a-permissioned-read.md).

**The executable probe** (`executableprobe.go`) runs each executable in the sandbox, **never through
a shell**, with no cluster and no network. **Every disk read outside Kstack's directories is bounded**
(`onDisk`), so a dead mount fails one probe and holds neither the queue nor `Close`. One probe runs
at once. Its report reaches the user alone, never a model: `sandboxExecutablesProbe` starts one
(`StartProbe`) and answers at once, since a probe can outlast the host's request budget, and
`sandboxExecutablesWatch` is the gauge its end reaches (`ProbeState` folded with the settings into
one `SandboxExecutablesReport`).

**A command runs where its request shows** (`resolveWorkdir`): its `workdir`, else the workspace. A
sandboxed call must start under the workspace by name (guidance, not a boundary).
**`resolveWorkdir` never touches the filesystem** — a UNC path touched before the user decides
starts an SMB login. → [the command names its
directory](../../../docs/security/2026-09-23-the-command-names-its-directory.md).

**The wrapper** (`wrapper.go`) sources the snapshot (outside the sandbox) and `eval`s the quoted
command. A sandboxed run leads its own session → [ADR](../../../docs/adr/2026-10-13-a-macos-run-leads-its-own-session-so-no-run-reads-another-processs-launch-strings.md).
On Windows the wrapper is a file, never a command line, which MSYS would reparse.

**The snapshot** (`snapshot.go`) is the user's profile, dumped once per start, for commands outside
the sandbox alone; its stop waits for the shell's reap. → [bash runs the login
shell](../../../docs/security/2026-09-23-bash-runs-the-login-shell.md).

**A stop is the runner's record** (`result.Stop`), never read off the exit code, and **a stopped
command is always an error result**. A cancel kills at once. → [ADR](../../../docs/adr/2026-10-17-a-bash-stop-is-the-runners-record.md).

What the model reads is `resultText`. **An error result's first line is the sidecar's** (`Exit
code N`, `Command timed out …`) and the output starts on the next, so nothing a command prints can
pass for a header. The output is redacted by `safe.Redact` — **hygiene, never the boundary**; the
approval request is the bound. Past `tools.InlineLimit` it is saved through `tools.Fit`. A confined
run that failed ends with `sandboxLine`, so the model knows the sandbox may be why.

**A background command** (`run_in_background`) is approved like any other. `startTask`
(`task_unix.go`, `task_windows.go`) is bounded by the call's context only until the process
starts, then clears the `exec.Cmd`'s `Cancel`. The chat holds the task from there
(`internal/agent/CLAUDE.md`). `TaskStop` (`tools/taskstop`) is the model's stop.

## Read, Write and Edit (`tools/read`, `tools/write`, `tools/edit`)

**The file tools** (`internal/tools/read`, `write`, `edit`) classify a path **by name alone** in
`Approval`, touching nothing on disk: unasked in the chat's directory (Read) or workspace (Write,
Edit), and in a granted folder through `Fence.Walk` alone; refused elsewhere in Kstack's
directories; asked for the rest. **File work runs on a goroutine** (`fetch`, `write`, `edit`): a
stat on a dead mount blocks where no context reaches. Refusals are words, never a Go error's text.
Read redacts the whole file and sets the path's **stamp** (hash, `Whole` when shown unchanged);
Write needs a matching `Whole` stamp and Edit a matching one, so the model Reads first. Edit refuses
a `new_string` holding `safe.Redacted`, and `matches` uses `strings.Count` plus one
`strings.Index` — never a walk of every overlap, which is quadratic. → [security records: read any
approved file](../../../docs/security/2026-09-23-read-any-approved-file.md), [the write
tool](../../../docs/security/2026-09-24-write-tool.md), [the edit
tool](../../../docs/security/2026-09-24-edit-tool.md).

## WebFetch (`tools/webfetch`)

**`internal/tools/webfetch` is `WebFetch`**: one approved `https` GET, returned as markdown.
`target` reads the URL by name; **`ActionOf` is `rebuild`**, so a later refusal or `x/net` update
changes no stored call's action. **`NewTransport` is the boundary**: its dialer refuses any
address `Dialing.Public` rejects, on the address dialled after DNS, the HTTPS proxy alone excepted.
`Run` follows redirects itself (`follow`); one to another host or port goes back to the model.
Failures name their kind, never the error's text. → [security record: the web fetch
tool](../../../docs/security/2026-09-24-web-fetch-tool.md).
