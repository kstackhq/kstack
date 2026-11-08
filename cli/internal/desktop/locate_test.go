package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSystem returns a System for goos whose root, home and environment all
// sit under one temporary directory.
func testSystem(t *testing.T, goos string, env map[string]string) *System {
	t.Helper()
	dir := t.TempDir()
	return &System{
		OS:     goos,
		Arch:   "arm64",
		Root:   filepath.Join(dir, "root"),
		Home:   filepath.Join(dir, "home"),
		Getenv: func(k string) string { return env[k] },
	}
}

// touch creates path, and its parents, holding body.
func touch(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

func TestLocateFindsNothingWhenNothingIsInstalled(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "windows", "linux"} {
		path, err := testSystem(t, goos, nil).Locate()
		require.NoError(t, err, goos)
		assert.Empty(t, path, goos)
	}
}

func TestLocateRefusesAnUnsupportedOS(t *testing.T) {
	t.Parallel()
	_, err := testSystem(t, "plan9", nil).Locate()
	require.EqualError(t, err, "kstack app does not support plan9")
}

func TestLocateOnMacOSPrefersApplicationsOverTheHomeFolders(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "darwin", nil)
	home := filepath.Join(s.Home, "Applications", "Kstack.app")
	require.NoError(t, os.MkdirAll(home, 0o755))
	path, err := s.Locate()
	require.NoError(t, err)
	assert.Equal(t, home, path)

	system := filepath.Join(s.Root, "Applications", "Kstack.app")
	require.NoError(t, os.MkdirAll(system, 0o755))
	path, err = s.Locate()
	require.NoError(t, err)
	assert.Equal(t, system, path)
}

func TestLocateOnWindowsLooksInProgramFiles(t *testing.T) {
	t.Parallel()
	programs := t.TempDir()
	s := testSystem(t, "windows", map[string]string{"ProgramFiles": programs})
	exe := filepath.Join(programs, "Kstack", "kstack-desktop.exe")
	touch(t, exe, "")
	path, err := s.Locate()
	require.NoError(t, err)
	assert.Equal(t, exe, path)
}

// Serial: t.Chdir changes the process's directory.
func TestLocateOnWindowsSkipsAnUnsetFolder(t *testing.T) {
	// With ProgramFiles unset, the candidate must not become a relative path.
	s := testSystem(t, "windows", nil)
	t.Chdir(t.TempDir())
	touch(t, filepath.Join("Kstack", "kstack-desktop.exe"), "")
	path, err := s.Locate()
	require.NoError(t, err)
	assert.Empty(t, path)
}

func TestLocateOnLinuxFindsThePackagesBinary(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	bin := filepath.Join(s.Root, "usr", "bin", "kstack-desktop")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	got, err := s.Locate()
	require.NoError(t, err)
	assert.Empty(t, got, "a directory is not the app")

	require.NoError(t, os.Remove(bin))
	touch(t, bin, "the app")
	got, err = s.Locate()
	require.NoError(t, err)
	assert.Equal(t, bin, got)
}
