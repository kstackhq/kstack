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
	"cmp"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// pageCursor is which way the pages go on, and where from. The embedded Scope
// and resolvedWindow are the first page's, echoed on every continuation; the
// scope is resolved again on every read. newer is the way this cursor reads.
// seen is where each stream goes on from, exclusive: the last line of it the
// pages passed that way, one entry per stream, the stream read off the entry;
// the same list a caller sends as a request's Seen. A stream with no entry goes
// on from at, exclusive, a line or a moment; with at zero, from the window's
// Start, since nothing of that stream has been shown. Only a newer cursor
// carries entries, and only one going on from a page read the other way carries
// both entries and at. before and after are distances, for a counted Padding:
// the context lines that lie between Since and this cursor's position, and
// between it and Until; 0 for a position inside the range. A side whose edge is
// not yet a position is paged by its count alone.
type pageCursor struct {
	Scope
	resolvedWindow

	newer         bool
	seen          map[containerName]runPlace
	at            lineCursor
	before, after int
}

// firstPage is the page cursor a first page reads from: the window's edge its
// PageFrom names, on the clock now, its entries the request's Seen. A Since that
// is a time, or that a span widens, pins the window's start; a counted one waits
// for the read that finds it. now is kept in UTC with no monotonic reading, as a
// continuation decodes it, so every page reports the same ResolvedAt.
func firstPage(q FetchRequest, now time.Time) (pageCursor, error) {
	if err := checkWindow(q.Window); err != nil {
		return pageCursor{}, err
	}
	seen, err := seenOf(q.Seen)
	if err != nil {
		return pageCursor{}, err
	}
	c := pageCursor{
		Scope: q.Scope, resolvedWindow: resolvedWindow{Window: q.Window, ResolvedAt: now.UTC().Round(0)},
		newer: q.PageFrom == PageFromFirst, seen: seen,
	}
	if q.Since != nil && q.Before.Lines == 0 {
		c.Start = momentAt(q.Since.Add(-q.Before.Span))
	}
	return c, nil
}

// errBadSeen refuses a Seen entry that does not name a line.
var errBadSeen = refuse(CodeCursorInvalid, "a seen entry is a line cursor a record carried")

// seenOf is a request's Seen as a cursor's entries: each the last line the caller
// has of its stream, the newer of two for one stream standing, whatever their
// order, so no stream reads lines again.
func seenOf(seen []string) (map[containerName]runPlace, error) {
	if len(seen) == 0 {
		return nil, nil
	}
	out := make(map[containerName]runPlace, len(seen))
	for _, s := range seen {
		c, err := decodeLineCursor(s)
		if err != nil {
			return nil, err
		}
		if c.moment() {
			return nil, errBadSeen
		}
		if e, ok := out[c.containerName]; ok && compareLineCursors(c, lineCursor{c.containerName, e}) < 0 {
			continue
		}
		out[c.containerName] = c.runPlace
	}
	return out, nil
}

// lookup is the context read a first page runs beside its range read when its
// own edge is counted, and the lines it reads: back from Since for PageFromFirst, on
// from the range's end for PageFromLast, each stream from the bound. Nil for an edge
// that is a time or a span.
func (c pageCursor) lookup() (*pageCursor, int) {
	var l pageCursor
	switch {
	case c.newer && c.counted(sideBefore):
		l = c.turned(momentAt(*c.Since))
	case !c.newer && c.counted(sideAfter):
		l = c.turned(c.rangeEnd().at)
	default:
		return nil, 0
	}
	return &l, c.lines(l.away())
}

// dir is the way c reads.
func (c pageCursor) dir() direction {
	if c.newer {
		return newer
	}
	return older
}

// turned is c reading the other way from at, with no entry.
func (c pageCursor) turned(at lineCursor) pageCursor {
	c.newer, c.seen, c.at = !c.newer, nil, at
	return c
}

// away is the side c moves away from as it reads.
func (c pageCursor) away() side {
	if c.newer {
		return sideAfter
	}
	return sideBefore
}

