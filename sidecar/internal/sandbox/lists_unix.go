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

//go:build !windows

package sandbox

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Never is the paths no run on this machine can read, whatever its policy
// grants, with ~/ under home.
func (s *Sandbox) Never(home string) []string { return neverPaths(home) }

// neverPaths is the Never lists with ~/ under home.
func neverPaths(home string) []string {
	return inHome(home, slices.Concat(sharedLists.Never, platformLists.Never))
}

// inHome is paths with ~/ replaced by home. With no home it is the absolute
// paths alone: there is no home for a ~/ path to lie under.
func inHome(home string, paths []string) []string {
	var out []string
	for _, p := range paths {
		rel, under := strings.CutPrefix(p, "~/")
		switch {
		case !under:
			out = append(out, p)
		case home != "":
			out = append(out, filepath.Join(home, filepath.FromSlash(rel)))
		}
	}
	return out
}

// System is what every sandboxed run on this machine starts from, from one
// look at home. It reads the System folders, each Toolchain folder that
// exists and is not broad (broadDirs), shell's folders (shellReads) and this
// executable at its resolved path, and denies Homebrew's var. It holds no
// Read on or inside a Never path, and none over a fixed mount. A
// location's Env is set when its first folder exists.
func (s *Sandbox) System(home, shell string) System {
	var sys System
	read := systemFolders()
	broad := broadDirs(home)
	if home != "" {
		readable := func(p string) bool {
			_, err := os.Stat(p)
			return err == nil && !isBroad(p, broad)
		}
		for _, l := range slices.Concat(sharedLists.Toolchain, platformLists.Toolchain) {
			folders := inHome(home, l.Read)
			read = append(read, slices.DeleteFunc(slices.Clone(folders), func(p string) bool { return !readable(p) })...)
			if !readable(folders[0]) {
				continue
			}
			for _, name := range slices.Sorted(maps.Keys(l.Env)) {
				sys.Env = append(sys.Env, name+"="+inHome(home, []string{l.Env[name]})[0])
			}
			sys.Asdf = sys.Asdf || l.Env["ASDF_DATA_DIR"] != ""
		}
	}
	for _, p := range shellReads(home, shell, broad) {
		if !slices.Contains(read, p) {
			read = append(read, p)
		}
	}
	read = slices.DeleteFunc(append(read, resolved(s.self)), overFixedMount)
	sys.Files = FilePolicy{Read: read, Deny: slices.Clone(brewVar)}.Outside(neverPaths(home)...)
	return sys
}

// broadDirs is what no toolchain or shell folder may be or hold, resolved:
// /, and with a home, the home and appDataDirs under it.
func broadDirs(home string) []string {
	if home == "" {
		return []string{"/"}
	}
	return append(resolvedAll(append(inHome(home, appDataDirs), home)), "/")
}

// isBroad reports whether p, resolved, is or holds one of broad.
func isBroad(p string, broad []string) bool {
	r := resolved(p)
	return slices.ContainsFunc(broad, func(b string) bool { return within(b, r) })
}

// maxHops is how many links the kernel follows in one lookup.
const maxHops = 40

// shellReads is what System reads for shell and for each link on its way to
// the program, which the kernel reads to run it: the folder shellFolder
// names, or the program alone where that folder is broad.
func shellReads(home, shell string, broad []string) []string {
	var reads []string
	p := shell
	for range maxHops {
		if dir := shellFolder(home, p); isBroad(dir, broad) {
			reads = append(reads, p)
		} else {
			reads = append(reads, dir)
		}
		target, err := os.Readlink(p)
		if err != nil {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		p = target
	}
	return reads
}

// shellFolder is the folder System reads for shell. A folder named bin
// outside the home reads its parent, so a shell under a prefix such as
// /opt/zsh reaches its own share, unless the parent is / or holds the home.
// Under the home the folder is read alone, since a parent there can hold
// every app's data.
func shellFolder(home, shell string) string {
	dir := filepath.Dir(shell)
	underHome := home != "" && within(resolved(dir), resolved(home))
	parent := filepath.Dir(dir)
	holdsHome := home != "" && within(resolved(home), resolved(parent))
	if filepath.Base(dir) == "bin" && !underHome && parent != "/" && !holdsHome {
		return parent
	}
	return dir
}

// systemFolders is the System folders every run on this platform reads.
func systemFolders() []string {
	return slices.Concat(sharedLists.System, platformLists.System)
}
