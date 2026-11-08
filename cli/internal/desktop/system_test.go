package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSystemDescribesThisMachine(t *testing.T) {
	t.Parallel()
	s, err := NewSystem()
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, runtime.GOOS, s.OS)
	assert.Equal(t, runtime.GOARCH, s.Arch)
	assert.Equal(t, string(filepath.Separator), s.Root)
	assert.Equal(t, home, s.Home)
	assert.Equal(t, "https://api.github.com/repos/kstackhq/kstack/releases?per_page=100", s.Releases)
	assert.Equal(t, os.Geteuid() == 0, s.IsRoot)
	assert.Equal(t, 30*time.Second, s.Stall)
	assert.Equal(t, os.Getenv("PATH"), s.Getenv("PATH"))
}

// The test binary stands in for a command: with -test.run=^$ it runs no test
// and exits 0, with -test.list=^$ it also prints nothing, and with an unknown
// flag it exits 2.

func TestRunWaitsForTheCommand(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, run(context.Background(), self, "-test.run=^$"))
}

func TestRunReportsAFailedCommandsOutput(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	err = run(context.Background(), self, "-test.nosuchflag")
	require.ErrorContains(t, err, "exit status 2")
	require.ErrorContains(t, err, "flag provided but not defined")
}

func TestInteractWaitsForTheCommand(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, interact(context.Background(), self, "-test.list=^$"))
}

func TestInteractReportsAFailedCommand(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	err = interact(context.Background(), self, "-test.nosuchflag")
	require.ErrorContains(t, err, "exit status 2")
}

func TestStartReturnsWithoutWaiting(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, start(self, "-test.run=^$"))
}

func TestStartFailsForAMissingProgram(t *testing.T) {
	t.Parallel()
	require.Error(t, start(filepath.Join(t.TempDir(), "missing")))
}

func TestLaunchStartsTheApp(t *testing.T) {
	t.Parallel()
	for goos, want := range map[string][]string{
		"darwin":  {"open", "-a", "/Applications/Kstack.app"},
		"windows": {`C:\Kstack\kstack-desktop.exe`},
		"linux":   {"/usr/bin/kstack-desktop"},
	} {
		var got []string
		s := testSystem(t, goos, nil)
		s.Start = func(name string, args ...string) error {
			got = append([]string{name}, args...)
			return errStep
		}
		path := want[len(want)-1]
		require.ErrorIs(t, s.Launch(path), errStep, goos)
		assert.Equal(t, want, got, goos)
	}
}
