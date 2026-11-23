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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// An ID is a file as the disk tells it from every other, whatever names it.
type ID struct {
	info os.FileInfo
}

// NoFollowID is the ID of the file path names with no link on it,
// ErrNotFoundNoFollow when it names none, or the error of a lookup that failed.
// Each name is read on its own, and Is may read path again, so a change to
// the disk between the reads can lead path elsewhere. It reads the disk.
func NoFollowID(path string) (ID, error) {
	// filepath.Dir cleans, so on a path that is not clean the walk below
	// would step lexically past a link a .. follows; a relative one would end
	// at the working folder.
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ID{}, ErrNotFoundNoFollow
	}
	var named os.FileInfo
	for at := path; ; at = filepath.Dir(at) {
		info, err := os.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return ID{}, ErrNotFoundNoFollow
		case err != nil:
			return ID{}, err
		case IsLink(info):
			return ID{}, ErrNotFoundNoFollow
		}
		if named == nil {
			named = info
		}
		if at == filepath.Dir(at) {
			break
		}
	}
	return ID{info: named}, nil
}

// Is reports whether file, an os.Stat's answer, is the ID's file. The two
// are compared as files: a folder can tell names apart by case where
// filepath does not. It may read the disk at either's path.
func (id ID) Is(file os.FileInfo) bool {
	return os.SameFile(id.info, file)
}

// Same reports whether other is the ID's file, compared as Is compares. It
// may read the disk at either's path.
func (id ID) Same(other ID) bool {
	return os.SameFile(id.info, other.info)
}
