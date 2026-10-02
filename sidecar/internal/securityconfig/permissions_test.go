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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// openFile opens a store over a file holding body; "" is no file.
func openFile(t *testing.T, body string) (*Store, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "security.json")
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
		mode, source := s.ModeFor(context)
		assert.Equal(t, permissions.Ask, mode, context)
		assert.Equal(t, SourceDefault, source, context)
	}

	require.NoError(t, s.SetMode("prod-eu", permissions.ReadOnly))
	got := s.ContextModes([]string{"prod-eu", "prod-us"})
	assert.Equal(t, []ContextModeState{
		{Context: "prod-eu", Mode: permissions.ReadOnly, Source: SourceEntry, Pattern: "prod-eu", Own: true},
		{Context: "prod-us", Mode: permissions.Ask, Source: SourceDefault},
	}, got)
}

func TestModesMatchInOrder(t *testing.T) {
	s, _ := openFile(t, `{"defaultMode": "auto", "modes": [
		{"context": "dev-*", "mode": "read-only"},
		{"context": "dev-eks", "mode": "ask"},
		{"context": "*", "mode": "ask"}
	]}`)
	mode, _ := s.ModeFor("dev-eks")
	assert.Equal(t, permissions.ReadOnly, mode, "the first match wins")
	mode, source := s.ModeFor("staging")
	assert.Equal(t, permissions.Ask, mode)
	assert.Equal(t, SourceEntry, source)
	got := s.ContextModes([]string{"prod-eu"})
	assert.Equal(t, ContextModeState{Context: "prod-eu", Mode: permissions.Ask, Source: SourceEntry, Pattern: "*"}, got[0],
		"Settings names the pattern that decided it")

	s, _ = openFile(t, `{"defaultMode": "auto"}`)
	mode, _ = s.ModeFor("staging")
	assert.Equal(t, permissions.Auto, mode, "the default mode answers what nothing else does")
}

func TestABadModeIsReadOnly(t *testing.T) {
	s, file := openFile(t, `{"defaultMode": "readonly"}`)
	mode, source := s.ModeFor("dev")
	assert.Equal(t, permissions.ReadOnly, mode)
	assert.Equal(t, SourceDefault, source)
	assert.Equal(t, []Refusal{{Field: "defaultMode", Value: `"readonly"`, Reason: "is not read-only, ask or auto"}}, s.Refused())
	require.NoError(t, s.SetDefaultMode(permissions.Auto), "setting the default mode is its fix")
	mode, _ = s.ModeFor("dev")
	assert.Equal(t, permissions.Auto, mode)
	assert.JSONEq(t, `{"defaultMode": "auto", "schemaVersion": 1}`, readFile(t, file))

	s, file = openFile(t, `{"modes": [{"context": "dev", "mode": "auto"}, {"context": "x", "mode": "yolo"}, {"context": "", "mode": "ask"}]}`)
	for _, context := range []string{"dev", "staging"} {
		mode, source = s.ModeFor(context)
		assert.Equal(t, permissions.ReadOnly, mode, context)
		assert.Equal(t, SourceRefused, source, context)
	}
	assert.Len(t, s.Refused(), 2)
	assert.Equal(t, "modes", s.Refused()[0].Field)
	require.NoError(t, s.SetDefaultMode(permissions.Auto), "a write that does not touch the field")
	assert.JSONEq(t, `{"defaultMode": "auto", "modes": [{"context": "dev", "mode": "auto"}, {"context": "x", "mode": "yolo"}, {"context": "", "mode": "ask"}], "schemaVersion": 1}`,
		readFile(t, file), "keeps the file's raw value")
}

