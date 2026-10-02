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

import "strings"

// outsideEnv is a run's environment outside the sandbox: the process's whole,
// then what Kstack adds, then PWD. exec sets PWD from Dir only when Env is nil;
// without it the shell keeps the sidecar's, finds it wrong and reads the
// physical path, so pwd would print something other than the directory the
// approval request showed.
func outsideEnv(environ, kstack []string, dir string) []string {
	env := make([]string, 0, len(environ)+len(kstack)+1)
	env = append(env, environ...)
	env = append(env, kstack...)
	return append(env, "PWD="+dir)
}

// sandboxedRunEnv is a sandboxed run's environment, built rather than inherited,
// so no credential rides in: PATH, LANG and every LC_* from the process's;
// HOME the workspace and PWD the start directory; ZDOTDIR the run's directory,
// made for the run and so holding no startup file, so zsh -c never sources a
// .zshenv a command left in the workspace; TMPDIR the run's own, in the cache;
// KUBECONFIG and KUBECACHEDIR for a run with a cluster (cluster not nil);
// TERM=dumb; the variables that point each tool into toolHome; and what
// Kstack adds.
func sandboxedRunEnv(environ, kstack []string, workspace, dir string, rd *runDir, cluster *target, toolHome string) []string {
	var path string
	var locale []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "PATH":
			path = kv
		case name == "LANG" || strings.HasPrefix(name, "LC_"):
			locale = append(locale, kv)
		}
	}
	var env []string
	if path != "" {
		env = append(env, path)
	}
	env = append(env, "HOME="+workspace, "PWD="+dir, "ZDOTDIR="+rd.path, "TMPDIR="+rd.tmp)
	if cluster != nil {
		env = append(env, "KUBECONFIG="+rd.kubeconfig(), "KUBECACHEDIR="+cluster.cacheDir)
	}
	env = append(env, locale...)
	env = append(env, "TERM=dumb")
	env = append(env, toolHomeEnv(toolHome)...)
	return append(env, kstack...)
}
