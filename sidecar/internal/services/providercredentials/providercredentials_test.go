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

package providercredentials

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/logging"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring/oskeyringtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/poke"
)

func TestNewRefusesAProviderItCannotUse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		providers []Provider
	}{
		{"an empty id", []Provider{{Routes: routerProvider.Routes}}},
		{"a duplicate id", []Provider{routerProvider, routerProvider}},
		{"a base with no host", []Provider{{ID: "x", Routes: []Route{{BaseURL: "localhost:8080"}}}}},
		{"a base that does not parse", []Provider{{ID: "x", Routes: []Route{{BaseURL: "http://[::1"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{Providers: tc.providers})
			assert.Error(t, err)
		})
	}
}

func TestStatesFollowTheRows(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{EnvKeys: map[string]string{twinID: envKey}})
	assert.Equal(t, []State{
		{ID: routerID, Label: "Router", KeyVar: "ROUTER_API_KEY", Source: SourceNone},
		{ID: twinID, Label: "Twin", KeyVar: "TWIN_API_KEY", Source: SourceEnvironmentKey, HasEnvironmentKey: true},
	}, s.States())
	_, ok := s.State("nope")
	assert.False(t, ok)
	assert.Nil(t, s.Signer("nope"))
}

func TestTheSourceTable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		connector  bool
		session    uint64
		saved, env bool
		want       Source
	}{
		{"connected over both keys", true, 7, true, true, SourceConnector},
		{"connected with no key", true, 7, false, false, SourceConnector},
		{"a saved key over the environment's", true, 0, true, true, SourceSavedKey},
		{"a saved key with no connector", false, 0, true, false, SourceSavedKey},
		{"the environment's key", true, 0, false, true, SourceEnvironmentKey},
		{"the environment's key with no connector", false, 0, false, true, SourceEnvironmentKey},
		{"nothing", true, 0, false, false, SourceNone},
		{"nothing and no connector", false, 0, false, false, SourceNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := oskeyringtest.New()
			cfg := Config{Keyring: k, EnvKeys: map[string]string{}}
			if tc.saved {
				require.NoError(t, k.Set(account(routerID), encode(savedKey)))
			}
			if tc.env {
				cfg.EnvKeys[routerID] = envKey
			}
			var c *fakeConnector
			if tc.connector {
				c = newFakeConnector()
				cfg.Connectors = map[string]Connector{routerID: c}
			}
			s := newService(t, cfg)
			if tc.session != 0 {
				c.connect(tc.session)
				await(t, s, routerID, "the connector signs", signedBy(SourceConnector))
			}
			assert.Equal(t, tc.want, state(t, s, routerID).Source)
		})
	}
}

// New reads the connector's session before it returns.
func TestTheLaunchStateHasTheConnectorsSession(t *testing.T) {
	t.Parallel()
	c := newFakeConnector()
	c.connect(3)
	s := newService(t, Config{Connectors: map[string]Connector{routerID: c}})
	assert.Equal(t, SourceConnector, state(t, s, routerID).Source)
	c.connect(0)
	await(t, s, routerID, "the session ends", signedBy(SourceNone))
}

func TestSubscribeIsAGauge(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{})
	frames, cancel := s.Subscribe()
	defer cancel()
	assert.Equal(t, s.States(), testutil.Recv(t, frames, "the current list"))
	require.NoError(t, s.Save(twinID, savedKey))
	frame := testutil.Recv(t, frames, "the change")
	assert.Equal(t, SourceSavedKey, frame[1].Source)
}

// Every change is built and sent under one lock, so the gauge never ends on a
// list older than the providers.
func TestConcurrentChangesPublishTheLatestList(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{})
	for range 50 {
		var wg sync.WaitGroup
		for _, id := range []string{routerID, twinID} {
			wg.Go(func() { assert.NoError(t, s.Save(id, savedKey)) })
			wg.Go(func() { assert.NoError(t, s.Delete(id)) })
		}
		wg.Wait()
		frames, cancel := s.Subscribe()
		assert.Equal(t, s.States(), testutil.Recv(t, frames, "the latest list"))
		cancel()
	}
}

func TestSyncCatalogsFollowsTheCredential(t *testing.T) {
	t.Parallel()
	written := []llm.Model{{ID: "m"}}
	models := llm.New(
		llm.Provider{ID: routerID, Dialect: llm.DialectChatCompletions},
		llm.Provider{ID: twinID, Dialect: llm.DialectChatCompletions},
	)
	frames, cancel := models.Subscribe()
	defer cancel()
	testutil.Recv(t, frames, "the first frame")

	s := newService(t, Config{EnvKeys: map[string]string{routerID: envKey}})
	stop := s.SyncCatalogs(poke.New(), models, func(context.Context, llm.Provider) ([]llm.Model, error) { return written, nil })
	defer stop()
	listed := func(id string) bool {
		p, _ := models.Provider(id)
		return len(p.Catalog) > 0
	}
	awaitModels := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			testutil.Recv(t, frames, what)
		}
	}
	awaitModels("the keyed provider's models", func() bool { return listed(routerID) })
	assert.False(t, listed(twinID))

	require.NoError(t, s.Save(twinID, savedKey))
	awaitModels("the saved provider's models", func() bool { return listed(twinID) })
	require.NoError(t, s.Delete(twinID))
	awaitModels("the removed provider's models leave", func() bool { return !listed(twinID) })
}

// No saved key reaches a log line, and each is registered with the redactor
// only while held. Serial: it swaps the default logger.
func TestNoSavedKeyLeavesTheKeyring(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const first, second = "first-key-0123456789abcdef", "second-key-0123456789abcdef"
	k := oskeyringtest.New()
	s := newService(t, Config{Keyring: k})
	require.NoError(t, s.Save(routerID, first))
	assert.True(t, safe.HasSecret(first))
	b := s.Signer(routerID).Bind()
	refusedWith(t, b)
	require.NoError(t, s.Save(routerID, second))
	assert.False(t, safe.HasSecret(first), "a replaced key is forgotten")
	k.FailDelete = errors.New("locked")
	require.ErrorIs(t, s.Delete(routerID), ErrNotDeleted)
	k.FailDelete = nil
	require.NoError(t, s.Delete(routerID))
	k.FailSet = errors.New("locked")
	require.ErrorIs(t, s.Save(routerID, first), ErrNotSaved)

	for _, key := range []string{first, second} {
		assert.NotContains(t, logs.String(), key)
		assert.False(t, safe.HasSecret(key), "%s is forgotten once removed", key)
	}
}

// A refusal is logged while its key is still registered, so an error that
// quotes the key is redacted. Serial: it swaps the default logger.
func TestARefusedSaveLogsNoKey(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(logging.Init(&logs, slog.LevelDebug))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const key = "quoted-key-0123456789abcdef"
	k := oskeyringtest.New()
	k.FailSet = errors.New("keyring refused " + encode(key))
	s := newService(t, Config{Keyring: k})
	require.ErrorIs(t, s.Save(routerID, key), ErrNotSaved)

	assert.Contains(t, logs.String(), "could not save the key")
	assert.NotContains(t, logs.String(), key)
}
