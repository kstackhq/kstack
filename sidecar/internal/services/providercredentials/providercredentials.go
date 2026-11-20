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

// Package providercredentials holds each provider's API keys, the
// environment's and the one saved from the Providers pane, and signs the
// provider's requests with the highest credential present: its connector, then
// the saved key, then the environment key. A provider that takes no key is
// signed by its connector alone.
//
// Files: this one (Service, New, state, the watches), keyring.go (the entry),
// save.go (Save, Delete), sign.go (the signers).
package providercredentials

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/internal/catalogsync"
	"github.com/kstackhq/kstack/sidecar/internal/services/poke"
)

// Source is which credential signs a provider.
type Source string

const (
	SourceNone           Source = "None"
	SourceEnvironmentKey Source = "EnvironmentKey"
	SourceSavedKey       Source = "SavedKey"
	SourceConnector      Source = "Connector"
)

// Failure is what is wrong with a provider's key.
type Failure string

const (
	// NotDeleted is a Delete the keyring refused: the key is out of memory,
	// still in the keyring, and returns at the next launch. It is reported
	// over KeyRejected.
	NotDeleted Failure = "NotDeleted"
	// KeyRejected is a key the provider answered 401 to. It is kept, and signs.
	KeyRejected Failure = "KeyRejected"
)

var (
	ErrUnknownProvider = errors.New("providercredentials: no such provider")
	ErrBadKey          = errors.New("providercredentials: not an API key")
	ErrNotSaved        = errors.New("providercredentials: the keyring refused the key")
	ErrNotDeleted      = errors.New("providercredentials: the keyring refused the delete")
	// ErrNoKey is a Save or Delete for a provider that takes no key.
	ErrNoKey = errors.New("providercredentials: the provider takes no key")
)

// State is one provider as every window draws it. It holds no key.
type State struct {
	ID, Label, KeyVar string
	Source            Source
	// Which keys are held, whichever signs.
	HasEnvironmentKey bool
	HasSavedKey       bool
	// Failure is "" for none.
	Failure Failure
}

// Provider is a provider as the service needs it. catalog.KeyedProviders
// builds them.
type Provider struct {
	ID, Label string
	// KeyVar is "" for a provider that takes no key.
	KeyVar string
	// Routes is every base URL the provider's models are sent to, each with the
	// header its key goes in.
	Routes []Route
}

// Route is a base URL and the header a key goes in there.
type Route struct {
	BaseURL string
	Header  string // "Authorization", "X-Api-Key"
	Prefix  string // "Bearer " or ""
}

// Connector is a provider's connector: it signs in the keys' place while it
// has a session. Its Bind must answer signer.Ended{} whenever it has no
// session, and nothing else then: that value is how the provider knows to bind
// a key.
type Connector interface {
	llm.SessionSigner
	// WatchSession is the connected session's generation, 0 while there is
	// none: current on subscribe, then each change. A token or key rotated
	// within a session keeps its generation.
	WatchSession() (<-chan uint64, func())
}

// Config is what New builds the service from.
type Config struct {
	Keyring oskeyring.Keyring
	// Providers is every provider, in picker order.
	Providers []Provider
	// EnvKeys is the launch's key per provider id.
	EnvKeys map[string]string
	// Connectors is each provider's connector, by provider id.
	Connectors map[string]Connector
}

// Service holds every provider's keys. writes serializes Save and Delete
// and is the only lock held across a keyring call; mu guards every provider's
// state, and each change is published under it, so a frame never carries one
// provider's older state after a newer one went out. mu is never held across a call to
// the keyring or a connector.
type Service struct {
	keyring   oskeyring.Keyring
	providers []*provider
	byID      map[string]*provider

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	writes sync.Mutex
	mu     sync.Mutex
	states *watch.Hub[[]State]
}

// provider is one provider's keys. Every field below bases is guarded by
// mu.
type provider struct {
	Provider
	bases     []*url.URL // Routes' base URLs, parsed
	env       string
	connector Connector
	// credential is WatchCredential's value.
	credential *watch.Hub[uint64]

	saved string
	// keyGen is the keys' generation. It moves when the saved key held in
	// memory changes and when the connector starts a session: a binding made
	// under a key signs until it moves.
	keyGen uint64
	// connectorGen is the connector's session's generation, 0 for none.
	connectorGen uint64
	// undeleted is a saved key the keyring would not delete; rejected is a 401
	// to the key that signs; newer is an entry a newer Kstack wrote.
	undeleted, rejected, newer bool
}

