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
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// waitDelay is how long a run waits, once the tool has exited or been stopped, for
// a child it left holding its output open.
const waitDelay = 2 * time.Second

// execRun runs a tool on the host, outside every sandbox, with the sidecar's own
// environment and stdin the null device. It runs in home, since the sidecar's own
// working directory can be one of Kstack's and a tool may cache beside where it
// runs. A run ends taking everything the tool started with it, since a helper it
// runs (AWS's credential_process) can hang where the tool cannot. wait bounds the
// wait for a child holding the output open.
func execRun(home string, wait time.Duration) runner {
	return func(ctx context.Context, env []string, bin string, args ...string) ([]byte, []byte, int, error) {
		cmd, err := toolCommand(ctx, bin, args)
		if err != nil {
			return nil, nil, -1, err
		}
		cmd.Dir = home
		cmd.Env = env
		cmd.WaitDelay = wait
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = runInGroup(cmd)
		// The tool exited and a child it left kept the output open past wait;
		// what the tool printed is whole.
		if errors.Is(err, exec.ErrWaitDelay) {
			err = nil
		}
		if ctx.Err() != nil {
			return nil, nil, -1, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return stdout.Bytes(), stderr.Bytes(), exit.ExitCode(), nil
		}
		if err != nil {
			return nil, nil, -1, err
		}
		return stdout.Bytes(), stderr.Bytes(), 0, nil
	}
}
