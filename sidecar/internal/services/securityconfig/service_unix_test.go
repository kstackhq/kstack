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

package securityconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

// zoneFixture is a machine in miniature: open is what every run reads, with
// a closed folder inside it, home is outside it, and never is denied always.
type zoneFixture struct {
	base, open, closed, home, never string
	zones                           Zones
}

func newZones(t *testing.T) *zoneFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	z := &zoneFixture{
		base:   base,
		open:   dirWithMode(t, base, "open", 0o755),
		closed: dirWithMode(t, base, "open/closed", 0o755),
		home:   dirWithMode(t, base, "home", 0o755),
		never:  dirWithMode(t, base, "never", 0o755),
	}
	z.zones = Zones{Never: []string{z.never}, Open: sandbox.FilePolicy{Read: []string{z.open}, Deny: []string{z.closed}}}
	return z
}

// dir makes a folder under the fixture's base and answers its path.
func (z *zoneFixture) dir(t *testing.T, rel string) string {
	t.Helper()
	return dirWithMode(t, z.base, rel, 0o755)
}

// newTestService is a Service over a fresh file, judging by z.
// synced syncs resolved into s, which must take it.
func synced(t *testing.T, s *Service, resolved []string) {
	t.Helper()
	_, err := s.SyncPath(t.Context(), resolved)
	require.NoError(t, err)
}

func newTestService(t *testing.T, z *zoneFixture, resolve func(context.Context) ([]string, error)) *Service {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	return NewService(store, func() Zones { return z.zones }, resolve, "")
}

// statesOf is each entry's dir and state, in order.
func statesOf(entries []PathEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Dir+"="+string(e.State)+"/"+string(e.Source))
	}
	return out
}

func TestSyncPathDiffsFourWays(t *testing.T) {
	fakeGroups(t, "staff")
	z := newZones(t)
	s := newTestService(t, z, nil)
	gone1, gone2 := z.dir(t, "open/gone"), z.dir(t, "home/gone")
	kept := z.dir(t, "open/kept")
	synced(t, s, []string{gone1, kept})
	require.NoError(t, s.Update(func(v *Settings) error {
		v.Path = append(v.Path, PathEntry{Dir: gone2, Target: gone2, State: PathPending, Source: SourceShell})
		return nil
	}, "path"))

	fresh := z.dir(t, "open/fresh")
	inHome := z.dir(t, "home/bin")
	inClosed := z.dir(t, "open/closed/bin")
	outside := z.dir(t, "outside/bin")
	sharedDir := dirWithMode(t, z.open, "shared", 0o775)
	synced(t, s, []string{fresh, inHome, kept, inClosed, outside, sharedDir})

	assert.Equal(t, []string{
		fresh + "=adopted/shell", inHome + "=pending/shell", kept + "=adopted/shell",
		inClosed + "=pending/shell", outside + "=pending/shell", sharedDir + "=pending/shell",
	}, statesOf(s.Get().Path))
	assert.True(t, s.Get().Path[5].Shared)

	// A reorder keeps every state, and a kubectl that moves says so once.
	require.NoError(t, os.WriteFile(filepath.Join(kept, "kubectl"), nil, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fresh, "kubectl"), nil, 0o755))
	log := testutil.CaptureLogs(t)
	synced(t, s, []string{sharedDir, outside, kept, inClosed, inHome, fresh})
	assert.Equal(t, []string{
		sharedDir + "=pending/shell", outside + "=pending/shell", kept + "=adopted/shell",
		inClosed + "=pending/shell", inHome + "=pending/shell", fresh + "=adopted/shell",
	}, statesOf(s.Get().Path))
	// The probes run after the write, on a goroutine of their own, and an
	// earlier sync's may log as well, so the test waits for this move.
	move := `"tool":"kubectl","from":"` + fresh + `","to":"` + kept + `"`
	require.Eventually(t, func() bool { return strings.Contains(log.String(), move) }, testutil.Timeout, time.Millisecond)
	assert.Equal(t, 1, strings.Count(log.String(), move), "said once")
}

