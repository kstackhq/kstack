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

package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Each path is where the tree on paths puts it.
func TestEveryPathIsUnderItsKind(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	p := pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime})

	for want, got := range map[string]string{
		filepath.Join(data, "app.db"):              p.AppDBFile,
		filepath.Join(data, "chats"):               p.ChatsDir,
		filepath.Join(data, "security.json"):       p.SecurityFile,
		filepath.Join(data, "settings.json"):       p.Cloud.SettingsFile,
		filepath.Join(data, "settings-queue.json"): p.Cloud.QueueFile,
		filepath.Join(data, "beehive.db"):          p.Cluster.BeehiveDBFile,
		filepath.Join(cache, "kubestore"):          p.Cluster.KubestoreDir,
		filepath.Join(cache, "kubectl"):            p.Bash.KubectlDir,
		filepath.Join(runtime, "shell"):            p.Bash.ShellDir,
		filepath.Join(runtime, "runs"):             p.Bash.RunsDir,
		filepath.Join(cache, "tmp"):                p.Bash.TmpDir,
	} {
		assert.Equal(t, want, got)
	}
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "a sandboxed run reads none of the three")
}
