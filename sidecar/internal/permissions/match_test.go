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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchCrossesSlashes(t *testing.T) {
	for _, c := range []struct {
		pattern, value string
		want           bool
	}{
		{"*prod*", "arn:aws:eks:us-east-1:1:cluster/prod-eu", true},
		{"*prod*", "gke_p_z_prod", true},
		{"dev-*", "dev-eks", true},
		{"dev-?ks", "dev-eks", true},
		{"dev-?", "dev-eks", false},
		{"[a]", "[a]", true},
		{"[a]", "a", false},
		{"", "anything", false},
		{"", "", true},
		{"dev", "prod", false},
		{"dev", "dev", true},
		{"*", "", true},
		{"a*b*c", "a/x/b:y/c", true},
		{"a*b", "a/x/bc", false},
	} {
		assert.Equal(t, c.want, Match(c.pattern, c.value), "%q against %q", c.pattern, c.value)
	}
}

func TestALiteralMatchesItselfAlone(t *testing.T) {
	assert.Equal(t, `dev\*`, Literal("dev*"))
	assert.True(t, Match(Literal("dev*"), "dev*"))
	assert.False(t, Match(Literal("dev*"), "dev-eks"))
	for _, v := range []string{"a?b", `a\b`, `x\*?y`, ""} {
		assert.True(t, Match(Literal(v), v), "%q", v)
	}
	assert.False(t, Match(Literal("a?b"), "axb"))
}
