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

package logstore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// sweepReport is what one sweep did: the chunks' bytes before and after it, how many
// chunks it evicted, whether it stopped over budget with nothing left to evict, and
// whether it handed free pages back.
type sweepReport struct {
	Before, After int64
	Evicted       int
	Short         bool
	Vacuumed      bool
}

// candidate is a chunk a sweep may evict, and what evicting it frees.
type candidate struct {
	id, stream, bytes int64
}

// Sweep runs one sweep of the claim's file.
func (s *Store) Sweep(ctx context.Context) error {
	err := s.operate(ctx, func(ctx context.Context, f *file) error {
		_, err := s.m.sweep(ctx, s.cacheID, f)
		return err
	})
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	return nil
}

// sweep evicts f's unprotected chunks until every stream is under its share and the
// file under its budget, and vacuums. Each
// eviction is its own transaction, so the tailers' batches land between them.
func (m *Manager) sweep(ctx context.Context, cacheID int64, f *file) (sweepReport, error) {
	var r sweepReport
	sizes, err := streamBytes(ctx, f)
	if err != nil {
		return r, err
	}
	for _, n := range sizes {
		r.Before += n
	}
	r.After = r.Before
	cutoff := m.now().Add(-m.ret.ReadTTL).UnixMilli()
	evict := func(c candidate) error {
		var evicted bool
		err := f.heldChunks.unlessHeld(c.id, func() (err error) {
			evicted, err = f.evict(ctx, c.id, cutoff)
			return err
		})
		if evicted {
			r.After -= c.bytes
			sizes[c.stream] -= c.bytes
			r.Evicted++
		}
		return err
	}
	var cands []candidate
	if m.over(sizes, r.After) {
		if cands, err = candidates(ctx, f, cutoff); err != nil {
			return r, err
		}
	}
	// An over-share stream's chunks go first, then any stream's while the file is over
	// its budget; the candidates are in eviction order within each.
	var rest []candidate
	for _, c := range cands {
		if sizes[c.stream] <= m.ret.StreamShare {
			rest = append(rest, c)
			continue
		}
		if err := evict(c); err != nil {
			return r, err
		}
	}
	for _, c := range rest {
		if r.After <= m.ret.Bytes {
			break
		}
		if err := evict(c); err != nil {
			return r, err
		}
	}
	r.Short = m.over(sizes, r.After)
	if f.short.Swap(r.Short) != r.Short {
		if r.Short {
			slog.Warn("log store: the file is over its budget with nothing to evict", "cacheID", cacheID, "bytes", r.After)
		} else {
			slog.Info("log store: the file is back within its budget", "cacheID", cacheID, "bytes", r.After)
		}
	}
	if r.Vacuumed, err = m.vacuum(ctx, f); err != nil {
		return r, err
	}
	slog.Debug("log store: swept", "cacheID", cacheID, "before", r.Before, "after", r.After,
		"evicted", r.Evicted, "short", r.Short, "vacuumed", r.Vacuumed)
	if m.sweeps != nil {
		m.sweeps(cacheID, r)
	}
	return r, nil
}

// over is whether a file of size bytes, its streams' as sizes, is over its budget or
// has a stream over its share.
func (m *Manager) over(sizes map[int64]int64, size int64) bool {
	if size > m.ret.Bytes {
		return true
	}
	for _, n := range sizes {
		if n > m.ret.StreamShare {
			return true
		}
	}
	return false
}

// vacuum hands at most VacuumPages free pages back to the filesystem, when the file
// has any, then truncates the write-ahead log when it outweighs the file, as appdb's
// janitor does. It answers whether it vacuumed.
func (m *Manager) vacuum(ctx context.Context, f *file) (bool, error) {
	var free int64
	if err := f.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return false, err
	}
	if free > 0 {
		if _, err := f.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, min(free, m.ret.VacuumPages))); err != nil {
			return false, err
		}
	}
	if fileSize(f.path+"-wal") > fileSize(f.path) {
		var busy, logPages, moved int64
		if err := f.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logPages, &moved); err != nil {
			return free > 0, err
		}
	}
	return free > 0, nil
}

// fileSize is the size of the file at path, 0 for one that cannot be read.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// streamBytes is each stream's chunks' bytes summed.
func streamBytes(ctx context.Context, f *file) (map[int64]int64, error) {
	rows, err := f.set.Stmts().Query(ctx, stmtStreamBytes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// candidates is every chunk neither open nor read since cutoff, in the order a sweep
// evicts them: never-read before read, oldest first within each.
func candidates(ctx context.Context, f *file, cutoff int64) ([]candidate, error) {
	rows, err := f.set.Stmts().Query(ctx, stmtCandidates, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.stream, &c.bytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
