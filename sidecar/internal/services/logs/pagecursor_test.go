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
	"bytes"
	"compress/flate"
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// webCursor is a newer page cursor over webScope's deployment between 10:00:10 and
// 10:00:20, padded by three lines each side, with an entry for web-1's app and a
// floor at web-2's.
func webCursor() pageCursor {
	web1 := lineAt(StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}, 12*time.Second, 1)
	return pageCursor{
		Scope: webScope,
		resolvedWindow: resolvedWindow{
			Window:     Window{Since: moment(10 * time.Second), Until: moment(20 * time.Second), Before: Padding{Lines: 3}, After: Padding{Lines: 3}},
			ResolvedAt: at(time.Hour),
			Start:      lineAt(StreamKey{Namespace: "prod", Pod: "web-2", Container: "app"}, 8*time.Second, 0),
		},
		newer:  true,
		seen:   map[containerName]runPlace{web1.containerName: web1.runPlace},
		at:     lineAt(StreamKey{Namespace: "prod", Pod: "web-2", Container: "app"}, 11*time.Second, 0),
		before: 2,
		after:  1,
	}
}

// rawPageCursor is c's JSON, inflated.
func rawPageCursor(t *testing.T, c string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(c)
	require.NoError(t, err)
	var out bytes.Buffer
	_, err = out.ReadFrom(flate.NewReader(bytes.NewReader(b)))
	require.NoError(t, err)
	return out.String()
}

// pageCursorOfJSON is j deflated and encoded as a page cursor is.
func pageCursorOfJSON(t *testing.T, j string) string {
	t.Helper()
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, flate.BestCompression)
	require.NoError(t, err)
	_, err = w.Write([]byte(j))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return base64.RawURLEncoding.EncodeToString(b.Bytes())
}

// A page cursor decodes to what was encoded, and one cursor has one string.
func TestAPageCursorRoundTrips(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]pageCursor{
		"newer":          webCursor(),
		"older":          {Scope: webScope, resolvedWindow: resolvedWindow{ResolvedAt: at(time.Hour)}, at: momentAt(at(5 * time.Second))},
		"no at":          {Scope: webScope, resolvedWindow: resolvedWindow{ResolvedAt: at(time.Hour)}, newer: true},
		"a span":         {Scope: webScope, resolvedWindow: resolvedWindow{Window: Window{Since: moment(0), After: Padding{Span: time.Minute}}, ResolvedAt: at(time.Hour), Start: momentAt(at(0))}, newer: true},
		"a grep":         {Scope: Scope{Sources: webScope.Sources, Filters: []ScopeFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}, Grep: "err(or)?"}, resolvedWindow: resolvedWindow{ResolvedAt: at(time.Hour)}},
		"a moment entry": {Scope: webScope, resolvedWindow: resolvedWindow{ResolvedAt: at(time.Hour)}, newer: true, seen: map[containerName]runPlace{{"prod", "web-1", "app"}: {Timestamp: at(3 * time.Second)}}},
	} {
		s := encodePageCursor(c)
		back, err := decodePageCursor(s)
		require.NoError(t, err, name)
		if len(c.seen) == 0 {
			c.seen = nil
		}
		assert.Equal(t, c, back, name)
		assert.Equal(t, s, encodePageCursor(back), name+": one string")
	}
}

// A window's bounds are written in UTC, so one window named in two zones has one
// string.
func TestAPageCursorsBoundsAreUTC(t *testing.T) {
	t.Parallel()
	c := webCursor()
	east := time.FixedZone("east", 2*60*60)
	since, until := c.Since.In(east), c.Until.In(east)
	zoned := c
	zoned.Since, zoned.Until = &since, &until

	assert.Equal(t, encodePageCursor(c), encodePageCursor(zoned))
	back, err := decodePageCursor(encodePageCursor(zoned))
	require.NoError(t, err)
	assert.Equal(t, time.UTC, back.Since.Location())
	assert.Equal(t, time.UTC, back.Until.Location())
}

