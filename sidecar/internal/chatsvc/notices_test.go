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
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/session"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// A notice turn renders no card, so it carries a context block only when the
// chat's switch moved since the newest one: the model then learns where the
// turn's commands run before it runs one.
func TestANoticeTurnSaysWhereCommandsRunWhenTheSwitchMoved(t *testing.T) {
	tt := newTaskTool()
	s := startServiceWithTool(t, tt)
	s.sandboxStatus = sandbox.Status{Available: true}
	first, ft := startTaskTurn(t, s, tt, nil, "1")
	sandboxed := newestContextOf(t, s, first.ChatID)

	_, err := s.SetSandboxDisabled(t.Context(), first.ChatID, true)
	require.NoError(t, err)
	ft.exit(0)
	answer := awaitNoticeTurn(t, s, first.ChatID, first.ID)

	q := questionOf(t, s, answer)
	require.Equal(t, []llm.BlockType{llm.BlockContext, llm.BlockTaskNotification}, []llm.BlockType{q[0].Type, q[1].Type})
	outside := strings.Replace(sandboxed, `{"commands":"sandboxed","network":"unavailable on this machine"}`, `{"commands":"outside"}`, 1)
	assert.Equal(t, outside, newestContextOf(t, s, first.ChatID), "the newest block with the switch swapped")
}

// So does a notice turn whose chat's network switch moved: the model learns
// what its commands reach before it runs one.
func TestANoticeTurnSaysTheNetworkWhenTheSwitchMoved(t *testing.T) {
	tt := newTaskTool()
	s := startServiceWithTool(t, tt)
	s.sandboxStatus = sandbox.Status{Available: true, NetworkAvailable: true}
	first, ft := startTaskTurn(t, s, tt, nil, "1")
	off := newestContextOf(t, s, first.ChatID)

	_, err := s.SetNetworkEnabled(t.Context(), first.ChatID, true)
	require.NoError(t, err)
	ft.exit(0)
	answer := awaitNoticeTurn(t, s, first.ChatID, first.ID)

	q := questionOf(t, s, answer)
	require.Equal(t, llm.BlockContext, q[0].Type)
	on := strings.Replace(off, `"network":"off"`, `"network":"on for this chat"`, 1)
	assert.Equal(t, on, newestContextOf(t, s, first.ChatID))
}

// A notice turn whose switch is where the newest context says carries none.
func TestANoticeTurnCarriesNoContextWhenTheSwitchStayed(t *testing.T) {
	tt := newTaskTool()
	s := startServiceWithTool(t, tt)
	s.sandboxStatus = sandbox.Status{Available: true}
	first, ft := startTaskTurn(t, s, tt, nil, "1")

	ft.exit(0)
	answer := awaitNoticeTurn(t, s, first.ChatID, first.ID)

	q := questionOf(t, s, answer)
	require.Len(t, q, 1)
	assert.Equal(t, llm.BlockTaskNotification, q[0].Type)
}

// A notice turn makes every check a send makes, the length check included: a
// task that exits in a chat past the ceiling starts no turn, files nothing, and
// its notice waits for a question on a model that can read the chat.
func TestAFullChatStartsNoNoticeTurn(t *testing.T) {
	tt := newTaskTool()
	s, f := windowed(t, 200_000, 64_000, 0, tt)
	f.SetToolCalls(taskCall())
	f.SetUsage(reads(10), reads(150_000))
	first := converse(t, s, nil, "fake", "fake", "start it")
	ft := testutil.Recv(t, tt.ready, "the task to start")
	done := taskDoneOf(t, s, first.ChatID, ft)

	ft.exit(0)
	testutil.Wait(t, done, "the row")

	assert.Nil(t, s.turnOf(first.ChatID), "no turn started")
	msgs, err := s.transcript(t.Context(), first.ChatID)
	require.NoError(t, err)
	assert.Len(t, msgs, 2, "nothing filed")
	assert.Equal(t, 1, tableCount(t, s.db, "agent_runs"))
	assert.False(t, taskRows(t, s.db)[0].notified, "the notice waits")
}

