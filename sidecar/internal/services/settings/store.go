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
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// errUnchanged ends a write that has nothing to change: it commits nothing and
// publishes nothing, and its caller answers no error.
var errUnchanged = errors.New("settings: nothing to write")

// write runs fn in one transaction and, once it commits, notifies key. A
// write fn refuses notifies nothing.
func (s *Store) write(ctx context.Context, key string, fn func(stmts) error) error {
	err := s.stmts.InTx(ctx, fn)
	if errors.Is(err, errUnchanged) {
		return nil
	}
	if err != nil {
		return err
	}
	s.db.Notify(key)
	return nil
}

// KubeRuleColumns are a kube_rules row's columns, in the order KubeRules
// scans them and KubeRuleValues gives them: the table's one encoding, which
// agent/chat reads and writes a chat's rows through.
const KubeRuleColumns = `id, effect, class, cluster_id, namespace, verb, api_group, kind, inside`

// FolderGrantColumns are a folder_grants row's columns, in the order
// FolderGrants scans them.
const FolderGrantColumns = `id, folder, access`

// KubeRules is the rules rows hold, each row KubeRuleColumns, in their order.
// It closes rows. A read that fails partway answers none.
func KubeRules(rows *sql.Rows) ([]permissions.Rule, error) {
	return scanAll(rows, func() (permissions.Rule, error) {
		var r permissions.Rule
		var class string
		var cluster sql.NullString
		if err := rows.Scan(&r.ID, &r.Effect, &class, &cluster, &r.Namespace, &r.Verb, &r.Group, &r.Kind, &r.Inside); err != nil {
			return permissions.Rule{}, fmt.Errorf("scan rule: %w", err)
		}
		r.ClusterID = apimeta.ClusterID(cluster.String)
		var ok bool
		if r.Class, ok = permissions.ClassNamed(class); !ok { // coverage:ignore the column's CHECK holds the three names
			return permissions.Rule{}, fmt.Errorf("scan rule: a class named %q", class)
		}
		return r, nil
	})
}

// KubeRuleValues is r as a row stores it, one value per column of
// KubeRuleColumns: a rule naming no cluster stores NULL.
func KubeRuleValues(r permissions.Rule) []any {
	cluster := sql.NullString{String: string(r.ClusterID), Valid: r.ClusterID != ""}
	return []any{r.ID, string(r.Effect), r.Class.Name(), cluster, r.Namespace, r.Verb, r.Group, r.Kind, r.Inside}
}

// FolderGrants is the grants rows hold, each row FolderGrantColumns, in their
// order. It closes rows. A read that fails partway answers none.
func FolderGrants(rows *sql.Rows) ([]FolderGrant, error) {
	return scanAll(rows, func() (FolderGrant, error) {
		var g FolderGrant
		var access string
		if err := rows.Scan(&g.ID, &g.Path, &access); err != nil {
			return FolderGrant{}, fmt.Errorf("scan folder grant: %w", err)
		}
		g.Write = access == "write"
		return g, nil
	})
}

// scanAll is every row of rows through scan, and closes rows. A read that
// fails partway answers none.
func scanAll[T any](rows *sql.Rows, scan func() (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil { // coverage:ignore an I/O error past the first row, which no test can inject
		return nil, err
	}
	return out, nil
}

// FolderAccess is a grant's access column.
func FolderAccess(write bool) string {
	if write {
		return "write"
	}
	return "read"
}

// clusterHasKubeContext refuses a cluster that is gone or awaiting deletion,
// and answers whether it has a kube-context.
func clusterHasKubeContext(ctx context.Context, st stmts, cluster apimeta.ClusterID) (bool, error) {
	var has bool
	err := st.QueryRow(ctx, stmtSelectClusterKubeContext, cluster).Scan(&has)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrClusterGone
	}
	if err != nil {
		return false, fmt.Errorf("select cluster: %w", err)
	}
	return has, nil
}

// requireKubeContext refuses a cluster as clusterHasKubeContext does, and one
// with no kube-context as ErrNoKubeContext.
func requireKubeContext(ctx context.Context, st stmts, cluster apimeta.ClusterID) error {
	has, err := clusterHasKubeContext(ctx, st, cluster)
	if err != nil {
		return err
	}
	if !has {
		return ErrNoKubeContext
	}
	return nil
}

// selectKubeRules is the always rules in the order written.
func selectKubeRules(ctx context.Context, st stmts) ([]permissions.Rule, error) {
	rows, err := st.Query(ctx, stmtSelectKubeRules)
	if err != nil {
		return nil, fmt.Errorf("select rules: %w", err)
	}
	return KubeRules(rows)
}

func insertKubeRule(ctx context.Context, st stmts, r permissions.Rule, at int64) error {
	if _, err := st.Exec(ctx, stmtInsertKubeRule, append(KubeRuleValues(r), at)...); err != nil {
		return fmt.Errorf("insert rule: %w", err)
	}
	return nil
}

// execOne runs stmt over args: missing when it changed no row.
func execOne(ctx context.Context, st stmts, stmt stmtID, missing error, args ...any) error {
	res, err := st.Exec(ctx, stmt, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil { // coverage:ignore modernc.org/sqlite's RowsAffected never fails
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return missing
	}
	return nil
}

// selectFolderGrants is the always folder grants in the order written,
// unchecked.
func selectFolderGrants(ctx context.Context, st stmts) ([]FolderGrant, error) {
	rows, err := st.Query(ctx, stmtSelectFolderGrants)
	if err != nil {
		return nil, fmt.Errorf("select folder grants: %w", err)
	}
	return FolderGrants(rows)
}

func upsertFolderGrant(ctx context.Context, st stmts, id, path string, write bool, at int64) error {
	if _, err := st.Exec(ctx, stmtUpsertFolderGrant, id, path, FolderAccess(write), at); err != nil {
		return fmt.Errorf("write folder grant: %w", err)
	}
	return nil
}
