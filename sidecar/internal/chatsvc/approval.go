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

// The gate: a gated call's rows, the wait on the user's decision, and the
// delivery of that decision from approvalDecide to the turn that waits on it.
package chatsvc

import (
	"context"
	"errors"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// Approve is agent.Approver over the run's rows. The waiter is registered before
// anything is written or shown, so a decision cannot arrive at a request nobody
// waits on and no id is decidable ahead of its request. The call's row, its
// approval and the run's flip to waiting_approval land in one transaction; then
// the request is published, and the wait ends on the decision or the run's
// cancel. Only this run flips: a subagent's wait leaves its parent's alone. A
// cancel that wins leaves the approval pending, the record of a question nobody
// answered. Once the decision has committed it stands, and the loop decides what
// a cancel after it means.
//
// A run with an unansweredLimit, a subagent's, is bounded too: past it the agent
// is stopped, as the user's Stop would, and the wait ends on that stop's cancel.
func (j *runJournal) Approve(ctx context.Context, call llm.Block, shown tools.Approval) (bool, error) {
	s := j.s
	id := newApprovalID()
	decision := s.await(id)
	row := j.newToolCall(call)
	row.Status, row.IsMutating, row.Cwd, row.Sandboxed = toolAwaitingApproval, true, shown.Cwd, shown.Sandboxed
	a := &approval{ID: id, ToolCallID: row.ID, Status: ApprovalPending, CreatedAt: row.CreatedAt}
	if err := j.writeWaiting(ctx, row, *a); err != nil {
		s.forget(id)
		return false, err
	}
	row.Approval = a
	j.toolCalls, j.openTool = append(j.toolCalls, row), row
	j.publish(StatusWaitingApproval)
	s.notify(conversationsKey)

	status, err := j.waitDecision(ctx, id, decision)
	if err != nil {
		return false, err
	}
	if err := j.endApproval(ctx, a, status); err != nil {
		return false, err
	}
	return status == ApprovalApproved, nil
}

// writeWaiting writes a pending, with row when it is a call's own first row,
// and flips the run to waiting_approval, in one transaction on ctx without its
// cancel.
func (j *runJournal) writeWaiting(ctx context.Context, row *toolCallEntry, a approval) error {
	wctx := context.WithoutCancel(ctx)
	return j.s.store.InTx(wctx, func(st stmts) error {
		if row != nil {
			if err := upsertToolCall(wctx, st, *row); err != nil {
				return err
			}
		}
		if err := upsertApproval(wctx, st, a); err != nil {
			return err
		}
		return flipRun(wctx, st, j.runID, runWaitingApproval)
	})
}

// waitDecision waits for the user's answer to id: approved or denied, or
// pending with the error that ended the wait first. Past the run's
// unansweredLimit the agent is stopped, as the user's Stop would, and the wait
// ends on that stop's cancel.
func (j *runJournal) waitDecision(ctx context.Context, id ApprovalID, decision <-chan bool) (ApprovalStatus, error) {
	s := j.s
	var unanswered <-chan time.Time
	if j.unansweredLimit > 0 {
		timer := time.NewTimer(j.unansweredLimit)
		defer timer.Stop()
		unanswered = timer.C
	}
	select {
	case approved := <-decision:
		if approved {
			return ApprovalApproved, nil
		}
		return ApprovalDenied, nil
	case <-ctx.Done():
		s.forget(id)
		return ApprovalPending, ctx.Err()
	case <-unanswered:
		s.forget(id)
		if s.stopTaskOf(j.agentCallID, stoppedByUnanswered) {
			<-ctx.Done()
		}
		return ApprovalPending, context.Canceled
	}
}

// endApproval writes a's end as status and flips the run back to running, in
// one transaction on ctx without its cancel, then publishes. a takes the end
// only once it has landed.
func (j *runJournal) endApproval(ctx context.Context, a *approval, status ApprovalStatus) error {
	ended := *a
	ended.Status, ended.DecidedAt = status, nullMillis(normalizeTime(j.s.now()))
	wctx := context.WithoutCancel(ctx)
	err := j.s.store.InTx(wctx, func(st stmts) error {
		if err := upsertApproval(wctx, st, ended); err != nil {
			return err
		}
		return flipRun(wctx, st, j.runID, runRunning)
	})
	if err != nil {
		return err
	}
	*a = ended
	j.publish(StatusStreaming)
	j.s.notify(conversationsKey)
	return nil
}

// clusterWriteAsker is a run's journal as the tools.ClusterWriteAsker its calls
// get, bound to the run's context, so a wait ends with the run as well as with
// the request.
type clusterWriteAsker struct {
	j   *runJournal
	run context.Context
}

func (w clusterWriteAsker) Ask(ctx context.Context, cw tools.ClusterWriteRequest) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(w.run, cancel)()
	return w.j.askClusterWrite(ctx, cw)
}

