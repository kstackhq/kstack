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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckExecutableRefusesEachBadShape(t *testing.T) {
	assert.NoError(t, CheckExecutable("kubectl-foo", "kubectl-foo --version"))
	assert.NoError(t, CheckExecutable("k9s", "k9s version --short"))

	for name, c := range map[string]struct{ name, invocation string }{
		"a name with a slash":          {"bin/tool", "bin/tool --version"},
		"a name starting with -":       {"-tool", "-tool --version"},
		"a name past 64 bytes":         {strings.Repeat("a", 65), strings.Repeat("a", 65)},
		"an empty name":                {"", "--version"},
		"a name with a space":          {"my tool", "my tool"},
		"a name that is a dot":         {".", ". --version"},
		"a name that is two dots":      {"..", ".. --version"},
		"a curated name":               {"kubectl", "kubectl version"},
		"an empty invocation":          {"tool", ""},
		"an invocation of nine fields": {"tool", "tool 1 2 3 4 5 6 7 8"},
		"a first field not the name":   {"tool", "other --version"},
		"a field past 255 bytes":       {"tool", "tool " + strings.Repeat("a", 256)},
		"a field with a control char":  {"tool", "tool --ver\x1bsion"},
		"a field with a DEL":           {"tool", "tool \x7f"},
	} {
		assert.Error(t, CheckExecutable(c.name, c.invocation), name)
	}
}

// An executable the file holds that fails the check, or repeats a name, is read back
// without it, and its reason is kept for Settings.
func TestABadExecutableIsLeftOutWithItsReason(t *testing.T) {
	file := filepath.Join(t.TempDir(), "security.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"executables": [
		{"name": "k9s", "invocation": "k9s version"},
		{"name": "bin/tool", "invocation": "bin/tool"},
		{"name": "helm", "invocation": "helm env"},
		{"name": "k9s", "invocation": "k9s info"}
	]}`), 0o600))

	s, err := Open(file)
	require.NoError(t, err)

	assert.Equal(t, []Executable{{Name: "k9s", Invocation: "k9s version"}}, s.Get().Executables)
	var refused []string
	for _, r := range s.Refused() {
		assert.Equal(t, FieldExecutables, r.Field)
		refused = append(refused, r.Value)
	}
	assert.Equal(t, []string{"bin/tool", "helm", "k9s"}, refused)
}

// An executable is registered with its invocation, or "<name> --version" with none,
// and removed by name; each refusal is in the user's words and writes nothing.
func TestRegisterAndRemoveAnExecutable(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)

	require.NoError(t, s.RegisterExecutable("k9s", ""))
	require.NoError(t, s.RegisterExecutable("kubectl-foo", "kubectl-foo info"))
	assert.Equal(t, []Executable{{Name: "k9s", Invocation: "k9s --version"}, {Name: "kubectl-foo", Invocation: "kubectl-foo info"}}, s.Get().Executables)

	var refusal ExecutableRefusal
	assert.ErrorAs(t, s.RegisterExecutable("k9s", "k9s info"), &refusal, "a name already listed")
	assert.ErrorAs(t, s.RegisterExecutable("kubectl", ""), &refusal, "a curated name")
	assert.ErrorAs(t, s.RegisterExecutable("bad/name", ""), &refusal, "a bad shape")
	assert.ErrorAs(t, s.RemoveExecutable("helm"), &refusal, "a name not registered")
	assert.Len(t, s.Get().Executables, 2)

	require.NoError(t, s.RemoveExecutable("k9s"))
	assert.Equal(t, []Executable{{Name: "kubectl-foo", Invocation: "kubectl-foo info"}}, s.Get().Executables)
}
