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
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
)

// keyedProvider is a provider and the environment variable that keys it.
type keyedProvider struct {
	provider llm.Provider
	keyVar   string
}

// keyedProviders is every keyed provider, in picker order. The fake is not here:
// it needs no key, and New lists it last when it is given one. Every catalog is
// written, and no test reaches a vendor, so each row's comment says whether its
// ids were checked against the vendor's GET /models; one marked UNVERIFIED is
// owed that check by whoever holds a key.
func keyedProviders() []keyedProvider {
	return []keyedProvider{
		{newAnthropicProvider(), "ANTHROPIC_API_KEY"},
		{newOpenAIProvider(), "OPENAI_API_KEY"},
		{newGeminiProvider(), "GEMINI_API_KEY"},
		{newGroqProvider(), "GROQ_API_KEY"},
		{newMistralProvider(), "MISTRAL_API_KEY"},
		{newDeepSeekProvider(), "DEEPSEEK_API_KEY"},
		{newXAIProvider(), "XAI_API_KEY"},
		{newOpenRouterProvider(), "OPENROUTER_API_KEY"},
		{newTogetherProvider(), "TOGETHER_API_KEY"},
		{newFireworksProvider(), "FIREWORKS_API_KEY"},
	}
}

// KeyVars is the environment variable that keys each provider, by provider id: the
// table the config reads keys from and clears out of the environment.
func KeyVars() map[string]string {
	vars := map[string]string{}
	for _, kp := range keyedProviders() {
		vars[kp.provider.ID] = kp.keyVar
	}
	return vars
}

// BaseURLVars is the variable that moves each provider's base URL in a debug
// build, by provider id: the table the config reads overrides from. The fake has
// no URL and is not in it.
func BaseURLVars() map[string]string {
	vars := map[string]string{}
	for _, kp := range keyedProviders() {
		vars[kp.provider.ID] = baseURLVar(kp.provider.ID)
	}
	return vars
}

// baseURLVar is KSTACK_<ID>_BASE_URL, the id upper-cased and each '-' as '_',
// since a shell variable's name holds no hyphen.
func baseURLVar(id string) string {
	return "KSTACK_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_BASE_URL"
}

// The rows, in picker order. Each is one vendor: where its requests go and the
// models it is written to serve.

// writtenCap is the output cap every written catalog asks for: room for a long
// answer with streaming on, at or under each listed model's own maximum. The
// Messages and Responses APIs require a cap on every request.
const writtenCap = 64_000

