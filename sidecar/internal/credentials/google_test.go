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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gcloudJSON = `{"configuration":{"active_configuration":"default"},` +
	`"credential":{"access_token":"ya29.token0123456789abcdef","token_expiry":"2026-09-30T13:00:00Z"}}`

func TestGoogleBorrowsFromGcloud(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(gcloudTok, reply{stdout: gcloudJSON})
	s := newTestStore(t, f, c)

	token, err := s.Google(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Token{Value: "ya29.token0123456789abcdef", Expires: time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)}, token)
	assert.Equal(t, []string{gcloudTok}, f.all())

	c.advance(58*time.Minute + 59*time.Second)
	_, err = s.Google(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, f.count(gcloudTok))
	c.advance(time.Second)
	_, err = s.Google(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, f.count(gcloudTok), "cached to token_expiry less a minute")
}

func TestGoogleEachExpiryLineIsErrExpired(t *testing.T) {
	for _, line := range []string{
		"ERROR: (gcloud.config.config-helper) There was a problem refreshing your current auth tokens: Reauthentication required.",
		"ERROR: (gcloud.config.config-helper) Reauthentication failed. cannot prompt during non-interactive execution.",
		"ERROR: ('invalid_grant: Bad Request', {'error': 'invalid_grant'})",
	} {
		t.Run(line, func(t *testing.T) {
			f, c := newFakeTools(), newClock()
			f.on(gcloudTok, reply{stderr: line + "\n", exit: 1})
			s := newTestStore(t, f, c)
			_, err := s.Google(t.Context())
			assert.ErrorIs(t, err, ErrExpired)
			assert.Equal(t, Expired, s.State(Key{Provider: Google}).Status)
		})
	}

	t.Run("a line off the table is an ordinary error", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(gcloudTok, reply{stderr: "ERROR: network unreachable\n", exit: 1})
		s := newTestStore(t, f, c)
		_, err := s.Google(t.Context())
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrExpired)
	})
}
