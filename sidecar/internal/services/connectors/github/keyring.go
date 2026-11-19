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
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
)

// The keyring's accounts under the service's name.
const (
	// tokenAccount holds the token and its account, in an entry.
	tokenAccount = "github-token"
	// checkAccount is written and deleted before a sign-in asks GitHub for a
	// code, so a machine with no keyring learns so before GitHub grants a token.
	checkAccount = "github-keyring-check"
)

// entryVersion is the entry's stamp.
const entryVersion = 1

// entry is the token account's value. Login is the account's, display only.
type entry struct {
	V     int    `json:"v"`
	Token string `json:"token"`
	Login string `json:"login"`
}

// saveEntry writes the token's entry.
func saveEntry(k oskeyring.Keyring, token, login string) error {
	data, _ := json.Marshal(entry{V: entryVersion, Token: token, Login: login}) // a struct of strings and an int encodes
	return k.Set(tokenAccount, string(data))
}

// restore reads the entry. A readable one is the token, connected. Any other is
// signed out: a newer version's is left for the Kstack that wrote it, and one
// that does not parse is deleted. A keyring that cannot be read is no proof of
// a sign-out, so nothing is written.
func (s *Service) restore() connectors.State {
	v, err := s.keyring.Get(tokenAccount)
	if errors.Is(err, oskeyring.ErrNoEntry) {
		return connectors.State{Phase: connectors.SignedOut}
	}
	if err != nil {
		slog.Warn("github: could not read the keyring; treating as signed out", "err", err)
		return connectors.State{Phase: connectors.SignedOut}
	}
	// The stamp is read alone, so a newer shape is recognised before it fails
	// to decode as this one.
	var stamp struct {
		V int `json:"v"`
	}
	if json.Unmarshal([]byte(v), &stamp) == nil && stamp.V > entryVersion {
		slog.Warn("github: the token's entry is a newer Kstack's; leaving it", "v", stamp.V)
		return connectors.State{Phase: connectors.SignedOut}
	}
	var e entry
	if json.Unmarshal([]byte(v), &e) != nil || e.V != entryVersion || e.Token == "" || e.Login == "" {
		if err := s.keyring.Delete(tokenAccount); err != nil {
			slog.Warn("github: could not delete an unreadable keyring entry", "err", err)
		}
		return connectors.State{Phase: connectors.SignedOut}
	}
	safe.AddSecret(e.Token)
	s.gen++
	s.token = e.Token
	return connectors.State{Phase: connectors.Connected, Name: e.Login}
}
