package appdb

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/amorey/gobus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate/sqlitemigratetest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitepool"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// Open creates a missing parent dir, runs the embedded migrations (recording
// the sequence in schema_migrations), and the file survives a close/reopen
// cycle.
func TestOpenCreatesMigratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "app.db")

	db, err := Open(path, 0)
	require.NoError(t, err)
	require.FileExists(t, path)

	// Migrations ran: the schema_migrations sequence is at version 6.
	var v int
	require.NoError(t, db.Write.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 6, v)
	requireTables(t, db.Write.DB)
	require.NoError(t, db.Close())

	// Reopening the same file is idempotent — migrations are not re-applied.
	db2, err := Open(path, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db2.Close() })
	require.NoError(t, db2.Write.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 6, v)
	requireTables(t, db2.Write.DB)
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

// requireTables asserts the schema the migrations create: exactly the
// application tables (schema_migrations is the migrator's), each STRICT,
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
		"chats wr=1 strict=1",
		"clusters wr=1 strict=1",
		"env_vars wr=0 strict=1",
		"executables wr=0 strict=1",
		"folder_grants wr=0 strict=1",
		"kube_rules wr=0 strict=1",
		"llm_calls wr=0 strict=1",
		"memories wr=0 strict=1",
		"messages wr=0 strict=1",
		"oauth_connections wr=1 strict=1",
		"settings wr=0 strict=1",
		"shell_path wr=0 strict=1",
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
		"CREATE UNIQUE INDEX env_vars_name ON env_vars (ifnull(cluster_id, ''), name)",
		"CREATE INDEX folder_grants_chat_idx ON folder_grants (chat_id, created_at, id)",
		"CREATE UNIQUE INDEX folder_grants_folder ON folder_grants (ifnull(chat_id, ''), folder)",
		"CREATE INDEX kube_rules_chat_idx ON kube_rules (chat_id, created_at, id)",
		"CREATE INDEX kube_rules_cluster_idx ON kube_rules (cluster_id)",
		"CREATE UNIQUE INDEX llm_calls_run_idx ON llm_calls (run_id, seq)",
		"CREATE UNIQUE INDEX memories_name ON memories (ifnull(cluster_id, ''), name)",
		"CREATE INDEX messages_run_idx ON messages (run_id)",
		"CREATE UNIQUE INDEX settings_scope_key ON settings (ifnull(cluster_id, ''), key)",
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

	require.Equal(t, []string{"run_id -> agent_runs.id CASCADE"}, foreignKeys(t, db.Read.DB, "llm_calls"))
	require.Equal(t, []string{"spawned_run_id -> agent_runs.id SET NULL", "llm_call_id -> llm_calls.id CASCADE"}, foreignKeys(t, db.Read.DB, "tool_calls"))
	require.Equal(t, []string{"tool_call_id -> tool_calls.id CASCADE"}, foreignKeys(t, db.Read.DB, "approvals"))
	require.Equal(t, []string{"tool_call_id -> tool_calls.id CASCADE", "chat_id -> chats.id CASCADE"},
		foreignKeys(t, db.Read.DB, "background_tasks"))

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
	requireForeignKeysClean(t, db.Read.DB)

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
	requireForeignKeysClean(t, db.Read.DB)
}

// A cluster's memories go with its row; a chat's delete leaves the memories it
// wrote and clears the pointer. A name is unique within a scope, the global scope
// included, though NULL never equals NULL.
func TestMemoriesHangOffTheirClusterAndOutliveTheirChat(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Equal(t, []string{"chat_id -> chats.id SET NULL", "cluster_id -> clusters.id CASCADE"},
		foreignKeys(t, db.Read.DB, "memories"))

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
	requireForeignKeysClean(t, db.Read.DB)
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

// A file a newer build migrated is refused with the runner's typed error, so a caller
// can name it to the user.
func TestOpenRefusesANewerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := Open(path, 0)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	sqlitemigratetest.StampNewer(t, path)

	_, err = Open(path, 0)

	require.ErrorAs(t, err, new(*sqlitemigrate.NewerSchemaError))
}

