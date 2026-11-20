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

// Package oskeyringtest is an in-memory oskeyring.Keyring for tests.
package oskeyringtest

import (
	"maps"
	"sync"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
)

// Keyring is the OS keyring in memory; the zero value is empty. A non-nil Fail
// field is the error every call of its kind answers, changing nothing. Set the
// fields before sharing it.
type Keyring struct {
	FailGet, FailSet, FailDelete error

	mu      sync.Mutex
	entries map[string]string
}

// New is an empty keyring.
func New() *Keyring { return &Keyring{} }

func (k *Keyring) Get(account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.FailGet != nil {
		return "", k.FailGet
	}
	v, ok := k.entries[account]
	if !ok {
		return "", oskeyring.ErrNoEntry
	}
	return v, nil
}

func (k *Keyring) Set(account, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.FailSet != nil {
		return k.FailSet
	}
	if k.entries == nil {
		k.entries = map[string]string{}
	}
	k.entries[account] = value
	return nil
}

func (k *Keyring) Delete(account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.FailDelete != nil {
		return k.FailDelete
	}
	delete(k.entries, account)
	return nil
}

// Entries is a copy of every entry.
func (k *Keyring) Entries() map[string]string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return maps.Clone(k.entries)
}

var _ oskeyring.Keyring = (*Keyring)(nil)
