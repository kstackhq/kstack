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

package realpath

import (
	"strings"

	"golang.org/x/sys/unix"
)

// statNoFollow is the file at path, absolute and clean, or an error when a
// name on path is a link. Each name is opened from its folder's descriptor
// with no link followed. O_PATH opens nothing to read, so it needs no access
// to the file.
func statNoFollow(path string, st *unix.Stat_t) error {
	fd, err := unix.Open("/", unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil { // coverage:ignore the root always opens
		return err
	}
	defer func() { unix.Close(fd) }()
	for name := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
		if name == "" { // path is the root
			break
		}
		// A link opens as itself, and the name after it fails as under no folder.
		next, err := unix.Openat(fd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		unix.Close(fd)
		fd = next
	}
	return unix.Fstat(fd, st)
}
