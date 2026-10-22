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
	"fmt"
	"time"
)

// checkWindow refuses a window whose bounds cross, or whose padding is past its
// limits or names both a count and a span. Padding changes what the window means,
// so it is refused, never clamped.
func checkWindow(w Window) error {
	if w.Since != nil && w.Until != nil && w.Since.After(*w.Until) {
		return refuse(CodeInvalidRange, "since is after until")
	}
	for _, p := range []Padding{w.Before, w.After} {
		switch {
		case p.Lines != 0 && p.Span != 0:
			return refuse(CodeConflictingParameters, "padding is a number of lines or a span of time, not both")
		case p.Lines < 0 || p.Lines > MaxPageSize:
			return refuse(CodeBadPadding, fmt.Sprintf("padding is between 0 and %d lines", MaxPageSize))
		case p.Span < 0 || p.Span > MaxContextSpan:
			return refuse(CodeBadPadding, fmt.Sprintf("padding is a span between 0 and %s", MaxContextSpan))
		}
	}
	return nil
}

// resolvedWindow is a Window as the first page pinned it: the Window;
// ResolvedAt, the first page's clock, which an open Until's After is measured
// from on every page, so the window's end does not slide from page to page; and
// Start, the window's start as an inclusive position once it is known: zero for
// an open Since with no Before, the moment a time or a span names, and the
// farthest line a counted Before's read found. A stream no page has seen reads
// from it. Only a page cursor holds one.
type resolvedWindow struct {
	Window
	ResolvedAt time.Time
	Start      lineCursor
}

// side is which part of a window a line lies in: the range, or the context
// before or after it.
type side int

const (
	sideRange side = iota
	sideBefore
	sideAfter
)

// side is the part of w rec lies in by its timestamp. Lines past an open Until
// are context only when After is set; with none the range has no end.
func (w resolvedWindow) side(rec Record) side {
	switch {
	case w.Since != nil && rec.Timestamp.Before(*w.Since):
		return sideBefore
	case w.After != (Padding{}) && w.rangeEnd().above(rec):
		return sideAfter
	}
	return sideRange
}

// padding is s's padding.
func (w resolvedWindow) padding(s side) Padding {
	if s == sideBefore {
		return w.Before
	}
	return w.After
}

// counted is whether s's padding is a count of lines. Before an open Since there
// is no context, so no count.
func (w resolvedWindow) counted(s side) bool {
	return w.padding(s).Lines > 0 && (s != sideBefore || w.Since != nil)
}

// lines is the count of s's padding.
func (w resolvedWindow) lines(s side) int { return w.padding(s).Lines }

// start is the window's start, inclusive, zero while it is open or a count's
// edge not yet found.
func (w resolvedWindow) start() bound {
	if w.Start.isZero() {
		return bound{}
	}
	return bound{w.Start, true}
}

// end is the window's end, inclusive, zero while it is open or counted: Until, or
// for an open Until the first page's clock, widened by a span.
func (w resolvedWindow) end() bound {
	switch {
	case w.After.Lines > 0, w.Until == nil && w.After.Span == 0:
		return bound{}
	case w.Until == nil:
		return bound{momentAt(w.ResolvedAt.Add(w.After.Span)), true}
	}
	return bound{momentAt(w.Until.Add(w.After.Span)), true}
}

// rangeStart is the range's start, inclusive, zero when Since is open.
func (w resolvedWindow) rangeStart() bound {
	if w.Since == nil {
		return bound{}
	}
	return bound{momentAt(*w.Since), true}
}

// rangeEnd is the range's end, inclusive: Until, or with Until open and After set
// the nanosecond before the first page's clock, since After counts the lines at
// it and past it. Zero when the range has no end.
func (w resolvedWindow) rangeEnd() bound {
	switch {
	case w.Until != nil:
		return bound{momentAt(*w.Until), true}
	case w.After != (Padding{}):
		return bound{momentAt(w.ResolvedAt.Add(-time.Nanosecond)), true}
	}
	return bound{}
}

// afterIsEmpty is whether w pads an open Until and none of recs lies in that
// padding, at or past the first page's clock.
func (w resolvedWindow) afterIsEmpty(recs []Record) bool {
	if w.Until != nil || w.After == (Padding{}) {
		return false
	}
	for _, rec := range recs {
		if !rec.Timestamp.Before(w.ResolvedAt) {
			return false
		}
	}
	return true
}
