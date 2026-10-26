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

//go:build windows

package fileguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A junction is a link: Go reports it as ModeIrregular, so Open reads its tag.
// mklink /J needs no privilege, where a symbolic link does.
func TestOpenRefusesAJunction(t *testing.T) {
	target := t.TempDir()
	junction := filepath.Join(t.TempDir(), "j")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput()
	require.NoError(t, err, string(out))

	_, err = Open(junction)
	var link ErrLink
	require.ErrorAs(t, err, &link)
	assert.Equal(t, target, link.Target)
}

// Either separator is plain and both answer one native path; a Git Bash drive
// path is native; plainness is judged before the conversion; a UNC or device
// path is refused.
func TestAbsRefusesAPathThatIsNotPlain(t *testing.T) {
	for p, want := range map[string]string{
		`C:/Users/x`: `C:\Users\x`,
		`C:\Users\x`: `C:\Users\x`,
		`/c/Users/x`: `C:\Users\x`,
		`C:\`:        `C:\`,
	} {
		got, err := Abs(p)
		require.NoError(t, err, p)
		assert.Equal(t, want, got, p)
	}

	for p, plain := range map[string]string{
		`/c/a/../b`: `/c/b`,
		`C:\a\..\b`: `C:\b`,
		`C:/a//b`:   `C:/a/b`,
		`C:\a\`:     `C:\a`,
	} {
		_, err := Abs(p)
		var np ErrNotPlain
		require.ErrorAs(t, err, &np, p)
		assert.Equal(t, plain, np.Plain, p)
	}

	for _, p := range []string{`\\host\share\x`, `\\?\C:\x`, `\\.\x`, `//host/share/x`} {
		_, err := Abs(p)
		assert.ErrorIs(t, err, ErrNotLocal, p)
	}
	for _, p := range []string{`\x`, `C:x`, `/tmp/x`} {
		_, err := Abs(p)
		assert.ErrorIs(t, err, ErrNotAbs, p)
	}
}

// Named compares by name, and Windows compares names without case: the data
// directory in another case is under the fence by name as well.
func TestNamedCatchesTheDataDirInAnotherCase(t *testing.T) {
	f, upper := otherCase(t)

	assert.True(t, f.Named(upper))
}

// A file that is not a reparse point has no tag; a path that cannot name a
// directory entry has none to read.
func TestReparseTag(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	tag, err := reparseTag(file)
	require.NoError(t, err)
	assert.Zero(t, tag)

	_, err = reparseTag(filepath.Join(t.TempDir(), "missing"))
	assert.Error(t, err)
	_, err = reparseTag("C:\\a\x00b")
	assert.Error(t, err)
}
