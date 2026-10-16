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
// schema is migrations/0001_init.sql. Nothing reads or writes a line through it yet.
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
	"sync"

	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitepool"
)

// readerPoolSize is how many reads run beside the one writer.
const readerPoolSize = 4

// file is one open log file: the writer and the reader pool beside it.
type file struct {
	db     *sql.DB
	readDB *sql.DB
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
	ctx, cancel := context.WithCancel(context.Background())
	return &file{db: db, readDB: readDB, ctx: ctx, cancel: cancel}, nil
}

// close interrupts the file's operations, waits for them to end, and closes both pools.
func (f *file) close() error {
	f.cancel()
	f.inUse.Wait()
	return errors.Join(f.db.Close(), f.readDB.Close())
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
