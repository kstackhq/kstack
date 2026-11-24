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
	"context"
	"path/filepath"
	"testing"

	"github.com/amorey/gobus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
)

// Each bad element refuses the whole value, naming the element; a good value
// passes.
func TestCheckPathRefusesEachBadElement(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{
		"$PATH", "${PATH}", "/opt/bin", "~/bin:$PATH:/usr/local/bin", "$HOME/go/bin", "${HOME}/go/bin", "/opt/bin/", "//opt/./bin",
	} {
		assert.NoError(t, CheckPath(ok), ok)
	}
	for value, says := range map[string]string{
		"":                "an empty entry",
		"/opt/bin::$PATH": "an empty entry",
		"$PATH:":          "an empty entry",
		"bin":             `"bin" is not a full path`,
		"~bob/bin":        `"~bob/bin" is not a full path`,
		"~":               `"~" is your home`,
		"$HOME":           `"$HOME" is your home`,
		"${HOME}":         `"${HOME}" is your home`,
		"~/":              `"~/" is your home`,
		"$HOME/":          `"$HOME/" is your home`,
		"~/.":             `"~/." is your home`,
		"$PATH/bin":       `"$PATH/bin" mixes $PATH`,
		"/x${PATH}":       `"/x${PATH}" mixes $PATH`,
		"/opt/$TOOLS/bin": `"/opt/$TOOLS/bin" names a variable`,
		"~/$X":            `"~/$X" names a variable`,
		"/opt/x/../bin":   `"/opt/x/../bin" holds ".."`,
		"~/..":            `"~/.." holds ".."`,
		"/opt/b\x00in":    `holds a NUL`,
	} {
		err := CheckPath(value)
		if assert.Error(t, err, value) {
			assert.Contains(t, err.Error(), says, value)
			assert.IsType(t, PathRefusal(""), err, "a refusal in the user's words")
		}
	}
}

// The shell's PATH is spliced in already split: nothing it lists is ever
// typed, and nothing in it is expanded again.
func TestTheShellsPathIsNeverTyped(t *testing.T) {
	t.Parallel()
	shell := []string{"/x", "~/y", "$HOME", "$PATH", "/z/../w"}
	folders, err := ExpandPath("$PATH", shell, "/home/me")
	assert.NoError(t, err)
	assert.Equal(t, []PathFolder{
		{Path: "/x", Origin: OriginShell},
		{Path: "~/y", Origin: OriginShell},
		{Path: "$HOME", Origin: OriginShell},
		{Path: "$PATH", Origin: OriginShell},
		{Path: "/z/../w", Origin: OriginShell},
	}, folders)
}

// A typed folder is expanded against the home and cleaned, in its place
// among the shell's.
func TestATypedFolderIsCleaned(t *testing.T) {
	t.Parallel()
	folders, err := ExpandPath("~/bin/:${PATH}:$HOME/go/bin://opt/./bin/:${HOME}/.cargo/bin", []string{"/usr/bin"}, "/home/me")
	assert.NoError(t, err)
	assert.Equal(t, []PathFolder{
		{Path: "/home/me/bin", Origin: OriginTyped},
		{Path: "/usr/bin", Origin: OriginShell},
		{Path: "/home/me/go/bin", Origin: OriginTyped},
		{Path: "/opt/bin", Origin: OriginTyped},
		{Path: "/home/me/.cargo/bin", Origin: OriginTyped},
	}, folders)

	_, err = ExpandPath("bin", nil, "/home/me")
	assert.Error(t, err, "a value that does not parse expands to nothing")
}

// pathSetting is the setting as a run reads it.
func pathSetting(ctx context.Context, s *Store) (PathSetting, error) {
	setting, _, _, err := s.readPath(ctx)
	return setting, err
}

// The setting starts as $PATH, is saved as written and read back; a value the
// grammar refuses writes nothing.
func TestThePathSettingRoundTrips(t *testing.T) {
	t.Parallel()
	s, _ := openFile(t, "")
	ctx := t.Context()
	got, err := pathSetting(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, PathSetting{Value: "$PATH"}, got)

	require.NoError(t, s.SetPath(ctx, "~/bin:$PATH"))
	got, err = pathSetting(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, PathSetting{Value: "~/bin:$PATH"}, got)

	assert.IsType(t, PathRefusal(""), s.SetPath(ctx, "bin:$PATH"))
	got, err = pathSetting(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, "~/bin:$PATH", got.Value, "a refused value wrote nothing")
}

// A stored value the grammar refuses is read with its refusal, never as
// $PATH.
func TestARefusedPathSettingIsReadWithItsRefusal(t *testing.T) {
	t.Parallel()
	db := appdbtest.Open(t)
	s := openStore(t, filepath.Join(t.TempDir(), "settings.json"), db)
	exec(t, db, `UPDATE sandbox_settings SET path = 'bin:$PATH'`)

	got, err := pathSetting(t.Context(), s)
	require.NoError(t, err)
	assert.Equal(t, "bin:$PATH", got.Value)
	assert.Contains(t, got.Refusal, `"bin" is not a full path`)
}

