# A bounded log window that reads its dropped lines back

**Area:** `src/lib/log-viewer/` (paths relative to `src/` unless rooted).
**Answers:** [#389](https://github.com/kstackhq/kstack/issues/389).
**Prerequisites:** none.
**Status:** Planned.

## Goal

The window holds at most `maxLines` lines. Past that it drops lines at the edge farther from
the reader, never one on screen, and that edge keeps *more*: scrolling back to a dropped line
pages it in again, each once, in order, with no repeat and no gap. Every kept line keeps its key.

The window does not need to keep a line it can read again. Every line the sidecar has served is
answered by its log cache, or by the kubelet once the cache has let it go. So the cost of the
bound is only the read that brings a dropped line back.

Read `lib/log-viewer/README.md`, `window.ts`, `lines.ts`, `double-tailed-array.ts` and the
*Answers are state* paragraph of `core.tsx` first.

## Where a dropped edge reads back from

A page cursor is positioned only at a page's ends: the sidecar writes each page's `prev` at the
page's first line and its `next` past its last (`pageCursor.edges` in
`sidecar/internal/services/logs/fetch.go`). A follow's lines carry no page cursor at all, and a
long follow is exactly what grows the window. So a drop that falls inside a page, or anywhere in a
follow's lines, leaves no cursor that reads on from the cut, and `seen` cannot rewind a cursor:
the sidecar keeps the newer of a container's `seen` line and the cursor's own entry.

**A dropped edge reads back from a moment.** The window remembers the drop as a *seam*, the
timestamp farthest into the kept lines among the lines it dropped, and the next page that way is
an `At` at that moment: the `FetchQuery` kind the contract already has, which the client answers
as two disjoint halves, the older ending the nanosecond before the moment and the newer starting at
it (`lib/logs-client.ts`). The window keeps the half that lies its way and ignores the rest:

- **Before edge.** The seam is the **newest** timestamp among the dropped lines, so every dropped
  line is stamped at or before it. The page's lines stamped at or before the seam are prepended,
  the fresh ones; its `prev` becomes the before cursor, and the seam is cleared. A line stamped
  after the seam is left for the after side, which brings it as a late line.
- **After edge.** The seam is the **oldest** timestamp among the dropped lines, so every dropped
  line is stamped at or after it. The page's lines stamped at or after the seam are appended, the
  fresh ones; `resumeOf(page)` becomes `resume`, and the seam is cleared. A line stamped before
  the seam is left alone.

The farthest stamp, not the edge line's, because lines are held in arrival order: a follow's
streams interleave and a page after may bring a line older than one held, so the line at a
position is not the oldest or newest by time. After the seam page, paging that way is the ordinary
`Continue` from the cursor it answered. A line read back is new, not a repeat, because the drop
takes the dropped lines' cursors out of `held`.

Lines a line's own cursor would not reach are the same as today: more lines at the seam's
nanosecond than one half's limit are the sidecar's loud-second cut, named in `stream-skipped`.

## The drop

A drop runs at the end of `paged` and `followed`, after the page's or the batch's lines are in,
while the window holds more than `maxLines`. It never runs in `landed`: a landing is one page.

**Which edge.** While following, the before edge: the reader is at the end. Otherwise the edge
farther from the visible range: the before edge when more lines lie above the range than below
it, else the after edge. With no visible range yet, the before edge while pinned, else the after
edge.

**How many.** The excess over `maxLines`, capped so that no line of the visible range goes. A drop
capped short leaves the window over `maxLines` until the next drop.

**What the drop does**, in the window:

1. Takes the lines off `Lines` at that edge, which takes their cursors out of `held`, and sets the
   seam to the farthest stamp among them, as above.
2. Before edge: the before cursor is cleared, the edge keeps `more` and loses `empty`, and
   `visibleRange` moves down by the count so the next `emit` reads the right lines. After edge:
   `resume` is cleared, the pin and the wish to pin are dropped, since the newest lines are no
   longer held, and `newest` is rebuilt from the lines still held. A window that dropped at the
   after edge is behind, so its after edge has more by the existing rule.
3. Answers `hold` for a before-edge drop unless the answer is already `toEnd`; an after-edge drop
   changes nothing the scroller must do beyond what the page already answered.

**Invariants** the tests pin:

- `resume` is null exactly while an after seam is set, and the window is then unpinned, so
  `followFrom` is never called over a null `resume`.
- A seam is set exactly while its edge has no cursor and `more`. `query(edge)` answers the `At`
  while a seam is set, the `Continue` while a cursor is, and null with neither.
