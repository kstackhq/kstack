#!/bin/sh
# A coverage marker exempts lines from codecov/patch, so it must say why:
# `// coverage:ignore <reason>` or `// coverage:ignore-start <reason>` in Go,
# `/* v8 ignore next -- <reason> */` in TypeScript. Prints every marker without
# a reason and fails. A `stop` closes a `start` and carries no reason of its own.
set -eu
cd "$(dirname "$0")/.."

status=0
if git grep -nE '//[[:space:]]*coverage:ignore(-start)?[[:space:]]*$' -- 'sidecar/*.go'; then
  status=1
fi
if git grep -nE '(istanbul|[cv]8|node:coverage)[[:space:]]+ignore[[:space:]]+(if|else|next|file|start)([^[:alnum:]]|$)' -- 'src/*.ts' 'src/*.tsx' |
  grep -vE -- '--[[:space:]]*[^[:space:]*]'; then
  status=1
fi
if [ "$status" -ne 0 ]; then
  echo "coverage markers above need a reason" >&2
fi
exit "$status"
