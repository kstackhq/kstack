#!/usr/bin/env bash

set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Read the pins from the files that already enforce them, so a bump there
# lands here too. The Go parse matches scripts/sandbox-dev-setup.sh.
rust_pin() { sed -n 's/^channel = "\(.*\)"/\1/p' "$REPO/src-tauri/rust-toolchain.toml"; }
go_pin() {
  awk '
    /^  go-version:/ { in_go = 1; next }
    /^  [^ ]/        { in_go = 0 }
    in_go && /^    default:/ { gsub(/[^0-9.]/, "", $2); print $2; exit }
  ' "$REPO/.github/actions/setup-environment/action.yml"
}
pnpm_pin() { sed -n 's/.*"packageManager": "pnpm@\([^"]*\)".*/\1/p' "$REPO/package.json"; }

RUST_VERSION=$(rust_pin)
GO_VERSION=$(go_pin)
NODE_MAJOR=24
PNPM_VERSION=$(pnpm_pin)

BIN=$HOME/.local/bin
ORIG_PATH=$PATH
export PATH="$BIN:$HOME/.cargo/bin:$PATH"

has() { command -v "$1" >/dev/null; }
path_has() { case ":$1:" in *":$2:"*) return 0 ;; esac; return 1; }

rust_ok() {
  has rustup || return 1
  local n
  n=$(rustup component list --toolchain "$RUST_VERSION" --installed |
    grep -cE '^(rustfmt|clippy|llvm-tools)' || true)
  [ "${n:-0}" -eq 3 ]
}
go_ok() { has go && go version | grep -q "go$GO_VERSION"; }
node_ok() { has node && [ "$(node -p 'process.versions.node.split(".")[0]')" = "$NODE_MAJOR" ]; }
pnpm_ok() { has pnpm && [ "$(pnpm --version)" = "$PNPM_VERSION" ]; }
uv_ok() { has uv; }

confirm() {
  if [ ! -t 0 ]; then echo "no terminal; run this script interactively" >&2; exit 1; fi
  local reply
  read -rp "$1 [Y/n] " reply || return 1
  case "${reply:-y}" in n|N|no|NO) return 1 ;; esac
}

install_rust() {
  if rust_ok; then return 0; fi
  echo "→ Installing Rust $RUST_VERSION"
  if ! has rustup; then
    curl --proto '=https' --tlsv1.2 -fsSL https://sh.rustup.rs -o "$TMP/rustup.sh"
    sh "$TMP/rustup.sh" -y --no-modify-path --profile minimal --default-toolchain none
  fi
  export PATH="$HOME/.cargo/bin:$PATH"
  rustup toolchain install "$RUST_VERSION" --profile minimal \
    --component rustfmt --component clippy --component llvm-tools-preview
  rustup default >/dev/null 2>&1 || rustup default "$RUST_VERSION"
}

install_go() {
  if go_ok; then return 0; fi
  echo "→ Installing Go $GO_VERSION"
  local arch file dest sha
  case "$(uname -m)" in
    arm64) arch=arm64 ;;
    x86_64) arch=amd64 ;;
    *) echo "Go: unsupported architecture $(uname -m)" >&2; exit 1 ;;
  esac
  file="go$GO_VERSION.darwin-$arch.tar.gz"
  # Each file entry in go.dev's JSON carries its sha256; check the download
  # against it, as Node's SHASUMS256.txt is checked below.
  sha=$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' |
    perl -ne 'if (/"filename": "'"$file"'"/ .. /\}/) { /"sha256": "([0-9a-f]+)"/ && print "$1\n"; }')
  [ -n "$sha" ] || { echo "Go: no checksum listed for $file" >&2; exit 1; }
  dest="$HOME/.local/share/kstack/go/$GO_VERSION"
  curl -fsSL "https://go.dev/dl/$file" -o "$TMP/$file"
  [ "$(shasum -a 256 "$TMP/$file" | cut -d' ' -f1)" = "$sha" ] || {
    echo "Go: checksum mismatch for $file" >&2; exit 1;
  }
  rm -rf "$dest"
  mkdir -p "$dest"
  tar -xzf "$TMP/$file" -C "$dest" --strip-components=1
  ln -sf "$dest/bin/go" "$BIN/go"
  ln -sf "$dest/bin/gofmt" "$BIN/gofmt"
}

