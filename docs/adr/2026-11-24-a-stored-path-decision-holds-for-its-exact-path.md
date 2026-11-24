---
title: A stored PATH decision holds for its exact path
date: 2026-11-24
scope: sidecar
status: Superseded by [2026-11-24-the-sandbox-path-is-one-setting-the-user-writes](2026-11-24-the-sandbox-path-is-one-setting-the-user-writes.md)
---

# A stored PATH decision holds for its exact path

## Context

The frozen `PATH` keeps each folder the user approved or removed, as a `PathEntry` whose `Target`
is the folder the entry resolved to. `realpath.Canonical` stores it as the disk spells it.

A stored `Target` that differed from a fresh folder in case alone was read off the disk, with no
link followed, to tell whether it was the same folder; if it was, the decision carried over. That
lookup ran on every sync, on every Include and on every run's check. It brought its own failure
modes: a sync whose lookup failed wrote nothing, and an Include read the disk before taking the
list's lock, retried while a sync changed the list, and had two refusals of its own.

None of it protected anything. A stored spelling changes only when the user renames the folder.
And Kstack adds a folder unasked only when every sandboxed command can already read it, so a
removed folder that comes back exposes nothing new.

## Decision

A stored `PATH` decision holds for the exact path it was made for, compared as text. A folder in
another spelling is a new folder: the sync files it as any new entry, adopted unasked only when it
is open and unshared, else pending until the user includes it. An Include drops only the removals
kept under other `Dir`s whose `Target` is exactly the one included.

What keeps sandboxed commands safe stays: each run checks every folder afresh (`RunCheck`), against
the filter and, for an approval, against the exact `Target` approved.

This replaces the paragraph of [two mounts of one folder are one `PATH`
folder](2026-11-23-two-mounts-of-one-folder-are-one-path-folder.md) that carried a decision to its
folder in another case. Its duplicate rule stands.

## Alternatives considered

**Keep the case-only lookup.** It saved one Include after a rename, at the cost of a disk read on
every sync and Include, two refusals the user could not act on, and platform code to stat a path
with no link followed.

## Consequences

Renaming a folder that holds an approval asks again, unless Kstack would add it unasked. A removal
does not carry to another spelling, and the folder is adopted only if every run can already read
it.

`realpath` no longer looks a file up without following links; nothing else needed it.

## Revisit when

A folder's spelling starts changing without the user's doing, so the prompts it brings back are
noise.
