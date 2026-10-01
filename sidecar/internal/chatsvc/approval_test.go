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
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// toolCallsOf is a message's list as the wire serves it.
func toolCallsOf(t *testing.T, m ChatMessage) []ToolCall {
	t.Helper()
	var out []ToolCall
	require.NoError(t, json.Unmarshal([]byte(m.ToolCalls), &out))
	return out
}

// awaitToolCall watches chatID until message id's first call has status, and
// returns the message as the watch served it.
func awaitToolCall(t *testing.T, s *service, chatID ChatID, id MessageID, status ToolCallStatus) ChatMessage {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := s.WatchMessages(ctx, chatID)
	require.NoError(t, err)
	f := awaitFrame(t, st.Frames, func(f ChatMessageWatchFrame) bool {
		if f.Message == nil || f.Message.ID != id {
			return false
		}
		var calls []ToolCall
		return json.Unmarshal([]byte(f.Message.ToolCalls), &calls) == nil && len(calls) > 0 && calls[0].Status == status
	})
	return *f.Message
}

// awaitRequest waits for the message's first call to be put to the user, and
// returns the message and the approval to decide.
func awaitRequest(t *testing.T, s *service, msg ChatMessage) (ChatMessage, ApprovalID) {
	t.Helper()
	got := awaitToolCall(t, s, msg.ChatID, msg.ID, ToolCallAwaitingApproval)
	calls := toolCallsOf(t, got)
	require.NotNil(t, calls[0].Approval)
	return got, calls[0].Approval.ID
}

// approve decides id and requires that a turn was waiting on it.
func approve(t *testing.T, s *service, id ApprovalID, yes bool) {
	t.Helper()
	ok, err := s.Approve(t.Context(), id, yes)
	require.NoError(t, err)
	require.True(t, ok, "a turn was waiting on the approval")
}

// approvalRow is what one approvals row stored.
type approvalRow struct {
	status  ApprovalStatus
	decided bool
}

func approvalRows(t *testing.T, db *appdb.DB) []approvalRow {
	t.Helper()
	rows, err := db.Read.Query(`SELECT status, decided_at FROM approvals ORDER BY created_at, id`)
	require.NoError(t, err)
	defer rows.Close()
	var out []approvalRow
	for rows.Next() {
		var (
			r       approvalRow
			decided sql.NullInt64
		)
		require.NoError(t, rows.Scan(&r.status, &decided))
		r.decided = decided.Valid
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// isMutating is a tool call row's is_mutating, by its tool_use_id.
func isMutating(t *testing.T, db *appdb.DB, useID string) bool {
	t.Helper()
	var v bool
	require.NoError(t, db.Read.QueryRow(`SELECT is_mutating FROM tool_calls WHERE tool_use_id = ?`, useID).Scan(&v))
	return v
}

// skipTool is a gated tool whose every approval skips.
type skipTool struct{ testTool }

func (skipTool) Approval(context.Context, tools.Runtime, json.RawMessage) (tools.Approval, error) {
	return tools.Approval{Skip: true}, nil
}

// A gated call whose approval skips runs with nothing asked, and its row is an
// ungated call's: no approval, is_mutating 0, running then succeeded.
func TestASkippedApprovalRunsUnasked(t *testing.T) {
	s := startServiceWithTool(t, skipTool{testTool{name: "Read"}})
	fakeOf(s).SetToolCalls(llm.StagedCall("Read", `{"file_path":"/x"}`))

	msg := send(t, s, nil, "1", "hi")
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, StatusComplete, got.Status)
	calls := toolCallsOf(t, got)
	require.Len(t, calls, 1)
	assert.Nil(t, calls[0].Approval)
	assert.Equal(t, ToolCallSucceeded, calls[0].Status)
	assert.Empty(t, approvalRows(t, s.db))
	assert.False(t, isMutating(t, s.db, "call-1"))
	rows := toolCallRows(t, s.db, msg.RunID)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].hasStarted)
}

// A command is a committed row and an approval before anything runs: the row
// awaiting_approval with no started_at, the approval pending, the run and the
// message waiting, and the list carrying the approval id beside the exact
// command and the description shown with it.
func TestACommandWaitsOnTheUser(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	command := `kubectl get pods > out && cat out | grep "é"`
	fakeOf(s).SetToolCalls(describedBashCall(command, "List pods\nand grep them"))

	msg := send(t, s, nil, "1", "hi")
	got, _ := awaitRequest(t, s, msg)

	assert.Equal(t, StatusWaitingApproval, got.Status)
	calls := toolCallsOf(t, got)
	require.NotNil(t, calls[0].Action)
	assert.Equal(t, command, calls[0].Action.Command.Text, "the request is the command byte for byte")
	assert.Equal(t, "List pods\nand grep them", calls[0].Action.Description)
	assert.Equal(t, ApprovalPending, calls[0].Approval.Status)
	assert.Equal(t, "Bash", calls[0].Name)
	ran := sh.commands()
	assert.Empty(t, ran, "nothing ran")
	rows := toolCallRows(t, s.db, msg.RunID)
	require.Len(t, rows, 1)
	assert.Equal(t, toolAwaitingApproval, rows[0].status)
	assert.False(t, rows[0].hasStarted)
	assert.True(t, isMutating(t, s.db, "call-1"))
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))
	assert.Equal(t, runWaitingApproval, runStatusOf(t, s.db, msg.RunID))
}

// A background command's request says so, on the live list and the stored read
// alike.
func TestABackgroundApprovalSaysSo(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(llm.StagedCall("Bash", `{"command":"make serve","run_in_background":true}`))

	msg := send(t, s, nil, "1", "hi")
	got, _ := awaitRequest(t, s, msg)

	assert.True(t, toolCallsOf(t, got)[0].Action.Command.Background, "the live list")
	stored, err := s.transcript(t.Context(), msg.ChatID)
	require.NoError(t, err)
	assert.True(t, toolCallsOf(t, stored[1])[0].Action.Command.Background, "the stored read")
}

