# The omnibox opens the cluster picker

Answers questions 1, 4, 5, 6 and 7 of #505, and the switching half of question 2. Read
`src/CLAUDE.md` (*The app bar*, *the context omnibox*, *Memory*) and the root `CLAUDE.md`'s
delta-watch rules before the first task.

## What it builds

The omnibox is the address bar of the cluster surface, and the cluster is the address. Today it
holds three controls: a server button that opens the Clusters dialog, a small centred dropdown
of kube-contexts that switches the window's cluster, and the memory button. The dropdown shows
no health; the dialog cannot switch. Its status dot is always green.

After this spec the omnibox works like Safari's address bar. A click anywhere on it outside the
memory button's box opens **the cluster picker**, a popover hanging centred under the omnibox at
the omnibox's width. The picker lists the kubeconfig's contexts, each with the real health
verdict the Clusters dialog computes, and a click on a row switches the window's cluster the way
the dropdown does today. A filter box narrows a long list as the user types. The omnibox's dot
carries the window's cluster's verdict. The server button goes away as a button; the Clusters
dialog is reached from a *Manage clusters…* link at the bottom of the picker and is otherwise
untouched.

### Answers recorded on #505

1. **One surface or two.** One picker, opened by the omnibox body. The dialog stays, reached
   from the picker's footer, until questions 2 and 3 settle what moves into the picker.
2. **The list** (switching half). A row is a context from the kubeconfig: its dot, its name,
   a one-line status summary, and the TLS badge when it applies. Click switches. Orphaned caches
   do not appear; they have no context to switch to and stay in the dialog.
4. **The status dot.** The overall tone of the window's cluster's record: the worst of its
   connection and sync tones, as the dialog's leading circle shows. Muted while no record
   matches the context.
5. **Size and shape.** Anchored and centred on the omnibox like `MemoryPopover`, at least 480px
   wide. Not resizable: the list scrolls within the room below the omnibox.
6. **Search.** A filter box at the top of the picker, focused on open, from six contexts up.
7. **Empty and loading.** The omnibox text stays *Connecting…* and *No kubeconfig*. The picker
   opens regardless and says the same in its body, with the footer link still present, so a
   kubeconfig-less user can still reach the dialog's orphaned caches.

## Non-goals

Do not build any of these. They wait on questions 2 and 3 of #505.

- Any cluster action in the picker: enable, disable, pause, resume, clear cache, remove.
- Connection or sync detail in the picker, inline or in a side pane.
- Orphaned caches in the picker.
- Resizing the picker, or persisting a size for it. Nothing new goes in
  `docs/persisted-data.md`.
- Any change to the Clusters dialog beyond importing the health module task 2 moves out of it.
- Typing into the omnibox itself. The omnibox is a button; the filter box is in the picker.
- Any change to `setContext`, to `tlsUnverifiedReason`, or to the clusters stream.

## Design

### Structure

```
┌ omnibox (pill, ring, drag opt-out) ───────────────────────────────┐
│ [ trigger: 🖥  ● context-name  [Unverified TLS]               ] [🧠] │
└───────────────────────────────────────────────────────────────────┘
```

- The pill is a `div`, as today, and keeps `data-testid="context-omnibox"` and
  `data-tauri-drag-region="false"`. It loses its padding: a click on padding would land on the
  `div`, not the trigger. The trigger carries the left and vertical padding inside itself and
  the memory button sits in a `pr-1` box, so the pill still measures 44px by content and looks
  as it does today.
- **The trigger** is one `button`, `flex-1 min-w-0`, the pill's full height, filling everything
  left of the memory button. Its `aria-label` is `Cluster: <name>`, or `Cluster: connecting`
  and `Cluster: no kubeconfig` in those states. Inside it, from the left: the server glyph in a
  `size-8` box, then a centred group of the dot, the truncating name and the TLS badge. The
  glyph's box and the memory button are the same size, so the group centres on the pill. No
  chevron.
- **The memory button** is the trigger's sibling, not its child. A button inside a button is
  invalid HTML and breaks keyboard focus. The pill stays the memory popover's anchor.
