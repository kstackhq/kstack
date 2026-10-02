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

package sandbox

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// paramsOf is a profile's -D arguments, by name.
func paramsOf(t *testing.T, args []string) map[string]string {
	t.Helper()
	require.Zero(t, len(args)%2, args)
	params := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		require.Equal(t, "-D", args[i])
		name, value, ok := strings.Cut(args[i+1], "=")
		require.True(t, ok, args[i+1])
		params[name] = value
	}
	return params
}

// nameOf is the one parameter of the kind prefix names whose value is p.
func nameOf(t *testing.T, params map[string]string, prefix, p string) string {
	t.Helper()
	var names []string
	for name, value := range params {
		if value == p && strings.HasPrefix(name, prefix+"_") {
			names = append(names, name)
		}
	}
	require.Len(t, names, 1, "one %s parameter names %s: %v", prefix, p, params)
	return names[0]
}

// ruleAt is where the text first reads the parameter name.
func ruleAt(t *testing.T, text, name string) int {
	t.Helper()
	i := strings.Index(text, `(param "`+name+`")`)
	require.GreaterOrEqual(t, i, 0, "no rule reads %s", name)
	return i
}

// profileRun is a run over a stand-in machine for the profile's own tests: a
// home whose ~/.cargo is a toolchain folder, one System folder outside it,
// and Kstack's three directories holding the workspace, the TMPDIR and the
// run's own directory. Its policy is the Workspace policy on s.
func profileRun(t *testing.T, s *Sandbox) (Run, string) {
	t.Helper()
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "home/.cargo/bin", "tools/bin", "data/chats/c/workspace", "cache/tmp/1-a", "runtime/runs/1-a")
	home := filepath.Join(base, "home")
	addToolchain(t, "~/.cargo")
	addRoot(t, d[1])
	env := []string{"PATH=" + d[0] + string(filepath.ListSeparator) + d[1] + string(filepath.ListSeparator) + "/usr/bin"}
	kstack := []string{filepath.Join(base, "data"), filepath.Join(base, "cache"), filepath.Join(base, "runtime")}
	return Run{
		Shell: "/bin/sh",
		Dir:   d[2],
		Env:   env,
		Policy: Policy{
			Files:  s.System(home, "/bin/sh").Files.Outside(kstack...),
			Always: AlwaysPolicy{Deny: s.Never(home), Kstack: kstack, Read: []string{d[4]}, Write: []string{d[2], d[3]}},
		},
	}, base
}

// ruleNamed is where the text first reads the rule whose format and value are
// given, its %s the rule's parameter.
func ruleNamed(t *testing.T, text string, params map[string]string, format, p string) int {
	t.Helper()
	for name, value := range params {
		if value == p && strings.HasPrefix(name, "RULE_") {
			if i := strings.Index(text, fmt.Sprintf(format, name)); i >= 0 {
				return i
			}
		}
	}
	require.Failf(t, "no rule", "%s for %s in %v", format, p, params)
	return -1
}

// The rule shapes each kind compiles to.
const (
	readRule  = `(allow file-read* (subpath (param "%s")))`
	writeRule = `(allow file-read* file-write* (subpath (param "%s")))`
	denyRule  = `(deny file-read* file-write* (subpath (param "%s")))`
)

// Every path reaches the profile as a parameter, and its text holds only names
// Kstack made: a path holding a quote or a parenthesis is data, never code.
func TestTheProfileWritesNoPathIntoItsText(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)
	odd := mkdirs(t, base, "data/we\"ird)\n(allow default)")[0]
	r.Policy.Always.Write[0], r.Dir = odd, odd

	text, args := s.profile(r)

	assert.NotContains(t, text, base)
	assert.NotContains(t, text, "(allow default)")
	for _, m := range []string{markerRules, markerAncestors, markerNetwork} {
		assert.NotContains(t, text, m)
	}
	params := paramsOf(t, args)
	ruleNamed(t, text, params, writeRule, odd)
	for name := range params {
		assert.Regexp(t, `^[A-Z]+(_[A-Z]+)*(_[0-9]+)?$`, name)
	}
}

// The System folders that exist, the toolchain folders and the sidecar's own
// executable are read and not written; the workspace and the TMPDIR are read
// and written, the run's directory read.
func TestTheProfileNamesWhatARunReads(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)

	text, args := s.profile(r)
	params := paramsOf(t, args)

	reads := []string{resolved("/bin/sh"), filepath.Join(base, "home", ".cargo"), filepath.Join(base, "tools", "bin")}
	for _, p := range platformLists.System {
		if _, err := os.Stat(p); err == nil {
			reads = append(reads, resolved(p))
		}
	}
	for _, p := range reads {
		i := ruleNamed(t, text, params, readRule, p)
		assert.Less(t, i, ruleNamed(t, text, params, `(deny file-write* (subpath (param "%s")))`, p), p)
	}
	for _, p := range r.Policy.Always.Write {
		ruleNamed(t, text, params, writeRule, p)
	}
	ruleNamed(t, text, params, readRule, r.Policy.Always.Read[0])
}

// Seatbelt checks a file's real path, so every path is resolved, but for the
// socket, which is named both as given and as resolved.
func TestAProfilePathIsResolved(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)
	runDir := r.Policy.Always.Read[0]
	link := filepath.Join(base, "ws-link")
	require.NoError(t, os.Symlink(r.Policy.Always.Write[0], link))
	sockLink := filepath.Join(base, "runtime-link")
	require.NoError(t, os.Symlink(runDir, sockLink))
	r.Policy.Always.Write[0], r.Dir = link, link
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "proxy.sock"), nil, 0o600))
	socket := filepath.Join(sockLink, "proxy.sock")
	r.Policy.Network.Relays = []Relay{{Port: 4321, Socket: socket}}

	text, args := s.profile(r)
	params := paramsOf(t, args)

	ruleNamed(t, text, params, writeRule, filepath.Join(base, "data", "chats", "c", "workspace"))
	assert.Equal(t, socket, params["SOCKET"])
	assert.Equal(t, filepath.Join(runDir, "proxy.sock"), params["SOCKET_RESOLVED"])
}

