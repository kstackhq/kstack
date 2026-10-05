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

package securityconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

func TestAFolderRuleIsShapeChecked(t *testing.T) {
	read := `{"id": "r", "effect": "allow", "class": 1, "folder": "/nowhere/on/this/disk"}`
	s, _ := openFile(t, `{"rules": [`+read+`, {"id": "w", "effect": "allow", "class": 2, "folder": "/also/nowhere"}]}`)
	assert.False(t, s.Held("rules"), "the read-back reads the value alone, never the disk")
	assert.Len(t, s.Rules(), 2)

	for name, bad := range map[string]permissions.Rule{
		"relative":      {ID: "b", Effect: permissions.Allow, Class: permissions.ReadInside, Folder: "code/svc"},
		"not clean":     {ID: "b", Effect: permissions.Allow, Class: permissions.ReadInside, Folder: "/Users/me/../me/code"},
		"the root":      {ID: "b", Effect: permissions.Allow, Class: permissions.WriteInside, Folder: "/"},
		"no folder":     {ID: "b", Effect: permissions.Allow, Class: permissions.ReadInside},
		"a deny":        {ID: "b", Effect: permissions.Deny, Class: permissions.ReadInside, Folder: "/Users/me/code"},
		"an ask":        {ID: "b", Effect: permissions.AskFor, Class: permissions.WriteInside, Folder: "/Users/me/code"},
		"a context":     {ID: "b", Effect: permissions.Allow, Class: permissions.ReadInside, Folder: "/Users/me/code", Context: "dev"},
		"a kind":        {ID: "b", Effect: permissions.Allow, Class: permissions.WriteInside, Folder: "/Users/me/code", Kind: "pods"},
		"cluster class": {ID: "b", Effect: permissions.Deny, Class: permissions.UpstreamWrite, Folder: "/Users/me/code"},
	} {
		err := s.AddRule(bad)
		var r Refusal
		require.ErrorAs(t, err, &r, name)
		assert.NotEmpty(t, r.Reason, name)
	}
	assert.Len(t, s.Get().Rules, 2, "a refused add writes nothing")

	err := s.AddRule(permissions.Rule{ID: "b", Effect: permissions.Deny, Class: permissions.NewHost})
	var r Refusal
	require.ErrorAs(t, err, &r)
	assert.Equal(t, "names a class no rule decides: only 1 (folder reads), 2 (folder reads and writes), "+
		"4 (cluster writes), 5 (destructive cluster writes) and 6 (Secret reads)", r.Reason)
}
