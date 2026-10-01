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

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A link is followed before the tree is chosen: one into a root adds nothing,
// one out of the home names its target, and one into the home names the first
// directory below it.
func TestAPathLinkIsFollowed(t *testing.T) {
	home, root, outside := machine(t)
	inRoot := mkdirs(t, root, "bin")[0]
	tool := mkdirs(t, outside, "bin")[0]
	asdf := mkdirs(t, home, ".asdf/shims")[0]
	links := mkdirs(t, outside, "links")[0]
	for name, target := range map[string]string{"root": inRoot, "tool": tool, "asdf": asdf} {
		require.NoError(t, os.Symlink(target, filepath.Join(links, name)))
	}
	homeLink := filepath.Join(home, "local-bin")
	require.NoError(t, os.Symlink(tool, homeLink))

	trees := pathTrees(home, []string{root}, nil, []string{
		filepath.Join(links, "root"), filepath.Join(links, "tool"), filepath.Join(links, "asdf"), homeLink,
	})

	assert.Equal(t, []string{filepath.Join(home, ".asdf"), tool}, trees)
}

// A home reached through a link is named at its target, by the trees and the
// credential list alike, so a denial still falls inside the tree that holds it.
func TestAHomeThroughALinkIsNamedAtItsTarget(t *testing.T) {
	home, _, outside := machine(t)
	bin := mkdirs(t, home, ".docker/cli-plugins")[0]
	link := filepath.Join(outside, "home-link")
	require.NoError(t, os.Symlink(home, link))

	trees := pathTrees(link, nil, nil, []string{bin})
	var denied []string
	for _, r := range (Policy{Files: FilePolicy{Read: trees}, Always: AlwaysPolicy{Deny: neverPaths(link)}}).rules() {
		if r.kind == ruleDeny {
			denied = append(denied, r.at)
		}
	}

	assert.Equal(t, []string{filepath.Join(home, ".docker")}, trees)
	assert.Equal(t, []string{filepath.Join(home, ".docker")}, denied)
}

// A program that is a link opens the directory its link names: under
// ~/.local/share the first directory below it, since a pipx program needs its
// virtualenv; anywhere else that directory alone. A relative link reads
// against its entry, and a link into a root, to the home or above it, or to
// nothing opens nothing.
func TestAProgramLinkOpensTheDirectoryItNames(t *testing.T) {
	home, root, outside := machine(t)
	bin := mkdirs(t, home, ".local/bin")[0]
	venv := mkdirs(t, home, ".local/share/pipx/venvs/x/bin")[0]
	project := mkdirs(t, home, "projects/foo/bin")[0]
	tool := mkdirs(t, outside, "tool/bin")[0]
	inRoot := mkdirs(t, root, "bin")[0]
	for name, target := range map[string]string{
		"x":       filepath.Join(venv, "x"),
		"y":       "../../projects/foo/bin/y",
		"z":       filepath.Join(tool, "z"),
		"snap":    filepath.Join(inRoot, "snap"),
		"home":    home,
		"missing": filepath.Join(outside, "gone", "m"),
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(bin, name)))
	}
	for _, f := range []string{filepath.Join(venv, "x"), filepath.Join(project, "y"), filepath.Join(tool, "z"), filepath.Join(bin, "plain")} {
		require.NoError(t, os.WriteFile(f, nil, 0o700))
	}

	trees := pathTrees(home, []string{root}, nil, []string{bin})

	assert.Equal(t, []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "share", "pipx"),
		project,
		tool,
	}, trees)
}