- The TLS badge stays inside the trigger and stays derived from `tlsUnverifiedReason` alone.

### The picker

- Hangs from the pill through the same positioner the memory popover uses: `anchor` the pill,
  `align="center"`, `side="bottom"`, `sideOffset={8}`, `collisionAvoidance={{ side: 'none' }}`,
  `z-50`. Width `max(var(--anchor-width), 480px)`, `max-w-[calc(100vw-16px)]`. Height
  `min(content, var(--available-height))`, the list scrolling inside.
- Header: *Clusters* and a close button, the memory popover's shape.
- Filter: `Input` from `components/widgets/text-entry.tsx` as code text (no `prose`), with
  `placeholder="Filter clusters"` and `aria-label="Filter clusters"`, rendered when
  `contexts.length >= 6`. It takes focus on open through the `Popup`'s `initialFocus`, given a
  function that returns the input, or `true` when there is none so the popup's default focus
  applies; a bare `autoFocus` loses to the popup's own focus handling. It matches on the context
  name,
  case-insensitive substring. `Enter` picks the first visible row. An empty match reads *No
  cluster matches.*
- Keyboard: `Tab` moves from the filter through the rows to the footer. No arrow-key handling.
- Rows: one `button` per visible context, full width. From the left: the dot, then the name
  over a one-line summary in muted small text, then the TLS badge when it applies, then a
  `Check` icon on the window's cluster (`aria-current="true"`). Name and summary are cluster
  data: drawn as text, never markup.
- The window's cluster leads the list; the rest keep the kubeconfig's order.
- Footer: a `Manage clusters…` link-styled button that closes the picker and calls
  `openDialog('clusters')`.
- States, in the body in place of the list: a spinner and *Connecting…* while `phase` is
  `connecting` and there are no contexts; *No kubeconfig* once connected with none. The footer
  stays.

### Health

A context's record is the cluster whose `spec.source.kubeconfig.context` equals its name, as
`MemoryPopoverContent` finds it. The row's tone and summary are the dialog's `overallStatus`
of `connectionStatus` and `statusOf`, computed for the `active` group. A context with no record
is `{ tone: 'muted', summary: 'Not reached' }`, and so is every context while
`useClusters().clusters` is still `null`: the snapshot's `Bookmark` has not landed, and nothing
draws a verdict before it. The pill's dot is the same value for the window's cluster, with the
summary as its `title`.

The verdict does not read `spec.enabled`: a disabled cluster's colour comes from whatever
conditions it last reported, in the picker as in the dialog. Changing that is a change to the
dialog's verdict, out of this spec's scope.

### Switching

A row click runs exactly what the dropdown's `select` runs today: `clearView()`, then navigate
to `/chat` if the window is on `/chat/*` else `.`, with `kubeContext: name` and
`chat: undefined`. Then the picker closes. Picking the window's own cluster closes the picker
and navigates nowhere.

## On acceptance

