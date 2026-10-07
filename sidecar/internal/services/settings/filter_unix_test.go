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
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGroups answers every gid's name as name, or fails when name is "",
// and starts the cache empty.
func fakeGroups(t *testing.T, name string) *int {
	t.Helper()
	lookups := 0
	was := lookupGroupID
	lookupGroupID = func(gid string) (*user.Group, error) {
		lookups++
		if name == "" {
			return nil, errors.New("no such group")
		}
		return &user.Group{Gid: gid, Name: name}, nil
	}
	groupNames.Clear()
	t.Cleanup(func() {
		lookupGroupID = was
		groupNames.Clear()
	})
	return &lookups
}

// dirWithMode makes a folder under base and sets its mode, past the umask.
func dirWithMode(t *testing.T, base, name string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(base, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.Chmod(dir, mode))
	return dir
}

func TestFilterPathDropsEachRule(t *testing.T) {
	fakeGroups(t, "staff")
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	bin := dirWithMode(t, base, "bin", 0o755)
	project := dirWithMode(t, base, "app/node_modules/.bin", 0o755)
	again := filepath.Join(base, "again")
	require.NoError(t, os.Symlink(bin, again))
	group := dirWithMode(t, base, "group", 0o775)
	groupSticky := dirWithMode(t, base, "group-sticky", 0o775|os.ModeSticky)
	other := dirWithMode(t, base, "other", 0o755)

	// The sandbox's rules are its own test's; one stands in to show its
	// reason is counted beside the list's.
	kept, dropped := filterPath([]string{other, "bin", project, bin, again, group, groupSticky}, zones{})

	assert.Equal(t, []pathDir{
		{Dir: other, Target: other},
		{Dir: bin, Target: bin},
		{Dir: group, Target: group, Shared: true},
		{Dir: groupSticky, Target: groupSticky, Shared: true},
	}, kept, "in the shell's order")
	assert.Equal(t, map[string]int{"relative": 1, "project": 1, "duplicate": 1}, dropped)
}

// A folder an administrators' group can write adds no writer, so it is not
// shared; a group that cannot be looked up is.
func TestFilterPathSharesNothingWithAnAdminGroup(t *testing.T) {
	group := dirWithMode(t, t.TempDir(), "group", 0o775)

	fakeGroups(t, adminGroups[0])
	kept, _ := filterPath([]string{group}, zones{})
	require.Len(t, kept, 1)
	assert.False(t, kept[0].Shared)

	fakeGroups(t, "")
	kept, _ = filterPath([]string{group}, zones{})
	require.Len(t, kept, 1)
	assert.True(t, kept[0].Shared)
}

// gid 0 is an administrators' group whatever it is called.
func TestGidZeroIsAnAdminGroup(t *testing.T) {
	lookups := fakeGroups(t, "staff")
	assert.True(t, adminGroup(0))
	assert.Zero(t, *lookups)
	assert.False(t, adminGroup(20))
}

// A group's name is looked up once for the sidecar's life.
func TestAGroupIsLookedUpOnce(t *testing.T) {
	lookups := fakeGroups(t, "staff")
	group := dirWithMode(t, t.TempDir(), "group", 0o775)
	filterPath([]string{group}, zones{})
	filterPath([]string{group}, zones{})
	assert.Equal(t, 1, *lookups)
}
