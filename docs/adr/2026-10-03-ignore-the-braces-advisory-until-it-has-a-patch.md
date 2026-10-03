---
title: Ignore the braces advisory until it has a patch
date: 2026-10-03
scope: repo
status: Accepted
---

# Ignore the braces advisory until it has a patch

## Context

`TypeScript · Audit` runs `pnpm audit --audit-level moderate` on every pull request. On 2 October
2026, [GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm) (CVE-2026-93687, high)
was widened to cover `braces` 3.0.3: a deeply nested brace pattern exhausts the stack. 3.0.3 is the
latest release, and the advisory names no patched version, so the audit failed on every branch
and no override could fix it.

`braces` is a dev dependency only: `pnpm why braces --prod` finds nothing. It reaches us through
`micromatch`, under ESLint (`eslint-config-airbnb-extended` → `@next/eslint-plugin-next` →
`fast-glob`) and GraphQL codegen (`@graphql-codegen/cli` → `@graphql-tools/*`). It never ships in
the bundle. The patterns it expands are the globs in our own lint and codegen config, so the
denial of service needs a malicious glob committed to the repository, and it would only stop a
lint or codegen run.

## Decision

Accept the advisory. `pnpm-workspace.yaml` lists it under `auditConfig.ignoreGhsas`, with the
reason beside it. The audit still fails on any other advisory, at moderate and above.

## Alternatives considered

**Override `braces` or `micromatch`.** There is no release to override to: every published
`braces` is in the vulnerable range, and `micromatch` has no line that drops it.

**Audit production dependencies only (`pnpm audit --prod`).** It would pass, but it also stops
reporting every future dev-dependency advisory, including ones that do have a patch. Ignoring this
one advisory keeps the job's reach.

**Drop the dependents.** ESLint and GraphQL codegen are the lint and codegen toolchain; replacing
either to remove a dev-only denial of service costs far more than the risk.

## Consequences

The ignore is per advisory, so a new one still fails the build. It has to be removed by hand once
`braces` ships a fix: `pnpm audit` does not report that an ignored advisory has been patched.

## Revisit when

`braces` publishes a release outside the vulnerable range, or `braces` becomes a production
dependency.
