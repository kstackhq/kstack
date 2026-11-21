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
		filepath.Join(data, "sidecar.lock"):  p.LockFile,
		filepath.Join(data, "app.db"):        p.AppDBFile,
		filepath.Join(data, "chats"):         p.ChatsDir,
		filepath.Join(data, "monitor"):       p.MonitorDir,
		filepath.Join(data, "settings.json"): p.SettingsFile,
		filepath.Join(data, "beehive.db"):    p.Cluster.BeehiveDBFile,
		filepath.Join(cache, "kube"):         p.Cluster.KubestoreDir,
		filepath.Join(cache, "logs"):         p.LogsDir,
		filepath.Join(cache, "kubectl"):      p.Bash.KubectlDir,
		filepath.Join(runtime, "shell"):      p.Bash.ShellDir,
		filepath.Join(runtime, "runs"):       p.Bash.RunsDir,
		filepath.Join(cache, "tmp"):          p.Bash.TmpDir,
	} {
		assert.Equal(t, want, got)
	}
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "a sandboxed run reads none of the three")
}

func TestTheLogDirectoryIsKstacks(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	logs := filepath.FromSlash("/u/Library/Logs/Kstack")
	p := pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime, LogDir: logs})
	assert.Equal(t, []string{data, cache, runtime, logs}, p.Bash.DeniedDirs,
		"a log directory outside the three is one of Kstack's directories, which the file tools are fenced out of too")

	nested := filepath.Join(data, "logs")
	p = pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime, LogDir: nested})
	assert.Equal(t, []string{data, cache, runtime, nested}, p.Bash.DeniedDirs,
		"one under the data directory by name is kept, since a link can lead it elsewhere")

	p = pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime, LogDir: data})
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "one that is the data directory adds nothing")
}

func TestTheConfigDirectoryIsKstacks(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	config := filepath.FromSlash("/u/.config/Kstack")
	p := pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime, ConfigDir: config})
	assert.Equal(t, []string{data, cache, runtime, config}, p.Bash.DeniedDirs,
		"the config directory holds host.json, which the file tools are fenced out of")

	// macOS's config directory is the data directory.
	p = pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime, ConfigDir: data})
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "one that is the data directory adds nothing")

	nested := filepath.Join(data, "config")
	p = pathsOf(Config{DataDir: data, CacheDir: cache, RuntimeDir: runtime, ConfigDir: nested})
	assert.Equal(t, []string{data, cache, runtime, nested}, p.Bash.DeniedDirs,
		"one under another by name is kept, since a link can lead it elsewhere")
}
