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

package logs

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// maxStreams is the most containers one view reads at once: each is its own
// request to the API server, held open for as long as a follow runs.
const maxStreams = 100

// The mirror reads' bounds. No statement reads more than maxStreams plus one rows,
// each a few names long, so neither bound cuts an answer the statement allows.
const (
	maxRows  = maxStreams + 1
	maxBytes = 1 << 20
)

// Refusal is a read the service declines, in words the user can act on. It is a
// value, so a caller holding one cannot change the sentinels below.
type Refusal struct{ Reason string }

func (r Refusal) Error() string { return r.Reason }

var (
	// ErrNoCache is a cluster with no cache to resolve a view against.
	ErrNoCache = Refusal{"this cluster has no cache yet"}
	// ErrSyncing is a cache whose verdict withholds its rows: a pod missing from it
	// may still exist.
	ErrSyncing = Refusal{"this cluster's cache is still syncing"}
	// ErrTooMany is a view of more than maxStreams containers.
	ErrTooMany = Refusal{fmt.Sprintf("this view reads more than %d containers, the most Kstack reads at once; narrow it to fewer sources, containers or pods", maxStreams)}
	// errCut is a mirror answer cut at its bounds. Asking again does not shrink it.
	errCut = Refusal{"the cluster's cache answered more than Kstack reads at once"}
)

// Unmirrored is a view whose sources could never appear: the cache does not
// mirror the kind of the source at index Source, or pods at all when Source is -1.
type Unmirrored struct {
	Refusal
	Source int
}

// SourceState is what the mirror holds of a source now. A view follows each
// source as its object and pods come and go, so no state refuses it.
type SourceState int

const (
	// Reading is a source with a stream to read.
	Reading SourceState = iota
	// Missing is a source whose object the mirror does not hold yet.
	Missing
	// Idle is a source whose object the mirror holds with nothing to read yet: no
	// pods, none with a log of a container named, or none of those with a previous
	// instance.
	Idle
)

// target is what a view reads on one cluster: the server the mirror's pods came
// from, one stream per container, in the order the sources were asked, and each
// source's state.
type target struct {
	cacheID   cluster.ClusterCacheID
	serverUID string
	streams   []stream
	states    []SourceState
}

// stream is one container a view reads: the log it names, and where its pod runs.
type stream struct {
	key   StreamKey
	place Place
}

// resolve reads view over one reading of cluster id's active cache. A cache
// missing or withholding its rows is refused as the LogsView tool refuses it, and
// so is a kind the cache does not mirror and a view of more than maxStreams
// containers.
func resolve(ctx context.Context, svc cluster.Service, id cluster.ClusterID, view View) (target, error) {
	var t target
	err := svc.CachedData().ReadActive(ctx, id, maxRows, maxBytes, func(q cluster.CacheReading, active cluster.ActiveCluster) error {
		streams, states, err := reading{q}.streamsOf(view)
		if err != nil {
			return err
		}
		// Set whole, never appended to: ReadActive may run this twice.
		t = target{cacheID: active.Cache.ID, serverUID: active.Cache.Spec.ServerUID, streams: streams, states: states}
		return nil
	})
	switch {
	case errors.Is(err, cluster.ErrNoCache):
		return target{}, ErrNoCache
	case errors.Is(err, cluster.ErrSyncing):
		return target{}, ErrSyncing
	case err != nil:
		return target{}, err
	}
	return t, nil
}

// Check is each source's state as resolve would read view over q, refusing the
// view as resolve would. The LogsView tool checks through it, so a view it opens
// is one resolve reads.
func Check(q cluster.CacheReading, view View) ([]SourceState, error) {
	_, states, err := reading{q}.streamsOf(view)
	return states, err
}

// reading is one reading of a cache, queried through its views.
type reading struct{ cluster.CacheReading }

// rows is sql's rows with args bound to its placeholders, refusing an answer cut
// at its bounds: read as every row, it would drop sources without a word.
func (r reading) rows(sql string, args ...any) ([][]any, error) {
	res, err := r.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	if res.More {
		return nil, errCut
	}
	return res.Rows, nil
}

