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
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// Colour and cursor sequences, in their ESC and C1 forms, leave the text alone.
func TestStripControlRemovesSequences(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ in, want string }{
		"colour":       {"\x1b[31mred\x1b[0m", "red"},
		"cursor":       {"a\x1b[2Kb\x1b[1;1Hc", "abc"},
		"osc bel":      {"\x1b]0;title\x07text", "text"},
		"osc st":       {"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		"c1 csi":       {"a\u009b31mb", "ab"},
		"c1 osc":       {"a\u009d0;t\u009cb", "ab"},
		"other c1":     {"a\u0085b", "ab"},
		"del":          {"a\x7fb", "ab"},
		"c0":           {"a\x00b\rc\x08d", "abcd"},
		"tab kept":     {"\x1b[1ma\tb", "a\tb"},
		"lone esc":     {"a\x1bb", "ab"},
		"unterminated": {"a\x1b[31", "a"},
		"open osc":     {"a\x1b]0;title", "a"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, stripControl(tc.in))
		})
	}
}

// A line past 16 KiB is cut there on a character boundary and ends with an ellipsis.
func TestStripControlCutsALongLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", maxMessage-1) + "é" + strings.Repeat("b", 4096)

	got := stripControl(long)

	assert.True(t, utf8.ValidString(got))
	assert.Equal(t, strings.Repeat("a", maxMessage-1)+"…", got)
}

// Tabs count toward the limit like any other character.
func TestStripControlCutsALineOfTabs(t *testing.T) {
	t.Parallel()
	got := stripControl("\x1b[1m" + strings.Repeat("\t", maxMessage+10))
	assert.Equal(t, strings.Repeat("\t", maxMessage)+"…", got)
}

// A line within the limit is kept whole.
func TestStripControlKeepsAShortLine(t *testing.T) {
	t.Parallel()
	line := strings.Repeat("x", maxMessage)
	assert.Equal(t, line, stripControl(line))
}

// Grep matches the text the user sees, not the escapes around it.
func TestGrepMatchesTheStrippedText(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^error: x$`)
	assert.True(t, re.MatchString(stripControl("\x1b[31merror\x1b[0m: x")))
}
