---
title: Outside the sandbox is the user's choice
scope: sidecar, webview
status: Done
---

# Outside the sandbox is the user's choice

**Needs:** nothing beyond `main`. **Unblocks:** nothing; every later step assumes the model has no
flag, and step 2C folds this step's runtime field into the session.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the model decides when a command leaves the sandbox: it sets `dangerouslyDisableSandbox`
on a Bash call, and the call waits for the user's approval and then runs unconfined. The note's
threat model says the agent must not be able to run anything outside the sandbox, and a prompt
injection can make the model ask.

After this step:

- **The model has no flag.** `dangerouslyDisableSandbox` leaves the Bash tool's schema. A call
  that sends it is bad input.
- **The user has a switch, per chat.** A chat runs its commands in the sandbox, or, once the user
  switches it, outside. The switch is off for every chat, and the user turns it on in the
  composer, after a dialog says what it means.
- **A command outside the sandbox asks, every time**, as it does today: the request shows the
  command, and Approve is the gate ([the bash tool](../../security/2026-09-18-bash-tool.md)).
- **The model is told which way the chat runs**, on each question, so it can tell the user when a
  command needs what the sandbox lacks and let the user decide.

On a machine with no sandbox nothing changes: every command runs outside it and asks, the switch
is not offered, and the model was never offered the flag.

## What is not in this step

- **No new setting file.** The switch is a column on the chat. Step 1C makes `sandboxconfig`.
- **No change to what a sandboxed command can reach.** Steps 2A, 3A, 4C, 4D, 5C, 5D, 6B and 6C widen the
  sandbox so fewer chats need the switch.
- **No change to `Write` and `Edit`.** They reach the workspace unasked and nothing else, with a
  sandbox or without one, whatever the chat's switch says.

## Design

### 1. The chat's switch

`conversations` gains `outside_sandbox INTEGER NOT NULL DEFAULT 0 CHECK (outside_sandbox IN (0,
1))`, in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md), so a dev
`app.db` from before this step is reset, and with it every stored call that carried the flag.
`chatsvc`'s
conversation row, `Chat` on the wire and the recent list's fold all carry it.

The wire gains:

```graphql
type Chat {
  # …
  "Whether the user switched this chat to run its commands outside the sandbox."
  outsideSandbox: Boolean!
}

"Whether sandboxed Bash is offered on this machine. It is fixed for the sidecar's life, so this is a query and not a watch."
type SandboxStatus {
  available: Boolean!
  "What the probe found, such as Seatbelt or bwrap at /usr/bin/bwrap, or why there is none, in the sidecar's words."
  reason: String!
}
```

`Query` gains `sandbox: SandboxStatus!` in a new `# --- Sandbox ---` section after
`# --- Model ---`, since `Query` has no chat section, and `type SandboxStatus` sits beside
`type Chat`. `Mutation` gains `chatOutsideSandboxSet(id: ChatID!, outsideSandbox: Boolean!):
Chat!` in its existing `# --- Chat ---` block, the setter spelled as `clusterEnabledSet` is. The mutation's doc says it
is refused with `KSTACK_VALIDATION_ERROR` on a machine with no sandbox.

`chatOutsideSandboxSet` writes the column and notifies the chats watch, so every window's list
and pane see the change: the watch folds by value (`deltafold.Equal`), so the changed column is a
`Modified` frame. It leaves `updated_at` alone, which orders the recent list by activity and
moves only for what `Chat.updatedAt`'s doc names: a message, a settled run, a rename. A chat
that is gone is `KSTACK_RECORD_NOT_FOUND`. It does not check the chat's cluster's deletion mark:
a chat whose cluster is going is torn down with it, and a turn started meanwhile fails on the
gone cluster as it does today.

**Available means sandboxed Bash is offered**: a shell was found (`bash.New` answered ok) and the
probe found a sandbox. A machine with a sandbox and no shell offers no Bash, so it has nothing to
switch. `newShell` in `app/app.go` returns the probe's verdict beside the tool, and `app` builds
one `SandboxStatus` from both — the probe's reason, or *no shell was found* — and hands it to
`chatsvc.New` and to `graph.Resolver`, so the query, the mutation and the context (§4) agree.

### 2. The turn reads it once

