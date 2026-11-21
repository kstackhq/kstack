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

// Every statement toolsettings issues, named once and prepared once on the
// app's DB (sqlstmt.Set).
package toolsettings

import "github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"

// stmtID indexes statements.
type stmtID int

const (
	stmtSelectDisabled stmtID = iota
	stmtUpsert
)

var statements = []sqlstmt.Statement{
	stmtSelectDisabled: sqlstmt.OnReader(`SELECT tool_name FROM tool_settings
	WHERE cluster_id IS NULL AND enabled = 0 ORDER BY tool_name`),
	stmtUpsert: sqlstmt.OnWriter(`INSERT INTO tool_settings (tool_name, cluster_id, enabled, updated_at)
	VALUES (?, NULL, ?, ?)
	ON CONFLICT (ifnull(cluster_id, ''), tool_name) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`),
}

type stmts = sqlstmt.Stmts[stmtID]
