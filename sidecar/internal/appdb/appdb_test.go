package appdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amorey/gobus"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// Open creates a missing parent dir, runs the embedded migrations (recording
// the sequence in schema_migrations), and the file survives a close/reopen
// cycle.
func TestOpenCreatesMigratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "app.db")

	db, err := Open(path, 0)
	require.NoError(t, err)
	require.FileExists(t, path)

	// Migrations ran: the schema_migrations sequence is at version 1.
	var v int
	require.NoError(t, db.Write.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 1, v)
	requireTables(t, db.Write)
	require.NoError(t, db.Close())

	// Reopening the same file is idempotent — migrations are not re-applied.
	db2, err := Open(path, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db2.Close() })
	require.NoError(t, db2.Write.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 1, v)
	requireTables(t, db2.Write)
}

// The reader is opened through OpenReader, not a second OpenWriter: query_only is in
// its DSN, so a write there is refused — and a read there sees what the writer
// committed, which is the two pools sharing one file.
func TestOpenReturnsAWriterAndAReader(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Write.Exec(`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('c', 'cloud', 0, 0)`)
	require.NoError(t, err)
	const insert = `INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES (?, 'c', 'chat', 0, 0)`
	_, err = db.Read.Exec(insert, "r")
	require.ErrorContains(t, err, "attempt to write a readonly database")
	_, err = db.Write.Exec(insert, "w")
	require.NoError(t, err)

	var id string
	require.NoError(t, db.Read.QueryRow(`SELECT id FROM chats`).Scan(&id))
	require.Equal(t, "w", id)
}

// A chat's messages and runs reference each other both ways, and neither
// cascades off the other: the run keeps its message, the answer keeps its run. One
// DELETE of the chat takes all of them, because SQLite checks a foreign key
// at the end of the statement — after the cascade off chats has removed
// both sides.
func TestDeletingAChatTakesItsMessagesAndRuns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, q := range []string{
		`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('k', 'cloud', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', 'k', 'chat', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, request_key, created_at) VALUES ('u', 'c', 0, 'user', '[]', 'key', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at)
		 VALUES ('r', 'chat', 'dev', 'chat', 'c', 'u', 'p', 'm', 'fake', 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, run_id, created_at) VALUES ('a', 'c', 1, 'assistant', '[]', 'r', 0)`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}

	_, err = db.Write.Exec(`DELETE FROM chats WHERE id = 'c'`)
	require.NoError(t, err)
	for _, table := range []string{"messages", "agent_runs"} {
		var n int
		require.NoError(t, db.Read.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		require.Zero(t, n, table)
	}
}

// The two keys the send transaction relies on: a seq handed out twice in one
// chat is refused, and a message starts at most one run.
func TestMessagesAndRunsAreKeyedForOneTurn(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, q := range []string{
		`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('k', 'cloud', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', 'k', 'chat', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, created_at) VALUES ('u', 'c', 0, 'user', '[]', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at)
		 VALUES ('r', 'chat', 'dev', 'chat', 'c', 'u', 'p', 'm', 'fake', 0)`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}

	_, err = db.Write.Exec(`INSERT INTO messages (id, chat_id, seq, role, content, created_at) VALUES ('u2', 'c', 0, 'user', '[]', 0)`)
	require.ErrorContains(t, err, "UNIQUE")
	_, err = db.Write.Exec(`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at)
		 VALUES ('r2', 'chat', 'dev', 'chat', 'c', 'u', 'p', 'm', 'fake', 0)`)
	require.ErrorContains(t, err, "UNIQUE")
}

