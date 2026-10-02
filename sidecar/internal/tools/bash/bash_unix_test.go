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

package bash

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// shortTempBase is where shortTemp makes its directories: macOS's per-user temp
// directory is too long for a run's socket once a test's name is under it.
const shortTempBase = "/tmp"

func TestNewFindsBashOrDeclines(t *testing.T) {
	bash, err := exec.LookPath("bash")
	require.NoError(t, err)
	bin := t.TempDir()
	require.NoError(t, os.Symlink(bash, filepath.Join(bin, "bash")))
	t.Setenv("SHELL", "")
	t.Setenv("PATH", bin)
	_, ok := New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
	assert.True(t, ok)

	t.Setenv("PATH", "")
	_, ok = New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
	assert.False(t, ok)

	// A bash with no home to start in is no tool either.
	t.Setenv("PATH", bin)
	t.Setenv("HOME", "")
	_, ok = New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
	assert.False(t, ok)
}

// The shell is $SHELL when it is zsh or bash, named by an absolute path to an
// executable; anything else falls back to bash on PATH.
func TestTheShellIsTheLoginShellWhenItIsZshOrBash(t *testing.T) {
	bin := t.TempDir()
	onPath := filepath.Join(bin, "bash")
	require.NoError(t, os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", bin)
	shells := t.TempDir()
	login := func(name string, mode os.FileMode) string {
		path := filepath.Join(shells, name)
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), mode))
		return path
	}
	zsh, bash := login("zsh", 0o755), login("bash", 0o755)

	for shell, want := range map[string]struct{ path, kind string }{
		zsh:                          {zsh, "zsh"},
		bash:                         {bash, "bash"},
		login("fish", 0o755):         {onPath, "bash"},
		"zsh":                        {onPath, "bash"},
		filepath.Join(bin, "absent"): {onPath, "bash"},
		"":                           {onPath, "bash"},
	} {
		t.Setenv("SHELL", shell)
		tl, ok := New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
		require.True(t, ok, shell)
		assert.Equal(t, want.path, tl.shell, shell)
		assert.Equal(t, want.kind, tl.kind, shell)
	}

	notExecutable := filepath.Join(t.TempDir(), "zsh")
	require.NoError(t, os.WriteFile(notExecutable, nil, 0o644))
	t.Setenv("SHELL", notExecutable)
	tl, ok := New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
	require.True(t, ok)
	assert.Equal(t, onPath, tl.shell)
}

// New reads the version off the bash it found, and a bash whose version cannot be
// read is still offered, unnamed: the version is a hint to the model, not the gate.
func TestNewReadsTheBashVersion(t *testing.T) {
	t.Setenv("SHELL", "")
	for script, want := range map[string]string{
		"echo 'GNU bash, version 3.2.57(1)-release (arm64-apple-darwin23)'": "3.2",
		"echo 'not bash'": "",
		"echo 'GNU bash, version 5.2.15(1)-release'; exit 1": "",
	} {
		bin := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(bin, "bash"), []byte("#!/bin/sh\n"+script+"\n"), 0o755))
		t.Setenv("PATH", bin)
		tl, ok := New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
		require.True(t, ok, script)
		assert.Equal(t, want, tl.version, script)
	}
}

// holder is a FIFO a command's sleep holds open for writing. A reader opens it
// before the command runs — a blocking open, so it joins when the writer
// does — reads "started" once the sleep is up, and then reads to EOF, which
// arrives exactly when every process holding it is gone. Events, never a
// duration.
type holder struct {
	path string
	line chan string
	gone chan struct{}
}

func newHolder(t *testing.T) *holder {
	t.Helper()
	path := filepath.Join(t.TempDir(), "held")
	require.NoError(t, syscall.Mkfifo(path, 0o600))
	h := &holder{path: path, line: make(chan string, 1), gone: make(chan struct{})}
	go func() {
		defer close(h.gone)
		r, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			return
		}
		defer r.Close()
		br := bufio.NewReader(r)
		l, _ := br.ReadString('\n')
		h.line <- l
		buf := make([]byte, 64)
		for {
			if _, err := br.Read(buf); err != nil {
				return
			}
		}
	}()
	return h
}

// command opens the FIFO, starts the sleep in the background, then says
// "started" in the foreground, so the sleep is in bash's group before the
// greeting and the greeting is written before bash can exit. tail follows.
func (h *holder) command(tail string) string {
	return "exec 3>" + h.path + "; sleep 60 & echo started >&3" + tail
}

// awaitStarted reads the sleep's greeting.
func (h *holder) awaitStarted(t *testing.T) {
	t.Helper()
	assert.Equal(t, "started\n", testutil.Recv(t, h.line, "the sleep to start"))
}

// awaitGone waits for EOF: the sleep has released the FIFO.
func (h *holder) awaitGone(t *testing.T) {
	t.Helper()
	testutil.Wait(t, h.gone, "the sleep to be gone")
}

