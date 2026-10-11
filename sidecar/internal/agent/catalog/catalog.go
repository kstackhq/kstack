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

// Package catalog is the providers the app holds and, for each, every tool its
// turns are offered.
package catalog

import (
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/tools/agent"
	"github.com/kstackhq/kstack/sidecar/internal/tools/anthropicwebsearch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
	"github.com/kstackhq/kstack/sidecar/internal/tools/edit"
	"github.com/kstackhq/kstack/sidecar/internal/tools/kubequery"
	"github.com/kstackhq/kstack/sidecar/internal/tools/logsview"
	"github.com/kstackhq/kstack/sidecar/internal/tools/memory"
	"github.com/kstackhq/kstack/sidecar/internal/tools/read"
	"github.com/kstackhq/kstack/sidecar/internal/tools/taskstop"
	"github.com/kstackhq/kstack/sidecar/internal/tools/webfetch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/write"
)

// Catalog is the providers the app holds, and the tools each one's turns are
// offered.
type Catalog struct {
	providers []llm.Provider
}

// Config is what New builds the providers from.
type Config struct {
	// APIKeys is each keyed provider's API key, by provider id. A provider with
	// no key here is left out.
	APIKeys map[string]string
	// BaseURLs replaces a listed provider's base URL, by provider id. Set only by
	// a debug build: a release never takes an endpoint from the environment.
	BaseURLs map[string]string
	// Fake, when non-nil, is listed last: a run with no key still has a model to
	// answer on.
	Fake *llm.Fake
}

// New is every keyed provider whose key cfg holds, in picker order, at the base
// URL cfg moves it to, then the fake.
func New(cfg Config) Catalog {
	var c Catalog
	for _, kp := range keyedProviders() {
		key, ok := cfg.APIKeys[kp.provider.ID]
		if !ok {
			continue
		}
		kp.provider.Key = key
		if url, ok := cfg.BaseURLs[kp.provider.ID]; ok {
			kp.provider.BaseURL = url
		}
		c.providers = append(c.providers, kp.provider)
	}
	if cfg.Fake != nil {
		c.providers = append(c.providers, llm.FakeProvider(cfg.Fake))
	}
	return c
}

// Providers is every provider New listed, in picker order, for llm.New.
func (c Catalog) Providers() []llm.Provider { return c.providers }

// ToolsFor is the tools a turn on t is offered, by name: its provider's list,
// or none for a provider with no list.
func (c Catalog) ToolsFor(t llm.Target) []string {
	return lists[t.Provider.ID]
}

// ours is every tool the app defines, which every provider is offered unless
// its list says otherwise.
var ours = []string{bash.Name, read.Name, memory.Name, write.Name, edit.Name, webfetch.Name, taskstop.Name, agent.Name, kubequery.Name, logsview.Name}

// withSearch is ours and the Messages API's web search.
var withSearch = append(slices.Clone(ours), anthropicwebsearch.Name)

// lists is each provider's tools, by provider id, one tool per ActionKind. A
// list is policy: it decides which code runs each kind of action for a
// provider, and what leaves the machine for it, so adding a vendor's tool to a
// list, or leaving one of ours off, is a security change.
var lists = map[string][]string{
	"anthropic":  withSearch,
	"openai":     ours,
	"gemini":     ours,
	"groq":       ours,
	"mistral":    ours,
	"deepseek":   ours,
	"xai":        ours,
	"openrouter": ours,
	"together":   ours,
	"fireworks":  ours,
	"fake":       withSearch,
}
