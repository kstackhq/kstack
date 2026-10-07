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

// A keyed provider is listed when its key is given, with the key on it, and
// left out when not; the fake is listed after them when given, and is the one
// a send on it runs on.
func TestNewListsTheKeyedProvidersAndAddsTheFake(t *testing.T) {
	assert.Empty(t, New(Config{}).Providers())

	f := llm.NewFake(0)
	providers := New(Config{APIKeys: map[string]string{"openai": "sk-test", "anthropic": "sk-ant-test"}, Fake: f}).Providers()

	require.Len(t, providers, 3)
	assert.Equal(t, []string{"anthropic", "openai", "fake"}, providerIDs(providers), "picker order, the fake last")
	assert.Equal(t, "sk-ant-test", providers[0].Key)
	assert.Equal(t, "sk-test", providers[1].Key)
	target, err := llm.New(providers...).Resolve("fake", "fake", "low")
	require.NoError(t, err)
	_, err = target.Stream(t.Context(), "", []llm.Message{{Role: "user", Blocks: []llm.Block{llm.TextBlock("hi")}}}, "", nil, nil, func(llm.Chunk) {})
	require.NoError(t, err)
	assert.Equal(t, 1, f.Asked(), "the fake given is the one a send runs on")
}

// A debug build's base URL moves the listed provider it names and nothing else:
// a URL for a provider with no key, or for an id with no row, lists nothing.
func TestNewAppliesADebugBaseURLToAListedProviderAlone(t *testing.T) {
	providers := New(Config{
		APIKeys: map[string]string{"anthropic": "sk-ant-test", "openai": "sk-test"},
		BaseURLs: map[string]string{
			"anthropic": "http://127.0.0.1:1",
			"groq":      "http://127.0.0.1:2",
			"nobody":    "http://127.0.0.1:3",
		},
	}).Providers()

	assert.Equal(t, []string{"anthropic", "openai"}, providerIDs(providers))
	assert.Equal(t, "http://127.0.0.1:1", providers[0].BaseURL)
	assert.Equal(t, newOpenAIProvider().BaseURL, providers[1].BaseURL)
}

// Every keyed provider is on a dialect Stream speaks, so a key is all that
// stands between it and a send.
func TestEveryKeyedProviderResolves(t *testing.T) {
	svc := llm.New(everyProvider()...)

	for _, p := range svc.Providers() {
		_, err := svc.Resolve(p.ID, p.Catalog[0].ID, p.Catalog[0].DefaultEffort)
		assert.NoError(t, err, p.ID)
	}
}

// Every provider the catalog can list has a list, so none is offered nothing
// by omission.
func TestEveryProviderHasAList(t *testing.T) {
	for _, p := range everyProvider() {
		assert.Contains(t, lists, p.ID)
	}
}

// No two providers share an id: llm.New would list both and resolve the id to
// the last, so a picker entry would send to the other row.
func TestNoTwoProvidersShareAnID(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range everyProvider() {
		assert.False(t, seen[p.ID], "%s is listed twice", p.ID)
		seen[p.ID] = true
	}
}

// everyProvider is every provider New can list, each keyed, and the fake.
func everyProvider() []llm.Provider {
	keys := map[string]string{}
	for id := range KeyVars() {
		keys[id] = "key"
	}
	return New(Config{APIKeys: keys, Fake: llm.NewFake(0)}).Providers()
}

// providerIDs is providers' ids, in order.
func providerIDs(providers []llm.Provider) []string {
	ids := make([]string, 0, len(providers))
	for _, p := range providers {
		ids = append(ids, p.ID)
	}
	return ids
}
