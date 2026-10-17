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

Each earlier step updated the `CLAUDE.md` lines it changed. This step writes down the reasons, adds
the follow-up to `TODO.md`, and retires the sequence. Nothing in it changes code.

## What is not in this step

- **No code.** A gap found while reading is an issue, not a fix here.

## Design

### 1. The ADR

One, in `docs/adr/`, *Backward reads assume normal logs*, with the README's and each step's
*Decisions* as its source: windows sized from nearby rates and corrected as they are read, every
kubelet line written as it arrives and grepped on disk, pages cut for work and streams stopped only
when abnormal, and the 1 GiB file. Alternatives considered: windows sized from the time span alone,
`tailLines` offsets kept as hints, a map of probes, keeping only matching lines.

The log cache ADR gains an `amended_by` pointer to it, since its context names kubetail's windows as
the read it built on.

### 2. `TODO.md`

A line under the log viewer: offer kubetail when a stream stops at a limit. Another for each item of
the README's *Later* not yet planned elsewhere.

### 3. The retirement

Delete `docs/specs/backward-log-reads/`, the README with it. The sequence shares `wip/log-views`, so
this lands before, or with, the pull request that takes `wip/log-views` to `main`.

## Tasks

1. **The ADR and the pointer.** — Planned.
2. **`TODO.md`.** — Planned.
3. **The retirement.** — Planned.

## Tests

- A read of `sidecar/internal/services/logs/CLAUDE.md` cold describes what the code does, and links
  the new ADR, not this sequence.

## When it lands

The sequence is gone.

## Verification

None beyond reading: no code changes.
