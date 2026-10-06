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

package kubequery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

var errRead = errors.New("boom")

// fakeService is the cluster service as the binding reads it: one reading handed
// to read, the active cache's health, and a statement's answer. Every other read
// fails, so a query that reached for the card's facts would fail with it.
type fakeService struct {
	cluster.Service
	// active is what ReadActive hands read, attempts times (a retry is two); readErr,
	// when set, is ReadActive's answer instead.
	active   cluster.ActiveCluster
	attempts int
	readErr  error
	// health answers the health read, and healthGone finds no cache.
	health     cluster.ClusterCacheHealth
	healthGone bool
	// query answers a statement, queryErr fails it, and queryGone finds no file.
	query     cluster.ClusterCachedDataQueryResult
	queryErr  error
	queryGone bool
	// queried is every statement run, with the cache and caps it was given.
	queried []queried
}

// queried is one statement a fake ran.
type queried struct {
	cacheID           cluster.ClusterCacheID
	sql               string
	maxRows, maxBytes int
}

type (
	fakeClusters struct {
		cluster.Clusters
		*fakeService
	}
	fakeCaches struct {
		cluster.Caches
		*fakeService
	}
	fakeCachedData struct {
		cluster.CachedData
		*fakeService
	}
)

func (f *fakeService) Clusters() cluster.Clusters     { return fakeClusters{fakeService: f} }
func (f *fakeService) Caches() cluster.Caches         { return fakeCaches{fakeService: f} }
func (f *fakeService) CachedData() cluster.CachedData { return fakeCachedData{fakeService: f} }

