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

// Package kstackcloud is the Kstack Cloud connector: the Kstack Cloud account, signed in to by
// the OAuth engine under a fixed client. It signs no request.
package kstackcloud

import (
	"context"
	"log/slog"
	"sync"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/internal/oauthconn"
)

const (
	// ID is the row's key and the keyring accounts' prefix.
	ID = "kstack-cloud"
	// oldAccount is where desktop/v0.0.1 kept the whole token. Nothing reads it:
	// its user signs in again.
	oldAccount = "auth-token"
)

// Config is the connector's production knobs.
type Config struct {
	// DB is app.db, which holds the account's row.
	DB *appdb.DB
	// Keyring is the OS keyring the refresh token is kept in.
	Keyring oskeyring.Keyring
	// Issuer and ClientID are Kstack Cloud's auth server and the app's client there.
	Issuer   string
	ClientID string
}

// engine is what the connector uses of the OAuth engine.
type engine interface {
	State() connectors.State
	Restored() <-chan struct{}
	WatchState() (<-chan connectors.State, func())
	Start(ctx context.Context, fresh bool) (connectors.Failure, error)
	Cancel()
	Disconnect(ctx context.Context) error
	Close()
}

// Service is the Kstack Cloud connector. The engine stays unexported, so nothing
// outside this package can sign with the account.
type Service struct {
	engine engine
	// connecting runs one Connect at a time.
	connecting sync.Mutex

	// ctx ends the old entry's delete, which runs on wg.
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
	// oldEntryDone closes when the old entry's delete has ended, run or not.
	oldEntryDone chan struct{}
}

// New is the connector, Restoring until the engine has read the keyring, then
// signed in if app.db and the keyring hold a session. It reaches no network
// and makes no keyring call.
func New(cfg Config) (*Service, error) {
	return newService(cfg.Keyring, func() (engine, error) {
		return oauthconn.New(oauthconn.Config{
			Provider: provider(cfg),
			DB:       cfg.DB,
			Keyring:  cfg.Keyring,
		})
	})
}

// newService builds the engine, and deletes the old entry on a goroutine.
func newService(k oskeyring.Keyring, newEngine func() (engine, error)) (*Service, error) {
	e, err := newEngine()
	if err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Service{engine: e, ctx: ctx, stop: stop, oldEntryDone: make(chan struct{})}
	s.wg.Go(func() { s.deleteOldEntry(k) })
	return s, nil
}

// deleteOldEntry deletes oldAccount after the engine's restore, whose read has
// opened the keyring by then. A keyring that did not open, a dismissed prompt
// included, is not asked again: a delete is not gated, and would prompt.
func (s *Service) deleteOldEntry(k oskeyring.Keyring) {
	defer close(s.oldEntryDone)
	select {
	case <-s.engine.Restored():
	case <-s.ctx.Done():
		return
	}
	err := oskeyring.Opened(k)
	if err == nil {
		err = oskeyring.Delete(s.ctx, k, oldAccount)
	}
	if err != nil && s.ctx.Err() == nil {
		// A machine with no keyring fails this at every launch; the next tries again.
		slog.Info("kstack: could not delete the old sign-in's keyring entry", "err", err)
	}
}

// Restored closes once the engine has published what the stores held.
func (s *Service) Restored() <-chan struct{} { return s.engine.Restored() }

// State is the connector's current state.
func (s *Service) State() connectors.State { return s.engine.State() }

// Disconnect signs out on this machine, then revokes the refresh token on a
// goroutine. It errs only when the sign-out could not be recorded and the
// entry is still there.
func (s *Service) Disconnect(ctx context.Context) error { return s.engine.Disconnect(ctx) }

// Close ends the connector's goroutines and the engine's, a revocation under
// way included, and waits for them.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
	s.engine.Close()
}

// provider is the Kstack Cloud sign-in at cfg's issuer.
func provider(cfg Config) oauthconn.Provider {
	return oauthconn.Provider{
		ID:            ID,
		Issuer:        cfg.Issuer,
		ClientID:      cfg.ClientID,
		Scopes:        []string{"openid", "offline_access", "email", "profile"},
		CallbackPath:  "/oauth/callback",
		SessionEnders: []string{"invalid_grant"},
	}
}
