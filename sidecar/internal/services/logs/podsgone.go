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

package logs

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// podsGone answers which of uids the mirror of the cache cacheID no longer holds,
// for the log file's sweep. It answers none unless that cache is its cluster's
// active one, trusted and mirroring pods: a stream the sweep removes is gone for
// good, so a pod is gone only when the mirror is sure.
func (s *Service) podsGone(ctx context.Context, cacheID int64, uids []string) ([]string, error) {
	cache, err := s.clusters.Caches().Get(ctx, cluster.ClusterCacheID(cacheID))
	if errors.Is(err, cluster.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	arg, _ := json.Marshal(uids) // a list of strings always marshals
	var gone []string
	err = s.clusters.CachedData().ReadActive(ctx, cache.Spec.ClusterID, len(uids)+1, maxBytes, func(q cluster.CacheReading, active cluster.ActiveCluster) error {
		// Set whole, never appended to: ReadActive may run this twice.
		gone = nil
		r := reading{q}
		if active.Cache.ID != cache.ID {
			return nil
		}
		mirrored, err := r.mirrors(mirrorKinds[SourcePod])
		if err != nil || !mirrored {
			return err
		}
		rows, err := r.rows(`SELECT uid FROM objects WHERE api_version = 'v1' AND kind = 'Pod' AND uid IN (SELECT value FROM json_each(?))`, string(arg))
		if err != nil {
			return err
		}
		held := map[string]bool{}
		for _, row := range rows {
			held[cluster.CellText(row[0])] = true
		}
		for _, uid := range uids {
			if !held[uid] {
				gone = append(gone, uid)
			}
		}
		return nil
	})
	if errors.Is(err, cluster.ErrNoCache) || errors.Is(err, cluster.ErrSyncing) {
		return nil, nil
	}
	return gone, err
}
