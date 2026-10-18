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

package logswatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// A watch's bounds: the lines that end it, at most maxLimit and one when the call
// names none; and how long it runs at most, from its start.
const (
	maxLimit     = 100
	defaultLimit = 1
	maxWatch     = 24 * time.Hour
)

// input is one call's arguments: the view's, the window and the limits. since is
// where the window starts, now for none; after is the cursor the follow reads
// past; until is when the watch ends on time alone, and capped whether that was
// cut to maxWatch.
type input struct {
	logsinput.Input
	since           time.Time
	after           string
	until           time.Time
	capped          bool
	limit, maxBytes int
}

// sinceMessage and untilMessage are the refusals of an end the tool cannot read on
// the clock now, which they name so the retry can be exact.
func sinceMessage(now time.Time) string {
	return "an RFC 3339 time or an ISO 8601 duration back from now such as PT10M, not after now, or a cursor a read printed; now is " + now.UTC().Format(time.RFC3339)
}

func untilMessage(now time.Time) string {
	return "an RFC 3339 time or an ISO 8601 duration ahead of now such as PT2H, after now; now is " + now.UTC().Format(time.RFC3339)
}

// parse reads the call's arguments, the shared ones through logsinput, and resolves
// the window on the clock now.
func parse(raw json.RawMessage, now time.Time) (input, error) {
	in := input{limit: defaultLimit, maxBytes: logsinput.SampleBudget}
	var since, until string
	shared, err := logsinput.Parse(raw, logsinput.Fields{
		"since": &since, "until": &until, "limit": &in.limit, "max_bytes": &in.maxBytes,
		"anchor":     logsinput.BadInput("anchor", "a watch takes since and until"),
		"pin_to_end": logsinput.BadInput("pin_to_end", "a watch opens no viewer"),
		"mode":       logsinput.BadInput("mode", "a watch reads its lines in order"),
	})
	if err != nil {
		return input{}, err
	}
	in.Input = shared
	if in.limit < 1 || in.limit > maxLimit {
		return input{}, logsinput.BadInput("limit", fmt.Sprintf("1 to %d", maxLimit))
	}
	if in.maxBytes < 1 || in.maxBytes > logsinput.SampleBudget {
		return input{}, logsinput.BadInput("max_bytes", fmt.Sprintf("1 to %d", logsinput.SampleBudget))
	}
	if in.since, in.after, err = startOf(since, now); err != nil {
		return input{}, err
	}
	if in.until, in.capped, err = untilOf(until, now); err != nil {
		return input{}, err
	}
	return in, nil
}

// startOf is where the window s names starts, on the clock now, and the cursor
// the follow reads past: for none, a moment, or a duration back from now, the
// moment and a nanosecond before it, which is how the service spells a moment
// whose lines it keeps; for a cursor a read printed, its line's moment and the
// service's own cursor. The follow is always told its start: the one it would
// pick itself comes after its setup, and the window says now. A cursor is
// base64url JSON, starting eyJ, so it never reads as a duration.
func startOf(s string, now time.Time) (time.Time, string, error) {
	if s == "" {
		return now, before(now), nil
	}
	if at, ok := logsinput.MomentOf(s, now, logsinput.Behind); ok && !at.After(now) {
		return at, before(at), nil
	}
	after, at, err := logsinput.DecodeCursor(s)
	if errors.Is(err, logsinput.ErrOldCursor) {
		return time.Time{}, "", logsinput.BadInput("since", logsinput.OldCursorMessage)
	}
	if err != nil {
		return time.Time{}, "", logsinput.BadInput("since", sinceMessage(now))
	}
	return at, after, nil
}

// before is the cursor a nanosecond ahead of the moment t, which a follow from it
// reads past.
func before(t time.Time) string { return t.Add(-time.Nanosecond).Format(time.RFC3339Nano) }

// untilOf is when the watch ends on time alone: the moment s names ahead of now, or
// a duration from now, cut to maxWatch from now with capped set; maxWatch from now
// for none.
func untilOf(s string, now time.Time) (time.Time, bool, error) {
	limit := now.Add(maxWatch)
	if s == "" {
		return limit, false, nil
	}
	at, ok := logsinput.MomentOf(s, now, logsinput.Ahead)
	if !ok || !at.After(now) {
		return time.Time{}, false, logsinput.BadInput("until", untilMessage(now))
	}
	if at.After(limit) {
		return limit, true, nil
	}
	return at, false, nil
}