// A page cursor is deflated JSON with a version first, its entries a list in name
// order whatever the map's order.
func TestAPageCursorIsVersionedJSON(t *testing.T) {
	t.Parallel()
	c := webCursor()
	c.seen[containerName{"prod", "web-0", "app"}] = runPlace{"containerd://uid-web-0-app-0", at(9 * time.Second), 0}

	raw := rawPageCursor(t, encodePageCursor(c))

	assert.True(t, strings.HasPrefix(raw, `{"v":1,"s":{"sources":`), raw)
	assert.Less(t, strings.Index(raw, `"pod":"web-0"`), strings.Index(raw, `"pod":"web-1"`), raw)
	assert.Contains(t, raw, `"n":true`)
	assert.Contains(t, raw, `"b":2,"a":1`)
}

// A page cursor of more than MaxStreams entries decodes: the service writes one for
// every container a page passed, gone pods' included.
func TestAPageCursorOfManyEntriesDecodes(t *testing.T) {
	t.Parallel()
	c := webCursor()
	for i := range MaxStreams + 10 {
		c.seen[containerName{"prod", "web-" + strconv.Itoa(100+i), "app"}] = runPlace{"containerd://a", at(time.Second), 0}
	}

	back, err := decodePageCursor(encodePageCursor(c))

	require.NoError(t, err)
	assert.Len(t, back.seen, MaxStreams+11)
}

// A string that is not a page cursor this version wrote is cursor-invalid.
func TestABadPageCursorIsRefused(t *testing.T) {
	t.Parallel()
	good := rawPageCursor(t, encodePageCursor(webCursor()))
	older := webCursor()
	older.newer = false
	for name, s := range map[string]string{
		"not base64url":          "a+b/",
		"not deflated":           base64.RawURLEncoding.EncodeToString([]byte("{}")),
		"not JSON":               pageCursorOfJSON(t, "yesterday"),
		"another version":        pageCursorOfJSON(t, strings.Replace(good, `"v":1`, `"v":2`, 1)),
		"a bad line":             pageCursorOfJSON(t, strings.Replace(good, `"n":1`, `"n":-1`, 1)),
		"a bare moment":          pageCursorOfJSON(t, strings.Replace(good, `"seen":[{"v":1,"ns":"prod","pod":"web-1","c":"app",`, `"seen":[{"v":1,`, 1)),
		"no clock":               pageCursorOfJSON(t, strings.Replace(good, `"resolvedAt":"2026-10-08T11:00:00Z"`, `"resolvedAt":"0001-01-01T00:00:00Z"`, 1)),
		"an older entry":         encodePageCursor(older),
		"a count past its lines": pageCursorOfJSON(t, strings.Replace(good, `"b":2`, `"b":4`, 1)),
		"a negative count":       pageCursorOfJSON(t, strings.Replace(good, `"a":1`, `"a":-1`, 1)),
		"a window refused":       pageCursorOfJSON(t, strings.Replace(good, `"lines":3`, `"lines":501`, 1)),
	} {
		_, err := decodePageCursor(s)
		assert.Equal(t, CodeCursorInvalid, codeOf(err), name)
	}

	spanned := webCursor()
	spanned.Window.After = Padding{Span: time.Minute}
	_, err := decodePageCursor(encodePageCursor(spanned))
	assert.Equal(t, CodeCursorInvalid, codeOf(err), "a count for a span")

	open := webCursor()
	open.Since, open.Before, open.before = nil, Padding{}, 0
	_, err = decodePageCursor(encodePageCursor(open))
	assert.Equal(t, CodeCursorInvalid, codeOf(err), "a start for an open since with no count")

	_, err = decodePageCursor(pageCursorOfJSON(t, strings.Replace(good, `"v":1`, `"v":2`, 1)))
	assert.ErrorContains(t, err, "read again")
}

