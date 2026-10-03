---
title: Onboarding
scope: sidecar, webview
status: Planned
---

# Onboarding

**Needs:** step 6A, whose probe the Tools step runs, and steps 3A, 3B and 5B, whose list, mode
picker and popover the flow mounts. **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

By step 6A the sandbox has a Settings section for each of its parts: the `PATH` list (step 3A),
the tools it probed (step 6A), and the approval modes (step 3B). A new user meets none of them until something fails, and the first failure is
`kubectl` missing from the sandbox or a `prod` write refused with no warning.

After this step, **one flow on first launch** walks the three in order, each step skippable,
nothing typed and nothing signed into, as [the note](../../notes/sandbox-credentials-and-permissions.md)'s
*Onboarding* asks:

1. **Programs**: the folders sandboxed commands find programs in, with any waiting for a yes.
2. **Tools**: which `kubectl`, `helm` and the rest the sandbox runs, what each was refused, and
   a plain line when `kubectl` is missing.
3. **Permissions**: the default mode, Ask, and the contexts, `*prod*` read-only.

Finish writes `securityconfig.Settings.Onboarded`, and the flow can be opened again from
Settings. **Nothing widens by default**: every grant in the flow is a click on one path or host,
and the mode is what step 3B ships.

## What is not in this step

- **No new setting but the flag.** Every list, probe and mode is the query and mutation its
  step introduced; this step mounts them in one dialog.
- **No monitor.** Step 6B's Monitoring section is a setting the user finds later; a flow that
  asked about a monitor with no agent behind it would ask about nothing.
- **Windows** gets one screen (§5) and no probe.

## Design

### 1. When

`securityconfig.Settings` gains `Onboarded bool`, tagged `onboarded`, false on a fresh file.
The wire:

```graphql
"The security settings that are one value, not a list."
type SandboxSettings {
  "Whether the onboarding flow has been finished, or skipped to its end, on this machine."
  onboarded: Boolean!
}

extend type Query {
  sandboxSettings: SandboxSettings!
}

extend type Mutation {
  "Write the flag. Finish and the Windows screen pass true; Settings' *Set up the sandbox again* opens the dialog and writes nothing."
  sandboxOnboarded(done: Boolean!): SandboxSettings!
}
```

**The launcher.** `useOnboardingLaunch()` in `src/lib/onboarding.tsx`, mounted by `AppLayout`
under `DialogProvider` — everything there renders once `ReadyGate` has passed, so the query
never races the sidecar's start — runs `sandboxSettings` once per webview and calls
`openDialog('onboarding')` when `onboarded` is false. It opens once: a ref remembers it did,
so a closed dialog is not reopened by a re-render, and a query that fails opens nothing
(`errorReportExchange` reports it). A second window opened meanwhile runs the same query and
may open the dialog too; whichever finishes first writes the flag, and the other's Finish
writes it again, which changes nothing.

**On demand.** The Sandbox section (step 3A) gains a button, *Set up the sandbox again*, which
calls `openDialog('onboarding')`. `DialogId` gains `'onboarding'`, and `AppDialogs` mounts
`OnboardingDialog` under it, so the dialog outlives the chrome that opened it and one dialog is
open at a time: opening it from Settings closes Settings, as the provider does for any pair.

### 2. Three steps, one dialog

`onboarding-dialog.tsx`, on the shared `Dialog` wrapper (`components/widgets/dialog.tsx`),
`sm:max-w-2xl`, title *Set up the sandbox*. A stepper row under the title names the three steps
and marks the current one (`aria-current="step"`); the body is the current step; the footer is
**Back** (disabled on the first), **Skip** and **Next**, and on the last step **Finish** in
Next's place. The current step is the dialog's own state, so closing and reopening starts at
Programs, and Skip goes to the next step and does nothing else. Every step's data is live: a
grant made in it is written at once through its own mutation, so a user who
closes the dialog halfway keeps what they did.