// Denied are the Always paths and Files Denies a read takes in — here the
// credential paths in a tree, /etc's secrets and Homebrew's var — after the
// read that holds them and before the run's own, since a later rule wins.
// Kstack's directories and the other homes, which no read takes in, are left
// out.
func TestTheProfileDeniesBetweenTheReadsAndTheRunsOwn(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)
	brew := mkdirs(t, base, "tools/bin/var")[0]
	r.Policy.Files.Deny = []string{brew}

	text, args := s.profile(r)
	params := paramsOf(t, args)

	cargo := filepath.Join(base, "home", ".cargo")
	denied := []string{filepath.Join(cargo, "credentials"), filepath.Join(cargo, "credentials.toml"), brew}
	for _, p := range platformLists.Never {
		if !strings.HasPrefix(p, "~/") {
			denied = append(denied, p)
		}
	}
	var names []string
	for name, v := range params {
		if strings.HasPrefix(name, "RULE_") && strings.Contains(text, fmt.Sprintf(denyRule, name)) {
			names = append(names, v)
		}
	}
	assert.ElementsMatch(t, denied, names, "no Deny a read does not take in")
	for _, p := range denied {
		i := ruleNamed(t, text, params, denyRule, p)
		assert.Less(t, ruleNamed(t, text, params, readRule, filepath.Dir(p)), i, p)
		assert.Less(t, i, ruleNamed(t, text, params, writeRule, r.Policy.Always.Write[0]))
		assert.Less(t, i, ruleNamed(t, text, params, readRule, r.Policy.Always.Read[0]))
	}
}

// Each of the run's own Write paths is followed by a denial of its root's own
// entry, so the allow cannot outrank it; a Files Write rule gets none.
func TestTheProfileKeepsARunsOwnRootsInPlace(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)
	extra := mkdirs(t, base, "extra")[0]
	r.Policy.Files.Write = append(r.Policy.Files.Write, extra)

	text, args := s.profile(r)
	params := paramsOf(t, args)

	deny := `(deny file-write-unlink file-write-create (literal (param "%s")))`
	for _, p := range r.Policy.Always.Write {
		name := nameOf(t, params, "RULE", resolved(p))
		assert.Contains(t, text, fmt.Sprintf(writeRule, name)+"\n"+fmt.Sprintf(deny, name))
	}
	name := nameOf(t, params, "RULE", extra)
	assert.NotContains(t, text, fmt.Sprintf(deny, name))
}

// A credential path is compared at its target against every read, the System
// folders among them: ~/.aws linked under /usr is denied there.
func TestTheProfileDeniesALinkedCredentialPathUnderARoot(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)
	require.NoError(t, os.Symlink("/usr/share", filepath.Join(base, "home", ".aws")))

	text, args := s.profile(r)

	ruleNamed(t, text, paramsOf(t, args), denyRule, "/usr/share")
}

// Every ancestor of what a run reads can be statted, after the denials and the
// fixed rules, so the workspace's own resolves though the data directory
// holding it is denied.
func TestTheProfileLetsEveryAncestorBeStattedAfterTheDenials(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, base := profileRun(t, s)

	text, args := s.profile(r)
	params := paramsOf(t, args)

	lastDenial := strings.LastIndex(text, "(deny file-read* file-write*")
	fixed := strings.Index(text, `(allow file-read-data (literal "/"))`)
	require.Greater(t, fixed, lastDenial)
	for _, p := range []string{"/", base, filepath.Join(base, "data"), filepath.Join(base, "data", "chats", "c"), filepath.Dir(resolved("/bin/sh"))} {
		name := nameOf(t, params, "UP", p)
		assert.Contains(t, text, `(allow file-read-metadata (literal (param "`+name+`")))`)
		assert.Greater(t, ruleAt(t, text, name), fixed)
	}
}

// A run with no relay has no network rule at all; one with a relay may bind
// and reach its port and reach its socket, and nothing else.
func TestOnlyARunWithASocketHasNetwork(t *testing.T) {
	s := &Sandbox{self: "/bin/sh"}
	r, _ := profileRun(t, s)

	text, args := s.profile(r)
	assert.NotContains(t, text, "network")
	assert.NotContains(t, paramsOf(t, args), "SOCKET")

	r.Policy.Network.Relays = []Relay{{Port: 4321, Socket: filepath.Join(r.Policy.Always.Read[0], "proxy.sock")}}
	text, _ = s.profile(r)
	assert.Contains(t, text, `(allow network-bind network-inbound (local tcp4 "localhost:4321"))`)
	assert.Contains(t, text, `(remote tcp4 "localhost:4321")`)
	assert.Contains(t, text, `(remote unix-socket (path-literal (param "SOCKET")))`)
	assert.Contains(t, text, `(remote unix-socket (path-literal (param "SOCKET_RESOLVED")))`)
	assert.Equal(t, 2, strings.Count(text, "(allow network"), "one bind rule and one outbound rule")
}

func TestARefusedServiceMatchesByNameOrPrefix(t *testing.T) {
	assert.True(t, refused("com.apple.dnssd.service"))
	assert.True(t, refused("com.apple.lsd.mapdb"))
	assert.True(t, refused("com.apple.pasteboard.1"))
	assert.False(t, refused("com.apple.dnssd.service.other"))
	assert.False(t, refused("com.apple.system.logger"))
}

// The profile's Mach allowlist names no refused service, and no rule lets
// cfprefsd serve another app's preferences to the run.
func TestTheProfileNamesNoRefusedService(t *testing.T) {
	names := regexp.MustCompile(`global-name "([^"]+)"`).FindAllStringSubmatch(profileText, -1)
	require.NotEmpty(t, names)
	for _, n := range names {
		assert.False(t, refused(n[1]), n[1])
	}
	assert.NotContains(t, profileText, "user-preference")
	assert.False(t, slices.ContainsFunc(names, func(n []string) bool { return n[1] == "" }))
}

// launcher writes a stand-in for sandbox-exec: script, run with the
// launcher's arguments.
func launcher(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandbox-exec")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700))
	return path
}

// passThrough is a launcher that drops the profile's arguments and runs the
// command after them, as sandbox-exec does once the profile is applied.
const passThrough = `while [ "$1" = -p ] || [ "$1" = -D ]; do shift 2; done
exec "$@"
`

func TestWithoutSandboxExecThereIsNoSandbox(t *testing.T) {
	s, v := probe(t.Context(), filepath.Join(t.TempDir(), "sandbox-exec"), testutil.Timeout)

	assert.Nil(t, s)
	assert.Equal(t, Status{Reason: "sandbox-exec not found"}, v)
}

// A profile the launcher refuses fails the probe with its stderr's first line.
func TestAProbeThatFailsSaysWhy(t *testing.T) {
	path := launcher(t, "echo 'sandbox_apply: Operation not permitted' >&2\necho more >&2\nexit 71\n")

	s, v := probe(t.Context(), path, testutil.Timeout)

	assert.Nil(t, s)
	assert.Equal(t, Status{Reason: "sandbox_apply: Operation not permitted"}, v)
}

