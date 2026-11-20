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

package providerkeys

import (
	"net/http"
	"sync"
	"testing"

	"github.com/amorey/gochan/watch"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring/oskeyringtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/internal/signer"
)

// Two providers: one with a connector's place, one with two routes.
const (
	routerID  = "router"
	twinID    = "twin"
	routerAPI = "https://router.test/api/v1"
)

var (
	routerProvider = Provider{ID: routerID, Label: "Router", KeyVar: "ROUTER_API_KEY", Routes: []Route{
		{BaseURL: routerAPI, Header: "Authorization", Prefix: "Bearer "},
	}}
	twinProvider = Provider{ID: twinID, Label: "Twin", KeyVar: "TWIN_API_KEY", Routes: []Route{
		{BaseURL: "https://twin.test/openai/v1", Header: "Authorization", Prefix: "Bearer "},
		{BaseURL: "https://twin.test/anthropic", Header: "X-Api-Key"},
	}}
)

// Keys long enough for the redactor, each unique to its test.
const (
	envKey   = "env-key-0123456789abcdef"
	savedKey = "saved-key-0123456789abcdef"
	otherKey = "other-key-0123456789abcdef"
)

// newService builds a service over cfg's keyring, or a fresh one, closed at
// the test's end.
func newService(t *testing.T, cfg Config) *Service {
	t.Helper()
	if cfg.Keyring == nil {
		cfg.Keyring = oskeyringtest.New()
	}
	if cfg.Providers == nil {
		cfg.Providers = []Provider{routerProvider, twinProvider}
	}
	s, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

// state is id's state, which the test knows is there.
func state(t *testing.T, s *Service, id string) State {
	t.Helper()
	st, ok := s.State(id)
	require.True(t, ok, id)
	return st
}

// request is a request to url, unsigned.
func request(t *testing.T, url string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, err)
	return r
}

// fakeConnector is a provider's connector: it signs with "connector" while it has
// a session.
type fakeConnector struct {
	sessions *watch.Hub[uint64]

	mu      sync.Mutex
	session uint64
	refused int
}

func newFakeConnector() *fakeConnector {
	return &fakeConnector{sessions: watch.New[uint64](0)}
}

// connect starts session id; 0 ends it.
func (c *fakeConnector) connect(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = id
	c.sessions.Sender().Send(id) //nolint:errcheck // never closed
}

func (c *fakeConnector) Sign(r *http.Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == 0 {
		return llm.ErrSignedOut
	}
	r.Header.Set("Authorization", "Bearer connector")
	return nil
}

func (c *fakeConnector) Refused(*http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refused++
}

func (c *fakeConnector) Bind() llm.Signer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == 0 {
		return signer.Ended{}
	}
	return connectorBinding{c: c, session: c.session}
}

func (c *fakeConnector) WatchSession() (<-chan uint64, func()) {
	rx := c.sessions.Receiver()
	return rx.Chan(), rx.Close
}

// connectorBinding signs under one session of the connector's.
type connectorBinding struct {
	c       *fakeConnector
	session uint64
}

func (b connectorBinding) Sign(r *http.Request) error {
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	if b.c.session != b.session {
		return llm.ErrSignedOut
	}
	r.Header.Set("Authorization", "Bearer connector")
	return nil
}

func (b connectorBinding) Refused(*http.Request) {}

// await reads the gauge until id's state meets ok, failing on the timeout.
func await(t *testing.T, s *Service, id, what string, ok func(State) bool) State {
	t.Helper()
	frames, cancel := s.Subscribe()
	defer cancel()
	for {
		for _, st := range testutil.Recv(t, frames, what) {
			if st.ID == id && ok(st) {
				return st
			}
		}
	}
}

// signedBy is a state whose source is src.
func signedBy(src Source) func(State) bool {
	return func(st State) bool { return st.Source == src }
}
