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

// Package clustertest is a cache store for tests outside the cluster package: the
// real schema and query views, holding the objects a test applies. Test-only.
package clustertest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/internal/kubestore"
)

// Store is one cache's store, open for the test's life.
type Store struct {
	s *kubestore.Store
	// applied counts the bodies applied, the resourceVersion the next one takes.
	applied int
}

// NewStore opens an empty store in a directory of the test's.
func NewStore(t testing.TB) *Store {
	t.Helper()
	m := kubestore.NewManager(t.TempDir(), kubestore.Retention{})
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	s, err := m.OpenOrCreate(1)
	require.NoError(t, err)
	t.Cleanup(s.Release)
	return &Store{s: s}
}

// Apply adds or replaces body, as a watch's Added would, and mirrors its kind,
// cluster-scoped when the body names no namespace. Its resource is its kind's
// lower-case plural, and its resourceVersion a fresh one, written into body,
// since a body applied again under the one it had is no change to the store.
func (s *Store) Apply(t testing.TB, body map[string]any) {
	t.Helper()
	u := &unstructured.Unstructured{Object: body}
	s.applied++
	u.SetResourceVersion(strconv.Itoa(s.applied))
	scope := kubestore.ScopeNamespaced
	if u.GetNamespace() == "" {
		scope = kubestore.ScopeCluster
	}
	s.mirror(t, u.GetAPIVersion(), u.GetKind(), scope)
	k := kubestore.Kind{APIVersion: u.GetAPIVersion(), Kind: u.GetKind(), Resource: strings.ToLower(u.GetKind()) + "s"}
	require.NoError(t, s.s.ApplyChange(context.Background(), k, watch.Added, u))
}

// Mirror adds a namespaced kind to the kinds the cache mirrors, as its sync does
// when it starts, so the kinds view holds it with no object of it applied.
func (s *Store) Mirror(t testing.TB, apiVersion, kind string) {
	t.Helper()
	s.mirror(t, apiVersion, kind, kubestore.ScopeNamespaced)
}

func (s *Store) mirror(t testing.TB, apiVersion, kind, scope string) {
	t.Helper()
	row := kubestore.KindRow{APIVersion: apiVersion, Kind: kind, Resource: strings.ToLower(kind) + "s", Scope: scope}
	require.NoError(t, s.s.SyncKinds(context.Background(), []kubestore.KindRow{row}, false, 0))
}

// Query runs sql over the query views, as CachedData's QueryOwn does for a
// CacheReading.
func (s *Store) Query(ctx context.Context, sql string, maxRows, maxBytes int, args ...any) (cluster.ClusterCachedDataQueryResult, error) {
	res, err := s.s.QueryOwn(ctx, sql, maxRows, maxBytes, args...)
	if qe := (*kubestore.QueryError)(nil); errors.As(err, &qe) {
		return cluster.ClusterCachedDataQueryResult{}, &cluster.QueryError{Message: qe.Message}
	}
	if err != nil {
		return cluster.ClusterCachedDataQueryResult{}, err
	}
	return cluster.ClusterCachedDataQueryResult{Columns: res.Columns, Rows: res.Rows, More: res.More}, nil
}
