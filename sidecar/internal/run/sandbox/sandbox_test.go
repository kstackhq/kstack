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

// sandbox-init, sandbox-shell or sandbox-pasta as the first argument runs that part of a run,
// whose own arguments come after it; anything else is the caller's.
func TestMainRunsTheSubcommandItNames(t *testing.T) {
	for _, command := range []string{InitCommand, ShellCommand, PastaCommand} {
		code, ok := Main([]string{"kstack-sidecar", command})
		assert.True(t, ok, command)
		assert.Equal(t, initFailed, code, command)
	}
	for _, argv := range [][]string{
		{"kstack-sidecar"}, {"kstack-sidecar", "--socket", "s"}, {"kstack-sidecar", "--log-file", InitCommand},
	} {
		_, ok := Main(argv)
		assert.False(t, ok, argv)
	}
}

// A run holding a variable no run may hold fails its check, whatever its
// policy; one holding none passes as its policy does.
func TestARunsCheckRefusesAnUnpassableVariable(t *testing.T) {
	for _, kv := range []string{"LD_PRELOAD=/x.so", "AWS_SESSION_TOKEN=t"} {
		r := Run{Env: []string{"PATH=/usr/bin", kv}}
		assert.ErrorContains(t, r.check(), "may not pass", kv)
	}
	assert.NoError(t, Run{Env: []string{"PATH=/usr/bin", "HOME=/w"}}.check())
}
