#!/usr/bin/env bash
# Runs the checks for the areas the working tree's changes touch
# (scripts/changed.sh), so a change to one area needs only that area's
# toolchain. `make check` runs every area; CI runs what
# .github/ci-config/path-filters.yml selects.
#
#   always                     lint-md, lint-markers, then test-changed and
#                              cover-changed
#   sidecar/, cli/, tools/     lint-go, vet-go
#   src-tauri/, proto/         lint-rust, vet-rust
#   the webview and its config lint-ts, lint-tailwind
#   *.sh                       lint-sh
#   YAML, .github/             lint-yaml
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/changed.sh
. scripts/changed.sh

targets=(lint-md lint-markers)
if has '^(sidecar|cli|tools)/'; then targets+=(lint-go vet-go); fi
if has '^(src-tauri|proto)/'; then targets+=(lint-rust vet-rust); fi
if has '^(src|lint)/|^\.ox[a-z]+rc[^/]*\.json$|^package\.json$|^pnpm-[^/]*\.yaml$|^tsconfig[^/]*\.json$|^[^/]*\.ts$'; then
  targets+=(lint-ts lint-tailwind)
fi
if has '\.sh$'; then targets+=(lint-sh); fi
if has '\.ya?ml$|^\.github/|^\.yamllint'; then targets+=(lint-yaml); fi
targets+=(test-changed cover-changed)

make "${targets[@]}"
