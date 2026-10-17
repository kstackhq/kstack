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

import "time"

// mib is one mebibyte, the unit the byte knobs are set in.
const mib = 1 << 20

// defaultBytes is the file's default budget; StreamShare is a quarter of it.
const defaultBytes = 256 * mib

// Retention is every bound on what a log file holds and on how lines reach it. The
// janitor sweeps to the first five, the tailers and the writer run under the rest.
type Retention struct {
	// Bytes is the file's budget, the chunks' bytes summed. Eviction runs while the file
	// is over it.
	Bytes int64
	// StreamShare is one stream's share of Bytes, so a firehose cannot take the file.
	StreamShare int64
	// ReadTTL keeps a chunk read or written this recently out of eviction.
	ReadTTL time.Duration
	// ChunkBytes and ChunkSpan close a chunk, whichever it reaches first; the next line
	// opens the next one. A chunk is the unit of eviction.
	ChunkBytes int64
	ChunkSpan  time.Duration
	// TailGrace keeps a tailer reading this long after its last follow lets go, so a
	// viewer that scrolls up and back attaches rather than restarts.
	TailGrace time.Duration
	// WriteBatch and WriteBatchBytes cap one batch a tailer hands the writer, whichever it
	// reaches first.
	WriteBatch      int
	WriteBatchBytes int64
	// WriteTick is how long a tailer holds a batch before sending it, and how long it may
	// wait for the writer to take one.
	WriteTick time.Duration
	// WriteQueue is how many batches may wait for the writer. The queue holds at most
	// WriteQueue times WriteBatchBytes of text, 16 MiB by default: log text is the
	// cluster's to write, so the bound is on bytes, never on lines.
	WriteQueue int
	// VacuumPages bounds what one sweep hands back to the filesystem.
	VacuumPages int64
	// Interval is the janitor's cadence. Zero runs no janitor, which is what a test about
	// anything else opens its manager with.
	Interval time.Duration
}

// DefaultRetention is the production setting.
var DefaultRetention = Retention{
	Bytes:           defaultBytes,
	StreamShare:     defaultBytes / 4,
	ReadTTL:         10 * time.Minute,
	ChunkBytes:      mib,
	ChunkSpan:       5 * time.Minute,
	TailGrace:       30 * time.Second,
	WriteBatch:      1000,
	WriteBatchBytes: mib,
	WriteTick:       50 * time.Millisecond,
	WriteQueue:      16,
	VacuumPages:     2048,
	Interval:        5 * time.Minute,
}
