# Every tool call records its tool's contract

**Area:** the sidecar's `internal/tools` and `internal/agent/chat`, one schema comment and one
GraphQL field. The webview changes two test fixtures and draws one null.
**Answers:** [#356](https://github.com/kstackhq/kstack/issues/356).
**Prerequisites:** none.
**Status:** Planned.

## Goal

A stored tool call says which shape its JSON is in, and no build decodes a call's JSON in a
shape it does not know: its `arguments`, its `shown_action`, or the result of the task it
started. A tool can later change any of those shapes by bumping its contract, without changing
the name the model calls it by, and every row written before the change still reads as it did.
A row whose JSON does not decode costs its chat one action, not the whole read, and the log says
so without quoting the row.

Read `sidecar/internal/tools/CLAUDE.md`'s paragraph on `Action`, and the ADRs [a tool call shows
itself from its arguments](../adr/2026-09-23-a-tool-call-shows-itself-from-its-arguments.md) and
[a shown tool records its action at the run](../adr/2026-10-08-a-shown-tool-records-its-action-at-the-run.md),
first.

## The gap

A `tool_calls` row holds two JSON values a later build reads back: `arguments`, which the tool's
`Action` parses on every read, and `shown_action`, which chat decodes into `tools.Action`. A
`LogsWatch` call's `background_tasks` row holds a third, `result`, a `tools.WatchResult`.

- **A vendor's tool records its shape; ours do not.** `contract_name` holds a vendor's shape id
  (`web_search_20260318`) and is NULL for every tool of ours. Only `ServerCallSeen` writes it.
- **The only way to change one of our shapes is a new name.** The rule is "never change how a
  tool reads rows it has written", pinned by `TestActionOfReadsOldRowsTheSame`. A new name is a
  new tool to the model, so a change to what we store also changes what the model is offered.
- **The pin is missing for three tools.** `memory`, `kubequery` and `agent` parse their
  arguments in `Action` and have no `TestActionOfReadsOldRowsTheSame`.
- **`shown_action` has no pin and no shape id.** Chat decodes it with `json.Unmarshal` into
  `tools.Action` in three places (`store.go`: `viewCall`, the tool-calls read, the waiting
  notices). A retyped field fails the decode, and the failure fails the whole read: every chat
  holding a `LogsView`, `LogsRead` or `LogsWatch` call stops loading. A renamed field fails
  nothing: `json.Unmarshal` drops the old key and the new one reads as its zero value, so the
  call quietly shows something else.
- **A decided cluster write has the same gap, and is not a tool's.** An `approvals` row of kind
  `action` holds the `tools.ActionRequest` the gate showed for one write a sandboxed command
  asked for. `store.go`'s cluster-writes read decodes it, has no pin, and fails the whole read
  when it does not decode. Its shape is the gate's, `permissions.Action` and the write as sent,
  built in `bash/proxy.go` and written by `openAction`.
- **A watch's result has the same gap, and logs the row.** `watchResultOf` is called from the
  transcript (`record.go`), the notices (`notices.go`) and `viewCall`, and two callers log the
  decode error as it is. A `time.Time` parse error quotes the string it refused, which came from
  the cluster.

## The design

The version is the row's existing `contract_name`, never a field inside the JSON and never
the tool's name. `arguments` is the model's bytes verbatim, read by the approval request and sent
back in the history, so nothing is added to it. The name is what the model is taught, so a
storage change does not touch it. No column is added: the one a vendor's rows already fill is
filled for ours.

**Every tool has a contract.** `ContractName()` moves from `tools.Native` to `tools.Reader`, so
every tool answers one. A tool of ours names its contract `<name>/v<n>`: its `Name` lower-cased,
a slash, `v` and a number from 1 (`bash/v1`, `logsview/v1`, `kubequery/v1`). Each tool's package
exports it as `ContractName`, as `anthropicwebsearch` does. A vendor's contract stays the
vendor's own id. Every tool starts at `v1`. A contract covers every JSON the tool itself writes
for a call: its arguments, its shown action, and for `LogsWatch` the `WatchResult` its task
writes. The approval record a gated call leaves is the gate's, not the tool's (below).

