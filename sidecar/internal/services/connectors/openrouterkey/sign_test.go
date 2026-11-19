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

package openrouterkey

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
)

// Sign signs only the API base: the same scheme, host and port, and a path
// that is the base's or continues it after a slash. A near miss is refused
// unsigned.
func TestTheKeyGoesNowhereElse(t *testing.T) {
	keyring := newFakeKeyring()
	keyring.put(keyAccount, `{"v":1,"key":"stored-openrouter-key-0001"}`)
	s := newService(t, Config{APIBaseURL: "https://openrouter.test/api/v1"}, withKeyring(keyring))
	require.Equal(t, Connected, s.State().Phase)
	for _, raw := range []string{
		"https://openrouter.test/api/v1x",
		"https://openrouter.test.example/api/v1/chat/completions",
		"http://openrouter.test/api/v1/chat/completions",
		"https://openrouter.test:8443/api/v1/chat/completions",
		"https://openrouter.test/api/v2/chat/completions",
		"https://openrouter.test/auth",
		"https://openrouter.test/",
	} {
		req, err := http.NewRequest(http.MethodPost, raw, nil)
		require.NoError(t, err)

		assert.Error(t, s.Sign(req), raw)
		assert.Empty(t, req.Header.Get("Authorization"), raw)
	}
	for _, raw := range []string{"https://OPENROUTER.test/api/v1", "https://openrouter.test:443/api/v1/models", "https://openrouter.test/api/v1/chat/completions"} {
		req, err := http.NewRequest(http.MethodPost, raw, nil)
		require.NoError(t, err)

		assert.NoError(t, s.Sign(req), raw)
		assert.Equal(t, "Bearer stored-openrouter-key-0001", req.Header.Get("Authorization"), raw)
	}
}

// Outside Connected there is nothing to sign with.
func TestSignIsSignedOutWithoutAKey(t *testing.T) {
	r := newRig(t)

	_, err := r.signedKey(r.svc)

	assert.ErrorIs(t, err, llm.ErrSignedOut)
}

// A binding signs with the key it began under, and once that generation ends
// it fails signed out, whatever key replaced it.
func TestABindingEndsWithItsSession(t *testing.T) {
	r := newRig(t)
	connect(t, r.svc)
	bound := r.svc.Bind()
	key, err := r.signedKey(bound)
	require.NoError(t, err)
	stored, _ := r.storedKey()
	assert.Equal(t, stored, key)

	require.NoError(t, r.svc.Disconnect())
	connect(t, r.svc)

	_, err = r.signedKey(bound)
	assert.ErrorIs(t, err, llm.ErrSignedOut)
	_, err = r.signedKey(r.svc.Bind())
	assert.NoError(t, err)
}

// A binding made with no key is dead for good, even once a sign-in under way
// connects.
func TestABindingWithNoSessionIsDead(t *testing.T) {
	browser := &parked{}
	r := newRig(t, withBrowser(browser.open))
	require.NoError(t, r.svc.Connect(t.Context()))
	bound := r.svc.Bind()

	_, err := r.signedKey(bound)

	assert.ErrorIs(t, err, llm.ErrSignedOut)
	bound.Refused(r.apiRequest("/chat/completions"))
}

// refusedWith tells signer of a 401 on a request it signed.
func (r *rig) refusedWith(signer llm.Signer) {
	r.t.Helper()
	req := r.apiRequest("/chat/completions")
	require.NoError(r.t, signer.Sign(req))
	signer.Refused(req)
}

