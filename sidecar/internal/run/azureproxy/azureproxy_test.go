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

package azureproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/lib/version"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
)

const (
	sub2    = "00000000-0000-0000-0000-000000000002"
	tenantA = "0000000a-0000-0000-0000-000000000000"
	tenantB = "0000000b-0000-0000-0000-000000000000"
	door    = "127.0.0.1:6443"
)

var testAccount = &Account{Cloud: Cloud{Name: "AzureCloud"}, Subscriptions: []Subscription{
	{ID: sub, Name: "Pay-As-You-Go", TenantID: tenantA, IsDefault: true},
	{ID: sub2, Name: "Dev", TenantID: tenantB},
}}

// fakeTokens hands out tokens in order, one per call, and keeps what was
// asked and dropped.
type fakeTokens struct {
	mu      sync.Mutex
	next    []string
	err     error
	asked   []string // tenants
	dropped []string // tokens
}

func (f *fakeTokens) Token(_ context.Context, tenant string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, tenant)
	if f.err != nil {
		return "", f.err
	}
	tok := f.next[0]
	if len(f.next) > 1 {
		f.next = f.next[1:]
	}
	return tok, nil
}

func (f *fakeTokens) Drop(_, tok string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, tok)
}

// seen is one request the upstream received.
type seen struct {
	method, uri string
	header      http.Header
	body        string
}

// upstream is a fake Resource Manager over TLS, keeping each request, and
// answering with answer.
type upstream struct {
	*httptest.Server
	mu     sync.Mutex
	seen   []seen
	answer http.HandlerFunc
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value": []}`)
	}}
	u.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.seen = append(u.seen, seen{r.Method, r.URL.RequestURI(), r.Header.Clone(), string(b)})
		answer := u.answer
		u.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) requests() []seen {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seen{}, u.seen...)
}

func (u *upstream) setAnswer(h http.HandlerFunc) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.answer = h
}

// transport dials u whatever host a request names.
func (u *upstream) transport() http.RoundTripper { return testutil.DialAnywhere(u.Server) }

// fakeAsker hands each Ask to the test and waits for its answer.
type fakeAsker struct {
	asked    chan runproxy.Request
	answers  chan bool
	recorded chan runproxy.Request
}

func newFakeAsker() *fakeAsker {
	return &fakeAsker{asked: make(chan runproxy.Request, 8), answers: make(chan bool, 8), recorded: make(chan runproxy.Request, 8)}
}

func (a *fakeAsker) Ask(ctx context.Context, r runproxy.Request) (bool, error) {
	a.asked <- r
	select {
	case ok := <-a.answers:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (a *fakeAsker) Record(_ context.Context, r runproxy.Request, _ permissions.Decision, _ string) error {
	a.recorded <- r
	return nil
}

// harness is the proxy over a fake upstream, tokens and asker.
type harness struct {
	handler  http.Handler
	grant    *runproxy.Grant
	asker    *fakeAsker
	tokens   *fakeTokens
	upstream *upstream
}

func newHarness(t *testing.T, limits runproxy.Limits) *harness {
	t.Helper()
	if limits.QPS == 0 {
		limits = runproxy.Limits{QPS: 1000, Burst: 1000, Open: 8, Bodies: 8}
	}
	h := &harness{asker: newFakeAsker(), tokens: &fakeTokens{next: []string{"users-own-token"}}, upstream: newUpstream(t)}
	h.grant = runproxy.NewGrant(permissions.Azure, session.Session{Kind: session.Chat}, h.asker, "", limits)
	t.Cleanup(h.grant.End)
	h.handler = New(Config{Grant: h.grant, Port: 6443, Account: testAccount, Tokens: h.tokens, Transport: h.upstream.transport()})
	return h
}

// request is method on target at the door, carrying the grant's token as a
// bearer.
func (h *harness) request(method, target, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "https://"+door+target, nil)
	} else {
		r = httptest.NewRequest(method, "https://"+door+target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+h.grant.Token())
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// refusal is the message of a Resource Manager error answer.
func refusal(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), w.Body.String())
	assert.Equal(t, "KstackRefused", e.Error.Code)
	return e.Error.Message
}

