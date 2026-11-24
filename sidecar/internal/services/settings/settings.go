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

// Package settings is the sidecar's user settings, in <data>/settings.json and
// in app.db, and the sandbox state derived from them.
package settings

import (
	"context"
	"fmt"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/jsonsettings"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlstmt"
)

// Settings is what the file holds that Kstack reads: nothing. The file is
// kept, unread, until nothing opens it.
type Settings struct{}

// A Refusal is one value of the file Kstack left out, or an Update a check
// refused.
type Refusal = jsonsettings.Refusal

// ErrHeld is an Update that changes a held field without naming it.
var ErrHeld = jsonsettings.ErrHeld

// Store keeps the approval modes, the always rules, the always folder grants,
// the sandbox settings, the PATH setting, the login shell's last PATH and the
// registered executables in app.db, beside a settings file it reads nothing
// from. It publishes each write of the permissions, the executables and the
// PATH.
// Safe for concurrent use.
type Store struct {
	*jsonsettings.Store[Settings]
	db    *appdb.DB
	stmts *sqlstmt.Set[stmtID]
	now   func() time.Time
}

// An Option changes how Open reads the file.
type Option func(*options)

type options struct {
	checks []func(*Settings) []Refusal
}

// WithChecks runs checks in place of the read-back's own. It is a test seam:
// production passes none.
func WithChecks(checks ...func(*Settings) []Refusal) Option {
	return func(o *options) { o.checks = checks }
}

// Open reads file, and prepares the store's statements over db. A missing file
// is empty Settings; one that is not a JSON object, or holds a group that is
// not one, is an error naming the file, since a sandbox with no settings is
// one the user cannot see. A value the read-back refuses is logged and listed
// by Refused, and left out, or for a field that restricts, answered as that
// field's strictest state.
func Open(file string, db *appdb.DB, opts ...Option) (*Store, error) {
	o := options{checks: checks}
	for _, opt := range opts {
		opt(&o)
	}
	s, err := jsonsettings.Open(file, o.checks, strictest)
	if err != nil {
		return nil, err
	}
	st, err := sqlstmt.Prepare[stmtID](context.Background(), db.Write.DB, db.Read.DB, statements)
	if err != nil {
		return nil, fmt.Errorf("prepare settings statements: %w", err)
	}
	return &Store{Store: s, db: db, stmts: st, now: time.Now}, nil
}

// Close closes the store's statements.
func (s *Store) Close() error {
	return s.stmts.Close()
}
