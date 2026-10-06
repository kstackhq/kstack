---
title: Onboarding
scope: sidecar, webview
status: Planned
---

# Onboarding

**Needs:** steps 3A, 3B and 6A, whose `PATH` list, mode picker and executables report the flow
mounts. **Unblocks:** nothing.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

By step 6A the sandbox has a Settings section for each of its parts: the `PATH` list (step 3A),
the executables it probed (step 6A), and the approval modes (step 3B). A new user meets none of
them until something fails, and the first failure is `kubectl` missing from the sandbox or a
write refused with no warning.

After this step, **one flow on first launch** walks the three in order, each step skippable,
nothing typed and nothing signed into, as [the note](../../notes/sandbox-credentials-and-permissions.md)'s
*Onboarding* asks:

1. **Programs**: the folders sandboxed commands find programs in, with any waiting for a yes.
2. **Executables**: which `kubectl`, `helm` and the rest the sandbox runs, what each reported,
   and a plain line when `kubectl` is missing.
3. **Permissions**: the default mode and the contexts.

Finish writes `securityconfig.Settings.Onboarded`, and the flow can be opened again from
Settings. **Nothing widens by default**: every change in the flow is a click on one entry or
one mode, no chat starts with network, and the mode is what step 3B ships.

## What is not in this step

- **No new setting but the flag.** Every list, probe and mode is the query, watch and mutation
  its step introduced; this step mounts them in one dialog.
- **No folder grants.** Step 6A reports each executable's own error and no denied path, so the
  flow has nothing to offer a grant on; folders are granted in Settings' *Folders* section, or
  under a failed command (step 5B).
- **No registering executables.** *Your executables* stays in Settings.
- **No monitor.** Step 6B builds its plumbing and no setting; a flow that asked about a
  monitor with no agent behind it would ask about nothing.
- **Windows** gets one screen (§5) and no probe.

## Design

### 1. When

`securityconfig.Settings` gains `Onboarded bool`, tagged `json:"onboarded,omitempty"`, false on
a fresh file. The wire:

```graphql
"The onboarding flow's state on this machine."
type Onboarding {
  "Whether the flow has been finished."
  finished: Boolean!
}

extend type Query {
  onboarding: Onboarding!
}

extend type Mutation {
  "Mark the flow finished. Finish and the no-sandbox screen's OK call it."
  onboardingFinish: Onboarding!
}
```

The mutation only sets the flag: nothing in the app unsets it, so it takes no argument.

**The launcher.** `useOnboardingLaunch()` in `src/lib/onboarding.tsx`, mounted by `AppLayout`
under `DialogProvider` — everything there renders once `ReadyGate` has passed, so the query
never races the sidecar's start — runs `onboarding` once per webview and calls
`openDialog('onboarding')` when `finished` is false **and `useSandbox()` has answered**:
`available` is `true` or `false`. `useSandbox` answers undefined both before it answers and after
a failure, and a failure read as *no sandbox* would draw the one screen (§5), whose OK finishes
the flow on a machine that has a sandbox. So the launcher waits for both queries, and either
failing opens nothing (`errorReportExchange` reports it); the next launch asks again. It opens
once: a ref remembers it did, so a closed dialog is not reopened by a re-render. A second
window opened meanwhile runs the same query and may open the dialog too; whichever finishes
first writes the flag, and the other's Finish writes it again, which changes nothing.

**On demand.** The Sandbox section (step 3A) gains a button, *Set up the sandbox again*, which
calls `openDialog('onboarding')` and writes nothing. `DialogId` gains `'onboarding'`, and
`AppDialogs` mounts `OnboardingDialog` under it, so the dialog outlives the chrome that opened
it and one dialog is open at a time: opening it from Settings closes Settings, as the provider
does for any pair. The section is drawn only on a machine with a sandbox, so the no-sandbox
screen (§5) is shown once and has no button to open it again; it says nothing Settings does not.
Either way the dialog opens only with `available` known, so it picks its screen off a boolean.

### 2. Three steps, one dialog

`onboarding-dialog.tsx`, on the shared `Dialog` wrapper (`components/widgets/dialog.tsx`),
`sm:max-w-2xl`, title *Set up the sandbox*. A stepper row under the title names the three steps
and marks the current one (`aria-current="step"`); the body is the current step; the footer is
**Back** (disabled on the first) and **Next**, and on the last step **Finish** in Next's place.
Every step's data is live — a change made in it is written at once through its own mutation —
so Next is also how a step is skipped, and a user who closes the dialog halfway keeps what they
did. The current step is the dialog's own state, so closing and reopening starts at Programs.

The three steps' body holds one `useSandboxExecutables()` for the dialog's life, as Settings'
`SandboxSections` does, so a Refresh PATH on Programs starts the probe the Executables step
reads. The no-sandbox screen (§5) mounts none, so it opens no watch.

