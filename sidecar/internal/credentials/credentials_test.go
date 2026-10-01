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
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

func TestKeyString(t *testing.T) {
	assert.Equal(t, "aws:dev", Key{AWS, "dev"}.String())
	assert.Equal(t, "github", Key{GitHub, ""}.String())
	assert.Equal(t, "aws", Key{AWS, ""}.String())
}

func TestTheCacheExpires(t *testing.T) {
	t.Run("to the credential's expiry less a minute", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "2026-09-30T13:00:00Z")})
		s := newTestStore(t, f, c)

		_, err := s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		c.advance(58*time.Minute + 59*time.Second)
		_, err = s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		assert.Equal(t, 1, f.count(awsExport), "within the expiry the cache answers")

		c.advance(time.Second)
		_, err = s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		assert.Equal(t, 2, f.count(awsExport), "past it the tool runs again")
	})

	t.Run("a credential with no expiry keeps the default", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
		s := newTestStore(t, f, c)

		_, err := s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		c.advance(defaultTTL - time.Second)
		_, err = s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		assert.Equal(t, 1, f.count(awsExport))
		c.advance(time.Second)
		_, err = s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		assert.Equal(t, 2, f.count(awsExport))
	})

	t.Run("a GitHub token is kept for an hour", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghToken, reply{stdout: "gho_token0123456789abcdef\n"})
		s := newTestStore(t, f, c)

		_, err := s.GitHub(t.Context(), "github.com")
		require.NoError(t, err)
		c.advance(githubTTL - time.Second)
		_, err = s.GitHub(t.Context(), "github.com")
		require.NoError(t, err)
		assert.Equal(t, 1, f.count(ghToken))
		c.advance(time.Second)
		_, err = s.GitHub(t.Context(), "github.com")
		require.NoError(t, err)
		assert.Equal(t, 2, f.count(ghToken))
	})
}

func TestOneBorrowPerKeyAtATime(t *testing.T) {
	f, c := newFakeTools(), newClock()
	hold := make(chan struct{})
	f.on(awsExport, reply{stdout: awsJSON(secretKey, ""), hold: hold})
	f.on(ghToken, reply{stdout: "gho_token0123456789abcdef"})
	s := newTestStore(t, f, c)

	answers := make(chan AWSCredential, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			a, err := s.AWS(t.Context(), "dev")
			assert.NoError(t, err)
			answers <- a
		})
	}
	f.ran.Await(t, "the first run")

	// Another key runs while the first is held.
	_, err := s.GitHub(t.Context(), "github.com")
	require.NoError(t, err)
	assert.Equal(t, ghToken, f.ran.Await(t, "the other key's run"))

	close(hold)
	wg.Wait()
	assert.Equal(t, secretKey, (<-answers).SecretAccessKey)
	assert.Equal(t, secretKey, (<-answers).SecretAccessKey)
	assert.Equal(t, 1, f.count(awsExport), "two callers for one key run the tool once")

	// A caller after the flight finds the cache.
	_, err = s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, 1, f.count(awsExport))
}

func TestAWaiterThatGivesUpLeavesTheRun(t *testing.T) {
	f, c := newFakeTools(), newClock()
	hold := make(chan struct{})
	f.on(awsExport, reply{stdout: awsJSON(secretKey, ""), hold: hold})
	s := newTestStore(t, f, c)

	first, cancel := context.WithCancel(t.Context())
	firstErr := make(chan error, 1)
	go func() {
		_, err := s.AWS(first, "dev")
		firstErr <- err
	}()
	f.ran.Await(t, "the run")
	second := make(chan AWSCredential, 1)
	go func() {
		a, err := s.AWS(t.Context(), "dev")
		assert.NoError(t, err)
		second <- a
	}()

	cancel()
	assert.ErrorIs(t, testutil.Recv(t, firstErr, "the first caller's wait"), context.Canceled)
	close(hold)
	assert.Equal(t, secretKey, testutil.Recv(t, second, "the second caller's answer").SecretAccessKey)
	assert.Equal(t, 1, f.count(awsExport))
}

