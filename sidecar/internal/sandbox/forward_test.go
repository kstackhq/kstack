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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitArgsAreTheSocketThePortAndTheCommand(t *testing.T) {
	got, err := parseInitArgs([]string{"--socket", "/run/p.sock", "--port", "6443", "--", "/bin/sh", "-c", "--port 1"})
	require.NoError(t, err)
	assert.Equal(t, initArgs{socket: "/run/p.sock", port: 6443, argv: []string{"/bin/sh", "-c", "--port 1"}}, got)

	got, err = parseInitArgs([]string{"--port=1", "--socket=s", "--", "x"})
	require.NoError(t, err)
	assert.Equal(t, initArgs{socket: "s", port: 1, argv: []string{"x"}}, got)
}

// A run with no cluster names neither flag, and its forwarder listens on
// nothing.
func TestInitArgsTakeNeitherFlag(t *testing.T) {
	got, err := parseInitArgs([]string{"--", "/bin/sh", "-c", "true"})
	require.NoError(t, err)
	assert.Equal(t, initArgs{argv: []string{"/bin/sh", "-c", "true"}}, got)
}

func TestInitArgsRefuseWhatIsMissing(t *testing.T) {
	for name, args := range map[string][]string{
		"no socket":           {"--port", "1", "--", "x"},
		"no port":             {"--socket", "s", "--", "x"},
		"neither, no command": {"--"},
		"port zero":           {"--socket", "s", "--port", "0", "--", "x"},
		"port too big":        {"--socket", "s", "--port", "65536", "--", "x"},
		"no command":          {"--socket", "s", "--port", "1", "--"},
		"an unknown":          {"--socket", "s", "--port", "1", "--other", "--", "x"},
	} {
		_, err := parseInitArgs(args)
		assert.Error(t, err, name)
	}
}

// The forwarder's command line parses back to the run it was written for,
// with or without a socket.
func TestForwarderArgsRoundTrip(t *testing.T) {
	r := Run{Shell: "/bin/sh", Args: []string{"-c", "--port 1"}, Socket: "/run/p.sock", Port: 6443}
	args := ForwarderArgs(r)
	require.Equal(t, InitCommand, args[0])
	got, err := parseInitArgs(append(append(args[1:], r.Shell), r.Args...))
	require.NoError(t, err)
	assert.Equal(t, initArgs{socket: r.Socket, port: 6443, argv: []string{"/bin/sh", "-c", "--port 1"}}, got)

	r.Socket = ""
	got, err = parseInitArgs(append(ForwarderArgs(r)[1:], r.Shell))
	require.NoError(t, err)
	assert.Equal(t, initArgs{argv: []string{"/bin/sh"}}, got)
}
