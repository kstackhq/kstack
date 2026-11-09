---
title: app.db is copied before a migration runs, and the caches are not
date: 2026-11-09
scope: sidecar
status: Accepted
---

# app.db is copied before a migration runs, and the caches are not

## Context

`app.db` holds the user's clusters, chats and memories, and nothing rebuilds it. `sqlitemigrate`
migrates it in place, so a user who upgrades past a migration and goes back to the build they
came from finds it refused, with no copy of the file as it was. The [version-stamp
ADR](2026-10-29-each-persisted-format-carries-its-own-version-stamp.md) left "a backup before the
first migration past `0001`" to the first release. `desktop/v0.0.1` shipped with one migration per
store, so no upgrade has migrated anything yet.

## Decision

**`sqlitemigrate.Apply` takes a backup path.** When the file already has a version and a migration
is pending, it writes the file as it stands to that path with `VACUUM INTO` before the first
pending migration runs, replacing the copy the previous upgrade left. A fresh file, or one with
nothing pending, takes no copy. The copy is owner-only. A copy that cannot be written stops the
upgrade before any migration runs.

**`appdb` passes `app.db.bak`; the caches pass none.** A user going back renames `app.db.bak` over
`app.db`. A cache can reach gigabytes, and the cluster or the kubelet rebuilds one at the price of
the history it held. `beehive.db` has its own runner and no copy; a downgrade past a beehive bump
upgrades again.

## Alternatives considered

**Defer the backup to the first migration past `0001`.** It never fires while each store has one
migration, so it looks like dead code. Rejected: it is tested against `sqlitemigrate`'s own test
migrations today, and the PR that writes a migration in a hurry is the wrong place to write its
safety net.

**Copy the file with the filesystem.** Rejected: a WAL database's committed frames may sit in the
`-wal` file, so a byte copy of `app.db` alone can miss them. `VACUUM INTO` reads one consistent
snapshot whatever the journal mode.

**Back up the caches too.** Rejected for their size, and because each is rebuilt from the cluster.

## Consequences

`app.db.bak` sits beside `app.db` after the first upgrade that migrates it, at most as large as the
file was. A user who downgrades restores it by hand; there is no UI for it.

## Revisit when

A migration is slow enough on a large `app.db` that the copy's time shows at startup, or users
need a restore without touching files.
