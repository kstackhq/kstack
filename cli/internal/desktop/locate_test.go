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

func TestLocateOnWindowsLooksInLocalAppDataThenProgramFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	local, programs := filepath.Join(dir, "local"), filepath.Join(dir, "programs")
	s := testSystem(t, "windows", map[string]string{"LOCALAPPDATA": local, "ProgramFiles": programs})

	exe := filepath.Join(programs, "Kstack", "kstack.exe")
	touch(t, exe, "")
	path, err := s.Locate()
	require.NoError(t, err)
	assert.Equal(t, exe, path)

	exe = filepath.Join(local, "Kstack", "kstack.exe")
	touch(t, exe, "")
	path, err = s.Locate()
	require.NoError(t, err)
	assert.Equal(t, exe, path)
}

// Serial: t.Chdir changes the process's directory.
func TestLocateOnWindowsSkipsAnUnsetFolder(t *testing.T) {
	// With LOCALAPPDATA unset, the candidate must not become a relative path.
	s := testSystem(t, "windows", nil)
	t.Chdir(t.TempDir())
	touch(t, filepath.Join("Kstack", "kstack.exe"), "")
	path, err := s.Locate()
	require.NoError(t, err)
	assert.Empty(t, path)
}

func TestLocateOnLinuxFindsThePackagesBinaryButNeverItself(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	bin := filepath.Join(s.Root, "usr", "bin", "kstack")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	got, err := s.Locate()
	require.NoError(t, err)
	assert.Empty(t, got, "a directory is not the app")

	require.NoError(t, os.Remove(bin))
	touch(t, bin, "the app")
	got, err = s.Locate()
	require.NoError(t, err)
	assert.Equal(t, bin, got)

	s.Self = bin
	got, err = s.Locate()
	require.NoError(t, err)
	assert.Empty(t, got, "the CLI installed there is not the app")
}

func TestLocateOnLinuxIgnoresPATH(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "kstack"), "another CLI")
	s := testSystem(t, "linux", map[string]string{"PATH": dir})
	got, err := s.Locate()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLocateOnLinuxReadsTheDesktopEntry(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	exe := filepath.Join(s.Root, "opt", "Kstack", "kstack")
	touch(t, exe, "")
	entry := filepath.Join(s.Root, "usr", "share", "applications", "Kstack.desktop")
	touch(t, entry, "[Desktop Entry]\nName=Kstack\nExec="+exe+" %U\n")
	got, err := s.Locate()
	require.NoError(t, err)
	assert.Equal(t, exe, got)
}

func TestLocateOnLinuxIgnoresADesktopEntryWithoutAnAbsoluteExec(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	for _, body := range []string{"[Desktop Entry]\nExec=kstack\n", "[Desktop Entry]\nExec=\n", "[Desktop Entry]\n"} {
		touch(t, filepath.Join(s.Home, ".local", "share", "applications", "Kstack.desktop"), body)
		got, err := s.Locate()
		require.NoError(t, err)
		assert.Empty(t, got, body)
	}
}

func TestLocateOnLinuxFindsAnAppImage(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	image := filepath.Join(s.Home, ".local", "bin", "Kstack.AppImage")
	touch(t, image, "")
	got, err := s.Locate()
	require.NoError(t, err)
	assert.Equal(t, image, got)

	image = filepath.Join(s.Home, "Applications", "Kstack.AppImage")
	touch(t, image, "")
	got, err = s.Locate()
	require.NoError(t, err)
	assert.Equal(t, image, got)
}
