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
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/fileguard"
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
// nothing of the sidecar's rides in but LANG and TZ: PATH the run's own, the
// folders it froze at its start; HOME the workspace
// and PWD the start directory; TMPDIR the run's own, in the cache; ZDOTDIR
// the run's directory, made for the run and so holding no startup file, so
// zsh -c never sources a .zshenv a command left in the workspace; KUBECONFIG
// and KUBECACHEDIR for a run with a cluster (cluster not nil); LANG the
// sidecar's or the platform's default; TERM=dumb; then the variables that
// point each tool into toolHome; then toolchain, each found location's
// variables and asdf's versions; then what Kstack adds.
func sandboxedRunEnv(environ, kstack []string, path, workspace, dir string, rd *runDir, cluster *target, toolHome string, toolchain []string) []string {
	var lang, tz string
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		switch name {
		case "LANG":
			lang = value
		case "TZ":
			tz = value
		}
	}
	env := []string{"PATH=" + path}
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
	env = append(env, toolchain...)
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

// toolVersionsLimit is the most of ~/.tool-versions read.
const toolVersionsLimit = 64 << 10

// toolVersions is asdf's global versions, read from the user's
// ~/.tool-versions as parseToolVersions reads them. asdf reads them from
// $HOME, which in the sandbox is the workspace, so they ride as variables.
// Anything but a plain file holds none.
func toolVersions(home string) []string {
	f, err := fileguard.Open(filepath.Join(home, ".tool-versions"))
	if err != nil {
		return nil
	}
	defer f.Close()
	text, err := io.ReadAll(io.LimitReader(f, toolVersionsLimit))
	if err != nil {
		return nil
	}
	return parseToolVersions(string(text))
}

var (
	asdfTool    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	asdfVersion = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
)

// parseToolVersions is ASDF_<TOOL>_VERSION for each line of text whose tool
// and first version match asdf's plain shapes, the tool upper-cased with -
// spelled _, as asdf spells it. Any other line is skipped.
func parseToolVersions(text string) []string {
	var env []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !asdfTool.MatchString(f[0]) || !asdfVersion.MatchString(f[1]) {
			continue
		}
		name := strings.ToUpper(strings.ReplaceAll(f[0], "-", "_"))
		env = append(env, "ASDF_"+name+"_VERSION="+f[1])
	}
	return env
}
