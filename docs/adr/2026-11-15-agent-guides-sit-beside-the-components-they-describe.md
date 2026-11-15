---
title: Agent guides sit beside the components they describe
date: 2026-11-15
scope: repo
status: Accepted
---

# Agent guides sit beside the components they describe

## Context

`src/CLAUDE.md` had grown to 89 KB. Claude Code loads a `CLAUDE.md` when a file in its directory or below is read, so any session that opened a webview file loaded all of it: the chat, the dashboard, the log viewer, the omnibox and the settings dialog. A change to a table cell paid for the approval request's traps. The sidecar had already split its guide by package, one `CLAUDE.md` per larger package, but the webview had no such seams in its docs.

The webview's code splits two ways. `src/components/` has one folder per area (`chat/`, `dashboard/`, `logs/`, `cluster/`, `settings/`, `shell/`, `primitives/`). `src/lib/` is flat: each subsystem's hooks and providers sit side by side with every other subsystem's.

## Decision

`src/CLAUDE.md` holds what crosses the webview: the security invariants, routing and component areas, errors, window state, theming and storage, GraphQL, codegen, tests, and lint and build. Each larger subsystem has its own guide in its components folder (`src/components/<area>/CLAUDE.md`), with an `AGENTS.md` beside it, and a byte budget in `scripts/claude-md-budget`.

Each guide also owns its subsystem's `src/lib/` modules and lists them in its first paragraph. A nested guide loads only for files in its own folder, so `src/CLAUDE.md`'s *Guides* table tells an agent to open the subsystem's guide before changing those modules. A guide about code in another area's folder (the cluster guide covers `shell/context-omnibox/`) says so in the guide that does load, which points to it.

The security invariants stay in `src/CLAUDE.md`, whatever subsystem they name, so they load for every webview file.

## Alternatives considered

- **Keep one `src/CLAUDE.md`.** Every webview session pays for every subsystem, and the file keeps growing with each feature. Its budget was already the largest in the repo.
- **Put the guides beside `src/lib/` modules.** `src/lib/` is flat, so a guide there would load for every module. Giving each subsystem a `src/lib/` folder would move most of the webview's imports for the sake of the docs.
- **Keep the subsystem guides under `docs/` and link them from `src/CLAUDE.md`.** Nothing would load them automatically. An agent would open one only if it noticed the link, which is the failure the split is meant to remove.

## Consequences

A session that touches one subsystem loads `src/CLAUDE.md` (about 27 KB) and that subsystem's guide, not 89 KB.

An agent changing a `src/lib/` module must open the subsystem's guide itself. That is the cost of a flat `src/lib/`, and the *Guides* table and each guide's module list are what pay it. A new module joins the list of the guide that owns it, in the same change.

A rule that holds across subsystems belongs in `src/CLAUDE.md`. Copying it into two guides lets the copies drift apart.

## Revisit when

`src/lib/` is split into a folder per subsystem, so each guide can sit beside its modules.
