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

// at is the position of the ord-th line at s seconds past 10:00.
func at(s int64, ord int) Pos { return Pos{TS: second(s), Ord: ord} }

// ptr is p's address.
func ptr(p Pos) *Pos { return &p }

// chunk is a kubelet chunk from from to to.
func chunk(from, to Pos) Chunk { return Chunk{From: from, To: to, Origin: "kubelet"} }

// linked is c continuing from prev.
func linked(c Chunk, prev Pos) Chunk {
	c.Prev = &prev
	return c
}

// kubelet is the one backend of a cluster read through its API server.
var kubelet = []string{"kubelet"}

// Chunks that overlap are one span, and so are two the later one's link joins; a
// chunk with no link opens on a gap.
func TestCoverageJoinsOverlappingAndLinkedChunks(t *testing.T) {
	t.Parallel()
	cov := CoverageOf([]Chunk{
		chunk(at(1, 0), at(3, 0)),
		chunk(at(2, 0), at(5, 0)),
		linked(chunk(at(7, 0), at(8, 1)), at(5, 0)),
		chunk(at(10, 0), at(11, 0)),
	}, kubelet)

	assert.Equal(t, []Span{{at(1, 0), at(8, 1)}, {at(10, 0), at(11, 0)}}, cov.Spans)
	assert.Equal(t, []Gap{
		{Before: ptr(at(1, 0))},
		{After: ptr(at(8, 1)), Before: ptr(at(10, 0))},
		{After: ptr(at(11, 0))},
	}, cov.Gaps)
}

// A link that names no chunk the file holds starts a span: the chunk it named is
// gone, and its lines with it.
func TestAChunkWhoseLinkNamesAGoneChunkStartsASpan(t *testing.T) {
	t.Parallel()
	cov := CoverageOf([]Chunk{
		chunk(at(1, 0), at(3, 0)),
		linked(chunk(at(7, 0), at(8, 0)), at(5, 0)),
	}, kubelet)

	assert.Equal(t, []Span{{at(1, 0), at(3, 0)}, {at(7, 0), at(8, 0)}}, cov.Spans)
}

// A stream with no chunk is one gap, the whole log.
func TestAStreamWithNoChunkIsOneGap(t *testing.T) {
	t.Parallel()
	cov := CoverageOf(nil, kubelet)

	assert.Empty(t, cov.Spans)
	assert.Equal(t, []Gap{{}}, cov.Gaps)
}

// head is c marked head.
func head(c Chunk) Chunk {
	c.Head = true
	return c
}

// A gap older than a head chunk of the last backend is lost, the range before the
// oldest chunk included, and the gaps newer than it are live: a Tail page that
// reached the kubelet's oldest line at 10:30 over an older chunk from 9:55 crosses
// to it.
func TestAGapOlderThanTheLastBackendsHeadIsLost(t *testing.T) {
	t.Parallel()
	cov := CoverageOf([]Chunk{
		chunk(at(-300, 0), at(300, 0)),
		head(chunk(at(1800, 0), at(1900, 0))),
		chunk(at(2000, 0), at(2100, 0)),
	}, kubelet)

	var lost []bool
	for _, g := range cov.Gaps {
		lost = append(lost, g.Lost)
	}
	assert.Equal(t, []bool{true, true, false, false}, lost)
}

// A gap older than a head of an earlier backend is offered to the next, and lost
// only past the last.
func TestAGapOlderThanAnEarlierBackendsHeadGoesToTheNext(t *testing.T) {
	t.Parallel()
	archive := chunk(at(0, 0), at(10, 0))
	archive.Origin = "archive"
	chunks := []Chunk{archive, head(chunk(at(20, 0), at(30, 0)))}

	cov := CoverageOf(chunks, []string{"kubelet", "archive"})
	assert.Equal(t, []int{1, 1, 0}, tiers(cov), "the kubelet holds nothing before 20")
	assert.False(t, cov.Gaps[0].Lost)

	chunks[0] = head(archive)
	cov = CoverageOf(chunks, []string{"kubelet", "archive"})
	assert.Equal(t, []int{2, 1, 0}, tiers(cov))
	assert.True(t, cov.Gaps[0].Lost)
}

// tiers is each gap's tier.
func tiers(cov Coverage) []int {
	var out []int
	for _, g := range cov.Gaps {
		out = append(out, g.Tier)
	}
	return out
}

// The range newer than an at_end chunk is lost, since a finished instance writes no
// more, and every gap is lost in a stream no backend serves.
func TestTheNewestGapIsLostPastAtEnd(t *testing.T) {
	t.Parallel()
	end := chunk(at(0, 0), at(10, 0))
	end.AtEnd = true

	assert.True(t, CoverageOf([]Chunk{end}, kubelet).Gaps[1].Lost)
	assert.False(t, CoverageOf([]Chunk{chunk(at(0, 0), at(10, 0))}, kubelet).Gaps[1].Lost)
	for _, g := range CoverageOf([]Chunk{chunk(at(0, 0), at(10, 0)), chunk(at(20, 0), at(30, 0))}, nil).Gaps {
		assert.True(t, g.Lost)
	}
}

// insertChunk adds c to the stream id in the file behind s, and answers it with its
// row's id.
func insertChunk(t *testing.T, s *Store, id int64, c Chunk) Chunk {
	t.Helper()
	using(t, s, func(f *file) {
		var prevNS, prevOrd any
		if c.Prev != nil {
			prevNS, prevOrd = c.Prev.TS, c.Prev.Ord
		}
		res, err := f.db.Exec(`INSERT INTO chunks (stream_id, pattern, from_ns, from_ord, to_ns, to_ord, prev_ns, prev_ord, origin, at_head, at_end, open, bytes, read_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0)`, id, c.Pattern, c.From.TS, c.From.Ord, c.To.TS, c.To.Ord, prevNS, prevOrd, c.Origin, c.Head, c.AtEnd, c.Bytes)
		require.NoError(t, err)
		c.ID, err = res.LastInsertId()
		require.NoError(t, err)
	})
	return c
}

// labelled is c filled under pattern.
func labelled(c Chunk, pattern string) Chunk {
	c.Pattern = pattern
	return c
}

// A page for pattern P is covered by the chunks labelled P and the unlabelled ones,
// and treats a chunk labelled Q as a gap; an unfiltered page reads the unlabelled
// ones alone.
func TestAPatternReadsItsOwnAndUnlabelledChunks(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ids, err := s.PutStreams(t.Context(), []Stream{app()})
	require.NoError(t, err)
	plain := insertChunk(t, s, ids[0], chunk(at(0, 0), at(1, 0)))
	p := insertChunk(t, s, ids[0], labelled(chunk(at(2, 0), at(3, 0)), "P"))
	insertChunk(t, s, ids[0], labelled(chunk(at(4, 0), at(5, 0)), "Q"))

	got, err := s.Chunks(t.Context(), ids[0], "P")
	require.NoError(t, err)
	assert.Equal(t, []Chunk{plain, p}, got)
	got, err = s.Chunks(t.Context(), ids[0], "")
	require.NoError(t, err)
	assert.Equal(t, []Chunk{plain}, got)
}
