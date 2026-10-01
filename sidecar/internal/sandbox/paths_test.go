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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkdirs makes each path under base, and answers them joined to it.
func mkdirs(t *testing.T, base string, rels ...string) []string {
	t.Helper()
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = filepath.Join(base, filepath.FromSlash(r))
		require.NoError(t, os.MkdirAll(out[i], 0o700))
	}
	return out
}

// A stand-in machine: a home, a root, and a directory outside both, each
// resolved so the answers compare.
func machine(t *testing.T) (home, root, outside string) {
	t.Helper()
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "home/ana", "usr", "tools")
	return d[0], d[1], d[2]
}

func TestThePathTrees(t *testing.T) {
	home, root, outside := machine(t)
	dirs := mkdirs(t, home, ".asdf/shims", "go/bin", ".krew/bin", ".krew/bin/more")
	inRoot := mkdirs(t, root, "bin")[0]
	tool := mkdirs(t, outside, "bin")[0]

	trees := pathTrees(home, []string{root}, nil, []string{
		// One tree for each first directory below the home.
		dirs[0], dirs[1], dirs[2], dirs[3],
		// Outside the home, at its own path.
		tool,
		// Nothing for a root, the home or above it, a relative entry, or a missing one.
		inRoot, home, filepath.Dir(home), "relative/bin", "", filepath.Join(outside, "no"),
	})

	assert.Equal(t, []string{
		filepath.Join(home, ".asdf"), filepath.Join(home, ".krew"), filepath.Join(home, "go"), tool,
	}, trees)
}

// Under a shared directory an entry names itself: widening it would open
// every other tool's data beside it.
func TestAnEntryUnderASharedDirectoryIsNotWidened(t *testing.T) {
	home, _, _ := machine(t)
	d := mkdirs(t, home, "Library/Application Support/x/bin", ".config/x/bin", ".asdf/shims")

	trees := pathTrees(home, nil, []string{"Library", ".config"}, d)

	assert.Equal(t, []string{filepath.Join(home, ".asdf"), d[1], d[0]}, trees)
}

// A tree inside another is the outer one's already.
func TestATreeInsideAnotherIsDropped(t *testing.T) {
	home, _, outside := machine(t)
	d := mkdirs(t, outside, "opt", "opt/bin", "opt-other")

	assert.Equal(t, []string{d[0], d[2]}, pathTrees(home, nil, nil, []string{d[1], d[2], d[0]}))
}

func TestThePathIsTheEnvironmentsLast(t *testing.T) {
	sep := string(filepath.ListSeparator)
	env := []string{"PATH=/first", "HOME=/w", "PATH=/a" + sep + "/b", "XPATH=/no"}

	assert.Equal(t, []string{"/a", "/b"}, pathOf(env))
	assert.Empty(t, pathOf([]string{"HOME=/w"}))
}

// ~/.local, ~/.local/share and ~/.config hold the user's data and every
// tool's settings, so each counts as a home of its own: an entry under one
// opens the first directory below it, and each itself opens nothing.
func TestLocalItsShareAndConfigCountAsHomes(t *testing.T) {
	home, _, _ := machine(t)
	d := mkdirs(t, home, ".local/bin", ".local/share/JetBrains/Toolbox/scripts", ".local/state/tool/bin",
		".config/composer/vendor/bin", ".config/doctl")

	trees := pathTrees(home, nil, nil, []string{
		d[0], d[1], d[2], d[3],
		filepath.Join(home, ".local"), filepath.Join(home, ".local", "share"), filepath.Join(home, ".config"),
	})

	assert.Equal(t, []string{
		filepath.Join(home, ".config", "composer"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "share", "JetBrains"),
		filepath.Join(home, ".local", "state"),
	}, trees)
}
