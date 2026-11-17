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

//go:build !windows

package bash

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// cloudTool is a tool on a fake sandbox whose build has cloud platforms, each
// switched as sw says.
func cloudTool(t *testing.T, sw *switches, platforms ...CloudPlatform) *Tool {
	t.Helper()
	tl := tool(t)
	tl.sandboxer = &fakeSandboxer{}
	tl.cloud = Cloud{Platforms: platforms, On: sw.On}
	return tl
}

// chatRuntime is a fresh chat with no cluster, of kind, asking through asker.
func chatRuntime(t *testing.T, kind session.Kind, asker tools.ActionAsker) tools.Runtime {
	return tools.Runtime{Dir: testChatDir(t), Session: session.Session{Kind: kind}, ActionAsker: asker}
}

// startRun makes a sandboxed run of rt, ended when the test ends.
func startRun(t *testing.T, tl *Tool, rt tools.Runtime, background bool) *sandboxedRun {
	t.Helper()
	r, err := tl.sandboxedRunFor(t.Context(), tl.sandboxer, rt, tools.WorkspacePath(rt.Dir), background, session.NoNetwork)
	require.NoError(t, err)
	t.Cleanup(r.end)
	return r
}

// overSocket is a client whose every request reaches the run's socket.
func overSocket(r *sandboxedRun) *http.Client {
	socket := r.dir.socket()
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}}
}

// send sends method to host on the run's socket carrying token, and answers
// the status and body.
func send(t *testing.T, r *sandboxedRun, method, host, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, "http://"+host+"/x", nil)
	require.NoError(t, err)
	req.Header.Set(tokenHeader, token)
	resp, err := overSocket(r).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestARunWithACloudPlatformAndNoClusterHasARelay(t *testing.T) {
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	tl := cloudTool(t, switchesOn(permissions.AWS), p)
	r := startRun(t, tl, chatRuntime(t, session.Chat, nil), false)

	assert.Equal(t, []sandbox.Relay{{Port: 6443, Socket: r.dir.socket()}}, r.run.Policy.Network.Relays)
	m := testutil.Recv(t, p.mounts, "the mount")
	assert.Equal(t, 6443, m.run.Port)
	assert.Equal(t, r.dir.path, m.run.Dir)
	assert.Equal(t, r.dir.tmp, m.run.TmpDir)
	code, body := send(t, r, http.MethodGet, "127.0.0.1:6443", m.grant.Token())
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "read aws", body)
	assert.NotContains(t, strings.Join(r.run.Env, "\n"), "KUBECONFIG=")
}

func TestARunWithNeitherHasNoRelay(t *testing.T) {
	boxer := &fakeSandboxer{}
	tl := cloudTool(t, switchesOn())
	tl.sandboxer = boxer
	r := startRun(t, tl, chatRuntime(t, session.Chat, nil), false)
	assert.Empty(t, r.run.Policy.Network.Relays)
	assert.Zero(t, boxer.portsAsked())
	assert.Nil(t, r.proxy)
}

// A cloud platform whose switch is off at the run's start, a monitor's run and
// the probe's get no grant at all.
func TestACloudPlatformThatIsOffHasNoRoute(t *testing.T) {
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	r := startRun(t, cloudTool(t, switchesOn(), p), chatRuntime(t, session.Chat, nil), false)
	assert.Empty(t, p.mounts)
	assert.Empty(t, r.run.Policy.Network.Relays)
}

func TestAMonitorHasNoPlatform(t *testing.T) {
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	startRun(t, cloudTool(t, switchesOn(permissions.AWS), p), chatRuntime(t, session.Monitor, nil), false)
	assert.Empty(t, p.mounts)
}

func TestTheProbeHasNoPlatform(t *testing.T) {
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	tl := cloudTool(t, switchesOn(permissions.AWS), p)
	// The probe's runtime names no kind: no turn of the user's started it.
	startRun(t, tl, tools.Runtime{Dir: testChatDir(t)}, false)
	assert.Empty(t, p.mounts)
}

// A subagent's run reaches a cloud platform as its parent's would; a background
// command's grant has no asker and refuses every change naming the cloud
// platform.
func TestABackgroundCommandsGrantHasNoAsker(t *testing.T) {
	p := newFakePlatform(permissions.GCP, "storage.gcp.kstack.invalid")
	tl := cloudTool(t, switchesOn(permissions.GCP), p)
	a := newAnsweringAsker()

	startRun(t, tl, chatRuntime(t, session.Subagent, a), false)
	assert.Equal(t, cloudAsker{a}, testutil.Recv(t, p.mounts, "the subagent's mount").run.Asker)

	r := startRun(t, tl, chatRuntime(t, session.Chat, a), true)
	m := testutil.Recv(t, p.mounts, "the background mount")
	assert.Nil(t, m.run.Asker)
	code, body := send(t, r, http.MethodPost, "storage.gcp.kstack.invalid", m.grant.Token())
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, body, "a background command cannot change Google Cloud")
	assert.Empty(t, a.asked)
}