// A 401 starts one GET /key under the key. Only that check's own 401 drops
// the key: the entry is deleted and the state is KeyRejected. A 200, or any
// other answer, or none, keeps it.
func TestARejectedKeyIsConfirmedFirst(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		drop   bool
	}{
		{name: "a 200", status: http.StatusOK},
		{name: "a 500", status: http.StatusInternalServerError},
		{name: "a 403", status: http.StatusForbidden},
		{name: "no answer", drop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			connect(t, r.svc)
			key, _ := r.storedKey()
			r.openRouter.set(func(f *fakeOpenRouter) { f.checkStatus, f.dropCheck = tc.status, tc.drop })

			r.refusedWith(r.svc)

			r.checkEnded.Await(t, "the check")
			// A dropped connection is retried once by the client, under the same key.
			checks := r.openRouter.keyChecks()
			require.NotEmpty(t, checks)
			for _, c := range checks {
				assert.Equal(t, key, c)
			}
			assert.Equal(t, Connected, r.svc.State().Phase)
			_, ok := r.keyring.entry(keyAccount)
			assert.True(t, ok)
		})
	}

	t.Run("a 401", func(t *testing.T) {
		r := newRig(t)
		connect(t, r.svc)
		bound := r.svc.Bind()
		r.openRouter.set(func(f *fakeOpenRouter) { f.checkStatus = http.StatusUnauthorized })

		r.refusedWith(bound)

		await(t, r.svc, "KeyRejected", failedWith(KeyRejected))
		_, ok := r.keyring.entry(keyAccount)
		assert.False(t, ok)
		_, err := r.signedKey(bound)
		assert.ErrorIs(t, err, llm.ErrSignedOut)
		connect(t, r.svc)
	})

	t.Run("a 401 whose entry cannot be deleted", func(t *testing.T) {
		r := newRig(t)
		connect(t, r.svc)
		r.keyring.failDelete(keyAccount, errBoom)
		r.openRouter.set(func(f *fakeOpenRouter) { f.checkStatus = http.StatusUnauthorized })

		r.refusedWith(r.svc)

		await(t, r.svc, "NotDeleted", failedWith(NotDeleted))
	})
}

// One check runs at a time: a 401 while one is under way joins it.
func TestOneKeyCheckAtATime(t *testing.T) {
	r := newRig(t)
	connect(t, r.svc)
	release := make(chan struct{})
	r.openRouter.set(func(f *fakeOpenRouter) { f.holdCheck = release })

	r.refusedWith(r.svc)
	r.openRouter.arrived.Await(t, "the check")
	r.refusedWith(r.svc)
	close(release)

	r.checkEnded.Await(t, "the check")
	assert.Len(t, r.openRouter.keyChecks(), 1)
}

// A 401 under a key that has ended, or on a request the key did not sign,
// starts no check; nor does a check's 401 drop a key that replaced its own.
func TestAStaleRefusalChecksNothing(t *testing.T) {
	r := newRig(t)
	connect(t, r.svc)
	bound := r.svc.Bind()
	req := r.apiRequest("/chat/completions")
	require.NoError(t, bound.Sign(req))
	require.NoError(t, r.svc.Disconnect())
	connect(t, r.svc)

	bound.Refused(req)
	r.svc.Refused(req)
	r.svc.Refused(r.apiRequest("/chat/completions"))

	assert.Empty(t, r.openRouter.keyChecks())

	release := make(chan struct{})
	r.openRouter.set(func(f *fakeOpenRouter) { f.holdCheck, f.checkStatus = release, http.StatusUnauthorized })
	r.refusedWith(r.svc)
	r.openRouter.arrived.Await(t, "the check")
	require.NoError(t, r.svc.Disconnect())
	connect(t, r.svc)
	close(release)

	r.checkEnded.Await(t, "the check")
	assert.Equal(t, Connected, r.svc.State().Phase)
}

// A plain-HTTP base, as a debug build may point at, covers port 80 alone.
func TestAnHTTPBaseCoversPort80(t *testing.T) {
	s := newService(t, Config{APIBaseURL: "http://openrouter.test/api/v1"}, withKeyring(newFakeKeyring()))

	for raw, want := range map[string]bool{
		"http://openrouter.test:80/api/v1/models": true,
		"http://openrouter.test:81/api/v1/models": false,
	} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		assert.Equal(t, want, s.covers(u), raw)
	}
}
