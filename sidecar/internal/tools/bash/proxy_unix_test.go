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
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kstackhq/kstack/sidecar/internal/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/kubeproxy"
	"github.com/kstackhq/kstack/sidecar/internal/session"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// clientCommand is the subcommand that makes the test binary a cluster client
// reading the run's KUBECONFIG with client-go, as kubectl does.
const clientCommand = "kstack-test-client"

// runClient lists every pod and prints each name on a line. Given delete, it
// deletes the pod web/x and prints deleted. Given watch and a
// path, it leaves the run's process group, so the kill at the run's end misses
// it, opens a watch, writes the path once it is open, and waits for it to end.
// Given secret, it reads the Secret web/db and prints each value on a line.
func runClient(args []string) int {
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		fmt.Println(err)
		return 1
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	if len(args) == 1 && args[0] == "delete" {
		// JSON, as kubectl sends a change: the proxy refuses a protobuf body.
		cfg.ContentType = "application/json"
		if cs, err = kubernetes.NewForConfig(cfg); err != nil {
			fmt.Println(err)
			return 1
		}
		if err := cs.CoreV1().Pods("web").Delete(context.Background(), "x", metav1.DeleteOptions{}); err != nil {
			fmt.Println(err)
			return 1
		}
		fmt.Println("deleted")
		return 0
	}
	if len(args) == 1 && args[0] == "secret" {
		s, err := cs.CoreV1().Secrets("web").Get(context.Background(), "db", metav1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			return 1
		}
		for k, v := range s.Data {
			fmt.Printf("%s=%s\n", k, v)
		}
		return 0
	}
	if len(args) >= 2 && args[0] == "watch" {
		// It outlives the run's group, so only the grant's end can close the
		// watch. Seatbelt refuses setpgid, and cannot refuse posix_spawn's setsid.
		if len(args) == 2 && syscall.Setpgid(0, 0) != nil {
			return spawnDetached(args)
		}
		w, err := cs.CoreV1().Pods("").Watch(context.Background(), metav1.ListOptions{})
		if err != nil {
			_ = os.WriteFile(args[1], []byte(err.Error()+"\n"), 0)
			return 1
		}
		if err := os.WriteFile(args[1], []byte("open\n"), 0); err != nil {
			return 1
		}
		for range w.ResultChan() {
		}
		return 0
	}
	pods, err := cs.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Println(err)
		return 1
	}
	for _, p := range pods.Items {
		fmt.Println(p.Name)
	}
	return 0
}

// spawnDetached starts the watch client again in a session of its own,
// through python3's posix_spawn, and exits. A failure is written to the
// client's FIFO, so the run reading it does not wait forever.
func spawnDetached(args []string) int {
	spawn := exec.Command("/usr/bin/python3", "-c",
		"import os, sys; os.posix_spawn(sys.argv[1], sys.argv[1:], os.environ, setsid=True)",
		os.Args[0], clientCommand, args[0], args[1], "detached")
	if out, err := spawn.CombinedOutput(); err != nil {
		_ = os.WriteFile(args[1], []byte("cannot detach: "+err.Error()+": "+string(out)+"\n"), 0)
		return 1
	}
	return 0
}

// clientLine is the command a run gives the client with args.
func clientLine(args ...string) string {
	line := quote(os.Args[0]) + " " + clientCommand
	for _, a := range args {
		line += " " + quote(a)
	}
	return line
}

// fakeAPI is an API server that lists one pod, answers a get of a Secret with
// one holding a note, and keeps the credential each request carried. A watch stays open, sending nothing, until its request
// ends, and watchEnded gets a value then.
type fakeAPI struct {
	*httptest.Server
	mu         sync.Mutex
	auths      []string
	watchEnded chan struct{}
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{watchEnded: make(chan struct{}, 4)}
	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		api.auths = append(api.auths, r.Header.Get("Authorization"))
		api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			api.watchEnded <- struct{}{}
			return
		}
		if strings.Contains(r.URL.Path, "/secrets/") {
			_, _ = io.WriteString(w, `{"kind":"Secret","apiVersion":"v1","metadata":{"name":"db","namespace":"web"},"data":{"note":"aHVudGVyMg=="}}`)
			return
		}
		_, _ = io.WriteString(w, `{"kind":"PodList","apiVersion":"v1","items":[{"metadata":{"name":"web-1"}}]}`)
	}))
	t.Cleanup(api.Close)
	return api
}

