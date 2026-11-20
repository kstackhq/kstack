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

package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
)

const vertexBase = "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/endpoints/openapi"

// The Google row is listed after ChatGPT's and before the fake when its signer
// and the project's endpoint are given, with no models until it connects.
func TestTheGoogleRowIsListedWhenConfigured(t *testing.T) {
	f := llm.NewFake(0)
	signer := planSigner{token: "t"}

	providers := New(Config{
		Signers:       map[string]llm.SessionSigner{ChatGPTID: signer, GoogleID: signer},
		VertexBaseURL: vertexBase,
		Fake:          f,
	}).Providers()

	assert.Equal(t, []string{"chatgpt", "google", "fake"}, providerIDs(providers))
	p := providers[1]
	assert.Equal(t, "Vertex AI", p.Label)
	assert.Equal(t, llm.DialectChatCompletions, p.Dialect)
	assert.Equal(t, vertexBase, p.BaseURL)
	assert.Equal(t, signer, p.Signer)
	assert.Empty(t, p.Key)
	assert.Empty(t, p.Catalog)
}

// With no project there is no endpoint to sign: the Google provider is left out.
func TestTheGoogleRowIsLeftOut(t *testing.T) {
	assert.Empty(t, New(Config{Signers: map[string]llm.SessionSigner{GoogleID: planSigner{}}}).Providers())
}

// gemini and google are two providers: a Gemini key does not hide Vertex AI.
func TestTheGoogleRowIsListedBesideAGeminiKey(t *testing.T) {
	providers := New(Config{
		Keys:          newKeys(t, map[string]string{"gemini": "gm-0123456789abcdef"}, nil),
		Signers:       map[string]llm.SessionSigner{GoogleID: planSigner{}},
		VertexBaseURL: vertexBase,
	}).Providers()
	assert.Contains(t, providerIDs(providers), "gemini")
	assert.Contains(t, providerIDs(providers), GoogleID)
}

func TestADebugBuildMovesTheGoogleRow(t *testing.T) {
	providers := New(Config{
		Signers:       map[string]llm.SessionSigner{GoogleID: planSigner{}},
		VertexBaseURL: vertexBase,
		BaseURLs:      map[string]string{GoogleID: "http://127.0.0.1:1/v1"},
	}).Providers()

	require.Len(t, providers, 1)
	assert.Equal(t, "http://127.0.0.1:1/v1", providers[0].BaseURL)
}

// The row's turns are offered our tools, and nothing of a vendor's.
func TestTheGoogleRowIsOfferedOurTools(t *testing.T) {
	assert.Equal(t, ours, New(Config{}).ToolsFor(llm.Target{Provider: llm.Provider{ID: GoogleID}}))
}

// A connected account's catalog is the Gemini row's models under Vertex AI's
// publisher ids, read without a request.
func TestReadSignedModelsAnswersTheVertexCatalog(t *testing.T) {
	models, err := ReadSignedModels(t.Context(), llm.Provider{ID: GoogleID, BaseURL: "http://127.0.0.1:1"})

	require.NoError(t, err)
	gemini := newGeminiProvider().Catalog
	require.Len(t, models, len(gemini))
	for i, m := range models {
		want := gemini[i]
		want.ID = "google/" + want.ID
		assert.Equal(t, want, m)
	}
	llm.New(llm.Provider{ID: GoogleID, Dialect: llm.DialectChatCompletions, Catalog: models}) // every model resolves
}
