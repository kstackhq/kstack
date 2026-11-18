package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// fakeOAuth is a stand-in for the oauth protocol client the login flow drives,
// recording what it was asked and returning canned tokens/identity.
type fakeOAuth struct {
	authURL  string
	token    Token
	identity Identity

	gotState    string
	gotVerifier string
	gotRedirect string

	exchangeErr error
	verifyErr   error
}

func (f *fakeOAuth) AuthCodeURL(state, verifier, redirect string, _ url.Values) string {
	f.gotState, f.gotVerifier, f.gotRedirect = state, verifier, redirect
	return f.authURL
}

func (f *fakeOAuth) Exchange(_ context.Context, _, _, _ string) (Token, error) {
	return f.token, f.exchangeErr
}

func (f *fakeOAuth) Verify(_ context.Context, _ string) (Identity, error) {
	return f.identity, f.verifyErr
}

// fakeLoopback is a stand-in for the loopback redirect listener, handing back a
// canned authorization code without binding a real socket.
type fakeLoopback struct {
	code    string
	closed  bool
	waitErr error
}

func (l *fakeLoopback) RedirectURL() string                  { return "http://127.0.0.1:55555/callback" }
func (l *fakeLoopback) Wait(context.Context) (string, error) { return l.code, l.waitErr }
func (l *fakeLoopback) Close() error                         { l.closed = true; return nil }

// start runs the synchronous setup — bind loopback, open the browser at the
// authorize URL (passing the loopback redirect + a non-empty state + PKCE
// verifier) — and returns a pendingLogin tail. Driving that tail waits for the
// loopback code, exchanges + verifies it, returns the minted token + verified
// identity, and tears the listener down.
func TestLoginFlowRunsEndToEnd(t *testing.T) {
	var openedURL string
	lb := &fakeLoopback{code: "auth-code-xyz"}
	oa := &fakeOAuth{
		authURL:  "https://oauth.kstack.sh/oauth2/auth?x=1",
		token:    Token{AccessToken: "a", RefreshToken: "r", IDToken: "id", Expiry: time.Now().Add(time.Hour)},
		identity: Identity{UserID: "u1", Email: "a@x.com", Name: "Ada"},
	}
	flow := &loginFlow{
		oauth:       oa,
		newLoopback: func(string) (Loopback, error) { return lb, nil },
		openBrowser: func(u string) error { openedURL = u; return nil },
	}

	// Setup is synchronous: it opens the browser and returns the tail.
	finish, err := flow.start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// The browser was opened at the authorize URL during the synchronous setup.
	if openedURL != oa.authURL {
		t.Fatalf("opened browser at %q, want %q", openedURL, oa.authURL)
	}
	// The authorize request carried the loopback redirect and a non-empty state +
	// PKCE verifier.
	if oa.gotRedirect != lb.RedirectURL() {
		t.Fatalf("authorize redirect = %q, want loopback %q", oa.gotRedirect, lb.RedirectURL())
	}
	if oa.gotState == "" || oa.gotVerifier == "" {
		t.Fatalf("authorize must carry state+verifier, got state=%q verifier=%q", oa.gotState, oa.gotVerifier)
	}
	// The listener is still open until the tail completes.
	if lb.closed {
		t.Fatal("loopback should stay open until the tail finishes")
	}

	tok, id, err := finish(context.Background())
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	// The minted token + verified identity are returned to the caller.
	if tok.RefreshToken != "r" {
		t.Fatalf("returned token = %+v, want refresh \"r\"", tok)
	}
	if id.UserID != "u1" || id.Email != "a@x.com" {
		t.Fatalf("returned identity = %+v", id)
	}
	// The loopback listener was torn down after the tail finished.
	if !lb.closed {
		t.Fatal("loopback was not closed after the flow")
	}
}

