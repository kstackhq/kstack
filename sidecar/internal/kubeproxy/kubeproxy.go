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

// Package kubeproxy is the cluster proxy a sandboxed run reaches its chat's
// cluster through: a grant per run, which answers a request carrying its token
// that the policy passes with the cluster connection's own credentials, a
// write once permissions.Decide allows it or its Asker approves it. It knows
// no tool and no cluster record.
package kubeproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/session"
)

// Host is the cluster's name in every run's kubeconfig. client-go sends each
// request to proxy-url in absolute form, so it arrives as the request's Host,
// which no browser page can send.
const Host = "cluster.kstack.invalid"

// ProxyUser is the user name in a run's proxy-url, whose password is the
// grant's token. client-go sends it as Proxy-Authorization on every request,
// where it applies a kubeconfig user's credentials only to a server reached
// over TLS; and no browser page can set a Proxy- header.
const ProxyUser = "kstack"

// maxBody is the largest request body the proxy forwards.
const maxBody = 64 << 10

// Upstream is the chat's cluster, as the grant reaches it per request. It is
// asked per request, so a rotated credential is picked up.
type Upstream interface {
	Endpoint(ctx context.Context) (Endpoint, error)
}

// Endpoint is one request's way to the cluster: where it goes, and the
// authenticated client that sends it.
type Endpoint struct {
	Base   *url.URL
	Client *http.Client
	// Done closes once Base and Client no longer reach the chat's cluster, and
	// a request still open then is cut. Nil never closes.
	Done <-chan struct{}
}

// The errors an Upstream answers, each a 503 with its own message. Any other
// is the cluster being unreachable.
var (
	// ErrIdentityMismatch is a connection that no longer vouches for the
	// chat's cluster.
	ErrIdentityMismatch = errors.New("kubeproxy: the connection is not the chat's cluster")
	// ErrNotIdentified is a cluster never identified, so no connection can
	// vouch for it.
	ErrNotIdentified = errors.New("kubeproxy: the cluster was never identified")
	// ErrNotConnectable is a cluster Kstack does not connect to.
	ErrNotConnectable = errors.New("kubeproxy: the cluster is not connected")
)

// Grant is one run's way to its chat's cluster, dead once End is called.
type Grant struct {
	up      Upstream
	session session.Session
	// context is the kube-context the grant was made for: the scope every
	// action is classified, and its mode and rules read, in.
	context string
	// asker puts a write or a Secret read to the user; nil refuses every write
	// with refusal and reads Secret data redacted.
	asker   Asker
	refusal string
	token   string
	// auth is the Proxy-Authorization a request carries: ProxyUser and token.
	auth    []byte
	limiter *rate.Limiter
	// openRequests bounds the requests forwarded at once.
	openRequests *semaphore.Weighted
	// writeLock is held by the one write or Secret read being decided, and by a
	// write while it is forwarded; writeWaiters bounds the requests waiting for
	// it.
	writeLock    *semaphore.Weighted
	writeWaiters *semaphore.Weighted
	// commandRules are the rules the user's command answers added, under the
	// write lock: the grant lives as long as the command, and so do they.
	commandRules []permissions.Rule
	// diffTimeout bounds a preview's two requests together.
	diffTimeout time.Duration
	// waitersMoved, when set, is told each write that starts or stops waiting for
	// writeLock, so a test can count them.
	waitersMoved func(delta int)
	// ctx ends with the grant, and every request in flight with it.
	ctx context.Context
	end context.CancelFunc
	// mu orders End against a request joining handlers, so Wait sees every
	// handler a live grant let in.
	mu       sync.Mutex
	handlers sync.WaitGroup
}

// NewGrant is a live grant over up for the run of sess in kubeContext: a fresh
// 256-bit token, a limiter of qps with burst, and at most maxInFlight requests
// open at once. Each write is decided and recorded through asker, or, with
// none, refused with refusal.
func NewGrant(up Upstream, sess session.Session, kubeContext string, asker Asker, refusal string, qps rate.Limit, burst, maxInFlight int) *Grant {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])
	ctx, end := context.WithCancel(context.Background())
	return &Grant{
		up: up, session: sess, context: kubeContext, asker: asker, refusal: refusal, token: token,
		auth:         []byte("Basic " + base64.StdEncoding.EncodeToString([]byte(ProxyUser+":"+token))),
		limiter:      rate.NewLimiter(qps, burst),
		openRequests: semaphore.NewWeighted(int64(maxInFlight)),
		writeLock:    semaphore.NewWeighted(1), writeWaiters: semaphore.NewWeighted(maxQueuedWrites),
		diffTimeout: defaultDiffTimeout,
		ctx:         ctx, end: end,
	}
}

// Session is the session of the run the grant serves: what its token maps to.
func (g *Grant) Session() session.Session { return g.session }

