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
// that entry names this context alone, which is what Clear removes.
type ContextModeState struct {
	Context string
	Mode    permissions.Mode
	Source  ModeSource
	Pattern string
	Own     bool
}

// The keys of the fields the permission readers answer the strictest state of
// while the store holds them.
const (
	FieldDefaultMode = "defaultMode"
	FieldModes       = "modes"
	FieldRules       = "rules"
)

// view is one reading of the settings and the fields the store holds, so a
// decision reads both once.
type view struct {
	Settings
	held map[string]bool
}

func (s *Store) view() view {
	v, held := s.snapshot()
	return view{v, held}
}

// ModeFor is context's mode and where it comes from: read-only while modes is
// held, else the first Modes entry matching it, else DefaultMode.
func (s *Store) ModeFor(context string) (permissions.Mode, ModeSource) {
	st := s.view().modeFor(context)
	return st.Mode, st.Source
}

// ContextModes is each context's effective mode, for Settings.
func (s *Store) ContextModes(contexts []string) []ContextModeState {
	v := s.view()
	out := make([]ContextModeState, len(contexts))
	for i, c := range contexts {
		out[i] = v.modeFor(c)
	}
	return out
}

// DefaultMode is the mode of a context nothing else names: Ask when unset.
func (s *Store) DefaultMode() permissions.Mode {
	return s.view().defaultMode()
}

// Rules is the always rules as Decide reads them: while rules is held, the
// rules that passed less every Allow, plus permissions.Refused, which is never
// written to the file and whose id the rules check refuses there.
func (s *Store) Rules() []permissions.Rule {
	return s.view().rules()
}

func (v view) defaultMode() permissions.Mode {
	return cmp.Or(v.DefaultMode, permissions.Ask)
}

func (v view) modeFor(context string) ContextModeState {
	st := ContextModeState{Context: context}
	switch i := slices.IndexFunc(v.Modes, func(m ContextMode) bool { return permissions.Match(m.Context, context) }); {
	case v.held[FieldModes]:
		st.Mode, st.Source = permissions.ReadOnly, SourceRefused
	case i >= 0:
		st.Mode, st.Source, st.Pattern = v.Modes[i].Mode, SourceEntry, v.Modes[i].Context
		st.Own = st.Pattern == permissions.Literal(context)
	default:
		st.Mode, st.Source = v.defaultMode(), SourceDefault
	}
	return st
}

func (v view) rules() []permissions.Rule {
	if !v.held[FieldRules] {
		return v.Rules
	}
	rules := slices.DeleteFunc(v.Rules, func(r permissions.Rule) bool { return r.Effect == permissions.Allow })
	return append(rules, permissions.Refused)
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

// providerClasses is the classes each provider's rules may name. Classes 1
// and 2 are allowed before any rule, so no rule names them.
var providerClasses = map[permissions.Provider][]permissions.Class{
	permissions.Kubernetes: {permissions.UpstreamWrite, permissions.Destructive, permissions.SecretRead},
	permissions.Net:        {permissions.NewHost},
	permissions.Path:       {},
}

var validEffects = map[permissions.Effect]bool{permissions.Allow: true, permissions.Deny: true, permissions.AskFor: true}

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
	classes, knownProvider := providerClasses[r.Provider]
	switch {
	case r.ID == "":
		return "has no id"
	case r.ID == permissions.Refused.ID:
		return "has the id Kstack keeps for its own rule"
	case seen[r.ID]:
		return "repeats another rule's id"
	case !validEffects[r.Effect]:
		return "has an effect other than allow, deny or ask"
	case !knownProvider:
		return "has a provider other than k8s, net or path"
	case !slices.Contains(classes, r.Class):
		return "names a class its provider does not have"
	case r.Effect == permissions.Allow && r.Class == permissions.Destructive:
		return "allows a destructive write, which always asks"
	case r.Provider != permissions.Kubernetes && (r.Verb != "" || r.Group != "" || r.Kind != ""):
		return "names a verb, group or kind on a provider other than k8s"
	case !scopeFits(r.Provider, r.Scope):
		return "names a scope its provider does not have"
	}
	return ""
}

// scopeFits is whether s sets only the fields p's actions have.
func scopeFits(p permissions.Provider, s permissions.Scope) bool {
	switch p {
	case permissions.Kubernetes:
		return s.Host == "" && s.Folder == ""
	case permissions.Net:
		return s.Context == "" && s.Namespace == "" && s.Folder == ""
	}
	return s.Context == "" && s.Namespace == "" && s.Host == ""
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
