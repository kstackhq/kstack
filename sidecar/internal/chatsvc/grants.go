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

package chatsvc

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/session"
)

// grantsFor is the rules that last for chatID's life, read on every decision
// and never cached, so one written while a command runs applies to its next
// write. A row that does not decode, or a read that fails, may have hidden a
// Deny, so it reads as permissions.Refused: the chat's cluster writes are
// refused until it is fixed.
func (s *service) grantsFor(ctx context.Context, chatID ChatID) []permissions.Rule {
	rows, err := s.store.Stmts().Query(ctx, stmtSelectChatGrants, string(chatID))
	if err != nil {
		slog.Warn("chat grants not read", "chat", chatID, "err", err)
		return []permissions.Rule{permissions.Refused}
	}
	defer rows.Close()
	var rules []permissions.Rule
	refused := false
	for rows.Next() {
		var raw string
		var r permissions.Rule
		err := rows.Scan(&raw)
		if err == nil {
			// A key Rule does not name could be a narrowing field the
			// build cannot read, and dropping it would widen the grant.
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.DisallowUnknownFields()
			err = dec.Decode(&r)
		}
		if err != nil {
			slog.Warn("chat grant not read", "chat", chatID, "err", err)
			refused = true
			continue
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("chat grants not read", "chat", chatID, "err", err)
		refused = true
	}
	if refused {
		rules = append(rules, permissions.Refused)
	}
	return rules
}

// sessionFor is a chat's session: its policy read live on every write, so a
// mode or rule changed in Settings applies to the next one, a running turn's
// and a running subagent's included. The chat's grants come before the always
// rules.
func (s *service) sessionFor(chatID ChatID, outside bool) session.Session {
	return session.Session{
		Kind:    session.Chat,
		Outside: outside,
		Policy: func(ctx context.Context, kubeContext string) permissions.Policy {
			return permissions.Policy{
				Mode:  s.security.ModeFor(kubeContext).Mode,
				Rules: append(s.grantsFor(ctx, chatID), s.security.Rules()...),
			}
		},
	}
}
