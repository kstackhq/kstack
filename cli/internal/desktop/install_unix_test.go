//go:build !windows

package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetSuffixSkipsAPackageManagerNobodyCanRun(t *testing.T) {
	t.Parallel()
	s := testSystem(t, "linux", nil)
	aptGet := filepath.Join(s.Root, "usr", "bin", "apt-get")
	touch(t, aptGet, "")
	touch(t, filepath.Join(s.Root, "usr", "bin", "dnf"), "")
	require.NoError(t, os.Chmod(aptGet, 0o644))

	suffix, err := s.assetSuffix()
	require.NoError(t, err)
	assert.Equal(t, "-linux-arm64.rpm", suffix)
}