// The call's first write is awaiting_approval, with its approval; nothing writes
// a pending row first.
func TestACommandRowStartsAwaiting(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	_, err := s.db.Write.Exec(`CREATE TABLE first_status (status TEXT);
		CREATE TRIGGER log_insert AFTER INSERT ON tool_calls BEGIN INSERT INTO first_status VALUES (NEW.status); END`)
	require.NoError(t, err)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	awaitSettled(t, s, msg.ChatID, msg.ID)

	var first []string
	rows, err := s.db.Read.Query(`SELECT status FROM first_status`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var st string
		require.NoError(t, rows.Scan(&st))
		first = append(first, st)
	}
	assert.Equal(t, []string{toolAwaitingApproval}, first)
}

// A decision that arrives before the turn reaches its wait is kept for it, and a
// second decision finds no one waiting.
func TestAFastDecisionFindsItsWaiter(t *testing.T) {
	s := newTestService(t)
	id := newApprovalID()
	decision := s.await(id)

	ok, err := s.Approve(t.Context(), id, true)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, <-decision)

	ok, err = s.Approve(t.Context(), id, false)
	require.NoError(t, err)
	assert.False(t, ok, "a second decision finds no waiter")
	ok, err = s.Approve(t.Context(), newApprovalID(), true)
	require.NoError(t, err)
	assert.False(t, ok, "nor does an id nobody minted")
}

// Approved, the command is shown running, then done, with what the model read.
func TestAnApprovedCommandIsShownRunningThenDone(t *testing.T) {
	release := make(chan struct{})
	sh := &fakeBash{run: func(context.Context, string) (string, bool) {
		<-release
		return "three pods\n", false
	}}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	running := awaitToolCall(t, s, msg.ChatID, msg.ID, ToolCallRunning)
	assert.Equal(t, ApprovalApproved, toolCallsOf(t, running)[0].Approval.Status)
	close(release)
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	calls := toolCallsOf(t, got)
	assert.Equal(t, ToolCallSucceeded, calls[0].Status)
	assert.Equal(t, "three pods\n", calls[0].Output)
	assert.Equal(t, "ls", calls[0].Action.Command.Text)
	rows := toolCallRows(t, s.db, msg.RunID)
	assert.Equal(t, toolSucceeded, rows[0].status)
	assert.True(t, rows[0].hasStarted)
	assert.Equal(t, []approvalRow{{status: ApprovalApproved, decided: true}}, approvalRows(t, s.db))
}

// Denied, nothing runs: the model is answered denied, the row is denied with no
// started_at, and the approval says what the user decided.
func TestADeniedCommandDoesNotRun(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("rm -rf ~"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, false)
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, StatusComplete, got.Status)
	ran := sh.commands()
	assert.Empty(t, ran)
	calls := toolCallsOf(t, got)
	assert.Equal(t, ToolCallDenied, calls[0].Status)
	assert.Equal(t, `{"error":"denied"}`, calls[0].Output)
	rows := toolCallRows(t, s.db, msg.RunID)
	assert.Equal(t, toolDenied, rows[0].status)
	assert.False(t, rows[0].hasStarted)
	assert.Equal(t, []approvalRow{{status: ApprovalDenied, decided: true}}, approvalRows(t, s.db))
}

// The decision flips the run and the message back from waiting while the command
// runs.
func TestTheRunFlipsBackOnTheDecision(t *testing.T) {
	release := make(chan struct{})
	sh := &fakeBash{run: func(context.Context, string) (string, bool) {
		<-release
		return "ok", false
	}}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	running := awaitToolCall(t, s, msg.ChatID, msg.ID, ToolCallRunning)

	assert.Equal(t, StatusStreaming, running.Status)
	assert.Equal(t, runRunning, runStatusOf(t, s.db, msg.RunID))
	close(release)
	awaitSettled(t, s, msg.ChatID, msg.ID)
}

// A bash call the budget refused asks no one and is still listed, not run.
func TestARefusedBashCallIsStillListed(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	calls := make([]llm.Block, maxToolCalls+1)
	for i := range calls {
		calls[i] = bashCall("ls")
	}
	fakeOf(s).SetToolCalls(calls...)

	msg := send(t, s, nil, "1", "hi")
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	listed := toolCallsOf(t, got)
	require.Len(t, listed, maxToolCalls+1)
	for _, c := range listed {
		assert.Equal(t, ToolCallNotRun, c.Status)
		assert.Equal(t, `{"error":"budget"}`, c.Output)
	}
	assert.Empty(t, approvalRows(t, s.db))
}

// A cancel while the request is up runs nothing: the call is answered cancelled
// with no started_at, and a decision that arrives after finds no one waiting.
func TestACancelledWaitRunsNothing(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	got := awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitTurnDone(t, s, msg.ChatID)

	assert.Equal(t, StatusCancelled, got.Status)
	ran := sh.commands()
	assert.Empty(t, ran)
	assert.Equal(t, ToolCallNotRun, toolCallsOf(t, got)[0].Status)
	ok, err := s.Approve(t.Context(), id, true)
	require.NoError(t, err)
	assert.False(t, ok, "a late decision changes nothing")
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))
}

// A cancel while an approved command runs kills it: the row keeps started_at and
// reads as interrupted, since it may have run.
func TestACancelledRunIsInterrupted(t *testing.T) {
	started := make(chan struct{})
	sh := &fakeBash{run: func(ctx context.Context, _ string) (string, bool) {
		close(started)
		<-ctx.Done()
		return "killed", true
	}}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("sleep 60"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	<-started
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, ToolCallInterrupted, toolCallsOf(t, got)[0].Status)
	rows := toolCallRows(t, s.db, msg.RunID)
	assert.True(t, rows[0].hasStarted)
	assert.JSONEq(t, `{"error":"cancelled"}`, rows[0].errText)
}

