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

package sandbox

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// confining is this machine's sandbox, for a test that needs it to confine,
// or testutil.RequireSandbox's verdict where there is none.
func confining(t *testing.T) *Sandbox {
	t.Helper()
	s, v := Probe(t.Context())
	if s != nil && s.Confines() {
		return s
	}
	why := "no sandbox: " + v.Reason
	if s != nil {
		why = "the sandbox confines nothing: " + v.Reason
	}
	testutil.RequireSandbox(t, why)
	return nil
}

// command is the command s makes for r, which must make one.
func command(t *testing.T, s *Sandbox, ctx context.Context, r Run) *exec.Cmd {
	t.Helper()
	cmd, err := s.Command(ctx, r)
	require.NoError(t, err)
	return cmd
}
