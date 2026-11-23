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

package realpath

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// An ID is a file as the disk tells it from every other, whatever names it.
type ID struct {
	st unix.Stat_t
}

// NoFollowID is the ID of the file path names with no link followed on it,
// ErrNotFoundNoFollow when it names none, or the error of a lookup that failed.
// The kernel refuses path's links in the lookup that answers the file
// (statNoFollow), so no change to the disk between two reads can lead path
// elsewhere. It reads the disk.
func NoFollowID(path string) (ID, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ID{}, ErrNotFoundNoFollow
	}
	var id ID
	switch err := statNoFollow(path, &id.st); {
	case err == unix.ENOENT || err == unix.ENOTDIR || err == unix.ELOOP:
		return ID{}, ErrNotFoundNoFollow
	case err != nil:
		return ID{}, err
	case id.st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return ID{}, ErrNotFoundNoFollow
	}
	return id, nil
}

// Is reports whether file, an os.Stat's answer, is the ID's file. It reads
// no disk.
func (id ID) Is(file os.FileInfo) bool {
	st, ok := file.Sys().(*syscall.Stat_t)
	return ok && id.st.Dev == st.Dev && id.st.Ino == st.Ino
}

// Same reports whether other is the ID's file. It reads no disk.
func (id ID) Same(other ID) bool {
	return id.st.Dev == other.st.Dev && id.st.Ino == other.st.Ino
}
