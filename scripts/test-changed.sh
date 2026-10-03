#!/usr/bin/env bash
# Run the tests the working tree's changes touch: committed, staged, unstaged and
# untracked, since the branch left main. The full suites and both coverage gates
# run in CI.
#
#   JS    vitest's `related`, which follows imports, so a hook's change runs the
#         tests of the components using it. A change to the toolchain runs it all.
#   Go    the changed packages. A change to go.mod or go.sum runs it all.
#   Rust  the whole suite, and only when src-tauri/ or proto/ changed: it builds
#         the sidecar first, and is the slowest of the three.
#
# BASE names the branch to diff against. It defaults to upstream/main when there
# is an upstream remote, since a fork's origin/main can lag, else origin/main.
set -euo pipefail
cd "$(dirname "$0")/.."

base="${BASE:-}"
if [ -z "$base" ]; then
  if git rev-parse -q --verify upstream/main >/dev/null; then
    base=upstream/main
  else
    base=origin/main
  fi
fi

changed=$({
  git diff --name-only "$(git merge-base "$base" HEAD)"
  git ls-files --others --exclude-standard
} | sort -u)

has() { grep -qE "$1" <<<"$changed"; }

# A deleted file has nothing to test, and vitest refuses a path that isn't there.
existing() { while read -r f; do [ -e "$f" ] && echo "$f"; done; }

if has '^(package\.json|pnpm-lock\.yaml|vite\.config\.ts|vitest\.setup\.ts|tsconfig[^/]*\.json)$'; then
  pnpm test --run
else
  js=$(grep -E '^src/.*\.tsx?$' <<<"$changed" | existing || true)
  if [ -n "$js" ]; then
    # shellcheck disable=SC2086 # one argument per file
    pnpm vitest related --run $js
  fi
fi

if has '^sidecar/go\.(mod|sum)$'; then
  (cd sidecar && go test ./...)
else
  go_pkgs=$(grep -E '^sidecar/.*\.go$' <<<"$changed" | xargs -r -n1 dirname | sort -u | existing | sed 's|^sidecar|.|' || true)
  if [ -n "$go_pkgs" ]; then
    # shellcheck disable=SC2086 # one argument per package
    (cd sidecar && go test $go_pkgs)
  fi
fi

if has '^(src-tauri|proto)/'; then
  make test-rust
fi
