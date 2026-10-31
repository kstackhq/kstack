---
title: Ignore the minimatch advisory until its dependents take the fix
date: 2026-10-31
scope: repo
status: Accepted
---

# Ignore the minimatch advisory until its dependents take the fix

## Context

`TypeScript · Audit` runs `pnpm audit --audit-level moderate`. On 31 October 2026 it failed on
`main` ([#581](https://github.com/kstackhq/kstack/issues/581)) with
[GHSA-g3hv-75wg-m249](https://github.com/advisories/GHSA-g3hv-75wg-m249) (moderate): an
attacker-supplied glob grows `minimatch`'s pattern list during preprocessing, then costs quadratic
time. Every release below 11.0.1 is in range.

We carry `minimatch` 3.1.5 and 10.2.6. 3.x comes in through ESLint 9 itself and the
`eslint-plugin-react`, `jsx-a11y` and `import` plugins; 10.x through `@typescript-eslint`,
`eslint-plugin-import-x` and `graphql-config`. All of it is dev-only: `pnpm why minimatch --prod`
finds nothing. The patterns it matches are the globs in our own lint and codegen config.

The only patched line, 11.x, is a rewrite: it drops the regular-expression engine and with it
`makeRe` and the `AST` export, and its default export differs from 3.x's. It was published on
27 October, inside the seven-day `minimumReleaseAge`.

## Decision

Accept the advisory. `pnpm-workspace.yaml` lists it under `auditConfig.ignoreGhsas`, with the
reason beside it. The audit still fails on any other advisory, at moderate and above.

## Alternatives considered

**Override `minimatch` to `^11.0.1`.** It forces a breaking major onto ESLint and its plugins,
which were not written against it, to fix a denial of service only our own config can trigger. It
would also need a `minimumReleaseAgeExclude` entry for a four-day-old rewrite.

**Audit production dependencies only (`pnpm audit --prod`).** It stops reporting every future
dev-dependency advisory, including ones with a compatible patch.

## Consequences

The ignore is per advisory, so a new one still fails the build. It has to be removed by hand:
`pnpm audit` does not report when an ignored advisory stops applying.

## Revisit when

Every dependent accepts `minimatch` 11 (or a patched 3.x or 10.x ships), or `minimatch` becomes a
production dependency.
