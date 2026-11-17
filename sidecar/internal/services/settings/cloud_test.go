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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

var providers = []permissions.Target{permissions.AWS, permissions.GitHub, permissions.GCP, permissions.Azure}

func TestEachProviderIsOffWhenUnset(t *testing.T) {
	s, _ := openFile(t, "")
	for _, target := range providers {
		assert.False(t, s.CloudOn(target), target)
	}
}

// A value that is not a boolean is refused and reads as off: each field only
// grants.
func TestARefusedSwitchIsOff(t *testing.T) {
	s, _ := openFile(t, `{"sandbox": {"aws": "yes", "github": 1, "gcp": null, "azure": true}}`)
	assert.False(t, s.CloudOn(permissions.AWS))
	assert.False(t, s.CloudOn(permissions.GitHub))
	assert.False(t, s.CloudOn(permissions.GCP))
	assert.True(t, s.CloudOn(permissions.Azure))
	var fields []string
	for _, r := range s.Refused() {
		fields = append(fields, r.Field)
	}
	assert.ElementsMatch(t, []string{FieldAWS, FieldGitHub, FieldGCP}, fields)
}

func TestSetCloudWritesItsField(t *testing.T) {
	s, file := openFile(t, "")
	for _, target := range providers {
		require.NoError(t, s.SetCloud(target, true))
		assert.True(t, s.CloudOn(target))
		_, field := cloudSwitch(&Settings{}, target)
		assert.Contains(t, readFile(t, file), `"`+strings.TrimPrefix(field, "sandbox.")+`": true`)
	}
	require.NoError(t, s.SetCloud(permissions.GitHub, false))
	assert.False(t, s.CloudOn(permissions.GitHub))
	assert.True(t, s.CloudOn(permissions.AWS), "one switch leaves the others")
}

func TestTheClusterHasNoSwitch(t *testing.T) {
	s, _ := openFile(t, "")
	assert.ErrorIs(t, s.SetCloud(permissions.Cluster, true), ErrNoCloudSwitch)
	assert.False(t, s.CloudOn(permissions.Cluster))
}
