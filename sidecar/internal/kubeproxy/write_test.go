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

package kubeproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/session"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// asked is one write put to a fakeAsker, and where its answer goes.
type asked struct {
	w      Write
	answer chan<- bool
	ctx    context.Context
}

// fakeAsker hands each write it is asked to the test, and waits for the
// test's answer or the write's context.
type fakeAsker chan asked

func (a fakeAsker) Ask(ctx context.Context, w Write) (bool, error) {
	answer := make(chan bool, 1)
	select {
	case a <- asked{w: w, answer: answer, ctx: ctx}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case ok := <-answer:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// next is the next write put to a, once it is asked.
func (a fakeAsker) next(t *testing.T) asked {
	t.Helper()
	return testutil.Recv(t, (<-chan asked)(a), "a write to be asked")
}

// serveAsking is a grant over up that puts each write to asker.
func serveAsking(t *testing.T, up Upstream, asker Asker) *served {
	t.Helper()
	return serveGrant(t, NewGrant(up, session.Session{}, asker, noAsker, 1000, 1000, 32))
}

// writeRequest is method on path with body sent as contentType, as a command
// in the sandbox sends it.
func (s *served) writeRequest(t *testing.T, method, path, contentType, body string) *http.Request {
	t.Helper()
	req := s.request(t, method, path, s.g.Token())
	if body != "" {
		req.Body = io.NopCloser(strings.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

// reply is a response and its body, read.
type reply struct {
	resp *http.Response
	body string
}

// sendAsync sends req on its own goroutine and hands back the reply.
func (s *served) sendAsync(t *testing.T, req *http.Request) <-chan reply {
	t.Helper()
	out := make(chan reply, 1)
	go func() {
		resp, err := s.client.Do(req)
		if err != nil {
			out <- reply{}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		out <- reply{resp: resp, body: string(body)}
	}()
	return out
}

const patchBody = `{"spec":{"replicas":3}}`

// A grant with no one to ask refuses every write with its own message, and a
// burst of them past the queue's bound reads that message, never the 429.
func TestAGrantWithNoAskerRefusesWrites(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	s := serve(t, api.upstream())

	var wg sync.WaitGroup
	for range maxQueuedWrites + 4 {
		wg.Go(func() {
			resp, body := s.send(t, "DELETE", "/api/v1/namespaces/web/pods/x", s.g.Token())
			assertForbidden(t, resp, body, noAsker)
		})
	}
	wg.Wait()
	assert.Empty(t, api.requests())
}

// A write reaches the cluster only once the asker approves it, and then as the
// bytes shown, with its length set.
func TestAWriteWaitsOnTheAsker(t *testing.T) {
	var got []byte
	var length int64
	api := newAPIServer(t, func(_ http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		length = r.ContentLength
	})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	done := s.sendAsync(t, s.writeRequest(t, "PATCH", "/apis/apps/v1/namespaces/web/deployments/x", "application/merge-patch+json", patchBody))
	a := asker.next(t)

	assert.Equal(t, "PATCH", a.w.Method)
	assert.Equal(t, patchBody, string(a.w.Body))
	assert.Empty(t, api.requests(), "nothing reaches the cluster while the user decides")
	a.answer <- true
	r := testutil.Recv(t, done, "the write to be answered")
	require.NotNil(t, r.resp)
	assert.Equal(t, http.StatusOK, r.resp.StatusCode)
	assert.Equal(t, patchBody, string(got))
	assert.Equal(t, int64(len(patchBody)), length)
}

// A denied write answers 403, and the cluster never sees it.
func TestADeniedWriteIsForbidden(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	done := s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/x", "", ""))
	asker.next(t).answer <- false
	r := testutil.Recv(t, done, "the write to be answered")

	require.NotNil(t, r.resp)
	assertForbidden(t, r.resp, r.body, refusedDenied)
	assert.Empty(t, api.requests())
}

// errAsker fails every write it is asked with err.
type errAsker struct{ err error }

func (a errAsker) Ask(context.Context, Write) (bool, error) { return false, a.err }

// A write the asker could not record answers 403 saying so, not that the user
// did not answer, and the cluster never sees it.
func TestAWriteThatCannotBeRecordedIsForbidden(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	s := serveAsking(t, api.upstream(), errAsker{errors.New("store refused")})

	r := testutil.Recv(t, s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/x", "", "")), "the write to be answered")

	require.NotNil(t, r.resp)
	assertForbidden(t, r.resp, r.body, refusedUnrecorded)
	assert.Empty(t, api.requests())
}

// A self review changes nothing, so it reaches the cluster with no one asked.
func TestASelfReviewPassesUnasked(t *testing.T) {
	api := newAPIServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	asker := make(fakeAsker)
	s := serveAsking(t, api.upstream(), asker)

	resp, _ := s.do(t, s.writeRequest(t, "POST", "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", "application/json", `{}`))

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Len(t, api.requests(), 1)
}

// quietWindow bounds the negative assertions below: a write that should not be
// asked yet has no event to wait for.
const quietWindow = 50 * time.Millisecond

// serveCountingWaiters is serveAsking with the writes waiting for the lock
// counted.
func serveCountingWaiters(t *testing.T, up Upstream, asker Asker) (*served, *atomic.Int32) {
	t.Helper()
	var waiting atomic.Int32
	g := NewGrant(up, session.Session{}, asker, noAsker, 1000, 1000, 32)
	g.waitersMoved = func(delta int) { waiting.Add(int32(delta)) }
	return serveGrant(t, g), &waiting
}

// A second write is asked only once the first has been forwarded, and a write
// waiting for its turn is dropped when its client gives up.
func TestWritesAskOneAtATime(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s, waiting := serveCountingWaiters(t, api.upstream(), asker)

	first := s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/a", "", ""))
	a := asker.next(t)
	ctx, giveUp := context.WithCancel(t.Context())
	gone := s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/b", "", "").WithContext(ctx))
	second := s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/c", "", ""))
	require.Eventually(t, func() bool { return waiting.Load() == 2 }, testutil.Timeout, time.Millisecond, "both to queue")
	giveUp()
	testutil.Recv(t, gone, "the write given up on to end")
	require.Eventually(t, func() bool { return waiting.Load() == 1 }, testutil.Timeout, time.Millisecond, "its place to free")

	testutil.NoRecv(t, (<-chan asked)(asker), quietWindow, "a second write asked while the first waits")
	a.answer <- true
	testutil.Recv(t, first, "the first write to be answered")
	b := asker.next(t)
	assert.Equal(t, "/api/v1/namespaces/web/pods/c", b.w.Path)
	b.answer <- true
	testutil.Recv(t, second, "the second write to be answered")
	require.Len(t, api.requests(), 2)
	assert.Equal(t, "/api/v1/namespaces/web/pods/a", api.requests()[0].URL.Path)
}

// signalReader fires read when it is read.
type signalReader struct {
	io.Reader
	read *testutil.Signal
}

func (r signalReader) Read(p []byte) (int, error) {
	r.read.Fire()
	return r.Reader.Read(p)
}

// With one write held and the queue full, one more answers 429 with no
// Retry-After; a queued write's body is not read until it holds the lock.
func TestQueuedWritesAreBounded(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s, waiting := serveCountingWaiters(t, api.upstream(), asker)

	s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/a", "", ""))
	held := asker.next(t)
	body := signalReader{Reader: strings.NewReader(patchBody), read: testutil.NewSignal()}
	req := httptest.NewRequest("PATCH", "http://"+Host+"/api/v1/namespaces/web/configmaps/x", body)
	req.Header.Set("Proxy-Authorization", proxyAuth(ProxyUser, s.g.Token()))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	go s.g.ServeHTTP(httptest.NewRecorder(), req)
	for range maxQueuedWrites - 1 {
		s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/b", "", ""))
	}
	require.Eventually(t, func() bool { return waiting.Load() == maxQueuedWrites }, testutil.Timeout, time.Millisecond, "the queue to fill")

	resp, reply := s.do(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/c", "", ""))
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Empty(t, resp.Header.Values("Retry-After"))
	assert.Equal(t, "kstack: too many changes are waiting on the user. Send one at a time.", statusOf(t, reply).Message)
	testutil.NoRecv(t, body.read.Chan(), quietWindow, "a queued write's body read")

	// The queued writes race each other into the lock's line, so any that took
	// the lock ahead of the PATCH are asked first: deny them until it holds it.
	held.answer <- true
	for {
		select {
		case <-body.read.Chan():
			return
		case a := <-asker:
			a.answer <- false
		case <-time.After(testutil.Timeout):
			t.Fatal("timed out waiting for the queued write's body to be read once it holds the lock")
		}
	}
}

// A write waiting on the user holds no slot: with a cap of one, a read passes
// while it waits.
func TestAWriteWaitingHoldsNoSlot(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveGrant(t, NewGrant(api.upstream(), session.Session{}, asker, noAsker, 1000, 1000, 1))

	s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/a", "", ""))
	asker.next(t)

	resp, _ := s.send(t, "GET", "/api/v1/pods", s.g.Token())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// writeCase is one write a test sends: its method, path, headers and body.
type writeCase struct {
	method, path, contentType, body string
	header                          http.Header
}

// sendWrite sends c, answering any ask with a denial, and answers the reply
// and the write it was asked as, nil when it was not asked.
func (s *served) sendWrite(t *testing.T, asker fakeAsker, c writeCase) (reply, *Write) {
	t.Helper()
	req := s.writeRequest(t, c.method, c.path, c.contentType, c.body)
	for k, vs := range c.header {
		req.Header[k] = vs
	}
	done := s.sendAsync(t, req)
	select {
	case a := <-asker:
		a.answer <- false
		return testutil.Recv(t, done, "the write to be answered"), &a.w
	case r := <-done:
		return r, nil
	}
}

// askedFor sends c and answers the write it was asked as, which it must be.
func (s *served) askedFor(t *testing.T, asker fakeAsker, c writeCase) Write {
	t.Helper()
	_, w := s.sendWrite(t, asker, c)
	require.NotNil(t, w, "the write to be asked")
	return *w
}

// A body the user could not read as it will be sent is refused unasked: one
// that is not JSON or YAML, one encoded, one not UTF-8, one past 1 MiB, one
// repeating a key in an object. A media type's parameters are not its type.
func TestAWriteThatCannotBeShownIsRefused(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)
	const path = "/api/v1/namespaces/web/configmaps"

	for name, c := range map[string]struct {
		c   writeCase
		why refusal
	}{
		"protobuf":     {writeCase{method: "POST", path: path, contentType: "application/vnd.kubernetes.protobuf", body: "k8s\x00"}, refusedUnshowable},
		"no type":      {writeCase{method: "POST", path: path, body: `{}`}, refusedUnshowable},
		"encoded":      {writeCase{method: "POST", path: path, contentType: "application/json", body: `{}`, header: http.Header{"Content-Encoding": {"gzip"}}}, refusedUnshowable},
		"not UTF-8":    {writeCase{method: "POST", path: path, contentType: "application/json", body: "{\"a\":\"\xff\"}"}, refusedUnshowable},
		"past 1 MiB":   {writeCase{method: "POST", path: path, contentType: "application/json", body: `{"a":"` + strings.Repeat("x", maxWriteBody) + `"}`}, refusedTooLarge},
		"with a body":  {writeCase{method: "DELETE", path: path + "/x", body: `{}`}, refusedUnshowable},
		"a pdf delete": {writeCase{method: "DELETE", path: path + "/x", contentType: "application/pdf", body: `{}`}, refusedUnshowable},
		"a namespace create repeating metadata": {writeCase{method: "POST", path: "/api/v1/namespaces", contentType: "application/json",
			body: `{"metadata":{"name":"blocked"},"metadata":{"labels":{"x":"y"}}}`}, refusedRepeatedKey},
		"a put repeating spec": {writeCase{method: "PUT", path: path + "/x", contentType: "application/json",
			body: `{"spec":{"replicas":0},"spec":{"template":{}}}`}, refusedRepeatedKey},
		"a patch repeating a nested key": {writeCase{method: "PATCH", path: path + "/x", contentType: "application/merge-patch+json",
			body: `{"spec":{"replicas":0,"replicas":3}}`}, refusedRepeatedKey},
		"an apply repeating spec": {writeCase{method: "PATCH", path: path + "/x", contentType: "application/apply-patch+yaml",
			body: "spec:\n  replicas: 0\nspec:\n  template: {}\n"}, refusedRepeatedKey},
		"a json patch repeating path": {writeCase{method: "PATCH", path: path + "/x", contentType: "application/json-patch+json",
			body: `[{"op":"replace","path":"/spec/replicas","path":"/metadata/labels/a","value":0}]`}, refusedRepeatedKey},
	} {
		r, write := s.sendWrite(t, asker, c.c)
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, write)
			require.NotNil(t, r.resp)
			assertForbidden(t, r.resp, r.body, c.why)
		})
	}
	assert.Empty(t, api.requests())

	for _, contentType := range []string{
		"application/json; charset=utf-8", "application/merge-patch+json", "application/strategic-merge-patch+json",
		"application/json-patch+json", "application/apply-patch+yaml",
	} {
		_, write := s.sendWrite(t, asker, writeCase{method: "PATCH", path: path + "/x", contentType: contentType, body: `{}`})
		assert.NotNil(t, write, contentType)
	}
	_, write := s.sendWrite(t, asker, writeCase{method: "PUT", path: path + "/x", contentType: "application/json",
		body: `{"metadata":{"name":"x"},"data":{"metadata":"y","name":"z"}}`})
	assert.NotNil(t, write, "a key repeated across objects is not repeated")
}

// A DELETE may carry no body, and asks.
func TestADeleteWithNoBodyAsks(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	_, write := s.sendWrite(t, asker, writeCase{method: "DELETE", path: "/api/v1/namespaces/web/pods/x"})

	assert.NotNil(t, write)
}

// A write carrying the sandbox's own redaction, as text or as its base64, is
// refused unasked: it would put the redacted value back in place of the real
// one. The check reads the values as the API server decodes them, so an escape
// that spells the mark differently does not hide it.
func TestAWriteCarryingRedactedIsRefused(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)
	const secret = "/api/v1/namespaces/web/secrets/x"

	for name, c := range map[string]writeCase{
		"as text":           {contentType: "application/merge-patch+json", body: `{"stringData":{"a":"[redacted]"}}`},
		"as base64":         {contentType: "application/merge-patch+json", body: `{"data":{"a":"W3JlZGFjdGVkXQ=="}}`},
		"escaped in JSON":   {contentType: "application/merge-patch+json", body: `{"stringData":{"a":"\u005bredacted\u005d"}}`},
		"base64, escaped":   {contentType: "application/json-patch+json", body: `[{"op":"replace","path":"/data/a","value":"W3JlZGFjdGVkXQ\u003d\u003d"}]`},
		"escaped in YAML":   {contentType: "application/apply-patch+yaml", body: "stringData:\n  a: \"\\x5bredacted\\x5d\"\n"},
		"in a YAML unicode": {contentType: "application/apply-patch+yaml", body: "stringData:\n  a: \"\\u005bredacted]\"\n"},
		"escaped in a key":  {contentType: "application/merge-patch+json", body: `{"stringData":{"\u005bredacted]":"x"}}`},
	} {
		c.method, c.path = "PATCH", secret
		r, write := s.sendWrite(t, asker, c)
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, write)
			assertForbidden(t, r.resp, r.body, refusedRedacted)
		})
	}
	assert.Empty(t, api.requests())
}

// A body its media type cannot decode is refused unasked, since what it would
// do cannot be read off it.
func TestAWriteThatDoesNotDecodeIsRefused(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	for _, c := range []writeCase{
		{contentType: "application/merge-patch+json", body: `{"a":`},
		{contentType: "application/merge-patch+json", body: `{"a":1} {"b":2}`},
		{contentType: "application/apply-patch+yaml", body: "a: [\n"},
	} {
		c.method, c.path = "PATCH", "/api/v1/namespaces/web/configmaps/x"
		r, write := s.sendWrite(t, asker, c)
		assert.Nil(t, write, c.body)
		assertForbidden(t, r.resp, r.body, refusedUnshowable)
	}
	assert.Empty(t, api.requests())
}

// Any write of a helm release is refused unasked: a POST typed as one or not
// readable as an object, and a PUT, PATCH or DELETE of one by name, a patch
// carrying no type. A DELETE of the secrets collection names no release, and asks.
func TestAHelmReleaseWriteIsRefused(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)
	const secrets = "/api/v1/namespaces/web/secrets"
	const release = secrets + "/sh.helm.release.v1.x.v1"

	for name, c := range map[string]writeCase{
		"a post typed as a release": {method: "POST", path: secrets, contentType: "application/json", body: `{"kind":"Secret","type":"helm.sh/release.v1"}`},
		"a post of no object":       {method: "POST", path: secrets, contentType: "application/json", body: `[]`},
		"a post typed twice":        {method: "POST", path: secrets, contentType: "application/json", body: `{"type":"helm.sh/release.v1","Type":"Opaque"}`},
		"a post typed by a number":  {method: "POST", path: secrets, contentType: "application/json", body: `{"type":5}`},
		"a put":                     {method: "PUT", path: release, contentType: "application/json", body: `{"kind":"Secret"}`},
		"a patch":                   {method: "PATCH", path: release, contentType: "application/merge-patch+json", body: `{"data":{"release":"x"}}`},
		"a delete":                  {method: "DELETE", path: release},
	} {
		r, write := s.sendWrite(t, asker, c)
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, write)
			assertForbidden(t, r.resp, r.body, refusedHelm)
		})
	}
	assert.Empty(t, api.requests())

	_, write := s.sendWrite(t, asker, writeCase{method: "DELETE", path: secrets})
	assert.NotNil(t, write, "a delete of the collection")
	_, write = s.sendWrite(t, asker, writeCase{method: "POST", path: secrets, contentType: "application/json", body: `{"kind":"Secret","type":"Opaque"}`})
	assert.NotNil(t, write, "a post of another type")
}

