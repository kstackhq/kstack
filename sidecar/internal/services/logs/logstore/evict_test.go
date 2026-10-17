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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Evicting a chunk takes its lines and its row in one transaction and leaves the
// stream's other chunks and lines alone; a chunk linked to it then starts a span.
func TestEvictTakesAChunkAndItsLines(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	id := streamOf(t, s)
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: lines(at(1, 0), at(2, 0))},
		Fill{Stream: id, Origin: "kubelet", Lines: lines(at(3, 0)), Prev: ptr(at(2, 0))})
	chunks, err := s.Chunks(t.Context(), id, "")
	require.NoError(t, err)

	require.NoError(t, s.Evict(t.Context(), chunks[0].ID))

	assert.Equal(t, []Pos{at(3, 0)}, positions(t, s, id))
	assert.Equal(t, []Chunk{linked(chunk(at(3, 0), at(3, 0)), at(2, 0))}, chunksOfStream(t, s, id))
	assert.Equal(t, []Span{{at(3, 0), at(3, 0)}}, CoverageOf(chunksOfStream(t, s, id), kubelet).Spans)
}

// Evicting a chunk keeps the lines another chunk still claims: two fills of one
// range can overlap, and the survivor's lines must still be in the file.
func TestEvictKeepsTheLinesAnotherChunkClaims(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	id := streamOf(t, s)
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: lines(at(1, 0), at(2, 0))},
		Fill{Stream: id, Origin: "kubelet", Lines: lines(at(2, 0), at(3, 0))})
	chunks, err := s.Chunks(t.Context(), id, "")
	require.NoError(t, err)

	require.NoError(t, s.Evict(t.Context(), chunks[0].ID))

	assert.Equal(t, []Pos{at(2, 0), at(3, 0)}, positions(t, s, id))
}

// An eviction reads the chunk's bounds in its own transaction: a fill that extended
// the chunk since the caller read it is evicted with it, leaving no line unclaimed.
func TestEvictTakesTheChunkAsItIsNow(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	id := streamOf(t, s)
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: lines(at(1, 0), at(2, 0))})
	chunks, err := s.Chunks(t.Context(), id, "")
	require.NoError(t, err)
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: lines(at(3, 0)), Prev: ptr(at(2, 0)), Merge: true})

	require.NoError(t, s.Evict(t.Context(), chunks[0].ID))

	assert.Empty(t, positions(t, s, id))
	assert.Empty(t, chunksOfStream(t, s, id))
}

// Evicting a chunk the file no longer holds does nothing.
func TestEvictingAGoneChunkDoesNothing(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	id := streamOf(t, s)
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: lines(at(1, 0))})

	require.NoError(t, s.Evict(t.Context(), 99))

	assert.Equal(t, []Pos{at(1, 0)}, positions(t, s, id))
}

// An eviction whose delete the file refuses fails, leaving the chunk and its lines.
func TestAnEvictionTheFileRefusesFails(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	id := streamOf(t, s)
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: lines(at(1, 0))})
	chunks, err := s.Chunks(t.Context(), id, "")
	require.NoError(t, err)
	refuse(t, s, "DELETE", "lines")

	err = s.Evict(t.Context(), chunks[0].ID)

	assert.ErrorContains(t, err, "evict: ")
	assert.ErrorContains(t, err, "refused")
	assert.Equal(t, []Pos{at(1, 0)}, positions(t, s, id))
}
