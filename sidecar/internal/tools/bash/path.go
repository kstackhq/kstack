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
	"path/filepath"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
)

// emptyPath is the PATH of a run that searches no folder. An empty PATH would
// search the working directory, which a run can write, so it names a folder
// no run can make.
const emptyPath = "/nonexistent"

// runPath is the folders a run searches, in order, and the Read rules they
// need: one per folder open does not read already. list is the stored list as
// the run read it at its start, and only its adopted entries are searched. A
// list never resolved — before a first launch's shell answered — searches
// fallback instead, each folder as an entry the shell adopted, so the same
// checks hold. Each entry left out, or run on a folder other than its Target,
// gets one log line. It reads the disk.
func runPath(list settings.RunPath, fallback []string, open sandbox.FilePolicy, never []string, home string) (folders, reads []string) {
	entries := list.Entries
	if !list.Resolved {
		for _, dir := range fallback {
			entries = append(entries, settings.PathEntry{
				Dir: dir, Target: dir, State: settings.PathAdopted, Source: settings.SourceShell,
			})
		}
	}
	check := settings.NewRunCheck(open, never, home)
	for _, e := range entries {
		if e.State != settings.PathAdopted {
			continue
		}
		folder, isOpen, ok := check.Folder(e)
		switch {
		case !ok:
			slog.Info("PATH entry left out of a run", "dir", e.Dir, "target", e.Target)
			continue
		case slices.Contains(folders, folder):
			continue
		case folder != e.Target:
			slog.Info("PATH entry runs on the folder it resolves to now", "dir", e.Dir, "target", e.Target, "folder", folder)
		}
		folders = append(folders, folder)
		if !isOpen {
			reads = append(reads, folder)
		}
	}
	return folders, reads
}

// joinPath is folders as PATH, or emptyPath with none.
func joinPath(folders []string) string {
	if len(folders) == 0 {
		return emptyPath
	}
	return strings.Join(folders, string(filepath.ListSeparator))
}
