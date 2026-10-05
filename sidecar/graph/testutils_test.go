package graph_test

// Test-only mocks shared by the resolver tests. The cluster fixtures are large
// enough to have a file of their own (cluster_testutils_test.go); the rest live
// here.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/amorey/gochan/watch"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/kstackhq/kstack/sidecar/internal/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/auth"
	"github.com/kstackhq/kstack/sidecar/internal/chatsvc"
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
)

// fakeAuth is a hand-written auth.Service for the resolver tests. The resolver
// depends on the auth.Service interface, so its tests fake that interface rather
// than standing up the real service (whose own package owns the real Login /
// persist / refresh coverage). It models only what the resolver uses:
// Current/Subscribe snapshots, and Login/Logout flipping the session and
// publishing the resulting State — mirroring the real service's derived state
// (Authenticated + Identity present only while signed in) and its latest-value,
// current-on-subscribe session stream.
type fakeAuth struct {
	mu       sync.Mutex
	signedIn bool
	identity auth.Identity // current identity (set while signed in)
	loginAs  auth.Identity // who a Login signs in as

	loginErr error // when set, Login fails synchronously (setup error) without signing in

	hub *watch.Hub[auth.State]
	tx  *watch.Sender[auth.State]
}

// newFakeAuth returns a signed-out fake whose Login signs in as loginAs (the
// resolver's login flow).
func newFakeAuth(loginAs auth.Identity) *fakeAuth {
	return newFakeAuthState(false, auth.Identity{}, loginAs)
}

// signedInFakeAuth returns a fake already signed in as id.
func signedInFakeAuth(id auth.Identity) *fakeAuth {
	return newFakeAuthState(true, id, id)
}

func newFakeAuthState(signedIn bool, identity, loginAs auth.Identity) *fakeAuth {
	f := &fakeAuth{signedIn: signedIn, identity: identity, loginAs: loginAs}
	f.hub = watch.New(f.stateLocked())
	f.tx = f.hub.Sender()
	return f
}

func (f *fakeAuth) Current(context.Context) (auth.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stateLocked(), nil
}

func (f *fakeAuth) StartLogin(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginErr != nil {
		return f.loginErr // synchronous setup failure: no sign-in, no publish
	}
	f.signedIn = true
	f.identity = f.loginAs
	f.publishLocked()
	return nil
}

func (f *fakeAuth) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signedIn = false
	f.identity = auth.Identity{}
	f.publishLocked()
	return nil
}

// TokenSource is unused by the graph resolvers (only the cloud client reads it),
// so the fake returns nil.
func (f *fakeAuth) TokenSource(context.Context) oauth2.TokenSource { return nil }

// Subscribe streams session State (current-on-subscribe, then changes) plus a
// cancel func — the same watch hub the real service publishes through.
func (f *fakeAuth) Subscribe() (<-chan auth.State, func()) {
	rx := f.hub.Receiver()
	return rx.Chan(), rx.Close
}

// stateLocked derives the public State from the session, mirroring the real
// grant: Identity is present only while signed in. Caller holds f.mu.
func (f *fakeAuth) stateLocked() auth.State {
	st := auth.State{Authenticated: f.signedIn}
	if f.signedIn {
		id := f.identity
		st.Identity = &id
	}
	return st
}

// publishLocked publishes the current State to subscribers. Caller holds f.mu.
func (f *fakeAuth) publishLocked() {
	f.tx.Send(f.stateLocked()) //nolint:errcheck // Send never blocks; a closed hub is a no-op
}

// gqlError is the half of a GraphQL error a client may branch on: never the
// message, only the code.
type gqlError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path"`
	Extensions map[string]any `json:"extensions"`
}

// mutation posts one operation and returns its data and coded errors.
func mutation(t *testing.T, srvURL, mutation string) (map[string]any, []gqlError) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": mutation})
	raw := postGQL(t, srvURL, string(body))

	var resp struct {
		Data   map[string]any
		Errors []gqlError
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	return resp.Data, resp.Errors
}

// refusingChat answers every send with err, for the refusals the real service gives
// only under conditions a resolver test cannot set up. The embedded Service is nil,
// so any other call panics, and a test leaning on it by accident fails on its code.
type refusingChat struct {
	chatsvc.Service
	err error
}

func (c refusingChat) Send(context.Context, *chatsvc.ChatID, chatsvc.Mode, apimeta.ClusterID, bool, bool, bool, string, string, string, string, string) (chatsvc.ChatMessage, error) {
	return chatsvc.ChatMessage{}, c.err
}

// testSecurity is a security store over a file not yet written: every default.
func testSecurity(t *testing.T) *securityconfig.Service {
	t.Helper()
	s, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	return securityconfig.NewService(s, nil, nil, "")
}
