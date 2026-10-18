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
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
)

// record is a line of prod/webapp-a/app at offset past 09:14, the ordinal-th of its
// moment, saying msg.
func record(offset time.Duration, ordinal int, msg string) logs.Record {
	return logs.Record{
		Timestamp: time.Date(2026, 10, 17, 9, 14, 0, 0, time.UTC).Add(offset), Message: msg,
		Stream:      logs.StreamKey{Namespace: "prod", Pod: "webapp-a", Container: "app"},
		ContainerID: "containerd://3f9a", Ordinal: ordinal,
	}
}

// A printed cursor is the line's position as JSON, base64url without padding, and
// reads back as the service's own cursor of that line, and its moment.
func TestACursorRoundTripsTheServicesForm(t *testing.T) {
	t.Parallel()
	rec := record(20*time.Second+4*time.Millisecond, 12, "a")

	printed := encodeCursor(rec)
	assert.NotContains(t, printed, "=")
	raw, err := base64.RawURLEncoding.DecodeString(printed)
	require.NoError(t, err)
	assert.JSONEq(t, `{"t":"2026-10-17T09:14:20.004Z","ns":"prod","pod":"webapp-a","c":"app","id":"containerd://3f9a","n":12}`, string(raw))

	c, at, ok := decodeCursor(printed)
	require.True(t, ok)
	assert.Equal(t, rec.Cursor(), c)
	assert.Equal(t, rec.Timestamp, at)
}

// A blob no sample could have printed is not a cursor.
func TestABadCursorIsRefused(t *testing.T) {
	t.Parallel()
	enc := func(json string) string { return base64.RawURLEncoding.EncodeToString([]byte(json)) }
	for name, blob := range map[string]string{
		"not base64url":  "eyJ0Ijo+",
		"not JSON":       enc(`{"t":`),
		"no container":   enc(`{"t":"2026-10-17T09:14:20Z","ns":"prod","pod":"a","id":"c://a","n":0}`),
		"a bad time":     enc(`{"t":"09:14","ns":"prod","pod":"a","c":"app","id":"c://a","n":0}`),
		"a tab in an id": enc(`{"t":"2026-10-17T09:14:20Z","ns":"prod","pod":"a","c":"app","id":"c://a\tb","n":0}`),
		"a long id":      enc(`{"t":"2026-10-17T09:14:20Z","ns":"prod","pod":"a","c":"app","id":"` + strings.Repeat("a", 257) + `","n":0}`),
		"another field":  enc(`{"t":"2026-10-17T09:14:20Z","ns":"prod","pod":"a","c":"app","id":"c://a","n":0,"x":1}`),
		"padded":         enc(`{"t":"2026-10-17T09:14:20Z","ns":"prod","pod":"a","c":"app","id":"c://a","n":0}`) + "==",
	} {
		_, _, ok := decodeCursor(blob)
		assert.False(t, ok, name)
	}
}
