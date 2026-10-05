## Architecture

- **`src/`** — the React webview (React 19, TanStack Router, urql, Tailwind v4).
- **`src-tauri/`** — the Tauri 2 / Rust native host: windows, tray, menus, and the sidecar's
  lifecycle. It bridges all webview GraphQL over a Unix socket — the webview has no network
  access.
- **`sidecar/`** — a Go binary owning the GraphQL API and all Kubernetes/cloud logic. It
  mirrors each cluster into a per-cluster SQLite cache and streams changes to the UI.
- **`proto/`** — the shared host↔sidecar gRPC contract.

Design rationale lives in [`docs/adr/`](docs/adr/README.md). Working conventions live in the
per-area `CLAUDE.md` files.

## Toolchain

Rust (rustup), Go, Node + `pnpm`. `rust-toolchain.toml` pins the Rust compiler, so rustup
installs and selects it for you on the first `cargo` call — don't override it with `+stable`.
On Linux, Tauri needs the usual native deps:

```
sudo apt-get install -y build-essential pkg-config libssl-dev \
  libwebkit2gtk-4.1-dev libgtk-3-dev libayatana-appindicator3-dev \
  librsvg2-dev libsoup-3.0-dev libjavascriptcoregtk-4.1-dev patchelf
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
npm install -g pnpm@12.4.1   # corepack can't run pnpm 12 on the bundled Node
```

The sidecar's sandbox on Linux is bubblewrap. Its tests and a dev build use the system's, and
`make bwrap` builds the one the packages carry, which needs meson, ninja and libcap's headers:

```
sudo apt-get install -y bubblewrap passt iproute2 meson ninja-build libcap-dev
```

A sandboxed command reaches the internet through `pasta`, from the `passt` package: without it the
chat's network switch is disabled and says so. One of its tests sets up a network namespace with
`ip`, from `iproute2`.

On Ubuntu 23.10 and 24.04, AppArmor keeps the system's bwrap from making a user namespace, and
the profile the `.deb` installs names the packaged bwrap alone. Lift the restriction for the
tests and a dev build's sandbox (`sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`,
until the next boot); without it the sandbox's tests skip and Bash runs unsandboxed.

On Windows, install Git for Windows, GNU Make, and Visual Studio Build Tools with the
**Desktop development with C++** workload. On an ARM machine also add **MSVC C++ ARM64 build
tools**: without the toolset for the host's arch, cargo falls back to whatever `link.exe` is on
`PATH`, which under Git Bash is coreutils' `link`. WebView2 ships with Windows 11. The checkout is
LF on every OS (`.gitattributes`), whatever `core.autocrlf` says. `pnpm tauri dev` needs no POSIX
shell, but run `make` targets from Git Bash: their recipes are `sh`.

## Develop

```
pnpm install
pnpm tauri dev      # full app (or `pnpm dev` for the webview alone)
```

The `Makefile` is the polyglot entry point:

```
make test    # JS + Rust + Go
make test-changed  # only the tests this branch's changes touch
make lint
make vet
make proto   # regenerate the gRPC bindings after editing proto/
```

If you develop inside a Linux sandbox against a macOS host checkout, run
`./scripts/sandbox-dev-setup.sh` first — see the root `CLAUDE.md` for details.

### Sandbox environment (optional)

```
docker build -f Dockerfile.sbx -t claude-kstack .
docker image save claude-kstack -o .sbx/claude-kstack.tar
sbx template load .sbx/claude-kstack.tar
sbx run claude --template claude-kstack
sbx exec -it claude-kstack bash     # shell inside
./scripts/expose-dev.sh                 # port forward
```

## Recommended IDE setup

[VS Code](https://code.visualstudio.com/) + [Tauri](https://marketplace.visualstudio.com/items?itemName=tauri-apps.tauri-vscode) + [rust-analyzer](https://marketplace.visualstudio.com/items?itemName=rust-lang.rust-analyzer)

## Security

Read the [security model](docs/security-model.md) and the
[latest review and open findings](docs/security/2026-09-04-security-review.md) before using sensitive
clusters. Only load trusted kubeconfigs: enabled contexts are connected automatically, and their
credential plugins can execute local programs. Cached cluster data may contain credentials outside
the redaction table, remains on disk after cloud sign-out, and is not encrypted by the app. Use
least-privilege Kubernetes credentials and protect the OS account and disk. Review open findings
and verification gaps before distributing a release.
