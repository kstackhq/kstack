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

package logs

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// A gap a tailer's start cuts in two keeps its place among the spans.
func TestSegmentsKeepOrderAcrossACutGap(t *testing.T) {
	t.Parallel()
	pos := func(s int64) logstore.Pos { return logstore.Pos{TS: s} }
	start := pos(10)
	chunks := []logstore.Chunk{{From: pos(0), To: pos(5)}, {From: pos(20), To: pos(25)}}
	var edges []string
	for _, seg := range segmentsFrom(logstore.CoverageOf(chunks, []string{"kubelet"}, &start), logstore.Oldest, newer) {
		if seg.span != nil {
			edges = append(edges, fmt.Sprintf("span %d", seg.span.From.TS))
		} else {
			edges = append(edges, fmt.Sprintf("gap tailed=%v", seg.gap.Tailed))
		}
	}
	assert.Equal(t, []string{"gap tailed=false", "span 0", "gap tailed=false", "gap tailed=true", "span 20", "gap tailed=true"}, edges)
}

// A backward read leaves a hole below its oldest line only when it continued a
// chunk: never with no chunk to continue, and from a chunk, when a whole read found
// nothing or its oldest line lies past the chunk's second.
func TestAHoleIsJudgedByWhatTheReadContinued(t *testing.T) {
	t.Parallel()
	prev := logstore.Pos{TS: at(time.Second).UnixNano()}
	oldest := logstore.Pos{TS: at(3 * time.Second).UnixNano()}
	for name, c := range map[string]struct {
		oldest *logstore.Pos
		whole  bool
		prev   *logstore.Pos
		want   bool
	}{
		"no chunk":           {oldest: &oldest, whole: true, want: false},
		"nothing read whole": {whole: true, prev: &prev, want: true},
		"nothing read short": {prev: &prev, want: false},
		"whole":              {oldest: &oldest, whole: true, prev: &prev, want: true},
		"short":              {oldest: &oldest, prev: &prev, want: false},
		"down to prev":       {oldest: &logstore.Pos{TS: prev.TS, Ord: 1}, whole: true, prev: &prev, want: false},
	} {
		assert.Equal(t, c.want, hole(c.oldest, c.whole, c.prev), name)
	}
}

// A forward read is head when it has no lower bound, or when it read from prev's
// second, returned no line at or before prev, and got a line or ended clean. A read
// from any other bound, as from a page's key, says nothing of rotation.
func TestAHeadReadIsJudgedByPrev(t *testing.T) {
	t.Parallel()
	prev := &logstore.Pos{TS: at(time.Second).UnixNano()}
	key := &logstore.Pos{TS: at(2 * time.Second).UnixNano()}
	for name, c := range map[string]struct {
		lower, prev *logstore.Pos
		began       time.Time
		proved      bool
		want        bool
	}{
		"no lower bound":      {began: at(3 * time.Second), proved: true, want: true},
		"no lower bound, cut": {want: false},
		"prev gone":           {lower: prev, prev: prev, began: at(3 * time.Second), proved: true, want: true},
		"prev returned":       {lower: prev, prev: prev, began: at(time.Second), proved: true, want: false},
		"nothing, clean":      {lower: prev, prev: prev, proved: true, want: true},
		"nothing, failed":     {lower: prev, prev: prev, want: false},
		"from the page's key": {lower: key, prev: prev, began: at(3 * time.Second), proved: true, want: false},
		"no chunk":            {lower: key, began: at(3 * time.Second), proved: true, want: false},
	} {
		f := &filler{}
		assert.Equal(t, c.want, f.headRead(c.lower, c.prev, c.began, c.proved), name)
	}
}

// pauser pauses the first body a getter it wraps answers, not counting a first line's
// read or a follow's, after its first n lines until gate closes or the read's context
// ends.
type pauser struct {
	n      int
	gate   <-chan struct{}
	paused atomic.Bool
}

// on is get, paused as p says.
func (p *pauser) on(get getter) getter {
	return func(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
		body, err := get(ctx, path, q)
		if err != nil || q.Has("limitBytes") || q.Get("follow") == "true" || p.paused.Swap(true) {
			return body, err
		}
		data, err := io.ReadAll(body)
		body.Close()
		if err != nil {
			return nil, err
		}
		cut := 0
		for range p.n {
			if i := strings.IndexByte(string(data[cut:]), '\n'); i >= 0 {
				cut += i + 1
			}
		}
		rest := pausedReader{ctx: ctx, gate: p.gate, r: strings.NewReader(string(data[cut:]))}
		return io.NopCloser(io.MultiReader(strings.NewReader(string(data[:cut])), &rest)), nil
	}
}

// pausedReader is r once gate closes; a read its context ended fails.
type pausedReader struct {
	ctx  context.Context
	gate <-chan struct{}
	r    io.Reader
}

func (p *pausedReader) Read(b []byte) (int, error) {
	select {
	case <-p.gate:
		return p.r.Read(b)
	case <-p.ctx.Done():
		return 0, p.ctx.Err()
	}
}
