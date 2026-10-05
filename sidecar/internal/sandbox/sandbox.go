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

// Package sandbox runs a command in this machine's sandbox, under a Policy
// (policy.go) each platform compiles. It knows no tool and no cluster. Each
// platform's file supplies Probe, Command, System, Never, Confines and Port,
// and InitMain, a run's forwarder (forward.go); Linux's PastaMain starts pasta
// for a run with the internet.
package sandbox

import (
	"fmt"
	"runtime"
	"strings"
)

// Main runs this executable as the part of a run its first argument names,
// sandbox-pasta, sandbox-init or sandbox-shell, and answers the exit code. ok is false for
// any other command line, which is the caller's own. Every binary a run can
// start calls it first: the sidecar, and each test binary whose tests do.
func Main(argv []string) (code int, ok bool) {
	if len(argv) < 2 {
		return 0, false
	}
	switch argv[1] {
	case InitCommand:
		// Each thread the forwarder starts counts against the run's process
		// limit on Linux (forwarderTasks), and relaying needs no parallelism.
		runtime.GOMAXPROCS(1)
		return InitMain(argv[2:]), true
	case ShellCommand:
		return ShellMain(argv[2:]), true
	case PastaCommand:
		return PastaMain(argv[2:]), true
	}
	return 0, false
}

// PastaCommand is the subcommand that starts pasta for a run with the
// internet, on Linux (PastaMain).
const PastaCommand = "sandbox-pasta"

// firstLine is s up to its first newline.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// ResolverAddress is where a run with the internet sends its DNS queries,
// named by the resolv.conf its caller writes where NeedsResolver says so:
// pasta forwards them to the host's first resolver, so a host whose resolver
// is on its own loopback still resolves.
const ResolverAddress = "169.254.1.53"

// Run is one command to start sandboxed.
type Run struct {
	Shell string   // the shell's path
	Args  []string // what the shell is given: -c and the wrapper
	Dir   string   // where it starts
	Env   []string // the whole environment

	Policy Policy
}

// check is why r cannot start: its policy's Check, or a variable in its
// environment that NeverEnv names. Both platforms' Command call it, so no
// caller passes one by hand.
func (r Run) check() error {
	if err := r.Policy.Check(); err != nil {
		return err
	}
	for _, kv := range r.Env {
		if name, _, _ := strings.Cut(kv, "="); Unpassable(name) {
			return fmt.Errorf("%s may not pass into a sandboxed run", name)
		}
	}
	return nil
}

// Status is whether this machine has a sandbox for commands to run through,
// and why not. Whether that sandbox confines them is Sandbox.Confines.
type Status struct {
	Available bool
	Reason    string
	// NetworkAvailable is whether a sandboxed command can be given the
	// internet here, and NetworkReason why not, empty when it can.
	NetworkAvailable bool
	NetworkReason    string
}

// Sandbox is this machine's sandbox. A nil *Sandbox is none.
type Sandbox struct {
	// self is this executable, which runs as a run's forwarder.
	self string
	// bwrap is the bwrap a run starts under, on Linux.
	bwrap string
	// launcher is the sandbox-exec a run starts under, on macOS.
	launcher string
	// perNamespace is whether the kernel counts a process limit per user
	// namespace, and ownUserNS whether the probe's run had a user namespace
	// of its own, on Linux; pastaOwnUserNS is the same for the probe's run
	// under pasta.
	perNamespace, ownUserNS, pastaOwnUserNS bool
	// pasta is the pasta a run with the internet starts under, on Linux; ""
	// where the probe found none that passes, and networkReason says why.
	pasta, networkReason string
}
