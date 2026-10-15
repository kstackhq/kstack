---
title: Columns, wrap and controls
scope: frontend
status: Built
---

# Columns, wrap and controls

**Needs:** step 3. **Unblocks:** step 4B, whose cell selection is over this step's columns.
The sequence can land without either.

TypeScript paths are under `src/`.

## In short

The viewer gets a header row, a position column and a small toolbar. Each row opens with its
offset from the line the view landed on — `-12`, `0`, `+3`. The user picks which other columns
to see — timestamp, a colour dot per container, pod, container, node, region, zone, OS,
architecture, message — wraps long messages or lets them run off the edge, jumps to the start
or the end, and pauses the stream. While the viewer is not pinned, a *Jump to now* button waits
at the bottom of the lines. The pin stays the user's hand: a scroll up unpins, a scroll to the
end pins.

The columns, the measured widths, the header and the wrap follow kubetail's console page
(`dashboard-ui/src/pages/console/main.tsx`). The toolbar's buttons follow its `header.tsx`;
*Jump to now* is kstack's own.

## What is not in this step

- **No selection, no copy.** Step 4B.
- **No pin button.** Scrolling pins and unpins, as in step 3.
- **No legend for the dots.** The Pod and Container columns name a stream; the dot only tells
  streams apart, and two containers can share a colour.
- No timezone setting, no download, no jump to a time.
- **No namespace column.** A view can span namespaces, but none of the fakes does, and a column
  can be added when the real client brings one.

## Design

### 1. Columns

**The position cell** opens every row, before the columns the user picks (from kubetail's
`RecordRow`). It is not a `Column`: it is not in the View menu, not stored, and drawn in every
column set. It shows the row's key, the core's — key 0 is the line the view landed on, older
lines count down and newer ones up — as a sign and a magnitude: `+3`, `-12`, and a bare `0`.
The sign is its own span at `text-[0.9rem]`, muted, so `+` and `-` read at a glance beside
digits drawn `tabular-nums` at `text-[0.65rem]`, `whitespace-nowrap`, centred, with `pr-1.5`.
Every position but the landed row's is muted. A landing — the mount, Start, End, *Jump to
now*, Try again — numbers again from its own key 0.

**The landed row's marker moves onto it.** Step 3 draws a left border on the whole row; here the
position cell of key 0 takes `border-l-2 border-primary font-extrabold` and `pl-[7px]`, which
keeps its digits where the other rows' sit, and the row loses its own border.

Its track is `minmax(48px, auto)`: 48px is kubetail's width, enough for a sign and five digits,
and the cell is measured like the columns are, under its own key in the widths map, so a longer
read widens it rather than spilling into the next cell. It is `select-none` and `cursor-default`.
It takes no role and no focus in this step: 4B makes it the row's handle.

`Column` in `src/components/widgets/log-viewer-columns.ts`: `Timestamp`, `Dot`, `Pod`,
`Container`, `Node`, `Region`, `Zone`, `OS`, `Arch`, `Message`, in that order, `Message` always
last and always on. The default set is `Timestamp`, `Dot`, `Message`. Each column's cell but the
dot reads one field of the line: the time as `lineTime` draws it, `stream.pod`,
`stream.container`, and the five of `place`. The pod cell is the pod alone; the container is its
own column, so step 3's `pod/container` and `readsManyContainers` go.

**The dot** is an 8px circle, `aria-hidden`, coloured by its container:
`dotColor(namespace, pod, container)` in `log-viewer-columns.ts` hashes
`namespace/pod/container` (FNV-1a, synchronous; none of the three can hold a `/`) to an index
into `DOT_PALETTE`, kubetail's 20 muted colours, which read on both
the light and the dark background. A previous instance shares its container's colour. The
colour is drawn as an inline `backgroundColor` from the palette, so no style or property name
is ever built from cluster text. Beside the circle the cell holds `pod/container` as `sr-only`
text through `VisibleText`, so the default set still names each line's stream to a screen
reader; visually hidden text takes no width. Its header cell has no visible name and an
`sr-only` *Source*, since *Container* names a column of its own; its width is the dot's, measured
like any other.

The set is the window's chrome: `kstack:log-viewer-columns` in localStorage through
`storageKey`, a JSON list of the visible names other than `Message`. A read drops a name the
code does not know and sorts the rest into column order, and a value that does not parse reads
as the default.
`useLogViewerColumns()` in the same file is the one reader and writer, and wraps both in
try/catch.

