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
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first system bwrap that exists, in the list's order, comes first; then
// Kstack's own, beside its executable; none where none exists.
func TestTheSystemBwrapIsFoundFirst(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "usr", "bin")
	system := []string{filepath.Join(base, "a", "bwrap"), filepath.Join(base, "b", "bwrap"), filepath.Join(base, "c", "bwrap")}
	write(t, system[1], "")
	write(t, system[2], "")

	assert.Empty(t, bwrapPaths(bin, system[:1]))
	assert.Equal(t, []string{system[1]}, bwrapPaths(bin, system))

	own := write(t, filepath.Join(base, "usr", "lib", "kstack", "bwrap"), "")
	assert.Equal(t, []string{system[1], own}, bwrapPaths(bin, system))
	assert.Equal(t, []string{own}, bwrapPaths(bin, system[:1]))
}

// seq reports whether args holds want as a run of consecutive arguments.
func seq(args []string, want ...string) bool {
	for i := range args {
		if i+len(want) <= len(args) && slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// A run starts bwrap with the run's environment, its namespaces first and the
// chain last: the forwarder, then the shell launcher, then the shell.
func TestTheCommandStartsBwrapOverTheChain(t *testing.T) {
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "opt/k", "w/sub", "c/tmp/1", "c/kubectl", "run/r1")
	self := write(t, filepath.Join(d[0], "kstack-sidecar"), "")
	snap := write(t, filepath.Join(base, "snap.sh"), "")
	w := filepath.Join(base, "w")
	socket := filepath.Join(d[4], "proxy.sock")
	s := &Sandbox{self: self, bwrap: "/usr/bin/bwrap"}
	r := Run{
		Shell: "/bin/bash", Args: []string{"-c", "ls"}, Dir: d[1], Env: []string{"A=1"},
		Policy: Policy{
			Files:   FilePolicy{Read: []string{snap, d[4], self}, Write: []string{w, d[2], d[3]}},
			Network: NetworkPolicy{Relays: []Relay{{Port: 6443, Socket: socket}}},
		},
	}

	cmd := command(t, s, context.Background(), r)

	assert.Equal(t, "/usr/bin/bwrap", cmd.Path)
	assert.Equal(t, []string{"A=1"}, cmd.Env)
	args := cmd.Args[1:]
	assert.Equal(t, []string{
		"--unshare-net", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try",
		"--die-with-parent", "--new-session", "--as-pid-1",
	}, args[:8])
	assert.True(t, seq(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp"), args)
	for _, p := range []string{snap, d[4], self} {
		assert.True(t, seq(args, "--ro-bind", p, p), p)
	}
	for _, p := range []string{w, d[2], d[3]} {
		assert.True(t, seq(args, "--bind", p, p), p)
	}
	assert.True(t, seq(args, "--remount-ro", "/"), args)
	assert.Equal(t, []string{
		"--chdir", d[1], "--",
		self, InitCommand, "--socket", socket, "--port", "6443", "--",
		self, ShellCommand, "--", "/bin/bash", "-c", "ls",
	}, args[len(args)-16:])
}

// A run with no cluster names no socket, and its forwarder listens on nothing.
func TestARunWithNoSocketStartsTheForwarderWithNoFlags(t *testing.T) {
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}

	args := command(t, s, context.Background(), Run{Shell: "/bin/sh", Args: []string{"-c", "true"}, Dir: "/w"}).Args

	assert.Equal(t, []string{"--", "/k", InitCommand, "--", "/k", ShellCommand, "--", "/bin/sh", "-c", "true"}, args[len(args)-10:])
	assert.False(t, slices.Contains(args, "--socket"))
}

// mounts is the arguments that mount r's rules: those after the fixed mounts
// and before the closing remounts.
func mounts(t *testing.T, s *Sandbox, r Run) []string {
	t.Helper()
	args := command(t, s, context.Background(), r).Args
	from := index(args, "--tmpfs", "/tmp") + 2
	return args[from:index(args, "--remount-ro", "/")]
}

// Each Read rule that exists is bound read-only at its resolved path, and one
// that does not, or links to nothing, is left out. A rule that is a link is
// recreated as one, and its target is bound unless a Read rule holds it.
func TestTheReadRulesAreBound(t *testing.T) {
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "usr/bin", "var/opt")
	usr, varOpt := filepath.Join(base, "usr"), d[1]
	bin := filepath.Join(base, "bin")
	require.NoError(t, os.Symlink("usr/bin", bin))
	opt := filepath.Join(base, "opt")
	require.NoError(t, os.Symlink(varOpt, opt))
	dangling := filepath.Join(base, "lib64")
	require.NoError(t, os.Symlink("usr/lib64", dangling))
	missing := filepath.Join(base, "lib32")
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}

	got := mounts(t, s, Run{Shell: "/bin/sh", Dir: usr, Policy: Policy{Files: FilePolicy{Read: []string{usr, bin, opt, dangling, missing}}}})

	assert.Equal(t, []string{
		"--ro-bind", usr, usr,
		"--symlink", d[0], bin,
		"--ro-bind", varOpt, varOpt, "--symlink", varOpt, opt,
	}, got)
}

// fakeBwrap is a script standing in for bwrap at path, running body.
func fakeBwrap(t *testing.T, path, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700))
	return path
}

func TestWithoutBwrapThereIsNoSandbox(t *testing.T) {
	s, v := probe(t.Context(), os.Args[0], nil, time.Minute)

	assert.Nil(t, s)
	assert.Equal(t, Status{Reason: "bwrap not found"}, v)
}

