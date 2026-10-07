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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

func openFile(t *testing.T, body string) (*Store, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "settings.json")
	if body != "" {
		require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
	}
	s, err := Open(file)
	require.NoError(t, err)
	return s, file
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	return string(data)
}

func TestAContextNothingNamesTakesTheDefaultMode(t *testing.T) {
	s, _ := openFile(t, "")
	for _, context := range []string{"dev", "prod-eu"} {
		assert.Equal(t, ContextModeState{Context: context, Mode: permissions.Ask, Source: SourceDefault}, s.ModeFor(context))
	}

	require.NoError(t, s.SetMode("prod-eu", permissions.ReadOnly))
	assert.Equal(t, ContextModeState{Context: "prod-eu", Mode: permissions.ReadOnly, Source: SourceEntry, Pattern: "prod-eu", Own: true}, s.ModeFor("prod-eu"))
	assert.Equal(t, ContextModeState{Context: "prod-us", Mode: permissions.Ask, Source: SourceDefault}, s.ModeFor("prod-us"))
}

func TestModesMatchInOrder(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"defaultMode": "auto", "modes": [
		{"context": "dev-*", "mode": "read-only"},
		{"context": "dev-eks", "mode": "ask"},
		{"context": "*", "mode": "ask"}
	]}}`)
	assert.Equal(t, permissions.ReadOnly, s.ModeFor("dev-eks").Mode, "the first match wins")
	assert.Equal(t, ContextModeState{Context: "prod-eu", Mode: permissions.Ask, Source: SourceEntry, Pattern: "*"}, s.ModeFor("prod-eu"),
		"Settings names the pattern that decided it")

	s, _ = openFile(t, `{"permissions": {"defaultMode": "auto"}}`)
	assert.Equal(t, permissions.Auto, s.ModeFor("staging").Mode, "the default mode answers what nothing else does")
	assert.Equal(t, permissions.Auto, s.DefaultMode())
}

func TestABadModeIsReadOnly(t *testing.T) {
	s, file := openFile(t, `{"permissions": {"defaultMode": "readonly"}}`)
	assert.Equal(t, ContextModeState{Context: "dev", Mode: permissions.ReadOnly, Source: SourceDefault}, s.ModeFor("dev"))
	assert.Equal(t, []Refusal{{Field: FieldDefaultMode, Value: `"readonly"`, Reason: "is not read-only, ask or auto"}}, s.Refused())
	require.NoError(t, s.SetDefaultMode(permissions.Auto), "setting the default mode is its fix")
	assert.Equal(t, permissions.Auto, s.ModeFor("dev").Mode)
	assert.JSONEq(t, `{"permissions": {"defaultMode": "auto"}, "schemaVersion": 1}`, readFile(t, file))

	s, file = openFile(t, `{"permissions": {"modes": [{"context": "dev", "mode": "auto"}, {"context": "x", "mode": "yolo"}, {"context": "", "mode": "ask"}]}}`)
	for _, context := range []string{"dev", "staging"} {
		assert.Equal(t, ContextModeState{Context: context, Mode: permissions.ReadOnly, Source: SourceRefused}, s.ModeFor(context))
	}
	assert.Len(t, s.Refused(), 2)
	assert.Equal(t, FieldModes, s.Refused()[0].Field)
	require.NoError(t, s.SetDefaultMode(permissions.Auto), "a write that does not touch the field")
	assert.JSONEq(t, `{"permissions": {"defaultMode": "auto", "modes": [{"context": "dev", "mode": "auto"}, {"context": "x", "mode": "yolo"}, {"context": "", "mode": "ask"}]}, "schemaVersion": 1}`,
		readFile(t, file), "keeps the file's raw value")
}

func TestABadRuleRefusesEveryClusterWrite(t *testing.T) {
	deny := `{"id": "d", "effect": "deny", "class": 4, "context": "prod-eu"}`
	allow := `{"id": "a", "effect": "allow", "class": 4, "context": "dev"}`
	for name, bad := range map[string]string{
		"unknown class":    `{"id": "b", "effect": "deny", "class": 9}`,
		"unknown effect":   `{"id": "b", "effect": "maybe", "class": 4}`,
		"wrong type":       `{"id": "b", "effect": "deny", "class": "four"}`,
		"repeated id":      `{"id": "d", "effect": "deny", "class": 4}`,
		"the id refused":   `{"id": "refused", "effect": "deny", "class": 4}`,
		"empty id":         `{"effect": "deny", "class": 4}`,
		"class 1":          `{"id": "b", "effect": "deny", "class": 1}`,
		"class 6 by verb":  `{"id": "b", "effect": "deny", "class": 6, "verb": "delete"}`,
		"the id kept":      `{"id": "refused-secrets", "effect": "deny", "class": 6}`,
		"allow of class 5": `{"id": "b", "effect": "allow", "class": 5}`,
		"misspelled field": `{"id": "b", "effect": "allow", "class": 4, "namepsace": "team-a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := openFile(t, `{"permissions": {"rules": [`+deny+`, `+allow+`, `+bad+`]}}`)
			assert.True(t, s.Held(FieldRules))
			ids := []string{}
			for _, r := range s.Rules() {
				ids = append(ids, r.ID)
			}
			assert.Equal(t, []string{"d", "refused", "refused-secrets"}, ids, "the Deny, no Allow, and the class 4 and 6 Denies")
			assert.Equal(t, permissions.Refused, s.Rules()[1])
			assert.Len(t, s.Get().Permissions.Rules, 2, "Get still holds the Allow")
			require.Len(t, s.Refused(), 1)
			assert.Equal(t, FieldRules, s.Refused()[0].Field)
		})
	}

	s, _ := openFile(t, `{"permissions": {"rules": [`+allow+`]}}`)
	for name, bad := range map[string]permissions.Rule{
		"repeated id":      {ID: "a", Effect: permissions.Deny, Class: permissions.UpstreamWrite},
		"the id refused":   {ID: "refused", Effect: permissions.Deny, Class: permissions.UpstreamWrite},
		"class 1":          {ID: "b", Effect: permissions.Deny, Class: permissions.ReadInside},
		"allow of class 5": {ID: "b", Effect: permissions.Allow, Class: permissions.Destructive},
	} {
		err := s.AddRule(bad)
		var r Refusal
		require.ErrorAs(t, err, &r, name)
		assert.Equal(t, FieldRules, r.Field, name)
		assert.NotEmpty(t, r.Reason, name)
	}
	assert.Len(t, s.Get().Permissions.Rules, 1, "a refused add writes nothing")
}

