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
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
)

// Zones is what the sync judges an entry, and a grant a folder, by: Never,
// the paths no rule opens; Open, what every run reads, whose Read paths are
// the open folders and whose Deny paths close what lies under them again;
// Home, which with its app-data folders no entry may be or hold; and NoWrite,
// what something outside the sandbox runs, which no grant may write.
type Zones struct {
	Never   []string
	Open    sandbox.FilePolicy
	Home    string
	NoWrite []string
}

// Service is the store, and the frozen PATH kept in it.
type Service struct {
	*Store
	// zones and resolve are nil on a machine with no sandbox.
	zones   func() Zones
	resolve func(context.Context) ([]string, error)

	// pathMu serializes the list's writes, and guards fault.
	pathMu sync.Mutex
	// fault is why the last resolution failed, "" once one answers.
	fault string
	// syncTimeout bounds a sync's reading of the disk: SyncTimeout, or a
	// test's shorter one.
	syncTimeout time.Duration

	// snapMu guards snap, the zones as the folder checks read them.
	snapMu sync.Mutex
	snap   *snapshot
	// checkFolder is the package's checkFolder, or a test's stand-in.
	checkFolder func(path string, write bool, z zones, pathEntries []string, stored bool) error
}

// SyncTimeout bounds a sync's reading of the disk, as the login shell's run is
// bounded, so a stat on a dead network mount cannot hold a launch or a
// refresh.
const SyncTimeout = 5 * time.Second

// NewService is the store with the list's sync. zones is read at each sync;
// resolve runs the login shell for a refresh; fault is why the launch's
// resolution failed, "" when it answered.
func NewService(store *Store, zones func() Zones, resolve func(context.Context) ([]string, error), fault string) *Service {
	return &Service{Store: store, zones: zones, resolve: resolve, fault: fault, syncTimeout: SyncTimeout, checkFolder: checkFolder}
}

// A PathRefusal is a change to the list the user asked for and cannot have,
// in their words.
type PathRefusal string

func (r PathRefusal) Error() string { return string(r) }

const (
	ErrPathNotListed PathRefusal = "That folder is not in the list."
	ErrPathIncluded  PathRefusal = "That folder is already included."
	ErrPathRemoved   PathRefusal = "That folder is already removed."
	ErrPathHeld      PathRefusal = "Your PATH settings hold an entry Kstack cannot read. Refresh PATH first."
	ErrPathChanged   PathRefusal = "That folder now leads somewhere else. Check it, then include it again."
	ErrNoSandbox     PathRefusal = "This machine has no sandbox."
)

// trackedTools are the programs whose first adopted folder a sync logs a
// move of, since a startup file that puts a folder first chooses which runs.
var trackedTools = []string{"kubectl", "helm"}

// freshDir is a kept entry with what the sync reads of its folder.
type freshDir struct {
	pathDir
	Open bool // adopting it adds no readable surface
}

// pathView is what one sync read off the disk, and the snapshot it took.
type pathView struct {
	fresh   []freshDir
	dropped map[string]int
	snap    *snapshot
}

