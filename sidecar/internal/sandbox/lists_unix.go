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
	"path/filepath"
	"slices"
	"strings"
)

// Never is the paths no run on this machine can read, whatever its policy
// grants, with ~/ under home.
func (s *Sandbox) Never(home string) []string { return neverPaths(home) }

// neverPaths is the Never lists with ~/ replaced by home. With no home it is
// the absolute paths alone: there is no home for a ~/ path to hide under.
func neverPaths(home string) []string {
	var paths []string
	for _, p := range slices.Concat(sharedLists.Never, platformLists.Never) {
		rel, inHome := strings.CutPrefix(p, "~/")
		switch {
		case !inHome:
			paths = append(paths, p)
		case home != "":
			paths = append(paths, filepath.Join(home, filepath.FromSlash(rel)))
		}
	}
	return paths
}

// systemFiles is the FilePolicy System answers: the System folders, trees and
// this executable at its resolved path read, deny denied, and no Read on or
// inside a Never path under home.
func (s *Sandbox) systemFiles(home string, trees, deny []string) FilePolicy {
	f := FilePolicy{Read: slices.Concat(systemFolders(), trees, []string{resolved(s.self)}), Deny: deny}
	return f.Outside(s.Never(home)...)
}

// systemFolders is the System folders every run on this platform reads.
func systemFolders() []string {
	return slices.Concat(sharedLists.System, platformLists.System)
}
