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

package realpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPathUnderAJunctionIsNotSameFileNoFollow(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "v2", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o700))
	junction := filepath.Join(base, "v1")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, filepath.Dir(bin)).CombinedOutput()
	require.NoError(t, err, string(out))
	assert.False(t, SameFileNoFollow(filepath.Join(junction, "bin"), statOf(t, bin)))
}

func TestAPathThatIsNotCleanIsNotSameFileNoFollow(t *testing.T) {
	base := t.TempDir()
	assert.False(t, SameFileNoFollow(base+`\.`, statOf(t, base)))
}

func TestARelativePathIsNotFoundNoFollow(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(base)
	require.NoError(t, os.Mkdir("bin", 0o700))
	_, err = NoFollowID("bin")
	assert.ErrorIs(t, err, ErrNotFoundNoFollow)
}

func TestAPathWithNoFileIsNotFoundNoFollow(t *testing.T) {
	_, err := NoFollowID(filepath.Join(t.TempDir(), "gone"))
	assert.ErrorIs(t, err, ErrNotFoundNoFollow)
}

// A lookup the disk refuses, here for a name Windows does not allow, tells
// nothing of the path, so it is no ErrNotFoundNoFollow.
func TestALookupThatFailsIsNotErrNotFoundNoFollow(t *testing.T) {
	_, err := NoFollowID(filepath.Join(t.TempDir(), "a|b"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFoundNoFollow)
}

func TestSameFileNoFollowTakesAPathWithNoLinkAsItsFile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	bin := filepath.Join(base, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	assert.True(t, SameFileNoFollow(bin, statOf(t, bin)))
}

func TestAnIDIsTheSameAsItsFilesOwn(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	require.NoError(t, os.Mkdir(a, 0o700))
	require.NoError(t, os.Mkdir(b, 0o700))
	idA, err := NoFollowID(a)
	require.NoError(t, err)
	again, err := NoFollowID(a)
	require.NoError(t, err)
	idB, err := NoFollowID(b)
	require.NoError(t, err)
	assert.True(t, idA.Same(again))
	assert.False(t, idA.Same(idB))
}

// A folder set to tell names apart by case holds bin and BIN as two folders,
// which filepath compares as one name.
func TestSameFileNoFollowTellsCaseSensitiveSiblingsApart(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	if out, err := exec.Command("fsutil", "file", "setCaseSensitiveInfo", base, "enable").CombinedOutput(); err != nil {
		t.Skipf("this machine sets no folder case-sensitive: %s", out)
	}
	lower, upper := filepath.Join(base, "bin"), filepath.Join(base, "BIN")
	require.NoError(t, os.Mkdir(lower, 0o700))
	require.NoError(t, os.Mkdir(upper, 0o700))

	assert.True(t, SameFileNoFollow(lower, statOf(t, lower)))
	assert.False(t, SameFileNoFollow(lower, statOf(t, upper)))
}
