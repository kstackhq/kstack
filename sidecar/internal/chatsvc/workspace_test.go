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
	s.sandboxStatus = sandbox.Status{Available: true, NetworkAvailable: true}

	first := sendAndSettle(t, s, nil, "1", "1", "one")
	context := newestContextOf(t, s, first.ChatID)
	sandboxed := "## Sandbox\n\n```json\n{\"commands\":\"sandboxed\",\"network\":\"off\"}\n```"
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
	s := &service{sandboxStatus: sandbox.Status{Available: true, NetworkAvailable: true}}
	card := "## Cluster\n\n```json\n{}\n```"
	off, chat := sandboxState{}, sandboxState{networkEnabled: true}
	outside := sandboxState{outside: true}

	assert.Equal(t, s.withSandbox(card, outside), s.withSandboxReplaced(s.withSandbox(card, off), outside))
	assert.Equal(t, s.withSandbox(card, off), s.withSandboxReplaced(s.withSandbox(card, outside), off))
	assert.Equal(t, s.withSandbox(card, off), s.withSandboxReplaced(s.withSandbox(card, chat), off), "the network moved")
	assert.Equal(t, s.withSandbox(card, chat), s.withSandboxReplaced(s.withSandbox(card, chat), chat), "already says so")
	assert.Equal(t, s.withSandbox("", off), s.withSandboxReplaced(s.withSandbox("", chat), off), "the block's only section")
	assert.Equal(t, card, s.withSandboxReplaced(card, outside), "no section to swap")

	none := &service{}
	assert.Equal(t, card, none.withSandboxReplaced(card, outside), "no sandbox on this machine")
}

// sandboxSectionOf is the newest context's Sandbox section, decoded.
func sandboxSectionOf(t *testing.T, s *service, chatID ChatID) map[string]string {
	t.Helper()
	context := newestContextOf(t, s, chatID)
	_, section, ok := strings.Cut(context, "## Sandbox\n\n```json\n")
	require.True(t, ok, context)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(section, "\n```")), &got))
	return got
}

// The Sandbox section says the network a sandboxed chat's commands have: the
// chat's switch over the turn's toggle, off, or none on this machine at all.
// It is sent again whenever it says something new, so a question after one
// sent with the toggle says off.
func TestTheContextSaysTheNetwork(t *testing.T) {
	s := serviceWithClusterCards(t, &stubClusterCards{card: "## Cluster\n\n```json\n{}\n```"})
	s.sandboxStatus = sandbox.Status{Available: true, NetworkAvailable: true}

	first := sendAndSettle(t, s, nil, "1", "1", "one")
	assert.Equal(t, "off", sandboxSectionOf(t, s, first.ChatID)["network"])

	toggled, err := s.Send(t.Context(), &first.ChatID, ModeChat, "1", false, false, true, "fake", "fake", "high", reqID("2"), "two")
	require.NoError(t, err)
	awaitSettled(t, s, toggled.ChatID, toggled.ID)
	assert.Equal(t, "on for this message", sandboxSectionOf(t, s, first.ChatID)["network"])

	sendAndSettle(t, s, &first.ChatID, "1", "3", "three")
	assert.Equal(t, "off", sandboxSectionOf(t, s, first.ChatID)["network"], "the toggle was that message's alone")

	_, err = s.SetNetworkEnabled(t.Context(), first.ChatID, true)
	require.NoError(t, err)
	switched, err := s.Send(t.Context(), &first.ChatID, ModeChat, "1", false, true, true, "fake", "fake", "high", reqID("4"), "four")
	require.NoError(t, err)
	awaitSettled(t, s, switched.ChatID, switched.ID)
	assert.Equal(t, "on for this chat", sandboxSectionOf(t, s, first.ChatID)["network"])

	_, err = s.SetSandboxDisabled(t.Context(), first.ChatID, true)
	require.NoError(t, err)
	// Leaving the sandbox turned the switch off, so the sender sees off.
	outside, err := s.Send(t.Context(), &first.ChatID, ModeChat, "1", true, false, false, "fake", "fake", "high", reqID("5"), "five")
	require.NoError(t, err)
	awaitSettled(t, s, outside.ChatID, outside.ID)
	assert.Equal(t, map[string]string{"commands": "outside"}, sandboxSectionOf(t, s, first.ChatID), "outside, there is no network line")

	none := serviceWithClusterCards(t, &stubClusterCards{card: "## Cluster\n\n```json\n{}\n```"})
	none.sandboxStatus = sandbox.Status{Available: true, NetworkReason: "pasta not found"}
	msg := sendAndSettle(t, none, nil, "1", "1", "one")
	assert.Equal(t, "unavailable on this machine", sandboxSectionOf(t, none, msg.ChatID)["network"])
}
