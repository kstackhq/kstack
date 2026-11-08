---
title: kstack app installs the newest desktop release with the platform's installer
date: 2026-11-08
scope: repo
status: Accepted
---

# kstack app installs the newest desktop release with the platform's installer

## Context

`kstack app` opens the desktop app and installs it first when it is missing. It was written
against a placeholder download URL and for installers no release ships: an AppImage copied into
`~/Applications` on Linux and a per-user NSIS setup on Windows. Its rule was that installs are per
user and need no admin.

A desktop release now attaches `kstack-desktop-<version>-linux-{amd64,arm64}.{deb,rpm}`,
`-darwin-universal.dmg` and `-windows-{amd64,arm64}.msi`
([release file names](2026-11-08-release-files-are-named-kstack-product-version-os-arch.md),
[Linux ships only the .deb and the .rpm](2026-11-07-linux-ships-only-the-deb-and-the-rpm.md)).
The `.deb`, the `.rpm` and Tauri's MSI all install system-wide and need admin rights. The file
names carry the version, and the CLI and the desktop app are released apart, so the CLI cannot
know the version itself ([#802](https://github.com/kstackhq/kstack/issues/802)).

## Decision

`Download` (`cli/internal/desktop/download.go`) asks the GitHub API for the repository's releases
and takes the newest one tagged `desktop/v<version>` that is not a draft. Pre-releases count. It
downloads that release's file for this platform, by its release name:

- macOS: `kstack-desktop-<version>-darwin-universal.dmg`.
- Windows: `kstack-desktop-<version>-windows-<arch>.msi`.
- Linux: `kstack-desktop-<version>-linux-<arch>.deb` when `/usr/bin/apt-get` exists, otherwise the
  `.rpm` when `/usr/bin/dnf` does. With neither, it fails before any request and names the
  releases page.

`Install` (`install.go`) installs it with the platform's own tool:

- macOS copies `Kstack.app` out of the disk image into `~/Applications`, for this user alone.
- Windows runs `msiexec /i <msi> /passive /norestart`. Windows asks for admin rights; the app lands
  in `%ProgramFiles%\Kstack`.
- Linux prints and runs `sudo apt-get install -y <deb>` or `sudo dnf install -y <rpm>`, attached to
  the terminal so the user answers sudo's prompt and sees the package manager's output. As root it
  drops `sudo`.

`Locate` looks only where these put the app: `/usr/bin/kstack-desktop` on Linux,
`%ProgramFiles%\Kstack\kstack-desktop.exe` on Windows, `Kstack.app` in `/Applications` or
`~/Applications` on macOS.

## Alternatives considered

- **Print the install command and exit.** No download and no admin step inside the CLI, but
  `kstack app` would no longer do what it says on a fresh Linux machine. The user already runs it
  in a terminal, where sudo's prompt is the ordinary way to grant root, and the command is printed
  before it runs.
- **`pkexec` instead of `sudo`.** It needs a running polkit agent, which a terminal over SSH or a
  minimal install lacks; sudo works wherever the user can administer the machine.
- **Build the file name from a version the CLI carries.** The CLI and the desktop app are tagged
  and released apart, so a pinned version would go stale with every desktop release.
- **A hosted download URL (`kstack.sh/download/...`).** Nothing serves it yet; the GitHub release
  is where the files are today.

## Consequences

- Installs need admin rights on Linux and Windows. Only macOS still installs for one user.
- `kstack app` depends on the GitHub API, unauthenticated: 60 requests an hour per address, one
  per install.
- The newest desktop release must attach every platform's file under its release name, or
  `kstack app` on the missing platform fails naming the file. `release-desktop.yml`'s `release`
  job already refuses any other set.
- A pre-release is installed when it is the newest release.
- The downloaded file runs as root on Linux with no check against the release's signed
  `SHA256SUMS` (`docs/TODO.md`, *Security*).

## Revisit when

Downloads move to a hosted URL, or the CLI is packaged so a package manager installs both.
