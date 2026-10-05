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
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/session"
)

// grantsFor is the rules that last for chatID's life, read on every decision
// and never cached, so one written while a command runs applies to its next
// write. A row that does not decode, or a read that fails, may have hidden a
// Deny, so it reads as permissions.Refused: the chat's cluster writes are
// refused until it is fixed.
func (s *service) grantsFor(ctx context.Context, chatID ChatID) []permissions.Rule {
	return grantsIn(ctx, s.store.Stmts(), chatID)
}

// grantsIn is grantsFor, read through st.
func grantsIn(ctx context.Context, st stmts, chatID ChatID) []permissions.Rule {
	rules, refused, err := readGrants(ctx, st, chatID)
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

// ErrGrantGone is a rule id the chat does not hold, or for RevokeFolder an id
// that is no always folder grant.
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
		var err error
		rule, err = s.putGrant(ctx, st, chatID, rule)
		return err
	})
	if err != nil {
		return permissions.Rule{}, err
	}
	return rule, nil
}

// putGrant is addGrant inside st.
func (s *service) putGrant(ctx context.Context, st stmts, chatID ChatID, rule permissions.Rule) (permissions.Rule, error) {
	if _, ok, err := getChat(ctx, st, chatID); err != nil || !ok {
		return permissions.Rule{}, cmp.Or(err, ErrChatGone)
	}
	held, _, err := readGrants(ctx, st, chatID)
	if err != nil {
		return permissions.Rule{}, err
	}
	if rule.ID == "" {
		for _, h := range held {
			if sameRule(h, rule) {
				return h, nil
			}
		}
		rule.ID = appdb.NewID()
	} else if !slices.ContainsFunc(held, func(h permissions.Rule) bool { return h.ID == rule.ID }) {
		return permissions.Rule{}, ErrGrantGone
	}
	b, err := json.Marshal(rule)
	if err != nil {
		return permissions.Rule{}, err
	}
	if _, err := st.Exec(ctx, stmtUpsertChatGrant, rule.ID, string(chatID), string(b), millis(normalizeTime(s.now()))); err != nil {
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

// FoldersFor is foldersFor, for a run with no turn of its own.
func (s *service) FoldersFor(ctx context.Context, chatID ChatID) []session.Folder {
	return s.foldersFor(ctx, chatID)
}

// foldersFor is the one builder of a session's folders: chatID's folder
// grants, then the always ones, less every folder that fails its check now.
// Each is read and checked live, so a grant written or revoked meanwhile
// applies to the next read. None where no sandbox confines a run: there a
// grant would open a folder to the file tools with nothing keeping the
// denied-always list out.
func (s *service) foldersFor(ctx context.Context, chatID ChatID) []session.Folder {
	return s.foldersFrom(ctx, chatID, s.folderRules(ctx, s.store.Stmts(), chatID))
}

// foldersFrom is foldersFor over rules already read.
func (s *service) foldersFrom(ctx context.Context, chatID ChatID, rules []permissions.Rule) []session.Folder {
	var folders []session.Folder
	for _, r := range rules {
		write := r.Class == permissions.WriteInside
		if err := s.security.CheckStoredFolder(ctx, r.Folder, write); err != nil {
			slog.Info("folder grant left out", "chat", chatID, "folder", r.Folder, "reason", err)
			continue
		}
		folders = append(folders, session.Folder{Path: r.Folder, Write: write})
	}
	return folders
}

// folderRules is the folder grants a run of chatID reads: the chat's, then the
// always ones, the chat's read through st. None without a sandbox. A held
// rules field holds no Allow, so it grants no folder.
func (s *service) folderRules(ctx context.Context, st stmts, chatID ChatID) []permissions.Rule {
	if !s.sandboxStatus.Available {
		return nil
	}
	var rules []permissions.Rule
	if chatID != "" {
		rules = grantsIn(ctx, st, chatID)
	}
	rules = append(rules, s.security.Rules()...)
	return slices.DeleteFunc(rules, func(r permissions.Rule) bool { return r.Folder == "" || r.Effect != permissions.Allow })
}

// FolderGrant is one folder grant as Settings and the composer draw it:
// Refused is why it fails its check now, so no run takes it; "" when it
// passes.
type FolderGrant struct {
	ID      string
	Path    string
	Write   bool
	Refused string
}

// FolderGrants is the always grants and, with a chat, the chat's own, each
// checked. While the rules field is held no always grant reaches a run
// (Rules holds no Allow then), so each is listed refused for that reason; it
// can be removed once the hold ends.
func (s *service) FolderGrants(ctx context.Context, chatID ChatID) (always, chat []FolderGrant) {
	if !s.sandboxStatus.Available {
		return nil, nil
	}
	always = s.checkedFolderGrants(ctx, s.security.Get().Rules)
	if s.security.Held(securityconfig.FieldRules) {
		for i := range always {
			always[i].Refused = RulesHeldReason
		}
	}
	if chatID != "" {
		chat = s.checkedFolderGrants(ctx, s.grantsFor(ctx, chatID))
	}
	return always, chat
}

// RulesHeldReason is why no always grant applies while the rules field is
// held, in the user's words; the wire gives a refused grant or revoke the same
// reason.
const RulesHeldReason = "The security settings file holds a rule Kstack cannot read: fix security.json, or discard what Kstack cannot read in Settings."

// checkedFolderGrants is the folder grants among rules, each checked.
func (s *service) checkedFolderGrants(ctx context.Context, rules []permissions.Rule) []FolderGrant {
	var grants []FolderGrant
	for _, r := range rules {
		if r.Folder == "" {
			continue
		}
		g := FolderGrant{ID: r.ID, Path: r.Folder, Write: r.Class == permissions.WriteInside}
		if err := s.security.CheckStoredFolder(ctx, g.Path, g.Write); err != nil {
			g.Refused = err.Error()
		}
		grants = append(grants, g)
	}
	return grants
}

// GrantFolder writes a folder grant, once it passes its check: in chatID's
// rows, or with no chat in the always rules. A grant of a folder already
// granted in the same place takes that grant's id, which replaces its mode.
// The lookup and the write are one transaction, or one update under the
// store's lock, so two grants of one folder at once leave one rule.
func (s *service) GrantFolder(ctx context.Context, chatID ChatID, path string, write bool) error {
	if err := s.security.CheckFolder(ctx, path, write); err != nil {
		return err
	}
	rule := permissions.Rule{Effect: permissions.Allow, Class: permissions.ReadInside, Folder: path}
	if write {
		rule.Class = permissions.WriteInside
	}
	grantsPath := func(r permissions.Rule) bool { return r.Folder == path }
	if chatID == "" {
		rule.ID = appdb.NewID()
		return s.security.PutRule(rule, grantsPath)
	}
	return s.store.InTx(ctx, func(st stmts) error {
		held, _, err := readGrants(ctx, st, chatID)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(held, grantsPath); i >= 0 {
			rule.ID = held[i].ID
		}
		_, err = s.putGrant(ctx, st, chatID, rule)
		return err
	})
}

// RevokeFolder removes an always folder grant by id.
func (s *service) RevokeFolder(id string) error {
	for _, r := range s.security.Get().Rules {
		if r.ID == id && r.Folder != "" {
			return s.security.RemoveRule(id)
		}
	}
	return ErrGrantGone
}

// sessionFor is a chat's session: its policy read live on every write, so a
// mode or rule changed in Settings applies to the next one, a running turn's
// and a running subagent's included. The chat's grants come before the always
// rules. Its network is the chat's switch, read at each command the same way,
// else the turn's toggle. Its folders are read live too, and none applies
// outside the sandbox.
func (s *service) sessionFor(chatID ChatID, outside, networkThisTurn bool) session.Session {
	folders := func(ctx context.Context) []session.Folder { return s.foldersFor(ctx, chatID) }
	if outside {
		folders = func(context.Context) []session.Folder { return nil }
	}
	return session.Session{
		Kind:    session.Chat,
		Outside: outside,
		Folders: folders,
		Policy: func(ctx context.Context, kubeContext string) permissions.Policy {
			return permissions.Policy{
				Mode:  s.security.ModeFor(kubeContext).Mode,
				Rules: append(s.grantsFor(ctx, chatID), s.security.Rules()...),
			}
		},
		Network: func(ctx context.Context) session.Network {
			switch {
			case s.networkEnabled(ctx, chatID):
				return session.NetworkChat
			case networkThisTurn:
				return session.NetworkTurn
			}
			return session.NoNetwork
		},
	}
}

// networkEnabled is the chat's network switch as stored now. A read that
// fails, or a chat that is gone, answers false, so a broken read gives no
// network.
func (s *service) networkEnabled(ctx context.Context, chatID ChatID) bool {
	c, ok, err := getChat(ctx, s.store.Stmts(), chatID)
	if err != nil {
		slog.Warn("chat network switch not read", "chat", chatID, "err", err)
		return false
	}
	return ok && c.NetworkEnabled
}
