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

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Windows takes `..` out by name, as the fences do, so a path holding one
// stands.
func TestConfigFromArgsTakesAKstackPathHoldingDotDot(t *testing.T) {
	dir := `C:\Users\x\link\..\Kstack`
	cfg := mustConfigFromArgs(t, []string{"--data-dir", dir}, noEnv)
	require.Equal(t, dir, cfg.App.DataDir)
}
