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

package securityconfig

// checks is the read-back: one check per field, each added by the field's
// step. A check reads the value alone, never the disk or another service; it
// removes from the settings every value it refuses and answers one Refusal for
// each.
var checks = []func(*Settings) []Refusal{checkPath}

// A Refusal is one value a check left out. Value is what the field's Settings
// section names it by, else its JSON; Reason is in the user's words. As an
// error, it is an Update the check refused.
type Refusal struct {
	Field  string
	Value  string
	Reason string
}

func (r Refusal) Error() string {
	return r.Field + ": " + r.Reason
}

func runChecks[T any](v *T, checks []func(*T) []Refusal) []Refusal {
	var refused []Refusal
	for _, check := range checks {
		refused = append(refused, check(v)...)
	}
	return refused
}

// strictest is how each field that restricts, keyed by its JSON name, is set
// to its most restrictive state; each such field's step adds its line. When the
// file holds a value of one the store refuses, the field answers that state
// until the user fixes the file, never its zero value. A field not listed only
// grants, so a refused value of it is dropped.
var strictest = map[string]func(*Settings){
	// A refused entry may have been a removal, so the field restricts. The
	// entries that passed stay as they are: what makes the field strict is
	// the sync's, while the store holds it.
	"path": func(*Settings) {},
	// Resolved, so no default stands in for the list.
	"pathResolved": func(v *Settings) { v.PathResolved = true },
	// Strict, so the next sync adopts nothing new unasked.
	"pathStrict": func(v *Settings) { v.PathStrict = true },
}
