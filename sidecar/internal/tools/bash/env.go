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
	"os"
	"runtime"
	"strings"
)

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

// sandboxedRunEnv is a sandboxed run's environment, built from one table so
// nothing of the sidecar's rides in but PATH, LANG and TZ: HOME the workspace
// and PWD the start directory; TMPDIR the run's own, in the cache; ZDOTDIR
// the run's directory, made for the run and so holding no startup file, so
// zsh -c never sources a .zshenv a command left in the workspace; KUBECONFIG
// and KUBECACHEDIR for a run with a cluster (cluster not nil); LANG the
// sidecar's or the platform's default; TERM=dumb; then the variables that
// point each tool into toolHome; then what Kstack adds.
func sandboxedRunEnv(environ, kstack []string, workspace, dir string, rd *runDir, cluster *target, toolHome string) []string {
	var path, lang, tz string
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		switch name {
		case "PATH":
			path = value
		case "LANG":
			lang = value
		case "TZ":
			tz = value
		}
	}
	var env []string
	if path != "" {
		env = append(env, "PATH="+path)
	}
	env = append(env, "HOME="+workspace, "PWD="+dir, "TMPDIR="+rd.tmp, "ZDOTDIR="+rd.path)
	if cluster != nil {
		env = append(env, "KUBECONFIG="+rd.kubeconfig(), "KUBECACHEDIR="+cluster.cacheDir)
	}
	if lang == "" {
		lang = defaultLang(runtime.GOOS, fileExists)
	}
	env = append(env, "LANG="+lang)
	if tz != "" {
		env = append(env, "TZ="+tz)
	}
	env = append(env, "TERM=dumb")
	env = append(env, toolHomeEnv(toolHome)...)
	return append(env, kstack...)
}

// defaultLang is LANG for a run whose sidecar has none: en_US.UTF-8 on
// macOS, which always has it; on Linux C.UTF-8 where the system has that
// locale, else C.
func defaultLang(goos string, exists func(string) bool) string {
	switch {
	case goos == "darwin":
		return "en_US.UTF-8"
	case exists("/usr/lib/locale/C.utf8") || exists("/usr/lib/locale/C.UTF-8"):
		return "C.UTF-8"
	}
	return "C"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
