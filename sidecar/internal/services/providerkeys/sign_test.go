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

package providerkeys

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring/oskeyringtest"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/internal/signer"
)

const routerChat = routerAPI + "/chat/completions"

// bearer is the Authorization header sign leaves on a request to url.
func bearer(t *testing.T, sg llm.Signer, url string) (string, error) {
	t.Helper()
	r := request(t, url)
	err := sg.Sign(r)
	return r.Header.Get("Authorization"), err
}

func TestTheHighestCredentialSigns(t *testing.T) {
	t.Parallel()
	c := newFakeConnector()
	s := newService(t, Config{EnvKeys: map[string]string{routerID: envKey}, Connectors: map[string]Connector{routerID: c}})
	sg := s.Signer(routerID)

	got, err := bearer(t, sg.Bind(), routerChat)
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+envKey, got)

	require.NoError(t, s.Save(routerID, savedKey))
	got, err = bearer(t, sg.Bind(), routerChat)
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+savedKey, got)

	c.connect(5)
	got, err = bearer(t, sg.Bind(), routerChat)
	require.NoError(t, err)
	assert.Equal(t, "Bearer connector", got)
	got, err = bearer(t, sg, routerChat)
	require.NoError(t, err)
	assert.Equal(t, "Bearer connector", got, "the unbound signer follows the same order")

	c.connect(0)
	got, err = bearer(t, sg, routerChat)
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+savedKey, got, "the unbound signer falls back to the key")
}

func TestAProviderWithNoCredentialBindsEnded(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{})
	assert.Equal(t, signer.Ended{}, s.Signer(twinID).Bind())
	_, err := bearer(t, s.Signer(twinID), "https://twin.test/openai/v1/chat")
	assert.ErrorIs(t, err, llm.ErrSignedOut)
}

func TestTheKeyGoesNowhereElse(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{EnvKeys: map[string]string{twinID: envKey}})
	b := s.Signer(twinID).Bind()

	r := request(t, "https://twin.test/openai/v1/chat/completions")
	require.NoError(t, b.Sign(r))
	assert.Equal(t, "Bearer "+envKey, r.Header.Get("Authorization"))
	assert.Empty(t, r.Header.Get("X-Api-Key"))

	r = request(t, "https://twin.test/anthropic/v1/messages")
	require.NoError(t, b.Sign(r))
	assert.Equal(t, envKey, r.Header.Get("X-Api-Key"))
	assert.Empty(t, r.Header.Get("Authorization"))

	for _, url := range []string{
		"https://twin.test/other",
		"https://twin.test/openai/v1x",
		"http://twin.test/openai/v1/chat",
		"https://evil.test/openai/v1/chat",
	} {
		r := request(t, url)
		assert.ErrorIs(t, b.Sign(r), errOffBase, url)
		assert.Empty(t, r.Header, url)
	}
}

// One stub under both bases takes both headers: a debug build's.
func TestABaseUnderTwoRoutesTakesBothHeaders(t *testing.T) {
	t.Parallel()
	stub := Provider{ID: "stub", Routes: []Route{
		{BaseURL: "http://127.0.0.1:9", Header: "Authorization", Prefix: "Bearer "},
		{BaseURL: "http://127.0.0.1:9", Header: "X-Api-Key"},
	}}
	s := newService(t, Config{Providers: []Provider{stub}, EnvKeys: map[string]string{"stub": envKey}})
	r := request(t, "http://127.0.0.1:9/v1/messages")
	require.NoError(t, s.Signer("stub").Bind().Sign(r))
	assert.Equal(t, "Bearer "+envKey, r.Header.Get("Authorization"))
	assert.Equal(t, envKey, r.Header.Get("X-Api-Key"))
}

