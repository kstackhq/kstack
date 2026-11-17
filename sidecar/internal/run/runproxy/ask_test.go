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

package runproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
)

// answer is what the fake asker answers one Ask with.
type answer struct {
	approved bool
	err      error
}

type recorded struct {
	req    Request
	d      permissions.Decision
	reason string
}

// fakeAsker hands each Ask to the test on asked and waits for its answer.
type fakeAsker struct {
	asked     chan Request
	answers   chan answer
	recorded  chan recorded
	recordErr error
}

func newFakeAsker() *fakeAsker {
	return &fakeAsker{asked: make(chan Request, 16), answers: make(chan answer, 16), recorded: make(chan recorded, 16)}
}

func (a *fakeAsker) Ask(ctx context.Context, r Request) (bool, error) {
	a.asked <- r
	select {
	case ans := <-a.answers:
		return ans.approved, ans.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (a *fakeAsker) Record(_ context.Context, r Request, d permissions.Decision, reason string) error {
	a.recorded <- recorded{r, d, reason}
	return a.recordErr
}

// change is a class 4 request with a shown body.
func change() Request {
	return Request{
		Action: permissions.Action{Class: permissions.UpstreamWrite, Summary: "AWS: call ec2:TerminateInstances"},
		Sent:   Sent{Host: "ec2.amazonaws.com", Method: "POST", Path: "/", ContentType: "application/json", Body: []byte(`{"InstanceIds":["i-1"]}`)},
	}
}

// dataRead is a class 6 request with no body.
func dataRead() Request {
	return Request{
		Action: permissions.Action{Class: permissions.SecretRead, Summary: "AWS: read s3://b/k"},
		Sent:   Sent{Host: "b.s3.amazonaws.com", Method: "GET", Path: "/k"},
	}
}

// decided is a Decide running on its own goroutine: its outcome, and a
// channel each forward call is reported on, which waits on release.
type decided struct {
	outcome  chan Outcome
	forwards chan struct{}
	release  chan struct{}
}

func decide(ctx context.Context, g *Grant, req Request) *decided {
	d := &decided{outcome: make(chan Outcome, 1), forwards: make(chan struct{}, 1), release: make(chan struct{})}
	go func() {
		d.outcome <- g.Decide(ctx, req, func() error {
			d.forwards <- struct{}{}
			<-d.release
			return nil
		})
	}()
	return d
}

// decideNow is Decide with a forward that returns at once, reporting whether
// it was called.
func decideNow(t *testing.T, g *Grant, req Request) (Outcome, bool) {
	t.Helper()
	forwarded := false
	out := g.Decide(t.Context(), req, func() error {
		forwarded = true
		return nil
	})
	return out, forwarded
}

func TestAGrantWithNoAskerRefuses(t *testing.T) {
	g := newTestGrant(t, nil, testLimits)
	out, forwarded := decideNow(t, g, change())
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: noAsker}, out)
	assert.True(t, g.lock.TryAcquire(1), "refused before any queue")
}

func TestABodyThatIsNotTextIsRefused(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	req := change()
	req.Sent.Body = []byte{0xff, 0xfe}
	out, forwarded := decideNow(t, g, req)
	assert.False(t, forwarded)
	assert.Equal(t, http.StatusForbidden, out.Status)
	assert.Equal(t, refusedUnshowable, out.Message)
	assert.Empty(t, a.asked, "never asked")
	assert.Empty(t, a.recorded, "never recorded")
}

func TestAQueryThatDoesNotParseIsRefused(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	req := change()
	req.Sent.Path = "/?a=%zz"
	out, forwarded := decideNow(t, g, req)
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: refusedQuery}, out)
	assert.Empty(t, a.asked)
}

func TestAFormBodyThatDoesNotParseIsRefused(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	req := change()
	req.Sent.ContentType = "Application/X-WWW-Form-Urlencoded; charset=utf-8"
	req.Sent.Body = []byte("Action=TerminateInstances&%zz=1")
	out, forwarded := decideNow(t, g, req)
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: refusedForm}, out)
	assert.Empty(t, a.asked)

	// A body of another type is drawn as sent, whatever it holds.
	req.Sent.ContentType = "text/plain"
	a.answers <- answer{approved: true}
	_, forwarded = decideNow(t, g, req)
	assert.True(t, forwarded)
}

