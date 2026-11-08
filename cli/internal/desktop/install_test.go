package desktop

import (
	"bytes"
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
	installer := downloaded(t, s, "kstack-desktop-1.2.0-darwin-universal.dmg")
	mnt := filepath.Join(filepath.Dir(installer), "mnt")
	app := filepath.Join(s.Home, "Applications", "Kstack.app")
	c := &commands{act: func(name string, args []string) error {
		if name == "ditto" {
			return os.MkdirAll(args[1], 0o755)
		}
		return nil
	}}
	s.Run = c.run

	path, err := s.Install(context.Background(), installer, &bytes.Buffer{})
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
	installer := downloaded(t, s, "kstack-desktop-1.2.0-darwin-universal.dmg")
	c := &commands{act: func(string, []string) error { return errStep }}
	s.Run = c.run

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.ErrorIs(t, err, errStep)
	assert.Len(t, c.got, 1)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnMacOSDetachesTheImageWhenTheCopyFails(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "darwin", nil)
	installer := downloaded(t, s, "kstack-desktop-1.2.0-darwin-universal.dmg")
	c := &commands{act: func(name string, _ []string) error {
		if name == "ditto" {
			return errStep
		}
		return nil
	}}
	s.Run = c.run

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.ErrorIs(t, err, errStep)
	assert.Equal(t, "hdiutil detach "+filepath.Join(filepath.Dir(installer), "mnt"), c.got[len(c.got)-1])
}

func TestInstallOnWindowsRunsTheMSI(t *testing.T) {
	t.Parallel()
	programs := t.TempDir()
	s := testSystem(t, "windows", map[string]string{"ProgramFiles": programs})
	installer := downloaded(t, s, "kstack-desktop-1.2.0-windows-arm64.msi")
	exe := filepath.Join(programs, "Kstack", "kstack-desktop.exe")
	c := &commands{act: func(string, []string) error {
		touch(t, exe, "")
		return nil
	}}
	s.Run = c.run

	path, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, exe, path)
	assert.Equal(t, []string{"msiexec /i " + installer + " /passive /norestart"}, c.got)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnWindowsFailsWhenTheMSIFails(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "windows", nil)
	installer := downloaded(t, s, "kstack-desktop-1.2.0-windows-arm64.msi")
	s.Run = (&commands{act: func(string, []string) error { return errStep }}).run

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.ErrorIs(t, err, errStep)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnLinuxInstallsThePackageThroughSudo(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"kstack-desktop-1.2.0-linux-arm64.deb": "sudo apt-get install -y ",
		"kstack-desktop-1.2.0-linux-arm64.rpm": "sudo dnf install -y ",
	} {
		s := testSystem(t, "linux", nil)
		installer := downloaded(t, s, name)
		bin := filepath.Join(s.Root, "usr", "bin", "kstack-desktop")
		c := &commands{act: func(string, []string) error {
			touch(t, bin, "")
			return nil
		}}
		s.Interact = c.run
		var out bytes.Buffer

		path, err := s.Install(context.Background(), installer, &out)
		require.NoError(t, err, name)
		assert.Equal(t, bin, path)
		assert.Equal(t, []string{want + installer}, c.got)
		assert.Equal(t, "Running "+want+installer+"\n", out.String())
		assertEmpty(t, s.TempDir)
	}
}

func TestInstallOnLinuxSkipsSudoAsRoot(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	s.IsRoot = true
	installer := downloaded(t, s, "kstack-desktop-1.2.0-linux-arm64.deb")
	c := &commands{act: func(string, []string) error { return errStep }}
	s.Interact = c.run

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.ErrorIs(t, err, errStep)
	assert.Equal(t, []string{"apt-get install -y " + installer}, c.got)
	assertEmpty(t, s.TempDir)
}

func TestInstallOnLinuxRefusesAnythingButAPackage(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	installer := downloaded(t, s, "kstack-desktop-1.2.0-linux-arm64.tar.gz")
	c := &commands{}
	s.Interact = c.run

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.EqualError(t, err, "kstack app cannot install kstack-desktop-1.2.0-linux-arm64.tar.gz")
	assert.Empty(t, c.got)
}

func TestInstallFailsWhenTheAppIsNotWhereTheInstallerPutsIt(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "windows", nil)
	installer := downloaded(t, s, "kstack-desktop-1.2.0-windows-arm64.msi")
	s.Run = (&commands{}).run

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.EqualError(t, err, "the installer finished, but Kstack is not where it installs")
}

func TestInstallRefusesAnUnsupportedOS(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "plan9", nil)
	installer := downloaded(t, s, "kstack-desktop")

	_, err := s.Install(context.Background(), installer, &bytes.Buffer{})
	require.EqualError(t, err, "kstack app does not support plan9")
	assertEmpty(t, s.TempDir)
}