func (f fakeClusters) ReadActive(ctx context.Context, _ cluster.ClusterID, read func(context.Context, cluster.ActiveCluster) error) error {
	if f.readErr != nil {
		return f.readErr
	}
	for range max(f.attempts, 1) {
		if err := read(ctx, f.active); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	return f.health, !f.healthGone, nil
}

func (f fakeCaches) SyncStatus(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheSyncStatus, bool, error) {
	return cluster.ClusterCacheSyncStatus{}, false, errRead
}

func (f fakeCachedData) ListKinds(context.Context, cluster.ClusterID, cluster.ClusterCacheID) ([]cluster.ClusterCachedDataKind, error) {
	return nil, errRead
}

func (f fakeCachedData) ListObjects(context.Context, cluster.ClusterID, cluster.ClusterCacheID, string, string) ([]cluster.ClusterCachedDataObject, bool, error) {
	return nil, false, errRead
}

func (f fakeCachedData) Query(_ context.Context, _ cluster.ClusterID, cacheID cluster.ClusterCacheID, sql string, maxRows, maxBytes int) (cluster.ClusterCachedDataQueryResult, bool, error) {
	f.queried = append(f.queried, queried{cacheID, sql, maxRows, maxBytes})
	if f.queryErr != nil {
		return cluster.ClusterCachedDataQueryResult{}, false, f.queryErr
	}
	return f.query, !f.queryGone, nil
}

func strp(s string) *string { return &s }

// healthy is a cluster named prod whose active cache, 11, is watching.
func healthy() *fakeService {
	return &fakeService{
		active: cluster.ActiveCluster{
			Cluster: &cluster.Cluster{ID: "7", Spec: cluster.ClusterSpec{
				Name:   strp("prod"),
				Source: cluster.ClusterSpecSource{Kubeconfig: &cluster.ClusterSpecSourceKubeconfig{Context: "prod-admin"}},
			}},
			Cache: &cluster.ClusterCache{RecordMeta: cluster.RecordMeta{ID: 11}},
		},
		health: cluster.ClusterCacheHealth{Reason: "Watching", TotalKinds: 2},
	}
}

// dataQuery runs sql over cluster 7's cache, as the tool does.
func dataQuery(f *fakeService, sql string) (answer, error) {
	return New(f).query(context.Background(), "7", sql, 5)
}

// The rows and the verdict come from one reading: the query runs inside it, on the
// active cache, and a retry of the reading runs it again.
func TestAQueryReadsInsideOneReading(t *testing.T) {
	f := healthy()
	f.query = cluster.ClusterCachedDataQueryResult{Columns: []string{"name"}, Rows: [][]any{{"api-0"}}, More: true}

	got, err := dataQuery(f, `SELECT name FROM objects`)

	require.NoError(t, err)
	assert.Equal(t, answer{
		cluster: "prod", freshness: []byte(`{"status":"watching"}`),
		rows: cluster.ClusterCachedDataQueryResult{Columns: []string{"name"}, Rows: [][]any{{"api-0"}}, More: true},
	}, got)
	assert.Equal(t, []queried{{11, `SELECT name FROM objects`, 5, tools.FileLimit}}, f.queried)

	retried := healthy()
	retried.attempts = 2
	_, err = dataQuery(retried, `SELECT 1`)
	require.NoError(t, err)
	assert.Len(t, retried.queried, 2, "the retry ran the query again")
}

// A query reads the cache's health and nothing else of what the card reads.
func TestAQueryReadsTheHealthNotTheCardsFacts(t *testing.T) {
	f := healthy()
	f.query = cluster.ClusterCachedDataQueryResult{Columns: []string{"n"}, Rows: [][]any{{1}}}

	got, err := dataQuery(f, `SELECT 1 AS n`)

	require.NoError(t, err)
	assert.Equal(t, [][]any{{1}}, got.rows.Rows)
}

func TestSyncingAndUnknownWithholdTheRows(t *testing.T) {
	for reason, status := range map[string]string{"Connecting": `{"status":"syncing"}`, "SizeLimit": `{"status":"unknown","reason":"SizeLimit"}`} {
		f := healthy()
		f.health.Reason = reason

		got, err := dataQuery(f, `SELECT 1`)

		require.NoError(t, err)
		assert.Equal(t, answer{cluster: "prod", freshness: []byte(status), withheld: true}, got, reason)
		assert.Empty(t, f.queried, reason)
	}
}

func TestNoCacheWithholdsAndRunsNothing(t *testing.T) {
	f := healthy()
	f.active.Cache = nil

	got, err := dataQuery(f, `SELECT 1`)

	require.NoError(t, err)
	assert.Equal(t, answer{cluster: "prod", freshness: []byte(`{"status":"syncing"}`), withheld: true}, got)
	assert.Empty(t, f.queried)
}

// Last-known is not withheld: the rows are the cache's, and the verdict says since when.
func TestLastKnownCarriesSince(t *testing.T) {
	f := healthy()
	live := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	f.health = cluster.ClusterCacheHealth{Reason: "Disconnected", TotalKinds: 2, LastLiveAt: &live,
		UnhealthyKindRefs: []cluster.SyncedKindRef{{APIVersion: "v1", Resource: "namespaces"}, {APIVersion: "v1", Resource: "nodes"}}}

	got, err := dataQuery(f, `SELECT 1`)

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"last-known","reason":"Disconnected","since":"2026-09-27T10:00:00Z"}`, string(got.freshness))
	assert.False(t, got.withheld)
	assert.Len(t, f.queried, 1)
}

func TestAGoneOrMovedClusterHasNoCache(t *testing.T) {
	for name, f := range map[string]*fakeService{
		"record gone":     {readErr: cluster.ErrNotFound},
		"identity moved":  {readErr: cluster.ErrIdentityMoved},
		"health gone":     func() *fakeService { f := healthy(); f.healthGone = true; return f }(),
		"cache file gone": func() *fakeService { f := healthy(); f.queryGone = true; return f }(),
	} {
		_, err := dataQuery(f, `SELECT 1`)
		assert.ErrorIs(t, err, errNoCache, name)
	}
}

func TestAStatementErrorIsSQLitesRefusal(t *testing.T) {
	f := healthy()
	f.queryErr = &cluster.QueryError{Message: "no such column: nope"}

	_, err := dataQuery(f, `SELECT nope FROM objects`)

	var qe *cluster.QueryError
	require.ErrorAs(t, err, &qe)
	assert.Equal(t, "no such column: nope", qe.Message)
}

func TestAnyOtherFailureIsReturned(t *testing.T) {
	f := healthy()
	f.queryErr = context.DeadlineExceeded
	_, err := dataQuery(f, `SELECT 1`)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	f = healthy()
	f.queryErr = errRead
	_, err = dataQuery(f, `SELECT 1`)
	require.ErrorIs(t, err, errRead)
	assert.False(t, errors.Is(err, errNoCache))
}

// The cluster goes by the name the user set, or by its context when there is none.
func TestTheClusterIsItsName(t *testing.T) {
	f := healthy()
	f.active.Cluster.Spec.Name = nil

	got, err := dataQuery(f, `SELECT 1`)

	require.NoError(t, err)
	assert.Equal(t, "prod-admin", got.cluster)
}
