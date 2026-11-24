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

	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
)

// On a machine with no sandbox the list is empty and every change is refused.
func TestAServiceWithNoSandboxChangesNothing(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.json"), appdbtest.Open(t))
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

// A stored state holds for its exact Target: an approval or a removal stored
// in another spelling of a fresh folder does not carry to it.
func TestDiffPathComparesTargetsAsText(t *testing.T) {
	stored := []PathEntry{
		{Dir: "/tools", Target: "/TOOLS", State: PathAdopted, Source: SourceUser},
		{Dir: "/old", Target: "/BIN", State: PathGone, Source: SourceUser},
	}
	fresh := []freshDir{
		{pathDir: pathDir{Dir: "/tools", Target: "/Tools"}},
		{pathDir: pathDir{Dir: "/new", Target: "/Bin"}, Open: true},
	}

	assert.Equal(t, []PathEntry{
		{Dir: "/tools", Target: "/Tools", State: PathPending, Source: SourceShell},
		{Dir: "/new", Target: "/Bin", State: PathAdopted, Source: SourceShell},
		{Dir: "/old", Target: "/BIN", State: PathGone, Source: SourceUser},
	}, diffPath(stored, fresh, false))
}
