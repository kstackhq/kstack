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

// Package logsread is LogsRead: the model's read of a range of logs. The call's
// read is rebuilt on the log service's window query (docs/specs/log-fetch-api);
// until then the tool is offered and its run is not implemented.
package logsread

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

//go:embed prompts/logsread.md
var prompt string

// description is the tool description the model reads with the schema.
//
//go:embed prompts/description.md
var description string

//go:embed prompts/schema.json
var inputSchema []byte

// Name is the name LogsRead is offered under.
const Name = "LogsRead"

// callTimeout is the loop's bound on a call.
const callTimeout = 30 * time.Second

// errNotShown is a call read off its arguments: the sample is the run's.
var errNotShown = errors.New("logsread: the sample is the run's")

var (
	_ tools.Shown   = (*Tool)(nil)
	_ tools.Bounded = (*Tool)(nil)
)

// Tool is LogsRead, over the cluster service whose mirror it checks against and
// the clock a duration is measured back from.
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

func (t *Tool) ActionKind() tools.ActionKind { return tools.ActionLogsRead }

// CallTimeout is the loop's bound on a call.
func (t *Tool) CallTimeout(json.RawMessage) time.Duration { return callTimeout }

// Action is none: the sample is what the run read, which RunShown hands back and
// the row keeps.
func (t *Tool) Action(json.RawMessage, string, bool) (tools.Action, error) {
	return tools.Action{}, errNotShown
}

// RunShown reads the call's range and answers the sample, with what the model
// read as the action.
func (t *Tool) RunShown(context.Context, tools.Runtime, json.RawMessage) (string, bool, *tools.Action) {
	panic("not implemented")
}

// Run is RunShown with the action dropped.
func (t *Tool) Run(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool) {
	text, isError, _ := t.RunShown(ctx, rt, raw)
	return text, isError
}
