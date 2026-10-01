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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// The discovery command lines.
const (
	ghStatus       = "gh auth status --hostname github.com"
	awsProfiles    = "aws configure list-profiles"
	gcloudAccount  = "gcloud config get-value account"
	gcloudProject  = "gcloud config get-value project"
	azShow         = "az account show --output json"
	azShowJSON     = `{"id":"sub-id","name":"my-sub","tenantId":"tenant-id","user":{"name":"alice@example.com","type":"user"}}`
	ghStatusActive = "github.com\n" +
		"  ✓ Logged in to github.com account bob (keyring)\n  - Active account: false\n" +
		"  ✓ Logged in to github.com account alice (keyring)\n  - Active account: true\n"
)

// stageSignedIn stages every tool signed in.
func stageSignedIn(f *fakeTools) {
	f.on(ghStatus, reply{stdout: ghStatusActive})
	f.on(awsProfiles, reply{stdout: "dev\nprod\n"})
	f.on(gcloudAccount, reply{stdout: "alice@example.com\n"})
	f.on(gcloudProject, reply{stdout: "my-project\n"})
	f.on(azShow, reply{stdout: azShowJSON})
}

func tool(found Found, p Provider) Tool {
	for _, t := range found.Tools {
		if t.Provider == p {
			return t
		}
	}
	return Tool{}
}

func TestDiscoverReadsEachTool(t *testing.T) {
	f, c := newFakeTools(), newClock()
	stageSignedIn(f)
	t.Cleanup(safe.ResetSecrets)
	s := newStoreWithOptions(allBinaries, "/home/user", func() []string { return []string{"a", "b"} }, nil,
		withRunner(f.run), withClock(c.Now))
	t.Cleanup(s.Close)

	found := s.Discover(t.Context())

	assert.Equal(t, []Provider{AWS, GitHub, Google, Azure}, []Provider{
		found.Tools[0].Provider, found.Tools[1].Provider, found.Tools[2].Provider, found.Tools[3].Provider,
	})
	assert.Equal(t, Tool{Provider: GitHub, Installed: true, Present: true, Identities: []Identity{
		{Key: Key{Provider: GitHub}, Label: "@alice", User: "alice"},
	}}, tool(found, GitHub), "the active one of two accounts")
	assert.Equal(t, Tool{Provider: AWS, Installed: true, Present: true, Identities: []Identity{
		{Key: Key{AWS, "dev"}, Label: "dev"}, {Key: Key{AWS, "prod"}, Label: "prod"},
	}}, tool(found, AWS))
	assert.Equal(t, Tool{Provider: Google, Installed: true, Present: true, Identities: []Identity{
		{Key: Key{Provider: Google}, Label: "alice@example.com · my-project", User: "alice@example.com", Project: "my-project"},
	}}, tool(found, Google))
	assert.Equal(t, Tool{Provider: Azure, Installed: true, Present: true, Identities: []Identity{
		{Key: Key{Provider: Azure}, Label: "alice@example.com · my-sub", User: "alice@example.com",
			Subscription: "my-sub", SubscriptionID: "sub-id", Tenant: "tenant-id"},
	}}, tool(found, Azure))
	assert.Equal(t, []string{"a", "b"}, found.Contexts)

	for _, line := range f.all() {
		for _, borrow := range []string{"export-credentials", "auth token", "config-helper", "get-access-token"} {
			assert.NotContains(t, line, borrow, "no fake was asked for a token")
		}
	}

	t.Run("an older gh names its user as", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghStatus, reply{stderr: "github.com\n  ✓ Logged in to github.com as carol (oauth_token)\n"})
		s := newTestStore(t, f, c)
		assert.Equal(t, "carol", tool(s.Discover(t.Context()), GitHub).Identities[0].User)
	})

	t.Run("a gh that names no user", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghStatus, reply{stdout: "github.com\n"})
		s := newTestStore(t, f, c)
		assert.Equal(t, []Identity{{Key: Key{Provider: GitHub}, Label: "github.com"}}, tool(s.Discover(t.Context()), GitHub).Identities)
	})

	t.Run("a closed store proves nothing", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		stageSignedIn(f)
		f.on(ghStatus, reply{stdout: ghStatusActive, hold: make(chan struct{})})
		s := newTestStore(t, f, c)
		s.Close()
		assert.Equal(t, Tool{Provider: GitHub, Installed: true}, tool(s.Discover(t.Context()), GitHub))
	})

	t.Run("a tool absent", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		t.Cleanup(safe.ResetSecrets)
		s := newStoreWithOptions(Binaries{}, "/home/user", nil, nil, withRunner(f.run), withClock(c.Now))
		t.Cleanup(s.Close)
		found := s.Discover(t.Context())
		assert.Equal(t, Tool{Provider: AWS}, tool(found, AWS))
		assert.Empty(t, f.all())
	})

	t.Run("each signed into nothing", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghStatus, reply{stderr: "You are not logged into any GitHub hosts. To log in, run: gh auth login\n", exit: 1})
		f.on(awsProfiles, reply{})
		f.on(gcloudAccount, reply{stderr: "(unset)\n"})
		f.on(azShow, reply{stderr: "Please run 'az login' to setup account.\n", exit: 1})
		s := newTestStore(t, f, c)
		found := s.Discover(t.Context())
		for _, p := range providers {
			assert.Equal(t, Tool{Provider: p, Installed: true}, tool(found, p), p)
		}
	})
}

