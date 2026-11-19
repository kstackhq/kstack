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
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// Disconnect forgets the key on this machine. The key stays live on OpenRouter
// until the user deletes it there. A delete the keyring refuses is returned,
// and calling again retries it.
func (s *Service) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Phase == FromEnvironment {
		return ErrFromEnvironment
	}
	s.stopAttemptLocked()
	// Only a held key, or one a refused delete left, is in the keyring; any
	// other entry is a newer Kstack's.
	if s.key == "" && !(s.state.Phase == Failed && s.state.Failure == NotDeleted) {
		s.gen++
		s.publishLocked(State{Phase: SignedOut})
		return nil
	}
	return s.forgetKeyLocked(State{Phase: SignedOut})
}

// forgetKeyLocked ends the key in memory, then in the keyring, and publishes
// next. Memory goes first: the generation moves on, so every binding is dead
// whatever the keyring answers. A refused delete publishes Failed with
// NotDeleted and is returned (s.mu held).
func (s *Service) forgetKeyLocked(next State) error {
	safe.ForgetSecret(s.key)
	s.key = ""
	s.gen++
	if err := s.keyring.Delete(keyAccount); err != nil {
		slog.Error("openrouter: could not delete the key", "err", err)
		s.publishLocked(State{Phase: Failed, Failure: NotDeleted})
		return err
	}
	s.publishLocked(next)
	return nil
}
