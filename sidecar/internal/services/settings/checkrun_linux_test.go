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

package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/realpath"
)

// tellsCaseApart skips the test on a volume where two names that differ in
// case alone are one file, such as a casefold directory.
func tellsCaseApart(t *testing.T, z *zoneFixture) {
	t.Helper()
	z.dir(t, "home/probe")
	if _, err := os.Stat(filepath.Join(z.home, "PROBE")); err == nil {
		t.Skip("the filesystem folds case")
	}
}

func TestAStoredTargetThatIsAnotherFolderInAnotherCaseIsNotTheFolder(t *testing.T) {
	z := newZones(t)
	tellsCaseApart(t, z)
	assert.False(t, approvedFor(t, z, z.dir(t, "home/bin"), z.dir(t, "home/BIN")))
}

func TestAStoredTargetThatIsGoneIsNotTheFolder(t *testing.T) {
	z := newZones(t)
	tellsCaseApart(t, z)
	assert.False(t, approvedFor(t, z, z.dir(t, "home/bin"), filepath.Join(z.home, "BIN")))
}

// A stored Target whose parent is a link has moved, though it leads to the
// fresh folder.
func TestAStoredTargetUnderALinkIsNotTheFolder(t *testing.T) {
	z := newZones(t)
	tellsCaseApart(t, z)
	fresh := z.dir(t, "home/v2/bin")
	require.NoError(t, os.Symlink(filepath.Dir(fresh), filepath.Join(z.home, "V2")))
	assert.False(t, approvedFor(t, z, fresh, filepath.Join(z.home, "V2", "bin")))
}

// The stored Target is compared with the folder the filter checked. A fresh
// path that leads to the stored folder only since then carries no approval.
func TestAFreshPathSwappedAfterTheFilterIsNotTheFolder(t *testing.T) {
	z := newZones(t)
	tellsCaseApart(t, z)
	stored, fresh := z.dir(t, "home/BIN"), z.dir(t, "home/bin")
	check := NewRunCheck(z.zones.Open, nil, "")
	// The lookup runs after the filter read fresh.
	check.noFollowID = func(path string) (realpath.ID, error) {
		assert.NoError(t, os.Remove(fresh))
		assert.NoError(t, os.Symlink(stored, fresh))
		return realpath.NoFollowID(path)
	}

	_, _, ok := check.Folder(PathEntry{Dir: fresh, Target: stored, State: PathAdopted, Source: SourceUser})
	assert.False(t, ok)
}
