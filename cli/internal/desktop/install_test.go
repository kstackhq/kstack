package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commands records each command Run is given, and runs act for it.
type commands struct {
	got []string
	act func(name string, args []string) error
}

func (c *commands) run(_ context.Context, name string, args ...string) error {
	c.got = append(c.got, strings.Join(append([]string{filepath.Base(name)}, args...), " "))
	if c.act == nil {
		return nil
	}
	return c.act(name, args)
}

// downloaded makes an installer in a directory of its own, as Download does.
func downloaded(t *testing.T, s *System, name string) string {
	t.Helper()
	s.TempDir = t.TempDir()
	dir, err := os.MkdirTemp(s.TempDir, installerDir)
	require.NoError(t, err)
	installer := filepath.Join(dir, name)
	touch(t, installer, "installer bytes")
	return installer
}

func TestInstallOnMacOSCopiesTheAppOutOfTheDiskImage(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "darwin", nil)
	installer := downloaded(t, s, "Kstack.dmg")
	mnt := filepath.Join(filepath.Dir(installer), "mnt")
	app := filepath.Join(s.Home, "Applications", "Kstack.app")
	c := &commands{act: func(name string, args []string) error {
		if name == "ditto" {
			return os.MkdirAll(args[1], 0o755)
		}
		return nil
	}}
	s.Run = c.run

	path, err := s.Install(context.Background(), installer)
	require.NoError(t, err)
	assert.Equal(t, app, path)
	assert.Equal(t, []string{
		"hdiutil attach -nobrowse -readonly -mountpoint " + mnt + " " + installer,
		"ditto " + filepath.Join(mnt, "Kstack.app") + " " + app,
		"hdiutil detach " + mnt,
	}, c.got)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnMacOSStopsWhenTheImageWillNotMount(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "darwin", nil)
	installer := downloaded(t, s, "Kstack.dmg")
	c := &commands{act: func(string, []string) error { return errStep }}
	s.Run = c.run

	_, err := s.Install(context.Background(), installer)
	require.ErrorIs(t, err, errStep)
	assert.Len(t, c.got, 1)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnMacOSDetachesTheImageWhenTheCopyFails(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "darwin", nil)
	installer := downloaded(t, s, "Kstack.dmg")
	c := &commands{act: func(name string, _ []string) error {
		if name == "ditto" {
			return errStep
		}
		return nil
	}}
	s.Run = c.run

	_, err := s.Install(context.Background(), installer)
	require.ErrorIs(t, err, errStep)
	assert.Equal(t, "hdiutil detach "+filepath.Join(filepath.Dir(installer), "mnt"), c.got[len(c.got)-1])
}

func TestInstallOnWindowsRunsTheSetupSilently(t *testing.T) {
	t.Parallel()
	local := t.TempDir()
	s := testSystem(t, "windows", map[string]string{"LOCALAPPDATA": local})
	installer := downloaded(t, s, "Kstack-setup.exe")
	exe := filepath.Join(local, "Kstack", "kstack-desktop.exe")
	c := &commands{act: func(string, []string) error {
		touch(t, exe, "")
		return nil
	}}
	s.Run = c.run

	path, err := s.Install(context.Background(), installer)
	require.NoError(t, err)
	assert.Equal(t, exe, path)
	assert.Equal(t, []string{"Kstack-setup.exe /S"}, c.got)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnWindowsFailsWhenTheSetupFails(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "windows", nil)
	installer := downloaded(t, s, "Kstack-setup.exe")
	s.Run = (&commands{act: func(string, []string) error { return errStep }}).run

	_, err := s.Install(context.Background(), installer)
	require.ErrorIs(t, err, errStep)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnLinuxPlacesTheAppImageInApplications(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	installer := downloaded(t, s, "Kstack.AppImage")

	path, err := s.Install(context.Background(), installer)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(s.Home, "Applications", "Kstack.AppImage"), path)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "installer bytes", string(body))
	assertEmpty(t, s.TempDir)
}

func TestInstallOnLinuxFailsWhenApplicationsCannotBeMade(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	installer := downloaded(t, s, "Kstack.AppImage")
	touch(t, filepath.Join(s.Home, "Applications"), "a file, not a folder")

	_, err := s.Install(context.Background(), installer)
	require.Error(t, err)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnLinuxFailsWhenTheAppImageCannotBeWritten(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	installer := downloaded(t, s, "Kstack.AppImage")
	require.NoError(t, os.MkdirAll(filepath.Join(s.Home, "Applications", "Kstack.AppImage"), 0o755))

	_, err := s.Install(context.Background(), installer)
	require.Error(t, err)
}

func TestInstallOnLinuxFailsWhenTheInstallerIsGone(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	installer := downloaded(t, s, "Kstack.AppImage")
	require.NoError(t, os.Remove(installer))

	_, err := s.Install(context.Background(), installer)
	require.Error(t, err)
}

func TestInstallFailsWhenTheAppIsNotWhereTheInstallerPutsIt(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "windows", nil)
	installer := downloaded(t, s, "Kstack-setup.exe")
	s.Run = (&commands{}).run

	_, err := s.Install(context.Background(), installer)
	require.EqualError(t, err, "the installer finished, but Kstack is not where it installs")
}

func TestInstallRefusesAnUnsupportedOS(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "plan9", nil)
	installer := downloaded(t, s, "Kstack")

	_, err := s.Install(context.Background(), installer)
	require.EqualError(t, err, "kstack app does not support plan9")
	assertEmpty(t, s.TempDir)
}
