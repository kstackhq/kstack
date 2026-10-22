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

package logswatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput/logsinputtest"
)

// fakeFollows is the log service as the tool opens a follow: a stream that holds
// until its context ends, closing ended then, or err; each follow's context, view
// and cursor kept. stalled, when set, holds the open itself until its context
// ends, closing stalled once it has begun.
type fakeFollows struct {
	err     error
	stalled chan struct{}
	ctx     context.Context
	view    logs.Scope
	after   *string
	ended   chan struct{}
	opened  bool
}

func newFakeFollows() *fakeFollows { return &fakeFollows{ended: make(chan struct{})} }

func (f *fakeFollows) Follow(ctx context.Context, _ cluster.ClusterID, view logs.Scope, after *string) (*cluster.Stream[logs.FollowFrame], error) {
	if f.stalled != nil {
		close(f.stalled)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	f.ctx, f.view, f.after, f.opened = ctx, view, after, true
	return cluster.NewStream(ctx, func(ctx context.Context, _ chan<- logs.FollowFrame) error {
		<-ctx.Done()
		close(f.ended)
		return nil
	}), nil
}

// fakeWatches is the chat's watches: it keeps what it is handed and answers id, or
// err.
type fakeWatches struct {
	err     error
	started []tools.Watch
}

func (f *fakeWatches) Start(w tools.Watch) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.started = append(f.started, w)
	return "t-1", nil
}

// webapp is the webapp deployment's view in prod, its app container watched.
const webapp = `{"sources":[{"namespace":"prod","resource":"deployments/webapp","containers":["app"]}]`

// run calls the tool over the watching cluster, follows and watches with raw on
// ctx, and answers what the model reads and the action.
func run(t *testing.T, ctx context.Context, f *fakeFollows, w *fakeWatches, raw string) (string, bool, *tools.Action) {
	t.Helper()
	tool := New(logsinputtest.Watching(t), f, func() time.Time { return clock })
	return tool.RunShown(ctx, tools.Runtime{ClusterID: "7", Watches: w}, json.RawMessage(raw))
}

// watchOf is the watch a call starts, which must start, its receipt and action.
func watchOf(t *testing.T, f *fakeFollows, raw string) (string, tools.LogsWatchAction) {
	t.Helper()
	w := &fakeWatches{}
	text, isError, shown := run(t, t.Context(), f, w, raw)
	require.False(t, isError, text)
	require.NotNil(t, shown)
	require.NotNil(t, shown.LogsWatch)
	require.Len(t, w.started, 1)
	t.Cleanup(w.started[0].Stop)
	return text, *shown.LogsWatch
}

// The receipt names the sources, the pattern, the start, the limits and the end,
// then the task; the action carries what the clock made of them, and the follow
// starts a nanosecond before a moment, so the moment's lines are in it.
func TestTheReceiptNamesTheWatch(t *testing.T) {
	t.Parallel()
	f := newFakeFollows()
	text, action := watchOf(t, f, webapp+`,"grep":"panic|Exception","since":"2026-10-18T13:45:00Z","until":"PT10M","limit":3,"description":"Panics"}`)

	assert.Equal(t, "Watching deployments/webapp in prod for /panic|Exception/ from 2026-10-18T13:45:00Z, up to 3 lines or 16384 bytes, "+
		"until 2026-10-18T14:00:00Z (task t-1). You will be told when it ends.", text)
	since := time.Date(2026, 10, 18, 13, 45, 0, 0, time.UTC)
	assert.Equal(t, since, action.Since)
	assert.Equal(t, clock.Add(10*time.Minute), action.Until)
	assert.Equal(t, clock, action.ResolvedAt)
	assert.Equal(t, 3, action.Limit)
	assert.Equal(t, logsinput.SampleBudget, action.MaxBytes)
	assert.Equal(t, "panic|Exception", action.Grep)
	assert.Equal(t, []string{"app"}, action.Sources[0].Containers)
	assert.Equal(t, "2026-10-18T13:44:59.999999999Z", *f.after)
	assert.Equal(t, "panic|Exception", f.view.Grep)
	deadline, ok := f.ctx.Deadline()
	require.True(t, ok)
	assert.Equal(t, action.Until, deadline)
}

// A bare call watches from now for any line, up to a day; a cursor's watch reads on
// past its line.
func TestABareWatchIsForAnyLineFromNow(t *testing.T) {
	t.Parallel()
	f := newFakeFollows()
	text, action := watchOf(t, f, webapp+`}`)
	assert.Equal(t, "Watching deployments/webapp in prod for any line from now, up to 1 line or 16384 bytes, "+
		"until 2026-10-19T13:50:00Z (task t-1). You will be told when it ends.", text)
	assert.Equal(t, clock, action.Since)
	assert.Equal(t, clock.Add(maxWatch), action.Until)
	assert.Equal(t, "2026-10-18T13:49:59.999999999Z", *f.after)
}

