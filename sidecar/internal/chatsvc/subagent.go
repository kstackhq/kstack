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

// The subagent: an agent an Agent call starts as a background task of the chat.
// It runs on a goroutine of its own, as a run under the parent's, and owns its
// rows.
package chatsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/agent"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/version"
)

var _ tools.Spawner = (*turn)(nil)

// Start is tools.Spawner: a subagent for the Agent call now running in t,
// started as a task of the chat. Its run, its task and the call's link to the
// run land in one transaction before it starts, and the call answers the task's
// id.
func (t *turn) Start(ctx context.Context, d tools.Delegation) (string, error) {
	target, err := t.subagentTarget(d.Model)
	if err != nil {
		return "", err
	}
	if t.openTool == nil {
		return "", errNoOpenCall
	}
	s := t.s
	card, err := newestContext(ctx, s.store.Stmts(), t.chatID)
	if err != nil {
		return "", err
	}
	run := agentRun{
		ID: newRunID(), ParentID: t.runID, AgentType: d.Type, ConversationID: t.chatID,
		ProviderID: target.Provider.ID, ModelID: target.Model.ID, Effort: target.Effort,
		Dialect: target.Provider.Dialect, Task: d.Prompt, AppVersion: version.Version, CreatedAt: normalizeTime(s.now()),
	}
	linked := *t.openTool
	linked.SpawnedRunID = run.ID
	chatID := t.chatID
	c := &subagent{runJournal: &runJournal{
		s: s, chatID: chatID, runID: run.ID, target: target, agentCallID: linked.ID,
		unansweredLimit: s.unansweredLimit,
		// Every row it writes has landed before it publishes, so the watchers'
		// re-read finds it.
		publish: func(MessageStatus) { s.notify(messagesKey(chatID)) },
	}}
	spec := agent.Turn{
		Target: target, SystemPrompt: subagentSystemPrompt(),
		Messages: []llm.Message{{Role: string(RoleUser), Blocks: subagentMessage(card, d.Prompt)}},
		// Its prefix is its brief, not the chat's.
		AffinityKey: string(run.ID),
		// No Agent, so depth is one, and no Memory: a note outlives the chat, and the
		// chat's own turns are what write one.
		Tools: s.boxFor(target).Without(tools.ActionDelegate, tools.ActionMemory),
		Runtime: tools.Runtime{
			ClusterID: t.clusterID, ChatID: chatID, OutsideSandbox: t.outsideSandbox,
			Dir: s.chatDir(chatID), Tasks: s.chatTasks(chatID, c.runJournal), Files: runStamps{},
		},
		MaxToolCalls: maxSubagentToolCalls, DefaultToolTimeout: defaultToolTimeout,
	}
	rec := taskRecord{
		write: func(ctx context.Context, st stmts) error {
			if err := insertSubagentRun(ctx, st, run); err != nil {
				return err
			}
			return upsertToolCall(ctx, st, linked)
		},
		// The link goes with the run: spawned_run_id is ON DELETE SET NULL.
		takeBack: func(ctx context.Context, st stmts) error { return deleteRun(ctx, st, run.ID) },
		end:      c.end,
	}
	id, _, err := s.startTask(s.chatDir(chatID), t.runID, linked.ID, rec, func(f *os.File) (tools.Task, error) {
		// The last moment before the subagent starts: a Cancel during the row
		// write starts nothing, rather than an agent under a call that answered
		// cancelled, whose id the model never saw.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return startAgentTask(c, spec, f), nil
	})
	if err != nil {
		return "", err
	}
	// Only now: the parent writes this row again when the call finishes, and a
	// link to a run a failed start took back would fail that write.
	*t.openTool = linked
	return id, nil
}

// subagentMessage is the one message a subagent starts from: the chat's newest card,
// what the model currently holds of the cluster, then the prompt; the prompt
// alone in a chat with no card.
func subagentMessage(card, prompt string) []llm.Block {
	if card == "" {
		return []llm.Block{llm.TextBlock(prompt)}
	}
	return []llm.Block{llm.ContextBlock(card), llm.TextBlock(prompt)}
}

// subagentTarget is what a subagent named onto model runs on: the parent's target,
// effort included, for no model or the parent's own; else that model of the
// parent's provider at its default effort, when it takes tools.
func (t *turn) subagentTarget(model string) (llm.Target, error) {
	if model == "" || model == t.target.Model.ID {
		return t.target, nil
	}
	for _, m := range t.target.Provider.Catalog {
		if m.ID == model && m.Tools {
			return llm.Target{Provider: t.target.Provider, Model: m, Effort: m.DefaultEffort}, nil
		}
	}
	return llm.Target{}, tools.ErrUnknownModel
}