install_node() {
  if node_ok; then return 0; fi
  echo "→ Installing Node.js $NODE_MAJOR.x"
  local arch base file ver dest
  case "$(uname -m)" in
    arm64) arch=arm64 ;;
    x86_64) arch=x64 ;;
    *) echo "Node.js: unsupported architecture $(uname -m)" >&2; exit 1 ;;
  esac
  base="https://nodejs.org/dist/latest-v$NODE_MAJOR.x"
  curl -fsSL "$base/SHASUMS256.txt" -o "$TMP/SHASUMS256.txt"
  file=$(awk -v s="-darwin-$arch.tar.gz" \
    'substr($2, length($2) - length(s) + 1) == s { print $2; exit }' "$TMP/SHASUMS256.txt")
  [ -n "$file" ] || { echo "Node.js: no archive for darwin-$arch" >&2; exit 1; }
  curl -fsSL "$base/$file" -o "$TMP/$file"
  (cd "$TMP" && grep " $file\$" SHASUMS256.txt | shasum -a 256 -c - >/dev/null)
  ver=${file#node-v}
  ver=${ver%%-*}
  dest="$HOME/.local/share/kstack/node/v$ver"
  rm -rf "$dest"
  mkdir -p "$dest"
  tar -xzf "$TMP/$file" -C "$dest" --strip-components=1
  for b in node npm npx; do ln -sf "$dest/bin/$b" "$BIN/$b"; done
}

install_pnpm() {
  if pnpm_ok; then return 0; fi
  echo "→ Installing pnpm $PNPM_VERSION"
  npm install -g --prefix "$HOME/.local" "pnpm@$PNPM_VERSION"
}

install_uv() {
  if uv_ok; then return 0; fi
  echo "→ Installing uv"
  curl -LsSf https://astral.sh/uv/install.sh -o "$TMP/uv.sh"
  sh "$TMP/uv.sh"
}

install_deps() {
  echo "→ Installing workspace dependencies"
  (cd "$REPO" && pnpm install --frozen-lockfile)
}

path_hint() {
  echo "Run this to use the tools in this terminal:"
  echo "  source $1"
  echo "Or open a new terminal."
}

add_path_to_profile() {
  local profile=$1
  cat >>"$profile" <<'EOF'

# kstack prerequisites
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"
EOF
  echo "Added to $profile."
  path_hint "$profile"
}

setup_path() {
  if path_has "$ORIG_PATH" "$BIN" && path_has "$ORIG_PATH" "$HOME/.cargo/bin"; then
    return 0
  fi
  local profile
  case "${SHELL:-}" in
    */zsh) profile=${ZDOTDIR:-$HOME}/.zshrc ;;
    */bash) profile=$HOME/.bash_profile ;;
    *) echo "Add $BIN and $HOME/.cargo/bin to PATH in your shell profile."; return 0 ;;
  esac
  add_path_to_profile "$profile"
}

summary() {
  echo
  echo "Versions:"
  printf '  rust  %s\n' "$(rustc --version 2>/dev/null || echo missing)"
  printf '  go    %s\n' "$(go version 2>/dev/null || echo missing)"
  printf '  node  %s\n' "$(node --version 2>/dev/null || echo missing)"
  printf '  pnpm  %s\n' "$(pnpm --version 2>/dev/null || echo missing)"
  printf '  uv    %s\n' "$(uv --version 2>/dev/null || echo missing)"
  printf '\nNext steps:\n  pnpm tauri dev\n  make test-changed\n'
}

main() {
  if [ "$(uname -s)" != Darwin ]; then
    echo "This script supports macOS only." >&2
    exit 1
  fi
  if ! xcode-select -p >/dev/null; then
    echo "Install the Xcode Command Line Tools first: xcode-select --install" >&2
    exit 1
  fi
  state() { if "$1" >/dev/null 2>&1; then echo installed; else echo "not installed"; fi; }

  echo "Kstack prerequisites(macOS)"
  echo
  printf '  %-6s %-10s %s\n' Tool Version Status
  printf '  %-6s %-10s %s\n' rust "$RUST_VERSION" "$(state rust_ok)"
  printf '  %-6s %-10s %s\n' go "$GO_VERSION" "$(state go_ok)"
  printf '  %-6s %-10s %s\n' node "$NODE_MAJOR.x" "$(state node_ok)"
  printf '  %-6s %-10s %s\n' pnpm "$PNPM_VERSION" "$(state pnpm_ok)"
  printf '  %-6s %-10s %s\n' uv - "$(state uv_ok)"
  printf '  %-6s %-10s %s\n' deps - "will install"
  echo
  confirm "Proceed?" || { echo "Cancelled."; exit 0; }

  mkdir -p "$BIN" "$HOME/.local/share/kstack"
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT
  install_rust
  install_go
  install_node
  install_pnpm
  install_uv
  install_deps
  hash -r
  setup_path
  summary
}

main
