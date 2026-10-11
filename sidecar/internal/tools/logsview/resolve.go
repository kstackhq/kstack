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
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustercard"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// defaultContainerAnnotation is kubectl's: the container a pod's author made the
// default, on the pod or on a workload's pod template.
const defaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

// The mirror reads' bounds: a source's containers, and the pods under a workload.
const (
	maxRows  = 10_000
	maxBytes = tools.FileLimit
)

var (
	// errNoCache is a cluster with no cache to check against: its record gone,
	// its identity moving under both readings, or its cache gone under the reads.
	errNoCache = errors.New("logsview: the cluster has no cache")
	// errSyncing is a cache whose verdict withholds its rows: a source missing
	// from it may still exist.
	errSyncing = errors.New("logsview: the cache is syncing")
)

// refused is a source the mirror refuses: the code the model reads, the source
// it concerns, and for a container it does not have, the ones it does.
type refused struct {
	code       string
	source     int
	containers []string
}

func (e *refused) Error() string { return "logsview: source refused: " + e.code }

// The codes a source is refused with.
const (
	codeNotFound    = "not-found"
	codeNoContainer = "no-container"
	codeNoPrevious  = "no-previous"
)

// note is what the receipt says of one source beyond its name: the containers
// it has, whether the one shown was defaulted among several, and for a
// workload with previous, how many of its pods have an instance to show.
type note struct {
	containers []string
	defaulted  bool
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
	err = t.svc.Clusters().ReadActive(ctx, clusterID, func(ctx context.Context, active cluster.ActiveCluster) error {
		var err error
		view, notes, err = t.resolveIn(ctx, clusterID, active, in)
		return err
	})
	if errors.Is(err, cluster.ErrNotFound) || errors.Is(err, cluster.ErrIdentityMoved) {
		return tools.LogsViewAction{}, nil, errNoCache
	}
	if err != nil {
		return tools.LogsViewAction{}, nil, err
	}
	view.Anchor, view.PinToEnd = anchor, in.pinToEnd
	return view, notes, nil
}

// resolveIn is one reading's view: the cache's verdict, then each source. It
// builds the view afresh, since ReadActive may run it twice.
func (t *Tool) resolveIn(ctx context.Context, clusterID cluster.ClusterID, active cluster.ActiveCluster, in input) (tools.LogsViewAction, []note, error) {
	if active.Cache == nil {
		return tools.LogsViewAction{}, nil, errNoCache
	}
	health, ok, err := t.svc.Caches().Health(ctx, clusterID, active.Cache.ID)
	if err != nil {
		return tools.LogsViewAction{}, nil, err
	}
	if !ok {
		return tools.LogsViewAction{}, nil, errNoCache
	}
	if clustercard.Freshness(&health).Withholds() {
		return tools.LogsViewAction{}, nil, errSyncing
	}
	q := query{t.svc.CachedData(), ctx, clusterID, active.Cache.ID}
	view := tools.LogsViewAction{Sources: []tools.LogsViewSource{}, Filters: in.filters, Grep: in.grep}
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
	return view, notes, nil
}

// resolveSource is one source checked against the mirror: the object is there,
// each container named is one of its, else the default one is resolved, and a
// previous instance exists where asked.
func resolveSource(q query, i int, s source) (tools.LogsViewSource, note, error) {
	f, err := q.object(s)
	if err != nil {
		return tools.LogsViewSource{}, note{}, err
	}
	if f.uid == "" {
		return tools.LogsViewSource{}, note{}, &refused{code: codeNotFound, source: i}
	}
	src := tools.LogsViewSource{Namespace: s.namespace, Kind: s.kind, Name: s.name, Containers: []string{}, Previous: s.previous}
	n := note{containers: f.names}
	switch {
	case s.allContainers:
	case len(s.containers) > 0:
		for _, c := range s.containers {
			if !slices.Contains(f.names, c) {
				return tools.LogsViewSource{}, note{}, &refused{code: codeNoContainer, source: i, containers: f.names}
			}
		}
		src.Containers = s.containers
	case len(f.names) > 0:
		src.Containers = []string{defaultContainer(f)}
		n.defaulted = len(f.names) > 1
	}
	if !s.previous {
		return src, n, nil
	}
	if s.kind == tools.LogsSourcePod {
		if !hasPrevious(src.Containers, f.restarted) {
			return tools.LogsViewSource{}, note{}, &refused{code: codeNoPrevious, source: i}
		}
		return src, n, nil
	}
	n.pods, n.restarted, err = q.previousPods(f.uid, src.Containers)
	return src, n, err
}

// defaultContainer is the one kubectl logs reads with none named: the one the
// annotation names when the pod has it, else the first of its containers.
func defaultContainer(f found) string {
	if f.annotated != "" && slices.Contains(f.names, f.annotated) {
		return f.annotated
	}
	return f.names[0]
}

// hasPrevious is whether any container shown restarted: with none named, every one.
func hasPrevious(shown []string, restarted map[string]bool) bool {
	for name, ok := range restarted {
		if ok && (len(shown) == 0 || slices.Contains(shown, name)) {
			return true
		}
	}
	return false
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
