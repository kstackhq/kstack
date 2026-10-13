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
	"strings"
	"unicode/utf8"
)

// maxMessage is the most of a line the sidecar sends, in bytes, before the ellipsis.
const maxMessage = 16 << 10

// stripControl is a log line as the user may see it: CSI and OSC sequences, led by
// ESC or by their one-character C1 forms, every other C1 control, DEL and every C0
// control but tab removed, invalid UTF-8 replaced, and the line cut at maxMessage on
// a character boundary with an ellipsis. Everything a line carries goes through it
// before it leaves the sidecar, grep included, so a pattern matches what is drawn.
func stripControl(s string) string {
	if plain(s) {
		return s
	}
	var b strings.Builder
	b.Grow(min(len(s), maxMessage+len("…")))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == 0x1b && i < len(s) && s[i] == '[':
			i = skipCSI(s, i+1)
		case r == 0x1b && i < len(s) && s[i] == ']':
			i = skipOSC(s, i+1)
		case r == 0x9b:
			i = skipCSI(s, i)
		case r == 0x9d:
			i = skipOSC(s, i)
		case r < 0x20 && r != '\t', r == 0x7f, r >= 0x80 && r <= 0x9f:
		default:
			if b.Len()+utf8.RuneLen(r) > maxMessage {
				b.WriteString("…")
				return b.String()
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// plain is whether s is printable ASCII and tabs within maxMessage, which most
// lines are and which stripControl leaves as it is.
func plain(s string) bool {
	if len(s) > maxMessage {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\t') || c >= 0x7f {
			return false
		}
	}
	return true
}

// skipCSI is the index past a control sequence whose parameters start at i: its
// final byte is the first in 0x40–0x7E, and an unterminated one runs to the end.
func skipCSI(s string, i int) int {
	for ; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return i
}

// skipOSC is the index past an operating system command whose text starts at i: it
// ends at BEL, at ESC \, or at the C1 string terminator, and an unterminated one runs
// to the end.
func skipOSC(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x07, r == 0x9c:
			return i + size
		case r == 0x1b && i+1 < len(s) && s[i+1] == '\\':
			return i + 2
		}
		i += size
	}
	return i
}
