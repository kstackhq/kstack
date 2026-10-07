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

// Package chat is the chat service: chats, their messages and the
// answers a model gives, as the GraphQL layer serves them.
package chat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/agent/loop"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/lib/rawjson"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// ChatID identifies a chat: the transcript's order is seq, and the list's
// is updated_at.
type ChatID = apimeta.ChatID

// MessageID identifies one message.
type MessageID string

// RunID identifies one agent run.
type RunID string

// LLMCallID identifies one model call and ToolCallID one tool call. Neither
// orders anything: a call's place in its parent is its seq.
type (
	LLMCallID  string
	ToolCallID string
)

func newChatID() ChatID         { return ChatID(appdb.NewID()) }
func newMessageID() MessageID   { return MessageID(appdb.NewID()) }
func newRunID() RunID           { return RunID(appdb.NewID()) }
func newLLMCallID() LLMCallID   { return LLMCallID(appdb.NewID()) }
func newToolCallID() ToolCallID { return ToolCallID(appdb.NewID()) }
func newApprovalID() ApprovalID { return ApprovalID(appdb.NewID()) }
func newTaskID() TaskID         { return TaskID(appdb.NewID()) }

// TaskID identifies one background task: its row, its output file's name, and
// the id the model stops it by.
type TaskID string

// ApprovalID identifies one approvals row: a UUIDv7 minted when a command is
// shown to the user, and what approvalDecide takes.
type ApprovalID string

// ApprovalStatus is where the user's decision is: pending until it commits. The
// row's vocabulary, and a named type because the GraphQL enum binds onto it value
// by value.
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalDenied   ApprovalStatus = "denied"
	// ApprovalAbandoned is a cluster write whose wait ended with no decision
	// while its call ran on; a call's own approval never takes it.
	ApprovalAbandoned ApprovalStatus = "abandoned"
	// ApprovalAllowed and ApprovalRefused are cluster writes the permissions
	// engine decided with nobody asked.
	ApprovalAllowed ApprovalStatus = "allowed"
	ApprovalRefused ApprovalStatus = "refused"
)

// Role is who produced a message, as the Messages API's wire format names them.
// A named type because the GraphQL enum binds onto it value by value.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// MessageStatus is how far along a turn is. A user message is born complete; an
// assistant message is born streaming and settles exactly once.
type MessageStatus string

const (
	StatusStreaming MessageStatus = "streaming"
	// StatusWaitingApproval is a turn stopped on a command the user has not
	// decided: in flight, like Streaming, and drawn with the approval request.
	StatusWaitingApproval MessageStatus = "waiting_approval"
	StatusComplete        MessageStatus = "complete"
	StatusFailed          MessageStatus = "failed"
	StatusCancelled       MessageStatus = "cancelled"
)

// inFlight is whether the turn is still running: nothing settled yet.
func (s MessageStatus) inFlight() bool {
	return s == StatusStreaming || s == StatusWaitingApproval
}

// Mode is which of the app's two modes a chat belongs to, fixed at creation. A named
// type because the GraphQL enum binds onto it value by value.
type Mode string

const (
	ModeChat      Mode = "chat"
	ModeDashboard Mode = "dashboard"
)

// Chat mirrors a chats row.
type Chat struct {
	ID    ChatID
	Title string
	Mode  Mode
	// The cluster the chat was started under. Fixed at creation.
	ClusterID apimeta.ClusterID
	// SandboxDisabled is the user's switch: the chat's commands run outside the
	// sandbox, each asking first.
	SandboxDisabled bool
	// NetworkEnabled is the user's other switch: the chat's sandboxed commands
	// reach the internet.
	NetworkEnabled bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// AwaitingApproval is whether any run of the chat waits on the user. Every
	// write that moves a run into or out of waiting_approval pings the list.
	AwaitingApproval bool
}

