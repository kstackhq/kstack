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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
)

// testLimits leave every limit wide but the one a test narrows.
var testLimits = Limits{QPS: 1000, Burst: 1000, Open: 32, Bodies: 16}

const noAsker = "kstack: this sandbox reads AWS and changes nothing."

func newTestGrant(t *testing.T, asker Asker, limits Limits) *Grant {
	t.Helper()
	g := NewGrant(permissions.AWS, session.Session{Kind: session.Chat}, asker, noAsker, limits)
	t.Cleanup(g.End)
	return g
}

func TestOnlyTheGrantsTokenHolds(t *testing.T) {
	g := newTestGrant(t, nil, testLimits)
	other := newTestGrant(t, nil, testLimits)
	assert.Len(t, g.Token(), 64, "256 bits, hex")
	assert.NotEqual(t, g.Token(), other.Token())
	assert.True(t, g.Holds(g.Token()))
	assert.False(t, g.Holds(""))
	assert.False(t, g.Holds(other.Token()))
	g.End()
	assert.False(t, g.Holds(g.Token()), "an ended grant's token holds nothing")
}

func TestAGrantForTheClusterPanics(t *testing.T) {
	assert.Panics(t, func() { NewGrant(permissions.Cluster, session.Session{}, nil, noAsker, testLimits) })
}

// A limit below one would refuse every request the user approves.
func TestAGrantWithAZeroLimitPanics(t *testing.T) {
	for name, narrow := range map[string]func(*Limits){
		"qps":    func(l *Limits) { l.QPS = 0 },
		"burst":  func(l *Limits) { l.Burst = 0 },
		"open":   func(l *Limits) { l.Open = 0 },
		"bodies": func(l *Limits) { l.Bodies = 0 },
	} {
		limits := testLimits
		narrow(&limits)
		assert.Panics(t, func() { NewGrant(permissions.AWS, session.Session{}, nil, noAsker, limits) }, name)
	}
}

func TestAChallengingGrantAnswers407(t *testing.T) {
	limits := testLimits
	limits.Challenges = true
	w := httptest.NewRecorder()
	newTestGrant(t, nil, limits).Challenge(w)
	assert.Equal(t, http.StatusProxyAuthRequired, w.Code)
	assert.Equal(t, `Basic realm="kstack"`, w.Header().Get("Proxy-Authenticate"))

	w = httptest.NewRecorder()
	newTestGrant(t, nil, testLimits).Challenge(w)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, w.Header().Get("Proxy-Authenticate"))
	assert.Equal(t, "kstack: unauthorized\n", w.Body.String())
}

// Enter lets a request in while the grant lives, ends it with the grant, and
// Wait joins it.
func TestEndCancelsAnEnteredRequest(t *testing.T) {
	g := newTestGrant(t, nil, testLimits)
	r, done, ok := g.Enter(httptest.NewRequest(http.MethodGet, "/", nil))
	require.True(t, ok)
	g.End()
	testutil.Wait(t, r.Context().Done(), "the request to end with the grant")
	joined := make(chan struct{})
	go func() {
		g.Wait()
		close(joined)
	}()
	done()
	testutil.Wait(t, joined, "Wait to join the handler")
	_, done, ok = g.Enter(httptest.NewRequest(http.MethodGet, "/", nil))
	assert.False(t, ok, "an ended grant lets nothing in")
	done()
}

func TestOnlyAllowedHeadersAreForwarded(t *testing.T) {
	in := http.Header{
		"Content-Type":           {"application/json"},
		"X-Amz-Target":           {"ec2.Describe"},
		"X-Http-Method-Override": {"DELETE"},
		"Authorization":          {"Bearer token"},
		"Proxy-Authorization":    {"Basic abc"},
	}
	out := Outbound(in, []string{"Content-Type", "X-Amz-Target", "Authorization", "Proxy-Authorization"})
	assert.Equal(t, http.Header{
		"Content-Type": {"application/json"},
		"X-Amz-Target": {"ec2.Describe"},
	}, out)
}

