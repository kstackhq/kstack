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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A line's cursor names it back; a time alone names a moment.
func TestACursorNamesALineOrAMoment(t *testing.T) {
	key := StreamKey{Namespace: "prod", Pod: "web-1", Container: "app", Previous: true}
	p := lineAt(key, 1500*time.Millisecond, 3)

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
	rec := Record{Timestamp: at(time.Second), Message: "hello", Stream: StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}, Ordinal: 2}

	p, err := parseCursor(rec.Cursor())

	require.NoError(t, err)
	assert.Equal(t, positionOf(rec), p)
}

// A cursor that is neither is refused.
func TestABadCursorIsRefused(t *testing.T) {
	for _, c := range []string{"yesterday", "2026-10-08T10:00:01Z\tprod", "2026-10-08T10:00:01Z\tprod\tweb\tapp\tmaybe\t0", "2026-10-08T10:00:01Z\tprod\tweb\tapp\tfalse\t-1", "2026-10-08T10:00:01Z\tprod\tweb\t\tfalse\t0"} {
		_, err := parseCursor(c)
		assert.EqualError(t, err, "a cursor must be an RFC 3339 time, or one a page handed back", c)
	}
}

// Lines order by time, then namespace, pod, container, the previous instance
// first, and ordinal; against a moment, by time alone.
func TestPositionsOrder(t *testing.T) {
	a := StreamKey{Namespace: "prod", Pod: "a", Container: "app"}
	prev := a
	prev.Previous = true
	assert.Less(t, comparePositions(lineAt(a, 0, 5), lineAt(a, time.Second, 0)), 0)
	assert.Less(t, comparePositions(lineAt(a, 0, 0), lineAt(a, 0, 1)), 0)
	assert.Less(t, comparePositions(lineAt(prev, 0, 9), lineAt(a, 0, 0)), 0)
	assert.Greater(t, comparePositions(lineAt(a, 0, 0), lineAt(prev, 0, 9)), 0)
	assert.Equal(t, 0, comparePositions(lineAt(a, 0, 7), position{Timestamp: at(0)}))
	assert.Equal(t, 0, comparePositions(position{Timestamp: at(0)}, lineAt(a, 0, 7)))
}

// A bound keeps or drops the line it sits on as its inclusivity says, and a
// moment bound every line of the moment.
func TestBoundsKeepOrDropTheirLine(t *testing.T) {
	a := StreamKey{Namespace: "prod", Pod: "a", Container: "app"}
	rec := Record{Timestamp: at(0), Stream: a, Ordinal: 1}
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
