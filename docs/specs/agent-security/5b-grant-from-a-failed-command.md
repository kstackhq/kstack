---
title: Grant a folder from a failed command
scope: webview, sidecar
status: Planned
---

# Grant a folder from a failed command

**Needs:** step 4D, whose `folderGrant` and Settings Add row this step reuses. **Unblocks:**
steps 6A and 7A, which open its grant form on a path a probe was denied.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command that needs a file the sandbox hides fails with its tool's own error —
`Permission denied`, `No such file or directory` — and the model reads that error and the
sandbox line, *(Ran in the sandbox: …)*. A user who wants to grant the folder has to open
Settings. [The note](../../notes/sandbox-credentials-and-permissions.md) calls this "the biggest
usability failure of sandboxes".

After this step, on macOS and Linux:

- **A failed sandboxed command offers a grant where it failed.** Under the call, a muted
  *Grant a folder…* opens `FolderGrantForm` in place, under the call: the folder, read or read
  and write, for this chat or always.
- **The user names the folder.** The field starts empty, and nothing reads the command's output
  to fill it. The user opens the call's disclosure, reads the error there — the same text the
  model read — and types or pastes the folder; the form sits under the disclosure, so both are
  on screen at once.
- **The model's result does not change.** It reads its command's output and the sandbox line as
  today. The prompt tells it the user can grant a folder from under the command that failed.
- **Settings' Add row is the same form**, with Always alone.

## What is not in this step

- **No path detection.** Nothing parses a command's output or reads Seatbelt's log. Step 6A
  finds what a probe was denied, where the run is the app's and its report reaches the user
  alone.
- **No new mutation.** `folderGrant` is step 4D's, with its checks.
- **No new line for a refused write or Secret read.** Steps 4B and 5A draw them in the call's
  disclosure.
- **No re-run.** A grant never runs the command again.
- **No change to step 3A's Include**, which stays in Settings.
- **No offer for a command outside the sandbox** (step 1B), which runs as the user. Nothing
  changes on Windows, which has no sandbox.

## Design

### 1. Where the offer is drawn

`ToolCalls` in `chat-transcript.tsx` draws *Grant a folder…* after a call's disclosure, outside
it, where a background call's **Stop** is drawn, for a call that `grantOffered` passes:

- `action.command.sandboxed` is set, and
- it failed: a foreground call whose `status` is `Failed`, or a background call whose
  `background.status` is `Exited` with an `exitCode` that is not `0` — null included, since a
  code the sidecar could not read may be a failure.

`grantOffered(call)` in `chats.tsx` is the one test of that. Nothing tells a hidden path from
any other failure, so the offer is drawn on every one: a muted link-styled button on one line,
never a prompt, carrying `aria-expanded` for the form under it. A subagent's call draws it where its calls are drawn, inside its `Agent` call's
disclosure, since `AgentBody` draws them through `ToolCalls`. A call that succeeded, one outside
the sandbox, and every other tool draw none. `ToolCalls` takes the chat's id for the form, from
`ChatTranscript`'s `chatID`, and `AgentBody` passes it on.

**`GrantOffer`** holds the offer's state for one call: closed, open, or granted. Pressing the
button opens the form under it, with a Cancel beside the submit; a grant that answers closes
the form and leaves *Granted* in the button's place, until the pane unmounts. The state is the
component's own and `ToolCalls` keys it on the call's id, so one call's offer never reaches
another's.

### 2. The form

`folder-grant-form.tsx` is step 4D's Add row lifted out of `sandbox-settings.tsx`:

```ts
type Props = {
  chatID?: string;
  durations: GrantDuration[];           // ['Always'], or ['Chat', 'Always']
  submitLabel: string;
  initial?: { path: string; write: boolean };
  onGranted?: () => void;
  onCancel?: () => void;                // draws Cancel when set
};
```

over `useSandboxFolders(chatID).grant(path, write, duration)`. `initial` fills the field and
the checkbox: step 6A passes a denied path's folder, and the transcript and Settings pass none.
It holds:

- the path field, mono, sent as typed, whitespace included, since the folder drawn is the folder
  granted;
- a *read and write* checkbox;
- when `durations` holds both, a segmented *For this chat* / *Always*, *For this chat* first
  and picked by default, since it is the narrower grant;
- step 4D's wide warning while the cleaned path is one of `useSandboxFolders().wide`;
- the submit button, disabled while the field is empty or a grant is in flight, and Cancel
  beside it when `onCancel` is set;
- a refusal's reason under the field, and for one carrying a `target`, *Grant `<target>`*,
  which fills the field.

Its row wraps (`flex-wrap`), since the dashboard's right sidebar can be 240px wide and the
transcript mounts it there: the field takes the first line, the checkbox, the picker and the
buttons fall to the next. `cleanPath`, `wideWarning` and the refusal's drawing move with it. The Settings section mounts
it with `durations={['Always']}` and `submitLabel="Add"`, and draws no picker and no Cancel,
so the section works as it does today; its Folders list, the macOS line and *Never readable*
stay where they are. The transcript mounts it with both durations, `submitLabel="Grant"`, and
`onCancel`.

**A chat grant reaches the composer's list.** *Allowed for this chat* reads `chatGrants`, which
a `SandboxFolders` answer does not touch, so `grant` runs its mutation with `chatGrantsContext`
(from `src/lib/chat-grants.tsx`) and the list asks again.

