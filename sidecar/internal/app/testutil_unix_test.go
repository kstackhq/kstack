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

package app

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// e2eConverge bounds each wait of an end-to-end test: the probes, the mirror,
// a turn and a sandboxed kubectl all sit between a write and its effect.
const e2eConverge = 30 * time.Second

// fakeCluster is an API server answering what the connection's probes, the
// mirror and kubectl ask of a cluster holding two pods in default, a delete of
// the pod x there, and recording every request as its method and path.
type fakeCluster struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

// The bodies the fake cluster answers with.
const (
	fakeUID        = "uid-e2e"
	fakeAPIVersion = `{"kind":"APIVersions","versions":["v1"],"serverAddressByClientCIDRs":[{"clientCIDR":"0.0.0.0/0","serverAddress":"127.0.0.1"}]}`
	fakeGroups     = `{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`
	fakeResources  = `{"kind":"APIResourceList","groupVersion":"v1","resources":[` +
		`{"name":"pods","singularName":"pod","namespaced":true,"kind":"Pod","verbs":["delete","get","list","watch"]}]}`
	fakeKubeSystem = `{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"kube-system","uid":"` + fakeUID + `"}}`
	fakeVersion    = `{"major":"1","minor":"31","gitVersion":"v1.31.4"}`
	fakeReview     = `{"kind":"SelfSubjectReview","apiVersion":"authentication.k8s.io/v1",` +
		`"status":{"userInfo":{"username":"admin","groups":["system:masters","system:authenticated"]}}}`
	fakePods = `{"kind":"PodList","apiVersion":"v1","metadata":{"resourceVersion":"1"},"items":[` +
		`{"metadata":{"name":"a","namespace":"default","uid":"pod-a","resourceVersion":"1"}},` +
		`{"metadata":{"name":"b","namespace":"default","uid":"pod-b","resourceVersion":"1"}}]}`
	fakeNotFound = `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
	fakePodX     = `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"x","namespace":"default","uid":"pod-x","resourceVersion":"2"}}`
)

// serveFakeCluster runs a fake cluster over TLS for the life of the test.
func serveFakeCluster(t *testing.T) *fakeCluster {
	t.Helper()
	c := &fakeCluster{}
	c.Server = httptest.NewTLSServer(c)
	t.Cleanup(c.Close)
	return c
}

func (c *fakeCluster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.seen = append(c.seen, r.Method+" "+r.URL.Path)
	c.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	body := ""
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api":
		body = fakeAPIVersion
	case r.Method == http.MethodGet && r.URL.Path == "/apis":
		body = fakeGroups
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1":
		body = fakeResources
	case r.Method == http.MethodGet && r.URL.Path == "/readyz":
		body = "ok"
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/kube-system":
		body = fakeKubeSystem
	case r.Method == http.MethodGet && r.URL.Path == "/version":
		body = fakeVersion
	case r.Method == http.MethodPost && r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, fakeReview)
		return
	case r.Method == http.MethodGet && (r.URL.Path == "/api/v1/pods" || r.URL.Path == "/api/v1/namespaces/default/pods"):
		if r.URL.Query().Get("watch") == "true" {
			// A watch with nothing to say is held open until its client leaves.
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		body = fakePods
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/namespaces/default/pods/x":
		body = fakePodX
	default:
		w.WriteHeader(http.StatusNotFound)
		body = fakeNotFound
	}
	_, _ = io.WriteString(w, body)
}

// requests is every request the cluster has seen, as its method and path.
func (c *fakeCluster) requests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.seen)
}

// writeClusterKubeconfig writes a kubeconfig at path whose one context, e2e,
// reaches c and trusts its certificate alone.
func writeClusterKubeconfig(t *testing.T, path string, c *fakeCluster) {
	t.Helper()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate().Raw})
	config := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: e2e
contexts:
- name: e2e
  context: {cluster: c, user: u}
clusters:
- name: c
  cluster: {server: %q, certificate-authority-data: %s}
users:
- name: u
  user: {token: t}
`, c.URL, base64.StdEncoding.EncodeToString(ca))
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
}