// A launcher that runs true under the profile is a sandbox that confines.
func TestAProbeThatRunsIsASandbox(t *testing.T) {
	s, v := probe(t.Context(), launcher(t, passThrough), testutil.Timeout)

	require.NotNil(t, s)
	assert.True(t, v.Available)
	assert.True(t, s.Confines())
}

// A probe that runs out of time keeps the sandbox, so a slow start leaves no
// command unconfined. The launcher hangs on purpose: the probe's own timeout
// ends it.
func TestAProbeThatTimesOutKeepsTheSandbox(t *testing.T) {
	s, v := probe(t.Context(), launcher(t, "exec sleep 60\n"), 10*time.Millisecond)

	require.NotNil(t, s)
	assert.Equal(t, Status{Available: true, Reason: "Seatbelt, unconfirmed: the probe did not finish within 10ms"}, v)
	assert.True(t, s.Confines())
}

// The command is sandbox-exec with the run's profile, then the run's argv: the
// shell, or for a run with a socket the forwarder with the shell its child.
func TestTheCommandIsSandboxExecOverTheProfile(t *testing.T) {
	s := &Sandbox{self: "/bin/kstack-sidecar", launcher: "/usr/bin/sandbox-exec"}
	r, _ := profileRun(t, s)
	r.Args = []string{"-c", "exit 0"}

	cmd := command(t, s, t.Context(), r)

	text, params := s.profile(r)
	assert.Equal(t, "/usr/bin/sandbox-exec", cmd.Path)
	assert.Equal(t, slices.Concat([]string{"/usr/bin/sandbox-exec", "-p", text}, params, []string{"/bin/sh", "-c", "exit 0"}), cmd.Args)
	assert.Equal(t, r.Dir, cmd.Dir)
	assert.Equal(t, r.Env, cmd.Env)

	r.Policy.Network.Relays = []Relay{{Port: 4321, Socket: "/run/p.sock"}}
	cmd = command(t, s, t.Context(), r)
	name, args := s.argv(r)
	assert.Equal(t, append([]string{name}, args...), cmd.Args[len(cmd.Args)-len(args)-1:])
}

// A run with a socket starts as this executable's forwarder, the shell its
// child; a run with none runs the shell itself.
func TestARunWithASocketStartsAsTheForwarder(t *testing.T) {
	s := &Sandbox{self: "/bin/kstack-sidecar"}
	relay := Relay{Port: 6443, Socket: "/run/p.sock"}
	r := Run{Shell: "/bin/sh", Args: []string{"-c", "--port 1"}, Policy: Policy{Network: NetworkPolicy{Relays: []Relay{relay}}}}

	name, args := s.argv(r)
	require.Equal(t, "/bin/kstack-sidecar", name)
	assert.Equal(t, append(append(ForwarderArgs(relay), r.Shell), r.Args...), args)

	r.Policy.Network.Relays = nil
	name, args = s.argv(r)
	assert.Equal(t, "/bin/sh", name)
	assert.Equal(t, r.Args, args)
}

// A profile that hangs, as one resolving a path on a stalled network mount
// does, holds the command only until its context ends.
func TestACommandWhoseProfileHangsIsRefusedWhenItsContextEnds(t *testing.T) {
	release := make(chan struct{})
	buildProfile = func(*Sandbox, Run) (string, []string) { <-release; return "", nil }
	t.Cleanup(func() { close(release); buildProfile = (*Sandbox).profile })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	cmd, err := (&Sandbox{self: "/bin/sh", launcher: "/usr/bin/true"}).Command(ctx, Run{Shell: "/bin/sh"})

	assert.Nil(t, cmd)
	assert.ErrorIs(t, err, context.Canceled)
}

// The port a run's kubeconfig dials is a free loopback one, which the
// forwarder can take.
func TestThePortIsAFreeLoopbackOne(t *testing.T) {
	port, err := (&Sandbox{}).Port()
	require.NoError(t, err)

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	require.NoError(t, err)
	_ = ln.Close()
}

// The test binary answers these when a run starts it as a process that tries
// to leave its group (leaveGroup), as the process left behind (linger), or to
// send a byte (send). init runs them before TestMain, which knows nothing of
// macOS.
const (
	leaveEnv    = "KSTACK_SANDBOX_TEST_LEAVE"
	lingerEnv   = "KSTACK_SANDBOX_TEST_LINGER"
	homeFileEnv = "KSTACK_SANDBOX_TEST_HOME_FILE"
	listenerEnv = "KSTACK_SANDBOX_TEST_LISTENER"
	sendEnv     = "KSTACK_SANDBOX_TEST_SEND"
)

func init() {
	if call := os.Getenv(leaveEnv); call != "" {
		os.Exit(leaveGroup(call))
	}
	if dir := os.Getenv(lingerEnv); dir != "" {
		os.Exit(linger(dir))
	}
	if target := os.Getenv(sendEnv); target != "" {
		os.Exit(send(target))
	}
}

// send dials target, "<network> <address>", and writes a byte, printing why
// either fails.
func send(target string) int {
	network, addr, _ := strings.Cut(target, " ")
	c, err := net.Dial(network, addr)
	if err == nil {
		_, err = c.Write([]byte("x"))
		_ = c.Close()
	}
	if err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}

// leaveGroup makes call, setsid or setpgid, and prints what it answered.
// Started by a shell, it leads no group, so outside the sandbox either succeeds.
func leaveGroup(call string) int {
	var err error
	if call == "setsid" {
		_, err = syscall.Setsid()
	} else {
		err = syscall.Setpgid(0, 0)
	}
	fmt.Println(err)
	return 0
}

// linger waits on dir's go FIFO until the test has killed the run's group,
// then tries the stand-in home and a loopback listener and writes what it got.
func linger(dir string) int {
	f, err := os.Open(filepath.Join(dir, "go"))
	if err != nil {
		return 1
	}
	_, _ = io.Copy(io.Discard, f)
	_ = f.Close()
	home, network := "read", "reached"
	if _, err := os.ReadFile(os.Getenv(homeFileEnv)); err != nil {
		home = "denied"
	}
	if c, err := net.Dial("tcp", os.Getenv(listenerEnv)); err != nil {
		network = "refused"
	} else {
		_ = c.Close()
	}
	result := filepath.Join(dir, "result")
	if err := os.WriteFile(result+".tmp", []byte(home+" "+network), 0o600); err != nil {
		return 1
	}
	return errCode(os.Rename(result+".tmp", result))
}

func errCode(err error) int {
	if err != nil {
		return 1
	}
	return 0
}

// machineRun is a run over a stand-in machine, as the Bash tool builds one: a
// home, and Kstack's data, cache and runtime directories holding the
// workspace, the run's TMPDIR, the kubectl cache, the snapshot and the run's
// directory. Its base is in the per-user temp directory, passed as /var/…
// unresolved as the runtime directory is, and short, so a socket fits.
type machineRun struct {
	Run
	base, home, data, cache, runtime, runDir string
	ws, snapshot, tmp, kubectl, socket       string
	port                                     int
}