`tools.Runtime` gains `OutsideSandbox bool`. `chatsvc` reads it from the conversation row where it
reads the chat's cluster (`chatCluster` in `turn.go`, which then answers both), once per turn
in `run()`, so
a switch flipped while a turn runs changes the next turn. `turn` gains `outsideSandbox` beside
`clusterID`, and `Start` in `subagent.go`, which builds its own `tools.Runtime`, sets the field
from it, so a subagent runs as its parent's turn did. Step 2C moves the field into `Session`;
until then it rides the runtime alone.

**What started under a switch keeps it.** A background command, and a background subagent, which
can run for 30 minutes, keep the value their turn read. So a subagent started while the chat ran
outside keeps running outside after the user switches back, and each of its commands still asks.
No command runs outside unasked; the switch governs what starts after it. The dialog's text (§5)
says so.

### 3. Bash reads the runtime, never the arguments

- `sandboxSchema` and `prompts/schema_sandbox.json` go. `Definition` always answers
  `inputSchema`, the reference's schema less the flag plus `workdir`. `parse` refuses
  `dangerouslyDisableSandbox` as it refuses any unknown key, and `errInputSandboxed` and
  `parse`'s `sandboxed` parameter go with it; `CallTimeout` calls the one-argument `parse`.
  `ActionOf` reads a stored row through the same `parse`, so what is shown is what runs. A row
  that carried the flag would read as bad input and show no action; the reset dev `app.db` (§1)
  holds none.
- **`workdir`'s description gains the sandbox's sentence**, which today lives only in
  `schema_sandbox.json`: `prompts/schema.json` takes it, reworded for either way a chat runs,
  since one schema serves both: *A `~` is the user's home outside the sandbox and the workspace in it,
  and a sandboxed command's directory must be under the workspace.* `resolveWorkdir` is
  unchanged; it is told whether the call is sandboxed, as today.
- `input.OutsideSandbox` goes. `sandboxerFor(rt)` answers the tool's sandbox when the tool has
  one and `rt.OutsideSandbox` is false, else nil. Every caller of today's `sandboxerFor(in)`
  moves to it — `Approval`, `runCall`, `runTask` in `task.go`, and `startDir`, which takes the
  runtime in place of the input — and all read the same runtime, so they agree.
- `tools.CommandAction.OutsideSandbox` (`tools/tool.go`) and `CommandAction.outsideSandbox` on the
  wire go. The request's heading keys on the machine's sandbox instead (§5). Task 3 stops setting
  the Go field; task 5 removes it with the wire field, the codegen and the webview's selection
  (`chats.tsx`), so no task leaves the webview's query naming a field the schema lacks.
- A sandboxed call whose `workdir` leaves the workspace is still refused, and its message no
  longer names the flag: *A sandboxed command starts in its workspace, `<ws>`, or a directory
  under it. Name one there, or ask the user to run this chat outside the sandbox.*

### 4. The prompt and the context

- **`prompts/sandbox.md`** is the system prompt of every chat on a machine with a sandbox,
  switched or not, so it says both. Its first paragraph opens *Unless the user has switched this
  chat outside the sandbox, commands run in a sandbox*, and goes on as today. Its second
  paragraph becomes: *If a command needs what the sandbox lacks — the user's files or
  credentials, the network, a helm change, a service account token, or a Secret's values — say
  so and what for. The user can switch this chat to run commands outside the sandbox; the
  question's context says whether they have. Do not work around the sandbox. What follows about
  the user's own credentials, `kubectl diff` and `--dry-run=server` is for a command run outside
  the sandbox.* Its third keeps its guidance for a command that asks, opening *A command outside
  the sandbox waits for the user to approve it* in place of *A command with
  `dangerouslyDisableSandbox`*.
- **`prompts/sandbox_linux.md`** keeps its snap sentence and replaces its last, which names the
  flag: *A command that runs one needs this chat run outside the sandbox.*
