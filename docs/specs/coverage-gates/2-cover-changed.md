# Step 2 — `make cover-changed`

**Package:** `tools/coverchanged` (new, in the `tools/` Go module), `Makefile`,
`scripts/test-changed.sh`, `.github/workflows/ci.yml`.
**Prerequisites:** step 1 on `main`.
**Status:** Planned.
**Pull request:** into `wip/coverage-gates`. Ref #97.

## Goal

One command lists the changed lines tests do not reach, as `file:lines`, and prints nothing when
the change is fine. CI runs the same command on every PR and blocks it on the same list.

Read `scripts/test-changed.sh` (how the changed set and the base are found),
`sidecar/scripts/coverage.go` (the profile format and the generated-file list), `vite.config.ts`'s
`coverage` block, and #97 first.

## Where it lives

`tools/` is a Go module with a `go.mod`, so the parsers get tests beside them, as every unit
does; a root `scripts/*.go` file cannot. The command is `go run ./coverchanged` from `tools/`,
and the recipe is:

```make
cover-changed:
	cd tools && go run ./coverchanged $(ARGS)
```

Files: `main.go` (flags, measure, print), `diff.go` (the changed lines), `goprofile.go`,
`lcov.go`, `ignore.go` (step 3A fills it), each with a `_test.go`. `make test-go` and
`test-changed.sh` (when `tools/` changes) run `cd tools && go test ./...`.

## The changed lines

The base is as `test-changed.sh` finds it: `BASE`, else `upstream/main` when it exists, else
`origin/main`; `-base <ref>` overrides it, which CI uses. The changed lines are the `+` lines of
`git diff -U0 <merge-base>` over committed, staged and unstaged changes, plus every line of an
untracked file. Only these files count:

- `sidecar/**/*.go`, not `_test.go`, not `coverage.go`'s generated list, not a file with
  `//go:build ignore`.
- `src/**/*.ts` and `*.tsx`, not `*.test.ts(x)`.

A deleted line is nothing to check. A moved line is a changed line; that is the PR's own code.

## The measurements

Locally the script measures both suites itself, then intersects:

- Go: `cd sidecar && go run scripts/coverage.go -out <tmp>`. The whole suite, both tag variants
  (README decision 7).
- TypeScript: `pnpm vitest run --coverage --coverage.reporter=lcovonly
  --coverage.reportsDirectory=<tmp>`.

In CI it runs no tests and reads what the coverage jobs uploaded: `-linux`, `-darwin`, `-windows`
profiles, unioned as `coverage.go` unions them, and `-lcov <file>`. Each language's inputs are
optional, since a PR that touched only one language runs only that language's jobs; a changed
file of a language with no measurement given is an error, not a pass.

A Go profile block is `file:line.col,line.col stmts count`. A changed line is **not covered** when
at least one block spans it and every block spanning it has count 0 in every profile given. A line
no block spans is not a statement and is skipped. An lcov `DA:line,count` of 0 is not covered; a
line with no `DA` record is skipped.

A changed Go file absent from every profile given was not compiled by the OS that measured.
Locally it is printed as `not measured on <GOOS>` and does not fail; in CI, the union holds every
OS, so it is an error. A changed TypeScript file absent from the lcov is one `vite.config.ts`
excludes, and is skipped.

## The output

One line per contiguous range, sorted by path then line, to stdout:

```
sidecar/internal/agent/chat/turn.go:88-91    not covered
src/lib/chat-outbox.tsx:212                  not covered
sidecar/internal/run/shell_windows.go        not measured on linux
```

Then, when any line was not covered, one line to stderr and exit 1:

```
cover-changed: 5 changed lines are not covered; add tests that check their behaviour
```

Nothing and exit 0 when every changed line is covered. No percentages.

## CI

- `typescript-coverage`: `coverage-js.go` also writes `lcovonly`; the job uploads it as
  `typescript-coverage`.
- A new job `coverage-changed`, *Coverage · Changed lines*, `ubuntu-24.04`, on `pull_request`
  only: checkout with `fetch-depth: 0`, download `go-coverage-*` and `typescript-coverage`, run
  `go run ./coverchanged -base origin/$GITHUB_BASE_REF` with whichever inputs downloaded. It
  `needs` `go-coverage` and `typescript-coverage` and runs when either succeeded and neither
  failed (`!cancelled()` plus a result check), so a one-language PR is still gated.
- Add it to the job every PR must pass (the `needs` list near the end of `ci.yml`).

## Tasks

1. **`diff.go`:** the base rule, the hunks, the file filters. Tests on a fixture repository
   made in `t.TempDir()`.
2. **`goprofile.go` and `lcov.go`:** parse, union, span. Tests on inline fixtures.
3. **`main.go`:** flags, local measurement, CI inputs, the output. Add `ignore.go` as an empty
   seam with a doc comment pointing at step 3A.
4. **`Makefile`, `test-changed.sh`, `test-go`.**
5. **CI:** the lcov upload, the job, the required-checks list.
6. **Docs:** the `cover-changed` recipe comment; the root `CLAUDE.md` *Testing conventions* bullet
   gains *run `make cover-changed` until it prints nothing before opening a PR*.

## Acceptance

- `cd tools && go test ./...` passes.
- On a branch that adds an untested function, `make cover-changed` lists its lines and exits 1;
  after a test is added it prints nothing and exits 0.
- A PR into `wip/coverage-gates` with an untested line is red on *Coverage · Changed lines*
  with the same `file:lines` the command printed locally.

## When it lands

The root `CLAUDE.md` *Testing conventions* and the three coverage paragraphs name the command.
`docs/TODO.md` gains the branch-gate row (README decision 4).
