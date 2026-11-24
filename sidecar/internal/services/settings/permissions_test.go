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

package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amorey/gobus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// openFile is a store over body, "" for no file, and a fresh app.db.
func openFile(t *testing.T, body string) (*Store, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "settings.json")
	if body != "" {
		require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
	}
	return openStore(t, file, appdbtest.Open(t)), file
}

// openStore is a store over file and db, closed when the test ends.
func openStore(t *testing.T, file string, db *appdb.DB, opts ...Option) *Store {
	t.Helper()
	s, err := Open(file, db, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

// exec runs q on db's writer.
func exec(t *testing.T, db *appdb.DB, q string, args ...any) {
	t.Helper()
	_, err := db.Write.Exec(q, args...)
	require.NoError(t, err)
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	return string(data)
}

// seedCluster writes a clusters row: from the kubeconfig, under context, or a
// cloud cluster with none when context is "".
func seedCluster(t *testing.T, db *appdb.DB, id apimeta.ClusterID, context string) {
	t.Helper()
	source, key := "kubeconfig", any(context)
	if context == "" {
		source, key = "cloud", string(id)
	}
	exec(t, db, `INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, id, source, key)
}

// storeWithClusters is a store over a fresh app.db holding the clusters dev,
// prod and a cloud cluster with no kube-context.
func storeWithClusters(t *testing.T) (*Store, *appdb.DB) {
	t.Helper()
	db := appdbtest.Open(t)
	seedCluster(t, db, "dev", "dev-eks")
	seedCluster(t, db, "prod", "prod-eu")
	seedCluster(t, db, "cloud", "")
	return openStore(t, filepath.Join(t.TempDir(), "settings.json"), db), db
}

func modeOf(t *testing.T, s *Store, cluster apimeta.ClusterID) ClusterModeState {
	t.Helper()
	st, err := s.ModeFor(t.Context(), cluster)
	require.NoError(t, err)
	return st
}

// A cluster's mode is its own row, else the default row, else ask.
func TestAClusterWithNoModeOfItsOwnTakesTheDefault(t *testing.T) {
	t.Parallel()
	s, _ := storeWithClusters(t)
	ctx := t.Context()

	assert.Equal(t, ClusterModeState{ClusterID: "dev", Mode: permissions.Ask, Source: SourceDefault}, modeOf(t, s, "dev"))
	mode, _, err := s.ClusterModes(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, permissions.Ask, mode)

	require.NoError(t, s.SetDefaultMode(ctx, permissions.Auto))
	require.NoError(t, s.SetDefaultMode(ctx, permissions.Auto), "a second write keeps one default")
	assert.Equal(t, ClusterModeState{ClusterID: "dev", Mode: permissions.Auto, Source: SourceDefault}, modeOf(t, s, "dev"))
	assert.Equal(t, ClusterModeState{ClusterID: "cloud", Mode: permissions.Auto, Source: SourceDefault}, modeOf(t, s, "cloud"),
		"a cluster with no kube-context takes the default")

	require.NoError(t, s.SetMode(ctx, "prod", permissions.ReadOnly))
	require.NoError(t, s.SetMode(ctx, "prod", permissions.Ask), "a second write changes the cluster's one row")
	assert.Equal(t, ClusterModeState{ClusterID: "prod", Mode: permissions.Ask, Source: SourceEntry}, modeOf(t, s, "prod"))
	assert.Equal(t, permissions.Auto, modeOf(t, s, "dev").Mode, "another cluster keeps the default")

	require.NoError(t, s.ClearMode(ctx, "prod"))
	assert.Equal(t, ClusterModeState{ClusterID: "prod", Mode: permissions.Auto, Source: SourceDefault}, modeOf(t, s, "prod"))
	require.NoError(t, s.ClearMode(ctx, "prod"), "clearing a mode the cluster does not have")
}

// A mode belongs to a cluster Settings lists: one with a kube-context that is
// not awaiting deletion.
func TestAModeIsRefusedForAClusterSettingsDoesNotList(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	ctx := t.Context()
	exec(t, db, `UPDATE clusters SET delete_requested_at = 1 WHERE id = 'prod'`)

	assert.ErrorIs(t, s.SetMode(ctx, "gone", permissions.Auto), ErrClusterGone)
	assert.ErrorIs(t, s.SetMode(ctx, "prod", permissions.Auto), ErrClusterGone, "a cluster awaiting deletion")
	assert.ErrorIs(t, s.SetMode(ctx, "cloud", permissions.Auto), ErrNoKubeContext)
	assert.ErrorIs(t, s.ClearMode(ctx, "gone"), ErrClusterGone)
	assert.ErrorIs(t, s.ClearMode(ctx, "prod"), ErrClusterGone)
	var n int
	require.NoError(t, db.Read.QueryRow(`SELECT count(*) FROM approval_modes`).Scan(&n))
	assert.Zero(t, n)
}

// A read of the modes that fails is an error, which a decision reads as
// read-only.
func TestAModesReadThatFailsIsAnError(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	exec(t, db, `DROP TABLE approval_modes`)

	_, err := s.ModeFor(t.Context(), "dev")
	assert.Error(t, err)
	_, _, err = s.ClusterModes(t.Context(), []apimeta.ClusterID{"dev"})
	assert.Error(t, err)
}

// Settings' list reads every mode once, and resolves each cluster as ModeFor
// does: its own row, else the one default.
func TestClusterModesResolveEachClusterFromOneRead(t *testing.T) {
	t.Parallel()
	s, _ := storeWithClusters(t)
	ctx := t.Context()
	require.NoError(t, s.SetDefaultMode(ctx, permissions.Auto))
	require.NoError(t, s.SetMode(ctx, "prod", permissions.ReadOnly))

	mode, states, err := s.ClusterModes(ctx, []apimeta.ClusterID{"dev", "prod"})
	require.NoError(t, err)
	assert.Equal(t, permissions.Auto, mode)
	assert.Equal(t, []ClusterModeState{modeOf(t, s, "dev"), modeOf(t, s, "prod")}, states)
	assert.Equal(t, ClusterModeState{ClusterID: "prod", Mode: permissions.ReadOnly, Source: SourceEntry}, states[1])
}

func kubeRules(t *testing.T, s *Store) []permissions.Rule {
	t.Helper()
	rules, err := s.KubeRules(t.Context())
	require.NoError(t, err)
	return rules
}

// The always rules are added in order, every field kept, a rule already held
// is not written twice, and one is removed by id.
func TestAlwaysRulesAreAddedListedAndRemoved(t *testing.T) {
	t.Parallel()
	s, _ := storeWithClusters(t)
	ctx := t.Context()
	assert.Empty(t, kubeRules(t, s))

	deny := permissions.Rule{Effect: permissions.Deny, Class: permissions.Destructive, ClusterID: "prod", Namespace: "team-a",
		Verb: "delete", Group: "apps", Kind: "deployments", Inside: true}
	allow := permissions.Rule{ID: "mine", Effect: permissions.Allow, Class: permissions.SecretRead, ClusterID: "dev", Verb: "get"}
	added, err := s.AddKubeRule(ctx, deny)
	require.NoError(t, err)
	require.NotEmpty(t, added.ID, "a rule with no id gets one")
	deny.ID = added.ID
	_, err = s.AddKubeRule(ctx, allow)
	require.NoError(t, err)
	assert.Equal(t, []permissions.Rule{deny, allow}, kubeRules(t, s))

	again := deny
	again.ID = ""
	held, err := s.AddKubeRule(ctx, again)
	require.NoError(t, err)
	assert.Equal(t, deny, held, "the rule already held answers")
	assert.Len(t, kubeRules(t, s), 2)

	require.NoError(t, s.RemoveKubeRule(ctx, deny.ID))
	assert.Equal(t, []permissions.Rule{allow}, kubeRules(t, s))
	assert.ErrorIs(t, s.RemoveKubeRule(ctx, deny.ID), ErrNoRule)
}

// A rule belongs to a cluster Settings lists, as a mode does, and goes with
// its cluster.
func TestARuleNamesAClusterSettingsListsAndGoesWithIt(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	ctx := t.Context()
	allow := func(cluster apimeta.ClusterID) permissions.Rule {
		return permissions.Rule{Effect: permissions.Allow, Class: permissions.UpstreamWrite, ClusterID: cluster}
	}
	_, err := s.AddKubeRule(ctx, allow("gone"))
	assert.ErrorIs(t, err, ErrClusterGone)
	_, err = s.AddKubeRule(ctx, allow("cloud"))
	assert.ErrorIs(t, err, ErrNoKubeContext)
	exec(t, db, `UPDATE clusters SET delete_requested_at = 1 WHERE id = 'prod'`)
	_, err = s.AddKubeRule(ctx, allow("prod"))
	assert.ErrorIs(t, err, ErrClusterGone, "a cluster awaiting deletion")
	assert.Empty(t, kubeRules(t, s))

	_, err = s.AddKubeRule(ctx, allow("dev"))
	require.NoError(t, err)
	exec(t, db, `DELETE FROM clusters WHERE id = 'dev'`)
	assert.Empty(t, kubeRules(t, s), "a deleted cluster's rules go with it")
}

// badRules are rules Kstack cannot apply, each with the reason a write is
// refused with.
var badRules = map[string]struct {
	rule   permissions.Rule
	reason string
}{
	"the refused id": {permissions.Rule{ID: "refused", Effect: permissions.Deny, Class: permissions.UpstreamWrite},
		"has the id Kstack keeps for its own rule"},
	"the refused-secrets id": {permissions.Rule{ID: "refused-secrets", Effect: permissions.Deny, Class: permissions.SecretRead},
		"has the id Kstack keeps for its own rule"},
	"an unknown effect": {permissions.Rule{ID: "b", Effect: "permit", Class: permissions.UpstreamWrite},
		"has an effect other than allow, deny or ask"},
	"a folder class": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.ReadInside},
		"names a class no rule decides: only 4 (cluster writes), 5 (destructive cluster writes) and 6 (Secret reads)"},
	"no class": {permissions.Rule{ID: "b", Effect: permissions.Deny},
		"names a class no rule decides: only 4 (cluster writes), 5 (destructive cluster writes) and 6 (Secret reads)"},
	"an allow of a destructive write": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.Destructive},
		"allows a destructive write, which always asks"},
	"a Secret read inside": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Namespace: "team-a", Inside: true},
		"is a Secret read rule marked inside, which only a cluster write rule may be"},
	"a cluster-scoped Secret read": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Namespace: permissions.ClusterScope},
		"is a Secret read rule naming [cluster], though every Secret is in a namespace"},
	"a Secret read's write verb": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Verb: "delete"},
		"is a Secret read rule naming a verb other than get, list or watch"},
	"a Secret read's other group": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Group: "apps"},
		"is a Secret read rule naming a group other than core"},
	"a Secret read's other kind": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Kind: "configmaps"},
		"is a Secret read rule naming a resource other than secrets"},
	"an allow on every cluster": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.UpstreamWrite, Namespace: "team-a"},
		"allows on every cluster, which only a deny or ask rule may"},
	"a Secret read's allow on every cluster": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead},
		"allows on every cluster, which only a deny or ask rule may"},
}

// A rule Kstack cannot apply is refused in the user's words, and nothing is
// written.
func TestARuleKstackCannotApplyIsRefused(t *testing.T) {
	t.Parallel()
	s, _ := storeWithClusters(t)
	for name, c := range badRules {
		_, err := s.AddKubeRule(t.Context(), c.rule)
		var r RuleRefusal
		require.ErrorAs(t, err, &r, name)
		assert.Equal(t, c.reason, string(r), name)
	}
	assert.Empty(t, kubeRules(t, s))
}

// The Go check and the table's CHECKs refuse the same rules, so no rule
// passes one and fails the other.
func TestTheGoCheckAndTheTableRefuseTheSameRules(t *testing.T) {
	t.Parallel()
	s, _ := storeWithClusters(t)
	rules := []permissions.Rule{
		{ID: "a", Effect: permissions.Allow, Class: permissions.UpstreamWrite, ClusterID: "dev", Namespace: permissions.ClusterScope, Inside: true},
		{ID: "b", Effect: permissions.AskFor, Class: permissions.Destructive, Verb: "delete"},
		{ID: "c", Effect: permissions.Deny, Class: permissions.SecretRead, Namespace: "team-a", Verb: "list", Group: "core", Kind: "secrets"},
		{ID: "d", Effect: permissions.Allow, Class: permissions.SecretRead, ClusterID: "dev", Verb: "watch"},
	}
	for _, c := range badRules {
		rules = append(rules, c.rule)
	}
	for i, r := range rules {
		// Each row its own id, but for the two the table refuses.
		if r.ID != permissions.Refused.ID && r.ID != permissions.RefusedSecrets.ID {
			r.ID = appdb.NewID()
		}
		err := insertKubeRule(t.Context(), s.stmts.Stmts(), r, int64(i))
		assert.Equal(t, ruleRefusal(r) != "", err != nil, "%+v: the Go check says %q, the table %v", r, ruleRefusal(r), err)
	}
}

// A read of the rules that fails is an error, which a decision reads as
// permissions.Unread.
func TestARulesReadThatFailsIsAnError(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	exec(t, db, `DROP TABLE kube_rules`)
	_, err := s.KubeRules(t.Context())
	assert.Error(t, err)
	_, err = s.AddKubeRule(t.Context(), permissions.Rule{Effect: permissions.Deny, Class: permissions.UpstreamWrite})
	assert.Error(t, err)
}

func folderGrants(t *testing.T, s *Store) []FolderGrant {
	t.Helper()
	grants, err := s.FolderGrants(t.Context())
	require.NoError(t, err)
	return grants
}

// An always grant of a folder granted already keeps its id and changes its
// access, and is revoked by id.
func TestAlwaysFolderGrantsAreGrantedChangedAndRevoked(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	ctx := t.Context()

	require.NoError(t, s.GrantFolder(ctx, "/srv/code", false))
	require.NoError(t, s.GrantFolder(ctx, "/srv/data", true))
	grants := folderGrants(t, s)
	require.Len(t, grants, 2)
	assert.Equal(t, FolderGrant{ID: grants[0].ID, Path: "/srv/code"}, grants[0])
	assert.Equal(t, FolderGrant{ID: grants[1].ID, Path: "/srv/data", Write: true}, grants[1])

	require.NoError(t, s.GrantFolder(ctx, "/srv/code", true))
	again := folderGrants(t, s)
	assert.Equal(t, FolderGrant{ID: grants[0].ID, Path: "/srv/code", Write: true}, again[0], "the first id, read-write")

	require.NoError(t, s.RevokeFolder(ctx, grants[1].ID))
	assert.Len(t, folderGrants(t, s), 1)
	assert.ErrorIs(t, s.RevokeFolder(ctx, grants[1].ID), ErrNoGrant)

	exec(t, db, `DROP TABLE folder_grants`)
	_, err := s.FolderGrants(ctx)
	assert.Error(t, err, "a read that fails is an error, not an empty list")
}

// Each write of the modes, the always rules or the always folder grants
// pings once it commits; a refused one pings nothing.
func TestEachPermissionWritePublishes(t *testing.T) {
	t.Parallel()
	s, _ := storeWithClusters(t)
	ctx := t.Context()
	rx := s.WatchPermissions()
	t.Cleanup(rx.Close)
	published := func(what string, err error) {
		t.Helper()
		require.NoError(t, err, what)
		_, err = rx.TryRecv()
		require.NoError(t, err, what)
	}

	published("the default mode", s.SetDefaultMode(ctx, permissions.Auto))
	published("a cluster's mode", s.SetMode(ctx, "dev", permissions.Auto))
	published("a cluster's mode cleared", s.ClearMode(ctx, "dev"))
	rule, err := s.AddKubeRule(ctx, permissions.Rule{Effect: permissions.Deny, Class: permissions.UpstreamWrite})
	published("a rule", err)
	published("a rule removed", s.RemoveKubeRule(ctx, rule.ID))
	published("a folder grant", s.GrantFolder(ctx, "/srv/code", false))
	published("a folder grant revoked", s.RevokeFolder(ctx, folderGrants(t, s)[0].ID))

	assert.ErrorIs(t, s.SetMode(ctx, "gone", permissions.Auto), ErrClusterGone)
	_, err = s.AddKubeRule(ctx, permissions.Rule{Effect: permissions.Allow, Class: permissions.Destructive})
	assert.Error(t, err)
	_, err = rx.TryRecv()
	assert.ErrorIs(t, err, gobus.ErrEmpty, "a refused write pinged")
}

// The file's permissions group is read by nothing and kept as it stands.
func TestTheFilesPermissionsGroupIsReadByNothing(t *testing.T) {
	t.Parallel()
	group := `{"defaultMode": "read-only", "rules": [{"id": "x", "effect": "deny", "class": 9}]}`
	s, file := openFile(t, `{"permissions": `+group+`}`)
	assert.Empty(t, s.Refused())
	mode, _, err := s.ClusterModes(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, permissions.Ask, mode)

	require.NoError(t, s.SetCloud(permissions.AWS, false))
	assert.JSONEq(t, `{"permissions": `+group+`, "sandbox": {"aws": false}, "schemaVersion": 1}`, readFile(t, file))
}

// looseTable replaces table with one of the same columns and no constraints,
// so a test can store a value no read of the real table meets.
func looseTable(t *testing.T, db *appdb.DB, table, columns string) {
	t.Helper()
	for _, q := range []string{`DROP TABLE ` + table, `CREATE TABLE ` + table + ` (` + columns + `)`} {
		exec(t, db, q)
	}
}

// A read that fails after some rows were read is a failed read: the rows
// already read are not used.
func TestAReadThatFailsPartwayAnswersNone(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	looseTable(t, db, "kube_rules", `id, chat_id, effect, class, cluster_id, namespace, verb, api_group, kind, inside, created_at`)
	looseTable(t, db, "folder_grants", `id, chat_id, folder, access, created_at`)
	for _, q := range []string{
		`INSERT INTO kube_rules VALUES ('a', NULL, 'deny', 'upstream-write', NULL, '', '', '', '', 0, 1), ('b', NULL, 'deny', 'upstream-write', NULL, '', '', '', '', 'maybe', 2)`,
		`INSERT INTO folder_grants VALUES ('a', NULL, '/srv/code', 'read', 1), ('b', NULL, NULL, 'read', 2)`,
	} {
		exec(t, db, q)
	}

	rules, err := s.KubeRules(t.Context())
	assert.Error(t, err)
	assert.Nil(t, rules)
	grants, err := s.FolderGrants(t.Context())
	assert.Error(t, err)
	assert.Nil(t, grants)
}

// A write that fails reaches its caller, and publishes nothing.
func TestAWriteThatFailsReachesTheCaller(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	ctx := t.Context()
	require.NoError(t, s.SetMode(ctx, "dev", permissions.Auto))
	require.NoError(t, s.GrantFolder(ctx, "/srv/code", false))
	grant := folderGrants(t, s)[0]
	for _, q := range []string{
		`CREATE TRIGGER refuse_mode_write BEFORE INSERT ON approval_modes BEGIN SELECT RAISE(ABORT, 'refused'); END`,
		`CREATE TRIGGER refuse_mode_delete BEFORE DELETE ON approval_modes BEGIN SELECT RAISE(ABORT, 'refused'); END`,
		`CREATE TRIGGER refuse_grant_write BEFORE INSERT ON folder_grants BEGIN SELECT RAISE(ABORT, 'refused'); END`,
		`CREATE TRIGGER refuse_rule_write BEFORE INSERT ON kube_rules BEGIN SELECT RAISE(ABORT, 'refused'); END`,
		`CREATE TRIGGER refuse_grant_delete BEFORE DELETE ON folder_grants BEGIN SELECT RAISE(ABORT, 'refused'); END`,
	} {
		exec(t, db, q)
	}
	rx := s.WatchPermissions()
	t.Cleanup(rx.Close)

	for name, err := range map[string]error{
		"the default mode": s.SetDefaultMode(ctx, permissions.ReadOnly),
		"a cluster's mode": s.SetMode(ctx, "prod", permissions.ReadOnly),
		"a mode cleared":   s.ClearMode(ctx, "dev"),
		"a folder grant":   s.GrantFolder(ctx, "/srv/data", true),
		"a grant revoked":  s.RevokeFolder(ctx, grant.ID),
		"a rule": func() error {
			_, err := s.AddKubeRule(ctx, permissions.Rule{Effect: permissions.Deny, Class: permissions.UpstreamWrite})
			return err
		}(),
	} {
		assert.ErrorContains(t, err, "refused", name)
	}
	_, err := rx.TryRecv()
	assert.ErrorIs(t, err, gobus.ErrEmpty, "a refused write pinged")
}

// A mode's write that cannot look its cluster up writes nothing.
func TestAModeWhoseClusterCannotBeReadIsAnError(t *testing.T) {
	t.Parallel()
	s, db := storeWithClusters(t)
	exec(t, db, `ALTER TABLE clusters RENAME TO clusters_gone`)
	err := s.SetMode(t.Context(), "dev", permissions.Auto)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrClusterGone)
}
