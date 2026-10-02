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

import "context"

// probePolicy is what a probe's run reaches before any run exists: System over
// its shell, its folder dir writable, and the Never paths under home denied.
// It names no Kstack path and no relay.
func (s *Sandbox) probePolicy(shell, dir, home string) Policy {
	files := s.System(home, shell).Files
	files.Write = append(files.Write, dir)
	return Policy{Files: files, Always: AlwaysPolicy{Deny: s.Never(home)}}
}

// probePolicyWithin is probePolicy, or ctx's error if ctx ends first: the
// policy reads folders under the home, which can hang on a network mount,
// so it is built on a goroutine left behind then.
func (s *Sandbox) probePolicyWithin(ctx context.Context, shell, dir, home string) (Policy, error) {
	built, build := make(chan Policy, 1), buildProbePolicy
	go func() { built <- build(s, shell, dir, home) }()
	select {
	case p := <-built:
		return p, nil
	case <-ctx.Done():
		return Policy{}, ctx.Err()
	}
}

// buildProbePolicy is probePolicy, which a test replaces with one that hangs.
var buildProbePolicy = (*Sandbox).probePolicy
