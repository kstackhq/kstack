//go:build !windows

package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocateOnLinuxSkipsAFileNobodyCanRun(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	bin := filepath.Join(s.Root, "usr", "bin", "kstack")
	touch(t, bin, "the app")
	require.NoError(t, os.Chmod(bin, 0o644))
	got, err := s.Locate()
	require.NoError(t, err)
	assert.Empty(t, got)
}
