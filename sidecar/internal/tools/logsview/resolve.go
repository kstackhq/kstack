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

package logsview

import (
	"context"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// resolve is the view the call opens: its sources checked against the mirror of
// clusterID, its containers defaulted, its anchor on the sidecar's clock. Not
// built yet: every call is refused.
func (t *Tool) resolve(context.Context, cluster.ClusterID, input) (tools.LogsViewAction, error) {
	return tools.LogsViewAction{}, errNotImplemented
}
