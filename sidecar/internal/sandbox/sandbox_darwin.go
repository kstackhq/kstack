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
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// probeTimeout bounds the probe: its profile and its sandboxed true.
const probeTimeout = 2 * time.Second

// Probe answers Seatbelt, available when sandbox-exec runs true under the
// profile a run with no cluster gets.
func Probe(ctx context.Context) (*Sandbox, Verdict) {
	return probe(ctx, "/usr/bin/sandbox-exec", probeTimeout)
}

// probe is Probe with the launcher at path, bounded by timeout. A probe that
// runs out of time keeps the sandbox: it runs once, at startup, so a slow
// start must not leave the whole session unconfined. A run that then fails
// under the profile fails closed.
func probe(ctx context.Context, path string, timeout time.Duration) (*Sandbox, Verdict) {
	if _, err := os.Stat(path); err != nil {
		return nil, Verdict{Reason: "sandbox-exec not found"}
	}
	self, err := os.Executable()
	if err != nil {
		return nil, Verdict{Reason: "cannot find its own executable"}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, Verdict{Reason: "cannot find the home directory"}
	}
	dir, err := os.MkdirTemp("", "kstack-probe-")
	if err != nil {
		return nil, Verdict{Reason: "cannot make a directory to probe in"}
	}
	defer os.RemoveAll(dir)

	s := &Sandbox{self: self, launcher: path}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := s.Command(ctx, Run{
		Shell: "/usr/bin/true", Dir: dir, Workspace: dir, Home: home,
		Env: []string{"PATH=" + os.Getenv("PATH")},
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return s, Verdict{Available: true, Reason: "Seatbelt, unconfirmed: the probe did not finish within " + timeout.String()}
	case err != nil:
		return nil, Verdict{Reason: probeFailure(stderr.String(), err)}
	}
	return s, Verdict{Available: true, Reason: "Seatbelt"}
}

// probeFailure is why the probe's run failed: the first line of its stderr,
// else what ended it.
func probeFailure(stderr string, err error) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// Command is the process that runs r sandboxed, not yet started, made by
// exec.CommandContext on ctx: sandbox-exec over r's profile, then the shell,
// or for a run with a socket the forwarder with the shell its child.
// sandbox-exec execs into it, so the session the caller makes is the run's
// process group, and the profile holds every descendant. The caller sets its
// output, session and Cancel, and starts it.
//
// The profile resolves paths, which can hang on a network mount, so ctx
// bounds it too: ctx ending first answers a command Start refuses.
func (s *Sandbox) Command(ctx context.Context, r Run) *exec.Cmd {
	type built struct {
		text   string
		params []string
	}
	done := make(chan built, 1)
	build := buildProfile
	go func() {
		text, params := build(s, r, brewVar)
		done <- built{text, params}
	}()
	var b built
	select {
	case b = <-done:
	case <-ctx.Done():
		return exec.CommandContext(ctx, s.launcher)
	}
	name, args := s.argv(r)
	cmd := exec.CommandContext(ctx, s.launcher, slices.Concat([]string{"-p", b.text}, b.params, []string{name}, args)...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	return cmd
}

// Confines reports whether a command run through s is confined: always, here.
func (s *Sandbox) Confines() bool { return true }

// Port is a free loopback port for a run's forwarder. Seatbelt has no private
// loopback, so it is the host's. The forwarder listens on it before the
// command starts and exits when it cannot, so a process that takes it in
// between fails the run rather than answering it.
func (s *Sandbox) Port() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// argv is the process that runs r: the shell, or for a run with a socket, this
// executable as the run's forwarder with the shell its child.
func (s *Sandbox) argv(r Run) (name string, args []string) {
	if r.Socket == "" {
		return r.Shell, r.Args
	}
	return s.self, append(append(ForwarderArgs(r), r.Shell), r.Args...)
}

// buildProfile is profile, which a test replaces with one that hangs.
var buildProfile = (*Sandbox).profile

// profileText is the Seatbelt profile's fixed rules, with a marker line where
// each list's rules go.
//
//go:embed profile_darwin.sb
var profileText string

// The marker lines profile replaces.
const (
	markerTrees     = ";; TREES\n"
	markerDenied    = ";; DENIED\n"
	markerOwn       = ";; OWN\n"
	markerAncestors = ";; ANCESTORS\n"
	markerNetwork   = ";; NETWORK\n"
)

// roots are the system's trees a run reads. /Applications holds app bundles,
// which are programs: Docker Desktop's kubectl is a link into one, and so is
// Xcode's developer directory.
var roots = []string{"/usr", "/bin", "/sbin", "/System", "/Library", "/Applications", "/private/etc", "/opt"}

// sharedHomeDirs are the directories under the home that hold every app's
// data, where a PATH entry names only itself.
var sharedHomeDirs = []string{"Library", ".config"}

// brewVar is Homebrew's var, which the roots take in and which holds its
// services' databases and logs.
var brewVar = []string{"/opt/homebrew/var", "/usr/local/var"}

// refusedServices are the Mach services no profile names: a service that
// resolves names, holds the Keychain, opens or drives an app, holds the
// pasteboard, searches the home, or fetches a URL for its caller. One ending
// in * refuses every name it begins.
var refusedServices = []string{
	"com.apple.dnssd.service",
	"com.apple.SecurityServer", "com.apple.securityd", "com.apple.securityd.xpc", "com.apple.secd",
	"com.apple.security.agent",
	"com.apple.coreservices.launchservicesd", "com.apple.lsd.*",
	"com.apple.coreservices.appleevents",
	"com.apple.pasteboard.*",
	"com.apple.metadata.*",
	"com.apple.trustd", "com.apple.trustd.agent", "com.apple.nsurlsessiond",
}

// refused reports whether refusedServices names service.
func refused(service string) bool {
	return slices.ContainsFunc(refusedServices, func(r string) bool {
		if prefix, ok := strings.CutSuffix(r, "*"); ok {
			return strings.HasPrefix(service, prefix)
		}
		return service == r
	})
}

// networkRules is a run's network with a cluster: its forwarder's port, which
// is the one value written into the text, and its socket, by the path as given
// and as resolved, since Seatbelt's match on a socket's path is undocumented.
// The port is TCP on 127.0.0.1 alone, the one endpoint the forwarder holds:
// UDP or IPv6 at that number could be another service's.
const networkRules = `(allow network-bind network-inbound (local tcp4 "localhost:%[1]d"))
(allow network-outbound
  (remote tcp4 "localhost:%[1]d")
  (remote unix-socket (path-literal (param "SOCKET")))
  (remote unix-socket (path-literal (param "SOCKET_RESOLVED"))))
`

// ownWriteRule opens a path the run writes, but not its root's own entry: a
// run that could unlink or create the root could put a link there, and the
// next run's profile, which resolves it, would open wherever the link leads.
const ownWriteRule = `(allow file-read* file-write* (subpath (param "%[1]s")))
(deny file-write-unlink file-write-create (literal (param "%[1]s")))`

// profile is r's Seatbelt profile and the -D arguments it reads, with extra
// denied beside the credential paths and Kstack's directories. Every path is
// resolved, since Seatbelt checks a file's real path, and is a parameter,
// since a path can hold what the text would read as code.
func (s *Sandbox) profile(r Run, extra []string) (text string, params []string) {
	trees := pathTrees(r.Home, roots, sharedHomeDirs, pathOf(r.Env))
	reads := slices.Concat(roots, trees, []string{resolved(s.self)})
	denied := slices.Concat(overlapping(credentialPaths(r.Home), reads), resolvedAll(extra), resolvedAll(r.Denied))
	writes := resolvedAll(append([]string{r.Workspace}, r.Writable...))
	own := resolvedAll(r.Readable)
	up := ancestors(slices.Concat(reads, writes, own))

	var network string
	if r.Socket != "" {
		params = append(params, "-D", "SOCKET="+r.Socket, "-D", "SOCKET_RESOLVED="+resolved(r.Socket))
		network = fmt.Sprintf(networkRules, r.Port)
	}
	text = strings.NewReplacer(
		markerTrees, rules(&params, "TREE", `(allow file-read* (subpath (param "%s")))`, reads),
		markerDenied, rules(&params, "DENY", `(deny file-read* file-write* (subpath (param "%s")))`, denied),
		markerOwn, rules(&params, "WRITE", ownWriteRule, writes)+
			rules(&params, "READ", `(allow file-read* (subpath (param "%s")))`, own),
		markerAncestors, rules(&params, "UP", `(allow file-read-metadata (literal (param "%s")))`, up),
		markerNetwork, network,
	).Replace(profileText)
	return text, params
}

// rules is one rule per path, format's %s naming its parameter, each name
// prefix and its index, and adds the parameters to params.
func rules(params *[]string, prefix, format string, paths []string) string {
	var b strings.Builder
	for i, p := range paths {
		name := prefix + "_" + strconv.Itoa(i)
		*params = append(*params, "-D", name+"="+p)
		fmt.Fprintf(&b, format+"\n", name)
	}
	return b.String()
}

// ancestors is every directory above one of paths, sorted.
func ancestors(paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		for d := filepath.Dir(p); !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
