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

package chatgptauth

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The provider asks for the plan's scope and keeps no sign-in without it, and
// the code for a registration OpenAI no longer knows ends a session.
func TestTheProviderIsSignInWithChatGPT(t *testing.T) {
	p := Provider()

	assert.Equal(t, "chatgpt", p.ID)
	assert.Equal(t, "https://auth.openai.com", p.Issuer)
	assert.Equal(t, "https://api.openai.com/v1", p.APIBaseURL)
	assert.Contains(t, p.Scopes, p.RequiredScope)
	assert.Equal(t, "chatgpt.tokens.use.direct", p.RequiredScope)
	assert.Contains(t, p.SessionEnders, p.ClientGone)
}

// The installation's host id is minted once and sent on every authorize; the
// app's name goes only with the sign-in that registers a client.
func TestAuthorizeNamesTheInstallation(t *testing.T) {
	p := Provider()
	data := p.NewData()
	assert.Regexp(t, `^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, data["hostID"])
	assert.NotEqual(t, data, p.NewData())

	assert.Equal(t, url.Values{"ext_agent_host_id": {data["hostID"]}, "agent_name_hint": {"Kstack"}}, p.Authorize(data, true))
	assert.Equal(t, url.Values{"ext_agent_host_id": {data["hostID"]}}, p.Authorize(data, false))
}