// streamsOf is every stream of view's sources, each once, in the order asked,
// and each source's state.
func (r reading) streamsOf(view View) ([]stream, []SourceState, error) {
	mirrored, err := r.mirrors(mirrorKinds[SourcePod])
	if err != nil {
		return nil, nil, err
	}
	if !mirrored {
		return nil, nil, Unmirrored{Refusal: Refusal{"Kstack's cache of this cluster holds no pods, so it reads no logs"}, Source: -1}
	}
	var streams []stream
	var states []SourceState
	seen := map[StreamKey]bool{}
	for i, src := range view.Sources {
		found, state, err := r.streams(i, src)
		if err != nil {
			return nil, nil, err
		}
		states = append(states, state)
		for _, s := range found {
			if !seen[s.key] {
				seen[s.key] = true
				streams = append(streams, s)
			}
		}
		if len(streams) > maxStreams {
			return nil, nil, ErrTooMany
		}
	}
	return streams, states, nil
}

// mirrors is whether the cache mirrors kind: the kinds view lists what its sync
// serves, with or without an object of it.
func (r reading) mirrors(kind mirrorKind) (bool, error) {
	rows, err := r.rows(`SELECT 1 FROM kinds WHERE api_version = ? AND kind = ?`, kind.apiVersion, kind.kind)
	return len(rows) > 0, err
}

// streams is one stream per container of each of src's pods, the source at
// index i, and src's state. A kind the cache does not mirror is refused, since no
// object of it could ever appear to follow. The statement reads at most one row
// past maxStreams, which is enough to refuse the view.
func (r reading) streams(i int, src ViewSource) ([]stream, SourceState, error) {
	kind, err := kindOf(src)
	if err != nil {
		return nil, 0, err
	}
	mirrored, err := r.mirrors(kind)
	if err != nil {
		return nil, 0, err
	}
	if !mirrored {
		return nil, 0, Unmirrored{Refusal: Refusal{fmt.Sprintf("Kstack's cache of this cluster holds no %s, so it cannot follow %s/%s in %s", kind.resource, kind.resource, src.Name, src.Namespace)}, Source: i}
	}
	sql, args := podsStatement(src, kind)
	rows, err := r.rows(sql, args...)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		state, err := r.idleOrMissing(src, kind)
		return nil, state, err
	}
	streams := make([]stream, 0, len(rows))
	for _, row := range rows {
		streams = append(streams, stream{
			key:   StreamKey{Namespace: src.Namespace, Pod: cluster.CellText(row[0]), Container: cluster.CellText(row[1]), Previous: src.Previous},
			place: Place{Node: cluster.CellText(row[2])},
		})
	}
	return streams, Reading, nil
}

// idleOrMissing is the state of src, which reads no stream: Missing when the
// mirror does not hold its object, else Idle.
func (r reading) idleOrMissing(src ViewSource, kind mirrorKind) (SourceState, error) {
	rows, err := r.rows(`SELECT 1 FROM objects WHERE api_version = ? AND kind = ? AND namespace = ? AND name = ?`, kind.apiVersion, kind.kind, src.Namespace, src.Name)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return Missing, nil
	}
	return Idle, nil
}

// podsStatement is the statement of src's pods' containers, each row the pod's
// name, the container's name and the pod's node, init containers first, as they
// run and as kubectl lists them. The containers src names, and having a log the
// kubelet serves — a previous instance when src asks for one, else a running or
// finished instance, the last one standing in while the container waits, as
// kubectl logs reads it — narrow it, so the row cap counts only what the view
// reads.
func podsStatement(src ViewSource, kind mirrorKind) (string, []any) {
	pods, args := podsOf(src, kind)
	containers, containerArgs := containersOf(src)
	where := pods + containers
	args = append(args, containerArgs...)
	if src.Previous {
		where += " AND c.last_container_id <> ''"
	} else {
		where += " AND (c.container_id <> '' OR c.last_container_id <> '')"
	}
	return `SELECT p.name, c.name, p.node FROM objects p JOIN containers c ON c.pod_uid = p.uid
WHERE p.api_version = 'v1' AND p.kind = 'Pod' AND ` + where + fmt.Sprintf(` ORDER BY p.name, c.init DESC, c.position LIMIT %d`, maxStreams+1), args
}

