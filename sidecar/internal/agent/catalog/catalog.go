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
	"github.com/kstackhq/kstack/sidecar/internal/services/providercredentials"
	"github.com/kstackhq/kstack/sidecar/internal/tools/agent"
	"github.com/kstackhq/kstack/sidecar/internal/tools/anthropicwebsearch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
	"github.com/kstackhq/kstack/sidecar/internal/tools/edit"
	"github.com/kstackhq/kstack/sidecar/internal/tools/kubequery"
	"github.com/kstackhq/kstack/sidecar/internal/tools/logsread"
	"github.com/kstackhq/kstack/sidecar/internal/tools/logsview"
	"github.com/kstackhq/kstack/sidecar/internal/tools/logswatch"
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
	// Keys signs every keyed provider. Nil leaves them out.
	Keys *providercredentials.Service
	// BaseURLs replaces a listed provider's base URL, by provider id. Set only by
	// a debug build: a release never takes an endpoint from the environment.
	BaseURLs map[string]string
	// Signers is each signed-in provider's signer, by provider id. A provider
	// with no signer here is left out. They are listed after the keyed providers
	// and before the fake.
	Signers map[string]llm.SessionSigner
	// VertexBaseURL is Vertex AI's endpoint in the user's project, which the
	// google row signs. Empty leaves the row out.
	VertexBaseURL string
	// Fake, when non-nil, is listed last: a run with no key still has a model to
	// answer on.
	Fake *llm.Fake
}

// New is every keyed provider in picker order, at the base URL cfg moves it
// to, then each signed-in provider cfg holds a signer and a base URL for, then
// the fake. A keyed provider with no credential lists no model until it has
// one.
func New(cfg Config) Catalog {
	var c Catalog
	if cfg.Keys != nil {
		for _, kp := range keyedProvidersAt(cfg.BaseURLs) {
			p := kp.provider
			p.Signer = cfg.Keys.Signer(p.ID)
			if st, _ := cfg.Keys.State(p.ID); st.Source == providercredentials.SourceNone {
				// SyncCatalogs lists it once the provider has a credential.
				p.Catalog = nil
			}
			c.providers = append(c.providers, p)
		}
	}
	for _, sp := range signedProviders() {
		signer, ok := cfg.Signers[sp.id]
		if !ok {
			continue
		}
		p := sp.row(cfg, signer)
		if url, ok := cfg.BaseURLs[sp.id]; ok {
			p.BaseURL = url
		}
		if p.BaseURL == "" {
			continue
		}
		c.providers = append(c.providers, p)
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
var ours = []string{bash.Name, read.Name, memory.Name, write.Name, edit.Name, webfetch.Name, taskstop.Name, agent.Name, kubequery.Name, logsview.Name, logsread.Name, logswatch.Name}

// withSearch is ours and the Messages API's web search.
var withSearch = append(slices.Clone(ours), anthropicwebsearch.Name)

// lists is each provider's tools, by provider id, one tool per ActionKind. A
// list is policy: it decides which code runs each kind of action for a
// provider, and what leaves the machine for it, so adding a vendor's tool to a
// list, or leaving one of ours off, is a security change.
var lists = map[string][]string{
	"anthropic":   withSearch,
	"openai":      ours,
	"gemini":      ours,
	"groq":        ours,
	"mistral":     ours,
	"deepseek":    ours,
	"xai":         ours,
	"openrouter":  ours,
	"together":    ours,
	"fireworks":   ours,
	"opencode-go": ours,
	ChatGPTID:     ours,
	GoogleID:      ours,
	"fake":        withSearch,
}
