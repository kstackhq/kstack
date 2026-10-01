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

	"github.com/kstackhq/kstack/sidecar/internal/cloud"
	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// paths is every file and directory the sidecar keeps, each in the kind of
// directory it belongs to, grouped by the service that owns it. Each service
// is handed its own and names nothing else; each owner makes its own
// directories.
//
//	<data>/                                what a user would lose
//	  app.db                               app
//	  security.json                        app: the security settings
//	  beehive.db                           clustersvc
//	  settings.json, settings-queue.json   cloud
//	  chats/<chat id>/                     chatsvc: results/, tasks/, workspace/
//	<cache>/                               what Kstack rebuilds
//	  kubestore/<cache id>.db              clustersvc: the mirror
//	  kubectl/<cluster id>/<server>/       bash: the kubectl cache
//	  tmp/<pid>-*/, tmp/<pid>.lock         bash: a sandboxed run's TMPDIR, the sidecar's lock
//	<runtime>/                             what lives for a session
//	  kstack-sidecar-<host pid>-<n>.sock   the host
//	  shell/                               bash: the snapshot, and Windows' scripts
//	  runs/<pid>-*/, runs/<pid>.lock       bash: a run's kubeconfig and socket, the lock
type paths struct {
	AppDBFile    string
	SecurityFile string
	ChatsDir     string
	Cloud        cloud.Paths
	Cluster      clustersvc.Paths
	Bash         bash.Paths
}

func pathsOf(cfg Config) paths {
	data, cache, runtime := cfg.DataDir, cfg.CacheDir, cfg.RuntimeDir
	return paths{
		AppDBFile:    filepath.Join(data, "app.db"),
		SecurityFile: filepath.Join(data, "security.json"),
		ChatsDir:     filepath.Join(data, "chats"),
		Cloud: cloud.Paths{
			SettingsFile: filepath.Join(data, "settings.json"),
			QueueFile:    filepath.Join(data, "settings-queue.json"),
		},
		Cluster: clustersvc.Paths{
			BeehiveDBFile: filepath.Join(data, "beehive.db"),
			KubestoreDir:  filepath.Join(cache, "kubestore"),
		},
		Bash: bash.Paths{
			ShellDir:   filepath.Join(runtime, "shell"),
			RunsDir:    filepath.Join(runtime, "runs"),
			TmpDir:     filepath.Join(cache, "tmp"),
			KubectlDir: filepath.Join(cache, "kubectl"),
			DeniedDirs: []string{data, cache, runtime},
		},
	}
}
