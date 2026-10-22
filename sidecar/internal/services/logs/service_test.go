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
)

// A page's walks read up to 64 MiB of the file, split evenly across its streams: about
// 640 KiB each at a hundred.
func TestAPagesWalksReadSixtyFourMiB(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())

	assert.Equal(t, int64(64<<20), svc.walkBudget)
}
