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
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager is a manager over a temp directory, closed when the test ends.
func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m := NewManager(dir, testRetention)
	t.Cleanup(func() { assert.NoError(t, m.Close()) })
	return m, dir
}

// open claims cacheID's file.
func open(t *testing.T, m *Manager, cacheID int64) *Store {
	t.Helper()
	s, err := m.OpenOrCreate(cacheID)
	require.NoError(t, err)
	return s
}

// using runs fn as one operation on the file behind a claim that must still have one.
func using(t *testing.T, s *Store, fn func(f *file)) {
	t.Helper()
	f, done, err := s.use()
	require.NoError(t, err)
	defer done()
	fn(f)
}

// streamsIn counts the streams in the file behind a claim.
func streamsIn(t *testing.T, s *Store) (n int) {
	t.Helper()
	using(t, s, func(f *file) { n = countStreams(t, f) })
	return n
}

// addStream inserts a stream into the file behind a claim.
func addStream(t *testing.T, s *Store) {
	t.Helper()
	using(t, s, func(f *file) { insertStream(t, f) })
}

// assertClosed checks that a claim answers ErrClosed.
func assertClosed(t *testing.T, s *Store, msgAndArgs ...any) {
	t.Helper()
	_, done, err := s.use()
	if err == nil {
		done()
	}
	assert.ErrorIs(t, err, ErrClosed, msgAndArgs...)
}

func TestTwoOpensShareOneFile(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	a := open(t, m, 1)
	b := open(t, m, 1)
	addStream(t, a)

	assert.Equal(t, 1, streamsIn(t, b))
	assert.FileExists(t, filepath.Join(dir, "1.db"))
	require.NoError(t, a.Close())
	assert.Equal(t, 1, streamsIn(t, b), "the file stays open while anyone holds it")
	require.NoError(t, b.Close())
	assertClosed(t, b, "the last close closes it")
}

// A holder that closes its claim twice, a defer beside an error path's close, gives back
// one reference, never another holder's.
func TestASecondCloseGivesNothingBack(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t)
	a := open(t, m, 1)
	b := open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, b.Close()) })

	require.NoError(t, a.Close())
	require.NoError(t, a.Close())

	assert.Zero(t, streamsIn(t, b), "b's file is still open")
	assertClosed(t, a)
}

// A clear closes the file only once the operations running on it end, so none of them
// meets a closed database. It waits outside the manager's lock: another cache opens and
// closes meanwhile.
func TestClearWaitsForAnOperationOnTheFile(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	held := open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, held.Close()) })
	f, done, err := held.use()
	require.NoError(t, err)
	done = sync.OnceFunc(done)
	t.Cleanup(done)
	cleared := make(chan error, 1)

	go func() { cleared <- m.Clear(1) }()

	// The clear has taken the file once the claim answers ErrClosed.
	require.Eventually(t, func() bool {
		_, probeDone, err := held.use()
		if err == nil {
			probeDone()
		}
		return errors.Is(err, ErrClosed)
	}, 5*time.Second, time.Millisecond)
	other := open(t, m, 2)
	require.NoError(t, other.Close())
	assert.Equal(t, 0, countStreams(t, f), "the file is still open under the operation")
	select {
	case err := <-cleared:
		t.Fatalf("the clear returned under a running operation: %v", err)
	default:
	}

	done()

	require.NoError(t, <-cleared)
	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
}

// A clear is the user throwing the cache's lines away. A claim from before it answers
// ErrClosed, so a follow on the old file ends and a page in flight fails, rather than
// reading an empty file as though every line were gone; the next open starts fresh.
func TestClearRetiresTheOpenFile(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	held := open(t, m, 1)
	addStream(t, held)

	require.NoError(t, m.Clear(1))

	assertClosed(t, held)
	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
	fresh := open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, fresh.Close()) })
	assert.Zero(t, streamsIn(t, fresh))
	assert.NoError(t, held.Close())
}

