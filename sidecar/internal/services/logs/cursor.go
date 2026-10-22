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
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// containerName is a container by name, which a restart does not change.
type containerName struct {
	Namespace, Pod, Container string
}

// runPlace is where a line sits within its container's lines: the runtime id
// the kubelet gave the run, so a restarted container's lines are told from the
// run before, and the line's moment and ordinal among the run's lines at that
// moment. A page cursor's seen holds one per container.
type runPlace struct {
	ContainerID string
	Timestamp   time.Time
	Ordinal     int
}

// lineCursor is a line's place in the scope's order: its container, and its
// place within it. Every line has one and no two share one, so a page can end
// at any line and the next can start just past it. One with no ContainerID is
// a moment, which stands before, or after, every line of that moment at once,
// a form only a page cursor holds; one with a zero Timestamp names nothing.
type lineCursor struct {
	containerName
	runPlace
}

// momentAt is the line cursor of the moment t.
func momentAt(t time.Time) lineCursor { return lineCursor{runPlace: runPlace{Timestamp: t}} }

// isZero is whether c names nothing.
func (c lineCursor) isZero() bool { return c.Timestamp.IsZero() }

// moment is whether c names a moment rather than a line.
func (c lineCursor) moment() bool { return c.ContainerID == "" }

// at is the line cursor of the line at p of c's run.
func (c lineCursor) at(p logstore.Pos) lineCursor {
	return lineCursor{c.containerName, runPlace{c.ContainerID, time.Unix(0, p.TS).UTC(), p.Ord}}
}

// lineCursorOf is rec's line cursor.
func lineCursorOf(rec Record) lineCursor {
	return lineCursor{
		containerName{rec.Stream.Namespace, rec.Stream.Pod, rec.Stream.Container},
		runPlace{rec.ContainerID, rec.Timestamp, rec.Ordinal},
	}
}

// compareNames orders two containers by namespace, pod and container.
func compareNames(a, b containerName) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Pod, b.Pod), cmp.Compare(a.Container, b.Container))
}

// compareLineCursors orders a against b: by time, then, unless either is a moment,
// by namespace, pod, container, ordinal and instance id. The ordinal goes before
// the id because the id is the resolve's, which lags a restart: a page can end on
// a line keyed with the old id that the next page keys with the new one, and
// with the id deciding the tie, the new instance's later lines at that moment
// would all sort before the cursor and be lost.
func compareLineCursors(a, b lineCursor) int {
	if c := a.Timestamp.Compare(b.Timestamp); c != 0 || a.moment() || b.moment() {
		return c
	}
	return cmp.Or(
		compareNames(a.containerName, b.containerName),
		cmp.Compare(a.Ordinal, b.Ordinal),
		cmp.Compare(a.ContainerID, b.ContainerID),
	)
}

// compare orders rec against p.
func compare(rec Record, p lineCursor) int { return compareLineCursors(lineCursorOf(rec), p) }

// CompareLines orders two lines as their cursors do: by moment, then, at one
// moment, by namespace, pod, container, ordinal and instance id.
func CompareLines(a, b Record) int { return compareLineCursors(lineCursorOf(a), lineCursorOf(b)) }

// lineCursorVersion is the version a line cursor's JSON carries first.
const lineCursorVersion = 1

// lineCursorJSON is a line cursor as JSON, its keys in the order written: a
// line's every field, a container's moment its names and time, a moment its
// time alone.
type lineCursorJSON struct {
	V         int       `json:"v"`
	Namespace string    `json:"ns,omitempty"`
	Pod       string    `json:"pod,omitempty"`
	Container string    `json:"c,omitempty"`
	ID        string    `json:"id,omitempty"`
	Time      time.Time `json:"t"`
	Ordinal   *int      `json:"n,omitempty"`
}

// toJSON is c as its JSON object.
func (c lineCursor) toJSON() lineCursorJSON {
	j := lineCursorJSON{V: lineCursorVersion, Namespace: c.Namespace, Pod: c.Pod, Container: c.Container, Time: c.Timestamp.UTC()}
	if !c.moment() {
		j.ID, j.Ordinal = c.ContainerID, &c.Ordinal
	}
	return j
}

