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

// probePolicy is what a probe's run reaches before any run exists: System over
// its shell and PATH, its folder dir writable, and the Never paths under home
// denied. It names no Kstack path and no relay.
func (s *Sandbox) probePolicy(shell string, env []string, dir, home string) Policy {
	files := s.System(home, shell, env)
	files.Write = append(files.Write, dir)
	return Policy{Files: files, Always: AlwaysPolicy{Deny: s.Never(home)}}
}
