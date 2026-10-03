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

package chatsvc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// seedGrant writes a chat_grants row holding rule as given.
func seedGrant(t *testing.T, db *appdb.DB, chatID ChatID, rule string) {
	t.Helper()
	_, err := db.Write.Exec(`INSERT INTO chat_grants (id, chat_id, rule, created_at) VALUES (?, ?, ?, ?)`,
		appdb.NewID(), string(chatID), rule, time.Now().UnixMilli())
	require.NoError(t, err)
}

func TestAChatsGrantsGoWithTheChat(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	other := seedChat(t, s.db, aChat("1", time.Now()))
	assert.Empty(t, s.grantsFor(t.Context(), c.ID))

	allow := permissions.Rule{ID: "a", Effect: permissions.Allow, Class: permissions.UpstreamWrite}
	seedGrant(t, s.db, c.ID, `{"id":"a","effect":"allow","class":4}`)
	assert.Equal(t, []permissions.Rule{allow}, s.grantsFor(t.Context(), c.ID), "a row is read live")
	assert.Empty(t, s.grantsFor(t.Context(), other.ID), "another chat's rows are its own")

	seedGrant(t, s.db, c.ID, `{"id":`)
	assert.Equal(t, []permissions.Rule{allow, permissions.Refused}, s.grantsFor(t.Context(), c.ID),
		"a row that does not decode refuses the chat's cluster writes")

	_, err := s.db.Write.Exec(`DELETE FROM chats WHERE id = ?`, string(c.ID))
	require.NoError(t, err)
	var n int
	require.NoError(t, s.db.Read.QueryRow(`SELECT COUNT(*) FROM chat_grants`).Scan(&n))
	assert.Zero(t, n, "the rows go with the chat")
}

// A key Rule does not name may be a narrowing field misspelled, so the row
// refuses rather than reads as a wider grant.
func TestAGrantWithAnUnknownKeyRefuses(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	seedGrant(t, s.db, c.ID, `{"id":"a","effect":"allow","class":4,"namepsace":"team-a"}`)

	assert.Equal(t, []permissions.Rule{permissions.Refused}, s.grantsFor(t.Context(), c.ID))
}

func TestTheSessionCarriesTheContextsMode(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	sess := s.sessionFor(c.ID, false)

	assert.Equal(t, permissions.Ask, sess.Policy(t.Context(), "dev").Mode)
	require.NoError(t, s.security.SetDefaultMode(permissions.Auto))
	assert.Equal(t, permissions.Auto, sess.Policy(t.Context(), "dev").Mode, "a mode changed in the file reaches the next write")
	require.NoError(t, s.security.SetMode("prod-eu", permissions.ReadOnly))
	assert.Equal(t, permissions.ReadOnly, sess.Policy(t.Context(), "prod-eu").Mode)
	assert.Empty(t, sess.Policy(t.Context(), "dev").Rules)

	allow := permissions.Rule{ID: "a", Effect: permissions.Allow, Class: permissions.UpstreamWrite}
	require.NoError(t, s.security.AddRule(allow))
	seedGrant(t, s.db, c.ID, `{"id":"g","effect":"deny","class":4}`)
	ids := []string{}
	for _, r := range sess.Policy(t.Context(), "dev").Rules {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"g", "a"}, ids, "the chat's grants, then the always rules")
}

// A read of the grants that fails may have hidden a Deny, so it refuses the
// chat's cluster writes.
func TestAGrantsReadThatFailsRefuses(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	_, err := s.db.Write.Exec(`DROP TABLE chat_grants`)
	require.NoError(t, err)

	assert.Equal(t, []permissions.Rule{permissions.Refused}, s.grantsFor(t.Context(), c.ID))
}
