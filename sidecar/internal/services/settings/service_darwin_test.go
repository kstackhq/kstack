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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An approval stored in another case of the folder holds, and the sync
// stores the disk's spelling.
func TestASyncKeepsAnApprovalStoredInAnotherCase(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir := z.dir(t, "home/tools")
	setEntry(t, s, PathEntry{Dir: dir, Target: otherCase(t, dir), State: PathAdopted, Source: SourceUser})

	synced(t, s, []string{dir})
	assert.Equal(t, PathEntry{Dir: dir, Target: dir, State: PathAdopted, Source: SourceUser}, entryOf(t, s, dir))
}

// A removal stored in another case holds for any entry that leads to the
// folder, and keeps its own spelling once the shell no longer lists it.
func TestARemovalStoredInAnotherCaseHoldsUnderAnotherDir(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir, old := z.dir(t, "open/tools"), filepath.Join(z.base, "old")
	stored := otherCase(t, dir)
	setEntry(t, s, PathEntry{Dir: old, Target: stored, State: PathGone, Source: SourceUser})

	synced(t, s, []string{dir})
	assert.Equal(t, PathEntry{Dir: dir, Target: dir, State: PathGone, Source: SourceUser}, entryOf(t, s, dir))
	assert.Equal(t, PathEntry{Dir: old, Target: stored, State: PathGone, Source: SourceUser}, entryOf(t, s, old))
}
