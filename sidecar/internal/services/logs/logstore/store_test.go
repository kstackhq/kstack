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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestFile opens a fresh file under a temp directory, closed when the test ends.
func openTestFile(t *testing.T) *file {
	t.Helper()
	f, err := openFile(filepath.Join(t.TempDir(), "1.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.close()) })
	return f
}

// insertStream adds one stream and answers its id.
func insertStream(t *testing.T, f *file) int64 {
	t.Helper()
	res, err := f.db.Exec(`INSERT INTO streams (namespace, pod, container, container_id, node, region, zone, os, arch, labels)
		VALUES ('web', 'api-7f9c', 'app', 'containerd://1', 'node-a', '', '', 'linux', 'amd64', '{}')`)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// insertLine adds one line at a position and answers its id.
func insertLine(t *testing.T, f *file, stream, tsNS, ord int64) int64 {
	t.Helper()
	res, err := f.db.Exec(`INSERT INTO lines (stream_id, ts_ns, ord, message) VALUES (?, ?, ?, 'hello')`, stream, tsNS, ord)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// countStreams reads how many streams the file holds, through the reader pool.
func countStreams(t *testing.T, f *file) int {
	t.Helper()
	var n int
	require.NoError(t, f.readDB.QueryRow(`SELECT count(*) FROM streams`).Scan(&n))
	return n
}

// The directory need not exist, and what a file held is there when it is opened again.
func TestAFileReopensWithWhatItHeld(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "logs", "1.db")
	f, err := openFile(path)
	require.NoError(t, err)
	insertStream(t, f)
	require.NoError(t, f.close())

	f, err = openFile(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.close()) })

	assert.Equal(t, 1, countStreams(t, f))
}

// STRICT is what keeps a moment a number: a loose column would store the text and sort it
// after every integer, putting the line out of position.
func TestAMomentMustBeAnInteger(t *testing.T) {
	t.Parallel()
	f := openTestFile(t)
	stream := insertStream(t, f)

	_, err := f.db.Exec(`INSERT INTO chunks (stream_id, pattern, from_ns, from_ord, to_ns, to_ord, origin, head, at_end, open, bytes, read_at_ms)
		VALUES (?, '', 'yesterday', 0, 2, 0, 'kubelet', 0, 0, 0, 0, 0)`, stream)

	assert.ErrorContains(t, err, "cannot store TEXT value in INTEGER column")
}

// A line's key is its position, so two fills that found the same line store it once: the
// second insert is refused, and the fill's OR IGNORE skips it.
func TestAPositionHoldsOneLine(t *testing.T) {
	t.Parallel()
	f := openTestFile(t)
	stream := insertStream(t, f)
	insertLine(t, f, stream, 100, 0)

	_, err := f.db.Exec(`INSERT INTO lines (stream_id, ts_ns, ord, message) VALUES (?, 100, 0, 'again')`, stream)
	assert.ErrorContains(t, err, "UNIQUE constraint failed")

	res, err := f.db.Exec(`INSERT OR IGNORE INTO lines (stream_id, ts_ns, ord, message) VALUES (?, 100, 0, 'again')`, stream)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	assert.Zero(t, n)
}

// A follow reads by id past the last one it saw, so an id handed out twice would put a
// new tail line below a live follow's mark and the follow would never send it. Deleting
// the newest row is when an implicit rowid would be handed out again.
func TestALineIDIsNeverReused(t *testing.T) {
	t.Parallel()
	f := openTestFile(t)
	stream := insertStream(t, f)
	insertLine(t, f, stream, 100, 0)
	newest := insertLine(t, f, stream, 200, 0)
	_, err := f.db.Exec(`DELETE FROM lines WHERE id = ?`, newest)
	require.NoError(t, err)

	next := insertLine(t, f, stream, 300, 0)

	assert.Greater(t, next, newest)
}
