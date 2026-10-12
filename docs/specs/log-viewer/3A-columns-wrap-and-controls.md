---
title: Columns, wrap and controls
scope: frontend
status: Planned
---

# Columns, wrap and controls

**Needs:** step 2. **Unblocks:** nothing; the sequence can land without it. Can be built beside
step 3B.

## In short

The viewer gets a header row and a small toolbar. The user picks which columns to see —
timestamp, pod, container, node, region, zone, OS, architecture, message — wraps long messages
or lets them run off the edge, jumps to the start or the end, and pauses the stream. A *Pinned*
indicator says whether the viewer is at the end, and a click pins it again.

The columns, the measured widths and the wrap come from kubetail's console page. The toolbar is
kstack's own, since kubetail's lives in a page header kstack does not have.

## What is not in this step

- **No selection, no copy.** Step 3B.
- **No colour dot** per source, no timezone setting, no download.
- **No namespace column.** A view can span namespaces, but the pod column's `title` names the
  namespace, which is enough until someone asks.

## Design

### 1. Columns

`Column` in `src/components/widgets/log-viewer-columns.ts`: `Timestamp`, `Pod`, `Container`,
`Node`, `Region`, `Zone`, `OS`, `Arch`, `Message`, in that order, `Message` always last and
always on. The default set is `Timestamp`, `Pod`, `Message`.

The set is the window's chrome: `kstack:log-viewer-columns` in localStorage through
`storageKey`, a sorted list of names. A name the code does not know is dropped on read.

A row is a CSS grid, `auto` for each column and `1fr` for the message, as kubetail lays it out.
Column widths are measured off the rendered cells (kubetail's `useMeasureWidths`, copied into
`src/components/widgets/log-viewer-widths.ts`, beside the viewer, since it is about columns and
the library knows none): each cell reports its width once, the widest wins, and
the grid's columns take the maximum, so a header and its rows line up and nothing jumps as rows
come and go. The widths reset when the view changes, which the mount's key does for free.

### 2. The header row

A fixed row above the scroller naming each visible column, in the same grid, scrolling sideways
with the rows (kubetail's `HeaderRow`, in `log-viewer-header.tsx`). It reserves the scrollbar's
width at its right edge, measured off the scroller, so the names stay over their columns.

### 3. Wrap

A toggle. Off, every row is one line tall and the scroller scrolls sideways. On, the message cell
wraps (`whitespace-pre-wrap`) and the core measures each row's height after it is drawn:
kubetail's `estimateRowHeight` — characters times an average width over the message column's
width — for the estimate, and `measureElement` for the truth. The message column's width is
tracked off the wrapper's width less the other columns' (kubetail's `useMessageContainerWidth`).
Flipping the toggle calls the core's `measure`.

The choice is `kstack:log-viewer-wrap`, chrome like the columns.

### 4. The toolbar

One row under the panel's header, before the column header:

- **Start** and **End**: `jumpTo({ kind: 'Head' })` and `jumpTo({ kind: 'Tail' })` on the core.
  End also pins.
- **Pinned** / **Not pinned**: a toggle button, `aria-pressed`, off the core's `pinnedToEnd`.
  Pressing it when off jumps to `Tail` and pins; pressing it when on unpins, which leaves the
  scroll where it is.
- **Playing** / **Paused**: a toggle button, `aria-pressed`, the core's `paused` prop. Paused,
  the viewer stops listening and stays where it is, pinned or not, so the user can read the last
  lines without them moving; playing again while pinned catches up from the last cursor. It is
  the one way to sit at the bottom without re-pinning, since reaching the bottom re-pins.
  Chrome, like the pin: not persisted, and a new view starts playing.
- **Wrap**: a toggle button, `aria-pressed`.
- **Columns**: a menu of checkboxes, one per column but `Message`.

Each is an icon button with a label for the screen reader. The buttons are `size="icon-sm"` as
the panel's close is.

### 5. Width

A viewer with four columns on wants more than 640px. `WIDTH.max` in `right-sidebar.tsx` becomes
1200 for chat mode. The dashboard's panel, which holds a transcript, keeps 640. The two maxima
sit beside the per-mode keys the widths already use.

## Decisions this step asks for

- **Columns and wrap are chrome, in localStorage.** They are the shape of the panel, not a
  setting the user picks in Settings, and every window should open the way the last was left —
  the rule the sidebar widths follow.
- **A toolbar of our own.** Kubetail's controls sit in its page header with its routing and
  settings. Four buttons and a menu are quicker to write than to untangle.
- **Pinned is a button, not just a badge.** The user unpins with the scroll wheel; re-pinning
  needs something to press, and End alone does not say the viewer will keep following.
- **Pause is separate from the pin.** Unpinning also stops the stream, so the two overlap, but
  only Pause lets the viewer rest at the bottom: scrolling there re-pins.
- **1200px for chat mode.** Enough for timestamp, pod, container, node and a message; the
  transcript beside it is still readable at a 1440px window.

## Tasks

`log-viewer-widths.ts`, `log-viewer-header.tsx` and `log-viewer-message-width.ts` are copied from
kubetail and open with both copyright lines, per the README's *Attribution*; the columns, the
toolbar and the width change are kstack's alone.

1. **Columns.** `src/components/widgets/log-viewer-columns.ts` (§1); `log-viewer-widths.ts`
   from kubetail's `useMeasureWidths`.
2. **The header row.** `src/components/widgets/log-viewer-header.tsx` (§2).
3. **Wrap.** The estimate and the measure in `log-viewer.tsx` (§3); the message width hook in
   `src/components/widgets/log-viewer-message-width.ts`.
4. **The toolbar.** `src/components/widgets/log-viewer-toolbar.tsx` (§4), mounted by
   `logs-view-panel.tsx` under its header.
5. **Width.** `right-sidebar.tsx` (§5).
6. **Docs.** Root `CLAUDE.md`, per *When it lands*.

## Tests

TypeScript, beside the files they cover.

- `log-viewer-columns.test.ts`: the default set; a stored set with an unknown name drops it;
  `Message` is always present and last.
- `log-viewer-header.test.tsx`: names the visible columns in order; reserves the scrollbar's
  width.
- `log-viewer-toolbar.test.tsx` also: Paused sets `paused` and the viewer stops at the
  bottom with lines no longer arriving; Playing again catches up.
- `log-viewer-toolbar.test.tsx`: Start and End call the core; End pins; Pinned reflects the
  core's state and toggles it; Wrap toggles and persists; the Columns menu adds and removes a
  column and persists.
- `log-viewer.test.tsx`: with wrap on, a long message's row is taller than one line and the
  next row starts below it; with wrap off, one line each; a hidden column is not drawn.
- `right-sidebar.test.tsx`: chat mode clamps at 1200, the dashboard at 640.

## Security

**Widens:** nothing. The column names and the header are the app's strings; every cell is still
cluster data through `VisibleText`. The stored column list is read back through a list of known
names, so a tampered localStorage value cannot name a column that does not exist.

## When it lands

- Root `CLAUDE.md`, *Chat*: the viewer's paragraph gains the columns, wrap and the toolbar;
  *Theming & window frame* gains the two storage keys.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while working.

By hand: open a view, turn on Node and Container, and the header and the rows line up as lines
arrive; turn on Wrap over a line longer than the panel, and it wraps with no overlap; scroll up,
press Pinned, and the viewer jumps to the end and keeps following; press Paused at the bottom,
and the last lines stay put while the pod writes; press Playing, and the missed lines arrive
and the end stays in view; open a second window, and it opens with the same columns and wrap.
