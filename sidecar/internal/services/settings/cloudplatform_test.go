// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package settings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

var cloudPlatforms = []permissions.Target{permissions.AWS, permissions.GitHub, permissions.GCP, permissions.Azure}

func TestEachCloudPlatformStartsOn(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	sw, err := s.CloudPlatformSwitches(t.Context())
	require.NoError(t, err)
	for _, target := range cloudPlatforms {
		assert.True(t, sw.On(target), target)
	}
	assert.False(t, sw.On(permissions.Cluster), "the cluster has no switch")
}

// A write turns one switch, and leaves the others.
func TestSetCloudPlatformSwitchWritesOneSwitch(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	for i, target := range cloudPlatforms {
		require.NoError(t, s.SetCloudPlatformSwitch(ctx, target, false))
		sw, err := s.CloudPlatformSwitches(ctx)
		require.NoError(t, err)
		for j, other := range cloudPlatforms {
			assert.Equal(t, j > i, sw.On(other), "%s after %s is off", other, target)
		}
	}
	require.NoError(t, s.SetCloudPlatformSwitch(ctx, permissions.GitHub, true))
	require.NoError(t, s.SetCloudPlatformSwitch(ctx, permissions.GitHub, true), "a write of the value it holds")
	sw, err := s.CloudPlatformSwitches(ctx)
	require.NoError(t, err)
	assert.Equal(t, CloudPlatformSwitches{GitHub: true}, sw)
}

func TestTheClusterHasNoCloudPlatformSwitch(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	assert.ErrorIs(t, s.SetCloudPlatformSwitch(t.Context(), permissions.Cluster, false), ErrNoCloudPlatformSwitch)
	sw, err := s.CloudPlatformSwitches(t.Context())
	require.NoError(t, err)
	assert.Equal(t, CloudPlatformSwitches{AWS: true, GitHub: true, GCP: true, Azure: true}, sw, "nothing was written")
}

// A read that fails is an error, never a default: each caller decides what it
// reads as.
func TestACloudPlatformSwitchReadThatFailsIsAnError(t *testing.T) {
	t.Parallel()
	db := appdbtest.Open(t)
	s := openStore(t, db)
	exec(t, db, `DROP TABLE sandbox_settings`)

	_, err := s.CloudPlatformSwitches(t.Context())
	assert.Error(t, err)
	assert.Error(t, s.SetCloudPlatformSwitch(t.Context(), permissions.AWS, false))
}

// The migration writes the one row, so a missing row is a failed read; a
// write does not put it back, which would turn on the switches that read off.
func TestAMissingSandboxSettingsRowIsAFailedRead(t *testing.T) {
	t.Parallel()
	db := appdbtest.Open(t)
	s := openStore(t, db)
	exec(t, db, `DELETE FROM sandbox_settings`)

	_, err := s.CloudPlatformSwitches(t.Context())
	assert.ErrorIs(t, err, ErrNoSandboxSettings)
	assert.ErrorIs(t, s.SetCloudPlatformSwitch(t.Context(), permissions.AWS, true), ErrNoSandboxSettings)
	var n int
	require.NoError(t, db.Read.QueryRow(`SELECT count(*) FROM sandbox_settings`).Scan(&n))
	assert.Zero(t, n, "the write inserted nothing")
}
