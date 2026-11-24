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

import "github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"

// stmtID names one of the store's statements over app.db.
type stmtID int

const (
	stmtSelectClusterKubeContext stmtID = iota
	stmtSelectMode
	stmtSelectModes
	stmtUpsertMode
	stmtDeleteMode
	stmtSelectKubeRules
	stmtInsertKubeRule
	stmtDeleteKubeRule
	stmtSelectFolderGrants
	stmtUpsertFolderGrant
	stmtDeleteFolderGrant
	stmtSelectCloudPlatformSwitches
	stmtUpdateAWSSwitch
	stmtUpdateGitHubSwitch
	stmtUpdateGCPSwitch
	stmtUpdateAzureSwitch
	stmtSelectExecutables
	stmtSelectExecutableListed
	stmtInsertExecutable
	stmtDeleteExecutable
)

// statements are the store's. Those over a table with a chat_id touch only
// rows whose chat_id is NULL: a chat's rows are agent/chat's. The text lives here and never at a call site.
var statements = []sqlstmt.Statement{
	// Whether a cluster not awaiting deletion has a kube-context. OnBoth,
	// since a mode's write reads it in its transaction.
	stmtSelectClusterKubeContext: sqlstmt.OnBoth(`SELECT source = 'kubeconfig' AND ifnull(source_key, '') <> ''
	FROM clusters WHERE id = ? AND delete_requested_at IS NULL`),
	// The cluster's own row first, then the default's, through the index on
	// ifnull(cluster_id, ''), which spells the default ''.
	stmtSelectMode: sqlstmt.OnReader(`SELECT cluster_id IS NOT NULL, mode FROM approval_modes
	WHERE ifnull(cluster_id, '') IN (?, '') ORDER BY cluster_id IS NULL LIMIT 1`),
	stmtSelectModes: sqlstmt.OnReader(`SELECT ifnull(cluster_id, ''), mode FROM approval_modes`),
	// A cluster of '' is the default's row.
	stmtUpsertMode: sqlstmt.OnWriter(`INSERT INTO approval_modes (cluster_id, mode, updated_at) VALUES (nullif(?, ''), ?, ?)
	ON CONFLICT (ifnull(cluster_id, '')) DO UPDATE SET mode = excluded.mode, updated_at = excluded.updated_at`),
	stmtDeleteMode: sqlstmt.OnWriter(`DELETE FROM approval_modes WHERE ifnull(cluster_id, '') = ?`),

	// OnBoth, since a rule's write reads the rules in its transaction.
	stmtSelectKubeRules: sqlstmt.OnBoth(`SELECT ` + KubeRuleColumns + ` FROM kube_rules WHERE chat_id IS NULL ORDER BY created_at, id`),
	stmtInsertKubeRule: sqlstmt.OnWriter(`INSERT INTO kube_rules (chat_id, ` + KubeRuleColumns + `, created_at)
	VALUES (NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
	stmtDeleteKubeRule: sqlstmt.OnWriter(`DELETE FROM kube_rules WHERE chat_id IS NULL AND id = ?`),

	stmtSelectFolderGrants: sqlstmt.OnReader(`SELECT ` + FolderGrantColumns + ` FROM folder_grants WHERE chat_id IS NULL ORDER BY created_at, id`),
	// A folder granted again keeps its row's id and created_at, and changes its access.
	stmtUpsertFolderGrant: sqlstmt.OnWriter(`INSERT INTO folder_grants (id, chat_id, folder, access, created_at) VALUES (?, NULL, ?, ?, ?)
	ON CONFLICT (ifnull(chat_id, ''), folder) DO UPDATE SET access = excluded.access`),
	stmtDeleteFolderGrant: sqlstmt.OnWriter(`DELETE FROM folder_grants WHERE chat_id IS NULL AND id = ?`),

	stmtSelectCloudPlatformSwitches: sqlstmt.OnReader(`SELECT aws_enabled, github_enabled, gcp_enabled, azure_enabled
	FROM sandbox_settings WHERE id = 1`),
	stmtUpdateAWSSwitch:    sqlstmt.OnWriter(`UPDATE sandbox_settings SET aws_enabled = ?, updated_at = ? WHERE id = 1`),
	stmtUpdateGitHubSwitch: sqlstmt.OnWriter(`UPDATE sandbox_settings SET github_enabled = ?, updated_at = ? WHERE id = 1`),
	stmtUpdateGCPSwitch:    sqlstmt.OnWriter(`UPDATE sandbox_settings SET gcp_enabled = ?, updated_at = ? WHERE id = 1`),
	stmtUpdateAzureSwitch:  sqlstmt.OnWriter(`UPDATE sandbox_settings SET azure_enabled = ?, updated_at = ? WHERE id = 1`),

	// rowid orders two added in the same millisecond.
	stmtSelectExecutables: sqlstmt.OnReader(`SELECT name, invocation FROM executables ORDER BY created_at, rowid`),
	// OnBoth, since a register reads it in its transaction.
	stmtSelectExecutableListed: sqlstmt.OnBoth(`SELECT EXISTS (SELECT 1 FROM executables WHERE name = ?)`),
	stmtInsertExecutable:       sqlstmt.OnWriter(`INSERT INTO executables (name, invocation, created_at) VALUES (?, ?, ?)`),
	stmtDeleteExecutable:       sqlstmt.OnWriter(`DELETE FROM executables WHERE name = ?`),
}

type stmts = sqlstmt.Stmts[stmtID]
