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

//go:build !windows

package credentials

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// fakeTool writes a shell script to a directory of its own and answers its path.
func fakeTool(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755))
	return path
}

func newExecStore(t *testing.T, b Binaries, home string) *Store {
	t.Helper()
	t.Cleanup(safe.ResetSecrets)
	s := NewStore(b, home, nil, nil)
	t.Cleanup(s.Close)
	return s
}

func TestAToolRunsInTheHome(t *testing.T) {
	record := filepath.Join(t.TempDir(), "cwd")
	gh := fakeTool(t, "gh", "pwd > '"+record+"'\necho gho_token0123456789abcdef\n")
	home := t.TempDir()
	t.Chdir(t.TempDir())
	s := newExecStore(t, Binaries{GH: gh}, home)

	token, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	assert.Equal(t, "gho_token0123456789abcdef", token)

	got, err := os.ReadFile(record)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(got)))
	require.NoError(t, err)
	assert.Equal(t, want, gotDir)
}

func TestAFailedBorrowNamesTheProviderAndNeverTheOutput(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	gcloud := fakeTool(t, "gcloud", "echo printed-on-stdout\necho printed-on-stderr >&2\nexit 3\n")
	s := newExecStore(t, Binaries{Gcloud: gcloud}, t.TempDir())

	_, err := s.Google(t.Context())
	require.Error(t, err)
	assert.Equal(t, "credentials: gcloud exited 3", err.Error())

	assert.Contains(t, logs.String(), "gcloud")
	assert.Contains(t, logs.String(), `"exit":3`)
	assert.NotContains(t, logs.String(), "printed-on")
}

func TestCloseStopsARun(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "started")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	gh := fakeTool(t, "gh", "echo started > '"+fifo+"'\nexec sleep 300\n")
	s := newExecStore(t, Binaries{GH: gh}, t.TempDir())

	done := make(chan error, 1)
	go func() {
		_, err := s.GitHub(t.Context(), "github.com")
		done <- err
	}()
	f, err := os.Open(fifo)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	_, err = bufio.NewReader(f).ReadString('\n')
	require.NoError(t, err, "the tool started")

	s.Close()
	assert.Error(t, testutil.Recv(t, done, "the borrow the close stopped"))
	_, err = s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrClosed)
}

// A helper the tool started, like AWS's credential_process, goes with it: its
// write end of alive is the last, so the read ends only when the helper dies.
func TestCloseStopsWhatTheToolStarted(t *testing.T) {
	alive := filepath.Join(t.TempDir(), "alive")
	require.NoError(t, syscall.Mkfifo(alive, 0o600))
	gh := fakeTool(t, "gh", "sleep 300 > '"+alive+"' &\nwait\n")
	s := newExecStore(t, Binaries{GH: gh}, t.TempDir())

	done := make(chan error, 1)
	go func() {
		_, err := s.GitHub(t.Context(), "github.com")
		done <- err
	}()
	f, err := os.Open(alive) // returns once the helper has opened its end
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })

	s.Close()
	assert.Error(t, testutil.Recv(t, done, "the borrow the close stopped"))
	gone := make(chan error, 1)
	go func() {
		_, err := f.Read(make([]byte, 1))
		gone <- err
	}()
	assert.ErrorIs(t, testutil.Recv(t, gone, "the helper's end closing"), io.EOF)
}

// A helper that outlives the tool, holding its output open, goes once the tool is
// reaped, and the borrow answers what the tool printed. The tool waits on proceed
// until the helper has opened alive, or its exit could kill the helper first.
func TestAHelperLeftBehindIsKilled(t *testing.T) {
	alive := filepath.Join(t.TempDir(), "alive")
	require.NoError(t, syscall.Mkfifo(alive, 0o600))
	proceed := filepath.Join(t.TempDir(), "proceed")
	require.NoError(t, syscall.Mkfifo(proceed, 0o600))
	gh := fakeTool(t, "gh", "sleep 300 3> '"+alive+"' &\nread _ < '"+proceed+"'\necho gho_token0123456789abcdef\n")
	t.Cleanup(safe.ResetSecrets)
	s := newStoreWithOptions(Binaries{GH: gh}, t.TempDir(), nil, nil, withRunner(execRun(t.TempDir(), time.Millisecond)))
	t.Cleanup(s.Close)

	done := make(chan error, 1)
	go func() {
		_, err := s.GitHub(t.Context(), "github.com")
		done <- err
	}()
	f, err := os.Open(alive) // returns once the helper has opened its end
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	require.NoError(t, os.WriteFile(proceed, []byte("\n"), 0o600))

	require.NoError(t, testutil.Recv(t, done, "the borrow"))
	gone := make(chan error, 1)
	go func() {
		_, err := f.Read(make([]byte, 1))
		gone <- err
	}()
	assert.ErrorIs(t, testutil.Recv(t, gone, "the helper's end closing"), io.EOF)
}

func TestAToolThatCannotStartIsAnError(t *testing.T) {
	s := newExecStore(t, Binaries{GH: filepath.Join(t.TempDir(), "gh")}, t.TempDir())
	_, err := s.GitHub(t.Context(), "github.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials: gh did not finish")
}
