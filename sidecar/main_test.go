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

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
)

// runArgs parses a command line the way main does, so a run test still starts
// from arguments even though run itself takes a parsed config — over an empty
// environment, so nothing of the shell's reaches the sidecar it starts.
func runArgs(t *testing.T, args ...string) config {
	t.Helper()
	cfg, err := configFromArgs(args, noEnv.get)
	if err != nil {
		t.Fatalf("configFromArgs(%v): %v", args, err)
	}
	return cfg
}

// A config the composition root rejects exits 1. READY is announced only once
// the app is built, so the host never sees an endpoint that will not serve.
func TestRunFailsWhenTheAppCannotBeComposed(t *testing.T) {
	var out strings.Builder
	// No --data-dir: app.New rejects it.
	cfg := runArgs(t, "--socket", socketPath(t))
	if code := run(t.Context(), cfg, strings.NewReader(""), &out); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (READY follows a composed app)", out.String())
	}
}

// An endpoint that cannot be bound exits 1 before anything is printed.
func TestRunFailsWhenTheEndpointCannotBeBound(t *testing.T) {
	var out strings.Builder
	cfg := runArgs(t, "--socket", unbindableEndpoint(t), "--cache-dir", t.TempDir(), "--runtime-dir", t.TempDir(), "--data-dir", t.TempDir())
	code := run(t.Context(), cfg, strings.NewReader(""), &out)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (the READY line follows a successful bind)", out.String())
	}
}

// A shutdown signal that lands before run — during the shell import — exits 0
// without binding or announcing READY, like the stdin-EOF shutdown.
func TestRunExitsCleanlyWhenTheContextEndedBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out strings.Builder
	cfg := runArgs(t, "--socket", socketPath(t), "--cache-dir", t.TempDir(), "--runtime-dir", t.TempDir(), "--data-dir", t.TempDir())
	if code := run(ctx, cfg, strings.NewReader(""), &out); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (a run that never starts announces nothing)", out.String())
	}
}

// A failed start still closes the app, and app.db with it. The chat service's start
// fails the runs a previous process left unfinished, so a trigger refusing that
// update fails the start. SQLite deletes the -wal beside a database when its last
// connection closes, so its absence shows the file was released.
func TestRunClosesTheAppWhenStartFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, err := appdb.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('k', 'kubeconfig', 'dev', 0, 0)`,
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', 'k', 'chat', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, created_at) VALUES ('u', 'c', 0, 'user', '[]', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, created_at)
		 VALUES ('r', 'chat', 'dev', 'chat', 'c', 'u', 'p', 'm', 'fake', 0)`,
		`CREATE TRIGGER refuse_start BEFORE UPDATE ON agent_runs BEGIN SELECT RAISE(ABORT, 'refused by the test'); END`,
	} {
		if _, err := db.Write.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// stdin stays open: its EOF means the parent died, and a start cancelled that way
	// would fail on whichever part was starting rather than on the trigger.
	stdin, hold := io.Pipe()
	t.Cleanup(func() { _ = hold.Close() })
	var out strings.Builder
	cfg := runArgs(t, "--socket", socketPath(t), "--cache-dir", t.TempDir(), "--runtime-dir", t.TempDir(), "--data-dir", dir)
	if code := run(t.Context(), cfg, stdin, &out); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if _, err := os.Stat(path + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("app.db-wal after a failed start: stat = %v, want absent (the database was not closed)", err)
	}
}