func TestAnExpiryIsRememberedForTheBackoff(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stderr: "Error loading SSO Token: Token for dev does not exist\n", exit: 255})
	f.on(ghToken, reply{stdout: "gho_token0123456789abcdef"})
	s := newTestStore(t, f, c, withBackoff(time.Minute))

	_, err := s.AWS(t.Context(), "dev")
	require.ErrorIs(t, err, ErrExpired)
	st := s.State(Key{AWS, "dev"})
	assert.Equal(t, Expired, st.Status)
	assert.Equal(t, "Error loading SSO Token: Token for dev does not exist", st.Detail)

	c.advance(time.Minute - time.Second)
	_, err = s.AWS(t.Context(), "dev")
	require.ErrorIs(t, err, ErrExpired)
	assert.Equal(t, 1, f.count(awsExport), "within the backoff nothing runs")
	assert.Equal(t, st, s.State(Key{AWS, "dev"}), "and no status is written")

	_, err = s.GitHub(t.Context(), "github.com")
	require.NoError(t, err, "another key runs")

	c.advance(time.Second)
	_, err = s.AWS(t.Context(), "dev")
	require.ErrorIs(t, err, ErrExpired)
	assert.Equal(t, 2, f.count(awsExport), "past the backoff the tool runs again")
}

func TestAnExcludedIdentityIsNeverBorrowed(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
	f.on("aws configure export-credentials --profile prod --format process", reply{stdout: awsJSON(secretKey, "")})
	var mu sync.Mutex
	var asked []Key
	excluded := func(k Key) bool {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, k)
		return k == Key{AWS, "dev"} || k.Provider == GitHub || k.Provider == Azure
	}
	t.Cleanup(safe.ResetSecrets)
	s := newStoreWithOptions(allBinaries, "/home/user", nil, excluded, withRunner(f.run), withClock(c.Now))
	t.Cleanup(s.Close)

	_, err := s.AWS(t.Context(), "dev")
	assert.ErrorIs(t, err, ErrExcluded)
	_, err = s.Region(t.Context(), "dev")
	assert.ErrorIs(t, err, ErrExcluded)
	_, err = s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExcluded)
	_, err = s.Azure(t.Context(), "https://management.azure.com/")
	assert.ErrorIs(t, err, ErrExcluded)
	assert.Empty(t, f.all(), "nothing ran")
	assert.Equal(t, State{Key: Key{AWS, "dev"}}, s.State(Key{AWS, "dev"}), "no status is written")

	_, err = s.AWS(t.Context(), "prod")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, asked, Key{GitHub, ""}, "asked by identity, never by host")
	assert.Contains(t, asked, Key{Azure, ""}, "asked by identity, never by resource")
}

func TestNoCLIIsMissing(t *testing.T) {
	f, c := newFakeTools(), newClock()
	t.Cleanup(safe.ResetSecrets)
	s := newStoreWithOptions(Binaries{GH: "/bin/gh"}, "/home/user", nil, nil, withRunner(f.run), withClock(c.Now))
	t.Cleanup(s.Close)

	_, err := s.AWS(t.Context(), "dev")
	assert.ErrorIs(t, err, ErrNoCLI)
	assert.Equal(t, Missing, s.State(Key{AWS, ""}).Status, "under the bare key, as Discover reports it")
	assert.Equal(t, Status(""), s.State(Key{AWS, "dev"}).Status)

	t.Run("an empty home names no tool", func(t *testing.T) {
		s := newStoreWithOptions(allBinaries, "", nil, nil, withRunner(f.run), withClock(c.Now))
		t.Cleanup(s.Close)
		_, err := s.GitHub(t.Context(), "github.com")
		assert.ErrorIs(t, err, ErrNoCLI)
		assert.Empty(t, f.all())
	})
}

