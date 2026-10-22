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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// saying is a record saying msg.
func saying(msg string) Record { return line("web-1", "app", time.Second, msg) }

// A message over the cap is cut at a character boundary, before any SGR sequence
// that would not fit whole, and counts the bytes it lost; one under the cap keeps
// its colours (rule 8).
func TestAMessageIsCutAtTheCap(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		msg, want string
		max       int
	}{
		"under the cap":         {"hello", "hello", 5},
		"at a boundary":         {"hello", "hel", 3},
		"inside a character":    {"héllo", "h", 2},
		"inside a sequence":     {"ab\x1b[31mred", "ab", 5},
		"past a sequence":       {"ab\x1b[31mred", "ab\x1b[31mr", 8},
		"coloured, under":       {"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m", 100},
		"a sequence at the end": {"red\x1b[0m", "red", 5},
	} {
		got, _, _ := capLines([]Record{saying(tc.msg)}, false, tc.max, MaxPageBytesCap)
		assert.Equal(t, tc.want, got[0].Message, name)
		assert.Equal(t, len(tc.msg)-len(tc.want), got[0].TruncatedBytes, name)
	}
}

// A page keeps its records in the order it reads while their messages, after the
// message cap, fit the page cap; the first always, saying so when it alone is over
// (rule 9).
func TestAPageKeepsWhatFitsItsCap(t *testing.T) {
	t.Parallel()
	recs := []Record{saying(strings.Repeat("a", 10)), saying(strings.Repeat("b", 10)), saying(strings.Repeat("c", 10))}

	got, cut, notices := capLines(recs, false, 100, 25)
	assert.Equal(t, recs[:2], got)
	assert.True(t, cut)
	assert.Empty(t, notices)

	got, cut, notices = capLines(recs, false, 100, 30)
	assert.Equal(t, recs, got)
	assert.False(t, cut)
	assert.Empty(t, notices)

	got, cut, _ = capLines(recs, false, 4, 10)
	assert.Len(t, got, 2, "the message cap counts first")
	assert.True(t, cut)

	got, cut, notices = capLines(recs, false, 100, 5)
	assert.Equal(t, recs[:1], got)
	assert.True(t, cut)
	assert.Equal(t, []Code{CodePageByteLimitExceeded}, codesOf(notices))

	got, cut, _ = capLines(recs, true, 100, 25)
	assert.Equal(t, recs[1:], got, "read newest first, the oldest is dropped")
	assert.True(t, cut)
}
