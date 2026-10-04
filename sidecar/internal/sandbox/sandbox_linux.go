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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// probeBound is how long a probe's run may take. It starts this executable
// twice, which from an AppImage's mount on a cold cache takes a while.
const probeBound = 5 * time.Second

// forwarderPort is where a run's forwarder listens: the network namespace's
// loopback is the run's own, so any port is free.
const forwarderPort = 6443

// systemBwraps are where a distribution puts bwrap, in the order looked for.
// bwrap is never found off PATH.
var systemBwraps = []string{"/usr/bin/bwrap", "/bin/bwrap", "/usr/local/bin/bwrap", "/run/current-system/sw/bin/bwrap"}

// Probe answers this machine's sandbox: the first bwrap that runs a command
// through the whole chain, the system's before Kstack's own. It answers ctx's
// error when ctx ended first.
func Probe(ctx context.Context) (*Sandbox, Status, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, Status{Reason: "cannot find its own executable"}, nil
	}
	s, v := probe(ctx, self, bwrapPaths(filepath.Dir(self), systemBwraps), probeBound)
	if s != nil {
		s.probePasta(ctx, systemPastas, probeBound)
		v.NetworkAvailable, v.NetworkReason = s.NetworkStatus()
	}
	if err := ctx.Err(); err != nil {
		return nil, Status{}, err
	}
	return s, v, nil
}

// probe tries each of bwraps in order and answers the first that passes, or
// every one's reason.
func probe(ctx context.Context, self string, bwraps []string, bound time.Duration) (*Sandbox, Status) {
	if len(bwraps) == 0 {
		return nil, Status{Reason: "bwrap not found"}
	}
	var uts unix.Utsname
	perNamespace := unix.Uname(&uts) == nil && countsPerNamespace(unix.ByteSliceToString(uts.Release[:]))
	var reasons []string
	for _, bwrap := range bwraps {
		s := &Sandbox{self: self, bwrap: bwrap, perNamespace: perNamespace}
		uidMap, err := s.try(ctx, bound)
		if err != nil {
			reasons = append(reasons, bwrap+": "+err.Error())
			continue
		}
		own, _ := os.ReadFile("/proc/self/uid_map")
		s.ownUserNS = ownNamespace(uidMap, firstLine(string(own)))
		return s, Status{Available: true, Reason: "bwrap at " + bwrap}
	}
	return nil, Status{Reason: strings.Join(reasons, "; ")}
}

// try runs a shell through s, as a run with no cluster: bwrap, the forwarder,
// and the shell launcher's filter. It answers the first line of the run's
// /proc/self/uid_map, read with the shell's builtins alone, since a NixOS
// run's PATH holds no other program. Its error is the first line the run
// wrote, which names the cause.
func (s *Sandbox) try(ctx context.Context, bound time.Duration) (string, error) {
	dir, err := os.MkdirTemp("", "kstack-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	// With no home the policy denies the absolute Never paths alone.
	home, _ := os.UserHomeDir()
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	shell, env := "/bin/sh", []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}
	p, err := s.probePolicyWithin(ctx, shell, dir, home)
	if err != nil {
		return "", fmt.Errorf("no answer in %s", bound)
	}
	cmd, err := s.Command(ctx, Run{
		Shell: shell, Args: []string{"-c", `read -r m < /proc/self/uid_map; echo "$m"`}, Dir: dir, Env: env, Policy: p,
	})
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("no answer in %s", bound)
	case err == nil:
		return firstLine(stdout.String()), nil
	}
	if line := firstLine(stderr.String()); line != "" {
		return "", errors.New(line)
	}
	return "", err
}

