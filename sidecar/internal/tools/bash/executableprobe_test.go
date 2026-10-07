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

package bash

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
)

// The list is the curated tools in their order, then the registered ones,
// each not probed yet.
func TestTheExecutableListIsTheCuratedThenTheRegistered(t *testing.T) {
	k9s := settings.Executable{Name: "k9s", Invocation: "k9s version"}

	got := executableList([]settings.Executable{k9s})

	assert.Len(t, got, len(settings.CuratedExecutables)+1)
	assert.Equal(t, ExecutableReport{Executable: settings.CuratedExecutables[0], Error: "not probed yet"}, got[0])
	assert.Equal(t, ExecutableReport{Executable: k9s, Registered: true, Error: "not probed yet"}, got[len(got)-1])
}

// cut counts characters, not bytes, so it never splits one.
func TestCutKeepsWholeCharacters(t *testing.T) {
	assert.Equal(t, "héé", cut("hééllo", 3))
	assert.Equal(t, "hé", cut("hé", 3))
}