// A notice names its task in one short line, whatever the model wrote: the first
// line with anything on it, cut to 200 characters, the transcript's rule.
func TestANoticeHoldsOneLine(t *testing.T) {
	assert.Equal(t, "List files…", noticeLine("List files\nthen delete them"))
	assert.Equal(t, strings.Repeat("é", 200)+"…", noticeLine(strings.Repeat("é", 300)))
	assert.Equal(t, "List files", noticeLine("List files"))
	assert.Equal(t, "List files", noticeLine("List files  \n \n"), "no mark for whitespace alone")
	assert.Equal(t, "List files", noticeLine("\n  \nList files"))
	assert.Empty(t, noticeLine(" \n\t "))

	n := noticeOf(waitingNotice{
		id: "t1", toolUseID: "call-1", path: "/p", status: taskExited,
		description: "Serve the site\nand watch it", command: "make serve && " + strings.Repeat("x", 250),
	})
	assert.Equal(t, "Serve the site…", n.Description)
	assert.Equal(t, "make serve && "+strings.Repeat("x", 186)+"…", n.Command)
}

// An agent whose run succeeded with no report could not answer, and its notice
// says so in place of an error.
func TestAnAgentWithNothingToSayIsTold(t *testing.T) {
	n := noticeOf(waitingNotice{id: "t1", path: "/p", status: taskFailed, agent: true, description: "look"})
	assert.Equal(t, "it had nothing to say", n.Error)
	assert.Empty(t, n.OutputFile, "a failed agent's file is empty")
}

// A stopped or lost agent's file is empty, so its notice names none.
func TestAStoppedAgentsNoticeNamesNoFile(t *testing.T) {
	for _, status := range []string{taskStopped, taskLost} {
		n := noticeOf(waitingNotice{id: "t1", path: "/p", status: status, agent: true, description: "look"})
		assert.Empty(t, n.OutputFile, status)
		assert.Empty(t, n.Error, status)
	}
}

// A notice turn also carries the context when the chat's folders changed
// since the newest one, so the model learns what its commands now read.
func TestANoticeTurnListsTheGrantsWhenTheyChanged(t *testing.T) {
	tt := newTaskTool()
	s := startServiceWithTool(t, tt)
	home := grantable(t, s)
	first, ft := startTaskTurn(t, s, tt, nil, "1")
	before := newestContextOf(t, s, first.ChatID)
	code := filepath.Join(home, "code")

	require.NoError(t, s.GrantFolder(t.Context(), first.ChatID, code, false))
	ft.exit(0)
	answer := awaitNoticeTurn(t, s, first.ChatID, first.ID)

	q := questionOf(t, s, answer)
	require.Equal(t, []llm.BlockType{llm.BlockContext, llm.BlockTaskNotification}, []llm.BlockType{q[0].Type, q[1].Type})
	after := newestContextOf(t, s, first.ChatID)
	assert.Equal(t, s.withSandboxReplaced(before, sandboxState{folders: []session.Folder{{Path: code}}}), after)
	assert.Contains(t, after, `"read":[`)
}

// A grant made between a notice turn's folder read and its transaction still
// reaches the turn's context.
func TestANoticeTurnListsAGrantMadeAsItStarts(t *testing.T) {
	tt := newTaskTool()
	s := startServiceWithTool(t, tt)
	home := grantable(t, s)
	first, ft := startTaskTurn(t, s, tt, nil, "1")
	awaitTurnDone(t, s, first.ChatID)
	before := newestContextOf(t, s, first.ChatID)
	code := filepath.Join(home, "code")

	var once sync.Once
	granted := make(chan error, 1)
	s.onFoldersRead = func() {
		once.Do(func() { granted <- s.GrantFolder(t.Context(), first.ChatID, code, false) })
	}
	ft.exit(0)
	require.NoError(t, testutil.Recv(t, granted, "the grant"))
	answer := awaitNoticeTurn(t, s, first.ChatID, first.ID)

	q := questionOf(t, s, answer)
	require.Len(t, q, 2, "a context block, then the notice")
	require.Equal(t, []llm.BlockType{llm.BlockContext, llm.BlockTaskNotification}, []llm.BlockType{q[0].Type, q[1].Type})
	assert.Equal(t, s.withSandboxReplaced(before, sandboxState{folders: []session.Folder{{Path: code}}}), newestContextOf(t, s, first.ChatID))
}
