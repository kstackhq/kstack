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

// Every keyed provider says everything a send needs: an id, a label, a dialect
// with a wire, a base URL, its key variable, and a catalog with one entry per
// model id, since a send names a model by its id alone. A cap is required
// on the two dialects whose wire refuses a request without one, and a model's
// default is one of its own efforts, since that is what a picker starts on.
func TestEveryKeyedProviderIsWhole(t *testing.T) {
	for _, kp := range keyedProviders() {
		p := kp.provider
		assert.NotEmpty(t, p.ID)
		assert.NotEmpty(t, p.Label, p.ID)
		assert.Contains(t, []llm.Dialect{llm.DialectMessages, llm.DialectResponses, llm.DialectChatCompletions}, p.Dialect, p.ID)
		assert.NotEmpty(t, p.BaseURL, p.ID)
		assert.NotEmpty(t, kp.keyVar, p.ID)
		assert.NotEmpty(t, p.Catalog, p.ID)
		var ids []string
		for _, m := range p.Catalog {
			assert.NotEmpty(t, m.ID, p.ID)
			assert.NotContains(t, ids, m.ID, "%s lists %s twice", p.ID, m.ID)
			ids = append(ids, m.ID)
			assert.NotEmpty(t, m.Label, m.ID)
			if len(m.Efforts) == 0 {
				assert.Empty(t, m.DefaultEffort, m.ID)
			} else {
				assert.Contains(t, m.Efforts, m.DefaultEffort, m.ID)
			}
			if p.Dialect == llm.DialectMessages || p.Dialect == llm.DialectResponses {
				assert.Positive(t, m.MaxOutputTokens, m.ID)
			}
		}
	}
}

// modelIDs is a provider's catalog in picker order.
func modelIDs(p llm.Provider) []string {
	ids := make([]string, 0, len(p.Catalog))
	for _, m := range p.Catalog {
		ids = append(ids, m.ID)
	}
	return ids
}

// effortsOf is a provider's catalog as the efforts each model lists, by model id.
// A model listing none is left out.
func effortsOf(p llm.Provider) map[string][]string {
	out := map[string][]string{}
	for _, m := range p.Catalog {
		if len(m.Efforts) > 0 {
			out[m.ID] = m.Efforts
		}
	}
	return out
}

// windowsOf is a provider's catalog as the window each model states, by model
// id. A model stating none is left out.
func windowsOf(p llm.Provider) map[string]int {
	out := map[string]int{}
	for _, m := range p.Catalog {
		if m.ContextWindow > 0 {
			out[m.ID] = m.ContextWindow
		}
	}
	return out
}