// A bwrap that fails the probe is no sandbox, and the reason is the first
// line it wrote, which names the cause.
func TestAProbeThatFailsSaysWhy(t *testing.T) {
	bwrap := fakeBwrap(t, filepath.Join(t.TempDir(), "bwrap"),
		`echo "bwrap: setting up uid map: Permission denied" >&2; echo more >&2; exit 1`)

	s, v := probe(t.Context(), os.Args[0], []string{bwrap}, time.Minute)

	assert.Nil(t, s)
	assert.Equal(t, Status{Reason: bwrap + ": bwrap: setting up uid map: Permission denied"}, v)
}

// A probe past its bound fails, saying so.
func TestAProbePastItsBoundFails(t *testing.T) {
	// Latency injected into the code under test: the fake outlasts the bound.
	bwrap := fakeBwrap(t, filepath.Join(t.TempDir(), "bwrap"), "exec sleep 60")

	s, v := probe(t.Context(), os.Args[0], []string{bwrap}, 10*time.Millisecond)

	assert.Nil(t, s)
	assert.Equal(t, Status{Reason: bwrap + ": no answer in 10ms"}, v)
}

// When the system's bwrap fails, Kstack's own is probed in its place, and
// both reasons are named when both fail; one that passes is never replaced.
func TestKstacksOwnBwrapStandsInForTheSystems(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "own-ran")
	failing := fakeBwrap(t, filepath.Join(dir, "system-failing"), `echo "system failed" >&2; exit 1`)
	passing := fakeBwrap(t, filepath.Join(dir, "system-passing"), "exit 0")
	own := fakeBwrap(t, filepath.Join(dir, "own"), "touch "+marker)
	ownFailing := fakeBwrap(t, filepath.Join(dir, "own-failing"), `echo "own failed" >&2; exit 1`)

	s, v := probe(t.Context(), os.Args[0], []string{failing, own}, time.Minute)
	require.NotNil(t, s)
	assert.Equal(t, own, s.bwrap)
	assert.Equal(t, Status{Available: true, Reason: "bwrap at " + own}, v)

	require.NoError(t, os.Remove(marker))
	s, _ = probe(t.Context(), os.Args[0], []string{passing, own}, time.Minute)
	require.NotNil(t, s)
	assert.Equal(t, passing, s.bwrap)
	assert.NoFileExists(t, marker)

	s, v = probe(t.Context(), os.Args[0], []string{failing, ownFailing}, time.Minute)
	assert.Nil(t, s)
	assert.Equal(t, Status{Reason: failing + ": system failed; " + ownFailing + ": own failed"}, v)
}

// A probe with nowhere to make its workspace fails, saying why.
func TestAProbeWithNoTempDirFails(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	bwrap := fakeBwrap(t, filepath.Join(t.TempDir(), "bwrap"), "exit 0")

	s, v := probe(t.Context(), os.Args[0], []string{bwrap}, time.Minute)

	assert.Nil(t, s)
	assert.Contains(t, v.Reason, "no such file or directory")
}

// A bwrap that fails without a word is named by how it exited.
func TestAProbeThatFailsSilentlySaysHowItExited(t *testing.T) {
	bwrap := fakeBwrap(t, filepath.Join(t.TempDir(), "bwrap"), "exit 3")

	_, v := probe(t.Context(), os.Args[0], []string{bwrap}, time.Minute)

	assert.Equal(t, Status{Reason: bwrap + ": exit status 3"}, v)
}

// run starts r through s and answers its exit code and its stdout. Stderr
// is left out of the answer: under coverage the forwarder, this test binary,
// warns there as it exits, since its GOCOVERDIR is outside the sandbox.
func run(t *testing.T, s *Sandbox, r testRun) (int, string) {
	t.Helper()
	cmd := command(t, s, t.Context(), r.on(s))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if cmd.ProcessState == nil {
		require.NoError(t, err)
	}
	t.Log("stderr: ", stderr.String())
	return ExitCode(cmd.ProcessState), string(out)
}

// testRun is a run as a test lays it out: the Run, the home its policy's
// System and Never paths are read under, the workspace, and the Files and
// Always rules the test adds.
type testRun struct {
	Run
	home, ws string
	files    FilePolicy
	always   AlwaysPolicy
}

// on is r as it runs on s, its policy built as Bash builds one: System less
// what lies in r's Kstack paths, then r's own Files rules, and the Never paths
// denied beside r's Always part.
func (r testRun) on(s *Sandbox) Run {
	files := s.System(r.home, r.Shell, r.Env).Outside(r.always.Kstack...)
	files.Read = append(files.Read, r.files.Read...)
	files.Write = append(files.Write, r.files.Write...)
	files.Deny = append(files.Deny, r.files.Deny...)
	always := r.always
	always.Deny = append(s.Never(r.home), always.Deny...)
	r.Policy = Policy{Files: files, Always: always, Network: r.Policy.Network}
	return r.Run
}

// shRun is a run of script under /bin/sh in a fresh workspace it writes, with
// a PATH of the system's and the environment env adds.
func shRun(t *testing.T, script string, env ...string) testRun {
	t.Helper()
	ws := resolved(t.TempDir())
	return testRun{
		Run:  Run{Shell: "/bin/sh", Args: []string{"-c", script}, Dir: ws, Env: append([]string{"PATH=/usr/bin:/bin"}, env...)},
		home: resolved(t.TempDir()), ws: ws, files: FilePolicy{Write: []string{ws}},
	}
}

// The command sees exactly the environment it was given: nothing the
// forwarder, the shell launcher or bwrap has of its own.
func TestTheEnvironmentIsExactlyGiven(t *testing.T) {
	s := confining(t)

	code, out := run(t, s, shRun(t, "env | grep -v -e ^PWD= -e ^SHLVL= -e ^_= | sort", "ONLY=this"))

	assert.Equal(t, 0, code, out)
	assert.Equal(t, "ONLY=this\nPATH=/usr/bin:/bin\n", out)
}

