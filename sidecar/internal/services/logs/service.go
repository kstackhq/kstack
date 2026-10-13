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

package logs

import (
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// Service reads views' lines off the clusters the cluster service connects to.
type Service struct {
	clusters cluster.Service
	// budget is the bytes one fetch may read.
	budget int64
}

// New is the service over the cluster service.
func New(clusters cluster.Service) *Service {
	return &Service{clusters: clusters, budget: maxBudget}
}
