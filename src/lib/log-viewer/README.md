# The log viewer

A headless log viewer for React: it keeps the lines of a log, lands at the start, the end or a
moment, loads more at either edge as the user scrolls, follows new lines while pinned to the
end, and hands its rows to a render function. It draws nothing and talks to nothing.

It began as [kubetail](https://github.com/kubetail-org/kubetail)'s dashboard log viewer, reshaped
to the contract below. Files that came from kubetail carry its copyright line beside ours.

## The contract

`types.ts` is the whole seam between the viewer and whatever feeds it:

- **`LogLine`** — one line: when it was written, the opaque `cursor` that names its place in the
  log, its text, the `stream` it came from (namespace, pod, container, and whether it is the
  instance before the last restart; the four together name the stream) and the `place` its pod
  runs (node, region, zone, OS, architecture; descriptive only).
- **`Cursor`** — how the client is asked for the lines before or after one. The viewer hands a
  cursor back to the client that issued it and reads nothing from it.
- **`Anchor`** — where the viewer is told to be: `Head`, `Tail`, or `At` a moment. The viewer
  speaks anchors; only the client speaks cursors.
- **`Client`** — four fetches (`since`/`until` keep the cursor's line, `after`/`before` leave it
  out; no cursor reads from the start or the end) answering a `FetchResult`, and
  `subscribe(after, onLine)`, which calls back per line written after `after` until the function
  it answers is called.

Anything that implements `Client` can feed the viewer: a GraphQL client, a file, a fake.

## The boundary

The library imports `react`, its own files, and nothing else: no `@/` import, no relative
import that leaves this directory, no package of the app's. Nothing enforces that yet; a change
here keeps it by hand, so the directory stays one a package could be lifted from.

What the app needs that the library does not know — a client over its transport, the rows and
columns, selection and copy, what to do with an error — lives beside the component that mounts
the viewer, never here.

`index.ts` is the public surface. The app imports from it and from nowhere else in the directory.
