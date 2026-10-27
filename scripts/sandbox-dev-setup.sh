#!/usr/bin/env bash
# Set up a Linux-native dev environment for Kstack inside the sandbox,
# without letting its build artifacts collide with the macOS host's.
#
# The repo is bind-mounted from the macOS host, so every build output path is
# shared. Two mechanisms keep the two platforms apart:
#
#   1. Configurable output paths (Cargo, pnpm store) are pointed at Linux-only
#      directories via environment variables.
#   2. Fixed paths that tooling can't relocate (node_modules, dist) get a
#      sandbox-only bind mount on top of them. A bind mount is invisible to the
#      host: macOS keeps its own node_modules underneath, untouched, while the
#      sandbox sees the Linux one.
#
# Cargo's target/ and node_modules live on the sandbox's own overlay
# ($OVERLAY_ROOT). Cargo needs it: the host mount's directory metadata is not
# coherent under dense writes, so a build there never converges. node_modules
# is read thousands of times per test run, and the host mount makes every read
# slow. Each worktree gets its own node_modules under $OVERLAY_ROOT/worktrees.
#
# The overlay is capped at 20G and wiped on sandbox recreate, so what is small
# or rarely read stays on the host's disk: dist under $BUILD_ROOT, and one pnpm
# store for every worktree under the main checkout's .sandbox-linux. pnpm copies
# from the store wherever it sits: link(2) fails across the node_modules bind
# mount even within one filesystem.
#
# Idempotent — safe to re-run. Run it once per sandbox boot: bind mounts do not
# survive a restart, the env vars (in /etc/sandbox-persistent.sh) do.

set -euo pipefail

# No-op outside the sandbox. This runs from a SessionStart hook, and the repo is
# shared with the macOS host, so the host's own agents fire this same hook —
# where every redirection here would be wrong. $SANDBOX_VM_ID is set only inside
# the sandbox; the Linux check guards against a stray export.
if [ -z "${SANDBOX_VM_ID:-}" ] || [ "$(uname -s)" != "Linux" ]; then
  exit 0
fi

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_ROOT="${KSTACK_SANDBOX_BUILD_ROOT:-$REPO/.sandbox-linux}"
OVERLAY_ROOT="${KSTACK_SANDBOX_OVERLAY_ROOT:-/home/agent/kstack-build}"
NODE_MODULES="$OVERLAY_ROOT/worktrees$REPO/node_modules"
MAIN_REPO="$(dirname "$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)")"
PNPM_STORE="$MAIN_REPO/.sandbox-linux/pnpm-store"
PERSIST="/etc/sandbox-persistent.sh"
MIN_FREE_MB=2048

echo "→ build root: $BUILD_ROOT"
echo "→ overlay root: $OVERLAY_ROOT"
# Go's caches are deliberately absent here: they default to $HOME, which is
# already sandbox-local (not shared with the host), so they need no
# redirection. Keeping them out of the repo also keeps the module cache's
# vendored *.ts fixtures out of vitest's file discovery, which — unlike
# eslint — does not honor .gitignore.
mkdir -p "$BUILD_ROOT/dist" "$PNPM_STORE" "$NODE_MODULES" "$OVERLAY_ROOT/target"

free_mb="$(df --output=avail -BM "$OVERLAY_ROOT" | tail -1 | tr -dc 0-9)"
if [ "$free_mb" -lt "$MIN_FREE_MB" ]; then
  echo "⚠ only ${free_mb}M free on the overlay; installs and builds may fail."
  echo "  Reclaim space with 'cargo clean', 'go clean -cache', or by deleting"
  echo "  removed worktrees' node_modules under $OVERLAY_ROOT/worktrees."
fi

# --- 1. bind mounts for paths the tooling can't relocate -------------------
# A mount from any other source (an older layout) is replaced. Comparing
# device and inode tells whether $target already shows $src.
bind() {
  local src="$1" target="$2"
  mkdir -p "$target"
  if [ "$(stat -c %d:%i "$src")" = "$(stat -c %d:%i "$target")" ]; then
    echo "✓ $target already bind-mounted"
    return
  fi
  if mountpoint -q "$target"; then
    sudo umount "$target"
  fi
  sudo mount --bind "$src" "$target"
  echo "✓ $target bind-mounted from $src"
}
bind "$NODE_MODULES" "$REPO/node_modules"
bind "$BUILD_ROOT/dist" "$REPO/dist"

# --- 2. env vars for the paths that are configurable -----------------------
# Written to the persistent env file, which is sourced before every command.
# Every line this script owns is dropped first, so a re-run rewrites the values
# rather than leaving an older copy above them.
#
# pnpm takes settings from PNPM_CONFIG_* or pnpm-workspace.yaml, and the yaml is
# committed — shared with the host, so it cannot hold a Linux path. The env file
# is shared by every worktree in the sandbox, so the store path must not depend
# on $REPO.
sudo touch "$PERSIST"
sudo sed -i \
  -e '/KSTACK_SANDBOX_ENV/d' \
  -e '/^export CARGO_TARGET_DIR=/d' \
  -e '/^export PNPM_CONFIG_STORE_DIR=/d' \
  -e '/^export PATH=.*\.cargo\/bin/d' \
  "$PERSIST"
sudo tee -a "$PERSIST" >/dev/null <<EOF
# KSTACK_SANDBOX_ENV — Linux build outputs, kept out of the macOS host's paths.
export CARGO_TARGET_DIR="$OVERLAY_ROOT/target"
export PNPM_CONFIG_STORE_DIR="$PNPM_STORE"
export PATH="\$HOME/.cargo/bin:\$HOME/.local/share/pnpm:\$PATH"
EOF
echo "✓ env vars written to $PERSIST"

echo
echo "Done. Open a new shell (or 'source $PERSIST') to pick up the env vars."
