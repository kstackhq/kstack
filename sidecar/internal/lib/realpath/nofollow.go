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
	"os"
)

// ErrNotFoundNoFollow is NoFollowID's answer for a path that names no file with
// no link on it: one not absolute and clean, with a link on it, or with no
// file there. Any other error is a lookup that failed, which tells nothing of
// the path.
var ErrNotFoundNoFollow = errors.New("realpath: the path names no file with no link on it")

// SameFileNoFollow reports whether path, with no link followed on it, names
// file, an os.Stat's answer (NoFollowID, ID.Is). It reads the disk.
func SameFileNoFollow(path string, file os.FileInfo) bool {
	id, err := NoFollowID(path)
	return err == nil && id.Is(file)
}