- A seam page of repeats alone that says more reads on, and one with no line that way and a page
  on is `empty`, both by the rules `paged` has today.
- A landing clears both seams, as it clears every other edge state.

## Keys are stable across a drop

`DoubleTailedArray` gets `dropFirst(n)` and `dropLast(n)`. `dropFirst` raises `keyOffset` by `n`,
so every kept line keeps `keyOffset + index`; `dropLast` moves nothing. Either may cross the
head–tail boundary, in which case it drops the one side whole and the rest off the other.
`Lines` gets `dropFirst(n)` and `dropLast(n)` that return the lines dropped, their cursors taken
out of `held`.

The core needs nothing new for keys: `getItemKey` reads `firstKey + index`, and the virtualizer
caches a row's measured size by its key, so a kept row keeps its measurement across the shift. A
`hold` answer is read before the commit as a key and an offset and performed after it through
`indexOfKey`, so it survives a drop that keeps the first visible row, which every drop does.

## The core

`maxLines` joins `Settings` in `core.tsx` with a default of `5000`, passed to `LogWindow` through
`LogWindowOptions`. `LogViewer` passes nothing. `CoreRows` is unchanged: `lineOfKey` already
answers `undefined` and `indexOfKey` `-1` for a key no longer held, which is what a selection or a
copy over a dropped row gets.

## Tasks

Do them in order. Each one passes `make test-changed`. The three are one pull request.

1. **The deque and the lines.** `dropFirst`/`dropLast` on `DoubleTailedArray` and `Lines`.
   Tests in `double-tailed-array.test.ts`: a drop at each end within one side, one that crosses
   the boundary, one of everything, and the keys of the kept elements unchanged after each. In
   `lines.test.ts`: the dropped lines returned in order, their cursors fresh again on the next
   `append`/`prepend`, and the kept ones still repeats.
2. **The window.** `maxLines` in `LogWindowOptions`; the seam per edge; the drop at the end of
   `paged` and `followed`; `query` answering the `At`; `paged` keeping one half of a seam page.
   Tests in `window.test.ts`: a follow past `maxLines` drops at the before edge and keeps
   `length` at `maxLines`, answering `toEnd`; a page after while reading back drops at the after
   edge, unpins, rebuilds `newest` from the kept lines and sends that `seen` on the next page
   after; a page before while reading forward drops at the after edge; the visible range is never
   dropped, the drop capped short; the seam is the farthest stamp, pinned with an out-of-order
   line among the dropped; the before query after a drop is an `At` at the seam, and the page's
   lines stamped after it are not taken; the after query likewise, with lines before it not
   taken; the seam page's `prev` (or `resumeOf`) replaces the seam; a seam page of repeats reads
   on; a landing clears the seams. Extend the seeded walk at the end of the file with a small
   `maxLines` and a check, after every step, that the lines held are a contiguous run of the
   fake's log with no repeat, and that scrolling back and forward over a drop brings every line
   once.
3. **The core and the docs.** `maxLines` in `Settings`. Tests in `core.test.tsx`: a follow run
   past `maxLines` keeps the window at `maxLines` with the end in view; scrolling up past a drop
   pages the dropped lines in again and the first visible row stays where it was; reading back
   past `maxLines` drops at the after edge and reaching the bottom pages them in and pins once
   the page finds no more. Then update `lib/log-viewer/README.md`: the window's rules gain
   *The window is bounded* and *A dropped edge reads back from a moment*, *Keys are stable* says a
   drop lowers nothing held, and the edges table gains the row for `At` from an edge. Add the
   bound to the brief in `src/CLAUDE.md`, and remove the first item under *Frontend (webview)* in
   `docs/TODO.md`.

## Acceptance

- A follow that runs past `maxLines` keeps the window at `maxLines`, with the view pinned to the
  end.
- Scrolling back past a drop pages the dropped lines in again, each once, in order, with no repeat
  and no gap, and the first visible row stays where it was.
- `window.test.ts`, `double-tailed-array.test.ts` and `core.test.tsx` cover a drop at each edge
  and a read back over it.
- `make lint-js` passes, and `make cover-js` does not fall.

## Verification

```sh
make test-changed
pnpm test --run src/lib/log-viewer
make lint-js
```

## When it lands

`lib/log-viewer/README.md` states the bound, the seam and the key rule; `src/CLAUDE.md`'s brief
names `maxLines`. An ADR records why a dropped edge reads back from a moment rather than a page
cursor. The TODO item is gone, and this spec with it.