// A first page pins its clock as a continuation decodes it: in UTC, with no
// monotonic reading, so every page reports the same ResolvedAt.
func TestAFirstPagePinsItsClockAsAContinuationDecodesIt(t *testing.T) {
	t.Parallel()
	now := time.Now().In(time.FixedZone("UTC+2", 2*60*60))
	c, err := firstPage(FetchRequest{Scope: webScope, PageFrom: PageFromFirst}, now)
	require.NoError(t, err)

	cont, err := decodePageCursor(encodePageCursor(c))
	require.NoError(t, err)
	assert.True(t, c.ResolvedAt == cont.ResolvedAt, "%v != %v", c.ResolvedAt, cont.ResolvedAt)
}

// A first page whose own edge is counted reads that side's context beside its
// range, the other way from the bound: back from Since for PageFromFirst, on from the
// range's end for PageFromLast. A first page from the other edge, or one whose edge is
// a time or a span, reads none; padding before an open Since counts for nothing.
func TestACountedFirstEdgeReadsItsContext(t *testing.T) {
	t.Parallel()
	w := Window{Since: moment(10 * time.Second), Until: moment(20 * time.Second), Before: Padding{Lines: 3}, After: Padding{Lines: 4}}
	c, err := firstPage(FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst}, at(time.Hour))
	require.NoError(t, err)

	l, lines := c.lookup()
	require.NotNil(t, l)
	assert.Equal(t, c.turned(momentAt(at(10*time.Second))), *l)
	assert.Equal(t, 3, lines)
	assert.Equal(t, pos(10*time.Second, -1), l.keyFor(psOf("web-1", "app")), "before every line at since")

	c.newer = false
	l, lines = c.lookup()
	require.NotNil(t, l)
	assert.Equal(t, c.turned(momentAt(at(20*time.Second))), *l)
	assert.Equal(t, 4, lines)
	assert.Equal(t, pos(20*time.Second, math.MaxInt), l.keyFor(psOf("web-1", "app")), "after every line at until")

	open, err := firstPage(FetchRequest{Window: Window{After: Padding{Lines: 4}}, PageFrom: PageFromLast}, at(time.Hour))
	require.NoError(t, err)
	l, _ = open.lookup()
	require.NotNil(t, l)
	assert.Equal(t, momentAt(at(time.Hour-1)), l.at, "an open until counts from the first page's clock")

	for name, tc := range map[string]struct {
		w    Window
		from PageFrom
	}{
		"the other edge":       {Window{Since: moment(10 * time.Second), Before: Padding{Lines: 3}}, PageFromLast},
		"a span":               {Window{Since: moment(0), Before: Padding{Span: time.Second}}, PageFromFirst},
		"before an open since": {Window{Before: Padding{Lines: 3}}, PageFromFirst},
	} {
		c, err := firstPage(FetchRequest{Window: tc.w, PageFrom: tc.from}, at(time.Hour))
		require.NoError(t, err, name)
		l, _ := c.lookup()
		assert.Nil(t, l, name)
	}
}

// psOf is the page stream of pod/container's first run.
func psOf(pod, container string) pageStream { return pageStream{row: rowOf(pod, container)} }

// pos is the file position of the ordinal-th line at d past 10:00.
func pos(d time.Duration, ord int) logstore.Pos { return logstore.Pos{TS: at(d).UnixNano(), Ord: ord} }

