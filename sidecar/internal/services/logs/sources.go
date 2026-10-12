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
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/mirrorsql"
)

// The mirror reads' bounds: a view's pods and containers, and the nodes.
const (
	maxRows  = 10_000
	maxBytes = 8 << 20
)

// maxStreams is the most containers one view reads at once: each is its own
// request to the API server, held open for as long as a follow runs.
const maxStreams = 100

// Refusal is a read the service declines, in words the user can act on.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

var (
	// ErrNoCache is a cluster with no cache to resolve a view against.
	ErrNoCache = &Refusal{"this cluster has no cache yet"}
	// ErrSyncing is a cache whose verdict withholds its rows: a pod missing from it
	// may still exist.
	ErrSyncing = &Refusal{"this cluster's cache is still syncing"}
	// errCut is a mirror answer cut at its bounds. Asking again does not shrink it.
	errCut = &Refusal{"this view names more containers or nodes than Kstack reads at once"}
)

// mirrorKinds is each source kind's api_version and Kind in the mirror.
var mirrorKinds = map[SourceKind]struct{ apiVersion, kind string }{
	SourcePod:         {"v1", "Pod"},
	SourceDeployment:  {"apps/v1", "Deployment"},
	SourceStatefulSet: {"apps/v1", "StatefulSet"},
	SourceDaemonSet:   {"apps/v1", "DaemonSet"},
	SourceReplicaSet:  {"apps/v1", "ReplicaSet"},
	SourceJob:         {"batch/v1", "Job"},
	SourceCronJob:     {"batch/v1", "CronJob"},
}

// MirrorKind is kind's api_version and Kind in the mirror.
func MirrorKind(kind SourceKind) (apiVersion, k string) {
	m := mirrorKinds[kind]
	return m.apiVersion, m.kind
}

// The well-known labels a node's place is read from.
const (
	labelRegion = "topology.kubernetes.io/region"
	labelZone   = "topology.kubernetes.io/zone"
	labelOS     = "kubernetes.io/os"
	labelArch   = "kubernetes.io/arch"
)

// target is what a view reads on one cluster: the server the mirror's pods came
// from, and one stream per container, in the order the sources were asked.
type target struct {
	cacheID   cluster.ClusterCacheID
	serverUID string
	streams   []stream
}

// stream is one container a view reads: its source, and the generation of the
// log behind it.
type stream struct {
	src RecordSource
	gen generation
}

// generation is which log a container's path serves: the pod's UID, since a pod
// replaced under the same name has a new one, the container's restarts, since a
// restart starts a new log, and its state, since a container that was waiting has
// one once it runs. A follow reopens a stream whose generation moved.
type generation struct {
	uid      string
	restarts int64
	state    string
}

// resolve is view over one reading of cluster id's active cache: each source's
// pods and containers, the filters applied, and the server UID the pods came from.
// A cache missing or withholding its rows is refused as the LogsView tool refuses
// it.
func resolve(ctx context.Context, svc cluster.Service, id cluster.ClusterID, view View) (target, error) {
	var t target
	err := mirrorsql.Read(ctx, svc, id, maxRows, maxBytes, func(q mirrorsql.Query, active cluster.ActiveCluster) error {
		var err error
		t, err = resolveIn(query{q}, active, view)
		return err
	})
	switch {
	case errors.Is(err, mirrorsql.ErrNoCache):
		return target{}, ErrNoCache
	case errors.Is(err, mirrorsql.ErrSyncing):
		return target{}, ErrSyncing
	case err != nil:
		return target{}, err
	}
	return t, nil
}

// holdsPod is whether a stream of t reads the pod "<ns>/<name>" whose UID is uid.
func (t target) holdsPod(key, uid string) bool {
	for _, st := range t.streams {
		if st.src.Namespace+"/"+st.src.Pod == key && st.gen.uid == uid {
			return true
		}
	}
	return false
}

// tooMany refuses a target of more than maxStreams containers.
func (t target) tooMany() error {
	if len(t.streams) <= maxStreams {
		return nil
	}
	return &Refusal{fmt.Sprintf("this view reads %d containers and Kstack reads at most %d at once; filter it by node, or name its containers", len(t.streams), maxStreams)}
}

// resolveIn is one reading's target. It builds the target afresh, since
// mirrorsql.Read may run it twice.
func resolveIn(q query, active cluster.ActiveCluster, view View) (target, error) {
	if err := q.nodesListed(view.Filters); err != nil {
		return target{}, err
	}
	t := target{cacheID: active.Cache.ID, serverUID: active.Cache.Spec.ServerUID}
	seen := map[RecordSource]bool{}
	for _, src := range view.Sources {
		streams, err := q.streams(src, view.Filters)
		if err != nil {
			return target{}, err
		}
		for _, s := range streams {
			if !seen[s.src] {
				seen[s.src] = true
				t.streams = append(t.streams, s)
			}
		}
	}
	if len(t.streams) > maxStreams {
		// tooMany refuses it, so its nodes are not read.
		return t, nil
	}
	return t, q.place(t.streams)
}

// query reads one cache's views inside one reading of the cluster.
type query struct{ mirrorsql.Query }

// run is sql's rows, refusing an answer cut at its bounds: it would drop sources,
// or nodes a filter needs, without a word.
func (q query) run(sql string) ([][]any, error) {
	res, err := q.Run(sql)
	if err != nil {
		return nil, err
	}
	if res.More {
		return nil, errCut
	}
	return res.Rows, nil
}