// A cancelled command is killed at once with everything in its group, and the
// result says so.
func TestRunKillsTheGroupOnCancel(t *testing.T) {
	tl := tool(t)
	h := newHolder(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	res := make(chan result, 1)
	go func() { res <- run(ctx, tl.spec(h.command("; wait"), 1024)) }()
	h.awaitStarted(t)
	cancel()
	r := testutil.Recv(t, res, "the run to end")
	assert.Equal(t, stopCancel, r.Stop)
	assert.Equal(t, 137, r.ExitCode)
	h.awaitGone(t)
}

// A command that exits leaving a process behind in its group does not leave
// it: the group is killed after bash exits, before the result is read.
func TestRunKillsWhatACommandLeftBehind(t *testing.T) {
	tl := tool(t)
	h := newHolder(t)
	r := run(t.Context(), tl.spec(h.command(""), 1024))
	assert.Zero(t, r.ExitCode)
	assert.Equal(t, stopNone, r.Stop)
	h.awaitStarted(t)
	h.awaitGone(t)
}

// A command runs in its workdir, and pwd prints it as the approval request
// showed it, through the link; pwd -P prints where the shell really is. The
// target is resolved too, since t.TempDir can sit under a link (/var on macOS).
func TestACommandRunsInItsWorkdir(t *testing.T) {
	rt := testRuntime(t)
	tl := tool(t)
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(target, link))
	real, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)

	text, isError := tl.Run(t.Context(), rt, commandIn("pwd; pwd -P", link))
	assert.False(t, isError, text)
	assert.Equal(t, link+"\n"+real+"\n", text)
}

// The directory the approval request shows is the one the shell starts in,
// with a workdir and without, since both come from resolveWorkdir over one input.
func TestTheApprovalShowsTheDirectoryItRunsIn(t *testing.T) {
	rt := testRuntime(t)
	tl := tool(t)
	sub := filepath.Join(tools.WorkspacePath(rt.Dir), "sub")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	for _, input := range []json.RawMessage{command("pwd"), commandIn("pwd", "sub"), commandIn("pwd", sub)} {
		approval, err := tl.Approval(t.Context(), rt, input)
		require.NoError(t, err)
		text, isError := tl.Run(t.Context(), rt, input)
		assert.False(t, isError, text)
		assert.Equal(t, approval.Cwd+"\n", text, string(input))
	}
}

// Every call starts fresh: a cd ends with its command, and the next one starts
// in the workspace again.
func TestACdDoesNotCarry(t *testing.T) {
	rt := testRuntime(t)
	tl := tool(t)

	_, isError := tl.Run(t.Context(), rt, command("cd /"))
	require.False(t, isError)
	ws, err := filepath.EvalSymlinks(tools.WorkspacePath(rt.Dir))
	require.NoError(t, err)
	text, isError := tl.Run(t.Context(), rt, command("pwd -P"))
	assert.False(t, isError, text)
	assert.Equal(t, ws+"\n", text)
}

// The command sees the process's own environment, no more and no less, and
// starts in its directory.
func TestACommandsEnvironmentIsTheProcessOwn(t *testing.T) {
	// A BASH_ENV in the test's own environment would have bash source a file
	// that rewrites PATH before the command runs; empty is unset to bash.
	t.Setenv("BASH_ENV", "")
	t.Setenv("KSTACK_TEST_MARK", "marked")
	os.Unsetenv("KSTACK_TEST_UNSET")
	tl := tool(t)
	r := run(t.Context(), tl.spec("echo $KSTACK_TEST_MARK; pwd", 1024))
	assert.Equal(t, "marked\n"+tl.home+"\n", r.Output)
	r = run(t.Context(), tl.spec("echo $HOME; echo $PATH", 4096))
	assert.Equal(t, os.Getenv("HOME")+"\n"+os.Getenv("PATH")+"\n", r.Output)
	r = run(t.Context(), tl.spec("echo $KSTACK_TEST_UNSET", 1024))
	assert.Equal(t, "\n", r.Output)
}

// Kstack adds three variables of its own: that a command runs under it, and the
// two processes the kill shims protect. The host's is left out when the sidecar
// was not told it.
func TestACommandSeesKstacksOwnVariables(t *testing.T) {
	t.Setenv("SHELL", "")
	line := `echo "$KSTACK/$KSTACK_SIDECAR_PID/${KSTACK_HOST_PID-unset}"`
	tl, ok := New(Paths{ShellDir: t.TempDir()}, 4242, nil, nil)
	require.True(t, ok)
	r := run(t.Context(), tl.spec(line, 1024))
	assert.Equal(t, "1/"+strconv.Itoa(os.Getpid())+"/4242\n", r.Output)

	tl, ok = New(Paths{ShellDir: t.TempDir()}, 0, nil, nil)
	require.True(t, ok)
	r = run(t.Context(), tl.spec(line, 1024))
	assert.Equal(t, "1/"+strconv.Itoa(os.Getpid())+"/unset\n", r.Output)
}

