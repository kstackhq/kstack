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
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/loginshell"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
	"github.com/kstackhq/kstack/sidecar/internal/tools/webfetch"
)

// TestMain answers sandbox-init and sandbox-shell: New's probe starts
// this test binary as both, through the machine's sandbox.
func TestMain(m *testing.M) {
	if code, ok := sandbox.Main(os.Args); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// Where the probe passes, Bash is offered through the sandbox, and its schema
// has no way out of it. Without one, testutil.RequireSandbox decides.
func TestBashIsOfferedWithTheSandbox(t *testing.T) {
	sb, v, err := sandbox.Probe(t.Context())
	require.NoError(t, err)
	if !v.Available {
		testutil.RequireSandbox(t, "no sandbox: "+v.Reason)
	}
	t.Setenv("SHELL", "")
	withBash := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withBash, "bash"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", withBash)
	shell, found := bash.New(bash.Paths{ShellDir: t.TempDir()}, 0, sb, nil, nil)
	require.True(t, found)

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(shell.Definition().InputSchema, &schema))
	assert.Len(t, schema.Properties, 5)
	assert.NotContains(t, schema.Properties, "dangerouslyDisableSandbox")
}

// Off Windows, bash.New finds the shell on PATH once $SHELL names none, so the
// test clears it and points PATH at a directory it controls. Read, Memory and Write come
// after bash, then TaskStop, which only a bash has tasks for.
func TestBashIsOfferedWhenItIsFound(t *testing.T) {
	t.Setenv("SHELL", "")
	withBash := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withBash, "bash"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", withBash)
	shell, found := bash.New(bash.Paths{ShellDir: t.TempDir()}, 0, nil, nil, nil)
	require.True(t, found)
	box, err := chatTools(shell, []string{t.TempDir()}, 0o022, nil, nil)
	require.NoError(t, err)
	defs, _ := box.Offer()
	assert.Equal(t, []string{"Bash", "Read", "Memory", "Write", "Edit", "WebFetch", "TaskStop", "Agent", "KubeQuery"}, definitionNames(defs))
	runner, ok := box.Runner(bash.Name)
	require.True(t, ok)
	assert.Same(t, shell, runner)
}

// The approval request draws what a gated call does, so every gated tool's calls
// are of a kind it can draw.
func TestEveryGatedToolCanBeShown(t *testing.T) {
	t.Setenv("SHELL", "")
	withBash := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withBash, "bash"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", withBash)
	shell, found := bash.New(bash.Paths{ShellDir: t.TempDir()}, 0, nil, nil, nil)
	require.True(t, found)

	box, err := chatTools(shell, []string{t.TempDir()}, 0o022, nil, nil)
	require.NoError(t, err)
	defs, _ := box.Offer()
	gated := 0
	var names []string
	for _, d := range defs {
		names = append(names, d.Name)
		runner, ok := box.Runner(d.Name)
		require.True(t, ok, d.Name)
		if _, ok := runner.(tools.Gated); ok {
			gated++
			assert.Contains(t, []tools.ActionKind{tools.ActionCommand, tools.ActionRead, tools.ActionWrite, tools.ActionEdit, tools.ActionFetch, tools.ActionMemory}, runner.ActionKind(), d.Name)
		}
	}
	assert.Positive(t, gated)
	assert.Contains(t, names, webfetch.Name)
}

