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
	"path/filepath"
	"testing"

	"github.com/kstackhq/kstack/sidecar/internal/run/loginshell"
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

// The launch resolution runs in the sandbox, its output leaving it, so the
// denied-always list is shut to it; a refused policy runs no shell.
func TestTheLaunchResolutionRunsInTheSandbox(t *testing.T) {
	sb := &refusingSandbox{never: []string{"/never"}}
	kstackDirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	tmpDir := filepath.Join(kstackDirs[1], "tmp")

	path, fault := launchShell(t.Context(), sb, kstackDirs, tmpDir)

	require.Nil(t, path)
	require.Equal(t, "sandbox refused", fault)
	require.Len(t, sb.runs, 1)
	p := sb.runs[0].Policy
	require.Equal(t, []string{"/never"}, p.Always.Deny)
	require.Equal(t, kstackDirs, p.Always.Kstack)
	require.Len(t, p.Always.Write, 1)
	require.Equal(t, tmpDir, filepath.Dir(p.Always.Write[0]))
	require.Zero(t, p.Network)
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