// Each row is written out whole and as its comment says: the dialect, where it
// goes, the catalog in picker order, the efforts each model takes in the
// vendor's own words, and the request parameters of the vendor's own. The two
// dialects that require a cap get the written one on every model.
func TestEachKeyedProviderIsWrittenAsItsRow(t *testing.T) {
	rows := []struct {
		id, label string
		dialect   llm.Dialect
		baseURL   string
		models    []string
		efforts   map[string][]string
		extra     map[string]any
		windows   map[string]int
	}{
		{"anthropic", "Anthropic", llm.DialectMessages, "https://api.anthropic.com",
			[]string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"},
			map[string][]string{
				"claude-fable-5-1":  {"low", "medium", "high", "xhigh", "max"},
				"claude-opus-5-5":   {"low", "medium", "high", "xhigh", "max"},
				"claude-sonnet-5-5": {"low", "medium", "high", "xhigh", "max"},
				"claude-haiku-5-5":  {"low", "medium", "high", "xhigh", "max"},
			}, nil,
			map[string]int{"claude-fable-5-1": 1_000_000, "claude-opus-5-5": 1_000_000, "claude-sonnet-5-5": 1_000_000, "claude-haiku-5-5": 1_000_000}},
		{"openai", "OpenAI", llm.DialectResponses, "https://api.openai.com/v1",
			[]string{"gpt-6-astra", "gpt-5.6", "gpt-5.6-terra", "gpt-5.6-luna"},
			map[string][]string{
				"gpt-6-astra":   {"low", "medium", "high", "xhigh", "max"},
				"gpt-5.6":       {"none", "low", "medium", "high", "xhigh", "max"},
				"gpt-5.6-terra": {"none", "low", "medium", "high", "xhigh", "max"},
				"gpt-5.6-luna":  {"none", "low", "medium", "high", "xhigh", "max"},
			}, nil,
			map[string]int{"gpt-6-astra": 1_050_000, "gpt-5.6": 1_050_000, "gpt-5.6-terra": 1_050_000, "gpt-5.6-luna": 1_050_000}},
		{"gemini", "Gemini", llm.DialectChatCompletions, "https://generativelanguage.googleapis.com/v1beta/openai",
			[]string{"gemini-3.1-pro-preview", "gemini-3.8-flash", "gemini-3.5-flash-lite"},
			map[string][]string{
				"gemini-3.1-pro-preview": {"low", "medium", "high"},
				"gemini-3.8-flash":       {"low", "medium", "high"},
				"gemini-3.5-flash-lite":  {"minimal", "low", "medium", "high"},
			}, nil,
			map[string]int{"gemini-3.1-pro-preview": 1_048_576, "gemini-3.8-flash": 1_048_576, "gemini-3.5-flash-lite": 1_048_576}},
		{"groq", "Groq", llm.DialectChatCompletions, "https://api.groq.com/openai/v1",
			[]string{"openai/gpt-oss-120b", "openai/gpt-oss-20b", "qwen/qwen3.8-27b", "qwen/qwen3.6-27b"},
			map[string][]string{
				"openai/gpt-oss-120b": {"low", "medium", "high"},
				"openai/gpt-oss-20b":  {"low", "medium", "high"},
				"qwen/qwen3.8-27b":    {"none", "low", "medium", "high"},
				"qwen/qwen3.6-27b":    {"none", "default"},
			}, nil,
			map[string]int{"openai/gpt-oss-120b": 131_072, "openai/gpt-oss-20b": 131_072, "qwen/qwen3.8-27b": 262_144, "qwen/qwen3.6-27b": 131_072}},
		{"mistral", "Mistral", llm.DialectChatCompletions, "https://api.mistral.ai/v1",
			[]string{"mistral-large-latest", "mistral-medium-latest", "mistral-small-latest"},
			map[string][]string{}, nil,
			map[string]int{}},
		{"deepseek", "DeepSeek", llm.DialectChatCompletions, "https://api.deepseek.com/v1",
			[]string{"deepseek-chat", "deepseek-reasoner"},
			map[string][]string{}, nil,
			map[string]int{}},
		{"xai", "xAI", llm.DialectChatCompletions, "https://api.x.ai/v1",
			[]string{"grok-4.6", "grok-4.5", "grok-4.3"},
			map[string][]string{
				"grok-4.6": {"low", "medium", "high", "xhigh"},
				"grok-4.5": {"low", "medium", "high"},
				"grok-4.3": {"none", "low", "medium", "high"},
			}, nil,
			map[string]int{"grok-4.6": 500_000, "grok-4.5": 500_000, "grok-4.3": 1_000_000}},
		{"openrouter", "OpenRouter", llm.DialectChatCompletions, "https://openrouter.ai/api/v1",
			[]string{"openrouter/auto", "openai/gpt-5.6", "anthropic/claude-haiku-4.5"},
			map[string][]string{}, map[string]any{"provider.data_collection": "deny"},
			map[string]int{}},
		{"together", "Together", llm.DialectChatCompletions, "https://api.together.xyz/v1",
			[]string{"deepseek-ai/DeepSeek-V3", "meta-llama/Llama-3.3-70B-Instruct-Turbo", "Qwen/Qwen2.5-72B-Instruct-Turbo"},
			map[string][]string{}, nil,
			map[string]int{}},
		{"fireworks", "Fireworks", llm.DialectChatCompletions, "https://api.fireworks.ai/inference/v1",
			[]string{"accounts/fireworks/models/deepseek-v3", "accounts/fireworks/models/llama-v3p3-70b-instruct", "accounts/fireworks/models/qwen3-235b-a22b"},
			map[string][]string{}, nil,
			map[string]int{}},
	}
	keyed := keyedProviders()
	require.Len(t, keyed, len(rows))
	for i, want := range rows {
		p := keyed[i].provider
		assert.Equal(t, want.id, p.ID)
		assert.Equal(t, want.label, p.Label, p.ID)
		assert.Equal(t, want.dialect, p.Dialect, p.ID)
		assert.Equal(t, want.baseURL, p.BaseURL, p.ID)
		assert.Equal(t, want.models, modelIDs(p), p.ID)
		assert.Equal(t, want.efforts, effortsOf(p), p.ID)
		assert.Equal(t, want.extra, p.Extra, p.ID)
		assert.Equal(t, want.windows, windowsOf(p), p.ID)
		if p.Dialect == llm.DialectMessages || p.Dialect == llm.DialectResponses {
			for _, m := range p.Catalog {
				assert.Equal(t, writtenCap, m.MaxOutputTokens, m.ID)
			}
		}
	}
}

