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

package github

import (
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
)

// Disconnect forgets the token on this machine. It stays authorized on GitHub
// until the user revokes Kstack there: revoking needs the OAuth App's secret,
// which Kstack does not hold. A delete the keyring refuses is returned, and
// calling again retries it.
func (s *Service) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopAttemptLocked()
	// Only a held token, or one a refused delete left, is in the keyring; any
	// other entry is a newer Kstack's.
	if s.token == "" && !(s.state.Phase == connectors.Failed && s.state.Failure == connectors.NotDeleted) {
		s.gen++
		s.publishLocked(connectors.State{Phase: connectors.SignedOut})
		return nil
	}
	return s.forgetTokenLocked(connectors.State{Phase: connectors.SignedOut})
}

// forgetTokenLocked ends the token in memory, then in the keyring, and
// publishes next. Memory goes first: the generation moves on whatever the
// keyring answers. A refused delete publishes Failed with NotDeleted and is
// returned (s.mu held).
func (s *Service) forgetTokenLocked(next connectors.State) error {
	safe.ForgetSecret(s.token)
	s.token = ""
	s.gen++
	if err := s.keyring.Delete(tokenAccount); err != nil {
		slog.Error("github: could not delete the token", "err", err)
		s.publishLocked(connectors.State{Phase: connectors.Failed, Failure: connectors.NotDeleted})
		return err
	}
	s.publishLocked(next)
	return nil
}
