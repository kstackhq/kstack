# Coverage gates

Each language's coverage is one number, ratcheted to what the last change measured. That
number sits at the top of the run-to-run spread, so a PR that changed nothing the coverage
depends on fails (#37). And it is a poor instruction for an agent writing tests: it does not say
which lines to cover, it rewards tests that run a line without checking it, and nothing stops a
PR from lowering it (#97).

This sequence turns the gate around. **The lines a PR changed are the gate**, reported as
`file:lines` an agent can finish. **The overall number is a backstop** with a tolerance, so it
fails only on a real drop, and only a human can lower it. Code a test cannot reach carries a
reason where a reviewer sees it.

## Decisions

Each is final for this sequence; record them in the ADR when the work lands.

1. **A tolerance, not a lower threshold.** The threshold keeps ratcheting to the measured
   number. The gate fails below `threshold − tolerance`. The tolerance is measured on CI, not
   guessed, and is a second number in a file beside the threshold, so each gate stays two files
   anyone can read.
2. **The changed lines are checked by a script in this repo, not by diff-cover.** `diff-cover`
   would add a Python toolchain to CI and to every machine an agent runs on, plus a Go profile
   converter, to do a job of a few hundred lines: intersect the diff's hunks with a Go profile
   and an lcov file. `sidecar/scripts/coverage.go` already parses profiles. The script is a
   Go `main` package in the `tools/` module, `tools/coverchanged`, so its parsers have tests
   beside them, which a root `scripts/*.go` file cannot.
3. **Locally and in CI it is the same command on the same inputs.** Locally the script measures
   the suites itself; in CI it reads the profiles the coverage jobs already upload. Either way
   it intersects the same diff against the same base, so a PR never fails on a line the agent
   could not see. Locally, a Go file this OS does not compile is listed as *not measured here*
   and does not fail; CI gates it on the union of the three OSes.
4. **Lines, not branches, to start.** A Go profile is per statement block, which already splits
   `if`/`else` arms into blocks. v8's lcov carries branch records too, but a line gate is what
   an agent can act on today; a branch gate is a row in `docs/TODO.md` at landing.
5. **A skipped line carries its reason inline, and CI greps for it.** TypeScript keeps v8's own
   `/* v8 ignore next */`; Go gets `// coverage:ignore`. Both take a reason on the same line. A
   marker added in a PR without one fails CI. The PR template's checklist asks the reviewer to
   read every marker the diff adds, which is where the human approves them.
6. **A label, not CODEOWNERS, guards the threshold files.** CODEOWNERS only bites when branch
   protection requires an owner's review, and a maintainer opening agent-written PRs from their
   own account gets no second human from it. A CI check that fails when a threshold goes down,
   unless the PR carries the `coverage-lowered` label, is self-contained and visible in the
   checks. The same check fails when measured coverage exceeds `threshold + tolerance`, saying
   what to raise the threshold to, so the ratchet stays in the PR history and no bot commits to
   `main`.
7. **The whole Go suite, locally.** A changed line may be covered by another package's test, so
   a per-package run would list lines CI then finds covered, and an agent would write a test it
   did not need. `cover-changed` is run once before a PR opens, so the cost of the full run is
   accepted.
8. **Mutation testing and Rust coverage come later, report-only.** They are the right next step
   for tests that run a line without checking it, but their run times are unknown and they
   block nothing today. Each is a row in `docs/TODO.md` at landing, with the tools to try:
   `cargo-mutants --in-diff`, Stryker's incremental mode with the vitest runner, Gremlins on the
   changed Go packages; `cargo-llvm-cov --lcov` for Rust, which `cover-changed` reads as it reads
   v8's.
9. **Two ADRs, one each.** Step 1 lands on `main` alone with the tolerance ADR. The landing PR
   writes the second, which links the first: the changed lines are the gate, the total is a
   backstop.

## The pieces

| Step | Spec | What it builds | Issue |
| --- | --- | --- | --- |
| 1 | [1-tolerance.md](1-tolerance.md) | A measured tolerance under each threshold, in three or more PRs | Closes #37 |
| 2 | [2-cover-changed.md](2-cover-changed.md) | `make cover-changed`: the changed lines tests do not reach, locally and in CI | Ref #97 |
| 3A | [3A-markers.md](3A-markers.md) | Ignore markers with a reason, and the CI check that reads them | Ref #97 |
| 3B | [3B-threshold-guards.md](3B-threshold-guards.md) | A threshold goes down only with the label, and up when CI says so | Ref #97 |

## Order

```
1 → 2 → 3A
      └ 3B
```

Step 2 needs step 1's `scripts/coverage-js.go` to write the lcov file CI gates on. Steps 3A and
3B need step 2 and can be built at the same time.

**Step 1's pull requests target `main`, not `wip/coverage-gates`.** It stands on its own, and
the flaky failures it fixes hit every PR until it merges. It is at least three PRs, because its
spread workflow must be on `main` before it can run: the tooling and workflow, then a PR per
cheap fix the measurement points at, then the tolerance with its ADR and docs. The last closes
#37. When `wip/coverage-gates` next takes `main` in, delete `1-tolerance.md` and its row above.
Every other step is one pull request into `wip/coverage-gates`, marked `Ref #97`.

## Closing the issues

GitHub closes an issue from `Closes #n` only when the commit or PR reaches `main`. A PR into
`wip/coverage-gates` closes nothing, and a squash of the `wip` branch drops its commits'
messages, so:

- Step 1's last PR, the one adding the tolerance, says `Closes #37` in its description and
  squash commit. Its earlier PRs say `Ref #37`.
- The landing PR from `wip/coverage-gates` into `main` says `Closes #97` in its description and
  its squash commit. Steps 2, 3A and 3B say `Ref #97`.

## When it lands

- Fold what is now true into the root `CLAUDE.md` (*Testing conventions*: run `make test-changed`
  while working and `make cover-changed` until it prints nothing before opening a PR; what a
  marker is and that it needs a reason; the threshold rule), `sidecar/CLAUDE.md` (*Coverage is
  gated*) and `src/CLAUDE.md` (*Coverage is gated*).
- Write the ADR *The changed lines are the gate and the total is a backstop*, recording
  decisions 2 to 9 and linking step 1's ADR for the tolerance. Link it from the three
  `CLAUDE.md` files.
- Add `docs/TODO.md` rows for the branch gate and for mutation testing and Rust coverage
  (decisions 4 and 8), each naming the tools to try.
- Post the write-up #97's *Done when* asks for as a comment on the issue: a link to the ADR and
  the list of ideas adopted and dropped.
- Delete this directory.
