---
title: Select and copy
scope: frontend
status: Built
---

# Select and copy

**Needs:** step 4A, whose columns a cell selection is over. **Unblocks:** rungs 3 and 5 of the
log views ladder, which read the selection.

TypeScript paths are under `src/`. Kubetail paths are under its
`dashboard-ui/src/pages/console/`.

## In short

The user can select rows and cells in the viewer and copy them. Click a row's position cell to
select it, drag for a range, shift-click to extend, `Cmd/Ctrl`-click to add or remove one.
Click a cell for one cell, drag or shift-click for a block, `Cmd/Ctrl`-click to add or remove
one, and the arrow keys move a selected cell. `Cmd/Ctrl+C` copies what is selected as
tab-separated text. A right-click on a cell offers to copy it. The selected lines go into the
viewer's `LogViewport`, in order, so a later rung can attach them to a question.

**This step is a port of kubetail's selection.** `selection.ts`, `selection-overlay.tsx`,
`context-menu.tsx`, and the band and edge drawing in `main.tsx` copy across with their logic
unchanged. They change in only four ways: jotai becomes a small store per hook, kubetail's virtualizer
becomes the core's `CoreRows`, the keyboard listens on the scroller instead of `document`, and
text-select mode is left out. Every other place this step differs from kubetail is named where
it happens, with its reason.

## What is not in this step

- **No text-select mode.** In kubetail, a click on a selected cell switches it to native text
  selection, and a modifier key turns the text cursor off again. That needs `isTextSelectMode`,
  `isCursorText`, `scheduleCursorText`, the `data-mod-key` attribute and the native-selection
  branch of every handler. Leaving it out is what lets every cell stay `select-none`. It can
  come later if someone misses it; copying the cell gets the text.
- **No auto-scroll while dragging.** Kubetail's drag stops at the scroller's edge, and so does
  this one. To grow a selection past the screen, scroll and shift-click.
- **No *Copy message (ANSI)*, and no timestamp *Copy as…* list.** The sidecar strips escape
  sequences, so there is no ANSI to copy. Kstack has no timestamp-format setting, so the list
  has one other format, and it is a plain item (§4).
- **No *Ask about this*.** Rung 5 of the ladder adds it over this step's selection.
- **No cap on the selection.** The viewport holds what was selected; the rung that sends it
  caps what it sends.

## Design

### 1. What a selection is

Kubetail's state (`state.ts`), in a small store the hook holds instead of jotai atoms, so
each handler reads what the last one wrote in the same event, as kubetail's `store.get` does:

- **Rows**: `selectedKeys: Set<number>`, and `lastClickedKey`, the key shift-click extends from.
- **Cells**: `selectedCells: Map<number, Set<Column>>`, and `anchorCell`, which is always a
  selected cell, or null when nothing is selected. It is the arrow keys' cursor.

Selecting one kind clears the other. The keys are the core's, so a selection survives a page
loading above it or a follow appending below: a prepend does not renumber, and the window
keeps every line it is given.

**A landing clears it.** Each landing — `jumpTo`, Start, End, *Jump to a time*, `retry` —
is a fresh window whose keys start again at 0, so a key held from the last landing would name
a different line. Kubetail resets its selection when `isLoading` goes up. Here the hook lives
in a component that the core's render function draws, under the landing's key (§2), so a new
landing mounts it fresh and nothing has to reset it.

### 2. Where it lives

`src/components/widgets/log-viewer/selection.ts`, from kubetail's `selection.ts`. It sits beside
the viewer, not in the library, because it knows about columns and cells. It holds the pure
functions, each copied as it is apart from types:

- `computeSelection` (a row click with its modifiers), `computeCellRange` (the rectangle from
  the anchor to a cell, sharing one `Set` across its rows so the row memo skips rows whose
  selection did not change), `nextSelectedCellInReadingOrder`, `hasMultipleSelectedCells`, and
  `getCellInArrowDirection`.
- `getPlainAttribute(line, col)`, which is a cell's text, `formatRowsForCopy` and
  `formatCellsForCopy`.