// The snapshot is a part only when the config asks for one, which main alone
// does: a test's app never spawns the developer's login shell. It comes last, so
// its stop runs first.
func TestAppTakesTheSnapshotOnlyWhenAsked(t *testing.T) {
	withBash := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withBash, "bash"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("SHELL", "")
	t.Setenv("PATH", withBash)

	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	assert.False(t, slices.ContainsFunc(a.parts, func(p lifecycle.Part) bool { return p.Name == "shell snapshot" }))

	stubLaunch(t, loginshell.Result{}, &loginshell.Fault{Reason: "no shell", ExitCode: -1})
	a, err = New(t.Context(), withDirs(t, Config{DataDir: t.TempDir(), RunLoginShell: true}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	assert.Equal(t, len(a.parts)-1, partIndex(t, a, "shell snapshot"))
}

// A directory the host did not make, as in a run without it, is made
// owner-only.
func TestNewMakesAMissingDirectoryOwnerOnly(t *testing.T) {
	base := t.TempDir()
	cfg := Config{
		DataDir:    filepath.Join(base, "data"),
		CacheDir:   filepath.Join(base, "cache"),
		RuntimeDir: filepath.Join(base, "run"),
	}
	a, err := New(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	for _, dir := range []string{cfg.DataDir, cfg.CacheDir, cfg.RuntimeDir} {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), dir)
	}
}

// A sandboxed read of the chat's cluster runs as the model asks for it: no one
// is asked, and the row says the sandbox confined it.
func TestASandboxedReadRunsUnasked(t *testing.T) {
	e := startE2E(t)

	e.ask(t, "how many pods", llm.StagedCall("Bash", bashInput("kubectl get pods -o json | jq '.items | length'")))

	row := e.bashCall(t, "succeeded", "failed", "awaiting_approval")
	assert.Equal(t, "succeeded", row.status, row.result)
	assert.Equal(t, "2", firstLine(row.result))
	assert.True(t, row.sandboxed)
	assert.Empty(t, row.approvalID)
	assert.Contains(t, e.cluster.requests(), "GET /api/v1/namespaces/default/pods", "kubectl reached the cluster")
}

// A delete from the sandbox waits for the user on one request showing the
// DELETE, reaches the cluster once approved, and the command reads its answer.
func TestASandboxedDeleteAsksAndRuns(t *testing.T) {
	e := startE2E(t)

	e.ask(t, "delete pod x", llm.StagedCall("Bash", bashInput("kubectl delete pod x --wait=false")))

	w := e.clusterWrite(t, "pending")
	assert.Equal(t, "DELETE", w.request.Write.Method)
	assert.Equal(t, "/api/v1/namespaces/default/pods/x", w.request.Write.Path)
	assert.Equal(t, "waiting_approval", e.runStatus(t))
	assert.NotContains(t, e.cluster.requests(), "DELETE /api/v1/namespaces/default/pods/x", "nothing reaches the cluster before the user decides")
	raw := graphql(t, e.url, `mutation { approvalDecide(id: "`+w.id+`", approve: true) }`)
	require.Contains(t, raw, `"approvalDecide":true`, raw)

	row := e.bashCall(t, "succeeded", "failed")
	assert.Equal(t, "succeeded", row.status, row.result)
	// kubectl names the namespace after it in later versions.
	assert.True(t, strings.HasPrefix(firstLine(row.result), `pod "x" deleted`), row.result)
	assert.True(t, row.sandboxed)
	assert.Empty(t, row.approvalID, "the call itself ran unasked")
	assert.Contains(t, e.cluster.requests(), "DELETE /api/v1/namespaces/default/pods/x")
	assert.Equal(t, "approved", e.clusterWrite(t, "approved").status)
}

// A denied delete comes back Forbidden and never reaches the cluster.
func TestASandboxedDeleteDeniedIsForbidden(t *testing.T) {
	e := startE2E(t)

	e.ask(t, "delete pod x", llm.StagedCall("Bash", bashInput("kubectl delete pod x --wait=false")))
	w := e.clusterWrite(t, "pending")
	raw := graphql(t, e.url, `mutation { approvalDecide(id: "`+w.id+`", approve: false) }`)
	require.Contains(t, raw, `"approvalDecide":true`, raw)

	row := e.bashCall(t, "succeeded", "failed")
	assert.Equal(t, "failed", row.status, row.result)
	assert.Contains(t, row.result, "Forbidden")
	assert.Contains(t, row.result, "kstack: the user did not approve this change.")
	for _, r := range e.cluster.requests() {
		assert.NotContains(t, r, "DELETE", "the cluster saw a change")
	}
}

// A call whose time runs out while its write waits ends: the write is
// abandoned, the run is not left waiting, and the cluster sees nothing.
func TestACallTimingOutWhileAWriteWaits(t *testing.T) {
	e := startE2E(t)

	e.ask(t, "delete pod x", llm.StagedCall("Bash", bashInputWithin("kubectl delete pod x --wait=false", 2000)))
	e.clusterWrite(t, "pending")

	row := e.bashCall(t, "succeeded", "failed")
	assert.Equal(t, "failed", row.status, row.result)
	assert.Equal(t, "abandoned", e.clusterWrite(t, "abandoned").status)
	assert.NotEqual(t, "waiting_approval", e.runStatus(t))
	for _, r := range e.cluster.requests() {
		assert.NotContains(t, r, "DELETE", "the cluster saw a change")
	}
}

// The same change in a chat the user switched outside the sandbox waits for
// the user, and a denial runs nothing.
func TestASwitchedChatAsks(t *testing.T) {
	e := startE2E(t)
	chatID := e.ask(t, "switch this chat")
	raw := graphql(t, e.url, `mutation { chatSandboxDisabledSet(id: "`+chatID+`", sandboxDisabled: true) { sandboxDisabled } }`)
	require.Contains(t, raw, `"sandboxDisabled":true`, raw)

	e.askAgain(t, chatID, true, "switch this chat", llm.StagedCall("Bash", bashInput("kubectl delete pod x")))

	row := e.bashCall(t, "awaiting_approval")
	assert.False(t, row.sandboxed)
	assert.Equal(t, "pending", row.approvalStatus)
	raw = graphql(t, e.url, `mutation { approvalDecide(id: "`+row.approvalID+`", approve: false) }`)
	require.Contains(t, raw, `"approvalDecide":true`, raw)

	row = e.bashCall(t, "denied")
	assert.Equal(t, "denied", row.approvalStatus)
	for _, r := range e.cluster.requests() {
		assert.NotContains(t, r, "DELETE", "the cluster saw a change")
	}
}

// A subagent's sandboxed call runs unasked, as its parent's does.
func TestASubagentsSandboxedCallRunsUnasked(t *testing.T) {
	e := startE2E(t)
	e.fake.Route("count the pods").SetToolCalls(
		llm.StagedCall("Bash", bashInput("kubectl get pods -o json | jq '.items | length'")))

	e.ask(t, "have an agent count pods",
		llm.StagedCall("Agent", `{"description":"Count pods","prompt":"count the pods"}`))

	row := e.bashCall(t, "succeeded", "failed", "awaiting_approval")
	assert.Equal(t, "succeeded", row.status, row.result)
	assert.Equal(t, "2", firstLine(row.result))
	assert.True(t, row.sandboxed)
	assert.Empty(t, row.approvalID)
}

// The PATH main read at launch is synced at Start, before the snapshot, on a
// machine with a sandbox; with none read there is nothing to sync.
func TestStartSyncsTheLaunchPath(t *testing.T) {
	if _, v, _ := sandbox.Probe(t.Context()); !v.Available {
		testutil.RequireSandbox(t, "no sandbox: "+v.Reason)
	}
	t.Setenv("SHELL", "")
	withBash := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withBash, "bash"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", withBash)

	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	assert.False(t, slices.ContainsFunc(a.parts, func(p lifecycle.Part) bool { return p.Name == "PATH sync" }))

	data := t.TempDir()
	usrBin, err := filepath.EvalSymlinks("/usr/bin")
	require.NoError(t, err)
	stubLaunch(t, loginshell.Result{Path: []string{"/usr/bin", "bin"}}, nil)
	a, err = New(t.Context(), withDirs(t, Config{DataDir: data, RunLoginShell: true}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	assert.Equal(t, partIndex(t, a, "shell snapshot")-1, partIndex(t, a, "PATH sync"))
	startApp(t, a)

	store, err := securityconfig.Open(filepath.Join(data, "security.json"))
	require.NoError(t, err)
	assert.Equal(t, []securityconfig.PathEntry{
		{Dir: "/usr/bin", Target: usrBin, State: securityconfig.PathAdopted, Source: securityconfig.SourceShell},
	}, store.Get().Path)
}

// countProbes replaces probeSandbox with one that counts its calls and keeps
// the sandbox the machine's probe answered.
func countProbes(t *testing.T) (count *atomic.Int32, probed **sandbox.Sandbox) {
	t.Helper()
	count, probed = new(atomic.Int32), new(*sandbox.Sandbox)
	probe := probeSandbox
	t.Cleanup(func() { probeSandbox = probe })
	probeSandbox = func(ctx context.Context) (*sandbox.Sandbox, sandbox.Status, error) {
		count.Add(1)
		sb, status, err := probe(ctx)
		*probed = sb
		return sb, status, err
	}
	return count, probed
}

// New probes the sandbox once, for the login shell and the bash tool alike,
// and runs the login shell once wherever the platform reads its answer.
func TestTheSandboxIsProbedOnce(t *testing.T) {
	probes, probed := countProbes(t)
	launches := stubLaunch(t, loginshell.Result{Path: []string{"/usr/bin"}}, nil)

	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir(), RunLoginShell: true}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })

	assert.Equal(t, int32(1), probes.Load())
	var sb sandboxer
	if *probed != nil {
		sb = *probed
	}
	want := 1
	if skipResolution(sb) {
		want = 0
	}
	assert.Equal(t, want, *launches)
}

