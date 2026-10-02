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

import "strings"

// Match is whether value matches pattern: * matches any run of characters, /
// and : included, since a context is often an ARN or a GKE name; ? matches one
// character; \ escapes the next; nothing else is special.
func Match(pattern, value string) bool {
	p, v := []rune(pattern), []rune(value)
	// The last * seen, and where in value it was tried from, so a mismatch
	// after it retries one character further on.
	star, from := -1, 0
	i, j := 0, 0
	for j < len(v) {
		switch {
		case i < len(p) && p[i] == '*':
			star, from = i, j
			i++
		case i < len(p) && matchOne(p, &i, v[j]):
			j++
		case star >= 0:
			from++
			i, j = star+1, from
		default:
			return false
		}
	}
	for i < len(p) && p[i] == '*' {
		i++
	}
	return i == len(p)
}

// matchOne is whether the token at p[*i] matches r, and moves *i past it when
// it does.
func matchOne(p []rune, i *int, r rune) bool {
	k := *i
	if p[k] == '\\' && k+1 < len(p) {
		k++
	} else if p[k] == '?' {
		*i = k + 1
		return true
	}
	if p[k] != r {
		return false
	}
	*i = k + 1
	return true
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
