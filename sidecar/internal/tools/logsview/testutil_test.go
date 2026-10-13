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
	"maps"
	"testing"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustertest"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
)

// mirrorOf is a watching cluster whose statements run over a real store holding
// objects and mirroring pods, so a statement the store refuses fails the test.
func mirrorOf(t *testing.T, objects ...map[string]any) *fakeService {
	t.Helper()
	store := clustertest.NewStore(t)
	store.Mirror(t, "v1", "Pod")
	for _, o := range objects {
		store.Apply(t, o)
	}
	return over(t, store)
}

// over is a watching cluster whose statements run over store.
func over(t *testing.T, store *clustertest.Store) *fakeService {
	t.Helper()
	f := &fakeService{
		active: cluster.ActiveCluster{Cluster: &cluster.Cluster{}, Cache: &cluster.ClusterCache{RecordMeta: cluster.RecordMeta{ID: 11}}},
		health: cluster.ClusterCacheHealth{Reason: "Watching"},
	}
	f.store = store
	return f
}

// webappObjects is a deployment webapp in prod whose template runs app and
// istio-proxy after init container setup, its replica set, and three pods under
// it running the same: webapp-7f9c and webapp-a with app restarted, and webapp-b.
func webappObjects() []map[string]any {
	webappPod := func(name string, restarted bool) map[string]any {
		return podOf(name, "", webappReplicaSet(), container{name: "app", restarted: restarted}, container{name: "istio-proxy"}, container{name: "setup", init: true})
	}
	replicaSet := meta("rs-webapp", "webapp-7d9f", "")
	replicaSet["ownerReferences"] = []any{controller("apps/v1", "Deployment", "webapp", "dep-webapp")}
	return []map[string]any{
		{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta("dep-webapp", "webapp", ""), "spec": map[string]any{"template": template("")}},
		{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": replicaSet},
		webappPod("webapp-7f9c", true),
		webappPod("webapp-a", true),
		webappPod("webapp-b", false),
	}
}

// webappReplicaSet is the controller reference onto webapp's replica set.
func webappReplicaSet() map[string]any {
	return controller("apps/v1", "ReplicaSet", "webapp-7d9f", "rs-webapp")
}

// controller is an owner reference that controls its object.
func controller(apiVersion, kind, name, uid string) map[string]any {
	return map[string]any{"apiVersion": apiVersion, "kind": kind, "name": name, "uid": uid, "controller": true}
}

// meta is an object's metadata in prod, annotated with its default container
// when defaulted is not "".
func meta(uid, name, defaulted string) map[string]any {
	m := map[string]any{"uid": uid, "name": name, "namespace": "prod"}
	maps.Copy(m, annotated(defaulted))
	return m
}

// annotated is a metadata carrying kubectl's default-container annotation,
// empty when defaulted is "".
func annotated(defaulted string) map[string]any {
	if defaulted == "" {
		return map[string]any{}
	}
	return map[string]any{"annotations": map[string]any{logs.DefaultContainerAnnotation: defaulted}}
}

// template is a pod template with containers app and istio-proxy and init
// container setup, annotated with its default container when defaulted is not "".
func template(defaulted string) map[string]any {
	return map[string]any{
		"metadata": annotated(defaulted),
		"spec": map[string]any{
			"initContainers": []any{map[string]any{"name": "setup", "image": "setup:1"}},
			"containers":     []any{map[string]any{"name": "app", "image": "app:1"}, map[string]any{"name": "istio-proxy", "image": "envoy:1"}},
		},
	}
}

// container is one running container of a fixture pod: an init container when
// init is set, with a previous instance when restarted is.
type container struct {
	name      string
	init      bool
	restarted bool
}

// podOf is a Pod named name in prod running containers, owned by owner when it
// is not nil, carrying the default-container annotation when defaulted is not "".
func podOf(name, defaulted string, owner map[string]any, containers ...container) map[string]any {
	m := meta("uid-"+name, name, defaulted)
	if owner != nil {
		m["ownerReferences"] = []any{owner}
	}
	var specs, initSpecs, statuses, initStatuses []any
	for _, c := range containers {
		spec := map[string]any{"name": c.name, "image": c.name + ":1"}
		status := containerStatus(name, c)
		if c.init {
			initSpecs, initStatuses = append(initSpecs, spec), append(initStatuses, status)
		} else {
			specs, statuses = append(specs, spec), append(statuses, status)
		}
	}
	spec := map[string]any{"containers": specs}
	status := map[string]any{"containerStatuses": statuses}
	if len(initSpecs) > 0 {
		spec["initContainers"], status["initContainerStatuses"] = initSpecs, initStatuses
	}
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": m, "spec": spec, "status": status}
}

// containerStatus is c's status in the pod named pod: running, and with the id
// of the instance before when it restarted.
func containerStatus(pod string, c container) map[string]any {
	id := "c://" + pod + "-" + c.name
	status := map[string]any{"name": c.name, "restartCount": int64(0), "containerID": id, "state": map[string]any{"running": map[string]any{}}}
	if c.restarted {
		status["restartCount"] = int64(1)
		status["lastState"] = map[string]any{"terminated": map[string]any{"containerID": id + "-old"}}
	}
	return status
}
