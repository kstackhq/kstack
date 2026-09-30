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
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// A reader pool that inherits the writer's DSN carries _txlock=immediate, so a read
// transaction that omits ReadOnly takes the WAL write lock — the one thing the reader pool
// exists to avoid, and a latency mystery rather than an error. query_only is what refuses it.
func TestOpenReaderRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	w, err := OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	_, err = w.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY); INSERT INTO t VALUES (1);`)
	require.NoError(t, err)

	r, err := OpenReader(path, 2)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	_, err = r.Exec(`INSERT INTO t VALUES (2)`)
	require.Error(t, err, "a write on the reader pool")

	var n int
	require.NoError(t, r.QueryRow(`SELECT count(*) FROM t`).Scan(&n))
	require.Equal(t, 1, n)

	// The trap: under query_only an _txlock=immediate BEGIN fails outright, so this goes
	// red the moment the writer's DSN is copied back onto this pool.
	tx, err := r.BeginTx(context.Background(), nil)
	require.NoError(t, err, "a transaction that did not ask for ReadOnly")
	require.NoError(t, tx.Rollback())
}

// SQLite ignores auto_vacuum once any table exists, so the only place it can be set is the
// connection that creates the file — a migration that set it would be a silent no-op.
func TestOpenWriterCreatesAnIncrementalFile(t *testing.T) {
	db, err := OpenWriter(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	const incremental = 2
	var mode int
	require.NoError(t, db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode))
	require.Equal(t, incremental, mode)
}

// sql.Open is lazy, so a file that cannot be created fails at the open, not at the first
// statement a caller runs.
func TestOpenWriterRefusesAFileItCannotCreate(t *testing.T) {
	_, err := OpenWriter(filepath.Join(t.TempDir(), "missing", "test.db"))
	require.Error(t, err)
}

// The DSN sets the mode on a file this build creates; it cannot reach one that already
// exists, because SQLite ignores the pragma once any table is in it. Deleting the repair
// branch would strand every such file at its high-water mark, with a janitor's
// PRAGMA incremental_vacuum a permanent no-op on it.
func TestOpenWriterRepairsAFileThatPredatesTheDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	legacy, err := sql.Open("sqlite", "file:"+path)
	require.NoError(t, err)
	_, err = legacy.Exec(`CREATE TABLE legacy (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	db, err := OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	const incremental = 2
	var mode int
	require.NoError(t, db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode))
	require.Equal(t, incremental, mode)
}

// database/sql keeps 2 idle connections by default, so a pool sized above that closes
// exactly the connections it was sized to open — and the next read reopens them, re-running
// the DSN's pragmas, on a path a watch walks every 250ms.
func TestOpenReaderKeepsEveryConnectionItOpens(t *testing.T) {
	const n = 4
	db, err := OpenReader(filepath.Join(t.TempDir(), "test.db"), n)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Held all at once: a Conn is exclusive until it is put back, so each checkout has to
	// open a new one. Connections released one at a time would be served by the same one,
	// and Idle would fall short for a reason unrelated to MaxIdleConns. No clock is
	// involved — a connection returns to the pool the moment it is released.
	conns := make([]*sql.Conn, n)
	for i := range conns {
		conns[i], err = db.Conn(context.Background())
		require.NoError(t, err)
	}
	for _, c := range conns {
		require.NoError(t, c.Close())
	}

	require.Equal(t, n, db.Stats().Idle)
	require.Zero(t, db.Stats().MaxIdleClosed)
}

// A query pool is read-only at open: a statement that turns query_only off still cannot
// write, since mode=ro is on the connection and no pragma undoes it.
func TestOpenQueryIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	w, err := OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	_, err = w.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY); INSERT INTO t VALUES (1);`)
	require.NoError(t, err)

	q := OpenQuery(&sqlite.Driver{}, path, 2)
	t.Cleanup(func() { q.Close() })

	conn, err := q.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(context.Background(), `PRAGMA query_only = false`)
	require.NoError(t, err)
	_, err = conn.ExecContext(context.Background(), `INSERT INTO t VALUES (2)`)
	require.ErrorContains(t, err, "readonly")

	var n int
	require.NoError(t, conn.QueryRowContext(context.Background(), `SELECT count(*) FROM t`).Scan(&n))
	require.Equal(t, 1, n)
}

// Every checkout is a fresh connection, so nothing one query leaves on its connection
// reaches the next.
func TestOpenQueryKeepsNoIdleConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	w, err := OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })

	q := OpenQuery(&sqlite.Driver{}, path, 2)
	t.Cleanup(func() { q.Close() })

	conn, err := q.Conn(context.Background())
	require.NoError(t, err)
	_, err = conn.ExecContext(context.Background(), `CREATE TEMP TABLE left_behind (x)`)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Zero(t, q.Stats().Idle)

	var n int
	require.NoError(t, q.QueryRow(`SELECT count(*) FROM temp.sqlite_master WHERE name = 'left_behind'`).Scan(&n))
	require.Zero(t, n)
}

// The pool opens through the driver it is handed, so that driver's functions reach its
// connections and no other.
func TestOpenQueryOpensThroughItsDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	w, err := OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	d := &sqlite.Driver{}
	require.NoError(t, d.RegisterDeterministicScalarFunction("only_here", 0,
		func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return int64(7), nil }))

	q := OpenQuery(d, path, 1)
	t.Cleanup(func() { q.Close() })

	var n int
	require.NoError(t, q.QueryRow(`SELECT only_here()`).Scan(&n))
	require.Equal(t, 7, n)
	require.ErrorContains(t, w.QueryRow(`SELECT only_here()`).Scan(&n), "no such function")
}

// The connector opens through its driver, and refuses a context already done.
func TestTheQueryConnectorIsItsDrivers(t *testing.T) {
	d := &sqlite.Driver{}
	c := connector{d: d, dsn: "file:" + filepath.Join(t.TempDir(), "test.db")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Connect(ctx)

	require.ErrorIs(t, err, context.Canceled)
	require.Same(t, d, c.Driver())
}
