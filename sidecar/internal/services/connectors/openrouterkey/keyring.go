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
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// The keyring's accounts under the service's name.
const (
	// keyAccount holds the key, in an entry.
	keyAccount = "openrouter-api-key"
	// checkAccount is written and deleted before a sign-in opens the browser,
	// so a machine with no keyring learns so before OpenRouter issues a key.
	checkAccount = "openrouter-keyring-check"
)

// entryVersion is the entry's stamp.
const entryVersion = 1

// entry is the key account's value.
type entry struct {
	V   int    `json:"v"`
	Key string `json:"key"`
}

// saveKey writes the key's entry.
func saveKey(k oskeyring.Keyring, key string) error {
	data, _ := json.Marshal(entry{V: entryVersion, Key: key}) // a struct of a string and an int encodes
	return k.Set(keyAccount, string(data))
}

// restore reads the entry. A readable one is the key, connected. Any other is
// signed out: a newer version's is left for the Kstack that wrote it, and one
// that does not parse is deleted. A keyring that cannot be read is no proof of
// a sign-out, so nothing is written.
func (s *Service) restore() State {
	v, err := s.keyring.Get(keyAccount)
	if errors.Is(err, oskeyring.ErrNoEntry) {
		return State{Phase: SignedOut}
	}
	if err != nil {
		slog.Warn("openrouter: could not read the keyring; treating as signed out", "err", err)
		return State{Phase: SignedOut}
	}
	// The stamp is read alone, so a newer shape is recognised before it fails
	// to decode as this one.
	var stamp struct {
		V int `json:"v"`
	}
	if json.Unmarshal([]byte(v), &stamp) == nil && stamp.V > entryVersion {
		slog.Warn("openrouter: the key's entry is a newer Kstack's; leaving it", "v", stamp.V)
		return State{Phase: SignedOut}
	}
	var e entry
	if json.Unmarshal([]byte(v), &e) != nil || e.V != entryVersion || e.Key == "" {
		if err := s.keyring.Delete(keyAccount); err != nil {
			slog.Warn("openrouter: could not delete an unreadable keyring entry", "err", err)
		}
		return State{Phase: SignedOut}
	}
	safe.AddSecret(e.Key)
	s.gen++
	s.key = e.Key
	return State{Phase: Connected}
}
