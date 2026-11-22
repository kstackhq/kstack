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

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

// A subfolder linked elsewhere joins the fence, so the files it leads to are
// Kstack's for every fence; one that is a folder adds nothing.
func TestALinkedSubfolderIsFenced(t *testing.T) {
	newConfig := func() (string, Config) {
		root := t.TempDir()
		return root, Config{
			DataDir:    filepath.Join(root, "data"),
			CacheDir:   filepath.Join(root, "cache"),
			RuntimeDir: filepath.Join(root, "runtime"),
		}
	}
	for _, sub := range []string{
		"data/chats", "data/monitor",
		"cache/logs", "cache/kube", "cache/tmp", "cache/kubectl",
		"runtime/shell", "runtime/runs",
	} {
		root, cfg := newConfig()
		link := filepath.Join(root, sub)
		elsewhere := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Dir(link), 0o700))
		require.NoError(t, os.Symlink(elsewhere, link))

		denied := pathsOf(cfg).Bash.DeniedDirs
		assert.Equal(t, []string{cfg.DataDir, cfg.CacheDir, cfg.RuntimeDir, link}, denied)
		assert.True(t, sandbox.Under(sandbox.Resolved([]string{filepath.Join(elsewhere, "x")})[0], sandbox.Resolved(denied)), sub)
	}

	root, cfg := newConfig()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cache/kube"), 0o700))
	assert.Equal(t, []string{cfg.DataDir, cfg.CacheDir, cfg.RuntimeDir}, pathsOf(cfg).Bash.DeniedDirs)
}

// A subfolder whose Lstat fails for any reason but absence may be a link, so
// it joins the fence. A root that is a file fails it with ENOTDIR.
func TestASubfolderThatCannotBeReadIsFenced(t *testing.T) {
	root := t.TempDir()
	cfg := Config{
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
		RuntimeDir: filepath.Join(root, "runtime"),
	}
	require.NoError(t, os.WriteFile(cfg.RuntimeDir, nil, 0o600))

	assert.Equal(t, []string{
		cfg.DataDir, cfg.CacheDir, cfg.RuntimeDir,
		filepath.Join(cfg.RuntimeDir, "shell"), filepath.Join(cfg.RuntimeDir, "runs"),
	}, pathsOf(cfg).Bash.DeniedDirs)
}
