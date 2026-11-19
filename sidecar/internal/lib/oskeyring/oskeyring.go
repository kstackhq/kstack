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

// Package oskeyring reads and writes the OS keyring.
package oskeyring

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// ErrNoEntry is an account the keyring holds nothing under.
var ErrNoEntry = errors.New("oskeyring: no keyring entry")

// Keyring is the OS keyring under one service name.
type Keyring interface {
	// Get answers ErrNoEntry for an account that holds nothing.
	Get(account string) (string, error)
	Set(account, value string) error
	// Delete of an account that holds nothing is not an error.
	Delete(account string) error
}

// New is the OS keyring under service.
func New(service string) Keyring { return osKeyring{service: service} }

// osKeyring is the OS keyring through go-keyring.
type osKeyring struct {
	service string
}

func (k osKeyring) Get(account string) (string, error) {
	v, err := keyring.Get(k.service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNoEntry
	}
	return v, err
}

func (k osKeyring) Set(account, value string) error {
	return keyring.Set(k.service, account, value)
}

func (k osKeyring) Delete(account string) error {
	if err := keyring.Delete(k.service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// Check writes and deletes a throwaway entry under account, so a caller learns
// the keyring cannot keep a credential before it asks for one.
func Check(k Keyring, account string) error {
	if err := k.Set(account, "ok"); err != nil {
		return err
	}
	return k.Delete(account)
}
