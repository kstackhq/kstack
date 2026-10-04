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

//go:build unix

package app

import (
	"context"
	"testing"

	"github.com/kstackhq/kstack/sidecar/internal/loginshell"
	"github.com/stretchr/testify/require"
)

// One run of the login shell answers both readers: its environment is set
// and its PATH handed on.
func TestResolveIsRunOnceAtLaunch(t *testing.T) {
	runs := 0
	path, fault := runShell(t.Context(), nil, func(context.Context, loginshell.Start) (loginshell.Result, *loginshell.Fault) {
		runs++
		return loginshell.Result{Path: []string{"/opt/bin", "/usr/bin"}}, nil
	})
	require.Equal(t, 1, runs)
	require.Equal(t, []string{"/opt/bin", "/usr/bin"}, path)
	require.Empty(t, fault)

	path, fault = runShell(t.Context(), nil, func(context.Context, loginshell.Start) (loginshell.Result, *loginshell.Fault) {
		return loginshell.Result{}, &loginshell.Fault{Reason: "timeout", ExitCode: -1}
	})
	require.Nil(t, path)
	require.Equal(t, "timeout", fault)
}

// stubLaunch makes the login shell's run answer res and f, and counts the
// runs it was asked for. A res with an Env is set process-wide on macOS, so a
// test that stubs one sets each of its names with t.Setenv first.
func stubLaunch(t *testing.T, res loginshell.Result, f *loginshell.Fault) *int {
	t.Helper()
	runs := new(int)
	resolve := resolveShell
	t.Cleanup(func() { resolveShell = resolve })
	resolveShell = func(context.Context, loginshell.Start) (loginshell.Result, *loginshell.Fault) {
		*runs++
		return res, f
	}
	return runs
}
