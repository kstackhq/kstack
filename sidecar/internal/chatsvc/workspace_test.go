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

package chatsvc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// A chat's first question tells the model its workspace's path, after the
// card, and the path is fixed for the chat, so the next question with nothing
// else changed carries no context block.
func TestTheContextNamesTheWorkspace(t *testing.T) {
	s := serviceWithClusterCards(t, &stubClusterCards{card: "## Cluster\n\n```json\n{}\n```"})

	first := sendAndSettle(t, s, nil, "1", "1", "one")
	path, err := json.Marshal(tools.WorkspacePath(s.chatDir(first.ChatID)))
	require.NoError(t, err)
	context := newestContextOf(t, s, first.ChatID)
	assert.Contains(t, context, "## Cluster")
	assert.Contains(t, context, "## Workspace\n\n```json\n{\"path\":"+string(path)+"}\n```")

	sendAndSettle(t, s, &first.ChatID, "1", "2", "two")
	assert.Equal(t, []llm.BlockType{llm.BlockText}, blockTypes(t, questions(t, s, first.ChatID)[1]), "nothing changed")
}

// On a machine with a sandbox the context says where the chat's commands run,
// after the workspace, and says it again once the user switches the chat.
func TestTheContextSaysWhereCommandsRun(t *testing.T) {
	s := serviceWithClusterCards(t, &stubClusterCards{card: "## Cluster\n\n```json\n{}\n```"})
	s.sandboxStatus = sandbox.Status{Available: true}

	first := sendAndSettle(t, s, nil, "1", "1", "one")
	context := newestContextOf(t, s, first.ChatID)
	sandboxed := "## Sandbox\n\n```json\n{\"commands\":\"sandboxed\"}\n```"
	assert.Contains(t, context, sandboxed)
	assert.Greater(t, strings.Index(context, sandboxed), strings.Index(context, "## Workspace"), "after the workspace")

	_, err := s.SetSandboxDisabled(t.Context(), first.ChatID, true)
	require.NoError(t, err)
	sendAndSettle(t, s, &first.ChatID, "1", "2", "two")
	assert.Contains(t, newestContextOf(t, s, first.ChatID), "## Sandbox\n\n```json\n{\"commands\":\"outside\"}\n```")

	none := serviceWithClusterCards(t, &stubClusterCards{card: "## Cluster\n\n```json\n{}\n```"})
	msg := sendAndSettle(t, none, nil, "1", "1", "one")
	assert.NotContains(t, newestContextOf(t, none, msg.ChatID), "## Sandbox")
}

// withSandboxReplaced swaps the newest context's Sandbox section, which is
// always its last, and hands the context back unchanged when there is nothing
// to swap.
func TestWithSandboxReplacedSwapsTheLastSection(t *testing.T) {
	s := &service{sandboxStatus: sandbox.Status{Available: true}}
	card := "## Cluster\n\n```json\n{}\n```"

	assert.Equal(t, s.withSandbox(card, true), s.withSandboxReplaced(s.withSandbox(card, false), true))
	assert.Equal(t, s.withSandbox(card, false), s.withSandboxReplaced(s.withSandbox(card, true), false))
	assert.Equal(t, s.withSandbox(card, true), s.withSandboxReplaced(s.withSandbox(card, true), true), "already says so")
	assert.Equal(t, card, s.withSandboxReplaced(card, true), "no section to swap")

	none := &service{}
	assert.Equal(t, card, none.withSandboxReplaced(card, true), "no sandbox on this machine")
}