// bwrap confines, and a run's forwarder listens on one fixed port, since the
// namespace's loopback is the run's own.
func TestBwrapConfinesOnAFixedPort(t *testing.T) {
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}

	port, err := s.Port()

	require.NoError(t, err)
	assert.Equal(t, forwarderPort, port)
	assert.True(t, s.Confines())
}

// index is where want starts as a run in args, or -1.
func index(args []string, want ...string) int {
	for i := range args {
		if i+len(want) <= len(args) && slices.Equal(args[i:i+len(want)], want) {
			return i
		}
	}
	return -1
}

// The mounts come in the order that lets each lie over the last: the Files
// rules, shallowest first and on one path the narrower last, then every Always
// path over them, then the run's own, then the PATH links, then the read-only
// remounts, which come after the binds made inside a denial. A rule on / goes
// under the fixed mounts.
func TestTheCommandMountsInOrder(t *testing.T) {
	home, _, outside := machine(t)
	cargo := mkdirs(t, home, ".cargo/bin")[0]
	tree := filepath.Dir(cargo)
	creds := filepath.Join(tree, "credentials.toml")
	require.NoError(t, os.WriteFile(creds, nil, 0o600))
	data := mkdirs(t, home, ".cargo/kstack-data")[0]
	ws := mkdirs(t, data, "chats/1/workspace")[0]
	tool := mkdirs(t, outside, "tool/bin")[0]
	links := mkdirs(t, outside, "links")[0]
	require.NoError(t, os.Symlink(tool, filepath.Join(links, "tool")))
	tie := mkdirs(t, outside, "tie")[0]
	runtime := mkdirs(t, outside, "runtime")[0]
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}
	r := Run{
		Shell: "/bin/sh", Args: []string{"-c", "true"}, Dir: ws,
		Env: []string{"PATH=" + cargo + ":" + filepath.Join(links, "tool")},
		Policy: Policy{
			Files:  FilePolicy{Read: []string{tree, tool, tie}, Deny: []string{cargo, tie}},
			Always: AlwaysPolicy{Deny: []string{creds}, Kstack: []string{data, runtime}, Write: []string{ws}},
		},
	}

	args := command(t, s, context.Background(), r).Args

	at := map[string]int{
		"fixed":   index(args, "--tmpfs", "/tmp"),
		"tree":    index(args, "--ro-bind", tree, tree),
		"bin":     index(args, "--tmpfs", cargo),
		"tieRead": index(args, "--ro-bind", tie, tie),
		"tieDeny": index(args, "--tmpfs", tie),
		"cred":    index(args, "--ro-bind", "/dev/null", creds),
		"data":    index(args, "--tmpfs", data),
		"own":     index(args, "--bind", ws, ws),
		"symlink": index(args, "--symlink", tool, filepath.Join(links, "tool")),
		"root":    index(args, "--remount-ro", "/"),
		"sealed":  index(args, "--remount-ro", data),
	}
	for name, i := range at {
		require.NotEqual(t, -1, i, "%s missing from %v", name, args)
	}
	assert.Less(t, at["fixed"], at["tree"])
	assert.Less(t, at["tree"], at["bin"])
	assert.Less(t, at["tieRead"], at["tieDeny"])
	for _, files := range []string{"bin", "tieDeny"} {
		assert.Less(t, at[files], at["cred"])
	}
	assert.Less(t, at["cred"], at["data"])
	assert.Less(t, at["data"], at["own"])
	assert.Less(t, at["own"], at["symlink"])
	assert.Less(t, at["symlink"], at["root"])
	assert.Less(t, at["root"], at["sealed"])
	assert.Equal(t, -1, index(args, "--tmpfs", runtime), "a denial no Read or Write reaches needs no mount")

	r.Policy = Policy{Files: FilePolicy{Read: []string{"/"}}}
	args = command(t, s, context.Background(), r).Args
	assert.Less(t, index(args, "--ro-bind", "/", "/"), index(args, "--proc", "/proc"), "a rule on / goes under the fixed mounts")
}

// A root with a link above it is bound at its resolved path and recreated as
// a link at its own, as Homebrew's is under Fedora Atomic's linked /home.
func TestARootBelowALinkIsBoundResolved(t *testing.T) {
	base := resolved(t.TempDir())
	brew := mkdirs(t, base, "var/home/linuxbrew/.linuxbrew")[0]
	require.NoError(t, os.Symlink(filepath.Join(base, "var", "home"), filepath.Join(base, "home")))
	root := filepath.Join(base, "home", "linuxbrew", ".linuxbrew")

	got := mounts(t, &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}, Run{Shell: "/bin/sh", Dir: brew, Policy: Policy{Files: FilePolicy{Read: []string{root}}}})

	assert.Equal(t, []string{"--ro-bind", brew, brew, "--symlink", brew, root}, got)
}

// A Read granted through a link inside a denied folder reads, whether the
// denial is mounted before its target or after: the denial's tmpfs hides the
// tree's link, so the link is recreated in it. The rest of the folder stays
// denied.
func TestALinkInsideADenialLeadsToItsRead(t *testing.T) {
	s := confining(t)
	for name, c := range map[string]struct{ target, denied string }{
		"a denial above the target": {"target/deep", "denied"},
		"a denial below the target": {"target", "denied/sub/deeper"},
	} {
		t.Run(name, func(t *testing.T) {
			base := resolved(t.TempDir())
			target := filepath.Join(base, c.target)
			write(t, filepath.Join(target, "f"), "granted")
			denied := filepath.Join(base, c.denied)
			write(t, filepath.Join(denied, "secret"), "secret")
			link := filepath.Join(denied, "link")
			require.NoError(t, os.Symlink(target, link))
			r := shRun(t, "cat "+filepath.Join(link, "f")+"; cat "+filepath.Join(denied, "secret"))
			r.files.Read = []string{base, link}
			r.files.Deny = []string{denied}

			_, out := run(t, s, r)

			assert.Contains(t, out, "granted")
			assert.NotContains(t, out, "secret")
		})
	}
}