func TestDiscoverWritesWhatItProved(t *testing.T) {
	t.Run("gh signed into another host alone", func(t *testing.T) {
		for _, stderr := range []string{
			"You are not logged into any accounts on github.com\n",
			"Hostname \"github.com\" not found among authenticated GitHub hosts\n",
		} {
			f, c := newFakeTools(), newClock()
			f.on(ghStatus, reply{stderr: stderr, exit: 1})
			s := newTestStore(t, f, c)
			s.mu.Lock()
			s.setLocked(Key{Provider: GitHub}, Valid, "")
			s.mu.Unlock()

			found := s.Discover(t.Context())
			assert.Equal(t, Tool{Provider: GitHub, Installed: true}, tool(found, GitHub), stderr)
			assert.Equal(t, Missing, s.State(Key{Provider: GitHub}).Status, stderr)
		}
	})
	t.Run("a logout replaces Valid and Expired with Missing under the bare key", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsProfiles, reply{})
		s := newTestStore(t, f, c)
		s.mu.Lock()
		s.setLocked(Key{AWS, "dev"}, Valid, "")
		s.setLocked(Key{AWS, "prod"}, Expired, "x")
		s.setLocked(Key{AWS, "ops"}, Excluded, "")
		s.mu.Unlock()

		s.Discover(t.Context())
		assert.Equal(t, map[string]Status{"aws": Missing, "aws:ops": Excluded}, statuses(s.states()))
	})

	t.Run("gh exiting 0 turns Expired Valid and clears the refusal and the backoff", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(ghToken, reply{stdout: "gho_tokenA0123456789abcdef"})
		f.on(ghStatus, reply{stdout: ghStatusActive})
		s := newTestStore(t, f, c)
		_, err := s.GitHub(t.Context(), "github.com")
		require.NoError(t, err)
		s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")
		_, err = s.GitHub(t.Context(), "github.com")
		require.ErrorIs(t, err, ErrExpired)

		s.Discover(t.Context())
		assert.Equal(t, Valid, s.State(Key{Provider: GitHub}).Status)
		_, err = s.GitHub(t.Context(), "github.com")
		require.NoError(t, err, "GitHub accepted the token gh holds")
		assert.Equal(t, 3, f.count(ghToken), "the next borrow runs gh")
	})

	t.Run("a listed profile a borrow set Expired stays Expired", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stderr: "Error loading SSO Token\n", exit: 255})
		f.on(awsProfiles, reply{stdout: "dev\nprod\n"})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		require.ErrorIs(t, err, ErrExpired)

		s.Discover(t.Context())
		assert.Equal(t, map[string]Status{"aws:dev": Expired, "aws:prod": Valid}, statuses(s.states()),
			"one with no status becomes Valid")
	})

	t.Run("a removed profile's state goes, and so does the bare key's Missing", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsProfiles, reply{stdout: "dev\n"})
		s := newTestStore(t, f, c)
		s.mu.Lock()
		s.setLocked(Key{AWS, "gone"}, Valid, "")
		s.setLocked(Key{Provider: AWS}, Missing, "")
		s.mu.Unlock()

		s.Discover(t.Context())
		assert.Equal(t, map[string]Status{"aws:dev": Valid}, statuses(s.states()))
	})

	t.Run("a profile's borrow removes the bare key's Missing", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsProfiles, reply{})
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
		s := newTestStore(t, f, c)
		s.Discover(t.Context())
		require.Equal(t, Missing, s.State(Key{Provider: AWS}).Status)

		_, err := s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		assert.Equal(t, map[string]Status{"aws:dev": Valid}, statuses(s.states()))
	})

	t.Run("an exit that proves nothing leaves the table and the last Found", func(t *testing.T) {
		logs := testutil.CaptureLogs(t)
		f, c := newFakeTools(), newClock()
		f.on(ghStatus, reply{stdout: ghStatusActive})
		s := newTestStore(t, f, c)
		first := s.Discover(t.Context())
		s.mu.Lock()
		s.setLocked(Key{Provider: GitHub}, Expired, "x")
		s.mu.Unlock()

		f.on(ghStatus, reply{stdout: "printed-by-gh", stderr: "error connecting to api.github.com\n", exit: 1})
		found := s.Discover(t.Context())
		assert.Equal(t, tool(first, GitHub), tool(found, GitHub))
		assert.Equal(t, Expired, s.State(Key{Provider: GitHub}).Status)
		assert.Contains(t, logs.String(), `"exit":1`)
		assert.NotContains(t, logs.String(), "printed-by-gh")
		assert.NotContains(t, logs.String(), "api.github.com")
	})
}