**Every row records it, from its first write.** `newToolCall` sets the row's contract from
`Box.ContractOf(name)`, which asks the service's box (`s.tools`) through its readers, so the live
row, the approval request drawn from it, and every later write carry the same value.
`ServerCallSeen` keeps writing a provider's. It is the service's box and not the turn's because the
turn's box reads no stored call: a Bash call on a machine with no shell, or a `LogsView` call a
subagent was never offered, gets its contract through the reader, as it gets its action today. It
is set before the gate because the request is the live row's action: a row that waited for its
contract would be refused by `Reads`, show no command, and offer no Approve. The schema makes the
rule strict: `stmtUpsertToolCall`'s `ON CONFLICT` updates neither `contract_name` nor `cwd`, so a
contract missing from the first write is never filled in. A call naming a tool the box does not
hold keeps `contract_name` NULL, since no tool gives it one. The column stays nullable and free
text, with no CHECK, so a bump never touches the schema. `0001_init.sql`'s comment changes to say
what the column now holds, and its example id becomes `web_search_20260318`, the value the code
writes. Nothing has shipped, so there is no backfill: a row written before this change carries no
contract, reads as its kind alone (below), and a dev database is reset with `make clean-data`.
**NULL is never read as `v1`.** It means no tool gave the row a contract, and no build infers one
from the name.

**The box says whether this build reads a row.** `Box.Reads(name string, contract ContractName)
bool` is true when the row names a tool and the contract equals that tool's `ContractName()`.
Today every tool has one contract, so this equality is the whole check: `Reader.Action` and each
package's `ActionOf` keep their signatures, and no tool switches on anything. `Box.Action` takes
the row's contract and answers false unless `Reads` does. A row written by a newer build and read
by an older one, or a row with no contract, shows its kind and nothing it might misread, as a
call whose arguments its tool refuses does. The first bump moves the comparison into the tool
(*When a contract bumps*).

**A shown action is decoded through the box.** `Box.Shown(name string, contract ContractName,
stored json.RawMessage) (Action, error)` answers the stored action when `Reads` is true, the
JSON decodes into `tools.Action`, and the action is of the tool's kind. Its errors are four bare
sentinels, `tools.ErrNoReader`, `tools.ErrContract`, `tools.ErrDecode` and `tools.ErrKind`, and
never wrap the decoder's error: a `time.Time` parse error quotes the stored string, and a
sentinel is what chat may log. Chat keeps `shown_action` as text when it reads the row and
decodes it through the box in `actionOf`, in the waiting notices, and in `viewCall`, which takes
the box and reads the row's name and contract beside the action. A shown action that does not
decode is no action, not a failed read: the call shows its kind and `viewCall` answers `ok`
false.

**A watch's result is read under its call's contract.** The three readers of `b.result` already
join `tool_calls`; each selects the call's name and contract beside it and calls
`watchResultOf` only when `Reads` is true. `watchResultOf` answers one bare sentinel in place of
the decoder's error. A result that is refused or does not decode is no notice text, no matched
lines and no view. For the transcript and the notices that is what a decode failure does today;
for `viewCall` it is a change: today the failure fails the read, and it becomes `ok` false.

**The waiting notices carry both contracts.** `stmtSelectWaitingNotices` builds two entries,
the task's call and the `Agent` call that spawned its run, and selects a contract for neither.
It selects `t.contract_name` and `a.contract_name`, and both `toolCallEntry` values carry
theirs, or `Reads` refuses them and a Bash notice loses its command and a subagent's notice its
description.