// An until past the cap is cut to it, and the receipt says so.
func TestTheReceiptSaysAnUntilWasCut(t *testing.T) {
	t.Parallel()
	text, action := watchOf(t, newFakeFollows(), webapp+`,"until":"P2D"}`)
	assert.Equal(t, clock.Add(maxWatch), action.Until)
	assert.Contains(t, text, "until 2026-10-19T13:50:00Z, the longest a watch runs (task t-1)")
}

// The follow outlives the call: the call's context ending leaves it open, and the
// watch's stop ends it.
func TestTheFollowOutlivesTheCall(t *testing.T) {
	t.Parallel()
	f, w := newFakeFollows(), &fakeWatches{}
	ctx, cancel := context.WithCancel(t.Context())
	// The real clock: the follow's deadline is now plus a day, which a fixed clock
	// would put in the past once the real date passes it.
	tool := New(logsinputtest.Watching(t), f, time.Now)
	_, isError, _ := tool.RunShown(ctx, tools.Runtime{ClusterID: "7", Watches: w}, json.RawMessage(webapp+`}`))
	require.False(t, isError)
	cancel()

	// A negative assertion: the follow's own context stays open past the call's.
	select {
	case <-f.ended:
		t.Fatal("the call's end ended the follow")
	case <-time.After(10 * time.Millisecond):
	}
	w.started[0].Stop()
	testutil.Wait(t, f.ended, "the stop to end the follow")
}

// A follow that stalls while it opens is still the call's: the call's end ends
// the open, and nothing starts. The loop answers the call's own cancel or timeout.
func TestTheCallBoundsTheFollowsOpen(t *testing.T) {
	t.Parallel()
	f, w := &fakeFollows{stalled: make(chan struct{})}, &fakeWatches{}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-f.stalled
		cancel()
	}()
	_, isError, shown := run(t, ctx, f, w, webapp+`}`)
	assert.True(t, isError)
	assert.Nil(t, shown)
	assert.Empty(t, w.started)
}

// A start the chat refuses lets the follow go and answers StartRefusal's words.
func TestARefusedStartLetsTheFollowGo(t *testing.T) {
	t.Parallel()
	f := newFakeFollows()
	text, isError, shown := run(t, t.Context(), f, &fakeWatches{err: tools.ErrChatTaskLimit}, webapp+`}`)
	assert.True(t, isError)
	assert.Nil(t, shown)
	assert.Equal(t, tools.StartRefusal(tools.ErrChatTaskLimit), text)
	testutil.Wait(t, f.ended, "the follow to end")
}

// A view the mirror refuses is refused as LogsView refuses it, and opens nothing; a
// follow the service refuses answers its words, and any other failure none.
func TestARefusedWatchOpensNothing(t *testing.T) {
	t.Parallel()
	f := newFakeFollows()
	text, isError, _ := run(t, t.Context(), f, &fakeWatches{}, `{"sources":[{"namespace":"prod","resource":"pods/webapp-a","containers":["nope"]}]}`)
	assert.True(t, isError)
	assert.JSONEq(t, `{"error":"no-container","source":0,"containers":["app","istio-proxy","setup"]}`, text)
	assert.False(t, f.opened)

	f = &fakeFollows{err: logs.Refusal{Reason: "no container of this view could be read: \x1b[31mdenied sk-" + strings.Repeat("A", 30)}}
	text, isError, _ = run(t, t.Context(), f, &fakeWatches{}, webapp+`}`)
	assert.True(t, isError)
	assert.Contains(t, text, `"error":"refused"`)
	assert.NotContains(t, text, "sk-")
	assert.NotContains(t, text, "\x1b")

	f = &fakeFollows{err: errors.New("open /home/user/cache: disk I/O error")}
	text, _, _ = run(t, t.Context(), f, &fakeWatches{}, webapp+`}`)
	assert.JSONEq(t, `{"error":"read-failed"}`, text)

	text, isError, _ = run(t, t.Context(), newFakeFollows(), &fakeWatches{}, webapp+`,"limit":0}`)
	assert.True(t, isError)
	assert.Contains(t, text, `"field":"limit"`)
}

// A runtime with no watches, a subagent's or a monitor's, opens nothing.
func TestARuntimeWithNoWatchesOpensNothing(t *testing.T) {
	t.Parallel()
	f := newFakeFollows()
	tool := New(logsinputtest.Watching(t), f, func() time.Time { return clock })
	_, isError, shown := tool.RunShown(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(webapp+`}`))
	assert.True(t, isError)
	assert.Nil(t, shown)
	assert.False(t, f.opened)
}