func identityRequest(h *harness, resource, header string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://"+door+IdentityPath+"?api-version=2019-08-01&resource="+resource, nil)
	if header != "" {
		r.Header.Set("X-IDENTITY-HEADER", header)
	}
	return r
}

// The identity endpoint answers the grant's own token for Resource Manager,
// and dials nothing.
func TestTheIdentityEndpointAnswersTheGrantsToken(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	for _, resource := range []string{"https://management.core.windows.net/", "https://management.core.windows.net", "https://management.azure.com/"} {
		w := serve(h.handler, identityRequest(h, resource, h.grant.Token()))
		require.Equal(t, http.StatusOK, w.Code, resource)
		var answer map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &answer))
		assert.Equal(t, h.grant.Token(), answer["access_token"])
		assert.Equal(t, "Bearer", answer["token_type"])
		assert.Equal(t, resource, answer["resource"])
		assert.NotEmpty(t, answer["expires_on"])
	}

	assert.Equal(t, http.StatusUnauthorized, serve(h.handler, identityRequest(h, "https://management.core.windows.net/", "")).Code)
	w := serve(h.handler, identityRequest(h, "https://vault.azure.net", h.grant.Token()))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, refusal(t, w), "Resource Manager")
	assert.Empty(t, h.upstream.requests())
	assert.Empty(t, h.tokens.asked, "the user's token is never asked for")
}

// The bearer opens Resource Manager's paths, the identity header the
// identity endpoint, and neither the other.
func TestEachPathAcceptsItsOwnSlotAlone(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	bearer := identityRequest(h, "https://management.core.windows.net/", "")
	bearer.Header.Set("Authorization", "Bearer "+h.grant.Token())
	assert.Equal(t, http.StatusUnauthorized, serve(h.handler, bearer).Code)

	r := httptest.NewRequest(http.MethodGet, "https://"+door+"/subscriptions?api-version=1", nil)
	r.Header.Set("X-IDENTITY-HEADER", h.grant.Token())
	assert.Equal(t, http.StatusUnauthorized, serve(h.handler, r).Code)

	r = httptest.NewRequest(http.MethodGet, "https://"+door+"/subscriptions?api-version=1", nil)
	r.Header.Set("Authorization", h.grant.Token())
	assert.Equal(t, http.StatusUnauthorized, serve(h.handler, r).Code, "a token outside the Bearer scheme")
	assert.Empty(t, h.upstream.requests())
}