**A decided write is the gate's record, kept whole and not versioned here.** `approvals.request`
is `tools.ActionRequest` over `permissions.Action`: the permission layer's shape, which the live
approval request draws and every gated tool would share. Versioning it under `bash/v1` would tie
the gate's shape to one tool, so this spec gives it no contract; how the gate versions its record
is a follow-up issue, opened when this lands. What this spec does fix is the read. A request that
does not decode keeps its row: the write is served with its approval, status, decision and times
intact, `ClusterWrite.action` null, and `method` and `path` empty. `action` is the one field that
says so, and it is nullable for this alone: `PermissionAction` has no empty value, since its
`class` is a required enum, and a zero `permissions.Action` would reach the wire as a class
outside it. **An unreadable write, waiting or decided, offers Deny alone**: Approve is a click on
a drawn request, and nothing was drawn. The guard exists: `ApprovalRequest` in
`chat-transcript.tsx` draws a change whose `method` is `''` as "This request can't be shown."
with no Approve, in the branch before the one that reads `change.action.class`. An unreadable
write rides that branch, and the order of the two branches is what keeps the rule; a test pins
it. In practice a waiting write is drawn from the journal this build wrote, and a restart abandons
a pending row, so the waiting case should not occur; the rule holds anyway. A decided write is
never dropped from the transcript. The decode answers a sentinel, and the read logs it as below.

**One warning per refused payload per read, naming no stored text.** Each decode site logs one
`slog.Warn` for each payload it refuses, carrying the chat id, the call id, which payload
(`shown_action`, `result` or `request`) and the sentinel, nothing else. A `LogsWatch` row under an
unknown contract logs twice in one load, once for each payload. A read happens at each chat load
and each notice pass, so a corrupt row logs again until `make clean-data` removes it. Nothing
deduplicates; the repetition is the signal that a row needs fixing.

**When a contract bumps.** A tool bumps its contract when a row written under the current one
would read differently:

- `parse` would refuse arguments it accepts now, or read them as something else;
- a stored shown action or watch result would not decode, or would decode as something else: a
  field renamed, retyped, or newly required. A rename is the silent case: nothing fails, the
  field reads as zero, and only the frozen fixtures catch it.

Adding an optional argument or an optional field to a stored shape needs no bump: old rows lack
it and read the same. A change to a type the stored shapes share bumps every tool that stores
it: `LogsScopeInput` and `LogsViewAction` sit in all three log tools' actions, and
`LogsReadLine` and `LogsViewAction` in `WatchResult`. `LogsScopeInput` is an alias of
`services/logs.Scope`, so a JSON tag change in that package bumps three tools; the fixtures catch
it, and `services/logs/CLAUDE.md` says so.

The first bump is the change that gives a tool two shapes to read, and it brings the per-tool
plumbing with it: `Reader.Action`, a `Shown` tool's decode and `watchResultOf` take the row's
contract, `Reads` asks the tool rather than comparing one value, and each decoder switches on the
contract with every case it has ever written, converting an old shape into the current one. The
three move together: a `Reads` that accepts two contracts over a decoder that reads one shape would
drop an old row's fields without an error. The bump itself lives in the tool's package and nowhere
else: the exported `ContractName` moves to the new value, the old value stays as an unexported
constant whose comment says in one line what changed, and the old-rows test gains a table for the
new contract. **The fixture tables are append-only.** A bump adds a table; it never edits one. No
`CLAUDE.md` lists versions, and a bump needs no ADR unless it was itself a judgment call. The name
the model calls the tool by does not change.

**The GraphQL field says what it now holds.** `ToolCall.contract`'s description becomes: "The
shape the call's arguments, shown action and task result are in: `bash/v1` for a tool of ours,
the vendor's own identifier (`web_search_20260318`) for a vendor's. Empty for a call of a tool
this build does not know." `ClusterWrite.action` becomes nullable: "The action the request is,
as the sidecar's classifier read it. Null when this build cannot read the stored request;
`method` and `path` are then empty, and the write offers Deny alone." The webview reads no
contract; the two card test fixtures move from `''` to the tool's contract, and every reader of a
write's `action` in `src/lib/chats.tsx` and `chat-transcript.tsx` takes the null, none of them
before the `method` guard.

