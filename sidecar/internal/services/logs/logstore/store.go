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

// Package logstore is the log cache's file: one SQLite database per cluster cache, holding
// the lines read from the cluster and, per stream, which ranges of them are whole. The
// schema is migrations/0001_init.sql.
//
// A Store is a claim on an open file, and the Manager is the only way to one. A clear or a
// removal has to close every claim's file before deleting it: on POSIX a delete under an
// open handle does not fail, it forks the file, the old handle writing into the unlinked
// inode while a fresh open starts empty. Sequencing that is only possible where the claims
// are held.
package logstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/amorey/gobus/conflate"

	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitepool"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"
)

// readerPoolSize is how many reads run beside the one writer.
const readerPoolSize = 4

// file is one open log file: the writer, the reader pool beside it, the statements
// prepared on both, and the bus every commit of lines pings by stream id.
type file struct {
	db     *sql.DB
	readDB *sql.DB
	set    *sqlstmt.Set[stmtID]
	bus    *conflate.Hub[int64, struct{}]
	// ctx is what an operation runs its statements under; close cancels it, so the wait
	// for inUse is a wait for interrupted statements to unwind. inUse counts the
	// operations running on the file, which close waits for, since sql.DB.Close does not
	// wait for a connection in use and Windows refuses to delete a file one holds open.
	// An operation registers under the manager's lock, where the file leaves its entry.
	ctx    context.Context
	cancel context.CancelFunc
	inUse  sync.WaitGroup
}

// openFile opens the file at path, creating its directory, the file and the schema on
// first touch. The directory is 0700 and the file 0600, since a log holds whatever the
// cluster wrote.
func openFile(path string) (*file, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	db, err := sqlitepool.OpenWriter(path)
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	if _, err := sqlitemigrate.Apply(context.Background(), db, migrationsFS, "migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// After the migrations, so a reader never races the schema onto a fresh file.
	readDB, err := sqlitepool.OpenReader(path, readerPoolSize)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open reader pool: %w", err)
	}
	set, err := sqlstmt.Prepare[stmtID](context.Background(), db, readDB, statements)
	if err != nil {
		db.Close()
		readDB.Close()
		return nil, fmt.Errorf("prepare: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &file{db: db, readDB: readDB, set: set, bus: conflate.New[int64, struct{}](), ctx: ctx, cancel: cancel}, nil
}

// close ends the bus's receivers, interrupts the file's operations, waits for them to
// end, and closes both pools.
func (f *file) close() error {
	f.bus.Close()
	f.cancel()
	f.inUse.Wait()
	return errors.Join(f.set.Close(), f.db.Close(), f.readDB.Close())
}

// bind is ctx ended also when the file closes, so a close interrupts the statements
// an operation runs under it.
func (f *file) bind(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(f.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// ping tells the bus each stream in ids was written, after the commit that wrote it.
func (f *file) ping(ids []int64) {
	for _, id := range ids {
		_ = f.bus.Sender().Send(id, struct{}{}) // an error is the bus closed, whose receivers have ended
	}
}

// Store is one holder's claim on one cache's file, and owes a Close. The file is resolved
// per operation from the entry the claim took, so a clear or a removal reaches every
// holder: once the entry is retired the claim answers ErrClosed, never a closed database
// and never the fresh file a later open created under the same id.
type Store struct {
	m       *Manager
	cacheID int64
	e       *entry
	// closed is set by the claim's own Close, so a second Close gives nothing back.
	closed bool
}

// use resolves the file behind the claim and counts an operation on it in one critical
// section, so the operation either registers before a close and is waited for, or
// answers ErrClosed. done ends it once its statements are finished; call it before
// anything else on the manager, the claim's Close included, since a close waits for it.
func (s *Store) use() (f *file, done func(), err error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()

	if s.closed || s.e.file == nil {
		return nil, nil, fmt.Errorf("log store %d: %w", s.cacheID, ErrClosed)
	}
	f = s.e.file
	f.inUse.Add(1)
	return f, f.inUse.Done, nil
}

// operate runs fn as one operation on the claim's file, its context ended also when
// the file closes.
func (s *Store) operate(ctx context.Context, fn func(context.Context, *file) error) error {
	f, done, err := s.use()
	if err != nil {
		return err
	}
	defer done()
	bound, stop := f.bind(ctx)
	defer stop()
	err = fn(bound, f)
	if err != nil && ctx.Err() == nil && f.ctx.Err() != nil {
		// The file's close interrupted the operation, which reads as a cancel.
		return fmt.Errorf("log store %d: %w", s.cacheID, ErrClosed)
	}
	return err
}

// with runs fn as one operation on the claim's file, its statements on the pools.
func (s *Store) with(ctx context.Context, fn func(context.Context, stmts) error) error {
	return s.operate(ctx, func(ctx context.Context, f *file) error { return fn(ctx, f.set.Stmts()) })
}

// inTx runs fn as one operation on the claim's file, inside one write transaction.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, stmts) error) error {
	return s.operate(ctx, func(ctx context.Context, f *file) error {
		return f.set.InTx(ctx, func(st stmts) error { return fn(ctx, st) })
	})
}

// inReadTx runs fn as one operation on the claim's file, inside one read transaction,
// so every statement it runs sees one snapshot.
func (s *Store) inReadTx(ctx context.Context, fn func(context.Context, stmts) error) error {
	return s.operate(ctx, func(ctx context.Context, f *file) error {
		return f.set.InReadTx(ctx, func(st stmts) error { return fn(ctx, st) })
	})
}

// Close gives the claim back. The last close on an entry closes its file and drops the
// entry; on a retired entry there is nothing left to close.
func (s *Store) Close() error {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	s.e.refs--
	if s.e.refs > 0 || s.e.file == nil {
		return nil
	}
	f := m.detachLocked(s.cacheID)
	m.acquireLocked(s.cacheID)
	m.mu.Unlock()
	err := f.close()
	m.mu.Lock()
	m.releaseLocked(s.cacheID)
	return err
}

// Subscribe is a receiver of a ping per stream in ids each time a commit writes it
// lines, coalesced per stream. Subscribe before the first read, or a commit between
// the two is missed; close the receiver when done. The file closing ends it.
func (s *Store) Subscribe(ids ...int64) (*conflate.Receiver[int64, struct{}], error) {
	f, done, err := s.use()
	if err != nil {
		return nil, fmt.Errorf("subscribe: %w", err)
	}
	defer done()
	return f.bus.Receiver(f.bus.WithKeyFilter(func(id int64) bool { return slices.Contains(ids, id) })), nil
}
