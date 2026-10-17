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
	"context"
	"fmt"
	"time"
)

// Line is one line of a stream: its position and its message, cleaned.
type Line struct {
	Pos
	Message string
}

// Fill is one backend read of one gap of a stream, buffered for Write: the lines it
// returned, oldest first, all inside the gap, and what the read proved about the range
// around them.
type Fill struct {
	Stream int64
	// Pattern is the grep the backend read under, "" for every line, and Origin the
	// backend, by name.
	Pattern string
	Origin  string
	Lines   []Line
	// Prev is the last line of the chunk the gap starts at, when the read began there:
	// the backend holds nothing between it and the first line, so the first chunk
	// continues from it. Merge is the read returning Prev's moment whole, so the first
	// chunk extends the one ending at Prev.
	Prev  *Pos
	Merge bool
	// Next is the first line of the chunk the gap ends at, when the read served every
	// line up to it: that chunk then continues from the last line, or from Prev when the
	// read returned none.
	Next *Pos
	// Head is the read reaching the backend's oldest line, which marks the oldest chunk
	// it wrote, or Next's with none. AtEnd is it reaching a finished instance's last
	// line, which marks the newest chunk it wrote, or Prev's with none.
	Head  bool
	AtEnd bool
}

// Write files every fill in one transaction, so a reader sees a fill's lines and the
// chunks claiming them together or not at all. A line two fills found is stored once.
// A stream that has finished is trimmed after its fills.
func (s *Store) Write(ctx context.Context, fills []Fill) error {
	now := time.Now().UnixMilli()
	err := s.inTx(ctx, func(ctx context.Context, st stmts) error {
		for _, f := range fills {
			if err := writeFill(ctx, st, s.m.ret, f, now); err != nil {
				return err
			}
		}
		for _, id := range streamsOf(fills) {
			if err := trimFinished(ctx, st, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// writeFill files f's lines, the chunks claiming them, and the links and marks its
// read proved.
func writeFill(ctx context.Context, st stmts, ret Retention, f Fill, now int64) error {
	for _, l := range f.Lines {
		if _, err := st.Exec(ctx, stmtInsertLine, f.Stream, l.TS, l.Ord, l.Message); err != nil {
			return err
		}
	}
	last := f.Prev
	if len(f.Lines) > 0 {
		chunks, err := chunksFor(ctx, st, ret, f)
		if err != nil {
			return err
		}
		for _, c := range chunks {
			if err := putChunk(ctx, st, f.Stream, c, now); err != nil {
				return err
			}
		}
		last = &f.Lines[len(f.Lines)-1].Pos
	} else {
		if f.Head && f.Next != nil {
			if _, err := st.Exec(ctx, stmtMarkHead, f.Stream, f.Pattern, f.Origin, f.Next.TS, f.Next.Ord); err != nil {
				return err
			}
		}
		if f.AtEnd && f.Prev != nil {
			if _, err := st.Exec(ctx, stmtMarkAtEnd, f.Stream, f.Pattern, f.Origin, f.Prev.TS, f.Prev.Ord); err != nil {
				return err
			}
		}
	}
	if f.Next != nil && last != nil {
		_, err := st.Exec(ctx, stmtLinkChunk, f.Stream, f.Pattern, f.Next.TS, f.Next.Ord, last.TS, last.Ord)
		return err
	}
	return nil
}

// chunksFor is f's lines cut into chunks at ret's ChunkBytes and ChunkSpan, each linked
// to the one before it: the first continuing from Prev, and extending the chunk that ends
// there when f merges and that chunk has room.
func chunksFor(ctx context.Context, st stmts, ret Retention, f Fill) ([]Chunk, error) {
	cur := Chunk{Pattern: f.Pattern, Origin: f.Origin, From: f.Lines[0].Pos, Prev: f.Prev, Head: f.Head}
	fresh := true
	if f.Merge && f.Prev != nil {
		c, ok, err := chunkEndingAt(ctx, st, f.Stream, f.Pattern, f.Origin, *f.Prev)
		if err != nil {
			return nil, err
		}
		if ok && !full(ret, c, f.Lines[0].TS) {
			cur, fresh = c, false
		}
	}
	var out []Chunk
	for _, l := range f.Lines {
		if !fresh && full(ret, cur, l.TS) {
			out = append(out, cur)
			prev := cur.To
			cur = Chunk{Pattern: f.Pattern, Origin: f.Origin, From: l.Pos, Prev: &prev}
		}
		cur.To = l.Pos
		cur.Bytes += int64(len(l.Message))
		fresh = false
	}
	cur.AtEnd = f.AtEnd
	return append(out, cur), nil
}

// full is whether c is closed to a line at ts: it holds ChunkBytes, or ts is ChunkSpan
// past its first line.
func full(ret Retention, c Chunk, ts int64) bool {
	return c.Bytes >= ret.ChunkBytes || ts-c.From.TS >= int64(ret.ChunkSpan)
}

// chunkEndingAt is the stream's chunk of pattern and origin whose last line is at,
// false with none.
func chunkEndingAt(ctx context.Context, st stmts, stream int64, pattern, origin string, at Pos) (Chunk, bool, error) {
	chunks, err := queryChunks(ctx, st, stmtSelectChunkEndingAt, stream, pattern, origin, at.TS, at.Ord)
	if err != nil || len(chunks) == 0 {
		return Chunk{}, false, err
	}
	return chunks[0], true, nil
}

// putChunk writes c: over its row when it has one, else as a new row, merged into
// the chunk of its pattern that already starts at its first line.
func putChunk(ctx context.Context, st stmts, stream int64, c Chunk, now int64) error {
	var prevNS, prevOrd any
	if c.Prev != nil {
		prevNS, prevOrd = c.Prev.TS, c.Prev.Ord
	}
	if c.ID != 0 {
		_, err := st.Exec(ctx, stmtExtendChunk, c.ID, c.To.TS, c.To.Ord, c.AtEnd, c.Bytes, now)
		return err
	}
	_, err := st.Exec(ctx, stmtInsertChunk, stream, c.Pattern, c.From.TS, c.From.Ord, c.To.TS, c.To.Ord,
		prevNS, prevOrd, c.Origin, c.Head, c.AtEnd, c.Bytes, now)
	return err
}

// streamsOf is each stream fills write to, once.
func streamsOf(fills []Fill) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, f := range fills {
		if !seen[f.Stream] {
			seen[f.Stream] = true
			out = append(out, f.Stream)
		}
	}
	return out
}
