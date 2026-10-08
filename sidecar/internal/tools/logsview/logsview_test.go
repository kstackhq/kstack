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

package logsview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

func TestTheDefinitionIsTheEmbeddedSchema(t *testing.T) {
	def := New(nil).Definition()

	assert.Equal(t, Name, def.Name)
	assert.Equal(t, description, def.Description)
	assert.JSONEq(t, string(inputSchema), string(def.InputSchema))
}

func TestTheToolIsOfKindLogsView(t *testing.T) {
	tool := New(nil)

	assert.Equal(t, Name, tool.Name())
	assert.Equal(t, tools.ActionLogsView, tool.ActionKind())
	assert.Equal(t, prompt, tool.Prompt())
}

// Until the tool is built, no call has an action and every run is refused.
func TestASkeletonCallIsRefused(t *testing.T) {
	tool := New(nil)
	raw := json.RawMessage(`{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`)

	_, err := tool.Action(raw, "", false)
	require.ErrorIs(t, err, errNotImplemented)

	text, isError := tool.Run(t.Context(), tools.Runtime{}, raw)
	assert.True(t, isError)
	assert.Equal(t, `{"error":"not-implemented"}`, text)
}