func TestAnUnshownBodyAsksWithItsSentence(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	req := change()
	req.Sent.Body, req.Sent.Unshown = nil, "A Git pack of the commits pushed."
	a.answers <- answer{approved: true}
	out, forwarded := decideNow(t, g, req)
	assert.True(t, forwarded)
	assert.True(t, out.Forwarded)
	asked := testutil.Recv(t, a.asked, "the request")
	assert.Equal(t, "A Git pack of the commits pushed.", asked.Sent.Unshown)
}

func TestOnlyAnApprovalForwards(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	a.answers <- answer{approved: true}
	failed := errors.New("upstream")
	out := g.Decide(t.Context(), change(), func() error { return failed })
	assert.Equal(t, Outcome{Forwarded: true, Err: failed}, out)
	assert.Equal(t, change().Sent, testutil.Recv(t, a.asked, "the request").Sent)
}

func TestADenialForwardsNothing(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	a.answers <- answer{approved: false}
	out, forwarded := decideNow(t, g, change())
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: refusedDenied}, out)
}

func TestAFailedAnswerForwardsNothing(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	a.answers <- answer{err: errors.New("disk full")}
	out, forwarded := decideNow(t, g, change())
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: refusedUnrecorded}, out)
}

func TestAWaitThatEndsForwardsNothing(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	ctx, cancel := context.WithCancel(t.Context())
	d := decide(ctx, g, change())
	testutil.Recv(t, a.asked, "the request")
	cancel()
	out := testutil.Recv(t, d.outcome, "the outcome")
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: refusedUnanswered}, out)
	assert.Empty(t, d.forwards)
}

// A session that never asks refuses, records the refusal, and forwards
// nothing; a record that fails says so.
func TestAFailedRecordForwardsNothing(t *testing.T) {
	a := newFakeAsker()
	g := NewGrant(permissions.AWS, session.Session{Kind: session.Chat, NoPrompts: true}, a, noAsker, testLimits)
	t.Cleanup(g.End)
	out, forwarded := decideNow(t, g, change())
	assert.False(t, forwarded)
	assert.Equal(t, http.StatusForbidden, out.Status)
	assert.Equal(t, "kstack: AWS: call ec2:TerminateInstances is not allowed: this session never asks: a provider's request always asks", out.Message)
	rec := testutil.Recv(t, a.recorded, "the record")
	assert.Equal(t, permissions.Denied, rec.d)
	assert.Empty(t, a.asked)

	a.recordErr = errors.New("disk full")
	out, forwarded = decideNow(t, g, change())
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusForbidden, Message: refusedUnrecorded}, out)
}

func TestNoSecretDataRefusesADataRead(t *testing.T) {
	a := newFakeAsker()
	g := NewGrant(permissions.AWS, session.Session{Kind: session.Chat, NoSecretData: true}, a, noAsker, testLimits)
	t.Cleanup(g.End)
	out, forwarded := decideNow(t, g, dataRead())
	assert.False(t, forwarded)
	assert.Equal(t, http.StatusForbidden, out.Status)
	assert.Empty(t, a.asked)
}

// An action a classifier left as the cluster's, or named another provider
// for, is decided, recorded and asked as the grant's, carrying its class and
// summary and no field of a cluster's object.
func TestDecideKeepsAProvidersFieldsAlone(t *testing.T) {
	want := permissions.Action{Target: permissions.AWS, Class: permissions.UpstreamWrite, Summary: "AWS: call ec2:TerminateInstances"}
	for _, target := range []permissions.Target{permissions.Cluster, permissions.GitHub} {
		a := newFakeAsker()
		g := newTestGrant(t, a, testLimits)
		req := change()
		req.Action = permissions.Action{
			Target: target, Class: permissions.UpstreamWrite, Summary: want.Summary,
			Context: "prod", Namespace: "dev", Verb: "delete", Group: "ec2", Kind: "instances", Name: "i-1", DryRun: true,
		}
		a.answers <- answer{approved: true}
		decideNow(t, g, req)
		assert.Equal(t, want, testutil.Recv(t, a.asked, "the request").Action)

		refusing := NewGrant(permissions.AWS, session.Session{NoPrompts: true}, a, noAsker, testLimits)
		t.Cleanup(refusing.End)
		decideNow(t, refusing, req)
		assert.Equal(t, want, testutil.Recv(t, a.recorded, "the record").req.Action)
	}
}

