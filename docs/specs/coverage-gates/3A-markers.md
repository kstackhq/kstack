# Step 3A — Ignore markers with a reason

**Package:** `tools/coverchanged` (`ignore.go`), `.github/pull_request_template.md`.
**Prerequisites:** step 2.
**Status:** Planned.
**Pull request:** into `wip/coverage-gates`. Ref #97.

## Goal

A line a test cannot reach is marked, the mark carries a reason, `cover-changed` skips it, and a
marker without a reason fails the same command. The reviewer reads the reasons in the diff; that
is the human approval.

Read `tools/coverchanged/main.go`, `vite.config.ts`'s `coverage` block and the
`@vitest/coverage-v8` documentation on ignore hints first.

## The markers

**Go** (ours; `coverage.go` keeps counting the lines, so the backstop stays honest):

```go
return fmt.Errorf("close: %w", err) // coverage:ignore a temp file's Close cannot be made to fail
```

```go
// coverage:ignore-start the signal arms run only when the OS delivers one mid-test
...
// coverage:ignore-end
```

The inline form ignores its own line. The pair ignores the lines between, and must close in the
same file; an unclosed start is an error.

**TypeScript** keeps v8's own hints, which drop the code from the report, so `cover-changed`
sees nothing: `/* v8 ignore next */`, `/* v8 ignore if */`, `/* v8 ignore else */`,
`/* v8 ignore start */` … `/* v8 ignore stop */`. The reason follows ` -- ` inside the comment:

```ts
/* v8 ignore next -- the fallback timer fires only when requestAnimationFrame is missing */
```

Task 1 confirms vitest honours that form; if it does not, the reason is a `//` comment ending
the same line, and this spec says which.

## The check

`cover-changed` looks at every changed line (step 2's set, test files included). A line that
carries a marker and no reason, or a reason under three words, is listed:

```
sidecar/internal/run/shell.go:41    coverage:ignore without a reason
```

and fails the command like an uncovered line. The three-word floor keeps `// coverage:ignore wip`
out; it is a floor, not a judgement. Judgement is the reviewer's: the PR template's checklist
gains

```
- [ ] Every coverage marker this PR adds carries a reason I agree with
```

Markers already on `main` are not re-checked; only changed lines are.

## Tasks

1. **Confirm the v8 reason form** with a one-line test in a scratch component; record the
   answer here.
2. **`ignore.go`:** find markers on changed lines, build the ignored line set per file, check
   reasons. Tests.
3. **Wire it:** `main.go` drops ignored lines before intersecting and prints the reason failures
   with the others.
4. **The PR template line.**
5. **Docs:** the root `CLAUDE.md` *Testing conventions* gains one bullet on markers: the two
   forms, the reason, that the reviewer reads them.

## Acceptance

- A changed Go line under `// coverage:ignore <reason>` is not listed; the same line with the
  reason removed is listed as *without a reason* and the command exits 1.
- A changed TypeScript line under `/* v8 ignore next -- <reason> */` is not listed.
- `cd tools && go test ./...` passes.

## When it lands

The root `CLAUDE.md` bullet stays; the sidecar guide's *What stays uncovered is deliberate* list
becomes markers on those lines in a follow-up PR, or stays as prose if marking `main()` reads
worse.
