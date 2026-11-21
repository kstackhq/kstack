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

package toolsettings

import (
	"context"
	"fmt"
)

// disabled is the names of the tools turned off for every cluster, sorted.
func disabled(ctx context.Context, st stmts) ([]string, error) {
	rows, err := st.Query(ctx, stmtSelectDisabled)
	if err != nil {
		return nil, fmt.Errorf("select disabled tools: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil { // coverage:ignore tool_name is a STRICT TEXT NOT NULL column
			return nil, fmt.Errorf("scan disabled tool: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// upsert writes the setting of the tool named name for every cluster.
func upsert(ctx context.Context, st stmts, name string, enabled bool, at int64) error {
	if _, err := st.Exec(ctx, stmtUpsert, name, enabled, at); err != nil {
		return fmt.Errorf("write setting of %s: %w", name, err)
	}
	return nil
}
