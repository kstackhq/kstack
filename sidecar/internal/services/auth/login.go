package auth

import (
	"context"
	"net/url"

	"golang.org/x/oauth2"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oauth/browser"
)

// generateVerifier returns a PKCE code verifier; the protocol layer treats it as
// caller-supplied.
func generateVerifier() string { return oauth2.GenerateVerifier() }

// generateState returns the CSRF state the loopback callback must echo back.
func generateState() string { return oauth2.GenerateVerifier() }

// OAuthFlow is the subset of the oauth client the login flow drives; tests fake it to
// exercise the flow without Hydra.
type OAuthFlow interface {
	AuthCodeURL(state, verifier, redirect string, extra url.Values) string
	Exchange(ctx context.Context, code, verifier, redirect string) (Token, error)
	Verify(ctx context.Context, rawIDToken string) (Identity, error)
}

// Loopback is the redirect listener the login flow waits on; tests fake it with a canned
// code and no socket.
type Loopback interface {
	RedirectURL() string
	Wait(ctx context.Context) (string, error)
	Close() error
}

// LoopbackFunc builds a Loopback bound to the given OAuth state.
type LoopbackFunc func(state string) (Loopback, error)

// pendingLogin is the slow tail of a started login: wait for the redirect's code, then
// exchange + verify it.
type pendingLogin func(ctx context.Context) (Token, Identity, error)

// loginStarter runs the synchronous setup (bind loopback, open browser) and returns the
// tail; its error is the pre-browser failure surfaced to the login mutation. Production
// is (*loginFlow).start; the service holds it behind this func as a test seam.
type loginStarter func(ctx context.Context) (pendingLogin, error)

// loginFlow orchestrates the interactive desktop login over three seams: the OAuth
// operations, the loopback factory, and the browser opener. The latter two stay out of
// oauth.Client — an HTTP listener and a browser launch aren't protocol concerns — which
// also keeps the per-concern test split.
type loginFlow struct {
	oauth       OAuthFlow
	newLoopback LoopbackFunc
	openBrowser func(url string) error
}

// start generates the verifier + state, binds the loopback listener and opens the
// browser, returning the tail that finishes the round-trip. Setup failures surface
// synchronously (tearing the listener down); otherwise the tail closes it.
func (f *loginFlow) start(_ context.Context) (pendingLogin, error) {
	verifier := generateVerifier()
	state := generateState()

	lb, err := f.newLoopback(state)
	if err != nil {
		return nil, err
	}
	redirect := lb.RedirectURL()

	if err := f.openBrowser(f.oauth.AuthCodeURL(state, verifier, redirect, nil)); err != nil {
		lb.Close()
		return nil, err
	}

	return func(ctx context.Context) (Token, Identity, error) {
		defer lb.Close()

		code, err := lb.Wait(ctx)
		if err != nil {
			return Token{}, Identity{}, err
		}
		tok, err := f.oauth.Exchange(ctx, code, verifier, redirect)
		if err != nil {
			return Token{}, Identity{}, err
		}
		id, err := f.oauth.Verify(ctx, tok.IDToken)
		if err != nil {
			return Token{}, Identity{}, err
		}
		return tok, id, nil
	}, nil
}

// callbackPath is where Hydra's redirect lands on the loopback listener.
const callbackPath = "/oauth/callback"

// codeLoopback is a browser.Listener read for the code alone.
type codeLoopback struct{ *browser.Listener }

// Wait answers the redirect's code.
func (l codeLoopback) Wait(ctx context.Context) (string, error) {
	q, err := l.Listener.Wait(ctx)
	if err != nil {
		return "", err
	}
	return q.Get("code"), nil
}

// defaultNewLoopback binds the loopback listener behind the Loopback seam.
func defaultNewLoopback(state string) (Loopback, error) {
	l, err := browser.Listen(state, callbackPath)
	if err != nil { // coverage:ignore binding an ephemeral loopback port does not fail
		return nil, err
	}
	return codeLoopback{l}, nil
}
