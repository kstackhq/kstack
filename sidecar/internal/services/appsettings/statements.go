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

// Every statement appsettings issues, named once and prepared once on the app's
// DB (sqlstmt.Set). The text lives here and never at a call site.
package appsettings

import "github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"

// stmtID indexes statements.
type stmtID int

const (
	stmtSelect stmtID = iota
	stmtSetDefaultCluster
	stmtClearDefaultCluster
	numStmts int = iota
)

// settingsColumns is the projection every statement answers, in scanSettings' order.
const settingsColumns = `default_cluster_id`

// Each write RETURNs the row it left, so no read sits between it and a
// concurrent write.
var statements = []sqlstmt.Statement{
	stmtSelect: sqlstmt.OnReader(`SELECT ` + settingsColumns + ` FROM app_settings`),
	// Only an unmarked cluster can become the default; any other writes no row.
	stmtSetDefaultCluster: sqlstmt.OnWriter(`UPDATE app_settings SET default_cluster_id = ?1
	WHERE EXISTS (SELECT 1 FROM clusters WHERE id = ?1 AND delete_requested_at IS NULL)
	RETURNING ` + settingsColumns),
	stmtClearDefaultCluster: sqlstmt.OnWriter(`UPDATE app_settings SET default_cluster_id = NULL
	RETURNING ` + settingsColumns),
}
