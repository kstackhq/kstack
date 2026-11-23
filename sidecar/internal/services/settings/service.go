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

	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
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
	targetLookup

	// snapMu guards snap, the zones as the folder checks read them.
	snapMu sync.Mutex
	snap   *snapshot
	// checkFolder is the package's checkFolder, or a test's stand-in.
	checkFolder func(path string, write bool, z zones, pathEntries []string, stored bool) error
	// syncing holds one sync at a time.
	syncing chan struct{}
}

// SyncTimeout bounds a sync's reading of the disk, as the login shell's run is
// bounded, so a stat on a dead network mount cannot hold a launch or a
// refresh.
const SyncTimeout = 5 * time.Second

// NewService is the store with the list's sync. zones is read at each sync;
// resolve runs the login shell for a refresh; fault is why the launch's
// resolution failed, "" when it answered.
func NewService(store *Store, zones func() Zones, resolve func(context.Context) ([]string, error), fault string) *Service {
	return &Service{Store: store, zones: zones, resolve: resolve, fault: fault, syncTimeout: SyncTimeout, targetLookup: newTargetLookup(), checkFolder: checkFolder, syncing: make(chan struct{}, 1)}
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
	ErrPathUnread    PathRefusal = "Kstack could not read that folder. Include it again."
	ErrPathBusy      PathRefusal = "Your PATH changed while Kstack checked that folder. Include it again."
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
	spelled map[string]string // spelledTargets over the stored list
	// err is set when a stored Target's lookup failed: the view is not
	// whole, and the sync writes nothing.
	err error
}

