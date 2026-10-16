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
	"strconv"
	"strings"
	"time"
	"unicode"
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

// compareLines orders two lines.
func compareLines(a, b Record) int { return comparePositions(positionOf(a), positionOf(b)) }

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
	if p.Ordinal, err = strconv.Atoi(fields[5]); err != nil || p.Ordinal < 0 || p.Container == "" || !validContainerID(p.ContainerID) {
		return position{}, errBadCursor
	}
	return p, nil
}

// maxContainerID is the longest runtime id a position takes.
const maxContainerID = 256

// validContainerID is whether id is one a position takes: what the resolve
// guarantees of every id it hands out, and nothing more, since a runtime spells
// its ids as it likes. A tab would split a cursor.
func validContainerID(id string) bool {
	return id != "" && len(id) <= maxContainerID && !strings.ContainsFunc(id, unicode.IsControl)
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
