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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// macOS's filesystems open a folder under any casing of its name, and the
// zones are compared by text, so a grant spelled in another case is refused
// with the disk's spelling, and a stored one has moved.
func TestAFolderSpelledInAnotherCaseIsRefused(t *testing.T) {
	f := newGrantFixture(t)
	upper := func(p string) string { return filepath.Join(filepath.Dir(p), strings.ToUpper(filepath.Base(p))) }
	if _, err := os.Stat(upper(f.home)); err != nil {
		t.Skip("the filesystem is case-sensitive")
	}
	for path, canonical := range map[string]string{
		upper(f.home):                          f.home,
		upper(f.in("code/svc")):                f.in("code/svc"),
		filepath.Join(upper(f.home), ".local"): f.in(".local"),
	} {
		err := CheckFolder(path, true, f.zones, nil)
		var r FolderRefusal
		require.ErrorAs(t, err, &r, path)
		assert.Equal(t, "spelling", r.Rule, path)
		assert.Equal(t, canonical, r.Target, path)
		assert.Equal(t, "moved", ruleOf(t, CheckStoredFolder(path, true, f.zones, nil)), path)
	}
	assert.Equal(t, "home", ruleOf(t, CheckFolder(f.home, true, f.zones, nil)), "spelled as listed, the home's own rule answers")
	assert.Equal(t, "home", ruleOf(t, CheckFolder(f.home, true, Zones{Home: upper(f.home)}, nil)), "a zone in another case is canonical too")
}

func TestWideHoldsTheVolumesOnMacOS(t *testing.T) {
	f := newGrantFixture(t)
	store, err := Open(filepath.Join(t.TempDir(), "settings.json"))
	require.NoError(t, err)
	s := NewService(store, func() Zones { return f.zones }, nil, "")
	assert.Contains(t, s.WideFolders(t.Context()), "/Volumes")
}
