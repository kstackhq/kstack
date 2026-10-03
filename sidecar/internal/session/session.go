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

// Package session is one agent run's identity and policy. A leaf: a proxy
// imports it to read the session a run's token maps to, so it imports no proxy.
package session

import (
	"context"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// Kind is what kind of agent a session runs.
type Kind string

const (
	Chat     Kind = "chat"
	Subagent Kind = "subagent"
	Monitor  Kind = "monitor"
)

// Session is one agent run's policy: what its tools and the proxies its runs
// serve read to decide what it may do. The chat and cluster it runs in are the
// runtime's. chatsvc builds a chat's at the start of each turn; a subagent's is
// Narrow of its parent's.
type Session struct {
	Kind    Kind
	Outside bool // the user switched the chat to run outside the sandbox
	// Policy is the mode and rules a cluster write in kubeContext, the context
	// the run's grant was made for, is decided by. It is read live on every
	// write, so a mode or rule changed meanwhile applies to the next one.
	Policy func(ctx context.Context, kubeContext string) permissions.Policy
}

// Narrow is a subagent's session under parent: the parent's, as Kind Subagent.
// The switch is copied at spawn, so a subagent started outside the sandbox
// stays outside, and it decides by its parent's Policy.
func Narrow(parent Session) Session {
	parent.Kind = Subagent
	return parent
}
