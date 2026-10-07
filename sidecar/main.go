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

// Sidecar entry point. Started by the Tauri host, listens on a Unix
// domain socket (no TCP port), and prints the socket path to stdout as
// `READY unix:<path>` so the host can dial it.
//
// This file is lifecycle only: parse flags, bind the listener, build the App
// (the composition root lives in internal/app), serve, and drive graceful
// shutdown. Shutdown signals (any one is sufficient):
//   - SIGINT / SIGTERM
//   - stdin EOF (the Tauri host closes its end when it exits, which is
//     the most reliable cross-platform "parent gone" indicator)
//
// AF_UNIX is supported on macOS, Linux, and Windows 10 build 17063+,
// so a single UDS path works for all our targets.
//
// Started as `kstack-sidecar sandbox-init …` or `sandbox-shell …`, the binary
// is a sandboxed run's forwarder or shell launcher instead (internal/run/sandbox),
// and none of the above runs.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/app"
	"github.com/kstackhq/kstack/sidecar/internal/lib/ipc"
	"github.com/kstackhq/kstack/sidecar/internal/lib/logging"
	"github.com/kstackhq/kstack/sidecar/internal/lib/version"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

func main() {
	// A sandboxed run's forwarder and shell launcher are this binary too, and
	// take none of the sidecar's flags.
	if code, ok := sandbox.Main(os.Args); ok {
		os.Exit(code)
	}
	// Before anything can create a file.
	userUmask := setOwnerOnlyUmask()

	// The log flags decide where records go, so the command line is read before
	// the logger exists. Nothing logs before it: configFromArgs doesn't, and the
	// flag package prints a parse error to stderr itself.
	cfg, err := configFromArgs(os.Args[1:], os.Getenv)
	if err != nil {
		os.Exit(2)
	}
	cfg.App.UserUmask = userUmask
	// Read, so cleared: nothing this process starts inherits a provider key.
	takeProviderKeys()

	logger, closeLog, logErr := logging.Open(cfg.LogFile, cfg.LogStderr, logging.ParseLevel(os.Getenv("KSTACK_LOG_LEVEL")))
	slog.SetDefault(logger)
	if logErr != nil {
		// Open handed back the stderr-only logger, so this reaches a terminal.
		slog.Warn("no log file; logging to stderr only", "path", cfg.LogFile, "err", logErr)
	}

	// The shutdown signals, from here: the login shell app.New runs is under
	// them too, so a quit during it cancels it — Resolve then kills and reaps
	// the shell's session, where the default disposition would end the process
	// and leave the shell running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// Here, not in run, which tests call: a test's app never spawns a
	// developer's real login shell.
	cfg.App.RunLoginShell = true

	code := run(ctx, cfg, os.Stdin, os.Stdout)
	stop()
	// os.Exit runs no deferred call, so the file is closed by hand.
	_ = closeLog()
	os.Exit(code)
}

// run is main without the process: it takes the shutdown context, the parsed
// command line and the two streams the host talks over, and returns the exit
// code instead of calling os.Exit. Everything main does after logging setup
// lives here so the boot and shutdown sequence is reachable from a test.
func run(ctx context.Context, cfg config, stdin io.Reader, stdout io.Writer) int {
	ln, err := ipc.Listen(cfg.Socket)
	if err != nil {
		slog.Error("listen", "socket", cfg.Socket, "err", err)
		return 1
	}
	ln = ipc.Authenticated(ln, ipc.Policy{HostPID: cfg.HostPID})
	// Named pipes vanish with their listener; only the UDS file needs cleanup.
	defer os.Remove(cfg.Socket)

	slog.Info("sidecar starting",
		"version", version.Version,
		"socket", cfg.Socket,
		"pid", os.Getpid(),
		"host_pid", cfg.HostPID,
		"data_dir", cfg.App.DataDir,
		"oauth_issuer", cfg.App.OAuthIssuerURL,
	)

	application, err := app.New(ctx, cfg.App)
	// A quit during startup: a clean exit, not a READY followed by a start that
	// fails on the ended context.
	if ctx.Err() != nil {
		slog.Info("sidecar shutting down", "reason", "signal before start")
		if err == nil {
			_ = application.Close()
		}
		return 0
	}
	if err != nil {
		slog.Error("app init", "err", err)
		return 1
	}

	const maxRequestBytes = 64 * 1024 * 1024
	srv := &http.Server{
		Handler:           http.MaxBytesHandler(application, maxRequestBytes),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// Fired as Shutdown begins so long-lived streams end and Shutdown's wait for
	// in-flight requests can complete; hijacked h2c gRPC streams (which Shutdown
	// can't see) are drained afterwards by DrainWithContext.
	srv.RegisterOnShutdown(application.NotifyShutdown)

	// The host matches the `READY ` prefix; scheme+path are informational (the
	// host picked the path and passed it via --socket).
	fmt.Fprintf(stdout, "READY %s:%s\n", ipc.Scheme, cfg.Socket)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Watch stdin for EOF as a parent-died signal.
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		cancel()
	}()

	stop, err := application.Start(ctx)
	if err != nil {
		// StartAll unwinds what it started but never closes.
		slog.Error("app start", "err", err)
		_ = application.Close()
		return 1
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	reason := "signal"
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "err", err)
			return 1
		}
		reason = "serve returned"
	}

	slog.Info("sidecar shutting down", "reason", reason)

	// Order matters: Shutdown (fires NotifyShutdown, waits for non-hijacked
	// GraphQL requests) → DrainWithContext (waits for hijacked h2c gRPC streams)
	// → stop → Close. See docs/adr/2026-08-09-single-socket-h2c.md.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
	if err := application.DrainWithContext(shutdownCtx); err != nil {
		slog.Warn("drain did not complete", "err", err)
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStop()
	if err := stop(stopCtx); err != nil {
		slog.Warn("stop did not complete cleanly", "err", err)
	}
	_ = application.Close()
	return 0
}
