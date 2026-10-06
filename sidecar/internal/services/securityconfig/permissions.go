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

package securityconfig

import (
	"cmp"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// ContextMode is the mode for the contexts a pattern matches.
type ContextMode struct {
	Context string           `json:"context"`
	Mode    permissions.Mode `json:"mode"`
}

// ModeSource is where a context's mode comes from.
type ModeSource string

const (
	// SourceRefused is every context read-only while the file's modes are held.
	SourceRefused ModeSource = "refused"
	// SourceEntry is a Modes entry whose pattern matches.
	SourceEntry ModeSource = "entry"
	// SourceDefault is DefaultMode.
	SourceDefault ModeSource = "default"
)

// ContextModeState is one context's effective mode, as Settings shows it.
// Pattern is the Modes entry that decided it, for SourceEntry, and Own whether
// that entry names this context alone, which is what ClearMode removes.
type ContextModeState struct {
	Context string
	Mode    permissions.Mode
	Source  ModeSource
	Pattern string
	Own     bool
}

// The keys of the fields that answer their strictest state while the store
// holds them.
const (
	FieldDefaultMode = "defaultMode"
	FieldModes       = "modes"
	FieldRules       = "rules"
)

// ModeFor is context's mode and where it comes from: read-only while modes is
// held, else the first Modes entry matching it, else DefaultMode.
func (s *Store) ModeFor(context string) ContextModeState {
	st := ContextModeState{Context: context}
	if s.Held(FieldModes) {
		st.Mode, st.Source = permissions.ReadOnly, SourceRefused
		return st
	}
	v := s.Get()
	for _, m := range v.Modes {
		if permissions.Match(m.Context, context) {
			st.Mode, st.Source, st.Pattern = m.Mode, SourceEntry, m.Context
			st.Own = m.Context == permissions.Literal(context)
			return st
		}
	}
	st.Mode, st.Source = defaultMode(v), SourceDefault
	return st
}

// DefaultMode is the mode of a context nothing else names: Ask when unset.
func (s *Store) DefaultMode() permissions.Mode {
	return defaultMode(s.Get())
}

func defaultMode(v Settings) permissions.Mode {
	return cmp.Or(v.DefaultMode, permissions.Ask)
}

// Rules is the always rules as a decision reads them. While rules is held,
// the rules that passed less every Allow, plus permissions.Refused and
// permissions.RefusedSecrets, which are never written to the file and whose
// ids the rules check refuses there.
func (s *Store) Rules() []permissions.Rule {
	rules := s.Get().Rules
	if !s.Held(FieldRules) {
		return rules
	}
	rules = slices.DeleteFunc(rules, func(r permissions.Rule) bool { return r.Effect == permissions.Allow })
	return append(rules, permissions.Refused, permissions.RefusedSecrets)
}

// SetDefaultMode sets the default mode, which also fixes a refused one.
func (s *Store) SetDefaultMode(mode permissions.Mode) error {
	return s.Update(func(v *Settings) error {
		v.DefaultMode = mode
		return nil
	}, FieldDefaultMode)
}

// SetMode sets context's own mode: an entry that names it alone, first in
// Modes so no pattern decides it, in place of one already there.
func (s *Store) SetMode(context string, mode permissions.Mode) error {
	entry := ContextMode{Context: permissions.Literal(context), Mode: mode}
	return s.Update(func(v *Settings) error {
		v.Modes = append([]ContextMode{entry}, withoutEntry(v.Modes, entry.Context)...)
		return nil
	})
}

// ClearMode removes context's own mode.
func (s *Store) ClearMode(context string) error {
	return s.Update(func(v *Settings) error {
		v.Modes = withoutEntry(v.Modes, permissions.Literal(context))
		return nil
	})
}

func withoutEntry(modes []ContextMode, pattern string) []ContextMode {
	return slices.DeleteFunc(slices.Clone(modes), func(m ContextMode) bool { return m.Context == pattern })
}

// ErrNoRule is a rule id the settings do not hold.
var ErrNoRule = errors.New("securityconfig: no rule has this id")

// AddRule adds an always rule, which the rules check reads first.
func (s *Store) AddRule(r permissions.Rule) error {
	return s.Update(func(v *Settings) error {
		v.Rules = append(v.Rules, r)
		return nil
	})
}

// RemoveRule removes the always rule with id.
func (s *Store) RemoveRule(id string) error {
	return s.Update(func(v *Settings) error {
		i := slices.IndexFunc(v.Rules, func(r permissions.Rule) bool { return r.ID == id })
		if i < 0 {
			return ErrNoRule
		}
		v.Rules = slices.Delete(v.Rules, i, i+1)
		return nil
	})
}

// PutRule swaps the first always rule replaces passes for r, where it stands
// and under its id, else appends r. One update, so a lookup and its write
// cannot interleave with another's.
func (s *Store) PutRule(r permissions.Rule, replaces func(permissions.Rule) bool) error {
	return s.Update(func(v *Settings) error {
		i := slices.IndexFunc(v.Rules, replaces)
		if i < 0 {
			v.Rules = append(v.Rules, r)
			return nil
		}
		r.ID = v.Rules[i].ID
		v.Rules[i] = r
		return nil
	})
}

// ErrNotHeld is a discard of a field the store does not hold.
var ErrNotHeld = errors.New("securityconfig: this setting holds nothing Kstack cannot read")

// DiscardRefused writes modes or rules as it stands, the elements that passed,
// which ends the hold and drops what could not be read.
func (s *Store) DiscardRefused(field string) error {
	if (field != FieldModes && field != FieldRules) || !s.Held(field) {
		return ErrNotHeld
	}
	return s.Update(func(*Settings) error { return nil }, field)
}

var validModes = map[permissions.Mode]bool{permissions.ReadOnly: true, permissions.Ask: true, permissions.Auto: true}

func checkDefaultMode(v *Settings) []Refusal {
	if v.DefaultMode == "" || validModes[v.DefaultMode] {
		return nil
	}
	r := Refusal{Field: FieldDefaultMode, Value: jsonOf(v.DefaultMode), Reason: "is not read-only, ask or auto"}
	v.DefaultMode = ""
	return []Refusal{r}
}

func checkModes(v *Settings) []Refusal {
	var refused []Refusal
	v.Modes = slices.DeleteFunc(v.Modes, func(m ContextMode) bool {
		reason := ""
		switch {
		case m.Context == "":
			reason = "names no context"
		case !validModes[m.Mode]:
			reason = "has a mode other than read-only, ask or auto"
		default:
			return false
		}
		refused = append(refused, Refusal{Field: FieldModes, Value: jsonOf(m), Reason: reason})
		return true
	})
	return refused
}

var validEffects = map[permissions.Effect]bool{permissions.Allow: true, permissions.Deny: true, permissions.AskFor: true}

// ruleClasses are the classes a rule may name: a folder grant's, the ones a
// cluster write is classified as, and a Secret read's. Nothing decides a rule
// of another class.
var ruleClasses = map[permissions.Class]bool{
	permissions.ReadInside: true, permissions.WriteInside: true,
	permissions.UpstreamWrite: true, permissions.Destructive: true,
	permissions.SecretRead: true,
}

// secretReadVerbs are the verbs a Secret read is classified with.
var secretReadVerbs = map[string]bool{"": true, "get": true, "list": true, "watch": true}

func checkRules(v *Settings) []Refusal {
	var refused []Refusal
	seen := map[string]bool{}
	v.Rules = slices.DeleteFunc(v.Rules, func(r permissions.Rule) bool {
		reason := ruleRefusal(r, seen)
		seen[r.ID] = true
		if reason == "" {
			return false
		}
		refused = append(refused, Refusal{Field: FieldRules, Value: jsonOf(r), Reason: reason})
		return true
	})
	return refused
}

// ruleRefusal is why r cannot be read, or "".
func ruleRefusal(r permissions.Rule, seen map[string]bool) string {
	switch {
	case r.ID == "":
		return "has no id"
	case r.ID == permissions.Refused.ID || r.ID == permissions.RefusedSecrets.ID:
		return "has the id Kstack keeps for its own rule"
	case seen[r.ID]:
		return "repeats another rule's id"
	case !validEffects[r.Effect]:
		return "has an effect other than allow, deny or ask"
	case !ruleClasses[r.Class]:
		return "names a class no rule decides: only 1 (folder reads), 2 (folder reads and writes), " +
			"4 (cluster writes), 5 (destructive cluster writes) and 6 (Secret reads)"
	case r.Class == permissions.ReadInside || r.Class == permissions.WriteInside:
		return folderRuleRefusal(r)
	case r.Folder != "":
		return "names a folder, which only a folder grant does"
	case r.Class == permissions.SecretRead:
		return secretRuleRefusal(r)
	case r.Effect == permissions.Allow && r.Class == permissions.Destructive:
		return "allows a destructive write, which always asks"
	}
	return ""
}

// secretRuleRefusal is why a Secret read rule cannot be read, or "": it may
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

// folderRuleRefusal is why a folder grant cannot be read, or "", by its value
// alone: what needs the disk is CheckFolder's, run whenever a grant is read.
func folderRuleRefusal(r permissions.Rule) string {
	switch {
	case r.Effect != permissions.Allow:
		return "is a folder rule that does not allow, which no folder rule may"
	case r.Folder == "":
		return "is a folder rule that names no folder"
	case !filepath.IsAbs(r.Folder) || filepath.Clean(r.Folder) != r.Folder:
		return "names a folder that is not an absolute, clean path"
	case r.Folder == string(filepath.Separator):
		return "names the root, which cannot be granted"
	case r.Context != "" || r.Namespace != "" || r.Verb != "" || r.Group != "" || r.Kind != "":
		return "is a folder rule that names a cluster field"
	}
	return ""
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
