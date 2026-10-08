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

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An action's kind is the field it sets, and none for an action that sets none.
func TestAnActionsKindIsItsField(t *testing.T) {
	assert.Equal(t, ActionCommand, Action{Command: &CommandAction{}}.Kind())
	assert.Equal(t, ActionRead, Action{Read: &ReadAction{}}.Kind())
	assert.Equal(t, ActionWrite, Action{Write: &WriteAction{}}.Kind())
	assert.Equal(t, ActionEdit, Action{Edit: &EditAction{}}.Kind())
	assert.Equal(t, ActionSearch, Action{Search: &SearchAction{}}.Kind())
	assert.Equal(t, ActionFetch, Action{Fetch: &FetchAction{}}.Kind())
	assert.Equal(t, ActionMemory, Action{Memory: &MemoryAction{}}.Kind())
	assert.Equal(t, ActionDelegate, Action{Delegate: &DelegateAction{}}.Kind())
	assert.Equal(t, ActionKubeQuery, Action{KubeQuery: &KubeQueryAction{}}.Kind())
	assert.Equal(t, ActionLogsView, Action{LogsView: &LogsViewAction{}}.Kind())
	assert.Equal(t, ActionKind(""), Action{Description: "no kind"}.Kind())
}

// ActionKinds is the set valid reads: each kind once, and nothing else.
func TestActionKindsIsTheSet(t *testing.T) {
	seen := map[ActionKind]bool{}
	for _, k := range ActionKinds {
		assert.False(t, seen[k], "%s twice", k)
		seen[k] = true
		assert.True(t, k.valid(), k)
	}
	assert.Len(t, ActionKinds, 11)
	assert.False(t, ActionKind("").valid())
	assert.False(t, ActionKind("unknown").valid())
}