// nodesListed refuses a filter on a label over a mirror with no nodes: the
// credentials could not list them, and an empty answer would read as no lines.
func (q query) nodesListed(filters []ViewFilter) error {
	byLabel := ""
	for _, f := range filters {
		if f.Field != FilterNode {
			byLabel = string(f.Field)
		}
	}
	if byLabel == "" {
		return nil
	}
	rows, err := q.run(`SELECT n.name FROM objects n WHERE n.api_version = 'v1' AND n.kind = 'Node' LIMIT 1`)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return &Refusal{"Kstack cannot read this cluster's nodes, so it cannot filter by " + byLabel}
	}
	return nil
}

// place sets each stream's node labels, which every line carries, reading only the
// nodes the streams run on.
func (q query) place(streams []stream) error {
	var names []string
	for _, s := range streams {
		if s.src.Node != "" && !slices.Contains(names, s.src.Node) {
			names = append(names, s.src.Node)
		}
	}
	if len(names) == 0 {
		return nil
	}
	rows, err := q.run(`SELECT n.name,
(SELECT value FROM labels l WHERE l.uid = n.uid AND l.key = ` + mirrorsql.Literal(labelRegion) + `),
(SELECT value FROM labels l WHERE l.uid = n.uid AND l.key = ` + mirrorsql.Literal(labelZone) + `),
(SELECT value FROM labels l WHERE l.uid = n.uid AND l.key = ` + mirrorsql.Literal(labelOS) + `),
(SELECT value FROM labels l WHERE l.uid = n.uid AND l.key = ` + mirrorsql.Literal(labelArch) + `)
FROM objects n WHERE n.api_version = 'v1' AND n.kind = 'Node' AND n.name IN (` + mirrorsql.Literals(names) + `)`)
	if err != nil {
		return err
	}
	nodes := map[string]RecordSource{}
	for _, row := range rows {
		nodes[mirrorsql.Text(row[0])] = RecordSource{Region: mirrorsql.Text(row[1]), Zone: mirrorsql.Text(row[2]), OS: mirrorsql.Text(row[3]), Arch: mirrorsql.Text(row[4])}
	}
	for i := range streams {
		n := nodes[streams[i].src.Node]
		streams[i].src.Region, streams[i].src.Zone, streams[i].src.OS, streams[i].src.Arch = n.Region, n.Zone, n.OS, n.Arch
	}
	return nil
}

// streams is one stream per container of each of src's pods: the containers src
// names that the pod has, or every one, each with the node the pod runs on and the
// generation of its log. The containers and the filters narrow the statement, so
// the row cap counts only what the view reads, and nothing else applies them. A
// workload's pods are walked down to from it, so the walk reads only its own
// owner references.
func (q query) streams(src ViewSource, filters []ViewFilter) ([]stream, error) {
	where := "p.namespace = " + mirrorsql.Literal(src.Namespace) + " AND p.name = " + mirrorsql.Literal(src.Name)
	if w := mirrorKinds[src.Kind]; src.Kind != SourcePod {
		// Depth 8 bounds the walk, as the ancestors view's does, so an owner cycle ends.
		where = "p.namespace = " + mirrorsql.Literal(src.Namespace) + ` AND p.uid IN (WITH RECURSIVE down(uid, depth) AS (
SELECT o.uid, 0 FROM objects o WHERE o.api_version = ` + mirrorsql.Literal(w.apiVersion) + " AND o.kind = " + mirrorsql.Literal(w.kind) +
			" AND o.namespace = " + mirrorsql.Literal(src.Namespace) + " AND o.name = " + mirrorsql.Literal(src.Name) + `
UNION ALL
SELECT r.uid, down.depth + 1 FROM down JOIN owners r ON r.owner_uid = down.uid WHERE down.depth < 8
) SELECT uid FROM down)`
	}
	if len(src.Containers) > 0 {
		where += " AND c.name IN (" + mirrorsql.Literals(src.Containers) + ")"
	}
	for _, f := range filters {
		where += " AND " + filterWhere(f)
	}
	rows, err := q.run(`SELECT p.name, p.node, c.name, p.uid, c.restarts, c.state FROM objects p JOIN containers c ON c.pod_uid = p.uid
WHERE p.api_version = 'v1' AND p.kind = 'Pod' AND ` + where + ` ORDER BY p.name, c.init, c.position`)
	if err != nil {
		return nil, err
	}
	out := make([]stream, 0, len(rows))
	for _, row := range rows {
		s := RecordSource{Namespace: src.Namespace, Pod: mirrorsql.Text(row[0]), Container: mirrorsql.Text(row[2]), Node: mirrorsql.Text(row[1]), Previous: src.Previous}
		out = append(out, stream{s, generation{mirrorsql.Text(row[3]), mirrorsql.Integer(row[4]), mirrorsql.Text(row[5])}})
	}
	return out, nil
}

// filterLabels is the node label each filter field other than the node reads.
var filterLabels = map[FilterField]string{
	FilterRegion: labelRegion,
	FilterZone:   labelZone,
	FilterOS:     labelOS,
	FilterArch:   labelArch,
}

// filterWhere is f as a condition on the pod p: its node among f's values, or a
// node the mirror holds whose label f reads holds one of them, which leaves out a
// pod whose node the mirror lacks.
func filterWhere(f ViewFilter) string {
	if f.Field == FilterNode {
		return "p.node IN (" + mirrorsql.Literals(f.Values) + ")"
	}
	return `p.node IN (SELECT n.name FROM objects n JOIN labels l ON l.uid = n.uid
WHERE n.api_version = 'v1' AND n.kind = 'Node' AND l.key = ` + mirrorsql.Literal(filterLabels[f.Field]) + " AND l.value IN (" + mirrorsql.Literals(f.Values) + "))"
}
