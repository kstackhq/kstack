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

package chatsvc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// The sweeper subscribes before its startup sweep and runs on every clusters signal:
// a marked cluster's chats go, and another cluster's stay.
func TestTheSweeperDeletesAMarkedClustersChats(t *testing.T) {
	s := newTestService(t)
	now := time.UnixMilli(1_000).UTC()
	mine := seedChat(t, s.db, aChat("7", now))
	seedTurn(t, s.db, mine.ID, now)
	also := seedChat(t, s.db, aChat("7", now))
	elsewhere := seedChat(t, s.db, aChat("8", now))

	markCluster(t, s.db, "7")
	s.db.Notify(appdb.KeyClusters)

	// The signal's sweep is the one that takes both chats.
	for {
		testutil.Wait(t, s.onSwept, "a sweep")
		_, ok, err := s.Get(t.Context(), also.ID)
		require.NoError(t, err)
		if !ok {
			break
		}
	}
	for _, id := range []ChatID{mine.ID, also.ID} {
		_, ok, err := s.Get(t.Context(), id)
		require.NoError(t, err)
		assert.False(t, ok, "chat %s was filed under the marked cluster", id)
	}
	_, ok, err := s.Get(t.Context(), elsewhere.ID)
	require.NoError(t, err)
	assert.True(t, ok, "another cluster's chat is not the sweeper's to take")
	assert.Zero(t, tableCount(t, s.db, "messages"), "the messages went with their chat")
}

// A sweep that finds nothing to do notifies nobody, or two subscribers of one key
// would wake each other forever.
func TestASweepThatCleansNothingNotifiesNobody(t *testing.T) {
	s := newTestService(t)
	sub := s.db.Subscribe(appdb.KeyClusters)
	defer sub.Close()

	markCluster(t, s.db, "9")
	s.db.Notify(appdb.KeyClusters)
	testutil.Recv(t, sub.Chan(), "the mark's own signal")
	testutil.Wait(t, s.onSwept, "the sweep to finish")

	select {
	case <-sub.Chan():
		t.Fatal("a no-op sweep must not notify")
	case <-time.After(10 * s.sweepRetry):
		// The window is a multiple of the retry cadence, which is also what the
		// sweeper would re-arm itself on had the sweep failed.
	}
}

// A marked cluster the last run left behind is swept at startup, with no signal.
func TestTheSweeperRecoversAtStartup(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	c := seedChat(t, db, aChat("7", time.UnixMilli(1_000).UTC()))
	markCluster(t, db, "7")
	require.NoError(t, db.Close())

	s := startService(t, dir)

	_, ok, err := s.Get(t.Context(), c.ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

// A sweep that fails re-arms its own retry: no notification follows a failure, since
// the mirror cannot remove a row while chats remain and notifies only on removal.
func TestAFailedSweepRetriesOnItsOwn(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("7", time.UnixMilli(1_000).UTC()))
	failures := 2
	s.deleteWrite = func(ctx context.Context, id ChatID) (bool, error) {
		if failures > 0 {
			failures--
			return false, assert.AnError
		}
		return s.deleteRow(ctx, id)
	}

	markCluster(t, s.db, "7")
	s.db.Notify(appdb.KeyClusters)

	// Sweeps until the retries have finished what the failures left: the
	// signal's, and one per failed attempt, come first.
	for {
		testutil.Wait(t, s.onSwept, "a sweep")
		_, ok, err := s.Get(t.Context(), c.ID)
		require.NoError(t, err)
		if !ok {
			break
		}
	}
	assert.Equal(t, 0, failures, "every failure was retried through")
}

// The read of the clusters table reports a store that will not answer.
func TestTheClusterReadsReportAStorageFault(t *testing.T) {
	s := newTestService(t)
	require.NoError(t, s.store.Close())

	_, err := markedClusterIDs(t.Context(), s.store.Stmts())
	assert.Error(t, err)
}

// A service that is stopping refuses to start its sweeper, the way every other
// entrant is refused once stop has begun.
func TestStartAfterStopRefusesTheSweeper(t *testing.T) {
	s, err := newService(openTestDB(t, t.TempDir()), chatsDirIn(t.TempDir()), fakeLLM(), noClusterCards, nil, testReaders, noLists, sandbox.Status{}, testSecurity(t))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	require.NoError(t, s.stop(t.Context()))

	_, err = s.Start(t.Context())

	assert.ErrorIs(t, err, ErrStopping)
}

// The sweep a cluster delete runs: every chat filed under it goes, and nothing
// else is touched.
func TestDeleteByClusterDeletesEachChat(t *testing.T) {
	s := newTestService(t)
	now := time.UnixMilli(1_000).UTC()
	first := seedChat(t, s.db, aChat("7", now))
	second := seedChat(t, s.db, aChat("7", now))
	elsewhere := seedChat(t, s.db, aChat("8", now))

	n, err := s.deleteByCluster(t.Context(), "7")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	for _, id := range []ChatID{first.ID, second.ID} {
		_, ok, err := s.Get(t.Context(), id)
		require.NoError(t, err)
		assert.False(t, ok, "chat %s was filed under the swept cluster", id)
	}
	_, ok, err := s.Get(t.Context(), elsewhere.ID)
	require.NoError(t, err)
	assert.True(t, ok, "another cluster's chat is not the sweep's to take")
}

func TestDeleteByClusterOfAClusterWithNoChatsIsANoOp(t *testing.T) {
	s := newTestService(t)
	n, err := s.deleteByCluster(t.Context(), "9")
	assert.NoError(t, err)
	assert.Zero(t, n)
}

// The read that names the chats is the sweep's first statement, and its failure is
// the caller's to see: a sweep that could not list is not a sweep that found none.
func TestDeleteByClusterReportsAReadThatFailed(t *testing.T) {
	s := newTestService(t)
	require.NoError(t, s.store.Close())

	_, err := s.deleteByCluster(t.Context(), "1")
	assert.ErrorContains(t, err, "conversation ids by cluster")
}

// A chats' directory the sweep cannot list is left for the next start.
func TestTheStartSweepLeavesAChatsDirectoryItCannotList(t *testing.T) {
	dir := t.TempDir()
	s, err := newService(openTestDB(t, dir), chatsDirIn(dir), fakeLLM(), noClusterCards, nil, testReaders, noLists, sandbox.Status{}, testSecurity(t))
	require.NoError(t, err)
	require.NoError(t, s.chatsRoot.Close())
	logs := testutil.CaptureLogs(t)

	s.sweepChatDirs()

	assert.Contains(t, logs.String(), "could not list the chats' directory")
}

// The start sweep removes every entry of the chats' directory that is not a
// chat's: what a failed removal or a crash left.
func TestTheStartSweepRemovesTheDirectoriesOfGoneChats(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	kept := seedChat(t, db, aChat("1", time.Now()))
	results := chatsDirIn(dir)
	for _, name := range []string{string(kept.ID), "01a0ce44-0000-7000-8000-000000000000"} {
		require.NoError(t, os.MkdirAll(filepath.Join(results, name), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(results, name, "out.txt"), nil, 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(results, "stray"), nil, 0o600))

	s, err := newService(db, chatsDirIn(dir), fakeLLM(), noClusterCards, nil, testReaders, noLists, sandbox.Status{}, testSecurity(t))
	require.NoError(t, err)
	startPrepared(t, s)

	entries, err := os.ReadDir(results)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, string(kept.ID), entries[0].Name())
	assert.FileExists(t, filepath.Join(results, string(kept.ID), "out.txt"))
}
