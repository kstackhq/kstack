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

// Package runproxy is what every cloud platform's proxy in a sandboxed run
// shares: the server the run's door is served on, and a grant per cloud
// cloud platform, which holds the run's token for it, its life, its limits and the
// one way its requests are put to the user. It knows no cloud platform and no
// tool.
package runproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
)

// maxBody is the largest request body a grant reads: the most a user can be
// asked to read.
const maxBody = 1 << 20

// Limits bound one grant.
type Limits struct {
	QPS    rate.Limit
	Burst  int
	Open   int // requests forwarded at once
	Bodies int // request bodies held at once
	// Challenges answers a request with no token 407, for a client that sends
	// proxy credentials only once asked.
	Challenges bool
}

// Grant is one run's access to one cloud platform, dead once End is called.
type Grant struct {
	target  permissions.Target
	session session.Session
	// asker puts a request to the user; nil refuses every request that would
	// ask with refusal.
	asker   Asker
	refusal string
	token   string
	// challenges answers a request with no token 407 (Limits.Challenges).
	challenges bool
	limiter    *rate.Limiter
	open       *semaphore.Weighted
	bodies     *semaphore.Weighted
	// lock is held by the one request being decided, and by a change while it
	// is forwarded; waiters bounds the requests waiting for it.
	lock    *semaphore.Weighted
	waiters *semaphore.Weighted
	// ctx ends with the grant, and every request in flight with it.
	ctx context.Context
	end context.CancelFunc
	// mu orders End against a request joining handlers, so Wait sees every
	// handler a live grant let in.
	mu       sync.Mutex
	handlers sync.WaitGroup
}

// NewGrant is a live grant to target for the run of sess, with a fresh
// 256-bit token. Each request that asks goes to asker, or, with none, is
// refused with refusal. A grant for the cluster is a wiring mistake: the
// cluster's grant is kubeproxy's. So is a limit below one, which would refuse
// every request the user approves.
func NewGrant(target permissions.Target, sess session.Session, asker Asker, refusal string, limits Limits) *Grant {
	if target == permissions.Cluster {
		panic("runproxy: a grant names a cloud platform, never the cluster")
	}
	if limits.QPS <= 0 || limits.Burst < 1 || limits.Open < 1 || limits.Bodies < 1 {
		panic("runproxy: every limit admits at least one request")
	}
	var b [32]byte
	_, _ = rand.Read(b[:])
	ctx, end := context.WithCancel(context.Background())
	return &Grant{
		target: target, session: sess, asker: asker, refusal: refusal,
		token: hex.EncodeToString(b[:]), challenges: limits.Challenges,
		limiter: rate.NewLimiter(limits.QPS, limits.Burst),
		open:    semaphore.NewWeighted(int64(limits.Open)),
		bodies:  semaphore.NewWeighted(int64(limits.Bodies)),
		lock:    semaphore.NewWeighted(1), waiters: semaphore.NewWeighted(maxQueued),
		ctx: ctx, end: end,
	}
}

// Target is the cloud platform the grant reaches.
func (g *Grant) Target() permissions.Target { return g.target }

// Token is the run's credential for this grant: 256 bits, hex. It opens the
// grant alone, until the run ends.
func (g *Grant) Token() string { return g.token }

// Holds is whether candidate is the grant's token, compared in constant time;
// false once the grant has ended.
func (g *Grant) Holds(candidate string) bool {
	return g.ctx.Err() == nil && subtle.ConstantTimeCompare([]byte(candidate), []byte(g.token)) == 1
}

// Challenge is the answer to a request with no token: 407 and
// Proxy-Authenticate when the grant challenges, else 401.
func (g *Grant) Challenge(w http.ResponseWriter) {
	if g.challenges {
		w.Header().Set("Proxy-Authenticate", `Basic realm="kstack"`)
		http.Error(w, unauthorized, http.StatusProxyAuthRequired)
		return
	}
	http.Error(w, unauthorized, http.StatusUnauthorized)
}

// Enter counts r in, unless the grant has ended: r's context then ends with
// the grant, and Wait joins the handler until done is called.
func (g *Grant) Enter(r *http.Request) (_ *http.Request, done func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx.Err() != nil {
		return r, func() {}, false
	}
	g.handlers.Add(1)
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(g.ctx, cancel)
	return r.WithContext(ctx), func() {
		stop()
		cancel()
		g.handlers.Done()
	}, true
}

// End kills the grant: Holds and Enter answer false from now on, and every
// request in flight is cancelled, one waiting on the user included. It does
// not wait for them; Wait does.
func (g *Grant) End() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.end()
}

// Wait returns once every handler Enter let in is done.
func (g *Grant) Wait() { g.handlers.Wait() }

// Body reads r's body in one of the grant's body slots, held until release is
// called, and puts it back as r's body. A request with no body takes nothing.
// A non-zero status is the refusal to answer: 403 for an encoded body, which
// no classifier reads, 429 with no slot free, 413 past maxBody, 400 for a body
// that ended early or whose read r's context cut short. release is always safe
// to call.
func (g *Grant) Body(w http.ResponseWriter, r *http.Request) (body []byte, release func(), status int) {
	release = func() {}
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil, release, 0
	}
	if r.Header.Get("Content-Encoding") != "" {
		return nil, release, http.StatusForbidden
	}
	if !g.bodies.TryAcquire(1) {
		return nil, release, http.StatusTooManyRequests
	}
	release = func() { g.bodies.Release(1) }
	// r's context ends with the grant, but that does not wake a read blocked
	// on the socket; a read deadline in the past does.
	stop := context.AfterFunc(r.Context(), func() {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	})
	defer stop()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return nil, release, http.StatusRequestEntityTooLarge
	}
	if err != nil {
		return nil, release, http.StatusBadRequest
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength, r.TransferEncoding = int64(len(body)), nil
	return body, release, 0
}

// Slot takes one of the grant's open-request slots, then waits on its
// limiter. A slot is held until release is called, a stream for as long as it
// is open, so nothing waits for one: none free is 429 with no Retry-After, so
// no client retries it. A wait ctx ends, or one past ctx's deadline, is 401:
// the grant or the client is gone.
func (g *Grant) Slot(ctx context.Context) (release func(), status int) {
	if !g.open.TryAcquire(1) {
		return func() {}, http.StatusTooManyRequests
	}
	if err := g.limiter.Wait(ctx); err != nil {
		g.open.Release(1)
		return func() {}, http.StatusUnauthorized
	}
	return func() { g.open.Release(1) }, 0
}

// Outbound is the headers a cloud platform forwards: those of in that allowed
// names, never Authorization or Proxy-Authorization.
func Outbound(in http.Header, allowed []string) http.Header {
	out := http.Header{}
	for _, name := range allowed {
		key := http.CanonicalHeaderKey(name)
		if key == "Authorization" || key == "Proxy-Authorization" {
			continue
		}
		if v, ok := in[key]; ok {
			out[key] = slices.Clone(v)
		}
	}
	return out
}