It also holds the hooks:

- `useSelectionState`: the state in §1 and `clearSelection`.
- `useRowDrag`, `useCellDrag`, over `startDocumentDrag`. These copy kubetail's mouse down,
  modifier clicks and drags. A right-click (or `Ctrl`-click on macOS) on a cell keeps the
  selection for the context menu. The drag listeners go on `document`: they are added on mouse
  down and removed on mouse up, so two viewers can never be dragging at once. A drag finds its
  target with `elementFromPoint` and `closest('[data-row-key]')` / `closest('[data-col]')`.
- `useSelectionEdges`: the top and bottom keys of each run of selected rows.
- `useSelectionKeyboard`, the one change in shape: it listens for `keydown` on the viewer's
  scroller, not on `document`, so a key reaches only the viewer that has focus. The scroller is
  already `tabIndex={0}`, and every cell is inside it. Everything else is kubetail's: Escape
  clears the selection unless something already handled it; `Cmd/Ctrl+C` copies the cells,
  else the rows; an arrow with no modifier moves the anchor cell one cell and selects only that
  cell. An arrow with no cell selected is not handled, so it scrolls.
- `useSelection(rows, scroller, columns, onLines)`, which puts all of these together.

**Where it is called.** `Rows` in `log-viewer.tsx` calls `useSelection`. `Rows` is what the
core's render function returns, so it gets `rows: CoreRows` directly and is remounted for each
landing (§1). `CoreRows` already has what kubetail reads off its virtualizer: `lineOfKey` is
`getRecord`, `keyAt` is `getKeyAtIndex`, and `indexOfKey` is `getIndexOfKey`. The keyboard
handler reads the latest `rows` through `useEffectEvent`. It does not write a ref during render
the way kubetail's `virtualizerRef.current = virtualizer` does, because the compiler's lint
refuses that. The library changes in one place, the pin (§3).

### 3. Drawing it

This is kubetail's drawing, copied from `main.tsx` and `selection-overlay.tsx`.

- **Rows.** `computeSelectionBands` paints one band per run of selected rows behind the rows,
  across the full width, in `--selection-band`, so neighbouring rows leave no seam. Each row
  also gets `selectionBoxShadow`: a 1px inset `--ring` line at the top of a run's first row and
  at the bottom of its last. The rows have no background of their own (4A draws no stripes), so
  there is no cell background to clear.
- **Cells.** A row with selected cells draws `SelectionOverlay`
  (`log-viewer/selection-overlay.tsx`). It draws one rect per selected cell, with a border on
  each edge that does not touch another selected cell, worked out from the rows above and
  below. The dot counts as connecting its two neighbours. When more than one cell is selected,
  the anchor cell is drawn with all four borders and a corner mark.
- **The overlay's geometry.** The overlay reads the position column's width from the widths
  map under `Pos`, where kubetail reads a fixed 48px. **The message's rect runs to the row's
  right edge** (`right: 0`), where kubetail computes `rowWidth − the other columns`. The message
  is always the last column, and while wrap is on, `rowWidth` is not measured, so kubetail's
  arithmetic would draw the ring the wrong width.
- **The row** gains `data-row-key` and `aria-selected`. The position cell gains
  `role="button"`, `tabIndex={0}`, a mouse down that selects its row, and an Enter or Space
  that acts like that mouse down. **That key handler runs in the capture phase and stops
  propagation.** The scroller's intent reader treats Space as a scroll down, and at the bottom
  it would pin. React runs capture handlers before the scroller's own listener, and bubble
  handlers after it. Each cell but the dot
  gains `role="gridcell"`, `tabIndex={0}` and the same Enter and Space handling. The dot takes
  no selection, no context menu, and nothing of it is copied.
- **A click unpins.** The core's intent reader (`lib/log-viewer/core.tsx`) calls the window's
  `scrolledUp` on a pointer down on the scroller, as kubetail's auto-scroll turns off on
  `mousedown`. A row clicked while pinned would otherwise scroll away as lines arrive, and a
  drag would shift under the pointer. Scrolling back to the bottom pins again, as before.