// ChatMessage is a messages row joined with its run, as the reads serve it.
// Comparable by value — the watch diff is ==, so the nullable columns are sql.Null*
// rather than pointers.
type ChatMessage struct {
	ID      MessageID
	ChatID  ChatID
	Seq     int64
	Role    Role
	Content rawjson.RawJSON
	// RunID is the run that produces an assistant message; empty on a user message.
	RunID  RunID
	Status MessageStatus
	Error  string
	// AwaitingApproval is whether a run of the answer waits on the user: its own,
	// or a subagent's under one of its Agent calls. A subagent can wait under an
	// answer that has settled, so this, not Status, says whether its request can
	// be answered.
	AwaitingApproval bool

	// ProviderID, ModelID and Effort are what the send asked for, the run's columns
	// written when it was accepted: a turn that fails or is interrupted still says
	// what it was to run. Empty on a user message, which has no run.
	ProviderID string
	ModelID    string
	Effort     string
	// FinishReason is the latest stop reason among the turn's model calls.
	FinishReason string
	// hasStored marks a stored answer holding what its live list lacks: a call
	// that started a task, or one that ran under an Agent call. Only such an
	// answer's live list is merged with it (overlay).
	hasStored bool
	// ToolCalls is the turn's calls off their rows, as marshalToolCalls spells
	// them: one string rather than a slice so the message stays comparable, and
	// [] on a message with none.
	ToolCalls rawjson.RawJSON
	// Citations is what the content's text cites, read from its payloads by the
	// run's dialect before a reader is shown the content without them: one string
	// like ToolCalls, and [] on a message that cites nothing.
	Citations  rawjson.RawJSON
	CreatedAt  time.Time
	FinishedAt sql.NullTime
	// dialect is the run's, which reads the payloads' citations; "" on a user
	// message, which has no run.
	dialect llm.Dialect
}

// CitationList is the message's citations, parsed from the string it carries.
func (m ChatMessage) CitationList() ([]*llm.Citation, error) {
	var citations []*llm.Citation
	if err := json.Unmarshal([]byte(m.Citations), &citations); err != nil {
		return nil, fmt.Errorf("citations: %w", err)
	}
	return citations, nil
}

// emptyCitations is a message that cites nothing.
const emptyCitations rawjson.RawJSON = "[]"

// marshalCitations is the one spelling of a message's citations, the live
// message's and the stored read's.
func marshalCitations(citations []llm.Citation) rawjson.RawJSON {
	if len(citations) == 0 {
		return emptyCitations
	}
	b, _ := json.Marshal(citations) // strings always marshal
	return rawjson.RawJSON(b)
}

// ToolCallList is the message's calls, parsed from the string it carries.
func (m ChatMessage) ToolCallList() ([]*ToolCall, error) {
	var calls []*ToolCall
	if err := json.Unmarshal([]byte(m.ToolCalls), &calls); err != nil {
		return nil, fmt.Errorf("tool calls: %w", err)
	}
	return calls, nil
}

// Thinking is what the message holds of the model's thinking: empty for a
// question, and for content that does not parse — a reader is owed a string, not
// a second way for a stored row to fail.
func (m ChatMessage) Thinking() string {
	blocks, err := unmarshalBlocks(m.Content)
	if err != nil {
		return ""
	}
	return llm.Thinking(blocks)
}

// emptyContent is an answer with nothing written yet: the stored form of no blocks.
const emptyContent rawjson.RawJSON = "[]"

// marshalBlocks is the content column's form of blocks. Nil marshals as
// emptyContent, since an empty column is not JSON.
func marshalBlocks(blocks []llm.Block) rawjson.RawJSON {
	if len(blocks) == 0 {
		return emptyContent
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		// A Block holds strings, an Input that is valid JSON by construction, and
		// a Payload that is an object, by the wires' replayable check.
		panic(err)
	}
	return rawjson.RawJSON(b)
}