// A cancel observed before the decision leaves the record of an unanswered
// question: the approval pending and undecided, the call not run, the run cancelled.
func TestACancelBeforeTheDecisionCommitsLeavesItPending(t *testing.T) {
	s := startServiceWithTool(t, &fakeBash{})
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	awaitRequest(t, s, msg)
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitTurnDone(t, s, msg.ChatID)

	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))
	rows := toolCallRows(t, s.db, msg.RunID)
	assert.Equal(t, toolFailed, rows[0].status)
	assert.False(t, rows[0].hasStarted)
	assert.Equal(t, runCancelled, runStatusOf(t, s.db, msg.RunID))
	s.turnsMu.Lock()
	assert.Empty(t, s.pending, "the waiter was taken back")
	s.turnsMu.Unlock()
}

// Once the decision is taken it commits and stands, and a cancel landing as it is
// written stops the command before it starts: the user approved, and nothing ran.
func TestACancelAfterTheDecisionCommitsKeepsItAndRunsNothing(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("ls"))
	var (
		mu    sync.Mutex
		armed func()
	)
	// The decision's time is read after the wait took it and before its write, so a
	// cancel fired there lands while the write commits.
	s.now = func() time.Time {
		mu.Lock()
		fire := armed
		armed = nil
		mu.Unlock()
		if fire != nil {
			fire()
		}
		return time.Now()
	}

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	mu.Lock()
	armed = func() { _ = s.Cancel(context.Background(), msg.ChatID) }
	mu.Unlock()
	approve(t, s, id, true)
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	ran := sh.commands()
	assert.Empty(t, ran)
	assert.Equal(t, []approvalRow{{status: ApprovalApproved, decided: true}}, approvalRows(t, s.db))
	calls := toolCallsOf(t, got)
	assert.Equal(t, ToolCallNotRun, calls[0].Status, "the command did not run")
	assert.Equal(t, ApprovalApproved, calls[0].Approval.Status, "and the user approved it")
	rows := toolCallRows(t, s.db, msg.RunID)
	assert.Equal(t, toolFailed, rows[0].status)
	assert.False(t, rows[0].hasStarted)
	assert.JSONEq(t, `{"error":"cancelled"}`, rows[0].errText)
}

// A request that cannot be written is never shown and never runs: the call is
// refused not-run and the turn fails.
func TestAFailedApprovalWriteRefusesTheCommand(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	_, err := s.db.Write.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON approvals BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, StatusFailed, got.Status)
	ran := sh.commands()
	assert.Empty(t, ran)
	assert.Empty(t, approvalRows(t, s.db))
	rows := toolCallRows(t, s.db, msg.RunID)
	require.Len(t, rows, 1)
	assert.JSONEq(t, `{"error":"not-run"}`, rows[0].errText)
	assert.False(t, rows[0].hasStarted)
	s.turnsMu.Lock()
	assert.Empty(t, s.pending, "the waiter was taken back")
	s.turnsMu.Unlock()
}

// A decision the store refuses ends the turn with the store's error; the approval
// stays pending, the question recorded and its answer not, and nothing runs.
func TestTheCommandWritesReportWhatTheStoreRefuses(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	_, err := s.db.Write.Exec(`CREATE TRIGGER refuse BEFORE UPDATE ON approvals WHEN NEW.status != 'pending'
		BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, StatusFailed, got.Status)
	assert.Contains(t, got.Error, "refused")
	ran := sh.commands()
	assert.Empty(t, ran)
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))
	assert.Equal(t, ToolCallNotRun, toolCallsOf(t, got)[0].Status)
}

// An approved command whose running write fails never starts: its row is closed
// not-run with no started_at, and the shell is never called.
func TestAFailedStartWriteLeavesTheCommandNotRun(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	_, err := s.db.Write.Exec(`CREATE TRIGGER refuse BEFORE UPDATE ON tool_calls WHEN NEW.status = 'running'
		BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)
	fakeOf(s).SetToolCalls(bashCall("ls"))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	ran := sh.commands()
	assert.Empty(t, ran)
	assert.Equal(t, ToolCallNotRun, toolCallsOf(t, got)[0].Status)
	rows := toolCallRows(t, s.db, msg.RunID)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].hasStarted)
	assert.JSONEq(t, `{"error":"not-run"}`, rows[0].errText)
}

// Whichever way a gated call ends, it is one row under its seq with its approval
// still joined.
func TestEveryEndOfAGatedCallClosesItsOneRow(t *testing.T) {
	for name, tc := range map[string]struct {
		trigger string
		end     func(t *testing.T, s *service, msg ChatMessage, id ApprovalID)
	}{
		"denied": {end: func(t *testing.T, s *service, _ ChatMessage, id ApprovalID) { approve(t, s, id, false) }},
		"cancelled while waiting": {end: func(t *testing.T, s *service, msg ChatMessage, _ ApprovalID) {
			require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
		}},
		"a failed decision write": {
			trigger: `CREATE TRIGGER refuse BEFORE UPDATE ON approvals WHEN NEW.status != 'pending' BEGIN SELECT RAISE(ABORT, 'refused'); END`,
			end:     func(t *testing.T, s *service, _ ChatMessage, id ApprovalID) { approve(t, s, id, true) },
		},
		"a failed start write": {
			trigger: `CREATE TRIGGER refuse BEFORE UPDATE ON tool_calls WHEN NEW.status = 'running' BEGIN SELECT RAISE(ABORT, 'refused'); END`,
			end:     func(t *testing.T, s *service, _ ChatMessage, id ApprovalID) { approve(t, s, id, true) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := startServiceWithTool(t, &fakeBash{})
			if tc.trigger != "" {
				_, err := s.db.Write.Exec(tc.trigger)
				require.NoError(t, err)
			}
			fakeOf(s).SetToolCalls(bashCall("ls"))
			msg := send(t, s, nil, "1", "hi")
			_, id := awaitRequest(t, s, msg)
			tc.end(t, s, msg, id)
			awaitSettled(t, s, msg.ChatID, msg.ID)
			awaitTurnDone(t, s, msg.ChatID)

			rows := toolCallRows(t, s.db, msg.RunID)
			require.Len(t, rows, 1)
			assert.Equal(t, 0, rows[0].seq)
			assert.True(t, rows[0].hasFinished)
			var joined int
			require.NoError(t, s.db.Read.QueryRow(`SELECT COUNT(*) FROM approvals a JOIN tool_calls t ON t.id = a.tool_call_id`).Scan(&joined))
			assert.Equal(t, 1, joined)
		})
	}
}