// A read that fails is an error, never $PATH, and so is a write over a
// missing row, which it never inserts.
func TestAPathSettingReadThatFailsIsAnError(t *testing.T) {
	t.Parallel()
	db := appdbtest.Open(t)
	s := openStore(t, filepath.Join(t.TempDir(), "settings.json"), db)
	exec(t, db, `DELETE FROM sandbox_settings`)
	_, err := pathSetting(t.Context(), s)
	assert.ErrorIs(t, err, ErrNoSandboxSettings)
	assert.ErrorIs(t, s.SetPath(t.Context(), "$PATH"), ErrNoSandboxSettings)

	exec(t, db, `DROP TABLE sandbox_settings`)
	_, err = pathSetting(t.Context(), s)
	assert.Error(t, err)
}

// A save pings once it commits; a refused one pings nothing.
func TestASavedPathSettingPublishes(t *testing.T) {
	t.Parallel()
	s, _ := openFile(t, "")
	rx := s.WatchSandboxPath()
	t.Cleanup(rx.Close)

	require.NoError(t, s.SetPath(t.Context(), "/opt/bin:$PATH"))
	_, err := rx.TryRecv()
	require.NoError(t, err)

	assert.Error(t, s.SetPath(t.Context(), ""))
	_, err = rx.TryRecv()
	assert.ErrorIs(t, err, gobus.ErrEmpty, "a refused save pinged")
}

// The shell's PATH is kept joined and read back split; a write answers
// whether it changed the row, and an empty PATH is never stored.
func TestTheShellPathRow(t *testing.T) {
	t.Parallel()
	s, _ := openFile(t, "")
	ctx := t.Context()
	read := func() ([]string, bool) {
		t.Helper()
		var dirs []string
		var found bool
		require.NoError(t, s.stmts.InTx(ctx, func(st stmts) error {
			var err error
			dirs, found, err = selectShellPath(ctx, st)
			return err
		}))
		return dirs, found
	}
	_, found := read()
	assert.False(t, found, "no row before a first read answers")

	before, changed, err := s.writeShellPath(ctx, []string{"/usr/bin", "/bin"})
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Nil(t, before)
	dirs, found := read()
	assert.True(t, found)
	assert.Equal(t, []string{"/usr/bin", "/bin"}, dirs)

	before, changed, err = s.writeShellPath(ctx, []string{"/usr/bin", "/bin"})
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, []string{"/usr/bin", "/bin"}, before)

	_, _, err = s.writeShellPath(ctx, nil)
	assert.ErrorIs(t, err, ErrEmptyShellPath)
	dirs, _ = read()
	assert.Equal(t, []string{"/usr/bin", "/bin"}, dirs, "an empty PATH wrote nothing")
}

// The file's PATH keys stay in it, unread: neither a list nor a value the
// store cannot read reaches the setting or is refused.
func TestTheFilesPathIsNotRead(t *testing.T) {
	t.Parallel()
	body := `{"sandbox": {"path": [{"dir": "/opt/bin", "target": "/opt/bin", "state": "gone", "source": "user"}, {"dir": "bin"}], "pathStrict": true, "pathResolved": "yes"}}`
	s, file := openFile(t, body)
	assert.Empty(t, s.Refused())
	got, err := pathSetting(t.Context(), s)
	require.NoError(t, err)
	assert.Equal(t, PathSetting{Value: "$PATH"}, got)
	assert.JSONEq(t, body, readFile(t, file), "nothing writes the file")
}

// A shell PATH whose row cannot be read, or written, is an error, and
// nothing changes.
func TestAShellPathWriteThatFailsIsAnError(t *testing.T) {
	t.Parallel()
	db := appdbtest.Open(t)
	s := openStore(t, filepath.Join(t.TempDir(), "settings.json"), db)
	ctx := t.Context()
	looseTable(t, db, "shell_path", `id INTEGER PRIMARY KEY, path TEXT NOT NULL CHECK (path = '/usr/bin')`)
	_, changed, err := s.writeShellPath(ctx, []string{"/bin"})
	assert.Error(t, err, "a write the table refuses")
	assert.False(t, changed)

	exec(t, db, `DROP TABLE shell_path`)
	_, _, err = s.writeShellPath(ctx, []string{"/bin"})
	assert.Error(t, err, "a read of the row it replaces")
}

// The migration writes sandbox_settings' row, so a run over none is a failed
// read, never $PATH.
func TestARunPathOverAMissingSettingsRowIsAFailedRead(t *testing.T) {
	t.Parallel()
	db := appdbtest.Open(t)
	s := openStore(t, filepath.Join(t.TempDir(), "settings.json"), db)
	exec(t, db, `DELETE FROM sandbox_settings`)
	_, _, _, err := s.readPath(t.Context())
	assert.ErrorIs(t, err, ErrNoSandboxSettings)
}
