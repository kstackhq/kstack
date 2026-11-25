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

package settings

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// ModeSource is where a cluster's mode comes from.
type ModeSource string

const (
	// SourceEntry is the cluster's own row.
	SourceEntry ModeSource = "entry"
	// SourceDefault is the default mode.
	SourceDefault ModeSource = "default"
)

// ClusterModeState is one cluster's mode, as Settings shows it.
type ClusterModeState struct {
	ClusterID apimeta.ClusterID
	Mode      permissions.Mode
	Source    ModeSource
}

var (
	// ErrClusterGone is a mode or rule for a cluster that is gone or awaiting
	// deletion.
	ErrClusterGone = errors.New("settings: the cluster is gone")
	// ErrNoKubeContext is a mode or rule for a cluster with no kube-context,
	// which Settings does not list.
	ErrNoKubeContext = errors.New("settings: only a cluster with a kube-context has a mode or rule of its own")
	// ErrNoRule is a rule id the always rules do not hold.
	ErrNoRule = errors.New("settings: no rule has this id")
	// ErrNoGrant is an id no always folder grant has.
	ErrNoGrant = errors.New("settings: no folder grant has this id")
)

// RuleRefusal is why a rule cannot be written, in the user's words.
type RuleRefusal string

func (r RuleRefusal) Error() string { return string(r) }

// FolderGrant is one folder grant as stored: its folder, absolute and
// resolved, and whether it grants writes.
type FolderGrant struct {
	ID    string
	Path  string
	Write bool
}

// modeEntry is the approval mode: the default's row and each cluster's own.
// Read-only is the strictest: a mode Kstack cannot use refuses every cluster
// write.
var modeEntry = Entry[permissions.Mode]{
	Key:        "permissions.approval_mode",
	Default:    permissions.Ask,
	Strictest:  new(permissions.ReadOnly),
	PerCluster: true,
	Check:      checkMode,
}

func checkMode(mode permissions.Mode) error {
	switch mode {
	case permissions.ReadOnly, permissions.Ask, permissions.Auto:
		return nil
	}
	return fmt.Errorf("no approval mode is named %q", mode)
}

// ModeFor is cluster's mode and where it comes from: its own row, else the
// default row, else Ask. A read that fails is an error, which a decision reads
// as read-only.
func (s *Store) ModeFor(ctx context.Context, cluster apimeta.ClusterID) (ClusterModeState, error) {
	mode, scope, err := readSetting(ctx, s.stmts.Stmts(), modeEntry, cluster)
	if err != nil {
		return ClusterModeState{}, err
	}
	return modeState(cluster, mode, scope), nil
}

// modeState is cluster's mode, its own (SourceEntry) only when its own row
// holds it.
func modeState(cluster apimeta.ClusterID, mode permissions.Mode, scope settingScope) ClusterModeState {
	st := ClusterModeState{ClusterID: cluster, Mode: mode, Source: SourceDefault}
	if scope == scopeOwn {
		st.Source = SourceEntry
	}
	return st
}

// ClusterModes is the default mode, Ask when unset, and each of clusters'
// modes as ModeFor answers it, from one read of the modes, so every cluster
// that takes the default shows the same one.
func (s *Store) ClusterModes(ctx context.Context, clusters []apimeta.ClusterID) (permissions.Mode, []ClusterModeState, error) {
	stored, err := selectSettingScopes(ctx, s.stmts.Stmts(), modeEntry.Key)
	if err != nil {
		return "", nil, err
	}
	defaultMode, _ := modeEntry.valueFor(stored, "")
	states := make([]ClusterModeState, 0, len(clusters))
	for _, c := range clusters {
		mode, scope := modeEntry.valueFor(stored, c)
		states = append(states, modeState(c, mode, scope))
	}
	return defaultMode, states, nil
}

// SetDefaultMode sets the default mode.
func (s *Store) SetDefaultMode(ctx context.Context, mode permissions.Mode) error {
	return s.write(ctx, appdb.KeyPermissions, func(st stmts) error {
		return writeSetting(ctx, st, modeEntry, "", mode, s.now().UnixMilli())
	})
}

// SetMode sets cluster's own mode: ErrClusterGone for a cluster that is gone
// or awaiting deletion, ErrNoKubeContext for one with no kube-context.
func (s *Store) SetMode(ctx context.Context, cluster apimeta.ClusterID, mode permissions.Mode) error {
	return s.write(ctx, appdb.KeyPermissions, func(st stmts) error {
		if err := requireKubeContext(ctx, st, cluster); err != nil {
			return err
		}
		return writeSetting(ctx, st, modeEntry, cluster, mode, s.now().UnixMilli())
	})
}

// ClearMode puts cluster back on the default mode, writing NULL in its own
// row: ErrClusterGone for a cluster that is gone or awaiting deletion.
func (s *Store) ClearMode(ctx context.Context, cluster apimeta.ClusterID) error {
	return s.write(ctx, appdb.KeyPermissions, func(st stmts) error {
		if _, err := clusterHasKubeContext(ctx, st, cluster); err != nil {
			return err
		}
		return resetSetting(ctx, st, modeEntry, cluster, s.now().UnixMilli())
	})
}