// unmarshalBlocks is the blocks a content column holds.
func unmarshalBlocks(c rawjson.RawJSON) ([]llm.Block, error) {
	var blocks []llm.Block
	if err := json.Unmarshal([]byte(c), &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// emptyToolCalls is a message with no tool calls: every message built in Go starts
// here, which is also what the stored read gives one with no rows.
const emptyToolCalls rawjson.RawJSON = "[]"

// ToolCallStatus is where a tool call is, as the transcript reads it. A named
// type because the GraphQL enum binds onto it value by value.
type ToolCallStatus string

const (
	ToolCallNotRun           ToolCallStatus = "NotRun"
	ToolCallAwaitingApproval ToolCallStatus = "AwaitingApproval"
	ToolCallDenied           ToolCallStatus = "Denied"
	ToolCallRunning          ToolCallStatus = "Running"
	ToolCallSucceeded        ToolCallStatus = "Succeeded"
	ToolCallFailed           ToolCallStatus = "Failed"
	ToolCallInterrupted      ToolCallStatus = "Interrupted"
)

// ToolCallRunsOn is who ran a tool call. A named type because the GraphQL enum
// binds onto it value by value.
type ToolCallRunsOn string

const (
	ToolCallRunsOnSidecar  ToolCallRunsOn = "Sidecar"
	ToolCallRunsOnProvider ToolCallRunsOn = "Provider"
)

// ToolCallNetwork is why a sandboxed call reached the internet. A named type
// because the GraphQL enum binds onto it value by value.
type ToolCallNetwork string

const (
	ToolCallNetworkChat     ToolCallNetwork = "Chat"
	ToolCallNetworkTurn     ToolCallNetwork = "Turn"
	ToolCallNetworkApproved ToolCallNetwork = "Approved"
)

// toolCallNetworkOf is a call's network as the wire serves it, nil for none.
func toolCallNetworkOf(network session.Network) *ToolCallNetwork {
	var n ToolCallNetwork
	switch network {
	case session.NetworkChat:
		n = ToolCallNetworkChat
	case session.NetworkTurn:
		n = ToolCallNetworkTurn
	case session.NetworkApproved:
		n = ToolCallNetworkApproved
	default:
		return nil
	}
	return &n
}

// ToolCall is one call of a tool in an answer's turn, off its row, as the wire
// serves it.
type ToolCall struct {
	ID        ToolCallID `json:"id"`
	ToolUseID string     `json:"toolUseID"`
	Name      string     `json:"name"`
	// Contract is the vendor's own identifier for the shape the call uses; "" for
	// a tool of ours.
	Contract  string          `json:"contract"`
	Arguments rawjson.RawJSON `json:"arguments"`
	Status    ToolCallStatus  `json:"status"`
	RunsOn    ToolCallRunsOn  `json:"runsOn"`
	// ActionKind is what the call does, its tool's kind whatever the arguments
	// hold; nil for a tool the box does not know.
	ActionKind *tools.ActionKind `json:"actionKind"`
	// Action is what the call does, read from its arguments by its own tool; nil
	// when the tool has no way to show a call or refuses the arguments.
	Action *tools.Action `json:"action"`
	// Approval is the user's decision on the call; nil on a call no one was asked about.
	Approval *ToolCallApproval `json:"approval"`
	// Network is why the call reached the internet; nil for none, and for a call
	// outside the sandbox, whose network is not the switch's.
	Network *ToolCallNetwork `json:"network"`
	// Output is what the model read; empty until the call is over.
	Output  string `json:"output"`
	IsError bool   `json:"isError"`
	// Background is the task the call started; nil on every other call.
	Background *BackgroundTask `json:"background"`
	// AgentCallID is the Agent call a subagent's call ran under; nil on the
	// answer's own calls.
	AgentCallID *ToolCallID `json:"agentCallID"`
	// ClusterWrites is every request the call's sandboxed command sent to change
	// the cluster, in the order asked.
	ClusterWrites []ClusterWrite `json:"clusterWrites"`
}

// ClusterWrite is an action a sandboxed command's request asked for and the
// user's decision on it, as the wire serves them: the request as sent, and the
// action the proxy classified it as. ContentType and Body are empty once it
// no longer waits, since each publish sends the call list whole; the record
// keeps them. An action with no write serves an empty request.
type ClusterWrite struct {
	Approval ToolCallApproval `json:"approval"`
	tools.ClusterWrite
	Action PermissionAction `json:"action"`
	// Diff is the change as YAML while the write waits; DiffCut says it stops
	// short of the whole change, and DiffError why there is none.
	Diff      string `json:"diff"`
	DiffCut   bool   `json:"diffCut"`
	DiffError string `json:"diffError"`
	// Reason is the mode or rule that decided a write nobody was asked about;
	// nil for one the user answered.
	Reason *string `json:"reason"`
}

// PermissionAction is a classified action as the wire serves it: the action,
// whether a rule may allow it, and the rule each allow answer adds, in words.
type PermissionAction struct {
	permissions.Action
	Grantable   bool   `json:"grantable"`
	CommandRule string `json:"commandRule"`
	ChatRule    string `json:"chatRule"`
}

// clusterWriteOf is an action approval as the wire serves it, the request
// whole.
func clusterWriteOf(a ToolCallApproval, r tools.ActionRequest) ClusterWrite {
	w := ClusterWrite{
		Approval: a,
		Action:   PermissionAction{Action: r.Action, Grantable: r.Grantable, CommandRule: r.CommandRule, ChatRule: r.ChatRule},
		Diff:     r.Diff, DiffCut: r.DiffCut, DiffError: r.DiffError,
	}
	if r.Write != nil {
		w.ClusterWrite = *r.Write
	}
	return w
}

// clusterWritesOf is a call's actions as the wire serves them. An action waits
// while its approval is pending and its call running.
func clusterWritesOf(r toolCallEntry) []ClusterWrite {
	out := make([]ClusterWrite, 0, len(r.ClusterWrites))
	for _, a := range r.ClusterWrites {
		w := clusterWriteOf(toolCallApprovalOf(a), *a.Request)
		if a.Reason != "" {
			w.Reason = &a.Reason
		}
		if a.Status != ApprovalPending || r.Status != toolRunning {
			w.ContentType, w.Body = "", ""
			w.Diff, w.DiffCut, w.DiffError = "", false, ""
		}
		out = append(out, w)
	}
	return out
}

// BackgroundTaskStatus is where a background task is, as the transcript reads
// it. A named type because the GraphQL enum binds onto it value by value.
type BackgroundTaskStatus string

const (
	BackgroundTaskRunning   BackgroundTaskStatus = "Running"
	BackgroundTaskExited    BackgroundTaskStatus = "Exited"
	BackgroundTaskCompleted BackgroundTaskStatus = "Completed"
	BackgroundTaskFailed    BackgroundTaskStatus = "Failed"
	BackgroundTaskStopped   BackgroundTaskStatus = "Stopped"
	BackgroundTaskLost      BackgroundTaskStatus = "Lost"
)

// BackgroundTask is a task a call started, off its row: a command, or an agent.
type BackgroundTask struct {
	Status BackgroundTaskStatus `json:"status"`
	// ExitCode is set on an exited task whose code could be read.
	ExitCode *int `json:"exitCode"`
	// Report is a completed agent's report, cut to tools.InlineLimit since it
	// rides every transcript read; empty for a command and until then.
	Report string `json:"report"`
}

// backgroundTaskStatuses is the wire's spelling of each stored status.
var backgroundTaskStatuses = map[string]BackgroundTaskStatus{
	taskRunning: BackgroundTaskRunning, taskExited: BackgroundTaskExited,
	taskCompleted: BackgroundTaskCompleted, taskFailed: BackgroundTaskFailed,
	taskStopped: BackgroundTaskStopped, taskLost: BackgroundTaskLost,
}

// backgroundTaskOf is a task's row as the wire serves it.
func backgroundTaskOf(t *taskState) *BackgroundTask {
	if t == nil {
		return nil
	}
	b := &BackgroundTask{Status: backgroundTaskStatuses[t.Status], Report: tools.Cut(t.Report, 0, tools.InlineLimit, "")}
	if t.ExitCode.Valid {
		code := int(t.ExitCode.Int64)
		b.ExitCode = &code
	}
	return b
}

// ToolCallApproval is the user's decision on a gated call, off its approvals row.
type ToolCallApproval struct {
	ID     ApprovalID     `json:"id"`
	Status ApprovalStatus `json:"status"`
	// Duration is how long an approval holds; nil unless approved.
	Duration *permissions.Duration `json:"duration"`
}

// toolCallApprovalOf is a's id, status and duration as the wire serves them.
func toolCallApprovalOf(a *approval) ToolCallApproval {
	out := ToolCallApproval{ID: a.ID, Status: a.Status}
	if a.Duration != "" {
		d := a.Duration
		out.Duration = &d
	}
	return out
}

// marshalToolCalls is the one spelling of a turn's calls, the live list's and the
// stored read's: SQLite's json_object and Go's encoder escape differently, so both
// paths build the string here from the rows. Each call's action is read through
// box, whether or not a turn offers its tool.
func marshalToolCalls(rows []toolCallEntry, box tools.Box) rawjson.RawJSON {
	out := make([]ToolCall, 0, len(rows))
	for _, r := range rows {
		status := toolCallStatus(r)
		runsOn := ToolCallRunsOnSidecar
		if r.ByProvider {
			runsOn = ToolCallRunsOnProvider
		}
		c := ToolCall{
			ID: r.ID, ToolUseID: r.ToolUseID, Name: r.Name, Contract: r.Contract, Arguments: argumentsJSON(r.Arguments),
			Status: status, RunsOn: runsOn, ActionKind: actionKindOf(box, r), Action: actionOf(box, r),
			Output: r.Result, IsError: r.Error != "", Background: backgroundTaskOf(r.Task),
			ClusterWrites: clusterWritesOf(r), Network: toolCallNetworkOf(r.Network),
		}
		if a := r.Approval; a != nil {
			approval := toolCallApprovalOf(a)
			c.Approval = &approval
		}
		if r.AgentCallID != "" {
			c.AgentCallID = &r.AgentCallID
		}
		out = append(out, c)
	}
	b, err := json.Marshal(out)
	if err != nil {
		panic(err) // strings, and arguments checked valid by argumentsJSON
	}
	return rawjson.RawJSON(b)
}

// actionOf is what a stored call does, read through box, and nil when nothing
// reads it (tools.Box.Action).
func actionOf(box tools.Box, r toolCallEntry) *tools.Action {
	a, ok := box.Action(r.Name, json.RawMessage(r.Arguments), r.Cwd, r.Sandboxed)
	if !ok {
		return nil
	}
	return &a
}

// actionKindOf is a stored call's kind, read through box, and nil when nothing
// reads it (tools.Box.ActionKind).
func actionKindOf(box tools.Box, r toolCallEntry) *tools.ActionKind {
	k, ok := box.ActionKind(r.Name)
	if !ok {
		return nil
	}
	return &k
}

// argumentsJSON is a row's arguments as a JSON value: the input as the model sent
// it, or that text as a string when it does not parse, so the field is never a
// marshal error.
func argumentsJSON(args string) rawjson.RawJSON {
	if json.Valid([]byte(args)) {
		return rawjson.RawJSON(args)
	}
	b, _ := json.Marshal(args) // a string always marshals
	return rawjson.RawJSON(b)
}

// toolCallStatus is a row's status as the transcript reads it, and none for a
// call the provider ran. started_at is what separates a call that never started
// (NotRun) from one that may have (Interrupted when the row has no answer from
// its tool, else Failed).
func toolCallStatus(r toolCallEntry) ToolCallStatus {
	if r.ByProvider {
		return ""
	}
	switch r.Status {
	case toolAwaitingApproval:
		return ToolCallAwaitingApproval
	case toolDenied:
		return ToolCallDenied
	case toolRunning:
		return ToolCallRunning
	case toolSucceeded:
		return ToolCallSucceeded
	case toolFailed:
		if !r.StartedAt.Valid {
			return ToolCallNotRun
		}
		if interrupted(r.Error) {
			return ToolCallInterrupted
		}
		return ToolCallFailed
	}
	return ToolCallNotRun
}

// interrupted is whether a started row's error says it was cut off with no answer
// from its tool: the loop's cancel or deadline, the sweep's stranded, or the panic
// path's interrupted.
func interrupted(errText string) bool {
	if code, ok := loop.RefusalOf(errText); ok {
		return code == loop.CodeCancelled || code == loop.CodeTimeout
	}
	return errText == toolCallStranded || errText == toolCallInterrupted
}
