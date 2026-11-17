package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate/sqlitemigratetest"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
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
		"Auth": rt.Auth, "LLM": rt.LLM, "Security": rt.Settings,
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

// An app.db that cannot open fails the build, saying so.
func TestAnAppDBThatCannotOpenFailsTheBuild(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "app.db"), 0o700))

	_, err := build(t.Context(), withDirs(t, Config{DataDir: dir}))

	assert.ErrorContains(t, err, "open app database")
}

// An app.db a newer build migrated fails the build with the text the start screen
// shows: the file and both versions.
func TestANewerAppDBFailsTheBuildNamingTheVersions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, err := appdb.Open(path, 0)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	sqlitemigratetest.StampNewer(t, path)

	_, err = build(t.Context(), withDirs(t, Config{DataDir: dir}))

	assert.EqualError(t, err, "app.db was written by a newer Kstack (schema 999; this build reads up to 1)")
	requireUnlocked(t, dir)
}

// With no shell the resolver has no prober. The comparison is to a nil
// interface: a nil *bash.Tool stored in one is not nil, and the resolver would
// call through it.
func TestTheResolverHasNoProberWithoutAShell(t *testing.T) {
	rt := &Runtime{SandboxStatus: sandbox.Status{Available: true}}

	assert.True(t, rt.resolver().Executables == nil)
}

// The resolver and the chat service list the build's cloud platforms in its
// order.
func TestCloudTargetsKeepTheBuildsOrder(t *testing.T) {
	got := cloudTargets([]bash.CloudPlatform{targetOnly(permissions.GCP), targetOnly(permissions.AWS)})
	assert.Equal(t, []permissions.Target{permissions.GCP, permissions.AWS}, got)
	assert.Empty(t, cloudTargets(nil))
}

// With a sandbox the cloud platforms are the build's, or a test's in their
// place; without one there are none.
func TestCloudPlatformsNeedASandbox(t *testing.T) {
	sandboxed := sandbox.Status{Available: true}
	configured := []bash.CloudPlatform{targetOnly(permissions.AWS)}
	assert.Equal(t, cloudTargets(builtCloudPlatforms()), cloudTargets(cloudPlatformsFor(sandboxed, nil)))
	assert.Equal(t, []permissions.Target{permissions.AWS}, cloudTargets(cloudPlatformsFor(sandboxed, configured)))
	assert.Empty(t, cloudPlatformsFor(sandbox.Status{Reason: "no bwrap"}, nil))
	assert.Empty(t, cloudPlatformsFor(sandbox.Status{}, configured))
}

// targetOnly is a cloud platform that names its target and mounts nothing.
type targetOnly permissions.Target

func (p targetOnly) Target() permissions.Target { return permissions.Target(p) }

func (targetOnly) Mount(context.Context, bash.CloudRun) (bash.CloudMount, error) {
	return bash.CloudMount{}, nil
}
