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
)

// PathEntry is one folder of the user's PATH and what the sandbox does with
// it. A state holds for Target alone: the approval is of the folder the entry
// resolved to, not of its spelling.
type PathEntry struct {
	Dir    string    `json:"dir"`              // as the shell gave it
	Target string    `json:"target"`           // what Dir resolved to when the state was set
	State  PathState `json:"state"`            // adopted, pending or gone
	Source Source    `json:"source"`           // whose decision the state is
	Shared bool      `json:"shared,omitempty"` // Target was writable by a non-admin group at the last sync
}

// PathState is what the sandbox does with an entry.
type PathState string

const (
	// PathAdopted is on the sandbox's PATH and readable in it.
	PathAdopted PathState = "adopted"
	// PathPending is listed by the shell and waits for the user: adopting it
	// would add readable surface, or it is shared.
	PathPending PathState = "pending"
	// PathGone is removed by the user, and kept so a sync does not adopt it
	// again, whether or not the shell lists it.
	PathGone PathState = "gone"
)

// Source is whose decision an entry's state is.
type Source string

const (
	SourceShell Source = "shell" // the sync's
	SourceUser  Source = "user"  // Include's or Remove's
)

// checkPath refuses an entry whose dir or target is not absolute, whose dir
// an earlier entry has, or whose state or source is not one of the names
// above.
func checkPath(v *Settings) []Refusal {
	var refused []Refusal
	var kept []PathEntry
	for _, e := range v.Path {
		reason := ""
		switch {
		case !filepath.IsAbs(e.Dir) || !filepath.IsAbs(e.Target):
			reason = "is not an absolute folder"
		case slices.ContainsFunc(kept, func(k PathEntry) bool { return k.Dir == e.Dir }):
			reason = "is listed twice"
		case !slices.Contains([]PathState{PathAdopted, PathPending, PathGone}, e.State):
			reason = "has a state Kstack does not know"
		case !slices.Contains([]Source{SourceShell, SourceUser}, e.Source):
			reason = "has a source Kstack does not know"
		}
		if reason != "" {
			refused = append(refused, Refusal{Field: "path", Value: e.Dir, Reason: reason})
			continue
		}
		kept = append(kept, e)
	}
	v.Path = kept
	return refused
}
