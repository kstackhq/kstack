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

package credentials

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubBorrowsFromGh(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(ghToken, reply{stdout: "gho_token0123456789abcdef\n"})
	s := newTestStore(t, f, c)

	token, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	assert.Equal(t, "gho_token0123456789abcdef", token)
	assert.Equal(t, []string{ghToken}, f.all())
	assert.Equal(t, Valid, s.State(Key{Provider: GitHub}).Status, "one identity whatever the host")
}

func TestGitHubEachExpiryLineIsErrExpired(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(ghToken, reply{stderr: "no oauth token found for github.com\n", exit: 1})
	s := newTestStore(t, f, c)

	_, err := s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExpired)
	assert.Equal(t, Expired, s.State(Key{Provider: GitHub}).Status)

	t.Run("a line off the table is an ordinary error", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghToken, reply{stderr: "could not reach the keyring\n", exit: 1})
		s := newTestStore(t, f, c)
		_, err := s.GitHub(t.Context(), "github.com")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrExpired)
		assert.Equal(t, Status(""), s.State(Key{Provider: GitHub}).Status)
	})
}
