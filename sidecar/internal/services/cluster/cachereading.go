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

package cluster

import (
	"context"
	"errors"
	"strings"
)

var (
	// ErrNoCache is a cluster with no cache to read: its record gone, its identity
	// moving under both readings, or its cache gone under the reads.
	ErrNoCache = errors.New("cluster: the cluster has no cache")
	// ErrSyncing is a cache whose rows are not trusted: an object missing from it
	// may still exist.
	ErrSyncing = errors.New("cluster: the cache is syncing")
)

// CacheReading runs statements over one cache inside one reading of its cluster,
// each answer cut at the reading's row and byte bounds.
type CacheReading struct {
	data      CachedData
	ctx       context.Context
	clusterID ClusterID
	cacheID   ClusterCacheID
	maxRows   int
	maxBytes  int
}

// ReadActiveCache is CachedData().ReadActive over svc. Exported because a fake
// CachedData outside this package answers ReadActive through it, over its own
// readings, health and query.
func ReadActiveCache(ctx context.Context, svc Service, id ClusterID, maxRows, maxBytes int, read func(CacheReading, ActiveCluster) error) error {
	err := svc.Clusters().ReadActive(ctx, id, func(ctx context.Context, active ActiveCluster) error {
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
		if !health.Trusted() {
			return ErrSyncing
		}
		return read(CacheReading{svc.CachedData(), ctx, id, active.Cache.ID, maxRows, maxBytes}, active)
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrIdentityMoved) {
		return ErrNoCache
	}
	return err
}

// Query is sql's answer, with args bound to its placeholders; a cache gone under
// the read is ErrNoCache.
func (r CacheReading) Query(sql string, args ...any) (ClusterCachedDataQueryResult, error) {
	res, ok, err := r.data.Query(r.ctx, r.clusterID, r.cacheID, sql, r.maxRows, r.maxBytes, args...)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, ErrNoCache
	}
	return res, nil
}

// Placeholders is n comma-separated placeholders, for an IN list of n values.
func Placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// CellText is a cell as text, "" for any other.
func CellText(v any) string {
	s, _ := v.(string)
	return s
}

// CellInteger is a cell as an integer, 0 for any other.
func CellInteger(v any) int64 {
	n, _ := v.(int64)
	return n
}
