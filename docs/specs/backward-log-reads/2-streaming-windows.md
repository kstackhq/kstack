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

A window's lines go to the file as they arrive, in batches. A backward reader no longer collects a
window and reverses it; the filler no longer holds a forward read until it ends. Memory drops from
one window per stream to one batch per stream, for every read. Because the part of a backward
window nearest the point now arrives last, it links up only when it reaches its end, and a window
cut part way is left as an island. Two of step 1's rules follow the order of arrival and change with
it: `AtEnd` and the seam.

Windows are sized as today. Step 3 changes how they are sized.

## What is not in this step

- **No change to how windows are sized**, or to when a read widens. Both readers keep today's
  decisions; only how lines leave them changes.
- **No too-busy stop.** Step 3 adds it. Here a window is cut part way only by a cancel, a failure,
  or the stream's budget share (`counted`).
- **No change to what a forward read decides.** It is one window, cancelled the moment it has
  counted `need`, as today. Only how its lines reach the file changes (§1). The rules in §2 are rules
  for a backward read.
- **No new limits.** That is step 4.

## Design

### 1. Lines leave a window as they arrive

A window's lines go to the file as they arrive: oldest first, in batches of `WriteBatch` lines or
`WriteBatchBytes` (1 MiB). The reader no longer collects a window or reverses it (`collect`,
`takeBack`). Memory is one batch per stream.

Both backward loops (`backward`, `backwardUntil`) decide what to do next from a window's first line,
its oldest moment, and its end. They keep making those decisions, from what they noted while the
window streamed past, instead of from a collected slice.

**A forward read streams through the same path.** It is one window whose lines arrive nearest the
point first, so today's links hold batch by batch: `Prev` and `Merge` on the first batch, `Next`
and `AtEnd` on the last, and only when the read finished. A read cancelled at `need` has already
written its part linked down. `Merge` needs the GET's first moment (`returnedWhole`) before the
first batch, so the read carries `oldest` from its first line (`onOldest`), not from its end. This
covers `Head` pages and the follow's gap fill, which today hold up to their budget share in memory;
the gap fill's hold (`WriteHeld`) is taken per batch and released together, as `readGap` already
collects releases.

**Each batch is one write.** `write` keeps its shape, the mirror check and then one `Write`, with a
batch as its unit instead of a window. So each batch is one mirror read and one commit, stamped,
pinging the stream's bus. Step 1's count of a few commits per stream per page no longer holds: a
page commits about once per `WriteBatch` lines (1,000) or per MiB downloaded, per stream, all
through SQLite's one writer, beside the tailers'. A commit of one batch is milliseconds, so this is
acceptable; if it shows, page writes can go through the file's writer later.

**`Next` and `AtEnd` wait for the end.** A batch that fills up may turn out to be the window's last,
so neither goes on a batch written before the window's end is known. The filler either holds a full
batch until the next line or the end arrives, or writes full batches at once and closes the window
with an empty `Fill` whose `Prev` is the last line written, carrying `Next` and `AtEnd`; `writeFill`
links and marks from `Prev` when a fill has no lines.

**A window is visible between its batches.** Until its last batch lands, a window is a chunk with
no link up and a live gap on each side. A follow on the same stream may see it and start filling
the upper gap while the page is still writing it. The file takes this: a line both write is stored
once (`INSERT OR IGNORE` on `lines_position`), and the result is one span with no line twice. The
invariant test seeds the case.

### 2. The rules

Lines now arrive oldest first inside a backward window, so the part of a window nearest the point
arrives last. These rules replace step 1's `AtEnd` and seam rules, which assumed the other order.

- **A window links up only when it reaches its end.** Its batches link to each other and down as in
  step 1. Only the batch that holds the window's last line links up (`Next`), to the oldest line the
  read has written so far, or the chunk at the point. Step 1 links a partial window up, because its
  part is the newest; here a partial window is the oldest part, so it never links up.
- **A window cut inside is an island.** A cancel, a failure, or the budget share can end a window
  before its end. What it wrote has no link up, and none down unless it reached the lower bound. The
  read does not go on from it: the next read of that range starts again at the point.
- **A window is judged only at its boundary.** The kept lines a window has counted so far are the
  ones farthest from the point, so `need` is checked only between windows. The filler no longer
  cancels a backward read the moment it has counted `need`.
- **`fromKey` means the first window completed to the key.** Today it means "the fill got any line",
  which with lines arriving oldest first would let the walk cross the unread range at the cursor. The
  reader's edge is the oldest line of the last window to complete, not the oldest line taken.
- **`AtEnd` goes only on a completed first window.** Step 1 puts it on a cancelled first window
  too, since the part that arrived was the newest. Here a cut first window is an island that may not
  hold the log's last line; marked `AtEnd`, it would end every later `Head` page at the island, and
  the lines past it would never be read. So `atEnd` asks for the window's end as well.
- **The seam is the first window's last line, set when it completes.** `newest` is the last line of
  the first window's last batch. Taken from the first write, as today, it would be the oldest batch's
  last line, and an `Around` page's `Head` half would link over lines another chunk holds. A cut
  first window sets no seam, as it sets no `fromKey`.

### 3. The filler stops the reader between windows

