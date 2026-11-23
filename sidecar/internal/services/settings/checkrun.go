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

package settings

import (
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

// RunCheck is the sync's checks over one folder, against a run's own open
// folders and the paths it never opens, since the stored list can predate a path the
// denied-always list gained, a chmod, or a narrower open list.
type RunCheck struct {
	z zones
	targetLookup
}

// NewRunCheck resolves open, never and home's broad folders once for every
// entry of a run.
func NewRunCheck(open sandbox.FilePolicy, never []string, home string) RunCheck {
	return RunCheck{z: zonesOf(Zones{Never: never, Open: open, Home: home}), targetLookup: newTargetLookup()}
}

// Folder is the folder a run searches for an adopted entry, and whether the
// run's open folders read it already; ok is false when the run leaves it out.
// An entry that resolves elsewhere than its Target runs on its new folder
// only when the sync would adopt that unasked. It reads the disk.
func (c RunCheck) Folder(entry PathEntry) (folder string, open, ok bool) {
	d, rule := filterDir(entry.Dir, c.z)
	if rule != "" {
		return "", false, false
	}
	open = c.z.open(d.Target)
	switch {
	case open && !d.Shared:
		// What the sync would adopt unasked, moved or not.
		return d.Target, true, true
	case entry.Source == SourceUser && c.approved(entry.Target, d):
		// The user approved the folder itself, in whatever spelling.
		return d.Target, open, true
	}
	return "", false, false
}

// approved reports whether stored, a Target the user approved, is fresh's
// folder (sameFolder). A lookup of stored that fails leaves the entry out.
func (c RunCheck) approved(stored string, fresh pathDir) bool {
	same, err := c.sameFolder(stored, fresh)
	return same && err == nil
}
