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

var platforms = []permissions.Target{permissions.AWS, permissions.GitHub, permissions.GCP, permissions.Azure}

func TestEachCloudPlatformIsOnWhenUnset(t *testing.T) {
	s, _ := openFile(t, "")
	for _, target := range platforms {
		assert.True(t, s.CloudOn(target), target)
	}
}

func TestASwitchTheFileTurnsOffIsOff(t *testing.T) {
	s, _ := openFile(t, `{"sandbox": {"github": false}}`)
	assert.False(t, s.CloudOn(permissions.GitHub))
	assert.True(t, s.CloudOn(permissions.AWS))
}

// A value that is not a boolean is refused and reads as off, since it may
// have been the user's off, and the store keeps it until a SetCloud names it.
func TestARefusedSwitchIsOff(t *testing.T) {
	s, file := openFile(t, `{"sandbox": {"aws": "yes", "github": 1, "gcp": null, "azure": []}}`)
	var fields []string
	for _, r := range s.Refused() {
		fields = append(fields, r.Field)
	}
	assert.ElementsMatch(t, []string{FieldAWS, FieldGitHub, FieldGCP, FieldAzure}, fields)
	for _, target := range platforms {
		assert.False(t, s.CloudOn(target), target)
	}

	require.NoError(t, s.SetCloud(permissions.GitHub, true))
	assert.True(t, s.CloudOn(permissions.GitHub))
	assert.False(t, s.CloudOn(permissions.AWS), "another switch's write keeps the hold")
	assert.Contains(t, readFile(t, file), `"aws": "yes"`)
}

func TestSetCloudWritesItsField(t *testing.T) {
	s, file := openFile(t, "")
	for _, target := range platforms {
		require.NoError(t, s.SetCloud(target, false))
		assert.False(t, s.CloudOn(target))
		_, field := cloudSwitch(&Settings{}, target)
		assert.Contains(t, readFile(t, file), `"`+strings.TrimPrefix(field, "sandbox.")+`": false`)
	}
	require.NoError(t, s.SetCloud(permissions.GitHub, true))
	assert.True(t, s.CloudOn(permissions.GitHub))
	assert.False(t, s.CloudOn(permissions.AWS), "one switch leaves the others")
}

func TestTheClusterHasNoSwitch(t *testing.T) {
	s, _ := openFile(t, "")
	assert.ErrorIs(t, s.SetCloud(permissions.Cluster, true), ErrNoCloudSwitch)
	assert.False(t, s.CloudOn(permissions.Cluster))
}
