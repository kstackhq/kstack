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

package kstackcloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
)

// Subscribe sends the first state, then only a change of account: a sign-in
// under way, failed or cancelled is still signed out.
func TestSubscribeSendsOnlyAccountChanges(t *testing.T) {
	t.Parallel()
	e := newFakeEngine()
	s := newTestService(t, e, &fakeKeyring{})
	states, cancel := s.Subscribe()
	defer cancel()

	signedOut := connectors.State{Phase: connectors.SignedOut}
	e.publish(t, signedOut)
	assert.Equal(t, signedOut, testutil.Recv(t, states, "the first state"))

	// Each publish returns once the filter has taken the state, so a frame sent
	// for any of these would arrive before the sign-in's.
	e.publish(t, connectors.State{Phase: connectors.SigningIn, Session: 1})
	e.publish(t, connectors.State{Phase: connectors.Failed, Failure: connectors.Unavailable, Session: 1})
	e.publish(t, connectors.State{Phase: connectors.SigningIn, Session: 2})
	e.publish(t, connectors.State{Phase: connectors.SignedOut, Session: 3})

	ada := connectors.State{Phase: connectors.Connected, Session: 4, Subject: "u1", Email: "ada@example.com", Name: "Ada"}
	e.publish(t, ada)
	assert.Equal(t, ada, testutil.Recv(t, states, "the sign-in"))

	e.publish(t, connectors.State{Phase: connectors.Connected, Session: 4, Subject: "u1", Email: "ada@example.com", Name: "Ada", RevocationUnconfirmed: true})
	e.publish(t, signedOut)
	assert.Equal(t, signedOut, testutil.Recv(t, states, "the sign-out"))
}

// Cancel ends the engine's subscription, and the stream with it, even while a
// frame waits on a subscriber that stopped reading.
func TestSubscribeCancelEndsTheStream(t *testing.T) {
	t.Parallel()
	e := newFakeEngine()
	s := newTestService(t, e, &fakeKeyring{})
	states, cancel := s.Subscribe()

	e.publish(t, connectors.State{Phase: connectors.SignedOut})
	cancel()

	testutil.WaitClosed(t, states, "the stream to end")
	require.Equal(t, []string{"Subscribe", "Unsubscribe"}, e.recorded())
}
