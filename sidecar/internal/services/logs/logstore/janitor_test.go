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
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// sweepRetention is a budget of three 100-byte chunks with no share to speak of, read
// within ten seconds.
func sweepRetention() Retention {
	ret := testRetention
	ret.Bytes, ret.StreamShare, ret.ReadTTL = 300, 1<<40, 10*time.Second
	return ret
}

// put adds a chunk of bytes at second from to the stream id, holding one line there,
// stamped at stamp (0 for none) and open or not, and answers its id.
func put(t *testing.T, s *Store, id, from, bytes, stamp int64, open bool) int64 {
	t.Helper()
	c := insertChunk(t, s, id, Chunk{From: at(from, 0), To: at(from, 0), Origin: "kubelet", Open: open, Bytes: bytes, ReadAt: stamp})
	using(t, s, func(f *file) { insertLine(t, f, id, second(from), 0) })
	return c.ID
}

// idsIn is the id of every row of table in the file behind s, in id order.
func idsIn(t *testing.T, s *Store, table string) []int64 {
	t.Helper()
	var out []int64
	using(t, s, func(f *file) {
		rows, err := f.readDB.Query(`SELECT id FROM ` + table + ` ORDER BY id`)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}
		require.NoError(t, rows.Err())
	})
	return out
}

// sweep runs one sweep of the file behind s by hand and answers its report.
func sweep(t *testing.T, s *Store) sweepReport {
	t.Helper()
	var r sweepReport
	using(t, s, func(f *file) {
		var err error
		r, err = s.m.sweep(t.Context(), s.cacheID, f)
		require.NoError(t, err)
	})
	return r
}

// A sweep over budget evicts until the file is under it: never an open chunk or one a
// page read within ReadTTL, a never-read chunk before a read one, the oldest first
// within each — by its first line among the never-read and by its stamp among the read.
func TestASweepEvictsLeastRecentlyReadFirst(t *testing.T) {
	t.Parallel()
	c := newClock(100_000)
	s := newClockedStore(t, sweepRetention(), c)
	id := streamOf(t, s)
	newer := put(t, s, id, 1, 100, 0, false)
	readEarly := put(t, s, id, 2, 100, 50_000, false)
	readNow := put(t, s, id, 3, 100, 95_000, false)
	open := put(t, s, id, 4, 100, 0, true)
	readEarliest := put(t, s, id, 5, 100, 40_000, false)
	older := put(t, s, id, 0, 100, 0, false)

	r := sweep(t, s)

	assert.Equal(t, []int64{readEarly, readNow, open}, idsIn(t, s, "chunks"))
	assert.NotContains(t, idsIn(t, s, "chunks"), newer)
	assert.NotContains(t, idsIn(t, s, "chunks"), readEarliest)
	assert.NotContains(t, idsIn(t, s, "chunks"), older)
	assert.Equal(t, sweepReport{Before: 600, After: 300, Evicted: 3}, r)
}

// A stream over its share gives up its chunks before an older chunk of a stream under
// its share, and only until it is back within the share.
func TestASweepTakesAnOverShareStreamFirst(t *testing.T) {
	t.Parallel()
	c := newClock(100_000)
	ret := sweepRetention()
	ret.Bytes, ret.StreamShare = 1<<40, 150
	s := newClockedStore(t, ret, c)
	a, b := twoStreams(t, s)
	other := put(t, s, b, 1, 100, 0, false)
	first := put(t, s, a, 5, 100, 0, false)
	second := put(t, s, a, 6, 100, 0, false)

	r := sweep(t, s)

	assert.Equal(t, []int64{other, second}, idsIn(t, s, "chunks"))
	assert.NotContains(t, idsIn(t, s, "chunks"), first)
	assert.Equal(t, 1, r.Evicted)
}

// A sweep over budget with nothing left it may evict stops short and says so, and the
// log says it once as the file enters that state and once as it leaves, never once a
// sweep. Serial: it reads the process's logger.
func TestASweepWithNothingToEvictStopsShort(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	c := newClock(100_000)
	s := newClockedStore(t, sweepRetention(), c)
	id := streamOf(t, s)
	held := put(t, s, id, 1, 400, 99_000, false)

	assert.True(t, sweep(t, s).Short)
	assert.True(t, sweep(t, s).Short)
	assert.Equal(t, []int64{held}, idsIn(t, s, "chunks"))
	assert.Equal(t, 1, strings.Count(logs.String(), "over its budget with nothing to evict"))

	c.set(200_000)
	r := sweep(t, s)
	assert.False(t, r.Short)
	assert.Empty(t, idsIn(t, s, "chunks"))
	sweep(t, s)
	assert.Equal(t, 1, strings.Count(logs.String(), "back within its budget"))
}

// podStream files a stream of pod with uid in s and answers its id.
func podStream(t *testing.T, s *Store, pod, uid string) int64 {
	t.Helper()
	row := app()
	row.Pod, row.PodUID = pod, uid
	ids, err := s.PutStreams(t.Context(), []Stream{row})
	require.NoError(t, err)
	return ids[0]
}

// goneAre is a PodsGone that answers the uids it is asked about that are in gone.
func goneAre(gone ...string) PodsGone {
	return func(_ context.Context, _ int64, uids []string) ([]string, error) {
		var out []string
		for _, uid := range uids {
			if slices.Contains(gone, uid) {
				out = append(out, uid)
			}
		}
		return out, nil
	}
}