**Out of scope.** The history sent to the model carries a call's arguments verbatim under the
tool's name, whatever its contract. The model reads them as text. `tool_calls.result` is what
the model read and is never parsed back; only a task's `background_tasks.result` is.

## Invariants the tests pin

- Every tool in the box answers a contract, unique in the box. A tool of ours answers one
  matching `^[a-z]+/v[1-9][0-9]*$`.
- A sidecar call of a known tool is written with its tool's contract; a call of an unknown tool
  with none.
- `Box.Reads` is false for a name the box lacks and for a contract that is not the reader's, the
  empty contract included; `Box.Action` and `Box.Shown` refuse whatever `Reads` refuses.
- Every tool whose `Action` reads its arguments reads a fixed set of stored `v1` arguments the
  same (`TestActionOfReadsOldRowsTheSame`), `memory`, `kubequery` and `agent` included.
- Every `Shown` tool's fixed set of stored `v1` shown actions decodes through `Box.Shown` the
  same (`TestShownOldRowsDecodeTheSame`); `logswatch`'s covers a stored `WatchResult` too.
- A chat holding a call whose shown action does not decode still loads; the call shows its kind
  and no action, and its view does not open.
- A fixed set of stored `approvals.request` values reads the same
  (`TestClusterWritesReadOldRowsTheSame`); one that does not decode is served with a null
  `action` and an empty `method`, its approval intact, and the chat loads.
- A waiting change with a null `action` and an empty `method`, under a Bash call whose command is
  drawn, shows Deny and no Approve (`chat-transcript.test.tsx`).
- A refused watch result is no notice text, no matched lines and no view, in the transcript,
  the notices and `viewCall` alike, whether the result is malformed or the row's contract is
  unknown.
- A refused payload logs one warning that carries the sentinel and never the stored text: a
  `resolvedAt` that is not a time, holding a marker string, in a shown action and in a watch
  result's `viewAround`, each decode to `ErrDecode`, and a `request` that is malformed JSON holding
  a marker decodes to `ErrDecode` too; each logs the sentinel alone, and the captured log
  (`testutil.CaptureLogs`) lacks the marker.
- The waiting notices read both contracts: a Bash task's notice carries its command, a
  subagent's its `Agent` call's description, and a `LogsWatch` task's its result's text and
  count.

## Tasks

Do them in order. Each one passes `make test-changed`. Both are one pull request.

0. **Contracts on every row.** `ContractName()` on `tools.Reader`; a `ContractName` constant in
   each tool's package; `Box.ContractOf` and `Box.Reads`; `newToolCall` sets the row's contract
   from the service's box; `Box.Action` takes the row's contract and `actionOf` passes
   `toolCallEntry.Contract`; `stmtSelectWaitingNotices` selects `t.contract_name` and
   `a.contract_name` and both entries carry theirs; the `0001_init.sql` comment; the GraphQL
   description, then `gqlgen generate` and `pnpm codegen`; the webview fixtures. Tests in
   `box_test.go`: every tool's contract is unique and of its form; `Reads` and `Action` refuse
   another tool's contract and the empty one. In `turn_test.go`: a sidecar row carries its tool's
   contract from its first write, an unknown tool's row none; a gated Bash call's pending approval
   request carries its command; a refused call of a tool the turn was not offered still shows its
   action. In `store_test.go`: a Bash task's notice carries its command and a subagent's its
   `Agent` call's description. `TestActionOfReadsOldRowsTheSame` in `memory`, `kubequery` and
   `agent`. The chat tests that make call rows by hand (`store_test.go`, `service_test.go`,
   `subagent_test.go`, `approval_test.go`, `tasks_test.go`, `record_test.go`) take their contract
   from one test helper, or their actions and notice text go blank; `testdata/context.md` is
   re-pinned if the contract reaches it.
