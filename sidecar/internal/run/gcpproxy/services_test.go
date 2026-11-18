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

package gcpproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// serviceNamed is the table's row for name.
func serviceNamed(name string) service {
	svc, _ := serviceOf(Host(name))
	return svc
}

func TestEachServiceHasItsRoute(t *testing.T) {
	assert.Equal(t, []string{"compute", "container", "iam", "logging", "storage", "cloudresourcemanager"}, Services())
	for _, name := range Services() {
		svc, ok := serviceOf(Host(name))
		assert.True(t, ok, name)
		assert.Equal(t, name, svc.name)
		assert.Equal(t, name+".googleapis.com", svc.upstream)
	}
}

func TestOnlyARoutesHostNamesAService(t *testing.T) {
	for _, host := range []string{
		"compute.gcp.kstack.invalid:80", "compute.googleapis.com", "evil.compute.gcp.kstack.invalid",
		"secretmanager.gcp.kstack.invalid", "gcp.kstack.invalid", "",
	} {
		_, ok := serviceOf(host)
		assert.False(t, ok, host)
	}
}