// SyncPath filters resolved and folds it into the stored list, and answers
// whether the list it wrote differs from the one it read. The disk is read on
// a goroutine abandoned when ctx ends or syncTimeout passes, since a stat on
// a dead network mount does not return; one that ends first changes nothing.
// A sync that could not compare a stored Target (sameFolder) changes nothing
// either, and answers the lookup's error. Syncs run one at a time, since each
// folds what it read of the stored list into the list it writes.
func (s *Service) SyncPath(ctx context.Context, resolved []string) (changed bool, err error) {
	if s.zones == nil {
		return false, ErrNoSandbox
	}
	ctx, cancel := context.WithTimeout(ctx, s.syncTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case s.syncing <- struct{}{}:
		defer func() { <-s.syncing }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	viewed := make(chan pathView, 1)
	go func() { viewed <- s.view(resolved) }()
	var v pathView
	select {
	case v = <-viewed:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if v.err != nil {
		return false, fmt.Errorf("compare a stored PATH target: %w", v.err)
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
	held := s.Held(FieldPath)
	var before, after []PathEntry
	err = s.Update(func(st *Settings) error {
		before = st.Sandbox.Path
		st.Sandbox.Path = diffPath(st.Sandbox.Path, v.fresh, v.spelled, held || st.Sandbox.PathStrict)
		st.Sandbox.PathResolved, st.Sandbox.PathStrict = true, false
		after = st.Sandbox.Path
		return nil
	}, FieldPath, FieldPathResolved, FieldPathStrict)
	if err != nil {
		return false, err
	}
	// A diagnostic: its stats run after the write, so one that hangs on a dead
	// mount never holds the sync.
	go logToolMoves(before, after, statTool)
	return !slices.Equal(before, after), nil
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
	if _, err := s.SyncPath(ctx, path); err != nil {
		return nil, err
	}
	return s.Get().Sandbox.Path, nil
}

// Path is the list, in the shell's order; nil on a machine with no sandbox.
func (s *Service) Path() []PathEntry {
	if s.zones == nil {
		return nil
	}
	return s.Get().Sandbox.Path
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
	return s.zones != nil && s.Get().Sandbox.PathResolved
}

// RunPath is the list a run starts from.
func (v Settings) RunPath() RunPath {
	return RunPath{Entries: v.Sandbox.Path, Resolved: v.Sandbox.PathResolved}
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
	// An approval names the folder the user was shown.
	if target == "" {
		return nil, ErrPathChanged
	}
	// Read before pathMu, so a lookup that hangs holds no sync. A sync can
	// file a removal meanwhile, so the read is redone until it compared
	// every removal the list holds.
	spelled, err := s.removalsOf(dir, target)
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	for read := 1; err == nil && !s.comparedAll(dir, target, spelled); read++ {
		if read == includeReads {
			return nil, ErrPathBusy
		}
		s.pathMu.Unlock()
		spelled, err = s.removalsOf(dir, target)
		s.pathMu.Lock()
	}
	if err != nil {
		return nil, err
	}
	if s.Held(FieldPath) {
		return nil, ErrPathHeld
	}
	return s.setState(dir, target, PathAdopted, ErrPathIncluded, false, spelled)
}

// includeReads is how many times an Include reads the removals for a list
// that keeps changing under it, before it is refused.
const includeReads = 3

// removalTargets is the Targets of the removals kept under Dirs other than
// dir that respell target.
func removalTargets(stored []PathEntry, dir, target string) []string {
	var targets []string
	for _, e := range stored {
		if e.Dir != dir && e.State == PathGone && respells(e.Target, target) && !slices.Contains(targets, e.Target) {
			targets = append(targets, e.Target)
		}
	}
	return targets
}

// removalsOf compares target with each removal's Target that respells it
// (removalTargets), as the list stands now, and answers, for each, whether
// it is target's folder. The entry's folder is the one target names with no
// link followed, so a target that became a link drops no removal of where it
// leads. The read is bounded by syncTimeout; one that fails or does not
// answer is ErrPathUnread.
func (s *Service) removalsOf(dir, target string) (map[string]bool, error) {
	stored := s.Get().Sandbox.Path
	// A request Include refuses reads no disk.
	if !s.includable(stored, dir, target) {
		return nil, nil
	}
	others := removalTargets(stored, dir, target)
	if len(others) == 0 {
		return nil, nil
	}
	type answer struct {
		same map[string]bool
		err  error
	}
	a, answered := readWithin(context.Background(), s.syncTimeout, func() answer {
		folder, found, err := s.lookUp(target)
		same := map[string]bool{}
		for _, other := range others {
			if err != nil {
				break
			}
			id, ok, failed := s.lookUp(other)
			same[other], err = found && ok && id.Same(folder), failed
		}
		return answer{same: same, err: err}
	})
	if !answered || a.err != nil {
		return nil, ErrPathUnread
	}
	return a.same, nil
}

// includable reports whether an Include of dir for target, off stored, is
// one AdoptPath would write: the field is not held, and the entry is listed
// with target and not adopted.
func (s *Service) includable(stored []PathEntry, dir, target string) bool {
	return !s.Held(FieldPath) && slices.ContainsFunc(stored, func(e PathEntry) bool {
		return e.Dir == dir && e.Target == target && e.State != PathAdopted
	})
}

// comparedAll reports whether compared, removalsOf's answer, holds every
// removal the list holds now. A request Include refuses has none to
// compare.
func (s *Service) comparedAll(dir, target string, compared map[string]bool) bool {
	stored := s.Get().Sandbox.Path
	if !s.includable(stored, dir, target) {
		return true
	}
	for _, other := range removalTargets(stored, dir, target) {
		if _, ok := compared[other]; !ok {
			return false
		}
	}
	return true
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
	return s.setState(dir, "", PathGone, ErrPathRemoved, s.Held(FieldPath), nil)
}

// setState sets dir's entry to state, by the user, or refuses an entry not
// listed, one whose Target is not target when target is set, or one already
// there with already. Adopting drops the removals kept for the entry's folder
// under other Dirs: those of its Target, and those whose Target is in
// spelled, judged off the Update. strict marks the next sync strict.
func (s *Service) setState(dir, target string, state PathState, already error, strict bool, spelled map[string]bool) ([]PathEntry, error) {
	err := s.Update(func(v *Settings) error {
		i := slices.IndexFunc(v.Sandbox.Path, func(e PathEntry) bool { return e.Dir == dir })
		switch {
		case i < 0:
			return ErrPathNotListed
		case target != "" && v.Sandbox.Path[i].Target != target:
			return ErrPathChanged
		case v.Sandbox.Path[i].State == state:
			return already
		}
		v.Sandbox.Path[i].State, v.Sandbox.Path[i].Source = state, SourceUser
		if state == PathAdopted {
			// A removal holds for its folder under any Dir, so one kept
			// under another Dir would remove this entry at the next sync.
			folder := v.Sandbox.Path[i].Target
			v.Sandbox.Path = slices.DeleteFunc(v.Sandbox.Path, func(e PathEntry) bool {
				return e.Dir != dir && (e.Target == folder || spelled[e.Target]) && e.State == PathGone
			})
		}
		v.Sandbox.PathStrict = v.Sandbox.PathStrict || strict
		return nil
	}, FieldPath, FieldPathStrict)
	if err != nil {
		return nil, err
	}
	return s.Get().Sandbox.Path, nil
}

// view reads the disk for a sync: the filter over resolved, whether each kept
// folder is open, and the snapshot the folder checks read until the next.
func (s *Service) view(resolved []string) pathView {
	snap := takeSnapshot(s.zones(), resolved)
	z := zonesOf(snap.zones)
	kept, dropped := filterPath(resolved, z)
	v := pathView{dropped: dropped, snap: snap}
	for _, d := range kept {
		v.fresh = append(v.fresh, freshDir{pathDir: d, Open: z.open(d.Target)})
	}
	v.spelled, v.err = s.spelledTargets(s.Get().Sandbox.Path, v.fresh)
	return v
}

// spelledTargets maps each stored Target that is a fresh folder in another
// spelling (targetLookup) to that folder's fresh Target. Every entry is
// compared, whatever its state: a removal holds for the folder under any
// Dir, and an entry read here can be removed before the sync writes. Each
// Target is read once, and only one that respells a fresh folder's. err is a
// lookup's failure.
func (s *Service) spelledTargets(stored []PathEntry, fresh []freshDir) (map[string]string, error) {
	spelled := map[string]string{}
	read := map[string]bool{}
	for _, e := range stored {
		respelled := func(d freshDir) bool { return respells(e.Target, d.Target) }
		if read[e.Target] || !slices.ContainsFunc(fresh, respelled) {
			continue
		}
		read[e.Target] = true
		id, found, err := s.lookUp(e.Target)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(fresh, func(d freshDir) bool { return found && respelled(d) && id.Is(d.info) }); i >= 0 {
			spelled[e.Target] = fresh[i].Target
		}
	}
	return spelled, nil
}

// diffPath folds fresh, in the shell's order, into stored. spelled maps a
// stored Target to the fresh spelling of its folder, which a listed entry is
// stored in. A removal the shell no longer lists keeps its own Target: the
// fresh folder can be a mount that goes away. held files every new entry
// pending.
func diffPath(stored []PathEntry, fresh []freshDir, spelled map[string]string, held bool) []PathEntry {
	targetOf := func(e PathEntry) string {
		if t, ok := spelled[e.Target]; ok {
			return t
		}
		return e.Target
	}
	removed := map[string]bool{}
	for _, e := range stored {
		if e.State == PathGone {
			removed[targetOf(e)] = true
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
		case i >= 0 && targetOf(stored[i]) == d.Target:
			e := stored[i]
			e.Target, e.Shared = d.Target, d.Shared
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
