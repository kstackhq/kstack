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

// Package logsread is LogsRead: the model's read of a range of logs. The tool
// checks the call's view against the mirror as LogsView does, reads one page of
// it through the log service, and answers a bounded sample of its lines, which
// the call's action keeps as the record of what the model read. It asks no one.
package logsread

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
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

// A read's bounds: the page's own deadline, inside the loop's on the call, so a
// slow read answers timed-out itself rather than being cut off.
const (
	readTimeout = 25 * time.Second
	callTimeout = 30 * time.Second
)

// errNotShown is a call read off its arguments: the sample is the run's.
var errNotShown = errors.New("logsread: the sample is the run's")

// Fetcher reads one page of a view: the log service.
type Fetcher interface {
	Fetch(ctx context.Context, id cluster.ClusterID, view logs.View, q logs.FetchQuery) (logs.Page, error)
}

var (
	_ tools.Shown   = (*Tool)(nil)
	_ tools.Bounded = (*Tool)(nil)
)

// Tool is LogsRead, over the cluster service whose mirror it checks against, the
// log service it reads through, and the clock a duration is measured back from.
type Tool struct {
	svc  cluster.Service
	logs Fetcher
	now  func() time.Time
	// readTimeout is the page's deadline.
	readTimeout time.Duration
}

// New is the tool over svc and logs, on the clock now.
func New(svc cluster.Service, logs Fetcher, now func() time.Time) *Tool {
	return &Tool{svc: svc, logs: logs, now: now, readTimeout: readTimeout}
}

func (t *Tool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: Name, Description: description, InputSchema: json.RawMessage(inputSchema)}
}

func (t *Tool) Prompt() string { return prompt }

func (t *Tool) Name() string { return Name }

func (t *Tool) ActionKind() tools.ActionKind { return tools.ActionLogsRead }

// CallTimeout is the loop's bound on a call, past the read's own.
func (t *Tool) CallTimeout(json.RawMessage) time.Duration { return callTimeout }

// Action is none: the sample is what the run read, which RunShown hands back and
// the row keeps, so a call that read nothing shows its kind alone.
func (t *Tool) Action(json.RawMessage, string, bool) (tools.Action, error) {
	return tools.Action{}, errNotShown
}

// RunShown checks the call's view, reads one page of its range, and answers the
// sample with what the model read as the action. A refusal names what to change
// and never the model's text.
func (t *Tool) RunShown(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool, *tools.Action) {
	now := t.now()
	in, err := parse(raw, now)
	if err != nil {
		return logsinput.Refusal(err), true, nil
	}
	view, notes, err := logsinput.Resolve(ctx, t.svc, now, rt.ClusterID, in.Input)
	if err != nil {
		return logsinput.Refusal(err), true, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, t.readTimeout)
	defer cancel()
	page, err := t.logs.Fetch(readCtx, rt.ClusterID, view.LogsViewInput, in.query())
	if err != nil {
		return readRefusal(readCtx, err), true, nil
	}
	s := sampleOf(in, page)
	read := s.action(in, view)
	return s.output(in, notes, view), false, &tools.Action{Description: in.Description, LogsRead: &read}
}

// Run is RunShown with the action dropped.
func (t *Tool) Run(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool) {
	text, isError, _ := t.RunShown(ctx, rt, raw)
	return text, isError
}

// query is the page the call reads.
func (in input) query() logs.FetchQuery {
	q := logs.FetchQuery{Mode: logs.FetchHead, Limit: in.limit}
	if in.mode == tools.LogsReadTail {
		q.Mode = logs.FetchTail
	}
	if in.since != nil {
		q.Since = &in.since.cursor
	}
	if in.until != nil {
		q.Until = &in.until.cursor
	}
	return q
}

// readRefusal is a failed read as the model reads it: past its deadline, refused
// by the service in its own words, which may carry cluster text, or as the view's
// refusals are.
func readRefusal(readCtx context.Context, err error) string {
	var rf logs.Refusal
	switch {
	case errors.Is(readCtx.Err(), context.DeadlineExceeded):
		return `{"error":"timed-out"}`
	case errors.As(err, &rf):
		return logsinput.Result(map[string]any{"error": "refused", "message": logs.ForModel(rf.Reason)})
	}
	return logsinput.Refusal(err)
}