// post is a request with body, and its media type.
func post(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestAnEncodedBodyIsRefused(t *testing.T) {
	limits := testLimits
	limits.Bodies = 1
	g := newTestGrant(t, nil, limits)
	r := post("gzipped")
	r.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	_, release, status := g.Body(w, r)
	assert.Equal(t, http.StatusForbidden, status)
	release()
	// No slot was taken: the one slot is still free.
	_, release, status = g.Body(httptest.NewRecorder(), post("{}"))
	assert.Zero(t, status)
	release()
}

func TestBodiesHeldAreCapped(t *testing.T) {
	limits := testLimits
	limits.Bodies = 2
	g := newTestGrant(t, nil, limits)
	held := make([]func(), 0, 2)
	for range 2 {
		body, release, status := g.Body(httptest.NewRecorder(), post(`{"n":1}`))
		require.Zero(t, status)
		assert.Equal(t, `{"n":1}`, string(body))
		held = append(held, release)
	}
	// None free: the body is not read.
	r := post("unread")
	_, release, status := g.Body(httptest.NewRecorder(), r)
	assert.Equal(t, http.StatusTooManyRequests, status)
	release()
	rest := make([]byte, 6)
	n, _ := r.Body.Read(rest)
	assert.Equal(t, "unread", string(rest[:n]), "a refused body is left unread")

	// A request with no body takes none.
	body, release, status := g.Body(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Zero(t, status)
	assert.Nil(t, body)
	release()

	held[0]()
	_, release, status = g.Body(httptest.NewRecorder(), post("{}"))
	assert.Zero(t, status, "a slot comes free when its request ends")
	release()
	held[1]()
}

func TestABodyPastTheCapIs413(t *testing.T) {
	g := newTestGrant(t, nil, testLimits)
	_, release, status := g.Body(httptest.NewRecorder(), post(strings.Repeat("x", maxBody+1)))
	defer release()
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
}

// The read body is the request's body again, for a provider that forwards r.
func TestABodyReadIsForwardable(t *testing.T) {
	g := newTestGrant(t, nil, testLimits)
	r := post(`{"a":1}`)
	_, release, status := g.Body(httptest.NewRecorder(), r)
	defer release()
	require.Zero(t, status)
	rest := make([]byte, 16)
	n, _ := r.Body.Read(rest)
	assert.Equal(t, `{"a":1}`, string(rest[:n]))
	assert.Equal(t, int64(7), r.ContentLength)
}

func TestOpenRequestsAreCapped(t *testing.T) {
	limits := testLimits
	limits.Open = 1
	g := newTestGrant(t, nil, limits)
	release, status := g.Slot(t.Context())
	require.Zero(t, status)
	none, status := g.Slot(t.Context())
	assert.Equal(t, http.StatusTooManyRequests, status)
	none()
	release()
	release, status = g.Slot(t.Context())
	assert.Zero(t, status)
	release()
}

// A slot whose limiter wait ends, as the grant does, is no slot.
func TestASlotWhoseWaitEndsIsUnauthorized(t *testing.T) {
	g := newTestGrant(t, nil, testLimits)
	g.End()
	release, status := g.Slot(g.ctx)
	assert.Equal(t, http.StatusUnauthorized, status)
	release()
	// The open slot it took is given back.
	assert.True(t, g.open.TryAcquire(int64(testLimits.Open)))
}

// failingBody ends before its length, as a client that leaves does.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("the client left") }

func TestABodyThatEndsEarlyIs400(t *testing.T) {
	limits := testLimits
	limits.Bodies = 1
	g := newTestGrant(t, nil, limits)
	r := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(failingBody{}))
	r.ContentLength = 10
	_, release, status := g.Body(httptest.NewRecorder(), r)
	assert.Equal(t, http.StatusBadRequest, status)
	release()
	_, release, status = g.Body(httptest.NewRecorder(), post("{}"))
	assert.Zero(t, status, "its slot was given back")
	release()
}

// A body whose client stalls partway is not read past the grant's end: End
// interrupts the read on the socket, the slot comes free, and Wait returns
// with the connection still open.
func TestEndInterruptsAStalledBodyRead(t *testing.T) {
	limits := testLimits
	limits.Bodies = 1
	g := newTestGrant(t, nil, limits)
	reading := testutil.NewSignal()
	statuses := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, done, ok := g.Enter(r)
		require.True(t, ok)
		defer done()
		reading.Fire()
		_, release, status := g.Body(w, r)
		release()
		statuses <- status
	}))
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Type: text/plain\r\nContent-Length: 100\r\n\r\nhalf")
	require.NoError(t, err)
	reading.Wait(t, "the body's read to start")

	g.End()
	assert.Equal(t, http.StatusBadRequest, testutil.Recv(t, statuses, "the stalled read's answer"))
	testutil.WaitReturn(t, g.Wait, "Wait, with the upload still open")
	assert.True(t, g.bodies.TryAcquire(1), "the body slot came free")
}