// requireTables asserts the schema the initial migration creates: exactly the
// seven application tables (schema_migrations is the migrator's), each STRICT,
// the small-row ones WITHOUT ROWID, and the named indexes as declared — partial
// WHEREs and column order included.
func requireTables(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT name, wr, strict FROM pragma_table_list
		WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		var withoutRowid, strict int
		require.NoError(t, rows.Scan(&name, &withoutRowid, &strict))
		tables = append(tables, fmt.Sprintf("%s wr=%d strict=%d", name, withoutRowid, strict))
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{
		"agent_runs wr=0 strict=1",
		"approvals wr=1 strict=1",
		"background_tasks wr=1 strict=1",
		"chat_grants wr=1 strict=1",
		"chats wr=1 strict=1",
		"clusters wr=1 strict=1",
		"llm_calls wr=0 strict=1",
		"memories wr=0 strict=1",
		"messages wr=0 strict=1",
		"tool_calls wr=0 strict=1",
	}, tables)

	idx, err := db.Query(`SELECT sql FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL ORDER BY name`)
	require.NoError(t, err)
	defer idx.Close()
	var indexes []string
	for idx.Next() {
		var ddl string
		require.NoError(t, idx.Scan(&ddl))
		indexes = append(indexes, strings.Join(strings.Fields(ddl), " "))
	}
	require.NoError(t, idx.Err())
	require.Equal(t, []string{
		"CREATE INDEX agent_runs_chat_idx ON agent_runs (chat_id)",
		"CREATE INDEX agent_runs_cluster_idx ON agent_runs (cluster_id)",
		"CREATE INDEX agent_runs_parent_idx ON agent_runs (parent_run_id)",
		"CREATE INDEX agent_runs_queued_idx ON agent_runs (id) WHERE status = 'queued'",
		"CREATE INDEX agent_runs_waiting_idx ON agent_runs (chat_id) WHERE status = 'waiting_approval'",
		"CREATE INDEX approvals_actions_idx ON approvals (tool_call_id) WHERE kind = 'action'",
		"CREATE UNIQUE INDEX approvals_call_idx ON approvals (tool_call_id) WHERE kind = 'call'",
		"CREATE INDEX approvals_pending_idx ON approvals (id) WHERE status = 'pending'",
		"CREATE INDEX background_tasks_chat_idx ON background_tasks (chat_id)",
		"CREATE INDEX chat_grants_chat_idx ON chat_grants (chat_id)",
		"CREATE UNIQUE INDEX llm_calls_run_idx ON llm_calls (run_id, seq)",
		"CREATE UNIQUE INDEX memories_name ON memories (ifnull(cluster_id, ''), name)",
		"CREATE INDEX messages_run_idx ON messages (run_id)",
		"CREATE UNIQUE INDEX tool_calls_llm_call_idx ON tool_calls (llm_call_id, seq)",
		"CREATE INDEX tool_calls_spawned_idx ON tool_calls (spawned_run_id)",
	}, indexes)
}

