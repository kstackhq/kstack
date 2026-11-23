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

package settings

import (
	"errors"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/lib/realpath"
)

// targetLookup compares a stored Target with a fresh folder. A Target held
// no link when it was stored, and a state holds for the folder it named. So
// a stored Target is a fresh folder in another spelling only when the two
// differ in case alone, the stored one has no link on it now, wherever the
// link leads, and it names the file the filter checked (pathDir.info), never
// the fresh path read again: a state carries to no folder the filter did not
// see. Any other Target is another folder, by its text, and is not read.
type targetLookup struct {
	// noFollowID is realpath.NoFollowID, or a test's stand-in.
	noFollowID func(path string) (realpath.ID, error)
}

func newTargetLookup() targetLookup {
	return targetLookup{noFollowID: realpath.NoFollowID}
}

// respells reports whether a and b differ, and in case alone.
func respells(a, b string) bool {
	return a != b && strings.EqualFold(a, b)
}

// lookUp is the file target names with no link followed on it, found false
// when it names none.
func (l targetLookup) lookUp(target string) (id realpath.ID, found bool, err error) {
	id, err = l.noFollowID(target)
	if errors.Is(err, realpath.ErrNotFoundNoFollow) {
		return realpath.ID{}, false, nil
	}
	return id, err == nil, err
}

// sameFolder reports whether stored is fresh's folder. It reads the disk
// only for a stored Target that respells fresh's, and err is that read's
// failure, which tells nothing of the two.
func (l targetLookup) sameFolder(stored string, fresh pathDir) (bool, error) {
	if !respells(stored, fresh.Target) {
		return stored == fresh.Target, nil
	}
	id, found, err := l.lookUp(stored)
	return found && id.Is(fresh.info), err
}
