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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
)

// cursorVersion is the shape a printed cursor has. A cursor lives on in chat
// transcripts, which no build can rewrite, so a build that changes the shape or
// the meaning of a field bumps it and refuses what older builds printed.
const cursorVersion = 1

// A printed cursor that is not read: one another version of Kstack printed, or
// one no read could have printed.
var (
	ErrOldCursor = errors.New("logsinput: a cursor from another version")
	ErrNotCursor = errors.New("logsinput: not a cursor")
)

// OldCursorMessage is the refusal's message for ErrOldCursor: the model cannot fix
// the cursor, only read again for one this version prints.
const OldCursorMessage = "a cursor another version of Kstack printed; read again for one from this version"

// cursor is a line's position as the model reads it. It is printed opaque, so it
// reads as a token to copy rather than a structure to imitate.
type cursor struct {
	V   int    `json:"v"`
	T   string `json:"t"`
	NS  string `json:"ns"`
	Pod string `json:"pod"`
	C   string `json:"c"`
	ID  string `json:"id"`
	N   int    `json:"n"`
}

// EncodeCursor is rec's cursor as the model reads it: its position as JSON,
// base64url without padding, which has nothing to strip or redact.
func EncodeCursor(rec logs.Record) string {
	c := cursor{
		V: cursorVersion, T: rec.Timestamp.UTC().Format(time.RFC3339Nano), NS: rec.Stream.Namespace, Pod: rec.Stream.Pod,
		C: rec.Stream.Container, ID: rec.ContainerID, N: rec.Ordinal,
	}
	b, _ := json.Marshal(c) // strings and ints always marshal
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor is the line a printed cursor names, as the service's cursor and its
// moment; ErrOldCursor for one another version printed, ErrNotCursor for a blob no
// read could have printed.
func DecodeCursor(s string) (string, time.Time, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", time.Time{}, ErrNotCursor
	}
	var c cursor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return "", time.Time{}, ErrNotCursor
	}
	at, err := time.Parse(time.RFC3339Nano, c.T)
	if err != nil {
		return "", time.Time{}, ErrNotCursor
	}
	own, err := logs.LineCursor(at, c.NS, c.Pod, c.C, c.ID, c.N)
	if err != nil {
		return "", time.Time{}, ErrNotCursor
	}
	// Checked last: only a cursor well formed in every other way is another
	// version's, since a wrong version on a blob no read printed says nothing.
	if c.V != cursorVersion {
		return "", time.Time{}, ErrOldCursor
	}
	return own, at.UTC(), nil
}
