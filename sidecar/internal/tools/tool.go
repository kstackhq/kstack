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

// Package tools is what the sidecar can do for a model: the offer, the prompt
// section that explains it, and the code that answers one call. A binding is a
// Tool over a capability — the capability is the one piece of code that runs
// something and knows no contract, and the binding parses its contract's input
// into the capability's request and renders the outcome in the contract's shape.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/safe"
	"github.com/kstackhq/kstack/sidecar/internal/session"
)

const (
	// InlineLimit is the most a result carries, header included.
	InlineLimit = 30_000
	// FileLimit is the most output kept, and the largest file Read opens.
	FileLimit = 8 << 20
)

// Reader reads what a stored call of one tool did. Every Tool is one.
type Reader interface {
	// Name is the tool's identity, unique in the box, and what every stored call
	// of it names: a custom tool's definition name, or a native tool's own name,
	// qualified by its vendor.
	Name() string
	// ActionKind is what the tool's calls do.
	ActionKind() ActionKind
	// Action is what a call does, read from its arguments and what its row
	// keeps: the cwd, and whether a sandbox confined the call. An error is
	// arguments the tool refuses, or a tool with nothing to show.
	Action(input json.RawMessage, cwd string, sandboxed bool) (Action, error)
}

// Tool is one thing the model can call. What else it is follows from the
// interfaces it implements: a Custom tool, or a Native one, which the sidecar
// runs when it is a Runner and the provider runs when it is Budgeted.
type Tool interface {
	Reader
	// Prompt is the tool's section of "What you can do": how to use it, what its
	// results mean. Markdown opening with its own ## heading.
	Prompt() string
}

// Runner is a Tool the sidecar runs.
type Runner interface {
	Tool
	// Run answers one call in the chat rt stands for. text is what the model
	// reads; isError marks a refusal. Neither may carry a Go error's text.
	Run(ctx context.Context, rt Runtime, input json.RawMessage) (text string, isError bool)
}

// Custom is a tool the app defines, offered by its definition.
type Custom interface {
	Runner
	// Definition is the offer the model sees. Its Name is the tool's.
	Definition() llm.ToolDefinition
}

// PerTarget is a Custom tool whose offer depends on the turn's target.
type PerTarget interface {
	Custom
	// For is the tool as a turn on t is offered it.
	For(t llm.Target) Custom
}

// ContractName is a vendor's own identifier for the shape of one of its tools:
// the Messages API's web_search_20260318, say.
type ContractName string

// Native is a tool a vendor defines. The sidecar runs it when it is also a
// Runner; the provider runs it otherwise. It has three names: Name, our
// identity for it, vendor-qualified and unique in the box; ContractName, the
// vendor's own identifier for its shape, recorded on each of its rows; and each
// wire's call name (MessagesCallName), which its calls arrive under there.
type Native interface {
	Tool
	llm.NativeTool
	ContractName() ContractName
}

// Budgeted is a native tool the provider runs, capped per turn.
type Budgeted interface {
	Native
	// MaxUses is the calls one turn may make, spread over its requests.
	MaxUses() int
	// Allowance is the tokens the provider adds to one turn for this tool at its
	// cap: what a length check keeps free for it.
	Allowance() int
}

// Approval is what the gate records on a gated call's row once its tool has
// read the input: Cwd, where it will start, exactly as it starts there, empty
// for a tool that runs nowhere.
type Approval struct {
	Cwd string
	// Sandboxed is whether a sandbox confines the call, recorded whether or not
	// it asks.
	Sandboxed bool
	// Skip runs the call without asking. The zero value asks.
	Skip bool
	// Network is the network a sandboxed call runs with. NetworkApproved holds
	// only once the user approves the call.
	Network session.Network
	// Folder is the granted folder a skip rests on, nil for any other skip and
	// for a call that asks. A tool reaches the call's path through it alone.
	Folder *session.Folder
}