// A write carries its path and query as sent, its media type as sent, and its
// subresource as the policy parsed the path.
func TestAWriteNamesItsSubresource(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	evict := s.askedFor(t, asker, writeCase{method: "POST", path: "/api/v1/namespaces/web/pods/x/eviction?fieldManager=kubectl", contentType: "application/json; charset=utf-8", body: `{}`})
	assert.Equal(t, Write{
		Method: "POST", Path: "/api/v1/namespaces/web/pods/x/eviction?fieldManager=kubectl", Subresource: "eviction",
		ContentType: "application/json; charset=utf-8", Body: []byte(`{}`),
	}, evict)
	assert.Equal(t, "finalize", s.askedFor(t, asker, writeCase{method: "PUT", path: "/api/v1/namespaces/web/finalize", contentType: "application/json", body: `{}`}).Subresource)
	assert.Empty(t, s.askedFor(t, asker, writeCase{method: "POST", path: "/api/v1/namespaces/web/pods", contentType: "application/json", body: `{}`}).Subresource)
}

// A dry run is read strictly: only a POST, PUT or PATCH whose every dryRun is
// All. A DELETE never is one, since the API server reads a delete's options
// from its body when it has one.
func TestADryRunAsks(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)
	const pods = "/api/v1/namespaces/web/pods"

	for _, c := range []writeCase{
		{method: "POST", path: pods + "?dryRun=All", contentType: "application/json", body: `{}`},
		{method: "PUT", path: pods + "/x?dryRun=All&dryRun=All", contentType: "application/json", body: `{}`},
		{method: "PATCH", path: pods + "/x?fieldManager=m&dryRun=All", contentType: "application/merge-patch+json", body: `{}`},
	} {
		assert.True(t, s.askedFor(t, asker, c).DryRun, c.method+" "+c.path)
	}
	for _, c := range []writeCase{
		{method: "POST", path: pods + "?dryrun=All", contentType: "application/json", body: `{}`},
		{method: "POST", path: pods + "?dryRun=All&dryRun=x", contentType: "application/json", body: `{}`},
		{method: "POST", path: pods + "?dryRun=", contentType: "application/json", body: `{}`},
		{method: "DELETE", path: pods + "/x?dryRun=All", contentType: "application/json", body: `{}`},
		{method: "DELETE", path: pods + "/x?dryRun=All"},
		{method: "DELETE", path: pods + "/x", contentType: "application/json", body: `{"dryRun":["All"]}`},
	} {
		assert.False(t, s.askedFor(t, asker, c).DryRun, c.method+" "+c.path+" "+c.body)
	}
	assert.Empty(t, api.requests())
}

