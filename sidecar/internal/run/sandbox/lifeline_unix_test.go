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
	"bufio"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

// pipe is a lifeline a test holds the write end of, both ends closed when the
// test ends.
func pipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// The watch ends once the write end closes, and the descriptor closes on
// exec, so no child holds it.
func TestTheWatchEndsAtTheLifelinesEOF(t *testing.T) {
	r, w := pipe(t)
	fd, err := unix.Dup(int(r.Fd()))
	require.NoError(t, err)
	ended := testutil.NewSignal()

	require.NoError(t, watchLifeline(fd, func() { ended.Fire() }))
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	require.NoError(t, err)
	assert.NotZero(t, flags&unix.FD_CLOEXEC)

	require.NoError(t, w.Close())
	ended.Wait(t, "the watch to end")
}

// A descriptor that is not open is no lifeline.
func TestTheWatchRefusesAClosedDescriptor(t *testing.T) {
	r, _ := pipe(t)
	fd, err := unix.Dup(int(r.Fd()))
	require.NoError(t, err)
	require.NoError(t, unix.Close(fd))

	assert.Error(t, watchLifeline(fd, func() {}))
}

// When the lifeline closes, the forwarder kills its whole group: the shell,
// and the sleep it left running.
func TestTheForwarderEndsTheRunAtTheLifelinesEOF(t *testing.T) {
	r, w := pipe(t)
	cmd := initCmd(lifelineFlag, strconv.Itoa(lifelineFD), "--", "/bin/sh", "-c", "sleep 1000 & echo up; wait")
	cmd.ExtraFiles = []*os.File{r}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	requireEndsWithTheLifeline(t, cmd, w)

	assert.Equal(t, 128+int(syscall.SIGKILL), ExitCode(cmd.ProcessState), "the kill reached the forwarder too")
}

// A forwarder told of a lifeline it was not given fails before its child
// starts.
func TestTheForwarderNeedsTheLifelineItIsTold(t *testing.T) {
	code, stdout, stderr := runInit(t, initCmd(lifelineFlag, "1000", "--", "/bin/sh", "-c", "echo ran"))

	assert.Equal(t, initFailed, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "sandbox-init: cannot watch the lifeline")
}

// requireEndsWithTheLifeline starts cmd, a run that prints up and then waits
// on a sleep it started in the background, closes the lifeline's write end
// (a pipe's, or a Sandbox) once the run is up, and waits for its stdout to
// reach EOF, which it does only once every process of the run holding it,
// the sleep among them, has ended. Then it reaps cmd.
func requireEndsWithTheLifeline(t *testing.T, cmd *exec.Cmd, held io.Closer) {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	lines := bufio.NewReader(stdout)
	up, err := lines.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "up\n", up)

	require.NoError(t, held.Close())
	drained := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, lines)
		drained <- err
	}()
	require.NoError(t, testutil.Recv(t, drained, "the run to let go of its stdout"))
	_ = cmd.Wait()
}
