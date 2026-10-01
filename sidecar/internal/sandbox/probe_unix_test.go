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

//go:build !windows

package sandbox

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The probe's policy is System over its shell and PATH, its folder writable,
// and the Never paths denied, with no Kstack paths and no relay. It passes
// Check with ~/.docker/bin on its PATH, and with no home at all.
func TestTheProbeKeepsTheCredentialsHidden(t *testing.T) {
	base := resolved(t.TempDir())
	home := filepath.Join(base, "home")
	docker := mkdirs(t, home, ".docker/bin")[0]
	dir := mkdirs(t, base, "probe")[0]
	s := &Sandbox{self: "/usr/bin/true"}
	env := []string{"PATH=/usr/bin:/bin:" + docker}

	p := s.probePolicy("/bin/sh", env, dir, home)

	require.NoError(t, p.Check())
	assert.Equal(t, s.System(home, "/bin/sh", env).Read, p.Files.Read)
	assert.Equal(t, []string{dir}, p.Files.Write)
	assert.Equal(t, AlwaysPolicy{Deny: s.Never(home)}, p.Always)
	assert.Empty(t, p.Network.Relays)

	p = s.probePolicy("/bin/sh", env, dir, "")

	require.NoError(t, p.Check())
	assert.Equal(t, s.Never(""), p.Always.Deny)
	for _, d := range p.Always.Deny {
		assert.True(t, filepath.IsAbs(d), d)
	}
}