// setEntry writes e over the stored entry of its dir, or appends it.
func setEntry(t *testing.T, s *Service, e PathEntry) {
	t.Helper()
	require.NoError(t, s.Update(func(v *Settings) error {
		for i := range v.Path {
			if v.Path[i].Dir == e.Dir {
				v.Path[i] = e
				return nil
			}
		}
		v.Path = append(v.Path, e)
		return nil
	}, "path"))
}

// entryOf is the stored entry of dir.
func entryOf(t *testing.T, s *Service, dir string) PathEntry {
	t.Helper()
	for _, e := range s.Get().Path {
		if e.Dir == dir {
			return e
		}
	}
	t.Fatalf("no entry for %s", dir)
	return PathEntry{}
}

func TestAnAdoptedEntryThatBecameSharedWaits(t *testing.T) {
	fakeGroups(t, "staff")
	z := newZones(t)
	s := newTestService(t, z, nil)
	byShell, byUser := z.dir(t, "open/shell"), z.dir(t, "open/user")
	synced(t, s, []string{byShell, byUser})
	setEntry(t, s, PathEntry{Dir: byUser, Target: byUser, State: PathAdopted, Source: SourceUser})

	require.NoError(t, os.Chmod(byShell, 0o775))
	require.NoError(t, os.Chmod(byUser, 0o775))
	synced(t, s, []string{byShell, byUser})

	assert.Equal(t, PathEntry{Dir: byShell, Target: byShell, State: PathPending, Source: SourceShell, Shared: true}, entryOf(t, s, byShell))
	assert.Equal(t, PathAdopted, entryOf(t, s, byUser).State)
}

func TestAnAdoptedEntryNoLongerOpenWaits(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	byShell, byUser := z.dir(t, "open/tools/shell"), z.dir(t, "open/tools/user")
	synced(t, s, []string{byShell, byUser})
	setEntry(t, s, PathEntry{Dir: byUser, Target: byUser, State: PathAdopted, Source: SourceUser})

	z.zones.Open.Deny = append(z.zones.Open.Deny, filepath.Join(z.open, "tools"))
	synced(t, s, []string{byShell, byUser})

	assert.Equal(t, PathPending, entryOf(t, s, byShell).State)
	assert.Equal(t, PathAdopted, entryOf(t, s, byUser).State)
}

// The home, and a folder that holds it, never reach the list: one Include
// would open all of the home. A folder inside it waits as usual.
func TestSyncPathDropsABroadEntry(t *testing.T) {
	z := newZones(t)
	z.zones.Home = z.home
	s := newTestService(t, z, nil)
	inHome := z.dir(t, "home/bin")
	synced(t, s, []string{z.home, z.base, inHome})

	assert.Equal(t, []PathEntry{{Dir: inHome, Target: inHome, State: PathPending, Source: SourceShell}}, s.Get().Path)
}

func TestARemovalOutlivesTheEntrysAbsence(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir, other := z.dir(t, "open/bin"), z.dir(t, "open/other")
	synced(t, s, []string{dir, other})
	setEntry(t, s, PathEntry{Dir: dir, Target: dir, State: PathGone, Source: SourceUser})

	synced(t, s, []string{other})
	assert.Equal(t, []string{other + "=adopted/shell", dir + "=gone/user"}, statesOf(s.Get().Path))

	synced(t, s, []string{dir, other})
	assert.Equal(t, PathEntry{Dir: dir, Target: dir, State: PathGone, Source: SourceUser}, entryOf(t, s, dir))
}

func TestARemovalHoldsForEverySpellingOfTheFolder(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir := z.dir(t, "open/bin")
	link := filepath.Join(z.base, "bin")
	require.NoError(t, os.Symlink(dir, link))
	synced(t, s, []string{dir})
	_, err := s.DropPath(dir)
	require.NoError(t, err)

	// A trailing slash, alone, then a link to the folder ahead of the spelling
	// removed, which the filter drops as a duplicate.
	synced(t, s, []string{dir + "/"})
	assert.Equal(t, []string{dir + "/=gone/user", dir + "=gone/user"}, statesOf(s.Get().Path))
	synced(t, s, []string{link, dir})
	assert.Equal(t, []string{link + "=gone/user", dir + "/=gone/user", dir + "=gone/user"}, statesOf(s.Get().Path))
}