// TestMain runs the drain test's helper when the test binary is started as one,
// and the forwarder, the shell launcher and a cluster client when a sandboxed
// run starts it as any of them.
func TestMain(m *testing.M) {
	if ready := os.Getenv("KSTACK_BASH_HELPER_READY"); ready != "" {
		os.Exit(holdThePipe(ready, os.Getenv("KSTACK_BASH_HELPER_RELEASE")))
	}
	if code, ok := sandbox.Main(os.Args); ok {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == clientCommand {
		os.Exit(runClient(os.Args[2:]))
	}
	dir, err := os.MkdirTemp("", "bash-cover")
	if err != nil {
		panic(err)
	}
	childCoverDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// holdThePipe is the helper: it leaves bash's group, so the kill after bash
// exits misses it, says so through ready, and then holds its stdout — the output
// pipe — until release reaches EOF.
func holdThePipe(ready, release string) int {
	if err := syscall.Setpgid(0, 0); err != nil {
		return 1
	}
	if err := os.WriteFile(ready, []byte("ready\n"), 0); err != nil {
		return 1
	}
	f, err := os.Open(release)
	if err != nil {
		return 1
	}
	_, _ = io.Copy(io.Discard, f)
	return 0
}

// stopTest is a run whose deadline, trap and parent a test drives by hand: the
// command's holder says when bash is up, so the deadline fires only once any
// trap the command sets is in place.
type stopTest struct {
	h      *holder
	clock  clock
	stops  chan stop
	cancel context.CancelFunc
	res    chan result
}

// startStop runs trap, then the holder's command in the background and waits on
// it, under hooks the test extends. before runs once bash is up.
func startStop(t *testing.T, trap string, killGrace time.Duration, before func(st *stopTest)) *stopTest {
	t.Helper()
	tl := tool(t)
	st := &stopTest{h: newHolder(t), clock: newClock(), stops: make(chan stop, 2), res: make(chan result, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	st.cancel = cancel
	s := tl.spec(trap+"; "+st.h.command("; wait"), 1024)
	s.killGrace = killGrace
	s.hooks = hooks{
		after:  st.clock.after,
		onStop: func(s stop) { st.stops <- s },
		beforeReap: func(context.Context) {
			st.h.awaitStarted(t)
			before(st)
		},
	}
	go func() { st.res <- run(ctx, s) }()
	return st
}

// A timeout sends SIGTERM first: a trap on it runs, and whatever it exits with,
// the command reads as timed out.
func TestATimeoutSendsTermFirst(t *testing.T) {
	st := startStop(t, `trap 'echo got TERM; exit 0' TERM`, time.Hour, func(st *stopTest) { st.clock.fire() })
	r := testutil.Recv(t, st.res, "the run to end")
	assert.Equal(t, stopTimeout, testutil.Recv(t, st.stops, "the stop"))
	assert.Equal(t, stopTimeout, r.Stop)
	assert.Equal(t, 0, r.ExitCode)
	assert.Contains(t, r.Output, "got TERM")
	assert.True(t, r.failed())
	st.h.awaitGone(t)
}

func TestATrapExitingNonzeroIsStillATimeout(t *testing.T) {
	st := startStop(t, `trap 'exit 7' TERM`, time.Hour, func(st *stopTest) { st.clock.fire() })
	r := testutil.Recv(t, st.res, "the run to end")
	assert.Equal(t, stopTimeout, r.Stop)
	assert.Equal(t, 7, r.ExitCode)
}

// A group that ignores SIGTERM gets SIGKILL once the grace is out.
func TestATimeoutKillsWhatIgnoresTerm(t *testing.T) {
	st := startStop(t, `trap '' TERM`, time.Millisecond, func(st *stopTest) { st.clock.fire() })
	r := testutil.Recv(t, st.res, "the run to end")
	assert.Equal(t, stopTimeout, r.Stop)
	assert.Equal(t, 137, r.ExitCode)
	st.h.awaitGone(t)
}

// A cancel has no grace, so a shutdown's kill lands inside the host's budget
// whatever the command traps.
func TestACancelSkipsTheGrace(t *testing.T) {
	st := startStop(t, `trap '' TERM`, time.Hour, func(st *stopTest) { st.cancel() })
	r := testutil.Recv(t, st.res, "the run to end")
	assert.Equal(t, stopCancel, r.Stop)
	assert.Equal(t, 137, r.ExitCode)
	st.h.awaitGone(t)
}

// A cancel inside a timeout's grace does not wait the grace out.
func TestACancelDuringTheGraceKillsAtOnce(t *testing.T) {
	st := startStop(t, `trap '' TERM`, time.Hour, func(st *stopTest) {
		st.clock.fire()
		assert.Equal(t, stopTimeout, testutil.Recv(t, st.stops, "the SIGTERM"))
		st.cancel()
	})
	r := testutil.Recv(t, st.res, "the run to end")
	assert.Equal(t, stopTimeout, r.Stop, "the stop is the one recorded when it signalled")
	assert.Equal(t, 137, r.ExitCode)
	st.h.awaitGone(t)
}

// An exit of 143 the command made itself is its own, not a timeout.
func TestAnExitOf143IsNotATimeout(t *testing.T) {
	tl := tool(t)
	r := run(t.Context(), tl.spec("kill -TERM $$", 1024))
	assert.Equal(t, stopNone, r.Stop)
	assert.Equal(t, 143, r.ExitCode)
	assert.Equal(t, "Exit code 143\n", resultText(r, time.Minute, nil, false))
}

// A deadline after the reap sends nothing and records nothing.
func TestAnExitReapedBeforeTheDeadlineIsNotRelabelled(t *testing.T) {
	tl := tool(t)
	c := newClock()
	s := tl.spec("exit 0", 1024)
	s.hooks = hooks{after: c.after, afterReap: func(ctx context.Context) {
		c.fire()
		<-ctx.Done()
	}}
	r := run(t.Context(), s)
	assert.Equal(t, stopNone, r.Stop)
	assert.False(t, r.failed())
	assert.Equal(t, "", resultText(r, time.Minute, nil, false))
}

// Bash that exited but was not yet reaped when the deadline fired reads as
// timed out: true at that moment, and never a success.
func TestAnExitUnreapedAtTheDeadlineIsATimeout(t *testing.T) {
	tl := tool(t)
	c := newClock()
	stops := make(chan stop, 1)
	s := tl.spec("exit 0", 1024)
	s.hooks = hooks{after: c.after, onStop: func(s stop) { stops <- s }, beforeReap: func(context.Context) {
		c.fire()
		testutil.Recv(t, stops, "the stop")
	}}
	r := run(t.Context(), s)
	assert.Equal(t, stopTimeout, r.Stop)
	assert.True(t, r.failed())
}

// The one race in which a cancelled command exited 0: it must still be an error,
// or the loop would keep it as a success.
func TestAnExitUnreapedAtACancelIsAnError(t *testing.T) {
	tl := tool(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stops := make(chan stop, 1)
	s := tl.spec("exit 0", 1024)
	s.hooks = hooks{onStop: func(s stop) { stops <- s }, beforeReap: func(context.Context) {
		cancel()
		testutil.Recv(t, stops, "the stop")
	}}
	r := run(ctx, s)
	assert.Equal(t, stopCancel, r.Stop)
	assert.True(t, r.failed())
}

// A command that exits 0 and leaves a process outside its group holding the pipe
// past the deadline is a success: the drain is not the command's.
func TestASlowDrainAfterTheExitIsNotATimeout(t *testing.T) {
	tl := tool(t)
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	require.NoError(t, syscall.Mkfifo(ready, 0o600))
	require.NoError(t, syscall.Mkfifo(release, 0o600))
	t.Setenv("KSTACK_BASH_HELPER_READY", ready)
	t.Setenv("KSTACK_BASH_HELPER_RELEASE", release)

	c := newClock()
	s := tl.spec("'"+exe+"' & read -r _ < '"+ready+"'; exit 0", 1024)
	s.hooks = hooks{after: c.after, afterReap: func(ctx context.Context) {
		c.fire()
		go func() {
			<-ctx.Done()
			f, err := os.OpenFile(release, os.O_WRONLY, 0)
			if err == nil {
				f.Close()
			}
		}()
	}}
	r := run(t.Context(), s)
	assert.Equal(t, stopNone, r.Stop)
	assert.Equal(t, 0, r.ExitCode)
	assert.False(t, r.failed())
}

// A process outside bash's group that still holds the pipe once bash is reaped
// is given pipeGrace and no more: the run ends with what was read.
func TestAPipeHeldPastTheGraceIsLetGo(t *testing.T) {
	tl := tool(t)
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	require.NoError(t, syscall.Mkfifo(ready, 0o600))
	require.NoError(t, syscall.Mkfifo(release, 0o600))
	t.Setenv("KSTACK_BASH_HELPER_READY", ready)
	t.Setenv("KSTACK_BASH_HELPER_RELEASE", release)

	s := tl.spec("'"+exe+"' & read -r _ < '"+ready+"'; echo done; exit 0", 1024)
	s.pipeGrace = 0
	r := run(t.Context(), s)

	// The helper holds the pipe until release reaches EOF.
	f, err := os.OpenFile(release, os.O_WRONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.Equal(t, stopNone, r.Stop)
	assert.Equal(t, 0, r.ExitCode)
	assert.Equal(t, "done\n", r.Output)
}

// A copy cut short by its deadline still takes what the pipe holds, though a
// process outside the group keeps the write end open.
func TestADrainTakesWhatThePipeHolds(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	_, err = w.WriteString("left in the pipe\n")
	require.NoError(t, err)
	require.NoError(t, r.SetReadDeadline(time.Now()))

	var out strings.Builder
	drainPipe(&out, r)

	assert.Equal(t, "left in the pipe\n", out.String())
}

// A file that takes no deadline is not drained.
func TestADrainOfAFileWithNoDeadlineTakesNothing(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	_, err = f.WriteString("not a pipe\n")
	require.NoError(t, err)
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)

	var out strings.Builder
	drainPipe(&out, f)

	assert.Empty(t, out.String())
}

// A copy of a file that takes no deadline is cut short by closing the file.
func TestACutCopyClosesAFileWithNoDeadline(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	read, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		awaitCopy(read, f, 0)
	}()

	require.Eventually(t, func() bool {
		_, err := f.Stat()
		return errors.Is(err, os.ErrClosed)
	}, testutil.Timeout, time.Millisecond, "the cut to close the file")
	close(read)
	testutil.Wait(t, done, "the wait to end once the copy does")
}

// A stop that finds bash already collected by the OS, before the runner's own
// reap, records and sends nothing: bash exited on its own.
func TestAStopAfterTheOSReapSendsNothing(t *testing.T) {
	tl := tool(t)
	cmd := exec.Command(tl.shell, "-c", "exit 0")
	require.NoError(t, cmd.Start())
	_, err := cmd.Process.Wait()
	require.NoError(t, err)

	var seen []stop
	g := newGuard(hooks{onStop: func(s stop) { seen = append(seen, s) }})
	assert.ErrorIs(t, stopBash(t.Context(), cmd, g, time.Hour), os.ErrProcessDone)
	assert.Empty(t, seen)
	assert.Equal(t, stopNone, g.reap())
}

// A stop that arrives once the runner has reaped bash records and sends
// nothing, though the process still answers: its group id may be another's.
func TestAStopAfterTheReapSendsNothing(t *testing.T) {
	tl := tool(t)
	cmd := exec.Command(tl.shell, "-c", "read -r _")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	var seen []stop
	g := newGuard(hooks{onStop: func(s stop) { seen = append(seen, s) }})
	g.reap()
	require.NoError(t, stopBash(t.Context(), cmd, g, time.Hour))
	assert.Empty(t, seen)

	// Nothing was sent, so bash is still reading, and exits cleanly on a line.
	_, err = io.WriteString(stdin, "go\n")
	require.NoError(t, err)
	assert.NoError(t, cmd.Wait())
}

// A results directory the file cannot be created in is a failed save.
func TestASaveThatCannotCreateItsFileFallsBack(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dir := filepath.Join(t.TempDir(), "c1")
	results := filepath.Join(dir, "results")
	require.NoError(t, os.MkdirAll(results, 0o700))
	require.NoError(t, os.Chmod(results, 0o500))
	t.Cleanup(func() { _ = os.Chmod(results, 0o700) })

	text, _ := tool(t).Run(t.Context(), tools.Runtime{Dir: chatDir(dir)}, command("yes | head -c 40000"))
	assert.Regexp(t, `the output could not be saved\]$`, text)
}

// A sandboxed call, in the foreground or the background, starts through the
// sandbox with the shell, arguments, directory and environment a call starts
// with anywhere; a call with the flag never reaches it.
func TestASandboxedCallRunsThroughTheSandbox(t *testing.T) {
	rt := testRuntime(t)
	tl := tool(t)
	fake := &fakeSandboxer{}
	tl.sandboxer = fake

	text, isError := tl.Run(t.Context(), rt, command("echo in-sandbox"))
	assert.False(t, isError, text)
	assert.Equal(t, "in-sandbox\n", text, "the command ran")
	runs := fake.seen()
	require.Len(t, runs, 1)
	r := runs[0]
	assert.Equal(t, tl.shell, r.Shell)
	require.Len(t, r.Args, 2)
	assert.Equal(t, "-c", r.Args[0])
	assert.Contains(t, r.Args[1], "echo in-sandbox", "the wrapper")
	assert.Equal(t, tools.WorkspacePath(rt.Dir), r.Dir)
	assert.Contains(t, r.Env, "KSTACK=1")
	assert.Contains(t, r.Env, "PWD="+tools.WorkspacePath(rt.Dir))

	outside := rt
	outside.OutsideSandbox = true
	text, isError = tl.Run(t.Context(), outside, command("echo outside"))
	assert.False(t, isError, text)
	assert.Equal(t, "outside\n", text)
	assert.Len(t, fake.seen(), 1, "a chat switched outside runs outside the sandbox")

	tasks := newFakeTasks(t)
	text, isError = tl.Run(t.Context(), tools.Runtime{Dir: rt.Dir, Tasks: tasks}, background("echo in-background"))
	require.False(t, isError, text)
	require.Len(t, tasks.started, 1)
	assert.Equal(t, 0, tasks.started[0].Wait().Code)
	runs = fake.seen()
	require.Len(t, runs, 2, "a background task starts through the sandbox too")
	assert.Contains(t, runs[1].Args[1], "echo in-background")
	assert.Equal(t, tools.WorkspacePath(rt.Dir), runs[1].Dir)
}

// A sandboxed run leads a session of its own, so it has no controlling
// terminal to push input into; a run outside keeps the sidecar's session.
func TestASandboxedRunLeadsItsOwnSession(t *testing.T) {
	w, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	s := spec{shell: "/bin/sh", command: "true", dir: t.TempDir()}

	outside, err := shellCmd(t.Context(), s, w)
	require.NoError(t, err)
	s.sandboxedRun = &sandboxedRun{boxer: &fakeSandboxer{}}
	inside, err := shellCmd(t.Context(), s, w)
	require.NoError(t, err)

	assert.Equal(t, &syscall.SysProcAttr{Setpgid: true}, outside.SysProcAttr)
	assert.Equal(t, &syscall.SysProcAttr{Setsid: true}, inside.SysProcAttr)
}

// A task's sandbox that hangs while it prepares, as one resolving a path on a
// stalled network mount does, gives way when the call is cancelled: the task
// is refused, and nothing starts.
func TestATaskWhoseSandboxHangsIsRefusedWhenTheCallEnds(t *testing.T) {
	rt := testRuntime(t)
	tl := tool(t)
	fake := &fakeSandboxer{hold: testutil.NewSignal()}
	tl.sandboxer = fake
	tasks := newFakeTasks(t)
	rt.Tasks = tasks
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan string, 1)

	go func() {
		text, _ := tl.Run(ctx, rt, background("echo started"))
		done <- text
	}()
	fake.hold.Wait(t, "the sandbox to start preparing")
	cancel()

	text := testutil.Recv(t, done, "the call to return")
	assert.Contains(t, text, "context canceled")
	assert.Empty(t, tasks.started)
}

// A sandboxed run carries the built environment and the Workspace policy: the
// sandbox's System less what lies in Kstack's directories, the Never paths and
// Kstack's directories denied, and the run's own paths inside them — its
// run's directory read, the workspace and its TMPDIR written.
// With no cluster it has no relay.
func TestTheWorkspacePolicyIsSystemAndTheRunsOwn(t *testing.T) {
	rt := testRuntime(t)
	tl := tool(t)
	k := kstackDirs(t)
	fake := &fakeSandboxer{
		system: sandbox.FilePolicy{Read: []string{"/usr", filepath.Join(k.data, "bin")}, Deny: []string{"/usr/var"}},
		never:  []string{filepath.Join(tl.home, ".ssh")},
	}
	tl.sandboxer = fake

	text, isError := tl.Run(t.Context(), rt, command(`echo "$ZDOTDIR"; echo "$TMPDIR"`))
	require.False(t, isError, text)

	runs := fake.seen()
	require.Len(t, runs, 1)
	r := runs[0]
	lines := strings.Fields(text)
	require.Len(t, lines, 2)
	tmp := lines[1]
	rd := &runDir{path: lines[0], tmp: tmp}
	ws := tools.WorkspacePath(rt.Dir)
	assert.Equal(t, sandboxedRunEnv(os.Environ(), tl.env, ws, ws, rd, nil), r.Env)
	assert.Equal(t, sandbox.Policy{
		Files: sandbox.FilePolicy{Read: []string{"/usr"}, Deny: []string{"/usr/var"}},
		Always: sandbox.AlwaysPolicy{
			Deny:   fake.never,
			Kstack: []string{k.data, k.cache, k.runtime},
			Read:   []string{rd.path},
			Write:  []string{ws, tmp},
		},
	}, r.Policy)
	assert.NoError(t, r.Policy.Check())
}

// A test's extra writable directories are Files Write rules: they lie in none
// of Kstack's directories.
func TestASandboxedRunWritesTheExtraWritable(t *testing.T) {
	tl := tool(t)
	fake := &fakeSandboxer{}
	tl.sandboxer = fake
	tl.extraWritable = []string{"/cover"}

	text, isError := tl.Run(t.Context(), testRuntime(t), command(`echo "$TMPDIR"`))
	require.False(t, isError, text)

	require.Len(t, fake.seen(), 1)
	assert.Equal(t, []string{"/cover"}, fake.seen()[0].Policy.Files.Write)
}

// A run's directory and its TMPDIR, in the cache's tmp directory, go when the
// run ends, and a directory the command left in its TMPDIR that it cannot write
// does not keep it.
func TestARunsTMPDIRGoesWithIt(t *testing.T) {
	tl := tool(t)
	tl.sandboxer = &fakeSandboxer{}

	text, isError := tl.Run(t.Context(), testRuntime(t), command(`mkdir "$TMPDIR/x" && touch "$TMPDIR/x/f" && chmod 500 "$TMPDIR/x" && echo "$ZDOTDIR" && echo "$TMPDIR"`))
	require.False(t, isError, text)

	lines := strings.Fields(text)
	require.Len(t, lines, 2)
	assert.True(t, strings.HasPrefix(lines[0], tl.runsDir), lines[0])
	assert.True(t, strings.HasPrefix(lines[1], tl.tmpDir), lines[1])
	assert.NoDirExists(t, lines[0])
	assert.NoDirExists(t, lines[1])
}

// A run with no cluster has no forwarder: no socket, and no port asked for,
// since nothing would dial it. The shell is the command's own process.
func TestARunWithNoClusterHasNoForwarder(t *testing.T) {
	tl := tool(t)
	fake := &fakeSandboxer{}
	tl.sandboxer = fake

	text, isError := tl.Run(t.Context(), testRuntime(t), command(`ls "$ZDOTDIR"`))
	require.False(t, isError, text)

	assert.Empty(t, text, "no socket in the run's directory")
	require.Len(t, fake.seen(), 1)
	assert.Empty(t, fake.seen()[0].Policy.Network.Relays)
	assert.Zero(t, fake.portsAsked())
}

// A run with no cluster has no kubeconfig in its directory, and the
// environment names none.
func TestWithNoClusterThereIsNoKubeconfig(t *testing.T) {
	tl := tool(t)
	tl.sandboxer = &fakeSandboxer{}

	text, isError := tl.Run(t.Context(), testRuntime(t), command(`ls "$ZDOTDIR"; echo "${KUBECONFIG-none}"`))
	require.False(t, isError, text)
	assert.Equal(t, "none\n", text)
}

// clusterTool is a tool over a fake sandbox and the clusters given, each
// claimed through a lease with no connection.
func clusterTool(t *testing.T, clusters map[apimeta.ClusterID]*clustersvc.Cluster) *Tool {
	t.Helper()
	tl := tool(t)
	tl.sandboxer = &fakeSandboxer{}
	tl.clusterSvc = fakeService{clusters: clusters, lease: &fakeLease{}}
	return tl
}

// runOn runs line sandboxed in a fresh chat on cluster, and answers its output.
func runOn(t *testing.T, tl *Tool, cluster apimeta.ClusterID, line string) string {
	t.Helper()
	text, isError := tl.Run(t.Context(), tools.Runtime{ClusterID: cluster, Dir: testChatDir(t)}, command(line))
	require.False(t, isError, text)
	return text
}

// A run on a cluster has the run's kubeconfig, 0600 in the run's directory
// beside its proxy's socket, naming the card's context and the sandbox's port,
// and the socket is the Run's.
func TestTheRunsKubeconfigNamesTheCardsContextAndPort(t *testing.T) {
	tl := clusterTool(t, map[apimeta.ClusterID]*clustersvc.Cluster{"7": kubeCluster("prod-admin", "uid")})

	text := runOn(t, tl, "7", `ls "$ZDOTDIR"; stat -c %a "$KUBECONFIG" 2>/dev/null || stat -f %Lp "$KUBECONFIG"; cat "$KUBECONFIG"`)

	lines := strings.SplitN(text, "\n", 4)
	assert.Equal(t, []string{"kubeconfig", socketName, "600"}, lines[:3])
	assert.Contains(t, lines[3], "current-context: prod-admin\n")
	// The token in proxy-url's userinfo is redacted from what the model reads.
	assert.Contains(t, lines[3], "proxy-url: http://[redacted]@127.0.0.1:6443\n")
	runs := tl.sandboxer.(*fakeSandboxer).seen()
	require.Len(t, runs, 1)
	own := runs[0].Policy.Always.Read
	assert.Equal(t, []sandbox.Relay{{Port: 6443, Socket: filepath.Join(own[len(own)-1], socketName)}}, runs[0].Policy.Network.Relays)
}

// Every chat on a cluster shares its kubectl cache, under the cluster's
// directory; another cluster's, and a new server identity's, is another.
func TestTwoChatsOnOneClusterShareTheCache(t *testing.T) {
	clusters := map[apimeta.ClusterID]*clustersvc.Cluster{"7": kubeCluster("prod", "uid-1"), "8": kubeCluster("staging", "uid-2")}
	tl := clusterTool(t, clusters)
	cache := `echo "$KUBECACHEDIR"`

	first, second := runOn(t, tl, "7", cache), runOn(t, tl, "7", cache)
	other := runOn(t, tl, "8", cache)

	assert.Equal(t, first, second)
	assert.NotEqual(t, first, other)
	assert.True(t, strings.HasPrefix(first, filepath.Join(tl.kubectlDir, "7")+string(filepath.Separator)), first)

	clusters["7"] = kubeCluster("prod", "uid-3")
	assert.NotEqual(t, first, runOn(t, tl, "7", cache), "a new server identity")
}

// A cluster that is gone could not start: nothing runs, and nothing is left in
// the runs directory.
func TestAGoneClusterCouldNotStart(t *testing.T) {
	marked := kubeCluster("prod", "uid")
	at := time.Now()
	marked.DeletionRequestedAt = &at
	for name, fake := range map[string]fakeService{
		"nil record": {},
		"marked":     {clusters: map[apimeta.ClusterID]*clustersvc.Cluster{"7": marked}},
		"get error":  {err: errors.New("disk")},
	} {
		t.Run(name, func(t *testing.T) {
			tl := clusterTool(t, nil)
			tl.clusterSvc = fake
			boxer := tl.sandboxer.(*fakeSandboxer)

			text, isError := tl.Run(t.Context(), tools.Runtime{ClusterID: "7", Dir: testChatDir(t)}, command("echo ran"))

			assert.True(t, isError)
			assert.True(t, strings.HasPrefix(text, "could not start: "), text)
			assert.Empty(t, boxer.seen())
			for _, dir := range []string{tl.runsDir, tl.tmpDir} {
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				assert.Empty(t, entries, dir)
			}
		})
	}
}

// zsh -c reads $ZDOTDIR/.zshenv, and HOME is the workspace, so a .zshenv a
// command left there would run before every later command: ZDOTDIR is the run's
// directory, which holds none.
func TestZshReadsNoWorkspaceZshenv(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	tl := tool(t)
	tl.shell, tl.kind = zsh, "zsh"
	tl.sandboxer = &fakeSandboxer{}
	rt := testRuntime(t)
	require.NoError(t, makeWorkspace(rt.Dir))
	require.NoError(t, os.WriteFile(filepath.Join(tools.WorkspacePath(rt.Dir), ".zshenv"), []byte("echo sourced\n"), 0o600))

	text, isError := tl.Run(t.Context(), rt, command("echo ok"))
	require.False(t, isError, text)
	assert.Equal(t, "ok\n", text)
}

// A sandboxed call whose workdir leaves the workspace is refused before the
// user sees anything, in words that name the workspace and the way out. The
// workspace itself and a directory under it are taken.
func TestASandboxedWorkdirOutsideTheWorkspaceIsRefused(t *testing.T) {
	rt := testRuntime(t)
	ws := tools.WorkspacePath(rt.Dir)
	boxer := &fakeSandboxer{confines: true}
	tl := Tool{home: t.TempDir(), sandboxer: boxer}

	for _, workdir := range []string{"~/..", "../x", "/etc"} {
		_, err := tl.Approval(t.Context(), rt, commandIn("ls", workdir))
		var refusal *tools.Refusal
		require.ErrorAs(t, err, &refusal, workdir)
		assert.Equal(t, "A sandboxed command starts in its workspace, "+ws+", or a directory under it. Name one there, or ask the user to run this chat outside the sandbox.", refusal.Result)

		text, isError := tl.Run(t.Context(), rt, commandIn("ls", workdir))
		assert.Equal(t, badInput, text)
		assert.True(t, isError)
	}
	assert.Empty(t, boxer.runs, "nothing runs")

	for _, workdir := range []string{ws, filepath.Join(ws, "sub"), "~/sub"} {
		_, err := tl.Approval(t.Context(), rt, commandIn("ls", workdir))
		assert.NoError(t, err, workdir)
	}
}

// A sandbox that does not confine still counts as one: the model was told of
// it, so its calls resolve by the sandboxed column, and ask.
func TestASandboxThatDoesNotConfineResolvesAsSandboxed(t *testing.T) {
	rt := testRuntime(t)
	ws := tools.WorkspacePath(rt.Dir)
	home := t.TempDir()
	tl := Tool{home: home, sandboxer: &fakeSandboxer{confines: false}}

	got, err := tl.Approval(t.Context(), rt, commandIn("ls", "~"))
	require.NoError(t, err)
	assert.Equal(t, tools.Approval{Cwd: ws}, got)

	outside := rt
	outside.OutsideSandbox = true
	got, err = tl.Approval(t.Context(), outside, commandIn("ls", "~"))
	require.NoError(t, err)
	assert.Equal(t, tools.Approval{Cwd: home}, got)
}

// A call makes the workspace before it starts, in the foreground and in the
// background, so the first command of a chat has somewhere to start.
func TestRunMakesTheWorkspace(t *testing.T) {
	tl := tool(t)
	rt := testRuntime(t)
	ws := tools.WorkspacePath(rt.Dir)

	text, isError := tl.Run(t.Context(), rt, command("pwd"))
	assert.False(t, isError, text)
	assert.Equal(t, ws+"\n", text)

	rt = tools.Runtime{Dir: testChatDir(t), Tasks: newFakeTasks(t)}
	text, isError = tl.Run(t.Context(), rt, background("true"))
	assert.False(t, isError, text)
	assert.DirExists(t, tools.WorkspacePath(rt.Dir))
}

// A workspace that cannot be made is a call that could not start, in the
// foreground and in the background.
func TestAWorkspaceThatCannotBeMadeCouldNotStart(t *testing.T) {
	tl := tool(t)
	results := testChatDir(t)
	require.NoError(t, os.MkdirAll(string(results), 0o700))
	require.NoError(t, os.WriteFile(tools.WorkspacePath(results), nil, 0o600))

	text, isError := tl.Run(t.Context(), tools.Runtime{Dir: results}, command("true"))
	assert.True(t, isError)
	assert.Contains(t, text, "could not start: ")

	tasks := newFakeTasks(t)
	text, isError = tl.Run(t.Context(), tools.Runtime{Dir: results, Tasks: tasks}, background("true"))
	assert.True(t, isError)
	assert.Contains(t, text, "could not start: ")
	assert.Empty(t, tasks.started)
}

// A sandboxed run that cannot be given what it needs could not start, and
// nothing runs: no port for its kubeconfig, a runs directory too long for its
// socket, or a policy the sandbox refuses.
func TestASandboxedRunThatCannotBePreparedCouldNotStart(t *testing.T) {
	long := "/" + strings.Repeat("t", maxSocketPath)
	for name, tc := range map[string]struct {
		boxer   *fakeSandboxer
		cluster bool
		runs    string
		want    string
	}{
		"no port":       {boxer: &fakeSandboxer{portErr: errors.New("no free port")}, cluster: true, want: "no free port"},
		"too long runs": {boxer: &fakeSandboxer{}, runs: long, want: long},
		"refused":       {boxer: &fakeSandboxer{cmdErr: errors.New("a rule over a fixed mount")}, want: "a rule over a fixed mount"},
	} {
		t.Run(name, func(t *testing.T) {
			tl := tool(t)
			tl.sandboxer = tc.boxer
			if tc.runs != "" {
				tl.runsDir = tc.runs
			}
			rt := testRuntime(t)
			if tc.cluster {
				tl.clusterSvc = fakeService{
					clusters: map[apimeta.ClusterID]*clustersvc.Cluster{"7": kubeCluster("prod", "uid")},
					lease:    &fakeLease{serverUID: "uid"},
				}
				rt.ClusterID = "7"
			}

			text, isError := tl.Run(t.Context(), rt, command("echo ran"))

			assert.True(t, isError)
			assert.True(t, strings.HasPrefix(text, "could not start: "), text)
			assert.Contains(t, text, tc.want)
			assert.NotContains(t, text, "ran\n")
			if tc.boxer.cmdErr == nil {
				assert.Empty(t, tc.boxer.seen())
			}
		})
	}
}

// Stored arguments whose workdir is absolute only on Unix, where alone a run
// could have written them, read the same. A case here is never edited.
func TestActionOfReadsOldUnixRowsTheSame(t *testing.T) {
	for raw, want := range map[string]tools.Action{
		`{"command":"echo \"a\\tb\" ‮","workdir":"/tmp"}`: {Command: &tools.CommandAction{Text: "echo \"a\\tb\" ‮", Cwd: "/home/ana"}},
	} {
		got, err := ActionOf(json.RawMessage(raw), "/home/ana", false)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
}

// A chat's directory that cannot be opened by the time the output is saved
// is a failed save. The command puts a file in its place, which Windows
// refuses while the run holds the directory open.
func TestASaveThatCannotOpenItsDirectoryFallsBack(t *testing.T) {
	rt := testRuntime(t)
	chat := string(rt.Dir.(chatDir))
	line := fmt.Sprintf("cd / && rm -rf '%s' && touch '%s' && yes | head -c 40000", chat, chat)

	text, _ := tool(t).Run(t.Context(), rt, command(line))
	assert.Regexp(t, `the output could not be saved\]$`, text)
}

// Docker Desktop puts its programs in ~/.docker/bin, whose tree ~/.docker is a
// Never path. With it on PATH the policy passes Check and the run starts, and
// neither ~/.docker/config.json nor ~/.docker/bin can be read.
func TestDockerDesktopsBinStaysDenied(t *testing.T) {
	tl := proxyTool(t, &fakeLease{})
	tl.sandboxer = confining(t)
	bin := filepath.Join(tl.home, ".docker", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho docker-ran\n"), 0o700))
	config := filepath.Join(tl.home, ".docker", "config.json")
	require.NoError(t, os.WriteFile(config, []byte("secret"), 0o600))
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))

	text, isError := tl.Run(t.Context(), testRuntime(t), command(fmt.Sprintf(`cat '%s' 2>/dev/null; ls '%s' 2>/dev/null; echo started`, config, bin)))

	require.False(t, isError, text)
	assert.Equal(t, "started\n", text)
}