// shippedMigrations is every file in migrations/ by SHA-256. A new migration adds its
// line in the PR that writes it. Once a desktop release carries a migration, every
// installed app.db has run it, so its line never changes again.
var shippedMigrations = map[string]string{
	"0001_init.sql":                   "5bce01af11e58cea4359e82f58d5c5719c9077646fb71c0b4a3055e8a0489944", // desktop/v0.0.1
	"0002_oauth_connections.sql":      "a0f97ef3bdd26e882187424cf23f7a73b35747d09e85eef705f7f4d72fc03fea",
	"0003_oauth_connections_name.sql": "e734c075633be2f4a9dc8e43a460185a422d962116f7b7038ea5977773df1808",
	"0004_env_vars.sql":               "d1b78060708e5ea46b55f65d43136c935d6166eb35358b7d92c5bc72a1231d86",
	"0005_tool_settings.sql":          "286b5254057d5a29b9824f54136c3df82c039c06e94b014510b19a0ce9f9991c",
	"0006_settings.sql":               "69ae7e8982d08138f542b0c65babe469266fbc4b249efa6897f59f7e274967f7",
}

func TestAShippedMigrationIsNeverEdited(t *testing.T) {
	assert.Equal(t, shippedMigrations, sqlitemigratetest.Checksums(t, migrationsFS, "migrations"))
}

// A file 0001 built keeps its rows across 0002, and gains the connections table.
func TestMigration0002AddsTheOAuthConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	frozen, err := fs.ReadFile(migrationsFS, "migrations/0001_init.sql")
	require.NoError(t, err)
	pool, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	_, err = sqlitemigrate.Apply(ctx, pool.DB, fstest.MapFS{"migrations/0001_init.sql": {Data: frozen}}, "migrations")
	require.NoError(t, err)
	_, err = pool.ExecContext(ctx, `INSERT INTO clusters (id, source, created_at, updated_at) VALUES ('c', 'cloud', 0, 0)`)
	require.NoError(t, err)
	require.NoError(t, sqlitepool.CloseAll(sqlitepool.CloseBudget, pool))

	db := openThrough(t, path, "0002_oauth_connections.sql")

	var id string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM clusters`).Scan(&id))
	assert.Equal(t, "c", id)
	_, err = db.ExecContext(ctx, `INSERT INTO oauth_connections (provider, created_at, updated_at) VALUES ('chatgpt', 0, 0)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO oauth_connections (provider, created_at, updated_at) VALUES ('chatgpt', 0, 0)`)
	assert.Error(t, err, "one row per provider")
}

// A file 0002 built keeps its oauth_connections rows across 0003, and gains
// name.
func TestMigration0003AddsTheName(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	frozen := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_oauth_connections.sql"} {
		data, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		require.NoError(t, err)
		frozen["migrations/"+name] = &fstest.MapFile{Data: data}
	}
	pool, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	_, err = sqlitemigrate.Apply(ctx, pool.DB, frozen, "migrations")
	require.NoError(t, err)
	_, err = pool.ExecContext(ctx, `INSERT INTO oauth_connections (provider, client_id, subject, email, session_id, created_at, updated_at)
		VALUES ('chatgpt', 'c', 'user-1', 'ada@example.com', 's1', 0, 0)`)
	require.NoError(t, err)
	require.NoError(t, sqlitepool.CloseAll(sqlitepool.CloseBudget, pool))

	db := openThrough(t, path, "0003_oauth_connections_name.sql")

	var clientID, subject, email, session string
	var name *string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT client_id, subject, email, session_id, name FROM oauth_connections`).
		Scan(&clientID, &subject, &email, &session, &name))
	assert.Equal(t, []string{"c", "user-1", "ada@example.com", "s1"}, []string{clientID, subject, email, session})
	assert.Nil(t, name, "a row 0002 wrote has no name")
	_, err = db.ExecContext(ctx, `UPDATE oauth_connections SET name = 'Ada' WHERE provider = 'chatgpt'`)
	require.NoError(t, err)
}