// KubeRules is the always rules in the order written. A read that fails is an
// error, which a decision reads as permissions.Unread.
func (s *Store) KubeRules(ctx context.Context) ([]permissions.Rule, error) {
	return selectKubeRules(ctx, s.stmts.Stmts())
}

// AddKubeRule writes r as an always rule, under a fresh id when it has none,
// and answers it as written: a RuleRefusal for a rule Kstack cannot apply,
// ErrClusterGone or ErrNoKubeContext for a cluster as SetMode refuses it, and
// the rule held already, unwritten, when the rules hold the same one.
func (s *Store) AddKubeRule(ctx context.Context, r permissions.Rule) (permissions.Rule, error) {
	if err := CheckKubeRule(r); err != nil {
		return permissions.Rule{}, err
	}
	err := s.write(ctx, appdb.KeyPermissions, func(st stmts) error {
		if r.ClusterID != "" {
			if err := requireKubeContext(ctx, st, r.ClusterID); err != nil {
				return err
			}
		}
		held, err := selectKubeRules(ctx, st)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(held, r.Same); i >= 0 {
			r = held[i]
			return errUnchanged
		}
		if r.ID == "" {
			r.ID = appdb.NewID()
		}
		return insertKubeRule(ctx, st, r, s.now().UnixMilli())
	})
	if err != nil {
		return permissions.Rule{}, err
	}
	return r, nil
}

// RemoveKubeRule removes the always rule id: ErrNoRule when none has it.
func (s *Store) RemoveKubeRule(ctx context.Context, id string) error {
	return s.write(ctx, appdb.KeyPermissions, func(st stmts) error { return execOne(ctx, st, stmtDeleteKubeRule, ErrNoRule, id) })
}

// FolderGrants is the always folder grants in the order written, unchecked: a
// grant is checked where it is read. A read that fails is an error.
func (s *Store) FolderGrants(ctx context.Context) ([]FolderGrant, error) {
	return selectFolderGrants(ctx, s.stmts.Stmts())
}

// GrantFolder writes an always grant of path, which its caller has checked. A
// folder granted already keeps its grant's id and changes its access.
func (s *Store) GrantFolder(ctx context.Context, path string, write bool) error {
	return s.write(ctx, appdb.KeyPermissions, func(st stmts) error {
		return upsertFolderGrant(ctx, st, appdb.NewID(), path, write, s.now().UnixMilli())
	})
}

// RevokeFolder removes the always folder grant id: ErrNoGrant when none has
// it.
func (s *Store) RevokeFolder(ctx context.Context, id string) error {
	return s.write(ctx, appdb.KeyPermissions, func(st stmts) error { return execOne(ctx, st, stmtDeleteFolderGrant, ErrNoGrant, id) })
}

// ListenForPermissionChanges notifies after each write of the modes, the always rules or
// the always folder grants commits. Listen before the first read.
func (s *Store) ListenForPermissionChanges() ChangeNotifications {
	return s.db.ListenForChanges(appdb.KeyPermissions)
}

var validEffects = map[permissions.Effect]bool{permissions.Allow: true, permissions.Deny: true, permissions.AskFor: true}

// secretReadVerbs are the verbs a Secret read is classified with.
var secretReadVerbs = map[string]bool{"": true, "get": true, "list": true, "watch": true}

// CheckKubeRule is why r cannot be written, as a RuleRefusal, or nil: what
// kube_rules' CHECKs refuse, in the user's words. Every writer of the table
// checks a rule here first.
func CheckKubeRule(r permissions.Rule) error {
	if reason := ruleRefusal(r); reason != "" {
		return RuleRefusal(reason)
	}
	return nil
}

// ruleRefusal is why r cannot be written, or "".
func ruleRefusal(r permissions.Rule) string {
	switch {
	case r.ID == permissions.Refused.ID || r.ID == permissions.RefusedSecrets.ID:
		return "has the id Kstack keeps for its own rule"
	case !validEffects[r.Effect]:
		return "has an effect other than allow, deny or ask"
	case r.Class.Name() == "": // a class kube_rules stores
		return "names a class no rule decides: only 4 (cluster writes), 5 (destructive cluster writes) and 6 (Secret reads)"
	case r.Effect == permissions.Allow && r.Class == permissions.Destructive:
		return "allows a destructive write, which always asks"
	}
	if r.Class == permissions.SecretRead {
		if reason := secretRuleRefusal(r); reason != "" {
			return reason
		}
	}
	if r.Effect == permissions.Allow && r.ClusterID == "" {
		return "allows on every cluster, which only a deny or ask rule may"
	}
	return ""
}

// secretRuleRefusal is why a Secret read rule cannot be written, or "": it may
// name what a Secret read is classified with, and nothing else.
func secretRuleRefusal(r permissions.Rule) string {
	switch {
	case r.Inside:
		return "is a Secret read rule marked inside, which only a cluster write rule may be"
	case r.Namespace == permissions.ClusterScope:
		return "is a Secret read rule naming [cluster], though every Secret is in a namespace"
	case !secretReadVerbs[r.Verb]:
		return "is a Secret read rule naming a verb other than get, list or watch"
	case r.Group != "" && r.Group != "core":
		return "is a Secret read rule naming a group other than core"
	case r.Kind != "" && r.Kind != "secrets":
		return "is a Secret read rule naming a resource other than secrets"
	}
	return ""
}
