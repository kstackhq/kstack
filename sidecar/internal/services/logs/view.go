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

// Package logs serves a view's log lines: it resolves the view's sources against
// the mirror, reads each container's log through the API server with the user's
// credentials, merges the lines by time, and strips terminal control sequences
// before anything leaves it.
package logs

// View is what a view reads: the Sources whose lines are merged by timestamp, in
// the order asked; the Filters on those lines, none keeping every line; and the
// Grep, a regular expression each line's message must match, empty for none. The
// LogsView tool's action carries it, so the tool and the backend share one shape.
type View struct {
	Sources []ViewSource `json:"sources"`
	Filters []ViewFilter `json:"filters"`
	Grep    string       `json:"grep"`
}

// ViewSource is one resource a view reads, in its Namespace, by Kind and Name: its
// Containers by exact name, every one when empty, and Previous for the instance
// before the last restart, which is finished.
type ViewSource struct {
	Namespace  string     `json:"namespace"`
	Kind       SourceKind `json:"kind"`
	Name       string     `json:"name"`
	Containers []string   `json:"containers"`
	Previous   bool       `json:"previous"`
}

// SourceKind is a kind the backend reads: a pod, or a workload whose pods it
// follows. The GraphQL LogsViewSourceKind enum binds member by member onto it.
type SourceKind string

const (
	SourcePod         SourceKind = "pod"
	SourceDeployment  SourceKind = "deployment"
	SourceStatefulSet SourceKind = "statefulset"
	SourceDaemonSet   SourceKind = "daemonset"
	SourceJob         SourceKind = "job"
	SourceCronJob     SourceKind = "cronjob"
	SourceReplicaSet  SourceKind = "replicaset"
)

// ViewFilter is one filter on a view's lines: the Values its Field may take, never
// empty.
type ViewFilter struct {
	Field  FilterField `json:"field"`
	Values []string    `json:"values"`
}

// FilterField is what a line can be filtered by: the node it ran on, and the node's
// labels. The GraphQL LogsViewFilterField enum binds member by member onto it.
type FilterField string

const (
	FilterNode   FilterField = "node"
	FilterRegion FilterField = "region"
	FilterZone   FilterField = "zone"
	FilterOS     FilterField = "os"
	FilterArch   FilterField = "arch"
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