### 3. The prompt

`tools/bash/prompts/sandbox.md`'s line *A tool that cannot find its own files under the home
needs a folder the user grants, or this chat switched outside the sandbox.* becomes *A tool
that cannot find its own files under the home needs a folder the user grants, which they can do
from under the command that failed or in Settings: say which folder and why, and run the
command again once they have.* The prompt never suggests switching the chat outside the
sandbox for one folder; its later line, *A path a command could not read is one the user can
grant, in Settings or from the chat*, stays.

### 4. The seam with step 5A

Both steps edit `chat-transcript.tsx` and `prompts/sandbox.md`, in different places. The step
that lands second keeps the first's lines.

## Decisions this step asks for

1. **The user names the folder; nothing reads the output for it.** A path in a command's output
   is the command's claim. Turning it into a folder offer either reads the disk, which tells
   the model about the machine through what it is offered, or guesses. The user reads the same
   error the model read and types the folder. Recommended.
2. **The offer is drawn under every failed sandboxed command.** Without reading the output
   nothing tells a hidden path from another failure, so the offer is muted and costs one line.
   Recommended.
3. **The form opens in place, not in a popover or a dialog.** The user types a path they read
   off the call's output, and anything that floats over the transcript covers it. An inline
   form needs no anchor, no focus trap and no Escape handling, and `src/` has no popover yet.
   Recommended.
4. **A grant never re-runs the command.** The model's next command is the model's, with the
   grant in place. Recommended.
5. **Settings' Add row and the offer are one form.** One component draws the field, the wide
   warning and the refusal; two copies would drift. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `FolderGrantForm` out of the Settings Add row; the Folders section on it | `src/components/widgets/folder-grant-form.tsx`, `src/components/widgets/sandbox-settings.tsx`, their tests | — | Planned |
| 2 | `grant` under `chatGrantsContext` | `src/lib/sandbox-folders.tsx`, its test | — | Planned |
| 3 | `grantOffered`; `GrantOffer` and the chat id through `ToolCalls` | `src/lib/chats.tsx`, `src/components/widgets/chat-transcript.tsx`, their tests | 1, 2 | Planned |
| 4 | The prompt | `tools/bash/prompts/sandbox.md`, `tools/bash/bash_test.go` | — | Planned |
| 5 | Docs, per *When it lands* | see there | 1–4 | Planned |

**Order:** 1, 2 and 4 at the same time, then 3, then 5.

## Tests

**Webview**

- `folder-grant-form.test.tsx`: the path sent as typed; read and write; `initial` filling both;
  both durations with *For this chat* first and picked, and Always alone with no picker; the
  wide warning for the home; a refusal's reason, and *Grant `<target>`* filling the field;
  disabled while empty and in flight; Cancel drawn with `onCancel` alone; `onGranted` called
  once the mutation answers.
- `sandbox-settings.test.tsx`: the Folders section's Add tests pass unchanged on the form.
- `sandbox-folders.test.tsx`: `grant` runs with `chatGrantsContext`.
- `chats.test.tsx`: `grantOffered` is true for a sandboxed foreground `Failed` call and a
  sandboxed background call exited non-zero or with a null code; false for one running,
  succeeded, exited 0, stopped or lost, one outside the sandbox, and every other tool.
- `chat-transcript.test.tsx`: the offer drawn after the disclosure, and a subagent's inside its
  `Agent` disclosure; the button's `aria-expanded` following the form; the form opening in place
  with an empty field and both durations; Grant calling `folderGrant` with the chat's id;
  *Granted* after; Cancel closing it; no offer outside the sandbox.

**`bash`**: `TestThePromptOpensWithTheSandboxWhenThereIsOne` pins the new line and that the
old one is gone.

## Security

Nothing widens. A grant is the user's typing, as in Settings, checked by step 4D's
`folderGrant`; the transcript adds a place to make one, not a way. Nothing a command prints fills
the field, so a command cannot propose a folder. The model's result is unchanged, so it learns
nothing new about the machine. A chat grant shows at once in *Allowed for this chat*.

The residual is a user who grants what a model's answer or a command's error persuaded them to
type. The guard is Settings': the field holds exactly the folder granted, and the wide warning
names a grant that reads more than a project.

No security record. `security-model.md`'s path-grants row names the transcript's offer beside
Settings, with the form's tests.

## When it lands

- **`security-model.md`**: the row above.
- **Root `CLAUDE.md`**, *Chat*: `grantOffered`, `GrantOffer` and the offer under a failed
  sandboxed command; the Settings dialog: `FolderGrantForm` and `grant` under
  `chatGrantsContext`.
- **`sidecar/CLAUDE.md`**: the prompt's line.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands).

By hand, `pnpm tauri dev` on macOS and on Linux, with `<home>` the real home: ask for `cat
<home>/code/my-service/README.md` and read the call fail with *Grant a folder…* under it; press
it, type `<home>/code/my-service`, choose read and this chat, press Grant, and read *Granted*
and the folder in *Allowed for this chat*; ask again and read the file. Open the offer on
another failure, type `<home>`, and read the wide warning. Type a link to a folder and read the
refusal with *Grant `<target>`*. Ask for `ls` in the workspace and read no offer. In Settings,
add a folder and read the section as before.
