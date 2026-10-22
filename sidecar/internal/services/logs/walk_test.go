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

package logs

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// A line is held by the page's chunks when it lies in one of them, two that touch or
// overlap read as one run, and a line in the gap between two runs is nobody's.
func TestHeldByJoinsChunksThatTouch(t *testing.T) {
	t.Parallel()
	pos := func(ts int64) logstore.Pos { return logstore.Pos{TS: ts} }
	held := heldBy([]logstore.Chunk{
		{From: pos(0), To: pos(10)},
		{From: pos(5), To: pos(8)},
		{From: pos(10), To: pos(20)},
		{From: pos(30), To: pos(40)},
	})
	for ts, want := range map[int64]bool{0: true, 7: true, 10: true, 15: true, 20: true, 25: false, 30: true, 40: true, 41: false} {
		assert.Equal(t, want, held(pos(ts)), ts)
	}
}