**Programs.** Step 3A's list, the same `SandboxPathList` its Settings section draws (this step
splits it out of `sandbox-settings.tsx` if it is not its own component): the entries in the
shell's order, each *included*, *waiting for you* or *removed*, with **Include** on a waiting
one and **Refresh** above the list. One line under it: *Sandboxed commands find programs in
these folders.* A list with nothing waiting says *Nothing is waiting for you.* under the line.

**Tools.** When the step opens, `sandboxToolsProbe` runs once, with a spinner and *Checking
your tools…* until it answers; opening the step again does not run it again, since a probe
runs every tool. Then step 6A's list, the same component its Settings section draws: each
curated tool with the binary it resolved to and its version, or *not found*; under a tool,
each path it was denied with step 5B's `grant-popover.tsx`, offering **always** alone, since a
grant made here is for every chat and there is no chat to grant it for. A missing `kubectl` is
a red line above the list, `role="alert"`: *kubectl was not found on your PATH. Install it, or
include the folder it is in under Programs, then press Refresh.* The probe's error, if it
fails, is one line under the spinner's place with **Try again**.

**Permissions.** Step 3B's default-mode picker and contexts list, the same components its
Settings section draws (`permission-settings.tsx` exports them), Ask selected; each context
whose mode comes from `*prod*` says *read-only by default*, and an override made here writes
`permissionModeSet` as it does there. Under the picker: *Ask means a change to a cluster, a
new host or a Secret's values waits for you. Read-only refuses the changes instead. Contexts
whose name holds `prod` start read-only.*

**Finish** calls `sandboxOnboarded(true)`, then `closeDialog()`; a refused write leaves the
dialog open with the error on one line and Finish handed back. The close button and Escape
close it without writing, so the launcher opens it again at the next launch.

### 3. What is shared and what is drawn again

| Step | Shared with its Settings section | Drawn for the flow |
| --- | --- | --- |
| Programs | `SandboxPathList` and `useSandboxPath` (step 3A) | the heading, the one line, the *nothing waiting* line |
| Tools | the tools list and `grant-popover.tsx` (steps 5B and 6A) | running the probe on open, the spinner, the `kubectl` alert, Try again |
| Permissions | the mode picker and the contexts list (step 3B) | the line on what Ask means |

Each shared component takes its data through its own hook and writes through its own mutation,
so the flow adds no state of its own but the current step and the probe's one run. The Settings
sections' chrome — their `Field` rows and section headings — is not mounted; the flow has its
own headings. A section whose list is not yet its own component is split in this step's task,
with the section's test unchanged.

### 4. The dialog's words

Every line speaks in the user's terms: folders, programs, contexts. No line says profile,
proxy, token, session, zone, grant rule or class. Where a
Settings component's own text names one, it is that step's to fix, not this one's.

### 5. Windows, and a machine with no sandbox

`sandbox.available` (step 1B) false is one screen in place of the three: *Kstack has no sandbox
on this machine, so every command the model runs waits for you first.* On native Windows that
is the whole text; on Linux the second sentence is the sidecar's `reason` (*user namespaces
are disabled*, in its words). One button, **OK**, calls `sandboxOnboarded(true)` and closes.
No probe runs: nothing routes through a proxy there.

The store is open on every platform (step 1C), so the flag has a home on Windows; `sandboxPath`
keeps answering empty there, and the `PATH` mutations stay refused.

## Decisions this step asks for

1. **The flag lives in `security.json`, beside the settings the flow walks through.** Step 1C
   opens the store on every platform for this reason, so the Windows screen can write it, and
   every *no sandbox* answer keys on `sandbox.available`, never on a missing store.
   Recommended over a second file for one flag.
2. **Once per webview, not once per process.** The launcher reads the query in each window; a
   process-wide latch would need the host, for a dialog two windows rarely both show.
   Recommended.