// bwrapPaths is the bwraps the probe tries, in order: the first of system
// that exists, then Kstack's own, beside the executable in dir, where it
// exists. The system's comes first because the distribution patches it, while
// Kstack's own changes only when the user installs a new release.
func bwrapPaths(dir string, system []string) []string {
	var paths []string
	for _, p := range system {
		if exists(p) {
			paths = append(paths, p)
			break
		}
	}
	if own := filepath.Join(dir, "..", "lib", "kstack", "bwrap"); exists(own) {
		paths = append(paths, own)
	}
	return paths
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Command is the process that runs r under bwrap, not yet started, made by
// exec.CommandContext on ctx, or why r's policy cannot be enforced. The caller
// sets its output, process group and Cancel, and starts it; exec refuses a
// Cancel on a command made without ctx.
func (s *Sandbox) Command(ctx context.Context, r Run) (*exec.Cmd, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	p := r.Policy
	for _, path := range slices.Concat(p.Files.Read, p.Files.Write, p.Files.Deny, p.Always.Read, p.Always.Write) {
		if overFixedMount(resolved(path)) {
			return nil, fmt.Errorf("a rule on %s would replace a mount every run has", path)
		}
	}
	if !p.Network.Internet {
		cmd := exec.CommandContext(ctx, s.bwrap, s.args(r, "")...)
		cmd.Env = r.Env
		return cmd, nil
	}
	if s.pasta == "" {
		return nil, errors.New("this machine cannot give a sandboxed command the internet: " + s.networkReason)
	}
	if p.Network.Resolver == "" {
		return nil, errors.New("a run with the internet names no resolver")
	}
	at, err := resolverTarget(p)
	if err != nil {
		return nil, err
	}
	return s.pastaCommand(ctx, r, at, true), nil
}

// args is bwrap's arguments for r, in order, since a later mount lies over an
// earlier one: the namespaces; a rule on / itself, then the fixed mounts over
// it; the policy's other rules; r's resolver at resolverAt, unless that is "";
// then the closing remounts and the chain.
func (s *Sandbox) args(r Run, resolverAt string) []string {
	// A run with the internet is in pasta's network namespace, where bwrap
	// starts as uid 0 holding every capability: it makes a user namespace of
	// its own, runs the command as the user, and drops them all.
	args := []string{"--unshare-user-try", "--unshare-net"}
	if r.Policy.Network.Internet {
		args = []string{"--unshare-user", "--uid", strconv.Itoa(os.Getuid()), "--gid", strconv.Itoa(os.Getgid()), "--cap-drop", "ALL"}
	}
	args = append(args,
		"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try",
		"--die-with-parent", "--new-session", "--as-pid-1",
	)
	// Rules sort shallowest first, so any rule on / leads.
	rules := r.Policy.rules()
	var m mounter
	for len(rules) > 0 && rules[0].at == "/" {
		m.mount(rules[0])
		rules = rules[1:]
	}
	m.args = append(m.args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp")
	// These hide the tree beneath them as a denial does, so a rule under one
	// is bound however readable a rule on / made it.
	for _, at := range []string{"/proc", "/dev", "/tmp"} {
		m.mounted = append(m.mounted, rule{path: at, at: at, kind: ruleDeny})
	}
	for _, ru := range rules {
		m.mount(ru)
	}
	args = append(args, m.args...)
	// After every rule and fixed mount, so it lies over whichever holds the
	// path.
	if resolverAt != "" {
		args = append(args, "--ro-bind", r.Policy.Network.Resolver, resolverAt)
	}

	// A remount is not recursive, so the binds made inside a denial stay
	// writable; it comes after them, since bwrap makes their mount points in
	// the tmpfs.
	args = append(args, "--remount-ro", "/")
	for _, d := range m.sealed {
		args = append(args, "--remount-ro", d)
	}
	args = append(args, "--chdir", r.Dir, "--", s.self)
	init := ForwarderArgs(r.Policy.Network.relay())
	if r.Policy.Network.Internet {
		init = slices.Insert(init, 1, stderrOnStdinFlag)
	}
	args = append(args, init...)
	args = append(args, s.self)
	args = append(args, shellCommand(r.Policy.Limits)...)
	args = append(args, r.Shell)
	return append(args, r.Args...)
}

// mounter is bwrap's arguments for a policy's rules, one mount per rule.
type mounter struct {
	args    []string
	mounted []rule   // each rule mounted, in order
	sealed  []string // the denied folders, remounted read-only at the end
	links   []rule   // each Read or Write rule whose path is a link, one per path
}

// mount adds ru's mount. A Read or Write rule is bound at its resolved path,
// and where that differs from the path as written the link is recreated
// there, so a merged /usr still has /bin. A Read rule whose path is readable
// already under an earlier Read is not bound again. The run's own paths are
// bound as written, since bwrap resolves them through the links the tree
// holds. A denied folder is an empty tmpfs, and a denied file /dev/null.
func (m *mounter) mount(ru rule) {
	switch {
	case ru.own:
		m.args = append(m.args, bindFlag(ru.kind), ru.path, ru.path)
	case ru.kind == ruleDeny:
		info, err := os.Stat(ru.at)
		switch {
		case err != nil:
			return
		case info.IsDir():
			m.args = append(m.args, "--tmpfs", ru.at)
			m.sealed = append(m.sealed, ru.at)
			// The tmpfs hides every link inside it, the tree's and those
			// recreated, and a rule may reach its path through one.
			for _, l := range m.links {
				if within(l.path, ru.at) {
					m.args = append(m.args, "--symlink", l.at, l.path)
				}
			}
		default:
			m.args = append(m.args, "--ro-bind", "/dev/null", ru.at)
		}
	case ru.kind == ruleRead && m.readable(ru.at):
		m.link(ru)
		return
	default:
		m.args = append(m.args, bindFlag(ru.kind), ru.at, ru.at)
		m.link(ru)
	}
	m.mounted = append(m.mounted, ru)
}

// bindFlag is bwrap's bind for a Read or Write rule.
func bindFlag(kind ruleKind) string {
	if kind == ruleRead {
		return "--ro-bind"
	}
	return "--bind"
}

// readable reports whether the last mount over at is a Files Read rule's.
func (m *mounter) readable(at string) bool {
	last, ok := m.lastOver(at)
	return ok && last.kind == ruleRead && !last.own
}

// lastOver is the last rule mounted on or above p, if any.
func (m *mounter) lastOver(p string) (rule, bool) {
	for i := len(m.mounted) - 1; i >= 0; i-- {
		if within(p, m.mounted[i].at) {
			return m.mounted[i], true
		}
	}
	return rule{}, false
}

// link recreates ru's path as a link to where it resolves, unless it is its
// own path, a link is already there, or the last mount over it is a bind,
// which holds the tree's own link. A denial mounted later recreates it too.
func (m *mounter) link(ru rule) {
	if ru.at == filepath.Clean(ru.path) || slices.ContainsFunc(m.links, func(l rule) bool { return l.path == ru.path }) {
		return
	}
	m.links = append(m.links, ru)
	if last, ok := m.lastOver(ru.path); ok && last.kind != ruleDeny {
		return
	}
	m.args = append(m.args, "--symlink", ru.at, ru.path)
}

// Confines reports whether a command run through s is confined: always, here.
func (s *Sandbox) Confines() bool { return true }

// NeedsResolver reports whether a run with the internet needs a resolv.conf
// of its own: always, here, since the system's may name a loopback resolver
// the run's namespace cannot reach.
func (s *Sandbox) NeedsResolver() bool { return true }

// Port is where a run's forwarder listens, on the namespace's own loopback.
func (s *Sandbox) Port() (int, error) { return forwarderPort, nil }

// overFixedMount reports whether a rule on p would replace a mount every run
// has: its private /tmp, its /dev, or anything on or under /proc.
func overFixedMount(p string) bool {
	p = filepath.Clean(p)
	return p == "/tmp" || p == "/dev" || within(p, "/proc")
}

// countsPerNamespace reports whether a kernel of release counts a process
// limit per user namespace, which Linux does from 5.14. A release that does
// not parse does not.
func countsPerNamespace(release string) bool {
	major, minor, _ := strings.Cut(release, ".")
	if i := strings.IndexFunc(minor, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minor = minor[:i]
	}
	m, err1 := strconv.Atoi(major)
	n, err2 := strconv.Atoi(minor)
	if err1 != nil || err2 != nil {
		return false
	}
	return m > 5 || m == 5 && n >= 14
}

// ownNamespace reports whether a run's first uid_map line names a user
// namespace other than the sidecar's: two namespaces with different maps are
// different namespaces. An empty line is not the run's own.
func ownNamespace(run, sidecar string) bool {
	fields := strings.Fields(run)
	return len(fields) > 0 && !slices.Equal(fields, strings.Fields(sidecar))
}