// foreignKeys is a table's references as (column → table.column, on delete).
func foreignKeys(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list(?) ORDER BY id`, table)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var from, to, col, onDelete string
		require.NoError(t, rows.Scan(&from, &to, &col, &onDelete))
		out = append(out, fmt.Sprintf("%s -> %s.%s %s", from, to, col, onDelete))
	}
	require.NoError(t, rows.Err())
	return out
}

// The call tables hang off the runs: a model call cascades off its run, a tool
// call off its model call, an approval off its tool call, and a spawned run's
// deletion nulls the tool call that spawned it. One DELETE of the chat
// takes every row of the turn, and the foreign keys check clean before and after.
func TestTheCallTablesCascadeOffTheRun(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Equal(t, []string{"run_id -> agent_runs.id CASCADE"}, foreignKeys(t, db.Read, "llm_calls"))
	require.Equal(t, []string{"spawned_run_id -> agent_runs.id SET NULL", "llm_call_id -> llm_calls.id CASCADE"}, foreignKeys(t, db.Read, "tool_calls"))
	require.Equal(t, []string{"tool_call_id -> tool_calls.id CASCADE"}, foreignKeys(t, db.Read, "approvals"))
	require.Equal(t, []string{"tool_call_id -> tool_calls.id CASCADE", "chat_id -> chats.id CASCADE"},
		foreignKeys(t, db.Read, "background_tasks"))

	for _, q := range []string{
		`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('k', 'cloud', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', 'k', 'chat', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, request_key, created_at) VALUES ('u', 'c', 0, 'user', '[]', 'key', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at)
		 VALUES ('r', 'chat', 'dev', 'chat', 'c', 'u', 'p', 'm', 'fake', 0)`,
		`INSERT INTO agent_runs (id, parent_run_id, agent_type, app_version, trigger, chat_id, provider, model, dialect, created_at)
		 VALUES ('child', 'r', 'chat', 'dev', 'agent', 'c', 'p', 'm', 'fake', 0)`,
		`INSERT INTO llm_calls (id, run_id, seq, provider, model, started_at) VALUES ('l', 'r', 0, 'p', 'm', 0)`,
		`INSERT INTO tool_calls (id, llm_call_id, seq, tool_name, status, spawned_run_id, created_at) VALUES ('t', 'l', 0, 'spawn', 'running', 'child', 0)`,
		`INSERT INTO approvals (id, tool_call_id, created_at) VALUES ('a', 't', 0)`,
		`INSERT INTO background_tasks (id, chat_id, tool_call_id, output_path, status, started_at) VALUES ('b', 'c', 't', '/o', 'running', 0)`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}
	requireForeignKeysClean(t, db.Read)

	_, err = db.Write.Exec(`DELETE FROM agent_runs WHERE id = 'child'`)
	require.NoError(t, err)
	var spawned *string
	require.NoError(t, db.Read.QueryRow(`SELECT spawned_run_id FROM tool_calls WHERE id = 't'`).Scan(&spawned))
	require.Nil(t, spawned, "the spawned run's deletion nulls the pointer")

	_, err = db.Write.Exec(`DELETE FROM chats WHERE id = 'c'`)
	require.NoError(t, err)
	for _, table := range []string{"messages", "agent_runs", "llm_calls", "tool_calls", "approvals", "background_tasks"} {
		var n int
		require.NoError(t, db.Read.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		require.Zero(t, n, table)
	}
	requireForeignKeysClean(t, db.Read)
}

// A cluster's memories go with its row; a chat's delete leaves the memories it
// wrote and clears the pointer. A name is unique within a scope, the global scope
// included, though NULL never equals NULL.
func TestMemoriesHangOffTheirClusterAndOutliveTheirChat(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Equal(t, []string{"chat_id -> chats.id SET NULL", "cluster_id -> clusters.id CASCADE"},
		foreignKeys(t, db.Read, "memories"))

	for _, q := range []string{
		`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('k', 'cloud', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', 'k', 'chat', 0, 0)`,
		`INSERT INTO memories (id, cluster_id, name, body, written_by, chat_id, created_at, updated_at)
		 VALUES ('m1', 'k', 'n', 'b', 'model', 'c', 0, 0)`,
		`INSERT INTO memories (id, cluster_id, name, body, written_by, created_at, updated_at)
		 VALUES ('m2', NULL, 'n', 'b', 'user', 0, 0)`,
		`INSERT INTO memories (id, cluster_id, name, body, written_by, created_at, updated_at)
		 VALUES ('m4', NULL, 'o', 'b', 'model', 0, 0)`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}
	_, err = db.Write.Exec(`INSERT INTO memories (id, cluster_id, name, body, written_by, created_at, updated_at)
		VALUES ('m3', NULL, 'n', 'b', 'user', 0, 0)`)
	require.Error(t, err, "a second global memory under one name")
	_, err = db.Write.Exec(`INSERT INTO memories (id, cluster_id, server_uid, name, body, written_by, created_at, updated_at)
		VALUES ('m5', NULL, 'uid', 'h', 'b', 'user', 0, 0)`)
	require.Error(t, err, "a global memory with a server UID")

	_, err = db.Write.Exec(`DELETE FROM chats WHERE id = 'c'`)
	require.NoError(t, err)
	var chatID *string
	require.NoError(t, db.Read.QueryRow(`SELECT chat_id FROM memories WHERE id = 'm1'`).Scan(&chatID))
	require.Nil(t, chatID)

	_, err = db.Write.Exec(`DELETE FROM clusters WHERE id = 'k'`)
	require.NoError(t, err)
	var ids []string
	rows, err := db.Read.Query(`SELECT id FROM memories`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"m2", "m4"}, ids)
	requireForeignKeysClean(t, db.Read)
}

func requireForeignKeysClean(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "foreign_key_check reported a violation")
}