3. **A closed dialog is not finished.** Only Finish, and the one-screen OK, write the flag, so
   a user who closes it to look at something sees it again next launch, and Skip-to-the-end is
   Finish. Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Onboarded` and its read-back | `securityconfig/`, its tests | — | Planned |
| 2 | The wire and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 1 | Planned |
| 3 | Split each shared list out of its section where it is not its own component | `sandbox-settings.tsx`, step 6A's and `permission-settings.tsx`, their tests | — | Planned |
| 4 | The dialog, the three steps, the Windows screen | `src/components/widgets/onboarding-dialog.tsx`, `src/lib/dialog.ts`, `app-dialogs.tsx`, their tests | 2, 3 | Planned |
| 5 | The launcher and *Set up the sandbox again* | `src/lib/onboarding.tsx`, `src/layouts/app-layout.tsx`, `sandbox-settings.tsx`, their tests | 4 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1 and 3 at the same time, then 2, then 4, then 5, then 6.

## Tests

**`securityconfig`**

- `TestOnboardedIsFalseOnAFreshFile`, and a written true is read back.
- Step 1C's `TestTheStoreOpensWithoutASandbox` gains a case: the flag is served on a machine
  with no sandbox.

**`graph`** (`schema.resolvers_test.go`)

- `TestSandboxSettingsServesTheFlag`, and `sandboxOnboarded` writes it and answers the new
  value.

**Webview** (`onboarding-dialog.test.tsx`, `onboarding.test.tsx`, `app-dialogs.test.tsx`,
`sandbox-settings.test.tsx`)

- The launcher opens the dialog once when `onboarded` is false, not again on a re-render, and
  not at all when it is true or the query fails.
- Each step draws its data off its hook: the Programs list with a waiting entry and Include
  calling `sandboxPathInclude`; the Tools step running `sandboxToolsProbe` once on open, the
  spinner until it answers, the resolved binaries, a denied path with the popover's always
  grant, the `kubectl` alert when it is missing and Try again on a failed probe; the
  Permissions picker with Ask selected and the `*prod*` line.
- Skip goes to the next step and calls nothing; Back returns; Finish calls
  `sandboxOnboarded(true)` then closes; closing by Escape calls nothing.
- With `sandbox.available` false the one screen draws, with the reason on Linux and without it
  on Windows, and OK writes the flag.
- *Set up the sandbox again* opens the dialog and writes nothing.

## Security

Nothing widens by default. Every grant in the flow is a click on a specific path (a Programs
Include, a Tools always-grant), each written by the mutation its step already guards; the mode defaults to Ask with `*prod*` read-only, which is step 3B's default with
or without the flow. The flow adds no mutation but the flag, and the flag gates nothing:
a machine that never finishes onboarding runs the sandbox as the earlier steps left it.

The residual is a user who clicks through every always-grant the Tools step offers without
reading them; the popover names the path and the tool that needs it, which is the guard step 5B
gives every denial.

No security record. `security-model.md` gains one line under the sandbox's rows: the onboarding
flow writes only through the grants' own mutations and a flag that gates nothing.

## When it lands

- **`security-model.md`**: the line above.
- **`sidecar/CLAUDE.md`**: `Onboarded`, the store open on every platform, the wire.
- **Root `CLAUDE.md`**, *Routing & layout* and the Settings dialog: the `onboarding` dialog, the
  launcher in `AppLayout`, the shared list components and *Set up the sandbox again*.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on macOS with a fresh data directory: read the dialog open on the
first paint of the app, the Programs list with any home folder waiting, the Tools step probing
and naming which `kubectl` it found, and the Permissions step with Ask selected and a `*prod*`
context read-only; press Finish, restart, and read no dialog; open Settings and press *Set up the
sandbox again*. Rename `kubectl` off your `PATH` and read the red line. On Windows, or on Linux
with `kernel.unprivileged_userns_clone=0`, read the one screen and its reason.
