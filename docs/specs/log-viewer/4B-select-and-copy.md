---
title: Select and copy
scope: frontend
status: Planned
---

# Select and copy

**Needs:** step 3. **Unblocks:** rungs 3 and 5 of the log views ladder, which read the
selection. Can be built beside step 4A.

TypeScript paths are under `src/`.

## In short

The user can select rows and cells in the viewer and copy them. Click a row's position cell to
select it, shift-click or drag for a range, shift and the arrow keys to grow it; click a cell
for one cell, drag for a block. `Cmd/Ctrl+C` copies what is selected as plain text. A
right-click offers *Copy message* and *Copy timestamp*. The selected lines go into the viewer's
`LogViewport`, in order, so a later rung can attach them to a question.

The selection logic comes from kubetail's `selection.ts`, a set of hooks over the core's rows
that copies across with little change. The overlay drawing comes with it.

## What is not in this step

- **No native text selection inside a cell.** Kubetail lets a modifier key switch to a text
  cursor inside a selected cell. It can come later if someone misses it; copying the cell gets
  the text.
- **No *Ask about this*.** Rung 5 of the ladder adds it over this step's selection.
- **No cap on the selection.** The viewport holds what was selected; the rung that sends it
  caps what it sends.

## Design

### 1. What a selection is

Two kinds, never both at once:

- **Rows**: a set of row keys, with the key last clicked as the anchor.
- **Cells**: a map of row key to the set of columns selected in it, forming a rectangle, with
  the cell first clicked as the anchor.

The keys are the core's, so a selection survives rows loading above it: key 0 is where the view
landed, and a prepend does not renumber. The window keeps every line it is given,
so a key in the selection always names a line the window holds.

### 2. Making one

From kubetail's `selection.ts`, copied into `src/components/widgets/log-viewer-selection.ts` —
beside the viewer, not in the library, since it knows columns and cells:

- `useSelectionState`: the two kinds and their anchors, as React state.
- `useRowDrag`, `useCellDrag`: mouse down on a position cell or a cell starts, moving extends,
  up ends; shift-click extends from the anchor; `Cmd/Ctrl`-click adds or removes a row. A drag
  that reaches the scroller's edge scrolls it, which the core reads as the user's (a pointer is
  down on the scroller, step 2 §6): a drag past the top unpins, as a scroll up does, and a drag
  past the bottom moves down, which never unpins.
- `useSelectionKeyboard`: shift and the arrows grow a selection from the anchor; Escape clears
  it; `Cmd/Ctrl+C` copies. The listener goes on the viewer's scroller, which is already
  `tabIndex={0}`, so the keys reach the viewer that has focus and a second viewer on the page is
  left alone. The modifier-key attribute kubetail toggles off `document` keydown goes on the
  scroller the same way. The drag listeners stay on `document`: they are installed on mouse
  down and removed on mouse up, so two viewers cannot both be dragging.
- `computeSelection`, `computeCellRange`: the pure functions behind them.
- `formatRowsForCopy`, `formatCellsForCopy`: rows as one line each — the timestamp, the pod and
  the message with a tab between — and cells as a tab-separated block, in column order.

`useSelection(rows)` wraps them over the core's `CoreRows`, and `LogViewer` calls it. The jotai
atoms kubetail keeps the state in become `useState` inside the hook; nothing outside the viewer
reads it but the viewport.

### 3. Drawing it

