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
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
)

// sandboxed is this machine's sandbox, for a test that runs a command through
// it, or testutil.RequireSandbox's verdict where there is none.
func sandboxed(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	s, v, err := sandbox.Probe(t.Context())
	require.NoError(t, err)
	if v.Available {
		return s
	}
	testutil.RequireSandbox(t, "no sandbox: "+v.Reason)
	return nil
}

// confining is this machine's sandbox, for a test that needs it to confine,
// or testutil.RequireSandbox's verdict where there is none.
func confining(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	s, v, err := sandbox.Probe(t.Context())
	require.NoError(t, err)
	if s != nil && s.Confines() {
		return s
	}
	testutil.RequireSandbox(t, "no confining sandbox: "+v.Reason)
	return nil
}

// withTool puts name, where the machine has it, on tl's PATH through a folder
// the user included that holds a link to it: the folder it is in may be one
// no run searches, as a CI runner's world-writable /usr/local/bin is. A
// machine without it skips the test.
func withTool(t *testing.T, tl *Tool, name string) {
	t.Helper()
	bin, err := exec.LookPath(name)
	if err != nil {
		t.Skip("no " + name + " on PATH")
	}
	target, err := filepath.EvalSymlinks(bin)
	require.NoError(t, err)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Symlink(target, filepath.Join(dir, name)))
	list := tl.pathList()
	list.Entries = append([]securityconfig.PathEntry{{
		Dir: dir, Target: dir, State: securityconfig.PathAdopted, Source: securityconfig.SourceUser,
	}}, list.Entries...)
	tl.pathList = func() securityconfig.RunPath { return list }
}
