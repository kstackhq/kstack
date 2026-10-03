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

package permissions

import (
	"regexp"
	"strings"
)

// Match is whether value matches pattern: * matches any run of characters, /
// and : included, since a context is often an ARN or a GKE name; ? matches one
// character; \ escapes the next; nothing else is special.
func Match(pattern, value string) bool {
	var re strings.Builder
	re.WriteString(`(?s)^`)
	escaped := false
	for _, r := range pattern {
		switch {
		case escaped:
			re.WriteString(regexp.QuoteMeta(string(r)))
			escaped = false
		case r == '\\':
			escaped = true
		case r == '*':
			re.WriteString(`.*`)
		case r == '?':
			re.WriteString(`.`)
		default:
			re.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	if escaped { // a trailing \ is itself
		re.WriteString(`\\`)
	}
	re.WriteString(`$`)
	return regexp.MustCompile(re.String()).MatchString(value)
}

// Literal is the pattern that matches s alone.
func Literal(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '*' || r == '?' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
