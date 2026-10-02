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
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// ShellMain is sandbox-shell, given the arguments after the subcommand: it
// installs the filter and execs the command under it, so the filter holds the
// command and everything it starts. It answers only if it cannot.
func ShellMain(args []string) int {
	argv, err := parseShellArgs(args)
	if err != nil {
		return fail(os.Stderr, ShellCommand, "bad arguments", err)
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fail(os.Stderr, ShellCommand, "cannot start "+argv[0], err)
	}
	// no_new_privs holds for the calling thread alone, and seccomp refuses a
	// thread without it; the exec below runs on this one.
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fail(os.Stderr, ShellCommand, "cannot set no_new_privs", err)
	}
	if err := install(filter()); err != nil {
		return fail(os.Stderr, ShellCommand, "cannot install the filter", err)
	}
	err = unix.Exec(path, argv, os.Environ())
	return fail(os.Stderr, ShellCommand, "cannot start "+argv[0], err)
}

// install loads prog on every thread of the process, since the Go runtime
// runs several.
func install(prog []bpf.Instruction) error {
	filters, err := assemble(prog)
	if err != nil {
		return err
	}
	fprog := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	tid, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER,
		unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&fprog)))
	switch {
	case errno != 0:
		return errno
	case tid != 0:
		return fmt.Errorf("thread %d could not take it", tid)
	}
	return nil
}

// Offsets into seccomp_data: the syscall's number, its architecture, and the
// low words of its first two arguments, which is enough for every flag it
// reads.
const (
	offNr   = 0
	offArch = 4
	offArg0 = 16
	offArg1 = 24
)

// sockTypeMask is a socket type's bits below SOCK_NONBLOCK and SOCK_CLOEXEC.
const sockTypeMask = 0xf

var (
	allow  = bpf.RetConstant{Val: unix.SECCOMP_RET_ALLOW}
	eperm  = bpf.RetConstant{Val: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)}
	enosys = bpf.RetConstant{Val: unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)}
)

// filter is the seccomp program sandbox-shell installs over the shell.
func filter() []bpf.Instruction {
	prog := []bpf.Instruction{
		bpf.LoadAbsolute{Off: offArch, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: auditArch, SkipTrue: 1},
		bpf.RetConstant{Val: unix.SECCOMP_RET_KILL_PROCESS},
		bpf.LoadAbsolute{Off: offNr, Size: 4},
	}
	prog = append(prog, archRules...)
	prog = append(prog,
		// IP alone, which the network namespace fences. A Unix socket reaches
		// the host's resolvers and buses, and vsock the hypervisor and other
		// VMs, and no namespace fences either.
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.SYS_SOCKET, SkipFalse: 5},
		bpf.LoadAbsolute{Off: offArg0, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.AF_INET, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.AF_INET6, SkipTrue: 1},
		eperm,
		allow,

		// A stream or seqpacket pair reaches its other end alone, but a
		// datagram end can send to a pathname socket, and a raw Unix pair is
		// a datagram one.
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.SYS_SOCKETPAIR, SkipFalse: 6},
		bpf.LoadAbsolute{Off: offArg1, Size: 4},
		bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: sockTypeMask},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.SOCK_STREAM, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.SOCK_SEQPACKET, SkipTrue: 1},
		eperm,
		allow,
	)
	for _, nr := range []uint32{unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER, unix.SYS_CLONE3} {
		prog = append(prog, bpf.JumpIf{Cond: bpf.JumpEqual, Val: nr, SkipFalse: 1}, enosys)
	}
	// No process traces, reads or writes another, takes its descriptors, or
	// compares kernel objects with it, its own children included. Nor does
	// one reach the kernel keyring, which bwrap leaves the user's session's.
	for _, nr := range []uint32{
		unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV,
		unix.SYS_PIDFD_GETFD, unix.SYS_KCMP, unix.SYS_PROCESS_MADVISE,
		unix.SYS_KEYCTL, unix.SYS_ADD_KEY, unix.SYS_REQUEST_KEY,
	} {
		prog = append(prog, bpf.JumpIf{Cond: bpf.JumpEqual, Val: nr, SkipFalse: 1}, eperm)
	}
	return append(prog,
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.SYS_UNSHARE, SkipTrue: 1},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.SYS_CLONE, SkipFalse: 4},
		bpf.LoadAbsolute{Off: offArg0, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpBitsSet, Val: unix.CLONE_NEWUSER, SkipFalse: 1},
		eperm,
		allow,

		allow,
	)
}

// assemble is prog in the form the kernel loads.
func assemble(prog []bpf.Instruction) ([]unix.SockFilter, error) {
	raw, err := bpf.Assemble(prog)
	if err != nil {
		return nil, err
	}
	filters := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filters[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	return filters, nil
}