// A stream reads from its entry, else from at, else from the window's edge, each
// clipped to the window's known edge; an older cursor from at, else the window's
// end.
func TestAStreamsKeyIsItsEntryAtOrTheEdge(t *testing.T) {
	t.Parallel()
	c := webCursor()
	c.Start = momentAt(at(9 * time.Second))
	web1, web2 := psOf("web-1", "app"), psOf("web-2", "app")

	assert.Equal(t, pos(12*time.Second, 1), c.keyFor(web1), "past its entry")
	assert.Equal(t, pos(11*time.Second, 0), c.keyFor(web2), "past at, its own line")
	assert.Equal(t, pos(11*time.Second, math.MaxInt), c.keyFor(psOf("web-1", "sidecar")), "past at, after its moment's lines sorting before at")

	c.at = lineCursor{}
	assert.Equal(t, pos(9*time.Second, -1), c.keyFor(web2), "from Start")

	c.seen[web1.run().containerName] = runPlace{web1.row.ContainerID, at(2 * time.Second), 0}
	assert.Equal(t, pos(9*time.Second, -1), c.keyFor(web1), "an entry short of Start is clipped to it")

	c.seen[web1.run().containerName] = runPlace{Timestamp: at(12 * time.Second)}
	assert.Equal(t, pos(12*time.Second, math.MaxInt), c.keyFor(web1), "past a container's moment")

	older := pageCursor{resolvedWindow: resolvedWindow{Window: Window{Until: moment(20 * time.Second)}, ResolvedAt: at(time.Hour)}}
	assert.Equal(t, pos(20*time.Second, math.MaxInt), older.keyFor(web1), "an older cursor from the end")
	older.at = momentAt(at(15 * time.Second))
	assert.Equal(t, pos(15*time.Second, -1), older.keyFor(web1), "before at")
	older.at = momentAt(at(25 * time.Second))
	assert.Equal(t, pos(20*time.Second, math.MaxInt), older.keyFor(web1), "at past the end is clipped to it")

	counted := pageCursor{resolvedWindow: resolvedWindow{Window: Window{Since: moment(10 * time.Second), Before: Padding{Lines: 3}}, ResolvedAt: at(time.Hour)}, newer: true}
	assert.Equal(t, pos(10*time.Second, -1), counted.keyFor(web1), "a counted start not yet found reads from Since")
	counted.at = momentAt(at(5 * time.Second))
	assert.Equal(t, pos(5*time.Second, math.MaxInt), counted.keyFor(web1), "no clip while the edge is a count")
}

// Two runs of one name share one entry, so the restarted run reads on from the
// old run's place rather than from the window's start; at the entry's moment the
// ordinal decides, then the id.
func TestAnEntryHoldsAcrossARestart(t *testing.T) {
	t.Parallel()
	c := webCursor()
	c.at = lineCursor{}
	old := psOf("web-1", "app")
	restarted := old
	restarted.row.ContainerID = containerID("uid-web-1", "app", 1)
	c.seen[old.run().containerName] = runPlace{old.row.ContainerID, at(12 * time.Second), 3}

	assert.Equal(t, pos(12*time.Second, 3), c.keyFor(old))
	assert.Equal(t, pos(12*time.Second, 2), c.keyFor(restarted), "its own line at the ordinal sorts past the old id's")
}

// A first page's Seen names a stream's last line: the newer of two for one stream
// stands, whatever their order, and a moment is refused.
func TestSeenIsTheFirstPagesEntries(t *testing.T) {
	t.Parallel()
	first := nth("web-1", "app", time.Second, 0, "")
	later := nth("web-1", "app", 2*time.Second, 0, "")
	other := nth("web-2", "app", time.Second, 0, "")

	want := map[containerName]runPlace{
		lineCursorOf(later).containerName: lineCursorOf(later).runPlace,
		lineCursorOf(other).containerName: lineCursorOf(other).runPlace,
	}
	for _, order := range [][]string{
		{first.Cursor(), other.Cursor(), later.Cursor()},
		{later.Cursor(), other.Cursor(), first.Cursor()},
	} {
		seen, err := seenOf(order)
		require.NoError(t, err)
		assert.Equal(t, want, seen, "the newer stands in either order")
	}
	_, err := seenOf([]string{encodeLineCursor(momentAt(at(0)))})
	assert.Equal(t, CodeCursorInvalid, codeOf(err), "a moment")
	_, err = seenOf([]string{"nope"})
	assert.Equal(t, CodeCursorInvalid, codeOf(err), "not a cursor")
}

