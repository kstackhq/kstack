---
title: Lint the webview with oxlint, every rule an error
date: 2026-11-05
scope: frontend
status: Superseded by [2026-11-05-lint-the-webview-with-oxlints-categories-and-a-chosen-rule-list](2026-11-05-lint-the-webview-with-oxlints-categories-and-a-chosen-rule-list.md)
---

# Lint the webview with oxlint, every rule an error

## Context

`pnpm lint` ran ESLint over `eslint-config-airbnb-extended`, with typescript-eslint's project
service, the React compiler's rules from `eslint-plugin-react-hooks`, and a `no-restricted-syntax`
block that bans HTML sinks and template engines in `src/`
([#18](https://github.com/kstackhq/kstack/issues/18)). It took about 23 seconds. We already
formatted with oxfmt, from the same toolchain as oxlint.

Most of the code here is written by agents, which run the lint after each change. A slow lint
gets run less. An agent also treats a warning as noise, and a disable comment as a way out.

oxlint cannot load airbnb, and it has no `no-restricted-syntax`. Its JS plugins take ESLint's
plugin API, AST selectors included. Its type-aware rules run through `oxlint-tsgolint`. It ships
native `react/*` ports of the compiler's rules.

## Decision

**`.oxlintrc.json` is the lint.** oxlint's `correctness` category is on. The config then lists by
name each airbnb rule oxlint has and the category lacks, with airbnb's options where they differ
from oxlint's defaults. airbnb's warnings are errors, and `denyWarnings` fails the run on any
warning, so no rule is advisory.

**Type-aware rules run on every lint** (`typeAware`). They are airbnb's, plus the promise and
exhaustiveness rules: `no-floating-promises`, `no-misused-promises`, `await-thenable`,
`switch-exhaustiveness-check`, `no-base-to-string`, `no-deprecated` and `no-misused-spread`. A
`default` counts as exhaustive, since airbnb's `default-case` already asks every switch for one.

**The compiler's rules come from `eslint-plugin-react-hooks`**, loaded as the JS plugin
`react-hooks-js`, with `recommended-latest`'s rules, every one an error. The native `react/*` ports
are off.

**Kstack's rules live in `lint/oxlint-plugin.js`**: `kstack/no-restricted-syntax`, ESLint's rule by
the same name, carries the HTML-sink and template-engine bans and airbnb's four syntax bans;
`kstack/disable-needs-reason` fails a disable comment without a `-- reason`. Unused disable
comments fail too, and only `oxlint-disable` comments are read.

**oxfmt sorts imports** (`sortImports` in `.oxfmtrc.json`): packages, then `@/`, then relative
paths, a blank line between groups.

## Alternatives considered

- **Keep ESLint for the bans alone.** That means two linters and two configs to keep in step. A
  selector rule is a dozen lines in a JS plugin.
- **oxlint's native compiler rules.** On this code, `react/immutability` reports a variable
  reassigned inside an effect's closure, which the compiler accepts. The plugin is the compiler
  the Vite transform runs, so the two agree. It costs about five seconds of the lint.
- **`oxlint-migrate`'s output as the config.** It reproduces airbnb as it stood. We wanted the
  stricter type-aware rules and a config whose every entry is a choice.
- **Every type-aware rule.** `strict-boolean-expressions`, `prefer-readonly-parameter-types` and
  `no-unnecessary-condition` report thousands of lines in code we consider correct.
- **The plugin in TypeScript.** oxlint loads a plugin with Node's own loader, which reads a `.ts`
  file only where type stripping is on. The sandbox's Node 22 has it off. A JS file with
  `// @ts-check` loads under any Node and is still type-checked by `tsconfig.node.json`.

## Consequences

`pnpm lint` takes about 7 seconds, most of it the compiler plugin.

airbnb's rules oxlint lacks are gone. Those that applied to this code: `import/order` (now the
formatter's), `@typescript-eslint/naming-convention`, `react/jsx-no-bind`,
`react/destructuring-assignment`, `react/no-unused-prop-types`, `no-undef-init`,
`import/no-useless-path-segments`, `import/no-relative-packages`, and the `@stylistic` spacing
rules. The rest apply to class components, prop-types or sloppy mode.

`eslint-plugin-react-hooks` keeps `eslint` installed as its peer, which nothing runs.

A rule added to the config either passes on the code or comes with its fixes. A disable comment is
a claim a reviewer reads.

## Revisit when

oxlint's native compiler rules agree with the compiler on this code, or oxlint gains
`no-restricted-syntax`.
