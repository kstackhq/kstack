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

package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// resolvedAll is each of paths resolved. Comparisons between paths read
// their text, so a path named through a link must be resolved to match.
func resolvedAll(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = resolved(p)
	}
	return out
}

// inAny reports whether p lies within any of dirs.
func inAny(p string, dirs []string) bool {
	return slices.ContainsFunc(dirs, func(d string) bool { return within(p, d) })
}

// within reports whether p is dir or lies under it, by their text alone.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolved is p with its links followed. A path that does not exist is
// resolved through its deepest folder that does.
func resolved(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolved(parent), filepath.Base(p))
}

// Resolved is each of paths with its links followed, so a caller comparing
// many paths against them resolves them once.
func Resolved(paths []string) []string {
	return resolvedAll(paths)
}

// Under reports whether p is one of dirs or lies under one, by their text
// alone: both must already be resolved.
func Under(p string, dirs []string) bool {
	return inAny(p, dirs)
}

// Spelled is each of paths as the disk spells it: every component replaced by
// the name its directory lists for the same file, so on a case-insensitive
// filesystem two spellings of one folder compare equal by text. Each path
// should be resolved first. A component not found, or whose directory cannot
// be listed, is kept as given with the rest of its path. It reads the disk,
// each directory once.
func Spelled(paths []string) []string {
	listings := map[string][]os.DirEntry{}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = spelled(p, listings)
	}
	return out
}

func spelled(p string, listings map[string][]os.DirEntry) string {
	p = filepath.Clean(p)
	vol := filepath.VolumeName(p)
	parts := strings.Split(p[len(vol):], string(filepath.Separator))
	dir := vol + string(filepath.Separator)
	for i, name := range parts {
		if name == "" {
			continue
		}
		listed, ok := listedName(dir, name, listings)
		if !ok {
			return filepath.Join(append([]string{dir}, parts[i:]...)...)
		}
		dir = filepath.Join(dir, listed)
	}
	return dir
}

// listedName is the name dir lists for the file name names in it.
func listedName(dir, name string, listings map[string][]os.DirEntry) (string, bool) {
	info, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	entries, ok := listings[dir]
	if !ok {
		entries, err = os.ReadDir(dir)
		if err != nil {
			return "", false
		}
		listings[dir] = entries
	}
	for _, e := range entries {
		if e.Name() == name {
			return name, true
		}
	}
	for _, e := range entries {
		if other, err := e.Info(); err == nil && os.SameFile(info, other) {
			return e.Name(), true
		}
	}
	return "", false
}
