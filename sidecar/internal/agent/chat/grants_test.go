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

package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
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
	sess := s.sessionFor(c.ID, false, false)

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
// chat's cluster writes, and a grant that cannot read them is not written.
func TestAGrantsReadThatFailsRefuses(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	_, err := s.db.Write.Exec(`DROP TABLE chat_grants`)
	require.NoError(t, err)

	assert.Equal(t, []permissions.Rule{permissions.Refused}, s.grantsFor(t.Context(), c.ID))
	_, err = s.addGrant(t.Context(), c.ID, folderRule(home, false))
	assert.Error(t, err)
	assert.Error(t, s.GrantFolder(t.Context(), c.ID, home, false))
}

// A grant's write or removal that fails reaches the caller.
func TestAGrantsWriteThatFailsRefuses(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	rule := permissions.Rule{Effect: permissions.Allow, Class: permissions.UpstreamWrite, Context: "dev"}
	added, err := s.addGrant(t.Context(), c.ID, rule)
	require.NoError(t, err)
	for _, kind := range []string{"INSERT", "DELETE"} {
		_, err := s.db.Write.Exec(`CREATE TRIGGER refuse_` + kind + ` BEFORE ` + kind + ` ON chat_grants
			BEGIN SELECT RAISE(ABORT, 'refused'); END`)
		require.NoError(t, err)
	}

	rule.Context = "prod"
	_, err = s.addGrant(t.Context(), c.ID, rule)
	assert.ErrorContains(t, err, "refused")
	_, err = s.RemoveChatGrant(t.Context(), c.ID, added.ID)
	assert.ErrorContains(t, err, "refused")
	assert.Equal(t, []permissions.Rule{added}, s.grantsFor(t.Context(), c.ID))
}

// A chat's rules are written, listed, changed in place and removed by id, and
// the command's next decision reads each change.
func TestAChatsGrantsAreListedAndRemoved(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	c := seedChat(t, s.db, aChat("1", time.Now()))
	rule := permissions.Rule{Effect: permissions.Allow, Class: permissions.UpstreamWrite, Context: "dev", Namespace: "web"}

	added, err := s.addGrant(ctx, c.ID, rule)
	require.NoError(t, err)
	require.NotEmpty(t, added.ID)
	var rowID string
	require.NoError(t, s.db.Read.QueryRow(`SELECT id FROM chat_grants`).Scan(&rowID))
	assert.Equal(t, rowID, added.ID, "the row's id is the rule's")
	assert.Equal(t, []permissions.Rule{added}, s.grantsFor(ctx, c.ID))

	again, err := s.addGrant(ctx, c.ID, rule)
	require.NoError(t, err)
	assert.Equal(t, added, again, "a rule the chat holds is not written twice")
	assert.Len(t, s.grantsFor(ctx, c.ID), 1)

	changed := added
	changed.Namespace = "api"
	_, err = s.addGrant(ctx, c.ID, changed)
	require.NoError(t, err)
	assert.Equal(t, []permissions.Rule{changed}, s.grantsFor(ctx, c.ID), "a rule under its id replaces that row's")
	stranger := changed
	stranger.ID = appdb.NewID()
	_, err = s.addGrant(ctx, c.ID, stranger)
	assert.ErrorIs(t, err, ErrGrantGone)

	left, err := s.RemoveChatGrant(ctx, c.ID, changed.ID)
	require.NoError(t, err)
	assert.Empty(t, left)
	assert.Empty(t, s.grantsFor(ctx, c.ID), "the command's next decision no longer finds it")
	_, err = s.RemoveChatGrant(ctx, c.ID, changed.ID)
	assert.ErrorIs(t, err, ErrGrantGone)

	_, err = s.db.Write.Exec(`DELETE FROM chats WHERE id = ?`, string(c.ID))
	require.NoError(t, err)
	_, err = s.RemoveChatGrant(ctx, c.ID, appdb.NewID())
	assert.ErrorIs(t, err, ErrChatGone)
	_, err = s.addGrant(ctx, c.ID, rule)
	assert.ErrorIs(t, err, ErrChatGone)
}

