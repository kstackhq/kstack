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

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/loginshell"
)

// launchShell runs the account's login shell once: its environment is
// set where the platform needs it, and its PATH is what the sandbox's
// is resolved from.
func launchShell(ctx context.Context) (path []string, fault string) {
	return runShell(ctx, loginshell.In(nil, nil, nil, nil), loginshell.Resolve)
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