// refusingSandbox records each Run it is handed and refuses it; its Never
// list is never, and it reads nothing.
type refusingSandbox struct {
	never []string
	runs  []sandbox.Run
}

func (s *refusingSandbox) Command(_ context.Context, r sandbox.Run) (*exec.Cmd, error) {
	s.runs = append(s.runs, r)
	return nil, errors.New("refused")
}

func (s *refusingSandbox) Never(string) []string { return s.never }

func (s *refusingSandbox) System(string, string) sandbox.System { return sandbox.System{} }

// Refresh PATH runs the login shell in the sandbox, under the launch's
// policy: the denied-always list, as it stands at each refresh, and Kstack's
// directories shut.
func TestTheRefreshRunsInTheSandbox(t *testing.T) {
	store, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	sb := &refusingSandbox{never: []string{"/never"}}
	kstackDirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	svc := newSecurityService(store, sb, nil, sandbox.Status{Available: true}, kstackDirs, "", filepath.Join(kstackDirs[1], "tmp"))

	_, err = svc.RefreshPath(t.Context())
	require.ErrorContains(t, err, "sandbox refused")
	sb.never = []string{"/never", "/home/new"}
	_, err = svc.RefreshPath(t.Context())
	require.ErrorContains(t, err, "sandbox refused")

	require.Len(t, sb.runs, 2)
	assert.Equal(t, []string{"/never"}, sb.runs[0].Policy.Always.Deny)
	assert.Equal(t, kstackDirs, sb.runs[0].Policy.Always.Kstack)
	assert.Equal(t, []string{"/never", "/home/new"}, sb.runs[1].Policy.Always.Deny)
}

// A context that ends during startup stops New before the login shell runs,
// so a probe a quit cut short never stands for no sandbox.
func TestNewStopsWhenItsContextEnds(t *testing.T) {
	launches := stubLaunch(t, loginshell.Result{Path: []string{"/usr/bin"}}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	a, err := New(ctx, withDirs(t, Config{DataDir: t.TempDir(), RunLoginShell: true}))

	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, a)
	assert.Zero(t, *launches)
}