// A Read under /tmp reads beside a Read on /, whose bind the run's fresh /tmp
// hides.
func TestAReadUnderTmpReadsBesideARootRead(t *testing.T) {
	s := confining(t)
	dir, err := os.MkdirTemp("/tmp", "kstack-test-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := write(t, filepath.Join(dir, "f"), "granted")
	r := shRun(t, "cat "+f)
	r.files.Read = []string{"/", dir}

	_, out := run(t, s, r)

	assert.Equal(t, "granted", out)
}

// write makes a file at path holding text, and its directory.
func write(t *testing.T, path, text string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(text), 0o700))
	return path
}

// program makes an executable script at path that prints name.
func program(t *testing.T, path, name string) string {
	t.Helper()
	return write(t, path, "#!/bin/sh\necho "+name+"\n")
}

// withPath is r with PATH set to the system's then entries.
func withPath(r testRun, entries ...string) testRun {
	r.Env = append([]string{"PATH=" + strings.Join(append([]string{"/usr/bin", "/bin"}, entries...), ":")}, r.Env[1:]...)
	return r
}

func TestTheHomeIsUnreadable(t *testing.T) {
	s := confining(t)
	r := shRun(t, "")
	write(t, filepath.Join(r.home, "secret"), "secret")
	r.Args[1] = "cat " + filepath.Join(r.home, "secret") + "; ls -A " + r.home

	_, out := run(t, s, r)

	assert.NotContains(t, out, "secret")
}

func TestACredentialPathInsideAReadableTreeIsUnreadable(t *testing.T) {
	s := confining(t)
	r := shRun(t, "")
	program(t, filepath.Join(r.home, ".cargo", "bin", "tool"), "tool-ran")
	creds := write(t, filepath.Join(r.home, ".cargo", "credentials.toml"), "secret")
	r = withPath(r, filepath.Join(r.home, ".cargo", "bin"))
	r.Args[1] = "tool; cat " + creds

	_, out := run(t, s, r)

	assert.Contains(t, out, "tool-ran")
	assert.NotContains(t, out, "secret")
}

func TestLocalShareIsUnreadable(t *testing.T) {
	s := confining(t)
	r := shRun(t, "")
	bin := filepath.Join(r.home, ".local", "bin")
	program(t, filepath.Join(bin, "tool"), "tool-ran")
	x := program(t, filepath.Join(r.home, ".local", "share", "pipx", "venvs", "x", "bin", "x"), "pipx-ran")
	require.NoError(t, os.Symlink(x, filepath.Join(bin, "x")))
	other := write(t, filepath.Join(r.home, ".local", "share", "other", "x"), "secret")
	state := write(t, filepath.Join(r.home, ".local", "state", "x"), "secret")
	r = withPath(r, bin)
	r.Args[1] = "tool; x; cat " + other + " " + state

	_, out := run(t, s, r)

	assert.Contains(t, out, "tool-ran")
	assert.Contains(t, out, "pipx-ran")
	assert.NotContains(t, out, "secret")
}

// A ~/.local/share that links elsewhere in the home opens the tool's tree
// there, and nothing beside it.
func TestALinkedLocalShareOpensOnlyTheToolsTree(t *testing.T) {
	s := confining(t)
	r := shRun(t, "")
	docs := filepath.Join(r.home, "Documents")
	program(t, filepath.Join(docs, "pipx", "bin", "x"), "pipx-ran")
	private := write(t, filepath.Join(docs, "private", "notes"), "secret")
	require.NoError(t, os.MkdirAll(filepath.Join(r.home, ".local"), 0o700))
	require.NoError(t, os.Symlink(docs, filepath.Join(r.home, ".local", "share")))
	r = withPath(r, filepath.Join(r.home, ".local", "share", "pipx", "bin"))
	r.Args[1] = "x; cat " + private

	_, out := run(t, s, r)

	assert.Contains(t, out, "pipx-ran")
	assert.NotContains(t, out, "secret")
}

// A program reached through a chain of links runs.
func TestAProgramLinkChainRuns(t *testing.T) {
	s := confining(t)
	r := shRun(t, "tool")
	tool := program(t, filepath.Join(r.home, "install", "bin", "tool"), "tool-ran")
	bin := filepath.Join(r.home, ".local", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(r.home, "links"), 0o700))
	require.NoError(t, os.Symlink(tool, filepath.Join(r.home, "links", "tool")))
	require.NoError(t, os.Symlink(filepath.Join(r.home, "links", "tool"), filepath.Join(bin, "tool")))

	code, out := run(t, s, withPath(r, bin))

	assert.Equal(t, 0, code)
	assert.Equal(t, "tool-ran\n", out)
}

// linkedHome is a home reached through a link: the link, and the directory
// it names.
func linkedHome(t *testing.T) (link, home string) {
	t.Helper()
	base := resolved(t.TempDir())
	home = mkdirs(t, base, "data/ana")[0]
	link = filepath.Join(base, "ana")
	require.NoError(t, os.Symlink(home, link))
	return link, home
}

func TestAProgramLinkThroughALinkedHomeRuns(t *testing.T) {
	s := confining(t)
	link, _ := linkedHome(t)
	r := shRun(t, "x")
	r.home = link
	x := program(t, filepath.Join(link, ".local", "share", "pipx", "venvs", "x", "bin", "x"), "pipx-ran")
	bin := filepath.Join(link, ".local", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o700))
	require.NoError(t, os.Symlink(x, filepath.Join(bin, "x")))
	r = withPath(r, bin)

	code, out := run(t, s, r)

	assert.Equal(t, 0, code)
	assert.Equal(t, "pipx-ran\n", out)
}

