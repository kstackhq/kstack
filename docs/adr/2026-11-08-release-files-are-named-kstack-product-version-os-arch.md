---
title: Release files are named kstack-<product>-<version>-<os>-<arch>
date: 2026-11-08
scope: repo
status: Accepted
---

# Release files are named kstack-\<product\>-\<version\>-\<os\>-\<arch\>

## Context

Kstack has several products under one name: the CLI, the desktop app and, later, a mobile app. The
CLI's release named its files `kstack-cli-<version>-<os>-<arch>.{tar.gz,zip}`. The desktop release
attached Tauri's default names, which follow `productName` (`Kstack`) and differ per format:
`Kstack_<version>_amd64.deb`, `Kstack-<version>-1.x86_64.rpm`, and the `.dmg` and `.msi` each in
their own style. Its SBOM was `kstack-<version>.cdx.json` and its source archive
`embedded-sources.tar.gz`. No release had shipped, so a rename had nothing to migrate
([#801](https://github.com/kstackhq/kstack/issues/801)).

## Decision

Replaces the `Kstack_*` package files in
[The desktop app is Kstack, and its binary kstack-desktop](2026-11-07-the-desktop-app-is-kstack-and-its-binary-kstack-desktop.md),
which kept Tauri's names.

Every release file is named `kstack-<product>-<version>-<os>-<arch>.<ext>`, all lowercase. `<os>` is
`linux`, `darwin` or `windows` and `<arch>` `amd64` or `arm64`, the CLI's words. The macOS app is one
universal build, so its `<arch>` is `universal`. `<version>` is the release's SemVer, also for the
RPM and MSI, whose package versions differ. A file that covers every platform drops `-<os>-<arch>`.
`SHA256SUMS` and `SHA256SUMS.asc` keep their names.

A desktop release attaches:

- `kstack-desktop-<version>-linux-{amd64,arm64}.{deb,rpm}`
- `kstack-desktop-<version>-darwin-universal.dmg`
- `kstack-desktop-<version>-windows-{amd64,arm64}.msi`
- `kstack-desktop-<version>.cdx.json`
- `kstack-desktop-<version>-embedded-sources.tar.gz`
- `SHA256SUMS`, `SHA256SUMS.asc`

`productName` stays `Kstack`, so Tauri still names the files it builds after it.
`release-desktop.yml` renames each one as it collects it: the `.deb`, `.rpm` and `.dmg` in their
build jobs, the `.msi` after SignPath has signed it, since SignPath's artifact configuration matches
Tauri's name. The release job fails unless its files are exactly the list above.

**The Linux packages' own name stays `kstack`.** Tauri's bundler (tauri-cli 2.12) writes the
`.deb`'s `Package:` and the `.rpm`'s `Name:` as `productName` in kebab case and offers no setting for
either. A `productName` of `kstack-desktop` in `tauri.linux.conf.json` would name them, but it also
names the desktop entry (`kstack-desktop.desktop`), its `Name=`, and the resource folder, which this
decision keeps. So `apt remove kstack` and `dnf remove kstack` remove the desktop app.

## Alternatives considered

- **Keep Tauri's names.** Four styles in one release, none saying which product it holds, and no
  template the CLI's downloader could build a name from.
- **Rebuild the `.deb` and `.rpm` to set their package name.** `dpkg-deb -R`, edit the control file,
  `dpkg-deb -b` works for the `.deb`; the `.rpm` has no such round trip without a spec. Two
  hand-repacked formats is more release machinery than a name that no packaged CLI needs yet.

## Consequences

- One template builds every file name, for the workflows and for `kstack app`'s downloader.
- A later mobile app's files are `kstack-mobile-…`.
- A packaged CLI cannot take the package name `kstack` while the desktop app holds it. Packaging the
  CLI means taking another name, or repacking the desktop's `.deb` and `.rpm`.
