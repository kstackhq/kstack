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

package google

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
	"github.com/kstackhq/kstack/sidecar/internal/services/poke"
)

const testBase = "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/endpoints/openapi"

// newService is a signed-out connector over a fresh app.db, under a keychain
// service no real entry is kept under.
func newService(t *testing.T, cfg Config) *Service {
	t.Helper()
	cfg.DB = appdbtest.Open(t)
	cfg.KeychainService = "kstack-test-google"
	s, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

// The provider is Google's installed-app sign-in: written endpoints, the
// build's client and secret, Vertex AI's scope required, and a refresh token
// asked for on every sign-in.
func TestTheProviderIsGooglesInstalledAppSignIn(t *testing.T) {
	p := provider(Config{ClientID: "client", ClientSecret: "secret", APIBaseURL: testBase})

	assert.Equal(t, "google", p.ID)
	assert.Equal(t, &endpoints, p.Endpoints)
	assert.Equal(t, "https://accounts.google.com", p.Endpoints.Issuer)
	assert.Equal(t, "client", p.ClientID)
	assert.Equal(t, "secret", p.ClientSecret)
	assert.Equal(t, testBase, p.APIBaseURL)
	assert.True(t, p.NoResource)
	assert.Equal(t, []string{"openid", "email", "profile", cloudPlatformScope}, p.Scopes)
	assert.Equal(t, cloudPlatformScope, p.RequiredScope)
	assert.Equal(t, url.Values{"access_type": {"offline"}, "prompt": {"consent"}}, p.Authorize(nil, false))
	assert.Equal(t, []string{"invalid_grant"}, p.SessionEnders)
}

// New is the engine at the configured base: signed out, it answers a request
// there signed out, and with no base it signs nothing.
func TestNewSignsTheConfiguredBase(t *testing.T) {
	s := newService(t, Config{ClientID: "client", APIBaseURL: testBase, Project: "my-project"})
	require.Equal(t, connectors.SignedOut, s.State().Phase)
	assert.Equal(t, "my-project", s.Project())

	req, err := http.NewRequest(http.MethodPost, testBase+"/chat/completions", nil)
	require.NoError(t, err)
	assert.ErrorIs(t, s.Sign(req), llm.ErrSignedOut)

	none := newService(t, Config{ClientID: "client"})
	err = none.Sign(req)
	require.Error(t, err)
	assert.NotErrorIs(t, err, llm.ErrSignedOut)
}

// A build with no client refuses Connect before anything starts.
func TestConnectWithNoClientIsRefused(t *testing.T) {
	s := newService(t, Config{APIBaseURL: testBase})

	assert.ErrorIs(t, s.Connect(t.Context(), false), connectors.ErrNoClient)
	assert.Equal(t, connectors.SignedOut, s.State().Phase)
}

// With a client, Connect is the engine's: one whose stores refuse the sign-in
// fails NotSaved before any browser opens.
func TestConnectWithAClientRunsTheEngine(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	s, err := New(Config{DB: db, KeychainService: "kstack-test-google", ClientID: "client", APIBaseURL: testBase})
	require.NoError(t, err)
	t.Cleanup(s.Close)
	require.NoError(t, db.Close())

	require.NoError(t, s.Connect(t.Context(), false))

	assert.Equal(t, connectors.State{Phase: connectors.Failed, Failure: connectors.NotSaved, Session: s.State().Session}, s.State())
}

// The engine's failure fails New.
func TestNewFailsOnAClosedDatabase(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = New(Config{DB: db})

	assert.Error(t, err)
}

// SyncCatalog keeps the google row's catalog the account's until its stop:
// signed out, it empties it.
func TestSyncCatalogEmptiesASignedOutCatalog(t *testing.T) {
	s := newService(t, Config{APIBaseURL: testBase})
	models := llm.New(llm.Provider{ID: ID, Dialect: llm.DialectChatCompletions, Catalog: []llm.Model{{ID: "stale"}}})
	frames, cancel := models.Subscribe()
	defer cancel()
	testutil.Recv(t, frames, "the first frame")

	stop := s.SyncCatalog(poke.New(), models, func(context.Context, llm.Provider) ([]llm.Model, error) {
		return nil, errors.New("signed out: never read")
	})
	defer stop()

	assert.Empty(t, testutil.Recv(t, frames, "the emptied catalog")[0].Catalog)
}