// count is the context lines c has passed on side s.
func (c pageCursor) count(s side) int {
	if s == sideBefore {
		return c.before
	}
	return c.after
}

// nearEdge is the window's known edge c reads away from, zero while it is open or
// a count's edge is still to be found.
func (c pageCursor) nearEdge() bound {
	if c.newer {
		return c.start()
	}
	return c.end()
}

// farEdge is the window's known edge c reads toward, zero while it is open or a
// count's edge is still to be found.
func (c pageCursor) farEdge() bound {
	if c.newer {
		return c.end()
	}
	return c.start()
}

// keyFor is where ps's read starts: past its entry, else past at, else from the
// window's edge that way, a counted edge still to be found read as its range
// bound. A key short of the window's known near edge is clipped to it, so a page
// never crosses the window's edge; no clip applies while that edge is a count
// still to be found.
func (c pageCursor) keyFor(ps pageStream) logstore.Pos {
	run := ps.run()
	b := c.nearEdge()
	if b.at.isZero() {
		b = c.rangeStart()
		if !c.newer {
			b = c.rangeEnd()
		}
	}
	if !c.at.isZero() {
		b = bound{c.at, false}
	}
	if e, ok := c.seen[run.containerName]; ok {
		b = bound{lineCursor{run.containerName, e}, false}
	}
	key := streamBound(b, run, c.dir())
	edge := c.nearEdge()
	switch {
	case edge.at.isZero():
		return key
	case c.newer:
		return logstore.MaxPos(key, streamBound(edge, run, c.dir()))
	}
	return logstore.MinPos(key, streamBound(edge, run, c.dir()))
}

// found is what a first page's context read m, of lines asked for, tells c: the
// side's count is the lines found. Short of lines, the read was cut, at a moment
// or at a stream skipped on a transient failure, which is answered as stop and,
// for a counted Before, where a stream with no entry goes on from; or it reached
// the log's edge. Otherwise a counted Before's start is its farthest line found,
// or Since with none.
func (c *pageCursor) found(m merge, lines int) (stop lineCursor, notices []Notice) {
	k := len(m.records)
	if k < lines {
		stop = m.cut
	}
	if !c.newer {
		c.after = k
		return stop, nil
	}
	c.before = k
	if !stop.isZero() {
		c.at = stop
		return stop, nil
	}
	c.Start = momentAt(*c.Since)
	if k > 0 {
		c.Start = lineCursorOf(m.records[0])
	}
	if k < lines {
		notices = append(notices, Notice{CodeContextShort, fmt.Sprintf("the context before since holds %d of the %d lines asked for", k, lines)})
	}
	return stop, notices
}

// trim keeps at most what remains of a counted side c moves away from while its
// edge is a count, Lines less the count, and drops the rest of m's records: they
// lie past the window, so m is no longer cut. That side's lines sort past every
// other line the page reads, so they lie at the page's far end.
func (c pageCursor) trim(m *merge) {
	s := c.away()
	if !c.counted(s) || s == sideBefore && !c.Start.isZero() {
		return
	}
	past := 0
	for _, rec := range m.records {
		if c.side(rec) == s {
			past++
		}
	}
	drop := past - (c.lines(s) - c.count(s))
	if drop <= 0 {
		return
	}
	if c.newer {
		m.records = m.records[:len(m.records)-drop]
	} else {
		m.records = m.records[drop:]
	}
	m.cut = lineCursor{}
}

// closed is whether c sits at the window's edge on the side it moves away from:
// every line of a counted side lies between it and the bound.
func (c pageCursor) closed() bool {
	s := c.away()
	return c.counted(s) && c.count(s) == c.lines(s)
}

// live is whether more lines may come past c's window's end: its end is open or
// lies after now, or a counted After is short of its Lines (rule 7).
func (c pageCursor) live(now time.Time) bool {
	if c.Window.After.Lines > 0 {
		return c.after < c.Window.After.Lines
	}
	end := c.end()
	return end.at.isZero() || end.at.Timestamp.After(now)
}