// Gated is a Runner whose call runs only once the user has approved it, unless
// its Approval skips. The loop finds it by type assertion at the call; the box
// need not know. A call that asks has an action the approval request can draw.
type Gated interface {
	Runner
	// Approval is what the gate records. An error is an input the tool cannot
	// read or refuses: the call is refused and nothing is shown, answered bad-input
	// unless the error is a *Refusal. It reads rt and never starts or stops a task,
	// or writes a stamp.
	Approval(ctx context.Context, rt Runtime, input json.RawMessage) (Approval, error)
}

// ApprovedRunner is a Gated tool whose run takes the Approval its gate
// decided, so what runs is what was decided or approved, whatever the
// session says by the time it starts. The loop calls RunApproved in place of
// Run.
type ApprovedRunner interface {
	Gated
	RunApproved(ctx context.Context, rt Runtime, input json.RawMessage, a Approval) (text string, isError bool)
}

// Refusal is an Approval error carrying the result the model reads, so a call
// refused before the user is asked says why in the tool's own words.
type Refusal struct {
	Result string
}

func (e *Refusal) Error() string { return "refused: " + e.Result }

// ActionKind is what a call of a tool does, from a closed set. A turn is offered
// at most one tool of each kind, and a tool's Action is always of its kind.
type ActionKind string

const (
	ActionCommand   ActionKind = "command"
	ActionRead      ActionKind = "read"
	ActionWrite     ActionKind = "write"
	ActionEdit      ActionKind = "edit"
	ActionSearch    ActionKind = "search"
	ActionFetch     ActionKind = "fetch"
	ActionStop      ActionKind = "stop" // stops a background task; its calls show no action
	ActionMemory    ActionKind = "memory"
	ActionDelegate  ActionKind = "delegate"   // hands a task to a subagent
	ActionKubeQuery ActionKind = "kube-query" // SQL over the cluster's cache
)

// ActionKinds is every kind, in declaration order.
var ActionKinds = []ActionKind{ActionCommand, ActionRead, ActionWrite, ActionEdit, ActionSearch, ActionFetch, ActionStop, ActionMemory, ActionDelegate, ActionKubeQuery}

// valid reports whether k is one of ActionKinds.
func (k ActionKind) valid() bool {
	return slices.Contains(ActionKinds, k)
}

// Action is what a call does, read from its arguments by its own tool:
// Description, the model's own account of it, empty when it gave none, and
// exactly one kind.
type Action struct {
	Description string           `json:"description"`
	Command     *CommandAction   `json:"command"`
	Read        *ReadAction      `json:"read"`
	Write       *WriteAction     `json:"write"`
	Edit        *EditAction      `json:"edit"`
	Search      *SearchAction    `json:"search"`
	Fetch       *FetchAction     `json:"fetch"`
	Memory      *MemoryAction    `json:"memory"`
	Delegate    *DelegateAction  `json:"delegate"`
	KubeQuery   *KubeQueryAction `json:"kubeQuery"`
}

// Kind is the kind whose field is set, or "" for none.
func (a Action) Kind() ActionKind {
	switch {
	case a.Command != nil:
		return ActionCommand
	case a.Read != nil:
		return ActionRead
	case a.Write != nil:
		return ActionWrite
	case a.Edit != nil:
		return ActionEdit
	case a.Search != nil:
		return ActionSearch
	case a.Fetch != nil:
		return ActionFetch
	case a.Memory != nil:
		return ActionMemory
	case a.Delegate != nil:
		return ActionDelegate
	case a.KubeQuery != nil:
		return ActionKubeQuery
	}
	return ""
}

// CommandAction is a command: Text is what the shell receives, byte for byte;
// Cwd is where it started, empty when the call never reached the gate;
// Background is a command that keeps running after its call answers; Network
// is a call that asked for the internet, which changes nothing outside the
// sandbox.
type CommandAction struct {
	Text       string `json:"text"`
	Cwd        string `json:"cwd"`
	Background bool   `json:"background"`
	Network    bool   `json:"network"`
	// Sandboxed is whether a sandbox confined the command, off its row.
	Sandboxed bool `json:"sandboxed"`
}

// ReadAction is a read of the file the call named, as it named it.
type ReadAction struct {
	Path string `json:"path"`
}

