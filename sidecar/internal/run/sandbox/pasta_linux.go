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
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

// pastaOutputLimit is the most of pasta's and bwrap's own output a run that
// never started reports.
const pastaOutputLimit = 64 << 10

// PastaMain is sandbox-pasta, given the arguments after the subcommand,
// -- <pasta> <args…>. It runs pasta with the run's stderr on stdin, which
// pasta and bwrap pass to sandbox-init as they pass every standard stream, and
// pasta's own stderr on a pipe. An older pasta writes notices there whatever
// --quiet says, so what pasta and bwrap write reaches the run's stderr only
// when the run never started. It answers pasta's exit code.
func PastaMain(args []string) int {
	if len(args) < 2 || args[0] != "--" {
		return fail(os.Stderr, PastaCommand, "bad arguments", errors.New("no command after --"))
	}
	r, w, err := os.Pipe()
	if err != nil {
		return fail(os.Stderr, PastaCommand, "cannot make a pipe", err)
	}
	defer r.Close()
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stderr, os.Stdout, w
	// Killed alone, this takes pasta with it, and pasta the rest of the run.
	// The signal follows the thread that started pasta, which stays locked
	// until this returns.
	runtime.LockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	// A stop to the group reaches pasta, which ends the run; this waits for
	// it, as sandbox-init waits for its child.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	err = cmd.Start()
	w.Close()
	if err != nil {
		return fail(os.Stderr, PastaCommand, "cannot start "+args[1], err)
	}
	notices := untilStarted(r)
	err = cmd.Wait()
	if cmd.ProcessState == nil {
		return fail(os.Stderr, PastaCommand, "cannot wait for "+args[1], err)
	}
	_, _ = os.Stderr.Write(notices)
	return ExitCode(cmd.ProcessState)
}

// untilStarted reads r to its end and answers the first pastaOutputLimit
// bytes of it, or nothing once runStarted is among them.
func untilStarted(r io.Reader) []byte {
	var held, window []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		window = append(window, buf[:n]...)
		if bytes.Contains(window, []byte(runStarted)) {
			_, _ = io.Copy(io.Discard, r)
			return nil
		}
		held = append(held, buf[:min(n, pastaOutputLimit-len(held))]...)
		// Keep what a marker split across two reads begins with.
		window = window[max(0, len(window)-len(runStarted)+1):]
		if err != nil {
			return held
		}
	}
}