// A read goes to the cloud's host with the user's token for the tenant of
// the subscription it names, never the grant's.
func TestARequestCarriesTheUsersTokenUpstream(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	w := serve(h.handler, h.request(http.MethodGet, "/subscriptions/"+sub2+"/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01", ""))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"value": []}`, w.Body.String())

	got := h.upstream.requests()
	require.Len(t, got, 1)
	assert.Equal(t, "/subscriptions/"+sub2+"/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01", got[0].uri)
	assert.Equal(t, "Bearer users-own-token", got[0].header.Get("Authorization"))
	assert.NotContains(t, strings.Join(got[0].header.Values("Authorization"), " "), h.grant.Token())
	assert.Equal(t, []string{tenantB}, h.tokens.asked)

	// A path naming no subscription takes the default's tenant.
	serve(h.handler, h.request(http.MethodGet, "/subscriptions?api-version=2022-12-01", ""))
	assert.Equal(t, []string{tenantB, tenantA}, h.tokens.asked)
}

// The upstream sees the listed headers alone.
func TestOnlyTheListedHeadersAreForwarded(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	r := h.request(http.MethodGet, "/subscriptions/"+sub+"/resourcegroups?api-version=1", "")
	for name, value := range map[string]string{
		"Accept": "application/json", "Accept-Language": "en-US", "User-Agent": "AZURECLI/2.93.0",
		"x-ms-client-request-id": "c1", "x-ms-authorization-auxiliary": "Bearer other-tenant",
		"X-HTTP-Method-Override": "DELETE", "X-HTTP-Method": "DELETE", "X-Method-Override": "DELETE",
		"Accept-Encoding": "br", "CommandName": "group list", "Proxy-Authorization": "Basic x",
	} {
		r.Header.Set(name, value)
	}
	require.Equal(t, http.StatusOK, serve(h.handler, r).Code)

	got := h.upstream.requests()[0].header
	assert.Equal(t, "application/json", got.Get("Accept"))
	assert.Equal(t, "en-US", got.Get("Accept-Language"))
	assert.Equal(t, version.UserAgent()+" AZURECLI/2.93.0", got.Get("User-Agent"), "Kstack named ahead of the command")
	assert.Equal(t, "c1", got.Get("X-Ms-Client-Request-Id"))
	for _, name := range []string{
		"X-Ms-Authorization-Auxiliary", "X-Http-Method-Override", "X-Http-Method", "X-Method-Override",
		"Commandname", "Proxy-Authorization",
	} {
		assert.Empty(t, got.Values(name), name)
	}
	assert.NotEqual(t, "br", got.Get("Accept-Encoding"), "the client's encoding is not forwarded")
}

func TestAnotherSubscriptionIsRefused(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	w := serve(h.handler, h.request(http.MethodGet, "/subscriptions/00000000-0000-0000-0000-00000000000f/resourcegroups?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, refusal(t, w), "subscription")
	assert.Empty(t, h.upstream.requests())
	assert.Empty(t, h.tokens.asked)
}

// A token az cannot give is the one sign-in sentence.
func TestAFailedTokenIsOneFixedSentence(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	h.tokens.err = errors.New("AADSTS70043: refresh token expired for ana@example.com")
	w := serve(h.handler, h.request(http.MethodGet, "/subscriptions?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, refusedSignIn, refusal(t, w))
	assert.Empty(t, h.upstream.requests())
}

// An upstream 401 drops the token. A read is sent once more with a fresh
// An upstream 401 drops the token and answers the sign-in sentence, for a
// read and a change alike: az would hand back the token it has cached, so a
// retry would fail the same way. The next request asks for a token again.
func TestA401DropsTheToken(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	h.tokens.next = []string{"stale-token", "fresh-token"}
	h.upstream.setAnswer(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	w := serve(h.handler, h.request(http.MethodGet, "/subscriptions?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, refusedSignIn, refusal(t, w))
	assert.Equal(t, []string{"stale-token"}, h.tokens.dropped)
	assert.Len(t, h.upstream.requests(), 1, "a read is sent once")

	w = serve(h.handler, h.request(http.MethodGet, "/subscriptions?api-version=1", ""))
	assert.Equal(t, http.StatusOK, w.Code, "the next read asks for a token again")

	h.upstream.setAnswer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	done := serveAsync(h.handler, h.request(http.MethodDelete, compute+"?api-version=1", ""))
	testutil.Recv(t, h.asker.asked, "the delete")
	h.asker.answers <- true
	w = testutil.Recv(t, done, "the delete's answer")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, refusedSignIn, refusal(t, w))
	assert.Len(t, h.upstream.requests(), 3, "a change is sent once")
}

// A change on a path that names no subscription, a management group's, is
// refused: it reaches past the account the run's az shows. A read there passes.
func TestAChangeOutsideASubscriptionIsRefused(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	for _, target := range []string{
		"/providers/Microsoft.Management/managementGroups/mg/providers/Microsoft.Authorization/roleAssignments/ra",
		"/providers/Microsoft.Management/managementGroups/mg",
	} {
		w := serve(h.handler, h.request(http.MethodPut, target+"?api-version=1", "{}"))
		assert.Equal(t, http.StatusForbidden, w.Code, target)
		assert.Equal(t, refusedOutsideSubscription, refusal(t, w), target)
	}
	assert.Empty(t, h.asker.asked)
	assert.Empty(t, h.upstream.requests())

	w := serve(h.handler, h.request(http.MethodGet, "/providers/Microsoft.Management/managementGroups/mg?api-version=1", ""))
	assert.Equal(t, http.StatusOK, w.Code)
}

// With no login every request that holds the token, the identity endpoint's
// included, is the sign-in sentence.
func TestWithNoAccountEveryRequestIsTheSignInSentence(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	handler := New(Config{Grant: h.grant, Port: 6443, Tokens: h.tokens, Transport: h.upstream.transport()})
	for _, r := range []*http.Request{
		h.request(http.MethodGet, "/subscriptions?api-version=1", ""),
		identityRequest(h, "https://management.core.windows.net/", h.grant.Token()),
	} {
		w := serve(handler, r)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, refusedSignIn, refusal(t, w))
	}
	assert.Empty(t, h.tokens.asked)
}

// A request off the grammar is refused before the user's token is asked.
func TestAPathOffTheGrammarIsRefused(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	for _, target := range []string{"/batch?api-version=1", "/subscriptions/" + sub + "/a/b/c", "/subscriptions?a=%zz", "/subscriptions/%2e%2e/x"} {
		w := serve(h.handler, h.request(http.MethodGet, target, ""))
		assert.Equal(t, http.StatusForbidden, w.Code, target)
	}
	assert.Empty(t, h.upstream.requests())
	assert.Empty(t, h.tokens.asked)
}

// An action that answers a credential is refused: never asked, never
// recorded, never sent.
func TestACredentialActionIsRefusedUnasked(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	for _, target := range []string{
		rg + "/providers/Microsoft.Storage/storageAccounts/sa/listKeys",
		rg + "/providers/Microsoft.Storage/storageAccounts/sa/regenerateKey",
		aks + "/listClusterUserCredential",
		aks + "/accessProfiles/clusterAdmin/listCredential",
		rg + "/providers/Microsoft.Network/networkWatchers/nw/listTopology",
		site + "/publishxml",
		site + "/slots/staging/publishxml",
	} {
		w := serve(h.handler, h.request(http.MethodPost, target+"?api-version=1", `{}`))
		assert.Equal(t, http.StatusForbidden, w.Code, target)
	}
	w := serve(h.handler, h.request(http.MethodGet, rg+"/providers/Microsoft.DocumentDB/databaseAccounts/db/readonlykeys?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code, "a GET that answers keys")
	assert.Empty(t, h.asker.asked)
	assert.Empty(t, h.asker.recorded)
	assert.Empty(t, h.upstream.requests())
}

// serveAsync serves r on a goroutine and answers its recorder once done.
func serveAsync(h http.Handler, r *http.Request) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(h, r) }()
	return done
}

// A change is put to the user whole, and reaches Azure only once approved.
func TestAChangeAsksAndOnlyAnApprovalForwards(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	body := `{"location": "westus"}`
	for _, approve := range []bool{false, true} {
		done := serveAsync(h.handler, h.request(http.MethodPut, compute+"?api-version=2024-07-01", body))
		req := testutil.Recv(t, h.asker.asked, "the change")
		assert.Equal(t, permissions.UpstreamWrite, req.Action.Class)
		assert.Equal(t, "Azure: PUT Microsoft.Compute/virtualMachines/vm-1 in rg", req.Action.Summary)
		assert.Equal(t, runproxy.Sent{
			Host: "management.azure.com", Method: http.MethodPut, Path: compute + "?api-version=2024-07-01",
			ContentType: "application/json", Body: []byte(body),
			Details: []runproxy.Detail{
				{Label: "Subscription", Value: "Pay-As-You-Go (" + sub + ")"},
				{Label: "Resource group", Value: "rg"},
				{Label: "Resource", Value: "Microsoft.Compute/virtualMachines/vm-1"},
			},
		}, req.Sent)
		assert.Empty(t, h.upstream.requests(), "nothing is sent before the user decides")
		h.asker.answers <- approve
		w := testutil.Recv(t, done, "the answer")
		if !approve {
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Empty(t, h.upstream.requests())
			continue
		}
		assert.Equal(t, http.StatusOK, w.Code)
		got := h.upstream.requests()
		require.Len(t, got, 1)
		assert.Equal(t, http.MethodPut, got[0].method)
		assert.Equal(t, body, got[0].body)
	}
}

// Deleting a resource group is class 5 and names the subscription.
func TestAGroupDeleteIsClass5(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	done := serveAsync(h.handler, h.request(http.MethodDelete, rg+"?api-version=2024-11-01", ""))
	req := testutil.Recv(t, h.asker.asked, "the delete")
	assert.Equal(t, permissions.Destructive, req.Action.Class)
	assert.Equal(t, "Azure: delete resource group rg in Pay-As-You-Go", req.Action.Summary)
	h.asker.answers <- false
	testutil.Recv(t, done, "the answer")
}

func TestAConditionalHeaderIsDrawn(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	r := h.request(http.MethodPut, compute+"?api-version=1", `{}`)
	r.Header.Set("If-Match", `"etag-1"`)
	r.Header.Set("If-None-Match", "*")
	done := serveAsync(h.handler, r)
	req := testutil.Recv(t, h.asker.asked, "the change")
	assert.Contains(t, req.Sent.Details, runproxy.Detail{Label: "If-Match", Value: `"etag-1"`})
	assert.Contains(t, req.Sent.Details, runproxy.Detail{Label: "If-None-Match", Value: "*"})
	h.asker.answers <- true
	testutil.Recv(t, done, "the answer")
	got := h.upstream.requests()[0].header
	assert.Equal(t, `"etag-1"`, got.Get("If-Match"))
	assert.Equal(t, "*", got.Get("If-None-Match"))
}

// Each row of the secret-metadata list asks as a data read; a deployment's
// polling does not.
func TestSecretMetadataAsksAsADataRead(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	for target, summary := range map[string]string{
		site + "/config/appsettings/list?api-version=1":          "Azure: read app settings of Microsoft.Web/sites/shop/config/appsettings in rg",
		compute + "?api-version=1&$expand=userData":              "Azure: read user data of Microsoft.Compute/virtualMachines/vm-1 in rg",
		rg + "/providers/Microsoft.Resources/deployments/d1?a=1": "Azure: read a deployment of Microsoft.Resources/deployments/d1 in rg",
	} {
		method := http.MethodGet
		if strings.Contains(target, "/list") {
			method = http.MethodPost
		}
		done := serveAsync(h.handler, h.request(method, target, ""))
		req := testutil.Recv(t, h.asker.asked, target)
		assert.Equal(t, permissions.SecretRead, req.Action.Class, target)
		assert.Equal(t, summary, req.Action.Summary)
		h.asker.answers <- true
		assert.Equal(t, http.StatusOK, testutil.Recv(t, done, target).Code)
	}
	w := serve(h.handler, h.request(http.MethodGet, rg+"/providers/Microsoft.Resources/deployments/d1/operationStatuses/op?api-version=1", ""))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, h.asker.asked, "polling asks nothing")
}

// Every refusal and a full grant are 403, which az does not retry; a body
// past the cap is 413.
func TestARefusalAndAFullGrantAre403(t *testing.T) {
	h := newHarness(t, runproxy.Limits{QPS: 1000, Burst: 1000, Open: 1, Bodies: 16})

	release, status := h.grant.Slot(t.Context())
	require.Zero(t, status)
	w := serve(h.handler, h.request(http.MethodGet, "/subscriptions?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, runproxy.SlotRefusal(http.StatusTooManyRequests), refusal(t, w))
	release()

	// One change waits on the user and eight wait for it; the tenth finds
	// Decide's queue full, and is the only one answered.
	queued := make(chan *httptest.ResponseRecorder, 10)
	for range 10 {
		go func() { queued <- serve(h.handler, h.request(http.MethodPut, compute+"?api-version=1", "{}")) }()
	}
	w = testutil.Recv(t, queued, "the change past the queue")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, refusal(t, w), "waiting on the user")
	for range 9 {
		testutil.Recv(t, h.asker.asked, "a queued change")
		h.asker.answers <- false
		testutil.Recv(t, queued, "a denied change")
	}

	var releases []func()
	for range 16 {
		held := httptest.NewRequest(http.MethodPut, "https://"+door+"/", strings.NewReader("{}"))
		_, releaseBody, status := h.grant.Body(httptest.NewRecorder(), held)
		require.Zero(t, status)
		releases = append(releases, releaseBody)
	}
	w = serve(h.handler, h.request(http.MethodPut, compute+"?api-version=1", "{}"))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, runproxy.BodyRefusal(http.StatusTooManyRequests), refusal(t, w))
	for _, releaseBody := range releases {
		releaseBody()
	}

	w = serve(h.handler, h.request(http.MethodPut, compute+"?api-version=1", `"`+strings.Repeat("x", 1<<20)+`"`))
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	for _, target := range []string{"/batch", rg + "/providers/Microsoft.Storage/storageAccounts/sa/listKeys"} {
		w = serve(h.handler, h.request(http.MethodPost, target+"?api-version=1", "{}"))
		assert.Equal(t, http.StatusForbidden, w.Code, target)
	}
	h.upstream.Close()
	w = serve(h.handler, h.request(http.MethodGet, "/subscriptions?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code, "an upstream failure")
	assert.Equal(t, refusedUnreachable, refusal(t, w))
}

// A grant with no asker refuses a change in its own words, as 403.
func TestAChangeWithNoAskerIsRefused(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	g := runproxy.NewGrant(permissions.Azure, session.Session{Kind: session.Chat}, nil, "kstack: this sandbox reads Azure and changes nothing.", runproxy.Limits{QPS: 1, Burst: 1, Open: 1, Bodies: 1})
	t.Cleanup(g.End)
	handler := New(Config{Grant: g, Port: 6443, Account: testAccount, Tokens: h.tokens, Transport: h.upstream.transport()})
	r := h.request(http.MethodDelete, rg+"?api-version=1", "")
	r.Header.Set("Authorization", "Bearer "+g.Token())
	w := serve(handler, r)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "kstack: this sandbox reads Azure and changes nothing.", refusal(t, w))
	assert.Empty(t, h.upstream.requests())
}

// With no default subscription, a path naming none takes the first one's
// tenant.
func TestWithNoDefaultTheFirstSubscriptionIsTaken(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	handler := New(Config{Grant: h.grant, Port: 6443, Tokens: h.tokens, Transport: h.upstream.transport(),
		Account: &Account{Cloud: Cloud{Name: "AzureCloud"}, Subscriptions: []Subscription{{ID: sub2, Name: "Dev", TenantID: tenantB}}}})
	assert.Equal(t, http.StatusOK, serve(handler, h.request(http.MethodGet, "/subscriptions?api-version=1", "")).Code)
	assert.Equal(t, []string{tenantB}, h.tokens.asked)
}

// An encoded change is refused unread: the user could not read it.
func TestAnEncodedChangeIsRefused(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	r := h.request(http.MethodPut, compute+"?api-version=1", "{}")
	r.Header.Set("Content-Encoding", "gzip")
	w := serve(h.handler, r)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, runproxy.BodyRefusal(http.StatusForbidden), refusal(t, w))
	assert.Empty(t, h.asker.asked)
}

// An account with no subscription holds none a path could take.
func TestAnAccountWithNoSubscriptionRefuses(t *testing.T) {
	h := newHarness(t, runproxy.Limits{})
	handler := New(Config{Grant: h.grant, Port: 6443, Tokens: h.tokens, Transport: h.upstream.transport(),
		Account: &Account{Cloud: Cloud{Name: "AzureCloud"}}})
	w := serve(handler, h.request(http.MethodGet, "/subscriptions?api-version=1", ""))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, h.upstream.requests())
}
