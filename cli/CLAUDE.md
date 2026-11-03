# cli — the `kstack` command

A Go module of its own (`github.com/kstackhq/kstack/cli`), on the sidecar's Go version. Commands
are [Cobra](https://github.com/spf13/cobra); the TUI is [Bubble Tea](https://github.com/charmbracelet/bubbletea)
with Bubbles and Lip Gloss. Run it with `go run .` from `cli/`; build it with
`go build -o kstack .`, since the directory alone would name the binary `cli`.

- **`kstack`** opens the placeholder TUI: the title, "TUI coming soon..." and a newsletter box.
- **`kstack app`** opens the desktop app, downloading and installing it first when it is missing.

The download URL (`desktop.DefaultURL`) and the newsletter endpoint (`newsletter.DefaultURL`) are
placeholders.

## Layout

| Package | Holds |
| --- | --- |
| `main.go` | `main`: wires the real dependencies into `cmd.Execute`. Nothing else, so it is not measured. |
| `cmd` | The Cobra commands, one file each (`root.go`, `app.go`). `New(Deps)` builds the tree, never a package variable, so tests build their own; `Execute` runs it and turns the result into an exit status. |
| `internal/desktop` | `Open`, the locate → download → install → launch decision, over the `Steps` interface; `System`, the real `Steps`. |
| `internal/newsletter` | `Client.Subscribe`: a JSON POST of the address. |
| `internal/tui` | `Model` (the update loop) and `Run`. |

## Conventions

- **`System` switches on its `OS` field, never on build tags.** Every platform's choices run in
  every platform's tests. Its folders (`Root`, `Home`, `Getenv`), commands (`Run`, `Start`) and
  network (`HTTP`, `URL`, `Stall`) are fields; tests point them at a `t.TempDir()`, a recorder and an
  `httptest.Server`.
- **The desktop app's Linux and Windows binary is also named `kstack`**, so `Locate` never
  searches PATH: on Linux it looks at `/usr/bin/kstack`, where the packages install the app, and
  skips `System.Self` there, or the CLI would launch itself.
- **Installs are per user and need no admin**: `~/Applications` on macOS and Linux, NSIS's silent
  current-user install (`%LOCALAPPDATA%\Kstack`) on Windows. `Install` ends by calling `Locate`, so
  where it installs and where it looks cannot drift apart.
- **`q` quits the TUI only once the email box is gone**; before that it may be part of an address.
  Esc and Ctrl+C always quit.
- **A TUI test never runs the text input's command**: that is the cursor's blink, which waits on a
  real timer. Run only a command the model returned (a submit, `tea.Quit`).
- Lip Gloss queries the terminal's background on start. A harness driving the binary through a
  bare pty must answer it (OSC 11 and the cursor report), or the TUI waits.

`make test`, `make lint`, `make vet` and `make test-changed` include this module, as CI's Go jobs do.