// The permissions group holds each field on its own: a bad rule leaves the
// modes beside it readable.
func TestABadRuleLeavesTheModesReadable(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"modes": [{"context": "dev", "mode": "auto"}],
		"rules": [{"id": "b", "effect": "deny", "class": 9}]}}`)
	assert.True(t, s.Held(FieldRules))
	assert.False(t, s.Held(FieldModes))
	assert.Equal(t, ContextModeState{Context: "dev", Mode: permissions.Auto, Source: SourceEntry, Pattern: "dev", Own: true}, s.ModeFor("dev"))
	require.Len(t, s.Refused(), 1)
	assert.Equal(t, "permissions.rules", s.Refused()[0].Field)
}

func TestAHeldListRefusesItsEdits(t *testing.T) {
	s, file := openFile(t, `{"permissions": {"modes": [{"context": "dev", "mode": "auto"}, {"context": "x", "mode": "yolo"}],
		"rules": [{"id": "a", "effect": "allow", "class": 4}, {"id": "b", "effect": "deny", "class": 9}]}}`)
	before := readFile(t, file)
	rule := permissions.Rule{ID: "c", Effect: permissions.Deny, Class: permissions.UpstreamWrite}
	for name, err := range map[string]error{
		"set":    s.SetMode("dev", permissions.Ask),
		"clear":  s.ClearMode("dev"),
		"add":    s.AddRule(rule),
		"remove": s.RemoveRule("a"),
	} {
		assert.ErrorIs(t, err, ErrHeld, name)
	}
	assert.Equal(t, before, readFile(t, file), "a refused edit writes nothing")

	require.NoError(t, s.DiscardRefused(FieldModes))
	assert.False(t, s.Held(FieldModes))
	assert.Equal(t, ContextModeState{Context: "dev", Mode: permissions.Auto, Source: SourceEntry, Pattern: "dev", Own: true}, s.ModeFor("dev"))

	require.NoError(t, s.DiscardRefused(FieldRules))
	assert.Equal(t, []permissions.Rule{{ID: "a", Effect: permissions.Allow, Class: permissions.UpstreamWrite}}, s.Rules())
	assert.JSONEq(t, `{"permissions": {"modes": [{"context": "dev", "mode": "auto"}], "rules": [{"id": "a", "effect": "allow", "class": 4}]}, "schemaVersion": 1}`,
		readFile(t, file))
	require.NoError(t, s.AddRule(rule), "the edits work once the hold ends")

	assert.ErrorIs(t, s.DiscardRefused(FieldRules), ErrNotHeld, "a field that is not held")
	assert.ErrorIs(t, s.DiscardRefused(FieldDefaultMode), ErrNotHeld, "the default mode is fixed by setting it")
	assert.ErrorIs(t, s.RemoveRule("nope"), ErrNoRule)
}

func TestAModeSetInSettingsWinsOverAPattern(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"modes": [{"context": "*", "mode": "auto"}]}}`)
	require.NoError(t, s.SetMode("prod-eu", permissions.Ask))
	assert.Equal(t, permissions.Ask, s.ModeFor("prod-eu").Mode)
	assert.Equal(t, permissions.Auto, s.ModeFor("dev").Mode)

	require.NoError(t, s.SetMode("prod-eu", permissions.ReadOnly))
	assert.Equal(t, []ContextMode{{Context: "prod-eu", Mode: permissions.ReadOnly}, {Context: "*", Mode: permissions.Auto}}, s.Get().Permissions.Modes,
		"setting it again replaces the entry")

	require.NoError(t, s.SetMode("dev*", permissions.Ask))
	assert.Equal(t, ContextMode{Context: `dev\*`, Mode: permissions.Ask}, s.Get().Permissions.Modes[0], "the entry is the context's literal")
	assert.Equal(t, permissions.Auto, s.ModeFor("dev-eks").Mode, "and names that context alone")

	require.NoError(t, s.ClearMode("prod-eu"))
	assert.Equal(t, permissions.Auto, s.ModeFor("prod-eu").Mode)
	assert.Error(t, s.SetMode("dev", "yolo"))
}

