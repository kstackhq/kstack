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
	"encoding/json"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// query reads one cache's views, as KubeQuery does, inside one reading of the cluster.
type query struct {
	data      cluster.CachedData
	ctx       context.Context
	clusterID cluster.ClusterID
	cacheID   cluster.ClusterCacheID
}

// found is what the mirror holds of a source's object: its containers first and
// its init containers after them, the default-container annotation's value, and
// for a pod, each container that restarted.
type found struct {
	uid       string
	names     []string
	annotated string
	restarted map[string]bool
}

// apiOf is each kind's api_version and Kind in the mirror, and where its pod
// template is, "" for a pod's own spec.
var apiOf = map[tools.LogsSourceKind]struct{ apiVersion, kind, template string }{
	tools.LogsSourcePod:         {"v1", "Pod", ""},
	tools.LogsSourceDeployment:  {"apps/v1", "Deployment", "$.spec.template"},
	tools.LogsSourceStatefulSet: {"apps/v1", "StatefulSet", "$.spec.template"},
	tools.LogsSourceDaemonSet:   {"apps/v1", "DaemonSet", "$.spec.template"},
	tools.LogsSourceReplicaSet:  {"apps/v1", "ReplicaSet", "$.spec.template"},
	tools.LogsSourceJob:         {"batch/v1", "Job", "$.spec.template"},
	tools.LogsSourceCronJob:     {"batch/v1", "CronJob", "$.spec.jobTemplate.spec.template"},
}

// run runs one statement over the cache; a cache gone under the read is errNoCache.
func (q query) run(sql string) (cluster.ClusterCachedDataQueryResult, error) {
	res, ok, err := q.data.Query(q.ctx, q.clusterID, q.cacheID, sql, maxRows, maxBytes)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, errNoCache
	}
	return res, nil
}

// object is the source's object as the mirror holds it, a zero uid for none: a
// pod's containers off the containers table, with their restarts; a workload's
// off its pod template.
func (q query) object(s source) (found, error) {
	api := apiOf[s.kind]
	where := " WHERE o.api_version = " + literal(api.apiVersion) + " AND o.kind = " + literal(api.kind) +
		" AND o.namespace = " + literal(s.namespace) + " AND o.name = " + literal(s.name)
	if api.template == "" {
		res, err := q.run(`SELECT o.uid, o.body ->> '$.metadata.annotations."` + defaultContainerAnnotation + `"', c.name, coalesce(c.restarts, 0)
FROM objects o LEFT JOIN containers c ON c.pod_uid = o.uid` + where + ` ORDER BY c.init, c.position`)
		if err != nil || len(res.Rows) == 0 {
			return found{}, err
		}
		f := found{uid: text(res.Rows[0][0]), annotated: text(res.Rows[0][1]), restarted: map[string]bool{}}
		for _, row := range res.Rows {
			if name := text(row[2]); name != "" {
				f.names = append(f.names, name)
				f.restarted[name] = integer(row[3]) > 0
			}
		}
		return f, nil
	}
	res, err := q.run(`SELECT o.uid, o.body ->> '` + api.template + `.metadata.annotations."` + defaultContainerAnnotation + `"',
o.body -> '` + api.template + `.spec.containers', o.body -> '` + api.template + `.spec.initContainers'
FROM objects o` + where)
	if err != nil || len(res.Rows) == 0 {
		return found{}, err
	}
	f := found{uid: text(res.Rows[0][0]), annotated: text(res.Rows[0][1])}
	f.names = append(containerNames(text(res.Rows[0][2])), containerNames(text(res.Rows[0][3]))...)
	return f, nil
}

// previousPods counts the pods under the workload uid, and those with a
// restarted container among shown: every container when shown is empty.
func (q query) previousPods(uid string, shown []string) (pods, restarted int, err error) {
	among := ""
	if len(shown) > 0 {
		among = " AND c.name IN (" + literals(shown) + ")"
	}
	res, err := q.run(`SELECT count(*), coalesce(sum(EXISTS (SELECT 1 FROM containers c WHERE c.pod_uid = p.uid AND c.restarts > 0` + among + `)), 0)
FROM objects p WHERE p.api_version = 'v1' AND p.kind = 'Pod'
AND p.uid IN (SELECT uid FROM ancestors WHERE ancestor_uid = ` + literal(uid) + `)`)
	if err != nil || len(res.Rows) == 0 {
		return 0, 0, err
	}
	return int(integer(res.Rows[0][0])), int(integer(res.Rows[0][1])), nil
}

// containerNames is each container's name in a template's list, as JSON.
func containerNames(list string) []string {
	var containers []struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(list), &containers) // a body the mirror holds is valid JSON; anything else is no container
	var names []string
	for _, c := range containers {
		if c.Name != "" {
			names = append(names, c.Name)
		}
	}
	return names
}

// literal is s as a SQL string literal.
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// literals is each of list as a SQL string literal, comma-separated.
func literals(list []string) string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = literal(s)
	}
	return strings.Join(out, ", ")
}

// text is a cell as text, "" for any other.
func text(v any) string {
	s, _ := v.(string)
	return s
}

// integer is a cell as an integer, 0 for any other.
func integer(v any) int64 {
	n, _ := v.(int64)
	return n
}
