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
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Direction is the way a duration runs from now, which is the field's to say: a
// duration has no sign of its own.
type Direction int

const (
	Behind Direction = iota
	Ahead
)

// duration is an ISO 8601 duration: each unit at most once, in the standard's
// order, a fraction in seconds alone. Every count has at most nine digits. What
// it cannot check, MomentOf does: a T has a time unit after it, and some unit is
// not zero.
var duration = regexp.MustCompile(`^P(?:(\d{1,9})Y)?(?:(\d{1,9})M)?(?:(\d{1,9})W)?(?:(\d{1,9})D)?(?:T(?:(\d{1,9})H)?(?:(\d{1,9})M)?(?:(\d{1,9})(?:\.(\d{1,9}))?S)?)?$`)

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
	m := duration.FindStringSubmatch(s)
	if m == nil || strings.Contains(s, "T") && m[5]+m[6]+m[7] == "" {
		return time.Time{}, false
	}
	var n [8]int64
	for i, v := range m[1:8] {
		n[i+1], _ = strconv.ParseInt(v, 10, 64) // nine digits at most, or "" for 0
	}
	years, months, weeks, days, hours, minutes, seconds := n[1], n[2], n[3], n[4], n[5], n[6], n[7]
	// The fraction's digits are padded on the right, so the 5 of 1.5 is 500,000,000 ns.
	nanos, _ := strconv.ParseInt((m[8] + "000000000")[:9], 10, 64)
	if years+months+weeks+days+hours+minutes+seconds+nanos == 0 {
		return time.Time{}, false
	}
	// Summed in seconds, nine-digit counts cannot overflow; as a time.Duration, which
	// holds about 292 years, they would wrap silently.
	secs := hours*3600 + minutes*60 + seconds
	if secs > (math.MaxInt64-nanos)/int64(time.Second) {
		return time.Time{}, false
	}
	sign := int64(-1)
	if dir == Ahead {
		sign = 1
	}
	at := calendar(now.UTC(), int(sign*years), int(sign*months))
	at = at.AddDate(0, 0, int(sign*(7*weeks+days)))
	at = at.Add(time.Duration(sign * (secs*int64(time.Second) + nanos)))
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
