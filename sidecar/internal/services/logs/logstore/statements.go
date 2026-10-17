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
	"encoding/json"

	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"
)

// stmtID names one statement of the file's table.
type stmtID int

const (
	stmtInsertStream stmtID = iota
	stmtUpdateStream
	stmtSelectStreamState
	stmtSelectStream
	stmtDefaultUnsettled
	stmtMoveDefault
	stmtFinishStream
	stmtSelectPodStreams
	stmtSelectChunks
	stmtInsertLine
	stmtInsertChunk
	stmtMarkHead
	stmtMarkAtEnd
	stmtLinkChunk
	stmtSelectFinishedAt
	stmtTrimLines
	stmtTrimChunks
	stmtCutChunks
	stmtClearAtEnd
	stmtWalkForward
	stmtWalkBackward
	stmtEvictLines
	stmtSelectOverlapping
	stmtEvictChunk
	stmtSelectChunkBounds
	stmtSelectChunkEndingAt
	stmtCloseOpenChunks
	stmtCloseChunk
	stmtStampChunks
	stmtStreamBytes
	stmtCandidates
)

// statements is every statement the store runs, each on the pool its callers need: a
// read a write runs inside its transaction is OnBoth, since on the reader it would miss
// the transaction's own writes.
var statements = []sqlstmt.Statement{
	stmtInsertStream: sqlstmt.OnWriter(`
		INSERT INTO streams (
			namespace, pod, pod_uid, container, is_default, container_id,
			started_at, finished_at, node, region, zone, os, arch, labels
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
	// The place, the labels and whether the container is the pod's default follow the
	// mirror while the pod is there; a time once known is kept, since an instance
	// starts and stops once. A row already so writes nothing, so a page over streams
	// that did not change commits no write.
	stmtUpdateStream: sqlstmt.OnWriter(`
		UPDATE streams SET
			node = ?5, region = ?6, zone = ?7, os = ?8, arch = ?9, labels = ?10, is_default = ?13,
			started_at = coalesce(started_at, ?11),
			finished_at = coalesce(finished_at, ?12)
		WHERE namespace = ?1 AND pod = ?2 AND container = ?3 AND container_id = ?4
			AND ((node, region, zone, os, arch, labels, is_default) IS NOT (?5, ?6, ?7, ?8, ?9, ?10, ?13)
				OR started_at IS NULL AND ?11 IS NOT NULL
				OR finished_at IS NULL AND ?12 IS NOT NULL)`),
	// A time once known is kept, as stmtUpdateStream keeps it.
	stmtFinishStream: sqlstmt.OnWriter(`
		UPDATE streams SET finished_at = ?5
		WHERE namespace = ?1 AND pod = ?2 AND container = ?3 AND container_id = ?4 AND finished_at IS NULL
		RETURNING id`),
	// A pod has one default container, so filing one moves the mark onto every
	// instance of it and off every other container's.
	stmtMoveDefault: sqlstmt.OnWriter(`
		UPDATE streams SET is_default = (container = ?4)
		WHERE namespace = ?1 AND pod = ?2 AND pod_uid = ?3 AND is_default <> (container = ?4)`),
	stmtSelectStreamState: sqlstmt.OnBoth(`
		SELECT id, finished_at FROM streams
		WHERE namespace = ? AND pod = ? AND container = ? AND container_id = ?`),
	stmtSelectStream: sqlstmt.OnReader(`
		SELECT ` + streamColumns + ` FROM streams
		WHERE namespace = ? AND pod = ? AND container = ? AND container_id = ?`),
	// Whether stmtMoveDefault would move the mark.
	stmtDefaultUnsettled: sqlstmt.OnReader(`
		SELECT EXISTS (
			SELECT 1 FROM streams
			WHERE namespace = ?1 AND pod = ?2 AND pod_uid = ?3 AND is_default <> (container = ?4)
		)`),
	stmtSelectPodStreams: sqlstmt.OnReader(`
		SELECT ` + streamColumns + ` FROM streams
		WHERE namespace = ?1 AND pod = ?2 AND (?3 = '' OR pod_uid = ?3)
		ORDER BY container, started_at DESC, id DESC`),
	// A pattern is covered by its own chunks and the unlabelled ones, since every line
	// is a superset of the matches.
	stmtSelectChunks: sqlstmt.OnBoth(`
		SELECT ` + chunkColumns + ` FROM chunks
		WHERE stream_id = ?1 AND (pattern = ?2 OR pattern = '')
		ORDER BY from_ns, from_ord, id`),
	stmtInsertLine: sqlstmt.OnWriter(`
		INSERT OR IGNORE INTO lines (stream_id, ts_ns, ord, message) VALUES (?, ?, ?, ?)`),
	// Two fills of one gap can start at one line, as two pages with different limits
	// read it, and so can a fill and a tail, and a tail extends its own chunk through
	// the same insert: the row keeps the later end, with its at_end, either's head
	// mark and link, since every claim each made holds, and stays open while a tail
	// writes it. Neither touches read_at_ms, which Write and a page stamp apart.
	stmtInsertChunk: sqlstmt.OnWriter(`
		INSERT INTO chunks (
			stream_id, pattern, from_ns, from_ord, to_ns, to_ord, prev_ns, prev_ord,
			origin, at_head, at_end, open, bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (stream_id, pattern, from_ns, from_ord) DO UPDATE SET
			at_end = CASE
				WHEN (excluded.to_ns, excluded.to_ord) > (to_ns, to_ord) THEN excluded.at_end
				WHEN (excluded.to_ns, excluded.to_ord) = (to_ns, to_ord) THEN max(at_end, excluded.at_end)
				ELSE at_end END,
			to_ord = CASE WHEN (excluded.to_ns, excluded.to_ord) > (to_ns, to_ord) THEN excluded.to_ord ELSE to_ord END,
			to_ns = max(to_ns, excluded.to_ns),
			bytes = max(bytes, excluded.bytes),
			at_head = max(at_head, excluded.at_head),
			prev_ns = coalesce(prev_ns, excluded.prev_ns),
			prev_ord = coalesce(prev_ord, excluded.prev_ord),
			open = max(open, excluded.open)
		RETURNING id, to_ns, to_ord, at_end, open, bytes`),
	// A fill's proof holds for its own pattern alone, so it links and marks no other.
	stmtMarkHead: sqlstmt.OnWriter(`
		UPDATE chunks SET at_head = 1
		WHERE stream_id = ?1 AND pattern = ?2 AND origin = ?3 AND from_ns = ?4 AND from_ord = ?5`),
	stmtMarkAtEnd: sqlstmt.OnWriter(`
		UPDATE chunks SET at_end = 1
		WHERE stream_id = ?1 AND pattern = ?2 AND origin = ?3 AND to_ns = ?4 AND to_ord = ?5`),
	stmtLinkChunk: sqlstmt.OnWriter(`
		UPDATE chunks SET prev_ns = ?5, prev_ord = ?6
		WHERE stream_id = ?1 AND pattern = ?2 AND from_ns = ?3 AND from_ord = ?4`),
	stmtSelectFinishedAt: sqlstmt.OnBoth(`SELECT finished_at FROM streams WHERE id = ?`),
	stmtTrimLines:        sqlstmt.OnWriter(`DELETE FROM lines WHERE stream_id = ?1 AND ts_ns > ?2`),
	stmtTrimChunks:       sqlstmt.OnWriter(`DELETE FROM chunks WHERE stream_id = ?1 AND from_ns > ?2`),
	// A chunk that starts before the cut keeps its first line, so a line is left to end
	// it at.
	stmtCutChunks: sqlstmt.OnWriter(`
		UPDATE chunks SET (to_ns, to_ord) = (
			SELECT ts_ns, ord FROM lines l
			WHERE l.stream_id = chunks.stream_id AND l.ts_ns <= ?2
				AND (l.ts_ns, l.ord) >= (chunks.from_ns, chunks.from_ord)
			ORDER BY ts_ns DESC, ord DESC LIMIT 1
		)
		WHERE stream_id = ?1 AND to_ns > ?2`),
	stmtClearAtEnd: sqlstmt.OnWriter(`UPDATE chunks SET at_end = 0 WHERE stream_id = ?`),
	// One walk of lines_position each way, which the index answers in order.
	stmtWalkForward: sqlstmt.OnReader(`
		SELECT ts_ns, ord, message FROM lines
		WHERE stream_id = ?1 AND (ts_ns, ord) > (?2, ?3) AND (ts_ns, ord) <= (?4, ?5)
		ORDER BY ts_ns, ord`),
	stmtWalkBackward: sqlstmt.OnReader(`
		SELECT ts_ns, ord, message FROM lines
		WHERE stream_id = ?1 AND (ts_ns, ord) < (?2, ?3) AND (ts_ns, ord) >= (?4, ?5)
		ORDER BY ts_ns DESC, ord DESC`),
	// One range of the position index, which the caller cuts around the lines other
	// chunks claim.
	stmtEvictLines: sqlstmt.OnWriter(`
		DELETE FROM lines WHERE stream_id = ?1 AND (ts_ns, ord) >= (?2, ?3) AND (ts_ns, ord) <= (?4, ?5)`),
	// Fills of one range can overlap: each chunk sharing a line with ?6's range.
	stmtSelectOverlapping: sqlstmt.OnBoth(`
		SELECT from_ns, from_ord, to_ns, to_ord FROM chunks
		WHERE stream_id = ?1 AND id <> ?6 AND (to_ns, to_ord) >= (?2, ?3) AND (from_ns, from_ord) <= (?4, ?5)
		ORDER BY from_ns, from_ord`),
	stmtEvictChunk: sqlstmt.OnWriter(`DELETE FROM chunks WHERE id = ?`),
	// An open chunk is never evicted: its tail writes it back from what it holds of it,
	// and one evicted under the tail would come back claiming the evicted lines. Nor is
	// one read since the cutoff.
	stmtSelectChunkBounds: sqlstmt.OnBoth(`
		SELECT stream_id, from_ns, from_ord, to_ns, to_ord FROM chunks
		WHERE id = ?1 AND open = 0 AND (read_at_ms IS NULL OR read_at_ms < ?2)`),
	// An open chunk is its tailer's alone to extend.
	stmtSelectChunkEndingAt: sqlstmt.OnBoth(`
		SELECT ` + chunkColumns + ` FROM chunks
		WHERE stream_id = ? AND pattern = ? AND origin = ? AND to_ns = ? AND to_ord = ? AND open = 0`),
	stmtCloseOpenChunks: sqlstmt.OnWriter(`UPDATE chunks SET open = 0 WHERE open = 1`),
	stmtCloseChunk:      sqlstmt.OnWriter(`UPDATE chunks SET open = 0 WHERE id = ?`),
	stmtStreamBytes:     sqlstmt.OnReader(`SELECT stream_id, sum(bytes) FROM chunks GROUP BY stream_id`),
	// A never-read chunk goes before a read one: the oldest line first among the
	// never-read, the oldest stamp first among the read.
	stmtCandidates: sqlstmt.OnReader(`
		SELECT id, stream_id, bytes FROM chunks
		WHERE open = 0 AND (read_at_ms IS NULL OR read_at_ms < ?1)
		ORDER BY read_at_ms IS NOT NULL, read_at_ms, from_ns, from_ord, id`),
	// A chunk stamped within the interval keeps its stamp, so a page re-reading it
	// writes nothing.
	stmtStampChunks: sqlstmt.OnWriter(`
		UPDATE chunks SET read_at_ms = ?1
		WHERE id IN (SELECT value FROM json_each(?2)) AND (read_at_ms IS NULL OR read_at_ms <= ?1 - ?3)`),
}

// idList is ids as the one JSON argument a statement reads through json_each.
func idList(ids []int64) string {
	arg, _ := json.Marshal(ids) // a list of integers always marshals
	return string(arg)
}

// chunkColumns is a chunk row as chunksOf reads it.
const chunkColumns = `
	id, pattern, from_ns, from_ord, to_ns, to_ord, prev_ns, prev_ord, origin, at_head, at_end, open, bytes,
	coalesce(read_at_ms, 0)`

// streamColumns is a stream row as scanStream reads it.
const streamColumns = `
	id, namespace, pod, pod_uid, container, is_default, container_id,
	started_at, finished_at, node, region, zone, os, arch, labels`
