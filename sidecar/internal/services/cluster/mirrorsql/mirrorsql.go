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

// Package mirrorsql reads a cluster's mirror by SQL for a reader that resolves
// what it names against it: one reading of the active cache whose verdict does not
// withhold its rows, the statements run inside it, and the literals and cells they
// are written and read with.
package mirrorsql

import (
	"context"
	"errors"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustercard"
)

var (
	// ErrNoCache is a cluster with no cache to read: its record gone, its identity
	// moving under both readings, or its cache gone under the reads.
	ErrNoCache = errors.New("mirrorsql: the cluster has no cache")
	// ErrSyncing is a cache whose verdict withholds its rows: an object missing
	// from it may still exist.
	ErrSyncing = errors.New("mirrorsql: the cache is syncing")
)

// Query runs statements over one cache inside one reading of its cluster, each
// answer cut at MaxRows rows and MaxBytes bytes.
type Query struct {
	data      cluster.CachedData
	ctx       context.Context
	clusterID cluster.ClusterID
	cacheID   cluster.ClusterCacheID
	maxRows   int
	maxBytes  int
}

// Read runs read over one reading of cluster id's active cache, its statements
// bounded by maxRows and maxBytes. read may run twice, as ReadActive's does, so it
// sets what it returns and never appends to it.
func Read(ctx context.Context, svc cluster.Service, id cluster.ClusterID, maxRows, maxBytes int, read func(Query, cluster.ActiveCluster) error) error {
	err := svc.Clusters().ReadActive(ctx, id, func(ctx context.Context, active cluster.ActiveCluster) error {
		if active.Cache == nil {
			return ErrNoCache
		}
		health, ok, err := svc.Caches().Health(ctx, id, active.Cache.ID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoCache
		}
		if clustercard.Freshness(&health).Withholds() {
			return ErrSyncing
		}
		return read(Query{svc.CachedData(), ctx, id, active.Cache.ID, maxRows, maxBytes}, active)
	})
	if errors.Is(err, cluster.ErrNotFound) || errors.Is(err, cluster.ErrIdentityMoved) {
		return ErrNoCache
	}
	return err
}

// Run is sql's answer; a cache gone under the read is ErrNoCache.
func (q Query) Run(sql string) (cluster.ClusterCachedDataQueryResult, error) {
	res, ok, err := q.data.Query(q.ctx, q.clusterID, q.cacheID, sql, q.maxRows, q.maxBytes)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, ErrNoCache
	}
	return res, nil
}

// Literal is s as a SQL string literal.
func Literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Literals is each of list as a SQL string literal, comma-separated.
func Literals(list []string) string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = Literal(s)
	}
	return strings.Join(out, ", ")
}

// Text is a cell as text, "" for any other.
func Text(v any) string {
	s, _ := v.(string)
	return s
}

// Integer is a cell as an integer, 0 for any other.
func Integer(v any) int64 {
	n, _ := v.(int64)
	return n
}
