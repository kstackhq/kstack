# Keep a long chat's transcript cheap to render while an answer streams

Answers [#911](https://github.com/kstackhq/kstack/issues/911).

## Problem

A streaming answer arrives as `Modified` frames, each carrying the whole text so far. On each
frame `ChatTranscript` re-renders every `Message`, which rebuilds its tool-call tree and the rest
of its body. The work per frame grows with the chat's length, so a long chat stutters while an
answer streams.

Without the React compiler, every assistant message also parses its markdown again. With it, the
compiler probably keeps `<Markdown text={text} />` for an unchanged `text`, so a real build may
already skip that parse. Task 1 counts the `Markdown` renders to find out. The fix is the same
either way: the compiler bails out silently, so the transcript's promise is kept by hand.

Three things cause it, all in `src/`:

- `Message` (`components/chat/chat-transcript/transcript.tsx`) is not memoized.
- `ChatTranscript` passes every `Message` a new `ends` map on each render (`viewEnds(messages)`),
  so a memoized `Message` would still see changed props.
- `Code` (`components/primitives/markdown/markdown.tsx`) calls `highlight(lang, children)` on every
  render, so each finished fence in the streaming answer is highlighted again on each frame.

## Goal

While an answer streams, a frame renders the streaming message and nothing else. The commit time
per frame does not grow with the number of messages in the chat.

## What is already right

Leave these alone; each is checked against the code.

- `fold` (`lib/chats.tsx`) copies the map and replaces only the changed entity, so an unchanged
  message keeps its object from frame to frame. Task 2 pins this with a test.
- `onAskAgain` reaches the last row alone. Every other row receives `undefined`.
- `labelOf` is a `useCallback` on `models`. The other props of `Message` are primitives.

## Tasks

Run them in order. Each lands with its tests.

### 1. Measure the baseline

Record, in a dev build (`pnpm tauri dev`, React compiler on), while an answer streams into a chat
of about 50 turns and again into one of about 200:

- the commit time per frame,
- how many `Message` and `Markdown` components render per frame.

Wrap the transcript's rows in a temporary `<Profiler onRender>` that logs, and do not commit it.
React DevTools cannot attach: the CSP's `connect-src` (`src-tauri/tauri.conf.json`) refuses its
socket, `ws://localhost:8097`. Do not widen the CSP to let it in (`src/CLAUDE.md`, *Security
invariants*).

Take the median commit time over one whole answer. Write down how the chats were made and which
answer streamed, so task 4 repeats the same run.

### 2. Render only the message that changed

In `components/chat/chat-transcript/transcript.tsx`:

- Wrap `Message` in `memo`, written by hand. The coverage run loads no React compiler, and the
  test below must pass there (`src/CLAUDE.md`, *Lint / build*). `LogViewerRow`
  (`components/logs/log-viewer/row.tsx`) is the precedent.
- Give `Message` the same `ends` map for as long as its entries are unchanged: same call ids, same
  values, same order. It changes only when a logs view opens or resolves, and one full re-render
  then is fine. Do not write a ref during render: the compiler's lint rules (`react/refs`) refuse
  it. Keeping the map in state and replacing it during render when the entries differ is one
  shape that passes.

Tests:

- `lib/chats.test.tsx`: after `fold` applies a `Modified` for one message, every other message in
  `items` is the same object as before.
- `components/chat/chat-transcript/transcript.test.tsx`: render a transcript of several finished
  messages and one streaming one, with `Markdown` mocked by a component that counts its renders
  per `text`. Re-render with a new object for the streaming message alone. The finished messages'
  counts do not move; the streaming message's does.
- Same file: when a later message opens a logs view, an earlier view's row still draws its new
  end. This guards the stable `ends` against going stale.

### 3. Parse and highlight a finished text once

In `components/primitives/markdown/markdown.tsx`:

- Wrap `Markdown` in `memo`. Its one prop is `text`.
- Memoize `Code`'s highlighted body with `useMemo` on `[lang, children, ready]`. `ready` is what
  turns a fence drawn plain into a highlighted one when its grammar arrives. It comes from state
  the load sets, not from `loaded(lang)` read in render: the compiler caches that call on `lang`,
  so a fence drawn before its grammar arrived would stay plain.

Tests, in `markdown.test.tsx`:

- Re-rendering `Markdown` with a longer text that keeps an earlier fence unchanged highlights that
  fence once in total.
- The existing test of a grammar that arrives late still passes.

### 4. Measure again

Repeat task 1's run on the same chats. Put the before and after numbers in the PR.

Then decide the two follow-ups below from the numbers, and say which way each went in the PR.

## Not in this spec

- **`content-visibility: auto` on the rows** (the issue's step 4). After task 2 the finished rows
  do not change, and the streaming row is the last one, so the browser has little layout or paint
  left to skip. It also risks scroll jumps where the webview has no scroll anchoring, and it clips
  whatever a row draws outside its box. Add it in its own PR only if task 4 shows layout or paint
  time still growing with the chat's length, and check it by hand on each platform's webview.
- **Parsing the streaming answer block by block.** Each frame parses the whole text of the answer
  being written, so one very long answer slows as it grows. If task 4 shows the streaming row
  dominating a frame, open an issue for it. Reference-style links and footnotes cross blocks, so
  it is not a small change.
- **Virtualizing the transcript**, for the reasons the issue gives.

## Verification

- `make test-changed` while working; `make check-changed` before the pull request.
- `make cover-changed` lists no changed line.
- The re-render tests pass under `pnpm vitest run --mode coverage`, which is the run without the
  compiler.

## Done when

- While an answer streams, only the streaming message's `Message` and `Markdown` render on each
  frame, shown by the Profiler numbers in the PR.
- The 200-turn chat's median commit time per frame is within 20% of the 50-turn chat's.
- The tests of tasks 2 and 3 are in.

## When it lands

- `src/components/chat/CLAUDE.md` states that `Message` is memoized by hand and that every prop
  `ChatTranscript` passes it must keep its identity across frames.
- `src/CLAUDE.md`'s coverage rule names the transcript's row beside the log row's cells as a
  re-render memoized by hand.
- This file is deleted.