// reachOf is the reach of pod/container's first run from first to last, d past
// 10:00 each, the first line of its moment.
func reachOf(pod, container string, first, last time.Duration) streamReach {
	key := StreamKey{Namespace: "prod", Pod: pod, Container: container}
	return streamReach{run: psOf(pod, container).run(), first: lineAt(key, first, 0), last: lineAt(key, last, 0)}
}

// entry is the seen entry of pod/container's first run at d, the first of its
// moment.
func entry(pod, container string, d time.Duration) runPlace {
	return lineAt(StreamKey{Namespace: "prod", Pod: pod, Container: container}, d, 0).runPlace
}

// name is pod/container in prod.
func name(pod, container string) containerName { return containerName{"prod", pod, container} }

// openCursor is a cursor over an open window, newer or older.
func openCursor(newer bool) pageCursor {
	return pageCursor{Scope: webScope, resolvedWindow: resolvedWindow{ResolvedAt: at(time.Hour)}, newer: newer}
}

// Going newer, each stream's entry is the last row it passed, kept or rejected by
// the grep, short of the cut; past the cut, its last line on the page; a stream
// stopped at a moment reads on past the moment. at goes on as it came.
func TestTheEdgeAheadNewerIsEachStreamsReach(t *testing.T) {
	t.Parallel()
	c := openCursor(true)
	c.at = momentAt(at(0))
	c.seen = map[containerName]runPlace{name("web-3", "app"): entry("web-3", "app", 0), name("gone", "app"): entry("gone", "app", 0)}
	loud := reachOf("web-3", "app", 0, 0)
	loud.first, loud.last = lineCursor{}, lineCursor{}
	loud.stop = lineCursor{loud.run.containerName, runPlace{Timestamp: at(3 * time.Second)}}
	m := merge{
		records: []Record{line("web-1", "app", time.Second, "a"), line("web-2", "app", 2*time.Second, "b"), line("web-1", "app", 3*time.Second, "c")},
		cut:     lineAt(StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}, 3*time.Second, 0),
		reaches: []streamReach{reachOf("web-1", "app", time.Second, 5*time.Second), reachOf("web-2", "app", 2*time.Second, 2500*time.Millisecond), loud, reachOf("web-4", "app", 0, 0)},
	}
	m.reaches[3].first, m.reaches[3].last = lineCursor{}, lineCursor{}

	ahead, behind := c.edges(m)

	assert.True(t, ahead.newer)
	assert.Equal(t, c.at, ahead.at)
	assert.Equal(t, map[containerName]runPlace{
		name("web-1", "app"): entry("web-1", "app", 3*time.Second),
		name("web-2", "app"): entry("web-2", "app", 2500*time.Millisecond),
		name("web-3", "app"): {Timestamp: at(3 * time.Second)},
	}, ahead.seen, "web-1 past the cut falls back to its last line; web-2 keeps its rejected row; web-3 its stop; gone left the scope")
	require.NotNil(t, behind)
	assert.False(t, behind.newer)
	assert.Equal(t, lineCursorOf(m.records[0]), behind.at)
	assert.Nil(t, behind.seen)
}

