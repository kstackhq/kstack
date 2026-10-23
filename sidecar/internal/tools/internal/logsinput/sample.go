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
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logline"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// A sample's caps, in bytes: one message, and the lines together. With a header
// they keep a sample under tools.InlineLimit, so it is never saved to a file.
const (
	MaxSampleLine = 2 << 10
	SampleBudget  = 16 << 10
)

// CutLine is s in at most MaxSampleLine bytes, cut on a character boundary with
// an ellipsis. A message is cut after logline.ForModel, since redaction reads the
// whole message.
func CutLine(s string) string {
	if len(s) <= MaxSampleLine {
		return s
	}
	return s[:tools.RuneBoundary(s, MaxSampleLine-len("…"))] + "…"
}

// LineText is rec as the model reads it: the hole before it on a line of its own,
// then its timestamp, its stream and its message. A message holds no newline, so
// every line a record writes starts with the sidecar's [ or a timestamp.
func LineText(rec logline.Record) string {
	line := Stamp(rec.Timestamp) + " " + streamOf(rec.Stream) + " " + rec.Message
	if rec.Gap == nil {
		return line
	}
	return "[lines may be missing from " + streamOf(rec.Stream) + " between " + Stamp(rec.Gap.After) + " and " + Stamp(rec.Gap.Before) + "]\n" + line
}

// streamOf is a stream as a line names it.
func streamOf(k logline.StreamKey) string {
	s := k.Namespace + "/" + k.Pod + "/" + k.Container
	if k.Previous {
		s += " (previous)"
	}
	return s
}

// Stamp is t in RFC 3339, in UTC, at its own precision.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ReadLine is rec as an action keeps it.
func ReadLine(rec logline.Record) tools.LogsReadLine {
	return tools.LogsReadLine{
		Timestamp: rec.Timestamp, Namespace: rec.Stream.Namespace, Pod: rec.Stream.Pod, Container: rec.Stream.Container,
		Previous: rec.Stream.Previous, Message: rec.Message, Gap: rec.Gap, TruncatedBytes: rec.TruncatedBytes,
	}
}

// ViewAround is the view View around this opens: view at the moment at, unpinned,
// resolved at resolvedAt.
func ViewAround(view tools.LogsScopeInput, at, resolvedAt time.Time) *tools.LogsViewAction {
	return &tools.LogsViewAction{
		LogsScopeInput: view, Anchor: tools.LogsViewAnchor{Kind: tools.LogsAnchorAt, At: &at}, ResolvedAt: resolvedAt,
	}
}
