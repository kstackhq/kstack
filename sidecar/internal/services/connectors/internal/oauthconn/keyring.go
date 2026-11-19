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

package oauthconn

import (
	"encoding/json"
	"errors"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
)

// The keyring's accounts under the service's name, each prefixed with the
// provider's id.

// refreshAccount holds the refresh token, in an entry.
func (s *Service) refreshAccount() string { return s.p.ID + "-refresh-token" }

// checkAccount is written and deleted before a sign-in opens the browser, so a
// machine with no keyring learns so before the user signs in.
func (s *Service) checkAccount() string { return s.p.ID + "-keyring-check" }

// entryVersion is the entry's stamp.
const entryVersion = 1

// entry is the refresh account's value: the refresh token and the sign-in it
// belongs to, which the registration's row names too.
type entry struct {
	V       int    `json:"v"`
	Token   string `json:"token"`
	Session string `json:"session"`
}

// saveEntry writes account's entry: the refresh token of the sign-in sessionID
// names.
func saveEntry(k oskeyring.Keyring, account, sessionID, token string) error {
	data, _ := json.Marshal(entry{V: entryVersion, Token: token, Session: sessionID}) // a struct of strings and an int encodes
	return k.Set(account, string(data))
}

// loadEntry reads account's entry. found says the account holds something; a
// value this build cannot read is found and names no session.
func loadEntry(k oskeyring.Keyring, account string) (e entry, found bool, err error) {
	v, err := k.Get(account)
	if errors.Is(err, oskeyring.ErrNoEntry) {
		return entry{}, false, nil
	}
	if err != nil {
		return entry{}, false, err
	}
	if json.Unmarshal([]byte(v), &e) != nil || e.V != entryVersion {
		return entry{}, true, nil
	}
	return e, true, nil
}
