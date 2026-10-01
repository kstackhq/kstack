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
	"fmt"
	"testing"
	"time"

	"github.com/amorey/gochan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

const (
	arm   = "https://management.azure.com/"
	graph = "https://graph.microsoft.com/"
)

// statuses is a gauge delivery as each identity's status, by key.
func statuses(states []State) map[string]Status {
	out := map[string]Status{}
	for _, st := range states {
		out[st.Key.String()] = st.Status
	}
	return out
}

func TestTheGaugeIsCurrentOnSubscribe(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(ghToken, reply{stdout: "gho_token0123456789abcdef"})
	s := newTestStore(t, f, c)

	rx := s.Subscribe()
	t.Cleanup(rx.Close)
	assert.Empty(t, testutil.Recv(t, rx.Chan(), "the empty table"))

	_, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	assert.Equal(t, map[string]Status{"github": Valid}, statuses(testutil.Recv(t, rx.Chan(), "the change")))

	late := s.Subscribe()
	t.Cleanup(late.Close)
	assert.Equal(t, map[string]Status{"github": Valid}, statuses(testutil.Recv(t, late.Chan(), "the table on subscribe")))
}

func TestMarkExpiredDropsTheCacheAndSetsTheStatus(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
	f.on(awsRegion, reply{stdout: "eu-west-1"})
	f.on(azARM, reply{stdout: azJSON("arm-token-0123456789abcdef", time.Time{})})
	f.on(azGraph, reply{stdout: azJSON("graph-token-0123456789abcdef", time.Time{})})
	s := newTestStore(t, f, c)

	_, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	_, err = s.Region(t.Context(), "dev")
	require.NoError(t, err)
	_, err = s.Azure(t.Context(), arm)
	require.NoError(t, err)
	_, err = s.Azure(t.Context(), graph)
	require.NoError(t, err)
	rx := s.Subscribe()
	t.Cleanup(rx.Close)
	testutil.Recv(t, rx.Chan(), "the table")

	s.MarkExpired(Key{AWS, "dev"}, "dev", secretKey)
	s.MarkExpired(Key{Provider: Azure}, arm, "arm-token-0123456789abcdef")

	st := s.State(Key{AWS, "dev"})
	assert.Equal(t, Expired, st.Status)
	assert.Equal(t, "the provider refused the credential", st.Detail)
	assert.Equal(t, map[string]Status{"aws:dev": Expired, "azure": Expired}, statuses(testutil.Recv(t, rx.Chan(), "the change")))

	f.on(awsExport, reply{stdout: awsJSON("another-secret-0123456789", "")})
	f.on(azARM, reply{stdout: azJSON("arm-token-2-0123456789abcdef", time.Time{})})
	got, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, "111111111111", got.Account)
	_, err = s.Region(t.Context(), "dev")
	require.NoError(t, err)
	_, err = s.Azure(t.Context(), arm)
	require.NoError(t, err)
	_, err = s.Azure(t.Context(), graph)
	require.NoError(t, err)
	assert.Equal(t, 2, f.count(awsExport))
	assert.Equal(t, 2, f.count(awsCaller), "the account is read again with the credential")
	assert.Equal(t, 2, f.count(awsRegion), "and its region")
	assert.Equal(t, 2, f.count(azARM))
	assert.Equal(t, 2, f.count(azGraph), "every resource of the identity goes")
}

func TestARefusedCredentialStaysRefused(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(ghToken, reply{stdout: "gho_tokenA0123456789abcdef"})
	s := newTestStore(t, f, c)
	_, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)

	s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")
	_, err = s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExpired, "the tool hands back the token it was refused")
	assert.Equal(t, Expired, s.State(Key{Provider: GitHub}).Status)

	c.advance(expiredBackoff)
	_, err = s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExpired, "nothing is cached")
	assert.Equal(t, 3, f.count(ghToken))

	c.advance(expiredBackoff)
	f.on(ghToken, reply{stdout: "gho_tokenB0123456789abcdef"})
	token, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	assert.Equal(t, "gho_tokenB0123456789abcdef", token)
	assert.Equal(t, Valid, s.State(Key{Provider: GitHub}).Status)

	t.Run("a borrow in flight when the refusal lands", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		hold := make(chan struct{})
		f.on(ghToken, reply{stdout: "gho_tokenA0123456789abcdef", hold: hold})
		s := newTestStore(t, f, c)

		done := make(chan error, 1)
		go func() {
			_, err := s.GitHub(t.Context(), "github.com")
			done <- err
		}()
		f.ran.Await(t, "the run")
		s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")
		close(hold)

		assert.ErrorIs(t, testutil.Recv(t, done, "the borrow"), ErrExpired)
		assert.Equal(t, Expired, s.State(Key{Provider: GitHub}).Status, "it writes no Valid")
		s.mu.Lock()
		_, cached := s.cache["github:github.com"]
		s.mu.Unlock()
		assert.False(t, cached, "and nothing to the cache")
	})
}

