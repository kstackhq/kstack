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

package mirrorsql

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// fakeService is the cluster service as Read reads it: one reading, its cache's
// health, and a query answering rows.
type fakeService struct {
	cluster.Service
	readErr   error
	active    cluster.ActiveCluster
	health    cluster.ClusterCacheHealth
	noHealth  bool
	healthErr error
	noQuery   bool
	// asked is the statement and bounds the query was handed.
	asked struct {
		sql              string
		maxRows, maxByte int
	}
}

func (f *fakeService) Clusters() cluster.Clusters     { return fakeClusters{f: f} }
func (f *fakeService) Caches() cluster.Caches         { return fakeCaches{f: f} }
func (f *fakeService) CachedData() cluster.CachedData { return fakeData{f: f} }

type fakeClusters struct {
	cluster.Clusters
	f *fakeService
}

func (fc fakeClusters) ReadActive(ctx context.Context, _ cluster.ClusterID, read func(context.Context, cluster.ActiveCluster) error) error {
	if fc.f.readErr != nil {
		return fc.f.readErr
	}
	return read(ctx, fc.f.active)
}

type fakeCaches struct {
	cluster.Caches
	f *fakeService
}

func (fc fakeCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	return fc.f.health, !fc.f.noHealth, fc.f.healthErr
}

type fakeData struct {
	cluster.CachedData
	f *fakeService
}

func (fd fakeData) Query(_ context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, sql string, maxRows, maxBytes int) (cluster.ClusterCachedDataQueryResult, bool, error) {
	fd.f.asked.sql, fd.f.asked.maxRows, fd.f.asked.maxByte = sql, maxRows, maxBytes
	return cluster.ClusterCachedDataQueryResult{Rows: [][]any{{"a", int64(2)}}}, !fd.f.noQuery, nil
}

// watching is a service whose active cache is live.
func watching() *fakeService {
	return &fakeService{
		active: cluster.ActiveCluster{Cache: &cluster.ClusterCache{RecordMeta: cluster.RecordMeta{ID: 41}}},
		health: cluster.ClusterCacheHealth{Reason: "Watching"},
	}
}

// Read hands a live cache's reading to read, whose statements run within its
// bounds.
func TestReadRunsOverALiveCache(t *testing.T) {
	svc := watching()
	var rows [][]any
	err := Read(t.Context(), svc, "7", 10, 20, func(q Query, active cluster.ActiveCluster) error {
		assert.Equal(t, cluster.ClusterCacheID(41), active.Cache.ID)
		res, err := q.Run("SELECT 1")
		rows = res.Rows
		return err
	})

	require.NoError(t, err)
	assert.Equal(t, [][]any{{"a", int64(2)}}, rows)
	assert.Equal(t, "SELECT 1", svc.asked.sql)
	assert.Equal(t, 10, svc.asked.maxRows)
	assert.Equal(t, 20, svc.asked.maxByte)
}

// Read is ErrNoCache for a cluster with no cache, a record gone or an identity
// that moved, and a cache gone under the read; ErrSyncing for a cache whose
// verdict withholds its rows; and any other failure as it is.
func TestReadRefusesWhatHasNoRows(t *testing.T) {
	failed := errors.New("db closed")
	for name, tc := range map[string]struct {
		change func(*fakeService)
		want   error
	}{
		"no cache":       {func(f *fakeService) { f.active.Cache = nil }, ErrNoCache},
		"record gone":    {func(f *fakeService) { f.readErr = cluster.ErrNotFound }, ErrNoCache},
		"identity moved": {func(f *fakeService) { f.readErr = cluster.ErrIdentityMoved }, ErrNoCache},
		"no health":      {func(f *fakeService) { f.noHealth = true }, ErrNoCache},
		"cache gone":     {func(f *fakeService) { f.noQuery = true }, ErrNoCache},
		"syncing":        {func(f *fakeService) { f.health.Reason = "Connecting" }, ErrSyncing},
		"health failed":  {func(f *fakeService) { f.healthErr = failed }, failed},
		"reading failed": {func(f *fakeService) { f.readErr = failed }, failed},
	} {
		t.Run(name, func(t *testing.T) {
			svc := watching()
			tc.change(svc)

			err := Read(t.Context(), svc, "7", 10, 20, func(q Query, _ cluster.ActiveCluster) error {
				_, err := q.Run("SELECT 1")
				return err
			})

			assert.ErrorIs(t, err, tc.want)
		})
	}
}

// A literal quotes its text and doubles every quote inside it.
func TestLiteral(t *testing.T) {
	assert.Equal(t, `'it''s'`, Literal("it's"))
	assert.Equal(t, `'a', 'b''c'`, Literals([]string{"a", "b'c"}))
	assert.Empty(t, Literals(nil))
}

// A cell is read as its type, and as the zero value when it is another.
func TestCells(t *testing.T) {
	assert.Equal(t, "x", Text("x"))
	assert.Empty(t, Text(int64(1)))
	assert.Equal(t, int64(3), Integer(int64(3)))
	assert.Zero(t, Integer("3"))
}
