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
	"cmp"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// position is a line's place in a view's order: its moment, then its container,
// then its ordinal, its index among its instance's lines at that moment, then
// the runtime id of the instance it came from. Every line has one and no two
// share one, so a page can end at any line and the next can start just past it.
// A position with no container is a moment: it stands before, or after, every
// line of that moment at once.
type position struct {
	Timestamp   time.Time
	Namespace   string
	Pod         string
	Container   string
	ContainerID string
	Ordinal     int
}

// isZero is whether p names nothing.
func (p position) isZero() bool { return p.Timestamp.IsZero() }

// moment is whether p names a moment rather than a line.
func (p position) moment() bool { return p.Container == "" }

// positionOf is rec's position.
func positionOf(rec Record) position {
	return position{rec.Timestamp, rec.Stream.Namespace, rec.Stream.Pod, rec.Stream.Container, rec.ContainerID, rec.Ordinal}
}

// comparePositions orders a against b: by time, then, unless either is a moment,
// by namespace, pod, container, ordinal and instance id. The ordinal goes before
// the id because the id is the resolve's, which lags a restart: a page can end on
// a line keyed with the old id that the next page keys with the new one, and
// with the id deciding the tie, the new instance's later lines at that moment
// would all sort before the cursor and be lost.
func comparePositions(a, b position) int {
	if c := a.Timestamp.Compare(b.Timestamp); c != 0 || a.moment() || b.moment() {
		return c
	}
	return cmp.Or(
		cmp.Compare(a.Namespace, b.Namespace),
		cmp.Compare(a.Pod, b.Pod),
		cmp.Compare(a.Container, b.Container),
		cmp.Compare(a.Ordinal, b.Ordinal),
		cmp.Compare(a.ContainerID, b.ContainerID),
	)
}

// compare orders rec against p.
func compare(rec Record, p position) int { return comparePositions(positionOf(rec), p) }

// CompareLines orders two lines as their cursors do: by moment, then, at one
// moment, by namespace, pod, container, ordinal and instance id.
func CompareLines(a, b Record) int { return comparePositions(positionOf(a), positionOf(b)) }

// cursorOf is p as a cursor: its time with nanoseconds, then, for a line, its
// container, instance id and ordinal, each field behind a tab, which no name
// holds.
func cursorOf(p position) *string {
	c := p.Timestamp.UTC().Format(time.RFC3339Nano)
	if !p.moment() {
		c += "\t" + p.Namespace + "\t" + p.Pod + "\t" + p.Container + "\t" + p.ContainerID + "\t" + strconv.Itoa(p.Ordinal)
	}
	return &c
}

// errBadCursor refuses a cursor that names neither a moment nor a line.
var errBadCursor = Refusal{"a cursor must be an RFC 3339 time, or one a page handed back"}

// parseCursor is the position a cursor names: a time alone is a moment, as the
// viewer's At anchor sends one.
func parseCursor(c string) (position, error) {
	fields := strings.Split(c, "\t")
	t, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil || len(fields) != 1 && len(fields) != 6 {
		return position{}, errBadCursor
	}
	p := position{Timestamp: t}
	if len(fields) == 1 {
		return p, nil
	}
	p.Namespace, p.Pod, p.Container, p.ContainerID = fields[1], fields[2], fields[3], fields[4]
	// streamBound steps one past the ordinal, so MaxInt has no position past it.
	if p.Ordinal, err = strconv.Atoi(fields[5]); err != nil || p.Ordinal < 0 || p.Ordinal == math.MaxInt || p.Container == "" || !validContainerID(p.ContainerID) {
		return position{}, errBadCursor
	}
	return p, nil
}

// LineCursor is the cursor a page hands back for the line at moment, of namespace,
// pod and container's instance containerID, the ordinal-th of its moment: for a
// caller that spells a line's cursor its own way. It refuses what a parsed cursor
// refuses, and a name holding a tab, which would split it.
func LineCursor(moment time.Time, namespace, pod, container, containerID string, ordinal int) (string, error) {
	if moment.IsZero() || namespace == "" || pod == "" || container == "" || strings.ContainsFunc(namespace+pod+container, unicode.IsControl) {
		return "", errBadCursor
	}
	c := cursorOf(position{moment, namespace, pod, container, containerID, ordinal})
	if _, err := parseCursor(*c); err != nil {
		return "", err
	}
	return *c, nil
}

// maxContainerID is the longest runtime id a position takes.
const maxContainerID = 256

// validContainerID is whether id is one a position takes: what the resolve
// guarantees of every id it hands out, and nothing more, since a runtime spells
// its ids as it likes. A tab would split a cursor.
func validContainerID(id string) bool {
	return id != "" && len(id) <= maxContainerID && !strings.ContainsFunc(id, unicode.IsControl)
}

