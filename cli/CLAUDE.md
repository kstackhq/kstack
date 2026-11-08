# cli — the `kstack` command

A Go module of its own (`github.com/kstackhq/kstack/cli`), on the sidecar's Go version. Commands
are [Cobra](https://github.com/spf13/cobra); the TUI is [Bubble Tea](https://github.com/charmbracelet/bubbletea)
with Bubbles and Lip Gloss. Run it with `go run .` from `cli/`; build it with
`go build -o kstack .`, since the directory alone would name the binary `cli`.

- **`kstack`** opens the placeholder TUI: the title, "TUI coming soon..." and a newsletter box.
- **`kstack app`** opens the desktop app, downloading and installing it first when it is missing.
- **`kstack --version`** prints the version the release build set, or `dev`.

The download URL (`desktop.DefaultURL`) and the newsletter endpoint (`newsletter.DefaultURL`) are
placeholders.

## Layout

| Package | Holds |
| --- | --- |
| `main.go` | `main`: wires the real dependencies and the build's `version` into `cmd.Execute`. Nothing else, so it is not measured. |
| `cmd` | The Cobra commands, one file each (`root.go`, `app.go`). `New(Deps)` builds the tree, never a package variable, so tests build their own; `Execute` runs it and turns the result into an exit status. |
| `internal/desktop` | `Open`, the locate → download → install → launch decision, over the `Steps` interface; `System`, the real `Steps`. |
| `internal/newsletter` | `Client.Subscribe`: a JSON POST of the address. |
| `internal/tui` | `Model` (the update loop) and `Run`. |

## Conventions

- **`System` switches on its `OS` field, never on build tags.** Every platform's choices run in
  every platform's tests. Its folders (`Root`, `Home`, `Getenv`), commands (`Run`, `Start`) and
  network (`HTTP`, `URL`, `Stall`) are fields; tests point them at a `t.TempDir()`, a recorder and an
  `httptest.Server`.
- **The desktop app's binary is `kstack-desktop`**, never `kstack`: on Linux `Locate` looks at
  `/usr/bin/kstack-desktop`, where the packages install it. Its display name, and so its bundle and
  install folder, is `Kstack` (`Kstack.app`, `%LOCALAPPDATA%\Kstack\kstack-desktop.exe`). →
  [ADR: the app is Kstack and its binary kstack-desktop](../docs/adr/2026-11-07-the-desktop-app-is-kstack-and-its-binary-kstack-desktop.md).
- **Installs are per user and need no admin**: `~/Applications` on macOS and Linux, NSIS's silent
  current-user install (`%LOCALAPPDATA%\Kstack`) on Windows. `Install` ends by calling `Locate`, so
  where it installs and where it looks cannot drift apart.
- **`q` quits the TUI only once the email box is gone**; before that it may be part of an address.
  Esc and Ctrl+C always quit.
- **A TUI test never runs the text input's command**: that is the cursor's blink, which waits on a
  real timer. Run only a command the model returned (a submit, `tea.Quit`).
- Lip Gloss queries the terminal's background on start. A harness driving the binary through a
  bare pty must answer it (OSC 11 and the cursor report), or the TUI waits.

## Releasing

`.github/workflows/release-cli.yml` builds every darwin, linux and windows target on amd64 and arm64
on one Linux runner (`CGO_ENABLED=0`, `-trimpath`, `-X main.version=<version>`). It packs each as
`kstack-cli-<version>-<os>-<arch>.tar.gz` (`.zip` on Windows), with `LICENSE`, and publishes them
with a CycloneDX SBOM, `SHA256SUMS` and its GPG signature `SHA256SUMS.asc`. The binaries are not
code-signed, so macOS Gatekeeper blocks one downloaded through a browser.

Every Kstack product's release names its files `kstack-<product>-<version>-<os>-<arch>.<ext>`, all
lowercase; a file covering every platform drops `-<os>-<arch>` (`kstack-cli-<version>.cdx.json`).
The desktop app's are `kstack-desktop-…`, its macOS `<arch>` `universal` (`src-tauri/CLAUDE.md`).
→ [ADR: release files are named kstack-\<product\>-\<version\>-\<os\>-\<arch\>](../docs/adr/2026-11-08-release-files-are-named-kstack-product-version-os-arch.md).

- **Pick the version** with `/release-version cli` (`.claude/skills/release-version/SKILL.md`). It reads
  the commits under `cli/` since the last `cli/v*` tag and recommends the next SemVer version and its
  tag. It tags nothing.
- **Cut a release** by pushing a tag: `git tag cli/v0.1.0 && git push origin cli/v0.1.0`. The workflow
  creates a draft GitHub release named `cli/v<version>`, a prerelease when the version has a `-`
  suffix. Write its notes with `/release-notes cli cli/v<version>` (`.claude/skills/release-notes`),
  then publish the draft by hand.
- **Dry run** with *Run workflow* (`workflow_dispatch`), `dry_run` checked. It builds the dispatched
  commit, so it needs no tag, then prints `SHA256SUMS` and its signature and publishes nothing. A
  dispatch with `dry_run` unchecked builds the `cli/v<version>` tag, which must exist.
- The version must be `major.minor.patch` with an optional dot-separated prerelease
  (`0.1.0-rc.1`); the workflow rejects anything else.

`make test`, `make lint`, `make vet` and `make test-changed` include this module, as CI's Go jobs do. So do `make cover-go` and `make cover-changed`; the latter measures it as CI's `Go · Coverage` job does, without the sidecar's `CGO_ENABLED=0`, empty `SSH_AUTH_SOCK` or `-tags debug`.
