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

// Package appsettings keeps the app-level choices the user makes in Kstack's own
// UI: the one row of app.db's app_settings table, a typed column per setting.
// settings.json, the policy a person edits by hand, is services/settings'.
package appsettings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/lib/drain"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"
)

// Settings is the row, as the store and the wire hold it.
type Settings struct {
	// DefaultClusterID is Kstack's default cluster, nil when unset. It may name a
	// cluster that is disabled or no longer in its source; readers fall back to
	// the kubeconfig's current-context.
	DefaultClusterID *apimeta.ClusterID
}

var (
	// ErrClusterNotFound is a default naming a cluster that is missing or marked
	// for deletion.
	ErrClusterNotFound = errors.New("appsettings: cluster not found")
	// ErrStopping is a watch opened as the service stops.
	ErrStopping = errors.New("appsettings: stopping")
)

// Service is the app settings: the row, its writes and its watch.
type Service interface {
	Start(ctx context.Context) (func(context.Context) error, error)
	Close() error

	Get(ctx context.Context) (Settings, error)
	// Watch is the settings as a gauge: the current row, then the row after each
	// change to it.
	Watch(ctx context.Context) (*Stream, error)

	// SetDefaultCluster makes id Kstack's default cluster, or clears it for "",
	// and answers the settings it left. A cluster that is gone or marked is
	// ErrClusterNotFound. Deleting the cluster clears the default.
	SetDefaultCluster(ctx context.Context, id apimeta.ClusterID) (Settings, error)
}

var _ Service = (*service)(nil)

// New builds the service over the app's DB. Nothing runs until Start.
func New(db *appdb.DB) (Service, error) { return newService(db) }

type service struct {
	db    *appdb.DB
	store *sqlstmt.Set[stmtID]

	// Shutdown: wg holds the watch pumps, which join through enter.
	wg       sync.WaitGroup
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	stopped  chan struct{}
	stopOnce sync.Once
}

func newService(db *appdb.DB) (*service, error) {
	st, err := sqlstmt.Prepare[stmtID](context.Background(), db.Write.DB, db.Read.DB, statements)
	if err != nil {
		return nil, fmt.Errorf("prepare app settings statements: %w", err)
	}
	s := &service{db: db, store: st, stopped: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

// Start has no startup work; the func it returns ends the watch pumps and joins them.
func (s *service) Start(context.Context) (func(context.Context) error, error) {
	return s.stop, nil
}

func (s *service) stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		close(s.stopped)
		s.mu.Unlock()
	})
	return drain.WithContext(ctx, s.wg.Wait)
}

// enter joins the group stop waits on, ErrStopping once the service is stopping.
// The caller owes the matching wg.Done.
func (s *service) enter() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stopped:
		return ErrStopping
	default:
		s.wg.Add(1)
		return nil
	}
}

// Close releases the prepared statements. It runs after stop has joined every reader.
func (s *service) Close() error { return s.store.Close() }

func (s *service) Get(ctx context.Context) (Settings, error) {
	out, err := scanSettings(s.store.Stmts().QueryRow(ctx, stmtSelect))
	if err != nil {
		return Settings{}, fmt.Errorf("get app settings: %w", err)
	}
	return out, nil
}

func (s *service) SetDefaultCluster(ctx context.Context, id apimeta.ClusterID) (Settings, error) {
	stmt, args := stmtSetDefaultCluster, []any{string(id)}
	if id == "" {
		stmt, args = stmtClearDefaultCluster, nil
	}
	out, err := scanSettings(s.store.Stmts().QueryRow(ctx, stmt, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, fmt.Errorf("set default cluster %s: %w", id, ErrClusterNotFound)
	}
	if err != nil {
		return Settings{}, fmt.Errorf("set default cluster: %w", err)
	}
	s.db.Notify(appdb.KeyAppSettings)
	return out, nil
}

func scanSettings(row *sql.Row) (Settings, error) {
	var def sql.NullString
	if err := row.Scan(&def); err != nil {
		return Settings{}, err
	}
	var out Settings
	if def.Valid {
		id := apimeta.ClusterID(def.String)
		out.DefaultClusterID = &id
	}
	return out, nil
}