A row is a CSS grid: the position track, `auto` for each visible column, then `1fr` for the
message. Rows are positioned apart, so `auto` alone would size each row to its own cells; what
lines them up is that every cell carries `minWidth` set to its column's measured width, so each
row's tracks come out the same.

The widths are measured in `log-viewer-widths.ts`, from kubetail's `useMeasureWidths`:

- **A cell is measured by its content**, an `inline-block` span inside it, never by the cell
  itself: the cell's width is its track and its own `minWidth`, so measuring it reports the
  clamp or grows on every render. The column's width is the span's width plus the cell's
  horizontal padding, and the widest wins.
- **A row is measured by its `scrollWidth`**, the widest wins, and rows and the sizer carry it
  as `minWidth` while wrap is off. That is the sideways extent, in place of step 3's `w-max`.
- **The header's cells are measured the same way**, so a name wider than its values widens its
  column.
- Each element is measured once, and what changed is published once per animation frame. The
  map of widths is replaced only when a column's width moved, so rows memoized over it re-render
  only then.

The widths grow and never shrink, and they reset only when the view changes, which the mount's
key does for free. A landing inside one view — Start, End, Jump to now, Try again — keeps them,
so the columns do not move under a jump. Hiding a column keeps its width for when it returns.

### 2. The header row