// A request signed before a refresh can be refused after it: the credential that
// replaced it stays, and only what was sent is refused.
func TestARefusalOfAReplacedSecretLeavesTheIdentity(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(ghToken, reply{stdout: "gho_tokenB0123456789abcdef"})
	s := newTestStore(t, f, c)
	_, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)

	s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")
	token, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	assert.Equal(t, "gho_tokenB0123456789abcdef", token)
	assert.Equal(t, Valid, s.State(Key{Provider: GitHub}).Status)
	assert.Equal(t, 1, f.count(ghToken), "the cache kept B")

	f.on(ghToken, reply{stdout: "gho_tokenA0123456789abcdef"})
	assert.Equal(t, Expired, s.Recheck(t.Context(), Key{Provider: GitHub}).Status, "A is refused")
}

func TestAStandingRefusalHoldsTheIdentity(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(azARM, reply{stdout: azJSON("arm-token-0123456789abcdef", time.Time{})})
	f.on(azGraph, reply{stdout: azJSON("graph-token-0123456789abcdef", time.Time{})})
	s := newTestStore(t, f, c)
	_, err := s.Azure(t.Context(), arm)
	require.NoError(t, err)

	s.MarkExpired(Key{Provider: Azure}, arm, "arm-token-0123456789abcdef")
	// Read through TryRecv alone: Chan's feeder goroutine would own the receiver.
	rx := s.Subscribe()
	t.Cleanup(rx.Close)
	table, err := rx.TryRecv()
	require.NoError(t, err)
	assert.Equal(t, map[string]Status{"azure": Expired}, statuses(table))

	graphToken, err := s.Azure(t.Context(), graph)
	require.NoError(t, err, "a fresh Graph token is cached")
	assert.Equal(t, "graph-token-0123456789abcdef", graphToken.Value)
	assert.Equal(t, Expired, s.State(Key{Provider: Azure}).Status, "and the identity stays Expired")
	// The store publishes before the borrow returns, so a change would be pending.
	_, err = rx.TryRecv()
	assert.ErrorIs(t, err, gochan.ErrEmpty, "the gauge delivered no change")

	c.advance(expiredBackoff)
	f.on(azARM, reply{stdout: azJSON("arm-token-2-0123456789abcdef", time.Time{})})
	_, err = s.Azure(t.Context(), arm)
	require.NoError(t, err)
	assert.Equal(t, Valid, s.State(Key{Provider: Azure}).Status)
}

