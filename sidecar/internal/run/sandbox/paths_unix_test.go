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

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A path that does not exist is resolved through its deepest folder that
// does, so a missing path under a link names where the link leads.
func TestAMissingPathIsResolvedThroughItsDeepestFolder(t *testing.T) {
	base := resolved(t.TempDir())
	target := mkdirs(t, base, "private/var")[0]
	require.NoError(t, os.Symlink(target, filepath.Join(base, "var")))

	assert.Equal(t, filepath.Join(target, "missing", "file"), resolved(filepath.Join(base, "var", "missing", "file")))
}

// Resolved follows a link, so a path through one is Under the folder it
// leads into; Under itself reads the text alone.
func TestUnderComparesResolvedPaths(t *testing.T) {
	base := resolved(t.TempDir())
	inside := mkdirs(t, base, "denied/inside")[0]
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(inside, link))
	denied := Resolved([]string{filepath.Join(base, "denied")})

	assert.True(t, Under(Resolved([]string{link})[0], denied))
	assert.True(t, Under(denied[0], denied))
	assert.False(t, Under(link, denied), "an unresolved link is compared as written")
	assert.False(t, Under(base, denied))
}

// unnameable is a path the kernel cannot name: one through a link to itself.
func unnameable(t *testing.T) string {
	t.Helper()
	loop := filepath.Join(resolved(t.TempDir()), "loop")
	require.NoError(t, os.Symlink(loop, loop))
	return filepath.Join(loop, "svc")
}

func TestCanonicalFailsOnAPathThroughALinkToItself(t *testing.T) {
	svc := unnameable(t)

	_, err := canonical([]string{svc})
	require.ErrorContains(t, err, svc)
	assert.Equal(t, []string{svc}, Canonical([]string{svc}), "kept as given")
}

// The kernel names a path by searching its folders, so none need be listed.
func TestCanonicalNamesAPathUnderAFolderItCannotList(t *testing.T) {
	svc := mkdirs(t, resolved(t.TempDir()), "locked/svc")[0]
	locked := filepath.Dir(svc)
	require.NoError(t, os.Chmod(locked, 0o311))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	missing := filepath.Join(locked, "missing", "x")

	got, err := canonical([]string{svc, missing})
	require.NoError(t, err)
	assert.Equal(t, []string{svc, missing}, got)
}

// A folder this user may not search hides what it holds from a run of theirs
// too, so a path through it is kept as given.
func TestCanonicalKeepsAPathItMayNotReach(t *testing.T) {
	svc := mkdirs(t, resolved(t.TempDir()), "locked/svc")[0]
	locked := filepath.Dir(svc)
	require.NoError(t, os.Chmod(locked, 0o600))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if _, err := os.Stat(svc); err == nil {
		t.Skip("this user searches a folder whatever its mode")
	}

	got, err := canonical([]string{svc})
	require.NoError(t, err)
	assert.Equal(t, []string{svc}, got)
}

// Nothing lies beneath a file, so a path through one is missing. Any other
// failure leaves the path's name unknown.
func TestCanonicalFailsOnAPathTheKernelCannotName(t *testing.T) {
	base := resolved(t.TempDir())
	file := filepath.Join(base, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	through := filepath.Join(file, "x")
	got, err := canonical([]string{through})
	require.NoError(t, err)
	assert.Equal(t, []string{through}, got)

	_, err = canonical([]string{filepath.Join(base, strings.Repeat("n", 300))})
	require.ErrorIs(t, err, syscall.ENAMETOOLONG)
	_, err = Canonical([]string{filepath.Join(base, "a\x00b")})
	require.ErrorIs(t, err, syscall.EINVAL, "no file is named with a NUL")
}