// e2e is an app over the machine's sandbox, a fake cluster it has identified,
// and the fake model, driven over GraphQL and read back from app.db.
type e2e struct {
	url       string
	fake      *llm.Fake
	cluster   *fakeCluster
	db        *sql.DB
	clusterID string
}

// shortTemp is a temp directory for one test, short enough for a run's socket
// under it. Not t.TempDir(): it embeds the test's name, and on macOS its path
// is past what a run's socket fits under.
func shortTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "k")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startE2E starts the app over a fake cluster and waits for the cluster to be
// identified, so a sandboxed run can claim its connection. Without a sandbox,
// kubectl or jq, testutil.RequireSandbox decides.
func startE2E(t *testing.T) *e2e {
	t.Helper()
	if _, v := sandbox.Probe(t.Context()); !v.Available {
		testutil.RequireSandbox(t, "no sandbox: "+v.Reason)
	}
	for _, name := range []string{"kubectl", "jq"} {
		if _, err := exec.LookPath(name); err != nil {
			testutil.RequireSandbox(t, "no "+name+" on PATH")
		}
	}

	cluster := serveFakeCluster(t)
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	writeClusterKubeconfig(t, kubeconfig, cluster)
	data := filepath.Join(dir, "data")
	fake := llm.NewFake(0)
	a, err := New(withDirs(t, Config{KubeconfigPath: kubeconfig, DataDir: data, RuntimeDir: shortTemp(t), fake: fake}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	startApp(t, a)
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)

	db, err := sql.Open("sqlite", "file:"+filepath.Join(data, "app.db")+"?mode=ro")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	e := &e2e{url: srv.URL, fake: fake, cluster: cluster, db: db}
	require.Eventually(t, func() bool {
		var resp struct {
			Data struct {
				Clusters []struct {
					ID     string `json:"id"`
					Status struct {
						Server struct {
							UID *string `json:"uid"`
						} `json:"server"`
					} `json:"status"`
				} `json:"clusters"`
			} `json:"data"`
		}
		raw := graphql(t, srv.URL, `{ clusters { id status { server { uid } } } }`)
		require.NoError(t, json.Unmarshal([]byte(raw), &resp), raw)
		for _, c := range resp.Data.Clusters {
			if c.Status.Server.UID != nil && *c.Status.Server.UID == fakeUID {
				e.clusterID = c.ID
				return true
			}
		}
		return false
	}, e2eConverge, 10*time.Millisecond, "the cluster identified")
	return e
}

// ask starts a chat on the cluster with question, whose turn asks for calls,
// and answers the chat's id: the fake answers a chat whose first question is
// question on a route of its own, so each test stages its own.
func (e *e2e) ask(t *testing.T, question string, calls ...llm.Block) string {
	t.Helper()
	e.fake.Route(question).SetToolCalls(calls...)
	raw := graphql(t, e.url, `mutation { chatSend(mode: Chat, clusterID: "`+e.clusterID+
		`", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "low", requestID: "`+appdb.NewID()+
		`", content: "`+question+`") { chatID } }`)
	var resp struct {
		Data struct {
			ChatSend struct {
				ChatID string `json:"chatID"`
			} `json:"chatSend"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &resp), raw)
	require.NotEmpty(t, resp.Data.ChatSend.ChatID, raw)
	return resp.Data.ChatSend.ChatID
}

// askAgain sends a second question into the chat ask started with first, once
// its turn has settled, whose turn asks for calls: the fake still routes the
// chat by its first question. outside is the chat's switch as the sender saw it.
func (e *e2e) askAgain(t *testing.T, chatID string, disabled bool, first string, calls ...llm.Block) {
	t.Helper()
	require.Eventually(t, func() bool {
		var n int
		err := e.db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE chat_id = ? AND status IN ('queued', 'running')`, chatID).Scan(&n)
		return err == nil && n == 0
	}, e2eConverge, 10*time.Millisecond, "the first turn settled")
	e.fake.Route(first).SetToolCalls(calls...)
	raw := graphql(t, e.url, `mutation { chatSend(chatID: "`+chatID+`", mode: Chat, clusterID: "`+e.clusterID+
		`", sandboxDisabled: `+strconv.FormatBool(disabled)+`, providerID: "fake", modelID: "fake", effort: "low", requestID: "`+appdb.NewID()+
		`", content: "again") { chatID } }`)
	require.Contains(t, raw, `"chatID"`, raw)
}