func TestAContextSaysWhetherItsEntryIsItsOwn(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"modes": [{"context": "dev-eks", "mode": "auto"}, {"context": "*", "mode": "ask"}]}}`)
	assert.True(t, s.ModeFor("dev-eks").Own, "an entry naming the context alone")
	assert.False(t, s.ModeFor("staging").Own, "a pattern that matches it")

	require.NoError(t, s.SetMode("prod-eu", permissions.Ask))
	assert.True(t, s.ModeFor("prod-eu").Own)
}

func TestHeldRulesGrantNoFolder(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"rules": [{"id": "r", "effect": "allow", "class": 1, "folder": "/Users/me/code"},
		{"id": "b", "effect": "deny", "class": 9}]}}`)
	require.True(t, s.Held(FieldRules))
	assert.Equal(t, []permissions.Rule{permissions.Refused, permissions.RefusedSecrets}, s.Rules(), "a file Kstack cannot read grants no folder")
}

func TestPutRuleKeepsItsPlace(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"rules": [{"id": "a", "effect": "deny", "class": 4}, {"id": "b", "effect": "allow", "class": 4, "context": "dev"},
		{"id": "c", "effect": "deny", "class": 5}]}}`)
	byID := func(id string) func(permissions.Rule) bool {
		return func(r permissions.Rule) bool { return r.ID == id }
	}
	b := permissions.Rule{ID: "fresh", Effect: permissions.Deny, Class: permissions.UpstreamWrite, Context: "prod"}
	require.NoError(t, s.PutRule(b, byID("b")))
	ids := []string{}
	for _, r := range s.Rules() {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"a", "b", "c"}, ids, "the rule stays where it stood, under its id")
	b.ID = "b"
	assert.Equal(t, b, s.Rules()[1])

	d := permissions.Rule{ID: "d", Effect: permissions.Deny, Class: permissions.UpstreamWrite}
	require.NoError(t, s.PutRule(d, byID("nope")))
	assert.Equal(t, d, s.Rules()[3], "appended when none is replaced")
	var r Refusal
	require.ErrorAs(t, s.PutRule(permissions.Rule{ID: "x", Effect: permissions.Allow, Class: permissions.Destructive}, byID("a")), &r,
		"the replacement is checked as any rule is")
}

func TestAClassSixRuleIsRead(t *testing.T) {
	kept := `{"id": "k", "effect": "allow", "class": 6, "context": "dev-*", "namespace": "team-a"}`
	s, _ := openFile(t, `{"permissions": {"rules": [`+kept+`, {"id": "c", "effect": "deny", "class": 6, "verb": "list", "group": "core", "kind": "secrets"}]}}`)
	assert.False(t, s.Held(FieldRules))
	assert.Len(t, s.Rules(), 2)

	for name, c := range map[string]struct {
		rule   permissions.Rule
		reason string
	}{
		"a folder":       {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Folder: "/Users/me"}, "names a folder, which only a folder grant does"},
		"inside":         {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Namespace: "team-a", Inside: true}, "is a Secret read rule marked inside, which only a cluster write rule may be"},
		"cluster-scoped": {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Namespace: permissions.ClusterScope}, "is a Secret read rule naming [cluster], though every Secret is in a namespace"},
		"a write verb":   {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Verb: "delete"}, "is a Secret read rule naming a verb other than get, list or watch"},
		"another group":  {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Group: "apps"}, "is a Secret read rule naming a group other than core"},
		"another kind":   {permissions.Rule{ID: "b", Effect: permissions.Allow, Class: permissions.SecretRead, Kind: "configmaps"}, "is a Secret read rule naming a resource other than secrets"},
		"the id refused": {permissions.Rule{ID: "refused-secrets", Effect: permissions.Deny, Class: permissions.SecretRead}, "has the id Kstack keeps for its own rule"},
	} {
		var r Refusal
		require.ErrorAs(t, s.AddRule(c.rule), &r, name)
		assert.Equal(t, c.reason, r.Reason, name)
	}
}

func TestHeldRulesKeepSecretDataRedacted(t *testing.T) {
	s, _ := openFile(t, `{"permissions": {"rules": [{"id": "a", "effect": "allow", "class": 6}, {"id": "b", "effect": "deny", "class": 9}]}}`)
	require.True(t, s.Held(FieldRules))
	assert.Equal(t, []permissions.Rule{permissions.Refused, permissions.RefusedSecrets}, s.Rules())

	read := permissions.Action{Class: permissions.SecretRead, Context: "dev", Namespace: "team-a", Verb: "get", Group: "core", Kind: "secrets"}
	v, _ := permissions.Policy{Mode: permissions.Auto, Rules: s.Rules()}.Authorize(read)
	assert.Equal(t, permissions.Refuse, v)
}
