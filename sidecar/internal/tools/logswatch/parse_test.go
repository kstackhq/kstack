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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// clock is the tool's now in every test: 2026-10-18T13:50:00Z.
var clock = time.Date(2026, 10, 18, 13, 50, 0, 0, time.UTC)

// record is a line of payments/webapp-a/app at offset past clock, the ordinal-th of
// its moment, saying msg.
func record(offset time.Duration, ordinal int, msg string) logs.Record {
	return logs.Record{
		Timestamp: clock.Add(offset), Message: msg,
		Stream:      logs.StreamKey{Namespace: "payments", Pod: "webapp-a", Container: "app"},
		ContainerID: "containerd://3f9a", Ordinal: ordinal,
	}
}

// parsed is raw's sources with extra keys, parsed on the clock.
func parsed(t *testing.T, extra string) (input, error) {
	t.Helper()
	return parse(json.RawMessage(`{"sources":[{"namespace":"payments","resource":"deployments/webapp"}]`+extra+`}`), clock)
}

// A bare call watches from now for one line of anything, up to a day; the follow
// is told to start a nanosecond before now, not left to pick its own start.
func TestABareCallWatchesFromNowForOneLine(t *testing.T) {
	t.Parallel()
	in, err := parsed(t, "")
	require.NoError(t, err)
	assert.Equal(t, clock, in.since)
	assert.Equal(t, "2026-10-18T13:49:59.999999999Z", in.after)
	assert.Equal(t, 1, in.limit)
	assert.Equal(t, logsinput.SampleBudget, in.maxBytes)
	assert.Equal(t, clock.Add(maxWatch), in.until)
	assert.False(t, in.capped)
	assert.Empty(t, in.Grep)
}

// since takes a moment or a duration back from now, whose lines are in the window,
// so the follow starts a nanosecond before it; or a printed cursor, whose own line
// is not, so the follow starts at it. until takes a moment or a duration ahead.
func TestTheWindowIsAMomentADurationOrACursor(t *testing.T) {
	t.Parallel()
	in, err := parsed(t, `,"since":"PT10M","until":"PT2H","limit":100,"max_bytes":4096,"grep":"panic"`)
	require.NoError(t, err)
	assert.Equal(t, clock.Add(-10*time.Minute), in.since)
	assert.Equal(t, "2026-10-18T13:39:59.999999999Z", in.after)
	assert.Equal(t, clock.Add(2*time.Hour), in.until)
	assert.Equal(t, 100, in.limit)
	assert.Equal(t, 4096, in.maxBytes)
	assert.Equal(t, "panic", in.Grep)

	in, err = parsed(t, `,"since":"2026-10-18T13:00:00Z","until":"2026-10-18T14:00:00Z"`)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC), in.since)
	assert.Equal(t, "2026-10-18T12:59:59.999999999Z", in.after)
	assert.Equal(t, time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC), in.until)
}

// An until past the cap is cut to it, and the input says so.
func TestAnUntilPastTheCapIsCut(t *testing.T) {
	t.Parallel()
	in, err := parsed(t, `,"until":"P3D"`)
	require.NoError(t, err)
	assert.Equal(t, clock.Add(maxWatch), in.until)
	assert.True(t, in.capped)
}

// Each argument a watch cannot take is refused by its field.
func TestBadArgumentsAreRefusedByField(t *testing.T) {
	t.Parallel()
	const sinceMessage = "an RFC 3339 time or an ISO 8601 duration back from now such as PT10M, not after now; now is 2026-10-18T13:50:00Z"
	const untilMessage = "an RFC 3339 time or an ISO 8601 duration ahead of now such as PT2H, after now; now is 2026-10-18T13:50:00Z"
	for extra, want := range map[string]string{
		`,"anchor":"tail"`:                    `{"error":"bad-input","field":"anchor","message":"a watch takes since and until"}`,
		`,"pin_to_end":true`:                  `{"error":"bad-input","field":"pin_to_end","message":"a watch opens no viewer"}`,
		`,"mode":"tail"`:                      `{"error":"bad-input","field":"mode","message":"a watch reads its lines in order"}`,
		`,"limit":0`:                          `{"error":"bad-input","field":"limit","message":"1 to 100"}`,
		`,"limit":101`:                        `{"error":"bad-input","field":"limit","message":"1 to 100"}`,
		`,"max_bytes":0`:                      `{"error":"bad-input","field":"max_bytes","message":"1 to 16384"}`,
		`,"max_bytes":16385`:                  `{"error":"bad-input","field":"max_bytes","message":"1 to 16384"}`,
		`,"since":"2026-10-18T14:00:00Z"`:     `{"error":"bad-input","field":"since","message":"` + sinceMessage + `"}`,
		`,"since":"10m"`:                      `{"error":"bad-input","field":"since","message":"` + sinceMessage + `"}`,
		`,"since":"eyJub3QiOiJhIGN1cnNvciJ9"`: `{"error":"bad-input","field":"since","message":"` + sinceMessage + `"}`,
		`,"until":"2026-10-18T13:00:00Z"`:     `{"error":"bad-input","field":"until","message":"` + untilMessage + `"}`,
		`,"until":"2026-10-18T13:50:00Z"`:     `{"error":"bad-input","field":"until","message":"` + untilMessage + `"}`,
	} {
		_, err := parsed(t, extra)
		require.Error(t, err, extra)
		assert.Equal(t, want, logsinput.Refusal(err), extra)
	}
}