// New builds the service and restores every saved key. It reaches no network.
func New(cfg Config) (*Service, error) {
	s := &Service{keyring: cfg.Keyring, byID: map[string]*provider{}}
	for _, in := range cfg.Providers {
		p, err := newProvider(in)
		if err != nil {
			return nil, err
		}
		if _, dup := s.byID[p.ID]; dup {
			return nil, fmt.Errorf("providercredentials: %s is listed twice", p.ID)
		}
		p.connector = cfg.Connectors[p.ID]
		if p.KeyVar != "" {
			p.env = cfg.EnvKeys[p.ID]
		}
		s.providers = append(s.providers, p)
		s.byID[p.ID] = p
	}
	// Each read can start a process (macOS's security), so they run at once. A
	// provider with no key has no entry.
	var restores sync.WaitGroup
	for _, p := range s.providers {
		if p.KeyVar == "" {
			continue
		}
		restores.Go(func() { p.saved, p.newer = s.restore(p.ID) })
	}
	restores.Wait()

	// Nothing else holds s until the followers start, so this needs no lock. Each
	// subscribes before the hubs are built, so it misses no generation.
	var follows []func()
	for _, p := range s.providers {
		if p.connector != nil {
			gens, unsubscribe := p.connector.WatchSession()
			p.connectorGen = <-gens
			follows = append(follows, func() { s.follow(p, gens, unsubscribe) })
		}
		p.credential = watch.New(p.credentialLocked())
	}
	s.states = watch.New(s.statesLocked())
	s.ctx, s.stop = context.WithCancel(context.Background())
	for _, follow := range follows {
		follow()
	}
	return s, nil
}

func newProvider(in Provider) (*provider, error) {
	if in.ID == "" {
		return nil, errors.New("providercredentials: a provider has no id")
	}
	p := &provider{Provider: in}
	for _, rt := range in.Routes {
		base, err := url.Parse(rt.BaseURL)
		if err != nil || base.Host == "" {
			return nil, fmt.Errorf("providercredentials: %s's base URL %q has no host", in.ID, rt.BaseURL)
		}
		p.bases = append(p.bases, base)
	}
	return p, nil
}

// follow republishes p on each change of its connector's generation until
// Close. A new session ends every binding made under a key.
func (s *Service) follow(p *provider, gens <-chan uint64, unsubscribe func()) {
	s.wg.Go(func() {
		defer unsubscribe()
		for {
			select {
			case <-s.ctx.Done():
				return
			case gen := <-gens:
				s.mu.Lock()
				if gen != 0 && gen != p.connectorGen {
					p.keyGen++
				}
				p.connectorGen = gen
				s.publishLocked(p)
				s.mu.Unlock()
			}
		}
	})
}

// States is every provider's state, in Providers order.
func (s *Service) States() []State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statesLocked()
}

// State is id's state.
func (s *Service) State(id string) (State, bool) {
	p, ok := s.byID[id]
	if !ok {
		return State{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.stateLocked(), true
}

// Subscribe streams every provider's state: current on subscribe, then each change.
func (s *Service) Subscribe() (<-chan []State, func()) {
	rx := s.states.Receiver()
	return rx.Chan(), rx.Close
}

// WatchCredential is what id's catalog is read under: 0 with no credential, 1
// for a provider that takes a key, whichever credential signs, and its
// connector's session's generation for one that takes none. Current on
// subscribe, then each change. A keyed provider's catalog is the same under
// every credential; a keyless one's is its account's, so each new session is a
// new value.
func (s *Service) WatchCredential(id string) (<-chan uint64, func()) {
	rx := s.byID[id].credential.Receiver()
	return rx.Chan(), rx.Close
}

// SyncCatalogs keeps each provider's catalog listed while WatchCredential is
// not 0, read again for each new value, until the stop it returns.
func (s *Service) SyncCatalogs(pokes *poke.Service, models *llm.Service, read func(context.Context, llm.Provider) ([]llm.Model, error)) (stop func()) {
	var stops []func()
	for _, p := range s.providers {
		stops = append(stops, catalogsync.Start(p.ID, func() (<-chan uint64, func()) { return s.WatchCredential(p.ID) }, pokes, models, read))
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}

// Close ends the goroutines following the connectors and waits for them.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

func (s *Service) statesLocked() []State {
	states := make([]State, len(s.providers))
	for i, p := range s.providers {
		states[i] = p.stateLocked()
	}
	return states
}

// publishLocked sends every provider's state, and p's credential (s.mu held).
func (s *Service) publishLocked(p *provider) {
	s.states.Sender().Send(s.statesLocked())         //nolint:errcheck // Send never blocks, and the hub is never closed
	p.credential.Sender().Send(p.credentialLocked()) //nolint:errcheck // as above
}

func (p *provider) stateLocked() State {
	src := source(p.connectorGen, p.saved != "", p.env != "")
	return State{
		ID: p.ID, Label: p.Label, KeyVar: p.KeyVar,
		Source:            src,
		HasEnvironmentKey: p.env != "",
		HasSavedKey:       p.saved != "",
		Failure:           failure(p.undeleted, p.rejected, src),
	}
}

func (p *provider) credentialLocked() uint64 {
	switch {
	case p.KeyVar == "":
		return p.connectorGen
	case p.stateLocked().Source == SourceNone:
		return 0
	}
	return 1
}

// source is the credential that signs: the highest one present.
func source(connectorGen uint64, saved, env bool) Source {
	switch {
	case connectorGen != 0:
		return SourceConnector
	case saved:
		return SourceSavedKey
	case env:
		return SourceEnvironmentKey
	}
	return SourceNone
}

// failure is what the provider reports. A failed delete hides a rejection, and a
// rejection counts only while a key signs.
func failure(undeleted, rejected bool, src Source) Failure {
	switch {
	case undeleted:
		return NotDeleted
	case rejected && (src == SourceEnvironmentKey || src == SourceSavedKey):
		return KeyRejected
	}
	return ""
}