func TestABindingEndsWithItsKey(t *testing.T) {
	t.Parallel()
	k := oskeyringtest.New()
	c := newFakeConnector()
	s := newService(t, Config{Keyring: k, EnvKeys: map[string]string{routerID: envKey}, Connectors: map[string]Connector{routerID: c}})
	sg := s.Signer(routerID)

	underEnv := sg.Bind()
	k.FailSet = errors.New("locked")
	require.ErrorIs(t, s.Save(routerID, savedKey), ErrNotSaved)
	_, err := bearer(t, underEnv, routerChat)
	require.NoError(t, err, "a refused Save leaves the binding")
	require.NoError(t, s.Delete(routerID))
	_, err = bearer(t, underEnv, routerChat)
	require.NoError(t, err, "a Delete with no key held leaves the binding")

	k.FailSet = nil
	require.NoError(t, s.Save(routerID, savedKey))
	_, err = bearer(t, underEnv, routerChat)
	assert.ErrorIs(t, err, llm.ErrCredentialChanged, "a saved key ends the binding")

	underSaved := sg.Bind()
	k.FailDelete = errors.New("locked")
	require.ErrorIs(t, s.Delete(routerID), ErrNotDeleted)
	_, err = bearer(t, underSaved, routerChat)
	assert.ErrorIs(t, err, llm.ErrCredentialChanged, "a Delete that drops the key ends its binding")

	underEnv = sg.Bind()
	c.connect(9)
	await(t, s, routerID, "the connector's session", signedBy(SourceConnector))
	_, err = bearer(t, underEnv, routerChat)
	assert.ErrorIs(t, err, llm.ErrCredentialChanged, "the connector's connect ends a key's binding")

	underConnector := sg.Bind()
	require.ErrorIs(t, s.Delete(routerID), ErrNotDeleted)
	got, err := bearer(t, underConnector, routerChat)
	require.NoError(t, err, "the connector's binding is the connector's")
	assert.Equal(t, "Bearer connector", got)
}

// refusedWith tells sg of a 401 to a request it signed.
func refusedWith(t *testing.T, sg llm.Signer) {
	t.Helper()
	r := request(t, routerChat)
	require.NoError(t, sg.Sign(r))
	sg.Refused(r)
}

func TestARejectedKeyIsKept(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{})
	require.NoError(t, s.Save(routerID, savedKey))
	old := s.Signer(routerID).Bind()
	r := request(t, routerChat)
	require.NoError(t, old.Sign(r))

	require.NoError(t, s.Save(routerID, otherKey))
	old.Refused(r)
	assert.Empty(t, state(t, s, routerID).Failure, "a 401 to a replaced key marks nothing")

	b := s.Signer(routerID).Bind()
	refusedWith(t, b)
	assert.Equal(t, KeyRejected, state(t, s, routerID).Failure)
	got, err := bearer(t, b, routerChat)
	require.NoError(t, err, "a rejected key still signs")
	assert.Equal(t, "Bearer "+otherKey, got)

	require.NoError(t, s.Save(routerID, savedKey))
	assert.Empty(t, state(t, s, routerID).Failure, "a new key clears the mark")
}

func TestTheConnectorHearsItsOwn401(t *testing.T) {
	t.Parallel()
	c := newFakeConnector()
	c.connect(1)
	s := newService(t, Config{EnvKeys: map[string]string{routerID: envKey}, Connectors: map[string]Connector{routerID: c}})
	refusedWith(t, s.Signer(routerID))
	c.mu.Lock()
	assert.Equal(t, 1, c.refused)
	c.mu.Unlock()
	assert.Empty(t, state(t, s, routerID).Failure, "the connector's 401 is not the key's")
}

func TestARejectionDoesNotHideAFailedDelete(t *testing.T) {
	t.Parallel()
	k := oskeyringtest.New()
	s := newService(t, Config{Keyring: k, EnvKeys: map[string]string{routerID: envKey}})
	require.NoError(t, s.Save(routerID, savedKey))
	k.FailDelete = errors.New("locked")
	require.ErrorIs(t, s.Delete(routerID), ErrNotDeleted)

	refusedWith(t, s.Signer(routerID).Bind())
	assert.Equal(t, NotDeleted, state(t, s, routerID).Failure)

	k.FailDelete = nil
	require.NoError(t, s.Delete(routerID))
	assert.Equal(t, KeyRejected, state(t, s, routerID).Failure, "the environment key's mark shows once the delete lands")
}

// A 401 to a request off the provider's bases marks nothing.
func TestARejectionOffTheBasesMarksNothing(t *testing.T) {
	t.Parallel()
	s := newService(t, Config{EnvKeys: map[string]string{routerID: envKey}})
	r := request(t, "https://evil.test/v1")
	r.Header.Set("Authorization", "Bearer "+envKey)
	s.Signer(routerID).Bind().Refused(r)
	assert.Empty(t, state(t, s, routerID).Failure)
}