// credentials is the credential each request reached the server with.
func (api *fakeAPI) credentials() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]string(nil), api.auths...)
}

// connection is api as the cluster's connection, whose client adds the
// user's own credential to a request that carries none.
func (api *fakeAPI) connection() *clustersvc.Connection {
	base, _ := url.Parse(api.URL)
	return &clustersvc.Connection{BaseURL: base, HTTPClient: &http.Client{Transport: usersOwn{api.Client().Transport}}}
}

type usersOwn struct{ base http.RoundTripper }

func (u usersOwn) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer users-own")
	}
	return u.base.RoundTrip(r)
}

// proxyTool is a tool on the machine's sandbox, whose chat's cluster "7" is
// reached through lease.
func proxyTool(t *testing.T, lease *fakeLease) *Tool {
	t.Helper()
	tl := clusterTool(t, map[apimeta.ClusterID]*clustersvc.Cluster{"7": kubeCluster("prod", "uid-1")})
	tl.sandboxer = sandboxed(t)
	svc := tl.clusterSvc.(fakeService)
	svc.lease = lease
	tl.clusterSvc = svc
	// The forwarder and the client are this test binary, which under coverage
	// writes to childCoverDir, in the system's temp directory no sandbox lets a
	// run write, and warns on stderr as it exits unless it can.
	tl.env = append(tl.env, "GOCOVERDIR="+childCoverDir)
	tl.extraWritable = []string{childCoverDir}
	return tl
}

// childCoverDir is where the test binary writes its coverage when a run starts
// it, made by TestMain and removed once every test is done, since a client can
// outlive the test that started it.
var childCoverDir string

// runDirOf is the run's directory a command printed as its one line.
func runDirOf(t *testing.T, tl *Tool, out string) string {
	t.Helper()
	require.Regexp(t, "^"+regexp.QuoteMeta(tl.runsDir)+"/[^\n]+\n$", out)
	return strings.TrimSuffix(out, "\n")
}

// clusterRuntime is a fresh chat on cluster "7".
func clusterRuntime(t *testing.T) tools.Runtime {
	return tools.Runtime{ClusterID: "7", Dir: testChatDir(t)}
}

// watchLine opens a watch in a client that outlives the run's group, waits for
// it to be open, and prints the run's directory.
func watchLine() string {
	return `mkfifo "$TMPDIR/open" && { ` + clientLine("watch") + ` "$TMPDIR/open" >/dev/null 2>&1 & } && read _ < "$TMPDIR/open" && echo "$ZDOTDIR"`
}

// The grant dies with the run, for a call at its reap and for a task at its
// end: a watch open then is closed, though its client is still running, and
// the socket is gone.
func TestTheGrantDiesWithTheRun(t *testing.T) {
	t.Run("call", func(t *testing.T) {
		api := newFakeAPI(t)
		lease := &fakeLease{serverUID: "uid-1", conn: api.connection()}
		tl := proxyTool(t, lease)

		text, isError := tl.Run(t.Context(), clusterRuntime(t), command(watchLine()))

		require.False(t, isError, text)
		testutil.Recv(t, api.watchEnded, "the watch to close")
		assert.NoFileExists(t, filepath.Join(runDirOf(t, tl, text), socketName))
		assert.Equal(t, int32(1), lease.released.Load())
	})
	t.Run("task", func(t *testing.T) {
		api := newFakeAPI(t)
		lease := &fakeLease{serverUID: "uid-1", conn: api.connection()}
		tl := proxyTool(t, lease)
		rt := clusterRuntime(t)
		tasks := newFakeTasks(t)
		rt.Tasks = tasks

		text, isError := tl.Run(t.Context(), rt, background(watchLine()))
		require.False(t, isError, text)
		require.Len(t, tasks.started, 1)
		exit := tasks.started[0].Wait()

		assert.Equal(t, 0, exit.Code)
		testutil.Recv(t, api.watchEnded, "the watch to close")
		out, err := os.ReadFile(filepath.Join(string(tasks.dir), "t1.output"))
		require.NoError(t, err)
		assert.NoFileExists(t, filepath.Join(runDirOf(t, tl, string(out)), socketName))
		assert.Equal(t, int32(1), lease.released.Load())
	})
}

