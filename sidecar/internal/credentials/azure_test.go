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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// azJSON is get-access-token's answer; expiresOn alone when expires is zero, as
// before azure-cli 2.54.
func azJSON(token string, expires time.Time) string {
	s := `{"accessToken":"` + token + `","expiresOn":"2026-09-30 13:00:00.000000","tokenType":"Bearer"`
	if !expires.IsZero() {
		s += `,"expires_on":` + strconv.FormatInt(expires.Unix(), 10)
	}
	return s + "}"
}

func TestAzureBorrowsPerResource(t *testing.T) {
	f, c := newFakeTools(), newClock()
	armExpiry, graphExpiry := c.Now().Add(time.Hour), c.Now().Add(2*time.Hour)
	f.on(azARM, reply{stdout: azJSON("arm-token-0123456789abcdef", armExpiry)})
	f.on(azGraph, reply{stdout: azJSON("graph-token-0123456789abcdef", graphExpiry)})
	s := newTestStore(t, f, c)

	arm, err := s.Azure(t.Context(), "https://management.azure.com/")
	require.NoError(t, err)
	assert.Equal(t, Token{Value: "arm-token-0123456789abcdef", Expires: armExpiry}, arm)
	graph, err := s.Azure(t.Context(), "https://graph.microsoft.com/")
	require.NoError(t, err)
	assert.Equal(t, "graph-token-0123456789abcdef", graph.Value)
	assert.Equal(t, []string{azARM, azGraph}, f.all(), "two resources are two runs")

	c.advance(59 * time.Minute)
	_, err = s.Azure(t.Context(), "https://management.azure.com/")
	require.NoError(t, err)
	_, err = s.Azure(t.Context(), "https://graph.microsoft.com/")
	require.NoError(t, err)
	assert.Equal(t, 2, f.count(azARM), "each cached to its own expires_on less a minute")
	assert.Equal(t, 1, f.count(azGraph))

	t.Run("an older CLI's answer keeps the default", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(azARM, reply{stdout: azJSON("arm-token-0123456789abcdef", time.Time{})})
		s := newTestStore(t, f, c)
		tok, err := s.Azure(t.Context(), "https://management.azure.com/")
		require.NoError(t, err)
		assert.True(t, tok.Expires.IsZero())
		c.advance(defaultTTL - time.Second)
		_, err = s.Azure(t.Context(), "https://management.azure.com/")
		require.NoError(t, err)
		assert.Equal(t, 1, f.count(azARM))
		c.advance(time.Second)
		_, err = s.Azure(t.Context(), "https://management.azure.com/")
		require.NoError(t, err)
		assert.Equal(t, 2, f.count(azARM))
	})
}

func TestAzureEachExpiryLineIsErrExpired(t *testing.T) {
	for _, line := range []string{
		"AADSTS700082: The refresh token has expired due to inactivity.",
		"AADSTS70008: The provided authorization code or refresh token has expired.",
		"AADSTS50173: The provided grant has expired due to it being revoked.",
		"ERROR: Interactive authentication is needed. Please run: az login",
		"Please run 'az login' to setup account.",
	} {
		t.Run(line, func(t *testing.T) {
			f, c := newFakeTools(), newClock()
			f.on(azARM, reply{stderr: line + "\n", exit: 1})
			s := newTestStore(t, f, c)
			_, err := s.Azure(t.Context(), "https://management.azure.com/")
			assert.ErrorIs(t, err, ErrExpired)
			assert.Equal(t, Expired, s.State(Key{Provider: Azure}).Status)
		})
	}

	t.Run("a code matches as a whole word", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(azARM, reply{stderr: "AADSTS700084: The refresh token was issued to a single page app.\n", exit: 1})
		s := newTestStore(t, f, c)
		_, err := s.Azure(t.Context(), "https://management.azure.com/")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrExpired)
		assert.Equal(t, Status(""), s.State(Key{Provider: Azure}).Status)
	})
}