// SyncPath filters resolved and folds it into the stored list. The disk is
// read on a goroutine abandoned when ctx ends or syncTimeout passes, since a
// stat on a dead network mount does not return; one that ends first changes
// nothing.
func (s *Service) SyncPath(ctx context.Context, resolved []string) error {
	if s.zones == nil {
		return ErrNoSandbox
	}
	ctx, cancel := context.WithTimeout(ctx, s.syncTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	viewed := make(chan pathView, 1)
	go func() { viewed <- s.view(resolved) }()
	var v pathView
	select {
	case v = <-viewed:
	case <-ctx.Done():
		return ctx.Err()
	}
	if len(v.dropped) > 0 {
		slog.Info("PATH entries left out", "rules", countsOf(v.dropped))
	}
	s.snapMu.Lock()
	s.snap = v.snap
	s.snapMu.Unlock()

	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	// Read before the Update, which holds the store's lock while fn runs.
	held := s.Held("path")
	var before, after []PathEntry
	err := s.Update(func(st *Settings) error {
		before = st.Path
		st.Path = diffPath(st.Path, v.fresh, held || st.PathStrict)
		st.PathResolved, st.PathStrict = true, false
		after = st.Path
		return nil
	}, "path", "pathResolved", "pathStrict")
	if err != nil {
		return err
	}
	// A diagnostic: its stats run after the write, so one that hangs on a dead
	// mount never holds the sync.
	go logToolMoves(before, after, statTool)
	return nil
}

// RefreshPath is Refresh PATH: the login shell run again, then a sync. A
// resolution that fails changes nothing and answers its reason. It answers
// the whole list.
func (s *Service) RefreshPath(ctx context.Context) ([]PathEntry, error) {
	if s.resolve == nil {
		return nil, ErrNoSandbox
	}
	path, err := s.resolve(ctx)
	s.pathMu.Lock()
	if err != nil {
		s.fault = err.Error()
	} else {
		s.fault = ""
	}
	s.pathMu.Unlock()
	if err != nil {
		return nil, PathRefusal("Your shell did not answer: " + err.Error() + ".")
	}
	if err := s.SyncPath(ctx, path); err != nil {
		return nil, err
	}
	return s.Get().Path, nil
}

// Path is the list, in the shell's order; nil on a machine with no sandbox.
func (s *Service) Path() []PathEntry {
	if s.zones == nil {
		return nil
	}
	return s.Get().Path
}

// RunPath is the list as a run reads it: the entries, and whether a sync has
// ever written them.
type RunPath struct {
	Entries []PathEntry
	// Resolved is false only before the first sync, the one case a run
	// searches the platform's default instead: an empty resolved list
	// searches nothing.
	Resolved bool
}

// PathResolved reports whether a sync has written the list; false on a
// machine with no sandbox.
func (s *Service) PathResolved() bool {
	return s.zones != nil && s.Get().PathResolved
}

// RunPath is the list a run starts from.
func (v Settings) RunPath() RunPath {
	return RunPath{Entries: v.Path, Resolved: v.PathResolved}
}

// PathFault is why the last read of the login shell failed, at launch or on
// refresh, "" once one answers.
func (s *Service) PathFault() string {
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	return s.fault
}

// AdoptPath is Include: a pending or removed entry becomes adopted, by the
// user, for target, the folder the user was shown, which a refresh may have
// moved since: the approval is of the folder, so a stale one is refused. It
// widens, so it waits for the sync that ends a hold. It answers the whole
// list.
func (s *Service) AdoptPath(dir, target string) ([]PathEntry, error) {
	if s.zones == nil {
		return nil, ErrNoSandbox
	}
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	if s.Held("path") {
		return nil, ErrPathHeld
	}
	return s.setState(dir, target, PathAdopted, ErrPathIncluded, false)
}

// DropPath is Remove: an adopted or pending entry becomes removed, by the
// user. It narrows, so it writes while the store holds the field, which ends
// the hold; PathStrict keeps the next sync strict, across a restart too. It
// answers the whole list.
func (s *Service) DropPath(dir string) ([]PathEntry, error) {
	if s.zones == nil {
		return nil, ErrNoSandbox
	}
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	// Remove narrows, so it holds for whatever the entry leads to.
	return s.setState(dir, "", PathGone, ErrPathRemoved, s.Held("path"))
}

// setState sets dir's entry to state, by the user, or refuses an entry not
// listed, one whose Target is not target when target is set, or one already
// there with already. strict marks the next sync strict.
func (s *Service) setState(dir, target string, state PathState, already error, strict bool) ([]PathEntry, error) {
	err := s.Update(func(v *Settings) error {
		i := slices.IndexFunc(v.Path, func(e PathEntry) bool { return e.Dir == dir })
		switch {
		case i < 0:
			return ErrPathNotListed
		case target != "" && v.Path[i].Target != target:
			return ErrPathChanged
		case v.Path[i].State == state:
			return already
		}
		v.Path[i].State, v.Path[i].Source = state, SourceUser
		v.PathStrict = v.PathStrict || strict
		return nil
	}, "path", "pathStrict")
	if err != nil {
		return nil, err
	}
	return s.Get().Path, nil
}

// view reads the disk for a sync: the filter over resolved, whether each kept
// folder is open, and the snapshot the folder checks read until the next.
func (s *Service) view(resolved []string) pathView {
	snap := takeSnapshot(s.zones(), resolved)
	z := resolveZones(snap.zones)
	kept, dropped := filterPath(resolved, z)
	v := pathView{dropped: dropped, snap: snap}
	for _, d := range kept {
		v.fresh = append(v.fresh, freshDir{pathDir: d, Open: z.open(d.Target)})
	}
	return v
}

// diffPath folds fresh, in the shell's order, into stored. held files every
// new entry pending.
func diffPath(stored []PathEntry, fresh []freshDir, held bool) []PathEntry {
	removed := map[string]bool{}
	for _, e := range stored {
		if e.State == PathGone {
			removed[e.Target] = true
		}
	}
	var out []PathEntry
	listed := map[string]bool{}
	for _, d := range fresh {
		listed[d.Dir] = true
		i := slices.IndexFunc(stored, func(e PathEntry) bool { return e.Dir == d.Dir })
		switch {
		case i >= 0 && stored[i].State == PathGone:
			// The removal holds for whatever the entry resolves to now.
			e := stored[i]
			e.Target, e.Shared = d.Target, d.Shared
			out = append(out, e)
		case removed[d.Target]:
			// Another spelling of a removed folder: the removal is of the
			// folder, so it holds here too.
			out = append(out, PathEntry{Dir: d.Dir, Target: d.Target, State: PathGone, Source: SourceUser, Shared: d.Shared})
		case i >= 0 && stored[i].Target == d.Target:
			e := stored[i]
			e.Shared = d.Shared
			if e.State == PathAdopted && e.Source == SourceShell && (d.Shared || !d.Open) {
				e.State = PathPending
			}
			out = append(out, e)
		default:
			// New, or moved: filed again for the folder it resolves to now.
			e := PathEntry{Dir: d.Dir, Target: d.Target, State: PathPending, Source: SourceShell, Shared: d.Shared}
			if !held && d.Open && !d.Shared {
				e.State = PathAdopted
			}
			out = append(out, e)
		}
	}
	for _, e := range stored {
		// The removal outlives the entry's absence; anything else the shell
		// no longer lists is dropped.
		if !listed[e.Dir] && e.State == PathGone {
			out = append(out, e)
		}
	}
	return out
}

// statTool is os.Stat, or a test's stand-in.
var statTool = os.Stat

// logToolMoves logs each tracked tool whose first adopted folder differs
// between before and after, probing with stat. Only adopted folders are
// probed: a removed or waiting one finds nothing a run would run.
func logToolMoves(before, after []PathEntry, stat func(string) (os.FileInfo, error)) {
	holds := map[string]bool{}
	first := func(entries []PathEntry, tool string) string {
		for _, e := range entries {
			if e.State != PathAdopted {
				continue
			}
			key := filepath.Join(e.Target, tool)
			found, seen := holds[key]
			if !seen {
				info, err := stat(key)
				found = err == nil && !info.IsDir()
				holds[key] = found
			}
			if found {
				return e.Target
			}
		}
		return ""
	}
	for _, tool := range trackedTools {
		if from, to := first(before, tool), first(after, tool); from != to {
			slog.Info("PATH moved a tool", "tool", tool, "from", from, "to", to)
		}
	}
}

// countsOf is counts as one string, by key: the log renders no map.
func countsOf(counts map[string]int) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, ",")
}
