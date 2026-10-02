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

package securityconfig

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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
