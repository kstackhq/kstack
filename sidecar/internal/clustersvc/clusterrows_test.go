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

package clustersvc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/sqlstmt"
)

// openTestDB is an app.db of this test's own, with no janitor.
func openTestDB(t *testing.T) *appdb.DB {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openTestStore prepares the cluster statements over a fresh app.db.
func openTestStore(t *testing.T) (*appdb.DB, *sqlstmt.Set[stmtID]) {
	t.Helper()
	db := openTestDB(t)
	st, err := sqlstmt.Prepare[stmtID](context.Background(), db.Write, db.Read, statements)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return db, st
}

var t0 = time.UnixMilli(1_700_000_000_000).UTC()

// importRow inserts a kubeconfig row for contextName at t0 and returns it.
func importRow(t *testing.T, st *sqlstmt.Set[stmtID], contextName string) ClusterRow {
	t.Helper()
	var inserted bool
	id := appdb.NewID()
	err := st.InTx(context.Background(), func(s stmts) error {
		var err error
		inserted, err = insertClusterIfAbsent(context.Background(), s, id, contextName, t0)
		return err
	})
	require.NoError(t, err)
	require.True(t, inserted)
	row, ok, err := getCluster(context.Background(), st.Stmts(), ClusterID(id))
	require.NoError(t, err)
	require.True(t, ok)
	return row
}

// A fresh import takes SQL's defaults: enabled, syncing, not monitored, unnamed,
// unmarked, both stamps at the import time.
func TestInsertClusterIfAbsentTakesTheDefaults(t *testing.T) {
	_, st := openTestStore(t)
	row := importRow(t, st, "dev")

	require.Equal(t, "kubeconfig", row.Source)
	require.NotNil(t, row.SourceKey)
	require.Equal(t, "dev", *row.SourceKey)
	require.Nil(t, row.Name)
	require.True(t, row.Enabled)
	require.True(t, row.SyncEnabled)
	require.False(t, row.MonitoringEnabled)
	require.Equal(t, t0, row.CreatedAt)
	require.Equal(t, t0, row.UpdatedAt)
	require.Nil(t, row.DeleteRequestedAt)
}

// A second import of the same context is a no-op — the claim is per (source,
// source_key), so the row, its id and its toggles survive a context that leaves and
// returns, and a marked row keeps the claim until it is removed.
func TestInsertClusterIfAbsentKeepsTheClaim(t *testing.T) {
	ctx := context.Background()
	_, st := openTestStore(t)
	row := importRow(t, st, "dev")
	_, err := setClusterToggle(ctx, st.Stmts(), stmtSetClusterEnabled, row.ID, false, t0.Add(time.Second))
	require.NoError(t, err)

	reimport := func() bool {
		var inserted bool
		require.NoError(t, st.InTx(ctx, func(s stmts) error {
			var err error
			inserted, err = insertClusterIfAbsent(ctx, s, appdb.NewID(), "dev", t0.Add(time.Minute))
			return err
		}))
		return inserted
	}
	require.False(t, reimport())
	rows, err := listClusters(ctx, st.Stmts())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, row.ID, rows[0].ID)
	require.False(t, rows[0].Enabled)

	_, err = markCluster(ctx, st.Stmts(), row.ID, t0.Add(2*time.Second))
	require.NoError(t, err)
	require.False(t, reimport())
}

// A toggle write moves the field and updated_at alone, and comes back as the row it
// wrote. A marked row refuses every toggle as not found: the user's choices are
// closed once its deletion is asked for.
func TestClusterTogglesAreGuardedByTheMark(t *testing.T) {
	ctx := context.Background()
	_, st := openTestStore(t)
	row := importRow(t, st, "dev")
	at := t0.Add(time.Second)

	got, err := setClusterToggle(ctx, st.Stmts(), stmtSetClusterSyncEnabled, row.ID, false, at)
	require.NoError(t, err)
	require.False(t, got.SyncEnabled)
	require.True(t, got.Enabled)
	require.Equal(t, at, got.UpdatedAt)
	require.Equal(t, t0, got.CreatedAt)

	got, err = setClusterToggle(ctx, st.Stmts(), stmtSetClusterMonitoringEnabled, row.ID, true, at)
	require.NoError(t, err)
	require.True(t, got.MonitoringEnabled)

	_, err = markCluster(ctx, st.Stmts(), row.ID, at)
	require.NoError(t, err)
	for name, set := range map[string]func() error{
		"enabled": func() error {
			_, err := setClusterToggle(ctx, st.Stmts(), stmtSetClusterEnabled, row.ID, false, at)
			return err
		},
		"sync": func() error {
			_, err := setClusterToggle(ctx, st.Stmts(), stmtSetClusterSyncEnabled, row.ID, true, at)
			return err
		},
		"monitoring": func() error {
			_, err := setClusterToggle(ctx, st.Stmts(), stmtSetClusterMonitoringEnabled, row.ID, false, at)
			return err
		},
	} {
		require.ErrorIs(t, set(), ErrNotFound, name)
	}
	_, err = setClusterToggle(ctx, st.Stmts(), stmtSetClusterEnabled, ClusterID(appdb.NewID()), false, at)
	require.ErrorIs(t, err, ErrNotFound)
}