A row above the scroller naming each visible column, in the same grid, in
`log-viewer-header.tsx` (from kubetail's `HeaderRow`). Its first cell, over the position
column, has no visible name and an `sr-only` *Position*. It sits outside the scroller, in a
wrapper with `overflow-x-hidden`, and its `scrollLeft` follows the scroller's on every scroll
event. Hidden overflow still takes a `scrollLeft` from code but none from the user, so a swipe
over the header cannot scroll it out of line with the rows. A spacer at its right edge is the
scroller's vertical scrollbar width, `offsetWidth - clientWidth` read through a
`ResizeObserver`, so the names stay over their columns at the far right.

**It needs the scroller, which the core owns.** `LogViewerCoreProps` gains
`scrollerRef?: Ref<HTMLDivElement>`, passed to each landing's scroller. Each landing mounts a
new scroller, so the viewer takes it as a callback ref into state, and the header and the
message width (§3) re-attach when it changes. The core still knows nothing of columns: the ref
is the DOM node and nothing else.

### 3. Wrap

A toggle. Off, every row is one line tall and the scroller scrolls sideways. On:

- A row's `minWidth` is `100%` instead of the measured row width, and it has no fixed height.
- The message cell is `whitespace-pre-wrap wrap-break-word`, with no `minWidth` and no width
  measurement.
- `estimateRowHeight` is `ceil(message.length × CHAR_WIDTH / messageWidth)` rows of
  `LOG_ROW_PX`, at least one. `CHAR_WIDTH` is the mono `text-xs` advance, read off a rendered
  row as `LOG_ROW_PX` is. `VisibleText` spells some characters wider than one, so the estimate
  runs low; the measurement corrects it.
- `measureElement` is `(el) => el.getBoundingClientRect().height`, passed to the core only while
  wrap is on. Each row then carries `ref={rows.measureElement}` and `data-index`.

`messageWidth` is the scroller's `clientWidth` less the position's and the other visible
columns' widths, at least 100px, read through a `ResizeObserver` once per frame
(`log-viewer-message-width.ts`, from kubetail's `useMessageContainerWidth`). The scroller is
watched whether or not wrap is on — one observer on one element — so the width is already
read when wrap is turned on.

Flipping the toggle calls the core's `measure` in the same handler, so the hold it performs
reads the reader's row off the layout before the flip, and keeps it, or the end.

The choice is `kstack:log-viewer-wrap`, a flag through `usePersistedFlag`.

### 4. The toolbar

One row at the top of the viewer, above its notices (*Not read*, *No new lines*, a page's
error) and the header row (`log-viewer-toolbar.tsx`). `LogViewer` draws it, since the core's
handle, its store and `paused` are the viewer's, and the viewer is keyed per call.

- **Start** and **End**: `jumpTo({ kind: 'Head', at: null })` and `jumpTo({ kind: 'Tail', at: null })`
  on the core. End pins, since a jump to `Tail` does.
- **Pause** / **Play**: one button whose icon and label say what pressing it does, the core's
  `paused` prop. Paused, the viewer stops listening and stays where it is, pinned or not, so the
  user can read the last lines without them moving; playing again while pinned reopens the
  follow from the last checkpoint. It is the one way to sit at the bottom without the stream
  moving, since reaching the bottom by hand pages and pins. Chrome, like the pin: not persisted,
  and a new view starts playing.
- **View**: a `Popover` of `Checkbox`es — one per column but `Message`, then *Wrap* under them.

Each is an icon button with a label for the screen reader, `size="icon-sm"` as the panel's close
is, and each is disabled while the core is `loading`.

**Jump to now** floats over the bottom of the scroller, centred, while the viewer has landed
(`!loading && !failed`) and is not pinned — after the user scrolls up, and from the start of a
`Head` or `At` landing, or a `Tail` one that did not pin: a small labelled button calling
`jumpTo({ kind: 'Tail', at: null })`, as End does. Unpinned, the follow is closed and the window
cannot know whether lines arrived, so the button says where the live end is rather than that
anything is new. It goes as the viewer pins again, by the button or by scrolling down to the
end.

### 5. Width

A viewer with four columns on wants more than 640px. `WIDTH.max` in `right-sidebar.tsx` becomes
1200 for chat mode; the dashboard's panel, which holds a transcript, keeps 640. The two maxima
sit beside the per-mode keys the widths already use.

**The panel gives way before the page does.** The `aside` loses `shrink-0` and takes
`min-width: WIDTH.min`, and `<main>` in `app-layout.tsx` takes `min-w-[min(480px,50%)]`, so on a
window too narrow for both the panel draws narrower than its stored width, down to its minimum.
The floor is half the content row on a small window, so the pill, the page and the panel's
minimum always fit. The stored width is left alone, and a wider window draws it again.

## Decisions this step asks for

- **Columns and wrap are chrome, in localStorage.** They are the shape of the panel, not a
  setting the user picks in Settings, and every window should open the way the last was left —
  the rule the sidebar widths follow.
- **A dot by default, not the pod.** Timestamp, dot and message fit the sidebar's default width
  and still tell one container's lines from another's; the names are a column away.
- **The dot is kubetail's palette, hashed, drawn inline.** A colour follows its container across
  views and windows with nothing stored. Kubetail sets a CSS variable per container on `<html>`;
  an inline colour needs no name derived from cluster text.
- **Pod and container are two columns.** One field per cell, as kubetail has it, and 4B copies a
  cell as the field it names.
- **The pin stays the scroll's.** Scrolling up unpins and scrolling to the end pins, as in step
  3; End and *Jump to now* re-pin without a scroll. A button for it would be a second way to say
  the same thing.
- **Widths are kept across landings.** Kubetail resets them on every load; keeping them means a
  jump does not move the columns.
- **The core hands out its scroller.** The header and the wrap need its width and its sideways
  scroll; a ref is the smallest thing that gives them both.
- **Jump to now beside the lines, not only in the toolbar.** Catching up by scrolling reads a
  page at a time, which after a long read is slow; the way back to the live end sits where the
  reader's eyes are. It names no count, since a closed follow has none to give.
- **Pause is separate from the pin.** Unpinning also stops the stream, so the two overlap, but
  only Pause lets the viewer rest at the bottom.
- **1200px for chat mode, and the panel yields to the page.** Enough for timestamp, pod,
  container, node and a message; the page keeps 480px, or half the row on a small window,
  whatever the panel was dragged to.

## Tasks

`log-viewer-widths.ts`, `log-viewer-header.tsx` and `log-viewer-message-width.ts` carry both
copyright lines, per the README's *Attribution*, and so does `log-viewer-columns.ts`, which holds
kubetail's dot palette; the toolbar, *Jump to now*, `scrollerRef` and the width change are
kstack's alone.

1. **`scrollerRef`.** `LogViewerCoreProps.scrollerRef` in `src/lib/log-viewer/core.tsx`, its
   test, and a line in the library's `README.md`. **Done.**
2. **Columns.** `src/components/widgets/log-viewer-columns.ts` (§1); `log-viewer-widths.ts`;
   `LogViewerRow` takes the row's key in place of `landed`, draws the position cell and the
   visible columns in the grid, and moves the landed marker onto the position cell.
   **Done.**
3. **The header row.** `src/components/widgets/log-viewer-header.tsx` (§2). **Done.**
4. **Wrap.** The estimate and the measure in `log-viewer.tsx` (§3);
   `log-viewer-message-width.ts`. **Done.**
5. **The toolbar.** `src/components/widgets/log-viewer-toolbar.tsx` (§4), drawn by
   `log-viewer.tsx`; *Jump to now* in `log-viewer.tsx`. **Done.**
6. **Width.** `right-sidebar.tsx` and `app-layout.tsx` (§5). **Done.**
7. **Docs.** Root `CLAUDE.md`, per *When it lands*. **Done.**

## Tests

TypeScript, beside the files they cover. jsdom has no layout, so a test that needs a width or a
height stubs it.

- `core.test.tsx`: `scrollerRef` receives the scroller, and a new one after `jumpTo`.
- `log-viewer-columns.test.ts`: the default set is `Timestamp`, `Dot`, `Message`; `dotColor` is
  stable for one container, ignores `previous`, and always answers a palette colour; a stored
  set with an unknown name drops it; a stored set out of order reads in column order; a value
  that does not parse reads as the default; `Message` is always present and last.
- `log-viewer-widths.test.ts`: a column takes its widest cell plus padding; a narrower cell
  later leaves it; an unchanged width keeps the map's identity; the position cell's width is at
  least 48px and grows with a wider key.
- `log-viewer-row.test.tsx`: the position cell comes first and draws `+3`, `-12` and `0` for
  keys 3, -12 and 0; the landed row's position cell carries the marker and the row does not;
  the cell is drawn with every column set.
- `log-viewer-header.test.tsx`: names the position column, then the visible columns in order;
  follows the scroller's `scrollLeft`; is `overflow-x-hidden`; reserves the scrollbar's width.
- `log-viewer-toolbar.test.tsx`: Start and End call the core; End pins; Pause sets `paused` and
  the viewer stops at the bottom with lines no longer arriving; Play again catches up; every
  button is disabled while loading; the View popover adds and removes a column and toggles Wrap,
  and both persist; the popover lists no position column.
- `log-viewer.test.tsx`: *Jump to now* is absent while pinned and before the landing, shows once
  the user scrolls up and from the start of a `Head` landing, lands at the end and pins when
  pressed, and goes when scrolling reaches the end; with wrap on and `measureElement` answering
  a stubbed height, a long message's row is taller than one line and the next row starts below
  it; with wrap off, one line each; a hidden column is not drawn; the pod and the container are
  separate cells; the dot's circle is hidden from the screen reader and its cell names
  `pod/container` to it; a dot's colour is `dotColor`'s for its container.
- `right-sidebar.test.tsx`: chat mode clamps at 1200, the dashboard at 640.

## Security

**Widens:** nothing. The column names and the header are the app's strings; every cell is still
cluster data through `VisibleText`, the dot's screen-reader text included. The dot's colour is
picked from the app's palette by a hash of the container's name, and no CSS name is built from
it. The stored column list is read back through a list of known names, so a tampered
localStorage value cannot name a column that does not exist.

## When it lands

- Root `CLAUDE.md`, *Chat*: the viewer's paragraph gains the columns and the dot, wrap, the
  toolbar and *Jump to now*, and the position cell with the landed marker on it, and loses
  `/container` on the pod and the row's own border; *The log viewer's library* gains
  `scrollerRef`; *Theming & window frame* gains the two storage keys; the right sidebar's
  paragraph gains the per-mode maximum and the panel yielding to the page.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `pnpm build`, `make test-js`, `make lint-js`; `make test-changed` while working.

By hand: open a view, and the landed line reads `0`, marked, with the lines above it counting
down and the ones the follow brings counting up; press Start, and the first line is the new
`0`; open a view over two containers, and each draws its own dot colour, the same in a
second window; turn on Pod, Node and Container, and the header and the rows line up as lines
arrive and as the panel scrolls sideways; turn on Wrap over a line longer than the panel, and it
wraps with no overlap; scroll up, and *Jump to now* appears; press it, and the viewer lands at
the newest lines and follows; press Pause at the bottom, and the last lines stay put while the
pod writes; press Play, and the missed lines arrive and the end stays in view; drag the panel to
1200px and narrow the window, and the panel gives way before the page; open a second window, and
it opens with the same columns and wrap.
