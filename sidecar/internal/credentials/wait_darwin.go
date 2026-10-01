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

package credentials

import "golang.org/x/sys/unix"

// waitExit returns once the process pid has exited, leaving it unreaped. macOS has
// no waitid in Go, so the exit is a kqueue event. Attaching to a process that has
// already exited is ESRCH.
func waitExit(pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kq)
	change := make([]unix.Kevent_t, 1)
	unix.SetKevent(&change[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change[0].Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(kq, change, nil, nil); err != nil {
		if err == unix.ESRCH {
			return nil
		}
		return err
	}
	events := make([]unix.Kevent_t, 1)
	for {
		_, err := unix.Kevent(kq, nil, events, nil)
		if err != unix.EINTR {
			return err
		}
	}
}
