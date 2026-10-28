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

package sandbox

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// endGroupAtEOF watches the lifeline on fd and, at its EOF, kills the
// caller's process group. The forwarder leads the run's group, so one kill
// reaches every process in it, the caller too, and the caller waits for its
// own: macOS delivers it after kill returns, so an exit would race it. As a
// PID namespace's first process the kill passes it by, and its exit ends the
// namespace. sandbox-pasta leads pasta's: pasta killed during setup leaves
// the child it made for the run's namespaces, which no Pdeathsig reaches.
func endGroupAtEOF(fd int) error {
	return watchLifeline(fd, func() {
		_ = unix.Kill(0, unix.SIGKILL)
		if os.Getpid() != 1 {
			select {}
		}
		os.Exit(initFailed)
	})
}

// watchLifeline closes fd on exec, so no child inherits it, and calls end
// once it reads EOF. Nothing writes to the lifeline, so any read's end is the
// sidecar's. It fails when fd is not open.
func watchLifeline(fd int, end func()) error {
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "lifeline")
	go func() {
		_, _ = io.Copy(io.Discard, f)
		end()
	}()
	return nil
}
