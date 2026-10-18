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
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// A line's cursor names it back; a time alone names a moment.
func TestACursorNamesALineOrAMoment(t *testing.T) {
	t.Parallel()
	p := lineAt(StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}, 1500*time.Millisecond, 3)

	back, err := parseCursor(*cursorOf(p))
	require.NoError(t, err)
	assert.Equal(t, p, back)

	moment, err := parseCursor("2026-10-08T10:00:01.5Z")
	require.NoError(t, err)
	assert.Equal(t, position{Timestamp: at(1500 * time.Millisecond)}, moment)
	assert.True(t, moment.moment())
	assert.False(t, back.moment())
}

// A record's cursor names its line.
func TestARecordsCursorNamesItsLine(t *testing.T) {
	t.Parallel()
	rec := nth("web-1", "app", time.Second, 2, "hello")

	p, err := parseCursor(rec.Cursor())

	require.NoError(t, err)
	assert.Equal(t, positionOf(rec), p)
}

// A line keeps its cursor once its instance is the previous one: the id names the
// instance, not whether it is the last.
func TestALineKeepsItsCursorAcrossARestart(t *testing.T) {
	t.Parallel()
	rec := line("web-1", "app", time.Second, "")
	prev := rec
	prev.Stream.Previous = true

	assert.Equal(t, rec.Cursor(), prev.Cursor())
}

// A cursor that is neither is refused, and so is one whose instance id no runtime
// writes: empty, holding a control character, or past 256 bytes.
func TestABadCursorIsRefused(t *testing.T) {
	t.Parallel()
	line := func(id string) string { return "2026-10-08T10:00:01Z\tprod\tweb\tapp\t" + id + "\t0" }
	for _, c := range []string{
		"yesterday",
		"2026-10-08T10:00:01Z\tprod",
		"2026-10-08T10:00:01Z\tprod\tweb\tapp\tcontainerd://a\t-1",
		"2026-10-08T10:00:01Z\tprod\tweb\tapp\tcontainerd://a\t" + strconv.Itoa(math.MaxInt),
		"2026-10-08T10:00:01Z\tprod\tweb\t\tcontainerd://a\t0",
		line(""),
		line("containerd://a\nb"),
		line("containerd://a\x7fb"),
		line("containerd://" + strings.Repeat("a", 244)),
	} {
		_, err := parseCursor(c)
		assert.EqualError(t, err, "a cursor must be an RFC 3339 time, or one a page handed back", c)
	}
}

// A cursor takes whatever id a runtime writes, up to 256 bytes: the server handed
// it out.
func TestACursorTakesARuntimesID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"containerd://uid-web-1-app-0", "docker://3f4e", "containerd://" + strings.Repeat("a", 243)} {
		p, err := parseCursor("2026-10-08T10:00:01Z\tprod\tweb\tapp\t" + id + "\t0")
		require.NoError(t, err, id)
		assert.Equal(t, id, p.ContainerID)
	}
}

// Lines order by time, then namespace, pod and container, then ordinal, and the
// instance id decides only an equal ordinal; against a moment, by time alone.
func TestPositionsOrder(t *testing.T) {
	t.Parallel()
	a := StreamKey{Namespace: "prod", Pod: "a", Container: "app"}
	sidecar := StreamKey{Namespace: "prod", Pod: "a", Container: "sidecar"}
	older, newer := lineAt(a, 0, 0), lineAt(a, 0, 0)
	older.ContainerID, newer.ContainerID = "containerd://a-0", "containerd://a-1"
	assert.Less(t, comparePositions(lineAt(a, 0, 5), lineAt(a, time.Second, 0)), 0)
	assert.Less(t, comparePositions(lineAt(a, 0, 0), lineAt(a, 0, 1)), 0)
	assert.Less(t, comparePositions(older, newer), 0)
	assert.Greater(t, comparePositions(newer, older), 0)
	assert.Less(t, comparePositions(lineAt(a, 0, 9), lineAt(sidecar, 0, 0)), 0)
	assert.Equal(t, 0, comparePositions(lineAt(a, 0, 7), position{Timestamp: at(0)}))
	assert.Equal(t, 0, comparePositions(position{Timestamp: at(0)}, lineAt(a, 0, 7)))
}

// A line past a cursor's ordinal at the cursor's moment sorts after it whichever
// instance id it carries: the resolve lags a restart, so a page can end on a
// line keyed with the old id that the next page keys with the new one, and the
// lines past it must not fall before the cursor.
func TestALaterOrdinalFollowsTheCursorWhateverItsID(t *testing.T) {
	t.Parallel()
	a := StreamKey{Namespace: "prod", Pod: "a", Container: "app"}
	for _, ids := range [][2]string{{"containerd://a-0", "containerd://a-1"}, {"containerd://a-1", "containerd://a-0"}} {
		end, next := lineAt(a, 0, 3), lineAt(a, 0, 4)
		end.ContainerID, next.ContainerID = ids[0], ids[1]
		assert.Greater(t, comparePositions(next, end), 0, ids)
	}
}