// A write's answer on secrets is redacted, as a read's is.
func TestAWritesAnswerOnSecretsIsRedacted(t *testing.T) {
	api := answering(t, secretJSON)
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	done := s.sendAsync(t, s.writeRequest(t, "PATCH", "/api/v1/namespaces/web/secrets/db", "application/merge-patch+json", `{"metadata":{"labels":{"a":"b"}}}`))
	asker.next(t).answer <- true
	r := testutil.Recv(t, done, "the write to be answered")

	require.NotNil(t, r.resp)
	require.Equal(t, http.StatusOK, r.resp.StatusCode, r.body)
	assert.JSONEq(t, redactedSecretJSON, r.body)
}

// A write whose wait ends forwards nothing: its client dropping the connection
// ends the wait, and so does the grant's end.
func TestAWriteWhoseWaitEndsForwardsNothing(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	ctx, drop := context.WithCancel(t.Context())
	s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/a", "", "").WithContext(ctx))
	a := asker.next(t)
	drop()
	testutil.Wait(t, a.ctx.Done(), "the wait to end with its client")

	s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/b", "", ""))
	b := asker.next(t)
	s.g.End()
	testutil.Wait(t, b.ctx.Done(), "the wait to end with the grant")
	s.g.Wait()
	assert.Empty(t, api.requests())
}

