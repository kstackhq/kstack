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

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The fences read `..` by name and the filesystem through links, so a Kstack
// path holding one could put files outside them. The host refuses such a root
// first; the sidecar does not rely on it.
func TestConfigFromArgsRefusesAKstackPathHoldingDotDot(t *testing.T) {
	for _, flag := range []string{"--data-dir", "--cache-dir", "--runtime-dir", "--config-dir", "--log-file"} {
		t.Run(flag, func(t *testing.T) {
			_, err := configFromArgs([]string{flag, "/home/x/link/../kstack"}, noEnv.get)
			require.ErrorContains(t, err, flag)
		})
	}
}

// A name that only begins with dots is an ordinary one.
func TestConfigFromArgsTakesANameBeginningWithDots(t *testing.T) {
	cfg := mustConfigFromArgs(t, []string{"--data-dir", "/home/x/..data"}, noEnv)
	require.Equal(t, "/home/x/..data", cfg.App.DataDir)
}
