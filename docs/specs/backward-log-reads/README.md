# Backward log reads

Scrolling up through a log, opening it at a moment, or searching it with a grep works for any
normal log. A backward read guesses a window of time from how busy the log is nearby and corrects
the guess as it reads. Every line it downloads goes straight to the log file, and the grep runs over
the file. A log far outside normal sizes or rates is stopped with a clear reason instead of failing
at random.

Answers [#320](https://github.com/kstackhq/kstack/issues/320). Builds on the log cache (ADR *the log
cache*). Go paths are under `sidecar/internal/services/logs/`. This is a sequence of five steps,
listed under [*Steps*](#steps), each a pull request into `wip/log-views`, where the log cache lives.
It shares that branch rather than taking a `wip/` of its own, as the log cache sequence did, so it
retires with the log views' own landing (step 5).
This README is the shared design; each step's file states only what is its own.

## Why

Today, scrolling up can stop for good with *"this page ran out of budget before pod/container was
read past its start"*.

The kubelet cannot return "the lines before this point". It can return the lines after a moment
(`sinceTime`), oldest first, or the last N lines of the whole log (`tailLines`). So to read backward
we pick a start time and download everything from there up to the point. Today the start time is
guessed from the time span alone. A burst of logging just before the point makes the window huge.
The page has 32 MiB to spend, split evenly across its containers: about 330 KiB each when a view has
a hundred. If the first window holds more than that, the page is refused. A retry makes the same
guess and fails the same way.

Example: a 100-replica view where one pod was quiet for 7 days and then burst at more than about
9 KiB/s. Its first window is about 37 s, it is over budget, and scrolling up is stuck there.

The kubelet also cannot grep. A grep downloads every line and filters it here. A sparse grep, or one
with no match, has to download a whole log to say so.

## The approach

The Kubernetes API cannot serve every log well, so this sequence does not try. It designs for normal
logs and names the limits of "normal". Five changes:

1. **Write as we read, grep on disk** (steps 1 and 2). Each window goes to the log file as soon as it
   completes (step 1), then as its lines arrive (step 2), so memory stays flat. Every kubelet line is
   kept, and the grep runs when the page reads the file. A later search over the same range
   downloads nothing. The file grows to 1 GiB.
2. **Guess the window from nearby activity** (step 3). The lines next to the point show how busy the
   log is. Pick a window that should hold about one page, and not too many bytes.
3. **Correct the guess as it reads** (step 3). A window far busier than planned is stopped, kept as an
   island, and replaced by a shorter one. A window that is too quiet is followed by an older one.
4. **Cut a page at normal limits, stop a stream only when it is abnormal** (step 4). A page that has
   read a lot is cut, and the next page goes on. A stream that cannot be read within normal limits
   stops with a reason the user can read. The rest of the view goes on.
5. **Write the reasons down** (step 5).

A later change can offer kubetail, installed in the cluster, to a user whose logs go past these
limits. That is not part of this sequence.

## Words used here

- **The point.** Where a backward read stops: the page's cursor, the first line of the chunk above a
  gap, or an `Around` page's moment.
- **A window.** One GET with `sinceTime`. It returns every line from the top of a second up to the
  point, oldest first.
- **Kept lines.** Lines that pass the view's grep. Without a grep, every line. A page needs `need`
  kept lines (`limit + 1`).
- **The rates.** Kept lines per second, and bytes per second, measured from lines we already have.
- **An island.** A chunk written from part of a window, linked to nothing on either side.
- **Cut.** A page ends early with `more: true`, and the next page goes on from where it ended.
- **Stop.** A stream's read ends with a reason, named in the page's `skipped`.

## What the kubelet does

Read from `staging/src/k8s.io/cri-client/pkg/logs/logs.go` and `tail.go` on `kubernetes/master` on
2026-10-17. Step 2 pins each point in the test fake.

1. **Only the current log file is served.** Rotated files are never read. A container's whole
   readable log is one file: `containerLogMaxSize`, 10 MiB by default.
2. **`sinceTime` filters; it does not seek.** The kubelet reads the file from its first byte and
   skips older lines. Each window costs the node a scan of the file up to its start.
3. **`sinceTime` is whole seconds.** A window always starts at the top of a second. The smallest
   window is one second, and every second a window returns is whole.
4. **`tailLines` counts raw file lines.** The runtime splits a long line into parts, and each part
   counts. So `tailLines=n` returns at most `n` lines.

## What counts as normal

| Normal | Limit |
| --- | --- |
| One container's log, as the kubelet serves it | about 10 MiB (the kubelet's default) |
| One container's busiest second | at most 1 MiB, about 5,000 lines |
| One view | at most 100 containers (`maxStreams`, as today) |