// Going older, the edge ahead is one position: the cut, else the farthest row
// passed, with no entry; the edge behind goes newer from each stream's newest line
// on the page, and from the farthest line passed for a stream with none.
func TestTheEdgesOfAnOlderPage(t *testing.T) {
	t.Parallel()
	c := openCursor(false)
	m := merge{
		records: []Record{line("web-1", "app", 3*time.Second, "c"), line("web-2", "app", 4*time.Second, "d"), line("web-1", "app", 5*time.Second, "e")},
		reaches: []streamReach{reachOf("web-1", "app", 5*time.Second, 3*time.Second), reachOf("web-2", "app", 4*time.Second, time.Second)},
	}

	ahead, behind := c.edges(m)

	assert.False(t, ahead.newer)
	assert.Nil(t, ahead.seen)
	assert.Equal(t, lineAt(StreamKey{Namespace: "prod", Pod: "web-2", Container: "app"}, time.Second, 0), ahead.at, "the farthest row passed")
	require.NotNil(t, behind)
	assert.True(t, behind.newer)
	assert.Equal(t, map[containerName]runPlace{name("web-1", "app"): entry("web-1", "app", 5*time.Second), name("web-2", "app"): entry("web-2", "app", 4*time.Second)}, behind.seen)
	assert.Equal(t, ahead.at, behind.at)

	m.cut = lineCursorOf(m.records[0])
	ahead, _ = c.edges(m)
	assert.Equal(t, m.cut, ahead.at, "a cut page goes on from its cut")
}

// A page that passed no line hands its incoming edge back, and has none behind.
func TestAPageThatPassedNothingKeepsItsEdge(t *testing.T) {
	t.Parallel()
	c := openCursor(true)
	c.seen = map[containerName]runPlace{name("web-1", "app"): entry("web-1", "app", time.Second)}
	m := merge{records: []Record{}, reaches: []streamReach{{run: psOf("web-1", "app").run()}}}

	ahead, behind := c.edges(m)

	assert.Equal(t, c, ahead)
	assert.Nil(t, behind)

	older := openCursor(false)
	older.at = momentAt(at(time.Second))
	ahead, behind = older.edges(merge{records: []Record{}, reaches: m.reaches})
	assert.Equal(t, older, ahead)
	assert.Nil(t, behind)
}

// Two runs of one name passed on one page leave the later as the entry.
func TestTwoRunsOfOneNameLeaveTheLaterEntry(t *testing.T) {
	t.Parallel()
	old, restarted := reachOf("web-1", "app", 0, 2*time.Second), reachOf("web-1", "app", 3*time.Second, 4*time.Second)
	restarted.run.ContainerID = containerID("uid-web-1", "app", 1)
	restarted.first.ContainerID, restarted.last.ContainerID = restarted.run.ContainerID, restarted.run.ContainerID

	ahead, _ := openCursor(true).edges(merge{records: []Record{}, reaches: []streamReach{restarted, old}})

	assert.Equal(t, map[containerName]runPlace{name("web-1", "app"): restarted.last.runPlace}, ahead.seen)
}

// Each edge's counts move by the context lines the page delivered: up moving away
// from a bound, down moving toward it, clamped to 0 through Lines; the edge behind
// keeps the incoming counts.
func TestTheCountsMoveByTheContextDelivered(t *testing.T) {
	t.Parallel()
	w := resolvedWindow{Window: Window{Since: moment(10 * time.Second), Until: moment(20 * time.Second), Before: Padding{Lines: 3}, After: Padding{Lines: 3}}, ResolvedAt: at(time.Hour)}
	recs := []Record{line("web-1", "app", 8*time.Second, ""), line("web-1", "app", 9*time.Second, ""), line("web-1", "app", 15*time.Second, ""), line("web-1", "app", 21*time.Second, "")}
	m := merge{records: recs, reaches: []streamReach{reachOf("web-1", "app", 8*time.Second, 21*time.Second)}}

	newer := pageCursor{Scope: webScope, resolvedWindow: w, newer: true, before: 1, after: 1}
	ahead, behind := newer.edges(m)
	assert.Equal(t, [2]int{0, 2}, [2]int{ahead.before, ahead.after})
	assert.Equal(t, [2]int{1, 1}, [2]int{behind.before, behind.after})

	older := pageCursor{Scope: webScope, resolvedWindow: w, before: 2, after: 1}
	ahead, behind = older.edges(m)
	assert.Equal(t, [2]int{3, 0}, [2]int{ahead.before, ahead.after})
	assert.Equal(t, [2]int{2, 1}, [2]int{behind.before, behind.after})
}
