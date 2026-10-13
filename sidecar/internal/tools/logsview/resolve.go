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

package logsview

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// The reading's bounds: a source's containers, and the statements logs.Check
// runs, each of which the service limits itself.
const (
	maxRows  = 10_000
	maxBytes = tools.FileLimit
)

// refused is a view the mirror refuses: the code the model reads, the source it
// concerns (-1 for the whole view), and for a container it does not have, the
// ones it does.
type refused struct {
	code       string
	source     int
	containers []string
}

func (e *refused) Error() string { return "logsview: refused: " + e.code }

// The codes a view is refused with.
const (
	codeNoContainer = "no-container"
	codeTooMany     = "too-many"
	codeNotMirrored = "not-mirrored"
)

// note is what the receipt says of one source beyond its name: what the mirror
// holds of it now, the containers it has, the default container the mirror shows
// now when it was picked among several, and for a workload with previous, how
// many of its pods have an instance to show.
type note struct {
	state      logs.SourceState
	containers []string
	defaulted  string
	pods       int
	restarted  int
}

// resolve is the view the call opens, over one reading of the cluster's active
// cache: each source checked against the mirror, its containers defaulted, its
// previous instance found, and the anchor on the tool's clock.
func (t *Tool) resolve(ctx context.Context, clusterID cluster.ClusterID, in input) (tools.LogsViewAction, []note, error) {
	anchor, err := anchorOf(in.anchor, t.now())
	if err != nil {
		return tools.LogsViewAction{}, nil, err
	}
	var view tools.LogsViewAction
	var notes []note
	err = t.svc.CachedData().ReadActive(ctx, clusterID, maxRows, maxBytes, func(q cluster.CacheReading, _ cluster.ActiveCluster) error {
		var err error
		view, notes, err = resolveIn(q, in)
		return err
	})
	if err != nil {
		return tools.LogsViewAction{}, nil, err
	}
	view.Anchor, view.PinToEnd = anchor, in.pinToEnd
	return view, notes, nil
}

// resolveIn is one reading's view, each source checked in turn, then the whole
// as the logs service resolves it, so a view the tool opens is one it reads. It
// builds the view afresh, since ReadActive may run it twice.
func resolveIn(q cluster.CacheReading, in input) (tools.LogsViewAction, []note, error) {
	view := tools.LogsViewAction{LogsViewInput: tools.LogsViewInput{Sources: []tools.LogsViewSource{}, Filters: in.filters, Grep: in.grep}}
	if view.Filters == nil {
		view.Filters = []tools.LogsViewFilter{}
	}
	var notes []note
	for i, s := range in.sources {
		src, n, err := resolveSource(q, i, s)
		if err != nil {
			return tools.LogsViewAction{}, nil, err
		}
		view.Sources = append(view.Sources, src)
		notes = append(notes, n)
	}
	states, err := logs.Check(q, view.LogsViewInput)
	if err != nil {
		return tools.LogsViewAction{}, nil, refusedBy(err)
	}
	for i, src := range view.Sources {
		notes[i].state = states[i]
		if src.Previous && src.Kind != tools.LogsSourcePod {
			count, err := logs.CountPods(q, src)
			if err != nil {
				return tools.LogsViewAction{}, nil, err
			}
			notes[i].pods, notes[i].restarted = count.Pods, count.Previous
		}
	}
	return view, notes, nil
}

// refusedBy is the logs service's refusal of a view as the model reads it.
func refusedBy(err error) error {
	var unmirrored logs.Unmirrored
	switch {
	case errors.As(err, &unmirrored):
		return &refused{code: codeNotMirrored, source: unmirrored.Source}
	case errors.Is(err, logs.ErrTooMany):
		return &refused{code: codeTooMany, source: -1}
	}
	return err
}

// resolveSource is one source checked against the mirror: each container named
// is one of its object's. With none named the backend reads each pod's default
// container, and the receipt names the one the object has now. An object the
// mirror does not hold yet checks nothing.
func resolveSource(q cluster.CacheReading, i int, s source) (tools.LogsViewSource, note, error) {
	f, err := object(q, s)
	if err != nil {
		return tools.LogsViewSource{}, note{}, err
	}
	src := tools.LogsViewSource{Namespace: s.namespace, Kind: s.kind, Name: s.name, Containers: []string{}, AllContainers: s.allContainers, Previous: s.previous}
	n := note{containers: f.names}
	switch {
	case s.allContainers:
	case len(s.containers) > 0:
		for _, c := range s.containers {
			if f.uid != "" && !slices.Contains(f.names, c) {
				return tools.LogsViewSource{}, note{}, &refused{code: codeNoContainer, source: i, containers: f.names}
			}
		}
		src.Containers = s.containers
	case len(f.names) > 1:
		n.defaulted = defaultContainer(f)
	}
	return src, n, nil
}

// defaultContainer is the one kubectl logs reads with none named: the one the
// annotation names when the object has it, else the first of its containers.
func defaultContainer(f found) string {
	if f.annotated != "" && slices.Contains(f.names, f.annotated) {
		return f.annotated
	}
	return f.names[0]
}

// anchorOf is the anchor the model typed on the clock: tail, head, an RFC 3339
// time, or a duration back from now.
func anchorOf(s string, now time.Time) (tools.LogsViewAnchor, error) {
	switch s {
	case "", "tail":
		return tools.LogsViewAnchor{Kind: tools.LogsAnchorTail}, nil
	case "head":
		return tools.LogsViewAnchor{Kind: tools.LogsAnchorHead}, nil
	}
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		at = at.UTC()
		return tools.LogsViewAnchor{Kind: tools.LogsAnchorAt, At: &at}, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		at := now.Add(-d).UTC()
		return tools.LogsViewAnchor{Kind: tools.LogsAnchorAt, At: &at}, nil
	}
	return tools.LogsViewAnchor{}, &inputError{field: "anchor", source: -1, message: anchorMessage}
}