func (w clusterWriteAsker) Record(ctx context.Context, cw tools.ClusterWriteRequest, d permissions.Decision, why permissions.Reason) error {
	return w.j.recordClusterWrite(ctx, cw, d, why)
}

// recordClusterWrite records a sandboxed command's cluster write the
// permissions engine decided with nobody asked, against the call the run has
// open, as askClusterWrite records one the user decided: written already
// decided, so nothing waits and the run stays running.
func (j *runJournal) recordClusterWrite(ctx context.Context, w tools.ClusterWriteRequest, d permissions.Decision, why permissions.Reason) error {
	s := j.s
	status, ok := recordedStatus[d]
	if !ok {
		return errNotRecorded
	}
	call := j.openTool
	if call == nil {
		return errNoRunningCall
	}
	now := normalizeTime(s.now())
	a := &approval{
		ID: newApprovalID(), ToolCallID: call.ID, Status: status, CreatedAt: now,
		DecidedAt: nullMillis(now), Request: &w, Reason: why.String(),
	}
	wctx := context.WithoutCancel(ctx)
	if err := s.store.InTx(wctx, func(st stmts) error { return upsertApproval(wctx, st, *a) }); err != nil {
		return err
	}
	call.ClusterWrites = append(call.ClusterWrites, a)
	j.publish(StatusStreaming)
	return nil
}

// askClusterWrite puts a sandboxed command's cluster write to the user as a
// request of the call the run has open: calls run one at a time, so it is the
// one whose command sent the write. Unlike Approve, every end of the wait writes
// the approval and flips the run back, since the command runs on after it and
// the run must not stay waiting behind it: a wait that ends without a decision
// writes abandoned. The call's row keeps running throughout. An end the store
// refuses answers the command with that error, never the decision: no change
// goes on a decision the record does not hold.
//
// It runs on the proxy's goroutine while the loop waits in the call's Run, which
// returns only once every write it sent has returned (kubeproxy's Wait), so the
// journal is never touched by both at once.
func (j *runJournal) askClusterWrite(ctx context.Context, w tools.ClusterWriteRequest) (bool, error) {
	s := j.s
	call := j.openTool
	if call == nil {
		return false, errNoRunningCall
	}
	id := newApprovalID()
	decision := s.await(id)
	a := &approval{ID: id, ToolCallID: call.ID, Status: ApprovalPending, CreatedAt: normalizeTime(s.now()), Request: &w}
	if err := j.writeWaiting(ctx, nil, *a); err != nil {
		s.forget(id)
		return false, err
	}
	call.ClusterWrites = append(call.ClusterWrites, a)
	j.publish(StatusWaitingApproval)
	s.notify(conversationsKey)

	status, waitErr := j.waitDecision(ctx, id, decision)
	if waitErr != nil {
		status = ApprovalAbandoned
	}
	if err := j.endApproval(ctx, a, status); err != nil {
		// The command runs on, so the request must come down now: the journal
		// takes the end the store refused, and the settle writes it whole.
		a.Status, a.DecidedAt = status, nullMillis(normalizeTime(s.now()))
		j.publish(StatusStreaming)
		s.notify(conversationsKey)
		return false, err
	}
	return status == ApprovalApproved, waitErr
}

// errNoRunningCall is a write asked while no call of the run is running.
var errNoRunningCall = errors.New("chatsvc: a cluster write with no call running")

// recordedStatus is the status a decision is recorded as. A prompt is asked,
// never recorded.
var recordedStatus = map[permissions.Decision]ApprovalStatus{
	permissions.Allowed: ApprovalAllowed,
	permissions.Denied:  ApprovalRefused,
}

// errNotRecorded is a record of a decision that is not Allowed or Denied.
var errNotRecorded = errors.New("chatsvc: only an allowed or denied cluster write is recorded")

// await registers a waiter for id: a channel of one, buffered, so a decision
// delivered before the turn reaches its select is kept for it.
func (s *service) await(id ApprovalID) chan bool {
	ch := make(chan bool, 1)
	s.turnsMu.Lock()
	s.pending[id] = ch
	s.turnsMu.Unlock()
	return ch
}

// forget takes a waiter back: its request was never shown, or its turn stopped waiting.
func (s *service) forget(id ApprovalID) {
	s.turnsMu.Lock()
	delete(s.pending, id)
	s.turnsMu.Unlock()
}

// Approve delivers a decision to the turn waiting on id and removes the waiter, so
// each approval has one writer and a second decision changes nothing. false is an
// id nothing waits on: already decided, cancelled, or stranded across a restart.
// true says the decision reached a waiting turn, not that it was recorded: a
// cancel ready in the same moment can still win the turn's select.
func (s *service) Approve(_ context.Context, id ApprovalID, approve bool) (bool, error) {
	s.turnsMu.Lock()
	ch, ok := s.pending[id]
	delete(s.pending, id)
	s.turnsMu.Unlock()
	if !ok {
		return false, nil
	}
	ch <- approve // buffered, and the entry is gone: this send is the only one
	return true, nil
}