func TestASecretIsRegisteredWithSafe(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: awsJSON("first-secret-0123456789", "2026-09-30T12:30:00Z")})
	s := newTestStore(t, f, c)

	_, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, safe.Redacted, safe.Redact("first-secret-0123456789"))
	assert.Equal(t, safe.Redacted, safe.Redact("session-token-0123456789"))

	f.on(awsExport, reply{stdout: awsJSON("second-secret-0123456789", "")})
	c.advance(time.Hour)
	_, err = s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, "first-secret-0123456789", safe.Redact("first-secret-0123456789"), "a re-borrow replaces the slot")
	assert.Equal(t, safe.Redacted, safe.Redact("second-secret-0123456789"))

	s.MarkExpired(Key{AWS, "dev"}, "dev", "second-secret-0123456789")
	f.on(awsExport, reply{stdout: awsJSON("third-secret-0123456789", "")})
	assert.Equal(t, Valid, s.Recheck(t.Context(), Key{AWS, "dev"}).Status)
	assert.Equal(t, safe.Redacted, safe.Redact("third-secret-0123456789"))
	assert.Equal(t, safe.Redacted, safe.Redact("second-secret-0123456789"), "a refused value stays blanked")
}

func TestAFailureThatIsNotAnExpiryLeavesTheStatus(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stderr: "Could not connect to the endpoint URL\n", exit: 255})
	s := newTestStore(t, f, c)

	_, err := s.AWS(t.Context(), "dev")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrExpired))
	assert.Equal(t, "credentials: aws exited 255", err.Error())
	assert.Equal(t, Status(""), s.State(Key{AWS, "dev"}).Status)
}

func TestABorrowAfterCloseIsErrClosed(t *testing.T) {
	f, c := newFakeTools(), newClock()
	s := newTestStore(t, f, c)
	s.Close()
	_, err := s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrClosed)
	assert.Empty(t, f.all())
}

// Close returns only once every run it stopped has returned, so no tool outlives
// the store: the sidecar exits right after it.
func TestCloseWaitsForTheRunsItStops(t *testing.T) {
	started, cancelled := testutil.NewSignal(), testutil.NewSignal()
	release := make(chan struct{})
	var reaped atomic.Bool
	run := func(ctx context.Context, _ []string, _ string, _ ...string) ([]byte, []byte, int, error) {
		started.Fire()
		<-ctx.Done()
		cancelled.Fire()
		<-release // the kill and the reap
		reaped.Store(true)
		return nil, nil, -1, ctx.Err()
	}
	t.Cleanup(safe.ResetSecrets)
	s := newStoreWithOptions(allBinaries, "/home/user", nil, nil, withRunner(run))
	go func() { _, _ = s.GitHub(context.Background(), "github.com") }()
	started.Wait(t, "the run")

	closed := make(chan bool, 1)
	go func() {
		s.Close()
		closed <- reaped.Load()
	}()
	cancelled.Wait(t, "the cancel")
	close(release)
	assert.True(t, testutil.Recv(t, closed, "the close"), "Close returned before the run was reaped")
}

func TestAnAnswerThatDoesNotParseIsAnError(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: "not json"})
	f.on(awsRegion, reply{stderr: "boom", exit: 2})
	f.on(ghToken, reply{stdout: "\n"})
	f.on(gcloudTok, reply{stdout: "{}"})
	f.on(azARM, reply{stdout: "[]"})
	s := newTestStore(t, f, c)

	_, err := s.AWS(t.Context(), "dev")
	assert.EqualError(t, err, "credentials: aws answered no credential")
	_, err = s.Region(t.Context(), "dev")
	assert.EqualError(t, err, "credentials: aws exited 2")
	_, err = s.GitHub(t.Context(), "github.com")
	assert.EqualError(t, err, "credentials: gh answered no token")
	_, err = s.Google(t.Context())
	assert.EqualError(t, err, "credentials: gcloud answered no token")
	_, err = s.Azure(t.Context(), arm)
	assert.EqualError(t, err, "credentials: az answered no token")
	assert.Empty(t, s.states(), "none of them writes a status")
}

// A run past the bound ends, since a tool can hang on a prompt or the network.
func TestARunIsBounded(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(ghToken, reply{stdout: "gho_token0123456789abcdef", hold: make(chan struct{})})
	s := newTestStore(t, f, c, withTimeout(time.Millisecond))

	_, err := s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.EqualError(t, err, "credentials: gh did not finish: context deadline exceeded")
}
