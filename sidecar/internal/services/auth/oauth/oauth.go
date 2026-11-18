// Package oauth is the OAuth2/OIDC protocol layer for the sign-in flow: PKCE (S256), the
// code→token exchange, refresh, RFC 7009 revocation, ID-token verification. A LEAF —
// it must not import the parent auth package, so it owns the value types the protocol
// produces (Token, Identity), which the parent re-exports as aliases.
//
// The Client takes resolved endpoint URLs and is Hydra-agnostic. The loopback listener
// and the verifier/state generation live in the parent — not protocol concerns.
package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// expiryLeeway expires a token early, so one handed out stays valid through the request
// that uses it.
const expiryLeeway = 30 * time.Second

// Token is a grant's credential. Access and id tokens are short-lived and renewed via
// Refresh; an issuer that rotates refresh tokens hands a replacement back with each one.
// It keeps the id token, which the auth service needs to restore display identity at
// startup.
type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	IDToken      string    `json:"idToken"`
	Expiry       time.Time `json:"expiry"`
	// Scope is the scopes the issuer granted, space-separated, as its response
	// said them. Never stored.
	Scope string `json:"-"`
}

// Valid reports an access token present and outside the leeway window of expiry.
func (t Token) Valid(now time.Time) bool {
	return t.AccessToken != "" && now.Before(t.Expiry.Add(-expiryLeeway))
}

// Identity is the ID-token claims the app cares about, from a token whose signature Verify
// checked. The JSON tags decode straight from the OIDC claims.
type Identity struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	Name   string `json:"name"`
}

// UnverifiedIdentity is the same claims from a token nobody checked. A separate type so the
// compiler keeps an unchecked claim out of everywhere a verified one is expected; DisplayOnly
// is the only way across, and it is named for the one thing such a value may be used for.
type UnverifiedIdentity struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	Name   string `json:"name"`
}

// DisplayOnly hands the claims over to be shown. Nothing gates on them — the cloud re-verifies
// the access token per request.
func (u UnverifiedIdentity) DisplayOnly() Identity {
	return Identity(u)
}

// Config is the OAuth client's static configuration. Its endpoints are given, written out
// or read by Discover, so tests can point them at httptest servers.
type Config struct {
	ClientID      string
	AuthURL       string
	TokenURL      string
	JWKSURL       string
	RevocationURL string // Hydra's RFC 7009 token-revocation endpoint
	Issuer        string // expected "iss" of the ID token
	Scopes        []string
	// Resource is the RFC 8707 resource the tokens are for, sent on the authorize,
	// exchange and refresh requests when set.
	Resource string
}

// Client performs the OAuth flow and verifies ID tokens.
type Client struct {
	oc            *oauth2.Config
	verifier      *oidc.IDTokenVerifier
	revocationURL string
	resource      string
	httpc         *http.Client
}

// newHTTPClient is the issuer's client. It follows no redirect: a 307 or 308 would
// resend a form holding a code or a refresh token to wherever it points.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NewClient does no network I/O — the JWKS fetch is lazy, on first Verify. That fetch
// reuses the 15s-timeout HTTP client, because go-oidc ignores context cancellation for
// the key set, so only the client timeout bounds it.
func NewClient(cfg Config) *Client {
	oc := &oauth2.Config{
		ClientID: cfg.ClientID,
		Scopes:   cfg.Scopes,
		// A public client: client_id goes in the form, never as Basic auth.
		Endpoint: oauth2.Endpoint{
			AuthURL:   cfg.AuthURL,
			TokenURL:  cfg.TokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	c := &Client{
		oc:            oc,
		revocationURL: cfg.RevocationURL,
		resource:      cfg.Resource,
		httpc:         newHTTPClient(),
	}
	if cfg.JWKSURL != "" {
		// The base context is irrelevant — go-oidc ignores its cancel; ClientContext
		// is only how the timeout-bounded client reaches the lazy fetch.
		keySet := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), c.httpc), cfg.JWKSURL)
		c.verifier = oidc.NewVerifier(cfg.Issuer, keySet, &oidc.Config{ClientID: cfg.ClientID})
	}
	return c
}

