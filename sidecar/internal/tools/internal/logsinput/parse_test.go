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

package logsinput

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webapp = `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]`

// A tool's own field is read into its destination, by its type; a key that is
// neither shared nor the tool's own is refused without its name.
func TestParseReadsAToolsOwnFields(t *testing.T) {
	t.Parallel()
	var mode string
	var limit int
	own := Fields{"mode": &mode, "limit": &limit, "anchor": BadInput("anchor", "not here")}

	in, err := Parse(json.RawMessage(webapp+`,"mode":"tail","limit":20}`), own)
	require.NoError(t, err)
	assert.Equal(t, "tail", mode)
	assert.Equal(t, 20, limit)
	assert.Equal(t, "webapp", in.Sources[0].Name)

	_, err = Parse(json.RawMessage(webapp+`,"hunter2":1}`), own)
	assert.Equal(t, `{"error":"bad-input"}`, Refusal(err))

	_, err = Parse(json.RawMessage(webapp+`,"anchor":"tail"}`), own)
	assert.Equal(t, `{"error":"bad-input","field":"anchor","message":"not here"}`, Refusal(err))

	for _, bad := range []string{`"20"`, `2.5`, `null`} {
		_, err = Parse(json.RawMessage(webapp+`,"limit":`+bad+`}`), own)
		assert.Equal(t, `{"error":"bad-input","field":"limit"}`, Refusal(err), bad)
	}
}

// A call names at most maxSources sources.
func TestMoreThanMaxSourcesIsRefused(t *testing.T) {
	t.Parallel()
	source := `{"namespace":"prod","resource":"pods/a"}`
	many := func(n int) json.RawMessage {
		return json.RawMessage(`{"sources":[` + strings.Repeat(source+",", n-1) + source + `]}`)
	}
	_, err := Parse(many(maxSources), nil)
	require.NoError(t, err)

	_, err = Parse(many(maxSources+1), nil)
	assert.Equal(t, `{"error":"bad-input","field":"sources","message":"at most 100"}`, Refusal(err))
}