func TestSyncPathRefilesAMovedEntry(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	first, second, inHome := z.dir(t, "open/v1"), z.dir(t, "open/v2"), z.dir(t, "home/v3")
	removedFirst, removedNow := z.dir(t, "open/w1"), z.dir(t, "home/w2")
	link, goneLink := filepath.Join(z.base, "current"), filepath.Join(z.base, "removed")
	repoint := func(link, target string) {
		_ = os.Remove(link)
		require.NoError(t, os.Symlink(target, link))
	}
	repoint(link, first)
	repoint(goneLink, removedFirst)
	synced(t, s, []string{link, goneLink})
	setEntry(t, s, PathEntry{Dir: goneLink, Target: removedFirst, State: PathGone, Source: SourceUser})

	repoint(link, second)
	synced(t, s, []string{link, goneLink})
	assert.Equal(t, PathEntry{Dir: link, Target: second, State: PathAdopted, Source: SourceShell}, entryOf(t, s, link))

	repoint(link, inHome)
	repoint(goneLink, removedNow)
	synced(t, s, []string{link, goneLink})
	assert.Equal(t, PathEntry{Dir: link, Target: inHome, State: PathPending, Source: SourceShell}, entryOf(t, s, link))
	assert.Equal(t, PathEntry{Dir: goneLink, Target: removedNow, State: PathGone, Source: SourceUser}, entryOf(t, s, goneLink))
}

func TestAdoptAndDropMoveOneEntry(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	pending, gone, adopted := z.dir(t, "home/pending"), z.dir(t, "home/gone"), z.dir(t, "open/adopted")
	link := filepath.Join(z.base, "link")
	require.NoError(t, os.Symlink(pending, link))
	synced(t, s, []string{link, gone, adopted})
	setEntry(t, s, PathEntry{Dir: gone, Target: gone, State: PathGone, Source: SourceUser})

	// Include approves the folder the user was shown, not the one the link
	// leads to now.
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(z.home, link))
	entries, err := s.AdoptPath(link, entryOf(t, s, link).Target)
	require.NoError(t, err)
	assert.Equal(t, PathEntry{Dir: link, Target: pending, State: PathAdopted, Source: SourceUser}, entryOf(t, s, link))
	assert.Equal(t, s.Get().Path, entries)
	_, err = s.AdoptPath(gone, entryOf(t, s, gone).Target)
	require.NoError(t, err)
	assert.Equal(t, PathAdopted, entryOf(t, s, gone).State)

	_, err = s.DropPath(adopted)
	require.NoError(t, err)
	assert.Equal(t, PathEntry{Dir: adopted, Target: adopted, State: PathGone, Source: SourceUser}, entryOf(t, s, adopted))
	_, err = s.DropPath(link)
	require.NoError(t, err)
	assert.Equal(t, PathGone, entryOf(t, s, link).State)

	for _, tc := range []struct {
		do   func(string) ([]PathEntry, error)
		dir  string
		want error
	}{
		{func(dir string) ([]PathEntry, error) { return s.AdoptPath(dir, dir) }, filepath.Join(z.base, "unlisted"), ErrPathNotListed},
		{func(dir string) ([]PathEntry, error) { return s.AdoptPath(dir, dir) }, gone, ErrPathIncluded},
		{s.DropPath, filepath.Join(z.base, "unlisted"), ErrPathNotListed},
		{s.DropPath, adopted, ErrPathRemoved},
	} {
		before := s.Get().Path
		_, err := tc.do(tc.dir)
		assert.ErrorIs(t, err, tc.want)
		assert.Equal(t, before, s.Get().Path)
	}

	// A gone entry survives the next sync.
	synced(t, s, []string{adopted})
	assert.Equal(t, PathGone, entryOf(t, s, adopted).State)
}

// heldService is a Service over a file whose path field holds an entry the
// store refuses beside kept, adopted.
func heldService(t *testing.T, z *zoneFixture, kept string) (*Service, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "security.json")
	raw := `{"path": [{"dir": "` + kept + `", "target": "` + kept + `", "state": "adopted", "source": "shell"},
		{"dir": "bin", "target": "bin", "state": "gone", "source": "user"}], "count": 3}`
	require.NoError(t, os.WriteFile(file, []byte(raw), 0o600))
	store, err := Open(file)
	require.NoError(t, err)
	require.True(t, store.Held("path"))
	return NewService(store, func() Zones { return z.zones }, nil, ""), file
}

