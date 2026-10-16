---
title: The docs
scope: docs
status: Planned
---

# The docs

**Needs:** step 5, so what is written down is what was built. **Unblocks:** the log viewer
README's own next step, the real client, which reads these docs.

Paths are from the repository root. The design is the README's *When it lands*; this file says
what to write and how to know it is done.

## In short

Each earlier step updated the `CLAUDE.md` lines it changed. This step writes down the reasons,
retires the plan the sequence replaced, and reads the result once as a whole. Nothing in it
changes code.

## What is not in this step

- **No code.** A gap found while reading is an issue, not a fix here.
- **No retirement of the sequence README.** That is the `wip/log-views` pull request's, when the
  real client lands and the fake goes.

## Design

### 1. The ADRs

Two, in `docs/adr/`, each with the README's *Decisions this sequence asks for* as its source:

- **The log cache**: one file per cluster cache; coverage per stream and per chunk, claimed only
  from a read's first returned line; a stream is a runtime instance and `previous` is derived;
  the place and labels are a snapshot; eviction is LRU by read under a byte budget; sources are
  tiered and a range has one source; messages are text, with the dictionary path named for
  later. Alternatives considered: the webview keeping every line, a session table, coverage from
  the requested `sinceTime`, `previous` on the row, a line cap on the grace.
- **A cursor is a position**: what the file holds in place of the per-stream hint the sidecar
  wire note planned, and why `container_id` names the stream. It closes the note.

`docs/security-model.md`'s row from step 3 is repointed at the first ADR.

### 2. The retirement

- `docs/specs/log-viewer/README.md`: *What is left* loses the sidecar half of its first bullet,
  which now reads as the real client alone, and the paragraph above *The wire* no longer says
  the sidecar serves an older shape.
- `docs/specs/log-viewer/notes/the-sidecar-wire.md` is deleted, and every link to it goes with
  it.

### 3. The coherence pass

Read `sidecar/CLAUDE.md`'s *Tools* paragraphs on `services/logs` and LogsView, the directory
table, the janitor paragraphs and the *Patterns* bullets once, as a reader opening them cold,
and fix what the step-by-step edits left: a sentence that describes a step rather than the
result, a term spelled two ways, a claim no test pins. Check that no `CLAUDE.md` links a
superseded ADR.

## Tasks

1. **The ADRs**, and the security row's link.
2. **The retirement** of the note and the README's bullet.
3. **The coherence pass.**

## Tests

None. *Verification* is the check.

## Security

**Widens:** nothing. The security row already exists; this step points it at its ADR.

## When it lands

- This README's row reads Built, with the pull request.

## Verification

Each ADR has `title`, `date`, `scope` and `status: Accepted`, and `docs/adr/README.md` lists
it. `grep -rn the-sidecar-wire docs sidecar src` finds nothing. Every `docs/adr/` link in a
`CLAUDE.md` resolves to a file whose status is not superseded. `make lint` passes, since the
docs change nothing it checks.
