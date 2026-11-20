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

package providercredentials

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/oskeyring/oskeyringtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// An account name is a persisted format: this table changes only with a
// migration.
func TestTheAccountNames(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{
		"anthropic":   "anthropic-api-key",
		"openai":      "openai-api-key",
		"gemini":      "gemini-api-key",
		"groq":        "groq-api-key",
		"mistral":     "mistral-api-key",
		"deepseek":    "deepseek-api-key",
		"xai":         "xai-api-key",
		"openrouter":  "openrouter-saved-api-key",
		"together":    "together-api-key",
		"fireworks":   "fireworks-api-key",
		"opencode-go": "opencode-go-api-key",
	} {
		assert.Equal(t, want, account(id), id)
	}
}

func TestNewRestoresWhatTheKeyringHolds(t *testing.T) {
	t.Parallel()
	const key = "restored-key-0123456789abcdef"
	k := oskeyringtest.New()
	require.NoError(t, k.Set(account(twinID), `{"v":1,"key":"`+key+`"}`))
	s := newService(t, Config{Keyring: k})
	st := state(t, s, twinID)
	assert.Equal(t, SourceSavedKey, st.Source)
	assert.True(t, st.HasSavedKey)
	assert.True(t, safe.HasSecret(key), "a restored key is registered with the redactor")
	assert.Equal(t, SourceNone, state(t, s, routerID).Source)
}

func TestANewerEntryIsLeft(t *testing.T) {
	t.Parallel()
	const entry = `{"v":2,"key":"newer-key-0123456789abcdef"}`
	k := oskeyringtest.New()
	require.NoError(t, k.Set(account(twinID), entry))
	s := newService(t, Config{Keyring: k})
	assert.False(t, state(t, s, twinID).HasSavedKey)
	assert.Equal(t, entry, k.Entries()[account(twinID)])
}

func TestAnEntryThatDoesNotParseIsDeleted(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{
		`not json`,
		`{"v":0,"key":"k-0123456789abcdef"}`,
		`{"v":1,"key":""}`,
		`{"v":1,"key":"too-short"}`,
		`{"v":1,"key":"k-0123456789\nabcdef"}`,
	} {
		k := oskeyringtest.New()
		require.NoError(t, k.Set(account(twinID), entry))
		s := newService(t, Config{Keyring: k})
		assert.False(t, state(t, s, twinID).HasSavedKey, entry)
		assert.Empty(t, k.Entries(), entry)
	}
}

func TestAnUnreadableKeyringWritesNothing(t *testing.T) {
	t.Parallel()
	k := oskeyringtest.New()
	require.NoError(t, k.Set(account(twinID), `not json`))
	k.FailGet = errors.New("locked")
	s := newService(t, Config{Keyring: k})
	assert.False(t, state(t, s, twinID).HasSavedKey)
	assert.Equal(t, map[string]string{account(twinID): `not json`}, k.Entries())
}

// An unparsable entry the keyring will not delete is still no key.
func TestAnUndeletableBadEntryIsNoKey(t *testing.T) {
	t.Parallel()
	k := oskeyringtest.New()
	require.NoError(t, k.Set(account(twinID), `not json`))
	k.FailDelete = errors.New("locked")
	s := newService(t, Config{Keyring: k})
	assert.False(t, state(t, s, twinID).HasSavedKey)
}
