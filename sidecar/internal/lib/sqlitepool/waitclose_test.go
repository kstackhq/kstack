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

package sqlitepool

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// A connection in use when the pool closes, as one a cancelled transaction's rollback
// still holds on database/sql's goroutine, has closed by the time WaitClosed returns
// (#648).
func TestWaitClosedWaitsForAConnectionInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenWriter(path)
	require.NoError(t, err)
	readDB, err := OpenReader(path, 1)
	require.NoError(t, err)
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, readDB.Close())

	done := make(chan error, 1)
	go func() { done <- WaitClosed(CloseBudget, db, readDB) }()
	require.NoError(t, conn.Close())
	require.NoError(t, testutil.Recv(t, done, "wait"))
	require.Zero(t, db.Stats().OpenConnections)
}

func TestWaitClosedGivesUpOnAConnectionHeldPastTheBudget(t *testing.T) {
	db, err := OpenWriter(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	require.NoError(t, db.Close())

	require.ErrorContains(t, WaitClosed(0, db), "1 connections still open")
}
