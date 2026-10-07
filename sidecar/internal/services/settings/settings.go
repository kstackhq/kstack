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

// Package settings is the sidecar's user settings in <data>/settings.json, and
// the sandbox state derived from them.
package settings

import (
	"github.com/kstackhq/kstack/sidecar/internal/lib/jsonsettings"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// Settings is the user's settings, one group per area. Each field is added by
// the step that needs it.
type Settings struct {
	Sandbox     SandboxSettings    `json:"sandbox,omitzero"`
	Permissions PermissionSettings `json:"permissions,omitzero"`
	Executables []Executable       `json:"executables,omitempty"` // the user's registered tools, in the order added
	Onboarded   bool               `json:"onboarded,omitempty"`   // the onboarding flow was finished; it gates nothing
}

// SandboxSettings is the sandbox group: the user's PATH as a sandboxed run
// searches it.
type SandboxSettings struct {
	Path []PathEntry `json:"path,omitempty"` // the user's PATH, frozen, in the shell's order
	// PathResolved is set by the first sync: before it a run searches the
	// platform's default PATH, after it the list alone.
	PathResolved bool `json:"pathResolved,omitempty"`
	// PathStrict is set by a Remove that ends the store's hold on path, and
	// cleared by the next sync, which it makes file every new entry pending:
	// the entry the store could not read may have been a removal.
	PathStrict bool `json:"pathStrict,omitempty"`
}

// PermissionSettings is the permissions group: the approval modes and the
// always rules.
type PermissionSettings struct {
	DefaultMode permissions.Mode   `json:"defaultMode,omitempty"` // Ask when empty
	Modes       []ContextMode      `json:"modes,omitempty"`       // first match wins
	Rules       []permissions.Rule `json:"rules,omitempty"`       // the always rules, the user's
}

// A Refusal is one value of the file Kstack left out, or an Update a check
// refused.
type Refusal = jsonsettings.Refusal

// ErrHeld is an Update that changes a held field without naming it.
var ErrHeld = jsonsettings.ErrHeld

// Store keeps Settings in one JSON file and publishes each write. Safe for
// concurrent use. It wraps the generic store so the permission readers in
// permissions.go can be its methods.
type Store struct {
	*jsonsettings.Store[Settings]
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

// Open reads file. A missing file is empty Settings; one that is not a JSON
// object, or holds a group that is not one, is an error naming the file, since
// a sandbox with no settings is one the user cannot see. A value the read-back
// refuses is logged and listed by Refused, and left out, or for a field that
// restricts, answered as that field's strictest state.
func Open(file string, opts ...Option) (*Store, error) {
	o := options{checks: checks}
	for _, opt := range opts {
		opt(&o)
	}
	s, err := jsonsettings.Open(file, o.checks, strictest)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}
