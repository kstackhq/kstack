---
title: Linux ships only the .deb and .rpm, and they carry bwrap's and pasta's source
date: 2026-11-07
scope: repo
status: Accepted
---

# Linux ships only the .deb and .rpm, and they carry bwrap's and pasta's source

## Context

A desktop release attached a `.deb`, an `.rpm` and an AppImage for each Linux architecture, and
`bubblewrap-<version>.tar.xz` and `passt-<version>.tar.xz` beside them
([Kstack ships its own bwrap](2026-09-28-kstack-ships-its-own-bwrap.md),
[Kstack ships its own pasta](2026-10-04-kstack-ships-its-own-pasta.md)).

The AppImage's bwrap and pasta sit under a mount no AppArmor profile names, so on Ubuntu 23.10 and
24.04 it has no sandbox and no network. The tarballs are the source of the two binaries, which
bubblewrap's LGPL-2.0-or-later and passt's GPL-2.0-or-later require be offered with them. Nobody
needs them to install Kstack, and their names do not say what they are.

## Decision

Linux ships the `.deb` and the `.rpm` alone. `release-desktop.yml` builds `--bundles deb`, then the
RPM, and attaches no source tarball.

Each package carries the tarball bwrap and pasta were built from under `/usr/share/doc/kstack/`,
beside their licenses: `bubblewrap-source.tar.xz` and `passt-source.tar.xz`, about 400 KB each.
`make bwrap` and `make pasta` write them under those names, so `tauri.linux.conf.json` names no
version. The release's Ubuntu 24.04 check reads both from the installed `.deb`.

This replaces the release-assets part of both ADRs above: the release no longer attaches bwrap's or
pasta's source, and there is no AppImage.

## Alternatives considered

- **Keep attaching the tarballs to the release.** The source is offered once per release, but as
  two assets most users will not recognise, apart from the packages that need it.
- **Link to upstream's tarballs from the packages' docs.** The packages stay smaller, but passt's
  snapshots are generated on request, so the bytes behind a link can change or go, and the offer
  then depends on a server Kstack does not run.

## Consequences

Each Linux package grows by about 800 KB, and each carries the exact source of its binaries
however it is passed on. A release lists only installers, the SBOM and the signed `SHA256SUMS`.

Ubuntu 23.10 and 24.04 users have no build without a sandbox to pick by mistake. `kstack app` on
Linux still installs an AppImage, and cannot install from a real release until it installs a
package (`docs/TODO.md`, *CLI*).