// A call queued behind a command whose wait was cancelled is answered cancelled
// and never asked about, let alone run.
func TestACallQueuedBehindACancelledWaitRunsNothing(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall("ls"), bashCall("rm -rf /tmp/x"))

	msg := send(t, s, nil, "1", "hi")
	awaitRequest(t, s, msg)
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Empty(t, sh.commands())
	calls := toolCallsOf(t, got)
	require.Len(t, calls, 2)
	assert.Equal(t, ToolCallNotRun, calls[1].Status)
	assert.Equal(t, `{"error":"cancelled"}`, calls[1].Output)
	assert.Nil(t, calls[1].Approval, "the second command was never put to the user")
	assert.Len(t, approvalRows(t, s.db), 1)
}

// seedWait files a turn stopped on a command, as a process that died with the
// request up leaves it: the run waiting, the call's row awaiting_approval and
// its approval pending. content is the answer's stored content.
func seedWait(t *testing.T, db *appdb.DB, chatID ChatID, command, content string) (seededTurn, ApprovalID) {
	t.Helper()
	now := time.UnixMilli(1_000).UTC()
	turn := seedTurn(t, db, chatID, now)
	setRunStatus(t, db, turn.Run, runWaitingApproval)
	_, err := db.Write.Exec(`UPDATE messages SET content = ? WHERE id = ?`, content, string(turn.Assistant))
	require.NoError(t, err)
	call, row, id := appdb.NewID(), appdb.NewID(), newApprovalID()
	args, _ := json.Marshal(map[string]string{"command": command})
	_, err = db.Write.Exec(`INSERT INTO llm_calls (id, run_id, seq, provider, model, started_at) VALUES (?, ?, 0, 'fake', 'fake', 0)`, call, string(turn.Run))
	require.NoError(t, err)
	_, err = db.Write.Exec(`INSERT INTO tool_calls (id, llm_call_id, seq, tool_name, tool_use_id, arguments, is_mutating, status, created_at)
		VALUES (?, ?, 0, 'Bash', 'toolu_1', ?, 1, 'awaiting_approval', 0)`, row, call, string(args))
	require.NoError(t, err)
	_, err = db.Write.Exec(`INSERT INTO approvals (id, tool_call_id, status, created_at) VALUES (?, ?, 'pending', 0)`, string(id), row)
	require.NoError(t, err)
	return turn, id
}

// The directory a command runs in is on its request: in the tool_calls row, on
// the live list, and on the stored read once the turn has settled.
func TestTheToolCallRowHoldsTheCwd(t *testing.T) {
	s := startServiceWithTool(t, &fakeBash{})
	// Absolute on every OS; the request reads it without touching the disk.
	workdir := filepath.Join(t.TempDir(), "app")
	input, _ := json.Marshal(map[string]string{"command": "ls", "workdir": workdir})
	fakeOf(s).SetToolCalls(llm.StagedCall("Bash", string(input)))

	msg := send(t, s, nil, "1", "hi")
	got, id := awaitRequest(t, s, msg)
	assert.Equal(t, workdir, toolCallsOf(t, got)[0].Action.Command.Cwd)
	var cwd string
	require.NoError(t, s.db.Read.QueryRow(`SELECT cwd FROM tool_calls`).Scan(&cwd))
	assert.Equal(t, workdir, cwd)

	approve(t, s, id, true)
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitTurnDone(t, s, msg.ChatID)
	msgs, err := s.readMessages(t.Context(), msg.ChatID)
	require.NoError(t, err)
	assert.Equal(t, workdir, toolCallsOf(t, msgs[1])[0].Action.Command.Cwd)
	require.NoError(t, s.db.Read.QueryRow(`SELECT cwd FROM tool_calls`).Scan(&cwd))
	assert.Equal(t, workdir, cwd, "the settle's rewrite keeps it")
}

// approvingTool is a testTool gated by a fixed approval.
type approvingTool struct {
	testTool
	approval tools.Approval
}

func (a approvingTool) Approval(context.Context, tools.Runtime, json.RawMessage) (tools.Approval, error) {
	return a.approval, nil
}

// cwdAndSandboxed is the one tool_calls row's cwd and sandboxed.
func cwdAndSandboxed(t *testing.T, s *service) (string, bool) {
	t.Helper()
	var cwd string
	var sandboxed bool
	require.NoError(t, s.db.Read.QueryRow(`SELECT cwd, sandboxed FROM tool_calls`).Scan(&cwd, &sandboxed))
	return cwd, sandboxed
}

// Whether a sandbox confined a call is on its row beside the cwd, from the first
// write through the settle, whether the call asked or skipped the question.
func TestASandboxedCallIsRecordedAsSandboxed(t *testing.T) {
	for _, skip := range []bool{false, true} {
		var s *service
		var whileRunning struct {
			cwd       string
			sandboxed bool
		}
		tool := approvingTool{
			testTool: testTool{name: "sbx", run: func(context.Context, json.RawMessage) (string, bool) {
				whileRunning.cwd, whileRunning.sandboxed = cwdAndSandboxed(t, s)
				return "ran", false
			}},
			approval: tools.Approval{Cwd: "/work", Sandboxed: true, Skip: skip},
		}
		s = startServiceWithTool(t, tool)
		fakeOf(s).SetToolCalls(llm.StagedCall("sbx", `{}`))

		msg := send(t, s, nil, "1", "hi")
		if !skip {
			_, id := awaitRequest(t, s, msg)
			cwd, sandboxed := cwdAndSandboxed(t, s)
			assert.Equal(t, "/work", cwd)
			assert.True(t, sandboxed, "the request's row")
			approve(t, s, id, true)
		}
		awaitSettled(t, s, msg.ChatID, msg.ID)
		awaitTurnDone(t, s, msg.ChatID)

		assert.Equal(t, "/work", whileRunning.cwd, "skip=%v", skip)
		assert.True(t, whileRunning.sandboxed, "the running row, skip=%v", skip)
		cwd, sandboxed := cwdAndSandboxed(t, s)
		assert.Equal(t, "/work", cwd, "skip=%v", skip)
		assert.True(t, sandboxed, "the settle's rewrite keeps it, skip=%v", skip)
	}
}

