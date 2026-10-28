# Step 3B — A threshold goes down only with the label, and up when CI says so

**Files:** `scripts/check-coverage-thresholds.sh` (new), `sidecar/scripts/coverage.go`,
`scripts/coverage-js.go`, `.github/workflows/ci.yml`.
**Prerequisites:** step 2.
**Status:** Planned.
**Pull request:** into `wip/coverage-gates`. Ref #97.

## Goal

Nobody edits a threshold by hand without CI noticing. Lowering one needs the `coverage-lowered`
label, which only a maintainer applies. Raising one is demanded by CI when the measured number
has clearly moved up, so the ratchet stays in the PR history and nothing commits to `main` on its
own.

Read step 1's ADR, the `go-coverage-gate` and `typescript-coverage` jobs, and `coverage.go`'s
`gate` first.

## Down: the label

`scripts/check-coverage-thresholds.sh <base-ref>` reads each of the four `coverage-threshold*`
files at `<base-ref>` and in the working tree. For each one that went down it prints the file and
both numbers. If any went down and `COVERAGE_LOWERED` is not `true`, it exits 1 with:

```
coverage-threshold lowered from 97.27 to 97.00; a maintainer applies the coverage-lowered label and the commit body says why
```

A new job `coverage-thresholds`, *Coverage · Thresholds*, `ubuntu-24.04`, `pull_request` only,
`fetch-depth: 0`, runs it with `origin/$GITHUB_BASE_REF` and
`COVERAGE_LOWERED: ${{ contains(github.event.pull_request.labels.*.name, 'coverage-lowered') }}`.
It joins the required-checks list. A PR is re-run when a label is added, so applying the label
turns the check green; `pull_request`'s default types include `labeled`, which the workflow must
not narrow away.

Create the label once: `gh label create coverage-lowered --description "A coverage threshold goes
down in this PR, and the commit body says why"`.

Tolerance files are not guarded: a tolerance is re-measured, and widening one shows in the
diff as plainly as lowering a threshold. Revisit if one gets edited to pass a PR.

## Up: the demand

In `coverage.go`'s `gate` (the CI union; not `check`, since one OS alone measures below the union)
and in `coverage-js.go`, after the existing gate: when
`pct > threshold + tolerance`, print to stderr and exit 1:

```
coverage rose to 97.51%; raise scripts/coverage-threshold to 97.51
```

The two sides of the window are symmetric, so a lucky run cannot demand a raise any more than an
unlucky one can fail the gate. Both Go thresholds get the demand.

## Tasks

1. **`check-coverage-thresholds.sh`** with a shell test in the same style as the other
   `scripts/*.sh` checks are tested, if any are; else exercised in task 3's acceptance.
2. **The demand** in `coverage.go` and `coverage-js.go`.
3. **The job**, the label, the required-checks list.
4. **Docs:** the *Coverage is gated* paragraphs in `sidecar/CLAUDE.md` and `src/CLAUDE.md`, and
   the root `CLAUDE.md` bullet, now say: *CI tells you when to raise a threshold and to what;
   lowering one needs the `coverage-lowered` label and a reason in the commit body.* The
   `coverage.go` header and the `Makefile` comments follow.

## Acceptance

- A PR that lowers `scripts/coverage-threshold` is red on *Coverage · Thresholds* until the
  label is applied, then green on re-run.
- `gate` with a union above `threshold + tolerance` exits 1 naming the number to set;
  at or below it, passes.
- `make cover-js` on a tree measuring above `threshold + tolerance` exits 1 the same way.

## When it lands

The threshold rule in the three `CLAUDE.md` files is the one above. The ADR's ratchet section
names the label and the demand.