// A file 0003 built keeps its clusters and tool calls across 0004, and gains the
// user's variables: one name per scope, a cluster's going with it.
func TestMigration0004AddsTheUsersVariables(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	frozen := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_oauth_connections.sql", "0003_oauth_connections_name.sql"} {
		data, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		require.NoError(t, err)
		frozen["migrations/"+name] = &fstest.MapFile{Data: data}
	}
	pool, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	_, err = sqlitemigrate.Apply(ctx, pool.DB, frozen, "migrations")
	require.NoError(t, err)
	for _, seed := range []string{
		`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('c1', 'kubeconfig', 'ctx', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('h1', 'c1', 'chat', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, request_key, created_at) VALUES ('m1', 'h1', 0, 'user', '[]', 'key', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at)
		 VALUES ('r1', 'chat', 'dev', 'chat', 'h1', 'm1', 'p', 'm', 'fake', 0)`,
		`INSERT INTO llm_calls (id, run_id, seq, provider, model, started_at) VALUES ('l1', 'r1', 0, 'p', 'm', 0)`,
		`INSERT INTO tool_calls (id, llm_call_id, seq, tool_name, status, cwd, created_at) VALUES ('t1', 'l1', 0, 'Bash', 'running', '/w', 0)`,
	} {
		_, err = pool.ExecContext(ctx, seed)
		require.NoError(t, err, seed)
	}
	require.NoError(t, sqlitepool.CloseAll(sqlitepool.CloseBudget, pool))

	db := openThrough(t, path, "0004_env_vars.sql")

	var cwd, env string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT cwd, env FROM tool_calls WHERE id = 't1'`).Scan(&cwd, &env))
	assert.Equal(t, []string{"/w", ""}, []string{cwd, env}, "a call 0003 wrote was shown no variables")

	insert := func(id string, cluster any, target string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO env_vars (id, cluster_id, name, value, target, created_at, updated_at)
			VALUES (?, ?, 'HELM_NAMESPACE', 'apps', ?, 0, 0)`, id, cluster, target)
		return err
	}
	require.NoError(t, insert("every", nil, "both"))
	assert.Error(t, insert("every-again", nil, "sandbox"), "a second row of one name for every cluster")
	require.NoError(t, insert("cluster", "c1", "outside"), "the name again, in a cluster")
	assert.Error(t, insert("nowhere", "c1", "chat"), "a target outside the list")

	_, err = db.ExecContext(ctx, `DELETE FROM clusters WHERE id = 'c1'`)
	require.NoError(t, err)
	var ids string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT group_concat(id) FROM env_vars`).Scan(&ids))
	assert.Equal(t, "every", ids, "the cluster's row goes with it")
}

// A subscriber hears the keys it asked for and nothing else, and a burst on one key
// arrives as one ping.
func TestNotifyReachesASubscriberAndConflates(t *testing.T) {
	h, _ := openTestDB(t)
	rx := h.ListenForChanges("a", "b")
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
	rx := h.ListenForChanges("a")

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
func TestListenForChangesWithNoKeysPanics(t *testing.T) {
	h, _ := openTestDB(t)
	require.Panics(t, func() { h.ListenForChanges() })
}

// 0005 adds the user's tool settings over a 0004 database, keeping its rows:
// one setting per tool per scope, a cluster's going with the cluster.
func TestMigration0005AddsTheToolSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	frozen := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_oauth_connections.sql", "0003_oauth_connections_name.sql", "0004_env_vars.sql"} {
		data, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		require.NoError(t, err)
		frozen["migrations/"+name] = &fstest.MapFile{Data: data}
	}
	pool, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	_, err = sqlitemigrate.Apply(ctx, pool.DB, frozen, "migrations")
	require.NoError(t, err)
	for _, seed := range []string{
		`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('c1', 'kubeconfig', 'ctx', 0, 0)`,
		`INSERT INTO env_vars (id, cluster_id, name, value, target, created_at, updated_at) VALUES ('v1', NULL, 'HELM_NAMESPACE', 'apps', 'both', 0, 0)`,
	} {
		_, err = pool.ExecContext(ctx, seed)
		require.NoError(t, err, seed)
	}
	require.NoError(t, sqlitepool.CloseAll(sqlitepool.CloseBudget, pool))

	db := openThrough(t, path, "0005_tool_settings.sql")

	var value string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM env_vars WHERE id = 'v1'`).Scan(&value))
	assert.Equal(t, "apps", value, "0004's row survives")

	insert := func(cluster any, enabled int) error {
		_, err := db.ExecContext(ctx, `INSERT INTO tool_settings (tool_name, cluster_id, enabled, updated_at)
			VALUES ('Bash', ?, ?, 0)`, cluster, enabled)
		return err
	}
	require.NoError(t, insert(nil, 0))
	assert.Error(t, insert(nil, 1), "a second setting of one tool for every cluster")
	require.NoError(t, insert("c1", 1), "the tool again, in a cluster")
	assert.Error(t, insert("c2", 0), "a cluster that does not exist")
	assert.Error(t, insert(nil, 2), "enabled is 0 or 1")

	_, err = db.ExecContext(ctx, `DELETE FROM clusters WHERE id = 'c1'`)
	require.NoError(t, err)
	var scopes string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT group_concat(ifnull(cluster_id, 'every')) FROM tool_settings`).Scan(&scopes))
	assert.Equal(t, "every", scopes, "the cluster's row goes with it")
}

// 0006 keeps every earlier table's rows, drops chat_grants and tool_settings
// without copying their rows, by decision, and adds the settings tables,
// empty.
func TestMigration0006AddsTheSettingsTables(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	pool, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	_, err = sqlitemigrate.Apply(ctx, pool.DB, frozenThrough(t, "0005_tool_settings.sql"), "migrations")
	require.NoError(t, err)
	for _, seed := range []string{
		`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('c1', 'kubeconfig', 'ctx', 1, 2)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('h1', 'c1', 'chat', 3, 4)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, created_at) VALUES ('u1', 'h1', 0, 'user', '[{"type":"text","text":"hi"}]', 5)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at) VALUES ('r1', 'chat', 'dev', 'chat', 'h1', 'u1', 'p', 'm', 'fake', 6)`,
		`INSERT INTO llm_calls (id, run_id, seq, provider, model, started_at) VALUES ('l1', 'r1', 0, 'p', 'm', 7)`,
		`INSERT INTO tool_calls (id, llm_call_id, seq, tool_name, created_at) VALUES ('t1', 'l1', 0, 'Bash', 8)`,
		`INSERT INTO approvals (id, tool_call_id, created_at) VALUES ('a1', 't1', 9)`,
		`INSERT INTO background_tasks (id, chat_id, tool_call_id, output_path, status, started_at) VALUES ('b1', 'h1', 't1', '/o', 'running', 10)`,
		`INSERT INTO memories (id, cluster_id, name, body, written_by, chat_id, created_at, updated_at) VALUES ('m1', 'c1', 'n', 'b', 'model', 'h1', 11, 12)`,
		`INSERT INTO oauth_connections (provider, created_at, updated_at) VALUES ('google', 13, 14)`,
		`INSERT INTO env_vars (id, cluster_id, name, value, target, created_at, updated_at) VALUES ('v1', NULL, 'HELM_NAMESPACE', 'apps', 'both', 15, 16)`,
	} {
		_, err = pool.ExecContext(ctx, seed)
		require.NoError(t, err, seed)
	}
	before := tableRows(t, pool.DB)
	require.Len(t, before, 11, "a row in every earlier table that stays")
	for _, seed := range []string{
		`INSERT INTO chat_grants (id, chat_id, rule, created_at) VALUES ('g1', 'h1', '{"id":"g1","effect":"allow","class":4}', 17)`,
		`INSERT INTO tool_settings (tool_name, cluster_id, enabled, updated_at) VALUES ('Bash', NULL, 0, 18)`,
	} {
		_, err = pool.ExecContext(ctx, seed)
		require.NoError(t, err, seed)
	}
	require.NoError(t, sqlitepool.CloseAll(sqlitepool.CloseBudget, pool))

	db := openThrough(t, path, "0006_settings.sql")

	assert.Equal(t, before, tableRows(t, db), "every other row survives as it was, and the new tables start empty")
	// count is how many rows of from there are.
	count := func(from string) int {
		t.Helper()
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM `+from).Scan(&n))
		return n
	}
	assert.Zero(t, count(`sqlite_schema WHERE name IN ('chat_grants', 'tool_settings')`), "chat_grants and tool_settings are gone")

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}

	setting := func(key string, cluster, value any) error {
		return exec(`INSERT INTO settings (key, cluster_id, value, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, key, cluster, value)
	}
	require.NoError(t, setting("tools.Bash.enabled", nil, 0), "a setting for every cluster")
	assert.Error(t, setting("tools.Bash.enabled", nil, 1), "a second row of one key for every cluster")
	require.NoError(t, setting("tools.Bash.enabled", "c1", 1), "the key again, in a cluster")
	assert.Error(t, setting("tools.Bash.enabled", "c1", 0), "a second row of one key in a cluster")
	assert.Error(t, setting("x.y", "c2", 1), "a cluster that does not exist")
	assert.Error(t, setting("", nil, 1), "an empty key")
	assert.Error(t, setting("x.real", nil, 1.5), "a real")
	assert.Error(t, setting("x.blob", nil, []byte{1}), "a blob")
	require.NoError(t, setting("x.null", nil, nil), "a NULL is the default, chosen")
	require.NoError(t, setting("x.int", nil, int64(7)))
	var kind string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT typeof(value) FROM settings WHERE key = 'x.int'`).Scan(&kind))
	assert.Equal(t, "integer", kind, "an integer stays an integer")

	rule := func(id string, chat any, effect, class string, cols map[string]any) error {
		q := `INSERT INTO kube_rules (id, chat_id, effect, class, cluster_id, namespace, verb, api_group, kind, inside, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`
		get := func(k string, def any) any {
			if v, ok := cols[k]; ok {
				return v
			}
			return def
		}
		return exec(q, id, chat, effect, class, get("cluster_id", nil), get("namespace", ""), get("verb", ""), get("api_group", ""), get("kind", ""), get("inside", 0))
	}
	require.NoError(t, rule("r1", nil, "allow", "upstream-write", map[string]any{"cluster_id": "c1"}), "an always rule")
	require.NoError(t, rule("r2", "h1", "deny", "destructive", nil), "a chat's rule")
	require.NoError(t, rule("r3", nil, "allow", "secret-read", map[string]any{"cluster_id": "c1", "verb": "get", "api_group": "core", "kind": "secrets"}), "a Secret read rule naming what a Secret read is")
	require.NoError(t, rule("r4", nil, "ask", "upstream-write", nil), "a rule for every cluster that restricts")
	for name, err := range map[string]error{
		"the empty id":              rule("", nil, "deny", "upstream-write", nil),
		"Refused's id":              rule("refused", nil, "deny", "upstream-write", nil),
		"RefusedSecrets' id":        rule("refused-secrets", nil, "deny", "secret-read", nil),
		"an unknown effect":         rule("x1", nil, "permit", "upstream-write", nil),
		"a folder class":            rule("x2", nil, "allow", "read-inside", nil),
		"an allow of destructive":   rule("x3", nil, "allow", "destructive", nil),
		"inside is 0 or 1":          rule("x4", nil, "deny", "upstream-write", map[string]any{"inside": 2}),
		"a Secret read inside":      rule("x5", nil, "deny", "secret-read", map[string]any{"inside": 1}),
		"a Secret read [cluster]":   rule("x6", nil, "deny", "secret-read", map[string]any{"namespace": "[cluster]"}),
		"a Secret read's verb":      rule("x7", nil, "deny", "secret-read", map[string]any{"verb": "delete"}),
		"a Secret read's group":     rule("x8", nil, "deny", "secret-read", map[string]any{"api_group": "apps"}),
		"a Secret read's resource":  rule("x9", nil, "deny", "secret-read", map[string]any{"kind": "configmaps"}),
		"a chat that is gone":       rule("x10", "h2", "deny", "upstream-write", nil),
		"an allow on every cluster": rule("x11", nil, "allow", "upstream-write", nil),
		"a cluster that is gone":    rule("x12", nil, "deny", "upstream-write", map[string]any{"cluster_id": "c2"}),
	} {
		assert.Error(t, err, name)
	}

	grant := func(id string, chat any, folder, access string) error {
		return exec(`INSERT INTO folder_grants (id, chat_id, folder, access, created_at) VALUES (?, ?, ?, ?, 0)`, id, chat, folder, access)
	}
	require.NoError(t, grant("f1", nil, "/srv/code", "read"), "an always grant")
	require.NoError(t, grant("f2", "h1", "/srv/code", "write"), "the folder again, in a chat")
	assert.Error(t, grant("f3", nil, "/srv/code", "write"), "a second always grant of one folder")
	assert.Error(t, grant("f4", "h1", "/srv/code", "read"), "a second grant of one folder in a chat")
	assert.Error(t, grant("f5", nil, "", "read"), "no folder")
	assert.Error(t, grant("f6", nil, "/srv/other", "execute"), "an access outside the two")
	assert.Error(t, grant("f7", "h2", "/srv/other", "read"), "a chat that is gone")
	require.NoError(t, exec(`INSERT INTO folder_grants (id, chat_id, folder, access, created_at) VALUES ('f8', NULL, '/srv/code', 'write', 1)
		ON CONFLICT (ifnull(chat_id, ''), folder) DO UPDATE SET access = excluded.access`))
	var id, access string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id, access FROM folder_grants WHERE chat_id IS NULL`).Scan(&id, &access))
	assert.Equal(t, []string{"f1", "write"}, []string{id, access}, "a grant again keeps its row")

	require.NoError(t, exec(`INSERT INTO executables (name, invocation, created_at) VALUES ('k9s', 'k9s version', 22)`))
	assert.Error(t, exec(`INSERT INTO executables (name, invocation, created_at) VALUES ('k9s', 'k9s info', 23)`), "a second row of one name")
	assert.Error(t, exec(`INSERT INTO executables (name, invocation, created_at) VALUES ('stern', NULL, 24)`), "an invocation is NOT NULL")
	assert.Error(t, exec(`INSERT INTO executables (name, invocation, created_at) VALUES ('stern', 'stern', 'now')`), "created_at is an INTEGER")

	require.NoError(t, exec(`INSERT INTO shell_path (id, path) VALUES (1, '/usr/bin:/bin')`))
	assert.Error(t, exec(`INSERT INTO shell_path (id, path) VALUES (2, '/bin')`), "one row at most")
	assert.Error(t, exec(`INSERT OR REPLACE INTO shell_path (id, path) VALUES (1, NULL)`), "a path is NOT NULL")

	require.NoError(t, exec(`DELETE FROM chats WHERE id = 'h1'`))
	assert.Zero(t, count(`kube_rules WHERE chat_id IS NOT NULL`), "a chat's rules go with it")
	assert.Zero(t, count(`folder_grants WHERE chat_id IS NOT NULL`), "a chat's folder grants go with it")
	require.NoError(t, exec(`DELETE FROM clusters WHERE id = 'c1'`))
	assert.Zero(t, count(`settings WHERE cluster_id IS NOT NULL`), "a cluster's settings go with it")
	assert.Zero(t, count(`kube_rules WHERE cluster_id IS NOT NULL`), "a cluster's rules go with it")
	assert.Equal(t, 1, count(`kube_rules`), "a rule for every cluster stays")
}

// tableRows is every row of every table in db but schema_migrations, keyed by
// table, each row its values in column order, the rows by their first column:
// some tables have no rowid.
func tableRows(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	var tables []string
	names, err := db.Query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`)
	require.NoError(t, err)
	for names.Next() {
		var name string
		require.NoError(t, names.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, names.Err())
	require.NoError(t, names.Close())

	out := map[string][][]any{}
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1`)
		require.NoError(t, err, table)
		cols, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			row := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range row {
				ptrs[i] = &row[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			out[table] = append(out[table], row)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	return out
}

// frozenThrough is every migration up to and including last, read from the
// embedded set.
func frozenThrough(t *testing.T, last string) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	require.NoError(t, err)
	frozen := fstest.MapFS{}
	for _, e := range entries {
		if e.Name() > last {
			break
		}
		data, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		require.NoError(t, err)
		frozen["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	require.Contains(t, frozen, "migrations/"+last)
	return frozen
}

// openThrough is a writer on the file at path, migrated through last and no
// further, for a test of a migration a later one changes. It closes when the
// test ends.
func openThrough(t *testing.T, path, last string) *sql.DB {
	t.Helper()
	pool, err := sqlitepool.OpenWriter(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlitepool.CloseAll(sqlitepool.CloseBudget, pool)) })
	_, err = sqlitemigrate.Apply(context.Background(), pool.DB, frozenThrough(t, last), "migrations")
	require.NoError(t, err)
	return pool.DB
}