// A command that never ran reads back off its row once the turn has settled and
// the overlay is gone: its request, and that it did not run.
func TestAStoredReadKeepsAnUnrunCommand(t *testing.T) {
	s := startServiceWithTool(t, &fakeBash{})
	fakeOf(s).SetToolCalls(bashCall("kubectl delete pod web-0"))

	msg := send(t, s, nil, "1", "hi")
	awaitRequest(t, s, msg)
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitTurnDone(t, s, msg.ChatID)

	msgs, err := s.readMessages(t.Context(), msg.ChatID)
	require.NoError(t, err)
	calls := toolCallsOf(t, msgs[1])
	require.Len(t, calls, 1)
	assert.Equal(t, "kubectl delete pod web-0", calls[0].Action.Command.Text)
	assert.Equal(t, ToolCallNotRun, calls[0].Status)
	assert.Equal(t, ApprovalPending, calls[0].Approval.Status, "a question nobody answered")
}

// A wait the process died in is failed at the next start: the row closed stranded
// with no started_at, the approval left pending, and a decision on it finds no one.
func TestStartupFailsAStrandedWait(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	c := seedChat(t, db, aChat("1", time.UnixMilli(1_000).UTC()))
	turn, id := seedWait(t, db, c.ID, "ls", `[]`)
	require.NoError(t, db.Close())

	s := startService(t, dir)

	assert.Equal(t, runFailed, runStatusOf(t, s.db, turn.Run))
	rows := toolCallRows(t, s.db, turn.Run)
	require.Len(t, rows, 1)
	assert.Equal(t, toolCallStranded, rows[0].errText)
	assert.False(t, rows[0].hasStarted)
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))
	ok, err := s.Approve(t.Context(), id, true)
	require.NoError(t, err)
	assert.False(t, ok)
	msgs, err := s.readMessages(t.Context(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, ToolCallNotRun, toolCallsOf(t, msgs[1])[0].Status)
}

// The transcript reads a command off its row, never off the content, so a command
// whose tool_use never reached the stored content is still shown.
func TestACommandSurvivesACrashBeforeItsCheckpoint(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	c := seedChat(t, db, aChat("1", time.UnixMilli(1_000).UTC()))
	seedWait(t, db, c.ID, "rm -rf /tmp/x", `[]`)
	require.NoError(t, db.Close())

	s := startService(t, dir)

	msgs, err := s.readMessages(t.Context(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, emptyContent, msgs[1].Content)
	calls := toolCallsOf(t, msgs[1])
	require.Len(t, calls, 1)
	assert.Equal(t, "rm -rf /tmp/x", calls[0].Action.Command.Text)
	assert.JSONEq(t, `{"command":"rm -rf /tmp/x"}`, string(calls[0].Arguments))
}

// A command whose own output spells a refusal ran and succeeded: the row is
// judged by whether the result is an error, never by what its text happens to say.
func TestACommandPrintingARefusalStillSucceeded(t *testing.T) {
	sh := &fakeBash{run: func(context.Context, string) (string, bool) { return "{\"error\":\"denied\"}\n", false }}
	s := startServiceWithTool(t, sh)
	fakeOf(s).SetToolCalls(bashCall(`echo '{"error":"denied"}'`))

	msg := send(t, s, nil, "1", "hi")
	_, id := awaitRequest(t, s, msg)
	approve(t, s, id, true)
	got := awaitSettled(t, s, msg.ChatID, msg.ID)

	calls := toolCallsOf(t, got)
	assert.Equal(t, ToolCallSucceeded, calls[0].Status)
	assert.False(t, calls[0].IsError)
	rows := toolCallRows(t, s.db, msg.RunID)
	assert.Equal(t, toolSucceeded, rows[0].status)
	assert.True(t, rows[0].hasStarted)
}

// A Bash call that never reached the gate still shows what it asked for, with
// no directory: one cancelled behind another's wait, and one on a box with no
// shell, which the loop refuses unknown-tool.
func TestAnUngatedBashCallShowsItsCommand(t *testing.T) {
	s := startServiceWithTool(t, &fakeBash{})
	fakeOf(s).SetToolCalls(bashCall("ls"), describedBashCall("rm -rf /tmp/x", "Clear the scratch"))
	msg := send(t, s, nil, "1", "hi")
	awaitRequest(t, s, msg)
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitTurnDone(t, s, msg.ChatID)
	msgs, err := s.readMessages(t.Context(), msg.ChatID)
	require.NoError(t, err)
	queued := toolCallsOf(t, msgs[1])[1]
	assert.Nil(t, queued.Approval)
	assert.Equal(t, &tools.Action{
		Description: "Clear the scratch",
		Command:     &tools.CommandAction{Text: "rm -rf /tmp/x"},
	}, queued.Action)

	s = startServiceWithTool(t, testTool{name: "echo"})
	fakeOf(s).SetToolCalls(describedBashCall("kubectl get pods", "List pods"))
	msg = send(t, s, nil, "1", "hi")
	got := awaitSettled(t, s, msg.ChatID, msg.ID)
	unknown := toolCallsOf(t, got)[0]
	assert.Equal(t, `{"error":"unknown-tool"}`, unknown.Output)
	assert.Equal(t, &tools.Action{
		Description: "List pods",
		Command:     &tools.CommandAction{Text: "kubectl get pods"},
	}, unknown.Action)
}

// A message awaits approval while its own run waits on the user, on the live
// answer, and not once the decision is in.
func TestAWaitingRequestMarksItsMessage(t *testing.T) {
	s := startServiceWithTool(t, &fakeBash{})
	fakeOf(s).SetToolCalls(bashCall("kubectl get pods"))

	msg := send(t, s, nil, "1", "hi")
	waiting, id := awaitRequest(t, s, msg)
	assert.True(t, waiting.AwaitingApproval)

	approve(t, s, id, true)
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)
	assert.False(t, settled.AwaitingApproval)
}