// The provider denies data collection on every request, which is what the egress
// record rests on: OpenRouter routes only to downstream providers it classifies
// as not collecting user data.
func TestOpenRouterProviderDeniesDataCollection(t *testing.T) {
	assert.Equal(t, map[string]any{"provider.data_collection": "deny"}, newOpenRouterProvider().Extra)
}

// KeyVars is the table by provider id, and the fake is not in it.
func TestKeyVarsIsTheKeyedTable(t *testing.T) {
	assert.Equal(t, map[string]string{
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"gemini":     "GEMINI_API_KEY",
		"groq":       "GROQ_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"deepseek":   "DEEPSEEK_API_KEY",
		"xai":        "XAI_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"together":   "TOGETHER_API_KEY",
		"fireworks":  "FIREWORKS_API_KEY",
	}, KeyVars())
}

// Each keyed provider's base URL moves under a variable of its own, and the fake,
// with no URL, under none. A hyphen in an id, which no shell variable can hold,
// is spelled as an underscore.
func TestBaseURLVarsNamesEachProvidersOverride(t *testing.T) {
	assert.Equal(t, map[string]string{
		"anthropic":  "KSTACK_ANTHROPIC_BASE_URL",
		"openai":     "KSTACK_OPENAI_BASE_URL",
		"gemini":     "KSTACK_GEMINI_BASE_URL",
		"groq":       "KSTACK_GROQ_BASE_URL",
		"mistral":    "KSTACK_MISTRAL_BASE_URL",
		"deepseek":   "KSTACK_DEEPSEEK_BASE_URL",
		"xai":        "KSTACK_XAI_BASE_URL",
		"openrouter": "KSTACK_OPENROUTER_BASE_URL",
		"together":   "KSTACK_TOGETHER_BASE_URL",
		"fireworks":  "KSTACK_FIREWORKS_BASE_URL",
	}, BaseURLVars())
	assert.Equal(t, "KSTACK_BEDROCK_GPT_BASE_URL", baseURLVar("bedrock-gpt"))
}

// Every model of a wire whose whole catalog takes tools says so.
func TestEveryModelOnAWireThatCarriesToolsTakesThem(t *testing.T) {
	for _, kp := range keyedProviders() {
		if kp.provider.Dialect != llm.DialectMessages && kp.provider.Dialect != llm.DialectResponses {
			continue
		}
		for _, m := range kp.provider.Catalog {
			assert.True(t, m.Tools, m.ID)
		}
	}
}

