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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/session"
)

// grantsFor is the rules that last for chatID's life, read on every decision
// and never cached, so one written while a command runs applies to its next
// write. A row that does not decode, or a read that fails, may have hidden a
// Deny, so it reads as permissions.Refused: the chat's cluster writes are
// refused until it is fixed.
func (s *service) grantsFor(ctx context.Context, chatID ChatID) []permissions.Rule {
	rules, refused, err := readGrants(ctx, s.store.Stmts(), chatID)
	if err != nil {
		slog.Warn("chat grants not read", "chat", chatID, "err", err)
		return []permissions.Rule{permissions.Refused}
	}
	if refused {
		rules = append(rules, permissions.Refused)
	}
	return rules
}

// readGrants is chatID's rules in the order written, and whether a row did
// not decode, which is logged and left out.
func readGrants(ctx context.Context, st stmts, chatID ChatID) ([]permissions.Rule, bool, error) {
	rows, err := st.Query(ctx, stmtSelectChatGrants, string(chatID))
	if err != nil {
		return nil, false, err
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
	return rules, refused, rows.Err()
}

// ErrGrantGone is a rule id the chat does not hold.
var ErrGrantGone = errors.New("chatsvc: the chat holds no rule with this id")

// ChatGrants is chatID's rules, each as Settings spells a rule; none for a
// chat that is gone.
func (s *service) ChatGrants(ctx context.Context, chatID ChatID) ([]permissions.Rule, error) {
	rules, _, err := readGrants(ctx, s.store.Stmts(), chatID)
	return rules, err
}

// addGrant writes rule as one of chatID's, and answers it as written. A rule
// with no ID is written under a fresh one, the row's and the rule's, unless
// the chat holds the same rule already, which it answers instead. A rule with
// an ID replaces the chat's rule under it, so the id outlives the change.
func (s *service) addGrant(ctx context.Context, chatID ChatID, rule permissions.Rule) (permissions.Rule, error) {
	err := s.store.InTx(ctx, func(st stmts) error {
		if _, ok, err := getChat(ctx, st, chatID); err != nil || !ok {
			return cmp.Or(err, ErrChatGone)
		}
		held, _, err := readGrants(ctx, st, chatID)
		if err != nil {
			return err
		}
		if rule.ID == "" {
			for _, h := range held {
				if sameRule(h, rule) {
					rule = h
					return nil
				}
			}
			rule.ID = appdb.NewID()
		} else if !slices.ContainsFunc(held, func(h permissions.Rule) bool { return h.ID == rule.ID }) {
			return ErrGrantGone
		}
		b, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		_, err = st.Exec(ctx, stmtUpsertChatGrant, rule.ID, string(chatID), string(b), millis(normalizeTime(s.now())))
		return err
	})
	if err != nil {
		return permissions.Rule{}, err
	}
	return rule, nil
}

// RemoveChatGrant removes chatID's rule id, and answers the rules left:
// ErrChatGone for a chat that is gone, ErrGrantGone for an id it does not hold.
func (s *service) RemoveChatGrant(ctx context.Context, chatID ChatID, id string) ([]permissions.Rule, error) {
	var left []permissions.Rule
	err := s.store.InTx(ctx, func(st stmts) error {
		if _, ok, err := getChat(ctx, st, chatID); err != nil || !ok {
			return cmp.Or(err, ErrChatGone)
		}
		res, err := st.Exec(ctx, stmtDeleteChatGrant, string(chatID), id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return cmp.Or(err, ErrGrantGone)
		}
		left, _, err = readGrants(ctx, st, chatID)
		return err
	})
	return left, err
}

// sameRule is whether a and b are one rule but for their ids.
func sameRule(a, b permissions.Rule) bool {
	a.ID, b.ID = "", ""
	return a == b
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
