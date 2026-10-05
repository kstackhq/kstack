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

package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// A subagent's session is its parent's under Kind Subagent, compared field by
// field.
func TestNarrowKeepsTheParentsIdentity(t *testing.T) {
	for _, outside := range []bool{false, true} {
		parent := Session{Kind: Chat, Outside: outside}

		got := Narrow(parent)

		assert.Equal(t, Subagent, got.Kind)
		assert.Equal(t, parent.Outside, got.Outside)
	}
}

// A subagent decides by its parent's mode and rules.
func TestNarrowKeepsThePolicy(t *testing.T) {
	policy := permissions.Policy{Mode: permissions.Auto, Rules: []permissions.Rule{permissions.Refused}}
	parent := Session{Kind: Chat, Policy: func(context.Context, string) permissions.Policy { return policy }}

	got := Narrow(parent)

	assert.Equal(t, policy, got.Policy(t.Context(), "dev"))
}

// A subagent has its parent's network, the turn's toggle included, for its
// whole life.
func TestNarrowKeepsTheNetwork(t *testing.T) {
	parent := Session{Kind: Chat, Network: func(context.Context) Network { return NetworkTurn }}

	got := Narrow(parent)

	assert.Equal(t, NetworkTurn, got.Network(t.Context()))
}

// A session made without sessionFor has no network, ever.
func TestTheZeroSessionHasNoNetwork(t *testing.T) {
	assert.Nil(t, Session{Kind: Monitor}.Network)
}
