---
title: Put the sandbox's node_modules on the overlay, beside Cargo's target dir
date: 2026-10-27
scope: repo
status: Superseded by [2026-10-28-share-one-pnpm-store-across-sandbox-worktrees](2026-10-28-share-one-pnpm-store-across-sandbox-worktrees.md)
---

# Put the sandbox's node_modules on the overlay, beside Cargo's target dir

Supersedes [Sandbox build-output locations](2026-09-03-sandbox-build-output-locations.md), which
kept `node_modules` and the pnpm store on the host share under `.sandbox-linux/`.

## Context

Development happens inside a Linux sandbox that bind-mounts this repo from a macOS host, so both
platforms share every build-output path. `scripts/sandbox-dev-setup.sh` keeps them apart: a
sandbox-only bind mount covers `node_modules` and `dist`, and environment variables move the pnpm
store and Cargo's `target/`. Cargo's `target/` already sits on the sandbox's overlay, because the
host share is not coherent under dense writes.

`node_modules` stayed on the share to save the overlay's 20G. Reads there work but are slow, and a
JS run reads `node_modules` constantly: vitest reloads each test file's imports in a fresh
environment. The full coverage run on PR #452 took 161.5s in the sandbox against 86.6s in CI, with
setup at 111.8s against 18.1s. `window.test.ts`'s 300-seed walk timed out locally under coverage.

Two more facts shaped the decision:

- **pnpm cannot hard-link into a bind-mounted `node_modules`.** `link(2)` returns `EXDEV` across
  two mounts even when both show the same filesystem. pnpm then copies from the store, wherever the
  store sits. The share layout copied too.
- **Every worktree shares one sandbox, and so one persistent env file.** A per-worktree
  `PNPM_CONFIG_STORE_DIR` pointed every worktree at whichever one ran the setup script last.

## Decision

`node_modules` is bind-mounted from the overlay, one directory per worktree:
`/home/agent/kstack-build/worktrees<repo path>/node_modules`. `KSTACK_SANDBOX_OVERLAY_ROOT`
overrides `/home/agent/kstack-build`, which also holds Cargo's `target/`. The script compares the
mount's device and inode with the source's, so a re-run replaces a mount left by the older layout.

The pnpm store stays on the share, as one store for every worktree in the main checkout's
`.sandbox-linux/pnpm-store`. The overlay would not make it hard-link, and it is read only at
install time. `dist` stays on the share under `.sandbox-linux/`.

The script warns when the overlay has less than 2G free.

## Alternatives considered

**The store on the overlay, as #456 proposed.** Rejected: pnpm copies from it anyway, so it
spends about 400M of the overlay and buys nothing. A store inside `node_modules` would hard-link,
but each worktree would carry its own, and a `node_modules` purge would wipe it.

**One `node_modules` on the overlay for every worktree.** Rejected: worktrees can be on different
lockfiles, and an install in one would rewrite another's dependencies.

**Leaving `node_modules` on the share.** Rejected: it doubles the JS suite's time and makes
time-bounded tests fail locally while passing in CI.

## Consequences

JS tooling reads its dependencies from the overlay. Each worktree's `node_modules` costs about
420M of the overlay and is wiped when the sandbox is recreated. A fresh install then copies from
the store, which survives on the share, rather than downloading.

The overlay is now the routine constraint. Cargo's `target/` and Go's build cache are each several
gigabytes, and a removed worktree's `node_modules` stays under `worktrees/` until deleted by hand.
The free-space warning names these.

The script must still run before any install or build in the sandbox, which the `SessionStart`
hook does. The first re-run after this change unmounts the old `node_modules`. Run
`pnpm install` after it, since the new directory starts empty.

## Revisit when

The sandbox's overlay grows past 20G, or the host share becomes fast enough for dense reads.