// A run's grant answers the session of the runtime it was made for, which is
// how its token maps to that session.
func TestTheGrantKeepsTheSession(t *testing.T) {
	api := newFakeAPI(t)
	tl := proxyTool(t, &fakeLease{serverUID: "uid-1", conn: api.connection()})
	rt := clusterRuntime(t)
	rt.Session = session.Session{Kind: session.Subagent}

	r, err := tl.sandboxedRunFor(t.Context(), &fakeSandboxer{}, rt, tools.WorkspacePath(rt.Dir), false)
	require.NoError(t, err)
	t.Cleanup(r.end)

	assert.Equal(t, rt.Session, r.proxy.grant.Session())
}

// A proxy that cannot listen on its socket answers why.
func TestAProxyThatCannotListenFails(t *testing.T) {
	_, err := startProxy(refused{}, session.Session{}, filepath.Join(t.TempDir(), "missing", socketName), nil, refusedNoAsker)
	assert.Error(t, err)
}

// A run whose directory cannot be made releases its claim, and runs nothing.
func TestARunThatCannotBeMadeReleasesItsClaim(t *testing.T) {
	lease := &fakeLease{serverUID: "uid-1"}
	tl := proxyTool(t, lease)
	tl.runsDir = "/" + strings.Repeat("t", maxSocketPath)

	text, isError := tl.Run(t.Context(), clusterRuntime(t), command("echo ran"))

	assert.True(t, isError)
	assert.True(t, strings.HasPrefix(text, "could not start: "), text)
	assert.Equal(t, int32(1), lease.released.Load())
}

// A sandboxed task that cannot start ends its run: the claim is released and
// the run's directory goes. It cannot start when its shell is missing, or
// when the sandbox refuses its policy, whose reason the model reads.
func TestASandboxedTaskThatCannotStartEndsItsRun(t *testing.T) {
	for name, boxer := range map[string]*fakeSandboxer{
		"no shell": {},
		"refused":  {cmdErr: errors.New("a rule over a fixed mount")},
	} {
		t.Run(name, func(t *testing.T) {
			lease := &fakeLease{serverUID: "uid-1"}
			tl := proxyTool(t, lease)
			tl.sandboxer = boxer // runs the shell itself, so its start is the task's
			if boxer.cmdErr == nil {
				tl.shell = filepath.Join(t.TempDir(), "no-shell")
			}
			rt := clusterRuntime(t)
			tasks := newFakeTasks(t)
			rt.Tasks = tasks

			text, isError := tl.Run(t.Context(), rt, background("true"))

			assert.True(t, isError, text)
			if boxer.cmdErr != nil {
				assert.Contains(t, text, boxer.cmdErr.Error())
			}
			assert.Empty(t, tasks.started)
			assert.Equal(t, int32(1), lease.released.Load())
			entries, err := os.ReadDir(tl.runsDir)
			require.NoError(t, err)
			for _, e := range entries {
				assert.False(t, e.IsDir(), "a run's directory was left: %s", e.Name())
			}
		})
	}
}

