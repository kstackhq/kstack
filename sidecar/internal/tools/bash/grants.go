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
	"log/slog"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
)

// grantRules is the session's folders as Files rules: each checked again
// against what this run enforces, since a folder can move or become a link
// after the session's builder checked it, and one that fails is left out.
// A folder granted twice keeps the wider grant, and one under a read-write
// folder is dropped, since that rule covers it and Check refuses any rule
// beneath a Write rule. It reads the disk.
func (t *Tool) grantRules(folders []session.Folder, open sandbox.FilePolicy, never []string, entries []settings.PathEntry) sandbox.FilePolicy {
	if len(folders) == 0 {
		return sandbox.FilePolicy{}
	}
	zones := settings.Zones{Never: slices.Concat(never, t.denied), Open: open, Home: t.home, NoWrite: sandbox.NoWrite(t.home)}
	pathFolders := settings.PathEntryFolders(entries)
	var passed []string
	writable := map[string]bool{}
	for _, f := range folders {
		if err := settings.CheckStoredFolder(f.Path, f.Write, zones, pathFolders); err != nil {
			slog.Info("folder grant left out of a run", "folder", f.Path, "reason", err.Error())
			continue
		}
		if _, seen := writable[f.Path]; !seen {
			passed = append(passed, f.Path)
		}
		writable[f.Path] = writable[f.Path] || f.Write
	}
	var rules sandbox.FilePolicy
	for _, p := range passed {
		underWrite := slices.ContainsFunc(passed, func(w string) bool { return w != p && writable[w] && sandbox.Under(p, []string{w}) })
		switch {
		case underWrite:
		case writable[p]:
			rules.Write = append(rules.Write, p)
		default:
			rules.Read = append(rules.Read, p)
		}
	}
	return rules
}