func TestKstacksDirectoriesAreUnreadableThroughALinkedHome(t *testing.T) {
	s := confining(t)
	link, home := linkedHome(t)
	r := shRun(t, "")
	r.home = link
	program(t, filepath.Join(home, ".config", "tool", "bin", "tool"), "tool-ran")
	write(t, filepath.Join(home, ".config", "kstack", "app.db"), "secret")
	r.always.Kstack = []string{filepath.Join(link, ".config", "kstack")}
	r = withPath(r, filepath.Join(link, ".config", "tool", "bin"))
	r.Args[1] = "tool; cat " + filepath.Join(home, ".config", "kstack", "app.db") + " " + filepath.Join(link, ".config", "kstack", "app.db")

	_, out := run(t, s, r)

	assert.Contains(t, out, "tool-ran")
	assert.NotContains(t, out, "secret")
}

func TestAPathEntryThroughALinkRuns(t *testing.T) {
	s := confining(t)
	r := shRun(t, "nixtool; tool")
	outside := resolved(t.TempDir())
	program(t, filepath.Join(outside, "profile", "bin", "nixtool"), "nix-ran")
	require.NoError(t, os.Symlink(filepath.Join(outside, "profile"), filepath.Join(r.home, ".nix-profile")))
	program(t, filepath.Join(outside, "tool", "bin", "tool"), "tool-ran")
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "links"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(outside, "tool", "bin"), filepath.Join(outside, "links", "tool")))
	r = withPath(r, filepath.Join(r.home, ".nix-profile", "bin"), filepath.Join(outside, "links", "tool"))

	code, out := run(t, s, r)

	assert.Equal(t, 0, code)
	assert.Equal(t, "nix-ran\ntool-ran\n", out)
}

// addRoot makes dir one of the system roots for the rest of the test.
func addRoot(t *testing.T, dir string) {
	t.Helper()
	old := platformLists
	platformLists.System = append(slices.Clone(old.System), dir)
	t.Cleanup(func() { platformLists = old })
}

// A credential that is a link is unreadable where its target lies, though a
// PATH tree takes that in.
func TestACredentialThroughALinkIsUnreadable(t *testing.T) {
	s := confining(t)
	r := shRun(t, "")
	program(t, filepath.Join(r.home, "tools", "bin", "tool"), "tool-ran")
	creds := write(t, filepath.Join(r.home, "tools", "credentials", "config"), "secret")
	require.NoError(t, os.Symlink(filepath.Dir(creds), filepath.Join(r.home, ".kube")))
	r = withPath(r, filepath.Join(r.home, "tools", "bin"))
	r.Args[1] = "tool; cat " + creds

	_, out := run(t, s, r)

	assert.Contains(t, out, "tool-ran")
	assert.NotContains(t, out, "secret")
}

// A root that is a link runs what is under its target, wherever that lies.
func TestARootThatIsALinkRuns(t *testing.T) {
	s := confining(t)
	base := resolved(t.TempDir())
	program(t, filepath.Join(base, "var", "opt", "bin", "tool"), "tool-ran")
	opt := filepath.Join(base, "opt")
	require.NoError(t, os.Symlink(filepath.Join(base, "var", "opt"), opt))
	addRoot(t, opt)

	code, out := run(t, s, withPath(shRun(t, "tool"), filepath.Join(opt, "bin")))

	assert.Equal(t, 0, code)
	assert.Equal(t, "tool-ran\n", out)
}

// A program in a root's PATH entry that links under the home runs, as
// /usr/local/bin/tool linked to ~/tools/tool does.
func TestAProgramLinkInARootRuns(t *testing.T) {
	s := confining(t)
	root := resolved(t.TempDir())
	addRoot(t, root)
	r := shRun(t, "tool")
	tool := program(t, filepath.Join(r.home, "tools", "tool"), "tool-ran")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o700))
	require.NoError(t, os.Symlink(tool, filepath.Join(root, "bin", "tool")))

	code, out := run(t, s, withPath(r, filepath.Join(root, "bin")))

	assert.Equal(t, 0, code)
	assert.Equal(t, "tool-ran\n", out)
}

// Kstack's directories inside a root are unreadable, but for the run's own
// workspace inside them.
func TestKstacksDirectoriesInsideARootAreUnreadable(t *testing.T) {
	s := confining(t)
	root := resolved(t.TempDir())
	addRoot(t, root)
	data := filepath.Join(root, "kstack")
	write(t, filepath.Join(data, "app.db"), "secret")
	ws := mkdirs(t, data, "chats/1/workspace")[0]
	r := shRun(t, "cat "+filepath.Join(data, "app.db")+"; touch w && echo wrote")
	r.Dir, r.files.Write = ws, nil
	r.always = AlwaysPolicy{Kstack: []string{data}, Write: []string{ws}}

	_, out := run(t, s, r)

	assert.Equal(t, "wrote\n", out)
	assert.FileExists(t, filepath.Join(ws, "w"))
}

// kstackRun is a run laid out as Kstack lays one out, with its data and cache
// directories where a PATH tree takes them in, and a sibling run beside it.
type kstackRun struct {
	testRun
	data, cache, runtime, sibling  string
	snapshot, runDir, tmp, kubectl string
}