// A cluster the proxy cannot reach costs the cluster commands alone: the
// command runs, and a request answers why.
func TestAClusterThatCannotBeClaimedStillRunsTheCommand(t *testing.T) {
	for name, c := range map[string]struct {
		uid        string
		acquireErr error
		want       string
	}{
		"not connectable": {
			uid: "uid-1", acquireErr: clustersvc.ErrNotConnectable,
			want: "Kstack does not connect to this cluster: it is disabled, being deleted, or has no kubeconfig credentials",
		},
		"unidentified": {
			want: "Kstack has not identified this cluster, since it cannot read the kube-system namespace, so the sandbox cannot reach it",
		},
	} {
		t.Run(name, func(t *testing.T) {
			tl := proxyTool(t, nil)
			tl.clusterSvc = fakeService{
				clusters:   map[apimeta.ClusterID]*clustersvc.Cluster{"7": kubeCluster("prod", c.uid)},
				acquireErr: c.acquireErr,
			}

			text, isError := tl.Run(t.Context(), clusterRuntime(t), command("echo ran; "+clientLine()))

			assert.True(t, isError)
			assert.Contains(t, text, "ran\n")
			assert.Contains(t, text, c.want)
		})
	}
}

// A claim that fails for another reason could not start, and one refused
// because the cluster is gone since the target read it fails as a gone cluster
// does; nothing runs.
func TestAClaimThatFailsCouldNotStart(t *testing.T) {
	for name, acquireErr := range map[string]error{
		"another": errDisk,
		"gone":    clustersvc.ErrNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			tl := proxyTool(t, nil)
			tl.clusterSvc = fakeService{
				clusters:   map[apimeta.ClusterID]*clustersvc.Cluster{"7": kubeCluster("prod", "uid-1")},
				acquireErr: acquireErr,
			}

			text, isError := tl.Run(t.Context(), clusterRuntime(t), command("echo ran"))

			assert.True(t, isError)
			assert.True(t, strings.HasPrefix(text, "could not start: "), text)
			assert.NotContains(t, text, "ran")
			if acquireErr == clustersvc.ErrNotFound {
				assert.Contains(t, text, errClusterGone.Error())
			}
			entries, err := os.ReadDir(tl.runsDir)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}

// A client reading the run's KUBECONFIG lists pods through the forwarder and
// the proxy, and the API server sees the connection's credential.
func TestARunReachesTheClusterThroughTheProxy(t *testing.T) {
	api := newFakeAPI(t)
	lease := &fakeLease{serverUID: "uid-1", conn: api.connection()}
	tl := proxyTool(t, lease)

	text, isError := tl.Run(t.Context(), clusterRuntime(t), command(clientLine()))

	require.False(t, isError, text)
	assert.Equal(t, "web-1\n", text)
	assert.Equal(t, []string{"Bearer users-own"}, api.credentials())
	assert.Equal(t, int32(1), lease.released.Load(), "the claim is released with the run")
}

// Under the machine's sandbox, the forwarder sits on the namespace's own
// loopback and the client still reaches the cluster. It asserts on what the
// API server saw, never on the output: the child's GOCOVERDIR is outside the
// sandbox, so under coverage its exit warning lands there.
func TestASandboxedRunReachesTheClusterThroughTheProxy(t *testing.T) {
	api := newFakeAPI(t)
	lease := &fakeLease{serverUID: "uid-1", conn: api.connection()}
	tl := proxyTool(t, lease)
	tl.sandboxer = confining(t)

	text, isError := tl.Run(t.Context(), clusterRuntime(t), command(clientLine()))

	require.False(t, isError, text)
	assert.Equal(t, []string{"Bearer users-own"}, api.credentials())
	assert.Equal(t, int32(1), lease.released.Load(), "the claim is released with the run")
}

// A sandboxed run reads a Secret through the proxy, and reads it redacted.
func TestASandboxedRunReadsASecretRedacted(t *testing.T) {
	api := newFakeAPI(t)
	tl := proxyTool(t, &fakeLease{serverUID: "uid-1", conn: api.connection()})

	text, isError := tl.Run(t.Context(), clusterRuntime(t), command(clientLine("secret")))

	require.False(t, isError, text)
	assert.Equal(t, "note=[redacted]\n", text)
}

// fakeClusterWriteAsker is a runtime's ClusterWriteAsker that records each
// write and answers with approve.
type fakeClusterWriteAsker struct {
	mu      sync.Mutex
	approve bool
	asked   []tools.ClusterWriteRequest
}

func (f *fakeClusterWriteAsker) Ask(_ context.Context, w tools.ClusterWriteRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, w)
	return f.approve, nil
}

