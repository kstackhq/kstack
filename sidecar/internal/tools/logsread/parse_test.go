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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// clock is the tool's now in every test: 2026-10-17T10:00:00Z.
var clock = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

// parsed is raw's sources with extra keys, parsed on the clock.
func parsed(t *testing.T, extra string) (input, error) {
	t.Helper()
	return parse(json.RawMessage(`{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]`+extra+`}`), clock)
}

// A bare call reads the oldest 50 lines of the whole range.
func TestABareCallIsAHeadReadOfFifty(t *testing.T) {
	t.Parallel()
	in, err := parsed(t, "")
	require.NoError(t, err)
	assert.Equal(t, tools.LogsReadHead, in.mode)
	assert.Equal(t, 50, in.limit)
	assert.Nil(t, in.since)
	assert.Nil(t, in.until)
}

// since and until each take a moment, a duration back from now, or a printed
// cursor, which names a line.
func TestAnEndIsAMomentADurationOrACursor(t *testing.T) {
	t.Parallel()
	rec := record(20*time.Second, 3, "a")
	in, err := parsed(t, `,"since":"2026-10-17T09:13:00.5Z","until":"PT5M","mode":"tail","limit":100`)
	require.NoError(t, err)
	assert.Equal(t, &end{cursor: "2026-10-17T09:13:00.5Z", at: time.Date(2026, 10, 17, 9, 13, 0, 5e8, time.UTC)}, in.since)
	assert.Equal(t, &end{cursor: "2026-10-17T09:55:00Z", at: clock.Add(-5 * time.Minute)}, in.until)
	assert.Equal(t, tools.LogsReadTail, in.mode)
	assert.Equal(t, 100, in.limit)

	in, err = parsed(t, `,"since":"`+encodeCursor(rec)+`","until":"`+encodeCursor(record(30*time.Second, 0, "b"))+`"`)
	require.NoError(t, err)
	assert.Equal(t, &end{cursor: rec.Cursor(), at: rec.Timestamp, line: true}, in.since)
	assert.True(t, in.until.line)
}

// Each argument a read cannot take is refused by its field: a viewer's arguments, a
// limit out of range, a mode it does not know, an end it cannot read, and a range
// that ends before it starts.
func TestBadArgumentsAreRefusedByField(t *testing.T) {
	t.Parallel()
	// An unreadable end's refusal names the clock, which the model has no other way to know.
	const endMessage = "an RFC 3339 time, an ISO 8601 duration back from now such as PT15M, or a cursor a read printed; now is 2026-10-17T10:00:00Z"
	for extra, want := range map[string]string{
		`,"anchor":"tail"`:               `{"error":"bad-input","field":"anchor","message":"a read takes since and until"}`,
		`,"pin_to_end":true`:             `{"error":"bad-input","field":"pin_to_end","message":"a read opens no viewer"}`,
		`,"limit":0`:                     `{"error":"bad-input","field":"limit","message":"1 to 100"}`,
		`,"limit":101`:                   `{"error":"bad-input","field":"limit","message":"1 to 100"}`,
		`,"limit":"5"`:                   `{"error":"bad-input","field":"limit"}`,
		`,"mode":"around"`:               `{"error":"bad-input","field":"mode","message":"head or tail"}`,
		`,"since":"15m"`:                 `{"error":"bad-input","field":"since","message":"` + endMessage + `"}`,
		`,"since":"yesterday"`:           `{"error":"bad-input","field":"since","message":"` + endMessage + `"}`,
		`,"until":"eyJ0IjoiMDk6MTQifQ"`:  `{"error":"bad-input","field":"until","message":"` + endMessage + `"}`,
		`,"since":"PT1M","until":"PT2M"`: `{"error":"bad-input","field":"until","message":"not before since"}`,
		`,"since":"PT1M","since":"PT2M"`: `{"error":"bad-input"}`,
	} {
		_, err := parsed(t, extra)
		assert.Equal(t, want, logsinput.Refusal(err), extra)
	}
}
