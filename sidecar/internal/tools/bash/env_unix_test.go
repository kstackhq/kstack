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

//go:build !windows

package bash

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A sandboxed run's environment is built whole: exactly the list, key for key,
// and none of the process's credentials.
func TestTheSandboxedEnvironmentIsBuiltWhole(t *testing.T) {
	rd := &runDir{path: "/run/kstack/runs/10-1", tmp: "/cache/tmp/10-2"}
	cluster := &target{context: "prod", cacheDir: "/cache/kubectl/7/abc"}

	got := sandboxedRunEnv(processEnv, kstackVars, "/data/ws", "/data/ws/sub", rd, cluster, "/data/th")

	assert.Equal(t, []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/data/ws",
		"PWD=/data/ws/sub",
		"ZDOTDIR=/run/kstack/runs/10-1",
		"TMPDIR=/cache/tmp/10-2",
		"KUBECONFIG=/run/kstack/runs/10-1/kubeconfig",
		"KUBECACHEDIR=/cache/kubectl/7/abc",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"LC_CTYPE=UTF-8",
		"TERM=dumb",
		"XDG_CACHE_HOME=/data/th/xdg/cache",
		"XDG_CONFIG_HOME=/data/th/xdg/config",
		"XDG_DATA_HOME=/data/th/xdg/data",
		"HELM_CACHE_HOME=/data/th/helm/cache",
		"HELM_CONFIG_HOME=/data/th/helm/config",
		"HELM_DATA_HOME=/data/th/helm/data",
		"NPM_CONFIG_CACHE=/data/th/npm",
		"PIP_CACHE_DIR=/data/th/pip",
		"GOCACHE=/data/th/go/build",
		"GOMODCACHE=/data/th/go/mod",
		"CARGO_HOME=/data/th/cargo",
		"KSTACK=1",
		"KSTACK_SIDECAR_PID=10",
		"KSTACK_HOST_PID=9",
	}, got)
}