func TestARefusedEntryHoldsTheSync(t *testing.T) {
	z := newZones(t)
	kept, fresh, later := z.dir(t, "open/kept"), z.dir(t, "open/fresh"), z.dir(t, "open/later")
	s, file := heldService(t, z, kept)

	_, err := s.AdoptPath(kept, entryOf(t, s, kept).Target)
	assert.ErrorIs(t, err, ErrPathHeld)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"dir": "bin"`, "a refused Include writes nothing")

	synced(t, s, []string{kept, fresh})
	assert.Equal(t, []string{kept + "=adopted/shell", fresh + "=pending/shell"}, statesOf(s.Get().Path))
	assert.False(t, s.Held("path"), "the sync's write ends the hold")
	reopened, err := Open(file)
	require.NoError(t, err)
	assert.Empty(t, reopened.Refused(), "the file is readable again")

	synced(t, s, []string{kept, fresh, later})
	assert.Equal(t, PathAdopted, entryOf(t, s, later).State)
}

func TestARemovalWhileHeldKeepsTheSyncStrict(t *testing.T) {
	z := newZones(t)
	kept, fresh := z.dir(t, "open/kept"), z.dir(t, "open/fresh")
	s, _ := heldService(t, z, kept)

	_, err := s.DropPath(kept)
	require.NoError(t, err)
	assert.False(t, s.Held("path"), "Remove writes and ends the store's hold")

	synced(t, s, []string{kept, fresh})
	assert.Equal(t, PathPending, entryOf(t, s, fresh).State, "the next sync is still strict")

	other := z.dir(t, "open/other")
	synced(t, s, []string{kept, fresh, other})
	assert.Equal(t, PathAdopted, entryOf(t, s, other).State, "the one after adopts")
}

func TestAFailedResolutionKeepsTheList(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "open/bin")
	s := newTestService(t, z, func(context.Context) ([]string, error) { return nil, errors.New("timeout") })
	synced(t, s, []string{dir})
	before := s.Get().Path

	_, err := s.RefreshPath(t.Context())
	var refusal PathRefusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "Your shell did not answer: timeout.", err.Error())
	assert.Equal(t, before, s.Get().Path)
}

func TestTheLastFaultIsKept(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "open/bin")
	answer := errors.New("exit")
	store, err := Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	s := NewService(store, func() Zones { return z.zones }, func(context.Context) ([]string, error) {
		if answer != nil {
			return nil, answer
		}
		return []string{dir}, nil
	}, "timeout")
	assert.Equal(t, "timeout", s.PathFault(), "the launch's fault until a refresh answers")

	_, err = s.RefreshPath(t.Context())
	require.Error(t, err)
	assert.Equal(t, "exit", s.PathFault(), "a refresh's fault replaces it")

	answer = nil
	entries, err := s.RefreshPath(t.Context())
	require.NoError(t, err)
	assert.Empty(t, s.PathFault())
	assert.Equal(t, []string{dir + "=adopted/shell"}, statesOf(entries))

	// A sync whose context ends first changes nothing.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.SyncPath(ctx, []string{z.dir(t, "open/other")})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{dir + "=adopted/shell"}, statesOf(s.Get().Path))
}

// What the filter left out is counted on one log line, never by entry.
func TestASyncLogsWhatItLeftOut(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	log := testutil.CaptureLogs(t)

	synced(t, s, []string{"", "bin", z.dir(t, "open/bin")})
	assert.Contains(t, log.String(), `"rules":"empty=1,relative=1"`)
	assert.Equal(t, s.Get().Path, s.Path())
}

// Include approves the folder the user was shown: one a refresh has moved
// since is refused, and nothing is written.
func TestIncludeApprovesTheTargetShown(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	first, second := z.dir(t, "home/v1"), z.dir(t, "home/v2")
	link := filepath.Join(z.base, "current")
	require.NoError(t, os.Symlink(first, link))
	synced(t, s, []string{link})
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(second, link))
	synced(t, s, []string{link})
	before := s.Get().Path

	_, err := s.AdoptPath(link, first)
	assert.ErrorIs(t, err, ErrPathChanged)
	assert.Equal(t, before, s.Get().Path)

	_, err = s.AdoptPath(link, second)
	require.NoError(t, err)
	assert.Equal(t, PathEntry{Dir: link, Target: second, State: PathAdopted, Source: SourceUser}, entryOf(t, s, link))
}

// Shared follows the folder on every sync, whatever the entry's state, so a
// waiting entry warns before Include and stops warning once it is safe.
func TestSharedFollowsAnUnmovedFolder(t *testing.T) {
	fakeGroups(t, "staff")
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir := z.dir(t, "home/bin")
	synced(t, s, []string{dir})
	require.False(t, entryOf(t, s, dir).Shared)

	require.NoError(t, os.Chmod(dir, 0o775))
	synced(t, s, []string{dir})
	assert.Equal(t, PathEntry{Dir: dir, Target: dir, State: PathPending, Source: SourceShell, Shared: true}, entryOf(t, s, dir))

	require.NoError(t, os.Chmod(dir, 0o755))
	synced(t, s, []string{dir})
	assert.False(t, entryOf(t, s, dir).Shared)
}

// A sync's reading of the disk is bounded, so a refresh over a dead network
// mount answers; it changes nothing.
func TestARefreshIsBounded(t *testing.T) {
	z := newZones(t)
	dir := z.dir(t, "open/bin")
	store, err := Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := NewService(store, func() Zones {
		<-release
		return z.zones
	}, func(context.Context) ([]string, error) { return []string{dir}, nil }, "")
	s.syncTimeout = time.Millisecond

	_, err = s.RefreshPath(t.Context())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, s.Get().Path)
}

// A sync marks the list resolved, even one it leaves empty, so a run never
// mistakes a filtered-out list for one never read.
func TestASyncMarksTheListResolved(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	assert.False(t, s.Get().RunPath().Resolved)

	synced(t, s, []string{"", "bin"})
	assert.Equal(t, RunPath{Resolved: true}, s.Get().RunPath())
}

// The strictness a removal leaves behind is stored, so a restart before the
// next sync keeps it: that sync files a new open entry pending.
func TestARemovalWhileHeldStaysStrictAcrossARestart(t *testing.T) {
	z := newZones(t)
	kept, fresh := z.dir(t, "open/kept"), z.dir(t, "open/fresh")
	s, file := heldService(t, z, kept)
	_, err := s.DropPath(kept)
	require.NoError(t, err)

	store, err := Open(file)
	require.NoError(t, err)
	s = NewService(store, func() Zones { return z.zones }, nil, "")
	synced(t, s, []string{kept, fresh})
	assert.Equal(t, PathPending, entryOf(t, s, fresh).State)
	assert.False(t, s.Get().PathStrict, "the sync consumes it")
}

// A hand edit the store refuses leaves both marks at their safe value: the
// list resolved, so no default stands in, and the next sync strict.
func TestRefusedPathMarksAnswerTheirSafeValue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "security.json")
	for _, raw := range []string{`{"pathResolved": "no", "pathStrict": 0}`, `{"pathResolved": null, "pathStrict": null}`} {
		require.NoError(t, os.WriteFile(file, []byte(raw), 0o600))
		store, err := Open(file)
		require.NoError(t, err)
		assert.True(t, store.Get().PathResolved, raw)
		assert.True(t, store.Get().PathStrict, raw)
	}
}

// pathCase is one combination of the list's state: the one entry's, the
// folder it leads to now, what the shell lists, and the stored marks.
type pathCase struct {
	state        PathState // "" for no stored entry
	source       Source
	moved        bool // the entry's link leads elsewhere than its stored Target
	open, shared bool // of the folder it leads to now
	listed       bool // the shell lists it
	held, strict bool
	action       string
}

func (c pathCase) String() string {
	return fmt.Sprintf("%s/%s moved=%t open=%t shared=%t listed=%t held=%t strict=%t %s",
		c.state, c.source, c.moved, c.open, c.shared, c.listed, c.held, c.strict, c.action)
}

// pathCases is every combination worth telling apart.
func pathCases() []pathCase {
	var cases []pathCase
	entries := []pathCase{{}}
	for _, state := range []PathState{PathAdopted, PathPending, PathGone} {
		for _, source := range []Source{SourceShell, SourceUser} {
			for _, moved := range []bool{false, true} {
				entries = append(entries, pathCase{state: state, source: source, moved: moved})
			}
		}
	}
	for _, e := range entries {
		for _, open := range []bool{false, true} {
			for _, shared := range []bool{false, true} {
				for _, listed := range []bool{false, true} {
					for _, held := range []bool{false, true} {
						for _, strict := range []bool{false, true} {
							for _, action := range []string{"sync", "restart and sync", "include", "include stale", "remove", "failed refresh"} {
								c := e
								c.open, c.shared, c.listed, c.held, c.strict, c.action = open, shared, listed, held, strict, action
								cases = append(cases, c)
							}
						}
					}
				}
			}
		}
	}
	return cases
}

// Every combination of state and action keeps the list's invariants: nothing
// is adopted unasked but an open folder no group shares, never while held or
// strict; a removal only Include undoes, and only for the folder shown; a
// refused change writes nothing; a sync leaves the marks resolved and not
// strict; and a run searches no folder but one the sync would adopt unasked or
// one the user approved itself.
func TestEveryStateCombinationKeepsTheInvariants(t *testing.T) {
	fakeGroups(t, "staff")
	for _, c := range pathCases() {
		t.Run(c.String(), func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			open := filepath.Join(base, "open")
			zones := Zones{Open: sandbox.FilePolicy{Read: []string{open}}}
			// now is the folder the entry leads to, in the zone and with the mode
			// the case names; a moved entry's stored Target is another folder.
			zone := map[bool]string{true: open, false: filepath.Join(base, "home")}[c.open]
			mode := map[bool]os.FileMode{true: 0o775, false: 0o755}[c.shared]
			now := dirWithMode(t, zone, "now", mode)
			stored := now
			if c.moved {
				stored = dirWithMode(t, open, "stored", 0o755)
			}
			link := filepath.Join(base, "link")
			require.NoError(t, os.Symlink(now, link))

			entries := []map[string]any{}
			if c.state != "" {
				entries = append(entries, map[string]any{"dir": link, "target": stored, "state": c.state, "source": c.source})
			}
			if c.held {
				entries = append(entries, map[string]any{"dir": "bin", "target": "bin", "state": "gone", "source": "user"})
			}
			raw, err := json.Marshal(map[string]any{"path": entries, "pathStrict": c.strict, "pathResolved": true})
			require.NoError(t, err)
			file := filepath.Join(t.TempDir(), "security.json")
			require.NoError(t, os.WriteFile(file, raw, 0o600))
			open1 := func() *Service {
				store, err := Open(file)
				require.NoError(t, err)
				return NewService(store, func() Zones { return zones }, func(context.Context) ([]string, error) {
					return nil, errors.New("timeout")
				}, "")
			}
			s := open1()
			require.Equal(t, c.held, s.Held("path"))
			var shell []string
			if c.listed {
				shell = []string{link}
			}
			before := s.Get()

			switch c.action {
			case "sync":
				synced(t, s, shell)
			case "restart and sync":
				s = open1()
				synced(t, s, shell)
			case "include":
				_, err = s.AdoptPath(link, stored)
			case "include stale":
				_, err = s.AdoptPath(link, filepath.Join(base, "elsewhere"))
			case "remove":
				_, err = s.DropPath(link)
			case "failed refresh":
				_, err = s.RefreshPath(t.Context())
			}
			after := s.Get()
			var was, is *PathEntry
			for i := range before.Path {
				if before.Path[i].Dir == link {
					was = &before.Path[i]
				}
			}
			for i := range after.Path {
				if after.Path[i].Dir == link {
					is = &after.Path[i]
				}
			}

			switch c.action {
			case "sync", "restart and sync":
				assert.True(t, after.PathResolved)
				assert.False(t, after.PathStrict)
				assert.False(t, s.Held("path"))
				if was != nil && was.State == PathGone {
					require.NotNil(t, is, "a removal is kept")
					assert.Equal(t, PathGone, is.State)
					assert.Equal(t, was.Source, is.Source)
				}
				if !c.listed {
					assert.Equal(t, was != nil && was.State == PathGone, is != nil, "only a removal outlives the shell's list")
				}
				if is != nil && c.listed {
					assert.Equal(t, c.shared, is.Shared, "shared follows the folder")
				}
				if is != nil && is.State == PathAdopted && is.Source == SourceShell {
					assert.True(t, c.open && !c.shared, "adopted unasked only when open and not shared")
				}
				if (was == nil || c.moved) && (c.held || c.strict) && is != nil {
					assert.NotEqual(t, PathAdopted, is.State, "nothing new adopted while held or strict")
				}
				if was != nil && !c.moved && was.State == PathAdopted && was.Source == SourceUser && c.listed {
					assert.Equal(t, *was, PathEntry{Dir: is.Dir, Target: is.Target, State: is.State, Source: is.Source, Shared: was.Shared}, "the user's adoption stands")
				}
			case "include":
				allowed := !c.held && was != nil && was.State != PathAdopted
				if allowed {
					require.NoError(t, err)
					assert.Equal(t, PathEntry{Dir: link, Target: stored, State: PathAdopted, Source: SourceUser}, *is, "for the folder shown")
				} else {
					assert.Error(t, err)
					assert.Equal(t, before, after)
				}
			case "include stale":
				assert.Error(t, err)
				assert.Equal(t, before, after, "a stale approval writes nothing")
			case "remove":
				if was != nil && was.State != PathGone {
					require.NoError(t, err)
					assert.Equal(t, PathGone, is.State)
					assert.Equal(t, SourceUser, is.Source)
					assert.Equal(t, c.held || c.strict, after.PathStrict, "a removal that ends a hold leaves the next sync strict")
				} else {
					assert.Error(t, err)
					assert.Equal(t, before, after)
				}
			case "failed refresh":
				assert.Error(t, err)
				assert.Equal(t, before, after)
				assert.Equal(t, "timeout", s.PathFault())
			}

			check := NewRunCheck(zones.Open, nil, "")
			for _, e := range after.Path {
				if e.State != PathAdopted || e.Dir != link {
					continue
				}
				if folder, _, ok := check.Folder(e); ok {
					assert.Equal(t, now, folder)
					unasked := c.open && !c.shared
					approved := e.Source == SourceUser && e.Target == now
					assert.True(t, unasked || approved, "a run searches only what the sync would adopt or the user approved")
				}
			}
		})
	}
}

// The tool probes are diagnostics: one that hangs, as a stat on a dead network
// mount does, never holds the sync's write, and a removed folder is never
// probed at all.
func TestAHungToolProbeDoesNotHoldTheSync(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir, removed := z.dir(t, "open/bin"), z.dir(t, "open/removed")
	setEntry(t, s, PathEntry{Dir: removed, Target: removed, State: PathGone, Source: SourceUser})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	probed := make(chan string, 16)
	defer func(stat func(string) (os.FileInfo, error)) { statTool = stat }(statTool)
	statTool = func(name string) (os.FileInfo, error) {
		probed <- filepath.Dir(name)
		<-release
		return nil, os.ErrNotExist
	}

	synced(t, s, []string{dir})
	assert.Equal(t, PathAdopted, entryOf(t, s, dir).State, "the write landed while the probe hangs")
	assert.Equal(t, dir, testutil.Recv(t, probed, "a probe"), "an adopted folder is probed")
	select {
	case got := <-probed:
		assert.NotEqual(t, removed, got, "a removed folder is never probed")
	default:
	}
}

// A sync that adds or drops an entry says it changed the list; one that
// leaves the list as it was does not.
func TestSyncPathSaysWhetherItChanged(t *testing.T) {
	z := newZones(t)
	s := newTestService(t, z, nil)
	dir, other := z.dir(t, "open/bin"), z.dir(t, "open/other")

	changed, err := s.SyncPath(t.Context(), []string{dir})
	require.NoError(t, err)
	assert.True(t, changed, "the first sync adds an entry")
	changed, err = s.SyncPath(t.Context(), []string{dir})
	require.NoError(t, err)
	assert.False(t, changed, "the same list")
	changed, err = s.SyncPath(t.Context(), []string{dir, other})
	require.NoError(t, err)
	assert.True(t, changed, "an entry added")
	changed, err = s.SyncPath(t.Context(), []string{other})
	require.NoError(t, err)
	assert.True(t, changed, "an entry dropped")
}
