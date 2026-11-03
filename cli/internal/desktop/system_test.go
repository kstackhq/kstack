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
	self, err := os.Executable()
	require.NoError(t, err)

	assert.Equal(t, runtime.GOOS, s.OS)
	assert.Equal(t, runtime.GOARCH, s.Arch)
	assert.Equal(t, string(filepath.Separator), s.Root)
	assert.Equal(t, home, s.Home)
	assert.Equal(t, self, s.Self)
	assert.Equal(t, "https://kstack.sh/download/{os}/{arch}", s.URL)
	assert.Equal(t, 30*time.Second, s.Stall)
	assert.Equal(t, os.Getenv("PATH"), s.Getenv("PATH"))
}

// The test binary stands in for a command: with -test.run=^$ it runs no test
// and exits 0, and with an unknown flag it exits 2.

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
		"windows": {`C:\Kstack\kstack.exe`},
		"linux":   {"/usr/bin/kstack"},
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
