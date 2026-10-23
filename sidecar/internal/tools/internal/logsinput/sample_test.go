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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logline"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// A line is its timestamp, its stream and its message; the instance before a
// restart says so.
func TestALineIsItsStampStreamAndMessage(t *testing.T) {
	t.Parallel()
	rec := record(20*time.Second+4*time.Millisecond, 0, "panic: nil map")
	assert.Equal(t, "2026-10-17T09:14:20.004Z prod/webapp-a/app panic: nil map", LineText(rec))

	rec.Stream.Previous = true
	assert.Equal(t, "2026-10-17T09:14:20.004Z prod/webapp-a/app (previous) panic: nil map", LineText(rec))
}

// A hole before a line is a line of its own ahead of it, starting with [.
func TestAHoleIsALineBeforeIt(t *testing.T) {
	t.Parallel()
	rec := record(time.Minute, 0, "back")
	rec.Gap = &logline.Gap{After: rec.Timestamp.Add(-time.Minute), Before: rec.Timestamp}
	assert.Equal(t, "[lines may be missing from prod/webapp-a/app between 2026-10-17T09:14:00Z and 2026-10-17T09:15:00Z]\n"+
		"2026-10-17T09:15:00Z prod/webapp-a/app back", LineText(rec))
}

// A message past MaxSampleLine is cut on a character boundary with an ellipsis.
func TestALongMessageIsCut(t *testing.T) {
	t.Parallel()
	short := strings.Repeat("a", MaxSampleLine)
	assert.Equal(t, short, CutLine(short))

	long := strings.Repeat("é", MaxSampleLine)
	cut := CutLine(long)
	assert.LessOrEqual(t, len(cut), MaxSampleLine)
	assert.True(t, strings.HasSuffix(cut, "…"))
	assert.True(t, strings.HasPrefix(long, strings.TrimSuffix(cut, "…")))
}

// A record is the action's line field for field, and the view around a line is
// the view at its moment, unpinned.
func TestARecordIsTheActionsLine(t *testing.T) {
	t.Parallel()
	rec := record(time.Second, 3, "a")
	rec.Stream.Previous = true
	rec.Gap = &logline.Gap{After: rec.Timestamp.Add(-time.Second), Before: rec.Timestamp}
	rec.TruncatedBytes = 5
	assert.Equal(t, tools.LogsReadLine{
		Timestamp: rec.Timestamp, Namespace: "prod", Pod: "webapp-a", Container: "app", Previous: true, Message: "a", Gap: rec.Gap,
		TruncatedBytes: 5,
	}, ReadLine(rec))

	view := tools.LogsScopeInput{Grep: "a"}
	resolved := rec.Timestamp.Add(time.Hour)
	around := ViewAround(view, rec.Timestamp, resolved)
	assert.Equal(t, tools.LogsViewAction{
		LogsScopeInput: view, Anchor: tools.LogsViewAnchor{Kind: tools.LogsAnchorAt, At: &rec.Timestamp}, ResolvedAt: resolved,
	}, *around)
}

// record is a line of prod/webapp-a/app at offset past 09:14, the ordinal-th of its
// moment, saying msg.
func record(offset time.Duration, ordinal int, msg string) logline.Record {
	return logline.Record{
		Timestamp: time.Date(2026, 10, 17, 9, 14, 0, 0, time.UTC).Add(offset), Message: msg,
		Stream:      logline.StreamKey{Namespace: "prod", Pod: "webapp-a", Container: "app"},
		ContainerID: "containerd://3f9a", Ordinal: ordinal,
	}
}
