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

package providercredentials

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// openRouterAccount is OpenRouter's saved key's account: its connector owns
// "openrouter-api-key".
const openRouterAccount = "openrouter-saved-api-key"

// entryVersion is the entry's stamp.
const entryVersion = 1

// entry is an account's value.
type entry struct {
	V   int    `json:"v"`
	Key string `json:"key"`
}

// account is id's keyring account under the service's name.
func account(id string) string {
	if id == "openrouter" {
		return openRouterAccount
	}
	return id + "-api-key"
}

func encode(key string) string {
	data, _ := json.Marshal(entry{V: entryVersion, Key: key}) // a struct of a string and an int encodes
	return string(data)
}

// restore is id's saved key, "" for none, and whether the entry is a newer
// Kstack's, which is left for it. One that does not parse, or holds no key Save
// would take, is deleted. A keyring that cannot be read is no proof there is no
// key, so nothing is written.
func (s *Service) restore(id string) (key string, newer bool) {
	v, err := s.keyring.Get(account(id))
	if errors.Is(err, oskeyring.ErrNoEntry) {
		return "", false
	}
	if err != nil {
		slog.Warn("providercredentials: could not read the keyring", "provider", id, "err", err)
		return "", false
	}
	// The stamp is read alone, so a newer shape is recognised before it fails
	// to decode as this one.
	var stamp struct {
		V int `json:"v"`
	}
	if json.Unmarshal([]byte(v), &stamp) == nil && stamp.V > entryVersion {
		slog.Warn("providercredentials: the saved key's entry is a newer Kstack's; leaving it", "provider", id, "v", stamp.V)
		return "", true
	}
	var e entry
	if json.Unmarshal([]byte(v), &e) != nil || e.V != entryVersion || !isKey(e.Key) {
		if err := s.keyring.Delete(account(id)); err != nil {
			slog.Warn("providercredentials: could not delete an unreadable keyring entry", "provider", id, "err", err)
		}
		return "", false
	}
	safe.AddSecret(e.Key)
	return e.Key, false
}
