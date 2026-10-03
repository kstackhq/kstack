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

package bash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// pathFixture is a tool over a fake sandbox whose System reads open, and
// whose Never denies never; home is read by no rule.
type pathFixture struct {
	tl                      *Tool
	fake                    *fakeSandboxer
	base, open, home, never string
}

func newPathFixture(t *testing.T) *pathFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	f := &pathFixture{tl: tool(t), base: base}
	f.open, f.home, f.never = f.dir(t, "open"), f.dir(t, "home"), f.dir(t, "never")
	f.fake = &fakeSandboxer{
		system: sandbox.System{Files: sandbox.FilePolicy{Read: []string{"/usr", "/bin", f.open}}},
		never:  []string{f.never},
	}
	f.tl.sandboxer = f.fake
	return f
}

// dir makes a folder under the fixture's base.
func (f *pathFixture) dir(t *testing.T, rel string) string {
	t.Helper()
	dir := filepath.Join(f.base, rel)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

// adopted is an entry the shell adopted for dir at target.
func adopted(dir, target string, source securityconfig.Source) securityconfig.PathEntry {
	return securityconfig.PathEntry{Dir: dir, Target: target, State: securityconfig.PathAdopted, Source: source}
}

// runPathOf runs echo "$PATH" sandboxed and answers it and the run.
func (f *pathFixture) runPathOf(t *testing.T) (string, sandbox.Run) {
	t.Helper()
	text, isError := f.tl.Run(t.Context(), testRuntime(t), command(`echo "$PATH"`))
	require.False(t, isError, text)
	runs := f.fake.seen()
	require.NotEmpty(t, runs)
	return strings.TrimSuffix(text, "\n"), runs[len(runs)-1]
}

func TestTheRunFreezesThePath(t *testing.T) {
	f := newPathFixture(t)
	inOpen, inHome := f.dir(t, "open/bin"), f.dir(t, "home/bin")
	entries := []securityconfig.PathEntry{
		adopted(inOpen, inOpen, securityconfig.SourceShell),
		adopted(inHome, inHome, securityconfig.SourceUser),
	}
	f.tl.pathList = func() securityconfig.RunPath { return securityconfig.RunPath{Entries: entries, Resolved: true} }
	// A store changed while the run is built changes nothing it finds.
	release := make(chan struct{})
	close(release)
	f.fake.onSystem, f.fake.holdSys = func() { entries = nil }, release

	path, r := f.runPathOf(t)
	assert.Equal(t, inOpen+":"+inHome, path)
	assert.Equal(t, []string{"/usr", "/bin", f.open, inHome}, r.Policy.Files.Read, "a Read rule for each folder System does not open")
	assert.Contains(t, r.Env, "PATH="+inOpen+":"+inHome)

	// A list the user emptied finds nothing: no default stands in for it.
	f.fake.onSystem = nil
	removed := securityconfig.PathEntry{Dir: inOpen, Target: inOpen, State: securityconfig.PathGone, Source: securityconfig.SourceUser}
	entries = []securityconfig.PathEntry{removed}
	path, r = f.runPathOf(t)
	assert.Equal(t, emptyPath, path)
	assert.Equal(t, []string{"/usr", "/bin", f.open}, r.Policy.Files.Read)
}

// A list never resolved, as after a first launch whose shell failed, searches
// the platform's default folders, each under the run's checks; a resolved list
// that is empty searches nothing.
func TestAnEmptyListSearchesTheCheckedDefault(t *testing.T) {
	f := newPathFixture(t)
	usable, world := f.dir(t, "open/bin"), f.dir(t, "open/world")
	require.NoError(t, os.Chmod(world, 0o777))
	link := filepath.Join(f.base, "bin")
	require.NoError(t, os.Symlink(usable, link))
	f.tl.fallbackPath = []string{usable, world, link, f.dir(t, "home/bin")}
	f.tl.pathList = func() securityconfig.RunPath { return securityconfig.RunPath{} }

	path, r := f.runPathOf(t)
	assert.Equal(t, usable, path, "the world-writable, the duplicate and the closed are left out")
	assert.Equal(t, []string{"/usr", "/bin", f.open}, r.Policy.Files.Read, "a default opens nothing")

	f.tl.pathList = func() securityconfig.RunPath { return securityconfig.RunPath{Resolved: true} }
	path, _ = f.runPathOf(t)
	assert.Equal(t, emptyPath, path)
}

func TestTheRunLeavesOutAMovedEntry(t *testing.T) {
	f := newPathFixture(t)
	first, other := f.dir(t, "open/v1"), f.dir(t, "open/other")
	link := filepath.Join(f.base, "current")
	require.NoError(t, os.Symlink(f.dir(t, "home/v2"), link))
	f.tl.pathList = func() securityconfig.RunPath {
		return securityconfig.RunPath{Resolved: true, Entries: []securityconfig.PathEntry{
			adopted(link, first, securityconfig.SourceShell),
			adopted(other, other, securityconfig.SourceShell),
		}}
	}
	log := testutil.CaptureLogs(t)

	path, r := f.runPathOf(t)
	assert.Equal(t, other, path)
	assert.NotContains(t, r.Policy.Files.Read, filepath.Join(f.home, "v2"))
	assert.Equal(t, 1, strings.Count(log.String(), link), log.String())
}

func TestTheRunLeavesOutAnEntryNoRuleOpens(t *testing.T) {
	f := newPathFixture(t)
	inNever := f.dir(t, "never/bin")
	world := f.dir(t, "open/world")
	require.NoError(t, os.Chmod(world, 0o777))
	byShell, byUser := f.dir(t, "home/shell"), f.dir(t, "home/user")
	kept := f.dir(t, "open/kept")
	f.tl.pathList = func() securityconfig.RunPath {
		return securityconfig.RunPath{Resolved: true, Entries: []securityconfig.PathEntry{
			adopted(inNever, inNever, securityconfig.SourceUser),
			adopted(world, world, securityconfig.SourceUser),
			adopted(byShell, byShell, securityconfig.SourceShell),
			adopted(byUser, byUser, securityconfig.SourceUser),
			adopted(kept, kept, securityconfig.SourceShell),
		}}
	}
	log := testutil.CaptureLogs(t)

	path, r := f.runPathOf(t)
	assert.Equal(t, byUser+":"+kept, path)
	require.NoError(t, r.Policy.Check(), "no Read rule lies inside an Always path")
	for _, dir := range []string{inNever, world, byShell} {
		assert.Contains(t, log.String(), dir)
	}
}

// An adopted entry that is the home, or holds it, is left out of a run, as a
// list stored before the check would have it: its Read rule would open all of
// the home.
func TestTheRunLeavesOutABroadEntry(t *testing.T) {
	f := newPathFixture(t)
	f.tl.home = f.home
	inHome := f.dir(t, "home/bin")
	f.tl.pathList = func() securityconfig.RunPath {
		return securityconfig.RunPath{Resolved: true, Entries: []securityconfig.PathEntry{
			adopted(f.home, f.home, securityconfig.SourceUser),
			adopted(f.base, f.base, securityconfig.SourceUser),
			adopted(inHome, inHome, securityconfig.SourceUser),
		}}
	}

	path, r := f.runPathOf(t)
	assert.Equal(t, inHome, path)
	assert.Equal(t, []string{"/usr", "/bin", f.open, inHome}, r.Policy.Files.Read)
}

func TestTheRunFollowsALinkIntoAnOpenFolder(t *testing.T) {
	f := newPathFixture(t)
	first, second := f.dir(t, "open/v1"), f.dir(t, "open/v2")
	link := filepath.Join(f.base, "current")
	require.NoError(t, os.Symlink(second, link))
	entries := []securityconfig.PathEntry{adopted(link, first, securityconfig.SourceShell)}
	f.tl.pathList = func() securityconfig.RunPath { return securityconfig.RunPath{Entries: entries, Resolved: true} }
	log := testutil.CaptureLogs(t)

	path, _ := f.runPathOf(t)
	assert.Equal(t, second, path)
	assert.Contains(t, log.String(), link)
	assert.Equal(t, first, entries[0].Target, "the stored list is not written")

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(f.dir(t, "home/v3"), link))
	path, _ = f.runPathOf(t)
	assert.Equal(t, emptyPath, path)
}

