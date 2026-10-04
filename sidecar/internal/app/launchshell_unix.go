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

//go:build unix

package app

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/loginshell"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// resolveShell is loginshell.Resolve, which a test replaces.
var resolveShell = loginshell.Resolve

// launchShell runs the account's login shell once, in sb: its environment is
// set where the platform needs it, and its PATH is what the sandbox's is
// resolved from. Its output leaves the sandbox, so it reads nothing on the
// denied-always list, and nothing of Kstack's directories (kstackDirs); its
// TMPDIR is a run's, under tmpDir. sb is nil for no sandbox, where the shell
// runs unconfined, or not at all where nothing reads its answer
// (skipResolution). It answers the shell's PATH, nil when it was not read, and
// why not, "" when it was.
func launchShell(ctx context.Context, sb sandboxer, kstackDirs []string, tmpDir string) (path []string, fault string) {
	if skipResolution(sb) {
		return nil, ""
	}
	var deny []string
	if sb == nil {
		slog.Warn("login shell runs unconfined: this machine has no sandbox")
	} else {
		home, _ := os.UserHomeDir()
		deny = sb.Never(home)
	}
	return runShell(ctx, loginshell.In(sb, deny, kstackDirs, bash.TempDir(tmpDir)), resolveShell)
}

// runShell calls resolve once, through start, under the shell's timeout, and
// hands its answer to both readers. It logs how long the shell took, so the
// timeout can be judged against real startup files. A failure is not a startup
// error: it logs the reason, never a value, and answers no path.
func runShell(ctx context.Context, start loginshell.Start, resolve func(context.Context, loginshell.Start) (loginshell.Result, *loginshell.Fault)) ([]string, string) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, loginshell.DefaultTimeout)
	defer cancel()

	res, f := resolve(ctx, start)
	if f != nil {
		slog.Warn("login shell not read",
			"reason", f.Reason,
			"exit_code", f.ExitCode,
			"elapsed", time.Since(started),
		)
		return nil, f.Reason
	}
	slog.Info("login shell read", "elapsed", time.Since(started))
	setShellEnv(res.Env)
	return res.Path, ""
}
