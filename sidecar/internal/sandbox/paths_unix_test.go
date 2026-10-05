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

func TestSpelledKeepsAPathUnderAFolderItCannotList(t *testing.T) {
	base := resolved(t.TempDir())
	svc := mkdirs(t, base, "locked/svc")[0]
	locked := filepath.Dir(svc)
	require.NoError(t, os.Chmod(locked, 0o311))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	assert.Equal(t, []string{svc}, Spelled([]string{svc}))
}