// The chat's network switch is read at each command, so one turned on or off
// mid-turn reaches the next command; a read that fails, or a chat that is gone,
// gives no network.
func TestTheSwitchIsReadLive(t *testing.T) {
	s := newTestService(t)
	s.sandboxStatus = sandbox.Status{Available: true, NetworkAvailable: true}
	c := seedChat(t, s.db, aChat("1", time.Now()))
	sess := s.sessionFor(c.ID, false, false)
	assert.Empty(t, sess.Network(t.Context()))

	_, err := s.SetNetworkEnabled(t.Context(), c.ID, true)
	require.NoError(t, err)
	assert.Equal(t, session.NetworkChat, sess.Network(t.Context()))
	assert.Equal(t, session.NetworkChat, s.sessionFor(c.ID, false, true).Network(t.Context()), "the switch names itself over the toggle")

	failed, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Empty(t, sess.Network(failed))
	assert.Empty(t, s.sessionFor(ChatID(appdb.NewID()), false, false).Network(t.Context()))

	_, err = s.SetNetworkEnabled(t.Context(), c.ID, false)
	require.NoError(t, err)
	assert.Empty(t, sess.Network(t.Context()))
	assert.Equal(t, session.NetworkTurn, s.sessionFor(c.ID, false, true).Network(t.Context()))
}

// folderRule is an always or chat grant of path.
func folderRule(path string, write bool) permissions.Rule {
	class := permissions.ReadInside
	if write {
		class = permissions.WriteInside
	}
	return permissions.Rule{Effect: permissions.Allow, Class: class, Folder: path}
}

func TestFoldersForNoChatIsTheAlwaysOnes(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	code, svc := filepath.Join(home, "code"), filepath.Join(home, "code", "svc")
	always := folderRule(svc, true)
	always.ID = "always"
	require.NoError(t, s.security.AddRule(always))
	_, err := s.addGrant(t.Context(), c.ID, folderRule(code, false))
	require.NoError(t, err)

	assert.Equal(t, []session.Folder{{Path: svc, Write: true}}, s.FoldersFor(t.Context(), ""), "the always folders alone")
	assert.Equal(t, []session.Folder{{Path: code}, {Path: svc, Write: true}}, s.FoldersFor(t.Context(), c.ID),
		"the chat's, then the always ones")
	assert.Equal(t, s.FoldersFor(t.Context(), c.ID), s.sessionFor(c.ID, false, false).GrantedFolders(t.Context()))
}

func TestNoFolderAppliesWithoutASandbox(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	always := folderRule(home, false)
	always.ID = "home"
	require.NoError(t, s.security.AddRule(always))
	require.NotEmpty(t, s.sessionFor(c.ID, false, false).GrantedFolders(t.Context()))

	assert.Empty(t, s.sessionFor(c.ID, true, false).GrantedFolders(t.Context()), "a chat switched outside the sandbox gets none")
	s.sandboxStatus = sandbox.Status{Reason: "no bwrap"}
	assert.Empty(t, s.FoldersFor(t.Context(), c.ID))
	assert.Empty(t, s.sessionFor(c.ID, false, false).GrantedFolders(t.Context()))
	listed, chat := s.FolderGrants(t.Context(), c.ID)
	assert.Empty(t, listed, "nor is one listed")
	assert.Empty(t, chat)
}

func TestFoldersForAnswersNoneWithNoSnapshot(t *testing.T) {
	s := newTestService(t)
	s.sandboxStatus = sandbox.Status{Available: true}
	dir := testutil.GrantableDir(t)
	always := folderRule(dir, false)
	always.ID = "a"
	require.NoError(t, s.security.AddRule(always))
	assert.Empty(t, s.FoldersFor(t.Context(), ""), "a service with no zones checks no folder")
}

