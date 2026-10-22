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

package logsread

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

func TestTheToolIsAShownBoundedRead(t *testing.T) {
	t.Parallel()
	tool := New(nil, time.Now)
	def := tool.Definition()
	assert.Equal(t, Name, def.Name)
	assert.Equal(t, description, def.Description)
	assert.JSONEq(t, string(inputSchema), string(def.InputSchema))
	assert.Equal(t, tools.ActionLogsRead, tool.ActionKind())
	assert.Equal(t, prompt, tool.Prompt())
	_, err := tool.Action(json.RawMessage(`{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`), "", false)
	assert.ErrorIs(t, err, errNotShown)
}
