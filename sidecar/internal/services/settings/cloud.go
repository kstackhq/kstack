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
	"errors"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// The consent switches, one per cloud platform a sandboxed command may reach.
// Each only grants, so a refused value reads as off.
const (
	FieldAWS    = "sandbox.aws"
	FieldGitHub = "sandbox.github"
	FieldGCP    = "sandbox.gcp"
	FieldAzure  = "sandbox.azure"
)

// ErrNoCloudSwitch is a switch asked of a target that has none: the cluster.
var ErrNoCloudSwitch = errors.New("settings: no switch for this target")

// cloudSwitch is target's switch in v and the field it is stored under; nil
// for a target with none.
func cloudSwitch(v *Settings, target permissions.Target) (*bool, string) {
	switch target {
	case permissions.AWS:
		return &v.Sandbox.AWS, FieldAWS
	case permissions.GitHub:
		return &v.Sandbox.GitHub, FieldGitHub
	case permissions.GCP:
		return &v.Sandbox.GCP, FieldGCP
	case permissions.Azure:
		return &v.Sandbox.Azure, FieldAzure
	}
	return nil, ""
}

// CloudOn is whether the user turned target's switch on. Off by default: a
// login on the machine is not consent.
func (s *Store) CloudOn(target permissions.Target) bool {
	v := s.Get()
	sw, _ := cloudSwitch(&v, target)
	return sw != nil && *sw
}

// SetCloud turns target's switch on or off, naming the field it writes.
func (s *Store) SetCloud(target permissions.Target, on bool) error {
	_, field := cloudSwitch(&Settings{}, target)
	if field == "" {
		return ErrNoCloudSwitch
	}
	return s.Update(func(v *Settings) error {
		sw, _ := cloudSwitch(v, target)
		*sw = on
		return nil
	}, field)
}
