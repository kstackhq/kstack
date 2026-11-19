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

// Package oauthconn is a sign-in to a service with OAuth: the client, issued
// to this installation or fixed, the session, and its tokens. A Service is one
// provider's connection and signs the requests to its API base
// (llm.SessionSigner), none when it has no base. What the provider has of its
// own is its Provider, whose ID keys its row and its keyring accounts.
//
// Files: this one (Service, New, the session's end), provider.go (the
// provider's own part), registration.go (the provider's row in app.db),
// keyring.go (the refresh token's entry), connect.go (the sign-in), sign.go
// (Sign, Bind, the refresh), disconnect.go (sign-out and revocation).
package oauthconn

import (
	"cmp"
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oauth"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oauth/browser"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
)

const (
	attemptTimeout = 5 * time.Minute
	// issuerTimeout bounds one answer of the issuer's: discovery, a code
	// exchange with its verification, a refresh.
	issuerTimeout = 30 * time.Second
	revokeTimeout = 10 * time.Second
)

// Config is the service's production knobs.
type Config struct {
	// Provider is the provider signed in to.
	Provider Provider
	// DB is app.db, which holds the registration.
	DB *appdb.DB
	// KeychainService is the OS-keyring service the refresh token is kept under.
	KeychainService string
}

// Service is one provider's connection. Every change to the session happens under
// mu, against gen: Connect, Cancel, Disconnect and a session's end each bump it,
// and a sign-in, a refresh or a revocation that finishes applies only to the
// generation it began under.
type Service struct {
	p              Provider
	base           *url.URL
	keyring        oskeyring.Keyring
	reg            *registrationStore
	open           func(url string) error
	attemptTimeout time.Duration

	// ctx bounds every goroutine the service starts; Close cancels it and joins
	// them.
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	hub *watch.Hub[connectors.State]
	tx  *watch.Sender[connectors.State]
	// sessions holds the generation while Connected, 0 otherwise.
	sessions *watch.Hub[uint64]

	mu         sync.Mutex
	gen        uint64
	state      connectors.State
	session    *session
	attempt    *attempt
	refreshing *refresh
	endpoints  *oauth.Endpoints
	// consent asks the next authorize for consent again: the last one did not
	// grant the required scope.
	consent bool
}

// option is an unexported build seam, so only in-package tests pass one.
type option func(*Service)

func withKeyring(k oskeyring.Keyring) option { return func(s *Service) { s.keyring = k } }

func withBrowser(open func(string) error) option { return func(s *Service) { s.open = open } }

func withAttemptTimeout(d time.Duration) option { return func(s *Service) { s.attemptTimeout = d } }

// withRegistrationWriter wraps the row's writer.
func withRegistrationWriter(wrap func(registrationWriter) registrationWriter) option {
	return func(s *Service) { s.reg.write = wrap(s.reg.write) }
}

// New builds the service over its stores and restores a stored session. It
// reaches no network: endpoints are discovered at the first sign-in or refresh.
func New(cfg Config) (*Service, error) {
	return newWithOptions(cfg)
}

func newWithOptions(cfg Config, opts ...option) (*Service, error) {
	base, err := url.Parse(cfg.Provider.APIBaseURL)
	if err != nil { // coverage:ignore the base is a constant, or a debug build's own
		panic("oauthconn: bad API base URL")
	}
	reg, err := openRegistration(cfg.DB, cfg.Provider.ID)
	if err != nil {
		return nil, err
	}
	readErr := reg.load()
	ctx, stop := context.WithCancel(context.Background())
	s := &Service{
		p:              cfg.Provider,
		base:           base,
		keyring:        oskeyring.New(cfg.KeychainService),
		reg:            reg,
		open:           browser.Open,
		attemptTimeout: attemptTimeout,
		ctx:            ctx,
		stop:           stop,
		hub:            watch.New(connectors.State{}),
		sessions:       watch.New[uint64](0),
	}
	s.tx = s.hub.Sender()
	for _, opt := range opts {
		opt(s)
	}
	if readErr != nil {
		// The entry is left for a launch that can read the row, unless a Connect
		// rewrites the row first, which ends that session.
		slog.Warn("oauth: could not read the registration; treating as signed out", "provider", s.p.ID, "err", readErr)
		s.publishLocked(connectors.State{Phase: connectors.SignedOut})
		return s, nil
	}
	s.publishLocked(s.restore())
	return s, nil
}

