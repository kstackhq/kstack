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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Entries survive a reopen in order, with their states and sources; an entry
// the check refuses is left out, listed, and held.
func TestPathEntriesPersist(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	entries := []PathEntry{
		{Dir: b, Target: b, State: PathAdopted, Source: SourceShell},
		{Dir: a, Target: c, State: PathPending, Source: SourceShell, Shared: true},
		{Dir: c, Target: c, State: PathGone, Source: SourceUser},
	}
	s, err := Open(file)
	require.NoError(t, err)
	require.NoError(t, s.Update(func(v *Settings) error {
		v.Sandbox.Path = entries
		return nil
	}, FieldPath))

	s, err = Open(file)
	require.NoError(t, err)
	assert.Equal(t, entries, s.Get().Sandbox.Path)
	assert.Empty(t, s.Refused())

	bad := append(entries,
		PathEntry{Dir: "bin", Target: a, State: PathAdopted, Source: SourceShell},
		PathEntry{Dir: b, Target: b, State: PathAdopted, Source: SourceShell},
		PathEntry{Dir: filepath.Join(a, "x"), Target: a, State: "maybe", Source: SourceShell},
		PathEntry{Dir: filepath.Join(a, "y"), Target: a, State: PathAdopted, Source: "them"},
		PathEntry{Dir: filepath.Join(a, "z"), Target: "z", State: PathAdopted, Source: SourceShell},
	)
	raw, err := json.Marshal(map[string]any{"sandbox": map[string]any{"path": bad}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, raw, 0o600))

	s, err = Open(file)
	require.NoError(t, err)
	assert.Equal(t, entries, s.Get().Sandbox.Path, "the entries that passed load as they are")
	refused := s.Refused()
	require.Len(t, refused, 5)
	assert.Equal(t, "bin", refused[0].Value)
	assert.Equal(t, b, refused[1].Value)
	for _, r := range refused {
		assert.Equal(t, FieldPath, r.Field)
	}
	assert.True(t, s.Held(FieldPath))
}