func TestFoundIsTheLastDiscover(t *testing.T) {
	f, c := newFakeTools(), newClock()
	stageSignedIn(f)
	s := newTestStore(t, f, c)

	assert.Equal(t, Found{}, s.Found())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.WaitFound(ctx)
	assert.ErrorIs(t, err, context.Canceled, "before the first Discover ends")

	waited := make(chan Found, 1)
	go func() {
		found, err := s.WaitFound(t.Context())
		assert.NoError(t, err)
		waited <- found
	}()
	found := s.Discover(t.Context())
	assert.Equal(t, found, s.Found())
	assert.Equal(t, found, testutil.Recv(t, waited, "WaitFound"))
	assert.Equal(t, "my-project", tool(found, Google).Identities[0].Project)
	azure := tool(found, Azure).Identities[0]
	assert.Equal(t, "my-sub", azure.Subscription)
	assert.Equal(t, "sub-id", azure.SubscriptionID)
	assert.Equal(t, "tenant-id", azure.Tenant)
	assert.True(t, strings.HasPrefix(tool(found, GitHub).Identities[0].Label, "@"))
}

// A refusal that lands after gh has checked its token, while another tool's look
// still runs, is newer than the proof, so Discover leaves it standing.
func TestDiscoverKeepsARefusalNewerThanItsCheck(t *testing.T) {
	f, c := newFakeTools(), newClock()
	stageSignedIn(f)
	hold := make(chan struct{})
	f.on(awsProfiles, reply{stdout: "dev\n", hold: hold})
	f.on(ghToken, reply{stdout: "gho_tokenA0123456789abcdef"})
	s := newTestStore(t, f, c)

	done := make(chan Found, 1)
	go func() { done <- s.Discover(t.Context()) }()
	for seen := map[string]bool{}; !seen[ghStatus] || !seen[awsProfiles]; {
		seen[f.ran.Await(t, "the looks")] = true
	}
	s.MarkExpired(Key{Provider: GitHub}, "github.com", "gho_tokenA0123456789abcdef")
	close(hold)
	testutil.Recv(t, done, "the discovery")

	assert.Equal(t, Expired, s.State(Key{Provider: GitHub}).Status)
	_, err := s.GitHub(t.Context(), "github.com")
	assert.ErrorIs(t, err, ErrExpired, "the refused token is still refused")
}
