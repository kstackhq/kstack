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

package securityconfig

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
)

// pathDir is one folder filterPath kept.
type pathDir struct {
	Dir    string // the entry as the shell gave it
	Target string // the folder it resolved to
	Shared bool   // writable by a group other than an administrators' one
}

// zones is Zones with every path resolved once, so each folder is compared
// with them by text.
type zones struct {
	never, broad, read, deny []string
}

func resolveZones(z Zones) zones {
	return zones{
		never: sandbox.Resolved(z.Never),
		broad: sandbox.Broad(z.Home),
		read:  sandbox.Resolved(z.Open.Read),
		deny:  sandbox.Resolved(z.Open.Deny),
	}
}

// open reports whether target, resolved, is under a Read path and no Deny
// path: every run reads it already.
func (z zones) open(target string) bool {
	return sandbox.Under(target, z.read) && !sandbox.Under(target, z.deny)
}

// filterPath is entries less what no run should search, in the shell's
// order, and how many each rule dropped. Every check is on the resolved path.
func filterPath(entries []string, z zones) (kept []pathDir, dropped map[string]int) {
	dropped = map[string]int{}
	for _, dir := range entries {
		d, rule := filterDir(dir, z)
		if rule == "" && slices.ContainsFunc(kept, func(k pathDir) bool { return k.Target == d.Target }) {
			// It finds nothing the first does not.
			rule = "duplicate"
		}
		if rule != "" {
			dropped[rule]++
			continue
		}
		kept = append(kept, d)
	}
	return kept, dropped
}

// filterDir is dir as a pathDir, or the rule that drops it: the sandbox's
// rules for a folder a run may search, then this list's own.
func filterDir(dir string, z zones) (pathDir, string) {
	target, info, reason := sandbox.SearchFolder(dir, z.never, z.broad)
	switch {
	case reason != "":
		return pathDir{}, reason
	case slices.Contains(strings.Split(target, string(filepath.Separator)), "node_modules"):
		// A project's folder holds whatever its last install fetched.
		return pathDir{}, "project"
	}
	return pathDir{Dir: dir, Target: target, Shared: shared(info)}, ""
}
