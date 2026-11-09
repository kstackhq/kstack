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

package logstore

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate/sqlitemigratetest"
)

// shippedMigrations is every file in migrations/ by SHA-256. A new migration adds its
// line in the PR that writes it. Once a desktop release carries a migration, every
// installed log cache has run it, so its line never changes again.
var shippedMigrations = map[string]string{
	"0001_init.sql": "0fc4793c07525ac1b371fbbac1540fddb7d7e5a9ad90e3dd711a13436e86e85a", // desktop/v0.0.1
}

func TestAShippedMigrationIsNeverEdited(t *testing.T) {
	t.Parallel()
	assert.Equal(t, shippedMigrations, sqlitemigratetest.Checksums(t, migrationsFS, "migrations"))
}