func TestABadRuleRefusesEveryClusterWrite(t *testing.T) {
	deny := `{"id": "d", "effect": "deny", "class": 4, "provider": "k8s", "scope": {"context": "prod-eu"}}`
	allow := `{"id": "a", "effect": "allow", "class": 4, "provider": "k8s", "scope": {"context": "dev"}}`
	for name, bad := range map[string]string{
		"unknown class":    `{"id": "b", "effect": "deny", "class": 9, "provider": "k8s"}`,
		"unknown effect":   `{"id": "b", "effect": "maybe", "class": 4, "provider": "k8s"}`,
		"unknown provider": `{"id": "b", "effect": "deny", "class": 4, "provider": "ftp"}`,
		"wrong type":       `{"id": "b", "effect": "deny", "class": "four", "provider": "k8s"}`,
		"repeated id":      `{"id": "d", "effect": "deny", "class": 4, "provider": "k8s"}`,
		"the id refused":   `{"id": "refused", "effect": "deny", "class": 4, "provider": "k8s"}`,
		"empty id":         `{"effect": "deny", "class": 4, "provider": "k8s"}`,
		"class 1":          `{"id": "b", "effect": "deny", "class": 1, "provider": "k8s"}`,
		"class 2":          `{"id": "b", "effect": "deny", "class": 2, "provider": "path"}`,
		"allow of class 5": `{"id": "b", "effect": "allow", "class": 5, "provider": "k8s"}`,
		"class not k8s's":  `{"id": "b", "effect": "deny", "class": 3, "provider": "k8s"}`,
		"verb on another":  `{"id": "b", "effect": "deny", "class": 3, "provider": "net", "verb": "get"}`,
		"namespace on net": `{"id": "b", "effect": "deny", "class": 3, "provider": "net", "scope": {"namespace": "x"}}`,
		"misspelled scope": `{"id": "b", "effect": "allow", "class": 4, "provider": "k8s", "scope": {"context": "dev", "namepsace": "team-a"}}`,
		"misspelled field": `{"id": "b", "effect": "allow", "class": 4, "provider": "k8s", "knd": "pods"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := openFile(t, `{"rules": [`+deny+`, `+allow+`, `+bad+`]}`)
			assert.True(t, s.Held("rules"))
			ids := []string{}
			for _, r := range s.Rules() {
				ids = append(ids, r.ID)
			}
			assert.Equal(t, []string{"d", "refused"}, ids, "the Deny, no Allow, and the class 4 Deny")
			assert.Equal(t, permissions.Refused, s.Rules()[1])
			assert.Len(t, s.Get().Rules, 2, "Get still holds the Allow")
			require.Len(t, s.Refused(), 1)
			assert.Equal(t, "rules", s.Refused()[0].Field)
		})
	}

	s, _ := openFile(t, `{"rules": [`+allow+`]}`)
	for name, bad := range map[string]permissions.Rule{
		"repeated id":      {ID: "a", Effect: permissions.Deny, Class: permissions.UpstreamWrite, Provider: permissions.Kubernetes},
		"the id refused":   {ID: "refused", Effect: permissions.Deny, Class: permissions.UpstreamWrite, Provider: permissions.Kubernetes},
		"class 1":          {ID: "b", Effect: permissions.Deny, Class: permissions.ReadInside, Provider: permissions.Kubernetes},
		"allow of class 5": {ID: "b", Effect: permissions.Allow, Class: permissions.Destructive, Provider: permissions.Kubernetes},
	} {
		err := s.AddRule(bad)
		var r Refusal
		require.ErrorAs(t, err, &r, name)
		assert.Equal(t, "rules", r.Field, name)
		assert.NotEmpty(t, r.Reason, name)
	}
	assert.Len(t, s.Get().Rules, 1, "a refused add writes nothing")
}

func TestAHeldListRefusesItsEdits(t *testing.T) {
	s, file := openFile(t, `{"modes": [{"context": "dev", "mode": "auto"}, {"context": "x", "mode": "yolo"}],
		"rules": [{"id": "a", "effect": "allow", "class": 4, "provider": "k8s"}, {"id": "b", "effect": "deny", "class": 9, "provider": "k8s"}]}`)
	before := readFile(t, file)
	rule := permissions.Rule{ID: "c", Effect: permissions.Deny, Class: permissions.UpstreamWrite, Provider: permissions.Kubernetes}
	for name, err := range map[string]error{
		"set":    s.SetMode("dev", permissions.Ask),
		"clear":  s.ClearMode("dev"),
		"add":    s.AddRule(rule),
		"remove": s.RemoveRule("a"),
	} {
		assert.ErrorIs(t, err, ErrHeld, name)
	}
	assert.Equal(t, before, readFile(t, file), "a refused edit writes nothing")

	require.NoError(t, s.DiscardRefused("modes"))
	assert.False(t, s.Held("modes"))
	mode, source := s.ModeFor("dev")
	assert.Equal(t, permissions.Auto, mode)
	assert.Equal(t, SourceEntry, source)

	require.NoError(t, s.DiscardRefused("rules"))
	assert.Equal(t, []permissions.Rule{{ID: "a", Effect: permissions.Allow, Class: permissions.UpstreamWrite, Provider: permissions.Kubernetes}}, s.Rules())
	assert.JSONEq(t, `{"modes": [{"context": "dev", "mode": "auto"}], "rules": [{"id": "a", "effect": "allow", "class": 4, "provider": "k8s"}], "schemaVersion": 1}`,
		readFile(t, file))
	require.NoError(t, s.AddRule(rule), "the edits work once the hold ends")

	assert.Error(t, s.DiscardRefused("rules"), "a field that is not held")
	assert.Error(t, s.DiscardRefused("defaultMode"), "the default mode is fixed by setting it")
}

func TestAModeSetInSettingsWinsOverAPattern(t *testing.T) {
	s, _ := openFile(t, `{"modes": [{"context": "*", "mode": "auto"}]}`)
	require.NoError(t, s.SetMode("prod-eu", permissions.Ask))
	mode, _ := s.ModeFor("prod-eu")
	assert.Equal(t, permissions.Ask, mode)
	mode, _ = s.ModeFor("dev")
	assert.Equal(t, permissions.Auto, mode)

	require.NoError(t, s.SetMode("prod-eu", permissions.ReadOnly))
	assert.Equal(t, []ContextMode{{Context: "prod-eu", Mode: permissions.ReadOnly}, {Context: "*", Mode: permissions.Auto}}, s.Get().Modes,
		"setting it again replaces the entry")

	require.NoError(t, s.SetMode("dev*", permissions.Ask))
	assert.Equal(t, ContextMode{Context: `dev\*`, Mode: permissions.Ask}, s.Get().Modes[0], "the entry is the context's literal")
	mode, _ = s.ModeFor("dev-eks")
	assert.Equal(t, permissions.Auto, mode, "and names that context alone")

	require.NoError(t, s.ClearMode("prod-eu"))
	mode, _ = s.ModeFor("prod-eu")
	assert.Equal(t, permissions.Auto, mode)
	assert.Error(t, s.SetMode("dev", "yolo"))
}

// Settings offers Clear for a context's own entry alone.
func TestAContextSaysWhetherItsEntryIsItsOwn(t *testing.T) {
	s, _ := openFile(t, `{"modes": [{"context": "dev-eks", "mode": "auto"}, {"context": "*", "mode": "ask"}]}`)
	got := s.ContextModes([]string{"dev-eks", "staging", "prod-eu"})
	assert.True(t, got[0].Own, "an entry naming the context alone")
	assert.False(t, got[1].Own, "a pattern that matches it")
	assert.False(t, got[2].Own)

	require.NoError(t, s.SetMode("prod-eu", permissions.Ask))
	assert.True(t, s.ContextModes([]string{"prod-eu"})[0].Own)
}