// A settled answer awaits approval while a run under it waits on the user: the
// stored read looks at the subagents' runs too.
func TestASubagentWaitingMarksItsSettledAnswer(t *testing.T) {
	now := time.UnixMilli(1_000).UTC()
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", now))
	turn := seedTurn(t, s.db, c.ID, now)
	settleSeededRun(t, s.db, turn.Run, runSucceeded, now)
	sub := appdb.NewID()
	_, err := s.db.Write.Exec(`INSERT INTO agent_runs (id, parent_run_id, agent_type, app_version, trigger, conversation_id, provider, model, dialect, task, status, created_at)
		VALUES (?, ?, 'general-purpose', 'test', 'agent', ?, 'fake', 'fake', 'fake', 'p', 'waiting_approval', 0)`, sub, string(turn.Run), string(c.ID))
	require.NoError(t, err)

	msgs, err := s.readMessages(t.Context(), c.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.False(t, msgs[0].AwaitingApproval, "a question has no run")
	assert.True(t, msgs[1].AwaitingApproval)
	assert.Equal(t, StatusComplete, msgs[1].Status, "the answer's status stays its own run's")

	setRunStatus(t, s.db, RunID(sub), runRunning)
	msgs, err = s.readMessages(t.Context(), c.ID)
	require.NoError(t, err)
	assert.False(t, msgs[1].AwaitingApproval)
}

// An agent's request left unanswered past the bound stops the agent,
// stopped_by unanswered: its slot is freed, the request can no longer be
// decided, and its notice rides the next question.
func TestAnUnansweredRequestStopsItsAgent(t *testing.T) {
	sh := &fakeBash{}
	s := startServiceWithAgent(t, sh)
	s.unansweredLimit = time.Millisecond
	fakeOf(s).SetToolCalls(agentCall("List the pods."))
	subagentFake(s, "List the pods.").SetToolCalls(bashCall("kubectl get pods"))

	msg := send(t, s, nil, "k", "which pods?")
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitAgentOf(t, s, msg.ChatID, msg.RunID)

	row := taskRows(t, s.db)[0]
	assert.Equal(t, taskStopped, row.status)
	assert.Equal(t, sql.NullString{String: stoppedByUnanswered, Valid: true}, row.stoppedBy)
	assert.False(t, row.notified)
	assert.Empty(t, sh.commands())
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db), "the record of a question nobody answered")
	var id ApprovalID
	require.NoError(t, s.db.Read.QueryRow(`SELECT id FROM approvals`).Scan(&id))
	ok, err := s.Approve(t.Context(), id, true)
	require.NoError(t, err)
	assert.False(t, ok, "nothing waits on it")
}

// The parent's own request is not bounded: it holds the turn, which the user
// sees and can cancel.
func TestTheParentsOwnRequestWaits(t *testing.T) {
	s := startServiceWithTool(t, &fakeBash{})
	s.unansweredLimit = time.Millisecond
	fakeOf(s).SetToolCalls(bashCall("kubectl get pods"))

	msg := send(t, s, nil, "k", "which pods?")
	awaitRequest(t, s, msg)
	turn := s.turnOf(msg.ChatID)
	require.NotNil(t, turn)

	// A negative assertion has no event to wait on: fifty times the bound, which a
	// bounded wait would have long passed.
	select {
	case <-turn.done:
		t.Fatal("the parent's request was given up")
	case <-time.After(50 * time.Millisecond):
	}
}