// edges is where the pages go on from after c read m: ahead, the way c reads, and
// behind, the other way, nil when the page delivered no line. m is the page as
// delivered: its records after every trim and cap, and its cut, zero when nothing
// was cut ahead. Each edge's counts are the context lines between its position
// and each bound.
//
// Going newer, each stream goes on from its reach, the last line it passed, so a
// stream whose clock runs behind loses no line; at goes on as it came. Going
// older, history does not move, so one position is exact: the cut, else the
// farthest line passed. Behind a newer page, the page's oldest line; behind an
// older one, each stream's newest line on the page, and the farthest line passed
// for a stream with none, so nothing the page showed is repeated.
func (c pageCursor) edges(m merge) (ahead pageCursor, behind *pageCursor) {
	ahead = c
	ahead.before, ahead.after = c.moved(m.records)
	var far lineCursor
	if c.newer {
		ahead.seen = c.reached(m)
	} else {
		far = farthest(older, m)
		ahead.at = cmp.Or(m.cut, far, c.at)
	}
	if len(m.records) == 0 {
		return ahead, nil
	}
	if c.newer {
		back := c.turned(lineCursorOf(m.records[0]))
		return ahead, &back
	}
	back := c.turned(far)
	back.seen = newestOnPage(m.records)
	return ahead, &back
}

// moved is c's counts once the page delivered recs: plus the context lines it
// delivered moving away from a bound, minus those moving toward one, clamped to
// 0 through each side's Lines, the backstop for a late line's one-line slip.
func (c pageCursor) moved(recs []Record) (before, after int) {
	before, after = c.before, c.after
	for _, rec := range recs {
		s := c.side(rec)
		if s == sideRange {
			continue
		}
		n := -1
		if s == c.away() {
			n = 1
		}
		if s == sideBefore {
			before += n
		} else {
			after += n
		}
	}
	return clamp(before, c.Window.Before.Lines), clamp(after, c.Window.After.Lines)
}

// clamp is n held to 0 through lines.
func clamp(n, lines int) int { return max(0, min(n, lines)) }