// A foreground call's write is put to the user through its runtime, and
// reaches the cluster once approved; with no one to ask it is refused.
func TestAForegroundGrantAsksThroughTheRuntime(t *testing.T) {
	api := newFakeAPI(t)
	tl := proxyTool(t, &fakeLease{serverUID: "uid-1", conn: api.connection()})
	rt := clusterRuntime(t)
	asker := &fakeClusterWriteAsker{approve: true}
	rt.ClusterWriteAsker = asker

	text, isError := tl.Run(t.Context(), rt, command(clientLine("delete")))

	require.False(t, isError, text)
	assert.Equal(t, "deleted\n", text)
	require.Len(t, asker.asked, 1)
	assert.Equal(t, "DELETE", asker.asked[0].Method)
	assert.Equal(t, "/api/v1/namespaces/web/pods/x", asker.asked[0].Path)

	tl = proxyTool(t, &fakeLease{serverUID: "uid-1", conn: api.connection()})
	text, isError = tl.Run(t.Context(), clusterRuntime(t), command(clientLine("delete")))
	assert.True(t, isError)
	assert.Contains(t, text, "kstack: this sandbox reads the cluster and changes nothing.")
}

// A background command's write is refused, whoever could be asked.
func TestABackgroundGrantRefusesWrites(t *testing.T) {
	api := newFakeAPI(t)
	tl := proxyTool(t, &fakeLease{serverUID: "uid-1", conn: api.connection()})
	rt := clusterRuntime(t)
	asker := &fakeClusterWriteAsker{approve: true}
	rt.ClusterWriteAsker = asker
	tasks := newFakeTasks(t)
	rt.Tasks = tasks

	text, isError := tl.Run(t.Context(), rt, background(clientLine("delete")))
	require.False(t, isError, text)
	require.Len(t, tasks.started, 1)
	tasks.started[0].Wait()

	out, err := os.ReadFile(filepath.Join(string(tasks.dir), "t1.output"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "kstack: a background command cannot change the cluster. Run it in the foreground.")
	assert.Empty(t, asker.asked)
}

// The proxy's end returns while a handler is blocked reading a body its client
// never finishes: it closes the server, which closes the connection, before it
// waits for the handlers. The handler is known to be reading once the server
// answers 100 Continue, which Go's server sends on the handler's first read.
func TestTheProxyClosesBeforeItWaits(t *testing.T) {
	socket := filepath.Join(shortTemp(t), socketName)
	p, err := startProxy(refused{}, session.Session{}, socket, runtimeAsker{&fakeClusterWriteAsker{approve: true}}, "")
	require.NoError(t, err)
	conn, err := net.Dial("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = fmt.Fprintf(conn, "PATCH http://%s/api/v1/namespaces/web/configmaps/x HTTP/1.1\r\nHost: %s\r\n"+
		"Proxy-Authorization: Basic %s\r\nContent-Type: application/merge-patch+json\r\n"+
		"Content-Length: 100\r\nExpect: 100-continue\r\n\r\n",
		kubeproxy.Host, kubeproxy.Host, base64.StdEncoding.EncodeToString([]byte(kubeproxy.ProxyUser+":"+p.grant.Token())))
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "HTTP/1.1 100 Continue\r\n", line)
	_, err = io.WriteString(conn, `{"data":`)
	require.NoError(t, err)

	testutil.WaitReturn(t, p.end, "the proxy's end, with a handler reading a body")
}
