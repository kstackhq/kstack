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

package logsread

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// The lines a read returns: at most maxLimit, defaultLimit when the call names none.
const (
	maxLimit     = 100
	defaultLimit = 50
)

// endMessage is the refusal of a since or until the tool cannot read on the clock
// now, which it names so the retry can be exact.
func endMessage(now time.Time) string {
	return "an RFC 3339 time, an ISO 8601 duration back from now such as PT15M, or a cursor a read printed; now is " + now.UTC().Format(time.RFC3339)
}

// input is one call's arguments: the view's, the range's ends, nil for an open
// end, the mode and the limit.
type input struct {
	logsinput.Input
	since, until *end
	mode         tools.LogsReadMode
	limit        int
}

// end is one end of a read's range: the service's cursor for it, the moment the
// action records, and whether it names a line, which is outside the range, rather
// than a moment, whose lines are in it.
type end struct {
	cursor string
	at     time.Time
	line   bool
}

// parse reads the call's arguments, the shared ones through logsinput, and resolves
// each end of the range on the clock now.
func parse(raw json.RawMessage, now time.Time) (input, error) {
	in := input{mode: tools.LogsReadHead, limit: defaultLimit}
	var since, until, mode string
	shared, err := logsinput.Parse(raw, logsinput.Fields{
		"since": &since, "until": &until, "mode": &mode, "limit": &in.limit,
		"anchor":     logsinput.BadInput("anchor", "a read takes since and until"),
		"pin_to_end": logsinput.BadInput("pin_to_end", "a read opens no viewer"),
	})
	if err != nil {
		return input{}, err
	}
	in.Input = shared
	if in.limit < 1 || in.limit > maxLimit {
		return input{}, logsinput.BadInput("limit", fmt.Sprintf("1 to %d", maxLimit))
	}
	switch mode {
	case "", "head":
	case "tail":
		in.mode = tools.LogsReadTail
	default:
		return input{}, logsinput.BadInput("mode", "head or tail")
	}
	if in.since, err = endOf(since, now, "since"); err != nil {
		return input{}, err
	}
	if in.until, err = endOf(until, now, "until"); err != nil {
		return input{}, err
	}
	if in.since != nil && in.until != nil && in.since.at.After(in.until.at) {
		return input{}, logsinput.BadInput("until", "not before since")
	}
	return in, nil
}

// endOf is the end s names on the clock now, nil for none: a moment, a duration
// back from now, or a cursor a read printed. A cursor is base64url JSON, starting
// eyJ, so it never reads as a duration.
func endOf(s string, now time.Time, field string) (*end, error) {
	if s == "" {
		return nil, nil
	}
	if at, ok := logsinput.MomentOf(s, now, logsinput.Behind); ok {
		return &end{cursor: at.Format(time.RFC3339Nano), at: at}, nil
	}
	if c, at, ok := decodeCursor(s); ok {
		return &end{cursor: c, at: at, line: true}, nil
	}
	return nil, logsinput.BadInput(field, endMessage(now))
}