// boundAfter is the bound a read past the cursor after reads from, the cursor's own
// line dropped; zero with none.
func boundAfter(after *string) (bound, error) {
	if after == nil {
		return bound{}, nil
	}
	p, err := parseCursor(*after)
	return bound{p, false}, err
}

// boundIn is the bound a range's end names, zero with none: a moment's lines are in
// the range, a line is not.
func boundIn(c *string) (bound, error) {
	if c == nil {
		return bound{}, nil
	}
	p, err := parseCursor(*c)
	return bound{p, p.moment()}, err
}

// bound is one end of a read's range: a position, and whether a line at it is in
// range. A zero bound is no bound.
type bound struct {
	at   position
	incl bool
}

// below is whether rec is under the lower bound b.
func (b bound) below(rec Record) bool {
	if b.at.isZero() {
		return false
	}
	c := compare(rec, b.at)
	return c < 0 || c == 0 && !b.incl
}

// above is whether rec is over the upper bound b.
func (b bound) above(rec Record) bool {
	if b.at.isZero() {
		return false
	}
	c := compare(rec, b.at)
	return c > 0 || c == 0 && !b.incl
}

// instance is what orders one stream's lines against another's at one moment: its
// namespace, pod and container, then the runtime id of the instance.
type instance struct{ namespace, pod, container, id string }

// streamBound is where a page reading mode from b starts in one stream, as a
// position of that stream alone: a Head page reads the stream's lines after it, a
// Tail page those before it, and with a zero b the page reads from the start or
// the end. It is the one cursor across the view read as comparePositions orders
// lines, so the pages of one view join in every stream.
func streamBound(b bound, in instance, mode FetchMode) logstore.Pos {
	head := mode == FetchHead
	if b.at.isZero() {
		if head {
			return logstore.Oldest
		}
		return logstore.Newest
	}
	moment := b.at.Timestamp.UnixNano()
	at := func(ord int) logstore.Pos { return logstore.Pos{TS: moment, Ord: ord} }
	// every and none read every line of the moment or none of it.
	every, none := logstore.Pos{TS: moment, Ord: -1}, logstore.Pos{TS: moment, Ord: math.MaxInt}
	if !head {
		every, none = none, every
	}
	if b.at.moment() {
		if b.incl {
			return every
		}
		return none
	}
	names := cmp.Or(cmp.Compare(in.namespace, b.at.Namespace), cmp.Compare(in.pod, b.at.Pod), cmp.Compare(in.container, b.at.Container))
	switch {
	case names < 0 && head, names > 0 && !head:
		return none
	case names != 0:
		return every
	}
	// The ordinal decides before the id, so the stream's line at the cursor's ordinal
	// is the page's when the id puts it on the page's side, or when it is the
	// cursor's own line and the bound keeps it.
	ord := b.at.Ordinal
	id := cmp.Compare(in.id, b.at.ContainerID)
	switch {
	case id == 0 && !b.incl, head && id < 0, !head && id > 0:
		return at(ord)
	case head:
		return at(ord - 1)
	}
	return at(ord + 1)
}

// boundOf is p, the exclusive bound of a walk of in's stream, as a bound across the
// view: before every line of a moment, after every line of it, or past a line.
func boundOf(in instance, p logstore.Pos) bound {
	at := time.Unix(0, p.TS).UTC()
	switch {
	case p.Ord < 0:
		return bound{position{Timestamp: at}, true}
	case p.Ord == math.MaxInt:
		return bound{position{Timestamp: at}, false}
	}
	return bound{positionAt(in, p), false}
}

// compareBounds orders two lower bounds by where the lines past them start: by
// moment, then before every line of it, at a line, or after every line of it, then
// between two lines by position.
func compareBounds(a, b bound) int {
	return cmp.Or(a.at.Timestamp.Compare(b.at.Timestamp), cmp.Compare(boundRank(a), boundRank(b)), comparePositions(a.at, b.at))
}

// boundRank is where b falls among the bounds at its moment: before every line, at a
// line, or after every line.
func boundRank(b bound) int {
	switch {
	case !b.at.moment():
		return 0
	case b.incl:
		return -1
	}
	return 1
}

// cursorOfBound is the cursor a read past which starts where b does. A cursor reads
// past what it names, so a bound that keeps its moment names the moment before.
func cursorOfBound(b bound) *string {
	if b.at.moment() && b.incl {
		return cursorOf(position{Timestamp: b.at.Timestamp.Add(-time.Nanosecond)})
	}
	return cursorOf(b.at)
}
