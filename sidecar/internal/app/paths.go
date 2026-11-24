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
	"errors"
	"io/fs"
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
//	  beehive.db                           cluster
//	  chats/<chat id>/                     chat: results/, tasks/, workspace/
//	  monitor/<cluster id>/                chat: a monitor's results/, workspace/, toolhome/
//	<cache>/                               what Kstack rebuilds
//	  kube/<cache id>.db                   cluster: the mirror
//	  logs/<cache id>.db                   logs: the cache's log lines
//	  kubectl/<cluster id>/<server>/       bash: the kubectl cache
//	  tmp/<pid>-*/, tmp/<pid>.lock         bash: a sandboxed run's TMPDIR, the sidecar's lock
//	<runtime>/                             what lives for a session
//	  kstack-sidecar-<host pid>-<n>.sock   the host
//	  shell/                               bash: the snapshot, and Windows' scripts
//	  runs/<pid>-*/, runs/<pid>.lock       bash: a run's kubeconfig and socket, the lock
//
// A subfolder above that is a symlink or a Windows junction at start joins
// the fence (pathsOf).
type paths struct {
	LockFile   string
	AppDBFile  string
	ChatsDir   string
	MonitorDir string
	LogsDir    string
	Cluster    cluster.Paths
	Bash       bash.Paths
}

func pathsOf(cfg Config) paths {
	data, cache, runtime := cfg.DataDir, cfg.CacheDir, cfg.RuntimeDir
	kstack := []string{data, cache, runtime}
	// Every folder the layout names is fenced too: the host's config, state
	// and logs, and the app folders that hold the kinds on macOS and Windows.
	// Only an exact duplicate is dropped: one nested in another by name can
	// resolve elsewhere through a link, and the fences resolve each path
	// themselves.
	for _, dir := range cfg.Layout.Dirs() {
		if !slices.Contains(kstack, dir) {
			kstack = append(kstack, dir)
		}
	}
	// A subfolder that is a link holds Kstack's files where it leads, outside
	// the directories above, so it is fenced too: the fences resolve each path
	// they are given, so a link retargeted while Kstack runs is fenced where it
	// leads now, and its former target is not. A link made while Kstack runs is
	// not seen. A subfolder that cannot be read may be a link, so it is fenced.
	var linked []string
	sub := func(root, name string) string {
		dir := filepath.Join(root, name)
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return dir
		}
		if err != nil || realpath.IsLink(info) {
			linked = append(linked, dir)
		}
		return dir
	}
	p := paths{
		LockFile:   filepath.Join(data, "sidecar.lock"),
		AppDBFile:  filepath.Join(data, "app.db"),
		ChatsDir:   sub(data, "chats"),
		MonitorDir: sub(data, "monitor"),
		LogsDir:    sub(cache, "logs"),
		Cluster: cluster.Paths{
			BeehiveDBFile: filepath.Join(data, "beehive.db"),
			KubestoreDir:  sub(cache, "kube"),
		},
		Bash: bash.Paths{
			ShellDir:   sub(runtime, "shell"),
			RunsDir:    sub(runtime, "runs"),
			TmpDir:     sub(cache, "tmp"),
			KubectlDir: sub(cache, "kubectl"),
		},
	}
	p.Bash.DeniedDirs = slices.Concat(kstack, linked)
	return p
}
