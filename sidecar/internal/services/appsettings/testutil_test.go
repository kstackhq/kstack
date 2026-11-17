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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
)

// The clusters a test names as the default. Raw SQL, since this package owns no
// cluster statement.
const (
	prod apimeta.ClusterID = "prod"
	dev  apimeta.ClusterID = "dev"
)

type harness struct {
	svc *service
	db  *appdb.DB
}

// newHarness is a started service over an app.db of the test's own, with two
// clusters seeded.
func newHarness(t *testing.T) *harness {
	t.Helper()
	db := appdbtest.Open(t)
	for _, id := range []apimeta.ClusterID{prod, dev} {
		exec(t, db, `INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES (?, 'kubeconfig', ?, 0, 0)`, string(id), "ctx-"+string(id))
	}
	svc, err := newService(db)
	require.NoError(t, err)
	stop, err := svc.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, stop(context.Background()))
		require.NoError(t, svc.Close())
	})
	return &harness{svc: svc, db: db}
}

func exec(t *testing.T, db *appdb.DB, q string, args ...any) {
	t.Helper()
	_, err := db.Write.Exec(q, args...)
	require.NoError(t, err)
}

// setDefault makes id the default; it must succeed.
func (h *harness) setDefault(t *testing.T, id apimeta.ClusterID) {
	t.Helper()
	_, err := h.svc.SetDefaultCluster(t.Context(), id)
	require.NoError(t, err)
}

// defaultID is the stored default, "" for none.
func (h *harness) defaultID(t *testing.T) apimeta.ClusterID {
	t.Helper()
	got, err := h.svc.Get(t.Context())
	require.NoError(t, err)
	return idOf(got)
}

func idOf(s Settings) apimeta.ClusterID {
	if s.DefaultClusterID == nil {
		return ""
	}
	return *s.DefaultClusterID
}
