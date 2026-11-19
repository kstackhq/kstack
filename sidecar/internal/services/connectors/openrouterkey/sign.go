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

package openrouterkey

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
)

// anySession is the generation the service itself signs under: whichever key
// is current. A key's own generation is never 0.
const anySession = 0

// errOffBase is a request outside the API base the key is for.
var errOffBase = errors.New("openrouterkey: the request is not for OpenRouter's API")

// Sign sets r's bearer to the key. It signs only the API base and what is
// under it, and only while connected.
func (s *Service) Sign(r *http.Request) error { return s.sign(r, anySession) }

// Refused is told of a 401: it checks whether OpenRouter still honours the key
// before dropping it.
func (s *Service) Refused(r *http.Request) { s.refused(r, anySession) }

// Bind is a signer for the current key alone. With no key, during a sign-in
// included, it is dead for good: an attempt's generation becomes its key's, so
// a binding made during one would come alive later.
func (s *Service) Bind() llm.Signer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Phase != Connected {
		return deadSigner{}
	}
	return boundSigner{s: s, gen: s.gen}
}

// boundSigner signs under one generation, and fails signed out once it has
// moved.
type boundSigner struct {
	s   *Service
	gen uint64
}

func (b boundSigner) Sign(r *http.Request) error { return b.s.sign(r, b.gen) }
func (b boundSigner) Refused(r *http.Request)    { b.s.refused(r, b.gen) }

// deadSigner is a binding made with no key.
type deadSigner struct{}

func (deadSigner) Sign(*http.Request) error { return llm.ErrSignedOut }
func (deadSigner) Refused(*http.Request)    {}

// keyLocked is the key gen names, "" when it has ended (s.mu held).
func (s *Service) keyLocked(gen uint64) string {
	if (gen != anySession && gen != s.gen) || s.state.Phase != Connected {
		return ""
	}
	return s.key
}

func (s *Service) sign(r *http.Request, gen uint64) error {
	if !s.covers(r.URL) {
		return errOffBase
	}
	s.mu.Lock()
	key := s.keyLocked(gen)
	s.mu.Unlock()
	if key == "" {
		return llm.ErrSignedOut
	}
	r.Header.Set("Authorization", "Bearer "+key)
	return nil
}

// refused starts a check of the key r carried, if it is still the key gen
// names and no check is under way.
func (s *Service) refused(r *http.Request, gen uint64) {
	sent := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.keyLocked(gen)
	if key == "" || sent != key || s.checking {
		return
	}
	s.checking = true
	// The key's own generation: gen may be anySession.
	current := s.gen
	s.wg.Go(func() { s.check(current, key) })
}

// check asks OpenRouter whether it still honours key, reading the status
// alone. Only a 401 drops the key, and only while it is still current: Kstack
// cannot revoke a key, so dropping one OpenRouter honours would strand it.
func (s *Service) check(gen uint64, key string) {
	if s.checkEnded != nil {
		defer s.checkEnded()
	}
	status, err := s.keyStatus(key)
	if err != nil {
		slog.Warn("openrouter: could not check the key", "err", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.checking = false
	if status != http.StatusUnauthorized || gen != s.gen {
		return
	}
	slog.Warn("openrouter: OpenRouter no longer honours the key")
	s.forgetKeyLocked(State{Phase: Failed, Failure: KeyRejected}) //nolint:errcheck // a refused delete is the state's NotDeleted
}

// keyStatus is the status GET <base>/key answers under key.
func (s *Service) keyStatus(key string) (int, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.issuerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base.JoinPath("key").String(), nil)
	if err != nil { // coverage:ignore the base parsed in New, and the method is a constant
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := llm.NoRedirectClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// covers reports whether u is the API base or under it: the same scheme, the
// host without case, the same effective port, and a path that is the base's or
// continues it after a slash.
func (s *Service) covers(u *url.URL) bool {
	b := s.base
	if u.Scheme != b.Scheme || !strings.EqualFold(u.Hostname(), b.Hostname()) || effectivePort(u) != effectivePort(b) {
		return false
	}
	path := strings.TrimSuffix(b.Path, "/")
	return u.Path == path || strings.HasPrefix(u.Path, path+"/")
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
