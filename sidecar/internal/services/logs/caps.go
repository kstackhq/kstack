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
	"fmt"
	"unicode/utf8"
)

// capPage applies q's caps to m's records from the edge c reads from: a newer page
// keeps its oldest lines, an older one its newest. A record the page cap dropped
// cuts the page at the last line kept, so no edge passes it. It answers the caps'
// notices.
func (c pageCursor) capPage(m *merge, q FetchRequest) []Notice {
	kept, cut, notices := capLines(m.records, !c.newer, q.MaxMessageBytes, q.MaxPageBytes)
	m.records = kept
	switch {
	case cut && c.newer:
		m.cut = lineCursorOf(kept[len(kept)-1])
	case cut:
		m.cut = lineCursorOf(kept[0])
	}
	return notices
}

// capLines is records, oldest first, under its caps, read from the oldest or, with
// newestFirst, the newest: each message cut to maxLine bytes, then the records kept
// while their messages sum to maxPage. The first record read is always kept, with a
// notice when it alone is over. cut is whether a record was dropped, which leaves a
// line past the page.
func capLines(records []Record, newestFirst bool, maxLine, maxPage int) (kept []Record, cut bool, notices []Notice) {
	out := make([]Record, len(records))
	n, total := 0, 0
	for i := range records {
		j := i
		if newestFirst {
			j = len(records) - 1 - i
		}
		rec := capMessage(records[j], maxLine)
		total += len(rec.Message)
		if total > maxPage {
			if n > 0 {
				cut = true
				break
			}
			notices = append(notices, Notice{CodePageByteLimitExceeded, fmt.Sprintf("the page's first line alone is over its %d bytes", maxPage)})
		}
		out[j] = rec
		n++
	}
	if newestFirst {
		return out[len(out)-n:], cut, notices
	}
	return out[:n], cut, notices
}

// capMessage is rec with its message cut to at most max bytes at a character
// boundary, and TruncatedBytes the bytes cut. A stored line's only control is a
// kept SGR sequence, which is never split: one that does not fit whole is cut off
// with what follows it.
func capMessage(rec Record, max int) Record {
	msg := rec.Message
	if len(msg) <= max {
		return rec
	}
	end := 0
	for end < len(msg) {
		_, size := utf8.DecodeRuneInString(msg[end:])
		next := end + size
		if msg[end] == 0x1b {
			next = skipCSI(msg, end+2)
		}
		if next > max {
			break
		}
		end = next
	}
	rec.Message, rec.TruncatedBytes = msg[:end], len(msg)-end
	return rec
}