func TestClearDeletesAFileNobodyHolds(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	require.NoError(t, open(t, m, 1).Close())
	require.FileExists(t, filepath.Join(dir, "1.db"))

	require.NoError(t, m.Clear(1))

	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
	assert.NoError(t, m.Clear(2), "a cache with no file is cleared already")
}

// A claim from before a clear counts down the entry it took, never the one the id names
// now, so letting it go cannot close a fresh file someone else holds.
func TestAnOldClaimLeavesTheNewEntryAlone(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t)
	old := open(t, m, 1)
	require.NoError(t, m.Clear(1))
	fresh := open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, fresh.Close()) })

	require.NoError(t, old.Close())

	addStream(t, fresh)
	joined := open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, joined.Close()) })
	assert.Same(t, fresh.e, joined.e, "the next open joins the fresh entry")
	assert.Equal(t, 1, streamsIn(t, joined))
}

// A cache going away with its record takes its file, and nothing may open the id again:
// a straggler holding a view from before the teardown would otherwise create a file
// nothing would ever delete.
func TestRemoveClosesTheFileAndRefusesTheID(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	held := open(t, m, 1)

	require.NoError(t, m.Remove(1))

	assertClosed(t, held)
	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
	assert.NoError(t, held.Close())
	_, err := m.OpenOrCreate(1)
	assert.ErrorIs(t, err, ErrRemoved)
	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
}

// The cache pass runs again after a crash between the removal and the finalizer's clear.
func TestRemoveTwiceSucceeds(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t)
	require.NoError(t, open(t, m, 1).Close())

	require.NoError(t, m.Remove(1))

	assert.NoError(t, m.Remove(1))
	assert.NoError(t, m.Remove(2), "a cache never opened is removed already")
}

func TestCloseClosesTheFilesStillHeld(t *testing.T) {
	t.Parallel()
	m := NewManager(t.TempDir(), testRetention)
	held := open(t, m, 1)

	require.NoError(t, m.Close())

	assertClosed(t, held)
	assert.NoError(t, held.Close())
}

// The cluster service closes before the manager, but its cache pass clears a cache's
// finalizer on Remove's answer and nothing else would ever delete the file. So both
// still delete after Close, and nothing opens.
func TestClearAndRemoveAfterCloseDeleteTheFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := NewManager(dir, testRetention)
	require.NoError(t, open(t, m, 1).Close())
	require.NoError(t, open(t, m, 2).Close())
	require.NoError(t, m.Close())

	require.NoError(t, m.Clear(1))
	require.NoError(t, m.Remove(2))

	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
	assert.NoFileExists(t, filepath.Join(dir, "2.db"))
	_, err := m.OpenOrCreate(1)
	assert.ErrorIs(t, err, ErrClosed)
	assert.NoFileExists(t, filepath.Join(dir, "1.db"))
}

// A -wal left beside a fresh database would be replayed into it, so a delete that fails
// partway leaves the database whole rather than its -wal alone.
func TestADeleteThatFailsKeepsTheDatabase(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	require.NoError(t, open(t, m, 1).Close())
	// A directory with something in it is what os.Remove refuses on every platform.
	wal := filepath.Join(dir, "1.db-wal")
	require.NoError(t, os.MkdirAll(filepath.Join(wal, "x"), 0o700))

	assert.Error(t, m.Clear(1))

	assert.FileExists(t, filepath.Join(dir, "1.db"))
}

// The caller retries a failed removal, and an open in between would recreate the file.
func TestARemoveThatFailsStillRefusesTheID(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	require.NoError(t, open(t, m, 1).Close())
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "1.db-wal", "x"), 0o700))

	assert.Error(t, m.Remove(1))

	_, err := m.OpenOrCreate(1)
	assert.ErrorIs(t, err, ErrRemoved)
}

func TestAnOpenThatCannotMakeTheDirectoryFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "logs")
	require.NoError(t, os.WriteFile(dir, nil, 0o600))
	m := NewManager(dir, testRetention)
	t.Cleanup(func() { assert.NoError(t, m.Close()) })

	_, err := m.OpenOrCreate(1)

	assert.Error(t, err)
}