func newKstackRun(t *testing.T) kstackRun {
	t.Helper()
	r := shRun(t, "")
	home := r.home
	program(t, filepath.Join(home, "apps", "bin", "tool"), "tool-ran")
	program(t, filepath.Join(home, ".cache", "bin", "cached"), "cached-ran")
	data := filepath.Join(home, "apps", "kstack")
	cache := filepath.Join(home, ".cache", "kstack")
	runtime := resolved(t.TempDir())
	write(t, filepath.Join(data, "app.db"), "secret")
	write(t, filepath.Join(cache, "kubestore", "c.db"), "secret")
	write(t, filepath.Join(runtime, "host.sock"), "secret")
	sibling := write(t, filepath.Join(runtime, "runs", "2", "kubeconfig"), "secret")
	snapshot := write(t, filepath.Join(runtime, "shell", "snapshot.sh"), "snapshot")
	runDir := filepath.Dir(write(t, filepath.Join(runtime, "runs", "1", "kubeconfig"), "own-kubeconfig"))
	ws := mkdirs(t, data, "chats/1/workspace")[0]
	tmp := mkdirs(t, cache, "tmp/1")[0]
	kubectl := mkdirs(t, cache, "kubectl/c")[0]
	r.Dir, r.ws, r.files.Write = ws, ws, nil
	r.always = AlwaysPolicy{Kstack: []string{data, cache, runtime}, Read: []string{snapshot, runDir}, Write: []string{ws, tmp, kubectl}}
	r = withPath(r, filepath.Join(home, "apps", "bin"), filepath.Join(home, ".cache", "bin"))
	return kstackRun{
		testRun: r, data: data, cache: cache, runtime: runtime, sibling: sibling,
		snapshot: snapshot, runDir: runDir, tmp: tmp, kubectl: kubectl,
	}
}

// Kstack's directories are unreadable, but for the run's own: its workspace,
// kubectl cache, snapshot, run directory and TMPDIR.
func TestKstacksDirectoriesAreUnreadable(t *testing.T) {
	s := confining(t)
	k := newKstackRun(t)
	k.Args[1] = fmt.Sprintf("tool; cached; cat %s/app.db %s/kubestore/c.db %s/host.sock; cat %s %s/kubeconfig; touch %s/w %s/w %s/w && echo wrote",
		k.data, k.cache, k.runtime, k.snapshot, k.runDir, k.ws, k.tmp, k.kubectl)

	_, out := run(t, s, k.testRun)

	assert.Contains(t, out, "tool-ran\ncached-ran\n")
	assert.NotContains(t, out, "secret")
	assert.Contains(t, out, "snapshotown-kubeconfig")
	assert.Contains(t, out, "wrote")
}

func TestASiblingRunsKubeconfigIsUnreadable(t *testing.T) {
	s := confining(t)
	k := newKstackRun(t)
	k.Args[1] = "cat " + k.sibling + "; ls " + filepath.Dir(filepath.Dir(k.sibling))

	_, out := run(t, s, k.testRun)

	assert.NotContains(t, out, "secret")
	assert.NotContains(t, out, "2\n")
}

// A write lands in the workspace, the cache or the run's own /tmp, and
// anywhere else fails; nothing outside the sandbox changes but the first two.
// The stand-in home is under the host's /tmp, which inside is the run's own,
// so a write there lands in it: it is checked outside alone.
func TestOnlyTheWorkspaceAndTheCacheAreWritten(t *testing.T) {
	s := confining(t)
	k := newKstackRun(t)
	marker := "kstack-write-" + strconv.Itoa(os.Getpid())
	var script strings.Builder
	for _, p := range []string{"/" + marker, "/etc/" + marker, "/usr/" + marker, k.data + "/" + marker, k.cache + "/" + marker} {
		fmt.Fprintf(&script, "touch %s 2>/dev/null && echo wrote %s; ", p, p)
	}
	fmt.Fprintf(&script, "touch %s/%s 2>/dev/null; ", k.home, marker)
	fmt.Fprintf(&script, "touch /tmp/%s && echo tmp; touch %s/%s %s/%s && echo own", marker, k.ws, marker, k.kubectl, marker)
	k.Args[1] = script.String()

	_, out := run(t, s, k.testRun)

	assert.Equal(t, "tmp\nown\n", out)
	assert.NoFileExists(t, "/tmp/"+marker)
	assert.NoFileExists(t, filepath.Join(k.home, marker))
	assert.NoFileExists(t, filepath.Join(k.data, marker))
	assert.FileExists(t, filepath.Join(k.ws, marker))
}

// A run cannot remove, rename or replace the root of a path it writes: each
// is a mount point, whose parent is the namespace's own. What lies inside
// stays its own.
func TestARunCannotReplaceItsWorkspace(t *testing.T) {
	s := confining(t)
	for _, script := range []string{
		`cd /; rm -rf "$W" && ln -s "$D" "$W"`,
		`cd /; rmdir "$W"`,
		`cd /; mv "$W" "$O/moved"`,
		`mkdir "$O/x" && mv -T "$O/x" "$W"`,
		`ln -s "$D" "$O/l" && mv -T "$O/l" "$W"`,
	} {
		k := newKstackRun(t)
		for _, pair := range [][2]string{{k.ws, k.kubectl}, {k.kubectl, k.ws}} {
			w, other := pair[0], pair[1]
			r := k.testRun
			r.Args = []string{"-c", script}
			r.Env = append(slices.Clone(r.Env), "W="+w, "D="+k.data, "O="+other)
			code, out := run(t, s, r)
			assert.NotEqual(t, 0, code, "%s on %s: %s", script, w, out)
			info, err := os.Lstat(w)
			require.NoError(t, err, script)
			assert.True(t, info.IsDir(), "%s on %s left %v", script, w, info.Mode())
		}
	}

	k := newKstackRun(t)
	for _, w := range []string{k.ws, k.kubectl} {
		r := k.testRun
		r.Args = []string{"-c", `mkdir "$W/a" && echo x > "$W/a/f" && mv "$W/a" "$W/b" && rm -rf "$W/b" && ln -s /usr "$W/l" && rm "$W/l"`}
		r.Env = append(slices.Clone(r.Env), "W="+w)
		code, out := run(t, s, r)
		assert.Equal(t, 0, code, "%s: %s", w, out)
	}
}

func init() {
	helpers["dial"] = func() int {
		c, err := net.DialTimeout("tcp", os.Getenv("KSTACK_SANDBOX_TEST_ADDR"), time.Second)
		if err != nil {
			fmt.Println(err)
			return 1
		}
		_ = c.Close()
		fmt.Println("connected")
		return 0
	}
}

