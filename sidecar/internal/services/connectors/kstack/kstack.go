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

// Package kstack is the Kstack connector: the Kstack account, signed in to by
// the OAuth engine under a fixed client. It signs no request.
package kstack

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
	ID = "kstack"
	// oldAccount is where desktop/v0.0.1 kept the whole token. Nothing reads it:
	// its user signs in again.
	oldAccount = "auth-token"
)

// Config is the connector's production knobs.
type Config struct {
	// DB is app.db, which holds the account's row.
	DB *appdb.DB
	// KeychainService is the OS-keyring service the refresh token is kept under.
	KeychainService string
	// Issuer and ClientID are Kstack's auth server and the app's client there.
	Issuer   string
	ClientID string
}

// engine is what the connector uses of the OAuth engine.
type engine interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Start(ctx context.Context, fresh bool) (connectors.Failure, error)
	Cancel()
	Disconnect(ctx context.Context) error
	Close()
}

// Service is the Kstack connector. The engine stays unexported, so nothing
// outside this package can sign with the account.
type Service struct {
	engine engine
	// connecting runs one Connect at a time.
	connecting sync.Mutex
}

// New is the connector, signed in if app.db and the keyring hold a session. It
// reaches no network.
func New(cfg Config) (*Service, error) {
	return newService(oskeyring.New(cfg.KeychainService), func() (engine, error) {
		return oauthconn.New(oauthconn.Config{
			Provider:        provider(cfg),
			DB:              cfg.DB,
			KeychainService: cfg.KeychainService,
		})
	})
}

// newService deletes the old entry, then builds the engine.
func newService(k oskeyring.Keyring, newEngine func() (engine, error)) (*Service, error) {
	if err := k.Delete(oldAccount); err != nil {
		// A machine with no keyring fails this at every launch; the next tries again.
		slog.Info("kstack: could not delete the old sign-in's keyring entry", "err", err)
	}
	e, err := newEngine()
	if err != nil {
		return nil, err
	}
	return &Service{engine: e}, nil
}

// State is the connector's current state.
func (s *Service) State() connectors.State { return s.engine.State() }

// Disconnect signs out on this machine, then revokes the refresh token on a
// goroutine. It errs only when the sign-out could not be recorded and the
// entry is still there.
func (s *Service) Disconnect(ctx context.Context) error { return s.engine.Disconnect(ctx) }

// Close ends the engine's goroutines, a revocation under way included, and
// waits for them.
func (s *Service) Close() { s.engine.Close() }

// provider is the Kstack sign-in at cfg's issuer.
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