When the spec PR is accepted, post the *Answers recorded on #505* block as a comment on #505 and
file one task issue per remaining question (2's content and grouping, 3), from
`.github/ISSUE_TEMPLATE/task.md`. This is the maintainer's step, not an implementation task.

## Tasks

Do them in order. Each is one commit. Run `make test-changed` after each; the full checks are in
`docs/specs/README.md` under *Verification commands*. Paths are from the repository root.

### 1. One positioner for popovers hung from the omnibox

New `src/components/widgets/omnibox-popover.tsx` exporting `OmniboxPopover`, taking
`anchorRef`, `children` (the trigger element, passed to `PopoverTrigger`'s `render`) and
`popup` (the `Popup` element). It holds the `Popover`, `PopoverTrigger`, `Portal` and
`Positioner` now at the bottom of `src/components/widgets/memory/popover.tsx`, with the same
props and the comment on why it never flips.

- `MemoryPopover` becomes `OmniboxPopover` with `popup={<MemoryPopup />}`. Its behaviour and
  `memory-popup` test id do not change.
- Test `omnibox-popover.test.tsx`: opens on the trigger's click, renders the popup in a
  portal, closes on Escape. `memory/popover.test.tsx` keeps passing unchanged.
- `src/CLAUDE.md`, *Memory*: the popover hangs through `OmniboxPopover`.

### 2. The health verdict moves out of the dialog

New `src/lib/cluster-health.ts`. Move from `cluster-sync-panel.tsx`, unchanged in behaviour:
`Tone`, `Group`, `TONE`, `overallTone`, `isPending`, `displayName`, `findCondition`,
`connectionStatus`, `statusOf`, `Status`, `overallStatus`. Export each the panel imports. Add:

```ts
export type Health = { tone: Tone; summary: string };
export const UNREACHED: Health = { tone: 'muted', summary: 'Not reached' };
// The verdict for a kubeconfig context: its record's, or `UNREACHED` while none matches.
export function contextHealth(record: Cluster | undefined): Health
```

`contextHealth` returns `overallStatus(connectionStatus(record, 'active'), statusOf(record, 'active'))`
for a record and `UNREACHED` for none. `overallStatus`'s return type becomes `Health`.

- Move the `overallTone` `describe` from `cluster-sync-panel.test.tsx` to
  `cluster-health.test.ts`. Add cases for `contextHealth`: no record is muted and *Not reached*;
  a connected, watching record is `ok` with *Active · Synced*; a disconnected one is `error`
  with *Disconnected · Stalled*. `contextHealth` is pure, so build each `Cluster` as a literal
  in the test file; the panel's fixtures push GraphQL frames and do not apply here.
- The panel's own tests keep passing unchanged.

### 3. The picker

New `src/components/widgets/cluster-picker/` with `popover.tsx`, `list.tsx`, `index.ts`
exporting `ClusterPicker` alone, and a test beside each.

`list.tsx` exports `ClusterList({ contexts, current, records, onSelect, filterRef })`: the
filter, the rows and the empty-match text, as *Design* states. `filterRef` is the popover's
`RefObject<HTMLInputElement | null>`, attached to the filter when it renders; the popover passes
`initialFocus={() => filterRef.current ?? true}` to the `Popup`. `useActiveKubeContext` stays
out of the list so the test feeds it props. The `Check` icon is lucide's.

`popover.tsx` exports `ClusterPicker({ anchorRef, children })`: `OmniboxPopover` whose popup
holds the header, `ClusterList` or the connecting/empty state, and the footer. It reads
`useActiveKubeContext`, `useClusters`, `useLogsView`, `useLocation`, `useNavigate` and
`useDialog`. The `select` function and its comment move here from `context-omnibox.tsx`.
Closing after a pick: hold `open` in state and pass it to `OmniboxPopover`; add `open` and
`onOpenChange` props to `OmniboxPopover` for this, optional, so the memory popover is untouched.

Tests, `list.test.tsx`:

- renders a row per context, the current one first and marked current;
- a row shows its record's tone on the dot (`data-tone`) and the summary text;
- a context with no record reads *Not reached*;
- shows the TLS badge on a row whose cluster entry skips verification, and not otherwise;
- shows the filter from six contexts and not at five, pinned as code text with
  `expectCodeText` from `@/test-utils`;
- the filter narrows the rows, case-insensitively, and shows *No cluster matches.* on none;
- `Enter` in the filter picks the first visible row;
- a row click calls `onSelect` with its name.

Tests, `popover.test.tsx`, with the mocks `context-omnibox.test.tsx` uses today:

- a row click switches cluster, clearing the panel's chat, clearing the log view, and leaving
  an open chat for `/chat` (move the three switching tests from `context-omnibox.test.tsx`
  here, as is);
- picking the current cluster closes and does not navigate;
- *Manage clusters…* calls `openDialog('clusters')` and closes;
- says *Connecting…* with no contexts while connecting, and *No kubeconfig* once connected,
  with the footer present in both;
- the filter has focus after opening, with six contexts served.

The omnibox test's mocks serve `useClusters` as `{ clusters: [] }` and contexts without a
`clusterEntry`. The dot and per-row badge tests need both to vary, so the test's mocks take a
value per test. That is a test change only; production reads the real hooks.

### 4. The omnibox becomes the trigger

`src/components/widgets/context-omnibox.tsx`:

- Delete `Segment`, `ClusterDot`'s placeholder body and the server `Button`. Delete the
  `DropdownMenu` imports.
- `ClusterSegment` becomes `ClusterTrigger`: the button *Design* describes, wrapped by
  `ClusterPicker` with the pill as `anchorRef`. It renders `UnverifiedTLSBadge` and a
  `ClusterDot` that takes `tone` and `title` from `contextHealth(record)`, where `record` is
  found in `useClusters().clusters` by context name. The connecting and empty texts stay inside
  the trigger, replacing the name group.
- `ContextOmnibox` lays out `ClusterTrigger` then `MemoryButton`; both take `containerRef`.
- The file's header comment describes the omnibox as the picker's trigger.

`context-omnibox.test.tsx`:

- *reads as one control* and the drag opt-out tests stay.
- *opens the clusters dialog from the icon* becomes *opens the cluster picker from anywhere on
  the pill*: clicking the name, clicking the glyph and clicking the badge each open a popup with
  the *Clusters* heading. A click on the brain opens the memory popup and not the picker.
- *switches cluster from the quick dropdown…* and the two switching tests after it leave for
  task 3's `popover.test.tsx`; here one test pins that the picker's row click reaches the
  navigate mock, through the real `ClusterPicker`.
- The dot test: `data-tone` is `ok` for a connected, watching record; `error` for a
  disconnected one; `muted` with no record. Its `title` is the summary.
- The TLS and connecting tests stay, reading the trigger. *says there is no kubeconfig once
  connected with nothing in it* is rewritten: the trigger exists, is named `Cluster: no
  kubeconfig`, reads *No kubeconfig*, and opens the picker.
- `app-bar.test.tsx` and `app-layout.test.tsx` mock the omnibox and need no change.

### 5. Docs and the issue

- `src/CLAUDE.md`, *the context omnibox*: the omnibox is one trigger plus the memory button;
  a click on it opens `ClusterPicker` (`components/widgets/cluster-picker/`), listing the
  kubeconfig's contexts with `cluster-health.ts`'s verdict, filterable from six; the dot is the
  window's cluster's verdict; the Clusters dialog opens from the picker's footer. Keep the TLS
  badge sentence. Update the *Chat* sentence that names the omnibox's switch to name the
  picker.
- `src/CLAUDE.md`, *Memory*: `MemoryPopover` hangs through `OmniboxPopover`, shared with the
  picker.

## Done when

- A click on any part of the omnibox outside the memory button's box opens the picker; the
  memory button opens the memory popover and nothing else.
- The picker lists the kubeconfig's contexts with a real dot and summary each, the window's
  first; a row click switches the way the dropdown did, pinned by the moved tests; the filter
  appears from six contexts and narrows them.
- The omnibox's dot reads the window's cluster's verdict, muted without a record.
- The Clusters dialog opens from *Manage clusters…* and from nowhere else in the omnibox.
- Dialog tests pass unchanged apart from the moved `overallTone` cases; the memory popover's
  tests pass unchanged.
- No new `localStorage` key.
- `make test-changed`, `make lint`, `make vet` pass. Coverage gates are not lowered.
- A check in the running app that pressing anywhere on the pill outside the memory button's
  box opens the picker, and that no press on the pill drags the window.

## Handoff

Report changed files, the test names added or moved, the verification commands run with their
results, and anything left failing. If a step cannot be done as written, stop and say why rather
than build around it; in particular, do not add anything listed under *Non-goals*.

## When it lands

This spec is deleted in the PR that lands it. `src/CLAUDE.md` is updated by task 5. Write an
ADR, *the omnibox is the cluster picker*, recording that the picker and the dialog's switching
role merged into one popover hung from the omnibox and that the dialog keeps its actions until
#505's remaining questions are answered; link it from the omnibox paragraph of `src/CLAUDE.md`.
