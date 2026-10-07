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

package sandbox

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A policy fails Check for each thing it refuses, and passes when every
// rule is where a run's rules go.
func TestAPolicyChecks(t *testing.T) {
	base := resolved(t.TempDir())
	p := func(rel string) string { return filepath.Join(base, filepath.FromSlash(rel)) }
	workspace := func() Policy {
		return Policy{
			Files: FilePolicy{Read: []string{p("usr"), p("home/.config")}, Write: []string{p("cover")}, Deny: []string{p("usr/var")}},
			Always: AlwaysPolicy{
				Deny:   []string{p("home/.ssh")},
				Kstack: []string{p("data"), p("cache"), p("runtime")},
				Read:   []string{p("runtime/runs/1"), p("runtime/shell/snapshot.sh")},
				Write:  []string{p("data/chats/c/workspace"), p("cache/tmp/1")},
			},
			Network: NetworkPolicy{Relays: []Relay{{Port: 6443, Socket: p("runtime/runs/1/proxy.sock")}}},
		}
	}
	assert.NoError(t, workspace().Check(), "the workspace policy")
	tie := workspace()
	tie.Files.Deny = append(tie.Files.Deny, p("cover"))
	assert.NoError(t, tie.Check(), "a rule on a Write rule's own path is a tie")

	for name, change := range map[string]func(*Policy){
		"a relative path":                    func(q *Policy) { q.Files.Read = append(q.Files.Read, "usr/lib") },
		"a Files rule beneath a Files Write": func(q *Policy) { q.Files.Read = append(q.Files.Read, p("cover/x")) },
		"a Deny beneath a Files Write": func(q *Policy) {
			q.Files.Write = append(q.Files.Write, p("home/.config"))
			q.Files.Deny = append(q.Files.Deny, p("home/.config/gcloud"))
		},
		"an Always path beneath a Files Write": func(q *Policy) { q.Always.Deny = append(q.Always.Deny, p("cover/.ssh")) },
		"a rule beneath an Always Write":       func(q *Policy) { q.Always.Read = append(q.Always.Read, p("cache/tmp/1/x")) },
		"a Files rule on a Deny path":          func(q *Policy) { q.Files.Deny = append(q.Files.Deny, p("home/.ssh")) },
		"a Files rule inside a Deny path":      func(q *Policy) { q.Files.Read = append(q.Files.Read, p("home/.ssh/keys")) },
		"a Files rule inside a Kstack path":    func(q *Policy) { q.Files.Read = append(q.Files.Read, p("data/bin")) },
		"a run's own path outside Kstack's":    func(q *Policy) { q.Always.Read = append(q.Always.Read, p("home/snapshot.sh")) },
		"a run's own path inside a Deny path": func(q *Policy) {
			q.Always.Deny = append(q.Always.Deny, p("data/keys"))
			q.Always.Write = append(q.Always.Write, p("data/keys/w"))
		},
		"a run's own path holding a Deny path": func(q *Policy) {
			q.Always.Deny = append(q.Always.Deny, p("runtime/runs/1/secret"))
		},
		"a second relay": func(q *Policy) { q.Network.Relays = append(q.Network.Relays, Relay{Port: 1, Socket: p("s")}) },
	} {
		t.Run(name, func(t *testing.T) {
			q := workspace()
			change(&q)
			assert.Error(t, q.Check())
		})
	}
}

// Outside drops a Read and a Write rule on or inside the paths it is given,
// keeps one beside them and one above them, and keeps every Deny.
func TestOutsideDropsWhatIsOnOrInsideItsPaths(t *testing.T) {
	f := FilePolicy{
		Read:  []string{"/k", "/k/bin", "/kx", "/"},
		Write: []string{"/k/w", "/w"},
		Deny:  []string{"/k/d"},
	}

	got := f.Outside("/k")

	assert.Equal(t, FilePolicy{Read: []string{"/kx", "/"}, Write: []string{"/w"}, Deny: []string{"/k/d"}}, got)
}

// A Deny is compiled only where a Files Read or Write reaches it: inside one,
// on one, or holding one. Anywhere else its path is off limits already.
func TestADenyIsCompiledOnlyWhereAReadOrWriteReachesIt(t *testing.T) {
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "home/.local/share/keyrings", "home/.kube", "srv/tools", "home/.ssh")
	keyrings, kube, tools, ssh := d[0], d[1], d[2], d[3]
	p := Policy{
		Files:  FilePolicy{Read: []string{filepath.Join(base, "home", ".local"), kube, tools}},
		Always: AlwaysPolicy{Deny: []string{ssh, keyrings, kube, filepath.Join(base, "srv")}},
	}

	var denied []string
	for _, r := range p.rules() {
		if r.kind == ruleDeny {
			denied = append(denied, r.at)
		}
	}

	assert.Equal(t, []string{keyrings, kube, filepath.Join(base, "srv")}, denied)
}

func TestLimitsAreChecked(t *testing.T) {
	assert.NoError(t, Policy{}.Check(), "no limits")
	assert.NoError(t, Policy{Limits: Limits{CPUSeconds: 1, MemoryBytes: 1, OpenFiles: 1, Processes: 1}}.Check())
	for name, l := range map[string]Limits{
		"a negative CPU time":          {CPUSeconds: -1},
		"a negative memory":            {MemoryBytes: -1, OpenFiles: 1},
		"a negative file count":        {OpenFiles: -1},
		"a negative process count":     {Processes: -1, OpenFiles: 1},
		"memory with no file limit":    {MemoryBytes: 1},
		"processes with no file limit": {Processes: 1},
	} {
		assert.Error(t, Policy{Limits: l}.Check(), name)
	}
}
