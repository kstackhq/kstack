//go:build !windows

package desktop

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallOnLinuxMakesTheAppImageExecutable(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	installer := downloaded(t, s, "Kstack.AppImage")
	require.NoError(t, os.Chmod(installer, 0o600))

	path, err := s.Install(context.Background(), installer)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}