// WriteAction is a file written whole: Path as the call named it, and Content,
// the file's new bytes.
type WriteAction struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// EditAction is a replacement in a file: Path as the call named it, OldString
// the text that goes and NewString the text that replaces it, byte for byte,
// and ReplaceAll every occurrence rather than one.
type EditAction struct {
	Path       string `json:"path"`
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll"`
}

// SearchAction is a web search: Query is what left the machine, empty for a call
// that sent none.
type SearchAction struct {
	Query string `json:"query"`
}

// FetchAction is a page fetched: URL as the sidecar fetches it, https with its
// host in ASCII, and Host, the host it dials, with the port when it is not 443.
type FetchAction struct {
	URL  string `json:"url"`
	Host string `json:"host"`
}

// MemoryAction is a call of the Memory tool as its arguments name it: Op, Name,
// on a save the note's Body, and Scope, who the note reaches: cluster, or
// everywhere for a call that asks the user.
type MemoryAction struct {
	Op    string `json:"op"`
	Name  string `json:"name"`
	Body  string `json:"body"`
	Scope string `json:"scope"`
}

// KubeQueryAction is a statement over the chat's cluster's cache: SQL as it runs,
// without a trailing semicolon, and Limit, the most rows the call asked for.
type KubeQueryAction struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit"`
}

// DelegateAction is a task handed to a subagent: Prompt, the brief the parent
// wrote; AgentType, the kind of agent; Model, the model it runs on, empty for
// the parent's.
type DelegateAction struct {
	Prompt    string `json:"prompt"`
	AgentType string `json:"agentType"`
	Model     string `json:"model"`
}

// Bounded is a Runner whose calls run under a bound of their own rather than the
// turn's DefaultToolTimeout.
type Bounded interface {
	Runner
	// CallTimeout is the loop's bound on this call. input is the call's own; an
	// input the tool cannot read gets any bound, since it never runs.
	CallTimeout(input json.RawMessage) time.Duration
}

// ChatDir is a chat's own directory, deleted with the chat: its saved results,
// its background tasks' output and its workspace.
type ChatDir interface {
	// Path is the directory as a result names it to the model. It may not exist.
	Path() string
	// Root opens the directory as a root, so no name under it resolves outside
	// it. create makes it, 0700, when it is missing; without create a missing
	// directory is fs.ErrNotExist. The caller closes the root.
	Root(create bool) (*os.Root, error)
}

// Tasks is a chat's background tasks.
type Tasks interface {
	// Start records a task for the call now running and starts it. start gets
	// the task's output file, open for writing, and returns the running process.
	// It answers the task's id and the file's path as a result names it, or
	// ErrChatTaskLimit, ErrTaskLimit, or why the task could not start.
	Start(start func(out *os.File) (Task, error)) (id, path string, err error)
	// Stop stops a running task of this chat on the model's word. false when
	// the chat has no running task by that id.
	Stop(id string) bool
}

// The limits a task start can meet: the chat's own, or the app's.
var (
	ErrChatTaskLimit = errors.New("tools: the chat runs as many background tasks as it may")
	ErrTaskLimit     = errors.New("tools: the app runs as many background tasks as it may")
)

// StartRefusal is what the model reads when a task could not start: a limit in
// words it can act on, naming commands and agents since they share the slots,
// or the start's own error.
func StartRefusal(err error) string {
	switch {
	case errors.Is(err, ErrChatTaskLimit):
		return "At most 4 background commands and agents run at once in a chat. Wait for a notification, or stop one with TaskStop."
	case errors.Is(err, ErrTaskLimit):
		return "At most 16 background commands and agents run at once in Kstack. Wait for one to finish."
	}
	return "could not start: " + safe.String(err.Error())
}

// TaskOutputLimit is the most of a task's output its file keeps: FileLimit less
// room for the line that ends it, so Read still opens a full file.
const TaskOutputLimit = FileLimit - 64

// Task is one background process a tool started.
type Task interface {
	// Wait blocks until the process is reaped and its output copied, and
	// answers how it ended. It releases everything the process held.
	Wait() Exit
	// Stop ends the process's group, or its job on Windows. now kills at once;
	// otherwise the tool's own grace applies where the platform has one. Safe to
	// call more than once, and after the reap, when it does nothing.
	Stop(now bool)
}