**Programs.** Step 3A's list, `SandboxPathList`, with `onRefreshed` the dialog's `probe`: the
entries in the shell's order, each *included*, *waiting for you* or *removed*, with **Include**
on a waiting or removed one, **Remove** on an included or waiting one, and **Refresh PATH**
above the list. One line under it: *Sandboxed commands find programs in these folders.* A list
with nothing waiting says *Nothing is waiting for you.* under the line.

**Executables.** Step 6A's report, `SandboxExecutableList`, off the dialog's
`useSandboxExecutables()`: each curated executable with the binary it resolved to, *ok* or
*failed*, its version and its own error, or the sidecar's *not found on the sandbox's PATH*, and
**Probe again**, which reads *Probing…* with a spinner while `probing`. The launch probe has
usually run by the time the step opens — a fresh machine's first `PATH` sync moves the list,
which starts one — so the step decides once per dialog, the first time it is shown: on the
latest report the dialog's hook holds (waiting for the watch's first frame if none has
arrived), it starts a probe only when none is running and an executable is not probed
(`probed` false). The hook subscribes when the dialog opens, so its first frame can predate
the step; the decision reads what the watch says when the step opens, not that frame.

A missing `kubectl` (probed and not resolved) is a line above the list, `role="alert"`:
*kubectl was not found on the sandbox's PATH. Install it, or go Back to Programs and include
the folder it is in, then Probe again.* An Include starts no probe, so the step's Probe again
is what checks. A refused probe draws the hook's `probeError` under the button, as in Settings.

**Permissions.** Step 3B's default-mode picker and contexts list, the same components its
Settings section draws, showing the default mode the settings hold (Ask on a fresh file); an
override made here writes `permissionModeSet` as it does there. The picker carries its own line
per mode, as in Settings, so the flow adds only what Settings does not say, under the
contexts: *Set a production context to read-only here. Sandboxed commands have no network
until you turn it on in a chat.*

**Finish** calls `onboardingFinish`, then `closeDialog()`; a refused write leaves the
dialog open with the error on one line and Finish handed back. The close button and Escape
close it without writing, so the launcher opens it again at the next launch.

### 3. What is shared and what is drawn again

| Step | Shared with its Settings section | Drawn for the flow |
| --- | --- | --- |
| Programs | `SandboxPathList` and `useSandboxPath` (step 3A) | the heading, the one line, the *nothing waiting* line |
| Executables | `SandboxExecutableList` and `useSandboxExecutables` (step 6A) | the probe on open when nothing is probed, the `kubectl` alert |
| Permissions | `DefaultModePicker`, with its line per mode, and `ContextModes` (step 3B) | the line on production contexts and the network |

Each shared component takes its data through its own hook and writes through its own mutation,
so the flow adds no state of its own but the current step and whether it started a probe. The
Settings sections' chrome — their `Field` rows, labels and descriptions — is not mounted; the
flow has its own headings. Task 3 splits what is not yet its own component, with each section's
test unchanged:

- `SandboxPathList` draws its own `Field`, label and description; they move to the section.
- `SandboxExecutableList` draws the section's chrome, its own `kubectl` line (*include its
  folder above*, which is wrong in the flow) and `RegisteredExecutables`; all three move to the
  section, leaving the report and Probe again.
- `PermissionSection` holds the query, its focus refresh, the mutations and the error inline.
  They become `usePermissionSettings()` in `src/lib/permission-settings.tsx`, and the picker,
  with its held-mode line and its line per mode, becomes `DefaultModePicker` beside
  `ContextModes`; both are exported. While `modes` is held, the flow's `ContextModes` is
  disabled as in Settings, and one line says to fix it in Settings. Opening Settings closes the
  flow unfinished, so it opens again at the next launch, or from *Set up the sandbox again*.

### 4. The dialog's words

Every line speaks in the user's terms: folders, programs, contexts. No line says profile,
proxy, token, session, zone, grant rule or class. Where a Settings component's own text names
one, it is that step's to fix, not this one's.

### 5. Windows, and a machine with no sandbox

`sandbox.available` (step 1B) false is one screen in place of the three: *Kstack has no sandbox
on this machine, so every command the model runs waits for you first.* On native Windows that
is the whole text; elsewhere the second sentence is the sidecar's `reason` (*user namespaces
are disabled*, in its words). One button, **OK**, calls `onboardingFinish` and closes.
No probe runs: the sidecar refuses one there.

The store is open on every platform (step 1C), so the flag has a home on Windows; `sandboxPath`
keeps answering empty there, and the `PATH` mutations stay refused.

## Decisions this step asks for

1. **The flag lives in `security.json`, beside the settings the flow walks through.** Step 1C
   opens the store on every platform, so the no-sandbox screen can write it, and every
   *no sandbox* answer keys on `sandbox.available`, never on a missing store. Recommended over
   a second file for one flag.
2. **Once per webview, not once per process.** The launcher reads the query in each window; a
   process-wide latch would need the host, for a dialog two windows rarely both show.
   Recommended.
3. **A closed dialog is not finished.** Only Finish, and the one-screen OK, write the flag, so
   a user who closes it to look at something sees it again next launch. Recommended.