// podsOf is the condition on the pod p that it is one of src's pods, with the
// args it binds: the pod itself, or the pods walked down from a workload through
// controller references alone, so the walk reads only the objects it controls.
// Depth 8 bounds the walk, as the ancestors view's does, so an owner cycle ends.
func podsOf(src ViewSource, kind mirrorKind) (string, []any) {
	if src.Kind == SourcePod {
		return "p.namespace = ? AND p.name = ?", []any{src.Namespace, src.Name}
	}
	return `p.namespace = ? AND p.uid IN (WITH RECURSIVE down(uid, depth) AS (
SELECT o.uid, 0 FROM objects o WHERE o.api_version = ? AND o.kind = ? AND o.namespace = ? AND o.name = ?
UNION ALL
SELECT r.uid, down.depth + 1 FROM down JOIN owners r ON r.owner_uid = down.uid WHERE r.controller = 1 AND down.depth < 8
) SELECT uid FROM down)`, []any{src.Namespace, kind.apiVersion, kind.kind, src.Namespace, src.Name}
}

// containersOf is the condition on the container c of the pod p that src reads
// it, with the args it binds: one it names; with none named, any when it asks
// for every one, else p's default container, picked per pod as kubectl logs
// picks it: the one the annotation names when p has it, else p's first.
func containersOf(src ViewSource) (string, []any) {
	switch {
	case len(src.Containers) > 0:
		return " AND c.name IN (" + cluster.Placeholders(len(src.Containers)) + ")", asArgs(src.Containers)
	case src.AllContainers:
		return "", nil
	}
	return ` AND c.name = coalesce(
(SELECT d.name FROM containers d WHERE d.pod_uid = p.uid AND d.name = p.body ->> '$.metadata.annotations."` + DefaultContainerAnnotation + `"'),
(SELECT d.name FROM containers d WHERE d.pod_uid = p.uid AND d.init = 0 ORDER BY d.position LIMIT 1))`, nil
}

// kindOf is src's kind in the mirror, refusing one outside the enum.
func kindOf(src ViewSource) (mirrorKind, error) {
	kind, ok := mirrorKinds[src.Kind]
	if !ok {
		return mirrorKind{}, Refusal{fmt.Sprintf("Kstack cannot read the logs of a %q", src.Kind)}
	}
	return kind, nil
}

// PodCount is a workload source's pods, and those with a previous instance of a
// container it reads.
type PodCount struct {
	Pods     int
	Previous int
}

// CountPods counts src's pods as resolve walks to them, for the LogsView tool's
// receipt.
func CountPods(q cluster.CacheReading, src ViewSource) (PodCount, error) {
	kind, err := kindOf(src)
	if err != nil {
		return PodCount{}, err
	}
	containers, containerArgs := containersOf(src)
	pods, podArgs := podsOf(src, kind)
	rows, err := reading{q}.rows(`SELECT count(*),
coalesce(sum(EXISTS (SELECT 1 FROM containers c WHERE c.pod_uid = p.uid AND c.last_container_id <> ''`+containers+`)), 0)
FROM objects p WHERE p.api_version = 'v1' AND p.kind = 'Pod' AND `+pods, slices.Concat(containerArgs, podArgs)...)
	if err != nil {
		return PodCount{}, err
	}
	return PodCount{Pods: int(cluster.CellInteger(rows[0][0])), Previous: int(cluster.CellInteger(rows[0][1]))}, nil
}

// asArgs is list as a statement's args.
func asArgs(list []string) []any {
	args := make([]any, len(list))
	for i, s := range list {
		args[i] = s
	}
	return args
}