// A loopback build failure aborts setup before the browser is opened, surfacing
// the error synchronously from start.
func TestLoginFlowLoopbackBuildError(t *testing.T) {
	opened := false
	flow := &loginFlow{
		oauth:       &fakeOAuth{},
		newLoopback: func(string) (Loopback, error) { return nil, errors.New("bind failed") },
		openBrowser: func(string) error { opened = true; return nil },
	}
	if _, err := flow.start(context.Background()); err == nil {
		t.Fatal("want an error when the loopback can't be built")
	}
	if opened {
		t.Fatal("browser should not open when the loopback failed to bind")
	}
}

// A browser-open failure aborts setup synchronously and still tears the listener
// down (no tail is returned to finish it).
func TestLoginFlowBrowserError(t *testing.T) {
	lb := &fakeLoopback{code: "c"}
	flow := &loginFlow{
		oauth:       &fakeOAuth{authURL: "https://x/auth"},
		newLoopback: func(string) (Loopback, error) { return lb, nil },
		openBrowser: func(string) error { return errors.New("no browser") },
	}
	if _, err := flow.start(context.Background()); err == nil {
		t.Fatal("want an error when the browser can't open")
	}
	if !lb.closed {
		t.Fatal("loopback should be closed even when the browser open fails")
	}
}

const lbState = "test-state-123"

// The default loopback is a browser listener on Hydra's callback path, read for
// its code.
func TestDefaultLoopbackAnswersTheCode(t *testing.T) {
	lb, err := defaultNewLoopback(lbState)
	if err != nil {
		t.Fatalf("defaultNewLoopback: %v", err)
	}
	t.Cleanup(func() { _ = lb.Close() })
	u, err := url.Parse(lb.RedirectURL())
	if err != nil || u.Path != callbackPath {
		t.Fatalf("redirect url = %q, want …%s", lb.RedirectURL(), callbackPath)
	}
	u.RawQuery = url.Values{"state": {lbState}, "code": {"the-code"}}.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	code, err := lb.Wait(context.Background())
	if err != nil || code != "the-code" {
		t.Fatalf("Wait = (%q, %v), want (the-code, nil)", code, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lb.Wait(ctx); err == nil {
		t.Fatal("Wait should return the context error when cancelled")
	}
}

// Each step of the tail — the redirect, the code exchange, the ID-token check — reports
// its own failure and stops there, and the loopback is torn down whichever one fails.
func TestLoginFlowTailFailures(t *testing.T) {
	boom := errors.New("step failed")
	for _, tc := range []struct {
		name  string
		wire  func(*fakeOAuth, *fakeLoopback)
		after func(*testing.T, *fakeOAuth)
	}{
		{
			name: "redirect",
			wire: func(_ *fakeOAuth, lb *fakeLoopback) { lb.waitErr = boom },
			after: func(t *testing.T, o *fakeOAuth) {
				if o.gotRedirect == "" {
					t.Fatal("the authorize URL should still have been built")
				}
			},
		},
		{
			name: "exchange",
			wire: func(o *fakeOAuth, _ *fakeLoopback) { o.exchangeErr = boom },
		},
		{
			name: "verify",
			wire: func(o *fakeOAuth, _ *fakeLoopback) { o.verifyErr = boom },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oauth := &fakeOAuth{authURL: "https://issuer/authorize"}
			lb := &fakeLoopback{code: "the-code"}
			tc.wire(oauth, lb)
			flow := &loginFlow{
				oauth:       oauth,
				newLoopback: func(string) (Loopback, error) { return lb, nil },
				openBrowser: func(string) error { return nil },
			}

			finish, err := flow.start(context.Background())
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if _, _, err := finish(context.Background()); !errors.Is(err, boom) {
				t.Fatalf("finish err = %v, want %v", err, boom)
			}
			if !lb.closed {
				t.Fatal("the loopback must be torn down even when the tail fails")
			}
			if tc.after != nil {
				tc.after(t, oauth)
			}
		})
	}
}
