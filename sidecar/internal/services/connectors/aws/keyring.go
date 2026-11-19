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

package aws

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
	// sessionAccount holds the session, in an entry.
	sessionAccount = "aws-session"
	// checkAccount is written and deleted before a sign-in opens the browser,
	// so a machine with no keyring learns so before the user signs in.
	checkAccount = "aws-keyring-check"
)

// entryVersion is the entry's stamp.
const entryVersion = 1

// entry is the session account's value: what a later launch needs to refresh
// the session. The credentials themselves are memory's alone.
type entry struct {
	V int `json:"v"`
	// Refresh is the refresh token, bound to DPoPKey.
	Refresh string `json:"refresh"`
	// DPoPKey is the session's P-256 private key, PEM-encoded.
	DPoPKey string `json:"dpopKey"`
	// ARN is the signed-in principal's, display only.
	ARN string `json:"arn"`
}

// saveEntry writes the session's entry.
func saveEntry(k oskeyring.Keyring, e entry) error {
	e.V = entryVersion
	data, _ := json.Marshal(e) // a struct of strings and an int encodes
	return k.Set(sessionAccount, string(data))
}

// loadEntry reads the session's entry. found says the account holds
// something. A value that does not decode whole is answered as far as it did,
// so a newer shape still shows its stamp, and the caller judges the rest.
func loadEntry(k oskeyring.Keyring) (e entry, found bool, err error) {
	v, err := k.Get(sessionAccount)
	if errors.Is(err, oskeyring.ErrNoEntry) {
		return entry{}, false, nil
	}
	if err != nil {
		return entry{}, false, err
	}
	_ = json.Unmarshal([]byte(v), &e)
	return e, true, nil
}

// restore reads the entry. A whole one is the session, connected. Any other is
// signed out: a newer version's is left for the Kstack that wrote it, and one
// this build cannot use is deleted. A keyring that cannot be read is no proof
// of a sign-out, so nothing is written.
func (s *Service) restore() connectors.State {
	e, found, err := loadEntry(s.keyring)
	if err != nil {
		slog.Warn("aws: could not read the keyring; treating as signed out", "err", err)
		return connectors.State{Phase: connectors.SignedOut}
	}
	if !found {
		return connectors.State{Phase: connectors.SignedOut}
	}
	if e.V > entryVersion {
		slog.Warn("aws: the session's entry is a newer Kstack's; leaving it", "v", e.V)
		return connectors.State{Phase: connectors.SignedOut}
	}
	key, keyErr := parseKey(e.DPoPKey)
	_, arnErr := accountOf(e.ARN)
	if e.V != entryVersion || e.Refresh == "" || keyErr != nil || arnErr != nil {
		if err := s.keyring.Delete(sessionAccount); err != nil {
			slog.Warn("aws: could not delete an unusable keyring entry", "err", err)
		}
		return connectors.State{Phase: connectors.SignedOut}
	}
	safe.AddSecret(e.Refresh)
	s.gen++
	s.session = &session{refresh: e.Refresh, key: key, arn: e.ARN}
	return connectors.State{Phase: connectors.Connected, Subject: e.ARN}
}