// Revoke revokes a refresh token (RFC 7009), which also invalidates its grant's access
// tokens; the desktop public client needs only client_id. A nil error means "no longer
// usable" — the server also reports success for an already-invalid token.
func (c *Client) Revoke(ctx context.Context, token string) error {
	// Cap of the non-2xx body read for the error message.
	const maxErrBody = 4 << 10
	if c.revocationURL == "" {
		return fmt.Errorf("oauth: no revocation endpoint configured")
	}
	form := url.Values{
		"token":           {token},
		"token_type_hint": {"refresh_token"},
		"client_id":       {c.oc.ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.revocationURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		return fmt.Errorf("oauth: revoke failed %d: %s", resp.StatusCode, body)
	}
	return nil
}

// AuthCodeURL builds the PKCE (S256) authorize URL. redirect is the loopback listener's
// ephemeral address and MUST match the one passed to Exchange. extra is the caller's own
// parameters, nil for none.
func (c *Client) AuthCodeURL(state, verifier, redirect string, extra url.Values) string {
	opts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("redirect_uri", redirect),
	}
	if c.resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.resource))
	}
	for k := range extra {
		opts = append(opts, oauth2.SetAuthURLParam(k, extra.Get(k)))
	}
	return c.oc.AuthCodeURL(state, opts...)
}

// Exchange trades an authorization code + PKCE verifier for a Token; redirect must echo
// the one sent to AuthCodeURL, as OAuth requires.
func (c *Client) Exchange(ctx context.Context, code, verifier, redirect string) (Token, error) {
	opts := []oauth2.AuthCodeOption{
		oauth2.VerifierOption(verifier),
		oauth2.SetAuthURLParam("redirect_uri", redirect),
	}
	if c.resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.resource))
	}
	tok, err := c.oc.Exchange(context.WithValue(ctx, oauth2.HTTPClient, c.httpc), code, opts...)
	if retrieve, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		return Token{}, &TokenError{Status: retrieve.Response.StatusCode, Code: retrieve.ErrorCode}
	}
	if err != nil {
		return Token{}, err
	}
	return toToken(tok), nil
}

// TokenError is a token request the issuer refused: its status, and its error code
// (RFC 6749 §5.2) when the body named one.
type TokenError struct {
	Status int
	Code   string
}

func (e *TokenError) Error() string {
	return fmt.Sprintf("oauth: token endpoint refused the grant: %d %s", e.Status, e.Code)
}

// maxBody caps a reply of the issuer's that is decoded: a token response, the
// discovery document.
const maxBody = 1 << 20

