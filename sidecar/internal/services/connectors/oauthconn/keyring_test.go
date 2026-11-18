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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// The OS keyring answers errNoEntry for an empty account and deletes one that
// holds nothing without an error. Serial: go-keyring's mock is process state.
func TestTheOSKeyring(t *testing.T) {
	keyring.MockInit()
	k := osKeyring{service: "Kstack-test"}

	_, err := k.Get(refreshAccount)
	assert.ErrorIs(t, err, errNoEntry)
	require.NoError(t, k.Set(refreshAccount, "v"))
	got, err := k.Get(refreshAccount)
	require.NoError(t, err)
	assert.Equal(t, "v", got)
	require.NoError(t, k.Delete(refreshAccount))
	assert.NoError(t, k.Delete(refreshAccount))

	keyring.MockInitWithError(errBoom)
	assert.ErrorIs(t, k.Delete(refreshAccount), errBoom)
}

// The check writes and deletes its entry, and fails when either does.
func TestCheckKeyring(t *testing.T) {
	k := newFakeKeyring()
	require.NoError(t, checkKeyring(k, checkAccount))
	_, ok := k.entry(checkAccount)
	assert.False(t, ok)

	k.failDelete(checkAccount, errBoom)
	assert.ErrorIs(t, checkKeyring(k, checkAccount), errBoom)
}
