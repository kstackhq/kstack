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

package settings

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/realpath"
)

// A folder now shared leaves the shell's adoption out of the run, and keeps
// the user's.
func TestCheckRunLeavesOutAShellAdoptedSharedFolder(t *testing.T) {
	fakeGroups(t, "staff")
	z := newZones(t)
	dir := z.dir(t, "open/bin")
	assert.NoError(t, os.Chmod(dir, 0o775))

	check := NewRunCheck(z.zones.Open, nil, "")
	_, _, ok := check.Folder(PathEntry{Dir: dir, Target: dir, State: PathAdopted, Source: SourceShell})
	assert.False(t, ok)
	got, open, ok := check.Folder(PathEntry{Dir: dir, Target: dir, State: PathAdopted, Source: SourceUser})
	assert.True(t, ok)
	assert.True(t, open)
	assert.Equal(t, dir, got)
}

// A folder the sync drops is left out of a run too, a project's among them.
func TestCheckRunAppliesTheSyncsFilter(t *testing.T) {
	z := newZones(t)
	project := z.dir(t, "open/app/node_modules/.bin")
	_, _, ok := NewRunCheck(z.zones.Open, nil, "").Folder(PathEntry{Dir: project, Target: project, State: PathAdopted, Source: SourceUser})
	assert.False(t, ok)
}

// The user approved the folder, so a stored Target that has become a link to
// another folder has moved, and a run leaves it out unless the sync would
// adopt where it leads unasked.
func TestCheckRunLeavesOutAnApprovedFolderThatBecameALink(t *testing.T) {
	z := newZones(t)
	approved := filepath.Join(z.home, "v1")
	require.NoError(t, os.Symlink(z.dir(t, "home/v2"), approved))

	_, _, ok := NewRunCheck(z.zones.Open, nil, "").Folder(PathEntry{Dir: approved, Target: approved, State: PathAdopted, Source: SourceUser})
	assert.False(t, ok)
}

// An approval stored in another spelling of the folder its Dir leads to holds
// for the run.
func TestCheckRunKeepsAnApprovalInAnotherSpelling(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "home/bin")
	spelled := filepath.Join(z.home, "BIN")
	check := NewRunCheck(z.zones.Open, nil, "")
	check.noFollowID = func(path string) (realpath.ID, error) {
		if path == spelled {
			path = dir
		}
		return realpath.NoFollowID(path)
	}

	got, open, ok := check.Folder(PathEntry{Dir: dir, Target: spelled, State: PathAdopted, Source: SourceUser})
	assert.True(t, ok)
	assert.False(t, open)
	assert.Equal(t, dir, got)
}

// An approved Target whose read fails leaves its entry out of the run.
func TestCheckRunLeavesOutAnApprovalWhoseReadFails(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "home/bin")
	check := NewRunCheck(z.zones.Open, nil, "")
	check.noFollowID = func(string) (realpath.ID, error) { return realpath.ID{}, syscall.EIO }

	_, _, ok := check.Folder(PathEntry{Dir: dir, Target: filepath.Join(z.home, "BIN"), State: PathAdopted, Source: SourceUser})
	assert.False(t, ok)
}

// approvedFor is whether a run keeps the user's approval of stored for the
// folder at dir, which the run's open folders do not read.
func approvedFor(t *testing.T, z *zoneFixture, dir, stored string) bool {
	t.Helper()
	_, _, ok := NewRunCheck(z.zones.Open, nil, "").Folder(PathEntry{Dir: dir, Target: stored, State: PathAdopted, Source: SourceUser})
	return ok
}

// An approval stored as the disk spells the folder holds without a lookup.
func TestCheckRunLooksUpNoApprovalInTheFreshSpelling(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "home/bin")
	check := NewRunCheck(z.zones.Open, nil, "")
	check.noFollowID = func(path string) (realpath.ID, error) {
		t.Errorf("the check read %s", path)
		return realpath.ID{}, realpath.ErrNotFoundNoFollow
	}

	_, _, ok := check.Folder(PathEntry{Dir: dir, Target: dir, State: PathAdopted, Source: SourceUser})
	assert.True(t, ok)
}

// A stored Target that differs from the fresh folder by more than case is
// another folder by its text, and is not read.
func TestAStoredTargetThatIsAnotherFolderIsNotRead(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "home/a")
	check := NewRunCheck(z.zones.Open, nil, "")
	check.noFollowID = func(path string) (realpath.ID, error) {
		t.Errorf("the check read %s", path)
		return realpath.NoFollowID(dir)
	}

	_, _, ok := check.Folder(PathEntry{Dir: dir, Target: filepath.Join(z.home, "b"), State: PathAdopted, Source: SourceUser})
	assert.False(t, ok)
}
