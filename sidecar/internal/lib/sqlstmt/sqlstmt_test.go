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

package sqlstmt

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitepool"
)

// A four-statement table over one table of the test's own, one per pool and a
// reader-only read of the kind a read transaction takes.
type testStmt int

const (
	stmtInsert testStmt = iota
	stmtCount
	stmtList
	stmtHead
	numTestStmts int = iota
)

var testTable = []Statement{
	stmtInsert: OnWriter(`INSERT INTO t (id) VALUES (?)`),
	stmtCount:  OnBoth(`SELECT COUNT(*) FROM t`),
	stmtList:   OnReader(`SELECT id FROM t ORDER BY id`),
	stmtHead:   OnReader(`SELECT MAX(id) FROM t`),
}

// openPools opens a writer and a reader over a fresh file holding table t.
func openPools(t *testing.T) (write, read *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	write, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = write.Close() })
	_, err = write.ExecContext(t.Context(), `CREATE TABLE t (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	read, err = sqlitepool.OpenReader(path, 2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = read.Close() })
	return write, read
}

func prepareTestSet(t *testing.T) *Set[testStmt] {
	t.Helper()
	write, read := openPools(t)
	s, err := Prepare[testStmt](t.Context(), write, read, testTable)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func count(t *testing.T, st Stmts[testStmt]) int {
	t.Helper()
	var n int
	require.NoError(t, st.QueryRow(t.Context(), stmtCount).Scan(&n))
	return n
}

func insert(t *testing.T, st Stmts[testStmt], id string) {
	t.Helper()
	_, err := st.Exec(t.Context(), stmtInsert, id)
	require.NoError(t, err)
}

// Each pool takes the ids declared for it and no other: a write prepared on the
// reader would meet query_only at execution, and a read prepared on the writer alone
// would be unreachable outside a transaction.
func TestPrepareCompilesEachStatementOnItsPools(t *testing.T) {
	s := prepareTestSet(t)
	require.Len(t, s.write, numTestStmts)
	require.Len(t, s.read, numTestStmts)
	for id, st := range testTable {
		assert.Equal(t, st.On == Writer || st.On == Both, s.write[id] != nil, st.Text)
		assert.Equal(t, st.On == Reader || st.On == Both, s.read[id] != nil, st.Text)
	}
}

// A statement that will not compile fails the open, whichever pool it belongs to; so
// does an id the table left without text.
func TestPrepareRefusesABadTable(t *testing.T) {
	write, read := openPools(t)
	for name, on := range map[string]Pool{"writer": Writer, "reader": Reader} {
		t.Run(name, func(t *testing.T) {
			table := []Statement{{Text: `SELECT nothing FROM nowhere`, On: on}}
			_, err := Prepare[testStmt](t.Context(), write, read, table)
			require.ErrorContains(t, err, "prepare")
			require.ErrorContains(t, err, "nowhere")
		})
	}
	_, err := Prepare[testStmt](t.Context(), write, read, []Statement{stmtCount: OnBoth(`SELECT 1`)})
	require.ErrorContains(t, err, "statement 0 has no text")
}

// Closing the set leaves the pools open: they are the caller's, and another set may
// still be on them.
func TestSetCloseLeavesThePoolsOpen(t *testing.T) {
	s := prepareTestSet(t)
	require.NoError(t, s.Close())
	require.NoError(t, s.writeDB.Ping())
	require.NoError(t, s.readDB.Ping())
}

// A helper issued inside a transaction is rebound onto it, so its write goes with
// the rollback rather than landing on the pool beside it.
func TestAStatementIssuedInATransactionRollsBackWithIt(t *testing.T) {
	s := prepareTestSet(t)
	tx, err := s.writeDB.BeginTx(t.Context(), nil)
	require.NoError(t, err)

	st := s.tx(tx, s.write)
	insert(t, st, "a")
	require.Equal(t, 1, count(t, st))
	require.NoError(t, tx.Rollback())

	assert.Equal(t, 0, count(t, s.Stmts()), "the write escaped the transaction")
}

// Tx.StmtContext allocates a fresh statement per call, registers it db-wide and
// releases it only at commit. One rebinding per id, reused for the transaction's life.
func TestATransactionRebindsEachStatementOnce(t *testing.T) {
	s := prepareTestSet(t)
	tx, err := s.writeDB.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback() //nolint:errcheck // nothing to keep

	st := s.tx(tx, s.write)
	assert.Same(t, st.stmt(t.Context(), stmtInsert), st.stmt(t.Context(), stmtInsert))
}

// A read declared Both runs on the writer inside a transaction, so it sees the
// transaction's own writes rather than a reader's snapshot. A failing fn rolls back.
func TestAReadInsideATransactionSeesItsWrites(t *testing.T) {
	s := prepareTestSet(t)
	errRollBack := errors.New("roll back")
	err := s.InTx(t.Context(), func(st Stmts[testStmt]) error {
		insert(t, st, "a")
		assert.Equal(t, 1, count(t, st), "the read missed the transaction's own insert")
		return errRollBack
	})
	require.ErrorIs(t, err, errRollBack)
	assert.Equal(t, 0, count(t, s.Stmts()))

	require.NoError(t, s.InTx(t.Context(), func(st Stmts[testStmt]) error {
		insert(t, st, "b")
		return nil
	}))
	assert.Equal(t, 1, count(t, s.Stmts()))
}

// A read transaction rebinds the reader's copy, and every read in it comes off one
// snapshot: a write landing between two of them is not seen by the second.
func TestAReadTransactionIsOneSnapshotOnTheReader(t *testing.T) {
	s := prepareTestSet(t)
	insert(t, s.Stmts(), "a")

	require.NoError(t, s.InReadTx(t.Context(), func(st Stmts[testStmt]) error {
		var head string
		require.NoError(t, st.QueryRow(t.Context(), stmtHead).Scan(&head))
		require.Equal(t, "a", head)

		insert(t, s.Stmts(), "b")
		assert.Equal(t, 1, count(t, st), "the second read left the snapshot")
		return nil
	}))
	assert.Equal(t, 2, count(t, s.Stmts()))
}

// An id the transaction's pool does not hold is a table error, caught at the first
// call rather than reported as the row's failure.
func TestAnIdOffTheTransactionsPoolPanics(t *testing.T) {
	s := prepareTestSet(t)
	assert.Panics(t, func() {
		_ = s.InTx(t.Context(), func(st Stmts[testStmt]) error {
			_, err := st.Query(t.Context(), stmtList)
			return err
		})
	})
	assert.Panics(t, func() {
		_ = s.InReadTx(t.Context(), func(st Stmts[testStmt]) error {
			_, err := st.Exec(t.Context(), stmtInsert, "a")
			return err
		})
	})
}

// InReadTx hands fn's error back as it is, so a caller wraps it once.
func TestInReadTxReturnsWhatFnReturned(t *testing.T) {
	s := prepareTestSet(t)
	errRead := errors.New("read failed")
	err := s.InReadTx(t.Context(), func(Stmts[testStmt]) error { return errRead })
	require.ErrorIs(t, err, errRead)
}

// A closed pool fails a transaction at begin, and says so.
func TestInTxReportsABeginItCouldNotRun(t *testing.T) {
	s := prepareTestSet(t)
	require.NoError(t, s.writeDB.Close())
	require.NoError(t, s.readDB.Close())
	err := s.InTx(context.Background(), func(Stmts[testStmt]) error { return nil })
	require.ErrorContains(t, err, "begin")
	err = s.InReadTx(context.Background(), func(Stmts[testStmt]) error { return nil })
	require.ErrorContains(t, err, "begin")
}

// A closed set issues nothing inside a transaction either: Tx.StmtContext would
// quietly re-prepare a closed statement, so the set refuses the transaction itself,
// as the pools refuse a closed statement outside one.
func TestAClosedSetRefusesATransaction(t *testing.T) {
	s := prepareTestSet(t)
	require.NoError(t, s.Close())
	ran := false
	fn := func(Stmts[testStmt]) error { ran = true; return nil }
	require.ErrorIs(t, s.InTx(t.Context(), fn), ErrClosed)
	require.ErrorIs(t, s.InReadTx(t.Context(), fn), ErrClosed)
	require.False(t, ran)
}