// restore reads both stores. A session exists only when the row and the
// keyring's entry name the same sign-in. Anything else is a sign-in or a
// sign-out that a crash, a lost commit or a failed delete left half done, or a
// store restored from a backup: the row is signed out and the entry deleted.
func (s *Service) restore() connectors.State {
	reg := s.reg.get()
	e, found, err := loadEntry(s.keyring, s.refreshAccount())
	if err != nil {
		// A locked keyring is no proof of a sign-out: write nothing.
		slog.Warn("oauth: could not read the keyring; treating as signed out", "provider", s.p.ID, "err", err)
		return connectors.State{Phase: connectors.SignedOut}
	}
	clientID := s.clientID(reg)
	if reg.SessionID != "" && e.Session == reg.SessionID && clientID != "" && e.Token != "" {
		safe.AddSecret(e.Token)
		s.gen++
		s.session = &session{id: reg.SessionID, clientID: clientID, refresh: e.Token}
		return connectors.State{Phase: connectors.Connected, Subject: reg.Subject, Email: reg.Email, Name: reg.Name}
	}
	if reg.SessionID != "" {
		reg.SessionID = ""
		if err := s.reg.save(reg); err != nil {
			slog.Warn("oauth: could not record the sign-out", "provider", s.p.ID, "err", err)
		}
	}
	if found {
		if err := s.keyring.Delete(s.refreshAccount()); err != nil {
			slog.Warn("oauth: could not delete a stale keyring entry", "provider", s.p.ID, "err", err)
		}
	}
	return connectors.State{Phase: connectors.SignedOut}
}

// State is the connector's current state.
func (s *Service) State() connectors.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Subscribe streams the state: current on subscribe, then each change.
func (s *Service) Subscribe() (<-chan connectors.State, func()) {
	rx := s.hub.Receiver()
	return rx.Chan(), rx.Close
}

// WatchSession streams the connected session: its generation while Connected,
// 0 otherwise. Current on subscribe, then each change.
func (s *Service) WatchSession() (<-chan uint64, func()) {
	rx := s.sessions.Receiver()
	return rx.Chan(), rx.Close
}

// Close ends every goroutine the service started, waits for them, and closes
// its statements.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
	if err := s.reg.close(); err != nil { // coverage:ignore closing prepared statements does not fail
		slog.Warn("oauth: could not close the statements", "provider", s.p.ID, "err", err)
	}
}

// publishLocked makes st the state, stamped with the generation and whether a
// client is registered (s.mu held).
func (s *Service) publishLocked(st connectors.State) {
	st.Session = s.gen
	st.Registered = s.reg.get().ClientID != ""
	s.state = st
	s.tx.Send(st) //nolint:errcheck // Send never blocks, and the hub is never closed
	var session uint64
	if st.Phase == connectors.Connected {
		session = s.gen
	}
	s.sessions.Sender().Send(session) //nolint:errcheck // as above
}

// clearLocked ends the session in memory, then in the stores: the generation,
// the row's session, then the refresh token's entry. Each step runs
// whatever the one before did. forget also drops the registration, so the next
// Connect registers anew (s.mu held).
func (s *Service) clearLocked(forget bool) (rowErr, keyErr error) {
	if s.session != nil {
		safe.ForgetSecret(s.session.refresh)
		safe.ForgetSecret(s.session.access.AccessToken)
	}
	s.session = nil
	s.gen++
	reg := s.reg.get()
	reg.SessionID = ""
	if forget {
		reg = reg.forgotten()
	}
	if rowErr = s.reg.save(reg); rowErr != nil {
		slog.Error("oauth: could not record the sign-out", "provider", s.p.ID, "err", rowErr)
	}
	if keyErr = s.keyring.Delete(s.refreshAccount()); keyErr != nil {
		slog.Error("oauth: could not delete the refresh token", "provider", s.p.ID, "err", keyErr)
	}
	return rowErr, keyErr
}

// clientID is the client a registration runs under: the provider's fixed one,
// else the one the row saved. Empty is a sign-in that registers one.
func (s *Service) clientID(reg registration) string {
	return cmp.Or(s.p.ClientID, reg.ClientID)
}

// discover reads the issuer's endpoints, once per launch.
func (s *Service) discover(ctx context.Context) (oauth.Endpoints, error) {
	s.mu.Lock()
	known := s.endpoints
	s.mu.Unlock()
	if known != nil {
		return *known, nil
	}
	e, err := oauth.Discover(ctx, s.p.Issuer)
	if err != nil {
		return oauth.Endpoints{}, err
	}
	s.mu.Lock()
	s.endpoints = &e
	s.mu.Unlock()
	return e, nil
}

// clientFor is the OAuth client of the registration clientID names.
func (s *Service) clientFor(ctx context.Context, clientID string) (*oauth.Client, error) {
	e, err := s.discover(ctx)
	if err != nil {
		return nil, err
	}
	return s.newClient(e, clientID), nil
}

func (s *Service) newClient(e oauth.Endpoints, clientID string) *oauth.Client {
	return oauth.NewClient(oauth.Config{
		ClientID: clientID, Issuer: e.Issuer,
		AuthURL: e.AuthURL, TokenURL: e.TokenURL, JWKSURL: e.JWKSURL, RevocationURL: e.RevocationURL,
		Scopes: s.p.Scopes, Resource: s.base.String(),
	})
}

var _ llm.SessionSigner = (*Service)(nil)
