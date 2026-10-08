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

// Package logsview is LogsView: a live view of logs the user looks at, opened by
// the model's call. The tool checks the call's sources against the mirror, resolves
// its anchor on the sidecar's clock, records what it showed as the call's action
// and answers a one-line receipt. It reads no log line and asks no one.
//
// The tool is a skeleton: its offer and prompt are written; parse, resolve and the
// receipt are not, and it is not in the box.
package logsview

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

//go:embed prompts/logsview.md
var prompt string

// description is the tool description the model reads with the schema.
//
//go:embed prompts/description.md
var description string

//go:embed prompts/schema.json
var inputSchema []byte

// Name is the name LogsView is offered under.
const Name = "LogsView"

// errNotImplemented is a step the tool does not take yet.
var errNotImplemented = errors.New("logsview: not implemented")

var _ tools.Custom = (*Tool)(nil)

// Tool is LogsView, over the cluster service whose mirror it checks against.
type Tool struct{ svc cluster.Service }

// New is the tool over svc.
func New(svc cluster.Service) *Tool { return &Tool{svc: svc} }

func (t *Tool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: Name, Description: description, InputSchema: json.RawMessage(inputSchema)}
}

func (t *Tool) Prompt() string { return prompt }

func (t *Tool) Name() string { return Name }

func (t *Tool) ActionKind() tools.ActionKind { return tools.ActionLogsView }

// Action is the call's description and the view as it was shown.
func (t *Tool) Action(raw json.RawMessage, _ string, _ bool) (tools.Action, error) {
	in, err := parse(raw)
	if err != nil {
		return tools.Action{}, err
	}
	return tools.Action{Description: in.description, LogsView: &in.view}, nil
}

// Run checks the call's sources, resolves its anchor and answers the receipt.
func (t *Tool) Run(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool) {
	in, err := parse(raw)
	if err != nil {
		return refusal(err), true
	}
	view, err := t.resolve(ctx, rt.ClusterID, in)
	if err != nil {
		return refusal(err), true
	}
	return receipt(view), false
}

// input is one call's arguments as parse reads them: the description, and the
// view as the model typed it.
type input struct {
	description string
	view        tools.LogsViewAction
}

// parse reads the call's arguments. Not built yet: every input is refused.
func parse(json.RawMessage) (input, error) {
	return input{}, errNotImplemented
}

// refusal is an error as the model reads it. Not built yet: one code for all.
func refusal(error) string {
	return `{"error":"not-implemented"}`
}

// receipt is the one line the model reads of a view that opened. Not built yet.
func receipt(tools.LogsViewAction) string {
	return ""
}
