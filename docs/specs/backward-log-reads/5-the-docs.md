---
title: The docs
scope: docs
status: Planned
---

# The docs

**Needs:** step 4, so what is written down is what was built. **Unblocks:** the kubetail offer,
which reads these docs.

Paths are from the repository root. The design is the README; this file says what to write and how
to know it is done.

## In short

Each earlier step updated the `CLAUDE.md` lines it changed. This step writes down the reasons, reads
the logs `CLAUDE.md` cold and fixes what it gets wrong, adds the follow-ups to `TODO.md` and retires
the sequence. #320 closes when `wip/log-views` lands on `main`. Nothing in it changes code.

## What is not in this step

- **No code.** A gap found while reading is an issue, not a fix here.

## Design

### 1. The ADR

One, in `docs/adr/`, *Backward reads assume normal logs*, with a row in the index. It records the
design as built, not the order it was built in. The steps' *Decisions* are its source, but some were
replaced by a later step; the later one is the decision:

| Step decision | Replaced by |
| --- | --- |
| 1: write per window, not per batch inside a window | 2: a window streams to the file in batches |
| 1: keep the walk's even split | 4: the walk follows the fill |
| 3: first windows sized under the share of `maxBudget` | 4: the allowance and the pool |
| 3: doubling `tailLines` windows | 4: one `tailLines` window, then windows of time |

An interim choice goes under *Alternatives considered* only if it was weighed as an end state.

Its context carries what the README holds and the code does not, since the README is deleted:

- **What the kubelet does**: only the current log file is served; `sinceTime` filters and does
  not seek, so each window costs the node a scan up to its start; `sinceTime` is whole seconds;
  `tailLines` counts parts of lines. These shape the windows.
- **What counts as normal**: the README's table.
- **The failure it answers**: the 100-replica view stuck at *"ran out of budget"* (the README's
  *Why*).

Its decision:

- **Design for normal logs and name the limits**: about 10 MiB a container's log, 1 MiB its loudest
  second, 100 containers a view.
- **Every kubelet line is written as it arrives and grepped on disk**, in a 1 GiB file.
- **Windows sized from nearby rates, by kept lines and by bytes**, and corrected as they are read:
  stopped at four times the plan and replaced from the stop or halved, followed by an older window
  when short.
- **The limits**: 16 MiB and 16 GETs a stream, 64 MiB a query, 1 MiB a second. Each stream
  spends an allowance, the smaller of 16 MiB and 64 MiB ÷ streams, on its first window; the rest
  is a pool its later windows share, each sized from its stream's share and at most 4 MiB. Bytes
  are reserved at four times a window's target and charged, not enforced.
- **The walk follows the fill**: each walk reads its share of 64 MiB plus what its fill
  downloaded.
- **Cut for work, stop only for the abnormal**: a page past its limits is cut; a second past 1 MiB
  stops its stream and cuts the page at that second. Every cut has an edge, so a page always makes
  progress.

Its consequences state the bound a hostile container can push: a window is not stopped in its
last second, so it can read about one second past its reservation, at most 1 MiB. The `tailLines`
window asks for no more lines than its allowance holds at 16 KiB a part (`limits.tailLines`), so
it stays within the allowance, or 160 KiB at the least.

Alternatives considered: windows sized from the time span alone, `tailLines` offsets kept as hints,
a map of probes, keeping only matching lines, refusing a page that runs out of budget.

It links the [log cache ADR](../../adr/2026-10-17-the-log-cache.md) as what it builds on. The log
cache ADR gets no `amended_by`: none of its decisions is replaced. Its *Context* describes the
webview's read before the cache, which is history.

### 2. The logs `CLAUDE.md`

A cold read of `sidecar/internal/services/logs/CLAUDE.md` against the code, fixing what it gets
wrong. Known so far:

- The backend line calls the kubelet backend "kubetail's algorithm". Only the forward read and the
  first `tailLines` window are kubetail's; say so, and point at `windows.go` for the rest.
- "The grep runs where lines are read" must not read as filtering before the write: every line is
  written, and the grep decides what counts toward the page and what the walk passes.
- The windows and limits bullets link the new ADR.

### 3. Security model

The log file's row in `docs/security-model.md` (`services/logs/logstore`) links the new ADR beside
the log cache's for the 1 GiB file. That row is about confidentiality, so what a hostile container
can do to a page's reads gets a row of its own, **By decision**, linking the new ADR: it can stop
its own stream and slow pages, and a window can read about one second, at most 1 MiB, past its
reservation. Its files are `services/logs/limits.go` and `windows.go`; its tests
`TestStreamsReadingAtOnceStayWithinThePage`, `TestAWindowsReservationIsChargedNotEnforced`,
`TestTheTailLinesWindowIsCappedByItsAllowance` and `TestAViewOfOneTooLoudStreamAnswersThePage`.
No protection moves.

### 4. `TODO.md`

One line for each item of the README's *Later*. None is planned elsewhere. Under *Frontend
(webview)*, next to the log viewer's lines:

- Read on through empty pages (the one to do next).
- Say how far a search has got (a small wire change).
- Tell a limit from a failure in the viewer.

Under *Sidecar (Go)*, since it is mostly a new backend:

- Offer kubetail when a stream stops at a limit.

### 5. The retirement

- Repoint the two links to `../backward-log-reads/README.md` in `docs/specs/log-views/4-logsread.md`
  (its *Design* and its *Decisions*) to the new ADR. Reword both passages as current: drop the
  step names (the *Decisions* bullet's title names "backward-log-reads step 3"), "none has
  landed", and "today's backward-read refusal", which step 4 removed.
- `git grep backward-log-reads`; outside `docs/adr/`, nothing else names it. Plain `grep -r` from
  the main checkout also searches the ignored `.worktrees/`.
- #320 stays open. Step 4's PR merged into `wip/log-views`, not `main`, so the fix is not on
  `main` yet. The PR that takes `wip/log-views` to `main` says `Closes #320`.
- Delete `docs/specs/backward-log-reads/`: the README, this file, and steps 1 to 4, which stayed
  while the sequence was open. The sequence shares `wip/log-views`, so this lands before, or with,
  the pull request that takes `wip/log-views` to `main`.

## Tasks

1. **The ADR.** — Planned.
2. **The logs `CLAUDE.md` and the security model.** — Planned.
3. **`TODO.md`.** — Planned.
4. **The retirement.** — Planned.

## Tests

- A read of `sidecar/internal/services/logs/CLAUDE.md` cold describes what the code does, and links
  the new ADR, not this sequence.
- `git grep backward-log-reads` finds nothing outside `docs/adr/`, which may name the sequence as
  history.
- `git ls-files docs/specs/backward-log-reads` prints nothing. The grep cannot show this: only this
  file names the sequence.
- The ADR's context holds the kubelet's four behaviours and the table of normal.
- Every link the change adds or edits resolves.

## When it lands

The sequence is gone. #320 closes with the PR that takes `wip/log-views` to `main`.

## Verification

None beyond reading: no code changes.