// on is m as it runs on s, its policy the Workspace policy: System less what
// lies in Kstack's directories, the Never paths and Kstack's directories
// denied, the run's own paths inside them, and with a cluster one relay.
func (m *machineRun) on(s *Sandbox) Run {
	r := m.Run
	kstack := []string{m.data, m.cache, m.runtime}
	r.Policy = Policy{
		Files: s.System(m.home, r.Shell).Files.Outside(kstack...),
		Always: AlwaysPolicy{
			Deny: s.Never(m.home), Kstack: kstack,
			Read: []string{m.snapshot, m.runDir}, Write: []string{m.ws, m.tmp, m.kubectl},
		},
	}
	if m.socket != "" {
		r.Policy.Network.Relays = []Relay{{Port: m.port, Socket: m.socket}}
	}
	return r
}

func standIn(t *testing.T) *machineRun {
	t.Helper()
	base, err := os.MkdirTemp("", "kst")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	m := &machineRun{base: base}
	m.home, m.data, m.cache, m.runtime = filepath.Join(base, "home"), filepath.Join(base, "data"), filepath.Join(base, "cache"), filepath.Join(base, "runtime")
	d := mkdirs(t, base, "home", "data/chats/c/workspace", "cache/tmp/1-a", "cache/kubectl/c/s", "runtime/runs/1-a", "runtime/shell")
	ws, tmp, kubectl := d[1], d[2], d[3]
	m.runDir = d[4]
	snapshot := filepath.Join(d[5], "snapshot.sh")
	require.NoError(t, os.WriteFile(snapshot, []byte("true\n"), 0o400))
	SeedTmpDir(tmp)
	m.ws, m.snapshot, m.tmp, m.kubectl = ws, snapshot, tmp, kubectl
	m.Run = Run{
		Shell: "/bin/sh",
		Dir:   ws,
		Env:   []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + ws, "TMPDIR=" + tmp},
	}
	return m
}

// withCluster gives the run a socket, serving HTTP that answers ok, and a
// free port for its forwarder.
func (m *machineRun) withCluster(t *testing.T) {
	t.Helper()
	m.socket = filepath.Join(m.runDir, "proxy.sock")
	serveHTTP(t, "unix", m.socket, "ok")
	m.port = freePort(t)
	m.Env = append(m.Env, "PORT="+strconv.Itoa(m.port), "SOCKET="+m.socket)
}

// serveHTTP answers body to every request on a listener of network at addr,
// and answers the address it listens on.
func serveHTTP(t *testing.T, network, addr, body string) string {
	t.Helper()
	ln, err := net.Listen(network, addr)
	require.NoError(t, err)
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }),
		ReadHeaderTimeout: testutil.Timeout,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// sh runs script under the sandbox with env added, and answers its output and
// whether it exited 0.
func sh(t *testing.T, s *Sandbox, r Run, script string, env ...string) (string, bool) {
	t.Helper()
	out, ok, _ := shWithin(t, s, r, testutil.Timeout, script, env...)
	return out, ok
}

// shWithin is sh bounded by d, and answers whether d ran out.
func shWithin(t *testing.T, s *Sandbox, r Run, d time.Duration, script string, env ...string) (string, bool, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	r.Args = []string{"-c", script}
	r.Env = slices.Concat(r.Env, env)
	out, err := command(t, s, ctx, r).CombinedOutput()
	return string(out), err == nil, ctx.Err() != nil
}

// firstLine is what a run printed first.
func firstLine(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	return line
}

// write makes a file holding "secret" under dir.
func write(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))
	return path
}

// These are the listed programs the profile's rules and Mach services answer
// to. A Go binary is among them: a run with a cluster starts the test binary
// as its forwarder.
func TestTheListedProgramsRun(t *testing.T) {
	s := confining(t)
	hasTools := exec.Command("xcode-select", "-p").Run() == nil
	for _, p := range []struct{ name, script string }{
		{"sh", "/bin/sh -c true"},
		{"bash", "/bin/bash -c true"},
		{"zsh", "/bin/zsh -c true"},
		{"perl", "/usr/bin/perl -e 1"},
		{"python3", "/usr/bin/python3 -c pass"},
		{"git", "/usr/bin/git --version"},
		{"awk", "awk 'BEGIN { exit 0 }'"},
		{"sed", "echo a | sed s/a/b/"},
		{"grep", "echo a | grep -q a"},
		{"sort", "printf 'b\\na\\n' | sort"},
		{"tar", `tar -cf "$TMPDIR/t.tar" -C "$HOME" .`},
		{"mktemp", `f=$(mktemp) && case "$f" in "$TMPDIR"*) ;; *) exit 1 ;; esac`},
		{"curl", `curl -sS --fail "http://127.0.0.1:$PORT/"`},
	} {
		t.Run(p.name, func(t *testing.T) {
			if p.name == "git" || p.name == "python3" {
				if !hasTools {
					t.Skip("no Command Line Tools: /usr/bin/" + p.name + " only offers to install them")
				}
				// A user who has run the tool has it in xcrun's cache, which
				// the run starts with.
				require.NoError(t, exec.Command("/bin/sh", "-c", p.script).Run())
			}
			m := standIn(t)
			m.withCluster(t)

			out, ok := sh(t, s, m.on(s), p.script)

			assert.True(t, ok, out)
		})
	}
}

func TestTheHomeIsUnreadable(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	file := write(t, m.home, "secret")

	out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+file)
	assert.False(t, ok, out)
	out, ok = sh(t, s, m.on(s), `ls "$F"`, "F="+m.home)
	assert.False(t, ok, out)
}

func TestACredentialPathInsideAReadableTreeIsUnreadable(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	bin := mkdirs(t, m.home, ".cargo/bin")[0]
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\necho tool\n"), 0o700))
	creds := write(t, m.home, ".cargo/credentials.toml")
	addToolchain(t, "~/.cargo")
	m.Env[0] = "PATH=" + bin + ":/usr/bin:/bin"

	out, ok := sh(t, s, m.on(s), "tool")
	assert.True(t, ok, out)
	assert.Equal(t, "tool\n", out)
	out, ok = sh(t, s, m.on(s), `cat "$F"`, "F="+creds)
	assert.False(t, ok, out)
}

