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

package oskeyringtest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
)

func TestTheFakeKeyring(t *testing.T) {
	k := New()
	_, err := k.Get("a")
	require.ErrorIs(t, err, oskeyring.ErrNoEntry)

	require.NoError(t, k.Set("a", "v"))
	v, err := k.Get("a")
	require.NoError(t, err)
	assert.Equal(t, "v", v)
	assert.Equal(t, map[string]string{"a": "v"}, k.Entries())

	require.NoError(t, k.Delete("a"))
	require.NoError(t, k.Delete("a"), "a delete of nothing is not an error")
	assert.Empty(t, k.Entries())
}

// A keyring built as a literal, to set a Fail field, holds entries too.
func TestTheZeroKeyringIsEmpty(t *testing.T) {
	k := &Keyring{FailGet: errors.New("boom")}
	assert.Empty(t, k.Entries())
	require.NoError(t, k.Set("a", "v"))
	assert.Equal(t, map[string]string{"a": "v"}, k.Entries())
}

func TestTheFakeKeyringFailsOnRequest(t *testing.T) {
	boom := errors.New("boom")
	k := New()
	require.NoError(t, k.Set("a", "v"))
	k.FailGet, k.FailSet, k.FailDelete = boom, boom, boom

	_, err := k.Get("a")
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, k.Set("a", "w"), boom)
	assert.ErrorIs(t, k.Delete("a"), boom)
	assert.Equal(t, map[string]string{"a": "v"}, k.Entries(), "a failed call changes nothing")
}
