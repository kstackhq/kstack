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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
)

// cursor is a line's position as the model reads it. It is printed opaque, so it
// reads as a token to copy rather than a structure to imitate.
type cursor struct {
	T   string `json:"t"`
	NS  string `json:"ns"`
	Pod string `json:"pod"`
	C   string `json:"c"`
	ID  string `json:"id"`
	N   int    `json:"n"`
}

// encodeCursor is rec's cursor as the model reads it: its position as JSON,
// base64url without padding, which has nothing to strip or redact.
func encodeCursor(rec logs.Record) string {
	c := cursor{
		T: rec.Timestamp.UTC().Format(time.RFC3339Nano), NS: rec.Stream.Namespace, Pod: rec.Stream.Pod,
		C: rec.Stream.Container, ID: rec.ContainerID, N: rec.Ordinal,
	}
	b, _ := json.Marshal(c) // strings and an int always marshal
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor is the line a printed cursor names, as the service's cursor and its
// moment; false for a blob no read could have printed.
func decodeCursor(s string) (string, time.Time, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", time.Time{}, false
	}
	var c cursor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return "", time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, c.T)
	if err != nil {
		return "", time.Time{}, false
	}
	own, err := logs.LineCursor(at, c.NS, c.Pod, c.C, c.ID, c.N)
	if err != nil {
		return "", time.Time{}, false
	}
	return own, at.UTC(), true
}
