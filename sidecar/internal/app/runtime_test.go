package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

// The app keeps every service it built, so a caller reaches one through the
// runtime rather than through a constructor's arguments.
func TestNewKeepsTheRuntime(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })

	rt := a.rt
	for name, svc := range map[string]any{
		"DB": rt.DB, "Poke": rt.Poke, "Kubeconfig": rt.Kubeconfig, "Cluster": rt.Cluster,
		"Auth": rt.Auth, "LLM": rt.LLM, "Security": rt.Security,
		"Memory": rt.Memory, "Chat": rt.Chat,
	} {
		assert.NotNil(t, svc, name)
	}
}

// A runtime is built apart from the servers over it, and closes what it built
// on its own. SQLite deletes the -wal beside a database when its last connection
// closes, so the sibling's absence shows app.db was released.
func TestARuntimeClosesWhatItBuilt(t *testing.T) {
	dir := t.TempDir()
	rt, err := build(t.Context(), withDirs(t, Config{DataDir: dir}))
	require.NoError(t, err)

	wal := filepath.Join(dir, "app.db-wal")
	require.FileExists(t, wal)
	require.NoError(t, rt.Close())
	assert.NoFileExists(t, wal)
}

// With no shell the resolver has no prober. The comparison is to a nil
// interface: a nil *bash.Tool stored in one is not nil, and the resolver would
// call through it.
func TestTheResolverHasNoProberWithoutAShell(t *testing.T) {
	rt := &Runtime{SandboxStatus: sandbox.Status{Available: true}}

	assert.True(t, rt.resolver().Executables == nil)
}
