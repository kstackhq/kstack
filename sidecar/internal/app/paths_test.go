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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/app/layout"
	"github.com/kstackhq/kstack/sidecar/internal/lib/realpath"
)

// Each path is where the tree on paths puts it.
func TestEveryPathIsUnderItsKind(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	p := pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime}})

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
	p := pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Logs: logs}}})
	assert.Equal(t, []string{data, cache, runtime, logs}, p.Bash.DeniedDirs,
		"a log directory outside the three is one of Kstack's directories, which the file tools are fenced out of too")

	nested := filepath.Join(data, "logs")
	p = pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Logs: nested}}})
	assert.Equal(t, []string{data, cache, runtime, nested}, p.Bash.DeniedDirs,
		"one under the data directory by name that does not exist keeps its entry")

	p = pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Logs: data}}})
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "one that is the data directory adds nothing")
}

func TestTheStateDirectoryIsKstacks(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	state := filepath.FromSlash("/u/.local/state/kstack")
	logs := filepath.Join(state, "logs")
	p := pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Logs: logs, State: state}}})
	assert.Equal(t, []string{data, cache, runtime, logs, state}, p.Bash.DeniedDirs,
		"the state directory holds the window frame and the webview's profile beside the logs")

	p = pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{State: data}}})
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "one that is the data directory adds nothing")
}

func TestTheAppFolderIsKstacks(t *testing.T) {
	app := filepath.FromSlash("/u/Library/Application Support/Kstack")
	data, state := filepath.Join(app, "data"), filepath.Join(app, "state")
	cache, runtime := filepath.FromSlash("/c"), filepath.FromSlash("/r")
	p := pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{State: state, App: app}}})
	assert.Equal(t, []string{data, cache, runtime, state, app}, p.Bash.DeniedDirs,
		"the folder holding the kinds is fenced beside them, so nothing is written among them")
}

// Windows keeps the config in a folder of its own on the roaming root, apart
// from the app folder.
func TestTheConfigsAppFolderIsKstacks(t *testing.T) {
	app := filepath.FromSlash("/u/AppData/Local/Kstack")
	appOnConfigRoot := filepath.FromSlash("/u/AppData/Roaming/Kstack")
	data, config := filepath.Join(app, "data"), filepath.Join(appOnConfigRoot, "config")
	cache, runtime := filepath.Join(app, "cache"), filepath.Join(app, "run")
	cfg := Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Config: config, App: app, AppOnConfigRoot: appOnConfigRoot}}}
	assert.Equal(t, []string{data, cache, runtime, config, app, appOnConfigRoot}, pathsOf(cfg).Bash.DeniedDirs,
		"the config's folder is fenced beside the config, so nothing is written next to it")

	cfg.Folders.Layout.AppOnConfigRoot = app
	assert.Equal(t, []string{data, cache, runtime, config, app}, pathsOf(cfg).Bash.DeniedDirs,
		"one that is the app folder adds nothing")
}

func TestTheConfigDirectoryIsKstacks(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	config := filepath.FromSlash("/u/.config/Kstack")
	p := pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Config: config}}})
	assert.Equal(t, []string{data, cache, runtime, config}, p.Bash.DeniedDirs,
		"the config directory holds host.json, which the file tools are fenced out of")

	p = pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Config: data}}})
	assert.Equal(t, []string{data, cache, runtime}, p.Bash.DeniedDirs, "one that is the data directory adds nothing")

	nested := filepath.Join(data, "config")
	p = pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: layout.Layout{Config: nested}}})
	assert.Equal(t, []string{data, cache, runtime, nested}, p.Bash.DeniedDirs,
		"one under another by name that does not exist keeps its entry")
}

// A debug build can move the data and cache directories off the layout; the
// layout's own are still Kstack's.
func TestTheLayoutsDataAndCacheAreKstacks(t *testing.T) {
	data, cache, runtime := filepath.FromSlash("/d"), filepath.FromSlash("/c"), filepath.FromSlash("/r")
	l := layout.Layout{Data: filepath.FromSlash("/l/data"), Cache: filepath.FromSlash("/l/cache")}
	p := pathsOf(Config{Folders: Folders{Data: data, Cache: cache, Runtime: runtime, Layout: l}})
	assert.Equal(t, []string{data, cache, runtime, l.Data, l.Cache}, p.Bash.DeniedDirs)
}

// fenceOf drops a folder inside another and keeps the order of the rest. One
// inside does not place in another keeps its entry: in a case-sensitive
// Windows folder, logs under state beside a State.
func TestFenceOfDropsAFolderInsideAnother(t *testing.T) {
	inside := func(p, dir string) bool { return p == "/state/logs" && dir == "/state" }

	got := fenceOf([]string{"/data", "/state/logs", "/state", "/State/logs"}, inside)

	assert.Equal(t, []string{"/data", "/state", "/State/logs"}, got)
}

// A folder that does not exist yet keeps its entry, even under one that
// does: dropped by mistake, it would be left unfenced.
func TestAMissingFolderIsInsideNoOther(t *testing.T) {
	state := scratch(t)
	logs := filepath.Join(state, "logs")

	assert.False(t, insideByNameAndFile(logs, state))
	require.NoError(t, os.Mkdir(logs, 0o700))
	assert.True(t, insideByNameAndFile(logs, state))
	assert.False(t, insideByNameAndFile(state, state))
}

// Every platform's layout, on real folders, is fenced with no folder inside
// another: each resolved folder once.
func TestTheFenceHoldsNoFolderInsideAnother(t *testing.T) {
	for _, c := range []struct {
		name     string
		platform layout.Platform
		roots    func(root string) layout.Roots
	}{
		{"linux", layout.Linux, linuxRoots},
		{"macos", layout.MacOS, macOSRoots},
		{"windows", layout.Windows, windowsRoots},
		{"windows with one root", layout.Windows, sharedWindowsRoots},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := scratch(t)
			n := namedOn(c.platform, root, c.roots(root))
			for _, dir := range n.Layout.Dirs() {
				require.NoError(t, os.MkdirAll(dir, 0o700))
			}
			f, err := ResolveFolders(n)
			require.NoError(t, err)

			fence := pathsOf(Config{Folders: f}).Bash.DeniedDirs

			for _, dir := range f.Layout.Dirs() {
				assert.True(t, realpath.UnderAny(dir, fence), "%s is fenced", dir)
			}
			assert.Equal(t, fence, fenceOf(fence, insideByNameAndFile), "no folder in the fence is inside another")
		})
	}
}
