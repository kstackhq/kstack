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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// credentials are the paths under the home that hold a credential. Each is
// denied wherever a readable tree takes it in. The last four sit beside a
// tool's programs, in a tree its bin on PATH opens.
var credentials = []string{
	".kube", ".aws", ".azure", ".config/gcloud", ".ssh", ".gnupg", ".config/gh", ".docker",
	".netrc", ".git-credentials", ".local/share/keyrings", "Library/Keychains",
	".cargo/credentials", ".cargo/credentials.toml", ".pulumi/credentials.json", ".fly/config.yml",
}

// credentialPaths is the credential list under home, each resolved as
// pathTrees resolves a tree, so the two compare: a credential that is a link
// is denied where its target lies.
func credentialPaths(home string) []string {
	paths := make([]string, len(credentials))
	for i, c := range credentials {
		paths[i] = resolved(filepath.Join(home, filepath.FromSlash(c)))
	}
	return paths
}

// resolvedAll is each of paths resolved. The trees are resolved and the
// comparisons against them read text, so a path named through a link would
// match no tree.
func resolvedAll(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = resolved(p)
	}
	return out
}

// pathTrees is what the PATH entries make readable beyond roots. An entry
// outside the home is readable at its resolved path. An entry under the home
// makes the first directory below the home readable, so a program there can
// read its own tree. ~/.local, ~/.local/share and ~/.config count as homes of
// their own, since they hold the user's data and every tool's settings: an
// entry under one opens the first directory below it. An entry under one of
// shared (relative to the home), which holds every app's data, opens only
// itself. A program in an entry that is a link opens the
// directory of every link on its way, in a root's entry too, since
// /usr/local/bin can link to a program under the home: under ~/.local/share
// the first directory below it, since a pipx or uv program needs its
// virtualenv, and elsewhere that directory alone. An entry or a link's directory that is not absolute,
// does not exist, lies inside a root, or is a home or above one adds nothing
// of its own, since binding it would bind that home. The trees come sorted,
// none inside another.
func pathTrees(home string, roots, shared, entries []string) []string {
	homes := homesOf(home)
	sharedDirs := make([]string, len(shared))
	for i, d := range shared {
		sharedDirs[i] = resolved(filepath.Join(home, d))
	}
	var trees []string
	for _, e := range entries {
		p, ok := readable(e, homes, roots)
		switch {
		case ok && inAny(p, sharedDirs):
			trees = append(trees, p)
		case ok:
			trees = append(trees, firstBelow(p, homes))
		case !inRoot(e, roots):
			continue
		}
		for _, dir := range linkTargets(e) {
			if q, ok := readable(dir, homes, roots); ok {
				trees = append(trees, firstBelow(q, homes[:1]))
			}
		}
	}
	slices.Sort(trees)
	var outer []string
	for _, t := range trees {
		if !inAny(t, outer) {
			outer = append(outer, t)
		}
	}
	return outer
}

// readable is dir resolved, and whether a tree may take it in: it is
// absolute, exists, lies inside no root, and is no home nor above one.
func readable(dir string, homes, roots []string) (string, bool) {
	if !filepath.IsAbs(dir) {
		return "", false
	}
	p, err := filepath.EvalSymlinks(dir)
	if err != nil || inAny(p, roots) {
		return "", false
	}
	// A linked home need not lie under the next one out, so each is checked.
	for _, h := range homes {
		if within(h, p) {
			return "", false
		}
	}
	return p, true
}

// inRoot reports whether dir is absolute and, resolved, lies inside a root.
func inRoot(dir string, roots []string) bool {
	p, err := filepath.EvalSymlinks(dir)
	return err == nil && filepath.IsAbs(dir) && inAny(p, roots)
}

// maxHops is how many links the kernel follows in one lookup before it
// answers ELOOP.
const maxHops = 40

// linkTargets is the directory of each link a program in entry passes
// through, as each link names it: read with os.Readlink and made absolute
// against the link's own directory. The kernel reads every one to reach the
// program, so each must be there.
func linkTargets(entry string) []string {
	programs, err := os.ReadDir(entry)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, p := range programs {
		if p.Type()&fs.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(entry, p.Name())
		for range maxHops {
			target, err := os.Readlink(link)
			if err != nil {
				break
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(link), target)
			}
			dirs = append(dirs, filepath.Dir(target))
			link = target
		}
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// homesOf is home and the three directories under it that count as homes of
// their own, innermost first, each resolved as the paths compared against
// them are: a linked ~/.local/share is a home where its link leads.
func homesOf(home string) []string {
	home = resolved(home)
	local := resolved(filepath.Join(home, ".local"))
	config := resolved(filepath.Join(home, ".config"))
	return []string{resolved(filepath.Join(local, "share")), local, config, home}
}

// firstBelow is the first directory below the innermost of homes that holds
// p, or p itself where none does.
func firstBelow(p string, homes []string) string {
	for _, h := range homes {
		if within(p, h) {
			rel, _ := filepath.Rel(h, p)
			return filepath.Join(h, strings.SplitN(rel, string(filepath.Separator), 2)[0])
		}
	}
	return p
}

// link is a symbolic link a sandbox recreates at path, naming target.
type link struct{ path, target string }

// pathLinks is the links that let entries, and the directories their program
// links name, be reached as written: each whose resolved path differs from its
// own, lies inside a root or a tree, and whose own path lies in neither. A
// link inside another's path is left out, since the outer one reaches it.
func pathLinks(roots, trees, entries []string) []link {
	// A PATH directory's program links mostly name a few directories, so each
	// is resolved once.
	named := map[string]bool{}
	for _, e := range entries {
		for _, n := range append(linkTargets(e), e) {
			if filepath.IsAbs(n) {
				named[filepath.Clean(n)] = true
			}
		}
	}
	inside := func(p string) bool { return inAny(p, roots) || inAny(p, trees) }
	var links []link
	for e := range named {
		p, err := filepath.EvalSymlinks(e)
		if err != nil || p == e || !inside(p) || inside(e) {
			continue
		}
		links = append(links, link{path: e, target: p})
	}
	slices.SortFunc(links, func(a, b link) int { return strings.Compare(a.path, b.path) })
	all := slices.Clone(links)
	return slices.DeleteFunc(links, func(l link) bool {
		return slices.ContainsFunc(all, func(o link) bool { return o != l && within(l.path, o.path) })
	})
}

// overlapping is the denials that overlap a tree, in their order: one inside
// a tree, or one holding a tree. Each is laid over the trees, so a denial the
// trees never reach needs no rule.
func overlapping(denials, trees []string) []string {
	var out []string
	for _, d := range denials {
		if slices.ContainsFunc(trees, func(t string) bool { return within(d, t) || within(t, d) }) {
			out = append(out, d)
		}
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

// pathOf is the PATH env sets, split into its entries. The last one wins, as
// exec.Cmd keeps only the last of a repeated key.
func pathOf(env []string) []string {
	var path string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	return filepath.SplitList(path)
}