- **The row memo** gains kubetail's selection props: `selected`, `selectionTop`,
  `selectionBottom`, the row's selected columns and those of the rows above and below, and the
  anchor's column. All of them are primitives or shared `Set`s, so `memo`'s shallow compare
  still skips rows that did not change.

`--selection-band` is added to `index.css` with kubetail's values:
`color-mix(in srgb, var(--chart-4) 30%, transparent)` in light and `50%` in dark. The anchor
mark uses `bg-primary` in place of kubetail's `bg-blue-500`, so it stays in the theme.

### 4. Copying

- **What a cell's text is.** `getPlainAttribute` returns the cell's text as the cell draws it:
  the timestamp through `lineTime` (moved to `log-viewer/line-time.ts`, since the row now
  imports the selection), the pod, the container and the place fields as they are,
  and the message as it is. It returns the raw string, not the `VisibleText` spelling.
- **`Cmd/Ctrl+C`.** This is kubetail's handler. For cells, `formatCellsForCopy` writes, for
  each row in key order, that row's selected cells in column order, joined by tabs. For rows,
  `formatRowsForCopy` writes, for each row, every visible column but the dot, joined by tabs.
  Lines are joined by `\n`. The text goes through `navigator.clipboard.writeText`. A rejection
  goes to `reportError` as source `render`, not as an unhandled rejection. With nothing
  selected, the handler does nothing.
- **The context menu.** `log-viewer/context-menu.tsx`, from kubetail's `CellContextMenu`, over
  `@kubetail/ui`'s `ContextMenu`. It is the same base-ui component, with
  `ContextMenuTrigger render={cell}`. Each cell but the dot is wrapped, and the menu is by
  column:
  - on a timestamp, *Copy timestamp* copies it as drawn, and *Copy as RFC 3339* copies it as
    the client gave it. Kubetail's *Copy as…* list is cut down to the one format kstack has
    besides the drawn one.
  - on a message, *Copy message*.
  - on any other cell, *Copy*.

### 5. The viewport

The viewport and its `selection` field are kstack's alone. `Rows` reports the selected lines,
in key order, through an `onSelectionChange(lines)` prop: a row selection's lines, or a cell
selection's rows' lines. `LogViewer` keeps the last list in a ref and puts it into each
viewport write. The store subscription writes the range and the selection with it, so neither
write drops the other. A landing mounts a fresh `Rows`, which reports an empty list. On
unmount the viewer writes `EMPTY_LOG_VIEWPORT`, as it does now.

## Decisions this step asks for

- **Port, don't rewrite.** Kubetail's selection has been in use for a long time and has a
  2,200-line test suite. Copying it keeps its edge cases: the anchor invariant, a right-click
  that keeps the selection, and the shared `Set`s the memo relies on. Each place this step
  differs from it is named above, with the reason.
- **The keyboard listens on the scroller.** The README's rule is that a viewer is
  self-contained, and a page can hold several viewers. A `document` listener would copy every
  viewer's selection at once.
- **The selection lives under the landing's key.** That gives kubetail's reset-on-load for
  free, and it removes the need for a ref written during render or a new method on the core's
  handle.
- **Copy what the cell draws, plus the client's timestamp on request.** A copied row reads like
  the row on screen, which is kubetail's rule. The timestamp as drawn leaves out today's date,
  so the menu offers the full one.
- **Rows, not cells, in the viewport.** The ladder attaches whole lines, and a block of cells
  says nothing that its lines do not.
- **The clipboard API, with nothing behind it.** Nothing in the app writes to the clipboard
  yet, so the first by-hand check (*Verification*) is a copy on each platform. If a webview
  refuses it, stop and ask. Tauri's clipboard plugin would be a new capability, and the fix
  must never be a hidden element the webview writes HTML into.

## Tasks

