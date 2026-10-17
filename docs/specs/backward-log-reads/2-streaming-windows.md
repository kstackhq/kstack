---
title: Streaming windows
scope: sidecar
status: Planned
---

# Streaming windows

**Needs:** step 1, which writes each window when it completes, with window boundaries on the read. **Unblocks:** step 3, whose windows can be large and can stop part way.

Go paths are under `sidecar/internal/services/logs/`. The shared design is the README; this file
says what to build and how to know it is built.

## In short

A backward window's lines go to the file as they arrive, oldest first, in batches. The reader no
longer collects a window and reverses it. Memory drops from one window per stream to one batch per
stream. Because the part of a window nearest the point now arrives last, a window links up only when
it reaches its end, and a window cut part way is left as an island.

Windows are sized as today. Step 3 changes how they are sized.

## What is not in this step

- **No change to how windows are sized**, or to when a read widens. Both readers keep today's
  decisions; only how lines leave them changes.
- **No too-busy stop.** Step 3 adds it. Here a window is cut part way only by a cancel, a failure,
  or the stream's budget share (`counted`).
- **No change to forward reads.** Their lines already arrive in the order they are written.
- **No new limits.** That is step 4.

## Design

### 1. Lines leave a window as they arrive

A window's lines go to the file as they arrive: oldest first, in batches of `WriteBatch` lines or
`WriteBatchBytes` (1 MiB). The reader no longer collects a window or reverses it (`collect`,
`takeBack`). Memory is one batch per stream.

Both backward loops (`backward`, `backwardUntil`) decide what to do next from a window's first line,
its oldest moment, and its end. They keep making those decisions, from what they noted while the
window streamed past, instead of from a collected slice.

### 2. Four rules

Lines now arrive oldest first inside a window, so the part of a window nearest the point arrives
last.

- **A window links up only when it reaches its end.** Its batches link to each other and down as in
  step 1. Only the batch that holds the window's last line links up (`Next`), to the oldest line the
  read has written so far, or the chunk at the point. Step 1 links a partial window up, because its
  part is the newest; here a partial window is the oldest part, so it never links up.
- **A window cut inside is an island.** A cancel, a failure, or the budget share can end a window
  before its end. What it wrote has no link up, and none down unless it reached the lower bound. The
  read does not go on from it: the next read of that range starts again at the point.
- **A window is judged only at its boundary.** The kept lines a window has counted so far are the
  ones farthest from the point, so `need` is checked only between windows. The filler no longer
  cancels a read the moment it has counted `need`.
- **`fromKey` means the first window completed to the key.** Today it means "the fill got any line",
  which with lines arriving oldest first would let the walk cross the unread range at the cursor. The
  reader's edge is the oldest line of the newest completed window, not the oldest line taken.

### 3. The filler stops the reader between windows

`streamRead` carries window boundaries (step 1). It also gains a way for the filler to say "no more
windows". The reader checks it at each boundary, before its next GET. This is how a read ends once it
has `need` kept lines. Cancelling the read stays for the page ending, and leaves an island.

### 4. The `tailLines` reader

It follows the same rules. It also holds back a window's first moment, which may be partial, until a
later window or `wholeMoment` reads it whole. A held moment stays in memory, is written with the
window that completes it, and is never written on its own.

### 5. Islands

An island is a chunk with no link on either side. The file already accepts one: an unlinked chunk
leaves two live gaps, and a write takes a nil `Prev`. This step adds tests, not code, for:

- **Both gaps stay live**, so a later fill is offered each.
- **The janitor evicts an unread island** before any chunk a page read.
- **A later read links to an island.** A forward read from its last line links as any forward fill
  links to a chunk it continues (`returnedWhole`). A backward read whose lower bound is the island's
  last line links to it as to any `prev`.
- **Its first second is whole**, because a time window starts at the top of a second. **Its last
  second may be cut.** Lines up to the cut are numbered from the top of that second, so their
  ordinals are right.

## Decisions this step asks for

1. **Stream inside a window in its own step.** It changes how lines leave the readers, not what the
   readers decide. Step 3 then changes the decisions alone.
2. **Judge `need` only at a window's boundary.** Inside a window, the count measures the wrong end.

## Tasks

1. **The readers stream.** Both backward loops write as lines arrive, without `collect` or
   `takeBack`, keeping their decisions from what they noted. — Planned.
2. **The four rules.** Linking on completion, islands on a cut, `need` at boundaries, `fromKey` and
   the edge. — Planned.
3. **The stop between windows.** The filler's signal on `streamRead`, checked at each boundary.
   — Planned.
4. **The `tailLines` reader's held moment.** — Planned.
5. **The fake.** The test kubelet models the README's four points and a log that grows between
   requests. — Planned.

## Tests

- **Memory is one batch.** A large window holds at most one batch per stream in memory, plus a held
  moment.
- **Links only on completion.** A window cancelled after its first batch leaves no link up. A window
  read whole links up exactly once.
- **A cut window is an island.** The next page reads the lines nearest the cursor, not the island's.
  No line is lost at the cursor.
- **Islands.** Both gaps stay live; an unread island is evicted before a read chunk; a forward read
  from its last line and a backward read with it as lower bound each link to it, and no line is
  stored twice.
- **`need` at boundaries.** A window holding more than `need` kept lines is read to its end before
  the read stops.
- **Growth.** The log grows between requests. Reads return the same lines as with no growth.
- **The kubelet's points** are each a case in the fake.
- **`TestPagesJoinWithoutLossOrRepeat` and `TestTheFileKeepsItsInvariant` pass**, with cancels and
  failures inside windows seeded.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: a backward window streams to the file, links up only
  when complete, and leaves an island when cut.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the Go checks. No wire change.
