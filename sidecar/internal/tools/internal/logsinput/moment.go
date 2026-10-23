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

package logsinput

import (
	"math"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/iso8601"
)

// Direction is the way a duration runs from now, which is the field's to say: a
// duration has no sign of its own.
type Direction int

const (
	Behind Direction = iota
	Ahead
)

// The moments a duration can name lie between the epoch and the end of 9999; a
// log holds none outside them.
var (
	epoch = time.Unix(0, 0).UTC()
	last  = time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
)

// MomentOf is the moment s names on the clock now: an RFC 3339 time, or an ISO
// 8601 duration from now in the direction dir; false for neither. A duration is
// applied in UTC, years and months first on the calendar, then weeks and days,
// then the time part.
func MomentOf(s string, now time.Time, dir Direction) (time.Time, bool) {
	if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return at.UTC(), true
	}
	p, err := iso8601.Parse(s)
	if err != nil {
		return time.Time{}, false
	}
	// The time part alone is a fixed length. Uncapped here: one a Duration cannot
	// hold lands outside the range below anyway.
	clock, err := iso8601.Span(iso8601.Parts{Hours: p.Hours, Minutes: p.Minutes, Seconds: p.Seconds, Nanos: p.Nanos}, math.MaxInt64)
	if err != nil {
		return time.Time{}, false
	}
	sign := -1
	if dir == Ahead {
		sign = 1
	}
	at := calendar(now.UTC(), sign*int(p.Years), sign*int(p.Months))
	at = at.AddDate(0, 0, sign*int(7*p.Weeks+p.Days))
	at = at.Add(time.Duration(sign) * clock)
	if at.Before(epoch) || at.After(last) {
		return time.Time{}, false
	}
	return at, true
}

// calendar is t moved by years and months, a day past the end of the target
// month clamped to its last day: AddDate would roll 31 March back to 3 March.
func calendar(t time.Time, years, months int) time.Time {
	at := t.AddDate(years, months, 0)
	if at.Day() != t.Day() {
		// The day overflowed into the next month; step back to the target's last day.
		at = at.AddDate(0, 0, -at.Day())
	}
	return at
}
