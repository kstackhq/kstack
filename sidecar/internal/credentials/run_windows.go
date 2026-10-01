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

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// toolCommand is bin with args, with no console, since every tool is a
// console-subsystem program and the sidecar has none to lend it. gcloud and az
// install as batch wrappers, which only cmd.exe runs and which read their
// arguments by its rules, not CommandLineToArgvW's, so they go through cmd.exe
// on a command line quoted for it.
func toolCommand(ctx context.Context, bin string, args []string) (*exec.Cmd, error) {
	attr := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	ext := strings.ToLower(filepath.Ext(bin))
	if ext != ".cmd" && ext != ".bat" {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.SysProcAttr = attr
		return cmd, nil
	}
	line, err := batchLine(bin, args)
	if err != nil {
		return nil, err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	cmdExe := filepath.Join(system, "cmd.exe")
	cmd := exec.CommandContext(ctx, cmdExe)
	attr.CmdLine = `"` + cmdExe + `" /d /e:on /v:off /s /c "` + line + `"`
	cmd.SysProcAttr = attr
	return cmd, nil
}

// runInGroup runs cmd in a kill-on-close job object, which its context's end
// terminates and which is closed once the tool is reaped, taking whatever the tool
// left running with it. The tool starts suspended and runs only once it is in the
// job, so nothing it starts is outside it.
func runInGroup(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.Cancel = func() error {
		_ = windows.TerminateJobObject(job, 1)
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := joinAndResume(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

// joinAndResume puts the suspended process pid in job, then resumes its one
// thread. exec hands out no thread handle, so the thread is found by a snapshot.
func joinAndResume(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	err = windows.AssignProcessToJobObject(job, h)
	windows.CloseHandle(h)
	if err != nil {
		return err
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return errors.New("credentials: the tool's thread was not found")
}
