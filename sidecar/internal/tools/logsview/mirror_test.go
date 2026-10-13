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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// objectOf is what object reads of src over f.
func objectOf(t *testing.T, f *fakeService, src source) found {
	t.Helper()
	var got found
	require.NoError(t, f.CachedData().ReadActive(t.Context(), "7", maxRows, maxBytes, func(q cluster.CacheReading, _ cluster.ActiveCluster) error {
		var err error
		got, err = object(q, src)
		return err
	}))
	return got
}

// A pod's containers come off the containers table, its own before its init
// containers.
func TestAPodIsReadOffTheContainersTable(t *testing.T) {
	f := mirrorOf(t, podOf("web-1", "istio-proxy", nil, container{name: "app", restarted: true}, container{name: "istio-proxy"}, container{name: "setup", init: true}))

	got := objectOf(t, f, source{namespace: "prod", kind: tools.LogsSourcePod, name: "web-1"})

	assert.Equal(t, found{uid: "uid-web-1", names: []string{"app", "istio-proxy", "setup"}, annotated: "istio-proxy"}, got)
}

// A workload's containers come off its pod template, and a CronJob's off its job
// template's.
func TestAWorkloadIsReadOffItsTemplate(t *testing.T) {
	f := mirrorOf(t,
		map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta("dep-1", "web", ""),
			"spec": map[string]any{"template": template("istio-proxy")}},
		map[string]any{"apiVersion": "batch/v1", "kind": "CronJob", "metadata": meta("cron-1", "nightly", ""),
			"spec": map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": template("app")}}}},
	)
	want := []string{"app", "istio-proxy", "setup"}

	got := objectOf(t, f, source{namespace: "prod", kind: tools.LogsSourceDeployment, name: "web"})
	assert.Equal(t, found{uid: "dep-1", names: want, annotated: "istio-proxy"}, got)

	got = objectOf(t, f, source{namespace: "prod", kind: tools.LogsSourceCronJob, name: "nightly"})
	assert.Equal(t, found{uid: "cron-1", names: want, annotated: "app"}, got)
}

// An object the mirror does not hold reads as none.
func TestAMissingObjectReadsAsNone(t *testing.T) {
	f := mirrorOf(t)
	for _, kind := range []tools.LogsSourceKind{tools.LogsSourcePod, tools.LogsSourceStatefulSet} {
		assert.Empty(t, objectOf(t, f, source{namespace: "prod", kind: kind, name: "web"}).uid, kind)
	}
}
