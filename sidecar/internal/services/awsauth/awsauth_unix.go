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

//go:build unix

// Package awsauth holds the user's AWS login: the SDK's default chain in the
// sidecar's environment, the one the EKS exec plugin sees, resolved outside
// the sandbox and only when a request needs it. Only a sandboxed run reaches
// AWS, so it is built where a sandbox is: not on Windows.
package awsauth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"golang.org/x/sync/semaphore"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// ErrNoLogin is every failure to resolve the chain: the SDK's own text can
// name files and accounts.
var ErrNoLogin = errors.New("no AWS login was found")

// retrieveTimeout is how long a retrieval may take: past the SDK's one-minute
// timeout for a credential_process and its helper's wait.
const retrieveTimeout = 2 * time.Minute

// Service is the user's AWS login.
type Service struct {
	load func(context.Context) (aws.Config, error)
	// retrieveTimeout bounds a retrieval, which an SSO or STS endpoint that
	// never answers would otherwise hold its turn through for good.
	retrieveTimeout time.Duration

	mu     sync.Mutex // guards region and cache
	region string
	cache  *aws.CredentialsCache // nil until a load yields a chain

	// turn is held across each retrieval and each Drop, with the registration
	// it makes, so what safe blanks is always what the cache last answered: a
	// Drop never forgets a newer credential, nor an older one registers last.
	turn *semaphore.Weighted
	held aws.Credentials // what safe blanks now; read and written in turn
}

// New is the service over the SDK's default configuration. It reads nothing
// until asked.
func New() *Service {
	return newService(func(ctx context.Context) (aws.Config, error) { // coverage:ignore it reads the user's own configuration
		return config.LoadDefaultConfig(ctx)
	})
}

// newService is the service over load, a test's own configuration.
func newService(load func(context.Context) (aws.Config, error)) *Service {
	return &Service{load: load, retrieveTimeout: retrieveTimeout, turn: semaphore.NewWeighted(1)}
}

// config is the configuration's region and credential chain, loaded when no
// chain is held: on first use, after a load that yields none, and after a
// retrieval fails or a Drop, since a chain is fixed at load. Keys read from
// a file, or a missing profile's metadata provider, recover only by loading
// again. Loading reads files and runs nothing: a credential_process runs only
// when Credentials resolves.
func (s *Service) config() (region string, cache *aws.CredentialsCache) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		cfg, err := s.load(context.Background())
		// A load that fails keeps no region of an earlier one.
		s.region = cfg.Region
		if err == nil {
			// LoadDefaultConfig answers its chain as a cache; anything else is none.
			s.cache, _ = quiet(cfg).(*aws.CredentialsCache)
		}
	}
	return s.region, s.cache
}

// unload drops the chain, so the next request loads the configuration again.
func (s *Service) unload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = nil
}

// Region is the configuration's default region, "" for none.
func (s *Service) Region() string {
	region, _ := s.config()
	return region
}

// Credentials resolves the login through the SDK's cache, so an expiring SSO
// or assumed-role credential refreshes. The secret key and session token are
// blanked in rendered text while held.
func (s *Service) Credentials(ctx context.Context) (aws.Credentials, error) {
	if err := s.turn.Acquire(ctx, 1); err != nil {
		return aws.Credentials{}, ErrNoLogin
	}
	done := make(chan aws.Credentials, 1)
	go func() {
		defer s.turn.Release(1)
		// The SDK finishes a refresh whether or not anyone waits for it, so the
		// turn is held, and what it fills in registered, until it ends, or until
		// retrieveTimeout, after which the chain is unloaded and a late refresh
		// fills a cache no one reads.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.retrieveTimeout)
		defer cancel()
		done <- s.retrieve(rctx)
	}()
	select {
	case creds := <-done:
		if !creds.HasKeys() {
			return aws.Credentials{}, ErrNoLogin
		}
		return creds, nil
	case <-ctx.Done():
		return aws.Credentials{}, ErrNoLogin
	}
}

// retrieve is the login, or none, registered as held. The chain is read in
// the turn, so it is the one the last Drop left. The caller holds turn.
func (s *Service) retrieve(ctx context.Context) aws.Credentials {
	_, cache := s.config()
	if cache == nil {
		return aws.Credentials{}
	}
	creds, err := cache.Retrieve(ctx)
	if err != nil {
		s.unload()
		return aws.Credentials{}
	}
	s.hold(creds)
	return creds
}

// Drop forgets the login, once AWS says the credential is dead: the next
// request loads the configuration again, so keys the user replaced are read.
func (s *Service) Drop() {
	_ = s.turn.Acquire(context.Background(), 1)
	defer s.turn.Release(1)
	s.unload()
	s.hold(aws.Credentials{})
}

// hold registers creds' secrets with safe, forgetting those it replaces. The
// caller holds turn.
func (s *Service) hold(creds aws.Credentials) {
	if creds.SecretAccessKey == s.held.SecretAccessKey && creds.SessionToken == s.held.SessionToken {
		return
	}
	safe.ForgetSecret(s.held.SecretAccessKey)
	safe.ForgetSecret(s.held.SessionToken)
	safe.AddSecret(creds.SecretAccessKey)
	safe.AddSecret(creds.SessionToken)
	s.held = creds
}
