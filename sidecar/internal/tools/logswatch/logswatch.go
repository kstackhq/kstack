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

// Package logswatch is LogsWatch: a follow of a view that runs as a background
// task of the chat and wakes the model once, when it ends. Its watch is rebuilt
// on the log service's new follow (docs/specs/log-fetch-api); until then the tool
// is offered and its run is not implemented.
package logswatch

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

//go:embed prompts/logswatch.md
var prompt string

// description is the tool description the model reads with the schema.
//
//go:embed prompts/description.md
var description string

//go:embed prompts/schema.json
var inputSchema []byte

// Name is the name LogsWatch is offered under.
const Name = "LogsWatch"

// callTimeout bounds a call: a resolve and a follow's start, not the watch.
const callTimeout = 30 * time.Second

// errNotShown is a call read off its arguments: the watch is the run's.
var errNotShown = errors.New("logswatch: the watch is the run's")

var (
	_ tools.Shown   = (*Tool)(nil)
	_ tools.Bounded = (*Tool)(nil)
)

// Tool is LogsWatch, over the cluster service whose mirror it checks against and
// the clock its window is read on.
type Tool struct {
	svc cluster.Service
	now func() time.Time
}

// New is the tool over svc, on the clock now.
func New(svc cluster.Service, now func() time.Time) *Tool {
	return &Tool{svc: svc, now: now}
}

func (t *Tool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: Name, Description: description, InputSchema: json.RawMessage(inputSchema)}
}

func (t *Tool) Prompt() string { return prompt }

func (t *Tool) Name() string { return Name }

func (t *Tool) ActionKind() tools.ActionKind { return tools.ActionLogsWatch }

// CallTimeout is the loop's bound on a call.
func (t *Tool) CallTimeout(json.RawMessage) time.Duration { return callTimeout }

// Action is none: the watch is what the run resolved, which RunShown hands back and
// the row keeps.
func (t *Tool) Action(json.RawMessage, string, bool) (tools.Action, error) {
	return tools.Action{}, errNotShown
}

// RunShown starts the call's watch and answers the receipt, with what it watches
// as the action.
func (t *Tool) RunShown(context.Context, tools.Runtime, json.RawMessage) (string, bool, *tools.Action) {
	panic("not implemented")
}

// Run is RunShown with the action dropped.
func (t *Tool) Run(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool) {
	text, isError, _ := t.RunShown(ctx, rt, raw)
	return text, isError
}
