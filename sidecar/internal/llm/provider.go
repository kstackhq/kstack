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

package llm

// Provider is one entry of the catalog: who serves models, on which wire, and which.
type Provider struct {
	// ID is what a send and a stored message name: "anthropic", "fake".
	ID string
	// Label is the picker's group heading: "Anthropic".
	Label string
	// Dialect and BaseURL are the defaults for every model of Catalog, which a
	// model may override (ForModel). BaseURL is stated here: an SDK's own
	// default is off.
	Dialect Dialect
	BaseURL string
	// Key is the credential. Empty for a provider that needs none.
	Key string
	// Catalog is the models this provider offers, in picker order.
	Catalog []Model
	// Extra is this provider's own request parameters, each keyed by its JSON path
	// into the body: a vendor's own knob is a line of data on its entry, never a
	// branch in a dialect.
	Extra map[string]any
	// KeyHeader is the header the key goes in, for an endpoint that takes it
	// neither in the dialect's own header nor as a bearer. Empty sends it the
	// dialect's way.
	KeyHeader string
	// Query is added to every request's URL.
	Query map[string]string
	// Omit is deleted from every request body, each entry a JSON path: a field
	// the dialect sends that an endpoint refuses.
	Omit []string
	// AffinityRoute is where Request.AffinityKey goes, for a vendor that routes a
	// conversation to the machine holding its cached prefix by a key.
	AffinityRoute AffinityRoute

	// fake is the Fake a provider on DialectFake answers from: the one provider
	// whose code is a value, since a test steers it. Nil on every other dialect.
	fake *Fake
}

// ForModel is p with m's overrides applied: m's dialect and base URL where m
// states them, p's defaults where it does not. Every Target holds the provider
// this returns, so a wire reads one dialect and one URL off Target.Provider.
func (p Provider) ForModel(m Model) Provider {
	if m.Dialect != "" {
		p.Dialect = m.Dialect
	}
	if m.BaseURL != "" {
		p.BaseURL = m.BaseURL
	}
	return p
}

// AffinityRoute is where Request.AffinityKey goes on a provider's endpoint: one
// of the two, or neither to send it nowhere.
type AffinityRoute struct {
	Header string // "x-grok-conv-id"
	Field  string // a JSON path into the body: "prompt_cache_key"
}

// Fake is the fake a provider on DialectFake answers from, for a test to steer;
// nil on every other dialect.
func (p Provider) Fake() *Fake { return p.fake }