// self is a run of this test binary as the helper named, with env.
func self(t *testing.T, helper string, env ...string) testRun {
	t.Helper()
	r := shRun(t, "")
	r.Shell, r.Args = os.Args[0], nil
	r.Env = append(append(r.Env, "KSTACK_SANDBOX_TEST_HELPER="+helper), env...)
	return r
}

func TestNoListenerOutsideIsReachable(t *testing.T) {
	s := confining(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	code, out := run(t, s, self(t, "dial", "KSTACK_SANDBOX_TEST_ADDR="+ln.Addr().String()))

	assert.Equal(t, 1, code)
	assert.Contains(t, out, "connection refused")
}

// A lookup fails, and fails at once rather than waiting on a resolver.
func TestAPublicNameDoesNotResolve(t *testing.T) {
	s := confining(t)
	start := time.Now()

	code, out := run(t, s, shRun(t, "getent hosts example.com"))

	assert.NotEqual(t, 0, code, out)
	assert.Less(t, time.Since(start), time.Second)
}

// The shell is refused a Unix or vsock socket while the forwarder, outside
// the filter, still relays a connection to the run's.
func TestAUnixOrVsockSocketCannotBeOpened(t *testing.T) {
	s := confining(t)
	socket := echoSocket(t)
	r := self(t, "sockets")
	r.files.Read = []string{filepath.Dir(socket)}
	r.Policy.Network.Relays = []Relay{{Port: forwarderPort, Socket: socket}}

	_, out := run(t, s, r)
	assert.Equal(t, "unix=operation not permitted vsock=operation not permitted inet=ok dgram-pair=operation not permitted stream-pair=ok", out)

	r = self(t, "", "KSTACK_SANDBOX_TEST_DIAL="+strconv.Itoa(forwarderPort))
	r.files.Read = []string{filepath.Dir(socket)}
	r.Policy.Network.Relays = []Relay{{Port: forwarderPort, Socket: socket}}
	code, out := run(t, s, r)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ping|eof", out)
}

// A datagram pair cannot be made, so nothing under the filter sends to a
// host's socket in a directory it can read.
func TestNoDatagramReachesAHostSocket(t *testing.T) {
	s := confining(t)
	dir := resolved(t.TempDir())
	path := filepath.Join(dir, "host.sock")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	require.NoError(t, err)
	defer ln.Close()
	r := self(t, "sendto", "KSTACK_SANDBOX_TEST_SOCKET="+path)
	r.files.Read = []string{dir}

	code, out := run(t, s, r)

	assert.Equal(t, 1, code)
	assert.Equal(t, "operation not permitted", out)
	// The run has ended, so anything it sent is queued: a read that waits
	// for nothing finds it.
	require.NoError(t, ln.SetReadDeadline(time.Now()))
	_, _, err = ln.ReadFrom(make([]byte, 16))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
}

func TestAUserNamespaceCannotBeMade(t *testing.T) {
	s := confining(t)

	code, _ := run(t, s, shRun(t, "unshare -U true"))

	assert.NotEqual(t, 0, code)
}

// The forwarder is the namespace's first process, and nothing under the
// filter can read its environment or memory.
func TestTheFirstProcessCannotBeRead(t *testing.T) {
	s := confining(t)
	r := shRun(t, "tr '\\0' ' ' < /proc/1/cmdline; echo; cat /proc/1/environ >/dev/null 2>&1 || echo environ-refused; head -c1 /proc/1/mem >/dev/null 2>&1 || echo mem-refused")

	_, out := run(t, s, r)

	assert.Contains(t, out, InitCommand)
	assert.Contains(t, out, "environ-refused\nmem-refused\n")
}

// A command that leaves a process behind ends at once whether its group is
// sent SIGTERM or SIGKILL, and nothing of the run is left.
func TestNothingOutlivesTheGroupsKill(t *testing.T) {
	s := confining(t)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		marker := fmt.Sprintf("3001.%d%d", os.Getpid(), sig)
		cmd := command(t, s, t.Context(), shRun(t, "sleep "+marker+" & echo up; exec sleep 3002").on(s))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, err := cmd.StdoutPipe()
		require.NoError(t, err)
		require.NoError(t, cmd.Start())
		line, err := bufio.NewReader(out).ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "up\n", line)

		require.NoError(t, syscall.Kill(-cmd.Process.Pid, sig))
		_ = cmd.Wait()

		require.Eventually(t, func() bool { return !running(marker) }, 5*time.Second, 10*time.Millisecond, "%s left %s running", sig, marker)
	}
}

// running reports whether a process whose command line holds marker is alive.
func running(marker string) bool {
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, p := range procs {
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), marker) {
			return true
		}
	}
	return false
}

