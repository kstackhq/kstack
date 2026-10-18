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
	"testing"
	"time"

	_ "time/tzdata" // the DST case's zone, on every runner

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// now is the clock every case reads a duration on: 15 March 2027, 12:00 UTC.
var now = time.Date(2027, 3, 15, 12, 0, 0, 0, time.UTC)

// An RFC 3339 time is the moment it names, in UTC, whichever way the field runs.
func TestATimeIsReturnedInUTC(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 10, 18, 8, 14, 20, 5e8, time.UTC)
	for _, dir := range []Direction{Behind, Ahead} {
		got, ok := MomentOf("2026-10-18T09:14:20.5+01:00", now, dir)
		assert.True(t, ok)
		assert.Equal(t, want, got)
		assert.Equal(t, time.UTC, got.Location())
	}
}

// whole is each duration in whole units and how far it runs.
var whole = map[string]time.Duration{
	"P1W":     7 * 24 * time.Hour,
	"P3D":     3 * 24 * time.Hour,
	"PT15M":   15 * time.Minute,
	"PT1H30M": 90 * time.Minute,
	"P1DT12H": 36 * time.Hour,
	"P1W3D":   10 * 24 * time.Hour,
}

// A duration Behind is that far back from now.
func TestADurationBehindIsBackFromNow(t *testing.T) {
	t.Parallel()
	for s, d := range whole {
		got, ok := MomentOf(s, now, Behind)
		assert.True(t, ok, s)
		assert.Equal(t, now.Add(-d), got, s)
	}
}

// A duration Ahead is that far on from now.
func TestADurationAheadIsOnFromNow(t *testing.T) {
	t.Parallel()
	for s, d := range whole {
		got, ok := MomentOf(s, now, Ahead)
		assert.True(t, ok, s)
		assert.Equal(t, now.Add(d), got, s)
	}
}

// A fraction of a second reads as its digits padded to nanoseconds.
func TestAFractionOfASecondIsExact(t *testing.T) {
	t.Parallel()
	for s, d := range map[string]time.Duration{
		"PT1.5S":         1500 * time.Millisecond,
		"PT0.001S":       time.Millisecond,
		"PT0.123456789S": 123456789,
		"PT1M0.5S":       time.Minute + 500*time.Millisecond,
	} {
		got, ok := MomentOf(s, now, Behind)
		assert.True(t, ok, s)
		assert.Equal(t, now.Add(-d), got, s)
	}
}

// day is midnight UTC on the date.
func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// Years and months move on the calendar, and a day past the end of the target
// month is that month's last day. Days are applied after the clamp.
func TestAMonthIsOnTheCalendar(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		s        string
		from, to time.Time
	}{
		{"P1M", day(2027, 3, 15), day(2027, 2, 15)},
		{"P1M", day(2027, 3, 31), day(2027, 2, 28)},
		{"P1M", day(2028, 3, 31), day(2028, 2, 29)},
		{"P1Y", day(2028, 2, 29), day(2027, 2, 28)},
		{"P1D", day(2027, 3, 31), day(2027, 3, 30)},
		{"P1M3D", day(2027, 3, 31), day(2027, 2, 25)},
	} {
		got, ok := MomentOf(c.s, c.from, Behind)
		assert.True(t, ok, c.s)
		assert.Equal(t, c.to, got, "%s from %s", c.s, c.from)
	}
	got, _ := MomentOf("P1M", day(2027, 1, 31), Ahead)
	assert.Equal(t, day(2027, 2, 28), got, "ahead clamps too")
}

// A day is 24 hours on a clock in a zone whose day that is not: the maths runs
// in UTC.
func TestADayIsADayAcrossDaylightSaving(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	// New York's clocks went forward at 2am on 14 March 2027, a 23-hour day.
	local := time.Date(2027, 3, 14, 12, 0, 0, 0, ny)

	got, ok := MomentOf("P1D", local, Behind)
	assert.True(t, ok)
	assert.Equal(t, local.UTC().Add(-24*time.Hour), got)
	assert.Equal(t, time.UTC, got.Location())
}

// What is neither a time nor an ISO 8601 duration naming a moment is refused: Go's
// durations, a sign, no unit, a T with no time unit, every unit zero, units out of
// order or twice, and a fraction outside seconds.
func TestNotAMomentIsRefused(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"", "15m", "-PT15M", "P", "PT", "P1DT", "PT0S", "P0D", "PT5M1H", "PT1M2M",
		"P1.5D", "PT1.5H", "P1.5DT1H", "PT1234567890S", "yesterday",
	} {
		for _, dir := range []Direction{Behind, Ahead} {
			_, ok := MomentOf(s, now, dir)
			assert.False(t, ok, s)
		}
	}
}

// A time part past what a time.Duration holds is refused rather than wrapped, as
// is a moment before the epoch going back or after 9999 going ahead.
func TestAMomentOutOfBoundsIsRefused(t *testing.T) {
	t.Parallel()
	nearEpoch := day(1970, 1, 2)
	near9999 := day(9999, 12, 30)
	for _, c := range []struct {
		s   string
		now time.Time
		dir Direction
	}{
		{"PT999999999H", now, Behind},
		{"PT999999999M", now, Ahead},
		{"P999999999Y", nearEpoch, Behind},
		{"P2D", nearEpoch, Behind},
		{"P999999999Y", near9999, Ahead},
		{"P2D", near9999, Ahead},
	} {
		_, ok := MomentOf(c.s, c.now, c.dir)
		assert.False(t, ok, "%s from %s", c.s, c.now)
	}

	got, ok := MomentOf("P1D", nearEpoch, Behind)
	assert.True(t, ok)
	assert.Equal(t, time.Unix(0, 0).UTC(), got, "the epoch itself is in bounds")
	got, ok = MomentOf("PT999999S", now, Behind)
	assert.True(t, ok)
	assert.Equal(t, now.Add(-999999*time.Second), got)
}
