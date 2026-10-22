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

// A window whose bounds cross, or whose padding is past its limits or names both
// a count and a span, is refused.
func TestAWindowIsChecked(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		w    Window
		code Code
	}{
		"since after until":   {Window{Since: moment(2 * time.Second), Until: moment(time.Second)}, CodeInvalidRange},
		"both lines and span": {Window{Before: Padding{Lines: 1, Span: time.Second}}, CodeConflictingParameters},
		"too many lines":      {Window{After: Padding{Lines: MaxPageSize + 1}}, CodeBadPadding},
		"negative lines":      {Window{Before: Padding{Lines: -1}}, CodeBadPadding},
		"too long a span":     {Window{After: Padding{Span: MaxPaddingSpan + 1}}, CodeBadPadding},
		"a negative span":     {Window{Before: Padding{Span: -time.Second}}, CodeBadPadding},
	} {
		assert.Equal(t, tc.code, codeOf(checkWindow(tc.w)), name)
	}
	for name, w := range map[string]Window{
		"open":             {},
		"one nanosecond":   {Since: moment(time.Second), Until: moment(time.Second)},
		"the most lines":   {Before: Padding{Lines: MaxPageSize}},
		"the longest span": {After: Padding{Span: MaxPaddingSpan}},
	} {
		assert.NoError(t, checkWindow(w), name)
	}
}

// A window's edges are its bounds widened by its spans; an open Until's span is
// measured from the first page's clock, so it is the same on every page; a count's
// edge is unknown until a page finds it.
func TestAWindowsEdges(t *testing.T) {
	t.Parallel()
	resolved := at(time.Hour)
	for name, tc := range map[string]struct {
		w          Window
		start, end bound
	}{
		"a span each side":     {Window{Since: moment(10 * time.Second), Until: moment(20 * time.Second), Before: Padding{Span: time.Second}, After: Padding{Span: 2 * time.Second}}, from(9 * time.Second), to(22 * time.Second)},
		"open":                 {Window{}, bound{}, bound{}},
		"an open until":        {Window{Since: moment(10 * time.Second)}, from(10 * time.Second), bound{}},
		"an open until's span": {Window{After: Padding{Span: time.Minute}}, bound{}, to(time.Hour + time.Minute)},
		"one nanosecond":       {Window{Since: moment(time.Second), Until: moment(time.Second)}, from(time.Second), to(time.Second)},
		"counts":               {Window{Since: moment(10 * time.Second), Until: moment(20 * time.Second), Before: Padding{Lines: 5}, After: Padding{Lines: 5}}, bound{}, bound{}},
		"before an open since": {Window{Before: Padding{Span: time.Second}}, bound{}, bound{}},
	} {
		c, err := firstPage(FetchRequest{Window: tc.w, PageFrom: PageFromFirst}, resolved)
		require.NoError(t, err, name)
		assert.Equal(t, tc.start, c.start(), name+": start")
		assert.Equal(t, tc.end, c.end(), name+": end")
	}

	// A continuation reads its window's end from the clock the first page pinned.
	w := resolvedWindow{Window: Window{After: Padding{Span: time.Minute}}, ResolvedAt: resolved}
	later := resolvedWindow{Window: w.Window, ResolvedAt: resolved.Add(time.Hour)}
	assert.Equal(t, to(time.Hour+time.Minute), w.end())
	assert.Equal(t, to(2*time.Hour+time.Minute), later.end())
}

// The range's own bounds tell its lines from the padding's: an open Until with
// After set ends just before the first page's clock.
func TestARangesBoundsSortPadding(t *testing.T) {
	t.Parallel()
	w := resolvedWindow{Window: Window{Since: moment(10 * time.Second), Until: moment(20 * time.Second), After: Padding{Lines: 1}}}
	assert.Equal(t, sideBefore, w.side(line("web-1", "app", 9*time.Second, "")))
	assert.Equal(t, sideRange, w.side(line("web-1", "app", 10*time.Second, "")))
	assert.Equal(t, sideRange, w.side(line("web-1", "app", 20*time.Second, "")))
	assert.Equal(t, sideAfter, w.side(line("web-1", "app", 21*time.Second, "")))

	open := resolvedWindow{Window: Window{After: Padding{Lines: 1}}, ResolvedAt: at(time.Hour)}
	assert.Equal(t, sideRange, open.side(line("web-1", "app", time.Hour-1, "")))
	assert.Equal(t, sideAfter, open.side(line("web-1", "app", time.Hour, "")))
	unbounded := resolvedWindow{Window: Window{}, ResolvedAt: at(time.Hour)}
	assert.Equal(t, sideRange, unbounded.side(line("web-1", "app", 2*time.Hour, "")))
}

// A first page pins a time or span start; a counted one waits for its read.
func TestAFirstPagePinsItsStart(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		w     Window
		start lineCursor
	}{
		"a time":  {Window{Since: moment(10 * time.Second)}, momentAt(at(10 * time.Second))},
		"a span":  {Window{Since: moment(10 * time.Second), Before: Padding{Span: time.Second}}, momentAt(at(9 * time.Second))},
		"a count": {Window{Since: moment(10 * time.Second), Before: Padding{Lines: 1}}, lineCursor{}},
		"open":    {Window{}, lineCursor{}},
	} {
		c, err := firstPage(FetchRequest{Window: tc.w, PageFrom: PageFromLast}, at(time.Hour))
		require.NoError(t, err, name)
		assert.Equal(t, tc.start, c.Start, name)
	}
}
