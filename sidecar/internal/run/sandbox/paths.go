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
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	if parent == p { // coverage:ignore EvalSymlinks of the root fails only when / cannot be read
		return p
	}
	return filepath.Join(resolved(parent), filepath.Base(p))
}

// Resolved is each of paths with its links followed, so a caller comparing
// many paths against them resolves them once.
func Resolved(paths []string) []string {
	return resolvedAll(paths)
}

// Canonical is each of paths as the kernel names it (kernelPath), so two names
// of one folder compare equal by text: a path through a link, and on a
// case-insensitive filesystem a path in another case. A path the kernel
// cannot name is kept as given.
func Canonical(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i], _ = kernelNamed(filepath.Clean(p))
	}
	return out
}

// canonical is Canonical, or the first error naming a path: the caller
// refuses what it cannot compare.
func canonical(paths []string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		s, err := kernelNamed(filepath.Clean(p))
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// Under reports whether p is one of dirs or lies under one, by their text
// alone: both must already be resolved.
func Under(p string, dirs []string) bool {
	return inAny(p, dirs)
}

// Spelled is Canonical: the kernel's name for a path follows its links too.
func Spelled(paths []string) []string {
	return Canonical(paths)
}

// kernelNamed is p as the kernel names it. A path that is not there, or that this
// user may not reach, which no run of theirs can either, is named through its
// deepest folder that is, the rest kept as given.
func kernelNamed(p string) (string, error) {
	at, err := kernelPath(p)
	// ENOTDIR is a path through a file, which holds nothing.
	missing := errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR)
	switch {
	case err == nil:
		return at, nil
	case !missing:
		return p, fmt.Errorf("the path of %s on disk: %w", p, err)
	}
	parent := filepath.Dir(p)
	if parent == p { // coverage:ignore the root is always there
		return p, nil
	}
	// On an error dir is parent as given, so the join is p.
	dir, err := kernelNamed(parent)
	return filepath.Join(dir, filepath.Base(p)), err
}