// A stream with no chunks goes once the service says its pod is gone, and only then:
// not while something holds it, not while its pod is still there, not with no pod
// uid to ask about, and not with chunks left.
func TestASweepRemovesAGonePodsEmptyStreams(t *testing.T) {
	t.Parallel()
	c := newClock(100_000)
	s := newClockedStore(t, sweepRetention(), c)
	gone := podStream(t, s, "gone", "uid-gone")
	held := podStream(t, s, "held", "uid-held")
	live := podStream(t, s, "live", "uid-live")
	unknown := podStream(t, s, "unknown", "")
	full := podStream(t, s, "full", "uid-full")
	put(t, s, full, 1, 10, 0, false)
	release := s.Hold(held)

	r := sweep(t, s)
	assert.Zero(t, r.Removed, "no PodsGone set")
	assert.Equal(t, []int64{gone, held, live, unknown, full}, idsIn(t, s, "streams"))

	s.m.SetPodsGone(goneAre("uid-gone", "uid-held", "uid-unknown", "uid-full", ""))
	r = sweep(t, s)
	assert.Equal(t, 1, r.Removed)
	assert.Equal(t, []int64{held, live, unknown, full}, idsIn(t, s, "streams"))

	release()
	sweep(t, s)
	assert.Equal(t, []int64{live, unknown, full}, idsIn(t, s, "streams"), "once the hold is let go")
}

// A sweep asks about at most its limit of pods, taking up where the last one left
// off: pods that stay never keep a gone one later in the order from being asked.
func TestASweepAsksAboutThePodsInTurn(t *testing.T) {
	t.Parallel()
	c := newClock(100_000)
	s := newClockedStore(t, sweepRetention(), c)
	s.m.podsPerSweep = 2
	var asked [][]string
	s.m.SetPodsGone(func(ctx context.Context, cacheID int64, uids []string) ([]string, error) {
		asked = append(asked, uids)
		return goneAre("uid-c")(ctx, cacheID, uids)
	})
	podStream(t, s, "a", "uid-a")
	podStream(t, s, "b", "uid-b")
	gone := podStream(t, s, "c", "uid-c")

	assert.Zero(t, sweep(t, s).Removed)
	assert.Equal(t, 1, sweep(t, s).Removed)
	assert.NotContains(t, idsIn(t, s, "streams"), gone)
	sweep(t, s)
	assert.Equal(t, [][]string{{"uid-a", "uid-b"}, {"uid-c", "uid-a"}, {"uid-b", "uid-a"}}, asked)
}

// A sweep hands freed pages back only when the file holds some: a sweep that evicted
// nothing leaves the file alone, and one that just evicted vacuums in the same sweep.
func TestASweepVacuumsOnlyWithAFreelist(t *testing.T) {
	t.Parallel()
	c := newClock(100_000)
	s := newClockedStore(t, sweepRetention(), c)
	id := streamOf(t, s)
	big := make([]Line, 64)
	for i := range big {
		big[i] = Line{Pos: at(int64(i+1), 0), Message: strings.Repeat("x", 8<<10)}
	}
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: big})

	assert.False(t, sweep(t, s).Vacuumed, "nothing freed while the page's stamp protects every chunk")

	c.set(1_000_000)
	r := sweep(t, s)
	assert.NotZero(t, r.Evicted)
	assert.True(t, r.Vacuumed)
}

// A sweep whose statement the file refuses, or whose question about the pods fails,
// fails whole, saying so.
func TestASweepTheFileRefusesFails(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		op, table string
		podsGone  PodsGone
	}{
		"an eviction":        {op: "DELETE", table: "lines"},
		"a stream's removal": {op: "DELETE", table: "streams", podsGone: goneAre("uid-gone")},
		"the pods' question": {podsGone: func(context.Context, int64, []string) ([]string, error) {
			return nil, errors.New("refused")
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newClock(100_000)
			s := newClockedStore(t, sweepRetention(), c)
			podStream(t, s, "gone", "uid-gone")
			id := streamOf(t, s)
			put(t, s, id, 1, 400, 0, false)
			if tc.op != "" {
				refuse(t, s, tc.op, tc.table)
			}
			s.m.SetPodsGone(tc.podsGone)

			err := s.Sweep(t.Context())

			assert.ErrorContains(t, err, "sweep: ")
			assert.ErrorContains(t, err, "refused")
		})
	}
}

// A sweep whose context has ended reads nothing and says why.
func TestACancelledSweepFails(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	assert.ErrorIs(t, s.Sweep(ctx), context.Canceled)
}

// A write-ahead log that outweighs the file is truncated by the sweep after it vacuums.
func TestASweepTruncatesAnOutweighingLog(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	id := streamOf(t, s)
	big := make([]Line, 64)
	for i := range big {
		big[i] = Line{Pos: at(int64(i+1), 0), Message: strings.Repeat("x", 8<<10)}
	}
	write(t, s, Fill{Stream: id, Origin: "kubelet", Lines: big})
	var path string
	using(t, s, func(f *file) { path = f.path })
	require.Greater(t, fileSize(path+"-wal"), fileSize(path), "the fixture's log outweighs its file")

	sweep(t, s)

	assert.LessOrEqual(t, fileSize(path+"-wal"), fileSize(path))
}

// A chunk held survives a sweep over budget, as a follow's gap fill does until its walk
// returns, and goes once released.
func TestASweepLeavesAHeldChunk(t *testing.T) {
	t.Parallel()
	c := newClock(100_000)
	ret := sweepRetention()
	ret.Bytes = 0
	s := newClockedStore(t, ret, c)
	id := streamOf(t, s)
	held := put(t, s, id, 1, 100, 0, false)
	release := s.HoldChunks(held)

	sweep(t, s)
	assert.Equal(t, []int64{held}, idsIn(t, s, "chunks"))

	release()
	sweep(t, s)
	assert.Empty(t, idsIn(t, s, "chunks"))
}