4. **The flow reuses the launch probe.** A probe runs every executable, so the step starts one
   only when nothing has been probed. Recommended over a probe each time the step opens.
5. **An existing install sees the flow once.** A `security.json` written before this step has
   no flag, so a user who set the sandbox up in Settings meets the flow on the first launch
   after upgrading. It shows their current settings and writes nothing until they change one,
   so Finish or Escape costs a click. Recommended over inferring *onboarded* from settings that
   a user may never have looked at.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Onboarded` and its read-back | `securityconfig/`, its tests | — | Planned |
| 2 | The wire and codegen | `sidecar/graph/schema.graphqls`, `graph/`, generated code, `src/gql/` | 1 | Planned |
| 3 | Split the shared lists out of their sections (§3) | `sandbox-settings.tsx`, `permission-settings.tsx`, `src/lib/permission-settings.tsx`, their tests | — | Planned |
| 4 | The dialog, the three steps, the no-sandbox screen | `src/components/widgets/onboarding-dialog.tsx`, `src/lib/dialog.tsx`, `app-dialogs.tsx`, `src/lib/sandbox.tsx` (`reason`), `src/lib/platform.ts` (`isWindows`), their tests | 2, 3 | Planned |
| 5 | The launcher and *Set up the sandbox again* | `src/lib/onboarding.tsx`, `src/layouts/app-layout.tsx`, `sandbox-settings.tsx`, their tests | 4 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1 and 3 at the same time, then 2, then 4, then 5, then 6.

## Tests

**`securityconfig`**

- `TestOnboardedIsFalseOnAFreshFile`, and a written true is read back.

**`graph`** (`schema.resolvers_test.go`)

- `TestOnboardingServesTheFlag`: `onboarding` answers false on a fresh file, and
  `onboardingFinish` writes it and answers true, with a sandbox and without one.

**Webview** (`onboarding-dialog.test.tsx`, `onboarding.test.tsx`, `app-dialogs.test.tsx`,
`sandbox-settings.test.tsx`, `permission-settings.test.tsx`)

- The launcher opens the dialog once when `finished` is false and `sandbox` has answered, not
  again on a re-render, not before `sandbox` answers, and not at all when `finished` is true or
  either query fails.
- Programs draws the list off its hook, with Include on a waiting entry calling
  `sandboxPathInclude`, and a Refresh PATH that answers calling `sandboxExecutablesProbe`.
- Executables: shown with an executable not probed and none running, the step starts
  `sandboxExecutablesProbe` once, and a second visit starts none; shown with every executable
  probed, or with a probe running, it starts none; a report that was unprobed when the dialog
  opened and probed by the time the step is shown starts none; shown before the watch's first
  frame, it waits for it and then decides; the spinner shows while `probing`; the resolved
  binaries draw; the `kubectl` alert draws when it was probed and not resolved, and not before
  it was probed.
- Permissions draws the picker with the settings' default mode and each context's mode, and a
  context's select calls `permissionModeSet`.
- Next from Executables, and Back, call nothing (Next onto Executables may start the step's
  probe, above); Finish calls `onboardingFinish` then closes, and a refused one keeps the
  dialog open with its error; closing by Escape calls nothing.
- With `sandbox.available` false the one screen draws, with the reason off Windows and without
  it on Windows, and OK writes the flag.
- *Set up the sandbox again* opens the dialog and writes nothing.
- The split sections' existing tests pass unchanged.

## Security

Nothing widens by default. Every change in the flow is a click on one entry or one context —
a Programs Include, a mode — each written by the mutation its step already guards; the default
mode is Ask, which is step 3B's default with or without the flow. The probe is step 6A's, with
no cluster and no network. The flow adds no mutation but the flag, and the flag gates nothing:
a machine that never finishes onboarding runs the sandbox as the earlier steps left it.

The residual is a user who includes every waiting folder without reading it; each entry draws
the folder and what it resolved to, and Include sends the target drawn, which is the guard step
3A gives every Include.

No security record. `security-model.md` gains one line under the sandbox's rows: the onboarding
flow writes only through the mutations its Settings sections already use, and a flag that gates
nothing.

## When it lands

- **`security-model.md`**: the line above.
- **`sidecar/CLAUDE.md`**: `Onboarded` and the wire.
- **Root `CLAUDE.md`**, *Routing & layout* and the Settings dialog: the `onboarding` dialog, the
  launcher in `AppLayout`, the shared list components, `usePermissionSettings`, and
  *Set up the sandbox again*.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on macOS with a fresh data directory: read the dialog open on the
first paint of the app, the Programs list with any home folder waiting, the Executables step
showing the launch probe's report (or probing, if it is still running) and naming which
`kubectl` it found, and the Permissions step with Ask selected and each context in it; press
Finish, restart, and read no dialog; open Settings and press *Set up the sandbox again*. Rename
`kubectl` off your `PATH`, press Refresh PATH, and read the alert. On Windows, or on Linux with
`kernel.unprivileged_userns_clone=0`, read the one screen and its reason.
