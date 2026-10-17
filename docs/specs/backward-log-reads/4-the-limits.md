---
title: The limits
scope: sidecar
status: Planned
---

# The limits

**Needs:** step 3, whose windows are bounded, so a limit checked between windows bounds a page.
**Unblocks:** step 5. This step closes #320 and runs the sequence's integration gate.

Go paths are under `sidecar/internal/services/logs/`. The shared design is the README; this file
says what to build and how to know it is built.

## In short

The README's limits of normal become limits on a page. Most of them cut the page, and the next page
goes on. One, a single second too loud to read, stops the stream with a reason the user can read.
The old budget, its even split and the *"ran out of budget"* refusal go.

## What is not in this step

- **The kubetail offer, and any viewer change.** The reason reaches the viewer through `skipped`, as
  any failed stream's does today.
- **The walk's budget.** Step 1 raised it to 64 MiB and kept its even split.

## Design

### 1. The limits

| Limit | Value | When it is reached |
| --- | --- | --- |
| Bytes in a one-second window | 1 MiB | the stream stops |
| Bytes one stream downloads for one page | 16 MiB | the page is cut |
| GETs one stream sends for one page | 16 | the page is cut |
| Bytes one page downloads, all streams together | 64 MiB | the page is cut |

They apply to every read a page makes: backward, forward, and the first `Tail` page's `tailLines`
windows.

### 2. Cut

A page that has read a lot is normal: a sparse grep, a quiet log read back far, or many containers.
The stream that reached the limit ends at the oldest line it wrote. The page is cut there with
`more: true`, and the next page goes on from it.

Download limits are checked between windows, never inside one. So every stream reads at least one
window, and every stream that read lines has a line to cut at. Step 3 sizes each stream's first
window from the page's limit, 64 MiB ÷ the streams it reads, so the first windows together fit in
it. A page downloads about 64 MiB at most. It goes over only by the part of a window read before that
window was stopped as too busy: at most four times the window's byte target.

### 3. Stop

A one-second window over 1 MiB is the one case no normal read gets past: the window cannot be made
smaller, and it is not normal. It is checked as the window reads, and the GET ends at 1 MiB. The
stream's read ends, and the page names it in `skipped`:

> *pod/container logs more than 1 MiB in one second, more than Kstack reads back through the
> Kubernetes API*

This is a skipped stream, as a failed read is today. The page goes on with the other streams, and its
edge still says there is more that way. If every stream of the page stopped and none read a line, the
page is refused with the reason, as today (`everyStreamFailed`).

The stop is its own failure kind, `failLimit`, beside fatal, denied, ended and transient
(`failureKind`). The kubetail offer can find it later. The reason is a `Refusal` value, so no caller
changes its words.

### 4. What goes

- `maxBudget` and the per-stream `share` in `fillQuery`.
- `maxWindows`, if step 3 left any use of it.
- The *"ran out of budget"* refusal in `mergePage`. No backward read ends with nothing to show
  anymore. It either has a line to cut at or stopped with a reason.

## Decisions this step asks for

1. **16 MiB and 16 GETs per stream, 64 MiB per page.** They keep a page to a few seconds, and a
   default 10 MiB log fits in one page's reads.
2. **1 MiB in one second is the line for abnormal.** It is about 5,000 lines of normal length.
3. **The stop is a skipped stream, not a refused page.** One wild container does not take the view
   down.

## Tasks

1. **The limits and the cut.** The three cutting limits, checked between windows, in every read a page
   makes. — Planned.
2. **The stop.** The one-second limit, `failLimit`, its reason. — Planned.
3. **The removals.** `maxBudget`, the split, `maxWindows`, the refusal. — Planned.
4. **The regression test and the gate.** — Planned.

## Tests

- **The regression.** A 100-stream view where one stream bursts at 5,000 lines/s just before the
  cursor pages back with no refusal and no stopped stream.
- **A grep with no match.** One stream: the read reaches the log's first line, and the page answers no
  record and no more. A hundred streams: pages are cut at 64 MiB with `more: true` until every stream
  reaches its first line, and no stream stops.
- **The per-stream cuts.** A sparse grep over a 40 MiB log is cut at 16 MiB, then at 16 GETs on a log
  whose windows are tiny, and each next page continues from its cut.
- **Too loud.** One stream logs 2 MiB in one second just before the cursor. It is skipped with the
  reason, and the other streams page on.
- **Every stream stopped.** A view whose only stream is too loud is refused with the reason.
- **No budget refusal.** No seeded walk of `TestPagesJoinWithoutLossOrRepeat` ends in *"ran out of
  budget"*.

## When it lands

- `sidecar/internal/services/logs/CLAUDE.md`: the limits, cut and stop; `failureKind` gains
  `failLimit`.
- Close #320.

## Verification

Per *Verification commands* in [`docs/specs/README.md`](../README.md): the full checks, since this
step is the sequence's integration gate. No wire change.