// ~/.aws linked into ~/tools is unreadable at its target, which the ~/tools
// toolchain folder takes in.
func TestALinkedCredentialPathIsUnreadable(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	bin := mkdirs(t, m.home, "tools/bin")[0]
	secret := write(t, m.home, "tools/aws/credentials")
	require.NoError(t, os.Symlink(filepath.Dir(secret), filepath.Join(m.home, ".aws")))
	addToolchain(t, "~/tools")
	m.Env[0] = "PATH=" + bin + ":/usr/bin:/bin"

	out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+secret)

	assert.False(t, ok, out)
}

// Homebrew's var is denied inside a tree that takes it in; a stand-in plays it.
func TestHomebrewsVarIsDenied(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	brew := mkdirs(t, m.base, "brew")[0]
	db := write(t, brew, "var/db")
	readme := write(t, brew, "readme")
	saved := brewVar
	brewVar = []string{filepath.Join(brew, "var")}
	t.Cleanup(func() { brewVar = saved })
	addRoot(t, brew)

	out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+db)
	assert.False(t, ok, out)
	out, ok = sh(t, s, m.on(s), `cat "$F"`, "F="+readme)
	assert.True(t, ok, out)
}

// /etc is read whole and its secret files are not, whatever their mode: a
// stand-in plays it, and the real /etc/hosts still reads.
func TestEtcSecretsStayHidden(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	etc := filepath.Join(resolved(m.base), "etc")
	hosts := write(t, etc, "hosts")
	require.NoError(t, os.WriteFile(hosts, []byte("hosts-read"), 0o644))
	key := write(t, etc, "ssh/ssh_host_ed25519_key")
	shadow := write(t, etc, "shadow")
	for _, f := range []string{key, shadow} {
		require.NoError(t, os.Chmod(f, 0o644))
	}
	addRoot(t, etc)
	old := platformLists
	platformLists.Never = append(slices.Clone(platformLists.Never), filepath.Join(etc, "ssh"), shadow)
	t.Cleanup(func() { platformLists = old })

	out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+hosts)
	assert.True(t, ok, out)
	assert.Equal(t, "hosts-read", out)
	for _, f := range []string{key, shadow} {
		out, ok = sh(t, s, m.on(s), `cat "$F"`, "F="+f)
		assert.False(t, ok, out)
	}

	out, ok = sh(t, s, m.on(s), "cat /etc/hosts >/dev/null && echo read")
	assert.True(t, ok, out)
	assert.Equal(t, "read\n", out)
}

// A folder on the sidecar's PATH opens nothing: only the lists decide what a
// run reads.
func TestAFolderOnThePathIsNotRead(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	bin := mkdirs(t, m.home, "notes/bin")[0]
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\necho tool\n"), 0o700))
	notes := write(t, m.home, "notes/todo")
	m.Env[0] = "PATH=" + bin + ":/usr/bin:/bin"

	out, ok := sh(t, s, m.on(s), "tool")
	assert.False(t, ok, out)
	out, ok = sh(t, s, m.on(s), `cat "$F"`, "F="+notes)
	assert.False(t, ok, out)
	out, ok = sh(t, s, m.on(s), `ls "$F"`, "F="+filepath.Dir(notes))
	assert.False(t, ok, out)
}

// A Read of the home leaves the Closed folders shut, and a Read of a folder
// inside one opens that folder.
func TestAGrantOfTheHomeLeavesClosedFoldersShut(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	notes := write(t, m.home, "notes")
	private := write(t, m.home, "Documents/private")
	project := write(t, m.home, "Documents/project/main.go")
	r := m.on(s)
	r.Policy.Files.Read = append(r.Policy.Files.Read, m.home)

	out, ok := sh(t, s, r, `cat "$F"`, "F="+notes)
	assert.True(t, ok, out)
	for _, f := range []string{private, project} {
		out, ok = sh(t, s, r, `cat "$F"`, "F="+f)
		assert.False(t, ok, out)
	}

	r.Policy.Files.Read = append(r.Policy.Files.Read, filepath.Dir(project))
	out, ok = sh(t, s, r, `cat "$F"`, "F="+project)
	assert.True(t, ok, out)
	out, ok = sh(t, s, r, `cat "$F"`, "F="+private)
	assert.False(t, ok, out)
}

// Each Toolchain location runs a program from its first folder, with its
// variables set and pointing into the home.
func TestEachToolchainLocationRunsAProgram(t *testing.T) {
	s := confining(t)
	for _, l := range sharedLists.Toolchain {
		t.Run(l.Name, func(t *testing.T) {
			m := standIn(t)
			dir := inHome(m.home, l.Read[:1])[0]
			if filepath.Base(dir) != "bin" {
				dir = filepath.Join(dir, "bin")
			}
			require.NoError(t, os.MkdirAll(dir, 0o700))
			prog := filepath.Join(dir, "kstack-tool")
			require.NoError(t, os.WriteFile(prog, []byte("#!/bin/sh\necho tool-ran\n"), 0o700))
			r := m.on(s)
			r.Env = append(r.Env, s.System(m.home, r.Shell).Env...)
			script := `"$P"`
			for name := range l.Env {
				script += `; printf '%s\n' "$` + name + `"`
			}

			out, ok := sh(t, s, r, script, "P="+prog)

			require.True(t, ok, out)
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			assert.Equal(t, "tool-ran", lines[0])
			require.Len(t, lines, 1+len(l.Env))
			for _, v := range lines[1:] {
				assert.True(t, within(v, m.home), "%s is not under the home", v)
			}
		})
	}
}

func TestKstacksDirectoriesAreUnreadable(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	assert.True(t, strings.HasPrefix(m.base, "/var/"), "the run's paths are passed unresolved: %s", m.base)
	for _, f := range []string{write(t, m.data, "app.db"), write(t, m.cache, "kubestore/c.db"), write(t, m.runtime, "host.sock"), write(t, m.runtime, "shell/other")} {
		out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+f)
		assert.False(t, ok, "%s: %s", f, out)
	}
	for _, f := range []string{
		write(t, m.ws, "f"), m.snapshot, write(t, m.runDir, "kubeconfig"),
		write(t, m.tmp, "f"), write(t, m.kubectl, "f"),
	} {
		out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+f)
		assert.True(t, ok, "%s: %s", f, out)
	}
}

func TestASiblingRunsKubeconfigIsUnreadable(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	m.withCluster(t)
	sibling := mkdirs(t, m.runtime, "runs/1-b")[0]
	kubeconfig := write(t, sibling, "kubeconfig")
	socket := filepath.Join(sibling, "proxy.sock")
	serveHTTP(t, "unix", socket, "sibling")

	out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+kubeconfig)
	assert.False(t, ok, out)
	out, _ = sh(t, s, m.on(s), `curl -sS --unix-socket "$F" http://x/`, "F="+socket)
	assert.NotContains(t, out, "sibling")
}

