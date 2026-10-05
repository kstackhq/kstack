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

package chatsvc

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/read"
	"github.com/kstackhq/kstack/sidecar/internal/tools/write"
)

// fileApprovals is what the real Read and Write, knowing s's closed paths,
// answer for paths under a session of chat c.
func fileApprovals(t *testing.T, s *service, c ChatID, readPath, writePath string) (readA, writeA tools.Approval) {
	t.Helper()
	data := t.TempDir()
	reader, err := read.New(s.security.Hidden, data)
	require.NoError(t, err)
	writer, err := write.New(0o022, s.security.Hidden, data)
	require.NoError(t, err)
	rt := tools.Runtime{Session: s.sessionFor(c, false, false), Dir: s.chatDir(c)}
	raw := func(v map[string]string) json.RawMessage {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return b
	}
	readA, err = reader.Approval(t.Context(), rt, raw(map[string]string{"file_path": readPath}))
	require.NoError(t, err)
	writeA, err = writer.Approval(t.Context(), rt, raw(map[string]string{"file_path": writePath, "content": "x"}))
	require.NoError(t, err)
	return readA, writeA
}

// A rule hand-edited into security.json passes the store's shape check, which
// reads no disk; foldersFor's check refuses it.
func TestFoldersForAnswersOnlyCheckedFolders(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	logs := testutil.CaptureLogs(t)
	for id, r := range map[string]bool{home: true, "/proc": false} {
		rule := folderRule(id, r)
		rule.ID = id
		require.NoError(t, s.security.AddRule(rule))
	}

	assert.Empty(t, s.FoldersFor(t.Context(), c.ID))
	assert.Empty(t, s.sessionFor(c.ID, false, false).GrantedFolders(t.Context()))
	assert.Contains(t, logs.String(), "Your home can be granted read-only.")
	readA, writeA := fileApprovals(t, s, c.ID, "/proc/self/environ", filepath.Join(home, ".zshrc"))
	assert.Equal(t, tools.Approval{}, readA, "Read asks for /proc/self/environ")
	assert.Equal(t, tools.Approval{}, writeA, "Write asks for ~/.zshrc")
	assert.Contains(t, logs.String(), `"folder":"/proc"`, "refused as a fixed mount on Linux, as missing on macOS")
}

func TestNoFolderAppliesWithoutASandboxThroughTheFileTools(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	require.NoError(t, s.GrantFolder(t.Context(), "", home, false))
	require.NoError(t, s.GrantFolder(t.Context(), "", filepath.Join(home, "code"), true))

	readA, writeA := fileApprovals(t, s, c.ID, filepath.Join(home, ".ssh", "config"), filepath.Join(home, "code", "x"))
	assert.Equal(t, tools.Approval{}, readA, "a closed path under a granted home asks")
	require.NotNil(t, writeA.Folder, "a write in the read-write folder runs unasked")

	s.sandboxStatus = sandbox.Status{Reason: "no bwrap"}
	readA, writeA = fileApprovals(t, s, c.ID, filepath.Join(home, "code", "y"), filepath.Join(home, "code", "x"))
	assert.Equal(t, tools.Approval{}, readA, "with no sandbox every folder asks")
	assert.Equal(t, tools.Approval{}, writeA)
}
