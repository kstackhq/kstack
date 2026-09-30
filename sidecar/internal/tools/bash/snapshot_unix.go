// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !windows

package bash

import (
	"context"

	"github.com/kstackhq/kstack/sidecar/internal/loginshell"
)

// shims follow the user's definitions in the snapshot, so they win. They are a
// courtesy, not a boundary: /bin/kill passes them. The unalias comes first
// because a function's name goes through alias expansion, so under
// alias kill='kill -9' the definition would not parse. kill is the builtin
// underneath, which keeps a job spec (%%) working. pkill asks pgrep first with
// the same arguments less the ones pgrep does not take — a signal, and procps's
// -e, -H and -q — which only widens what it matches. A signal name is matched
// with [[:upper:]]: macOS's bash 3.2 matches a range like [A-Z] by the locale's
// collation, which takes -ef for one. A pgrep that cannot answer
// is a refusal, since an unreadable answer is not permission; a machine without
// pgrep is left to pkill.
const shims = `
\builtin unalias kill pkill pgrep 2>/dev/null
__kstack_refuse() {
	\builtin printf '%s\n' "refused: ${1:-that would stop Kstack}" >&2
	return 1
}
__kstack_protected() {
	for __kstack_p in "$KSTACK_SIDECAR_PID" "$KSTACK_HOST_PID"; do
		\builtin test -n "$__kstack_p" || continue
		\builtin test "$1" = "$__kstack_p" && return 0
		\builtin test "$1" = "-$__kstack_p" && return 0
	done
	return 1
}
kill() {
	for __kstack_a in "$@"; do
		__kstack_protected "$__kstack_a" && { __kstack_refuse; return 1; }
	done
	\builtin kill "$@"
}
pkill() {
	\builtin command -v pgrep >/dev/null 2>&1 || { \builtin command pkill "$@"; return; }
	__kstack_args=()
	__kstack_first=1
	__kstack_skip=0
	for __kstack_a in "$@"; do
		if \builtin test "$__kstack_skip" = 1; then
			__kstack_skip=0
		else
			case "$__kstack_first:$__kstack_a" in
			1:-[0-9]* | 1:-[[:upper:]][[:upper:]]*) ;;
			*:--signal | *:-q | *:--queue) __kstack_skip=1 ;;
			*:--signal=* | *:--queue=* | *:-e | *:--echo | *:-H | *:--require-handler) ;;
			*) __kstack_args+=("$__kstack_a") ;;
			esac
		fi
		__kstack_first=0
	done
	__kstack_pids=$(\builtin command pgrep "${__kstack_args[@]}" 2>/dev/null)
	\builtin test $? -gt 1 && { __kstack_refuse 'pgrep could not check what that would stop'; return 1; }
	__kstack_nl='
'
	for __kstack_p in "$KSTACK_SIDECAR_PID" "$KSTACK_HOST_PID"; do
		\builtin test -n "$__kstack_p" || continue
		case "$__kstack_nl$__kstack_pids$__kstack_nl" in
		*"$__kstack_nl$__kstack_p$__kstack_nl"*) __kstack_refuse; return 1 ;;
		esac
	done
	\builtin command pkill "$@"
}
`

// launchDump runs the dump in the login shell, through the launcher
// loginshell.Import uses. reason is "" when the dump arrived whole.
func (t *Tool) launchDump(ctx context.Context) (out []byte, reason string, code int) {
	out, f := loginshell.Launch(ctx, t.shell, dumpCommand(t.kind), t.snapLimit, snapshotDone)
	if f != nil {
		return nil, f.Reason, f.ExitCode
	}
	return out, "", 0
}
