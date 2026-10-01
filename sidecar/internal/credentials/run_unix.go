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

package credentials

import (
	"context"
	"os/exec"
	"syscall"
)

func toolCommand(ctx context.Context, bin string, args []string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, bin, args...), nil
}

// runInGroup runs cmd in a process group of its own. Its context's end kills the
// group, and so does the tool's exit, taking any child it left behind. The exit's
// kill lands before the tool is reaped: until then its pid names this group, and
// after it the id can pass to another process's group.
func runInGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Cancel = kill
	if err := cmd.Start(); err != nil {
		return err
	}
	if waitExit(cmd.Process.Pid) == nil {
		_ = kill()
	}
	return cmd.Wait()
}
