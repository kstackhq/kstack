#!/usr/bin/env bash
# List the lines the working tree changes since the branch left main that a
# local coverage run measured and no test reached (tools/coverdiff). A pre-push
# aid, not the gate: CI's Coverage · Gate merges every OS's runs, so a line only
# another OS's tests reach is listed here.
#
#   Go    per module, sidecar/ and cli/, when it changed: the changed packages'
#         tests, or every test with FULL=1 or a change to its go.mod or go.sum.
#   JS    when src/ changed: the whole suite.
#   Rust  when src-tauri/ or proto/ changed, and only with RUST=1: it needs
#         cargo-llvm-cov and is the slowest of the three.
#
# Each suite is measured as .github/workflows/ci.yml's coverage jobs measure
# it; a change to one belongs in the other. BASE is as in scripts/test-changed.sh.
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
merge_base=$(git merge-base "$base" HEAD)

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

untracked=$(git ls-files --others --exclude-standard)
changed=$({
  git diff --name-only "$merge_base"
  echo "$untracked"
} | sort -u)

has() { grep -qE "$1" <<<"$changed"; }

existing() { while read -r f; do [ -e "$f" ] && echo "$f"; done; }

# The changed lines: tracked changes against the merge base, then each
# untracked file whole. --no-index exits 1 when the files differ.
diff_opts=(-U0 --no-color --no-ext-diff --src-prefix=a/ --dst-prefix=b/)
{
  git diff "${diff_opts[@]}" "$merge_base"
  while read -r f; do
    [ -n "$f" ] && { git diff "${diff_opts[@]}" --no-index /dev/null "$f" || true; }
  done <<<"$untracked"
} >"$out/changes.diff"

reports=()

# Each Go module as CI measures it: the sidecar also under -tags debug.
for mod in sidecar cli; do
  has "^$mod/" || continue
  if [ "${FULL:-}" = 1 ] || has "^$mod/go\.(mod|sum)$"; then
    go_pkgs=./...
  else
    go_pkgs=$(grep -E "^$mod/.*\.go$" <<<"$changed" | xargs -r -n1 dirname | sort -u | existing | sed "s|^$mod|.|" || true)
  fi
  [ -n "$go_pkgs" ] || continue
  profiles=("$out/$mod-untagged.out")
  [ "$mod" != sidecar ] || profiles+=("$out/$mod-debug.out")
  # shellcheck disable=SC2086 # one argument per package
  (
    cd "$mod"
    export CGO_ENABLED=0 SSH_AUTH_SOCK=""
    go test -timeout 30m -covermode=atomic -coverpkg=./... -coverprofile="$out/$mod-untagged.out" $go_pkgs
    if [ "$mod" = sidecar ]; then
      go test -tags debug -timeout 30m -covermode=atomic -coverpkg=./... -coverprofile="$out/$mod-debug.out" $go_pkgs
    fi
  )
  (cd tools && go run ./coverignore -dir "../$mod" -o "$out/$mod.out" "${profiles[@]}")
  reports+=(-go "$mod=$out/$mod.out")
done

if has '^src/'; then
  # lcov alone: the per-file table would bury the lines listed below.
  pnpm vitest run --mode coverage --coverage.reporter=lcovonly
  reports+=(-lcov "$PWD/coverage/lcov.info")
fi

if [ "${RUST:-}" = 1 ] && has '^(src-tauri|proto)/'; then
  # tauri-build fails without the bundled sidecar binary.
  make sidecar
  (cd src-tauri && cargo llvm-cov --lcov --output-path "$out/rust.lcov")
  (cd tools && go run ./coverignore -lcov -o "$out/rust-filtered.lcov" "$out/rust.lcov")
  reports+=(-lcov "$out/rust-filtered.lcov")
fi

if [ ${#reports[@]} -eq 0 ]; then
  echo "No measured suite changed; nothing to check."
  exit 0
fi
(cd tools && go run ./coverdiff -root .. -diff "$out/changes.diff" "${reports[@]}")
echo "Every changed line a test measured is covered."