// A chat with an agent waiting on the user is marked on the list, which a watch
// hears, and the mark clears when the agent is stopped.
func TestAWaitingAgentMarksItsChat(t *testing.T) {
	s := startServiceWithAgent(t, &fakeBash{})
	fakeOf(s).SetToolCalls(agentCall("List the pods."))
	subagentFake(s, "List the pods.").SetToolCalls(bashCall("kubectl get pods"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	list, err := s.WatchList(ctx)
	require.NoError(t, err)

	msg := send(t, s, nil, "k", "which pods?")
	awaitFrame(t, list.Frames, func(f ChatWatchFrame) bool {
		return f.Chat != nil && f.Chat.ID == msg.ChatID && f.Chat.AwaitingApproval
	})
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitCall(t, s, msg.ChatID, msg.ID, "Bash", ToolCallAwaitingApproval)

	_, err = s.StopBackgroundTask(t.Context(), ToolCallID(taskRows(t, s.db)[0].toolCallID))
	require.NoError(t, err)
	awaitFrame(t, list.Frames, func(f ChatWatchFrame) bool {
		return f.Chat != nil && f.Chat.ID == msg.ChatID && !f.Chat.AwaitingApproval
	})
}

// A chat a restart left waiting is not marked: the start fails the run, and a
// watch opened before it hears the mark clear.
func TestAStrandedWaitClearsTheChatsMark(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	c := seedChat(t, db, aChat("1", time.UnixMilli(1_000).UTC()))
	turn := seedTurn(t, db, c.ID, time.UnixMilli(1_000).UTC())
	setRunStatus(t, db, turn.Run, runWaitingApproval)
	require.NoError(t, db.Close())

	s, err := newService(openTestDB(t, dir), chatsDirIn(dir), fakeLLM(), noClusterCards, nil, testReaders, noLists, sandbox.Status{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	list, err := s.WatchList(ctx)
	require.NoError(t, err)
	awaitFrame(t, list.Frames, func(f ChatWatchFrame) bool { return f.Chat != nil && f.Chat.AwaitingApproval })
	startPrepared(t, s)

	awaitFrame(t, list.Frames, func(f ChatWatchFrame) bool {
		return f.Chat != nil && f.Chat.ID == c.ID && !f.Chat.AwaitingApproval
	})
}

// deleteX is the write the chat tests' writer sends.
var deleteX = tools.ClusterWriteRequest{
	Method: "DELETE", Path: "/api/v1/namespaces/web/pods/x?dryRun=All", ContentType: "application/json",
	Body: `{"propagationPolicy":"Background"}`,
}

// writerTool is a tool whose run puts one cluster write to the user through its
// runtime, as Bash's grant does, and answers with the decision. hold, when set,
// keeps it running after the answer until the test closes it; ctx, when set,
// is the write's context in place of the call's.
type writerTool struct {
	testTool
	hold chan struct{}
	ctx  context.Context
}

func (w writerTool) Run(ctx context.Context, rt tools.Runtime, _ json.RawMessage) (string, bool) {
	if rt.ClusterWriteAsker == nil {
		return "nobody to ask", true
	}
	if w.ctx != nil {
		ctx = w.ctx
	}
	ok, err := rt.ClusterWriteAsker.Ask(ctx, deleteX)
	if w.hold != nil {
		<-w.hold
	}
	if err != nil {
		return "unanswered", true
	}
	return fmt.Sprint(ok), false
}

// startWriter is a started service offering w as Writer, and the model calling
// it once.
func startWriter(t *testing.T, w writerTool) *service {
	t.Helper()
	w.name = "Writer"
	s := startServiceWithTool(t, w)
	fakeOf(s).SetToolCalls(llm.StagedCall("Writer", `{}`))
	return s
}

// awaitWrite watches the message until its first call's last write has status,
// and returns the message as the watch served it.
func awaitWrite(t *testing.T, s *service, msg ChatMessage, status ApprovalStatus) ChatMessage {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := s.WatchMessages(ctx, msg.ChatID)
	require.NoError(t, err)
	f := awaitFrame(t, st.Frames, func(f ChatMessageWatchFrame) bool {
		if f.Message == nil || f.Message.ID != msg.ID {
			return false
		}
		var calls []ToolCall
		if json.Unmarshal([]byte(f.Message.ToolCalls), &calls) != nil || len(calls) == 0 {
			return false
		}
		writes := calls[0].ClusterWrites
		return len(writes) > 0 && writes[len(writes)-1].Approval.Status == status
	})
	return *f.Message
}

// A write is asked under the call that is running: a cluster approval on
// it, the run waiting and then running again once decided, and the call running
// throughout. While it waits the list carries the write whole; once decided,
// its method and path alone.
func TestAWriteIsAskedUnderTheRunningCall(t *testing.T) {
	hold := make(chan struct{})
	s := startWriter(t, writerTool{hold: hold})

	msg := send(t, s, nil, "1", "hi")
	got := awaitWrite(t, s, msg, ApprovalPending)

	assert.Equal(t, StatusWaitingApproval, got.Status)
	calls := toolCallsOf(t, got)
	require.Len(t, calls, 1)
	assert.Equal(t, ToolCallRunning, calls[0].Status)
	assert.Nil(t, calls[0].Approval, "the call itself was not asked about")
	require.Len(t, calls[0].ClusterWrites, 1)
	w := calls[0].ClusterWrites[0]
	assert.Equal(t, ClusterWrite{Approval: ToolCallApproval{ID: w.Approval.ID, Status: ApprovalPending}, ClusterWriteRequest: deleteX}, w)
	assert.Equal(t, runWaitingApproval, runStatusOf(t, s.db, msg.RunID))
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))

	approve(t, s, w.Approval.ID, true)
	got = awaitWrite(t, s, msg, ApprovalApproved)
	assert.Equal(t, runRunning, runStatusOf(t, s.db, msg.RunID))
	assert.Equal(t, StatusStreaming, got.Status)
	w = toolCallsOf(t, got)[0].ClusterWrites[0]
	decided := deleteX
	decided.ContentType, decided.Body = "", ""
	assert.Equal(t, ClusterWrite{Approval: ToolCallApproval{ID: w.Approval.ID, Status: ApprovalApproved}, ClusterWriteRequest: decided}, w)
	assert.Equal(t, []approvalRow{{status: ApprovalApproved, decided: true}}, approvalRows(t, s.db))

	close(hold)
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)
	calls = toolCallsOf(t, settled)
	assert.Equal(t, "true", calls[0].Output)
	assert.Equal(t, ToolCallSucceeded, calls[0].Status)
	assert.Equal(t, ApprovalApproved, calls[0].ClusterWrites[0].Approval.Status)
}

// A denied write answers false to the command, which runs on.
func TestADeniedWriteAnswersFalse(t *testing.T) {
	s := startWriter(t, writerTool{})

	msg := send(t, s, nil, "1", "hi")
	got := awaitWrite(t, s, msg, ApprovalPending)
	approve(t, s, toolCallsOf(t, got)[0].ClusterWrites[0].Approval.ID, false)
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)

	calls := toolCallsOf(t, settled)
	assert.Equal(t, "false", calls[0].Output)
	assert.Equal(t, ApprovalDenied, calls[0].ClusterWrites[0].Approval.Status)
}