func TestTheTempDirectoriesAreDenied(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	for _, base := range []string{"", "/tmp"} {
		dir, err := os.MkdirTemp(base, "kst-outside")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		file := write(t, dir, "f")

		out, ok := sh(t, s, m.on(s), `cat "$F"`, "F="+file)
		assert.False(t, ok, "%s: %s", file, out)
		out, ok = sh(t, s, m.on(s), `echo x > "$D/new"`, "D="+dir)
		assert.False(t, ok, "%s: %s", dir, out)
		assert.NoFileExists(t, filepath.Join(dir, "new"))
	}
}

func TestOnlyTheWorkspaceAndTheCacheAreWritten(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	for _, dir := range []string{m.ws, m.tmp, m.kubectl} {
		out, ok := sh(t, s, m.on(s), `echo x > "$D/new"`, "D="+dir)
		assert.True(t, ok, "%s: %s", dir, out)
	}
	for _, dir := range []string{m.home, filepath.Dir(m.ws), m.runDir, filepath.Dir(m.snapshot), m.base} {
		out, ok := sh(t, s, m.on(s), `echo x > "$D/new"`, "D="+dir)
		assert.False(t, ok, "%s: %s", dir, out)
		assert.NoFileExists(t, filepath.Join(dir, "new"))
	}
}

// A run cannot remove, rename or replace the root of a path it writes, so it
// cannot leave a link there for the next run's profile to resolve. What lies
// inside stays its own.
func TestARunCannotReplaceItsWorkspace(t *testing.T) {
	s := confining(t)
	for _, script := range []string{
		`cd /; rm -rf "$W" && ln -s "$D" "$W"`,
		`cd /; rmdir "$W"`,
		`cd /; mv "$W" "$O/moved"`,
		`mkdir "$O/x" && perl -e 'rename($ARGV[0], $ARGV[1]) or die "$!\n"' "$O/x" "$W"`,
		`ln -s "$D" "$O/l" && perl -e 'rename($ARGV[0], $ARGV[1]) or die "$!\n"' "$O/l" "$W"`,
	} {
		m := standIn(t)
		for _, pair := range [][2]string{{m.ws, m.kubectl}, {m.kubectl, m.ws}} {
			w, other := pair[0], pair[1]
			out, ok := sh(t, s, m.on(s), script, "W="+w, "D="+m.data, "O="+other)
			assert.False(t, ok, "%s on %s: %s", script, w, out)
			info, err := os.Lstat(w)
			require.NoError(t, err, script)
			assert.True(t, info.IsDir(), "%s on %s left %v", script, w, info.Mode())
		}
	}

	m := standIn(t)
	for _, w := range []string{m.ws, m.kubectl} {
		out, ok := sh(t, s, m.on(s), `mkdir "$W/a" && echo x > "$W/a/f" && mv "$W/a" "$W/b" && rm -rf "$W/b" && ln -s /usr "$W/l" && rm "$W/l"`, "W="+w)
		assert.True(t, ok, "%s: %s", w, out)
	}
}

func TestAnAncestorCanBeStatted(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	parent := filepath.Dir(m.ws)

	out, ok := sh(t, s, m.on(s), `stat "$D"`, "D="+parent)
	assert.True(t, ok, out)
	out, ok = sh(t, s, m.on(s), `ls "$D"`, "D="+parent)
	assert.False(t, ok, out)
}