// bashRow is a Bash call's row as the test reads it: its status, whether a
// sandbox confined it, what the model read, and its approval, "" for none.
type bashRow struct {
	status         string
	sandboxed      bool
	result         string
	approvalID     string
	approvalStatus string
}

// bashCall waits for the app's one Bash call to reach a state in want, and
// answers its row.
func (e *e2e) bashCall(t *testing.T, want ...string) bashRow {
	t.Helper()
	var row bashRow
	require.Eventually(t, func() bool {
		err := e.db.QueryRow(`SELECT tc.status, tc.sandboxed, coalesce(tc.result, ''),
			coalesce(a.id, ''), coalesce(a.status, '')
			FROM tool_calls tc LEFT JOIN approvals a ON a.tool_call_id = tc.id AND a.kind = 'call'
			WHERE tc.tool_name = 'Bash'`).Scan(&row.status, &row.sandboxed, &row.result, &row.approvalID, &row.approvalStatus)
		return err == nil && slices.Contains(want, row.status)
	}, e2eConverge, 10*time.Millisecond, "a Bash call in %v", want)
	return row
}

// writeRow is a cluster write's approval as the test reads it: its id, its
// status and the request it holds.
type writeRow struct {
	id, status string
	request    tools.ClusterWriteRequest
}

// clusterWrite waits for the app's one cluster write to reach a status in
// want, and answers its row.
func (e *e2e) clusterWrite(t *testing.T, want ...string) writeRow {
	t.Helper()
	var row writeRow
	require.Eventually(t, func() bool {
		var request string
		err := e.db.QueryRow(`SELECT id, status, request FROM approvals WHERE kind = 'cluster'`).Scan(&row.id, &row.status, &request)
		return err == nil && json.Unmarshal([]byte(request), &row.request) == nil && slices.Contains(want, row.status)
	}, e2eConverge, 10*time.Millisecond, "a cluster write in %v", want)
	return row
}

// runStatus is the status of the run behind the app's one Bash call.
func (e *e2e) runStatus(t *testing.T) string {
	t.Helper()
	var status string
	require.NoError(t, e.db.QueryRow(`SELECT r.status FROM agent_runs r JOIN llm_calls c ON c.run_id = r.id
		JOIN tool_calls tc ON tc.llm_call_id = c.id WHERE tc.tool_name = 'Bash'`).Scan(&status))
	return status
}

// firstLine is what a command printed first. Under coverage the forwarder, this
// test binary, warns after it as it exits, since its GOCOVERDIR is outside the
// sandbox.
func firstLine(result string) string {
	line, _, _ := strings.Cut(result, "\n")
	return line
}

// bashInput is a Bash call's arguments running command.
func bashInput(command string) string {
	return bashInputWithin(command, 0)
}

// bashInputWithin is bashInput with the call's timeout in milliseconds, none
// when zero.
func bashInputWithin(command string, timeoutMs int) string {
	in := map[string]any{"command": command, "description": "e2e"}
	if timeoutMs > 0 {
		in["timeout"] = timeoutMs
	}
	b, _ := json.Marshal(in)
	return string(b)
}
