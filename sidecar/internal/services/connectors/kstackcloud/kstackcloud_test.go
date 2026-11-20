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

package kstackcloud

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring/oskeyringtest"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
)

// The provider is the Kstack Cloud account under the configured fixed client, and
// signs nothing.
func TestTheProviderIsTheKstackCloudSignIn(t *testing.T) {
	t.Parallel()
	p := provider(Config{Issuer: "https://issuer.test", ClientID: "kstack-desktop"})

	assert.Equal(t, "kstack-cloud", p.ID)
	assert.Equal(t, "https://issuer.test", p.Issuer)
	assert.Equal(t, "kstack-desktop", p.ClientID)
	assert.Equal(t, []string{"openid", "offline_access", "email", "profile"}, p.Scopes)
	assert.Equal(t, "/oauth/callback", p.CallbackPath)
	assert.Equal(t, []string{"invalid_grant"}, p.SessionEnders)
	assert.Empty(t, p.APIBaseURL)
	assert.Empty(t, p.RequiredScope)
	assert.Empty(t, p.FirstClientID)
	assert.Empty(t, p.ClientGone)
	assert.Nil(t, p.NewData)
	assert.Nil(t, p.Authorize)
}

// The installed build's whole token is dropped at launch, and nothing else in
// the keyring is touched.
func TestNewDeletesTheOldEntry(t *testing.T) {
	t.Parallel()
	k := &fakeKeyring{entries: map[string]string{"auth-token": `{"refresh_token":"r"}`, "kstack-cloud-refresh-token": "kept"}}

	newTestService(t, newFakeEngine(), k)

	_, err := k.Get("auth-token")
	require.ErrorIs(t, err, oskeyring.ErrNoEntry)
	v, err := k.Get("kstack-cloud-refresh-token")
	require.NoError(t, err)
	assert.Equal(t, "kept", v)
}

// A keyring that will not delete the old entry fails nothing, and the next
// launch tries again.
func TestAFailedDeleteOfTheOldEntryIsRetried(t *testing.T) {
	t.Parallel()
	k := &fakeKeyring{entries: map[string]string{"auth-token": "old"}, deleteErr: errors.New("no keyring")}
	e := newFakeEngine()

	s := newTestService(t, e, k)
	assert.Same(t, e, s.engine)
	newTestService(t, newFakeEngine(), k)

	assert.Equal(t, 2, k.deletes)
}

// The old entry is deleted once the keyring opens, and is never read.
func TestTheOldEntryIsDeletedUnread(t *testing.T) {
	t.Parallel()
	k := oskeyringtest.New()
	require.NoError(t, k.Set("auth-token", "old"))

	_, err := newService(oskeyring.Gate(k, oskeyring.Pace{}), func() (engine, error) { return newFakeEngine(), nil })

	require.NoError(t, err)
	assert.Equal(t, []oskeyringtest.Call{
		{Kind: oskeyringtest.Set, Account: "auth-token"},
		{Kind: oskeyringtest.Get, Account: "keyring-probe"},
		{Kind: oskeyringtest.Delete, Account: "auth-token"},
	}, k.Calls())
}

// A keyring whose opening failed, a dismissed prompt included, is not asked
// to delete the old entry: the delete would raise the prompt again.
func TestAKeyringThatDidNotOpenKeepsTheOldEntry(t *testing.T) {
	t.Parallel()
	k := oskeyringtest.New()
	require.NoError(t, k.Set("auth-token", "old"))
	k.FailGets(1, errors.New("dismissed"))

	_, err := newService(oskeyring.Gate(k, oskeyring.Pace{}), func() (engine, error) { return newFakeEngine(), nil })

	require.NoError(t, err)
	assert.Equal(t, []oskeyringtest.Call{
		{Kind: oskeyringtest.Set, Account: "auth-token"},
		{Kind: oskeyringtest.Get, Account: "keyring-probe"},
	}, k.Calls())
}

// An engine that cannot be built is New's error.
func TestNewAnswersTheEnginesError(t *testing.T) {
	t.Parallel()
	want := errors.New("no statements")

	_, err := newService(&fakeKeyring{}, func() (engine, error) { return nil, want })

	require.ErrorIs(t, err, want)
}

// New builds the engine over app.db, signed out with no session stored, and
// reaches no network.
func TestNewIsSignedOutWithNoSession(t *testing.T) {
	t.Parallel()
	s, err := New(Config{DB: appdbtest.Open(t), Keyring: oskeyringtest.New(), Issuer: "https://issuer.test", ClientID: "kstack-desktop"})
	require.NoError(t, err)
	t.Cleanup(s.Close)

	assert.Equal(t, connectors.SignedOut, s.State().Phase)
}

// State, Disconnect and Close are the engine's.
func TestStateDisconnectAndCloseAreTheEngines(t *testing.T) {
	t.Parallel()
	e := newFakeEngine()
	e.state = connectors.State{Phase: connectors.Connected, Subject: "u1"}
	s := newTestService(t, e, &fakeKeyring{})

	assert.Equal(t, e.state, s.State())
	require.NoError(t, s.Disconnect(context.Background()))
	s.Close()

	assert.Equal(t, []string{"Disconnect", "Close"}, e.recorded())
}
