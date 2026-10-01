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

package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gcloud and az install as batch wrappers; each argument reaches the wrapper
// quoted, and a cmd.exe metacharacter inside one is text.
func TestABatchWrapperRuns(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	bin := filepath.Join(dir, "gcloud.cmd")
	require.NoError(t, os.WriteFile(bin, []byte("@echo %*\r\n"), 0o600))

	stdout, _, exit, err := execRun(t.TempDir(), waitDelay)(t.Context(), nil, bin, "a b", "x&echo>"+marker)
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, `"a b" "x&echo>`+marker+`"`, strings.TrimSpace(string(stdout)))
	assert.NoFileExists(t, marker, "the & ran nothing")
}
