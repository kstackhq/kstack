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

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// System reads the system's folders, /private/etc whole, and denies
// Homebrew's var.
func TestDarwinListsReadTheSystem(t *testing.T) {
	for _, p := range []string{"/usr", "/private/etc", "/opt", "/Applications", "/nix/store", "/nix/var/nix/profiles", "/run/current-system"} {
		assert.Contains(t, platformLists.System, p)
	}
	assert.Equal(t, []string{"/opt/homebrew/var", "/usr/local/var"}, brewVar)
}