1. **Shown actions and watch results through the box.** `Box.Shown` and its sentinels; the three
   `shown_action` decode sites in `store.go` go through it, `viewCall` taking the box; the three
   `b.result` readers select the call's name and contract and check `Reads` before `watchResultOf`,
   which answers a sentinel; the cluster-writes read answers a sentinel and serves a null
   `ClusterWrite.action`, then `gqlgen generate`, `pnpm codegen`, and the webview's readers of a
   write take the null and draw the line; each refusal is one warning carrying the sentinel. Tests:
   `TestShownOldRowsDecodeTheSame` in `logsview`, `logsread` and `logswatch`, over fixed JSON
   strings each tool has written, `logswatch`'s with a stored `WatchResult`; in `store_test.go`, a
   chat with a row whose `shown_action` is `{"logsView":7}` loads, the call shows its kind and no
   action, and `viewCall` answers `ok` false; a `LogsWatch` task's notice carries its result's text
   and count; a watch whose `background_tasks.result` is `{"text":1}` and one whose row's
   `contract_name` is `logswatch/v999` each render with no matched lines, notice with no text, and
   open no view; a `shown_action` and a watch result whose `resolvedAt` is a marker string each log
   a warning without the marker; `TestClusterWritesReadOldRowsTheSame` over fixed
   `approvals.request` strings; a chat whose `approvals.request` is malformed JSON holding a marker
   loads with that write's `action` null and `method` empty, its approval intact, and a warning
   without the marker. In `chat-transcript.test.tsx`: a waiting change with a null `action` and
   an empty `method`, under a Bash call whose command is drawn, shows Deny and no Approve.

## Acceptance

- A fresh dev database's `tool_calls` rows carry `bash/v1`, `read/v1` and so on, and the
  vendor's search its own id.
- A chat with `LogsView`, `LogsRead` and `LogsWatch` calls reads and redraws as before, and a
  finished watch's notice reaches the model as before.
- Hand-editing one row's `shown_action` to invalid JSON leaves its chat loading, with that one
  call drawn as its kind and one warning per load in the sidecar's log, quoting none of the row.
- Hand-editing one watch's `background_tasks.result` the same way draws the call with no
  matched lines and opens no view.
- Hand-editing one row's `contract_name` to `bash/v999` draws that call as its kind.
- Hand-editing one `approvals.request` to invalid JSON leaves its chat loading, with that write
  drawn as its approval and "This request can't be shown.", and no Approve.
- `make lint-go` and `make vet-go` pass; the Go coverage gate does not fall.

## Verification

```sh
make test-changed
cd sidecar && go test ./internal/tools/... ./internal/agent/...
cd sidecar && go run github.com/99designs/gqlgen generate && cd .. && pnpm codegen && pnpm build
make lint-go vet-go
```

## When it lands

`sidecar/internal/tools/CLAUDE.md` replaces "Never change how a tool reads rows it has written"
with the contract rule: every tool has a `<name>/v<n>` contract covering its arguments, shown
action and task result, the box reads a row only under its reader's contract (`Reads`), a change
that reads an old row differently bumps it, and the first bump moves the check into the tool, whose
every decoder, the watch result's included, then reads each contract it has written. It says where
a bump is recorded (the tool's package, with append-only fixture tables). `services/logs/CLAUDE.md`
says `Scope`'s JSON is three tools' stored shape, and a tag change there bumps them.
`sidecar/internal/agent/CLAUDE.md` says a call row is given its tool's contract at its first write,
from the service's box, a shown action and a watch's result are decoded only when the box `Reads`
the row, a failure costs one action and logs a sentinel alone. An ADR, *a tool call records its
tool's contract*, records why the version is a column beside the JSON rather than a field in it or
the tool's name, and amends the two ADRs above; their *Consequences* lines on old rows point at it.
A follow-up issue asks how the gate versions `approvals.request`, since a decided write's shape
is the permission layer's and not a tool's. A decided write is never dropped from the
transcript, so no row in `docs/security-model.md` moves. #356 is closed by the PR. This spec is
deleted in the same change.