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
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoPathIsInTwoLists(t *testing.T) {
	for _, p := range slices.Concat(sharedLists.System, sharedLists.Never) {
		assert.NotContains(t, slices.Concat(platformLists.System, platformLists.Never), p)
	}
}

func TestEveryListPathIsAbsoluteOrInTheHome(t *testing.T) {
	for _, p := range slices.Concat(sharedLists.System, sharedLists.Never, platformLists.System, platformLists.Never) {
		assert.True(t, strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~/"), p)
	}
}

// Never is the Never paths with ~/ under the home, and with no home the
// absolute ones alone, since a ~/ path would be relative.
func TestNeverWithNoHomeIsTheAbsolutePaths(t *testing.T) {
	old := platformLists
	platformLists.Never = append(slices.Clone(old.Never), "/etc/kstack-secret")
	t.Cleanup(func() { platformLists = old })
	var s *Sandbox

	assert.Equal(t, []string{"/etc/kstack-secret"}, s.Never(""))
	assert.Contains(t, s.Never("/home/ana"), "/home/ana/.kube")
	assert.Contains(t, s.Never("/home/ana"), "/etc/kstack-secret")
	assert.Len(t, s.Never("/home/ana"), len(sharedLists.Never)+len(platformLists.Never))
}

// A PATH entry inside a Never path makes no rule, so a policy of System and
// Never passes Check.
func TestSystemLeavesOutTheNeverPaths(t *testing.T) {
	base := resolved(t.TempDir())
	home := filepath.Join(base, "home")
	d := mkdirs(t, home, ".docker/bin", "apps/bin")
	s := &Sandbox{self: "/usr/bin/true"}

	got := s.System(home, "/bin/sh", []string{"PATH=" + d[0] + ":" + d[1]})

	assert.NotContains(t, got.Read, filepath.Join(home, ".docker"))
	assert.Contains(t, got.Read, filepath.Join(home, "apps"))
	assert.NoError(t, Policy{Files: got, Always: AlwaysPolicy{Deny: s.Never(home)}}.Check())
}