// A bound keeps or drops the line it sits on as its inclusivity says, and a
// moment bound every line of the moment.
func TestBoundsKeepOrDropTheirLine(t *testing.T) {
	t.Parallel()
	a := StreamKey{Namespace: "prod", Pod: "a", Container: "app"}
	rec := nth("a", "app", 0, 1, "")
	assert.False(t, bound{lineAt(a, 0, 1), true}.below(rec))
	assert.True(t, bound{lineAt(a, 0, 1), false}.below(rec))
	assert.True(t, bound{lineAt(a, 0, 2), true}.below(rec))
	assert.False(t, bound{position{Timestamp: at(0)}, true}.below(rec))
	assert.True(t, bound{position{Timestamp: at(0)}, false}.below(rec))
	assert.False(t, bound{position{Timestamp: at(0)}, true}.above(rec))
	assert.True(t, bound{position{Timestamp: at(0)}, false}.above(rec))
	assert.False(t, bound{}.below(rec))
	assert.False(t, bound{}.above(rec))
}

// Each stream's bound is worked out from the one cursor as comparePositions orders
// lines: at the cursor's moment a stream whose names sort before the cursor's
// starts after the moment, one whose names sort after starts at it, the cursor's own
// stream starts past its ordinal, and another instance of its container at its
// ordinal, kept when its id sorts after the cursor's and dropped when before. Tail
// is the mirror image, and a moment bounds every stream at the moment.
func TestEachStreamsBoundOffOneCursor(t *testing.T) {
	t.Parallel()
	moment := at(0).UnixNano()
	ats := func(ord int) logstore.Pos { return logstore.Pos{TS: moment, Ord: ord} }
	stream := func(pod, id string) instance { return instance{"prod", pod, "app", id} }
	cursor := bound{position{at(0), "prod", "web-1", "app", "b", 2}, false}
	for name, tc := range map[string]struct {
		in         instance
		head, tail logstore.Pos
	}{
		"names before":  {stream("web-0", "b"), ats(math.MaxInt), ats(math.MaxInt)},
		"names after":   {stream("web-2", "b"), ats(-1), ats(-1)},
		"its own":       {stream("web-1", "b"), ats(2), ats(2)},
		"an earlier id": {stream("web-1", "a"), ats(2), ats(3)},
		"a later id":    {stream("web-1", "c"), ats(1), ats(2)},
	} {
		assert.Equal(t, tc.head, streamBound(cursor, tc.in, FetchHead), name+", head")
		assert.Equal(t, tc.tail, streamBound(cursor, tc.in, FetchTail), name+", tail")
	}

	own := stream("web-1", "b")
	kept := bound{cursor.at, true}
	assert.Equal(t, ats(1), streamBound(kept, own, FetchHead), "a kept line, head")
	assert.Equal(t, ats(3), streamBound(kept, own, FetchTail), "a kept line, tail")
	m := position{Timestamp: at(0)}
	assert.Equal(t, ats(math.MaxInt), streamBound(bound{m, false}, own, FetchHead), "after a moment")
	assert.Equal(t, ats(-1), streamBound(bound{m, true}, own, FetchHead), "from a moment")
	assert.Equal(t, ats(-1), streamBound(bound{m, false}, own, FetchTail), "before a moment")
	assert.Equal(t, ats(math.MaxInt), streamBound(bound{m, true}, own, FetchTail), "up to a moment")
	assert.Equal(t, logstore.Oldest, streamBound(bound{}, own, FetchHead), "the start")
	assert.Equal(t, logstore.Newest, streamBound(bound{}, own, FetchTail), "the end")
}

// LineCursor spells a line's fields as a page's cursor does, and refuses what
// parseCursor refuses.
func TestLineCursorIsAPagesCursor(t *testing.T) {
	t.Parallel()
	rec := nth("web-1", "app", time.Second, 3, "a")

	c, err := LineCursor(rec.Timestamp, "prod", "web-1", "app", rec.ContainerID, 3)
	require.NoError(t, err)
	assert.Equal(t, rec.Cursor(), c)

	id := rec.ContainerID
	for name, bad := range map[string]func() (string, error){
		"no time":        func() (string, error) { return LineCursor(time.Time{}, "prod", "web-1", "app", id, 3) },
		"no namespace":   func() (string, error) { return LineCursor(rec.Timestamp, "", "web-1", "app", id, 3) },
		"no pod":         func() (string, error) { return LineCursor(rec.Timestamp, "prod", "", "app", id, 3) },
		"no container":   func() (string, error) { return LineCursor(rec.Timestamp, "prod", "web-1", "", id, 3) },
		"a tab":          func() (string, error) { return LineCursor(rec.Timestamp, "prod", "web\t1", "app", id, 3) },
		"a tab in an id": func() (string, error) { return LineCursor(rec.Timestamp, "prod", "web-1", "app", "c://a\tb", 3) },
		"a long id": func() (string, error) {
			return LineCursor(rec.Timestamp, "prod", "web-1", "app", strings.Repeat("a", 257), 3)
		},
		"a negative": func() (string, error) { return LineCursor(rec.Timestamp, "prod", "web-1", "app", id, -1) },
		"the last":   func() (string, error) { return LineCursor(rec.Timestamp, "prod", "web-1", "app", id, math.MaxInt) },
	} {
		_, err := bad()
		assert.ErrorIs(t, err, errBadCursor, name)
	}
}