// The mark is a user edit, so it stamps updated_at; a repeat leaves the first stamp
// and reports that nothing changed.
func TestMarkClusterIsIdempotent(t *testing.T) {
	ctx := context.Background()
	_, st := openTestStore(t)
	row := importRow(t, st, "dev")
	at := t0.Add(time.Second)

	marked, err := markCluster(ctx, st.Stmts(), row.ID, at)
	require.NoError(t, err)
	require.True(t, marked)
	got, ok, err := getCluster(ctx, st.Stmts(), row.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.DeleteRequestedAt)
	require.Equal(t, at, *got.DeleteRequestedAt)
	require.Equal(t, at, got.UpdatedAt)

	marked, err = markCluster(ctx, st.Stmts(), row.ID, at.Add(time.Second))
	require.NoError(t, err)
	require.False(t, marked)
	got, _, err = getCluster(ctx, st.Stmts(), row.ID)
	require.NoError(t, err)
	require.Equal(t, at, *got.DeleteRequestedAt)

	marked, err = markCluster(ctx, st.Stmts(), ClusterID(appdb.NewID()), at)
	require.NoError(t, err)
	require.False(t, marked)
}

// The final delete goes only through a marked row nothing references: unmarked, it
// deletes nothing; a chat filed under it holds it; once the chat is gone it goes.
func TestDeleteClusterIsGuarded(t *testing.T) {
	ctx := context.Background()
	db, st := openTestStore(t)
	row := importRow(t, st, "dev")

	deleted, err := deleteMarkedCluster(ctx, st.Stmts(), row.ID)
	require.NoError(t, err)
	require.False(t, deleted)

	_, err = markCluster(ctx, st.Stmts(), row.ID, t0)
	require.NoError(t, err)
	_, err = db.Write.Exec(`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', ?, 'chat', 0, 0)`, string(row.ID))
	require.NoError(t, err)
	deleted, err = deleteMarkedCluster(ctx, st.Stmts(), row.ID)
	require.NoError(t, err)
	require.False(t, deleted)

	_, err = db.Write.Exec(`DELETE FROM chats`)
	require.NoError(t, err)
	deleted, err = deleteMarkedCluster(ctx, st.Stmts(), row.ID)
	require.NoError(t, err)
	require.True(t, deleted)
	_, ok, err := getCluster(ctx, st.Stmts(), row.ID)
	require.NoError(t, err)
	require.False(t, ok)
}

// The list is in id order — ids are v7, so that is import order.
func TestListClustersIsInIDOrder(t *testing.T) {
	_, st := openTestStore(t)
	first := importRow(t, st, "b")
	second := importRow(t, st, "a")
	rows, err := listClusters(context.Background(), st.Stmts())
	require.NoError(t, err)
	require.Equal(t, []ClusterID{first.ID, second.ID}, []ClusterID{rows[0].ID, rows[1].ID})
}

// Every read and write over the table reports a store that will not answer, never an
// empty or a no-op answer.
func TestClusterRowsReportAStorageFault(t *testing.T) {
	ctx := context.Background()
	db, st := openTestStore(t)
	row := importRow(t, st, "dev")
	require.NoError(t, db.Close())

	for name, op := range map[string]func() error{
		"get":  func() error { _, _, err := getCluster(ctx, st.Stmts(), row.ID); return err },
		"list": func() error { _, err := listClusters(ctx, st.Stmts()); return err },
		"toggle": func() error {
			_, err := setClusterToggle(ctx, st.Stmts(), stmtSetClusterEnabled, row.ID, false, t0)
			return err
		},
		"mark":   func() error { _, err := markCluster(ctx, st.Stmts(), row.ID, t0); return err },
		"delete": func() error { _, err := deleteMarkedCluster(ctx, st.Stmts(), row.ID); return err },
		"insert": func() error {
			return st.InTx(ctx, func(s stmts) error {
				_, err := insertClusterIfAbsent(ctx, s, appdb.NewID(), "x", t0)
				return err
			})
		},
	} {
		assert.Error(t, op(), name)
	}
}
