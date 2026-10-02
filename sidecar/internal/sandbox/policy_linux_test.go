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
	"github.com/stretchr/testify/require"
)

// A process that ignores SIGXCPU is killed at the hard limit, cpuGrace past
// the soft one.
func TestAnIgnoredSIGXCPUIsKilledAtTheHardLimit(t *testing.T) {
	s := confining(t)

	code, _ := limitedRun(t, s, Limits{CPUSeconds: 1}, nil, "/bin/sh", "-c", "trap '' XCPU; while :; do :; done")

	assert.Equal(t, 137, code)
}

// An allocation past the memory limit fails, where with none it succeeds.
func TestAllocatingPastTheMemoryLimitFails(t *testing.T) {
	s := confining(t)
	dd := []string{"/bin/sh", "-c", "dd if=/dev/zero of=/dev/null bs=1G count=1 2>&1"}

	code, out := limitedRun(t, s, Limits{}, nil, dd...)
	require.Equal(t, 0, code, out)

	code, out = limitedRun(t, s, Limits{MemoryBytes: 512 << 20, OpenFiles: 4096}, nil, dd...)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "memory", "dd says why it stopped")
}
