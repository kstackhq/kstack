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

//go:build unix

package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/services/awsauth"
	"github.com/kstackhq/kstack/sidecar/internal/services/kubeconfig"
)

// The build's cloud platforms, in Settings' order.
func TestTheBuildsCloudPlatforms(t *testing.T) {
	assert.Equal(t, []permissions.Target{
		permissions.AWS, permissions.GitHub, permissions.GCP, permissions.Azure,
	}, cloudTargets(builtCloudPlatforms(nil)))
}

// AWS's logins read a kube-context's profile off the kubeconfig, none before
// it is read, and hand out one login per profile.
func TestAWSLoginsJoinTheKubeconfigAndTheLogins(t *testing.T) {
	logins := awsLogins{kubeconfig: kubeconfig.New(filepath.Join(t.TempDir(), "config"), nil), logins: awsauth.New()}
	assert.Empty(t, logins.Profile("eks"))
	assert.Same(t, logins.Login("prod"), logins.Login("prod"))
	assert.NotSame(t, logins.Login("prod"), logins.Login(""))
}