// A write's wait that ends with its request, while the run goes on, writes it
// abandoned, flips the run back to running and publishes; a later decision on
// it reaches no one.
func TestAWriteWaitEndsWithTheRequest(t *testing.T) {
	ctx, drop := context.WithCancel(t.Context())
	hold := make(chan struct{})
	s := startWriter(t, writerTool{hold: hold, ctx: ctx})

	msg := send(t, s, nil, "1", "hi")
	id := toolCallsOf(t, awaitWrite(t, s, msg, ApprovalPending))[0].ClusterWrites[0].Approval.ID
	drop()
	got := awaitWrite(t, s, msg, ApprovalAbandoned)

	assert.Equal(t, StatusStreaming, got.Status)
	assert.Equal(t, runRunning, runStatusOf(t, s.db, msg.RunID))
	assert.Equal(t, []approvalRow{{status: ApprovalAbandoned, decided: true}}, approvalRows(t, s.db))
	ok, err := s.Approve(t.Context(), id, true)
	require.NoError(t, err)
	assert.False(t, ok, "a decision after the wait ended reaches no one")
	close(hold)
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)
	assert.Equal(t, "unanswered", toolCallsOf(t, settled)[0].Output)
}

// Cancelling the run ends a write's wait: the write is abandoned, and the run
// settles cancelled.
func TestAWriteWaitEndsWithTheRun(t *testing.T) {
	s := startWriter(t, writerTool{})

	msg := send(t, s, nil, "1", "hi")
	awaitWrite(t, s, msg, ApprovalPending)
	require.NoError(t, s.Cancel(t.Context(), msg.ChatID))
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, StatusCancelled, settled.Status)
	assert.Equal(t, ApprovalAbandoned, toolCallsOf(t, settled)[0].ClusterWrites[0].Approval.Status)
	assert.Equal(t, []approvalRow{{status: ApprovalAbandoned, decided: true}}, approvalRows(t, s.db))
}

// The settle writes each write's approval again from what the turn holds, so a
// decision whose row was lost is healed.
func TestTheSettleRewritesTheWrites(t *testing.T) {
	hold := make(chan struct{})
	s := startWriter(t, writerTool{hold: hold})

	msg := send(t, s, nil, "1", "hi")
	id := toolCallsOf(t, awaitWrite(t, s, msg, ApprovalPending))[0].ClusterWrites[0].Approval.ID
	approve(t, s, id, false)
	awaitWrite(t, s, msg, ApprovalDenied)
	_, err := s.db.Write.Exec(`UPDATE approvals SET status = 'pending', decided_at = NULL WHERE id = ?`, string(id))
	require.NoError(t, err)
	close(hold)
	awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, []approvalRow{{status: ApprovalDenied, decided: true}}, approvalRows(t, s.db))
}

// A subagent asks its writes as its own, under its own run: past the bound its
// write is abandoned and the agent stopped, stopped_by unanswered.
func TestASubagentsWriteIsBoundedByItsLimit(t *testing.T) {
	s := startServiceWithAgent(t, writerTool{testTool: testTool{name: "Writer"}})
	s.unansweredLimit = time.Millisecond
	fakeOf(s).SetToolCalls(agentCall("Delete the pod."))
	subagentFake(s, "Delete the pod.").SetToolCalls(llm.StagedCall("Writer", `{}`))

	msg := send(t, s, nil, "k", "delete pod x")
	awaitSettled(t, s, msg.ChatID, msg.ID)
	awaitAgentOf(t, s, msg.ChatID, msg.RunID)

	row := taskRows(t, s.db)[0]
	assert.Equal(t, taskStopped, row.status)
	assert.Equal(t, sql.NullString{String: stoppedByUnanswered, Valid: true}, row.stoppedBy)
	assert.Equal(t, []approvalRow{{status: ApprovalAbandoned, decided: true}}, approvalRows(t, s.db))
}

// A write whose request cannot be written is never shown: the command reads
// the wait ended, and nothing waits on its id.
func TestAWriteThatCannotBeRecordedIsNotAsked(t *testing.T) {
	s := startWriter(t, writerTool{})
	_, err := s.db.Write.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON approvals BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)

	msg := send(t, s, nil, "1", "hi")
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, "unanswered", toolCallsOf(t, settled)[0].Output)
	assert.Empty(t, approvalRows(t, s.db))
	s.turnsMu.Lock()
	defer s.turnsMu.Unlock()
	assert.Empty(t, s.pending, "no waiter is left behind")
}

// A decision whose write fails answers the command with an error, so no change
// goes on a decision the record does not hold. The request still comes down,
// since the command runs on, and the settle writes the decision.
func TestADecisionThatCannotBeRecordedForwardsNothing(t *testing.T) {
	hold := make(chan struct{})
	s := startWriter(t, writerTool{hold: hold})
	_, err := s.db.Write.Exec(`CREATE TRIGGER refuse BEFORE UPDATE ON approvals WHEN NEW.status != 'pending'
		BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)

	msg := send(t, s, nil, "1", "hi")
	id := toolCallsOf(t, awaitWrite(t, s, msg, ApprovalPending))[0].ClusterWrites[0].Approval.ID
	approve(t, s, id, true)
	got := awaitWrite(t, s, msg, ApprovalApproved)
	assert.Equal(t, StatusStreaming, got.Status)
	assert.Equal(t, []approvalRow{{status: ApprovalPending}}, approvalRows(t, s.db))

	_, err = s.db.Write.Exec(`DROP TRIGGER refuse`)
	require.NoError(t, err)
	close(hold)
	settled := awaitSettled(t, s, msg.ChatID, msg.ID)

	assert.Equal(t, "unanswered", toolCallsOf(t, settled)[0].Output)
	assert.Equal(t, []approvalRow{{status: ApprovalApproved, decided: true}}, approvalRows(t, s.db))
	assert.Equal(t, runSucceeded, runStatusOf(t, s.db, msg.RunID))
}

// A write asked while no call of the run is running is refused.
func TestAWriteWithNoRunningCallIsRefused(t *testing.T) {
	j := &runJournal{s: newTestService(t)}

	ok, err := j.askClusterWrite(t.Context(), deleteX)

	assert.False(t, ok)
	assert.ErrorIs(t, err, errNoRunningCall)
}