// holdingAsker waits for its write's context to end, then for release, so a
// test can hold a handler open past End.
type holdingAsker struct {
	asked   chan struct{}
	release chan struct{}
}

func (a holdingAsker) Ask(ctx context.Context, _ Write) (bool, error) {
	close(a.asked)
	<-ctx.Done()
	<-a.release
	return false, ctx.Err()
}

// End cancels a waiting Ask and returns; Wait returns only once that Ask has.
// A request arriving after End answers 401.
func TestWaitJoinsTheHandlers(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := holdingAsker{asked: make(chan struct{}), release: make(chan struct{})}
	s := serveAsking(t, api.upstream(), asker)
	s.sendAsync(t, s.writeRequest(t, "DELETE", "/api/v1/namespaces/web/pods/a", "", ""))
	testutil.Wait(t, asker.asked, "the write to be asked")

	s.g.End()
	waited := make(chan struct{})
	go func() {
		s.g.Wait()
		close(waited)
	}()
	testutil.NoRecv(t, (<-chan struct{})(waited), quietWindow, "Wait returning while an Ask is open")
	close(asker.release)
	testutil.Wait(t, waited, "Wait to return once the Ask has")

	resp, _ := s.send(t, "GET", "/api", s.g.Token())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// failingReader fails every read, as a client that drops its body does.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// A write whose body cannot be read is never asked.
func TestAWriteWhoseBodyCannotBeReadIsNotAsked(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)
	req := httptest.NewRequest("PATCH", "http://"+Host+"/api/v1/namespaces/web/configmaps/x", failingReader{})
	req.Header.Set("Proxy-Authorization", proxyAuth(ProxyUser, s.g.Token()))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	s.g.ServeHTTP(httptest.NewRecorder(), req)

	testutil.NoRecv(t, (<-chan asked)(asker), quietWindow, "a write whose body could not be read asked")
	assert.Empty(t, api.requests())
}

// A write whose query does not parse is refused unasked: the pair that fails
// is dropped on the way to the API server, so a selector shown on the request
// would not reach it, and a delete of the matching pods would delete them all.
func TestAWriteWithAQueryThatDoesNotParseIsRefused(t *testing.T) {
	api := newAPIServer(t, func(http.ResponseWriter, *http.Request) {})
	asker := make(fakeAsker, 1)
	s := serveAsking(t, api.upstream(), asker)

	for _, path := range []string{
		"/api/v1/namespaces/web/pods?labelSelector=app%3Dfoo;",
		"/api/v1/namespaces/web/pods?labelSelector=app%zz",
	} {
		r, write := s.sendWrite(t, asker, writeCase{method: "DELETE", path: path})
		assert.Nil(t, write, path)
		assertForbidden(t, r.resp, r.body, refusedQuery)
	}
	assert.Empty(t, api.requests())
}
