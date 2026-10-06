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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnboardedIsFalseOnAFreshFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "security.json")
	s, err := Open(file)
	require.NoError(t, err)
	assert.False(t, s.Get().Onboarded)

	require.NoError(t, s.FinishOnboarding())
	assert.True(t, s.Get().Onboarded)

	again, err := Open(file)
	require.NoError(t, err)
	assert.True(t, again.Get().Onboarded, "a written true is read back")
}