Most clusters are well inside these numbers. Step 4 turns them into limits.

## What is not in this sequence

- **The kubetail offer.** This sequence only makes the limits clear and reports them.
- **Viewer changes** (see *Later*). Pages, edges, cursors and `skipped` keep their shape. No wire
  change.
- **Forward reads and the follow's gap fill.** They change only by writing as their lines arrive
  (steps 1 and 2) and by the new limits (step 4).
- **No new backend.** The kubelet is still the only one.

## Later

Each is a small change once this sequence lands. The first is the one to do next.

- **Read on through empty pages.** A grep with no match across many logs returns several empty pages
  in a row, and the viewer shows *Keep looking* after each. The viewer could read on by itself a few
  times before asking. Then the page limit's size no longer matters to the user.
- **The kubetail offer** when a stream stops at a limit. A `docs/TODO.md` line tracks it.
- **Say how far a search has got.** *"Searched back to 09:14, no matches yet."* The webview never
  reads a cursor, so this needs a moment on the page: a small wire change.
- **Tell a limit from a failure** in the viewer, so the kubetail offer can sit next to the reason.

## Decisions this sequence asks for

1. **Design for normal logs, and say where normal ends.** The Kubernetes API cannot serve every log
   well. Clear limits with a reason beat a best effort that fails at random.
2. **Cut for work, stop only for the abnormal.** A large read is normal and makes progress, so it
   cuts. Only a second too loud to read is abnormal, so only it stops.
3. **Keep every kubelet line, grep on disk.** A search over a range already downloaded is free, and
   memory stays flat with batched writes.
4. **Windows sized by kept lines and by bytes.** Kept lines keep a normal page to one window. Bytes
   keep a sparse grep's windows to a few MiB each.
5. **A 1 GiB log file.** A search across a view's logs stays cached.

Each step adds the decisions that are its own.

## Steps

Built one after another, each a pull request into `wip/log-views`. Each leaves the sidecar building
and its tests passing.

| Step | Builds | Status |
| --- | --- | --- |
| [1 The file](1-the-file.md) | Each window written when it completes; the larger walk budget; the 1 GiB budget | Done |
| [2 Streaming windows](2-streaming-windows.md) | A window's lines stream to the file; links only on completion; islands when cut; a backward read's `need` judged between windows | Done |
| [3 The windows](3-the-windows.md) | Windows sized from nearby rates, stop and narrow, widen, doubling `tailLines`, `Around` | Done |
| [4 The limits](4-the-limits.md) | Bytes reserved per window; cut and stop, every cut with an edge; time windows after the first `tailLines` window; `failLimit`; the walk follows the fill; the old budgets and the refusal removed; the regression test | Done |
| [5 The docs](5-the-docs.md) | The ADR, the logs `CLAUDE.md`, the security model's rows, `TODO.md`; the sequence retired | Planned |

## Security

No new boundary. Window bounds are moments the sidecar computes. Log text still reaches the file only
through `stripControl`. A hostile container can make its own stream stop, or make pages slow. It
cannot make a page download or walk more than the limits allow, except by about one second, at
most 1 MiB, a window: a window is not stopped in its last second. The `tailLines` window asks for
no more lines than its allowance holds at a part a line. The log file can now hold 1 GiB of
cluster data in the clear, by the same decision that covers the cache today.