- **`prompts/bash.md`** is unchanged. It is the whole prompt on a machine with no sandbox, and on
  one with a sandbox `Tool.Prompt` keeps its heading, replaces its first paragraph with
  `sandbox.md` (and on Linux `sandbox_linux.md`'s sentence), and keeps the rest, so a sentence
  there still reaches every chat. The splice is by blank line, so `sandbox.md` keeps its
  paragraphs apart with one.
- **The question's context** gains a `Sandbox` section after `Workspace`, appended in `chatsvc`
  beside `withWorkspace` (`chatsvc/workspace.go`), inside the send's transaction. The
  transaction already reads the row through `checkChat` (`service.go`), which answers only an
  error today; it returns the switch too, so the section and the check read one row. Its other
  caller, the turn a background notice starts (`notices.go`), ignores it, since that turn writes
  no context. A send that creates its chat has no row to read: the chat starts sandboxed, and
  the section says so. Like every section it is one JSON value, which
  `clustercard.WithSection` fences: `{"commands":"sandboxed"}` or `{"commands":"outside"}`. On a
  machine with no sandbox the section is left out. The block is attached when what it holds
  changes, so a switch reaches the model as a new block on the next question. `contextOf` in
  `chats.tsx` already draws the whole block.
- **`chatsvc/prompts/system.md`** explains each section to the model. Its Workspace paragraph
  stops calling that section the last, and a paragraph follows it: *The last section,
  `## Sandbox`, says where this chat's commands run: `sandboxed` in the sandbox, `outside` as the
  user, each waiting for their approval. It is absent on a machine with no sandbox.* The context
  is written at the send and the turn reads the switch when it starts, so a switch flipped
  between the two, or before a turn a background notice starts, reaches the model on the next
  question. A subagent reads its parent's newest context, so it can hold the same stale belief
  about the turn it runs under. Either way the model's belief errs toward a command that asks or
  one that is refused, never toward one that runs unasked outside. The section is attached
  whatever the chat's model; one that takes no tools reads it and runs nothing.

### 5. The composer's switch

`useSandbox()` (`src/lib/sandbox.tsx`) is the one reader of the `sandbox` query: a plain
`useQuery`, as `useModels` is, with no retry of its own and no polling, since the answer cannot
change while the sidecar runs. It answers `available` as `true`, `false` or unknown — unknown
until the query answers, and after a failure.

`ChatComposer` draws, left of the model select, a **sandbox button** for an open chat on a
machine with a sandbox: a shield icon and *Sandboxed*, or an open shield and *Outside the
sandbox* when the chat's switch is on. The composer does not hold the `Chat` today. `OpenChat`
in `chat-pane.tsx` does, and passes it `outsideSandbox` and the chat's id; the dashboard's panel
mounts the same `ChatPane`, so it draws the button too. While the list watch has not delivered
the chat (`chat` is `undefined`), the button is drawn disabled as *Sandboxed*, the value a chat
starts with, and the notices for a deleted chat or one of another cluster draw no composer.
While `available` is unknown the button is not drawn. `NewChatPane` draws none, since the chat
has no row yet: a chat starts sandboxed, so a user who wants a new chat outside sends its first
question sandboxed and switches after. Carrying the switch in the unstarted chat's outbox entry is a
follow-on, not this step.

- Pressing it while off opens a confirm `Dialog`: *Run this chat's commands outside the sandbox?*
  — *Every command will run as you, with your files, your credentials and the network, after you
  approve it. Kstack's sandbox will not confine it. You can switch back at any time; what
  already started keeps running as it started.* — with *Run outside the sandbox* and Cancel.
  Confirm calls `chatOutsideSandboxSet(id, true)`.
- Pressing it while on calls `chatOutsideSandboxSet(id, false)` with no dialog: narrowing needs
  no warning.
- The button is disabled while the mutation is in flight, and an error hands it back;
  `errorReportExchange` reports it.

The request's heading for a call outside the sandbox stays *Run this command outside the
sandbox?* (or the background form), now keyed on the machine's `sandbox.available` alone, which
`ChatTranscript` takes as a prop from `OpenChat`: on a machine with a sandbox a command asks only
when it runs outside it, so every command's request there is one. That rests on one invariant,
that a sandboxed command never asks (`Skip` is `sandboxed` in `Approval`), which
`assertSandboxedCallsAskNoOne` pins; a later step that makes a sandboxed command ask brings back a
per-call field first. While `available` is unknown, the heading is the outside one: on a machine
with a sandbox it is right, and on one without, every command does run outside a sandbox, so
the heading never says less than is true. It is never keyed on the chat's switch, which the
user can flip while a request waits: the turn read the switch when it started, so the waiting
command still runs outside, and a heading that followed the switch would say otherwise. A machine with no sandbox keeps today's headings. The disclosure line stays
`in <dir>` for a call outside and `in <dir>, sandboxed` for one inside, off the row's
`sandboxed`, as today.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The column, the row, the mutation, the query and the status `app` builds | `appdb/migrations/0001_init.sql`, `chatsvc/` (with `testutil_test.go`'s constructors for `chatsvc.New`'s new argument), `sidecar/graph/schema.graphqls`, `sidecar/graph/resolver.go`, `sidecar/graph/schema.resolvers.go`, the graph tests' servers, `app/app.go`, `app/app_unix_test.go` (`newShell`'s new result), generated code, their tests | — | Done |
| 2 | `Runtime.OutsideSandbox`, read with the chat's cluster and set on a subagent | `tools/tool.go`, `chatsvc/turn.go`, `chatsvc/subagent.go`, their tests | 1 | Done |
| 3 | Bash: the flag goes; the runtime decides; `workdir`'s description; the message; the prompts | `tools/bash/bash.go`, `tools/bash/task.go`, `tools/bash/prompts/`, `app/app_unix_test.go` and `app/testutil_unix_test.go` (the flag's end-to-end tests), `sidecar/graph/schema.resolvers_test.go` (`TestCommandActionCarriesTheSandbox` sends the flag), every `bashInput` call in `app_unix_test.go`, their tests | 2 | Done |
| 4 | The context's `Sandbox` section and the system prompt's paragraph | `chatsvc/workspace.go`, `chatsvc/service.go`, `chatsvc/notices.go` (`checkChat`'s other caller), `chatsvc/prompts/system.md`, their tests | 2 | Done |
| 5 | `CommandAction.outsideSandbox` goes; `Chat.outsideSandbox` joins the chats watch's selection; codegen; `useSandbox`; the composer's button and dialog; the headings | `tools/tool.go`, `sidecar/graph/schema.graphqls`, generated code, `src/gql/`, `src/lib/sandbox.tsx`, `src/lib/chats.tsx`, `src/components/widgets/chat-composer.tsx`, `chat-pane.tsx`, `chat-transcript.tsx`, their tests, `chats.test.tsx` (it pins the selection) and every fixture that carries `outsideSandbox` (`chat-transcript.test.tsx`), and every test fixture that builds a `Chat` (`chat-nav.test.tsx` among them) | 1, 3 | Done |
| 6 | Docs, per *When it lands* | see there | 1–5 | Done |

**Order:** 1, then 2, then 3 and 4 at the same time, then 5, then 6.

## Tests

**`chatsvc`**

- `TestAChatStartsSandboxed`: a new chat's `outsideSandbox` is false.
- `TestTheSwitchIsWrittenAndWatched`: `chatOutsideSandboxSet` flips the column, leaves
  `updatedAt` alone, and the chats watch delivers a `Modified` frame carrying it.
- `TestTheSwitchIsRefusedWithoutASandbox`: `KSTACK_VALIDATION_ERROR`.
- `TestATurnsRuntimeIsItsChatsCluster` and `TestASubagentsRuntimeIsItsChatsCluster` grow to
  `OutsideSandbox`: a turn started on a chat switched on runs its calls with it true, a switch
  flipped mid-turn does not change that turn's runtime, and a subagent's runtime carries its
  parent turn's value.
- `TestTheContextSaysWhereCommandsRun`, beside `TestTheContextNamesTheWorkspace`: the section is
  `{"commands":"sandboxed"}`, then `{"commands":"outside"}` after the switch, and absent on a
  machine with no sandbox.
- `TestTheSystemPromptExplainsTheWorkspaceSection` follows the new wording, and
  `TestTheSystemPromptExplainsTheSandboxSection` pins the new paragraph.

**`bash`**

- `TestTheFlagIsBadInput` replaces `TestTheFlagIsAcceptedOnlyWhereItIsOffered`: a call carrying
  `dangerouslyDisableSandbox` is refused as bad input on every machine, and Bash's
  `TestTheDefinitionIsTheReferences` holds the one schema and `workdir`'s new description.
  `TestTheFlagKeepsTheCallTimeout` and `TestActionOfReadsTheFlagAnywhere` go with the flag.
- `assertSandboxedCallsAskNoOne`, under `TestASandboxedCallAsksNoOne` and
  `TestASandboxedBackgroundCallAsksNoOne`, keys on the runtime: with a sandbox, a runtime whose
  `OutsideSandbox` is false gets a sandboxed call that asks no one, and one whose
  `OutsideSandbox` is true a call outside that asks; without a sandbox both ask and neither is
  sandboxed.
- `TestASandboxedWorkdirOutsideTheWorkspaceIsRefused` pins the refusal's new text.
- `TestThePromptOpensWithTheSandboxWhenThereIsOne` and `TestTheLinuxPromptSaysASnapRunsOutside`
  follow the new text, and `TestNoPromptNamesTheFlag` pins that neither the prompt on either
  platform nor the definition holds `dangerouslyDisableSandbox`.

**`app`**

- `TestTheSandboxStatusIsTheShellAndTheProbe`: available, with the probe's reason, with a shell and a sandbox; not, with
  the probe's reason, without a sandbox; not, with *no shell was found*, without a shell.
- `TestTheFlagAsks` (`app_unix_test.go`), end to end, becomes `TestASwitchedChatAsks`: the chat
  is switched through the mutation, and its command waits for the user. `bashInput` in
  `testutil_unix_test.go` loses its flag argument.
- `TestBashIsOfferedWithTheSandbox` asserts the schema holds no `dangerouslyDisableSandbox`.

**`graph`**

- `TestCommandActionCarriesTheSandbox` goes with `CommandAction.outsideSandbox`; its staged call
  carried the flag.

**Webview** (`sandbox.test.tsx`, `chat-composer.test.tsx`, `chat-pane.test.tsx`,
`chat-transcript.test.tsx`)

- `useSandbox` asks once and holds the answer, and is unknown before it and after a failure.
- The button draws *Sandboxed* for a chat switched off, *Outside the sandbox* for one switched
  on, disabled as *Sandboxed* before the list watch delivers the chat, and nothing on a machine
  with no sandbox or on the unstarted pane; `OpenChat` hands the composer the chat's switch.
- Turning it on opens the dialog and calls the mutation only on confirm; turning it off calls it
  at once.
- The outside heading follows the machine's sandbox, holds while the chat's switch flips under
  a waiting request, is drawn while the `sandbox` query has not answered, and a machine with no
  sandbox draws today's headings.

## Security

What the user decides on is unchanged: a command outside the sandbox asks with its exact text,
and Approve is the gate. What changes is who chooses to leave the sandbox. Before this step the
model could ask to, on any call, and an injection could make it ask persuasively; after it, only
the user can, for one chat, through a dialog that says what it means, and no chain of approvals
gets there.

The residual is the user switching a chat on and then approving a command they did not read
closely, which is the bash tool record's residual today.

A security record, `docs/security/<date>-outside-the-sandbox-is-the-users-choice.md`, records
the change and the tests. `security-model.md`'s bash row, which names the flag and its tests,
moves to say the switch is the chat's.

## When it lands

- **The security record** above, and an ADR: the model asks for nothing outside the sandbox; the
  user switches a chat, since the chat is the session. It gets its row in `docs/adr/README.md`'s
  index. Two ADRs describe the flag as current —
  [the sandbox is the gate](../../adr/2026-09-28-the-sandbox-is-the-gate-for-a-sandboxed-command.md)
  and [native Windows has no sandbox](../../adr/2026-09-28-native-windows-has-no-sandbox.md). The
  first is amended, not superseded: its decision, that a sandboxed command runs unasked, still
  holds, and only its paragraph on the flag is answered. Per `docs/adr/README.md`, it keeps
  `status: Accepted` and gains an `amended_by` pointer to the new ADR, which names the paragraph
  it replaces; `CLAUDE.md` links to it stay. The second names the flag only as absent on Windows,
  which stays true.
- **`security-model.md`**: the bash row (a command outside the sandbox) and its tests, renamed as
  *Tests* says.
- **`sidecar/CLAUDE.md`**: every place that names the flag, `input.OutsideSandbox` or
  `CommandAction.OutsideSandbox`; the Bash tool's one schema, `sandboxerFor` off the runtime,
  `Runtime.OutsideSandbox`, the column, the mutation, the query, `SandboxStatus` and the context
  section.
- **Root `CLAUDE.md`**, *Chat*: *A command the model asks for outside the sandbox* becomes a
  command in a chat the user switched outside; the composer's button and dialog, `useSandbox`, the headings keyed
  on the machine's sandbox rather than `outsideSandbox` (the background-command paragraph names
  them too), `ToolCall.action.command`'s field list less `outsideSandbox`, and the context's
  `Sandbox` section.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on macOS or Linux: ask for `ls ~` and read it empty with no request;
switch the chat, confirm the dialog, ask again, and read the request *Run this command outside
the sandbox?*, then the listing once approved; switch back and read the sandboxed answer again.
Ask the model to run a command outside the sandbox without switching, and read it decline and
name the switch.
