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
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/lib/realpath"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// paths is every file and directory the sidecar keeps, each in the kind of
// directory it belongs to, grouped by the service that owns it. Each service
// is handed its own and names nothing else; each owner makes its own
// directories. A shipped path is never renamed or moved: nothing migrates the
// layout on upgrade, so it only grows (docs/persisted-data.md).
//
//	<data>/                                what a user would lose
//	  sidecar.lock                         app: held while a sidecar runs; never deleted
//	  app.db                               app
//	  settings.json                        app: the user's settings
//	  beehive.db                           cluster
//	  chats/<chat id>/                     chat: results/, tasks/, workspace/
//	  monitor/<cluster id>/                chat: a monitor's results/, workspace/, toolhome/
//	<cache>/                               what Kstack rebuilds
//	  kube/<cache id>.db                   cluster: the mirror
//	  logs/<cache id>.db                   logs: the cache's log lines
//	  kubectl/<cluster id>/<server>/       bash: the kubectl cache
//	  tmp/<pid>-*/, tmp/<pid>.lock         bash: a sandboxed run's TMPDIR, the sidecar's lock
//	<runtime>/                             what lives for a session
//	  kstack-sidecar-<host pid>-<n>.sock   the host; one a killed sidecar left is swept at start
//	  shell/                               bash: the snapshot, and Windows' scripts
//	  runs/<pid>-*/, runs/<pid>.lock       bash: a run's kubeconfig and socket, the lock
//
// Each subfolder above that a service keeps its files in is made at start,
// and must be a folder, not a link (makeSubfolders).
type paths struct {
	LockFile     string
	AppDBFile    string
	SettingsFile string
	ChatsDir     string
	MonitorDir   string
	LogsDir      string
	Cluster      cluster.Paths
	Bash         bash.Paths
}

func pathsOf(cfg Config) paths {
	data, cache, runtime := cfg.Folders.Data, cfg.Folders.Cache, cfg.Folders.Runtime
	kstack := []string{data, cache, runtime}
	// Every folder the layout names is fenced too: the host's config, state
	// and logs, and the app folders that hold the kinds on macOS and Windows.
	for _, dir := range cfg.Folders.Layout.Dirs() {
		if !slices.Contains(kstack, dir) {
			kstack = append(kstack, dir)
		}
	}
	p := paths{
		LockFile:     filepath.Join(data, "sidecar.lock"),
		AppDBFile:    filepath.Join(data, "app.db"),
		SettingsFile: filepath.Join(data, "settings.json"),
		ChatsDir:     filepath.Join(data, "chats"),
		MonitorDir:   filepath.Join(data, "monitor"),
		LogsDir:      filepath.Join(cache, "logs"),
		Cluster: cluster.Paths{
			BeehiveDBFile: filepath.Join(data, "beehive.db"),
			KubestoreDir:  filepath.Join(cache, "kube"),
		},
		Bash: bash.Paths{
			ShellDir:   filepath.Join(runtime, "shell"),
			RunsDir:    filepath.Join(runtime, "runs"),
			TmpDir:     filepath.Join(cache, "tmp"),
			KubectlDir: filepath.Join(cache, "kubectl"),
		},
	}
	p.Bash.DeniedDirs = fenceOf(kstack, insideByNameAndFile)
	p.Bash.Links = cfg.Folders.Links
	return p
}

// fenceOf is dirs less each one inside another of them (inside), so the
// fence holds each resolved folder once.
func fenceOf(dirs []string, inside func(p, dir string) bool) []string {
	var fence []string
	for _, dir := range dirs {
		if !slices.ContainsFunc(dirs, func(other string) bool { return inside(dir, other) }) {
			fence = append(fence, dir)
		}
	}
	return fence
}

// insideByNameAndFile reports whether the resolved p is inside the resolved
// dir both by name, as the sandbox denies by path, and as files, so no letter
// case is folded. A second name of a folder, such as a bind mount, keeps its
// own entry. Only two folders that both exist are compared: a folder dropped
// by mistake is left unfenced.
func insideByNameAndFile(p, dir string) bool {
	return p != dir && realpath.Within(p, dir) && exists(p) && exists(dir) &&
		realpath.Relation(p, dir) == realpath.Inside
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
