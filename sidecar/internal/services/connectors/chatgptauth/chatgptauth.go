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

// Package chatgptauth is Sign in with ChatGPT as oauthconn runs it: OpenAI's
// endpoints, scopes and codes, and what its authorize request adds. Its tokens
// spend the user's ChatGPT plan.
package chatgptauth

import (
	"net/url"

	"github.com/google/uuid"

	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/oauthconn"
)

const (
	// ID is the ChatGPT plan's provider id.
	ID = "chatgpt"
	// Issuer is OpenAI's auth server.
	Issuer = "https://auth.openai.com"
	// APIBaseURL is the one resource the tokens are for.
	APIBaseURL = "https://api.openai.com/v1"
	// UsageURL is ChatGPT's page that manages each app's access and usage.
	UsageURL = "https://chatgpt.com/settings/usage"

	agentName = "Kstack"
	// planScope is what lets the tokens spend the plan; signing in alone does not.
	planScope = "chatgpt.tokens.use.direct"
	// hostIDKey is the installation's host id in the row's provider_data.
	hostIDKey = "hostID"
	// clientGone is the token endpoint's code for a registration it no longer
	// knows.
	clientGone = "invalid_client"
)

// Provider is Sign in with ChatGPT.
func Provider() oauthconn.Provider {
	return oauthconn.Provider{
		ID:            ID,
		Issuer:        Issuer,
		APIBaseURL:    APIBaseURL,
		Scopes:        []string{"openid", "profile", "email", "offline_access", "resource.invoke", planScope},
		RequiredScope: planScope,
		CallbackPath:  "/auth/callback",
		FirstClientID: "dynamic_agent_client",
		NewData:       newData,
		Authorize:     authorize,
		SessionEnders: []string{
			"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired",
			"refresh_token_invalidated", "refresh_token_reused", clientGone,
		},
		ClientGone: clientGone,
	}
}

// newData mints the installation's host id, which every sign-in sends.
func newData() map[string]string {
	return map[string]string{hostIDKey: "urn:uuid:" + uuid.NewString()}
}

// authorize names the installation, and on the sign-in that registers a
// client, the app the client is for.
func authorize(data map[string]string, registering bool) url.Values {
	q := url.Values{"ext_agent_host_id": {data[hostIDKey]}}
	if registering {
		q.Set("agent_name_hint", agentName)
	}
	return q
}