// Exit is how a task ended: its code as the foreground reads it, OK false when
// the code could not be read, and Stopped when a stop reached it before the reap.
type Exit struct {
	Code    int
	OK      bool
	Stopped bool
}

// Stamp is what a turn last saw of a file: the SHA-256 of its bytes, and
// whether the model was shown every byte as it is.
type Stamp struct {
	Sum   [32]byte
	Whole bool
}

// FileStamps is what a chat's turns have seen of the files they read or
// wrote, by the path as fileguard.Abs spells it.
type FileStamps interface {
	Stamp(path string) (Stamp, bool)
	SetStamp(path string, s Stamp)
}

// Delegation is one Agent call's request: the type, the brief, and the model
// override, empty for the parent's.
type Delegation struct {
	Type, Prompt, Model string
}

// ErrUnknownModel is a model the parent's provider does not offer with tools.
var ErrUnknownModel = errors.New("tools: no such model takes tools")

// Spawner starts a subagent for the call now running.
type Spawner interface {
	// Start writes the subagent's run and task and starts it. It answers the
	// task's id, or ErrUnknownModel, ErrChatTaskLimit, ErrTaskLimit, or the
	// error that stopped it.
	Start(ctx context.Context, d Delegation) (id string, err error)
}

// ClusterWrite is what a sandboxed command sent to change the cluster, as the
// user is shown it.
type ClusterWrite struct {
	Method string `json:"method"`
	// Path is the path and raw query, as forwarded.
	Path string `json:"path"`
	// Subresource is the path's subresource as the proxy parsed it; "" for none.
	Subresource string `json:"subresource"`
	ContentType string `json:"contentType"`
	// Body is valid UTF-8, or empty.
	Body   string `json:"body"`
	DryRun bool   `json:"dryRun"`
}

// ActionRequest is one classified action held for the user, or decided with
// nobody asked. It is what an action approval's request column holds.
type ActionRequest struct {
	Action permissions.Action `json:"action"`
	// Grantable is whether an answer may write a rule that allows it.
	Grantable bool `json:"grantable"`
	// CommandRule and ChatRule are the rules each allow answer adds, in the
	// words Settings uses: Rule.Line of the rule a command answer adds, and of
	// the one a chat or always answer writes. Empty when not grantable.
	CommandRule string `json:"commandRule"`
	ChatRule    string `json:"chatRule"`
	// Write is the request as sent; nil for an action with none.
	Write *ClusterWrite `json:"write,omitempty"`
	// Diff is the change as a unified diff of YAML; "" for none.
	Diff string `json:"diff"`
	// DiffCut is a diff that stops short of the whole change.
	DiffCut bool `json:"diffCut"`
	// DiffError is why there is no diff, when one was looked for.
	DiffError string `json:"diffError"`
}

// Answer is the user's decision on an action: approved or not, and for how
// long.
type Answer struct {
	Approved bool
	Duration permissions.Duration
}

// ActionAsker puts an action to the user, as a request of the call that is
// running, and records one the proxy decided with nobody asked against that
// call, with the reason in the user's words.
type ActionAsker interface {
	Ask(ctx context.Context, r ActionRequest) (Answer, error)
	Record(ctx context.Context, r ActionRequest, d permissions.Decision, reason string) error
}

// Runtime is what a tool gets of the chat its call runs in. chatsvc sets every
// field it has; a test sets the ones its tool reads. ClusterID is the chat's stored
// cluster, the one a tool acting on a cluster reaches, and ChatID the chat; a model
// names neither. Session is the run's policy, the chat's switch as its turn read
// it included. Agent is nil in a subagent's runtime, so a subagent spawns
// nothing. ActionAsker is nil where nobody can be asked.
type Runtime struct {
	ClusterID   apimeta.ClusterID
	ChatID      apimeta.ChatID
	Session     session.Session
	Dir         ChatDir
	Tasks       Tasks
	Files       FileStamps
	Agent       Spawner
	ActionAsker ActionAsker
}