func TestAnOpenThatCannotOpenTheFileFails(t *testing.T) {
	t.Parallel()
	m, dir := newTestManager(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "1.db", "x"), 0o700))

	_, err := m.OpenOrCreate(1)

	assert.Error(t, err)
}

// An open meeting a clear in progress waits for it, so it never opens the file the clear
// is about to delete, and then creates a fresh one.
func TestAnOpenWaitsOutAClear(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t)
	held := open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, held.Close()) })
	addStream(t, held)
	_, done, err := held.use()
	require.NoError(t, err)
	done = sync.OnceFunc(done)
	t.Cleanup(done)
	cleared := make(chan error, 1)
	go func() { cleared <- m.Clear(1) }()
	require.Eventually(t, func() bool {
		_, probeDone, err := held.use()
		if err == nil {
			probeDone()
		}
		return errors.Is(err, ErrClosed)
	}, 5*time.Second, time.Millisecond)
	opened := make(chan *Store, 1)

	go func() { opened <- open(t, m, 1) }()

	// A negative assertion: nothing marks the open as waiting, so it gets a window.
	select {
	case <-opened:
		t.Fatal("the open returned under a clear in progress")
	case <-time.After(50 * time.Millisecond):
	}
	done()
	require.NoError(t, <-cleared)
	fresh := <-opened
	t.Cleanup(func() { assert.NoError(t, fresh.Close()) })
	assert.Zero(t, streamsIn(t, fresh))
}

// Close finds no entry for a file still opening, so the open closes what it opened.
func TestAnOpenThatCloseOvertakesClosesItsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := NewManager(dir, testRetention)
	// Another connection's exclusive lock holds the open inside SQLite's busy wait.
	locker, err := sql.Open("sqlite", filepath.Join(dir, "1.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, locker.Close()) })
	lock, err := locker.Conn(t.Context())
	require.NoError(t, err)
	_, err = lock.ExecContext(t.Context(), "BEGIN EXCLUSIVE")
	require.NoError(t, err)
	opened := make(chan error, 1)
	go func() {
		_, err := m.OpenOrCreate(1)
		opened <- err
	}()
	require.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, opening := m.busy[1]
		return opening
	}, 5*time.Second, time.Millisecond)

	require.NoError(t, m.Close())
	_, err = lock.ExecContext(t.Context(), "ROLLBACK")
	require.NoError(t, err)
	require.NoError(t, lock.Close())

	assert.ErrorIs(t, <-opened, ErrClosed)
}

// openChunk marks c, a chunk of the file behind s, open, as a tailer leaves the chunk it
// writes into.
func openChunk(t *testing.T, s *Store, c Chunk) {
	t.Helper()
	using(t, s, func(f *file) {
		_, err := f.db.Exec(`UPDATE chunks SET open = 1 WHERE id = ?`, c.ID)
		require.NoError(t, err)
	})
}

// A chunk a crash or a failed write left open is closed when the file is opened again,
// the one point at which no tailer can be running, and a tailer then extends it.
func TestAStaleOpenChunkIsClosedWhenTheFileOpens(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t)
	s := open(t, m, 1)
	id := streamOf(t, s)
	openChunk(t, s, insertChunk(t, s, id, chunk(at(0, 0), at(1, 0))))
	require.True(t, chunksOfStream(t, s, id)[0].Open)
	require.NoError(t, s.Close())

	s = open(t, m, 1)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })

	assert.False(t, chunksOfStream(t, s, id)[0].Open)
	tail, err := s.OpenTail(id, "kubelet", ptr(at(1, 0)), true)
	require.NoError(t, err)
	require.NoError(t, tail.Close(t.Context(), lines(at(1, 1))))
	assert.Equal(t, []Chunk{chunk(at(0, 0), at(1, 1))}, chunksOfStream(t, s, id), "a tailer extends it")
}
