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

package logs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/amorey/gochan/spsc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// A follow sends one stream's lines while another stays silent.
func TestAFollowDoesNotWaitOnASilentStream(t *testing.T) {
	// Not t.Context, which ends before cleanup runs: the relays must still be able
	// to report their ends there.
	ctx, cancel := context.WithCancel(context.Background())
	f := newFanIn()
	quiet, silent := spsc.New[Record](0)
	q := &streamRead{key: StreamKey{Pod: "quiet"}, lines: silent}
	f.add(ctx, q)
	talking, lines := spsc.New[Record](0)
	r := &streamRead{key: StreamKey{Pod: "a"}, lines: lines}
	f.add(ctx, r)
	t.Cleanup(func() {
		defer cancel()
		quiet.Close()
		talking.Close()
		ended := f.endedHub.Receiver().Chan()
		assert.ElementsMatch(t, []*streamRead{q, r}, []*streamRead{
			testutil.Recv(t, ended, "one relay's end"),
			testutil.Recv(t, ended, "the other relay's end"),
		})
	})

	require.NoError(t, talking.Send(line("a", "app", 0, "one")))
	first := testutil.Recv(t, f.linesHub.Receiver().Chan(), "the first line")
	assert.Equal(t, "one", first.rec.Message)
	assert.Same(t, r, first.from)
	require.NoError(t, talking.Send(line("a", "app", time.Second, "two")))
	assert.Equal(t, "two", testutil.Recv(t, f.linesHub.Receiver().Chan(), "the second line").rec.Message)
}

// A fan-in whose follow ends drains the reading it was relaying.
func TestAFanInDrainsOnceTheFollowEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	f := newFanIn()
	out, lines := spsc.New[Record](0)
	f.add(ctx, &streamRead{key: StreamKey{Pod: "a"}, lines: lines})
	require.NoError(t, out.Send(line("a", "app", 0, "held")))

	cancel()

	// Taken only once the relay has given up on the follow and is draining.
	require.NoError(t, out.Send(line("a", "app", time.Second, "drained")))
	out.Close()
}

// A reading that ends is reported, with its error on it.
func TestAFollowedReadingReportsItsEnd(t *testing.T) {
	ctx := t.Context()
	f := newFanIn()
	r := cannedFrom(ctx, StreamKey{Pod: "a", Container: "app"}, []Record{line("a", "app", time.Second, "x")}, errors.New("gone"), position{})
	f.add(ctx, r)

	testutil.Recv(t, f.linesHub.Receiver().Chan(), "the line")
	end := testutil.Recv(t, f.endedHub.Receiver().Chan(), "the end")

	assert.Same(t, r, end)
	assert.EqualError(t, end.err, "gone")
}

// The lines that arrive before the handoff closes go out sorted, and every later
// one goes out as it comes.
func TestTheHandoffSortsTheFirstLines(t *testing.T) {
	var h handoff

	assert.True(t, h.hold(line("b", "app", 2*time.Second, "b2")))
	assert.True(t, h.hold(line("a", "app", time.Second, "a1")))
	assert.True(t, h.hold(line("a", "app", 3*time.Second, "a3")))
	assert.Equal(t, []string{"a1", "b2", "a3"}, messages(h.release()))
	assert.False(t, h.hold(line("c", "app", 0, "late")))
}
