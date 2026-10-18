## Requirements

- Rust 1.98.1 via [rustup](https://rustup.rs/) (pinned in `src-tauri/rust-toolchain.toml`; rustup installs it on the first `cargo` call)
- [Go](https://go.dev/dl/) 1.26
- [Node](https://nodejs.org/en/download) 24
- [pnpm](https://pnpm.io/installation) 12.4.1 (`npm install -g pnpm@12.4.1`)

### Linux

```console
sudo apt-get install -y build-essential libwebkit2gtk-4.1-dev \
  libayatana-appindicator3-dev librsvg2-dev
```

For the agent's command sandbox and its tests:

```console
sudo apt-get install -y bubblewrap passt
```

## Run

```console
pnpm tauri dev
```

## Commands

```console
make test-changed  # tests touched by your branch
make test          # all tests
make lint
make vet
make proto         # after editing proto/
make clean-data    # wipe the dev app's data
```

## Read next

- `CLAUDE.md` (root and per area) — conventions
- `docs/adr/` — design decisions
- `docs/security-model.md`
