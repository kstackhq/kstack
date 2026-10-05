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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
)

// FolderRefusal is why a folder cannot be granted, in the user's words. Rule
// names the check that refused it; for "link", Target is where it leads.
type FolderRefusal struct {
	Rule   string
	Reason string
	Target string
}

func (r FolderRefusal) Error() string { return r.Reason }

// CheckFolder is nil when path can be granted, read, or with write read and
// write, else a FolderRefusal. It reads the disk: path is made canonical and
// compared with z, every path canonical, and pathEntries, the PATH folders,
// already canonical. A link on or above path, or a spelling the disk does not
// list, is refused with the canonical path, so the folder the user reads is
// the folder the sandbox checks.
func CheckFolder(path string, write bool, z Zones, pathEntries []string) error {
	return checkFolder(path, write, resolveZones(z), pathEntries, false)
}

// CheckStoredFolder is CheckFolder for a grant already stored: one that
// resolves elsewhere than it did has moved, or had a link put in its place,
// since it was granted.
func CheckStoredFolder(path string, write bool, z Zones, pathEntries []string) error {
	return checkFolder(path, write, resolveZones(z), pathEntries, true)
}

func checkFolder(path string, write bool, z zones, pathEntries []string, stored bool) error {
	refuse := func(rule, reason string) error { return FolderRefusal{Rule: rule, Reason: reason} }
	switch {
	case !filepath.IsAbs(path) || filepath.Clean(path) != path:
		return refuse("absolute", "Name the folder by its full path.")
	case path == string(filepath.Separator):
		return refuse("root", "The root cannot be granted.")
	case sandbox.FixedMount(path):
		return refuse("fixed", "Every sandboxed command has its own "+fixedMountOf(path)+"; it cannot be granted.")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return refuse("exists", "This folder does not exist.")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return refuse("exists", "This folder does not exist.")
	}
	spelled := sandbox.Spelled([]string{resolved})[0]
	switch {
	case spelled != path && stored:
		return refuse("moved", "This folder has moved or become a link. Grant it again.")
	case resolved != path:
		return FolderRefusal{Rule: "link", Reason: path + " is a link to " + spelled + "; grant " + spelled + " instead.", Target: spelled}
	case spelled != path:
		// A case-insensitive filesystem opens either spelling, and the zones
		// are compared by text.
		return FolderRefusal{Rule: "spelling", Reason: path + " is spelled " + spelled + " on disk; grant " + spelled + " instead.", Target: spelled}
	case sandbox.Under(path, z.never):
		return refuse("never", "This folder is never readable in the sandbox.")
	case slices.Contains(z.deny, path):
		// A closed folder's Deny is not outranked by a Read on the same path.
		return refuse("closed", "Grant a folder inside it.")
	case !write:
		return nil
	case path == z.home:
		// A command that can write a folder can rename what is under it.
		return refuse("home", "Your home can be granted read-only.")
	}
	if p, ok := overlapping(path, slices.Concat(z.read, pathEntries)); ok {
		return refuse("tool", "Kstack reads tools from "+p+"; grant it read-only, or a folder beside it.")
	}
	if p, ok := inside(path, z.deny); ok {
		return refuse("over-closed", p+" is closed in the sandbox; grant a folder inside it.")
	}
	if p, ok := overlapping(path, z.noWrite); ok {
		return refuse("code", "Something outside the sandbox runs what is in "+p+"; grant it read-only.")
	}
	if p, ok := inside(path, z.never); ok {
		return refuse("over-never", p+" is never readable in the sandbox, so this folder can be granted read-only.")
	}
	return nil
}

// overlapping is the first of dirs that path is on, under or over.
func overlapping(path string, dirs []string) (string, bool) {
	for _, d := range dirs {
		if sandbox.Under(path, []string{d}) || sandbox.Under(d, []string{path}) {
			return d, true
		}
	}
	return "", false
}

// inside is the first of dirs that lies under path, path itself left out.
func inside(path string, dirs []string) (string, bool) {
	for _, d := range dirs {
		if d != path && sandbox.Under(d, []string{path}) {
			return d, true
		}
	}
	return "", false
}

// fixedMountOf is the mount path is on or under: its outermost folder that is
// still a fixed mount.
func fixedMountOf(path string) string {
	for {
		parent := filepath.Dir(path)
		if parent == path || !sandbox.FixedMount(parent) {
			return path
		}
		path = parent
	}
}

// PathEntryFolders is each stored PATH entry's Dir and Target, whatever its
// state, canonical: what a run's own check of a grant reads as the PATH.
func PathEntryFolders(entries []PathEntry) []string {
	return pathFolders(entries, nil)
}

