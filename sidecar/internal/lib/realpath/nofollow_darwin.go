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

import "golang.org/x/sys/unix"

// atSymlinkNoFollowAny is AT_SYMLINK_NOFOLLOW_ANY, which x/sys does not
// define: a link on the way to the file is an error, and one at its end is
// answered as itself.
const atSymlinkNoFollowAny = 0x0800

// statNoFollow is the file at path, absolute and clean, or an error when a
// name before its last is a link. Like a stat it opens nothing, so no folder
// macOS guards.
func statNoFollow(path string, st *unix.Stat_t) error {
	return unix.Fstatat(unix.AT_FDCWD, path, st, atSymlinkNoFollowAny)
}
