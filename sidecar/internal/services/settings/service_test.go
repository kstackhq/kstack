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
	"github.com/stretchr/testify/require"
)

// On a machine with no sandbox the list is empty and every change is refused.
func TestAServiceWithNoSandboxChangesNothing(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.json"))
	require.NoError(t, err)
	s := NewService(store, nil, nil, "")

	assert.Nil(t, s.Path())
	assert.Empty(t, s.PathFault())
	_, err = s.SyncPath(t.Context(), []string{t.TempDir()})
	assert.ErrorIs(t, err, ErrNoSandbox)
	_, err = s.RefreshPath(t.Context())
	assert.ErrorIs(t, err, ErrNoSandbox)
	_, err = s.AdoptPath(t.TempDir(), t.TempDir())
	assert.ErrorIs(t, err, ErrNoSandbox)
	_, err = s.DropPath(t.TempDir())
	assert.ErrorIs(t, err, ErrNoSandbox)
	assert.Empty(t, store.Get().Sandbox.Path)
}

// A stored Target that is another spelling of the fresh folder is the same
// entry: the user's decision holds, and the disk's spelling is stored.
func TestDiffPathKeepsAnEntryStoredInAnotherSpelling(t *testing.T) {
	stored := []PathEntry{{Dir: "/tools", Target: "/TOOLS", State: PathAdopted, Source: SourceUser}}
	fresh := []freshDir{{pathDir: pathDir{Dir: "/tools", Target: "/Tools"}}}

	got := diffPath(stored, fresh, map[string]string{"/TOOLS": "/Tools"}, false)
	assert.Equal(t, []PathEntry{{Dir: "/tools", Target: "/Tools", State: PathAdopted, Source: SourceUser}}, got)
}

// A removal stored in another spelling holds for any entry that leads to the
// folder, and keeps its own Target once the shell no longer lists it.
func TestDiffPathHoldsARemovalStoredInAnotherSpelling(t *testing.T) {
	stored := []PathEntry{{Dir: "/old", Target: "/TOOLS", State: PathGone, Source: SourceUser}}
	fresh := []freshDir{{pathDir: pathDir{Dir: "/new", Target: "/Tools"}, Open: true}}

	got := diffPath(stored, fresh, map[string]string{"/TOOLS": "/Tools"}, false)
	assert.Equal(t, []PathEntry{
		{Dir: "/new", Target: "/Tools", State: PathGone, Source: SourceUser},
		{Dir: "/old", Target: "/TOOLS", State: PathGone, Source: SourceUser},
	}, got)
	assert.Equal(t, "/TOOLS", stored[0].Target, "the stored list is not written")
}