// Token is the password a request carries as ProxyUser's.
func (g *Grant) Token() string { return g.token }

// End kills the grant: every later request answers 401, and those in flight
// are cancelled. It does not wait for them; Wait does.
func (g *Grant) End() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.end()
}

// Wait returns once every request the grant let in has returned. A handler
// reading a body returns only once its connection closes, so the server is
// closed first.
func (g *Grant) Wait() { g.handlers.Wait() }

// enter counts a request in, unless the grant is dead.
func (g *Grant) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx.Err() != nil {
		return false
	}
	g.handlers.Add(1)
	return true
}

func (g *Grant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != Host || !g.authorized(r) || !g.enter() {
		writeStatus(w, http.StatusUnauthorized, "kstack: unauthorized")
		return
	}
	defer g.handlers.Done()
	// The request ends with the grant: a write waiting on the user, and a watch
	// open at End.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(g.ctx, cancel)()
	r = r.WithContext(ctx)
	p, why := decide(r)
	if why != pass {
		writeStatus(w, http.StatusForbidden, string(why))
		return
	}
	if isWrite(r, p) {
		g.serveWrite(w, r, p)
		return
	}
	// Read whole before anything is sent, so the cluster never sees a body cut
	// short. What passes is a self review, a few hundred bytes.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeStatus(w, http.StatusRequestEntityTooLarge, "kstack: the request body is too large")
		return
	}
	if p.onSecrets() && !metadataOnly(r) {
		g.serveSecretRead(w, r, p, body)
		return
	}
	g.forward(w, r, p, body, p.onSecrets())
}

// forward sends r, carrying body, to the cluster: in a slot, through the
// limiter, and with the grant's end cutting it short. With redact, the answer
// is rewritten so no Secret's data passes.
func (g *Grant) forward(w http.ResponseWriter, r *http.Request, p apiPath, body []byte, redact bool) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength, r.TransferEncoding = int64(len(body)), nil
	// A slot is held until the request ends, a watch for as long as it is open,
	// so nothing waits for one: it may never come free.
	if !g.openRequests.TryAcquire(1) {
		// No Retry-After, so client-go does not retry it.
		writeStatus(w, http.StatusTooManyRequests, "kstack: too many requests are open at once. Close a watch or a follow, then try again.")
		return
	}
	defer g.openRequests.Release(1)
	// The request ends with the connection too (below).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	// A burst waits rather than fails, as client-go's own limiter has it. Only
	// End or the client leaving ends the wait, and the client is gone then or
	// the grant dead.
	if err := g.limiter.Wait(ctx); err != nil {
		writeStatus(w, http.StatusUnauthorized, "kstack: unauthorized")
		return
	}
	conn, err := g.up.Endpoint(ctx)
	if err != nil {
		writeStatus(w, http.StatusServiceUnavailable, unavailable(err))
		return
	}
	// A watch or a follow outlives the check above, so it ends with the
	// connection.
	go func() {
		select {
		case <-conn.Done:
			cancel()
		case <-ctx.Done():
		}
	}()
	proxy := forward(conn.Base, conn.Client)
	if redact {
		rewriteSecrets(proxy, isWatch(p, r.URL.Query()))
	}
	proxy.ServeHTTP(w, r)
}

// unavailable is the message for an upstream's err: its own for each
// sentinel, and never the error's text.
func unavailable(err error) string {
	switch {
	case errors.Is(err, ErrIdentityMismatch):
		return "kstack: identity-mismatch: the kube-context no longer vouches for this chat's cluster"
	case errors.Is(err, ErrNotIdentified):
		return "kstack: Kstack has not identified this cluster, since it cannot read the kube-system namespace, so the sandbox cannot reach it"
	case errors.Is(err, ErrNotConnectable):
		return "kstack: Kstack does not connect to this cluster: it is disabled, being deleted, or has no kubeconfig credentials"
	}
	return "kstack: the cluster is not reachable"
}

// forward is the proxy to the connection at base, through its own client.
func forward(base *url.URL, client *http.Client) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			// The connection's transport adds the user's credential only to a
			// request that carries none.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Proxy-Authorization")
		},
		// The connection's own, so the exec plugin, TLS and the pool are the
		// mirror's.
		Transport: client.Transport,
		// A watch or a follow streams as it arrives.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, errUnredactable) {
				writeStatus(w, http.StatusBadGateway, unredactable)
				return
			}
			writeStatus(w, http.StatusBadGateway, "kstack: the cluster did not answer")
		},
	}
}

// authorized is whether r carries the grant's token as ProxyUser's password.
// Whether the grant is alive is enter's to say.
func (g *Grant) authorized(r *http.Request) bool {
	got := r.Header.Get("Proxy-Authorization")
	return subtle.ConstantTimeCompare([]byte(got), g.auth) == 1
}
