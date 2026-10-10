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
// the model's call. The tool checks the call's sources against the mirror,
// resolves its anchor on the sidecar's clock, hands what it showed back as the
// call's action and answers a one-line receipt. It reads no log line and asks
// no one.
package logsview

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
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

// errNotShown is a call read off its arguments: the view is the run's.
var errNotShown = errors.New("logsview: the view is the run's")

var _ tools.Shown = (*Tool)(nil)

// Tool is LogsView, over the cluster service whose mirror it checks against,
// and the clock a duration is measured back from.
type Tool struct {
	svc cluster.Service
	now func() time.Time
}

// New is the tool over svc, on the clock now.
func New(svc cluster.Service, now func() time.Time) *Tool { return &Tool{svc: svc, now: now} }

func (t *Tool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: Name, Description: description, InputSchema: json.RawMessage(inputSchema)}
}

func (t *Tool) Prompt() string { return prompt }

func (t *Tool) Name() string { return Name }

func (t *Tool) ActionKind() tools.ActionKind { return tools.ActionLogsView }

// Action is none: the view is what the run showed, which RunShown hands back and
// the row keeps, so a call that did not open one shows its kind alone.
func (t *Tool) Action(json.RawMessage, string, bool) (tools.Action, error) {
	return tools.Action{}, errNotShown
}

// RunShown checks the call's sources, resolves its anchor, and answers the
// receipt with the view as the action. A refusal names what to change and
// never the model's text.
func (t *Tool) RunShown(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool, *tools.Action) {
	in, err := parse(raw)
	if err != nil {
		return refusal(err), true, nil
	}
	view, notes, err := t.resolve(ctx, rt.ClusterID, in)
	if err != nil {
		return refusal(err), true, nil
	}
	return receipt(in.sources, view, notes), false, &tools.Action{Description: in.description, LogsView: &view}
}

// Run is RunShown with the action dropped.
func (t *Tool) Run(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool) {
	text, isError, _ := t.RunShown(ctx, rt, raw)
	return text, isError
}

// refusal is an error as the model reads it: bad arguments by field and
// source, a source the mirror refuses by code, a cluster with no cache or one
// still syncing, and anything else with none of its text.
func refusal(err error) string {
	var ie *inputError
	var rf *refused
	switch {
	case errors.As(err, &ie):
		out := map[string]any{"error": "bad-input"}
		if ie.field != "" {
			out["field"] = ie.field
		}
		if ie.source >= 0 {
			out["source"] = ie.source
		}
		if ie.message != "" {
			out["message"] = ie.message
		}
		return result(out)
	case errors.As(err, &rf):
		out := map[string]any{"error": rf.code, "source": rf.source}
		if rf.containers != nil {
			out["containers"] = rf.containers
		}
		return result(out)
	case errors.Is(err, errNoCache):
		return `{"error":"no-cache"}`
	case errors.Is(err, errSyncing):
		return `{"error":"syncing"}`
	}
	return `{"error":"read-failed"}`
}

// result is v as one line of JSON, leaving <, > and & as they are.
func result(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // strings, string slices, ints and maps of them always encode
	return strings.TrimSuffix(b.String(), "\n")
}

// receipt is the one line the model reads of a view that opened: the first
// source and how many more, where it opened and whether it is pinned, then what
// each source shows beyond its name, each note under its source when there are
// several, then the grep. Every name in it is cluster data, redacted as a
// command's output is.
func receipt(typed []source, view tools.LogsViewAction, notes []note) string {
	var b strings.Builder
	b.WriteString("Viewing " + nameOf(typed[0]) + " in " + typed[0].namespace)
	if more := len(typed) - 1; more == 1 {
		b.WriteString(" and 1 more source")
	} else if more > 1 {
		b.WriteString(fmt.Sprintf(" and %d more sources", more))
	}
	b.WriteString(" " + anchorText(view.Anchor))
	if view.PinToEnd {
		b.WriteString(", pinned to the end")
	}
	b.WriteString(".")
	for i, n := range notes {
		for _, line := range noteLines(typed[i], view.Sources[i], n) {
			if len(typed) > 1 {
				line = nameOf(typed[i]) + ": " + line
			} else {
				line = strings.ToUpper(line[:1]) + line[1:]
			}
			b.WriteString(" " + line + ".")
		}
	}
	if view.Grep != "" {
		b.WriteString(" Matching /" + view.Grep + "/.")
	}
	return safe.Redact(b.String())
}

// nameOf is a source as kubectl names it, <resource>/<name>.
func nameOf(s source) string { return resourceOf[s.kind] + "/" + s.name }

// resourceOf is each kind's plural resource, as the receipt names a source.
var resourceOf = map[tools.LogsSourceKind]string{
	tools.LogsSourcePod: "pods", tools.LogsSourceDeployment: "deployments", tools.LogsSourceStatefulSet: "statefulsets",
	tools.LogsSourceDaemonSet: "daemonsets", tools.LogsSourceJob: "jobs", tools.LogsSourceCronJob: "cronjobs",
	tools.LogsSourceReplicaSet: "replicasets",
}

// anchorText is where the view opened.
func anchorText(a tools.LogsViewAnchor) string {
	switch a.Kind {
	case tools.LogsAnchorHead:
		return "from the start"
	case tools.LogsAnchorAt:
		return "from " + a.At.Format(time.RFC3339)
	}
	return "at the newest line"
}

// noteLines is what the receipt says of one source beyond its name: the
// container defaulted among several and the others, the containers named or
// every one, and the previous instance, of how many of a workload's pods.
func noteLines(typed source, src tools.LogsViewSource, n note) []string {
	var lines []string
	switch {
	case n.defaulted:
		lines = append(lines, "defaulted container "+src.Containers[0]+" out of "+strings.Join(n.containers, ", "))
	case typed.allContainers:
		lines = append(lines, "containers "+strings.Join(n.containers, ", "))
	case len(src.Containers) > 1:
		lines = append(lines, "containers "+strings.Join(src.Containers, ", "))
	case len(typed.containers) == 1:
		lines = append(lines, "container "+src.Containers[0])
	}
	switch {
	case !src.Previous:
	case typed.kind == tools.LogsSourcePod:
		lines = append(lines, "previous instance")
	default:
		lines = append(lines, fmt.Sprintf("previous instance of %d of %d pods", n.restarted, n.pods))
	}
	return lines
}