// Over the real service: a pending folder's program is not found until the
// entry is adopted.
func TestAPendingEntryIsNotOnThePath(t *testing.T) {
	f := newPathFixture(t)
	scripts := f.dir(t, "home/scripts")
	require.NoError(t, os.WriteFile(filepath.Join(scripts, "hello"), []byte("#!/bin/sh\necho hi\n"), 0o755))
	store, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	svc := securityconfig.NewService(store, func() securityconfig.Zones {
		return securityconfig.Zones{Never: f.fake.never, Open: f.fake.system.Files}
	}, nil, "")
	require.NoError(t, svc.SyncPath(t.Context(), []string{scripts}))
	f.tl.pathList = func() securityconfig.RunPath { return svc.Get().RunPath() }

	text, isError := f.tl.Run(t.Context(), testRuntime(t), command("command -v hello"))
	assert.True(t, isError, text)

	_, err = svc.AdoptPath(scripts, scripts)
	require.NoError(t, err)
	text, isError = f.tl.Run(t.Context(), testRuntime(t), command("command -v hello"))
	require.False(t, isError, text)
	assert.Equal(t, filepath.Join(scripts, "hello")+"\n", text)
}

// An included folder that the system's list denies by name, a closed folder
// itself, is opened: its Read rule takes the Deny's place.
func TestAnIncludedClosedFolderIsOpened(t *testing.T) {
	f := newPathFixture(t)
	closed := f.dir(t, "open/Downloads")
	f.fake.system.Files.Deny = []string{closed}
	f.tl.pathList = func() securityconfig.RunPath {
		return securityconfig.RunPath{Resolved: true, Entries: []securityconfig.PathEntry{adopted(closed, closed, securityconfig.SourceUser)}}
	}

	path, r := f.runPathOf(t)
	assert.Equal(t, closed, path)
	assert.Contains(t, r.Policy.Files.Read, closed)
	assert.NotContains(t, r.Policy.Files.Deny, closed)
}