func TestAChatsFoldersJoinTheAlwaysOnes(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	code, svc := filepath.Join(home, "code"), filepath.Join(home, "code", "svc")
	sess := s.sessionFor(c.ID, false, false)

	require.NoError(t, s.GrantFolder(t.Context(), c.ID, code, false))
	assert.Equal(t, []session.Folder{{Path: code}}, sess.GrantedFolders(t.Context()), "a chat's grant is read live")
	require.NoError(t, s.GrantFolder(t.Context(), "", svc, true))
	assert.Equal(t, []session.Folder{{Path: code}, {Path: svc, Write: true}}, sess.GrantedFolders(t.Context()))

	always, chat := s.FolderGrants(t.Context(), c.ID)
	require.Len(t, always, 1)
	require.Len(t, chat, 1)
	assert.Equal(t, FolderGrant{ID: always[0].ID, Path: svc, Write: true}, always[0])
	assert.Equal(t, FolderGrant{ID: chat[0].ID, Path: code}, chat[0])

	assert.ErrorIs(t, s.RevokeFolder(chat[0].ID), ErrGrantGone, "a chat's grant is removed through the chat")
	require.NoError(t, s.RevokeFolder(always[0].ID))
	assert.Equal(t, []session.Folder{{Path: code}}, sess.GrantedFolders(t.Context()))
	assert.ErrorIs(t, s.RevokeFolder(always[0].ID), ErrGrantGone)

	rules, err := s.ChatGrants(t.Context(), c.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, permissions.Rule{ID: rules[0].ID, Effect: permissions.Allow, Class: permissions.ReadInside, Folder: code}, rules[0])
	rules, err = s.RemoveChatGrant(t.Context(), c.ID, chat[0].ID)
	require.NoError(t, err)
	assert.Empty(t, rules)
	assert.Empty(t, sess.GrantedFolders(t.Context()))

	cluster := permissions.Rule{ID: "c", Effect: permissions.Allow, Class: permissions.UpstreamWrite}
	require.NoError(t, s.security.AddRule(cluster))
	assert.ErrorIs(t, s.RevokeFolder("c"), ErrGrantGone, "a rule that is not a folder's")
	always, _ = s.FolderGrants(t.Context(), c.ID)
	assert.Empty(t, always, "nor is it listed among them")
}

func TestAGrantInTheSamePlaceChangesItsMode(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	svc := filepath.Join(home, "code", "svc")

	// The chat's grants first, then the always ones, which "" names.
	for _, grantee := range []ChatID{c.ID, ""} {
		require.NoError(t, s.GrantFolder(t.Context(), grantee, svc, false))
		beforeAlways, beforeChat := s.FolderGrants(t.Context(), c.ID)
		require.NoError(t, s.GrantFolder(t.Context(), grantee, svc, true))
		afterAlways, afterChat := s.FolderGrants(t.Context(), c.ID)
		before, after := beforeChat, afterChat
		if grantee == "" {
			before, after = beforeAlways, afterAlways
		}
		require.Len(t, after, 1, "%q: one rule", grantee)
		assert.Equal(t, before[0].ID, after[0].ID, "%q: the first id", grantee)
		assert.True(t, after[0].Write, "%q: read-write", grantee)
	}
	always, chat := s.FolderGrants(t.Context(), c.ID)
	assert.Len(t, always, 1, "a chat grant beside an always one on the same folder")
	assert.Len(t, chat, 1)
}

func TestAGrantIsCheckedWhenWritten(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))

	var r securityconfig.FolderRefusal
	require.ErrorAs(t, s.GrantFolder(t.Context(), c.ID, home, true), &r)
	assert.Equal(t, "home", r.Rule)
	require.ErrorAs(t, s.GrantFolder(t.Context(), "", filepath.Join(home, ".ssh"), false), &r)
	assert.Equal(t, "never", r.Rule)
	gone := ChatID(appdb.NewID())
	assert.ErrorIs(t, s.GrantFolder(t.Context(), gone, home, false), ErrChatGone)

	always, chat := s.FolderGrants(t.Context(), c.ID)
	assert.Empty(t, always)
	assert.Empty(t, chat)

	// And again when listed: a row the check would refuse is listed refused.
	stored := folderRule(home, true)
	stored.ID = "stored"
	b, err := json.Marshal(stored)
	require.NoError(t, err)
	seedGrant(t, s.db, c.ID, string(b))
	_, chat = s.FolderGrants(t.Context(), c.ID)
	require.Len(t, chat, 1)
	assert.NotEmpty(t, chat[0].Refused)
}