// A process orphaned under the forwarder is reaped: its /proc entry goes,
// where an unreaped one would stay a zombie and hold the loop open.
func TestAnOrphanIsReaped(t *testing.T) {
	s := confining(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r := shRun(t, `pid=$(sh -c 'true & echo $!'); while [ -e /proc/$pid ]; do :; done; echo reaped`)

	cmd := command(t, s, ctx, r.on(s))
	out, err := cmd.Output()

	require.NoError(t, err)
	assert.Equal(t, "reaped\n", string(out))
}

// bwrap's arguments over the fixture are the golden's, with a cluster and
// without.
func TestTheCompiledArgumentsMatchTheGolden(t *testing.T) {
	f := newFixture(t)
	s := &Sandbox{self: f.self, bwrap: "/usr/bin/bwrap"}
	for name, cluster := range map[string]bool{"cluster": true, "no-cluster": false} {
		t.Run(name, func(t *testing.T) {
			cmd := command(t, s, context.Background(), f.run(s, cluster))
			f.golden(t, "args_linux_"+name+".golden", cmd.Args[1:])
		})
	}
}

// System is the lists' System folders, the PATH trees, the shell's folder
// among them, and this executable at its resolved path.
func TestSystemIsTheListsAndTheTrees(t *testing.T) {
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "usr/bin", "home/apps/bin", "home/shells", "app")
	self := write(t, filepath.Join(d[3], "kstack-sidecar"), "")
	require.NoError(t, os.Symlink(self, filepath.Join(base, "self-link")))
	oldShared, oldPlatform := sharedLists, platformLists
	sharedLists.System = []string{filepath.Join(base, "shared")}
	platformLists.System = []string{filepath.Join(base, "usr"), filepath.Join(base, "missing")}
	t.Cleanup(func() { sharedLists, platformLists = oldShared, oldPlatform })
	s := &Sandbox{self: filepath.Join(base, "self-link")}

	got := s.System(filepath.Join(base, "home"), filepath.Join(d[2], "bash"), []string{"PATH=" + d[1] + ":" + d[0]})

	assert.Equal(t, FilePolicy{Read: []string{
		filepath.Join(base, "shared"), filepath.Join(base, "usr"), filepath.Join(base, "missing"),
		filepath.Join(base, "home", "apps"), filepath.Join(base, "home", "shells"), self,
	}}, got)
}

// A PATH entry on /tmp or /dev, or on or under /proc, makes no rule, since a
// rule there would replace a fixed mount; one under /tmp does.
func TestSystemLeavesOutTheFixedMounts(t *testing.T) {
	under := mkdirs(t, t.TempDir(), "x/bin")[0]
	s := &Sandbox{self: "/usr/bin/true"}

	got := s.System("/nonexistent/home", "/bin/sh", []string{"PATH=/tmp:" + under + ":/proc/1:/dev"})

	for _, p := range []string{"/tmp", "/dev", "/proc/1", "/proc"} {
		assert.NotContains(t, got.Read, p)
	}
	assert.Contains(t, got.Read, resolved(under))
}

// A policy that fails Check answers its error and no command.
func TestAPolicyThatFailsCheckStartsNothing(t *testing.T) {
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}
	r := Run{Shell: "/bin/sh", Dir: "/w", Policy: Policy{Files: FilePolicy{Read: []string{"relative"}}}}

	cmd, err := s.Command(context.Background(), r)

	assert.Nil(t, cmd)
	assert.Error(t, err)
}

// A rule on /tmp or /dev, or on or under /proc, would replace a mount every
// run has, so Command refuses it, a Files rule and a run's own path alike.
func TestARuleOverAFixedMountIsRefused(t *testing.T) {
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}
	for _, p := range []Policy{
		{Files: FilePolicy{Write: []string{"/tmp"}}},
		{Files: FilePolicy{Read: []string{"/dev"}}},
		{Files: FilePolicy{Read: []string{"/proc"}}},
		{Files: FilePolicy{Deny: []string{"/proc/1"}}},
		{Always: AlwaysPolicy{Kstack: []string{"/"}, Write: []string{"/tmp"}}},
	} {
		cmd, err := s.Command(context.Background(), Run{Shell: "/bin/sh", Dir: "/w", Policy: p})

		assert.Nil(t, cmd, "%+v", p)
		assert.Error(t, err, "%+v", p)
	}
}

// A Read and a Write rule whose paths are missing are skipped: the run
// starts, and it cannot make either path.
func TestAMissingReadOrWritePathIsSkipped(t *testing.T) {
	s := confining(t)
	base := resolved(t.TempDir())
	read, written := filepath.Join(base, "read"), filepath.Join(base, "written")
	r := shRun(t, fmt.Sprintf("mkdir -p %[1]s %[2]s 2>/dev/null; touch %[1]s/x %[2]s/x 2>/dev/null; echo started", read, written))
	r.files.Read = []string{read}
	r.files.Write = append(r.files.Write, written)

	_, out := run(t, s, r)

	assert.Equal(t, "started\n", out)
	assert.NoDirExists(t, read)
	assert.NoDirExists(t, written)
}

// A Write rule on a folder under /tmp, and a run's own Write path under
// /dev/shm, are bound over the fixed mounts and written through them.
func TestARuleUnderTmpOrDevIsReachable(t *testing.T) {
	s := confining(t)
	tmp, err := os.MkdirTemp("/tmp", "kstack-rule-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	shm, err := os.MkdirTemp("/dev/shm", "kstack-rule-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(shm) })
	own := mkdirs(t, shm, "own")[0]
	r := shRun(t, fmt.Sprintf("echo a > %s/a && echo b > %s/b && echo wrote", tmp, own))
	r.files.Write = append(r.files.Write, tmp)
	r.always = AlwaysPolicy{Kstack: []string{shm}, Write: []string{own}}

	_, out := run(t, s, r)

	assert.Equal(t, "wrote\n", out)
	assert.FileExists(t, filepath.Join(tmp, "a"))
	assert.FileExists(t, filepath.Join(own, "b"))
}

// With no home, the probe still answers a sandbox: its policy denies the
// Never paths that are absolute alone.
func TestAProbeWithNoHomeFindsTheSandbox(t *testing.T) {
	confining(t)
	t.Setenv("HOME", "")

	s, v := Probe(t.Context())

	require.NotNil(t, s, v.Reason)
	assert.True(t, v.Available)
}

// Command refuses a run holding a variable no run may hold.
func TestARunWithAnUnpassableVariableIsRefused(t *testing.T) {
	s := &Sandbox{self: "/k", bwrap: "/usr/bin/bwrap"}
	for _, kv := range []string{"LD_PRELOAD=/x.so", "AWS_SESSION_TOKEN=t"} {
		cmd, err := s.Command(context.Background(), Run{Shell: "/bin/sh", Dir: "/", Env: []string{kv}})

		assert.Nil(t, cmd, kv)
		assert.ErrorContains(t, err, "may not pass", kv)
	}
}
