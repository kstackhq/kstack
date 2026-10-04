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
	"bufio"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/session"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// The server keeps the production bounds.
func TestNewServerSetsTheBounds(t *testing.T) {
	srv := NewServer(NewGrant(nil, session.Session{}, nil, noAsker, 1, 1, 1))
	assert.Equal(t, 10*time.Second, srv.ReadHeaderTimeout)
	assert.Equal(t, 60*time.Second, srv.IdleTimeout)
	assert.Equal(t, 64<<10, srv.MaxHeaderBytes)
	assert.Zero(t, srv.ReadTimeout)
	assert.Zero(t, srv.WriteTimeout)
}

// A connection that never sends its headers, or sits idle after a response,
// is closed; headers past the bound answer 431; and a watch quiet past the
// idle bound stays open, since it is not idle while it streams.
func TestTheServerBoundsItsConnections(t *testing.T) {
	const bound = 50 * time.Millisecond
	api, _, _ := watchServer(t)
	g := NewGrant(api.upstream(), session.Session{}, nil, noAsker, 1000, 1000, 32)
	t.Cleanup(g.End)
	srv := newServer(g, bound, bound)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()

	closed := func(c net.Conn) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(io.Discard, c)
		}()
		return done
	}

	silent, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer silent.Close()
	testutil.Wait(t, closed(silent), "a connection sending no headers to close")

	idle, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer idle.Close()
	_, err = io.WriteString(idle, "GET /api HTTP/1.1\r\nHost: "+Host+"\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(idle), nil)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	testutil.Wait(t, closed(idle), "an idle connection to close")

	pipes := newPipeListener()
	go func() { _ = srv.Serve(pipes) }()
	big := pipes.dial(t)
	defer big.Close()
	// The server answers once it has read past the bound and never reads the
	// rest, so the write ends only when the connection closes.
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		_, _ = io.WriteString(big, "GET /api HTTP/1.1\r\nHost: "+Host+"\r\nX-Big: "+strings.Repeat("x", 80<<10)+"\r\n\r\n")
	}()
	resp, err = http.ReadResponse(bufio.NewReader(big), nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, resp.StatusCode)
	_ = big.Close()
	testutil.Wait(t, wrote, "the oversized request's write to end")

	proxy, _ := url.Parse("http://" + addr)
	s := &served{g: g, client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}}
	lines, _ := s.openWatch(t)
	// A negative assertion: nothing to wait for, so a window of several idle
	// bounds, failing the moment the watch closes.
	select {
	case <-lines:
		t.Fatal("a quiet watch was closed")
	case <-time.After(5 * bound):
	}
}

// pipeListener serves net.Pipe connections. Over TCP, a server closing with
// request bytes unread sends a reset, and the reset can discard a response
// the client has not read yet; a pipe hands the response over before the
// server's write returns.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) dial(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client
	case <-l.done:
		t.Fatal("dialed a closed listener")
		return nil
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