func TestTheEnvironmentIsExactlyGiven(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	m.Shell = "/usr/bin/env"

	out, err := command(t, s, t.Context(), m.on(s)).Output()

	require.NoError(t, err)
	assert.ElementsMatch(t, m.Env, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"))
}

// The keychain file is readable inside, so a lookup that fails is the Mach
// denial's.
func TestTheKeychainIsDenied(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	keychain := filepath.Join(m.ws, "k.keychain")
	service := "kstack-test-" + rand.Text()
	security := func(args ...string) error { return exec.Command("security", args...).Run() }
	require.NoError(t, security("create-keychain", "-p", "pw", keychain))
	// create-keychain adds it to the user's search list; delete-keychain takes it off.
	t.Cleanup(func() { _ = security("delete-keychain", keychain) })
	require.NoError(t, security("add-generic-password", "-a", "kstack", "-s", service, "-w", "secret", keychain))
	require.NoError(t, security("find-generic-password", "-s", service, keychain), "found outside")

	// A negative assertion: a lookup that reached the Keychain would answer at once.
	out, ok, late := shWithin(t, s, m.on(s), time.Second, `security find-generic-password -s "$S" "$K"`, "S="+service, "K="+keychain)

	assert.False(t, late, "the lookup did not fail within a second")
	assert.False(t, ok, out)
}

func TestNoAppIsOpenedOrDriven(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	pasteboard := func(stdin string) (string, error) {
		copyCmd := exec.Command("pbcopy")
		copyCmd.Stdin = strings.NewReader(stdin)
		if err := copyCmd.Run(); err != nil {
			return "", err
		}
		out, err := exec.Command("pbpaste").Output()
		return string(out), err
	}
	prev, _ := exec.Command("pbpaste").Output()
	marker := "kstack-test-" + rand.Text()
	if got, err := pasteboard(marker); err != nil || got != marker {
		t.Skip("no pasteboard to test against outside the sandbox")
	}
	t.Cleanup(func() { _, _ = pasteboard(string(prev)) })

	// Each is a negative assertion bounded at 15 seconds: a profile that let
	// the Apple Event through would wait on a privacy prompt, which never ends,
	// while a refusal under a loaded suite can take over five seconds.
	// AppleScript answers an application's own name from its bundle, and
	// terms need Finder's dictionary, so the query is raw codes Finder alone
	// can answer.
	for _, script := range []string{
		"open -g https://example.invalid/",
		`osascript -e 'tell application "Finder" to get «property pnam» of «property sdsk»'`,
		"echo inside | pbcopy",
	} {
		out, ok, late := shWithin(t, s, m.on(s), 15*time.Second, script)
		assert.False(t, late, "%s did not end within 15 seconds", script)
		assert.False(t, ok, "%s: %s", script, out)
	}
	out, _, late := shWithin(t, s, m.on(s), 15*time.Second, "pbpaste")
	assert.False(t, late)
	assert.NotContains(t, out, marker)
	got, err := exec.Command("pbpaste").Output()
	require.NoError(t, err)
	assert.Equal(t, marker, string(got), "pbcopy inside changed nothing")
}

// cfprefsd reads another app's preferences for its caller unless the profile
// refuses user-preference-read.
func TestNoServiceReadsForTheRun(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	domain := "org.kubetail.kstack.test." + strings.ToLower(rand.Text())
	require.NoError(t, exec.Command("defaults", "write", domain, "k", "kstack-value").Run())
	t.Cleanup(func() { _ = exec.Command("defaults", "delete", domain).Run() })
	got, err := exec.Command("defaults", "read", domain, "k").Output()
	require.NoError(t, err)
	require.Equal(t, "kstack-value\n", string(got))

	out, ok := sh(t, s, m.on(s), `defaults read "$D" k`, "D="+domain)

	assert.False(t, ok, out)
	assert.NotContains(t, out, "kstack-value")
}

func TestNoOtherProcessIsRead(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	arg, value := "kstack-arg-"+rand.Text(), "kstack-env-"+rand.Text()
	// Two commands, so sh stays the process and keeps its arguments.
	other := exec.Command("/bin/sh", "-c", "sleep 60; :", arg)
	other.Env = []string{"KSTACK_TEST_MARKER=" + value}
	require.NoError(t, other.Start())
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	pid := strconv.Itoa(other.Process.Pid)
	outside, err := exec.Command("ps", "-E", "-ww", "-p", pid).Output()
	require.NoError(t, err)
	require.Contains(t, string(outside), arg)

	out, _ := sh(t, s, m.on(s), `ps -E -ww -p "$P"`, "P="+pid)

	assert.NotContains(t, out, arg)
	assert.NotContains(t, out, value)
}

func TestAPublicNameDoesNotResolve(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	if _, err := net.LookupHost("example.com"); err != nil {
		t.Skip("example.com does not resolve outside the sandbox: ", err)
	}

	// A negative assertion bounded at a second: a lookup that left the machine
	// would take longer, or answer.
	out, ok, late := shWithin(t, s, m.on(s), time.Second, "curl -sS --max-time 5 http://example.com/")

	assert.False(t, late, "the lookup did not fail within a second")
	assert.False(t, ok, out)
	assert.Contains(t, out, "Could not resolve host")
}

func TestOnlyTheRunsPortAndSocketAreReached(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	m.withCluster(t)
	other := serveHTTP(t, "tcp", "127.0.0.1:0", "other")
	otherSocket := filepath.Join(m.ws, "o.sock")
	serveHTTP(t, "unix", otherSocket, "other")

	// Under coverage the forwarder, this test binary, warns as it exits, since
	// its GOCOVERDIR is outside the sandbox, so curl's line is ended and read
	// alone.
	out, ok := sh(t, s, m.on(s), `curl -sS "http://127.0.0.1:$PORT/" && echo`)
	assert.True(t, ok, out)
	assert.Equal(t, "ok", firstLine(out))
	out, ok = sh(t, s, m.on(s), `curl -sS --unix-socket "$SOCKET" http://x/ && echo`)
	assert.True(t, ok, out)
	assert.Equal(t, "ok", firstLine(out))
	out, _ = sh(t, s, m.on(s), `curl -sS "http://$A/"`, "A="+other)
	assert.NotContains(t, out, "other")
	out, _ = sh(t, s, m.on(s), `curl -sS --unix-socket "$F" http://x/`, "F="+otherSocket)
	assert.NotContains(t, out, "other")
}

// The port is reached over TCP on 127.0.0.1 alone, the endpoint the forwarder
// holds: UDP or IPv6 at that number could be another service's.
func TestThePortIsReachedOverIPv4TCPAlone(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	m.withCluster(t)
	port := strconv.Itoa(m.port)
	for _, target := range []string{"udp4 127.0.0.1:" + port, "tcp6 [::1]:" + port, "udp6 [::1]:" + port} {
		out, ok := sh(t, s, m.on(s), sendEnv+`="$T" "$BIN"`, "T="+target, "BIN="+os.Args[0])

		assert.False(t, ok, target)
		assert.Contains(t, out, "operation not permitted", target)
	}
	out, ok := sh(t, s, m.on(s), sendEnv+`="$T" "$BIN"`, "T=tcp4 127.0.0.1:"+port, "BIN="+os.Args[0])
	assert.True(t, ok, out)
}

func TestARunWithoutAClusterHasNoNetwork(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	addr := serveHTTP(t, "tcp", "127.0.0.1:0", "reached")

	out, _ := sh(t, s, m.on(s), `curl -sS "http://$A/"`, "A="+addr)

	assert.NotContains(t, out, "reached")
}

func TestAPathIsAParameter(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	ws := mkdirs(t, m.data, "chats/we\"ird)\n(allow default)/workspace")[0]
	m.ws, m.Dir = ws, ws
	file := write(t, m.home, "secret")

	out, ok := sh(t, s, m.on(s), "echo x > f && cat f")
	assert.True(t, ok, out)
	assert.Equal(t, "x\n", out)
	out, ok = sh(t, s, m.on(s), `cat "$F"`, "F="+file)
	assert.False(t, ok, out)
}

func TestATakenPortFailsTheRun(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	m.withCluster(t)
	held, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.port)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })

	m.Args = []string{"-c", "echo started"}
	out, err := command(t, s, t.Context(), m.on(s)).CombinedOutput()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, initFailed, exit.ExitCode())
	assert.Contains(t, string(out), "sandbox-init: cannot listen on 127.0.0.1:"+strconv.Itoa(m.port))
	assert.NotContains(t, string(out), "started")
}

// The profile refuses setsid and setpgid, so a process cannot take itself out
// of the run's group.
func TestAProcessCannotLeaveItsGroup(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	for _, call := range []string{"setsid", "setpgid"} {
		outside, err := exec.Command("/bin/sh", "-c", leaveEnv+`="$1" "$0"`, os.Args[0], call).CombinedOutput()
		require.NoError(t, err, string(outside))
		require.Equal(t, "<nil>\n", string(outside), "%s succeeds outside", call)

		out, ok := sh(t, s, m.on(s), leaveEnv+`="$C" "$BIN"`, "C="+call, "BIN="+os.Args[0])

		assert.True(t, ok, out)
		assert.Equal(t, "operation not permitted\n", out, call)
	}
}

