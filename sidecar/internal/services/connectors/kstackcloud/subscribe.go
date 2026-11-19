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

package kstackcloud

import (
	"sync"

	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
)

// Subscribe streams the state: current on subscribe, then each change of
// account. The engine also publishes a sign-in under way, failed or cancelled,
// each of which the wire draws as signed out.
func (s *Service) Subscribe() (<-chan connectors.State, func()) {
	in, unsubscribe := s.engine.Subscribe()
	out := make(chan connectors.State)
	done := make(chan struct{})
	go func() {
		defer close(out)
		var last *account
		for st := range in {
			a := accountOf(st)
			if last != nil && a == *last {
				continue
			}
			last = &a
			select {
			case out <- st:
			case <-done:
				return
			}
		}
	}()
	return out, sync.OnceFunc(func() {
		close(done)
		unsubscribe()
	})
}

// account is what a subscriber is told of.
type account struct {
	connected            bool
	subject, email, name string
}

func accountOf(st connectors.State) account {
	return account{st.Phase == connectors.Connected, st.Subject, st.Email, st.Name}
}