func TestAGrantGoesWithTheChat(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	svc := filepath.Join(home, "code", "svc")

	require.NoError(t, s.GrantFolder(t.Context(), c.ID, svc, true))
	assert.Equal(t, []session.Folder{{Path: svc, Write: true}}, s.sessionFor(c.ID, false, false).GrantedFolders(t.Context()),
		"the chat's next command reads it")

	_, err := s.db.Write.Exec(`DELETE FROM chats WHERE id = ?`, string(c.ID))
	require.NoError(t, err)
	var n int
	require.NoError(t, s.db.Read.QueryRow(`SELECT COUNT(*) FROM chat_grants`).Scan(&n))
	assert.Zero(t, n)
}

// While the rules field is held, Rules holds no Allow, so no always grant
// reaches a run; Settings lists each as refused for that reason rather than as
// a folder commands can read, and none can be revoked until the hold ends.
func TestAHeldRulesFieldRefusesEveryAlwaysGrant(t *testing.T) {
	s := newTestService(t)
	home := grantableWith(t, s, `{"rules": [{"id": "r", "effect": "allow", "class": 1, "folder": "<home>/code"},
		{"id": "b", "effect": "deny", "class": 9}]}`)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	require.True(t, s.security.Held(securityconfig.FieldRules))

	assert.Empty(t, s.FoldersFor(t.Context(), c.ID))
	always, _ := s.FolderGrants(t.Context(), c.ID)
	require.Len(t, always, 1, "the grant stays listed, with its reason")
	assert.Equal(t, filepath.Join(home, "code"), always[0].Path)
	assert.Equal(t, RulesHeldReason, always[0].Refused)
	assert.ErrorIs(t, s.RevokeFolder("r"), securityconfig.ErrHeld)
}

// Two grants of one folder at once, in one place, leave one rule: the lookup
// and the write are one transaction for a chat's, one update for an always
// one.
func TestConcurrentGrantsOfOneFolderLeaveOneRule(t *testing.T) {
	s := newTestService(t)
	home := grantable(t, s)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	svc := filepath.Join(home, "code", "svc")
	for _, grantee := range []ChatID{c.ID, ""} {
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() { assert.NoError(t, s.GrantFolder(t.Context(), grantee, svc, i%2 == 1)) })
		}
		wg.Wait()
	}
	always, chat := s.FolderGrants(t.Context(), c.ID)
	assert.Len(t, always, 1)
	assert.Len(t, chat, 1)
}

// A chat's session asks and reads Secret data under its rules, and so does a
// subagent's under it: only a monitor's session is built with either flag.
func TestAChatsSessionAsksAndReadsSecretData(t *testing.T) {
	s := newTestService(t)
	c := seedChat(t, s.db, aChat("1", time.Now()))
	for _, sess := range []session.Session{s.sessionFor(c.ID, false, false), session.Narrow(s.sessionFor(c.ID, false, false))} {
		assert.False(t, sess.NoPrompts)
		assert.False(t, sess.NoSecretData)
	}
}

// The monitor's session holds less than any chat's: no switch, no policy (the
// proxy reads read-only), no network, no folders, no prompts and no Secret
// data. Under the policy the proxy builds from it, every write, every Secret
// read and every action a rule would ask about is refused.
func TestTheMonitorSessionIsReadOnlyAndAsksNobody(t *testing.T) {
	sess := monitorSession()

	assert.Equal(t, session.Monitor, sess.Kind)
	assert.False(t, sess.Outside)
	assert.Nil(t, sess.Policy)
	assert.Nil(t, sess.Network)
	assert.Nil(t, sess.Folders)
	assert.Empty(t, sess.GrantedFolders(t.Context()))
	assert.True(t, sess.NoPrompts)
	assert.True(t, sess.NoSecretData)

	asks := permissions.Rule{Effect: permissions.AskFor, Class: permissions.ReadInside}
	policy := permissions.Policy{Mode: permissions.ReadOnly, Rules: []permissions.Rule{asks}, NoPrompts: sess.NoPrompts, NoSecretData: sess.NoSecretData}
	for _, class := range []permissions.Class{permissions.UpstreamWrite, permissions.Destructive, permissions.SecretRead, permissions.ReadInside} {
		got, why := policy.Authorize(permissions.Action{Class: class, Verb: "patch", Group: "apps", Kind: "deployments"})
		assert.Equal(t, permissions.Refuse, got, "class %d: %s", class, why)
	}
}