// agentTask is a subagent as a tools.Task. Its context is its own, so only a
// stop ends it: a parent's Cancel stops the parent's answer, never the agents it
// started. Wait blocks until the loop has ended and the report is in the task's
// file; the task's watcher holds the service's wg while it does.
type agentTask struct {
	sub    *subagent
	cancel context.CancelFunc
	done   chan struct{}
}

// startAgentTask starts the subagent's loop on a goroutine of its own.
func startAgentTask(c *subagent, spec agent.Turn, f *os.File) *agentTask {
	ctx, cancel := context.WithCancel(context.Background())
	a := &agentTask{sub: c, cancel: cancel, done: make(chan struct{})}
	// Its writes ask as its own, under its run, bounded by its unansweredLimit.
	spec.Runtime.ClusterWriteAsker = clusterWriteAsker{j: c.runJournal, run: ctx}
	go a.run(ctx, spec, f)
	return a
}

// Wait answers a zero Exit: an agent's end is read off its run, not a process.
func (a *agentTask) Wait() tools.Exit {
	<-a.done
	return tools.Exit{}
}

// Stop cancels the subagent at once, whatever now says: it has no grace to give.
func (a *agentTask) Stop(bool) { a.cancel() }

// run is the subagent's loop, then its report into f. The stream is third-party
// code, so a panic in it fails the subagent rather than the process.
func (a *agentTask) run(ctx context.Context, spec agent.Turn, f *os.File) {
	defer close(a.done)
	defer a.cancel()
	c := a.sub
	func() {
		defer func() {
			if p := recover(); p != nil {
				c.streamErr = fmt.Errorf("panic: %v", p)
				c.status, c.report, c.errText = runFailed, "", c.streamErr.Error()
			}
		}()
		// agent.Run's error also carries the writes that are not fatal; Settled
		// reads the loop's own outcome.
		_, _ = agent.Run(ctx, spec, c, c)
	}()
	report := c.report[:tools.RuneBoundary(c.report, min(len(c.report), tools.FileLimit))]
	_, err := f.WriteString(report)
	if err = errors.Join(err, f.Close()); err != nil {
		slog.Warn("could not write an agent's report to its file", "err", err)
	}
}

// subagent is the agent.Recorder and Approver of one subagent's run: its calls
// are recorded as a parent's are, under its own run. status, report, errText and
// streamErr are how the loop settled, which the task's end writes: status is
// empty until Settled, and report is the text of the last reply of a subagent
// that succeeded.
type subagent struct {
	*runJournal
	status    runStatus
	report    string
	errText   string
	streamErr error
}

// Progress publishes nothing: the subagent's text is its report, which reaches
// the parent as a notice.
func (c *subagent) Progress([]llm.Block) {}

// Settled keeps how the loop ended. It writes nothing: the task's end writes the
// run whole, with its rows, in one transaction.
func (c *subagent) Settled(_ context.Context, res agent.Result, err error) error {
	c.streamErr = err
	c.status, c.errText = runOutcome(err)
	if c.status == runSucceeded {
		c.report = lastReplyText(res.Blocks)
	}
	return nil
}

// end is how the subagent's task ends, off the run's outcome: stopped for a run
// that settled cancelled, since only a stop cancels it; completed for one that
// answered; failed for any other, its own error or nothing to say. The task's
// row write ends the run and heals its rows too.
func (c *subagent) end(by string, at time.Time) taskEnd {
	end := taskEnd{Status: taskFailed, At: at}
	end.Run = func(ctx context.Context, st stmts) error {
		return settleRun(ctx, st, c.runID, c.status, c.report, c.errText, at)
	}
	// Every row whole again: it heals a write that did not land, and closes a
	// call a panic left open.
	end.Rows = func(ctx context.Context, st stmts) error {
		c.closeOpen("", c.streamErr, at)
		return c.writeCalls(ctx, st)
	}
	switch {
	case c.status == runCancelled:
		end.Status, end.StoppedBy, end.Notified = taskStopped, by, by == stoppedByModel
	case c.status == runSucceeded && c.report != "":
		end.Status = taskCompleted
	}
	return end
}

// lastReplyText is the text of the last reply in a run's blocks: llm.Text over
// the blocks after the last result.
func lastReplyText(blocks []llm.Block) string {
	start := 0
	for i, b := range blocks {
		if b.Type == llm.BlockToolResult {
			start = i + 1
		}
	}
	return llm.Text(blocks[start:])
}