// reached is c's entries once m passed: each stream the page resolved at its
// reach, the later of two runs of one name standing, and each other stream at its
// incoming entry. An entry for a stream that left the scope is dropped.
func (c pageCursor) reached(m merge) map[containerName]runPlace {
	out := map[containerName]runPlace{}
	passed := map[containerName]lineCursor{}
	for _, r := range m.reaches {
		name := r.run.containerName
		if e, ok := c.seen[name]; ok {
			out[name] = e
		}
		if p := r.passed(newer, m); !p.isZero() && (passed[name].isZero() || beyond(newer, p, passed[name])) {
			passed[name] = p
		}
	}
	for name, p := range passed {
		out[name] = p.runPlace
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// passed is the farthest line of r's stream the page m passed read dir: the first
// of its stop, its last row, and its last record on the page that does not lie
// past m's cut; zero with none. A row past the cut was not passed, since another
// stream cut the page short of it.
func (r streamReach) passed(dir direction, m merge) lineCursor {
	short := func(p lineCursor) bool { return !p.isZero() && (m.cut.isZero() || !beyond(dir, p, m.cut)) }
	switch {
	case short(r.stop):
		return r.stop
	case short(r.last):
		return r.last
	}
	if p := r.lastOnPage(dir, m.records); short(p) {
		return p
	}
	return lineCursor{}
}

// lastOnPage is the farthest line of r's run among recs read dir, zero with none.
func (r streamReach) lastOnPage(dir direction, recs []Record) lineCursor {
	var out lineCursor
	for _, rec := range recs {
		c := lineCursorOf(rec)
		if c.containerName == r.run.containerName && c.ContainerID == r.run.ContainerID && (out.isZero() || beyond(dir, c, out)) {
			out = c
		}
	}
	return out
}

// farthest is the farthest line any stream of m passed read dir, zero with none.
func farthest(dir direction, m merge) lineCursor {
	var out lineCursor
	for _, r := range m.reaches {
		if p := r.passed(dir, m); !p.isZero() && (out.isZero() || beyond(dir, p, out)) {
			out = p
		}
	}
	return out
}

// newestOnPage is each stream's newest line among recs, the later of two runs of
// one name standing.
func newestOnPage(recs []Record) map[containerName]runPlace {
	newest := map[containerName]lineCursor{}
	for _, rec := range recs {
		c := lineCursorOf(rec)
		if cur, ok := newest[c.containerName]; !ok || beyond(newer, c, cur) {
			newest[c.containerName] = c
		}
	}
	out := make(map[containerName]runPlace, len(newest))
	for name, c := range newest {
		out[name] = c.runPlace
	}
	return out
}

// place is the page's place in the window once c read m, from its edges. Ahead,
// the way c reads, is a cursor when a line lies past the page that way inside
// the window; a newer page in a live window with none sets Follow instead.
// Behind is a cursor when the page was a continuation, or on a first page whose
// context read stopped short at stop, which the edge that way reads on from; an
// older first page in a live window sets Follow from it instead, the poll.
func (c pageCursor) place(m merge, ahead pageCursor, behind *pageCursor, first bool, stop lineCursor, now time.Time) Page {
	var p Page
	onward, backward := &p.Next, &p.Prev
	if !c.newer {
		onward, backward = &p.Prev, &p.Next
	}
	switch {
	case ahead.closed():
	case !m.cut.isZero():
		*onward = encoded(ahead)
	case c.newer && ahead.live(now):
		p.Follow = encoded(ahead)
	}
	switch {
	case first && !stop.isZero():
		*backward = encoded(c.turned(stop))
	case !first && behind != nil:
		*backward = encoded(*behind)
	case first && !c.newer:
		poll := c.turned(lineCursor{})
		if behind != nil {
			poll = *behind
		}
		if poll.live(now) {
			p.Follow = encoded(poll)
		}
	}
	return p
}

// encoded is c as a page cursor.
func encoded(c pageCursor) *string {
	s := encodePageCursor(c)
	return &s
}

// pageCursorVersion is the version a page cursor's JSON carries first.
const pageCursorVersion = 1

// maxPageCursorInflate bounds a page cursor's decompression, far above any cursor
// the service writes.
const maxPageCursorInflate = 16 << 20

// pageCursorJSON is a page cursor as JSON, its keys in the order written. Every
// position is a line cursor's own JSON object.
type pageCursorJSON struct {
	V      int              `json:"v"`
	Scope  Scope            `json:"s"`
	Window windowJSON       `json:"w"`
	Newer  bool             `json:"n"`
	Seen   []lineCursorJSON `json:"seen,omitempty"`
	At     *lineCursorJSON  `json:"at,omitempty"`
	Before int              `json:"b"`
	After  int              `json:"a"`
}

// windowJSON is a resolvedWindow as JSON.
type windowJSON struct {
	Since      *time.Time      `json:"since,omitempty"`
	Until      *time.Time      `json:"until,omitempty"`
	Before     paddingJSON     `json:"before"`
	After      paddingJSON     `json:"after"`
	ResolvedAt time.Time       `json:"resolvedAt"`
	Start      *lineCursorJSON `json:"start,omitempty"`
}

// paddingJSON is a Padding as JSON, its span in nanoseconds.
type paddingJSON struct {
	Lines int           `json:"lines,omitempty"`
	Span  time.Duration `json:"span,omitempty"`
}

// errBadPageCursor refuses a page cursor this service did not write.
var errBadPageCursor = refuse(CodeCursorInvalid, "this is not a page cursor a page handed back")

// deflaters reuses flate writers: each holds several hundred KB of state, and a
// page encodes up to three cursors.
var deflaters = sync.Pool{New: func() any {
	w, _ := flate.NewWriter(nil, flate.BestCompression) // a valid level never fails
	return w
}}

// encodePageCursor is c as a page cursor: its JSON, deflated, base64url with no
// padding. The entries are written in name order and the bounds in UTC, so one
// page has one string.
func encodePageCursor(c pageCursor) string {
	j := pageCursorJSON{
		V: pageCursorVersion, Scope: c.Scope, Newer: c.newer, Before: c.before, After: c.after,
		Window: windowJSON{
			Since: inUTC(c.Since), Until: inUTC(c.Until), ResolvedAt: c.ResolvedAt.UTC(),
			Before: paddingJSON(c.Window.Before), After: paddingJSON(c.Window.After),
			Start: optionalJSON(c.Start),
		},
		At: optionalJSON(c.at),
	}
	for name, place := range c.seen {
		j.Seen = append(j.Seen, lineCursor{name, place}.toJSON())
	}
	slices.SortFunc(j.Seen, func(a, b lineCursorJSON) int {
		return compareNames(containerName{a.Namespace, a.Pod, a.Container}, containerName{b.Namespace, b.Pod, b.Container})
	})
	raw, err := json.Marshal(j)
	if err != nil {
		panic(err) // plain values always marshal
	}
	var b bytes.Buffer
	w := deflaters.Get().(*flate.Writer)
	defer deflaters.Put(w)
	w.Reset(&b)
	_, _ = w.Write(raw) // a bytes.Buffer never fails
	_ = w.Close()
	return base64.RawURLEncoding.EncodeToString(b.Bytes())
}

// inUTC is t in UTC, nil for nil.
func inUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// optionalJSON is c's JSON, nil when c is zero.
func optionalJSON(c lineCursor) *lineCursorJSON {
	if c.isZero() {
		return nil
	}
	j := c.toJSON()
	return &j
}

// decodePageCursor is the page cursor s spells, checked as a parsed request is.
// Its scope is not checked here: the read resolves it, and keeps that refusal's
// code.
func decodePageCursor(s string) (pageCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return pageCursor{}, errBadPageCursor
	}
	raw, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(b)), maxPageCursorInflate+1))
	if err != nil || len(raw) > maxPageCursorInflate {
		return pageCursor{}, errBadPageCursor
	}
	var j pageCursorJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return pageCursor{}, errBadPageCursor
	}
	if j.V != pageCursorVersion {
		return pageCursor{}, errCursorVersion
	}
	c := pageCursor{
		Scope: j.Scope, newer: j.Newer, before: j.Before, after: j.After,
		resolvedWindow: resolvedWindow{
			Window: Window{
				Since: inUTC(j.Window.Since), Until: inUTC(j.Window.Until),
				Before: Padding(j.Window.Before), After: Padding(j.Window.After),
			},
			ResolvedAt: j.Window.ResolvedAt.UTC(),
		},
	}
	if c.Start, err = fromOptionalJSON(j.Window.Start); err != nil {
		return pageCursor{}, err
	}
	if c.at, err = fromOptionalJSON(j.At); err != nil {
		return pageCursor{}, err
	}
	if len(j.Seen) > 0 {
		c.seen = make(map[containerName]runPlace, len(j.Seen))
	}
	for _, e := range j.Seen {
		line, err := e.lineCursor()
		if err != nil {
			return pageCursor{}, err
		}
		if line.containerName == (containerName{}) {
			return pageCursor{}, errBadPageCursor
		}
		c.seen[line.containerName] = line.runPlace
	}
	if err := c.check(); err != nil {
		return pageCursor{}, err
	}
	return c, nil
}

// fromOptionalJSON is the line cursor j spells, zero when j is nil.
func fromOptionalJSON(j *lineCursorJSON) (lineCursor, error) {
	if j == nil {
		return lineCursor{}, nil
	}
	return j.lineCursor()
}

// check refuses a decoded cursor no page writes: an older one with an entry, a
// count outside 0 to its Lines, a Start before an open Since with no count, or a
// window refused.
func (c pageCursor) check() error {
	countOK := func(n int, p Padding) bool { return n >= 0 && n <= p.Lines }
	switch {
	case c.ResolvedAt.IsZero(),
		!c.newer && len(c.seen) > 0,
		!countOK(c.before, c.Window.Before), !countOK(c.after, c.Window.After),
		c.Since == nil && c.Window.Before.Lines == 0 && !c.Start.isZero(),
		checkWindow(c.Window) != nil:
		return errBadPageCursor
	}
	return nil
}