`selection.ts`, `selection-overlay.tsx` and `context-menu.tsx`, and their tests, carry both
copyright lines, per the README's *Attribution*. So does `log-viewer.tsx` and `row.tsx`, once
they carry kubetail's bands and edge shadow. The viewport write is kstack's alone.

1. **The selection.** `log-viewer/selection.ts` and `selection.test.tsx`, ported from
   kubetail's (§1, §2). **Built.**
2. **Drawing.** The bands in `log-viewer.tsx`; the row's attributes, edge shadow, handlers and
   memo props in `row.tsx`; `log-viewer/selection-overlay.tsx`; `--selection-band` in
   `index.css`; the pointer-down unpin in `lib/log-viewer/core.tsx` (§3). **Built.**
3. **Copying.** The keyboard copy in the hook; `log-viewer/context-menu.tsx` (§4). **Built.**
4. **The viewport.** `onSelectionChange` from `Rows` to the viewport write in `log-viewer.tsx`
   (§5). **Built.**
5. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Built.**

## Tests

TypeScript, beside the files they cover. Kubetail's tests port with their cases. The
jotai `Provider` and `createStore` become a `renderHook` over `useSelection`, the virtualizer
mock becomes a stub `CoreRows`, and keyboard events are sent to the scroller instead of
`document`. Cases that only exercise text-select mode or the cursor-text flag are dropped.

- `selection.test.tsx`: kubetail's `selection.test.tsx`. That covers `getPlainAttribute`,
  `formatRowsForCopy`, `formatCellsForCopy`, `computeSelection` and `computeCellRange`; the row
  and cell mouse down with each modifier; row and cell drags; the selection boundaries; a
  right-click keeping the selection; `resetSelection`; and the keyboard (Escape, copy, the
  arrows). Added: a keydown on another element leaves the selection alone, and a selection
  keeps its keys after a prepend.
- `selection-overlay.test.tsx`: kubetail's, with the message's rect running to the right edge.
- `context-menu.test.tsx`: kubetail's, minus the ANSI and *Copy as…* cases, plus
  *Copy as RFC 3339* copying `line.timestamp`.
- `log-viewer.test.tsx`: a selected run draws one band; `Cmd/Ctrl+C` writes the rows to a mocked
  clipboard; a refused write reaches the error bus; the viewport holds the selected lines in
  order and empties on Escape; a block of cells puts its rows' lines in the viewport; a jump
  clears the selection and the viewport's; with two viewers mounted, `Cmd/Ctrl+C` on the
  focused one copies its selection and leaves the other's alone; Space on a position cell at the
  bottom does not pin.
- `row.test.tsx`: `data-row-key`, `aria-selected`, and the position cell's Enter and Space.
- `lib/log-viewer/core.test.tsx`: a pointer down unpins and closes the follow before any scroll.

## Security

**Widens:** where cluster data can go. It can now reach the clipboard, by the user's own
keypress or click, as plain text. Nothing writes to the clipboard without one. There is no HTML
sink: the formatted text is a string handed to `writeText`, and the `cluster-data-is-text` lint
covers these files. The context menu's labels are fixed strings. No cluster text reaches a
`title` or an attribute; `data-row-key` is the core's number and `data-col` is a column name.

**Residual:** a copied line holds whatever the log held, a secret included, just as
`kubectl logs | pbcopy` would. A copy is the raw text, not the `VisibleText` spelling, so an
invisible character marked on screen is pasted as itself.

## When it lands

- Root `CLAUDE.md`, *Chat*: the viewer's paragraph gains selection, copy, the context menu, and
  what the viewport's `selection` holds.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while working.

By hand, on each platform, **starting with the clipboard**: select a row, press `Cmd/Ctrl+C`,
and paste it. Do this on macOS (WKWebView), Windows (WebView2) and Linux (WebKitGTK) before
building the rest. Then: select three rows, copy them, and see three tab-separated lines; drag
over a block of cells, copy it, and the block pastes with tabs; right-click a message, choose
*Copy message*, and paste it; scroll up so rows load above, and the selection stays on the same
lines; press End and the selection clears.