// DeepSeek's reasoner is the one entry of its wire held back: its documentation
// has said both that a round's reasoning_content must come back on the assistant
// message carrying the calls and that sending it back is refused, and this wire's
// walk sends no thinking either way. The flag waits on a run with a key.
func TestDeepSeeksReasonerTakesNoTools(t *testing.T) {
	p := newDeepSeekProvider()

	for _, m := range p.Catalog {
		assert.Equal(t, m.ID == "deepseek-chat", m.Tools, m.ID)
	}
}

// Every Claude model reads a million tokens.
func TestAnthropicCatalogStatesAContextWindow(t *testing.T) {
	for _, m := range newAnthropicProvider().Catalog {
		assert.Equal(t, 1_000_000, m.ContextWindow, m.ID)
	}
}

// Each Claude model defaults to the effort the API gives it when none is sent.
func TestAnthropicCatalogDefaultsToTheAPIsEffort(t *testing.T) {
	defaults := map[string]string{}
	for _, m := range newAnthropicProvider().Catalog {
		defaults[m.ID] = m.DefaultEffort
	}
	assert.Equal(t, map[string]string{
		"claude-fable-5-1":  "high",
		"claude-opus-5-5":   "medium",
		"claude-sonnet-5-5": "high",
		"claude-haiku-5-5":  "medium",
	}, defaults)
}

// Every entry states how its provider caches it, since the wire marks a prompt by
// that word alone. Every Messages entry caches on marks, and so does Claude
// through OpenRouter; Groq documents no cache on its Qwen models; every other
// entry caches on its own.
func TestEveryModelStatesItsCaching(t *testing.T) {
	notAuto := map[string]llm.CacheKind{
		"openrouter/anthropic/claude-haiku-4.5": llm.CacheMarks,
		"groq/qwen/qwen3.8-27b":                 llm.CacheNone,
		"groq/qwen/qwen3.6-27b":                 llm.CacheNone,
	}
	providers := []llm.Provider{llm.FakeProvider(nil)}
	for _, kp := range keyedProviders() {
		providers = append(providers, kp.provider)
	}
	for _, p := range providers {
		for _, m := range p.Catalog {
			want, ok := notAuto[p.ID+"/"+m.ID]
			switch {
			case p.Dialect == llm.DialectMessages:
				want = llm.CacheMarks
			case !ok:
				want = llm.CacheAuto
			}
			assert.Equal(t, want, m.Cache, "%s/%s", p.ID, m.ID)
		}
	}
}

// A route puts the key in one place: a header or a body field, never both.
func TestEveryAffinityRouteNamesOnePlace(t *testing.T) {
	for _, kp := range keyedProviders() {
		r := kp.provider.AffinityRoute
		assert.False(t, r.Header != "" && r.Field != "", kp.provider.ID)
	}
}

// Only the vendors that document a key routing a conversation to its cache get
// one, where each documents it. OpenAI's prompt_cache_key only separates cache
// accounting, so the openai row sends none.
func TestEachRowRoutesTheAffinityKeyWhereItsVendorSays(t *testing.T) {
	routes := map[string]llm.AffinityRoute{}
	for _, kp := range keyedProviders() {
		if kp.provider.AffinityRoute != (llm.AffinityRoute{}) {
			routes[kp.provider.ID] = kp.provider.AffinityRoute
		}
	}
	assert.Equal(t, map[string]llm.AffinityRoute{
		"mistral":    {Field: "prompt_cache_key"},
		"xai":        {Header: "x-grok-conv-id"},
		"openrouter": {Field: "session_id"},
		"fireworks":  {Header: "x-session-affinity"},
	}, routes)
}

// A stated window holds the cap every request asks for, so no request is refused
// for its cap alone.
func TestEveryWrittenWindowHoldsTheCap(t *testing.T) {
	for _, kp := range keyedProviders() {
		for _, m := range kp.provider.Catalog {
			if m.ContextWindow > 0 {
				assert.Greater(t, m.ContextWindow, m.MaxOutputTokens, m.ID)
			}
		}
	}
}
