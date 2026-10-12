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
	"encoding/json"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/mirrorsql"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// found is what the mirror holds of a source's object: its containers first and
// its init containers after them, the default-container annotation's value, and
// for a pod, each container that restarted.
type found struct {
	uid       string
	names     []string
	annotated string
	restarted map[string]bool
}

// templateOf is where kind's pod template is in its body, "" for a pod's own spec.
func templateOf(kind tools.LogsSourceKind) string {
	switch kind {
	case tools.LogsSourcePod:
		return ""
	case tools.LogsSourceCronJob:
		return "$.spec.jobTemplate.spec.template"
	}
	return "$.spec.template"
}

// object is the source's object as the mirror holds it, a zero uid for none: a
// pod's containers off the containers table, with their restarts; a workload's
// off its pod template.
func object(q mirrorsql.Query, s source) (found, error) {
	apiVersion, kind := logs.MirrorKind(s.kind)
	template := templateOf(s.kind)
	where := " WHERE o.api_version = " + mirrorsql.Literal(apiVersion) + " AND o.kind = " + mirrorsql.Literal(kind) +
		" AND o.namespace = " + mirrorsql.Literal(s.namespace) + " AND o.name = " + mirrorsql.Literal(s.name)
	if template == "" {
		res, err := q.Run(`SELECT o.uid, o.body ->> '$.metadata.annotations."` + defaultContainerAnnotation + `"', c.name, coalesce(c.restarts, 0)
FROM objects o LEFT JOIN containers c ON c.pod_uid = o.uid` + where + ` ORDER BY c.init, c.position`)
		if err != nil || len(res.Rows) == 0 {
			return found{}, err
		}
		f := found{uid: mirrorsql.Text(res.Rows[0][0]), annotated: mirrorsql.Text(res.Rows[0][1]), restarted: map[string]bool{}}
		for _, row := range res.Rows {
			if name := mirrorsql.Text(row[2]); name != "" {
				f.names = append(f.names, name)
				f.restarted[name] = mirrorsql.Integer(row[3]) > 0
			}
		}
		return f, nil
	}
	res, err := q.Run(`SELECT o.uid, o.body ->> '` + template + `.metadata.annotations."` + defaultContainerAnnotation + `"',
o.body -> '` + template + `.spec.containers', o.body -> '` + template + `.spec.initContainers'
FROM objects o` + where)
	if err != nil || len(res.Rows) == 0 {
		return found{}, err
	}
	f := found{uid: mirrorsql.Text(res.Rows[0][0]), annotated: mirrorsql.Text(res.Rows[0][1])}
	f.names = append(containerNames(mirrorsql.Text(res.Rows[0][2])), containerNames(mirrorsql.Text(res.Rows[0][3]))...)
	return f, nil
}

// previousPods counts the pods under the workload uid, and those with a
// restarted container among shown: every container when shown is empty.
func previousPods(q mirrorsql.Query, uid string, shown []string) (pods, restarted int, err error) {
	among := ""
	if len(shown) > 0 {
		among = " AND c.name IN (" + mirrorsql.Literals(shown) + ")"
	}
	res, err := q.Run(`SELECT count(*), coalesce(sum(EXISTS (SELECT 1 FROM containers c WHERE c.pod_uid = p.uid AND c.restarts > 0` + among + `)), 0)
FROM objects p WHERE p.api_version = 'v1' AND p.kind = 'Pod'
AND p.uid IN (SELECT uid FROM ancestors WHERE ancestor_uid = ` + mirrorsql.Literal(uid) + `)`)
	if err != nil || len(res.Rows) == 0 {
		return 0, 0, err
	}
	return int(mirrorsql.Integer(res.Rows[0][0])), int(mirrorsql.Integer(res.Rows[0][1])), nil
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