A selected row's cells lose their background, and one band per run of selected rows is painted
behind them, full width, so neighbours leave no seam (kubetail's `computeSelectionBands`). The
first and last rows of a run get an inset line at the top and the bottom. A selected cell draws
a ring, and a block of cells one ring around the block (`log-viewer-selection-overlay.tsx`,
from kubetail's `SelectionOverlay`).

The colour is `--selection-band`, added to `index.css` for light and dark.

The position cell — the row's offset from the landing, `-12`, `0`, `+3` — is the row's handle
and is drawn in every column set.

### 4. Copying

`Cmd/Ctrl+C` with a selection writes the formatted text through `navigator.clipboard.writeText`.
With none, the browser's own copy runs, which copies nothing, since cells are `select-none`.

The context menu (`log-viewer-context-menu.tsx`, from kubetail's `context-menu.tsx`, over
`@kubetail/ui`'s `ContextMenu`): on any cell, *Copy message* and *Copy timestamp*; on a cell of
another column, *Copy* for that cell's text. The timestamp is copied as the client gave it.

### 5. The viewport

The selected lines, in row order, go into the viewport's `selection` on every change, and an
empty list when the selection clears. A selection is rows or cells; for cells the lines of the
rows in the block are what the viewport gets, since a line is the unit the ladder reads.

## Decisions this step asks for

- **State inside the hook, not in a provider.** Only the viewer and the viewport read it, and
  the mount's key clears it with the view, which is right: a new view has nothing selected.
- **Rows, not cells, in the viewport.** The ladder attaches lines, and a cell block says nothing
  a line does not.
- **The clipboard API, with nothing behind it.** `navigator.clipboard.writeText` works in every
  webview kstack ships on, from a user's keypress or click. If one platform refuses it, the fix
  is a host command, never a hidden element the webview writes HTML into.
- **No text-select mode.** The copy actions cover the need, and the mode is the most involved
  piece of kubetail's selection code.

## Tasks

`log-viewer-selection.ts`, `log-viewer-selection-overlay.tsx` and `log-viewer-context-menu.tsx`
carry both copyright lines, per the README's *Attribution*; the viewport write and the band drawing in `log-viewer.tsx` are kstack's alone.

1. **The selection.** `src/components/widgets/log-viewer-selection.ts` and its test (§1, §2).
   **Planned.**
2. **Drawing.** The bands and the overlay in `log-viewer.tsx` and
   `log-viewer-selection-overlay.tsx` (§3); `--selection-band` in `index.css`. **Planned.**
3. **Copying.** The keyboard copy in the hook; `log-viewer-context-menu.tsx` (§4). **Planned.**
4. **The viewport.** `selection` written from `log-viewer.tsx` (§5). **Planned.**
5. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Planned.**

## Tests

TypeScript, beside the files they cover. Kubetail's `selection.test.tsx` is large and most of
it copies across.

- `log-viewer-selection.test.ts`: click, shift-click, drag and modifier-click on rows; click and
  drag on cells; the arrows grow from the anchor; Escape clears; a selection keeps its keys
  after a prepend; `formatRowsForCopy` and
  `formatCellsForCopy` spell what they should.
- `log-viewer.test.tsx`: a selected run draws one band; a selected cell draws the ring;
  `Cmd/Ctrl+C` writes the rows to a mocked clipboard; the viewport holds the selected lines in
  order and empties on Escape; a block of cells puts its rows' lines in the viewport; with two
  viewers mounted, `Cmd/Ctrl+C` on the focused one copies its selection and leaves the other's
  untouched; a drag past the bottom edge does not unpin.
- `log-viewer-context-menu.test.tsx`: *Copy message* writes the message, *Copy timestamp* the
  timestamp as given.

## Security

**Widens:** where cluster data can go — the clipboard, by the user's own keypress or click, as
plain text. Nothing writes to the clipboard without one. No HTML sink: the formatted text is a
string handed to `writeText`, and the `cluster-data-is-text` lint covers the files.

**Residual:** a copied line holds whatever the log held, including a secret, as `kubectl logs |
pbcopy` would.

## When it lands

- Root `CLAUDE.md`, *Chat*: the viewer's paragraph gains selection, copy, and what the
  viewport's `selection` holds.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while working.

By hand, on each platform: select three rows, press `Cmd/Ctrl+C`, paste into a text editor and
see three lines; drag over a block of cells and copy, and the block pastes with tabs; right-click
a message and *Copy message* pastes it; scroll up so rows load above, and the selection stays
on the same lines.