// A run started while a switch was off never gets its route, whatever the
// switch does later.
func TestARunStartedWhileOffNeverGetsARoute(t *testing.T) {
	sw := switchesOn()
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	other := newFakePlatform(permissions.GCP, "storage.gcp.kstack.invalid")
	sw.set(permissions.GCP, true)
	r := startRun(t, cloudTool(t, sw, p, other), chatRuntime(t, session.Chat, nil), false)
	sw.set(permissions.AWS, true)
	code, _ := send(t, r, http.MethodGet, "127.0.0.1:6443", "anything")
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Empty(t, p.mounts)
}

// A change asked before the switch turns off is still forwarded once
// approved: it was already past the door.
func TestARequestWaitingOnTheUserOutlivesTheSwitch(t *testing.T) {
	sw := switchesOn(permissions.AWS)
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	a := newAnsweringAsker()
	r := startRun(t, cloudTool(t, sw, p), chatRuntime(t, session.Chat, a), false)
	token := testutil.Recv(t, p.mounts, "the mount").grant.Token()

	type answer struct {
		code int
		body string
	}
	answered := make(chan answer, 1)
	go func() {
		code, body := send(t, r, http.MethodPost, "127.0.0.1:6443", token)
		answered <- answer{code, body}
	}()
	asked := testutil.Recv(t, a.asked, "the change")
	assert.Equal(t, permissions.AWS, asked.Action.Target)
	sw.set(permissions.AWS, false)
	a.answers <- true
	got := testutil.Recv(t, answered, "the change's answer")
	assert.Equal(t, answer{http.StatusOK, "changed"}, got)
	code, _ := send(t, r, http.MethodGet, "127.0.0.1:6443", token)
	assert.Equal(t, http.StatusUnauthorized, code, "the next request meets the door")
}

// A mount that fails fails the run, ends every grant already mounted and
// leaves nothing behind.
func TestAFailedMountEndsTheEarlierOnes(t *testing.T) {
	first := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	failing := newFakePlatform(permissions.GCP, "storage.gcp.kstack.invalid")
	failing.err = errors.New("no login")
	tl := cloudTool(t, switchesOn(permissions.AWS, permissions.GCP), first, failing)
	var dir string
	tl.onRunDir = func(path string) { dir = path }
	rt := chatRuntime(t, session.Chat, nil)

	_, err := tl.sandboxedRunFor(t.Context(), tl.sandboxer, rt, tools.WorkspacePath(rt.Dir), false, session.NoNetwork)

	assert.ErrorContains(t, err, "no login")
	m := testutil.Recv(t, first.mounts, "the first mount")
	assert.False(t, m.grant.Holds(m.grant.Token()), "its grant has ended")
	assert.NotEmpty(t, dir)
	assert.NoDirExists(t, dir)
}

// A run whose cloud platforms' routes or variables clash fails as a failed
// mount does.
func TestAClashFailsTheRun(t *testing.T) {
	for name, second := range map[string]*fakePlatform{
		"a host": newFakePlatform(permissions.GCP, "127.0.0.1:6443"),
		"a variable": func() *fakePlatform {
			p := newFakePlatform(permissions.GCP, "storage.gcp.kstack.invalid")
			p.env = []string{"SHARED=b"}
			return p
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			first := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
			first.env = []string{"SHARED=a"}
			tl := cloudTool(t, switchesOn(permissions.AWS, permissions.GCP), first, second)
			rt := chatRuntime(t, session.Chat, nil)
			_, err := tl.sandboxedRunFor(t.Context(), tl.sandboxer, rt, tools.WorkspacePath(rt.Dir), false, session.NoNetwork)
			assert.Error(t, err)
			m := testutil.Recv(t, first.mounts, "the first mount")
			assert.False(t, m.grant.Holds(m.grant.Token()))
		})
	}
}

// A cloud platform's variables ride the run's environment.
func TestACloudPlatformsVariablesRideTheRun(t *testing.T) {
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	p.env = []string{"FAKE_ENDPOINT=http://127.0.0.1:6443"}
	r := startRun(t, cloudTool(t, switchesOn(permissions.AWS), p), chatRuntime(t, session.Chat, nil), false)
	assert.Contains(t, r.run.Env, "FAKE_ENDPOINT=http://127.0.0.1:6443")
}

// Once the run ends, every grant has ended, and a request waiting on the
// user was cancelled before end returned.
func TestTheRunEndsEveryGrant(t *testing.T) {
	p := newFakePlatform(permissions.AWS, "127.0.0.1:6443")
	a := newAnsweringAsker()
	tl := cloudTool(t, switchesOn(permissions.AWS), p)
	rt := chatRuntime(t, session.Chat, a)
	r, err := tl.sandboxedRunFor(t.Context(), tl.sandboxer, rt, tools.WorkspacePath(rt.Dir), false, session.NoNetwork)
	require.NoError(t, err)
	m := testutil.Recv(t, p.mounts, "the mount")

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:6443/x", nil)
		req.Header.Set(tokenHeader, m.grant.Token())
		if resp, err := overSocket(r).Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	testutil.Recv(t, a.asked, "the change")
	r.end()
	assert.False(t, m.grant.Holds(m.grant.Token()))
	// No answer was given, so the Ask returned on the grant's end, before
	// end did.
	assert.Empty(t, a.answers)
	testutil.Wait(t, sent, "the request to end")
	assert.Empty(t, a.asked, "nothing is asked once the run is over")
}
