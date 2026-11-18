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

package oauthconn

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// A first launch is signed out, with no registration.
func TestAFirstLaunchIsSignedOut(t *testing.T) {
	r := newRig(t)

	assert.Equal(t, State{Phase: SignedOut}, r.svc.State())
}

// New reads both stores and reaches no network. A session exists only when the
// row and the keyring's entry name the same sign-in; anything else is signed
// out, with the row's session cleared and the entry deleted.
func TestNewRestoresWhatTheStoresHold(t *testing.T) {
	registered := registration{Data: hostData("h"), ClientID: "c", Email: "ada@example.com"}
	signedIn := registered
	signedIn.SessionID = "s1"
	for name, c := range map[string]struct {
		row *registration
		// entry is the account's raw value; empty is no entry.
		entry       string
		want        Phase
		wantSession string // the row's session afterwards
		wantEntry   bool   // the entry still there
	}{
		"a session":                        {row: &signedIn, entry: `{"v":1,"token":"refresh-stored","session":"s1"}`, want: Connected, wantSession: "s1", wantEntry: true},
		"a session and no entry":           {row: &signedIn, want: SignedOut},
		"an entry beside a signed-out row": {row: &registered, entry: `{"v":1,"token":"refresh-stored","session":"s1"}`, want: SignedOut},
		"an entry of another sign-in":      {row: &signedIn, entry: `{"v":1,"token":"refresh-stored","session":"s2"}`, want: SignedOut},
		"an entry naming no session":       {row: &registered, entry: `{"v":1,"token":"refresh-stored","session":""}`, want: SignedOut},
		"an entry with no token":           {row: &signedIn, entry: `{"v":1,"token":"","session":"s1"}`, want: SignedOut},
		"an entry of another version":      {row: &signedIn, entry: `{"v":2,"token":"refresh-stored","session":"s1"}`, want: SignedOut},
		"an entry that is no object":       {row: &signedIn, entry: `refresh-stored`, want: SignedOut},
		"an entry and no client id":        {row: &registration{Data: hostData("h"), SessionID: "s1"}, entry: `{"v":1,"token":"refresh-stored","session":"s1"}`, want: SignedOut},
		"an entry and no row":              {entry: `{"v":1,"token":"refresh-stored","session":"s1"}`, want: SignedOut},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if c.row != nil {
				r.seed(*c.row, "", "")
			}
			if c.entry != "" {
				r.keyring.entries[refreshAccount] = c.entry
			}

			s := r.restart()

			assert.Equal(t, c.want, s.State().Phase)
			assert.Equal(t, c.wantSession, savedRegistration(t, r).SessionID)
			_, ok := storedRefresh(t, r)
			assert.Equal(t, c.wantEntry, ok)
			assert.Empty(t, r.issuer.forms("refresh_token"), "New reaches no network")
		})
	}
}

// A restored session shows the account it was signed in as, and that it is
// registered.
func TestARestoredSessionShowsItsAccount(t *testing.T) {
	r := newRig(t)
	r.seed(registration{Data: hostData("h"), ClientID: "c", Email: "ada@example.com", SessionID: "s1"}, "refresh-stored", "s1")

	st := r.restart().State()

	assert.Equal(t, "ada@example.com", st.Email)
	assert.True(t, st.Registered)
}

// A refresh token a refresh rotates is saved under its sign-in's name, so the
// next launch restores the session with it.
func TestARotatedTokenStaysUnderItsSession(t *testing.T) {
	r := newRig(t)
	r.issuer.set(func(f *fakeIssuer) { f.accessTTL = 0 })
	connect(t, r.svc)
	first, _ := storedRefresh(t, r)
	_, err := signedToken(t, r.svc) // the access token is spent: this refreshes
	require.NoError(t, err)
	rotated, _ := storedRefresh(t, r)
	require.NotEqual(t, first, rotated)

	s := r.restart()

	assert.Equal(t, Connected, s.State().Phase)
	_, err = signedToken(t, s)
	assert.NoError(t, err)
}

// A row New cannot read leaves the launch signed out and the entry in place
// for a launch that can.
func TestAnUnreadableRowLeavesTheEntry(t *testing.T) {
	r := newRig(t)
	_, err := r.db.Write.ExecContext(t.Context(), `INSERT INTO oauth_connections
		(provider, client_id, session_id, provider_data, created_at, updated_at) VALUES ('acme', 'c', 's1', '{', 0, 0)`)
	require.NoError(t, err)
	require.NoError(t, saveEntry(r.keyring, refreshAccount, "s1", "refresh-stored"))

	s := r.restart()

	assert.Equal(t, SignedOut, s.State().Phase)
	_, ok := storedRefresh(t, r)
	assert.True(t, ok)
}

// A keyring New cannot read is no proof of a sign-out: nothing is written.
func TestAnUnreadableKeyringWritesNothing(t *testing.T) {
	r := newRig(t)
	r.seed(registration{Data: hostData("h"), ClientID: "c", SessionID: "s1"}, "", "")
	r.keyring.getErr = errBoom

	s := r.restart()

	assert.Equal(t, SignedOut, s.State().Phase)
	assert.Equal(t, "s1", savedRegistration(t, r).SessionID)
}

// A database the statements cannot be prepared on fails New.
func TestNewFailsOnAClosedDatabase(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = New(Config{Provider: testProvider(""), DB: db})

	assert.Error(t, err)
}

// No token leaves the keyring: after a sign-in, a refresh and a refresh the
// issuer refused, no token is in a log line or in any file under the data
// directory, app.db included. The log is read raw, before redaction: the service logs none.
func TestNoTokenLeavesTheKeyring(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := newRig(t)
	r.issuer.set(func(f *fakeIssuer) { f.accessTTL = 0 })
	connect(t, r.svc)
	_, err := signedToken(t, r.svc) // the access token is spent: this refreshes
	require.NoError(t, err)
	r.issuer.set(func(f *fakeIssuer) { f.refreshCode = "invalid_grant" })
	_, err = signedToken(t, r.svc)
	require.Error(t, err)

	tokens := r.issuer.mintedTokens()
	require.NotEmpty(t, tokens)
	held, ok := storedRefresh(t, r)
	if ok {
		assert.True(t, safe.HasSecret(held), "a held token is registered with the redactor")
	}
	require.NoError(t, filepath.WalkDir(r.dir, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, tok := range tokens {
			assert.NotContains(t, string(data), tok, path)
		}
		return nil
	}))
	for _, tok := range tokens {
		assert.False(t, strings.Contains(logs.String(), tok), "a log line holds a token")
	}
}

// A stale entry New cannot delete is left for the next launch, signed out.
func TestAStaleEntryNewCannotDelete(t *testing.T) {
	r := newRig(t)
	r.keyring.entries[refreshAccount] = "refresh-stored"
	r.keyring.failDelete(refreshAccount, errBoom)

	s := r.restart()

	assert.Equal(t, SignedOut, s.State().Phase)
	_, ok := storedRefresh(t, r)
	assert.True(t, ok)
}