// An entry whose own path differs from its resolved one, where the resolved
// one is in a root or a tree and its own in neither, is recreated as a link,
// since PATH and the shell name it as written.
func TestAnEntryThroughALinkIsRecreated(t *testing.T) {
	home, root, outside := machine(t)
	profile := mkdirs(t, outside, "profile/bin")[0]
	nix := filepath.Join(home, ".nix-profile")
	require.NoError(t, os.Symlink(filepath.Dir(profile), nix))
	tool := mkdirs(t, outside, "tool/bin")[0]
	links := mkdirs(t, outside, "links")[0]
	require.NoError(t, os.Symlink(tool, filepath.Join(links, "tool")))
	inRoot := filepath.Join(root, "tool")
	require.NoError(t, os.Symlink(tool, inRoot))
	toHome := filepath.Join(home, "to-home")
	require.NoError(t, os.Symlink(home, toHome))
	notADir := filepath.Join(outside, "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))
	entries := []string{filepath.Join(nix, "bin"), filepath.Join(links, "tool"), inRoot, toHome, tool, notADir, "relative/bin"}

	got := pathLinks(append([]string{root}, pathTrees(home, []string{root}, nil, entries)...), entries)

	assert.Equal(t, []link{
		{path: filepath.Join(nix, "bin"), target: profile},
		{path: filepath.Join(links, "tool"), target: tool},
	}, got)
}

// A program link that names its target through a link, as pipx does under a
// home reached through one, has that directory recreated too.
func TestAProgramLinkThroughALinkedHomeIsRecreated(t *testing.T) {
	home, _, outside := machine(t)
	linked := filepath.Join(outside, "home-link")
	require.NoError(t, os.Symlink(home, linked))
	mkdirs(t, home, ".local/bin", ".local/share/pipx/venvs/x/bin")
	bin := filepath.Join(linked, ".local", "bin")
	venv := filepath.Join(linked, ".local", "share", "pipx", "venvs", "x", "bin")
	require.NoError(t, os.Symlink(filepath.Join(venv, "x"), filepath.Join(bin, "x")))
	require.NoError(t, os.WriteFile(filepath.Join(venv, "x"), nil, 0o700))

	got := pathLinks(pathTrees(linked, nil, nil, []string{bin}), []string{bin})

	assert.Equal(t, []link{
		{path: bin, target: filepath.Join(home, ".local", "bin")},
		{path: venv, target: filepath.Join(home, ".local", "share", "pipx", "venvs", "x", "bin")},
	}, got)
}

// An entry inside a root opens no tree of its own, but a program in it that
// is a link opens the directory its link names.
func TestAProgramLinkInARootOpensTheDirectoryItNames(t *testing.T) {
	home, root, _ := machine(t)
	bin := mkdirs(t, root, "local/bin")[0]
	tools := mkdirs(t, home, "tools")[0]
	require.NoError(t, os.WriteFile(filepath.Join(tools, "tool"), nil, 0o700))
	require.NoError(t, os.Symlink(filepath.Join(tools, "tool"), filepath.Join(bin, "tool")))

	assert.Equal(t, []string{tools}, pathTrees(home, []string{root}, nil, []string{bin}))
}

// ~/.local and ~/.local/share are homes of their own wherever their links
// lead: an entry under a linked one opens the first directory below its
// target, and the target itself opens nothing.
func TestALinkedLocalShareIsAHomeAtItsTarget(t *testing.T) {
	home, _, outside := machine(t)
	docs := mkdirs(t, home, "Documents")[0]
	mkdirs(t, home, ".local", "Documents/pipx/venvs/x/bin", "Documents/private")
	require.NoError(t, os.Symlink(docs, filepath.Join(home, ".local", "share")))
	mkdirs(t, outside, "local/bin", "local/state/tool/bin")
	local := filepath.Join(outside, "local")
	other := filepath.Join(outside, "other-home")
	require.NoError(t, os.MkdirAll(other, 0o700))
	require.NoError(t, os.Symlink(local, filepath.Join(other, ".local")))

	assert.Equal(t, []string{filepath.Join(docs, "pipx")},
		pathTrees(home, nil, nil, []string{filepath.Join(home, ".local", "share", "pipx", "venvs", "x", "bin"), docs}))
	assert.Equal(t, []string{filepath.Join(local, "bin"), filepath.Join(local, "state")},
		pathTrees(other, nil, nil, []string{filepath.Join(other, ".local", "bin"), filepath.Join(other, ".local", "state", "tool", "bin"), local}))
}

// A program that links through another link opens the directory of every
// link on the way, since the kernel reads each to reach the program.
func TestAProgramLinkChainOpensEachDirectory(t *testing.T) {
	home, _, _ := machine(t)
	bin := mkdirs(t, home, ".local/bin")[0]
	links := mkdirs(t, home, "links")[0]
	install := mkdirs(t, home, "install/bin")[0]
	require.NoError(t, os.WriteFile(filepath.Join(install, "tool"), nil, 0o700))
	require.NoError(t, os.Symlink("../install/bin/tool", filepath.Join(links, "tool")))
	require.NoError(t, os.Symlink(filepath.Join(links, "tool"), filepath.Join(bin, "tool")))
	require.NoError(t, os.Symlink("loop", filepath.Join(bin, "loop")))

	assert.Equal(t, []string{bin, install, links}, pathTrees(home, nil, nil, []string{bin}))
}

// A path that does not exist is resolved through its deepest folder that
// does, so a missing path under a link names where the link leads.
func TestAMissingPathIsResolvedThroughItsDeepestFolder(t *testing.T) {
	base := resolved(t.TempDir())
	target := mkdirs(t, base, "private/var")[0]
	require.NoError(t, os.Symlink(target, filepath.Join(base, "var")))

	assert.Equal(t, filepath.Join(target, "missing", "file"), resolved(filepath.Join(base, "var", "missing", "file")))
}