// lineCursor is j checked: a moment, a container's moment, or a line.
func (j lineCursorJSON) lineCursor() (lineCursor, error) {
	if j.V != lineCursorVersion {
		return lineCursor{}, errCursorVersion
	}
	names := j.Namespace != "" || j.Pod != "" || j.Container != ""
	switch {
	case j.Time.IsZero(),
		names && !(validName(j.Namespace) && validName(j.Pod) && validName(j.Container)),
		(j.ID == "") != (j.Ordinal == nil),
		j.Ordinal != nil && (!names || !validContainerID(j.ID) || *j.Ordinal < 0 || *j.Ordinal == math.MaxInt):
		// streamBound steps one past the ordinal, so MaxInt has no cursor past it.
		return lineCursor{}, errBadCursor
	}
	c := lineCursor{containerName{j.Namespace, j.Pod, j.Container}, runPlace{ContainerID: j.ID, Timestamp: j.Time.UTC()}}
	if j.Ordinal != nil {
		c.Ordinal = *j.Ordinal
	}
	return c, nil
}

// encodeLineCursor is c as a line cursor: its JSON, base64url with no padding.
func encodeLineCursor(c lineCursor) string {
	b, err := json.Marshal(c.toJSON())
	if err != nil {
		panic(err) // a struct of strings, a time and an int always marshals
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

var (
	// errBadCursor refuses a line cursor this service did not write.
	errBadCursor = refuse(CodeCursorInvalid, "this is not a line cursor a page handed back")
	// errCursorVersion refuses a cursor another version of the service wrote.
	errCursorVersion = refuse(CodeCursorInvalid, "this cursor is from another version of Kstack; read again without it")
)

// decodeLineCursor is the line cursor s spells.
func decodeLineCursor(s string) (lineCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return lineCursor{}, errBadCursor
	}
	var j lineCursorJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return lineCursor{}, errBadCursor
	}
	return j.lineCursor()
}

// maxName is the longest namespace, pod or container name.
const maxName = 253

// validName is whether name is one Kubernetes takes for a namespace, a pod or a
// container: lower-case letters, digits, '-' and '.'.
func validName(name string) bool {
	if name == "" || len(name) > maxName {
		return false
	}
	for _, r := range name {
		if !('a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// maxContainerID is the longest runtime id a line cursor takes.
const maxContainerID = 256

// validContainerID is whether id is one a line cursor takes: what the resolve
// guarantees of every id it hands out, and nothing more, since a runtime spells
// its ids as it likes.
func validContainerID(id string) bool {
	return id != "" && len(id) <= maxContainerID && !strings.ContainsFunc(id, unicode.IsControl)
}

// boundAfter is the bound a read past the cursor after reads from, the cursor's own
// line dropped; zero with none.
func boundAfter(after *string) (bound, error) {
	if after == nil {
		return bound{}, nil
	}
	p, err := decodeLineCursor(*after)
	return bound{p, false}, err
}

// bound is one end of a read's range: a position, and whether a line at it is in
// range. A zero bound is no bound.
type bound struct {
	at   lineCursor
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

// streamBound is where a page reading mode from b starts in run's stream, as a
// position of that stream alone: a newer page reads the stream's lines after it,
// an older page those before it, and with a zero b the page reads from the start
// or the end. It is one line cursor across the scope read as compareLineCursors
// orders lines, so the pages of one scope join in every stream.
func streamBound(b bound, run lineCursor, mode direction) logstore.Pos {
	head := mode == newer
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
	names := compareNames(run.containerName, b.at.containerName)
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
	id := cmp.Compare(run.ContainerID, b.at.ContainerID)
	switch {
	case id == 0 && !b.incl, head && id < 0, !head && id > 0:
		return at(ord)
	case head:
		return at(ord - 1)
	}
	return at(ord + 1)
}

// boundOf is p, the exclusive bound of a walk of run's stream, as a bound across
// the scope: before every line of a moment, after every line of it, or past a line.
func boundOf(run lineCursor, p logstore.Pos) bound {
	at := time.Unix(0, p.TS).UTC()
	switch {
	case p.Ord < 0:
		return bound{momentAt(at), true}
	case p.Ord == math.MaxInt:
		return bound{momentAt(at), false}
	}
	return bound{run.at(p), false}
}

// compareBounds orders two lower bounds by where the lines past them start: by
// moment, then before every line of it, at a line, or after every line of it, then
// between two lines by position.
func compareBounds(a, b bound) int {
	return cmp.Or(a.at.Timestamp.Compare(b.at.Timestamp), cmp.Compare(boundRank(a), boundRank(b)), compareLineCursors(a.at, b.at))
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
func cursorOfBound(b bound) string {
	if b.at.moment() && b.incl {
		return encodeLineCursor(momentAt(b.at.Timestamp.Add(-time.Nanosecond)))
	}
	return encodeLineCursor(b.at)
}
