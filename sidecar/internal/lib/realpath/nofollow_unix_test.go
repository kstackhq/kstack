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

package realpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSameFileNoFollowTakesAPathWithNoLinkAsItsFile(t *testing.T) {
	bin := mkdirs(t, Resolved(t.TempDir()), "tools/bin")[0]
	assert.True(t, SameFileNoFollow(bin, statOf(t, bin)))
}

func TestSameFileNoFollowFollowsTheOtherPathsLinks(t *testing.T) {
	base := Resolved(t.TempDir())
	bin := mkdirs(t, base, "bin")[0]
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(bin, link))
	assert.True(t, SameFileNoFollow(bin, statOf(t, link)))
}

func TestSameFileNoFollowTellsTwoFoldersApart(t *testing.T) {
	d := mkdirs(t, Resolved(t.TempDir()), "a", "b")
	assert.False(t, SameFileNoFollow(d[0], statOf(t, d[1])))
}

func TestAPathThatIsALinkIsNotSameFileNoFollow(t *testing.T) {
	base := Resolved(t.TempDir())
	bin := mkdirs(t, base, "bin")[0]
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(bin, link))
	assert.False(t, SameFileNoFollow(link, statOf(t, bin)))
}

func TestAPathUnderALinkIsNotSameFileNoFollow(t *testing.T) {
	base := Resolved(t.TempDir())
	bin := mkdirs(t, base, "v2/bin")[0]
	require.NoError(t, os.Symlink(filepath.Join(base, "v2"), filepath.Join(base, "v1")))
	assert.False(t, SameFileNoFollow(filepath.Join(base, "v1", "bin"), statOf(t, bin)))
}

func TestAPathThatIsGoneIsNotSameFileNoFollow(t *testing.T) {
	base := Resolved(t.TempDir())
	assert.False(t, SameFileNoFollow(filepath.Join(base, "gone"), statOf(t, base)))
}

// The kernel follows v1 before it takes the .., so the path holds a link.
func TestAPathThroughALinkAndDotDotIsNotSameFileNoFollow(t *testing.T) {
	base := Resolved(t.TempDir())
	bin := mkdirs(t, base, "opt/v2", "opt/bin")[1]
	require.NoError(t, os.Symlink(filepath.Join(base, "opt", "v2"), filepath.Join(base, "v1")))
	assert.False(t, SameFileNoFollow(base+"/v1/../bin", statOf(t, bin)))
}

// file is the folder that was read, wherever its path leads since.
func TestSameFileNoFollowComparesTheFileNotItsPath(t *testing.T) {
	base := Resolved(t.TempDir())
	was := mkdirs(t, base, "bin")[0]
	file := statOf(t, was)
	now := filepath.Join(base, "moved")
	require.NoError(t, os.Rename(was, now))
	mkdirs(t, base, "bin")

	assert.True(t, SameFileNoFollow(now, file))
	assert.False(t, SameFileNoFollow(was, file))
}

func TestNoFollowIDIsTheRootsOwn(t *testing.T) {
	id, err := NoFollowID("/")
	require.NoError(t, err)
	assert.True(t, id.Is(statOf(t, "/")))
	assert.False(t, id.Is(statOf(t, t.TempDir())))
}

func TestAnIDIsTheSameAsItsFilesOwn(t *testing.T) {
	d := mkdirs(t, Resolved(t.TempDir()), "a", "b")
	a, err := NoFollowID(d[0])
	require.NoError(t, err)
	again, err := NoFollowID(d[0])
	require.NoError(t, err)
	b, err := NoFollowID(d[1])
	require.NoError(t, err)
	assert.True(t, a.Same(again))
	assert.False(t, a.Same(b))
}

func TestARelativePathIsNotFoundNoFollow(t *testing.T) {
	t.Chdir(Resolved(t.TempDir()))
	mkdirs(t, ".", "bin")
	_, err := NoFollowID("bin")
	assert.ErrorIs(t, err, ErrNotFoundNoFollow)
}

func TestAPathThatIsNotCleanIsNotFoundNoFollow(t *testing.T) {
	base := Resolved(t.TempDir())
	mkdirs(t, base, "bin")
	for _, path := range []string{base + "/./bin", base + "/bin/", base + "//bin"} {
		_, err := NoFollowID(path)
		assert.ErrorIs(t, err, ErrNotFoundNoFollow, path)
	}
}

func TestAPathWithALinkOrNoFileIsNotFoundNoFollow(t *testing.T) {
	base := Resolved(t.TempDir())
	bin := mkdirs(t, base, "v2/bin")[0]
	require.NoError(t, os.Symlink(filepath.Join(base, "v2"), filepath.Join(base, "v1")))
	require.NoError(t, os.Symlink(bin, filepath.Join(base, "link")))
	for _, path := range []string{filepath.Join(base, "gone"), filepath.Join(bin, "gone", "bin"), filepath.Join(base, "link"), filepath.Join(base, "v1", "bin")} {
		_, err := NoFollowID(path)
		assert.ErrorIs(t, err, ErrNotFoundNoFollow, path)
	}
}

// A lookup the disk refuses tells nothing of the path, so it is no
// ErrNotFoundNoFollow.
func TestALookupThatFailsIsNotErrNotFoundNoFollow(t *testing.T) {
	_, err := NoFollowID(filepath.Join(Resolved(t.TempDir()), strings.Repeat("n", 4096)))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFoundNoFollow)
}