// claudeEfforts is the Messages API's own scale, least to most.
var claudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// newAnthropicProvider is the Messages API provider: the current Claude lineup in
// picker order. Every id was verified against GET /v1/models/{id} before it was
// written, since no test can catch a wrong one. Each default effort is the API's
// own for that model.
func newAnthropicProvider() llm.Provider {
	return llm.Provider{
		ID: "anthropic", Label: "Anthropic", Dialect: llm.DialectMessages,
		BaseURL: "https://api.anthropic.com",
		Catalog: []llm.Model{
			{ID: "claude-fable-5-1", Label: "Claude Fable 5.1", Efforts: claudeEfforts, DefaultEffort: "high", MaxOutputTokens: writtenCap, ContextWindow: 1_000_000, Tools: true, Cache: llm.CacheMarks},
			{ID: "claude-opus-5-5", Label: "Claude Opus 5.5", Efforts: claudeEfforts, DefaultEffort: "medium", MaxOutputTokens: writtenCap, ContextWindow: 1_000_000, Tools: true, Cache: llm.CacheMarks},
			{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5", Efforts: claudeEfforts, DefaultEffort: "high", MaxOutputTokens: writtenCap, ContextWindow: 1_000_000, Tools: true, Cache: llm.CacheMarks},
			{ID: "claude-haiku-5-5", Label: "Claude Haiku 5.5", Efforts: claudeEfforts, DefaultEffort: "medium", MaxOutputTokens: writtenCap, ContextWindow: 1_000_000, Tools: true, Cache: llm.CacheMarks},
		},
	}
}

// The efforts each GPT lineup takes, least to most. Astra always reasons, so it
// has no none.
var (
	gptEfforts   = []string{"none", "low", "medium", "high", "xhigh", "max"}
	astraEfforts = []string{"low", "medium", "high", "xhigh", "max"}
)

// newOpenAIProvider is the Responses API provider: the current GPT lineup in
// picker order, `gpt-5.6` being the alias that routes to Sol. Ids and efforts
// verified.
func newOpenAIProvider() llm.Provider {
	return llm.Provider{
		ID: "openai", Label: "OpenAI", Dialect: llm.DialectResponses,
		BaseURL: "https://api.openai.com/v1",
		Catalog: []llm.Model{
			{ID: "gpt-6-astra", Label: "GPT-6 Astra", Efforts: astraEfforts, DefaultEffort: "medium", MaxOutputTokens: writtenCap, ContextWindow: 1_050_000, Tools: true, Cache: llm.CacheAuto},
			{ID: "gpt-5.6", Label: "GPT-5.6 Sol", Efforts: gptEfforts, DefaultEffort: "medium", MaxOutputTokens: writtenCap, ContextWindow: 1_050_000, Tools: true, Cache: llm.CacheAuto},
			{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra", Efforts: gptEfforts, DefaultEffort: "medium", MaxOutputTokens: writtenCap, ContextWindow: 1_050_000, Tools: true, Cache: llm.CacheAuto},
			{ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna", Efforts: gptEfforts, DefaultEffort: "medium", MaxOutputTokens: writtenCap, ContextWindow: 1_050_000, Tools: true, Cache: llm.CacheAuto},
		},
	}
}

// geminiEfforts is the thinking budget in Google's own words, least to most.
var geminiEfforts = []string{"low", "medium", "high"}

// newGeminiProvider is Google's OpenAI-compatible endpoint, the lineup its
// models page lists. Ids and efforts verified; what the endpoint shows of the
// thinking is not, since no key has been held here. Every model caches
// implicitly; whether this endpoint reports a hit is UNVERIFIED.
func newGeminiProvider() llm.Provider {
	return llm.Provider{
		ID: "gemini", Label: "Gemini", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		Catalog: []llm.Model{
			{ID: "gemini-3.1-pro-preview", Label: "Gemini 3.1 Pro", Efforts: geminiEfforts, DefaultEffort: "high", MaxOutputTokens: 65_536, ContextWindow: 1_048_576, Tools: true, Cache: llm.CacheAuto},
			{ID: "gemini-3.8-flash", Label: "Gemini 3.8 Flash", Efforts: geminiEfforts, DefaultEffort: "medium", MaxOutputTokens: 65_536, ContextWindow: 1_048_576, Tools: true, Cache: llm.CacheAuto},
			{ID: "gemini-3.5-flash-lite", Label: "Gemini 3.5 Flash-Lite", Efforts: []string{"minimal", "low", "medium", "high"}, DefaultEffort: "medium", MaxOutputTokens: 65_536, ContextWindow: 1_048_576, Tools: true, Cache: llm.CacheAuto},
		},
	}
}

// ossEfforts is what the GPT-OSS models take, least to most.
var ossEfforts = []string{"low", "medium", "high"}

// newGroqProvider is Groq's OpenAI-compatible endpoint: open-weight models on
// its own hardware, in the order its models page shows them, each model's
// efforts in its own words. Ids and efforts verified; what Groq shows of the
// thinking is not, since no key has been held here. Groq documents a cache on
// the GPT-OSS models alone.
func newGroqProvider() llm.Provider {
	return llm.Provider{
		ID: "groq", Label: "Groq", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://api.groq.com/openai/v1",
		Catalog: []llm.Model{
			{ID: "openai/gpt-oss-120b", Label: "GPT-OSS 120B", Efforts: ossEfforts, DefaultEffort: "medium", MaxOutputTokens: 65_536, ContextWindow: 131_072, Tools: true, Cache: llm.CacheAuto},
			{ID: "openai/gpt-oss-20b", Label: "GPT-OSS 20B", Efforts: ossEfforts, DefaultEffort: "medium", MaxOutputTokens: 65_536, ContextWindow: 131_072, Tools: true, Cache: llm.CacheAuto},
			{ID: "qwen/qwen3.8-27b", Label: "Qwen 3.8 27B", Efforts: []string{"none", "low", "medium", "high"}, DefaultEffort: "medium", MaxOutputTokens: 16_384, ContextWindow: 262_144, Tools: true, Cache: llm.CacheNone},
			{ID: "qwen/qwen3.6-27b", Label: "Qwen 3.6 27B", Efforts: []string{"none", "default"}, DefaultEffort: "default", MaxOutputTokens: 16_384, ContextWindow: 131_072, Tools: true, Cache: llm.CacheNone},
		},
	}
}

// newMistralProvider is Mistral's own endpoint: its three chat models, each by
// the `-latest` alias it keeps across releases. Ids UNVERIFIED.
func newMistralProvider() llm.Provider {
	return llm.Provider{
		ID: "mistral", Label: "Mistral", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://api.mistral.ai/v1",
		Catalog: []llm.Model{
			{ID: "mistral-large-latest", Label: "Mistral Large", Tools: true, Cache: llm.CacheAuto},
			{ID: "mistral-medium-latest", Label: "Mistral Medium", Tools: true, Cache: llm.CacheAuto},
			{ID: "mistral-small-latest", Label: "Mistral Small", Tools: true, Cache: llm.CacheAuto},
		},
		// Mistral's docs name a conversation id as the key's value.
		AffinityRoute: llm.AffinityRoute{Field: "prompt_cache_key"},
	}
}

// newDeepSeekProvider is DeepSeek's own endpoint: two aliases onto the current
// release, the chat model and the one that reasons. Ids UNVERIFIED.
func newDeepSeekProvider() llm.Provider {
	return llm.Provider{
		ID: "deepseek", Label: "DeepSeek", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://api.deepseek.com/v1",
		Catalog: []llm.Model{
			{ID: "deepseek-chat", Label: "DeepSeek Chat", Tools: true, Cache: llm.CacheAuto},
			{ID: "deepseek-reasoner", Label: "DeepSeek Reasoner", Cache: llm.CacheAuto},
		},
	}
}

// newXAIProvider is xAI's own endpoint. No model states an output cap, so the
// vendor's own default applies. Ids and efforts verified; what xAI shows of the
// thinking is not, since no key has been held here.
func newXAIProvider() llm.Provider {
	return llm.Provider{
		ID: "xai", Label: "xAI", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://api.x.ai/v1",
		Catalog: []llm.Model{
			{ID: "grok-4.6", Label: "Grok 4.6", Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high", ContextWindow: 500_000, Tools: true, Cache: llm.CacheAuto},
			{ID: "grok-4.5", Label: "Grok 4.5", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high", ContextWindow: 500_000, Tools: true, Cache: llm.CacheAuto},
			{ID: "grok-4.3", Label: "Grok 4.3", Efforts: []string{"none", "low", "medium", "high"}, DefaultEffort: "high", ContextWindow: 1_000_000, Tools: true, Cache: llm.CacheAuto},
		},
		// xAI caches per server, and this header keeps a conversation on one.
		AffinityRoute: llm.AffinityRoute{Header: "x-grok-conv-id"},
	}
}

// newOpenRouterProvider is OpenRouter, which routes to other vendors: `auto`
// picks one per request, and the two ids beside it name theirs. Extra restricts
// routing to downstream providers OpenRouter classifies as not collecting user
// data: its classification, not zero retention. Ids UNVERIFIED. Claude caches
// only on a mark; `auto`'s caching is whichever model it picks, UNVERIFIED.
func newOpenRouterProvider() llm.Provider {
	return llm.Provider{
		ID: "openrouter", Label: "OpenRouter", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://openrouter.ai/api/v1",
		Catalog: []llm.Model{
			{ID: "openrouter/auto", Label: "Auto", Cache: llm.CacheAuto},
			{ID: "openai/gpt-5.6", Label: "GPT-5.6 Sol", Tools: true, Cache: llm.CacheAuto},
			{ID: "anthropic/claude-haiku-4.5", Label: "Claude Haiku 4.5", Tools: true, Cache: llm.CacheMarks},
		},
		Extra: map[string]any{"provider.data_collection": "deny"},
		// Sticky routing: a session's requests go to the provider holding its cache.
		AffinityRoute: llm.AffinityRoute{Field: "session_id"},
	}
}

// newTogetherProvider is Together's inference endpoint: open-weight models by
// the ids its models page prints. Ids UNVERIFIED, and so is caching: Together
// caches a model that has a cached-input price.
func newTogetherProvider() llm.Provider {
	return llm.Provider{
		ID: "together", Label: "Together", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://api.together.xyz/v1",
		Catalog: []llm.Model{
			{ID: "deepseek-ai/DeepSeek-V3", Label: "DeepSeek V3", Cache: llm.CacheAuto},
			{ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Label: "Llama 3.3 70B", Cache: llm.CacheAuto},
			{ID: "Qwen/Qwen2.5-72B-Instruct-Turbo", Label: "Qwen 2.5 72B", Cache: llm.CacheAuto},
		},
	}
}

// newFireworksProvider is Fireworks' inference endpoint. Its ids are account
// paths, and the served models live under accounts/fireworks. Ids UNVERIFIED.
func newFireworksProvider() llm.Provider {
	return llm.Provider{
		ID: "fireworks", Label: "Fireworks", Dialect: llm.DialectChatCompletions,
		BaseURL: "https://api.fireworks.ai/inference/v1",
		Catalog: []llm.Model{
			{ID: "accounts/fireworks/models/deepseek-v3", Label: "DeepSeek V3", Tools: true, Cache: llm.CacheAuto},
			{ID: "accounts/fireworks/models/llama-v3p3-70b-instruct", Label: "Llama 3.3 70B", Tools: true, Cache: llm.CacheAuto},
			{ID: "accounts/fireworks/models/qwen3-235b-a22b", Label: "Qwen3 235B", Tools: true, Cache: llm.CacheAuto},
		},
		// Fireworks caches per replica, and this header keeps a session on one.
		AffinityRoute: llm.AffinityRoute{Header: "x-session-affinity"},
	}
}