`streamRead` carries window boundaries (step 1). It also gains a way for the filler to say "no more
windows": a `more func() bool` on `readOpts`. The reader calls it at each boundary, after it sends
the window's end and before its next GET, and ends clean when it answers false. `more` waits until
the filler has written the window's last batch, then answers from the filler's count and the write's
outcome: false once the stream holds `need` kept lines, and false when the write found a restart or
a gone row. The count stays on the filler's goroutine, and the reader never starts a GET the filler
has already decided against. `more` also returns, false, once the read's context ends: a filler that
stopped early, on a failed write or the page's end, answers nothing, and the reader must not wait on
it. Cancelling the read stays for the page ending, and leaves an island.

### 4. The `tailLines` reader

It follows the same rules. Its windows drop their partial first moment, which the next window
re-reads, as today. The moment that ends the read, the log's first or `since`'s, is read whole and
forward (`wholeMoment`). Its lines are older than the window that found it and arrive after it, so
they are a window of their own: the reader ends the window, sends the moment's lines oldest first,
and ends again. The moment's window links up to the window's oldest line and, as the read's oldest
window, takes `Prev` and `Head` as the rules say. Nothing is held in memory.

### 5. Islands

An island is a chunk with no link on either side. The file already accepts one: an unlinked chunk
leaves two live gaps, and a write takes a nil `Prev`. This step adds tests, not code, for:

- **Both gaps stay live**, so a later fill is offered each.
- **The janitor keeps an island for `ReadTTL`, then evicts it as any read chunk.** A page stamps
  every chunk it writes (step 1 §3), an island included, so no island counts as unread.
- **A later read links to an island.** A forward read from its last line links as any forward fill
  links to a chunk it continues (`returnedWhole`). A backward read whose lower bound is the island's
  last line links to it as to any `prev`.
- **Its first second is whole**, because a time window starts at the top of a second. **Its last
  second may be cut.** Lines up to the cut are numbered from the top of that second, so their
  ordinals are right.

## Decisions this step asks for

1. **Stream inside a window in its own step.** It changes how lines leave the readers, not what the
   readers decide. Step 3 then changes the decisions alone.
2. **Judge a backward read's `need` only at a window's boundary.** Inside a window, the count
   measures the wrong end. A forward read's count measures the right end, so it keeps cancelling at
   `need`.
3. **Check the mirror and commit per batch.** `write` keeps one shape, and each write is as sure as
   a window's is today. The cost is a local query and a commit per 1,000 lines.
4. **`wholeMoment` is a window of its own.** Its lines arrive after the window they are older than,
   so a boundary between them keeps every link true, and nothing is held back.

## Tasks

1. **The readers stream.** Both backward loops write as lines arrive, without `collect` or
   `takeBack`, keeping their decisions from what they noted. A forward read writes as its lines
   arrive, `oldest` known from its first line. — Planned.
2. **The rules.** Linking on completion, islands on a cut, `need` at boundaries, `fromKey` and
   the edge, `AtEnd` on a completed first window, the seam from its last batch. — Planned.
3. **The stop between windows.** `readOpts.more`, called at each boundary before the next GET.
   — Planned.
4. **`wholeMoment` as its own window.** — Planned.
5. **The fake.** The test kubelet models the README's four points and a log that grows between
   requests. — Planned.

## Tests

- **Memory is one batch.** A large backward window, a `Head` page over a long sparse gap, and a
  follow's gap fill each hold at most one batch per stream in memory.
- **Links only on completion.** A window cancelled after its first batch leaves no link up. A window
  read whole links up exactly once.
- **A cut window is an island.** The next page reads the lines nearest the cursor, not the island's.
  No line is lost at the cursor.
- **Islands.** Both gaps stay live; an island is evicted after `ReadTTL` and not before; a forward
  read from its last line and a backward read with it as lower bound each link to it, and no line is
  stored twice.
- **`need` at boundaries.** A backward window holding more than `need` kept lines is read to its end
  before the read stops, and no GET follows the window that filled `need`.
- **A forward read still stops at `need`.** A `Head` page over a long gap downloads about one page,
  and the part it wrote is linked down.
- **`more` waits for the write.** A restart found at a window's last batch ends the read before its
  next GET. A write that fails between windows leaves no reader goroutine waiting.
- **`AtEnd`.** A `Tail` page whose first window completes marks its newest chunk `AtEnd`, and a later
  `Head` page to the end sends no GET. A `Tail` page whose first window is cut marks nothing, and
  the later `Head` page sends a GET and reads the lines past the island.
- **The seam.** An `Around` page whose `Tail` half's first window spans several batches joins its
  halves with no lost or repeated line.
- **A follow fills beside a page.** A follow's gap fill of the gap above a half-written window, seeded
  in the invariant test, leaves one span with no line twice.
- **`wholeMoment`.** A read ending at the log's first moment, and one ending at `since`'s, each form
  one span with `Head` or `Prev` on the moment's chunk.
- **Growth.** The log grows between requests. Reads return the same lines as with no growth.
- **The kubelet's points** are each a case in the fake.
- **`TestPagesJoinWithoutLossOrRepeat` and `TestTheFileKeepsItsInvariant` pass**, with cancels and
  failures inside windows seeded.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: the page line, which says each window is written once
  the next starts, says instead that a backward window streams to the file in batches as its lines
  arrive, links up only when complete and leaves an island when cut; that `AtEnd` goes only on a
  completed first window; and that a forward read is one window, written in batches as it arrives.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the Go checks. No wire change.
