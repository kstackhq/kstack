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

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring/oskeyringtest"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/providerkeys"
	"github.com/kstackhq/kstack/sidecar/internal/tools/agent"
)

// No provider but Anthropic, and the fake that stands in for it, is offered a
// vendor's tool: every other list names only tools of ours.
func TestOnlyAnthropicAndTheFakeListAVendorTool(t *testing.T) {
	for id, list := range lists {
		if id == "anthropic" || id == "fake" {
			continue
		}
		assert.Subset(t, ours, list, id)
	}
}

// Every provider's turns may hand a task to an agent: the subagent's gated calls
// wait on the user as the parent's do.
func TestEveryListOffersAgent(t *testing.T) {
	for id, list := range lists {
		assert.Contains(t, list, agent.Name, id)
	}
}

// Every keyed provider is listed, in picker order, signed by its service with no
// key on it; one with no credential lists no model until it has one. The fake
// is listed last when given, and is the one a send on it runs on.
func TestNewListsEveryKeyedRowAndAddsTheFake(t *testing.T) {
	assert.Empty(t, New(Config{}).Providers(), "no service, no keyed provider")

	f := llm.NewFake(0)
	keys := newKeys(t, map[string]string{"openai": "sk-test-0123456789abcdef", "anthropic": "sk-ant-test-0123456789abcdef"}, nil)
	providers := New(Config{Keys: keys, Fake: f}).Providers()

	want := []string{"anthropic", "openai", "gemini", "groq", "mistral", "deepseek", "xai", "openrouter", "together", "fireworks", "opencode-go", "fake"}
	assert.Equal(t, want, providerIDs(providers), "picker order, the fake last")
	for _, p := range providers[:len(providers)-1] {
		assert.Empty(t, p.Key, p.ID)
		assert.NotNil(t, p.Signer, p.ID)
	}
	assert.Equal(t, newAnthropicProvider().Catalog, providers[0].Catalog)
	assert.Equal(t, newOpenAIProvider().Catalog, providers[1].Catalog)
	assert.Empty(t, providers[2].Catalog, "a provider with no credential lists no model")
	llm.New(providers...) // a provider holds a key or a signer, never both

	target, err := llm.New(providers...).Resolve("fake", "fake", "low")
	require.NoError(t, err)
	_, err = target.Stream(t.Context(), "", []llm.Message{{Role: "user", Blocks: []llm.Block{llm.TextBlock("hi")}}}, "", nil, nil, func(llm.Chunk) {})
	require.NoError(t, err)
	assert.Equal(t, 1, f.Asked(), "the fake given is the one a send runs on")
}

// While the keyring is unread, a provider with an environment key lists its
// written catalog, and one whose only key may be saved lists none yet.
func TestNewListsAnEnvironmentKeyBeforeTheKeyringAnswers(t *testing.T) {
	k := oskeyringtest.New()
	t.Cleanup(k.Hold(oskeyringtest.Get))
	keys, err := providerkeys.New(providerkeys.Config{
		Keyring: k, Providers: KeyedProviders(nil), EnvKeys: map[string]string{"openai": "sk-test-0123456789abcdef"},
	})
	require.NoError(t, err)
	t.Cleanup(keys.Close)

	providers := New(Config{Keys: keys}).Providers()

	assert.Equal(t, newOpenAIProvider().Catalog, providers[1].Catalog)
	assert.Empty(t, providers[0].Catalog, "anthropic's saved key is unread")
}

// A debug build's base URL moves the provider it names and nothing else.
func TestNewAppliesADebugBaseURLToItsRowAlone(t *testing.T) {
	baseURLs := map[string]string{"anthropic": "http://127.0.0.1:1", "nobody": "http://127.0.0.1:3"}
	providers := New(Config{Keys: newKeys(t, nil, baseURLs), BaseURLs: baseURLs}).Providers()

	assert.Equal(t, "http://127.0.0.1:1", providers[0].BaseURL)
	assert.Equal(t, newOpenAIProvider().BaseURL, providers[1].BaseURL)
}

// A debug base URL clears each model's base URL override too, so one stub
// answers the whole row, and each model keeps its dialect override.
func TestADebugBaseURLMovesEveryModelOfTheRow(t *testing.T) {
	baseURLs := map[string]string{"opencode-go": "http://127.0.0.1:1"}
	keys := newKeys(t, map[string]string{"opencode-go": "oc-test-0123456789abcdef"}, baseURLs)
	providers := New(Config{Keys: keys, BaseURLs: baseURLs}).Providers()

	p, row := providers[len(providers)-1], newOpenCodeGoProvider()
	require.Equal(t, "opencode-go", p.ID)
	require.NotEmpty(t, p.Catalog)
	for i, m := range p.Catalog {
		assert.Equal(t, "http://127.0.0.1:1", p.ForModel(m).BaseURL, m.ID)
		assert.Equal(t, row.Catalog[i].Dialect, m.Dialect, m.ID)
	}
}

// Every model of every keyed provider is on a dialect Stream speaks, so a key
// is all that stands between it and a send.
func TestEveryKeyedProviderResolves(t *testing.T) {
	svc := llm.New(everyProvider(t)...)

	for _, p := range svc.Providers() {
		for _, m := range p.Catalog {
			_, err := svc.Resolve(p.ID, m.ID, m.DefaultEffort)
			assert.NoError(t, err, "%s/%s", p.ID, m.ID)
		}
	}
}

// Every provider the catalog can list has a list, so none is offered nothing
// by omission.
func TestEveryProviderHasAList(t *testing.T) {
	for _, p := range everyProvider(t) {
		assert.Contains(t, lists, p.ID)
	}
}

// No two providers share an id, keyed and signed-in alike: llm.New would list
// both and resolve the id to the first, so a picker entry would send to the
// other row.
func TestNoTwoProvidersShareAnID(t *testing.T) {
	providers := everyProvider(t)
	require.Len(t, providers, len(keyedProviders())+len(signedProviders())+1, "every row is listed")
	seen := map[string]bool{}
	for _, p := range providers {
		assert.False(t, seen[p.ID], "%s is listed twice", p.ID)
		seen[p.ID] = true
	}
}

// everyProvider is every provider New can list: each keyed one with a key, each
// signed-in one with a signer, and the fake.
func everyProvider(t *testing.T) []llm.Provider {
	keys := map[string]string{}
	for id := range KeyVars() {
		keys[id] = "key-0123456789abcdef"
	}
	signers := map[string]llm.SessionSigner{}
	for _, sp := range signedProviders() {
		signers[sp.id] = planSigner{}
	}
	return New(Config{
		Keys: newKeys(t, keys, nil), Signers: signers, VertexBaseURL: vertexBase, Fake: llm.NewFake(0),
	}).Providers()
}

// providerIDs is providers' ids, in order.
func providerIDs(providers []llm.Provider) []string {
	ids := make([]string, 0, len(providers))
	for _, p := range providers {
		ids = append(ids, p.ID)
	}
	return ids
}

// Signers holds the signed-in providers alone: a signer for a keyed provider lists
// nothing.
func TestAKeyedRowTakesNoSigner(t *testing.T) {
	providers := New(Config{Signers: map[string]llm.SessionSigner{"openai": planSigner{}}}).Providers()

	assert.Empty(t, providers)
}