// macOS has no PID namespace, and Seatbelt cannot refuse posix_spawn's
// POSIX_SPAWN_SETSID: a process spawned that way outlives the group's kill,
// and stays under the run's profile.
func TestAProcessThatLeavesTheGroupStaysConfined(t *testing.T) {
	s := confining(t)
	if exec.Command("xcode-select", "-p").Run() != nil {
		t.Skip("no Command Line Tools: /usr/bin/python3 only offers to install them")
	}
	m := standIn(t)
	fifo := filepath.Join(m.ws, "go")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	listener := serveHTTP(t, "tcp", "127.0.0.1:0", "reached")
	m.Env = append(m.Env, lingerEnv+"="+m.ws, homeFileEnv+"="+write(t, m.home, "secret"), listenerEnv+"="+listener, "BIN="+os.Args[0])
	// Its output goes to /dev/null, or the run's pipe stays open behind it.
	m.Args = []string{"-c", `exec /usr/bin/python3 -c 'import os, sys
null = [(os.POSIX_SPAWN_OPEN, fd, "/dev/null", os.O_WRONLY, 0) for fd in (1, 2)]
pid = os.posix_spawn(sys.argv[1], [sys.argv[1]], os.environ, file_actions=null, setsid=True)
open("pid", "w").write(str(pid))' "$BIN"`}

	cmd := command(t, s, t.Context(), m.on(s))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	raw, err := os.ReadFile(filepath.Join(m.ws, "pid"))
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(raw))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	// Opening the FIFO waits for linger to open it too.
	opened := make(chan *os.File, 1)
	go func() {
		if f, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			opened <- f
		}
	}()
	f := testutil.Recv(t, opened, "the process left behind opens its FIFO")
	require.NoError(t, f.Close())
	result := filepath.Join(m.ws, "result")
	require.Eventually(t, func() bool { _, err := os.Stat(result); return err == nil }, testutil.Timeout, 10*time.Millisecond)
	got, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Equal(t, "denied refused", string(got))
}

// A run started with a controlling terminal still cannot open it. script(1)
// gives its command a terminal of its own, as a sidecar started from one has.
func TestATerminalIsUnreachable(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	opened := `: < /dev/tty && echo opened || echo refused`
	outside, err := exec.Command("/usr/bin/script", "-q", "/dev/null", "/bin/sh", "-c", opened).CombinedOutput()
	require.NoError(t, err, string(outside))
	require.Contains(t, string(outside), "opened", "script gives its command a terminal")

	m.Args = []string{"-c", opened}
	run := command(t, s, t.Context(), m.on(s))
	cmd := exec.CommandContext(t.Context(), "/usr/bin/script", slices.Concat([]string{"-q", "/dev/null"}, run.Args)...)
	cmd.Dir, cmd.Env = run.Dir, run.Env
	out, err := cmd.CombinedOutput()

	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "refused")
	assert.NotContains(t, string(out), "opened")
}

// inlined is a profile's text with each parameter's value in place of its
// name, one line per rule, less the ancestor rules above the fixture.
func (f fixture) inlined(t *testing.T, text string, args []string) []string {
	t.Helper()
	for name, value := range paramsOf(t, args) {
		text = strings.ReplaceAll(text, `(param "`+name+`")`, strconv.Quote(value))
	}
	return slices.DeleteFunc(strings.Split(strings.TrimSuffix(text, "\n"), "\n"), f.aboveFixture)
}

// The profile over the fixture is the golden's, with a cluster and without.
func TestTheCompiledProfileMatchesTheGolden(t *testing.T) {
	f := newFixture(t)
	old := brewVar
	brewVar = f.brewVar
	t.Cleanup(func() { brewVar = old })
	s := &Sandbox{self: f.self}
	for name, cluster := range map[string]bool{"cluster": true, "no-cluster": false} {
		t.Run(name, func(t *testing.T) {
			text, args := s.profile(f.run(s, cluster))
			f.golden(t, "profile_darwin_"+name+".golden", f.inlined(t, text, args))
		})
	}
}

// A policy that fails Check answers its error and no command.
func TestAPolicyThatFailsCheckStartsNothing(t *testing.T) {
	s := &Sandbox{self: "/bin/sh", launcher: "/usr/bin/sandbox-exec"}
	r := Run{Shell: "/bin/sh", Dir: "/w", Policy: Policy{Files: FilePolicy{Read: []string{"relative"}}}}

	cmd, err := s.Command(t.Context(), r)

	assert.Nil(t, cmd)
	assert.Error(t, err)
}

// A Deny holds for a path that does not exist when the profile is made: a
// file made there afterwards cannot be read. The home is under /var, a link,
// so the Deny is resolved through the deepest folder that exists.
func TestADenyHoldsForAPathMadeLater(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	r := m.on(s)
	r.Policy.Files.Read = append(r.Policy.Files.Read, m.home)
	r.Args = []string{"-c", `cat "$F"`}
	r.Env = append(r.Env, "F="+filepath.Join(m.home, ".ssh", "id_ed25519"))

	cmd := command(t, s, t.Context(), r)
	write(t, filepath.Join(m.home, ".ssh"), "id_ed25519")
	out, err := cmd.CombinedOutput()

	assert.Error(t, err)
	assert.NotContains(t, string(out), "secret")
}

// A Read and a Write rule whose paths are missing are skipped: the run
// starts, and it cannot make either path.
func TestAMissingReadOrWritePathIsSkipped(t *testing.T) {
	s := confining(t)
	m := standIn(t)
	read, written := filepath.Join(m.base, "read"), filepath.Join(m.base, "written")
	r := m.on(s)
	r.Policy.Files.Read = append(r.Policy.Files.Read, read)
	r.Policy.Files.Write = append(r.Policy.Files.Write, written)

	out, _ := sh(t, s, r, `mkdir -p "$R" "$W" 2>/dev/null; touch "$R/x" "$W/x" 2>/dev/null; echo started`, "R="+read, "W="+written)

	assert.Equal(t, "started\n", out)
	assert.NoDirExists(t, read)
	assert.NoDirExists(t, written)
}

// The probe's run writes its own folder and reads its shell: the profile it
// hands the launcher names both.
func TestTheProbeHandsTheLauncherItsPolicy(t *testing.T) {
	check := `printf '%s\n' "$@" | grep -q '^RULE_[0-9]*=.*kstack-probe-' || { echo 'no probe folder' >&2; exit 1; }
printf '%s\n' "$@" | grep -q '^RULE_[0-9]*=/usr$' || { echo 'no /usr' >&2; exit 1; }
`
	s, v := probe(t.Context(), launcher(t, check+passThrough), testutil.Timeout)

	require.NotNil(t, s, v.Reason)
	assert.True(t, v.Available)
}

// Command refuses a run holding a variable no run may hold.
func TestARunWithAnUnpassableVariableIsRefused(t *testing.T) {
	s := &Sandbox{self: "/bin/sh", launcher: "/usr/bin/sandbox-exec"}
	for _, kv := range []string{"LD_PRELOAD=/x.so", "AWS_SESSION_TOKEN=t"} {
		cmd, err := s.Command(t.Context(), Run{Shell: "/bin/sh", Dir: "/", Env: []string{kv}})

		assert.Nil(t, cmd, kv)
		assert.ErrorContains(t, err, "may not pass", kv)
	}
}
