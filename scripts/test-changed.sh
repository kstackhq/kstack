#!/usr/bin/env bash
# Run the tests the working tree's changes touch (scripts/changed.sh). The full
# suites and coverage run in CI.
#
#   JS    vitest's `related`, which follows imports, so a hook's change runs the
#         tests of the components using it. A change to the toolchain runs it all.
#   Go    the changed packages, in sidecar/ and cli/. A change to a module's
#         go.mod or go.sum runs that module's whole suite.
#   Rust  the whole suite, and only when src-tauri/ or proto/ changed: it builds
#         the sidecar first, and is the slowest of the three.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/changed.sh
. scripts/changed.sh

if has '^(package\.json|pnpm-lock\.yaml|vite\.config\.ts|vitest\.setup\.ts|tsconfig[^/]*\.json)$'; then
  pnpm test --run
else
  js=$(grep -E '^src/.*\.tsx?$' <<<"$changed" | existing || true)
  if [ -n "$js" ]; then
    # shellcheck disable=SC2086 # one argument per file
    pnpm vitest related --run $js
  fi
fi

for mod in sidecar cli; do
  if has "^$mod/go\.(mod|sum)$"; then
    (cd "$mod" && go test ./...)
  else
    go_pkgs=$(grep -E "^$mod/.*\.go$" <<<"$changed" | xargs -r -n1 dirname | sort -u | existing | sed "s|^$mod|.|" || true)
    if [ -n "$go_pkgs" ]; then
      # shellcheck disable=SC2086 # one argument per package
      (cd "$mod" && go test $go_pkgs)
    fi
  fi
done

if has '^tools/'; then
  (cd tools && go test ./...)
fi

if has '^(src-tauri|proto)/'; then
  make test-rust
fi