func TestRequestsAskOneAtATime(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	first := decide(t.Context(), g, change())
	testutil.Recv(t, a.asked, "the first request")
	second := decide(t.Context(), g, change())
	// A negative assertion: the second waits on the lock, so nothing arrives.
	testutil.NoRecv(t, a.asked, 50*time.Millisecond, "a second request while the first waits")
	a.answers <- answer{approved: false}
	testutil.Recv(t, first.outcome, "the first outcome")
	testutil.Recv(t, a.asked, "the second request")
	a.answers <- answer{approved: false}
	testutil.Recv(t, second.outcome, "the second outcome")
}

func TestAFullQueueIs429(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	first := decide(t.Context(), g, change())
	testutil.Recv(t, a.asked, "the first request")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range maxQueued {
		decide(ctx, g, change())
	}
	// Each queued request holds a waiter slot once its goroutine runs.
	require.Eventually(t, func() bool {
		if g.waiters.TryAcquire(1) {
			g.waiters.Release(1)
			return false
		}
		return true
	}, testutil.Timeout, time.Millisecond)
	out, forwarded := decideNow(t, g, change())
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusTooManyRequests, Message: refusedQueued}, out)
	a.answers <- answer{approved: false}
	testutil.Recv(t, first.outcome, "the first outcome")
}

func TestADataReadForwardsOutsideTheLock(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	read := decide(t.Context(), g, dataRead())
	testutil.Recv(t, a.asked, "the read")
	a.answers <- answer{approved: true}
	testutil.Recv(t, read.forwards, "the read's forward")
	// The read is still forwarding, and a second request is decided.
	next := decide(t.Context(), g, change())
	testutil.Recv(t, a.asked, "the second request")
	a.answers <- answer{approved: false}
	testutil.Recv(t, next.outcome, "the second outcome")
	close(read.release)
	assert.True(t, testutil.Recv(t, read.outcome, "the read's outcome").Forwarded)
}

func TestAChangeHoldsTheLockUntilItIsForwarded(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	first := decide(t.Context(), g, change())
	testutil.Recv(t, a.asked, "the change")
	a.answers <- answer{approved: true}
	testutil.Recv(t, first.forwards, "the change's forward")
	decide(t.Context(), g, change())
	// A negative assertion: the lock is held while the change forwards.
	testutil.NoRecv(t, a.asked, 50*time.Millisecond, "a second request while the first forwards")
	close(first.release)
	testutil.Recv(t, first.outcome, "the change's outcome")
	testutil.Recv(t, a.asked, "the second request")
	a.answers <- answer{approved: false}
}

func TestAWaitingRequestHoldsNoSlot(t *testing.T) {
	limits := testLimits
	limits.Open = 1
	a := newFakeAsker()
	g := newTestGrant(t, a, limits)
	d := decide(t.Context(), g, change())
	testutil.Recv(t, a.asked, "the request")
	release, status := g.Slot(t.Context())
	require.Zero(t, status, "the waiting request holds no slot")
	// Approved with none free, it is a 429 and forwards nothing.
	a.answers <- answer{approved: true}
	out := testutil.Recv(t, d.outcome, "the outcome")
	assert.Equal(t, http.StatusTooManyRequests, out.Status)
	assert.Empty(t, d.forwards)
	release()
}

func TestEndCancelsAWaitingRequest(t *testing.T) {
	a := newFakeAsker()
	g := newTestGrant(t, a, testLimits)
	r, done, ok := g.Enter(httptest.NewRequest(http.MethodGet, "/", nil))
	require.True(t, ok)
	d := decide(r.Context(), g, change())
	testutil.Recv(t, a.asked, "the request")
	g.End()
	out := testutil.Recv(t, d.outcome, "the outcome")
	assert.Equal(t, refusedUnanswered, out.Message)
	done()
	testutil.WaitReturn(t, g.Wait, "Wait to join the handler")
}

// An approval whose limiter wait fails forwards nothing: the grant or the
// client is gone.
func TestAnApprovalTheLimiterRefusesIsUnauthorized(t *testing.T) {
	limits := testLimits
	limits.QPS, limits.Burst = rate.Every(time.Hour), 1
	a := newFakeAsker()
	g := newTestGrant(t, a, limits)
	require.True(t, g.limiter.Allow(), "the burst's one token, spent")
	a.answers <- answer{approved: true}
	// The next token is an hour away, past the deadline, so the wait fails at
	// once; nothing waits out the minute.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	forwarded := false
	out := g.Decide(ctx, change(), func() error {
		forwarded = true
		return nil
	})
	assert.False(t, forwarded)
	assert.Equal(t, Outcome{Status: http.StatusUnauthorized, Message: unauthorized}, out)
}
