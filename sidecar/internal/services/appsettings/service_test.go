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

package appsettings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
)

// The default is one cluster's id, or none; setting another replaces it, and ""
// clears it. Each write answers the settings it left.
func TestTheDefaultClusterIsSetReplacedAndCleared(t *testing.T) {
	h := newHarness(t)
	assert.Empty(t, h.defaultID(t))

	h.setDefault(t, prod)
	got, err := h.svc.SetDefaultCluster(t.Context(), dev)
	require.NoError(t, err)
	assert.Equal(t, dev, idOf(got))
	assert.Equal(t, dev, h.defaultID(t))

	got, err = h.svc.SetDefaultCluster(t.Context(), "")
	require.NoError(t, err)
	assert.Nil(t, got.DefaultClusterID)
	assert.Empty(t, h.defaultID(t))
}

// A default names a row that is there and unmarked; a refused one leaves the
// stored default alone.
func TestSetDefaultClusterRefusesAnUnknownOrMarkedCluster(t *testing.T) {
	h := newHarness(t)
	h.setDefault(t, prod)
	exec(t, h.db, `UPDATE clusters SET delete_requested_at = 1 WHERE id = ?`, string(dev))

	_, err := h.svc.SetDefaultCluster(t.Context(), "404")
	assert.ErrorIs(t, err, ErrClusterNotFound)
	_, err = h.svc.SetDefaultCluster(t.Context(), dev)
	assert.ErrorIs(t, err, ErrClusterNotFound)
	assert.Equal(t, prod, h.defaultID(t))
}

func TestDeletingTheClusterClearsTheDefault(t *testing.T) {
	h := newHarness(t)
	h.setDefault(t, prod)

	exec(t, h.db, `DELETE FROM clusters WHERE id = ?`, string(prod))

	assert.Empty(t, h.defaultID(t))
}

// A store that will not answer is reported, never read as no default.
func TestAStoreThatFailsIsReported(t *testing.T) {
	h := newHarness(t)
	exec(t, h.db, `DROP TABLE app_settings`)

	_, err := h.svc.Get(t.Context())
	assert.Error(t, err)
	_, err = h.svc.SetDefaultCluster(t.Context(), prod)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrClusterNotFound)
	_, err = h.svc.SetDefaultCluster(t.Context(), "")
	assert.Error(t, err)
}

func TestNewFailsOnADatabaseWithoutTheTable(t *testing.T) {
	db := appdbtest.Open(t)
	exec(t, db, `DROP TABLE app_settings`)

	_, err := New(db)

	assert.ErrorContains(t, err, "prepare app settings statements")
}