func TestRecheckRunsPastTheCacheAndTheBackoff(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(azARM, reply{stdout: azJSON("arm-token-0123456789abcdef", time.Time{})})
	f.on(azGraph, reply{stdout: azJSON("graph-token-0123456789abcdef", time.Time{})})
	s := newTestStore(t, f, c)
	_, err := s.Azure(t.Context(), arm)
	require.NoError(t, err)
	_, err = s.Azure(t.Context(), graph)
	require.NoError(t, err)
	s.MarkExpired(Key{Provider: Azure}, arm, "arm-token-0123456789abcdef")
	_, err = s.Azure(t.Context(), arm)
	require.ErrorIs(t, err, ErrExpired, "within the backoff now")

	f.on(azARM, reply{stdout: azJSON("arm-token-2-0123456789abcdef", time.Time{})})
	st := s.Recheck(t.Context(), Key{Provider: Azure})
	assert.Equal(t, Valid, st.Status)
	assert.Equal(t, 3, f.count(azARM), "once per standing refusal")
	assert.Equal(t, 1, f.count(azGraph), "and no other")

	t.Run("with no refusal, each credential borrowed since start", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
		f.on(awsRegion, reply{stdout: "eu-west-1"})
		s := newTestStore(t, f, c)
		_, err := s.Region(t.Context(), "dev")
		require.NoError(t, err)
		_, err = s.AWS(t.Context(), "dev")
		require.NoError(t, err)

		assert.Equal(t, Valid, s.Recheck(t.Context(), Key{AWS, "dev"}).Status)
		assert.Equal(t, 2, f.count(awsExport), "past the cache")
		assert.Equal(t, 1, f.count(awsRegion), "the credential alone")
	})

	t.Run("an identity never borrowed borrows its one credential", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghToken, reply{stdout: "gho_token0123456789abcdef"})
		s := newTestStore(t, f, c)
		assert.Equal(t, Valid, s.Recheck(t.Context(), Key{Provider: GitHub}).Status)
		assert.Equal(t, []string{ghToken}, f.all())
		assert.Equal(t, Status(""), s.Recheck(t.Context(), Key{Provider: Azure}).Status, "an Azure login has no resource to name")

		f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
		f.on(gcloudTok, reply{stdout: gcloudJSON})
		assert.Equal(t, Valid, s.Recheck(t.Context(), Key{AWS, "dev"}).Status)
		assert.Equal(t, Valid, s.Recheck(t.Context(), Key{Provider: Google}).Status)
	})
}

func TestAGoogleRefusalIsRechecked(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(gcloudTok, reply{stdout: gcloudJSON})
	s := newTestStore(t, f, c)
	s.MarkExpired(Key{Provider: Google}, "", "ya29.token0123456789abcdef")
	assert.Equal(t, Expired, s.Recheck(t.Context(), Key{Provider: Google}).Status, "gcloud hands back the refused token")
	assert.Equal(t, 1, f.count(gcloudTok))
}

// A refreshed credential never hashes the same, so a key remembers its last few.
func TestARefusalKeepsTheLastSixteen(t *testing.T) {
	f, c := newFakeTools(), newClock()
	s := newTestStore(t, f, c)
	for i := range maxRefused + 1 {
		s.MarkExpired(Key{Provider: GitHub}, "github.com", fmt.Sprintf("gho_refused%02d-0123456789", i))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.refused["github:github.com"]
	assert.Len(t, r.hashes, maxRefused)
	assert.Equal(t, "gho_refused01-0123456789", r.values[0])
}

// Proxies report one refusal once per request that carried it, so repeats must not
// push out another refused value.
func TestARefusalRepeatedTakesOnePlace(t *testing.T) {
	f, c := newFakeTools(), newClock()
	s := newTestStore(t, f, c)
	s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenB0123456789abcdef")
	for range maxRefused {
		s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")
	}

	f.on(ghToken, reply{stdout: "gho_tokenB0123456789abcdef"})
	_, err := s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExpired, "B is still refused")
	assert.Equal(t, Expired, s.State(Key{Provider: GitHub}).Status)

	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.refused["github:github.com"]
	assert.Equal(t, []string{"gho_tokenB0123456789abcdef", "gho_tokenA0123456789abcdef"}, r.values)
	assert.Len(t, r.hashes, 2)
}

// The tool can hand back a refused token after one that worked: the refusal stands
// again, and a recheck says so.
func TestARefusedTokenThatReturnsIsExpiredAgain(t *testing.T) {
	f, c := newFakeTools(), newClock()
	s := newTestStore(t, f, c)
	s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")

	f.on(ghToken, reply{stdout: "gho_tokenB0123456789abcdef"})
	c.advance(expiredBackoff)
	_, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	require.Equal(t, Valid, s.State(Key{Provider: GitHub}).Status)

	f.on(ghToken, reply{stdout: "gho_tokenA0123456789abcdef"})
	c.advance(githubTTL)
	_, err = s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExpired)
	st := s.State(Key{Provider: GitHub})
	assert.Equal(t, Expired, st.Status)
	assert.Equal(t, refusedDetail, st.Detail)
	assert.Equal(t, Expired, s.Recheck(t.Context(), Key{Provider: GitHub}).Status)
}
