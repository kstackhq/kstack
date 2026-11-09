---
title: host.json keeps a newer build's keys and version through a write
date: 2026-11-09
scope: host
status: Accepted
---

# host.json keeps a newer build's keys and version through a write

## Context

`host.json` (`src-tauri/src/host_file.rs`) parses into `HostFile` and writes that back, stamping
`schemaVersion` 1. A key this build does not know, and a higher version, are lost at the first
write. A newer build that adds a setting loses it as soon as the user goes back to an older build
and changes the colour scheme. The [version-stamp
ADR](2026-10-29-each-persisted-format-carries-its-own-version-stamp.md) accepted this as a gap,
since nothing had shipped and the file held one setting. `desktop/v0.0.1` has shipped, and the fix
helps only once it is in the older build, so each release without it is one more that drops a
newer build's settings.

## Decision

**A write keeps the keys this build does not know and the higher `schemaVersion`.** `HostFile`
carries a flattened `unknown` map, and `update` stamps the larger of the file's version and
`CURRENT_SCHEMA_VERSION`. This is what `settings.json` already does on the sidecar side, so both
preference files behave alike. `v0.0.1` is the one build that drops them.

A file an older build cannot parse still reads as defaults, and its next write replaces it. So a
new choice for an existing setting is a new field, never a new value of the old one.

## Alternatives considered

**Leave it as an accepted gap.** Rejected: the cost lands on users only after a newer build adds a
setting, and by then the older builds in the field are the ones that drop it.

## Consequences

The webview's `window.__KSTACK_HOST__` and the `host-file-updated` broadcast carry the unknown keys
too. The webview reads only the fields it knows.
