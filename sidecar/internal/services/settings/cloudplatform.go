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
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// ErrNoCloudPlatformSwitch is a switch asked of a target that has none: the
// cluster.
var ErrNoCloudPlatformSwitch = errors.New("settings: no switch for this target")

// ErrNoSandboxSettings is sandbox_settings with no row. The migration writes
// it, so a read that finds none failed.
var ErrNoSandboxSettings = errors.New("settings: sandbox_settings has no row")

// CloudPlatformSwitches is each cloud platform's switch: on, a sandboxed
// command reaches it as the user.
type CloudPlatformSwitches struct {
	AWS, GitHub, GCP, Azure bool
}

// On is target's switch; false for a target with none.
func (sw CloudPlatformSwitches) On(target permissions.Target) bool {
	switch target {
	case permissions.AWS:
		return sw.AWS
	case permissions.GitHub:
		return sw.GitHub
	case permissions.GCP:
		return sw.GCP
	case permissions.Azure:
		return sw.Azure
	}
	return false
}

// CloudPlatformSwitches reads every switch from sandbox_settings' one row.
func (s *Store) CloudPlatformSwitches(ctx context.Context) (CloudPlatformSwitches, error) {
	var sw CloudPlatformSwitches
	err := s.stmts.Stmts().QueryRow(ctx, stmtSelectCloudPlatformSwitches).Scan(&sw.AWS, &sw.GitHub, &sw.GCP, &sw.Azure)
	if errors.Is(err, sql.ErrNoRows) {
		return CloudPlatformSwitches{}, ErrNoSandboxSettings
	}
	if err != nil {
		return CloudPlatformSwitches{}, fmt.Errorf("select cloud platform switches: %w", err)
	}
	return sw, nil
}

// switchUpdates is each cloud platform's statement writing its switch: a
// prepared statement cannot take a column as a parameter.
var switchUpdates = map[permissions.Target]stmtID{
	permissions.AWS:    stmtUpdateAWSSwitch,
	permissions.GitHub: stmtUpdateGitHubSwitch,
	permissions.GCP:    stmtUpdateGCPSwitch,
	permissions.Azure:  stmtUpdateAzureSwitch,
}

// SetCloudPlatformSwitch turns target's switch on or off. It updates the one
// row and never inserts it: an insert would write the other switches on.
func (s *Store) SetCloudPlatformSwitch(ctx context.Context, target permissions.Target, on bool) error {
	stmt, ok := switchUpdates[target]
	if !ok {
		return ErrNoCloudPlatformSwitch
	}
	if err := execOne(ctx, s.stmts.Stmts(), stmt, ErrNoSandboxSettings, on, s.now().UnixMilli()); err != nil {
		return fmt.Errorf("write cloud platform switch: %w", err)
	}
	return nil
}