// pathFolders is each stored PATH entry's Dir and Target, whatever its state,
// then the login shell's own, each canonical: something outside the sandbox
// searches all of them.
func pathFolders(entries []PathEntry, loginPath []string) []string {
	var all []string
	for _, e := range entries {
		all = append(all, e.Dir, e.Target)
	}
	all = append(all, loginPath...)
	all = slices.DeleteFunc(all, func(p string) bool { return !filepath.IsAbs(p) || strings.ContainsRune(p, 0) })
	return canonical(all)
}

// snapshot is what the folder checks read: the zones as the zones function
// listed them, and the login shell's PATH as the last sync read it, unfiltered.
// Every check resolves them again, so a link on a list that is retargeted
// after the sync is caught at its target now; the snapshot saves the zones
// function's own reads of the disk alone.
type snapshot struct {
	zones     Zones
	loginPath []string
}

func takeSnapshot(z Zones, loginPath []string) *snapshot {
	abs := slices.DeleteFunc(slices.Clone(loginPath), func(p string) bool { return !filepath.IsAbs(p) })
	return &snapshot{zones: z, loginPath: abs}
}

// readWithin runs read, which reads the disk, and answers it, or false when
// ctx or bound ends first: a stat on a dead network mount blocks in a syscall
// no context reaches, so read runs on a goroutine left to return whenever the
// filesystem does, and what it answers then is dropped.
func readWithin[T any](ctx context.Context, bound time.Duration, read func() T) (T, bool) {
	done := make(chan T, 1)
	go func() { done <- read() }()
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	select {
	case v := <-done:
		return v, true
	case <-ctx.Done():
		var none T
		return none, false
	}
}

// currentSnapshot is the snapshot, taken on its first use when no sync has
// taken one: zones reads the disk, so it is bounded as a sync is. Nil when ctx
// or the bound ends first, or on a machine with no sandbox.
func (s *Service) currentSnapshot(ctx context.Context) *snapshot {
	if s.zones == nil {
		return nil
	}
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.snap == nil {
		s.snap, _ = readWithin(ctx, s.syncTimeout, func() *snapshot { return takeSnapshot(s.zones(), nil) })
	}
	return s.snap
}

var (
	noSandboxRefusal = FolderRefusal{Rule: "no-sandbox", Reason: "This machine has no sandbox."}
	timeoutRefusal   = FolderRefusal{Rule: "timeout", Reason: "Kstack could not check this folder in time."}
)

// CheckFolder is CheckFolder against the snapshot's zones, resolved now, and
// every PATH entry the list stores or the login shell answered. Its disk reads
// are bounded as a sync's are. On a machine with no sandbox every folder is
// refused.
func (s *Service) CheckFolder(ctx context.Context, path string, write bool) error {
	return s.checkWithin(ctx, path, write, false)
}

// CheckStoredFolder is CheckStoredFolder, bounded as CheckFolder is.
func (s *Service) CheckStoredFolder(ctx context.Context, path string, write bool) error {
	return s.checkWithin(ctx, path, write, true)
}

func (s *Service) checkWithin(ctx context.Context, path string, write, stored bool) error {
	if s.zones == nil {
		return noSandboxRefusal
	}
	snap := s.currentSnapshot(ctx)
	if snap == nil {
		return timeoutRefusal
	}
	entries := s.Get().Path
	err, ok := readWithin(ctx, s.syncTimeout, func() error {
		return s.checkFolder(path, write, resolveZones(snap.zones), pathFolders(entries, snap.loginPath), stored)
	})
	if !ok {
		return timeoutRefusal
	}
	return err
}

// Hidden is what the sandbox keeps shut under any grant, as the zones list it:
// never, the denied-always list with Kstack's directories, and closed, the
// closed folders. It reads the snapshot and never takes one: with none, no
// folder passed a check either. It touches no disk; the file tools resolve the
// paths when they compare them.
func (s *Service) Hidden() (never, closed []string) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.snap == nil {
		return nil, nil
	}
	return s.snap.zones.Never, s.snap.zones.Open.Deny
}

// NeverReadable is the denied-always list with Kstack's directories, as
// Settings draws it; nil on a machine with no sandbox.
func (s *Service) NeverReadable(ctx context.Context) []string {
	snap := s.currentSnapshot(ctx)
	if snap == nil {
		return nil
	}
	return snap.zones.Never
}

// WideFolders is the folders a read grant of which reads more than a project:
// the home and each folder over it, resolved, and the platform's own
// (wideDirs). Nil on a machine with no sandbox, and when resolving the home
// runs past the bound.
func (s *Service) WideFolders(ctx context.Context) []string {
	snap := s.currentSnapshot(ctx)
	if snap == nil || snap.zones.Home == "" {
		return nil
	}
	home, ok := readWithin(ctx, s.syncTimeout, func() string { return sandbox.Resolved([]string{snap.zones.Home})[0] })
	if !ok {
		return nil
	}
	var wide []string
	for dir := home; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		wide = append(wide, dir)
	}
	return append(wide, wideDirs...)
}
