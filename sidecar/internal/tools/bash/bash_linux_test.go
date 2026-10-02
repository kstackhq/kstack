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
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// node reserves about 10 GiB of address space for a WebAssembly memory, which
// fits under the memory limit.
func TestNodeRunsUnderTheMemoryLimit(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node on PATH")
	}
	tl := proxyTool(t, &fakeLease{})
	tl.sandboxer = confining(t)

	text, isError := tl.Run(t.Context(), testRuntime(t), command(`node -e 'new WebAssembly.Memory({initial: 1})'`))

	require.False(t, isError, text)
}
