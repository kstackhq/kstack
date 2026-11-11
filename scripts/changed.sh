# shellcheck shell=bash
# Sourced by the *-changed.sh scripts, from the repo root. Sets what the working
# tree changes since the branch left main: committed, staged, unstaged and
# untracked.
#
#   repo_root   the repo's absolute path
#   merge_base  the commit the branch left
#   untracked   the untracked files
#   changed     every changed path, one per line
#
# It exports CGO_ENABLED=0.
#
# The functions below read them.
#
# BASE names the branch to diff against. It defaults to upstream/main when there
# is an upstream remote, since a fork's origin/main can lag, else origin/main.

repo_root=$PWD

# As the sidecar and the CLI ship. One setting, so every script's Go commands
# share one build cache.
export CGO_ENABLED=0

base="${BASE:-}"
if [ -z "$base" ]; then
  if git rev-parse -q --verify upstream/main >/dev/null; then
    base=upstream/main
  else
    base=origin/main
  fi
fi
merge_base=$(git merge-base "$base" HEAD)

untracked=$(git ls-files --others --exclude-standard)
changed=$({
  git diff --name-only "$merge_base"
  echo "$untracked"
} | sort -u)

has() { grep -qE "$1" <<<"$changed"; }

# A deleted file has nothing to check, and vitest refuses a path that isn't there.
existing() { while read -r f; do [ -e "$f" ] && echo "$f"; done; }

# vitest_scope: the vitest arguments that run the changed files' tests: `related`
# with the changed files, which follows imports, so a hook's change runs the
# tests of the components using it; `run` when the toolchain changed; nothing
# when neither did.
vitest_scope() {
  if has '^(package\.json|pnpm-lock\.yaml|vite\.config\.ts|vitest\.setup\.ts|tsconfig[^/]*\.json)$'; then
    echo run
    return
  fi
  local js
  js=$(grep -E '^src/.*\.tsx?$|^lint/.*\.[jt]s$' <<<"$changed" | existing | tr '\n' ' ' || true)
  [ -z "$js" ] || echo "related --run $js"
}

# go_dirs <mod>: the module's packages with a changed Go file, as ./<dir>, or
# ./... when its go.mod or go.sum changed.
go_dirs() {
  if has "^$1/go\.(mod|sum)$"; then
    echo ./...
  else
    grep -E "^$1/.*\.go$" <<<"$changed" | xargs -r -n1 dirname | sort -u | existing | sed "s|^$1|.|" || true
  fi
}

# go_files <mod> <env> <pkg...>: each Go file the packages build under the
# environment, tests included, as "<package> <repo-relative path>", sorted.
go_files() {
  local mod=$1 config=$2
  shift 2
  # shellcheck disable=SC2016 # a Go template
  local fmt='{{$p := .ImportPath}}{{$d := .Dir}}{{range .GoFiles}}{{$p}} {{$d}}/{{.}}
{{end}}{{range .CgoFiles}}{{$p}} {{$d}}/{{.}}
{{end}}{{range .TestGoFiles}}{{$p}} {{$d}}/{{.}}
{{end}}{{range .XTestGoFiles}}{{$p}} {{$d}}/{{.}}
{{end}}'
  # shellcheck disable=SC2086 # one word per variable
  (cd "$repo_root/$mod" && env $config go list -e -f "$fmt" "$@") | sed "s| $repo_root/| |" | sort
}

# go_changed_only <here> <there>: the packages with a changed file that <there>
# lists and <here> does not. Each is go_files' output.
go_changed_only() {
  comm -13 <(echo "$1") <(echo "$2") | awk -v changed="$changed" '
    BEGIN { n = split(changed, c, "\n"); for (i = 1; i <= n; i++) want[c[i]] = 1 }
    $2 in want { print $1 }' | sort -u
}
