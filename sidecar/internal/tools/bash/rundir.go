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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kstackhq/kstack/sidecar/internal/lib/rootdir"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
)

const (
	// maxRunDirName is the longest name a run's directory can have: a pid of up
	// to 7 digits, - and MkdirTemp's suffix of up to 10.
	maxRunDirName = 7 + len("-") + 10
	// socketName is the forwarder's socket in a run's directory.
	socketName = "proxy.sock"
)

// runDir is one sandboxed run's own directories, each 0700 and named for the
// sidecar's pid. path, under the runs directory, holds its kubeconfig and its
// forwarder's socket and is its ZDOTDIR: outside the workspace, so a sandbox
// that confines can keep a command from rewriting them, and under the runtime
// directory, since a socket's path must fit sun_path and a data directory's can
// run past it. tmp, under the cache's tmp directory, is
// its TMPDIR and the one directory of its own the command may write: on disk,
// so a command fills it no further than it can fill its workspace.
type runDir struct {
	path string // under the runs directory as it was given, never resolved
	tmp  string
}

// newRunDir makes a run's directory under runsDir and its TMPDIR under tmpDir,
// making both owner-only first. A runs directory under which a run's socket
// would not fit is refused before anything is made, and so is one in a
// directory that is not this user's alone (checkPrivate). It holds the
// sidecar's lock in both before it makes anything in them (holdRunLock), so
// a sweep never takes the run of a sidecar still running.
// Its TMPDIR starts with what sandbox.SeedTmpDir copies.
func newRunDir(runsDir, tmpDir string, pid int) (*runDir, error) {
	runsDir = filepath.Clean(runsDir)
	if err := checkSocketPath(runsDir); err != nil {
		return nil, err
	}
	for _, dir := range []string{runsDir, tmpDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := checkPrivate(filepath.Dir(runsDir)); err != nil {
		return nil, err
	}
	for _, dir := range []string{runsDir, tmpDir} {
		if err := holdRunLock(dir, pid); err != nil {
			return nil, err
		}
	}
	name := strconv.Itoa(pid) + "-*"
	path, err := os.MkdirTemp(runsDir, name)
	if err != nil {
		return nil, err
	}
	d := &runDir{path: path}
	if d.tmp, err = os.MkdirTemp(tmpDir, name); err != nil {
		_ = d.remove()
		return nil, err
	}
	sandbox.SeedTmpDir(d.tmp)
	return d, nil
}

// checkSocketPath refuses a runs directory under which the longest run's
// socket path would pass what Go binds. The path is taken as given, never
// resolved: on macOS the per-user temp directory is under /var, a link to the
// longer /private/var.
func checkSocketPath(runs string) error {
	if n := len(runs) + len("/") + maxRunDirName + len("/"+socketName); n > maxSocketPath {
		return fmt.Errorf("the runs directory %s is too long: a run's socket would be %d bytes under it, and a socket's path can be at most %d", runs, n, maxSocketPath)
	}
	return nil
}

func (d *runDir) kubeconfig() string { return filepath.Join(d.path, "kubeconfig") }

func (d *runDir) socket() string { return filepath.Join(d.path, socketName) }

// resolver is the run's resolv.conf, for a run with the internet.
func (d *runDir) resolver() string { return filepath.Join(d.path, "resolv.conf") }

// removeLogged is remove at the end of a run, whose failure is the next
// start's sweep's.
func (d *runDir) removeLogged() {
	if err := d.remove(); err != nil {
		slog.Warn("could not remove a run's directories; the next start sweeps them", "err", err)
	}
}

// remove removes the run's directory and its TMPDIR.
func (d *runDir) remove() error {
	err := removeUnder(d.path)
	if d.tmp != "" {
		err = errors.Join(err, removeUnder(d.tmp))
	}
	return err
}

// removeUnder removes path through a root on its parent, so a link the command
// left in it is removed, never followed.
func removeUnder(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	return rootdir.RemoveAll(root, filepath.Base(path))
}
