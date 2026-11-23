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

package realpath

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSameFileNoFollowHoldsAFolderInAnotherCase(t *testing.T) {
	base := canonicalOf(t, t.TempDir())[0]
	folder := mkdirs(t, base, "Disk/kube")[0]
	upper := filepath.Join(base, "DISK", "KUBE")
	skipIfCaseSensitive(t, upper)

	assert.True(t, SameFileNoFollow(upper, statOf(t, folder)))
}