// Refresh renews the tokens from a refresh token. It posts its own form, since
// x/oauth2's token source carries no extra parameter. An issuer that rotates refresh
// tokens answers a replacement, which the Token carries; one that does not leaves the
// refresh token as it was sent.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.oc.ClientID},
	}
	if c.resource != "" {
		form.Set("resource", c.resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oc.Endpoint.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		IDToken      string      `json:"id_token"`
		ExpiresIn    json.Number `json:"expires_in"`
		Scope        string      `json:"scope"`
		Error        string      `json:"error"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body)
	if resp.StatusCode/100 != 2 {
		return Token{}, &TokenError{Status: resp.StatusCode, Code: body.Error}
	}
	if decodeErr != nil || body.AccessToken == "" {
		return Token{}, fmt.Errorf("oauth: the token endpoint answered no access token")
	}
	tok := Token{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		IDToken:      body.IDToken,
		Scope:        body.Scope,
	}
	// Capped as x/oauth2 caps it, so a huge value cannot overflow into the past.
	if n, err := body.ExpiresIn.Int64(); err == nil && n > 0 {
		tok.Expiry = time.Now().Add(time.Duration(min(n, math.MaxInt32)) * time.Second)
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// Verify validates an ID token against the JWKS (signature, issuer, audience, expiry) and
// returns its identity claims.
func (c *Client) Verify(ctx context.Context, rawIDToken string) (Identity, error) {
	id, _, err := c.verify(ctx, rawIDToken)
	return id, err
}

// VerifyNonce is Verify, also checking the token carries the nonce the authorize
// request sent.
func (c *Client) VerifyNonce(ctx context.Context, rawIDToken, nonce string) (Identity, error) {
	id, idt, err := c.verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, err
	}
	if nonce == "" || idt.Nonce != nonce {
		return Identity{}, fmt.Errorf("oauth: the ID token's nonce does not match")
	}
	return id, nil
}

func (c *Client) verify(ctx context.Context, rawIDToken string) (Identity, *oidc.IDToken, error) {
	if c.verifier == nil {
		return Identity{}, nil, fmt.Errorf("oauth: no verifier configured (missing JWKSURL)")
	}
	idt, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, nil, err
	}
	var id Identity
	if err := idt.Claims(&id); err != nil {
		return Identity{}, nil, err
	}
	return id, idt, nil
}

// ParseIdentityUnverified decodes an ID token's claims WITHOUT checking its signature — only
// to restore display identity at startup from our own keychain. An authorization decision
// takes Verify, and the return type is what holds that line.
func ParseIdentityUnverified(rawIDToken string) (UnverifiedIdentity, error) {
	parts := strings.Split(rawIDToken, ".")
	if len(parts) != 3 {
		return UnverifiedIdentity{}, fmt.Errorf("oauth: malformed ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return UnverifiedIdentity{}, fmt.Errorf("oauth: decode ID token payload: %w", err)
	}
	var id UnverifiedIdentity
	if err := json.Unmarshal(payload, &id); err != nil {
		return UnverifiedIdentity{}, fmt.Errorf("oauth: unmarshal ID token claims: %w", err)
	}
	return id, nil
}

// toToken maps an oauth2.Token to ours, lifting id_token and scope out of the extra
// fields.
func toToken(t *oauth2.Token) Token {
	idToken, _ := t.Extra("id_token").(string)
	scope, _ := t.Extra("scope").(string)
	return Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		IDToken:      idToken,
		Expiry:       t.Expiry,
		Scope:        scope,
	}
}

// Endpoints is what an OpenID Provider's configuration document says of it.
type Endpoints struct {
	Issuer        string
	AuthURL       string
	TokenURL      string
	JWKSURL       string
	RevocationURL string
}

// Discover reads issuer's endpoints from its /.well-known/openid-configuration. The
// document's issuer must be issuer exactly, and every endpoint it names must be on the
// issuer's origin, so a document served from elsewhere cannot send a code or a token
// there. The authorization, token and JWKS endpoints are required.
func Discover(ctx context.Context, issuer string) (Endpoints, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return Endpoints{}, err
	}
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return Endpoints{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Endpoints{}, fmt.Errorf("oauth: discovery answered %d", resp.StatusCode)
	}
	var doc struct {
		Issuer        string `json:"issuer"`
		AuthURL       string `json:"authorization_endpoint"`
		TokenURL      string `json:"token_endpoint"`
		JWKSURL       string `json:"jwks_uri"`
		RevocationURL string `json:"revocation_endpoint"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return Endpoints{}, fmt.Errorf("oauth: unreadable discovery document: %w", err)
	}
	if doc.Issuer != issuer {
		return Endpoints{}, fmt.Errorf("oauth: discovery names another issuer")
	}
	origin, err := originOf(issuer)
	if err != nil { // coverage:ignore the request above parsed the same URL
		return Endpoints{}, err
	}
	for _, endpoint := range []string{doc.AuthURL, doc.TokenURL, doc.JWKSURL, doc.RevocationURL} {
		if endpoint == "" {
			continue
		}
		if o, err := originOf(endpoint); err != nil || o != origin {
			return Endpoints{}, fmt.Errorf("oauth: discovery names an endpoint off the issuer's origin")
		}
	}
	if doc.AuthURL == "" || doc.TokenURL == "" || doc.JWKSURL == "" {
		return Endpoints{}, fmt.Errorf("oauth: discovery is missing an endpoint")
	}
	return Endpoints(doc), nil
}

// originOf is rawURL's scheme and host, the host without case.
func originOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}