// The CHECK constraints refuse what the app never writes: a status, trigger, mode
// or role outside its list, and a flag that is not 0 or 1.
func TestTheChecksRefuseValuesOutsideTheirLists(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('k', 'cloud', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', 'k', 'chat', 0, 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, provider, model, dialect, created_at) VALUES ('r', 'chat', 'dev', 'chat', 'c', 'p', 'm', 'fake', 0)`,
		`INSERT INTO llm_calls (id, run_id, seq, provider, model, started_at) VALUES ('l', 'r', 0, 'p', 'm', 0)`,
		`INSERT INTO tool_calls (id, llm_call_id, seq, tool_name, created_at) VALUES ('t', 'l', 0, 'list', 0)`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}
	for _, q := range []string{
		`INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('k2', 'file', 0, 0)`,
		`INSERT INTO clusters (id, source, enabled, created_at, updated_at) VALUES ('k2', 'cloud', 2, 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c2', 'k', 'monitor', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, created_at) VALUES ('m', 'c', 0, 'system', '[]', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, provider, model, dialect, created_at) VALUES ('r2', 'chat', 'dev', 'cron', 'c', 'p', 'm', 'fake', 0)`,
		`UPDATE agent_runs SET status = 'done' WHERE id = 'r'`,
		`UPDATE tool_calls SET status = 'ok' WHERE id = 't'`,
		`UPDATE tool_calls SET is_mutating = 2 WHERE id = 't'`,
		`INSERT INTO approvals (id, tool_call_id, status, created_at) VALUES ('a', 't', 'maybe', 0)`,
	} {
		_, err := db.Write.Exec(q)
		require.ErrorContains(t, err, "CHECK", q)
	}
	var status string
	require.NoError(t, db.Read.QueryRow(`SELECT status FROM tool_calls WHERE id = 't'`).Scan(&status))
	require.Equal(t, "pending", status, "the column's default")
}

// The clusters table's defaults: a row inserted with nothing but its identity and
// stamps is enabled, syncing, and not monitored; the source claim is unique.
func TestClustersTableDefaultsAndClaim(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const insert = `INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES (?, 'kubeconfig', 'dev', 0, 0)`
	_, err = db.Write.Exec(insert, NewID())
	require.NoError(t, err)
	_, err = db.Write.Exec(insert, NewID())
	require.ErrorContains(t, err, "UNIQUE")

	var enabled, syncEnabled, monitoringEnabled int
	var deleteRequestedAt *int64
	require.NoError(t, db.Read.QueryRow(`SELECT enabled, sync_enabled, monitoring_enabled, delete_requested_at FROM clusters`).
		Scan(&enabled, &syncEnabled, &monitoringEnabled, &deleteRequestedAt))
	require.Equal(t, []int{1, 1, 0}, []int{enabled, syncEnabled, monitoringEnabled})
	require.Nil(t, deleteRequestedAt)
}

// A data dir that cannot hold app.db fails the open — both when the parent path
// cannot be created and when the file itself cannot be an SQLite database.
func TestOpenFailsOnAnUnusablePath(t *testing.T) {
	dir := t.TempDir()

	notADir := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))
	_, err := Open(filepath.Join(notADir, "app.db"), 0)
	require.ErrorContains(t, err, "mkdir")

	// A directory where the database file belongs: the dir already exists, so the
	// failure comes from the pool's first connection.
	_, err = Open(dir, 0)
	require.Error(t, err)
}

// A subscriber hears the keys it asked for and nothing else, and a burst on one key
// arrives as one ping.
func TestNotifyReachesASubscriberAndConflates(t *testing.T) {
	h, _ := openTestDB(t)
	rx := h.Subscribe("a", "b")
	defer rx.Close()

	for range 3 {
		h.Notify("a")
	}
	h.Notify("other")
	h.Notify("b")

	ev, err := rx.RecvContext(t.Context())
	require.NoError(t, err)
	require.Equal(t, "a", ev.Key)
	ev, err = rx.RecvContext(t.Context())
	require.NoError(t, err)
	require.Equal(t, "b", ev.Key)

	// Nothing else is pending: "other" was not subscribed to and the burst on "a"
	// collapsed.
	_, err = rx.TryRecv()
	require.Error(t, err)
}

// Closing the DB ends every subscription, so a receiver parked on RecvContext
// returns rather than outliving the pools it would read.
func TestSubscriptionsEndOnClose(t *testing.T) {
	h, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	rx := h.Subscribe("a")

	done := make(chan error, 1)
	go func() {
		_, err := rx.RecvContext(context.Background())
		done <- err
	}()
	// Whether the receive parks before Close or finds the hub already closed, it
	// returns with the bus's error.
	require.NoError(t, h.Close())
	require.ErrorIs(t, testutil.Recv(t, done, "receiver woken by close"), gobus.ErrClosed)
	rx.Close()
}

// A subscription to no keys would wait forever: a caller error.
func TestSubscribeWithNoKeysPanics(t *testing.T) {
	h, _ := openTestDB(t)
	require.Panics(t, func() { h.Subscribe() })
}
