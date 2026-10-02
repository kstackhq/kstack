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

package bash

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// processEnv is a process environment carrying credentials a sandboxed run
// must not inherit.
var processEnv = []string{
	"PATH=/usr/local/bin:/usr/bin",
	"HOME=/Users/ana",
	"AWS_ACCESS_KEY_ID=AKIAEXAMPLE",
	"KUBECONFIG=/Users/ana/.kube/config",
	"SSH_AUTH_SOCK=/tmp/ssh.sock",
	"GITHUB_TOKEN=ghp_example",
	"LANG=en_US.UTF-8",
	"LC_ALL=en_US.UTF-8",
	"LC_CTYPE=UTF-8",
	"TMPDIR=/var/folders/x/T/",
	"TERM=xterm-256color",
}

var kstackVars = []string{"KSTACK=1", "KSTACK_SIDECAR_PID=10", "KSTACK_HOST_PID=9"}

// A run with no cluster has no kubeconfig and no kubectl cache to name.
func TestWithNoClusterTheEnvironmentNamesNoKubeconfig(t *testing.T) {
	got := sandboxedRunEnv(processEnv, kstackVars, "/data/ws", "/data/ws", &runDir{path: "/tmp/r"}, nil, "/th")

	for _, kv := range got {
		assert.NotRegexp(t, `^KUBE`, kv)
	}
}

// A process with no PATH gives the run none.
func TestNoPathIsNoPath(t *testing.T) {
	rd := &runDir{path: "/run/r", tmp: "/cache/t"}
	got := sandboxedRunEnv([]string{"LANG=C"}, nil, "/ws", "/ws", rd, nil, "/th")

	assert.Equal(t, []string{"HOME=/ws", "PWD=/ws", "TMPDIR=/cache/t", "ZDOTDIR=/run/r", "LANG=C", "TERM=dumb"}, got[:6])
	assert.NotRegexp(t, `^PATH=`, strings.Join(got, "\n"))
}

// A run outside the sandbox keeps the process's environment whole, then what
// Kstack adds, then PWD.
func TestAnOutsideRunKeepsItsEnvironment(t *testing.T) {
	got := outsideEnv(processEnv, kstackVars, "/data/ws")

	assert.Equal(t, append(append(append([]string{}, processEnv...), kstackVars...), "PWD=/data/ws"), got)
}

// The sidecar's LANG passes; with none, the platform's default: on macOS
// en_US.UTF-8, on Linux C.UTF-8 where the system has that locale, else C.
func TestTheLocale(t *testing.T) {
	rd := &runDir{path: "/run/r", tmp: "/cache/t"}
	assert.Contains(t, sandboxedRunEnv([]string{"LANG=de_DE.UTF-8"}, nil, "/ws", "/ws", rd, nil, "/th"), "LANG=de_DE.UTF-8")
	assert.Contains(t, sandboxedRunEnv(nil, nil, "/ws", "/ws", rd, nil, "/th"), "LANG="+defaultLang(runtime.GOOS, fileExists))

	none := func(string) bool { return false }
	assert.Equal(t, "en_US.UTF-8", defaultLang("darwin", none))
	assert.Equal(t, "C", defaultLang("linux", none))
	for _, dir := range []string{"/usr/lib/locale/C.utf8", "/usr/lib/locale/C.UTF-8"} {
		assert.Equal(t, "C.UTF-8", defaultLang("linux", func(p string) bool { return p == dir }), dir)
	}
}
