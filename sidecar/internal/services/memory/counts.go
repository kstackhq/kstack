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

package memory

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
)

// Count is the number of notes belonging to exactly one scope.
type Count struct {
	ClusterID *apimeta.ClusterID
	Count     int
}

func (s *service) Counts(ctx context.Context) ([]Count, error) {
	rows, err := s.store.Stmts().Query(ctx, stmtSelectCounts)
	if err != nil {
		return nil, fmt.Errorf("select memory counts: %w", err)
	}
	defer rows.Close()
	counts := []Count{}
	for rows.Next() {
		var id sql.NullString
		var count int
		if err := rows.Scan(&id, &count); err != nil { // coverage:ignore SQL COUNT and nullable TEXT always scan into these types
			return nil, fmt.Errorf("scan memory counts: %w", err)
		}
		item := Count{Count: count}
		if id.Valid {
			cluster := apimeta.ClusterID(id.String)
			item.ClusterID = &cluster
		}
		counts = append(counts, item)
	}
	return counts, rows.Err()
}

// WatchCounts sends one full snapshot at subscribe and another after a count changes.
func (s *service) WatchCounts(ctx context.Context) (*Stream[[]Count], error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	ch := make(chan []Count, 1)
	st := &Stream[[]Count]{Frames: ch}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	go func() {
		defer s.wg.Done()
		defer close(ch)
		defer stop()
		defer cancel()
		rx := s.db.Subscribe(appdb.KeyMemories, appdb.KeyClusters)
		defer rx.Close()
		var prev []Count
		first := true
		for {
			counts, err := s.Counts(ctx)
			if err != nil {
				if ctx.Err() == nil {
					st.err.Store(&err)
				}
				return
			}
			if first || !slices.EqualFunc(prev, counts, func(a, b Count) bool {
				return same(a.ClusterID, b.ClusterID) && a.Count == b.Count
			}) {
				select {
				case ch <- counts:
				case <-ctx.Done(): // coverage:ignore only a subscriber leaving while its counts frame is in flight
					return
				}
			}
			first, prev = false, counts
			if _, err := rx.RecvContext(ctx); err != nil {
				return
			}
		}
	}()
	return st, nil
}
